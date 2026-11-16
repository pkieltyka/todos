package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/pkieltyka/todos/internal/domain"
)

type Options struct {
	ResolveRepository func(context.Context) (domain.RepoRef, error)
	Project           string
	Now               func() time.Time
	Wait              func(context.Context, time.Duration) error
	Cache             CacheStore
	CacheCheck        func() domain.DoctorCheck
}
type metadata[T any] struct {
	Value      T
	ExpiresAt  time.Time
	Valid      bool
	ReadAt     time.Time
	Space, Key string
}
type core struct {
	backend        Backend
	settings       SettingsStore
	opts           Options
	mu             sync.Mutex
	session        metadata[domain.Session]
	labels         metadata[[]domain.Label]
	users          metadata[[]domain.User]
	board          metadata[domain.BoardInfo]
	selection      *domain.ProjectRef
	generation     uint64
	structureAfter time.Time
	cacheSpace     string
	requestedSpace string
	canonicalSpace string
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
	s := &core{settings: settings, opts: opts}
	s.backend = &structureBackend{Backend: b, owner: s}
	return s
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
func loadMetadata[T any](ctx context.Context, s *core, slot *metadata[T], key string, ttl time.Duration, fetch func() (T, error), clone func(T) T) (T, error) {
	var zero T
	if ctx.Err() != nil {
		return zero, typed(ctx.Err())
	}
	space := s.structureSpace(ctx)
	s.mu.Lock()
	after := s.structureAfter
	if since, marked := domain.FreshSince(ctx); marked && since.After(after) {
		after = since
	}
	if slot.Valid && slot.Space == space && slot.Key == key && !slot.ReadAt.Before(after) && !slot.ReadAt.After(s.opts.Now()) && s.opts.Now().Before(slot.ExpiresAt) {
		v := clone(slot.Value)
		s.mu.Unlock()
		return v, nil
	}
	generation := s.generation
	s.mu.Unlock()
	var v T
	var err error
	at := s.opts.Now()
	hit := false
	if s.opts.Cache != nil && space != "" && !isDoctor(ctx) {
		if data, stored, ok := s.opts.Cache.Get(space, key); ok && !stored.IsZero() && !stored.Before(after) && !stored.After(at) && at.Before(stored.Add(ttl)) && json.Unmarshal(data, &v) == nil && validMetadata(v) && metadataIdentity(v, space) {
			at, hit = stored, true
		}
	}
	if !hit {
		at = s.opts.Now() // the read's send time, not cache lookup/completion time
		v, err = fetch()
	}
	if err != nil {
		return zero, typed(err)
	}
	if ctx.Err() != nil {
		return zero, typed(ctx.Err())
	}
	// GitHub may redirect an old repository name to its canonical name. Once
	// known, use that space, including for this session's first persisted fill.
	if session, ok := any(v).(domain.Session); ok && space != "" && !metadataIdentity(session, space) {
		canonical := space[:strings.Index(space, "/structure/")] + "/structure/" + strings.ToLower(session.Repo.Ref.Host+"/"+session.Repo.Ref.Owner+"/"+session.Repo.Ref.Name)
		s.mu.Lock()
		if generation == s.generation {
			s.requestedSpace, s.canonicalSpace = space, canonical
			s.clearMemoLocked()
			s.cacheSpace = canonical
			space, generation = canonical, s.generation
		}
		s.mu.Unlock()
	}
	// A concurrent auth refresh may have changed the client's resolved token
	// while this read was in flight. Never persist it in the old partition.
	if s.opts.Cache != nil && space != "" {
		s.structureSpace(ctx) // a changed partition advances the generation
		if ctx.Err() != nil {
			return zero, typed(ctx.Err())
		}
	}
	owned := clone(v)
	s.mu.Lock()
	current := generation == s.generation
	if current {
		*slot = metadata[T]{Value: owned, Valid: true, ReadAt: at, ExpiresAt: at.Add(ttl), Space: space, Key: key}
	}
	s.mu.Unlock()
	if current && !hit && s.opts.Cache != nil && space != "" && !isDoctor(ctx) {
		if data, e := json.Marshal(v); e == nil {
			s.opts.Cache.Put(space, key, data, at)
		}
	}
	return clone(v), nil
}
func cloneLabels(v []domain.Label) []domain.Label { return append([]domain.Label{}, v...) }
func cloneUsers(v []domain.User) []domain.User    { return append([]domain.User{}, v...) }
func cloneInfo(v domain.BoardInfo) domain.BoardInfo {
	v.Options = append([]domain.StatusOption{}, v.Options...)
	return v
}
func (s *core) Session(ctx context.Context) (domain.Session, error) {
	return loadMetadata(ctx, s, &s.session, "session", 5*time.Minute, func() (domain.Session, error) {
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
	s.dropStructure()
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
	return loadMetadata(ctx, s, &s.labels, "labels", 5*time.Minute, func() ([]domain.Label, error) {
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
	return loadMetadata(ctx, s, &s.users, "users", 5*time.Minute, func() ([]domain.User, error) {
		session, err := s.issueSession(ctx)
		if err != nil {
			return nil, err
		}
		return allPages(ctx, func(ctx context.Context, p domain.PageRequest) (domain.Page[domain.User], error) {
			return s.backend.ListAssignableUsers(ctx, session.Repo.Ref, p)
		})
	}, cloneUsers)
}
