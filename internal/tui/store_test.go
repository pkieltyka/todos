package tui

import (
	"testing"
	"time"

	"github.com/pkieltyka/todos/internal/domain"
)

func iss(n int, title string, status *string) domain.Issue {
	i := domain.Issue{Number: n, Title: title, State: "open"}
	if status != nil || title == "nostatus" {
		i.Board = &domain.Placement{Status: status}
	}
	return i
}

func TestPut_olderAsOfIgnored(t *testing.T) {
	s := NewStore()
	t0 := fixtureNow
	s.Put(iss(1, "new", nil), t0.Add(time.Second))
	if s.Put(iss(1, "old", nil), t0) {
		t.Fatal("older copy accepted")
	}
	if i, _ := s.Issue(1); i.Title != "new" {
		t.Fatalf("title %q", i.Title)
	}
}

func TestPut_equalAsOfReplaces(t *testing.T) {
	s := NewStore()
	s.Put(iss(1, "a", nil), fixtureNow)
	if !s.Put(iss(1, "b", nil), fixtureNow) {
		t.Fatal("equal asOf rejected")
	}
}

func TestPut_writeArrivalBeatsEarlierSentRead(t *testing.T) {
	// A read sent at t1 lands after a write that arrived at t2 > t1: the
	// write wins (A5 rule 7).
	s := NewStore()
	t1, t2 := fixtureNow, fixtureNow.Add(2*time.Second)
	s.Put(iss(1, "written", nil), t2)
	s.Put(iss(1, "read", nil), t1)
	if i, _ := s.Issue(1); i.Title != "written" {
		t.Fatalf("title %q", i.Title)
	}
}

func TestPut_positionRecordedEvenWhenContentStale(t *testing.T) {
	h := newHarness(t, richFixture())
	g := h.hold("ListIssues", 0)
	h.press("r")
	// A newer copy of #6 lands (as a write would) while the page is in flight.
	h.advance(time.Second)
	fresh, _ := h.app.store.Issue(6)
	fresh.Title = "Newer title"
	h.app.store.Put(fresh, h.now)
	h.release(g)
	if i, _ := h.app.store.Issue(6); i.Title != "Newer title" {
		t.Fatalf("stale page replaced content: %q", i.Title)
	}
	if _, ok := indexOf(h.app.store.Issues.Numbers, 6); !ok {
		t.Fatal("position not recorded")
	}
}

// boardStore builds a store with options Todo, Doing, Done and the given order.
func boardStore(order ...ColumnOrder) *Store {
	s := NewStore()
	s.Board.Info = &domain.BoardInfo{Options: []domain.StatusOption{{ID: "t", Name: "Todo"}, {ID: "d", Name: "Doing"}, {ID: "x", Name: "Done"}}}
	// Like a Board result: every status column is present, empty or not.
	for _, name := range []string{"Todo", "Doing", "Done"} {
		found := false
		for _, o := range order {
			if o.Status != nil && *o.Status == name {
				found = true
			}
		}
		if !found {
			order = append(order, ColumnOrder{Status: ptr(name)})
		}
	}
	s.Board.Order = order
	s.Board.Loaded = true
	for _, o := range order {
		for _, n := range o.Numbers {
			i := iss(n, "card", o.Status)
			if o.Status == nil {
				i.Board = &domain.Placement{}
			}
			s.Put(i, fixtureNow)
		}
	}
	return s
}

func colNums(cols []DisplayColumn, name string) []int {
	for _, c := range cols {
		if c.Name == name {
			return c.Numbers
		}
	}
	return nil
}

func TestColumns_followsGitHubOrder(t *testing.T) {
	s := boardStore(ColumnOrder{Status: ptr("Todo"), Numbers: []int{3, 9, 1}})
	cols := s.Columns(s.Issue, nil)
	if got := colNums(cols, "Todo"); len(got) != 3 || got[0] != 3 || got[1] != 9 || got[2] != 1 {
		t.Fatalf("Todo = %v", got)
	}
	if len(cols) != 3 || cols[0].Name != "Todo" {
		t.Fatalf("columns %+v", cols)
	}
}

func TestColumns_noStatusShownOnlyWhenNotEmpty(t *testing.T) {
	s := boardStore(ColumnOrder{Status: nil, Numbers: nil}, ColumnOrder{Status: ptr("Todo"), Numbers: []int{1}})
	if cols := s.Columns(s.Issue, nil); cols[0].Name != "Todo" {
		t.Fatal("empty No status column shown")
	}
	s = boardStore(ColumnOrder{Status: nil, Numbers: []int{2}}, ColumnOrder{Status: ptr("Todo"), Numbers: []int{1}})
	if cols := s.Columns(s.Issue, nil); cols[0].Name != "No status" || cols[0].Numbers[0] != 2 {
		t.Fatalf("No status column %+v", cols[0])
	}
}

// moved replays a pending status change for one issue, as Phase 6's fold will.
func moved(s *Store, n int, to *string) func(int) (domain.Issue, bool) {
	return func(k int) (domain.Issue, bool) {
		i, ok := s.Issue(k)
		if ok && k == n {
			if to == nil {
				i.Board = nil
			} else {
				i.Board = &domain.Placement{Status: to}
			}
		}
		return i, ok
	}
}

func TestColumns_movedCardAtBottomOfNewColumn(t *testing.T) {
	s := boardStore(ColumnOrder{Status: ptr("Todo"), Numbers: []int{5, 2}}, ColumnOrder{Status: ptr("Doing"), Numbers: []int{1, 7}})
	cols := s.Columns(moved(s, 2, ptr("Doing")), nil)
	if got := colNums(cols, "Todo"); len(got) != 1 || got[0] != 5 {
		t.Fatalf("Todo = %v", got)
	}
	if got := colNums(cols, "Doing"); len(got) != 3 || got[2] != 2 {
		t.Fatalf("Doing = %v, want #2 at the bottom", got)
	}
}

func TestColumns_settledMoveStaysAtBottomUntilReload(t *testing.T) {
	s := boardStore(ColumnOrder{Status: ptr("Todo"), Numbers: []int{2}}, ColumnOrder{Status: ptr("Doing"), Numbers: []int{1, 7}})
	// The write settled: the base now says Doing, but GitHub's order still has #2 in Todo.
	s.Put(iss(2, "card", ptr("Doing")), fixtureNow.Add(time.Second))
	cols := s.Columns(s.Issue, nil)
	if got := colNums(cols, "Doing"); len(got) != 3 || got[2] != 2 {
		t.Fatalf("Doing = %v", got)
	}
	// A reload puts it where GitHub placed it.
	s.Board.Order = []ColumnOrder{{Status: ptr("Doing"), Numbers: []int{2, 1, 7}}}
	if got := colNums(s.Columns(s.Issue, nil), "Doing"); got[0] != 2 {
		t.Fatalf("after reload Doing = %v", got)
	}
}

func TestColumns_removeFromBoardHidesCard(t *testing.T) {
	s := boardStore(ColumnOrder{Status: ptr("Todo"), Numbers: []int{5, 2}})
	if got := colNums(s.Columns(moved(s, 2, nil), nil), "Todo"); len(got) != 1 {
		t.Fatalf("Todo = %v", got)
	}
}

func TestColumns_issueMovedFromListAppearsOnBoard(t *testing.T) {
	s := boardStore(ColumnOrder{Status: ptr("Todo"), Numbers: []int{5}})
	s.Put(iss(8, "not on board", nil), fixtureNow)
	cols := s.Columns(moved(s, 8, ptr("Todo")), []int{8})
	if got := colNums(cols, "Todo"); len(got) != 2 || got[1] != 8 {
		t.Fatalf("Todo = %v", got)
	}
}

func TestColumns_cap500CardsShowsNotice(t *testing.T) {
	var nums []int
	for n := 1; n <= 620; n++ {
		nums = append(nums, n)
	}
	s := boardStore(ColumnOrder{Status: ptr("Todo"), Numbers: nums[:400]}, ColumnOrder{Status: ptr("Done"), Numbers: nums[400:]})
	total := 0
	for _, c := range s.Columns(s.Issue, nil) {
		total += len(c.Numbers)
	}
	if total != cardsCap {
		t.Fatalf("drew %d cards", total)
	}
	// The sub-bar says the board is cut off.
	fx := richFixture()
	h := newHarness(t, fx, withStart(ViewBoard))
	h.app.store.Board = s.Board
	h.app.store.Board.Loaded = true
	for n := 1; n <= 620; n++ {
		h.app.store.Put(iss(n, "card", ptr("Todo")), fixtureNow)
	}
	h.send(nudge{})
	h.contains("board cut off")
}
