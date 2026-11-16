package service

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/pkieltyka/todos/internal/domain"
)

func noBoard(message string, candidates []domain.Project) error {
	return &domain.Error{Kind: domain.ErrNoBoard, Message: message, Hint: "run todos board list, then todos board use PROJECT", Candidates: append([]domain.Project{}, candidates...)}
}
func (s *core) BoardInfo(ctx context.Context) (domain.BoardInfo, error) {
	info, _, err := s.boardInfo(ctx)
	return info, err
}

// live says whether this call already read fields from GitHub. Board reuses
// that read on a cold resolution and refreshes fields after a memo/file hit.
func (s *core) boardInfo(ctx context.Context) (info domain.BoardInfo, live bool, err error) {
	session, err := s.Session(ctx)
	if err != nil {
		return domain.BoardInfo{}, false, err
	}
	ref, err := s.boardChoice(ctx, session.Repo.Ref)
	if err != nil {
		return domain.BoardInfo{}, false, typed(err)
	}
	info, err = loadMetadata(ctx, s, &s.board, boardKey(ref), 5*time.Minute, func() (domain.BoardInfo, error) {
		live = true
		return s.resolveBoard(ctx)
	}, cloneInfo)
	return info, live, err
}
func (s *core) validateBoard(ctx context.Context, p domain.Project) (domain.BoardInfo, error) {
	info, err := s.backend.ProjectFields(ctx, p.NodeID)
	if err != nil {
		return domain.BoardInfo{}, typed(err)
	}
	info.Project.Linked = p.Linked
	if info.StatusFieldID == "" || !info.Project.Usable {
		info.Project.Usable = false
		return domain.BoardInfo{}, noBoard("project has no single-select Status field", []domain.Project{info.Project})
	}
	return cloneInfo(info), nil
}
func (s *core) resolveBoard(ctx context.Context) (domain.BoardInfo, error) {
	session, err := s.Session(ctx)
	if err != nil {
		return domain.BoardInfo{}, err
	}
	s.mu.Lock()
	var ref *domain.ProjectRef
	if s.selection != nil {
		copy := *s.selection
		ref = &copy
	}
	s.mu.Unlock()
	if ref == nil && s.opts.Project != "" {
		r, e := ParseProjectRef(s.opts.Project, session.Repo.Ref)
		if e != nil {
			return domain.BoardInfo{}, e
		}
		ref = &r
	}
	if ref == nil && s.settings != nil {
		ref, err = s.settings.RememberedBoard(ctx, session.Repo.Ref)
		if err != nil {
			return domain.BoardInfo{}, typed(err)
		}
	}
	if ref != nil {
		if !strings.EqualFold(ref.Host, session.Repo.Ref.Host) {
			return domain.BoardInfo{}, invalid("project host must match repository")
		}
		p, e := s.backend.Project(ctx, *ref)
		if e != nil {
			return domain.BoardInfo{}, typed(e)
		}
		return s.validateBoard(ctx, p)
	}
	linked, err := allPages(ctx, func(ctx context.Context, p domain.PageRequest) (domain.Page[domain.Project], error) {
		return s.backend.LinkedProjects(ctx, session.Repo.Ref, p)
	})
	if err != nil {
		return domain.BoardInfo{}, err
	}
	if len(linked) == 0 {
		return domain.BoardInfo{}, noBoard("no linked project was found", nil)
	}
	if len(linked) > 1 {
		return domain.BoardInfo{}, noBoard("several projects are linked; choose a board", linked)
	}
	linked[0].Linked = true
	return s.validateBoard(ctx, linked[0])
}
func (s *core) BoardCandidates(ctx context.Context) (domain.ProjectsResult, error) {
	session, err := s.Session(ctx)
	if err != nil {
		return domain.ProjectsResult{}, err
	}
	result := domain.ProjectsResult{Projects: []domain.Project{}}
	seen := map[domain.NodeID]bool{}
	linked, err := allPages(ctx, func(ctx context.Context, p domain.PageRequest) (domain.Page[domain.Project], error) {
		return s.backend.LinkedProjects(ctx, session.Repo.Ref, p)
	})
	if err != nil {
		return result, err
	}
	for i := range linked {
		linked[i].Linked = true
	}
	groups := [][]domain.Project{linked}
	owners := []string{session.Repo.Ref.Owner}
	if !strings.EqualFold(session.Viewer.Login, session.Repo.Ref.Owner) {
		owners = append(owners, session.Viewer.Login)
	}
	for _, owner := range owners {
		projects, e := allPages(ctx, func(ctx context.Context, p domain.PageRequest) (domain.Page[domain.Project], error) {
			return s.backend.OwnerProjects(ctx, owner, p)
		})
		if e != nil {
			return domain.ProjectsResult{}, e
		}
		groups = append(groups, projects)
	}
	for _, group := range groups {
		for _, p := range group {
			if seen[p.NodeID] {
				continue
			}
			seen[p.NodeID] = true
			info, e := s.backend.ProjectFields(ctx, p.NodeID)
			if e != nil {
				return domain.ProjectsResult{}, typed(e)
			}
			p.Usable = info.Project.Usable && info.StatusFieldID != ""
			result.Projects = append(result.Projects, p)
		}
	}
	if selected, e := s.BoardInfo(ctx); e == nil {
		p := selected.Project
		result.Selected = &p
	} else if ctx.Err() != nil {
		return domain.ProjectsResult{}, typed(ctx.Err())
	}
	return result, nil
}
func (s *core) UseBoard(ctx context.Context, ref domain.ProjectRef) (domain.Project, error) {
	session, err := s.Session(ctx)
	if err != nil {
		return domain.Project{}, err
	}
	if ref.Host == "" {
		ref.Host = session.Repo.Ref.Host
	}
	if !strings.EqualFold(ref.Host, session.Repo.Ref.Host) || ref.Number < 1 || ref.Owner == "" {
		return domain.Project{}, invalid("invalid project reference")
	}
	at := s.opts.Now()
	p, err := s.backend.Project(ctx, ref)
	if err != nil {
		return domain.Project{}, typed(err)
	}
	info, err := s.validateBoard(ctx, p)
	if err != nil {
		return domain.Project{}, err
	}
	if s.settings == nil {
		return domain.Project{}, &domain.Error{Kind: domain.ErrConfig, Message: "board preferences are unavailable"}
	}
	if err = s.settings.RememberBoard(ctx, session.Repo.Ref, ref); err != nil {
		return domain.Project{}, typed(err)
	}
	s.mu.Lock()
	s.generation++
	copy := ref
	s.selection = &copy
	space := s.cacheSpace
	key := boardKey(&ref)
	s.board = metadata[domain.BoardInfo]{Value: cloneInfo(info), Valid: true, ReadAt: at, ExpiresAt: at.Add(5 * time.Minute), Space: space, Key: key}
	s.mu.Unlock()
	if s.opts.Cache != nil && space != "" {
		if data, e := json.Marshal(info); e == nil {
			s.opts.Cache.Put(space, key, data, at)
		}
	}
	return info.Project, nil
}
func (s *core) Board(ctx context.Context) (domain.Board, error) {
	session, err := s.Session(ctx)
	if err != nil {
		return domain.Board{}, err
	}
	info, live, err := s.boardInfo(ctx)
	if err != nil {
		return domain.Board{}, err
	}
	if !live {
		info, err = s.refreshBoardFields(ctx, info)
		if err != nil {
			return domain.Board{}, err
		}
	}
	result := domain.Board{Project: info.Project, Columns: []domain.Column{}}
	cards := []domain.Card{}
	p := domain.PageRequest{Size: 100}
	seen := map[string]bool{}
	for {
		page, e := s.backend.ProjectItems(ctx, info.Project.NodeID, p)
		if e != nil {
			return domain.Board{}, typed(e)
		}
		result.TotalItems = page.Total
		for _, item := range page.Items {
			if item.Archived {
				continue
			}
			if result.ScannedCount == 1000 {
				result.Truncated = true
				break
			}
			result.ScannedCount++
			if item.Kind != "Issue" || item.Issue == nil || item.RepositoryID != session.Repo.NodeID {
				result.HiddenCount++
				continue
			}
			card := domain.Card{Issue: domain.CloneIssue(*item.Issue)}
			placement := item.Placement
			card.Board = &placement
			result.VisibleCount++
			cards = append(cards, card)
		}
		if !page.HasMore {
			break
		}
		if result.ScannedCount >= 1000 {
			result.Truncated = true
			break
		}
		if page.NextCursor == "" || seen[page.NextCursor] {
			return domain.Board{}, &domain.Error{Kind: domain.ErrGitHub, Message: "invalid board pagination cursor"}
		}
		seen[page.NextCursor] = true
		p.Cursor = page.NextCursor
		p.Size = min(100, 1000-result.ScannedCount)
	}
	result.Project = info.Project
	indices := map[string]int{}
	for _, option := range info.Options {
		name := option.Name
		indices[option.ID] = len(result.Columns)
		result.Columns = append(result.Columns, domain.Column{Status: &name, OptionID: option.ID, Cards: []domain.Card{}})
	}
	noStatus := []domain.Card{}
	for _, card := range cards {
		if index, ok := indices[card.Board.OptionID]; ok {
			card.Board.Status = result.Columns[index].Status
			result.Columns[index].Cards = append(result.Columns[index].Cards, card)
		} else {
			card.Board.Status, card.Board.OptionID = nil, ""
			noStatus = append(noStatus, card)
		}
	}
	if len(noStatus) > 0 {
		result.Columns = append([]domain.Column{{Cards: noStatus}}, result.Columns...)
	}
	result.Truncated = result.Truncated || result.TotalItems > 1000
	result.CardsTruncated = result.VisibleCount > 500
	return domain.CloneBoard(result), nil
}

// Board always reads live fields, even with warm structure. Publish those
// options for BoardInfo and pickers, with the same five-minute expiry.
func (s *core) refreshBoardFields(ctx context.Context, old domain.BoardInfo) (domain.BoardInfo, error) {
	s.mu.Lock()
	generation, space, key := s.generation, s.cacheSpace, s.board.Key
	s.mu.Unlock()
	readAt := s.opts.Now()
	info, err := s.validateBoard(ctx, old.Project)
	if err != nil {
		return domain.BoardInfo{}, err
	}
	if ctx.Err() != nil {
		return domain.BoardInfo{}, typed(ctx.Err())
	}
	s.structureSpace(ctx)
	s.mu.Lock()
	current := generation == s.generation
	if current {
		s.board = metadata[domain.BoardInfo]{Value: cloneInfo(info), Valid: true, ReadAt: readAt, ExpiresAt: readAt.Add(5 * time.Minute), Space: space, Key: key}
	}
	s.mu.Unlock()
	if current && s.opts.Cache != nil && space != "" {
		if data, err := json.Marshal(info); err == nil {
			s.opts.Cache.Put(space, key, data, readAt)
		}
	}
	return info, nil
}
