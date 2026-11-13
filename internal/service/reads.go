package service

import (
	"context"

	"github.com/pkieltyka/todos/internal/domain"
)

func validatePage(p domain.PageRequest) error {
	if p.Size < 0 || p.Size > 100 {
		return invalid("page size must be 1..100")
	}
	return nil
}
func validateState(state string, pr bool) error {
	if state == "" || state == "open" || state == "closed" || state == "all" || (pr && state == "merged") {
		return nil
	}
	return invalid("invalid state filter")
}
func (s *core) ListIssues(ctx context.Context, f domain.IssueFilter, p domain.PageRequest) (domain.Page[domain.Issue], error) {
	if err := validatePage(p); err != nil {
		return domain.Page[domain.Issue]{}, err
	}
	if err := validateState(f.State, false); err != nil {
		return domain.Page[domain.Issue]{}, err
	}
	session, err := s.issueSession(ctx)
	if err != nil {
		return domain.Page[domain.Issue]{}, err
	}
	if f.State == "" {
		f.State = "open"
	}
	f.Labels = append([]string{}, f.Labels...)
	if f.Assignee == "@me" {
		f.Assignee = session.Viewer.Login
	}
	var boardID domain.NodeID
	if b, err := s.BoardInfo(ctx); err == nil {
		boardID = b.Project.NodeID
	}
	page, err := s.backend.ListIssues(ctx, session.Repo.Ref, f, p, boardID)
	if err != nil {
		return domain.Page[domain.Issue]{}, typed(err)
	}
	page.Items = append([]domain.Issue{}, page.Items...)
	for i := range page.Items {
		page.Items[i] = domain.CloneIssue(page.Items[i])
	}
	return page, nil
}
func (s *core) GetIssue(ctx context.Context, n int) (domain.IssueDetail, error) {
	if n < 1 {
		return domain.IssueDetail{}, invalid("issue number must be positive")
	}
	session, err := s.issueSession(ctx)
	if err != nil {
		return domain.IssueDetail{}, err
	}
	var id domain.NodeID
	if b, err := s.BoardInfo(ctx); err == nil {
		id = b.Project.NodeID
	}
	v, err := s.backend.Issue(ctx, session.Repo.Ref, n, id)
	v.Issue = domain.CloneIssue(v.Issue)
	return v, typed(err)
}
func (s *core) ListIssueComments(ctx context.Context, n int, p domain.PageRequest) (domain.Page[domain.Comment], error) {
	session, err := s.issueSession(ctx)
	if err != nil {
		return domain.Page[domain.Comment]{}, err
	}
	return s.comments(ctx, session.Repo.Ref, n, p, false)
}
func (s *core) ListPRComments(ctx context.Context, n int, p domain.PageRequest) (domain.Page[domain.Comment], error) {
	session, err := s.Session(ctx)
	if err != nil {
		return domain.Page[domain.Comment]{}, err
	}
	return s.comments(ctx, session.Repo.Ref, n, p, true)
}
func (s *core) comments(ctx context.Context, repo domain.RepoRef, n int, p domain.PageRequest, pr bool) (domain.Page[domain.Comment], error) {
	if n < 1 {
		return domain.Page[domain.Comment]{}, invalid("number must be positive")
	}
	if err := validatePage(p); err != nil {
		return domain.Page[domain.Comment]{}, err
	}
	var v domain.Page[domain.Comment]
	var err error
	if pr {
		v, err = s.backend.PRComments(ctx, repo, n, p)
	} else {
		v, err = s.backend.IssueComments(ctx, repo, n, p)
	}
	v.Items = append([]domain.Comment{}, v.Items...)
	for i := range v.Items {
		if a := v.Items[i].Author; a != nil {
			copy := *a
			v.Items[i].Author = &copy
		}
	}
	return v, typed(err)
}
func (s *core) ListPRs(ctx context.Context, f domain.PRFilter, p domain.PageRequest) (domain.Page[domain.PullRequest], error) {
	if err := validatePage(p); err != nil {
		return domain.Page[domain.PullRequest]{}, err
	}
	if err := validateState(f.State, true); err != nil {
		return domain.Page[domain.PullRequest]{}, err
	}
	session, err := s.Session(ctx)
	if err != nil {
		return domain.Page[domain.PullRequest]{}, err
	}
	if f.State == "" {
		f.State = "open"
	}
	page, err := s.backend.ListPRs(ctx, session.Repo.Ref, f, p)
	page.Items = append([]domain.PullRequest{}, page.Items...)
	for i := range page.Items {
		page.Items[i] = domain.ClonePR(page.Items[i])
	}
	return page, typed(err)
}
func (s *core) GetPR(ctx context.Context, n int) (domain.PRDetail, error) {
	if n < 1 {
		return domain.PRDetail{}, invalid("pull request number must be positive")
	}
	session, err := s.Session(ctx)
	if err != nil {
		return domain.PRDetail{}, err
	}
	v, err := s.backend.PR(ctx, session.Repo.Ref, n)
	v.PullRequest = domain.ClonePR(v.PullRequest)
	v.Closes = append([]int{}, v.Closes...)
	return v, typed(err)
}
