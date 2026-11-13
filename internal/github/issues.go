package github

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"unicode"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/pkieltyka/todos/internal/domain"
)

func (c *Client) Viewer(ctx context.Context) (domain.User, error) {
	var out struct{ Viewer *rawUser }
	if err := c.query(ctx, "Viewer", nil, &out); err != nil {
		return domain.User{}, err
	}
	if out.Viewer == nil || out.Viewer.Login == "" {
		return domain.User{}, malformed("missing viewer")
	}
	return user(*out.Viewer), nil
}
func (c *Client) Repository(ctx context.Context, ref domain.RepoRef) (domain.Repository, error) {
	var out struct{ Repository *rawRepository }
	if err := c.query(ctx, "Session", repoVariables(ref), &out); err != nil {
		return domain.Repository{}, err
	}
	r := out.Repository
	if r == nil {
		return domain.Repository{}, malformed("missing repository")
	}
	if r.ID == "" || !strings.Contains(r.NameWithOwner, "/") {
		return domain.Repository{}, malformed("missing repository identity")
	}
	return domain.Repository{Ref: repoRef(ref.Host, r.NameWithOwner), NodeID: r.ID, URL: r.URL, IssuesEnabled: r.HasIssuesEnabled, ViewerCanCreateIssues: r.ViewerCanCreateIssues, ViewerPermission: r.ViewerPermission}, nil
}
func (c *Client) ListLabels(ctx context.Context, ref domain.RepoRef, p domain.PageRequest) (domain.Page[domain.Label], error) {
	var out struct{ Repository *rawRepository }
	v, err := pageVariables(p)
	if err != nil {
		return domain.Page[domain.Label]{}, err
	}
	addRepo(v, ref)
	if err = c.query(ctx, "Labels", v, &out); err != nil {
		return domain.Page[domain.Label]{}, err
	}
	if out.Repository == nil {
		return domain.Page[domain.Label]{}, malformed("missing repository")
	}
	if out.Repository.Labels.Nodes == nil {
		return domain.Page[domain.Label]{}, malformed("missing labels connection")
	}
	return pageResult(out.Repository.Labels, label), nil
}
func (c *Client) ListAssignableUsers(ctx context.Context, ref domain.RepoRef, p domain.PageRequest) (domain.Page[domain.User], error) {
	var out struct{ Repository *rawRepository }
	v, err := pageVariables(p)
	if err != nil {
		return domain.Page[domain.User]{}, err
	}
	addRepo(v, ref)
	if err = c.query(ctx, "AssignableUsers", v, &out); err != nil {
		return domain.Page[domain.User]{}, err
	}
	if out.Repository == nil {
		return domain.Page[domain.User]{}, malformed("missing repository")
	}
	if out.Repository.AssignableUsers.Nodes == nil {
		return domain.Page[domain.User]{}, malformed("missing assignable-users connection")
	}
	return pageResult(out.Repository.AssignableUsers, user), nil
}

// Only errors confined to optional projectItems are recoverable; authentication,
// rate limits and issue-data errors retain their normal failure semantics.
func optionalMembershipError(err error) bool {
	var de *domain.Error
	if !errors.As(err, &de) || de.Kind == domain.ErrAuth || de.Kind == domain.ErrRateLimited || de.Kind == domain.ErrNetwork || de.Kind == domain.ErrTimeout {
		return false
	}
	var ge *api.GraphQLError
	if !errors.As(err, &ge) || len(ge.Errors) == 0 {
		return false
	}
	for _, e := range ge.Errors {
		found := false
		for _, p := range e.Path {
			if p == "projectItems" {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}
func (c *Client) ListIssues(ctx context.Context, ref domain.RepoRef, filter domain.IssueFilter, p domain.PageRequest, selected domain.NodeID) (domain.Page[domain.Issue], error) {
	v, err := pageVariables(p)
	if err != nil {
		return domain.Page[domain.Issue]{}, err
	}
	states, err := stateValues(filter.State, false)
	if err != nil {
		return domain.Page[domain.Issue]{}, err
	}
	v["withBoard"] = selected != ""
	var result domain.Page[domain.Issue]
	if strings.TrimSpace(filter.Search) != "" {
		q, e := buildIssueSearch(ref, filter)
		if e != nil {
			return result, e
		}
		v["query"] = q
		var out struct {
			Search *struct {
				IssueCount int
				Nodes      []rawIssue
				PageInfo   pageInfo
			}
		}
		err = c.query(ctx, "SearchIssues", v, &out)
		if err != nil && selected != "" && optionalMembershipError(err) {
			if out.Search == nil || out.Search.Nodes == nil {
				return c.ListIssues(ctx, ref, filter, p, "")
			}
			selected = ""
			err = nil
		}
		if err != nil {
			return result, err
		}
		if out.Search == nil || out.Search.Nodes == nil {
			return result, malformed("missing search connection")
		}
		result = domain.Page[domain.Issue]{Items: []domain.Issue{}, Total: out.Search.IssueCount, NextCursor: out.Search.PageInfo.EndCursor, HasMore: out.Search.PageInfo.HasNextPage, ProviderTruncated: out.Search.IssueCount > 1000}
		for _, r := range out.Search.Nodes {
			if r.Typename != "Issue" || r.Repository == nil || !strings.EqualFold(r.Repository.NameWithOwner, ref.Owner+"/"+ref.Name) {
				return domain.Page[domain.Issue]{}, invalidInput("search returned a result outside the selected repository or issue type")
			}
			if r.ID == "" || r.Number < 1 {
				return domain.Page[domain.Issue]{}, malformed("missing issue identity")
			}
			result.Items = append(result.Items, issue(r, selected))
		}
		return result, nil
	}
	addRepo(v, ref)
	v["states"] = states
	f := map[string]any{}
	if filter.Assignee != "" {
		f["assignee"] = filter.Assignee
	}
	if len(filter.Labels) > 0 {
		f["labels"] = filter.Labels
	}
	v["filter"] = f
	var out struct{ Repository *rawRepository }
	err = c.query(ctx, "IssueList", v, &out)
	if err != nil && selected != "" && optionalMembershipError(err) {
		if out.Repository == nil || out.Repository.Issues.Nodes == nil {
			return c.ListIssues(ctx, ref, filter, p, "")
		}
		selected = ""
		err = nil
	}
	if err != nil {
		return result, err
	}
	if out.Repository == nil {
		return result, malformed("missing repository")
	}
	if out.Repository.Issues.Nodes == nil {
		return result, malformed("missing issue connection")
	}
	for _, r := range out.Repository.Issues.Nodes {
		if r.ID == "" || r.Number < 1 {
			return result, malformed("missing issue identity")
		}
	}
	return pageResult(out.Repository.Issues, func(v rawIssue) domain.Issue { return issue(v, selected) }), nil
}
func (c *Client) Issue(ctx context.Context, ref domain.RepoRef, n int, selected domain.NodeID) (domain.IssueDetail, error) {
	if n < 1 {
		return domain.IssueDetail{}, invalidInput("issue number must be positive")
	}
	v := repoVariables(ref)
	v["number"] = n
	v["withBoard"] = selected != ""
	var out struct{ Repository *rawRepository }
	err := c.query(ctx, "IssueView", v, &out)
	if err != nil && selected != "" && optionalMembershipError(err) {
		if out.Repository == nil || out.Repository.Issue == nil {
			return c.Issue(ctx, ref, n, "")
		}
		selected = ""
		err = nil
	}
	if err != nil {
		return domain.IssueDetail{}, err
	}
	if out.Repository == nil || out.Repository.Issue == nil {
		return domain.IssueDetail{}, malformed("missing issue")
	}
	r := out.Repository.Issue
	if r.ID == "" || r.Number < 1 {
		return domain.IssueDetail{}, malformed("missing issue identity")
	}
	return domain.IssueDetail{Issue: issue(*r, selected), Body: r.Body}, nil
}
func (c *Client) comments(ctx context.Context, ref domain.RepoRef, n int, p domain.PageRequest, pr bool) (domain.Page[domain.Comment], error) {
	v, err := pageVariables(p)
	if err != nil {
		return domain.Page[domain.Comment]{}, err
	}
	if n < 1 {
		return domain.Page[domain.Comment]{}, invalidInput("number must be positive")
	}
	addRepo(v, ref)
	v["number"] = n
	name := "IssueComments"
	if pr {
		name = "PRComments"
	}
	var out struct{ Repository *rawRepository }
	if err = c.query(ctx, name, v, &out); err != nil {
		return domain.Page[domain.Comment]{}, err
	}
	if out.Repository == nil {
		return domain.Page[domain.Comment]{}, malformed("missing repository")
	}
	if pr {
		if out.Repository.PullRequest == nil {
			return domain.Page[domain.Comment]{}, malformed("missing pull request")
		}
		if out.Repository.PullRequest.Comments.Nodes == nil {
			return domain.Page[domain.Comment]{}, malformed("missing comments connection")
		}
		return pageResult(out.Repository.PullRequest.Comments, comment), nil
	}
	if out.Repository.Issue == nil {
		return domain.Page[domain.Comment]{}, malformed("missing issue")
	}
	if out.Repository.Issue.Comments.Nodes == nil {
		return domain.Page[domain.Comment]{}, malformed("missing comments connection")
	}
	return pageResult(out.Repository.Issue.Comments, comment), nil
}
func (c *Client) IssueComments(ctx context.Context, r domain.RepoRef, n int, p domain.PageRequest) (domain.Page[domain.Comment], error) {
	return c.comments(ctx, r, n, p, false)
}
func (c *Client) PRComments(ctx context.Context, r domain.RepoRef, n int, p domain.PageRequest) (domain.Page[domain.Comment], error) {
	return c.comments(ctx, r, n, p, true)
}
func buildIssueSearch(ref domain.RepoRef, f domain.IssueFilter) (string, error) {
	text := strings.TrimSpace(f.Search)
	depth := 0
	quoted, escape := false, false
	for _, r := range text {
		if unicode.IsControl(r) {
			return "", invalidInput("search contains control characters")
		}
		if escape {
			escape = false
			continue
		}
		if r == '\\' && quoted {
			escape = true
			continue
		}
		if r == '"' {
			quoted = !quoted
			continue
		}
		if !quoted {
			if r == '(' {
				depth++
			}
			if r == ')' {
				depth--
				if depth < 0 {
					return "", invalidInput("unbalanced search parentheses")
				}
			}
		}
	}
	if quoted || depth != 0 {
		return "", invalidInput("unbalanced search expression")
	}
	// Tokenise outside quotes/parentheses to reject scope and type overrides,
	// including negative qualifiers. Fixed scope is also checked on results.
	tokens := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return unicode.IsSpace(r) || r == '(' || r == ')' })
	for _, token := range tokens {
		token = strings.TrimPrefix(token, "-")
		key, value, ok := strings.Cut(token, ":")
		if ok {
			if key == "is" && (value == "pr" || value == "pull-request") {
				return "", invalidInput("search must return issues, not pull requests")
			}
			switch key {
			case "repo", "org", "user", "type", "sort":
				return "", invalidInput("search must not override repository, issue type or sort scope")
			}
		}
	}
	parts := []string{"repo:" + strconv.Quote(ref.Owner+"/"+ref.Name), "is:issue"}
	if f.State != "" && f.State != "all" {
		if _, err := stateValues(f.State, false); err != nil {
			return "", err
		}
		parts = append(parts, "state:"+f.State)
	} else if f.State == "" {
		parts = append(parts, "state:open")
	}
	if f.Assignee != "" {
		parts = append(parts, "assignee:"+strconv.Quote(f.Assignee))
	}
	for _, name := range f.Labels {
		parts = append(parts, "label:"+strconv.Quote(name))
	}
	if text != "" {
		parts = append(parts, "("+text+")")
	}
	parts = append(parts, "sort:updated-desc")
	return strings.Join(parts, " "), nil
}
