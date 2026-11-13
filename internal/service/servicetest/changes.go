package servicetest

import (
	"context"
	"fmt"
	"strings"

	"github.com/pkieltyka/todos/internal/domain"
)

func (f *Fake) change(ctx context.Context, method string, args Arguments, fn func(*Fixture, *domain.IssueDetail) bool) (domain.Change, error) {
	return invoke(ctx, f, method, args, func(x *Fixture) (domain.Change, error) {
		i := issueIndex(x, args.Number)
		if i < 0 {
			return domain.Change{}, missing("issue")
		}
		changed := fn(x, &x.Issues[i])
		return domain.Change{Issue: domain.CloneIssue(x.Issues[i].Issue), Changed: changed}, nil
	})
}

func (f *Fake) CreateIssue(ctx context.Context, in domain.NewIssue) (domain.Change, error) {
	in = cloneArguments(Arguments{NewIssue: in}).NewIssue
	return invoke(ctx, f, "CreateIssue", Arguments{NewIssue: in}, func(x *Fixture) (domain.Change, error) {
		var status *string
		var optionID string
		if in.AddToBoard || in.Status != nil {
			var err error
			status, optionID, err = resolveStatus(x, in.Status)
			if err != nil {
				return domain.Change{}, err
			}
		}
		for _, d := range x.Issues {
			f.nextNumber = max(f.nextNumber, d.Issue.Number)
		}
		for _, d := range x.PRs {
			f.nextNumber = max(f.nextNumber, d.PullRequest.Number)
		}
		f.nextNumber++
		n := f.nextNumber
		now := f.tick()
		labels := []domain.Label{}
		for _, name := range in.Labels {
			labels = appendLabel(labels, fixtureLabel(x, name))
		}
		i := domain.Issue{NodeID: domain.NodeID(fmt.Sprintf("ISSUE_%d", n)), Number: n, Title: in.Title, State: "open", Author: ptr(x.Session.Viewer.Login), Assignees: uniqueLogins(x, in.Assignees), Labels: labels, CreatedAt: now, UpdatedAt: now, URL: fmt.Sprintf("%s/issues/%d", x.Session.Repo.URL, n), ViewerCanUpdate: true, ViewerCanDelete: true}
		if in.AddToBoard || in.Status != nil {
			place(x, &i, status, optionID)
		}
		x.Issues = append(x.Issues, domain.IssueDetail{Issue: i, Body: in.Body})
		return domain.Change{Issue: domain.CloneIssue(i), Changed: true}, nil
	})
}

func (f *Fake) EditIssue(ctx context.Context, n int, in domain.EditIssue) (domain.Change, error) {
	in = cloneArguments(Arguments{Edit: in}).Edit
	return f.change(ctx, "EditIssue", Arguments{Number: n, Edit: in}, func(_ *Fixture, d *domain.IssueDetail) bool {
		changed := false
		if in.Title != nil && *in.Title != d.Issue.Title {
			d.Issue.Title = *in.Title
			changed = true
		}
		if in.Body != nil && *in.Body != d.Body {
			d.Body = *in.Body
			changed = true
		}
		if changed {
			d.Issue.UpdatedAt = f.tick()
		}
		return changed
	})
}

func uniqueLogins(x *Fixture, in []string) []string {
	out := []string{}
	for _, name := range in {
		if name == "@me" {
			name = x.Session.Viewer.Login
		}
		for _, u := range x.Users {
			if strings.EqualFold(u.Login, name) {
				name = u.Login
				break
			}
		}
		if !contains(out, name) {
			out = append(out, name)
		}
	}
	return out
}
func sameLogins(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for _, s := range a {
		if !contains(b, s) {
			return false
		}
	}
	return true
}
func (f *Fake) UpdateAssignees(ctx context.Context, n int, add, remove []string) (domain.Change, error) {
	add, remove = copySlice(add), copySlice(remove)
	return f.change(ctx, "UpdateAssignees", Arguments{Number: n, Add: add, Remove: remove}, func(x *Fixture, d *domain.IssueDetail) bool {
		adds, removes := uniqueLogins(x, add), uniqueLogins(x, remove)
		next := []string{}
		for _, s := range d.Issue.Assignees {
			if !contains(removes, s) {
				next = append(next, s)
			}
		}
		for _, s := range adds {
			if !contains(next, s) {
				next = append(next, s)
			}
		}
		if sameLogins(next, d.Issue.Assignees) {
			return false
		}
		d.Issue.Assignees = next
		d.Issue.UpdatedAt = f.tick()
		return true
	})
}
func (f *Fake) Reassign(ctx context.Context, n int, logins []string) (domain.Change, error) {
	logins = copySlice(logins)
	return f.change(ctx, "Reassign", Arguments{Number: n, Add: logins}, func(x *Fixture, d *domain.IssueDetail) bool {
		next := uniqueLogins(x, logins)
		if sameLogins(next, d.Issue.Assignees) {
			return false
		}
		d.Issue.Assignees = next
		d.Issue.UpdatedAt = f.tick()
		return true
	})
}
func fixtureLabel(x *Fixture, name string) domain.Label {
	for _, l := range x.Labels {
		if l.Name == name {
			return l
		}
	}
	return domain.Label{Name: name}
}
func appendLabel(v []domain.Label, label domain.Label) []domain.Label {
	for _, l := range v {
		if l.Name == label.Name {
			return v
		}
	}
	return append(v, label)
}
func sameLabels(a, b []domain.Label) bool {
	if len(a) != len(b) {
		return false
	}
	for _, l := range a {
		found := false
		for _, r := range b {
			if l.Name == r.Name {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}
func (f *Fake) UpdateLabels(ctx context.Context, n int, add, remove []string) (domain.Change, error) {
	add, remove = copySlice(add), copySlice(remove)
	return f.change(ctx, "UpdateLabels", Arguments{Number: n, Add: add, Remove: remove}, func(x *Fixture, d *domain.IssueDetail) bool {
		next := []domain.Label{}
		for _, l := range d.Issue.Labels {
			removed := false
			for _, s := range remove {
				if l.Name == s {
					removed = true
				}
			}
			if !removed {
				next = append(next, l)
			}
		}
		for _, s := range add {
			next = appendLabel(next, fixtureLabel(x, s))
		}
		if sameLabels(next, d.Issue.Labels) {
			return false
		}
		d.Issue.Labels = next
		d.Issue.UpdatedAt = f.tick()
		return true
	})
}

func (f *Fake) addComment(x *Fixture, d *domain.IssueDetail, body string) domain.Comment {
	f.nextComment++
	now := f.tick()
	c := domain.Comment{ID: fmt.Sprintf("COMMENT_%d", f.nextComment), Author: ptr(x.Session.Viewer.Login), CreatedAt: now, Body: body, URL: fmt.Sprintf("%s#issuecomment-%d", d.Issue.URL, f.nextComment)}
	if x.IssueComments == nil {
		x.IssueComments = map[int][]domain.Comment{}
	}
	x.IssueComments[d.Issue.Number] = append(x.IssueComments[d.Issue.Number], c)
	d.Issue.CommentCount = len(x.IssueComments[d.Issue.Number])
	d.Issue.UpdatedAt = now
	return c
}
func (f *Fake) Comment(ctx context.Context, n int, body string) (domain.CommentResult, error) {
	return invoke(ctx, f, "Comment", Arguments{Number: n, Text: body}, func(x *Fixture) (domain.CommentResult, error) {
		i := issueIndex(x, n)
		if i < 0 {
			return domain.CommentResult{}, missing("issue")
		}
		c := f.addComment(x, &x.Issues[i], body)
		return domain.CommentResult{Issue: domain.CloneIssue(x.Issues[i].Issue), Comment: cloneComments([]domain.Comment{c})[0]}, nil
	})
}
func (f *Fake) Close(ctx context.Context, n int, options domain.CloseOptions) (domain.Change, error) {
	options = cloneArguments(Arguments{Close: options}).Close
	return f.change(ctx, "Close", Arguments{Number: n, Close: options}, func(x *Fixture, d *domain.IssueDetail) bool {
		if d.Issue.State == "closed" {
			return false
		}
		if options.Comment != nil {
			f.addComment(x, d, *options.Comment)
		}
		reason := domain.StateReason(options.Reason)
		if reason == "" {
			reason = "completed"
		}
		d.Issue.State = "closed"
		d.Issue.StateReason = &reason
		now := f.tick()
		d.Issue.ClosedAt = &now
		d.Issue.UpdatedAt = now
		return true
	})
}
func (f *Fake) Reopen(ctx context.Context, n int) (domain.Change, error) {
	return f.change(ctx, "Reopen", Arguments{Number: n}, func(_ *Fixture, d *domain.IssueDetail) bool {
		if d.Issue.State == "open" {
			return false
		}
		d.Issue.State = "open"
		d.Issue.StateReason = ptr(domain.StateReason("reopened"))
		d.Issue.ClosedAt = nil
		d.Issue.UpdatedAt = f.tick()
		return true
	})
}
func (f *Fake) Delete(ctx context.Context, n int) error {
	_, err := invoke(ctx, f, "Delete", Arguments{Number: n}, func(x *Fixture) (struct{}, error) {
		i := issueIndex(x, n)
		if i < 0 {
			return struct{}{}, missing("issue")
		}
		x.Issues = append(x.Issues[:i], x.Issues[i+1:]...)
		delete(x.IssueComments, n)
		delete(x.Archived, n)
		for c := range x.Board.Columns {
			cards := []domain.Card{}
			for _, card := range x.Board.Columns[c].Cards {
				if card.Number != n {
					cards = append(cards, card)
				}
			}
			x.Board.Columns[c].Cards = cards
		}
		return struct{}{}, nil
	})
	return err
}
