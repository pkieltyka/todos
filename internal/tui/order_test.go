package tui

import (
	"slices"
	"testing"
	"time"

	"github.com/pkieltyka/todos/internal/domain"
	"github.com/pkieltyka/todos/internal/service/servicetest"
)

// ---- closed view ordering (tui.md 7.4) ----

func TestClosedOrder_newestClosedFirstWithFallbacks(t *testing.T) {
	at := func(h int) time.Time { return fixtureNow.Add(time.Duration(h) * time.Hour) }
	issues := []domain.Issue{
		{Number: 1, UpdatedAt: at(9), ClosedAt: ptr(at(1))}, // closed long ago, updated recently
		{Number: 2, UpdatedAt: at(2), ClosedAt: ptr(at(5))}, // newest closed
		{Number: 3, UpdatedAt: at(3), ClosedAt: ptr(at(3))}, // ties #4 on closed time, newer update
		{Number: 4, UpdatedAt: at(2), ClosedAt: ptr(at(3))}, // ties #3, older update
		{Number: 5, UpdatedAt: at(2), ClosedAt: ptr(at(2))}, // ties #6 on closed and updated time
		{Number: 6, UpdatedAt: at(2), ClosedAt: ptr(at(2))}, //   so the higher number first
		{Number: 7, UpdatedAt: at(4)},                       // no closed time: sorts by updated time
		{Number: 8, UpdatedAt: at(0)},                       // no closed time, oldest
	}
	before := slices.Clone(issues)
	got := closedOrder(issues)
	want := []int{2, 7, 3, 4, 6, 5, 1, 8}
	if !slices.Equal(got, want) {
		t.Fatalf("order %v, want %v", got, want)
	}
	for k := range issues {
		if issues[k].Number != before[k].Number {
			t.Fatal("closedOrder changed its input")
		}
	}
}

// closedFixture has n closed issues listed by GitHub newest-updated first
// (#n first) but closed in the opposite order (#1 closed most recently).
func closedFixture(n int) servicetest.Fixture {
	fx := bigFixture(n)
	for k := range fx.Issues {
		i := &fx.Issues[k].Issue
		i.State = "closed"
		i.StateReason = ptr(domain.StateReason("completed"))
		i.ClosedAt = ptr(fixtureNow.Add(-time.Duration(i.Number) * time.Minute))
	}
	return fx
}

func issueSel(h *harness) *IssueList { return h.app.top(ViewIssues).(*IssueList) }

func TestClosedOrder_openAndAllKeepGitHubOrder(t *testing.T) {
	fx := closedFixture(6)
	for _, k := range []int{0, 2, 4} { // #6, #4 and #2 are open
		fx.Issues[k].Issue.State, fx.Issues[k].Issue.ClosedAt, fx.Issues[k].Issue.StateReason = "open", nil, nil
	}
	h := newHarness(t, fx)
	if got := issueSel(h).rows; !slices.Equal(got, []int{6, 4, 2}) {
		t.Fatalf("open rows %v", got)
	}
	h.lacks("by closed time")
	h.press("s") // closed
	if got := issueSel(h).rows; !slices.Equal(got, []int{1, 3, 5}) {
		t.Fatalf("closed rows %v", got)
	}
	h.contains("closed ▾ by closed time")
	h.press("s") // all
	if got, want := issueSel(h).rows, h.app.store.Issues.Numbers; !slices.Equal(got, want) || !slices.Equal(got, []int{6, 5, 4, 3, 2, 1}) {
		t.Fatalf("all rows %v, GitHub order %v", got, want)
	}
	h.lacks("by closed time")
}

func TestClosedOrder_selectionFollowsIssueAcrossPagesAndRefresh(t *testing.T) {
	h := newHarness(t, closedFixture(250))
	g1 := h.hold("ListIssues", 0)
	g2 := h.hold("ListIssues", 0)
	h.press("s") // closed: page 1 is #250..#151, shown as #151..#250
	h.release(g1)
	s := issueSel(h)
	if s.rows[0] != 151 {
		t.Fatalf("first row #%d after page 1", s.rows[0])
	}
	for range 49 {
		h.press("j")
	}
	if s.sel != 200 {
		t.Fatalf("selected #%d, want #200", s.sel)
	}
	h.release(g2) // page 2 puts #150..#101 above it
	if s.sel != 200 {
		t.Fatalf("selection moved to #%d when page 2 landed", s.sel)
	}
	if i, _ := indexOf(s.rows, 200); i == 49 {
		t.Fatal("rows did not reorder; the test proves nothing")
	}
	h.settle()
	h.fake.Mutate(func(f *servicetest.Fixture) {
		for k := range f.Issues {
			if f.Issues[k].Issue.Number == 200 {
				f.Issues[k].Issue.ClosedAt = ptr(fixtureNow) // reopened and closed again elsewhere
			}
		}
	})
	h.press("r")
	if s.sel != 200 || s.rows[0] != 200 {
		t.Fatalf("after refresh: selected #%d, first row #%d", s.sel, s.rows[0])
	}
	if p := s.Position(nil); p != "1/250" {
		t.Fatalf("position %s", p)
	}
}

func TestClosedOrder_timeColumnShowsClosedTime(t *testing.T) {
	fx := closedFixture(2)
	for k := range fx.Issues {
		fx.Issues[k].Issue.UpdatedAt = fixtureNow.Add(-3 * time.Hour)
	}
	fx.Issues[0].Issue.ClosedAt = ptr(fixtureNow.Add(-2 * 24 * time.Hour))
	h := newHarness(t, fx)
	h.press("s")
	h.contains("2d")
	h.lacks("3h")
}

func TestClosedOrder_reopenKeepsItsRow(t *testing.T) {
	h := newHarness(t, closedFixture(3))
	h.press("s", "j") // rows #1 #2 #3; select #2
	s := issueSel(h)
	g := h.hold("Reopen", 2)
	h.press("x")
	if !slices.Equal(s.rows, []int{1, 2, 3}) || s.sel != 2 {
		t.Fatalf("while reopening: rows %v, selected #%d", s.rows, s.sel)
	}
	h.release(g)
	if !slices.Equal(s.rows, []int{1, 2, 3}) || s.sel != 2 {
		t.Fatalf("after reopening: rows %v, selected #%d", s.rows, s.sel)
	}
	h.press("x") // closed again: it sorts by its new closed time
	if !slices.Equal(s.rows, []int{2, 1, 3}) || s.sel != 2 {
		t.Fatalf("after closing again: rows %v, selected #%d", s.rows, s.sel)
	}
}
