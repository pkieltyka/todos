package tui

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/pkieltyka/todos/internal/domain"
)

// QuickAdd is the punch-list overlay (functional 4.7, 5.5, UI 3.9–3.11,
// tui.md 12): write, preview, run; a stop returns to write holding only the
// items not created.

type qaStep int

const (
	qaWrite qaStep = iota
	qaPreview
	qaRunning
)

type draftLoadedMsg struct {
	d   domain.Draft
	ok  bool
	err error
}

type draftTickMsg struct{ gen uint64 }

type planMsg struct {
	seq  uint64
	plan domain.QuickAddPlan
	err  error
}

// qaProgressMsg and qaDoneMsg stream from the run goroutine.
type qaProgressMsg struct {
	runID uint64
	p     domain.QuickAddProgress
}

type qaDoneMsg struct {
	runID uint64
	res   domain.QuickAddResult
	err   error
	d     domain.Draft
}

// mayExist is an uncertain item kept in the text: it is flagged until its
// exact source is edited (UI 3.11).
type mayExist struct {
	source string
	title  string
}

// qaRow is one row of the progress list.
type qaRow struct {
	phase   string // "", creating, closing, placing, settled
	outcome domain.QuickAddOutcome
	number  int
	err     *domain.Error
}

// QuickAdd is the overlay.
type QuickAdd struct {
	id        uint64
	repo      domain.RepoRef
	step      qaStep
	editor    Editor
	fromBoard bool
	opts      domain.QuickAddOptions

	loaded  bool
	saveOff bool // the saved draft could not be read: never overwrite it
	loadErr *domain.Error
	gen     uint64
	rev     uint64
	marks   []mayExist
	banner  []string
	saveErr bool

	plan     *domain.QuickAddPlan
	planErr  *domain.Error
	planSeq  uint64
	planning bool
	offset   int

	starting bool // waiting for the pre-run flush
	runID    uint64
	stop     *atomic.Bool
	events   chan tea.Msg
	rows     []qaRow
	stopping bool
	// loadWaiting: the draft load waits for the App's pending saves, so a
	// reopen never reads the file before the close-save lands.
	loadWaiting bool
}

// openQuickAdd opens quick add. From the Board view the status starts as the
// focused column and the issues are added to the board (A4, 5.5).
func (a *App) openQuickAdd(fromBoard bool, status *string) tea.Cmd {
	id := a.nextFormID()
	q := &QuickAdd{id: id, repo: a.repo(), fromBoard: fromBoard,
		editor: newEditor(id, a.deps.Env.Editor, "- one issue per line"),
		opts:   domain.QuickAddOptions{Checked: "skip"}} // done items: skip, every time it opens
	if fromBoard {
		q.opts.Status, q.opts.AddToBoard = status, true
	}
	a.pushOverlay(q)
	if a.deps.Drafts == nil {
		q.loaded = true
		return a.loadMeta()
	}
	if a.drafts.saving {
		q.loadWaiting = true // loaded when the queue drains (onDraftSavedMsg)
		return a.loadMeta()
	}
	return tea.Batch(a.loadMeta(), a.loadDraft(q.repo))
}

func (a *App) loadDraft(repo domain.RepoRef) tea.Cmd {
	store := a.deps.Drafts
	return func() tea.Msg {
		d, ok, err := store.Load(repo)
		return draftLoadedMsg{d: d, ok: ok, err: err}
	}
}

func (a *App) repo() domain.RepoRef {
	if r := a.opts.Repo; r != (domain.RepoRef{}) {
		return r
	}
	if s := a.store.Meta.Session; s != nil {
		return s.Repo.Ref
	}
	return domain.RepoRef{}
}

func (q *QuickAdd) Busy() bool { return q.step == qaRunning || q.starting }
func (q *QuickAdd) Mode() InputMode {
	if q.step == qaWrite {
		return ModeText
	}
	return ModeNav
}

func (q *QuickAdd) Actions(*Ctx) []Action {
	switch q.step {
	case qaPreview:
		return []Action{ActAssign, ActLabel, ActStatus, ActDoneItems, ActDown, ActUp, ActApply, ActBack}
	case qaRunning:
		return []Action{ActBack}
	}
	return nil
}

func (q *QuickAdd) Hints(c *Ctx) []hint {
	switch q.step {
	case qaPreview:
		if q.blocked() {
			return []hint{{"esc", "back to text to fix"}}
		}
		return []hint{{"enter", "create " + itoa(q.plan.ToCreate) + plural(q.plan.ToCreate, " issues")},
			{"a/L/M", "set for all"}, {"d", "done items"}, {"esc", "back to text"}}
	case qaRunning:
		if q.stopping {
			return []hint{{"", "stopping after this item…"}}
		}
		return []hint{{"esc", "stop after this"}}
	}
	hs := []hint{{c.Keys.Binding(ActSubmit).Help().Key, "preview"}}
	if c.a.deps.Env.Editor != nil {
		hs = append(hs, hint{"ctrl+o", "$EDITOR"})
	}
	return append(hs, hint{"ctrl+x", "discard"}, hint{"esc", "close (keeps draft)"})
}

func (q *QuickAdd) Position(*Ctx) string {
	if q.step != qaWrite {
		return ""
	}
	l, col := q.editor.LineCol()
	return "L" + itoaPlain(l) + ":C" + itoaPlain(col)
}

func (q *QuickAdd) Renders(*Ctx) []renderReq { return nil }

// blocked reports problems or a planning error: creation is refused.
func (q *QuickAdd) blocked() bool {
	return q.plan == nil || len(q.plan.Problems) > 0 || q.planErr != nil
}

// draft is the current text as a draft for the store.
func (q *QuickAdd) draft() *domain.Draft {
	q.rev++
	d := &domain.Draft{Version: 1, RepoKey: repoKey(q.repo), Text: q.editor.Value(), Revision: q.rev}
	text := q.editor.Value()
	for _, m := range q.marks {
		if i := strings.Index(text, m.source); i >= 0 {
			d.Uncertain = append(d.Uncertain, domain.DraftSpan{StartByte: i, EndByte: i + len(m.source), SourceSHA256: sourceHash(m.source)})
		}
	}
	return d
}

// save queues the draft, or deletes it when the text is empty (5.5).
func (q *QuickAdd) save(a *App) tea.Cmd {
	if q.saveOff || !q.loaded {
		return nil
	}
	if strings.TrimSpace(q.editor.Value()) == "" {
		return a.saveDraft(draftJob{repo: q.repo})
	}
	return a.saveDraft(draftJob{repo: q.repo, d: q.draft()})
}

// onEdited schedules the save 500 ms after the last keystroke and drops
// "may exist" flags whose exact source was edited (UI 3.11).
func (q *QuickAdd) onEdited(a *App) tea.Cmd {
	text := q.editor.Value()
	kept := q.marks[:0]
	for _, m := range q.marks {
		if strings.Contains(text, m.source) {
			kept = append(kept, m)
		}
	}
	q.marks = kept
	q.gen++
	gen := q.gen
	return a.deps.Tick(draftDelay, func(time.Time) tea.Msg { return draftTickMsg{gen} })
}

const draftDelay = 500 * time.Millisecond

func (q *QuickAdd) Update(c *Ctx, msg tea.Msg) (tea.Cmd, OverlayResult) {
	a := c.a
	switch m := msg.(type) {
	case draftLoadedMsg:
		q.loaded = true
		if m.err != nil {
			q.saveOff = true // never overwrite a file we could not read
			de := toDomainError(m.err)
			q.loadErr = de
			return nil, Keep
		}
		if m.ok {
			q.editor.SetValue(m.d.Text)
			q.rev = m.d.Revision
			spans := m.d.Uncertain
			if m.d.InFlight != nil { // a crash while sending: it may exist
				spans = append(spans, *m.d.InFlight)
			}
			for _, s := range spans {
				if s.StartByte < 0 || s.EndByte > len(m.d.Text) || s.StartByte > s.EndByte {
					continue
				}
				src := m.d.Text[s.StartByte:s.EndByte]
				if sourceHash(src) != s.SourceSHA256 {
					continue // the text changed since: the flag no longer applies
				}
				if !slices.ContainsFunc(q.marks, func(m mayExist) bool { return m.source == src }) {
					q.marks = append(q.marks, mayExist{source: src, title: firstLine(src)})
				}
			}
		}
		return nil, Keep
	case draftTickMsg:
		if m.gen == q.gen {
			return q.save(a), Keep
		}
	case planMsg:
		if m.seq != q.planSeq {
			return nil, Keep // a stale plan: the options changed meanwhile
		}
		q.planning = false
		if m.err != nil {
			q.planErr = toDomainError(m.err)
			q.plan = nil
			return nil, Keep
		}
		p := m.plan
		q.plan, q.planErr = &p, nil
	case qaProgressMsg:
		if m.runID != q.runID {
			return nil, Keep
		}
		q.onProgress(a, m.p)
		if a.deps.Send == nil {
			return waitEvent(q.events), Keep
		}
		return nil, Keep
	case qaDoneMsg:
		if m.runID != q.runID {
			return nil, Keep
		}
		return q.onDone(a, m)
	case editorDoneMsg, editorReadMsg:
		cmd, _ := q.editor.Update(c, msg)
		return tea.Batch(cmd, q.onEdited(a)), Keep
	case tea.PasteMsg:
		if q.step == qaWrite && q.loaded {
			cmd, _ := q.editor.Update(c, m)
			return tea.Batch(cmd, q.onEdited(a)), Keep
		}
	case tea.KeyPressMsg:
		if q.step == qaWrite {
			return q.onWriteKey(c, m)
		}
	case ActionMsg:
		switch q.step {
		case qaPreview:
			return q.onPreviewAction(c, m.A)
		case qaRunning:
			if m.A == ActBack && !q.stopping {
				q.stopping = true
				q.stop.Store(true) // the current item finishes first
			}
		}
	}
	return nil, Keep
}

func (q *QuickAdd) onWriteKey(c *Ctx, k tea.KeyPressMsg) (tea.Cmd, OverlayResult) {
	a := c.a
	if !q.loaded {
		if k.String() == "esc" {
			return nil, Close
		}
		return nil, Keep // read-only until the draft is restored
	}
	if key.Matches(k, c.Keys.Binding(ActSubmit)) {
		if strings.TrimSpace(q.editor.Value()) == "" {
			return a.toastInfo("Nothing to create"), Keep
		}
		q.step = qaPreview
		q.banner = nil
		return tea.Batch(q.save(a), q.replan(a)), Keep // a step change saves
	}
	switch k.String() {
	case "esc":
		return q.save(a), Close // the draft is kept
	case "ctrl+x":
		if strings.TrimSpace(q.editor.Value()) == "" {
			return nil, Close
		}
		a.pushOverlay(newConfirm("Discard this draft?", func(*Ctx) tea.Cmd {
			q.editor.SetValue("")
			q.marks = nil
			cmd := q.save(a) // an empty draft deletes the file
			a.closeOverlay(q)
			return cmd
		}))
		return nil, Keep
	}
	before := q.editor.Value()
	cmd, _ := q.editor.Update(c, k)
	if q.editor.Value() != before {
		return tea.Batch(cmd, q.onEdited(a)), Keep
	}
	return cmd, Keep
}

// replan asks the service for a plan of the current text and options.
func (q *QuickAdd) replan(a *App) tea.Cmd {
	q.planSeq++
	q.planning = true
	seq, text, opts, svc := q.planSeq, q.editor.Value(), q.opts, a.deps.Service
	opts.Assignees = append([]string(nil), opts.Assignees...)
	opts.Labels = append([]string(nil), opts.Labels...)
	return func() tea.Msg {
		p, err := svc.PlanQuickAdd(a.ctx, text, opts)
		return planMsg{seq: seq, plan: p, err: err}
	}
}

func (q *QuickAdd) onPreviewAction(c *Ctx, act Action) (tea.Cmd, OverlayResult) {
	a := c.a
	switch act {
	case ActBack:
		q.step = qaWrite
		q.plan, q.planErr = nil, nil
		return q.save(a), Keep // back to the text, unchanged
	case ActDown, ActUp:
		if q.plan != nil {
			q.offset = listNav(act, q.offset, len(q.plan.Items), 2)
		}
	case ActDoneItems:
		if q.opts.Checked == "close" {
			q.opts.Checked = "skip"
		} else {
			q.opts.Checked = "close"
		}
		return q.replan(a), Keep
	case ActAssign:
		return a.openAssigneePicker("Assignees · all items", q.opts.Assignees, func(_ *Ctx, sel []string) tea.Cmd {
			q.opts.Assignees = sel
			return q.replan(a)
		}), Keep
	case ActLabel:
		return a.openLabelPicker("Labels · all items", q.opts.Labels, func(_ *Ctx, sel []string) tea.Cmd {
			q.opts.Labels = sel
			return q.replan(a)
		}), Keep
	case ActStatus:
		return a.openStatusPicker("Board status · all items", statusChoice(q.opts.Status, q.opts.AddToBoard), false, true, !q.fromBoard,
			func(_ *Ctx, sel []string) tea.Cmd {
				q.opts.Status, q.opts.AddToBoard = parseStatusChoice(sel[0])
				if q.fromBoard {
					q.opts.AddToBoard = true
				}
				return q.replan(a)
			}), Keep
	case ActApply:
		if q.blocked() || q.planning || q.plan.ToCreate == 0 {
			return nil, Keep
		}
		// Flush the draft first; the run starts when the queue is empty, so
		// the run goroutine is then the only writer (tui.md 12.4).
		q.starting = true
		cmd := q.save(a)
		if cmd == nil && !a.drafts.saving {
			return q.startRun(a), Keep
		}
		return cmd, Keep
	}
	return nil, Keep
}

// startRun starts the run goroutine. It owns a private copy of the draft and
// saves it synchronously from the progress callback (A7.12).
func (q *QuickAdd) startRun(a *App) tea.Cmd {
	q.starting = false
	q.step = qaRunning
	q.stopping = false
	a.runSeq++
	q.runID = a.runSeq
	q.stop = &atomic.Bool{}
	q.events = make(chan tea.Msg, 64)
	plan := *q.plan
	q.rows = make([]qaRow, len(plan.Items))
	rd := &runDraft{plan: plan, outcomes: map[int]domain.QuickAddOutcome{}, store: a.deps.Drafts, repo: q.repo,
		d: domain.Draft{Version: 1, RepoKey: repoKey(q.repo), Revision: q.rev}}
	svc, ctx, events, runID, stop := a.deps.Service, a.ctx, q.events, q.runID, q.stop
	// Messages go straight into the program (Program.Send) when there is one;
	// otherwise through a channel read by waitEvent.
	send, streamed := a.deps.Send, a.deps.Send == nil
	if streamed {
		send = func(m tea.Msg) { events <- m }
	}
	a.deps.Go(func() {
		res, err := svc.RunQuickAdd(ctx, plan, domain.QuickAddRunOptions{
			StopRequested: stop.Load,
			OnProgress: func(p domain.QuickAddProgress) error {
				if err := rd.apply(p); err != nil {
					return err // the run stops before the next write
				}
				send(qaProgressMsg{runID: runID, p: p})
				return nil
			},
		})
		send(qaDoneMsg{runID: runID, res: res, err: err, d: rd.d})
		if streamed {
			close(events)
		}
	})
	if streamed {
		return waitEvent(events)
	}
	return nil
}

// waitEvent receives the next message of a run (the standard streaming
// pattern: no Program pointer, no shared mutation).
func waitEvent(ch chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		m, ok := <-ch
		if !ok {
			return nil
		}
		return m
	}
}

func (q *QuickAdd) onProgress(a *App, p domain.QuickAddProgress) {
	if p.Index < 0 || p.Index >= len(q.rows) {
		return
	}
	r := &q.rows[p.Index]
	r.phase = p.Phase
	if p.Number > 0 {
		r.number = p.Number
	}
	if p.Phase == "settled" {
		r.outcome = p.Outcome
		r.err = p.Err
		if p.Item != nil && p.Item.Issue != nil {
			a.created(*p.Item.Issue, a.now(), q.fromBoard) // the list fills in live
		}
	}
}

// onDone finishes the run: complete closes quick add; a stop returns to the
// write step with only the items not created (5.5, UI 3.11).
func (q *QuickAdd) onDone(a *App, m qaDoneMsg) (tea.Cmd, OverlayResult) {
	asked := q.stopping
	q.step = qaWrite
	q.stopping = false
	res := m.res
	var created, notPlaced, notClosed []int
	complete := m.err == nil
	plan := q.plan
	for i, r := range res.Items {
		switch r.Result {
		case "created":
			if r.Issue != nil {
				created = append(created, r.Issue.Number)
				wantBoard := plan != nil && plan.Options.AddToBoard && !r.Item.Checked
				if wantBoard && !r.OnBoard {
					notPlaced = append(notPlaced, r.Issue.Number)
					complete = false
				}
				if r.Item.Checked && !r.Closed {
					notClosed = append(notClosed, r.Issue.Number)
					complete = false
				}
			}
		case "failed", "uncertain", "unattempted":
			complete = false
		}
		_ = i
	}
	if complete {
		q.marks = nil
		return tea.Batch(a.saveDraft(draftJob{repo: q.repo}),
			a.toastOK(createdText(created, res.Counts.Skipped))), Close
	}
	// Stopped: the text is exactly the items not created, in their bytes.
	d := m.d
	q.rev = d.Revision
	q.editor.SetValue(d.Text)
	q.marks = nil
	for _, s := range d.Uncertain {
		if s.StartByte >= 0 && s.EndByte <= len(d.Text) && s.StartByte <= s.EndByte {
			src := d.Text[s.StartByte:s.EndByte]
			q.marks = append(q.marks, mayExist{source: src, title: firstLine(src)})
		}
	}
	q.banner = stopBanner(res, m.err, asked, created, notPlaced, notClosed)
	q.plan = nil
	return q.save(a), Keep // the rebuilt draft, saved at once
}

func createdText(nums []int, skipped int) string {
	s := "Created " + itoa(len(nums)) + plural(len(nums), " issues")
	if len(nums) > 0 {
		s += " #" + itoaPlain(nums[0])
		if len(nums) > 1 {
			s += "–#" + itoaPlain(nums[len(nums)-1])
		}
	}
	if skipped > 0 {
		s += " · " + itoa(skipped) + " skipped"
	}
	return s
}

// stopBanner names why it stopped, what was created, and what was created
// but not placed or not closed (UI 3.11).
// asked: the user pressed esc to stop. The real service reports that stop as
// a cancelled context ("operation interrupted"), which is not a failure.
func stopBanner(res domain.QuickAddResult, err error, asked bool, created, notPlaced, notClosed []int) []string {
	var out []string
	reason := ""
	if de := toDomainError(err); de != nil {
		if asked || errors.Is(err, context.Canceled) || (de.Err != nil && strings.Contains(strings.ToLower(errText(de)), "cancel")) {
			reason = "as you asked"
		} else {
			// The service's run error already says "quick add stopped: …".
			reason = errText(de)
			for _, p := range []string{"quick add stopped: ", "quick add: "} {
				if len(reason) >= len(p) && strings.EqualFold(reason[:len(p)], p) {
					reason = reason[len(p):]
				}
			}
		}
	}
	at := ""
	for _, r := range res.Items {
		if r.Result == "failed" || r.Result == "uncertain" {
			at = r.Item.Title
			if r.Error != nil {
				reason = errText(r.Error)
			}
			break
		}
	}
	if at != "" {
		out = append(out, "✗ Stopped at \""+line(at)+"\": "+reason+".")
	} else if reason == "" {
		out = append(out, "✗ Quick add stopped.")
	} else if reason == "as you asked" {
		out = append(out, "✗ Quick add stopped as you asked.")
	} else {
		out = append(out, "✗ Quick add stopped: "+reason+".")
	}
	var parts []string
	if len(created) > 0 {
		parts = append(parts, "Created "+numberList(created)+".")
	}
	for _, n := range notPlaced {
		parts = append(parts, "#"+itoaPlain(n)+" was created but is not on the board: press M on it.")
	}
	for _, n := range notClosed {
		parts = append(parts, "#"+itoaPlain(n)+" was created but is still open: press x on it.")
	}
	kept := res.Counts.Failed + res.Counts.Uncertain + res.Counts.Unattempted
	parts = append(parts, itoa(kept)+plural(kept, " items")+" kept below.")
	return append(out, parts...) // one sentence per line, so none is cut at 80 columns
}

func numberList(ns []int) string {
	var s []string
	for _, n := range ns {
		s = append(s, "#"+itoaPlain(n))
	}
	return strings.Join(s, ", ")
}

func firstLine(s string) string {
	l, _, _ := strings.Cut(strings.TrimLeft(s, "\n"), "\n")
	return line(l)
}

func (q *QuickAdd) Cursor(c *Ctx) *tea.Cursor {
	if q.step != qaWrite || !q.loaded {
		return nil
	}
	cur := q.editor.Cursor()
	if cur == nil {
		return nil
	}
	cur.X += c.Frame.Body.X + 1
	cur.Y += c.Frame.Body.Y + q.editorTop()
	return cur
}

func (q *QuickAdd) editorTop() int {
	n := 2 + len(q.banner) // heading, rule, banner
	if q.loadErr != nil {
		n++
	}
	return n
}

// layout sizes the editor under the heading and banner, above the
// "may exist" list and the reminder line.
func (q *QuickAdd) layout(c *Ctx) {
	below := 1 + len(q.marks)
	q.editor.SetSize(c.Frame.Body.W-2, max(1, c.Frame.Body.H-q.editorTop()-below))
}

func (q *QuickAdd) View(c *Ctx, body Rect) (string, Rect) {
	switch q.step {
	case qaPreview:
		return q.viewPreview(c, body), body
	case qaRunning:
		return q.viewRunning(c, body), body
	}
	return q.viewWrite(c, body), body
}

func (q *QuickAdd) viewWrite(c *Ctx, body Rect) string {
	t := c.Theme
	w := body.W
	lines := strings.Count(q.editor.Value(), "\n") + 1
	status := ""
	switch {
	case q.loadErr != nil:
	case !q.loaded:
		status = t.muted().Render(c.Spin + " restoring draft…")
	default:
		status = t.muted().Render("draft kept on this machine")
	}
	out := []string{spread(" "+t.bold().Render("Quick add")+t.muted().Render(" · "+itoa(lines)+plural(lines, " lines")), status+" ", w)}
	if q.loadErr != nil {
		out = append(out, " "+t.errorS().Render("✗ Saved draft unreadable (left on disk): "+errText(q.loadErr)))
	}
	for i, b := range q.banner {
		if i == 0 {
			out = append(out, " "+t.errorS().Render(b))
		} else {
			out = append(out, "   "+t.text().Render(b))
		}
	}
	out = append(out, t.subtle().Render(strings.Repeat("─", w)))
	q.layout(c)
	for _, l := range strings.Split(q.editor.View(c), "\n") {
		out = append(out, " "+l)
	}
	for len(out) < body.H-1-len(q.marks) {
		out = append(out, "")
	}
	for _, m := range q.marks {
		out = append(out, " "+t.pending().Render("⚠ may exist: check GitHub before resending · ")+t.text().Render(m.title))
	}
	out = append(out, " "+t.muted().Render("One issue per line · indent 2+ spaces to add a body · - [x] marks it done"))
	return box(out, w, body.H)
}

func (q *QuickAdd) viewPreview(c *Ctx, body Rect) string {
	t := c.Theme
	w := body.W
	if q.plan == nil {
		msg := c.Spin + " Reading the list…"
		if q.planErr != nil {
			msg = "✗ " + errText(q.planErr) + hintSuffix(q.planErr)
		}
		return box([]string{" " + t.bold().Render("Quick add"), t.subtle().Render(strings.Repeat("─", w)), " " + t.muted().Render(msg)}, w, body.H)
	}
	p := q.plan
	open, done := 0, 0
	for _, it := range p.Items {
		if it.Checked {
			done++
		} else {
			open++
		}
	}
	head := t.bold().Render("Quick add") + t.muted().Render(" · "+itoa(open)+plural(open, " issues"))
	if done > 0 {
		d := " · " + itoa(done) + " done"
		if q.opts.Checked == "close" {
			d += ", filed as closed"
		}
		head += t.muted().Render(d)
	}
	people := t.muted().Render("—")
	if len(q.opts.Assignees) > 0 {
		people = t.text().Render("@" + strings.Join(q.opts.Assignees, " @"))
	}
	labels := t.muted().Render("—")
	if len(q.opts.Labels) > 0 {
		var ls []domain.Label
		for _, n := range q.opts.Labels {
			ls = append(ls, labelNamed(n, nil, c.Store.Meta.Labels))
		}
		labels = labelDots(t, ls)
	}
	status := t.muted().Render("Not on board")
	if q.opts.AddToBoard {
		status = t.text().Render(statusName(q.opts.Status))
	}
	doneChoice := "skip"
	if q.opts.Checked == "close" {
		doneChoice = "file as closed"
	}
	out := []string{
		spread(" "+head, t.muted().Render("esc back to text")+" ", w),
		" " + t.muted().Render("Assignees ") + people + t.muted().Render(" (a)   Labels ") + labels +
			t.muted().Render(" (L)   Status ") + status + t.muted().Render(" (M)"),
		spread(" "+t.muted().Render("Done items ")+t.text().Render(doneChoice)+t.muted().Render(" (d)"),
			t.muted().Render("done items are not added to the board")+" ", w), // always shown (5.5)
		t.subtle().Render(strings.Repeat("─", w)),
	}
	if len(p.Problems) > 0 {
		for _, pr := range p.Problems {
			out = append(out, " "+t.errorS().Render("✗ line "+itoaPlain(pr.Line)+": "+line(pr.Message)))
		}
		out = append(out, " "+t.muted().Render("esc back to text to fix"))
		return box(out, w, body.H)
	}
	n := 0
	for i := q.offset; i < len(p.Items) && len(out) < body.H; i++ {
		it := p.Items[i]
		num, note := "", ""
		switch {
		case it.Checked && q.opts.Checked == "skip":
			num = t.fg(t.Done).Render("✓")
			note = t.muted().Render("skipped: done")
		case it.Checked:
			n++
			num = t.fg(t.Done).Render("✓")
			note = t.muted().Render("create, then close")
		default:
			n++
			num = t.muted().Render(itoaPlain(n))
			if body := strings.Count(strings.TrimRight(it.Body, "\n"), "\n") + 1; strings.TrimSpace(it.Body) != "" {
				note = t.muted().Render("+ " + itoa(body) + plural(body, " body lines"))
			}
		}
		// An item flagged in the write step keeps its flag here, where enter
		// sends it: it is never sent again silently (5.5, live dogfood).
		if !(it.Checked && q.opts.Checked == "skip") && slices.ContainsFunc(q.marks, func(m mayExist) bool { return m.source == it.Source }) {
			note = t.pending().Render("⚠ may exist: check GitHub before resending")
		}
		out = append(out, spread("  "+fit(num, 3)+t.text().Render(line(it.Title)), note+" ", w))
	}
	return box(out, w, body.H)
}

func (q *QuickAdd) viewRunning(c *Ctx, body Rect) string {
	t := c.Theme
	w := body.W
	// "creating X of N" counts only the issues to create: a skipped done item
	// is not one of them (live dogfood: "creating 2 of 5" for 4 issues).
	total, doneN := q.plan.ToCreate, 0
	for _, r := range q.rows {
		if r.phase == "settled" && r.outcome != "skipped" {
			doneN++
		}
	}
	right := t.muted().Render("esc stop after this")
	if q.stopping {
		right = t.pending().Render("stopping after this item…")
	}
	out := []string{
		spread(" "+t.bold().Render("Quick add")+t.muted().Render(" · creating "+itoa(min(doneN+1, total))+" of "+itoa(total)), right+" ", w),
		t.subtle().Render(strings.Repeat("─", w)),
	}
	for i, it := range q.plan.Items {
		r := q.rows[i]
		var mark, num, note string
		switch {
		case r.phase == "creating" || r.phase == "closing" || r.phase == "placing":
			mark = t.pending().Render(c.Spin)
			note = t.muted().Render(r.phase)
		case r.outcome == "created":
			mark = t.fg(t.Open).Render("✓")
			if it.Checked && r.err == nil {
				note = t.muted().Render("closed")
			}
		case r.outcome == "skipped":
			mark = t.muted().Render("–")
			note = t.muted().Render("skipped: done")
		case r.outcome == "uncertain":
			mark = t.pending().Render("?")
			note = t.pending().Render("may exist")
		case r.outcome == "failed":
			mark = t.errorS().Render("✗")
		default:
			mark = t.muted().Render("·")
		}
		if r.number > 0 {
			num = "#" + itoaPlain(r.number)
		}
		if r.err != nil {
			note = t.errorS().Render(errText(r.err))
		}
		out = append(out, spread("  "+mark+"  "+fit(t.text().Render(num), 6)+" "+t.text().Render(line(it.Title)), note+" ", w))
	}
	return box(out, w, body.H)
}
