package service

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/pkieltyka/todos/internal/domain"
)

// The add response was sampled with a null status; a workflow writes Backlog
// before the caller handles that response. No status still requires a clear.
type staleNoStatusBackend struct{ *writeBackend }

func (b *staleNoStatusBackend) AddProjectItem(ctx context.Context, project, id domain.NodeID) (domain.Placement, error) {
	p, e := b.writeBackend.AddProjectItem(ctx, project, id)
	if e == nil {
		b.placements[0].Placement.Status = str("Backlog")
		b.placements[0].Placement.OptionID = "backlog"
	}
	return p, e
}
func (b *staleNoStatusBackend) ClearProjectStatus(ctx context.Context, project, item, field domain.NodeID) error {
	if e := b.writeBackend.ClearProjectStatus(ctx, project, item, field); e != nil {
		return e
	}
	b.placements[0].Placement.Status = nil
	b.placements[0].Placement.OptionID = ""
	return nil
}
func TestNoStatus_NewCardClearsEvenWhenAddAcknowledgesNull(t *testing.T) {
	for _, create := range []bool{false, true} {
		s, base := writeFixture()
		b := &staleNoStatusBackend{base}
		s.backend = b
		var change domain.Change
		var e error
		if create {
			change, e = s.CreateIssue(context.Background(), domain.NewIssue{Title: "No status", AddToBoard: true})
		} else {
			change, e = s.BoardAdd(context.Background(), 1, nil)
		}
		want := []string{"AddProjectItem", "ClearProjectStatus"}
		if create {
			want = append([]string{"CreateIssue"}, want...)
		}
		if e != nil || !change.Changed || change.Issue.Board == nil || change.Issue.Board.Status != nil || b.placements[0].Placement.Status != nil || !reflect.DeepEqual(b.writes, want) {
			t.Fatal(create, change, e, b.writes)
		}
	}
}
func TestNoStatus_NewIssueClearsWorkflowExistingUnsetCard(t *testing.T) {
	s, b := writeFixture()
	member(b, false, nil)
	change, e := s.CreateIssue(context.Background(), domain.NewIssue{Title: "No status", AddToBoard: true})
	if e != nil || change.Issue.Board == nil || change.Issue.Board.Status != nil || !reflect.DeepEqual(b.writes, []string{"CreateIssue", "ClearProjectStatus"}) {
		t.Fatal(change, e, b.writes)
	}
}
func TestNoStatus_ExistingIssueNoOpAndBoardAddPreservesStatus(t *testing.T) {
	s, b := writeFixture()
	member(b, false, nil)
	change, e := s.SetStatus(context.Background(), 1, nil)
	if e != nil || change.Changed || len(b.writes) > 0 {
		t.Fatal(change, e, b.writes)
	}
	member(b, false, str("Backlog"))
	change, e = s.BoardAdd(context.Background(), 1, nil)
	if e != nil || change.Changed || *change.Issue.Board.Status != "Backlog" || len(b.writes) > 0 {
		t.Fatal(change, e, b.writes)
	}
}
func TestNoStatus_QuickAddClearsWorkflowCardInsteadOfPreservingIt(t *testing.T) {
	for _, status := range []*string{nil, str("Backlog")} {
		s, b, _ := quickFixture()
		run := domain.QuickAddRunOptions{OnProgress: func(p domain.QuickAddProgress) error {
			if p.Phase == "placing" {
				b.mu.Lock()
				defer b.mu.Unlock()
				b.placements["I1"] = domain.Placement{ItemID: "AUTO_ITEM", Status: status}
			}
			return nil
		}}
		r := runQuick(t, s, "No status", domain.QuickAddOptions{AddToBoard: true}, run)
		if !r.Items[0].OnBoard || r.Items[0].Issue.Board.Status != nil || b.counts["AddProjectItem"] != 0 || b.counts["ClearProjectStatus"] != 1 {
			t.Fatal(r, b.writes)
		}
		if b.writes[1].start.Sub(b.writes[0].end) < time.Second {
			t.Fatal("clear bypassed quick-add pacing", b.writes)
		}
	}
}
func TestNoStatus_ClearFailureKeepsCreatedAndKnownCard(t *testing.T) {
	s, b, _ := quickFixture()
	b.failures["ClearProjectStatus.1"] = &domain.Error{Kind: domain.ErrTimeout, Message: "clear timed out", Uncertain: true}
	r, e := s.RunQuickAdd(context.Background(), quickPlan("one\ntwo", domain.QuickAddOptions{AddToBoard: true}), domain.QuickAddRunOptions{})
	de := assertKind(t, e, domain.ErrPartial)
	if !de.Uncertain || r.Counts.Created != 1 || r.Counts.Unattempted != 1 || !r.Items[0].OnBoard || r.Items[0].Error == nil {
		t.Fatal(r, e)
	}
	if b.counts["CreateIssue"] != 1 || b.counts["ClearProjectStatus"] != 1 {
		t.Fatal("mutation was replayed", b.writes)
	}
}
