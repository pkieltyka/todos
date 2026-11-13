---
status: complete
---

# Service, quick add and config

Packages: internal/service, internal/quickadd, internal/config and internal/service/servicetest.
[Architecture](../architecture.md) is authoritative; types are in [domain.md](domain.md).

## Package boundaries

| Package | Allowed project imports | Owns |
|---|---|---|
| service | domain, quickadd | Use cases, metadata memoisation, resolution, validation, partial changes |
| quickadd | domain | Pure parser and remaining-source reconstruction |
| config | domain | Versioned config struct, atomic saves and user-authored drafts |
| service/servicetest | service, domain | Shared deterministic Service fake for CLI and TUI tests |

- service never imports github, config, CLI or TUI. Its dependencies are interfaces.
- Synchronous/context-aware methods; RateLimit is a nonblocking memory snapshot.
- No cached issues, PRs, comments or board contents. No issue locks, global semaphore, file locks or cross-process conflict detection.
- The caller controls concurrency: TUI serialises one issue's changes and allows four issue queues; CLI runs one operation.

## Service interface

Copy this exact architecture4 seam into service.go; no additional face-specific API.

```go
type Service interface {
    // session and metadata
    Session(context.Context) (domain.Session, error)
    RefreshMetadata(context.Context) error
    RateLimit() domain.RateLimit
    Labels(context.Context) ([]domain.Label, error)
    AssignableUsers(context.Context) ([]domain.User, error)

    // reads, one page per call
    ListIssues(context.Context, domain.IssueFilter, domain.PageRequest) (domain.Page[domain.Issue], error)
    GetIssue(context.Context, int) (domain.IssueDetail, error)
    ListIssueComments(context.Context, int, domain.PageRequest) (domain.Page[domain.Comment], error)
    ListPRs(context.Context, domain.PRFilter, domain.PageRequest) (domain.Page[domain.PullRequest], error)
    GetPR(context.Context, int) (domain.PRDetail, error)
    ListPRComments(context.Context, int, domain.PageRequest) (domain.Page[domain.Comment], error)

    // board
    BoardInfo(context.Context) (domain.BoardInfo, error)
    BoardCandidates(context.Context) (domain.ProjectsResult, error)
    UseBoard(context.Context, domain.ProjectRef) (domain.Project, error)
    Board(context.Context) (domain.Board, error)

    // changes
    CreateIssue(context.Context, domain.NewIssue) (domain.Change, error)
    EditIssue(context.Context, int, domain.EditIssue) (domain.Change, error)
    UpdateAssignees(ctx context.Context, number int, add, remove []string) (domain.Change, error)
    Reassign(ctx context.Context, number int, logins []string) (domain.Change, error)
    UpdateLabels(ctx context.Context, number int, add, remove []string) (domain.Change, error)
    Close(context.Context, int, domain.CloseOptions) (domain.Change, error)
    Reopen(context.Context, int) (domain.Change, error)
    Comment(ctx context.Context, number int, body string) (domain.CommentResult, error)
    Delete(context.Context, int) error
    SetStatus(ctx context.Context, number int, status *string) (domain.Change, error)
    BoardAdd(ctx context.Context, number int, status *string) (domain.Change, error)
    RemoveFromBoard(context.Context, int) (domain.Change, error)

    // quick add
    PlanQuickAdd(context.Context, string, domain.QuickAddOptions) (domain.QuickAddPlan, error)
    RunQuickAdd(context.Context, domain.QuickAddPlan, domain.QuickAddRunOptions) (domain.QuickAddResult, error)

    Doctor(context.Context) (domain.DoctorReport, error)
}
```

## Construction and metadata state

```go
type Options struct {
    ResolveRepository func(context.Context) (domain.RepoRef, error)
    Project string
    Now func() time.Time
    Wait func(context.Context, time.Duration) error // real timer; fake advances immediately
}
type metadata[T any] struct { Value T; ExpiresAt time.Time; Valid bool }
type core struct {
    backend Backend
    settings SettingsStore
    opts Options
    mu sync.Mutex // protects metadata only, never held during network I/O
    session metadata[domain.Session]
    labels metadata[[]domain.Label]
    users metadata[[]domain.User]
    board metadata[domain.BoardInfo]
    selection *domain.ProjectRef
    generation uint64
}
func New(Backend, SettingsStore, Options) Service
func (s *core) resolveBoard(context.Context) (domain.BoardInfo, error)
func (s *core) validateNames(ctx context.Context, assignees, labels []string, status *string, addToBoard bool) (domain.CreateInput, *domain.BoardInfo, error)
func (s *core) target(ctx context.Context, number int, boardID domain.NodeID) (domain.IssueDetail, error)
func partial(cause error, issue *domain.Issue, completed []string) *domain.Error
func ParseIssueRef(text string, repo domain.RepoRef, pullRequest bool) (int, error)
func ParseProjectRef(text string, repo domain.RepoRef) (domain.ProjectRef, error)
```

- New performs no network/filesystem work. CLI injects one lazy cached repository resolver.
- Session memoises viewer and canonical repo for the process. It returns IssuesEnabled=false as data so PR/Board tabs remain reachable.
- Issue-use-case methods check enabled issues and return issues_disabled when necessary.
- Labels, assignable users and board metadata expire after60 seconds; do not cache failures.
- Check metadata under mu, release before a fill, publish only if its generation is still current; duplicate concurrent metadata fills are acceptable.
- Return deep copies. Snapshot selected board once per use case; never switch target board mid-write.
- RefreshMetadata invalidates memoised metadata/session, calls backend.RefreshCredentials, then Session; a failed operation is never replayed.
- Unknown name: refresh that metadata once before invalid_input. No automatic query/mutation retries.
- Match assignees case-insensitively after expanding @me; deduplicate before the10-assignee check.
- Removals resolve against the fresh issue first: use its AssigneeIDs (canonical login keys) or Label.NodeID, including departed users/deleted labels. Never resolve a present removal ID through repository metadata. For absent names only, validate against repository metadata (refresh once on unknown): known means no-op, unknown means invalid_input. Validate the entire picker delta before any write; additions retain full repository validation.
- Labels must already exist, matching exact names. Status exact name first, then unique EqualFold match.
- Reject ambiguous option matches, add/remove overlap, blank title, invalid UTF8 and title>256 code points before writing.
- Status!=nil implies AddToBoard=true; AddToBoard=true with nil Status is a real No status placement.

## Backend interface

Implemented by *github.Client without a github -> service import. Wiring checks interface conformance.

```go
type Backend interface {
    Credentials(context.Context) (domain.CredentialInfo, error)
    DoctorScopes(context.Context) (domain.CredentialInfo, error)
    Viewer(context.Context) (domain.User, error)
    Repository(context.Context, domain.RepoRef) (domain.Repository, error)
    ListLabels(context.Context, domain.RepoRef, domain.PageRequest) (domain.Page[domain.Label], error)
    ListAssignableUsers(context.Context, domain.RepoRef, domain.PageRequest) (domain.Page[domain.User], error)
    ListIssues(context.Context, domain.RepoRef, domain.IssueFilter, domain.PageRequest, domain.NodeID) (domain.Page[domain.Issue], error)
    Issue(context.Context, domain.RepoRef, int, domain.NodeID) (domain.IssueDetail, error)
    IssueComments(context.Context, domain.RepoRef, int, domain.PageRequest) (domain.Page[domain.Comment], error)
    ListPRs(context.Context, domain.RepoRef, domain.PRFilter, domain.PageRequest) (domain.Page[domain.PullRequest], error)
    PR(context.Context, domain.RepoRef, int) (domain.PRDetail, error)
    PRComments(context.Context, domain.RepoRef, int, domain.PageRequest) (domain.Page[domain.Comment], error)
    LinkedProjects(context.Context, domain.RepoRef, domain.PageRequest) (domain.Page[domain.Project], error)
    OwnerProjects(ctx context.Context, owner string, page domain.PageRequest) (domain.Page[domain.Project], error)
    Project(context.Context, domain.ProjectRef) (domain.Project, error)
    ProjectFields(context.Context, domain.NodeID) (domain.BoardInfo, error)
    ProjectItems(context.Context, domain.NodeID, domain.PageRequest) (domain.Page[domain.ProjectItem], error)
    IssuePlacements(context.Context, domain.NodeID) ([]domain.ProjectItem, error)
    CreateIssue(context.Context, domain.CreateInput) (domain.Issue, error)
    UpdateIssue(context.Context, domain.NodeID, domain.IssuePatch) (domain.Issue, error)
    AddAssignees(context.Context, domain.NodeID, []domain.NodeID) (domain.Issue, error)
    RemoveAssignees(context.Context, domain.NodeID, []domain.NodeID) (domain.Issue, error)
    AddLabels(context.Context, domain.NodeID, []domain.NodeID) (domain.Issue, error)
    RemoveLabels(context.Context, domain.NodeID, []domain.NodeID) (domain.Issue, error)
    AddComment(ctx context.Context, issueID domain.NodeID, body string) (domain.CommentResult, error)
    CloseIssue(ctx context.Context, issueID domain.NodeID, reason string) (domain.Issue, error)
    ReopenIssue(context.Context, domain.NodeID) (domain.Issue, error)
    DeleteIssue(context.Context, domain.NodeID) error
    AddProjectItem(ctx context.Context, projectID, issueID domain.NodeID) (domain.Placement, error)
    SetProjectStatus(ctx context.Context, projectID, itemID, fieldID domain.NodeID, optionID string) error
    ClearProjectStatus(ctx context.Context, projectID, itemID, fieldID domain.NodeID) error
    RemoveProjectItem(ctx context.Context, projectID, itemID domain.NodeID) error
    UnarchiveProjectItem(ctx context.Context, projectID, itemID domain.NodeID) (domain.Placement, error)
    RateLimit() domain.RateLimit
    RefreshCredentials(context.Context) error
}
```

- selected-project NodeID parameters on issue reads are empty to omit optional board membership.
- IssuePlacements is first100 once, including archived; never follows membership cursors.
- ListLabels/ListAssignableUsers/LinkedProjects/OwnerProjects/ProjectItems each return one page.
- ProjectFields may complete its own field connection; PR completes its closing references.
- Create/Update return acknowledged Issue summaries. Placement-only writes return placement or error; service combines that with its fresh target summary.
- DoctorScopes alone uses REST GET /user; Credentials exposes only local token-source information.

## Repository and reference resolution

The CLI resolver, injected through Options.ResolveRepository, applies exactly:

1. Explicit --repo OWNER/REPO via go-gh repository.Parse.
2. GH_REPO via go-gh repository.Parse.
3. repository.Current(), unchanged. No hand-written remote sorting, SSH parsing or environment mutation.

- Logical host is retained; github.com is guaranteed, Enterprise best effort.
- Invalid explicit reference is invalid_input; no default available is no_repo.
- Canonical name/ID comes from Backend.Repository; use lowercase host/owner/name for local keys.
- Issue/PR arguments: positive number or #number, or HTTPS URL with the matching host/repo and /issues/N or /pull/N respectively.
- Reject wrong repository, host or issue/PR URL kind as invalid_input before a mutation. Ignore an optional fragment.
- Project reference accepts positive NUMBER (repo owner), OWNER/NUMBER, or /users/OWNER/projects/N or /orgs/OWNER/projects/N URL.

## Board resolution and reads

1. Explicit Options.Project, then successful UseBoard session choice, then remembered board, then linked projects.
2. Explicit/remembered inaccessible or unusable board does not fall through to another board.
3. Linked projects: none -> no_board; one -> validate it; several -> no_board with Candidates for TUI picker.
4. Validate exactly one single-select field named Status; wrong/missing type -> no_board. Preserve option order.
5. BoardCandidates expands linked projects, repository-owner projects, then viewer-owned projects, deduplicating node IDs while retaining that grouping.
6. Fill usable on every candidate from fields; selected is actual resolution or nil. Candidate listing itself does not require a selected board.
7. UseBoard validates before RememberBoard; only after persistence succeeds does it replace the active selection/generation. This explicit action supersedes the initial --project choice for the rest of that run.

- no_board Candidates are empty for no linked board, all linked candidates for ambiguity, or exactly the selected unusable project for missing/wrong-typed Status. This is the agreed TUI discriminator.
- CloseOptions.Reason uses completed/not_planned internally; CLI maps not-planned before invoking the service.

Board algorithm:

- Read pages of100 nonarchived project items until exhausted or1000 scanned.
- Match current repository by node ID. PR/draft/foreign/redacted content increments HiddenCount once.
- Archived items do not appear or contribute to HiddenCount; reads never restore them.
- Group current-repo issues by Status option in returned project-position order.
- Add a leading No status column only when nonempty; named columns stay in option order, including empty columns.
- Populate Card.Issue.Board from that card's placement; do not perform one IssueView request per card.
- Truncated is true if more than1000 nonarchived items exist. Set VisibleCount and CardsTruncated when more than500 cards are available; return all read cards to CLI, TUI draws first500.
- Board-dependent errors never fail ListIssues/GetIssue: omit placements. Real issue-data auth/network errors still fail.
- Issue reads use at most100 memberships; absence in that prefix is treated as absent under the v1 bound.

## Reads and pagination

| Method | Algorithm |
|---|---|
| ListIssues | Validate filter/page; expand @me; resolve optional board best effort; one outer issue/search page |
| GetIssue | Fresh body+summary, best-effort board; no comments |
| ListIssueComments / ListPRComments | One oldest-first connection page; caller caps500 |
| ListPRs | One native PR page, updated-desc; State only, no author Search |
| GetPR | Body, rollups, mergeable, complete same-repo closing refs; comments separate |
| Labels / AssignableUsers | Complete pageable repository metadata then memoise60s |

- Page size0 means100; otherwise1..100. Pass GitHub cursor unchanged.
- Caller must repeat filters and clear the cursor when filters change; service does not validate cursor ownership.
- CLI loops until limit/default50 or HasMore=false. --all has no list cap except Search's1000 ceiling.
- Search.ProviderTruncated and Total>1000 with --all become invalid_input in CLI before successful output; bounded output remains truncated.
- TUI issue mine uses Assignee=@me server-side. PR mine is a local author comparison; board mine is local.
- Lists are not snapshots under concurrent GitHub edits; faces deduplicate node IDs, preserving first-seen row order.

## Mutation algorithms

Common order: Session -> feature checks -> validate additions/statuses -> fresh target by number -> resolve removals from issue, validating only absent names against repository metadata -> no-op/diff -> requested writes.
No per-issue lock or service-wide semaphore is introduced. TUI queues provide ordering.

| Service operation | Writes and success rules |
|---|---|
| CreateIssue | Validate entire input first; CreateIssue with IDs; verify assignments; if AddToBoard, add then set/clear requested status. Ordinary nil Status + AddToBoard=false does not place. |
| EditIssue | At least one pointer set; send only supplied title/body. Compare current values for Changed=false. TUI owns best-effort title/body conflict check. |
| UpdateAssignees | Resolve removal IDs from the issue itself (not the assignable-user list); absent but repository-known removals are no-ops. Remove present requested removals, then add absent requested additions; validate final unique count<=10. Verify returned memberships. One call per picker apply. |
| Reassign | Replace assigneeIds only in UpdateIssue; verify final exact set. Empty list is allowed internally to remove all, although CLI requires login(s). |
| UpdateLabels | Resolve present removal IDs from Issue.Labels; validate only absent removal names against repository labels. Remove then add delta mutations, never replace unrelated issue fields or create labels. |
| Comment | Reject blank body; AddComment once; return summary and new comment. |
| Close | Already closed -> Changed=false, no comment. Otherwise optional Comment, then CloseIssue with completed/not-planned. |
| Reopen | Already open -> Changed=false; otherwise ReopenIssue. |
| Delete | Fresh ID then DeleteIssue; faces enforce confirmation. Do not infer success from an ambiguous request's later404. |
| SetStatus | Validate option or clear, find membership including archived, restore/add if needed, then set/clear. Existing same value -> no-op. |
| BoardAdd | With status delegates SetStatus. Nil preserves existing active status; archived restores its existing status; genuinely new card is cleared if a project workflow gave it a default. |
| RemoveFromBoard | Remove the selected membership, active or archived; absent is a no-op. |

- `todos unassign ISSUE` reads the current names and passes them as removals; the service freshly reads their issue-owned IDs, so an empty assignable-user list never blocks removing current assignees.
- All membership lookups stop at100; use node/item/field/option IDs inside Backend.
- Restoring archived membership uses UnarchiveProjectItem; confirmed restoration records added_to_board.
- Adding missing membership records added_to_board; then status failure is partial, no rollback.
- Assignments silently ignored by GitHub are forbidden. If creation already succeeded, return partial(Cause=forbidden, Completed=[created]).
- If a picker removal succeeded and its addition failed, return partial with internal Completed=[removed] and best-known Issue. CLI commands never issue both deltas in one call, so removed cannot leak to public CLI errors.
- Other public Completed names are only created/commented/added_to_board. Never invent closed/labeled/assigned step names.
- Partial copies underlying Uncertain/Err/Cause, carries acknowledged Issue, and preserves context cancellation through Unwrap.
- A first ambiguous create without confirmed step returns underlying timeout/network with Uncertain; RunQuickAdd elevates it to partial per its special contract.
- No-op result has Changed=false and current summary. The TUI uses an acknowledged write result without another read; only uncertain outcomes require a reconciliation read. A later scheduled refresh cannot turn an acknowledged write into rejection.

## Pure quick-add parser

```go
// internal/quickadd
func Parse(text string) domain.QuickAddPlan
func Remaining(plan domain.QuickAddPlan, result domain.QuickAddResult) (string, []domain.DraftSpan)
```

1. Reject invalid UTF8. Scan physical lines without bufio.Scanner's64KiB default; retain original bytes and half-open spans.
2. Parse CRLF as LF semantically, keeping exact Source bytes. Do not dedent a whole pasted block.
3. Nonblank line with>=2 spaces or one leading tab is body continuation. Remove exactly two spaces OR one tab; preserve remaining indentation and nested bullets.
4. Indented nonblank before first item is a line problem. Exactly one leading space is an invalid-indentation problem.
5. Blank lines are pending: append to Body only if followed by a continuation; between items retain only in Source.
6. Unindented nonblank starts an item. Recognise checkbox before generic bullet, then numbered prefix, then heading prefix.

| Prefix | Accepted form |
|---|---|
| Checkbox | -, *, + then whitespace and [ ]/[x]/[X], followed by whitespace/end; x means checked |
| Bullet | -, *, + followed by whitespace/end |
| Number | Digits then . or ) followed by whitespace/end; sequence need not be consecutive |
| Heading | One to six # followed by whitespace/end; #123 stays literal |
| Plain | Everything else; @name/#123 are literal, no per-item metadata shortcuts |

7. Remove only one recognised prefix; trim title edges. Empty title or>256 code points is a line problem. Preserve duplicate titles and body whitespace.
8. Exterior blank Body lines are removed; source spans include item and following separators.
9. Count all items including skipped checked ones; zero or>50 is invalid_input. Collect all syntax problems before API validation.
10. Remaining concatenates original Source for failed/uncertain/unattempted rows, removes created/skipped, and rebuilds uncertainty offsets from that concatenation.

## Planning and execution

PlanQuickAdd:

- Parse first; return Problems data without writes. Authentication/network errors remain errors.
- Validate shared assignees/labels/status before any write; refresh unknown names once.
- Canonicalise Checked empty to skip; invalid mode is invalid_input (CLI rejects as usage first).
- Status!=nil implies AddToBoard; checked items ignore AddToBoard entirely.
- Estimate ToCreate/ToSkip/WriteCount: bare open1, checked-close2, open board add up to3.
- Run re-parses Source and revalidates options; do not trust mutable/stale Plan.Items supplied by a caller.

RunQuickAdd:

1. Initialise every item as skipped (checked+skip) or unattempted. DryRun changes would-create to planned, sets Planned count and emits no mutations.
2. Process sequentially in source order. Before each item check StopRequested/context; no parallel item creation.
3. Emit creating; synchronous OnProgress must succeed before CreateIssue starts. TUI persists InFlight here.
4. Pace only this run's writes: first immediate, each later write starts>=1 second after previous write completed. Use injected Now/Wait, including add/set/clear/restore/close writes.
5. Invoke a private run-local Backend decorator that wraps ONLY mutating Backend methods in that pace rule; construct a run-local core sharing read-only metadata snapshots/settings but no pacer in the main core. Helpers never install global state.
6. After create, checked-close emits closing and closes completed; open+AddToBoard emits placing and adds/sets/clears. Checked items never get board placement.
7. Retain known Issue immediately after create. On later failure result stays created, with Error and actual Closed/OnBoard flags. Stop the batch.
8. Emit settled once per completed/failed item with owned ItemResult; callback removes acknowledged-created source, keeps uncertain/unattempted text and clears InFlight only when settled.
9. Callback failure is config; stop before next write. If anything was acknowledged, return partial with original callback cause and current Issue.
10. A pre-start failure is failed, an ambiguous create is uncertain; a known create followed by ambiguous close/place remains created with Error.Uncertain=true.
11. StopRequested finishes the whole active item before stopping. Context cancellation interrupts promptly; later rows remain unattempted. Never write after Run returns.
12. First failure stops; no rollback/retry. Derive all six counts from final rows once. Any created or uncertain row -> partial7; otherwise return the first failure kind. Intentional stop wraps context.Canceled for caller policy.

- Progress indexes include skipped rows. Emit settled for skipped rows without writing so every row can be drawn.
- OnProgress never calls a mutating Service recursively; it persists and sends an immutable TUI message.
- Pacer is local to RunQuickAdd. Concurrent ordinary actions have no imposed pacing or global semaphore.
- Create each item through the run-local core with AddToBoard=false and Status=nil; then call Close or BoardAdd/SetStatus as the separate planned step. This makes creating/closing/placing callbacks precede their actual writes and keeps an acknowledged number available before any later failure.
- Sequential creation preserves order but other GitHub users/PRs can interleave numbers.
- GitHub generally allows80 content writes/minute and500/hour; pacing is mitigation, not a quota guarantee. Stop on rate_limited, expose retry timing.

## Read-only doctor

Always return a full six-row DoctorReport, even if early remote checks fail.

| Order | Check | Pass evidence |
|---|---|---|
| 1 | Credentials | Backend.Credentials found local source; no token in output |
| 2 | Viewer | Backend.Viewer succeeded; DoctorScopes may add informational scopes |
| 3 | Repository | Exists, issues enabled, viewerCanCreateIssues=true |
| 4 | Board | Resolution succeeded with valid Status metadata |
| 5 | Board access | Item read succeeded and Project.viewerCanUpdate=true |
| 6 | Config | SettingsStore.Check says readable/valid or absent |

- Dependent inaccessible checks become failed rows with blocked-by-prior-check detail and actionable hint; still run config check.
- OK and exit status depend ONLY on checks1..3. Board/config problems do not change the exit code.
- FirstFailure holds first failing check1..3, otherwise nil. Report and error are both returned on failure.
- Never create, edit, close, add, restore or delete to test permissions. Capabilities are reports, not guarantees.
- CLI prints this report on stdout in all modes/verdicts; it does not replace it with a generic error envelope.

## SettingsStore and config implementation

```go
// Defined in service; config implements structurally.
type SettingsStore interface {
    RememberedBoard(context.Context, domain.RepoRef) (*domain.ProjectRef, error)
    RememberBoard(context.Context, domain.RepoRef, domain.ProjectRef) error
    Check(context.Context) (domain.DoctorCheck, error)
}
// internal/config
type Paths struct { ConfigFile, DraftDir string }
type File struct {
    Version int `yaml:"version"`
    Boards map[string]BoardChoice `yaml:"boards"`
    Extra map[string]any `yaml:",inline"`
}
type BoardChoice struct { Owner string `yaml:"owner"`; Number int `yaml:"number"` }
type Store struct {
    paths Paths
    mu sync.Mutex
    warning *domain.Error
}
func DefaultPaths(getenv func(string) string, userHomeDir func() (string,error)) (Paths,error)
func New(paths Paths) *Store
func (s *Store) RememberedBoard(context.Context, domain.RepoRef) (*domain.ProjectRef,error)
func (s *Store) RememberBoard(context.Context, domain.RepoRef, domain.ProjectRef) error
func (s *Store) Check(context.Context) (domain.DoctorCheck,error)
func (s *Store) Warning() *domain.Error
func atomicWrite(path string, data []byte) error
```

- Absolute XDG_CONFIG_HOME/todos/config.yml else ~/.config/todos/config.yml, on Linux and macOS.
- Absolute XDG_STATE_HOME/todos/drafts else ~/.local/state/todos/drafts. Relative/empty XDG falls back.
- Missing config is empty, not an error. Unsupported version, duplicate YAML keys, unreadable/malformed content -> empty+warning.
- New reads the config once for its initial warning/check state; construction is deferred until a command actually needs service wiring. RememberBoard and Check re-read current bytes.
- Read errors do not prevent ordinary work. RememberedBoard returns nil,nil for warning cases; Warning/Check preserve the diagnosis.
- RememberBoard re-reads current file, refuses any unreadable/unparseable input, modifies one key, encodes the versioned struct and its inline Extra map.
- Preserve unknown top-level keys via Extra, but no yaml.Node tree, formatting preservation, locks or conflict detection.
- Atomic save: ensure dirs0700; create temp in target directory0600; write, fsync, close, rename, fsync parent; remove temp on failure. Never truncate original first.
- Writes in separate processes are last save wins. A private mu protects one Store's in-memory warning/state only.
- CLI prints Warning on stderr only in text mode. JSON is silent; doctor exposes the config row.

```yaml
version: 1
boards:
  github.com/pkieltyka/playg:
    owner: pkieltyka
    number: 1
```

## DraftStore implementation

The interface belongs to tui; config has matching methods without importing tui.

```go
// internal/config
type DraftStore struct { paths Paths }
func NewDraftStore(paths Paths) *DraftStore
func (*DraftStore) Load(domain.RepoRef) (domain.Draft, bool, error)
func (*DraftStore) Save(domain.Draft) error
func (*DraftStore) Delete(domain.RepoRef) error
func draftPath(paths Paths, repoKey string) string
```

- Filename is HEX_SHA256(lowercase logical-host/owner/repo)+.json under DraftDir. No user-controlled path component.
- JSON version1 fields correspond to domain.Draft; persist empty Uncertain as [] and InFlight as null.
- Load: absent -> false,nil; invalid key/version/spans/JSON -> config, original remains untouched. Save must recheck parseability before replacing an existing file.
- The frontend owns one writer goroutine and orders local Revision values. Config performs atomic writes; no cross-process revisions/hash comparisons or locks.
- Debounce500ms after last keystroke. Flush on preview/run/close/quit and every workflow step change.
- Before creating, persist current remaining source and InFlight span. Keep InFlight through placing/closing.
- On settled, rebuild text from Item.Source and current results, remove created/skipped, recompute spans, atomically save; Delete when exhausted.
- On reload, InFlight becomes an Uncertain flag. UI does not silently send flagged text again; explicit check/edit/acknowledgement is required.
- Editing exact flagged Source clears its marker; hash records source identity only, not cross-process concurrency.
- Save failure pauses sending; retain visible text and conservatively saved marker. An unreadable draft is never overwritten silently.
- Concurrent TUI processes editing one draft are deliberately last save wins. No load-theirs/keep-mine conflict feature.
- No confirmed GitHub result/cache or token is written. CLI quick-add creates no draft/recovery file.

## Shared Service fake: internal/service/servicetest

Both CLI and TUI tests use this fake. Do not create a second face-specific fake.

```go
type Fixture struct {
    Archived map[int]domain.Placement // optional archived membership for restore tests
    Session domain.Session
    Credentials domain.CredentialInfo
    Issues []domain.IssueDetail
    IssueComments map[int][]domain.Comment
    PRs []domain.PRDetail
    PRComments map[int][]domain.Comment
    Labels []domain.Label
    Users []domain.User
    Projects []domain.Project
    BoardInfo domain.BoardInfo
    Board domain.Board
    RateLimit domain.RateLimit
    ConfigCheck domain.DoctorCheck
}
type Arguments struct {
    Number int
    Filter domain.IssueFilter
    PRFilter domain.PRFilter
    Page domain.PageRequest
    Add, Remove []string
    Status *string
    NewIssue domain.NewIssue
    Edit domain.EditIssue
    Close domain.CloseOptions
    Text string
    Project domain.ProjectRef
    QuickAdd domain.QuickAddOptions
    Index int
}
type Call struct {
    Method string
    Args Arguments
    StartOrder, EndOrder uint64
    Err *domain.Error
}
type Gate struct {
    Started <-chan struct{}
    mu sync.Mutex
    released chan struct{}
    mode gateMode
    err error
    once sync.Once
}
type gateMode int // wait, fail before effect, apply then fail
type Fake struct {
    mu sync.Mutex
    fixture Fixture
    calls []Call
    gates map[gateKey][]*Gate
    sequence uint64
    now time.Time
}
type gateKey struct { Method string; Number int }

func DefaultFixture() Fixture
func GenIssues(n int) []domain.IssueDetail
func New(Fixture) *Fake
func (f *Fake) Hold(method string, number int) *Gate
func (g *Gate) Release()
func (g *Gate) Fail(error)
func (g *Gate) ApplyThenFail(error)
func (f *Fake) Calls() []Call
func (f *Fake) Mutate(func(*Fixture))
func (f *Fake) SetRateLimit(domain.RateLimit)
```

- Fake implements every exact Service signature above through explicit wrappers; compile-time `var _ service.Service = (*Fake)(nil)` catches missing methods.
- Directly implement Service over Fixture. Do not construct or delegate to the real service, Backend, or SettingsStore.
- Reads page fixture values; writes apply simple set semantics. Do not duplicate real validation or multi-step use cases: tests stage refusals/partial/uncertain outcomes through gates.
- No network, config or environment reads.
- Private helpers clone fixture/argument/result data, begin/finish call-log entries, and await gates without holding the fixture mutex. There is no in-memory Backend or real-service constructor.
- Phase 1 implements every read, single-issue change and board change. PlanQuickAdd and RunQuickAdd return a typed internal error until Phase 5; their full behaviour and named phase/item-gate tests are deferred to that phase.
- Default time is exactly 2026-11-13T12:00:00Z. Fixture mutations advance a deterministic clock; gates, never sleeps, stage races.
- Fixture issues are canonical; board-card reads refresh their embedded Issue from that source while preserving column order/placement.
- Hold matches one future call by method/number, consumed in registration order. Number0 means non-issue/session/list/board call; quick-add item gates use zero-based input Index.
- A gate closes Started when the matched call arrives; waits without holding fake mu. Context cancellation unblocks it immediately.
- Release allows the call/effect. Fail sets the returned error and releases without applying effects.
- ApplyThenFail applies the fixture effect, then returns the supplied typed error; use Uncertain=true timeout/network to model a write that landed.
- Gate methods are idempotent; set mode/error under mu before closing released. The first release decision wins.
- Calls records all arguments as deep copies plus monotonic start/end order, including failed/canceled calls. Tests can compute overlap from that ordering.
- Mutate holds fake mu only during the callback, allowing tests to simulate github.com edits. Tests must not call fake Service recursively inside it.
- SetRateLimit updates a snapshot under mu; RateLimit is ungated/nonblocking.
- From Phase 5 the fake quick-add methods directly apply fixture effects and emit creating/closing/placing/settled phases, honour StopRequested between items, and never sleep or use the real executor. `Hold("RunQuickAdd.item", index)` will gate each item; until then both quick-add methods return internal without effects.
- All fixture/results/calls returned to tests are cloned; fixture mutation never aliases a live TUI result.

Required fixtures:

- Default includes open/completed/not-planned issues, null author, multiple labels/users, a PR of each state, comments, ordered board columns and deterministic IDs.
- A board with a populated No status column, HiddenCount>0, Truncated=true and more than500 cards.
- `Fixture.Issues = GenIssues(1204)` for list cutoff and issue-mine server filtering.
- No linked project; multiple Candidates; selected unusable project; missing_scope; read-only board; no credentials; disabled issues.
- GenIssues uses deterministic IDs/numbers/timestamps and no randomness. Each fixture may be independently copied/mutated.

## Files, dependencies and named tests

| Package/files | Contents |
|---|---|
| service/service.go, backend.go | Exact interfaces and constructor |
| service/metadata.go, resolve.go | Session, metadata, board and reference resolution |
| service/issues.go, board.go | Mutation flows, no-ops, assignment verification |
| service/quick_add.go, doctor.go | Run-local pacing, phases, read-only diagnostics |
| quickadd/parse.go, remaining.go | Pure parsing/source reconstruction |
| config/config.go, drafts.go, atomic.go | Versioned structs and atomic persistence |
| service/servicetest/fake.go, fixture.go, gates.go, backend.go | Shared fake |

Dependencies: service/quickadd use standard library only; config pins go.yaml.in/yaml/v3 v3.0.6.
No x/sys, file-lock package or conflict-token subsystem is added.

| Area | Named test cases |
|---|---|
| Metadata | TestMetadata_TTLAndCopies, TestMetadata_ConcurrentFillsNoHeldNetworkLock, TestRefreshMetadata_NewCredentialState |
| Resolution | TestBoard_ExplicitRememberedLinkedPrecedence, TestBoard_NoBoardCandidatesDiscriminator, TestBoard_WrongStatusType, TestBoardUse_PersistBeforeSelection, TestProjectRef_UserOrgAndWrongHost |
| Reads | TestReads_OnePageRawCursor, TestIssues_MineExpandsMe, TestPRs_MineHasNoServerAuthorFilter, TestIssueRead_BoardFailureNonfatal, TestBoard_1000Scan500DisplayAndHidden |
| Validation | TestNames_AllValidatedBeforeWrite, TestNames_RefreshUnknownOnce, TestAssignees_DedupMax10, TestRemovals_DepartedAssigneeUsesIssueID, TestRemovals_DeletedLabelUsesIssueID, TestRemovals_AbsentKnownNoOpUnknownRejectsBeforeAnyWrite, TestRemovals_DoNotRelaxAdditions, TestRemovals_MixedDeltasExpandMeAndPreservePartial, TestStatus_ExactThenUniqueFold, TestEdit_OnlySuppliedFields |
| Mutations | TestAssignment_SilentIgnoreForbidden, TestCreate_AssignmentFailurePartial, TestNoOp_ClosedSkipsComment, TestClose_CommentThenFailurePartial, TestPicker_RemovedThenAddFailureInternalStep, TestReassign_OnlyAssigneeField |
| Board writes | TestMove_AbsentAddThenSet, TestMove_ArchivedRestoreThenSet, TestBoardAdd_ExistingStatusPreserved, TestBoardAdd_NewDefaultStatusCleared, TestRestoreFailurePartialAddedToBoard, TestRemove_AbsentNoOp, TestRead_NeverUnarchives |
| Parser | TestParse_AllPrefixes, TestParse_CheckboxCasesAndLiteralShortcuts, TestParse_IndentAndNestedBody, TestParse_BlankLinesAndCRLF, TestParse_OrphanAndOneSpaceIndent, TestParse_Unicode256AndEmptyTitle, TestParse_51IncludingSkipped, TestParse_LongBodyLine, TestParse_DuplicateTitles, FuzzParse_SourceSpansDeterminism |
| Quick add | TestQuickAdd_DryRunPlannedCounts, TestQuickAdd_CheckedSkipOrCloseNoBoard, TestQuickAdd_AddToBoardNoStatus, TestQuickAdd_PerWriteOneSecondRunLocal, TestQuickAdd_OrdinaryWritesUnpaced, TestQuickAdd_StopFinishesItem, TestQuickAdd_ContextInterruptAccountsAll, TestQuickAdd_OnProgressSaveFailureStops, TestQuickAdd_FirstUnknownPartial7, TestQuickAdd_PostCreateFailureStaysCreated, TestQuickAdd_RemainingVerbatim |
| Doctor | TestDoctor_NeverWrites, TestDoctor_AlwaysSixRows, TestDoctor_FirstThreeDetermineVerdict, TestDoctor_ConfigWarningIndependentOfBoard |
| Config | TestConfig_Missing, TestConfig_ReadParseFailureEmptyButNeverOverwritten, TestConfig_UnknownTopLevelInlineMap, TestConfig_AtomicFailurePreservesOriginal, TestConfig_LastSaveWins, TestPaths_XDGMacAndFallback |
| Drafts | TestDraft_PrivateAtomicRoundTrip, TestDraft_UnparseableNeverOverwrite, TestDraft_InFlightReloadUncertain, TestDraft_RemainingOffsets, TestDraft_ConcurrentProcessesLastSaveWins |
| Shared fake | TestFake_GateReleaseFailureAndCancel, TestFake_ApplyThenFailChangesFixture, TestFake_ArgumentsAndCallOrderOwned, TestFake_MutateExternalEdit, TestFake_RateSnapshot, TestFake_QuickAddPhasesAndItemGates, TestFake_DefaultFixtureTimeAndGenIssues |

Use fake Backend/SettingsStore for service tests; shared servicetest for face tests. Fixtures and gates never need credentials or the network. Run gofmt -l ., go vet ./..., go test -race ./... during implementation; this documentation task creates no implementation/test files.

