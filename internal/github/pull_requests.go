package github

import (
	"context"
	"strings"

	"github.com/pkieltyka/todos/internal/domain"
)

func (c *Client) ListPRs(ctx context.Context, ref domain.RepoRef, f domain.PRFilter, p domain.PageRequest) (domain.Page[domain.PullRequest], error) {
	v, err := pageVariables(p)
	if err != nil {
		return domain.Page[domain.PullRequest]{}, err
	}
	states, err := stateValues(f.State, true)
	if err != nil {
		return domain.Page[domain.PullRequest]{}, err
	}
	addRepo(v, ref)
	v["states"] = states
	var out struct{ Repository *rawRepository }
	if err = c.query(ctx, "PRList", v, &out); err != nil {
		return domain.Page[domain.PullRequest]{}, err
	}
	if out.Repository == nil {
		return domain.Page[domain.PullRequest]{}, malformed("missing repository")
	}
	if out.Repository.PullRequests.Nodes == nil {
		return domain.Page[domain.PullRequest]{}, malformed("missing pull-request connection")
	}
	for _, r := range out.Repository.PullRequests.Nodes {
		if r.ID == "" || r.Number < 1 {
			return domain.Page[domain.PullRequest]{}, malformed("missing pull request identity")
		}
	}
	return pageResult(out.Repository.PullRequests, pullRequest), nil
}
func (c *Client) PR(ctx context.Context, ref domain.RepoRef, n int) (domain.PRDetail, error) {
	if n < 1 {
		return domain.PRDetail{}, invalidInput("pull request number must be positive")
	}
	result := domain.PRDetail{Closes: []int{}}
	v := repoVariables(ref)
	v["number"] = n
	v["afterCloses"] = nil
	seen := map[string]bool{}
	numbers := map[int]bool{}
	first := true
	var selected domain.NodeID
	for {
		var out struct{ Repository *rawRepository }
		if err := c.query(ctx, "PRView", v, &out); err != nil {
			return domain.PRDetail{}, err
		}
		if out.Repository == nil || out.Repository.PullRequest == nil {
			return domain.PRDetail{}, malformed("missing pull request")
		}
		r := out.Repository.PullRequest
		if r.ClosingIssuesReferences.Nodes == nil {
			return domain.PRDetail{}, malformed("missing closing-reference connection")
		}
		if r.ID == "" || r.Number < 1 || out.Repository.ID == "" {
			return domain.PRDetail{}, malformed("missing pull request or repository identity")
		}
		if first {
			result.PullRequest = pullRequest(*r)
			result.Body = r.Body
			result.Mergeable = domain.Mergeable(strings.ToLower(r.Mergeable))
			selected = out.Repository.ID
			first = false
		}
		for _, closing := range r.ClosingIssuesReferences.Nodes {
			if closing.Repository.ID == selected && !numbers[closing.Number] {
				result.Closes = append(result.Closes, closing.Number)
				numbers[closing.Number] = true
			}
		}
		p := r.ClosingIssuesReferences.PageInfo
		if !p.HasNextPage {
			break
		}
		if p.EndCursor == "" || seen[p.EndCursor] {
			return domain.PRDetail{}, malformed("repeated or empty closing-reference cursor")
		}
		seen[p.EndCursor] = true
		v["afterCloses"] = p.EndCursor
	}
	return result, nil
}
