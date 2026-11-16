---
status: complete
---

# Architecture: todos

This document fixes the structure, the contracts between packages, and every
cross-cutting decision. The per-package designs are in `components/`:

| Component doc | Packages | Owner |
|---|---|---|
| `components/domain.md` | `internal/domain` | @lead-dev |
| `components/github.md` | `internal/github` | @lead-dev |
| `components/service.md` | `internal/service`, `internal/quickadd`, `internal/config` | @lead-dev |
| `components/cli.md` | `internal/cli`, `main.go`, the skill file | @lead-dev |
| `components/tui.md` | `internal/tui` | @frontend-dev |
| `components/markdown.md` | `internal/markdown` | @frontend-dev |

Where a component doc and this document disagree, this document wins and the
component doc is wrong. References like (4.4) point to the functional spec and
(UI 3.5) to the UI design.

## 1. Principles

1. **One behaviour, two faces.** The CLI and the TUI call the same service
   methods. Neither talks to GitHub. A rule (validation, a multi-step change,
   quick add) is written once, in the service.
2. **GitHub is the only source of truth.** No database. A disposable on-disk
   cache holds copies of GitHub data under the rules in `components/cache.md`:
   issue lists are used only after GitHub confirms nothing changed, and
   slow-changing structure is reused for minutes. Board cards are never
   cached. Deleting the cache is
   always safe. The other files are the config file and quick-add drafts.
3. **The service is synchronous; concurrency belongs to the caller.** Every
   service method takes a context, blocks, and returns. The TUI turns calls
   into commands; the CLI just calls.
4. **Errors are data.** One error type with a kind. Every layer returns it
   unchanged; only the two faces turn it into words, exit codes and hints.
5. **Untrusted text is sanitised at the edge.** Everything read from GitHub
   passes through `markdown`'s sanitiser before it is written as text, whether
   to a terminal or to a pipe, since piped output is usually read in a pager.
   JSON output is the exception: `encoding/json` escapes control characters.
6. **No test needs the network.**
7. **Prefer the boring mechanism.** When a simple rule covers the cases this
   product actually has, it beats a general one. Section 9 lists where that
   was applied.

## 2. Layout

Module `github.com/pkieltyka/todos`, Go 1.27. `main.go` is at the module root,
so `go install github.com/pkieltyka/todos@latest` produces a binary named
`todos`.

```
main.go                  embeds the skill, calls cli.Main
skills/todos/SKILL.md    the agent skill (go:embed)
internal/cli             cobra command tree, output renderers, exit codes
internal/tui             Bubble Tea v2 application
internal/markdown        sanitiser, glamour renderer, render cache
internal/service         use cases; the only package the faces call
internal/service/servicetest   in-memory fake Service with fixtures and gates
internal/quickadd        punch-list parser (pure)
internal/config          config file and draft files
internal/cache           file store for the read-through cache (no policy)
internal/github          GitHub client on go-gh
internal/domain          plain types and the error type
```

Import rules, enforced by a test that inspects imports
(`TestImportRules` at the module root):

| Package | May import (of ours) |
|---|---|
| `domain` | nothing |
| `quickadd` | `domain` |
| `config` | `domain` |
| `cache` | `domain` |
| `markdown` | nothing |
| `github` | `domain` |
| `service` | `domain`, `quickadd` |
| `service/servicetest` | `domain`, `service`, `quickadd` (from Phase 5, for the fake's quick-add planning) |
| `tui` | `domain`, `service`, `markdown`, `quickadd` (only for `Remaining`, so the rule for which text is still unsent exists once) |
| `cli` | everything above |

- `service` defines the three interfaces it depends on: `Backend` (implemented
  by `github`), `SettingsStore` (implemented by `config`) and `CacheStore`
  (implemented by `cache`; nil means no cache). `tui` defines
  `DraftStore` (implemented by `config`).
- `cli` is the only place that constructs `github`, `config` and `service`
  and wires them together.

## 3. Who owns which state

| State | Owner | Lifetime |
|---|---|---|
| Credentials, HTTP clients | `github` | process |
| Authenticated user, repository | `service`, memoised for 5 minutes, persisted in the cache | across processes |
| Labels, assignable users | `service`, memoised for 5 minutes, persisted in the cache | across processes |
| Resolved board with its Status field and options | `service`, memoised for 5 minutes, persisted in the cache; `Board` reads the fields from GitHub on every call and replaces them | across processes |
| Issue list pages, each with the change token it was read under | `service` policy, `cache` files | until the token changes, a write through `todos`, or 10 minutes |
| Last seen rate-limit numbers | `github`, read through `service.RateLimit()` | process |
| Issues, pull requests, board contents, comments | `tui` store | TUI session |
| In-flight changes and their optimistic effect | `tui` | until settled |
| Board choice per repository | `config` file | persistent |
| Quick-add draft per repository | `config` draft files | until sent or discarded |

The service never caches board cards, one issue, one pull request, comments,
pull request lists or search results. Issue list pages are cached only under
the probe rule in `components/cache.md`.

## 4. The service seam

The `Service` interface is the contract between the two halves of the team. It
lives in `internal/service`; types are in `internal/domain`.

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

Rules of the seam:

- **Reads are paged by cursor.** A list call fetches exactly one page from
  GitHub and returns it with `Total`, `NextCursor` and `HasMore`. The CLI
  loops until it has `--limit` items or `HasMore` is false. The TUI issues one
  command per page. There is no streaming API. The cursor is GitHub's own
  cursor string, passed back unchanged; the caller must repeat the same
  filter.
- **One issue shape.** Lists, the board (`Card` embeds `Issue`) and
  single-issue reads all return the same `domain.Issue`, filled from one
  shared GraphQL fragment, including the issue's placement on the selected
  board. Body and comments are separate reads.
- **Changes are small verbs.** Each returns `domain.Change{Issue, Changed}`:
  the issue as GitHub acknowledged it, and whether anything changed. Nothing
  sends a whole issue back to GitHub.
- **Names in, IDs inside.** Callers pass label names, logins (`@me` allowed)
  and status names. The service validates them against memoised metadata and
  returns `invalid_input` listing the valid values. On an unknown name it
  refreshes the metadata once before rejecting.
- **Every mutation re-reads its target first**, by number, to get the node ID
  and current state, and to decide whether it is a no-op.
- **Multi-step changes are one call** and return `partial` when a later step
  fails (section 6).
- **Every error is a `*domain.Error`**, found with `errors.As`.
- `SetStatus(n, nil)` puts the issue on the board with no status.
  `BoardAdd(n, nil)` adds it and leaves an existing card untouched.
  `NewIssue` and `QuickAddOptions` carry `AddToBoard bool` next to
  `Status *string`: the Board view sets `AddToBoard` always, so creating from
  the "No status" column adds the card with no status, while the CLI sets it
  only when `--status` is given.

## 5. Optimistic changes in the TUI

(4.4) requires that assign, label, close, reopen and move show immediately,
survive a refresh while pending, roll back on rejection, and resolve an
unknown outcome by re-reading. The design is a replayed operation log.

**Model.** The TUI store keeps, for every issue number it has seen:

- `base`: the issue as last confirmed by GitHub.
- `asOf`: when the request that produced `base` was *sent*. For an issue
  returned by a write, `asOf` is when the write's response arrived.

and the pending set keeps, per issue, an ordered list of **changes**. A change
has an ID, a patch (assignee delta, label delta, state, or board placement),
and a phase (`queued`, `in flight`, `uncertain`). Each patch has a pure
`Apply(Issue) Issue` with set semantics, so applying it twice, or to an issue
that already has the change, is harmless.

**What is drawn** is always `Effective(base, changes)`: the base with every
pending change replayed on top, in order. The base is never modified
optimistically. Pending markers come from comparing the two.

**Rules.**

1. *Start.* The user acts; a change is appended. The patch is computed against
   the effective issue, so a picker opened on an issue with a pending `+docs`
   shows `docs` ticked.
2. *Send order.* Changes to one issue are sent one at a time, in order.
   Changes to different issues run in parallel, at most four at once.
3. *Success.* The change is removed; the returned issue becomes `base` with
   `asOf` = now; the next queued change for that issue is sent. There is no
   immediate extra read after a successful change: the service already read
   the issue just before writing, and the write returns it as GitHub
   acknowledged it. One exception, found in dogfooding: GitHub project
   workflows act a few seconds after a write (a new issue is auto-added to a
   column; a card moved to Done closes its issue). So after a create, a status
   change or a removal from the board settles, the TUI re-reads that issue
   once, five seconds later, through the normal read path. One timer per
   issue; a newer change restarts it; it is skipped while the issue has
   pending changes.
4. *Rejected.* The change is removed, so the fold no longer includes it and
   the screen returns to the truth with no rollback code. The error is shown.
   Later queued changes for that issue still run: they are independent set
   operations and are what the user asked for.
5. *Uncertain* (a write whose outcome is unknown). The change stays, marked
   `uncertain`, and **that issue's queue is held**. The issue is re-read by
   number. When a read sent after the failure lands, it becomes `base`, the
   uncertain change is removed, and the queue resumes. Holding the queue keeps
   a late-landing write from crossing with the next one.
6. *Refresh while pending.* A refresh replaces `base` and never touches the
   changes. The fold keeps pending values on screen.
7. *Stale data never wins.* An incoming copy of an issue replaces `base` only
   if its `asOf` is not older than the current one. This is the single
   ordering rule for every source: list pages, board loads, detail reads and
   write results. It is deliberately not based on GitHub's `updatedAt`, which
   does not change when a card moves on a board.

**The board is derived.** Board data is an ordered list of issue numbers per
status; the issues go into the same store. A column shows the issues whose
*effective* status is that column: first those whose effective status equals
their base status, in GitHub's order, then the rest at the bottom. Moving a
card is a board patch on an issue; there is no board-specific optimistic code.

Create, edit, comment and delete are not in this log: they wait for GitHub.

## 6. Errors

`domain.Error` carries `Kind`, `Message`, `Hint`, `RetryAfter`, `Uncertain`,
`Completed`, `Cause`, an optional `Issue`, `Candidates` (the projects to offer
when the board is ambiguous; TUI only), `Line` (quick-add diagnostics) and the
wrapped original error, which is never shown or serialised. The kinds are
exactly those of (6.5).

- **Who creates which kind.** `github` creates everything that comes from a
  GitHub response or the transport. `service` creates `invalid_input`,
  `no_repo`, `no_board`, `issues_disabled`, `config` and `partial`. `cli`
  creates `usage`.
- **Classification** of GitHub responses follows the order of (6.5) and is
  specified exactly in `components/github.md`. `missing_scope` is used only
  when GitHub says so; a refusal with no named scope is `forbidden`.
- **Cancellation** is wrapped as kind `network` with the message "operation
  interrupted" and keeps `context.Canceled` reachable through `Unwrap`. The
  CLI exits 130 when the cancellation came from a signal. The TUI drops
  results of requests it cancelled itself.
- **Uncertain.** A mutation that fails with a timeout, a network error, a 5xx
  or a malformed answer *after the request was started* has `Uncertain` set.
  It may have applied. No mutation is ever retried automatically, and no query
  either: the next refresh is the retry.
- **Partial.** A multi-step change that fails after a confirmed step returns
  kind `partial` with `Completed`, `Cause` and the `Issue` as far as it got.
  Public step names are `created`, `commented` and `added_to_board` (6.5).
  Bringing an archived card back counts as `added_to_board`. One internal
  step name exists, `removed`, for a TUI picker apply whose removal succeeded
  and whose addition failed; no CLI command can produce it.
- **Timeouts.** Every HTTP request has a 30 second timeout, applied in
  `github`.

## 7. Cross-cutting decisions

1. **GitHub API.** Everything is GraphQL through go-gh, with literal documents
   kept in `internal/github/queries/*.graphql` and embedded. The one REST call
   is `GET /user`, used by `doctor` for scope headers. No query is built by
   string interpolation of user text.
2. **Repository resolution**: `--repo`, then `GH_REPO`, then the git remote
   chosen by go-gh's `repository.Current()`. We do not reimplement gh's remote
   rules.
3. **Board resolution** follows (3.2): `--project`, then the remembered
   project, then the projects linked to the repository. `BoardCandidates`
   lists more (linked, then owned by the repository owner, then owned by the
   viewer) so the user has something to pick from.
4. **Board status in issue reads is best effort.** If the board cannot be
   resolved or read, issue reads run without it and `Issue.Board` is nil. A
   board problem never fails an issue read (6.4).
5. **Archived cards.** Board reads exclude archived items. A change that
   targets an issue whose card is archived (`SetStatus`, `BoardAdd`) restores
   the card first, then proceeds. Reading never restores anything.
6. **Board size.** `Board` reads at most 1,000 project items and reports
   `Truncated` beyond that. The TUI draws at most 500 cards and says so. The
   CLI prints every card that was read.
7. **Search.** Only `todos list --search` uses GitHub Search, which exposes at
   most 1,000 results. `--search` with `--all` fails with `invalid_input` and
   a hint to narrow the query when there are more; with `--limit` it returns
   what is available and `truncated: true`.
8. **Rate limits.** `github` records the rate-limit headers of every response
   per resource (`core`, `graphql`, `search`). `RateLimit()` returns the
   tightest known bucket. The TUI pauses auto-refresh when under 10% remains.
9. **Pacing.** Only `RunQuickAdd` paces: at least one second between the end
   of one write and the start of the next. Other writes are not paced.
10. **Doctor never writes.** It reports what GitHub says the caller can do
    (`viewerCanCreateIssues`, `viewerCanUpdate`), plus scope names where the
    token exposes them. It always prints its report on standard output; the
    exit code carries the verdict.
11. **Config warnings.** A config file that cannot be parsed is reported as a
    warning line on standard error in text mode, is silent with `--json`, and
    is a failing check in `doctor`.
12. **Drafts.** The quick-add draft is saved 500 ms after the last keystroke
    and at every step change. During a run, the draft is rewritten before each
    item is sent (marking it in flight) and after it settles (removing what
    was created), so a crash can never cause an item to be sent twice: an item
    that was in flight comes back flagged "may exist".
13. **Logging.** No log files and no debug output by default. go-gh's own
    logging is switched off so a debug variable cannot print tokens or bodies.

## 8. Testing strategy

| Package | How it is tested |
|---|---|
| `domain` | Unit tests for the error type. Domain types are never serialised; JSON shapes and conventions are tested in `cli`. |
| `quickadd` | Table tests and a fuzz test for the parser. |
| `github` | Fake `http.RoundTripper` with fixture request/response pairs: every document, paging, mapping, and the whole error-classification table. |
| `service` | Fake `Backend` and fake `SettingsStore`: resolution, validation, no-ops, every multi-step change and its partial outcome, quick add with an injected clock so nothing sleeps. |
| `config` | Temp directories with `XDG_*` set: atomic writes, never overwriting an unparseable file, draft round trips. |
| `cli` | Commands run against `servicetest`'s fake, checking stdout, stderr and exit code. Golden files for JSON and text. |
| `tui` | Pure tests for layout, pending changes, reselection and keys; model tests that drive `Update` directly with `servicetest`'s fake; golden renders at 80×24 and 140×40; a handful of end-to-end tests with teatest. |
| `markdown` | Sanitiser table tests, a fuzz test that the output contains only colour and hyperlink sequences, render tests. |
| whole module | `TestImportRules`; `TestSkillMatchesCLI` (every command and flag in `SKILL.md` exists). |

- `servicetest.Fake` is shared by `cli` and `tui` tests. It holds fixtures (a
  repository with issues, pull requests, labels, users and a board) and
  **gates**: a test can hold a call, release it, or make it fail with a given
  error, so races can be staged in any order.
- **The fake is a direct in-memory implementation of `Service`. It does not
  wrap the real service.** It applies each change to its fixture with the same
  set semantics and pages its lists, and that is all: it does not re-implement
  validation or multi-step rules, because tests stage those outcomes with
  gates. This is what lets the TUI track build against it from Phase 1, before
  the real service exists. The real service's rules are tested in `service`
  itself, against a fake `Backend`. Where `components/service.md` describes the
  fake as delegating to the real core, this paragraph overrules it.
- Clocks, timers and the terminal are injected. No test sleeps.
- Live tests against the dogfood repository are opt-in (`TODOS_LIVE_TESTS=1`)
  and read-only.
- The check command for every phase is
  `gofmt -l . && go vet ./... && go test -race ./...`.

## 9. Where the simple mechanism was chosen

Recorded so nobody re-adds these during implementation. Each was proposed and
deliberately not taken for v1.

| Not in v1 | What we do instead |
|---|---|
| A structured, self-validating page cursor | GitHub's cursor string, passed through |
| Reimplementing gh's git-remote resolution | `repository.Current()` |
| File locks and cross-process conflict detection for config and drafts | Atomic write (temp file, rename, mode 0600). Two `todos` processes editing the same draft: last save wins |
| Preserving unknown keys in `config.yml` through a YAML node tree | A versioned struct with an inline map for unknown top-level keys |
| Per-issue mutation locks and a global request semaphore in the service | The TUI already sends one change per issue at a time; a CLI run does one thing |
| A process-wide write pacer | Pacing inside quick add only |
| Tracking whether request bytes were written, to decide "uncertain" | Any mutation failure after the request started is uncertain |
| Custom redirect handling | go-gh's default client |
| Paging an issue's labels or project memberships past 100 | The first 100; GitHub caps labels per issue at 100 |
| Extra public step names for `partial` | The three of (6.5), plus internal `removed` |
| An in-app vim mode | `$VISUAL` / `$EDITOR` hand-off |
| A test fake that runs the real service over an in-memory backend | A direct in-memory fake (section 8), so the two tracks do not wait on each other |
| A read of the issue after every successful change | The issue the write returns, plus one delayed re-read after a create or a board change (section 5, rule 3) |

## 10. Dependencies

| Module | Version | For |
|---|---|---|
| `github.com/cli/go-gh/v2` | v2.17.0 | API clients, credentials, repository resolution, browser |
| `github.com/spf13/cobra` | v1.10.2 | commands |
| `go.yaml.in/yaml/v3` | v3.0.6 | config file |
| `golang.org/x/term` | v0.48.0 | terminal detection and width in the CLI |
| `charm.land/bubbletea/v2` | v2.1.1 | TUI runtime |
| `charm.land/bubbles/v2` | v2.2.1 | textarea, textinput, viewport, spinner, key, help |
| `charm.land/lipgloss/v2` | v2.1.0 | styles, layers |
| `charm.land/glamour/v2` | v2.0.2-0.20261015065741-010543cac1a0 | Markdown (the version go-gh v2.17.0 requires) |
| `github.com/charmbracelet/x/ansi` | v0.11.9 | strip, truncate, wrap |
| `github.com/google/shlex` | v0.0.0-20191202100458-e7afc7fbc510 | splitting `$EDITOR` |
| `github.com/yuin/goldmark` | the version glamour selects | finding raw HTML outside code, for the `<img>` rewrite |
| `golang.org/x/net` | the version bluemonday selects | reading `<img>` attributes (`html` tokenizer) |
| `github.com/charmbracelet/x/exp/golden` | v0.1.0 | golden files (tests) |
| `github.com/charmbracelet/x/exp/teatest/v2` | v2.0.0-20261101151554-27ecf3e4d454 | end-to-end TUI tests |

All of them go into `go.mod` in the first implementation phase, so the two
tracks never edit `go.mod` at the same time. Until real code imports them, a
file `internal/tools/deps.go` behind the build tag `deps` blank-imports each
one, so `go mod tidy` keeps the pins. It is deleted in the last phase. No second GitHub SDK, no GraphQL
code generator, no database, no HTTP cache: the read-through cache is our own
(`components/cache.md`), and go-gh's HTTP cache stays off.
