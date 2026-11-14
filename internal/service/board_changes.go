package service

import (
	"context"
	"errors"
	"strings"

	"github.com/pkieltyka/todos/internal/domain"
)

func (s *core) membership(ctx context.Context, id, projectID domain.NodeID) (*domain.ProjectItem, error) {
	items, err := s.backend.IssuePlacements(ctx, id)
	if err != nil {
		return nil, typed(err)
	}
	for _, p := range items {
		if p.ProjectID == projectID {
			p.Placement.ItemID = p.ID
			return &p, nil
		}
	}
	return nil, nil
}

// GitHub reports this race as a definite UNPROCESSABLE rejection. Never use
// an uncertain response (or an unrelated failure) to continue mutating.
func duplicateProjectItem(err error) bool {
	var de *domain.Error
	return errors.As(err, &de) && de.Kind == domain.ErrInvalidInput && !de.Uncertain && strings.Contains(strings.ToLower(de.Message), "content already exists in this project")
}
func (s *core) placeIssue(ctx context.Context, i domain.Issue, b domain.BoardInfo, status *string, preserve bool) (domain.Change, error) {
	option, err := statusOption(b, status)
	if err != nil {
		return domain.Change{}, err
	}
	member, err := s.membership(ctx, i.NodeID, b.Project.NodeID)
	if err != nil {
		return domain.Change{Issue: i}, err
	}
	i = domain.CloneIssue(i)
	i.Board = nil
	changed := false
	completed := []string{}
	added := false
	if member == nil {
		p, e := s.backend.AddProjectItem(ctx, b.Project.NodeID, i.NodeID)
		if e != nil {
			if !duplicateProjectItem(e) {
				return domain.Change{Issue: i}, typed(e)
			}
			// The add was definitely rejected. A workflow may have won the race;
			// read its item once, then continue with that item rather than retry add.
			member, err = s.membership(ctx, i.NodeID, b.Project.NodeID)
			if err != nil {
				return domain.Change{Issue: i}, err
			}
			if member == nil {
				return domain.Change{Issue: i}, typed(e)
			}
		} else {
			member = &domain.ProjectItem{ID: p.ItemID, ProjectID: b.Project.NodeID, Placement: p}
			added = true
			changed = true
			completed = append(completed, "added_to_board")
		}
	}
	if member.Archived {
		p, e := s.backend.UnarchiveProjectItem(ctx, b.Project.NodeID, member.ID)
		if e != nil {
			return domain.Change{Issue: i}, typed(e)
		}
		i.Board = &p
		changed = true
		completed = append(completed, "added_to_board")
	} else {
		p := member.Placement
		i.Board = &p
	}
	if preserve && !added {
		return domain.Change{Issue: i, Changed: changed}, nil
	}
	same := option == nil && i.Board.Status == nil || option != nil && i.Board.Status != nil && *i.Board.Status == option.Name
	if same {
		return domain.Change{Issue: i, Changed: changed}, nil
	}
	if option == nil {
		err = s.backend.ClearProjectStatus(ctx, b.Project.NodeID, i.Board.ItemID, b.StatusFieldID)
	} else {
		err = s.backend.SetProjectStatus(ctx, b.Project.NodeID, i.Board.ItemID, b.StatusFieldID, option.ID)
	}
	if err != nil {
		if len(completed) > 0 {
			return domain.Change{Issue: i, Changed: true}, partial(err, &i, completed)
		}
		return domain.Change{Issue: i}, typed(err)
	}
	i.Board.Status = nil
	i.Board.OptionID = ""
	if option != nil {
		name := option.Name
		i.Board.Status = &name
		i.Board.OptionID = option.ID
	}
	return domain.Change{Issue: i, Changed: true}, nil
}
func (s *core) boardChange(ctx context.Context, n int, status *string, preserve bool) (domain.Change, error) {
	if _, err := s.issueSession(ctx); err != nil {
		return domain.Change{}, err
	}
	b, option, err := s.boardForStatus(ctx, status)
	if err != nil {
		return domain.Change{}, err
	}
	if option != nil {
		canonical := option.Name
		status = &canonical
	}
	d, err := s.target(ctx, n, b.Project.NodeID)
	if err != nil {
		return domain.Change{}, err
	}
	return s.placeIssue(ctx, d.Issue, b, status, preserve)
}
func (s *core) SetStatus(ctx context.Context, n int, status *string) (domain.Change, error) {
	return s.boardChange(ctx, n, status, false)
}
func (s *core) BoardAdd(ctx context.Context, n int, status *string) (domain.Change, error) {
	return s.boardChange(ctx, n, status, status == nil)
}
func (s *core) RemoveFromBoard(ctx context.Context, n int) (domain.Change, error) {
	if _, err := s.issueSession(ctx); err != nil {
		return domain.Change{}, err
	}
	b, err := s.BoardInfo(ctx)
	if err != nil {
		return domain.Change{}, err
	}
	d, err := s.target(ctx, n, b.Project.NodeID)
	if err != nil {
		return domain.Change{}, err
	}
	member, err := s.membership(ctx, d.Issue.NodeID, b.Project.NodeID)
	if err != nil {
		return domain.Change{}, err
	}
	if member == nil {
		return domain.Change{Issue: d.Issue}, nil
	}
	if err := s.backend.RemoveProjectItem(ctx, b.Project.NodeID, member.ID); err != nil {
		return domain.Change{}, typed(err)
	}
	d.Issue.Board = nil
	return domain.Change{Issue: d.Issue, Changed: true}, nil
}
