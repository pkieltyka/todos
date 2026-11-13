package github

import (
	"strings"
	"time"

	"github.com/pkieltyka/todos/internal/domain"
)

type pageInfo struct {
	HasNextPage bool
	EndCursor   string
}
type connection[T any] struct {
	Nodes      []T
	TotalCount int
	PageInfo   pageInfo
}
type rawUser struct {
	ID          domain.NodeID
	Login, Name string
}
type rawLabel struct {
	ID                       domain.NodeID
	Name, Description, Color string
}
type rawRepository struct {
	ID                                      domain.NodeID
	NameWithOwner, URL, ViewerPermission    string
	HasIssuesEnabled, ViewerCanCreateIssues bool
	Issues                                  connection[rawIssue]
	Issue                                   *rawIssue
	PullRequests                            connection[rawPR]
	PullRequest                             *rawPR
	Labels                                  connection[rawLabel]
	AssignableUsers                         connection[rawUser]
	ProjectsV2                              connection[rawProject]
}
type rawPlacement struct {
	ID               domain.NodeID
	IsArchived       bool
	Project          struct{ ID domain.NodeID }
	FieldValueByName *struct {
		Typename       string `json:"__typename"`
		Name, OptionID string
	}
	Content *rawIssue
}
type rawIssue struct {
	Typename                             string `json:"__typename"`
	ID                                   domain.NodeID
	Number                               int
	Title, State, StateReason, Body, URL string
	Author                               *rawUser
	Assignees                            connection[rawUser]
	Labels                               connection[rawLabel]
	Milestone                            *struct{ Title string }
	Comments                             connection[rawComment]
	CreatedAt, UpdatedAt                 time.Time
	ClosedAt                             *time.Time
	ViewerCanUpdate, ViewerCanDelete     bool
	ProjectItems                         connection[rawPlacement]
	Repository                           *rawRepository
}
type rawComment struct {
	ID        string
	Author    *rawUser
	CreatedAt time.Time
	Body, URL string
}
type rawPR struct {
	rawIssue
	IsDraft                  bool
	HeadRefName, BaseRefName string
	ReviewDecision           *string
	MergedAt                 *time.Time
	Mergeable                string
	Commits                  connection[struct {
		Commit struct {
			StatusCheckRollup *struct {
				State    string
				Contexts struct{ TotalCount int }
			}
		}
	}]
	ClosingIssuesReferences connection[struct {
		Number     int
		Repository struct{ ID domain.NodeID }
	}]
}
type rawProject struct {
	ID         domain.NodeID
	Number     int
	Title, URL string
	Owner      struct {
		Login    string
		Typename string `json:"__typename"`
	}
	UpdatedAt               time.Time
	Closed, ViewerCanUpdate bool
	Items                   connection[rawPlacement]
	Fields                  connection[struct {
		ID       domain.NodeID
		Name     string
		Typename string `json:"__typename"`
		Options  []domain.StatusOption
	}]
}

func login(u *rawUser) *string {
	if u == nil {
		return nil
	}
	v := u.Login
	return &v
}
func user(u rawUser) domain.User { return domain.User{NodeID: u.ID, Login: u.Login, Name: u.Name} }
func label(l rawLabel) domain.Label {
	return domain.Label{NodeID: l.ID, Name: l.Name, Description: l.Description, Color: strings.TrimPrefix(l.Color, "#")}
}
func utc(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	v := t.UTC()
	return &v
}
func placement(p rawPlacement) domain.Placement {
	out := domain.Placement{ItemID: p.ID}
	if v := p.FieldValueByName; v != nil && v.Typename == "ProjectV2ItemFieldSingleSelectValue" {
		name := v.Name
		out.Status = &name
		out.OptionID = v.OptionID
	}
	return out
}
func issue(v rawIssue, selected domain.NodeID) domain.Issue {
	out := domain.Issue{NodeID: v.ID, Number: v.Number, Title: v.Title, State: domain.IssueState(strings.ToLower(v.State)), Author: login(v.Author), Assignees: []string{}, Labels: []domain.Label{}, CommentCount: v.Comments.TotalCount, CreatedAt: v.CreatedAt.UTC(), UpdatedAt: v.UpdatedAt.UTC(), ClosedAt: utc(v.ClosedAt), URL: v.URL, ViewerCanUpdate: v.ViewerCanUpdate, ViewerCanDelete: v.ViewerCanDelete}
	switch v.StateReason {
	case "COMPLETED", "NOT_PLANNED", "REOPENED":
		r := domain.StateReason(strings.ToLower(v.StateReason))
		out.StateReason = &r
	}
	for _, a := range v.Assignees.Nodes {
		out.Assignees = append(out.Assignees, a.Login)
	}
	for _, l := range v.Labels.Nodes {
		out.Labels = append(out.Labels, label(l))
	}
	if v.Milestone != nil {
		name := v.Milestone.Title
		out.Milestone = &name
	}
	if selected != "" {
		for _, p := range v.ProjectItems.Nodes {
			if p.Project.ID == selected && !p.IsArchived {
				mapped := placement(p)
				out.Board = &mapped
				break
			}
		}
	}
	return out
}
func pullRequest(v rawPR) domain.PullRequest {
	base := issue(v.rawIssue, "")
	out := domain.PullRequest{NodeID: base.NodeID, Number: base.Number, Title: base.Title, State: domain.PRState(strings.ToLower(v.State)), Draft: v.IsDraft, Author: base.Author, Head: v.HeadRefName, Base: v.BaseRefName, Review: "none", Checks: "none", Assignees: base.Assignees, Labels: base.Labels, CreatedAt: base.CreatedAt, UpdatedAt: base.UpdatedAt, ClosedAt: base.ClosedAt, MergedAt: utc(v.MergedAt), URL: base.URL, CommentCount: base.CommentCount}
	if v.ReviewDecision != nil {
		switch *v.ReviewDecision {
		case "APPROVED", "CHANGES_REQUESTED", "REVIEW_REQUIRED":
			out.Review = domain.ReviewStatus(strings.ToLower(*v.ReviewDecision))
		}
	}
	if n := v.Commits.Nodes; len(n) > 0 {
		r := n[len(n)-1].Commit.StatusCheckRollup
		if r != nil && r.Contexts.TotalCount > 0 {
			switch r.State {
			case "SUCCESS":
				out.Checks = "passing"
			case "FAILURE", "ERROR":
				out.Checks = "failing"
			case "EXPECTED", "PENDING":
				out.Checks = "pending"
			}
		}
	}
	return out
}
func comment(v rawComment) domain.Comment {
	return domain.Comment{ID: v.ID, Author: login(v.Author), CreatedAt: v.CreatedAt.UTC(), Body: v.Body, URL: v.URL}
}
func project(v rawProject, host string) domain.Project {
	return domain.Project{Ref: domain.ProjectRef{Host: host, Owner: v.Owner.Login, Number: v.Number}, NodeID: v.ID, Title: v.Title, URL: v.URL, ViewerCanUpdate: v.ViewerCanUpdate, TotalItems: v.Items.TotalCount, UpdatedAt: v.UpdatedAt.UTC()}
}
func repoRef(host, name string) domain.RepoRef {
	owner, repo, _ := strings.Cut(name, "/")
	return domain.RepoRef{Host: host, Owner: owner, Name: repo}
}
func pageResult[A, B any](c connection[A], convert func(A) B) domain.Page[B] {
	out := domain.Page[B]{Items: make([]B, 0, len(c.Nodes)), Total: c.TotalCount, HasMore: c.PageInfo.HasNextPage, NextCursor: c.PageInfo.EndCursor}
	for _, v := range c.Nodes {
		out.Items = append(out.Items, convert(v))
	}
	return out
}
func pageVariables(p domain.PageRequest) (map[string]any, error) {
	if p.Size == 0 {
		p.Size = 100
	}
	if p.Size < 1 || p.Size > 100 {
		return nil, invalidInput("page size must be between 1 and 100")
	}
	var after any
	if p.Cursor != "" {
		after = p.Cursor
	}
	return map[string]any{"first": p.Size, "after": after}, nil
}
func repoVariables(r domain.RepoRef) map[string]any {
	return map[string]any{"owner": r.Owner, "repo": r.Name}
}
func addRepo(v map[string]any, r domain.RepoRef) { v["owner"] = r.Owner; v["repo"] = r.Name }
func stateValues(state string, pr bool) (any, error) {
	if state == "" {
		state = "open"
	}
	if state == "all" {
		return nil, nil
	}
	if state != "open" && state != "closed" && !(pr && state == "merged") {
		return nil, invalidInput("invalid state")
	}
	return []string{strings.ToUpper(state)}, nil
}
