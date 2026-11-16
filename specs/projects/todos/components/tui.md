---
status: complete
---

# Component: tui (`internal/tui`)

The Bubble Tea v2 application behind `todos` and `todos tui`. Owner:
@frontend-dev.

`architecture.md` is the frame and wins over this document where they differ.
References: (4.4) = functional spec, (UI 3.5) = ui_design.md, (A5) =
architecture section 5. Domain types are defined in `components/domain.md`;
the `Service` interface is in A4.

Library facts here were checked against the source of the pinned versions
(A10). Facts read in source but not executed are marked *(read)*. The few I
could not verify are marked **UNVERIFIED**.

---

## 1. Principles

1. **One goroutine owns the state.** All TUI state changes only in `Update`.
   A `tea.Cmd` calls the service, the renderer or the draft store and returns
   a message. No Cmd touches a model.
2. **Data lives in one store; screens hold only UI state.** Screens keep a
   cursor (by issue number), a scroll offset and filter text. Issues, PRs, the
   board and details live in `Store`, so a change shows everywhere at once.
3. **What is drawn = base + replayed changes** (A5). The base only ever comes
   from GitHub. Optimistic state is a list of patches folded on top when
   drawing.
4. **One keymap.** Every key goes through it. The footer and the help screen
   are generated from it.
5. **Everything from GitHub is sanitised** through `markdown` before it is
   drawn (A1.5).

---

## 2. Package layout

One flat package (as built). Subpackages for screens, overlays and the editor
would need `Ctx` and would cycle back into `tui`.

```
internal/tui/
  app.go            App (root model), Run, Deps, Options, Ctx, dispatch, frame
  do.go             reads: slots, Cmd wrappers, every load and its handler
  changes.go        the pending-change engine's App side: send, settle, release
  pending.go        PendingSet, Change, Patch, Apply, Effective (pure)
  actions.go        per-issue actions, picker openers, status choices, create/delete
  msgs.go           read message types
  store.go          Store, ListCache, BoardCache, detail entries, asOf rule, Columns
  keys.go           Action, Keymap, DefaultKeymap, footer and help texts
  layout.go         ComputeFrame, Fit, BoardWindow, reselect
  theme.go          Theme, NewTheme, Dimmed, ParseOverride
  format.go         times, counts, error texts and hints
  widgets.go        filter input, glyphs, label and people cells, pending markers
  editor.go         Editor: textarea + preview + $EDITOR hand-off (14)
  draft.go          quick-add draft saves, the run's draft transitions (12)
  reread.go         the delayed re-read after creates and board changes (8.12)
  screen.go         Screen and Overlay interfaces
  screen_*.go       IssueList, IssueDetail, PRList, PRDetail, Board, BoardSetup
  overlay_*.go      Help, Picker, IssueForm, CommentForm, Confirm, Conflict,
                    DeleteConfirm, QuickAdd
```

`tui` imports `domain`, `service` and `markdown` (A2). Tests also import
`service/servicetest`.

---

## 3. Entry point and dependencies

```go
package tui

type Deps struct {
    Service  service.Service
    Drafts   DraftStore
    Renderer *markdown.Renderer
    Clock    func() time.Time        // time.Now; fixed in tests
    Open     func(url string) error  // go-gh pkg/browser; fake in tests
    Env      Env
    Tick     func(d time.Duration, fn func(time.Time) tea.Msg) tea.Cmd // tea.Tick; recorded by the harness
    Exec     func(cmd *exec.Cmd, fn tea.ExecCallback) tea.Cmd       // tea.ExecProcess; run directly by the harness
    Go       func(f func())    // starts the quick-add run goroutine; tracked by the harness (12.4)
    Send     func(tea.Msg)     // Program.Send, set by Run; nil = stream through a channel (12.4)
}

type Env struct {
    Theme  string   // TODOS_THEME as read by cli: "", "light" or "dark"
    Editor []string // shlex.Split($VISUAL, else $EDITOR); nil when both are empty
    NoColor bool    // --no-color: Run forces colorprofile.ASCII; Markdown uses Plain
}

type Options struct {
    StartView ViewID          // ViewIssues | ViewPRs | ViewBoard
    Repo      domain.RepoRef  // resolved by cli before the TUI starts
}

// DraftStore is defined here and implemented by internal/config (A2).
type DraftStore interface {
    Load(repo domain.RepoRef) (domain.Draft, bool, error)
    Save(d domain.Draft) error
    Delete(repo domain.RepoRef) error
}

func New(deps Deps, opts Options) *App
func Run(ctx context.Context, deps Deps, opts Options) error
func (a *App) Init() tea.Cmd
func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd)
func (a *App) View() tea.View
```

- `Run` derives a child context it owns, builds
  `tea.NewProgram(New(deps, opts), tea.WithContext(child))`, runs it, and
  cancels the child on every exit (`defer`). Every read descends from it, so
  no Service call outlives the program, whether it ended by `q`, `ctrl+c` or
  the caller's cancellation (review finding 9).
- `cli` reads `TODOS_THEME`, `VISUAL` and `EDITOR` once and splits the editor
  command with `github.com/google/shlex`, which go-gh's `pkg/browser`
  already uses. `cli` also prints a warning on standard error for an invalid
  `TODOS_THEME` value before the TUI starts.
- `DraftStore` methods are called only from Cmds or the quick-add run
  goroutine, never from `Update`, except the synchronous save on quit
  (`flushDraftNow`, 12.2).
- `cli` builds `Deps.Renderer` with
  `markdown.NewRenderer(markdown.Options{BaseURL: "https://HOST/OWNER/REPO"})`
  right after resolving the repository locally (no GitHub read). The first
  frame stays network-free (10).
- `CloseOptions.Reason` is `"completed"` or `"not_planned"`, the internal
  spelling (agreed with @lead-dev).

`Init` returns a batch of these commands (as built, `app.go` `Init`):

1. `loadSession`
2. `loadBoard`: one request that reads BoardInfo, then the board
3. the first auto-refresh tick (`scheduleAuto`) and the clock tick (`clockCmd`)
4. `tea.RequestBackgroundColor`, unless the theme is fixed
5. the start view's first load, for Issues or Pull requests (the board comes
   from 2)
6. `spinCmd`, which starts the spinner while those reads are in flight

The first frame draws immediately, with loading states (10).

---

## 4. Model tree

As built (`app.go`):

```
App
├── deps, opts, ctx
├── width, height, frame, fits        size and the too-small state (4.4)
├── theme, themeFixed, keys
├── store      *Store                 GitHub data (section 7)
├── inflight   map[Slot]*Request      cancellable reads (section 6.3)
├── reqSeq     ReqID                  increases only
├── tabs       [3][]Screen            Issues, PullRequests, Board; each a stack, list → detail
├── active     ViewID
├── overlays   []Overlay              modal stack; the top one has focus
├── toast, toastSeq                   one footer toast
├── spin, spinGen, spinning           the shared spinner frame and its tick chain (spinFrames)
├── autoGen, resizeGen, frozen, clockOn
├── rendering, lastRender, renderVer  Markdown renders in flight and last outputs (16)
├── pending    *PendingSet            optimistic changes (section 8); it holds the ChangeID sequence
├── formSeq    uint64                 form and overlay IDs
├── writes     map[string]*formWrite  form writes in flight (8.11)
├── rr         rereads                delayed re-reads (8.12)
└── drafts     draftQueue             quick-add draft saves, one at a time (12.2)
```

There is no per-tab banner field (banners are drawn from the store's errors)
and no `spinner.Model`. A quick-add run lives in its `QuickAdd` overlay.

### 4.1 `Ctx`

```go
type Ctx struct {
    a      *App           // unexported: helpers below use it
    Store  *Store         // read-only for screens
    Theme  Theme
    Keys   Keymap
    Now    time.Time
    Frame  Frame
    Spin   string         // current spinner frame
    Viewer string         // Session.Viewer.Login
    Repo   domain.RepoRef
}

func (c *Ctx) Issue(n int) (domain.Issue, bool)   // the effective issue (8.11)
func (c *Ctx) Marks(n int) Marks
func (c *Ctx) Columns() []DisplayColumn           // the derived board (7.5)
func (c *Ctx) Push(s Screen)
func (c *Ctx) Pop()
```

`App` builds a `Ctx` for every `Update` and `View`. Screens and overlays
change their own UI state; to read or write they call App methods through
`c.a` (`loadIssue`, `issueAction`, `applyChange`, …), never the service and
never the store directly. There are no `Intent` values (6.1).

### 4.2 Screens

```go
type Screen interface {
    ID() ScreenID
    Update(c *Ctx, msg tea.Msg) tea.Cmd             // ActionMsg in nav mode, raw keys in text mode
    Sync(c *Ctx)                                    // recompute derived state after every Update
    View(c *Ctx, area Rect) string
    SubBar(c *Ctx, w int) string                    // the line under the tabs
    Actions(c *Ctx) []Action                        // enabled actions, footer priority order
    Mode() InputMode                                // ModeNav, or ModeText while the filter has focus
    Title(c *Ctx) string                            // tea.View.WindowTitle, e.g. "todos · #142"
    Position(c *Ctx) string                         // footer right side: "12/342", "38%"
    Cursor(c *Ctx) *tea.Cursor                      // real cursor in text mode
    Slots() []Slot                                  // read slots to cancel when popped
    Renders(c *Ctx) []renderReq                     // Markdown the screen wants rendered (16)
}
```

| Screen | UI state it owns |
|---|---|
| `IssueList` | `sel int` (issue number), `prevIndex int`, `offset int`, `filter lineInput`, `filtering bool`, `rows []int`, `height int` |
| `IssueDetail` | `number int`, `from` (the tab it was opened from, for `‹ Board`), embedded `scroller{vp viewport.Model, anchors []int}` (line of each comment header), `head`, `panel []string` (drawn header and side panel) |
| `PRList` | `sel`, `prevIndex`, `offset`, `filter`, `filtering`, `mine bool` (local filter), `rows`, `height` |
| `PRDetail` | `number`, `scroller`, `head`, `panel` |
| `Board` | `col int` (index into displayed columns), `focusKey string` (the focused column's option ID), `sel int` (issue number), `prevIdx, scroll map[string]int` (per column key), `colOffset int`, `filter`, `filtering`, `mine bool` (local filter), `cols []DisplayColumn`, `total int` |
| `BoardSetup` | `state, prev SetupState` (NoBoard, Several, MissingScope, NoStatusField, Failed, Choosing), `err *domain.Error`, `projects []domain.Project`, `cursor`, `loading`, `candErr *domain.Error`, `pushed bool` (opened with `P` from the board: `esc` pops back) |

`lineInput` (`widgets.go`) is a small one-line input; bubbles' `textinput` is
not used.

`SetupState` comes from the `BoardInfo` error:

| Error | State |
|---|---|
| kind `missing_scope` | MissingScope |
| `no_board`, no `Candidates` | NoBoard (no board linked) |
| `no_board`, exactly one candidate | NoStatusField (that project has no usable Status field) |
| `no_board`, more than one candidate | Several (projects to pick from) |
| any other kind | Failed (message and hint) |

The state is chosen by **counting** `Error.Candidates`, not by inspecting
`Usable` (`domain.md`, `service.md`).

Keys in BoardSetup:

| State | `P` choose project | `enter` | `r` |
|---|---|---|---|
| NoBoard | ✓ switches to Choosing and lists `BoardCandidates` on this screen (UI 3.16) | | ✓ |
| NoStatusField | ✓ the same | | ✓ |
| Several, Choosing | ✓ only after the candidate list failed (retry) | ✓ use the highlighted project (`ChooseBoard`) | ✓ |
| MissingScope | | | ✓ |
| Failed | | | ✓ |

**Navigation stack.** Each tab has its own stack. `enter` pushes a detail
screen. `esc` pops it and cancels its `Slots()`, and never quits. Opening an
issue from the board pushes `IssueDetail` onto the Board tab's stack, so `esc`
returns to the same card. From the Board, `P` pushes a
`BoardSetup(Choosing)`; there is no project picker overlay. Switching tabs (`1` `2` `3` `tab` `shift+tab`)
keeps every stack.

When `BoardInfo` fails with `no_board`, `missing_scope`, or "no Status
field", the Board tab's stack is replaced by `[BoardSetup]`. When the board
becomes available again, it is replaced by `[Board]`.

### 4.3 Overlays

```go
type Overlay interface {
    // Update gets ActionMsg for its actions, raw keys otherwise (and always
    // in text mode), and every write result, which it ignores unless it is
    // its own (results are given to every open overlay).
    Update(c *Ctx, msg tea.Msg) (tea.Cmd, OverlayResult)
    View(c *Ctx, body Rect) (content string, box Rect)
    Actions(c *Ctx) []Action
    Hints(c *Ctx) []hint           // its footer
    Position(c *Ctx) string        // footer right side, e.g. "L4:C19"
    Mode() InputMode
    Cursor(c *Ctx) *tea.Cursor     // real cursor in text mode, in frame coordinates
    Busy() bool                    // its own request is in flight
}

// Optional: overlays with an editor implement layout(c), called after every
// Update so the editor is sized before its first draw, and Renders(c) for the
// preview.

type OverlayResult int
const ( Keep OverlayResult = iota; Close )
```

Overlays are a stack, so the form's Assignees row can open a picker on top
of the form.

| Overlay | State it owns |
|---|---|
| `IssueForm` | `formID`, `edit bool`, `number` (edit), `fromBoard`, `title lineInput`, `body Editor`, `assignees, labels []string`, `status *string`, `addToBoard bool`, `focus formField`, `orig *domain.IssueDetail` (edit: read by the form itself when it opens), `loading`, `saving bool`, `err *domain.Error` |
| `CommentForm` | `formID`, `number`, `body Editor`, `sending`, `err` |
| `Picker` | `kind` (Assignees, Labels, Status), `title`, `items []PickItem`, `initial, selected map[string]bool`, `order []string` (initial, in order, 13), `cursor`, `filter lineInput`, `filtering`, `multi bool`, `max int`, `apply func(*Ctx, []string) tea.Cmd` (the opener decides what applying means: an issue change, a form field, or the quick-add batch) |
| `DeleteConfirm` | `number`, `title`, `input lineInput`, `deleting`, `warn bool` (`!ViewerCanDelete`), `err` |
| `Confirm` | `text`, `onYes func(*Ctx) tea.Cmd`, `quit bool` (the quit confirmation: a second `ctrl+c` quits at once) |
| `Conflict` | `formID`, `number` |
| `QuickAdd` | `step` (Write, Preview, Running), `editor Editor`, `opts domain.QuickAddOptions`, `loaded`, `saveOff`, `loadErr`, `loadWaiting`, `gen`, `marks []mayExist`, `banner []string`, `saveErr`, `plan`, `planErr`, `planSeq`, `planning`, `starting`, `runID`, `stop *atomic.Bool`, `events`, `rows []qaRow`, `stopping` (12) |
| `Help` | `from ScreenID`, `enabled map[Action]bool`, `offset int` (9.3) |

### 4.4 The too-small state

This is not a screen. When `ComputeFrame` reports the window is too small,
`View` returns the too-small text (UI 3.15). `Update` keeps processing every
message. Keys other than `q` and `ctrl+c` are ignored, data keeps loading,
and nothing is reset. As built, `q` there quits at once, without the
pending-writes question (`app.go` key dispatch).

---

## 5. Message catalogue

Every message is routed through `App.Update`, which handles the global ones
and forwards the rest. Reads carry `sent` (the clock when the request was
sent). Writes carry `arrived` (the clock when the response arrived). Both
feed the asOf rule (7.2).

### 5.1 From Bubble Tea and bubbles

| Message | Sender | Handler |
|---|---|---|
| `tea.WindowSizeMsg` | runtime | App: recompute the frame, resize screens and overlays, schedule `resizeSettledMsg` |
| `tea.BackgroundColorMsg` | runtime (reply to `tea.RequestBackgroundColor`) | App: `NewTheme(msg.IsDark())` |
| `tea.KeyPressMsg` | runtime | App: key dispatch (9.2) |
| `tea.PasteMsg` | runtime (bracketed paste) | the focused text input; ignored in nav mode |
| `spinnerTickMsg{gen}` | `Deps.Tick(100ms)` from `spinCmd` | App: advance `spin`. The tick chain runs only while a read is loading or a change, form write or quick-add run is sending (`spinNeeded`) |

### 5.2 Reads

| Message | Sender | Handler |
|---|---|---|
| `sessionMsg{req, sent, s domain.Session, err}` | `Service.Session` | App: viewer and repo. On error every tab shows its failed state |
| `labelsMsg{labels, err}` | `Service.Labels`, from `loadMeta` (no slot; the service memoises for 60 s) | App → `store.Meta`, then any open label picker |
| `usersMsg{users, err}` | `Service.AssignableUsers`, from `loadMeta` | App → `store.Meta`, then any open assignee picker |
| `boardMsg{req, sent, info, board, err}` | one Cmd: `Service.BoardInfo`, then `Service.Board` (and `RefreshMetadata` first on recovery) | App → `store.board`. Setup errors switch the Board stack (4.2). Info and cards always come from the same request (finding 4) |
| `recoveredMsg{req, sent, s domain.Session, err}` | one Cmd: `Service.RefreshMetadata`, then `Service.Session` | App: store the session, then restart the visible read (7.6, finding 2) |
| `candidatesMsg{req, sent, res domain.ProjectsResult, err}` | `Service.BoardCandidates` | BoardSetup (Several, Choosing) |
| `boardChosenMsg{req, sent, project, err}` | `Service.UseBoard` | App: clear every cached placement, cancel issue and board reads, then reload the board, the issue list and open issue details (finding 3) |
| `issuesPageMsg{req, sent, q IssueQuery, page, err}` | `Service.ListIssues` | App → `store.issues` (7.3) |
| `prsPageMsg{req, sent, q PRQuery, page, err}` | `Service.ListPRs` | App → `store.prs` |
| `issueDetailMsg{req, sent, number, reason, detail, err}` | `Service.GetIssue` | App → store. `reason` = Open, Refresh or Reread (8.6, 8.12) |
| `formReadMsg{req, formID, sent, number, precheck, detail, err}` | `Service.GetIssue` from `formRead`, in `SlotForm` | IssueForm: the body to edit when it opens, or the conflict pre-check (14.4) |
| `commentsPageMsg{req, sent, kind, number, page, err}` | `Service.ListIssueComments` / `ListPRComments` | App → the detail entry, then the next page up to 500 |
| `prDetailMsg{req, sent, number, detail, err}` | `Service.GetPR` | App → `store.prDet` |

### 5.3 Writes

| Message | Sender | Handler |
|---|---|---|
| `changeDoneMsg{id ChangeID, arrived, res domain.Change, err}` | the Cmd of a pending change: `UpdateAssignees`, `UpdateLabels`, `Close`, `Reopen`, `SetStatus`, `RemoveFromBoard` | App → `pending.settle` (8.5) |
| `createDoneMsg{formID, arrived, res domain.Change, err}` | `Service.CreateIssue` | IssueForm, then App → store (8.9) |
| `editDoneMsg{formID, arrived, res domain.Change, err}` | `Service.EditIssue` | IssueForm, then App → store |
| `commentDoneMsg{formID, arrived, res domain.CommentResult, err}` | `Service.Comment` | CommentForm, then App → store, and append to the detail's comments |
| `deleteDoneMsg{number, err}` | `Service.Delete` | DeleteConfirm, then App → `store.remove`, and pop a detail showing it |

### 5.4 Quick add

| Message | Sender | Handler |
|---|---|---|
| `draftLoadedMsg{d domain.Draft, ok, err}` | `DraftStore.Load` Cmd | QuickAdd (12.1) |
| `draftTickMsg{gen}` | `Deps.Tick(500ms)` after a keystroke | QuickAdd: queue a save if `gen` is current |
| `draftSavedMsg{err}` | the App's draft queue Cmd (12.2) | App: send the next queued save; start a waiting run or load; warn once on error |
| `planMsg{seq, plan, err}` | `Service.PlanQuickAdd` | QuickAdd, only if `seq` is current |
| `qaProgressMsg{runID, p domain.QuickAddProgress}` | run goroutine, through `Deps.Send` (or a channel and `waitEvent` when `Send` is nil) | QuickAdd → `rows`, store |
| `qaDoneMsg{runID, res domain.QuickAddResult, err, d domain.Draft}` | run goroutine | App → QuickAdd: finish or stop (12.5) |

### 5.5 Everything else

| Message | Sender | Handler |
|---|---|---|
| `ActionMsg{a Action}` | App key dispatch | the target screen or overlay (9.2) |
| `renderedMsg{items []rendered}` | one Cmd per batch of up to `renderBatch` = 16 renders | App → the render cache; the screens and editor pick their outputs up by key |
| `rereadMsg{number, gen}` | `Deps.Tick(5s)` after a create or board change (8.12) | App: re-read the issue if `gen` is current and nothing is pending |
| `resizeSettledMsg{gen}` | `tea.Tick(100ms)` after a resize | App: request renders at the new width if `gen` is current |
| `autoRefreshMsg{gen}` | `tea.Tick(60s)` | App (7.7) |
| `clockTickMsg{}` | `Deps.Tick(1s)`, started by `Init`, re-scheduled by every tick | App: nothing but a redraw. Bubble Tea redraws only on messages, so without it "synced 12s ago" and every relative time froze while idle (dogfood) |
| `toastExpireMsg{id}` | `tea.Tick(6s)` for confirmation toasts | App: clear the toast if `id` is current |
| `editorDoneMsg{owner, path, err}` | `tea.ExecProcess` callback | the editor's owner (14.3) |
| `editorReadMsg{owner, text, err}` | Cmd that reads and removes the temp file | the editor |
| `openedMsg{err}` | `Deps.Open` Cmd | App: error toast only |

Clipboard is fire-and-forget: `tea.SetClipboard(url)` (OSC 52) plus a toast
that prints the URL (UI 9 #3). There is no reply message.

---

## 6. From service call to `tea.Cmd`

### 6.1 Reads and writes are App methods

*As built:* there are no `Intent` values and no `App.do`. Screens and
overlays call App methods through `Ctx` (4.1); each method starts its slot
(6.3), builds the Cmd with `read` or `write`, and has a matching handler:

- reads, in `do.go`: `loadSession`, `recover`, `loadBoard(refreshMeta,
  reason)` (BoardInfo then Board in one request), `loadCandidates`,
  `chooseBoard`, `loadIssues`, `loadPRs`, `loadIssue` (`GetIssue`, plus the
  first comments page on Open and Refresh), `issueDetail` (`GetIssue` only, for
  Reread), `loadPR`, `commentsPage`;
- metadata: `loadMeta` in `actions.go` (`Labels`, `AssignableUsers`), and
  `reloadMetaFresh` for a manual refresh;
- pending changes: `applyChange` and `sendChange` in `changes.go`;
- forms: `formRead` (edit load and pre-check), `CreateIssue`, `EditIssue`,
  `Comment` in `overlay_forms.go`; `Delete` in `overlay_confirm.go`;
- quick add: `PlanQuickAdd` and `RunQuickAdd` in `overlay_quickadd.go`.

`LoadReason` is Initial, Refresh, Auto, QueryChanged, Open, Reread, Precheck
or Manual (`msgs.go`). Manual is the user's own refresh: the `r` key, and the
recovery it starts after an auth or setup error (`recover`, `onRecovered`).
Refresh is a reload the app starts itself, such as after a project switch.
Only Manual is fresh (7.6).

### 6.2 The Cmd wrappers

```go
type ReqID uint64

// start registers a new request in a slot, cancelling the previous one, and
// returns its ID and a context derived from the program's (6.3). For
// ReasonManual the context is marked domain.WithFreshSince(manualAt), the
// time of the last press of r.
func (a *App) start(slot Slot, reason LoadReason) (ReqID, context.Context)

// startSince is start with an explicit fresh time; continuation pages pass
// the Since of the request they continue.
func (a *App) startSince(slot Slot, reason LoadReason, since time.Time) (ReqID, context.Context)

// read: cancellable through the slot's context, stamped with the time it was sent.
func read[T any](clock func() time.Time, ctx context.Context,
    call func(context.Context) (T, error),
    wrap func(sent time.Time, v T, err error) tea.Msg,
) tea.Cmd {
    return func() tea.Msg {
        sent := clock()
        v, err := call(ctx)
        return wrap(sent, v, err)
    }
}

// write (changes.go): never cancelled by navigation; stamped with the time the response arrived.
func write[T any](parent context.Context, clock func() time.Time,
    call func(context.Context) (T, error),
    wrap func(arrived time.Time, v T, err error) tea.Msg,
) tea.Cmd {
    return func() tea.Msg {
        v, err := call(parent)
        return wrap(clock(), v, err)
    }
}
```

- `sent` is taken inside the Cmd, right before the call. That is the moment
  the request leaves, which is what A5 rule 7 means by "sent".
- The 30 second timeout is applied by the `github` backend (A6), not by the
  service or the TUI. The TUI adds only cancellation. The fake never times
  out by itself; tests stage timeouts with gates.
- Errors are unpacked with `errors.As(err, &de)` into `*domain.Error`. A
  result whose error satisfies `errors.Is(err, context.Canceled)` from a
  request we cancelled is dropped silently (A6). Any other error that is not a
  `*domain.Error` is shown as kind `internal`.

### 6.3 Slots: matching results and dropping stale ones

```go
type SlotKind int // Session, Recover, Form, Candidates, Choose, Board, Issues, PRs, Issue, IssueComments, PR, PRComments
// Form: a form's own GetIssue (edit load, pre-check). Labels and users have no
// slot (loadMeta); the quick-add plan is matched by planSeq instead.

type Slot struct { Kind SlotKind; Number int }   // Number is 0 for non-item slots

type Request struct {
    ID     ReqID
    Cancel context.CancelFunc
    Reason LoadReason
}
```

- Starting a read for a slot cancels the previous request in that slot and
  stores the new one.
- A read result is accepted only when `inflight[slot].ID == msg.req`.
  Anything else is dropped: a request that was superseded or cancelled and
  returned anyway.
- Changing the state filter or the Issues view's mine toggle starts a new
  `LoadIssues` in the `Issues` slot. The old chain is cancelled, and late pages
  are dropped by ID. The message also carries `q`, which is checked against
  the cache's query as a second guard.
- Popping a detail cancels its `Issue`, `IssueComments`, `PR` or `PRComments`
  slots, unless the `Issue` slot holds a `Reread` for a held queue (8.6).
  That request is kept.
- Writes have no slot and are never cancelled by navigation. Only quitting
  abandons them, and that asks first (8.10).

---

## 7. The store

### 7.1 Structure

```go
type Store struct {                       // as built (store.go)
    issues  map[int]*IssueEntry           // every issue seen, by number
    prs     map[int]*PREntry              // every PR summary seen
    Issues  ListCache[IssueQuery]         // numbers in display order
    PRs     PRListCache
    Board   BoardCache
    details *lru[int, *DetailEntry]       // 64 entries: body + comments
    prDet   *lru[int, *PRDetailEntry]     // 32 entries
    Meta    Meta                          // session, labels, users
    version uint64                        // bumped on every change, for redraws
}

type IssueEntry struct {
    Base domain.Issue
    AsOf time.Time                         // A5: when the request producing Base was sent
}

type ListCache[Q comparable] struct {
    Query     Q
    Numbers   []int                        // GitHub order (most recently updated first)
    Total     int
    Cursor    string                       // GitHub's cursor, passed back unchanged (A4)
    HasMore   bool
    Phase     LoadPhase                    // Empty, FirstPage, Background, Complete
    LoadedAt  time.Time
    Err       *domain.Error                // non-nil with Numbers = stale
    Truncated bool                         // Total > 500
    next      *ListCache[Q]                // refresh being assembled (7.6)
}

type PRListCache struct {                  // PRs are not in the optimistic log
    Query PRQuery
    Numbers []int                          // summaries live in Store.prs
    Total int; Cursor string; HasMore bool; Phase LoadPhase
    LoadedAt time.Time; Err *domain.Error; Truncated bool
    next *PRListCache
}

// PREntry is one pull request summary under the same asOf rule as issues:
// a newer list page is never masked by an older detail (finding 6).
type PREntry struct {
    Base domain.PullRequest
    AsOf time.Time
}
func (s *Store) PutPR(p domain.PullRequest, asOf time.Time) bool

type BoardCache struct {
    Extra    map[int]bool                  // placed this session (created there, or a settled board change)
                                           // and maybe not in Order yet; cleared by every board load
    Info     *domain.BoardInfo             // Project, ordered Status options
    InfoErr  *domain.Error                 // the setup error (4.2)
    Order    []ColumnOrder                 // A5: board data = ordered numbers per status
    Hidden   int                           // HiddenCount
    Truncated, CardsTruncated bool
    Loaded   bool
    LoadedAt time.Time
    Err      *domain.Error
}

type ColumnOrder struct {                  // one column as the Board call returned it
    Status   *string                       // nil = "No status"
    OptionID string
    Numbers  []int                         // GitHub's order
}

type DetailEntry struct {
    Body          string
    BodyAsOf      time.Time
    Loaded        bool
    Comments      []domain.Comment
    CommentsTotal int
    CommentsPhase LoadPhase
    LoadedAt      time.Time
    BodyErr       *domain.Error            // body and comments are separate requests
    CommentsErr   *domain.Error            // with separate errors (finding 5)
    nextComments  []domain.Comment         // refresh being assembled
}
// PRDetailEntry has DetailErr and CommentsErr the same way.

// ClearPlacements forgets every placement as of t (board switch, finding 3).
func (s *Store) ClearPlacements(t time.Time)

type Meta struct {
    Session *domain.Session                // the viewer is Session.Viewer.Login
    Labels  []domain.Label
    Users   []domain.User
}
```

Issues live only in `issues`. Lists, the board and details hold numbers or
non-issue data (body, comments). The list, the board and the detail can never
disagree about an issue.

### 7.2 The asOf rule (A5 rule 7)

```go
// Put stores an incoming copy. asOf is the request's sent time for reads,
// or the response arrival time for writes. It reports whether Base changed.
func (s *Store) Put(i domain.Issue, asOf time.Time) bool {
    e, ok := s.issues[i.Number]
    if ok && asOf.Before(e.AsOf) {
        return false            // stale: an older read landed after a newer one
    }
    s.issues[i.Number] = &IssueEntry{Base: i, AsOf: asOf}
    return true
}

func (s *Store) Remove(number int)   // delete: issues, every list, board order, details
```

- This is the single ordering rule for every source: list pages, board
  loads, detail reads, re-reads and write results. GitHub's `updatedAt` is
  never compared. Equal times replace, because "not older" wins.
- A list page or board load still records the issue's *position* even when
  `Put` rejects the copy. Position and content are separate.
- `DetailEntry.Body` follows the same rule with `BodyAsOf`.

### 7.3 Progressive loading (5.2)

```
LoadIssues(q, Initial or QueryChanged):
  page 1: ListIssues(ctx, filter(q), PageRequest{Size: 100})
     → Put every issue (asOf = sent); Numbers = page order;
       Total, Cursor = NextCursor, HasMore; Phase = FirstPage, or Complete if !HasMore
     → the list renders now
  while HasMore && len(Numbers) < 500:
     next page: ListIssues(ctx, filter(q), PageRequest{Cursor: Cursor, Size: 100})
     → Put; append numbers not already present (a number already present
       keeps its row; its content follows the asOf rule); Phase = Background
  done: Phase = Complete; LoadedAt = now; Truncated = Total > 500
```

- `filter(q)` is `domain.IssueFilter{State: q.State, Assignee: "@me" if
  q.Mine}`. `Search` is never set (5.2).
- Each page is its own Cmd in the `Issues` slot, with a fresh `ReqID`, so a new
  query cancels the chain at any point. The sub-bar shows `loading 200/342…`
  in `Background` and `500 of 1,204 · list cut off` when truncated (UI 3.1).
- PRs load the same way into `PRListCache`, keyed by number.
- **Comments** load when a detail opens (5.2): `GetIssue` (or `GetPR`) and, in
  parallel, comments page 1 (100), then later pages up to 500. The body
  renders as soon as it arrives. Over 500, the notice of UI 3.2 appears.

### 7.4 Queries and "only mine" (5.2)

```go
type IssueQuery struct { State domain.IssueState; Mine bool }  // open | closed | all
type PRQuery    struct { State domain.PRState }                // open | closed | merged | all
```

| View | `m` | `/` |
|---|---|---|
| Issues | **server**: part of `IssueQuery`, `Assignee = "@me"` | local |
| Pull requests | **local**: `Author == Viewer` over the loaded items | local |
| Board | **local**: the viewer is among the effective assignees | local |

The local filters run over loaded data. The sub-bar shows the match count next
to the loaded count, for example `3 mine · 500 loaded of 1,204` (5.2). The `/`
text filter matches title, number, labels, assignees and author,
case-insensitively by substring, against the *effective* issue.

**The closed view's order.** With the state filter on `closed`, the Issues
view orders its rows by closed time, newest first. GitHub's issue listing
orders by created, updated or comments only, so this is a derived order over
the loaded list (`order.go`), computed in `IssueList.Sync` after the `/`
filter. `open` and `all`, and the PR view, keep GitHub's most-recently-updated
order. `todos list --state closed` is unchanged.

- `byClosed` compares closed time (an issue without one uses its updated
  time), then updated time, then the higher number. `closedOrder` is pure.
- Pages arrive in updated order, so rows move as they land. The selection
  stays on its issue by number (7.8), on a later page and on refresh.
- Past the 500-issue cap the order is over the loaded 500, under the existing
  truncation notice.
- The time column shows the closed time, and the sub-bar reads
  `closed ▾ by closed time`.
- A reopen clears the closed time and bumps the updated time. The view keeps
  the last closed time it saw for each issue (`IssueList.lastClosed`), so a
  reopened row stays where it was until it leaves the list at the next
  refresh, as it does in the other views. Closing it again brings a new
  closed time, and it sorts by that.

### 7.5 The board is derived (A5)

`boardMsg` carries `BoardInfo` and the board from **one** request, so they
can never disagree (finding 4). It stores each card's issue with
`Put(card.Issue, sent)` and records `Order` from `board.Columns`: one
`ColumnOrder` per returned column, in the order returned. **The displayed
columns, their names and their order come from `Order`, not from
`BoardInfo.Options`.** A Status renamed on GitHub therefore shows under its
new name on the next refresh, with its cards.

Displayed columns are computed from the store, never from cards:

```go
// effective is Ctx.Issue (base with pending changes replayed); extra are the
// issues with pending changes plus Board.Extra.
func (s *Store) Columns(effective func(int) (domain.Issue, bool), extra []int) []DisplayColumn {
    // 1. Membership: every issue whose effective placement is on the board.
    //    Candidates are the numbers in Order plus every issue in the store
    //    with a pending board patch.
    // 2. For each column c in Order's order (plus "No status" first):
    //      first: numbers in c.Numbers (GitHub order) whose effective status == c
    //             and whose base status == c
    //      then:  every other member whose effective status == c, by ascending number
    // 3. "No status" is shown only when it is not empty (3.2).
    // 4. Cards beyond 500 in total are not drawn; CardsTruncated or the 500 cap
    //    shows the notice.
}
```

- A card moved optimistically leaves its old column at once and appears at
  the bottom of the new one. After it settles, `Put` gives it the new base
  status, but it is not in the new column's `Numbers` until the board reloads,
  so it stays at the bottom. That matches (4.4): "sits at the bottom until a
  refresh shows where GitHub placed it". There is no board-specific
  optimistic code.
- "By ascending number" is the deterministic order for the bottom group. With
  several moves pending, the order inside that group is by number, not by
  when they were moved.
- `Columns` is not memoised as built: `Ctx.Columns` recomputes it on each
  call. At 500 cards that has not been measurable.

### 7.6 Refresh

`r`, the auto-refresh, and the recovery after a setup error all use one path
with a different `Reason`.

**A manual refresh is fresh** (cache.md 10). Fresh is a time, not a flag:
nothing read from GitHub before it may be used.
- **The time:** pressing `r` records `App.manualAt` from the injected clock.
  `r` and the recovery it starts use `ReasonManual`, and `start` wraps the
  read's context with `domain.WithFreshSince(ctx, manualAt)` for that reason
  only. The request keeps the time in `Request.Since`.
- **What the service does with it:** it may reuse what this refresh's own
  first read fetched (the session, the board's fields), but nothing older
  than the key press.
- **The same time on every read of the refresh:**
  - **Continuation pages:** list pages start their slot with the reason and
    `Since` of the request they continue (`r.Reason`, `r.Since`), not a new
    time.
  - **Comment pages:** `commentsPage` takes the reason and time of the load
    that started it and passes both on to each next page.
  - **Labels and assignable users:** `r` also reloads them, fresh since the
    same press (`reloadMetaFresh`), but only when they are already in the
    store, so a label made on github.com shows in the pickers after `r`.
- **A second `r`** later carries a later time.
- **A load already in flight:** a manual refresh replaces a load of the same
  view that is not manual, such as an auto-refresh, which the cache may
  answer (`keepLoading`). A second `r` while a manual refresh is loading does
  nothing.

Nothing else is fresh: not startup, opening a view or detail, the
auto-refresh, the delayed re-read, the form pre-check, or the reload after a
project switch (`ReasonRefresh`).

- **Lists:** pages are assembled into `next`, a shadow `ListCache`, with the
  same 500 limit. `Numbers` is swapped for `next.Numbers` only when the shadow
  is complete. Issue contents are `Put` as each page arrives, under the asOf
  rule. On failure, `next` is discarded, the old `Numbers` stays, `Err` is set,
  the header shows stale (UI 5.3), and a banner appears (UI 3.14).
- **Board:** one request reads `BoardInfo`, then `Board`, and replaces
  `Info`, `Order`, `Hidden` and the truncation flags whole, and `Put`s the
  cards.
- **Detail:** `GetIssue` (or `GetPR`), and comments page 1 onward into
  `nextComments`, swapped in whole when complete. A detail counts as in
  flight while **either** its body or its comments slot is busy, so a refresh
  never cancels comments that are still paging (finding 8). A failed refresh
  keeps the old body and shows `⚠ Refresh failed: … · r retry` plus the hint
  above it. A failed comments request shows `✗ Couldn't load comments: …`
  plus the hint, even when the body loaded (findings 5 and 10).
- **Recovery after an auth or setup error** (finding 2, @cto's ruling): when
  the visible view's error is `auth`, `missing_scope`, `no_repo`, `no_board`,
  `issues_disabled` or `config`, or the board tab shows `BoardSetup`, `r`
  first sends one request that calls `RefreshMetadata`, then `Session`. The
  account may have changed, so the session (viewer) is stored again, then the
  visible read restarts. Other errors are retried directly.
- **Choosing another project** (`ChooseBoard`, finding 3): on success, every
  cached placement is cleared as of now, so the asOf rule rejects any read
  sent before the switch. Issue and board reads in flight are cancelled, and
  the board, the issue list (if loaded) and every open issue detail reload.
- **Metadata:** every picker open sends `LoadMeta` in the background. The
  service keeps labels and users for 5 minutes (cache.md 3), and `r` reads
  them fresh. The picker opens at once from `meta`.
  When the fresh list lands, the picker updates and keeps its cursor by name.

### 7.7 Auto-refresh (4.4, 10)

There is one `tea.Tick(60s)` chain with a generation. On
`autoRefreshMsg{gen}`:

1. Drop it if `gen` is stale. Otherwise schedule the next tick.
2. Re-issue a `Reread` for every issue with a held queue (8.6). This runs
   even when the rest is skipped, because a held queue blocks the user.
3. **Skip the refresh** if any of these holds:
   - an overlay is open (4.4)
   - `svc.RateLimit()` is `Known` with `Remaining < Limit/10` (10). The header
     shows `⏸ auto-refresh paused · N% left` (UI 5.3)
   - the visible view's slot has a request in flight
   - the visible view finished a load less than 30 s ago. A view that has
     never loaded successfully (its first load failed) counts as infinitely
     old, so the next tick retries it, and so does switching to its tab
     (finding 7).
   - the terminal is too small
4. Otherwise refresh **only the visible view**: the top screen of the active
   tab. A hidden tab refreshes when the user switches to it, if its data is
   older than 60 s.

`r` ignores every skip rule except "already in flight", where it does
nothing.

### 7.8 Keeping the selection (4.4)

```go
// reselect keeps the selected issue by number after the rows change.
func reselect(rows []int, sel, prevIndex, offset, height int) (newSel, newOffset int) {
    if i, ok := indexOf(rows, sel); ok {
        return sel, clampOffset(offset, i, height, len(rows))
    }
    if len(rows) == 0 {
        return 0, 0
    }
    i := min(prevIndex, len(rows)-1)
    return rows[i], clampOffset(offset, i, height, len(rows))
}
```

- `clampOffset` keeps the offset unless the selection would leave the
  viewport, and then scrolls the minimum distance.
- Every screen records `prevIndex` when it renders.
- On the board, the focused column is kept by status option ID, and the
  selected card by number within it.

---

## 8. Pending changes (A5)

### 8.1 Types

```go
type ChangeID uint64

type Patch struct {
    Assignees *SetDelta    // Add / Remove logins
    Labels    *SetDelta    // Add / Remove names
    State     *StatePatch
    Board     *BoardPatch
}
type SetDelta   struct{ Add, Remove []string }
type StatePatch struct{ Open bool; Reason domain.StateReason }  // Reason when closing
type BoardPatch struct{ Status *string; Remove bool }           // Status nil + !Remove = "No status"

type Phase int
const ( Queued Phase = iota; InFlight; Uncertain )

type Change struct {
    ID       ChangeID
    Number   int
    Patch    Patch            // exactly one field set
    Phase    Phase
    FailedAt time.Time        // when it became Uncertain
}

type PendingSet struct {
    byIssue  map[int][]*Change   // per issue, in ID order; [0] is the head
    inFlight map[ChangeID]bool   // across all issues; at most 4 (A5 rule 2)
    ready    []int               // issues whose head is Queued and waiting for a slot, FIFO
    seq      ChangeID            // increases only
    version  uint64              // bumped on every change
}
```

| User action | Patch | Service call |
|---|---|---|
| Assignee picker apply | `Assignees{Add, Remove}` | `UpdateAssignees(n, add, remove)` |
| Label picker apply | `Labels{Add, Remove}` | `UpdateLabels(n, add, remove)` |
| `x` on open | `State{Open: false, Reason: completed}` | `Close(n, CloseOptions{Reason: "completed"})` |
| `X` on open | `State{Open: false, Reason: not_planned}` | `Close(n, CloseOptions{Reason: "not_planned"})` |
| `x` on closed | `State{Open: true}` | `Reopen(n)` |
| `M` → option or "No status" | `Board{Status: s}` | `SetStatus(n, s)` |
| `M` → "Remove from board" | `Board{Remove: true}` | `RemoveFromBoard(n)` |
| `<` / `>` | `Board{Status: neighbour}` | `SetStatus(n, neighbour)` |

`BoardAdd` is not used by the TUI. `SetStatus` already adds an issue that is
not on the board (A4). `X` on a closed issue does nothing and shows a toast:
`#142 is already closed`.

### 8.2 `Apply` and `Effective` (pure)

```go
// Apply has set semantics: applying twice, or to an issue that already
// has the change, changes nothing.
func Apply(i domain.Issue, p Patch, labels []domain.Label) domain.Issue

// Effective folds every pending change of one issue onto its base, in ID order.
func Effective(base domain.Issue, changes []*Change, labels []domain.Label) (domain.Issue, Marks)

type Marks struct {
    Busy, Uncertain              bool     // any change Queued/InFlight → gutter ⠋; any Uncertain → ?
    AddedLabels, RemovedLabels   []string // added: pending colour with ⠋; removed: struck through
    AddedPeople, RemovedPeople   []string
    State, Board                 bool     // state glyph pending; card in its new column with ⠋
}
```

- **Assignees and labels:** `result = (base ∪ Add) \ Remove`. The base order
  is kept and additions are appended in the order given. Added labels take
  their colour from `meta.Labels`, and fall back to `Muted`.
- **State:** sets `State` and `StateReason`.
- **Board:** `Remove` sets `Board = nil`. Otherwise it sets `Board.Status`,
  creating a placement with an empty `ItemID` when there was none, because
  `SetStatus` adds the issue.
- `Marks` comes from comparing base and effective per field, together with
  which fields the pending patches touch (UI 5.2).

Every screen draws `Effective`. The base is never modified optimistically.

### 8.3 Starting a change (A5 rules 1 and 2)

```
ApplyChange(n, patch):          // patch computed against Effective(n) by the caller
  c := &Change{ID: next(), Number: n, Patch: patch, Phase: Queued}
  append c to byIssue[n]
  if c is the head: enqueue n in ready
  pump()

pump():
  while inFlight < 4 and ready is not empty:
    n := pop front of ready
    head := byIssue[n][0]
    if head.Phase != Queued: continue        // held (Uncertain) or already running
    head.Phase = InFlight; inFlight++
    emit Cmd: write(call for head.Patch) → changeDoneMsg{head.ID, …}
  ensure the spinner tick runs
```

- One change in flight per issue, in the order the user made them.
- Up to four issues are written in parallel. Others wait in `ready`, FIFO.
- Because the patch is computed against the *effective* issue, opening the
  label picker on an issue with a pending `+docs` shows `docs` ticked, and
  `x` on an issue with a pending close queues a reopen. That is the undo in
  UI 3.14.

### 8.4 Pending markers

The row gutter shows `⠋` while `Marks.Busy`, and `?` while
`Marks.Uncertain`. Field-level markers follow UI 5.2. A queued change looks
the same as an in-flight one: the user sees "pending", not the queue.

### 8.5 Settling (A5 rules 3 and 4)

On `changeDoneMsg{id, arrived, res, err}`:

```
c := find(id)
if c == nil: drop                              // the issue was deleted meanwhile
inFlight--
switch {
case err == nil:                               // rule 3
    remove c
    store.Put(res.Issue, arrived)
    if c is a close or reopen: toast "✓ Closed #N · x to undo" / "✓ Reopened #N" (UI 5.2)
    advance(n)

case de.Uncertain:                             // rule 5
    c.Phase = Uncertain; c.FailedAt = arrived
    do(LoadIssue{n, Reason: Reread})           // held queue: nothing else for n is sent
    // advance(n) is NOT called

case de.Kind == domain.ErrPartial:            // a step succeeded, a later one failed
    remove c
    if de.Issue != nil: store.Put(*de.Issue, arrived)
    toast error naming what was done (de.Completed) and what failed
    advance(n)

default:                                       // rule 4: rejected
    remove c
    toast error "✗ Couldn't <verb> #N: <message>"   (stays until the next key, UI 9 #8)
    advance(n)
}
pump()

advance(n): if byIssue[n] is not empty, its new head is Queued → enqueue n in ready
```

- **Rejected changes snap back for free.** Removing the change removes it
  from the fold. There is no rollback code.
- **Later queued changes still run after a rejection** (A5 rule 4). They
  are independent set operations, and running them is what the user asked
  for.
- **Partial:** `UpdateLabels` or `UpdateAssignees` can return `partial` with
  the internal step `removed` (A6): the removal succeeded and the addition
  failed. `SetStatus` can return `added_to_board` (added, but the status was
  not set). The issue as far as it got becomes the base. The toast says what
  was done.
- No follow-up re-read after a success: `res.Issue` is GitHub's
  acknowledgement and becomes the base (A5 rule 3). The one exception is the
  delayed re-read after a create or a board change (8.12).

### 8.6 Uncertain changes and the held queue (A5 rule 5)

While an issue's head change is `Uncertain`, **nothing else for that issue is
sent**. Later changes stay `Queued` and stay in the fold.

The hold is released by the first read of that issue **sent after
`FailedAt`** that lands successfully: the explicit `Reread`, or any list
page, board load or detail read that contains the issue.

```
on every successful read that Puts issue n with asOf = sent:
  head := byIssue[n][0]
  if head != nil && head.Phase == Uncertain && !sent.Before(head.FailedAt):
      remove head                  // the base (GitHub's truth) now shows
      if Effective no longer shows what the patch intended:
          toast "#N: couldn't confirm the change; showing GitHub's current state"
      advance(n); pump()
```

- A read sent *before* the failure cannot release the hold, even if it lands
  afterwards. It is still `Put` under the asOf rule as usual.
- If the `Reread` fails, the `?` stays, and every auto-refresh tick re-issues
  it (7.7, step 2). `r` re-issues it too.
- The `Issue` slot of a held issue is not cancelled when its detail is popped
  (6.3).

### 8.7 Refresh while changes are pending (A5 rule 6)

Nothing special. A refresh `Put`s new bases, and `Effective` replays every
change on top, so pending values stay on screen.

- **State filter:** an issue closed optimistically under the `open` filter
  stays in the list, drawn closed with `⠋`, until the next list swap no
  longer contains it. The selection then moves per 7.8. Rows never jump out
  from under the cursor in the middle of an action.

### 8.8 Quitting with changes in flight

`q` or `ctrl+c` counts the changes in `PendingSet` (Queued, InFlight or
Uncertain) plus busy overlays. If there are any, a `Confirm` opens:
`2 changes still sending. Quit anyway? y / esc` (UI 4). A second `ctrl+c`
while it is open quits at once. A quick-add run in progress counts as one,
see 12.4.

### 8.9 Create, edit, comment, delete: they wait (A5, 4.4)

These are not in the log.

- **Create:** the form shows `⠋ Creating…` and accepts only `esc`, which asks
  `The issue may still be created. Close the form anyway?`.
  - Success: `store.Put(res.Issue, arrived)`. The number is prepended to
    `lists.issues.Numbers` if it matches the list's query. If the issue is on
    the board, it shows at the bottom of its column (7.5). The cursor moves to
    it, and the form closes.
  - `partial` (created, the board step failed): the same, plus an error toast
    naming the missing step.
  - Uncertain or other errors: the form stays open with its text and shows
    the error. An uncertain create is never retried (4.6). The message says
    to check GitHub.
  - **From the Board view**, `NewIssue.AddToBoard = true` always, with `Status`
    = the focused column, which is nil for "No status" (A4, 4.1). From the
    Issues view, `AddToBoard` is set only when a status was picked in the form
    (8.1 list: an option, or "No status").
- **Edit:** the pre-check of 14.4, then `EditIssue`. Success: `Put`, close.
  Error: inline, text kept.
- **Comment:** `Comment`. Success: `Put(res.Issue)`, append `res.Comment` to the
  detail's comments, close. Error: inline, text kept.
- **Delete:** the box shows `⠋ Deleting…`. Success: `store.Remove`, close, and
  pop an `IssueDetail` showing it from every tab stack. Error: shown in the
  box, which stays open until `esc` (UI 3.12). Pending changes for a deleted
  issue are dropped, and their late results are ignored (8.5, `find` misses).

---

### 8.11 As built (Phase 6)

- **Drawing.** Screens never draw the bare base: `Ctx.Issue(n)` is
  `Effective(base, changes)`, `Ctx.Marks(n)` its markers, and
  `Ctx.Columns()` the derived board. Lists and cards show `⠋` (busy) or `?`
  (uncertain) in the gutter. The detail shows `⠋+name` for pending additions,
  struck-through removals, and a spinner beside a pending state or board
  status (UI 5.2).
- **Issues placed this session.** `BoardCache.Extra` holds issues created on
  the board or moved by a settled change. GitHub's order does not list them
  until the next board load, so `Columns` considers them too. Every board load
  clears it.
- **The edit form's reads** (loading the body to edit, and the pre-check) use
  their own slot, `SlotForm`. They never cancel the detail screen's reads of
  the same issue, nor are cancelled by them.
- **Results reach overlays.** Create, edit, comment, delete, form-read and
  editor messages are given to every open overlay; each checks its own ID.
- **The assignee picker** lists `@me (login)` first, then the assignable
  users. In navigation mode, any letter other than `j`, `k` and space starts
  the filter (the App forwards keys an overlay has no action for).
- **Form writes** (create, edit, comment, delete) are registered while in
  flight and count as writes for the quit question. A result whose overlay
  was closed meanwhile (a create can be abandoned with `esc` → `y`) is handled
  by the App: the store is updated and a toast says what happened, so a
  result is never lost (Phase 6 review).
- **A held queue's read survives navigation.** Popping a detail never
  cancels the `Issue` slot of an issue whose queue is held. Opening the
  detail replaces the re-read with the detail's own read, which is sent after
  the failure and releases the hold when it lands (Phase 6 review, 6.3).
- **Quit.** `q`, or a first `ctrl+c`, with changes pending or a form write in flight
  opens `N changes still sending. Quit anyway?`. A second `ctrl+c` on that
  question quits at once.
- **Footer** descriptions are the short forms of the mocks (`n new  e edit
  x close  </> move`). The help screen keeps the long ones and sizes each
  group's key column to its widest key.

### 8.12 The delayed re-read (Phase 9)

GitHub project workflows act a few seconds after our write: a new issue is
auto-added to a column, a card moved to Done gets its issue closed. The live
dogfood showed both only at the next 60 s refresh. So, as the one exception to
"no extra read after a successful change":

- After a **create** (form or quick add, `App.created`), a successful **board
  change** (`SetStatus` or remove-from-board, any `Patch.Board`, also when it
  ends `partial`), the issue is re-read once, `rereadAfter` = 5 s later, through
  the normal read path (`issueDetail(n, ReasonReread)`) under the asOf rule.
- **One timer per issue** (`reread.go`, `App.rr`): `wantReread` queues it and
  `flushRereads`, run at the end of every `Update`, starts `Deps.Tick(5s) →
  rereadMsg{n, gen}`. A newer request for the same issue replaces the
  generation, so the old timer does nothing.
- **A newer change restarts it:** any other change on the issue that settles
  successfully while its timer is running or owed starts it again (`settled`).
- **Skipped while pending:** if the issue has pending changes when the timer
  fires, nothing is read and the entry stays as owed; the next successful
  settle on that issue schedules a new timer.
- A deleted issue's timer finds the issue gone and does nothing. Quitting
  clears every timer (`cancelRereads`) before `tea.Quit`.

---

## 9. Keymap

### 9.1 One source

```go
type Action int
const (
    // views and app
    ActViewIssues Action = iota; ActViewPRs; ActViewBoard; ActNextView; ActPrevView
    ActHelp; ActQuit; ActRefresh
    // navigation
    ActUp; ActDown; ActLeft; ActRight; ActTop; ActBottom; ActHalfDown; ActHalfUp
    ActNextComment; ActPrevComment; ActOpen; ActBack
    // lists
    ActFilter; ActMine; ActState; ActBrowser; ActCopyURL
    // issue actions
    ActNew; ActQuickAdd; ActEdit; ActAssign; ActLabel; ActComment
    ActClose; ActCloseNotPlanned; ActStatus; ActStatusPrev; ActStatusNext; ActDelete
    // board
    ActChooseProject; ActHiddenInfo
    // text mode
    ActSubmit; ActPreview; ActExternalEditor; ActDiscardDraft; ActNextField; ActPrevField
    // pickers and quick-add preview
    ActToggle; ActApply; ActDoneItems
    // confirms
    ActYes; ActOverwrite
    actCount
)

type Keymap struct{ b [actCount]key.Binding }

func DefaultKeymap() Keymap
func (k Keymap) Binding(a Action) key.Binding
func HelpText(s ScreenID, a Action, k Keymap) (key, desc string) // per-context description overrides
```

`DefaultKeymap` is the **only** place key strings appear. It is the table of
UI 4 verbatim:

| Action | Keys | Help |
|---|---|---|
| `ActViewIssues` / `ActViewPRs` / `ActViewBoard` | `1` / `2` / `3` | views |
| `ActNextView` / `ActPrevView` | `tab` / `shift+tab` | next / previous view |
| `ActDown` / `ActUp` | `j` `down` / `k` `up` | move |
| `ActLeft` / `ActRight` | `h` `left` / `l` `right` | column |
| `ActTop` / `ActBottom` | `g` `home` / `G` `end` | top / bottom |
| `ActHalfDown` / `ActHalfUp` | `ctrl+d` `pgdown` / `ctrl+u` `pgup` | half page |
| `ActNextComment` / `ActPrevComment` | `]` / `[` | comments |
| `ActOpen` | `enter` | open |
| `ActBack` | `esc` | back |
| `ActFilter` | `/` | filter |
| `ActMine` | `m` | only mine |
| `ActState` | `s` | state |
| `ActRefresh` | `r` | refresh |
| `ActBrowser` | `o` | open in browser |
| `ActCopyURL` | `y` | copy URL |
| `ActNew` | `n` | new issue / new here |
| `ActQuickAdd` | `A` | quick add |
| `ActEdit` | `e` | edit |
| `ActAssign` | `a` | assignees |
| `ActLabel` | `L` | labels |
| `ActComment` | `c` | comment |
| `ActClose` | `x` | close / reopen |
| `ActCloseNotPlanned` | `X` | close as not planned |
| `ActStatus` | `M` | board status |
| `ActStatusPrev` / `ActStatusNext` | `<` / `>` | move on board |
| `ActDelete` | `D` | delete |
| `ActChooseProject` | `P` | choose project |
| `ActHiddenInfo` | `i` | hidden items |
| `ActHelp` | `?` | help |
| `ActQuit` | `q` | quit |
| `ActSubmit` | `ctrl+s` | save / create / send / preview |
| `ActPreview` | `ctrl+r` | preview |
| `ActExternalEditor` | `ctrl+o` | $EDITOR |
| `ActDiscardDraft` | `ctrl+x` | discard draft |
| `ActNextField` / `ActPrevField` | `tab` / `shift+tab` | field |
| `ActToggle` | `space` | toggle |
| `ActApply` | `enter` | apply |
| `ActDoneItems` | `d` | done items |
| `ActYes` | `y` | yes |
| `ActOverwrite` | `o` | overwrite |

`ctrl+c` is not an action. It is handled before dispatch (9.2).
`ActStatusPrev` and `ActStatusNext` also bind `shift+left` and `shift+right`.

### 9.2 Dispatch

```
App.Update(tea.KeyPressMsg k):
  1. k is ctrl+c → the quit flow (8.8), in any mode
  2. too small → only q acts, and quits at once (no pending-writes question)
  3. target := top overlay, else the active tab's top screen
  4. if target.Mode() == ModeText:
        forward k to target unchanged. The target handles esc, tab, enter and its
        ctrl+ actions itself, and gives every other key to its text input
        return
  5. for each a in target.Actions(ctx): if key.Matches(k, keys.Binding(a)):
        return target.Update(ctx, ActionMsg{a})
  6. an overlay is open → forward k raw to it (e.g. a letter that starts a
     picker's filter)
  7. global actions (views, refresh, help, quit)
  8. otherwise ignore
```

In navigation mode, screens and overlays receive **actions**, never keys, so
none of them can bind a key the keymap does not know.

### 9.3 Footer and help

- **Footer in text mode** (typing in a filter): `enter keep filter  esc clear
  filter`, because letters are text there (dogfood).
- **Footer:** `target.Actions(ctx)` in priority order, rendered as `key desc`
  pairs with `HelpText`, as many as fit the footer width minus the position
  text (UI 1). When a toast is showing, it replaces them.
- **Help** (`overlay_help.go`, as built): a full-body overlay generated from
  the keymap, so it cannot drift. It lists every action the screen it was
  opened from enables, plus the global ones, in the groups Navigate, Issue
  actions and Views & app (`helpGroups`), one column each; paired keys use
  `helpPairs` (`j/k ↓/↑`, `< >`, `tab ⇧tab`). On the Issues, Issue and Board
  screens a **Writing** block follows (`writingActions`): the keys inside the
  forms, the comment form and quick add (`ctrl+s`, `ctrl+r`, `ctrl+o`, `tab
  ⇧tab`, `d`, `ctrl+x`), since help cannot open while typing (Phase 9).
  `j`/`k` scroll; `esc` or `?` close. bubbles' `help.Model` is not used.

### 9.4 The rule test

`TestKeymapSameKeySameAction` collects every key string in `DefaultKeymap`
and asserts each maps to one action, except for this documented list of
context-dependent keys:

| Key | Actions | Why it is safe |
|---|---|---|
| `enter` | Open, Apply, newline/next field in text mode | never active in the same context |
| `esc` | Back | one meaning, "go back one level" |
| `tab`, `shift+tab` | Next/PrevView, Next/PrevField | fields only in text mode |
| `y` | CopyURL, Yes | Yes only inside a Confirm |
| `o` | Browser, Overwrite | Overwrite only inside Conflict |

`TestKeymap_textModeKeysFreeInTextarea` asserts that `ctrl+s`, `ctrl+r`,
`ctrl+o` and `ctrl+x` are not bound by `textarea.DefaultKeyMap()`. In bubbles
v2.2.1 that keymap binds `ctrl+a b d e f g h k m n p t u v w`,
`ctrl+shift+c`, `alt` combinations and named keys *(read: every binding
listed)*.

---

## 10. Layout

### 10.1 Frame

```go
type Rect struct{ X, Y, W, H int }
type Frame struct{ Header, SubBar, Body, Footer Rect }

func ComputeFrame(w, h int) (Frame, bool) {
    if w < 80 || h < 24 {
        return Frame{}, false
    }
    return Frame{
        Header: Rect{0, 0, w, 1},
        SubBar: Rect{0, 1, w, 1},
        Body:   Rect{0, 2, w, h - 3},
        Footer: Rect{0, h - 1, w, 1},
    }, true
}
```

This gives a body of 21 rows at 80×24 and 37 rows at 140×40 (UI 1). Bands are
built as strings exactly `w` cells wide, using `lipgloss.Width` and
`ansi.Truncate(s, w, "…")`, and joined with `\n`.

### 10.2 Column fitting

```go
type Col struct {
    ID       ColID
    Min, Max int    // cells; Max 0 = flexible
    Need     int    // the flexible column's comfortable width (step 4)
    Drop     int    // higher drops first; 0 = never
    MinWidth int    // only shown when the body is at least this wide (0 = always)
}
type Placed struct{ ID ColID; X, W int }

func Fit(cols []Col, width int) []Placed
```

`Fit` works in four steps:

1. Remove columns whose `MinWidth > width`.
2. Set every column to `Min` (gaps of 1 included in the total).
3. While the total exceeds `width`, remove the column with the highest `Drop`.
4. Give the rest to the flexible column up to its `Need`, then grow the
   others towards `Max` in ascending `Drop` order, and give anything left
   over to the flexible column.

| Issue list column | Min | Max | Drop | MinWidth |
|---|---|---|---|---|
| state: gutter (`▌`, `⠋`, `?`) + glyph | 2 | 2 | 0 | |
| number | 6 | 7 | 0 | |
| title | 24 | flex, Need 44 | 0 | |
| updated | 4 | 14 | 2 | |
| assignees | 10 | 14 | 3 | |
| labels | 10 | 24 | 4 | |
| board status | 12 | 14 | 5 | 120 |

| PR list column | Min | Max | Drop | MinWidth |
|---|---|---|---|---|
| state: gutter + glyph | 2 | 2 | 0 | |
| number | 6 | 7 | 0 | |
| title | 24 | flex, Need 36 | 0 | |
| checks | 2 | 9 | 1 | |
| updated | 4 | 14 | 2 | |
| review | 10 | 19 | 3 | |
| branch | 12 | 24 | 4 | |
| author | 10 | 14 | 5 | 100 |

Cells are truncated with `…`, never wrapped. Review and checks use the short
forms below 140 columns and words at 140 or more (UI 3.3).

Below 120 columns a row's labels may borrow the room its assignee cell does
not use (`borrowForLabels`, Phase 9): when the labels are cut and the
assignee is narrower than its column, the labels extend into it and the
assignee moves to the column's right edge, one space apart. Relative times
under a minute read `now` in list cells and `just now` in sentences
(`agoPhrase`: "synced just now", never "0s ago").

### 10.3 Detail side panel

When `Body.W >= 120`, the detail screens draw a 32-cell panel on the right (its State row uses the short form `○ Open`, `✓ Closed`, `⊘ Not planned`, which fits; dogfood)
with a 1-cell `│` separator. The content area is `Body.W - 33` wide. Below
120, the metadata block is stacked above the rule (UI 3.2). Markdown is
rendered at `min(content width, 100)` (UI 1).

### 10.4 Board columns

```go
func BoardWindow(bodyW, n, focus, offset int) (colW, first, count int) {
    // "‹N" and "N›" take 3 cells each, with a one-cell gap between columns:
    // count*colW + (count-1) + 6 <= bodyW.
    colW = clamp((bodyW-5)/max(n, 1)-1, 24, 40)
    fit := max(1, (bodyW-5)/(colW+1))
    count = min(fit, n)
    first = clampWindow(offset, focus, count, n)   // move the window minimally to show focus
    return
}
```

At 80 columns this gives 3 columns of 24 (UI 3.5). At 140 it gives 5 of 26.
A card is 4 rows: 3 lines plus a gap. Each column scrolls by itself to keep
its selected card visible. When cards are cut, `…N more` is the column's last
row, so no blank rows hang below it (Phase 9).

### 10.5 Overlays with lipgloss layers

```go
base := a.renderBase(theme)       // theme = a.theme.Dimmed() when an overlay is open
layers := []*lipgloss.Layer{lipgloss.NewLayer(base)}
for i, o := range a.overlays {
    content, box := o.View(ctx, frame.Body)
    layers = append(layers, lipgloss.NewLayer(content).X(box.X).Y(box.Y).Z(i+1))
}
content := lipgloss.NewCompositor(layers...).Render()
```

- `NewLayer`, `X`, `Y`, `Z`, `NewCompositor` and `Render() string` are in
  lipgloss v2.1.0 `layer.go` *(read; compiled)*.
- The backdrop is dimmed by drawing the screens with `Theme.Dimmed()`, which
  maps every foreground to `Muted` (UI 5.1). It is not done by recolouring
  ANSI output.
- Full-body overlays (forms, quick add, help) use `box = frame.Body`. Pickers,
  confirms and the conflict box are centred, at the sizes in UI 3.8 and 3.12.

### 10.6 `View()`

```go
func (a *App) View() tea.View {
    v := tea.NewView(a.render())
    v.AltScreen = true
    v.WindowTitle = a.title()           // "todos · pkieltyka/todos", "todos · #142"
    v.Cursor = a.cursor()               // top overlay's Cursor(), offset by its box; nil in nav mode
    v.ProgressBar = a.progress()        // quick-add run (12.4); nil otherwise
    return v
}
```

These are all fields on `tea.View` in bubbletea v2.1.1 *(read)*. The textarea
and textinputs run with `SetVirtualCursor(false)`, and their `Cursor()`
(`*tea.Cursor`) gives the real terminal cursor (UI 5.1).

---

## 11. Screens: behaviour notes

The drawings are in UI 3. This section covers the logic behind them.

| Screen | Notes |
|---|---|
| IssueList | Rows are `lists.issues.Numbers` → `Effective` → local filter. Header and sub-bar states follow UI 3.1 and 5.3. Each empty state (loading, none, no match, failed with hint) is a function of `Phase`, `Err` and filter results. |
| IssueDetail | One viewport holds the body plus every comment, each rendered separately (`markdown.Key` per body and per comment). `anchors` are recomputed after every render, for `]`/`[`. On open it calls `loadIssue(n, ReasonOpen)`, which always reads; whatever is cached is shown meanwhile. |
| PRList, PRDetail | Read-only. `m` toggles the local author filter. `Closes` is plain text (UI 9 #9). |
| Board | Columns from `Ctx.Columns` (7.5). `<`/`>` step through `columnStatuses()`: "No status" first, then the columns in `Board.Order`, not `Info.Options`. They do nothing on an issue with no effective placement (toast, UI 4), and focus follows the stepped card (UI 3.5). `n` and `A` pass the focused column, with `AddToBoard: true` (8.9, 12.1). `i` shows the hidden-items toast. `P` pushes `BoardSetup(Choosing)`, which lists `BoardCandidates`. |
| BoardSetup | NoBoard, MissingScope and NoStatusField show UI 3.16 text with the hint from the error. In NoBoard and NoStatusField, `P` switches the screen to Choosing and lists `BoardCandidates` there (UI 3.16). If that list fails, the screen leaves its loading state, shows the reason and hint, and `P` tries again (finding 11). Several shows the list from `de.Candidates` (A6), and `enter` sends `ChooseBoard`. `r` runs the recovery of 7.6 in every state. |

The status picker (`M`) lists, in this order:

1. every column in `Board.Order` order (`columnStatuses()`, as built; the same
   order `<`/`>` step through)
2. `No status`
3. `Remove from board`, only when the effective issue is on the board

In the create form and the quick-add preview, its last entry is instead `Not
on board` (`AddToBoard = false`). That entry is not offered when opened from
the Board view, where `AddToBoard` is always true.

---

## 12. Quick add

Built on `Service.PlanQuickAdd` and `Service.RunQuickAdd` (A4). Draft rules
come from A7.12: saved 500 ms after the last keystroke and at every step
change. During a run it is rewritten before each item is sent and after it
settles. Across processes, the last save wins (A9). There is no conflict
detection.

### 12.1 Opening

`A` opens the `QuickAdd` overlay in **Write** and sends `DraftStore.Load(repo)`.
If the App's draft queue is still saving (a close-save or tick-save not yet
landed), the load waits (`loadWaiting`) and is sent when the queue drains, so a
quick reopen never reads the file before the latest save.

- `draftLoadedMsg{d, ok}`: the editor gets `d.Text`. `marks` come from
  `d.Uncertain`, plus `d.InFlight` if a crash left one, which counts as
  uncertain (A7.12). Each span maps to the line holding its `StartByte`. A
  span whose bytes no longer hash to `SourceSHA256` is dropped. Until the load
  lands, the editor is read-only.
- Load error (an unreadable or malformed file, kind `config`): the editor
  opens empty, with the banner `✗ Saved draft unreadable (left on disk):
  <message>` (shortened from the first wording so it fits 80 columns).
  `saveOff = true` for this session, so the file is never overwritten.
- From the Board view: `opts.Status` = the focused column (nil for "No
  status") and `opts.AddToBoard = true`. From the Issues view: no status,
  `AddToBoard = false`.
- `opts.Checked` is always reset to `skip` (UI 3.10).

### 12.2 Saving while writing

*As built (Phase 8):* there is no writer goroutine. The App owns a small queue
in `draft.go`:

```go
type draftJob   struct { repo domain.RepoRef; d *domain.Draft } // d nil → Delete
type draftQueue struct { saving bool; next *draftJob }
```

- **One save at a time.** `saveDraft` returns a Cmd only when nothing is being
  saved; otherwise the job replaces `next`. `draftSavedMsg` sends `next`. So
  saves land in order and the latest text wins, which is what the writer
  goroutine was for, with nothing to shut down.
- Every keystroke that changes the text bumps `gen` and schedules
  `Deps.Tick(500ms) → draftTickMsg{gen}`. If `gen` is still current when the
  tick arrives, the draft is queued. **At most 500 ms of typing can be lost
  to a crash** (5.5).
- **Step changes save.** Write → Preview, Preview → Write, closing the
  overlay, and Preview → Running. Before a run, `enter` queues a save and sets
  `starting`; the run starts only when the queue has drained without an error.
  A failed save there aborts with the toast `Couldn't save the draft, so
  nothing was created: <reason>` and stays in Preview.
- **Quit** calls `flushDraftNow`, a synchronous `Save` (or `Delete` when the
  text is empty) just before `tea.Quit`, on both the direct and the confirmed
  path. There is no later Update to deliver an async result.
- Editing removes a "may exist" mark once its exact source bytes are no longer
  in the text (UI 3.11), so the span leaves `Uncertain` in the next save.
- Empty text → Delete. `ctrl+x` discards: a Confirm if the text is not empty,
  then Delete.
- A save error never touches the text. It shows `Draft not saved: <reason>`
  once, and the next save tries again. A successful save resets that latch,
  so a later failure is shown again.
- `saveOff` (12.1) turns saving off for the session.

### 12.3 Preview

`ctrl+s` in Write flushes, then sends `LoadPlan{seq, text, opts}`.

- `a`, `L` and `M` open the pickers with target = batch. Applying one changes
  `opts` and re-plans with `seq+1`. `d` toggles `opts.Checked` between `skip`
  and `close` and re-plans. A `planMsg` with an old `seq` is dropped.
- Rows come from `plan.Items`. The header counts open items and done items
  separately (5.5). Each done row shows `skipped: done` or `create, then
  close`. The note `done items are not added to the board` is always shown
  (UI 3.10, 5.5).
- `plan.Problems` (each with a line number) replace the create hint, and
  `enter` does nothing while there are any. A `PlanQuickAdd` error (auth,
  network) shows the same way, and `ctrl+s` retries.
- `esc` → Write, with the text unchanged. The plan is discarded.

### 12.4 Running

```go
type QuickAddRun struct {
    ID     uint64
    stop   atomic.Bool
    events chan tea.Msg      // qaProgressMsg …, then exactly one qaDoneMsg; then closed
    rows   []RowState        // per plan item, owned by Update
    total  int               // plan.WriteCount, for the progress bar
    done   int
}

type RowState struct {
    Phase   string           // "", creating, closing, placing, settled
    Outcome domain.QuickAddOutcome
    Number  int
    Err     *domain.Error
}
```

`enter` in Preview:

1. Flush the draft. Turn the Update-side saver off, because the run goroutine
   is now the only writer.
2. Start the run in a goroutine, from a Cmd:

   ```go
   go func() {
       d := draft                                   // private copy, owned by this goroutine
       res, err := svc.RunQuickAdd(ctx, plan, domain.QuickAddRunOptions{
           StopRequested: run.stop.Load,
           OnProgress: func(p domain.QuickAddProgress) error {
               if err := durable(&d, plan, p, drafts); err != nil {
                   return err                         // the service stops before the next write
               }
               run.events <- qaProgressMsg{run.ID, p}
               return nil
           },
       })
       run.events <- qaDoneMsg{run.ID, res, err, d}
       close(run.events)
   }()
   ```

3. Return `waitEvent(run.events)`, which receives one message. Each handled
   `qaProgressMsg` re-issues it. `events` has capacity 64, so the UI never
   slows the executor.

   *As built:* the goroutine is started through `Deps.Go` and its messages go
   through `Deps.Send`. `Run` sets `Send = Program.Send`, so in production
   messages go straight into the program and the channel path above is only
   the fallback when `Send` is nil. The test harness sets both, so it counts
   the run goroutine and every message it sends as running work until
   handled; without that, the harness could settle before a progress event
   was drained.

`durable` (in `draft.go`) applies A7.12 and saves synchronously with
`drafts.Save(d)`:

| Event | Draft change |
|---|---|
| `Phase == "creating"` | `d.InFlight = span(item)`, saved **before** the create is sent |
| `Phase == "closing"` or `"placing"` | none. `InFlight` stays set until `settled` |
| `Phase == "settled"` | record the outcome in a per-item table, then **rebuild** `d` (below), with `InFlight = nil` |

The rebuild:

- `d.Text` = the concatenation, in plan order, of `Item.Source` for every
  item whose outcome is not `created` and not `skipped`.
- `d.Uncertain` = one span per `uncertain` item, over the rebuilt text, with
  the `SourceSHA256` of its bytes.
- Spans are always recomputed from `Item.Source`, never by shifting offsets of
  an earlier draft.
- Created items (including created but not closed, or not placed) leave the
  text, because they exist and must never be sent again (5.5).

**Stop and quit:**

- `esc` sets `stop` and never cancels `ctx`. The service finishes the whole
  current item, including its close or placement, then stops (A4). The
  overlay shows `stopping after this item…`.
- Quitting during a run asks first (8.8). On `y`, the program context is
  cancelled and the request in flight is aborted. Its `InFlight` span is
  already on disk, so the item comes back flagged "may exist" next time
  (A7.12).

**While running:**

- Each `qaProgressMsg` updates `rows[p.Index]`. Once an issue exists,
  `store.Put(*p.Item.Issue, now)`, and its number is prepended to the issues
  list, so the list behind the overlay fills in live.
- A done item filed as closed shows creating → closing → `✓ #153 closed`
  (UI 3.11).
- `tea.View.ProgressBar` is set to
  `tea.NewProgressBar(tea.ProgressBarDefault, 100*settled/items)` during the
  run and is nil afterwards. `ProgressBarDefault` was read in bubbletea
  v2.1.1. The denominator is all plan items (skipped ones settle at once), not
  `WriteCount`.
- The running header is `Quick add · creating X of N`: N is `plan.ToCreate`
  and X is the settled items that were not skipped, plus one. A skipped done
  item is not counted (live run: it read `creating 2 of 5` for 4 issues).
- *Live run (Phase 9):* the preview keeps an item's `⚠ may exist` flag on its
  row, since that is where `enter` sends it; and a stop the user asked for
  (`esc`) reads `✗ Quick add stopped as you asked.`, because the real service
  reports it as `operation interrupted`.
- `durable` is `runDraft.apply` in `draft.go`. `remaining` calls
  `quickadd.Remaining` (architecture 2 allows `tui → quickadd` for this only)
  with the outcomes so far, items without one counted as unattempted, and adds
  the in-flight span: the item's start is the sum of the `Source` lengths of
  the retained items before it, so duplicate items resolve by index.

### 12.5 Done and stopped (5.5, UI 3.11)

On `qaDoneMsg{res, err, d}`, `Update` adopts `d` and turns the saver back on.

- **Complete:** no failed, uncertain or unattempted items, and every created
  item is closed or placed as asked. Send a Delete job, close the overlay, and
  toast `Created N issues #a–#b` (`· M skipped` if any were skipped).
- *As built:* "may exist" marks are drawn as a list under the editor
  (`⚠ may exist: check GitHub before resending · <first line>`), not inline in
  the textarea, which has no per-line decoration.
- **Stopped:** go back to **Write** with:
  - the text = `d.Text`, which holds exactly the failed, uncertain and
    unattempted items, in their original bytes.
  - `marks` from `d.Uncertain`: `⚠ may exist: check GitHub before resending`.
    An item that failed for a known reason is kept without a mark (UI 3.11).
  - a banner built from `res` and `err` (UI 3.11): the stop reason, the
    created numbers, and each issue that was created but not placed (`press M
    on it`) or not closed (`press x on it`). As built, one sentence per line
    so none is cut at 80 columns; the first line is `✗ Stopped at "<title>":
    <reason>.` or `✗ Quick add stopped: <reason>.` (the service's `quick add
    stopped:` prefix is not repeated).
- `ctrl+s` plans the remaining text again. That is "resume". There is no
  resume state, so a created item can never be sent twice.

---

## 13. Pickers

| Kind | Items | Multi | Max | Apply |
|---|---|---|---|---|
| Assignees | `@me` first, then `meta.Users` by login | yes | 10 (11th refused with a toast) | `ApplyChange{Assignees: diff}`, or set the form or batch field |
| Labels | `meta.Labels` by name, with a coloured `●` | yes | — | `ApplyChange{Labels: diff}`, or set the form or batch field |
| Status | 11's list | no | — | `ApplyChange{Board}`, or set the form or batch `Status`/`AddToBoard` |
| Project | `BoardCandidates` (linked first) | no | — | `ChooseBoard` |

- The picker opens in navigation mode: `j`/`k` move, `space` toggles, and `/`
  or any printable letter except `j`, `k` and `space` focuses the filter.
  `enter` applies. `esc` clears the filter first, then closes.
- `initial` is taken from the **effective** issue (8.3).
- *Dogfood (live) fixes:* the spinner tick chain runs while a read is loading
  **or** a change, form write or quick-add run is sending (`spinNeeded`);
  before, a pending change's gutter spinner stood still. On the board, focus
  follows the focused card (UI 3.5, Phase 9 review finding 1): in every
  `Sync`, when `sel` is no longer in the focused column but is in another
  one, for any reason (a step, a rejected step snapping back, an uncertain
  change resolving, a refresh, the board's workflow), the focus moves to that
  column. Only a card gone from the board falls back to a neighbour
  (`reselect`). Moving focus onto an empty column clears `sel`, so the rule
  does not pull focus back. The edit form clears `saving` while the conflict dialog waits for
  the user; `o` sends and sets it again.
- Assignee and label pickers also list, ticked, every initial value the
  repository no longer lists (a departed assignee, a deleted label), after the
  repository's own, so it can be unticked: removal is judged against the issue
  (functional 4.1). They stay listed when fresh metadata lands.
  `diff = {Add: selected \ initial, Remove: initial \ selected}`. An empty
  diff closes the picker without a change.

---

## 14. The editor (`editor.go`, in package `tui`)

### 14.1 Model

```go
type Editor struct {
    ta      textarea.Model     // ShowLineNumbers off, MaxHeight 0, CharLimit 0, no blink, real cursor
    preview bool
    vp      viewport.Model
    owner   uint64             // the form's ID, for routing editor messages
    ext     []string           // argv from Env.Editor; nil = not offered
    w, h    int
}

func newEditor(owner uint64, ext []string, placeholder string) Editor
func (e *Editor) SetSize(w, h int)
func (e *Editor) Value() string
func (e *Editor) SetValue(s string)
func (e *Editor) Update(c *Ctx, msg tea.Msg) (tea.Cmd, bool)   // ctrl+r, ctrl+o, keys, paste, editor results
func (e *Editor) View(c *Ctx) string                          // textarea, or the preview via Ctx.Markdown
func (e *Editor) Renders() []renderReq                        // the preview's render, when open
func (e *Editor) Cursor() *tea.Cursor                          // relative; the form offsets it
func (e *Editor) LineCol() (int, int)                          // footer "L4:C19"
```

The textarea's cursor blink is turned off: with a real cursor there is
nothing to blink, and a blink timer would be a hidden `tea.Tick` that no test
could drive. The external editor runs through `Deps.Exec` (default
`tea.ExecProcess`), which the harness replaces with a direct `cmd.Run`.

### 14.2 Preview toggle (4.8)

`ctrl+r` in Write:

1. `key, cmd := opts.Render(value, width)`.
2. If `opts.Cached(key)` hits, show it at once. Otherwise show `⠋ rendering…`
   and return `cmd`.
3. On `renderedMsg{key}`, fill the viewport only if `key == m.preview`.

In Preview, `j`/`k`/`pgup`/`pgdn` scroll and other keys do nothing.
`ctrl+r` returns to Write with the cursor unchanged.

### 14.3 External editor hand-off (4.8)

```
ctrl+o (only when opts.External != nil):
  f := os.CreateTemp("", "todos-*.md")         // mode 0600 on Unix
  write m.Value(); close
  cmd := exec.Command(External[0], append(External[1:], f.Name())...)
  return tea.ExecProcess(cmd, func(err error) tea.Msg { return editorDoneMsg{owner, f.Name(), err} })

editorDoneMsg{path, err}:
  Cmd: text, rerr := os.ReadFile(path); os.Remove(path) → editorReadMsg{owner, text, err or rerr}

editorReadMsg:
  if the editor exited with an error (vim :cq): keep the body; toast "Editor exited with an error; body unchanged"
  else if the read failed: keep the body; toast the error
  else: SetValue(text, without one trailing "\n" if the original had none); cursor at the end
```

- `tea.ExecProcess(*exec.Cmd, ExecCallback)` is in bubbletea v2.1.1 `exec.go`
  *(read)*. It releases the terminal and restores it after.
- The draft goes only through the file. No GitHub text is ever passed as an
  argument or through a shell (10).
- The temp file is removed on every path. If the TUI crashes while the editor
  is open, the file stays in `$TMPDIR`.

### 14.4 The edit conflict check (4.5)

The form keeps `orig`: the `IssueDetail` the form read itself when it opened
(`formRead`, in `SlotForm`; not the detail screen's copy), and `normBody =
normalise(orig.Body)`, where `normalise` turns `\r\n` into `\n`. Bodies edited
on github.com often contain CRLF, and the textarea holds `\n`.

```
ctrl+s (edit):
  edit := domain.EditIssue{}
  if title != orig.Issue.Title:  edit.Title = &title
  if body  != normBody:          edit.Body  = &body
  if edit is empty: close the form, toast "No changes"
  saving = true; formRead(formID, n, precheck = true)

formReadMsg{precheck, current}:
  (current is also Put into the store as usual)
  if current.Issue.Title == orig.Issue.Title && current.Body == orig.Body:
      sendEdit (EditIssue)
  else:
      saving = false (nothing is sent while the user decides; dogfood)
      push Conflict:  "o overwrite with my version   esc keep editing"
        o   → sendEdit, saving = true             // same fields; no second check (4.5)
        esc → pop; text kept
  precheck error → inline error; ctrl+s tries again
```

The pre-check compares raw API values with raw API values, so it needs no
normalisation.

---

## 15. Theme

```go
type Theme struct {
    Dark bool
    Text, Muted, Subtle, Accent, SelectionBg   color.Color
    Open, Done, Inactive, Pending, Error       color.Color
    dimmed bool                                 // under an overlay
    plain  bool                                 // --no-color: Markdown uses Plain
}
// As built, the styles are methods (text(), muted(), accent(), pending(),
// selected(row), errorS(), …), not lipgloss.Style fields.

func NewTheme(dark bool) Theme                 // UI 6.1 hex values via lipgloss.LightDark(dark)
func (t Theme) Dimmed() Theme                   // every foreground → Muted, no backgrounds
func ParseOverride(v string) (dark, ok bool)    // "light" → false,true; "dark" → true,true; else ok=false
func LabelColor(t Theme, hex string) color.Color // "#"+hex; malformed → t.Muted
```

- **Detection:** if `Env.Theme` parses, use it and send no query. Otherwise
  `Init` sends `tea.RequestBackgroundColor`. Until `tea.BackgroundColorMsg`
  arrives, the theme is dark (UI 6). On arrival: `NewTheme(msg.IsDark())`,
  and the Markdown style becomes `markdown.Dark` or `markdown.Light`. Cached
  renders of the other style are simply not hit again.
- `lipgloss.LightDark(isDark) LightDarkFunc` is in lipgloss v2.1.0 *(read)*.
  Bubble Tea downsamples colours to the terminal's profile. colorprofile
  v0.4.3 honours `NO_COLOR`, `CLICOLOR` and `CLICOLOR_FORCE` (`env.go`, read).
  Meaning never depends on colour alone (UI 6).

---

## 16. Rendering Markdown in the TUI

The TUI uses `markdown.Renderer` (see `markdown.md`):

- Every body and every comment has its own `markdown.Key` (source, width,
  style).
- `Update` calls only `Renderer.Cached(key)`. On a miss, the `Render` intent
  becomes a Cmd calling `Renderer.Render`, and `renderedMsg` delivers it.
- On resize, the old render stays on screen until `resizeSettledMsg` (100 ms
  debounce), and then the new renders are requested. There is no blank
  flash and no render storm while dragging.
- A detail screen rebuilds its content on every comment page and every render
  batch that lands: about 45 times while 500 comments load. The rebuild
  reuses each comment's drawn lines (`commentBlocks`, keyed by the header
  label, the Markdown as drawn now, width and theme), so a comment is styled
  once per version, not once per rebuild. Entries not used by the latest
  rebuild are dropped. What each rebuild still costs is the viewport
  measuring every line (`SetContentLines`), bounded by the 500-comment cap.
- A render reaches the renderer's 128-entry cache before its `renderedMsg` is
  handled, and later renders can evict it in between. The detail screens read
  Markdown through `Ctx.markdownKept`, which copies a cache hit into
  `lastRender`, so a comment once drawn rendered never drops back to plain
  text while the rest load. `Ctx.Markdown` itself stays read-only, because the
  editor preview calls it from `View`.
- Titles, label names, logins, branch and project names pass through
  `markdown.SanitizeLine` when they enter the store (`Put`, page handlers).
  Everything drawn is therefore clean.

---

## 17. What the TUI needs from `servicetest`

`internal/service/servicetest` is owned by @lead-dev (A2, A8). The TUI's
tests rely on the following, all included by @lead-dev. The exact API is in
`service.md`, section "Shared Service fake: internal/service/servicetest",
which is authoritative.

| Need | Used for |
|---|---|
| `New(Fixture) *Fake`, `DefaultFixture()` with deterministic times relative to 2026-11-13T12:00:00Z | every model test and golden |
| `Fake.Hold(method string, number int) *Gate`, with `Gate.Started`, `Release()`, `Fail(error)`; gates consumed once, in registration order | staging races (8.3, 8.5, 8.6, 6.3) |
| `Gate.ApplyThenFail(error)`: apply the effect to the fixture, then return the error | "uncertain, but it did apply" |
| `Fake.Mutate(func(*Fixture))`: change the fixture as if someone did it on github.com | edit conflict (14.4), refresh while pending (8.7) |
| `Fake.SetRateLimit(domain.RateLimit)` | auto-refresh pause (7.7) |
| `Fake.Calls() []Call` with method, number, arguments (add, remove, status, filter, cursor), and start and end sequence numbers | "one in flight per issue", "at most four", "held queue sends nothing" |
| Fixture variants: a board with a non-empty "No status" column, `HiddenCount > 0` and `Truncated`; `BoardInfo` failing `no_board` with several `Candidates`, `no_board` with none, `missing_scope`, and no Status field; a generator for more than 500 issues (for example 1,204) | board states, truncation |
| The fake's quick-add methods (Phase 5) simulate the progress phases simply: `OnProgress` for creating, closing, placing and settled, `StopRequested` honoured between items, a stop when `OnProgress` returns an error, a gate per item, and no sleeping. They are the fake's own simple simulation, not the real executor | quick-add tests (Phase 8) |

---

## 18. Test plan

### 18.1 How

1. **Pure unit tests:** layout, column fitting, `Apply`/`Effective`, the
   scheduler, reselection, board derivation, the keymap rules, and `durable`.
2. **Model tests:** `App` with `servicetest.Fake`, a fixed clock, a fixed
   theme and a fake `DraftStore`. `Update` is driven directly. The helper
   `h := newHarness(t, fixture)` runs every returned Cmd in its own goroutine
   and feeds the messages back in arrival order. `h.settle()` waits until no
   Cmd is running except those held at a gate. No `tea.Program`, no sleeps.
   A gate counts as holding only while its own call is still parked: the
   harness knows which call each gate catches (the next matching call after
   it is registered) and checks the fake's call log. So a held call that the
   code under test cancels, such as an auto-refresh replaced by `r`, is waited
   for like any other Cmd.
   `settleDeadline` (30 s) only detects a hang. A passing test never waits for
   it, and one settle can cover a whole 500-comment load on a slow, shared CI
   runner under `-race`.
3. **Golden renders:** `App.View().Content` at 80×24 and 140×40, with
   `golden.RequireEqual` from x/exp/golden v0.1.0, which has an `-update`
   flag *(read)*.
4. **End-to-end tests with teatest**, as built two (`e2e_test.go`):
   `TestE2E_startupToFirstFrame` and `TestE2E_qQuits`. The editor round trip
   and quit during a run are harness tests (`Deps.Exec` runs the editor
   directly; `TestExternalEditor_roundTrip`, `TestQuit_duringQuickAddRunAsks`).

### 18.2 Stable goldens

- A fixed clock: `2026-11-13T12:00:00Z`.
- Spinner frame 0. The tick is not run in golden tests.
- `testdata/TestGolden/<case>/{80x24,140x40}.golden` (`ansi.Strip`, readable
  layout diffs) for every case, and
  `testdata/TestGolden_ansi/{issue_list,issue_detail,board}_{dark,light}.golden`
  (TrueColor escapes) for one case per main screen.
- Markdown is rendered synchronously through a harness hook, not raced.
- `TERM`, `COLORTERM`, `NO_COLOR` and `TODOS_THEME` are cleared in `TestMain`.
- glamour, chroma and lipgloss are pinned (A10). A bump that changes goldens
  is reviewed as a diff.

### 18.3 teatest

`github.com/charmbracelet/x/exp/teatest/v2`
`v2.0.0-20261101151554-27ecf3e4d454` exists, is untagged, and its `go.mod`
requires bubbletea `v2.0.0-rc.1`. Minimum version selection lifts that to
v2.1.1, and the combination **compiles** (verified). I have **not run** it,
so its behaviour against 2.1.1 is **UNVERIFIED**. That is why only the four
end-to-end tests use it. Its API *(read)*: `NewTestModel(tb, m,
WithInitialTermSize(w, h))`, `Send`, `Type`, `WaitFor`, `FinalModel`,
`FinalOutput`, `RequireEqualOutput`.

### 18.4 Named test cases

**layout**
- `TestComputeFrame_80x24_body21`
- `TestComputeFrame_140x40_body37`
- `TestComputeFrame_tooSmall_79x24_80x23`
- `TestFit_dropsLabelsFirstAt80`
- `TestFit_titleNeverBelow24`
- `TestFit_statusColumnOnlyFrom120`
- `TestFit_prAuthorOnlyFrom100`
- `TestBoardWindow_80_threeColumnsOf24`
- `TestBoardWindow_140_fiveColumnsOf26`
- `TestBoardWindow_followsFocusMinimally`
- `TestSidePanel_at119Stacked_at120Panel`
- `TestTooSmall_keepsStateAndRecovers`

**store and asOf**
- `TestPut_olderAsOfIgnored`
- `TestPut_equalAsOfReplaces`
- `TestPut_writeArrivalBeatsEarlierSentRead`
- `TestPut_positionRecordedEvenWhenContentStale`
- `TestIssues_firstPageRendersBeforeBackground`
- `TestIssues_stopsAt500_showsTruncated`
- `TestIssues_stateChangeCancelsChain_dropsLatePages`
- `TestIssues_mineIsServerQuery`
- `TestIssues_pageOverlapKeepsRow`
- `TestIssues_refreshBuildsShadowAndSwaps`
- `TestIssues_refreshFailureKeepsRowsMarksStale`
- `TestPRs_mineIsLocal_showsMatchAndLoadedCounts`
- `TestBoard_mineIsLocal`
- `TestDetail_bodyBeforeComments_commentsCappedAt500`
- `TestDetail_commentBlocksDrawnOncePerVersion`
- `TestDetail_commentBlocksRedrawWhenTheirHeaderChanges`
- `TestDetail_popCancelsSlots`
- `TestPending_popKeepsHeldIssueRead`
- `TestReselect_keepsNumber`
- `TestReselect_goneClampsToIndex`
- `TestReselect_keepsOffset`

**board derivation**
- `TestColumns_followsGitHubOrder`
- `TestColumns_movedCardAtBottomOfNewColumn`
- `TestColumns_settledMoveStaysAtBottomUntilReload`
- `TestColumns_noStatusShownOnlyWhenNotEmpty`
- `TestColumns_removeFromBoardHidesCard`
- `TestColumns_issueMovedFromListAppearsOnBoard`
- `TestColumns_cap500CardsShowsNotice`
- `TestBoard_setupStates_noBoard_several_missingScope_noStatusField`
- `TestBoardSetup_stateByCandidateCount`
- `TestBoardSetup_PChoosesProjectInNoBoardAndNoStatusField`
- `TestBoardSetup_candidatesFailureShowsErrorAndRetry`
- `TestBoard_renamedStatusKeepsCards`
- `TestChooseBoard_clearsOldPlacementsAndReloads`
- `TestRecovery_authRetryRefreshesMetadataAndSession`, `TestRecovery_networkRetryIsPlain`
- `TestDetail_commentsErrorSurvivesBodySuccess`, `TestDetail_refreshFailureShowsReasonAndHint`
- `TestPRs_newerListBeatsOlderDetail`
- `TestAutoRefresh_retriesFailedInitialLoad`, `TestAutoRefresh_doesNotCancelCommentPaging`
- `TestIssues_staleSameQueryPageDropped`, `TestDetail_stalePopResultDropped` (fail with the request-ID check removed)
- `TestRun_cancelsReadsOnQuit`
- `TestBoard_refreshOnSetupCallsRefreshMetadataFirst`
- `TestBoard_chooseProjectReloadsInfoThenBoard`

**pending**
- `TestApply_setSemanticsIdempotent`
- `TestApply_boardPlacementCreatedWhenAbsent`
- `TestEffective_replaysInIDOrder`
- `TestMarks_addedRemovedBusyUncertain`
- `TestPending_oneInFlightPerIssue`
- `TestPending_atMostFourInFlight_FIFO`
- `TestPending_successPutsArrivalAsOf_sendsNext`
- `TestPending_rejectSnapsBack_toasts_laterQueuedStillRun`
- `TestPending_partialRemovedStepPutsIssue`
- `TestPending_partialAddedToBoardPutsIssue`
- `TestPending_uncertainHoldsQueue`
- `TestPending_uncertainReleasedByReadSentAfterFailure`
- `TestPending_uncertainNotReleasedByReadSentBefore`
- `TestPending_uncertainReleasedByListPage`
- `TestPending_uncertainAppliedAnyway_noToast` (`Gate.ApplyThenFail`)
- `TestPending_uncertainNotApplied_toastsCouldntConfirm`
- `TestPending_uncertainRereadRetriedOnAutoTick`
- `TestPending_refreshDoesNotOverridePendingField`
- `TestPending_closeThenUndoQueuesReopen`
- `TestPending_closeReopenConfirmationToast`
- `TestPending_pickerDiffAgainstEffective`
- `TestPending_deletedIssueDropsLateResult`
- `TestPending_stepOnIssueNotOnBoard_toastsNoChange`
- `TestPending_closedUnderOpenFilterStaysUntilSwap`

**forms and waiting writes**
- `TestCreate_waitsThenInsertsAndSelects`
- `TestCreate_fromBoardAlwaysAddToBoard_noStatusColumn`
- `TestCreate_fromIssuesAddToBoardOnlyWithStatus`
- `TestCreate_partialInsertsAndToasts`
- `TestCreate_uncertainKeepsFormNoRetry`
- `TestCreate_escWhileCreatingAsks`
- `TestEdit_noChangesCloses`
- `TestEdit_conflictDetected_overwrite`
- `TestEdit_conflictDetected_keepEditing`
- `TestEdit_noConflict_sendsOnlyChangedFields`
- `TestEdit_crlfBodyNotSentWhenUntouched`
- `TestComment_emptyRefused_successAppends`
- `TestDelete_requiresExactNumber`
- `TestDelete_warnsWhenViewerCannotDelete`
- `TestDelete_forbiddenStaysOpen`
- `TestDelete_successRemovesEverywhere_popsDetail`
- `TestPicker_assigneeMax10`
- `TestPicker_filterKeepsCursorByName_onMetaRefresh`
- `TestPicker_statusEntries_issue_form_boardForm`

**keys**
- `TestKeymapSameKeySameAction`
- `TestKeymap_textModeKeysFreeInTextarea`
- `TestFooter_fromActions_fitsWidth`
- `TestHelp_listsEveryEnabledAction`
- `TestHelp_listsWritingKeysAndPrevView`
- `TestTextMode_lettersGoToInput`
- `TestQuit_withPendingAsks`
- `TestQuit_ctrlcTwiceForces`
- `TestOverlay_blocksGlobalActions`

**auto-refresh**
- `TestAutoRefresh_skipsWithOverlay`
- `TestAutoRefresh_skipsUnder10Percent_headerShowsPaused`
- `TestAutoRefresh_skipsInFlight`
- `TestAutoRefresh_skipsRecent`
- `TestAutoRefresh_onlyVisibleView`
- `TestAutoRefresh_hiddenTabRefreshesOnSwitchWhenOld`

**editor**
- `TestPreview_rendersCurrentText`
- `TestExternalEditor_roundTrip` (harness, fake editor script)
- `TestExternalEditor_nonZeroExitKeepsBody_removesTemp`
- `TestExternalEditor_notOfferedWithoutEnv`
- `TestExternalEditor_argvSplitNoShell`

**quick add**
- `TestQuickAdd_loadRestoresTextAndMarks`
- `TestQuickAdd_inFlightOnLoadBecomesMark`
- `TestQuickAdd_staleSpanHashDropped`
- `TestQuickAdd_unreadableDraftNeverOverwritten`
- `TestQuickAdd_saves500msAfterLastKeystroke`
- `TestQuickAdd_writerLatestWinsInOrder`
- `TestQuickAdd_flushOnEveryStepChange`
- `TestQuickAdd_emptyTextDeletesDraft`
- `TestQuickAdd_discardAsksWhenNotEmpty`
- `TestQuickAdd_fromBoardStatusAndAddToBoard`
- `TestQuickAdd_checkedResetsToSkipOnOpen`
- `TestQuickAdd_replanOnBatchChange_dropsStalePlan`
- `TestQuickAdd_problemsBlockCreate`
- `TestQuickAdd_inFlightSavedBeforeCreate`
- `TestQuickAdd_inFlightKeptThroughClosingAndPlacing`
- `TestQuickAdd_durableRebuildsFromSource`
- `TestQuickAdd_draftSaveFailureStopsRun`
- `TestQuickAdd_progressFillsListLive`
- `TestQuickAdd_stopFinishesCurrentItem`
- `TestQuickAdd_stoppedTextIsRemainingSource_marksUncertainOnly`
- `TestQuickAdd_bannerNamesNotPlacedAndNotClosed`
- `TestQuickAdd_completeDeletesDraft_toasts`
- `TestQuit_duringQuickAddRunAsks`, `TestQuickAdd_crashReloadFlagsInFlight`
  (the in-flight mark is on disk before the create, so quitting leaves it)
- `TestQuickAdd_preRunFlushFailureSendsNothing`, `TestQuickAdd_reopenWaitsForCloseSave`,
  `TestQuickAdd_saveErrorShownAgainAfterASuccess`, `TestQuickAdd_inFlightSpanAfterCreatedAndDuplicates`,
  `TestQuit_confirmedSavesDraft`

**Phase 6–9 additions (as built)**
- `TestPending_noExtraReadAfterSuccess`, `TestPending_openingDetailOfHeldIssueStillReleases`,
  `TestPendingSet_holdBlocksNext`
- `TestWrites_orphanedEditCommentDeleteHandled`, `TestCreate_closedFormStillStoresResult`
- `TestSpinner_animatesWhileAChangeIsSending`, `TestClock_*`
- `TestBoard_stepMovesCardOptimistically`, `TestBoard_stepFocusFollowsCard`,
  `TestBoard_statusPickerRemoveFromBoard`, `TestBoard_moreLineIsLastRow`
- `TestPicker_listsIssueValuesTheRepoNoLongerHas`
- `TestReread_*` (8.12), `TestWording_justNowAndNotPlannedToast`,
  `TestIssueList_labelsBorrowUnusedAssigneeRoom`
- `TestGolden`, `TestGolden_ansi`, `TestGolden_tooSmall`, `TestE2E_*`

**fresh mark** (`fresh_test.go`: a wrapper over the fake records
`domain.FreshSince` for every read; every read of one refresh carries the same
time)
- `TestFresh_rOnIssueListMarksEveryPage`
- `TestFresh_eachRCarriesItsKeyPressTime`
- `TestFresh_laterPagesKeepTheKeyPressTimeWhileTheClockMoves` (issue list, PR
  list and comment pages, with the clock moving between pages)
- `TestFresh_rOnPRList`
- `TestFresh_rOnBoard`
- `TestFresh_rOnIssueDetailMarksEveryCommentPage`
- `TestFresh_rOnPRDetail`
- `TestFresh_recoveryAfterAuthErrorIsFresh`
- `TestFresh_rReloadsLabelsAndUsersOnlyWhenLoaded`
- `TestFresh_rReplacesAnAutoRefreshInFlight`
- `TestFresh_notOnStartupOpenAutoRereadPrecheckOrProjectSwitch`
- `TestFresh_notOnProjectSwitch`

**closed view order**
- `TestClosedOrder_newestClosedFirstWithFallbacks`
- `TestClosedOrder_openAndAllKeepGitHubOrder`
- `TestClosedOrder_selectionFollowsIssueAcrossPagesAndRefresh`
- `TestClosedOrder_timeColumnShowsClosedTime`
- `TestClosedOrder_reopenKeepsItsRow`

**theme**
- `TestTheme_overrideSkipsQuery`
- `TestTheme_backgroundMsgSwitchesAndMarkdownStyle`
- `TestTheme_badLabelColourFallsBack`
- `TestTheme_dimmedMapsForegroundsToMuted`

**goldens** (each at 80×24 and 140×40)

- issue list: loaded, loading, empty, no match, failed, truncated, stale banner, pending markers, closed (by closed time)
- issue detail
- PR list, PR list mine
- PR detail
- board: plain, with a moved card, with the label picker over it
- each board setup state
- create form: write and preview
- edit conflict box
- comment form
- assignee picker, label picker, status picker
- quick add: write with marks, preview, preview with problems, progress, stopped banner
- delete confirm
- help
- toast and error toast
- too small (79×24)

---

## 19. Dependencies

The TUI uses these rows of A10, unchanged:

- `charm.land/bubbletea/v2` v2.1.1
- `charm.land/bubbles/v2` v2.2.1
- `charm.land/lipgloss/v2` v2.1.0
- `github.com/charmbracelet/x/ansi` v0.11.9
- `github.com/google/shlex` (used by `cli` for `Env.Editor`)
- `github.com/cli/go-gh/v2` `pkg/browser` (used by `cli` for `Deps.Open`)
- `github.com/charmbracelet/x/exp/golden` v0.1.0
- `github.com/charmbracelet/x/exp/teatest/v2` v2.0.0-20261101151554-27ecf3e4d454

glamour comes in through `markdown`. No other dependency.
