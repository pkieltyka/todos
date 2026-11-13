package servicetest_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/pkieltyka/todos/internal/domain"
	"github.com/pkieltyka/todos/internal/service/servicetest"
)

func TestFake_CreateEditAndDelete(t *testing.T) {
	f := fresh()
	ctx := context.Background()
	created, err := f.CreateIssue(ctx, domain.NewIssue{Title: "New issue", Body: "**Body**", Assignees: []string{"@me"}, Labels: []string{"docs"}, AddToBoard: true})
	if err != nil || !created.Changed || created.Issue.Number != 104 || created.Issue.Board == nil || created.Issue.Board.Status != nil || created.Issue.Assignees[0] != "pkieltyka" {
		t.Fatalf("create %+v %v", created, err)
	}
	d, err := f.GetIssue(ctx, 104)
	if err != nil || d.Body != "**Body**" {
		t.Fatal(err)
	}
	c, err := f.EditIssue(ctx, 104, domain.EditIssue{Body: pointer("")})
	if err != nil || !c.Changed || c.Issue.Title != "New issue" {
		t.Fatal("edit overwrote unsupplied field")
	}
	c, err = f.EditIssue(ctx, 104, domain.EditIssue{Body: pointer("")})
	if err != nil || c.Changed {
		t.Fatal("identical edit not no-op")
	}
	if err = f.Delete(ctx, 104); err != nil {
		t.Fatal(err)
	}
	_, err = f.GetIssue(ctx, 104)
	wantKind(t, err, domain.ErrNotFound)
	b, err := f.Board(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, col := range b.Columns {
		for _, card := range col.Cards {
			if card.Number == 104 {
				t.Fatal("delete left board card")
			}
		}
	}
	c, err = f.CreateIssue(ctx, domain.NewIssue{Title: "Next"})
	if err != nil || c.Issue.Number != 105 {
		t.Fatal("reused deleted number")
	}
}
func TestFake_AssigneeAndLabelSetSemantics(t *testing.T) {
	f := fresh()
	f.Mutate(func(x *servicetest.Fixture) {
		x.Users = append(x.Users, domain.User{Login: "absent", NodeID: "ABSENT"})
		x.Labels = append(x.Labels, domain.Label{Name: "absent", NodeID: "ABSENT_LABEL"})
	})
	ctx := context.Background()
	before, _ := f.GetIssue(ctx, 6)
	change, err := f.UpdateAssignees(ctx, 6, []string{"@me", "pkieltyka"}, []string{"reviewer", "absent"})
	if err != nil || !change.Changed || !reflect.DeepEqual(change.Issue.Assignees, []string{"pkieltyka"}) {
		t.Fatal(change, err)
	}
	change, err = f.UpdateAssignees(ctx, 6, []string{"@me"}, []string{"absent"})
	if err != nil || change.Changed {
		t.Fatal("assignment not idempotent")
	}
	change, err = f.Reassign(ctx, 6, []string{"reviewer", "reviewer"})
	if err != nil || len(change.Issue.Assignees) != 1 || change.Issue.Assignees[0] != "reviewer" {
		t.Fatal(change, err)
	}
	if !reflect.DeepEqual(before.Issue.Labels, change.Issue.Labels) {
		t.Fatal("reassign overwrote labels")
	}
	change, err = f.UpdateLabels(ctx, 6, []string{"docs", "docs"}, []string{"bug", "absent"})
	if err != nil || !change.Changed || len(change.Issue.Labels) != 2 || change.Issue.Labels[1].Name != "docs" {
		t.Fatal(change, err)
	}
	change, err = f.UpdateLabels(ctx, 6, []string{"docs"}, []string{"absent"})
	if err != nil || change.Changed {
		t.Fatal("label delta not idempotent")
	}
	if change.Issue.Assignees[0] != "reviewer" {
		t.Fatal("label delta overwrote assignees")
	}
}
func TestNoOp_ClosedSkipsComment(t *testing.T) {
	f := fresh()
	ctx := context.Background()
	first, err := f.Close(ctx, 6, domain.CloseOptions{Reason: "not_planned", Comment: pointer("Closing comment")})
	if err != nil || !first.Changed || first.Issue.StateReason == nil || *first.Issue.StateReason != "not_planned" || first.Issue.ClosedAt == nil {
		t.Fatal(first, err)
	}
	comments, _ := f.ListIssueComments(ctx, 6, domain.PageRequest{})
	if comments.Total != 2 {
		t.Fatal("closing comment missing")
	}
	second, err := f.Close(ctx, 6, domain.CloseOptions{Reason: "completed", Comment: pointer("Must not post")})
	if err != nil || second.Changed {
		t.Fatal("closing closed issue changed it")
	}
	comments, _ = f.ListIssueComments(ctx, 6, domain.PageRequest{})
	if comments.Total != 2 {
		t.Fatal("no-op posted another comment")
	}
	r, err := f.Reopen(ctx, 6)
	if err != nil || !r.Changed || r.Issue.ClosedAt != nil || *r.Issue.StateReason != "reopened" {
		t.Fatal(r, err)
	}
	r, err = f.Reopen(ctx, 6)
	if err != nil || r.Changed {
		t.Fatal("reopening open issue changed it")
	}
}
func TestFake_CommentResultAndOwnership(t *testing.T) {
	f := fresh()
	ctx := context.Background()
	r, err := f.Comment(ctx, 6, "Comment body")
	if err != nil || r.Comment.Body != "Comment body" || r.Comment.ID == "" || r.Issue.CommentCount != 2 {
		t.Fatal(r, err)
	}
	*r.Comment.Author = "tamper"
	r.Issue.Labels[0].Name = "tamper"
	comments, _ := f.ListIssueComments(ctx, 6, domain.PageRequest{})
	if *comments.Items[len(comments.Items)-1].Author == "tamper" {
		t.Fatal("comment output aliases storage")
	}
}
func TestMove_AbsentAddThenSet(t *testing.T) {
	f := fresh()
	ctx := context.Background()
	r, err := f.SetStatus(ctx, 1, pointer("Done"))
	if err != nil || !r.Changed || r.Issue.Board == nil || *r.Issue.Board.Status != "Done" {
		t.Fatal(r, err)
	}
	r, err = f.SetStatus(ctx, 1, pointer("Done"))
	if err != nil || r.Changed {
		t.Fatal("same move not a no-op")
	}
	r, err = f.SetStatus(ctx, 1, nil)
	if err != nil || !r.Changed || r.Issue.Board == nil || r.Issue.Board.Status != nil {
		t.Fatal("clear removed membership")
	}
	b, err := f.Board(ctx)
	if err != nil || b.Columns[0].Status != nil {
		t.Fatal("No status must be first")
	}
	if r.Issue.UpdatedAt.IsZero() {
		t.Fatal("lost issue metadata")
	}
}
func TestBoardAdd_ExistingStatusPreserved(t *testing.T) {
	f := fresh()
	ctx := context.Background()
	i, _ := f.GetIssue(ctx, 4)
	before := i.Issue.UpdatedAt
	r, err := f.BoardAdd(ctx, 4, nil)
	if err != nil || r.Changed || r.Issue.Board == nil || *r.Issue.Board.Status != "In progress" {
		t.Fatal(r, err)
	}
	r, err = f.SetStatus(ctx, 4, pointer("Done"))
	if err != nil || !r.Issue.UpdatedAt.Equal(before) {
		t.Fatal("board move changed issue updated_at")
	}
	r, err = f.BoardAdd(ctx, 1, nil)
	if err != nil || !r.Changed || r.Issue.Board == nil || r.Issue.Board.Status != nil {
		t.Fatal("new card not added without status")
	}
}
func TestMove_ArchivedRestoreThenSet(t *testing.T) {
	f := fresh()
	ctx := context.Background()
	f.Mutate(func(x *servicetest.Fixture) { x.Archived[4] = *x.Issues[2].Issue.Board })
	i, _ := f.GetIssue(ctx, 4)
	if i.Issue.Board != nil {
		t.Fatal("archived placement exposed as active")
	}
	b, _ := f.Board(ctx)
	for _, col := range b.Columns {
		for _, card := range col.Cards {
			if card.Number == 4 {
				t.Fatal("read restored archived card")
			}
		}
	}
	r, err := f.SetStatus(ctx, 4, pointer("In progress"))
	if err != nil || !r.Changed || r.Issue.Board == nil {
		t.Fatal("same-status archive not restored")
	}
	f.Mutate(func(x *servicetest.Fixture) { x.Archived[4] = *x.Issues[2].Issue.Board })
	r, err = f.BoardAdd(ctx, 4, nil)
	if err != nil || !r.Changed || *r.Issue.Board.Status != "In progress" {
		t.Fatal("restore lost existing status")
	}
}
func TestRemove_AbsentNoOp(t *testing.T) {
	f := fresh()
	ctx := context.Background()
	r, err := f.RemoveFromBoard(ctx, 1)
	if err != nil || r.Changed {
		t.Fatal("absent removal not no-op")
	}
	r, err = f.RemoveFromBoard(ctx, 6)
	if err != nil || !r.Changed || r.Issue.Board != nil {
		t.Fatal(r, err)
	}
	r, err = f.RemoveFromBoard(ctx, 6)
	if err != nil || r.Changed {
		t.Fatal("repeated removal not no-op")
	}
}
func TestFake_FixtureBoardTruncationAndSelection(t *testing.T) {
	fixture := servicetest.DefaultFixture()
	fixture.Issues = servicetest.GenIssues(600)
	fixture.Board.Columns = []domain.Column{{Status: pointer("Todo"), Cards: []domain.Card{}}}
	fixture.Board.Truncated = true
	for i := range fixture.Issues {
		fixture.Issues[i].Issue.Board = &domain.Placement{ItemID: domain.NodeID(fixture.Issues[i].Issue.URL), Status: pointer("Todo")}
	}
	f := servicetest.New(fixture)
	b, err := f.Board(context.Background())
	if err != nil || !b.Truncated || !b.CardsTruncated || b.VisibleCount != 600 || len(b.Columns[0].Cards) != 600 {
		t.Fatal("fake lost fixture truncation/visible data")
	}
	f.Mutate(func(x *servicetest.Fixture) {
		p := x.Projects[0]
		p.Ref.Number = 2
		p.NodeID = "PROJECT_2"
		p.Title = "Other"
		x.Projects = append(x.Projects, p)
	})
	p, err := f.UseBoard(context.Background(), domain.ProjectRef{Owner: "pkieltyka", Number: 2})
	if err != nil || p.Title != "Other" {
		t.Fatal(p, err)
	}
	info, _ := f.BoardInfo(context.Background())
	if info.Project.Ref.Number != 2 {
		t.Fatal("selection not changed")
	}
}
func TestFake_MissingWriteTargets(t *testing.T) {
	f := fresh()
	ctx := context.Background()
	for name, fn := range map[string]func() error{
		"edit": func() error { _, e := f.EditIssue(ctx, 999, domain.EditIssue{}); return e }, "assign": func() error { _, e := f.UpdateAssignees(ctx, 999, nil, nil); return e }, "label": func() error { _, e := f.UpdateLabels(ctx, 999, nil, nil); return e }, "close": func() error { _, e := f.Close(ctx, 999, domain.CloseOptions{}); return e }, "reopen": func() error { _, e := f.Reopen(ctx, 999); return e }, "comment": func() error { _, e := f.Comment(ctx, 999, "text"); return e }, "delete": func() error { return f.Delete(ctx, 999) }, "move": func() error { _, e := f.SetStatus(ctx, 999, nil); return e }, "add board": func() error { _, e := f.BoardAdd(ctx, 999, nil); return e }, "remove board": func() error { _, e := f.RemoveFromBoard(ctx, 999); return e },
	} {
		t.Run(name, func(t *testing.T) { wantKind(t, fn(), domain.ErrNotFound) })
	}
}

func TestFake_StatusResolvesCanonicalOption(t *testing.T) {
	ctx := context.Background()
	for _, operation := range []string{"SetStatus", "BoardAdd", "CreateIssue"} {
		t.Run(operation, func(t *testing.T) {
			f := fresh()
			var change domain.Change
			var err error
			switch operation {
			case "SetStatus":
				change, err = f.SetStatus(ctx, 1, pointer("todo"))
			case "BoardAdd":
				change, err = f.BoardAdd(ctx, 1, pointer("todo"))
			case "CreateIssue":
				change, err = f.CreateIssue(ctx, domain.NewIssue{Title: "New", Status: pointer("todo")})
			}
			if err != nil || change.Issue.Board == nil || *change.Issue.Board.Status != "Todo" || change.Issue.Board.OptionID != "todo" {
				t.Fatalf("canonical placement: %+v %v", change, err)
			}
			again, err := f.SetStatus(ctx, change.Issue.Number, pointer("TODO"))
			if err != nil || again.Changed {
				t.Fatalf("case-folded no-op: %+v %v", again, err)
			}
			board, err := f.Board(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for _, col := range board.Columns {
				if col.Status != nil && *col.Status == "todo" {
					t.Fatal("duplicate noncanonical column")
				}
			}
		})
	}
	f := fresh()
	f.Mutate(func(x *servicetest.Fixture) {
		x.BoardInfo.Options = append(x.BoardInfo.Options, domain.StatusOption{ID: "other", Name: "TODO"})
	})
	exact, err := f.SetStatus(ctx, 1, pointer("TODO"))
	if err != nil || exact.Issue.Board.OptionID != "other" {
		t.Fatal("exact match must win", err)
	}
	_, err = f.SetStatus(ctx, 1, pointer("tOdO"))
	wantKind(t, err, domain.ErrInvalidInput)
	_, err = f.SetStatus(ctx, 1, pointer("unknown"))
	wantKind(t, err, domain.ErrInvalidInput)
}

func TestFake_RemovesDepartedAssigneeAndDeletedLabel(t *testing.T) {
	f := fresh()
	f.Mutate(func(x *servicetest.Fixture) {
		x.Users = nil
		x.Labels = nil
		for j := range x.Issues {
			if x.Issues[j].Issue.Number == 6 {
				x.Issues[j].Issue.Assignees = []string{"Departed"}
				x.Issues[j].Issue.AssigneeIDs = map[string]domain.NodeID{"Departed": "DEPARTED"}
				x.Issues[j].Issue.Labels = []domain.Label{{Name: "deleted", NodeID: "DELETED"}}
			}
		}
	})
	c, e := f.UpdateAssignees(context.Background(), 6, nil, []string{"DEPARTED"})
	if e != nil || !c.Changed || len(c.Issue.Assignees) > 0 || len(c.Issue.AssigneeIDs) > 0 {
		t.Fatal(c, e)
	}
	c, e = f.UpdateLabels(context.Background(), 6, nil, []string{"deleted"})
	if e != nil || !c.Changed || len(c.Issue.Labels) > 0 {
		t.Fatal(c, e)
	}
	_, e = f.UpdateAssignees(context.Background(), 6, nil, []string{"typo"})
	wantKind(t, e, domain.ErrInvalidInput)
	_, e = f.UpdateLabels(context.Background(), 6, nil, []string{"typo"})
	wantKind(t, e, domain.ErrInvalidInput)
}
