package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/pkieltyka/todos/internal/domain"
	"github.com/pkieltyka/todos/internal/service/servicetest"
)

// Phase 6 flow tests (tui.md 18.4). Default fixture: open issues #6, #5, #4,
// #1 (closed #3, #2); #6 on the board with no status, #5 in Todo, #4 in In
// progress, #1 not on the board; #6 is assigned to reviewer with labels bug
// and sync. Every race is staged with the fake's gates.

func uncertainErr() error {
	return &domain.Error{Kind: domain.ErrTimeout, Message: "the request timed out", Uncertain: true}
}

func (h *harness) base(n int) domain.Issue {
	i, _ := h.app.store.Issue(n)
	return i
}

func (h *harness) startedNumbers(methods ...string) []int {
	var out []int
	for _, c := range h.fake.Calls() {
		if slices.Contains(methods, c.Method) {
			out = append(out, c.Args.Number)
		}
	}
	return out
}

// ---- the engine through the UI ----

func TestPending_oneInFlightPerIssue(t *testing.T) {
	h := newHarness(t, richFixture())
	g := h.hold("Close", 6)
	h.press("x") // close #6: in flight, held
	h.press("x") // the effective issue is closed, so this queues a reopen
	if n := len(h.calls("Reopen")); n != 0 {
		t.Fatalf("reopen sent while the close is in flight (%d)", n)
	}
	h.contains("#6") // still listed, drawn closed with a spinner
	h.release(g)
	if n := len(h.calls("Reopen")); n != 1 {
		t.Fatalf("reopen not sent after the close settled (%d)", n)
	}
	if b := h.base(6); b.State != "open" {
		t.Fatalf("base state %q", b.State)
	}
}

func TestPending_atMostFourInFlight_FIFO(t *testing.T) {
	h := newHarness(t, richFixture())
	h.press("s", "s") // all: rows #6 #5 #4 #3 #2 #1
	gates := map[int]*hgate{}
	for _, n := range []int{6, 5, 4, 1} {
		gates[n] = h.hold("Close", n)
	}
	for _, n := range []int{3, 2} {
		gates[n] = h.hold("Reopen", n)
	}
	for k := 0; k < 6; k++ {
		h.press("x", "j")
	}
	if got := h.startedNumbers("Close", "Reopen"); fmt.Sprint(got) != "[6 5 4 3]" {
		t.Fatalf("started %v, want the first four in order", got)
	}
	h.release(gates[5])
	if got := h.startedNumbers("Close", "Reopen"); fmt.Sprint(got) != "[6 5 4 3 2]" {
		t.Fatalf("after one settled: %v", got)
	}
	for _, n := range []int{6, 4, 3, 2} {
		h.release(gates[n])
	}
	h.release(gates[1])
	if h.app.pending.Count() != 0 {
		t.Fatal("changes left pending")
	}
}

func TestPending_successPutsArrivalAsOf_sendsNext(t *testing.T) {
	h := newHarness(t, richFixture())
	g := h.hold("Close", 6)
	h.press("x", "x")
	h.advance(5 * time.Second)
	gr := h.hold("Reopen", 6)
	h.release(g)
	e, _ := h.app.store.Entry(6)
	if e.Base.State != "closed" || !e.AsOf.Equal(fixtureNow.Add(5*time.Second)) {
		t.Fatalf("base %q asOf %v, want closed at the arrival time", e.Base.State, e.AsOf)
	}
	if n := len(h.calls("Reopen")); n != 1 {
		t.Fatal("next change not sent")
	}
	h.release(gr)
}

func TestPending_noExtraReadAfterSuccess(t *testing.T) {
	h := newHarness(t, richFixture())
	reads := len(h.calls("GetIssue")) + len(h.calls("ListIssues"))
	h.press("x")
	if n := len(h.calls("GetIssue")) + len(h.calls("ListIssues")); n != reads {
		t.Fatalf("%d extra reads after a successful change", n-reads)
	}
}

func TestPending_rejectSnapsBack_toasts_laterQueuedStillRun(t *testing.T) {
	h := newHarness(t, richFixture())
	g := h.hold("Close", 6)
	h.press("x")
	h.press("L", "j", "j", "space", "enter") // queue +docs behind the close
	if n := len(h.calls("UpdateLabels")); n != 0 {
		t.Fatal("label change sent while the close is in flight")
	}
	h.fail(g, &domain.Error{Kind: domain.ErrForbidden, Message: "you need triage access"})
	h.contains("✗ Couldn't close #6: you need triage access")
	if i, _ := h.app.ctxFor(h.app.theme).Issue(6); i.State != "open" {
		t.Fatalf("rejected close still drawn: %q", i.State)
	}
	if n := len(h.calls("UpdateLabels")); n != 1 {
		t.Fatal("the queued label change did not run after the rejection")
	}
	if !slices.Contains(nameList(h.base(6).Labels), "docs") {
		t.Fatal("label change not applied")
	}
}

func TestPending_partialRemovedStepPutsIssue(t *testing.T) {
	h := newHarness(t, richFixture())
	g := h.hold("UpdateLabels", 6)
	h.press("L", "space", "j", "j", "space", "enter") // remove bug, add docs
	partial := h.base(6)
	partial.Labels = []domain.Label{testLabels[1]} // the removal happened
	h.fail(g, &domain.Error{Kind: domain.ErrPartial, Message: "could not add docs", Completed: []string{"removed"},
		Cause: domain.ErrForbidden, Issue: &partial})
	if got := nameList(h.base(6).Labels); fmt.Sprint(got) != "[sync]" {
		t.Fatalf("base labels %v", got)
	}
	h.contains("removed done")
}

func TestPending_partialAddedToBoardPutsIssue(t *testing.T) {
	h := newHarness(t, richFixture())
	h.press("j", "j", "j") // #1, not on the board
	g := h.hold("SetStatus", 1)
	h.press("M", "enter") // Todo
	partial := h.base(1)
	partial.Board = &domain.Placement{ItemID: "ITEM_1"}
	h.fail(g, &domain.Error{Kind: domain.ErrPartial, Message: "could not set the status", Completed: []string{"added_to_board"},
		Cause: domain.ErrGitHub, Issue: &partial})
	if b := h.base(1).Board; b == nil || b.Status != nil {
		t.Fatalf("placement %+v, want on the board with no status", b)
	}
	h.contains("added_to_board done")
}

func TestPending_uncertainHoldsQueue(t *testing.T) {
	h := newHarness(t, richFixture())
	g := h.hold("Close", 6)
	h.press("x", "x") // close, then a queued reopen
	gr := h.hold("GetIssue", 6)
	h.fail(g, uncertainErr())
	if n := len(h.calls("Reopen")); n != 0 {
		t.Fatal("queue not held after an uncertain outcome")
	}
	if h.view(); !strings.Contains(h.view(), "?○ #6") && !strings.Contains(h.view(), "?✓ #6") {
		t.Fatalf("no ? marker:\n%s", h.view())
	}
	h.release(gr) // a read sent after the failure lands: the hold is released
	if n := len(h.calls("Reopen")); n != 1 {
		t.Fatal("queue not resumed")
	}
}

func TestPending_uncertainReleasedByReadSentAfterFailure(t *testing.T) {
	h := newHarness(t, richFixture())
	g := h.hold("Close", 6)
	h.press("x")
	g.g.ApplyThenFail(uncertainErr()) // it did apply on GitHub
	g.done = true
	h.settle()
	if h.app.pending.Count() != 0 {
		t.Fatal("uncertain change not released by the re-read")
	}
	if h.base(6).State != "closed" {
		t.Fatal("base not from the re-read")
	}
}

func TestPending_uncertainAppliedAnyway_noToast(t *testing.T) {
	h := newHarness(t, richFixture())
	g := h.hold("Close", 6)
	h.press("x")
	g.g.ApplyThenFail(uncertainErr())
	g.done = true
	h.settle()
	h.lacks("couldn't confirm")
}

func TestPending_uncertainNotApplied_toastsCouldntConfirm(t *testing.T) {
	h := newHarness(t, richFixture())
	g := h.hold("Close", 6)
	h.press("x")
	h.fail(g, uncertainErr()) // it did not apply
	h.contains("#6: couldn't confirm the change; showing GitHub's current state")
	if h.base(6).State != "open" || h.app.pending.Count() != 0 {
		t.Fatal("not settled to GitHub's state")
	}
}

func TestPending_uncertainNotReleasedByReadSentBefore(t *testing.T) {
	h := newHarness(t, richFixture())
	gl := h.hold("ListIssues", 0)
	h.press("r") // a list read sent at t0
	h.advance(time.Second)
	g := h.hold("Close", 6)
	h.press("x")
	gr := h.hold("GetIssue", 6)
	h.fail(g, uncertainErr()) // fails at t0+1s
	h.release(gl)             // the old read lands after the failure
	if cs := h.app.pending.Changes(6); len(cs) != 1 || cs[0].Phase != Uncertain {
		t.Fatal("a read sent before the failure released the hold")
	}
	h.release(gr)
	if h.app.pending.Count() != 0 {
		t.Fatal("the re-read did not release it")
	}
}

func TestPending_uncertainReleasedByListPage(t *testing.T) {
	h := newHarness(t, richFixture())
	g := h.hold("Close", 6)
	h.press("x")
	gr := h.hold("GetIssue", 6)
	h.fail(g, uncertainErr())
	h.advance(time.Second)
	h.press("r") // a list read sent after the failure
	if h.app.pending.Count() != 0 {
		t.Fatal("the list page did not release the hold")
	}
	h.release(gr)
}

func TestPending_uncertainRereadRetriedOnAutoTick(t *testing.T) {
	h := newHarness(t, richFixture())
	g := h.hold("Close", 6)
	h.press("x")
	gr := h.hold("GetIssue", 6)
	h.fail(g, uncertainErr())
	h.fail(gr, &domain.Error{Kind: domain.ErrNetwork, Message: "offline"})
	reads := len(h.calls("GetIssue"))
	h.press("?") // an overlay: the rest of auto-refresh is skipped, the re-read is not
	h.advance(61 * time.Second)
	h.fire(autoRefreshMsg{})
	if n := len(h.calls("GetIssue")); n != reads+1 {
		t.Fatalf("held re-read not retried on the auto tick (%d → %d)", reads, n)
	}
}

func TestPending_refreshDoesNotOverridePendingField(t *testing.T) {
	h := newHarness(t, richFixture())
	g := h.hold("Close", 6)
	h.press("x")
	h.advance(time.Second)
	h.press("r") // a refresh lands with #6 still open on GitHub
	if i, _ := h.app.ctxFor(h.app.theme).Issue(6); i.State != "closed" {
		t.Fatal("refresh overrode the pending close")
	}
	h.release(g)
}

func TestPending_closeThenUndoQueuesReopen(t *testing.T) {
	h := newHarness(t, richFixture())
	h.press("x")
	h.contains("✓ Closed #6 · x to undo")
	h.press("x")
	if n := len(h.calls("Reopen")); n != 1 {
		t.Fatal("x on a closed issue did not reopen")
	}
}

func TestPending_closeReopenConfirmationToast(t *testing.T) {
	h := newHarness(t, richFixture())
	h.press("x")
	h.contains("✓ Closed #6 · x to undo")
	h.press("x")
	h.contains("✓ Reopened #6")
}

func TestPending_pickerDiffAgainstEffective(t *testing.T) {
	h := newHarness(t, richFixture())
	g := h.hold("UpdateLabels", 6)
	h.press("L", "j", "j", "space", "enter") // +docs, pending
	h.press("L")
	p := h.app.overlays[len(h.app.overlays)-1].(*Picker)
	if !p.selected["docs"] {
		t.Fatal("picker does not show the pending +docs ticked")
	}
	h.press("enter") // no difference against the effective issue
	h.release(g)
	if n := len(h.calls("UpdateLabels")); n != 1 {
		t.Fatalf("%d label calls, want 1", n)
	}
}

func TestPending_deletedIssueDropsLateResult(t *testing.T) {
	h := newHarness(t, richFixture())
	g := h.hold("Close", 6)
	h.press("x")
	h.press("D")
	h.typeText("6")
	h.press("enter")
	if _, ok := h.app.store.Issue(6); ok {
		t.Fatal("#6 not removed")
	}
	h.release(g) // the close's late result
	if _, ok := h.app.store.Issue(6); ok || h.app.pending.Count() != 0 {
		t.Fatal("late result brought #6 back")
	}
}

func TestPending_stepOnIssueNotOnBoard_toastsNoChange(t *testing.T) {
	h := newHarness(t, richFixture())
	h.press("j", "j", "j", ">") // #1 is not on the board
	h.contains("#1 isn't on the board · M to add it")
	if n := len(h.calls("SetStatus")); n != 0 {
		t.Fatal("stepping added the issue")
	}
}

func TestPending_closedUnderOpenFilterStaysUntilSwap(t *testing.T) {
	h := newHarness(t, richFixture())
	h.press("x")
	h.contains("✓ #6")
	h.advance(time.Second)
	h.press("r")
	h.lacks("#6 ")
}

func TestBoard_stepMovesCardOptimistically(t *testing.T) {
	h := newHarness(t, richFixture(), withStart(ViewBoard))
	g := h.hold("SetStatus", 6)
	h.press(">") // #6: No status → Todo
	cols := h.app.ctxFor(h.app.theme).Columns()
	if todo := colNums(cols, "Todo"); fmt.Sprint(todo) != "[5 6]" {
		t.Fatalf("Todo = %v, want #6 at the bottom", todo)
	}
	h.release(g)
	cols = h.app.ctxFor(h.app.theme).Columns()
	if todo := colNums(cols, "Todo"); fmt.Sprint(todo) != "[5 6]" {
		t.Fatalf("after settling Todo = %v", todo)
	}
}

func TestBoard_statusPickerRemoveFromBoard(t *testing.T) {
	h := newHarness(t, richFixture(), withStart(ViewBoard))
	h.press("M")
	p := h.app.overlays[len(h.app.overlays)-1].(*Picker)
	p.cursor = len(p.rows) - 1 // "Remove from board"
	h.press("enter")
	if n := len(h.calls("RemoveFromBoard")); n != 1 {
		t.Fatal("not removed")
	}
	if b := h.base(6).Board; b != nil {
		t.Fatalf("placement %+v", b)
	}
}

// ---- forms ----

func TestCreate_waitsThenInsertsAndSelects(t *testing.T) {
	h := newHarness(t, richFixture())
	h.press("n")
	h.typeText("A new thing")
	g := h.hold("CreateIssue", 0)
	h.press("alt+s")
	h.contains("Creating…")
	if n := len(h.app.store.Issues.Numbers); n != 4 {
		t.Fatal("inserted before GitHub answered")
	}
	h.release(g)
	got := h.app.store.Issues.Numbers[0]
	if i := h.base(got); i.Title != "A new thing" {
		t.Fatalf("top row %+v", i)
	}
	if h.app.top(ViewIssues).(*IssueList).sel != got {
		t.Fatal("new issue not selected")
	}
	if c := h.calls("CreateIssue")[0].Args.NewIssue; c.AddToBoard || c.Status != nil {
		t.Fatalf("from Issues without a status: %+v", c)
	}
	h.contains("✓ Created #")
}

func TestCreate_fromBoardAlwaysAddToBoard_noStatusColumn(t *testing.T) {
	h := newHarness(t, richFixture(), withStart(ViewBoard)) // focused: No status
	h.press("n")
	h.typeText("On the board")
	h.press("ctrl+s")
	c := h.calls("CreateIssue")[0].Args.NewIssue
	if !c.AddToBoard || c.Status != nil {
		t.Fatalf("from No status: %+v", c)
	}
	h.press("l", "n") // Todo
	h.typeText("In Todo")
	h.press("ctrl+s")
	c = h.calls("CreateIssue")[1].Args.NewIssue
	if !c.AddToBoard || c.Status == nil || *c.Status != "Todo" {
		t.Fatalf("from Todo: %+v", c)
	}
	cols := h.app.ctxFor(h.app.theme).Columns()
	if todo := colNums(cols, "Todo"); len(todo) != 2 {
		t.Fatalf("new card not in Todo: %v", todo)
	}
}

func TestCreate_fromIssuesAddToBoardOnlyWithStatus(t *testing.T) {
	h := newHarness(t, richFixture())
	h.press("n")
	h.typeText("With No status")
	h.press("tab", "tab", "tab", "enter") // the Status row
	h.press("k", "enter")                 // "Not on board" → "No status"
	h.press("ctrl+s")
	c := h.calls("CreateIssue")[0].Args.NewIssue
	if !c.AddToBoard || c.Status != nil {
		t.Fatalf("No status chosen: %+v", c)
	}
}

func TestCreate_partialInsertsAndToasts(t *testing.T) {
	h := newHarness(t, richFixture())
	h.press("n")
	h.typeText("Half done")
	g := h.hold("CreateIssue", 0)
	h.press("ctrl+s")
	made := domain.Issue{Number: 99, Title: "Half done", State: "open"}
	h.fail(g, &domain.Error{Kind: domain.ErrPartial, Message: "could not add it to the board", Completed: []string{"created"}, Issue: &made})
	if len(h.app.overlays) != 0 {
		t.Fatal("form still open")
	}
	if h.app.store.Issues.Numbers[0] != 99 {
		t.Fatal("partial issue not inserted")
	}
	h.contains("Created #99, but could not add it to the board")
}

func TestCreate_uncertainKeepsFormNoRetry(t *testing.T) {
	h := newHarness(t, richFixture())
	h.press("n")
	h.typeText("Maybe")
	g := h.hold("CreateIssue", 0)
	h.press("ctrl+s")
	h.fail(g, uncertainErr())
	if len(h.app.overlays) != 1 {
		t.Fatal("form closed")
	}
	h.contains("may have been created. Check GitHub")
	if n := len(h.calls("CreateIssue")); n != 1 {
		t.Fatal("create retried")
	}
}

func TestCreate_escWhileCreatingAsks(t *testing.T) {
	h := newHarness(t, richFixture())
	h.press("n")
	h.typeText("Slow")
	g := h.hold("CreateIssue", 0)
	h.press("ctrl+s", "esc")
	h.contains("The issue may still be created. Close the form anyway?")
	h.press("y")
	if len(h.app.overlays) != 0 {
		t.Fatal("form not closed")
	}
	h.release(g)
}

func TestEdit_noChangesCloses(t *testing.T) {
	h := newHarness(t, richFixture())
	h.press("e", "ctrl+s")
	h.contains("No changes")
	if n := len(h.calls("EditIssue")); n != 0 {
		t.Fatal("an unchanged edit was sent")
	}
}

func TestEdit_conflictDetected_overwrite(t *testing.T) {
	h := newHarness(t, richFixture())
	h.press("e")
	h.typeText(" (edited)")
	h.fake.Mutate(func(f *servicetest.Fixture) { f.Issues[0].Body = "Someone else's body" })
	h.press("ctrl+s")
	h.contains("changed on GitHub since you started editing")
	h.lacks("Saving…") // live dogfood: the form behind the dialog still said Saving…
	h.press("o")
	calls := h.calls("EditIssue")
	if len(calls) != 1 || calls[0].Args.Edit.Title == nil || *calls[0].Args.Edit.Title != "Fix flaky sync when gh is offline (edited)" || calls[0].Args.Edit.Body != nil {
		t.Fatalf("edit %+v", calls)
	}
}

func TestEdit_conflictDetected_keepEditing(t *testing.T) {
	h := newHarness(t, richFixture())
	h.press("e")
	h.typeText(" (edited)")
	h.fake.Mutate(func(f *servicetest.Fixture) { f.Issues[0].Issue.Title = "Renamed elsewhere" })
	h.press("ctrl+s", "esc")
	if n := len(h.calls("EditIssue")); n != 0 {
		t.Fatal("sent after keep editing")
	}
	f := h.app.overlays[len(h.app.overlays)-1].(*IssueForm)
	if f.saving || f.title.String() != "Fix flaky sync when gh is offline (edited)" {
		t.Fatalf("form text lost: %q saving=%v", f.title.String(), f.saving)
	}
}

func TestEdit_noConflict_sendsOnlyChangedFields(t *testing.T) {
	h := newHarness(t, richFixture())
	h.press("e")
	h.typeText("!")
	h.press("alt+s")
	e := h.calls("EditIssue")[0].Args.Edit
	if e.Title == nil || e.Body != nil {
		t.Fatalf("edit %+v", e)
	}
	h.contains("✓ Saved #6")
}

func TestEdit_crlfBodyNotSentWhenUntouched(t *testing.T) {
	fx := richFixture()
	fx.Issues[0].Body = "line one\r\nline two\r\n"
	h := newHarness(t, fx)
	h.press("e")
	h.typeText("!")
	h.press("ctrl+s")
	if e := h.calls("EditIssue")[0].Args.Edit; e.Body != nil {
		t.Fatalf("untouched CRLF body sent: %q", *e.Body)
	}
}

func TestComment_emptyRefused_successAppends(t *testing.T) {
	h := newHarness(t, richFixture())
	h.press("enter", "c", "ctrl+s")
	h.contains("Comment is empty")
	h.typeText("Looks good")
	h.press("ctrl+r", "alt+s")
	if n := len(h.calls("Comment")); n != 1 {
		t.Fatal("comment not sent")
	}
	d, _ := h.app.store.PeekDetail(6)
	if last := d.Comments[len(d.Comments)-1]; last.Body != "Looks good" {
		t.Fatalf("last comment %q", last.Body)
	}
	h.press("G")
	h.contains("Looks good")
}

func TestDelete_requiresExactNumber(t *testing.T) {
	h := newHarness(t, richFixture())
	h.press("D")
	h.typeText("5a6")
	h.contains("Type 6 to confirm: 56") // letters are ignored
	h.press("enter")
	if n := len(h.calls("Delete")); n != 0 {
		t.Fatal("deleted with the wrong number")
	}
	h.press("backspace", "backspace")
	h.typeText("6")
	h.press("enter")
	if n := len(h.calls("Delete")); n != 1 {
		t.Fatal("not deleted with the right number")
	}
}

func TestDelete_warnsWhenViewerCannotDelete(t *testing.T) {
	fx := richFixture()
	fx.Issues[0].Issue.ViewerCanDelete = false
	h := newHarness(t, fx)
	h.press("D")
	h.contains("GitHub will probably refuse")
}

func TestDelete_forbiddenStaysOpen(t *testing.T) {
	h := newHarness(t, richFixture())
	g := h.hold("Delete", 6)
	h.press("D")
	h.typeText("6")
	h.press("enter")
	h.contains("Deleting…")
	h.fail(g, &domain.Error{Kind: domain.ErrForbidden, Message: "Must have admin rights"})
	h.contains("✗ Must have admin rights")
	if len(h.app.overlays) != 1 {
		t.Fatal("box closed on error")
	}
	if _, ok := h.app.store.Issue(6); !ok {
		t.Fatal("removed although GitHub refused")
	}
}

func TestDelete_successRemovesEverywhere_popsDetail(t *testing.T) {
	h := newHarness(t, richFixture())
	h.press("enter", "D")
	h.typeText("6")
	h.press("enter")
	if _, ok := h.app.top(ViewIssues).(*IssueList); !ok {
		t.Fatal("detail of the deleted issue still open")
	}
	if slices.Contains(h.app.store.Issues.Numbers, 6) {
		t.Fatal("#6 still listed")
	}
	h.contains("✓ Deleted #6")
}

func TestPicker_assigneeMax10(t *testing.T) {
	fx := richFixture()
	for k := 0; k < 10; k++ {
		fx.Users = append(fx.Users, domain.User{Login: fmt.Sprintf("user%02d", k)})
	}
	h := newHarness(t, fx)
	h.press("a") // #6 has reviewer already; rows: @me, reviewer, octocat, user00…user09
	p := h.app.overlays[len(h.app.overlays)-1].(*Picker)
	for len(p.selected) < 10 {
		k := p.items[p.rows[p.cursor]].Key
		if !p.selected[k] {
			h.press("space")
		}
		h.press("j")
	}
	h.press("space") // the 11th is refused
	h.contains("GitHub allows at most 10 assignees")
	if len(p.selected) != 10 {
		t.Fatalf("%d selected", len(p.selected))
	}
}

func TestPicker_filterKeepsCursorByName_onMetaRefresh(t *testing.T) {
	h := newHarness(t, richFixture())
	h.press("L", "j", "j") // cursor on docs
	p := h.app.overlays[len(h.app.overlays)-1].(*Picker)
	h.send(labelsMsg{labels: []domain.Label{{Name: "docs"}, {Name: "a-new-label"}, {Name: "bug"}, {Name: "sync"}}})
	if k := p.items[p.rows[p.cursor]].Key; k != "docs" {
		t.Fatalf("cursor moved to %q", k)
	}
	h.press("/")
	h.typeText("syn")
	if len(p.rows) != 1 || p.items[p.rows[0]].Key != "sync" {
		t.Fatalf("filter rows %v", p.rows)
	}
}

func TestPicker_statusEntries_issue_form_boardForm(t *testing.T) {
	labels := func(h *harness) []string {
		p := h.app.overlays[len(h.app.overlays)-1].(*Picker)
		var out []string
		for _, it := range p.items {
			out = append(out, it.Label)
		}
		return out
	}
	h := newHarness(t, richFixture())
	h.press("M") // #6 is on the board
	if got := labels(h); fmt.Sprint(got) != "[Todo In progress Done No status Remove from board]" {
		t.Fatalf("issue picker %v", got)
	}
	h.press("esc", "n", "tab", "tab", "tab", "enter")
	if got := labels(h); fmt.Sprint(got) != "[Todo In progress Done No status Not on board]" {
		t.Fatalf("form picker %v", got)
	}
	h = newHarness(t, richFixture(), withStart(ViewBoard))
	h.press("n", "tab", "tab", "tab", "enter")
	if got := labels(h); fmt.Sprint(got) != "[Todo In progress Done No status]" {
		t.Fatalf("board form picker %v", got)
	}
}

// ---- quit ----

func TestQuit_withPendingAsks(t *testing.T) {
	h := newHarness(t, richFixture())
	g := h.hold("Close", 6)
	h.press("x", "q")
	if h.quit {
		t.Fatal("quit with a change in flight")
	}
	h.contains("1 change still sending. Quit anyway?")
	h.press("esc")
	h.release(g)
	h.press("q")
	if !h.quit {
		t.Fatal("q did not quit once nothing was pending")
	}
}

func TestQuit_ctrlcTwiceForces(t *testing.T) {
	h := newHarness(t, richFixture())
	g := h.hold("Close", 6)
	h.press("x", "ctrl+c")
	if h.quit {
		t.Fatal("first ctrl+c quit")
	}
	h.press("ctrl+c")
	if !h.quit {
		t.Fatal("second ctrl+c did not quit")
	}
	h.release(g)
}

// ---- editor ----

func TestPreview_rendersCurrentText(t *testing.T) {
	h := newHarness(t, richFixture())
	h.press("enter", "c")
	h.typeText("**strong words**")
	h.press("ctrl+r")
	h.contains("◉ preview")
	h.contains("strong words")
	h.lacks("**strong")
	h.press("ctrl+r")
	h.contains("**strong words**")
}

// editorScript writes a fake editor: it logs its arguments, one per line,
// then runs body (shell) with the file as $last.
func editorScript(t *testing.T, body string) (script, log string) {
	dir := t.TempDir()
	script, log = filepath.Join(dir, "ed.sh"), filepath.Join(dir, "args.log")
	src := "#!/bin/sh\nfor a in \"$@\"; do echo \"$a\" >> " + log + "; done\neval last=\\${$#}\n" + body + "\n"
	if err := os.WriteFile(script, []byte(src), 0o755); err != nil {
		t.Fatal(err)
	}
	return script, log
}

func TestExternalEditor_roundTrip(t *testing.T) {
	script, log := editorScript(t, `printf 'written in the editor' > "$last"`)
	h := newHarness(t, richFixture(), withEditor(script))
	h.press("enter", "c")
	h.typeText("draft")
	h.press("ctrl+o")
	f := h.app.overlays[len(h.app.overlays)-1].(*CommentForm)
	if got := f.body.Value(); got != "written in the editor" {
		t.Fatalf("body %q", got)
	}
	args, _ := os.ReadFile(log)
	if _, err := os.Stat(strings.TrimSpace(string(args))); !os.IsNotExist(err) {
		t.Fatal("temp file not removed")
	}
}

func TestExternalEditor_nonZeroExitKeepsBody_removesTemp(t *testing.T) {
	script, log := editorScript(t, `printf 'discarded' > "$last"; exit 1`)
	h := newHarness(t, richFixture(), withEditor(script))
	h.press("enter", "c")
	h.typeText("keep me")
	h.press("ctrl+o")
	f := h.app.overlays[len(h.app.overlays)-1].(*CommentForm)
	if got := f.body.Value(); got != "keep me" {
		t.Fatalf("body %q", got)
	}
	h.contains("Editor exited with an error; body unchanged")
	args, _ := os.ReadFile(log)
	if _, err := os.Stat(strings.TrimSpace(string(args))); !os.IsNotExist(err) {
		t.Fatal("temp file not removed")
	}
}

func TestExternalEditor_notOfferedWithoutEnv(t *testing.T) {
	h := newHarness(t, richFixture())
	h.press("enter", "c")
	h.lacks("$EDITOR")
	h.typeText("text")
	h.press("ctrl+o")
	if f := h.app.overlays[len(h.app.overlays)-1].(*CommentForm); f.body.Value() != "text" {
		t.Fatal("ctrl+o changed the body without an editor")
	}
}

func TestExternalEditor_argvSplitNoShell(t *testing.T) {
	script, log := editorScript(t, `true`)
	h := newHarness(t, richFixture(), withEditor(script, "--wait", "two words"))
	h.press("enter", "c", "ctrl+o")
	args, _ := os.ReadFile(log)
	lines := strings.Split(strings.TrimSpace(string(args)), "\n")
	if len(lines) != 3 || lines[0] != "--wait" || lines[1] != "two words" || !strings.HasSuffix(lines[2], ".md") {
		t.Fatalf("editor argv %q", lines)
	}
}

// ---- Phase 6 review fixes ----

// Finding 1: a create whose form was closed while it was in flight still
// lands in the store with a toast, and quitting meanwhile asks first.
func TestCreate_closedFormStillStoresResult(t *testing.T) {
	h := newHarness(t, richFixture())
	h.press("n")
	h.typeText("Created after closing")
	g := h.hold("CreateIssue", 0)
	h.press("ctrl+s", "esc", "y") // close the form while the create is in flight
	if len(h.app.overlays) != 0 {
		t.Fatal("form not closed")
	}
	h.press("q")
	if h.quit {
		t.Fatal("quit with a create in flight")
	}
	h.contains("1 change still sending. Quit anyway?")
	h.press("esc")
	h.release(g)
	got := h.app.store.Issues.Numbers[0]
	if i := h.base(got); i.Title != "Created after closing" {
		t.Fatalf("result lost: top row %+v", i)
	}
	h.contains("✓ Created #" + itoaPlain(got))
	if h.app.pendingWrites() != 0 {
		t.Fatal("write still counted after its result")
	}
}

// Finding 1: edit, comment and delete results arriving after their overlay
// closed are handled too, and each counts as a write in flight until then.
func TestWrites_orphanedEditCommentDeleteHandled(t *testing.T) {
	closeTop := func(h *harness) { h.app.closeOverlay(h.app.overlays[len(h.app.overlays)-1]) }

	h := newHarness(t, richFixture())
	h.press("e")
	h.typeText("!")
	g := h.hold("EditIssue", 6)
	h.press("ctrl+s")
	closeTop(h)
	if h.app.pendingWrites() != 1 {
		t.Fatal("edit not counted")
	}
	h.release(g)
	if h.base(6).Title != "Fix flaky sync when gh is offline!" {
		t.Fatal("orphaned edit not stored")
	}
	h.contains("✓ Saved #6")

	h = newHarness(t, richFixture())
	h.press("enter", "c")
	h.typeText("late comment")
	g = h.hold("Comment", 6)
	h.press("ctrl+s")
	closeTop(h)
	if h.app.pendingWrites() != 1 {
		t.Fatal("comment not counted")
	}
	h.release(g)
	if d, _ := h.app.store.PeekDetail(6); d.Comments[len(d.Comments)-1].Body != "late comment" {
		t.Fatal("orphaned comment not stored")
	}
	h.contains("✓ Commented on #6")

	h = newHarness(t, richFixture())
	h.press("D")
	h.typeText("6")
	g = h.hold("Delete", 6)
	h.press("enter")
	closeTop(h)
	if h.app.pendingWrites() != 1 {
		t.Fatal("delete not counted")
	}
	h.release(g)
	if _, ok := h.app.store.Issue(6); ok {
		t.Fatal("orphaned delete not applied")
	}
	h.contains("✓ Deleted #6")
}

// Finding 2: popping a detail keeps the read a held queue waits for.
func TestPending_popKeepsHeldIssueRead(t *testing.T) {
	h := newHarness(t, richFixture())
	g := h.hold("Close", 6)
	h.press("x")
	gr := h.hold("GetIssue", 6) // the re-read: replaced (cancelled) when the detail opens
	g2 := h.hold("GetIssue", 6) // the detail's own read
	h.fail(g, uncertainErr())
	h.press("enter") // open #6: its read replaces the re-read
	gr.done = true   // cancelled: no longer parked at its gate
	h.press("esc")   // pop the detail
	if !h.app.busy(Slot{Kind: SlotIssue, Number: 6}) {
		t.Fatal("pop cancelled the read the held queue waits for")
	}
	h.release(g2)
	if h.app.pending.Count() != 0 {
		t.Fatal("hold not released by the kept read")
	}
}

// Finding 2: opening a detail of a held issue still leaves a read in flight
// that releases the hold.
func TestPending_openingDetailOfHeldIssueStillReleases(t *testing.T) {
	h := newHarness(t, richFixture())
	g := h.hold("Close", 6)
	h.press("x")
	gr := h.hold("GetIssue", 6) // the re-read, replaced (cancelled) when the detail opens
	h.fail(g, uncertainErr())
	h.press("enter")
	gr.done = true // cancelled: no longer parked at its gate
	h.settle()
	if h.app.pending.Count() != 0 {
		t.Fatal("the detail's read did not release the hold")
	}
}

// Removal is judged against the issue (functional 4.1): a departed assignee
// or a deleted label the issue still has is listed, ticked, and can be removed.
func TestPicker_listsIssueValuesTheRepoNoLongerHas(t *testing.T) {
	fx := richFixture()
	fx.Users = slices.DeleteFunc(fx.Users, func(u domain.User) bool { return u.Login == "reviewer" })
	gone := fx.Issues[0].Issue.Labels[0].Name
	fx.Labels = slices.DeleteFunc(fx.Labels, func(l domain.Label) bool { return l.Name == gone })
	h := newHarness(t, fx)
	untick := func(key string) {
		p := h.app.overlays[len(h.app.overlays)-1].(*Picker)
		row := slices.IndexFunc(p.rows, func(r int) bool { return p.items[r].Key == key })
		if row < 0 || !p.selected[key] {
			t.Fatalf("%q not listed ticked", key)
		}
		for p.cursor < row {
			h.press("j")
		}
		h.press("space", "enter")
	}
	h.press("a")
	untick("reviewer")
	h.press("L")
	untick(gone)
	if c := h.calls("UpdateAssignees"); len(c) != 1 || !slices.Equal(c[0].Args.Remove, []string{"reviewer"}) {
		t.Fatalf("assignee calls %+v", c)
	}
	if c := h.calls("UpdateLabels"); len(c) != 1 || !slices.Equal(c[0].Args.Remove, []string{gone}) {
		t.Fatalf("label calls %+v", c)
	}
}

// Live dogfood: the gutter spinner stood still while a change was sending,
// because the tick chain only ran while a read was loading.
func TestSpinner_animatesWhileAChangeIsSending(t *testing.T) {
	h := newHarness(t, richFixture())
	g := h.hold("Close", 6)
	h.press("x")
	before := h.app.spin
	h.fire(spinnerTickMsg{})
	h.fire(spinnerTickMsg{})
	if h.app.spin < before+2 {
		t.Fatalf("spinner frame %d → %d while #6 is closing", before, h.app.spin)
	}
	h.release(g)
	h.fire(spinnerTickMsg{})
	if h.app.spinning {
		t.Fatal("spinner still ticking after the change settled")
	}
}

// Live dogfood: after > the focus stayed in the old column, so a second >
// moved a different card. Focus follows the card (UI 3.5).
func TestBoard_stepFocusFollowsCard(t *testing.T) {
	h := newHarness(t, richFixture(), withStart(ViewBoard))
	h.press(">", ">") // #6: No status → Todo → the next column
	calls := h.calls("SetStatus")
	if len(calls) != 2 || calls[0].Args.Number != 6 || calls[1].Args.Number != 6 {
		t.Fatalf("SetStatus calls %+v, want #6 twice", calls)
	}
	b := h.app.top(ViewBoard).(*Board)
	if b.sel != 6 || !slices.Contains(b.focused().Numbers, 6) {
		t.Fatalf("focus on #%d in %q, want #6", b.sel, b.focused().Name)
	}
}
