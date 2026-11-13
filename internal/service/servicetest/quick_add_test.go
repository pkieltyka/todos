package servicetest_test

import (
	"context"
	"errors"
	"github.com/pkieltyka/todos/internal/domain"
	"reflect"
	"testing"
)

func TestFake_QuickAddPhasesAndItemGates(t *testing.T) {
	f := fresh()
	ctx := context.Background()
	p, err := f.PlanQuickAdd(ctx, "- one\n- [x] two\n- three", domain.QuickAddOptions{Checked: "close", Status: pointer("Todo")})
	if err != nil {
		t.Fatal(err)
	}
	g := f.Hold("RunQuickAdd.item", 1)
	done := make(chan error, 1)
	phases := []string{}
	var result domain.QuickAddResult
	go func() {
		var e error
		result, e = f.RunQuickAdd(ctx, p, domain.QuickAddRunOptions{OnProgress: func(p domain.QuickAddProgress) error { phases = append(phases, p.Phase); return nil }})
		done <- e
	}()
	started(t, g)
	g.Release()
	if err := await(t, done); err != nil {
		t.Fatal(err)
	}
	want := []string{"creating", "placing", "settled", "creating", "closing", "settled", "creating", "placing", "settled"}
	if !reflect.DeepEqual(phases, want) || result.Counts.Created != 3 || !result.Items[0].OnBoard || !result.Items[1].Closed || result.Items[1].OnBoard {
		t.Fatal(phases, result)
	}
	for _, c := range f.Calls() {
		if c.EndOrder <= c.StartOrder {
			t.Fatal(c)
		}
	}
}
func TestFake_QuickAddDryRunSkipAndReparse(t *testing.T) {
	f := fresh()
	p, e := f.PlanQuickAdd(context.Background(), "a\n- [x] b", domain.QuickAddOptions{})
	if e != nil || p.ToSkip != 1 || p.ToCreate != 1 || p.WriteCount != 1 {
		t.Fatal(p, e)
	}
	p.Items[0].Title = "tampered"
	r, e := f.RunQuickAdd(context.Background(), p, domain.QuickAddRunOptions{DryRun: true})
	if e != nil || r.Counts.Planned != 1 || r.Counts.Skipped != 1 || r.Items[0].Item.Title != "a" {
		t.Fatal(r, e)
	}
	for _, c := range f.Calls() {
		if c.Method == "CreateIssue" {
			t.Fatal("dry run wrote")
		}
	}
}
func TestFake_QuickAddStopFinishesItem(t *testing.T) {
	f := fresh()
	p, _ := f.PlanQuickAdd(context.Background(), "a\nb", domain.QuickAddOptions{AddToBoard: true})
	stop := false
	r, e := f.RunQuickAdd(context.Background(), p, domain.QuickAddRunOptions{StopRequested: func() bool { return stop }, OnProgress: func(p domain.QuickAddProgress) error {
		if p.Phase == "creating" {
			stop = true
		}
		return nil
	}})
	wantKind(t, e, domain.ErrPartial)
	if r.Counts.Created != 1 || r.Counts.Unattempted != 1 || !r.Items[0].OnBoard {
		t.Fatal(r)
	}
}
func TestFake_QuickAddFailuresAndCallbacks(t *testing.T) {
	for _, where := range []string{"create", "close", "place", "callback"} {
		t.Run(where, func(t *testing.T) {
			f := fresh()
			opts := domain.QuickAddOptions{AddToBoard: true}
			text := "a\nb"
			if where == "close" {
				text = "- [x] a\nb"
				opts.Checked = "close"
			}
			p, _ := f.PlanQuickAdd(context.Background(), text, opts)
			if where == "create" {
				g := f.Hold("CreateIssue", 0)
				g.ApplyThenFail(&domain.Error{Kind: domain.ErrTimeout, Message: "timeout", Uncertain: true})
			}
			if where == "close" {
				g := f.Hold("Close", 104)
				g.Fail(&domain.Error{Kind: domain.ErrForbidden, Message: "denied"})
			}
			if where == "place" {
				g := f.Hold("BoardAdd", 104)
				g.Fail(&domain.Error{Kind: domain.ErrForbidden, Message: "denied"})
			}
			run := domain.QuickAddRunOptions{}
			if where == "callback" {
				run.OnProgress = func(domain.QuickAddProgress) error { return errors.New("disk full") }
			}
			r, e := f.RunQuickAdd(context.Background(), p, run)
			if e == nil {
				t.Fatal("expected failure", r)
			}
			if r.Counts.Unattempted != 1 {
				t.Fatal(r)
			}
			if where == "create" {
				if r.Counts.Uncertain != 1 {
					t.Fatal(r)
				}
			} else if where != "callback" {
				if r.Counts.Created != 1 || r.Items[0].Issue == nil {
					t.Fatal(r)
				}
			}
		})
	}
}
func TestFake_QuickAddContextAndOwnedProgress(t *testing.T) {
	f := fresh()
	ctx, cancel := context.WithCancel(context.Background())
	p, _ := f.PlanQuickAdd(ctx, "a\nb", domain.QuickAddOptions{})
	r, e := f.RunQuickAdd(ctx, p, domain.QuickAddRunOptions{OnProgress: func(p domain.QuickAddProgress) error {
		if p.Phase == "settled" {
			p.Item.Item.Title = "changed"
			p.Item.Issue.Title = "changed"
			cancel()
		}
		return nil
	}})
	if !errors.Is(e, context.Canceled) || r.Items[0].Item.Title != "a" || r.Items[0].Issue.Title != "a" || r.Counts.Created != 1 || r.Counts.Unattempted != 1 {
		t.Fatal(r, e)
	}
}

func TestFake_QuickAddItemApplyThenFail(t *testing.T) {
	f := fresh()
	p, _ := f.PlanQuickAdd(context.Background(), "a\nb", domain.QuickAddOptions{})
	g := f.Hold("RunQuickAdd.item", 0)
	g.ApplyThenFail(&domain.Error{Kind: domain.ErrTimeout, Message: "timeout", Uncertain: true})
	r, e := f.RunQuickAdd(context.Background(), p, domain.QuickAddRunOptions{})
	wantKind(t, e, domain.ErrPartial)
	if r.Counts.Created != 1 || r.Counts.Unattempted != 1 || r.Items[0].Issue == nil || !r.Items[0].Error.Uncertain {
		t.Fatal(r, e)
	}
}
func TestFake_QuickAddGateCancel(t *testing.T) {
	f := fresh()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p, _ := f.PlanQuickAdd(ctx, "a\nb", domain.QuickAddOptions{})
	g := f.Hold("RunQuickAdd.item", 0)
	done := make(chan error, 1)
	go func() {
		r, e := f.RunQuickAdd(ctx, p, domain.QuickAddRunOptions{})
		if r.Counts.Created != 0 || r.Counts.Failed != 1 || r.Counts.Unattempted != 1 {
			done <- errors.New("bad accounting")
			return
		}
		done <- e
	}()
	started(t, g)
	cancel()
	if e := await(t, done); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}
