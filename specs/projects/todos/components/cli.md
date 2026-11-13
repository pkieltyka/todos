---
status: complete
---

# CLI, entry point and agent skill

Packages/files: internal/cli, main.go and skills/todos/SKILL.md.
[Architecture](../architecture.md) wins; [service.md](service.md) defines every operation.

## Responsibilities and dependencies

- Construct and wire github, config and service; no other package constructs these collaborators.
- Parse Cobra commands; call the same Service used by TUI; format output and map exit status.
- Launch tui.Run with its agreed Deps/Options; render text through internal/markdown.
- JSON is an explicit versioned DTO contract, never encoding domain structs directly.
- Pin cobra v1.10.2, go-gh v2.17.0 and x/term v0.48.0; use architecture10's frontend pins.
- No network/config writes for help, version, --version or skill. Constructors are lazy.

## main.go

```go
package main

import (
    _ "embed"
    "github.com/pkieltyka/todos/internal/cli"
)

//go:embed skills/todos/SKILL.md
var skill []byte

var version = "dev" // release build: -ldflags=-X=main.version=VERSION

func main() { cli.Main(skill, version) }
```

- Module root entrypoint yields binary todos for go install github.com/pkieltyka/todos@latest.
- No command construction, GitHub calls or domain behaviour in main.

## Construction and injection

```go
type GlobalOptions struct {
    Repo, Project string
    JSON, NoColor, Version bool
}
type OutputMode int
const (
    Human OutputMode = iota
    Piped
    JSON
)
type Wiring struct {
    Service service.Service
    Drafts tui.DraftStore
    ConfigWarning func() *domain.Error
}
type Dependencies struct {
    In io.Reader
    Out, Err io.Writer
    Skill []byte
    Version string
    Getenv func(string) string
    LookupEnv func(string) (string, bool)
    Now func() time.Time
    IsTTY func(io.Writer) bool
    InputIsTTY func(io.Reader) bool
    Width func(io.Writer) int
    ResolveRepo func(context.Context, string) (domain.RepoRef, error)
    Wire func(context.Context, func(context.Context)(domain.RepoRef,error), string) (Wiring, error)
    RunTUI func(context.Context, tui.Deps, tui.Options) error
    Open func(string) error
    Interrupted func() bool
}
type runtime struct {
    deps Dependencies
    global GlobalOptions
    mode OutputMode
    repo domain.RepoRef
    wiring Wiring
    wired bool
    renderer *markdown.Renderer
    result any
    resultOnFailure bool
}
type ListOptions struct { Limit int; All bool }
type CommandSpec struct {
    Use, Short, Group, Example string
    MinArgs, MaxArgs int // -1 means unlimited
    Flags func(*cobra.Command)
    Run func(context.Context, *runtime, *cobra.Command, []string) (any,error)
}

func Main(skill []byte, version string)
func Run(context.Context, []string, Dependencies) int
func NewRoot(Dependencies) *cobra.Command
func ProductionDependencies(skill []byte, version string) Dependencies
func ResolveRepository(ctx context.Context, flag string) (domain.RepoRef,error)
func (r *runtime) ensureService(context.Context) error
func (r *runtime) ensureDoctorService(context.Context) error
func (r *runtime) startTUI(context.Context, tui.ViewID) error
func modeFor(GlobalOptions, bool) OutputMode
func detectJSON(args []string) bool
func exitCode(err error, interrupted bool) int
func readText(in io.Reader, path string) (string,error)
```

Wiring algorithm:

1. Run creates a fresh Cobra tree/runtime and parses local flags/arguments first.
2. Help/version/skill complete without ensureService. Bare/explicit TUI checks both input/output TTY before resolving anything.
3. ResolveRepository applies --repo via repository.Parse, then GH_REPO via repository.Parse, then repository.Current. No remote enumeration/sorting, SSH parsing or global environment edits.
4. Explicit --repo has exactly OWNER/REPO syntax; GH_REPO accepts the forms go-gh accepts. Invalid supplied values are invalid_input; no default is no_repo.
5. DefaultPaths/New config store, NewDraftStore and github.New are constructed lazily. Wire receives a cached resolver closure, not an already-required valid repo. Pass a host closure using that ref's host when available, otherwise auth.DefaultHost, to github.
6. service.New receives Backend, SettingsStore and Options{ResolveRepository:that closure, Project, Now, Wait}. No GitHub request during wiring. Normal ensureService requires the resolver to succeed; ensureDoctorService allows its failure so Doctor can still inspect credentials/config and report a failed repository check.
7. Construct markdown.NewRenderer(markdown.Options{BaseURL:"https://HOST/OWNER/REPO"}). The TUI first frame never waits for GitHub.
8. RunTUI receives Deps{Service,Drafts,Renderer,Clock:Now,Open,Env} and Options{StartView,Repo}. Open is go-gh pkg/browser.
9. Env.Theme is raw TODOS_THEME; invalid nonempty values warn on stderr and fall back to detected background.
10. Env.Editor is shlex.Split(VISUAL), else shlex.Split(EDITOR), else nil; parse error is usage with the variable name, never a shell execution. The TUI owns ExecProcess/temp files.
11. Config warning: stderr line in text mode, silent in JSON; doctor gets the SettingsStore.Check row in all modes. Do not duplicate the same warning for each call.

- Main installs signal.NotifyContext for SIGINT/SIGTERM, tracks whether a signal canceled the call, calls Run and os.Exit.
- Run itself never calls os.Exit or installs global signal handlers; tests inject cancellation/Interrupted.
- A reusable cancellation-aware real Wait uses time.NewTimer/select. There is no global pacing in CLI wiring.

## Command tree and service dispatch

Persistent flags: --repo, --project, --json, --no-color, --help, --version.
Cobra: SilenceUsage=true, SilenceErrors=true, CompletionOptions.DisableDefaultCmd=true.
Own --version handling rather than Cobra's default text-only version output.

| Use / flags | Handler signature name / Service call |
|---|---|
| root with no subcommand | runRoot -> TUI ViewIssues |
| tui [issues\|prs\|board] | runTUI -> selected ViewID |
| list --state --assignee\|--mine --label... --search --limit\|--all | runList -> ListIssues loop |
| view ISSUE | runView -> GetIssue + ListIssueComments loop |
| add TITLE --body\|--body-file --assign... --label... --status | runAdd -> CreateIssue |
| quick-add --file --assign... --label... --status --checked --dry-run | runQuickAdd -> PlanQuickAdd + RunQuickAdd |
| edit ISSUE --title --body\|--body-file | runEdit -> EditIssue |
| assign ISSUE LOGIN... | runAssign -> UpdateAssignees(add,nil) |
| unassign ISSUE [LOGIN...] | runUnassign -> UpdateAssignees(nil,remove); no names reads current assignees first |
| reassign ISSUE LOGIN... | runReassign -> Reassign |
| label ISSUE NAME... | runLabel -> UpdateLabels(add,nil) |
| unlabel ISSUE NAME... | runUnlabel -> UpdateLabels(nil,remove) |
| comment ISSUE (TEXT\|--body-file) | runComment -> Comment |
| close ISSUE --reason --comment | runClose -> Close |
| reopen ISSUE | runReopen -> Reopen |
| rm ISSUE --yes | runRemove -> Delete |
| move ISSUE (STATUS\|--no-status) | runMove -> SetStatus |
| board | runBoard -> Board |
| board list | runBoardList -> BoardCandidates |
| board use PROJECT | runBoardUse -> UseBoard |
| board add ISSUE --status | runBoardAdd -> BoardAdd |
| board remove ISSUE | runBoardRemove -> RemoveFromBoard |
| pr list --state --limit\|--all | runPRList -> ListPRs loop |
| pr view PR | runPRView -> GetPR + ListPRComments loop |
| labels | runLabels -> Labels |
| assignees | runAssignees -> AssignableUsers |
| doctor | runDoctor -> Doctor; report stdout even on failure |
| skill | runSkill -> embedded bytes |
| version / --version | runVersion -> version result |
| help [COMMAND...] / --help | Cobra help, no Service |

All named handlers have this exact signature:

| Commands | Positional bounds |
|---|---|
| root, list, quick-add, board, board list, pr list, labels, assignees, doctor, skill, version | Exactly0 |
| tui | 0..1 |
| view, add, edit, close, reopen, rm, board use, board add, board remove, pr view | Exactly1 |
| assign, reassign, label, unlabel | At least2 |
| unassign | At least1 |
| comment, move | 1..2, with mutually exclusive flag rules below |
| help | 0..unlimited command-path segments |

```go
func runList(ctx context.Context, r *runtime, cmd *cobra.Command, args []string) (any,error)
// Identical signature for runRoot, runTUI, runView, runAdd, runQuickAdd,
// runEdit, runAssign, runUnassign, runReassign, runLabel, runUnlabel,
// runComment, runClose, runReopen, runRemove, runMove, runBoard,
// runBoardList, runBoardUse, runBoardAdd, runBoardRemove, runPRList,
// runPRView, runLabels, runAssignees, runDoctor, runSkill, runVersion.
func commandSpecs() []CommandSpec
func bindCommand(parent *cobra.Command, spec CommandSpec, r *runtime) *cobra.Command
func validateFlags(cmd *cobra.Command, args []string) error
```

- Register help in groups Issues, Board, Pull requests, Other. First help line explains bare todos launches TUI.
- Each command gets usage, one-line summary and2-3 examples. Usage errors print only error and command usage, never full help.
- Parse errors/Args/RunE go through one outer error presenter; leaf handlers never print the same failure.
- All non-TUI commands are noninteractive. Missing arguments never launch an editor or read keyboard input.

## Argument and flag algorithms

- ISSUE/PR/PROJECT parsing calls service.ParseIssueRef/ParseProjectRef after cheap repo resolution.
- add TITLE and comment TEXT are each one argument; shell quoting supplies spaces. Do not concatenate arbitrary extra arguments.
- State defaults open. Issue states open/closed/all; PR states open/closed/merged/all.
- Other defaults: repo/project/body strings empty, boolean flags false, checked=skip, reason=completed, limit=50. Flag Changed distinguishes an omitted body/title from an explicit empty value.
- --limit defaults50 and must be>0; explicit --limit plus --all is usage, using flag Changed rather than comparing the value to50.
- --mine and --assignee together are usage; mine maps to @me.
- Repeated --label means AND. Repeated --assign passes all unique logins to validation.
- --body and --body-file together are usage. `--body-file -` reads stdin; no other command implicitly reads stdin.
- --file is required for quick-add; --file - reads stdin. Read the whole text once and preserve bytes/source.
- edit requires at least one explicit title/body/body-file; empty body is a valid clear, empty title is invalid_input.
- comment requires exactly one nonblank positional text/body-file. Whitespace-only is usage.
- close reason CLI completed/not-planned maps to internal completed/not_planned. A blank explicit closing comment is usage.
- rm requires --yes true; absent/false is usage and never reaches Delete.
- move requires status or --no-status exclusively; nil status represents clear.
- CLI NewIssue/QuickAddOptions sets AddToBoard only when --status is supplied; TUI sets it separately for No status creation.
- checked mode skip default or close; invalid spelling is usage. --dry-run uses the same validation with zero writes.
- --json with root TUI/tui/skill is usage; --version short-circuits as version output before TUI rejection.
- Bare/explicit TUI without both TTYs is usage2.
- detectJSON scans flags before Cobra errors, stopping at -- and skipping values of known value-taking flags, so a body string equal to --json does not change error mode. Honour --json=false. This makes unknown-command/flag errors JSON when requested.

## List/detail collection

```go
func collectIssues(ctx context.Context, s service.Service, filter domain.IssueFilter, opts ListOptions) (IssuesDTO,error)
func collectPRs(ctx context.Context, s service.Service, filter domain.PRFilter, opts ListOptions) (PRsDTO,error)
func collectComments(ctx context.Context, fetch func(context.Context,domain.PageRequest)(domain.Page[domain.Comment],error)) ([]CommentDTO,bool,error)
```

1. Request first page with Size=min(100, remaining limit), or100 for --all.
2. Return --all Search total>1000 as invalid_input with narrow-query hint before stdout output.
3. Append owned rows, deduplicate node ID, preserve first occurrence order; repeat raw NextCursor until requested limit/HasMore=false/provider exhaustion.
4. Detect a repeated nonempty cursor as github malformed pagination, preventing an infinite loop.
5. Total is provider total, not number loaded. Truncated means requested prefix is incomplete or provider limit was hit.
6. Buffer the complete requested result in memory before output so an ordinary later-page failure leaves stdout empty. No on-disk GitHub cache.
7. Detail combines body with at most500 comments, fetched separately in oldest-first order. Extra comments set comments_truncated=true.
8. Board prints all matching cards from up to1000 scanned items, independent of TUI's500 display cap.

## JSON DTOs

These structs live in internal/cli/json.go, use explicit tags and never embed a domain value.
Time strings use `.UTC().Format(time.RFC3339)`; absent time pointers remain nil.

```go
type IssueDTO struct {
    Number int `json:"number"`
    Title string `json:"title"`
    State string `json:"state"`
    StateReason *string `json:"state_reason"`
    Author *string `json:"author"`
    Assignees []string `json:"assignees"`
    Labels []string `json:"labels"`
    Milestone *string `json:"milestone"`
    CommentsCount int `json:"comments_count"`
    CreatedAt string `json:"created_at"`
    UpdatedAt string `json:"updated_at"`
    ClosedAt *string `json:"closed_at"`
    URL string `json:"url"`
    BoardStatus *string `json:"board_status"`
}
type CommentDTO struct {
    ID string `json:"id"`
    Author *string `json:"author"`
    CreatedAt string `json:"created_at"`
    Body string `json:"body"`
    URL string `json:"url"`
}
type IssueViewDTO struct {
    IssueDTO
    Body string `json:"body"`
    Comments []CommentDTO `json:"comments"`
    CommentsTruncated bool `json:"comments_truncated"`
}
type PRDTO struct {
    Number int `json:"number"`
    Title string `json:"title"`
    State string `json:"state"`
    Draft bool `json:"draft"`
    Author *string `json:"author"`
    Head string `json:"head"`
    Base string `json:"base"`
    Review string `json:"review"`
    Checks string `json:"checks"`
    Assignees []string `json:"assignees"`
    Labels []string `json:"labels"`
    CreatedAt string `json:"created_at"`
    UpdatedAt string `json:"updated_at"`
    ClosedAt *string `json:"closed_at"`
    MergedAt *string `json:"merged_at"`
    URL string `json:"url"`
}
type PRViewDTO struct {
    PRDTO
    Body string `json:"body"`
    Mergeable string `json:"mergeable"`
    Closes []int `json:"closes"`
    Comments []CommentDTO `json:"comments"`
    CommentsTruncated bool `json:"comments_truncated"`
}
type ProjectDTO struct {
    Number int `json:"number"`
    Owner string `json:"owner"`
    Title string `json:"title"`
    URL string `json:"url"`
    Usable bool `json:"usable"`
}
type CardDTO struct {
    Number int `json:"number"`
    Title string `json:"title"`
    State string `json:"state"`
    Assignees []string `json:"assignees"`
    Labels []string `json:"labels"`
    URL string `json:"url"`
}
type ColumnDTO struct { Status *string `json:"status"`; Cards []CardDTO `json:"cards"` }
type BoardDTO struct {
    Project ProjectDTO `json:"project"`
    Columns []ColumnDTO `json:"columns"`
    HiddenItems int `json:"hidden_items"`
    Truncated bool `json:"truncated"`
}
type IssuesDTO struct { Issues []IssueDTO `json:"issues"`; Total int `json:"total"`; Truncated bool `json:"truncated"` }
type PRsDTO struct { PullRequests []PRDTO `json:"pull_requests"`; Total int `json:"total"`; Truncated bool `json:"truncated"` }
type ChangeDTO struct { Issue IssueDTO `json:"issue"`; Changed bool `json:"changed"` }
type CommentResultDTO struct { Issue IssueDTO `json:"issue"`; Comment CommentDTO `json:"comment"` }
type DeletedDTO struct { Deleted int `json:"deleted"` }
type ProjectsDTO struct { Projects []ProjectDTO `json:"projects"`; Selected *ProjectDTO `json:"selected"` }
type ProjectResultDTO struct { Project ProjectDTO `json:"project"` }
type LabelDTO struct { Name string `json:"name"`; Description string `json:"description"`; Color string `json:"color"` }
type UserDTO struct { Login string `json:"login"`; Name string `json:"name"` }
type LabelsDTO struct { Labels []LabelDTO `json:"labels"` }
type AssigneesDTO struct { Assignees []UserDTO `json:"assignees"` }
type CheckDTO struct { Name string `json:"name"`; OK bool `json:"ok"`; Detail string `json:"detail"`; Hint string `json:"hint"` }
type DoctorDTO struct { OK bool `json:"ok"`; Checks []CheckDTO `json:"checks"` }
type VersionDTO struct { Version string `json:"version"` }
type ErrorDTO struct {
    Kind string `json:"kind"`
    Message string `json:"message"`
    Hint string `json:"hint"`
    RetryAfterSeconds *int `json:"retry_after_seconds,omitempty"`
    Uncertain bool `json:"uncertain"`
    Completed *[]string `json:"completed,omitempty"`
    Cause *string `json:"cause,omitempty"`
    Issue *IssueDTO `json:"issue,omitempty"`
}
type ErrorEnvelope struct { Error ErrorDTO `json:"error"` }
type QuickItemDTO struct {
    Line int `json:"line"`
    Title string `json:"title"`
    Body string `json:"body"`
    Checked bool `json:"checked"`
    Result string `json:"result"`
    Issue *IssueDTO `json:"issue"`
    OnBoard bool `json:"on_board"`
    Closed bool `json:"closed"`
    Error *ErrorDTO `json:"error"`
}
type QuickCountsDTO struct {
    Created int `json:"created"`
    Failed int `json:"failed"`
    Uncertain int `json:"uncertain"`
    Unattempted int `json:"unattempted"`
    Skipped int `json:"skipped"`
    Planned int `json:"planned"`
}
type QuickDTO struct { Items []QuickItemDTO `json:"items"`; Counts QuickCountsDTO `json:"counts"` }

func issueDTO(domain.Issue) IssueDTO
func issueViewDTO(domain.IssueDetail, []CommentDTO, bool) IssueViewDTO
func commentDTO(domain.Comment) CommentDTO
func prDTO(domain.PullRequest) PRDTO
func prViewDTO(domain.PRDetail, []CommentDTO, bool) PRViewDTO
func projectDTO(domain.Project) ProjectDTO
func boardDTO(domain.Board) BoardDTO
func changeDTO(domain.Change) ChangeDTO
func errorDTO(*domain.Error) ErrorDTO
func quickDTO(domain.QuickAddResult) QuickDTO
func doctorDTO(domain.DoctorReport) DoctorDTO
func doctorWiringFailure(err error) (domain.DoctorReport,error)
```

- All arrays explicitly allocated as [] when empty. Nil authors/nullable fields become null.
- Body/comments/view-only fields exist only in view DTOs, even when empty.
- Error retry field only for rate_limited; Completed/Cause/Issue only for partial (Issue optional when unknown).
- Partial Completed is [] when no public confirmed step is available; never leak internal removed into CLI output.
- JSON encodes control characters safely; do not render/sanitise Markdown into JSON strings and change data.
- Internal IDs/colours/capabilities/source spans/Candidates/Err never accidentally become JSON fields.
- Fields may only be added within a major version; never rename/remove/retype.

## Output and error routing

```go
func renderJSON(w io.Writer, value any) error
func renderHuman(ctx context.Context, r *runtime, value any) error
func renderPiped(ctx context.Context, r *runtime, value any) error
func presentFailure(r *runtime, cmd *cobra.Command, err error) error
func tsvField(string) string
```

| Case | stdout | stderr |
|---|---|---|
| Ordinary success | Exactly one JSON document, or chosen text | Text config warning if any; JSON warnings silent |
| Ordinary failure | Empty | One JSON error envelope or text error+hint |
| Quick-add run stopped | Complete QuickDTO even on failure | Error envelope/text; exit carries failure |
| Quick-add parse/preflight error | Empty | Full line problems as one safe error message |
| Doctor, any verdict | Complete DoctorDTO/report always | No duplicate generic diagnostic envelope |
| Skill | Exact embedded bytes | Only actual I/O/usage error |
| Version | VersionDTO or version line | Only actual error |

- Render to a buffer before committing ordinary output so a rendering/late-read failure leaves stdout empty.
- Human: aligned muted-header tables, Unicode state glyphs, relative times, titles truncated by display cells to terminal width. No table borders.
- Piped: tab-separated rows, no header/colour/truncation, RFC3339 times; literal backslash/tab/newline/CR escaped as \\, \t, \n, \r within fields.
- Issue TSV: number,title,state,labels,assignees,updated_at,url.
- PR TSV: number,title,state,head,review,checks,updated_at,url.
- Board TSV: column,number,title,state,labels,assignees,url; No status is an empty column field.
- Project TSV: owner,number,title,url,usable; labels name,description,color; assignees login,name.
- Empty result is the approved plain sentence. Truncation summary is prose; TSV is convenient human piping, JSON remains the contract.
- Detail text: metadata prelude, body and chronological comment sections. Terminal body uses renderer.Render(raw,width,style); pipe body uses markdown.SanitizeBody raw source.
- Every short GitHub field/error/hint goes through markdown.SanitizeLine before terminal output. Generated table styles are applied afterward.
- Width fallback80, no default body width above100. NO_COLOR being present or --no-color disables colours. Piped output never has ANSI.
- CLI Markdown style is Plain when colour is disabled, Light for TODOS_THEME=light, otherwise Dark. Terminal background detection belongs to the TUI event loop; CLI does not block on a terminal background query.
- Quick-add human result names created/open/unplaced/skipped/failed/uncertain/unattempted rows and all counts; never suggest rerunning the whole input.
- On output write failure, return a safe internal/domain error; do not claim a GitHub write failed or retry it.

## Exit mapping and signals

| Code | Kinds / rule |
|---|---|
| 0 | Success, empty, no-op; doctor checks1..3 passed |
| 1 | network, timeout, github, internal |
| 2 | usage, invalid_input |
| 3 | not_found |
| 4 | auth, missing_scope, forbidden |
| 5 | rate_limited |
| 6 | no_repo, no_board, issues_disabled, config |
| 7 | partial |
| 130 | Context canceled by an actual signal |

- Preserve context.Canceled through Error.Unwrap/partial wrapping. An internal canceled read without signal is not automatically exit130.
- Quick-add signal cancellation still emits accounting/error documents when graceful completion permits; no writes continue after return.
- Doctor uses FirstFailure for exit kind while always printing its report. Config/board check failures alone return0.
- A valid doctor invocation whose local wiring itself fails still prints a six-row doctorWiringFailure report, marking unavailable checks as blocked with that cause; it never falls back to a generic stdout-empty error. Argument/unknown-flag parse errors remain ordinary usage errors.
- No cancellation permits retry of a create/comment; uncertain means check GitHub before resubmitting.

## Embedded skill contract

- Source: skills/todos/SKILL.md; Markdown with YAML front matter name: todos and a concise description.
- Print exact embedded bytes via todos skill. Never install automatically or write a skill file from this command.
- Explain when to use todos; one-repo/one-board scope; --repo and GH_REPO precedence.
- List every supported command/flag with real examples; agents use --json except skill/help.
- Include all JSON documents/null/array conventions and exit mapping, especially doctor/quick-add stdout exceptions.
- Quick add: --file -, --dry-run first, cap50, checked skip/close, shared metadata, no inline shortcuts, first-failure stop.
- Recover from partial using completed steps/known issue numbers; never rerun an entire partially successful batch.
- Uncertain: do not automatically retry; check GitHub. Created-but-unclosed/unplaced is fixed with close/move, never another create.
- Permanent rm only when the user explicitly requested deletion; prefer close otherwise. Never grant scopes or change authentication automatically.
- Tell agents to run doctor on access/setup failures and inspect its report plus exit code.

## Named tests, fixtures and module checks

Both CLI and TUI use service/servicetest.Fake; no CLI-only fake Service.

- Construct New(servicetest.DefaultFixture()); use Hold/Fail/ApplyThenFail for deterministic result order/failures.
- Inject terminal dimensions, Now, streams, environment, resolver and signals. No test sleeps or uses a real credential.
- Store stable JSON/text/TSV stdout and stderr goldens separately under internal/cli/testdata, plus expected exit codes.

| Area | Named test cases |
|---|---|
| Entry/constructors | TestMain_EmbedAndRootBinary, TestOffline_HelpVersionSkillNoService, TestTUI_NoTTYUsage2, TestTUI_WiringAndStartView |
| Resolution | TestResolveRepo_FlagThenGHRepoThenCurrent, TestResolveRepo_ExplicitInvalidNoFallback, TestRef_WrongRepoAndKind |
| Flags | TestCommands_ArgumentBounds, TestFlags_AllExclusivityCases, TestEdit_BodyFileCountsAsEdit, TestNoPrompt_ReadsStdinOnlyForExplicitDash, TestReason_HyphenToUnderscore |
| Lists | TestList_Limit50AndAll, TestList_RawCursorRepeatedFilter, TestSearch_AllOver1000InvalidInput, TestSearch_BoundedProviderTruncated, TestList_LaterFailureStdoutEmpty, TestComments_500Cutoff |
| JSON | TestJSON_AllCommandGoldens, TestJSON_EmptyArraysNullsTimes, TestJSON_ViewFieldsAbsentFromChanges, TestJSON_InternalFieldsNeverLeak, TestJSON_UnknownFlagRespectsRequestedMode |
| Errors | TestExit_AllKindsAndSignal130, TestErrors_SingleEnvelopeNoDuplicate, TestPartial_NoInternalRemovedStep, TestOutputFailure_NeverRetriesWrite |
| Doctor | TestDoctor_AlwaysStdoutAndFullReport, TestDoctor_BoardConfigFailureExit0, TestDoctor_FirstIssueFailureExitKind, TestDoctor_JSONConfigWarningInReportOnly |
| Quick add | TestQuickAdd_DryRunPlanned, TestQuickAdd_PartialStdoutAndStderr7, TestQuickAdd_FirstRejectedUsesCauseExit, TestQuickAdd_FirstUnknownPartial7, TestQuickAdd_CreatedButNotClosedOrPlaced, TestQuickAdd_SignalAccountsRemaining |
| Render | TestTerminal_WidthUnicodeAndRelativeTimes, TestPiped_TSVNoHeaderColourOrTruncation, TestNoColor_EnvAndFlag, TestMaliciousText_AlwaysSanitised, TestDetail_PipedSafeRawMarkdown |
| Shared fake | TestCLI_UsesSharedFixturesAndGates |

Root package checks:

```go
func TestImportRules(t *testing.T)
func TestSkillMatchesCLI(t *testing.T)
```

- TestImportRules inspects module Go imports using go/parser; enforce architecture2's exact allowed project-import edges, including test helpers. No imports of github/config from TUI; service imports only domain/quickadd.
- TestSkillMatchesCLI parses shell fenced examples and declared command/flag lists from the embedded skill; resolve each command in NewRoot, check local/inherited flags and exclusive options. Examples are parsed, never executed against GitHub.
- Release validation during implementation: gofmt -l . && go vet ./... && go test -race ./.... Live tests remain opt-in/read-only.
