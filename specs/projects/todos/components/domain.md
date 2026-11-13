---
status: complete
---

# Domain

Implements `internal/domain`. [Architecture](../architecture.md) wins over this document.

## Responsibilities

- Plain owned data; standard-library imports only.
- No HTTP, filesystem, terminal, framework, use-case or JSON-output policy.
- Names and IDs coexist internally. The CLI explicitly projects the public JSON schema.
- Returned slices, maps and pointer values must not alias a mutable cache or fixture.

## Session, issues, pull requests, boards and paging

```go
type NodeID string
type RepoRef struct { Host, Owner, Name string }
type Repository struct {
    Ref RepoRef
    NodeID NodeID
    URL string
    IssuesEnabled, ViewerCanCreateIssues bool
    ViewerPermission string // GitHub ADMIN/MAINTAIN/WRITE/TRIAGE/READ, or empty
}
type User struct { NodeID NodeID; Login, Name string }
type Label struct { NodeID NodeID; Name, Description, Color string } // six hex digits, no '#'
type Session struct { Repo Repository; Viewer User }
type CredentialInfo struct { Source string; Scopes []string } // no token field
type IssueState string // open, closed
type StateReason string // completed, not_planned, reopened; nil for unknown/unsupported
type PRState string // open, closed, merged
type ReviewStatus string // approved, changes_requested, review_required, none
type ChecksStatus string // passing, failing, pending, none
type Mergeable string // mergeable, conflicting, unknown
type Placement struct { ItemID NodeID; Status *string; OptionID string }
type Issue struct {
    NodeID NodeID
    Number int
    Title string
    State IssueState
    StateReason *StateReason
    Author *string
    Assignees []string
    Labels []Label
    Milestone *string
    CommentCount int
    CreatedAt, UpdatedAt time.Time
    ClosedAt *time.Time
    URL string
    Board *Placement // nil=not on selected board or board unavailable
    ViewerCanUpdate, ViewerCanDelete bool
}
type IssueDetail struct { Issue Issue; Body string }
type Comment struct { ID string; Author *string; CreatedAt time.Time; Body, URL string }
type PullRequest struct {
    NodeID NodeID
    Number int
    Title string
    State PRState
    Draft bool
    Author *string
    Head, Base string
    Review ReviewStatus
    Checks ChecksStatus
    Assignees []string
    Labels []Label
    CreatedAt, UpdatedAt time.Time
    ClosedAt, MergedAt *time.Time
    URL string
    CommentCount int
}
type PRDetail struct { PullRequest PullRequest; Body string; Mergeable Mergeable; Closes []int }
type ProjectRef struct { Host, Owner string; Number int }
type Project struct {
    Ref ProjectRef
    NodeID NodeID
    Title, URL string
    Usable, Linked, ViewerCanUpdate bool
    TotalItems int
    UpdatedAt time.Time
}
type StatusOption struct { ID, Name, Color string }
type BoardInfo struct { Project Project; StatusFieldID NodeID; Options []StatusOption }
type ProjectsResult struct { Projects []Project; Selected *Project }
type Card struct { Issue } // enough for pending changes and opening/editing a card
type Column struct { Status *string; OptionID string; Cards []Card }
type Board struct {
    Project Project
    Columns []Column
    HiddenCount, ScannedCount, TotalItems, VisibleCount int
    Truncated, CardsTruncated bool // scan cap 1000; TUI display cap 500
}
type PageRequest struct { Cursor string; Size int } // 1..100; empty cursor=first
type Page[T any] struct {
    Items []T
    Total int
    NextCursor string
    HasMore bool
    // Search has more than 1000 matches; the provider cannot return every match.
    ProviderTruncated bool
}
type IssueFilter struct { State string; Assignee string; Labels []string; Search string }
type PRFilter struct { State string }
type NewIssue struct {
    Title, Body string
    Assignees, Labels []string
    AddToBoard bool
    Status *string
}
type EditIssue struct { Title, Body *string } // nil=do not send; &"" clears body
type CloseOptions struct { Reason string; Comment *string }
type Change struct { Issue Issue; Changed bool }
type CommentResult struct { Issue Issue; Comment Comment }
type RateLimit struct {
    Known bool
    Remaining, Limit int
    ResetAt time.Time
    Resource string
    Buckets []RateBucket
    BlockedUntil time.Time // secondary-rate-limit cooldown
}
type RateBucket struct { Resource string; Remaining, Limit int; ResetAt time.Time }
type DoctorCheck struct { Name string; OK bool; Detail, Hint string }
type DoctorReport struct { OK bool; Checks []DoctorCheck; FirstFailure *Error }

type ErrorKind string
const (
    ErrNetwork ErrorKind = "network"
    ErrTimeout ErrorKind = "timeout"
    ErrGitHub ErrorKind = "github"
    ErrInternal ErrorKind = "internal"
    ErrUsage ErrorKind = "usage"
    ErrInvalidInput ErrorKind = "invalid_input"
    ErrNotFound ErrorKind = "not_found"
    ErrAuth ErrorKind = "auth"
    ErrMissingScope ErrorKind = "missing_scope"
    ErrForbidden ErrorKind = "forbidden"
    ErrRateLimited ErrorKind = "rate_limited"
    ErrNoRepo ErrorKind = "no_repo"
    ErrNoBoard ErrorKind = "no_board"
    ErrIssuesDisabled ErrorKind = "issues_disabled"
    ErrConfig ErrorKind = "config"
    ErrPartial ErrorKind = "partial"
)
type Error struct {
    Kind ErrorKind
    Message, Hint string
    RetryAfter time.Duration
    Uncertain bool
    Completed []string // created, commented, added_to_board; internal TUI-only removed
    Cause ErrorKind
    Issue *Issue
    Candidates []Project // internal only; lets TUI show ambiguous-board picker
    Line int // internal parser/validation diagnostic; rendered into public message
    Err error // underlying error, never serialized
}
func (*Error) Error() string
func (*Error) Unwrap() error
```

### Value rules

| Field/type | Rule |
|---|---|
| `RepoRef.Host` | Logical GitHub host, not API gateway; defaults to github.com |
| `RepoRef.Owner/Name` | Canonical spelling from repository lookup; keys compare case-insensitively |
| `PageRequest.Cursor` | Plain GitHub cursor; empty means first page; never wrap, hash or validate against filters |
| `PageRequest.Size` | 1..100; zero defaults to 100; negative or >100 is invalid_input |
| `Page.Items` | Owned, nonnil slice, including empty results |
| `NextCursor`, `HasMore` | GitHub endCursor and hasNextPage; no prefetch of the next outer page |
| `Issue.Board` | Nil when absent/unavailable; archived membership is not an active placement |
| `Placement.Status` | Nil means an active card in No status; OptionID empty in that case |
| `Labels.Color` | Six hexadecimal digits, no leading #; UI sanitises and validates before colouring |
| `Author` | Nil for deleted/unavailable accounts; never fabricate a login |
| Nullable times | Nil when inapplicable; public conversion uses UTC RFC3339 |
| `StateReason` | Known values lowered; unknown/DUPLICATE becomes nil, preserving the fixed public enum |
| `PRFilter` | State only; TUI mine is a local author predicate, never GitHub Search |
| `IssueFilter.Assignee` | Accepts @me; service resolves to authenticated login |
| `NewIssue.AddToBoard` | True requests placement even when Status=nil; nonnil Status implies true |
| `Board.Truncated` | More than 1000 nonarchived project items; CLI prints all cards read |
| `Board.CardsTruncated` | More than 500 visible cards; used only by the TUI to limit display |

- `IssueDetail` and `PRDetail` contain body but no comments: comments have a separate paged read.
- `Change.Issue` is acknowledged write state. The TUI uses it directly; it does not re-read after a successful write. An uncertain write is reconciled with a fresh read.
- CloseOptions.Reason is completed or not_planned internally; CLI alone accepts the not-planned flag spelling.
- `Card` embeds `Issue`; row and board pending changes use the same shape.
- Issue labels and memberships stop at the first 100. No overflow query or ExtraLabels operation exists.
- Missing strings become empty strings; all lists become nonnil empty slices. Never use zero timestamps for absent times.

## Backend request records

These are transport-neutral. The Backend interface is in [service.md](service.md).

```go
type ProjectItem struct {
    RepositoryID NodeID // content repository identity; used for board scoping
    ID NodeID
    ProjectID NodeID
    Archived bool
    Kind string // Issue, PullRequest, DraftIssue, Redacted
    Repo RepoRef
    Issue *Issue
    Placement Placement
}
type CreateInput struct { RepositoryID NodeID; Title, Body string; AssigneeIDs, LabelIDs []NodeID }
type IssuePatch struct { Title, Body *string; AssigneeIDs *[]NodeID }
```

- ProjectItem.Kind is Issue, PullRequest, DraftIssue or Redacted.
- ProjectItem.ProjectID identifies membership; Archived is retained for targeted write preflight.
- An IssuePatch nil pointer means omit the key; an empty pointed-to slice replaces all assignees with none.
- CreateInput contains validated IDs; no project IDs are sent in create because placement is a separate step.

## Quick add and local draft

```go
type CheckedMode string // skip, close
type QuickAddOptions struct {
    Assignees, Labels []string
    AddToBoard bool
    Status *string
    Checked CheckedMode
}
type QuickAddItem struct {
    Line int
    Title, Body string
    Checked bool
    Source string // exact source lines for this item, including body and separators
    StartByte, EndByte int // half-open slice of original input
}
type Problem struct { Line int; Message string }
type QuickAddPlan struct {
    Source string
    Items []QuickAddItem
    Options QuickAddOptions
    Problems []Problem
    ToCreate, ToSkip, WriteCount int
}
type QuickAddOutcome string // created, failed, uncertain, unattempted, skipped, planned
type QuickAddItemResult struct {
    Item QuickAddItem
    Result QuickAddOutcome
    Issue *Issue
    OnBoard, Closed bool
    Error *Error
}
type QuickAddCounts struct { Created, Failed, Uncertain, Unattempted, Skipped, Planned int }
type QuickAddResult struct { Items []QuickAddItemResult; Counts QuickAddCounts }
type QuickAddProgress struct {
    Index int // zero based, same item order as Plan.Items
    Phase string // creating, closing, placing, settled
    Outcome QuickAddOutcome // empty while a step is active
    Number int // zero until creation acknowledged
    Err *Error
    Item *QuickAddItemResult // immutable snapshot when settled
}
type QuickAddRunOptions struct {
    DryRun bool
    StopRequested func() bool // thread-safe; checked only between complete items
    OnProgress func(QuickAddProgress) error // synchronous, ordered; can fail draft save
}
type Draft struct {
    Version int
    RepoKey, Text string
    UpdatedAt time.Time
    Uncertain []DraftSpan
    InFlight *DraftSpan // crash while sending becomes uncertain on reload
    Revision uint64
}
type DraftSpan struct { StartByte, EndByte int; SourceSHA256 string }
```

### Quick-add invariants

- CheckedMode values: skip (default), close.
- QuickAddOutcome values: created, failed, uncertain, unattempted, skipped, planned.
- Phases: creating, closing, placing, settled; Index is zero-based input-item index.
- Source, StartByte and EndByte preserve the exact original item source, including CRLF and separator lines.
- A created-but-unplaced/unclosed row remains created, with an error and accurate flags.
- Counts.Created includes every acknowledged create; Counts.Planned is used only in dry-run.
- Dry-run has Planned/Skipped counts and zero Created/Failed/Uncertain/Unattempted.
- OnProgress runs synchronously on the worker, in order. A returned error stops subsequent writes.
- StopRequested is checked between whole items; context cancellation may interrupt a current request.
- Callbacks receive owned snapshots; never pass a mutable live result slice.
- Draft stores only user text, local byte spans, hashes, send markers and a local revision.
- No token, fetched GitHub content, confirmed issue number or remote-result cache is stored in a draft.
- InFlight survives through close/placement until settled. Reload converts it to Uncertain.
- Revision orders one TUI's saves only; it is not a cross-process conflict token. Concurrent processes use last save wins.
- Draft choices reset on opening: checked=skip; shared metadata defaults come from the invoking view.

## Error semantics

- Every package-boundary failure is `*Error`; Error returns Message, Unwrap returns Err.
- ErrorKind constants in the first block are the exhaustive public kind list.
- Partial uses Cause for the underlying kind, Issue for best-known acknowledged state, Completed for confirmed steps.
- Public steps: created, commented, added_to_board. Restoring an archived card counts as added_to_board.
- Internal step removed is allowed only for a TUI picker whose removal succeeded and addition failed.
- A mutation with a network/timeout/5xx/malformed-answer failure after its request starts has Uncertain=true. No httptrace or byte-written tracking.
- Explicit API rejections (auth, scopes, rate limit, forbidden, validation) remain definite rejections.
- Cancellation: Kind=network, Message="operation interrupted", Err wraps context.Canceled. Signal origin determines CLI exit130.
- RetryAfter is a duration; CLI exposes ceil(seconds) only for rate_limited.
- Candidates and Line are internal diagnostic data, never implicit additions to JSON.
- no_board discrimination: empty Candidates means no linked board; more than one means ambiguous linked boards; one unusable project means the selected project lacks a single-select Status field.
- Err is never printed, logged or serialised directly; presenters use the safe Message/Hint.

## Ownership helpers

Implement these as pure deep copies; copy nested pointers and every slice. No reflection is needed.

```go
func CloneIssue(Issue) Issue
func ClonePR(PullRequest) PullRequest
func CloneBoard(Board) Board
func ClonePlan(QuickAddPlan) QuickAddPlan
func CloneQuickAddResult(QuickAddResult) QuickAddResult
func CloneDraft(Draft) Draft
```

## Files and named tests

| File | Contents |
|---|---|
| repository.go | RepoRef, Repository, User, Session, CredentialInfo |
| issue.go | Issue, IssueDetail, Label, Comment, inputs and change results |
| pull_request.go | PullRequest, PRDetail and enums |
| board.go | Project/placement/board and backend project records |
| page.go | PageRequest, Page, filters |
| quick_add.go | Plan, options, progress, results, draft |
| error.go | ErrorKind, Error and its methods |
| clone.go | Ownership helpers |

- TestError_ErrorUnwrapAndAs: safe message, wrapped cause, errors.As/Is survive partial wrapping.
- TestErrorKinds_Exhaustive: all 16 kinds match the functional spec.
- TestCloneIssue_IndependentNestedValues; TestCloneBoard_IndependentCards; TestCloneQuickAdd_IndependentPlanAndResults.
- TestPage_EmptySlicesAndRawCursor; TestPlacement_AbsentVersusNoStatus.
- TestQuickAddCounts_PlannedSeparateFromCreated; TestDraftClone_InFlightAndUncertain.
- JSON formatting tests live in cli; domain data must not silently become the public schema.

No nonstandard dependency.
