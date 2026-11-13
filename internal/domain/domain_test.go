package domain

import (
	"context"
	"errors"
	"testing"
	"time"
)

func pointer[T any](v T) *T { return &v }

func TestError_ErrorUnwrapAndAs(t *testing.T) {
	cause := &Error{Kind: ErrNetwork, Message: "operation interrupted", Err: context.Canceled}
	e := &Error{Kind: ErrPartial, Message: "created but not placed", Cause: ErrNetwork, Completed: []string{"created"}, Err: cause}
	if e.Error() != "created but not placed" || !errors.Is(e, context.Canceled) {
		t.Fatal("lost safe message or wrapped cancellation")
	}
	var got *Error
	if !errors.As(e, &got) || got != e {
		t.Fatal("errors.As did not find domain error")
	}
	var nilError *Error
	if nilError.Error() != "" || nilError.Unwrap() != nil {
		t.Fatal("nil error methods are not safe")
	}
}
func TestErrorKinds_Exhaustive(t *testing.T) {
	kinds := []ErrorKind{ErrNetwork, ErrTimeout, ErrGitHub, ErrInternal, ErrUsage, ErrInvalidInput, ErrNotFound, ErrAuth, ErrMissingScope, ErrForbidden, ErrRateLimited, ErrNoRepo, ErrNoBoard, ErrIssuesDisabled, ErrConfig, ErrPartial}
	want := []string{"network", "timeout", "github", "internal", "usage", "invalid_input", "not_found", "auth", "missing_scope", "forbidden", "rate_limited", "no_repo", "no_board", "issues_disabled", "config", "partial"}
	for i, k := range kinds {
		if string(k) != want[i] {
			t.Fatalf("kind %d: %q", i, k)
		}
	}
}
func TestCloneIssue_IndependentNestedValues(t *testing.T) {
	now := time.Now()
	original := Issue{Author: pointer("author"), Assignees: []string{"one"}, Labels: []Label{{Name: "bug"}}, Milestone: pointer("v1"), StateReason: pointer(StateReason("completed")), ClosedAt: &now, Board: &Placement{ItemID: "item", Status: pointer("Todo")}}
	clone := CloneIssue(original)
	*clone.Author = "changed"
	clone.Assignees[0] = "two"
	clone.Labels[0].Name = "docs"
	*clone.Milestone = "v2"
	*clone.StateReason = "not_planned"
	*clone.ClosedAt = now.Add(time.Hour)
	*clone.Board.Status = "Done"
	if *original.Author != "author" || original.Assignees[0] != "one" || original.Labels[0].Name != "bug" || *original.Milestone != "v1" || *original.StateReason != "completed" || !original.ClosedAt.Equal(now) || *original.Board.Status != "Todo" {
		t.Fatal("clone aliases issue data")
	}
	pr := PullRequest{Author: pointer("one"), Assignees: []string{"one"}, Labels: []Label{{Name: "bug"}}, ClosedAt: &now, MergedAt: &now}
	cp := ClonePR(pr)
	*cp.Author = "two"
	cp.Assignees[0] = "two"
	cp.Labels[0].Name = "docs"
	*cp.MergedAt = now.Add(time.Hour)
	if *pr.Author != "one" || pr.Assignees[0] != "one" || pr.Labels[0].Name != "bug" || !pr.MergedAt.Equal(now) {
		t.Fatal("clone aliases PR data")
	}
}
func TestCloneBoard_IndependentCards(t *testing.T) {
	b := Board{Columns: []Column{{Status: pointer("Todo"), Cards: []Card{{Issue: Issue{Labels: []Label{{Name: "bug"}}, Board: &Placement{Status: pointer("Todo")}}}}}}}
	c := CloneBoard(b)
	*c.Columns[0].Status = "Done"
	c.Columns[0].Cards[0].Labels[0].Name = "docs"
	*c.Columns[0].Cards[0].Board.Status = "Done"
	if *b.Columns[0].Status != "Todo" || b.Columns[0].Cards[0].Labels[0].Name != "bug" || *b.Columns[0].Cards[0].Board.Status != "Todo" {
		t.Fatal("clone aliases board")
	}
}
func TestCloneQuickAdd_IndependentPlanAndResults(t *testing.T) {
	plan := QuickAddPlan{Items: []QuickAddItem{{Source: "- task\r\n"}}, Problems: []Problem{{Line: 1}}, Options: QuickAddOptions{Assignees: []string{"one"}, Labels: []string{"bug"}, Status: pointer("Todo")}}
	c := ClonePlan(plan)
	c.Items[0].Source = "changed"
	c.Problems[0].Line = 2
	c.Options.Assignees[0] = "two"
	c.Options.Labels[0] = "docs"
	*c.Options.Status = "Done"
	if plan.Items[0].Source != "- task\r\n" || plan.Problems[0].Line != 1 || plan.Options.Assignees[0] != "one" || plan.Options.Labels[0] != "bug" || *plan.Options.Status != "Todo" {
		t.Fatal("clone aliases plan")
	}
	result := QuickAddResult{Items: []QuickAddItemResult{{Issue: &Issue{Assignees: []string{"one"}}, Error: &Error{Completed: []string{"created"}, Issue: &Issue{Labels: []Label{{Name: "bug"}}}, Candidates: []Project{{Title: "board"}}}}}}
	r := CloneQuickAddResult(result)
	r.Items[0].Issue.Assignees[0] = "two"
	r.Items[0].Error.Completed[0] = "changed"
	r.Items[0].Error.Issue.Labels[0].Name = "docs"
	r.Items[0].Error.Candidates[0].Title = "changed"
	if result.Items[0].Issue.Assignees[0] != "one" || result.Items[0].Error.Completed[0] != "created" || result.Items[0].Error.Issue.Labels[0].Name != "bug" || result.Items[0].Error.Candidates[0].Title != "board" {
		t.Fatal("clone aliases result/error")
	}
}
func TestPage_EmptySlicesAndRawCursor(t *testing.T) {
	p := Page[Issue]{Items: []Issue{}, NextCursor: "opaque/GitHub==", HasMore: true}
	if p.Items == nil || p.NextCursor != "opaque/GitHub==" {
		t.Fatal("page changed cursor or nil array")
	}
	i := CloneIssue(Issue{})
	if i.Assignees == nil || i.Labels == nil {
		t.Fatal("cloned empty collections must be nonnil")
	}
}
func TestPlacement_AbsentVersusNoStatus(t *testing.T) {
	absent := CloneIssue(Issue{})
	present := CloneIssue(Issue{Board: &Placement{ItemID: "item"}})
	if absent.Board != nil || present.Board == nil || present.Board.Status != nil {
		t.Fatal("absent and no-status placements collapsed")
	}
}
func TestQuickAddCounts_PlannedSeparateFromCreated(t *testing.T) {
	r := CloneQuickAddResult(QuickAddResult{Counts: QuickAddCounts{Planned: 2, Skipped: 1}})
	if r.Counts.Planned != 2 || r.Counts.Created != 0 || r.Counts.Skipped != 1 {
		t.Fatal("count fields conflated")
	}
}
func TestDraftClone_InFlightAndUncertain(t *testing.T) {
	d := Draft{Text: "- task", Revision: 3, InFlight: &DraftSpan{StartByte: 0, EndByte: 6}, Uncertain: []DraftSpan{{SourceSHA256: "hash"}}}
	c := CloneDraft(d)
	c.InFlight.EndByte = 1
	c.Uncertain[0].SourceSHA256 = "changed"
	if d.InFlight.EndByte != 6 || d.Uncertain[0].SourceSHA256 != "hash" || c.Text != d.Text || c.Revision != 3 {
		t.Fatal("draft marker clone aliases input")
	}
}
