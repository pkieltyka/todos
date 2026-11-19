package tui

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/pkieltyka/todos/internal/domain"
)

// memDrafts is an in-memory DraftStore (the real one is config, Phase 7).
// Sharing one between two harnesses stages a crash and a restart.
type memDrafts struct {
	mu        sync.Mutex
	m         map[string]domain.Draft
	saves     []domain.Draft
	deletes   int
	loadErr   error
	failAfter int // fail every save after this many (0 = never)
	attempts  int
	failOn    map[int]bool // fail these save attempts (1-based)
}

func newMemDrafts() *memDrafts { return &memDrafts{m: map[string]domain.Draft{}} }

func (s *memDrafts) Load(r domain.RepoRef) (domain.Draft, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loadErr != nil {
		return domain.Draft{}, false, s.loadErr
	}
	d, ok := s.m[repoKey(r)]
	return d, ok, nil
}

func (s *memDrafts) Save(d domain.Draft) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attempts++
	if s.failOn[s.attempts] {
		return errors.New("disk full")
	}
	if s.failAfter > 0 && len(s.saves) >= s.failAfter {
		return errors.New("disk full")
	}
	s.saves = append(s.saves, d)
	s.m[d.RepoKey] = d
	return nil
}

func (s *memDrafts) Delete(r domain.RepoRef) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deletes++
	delete(s.m, repoKey(r))
	return nil
}

func (s *memDrafts) current() (domain.Draft, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.m["github.com/pkieltyka/todos"]
	return d, ok
}

func (s *memDrafts) saveCount() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.saves) }

func withDrafts(s *memDrafts) harnessOpt {
	return func(_ *harness, d *Deps, _ *Options) { d.Drafts = s }
}

func (h *harness) qa() *QuickAdd {
	for _, o := range h.app.overlays {
		if q, ok := o.(*QuickAdd); ok {
			return q
		}
	}
	h.t.Fatal("quick add not open")
	return nil
}

func (h *harness) paste(s string) { h.t.Helper(); h.send(tea.PasteMsg{Content: s}) }

const threeItems = "- Alpha\n- Bravo\n  with a body\n- Charlie\n"

// ---- opening and the draft ----

func TestQuickAdd_loadRestoresTextAndMarks(t *testing.T) {
	store := newMemDrafts()
	text := "- Alpha\n- Bravo\n"
	store.m["github.com/pkieltyka/todos"] = domain.Draft{RepoKey: "github.com/pkieltyka/todos", Text: text,
		Uncertain: []domain.DraftSpan{{StartByte: 8, EndByte: 16, SourceSHA256: sourceHash("- Bravo\n")}}}
	h := newHarness(t, richFixture(), withDrafts(store))
	h.press("A")
	if got := h.qa().editor.Value(); got != text {
		t.Fatalf("text %q", got)
	}
	h.contains("⚠ may exist: check GitHub before resending · - Bravo")
}

func TestQuickAdd_inFlightOnLoadBecomesMark(t *testing.T) {
	store := newMemDrafts()
	store.m["github.com/pkieltyka/todos"] = domain.Draft{RepoKey: "github.com/pkieltyka/todos", Text: "- Alpha\n",
		InFlight: &domain.DraftSpan{StartByte: 0, EndByte: 8, SourceSHA256: sourceHash("- Alpha\n")}}
	h := newHarness(t, richFixture(), withDrafts(store))
	h.press("A")
	h.contains("⚠ may exist")
}

func TestQuickAdd_staleSpanHashDropped(t *testing.T) {
	store := newMemDrafts()
	store.m["github.com/pkieltyka/todos"] = domain.Draft{RepoKey: "github.com/pkieltyka/todos", Text: "- Alpha\n",
		Uncertain: []domain.DraftSpan{{StartByte: 0, EndByte: 8, SourceSHA256: sourceHash("- Other\n")}}}
	h := newHarness(t, richFixture(), withDrafts(store))
	h.press("A")
	h.lacks("may exist")
}

func TestQuickAdd_unreadableDraftNeverOverwritten(t *testing.T) {
	store := newMemDrafts()
	store.loadErr = &domain.Error{Kind: domain.ErrConfig, Message: "draft file is not valid JSON"}
	h := newHarness(t, richFixture(), withDrafts(store))
	h.press("A")
	h.contains("Saved draft unreadable (left on disk): draft file is not valid JSON")
	h.typeText("- new")
	h.fire(draftTickMsg{})
	h.press("ctrl+s")
	if store.saveCount() != 0 || store.deletes != 0 {
		t.Fatal("an unreadable draft was overwritten")
	}
}

func TestQuickAdd_saves500msAfterLastKeystroke(t *testing.T) {
	store := newMemDrafts()
	h := newHarness(t, richFixture(), withDrafts(store))
	h.press("A")
	h.typeText("- Alpha")
	if store.saveCount() != 0 {
		t.Fatal("saved before the delay")
	}
	if d := h.delays[len(h.delays)-1]; d != 500*time.Millisecond {
		t.Fatalf("draft saved %v after the last keystroke, want 500ms", d)
	}
	h.fire(draftTickMsg{}) // seven ticks were scheduled; only the last one is current
	if store.saveCount() != 1 {
		t.Fatalf("%d saves, want 1", store.saveCount())
	}
	if d, _ := store.current(); d.Text != "- Alpha" {
		t.Fatalf("saved %q", d.Text)
	}
}

func TestQuickAdd_writerLatestWinsInOrder(t *testing.T) {
	store := newMemDrafts()
	a := New(Deps{Service: nil, Drafts: store}, Options{})
	repo := domain.RepoRef{Host: "github.com", Owner: "pkieltyka", Name: "todos"}
	job := func(text string) draftJob {
		return draftJob{repo: repo, d: &domain.Draft{RepoKey: repoKey(repo), Text: text}}
	}
	first := a.saveDraft(job("one"))
	if first == nil {
		t.Fatal("first save not started")
	}
	if a.saveDraft(job("two")) != nil || a.saveDraft(job("three")) != nil {
		t.Fatal("a second save started while one is running")
	}
	first() // the first save completes
	next, empty := a.onDraftSaved()
	if empty || next == nil {
		t.Fatal("queued save not sent")
	}
	next()
	if _, empty := a.onDraftSaved(); !empty {
		t.Fatal("queue not empty")
	}
	if got := []string{store.saves[0].Text, store.saves[1].Text}; fmt.Sprint(got) != "[one three]" || len(store.saves) != 2 {
		t.Fatalf("saves %v: the latest must win, in order", got)
	}
}

func TestQuickAdd_flushOnEveryStepChange(t *testing.T) {
	store := newMemDrafts()
	h := newHarness(t, richFixture(), withDrafts(store))
	h.press("A")
	h.paste(threeItems)
	h.press("alt+s") // write → preview
	if store.saveCount() != 1 {
		t.Fatalf("%d saves after preview", store.saveCount())
	}
	h.press("esc") // preview → write
	if store.saveCount() != 2 {
		t.Fatalf("%d saves after back", store.saveCount())
	}
	h.press("esc") // close: the draft is kept
	if store.saveCount() != 3 {
		t.Fatalf("%d saves after close", store.saveCount())
	}
	if _, ok := store.current(); !ok {
		t.Fatal("draft not kept on close")
	}
}

func TestQuickAdd_emptyTextDeletesDraft(t *testing.T) {
	store := newMemDrafts()
	store.m["github.com/pkieltyka/todos"] = domain.Draft{RepoKey: "github.com/pkieltyka/todos", Text: "- Alpha"}
	h := newHarness(t, richFixture(), withDrafts(store))
	h.press("A", "ctrl+x", "y")
	if _, ok := store.current(); ok || store.deletes == 0 {
		t.Fatal("discarded draft not deleted")
	}
	if len(h.app.overlays) != 0 {
		t.Fatal("quick add still open")
	}
}

func TestQuickAdd_discardAsksWhenNotEmpty(t *testing.T) {
	h := newHarness(t, richFixture(), withDrafts(newMemDrafts()))
	h.press("A")
	h.typeText("- Alpha")
	h.press("ctrl+x")
	h.contains("Discard this draft?")
	h.press("esc")
	if h.qa().editor.Value() != "- Alpha" {
		t.Fatal("text lost after declining")
	}
}

func TestQuickAdd_quitSavesDraft(t *testing.T) {
	store := newMemDrafts()
	h := newHarness(t, richFixture(), withDrafts(store))
	h.press("A")
	h.typeText("- unsaved")
	h.press("ctrl+c") // nothing in flight: quits, saving the draft first
	if !h.quit {
		t.Fatal("did not quit")
	}
	if d, _ := store.current(); d.Text != "- unsaved" {
		t.Fatalf("draft %q not saved on quit", d.Text)
	}
}

// ---- preview ----

func TestQuickAdd_fromBoardStatusAndAddToBoard(t *testing.T) {
	h := newHarness(t, richFixture(), withStart(ViewBoard), withDrafts(newMemDrafts()))
	h.press("l", "A") // the Todo column
	h.paste("- On the board\n")
	h.press("ctrl+s")
	calls := h.calls("PlanQuickAdd")
	o := calls[len(calls)-1].Args.QuickAdd
	if !o.AddToBoard || o.Status == nil || *o.Status != "Todo" {
		t.Fatalf("plan options %+v", o)
	}
	h.contains("Status Todo")
}

func TestQuickAdd_checkedResetsToSkipOnOpen(t *testing.T) {
	h := newHarness(t, richFixture(), withDrafts(newMemDrafts()))
	h.press("A")
	h.paste("- [x] Done already\n- Still to do\n")
	h.press("ctrl+s", "d")
	h.contains("1 done, filed as closed")
	h.contains("create, then close")
	h.press("esc", "esc", "A")
	if c := h.qa().opts.Checked; c != "skip" {
		t.Fatalf("done items %q on reopening", c)
	}
}

func TestQuickAdd_previewAlwaysSaysDoneItemsNotOnBoard(t *testing.T) {
	h := newHarness(t, richFixture(), withDrafts(newMemDrafts()))
	h.press("A")
	h.paste("- Alpha\n")
	h.press("ctrl+s")
	h.contains("done items are not added to the board") // no status set, no done item: still shown
}

func TestQuickAdd_replanOnBatchChange_dropsStalePlan(t *testing.T) {
	h := newHarness(t, richFixture(), withDrafts(newMemDrafts()))
	h.press("A")
	h.paste("- [x] Done\n- Open\n")
	g := h.hold("PlanQuickAdd", 0)
	h.press("ctrl+s") // plan 1, held
	h.press("d")      // plan 2 with "close"
	h.release(g)      // plan 1 lands late
	if q := h.qa(); q.plan == nil || q.plan.Options.Checked != "close" {
		t.Fatalf("stale plan kept: %+v", q.plan)
	}
}

func TestQuickAdd_problemsBlockCreate(t *testing.T) {
	h := newHarness(t, richFixture(), withDrafts(newMemDrafts()))
	h.press("A")
	h.paste("  indented before any item\n- Alpha\n")
	h.press("ctrl+s")
	h.contains("✗ line 1:")
	h.press("enter")
	if n := len(h.calls("RunQuickAdd")); n != 0 {
		t.Fatal("created despite problems")
	}
}

// ---- running ----

// inFlightText is the source bytes of the store's in-flight span.
func inFlightText(t *testing.T, s *memDrafts) string {
	d, ok := s.current()
	if !ok || d.InFlight == nil {
		t.Fatalf("no in-flight span in %+v", d)
	}
	return d.Text[d.InFlight.StartByte:d.InFlight.EndByte]
}

func TestQuickAdd_inFlightSavedBeforeCreate(t *testing.T) {
	store := newMemDrafts()
	h := newHarness(t, richFixture(), withDrafts(store))
	h.press("A")
	h.paste(threeItems)
	g := h.hold("CreateIssue", 0) // Alpha's create is held: it has been sent
	h.press("ctrl+s", "enter")
	if got := inFlightText(t, store); got != "- Alpha\n" {
		t.Fatalf("in flight %q", got)
	}
	h.release(g)
}

func TestQuickAdd_inFlightKeptThroughClosingAndPlacing(t *testing.T) {
	store := newMemDrafts()
	h := newHarness(t, richFixture(), withStart(ViewBoard), withDrafts(store))
	h.press("l", "A") // the Todo column: a named status, placed with BoardAdd
	h.paste("- Alpha\n")
	g := h.hold("BoardAdd", 104) // created as #104, placing is held
	h.press("ctrl+s", "enter")
	if got := inFlightText(t, store); got != "- Alpha\n" {
		t.Fatalf("in flight while placing %q", got)
	}
	h.release(g)
	h2 := newHarness(t, richFixture(), withDrafts(newMemDrafts()))
	h2.press("A")
	h2.paste("- [x] Done\n")
	gc := h2.hold("Close", 104)
	h2.press("ctrl+s", "d", "enter")
	if got := inFlightText(t, h2.app.deps.Drafts.(*memDrafts)); got != "- [x] Done\n" {
		t.Fatalf("in flight while closing %q", got)
	}
	h2.release(gc)
}

func TestQuickAdd_durableRebuildsFromSource(t *testing.T) {
	plan := domain.QuickAddPlan{Items: []domain.QuickAddItem{
		{Source: "- A\r\n"}, {Source: "- [x] B\n"}, {Source: "- C\n  body\n\n"}, {Source: "- D\n"}, {Source: "- E\n"},
	}}
	outcomes := map[int]domain.QuickAddOutcome{0: "created", 1: "skipped", 2: "uncertain", 3: "failed"}
	text, spans, flight := remaining(plan, outcomes, 4)
	if text != "- C\n  body\n\n- D\n- E\n" {
		t.Fatalf("text %q", text)
	}
	if len(spans) != 1 || text[spans[0].StartByte:spans[0].EndByte] != "- C\n  body\n\n" || spans[0].SourceSHA256 != sourceHash("- C\n  body\n\n") {
		t.Fatalf("spans %+v", spans)
	}
	if flight == nil || text[flight.StartByte:flight.EndByte] != "- E\n" {
		t.Fatalf("in flight %+v", flight)
	}
}

func TestQuickAdd_draftSaveFailureStopsRun(t *testing.T) {
	store := newMemDrafts()
	store.failAfter = 2 // preview and pre-run flush succeed; the in-flight mark fails
	h := newHarness(t, richFixture(), withDrafts(store))
	h.press("A")
	h.paste(threeItems)
	h.press("ctrl+s", "enter")
	if n := len(h.calls("CreateIssue")); n != 0 {
		t.Fatalf("%d issues created without a saved in-flight mark", n)
	}
	if h.qa().step != qaWrite {
		t.Fatal("run did not stop")
	}
	h.contains("quick-add progress could not be saved")
}

func TestQuickAdd_preRunFlushFailureSendsNothing(t *testing.T) {
	store := newMemDrafts()
	store.failAfter = 1 // the preview save succeeds; the flush before the run fails
	h := newHarness(t, richFixture(), withDrafts(store))
	h.press("A")
	h.paste(threeItems)
	h.press("ctrl+s", "enter")
	if n := len(h.calls("RunQuickAdd")); n != 0 {
		t.Fatalf("run started %d times without a saved draft", n)
	}
	if h.qa().step != qaPreview {
		t.Fatal("left the preview")
	}
	h.contains("Couldn't save the draft")
}

func TestQuickAdd_progressFillsListLive(t *testing.T) {
	h := newHarness(t, richFixture(), withDrafts(newMemDrafts()))
	h.press("A")
	h.paste(threeItems)
	g := h.hold("RunQuickAdd.item", 1) // Bravo waits; Alpha is done
	h.press("ctrl+s", "enter")
	h.contains("creating 2 of 3")
	if !slices.Contains(h.app.store.Issues.Numbers, 104) {
		t.Fatal("Alpha (#104) not in the list while the run continues")
	}
	h.release(g)
}

func TestQuickAdd_stopFinishesCurrentItem(t *testing.T) {
	store := newMemDrafts()
	h := newHarness(t, richFixture(), withDrafts(store))
	h.press("A")
	h.paste(threeItems)
	g := h.hold("CreateIssue", 0)
	h.press("ctrl+s", "enter")
	h.press("esc") // stop while Alpha is being created
	h.contains("stopping after this item…")
	h.release(g)
	if n := len(h.calls("CreateIssue")); n != 1 {
		t.Fatalf("%d creates, want only the current item", n)
	}
	q := h.qa()
	if q.step != qaWrite || q.editor.Value() != "- Bravo\n  with a body\n- Charlie\n" {
		t.Fatalf("after stop: step %d text %q", q.step, q.editor.Value())
	}
	h.contains("Created #104.")
}

func TestQuickAdd_stoppedTextIsRemainingSource_marksUncertainOnly(t *testing.T) {
	store := newMemDrafts()
	h := newHarness(t, richFixture(), withDrafts(store))
	h.press("A")
	h.paste("- Alpha\n- Bravo\n  with a body\n- Charlie\n")
	h.pass("CreateIssue", 0)
	g := h.hold("CreateIssue", 0) // Bravo's create
	h.press("ctrl+s", "enter")
	h.fail(g, uncertainErr())
	q := h.qa()
	if got := q.editor.Value(); got != "- Bravo\n  with a body\n- Charlie\n" {
		t.Fatalf("text %q: must be the remaining items' exact bytes", got)
	}
	if len(q.marks) != 1 || q.marks[0].source != "- Bravo\n  with a body\n" {
		t.Fatalf("marks %+v: only the uncertain item", q.marks)
	}
	if d, _ := store.current(); d.Text != q.editor.Value() || len(d.Uncertain) != 1 {
		t.Fatalf("stored draft %+v", d)
	}
	h.contains("Stopped at \"Bravo\"")
}

func TestQuickAdd_bannerNamesNotPlacedAndNotClosed(t *testing.T) {
	h := newHarness(t, richFixture(), withStart(ViewBoard), withDrafts(newMemDrafts()))
	h.press("l", "A") // the Todo column: a named status, placed with BoardAdd
	h.paste("- Alpha\n- Bravo\n")
	g := h.hold("BoardAdd", 104)
	h.press("ctrl+s", "enter")
	h.fail(g, &domain.Error{Kind: domain.ErrGitHub, Message: "board unavailable"})
	h.contains("#104 was created but is not on the board: press M on it.")
	if got := h.qa().editor.Value(); got != "- Bravo\n" {
		t.Fatalf("text %q: a created issue is never kept", got)
	}

	h = newHarness(t, richFixture(), withDrafts(newMemDrafts()))
	h.press("A")
	h.paste("- [x] Done\n")
	g = h.hold("Close", 104)
	h.press("ctrl+s", "d", "enter")
	h.fail(g, &domain.Error{Kind: domain.ErrForbidden, Message: "cannot close"})
	h.contains("#104 was created but is still open: press x on it.")
}

func TestQuickAdd_completeDeletesDraft_toasts(t *testing.T) {
	store := newMemDrafts()
	h := newHarness(t, richFixture(), withDrafts(store))
	h.press("A")
	h.paste("- Alpha\n- [x] Old\n- Bravo\n")
	h.press("ctrl+s", "enter")
	if len(h.app.overlays) != 0 {
		t.Fatal("quick add still open after full success")
	}
	if _, ok := store.current(); ok {
		t.Fatal("draft not removed after full success")
	}
	h.contains("✓ Created 2 issues #104–#105 · 1 skipped")
}

// A staged crash: reload the store while an item is being sent; the item
// comes back flagged "may exist" and nothing created is resent.
func TestQuickAdd_crashReloadFlagsInFlight(t *testing.T) {
	store := newMemDrafts()
	h := newHarness(t, richFixture(), withDrafts(store))
	h.press("A")
	h.paste(threeItems)
	h.pass("CreateIssue", 0)      // Alpha goes through
	g := h.hold("CreateIssue", 0) // Bravo is being sent when "todos dies"
	h.press("ctrl+s", "enter")

	restarted := newHarness(t, richFixture(), withDrafts(store))
	restarted.press("A")
	if got := restarted.qa().editor.Value(); got != "- Bravo\n  with a body\n- Charlie\n" {
		t.Fatalf("restored text %q: Alpha was created and must not come back", got)
	}
	restarted.contains("⚠ may exist: check GitHub before resending · - Bravo")
	h.release(g)
}

func TestQuit_duringQuickAddRunAsks(t *testing.T) {
	h := newHarness(t, richFixture(), withDrafts(newMemDrafts()))
	h.press("A")
	h.paste(threeItems)
	g := h.hold("RunQuickAdd.item", 0)
	h.press("ctrl+s", "enter", "ctrl+c")
	if h.quit {
		t.Fatal("quit during a run without asking")
	}
	h.contains("still sending. Quit anyway?")
	h.press("esc")
	h.release(g)
}

func TestQuickAdd_footerAndReminder(t *testing.T) {
	h := newHarness(t, richFixture(), withDrafts(newMemDrafts()))
	h.press("A")
	h.contains("One issue per line · indent 2+ spaces to add a body · - [x] marks it done")
	if !strings.Contains(h.view(), "ctrl+s/alt+s preview") {
		t.Fatal("write-step footer")
	}
}

func TestQuit_confirmedSavesDraft(t *testing.T) {
	store := newMemDrafts()
	h := newHarness(t, richFixture(), withDrafts(store))
	g := h.hold("Close", 6)
	h.press("x", "A")
	h.typeText("- unsaved")
	h.press("ctrl+c")
	h.contains("1 change still sending. Quit anyway?")
	h.press("y")
	if !h.quit {
		t.Fatal("did not quit")
	}
	if d, _ := store.current(); d.Text != "- unsaved" {
		t.Fatalf("draft %q not saved on a confirmed quit", d.Text)
	}
	h.release(g)
}

// runNow runs a Cmd to completion on the test goroutine, feeding every
// message to Update, so a test can choose the order two Cmds land in.
func (h *harness) runNow(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	switch m := cmd().(type) {
	case nil:
	case tea.BatchMsg:
		for _, c := range m {
			h.runNow(c)
		}
	default:
		_, next := h.app.Update(m)
		h.runNow(next)
	}
}

func TestQuickAdd_reopenWaitsForCloseSave(t *testing.T) {
	store := newMemDrafts()
	h := newHarness(t, richFixture(), withDrafts(store))
	h.press("A")
	h.typeText("- old")
	h.fire(draftTickMsg{})
	h.typeText(" new")
	// esc and A arrive before the close-save has run; the reopen's Cmds land first.
	_, save := h.app.Update(keyMsg("esc"))
	_, reopen := h.app.Update(keyMsg("A"))
	h.runNow(reopen)
	h.runNow(save)
	if got := h.qa().editor.Value(); got != "- old new" {
		t.Fatalf("reopened with %q, want the text saved on close", got)
	}
}

func TestQuickAdd_saveErrorShownAgainAfterASuccess(t *testing.T) {
	store := newMemDrafts()
	store.failOn = map[int]bool{1: true, 3: true}
	h := newHarness(t, richFixture(), withDrafts(store))
	h.press("A")
	h.typeText("a")
	h.fire(draftTickMsg{})
	h.contains("Draft not saved")
	h.typeText("b") // dismisses the toast
	h.fire(draftTickMsg{})
	h.typeText("c")
	h.fire(draftTickMsg{})
	if h.app.toast == nil || !strings.Contains(h.app.toast.Text, "Draft not saved") {
		t.Fatal("a failure after a successful save was not shown")
	}
}

func TestQuickAdd_inFlightSpanAfterCreatedAndDuplicates(t *testing.T) {
	plan := domain.QuickAddPlan{Items: []domain.QuickAddItem{
		{Source: "- Same\n"}, {Source: "- Done\n"}, {Source: "- Same\n"}, {Source: "- Last\n"},
	}}
	outcomes := map[int]domain.QuickAddOutcome{0: "created", 1: "uncertain"}
	text, spans, flight := remaining(plan, outcomes, 2)
	if text != "- Done\n- Same\n- Last\n" {
		t.Fatalf("text %q", text)
	}
	if len(spans) != 1 || spans[0].StartByte != 0 || spans[0].EndByte != 7 {
		t.Fatalf("uncertain spans %+v", spans)
	}
	if flight == nil || flight.StartByte != 7 || flight.EndByte != 14 || flight.SourceSHA256 != sourceHash("- Same\n") {
		t.Fatalf("in-flight span %+v, want the second Same at 7..14", flight)
	}
}
