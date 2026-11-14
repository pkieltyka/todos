package cli

import (
	"context"

	"github.com/pkieltyka/todos/internal/domain"
	"github.com/pkieltyka/todos/internal/service"
)

func collectRows[T any](ctx context.Context, opts ListOptions, fetch func(context.Context, domain.PageRequest) (domain.Page[T], error), id func(T) string, search bool) ([]T, int, bool, error) {
	rows := []T{}
	seenIDs := map[string]bool{}
	seenCursors := map[string]bool{}
	p := domain.PageRequest{}
	total := 0
	truncated := false
	scanned := 0
	for {
		if ctx.Err() != nil {
			return nil, 0, false, domainError(ctx.Err())
		}
		p.Size = 100
		if !opts.All {
			p.Size = min(100, opts.Limit-len(rows))
			if p.Size <= 0 {
				break
			}
		}
		if search {
			p.Size = min(p.Size, 1000-scanned)
			if p.Size <= 0 {
				truncated = true
				break
			}
		}
		page, err := fetch(ctx, p)
		if err != nil {
			return nil, 0, false, domainError(err)
		}
		total = page.Total
		scanned += len(page.Items)
		if opts.All && search && (page.Total > 1000 || page.ProviderTruncated) {
			return nil, 0, false, &domain.Error{Kind: domain.ErrInvalidInput, Message: "search --all cannot return more than 1000 matches", Hint: "narrow the search query"}
		}
		for _, row := range page.Items {
			key := id(row)
			if !seenIDs[key] {
				seenIDs[key] = true
				rows = append(rows, row)
			}
			if !opts.All && len(rows) >= opts.Limit {
				break
			}
		}
		truncated = page.HasMore || page.ProviderTruncated || len(rows) < total
		if !page.HasMore || !opts.All && len(rows) >= opts.Limit || search && scanned >= 1000 {
			break
		}
		if page.NextCursor == "" || seenCursors[page.NextCursor] {
			return nil, 0, false, &domain.Error{Kind: domain.ErrGitHub, Message: "GitHub returned an empty or repeated pagination cursor"}
		}
		seenCursors[page.NextCursor] = true
		p.Cursor = page.NextCursor
	}
	return rows, total, truncated, nil
}
func collectIssues(ctx context.Context, s service.Service, f domain.IssueFilter, opts ListOptions) (IssuesDTO, error) {
	rows, total, truncated, err := collectRows(ctx, opts, func(ctx context.Context, p domain.PageRequest) (domain.Page[domain.Issue], error) {
		return s.ListIssues(ctx, f, p)
	}, func(v domain.Issue) string { return string(v.NodeID) }, f.Search != "")
	if err != nil {
		return IssuesDTO{}, err
	}
	out := IssuesDTO{Issues: []IssueDTO{}, Total: total, Truncated: truncated}
	for _, row := range rows {
		out.Issues = append(out.Issues, issueDTO(row))
	}
	return out, nil
}
func collectPRs(ctx context.Context, s service.Service, f domain.PRFilter, opts ListOptions) (PRsDTO, error) {
	rows, total, truncated, err := collectRows(ctx, opts, func(ctx context.Context, p domain.PageRequest) (domain.Page[domain.PullRequest], error) {
		return s.ListPRs(ctx, f, p)
	}, func(v domain.PullRequest) string { return string(v.NodeID) }, false)
	if err != nil {
		return PRsDTO{}, err
	}
	out := PRsDTO{PullRequests: []PRDTO{}, Total: total, Truncated: truncated}
	for _, row := range rows {
		out.PullRequests = append(out.PullRequests, prDTO(row))
	}
	return out, nil
}
func collectComments(ctx context.Context, fetch func(context.Context, domain.PageRequest) (domain.Page[domain.Comment], error)) ([]CommentDTO, bool, error) {
	rows, _, truncated, err := collectRows(ctx, ListOptions{Limit: 500}, fetch, func(v domain.Comment) string { return v.ID }, false)
	if err != nil {
		return nil, false, err
	}
	out := make([]CommentDTO, 0, len(rows))
	for _, row := range rows {
		out = append(out, commentDTO(row))
	}
	return out, truncated, nil
}
