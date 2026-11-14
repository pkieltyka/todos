package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/pkieltyka/todos/internal/domain"
	"github.com/pkieltyka/todos/internal/service/servicetest"
)

func bigFixture(n int) servicetest.Fixture {
	fx := richFixture()
	fx.Issues = servicetest.GenIssues(n)
	fx.Board.Columns = fx.Board.Columns[:0]
	return fx
}

// ---- progressive loading (tui.md 7.3) ----

func TestIssues_firstPageRendersBeforeBackground(t *testing.T) {
	h := newHarness(t, bigFixture(1204), withLazy())
	g1 := h.hold("ListIssues", 0)
	g2 := h.hold("ListIssues", 0)
	h.boot()
	h.release(g1) // page 1 lands; page 2 is held
	if n := len(h.app.store.Issues.Numbers); n != 100 {
		t.Fatalf("%d rows after the first page", n)
	}
	h.contains("#1204")
	h.contains("loading 100/500…")
	h.release(g2)
	if n := len(h.app.store.Issues.Numbers); n != 500 {
		t.Fatalf("%d rows after background loading", n)
	}
}

func TestIssues_stopsAt500_showsTruncated(t *testing.T) {
	h := newHarness(t, bigFixture(1204))
	if n := len(h.app.store.Issues.Numbers); n != 500 {
		t.Fatalf("%d rows", n)
	}
	if n := len(h.calls("ListIssues")); n != 5 {
		t.Fatalf("%d pages requested", n)
	}
	for _, c := range h.calls("ListIssues") {
		if c.Args.Page.Size != 100 || c.Args.Filter.Search != "" {
			t.Fatalf("page request %+v", c.Args)
		}
	}
	h.contains("500 of 1,204 · list cut off")
}

func TestIssues_stateChangeCancelsChain_dropsLatePages(t *testing.T) {
	h := newHarness(t, bigFixture(300))
	g := h.hold("ListIssues", 0)
	h.press("r") // a refresh chain starts and its first page is held
	h.press("s") // the state filter changes: the chain is cancelled
	h.release(g)
	if q := h.app.store.Issues.Query.State; q != "closed" {
		t.Fatalf("query %q", q)
	}
	if n := len(h.app.store.Issues.Numbers); n != 0 {
		t.Fatalf("%d rows from the cancelled open-issues refresh", n)
	}
	h.contains("No closed issues.")
}

// Finding 14: a successful page from a superseded request with the SAME
// query must not touch the store. Only the request ID tells it apart.
func TestIssues_staleSameQueryPageDropped(t *testing.T) {
	h := newHarness(t, richFixture())
	slot := Slot{Kind: SlotIssues}
	g1 := h.hold("ListIssues", 0)
	h.press("r")
	old := h.app.inflight[slot].ID
	h.press("s", "s") // open → closed → all
	g2 := h.hold("ListIssues", 0)
	h.press("s") // → open again: the same query as the old request, held live
	if h.app.inflight[slot].ID == old || h.app.store.Issues.Query.State != "open" {
		t.Fatal("setup: no newer same-query request")
	}
	before := append([]int{}, h.app.store.Issues.Numbers...)
	stale, _ := h.app.store.Issue(6)
	stale.Title = "STALE TITLE"
	h.send(issuesPageMsg{req: old, sent: h.now, q: IssueQuery{State: "open"},
		page: domain.Page[domain.Issue]{Items: []domain.Issue{stale}, Total: 1}})
	if i, _ := h.app.store.Issue(6); i.Title == "STALE TITLE" {
		t.Fatal("stale page changed an issue")
	}
	if fmt.Sprint(h.app.store.Issues.Numbers) != fmt.Sprint(before) {
		t.Fatalf("stale page changed the rows: %v → %v", before, h.app.store.Issues.Numbers)
	}
	h.release(g2)
	h.release(g1)
}

func TestIssues_mineIsServerQuery(t *testing.T) {
	h := newHarness(t, richFixture())
	h.press("m")
	calls := h.calls("ListIssues")
	if last := calls[len(calls)-1]; last.Args.Filter.Assignee != "@me" {
		t.Fatalf("filter %+v", last.Args.Filter)
	}
	got := h.app.store.Issues.Numbers
	if len(got) != 2 || got[0] != 5 || got[1] != 1 {
		t.Fatalf("mine = %v", got)
	}
	h.contains("mine: on")
}

func TestIssues_pageOverlapKeepsRow(t *testing.T) {
	h := newHarness(t, bigFixture(300), withLazy())
	g1 := h.hold("ListIssues", 0)
	g2 := h.hold("ListIssues", 0)
	h.boot()
	h.release(g1)
	// #250 is updated on GitHub between pages: it jumps to the front, and
	// page 2 starts one row early, repeating a row already shown.
	h.fake.Mutate(func(f *servicetest.Fixture) {
		for i := range f.Issues {
			if f.Issues[i].Issue.Number == 250 {
				f.Issues[i].Issue.UpdatedAt = fixtureNow
			}
		}
	})
	before := append([]int{}, h.app.store.Issues.Numbers...)
	h.release(g2)
	nums := h.app.store.Issues.Numbers
	seen := map[int]bool{}
	for _, n := range nums {
		if seen[n] {
			t.Fatalf("#%d appears twice", n)
		}
		seen[n] = true
	}
	for i, n := range before {
		if nums[i] != n {
			t.Fatalf("row %d moved from #%d to #%d", i, n, nums[i])
		}
	}
}

func TestIssues_refreshBuildsShadowAndSwaps(t *testing.T) {
	h := newHarness(t, bigFixture(250))
	g := h.hold("ListIssues", 0)
	h.advance(time.Minute)
	h.press("r")
	if n := len(h.app.store.Issues.Numbers); n != 250 {
		t.Fatalf("rows dropped to %d while refreshing", n)
	}
	h.contains("syncing…")
	h.fake.Mutate(func(f *servicetest.Fixture) { f.Issues = f.Issues[1:] }) // #250 was closed elsewhere
	h.release(g)
	if n := len(h.app.store.Issues.Numbers); n != 249 {
		t.Fatalf("%d rows after the swap", n)
	}
	if !h.app.store.Issues.LoadedAt.Equal(h.now) {
		t.Fatal("LoadedAt not updated")
	}
	h.contains("synced just now")
}

func TestIssues_refreshFailureKeepsRowsMarksStale(t *testing.T) {
	h := newHarness(t, richFixture())
	g := h.hold("ListIssues", 0)
	h.advance(4 * time.Minute)
	h.press("r")
	h.fail(g, &domain.Error{Kind: domain.ErrNetwork, Message: "network unreachable"})
	if n := len(h.app.store.Issues.Numbers); n != 4 {
		t.Fatalf("%d rows", n)
	}
	h.contains("⚠ stale · 4m ago")
	h.contains("⚠ Refresh failed: network unreachable. Showing data from 4m ago · r retry")
}

func TestIssues_failedStateShowsHint(t *testing.T) {
	h := newHarness(t, richFixture(), withLazy())
	g := h.hold("ListIssues", 0)
	h.boot()
	h.fail(g, &domain.Error{Kind: domain.ErrAuth, Message: "not authenticated"})
	h.contains("✗ Couldn't load issues: not authenticated.")
	h.contains("Run: gh auth login")
	h.contains("✗ offline")
}

func TestPRs_mineIsLocal_showsMatchAndLoadedCounts(t *testing.T) {
	fx := richFixture()
	for i := range fx.PRs {
		fx.PRs[i].PullRequest.State = "open"
	}
	fx.PRs[1].PullRequest.Author = ptr("octocat")
	h := newHarness(t, fx, withStart(ViewPRs))
	before := len(h.calls("ListPRs"))
	h.press("m")
	if len(h.calls("ListPRs")) != before {
		t.Fatal("mine asked GitHub")
	}
	h.contains("2 mine · 3 loaded of 3")
	h.lacks("#102")
}

func TestPRs_stateCyclesOpenClosedMergedAll(t *testing.T) {
	h := newHarness(t, richFixture(), withStart(ViewPRs))
	for _, want := range []string{"closed", "merged", "all", "open"} {
		h.press("s")
		calls := h.calls("ListPRs")
		if got := calls[len(calls)-1].Args.PRFilter.State; got != want {
			t.Fatalf("state %q, want %q", got, want)
		}
	}
}

func TestBoard_mineIsLocal(t *testing.T) {
	h := newHarness(t, richFixture(), withStart(ViewBoard))
	before := len(h.calls("Board"))
	h.press("m")
	if len(h.calls("Board")) != before {
		t.Fatal("mine asked GitHub")
	}
	h.contains("1 of 3 cards")
	h.contains("#5 Issue 5")
	h.lacks("#4 Issue 4")
}

// ---- details ----

func TestDetail_bodyBeforeComments_commentsCappedAt500(t *testing.T) {
	fx := richFixture()
	var cs []domain.Comment
	for i := 0; i < 812; i++ {
		cs = append(cs, domain.Comment{ID: fmt.Sprint(i), Author: ptr("reviewer"), CreatedAt: fixtureNow.Add(time.Duration(i-900) * time.Minute), Body: fmt.Sprintf("comment %d", i)})
	}
	fx.IssueComments[6] = cs
	h := newHarness(t, fx, withLazy())
	h.boot()
	g := h.hold("ListIssueComments", 6)
	h.press("enter")
	h.contains("can't reach the network the refresh spinner never stops.")
	h.contains("Loading comments…")
	h.release(g)
	d, _ := h.app.store.PeekDetail(6)
	if len(d.Comments) != 500 || d.CommentsTotal != 812 {
		t.Fatalf("%d of %d comments", len(d.Comments), d.CommentsTotal)
	}
	h.press("G")
	h.contains("Showing 500 of 812 comments. o opens the issue in the browser.")
}

func TestDetail_popCancelsSlots(t *testing.T) {
	h := newHarness(t, richFixture())
	g := h.hold("GetIssue", 6)
	h.press("enter")
	if !h.app.busy(Slot{Kind: SlotIssue, Number: 6}) {
		t.Fatal("detail not loading")
	}
	h.press("esc")
	if h.app.busy(Slot{Kind: SlotIssue, Number: 6}) || h.app.busy(Slot{Kind: SlotIssueComments, Number: 6}) {
		t.Fatal("slots not cancelled on pop")
	}
	h.release(g)
	if d, _ := h.app.store.PeekDetail(6); d.Loaded {
		t.Fatal("cancelled result was applied")
	}
}

// Finding 14: a successful result of a request superseded by a pop, arriving
// while a newer request for the same issue is live, must not touch the store.
func TestDetail_stalePopResultDropped(t *testing.T) {
	h := newHarness(t, richFixture())
	slot := Slot{Kind: SlotIssue, Number: 6}
	g1 := h.hold("GetIssue", 6)
	h.press("enter")
	old := h.app.inflight[slot].ID
	h.press("esc")
	g2 := h.hold("GetIssue", 6)
	h.press("enter") // reopened: a newer request for #6 is live
	if r, ok := h.app.inflight[slot]; !ok || r.ID == old {
		t.Fatal("setup: no newer request")
	}
	h.send(issueDetailMsg{req: old, sent: h.now, number: 6, reason: ReasonOpen,
		detail: domain.IssueDetail{Issue: domain.Issue{Number: 6, Title: "STALE TITLE"}, Body: "STALE BODY"}})
	if i, _ := h.app.store.Issue(6); i.Title == "STALE TITLE" {
		t.Fatal("stale result changed the issue")
	}
	if d, _ := h.app.store.PeekDetail(6); d.Loaded || d.Body == "STALE BODY" {
		t.Fatal("stale result filled the detail")
	}
	h.release(g2)
	h.release(g1)
}

func TestDetail_commentsJump(t *testing.T) {
	fx := richFixture()
	fx.Issues[0].Body = strings.Repeat("line\n\n", 30)
	for i := 0; i < 12; i++ {
		fx.IssueComments[6] = append(fx.IssueComments[6], domain.Comment{ID: fmt.Sprint("x", i), Author: ptr("octocat"),
			CreatedAt: fixtureNow.Add(time.Duration(i-20) * time.Minute), Body: "more"})
	}
	h := newHarness(t, fx)
	h.press("enter", "]")
	top := strings.Split(h.view(), "\n")[6] // first viewport line
	if !strings.Contains(top, "── @reviewer") {
		t.Fatalf("] did not jump to the first comment: %q", top)
	}
	h.press("[")
	if h.app.top(ViewIssues).(*IssueDetail).vp.YOffset() != 0 {
		t.Fatal("[ did not go back to the top")
	}
}

func TestSidePanel_at119Stacked_at120Panel(t *testing.T) {
	h := newHarness(t, richFixture())
	h.press("enter")
	h.size(119, 30)
	h.contains("Assignees @reviewer  Labels")
	h.lacks("│ Author")
	h.size(120, 30)
	h.contains("│ Author     @pkieltyka")
	h.contains("Fix flaky sync when gh is offline") // the title moves into the sub-bar
}

func TestPRDetail_readOnly(t *testing.T) {
	h := newHarness(t, richFixture(), withStart(ViewPRs))
	h.press("enter")
	h.contains("wants to merge feature/core → main")
	h.contains("Closes #6")
	for _, a := range h.app.top(ViewPRs).Actions(h.app.ctxFor(h.app.theme)) {
		if a >= ActNew && a <= ActDelete {
			t.Fatalf("issue action %d enabled in PR detail", a)
		}
	}
}

// ---- board setup (UI 3.16, tui.md 4.2) ----

func TestBoardSetup_stateByCandidateCount(t *testing.T) {
	p := domain.Project{Title: "p"}
	cases := []struct {
		err  *domain.Error
		want SetupState
	}{
		{&domain.Error{Kind: domain.ErrNoBoard}, SetupNoBoard},
		{&domain.Error{Kind: domain.ErrNoBoard, Candidates: []domain.Project{p}}, SetupNoStatusField},
		{&domain.Error{Kind: domain.ErrNoBoard, Candidates: []domain.Project{p, p}}, SetupSeveral},
		{&domain.Error{Kind: domain.ErrMissingScope}, SetupMissingScope},
		{&domain.Error{Kind: domain.ErrNetwork}, SetupFailed},
	}
	for _, c := range cases {
		if got := setupStateFor(c.err); got != c.want {
			t.Errorf("%+v → %d, want %d", c.err, got, c.want)
		}
	}
}

func unlinkedFixture() servicetest.Fixture {
	fx := richFixture()
	fx.BoardInfo.Project = domain.Project{}
	fx.Projects[0].Linked = false
	return fx
}

func TestBoard_setupStates_noBoard_several_missingScope_noStatusField(t *testing.T) {
	h := newHarness(t, unlinkedFixture(), withStart(ViewBoard))
	h.contains("No project board is linked to pkieltyka/todos.")
	h.contains("P choose from your projects")

	fx := richFixture()
	fx.BoardInfo.Project = domain.Project{}
	second := fx.Projects[0]
	second.Title, second.NodeID, second.Ref.Number = "Roadmap", "PROJECT_2", 2
	fx.Projects = append(fx.Projects, second)
	h = newHarness(t, fx, withStart(ViewBoard))
	h.contains("Several projects are linked to pkieltyka/todos. Pick one:")
	h.contains("Roadmap (#2)")

	fx = richFixture()
	fx.BoardInfo.StatusFieldID = ""
	h = newHarness(t, fx, withStart(ViewBoard))
	h.contains(`Project "todos" (#1) has no "Status" field, so it can't be shown as a board.`)

	h = newHarness(t, richFixture(), withStart(ViewBoard), withLazy())
	g := h.hold("BoardInfo", 0)
	h.boot()
	h.fail(g, &domain.Error{Kind: domain.ErrMissingScope, Message: "missing project scope"})
	h.contains(`The board needs the "project" permission on your GitHub token.`)
	h.contains("gh auth refresh -s project")
}

func TestBoardSetup_PChoosesProjectInNoBoardAndNoStatusField(t *testing.T) {
	h := newHarness(t, unlinkedFixture(), withStart(ViewBoard))
	h.press("P")
	h.contains("Choose a project for pkieltyka/todos:")
	h.contains("todos (#1)")
	h.press("enter")
	if _, ok := h.app.top(ViewBoard).(*Board); !ok {
		t.Fatalf("board not shown after choosing: %T", h.app.top(ViewBoard))
	}
	h.contains("#5 Issue 5")

	fx := richFixture()
	fx.BoardInfo.StatusFieldID = ""
	h = newHarness(t, fx, withStart(ViewBoard))
	h.press("P")
	h.contains("Choose a project for pkieltyka/todos:")
	h.press("esc")
	h.contains(`has no "Status" field`)
}

func TestBoard_refreshOnSetupCallsRefreshMetadataFirst(t *testing.T) {
	h := newHarness(t, richFixture(), withStart(ViewBoard), withLazy())
	g := h.hold("BoardInfo", 0)
	h.boot()
	h.fail(g, &domain.Error{Kind: domain.ErrMissingScope, Message: "missing project scope"})
	h.press("r")
	var order []string
	for _, c := range h.fake.Calls() {
		switch c.Method {
		case "RefreshMetadata", "BoardInfo", "Board":
			order = append(order, c.Method)
		}
	}
	want := []string{"BoardInfo", "RefreshMetadata", "BoardInfo", "Board"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("calls %v, want %v", order, want)
	}
	h.contains("#5 Issue 5")
}

func TestBoard_chooseProjectReloadsInfoThenBoard(t *testing.T) {
	fx := richFixture()
	fx.BoardInfo.Project = domain.Project{}
	second := fx.Projects[0]
	second.Title, second.NodeID, second.Ref.Number = "Roadmap", "PROJECT_2", 2
	fx.Projects = append(fx.Projects, second)
	h := newHarness(t, fx, withStart(ViewBoard))
	h.press("enter") // the first candidate
	var order []string
	for _, c := range h.fake.Calls() {
		switch c.Method {
		case "UseBoard", "BoardInfo", "Board":
			order = append(order, c.Method)
		}
	}
	if strings.Join(order, ",") != "BoardInfo,UseBoard,BoardInfo,Board" {
		t.Fatalf("calls %v", order)
	}
	h.contains("Board: todos (#1)")
	h.contains("Using project todos")
}

func TestBoard_openCardAndBack(t *testing.T) {
	h := newHarness(t, richFixture(), withStart(ViewBoard))
	h.press("l", "l") // No status → Todo → In progress
	h.press("enter")
	h.contains("‹ Board  #4")
	h.press("esc")
	if s, ok := h.app.top(ViewBoard).(*Board); !ok || s.sel != 4 {
		t.Fatal("focus not kept on the card")
	}
}

func TestBoard_hiddenInfoToast(t *testing.T) {
	h := newHarness(t, richFixture(), withStart(ViewBoard))
	h.contains("2 hidden ⓘ")
	h.press("i")
	h.contains("2 items on this board are not an issue of pkieltyka/todos")
}

// ---- auto-refresh (tui.md 7.7) ----

func TestAutoRefresh_onlyVisibleView(t *testing.T) {
	h := newHarness(t, richFixture())
	issues, prs, board := len(h.calls("ListIssues")), len(h.calls("ListPRs")), len(h.calls("Board"))
	h.advance(61 * time.Second)
	h.fire(autoRefreshMsg{})
	if len(h.calls("ListIssues")) != issues+1 {
		t.Fatal("visible view not refreshed")
	}
	if len(h.calls("ListPRs")) != prs || len(h.calls("Board")) != board {
		t.Fatal("hidden views refreshed")
	}
}

func TestAutoRefresh_skipsWithOverlay(t *testing.T) {
	h := newHarness(t, richFixture())
	n := len(h.calls("ListIssues"))
	h.press("?")
	h.advance(61 * time.Second)
	h.fire(autoRefreshMsg{})
	if len(h.calls("ListIssues")) != n {
		t.Fatal("refreshed under an overlay")
	}
}

func TestAutoRefresh_skipsUnder10Percent_headerShowsPaused(t *testing.T) {
	h := newHarness(t, richFixture())
	n := len(h.calls("ListIssues"))
	h.fake.SetRateLimit(domain.RateLimit{Known: true, Limit: 5000, Remaining: 200})
	h.advance(61 * time.Second)
	h.fire(autoRefreshMsg{})
	if len(h.calls("ListIssues")) != n {
		t.Fatal("refreshed with 4% left")
	}
	h.contains("⏸ auto-refresh paused · 4% left")
	h.press("r") // manual refresh still works
	if len(h.calls("ListIssues")) != n+1 {
		t.Fatal("manual refresh blocked")
	}
}

func TestAutoRefresh_skipsInFlight(t *testing.T) {
	h := newHarness(t, richFixture())
	g := h.hold("ListIssues", 0)
	h.press("r")
	n := len(h.calls("ListIssues"))
	h.advance(61 * time.Second)
	h.fire(autoRefreshMsg{})
	if len(h.calls("ListIssues")) != n {
		t.Fatal("second refresh started while one is in flight")
	}
	h.release(g)
}

func TestAutoRefresh_skipsRecent(t *testing.T) {
	h := newHarness(t, richFixture())
	n := len(h.calls("ListIssues"))
	h.advance(20 * time.Second)
	h.fire(autoRefreshMsg{})
	if len(h.calls("ListIssues")) != n {
		t.Fatal("refreshed data loaded 20s ago")
	}
}

func TestAutoRefresh_hiddenTabRefreshesOnSwitchWhenOld(t *testing.T) {
	h := newHarness(t, richFixture())
	h.press("2")
	n := len(h.calls("ListPRs"))
	h.press("1")
	h.advance(30 * time.Second)
	h.press("2")
	if len(h.calls("ListPRs")) != n {
		t.Fatal("fresh tab refreshed on switch")
	}
	h.press("1")
	h.advance(61 * time.Second)
	h.press("2")
	if len(h.calls("ListPRs")) != n+1 {
		t.Fatal("old tab not refreshed on switch")
	}
}

// ---- toasts, browser, too small ----

func TestCopyURL_toastsAndExpires(t *testing.T) {
	h := newHarness(t, richFixture())
	h.press("y")
	h.contains("✓ Copied https://github.com/pkieltyka/todos/issues/6")
	h.fire(toastExpireMsg{})
	h.lacks("Copied")
}

func TestOpenInBrowser(t *testing.T) {
	h := newHarness(t, richFixture())
	h.press("j", "o")
	if len(h.opened) != 1 || h.opened[0] != "https://github.com/pkieltyka/todos/issues/5" {
		t.Fatalf("opened %v", h.opened)
	}
}

func TestErrorToast_staysUntilNextKey(t *testing.T) {
	h := newHarness(t, richFixture())
	h.send(h.app.toastErr("Couldn't do it"))
	h.app.toastErr("Couldn't do it")
	h.send(nudge{})
	h.contains("✗ Couldn't do it")
	h.fire(toastExpireMsg{})
	h.contains("✗ Couldn't do it")
	h.press("j")
	h.lacks("Couldn't do it")
}

func TestTooSmall_keepsStateAndRecovers(t *testing.T) {
	h := newHarness(t, richFixture())
	h.press("j", "j")
	h.size(79, 24)
	h.contains("todos needs at least 80×24.")
	h.contains("This terminal is 79×24.")
	h.press("j", "enter")
	h.size(80, 24)
	if s := h.app.top(ViewIssues).(*IssueList); s.sel != 4 {
		t.Fatalf("selection %d, want #4", s.sel)
	}
	h.contains("3/4")
}

// Dogfood (Phase 4): the side panel's state fits its 32 cells.
func TestSidePanel_notPlannedStateFits(t *testing.T) {
	h := newHarness(t, richFixture())
	h.press("s", "j") // closed: #3 (completed), then #2 (not planned)
	h.size(140, 40)
	h.press("enter")
	h.contains("│ State      ⊘ Not planned")
	h.lacks("pla…")
}

// Dogfood (Phase 4): Bubble Tea redraws only on messages, so a clock tick
// keeps "synced 2m ago" moving while idle. It is scheduled at start and
// re-scheduled by every tick.
func TestClock_ticksKeepRelativeTimesMoving(t *testing.T) {
	h := newHarness(t, richFixture())
	pending := func() int {
		h.mu.Lock()
		defer h.mu.Unlock()
		n := 0
		for _, m := range h.timers {
			if _, ok := m.(clockTickMsg); ok {
				n++
			}
		}
		return n
	}
	if pending() != 1 {
		t.Fatalf("%d clock ticks scheduled at start", pending())
	}
	h.advance(65 * time.Second)
	h.fire(clockTickMsg{})
	if pending() != 1 {
		t.Fatal("clock tick not re-scheduled")
	}
	h.contains("synced 1m ago")
}

func (h *harness) pendingClock() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, m := range h.timers {
		if _, ok := m.(clockTickMsg); ok {
			n++
		}
	}
	return n
}

// Review (Phase 4): while the terminal is too small the clock chain stops,
// so there is no permanent wakeup with nothing to draw...
func TestClock_stopsWhileTooSmall(t *testing.T) {
	h := newHarness(t, richFixture())
	h.size(70, 20)
	h.fire(clockTickMsg{})
	if n := h.pendingClock(); n != 0 {
		t.Fatalf("%d clock ticks scheduled while too small", n)
	}
}

// ...and a resize that fits again re-arms it, exactly once, so relative
// times do not freeze.
func TestClock_rearmsWhenFitsAgain(t *testing.T) {
	h := newHarness(t, richFixture())
	h.size(70, 20)
	h.fire(clockTickMsg{})
	h.size(80, 24)
	if n := h.pendingClock(); n != 1 {
		t.Fatalf("%d clock ticks after fitting again", n)
	}
	h.size(100, 30) // already running: no second chain
	if n := h.pendingClock(); n != 1 {
		t.Fatalf("%d clock ticks after another resize", n)
	}
	h.advance(63 * time.Second)
	h.fire(clockTickMsg{})
	h.contains("synced 1m ago")
	if n := h.pendingClock(); n != 1 {
		t.Fatalf("%d clock ticks after a tick", n)
	}
}
