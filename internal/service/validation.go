package service

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/pkieltyka/todos/internal/domain"
)

func validateTitle(title string) error {
	if !utf8.ValidString(title) {
		return invalid("title must be valid UTF-8")
	}
	if strings.TrimSpace(title) == "" {
		return invalid("title must not be blank")
	}
	if utf8.RuneCountInString(title) > 256 {
		return invalid("title must contain at most 256 characters")
	}
	return nil
}
func validateBody(body string) error {
	if !utf8.ValidString(body) {
		return invalid("body must be valid UTF-8")
	}
	return nil
}
func (s *core) usersForNames(ctx context.Context, names []string) ([]domain.User, error) {
	if len(names) == 0 {
		return []domain.User{}, nil
	}
	session, err := s.issueSession(ctx)
	if err != nil {
		return nil, err
	}
	names = append([]string{}, names...)
	for i := range names {
		if names[i] == "@me" {
			names[i] = session.Viewer.Login
		}
	}
	for attempt := 0; attempt < 2; attempt++ {
		users, err := s.AssignableUsers(ctx)
		if err != nil {
			return nil, err
		}
		out := []domain.User{}
		unknown := ""
		missing := false
		for _, name := range names {
			found := false
			for _, u := range users {
				if strings.EqualFold(name, u.Login) {
					found = true
					duplicate := false
					for _, v := range out {
						if strings.EqualFold(u.Login, v.Login) {
							duplicate = true
						}
					}
					if !duplicate {
						out = append(out, u)
					}
					break
				}
			}
			if !found {
				unknown = name
				missing = true
				break
			}
		}
		if !missing {
			return out, nil
		}
		if attempt == 0 {
			s.mu.Lock()
			s.generation++
			s.users.Valid = false
			s.mu.Unlock()
			continue
		}
		valid := []string{}
		for _, u := range users {
			valid = append(valid, u.Login)
		}
		return nil, invalid(fmt.Sprintf("unknown assignee %q; valid assignees: %s", unknown, strings.Join(valid, ", ")))
	}
	panic("unreachable")
}
func (s *core) labelsForNames(ctx context.Context, names []string) ([]domain.Label, error) {
	if len(names) == 0 {
		return []domain.Label{}, nil
	}
	for attempt := 0; attempt < 2; attempt++ {
		labels, err := s.Labels(ctx)
		if err != nil {
			return nil, err
		}
		out := []domain.Label{}
		unknown := ""
		missing := false
		for _, name := range names {
			found := false
			for _, l := range labels {
				if name == l.Name {
					found = true
					duplicate := false
					for _, v := range out {
						if v.Name == name {
							duplicate = true
						}
					}
					if !duplicate {
						out = append(out, l)
					}
					break
				}
			}
			if !found {
				unknown = name
				missing = true
				break
			}
		}
		if !missing {
			return out, nil
		}
		if attempt == 0 {
			s.mu.Lock()
			s.generation++
			s.labels.Valid = false
			s.mu.Unlock()
			continue
		}
		valid := []string{}
		for _, l := range labels {
			valid = append(valid, l.Name)
		}
		return nil, invalid(fmt.Sprintf("unknown label %q; valid labels: %s", unknown, strings.Join(valid, ", ")))
	}
	panic("unreachable")
}
func statusOption(b domain.BoardInfo, status *string) (*domain.StatusOption, error) {
	if status == nil {
		return nil, nil
	}
	for _, o := range b.Options {
		if o.Name == *status {
			return &o, nil
		}
	}
	var match *domain.StatusOption
	for _, o := range b.Options {
		if strings.EqualFold(o.Name, *status) {
			if match != nil {
				return nil, invalid(fmt.Sprintf("ambiguous status %q; use an exact option name", *status))
			}
			v := o
			match = &v
		}
	}
	if match != nil {
		return match, nil
	}
	names := []string{}
	for _, o := range b.Options {
		names = append(names, o.Name)
	}
	return nil, invalid(fmt.Sprintf("unknown status %q; valid statuses: %s", *status, strings.Join(names, ", ")))
}
func (s *core) boardForStatus(ctx context.Context, status *string) (domain.BoardInfo, *domain.StatusOption, error) {
	for attempt := 0; attempt < 2; attempt++ {
		b, err := s.BoardInfo(ctx)
		if err != nil {
			return b, nil, err
		}
		o, err := statusOption(b, status)
		if err == nil {
			return b, o, nil
		}
		if attempt == 1 {
			return b, nil, err
		}
		s.mu.Lock()
		s.generation++
		s.board.Valid = false
		s.mu.Unlock()
	}
	panic("unreachable")
}
func (s *core) validateNames(ctx context.Context, assignees, labels []string, status *string, addToBoard bool) (domain.CreateInput, *domain.BoardInfo, error) {
	session, err := s.issueSession(ctx)
	if err != nil {
		return domain.CreateInput{}, nil, err
	}
	users, err := s.usersForNames(ctx, assignees)
	if err != nil {
		return domain.CreateInput{}, nil, err
	}
	if len(users) > 10 {
		return domain.CreateInput{}, nil, invalid("an issue may have at most 10 assignees")
	}
	ls, err := s.labelsForNames(ctx, labels)
	if err != nil {
		return domain.CreateInput{}, nil, err
	}
	in := domain.CreateInput{RepositoryID: session.Repo.NodeID, AssigneeIDs: []domain.NodeID{}, LabelIDs: []domain.NodeID{}}
	for _, u := range users {
		in.AssigneeIDs = append(in.AssigneeIDs, u.NodeID)
	}
	for _, l := range ls {
		in.LabelIDs = append(in.LabelIDs, l.NodeID)
	}
	var board *domain.BoardInfo
	if addToBoard || status != nil {
		b, _, err := s.boardForStatus(ctx, status)
		if err != nil {
			return in, nil, err
		}
		board = &b
	}
	return in, board, nil
}
func hasLogin(names []string, name string) bool {
	for _, n := range names {
		if strings.EqualFold(n, name) {
			return true
		}
	}
	return false
}
func hasLabel(labels []domain.Label, name string) bool {
	for _, l := range labels {
		if l.Name == name {
			return true
		}
	}
	return false
}
func userIDs(users []domain.User) []domain.NodeID {
	out := []domain.NodeID{}
	for _, u := range users {
		out = append(out, u.NodeID)
	}
	return out
}
func labelIDs(labels []domain.Label) []domain.NodeID {
	out := []domain.NodeID{}
	for _, l := range labels {
		out = append(out, l.NodeID)
	}
	return out
}

// Resolve present removals only from the fresh issue. Repository metadata is
// consulted solely for absent names, to distinguish no-ops from typos.
func (s *core) removalUsers(ctx context.Context, issue domain.Issue, names []string) ([]domain.User, error) {
	session, err := s.issueSession(ctx)
	if err != nil {
		return nil, err
	}
	out := []domain.User{}
	absent := []string{}
	seen := []string{}
	for _, name := range names {
		if name == "@me" {
			name = session.Viewer.Login
		}
		if hasLogin(seen, name) {
			continue
		}
		seen = append(seen, name)
		present := false
		for _, login := range issue.Assignees {
			if strings.EqualFold(login, name) {
				id := issue.AssigneeIDs[login]
				if id == "" {
					return nil, &domain.Error{Kind: domain.ErrInternal, Message: "issue assignee is missing its node ID"}
				}
				out = append(out, domain.User{Login: login, NodeID: id})
				present = true
				break
			}
		}
		if !present {
			absent = append(absent, name)
		}
	}
	known, err := s.usersForNames(ctx, absent)
	if err != nil {
		return nil, err
	}
	return append(out, known...), nil
}
func (s *core) removalLabels(ctx context.Context, issue domain.Issue, names []string) ([]domain.Label, error) {
	out := []domain.Label{}
	absent := []string{}
	seen := map[string]bool{}
	for _, name := range names {
		if seen[name] {
			continue
		}
		seen[name] = true
		present := false
		for _, label := range issue.Labels {
			if label.Name == name {
				if label.NodeID == "" {
					return nil, &domain.Error{Kind: domain.ErrInternal, Message: "issue label is missing its node ID"}
				}
				out = append(out, label)
				present = true
				break
			}
		}
		if !present {
			absent = append(absent, name)
		}
	}
	known, err := s.labelsForNames(ctx, absent)
	if err != nil {
		return nil, err
	}
	return append(out, known...), nil
}
