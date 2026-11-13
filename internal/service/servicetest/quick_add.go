package servicetest

import (
	"context"
	"fmt"

	"github.com/pkieltyka/todos/internal/domain"
	"github.com/pkieltyka/todos/internal/quickadd"
)

func (f *Fake) PlanQuickAdd(ctx context.Context, text string, opts domain.QuickAddOptions) (domain.QuickAddPlan, error) {
	opts = domain.ClonePlan(domain.QuickAddPlan{Options: opts}).Options
	return invoke(ctx, f, "PlanQuickAdd", Arguments{Text: text, QuickAdd: opts}, func(_ *Fixture) (domain.QuickAddPlan, error) {
		p := quickadd.Parse(text)
		p.Options = opts
		if p.Options.Checked == "" {
			p.Options.Checked = "skip"
		}
		if p.Options.Checked != "skip" && p.Options.Checked != "close" {
			return p, &domain.Error{Kind: domain.ErrInvalidInput, Message: "checked must be skip or close"}
		}
		p.Options.AddToBoard = p.Options.AddToBoard || p.Options.Status != nil
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
		return p, nil
	})
}

// RunQuickAdd simulates the public phases using fixture effects. It never
// invokes the real executor, paces, sleeps, or holds the fake mutex in callbacks.
func (f *Fake) RunQuickAdd(ctx context.Context, plan domain.QuickAddPlan, opts domain.QuickAddRunOptions) (result domain.QuickAddResult, err error) {
	idx, gate := f.beginCall("RunQuickAdd", Arguments{Text: plan.Source, QuickAdd: plan.Options})
	defer func() { f.finishCall(idx, err) }()
	var injected error
	if gate != nil {
		apply, e := gate.wait(ctx)
		if !apply {
			return result, normalise(e)
		}
		injected = e
	}
	p, e := f.PlanQuickAdd(ctx, plan.Source, plan.Options)
	if e != nil {
		return result, e
	}
	if len(p.Problems) > 0 {
		return result, &domain.Error{Kind: domain.ErrInvalidInput, Message: fmt.Sprintf("line %d: %s", p.Problems[0].Line, p.Problems[0].Message)}
	}
	result.Items = make([]domain.QuickAddItemResult, len(p.Items))
	for j, item := range p.Items {
		outcome := domain.QuickAddOutcome("unattempted")
		if item.Checked && p.Options.Checked == "skip" {
			outcome = "skipped"
		}
		result.Items[j] = domain.QuickAddItemResult{Item: item, Result: outcome}
	}
	defer func() {
		if err == nil && injected != nil {
			err = injected
		}
		result.Counts = domain.QuickAddCounts{}
		for _, r := range result.Items {
			switch r.Result {
			case "created":
				result.Counts.Created++
			case "failed":
				result.Counts.Failed++
			case "uncertain":
				result.Counts.Uncertain++
			case "unattempted":
				result.Counts.Unattempted++
			case "skipped":
				result.Counts.Skipped++
			case "planned":
				result.Counts.Planned++
			}
		}
		if err != nil {
			de := normalise(err)
			if result.Counts.Created+result.Counts.Uncertain > 0 {
				issue := de.Issue
				for _, r := range result.Items {
					if r.Issue != nil {
						v := domain.CloneIssue(*r.Issue)
						issue = &v
					}
				}
				err = &domain.Error{Kind: domain.ErrPartial, Message: "quick add stopped: " + de.Message, Hint: "check created issues; send only the remaining source", Cause: de.Kind, Uncertain: de.Uncertain, Completed: []string{}, Issue: issue, Err: de}
			} else {
				err = de
			}
		}
	}()
	emit := func(index int, phase string) error {
		if opts.OnProgress == nil {
			return nil
		}
		snapshot := domain.CloneQuickAddResult(domain.QuickAddResult{Items: []domain.QuickAddItemResult{result.Items[index]}}).Items[0]
		progress := domain.QuickAddProgress{Index: index, Phase: phase}
		if snapshot.Issue != nil {
			progress.Number = snapshot.Issue.Number
		}
		if phase == "settled" {
			progress.Outcome = snapshot.Result
			progress.Err = domain.CloneError(snapshot.Error)
			progress.Item = &snapshot
		}
		if e := opts.OnProgress(progress); e != nil {
			return &domain.Error{Kind: domain.ErrConfig, Message: "quick-add progress could not be saved", Err: e}
		}
		return nil
	}
	for j := range result.Items {
		r := &result.Items[j]
		if ctx.Err() != nil {
			return result, normalise(ctx.Err())
		}
		if opts.StopRequested != nil && opts.StopRequested() {
			return result, normalise(context.Canceled)
		}
		if r.Result == "skipped" {
			if e := emit(j, "settled"); e != nil {
				return result, e
			}
			continue
		}
		if opts.DryRun {
			r.Result = "planned"
			if e := emit(j, "settled"); e != nil {
				return result, e
			}
			continue
		}
		itemIdx, itemGate := f.beginCall("RunQuickAdd.item", Arguments{Number: j, Index: j, Text: r.Item.Source})
		var itemErr, itemInjected error
		if itemGate != nil {
			apply, e := itemGate.wait(ctx)
			if !apply {
				itemErr = normalise(e)
			} else {
				itemInjected = e
			}
		}
		if itemErr == nil {
			itemErr = emit(j, "creating")
		}
		if itemErr == nil {
			change, e := f.CreateIssue(ctx, domain.NewIssue{Title: r.Item.Title, Body: r.Item.Body, Assignees: p.Options.Assignees, Labels: p.Options.Labels})
			if e != nil {
				itemErr = e
				if normalise(e).Uncertain {
					r.Result = "uncertain"
				} else {
					r.Result = "failed"
				}
			} else {
				r.Result = "created"
				i := change.Issue
				r.Issue = &i
			}
		}
		if itemErr == nil && r.Item.Checked {
			itemErr = emit(j, "closing")
			if itemErr == nil {
				change, e := f.Close(ctx, r.Issue.Number, domain.CloseOptions{Reason: "completed"})
				itemErr = e
				if e == nil {
					i := change.Issue
					r.Issue = &i
					r.Closed = i.State == "closed"
				}
			}
		} else if itemErr == nil && p.Options.AddToBoard {
			itemErr = emit(j, "placing")
			if itemErr == nil {
				change, e := f.BoardAdd(ctx, r.Issue.Number, p.Options.Status)
				itemErr = e
				if e == nil {
					i := change.Issue
					r.Issue = &i
					r.OnBoard = i.Board != nil
				}
			}
		}
		if itemErr == nil && itemInjected != nil {
			itemErr = itemInjected
		}
		if itemErr != nil {
			r.Error = normalise(itemErr)
			if r.Result == "unattempted" {
				r.Result = "failed"
			}
		}
		f.finishCall(itemIdx, itemErr)
		if e := emit(j, "settled"); e != nil {
			return result, e
		}
		if itemErr != nil {
			return result, itemErr
		}
	}
	return result, nil
}
