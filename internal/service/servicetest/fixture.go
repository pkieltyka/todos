package servicetest

import (
	"fmt"
	"time"

	"github.com/pkieltyka/todos/internal/domain"
)

func fixtureTime() time.Time                        { return time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC) }
func ptr[T any](v T) *T                             { return &v }
func copySlice[T any](v []T) []T                    { return append(make([]T, 0, len(v)), v...) }
func cloneRate(v domain.RateLimit) domain.RateLimit { v.Buckets = copySlice(v.Buckets); return v }
func cloneComments(in []domain.Comment) []domain.Comment {
	out := copySlice(in)
	for i := range out {
		if out[i].Author != nil {
			out[i].Author = ptr(*out[i].Author)
		}
	}
	return out
}
func cloneInfo(v domain.BoardInfo) domain.BoardInfo { v.Options = copySlice(v.Options); return v }
func cloneFixture(v Fixture) Fixture {
	archived := make(map[int]domain.Placement, len(v.Archived))
	for n, p := range v.Archived {
		if p.Status != nil {
			p.Status = ptr(*p.Status)
		}
		archived[n] = p
	}
	v.Archived = archived
	v.Credentials.Scopes = copySlice(v.Credentials.Scopes)
	v.Issues = copySlice(v.Issues)
	for i := range v.Issues {
		v.Issues[i].Issue = domain.CloneIssue(v.Issues[i].Issue)
	}
	v.PRs = copySlice(v.PRs)
	for i := range v.PRs {
		v.PRs[i].PullRequest = domain.ClonePR(v.PRs[i].PullRequest)
		v.PRs[i].Closes = copySlice(v.PRs[i].Closes)
	}
	comments := make(map[int][]domain.Comment, len(v.IssueComments))
	for k, c := range v.IssueComments {
		comments[k] = cloneComments(c)
	}
	v.IssueComments = comments
	comments = make(map[int][]domain.Comment, len(v.PRComments))
	for k, c := range v.PRComments {
		comments[k] = cloneComments(c)
	}
	v.PRComments = comments
	v.Labels = copySlice(v.Labels)
	v.Users = copySlice(v.Users)
	v.Projects = copySlice(v.Projects)
	v.BoardInfo = cloneInfo(v.BoardInfo)
	v.Board = domain.CloneBoard(v.Board)
	v.RateLimit = cloneRate(v.RateLimit)
	return v
}

func cloneArguments(v Arguments) Arguments {
	v.Filter.Labels = copySlice(v.Filter.Labels)
	v.Add = copySlice(v.Add)
	v.Remove = copySlice(v.Remove)
	if v.Status != nil {
		v.Status = ptr(*v.Status)
	}
	v.NewIssue.Assignees = copySlice(v.NewIssue.Assignees)
	v.NewIssue.Labels = copySlice(v.NewIssue.Labels)
	if v.NewIssue.Status != nil {
		v.NewIssue.Status = ptr(*v.NewIssue.Status)
	}
	if v.Edit.Title != nil {
		v.Edit.Title = ptr(*v.Edit.Title)
	}
	if v.Edit.Body != nil {
		v.Edit.Body = ptr(*v.Edit.Body)
	}
	if v.Close.Comment != nil {
		v.Close.Comment = ptr(*v.Close.Comment)
	}
	v.QuickAdd.Assignees = copySlice(v.QuickAdd.Assignees)
	v.QuickAdd.Labels = copySlice(v.QuickAdd.Labels)
	if v.QuickAdd.Status != nil {
		v.QuickAdd.Status = ptr(*v.QuickAdd.Status)
	}
	return v
}

// GenIssues returns n open issues in descending updated order, without randomness.
func GenIssues(n int) []domain.IssueDetail {
	out := make([]domain.IssueDetail, 0, max(0, n))
	for number := n; number > 0; number-- {
		updated := fixtureTime().Add(-time.Duration(n-number+1) * time.Hour)
		assignee := "pkieltyka"
		if number%2 == 0 {
			assignee = "reviewer"
		}
		out = append(out, domain.IssueDetail{Issue: domain.Issue{
			NodeID: domain.NodeID(fmt.Sprintf("ISSUE_%d", number)), Number: number, Title: fmt.Sprintf("Issue %d", number), State: "open", Author: ptr("pkieltyka"),
			Assignees: []string{assignee}, Labels: []domain.Label{}, CreatedAt: updated.Add(-24 * time.Hour), UpdatedAt: updated,
			URL: fmt.Sprintf("https://github.com/pkieltyka/todos/issues/%d", number), ViewerCanUpdate: true, ViewerCanDelete: true,
		}, Body: fmt.Sprintf("Body for issue %d.\n", number)})
	}
	return out
}

// DefaultFixture anchors all timestamps to 2026-11-13T12:00:00Z.
func DefaultFixture() Fixture {
	repo := domain.Repository{Ref: domain.RepoRef{Host: "github.com", Owner: "pkieltyka", Name: "todos"}, NodeID: "REPO_todos", URL: "https://github.com/pkieltyka/todos", IssuesEnabled: true, ViewerCanCreateIssues: true, ViewerPermission: "ADMIN"}
	project := domain.Project{Ref: domain.ProjectRef{Host: "github.com", Owner: "pkieltyka", Number: 1}, NodeID: "PROJECT_1", Title: "todos", URL: "https://github.com/users/pkieltyka/projects/1", Usable: true, Linked: true, ViewerCanUpdate: true, UpdatedAt: fixtureTime()}
	labels := []domain.Label{{NodeID: "LABEL_bug", Name: "bug", Color: "d73a4a", Description: "Something is broken"}, {NodeID: "LABEL_sync", Name: "sync", Color: "0075ca"}, {NodeID: "LABEL_docs", Name: "docs", Color: "008672"}}
	f := Fixture{Session: domain.Session{Repo: repo, Viewer: domain.User{NodeID: "USER_pk", Login: "pkieltyka", Name: "Peter"}}, Credentials: domain.CredentialInfo{Source: "gh", Scopes: []string{"repo", "project"}},
		Issues: GenIssues(6), IssueComments: map[int][]domain.Comment{}, PRs: []domain.PRDetail{}, PRComments: map[int][]domain.Comment{}, Labels: labels,
		Users: []domain.User{{NodeID: "USER_pk", Login: "pkieltyka", Name: "Peter"}, {NodeID: "USER_reviewer", Login: "reviewer", Name: "Reviewer"}, {NodeID: "USER_octocat", Login: "octocat", Name: "Octocat"}}, Projects: []domain.Project{project},
		BoardInfo: domain.BoardInfo{Project: project, StatusFieldID: "STATUS_1", Options: []domain.StatusOption{{ID: "todo", Name: "Todo", Color: "GREEN"}, {ID: "progress", Name: "In progress", Color: "YELLOW"}, {ID: "done", Name: "Done", Color: "PURPLE"}}},
		Board:     domain.Board{Project: project, Columns: []domain.Column{{Status: nil, Cards: []domain.Card{}}, {Status: ptr("Todo"), OptionID: "todo", Cards: []domain.Card{}}, {Status: ptr("In progress"), OptionID: "progress", Cards: []domain.Card{}}, {Status: ptr("Done"), OptionID: "done", Cards: []domain.Card{}}}, HiddenCount: 2},
		RateLimit: domain.RateLimit{Known: true, Limit: 5000, Remaining: 4990, Resource: "graphql", ResetAt: fixtureTime().Add(time.Hour), Buckets: []domain.RateBucket{{Resource: "graphql", Limit: 5000, Remaining: 4990, ResetAt: fixtureTime().Add(time.Hour)}}}, ConfigCheck: domain.DoctorCheck{Name: "Config", OK: true, Detail: "No config file"}}
	for i := range f.Issues {
		issue := &f.Issues[i].Issue
		issue.Labels = []domain.Label{labels[i%len(labels)]}
		if i == 1 {
			issue.Author = nil
		}
		if i == 3 || i == 4 {
			issue.State = "closed"
			reason := domain.StateReason("completed")
			if i == 4 {
				reason = "not_planned"
			}
			issue.StateReason = &reason
			issue.ClosedAt = ptr(issue.UpdatedAt)
		}
		if i < 3 {
			column := i
			issue.Board = &domain.Placement{ItemID: domain.NodeID(fmt.Sprintf("ITEM_%d", issue.Number)), Status: f.Board.Columns[column].Status, OptionID: f.Board.Columns[column].OptionID}
			f.Board.Columns[column].Cards = append(f.Board.Columns[column].Cards, domain.Card{Issue: domain.CloneIssue(*issue)})
		}
	}
	f.Issues[0].Issue.Title = "Fix flaky sync when gh is offline"
	f.Issues[0].Issue.Labels = copySlice(labels[:2])
	f.IssueComments[6] = []domain.Comment{{ID: "COMMENT_1", Author: ptr("reviewer"), CreatedAt: fixtureTime().Add(-time.Hour), Body: "Reproduced. **Thanks!**", URL: repo.URL + "/issues/6#issuecomment-1"}}
	f.Issues[0].Issue.CommentCount = 1
	for i, state := range []domain.PRState{"open", "closed", "merged"} {
		n := 101 + i
		updated := fixtureTime().Add(-time.Duration(i+1) * time.Hour)
		pr := domain.PullRequest{NodeID: domain.NodeID(fmt.Sprintf("PR_%d", n)), Number: n, Title: fmt.Sprintf("Pull request %d", n), State: state, Author: ptr("pkieltyka"), Head: "feature/core", Base: "main", Review: "approved", Checks: "passing", Assignees: []string{}, Labels: []domain.Label{labels[1]}, CreatedAt: updated.Add(-time.Hour), UpdatedAt: updated, URL: fmt.Sprintf("%s/pull/%d", repo.URL, n)}
		if state != "open" {
			pr.ClosedAt = ptr(updated)
		}
		if state == "merged" {
			pr.MergedAt = ptr(updated)
		}
		f.PRs = append(f.PRs, domain.PRDetail{PullRequest: pr, Body: "Read-only pull request.", Mergeable: "mergeable", Closes: []int{6}})
	}
	return cloneFixture(f)
}
