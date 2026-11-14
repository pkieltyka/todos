package service

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/pkieltyka/todos/internal/domain"
)

type writeBackend struct {
	*fakeBackend
	detail                          domain.IssueDetail
	placements                      []domain.ProjectItem
	writes                          []string
	patch                           domain.IssuePatch
	created                         domain.CreateInput
	ignoreAssignment                bool
	removedUserIDs, removedLabelIDs []domain.NodeID
	defaultStatus                   *string
	closeOnComment                  bool
}

func writeFixture() (*core, *writeBackend) {
	b := &writeBackend{fakeBackend: backendFixture(), detail: domain.IssueDetail{Issue: domain.Issue{NodeID: "I1", Number: 1, Title: "original", State: "open", Assignees: []string{"test"}, AssigneeIDs: map[string]domain.NodeID{"test": "U1"}, Labels: []domain.Label{{NodeID: "L1", Name: "bug"}}, URL: "https://github.com/test/repo/issues/1"}, Body: "body"}}
	b.users = []domain.User{{NodeID: "U1", Login: "test"}, {NodeID: "U2", Login: "alice"}, {NodeID: "U3", Login: "bob"}}
	b.labels = []domain.Label{{NodeID: "L1", Name: "bug"}, {NodeID: "L2", Name: "docs"}}
	s := New(b, &fakeSettings{}, Options{ResolveRepository: func(context.Context) (domain.RepoRef, error) { return b.repo.Ref, nil }}).(*core)
	return s, b
}
func (b *writeBackend) write(name string) error {
	b.writes = append(b.writes, name)
	return b.record(backendCall{Name: name})
}
func (b *writeBackend) Issue(_ context.Context, _ domain.RepoRef, n int, id domain.NodeID) (domain.IssueDetail, error) {
	e := b.record(backendCall{Name: "Issue", Project: id})
	d := b.detail
	d.Issue = domain.CloneIssue(d.Issue)
	d.Issue.Board = nil
	for _, p := range b.placements {
		if p.ProjectID == id && !p.Archived {
			v := p.Placement
			d.Issue.Board = &v
		}
	}
	return d, e
}
func (b *writeBackend) ack() domain.Issue {
	i := domain.CloneIssue(b.detail.Issue)
	i.Board = nil
	return i
}
func (b *writeBackend) CreateIssue(_ context.Context, in domain.CreateInput) (domain.Issue, error) {
	if e := b.write("CreateIssue"); e != nil {
		return domain.Issue{}, e
	}
	b.created = in
	b.detail.Issue.Title = in.Title
	b.detail.Body = in.Body
	b.detail.Issue.Assignees = []string{}
	if !b.ignoreAssignment {
		b.setAssignees(in.AssigneeIDs)
	}
	return b.ack(), nil
}
func (b *writeBackend) setAssignees(ids []domain.NodeID) {
	b.detail.Issue.Assignees = []string{}
	b.detail.Issue.AssigneeIDs = map[string]domain.NodeID{}
	for _, id := range ids {
		for _, u := range b.users {
			if u.NodeID == id {
				b.detail.Issue.Assignees = append(b.detail.Issue.Assignees, u.Login)
				b.detail.Issue.AssigneeIDs[u.Login] = u.NodeID
			}
		}
	}
}
func (b *writeBackend) UpdateIssue(_ context.Context, _ domain.NodeID, in domain.IssuePatch) (domain.Issue, error) {
	if e := b.write("UpdateIssue"); e != nil {
		return domain.Issue{}, e
	}
	b.patch = in
	if in.Title != nil {
		b.detail.Issue.Title = *in.Title
	}
	if in.Body != nil {
		b.detail.Body = *in.Body
	}
	if in.AssigneeIDs != nil && !b.ignoreAssignment {
		b.setAssignees(*in.AssigneeIDs)
	}
	return b.ack(), nil
}
func (b *writeBackend) AddAssignees(_ context.Context, _ domain.NodeID, ids []domain.NodeID) (domain.Issue, error) {
	if e := b.write("AddAssignees"); e != nil {
		return domain.Issue{}, e
	}
	if !b.ignoreAssignment {
		for _, id := range ids {
			for _, u := range b.users {
				if u.NodeID == id && !hasLogin(b.detail.Issue.Assignees, u.Login) {
					b.detail.Issue.Assignees = append(b.detail.Issue.Assignees, u.Login)
					b.detail.Issue.AssigneeIDs[u.Login] = u.NodeID
				}
			}
		}
	}
	return b.ack(), nil
}
func (b *writeBackend) RemoveAssignees(_ context.Context, _ domain.NodeID, ids []domain.NodeID) (domain.Issue, error) {
	b.removedUserIDs = append([]domain.NodeID{}, ids...)
	if e := b.write("RemoveAssignees"); e != nil {
		return domain.Issue{}, e
	}
	if !b.ignoreAssignment {
		next := []string{}
		for _, name := range b.detail.Issue.Assignees {
			remove := false
			for _, id := range ids {
				remove = remove || b.detail.Issue.AssigneeIDs[name] == id
			}
			if !remove {
				next = append(next, name)
			}
		}
		b.detail.Issue.Assignees = next
	}
	return b.ack(), nil
}
func (b *writeBackend) AddLabels(_ context.Context, _ domain.NodeID, ids []domain.NodeID) (domain.Issue, error) {
	if e := b.write("AddLabels"); e != nil {
		return domain.Issue{}, e
	}
	for _, id := range ids {
		for _, l := range b.labels {
			if l.NodeID == id && !hasLabel(b.detail.Issue.Labels, l.Name) {
				b.detail.Issue.Labels = append(b.detail.Issue.Labels, l)
			}
		}
	}
	return b.ack(), nil
}
func (b *writeBackend) RemoveLabels(_ context.Context, _ domain.NodeID, ids []domain.NodeID) (domain.Issue, error) {
	b.removedLabelIDs = append([]domain.NodeID{}, ids...)
	if e := b.write("RemoveLabels"); e != nil {
		return domain.Issue{}, e
	}
	next := []domain.Label{}
	for _, l := range b.detail.Issue.Labels {
		remove := false
		for _, id := range ids {
			remove = remove || l.NodeID == id
		}
		if !remove {
			next = append(next, l)
		}
	}
	b.detail.Issue.Labels = next
	return b.ack(), nil
}
func (b *writeBackend) AddComment(_ context.Context, _ domain.NodeID, body string) (domain.CommentResult, error) {
	if e := b.write("AddComment"); e != nil {
		return domain.CommentResult{}, e
	}
	b.detail.Issue.CommentCount++
	if b.closeOnComment {
		b.detail.Issue.State = "closed"
	}
	return domain.CommentResult{Issue: b.ack(), Comment: domain.Comment{ID: "C1", Body: body}}, nil
}
func (b *writeBackend) CloseIssue(_ context.Context, _ domain.NodeID, reason string) (domain.Issue, error) {
	if e := b.write("CloseIssue"); e != nil {
		return domain.Issue{}, e
	}
	b.detail.Issue.State = "closed"
	r := domain.StateReason(reason)
	b.detail.Issue.StateReason = &r
	return b.ack(), nil
}
func (b *writeBackend) ReopenIssue(context.Context, domain.NodeID) (domain.Issue, error) {
	if e := b.write("ReopenIssue"); e != nil {
		return domain.Issue{}, e
	}
	b.detail.Issue.State = "open"
	return b.ack(), nil
}
func (b *writeBackend) DeleteIssue(context.Context, domain.NodeID) error {
	return b.write("DeleteIssue")
}
func (b *writeBackend) IssuePlacements(context.Context, domain.NodeID) ([]domain.ProjectItem, error) {
	return append([]domain.ProjectItem{}, b.placements...), b.record(backendCall{Name: "IssuePlacements"})
}
func (b *writeBackend) AddProjectItem(_ context.Context, p, id domain.NodeID) (domain.Placement, error) {
	if e := b.write("AddProjectItem"); e != nil {
		return domain.Placement{}, e
	}
	placement := domain.Placement{ItemID: "ITEM1", Status: b.defaultStatus}
	b.placements = append(b.placements, domain.ProjectItem{ID: "ITEM1", ProjectID: p, Placement: placement})
	return placement, nil
}
func (b *writeBackend) UnarchiveProjectItem(_ context.Context, p, id domain.NodeID) (domain.Placement, error) {
	if e := b.write("UnarchiveProjectItem"); e != nil {
		return domain.Placement{}, e
	}
	b.placements[0].Archived = false
	return b.placements[0].Placement, nil
}
func (b *writeBackend) SetProjectStatus(_ context.Context, p, id, f domain.NodeID, option string) error {
	return b.write("SetProjectStatus")
}
func (b *writeBackend) ClearProjectStatus(context.Context, domain.NodeID, domain.NodeID, domain.NodeID) error {
	return b.write("ClearProjectStatus")
}
func (b *writeBackend) RemoveProjectItem(context.Context, domain.NodeID, domain.NodeID) error {
	return b.write("RemoveProjectItem")
}
func str(v string) *string { return &v }
func member(b *writeBackend, archived bool, status *string) {
	b.placements = []domain.ProjectItem{{ID: "ITEM1", ProjectID: "P1", Archived: archived, Placement: domain.Placement{ItemID: "ITEM1", Status: status}}}
}
func TestNames_AllValidatedBeforeWrite(t *testing.T) {
	for _, in := range []domain.NewIssue{{Title: "ok", Assignees: []string{"bad"}}, {Title: "ok", Labels: []string{"no"}}, {Title: "ok", Status: str("no")}, {Title: " "}, {Title: strings.Repeat("界", 257)}, {Title: "ok", Body: string([]byte{255})}} {
		s, b := writeFixture()
		_, e := s.CreateIssue(context.Background(), in)
		assertKind(t, e, domain.ErrInvalidInput)
		if len(b.writes) > 0 {
			t.Fatal(b.writes)
		}
	}
}
func TestNames_RefreshUnknownOnce(t *testing.T) {
	for _, which := range []string{"user", "label", "status"} {
		t.Run(which, func(t *testing.T) {
			s, b := writeFixture()
			ctx := context.Background()
			var e error
			switch which {
			case "user":
				s.AssignableUsers(ctx)
				_, e = s.Reassign(ctx, 1, []string{"unknown"})
				if b.count("AssignableUsers") != 2 {
					t.Fatal(b.calls)
				}
			case "label":
				s.Labels(ctx)
				_, e = s.UpdateLabels(ctx, 1, []string{"unknown"}, nil)
				if b.count("Labels") != 2 {
					t.Fatal(b.calls)
				}
			case "status":
				s.BoardInfo(ctx)
				_, e = s.SetStatus(ctx, 1, str("unknown"))
				if b.count("ProjectFields") != 2 {
					t.Fatal(b.calls)
				}
			}
			assertKind(t, e, domain.ErrInvalidInput)
			if len(b.writes) > 0 {
				t.Fatal(b.writes)
			}
		})
	}
}
func TestAssignees_DedupMax10(t *testing.T) {
	s, b := writeFixture()
	change, e := s.Reassign(context.Background(), 1, []string{"ALICE", "alice", "@me"})
	if e != nil || !reflect.DeepEqual(change.Issue.Assignees, []string{"alice", "test"}) {
		t.Fatal(change, e)
	}
	s, b = writeFixture()
	names := []string{}
	for j := 0; j < 11; j++ {
		name := fmt.Sprintf("user%d", j)
		names = append(names, name)
		b.users = append(b.users, domain.User{Login: name, NodeID: domain.NodeID(name)})
	}
	_, e = s.UpdateAssignees(context.Background(), 1, names, nil)
	assertKind(t, e, domain.ErrInvalidInput)
	if len(b.writes) > 0 {
		t.Fatal(b.writes)
	}
}
func TestStatus_ExactThenUniqueFold(t *testing.T) {
	b := domain.BoardInfo{Options: []domain.StatusOption{{Name: "Todo", ID: "one"}, {Name: "TODO", ID: "two"}}}
	o, e := statusOption(b, str("Todo"))
	if e != nil || o.ID != "one" {
		t.Fatal(o, e)
	}
	_, e = statusOption(b, str("todo"))
	assertKind(t, e, domain.ErrInvalidInput)
	b.Options = b.Options[:1]
	o, e = statusOption(b, str("todo"))
	if e != nil || o.Name != "Todo" {
		t.Fatal(o, e)
	}
}
func TestEdit_OnlySuppliedFields(t *testing.T) {
	s, b := writeFixture()
	change, e := s.EditIssue(context.Background(), 1, domain.EditIssue{Body: str("")})
	if e != nil || !change.Changed || b.patch.Title != nil || b.patch.AssigneeIDs != nil || b.patch.Body == nil || *b.patch.Body != "" {
		t.Fatal(change, b.patch, e)
	}
}
func TestAssignment_SilentIgnoreForbidden(t *testing.T) {
	for _, method := range []string{"add", "remove", "replace"} {
		s, b := writeFixture()
		b.ignoreAssignment = true
		var e error
		switch method {
		case "add":
			_, e = s.UpdateAssignees(context.Background(), 1, []string{"alice"}, nil)
		case "remove":
			_, e = s.UpdateAssignees(context.Background(), 1, nil, []string{"test"})
		case "replace":
			_, e = s.Reassign(context.Background(), 1, []string{"alice"})
		}
		assertKind(t, e, domain.ErrForbidden)
	}
}
func TestCreate_AssignmentFailurePartial(t *testing.T) {
	s, b := writeFixture()
	b.ignoreAssignment = true
	change, e := s.CreateIssue(context.Background(), domain.NewIssue{Title: "create", Assignees: []string{"alice"}, Status: str("Todo")})
	de := assertKind(t, e, domain.ErrPartial)
	if de.Cause != domain.ErrForbidden || !reflect.DeepEqual(de.Completed, []string{"created"}) || de.Issue == nil || change.Issue.Number != 1 || !reflect.DeepEqual(b.writes, []string{"CreateIssue"}) {
		t.Fatal(change, de, b.writes)
	}
}
func TestNoOp_ClosedSkipsComment(t *testing.T) {
	s, b := writeFixture()
	b.detail.Issue.State = "closed"
	c, e := s.Close(context.Background(), 1, domain.CloseOptions{Comment: str("do not post")})
	if e != nil || c.Changed || len(b.writes) > 0 {
		t.Fatal(c, e, b.writes)
	}
}
func TestClose_CommentThenFailurePartial(t *testing.T) {
	s, b := writeFixture()
	b.fail["CloseIssue"] = &domain.Error{Kind: domain.ErrTimeout, Message: "timeout", Uncertain: true, Err: context.Canceled}
	c, e := s.Close(context.Background(), 1, domain.CloseOptions{Comment: str("why")})
	de := assertKind(t, e, domain.ErrPartial)
	if !de.Uncertain || de.Cause != domain.ErrTimeout || !errors.Is(e, context.Canceled) || !reflect.DeepEqual(de.Completed, []string{"commented"}) || de.Issue.CommentCount != 1 || c.Issue.State != "open" {
		t.Fatal(c, de)
	}
}
func TestPicker_RemovedThenAddFailureInternalStep(t *testing.T) {
	for _, labels := range []bool{false, true} {
		s, b := writeFixture()
		var e error
		if labels {
			b.fail["AddLabels"] = &domain.Error{Kind: domain.ErrForbidden, Message: "denied"}
			_, e = s.UpdateLabels(context.Background(), 1, []string{"docs"}, []string{"bug"})
		} else {
			b.fail["AddAssignees"] = &domain.Error{Kind: domain.ErrNetwork, Message: "network", Uncertain: true}
			_, e = s.UpdateAssignees(context.Background(), 1, []string{"alice"}, []string{"test"})
		}
		de := assertKind(t, e, domain.ErrPartial)
		if !reflect.DeepEqual(de.Completed, []string{"removed"}) || de.Issue == nil {
			t.Fatal(de)
		}
	}
}
func TestReassign_OnlyAssigneeField(t *testing.T) {
	s, b := writeFixture()
	_, e := s.Reassign(context.Background(), 1, nil)
	if e != nil || b.patch.AssigneeIDs == nil || len(*b.patch.AssigneeIDs) != 0 || b.patch.Title != nil || b.patch.Body != nil {
		t.Fatal(b.patch, e)
	}
}
func TestMove_AbsentAddThenSet(t *testing.T) {
	s, b := writeFixture()
	c, e := s.SetStatus(context.Background(), 1, str("todo"))
	if e != nil || !c.Changed || c.Issue.Board == nil || *c.Issue.Board.Status != "Todo" || !reflect.DeepEqual(b.writes, []string{"AddProjectItem", "SetProjectStatus"}) {
		t.Fatal(c, e, b.writes)
	}
}
func TestMove_ArchivedRestoreThenSet(t *testing.T) {
	s, b := writeFixture()
	member(b, true, str("Done"))
	c, e := s.SetStatus(context.Background(), 1, str("Todo"))
	if e != nil || !c.Changed || *c.Issue.Board.Status != "Todo" || !reflect.DeepEqual(b.writes, []string{"UnarchiveProjectItem", "SetProjectStatus"}) {
		t.Fatal(c, e, b.writes)
	}
}
func TestBoardAdd_ExistingStatusPreserved(t *testing.T) {
	for _, archived := range []bool{false, true} {
		s, b := writeFixture()
		member(b, archived, str("Done"))
		c, e := s.BoardAdd(context.Background(), 1, nil)
		if e != nil || c.Changed != archived || *c.Issue.Board.Status != "Done" || len(b.writes) != map[bool]int{false: 0, true: 1}[archived] {
			t.Fatal(c, e, b.writes)
		}
	}
}
func TestBoardAdd_NewDefaultStatusCleared(t *testing.T) {
	s, b := writeFixture()
	b.defaultStatus = str("Todo")
	c, e := s.BoardAdd(context.Background(), 1, nil)
	if e != nil || !c.Changed || c.Issue.Board == nil || c.Issue.Board.Status != nil || !reflect.DeepEqual(b.writes, []string{"AddProjectItem", "ClearProjectStatus"}) {
		t.Fatal(c, e, b.writes)
	}
}
func TestRestoreFailurePartialAddedToBoard(t *testing.T) {
	s, b := writeFixture()
	member(b, true, str("Done"))
	b.fail["SetProjectStatus"] = &domain.Error{Kind: domain.ErrNetwork, Message: "reset", Uncertain: true}
	c, e := s.SetStatus(context.Background(), 1, str("Todo"))
	de := assertKind(t, e, domain.ErrPartial)
	if !de.Uncertain || !reflect.DeepEqual(de.Completed, []string{"added_to_board"}) || c.Issue.Board == nil || *de.Issue.Board.Status != "Done" {
		t.Fatal(c, de)
	}
}
func TestRemove_AbsentNoOp(t *testing.T) {
	s, b := writeFixture()
	c, e := s.RemoveFromBoard(context.Background(), 1)
	if e != nil || c.Changed || len(b.writes) > 0 {
		t.Fatal(c, e, b.writes)
	}
}
func TestIssueRead_ArchivedNeverUnarchives(t *testing.T) {
	s, b := writeFixture()
	member(b, true, str("Done"))
	_, e := s.GetIssue(context.Background(), 1)
	if e != nil || len(b.writes) > 0 {
		t.Fatal(e, b.writes)
	}
}
func TestCreate_AddToBoardRulesAndPartial(t *testing.T) {
	for _, mode := range []string{"absent", "no-status", "status", "place-fails", "set-fails"} {
		s, b := writeFixture()
		in := domain.NewIssue{Title: "new"}
		switch mode {
		case "no-status":
			in.AddToBoard = true
		case "status", "place-fails", "set-fails":
			in.Status = str("todo")
		}
		if mode == "place-fails" {
			b.fail["AddProjectItem"] = &domain.Error{Kind: domain.ErrForbidden, Message: "denied"}
		}
		if mode == "set-fails" {
			b.fail["SetProjectStatus"] = &domain.Error{Kind: domain.ErrTimeout, Message: "timeout", Uncertain: true}
		}
		c, e := s.CreateIssue(context.Background(), in)
		if strings.HasSuffix(mode, "fails") {
			de := assertKind(t, e, domain.ErrPartial)
			want := []string{"created"}
			if mode == "set-fails" {
				want = append(want, "added_to_board")
			}
			if !reflect.DeepEqual(de.Completed, want) || de.Issue == nil {
				t.Fatal(de)
			}
		} else if e != nil || !c.Changed || (c.Issue.Board != nil) != (mode != "absent") {
			t.Fatal(mode, c, e)
		}
	}
}
func TestMutations_NoOpsAndValidation(t *testing.T) {
	ctx := context.Background()
	for _, op := range []string{"edit", "assign", "labels", "reopen", "status"} {
		s, b := writeFixture()
		var c domain.Change
		var e error
		switch op {
		case "edit":
			c, e = s.EditIssue(ctx, 1, domain.EditIssue{Title: str("original"), Body: str("body")})
		case "assign":
			c, e = s.UpdateAssignees(ctx, 1, []string{"@me"}, []string{"alice"})
		case "labels":
			c, e = s.UpdateLabels(ctx, 1, []string{"bug"}, []string{"docs"})
		case "reopen":
			c, e = s.Reopen(ctx, 1)
		case "status":
			member(b, false, str("Todo"))
			c, e = s.SetStatus(ctx, 1, str("Todo"))
		}
		if e != nil || c.Changed || len(b.writes) > 0 {
			t.Fatal(op, c, e, b.writes)
		}
	}
	s, b := writeFixture()
	_, e := s.UpdateAssignees(ctx, 1, []string{"@me"}, []string{"TEST"})
	assertKind(t, e, domain.ErrInvalidInput)
	_, e = s.UpdateLabels(ctx, 1, []string{"bug"}, []string{"bug"})
	assertKind(t, e, domain.ErrInvalidInput)
	if len(b.writes) > 0 {
		t.Fatal(b.writes)
	}
}
func TestMutations_AcknowledgedNoRereadAndWorkflowNormal(t *testing.T) {
	s, b := writeFixture()
	member(b, false, str("Todo"))
	c, e := s.EditIssue(context.Background(), 1, domain.EditIssue{Title: str("new")})
	if e != nil || c.Issue.Title != "new" || c.Issue.Board == nil || b.count("Issue") != 1 {
		t.Fatal(c, e, b.calls)
	}
	s, b = writeFixture()
	b.closeOnComment = true
	c, e = s.Close(context.Background(), 1, domain.CloseOptions{Comment: str("note")})
	if e != nil || !c.Changed || c.Issue.State != "closed" || !reflect.DeepEqual(b.writes, []string{"AddComment"}) {
		t.Fatal(c, e, b.writes)
	}
}
func TestMutations_CommentReopenDeleteRemove(t *testing.T) {
	s, b := writeFixture()
	r, e := s.Comment(context.Background(), 1, "hello")
	if e != nil || r.Comment.Body != "hello" || r.Issue.CommentCount != 1 {
		t.Fatal(r, e)
	}
	b.detail.Issue.State = "closed"
	c, e := s.Reopen(context.Background(), 1)
	if e != nil || !c.Changed || c.Issue.State != "open" {
		t.Fatal(c, e)
	}
	member(b, true, str("Done"))
	c, e = s.RemoveFromBoard(context.Background(), 1)
	if e != nil || !c.Changed || c.Issue.Board != nil {
		t.Fatal(c, e)
	}
	if e := s.Delete(context.Background(), 1); e != nil || b.writes[len(b.writes)-1] != "DeleteIssue" {
		t.Fatal(e, b.writes)
	}
}

func TestNames_BlankAndCaseRules(t *testing.T) {
	for _, which := range []string{"user", "label", "label-case"} {
		s, b := writeFixture()
		var e error
		switch which {
		case "user":
			_, e = s.Reassign(context.Background(), 1, []string{""})
		case "label":
			_, e = s.UpdateLabels(context.Background(), 1, []string{""}, nil)
		case "label-case":
			_, e = s.UpdateLabels(context.Background(), 1, []string{"BUG"}, nil)
		}
		assertKind(t, e, domain.ErrInvalidInput)
		if len(b.writes) > 0 {
			t.Fatal(b.writes)
		}
	}
}
func TestAssignees_RemovalMakesRoomAtTen(t *testing.T) {
	s, b := writeFixture()
	b.detail.Issue.Assignees = []string{}
	for j := 0; j < 10; j++ {
		name := fmt.Sprintf("user%d", j)
		b.users = append(b.users, domain.User{Login: name, NodeID: domain.NodeID(name)})
		b.detail.Issue.Assignees = append(b.detail.Issue.Assignees, name)
		b.detail.Issue.AssigneeIDs[name] = domain.NodeID(name)
	}
	c, e := s.UpdateAssignees(context.Background(), 1, []string{"alice"}, []string{"user0"})
	if e != nil || len(c.Issue.Assignees) != 10 || hasLogin(c.Issue.Assignees, "user0") || !hasLogin(c.Issue.Assignees, "alice") || !reflect.DeepEqual(b.writes, []string{"RemoveAssignees", "AddAssignees"}) {
		t.Fatal(c, e, b.writes)
	}
}
func TestCreate_FirstAmbiguousFailureNotPartial(t *testing.T) {
	s, b := writeFixture()
	b.fail["CreateIssue"] = &domain.Error{Kind: domain.ErrTimeout, Message: "timeout", Uncertain: true}
	_, e := s.CreateIssue(context.Background(), domain.NewIssue{Title: "title"})
	de := assertKind(t, e, domain.ErrTimeout)
	if !de.Uncertain || de.Issue != nil || !reflect.DeepEqual(b.writes, []string{"CreateIssue"}) {
		t.Fatal(de, b.writes)
	}
}
func TestBoardWrites_FirstFailureCertainOrUncertainUnchanged(t *testing.T) {
	for _, archived := range []bool{false, true} {
		s, b := writeFixture()
		method := "AddProjectItem"
		if archived {
			member(b, true, str("Done"))
			method = "UnarchiveProjectItem"
		}
		b.fail[method] = &domain.Error{Kind: domain.ErrTimeout, Message: "timeout", Uncertain: true}
		_, e := s.SetStatus(context.Background(), 1, str("Todo"))
		de := assertKind(t, e, domain.ErrTimeout)
		if !de.Uncertain || len(b.writes) != 1 {
			t.Fatal(de, b.writes)
		}
	}
}
func TestBoardWrites_ClearArchivedAndRemoveArchived(t *testing.T) {
	s, b := writeFixture()
	member(b, true, str("Done"))
	c, e := s.SetStatus(context.Background(), 1, nil)
	if e != nil || !c.Changed || c.Issue.Board == nil || c.Issue.Board.Status != nil || !reflect.DeepEqual(b.writes, []string{"UnarchiveProjectItem", "ClearProjectStatus"}) {
		t.Fatal(c, e, b.writes)
	}
	s, b = writeFixture()
	member(b, true, str("Done"))
	c, e = s.RemoveFromBoard(context.Background(), 1)
	if e != nil || !c.Changed || c.Issue.Board != nil || !reflect.DeepEqual(b.writes, []string{"RemoveProjectItem"}) {
		t.Fatal(c, e, b.writes)
	}
}

func TestAssignment_IgnoredRemovalStopsBeforeAddition(t *testing.T) {
	s, b := writeFixture()
	b.ignoreAssignment = true
	_, e := s.UpdateAssignees(context.Background(), 1, []string{"alice"}, []string{"test"})
	assertKind(t, e, domain.ErrForbidden)
	if !reflect.DeepEqual(b.writes, []string{"RemoveAssignees"}) {
		t.Fatal(b.writes)
	}
}

func TestRemovals_DepartedAssigneeUsesIssueID(t *testing.T) {
	for _, repoKnows := range []bool{false, true} {
		t.Run(fmt.Sprint(repoKnows), func(t *testing.T) {
			s, b := writeFixture()
			b.detail.Issue.Assignees = []string{"Departed"}
			b.detail.Issue.AssigneeIDs = map[string]domain.NodeID{"Departed": "ISSUE_USER"}
			if repoKnows {
				b.users = append(b.users, domain.User{Login: "Departed", NodeID: "WRONG_REPO_USER"})
			}
			c, e := s.UpdateAssignees(context.Background(), 1, nil, []string{"DEPARTED", "departed"})
			if e != nil || !c.Changed || len(c.Issue.Assignees) != 0 || !reflect.DeepEqual(b.removedUserIDs, []domain.NodeID{"ISSUE_USER"}) || b.count("AssignableUsers") != 0 {
				t.Fatal(c, e, b.removedUserIDs, b.calls)
			}
		})
	}
}
func TestRemovals_DeletedLabelUsesIssueID(t *testing.T) {
	for _, repoKnows := range []bool{false, true} {
		s, b := writeFixture()
		b.detail.Issue.Labels = []domain.Label{{Name: "deleted", NodeID: "ISSUE_LABEL"}}
		if repoKnows {
			b.labels = append(b.labels, domain.Label{Name: "deleted", NodeID: "WRONG_REPO_LABEL"})
		}
		c, e := s.UpdateLabels(context.Background(), 1, nil, []string{"deleted", "deleted"})
		if e != nil || !c.Changed || len(c.Issue.Labels) != 0 || !reflect.DeepEqual(b.removedLabelIDs, []domain.NodeID{"ISSUE_LABEL"}) || b.count("Labels") != 0 {
			t.Fatal(c, e, b.removedLabelIDs, b.calls)
		}
	}
}
func TestRemovals_AbsentKnownNoOpUnknownRejectsBeforeAnyWrite(t *testing.T) {
	for _, labels := range []bool{false, true} {
		for _, known := range []bool{false, true} {
			s, b := writeFixture()
			name := "unknown"
			if known {
				name = "alice"
				if labels {
					name = "docs"
				}
			}
			var c domain.Change
			var e error
			if labels {
				c, e = s.UpdateLabels(context.Background(), 1, nil, []string{name})
			} else {
				c, e = s.UpdateAssignees(context.Background(), 1, nil, []string{name})
			}
			if known {
				if e != nil || c.Changed {
					t.Fatal(c, e)
				}
			} else {
				assertKind(t, e, domain.ErrInvalidInput)
			}
			if len(b.writes) != 0 {
				t.Fatal(b.writes)
			}
		}
	}
	s, b := writeFixture()
	b.detail.Issue.Assignees = []string{"departed"}
	b.detail.Issue.AssigneeIDs = map[string]domain.NodeID{"departed": "DEPARTED"}
	_, e := s.UpdateAssignees(context.Background(), 1, nil, []string{"departed", "typo"})
	assertKind(t, e, domain.ErrInvalidInput)
	if len(b.writes) > 0 {
		t.Fatal(b.writes)
	}
	s, b = writeFixture()
	b.detail.Issue.Labels = []domain.Label{{Name: "deleted", NodeID: "DELETED"}}
	_, e = s.UpdateLabels(context.Background(), 1, nil, []string{"deleted", "typo"})
	assertKind(t, e, domain.ErrInvalidInput)
	if len(b.writes) > 0 {
		t.Fatal(b.writes)
	}
}
func TestRemovals_DoNotRelaxAdditions(t *testing.T) {
	s, b := writeFixture()
	b.detail.Issue.Assignees = []string{"departed"}
	b.detail.Issue.AssigneeIDs = map[string]domain.NodeID{"departed": "DEPARTED"}
	_, e := s.UpdateAssignees(context.Background(), 1, []string{"departed"}, nil)
	assertKind(t, e, domain.ErrInvalidInput)
	_, e = s.UpdateLabels(context.Background(), 1, []string{"deleted"}, nil)
	assertKind(t, e, domain.ErrInvalidInput)
	if len(b.writes) > 0 {
		t.Fatal(b.writes)
	}
}
func TestRemovals_MixedDeltasExpandMeAndPreservePartial(t *testing.T) {
	s, b := writeFixture()
	b.detail.Issue.Assignees = []string{"test"}
	b.detail.Issue.AssigneeIDs = map[string]domain.NodeID{"test": "ISSUE_VIEWER"}
	b.users = b.users[1:]
	b.fail["AddAssignees"] = &domain.Error{Kind: domain.ErrForbidden, Message: "denied"}
	_, e := s.UpdateAssignees(context.Background(), 1, []string{"alice"}, []string{"@me"})
	de := assertKind(t, e, domain.ErrPartial)
	if !reflect.DeepEqual(b.removedUserIDs, []domain.NodeID{"ISSUE_VIEWER"}) || !reflect.DeepEqual(de.Completed, []string{"removed"}) || len(de.Issue.Assignees) > 0 {
		t.Fatal(de, b.removedUserIDs)
	}
}
