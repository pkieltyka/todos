package service

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/pkieltyka/todos/internal/domain"
)

// An external workflow places the issue after the absent-membership query but
// before AddProjectItem is rejected. No clocks, goroutines or sleeps are needed.
type autoAddBackend struct {
	*writeBackend
	rejection    *domain.Error
	queryFailure error
	lookupCount  int
	omitItem     bool
	archiveItem  bool
	statusItemID domain.NodeID
	statusOption string
}

func autoAddFixture() (*core, *autoAddBackend, *quickClock) {
	_, base := writeFixture()
	b := &autoAddBackend{writeBackend: base, rejection: &domain.Error{Kind: domain.ErrInvalidInput, Message: "GitHub rejected the supplied values: Content already exists in this project"}}
	info := b.fields["P1"]
	info.Options = append(info.Options, domain.StatusOption{Name: "Backlog", ID: "backlog"}, domain.StatusOption{Name: "Sprint", ID: "sprint"})
	b.fields["P1"] = info
	clock := &quickClock{at: time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)}
	s := New(b, &fakeSettings{}, Options{ResolveRepository: func(context.Context) (domain.RepoRef, error) { return b.repo.Ref, nil }, Now: clock.Now, Wait: clock.Wait}).(*core)
	return s, b, clock
}
func (b *autoAddBackend) AddProjectItem(_ context.Context, project, id domain.NodeID) (domain.Placement, error) {
	b.write("AddProjectItem")
	if !b.omitItem {
		b.placements = []domain.ProjectItem{{ID: "WORKFLOW_ITEM", ProjectID: project, Archived: b.archiveItem, Placement: domain.Placement{ItemID: "WORKFLOW_ITEM", Status: str("Backlog"), OptionID: "backlog"}}}
	}
	return domain.Placement{}, b.rejection
}
func (b *autoAddBackend) IssuePlacements(ctx context.Context, id domain.NodeID) ([]domain.ProjectItem, error) {
	b.lookupCount++
	if b.lookupCount > 1 && b.queryFailure != nil {
		return nil, b.queryFailure
	}
	return b.writeBackend.IssuePlacements(ctx, id)
}
func (b *autoAddBackend) SetProjectStatus(ctx context.Context, project, item, field domain.NodeID, option string) error {
	b.statusItemID = item
	b.statusOption = option
	return b.writeBackend.SetProjectStatus(ctx, project, item, field, option)
}
func (b *autoAddBackend) ClearProjectStatus(ctx context.Context, project, item, field domain.NodeID) error {
	b.statusItemID = item
	return b.writeBackend.ClearProjectStatus(ctx, project, item, field)
}
func TestBoard_AutoAddRaceUsesWorkflowItem(t *testing.T) {
	for _, operation := range []string{"move", "board-add", "create"} {
		t.Run(operation, func(t *testing.T) {
			s, b, _ := autoAddFixture()
			var c domain.Change
			var e error
			switch operation {
			case "move":
				c, e = s.SetStatus(context.Background(), 1, str("Sprint"))
			case "board-add":
				c, e = s.BoardAdd(context.Background(), 1, str("Sprint"))
			case "create":
				c, e = s.CreateIssue(context.Background(), domain.NewIssue{Title: "race", Status: str("Sprint")})
			}
			if e != nil || !c.Changed || c.Issue.Board == nil || c.Issue.Board.ItemID != "WORKFLOW_ITEM" || *c.Issue.Board.Status != "Sprint" || b.statusItemID != "WORKFLOW_ITEM" || b.statusOption != "sprint" || b.lookupCount != 2 {
				t.Fatal(c, e, b.writes, b.lookupCount)
			}
			want := []string{"AddProjectItem", "SetProjectStatus"}
			if operation == "create" {
				want = append([]string{"CreateIssue"}, want...)
			}
			if !reflect.DeepEqual(b.writes, want) {
				t.Fatal(b.writes)
			}
		})
	}
}
func TestBoard_AutoAddRaceNilStatusPreservesOrClears(t *testing.T) {
	for _, operation := range []string{"board-add", "clear", "create-no-status"} {
		s, b, _ := autoAddFixture()
		var c domain.Change
		var e error
		switch operation {
		case "board-add":
			c, e = s.BoardAdd(context.Background(), 1, nil)
		case "clear":
			c, e = s.SetStatus(context.Background(), 1, nil)
		case "create-no-status":
			c, e = s.CreateIssue(context.Background(), domain.NewIssue{Title: "race", AddToBoard: true})
		}
		if e != nil || c.Issue.Board == nil {
			t.Fatal(c, e)
		}
		if operation == "board-add" {
			if c.Changed || *c.Issue.Board.Status != "Backlog" || !reflect.DeepEqual(b.writes, []string{"AddProjectItem"}) {
				t.Fatal(c, b.writes)
			}
		} else if !c.Changed || c.Issue.Board.Status != nil || b.statusItemID != "WORKFLOW_ITEM" || b.writes[len(b.writes)-1] != "ClearProjectStatus" {
			t.Fatal(c, b.writes)
		}
	}
}
func TestBoard_AutoAddRaceRestoresArchivedItem(t *testing.T) {
	s, b, _ := autoAddFixture()
	b.archiveItem = true
	c, e := s.SetStatus(context.Background(), 1, str("Sprint"))
	if e != nil || !c.Changed || *c.Issue.Board.Status != "Sprint" || !reflect.DeepEqual(b.writes, []string{"AddProjectItem", "UnarchiveProjectItem", "SetProjectStatus"}) {
		t.Fatal(c, e, b.writes)
	}
}
func TestBoard_AutoAddRaceDoesNotRetryOtherFailures(t *testing.T) {
	for _, failure := range []*domain.Error{{Kind: domain.ErrTimeout, Message: "Content already exists in this project", Uncertain: true}, {Kind: domain.ErrInvalidInput, Message: "different invalid input"}, {Kind: domain.ErrForbidden, Message: "Content already exists in this project"}, {Kind: domain.ErrAuth, Message: "Content already exists in this project"}, {Kind: domain.ErrRateLimited, Message: "Content already exists in this project"}, {Kind: domain.ErrInvalidInput, Message: "Content already exists in this project", Uncertain: true}} {
		s, b, _ := autoAddFixture()
		b.rejection = failure
		_, e := s.BoardAdd(context.Background(), 1, str("Sprint"))
		de := assertKind(t, e, failure.Kind)
		if de.Uncertain != failure.Uncertain || b.lookupCount != 1 || !reflect.DeepEqual(b.writes, []string{"AddProjectItem"}) {
			t.Fatal(de, b.writes, b.lookupCount)
		}
	}
}
func TestBoard_AutoAddRaceReadbackMissingOrFails(t *testing.T) {
	for _, fails := range []bool{false, true} {
		s, b, _ := autoAddFixture()
		if fails {
			b.queryFailure = &domain.Error{Kind: domain.ErrNetwork, Message: "readback failed"}
		} else {
			b.omitItem = true
		}
		_, e := s.BoardAdd(context.Background(), 1, str("Sprint"))
		want := domain.ErrInvalidInput
		if fails {
			want = domain.ErrNetwork
		}
		de := assertKind(t, e, want)
		if de.Uncertain || b.lookupCount != 2 || !reflect.DeepEqual(b.writes, []string{"AddProjectItem"}) {
			t.Fatal(de, b.writes)
		}
	}
}
func TestCreate_AutoAddRaceStatusFailureReportsActualPlacement(t *testing.T) {
	s, b, _ := autoAddFixture()
	b.fail["SetProjectStatus"] = &domain.Error{Kind: domain.ErrTimeout, Message: "timeout", Uncertain: true}
	c, e := s.CreateIssue(context.Background(), domain.NewIssue{Title: "race", Status: str("Sprint")})
	de := assertKind(t, e, domain.ErrPartial)
	if !de.Uncertain || de.Cause != domain.ErrTimeout || !reflect.DeepEqual(de.Completed, []string{"created"}) || c.Issue.Board == nil || de.Issue.Board == nil || *de.Issue.Board.Status != "Backlog" {
		t.Fatal(c, de)
	}
}

type quickAutoAddBackend struct{ *quickBackend }

func (b *quickAutoAddBackend) AddProjectItem(ctx context.Context, project, id domain.NodeID) (domain.Placement, error) {
	p, err := b.quickBackend.AddProjectItem(ctx, project, id)
	if err != nil {
		return p, err
	}
	b.mu.Lock()
	p.Status = str("Backlog")
	p.OptionID = "backlog"
	b.placements[id] = p
	b.mu.Unlock()
	return domain.Placement{}, &domain.Error{Kind: domain.ErrInvalidInput, Message: "GitHub rejected the supplied values: Content already exists in this project"}
}
func TestQuickAdd_AutoAddRaceRecoversOrReportsOnBoard(t *testing.T) {
	for _, fails := range []bool{false, true} {
		s, b, clock := quickFixture()
		info := b.fields["P1"]
		info.Options = append(info.Options, domain.StatusOption{Name: "Sprint", ID: "sprint"})
		b.fields["P1"] = info
		s.backend = &quickAutoAddBackend{b}
		if fails {
			b.failures["SetProjectStatus.1"] = &domain.Error{Kind: domain.ErrForbidden, Message: "status denied"}
		}
		r, e := s.RunQuickAdd(context.Background(), quickPlan("one\ntwo", domain.QuickAddOptions{Status: str("Sprint")}), domain.QuickAddRunOptions{})
		if fails {
			assertKind(t, e, domain.ErrPartial)
			if r.Counts.Created != 1 || r.Counts.Unattempted != 1 || !r.Items[0].OnBoard || *r.Items[0].Issue.Board.Status != "Backlog" {
				t.Fatal(r, e)
			}
		} else {
			if e != nil || r.Counts.Created != 2 || r.Items[0].Issue.NodeID == r.Items[1].Issue.NodeID {
				t.Fatal(r, e)
			}
			for _, row := range r.Items {
				if !row.OnBoard || *row.Issue.Board.Status != "Sprint" {
					t.Fatal(r, e)
				}
			}
		}
		if len(clock.waits) < 2 {
			t.Fatal("recovery bypassed run-local pacing", clock.waits)
		}
		if b.counts["AddProjectItem"] != r.Counts.Created || b.counts["CreateIssue"] != r.Counts.Created {
			t.Fatal("create/add was replayed", b.writes)
		}
	}
}
