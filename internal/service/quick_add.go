package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/pkieltyka/todos/internal/domain"
	"github.com/pkieltyka/todos/internal/quickadd"
)

func (s *core) PlanQuickAdd(ctx context.Context, text string, opts domain.QuickAddOptions) (domain.QuickAddPlan, error) {
	p, _, err := s.planQuickAdd(ctx, text, opts)
	return p, err
}
func (s *core) planQuickAdd(ctx context.Context, text string, opts domain.QuickAddOptions) (domain.QuickAddPlan, *domain.BoardInfo, error) {
	p := quickadd.Parse(text)
	p.Options = domain.ClonePlan(domain.QuickAddPlan{Options: opts}).Options
	if p.Options.Checked == "" {
		p.Options.Checked = "skip"
	}
	if p.Options.Checked != "skip" && p.Options.Checked != "close" {
		return p, nil, invalid("checked must be skip or close")
	}
	p.Options.AddToBoard = p.Options.AddToBoard || p.Options.Status != nil
	if len(p.Problems) > 0 {
		return p, nil, nil
	}
	_, board, err := s.validateNames(ctx, p.Options.Assignees, p.Options.Labels, p.Options.Status, p.Options.AddToBoard)
	if err != nil {
		de := typed(err).(*domain.Error)
		if de.Kind == domain.ErrInvalidInput {
			p.Problems = append(p.Problems, domain.Problem{Line: p.Items[0].Line, Message: de.Message})
			return p, nil, nil
		}
		return p, nil, de
	}
	// Canonical names make previews stable without changing their public inputs.
	users, err := s.usersForNames(ctx, p.Options.Assignees)
	if err != nil {
		return p, nil, err
	}
	p.Options.Assignees = []string{}
	for _, u := range users {
		p.Options.Assignees = append(p.Options.Assignees, u.Login)
	}
	labels, err := s.labelsForNames(ctx, p.Options.Labels)
	if err != nil {
		return p, nil, err
	}
	p.Options.Labels = []string{}
	for _, l := range labels {
		p.Options.Labels = append(p.Options.Labels, l.Name)
	}
	if board != nil && p.Options.Status != nil {
		o, _ := statusOption(*board, p.Options.Status)
		name := o.Name
		p.Options.Status = &name
	}
	for _, item := range p.Items {
		if item.Checked && p.Options.Checked == "skip" {
			p.ToSkip++
			continue
		}
		p.ToCreate++
		p.WriteCount++
		if item.Checked {
			p.WriteCount++
		} else if p.Options.AddToBoard {
			p.WriteCount += 2
		}
	}
	return p, board, nil
}
func quickProblems(problems []domain.Problem) error {
	messages := make([]string, 0, len(problems))
	for _, p := range problems {
		messages = append(messages, fmt.Sprintf("line %d: %s", p.Line, p.Message))
	}
	line := 0
	if len(problems) > 0 {
		line = problems[0].Line
	}
	return &domain.Error{Kind: domain.ErrInvalidInput, Message: strings.Join(messages, "; "), Line: line}
}

// A run owns its metadata and pacer. It never installs pacing on the service
// shared by CLI/TUI callers, or holds that service's mutex during I/O.
func (s *core) quickCore(board *domain.BoardInfo) *core {
	s.mu.Lock()
	defer s.mu.Unlock()
	run := &core{backend: &pacedBackend{Backend: s.backend, now: s.opts.Now, wait: s.opts.Wait}, settings: s.settings, opts: s.opts, session: s.session, labels: s.labels, users: s.users, board: s.board, generation: s.generation}
	run.labels.Value = cloneLabels(s.labels.Value)
	run.users.Value = cloneUsers(s.users.Value)
	run.board.Value = cloneInfo(s.board.Value)
	if s.selection != nil {
		ref := *s.selection
		run.selection = &ref
	}
	if board != nil {
		run.board.Value = cloneInfo(*board)
		run.board.Valid = true
		ref := board.Project.Ref
		run.selection = &ref
	}
	return run
}
func initialQuickResult(p domain.QuickAddPlan, dry bool) domain.QuickAddResult {
	r := domain.QuickAddResult{Items: make([]domain.QuickAddItemResult, len(p.Items))}
	for j, item := range p.Items {
		outcome := domain.QuickAddOutcome("unattempted")
		if item.Checked && p.Options.Checked == "skip" {
			outcome = "skipped"
		} else if dry {
			outcome = "planned"
		}
		r.Items[j] = domain.QuickAddItemResult{Item: item, Result: outcome}
	}
	return r
}
func countQuickResult(r *domain.QuickAddResult) {
	r.Counts = domain.QuickAddCounts{}
	for _, row := range r.Items {
		switch row.Result {
		case "created":
			r.Counts.Created++
		case "failed":
			r.Counts.Failed++
		case "uncertain":
			r.Counts.Uncertain++
		case "unattempted":
			r.Counts.Unattempted++
		case "skipped":
			r.Counts.Skipped++
		case "planned":
			r.Counts.Planned++
		}
	}
}
func quickRunError(err error, result domain.QuickAddResult) error {
	if err == nil {
		return nil
	}
	de := typed(err).(*domain.Error)
	if result.Counts.Created+result.Counts.Uncertain == 0 {
		return de
	}
	out := domain.CloneError(de)
	out.Kind = domain.ErrPartial
	out.Cause = de.Kind
	if de.Kind == domain.ErrPartial {
		out.Cause = de.Cause
	}
	out.Err = de
	out.Completed = []string{}
	out.Message = "quick add stopped: " + de.Message
	out.Hint = "check the reported issues; send only remaining items, and check uncertain items on GitHub before resubmitting"
	for _, row := range result.Items {
		if row.Issue != nil {
			i := domain.CloneIssue(*row.Issue)
			out.Issue = &i
		}
		if row.Result == "uncertain" || row.Error != nil && row.Error.Uncertain {
			out.Uncertain = true
		}
	}
	return out
}
func quickProgress(opts domain.QuickAddRunOptions, index int, phase string, row domain.QuickAddItemResult) error {
	if opts.OnProgress == nil {
		return nil
	}
	snapshot := domain.CloneQuickAddResult(domain.QuickAddResult{Items: []domain.QuickAddItemResult{row}}).Items[0]
	p := domain.QuickAddProgress{Index: index, Phase: phase}
	if row.Issue != nil {
		p.Number = row.Issue.Number
	}
	if phase == "settled" {
		p.Outcome = snapshot.Result
		p.Err = domain.CloneError(snapshot.Error)
		p.Item = &snapshot
	}
	if err := opts.OnProgress(p); err != nil {
		return &domain.Error{Kind: domain.ErrConfig, Message: "quick-add progress could not be saved", Err: err}
	}
	return nil
}
func recordQuickIssue(row *domain.QuickAddItemResult, change domain.Change, err error) {
	var known *domain.Issue
	if change.Issue.Number > 0 {
		known = &change.Issue
	}
	if err != nil {
		de := typed(err).(*domain.Error)
		if de.Kind == domain.ErrPartial && de.Issue != nil {
			known = de.Issue
		}
	}
	if known != nil {
		i := domain.CloneIssue(*known)
		row.Issue = &i
		row.Result = "created"
		row.Closed = i.State == "closed"
		row.OnBoard = i.Board != nil
	}
}
func (s *core) RunQuickAdd(ctx context.Context, plan domain.QuickAddPlan, opts domain.QuickAddRunOptions) (result domain.QuickAddResult, err error) {
	p, board, e := s.planQuickAdd(ctx, plan.Source, plan.Options)
	if e != nil {
		return result, e
	}
	if len(p.Problems) > 0 {
		return result, quickProblems(p.Problems)
	}
	result = initialQuickResult(p, opts.DryRun)
	defer func() { countQuickResult(&result); err = quickRunError(err, result) }()
	run := s.quickCore(board)
	for j := range result.Items {
		row := &result.Items[j]
		if ctx.Err() != nil {
			return result, typed(ctx.Err())
		}
		if opts.StopRequested != nil && opts.StopRequested() {
			return result, typed(context.Canceled)
		}
		if row.Result == "skipped" || opts.DryRun {
			if e := quickProgress(opts, j, "settled", *row); e != nil {
				return result, e
			}
			continue
		}
		itemErr := quickProgress(opts, j, "creating", *row)
		if itemErr == nil {
			change, e := run.CreateIssue(ctx, domain.NewIssue{Title: row.Item.Title, Body: row.Item.Body, Assignees: p.Options.Assignees, Labels: p.Options.Labels})
			itemErr = e
			recordQuickIssue(row, change, e)
			if e != nil && row.Issue == nil {
				if typed(e).(*domain.Error).Uncertain {
					row.Result = "uncertain"
				} else {
					row.Result = "failed"
				}
			}
		}
		if itemErr == nil && row.Item.Checked {
			itemErr = quickProgress(opts, j, "closing", *row)
			if itemErr == nil {
				change, e := run.Close(ctx, row.Issue.Number, domain.CloseOptions{Reason: "completed"})
				itemErr = e
				recordQuickIssue(row, change, e)
			}
		} else if itemErr == nil && p.Options.AddToBoard {
			itemErr = quickProgress(opts, j, "placing", *row)
			if itemErr == nil {
				change, e := run.boardChange(ctx, row.Issue.Number, p.Options.Status, false, true)
				itemErr = e
				recordQuickIssue(row, change, e)
			}
		}
		if itemErr != nil {
			row.Error = typed(itemErr).(*domain.Error)
			if row.Result == "unattempted" {
				row.Result = "failed"
			}
		}
		if e := quickProgress(opts, j, "settled", *row); e != nil {
			return result, e
		}
		if itemErr != nil {
			return result, itemErr
		}
	}
	return result, nil
}
