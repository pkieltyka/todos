package service

import (
	"context"
	"errors"
	"strings"

	"github.com/pkieltyka/todos/internal/domain"
)

func (s *core) Doctor(ctx context.Context) (domain.DoctorReport, error) {
	ctx = context.WithValue(domain.WithFreshSince(ctx, s.opts.Now()), doctorKey{}, true)
	// A doctor run owns its memo: it starts fresh, never touches the file store,
	// and shares one viewer read between its Viewer and Session checks.
	s.mu.Lock()
	local := &core{backend: &doctorBackend{Backend: s.backend}, settings: s.settings, opts: s.opts}
	if s.selection != nil {
		ref := *s.selection
		local.selection = &ref
	}
	s.mu.Unlock()
	return local.doctor(ctx)
}

type doctorBackend struct {
	Backend
	viewer     domain.User
	viewerErr  error
	readViewer bool
}

func (b *doctorBackend) Viewer(ctx context.Context) (domain.User, error) {
	if !b.readViewer {
		b.viewer, b.viewerErr = b.Backend.Viewer(ctx)
		b.readViewer = true
	}
	return b.viewer, b.viewerErr
}

func (s *core) doctor(ctx context.Context) (domain.DoctorReport, error) {
	report := domain.DoctorReport{Checks: []domain.DoctorCheck{}}
	names := []string{"Credentials", "Viewer", "Repository", "Board", "Board access", "Config"}
	var blocked error
	add := func(index int, detail string, err error) {
		check := domain.DoctorCheck{Name: names[index], OK: err == nil, Detail: detail}
		if err != nil {
			check.Detail = err.Error()
			var de *domain.Error
			if errors.As(err, &de) {
				check.Hint = de.Hint
			}
			if index < 3 && report.FirstFailure == nil {
				var de *domain.Error
				errors.As(typed(err), &de)
				report.FirstFailure = de
			}
		}
		report.Checks = append(report.Checks, check)
	}
	credentials, err := s.backend.Credentials(ctx)
	add(0, "credentials from "+credentials.Source, err)
	if err != nil {
		blocked = err
	}
	if blocked != nil {
		add(1, "", blockedCheck(blocked))
	} else {
		viewer, e := s.backend.Viewer(ctx)
		detail := "signed in as " + viewer.Login
		if e == nil {
			if scopes, e := s.backend.DoctorScopes(ctx); e == nil && len(scopes.Scopes) > 0 {
				detail += "; scopes: " + strings.Join(scopes.Scopes, ", ")
			}
		}
		add(1, detail, e)
		if e != nil {
			blocked = e
		}
	}
	var session domain.Session
	if blocked != nil {
		add(2, "", blockedCheck(blocked))
	} else {
		session, err = s.Session(ctx)
		if err == nil {
			if !session.Repo.IssuesEnabled {
				err = &domain.Error{Kind: domain.ErrIssuesDisabled, Message: "issues are disabled for this repository"}
			} else if !session.Repo.ViewerCanCreateIssues {
				err = &domain.Error{Kind: domain.ErrForbidden, Message: "viewer cannot create issues in this repository"}
			}
		}
		add(2, session.Repo.URL+"; issues enabled and creation permitted", err)
		if err != nil {
			blocked = err
		}
	}
	if blocked != nil {
		add(3, "", blockedCheck(blocked))
		add(4, "", blockedCheck(blocked))
	} else {
		board, e := s.BoardInfo(ctx)
		add(3, board.Project.Title, e)
		if e != nil {
			add(4, "", blockedCheck(e))
		} else {
			_, e = s.backend.ProjectItems(ctx, board.Project.NodeID, domain.PageRequest{Size: 1})
			if e == nil && !board.Project.ViewerCanUpdate {
				e = &domain.Error{Kind: domain.ErrForbidden, Message: "viewer cannot update this project"}
			}
			add(4, "project items readable and viewer can update", e)
		}
	}
	if s.settings == nil {
		add(5, "no config file (defaults)", nil)
	} else {
		check, e := s.settings.Check(ctx)
		check.Name = "Config"
		if e != nil {
			check.OK = false
			if check.Detail == "" {
				check.Detail = e.Error()
			}
		}
		report.Checks = append(report.Checks, check)
	}
	check := domain.DoctorCheck{Name: "Cache", OK: true, Detail: "cache disabled (not configured)"}
	if s.opts.CacheCheck != nil {
		check = s.opts.CacheCheck()
		check.Name = "Cache"
	}
	report.Checks = append(report.Checks, check)
	report.OK = report.FirstFailure == nil
	if !report.OK {
		return report, report.FirstFailure
	}
	return report, nil
}
func blockedCheck(err error) error {
	var de *domain.Error
	errors.As(typed(err), &de)
	return &domain.Error{Kind: de.Kind, Message: "blocked by an earlier failed check: " + de.Message, Hint: de.Hint, Err: err}
}
