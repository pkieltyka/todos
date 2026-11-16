package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/pkieltyka/todos/internal/domain"
)

type doctorKey struct{}

func isDoctor(ctx context.Context) bool { v, _ := ctx.Value(doctorKey{}).(bool); return v }

func (s *core) clearMemoLocked() {
	s.generation++
	s.session.Valid = false
	s.labels.Valid = false
	s.users.Valid = false
	s.board.Valid = false
}

func (s *core) structureSpace(ctx context.Context) string {
	if s.opts.Cache == nil {
		return ""
	}
	key, err := s.backend.CredentialKey(ctx)
	if err != nil {
		return s.selectStructureSpace("")
	}
	if s.opts.ResolveRepository == nil {
		return s.selectStructureSpace("")
	}
	r, err := s.opts.ResolveRepository(ctx)
	if err != nil {
		return s.selectStructureSpace("")
	}
	space := strings.ToLower(key + "/structure/" + r.Host + "/" + r.Owner + "/" + r.Name)
	s.mu.Lock()
	if space == s.requestedSpace {
		space = s.canonicalSpace
	}
	s.mu.Unlock()
	return s.selectStructureSpace(space)
}

func (s *core) selectStructureSpace(space string) string {
	s.mu.Lock()
	if s.cacheSpace != space {
		s.clearMemoLocked()
		s.cacheSpace = space
	}
	s.mu.Unlock()
	return space
}

func (s *core) dropStructure() {
	s.mu.Lock()
	s.clearMemoLocked()
	s.structureAfter = s.opts.Now()
	space := s.cacheSpace
	s.mu.Unlock()
	if s.opts.Cache != nil && space != "" {
		s.opts.Cache.Drop(space, "")
	}
}

func (s *core) writeFailed(err error) {
	var de *domain.Error
	if errors.As(err, &de) && (de.Kind == domain.ErrNotFound || de.Kind == domain.ErrInvalidInput) {
		s.dropStructure()
	}
}

func validMetadata(v any) bool {
	switch v := v.(type) {
	case domain.Session:
		return v.Repo.NodeID != "" && v.Repo.Ref.Host != "" && v.Repo.Ref.Owner != "" && v.Repo.Ref.Name != "" && v.Viewer.NodeID != "" && v.Viewer.Login != ""
	case []domain.Label:
		if v == nil {
			return false
		}
		for _, l := range v {
			if l.NodeID == "" || l.Name == "" {
				return false
			}
		}
		return true
	case []domain.User:
		if v == nil {
			return false
		}
		for _, u := range v {
			if u.NodeID == "" || u.Login == "" {
				return false
			}
		}
		return true
	case domain.BoardInfo:
		return v.Project.NodeID != "" && v.Project.Usable && v.StatusFieldID != ""
	}
	return false
}

func boardKey(ref *domain.ProjectRef) string {
	if ref == nil {
		return "board/linked"
	}
	normalized := *ref
	normalized.Host = strings.ToLower(normalized.Host)
	normalized.Owner = strings.ToLower(normalized.Owner)
	b, _ := json.Marshal(normalized)
	return "board/" + string(b)
}

func metadataIdentity(v any, space string) bool {
	switch v := v.(type) {
	case domain.Session:
		r := v.Repo.Ref
		return strings.HasSuffix(space, "/structure/"+strings.ToLower(r.Host+"/"+r.Owner+"/"+r.Name))
	}
	return true
}

func (s *core) boardChoice(ctx context.Context, repo domain.RepoRef) (*domain.ProjectRef, error) {
	s.mu.Lock()
	if s.selection != nil {
		copy := *s.selection
		s.mu.Unlock()
		return &copy, nil
	}
	s.mu.Unlock()
	if s.opts.Project != "" {
		r, err := ParseProjectRef(s.opts.Project, repo)
		return &r, err
	}
	if s.settings != nil {
		return s.settings.RememberedBoard(ctx, repo)
	}
	return nil, nil
}
