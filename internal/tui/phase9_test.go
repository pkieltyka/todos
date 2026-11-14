package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"testing"

	"github.com/pkieltyka/todos/internal/domain"
	"github.com/pkieltyka/todos/internal/service/servicetest"
)

// Live dogfood, Phase 9 ruling 1: GitHub project workflows act a few seconds
// after our write, so creates and board changes are re-read once, 5 s later.

func getIssues(h *harness, n int) int {
	k := 0
	for _, c := range h.calls("GetIssue") {
		if c.Args.Number == n {
			k++
		}
	}
	return k
}

func rereadDelays(h *harness) int {
	k := 0
	for i, m := range h.timers {
		if _, ok := m.(rereadMsg); ok && h.delays[i] == 5*time.Second {
			k++
		}
	}
	return k
}

func mutateIssue(h *harness, n int, fn func(*domain.Issue)) {
	h.fake.Mutate(func(f *servicetest.Fixture) {
		for i := range f.Issues {
			if f.Issues[i].Issue.Number == n {
				fn(&f.Issues[i].Issue)
			}
		}
	})
}

func TestReread_afterCreateShowsWorkflowPlacement(t *testing.T) {
	h := newHarness(t, richFixture())
	h.press("n")
	h.typeText("New one")
	h.press("ctrl+s")
	if rereadDelays(h) != 1 {
		t.Fatal("no 5 s re-read scheduled after the create")
	}
	before := getIssues(h, 104)
	backlog := "Todo"
	mutateIssue(h, 104, func(i *domain.Issue) { i.Board = &domain.Placement{Status: &backlog} }) // the auto-add workflow
	h.advance(rereadAfter)
	h.fire(rereadMsg{})
	if getIssues(h, 104) != before+1 {
		t.Fatal("the created issue was not re-read")
	}
	if i, _ := h.app.store.Issue(104); i.Board == nil || i.Board.Status == nil || *i.Board.Status != "Todo" {
		t.Fatalf("workflow placement not shown: %+v", i.Board)
	}
}

func TestReread_afterStatusChangeShowsWorkflowClose(t *testing.T) {
	h := newHarness(t, richFixture(), withStart(ViewBoard))
	h.press(">") // #6 → Todo
	mutateIssue(h, 6, func(i *domain.Issue) { i.State = "closed" })
	h.advance(rereadAfter)
	h.fire(rereadMsg{})
	if i, _ := h.app.store.Issue(6); i.State != "closed" {
		t.Fatal("the workflow's close was not picked up")
	}
}

func TestReread_notAfterOtherChanges(t *testing.T) {
	h := newHarness(t, richFixture())
	h.press("x")
	if rereadDelays(h) != 0 {
		t.Fatal("a close scheduled a re-read")
	}
}

func TestReread_newerChangeRestartsTimer_oneRead(t *testing.T) {
	h := newHarness(t, richFixture(), withStart(ViewBoard))
	h.press(">", ">") // two status changes on #6
	before := getIssues(h, 6)
	h.fire(rereadMsg{})
	if n := getIssues(h, 6) - before; n != 1 {
		t.Fatalf("%d re-reads, want 1 (the first timer was restarted)", n)
	}
}

func TestReread_skippedWhilePending_nextSettleSchedules(t *testing.T) {
	h := newHarness(t, richFixture(), withStart(ViewBoard))
	h.press(">")
	g := h.hold("Close", 6)
	h.press("x") // pending when the timer fires
	before := getIssues(h, 6)
	h.fire(rereadMsg{})
	if getIssues(h, 6) != before {
		t.Fatal("re-read while a change was pending")
	}
	h.release(g) // the close settles: the owed re-read is scheduled again
	h.fire(rereadMsg{})
	if getIssues(h, 6) != before+1 {
		t.Fatal("owed re-read not scheduled by the next settle")
	}
}

func TestReread_cancelledOnDeleteAndQuit(t *testing.T) {
	h := newHarness(t, richFixture(), withStart(ViewBoard))
	h.press(">")
	h.app.deleted(6)
	before := len(h.calls("GetIssue"))
	h.fire(rereadMsg{})
	if len(h.calls("GetIssue")) != before {
		t.Fatal("re-read a deleted issue")
	}

	h = newHarness(t, richFixture(), withStart(ViewBoard))
	h.press(">", "q")
	if !h.quit || len(h.app.rr.due) != 0 {
		t.Fatalf("quit=%v timers=%v", h.quit, h.app.rr.due)
	}
}

// Phase 9 ruling 2: wording.
func TestWording_justNowAndNotPlannedToast(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	if got := agoPhrase(now, now.Add(-30*time.Second)); got != "just now" {
		t.Fatalf("30 s: %q", got)
	}
	if got := agoPhrase(now, now.Add(-90*time.Second)); got != "1m ago" {
		t.Fatalf("90 s: %q", got)
	}
	if got := ago(now, now); got != "now" {
		t.Fatalf("list cell: %q", got)
	}
	h := newHarness(t, richFixture())
	h.press("X")
	h.contains("✓ Closed #6 as not planned · x to undo")
	h.lacks("0s ago")
}

// Phase 9 polish 3.1: at 80 columns a long label used to be cut
// ("enhanceme…") while the assignee column beside it held only "—".
func TestIssueList_labelsBorrowUnusedAssigneeRoom(t *testing.T) {
	fx := richFixture()
	fx.Issues[0].Issue.Labels = []domain.Label{{Name: "enhancement", Color: "a2eeef"}}
	fx.Issues[0].Issue.Assignees = nil
	fx.Issues[1].Issue.Labels = []domain.Label{{Name: "documentation", Color: "0075ca"}}
	h := newHarness(t, fx)
	h.size(80, 24)
	h.contains("enhancement         — 1h") // the "—" moves to the column's right edge
	h.contains("documenta… @pkieltyka")    // a full assignee leaves no room: unchanged
	if strings.Contains(h.view(), "enhanceme…") {
		t.Fatal("label still cut although the assignee column had room")
	}
}

// Phase 9 polish 3.2: "…N more" is the column's last row, with no blank rows
// below it.
func TestBoard_moreLineIsLastRow(t *testing.T) {
	fx := richFixture()
	todo := fx.Board.Columns[1]
	for k := 0; k < 12; k++ { // more Todo cards than fit at 80x24
		d := fx.Issues[0]
		d.Issue.Number = 200 + k
		d.Issue.Board = &domain.Placement{ItemID: domain.NodeID(fmt.Sprintf("ITEM_%d", 200+k)), Status: todo.Status, OptionID: todo.OptionID}
		fx.Issues = append(fx.Issues, d)
		fx.Board.Columns[1].Cards = append(fx.Board.Columns[1].Cards, domain.Card{Issue: d.Issue})
	}
	h := newHarness(t, fx, withStart(ViewBoard))
	h.size(80, 24)
	lines := strings.Split(h.view(), "\n")
	at := -1
	for k, l := range lines {
		if strings.Contains(l, " more") && strings.Contains(l, "…") {
			at = k
		}
	}
	if at < 0 {
		t.Fatal("no …N more line")
	}
	if !strings.ContainsAny(lines[at+1], "└┗") {
		t.Fatalf("row below …N more is %q, want the column's bottom border", lines[at+1])
	}
}

// Live quick add (Phase 9 section 2): the header counted a skipped done item
// ("creating 2 of 5" for 4 issues).
func TestQuickAdd_runningHeaderCountsOnlyIssuesToCreate(t *testing.T) {
	h := newHarness(t, richFixture(), withDrafts(newMemDrafts()))
	h.press("A")
	h.paste("- [x] Done already\n- Alpha\n- Bravo\n- Charlie\n")
	g := h.hold("RunQuickAdd.item", 2) // the done item and Alpha are settled; Bravo waits
	h.press("ctrl+s", "enter")
	h.contains("creating 2 of 3")
	h.release(g)
}

// Live quick add: a stop the user asked for read "Quick add stopped:
// operation interrupted." The real service reports it as a cancelled context.
func TestQuickAdd_userStopSaysAsYouAsked(t *testing.T) {
	lines := stopBanner(domain.QuickAddResult{}, &domain.Error{Kind: domain.ErrNetwork, Message: "operation interrupted"}, true, []int{29, 30}, nil, nil)
	if lines[0] != "✗ Quick add stopped as you asked." {
		t.Fatalf("banner %q", lines[0])
	}
	h := newHarness(t, richFixture(), withDrafts(newMemDrafts()))
	h.press("A")
	h.paste(threeItems)
	g := h.hold("CreateIssue", 0)
	h.press("ctrl+s", "enter", "esc")
	h.release(g)
	h.contains("Quick add stopped as you asked.")
}

// Live quick add: after a crash the flagged item was flagged in the write
// step but not in the preview, where enter sends it.
func TestQuickAdd_previewKeepsMayExistFlag(t *testing.T) {
	store := newMemDrafts()
	text := "- Alpha\n- Bravo\n"
	store.m["github.com/pkieltyka/todos"] = domain.Draft{RepoKey: "github.com/pkieltyka/todos", Text: text,
		InFlight: &domain.DraftSpan{StartByte: 0, EndByte: 8, SourceSHA256: sourceHash("- Alpha\n")}}
	h := newHarness(t, richFixture(), withDrafts(store))
	h.press("A", "ctrl+s")
	h.contains("Alpha")
	for _, l := range strings.Split(h.view(), "\n") {
		if strings.Contains(l, "Bravo") && strings.Contains(l, "may exist") {
			t.Fatal("unflagged item shown as may exist")
		}
		if strings.Contains(l, "1  Alpha") && !strings.Contains(l, "⚠ may exist") {
			t.Fatalf("flag missing in the preview: %q", l)
		}
	}
}

// Review finding 1 (Phase 9): focus follows the focused card wherever it now
// is; only a card gone from the board falls back to a neighbour.

func boardScreen(h *harness) *Board { return h.app.top(ViewBoard).(*Board) }

func TestBoard_rejectedStepFocusStaysOnCard_secondPressSameCard(t *testing.T) {
	h := newHarness(t, richFixture(), withStart(ViewBoard))
	g := h.hold("SetStatus", 6)
	h.press(">") // #6: No status → Todo, optimistically; focus follows
	h.fail(g, &domain.Error{Kind: domain.ErrForbidden, Message: "not allowed"})
	b := boardScreen(h)
	if b.sel != 6 || b.focused().Status != nil {
		t.Fatalf("after the rejection focus is on #%d in %q, want #6 back in No status", b.sel, b.focused().Name)
	}
	h.press(">")
	calls := h.calls("SetStatus")
	if len(calls) != 2 || calls[1].Args.Number != 6 {
		t.Fatalf("second press moved %+v, want #6 again", calls)
	}
}

func TestBoard_refreshMovesFocusedCard_focusFollows(t *testing.T) {
	h := newHarness(t, richFixture(), withStart(ViewBoard))
	h.press("l") // Todo, #5
	if b := boardScreen(h); b.sel != 5 {
		t.Fatalf("setup: focus on #%d", b.sel)
	}
	progress := "In progress"
	if _, err := h.fake.SetStatus(context.Background(), 5, &progress); err != nil { // moved on GitHub
		t.Fatal(err)
	}
	h.press("r")
	b := boardScreen(h)
	if b.sel != 5 || b.focused().Name != "In progress" {
		t.Fatalf("focus on #%d in %q, want #5 in In progress", b.sel, b.focused().Name)
	}
}

func TestBoard_cardRemovedFromBoard_focusFallsBackToNeighbour(t *testing.T) {
	h := newHarness(t, richFixture(), withStart(ViewBoard))
	todo := "Todo"
	if _, err := h.fake.SetStatus(context.Background(), 4, &todo); err != nil {
		t.Fatal(err)
	}
	h.press("r", "l", "j") // Todo: #5, #4; focus #4
	if b := boardScreen(h); b.sel != 4 {
		t.Fatalf("setup: focus on #%d", b.sel)
	}
	if _, err := h.fake.RemoveFromBoard(context.Background(), 4); err != nil {
		t.Fatal(err)
	}
	h.press("r")
	b := boardScreen(h)
	if b.focused().Name != "Todo" || b.sel != 5 {
		t.Fatalf("focus on #%d in %q, want the neighbour #5 in Todo", b.sel, b.focused().Name)
	}
}

func TestBoard_focusCanRestOnEmptyColumn(t *testing.T) {
	h := newHarness(t, richFixture(), withStart(ViewBoard))
	h.press("l", "l", "l") // to Done, which is empty
	b := boardScreen(h)
	if b.focused().Name != "Done" || b.sel != 0 {
		t.Fatalf("focus on #%d in %q, want the empty Done column", b.sel, b.focused().Name)
	}
}
