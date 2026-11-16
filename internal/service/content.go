package service

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/pkieltyka/todos/internal/domain"
)

// Production constructs one service, and quick-add owns another core. A failed
// epoch write disables stored content across all of them until process exit.
var contentDisabled atomic.Bool

const contentTTL = 10 * time.Minute

type storedIssues struct {
	Token domain.ChangeToken
	Epoch string
	Page  domain.Page[domain.Issue]
}

func (s *core) contentSpace(ctx context.Context) string {
	if s.opts.Cache == nil || contentDisabled.Load() {
		return ""
	}
	key, err := s.backend.CredentialKey(ctx)
	if err != nil {
		return ""
	}
	return key + "/content"
}

func (s *core) bumpContent(ctx context.Context) {
	if s.opts.Cache == nil {
		return
	}
	key, err := s.backend.CredentialKey(ctx)
	if err != nil {
		contentDisabled.Store(true)
		return
	}
	space := key + "/content"
	if !s.opts.Cache.Bump(space) {
		contentDisabled.Store(true)
		// Deletion may still work when a full disk prevents replacing the epoch.
		// Best effort for other processes as well as this process's disable flag.
		s.opts.Cache.Drop(space, "")
	}
}

func (s *core) beginChange(ctx context.Context) func() {
	s.bumpContent(ctx)
	return func() { s.bumpContent(context.WithoutCancel(ctx)) }
}

func issuePageKey(repo domain.RepoRef, f domain.IssueFilter, p domain.PageRequest, board domain.NodeID) string {
	repo.Host, repo.Owner, repo.Name = strings.ToLower(repo.Host), strings.ToLower(repo.Owner), strings.ToLower(repo.Name)
	f.Assignee = strings.ToLower(f.Assignee)
	f.Labels = slices.Clone(f.Labels)
	slices.Sort(f.Labels)
	f.Labels = slices.Compact(f.Labels)
	if f.Labels == nil {
		f.Labels = []string{}
	}
	f.Search = ""
	if p.Size == 0 {
		p.Size = 100
	}
	b, _ := json.Marshal(struct {
		Method string
		Repo   domain.RepoRef
		Filter domain.IssueFilter
		Board  domain.NodeID
		Page   domain.PageRequest
	}{"ListIssues", repo, f, board, p})
	return string(b)
}

func quietToken(t domain.ChangeToken, withBoard bool) bool {
	if t.ServerTime.IsZero() || t.Viewer == "" {
		return false
	}
	quiet := func(updated time.Time) bool {
		age := t.ServerTime.Sub(updated)
		return age >= 5*time.Second && age <= 10*365*24*time.Hour
	}
	return quiet(t.NewestUpdated) && (!withBoard || quiet(t.ProjectUpdated))
}

func validIssuePage(p domain.Page[domain.Issue]) bool {
	if p.Items == nil || p.Total < 0 || p.HasMore && p.NextCursor == "" {
		return false
	}
	for _, issue := range p.Items {
		if issue.Number < 1 || issue.NodeID == "" {
			return false
		}
	}
	return true
}

func cloneIssuePage(p domain.Page[domain.Issue]) domain.Page[domain.Issue] {
	p.Items = append([]domain.Issue{}, p.Items...)
	for i := range p.Items {
		p.Items[i] = domain.CloneIssue(p.Items[i])
	}
	return p
}

func (s *core) issuePage(ctx context.Context, repo domain.RepoRef, f domain.IssueFilter, p domain.PageRequest, board domain.NodeID) (domain.Page[domain.Issue], error) {
	if ctx.Err() != nil {
		return domain.Page[domain.Issue]{}, typed(ctx.Err())
	}
	space := ""
	if _, fresh := domain.FreshSince(ctx); !fresh && strings.TrimSpace(f.Search) == "" {
		space = s.contentSpace(ctx)
	}
	key := issuePageKey(repo, f, p, board)
	var token domain.ChangeToken
	epoch := ""
	probed := false
	if space != "" {
		epoch = s.opts.Cache.Epoch(space)
		var err error
		token, err = s.backend.ChangeToken(ctx, repo, board)
		probed = err == nil
		if probed && quietToken(token, board != "") {
			if data, readAt, ok := s.opts.Cache.Get(space, key); ok && !readAt.IsZero() && !readAt.After(s.opts.Now()) && s.opts.Now().Sub(readAt) < contentTTL {
				var stored storedIssues
				if json.Unmarshal(data, &stored) == nil && stored.Token.Equal(token) && stored.Epoch == epoch && validIssuePage(stored.Page) && s.contentSpace(ctx) == space && s.opts.Cache.Epoch(space) == epoch {
					if ctx.Err() != nil {
						return domain.Page[domain.Issue]{}, typed(ctx.Err())
					}
					stored.Page.AsOf = readAt
					return cloneIssuePage(stored.Page), nil
				}
			}
		}
	}
	if ctx.Err() != nil {
		return domain.Page[domain.Issue]{}, typed(ctx.Err())
	}
	readAt := s.opts.Now()
	page, err := s.backend.ListIssues(ctx, repo, f, p, board)
	if err != nil {
		return domain.Page[domain.Issue]{}, typed(err)
	}
	if ctx.Err() != nil {
		return domain.Page[domain.Issue]{}, typed(ctx.Err())
	}
	page.AsOf = time.Time{}
	if probed && quietToken(token, board != "") && validIssuePage(page) && s.contentSpace(ctx) == space {
		if data, err := json.Marshal(storedIssues{Token: token, Epoch: epoch, Page: page}); err == nil {
			s.opts.Cache.Put(space, key, data, readAt)
		}
	}
	return cloneIssuePage(page), nil
}
