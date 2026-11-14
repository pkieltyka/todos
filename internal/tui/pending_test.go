package tui

import (
	"reflect"
	"testing"
	"time"

	"github.com/pkieltyka/todos/internal/domain"
)

var testLabels = []domain.Label{{Name: "bug", Color: "d73a4a"}, {Name: "sync", Color: "0075ca"}, {Name: "docs", Color: "008672"}}

func baseIssue() domain.Issue {
	return domain.Issue{Number: 6, State: "open", Assignees: []string{"reviewer"},
		Labels: []domain.Label{testLabels[0], testLabels[1]}}
}

func TestApply_setSemanticsIdempotent(t *testing.T) {
	patches := []Patch{
		{Assignees: &SetDelta{Add: []string{"octocat"}, Remove: []string{"reviewer"}}},
		{Labels: &SetDelta{Add: []string{"docs"}, Remove: []string{"bug"}}},
		{State: &StatePatch{Reason: "completed"}},
		{Board: &BoardPatch{Status: ptr("Todo")}},
		{Board: &BoardPatch{Remove: true}},
	}
	for _, p := range patches {
		once := Apply(baseIssue(), p, testLabels)
		twice := Apply(once, p, testLabels)
		if !reflect.DeepEqual(once, twice) {
			t.Fatalf("patch %+v not idempotent:\n%+v\n%+v", p, once, twice)
		}
	}
	// Applying to an issue that already has the change changes nothing.
	has := baseIssue()
	if got := Apply(has, Patch{Assignees: &SetDelta{Add: []string{"reviewer"}}}, testLabels); !reflect.DeepEqual(got.Assignees, has.Assignees) {
		t.Fatalf("assignees %v", got.Assignees)
	}
	got := Apply(baseIssue(), Patch{Labels: &SetDelta{Add: []string{"docs"}}}, testLabels)
	if len(got.Labels) != 3 || got.Labels[2].Color != "008672" {
		t.Fatalf("added label lost its colour: %+v", got.Labels)
	}
}

func TestApply_boardPlacementCreatedWhenAbsent(t *testing.T) {
	i := baseIssue()
	got := Apply(i, Patch{Board: &BoardPatch{Status: ptr("Done")}}, nil)
	if got.Board == nil || *got.Board.Status != "Done" {
		t.Fatalf("board %+v", got.Board)
	}
	got = Apply(i, Patch{Board: &BoardPatch{}}, nil) // "No status"
	if got.Board == nil || got.Board.Status != nil {
		t.Fatalf("No status: %+v", got.Board)
	}
	if i.Board != nil {
		t.Fatal("base modified")
	}
}

func TestEffective_replaysInIDOrder(t *testing.T) {
	cs := []*Change{
		{ID: 1, Patch: Patch{State: &StatePatch{Reason: "completed"}}},
		{ID: 2, Patch: Patch{State: &StatePatch{Open: true}}},
	}
	eff, _ := Effective(baseIssue(), cs, nil)
	if eff.State != "open" {
		t.Fatalf("state %q: close then reopen must end open", eff.State)
	}
	cs[0], cs[1] = cs[1], cs[0]
	if eff, _ := Effective(baseIssue(), cs, nil); eff.State != "closed" {
		t.Fatal("order not respected")
	}
}

func TestMarks_addedRemovedBusyUncertain(t *testing.T) {
	cs := []*Change{
		{ID: 1, Phase: InFlight, Patch: Patch{Labels: &SetDelta{Add: []string{"docs"}, Remove: []string{"bug"}}}},
		{ID: 2, Phase: Queued, Patch: Patch{Assignees: &SetDelta{Add: []string{"octocat"}}}},
	}
	_, m := Effective(baseIssue(), cs, testLabels)
	if !m.Busy || m.Uncertain || !reflect.DeepEqual(m.AddedLabels, []string{"docs"}) || !reflect.DeepEqual(m.RemovedLabels, []string{"bug"}) ||
		!reflect.DeepEqual(m.AddedPeople, []string{"octocat"}) {
		t.Fatalf("marks %+v", m)
	}
	_, m = Effective(baseIssue(), []*Change{{ID: 3, Phase: Uncertain, Patch: Patch{State: &StatePatch{Reason: "completed"}}}}, nil)
	if !m.Uncertain || m.Busy || !m.State {
		t.Fatalf("uncertain marks %+v", m)
	}
}

func TestPendingSet_holdBlocksNext(t *testing.T) {
	p := newPendingSet()
	p.add(1, Patch{State: &StatePatch{Reason: "completed"}})
	p.add(1, Patch{State: &StatePatch{Open: true}})
	first := p.next()
	if len(first) != 1 {
		t.Fatalf("%d sent", len(first))
	}
	p.hold(first[0], time.Now())
	if n := len(p.next()); n != 0 {
		t.Fatalf("%d sent while held", n)
	}
	p.remove(first[0]) // released
	if n := len(p.next()); n != 1 {
		t.Fatalf("%d sent after release", n)
	}
}
