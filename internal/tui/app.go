// Package tui is the Bubble Tea v2 application behind `todos` and `todos tui`.
// The design is components/tui.md; architecture.md wins where they differ.
package tui

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/pkieltyka/todos/internal/domain"
	"github.com/pkieltyka/todos/internal/markdown"
	"github.com/pkieltyka/todos/internal/service"
)

// Deps are the TUI's dependencies, all injected by cli (tui.md 3).
type Deps struct {
	Service  service.Service
	Drafts   DraftStore
	Renderer *markdown.Renderer
	Clock    func() time.Time       // time.Now; fixed in tests
	Open     func(url string) error // go-gh pkg/browser; fake in tests
	Env      Env
	// Tick schedules a timer message; tea.Tick in production. Injected so no
	// test sleeps (architecture 8). nil means tea.Tick.
	Tick func(d time.Duration, fn func(time.Time) tea.Msg) tea.Cmd
	// Exec runs the external editor; tea.ExecProcess in production. The test
	// harness runs the command directly. nil means tea.ExecProcess.
	Exec func(cmd *exec.Cmd, fn tea.ExecCallback) tea.Cmd
	// Go starts the quick-add run goroutine; `go f()` in production. The
	// harness tracks it.
	Go func(f func())
	// Send delivers a message from a goroutine into the program; Run sets it
	// to Program.Send. nil means the run streams through a channel instead.
	Send func(tea.Msg)
}

// Env is what cli read from the environment.
type Env struct {
	Theme  string   // TODOS_THEME as read by cli: "", "light" or "dark"
	Editor []string // shlex.Split($VISUAL, else $EDITOR); nil when both are empty
	// NoColor is the global --no-color flag. NO_COLOR in the environment is
	// honoured by Bubble Tea's own profile detection; the flag reaches the
	// TUI here, without changing the process environment.
	NoColor bool
}

// Options select what the TUI shows.
type Options struct {
	StartView ViewID
	Repo      domain.RepoRef
}

// DraftStore is defined here and implemented by internal/config (A2).
type DraftStore interface {
	Load(repo domain.RepoRef) (domain.Draft, bool, error)
	Save(d domain.Draft) error
	Delete(repo domain.RepoRef) error
}

const (
	autoRefreshEvery = 60 * time.Second
	recentLoad       = 30 * time.Second
	toastFor         = 6 * time.Second
	resizeSettle     = 100 * time.Millisecond
	spinEvery        = 100 * time.Millisecond
	clockEvery       = time.Second
	renderBatch      = 16
)

var spinFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// Toast is the one-line message in the footer (UI 3.14).
type Toast struct {
	ID    uint64
	Text  string
	Error bool
}

// App is the root model.
type App struct {
	deps Deps
	opts Options
	ctx  context.Context

	width, height int
	frame         Frame
	fits          bool

	theme      Theme
	themeFixed bool
	keys       Keymap
	store      *Store
	inflight   map[Slot]*Request
	reqSeq     ReqID

	tabs     [3][]Screen
	active   ViewID
	overlays []Overlay

	toast    *Toast
	toastSeq uint64

	spin, spinGen uint64
	spinning      bool
	autoGen       uint64
	resizeGen     uint64
	frozen        bool

	rendering  map[markdown.Key]bool
	lastRender map[lastKey]lastOut
	renderVer  uint64
	clockOn    bool // a clock tick is scheduled

	pending *PendingSet
	formSeq uint64
	writes  map[string]*formWrite // form writes in flight (create, edit, comment, delete)
	rr      rereads               // delayed re-reads after creates and board changes (reread.go)
	drafts  draftQueue            // quick-add draft saves, one at a time
	runSeq  uint64
	later   []tea.Cmd // commands produced while handling a message, run after it
}

type lastKey struct {
	hash  uint64
	style markdown.Style
}

// lastOut is the most recent render of a source, at whatever width it was
// rendered. It keeps a resize from flashing blank, and it is the TUI's own
// record of what is rendered: the renderer's LRU may evict entries when an
// issue has more comments than it holds, and that must not cause a re-render.
type lastOut struct {
	width int
	out   string
}

// New builds the App.
func New(deps Deps, opts Options) *App {
	if deps.Clock == nil {
		deps.Clock = time.Now
	}
	if deps.Tick == nil {
		deps.Tick = tea.Tick
	}
	if deps.Exec == nil {
		deps.Exec = tea.ExecProcess
	}
	if deps.Go == nil {
		deps.Go = func(f func()) { go f() }
	}
	if deps.Renderer == nil {
		deps.Renderer = markdown.NewRenderer(markdown.Options{})
	}
	a := &App{
		deps:       deps,
		opts:       opts,
		ctx:        context.Background(),
		keys:       DefaultKeymap(),
		store:      NewStore(),
		inflight:   make(map[Slot]*Request),
		pending:    newPendingSet(),
		writes:     map[string]*formWrite{},
		rr:         rereads{due: map[int]uint64{}},
		active:     opts.StartView,
		rendering:  make(map[markdown.Key]bool),
		lastRender: make(map[lastKey]lastOut),
		theme:      NewTheme(true), // dark until the terminal answers (UI 6)
	}
	if dark, ok := ParseOverride(deps.Env.Theme); ok {
		a.theme = NewTheme(dark)
		a.themeFixed = true
	}
	a.theme.plain = deps.Env.NoColor
	a.tabs[ViewIssues] = []Screen{newIssueList()}
	a.tabs[ViewPRs] = []Screen{newPRList()}
	a.tabs[ViewBoard] = []Screen{newBoard()}
	return a
}

// Run runs the TUI until the user quits or ctx is cancelled.
func Run(ctx context.Context, deps Deps, opts Options) error {
	return run(ctx, deps, opts)
}

// run owns a child context that every read descends from, and cancels it on
// every exit, so no Service call outlives the program (finding 9). Tests pass
// program options (input, output) through progOpts.
func run(ctx context.Context, deps Deps, opts Options, progOpts ...tea.ProgramOption) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	a := New(deps, opts)
	a.ctx = ctx
	p := tea.NewProgram(a, append(programOptions(ctx, deps.Env), progOpts...)...)
	if a.deps.Send == nil {
		a.deps.Send = p.Send
	}
	_, err := p.Run()
	return err
}

// programOptions are the Bubble Tea options for an environment. With
// --no-color the ASCII profile drops every colour (bold and other attributes
// stay, so the screen keeps its structure); glyphs carry every meaning (UI 6).
func programOptions(ctx context.Context, env Env) []tea.ProgramOption {
	opts := []tea.ProgramOption{tea.WithContext(ctx)}
	if env.NoColor {
		opts = append(opts, tea.WithColorProfile(colorprofile.ASCII))
	}
	return opts
}

// Init starts the first loads; the first frame never waits for them.
func (a *App) Init() tea.Cmd {
	cmds := []tea.Cmd{a.loadSession(), a.loadBoard(false, ReasonInitial), a.scheduleAuto(), a.clockCmd()}
	if !a.themeFixed {
		cmds = append(cmds, tea.RequestBackgroundColor)
	}
	switch a.active {
	case ViewIssues:
		cmds = append(cmds, a.loadIssues(ReasonInitial))
	case ViewPRs:
		cmds = append(cmds, a.loadPRs(ReasonInitial))
	}
	cmds = append(cmds, a.spinCmd())
	return tea.Batch(cmds...)
}

func (a *App) now() time.Time { return a.deps.Clock() }

// ---- Ctx ----

// Ctx is built for every Update and View: read access to the state plus a
// few navigation helpers (tui.md 4.1).
type Ctx struct {
	a      *App
	Store  *Store
	Theme  Theme
	Keys   Keymap
	Now    time.Time
	Frame  Frame
	Spin   string
	Viewer string
	Repo   domain.RepoRef
}

func (a *App) ctxFor(theme Theme) *Ctx {
	c := &Ctx{
		a: a, Store: a.store, Theme: theme, Keys: a.keys, Now: a.now(), Frame: a.frame,
		Spin: spinFrames[a.spin%uint64(len(spinFrames))], Repo: a.opts.Repo,
	}
	if s := a.store.Meta.Session; s != nil {
		c.Viewer = s.Viewer.Login
		if c.Repo == (domain.RepoRef{}) {
			c.Repo = s.Repo.Ref
		}
	}
	return c
}

// Markdown returns the best render available for src at width: the exact
// one, else the last render of the same source at any width (so a resize
// never flashes blank), else sanitised plain text.
func (c *Ctx) Markdown(src string, width int) string {
	r := c.a.deps.Renderer
	st := c.Theme.MarkdownStyle()
	k := r.Key(src, width, st)
	if out, ok := r.Cached(k); ok {
		return out
	}
	if l, ok := c.a.lastRender[lastKey{k.Hash, st}]; ok {
		return l.out
	}
	return ansi.Wordwrap(markdown.SanitizeBody(src), width, "")
}

// markdownKept is Markdown for a screen's Sync (it runs inside Update). A
// render can reach the renderer's cache before its renderedMsg is handled;
// with more comments than that cache holds (128), later renders can evict it
// in between, and the comment would drop back to plain text until its
// message lands, then render again: a flicker, and a second draw. Keeping a
// cache hit in lastRender, which Update owns, means a comment once drawn
// rendered stays rendered.
func (c *Ctx) markdownKept(src string, width int) string {
	r := c.a.deps.Renderer
	st := c.Theme.MarkdownStyle()
	k := r.Key(src, width, st)
	if out, ok := r.Cached(k); ok {
		c.a.lastRender[lastKey{k.Hash, st}] = lastOut{width: width, out: out}
		return out
	}
	return c.Markdown(src, width)
}

// Push opens a screen on the active tab.
func (c *Ctx) Push(s Screen) { c.a.tabs[c.a.active] = append(c.a.tabs[c.a.active], s) }

// Pop closes the top screen of the active tab and cancels its reads.
func (c *Ctx) Pop() { c.a.pop() }

// syncSig is what a detail screen's content depends on; Sync skips the
// rebuild when it is unchanged.
type syncSig struct {
	store, render, pending uint64
	frame                  Frame
	dark                   bool
	spin                   uint64
}

func (c *Ctx) sig(loading bool) syncSig {
	s := syncSig{store: c.Store.version, render: c.a.renderVer, pending: c.a.pending.version, frame: c.Frame, dark: c.Theme.Dark}
	if loading {
		s.spin = c.a.spin
	}
	return s
}

// Issue is the issue to draw: the base with every pending change replayed
// (A5). Screens never draw the bare base.
func (c *Ctx) Issue(n int) (domain.Issue, bool) {
	base, ok := c.Store.Issue(n)
	if !ok {
		return base, false
	}
	eff, _ := Effective(base, c.a.pending.Changes(n), c.Store.Meta.Labels)
	return eff, true
}

// Marks are the pending markers of an issue.
func (c *Ctx) Marks(n int) Marks {
	base, _ := c.Store.Issue(n)
	_, m := Effective(base, c.a.pending.Changes(n), c.Store.Meta.Labels)
	return m
}

// Columns is the derived board (A5): effective issues, plus every issue with
// a pending change or placed this session.
func (c *Ctx) Columns() []DisplayColumn {
	extra := c.a.pending.Numbers()
	for n := range c.Store.Board.Extra {
		extra = append(extra, n)
	}
	return c.Store.Columns(c.Issue, extra)
}

// Busy reports whether a slot has a live request.
func (c *Ctx) Busy(s Slot) bool { return c.a.busy(s) }

func (a *App) pop() {
	st := a.tabs[a.active]
	if len(st) <= 1 {
		return
	}
	for _, s := range st[len(st)-1].Slots() {
		// A held queue (A5 rule 5) waits for a read of its issue: whatever
		// read is in that slot (the re-read, or the detail's own read that
		// replaced it) must not be cancelled (tui.md 6.3, Phase 6 review).
		if s.Kind == SlotIssue && a.isHeld(s.Number) {
			continue
		}
		a.cancel(s)
	}
	a.tabs[a.active] = st[:len(st)-1]
}

func (a *App) top(v ViewID) Screen {
	st := a.tabs[v]
	if len(st) == 0 {
		return nil
	}
	return st[len(st)-1]
}

// ---- Update ----

// Update handles every message (tui.md 5).
func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	cmd := a.update(msg)
	if len(a.later) > 0 {
		cmd = tea.Batch(append([]tea.Cmd{cmd}, a.later...)...)
		a.later = nil
	}
	c := a.ctxFor(a.theme)
	for v := range a.tabs {
		if s := a.top(ViewID(v)); s != nil {
			s.Sync(c)
		}
	}
	if a.fits {
		for _, o := range a.overlays {
			if l, ok := o.(overlayLayout); ok {
				l.layout(c)
			}
		}
	}
	return a, tea.Batch(cmd, a.requestRenders(), a.spinCmd(), a.flushRereads())
}

func (a *App) update(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		first := a.width == 0
		a.width, a.height = m.Width, m.Height
		a.frame, a.fits = ComputeFrame(m.Width, m.Height)
		var clock tea.Cmd
		if a.fits && !a.clockOn {
			clock = a.clockCmd() // re-arm after a too-small spell
		}
		if first {
			return clock
		}
		a.frozen = true
		a.resizeGen++
		gen := a.resizeGen
		return tea.Batch(clock, a.deps.Tick(resizeSettle, func(time.Time) tea.Msg { return resizeSettledMsg{gen} }))
	case resizeSettledMsg:
		if m.gen == a.resizeGen {
			a.frozen = false
		}
		return nil
	case tea.BackgroundColorMsg:
		if !a.themeFixed {
			a.theme = NewTheme(m.IsDark())
			a.theme.plain = a.deps.Env.NoColor
		}
		return nil
	case tea.KeyPressMsg:
		return a.onKey(m)
	case tea.PasteMsg:
		if t := a.target(); t != nil && a.targetMode() == ModeText {
			return a.forward(msg)
		}
		return nil
	case spinnerTickMsg:
		if m.gen != a.spinGen {
			return nil
		}
		a.spinning = false
		a.spin++
		return nil
	case autoRefreshMsg:
		return a.onAuto(m)
	case clockTickMsg:
		a.clockOn = false
		if !a.fits {
			return nil // nothing to draw: the chain stops, a fitting resize re-arms it
		}
		return a.clockCmd() // the redraw after this Update is the point

	case toastExpireMsg:
		if a.toast != nil && a.toast.ID == m.id {
			a.toast = nil
		}
		return nil
	case renderedMsg:
		if len(a.lastRender) > 4096 {
			a.lastRender = make(map[lastKey]lastOut)
		}
		a.renderVer++
		for _, it := range m.items {
			delete(a.rendering, it.key)
			out := it.out
			if it.err != nil {
				// A failed render shows sanitised plain text and is not retried.
				out = ansi.Wordwrap(markdown.SanitizeBody(it.src), it.key.Width, "")
			}
			a.lastRender[lastKey{it.key.Hash, it.key.Style}] = lastOut{width: it.key.Width, out: out}
		}
		return nil
	case openedMsg:
		if m.err != nil {
			return a.toastErr("Couldn't open the browser: " + m.err.Error())
		}
		return nil
	case sessionMsg:
		return a.onSession(m)
	case issuesPageMsg:
		return a.onIssuesPage(m)
	case prsPageMsg:
		return a.onPRsPage(m)
	case recoveredMsg:
		return a.onRecovered(m)
	case changeDoneMsg:
		return a.onChangeDone(m)
	case rereadMsg:
		return a.onReread(m)
	case labelsMsg, usersMsg:
		a.onMeta(msg)
		return nil
	case createDoneMsg, editDoneMsg, commentDoneMsg, deleteDoneMsg:
		return a.onWriteResult(msg)
	case formReadMsg, conflictChoiceMsg, editorDoneMsg, editorReadMsg,
		draftLoadedMsg, draftTickMsg, planMsg, qaProgressMsg, qaDoneMsg:
		return a.toOverlays(msg)
	case draftSavedMsg:
		return a.onDraftSavedMsg(m)
	case boardMsg:
		return a.onBoard(m)
	case candidatesMsg:
		return a.onCandidates(m)
	case boardChosenMsg:
		return a.onBoardChosen(m)
	case issueDetailMsg:
		return a.onIssueDetail(m)
	case commentsPageMsg:
		return a.onCommentsPage(m)
	case prDetailMsg:
		return a.onPRDetail(m)
	}
	return nil
}

// target is the top overlay, else the active tab's top screen.
func (a *App) target() any {
	if n := len(a.overlays); n > 0 {
		return a.overlays[n-1]
	}
	return a.top(a.active)
}

func (a *App) targetMode() InputMode {
	switch t := a.target().(type) {
	case Overlay:
		return t.Mode()
	case Screen:
		return t.Mode()
	}
	return ModeNav
}

func (a *App) targetActions(c *Ctx) []Action {
	switch t := a.target().(type) {
	case Overlay:
		return t.Actions(c)
	case Screen:
		return t.Actions(c)
	}
	return nil
}

func (a *App) forward(msg tea.Msg) tea.Cmd {
	c := a.ctxFor(a.theme)
	switch t := a.target().(type) {
	case Overlay:
		cmd, res := t.Update(c, msg)
		if res == Close {
			a.closeOverlay(t)
		}
		return cmd
	case Screen:
		return t.Update(c, msg)
	}
	return nil
}

// onKey is the key dispatch of tui.md 9.2.
func (a *App) onKey(k tea.KeyPressMsg) tea.Cmd {
	// A key dismisses the toast; an error toast stays until the next key.
	a.toast = nil
	if k.String() == "ctrl+c" {
		return a.quit(true)
	}
	if !a.fits {
		if key.Matches(k, a.keys.Binding(ActQuit)) {
			a.cancelRereads()
			return tea.Quit
		}
		return nil
	}
	if a.targetMode() == ModeText {
		return a.forward(k)
	}
	c := a.ctxFor(a.theme)
	for _, act := range a.targetActions(c) {
		if key.Matches(k, a.keys.Binding(act)) {
			return a.forward(ActionMsg{act})
		}
	}
	if len(a.overlays) > 0 {
		return a.forward(k) // keys an overlay has no action for, e.g. a letter that starts a picker filter
	}
	for _, act := range []Action{ActViewIssues, ActViewPRs, ActViewBoard, ActNextView, ActPrevView, ActHelp, ActQuit, ActRefresh} {
		if key.Matches(k, a.keys.Binding(act)) {
			return a.global(act)
		}
	}
	return nil
}

func (a *App) global(act Action) tea.Cmd {
	switch act {
	case ActViewIssues:
		return a.switchTo(ViewIssues)
	case ActViewPRs:
		return a.switchTo(ViewPRs)
	case ActViewBoard:
		return a.switchTo(ViewBoard)
	case ActNextView:
		return a.switchTo((a.active + 1) % 3)
	case ActPrevView:
		return a.switchTo((a.active + 2) % 3)
	case ActHelp:
		a.overlays = append(a.overlays, newHelp(a, a.top(a.active)))
		return nil
	case ActQuit:
		return a.quit(false)
	case ActRefresh:
		return a.refresh(ReasonRefresh)
	}
	return nil
}

// quit asks first while writes are in flight (tui.md 8.8); a second ctrl+c
// on that question quits at once.
func (a *App) quit(ctrlC bool) tea.Cmd {
	if n := len(a.overlays); n > 0 {
		if c, ok := a.overlays[n-1].(*Confirm); ok && c.quit && ctrlC {
			a.cancelRereads()
			return tea.Quit
		}
	}
	n := a.pendingWrites()
	if n == 0 {
		a.flushDraftNow()
		a.cancelRereads()
		return tea.Quit
	}
	what := itoaPlain(n) + " changes"
	if n == 1 {
		what = "1 change"
	}
	q := newConfirm(what+" still sending. Quit anyway?", func(*Ctx) tea.Cmd { a.flushDraftNow(); a.cancelRereads(); return tea.Quit })
	q.quit = true
	a.pushOverlay(q)
	return nil
}

// pushOverlay opens an overlay on top.
func (a *App) pushOverlay(o Overlay) { a.overlays = append(a.overlays, o) }

// closeOverlay closes o and everything above it.
func (a *App) closeOverlay(o Overlay) tea.Cmd {
	for i, x := range a.overlays {
		if x == o {
			a.overlays = a.overlays[:i]
			break
		}
	}
	return nil
}

// toOverlays gives a write result to every overlay; each ignores results
// that are not its own.
func (a *App) toOverlays(msg tea.Msg) tea.Cmd {
	c := a.ctxFor(a.theme)
	var cmds []tea.Cmd
	for _, o := range append([]Overlay{}, a.overlays...) {
		cmd, res := o.Update(c, msg)
		cmds = append(cmds, cmd)
		if res == Close {
			a.closeOverlay(o)
		}
	}
	return tea.Batch(cmds...)
}

// switchTo changes tab; a tab never loaded, or with data older than the
// auto-refresh interval, loads now (tui.md 7.7).
func (a *App) switchTo(v ViewID) tea.Cmd {
	a.active = v
	switch v {
	case ViewIssues:
		if a.store.Issues.Phase == PhaseEmpty && !a.busy(Slot{Kind: SlotIssues}) {
			return a.loadIssues(ReasonInitial)
		}
	case ViewPRs:
		if a.store.PRs.Phase == PhaseEmpty && !a.busy(Slot{Kind: SlotPRs}) {
			return a.loadPRs(ReasonInitial)
		}
	}
	if a.age(v) > autoRefreshEvery {
		return a.refresh(ReasonAuto)
	}
	return nil
}

// neverLoaded is the age of a view that has no successful load yet: older
// than any interval, so the next auto tick or tab switch retries it
// (finding 7). A view still loading is protected by its busy slot.
const neverLoaded = time.Duration(1<<63 - 1)

// age is how old the visible data of a tab is.
func (a *App) age(v ViewID) time.Duration {
	var at time.Time
	switch s := a.top(v).(type) {
	case *IssueList:
		at = a.store.Issues.LoadedAt
	case *PRList:
		at = a.store.PRs.LoadedAt
	case *Board:
		at = a.store.Board.LoadedAt
	case *IssueDetail:
		if d, ok := a.store.PeekDetail(s.number); ok {
			at = d.LoadedAt
		}
	case *PRDetail:
		at = a.store.PRDetail(s.number).LoadedAt
	default:
		return 0
	}
	if at.IsZero() {
		return neverLoaded
	}
	return a.now().Sub(at)
}

// visibleErr is the error of the active tab's visible view.
func (a *App) visibleErr() *domain.Error {
	switch s := a.top(a.active).(type) {
	case *IssueList:
		return a.store.Issues.Err
	case *PRList:
		return a.store.PRs.Err
	case *Board:
		if a.store.Board.Err != nil {
			return a.store.Board.Err
		}
		return a.store.Board.InfoErr
	case *BoardSetup:
		if a.store.Board.InfoErr != nil {
			return a.store.Board.InfoErr
		}
		return s.err
	case *IssueDetail:
		if d, ok := a.store.PeekDetail(s.number); ok {
			if d.BodyErr != nil {
				return d.BodyErr
			}
			return d.CommentsErr
		}
	case *PRDetail:
		d := a.store.PRDetail(s.number)
		if d.DetailErr != nil {
			return d.DetailErr
		}
		return d.CommentsErr
	}
	return nil
}

// refresh reloads the visible view. A manual retry after an auth or setup
// error first refreshes metadata and the session, because the account may
// have changed (finding 2, @cto's ruling).
func (a *App) refresh(reason LoadReason) tea.Cmd {
	if reason == ReasonRefresh && (needsRecovery(a.visibleErr()) || a.top(a.active).ID() == ScreenBoardSetup) {
		if a.busy(Slot{Kind: SlotRecover}) {
			return nil
		}
		return a.recover()
	}
	return a.reload(reason)
}

// reload restarts the visible read (tui.md 7.6). A detail counts as busy
// while its body or its comments are loading (finding 8).
func (a *App) reload(reason LoadReason) tea.Cmd {
	switch s := a.top(a.active).(type) {
	case *IssueList:
		if a.busy(Slot{Kind: SlotIssues}) {
			return nil
		}
		return a.loadIssues(reason)
	case *PRList:
		if a.busy(Slot{Kind: SlotPRs}) {
			return nil
		}
		return a.loadPRs(reason)
	case *Board, *BoardSetup:
		if a.busy(Slot{Kind: SlotBoard}) {
			return nil
		}
		return a.loadBoard(false, reason)
	case *IssueDetail:
		if a.busy(Slot{Kind: SlotIssue, Number: s.number}) || a.busy(Slot{Kind: SlotIssueComments, Number: s.number}) {
			return nil
		}
		return a.loadIssue(s.number, reason)
	case *PRDetail:
		if a.busy(Slot{Kind: SlotPR, Number: s.number}) || a.busy(Slot{Kind: SlotPRComments, Number: s.number}) {
			return nil
		}
		return a.loadPR(s.number, reason)
	}
	return nil
}

// ---- auto-refresh (tui.md 7.7) ----

func (a *App) scheduleAuto() tea.Cmd {
	a.autoGen++
	gen := a.autoGen
	return a.deps.Tick(autoRefreshEvery, func(time.Time) tea.Msg { return autoRefreshMsg{gen} })
}

func (a *App) onAuto(m autoRefreshMsg) tea.Cmd {
	if m.gen != a.autoGen {
		return nil
	}
	next := tea.Batch(a.scheduleAuto(), a.rereadHeld()) // held queues retry even when the rest is skipped
	if a.autoSkip() {
		return next
	}
	return tea.Batch(next, a.refresh(ReasonAuto))
}

// autoSkip reports the skip rules of tui.md 7.7, step 3.
func (a *App) autoSkip() bool {
	if len(a.overlays) > 0 || !a.fits || a.rateLow() {
		return true
	}
	if a.age(a.active) < recentLoad {
		return true
	}
	return false
}

// rateLow reports less than 10% of the rate limit left (10).
func (a *App) rateLow() bool {
	rl := a.deps.Service.RateLimit()
	return rl.Known && rl.Limit > 0 && rl.Remaining*10 < rl.Limit
}

// ---- toasts ----

func (a *App) toastOK(text string) tea.Cmd {
	a.toastSeq++
	id := a.toastSeq
	a.toast = &Toast{ID: id, Text: "✓ " + text}
	return a.deps.Tick(toastFor, func(time.Time) tea.Msg { return toastExpireMsg{id} })
}

// toastErr shows an error toast; it stays until the next key (UI 9 #8).
func (a *App) toastErr(text string) tea.Cmd {
	a.toastSeq++
	a.toast = &Toast{ID: a.toastSeq, Text: "✗ " + text, Error: true}
	return nil
}

// toastInfo shows a neutral message that times out like a confirmation.
func (a *App) toastInfo(text string) tea.Cmd {
	a.toastSeq++
	id := a.toastSeq
	a.toast = &Toast{ID: id, Text: text}
	return a.deps.Tick(toastFor, func(time.Time) tea.Msg { return toastExpireMsg{id} })
}

// clockCmd schedules the next clock tick.
func (a *App) clockCmd() tea.Cmd {
	a.clockOn = true
	return a.deps.Tick(clockEvery, func(time.Time) tea.Msg { return clockTickMsg{} })
}

// ---- spinner ----

// spinCmd keeps one tick chain alive while anything is loading or a change
// is being sent: both draw the spinner (UI 5.2).
func (a *App) spinCmd() tea.Cmd {
	if a.spinning || !a.spinNeeded() {
		return nil
	}
	a.spinning = true
	a.spinGen++
	gen := a.spinGen
	return a.deps.Tick(spinEvery, func(time.Time) tea.Msg { return spinnerTickMsg{gen} })
}

// spinNeeded reports whether anything on screen may show the spinner: a
// read in flight, or a change, form write or quick-add run still sending. An
// uncertain change shows "?" and needs no ticks.
func (a *App) spinNeeded() bool {
	if len(a.inflight) > 0 || len(a.writes) > 0 {
		return true
	}
	for _, n := range a.pending.Numbers() {
		for _, c := range a.pending.Changes(n) {
			if c.Phase != Uncertain {
				return true
			}
		}
	}
	for _, o := range a.overlays {
		if q, ok := o.(*QuickAdd); ok && q.Busy() {
			return true
		}
	}
	return false
}

// ---- rendering ----

// requestRenders starts the Markdown renders the visible screen needs and
// that are neither cached nor already running. While a resize settles, the
// old renders stay on screen (tui.md 16).
func (a *App) requestRenders() tea.Cmd {
	s := a.top(a.active)
	if s == nil || a.frozen || !a.fits {
		return nil
	}
	c := a.ctxFor(a.theme)
	r := a.deps.Renderer
	st := a.theme.MarkdownStyle()
	type job struct {
		k   markdown.Key
		src string
		w   int
	}
	var jobs []job
	reqs := s.Renders(c)
	if n := len(a.overlays); n > 0 {
		if r, ok := a.overlays[n-1].(overlayRenders); ok {
			reqs = append(reqs, r.Renders(c)...)
		}
	}
	for _, req := range reqs {
		k := r.Key(req.Src, req.Width, st)
		if a.rendering[k] {
			continue
		}
		if l, ok := a.lastRender[lastKey{k.Hash, st}]; ok && l.width == req.Width {
			continue
		}
		a.rendering[k] = true
		jobs = append(jobs, job{k, req.Src, req.Width})
	}
	// Renders go out in batches, so an issue with hundreds of comments costs
	// a few dozen updates rather than one per comment.
	var cmds []tea.Cmd
	for len(jobs) > 0 {
		n := min(renderBatch, len(jobs))
		batch := jobs[:n]
		jobs = jobs[n:]
		cmds = append(cmds, func() tea.Msg {
			out := make([]rendered, len(batch))
			for i, j := range batch {
				s, err := r.Render(j.src, j.w, st)
				out[i] = rendered{key: j.k, src: j.src, out: s, err: err}
			}
			return renderedMsg{items: out}
		})
	}
	return tea.Batch(cmds...)
}

// ---- View ----

// View draws the frame (tui.md 10.6).
func (a *App) View() tea.View {
	v := tea.NewView(a.render())
	v.AltScreen = true
	v.WindowTitle = a.title()
	v.ProgressBar = a.progressBar()
	if a.fits {
		c := a.ctxFor(a.theme)
		if n := len(a.overlays); n > 0 {
			if o := a.overlays[n-1]; o.Mode() == ModeText {
				v.Cursor = o.Cursor(c)
			}
		} else if s := a.top(a.active); s != nil && s.Mode() == ModeText {
			v.Cursor = s.Cursor(c)
		}
	}
	return v
}

func (a *App) title() string {
	if s := a.top(a.active); s != nil {
		if t := s.Title(a.ctxFor(a.theme)); t != "" {
			return "todos · " + t
		}
	}
	return "todos"
}

func (a *App) render() string {
	if a.width == 0 {
		return ""
	}
	if !a.fits {
		return a.tooSmall()
	}
	theme := a.theme
	if len(a.overlays) > 0 {
		theme = a.theme.Dimmed()
	}
	c := a.ctxFor(theme)
	f := a.frame
	s := a.top(a.active)
	body := s.View(c, f.Body)
	base := strings.Join([]string{a.header(c), fit(s.SubBar(c, f.SubBar.W), f.SubBar.W), body, a.footer(c, s)}, "\n")
	if len(a.overlays) == 0 {
		return base
	}
	oc := a.ctxFor(a.theme)
	layers := []*lipgloss.Layer{lipgloss.NewLayer(base)}
	for i, o := range a.overlays {
		content, b := o.View(oc, f.Body)
		layers = append(layers, lipgloss.NewLayer(content).X(b.X).Y(b.Y).Z(i+1))
	}
	return lipgloss.NewCompositor(layers...).Render()
}

func (a *App) tooSmall() string {
	lines := []string{
		"todos needs at least 80×24.",
		fmt.Sprintf("This terminal is %d×%d.", a.width, a.height),
		"Resize the window, or q to quit.",
	}
	top := max(0, (a.height-len(lines))/2)
	out := make([]string, a.height)
	for i := range out {
		if j := i - top; j >= 0 && j < len(lines) {
			out[i] = center(lines[j], a.width)
		} else {
			out[i] = strings.Repeat(" ", a.width)
		}
	}
	return strings.Join(out, "\n")
}

func (a *App) header(c *Ctx) string {
	t := c.Theme
	w := a.frame.Header.W
	repo := c.Repo.Owner + "/" + c.Repo.Name
	names := [3]string{"1 Issues", "2 PRs", "3 Board"}
	gap := "  "
	if w >= 120 {
		names[1] = "2 Pull requests"
		gap = "   "
	}
	var tabs []string
	for i, n := range names {
		if ViewID(i) == a.active {
			tabs = append(tabs, t.accent().Bold(true).Render(n))
		} else {
			tabs = append(tabs, t.muted().Render(n))
		}
	}
	left := " " + t.bold().Render("todos") + "  " + t.text().Render(line(repo)) + "    " + strings.Join(tabs, gap)
	return spread(left, a.syncState(c)+" ", w)
}

// syncState is the header's right side (UI 5.3).
func (a *App) syncState(c *Ctx) string {
	t := c.Theme
	var (
		slots  []Slot
		loaded time.Time
		err    *domain.Error
		has    bool
	)
	switch s := a.top(a.active).(type) {
	case *IssueList:
		l := a.store.Issues
		slots, loaded, err, has = []Slot{{Kind: SlotIssues}}, l.LoadedAt, l.Err, len(l.Numbers) > 0
	case *PRList:
		l := a.store.PRs
		slots, loaded, err, has = []Slot{{Kind: SlotPRs}}, l.LoadedAt, l.Err, len(l.Numbers) > 0
	case *Board:
		b := a.store.Board
		slots, loaded, err, has = []Slot{{Kind: SlotBoard}}, b.LoadedAt, b.Err, b.Loaded
	case *BoardSetup:
		slots, err = []Slot{{Kind: SlotBoard}, {Kind: SlotRecover}}, a.store.Board.InfoErr
	case *IssueDetail:
		slots = []Slot{{Kind: SlotIssue, Number: s.number}, {Kind: SlotIssueComments, Number: s.number}}
		if d, ok := a.store.PeekDetail(s.number); ok {
			loaded, has = d.LoadedAt, d.Loaded
			err = d.BodyErr
			if err == nil {
				err = d.CommentsErr
			}
		}
	case *PRDetail:
		slots = []Slot{{Kind: SlotPR, Number: s.number}, {Kind: SlotPRComments, Number: s.number}}
		d := a.store.PRDetail(s.number)
		loaded, has = d.LoadedAt, d.Detail != nil
		err = d.DetailErr
		if err == nil {
			err = d.CommentsErr
		}
	}
	for _, sl := range slots {
		if a.busy(sl) {
			return t.muted().Render(c.Spin + " syncing…")
		}
	}
	if err != nil {
		if has {
			return t.pending().Render("⚠ stale · " + agoPhrase(c.Now, loaded))
		}
		if _, ok := a.top(a.active).(*BoardSetup); ok {
			return ""
		}
		return t.errorS().Render("✗ offline")
	}
	if a.rateLow() {
		rl := a.deps.Service.RateLimit()
		return t.pending().Render(fmt.Sprintf("⏸ auto-refresh paused · %d%% left", rl.Remaining*100/rl.Limit))
	}
	if loaded.IsZero() {
		return ""
	}
	return t.fg(t.Open).Render("●") + t.muted().Render(" synced "+agoPhrase(c.Now, loaded))
}

func (a *App) footer(c *Ctx, s Screen) string {
	t := c.Theme
	w := a.frame.Footer.W
	pos := s.Position(c)
	if n := len(a.overlays); n > 0 {
		pos = a.overlays[n-1].Position(c)
	}
	if a.toast != nil {
		st := t.text()
		if a.toast.Error {
			st = t.errorS()
		}
		return spread(" "+st.Render(a.toast.Text), t.muted().Render(pos)+" ", w)
	}
	return spread(a.hints(c, s, w-lipgloss.Width(pos)-3), t.muted().Render(pos)+" ", w)
}

// hints renders as many footer key hints as fit (tui.md 9.3).
func (a *App) hints(c *Ctx, s Screen, w int) string {
	t := c.Theme
	if len(a.overlays) == 0 && s.Mode() == ModeText {
		// Typing in the filter: letters are text, so navigation keys would
		// be wrong here (dogfood, Phase 4).
		return " " + t.accent().Render("enter") + " " + t.muted().Render("keep filter") + "  " +
			t.accent().Render("esc") + " " + t.muted().Render("clear filter")
	}
	if n := len(a.overlays); n > 0 {
		out := " "
		for _, h := range a.overlays[n-1].Hints(c) {
			item := t.accent().Render(h.key) + " " + t.muted().Render(h.desc)
			if lipgloss.Width(out+item) > w {
				break
			}
			out += item + "  "
		}
		return strings.TrimRight(out, " ")
	}
	acts := s.Actions(c)
	ctxID := s.ID()
	help := ""
	if len(a.overlays) == 0 {
		help = t.accent().Render("?") + " " + t.muted().Render("help")
		w -= lipgloss.Width(help) + 2
	}
	out := " "
	used := 1
	seen := map[string]bool{}
	for _, act := range acts {
		if footerHidden[act] || (ctxID == ScreenHelp && act == ActHelp) {
			continue
		}
		k, d := HelpText(ctxID, act, c.Keys)
		if s, ok := footerShort[act]; ok {
			if _, over := helpOverrides[ctxID][act]; !over {
				d = s
			}
		}
		if p, ok := footerPairs[act]; ok {
			k, d = p[0], p[1]
			if o, ok := helpOverrides[ctxID][act]; ok {
				k, d = o[0], o[1]
			}
		}
		if seen[k] {
			continue
		}
		seen[k] = true
		item := t.accent().Render(k) + " " + t.muted().Render(d)
		iw := lipgloss.Width(item) + 2
		if used+iw > w {
			break
		}
		out += item + "  "
		used += iw
	}
	return strings.TrimRight(out+help, " ")
}
