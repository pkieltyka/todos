package service

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/pkieltyka/todos/internal/domain"
)

type Options struct {
	ResolveRepository func(context.Context) (domain.RepoRef, error)
	Project           string
	Now               func() time.Time
	Wait              func(context.Context, time.Duration) error
}
type metadata[T any] struct {
	Value     T
	ExpiresAt time.Time
	Valid     bool
}
type core struct {
	backend    Backend
	settings   SettingsStore
	opts       Options
	mu         sync.Mutex
	session    metadata[domain.Session]
	labels     metadata[[]domain.Label]
	users      metadata[[]domain.User]
	board      metadata[domain.BoardInfo]
	selection  *domain.ProjectRef
	generation uint64
}

func New(b Backend, settings SettingsStore, opts Options) Service {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Wait == nil {
		opts.Wait = func(ctx context.Context, d time.Duration) error {
			timer := time.NewTimer(d)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
				return nil
			}
		}
	}
	return &core{backend: b, settings: settings, opts: opts}
}
func typed(err error) error {
	if err == nil {
		return nil
	}
	var de *domain.Error
	if errors.As(err, &de) {
		return domain.CloneError(de)
	}
	if errors.Is(err, context.Canceled) {
		return &domain.Error{Kind: domain.ErrNetwork, Message: "operation interrupted", Err: err}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &domain.Error{Kind: domain.ErrTimeout, Message: "operation timed out", Err: err}
	}
	return &domain.Error{Kind: domain.ErrInternal, Message: "internal operation failed", Err: err}
}
func invalid(message string) error {
	return &domain.Error{Kind: domain.ErrInvalidInput, Message: message}
}
func loadMetadata[T any](ctx context.Context, s *core, slot *metadata[T], ttl time.Duration, fetch func() (T, error), clone func(T) T) (T, error) {
	var zero T
	if ctx.Err() != nil {
		return zero, typed(ctx.Err())
	}
	s.mu.Lock()
	if slot.Valid && (slot.ExpiresAt.IsZero() || s.opts.Now().Before(slot.ExpiresAt)) {
		v := clone(slot.Value)
		s.mu.Unlock()
		return v, nil
	}
	generation := s.generation
	s.mu.Unlock()
	v, err := fetch()
	if err != nil {
		return zero, typed(err)
	}
	owned := clone(v)
	s.mu.Lock()
	if generation == s.generation {
		*slot = metadata[T]{Value: owned, Valid: true}
		if ttl > 0 {
			slot.ExpiresAt = s.opts.Now().Add(ttl)
		}
	}
	s.mu.Unlock()
	return clone(v), nil
}
func cloneLabels(v []domain.Label) []domain.Label { return append([]domain.Label{}, v...) }
func cloneUsers(v []domain.User) []domain.User    { return append([]domain.User{}, v...) }
func cloneInfo(v domain.BoardInfo) domain.BoardInfo {
	v.Options = append([]domain.StatusOption{}, v.Options...)
	return v
}
func (s *core) Session(ctx context.Context) (domain.Session, error) {
	return loadMetadata(ctx, s, &s.session, 0, func() (domain.Session, error) {
		if s.opts.ResolveRepository == nil {
			return domain.Session{}, &domain.Error{Kind: domain.ErrNoRepo, Message: "no repository was selected", Hint: "use --repo OWNER/REPO"}
		}
		ref, err := s.opts.ResolveRepository(ctx)
		if err != nil {
			return domain.Session{}, err
		}
		repo, err := s.backend.Repository(ctx, ref)
		if err != nil {
			return domain.Session{}, err
		}
		viewer, err := s.backend.Viewer(ctx)
		if err != nil {
			return domain.Session{}, err
		}
		return domain.Session{Repo: repo, Viewer: viewer}, nil
	}, func(v domain.Session) domain.Session { return v })
}
func (s *core) issueSession(ctx context.Context) (domain.Session, error) {
	v, err := s.Session(ctx)
	if err != nil {
		return v, err
	}
	if !v.Repo.IssuesEnabled {
		return v, &domain.Error{Kind: domain.ErrIssuesDisabled, Message: "issues are disabled for this repository"}
	}
	return v, nil
}
func (s *core) RefreshMetadata(ctx context.Context) error {
	s.mu.Lock()
	s.generation++
	s.session.Valid = false
	s.labels.Valid = false
	s.users.Valid = false
	s.board.Valid = false
	s.mu.Unlock()
	if err := s.backend.RefreshCredentials(ctx); err != nil {
		return typed(err)
	}
	_, err := s.Session(ctx)
	return err
}
func (s *core) RateLimit() domain.RateLimit {
	v := s.backend.RateLimit()
	v.Buckets = append([]domain.RateBucket{}, v.Buckets...)
	return v
}
func allPages[T any](ctx context.Context, fetch func(context.Context, domain.PageRequest) (domain.Page[T], error)) ([]T, error) {
	out := []T{}
	p := domain.PageRequest{Size: 100}
	seen := map[string]bool{}
	for {
		if ctx.Err() != nil {
			return nil, typed(ctx.Err())
		}
		page, err := fetch(ctx, p)
		if err != nil {
			return nil, typed(err)
		}
		out = append(out, page.Items...)
		if !page.HasMore {
			return out, nil
		}
		if page.NextCursor == "" || seen[page.NextCursor] {
			return nil, &domain.Error{Kind: domain.ErrGitHub, Message: "invalid pagination cursor"}
		}
		seen[page.NextCursor] = true
		p.Cursor = page.NextCursor
	}
}
func (s *core) Labels(ctx context.Context) ([]domain.Label, error) {
	return loadMetadata(ctx, s, &s.labels, time.Minute, func() ([]domain.Label, error) {
		session, err := s.issueSession(ctx)
		if err != nil {
			return nil, err
		}
		return allPages(ctx, func(ctx context.Context, p domain.PageRequest) (domain.Page[domain.Label], error) {
			return s.backend.ListLabels(ctx, session.Repo.Ref, p)
		})
	}, cloneLabels)
}
func (s *core) AssignableUsers(ctx context.Context) ([]domain.User, error) {
	return loadMetadata(ctx, s, &s.users, time.Minute, func() ([]domain.User, error) {
		session, err := s.issueSession(ctx)
		if err != nil {
			return nil, err
		}
		return allPages(ctx, func(ctx context.Context, p domain.PageRequest) (domain.Page[domain.User], error) {
			return s.backend.ListAssignableUsers(ctx, session.Repo.Ref, p)
		})
	}, cloneUsers)
}
