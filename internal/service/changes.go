package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/pkieltyka/todos/internal/domain"
)

func partial(cause error, issue *domain.Issue, completed []string) *domain.Error {
	de := typed(cause).(*domain.Error)
	out := domain.CloneError(de)
	out.Kind = domain.ErrPartial
	out.Cause = de.Kind
	out.Err = de
	out.Completed = append([]string{}, completed...)
	out.Message = "change partly completed"
	if issue != nil {
		out.Message += fmt.Sprintf(" for issue #%d", issue.Number)
	}
	if len(completed) > 0 {
		out.Message += " (" + strings.Join(completed, ", ") + ")"
	}
	out.Message += ": " + de.Message
	out.Hint = "do not rerun the whole command; finish the remaining change on the known issue"
	if issue != nil {
		i := domain.CloneIssue(*issue)
		out.Issue = &i
	}
	return out
}
func (s *core) target(ctx context.Context, n int, boardID domain.NodeID) (domain.IssueDetail, error) {
	if n < 1 {
		return domain.IssueDetail{}, invalid("issue number must be positive")
	}
	session, err := s.issueSession(ctx)
	if err != nil {
		return domain.IssueDetail{}, err
	}
	v, err := s.backend.Issue(ctx, session.Repo.Ref, n, boardID)
	v.Issue = domain.CloneIssue(v.Issue)
	return v, typed(err)
}
func preservePlacement(ack, previous domain.Issue) domain.Issue {
	ack = domain.CloneIssue(ack)
	ack.Board = domain.CloneIssue(previous).Board
	return ack
}
func (s *core) mutationTarget(ctx context.Context, n int) (domain.IssueDetail, error) {
	if _, err := s.issueSession(ctx); err != nil {
		return domain.IssueDetail{}, err
	}
	var id domain.NodeID
	if b, err := s.BoardInfo(ctx); err == nil {
		id = b.Project.NodeID
	}
	return s.target(ctx, n, id)
}
func assignmentResult(ack domain.Issue, expected []string) error {
	if len(ack.Assignees) != len(expected) {
		return &domain.Error{Kind: domain.ErrForbidden, Message: "GitHub did not apply the requested assignees"}
	}
	for _, name := range expected {
		if !hasLogin(ack.Assignees, name) {
			return &domain.Error{Kind: domain.ErrForbidden, Message: "GitHub did not apply the requested assignees"}
		}
	}
	return nil
}
func (s *core) CreateIssue(ctx context.Context, in domain.NewIssue) (domain.Change, error) {
	if err := validateTitle(in.Title); err != nil {
		return domain.Change{}, err
	}
	if err := validateBody(in.Body); err != nil {
		return domain.Change{}, err
	}
	input, board, err := s.validateNames(ctx, in.Assignees, in.Labels, in.Status, in.AddToBoard)
	if err != nil {
		return domain.Change{}, err
	}
	input.Title = in.Title
	input.Body = in.Body
	users, err := s.usersForNames(ctx, in.Assignees)
	if err != nil {
		return domain.Change{}, err
	}
	expected := []string{}
	for _, u := range users {
		expected = append(expected, u.Login)
	}
	i, err := s.backend.CreateIssue(ctx, input)
	if err != nil {
		return domain.Change{}, typed(err)
	}
	i = domain.CloneIssue(i)
	change := domain.Change{Issue: i, Changed: true}
	if err := assignmentResult(i, expected); err != nil {
		return change, partial(err, &i, []string{"created"})
	}
	if board != nil {
		change, err = s.placeIssue(ctx, i, *board, in.Status, false)
		change.Changed = true
		if err != nil {
			var completed = []string{"created"}
			de := typed(err).(*domain.Error)
			cause := err
			if de.Kind == domain.ErrPartial {
				completed = append(completed, de.Completed...)
				cause = de.Err
			}
			return change, partial(cause, &change.Issue, completed)
		}
	}
	return change, nil
}
func (s *core) EditIssue(ctx context.Context, n int, in domain.EditIssue) (domain.Change, error) {
	if in.Title == nil && in.Body == nil {
		return domain.Change{}, invalid("edit requires a title or body")
	}
	if in.Title != nil {
		if err := validateTitle(*in.Title); err != nil {
			return domain.Change{}, err
		}
	}
	if in.Body != nil {
		if err := validateBody(*in.Body); err != nil {
			return domain.Change{}, err
		}
	}
	d, err := s.mutationTarget(ctx, n)
	if err != nil {
		return domain.Change{}, err
	}
	patch := domain.IssuePatch{}
	if in.Title != nil && *in.Title != d.Issue.Title {
		patch.Title = in.Title
	}
	if in.Body != nil && *in.Body != d.Body {
		patch.Body = in.Body
	}
	if patch.Title == nil && patch.Body == nil {
		return domain.Change{Issue: d.Issue}, nil
	}
	ack, err := s.backend.UpdateIssue(ctx, d.Issue.NodeID, patch)
	if err != nil {
		return domain.Change{}, typed(err)
	}
	return domain.Change{Issue: preservePlacement(ack, d.Issue), Changed: true}, nil
}
func (s *core) UpdateAssignees(ctx context.Context, n int, add, remove []string) (domain.Change, error) {
	adds, err := s.usersForNames(ctx, add)
	if err != nil {
		return domain.Change{}, err
	}
	d, err := s.mutationTarget(ctx, n)
	if err != nil {
		return domain.Change{}, err
	}
	removes, err := s.removalUsers(ctx, d.Issue, remove)
	if err != nil {
		return domain.Change{}, err
	}
	for _, a := range adds {
		for _, r := range removes {
			if strings.EqualFold(a.Login, r.Login) {
				return domain.Change{}, invalid("assignee cannot be both added and removed")
			}
		}
	}
	i := d.Issue
	expected := []string{}
	actualRemove := []domain.User{}
	actualAdd := []domain.User{}
	for _, name := range i.Assignees {
		removed := false
		for _, r := range removes {
			if strings.EqualFold(name, r.Login) {
				removed = true
				actualRemove = append(actualRemove, r)
			}
		}
		if !removed {
			expected = append(expected, name)
		}
	}
	for _, a := range adds {
		if !hasLogin(expected, a.Login) {
			expected = append(expected, a.Login)
			actualAdd = append(actualAdd, a)
		}
	}
	if len(expected) > 10 {
		return domain.Change{}, invalid("an issue may have at most 10 assignees")
	}
	changed := false
	completed := []string{}
	if len(actualRemove) > 0 {
		ack, e := s.backend.RemoveAssignees(ctx, i.NodeID, userIDs(actualRemove))
		if e != nil {
			return domain.Change{}, typed(e)
		}
		i = preservePlacement(ack, i)
		for _, u := range actualRemove {
			if hasLogin(i.Assignees, u.Login) {
				return domain.Change{Issue: i, Changed: true}, &domain.Error{Kind: domain.ErrForbidden, Message: "GitHub did not remove the requested assignees"}
			}
		}

		changed = true
		completed = append(completed, "removed")
	}
	if len(actualAdd) > 0 {
		ack, e := s.backend.AddAssignees(ctx, i.NodeID, userIDs(actualAdd))
		if e != nil {
			if changed {
				return domain.Change{Issue: i, Changed: true}, partial(e, &i, completed)
			}
			return domain.Change{}, typed(e)
		}
		i = preservePlacement(ack, i)
		changed = true
	}
	if changed {
		if err := assignmentResult(i, expected); err != nil {
			if len(completed) > 0 && len(actualAdd) > 0 {
				return domain.Change{Issue: i, Changed: true}, partial(err, &i, completed)
			}
			return domain.Change{Issue: i, Changed: true}, err
		}
	}
	return domain.Change{Issue: i, Changed: changed}, nil
}
func (s *core) Reassign(ctx context.Context, n int, names []string) (domain.Change, error) {
	users, err := s.usersForNames(ctx, names)
	if err != nil {
		return domain.Change{}, err
	}
	if len(users) > 10 {
		return domain.Change{}, invalid("an issue may have at most 10 assignees")
	}
	d, err := s.mutationTarget(ctx, n)
	if err != nil {
		return domain.Change{}, err
	}
	expected := []string{}
	for _, u := range users {
		expected = append(expected, u.Login)
	}
	if assignmentResult(d.Issue, expected) == nil {
		return domain.Change{Issue: d.Issue}, nil
	}
	ids := userIDs(users)
	ack, err := s.backend.UpdateIssue(ctx, d.Issue.NodeID, domain.IssuePatch{AssigneeIDs: &ids})
	if err != nil {
		return domain.Change{}, typed(err)
	}
	i := preservePlacement(ack, d.Issue)
	return domain.Change{Issue: i, Changed: true}, assignmentResult(i, expected)
}
func (s *core) UpdateLabels(ctx context.Context, n int, add, remove []string) (domain.Change, error) {
	adds, err := s.labelsForNames(ctx, add)
	if err != nil {
		return domain.Change{}, err
	}
	d, err := s.mutationTarget(ctx, n)
	if err != nil {
		return domain.Change{}, err
	}
	removes, err := s.removalLabels(ctx, d.Issue, remove)
	if err != nil {
		return domain.Change{}, err
	}
	for _, a := range adds {
		for _, r := range removes {
			if a.Name == r.Name {
				return domain.Change{}, invalid("label cannot be both added and removed")
			}
		}
	}
	i := d.Issue
	ar, aa := []domain.Label{}, []domain.Label{}
	for _, l := range removes {
		if hasLabel(i.Labels, l.Name) {
			ar = append(ar, l)
		}
	}
	for _, l := range adds {
		if !hasLabel(i.Labels, l.Name) {
			aa = append(aa, l)
		}
	}
	changed := false
	if len(ar) > 0 {
		ack, e := s.backend.RemoveLabels(ctx, i.NodeID, labelIDs(ar))
		if e != nil {
			return domain.Change{}, typed(e)
		}
		i = preservePlacement(ack, i)
		changed = true
	}
	if len(aa) > 0 {
		ack, e := s.backend.AddLabels(ctx, i.NodeID, labelIDs(aa))
		if e != nil {
			if changed {
				return domain.Change{Issue: i, Changed: true}, partial(e, &i, []string{"removed"})
			}
			return domain.Change{}, typed(e)
		}
		i = preservePlacement(ack, i)
		changed = true
	}
	return domain.Change{Issue: i, Changed: changed}, nil
}
func (s *core) Comment(ctx context.Context, n int, body string) (domain.CommentResult, error) {
	if strings.TrimSpace(body) == "" {
		return domain.CommentResult{}, invalid("comment must not be blank")
	}
	if err := validateBody(body); err != nil {
		return domain.CommentResult{}, err
	}
	d, err := s.mutationTarget(ctx, n)
	if err != nil {
		return domain.CommentResult{}, err
	}
	result, err := s.backend.AddComment(ctx, d.Issue.NodeID, body)
	if err != nil {
		return domain.CommentResult{}, typed(err)
	}
	result.Issue = preservePlacement(result.Issue, d.Issue)
	return result, nil
}
func (s *core) Close(ctx context.Context, n int, opts domain.CloseOptions) (domain.Change, error) {
	if opts.Reason == "" {
		opts.Reason = "completed"
	}
	if opts.Reason != "completed" && opts.Reason != "not_planned" {
		return domain.Change{}, invalid("reason must be completed or not_planned")
	}
	if opts.Comment != nil {
		if strings.TrimSpace(*opts.Comment) == "" {
			return domain.Change{}, invalid("closing comment must not be blank")
		}
		if err := validateBody(*opts.Comment); err != nil {
			return domain.Change{}, err
		}
	}
	d, err := s.mutationTarget(ctx, n)
	if err != nil {
		return domain.Change{}, err
	}
	i := d.Issue
	if i.State == "closed" {
		return domain.Change{Issue: i}, nil
	}
	completed := []string{}
	if opts.Comment != nil {
		ack, e := s.backend.AddComment(ctx, i.NodeID, *opts.Comment)
		if e != nil {
			return domain.Change{}, typed(e)
		}
		i = preservePlacement(ack.Issue, i)
		completed = append(completed, "commented")
	}
	// A workflow may have closed the issue while the comment was being posted.
	if i.State == "closed" {
		return domain.Change{Issue: i, Changed: true}, nil
	}
	ack, err := s.backend.CloseIssue(ctx, i.NodeID, opts.Reason)
	if err != nil {
		if len(completed) > 0 {
			return domain.Change{Issue: i, Changed: true}, partial(err, &i, completed)
		}
		return domain.Change{}, typed(err)
	}
	return domain.Change{Issue: preservePlacement(ack, i), Changed: true}, nil
}
func (s *core) Reopen(ctx context.Context, n int) (domain.Change, error) {
	d, err := s.mutationTarget(ctx, n)
	if err != nil {
		return domain.Change{}, err
	}
	if d.Issue.State == "open" {
		return domain.Change{Issue: d.Issue}, nil
	}
	ack, err := s.backend.ReopenIssue(ctx, d.Issue.NodeID)
	if err != nil {
		return domain.Change{}, typed(err)
	}
	return domain.Change{Issue: preservePlacement(ack, d.Issue), Changed: true}, nil
}
func (s *core) Delete(ctx context.Context, n int) error {
	d, err := s.mutationTarget(ctx, n)
	if err != nil {
		return err
	}
	return typed(s.backend.DeleteIssue(ctx, d.Issue.NodeID))
}
