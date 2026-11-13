package service

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pkieltyka/todos/internal/domain"
)

type quickClock struct {
	mu       sync.Mutex
	at       time.Time
	waits    []time.Duration
	waitHook func(context.Context, time.Duration) error
}

func (c *quickClock) Now() time.Time          { c.mu.Lock(); defer c.mu.Unlock(); return c.at }
func (c *quickClock) advance(d time.Duration) { c.mu.Lock(); defer c.mu.Unlock(); c.at = c.at.Add(d) }
func (c *quickClock) Wait(ctx context.Context, d time.Duration) error {
	c.mu.Lock()
	c.waits = append(c.waits, d)
	hook := c.waitHook
	c.mu.Unlock()
	if hook != nil {
		return hook(ctx, d)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	c.advance(d)
	return nil
}

type quickWrite struct {
	name       string
	input      domain.CreateInput
	start, end time.Time
}
type quickBackend struct {
	*fakeBackend
	clock             *quickClock
	mu                sync.Mutex
	issues            map[int]domain.IssueDetail
	placements        map[domain.NodeID]domain.Placement
	archived          map[domain.NodeID]bool
	writes            []quickWrite
	counts            map[string]int
	failures          map[string]*domain.Error
	ignoreAssignments bool
	defaultStatus     bool
	blocked           chan struct{}
	started           chan struct{}
}

func quickFixture() (*core, *quickBackend, *quickClock) {
	clock := &quickClock{at: time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)}
	b := &quickBackend{fakeBackend: backendFixture(), clock: clock, issues: map[int]domain.IssueDetail{}, placements: map[domain.NodeID]domain.Placement{}, archived: map[domain.NodeID]bool{}, counts: map[string]int{}, failures: map[string]*domain.Error{}}
	b.users = []domain.User{{NodeID: "U1", Login: "test"}}
	b.labels = []domain.Label{{NodeID: "L1", Name: "bug"}}
	s := New(b, &fakeSettings{}, Options{ResolveRepository: func(context.Context) (domain.RepoRef, error) { return b.repo.Ref, nil }, Now: clock.Now, Wait: clock.Wait}).(*core)
	return s, b, clock
}
func (b *quickBackend) mutate(ctx context.Context, name string, in domain.CreateInput, effect func()) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	b.mu.Lock()
	b.counts[name]++
	index := b.counts[name]
	start := b.clock.Now()
	b.writes = append(b.writes, quickWrite{name: name, input: in, start: start})
	slot := len(b.writes) - 1
	failure := domain.CloneError(b.failures[fmt.Sprintf("%s.%d", name, index)])
	started, blocked := b.started, b.blocked
	b.mu.Unlock()
	if name == "CreateIssue" && blocked != nil {
		if started != nil {
			close(started)
		}
		select {
		case <-ctx.Done():
			failure = &domain.Error{Kind: domain.ErrNetwork, Message: "operation interrupted", Uncertain: true, Err: ctx.Err()}
		case <-blocked:
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if failure == nil {
		effect()
	}
	b.clock.advance(175 * time.Millisecond)
	b.writes[slot].end = b.clock.Now()
	if failure != nil {
		return failure
	}
	return nil
}
func (b *quickBackend) CreateIssue(ctx context.Context, in domain.CreateInput) (domain.Issue, error) {
	var i domain.Issue
	e := b.mutate(ctx, "CreateIssue", in, func() {
		n := len(b.issues) + 1
		i = domain.Issue{NodeID: domain.NodeID(fmt.Sprintf("I%d", n)), Number: n, Title: in.Title, State: "open", Assignees: []string{}, AssigneeIDs: map[string]domain.NodeID{}, Labels: []domain.Label{}, URL: fmt.Sprintf("https://github.com/test/repo/issues/%d", n)}
		if !b.ignoreAssignments {
			for _, id := range in.AssigneeIDs {
				for _, u := range b.users {
					if u.NodeID == id {
						i.Assignees = append(i.Assignees, u.Login)
						i.AssigneeIDs[u.Login] = id
					}
				}
			}
		}
		b.issues[n] = domain.IssueDetail{Issue: domain.CloneIssue(i), Body: in.Body}
	})
	return i, e
}
func (b *quickBackend) Issue(ctx context.Context, _ domain.RepoRef, n int, selected domain.NodeID) (domain.IssueDetail, error) {
	if ctx.Err() != nil {
		return domain.IssueDetail{}, ctx.Err()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.clock.advance(50 * time.Millisecond)
	d, ok := b.issues[n]
	if !ok {
		return d, &domain.Error{Kind: domain.ErrNotFound, Message: "missing"}
	}
	d.Issue = domain.CloneIssue(d.Issue)
	if p, ok := b.placements[d.Issue.NodeID]; ok && selected != "" && !b.archived[d.Issue.NodeID] {
		d.Issue.Board = &p
	}
	return d, nil
}
func (b *quickBackend) CloseIssue(ctx context.Context, id domain.NodeID, reason string) (domain.Issue, error) {
	var i domain.Issue
	e := b.mutate(ctx, "CloseIssue", domain.CreateInput{}, func() {
		for n, d := range b.issues {
			if d.Issue.NodeID == id {
				d.Issue.State = "closed"
				r := domain.StateReason(reason)
				d.Issue.StateReason = &r
				b.issues[n] = d
				i = domain.CloneIssue(d.Issue)
			}
		}
	})
	return i, e
}
func (b *quickBackend) IssuePlacements(ctx context.Context, id domain.NodeID) ([]domain.ProjectItem, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.clock.advance(50 * time.Millisecond)
	out := []domain.ProjectItem{}
	if p, ok := b.placements[id]; ok {
		out = append(out, domain.ProjectItem{ID: p.ItemID, ProjectID: "P1", Archived: b.archived[id], Placement: p})
	}
	return out, nil
}
func (b *quickBackend) AddProjectItem(ctx context.Context, project, id domain.NodeID) (domain.Placement, error) {
	var p domain.Placement
	e := b.mutate(ctx, "AddProjectItem", domain.CreateInput{}, func() {
		p = domain.Placement{ItemID: domain.NodeID("ITEM_" + string(id))}
		if b.defaultStatus {
			p.Status = str("Done")
		}
		b.placements[id] = p
	})
	return p, e
}
func (b *quickBackend) SetProjectStatus(ctx context.Context, pid, item, field domain.NodeID, option string) error {
	return b.mutate(ctx, "SetProjectStatus", domain.CreateInput{}, func() {
		for id, p := range b.placements {
			if p.ItemID == item {
				name := "Todo"
				if option == "done" {
					name = "Done"
				}
				p.Status = &name
				p.OptionID = option
				b.placements[id] = p
			}
		}
	})
}
func (b *quickBackend) ClearProjectStatus(ctx context.Context, pid, item, field domain.NodeID) error {
	return b.mutate(ctx, "ClearProjectStatus", domain.CreateInput{}, func() {
		for id, p := range b.placements {
			if p.ItemID == item {
				p.Status = nil
				p.OptionID = ""
				b.placements[id] = p
			}
		}
	})
}
func (b *quickBackend) UnarchiveProjectItem(ctx context.Context, pid, item domain.NodeID) (domain.Placement, error) {
	var p domain.Placement
	e := b.mutate(ctx, "UnarchiveProjectItem", domain.CreateInput{}, func() {
		for id, v := range b.placements {
			if v.ItemID == item {
				b.archived[id] = false
				p = v
			}
		}
	})
	return p, e
}
func (b *quickBackend) AddComment(ctx context.Context, id domain.NodeID, body string) (domain.CommentResult, error) {
	var result domain.CommentResult
	e := b.mutate(ctx, "AddComment", domain.CreateInput{}, func() {
		for n, d := range b.issues {
			if d.Issue.NodeID == id {
				d.Issue.CommentCount++
				b.issues[n] = d
				result = domain.CommentResult{Issue: domain.CloneIssue(d.Issue), Comment: domain.Comment{Body: body}}
			}
		}
	})
	return result, e
}
func quickPlan(source string, opts domain.QuickAddOptions) domain.QuickAddPlan {
	return domain.QuickAddPlan{Source: source, Options: opts}
}
func runQuick(t *testing.T, s *core, text string, opts domain.QuickAddOptions, run domain.QuickAddRunOptions) domain.QuickAddResult {
	t.Helper()
	r, e := s.RunQuickAdd(context.Background(), quickPlan(text, opts), run)
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func TestQuickAdd_PlanProblemsAndMetadata(t *testing.T) {
	s, b, _ := quickFixture()
	p, e := s.PlanQuickAdd(context.Background(), "-\n bad", domain.QuickAddOptions{})
	if e != nil || len(p.Problems) != 2 || len(b.calls) > 0 {
		t.Fatal(p, e, b.calls)
	}
	p, e = s.PlanQuickAdd(context.Background(), "task", domain.QuickAddOptions{Labels: []string{"unknown"}})
	if e != nil || len(p.Problems) != 1 || p.Problems[0].Line != 1 || len(b.writes) > 0 {
		t.Fatal(p, e)
	}
	b.fail["Repository"] = &domain.Error{Kind: domain.ErrAuth, Message: "login"}
	s.session.Valid = false
	_, e = s.PlanQuickAdd(context.Background(), "task", domain.QuickAddOptions{})
	assertKind(t, e, domain.ErrAuth)
}
func TestQuickAdd_PlanCanonicalCountsCheapReplans(t *testing.T) {
	s, b, _ := quickFixture()
	ctx := context.Background()
	opts := domain.QuickAddOptions{Assignees: []string{"@me", "TEST"}, Labels: []string{"bug", "bug"}, Status: str("todo"), Checked: "close"}
	p, e := s.PlanQuickAdd(ctx, "open\n- [X] closed", opts)
	if e != nil || p.ToCreate != 2 || p.WriteCount != 5 || !p.Options.AddToBoard || *p.Options.Status != "Todo" || !reflect.DeepEqual(p.Options.Assignees, []string{"test"}) || !reflect.DeepEqual(p.Options.Labels, []string{"bug"}) {
		t.Fatal(p, e)
	}
	calls := len(b.calls)
	p.Options.Assignees[0] = "mutated"
	_, e = s.PlanQuickAdd(ctx, "open\n- [X] closed", opts)
	if e != nil || len(b.calls) != calls {
		t.Fatal(e, b.calls)
	}
	_, e = s.PlanQuickAdd(ctx, "task", domain.QuickAddOptions{Checked: "bad"})
	assertKind(t, e, domain.ErrInvalidInput)
}
func TestQuickAdd_DryRunPlannedCounts(t *testing.T) {
	s, b, _ := quickFixture()
	r := runQuick(t, s, "open\n- [x] skipped\nlast", domain.QuickAddOptions{}, domain.QuickAddRunOptions{DryRun: true})
	if r.Counts.Planned != 2 || r.Counts.Skipped != 1 || r.Counts.Unattempted != 0 || len(b.writes) > 0 {
		t.Fatal(r, b.writes)
	}
}
func TestQuickAdd_CheckedSkipOrCloseNoBoard(t *testing.T) {
	for _, mode := range []domain.CheckedMode{"skip", "close"} {
		s, b, _ := quickFixture()
		phases := []string{}
		r := runQuick(t, s, "open\n- [x] done", domain.QuickAddOptions{Checked: mode, Status: str("Todo")}, domain.QuickAddRunOptions{OnProgress: func(p domain.QuickAddProgress) error {
			phases = append(phases, fmt.Sprintf("%d:%s", p.Index, p.Phase))
			return nil
		}})
		if r.Counts.Created != map[domain.CheckedMode]int{"skip": 1, "close": 2}[mode] || !r.Items[0].OnBoard || r.Items[1].OnBoard || r.Items[1].Closed != (mode == "close") {
			t.Fatal(r)
		}
		for _, w := range b.writes {
			if w.name == "AddProjectItem" && b.counts["AddProjectItem"] != 1 {
				t.Fatal(b.writes)
			}
		}
		want := []string{"0:creating", "0:placing", "0:settled", "1:settled"}
		if mode == "close" {
			want = []string{"0:creating", "0:placing", "0:settled", "1:creating", "1:closing", "1:settled"}
		}
		if !reflect.DeepEqual(phases, want) {
			t.Fatal(phases)
		}
	}
}
func TestQuickAdd_AddToBoardNoStatus(t *testing.T) {
	s, b, _ := quickFixture()
	b.defaultStatus = true
	r := runQuick(t, s, "task", domain.QuickAddOptions{AddToBoard: true}, domain.QuickAddRunOptions{})
	if !r.Items[0].OnBoard || r.Items[0].Issue.Board.Status != nil || b.counts["ClearProjectStatus"] != 1 {
		t.Fatal(r, b.writes)
	}
}
func TestQuickAdd_PerWriteOneSecondRunLocal(t *testing.T) {
	s, b, clock := quickFixture()
	runQuick(t, s, "one\n- [x] two\nthree", domain.QuickAddOptions{Checked: "close", Status: str("Todo")}, domain.QuickAddRunOptions{})
	if len(b.writes) != 8 || len(clock.waits) != 7 {
		t.Fatal(b.writes, clock.waits)
	}
	for j, w := range b.writes {
		if j > 0 && w.start.Sub(b.writes[j-1].end) < time.Second {
			t.Fatal("not end-to-start paced", b.writes)
		}
	}
	before := len(clock.waits)
	runQuick(t, s, "new run", domain.QuickAddOptions{}, domain.QuickAddRunOptions{})
	if len(clock.waits) != before {
		t.Fatal("pacer leaked across runs")
	}
}
func TestQuickAdd_OrdinaryWritesUnpaced(t *testing.T) {
	s, b, clock := quickFixture()
	ctx := context.Background()
	_, e := s.CreateIssue(ctx, domain.NewIssue{Title: "ordinary"})
	if e != nil {
		t.Fatal(e)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	clock.waitHook = func(ctx context.Context, d time.Duration) error {
		close(entered)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-release:
			clock.advance(d)
			return nil
		}
	}
	done := make(chan error, 1)
	go func() {
		_, e := s.RunQuickAdd(ctx, quickPlan("one\ntwo", domain.QuickAddOptions{}), domain.QuickAddRunOptions{})
		done <- e
	}()
	<-entered
	_, e = s.Comment(ctx, 1, "ordinary while run paused")
	if e != nil {
		t.Fatal(e)
	}
	close(release)
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	if b.counts["AddComment"] != 1 {
		t.Fatal(b.writes)
	}
}
func TestQuickAdd_StopFinishesItem(t *testing.T) {
	s, b, _ := quickFixture()
	stop := false
	r, e := s.RunQuickAdd(context.Background(), quickPlan("one\ntwo", domain.QuickAddOptions{Status: str("Todo")}), domain.QuickAddRunOptions{StopRequested: func() bool { return stop }, OnProgress: func(p domain.QuickAddProgress) error {
		if p.Phase == "creating" {
			stop = true
		}
		return nil
	}})
	assertKind(t, e, domain.ErrPartial)
	if !errors.Is(e, context.Canceled) || r.Counts.Created != 1 || r.Counts.Unattempted != 1 || !r.Items[0].OnBoard || b.counts["SetProjectStatus"] != 1 {
		t.Fatal(r, e, b.writes)
	}
}
func TestQuickAdd_ContextInterruptAccountsAll(t *testing.T) {
	for _, where := range []string{"before-item", "in-flight", "pacing"} {
		t.Run(where, func(t *testing.T) {
			s, b, c := quickFixture()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			run := domain.QuickAddRunOptions{}
			if where == "before-item" {
				run.OnProgress = func(p domain.QuickAddProgress) error {
					if p.Phase == "settled" {
						cancel()
					}
					return nil
				}
			}
			if where == "pacing" {
				c.waitHook = func(context.Context, time.Duration) error { cancel(); return ctx.Err() }
			}
			if where == "in-flight" {
				b.started = make(chan struct{})
				b.blocked = make(chan struct{})
				go func() { <-b.started; cancel() }()
			}
			r, e := s.RunQuickAdd(ctx, quickPlan("one\ntwo\nthree", domain.QuickAddOptions{}), run)
			if !errors.Is(e, context.Canceled) || len(r.Items) != 3 {
				t.Fatal(r, e)
			}
			if where == "in-flight" {
				if r.Counts.Uncertain != 1 || r.Counts.Unattempted != 2 {
					t.Fatal(r)
				}
			} else if where == "pacing" {
				if r.Counts.Created != 1 || r.Counts.Failed != 1 || r.Counts.Unattempted != 1 {
					t.Fatal(r)
				}
			} else if r.Counts.Created != 1 || r.Counts.Unattempted != 2 {
				t.Fatal(r)
			}
		})
	}
}
func TestQuickAdd_OnProgressSaveFailureStops(t *testing.T) {
	for _, phase := range []string{"creating", "closing", "placing", "settled"} {
		t.Run(phase, func(t *testing.T) {
			s, b, _ := quickFixture()
			text := "one\ntwo"
			opts := domain.QuickAddOptions{AddToBoard: true}
			if phase == "closing" {
				text = "- [x] one\ntwo"
				opts.Checked = "close"
			}
			r, e := s.RunQuickAdd(context.Background(), quickPlan(text, opts), domain.QuickAddRunOptions{OnProgress: func(p domain.QuickAddProgress) error {
				if p.Phase == phase {
					return errors.New("disk full")
				}
				return nil
			}})
			if phase == "creating" {
				assertKind(t, e, domain.ErrConfig)
				if len(b.writes) != 0 || r.Counts.Failed != 1 {
					t.Fatal(r, b.writes)
				}
			} else {
				de := assertKind(t, e, domain.ErrPartial)
				if de.Cause != domain.ErrConfig || r.Counts.Created != 1 {
					t.Fatal(r, e)
				}
			}
			if r.Counts.Unattempted != 1 {
				t.Fatal(r)
			}
		})
	}
}
func TestQuickAdd_FirstUnknownPartial7(t *testing.T) {
	s, b, _ := quickFixture()
	b.failures["CreateIssue.1"] = &domain.Error{Kind: domain.ErrTimeout, Message: "timeout", Uncertain: true}
	r, e := s.RunQuickAdd(context.Background(), quickPlan("one\ntwo", domain.QuickAddOptions{}), domain.QuickAddRunOptions{})
	de := assertKind(t, e, domain.ErrPartial)
	if de.Cause != domain.ErrTimeout || !de.Uncertain || r.Counts.Uncertain != 1 || r.Counts.Unattempted != 1 || r.Items[0].Issue != nil || b.counts["CreateIssue"] != 1 {
		t.Fatal(r, e)
	}
}
func TestQuickAdd_PostCreateFailureStaysCreated(t *testing.T) {
	for _, operation := range []string{"CloseIssue", "AddProjectItem", "SetProjectStatus"} {
		for _, uncertain := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", operation, uncertain), func(t *testing.T) {
				s, b, _ := quickFixture()
				text := "one\ntwo"
				opts := domain.QuickAddOptions{Status: str("Todo")}
				if operation == "CloseIssue" {
					text = "- [x] one\ntwo"
					opts.Checked = "close"
				}
				b.failures[operation+".1"] = &domain.Error{Kind: domain.ErrTimeout, Message: "timeout", Uncertain: uncertain}
				r, e := s.RunQuickAdd(context.Background(), quickPlan(text, opts), domain.QuickAddRunOptions{})
				de := assertKind(t, e, domain.ErrPartial)
				row := r.Items[0]
				if r.Counts.Created != 1 || r.Counts.Unattempted != 1 || row.Result != "created" || row.Issue == nil || row.Error == nil || row.Error.Uncertain != uncertain || de.Uncertain != uncertain || row.Closed {
					t.Fatal(r, e)
				}
				if row.OnBoard != (operation == "SetProjectStatus") {
					t.Fatal(r)
				}
			})
		}
	}
}
func TestQuickAdd_FirstRejectedUsesCause(t *testing.T) {
	s, b, _ := quickFixture()
	b.failures["CreateIssue.1"] = &domain.Error{Kind: domain.ErrForbidden, Message: "denied"}
	r, e := s.RunQuickAdd(context.Background(), quickPlan("one\ntwo", domain.QuickAddOptions{}), domain.QuickAddRunOptions{})
	assertKind(t, e, domain.ErrForbidden)
	if r.Counts.Failed != 1 || r.Counts.Unattempted != 1 || len(b.writes) != 1 {
		t.Fatal(r)
	}
}
func TestQuickAdd_AssignmentPartialKeepsKnownCreate(t *testing.T) {
	s, b, _ := quickFixture()
	b.ignoreAssignments = true
	r, e := s.RunQuickAdd(context.Background(), quickPlan("one\ntwo", domain.QuickAddOptions{Assignees: []string{"test"}}), domain.QuickAddRunOptions{})
	de := assertKind(t, e, domain.ErrPartial)
	if de.Cause != domain.ErrForbidden || r.Counts.Created != 1 || r.Items[0].Issue == nil || r.Items[0].Error.Kind != domain.ErrPartial || r.Counts.Unattempted != 1 {
		t.Fatal(r, e)
	}
}
func TestQuickAdd_ReparseAndWholeBatchValidation(t *testing.T) {
	s, b, _ := quickFixture()
	p, _ := s.PlanQuickAdd(context.Background(), "one\ntwo", domain.QuickAddOptions{})
	p.Items[0].Title = "tampered"
	r, e := s.RunQuickAdd(context.Background(), p, domain.QuickAddRunOptions{})
	if e != nil || r.Items[0].Item.Title != "one" || r.Items[1].Item.Title != "two" || r.Items[0].Issue.Number >= r.Items[1].Issue.Number {
		t.Fatal(r, e)
	}
	b.writes = nil
	p.Source = "one\n-\nthree"
	_, e = s.RunQuickAdd(context.Background(), p, domain.QuickAddRunOptions{})
	assertKind(t, e, domain.ErrInvalidInput)
	if len(b.writes) > 0 {
		t.Fatal(b.writes)
	}
}
func TestQuickAdd_ProgressOwnedAndSourceVerbatim(t *testing.T) {
	s, _, _ := quickFixture()
	source := "- one\r\n  body\r\n\r\n- two\r\n"
	r := runQuick(t, s, source, domain.QuickAddOptions{}, domain.QuickAddRunOptions{OnProgress: func(p domain.QuickAddProgress) error {
		if p.Item != nil {
			p.Item.Item.Source = "bad"
			p.Item.Issue.Title = "bad"
		}
		return nil
	}})
	if r.Items[0].Item.Source+r.Items[1].Item.Source != source || r.Items[0].Issue.Title != "one" || !strings.Contains(r.Items[0].Item.Body, "body") {
		t.Fatal(r)
	}
}
func TestQuickAdd_StopBeforeFirstPreservesAll(t *testing.T) {
	s, b, _ := quickFixture()
	r, e := s.RunQuickAdd(context.Background(), quickPlan("one\ntwo", domain.QuickAddOptions{}), domain.QuickAddRunOptions{StopRequested: func() bool { return true }})
	assertKind(t, e, domain.ErrNetwork)
	if !errors.Is(e, context.Canceled) || r.Counts.Unattempted != 2 || len(b.writes) > 0 {
		t.Fatal(r, e)
	}
}
func TestQuickAdd_PacerWaitChecksElapsedAndCancel(t *testing.T) {
	s, b, c := quickFixture()
	short := true
	c.waitHook = func(ctx context.Context, d time.Duration) error {
		if short {
			short = false
			c.advance(d / 2)
		} else {
			c.advance(d)
		}
		return nil
	}
	runQuick(t, s, "one\ntwo", domain.QuickAddOptions{}, domain.QuickAddRunOptions{})
	if len(c.waits) != 2 || b.writes[1].start.Sub(b.writes[0].end) < time.Second {
		t.Fatal(c.waits, b.writes)
	}
}

func TestQuickAdd_PacesRestoreAndClear(t *testing.T) {
	for _, restore := range []bool{false, true} {
		s, b, _ := quickFixture()
		b.defaultStatus = true
		opts := domain.QuickAddOptions{AddToBoard: true}
		run := domain.QuickAddRunOptions{}
		if restore {
			opts.Status = str("Todo")
			run.OnProgress = func(p domain.QuickAddProgress) error {
				if p.Phase == "placing" {
					b.mu.Lock()
					defer b.mu.Unlock()
					b.placements["I1"] = domain.Placement{ItemID: "ARCHIVED", Status: str("Done")}
					b.archived["I1"] = true
				}
				return nil
			}
		}
		r := runQuick(t, s, "one", opts, run)
		if !r.Items[0].OnBoard {
			t.Fatal(r)
		}
		want := []string{"CreateIssue", "AddProjectItem", "ClearProjectStatus"}
		if restore {
			want = []string{"CreateIssue", "UnarchiveProjectItem", "SetProjectStatus"}
		}
		actual := make([]string, 0, len(b.writes))
		for _, w := range b.writes {
			actual = append(actual, w.name)
		}
		if !reflect.DeepEqual(actual, want) {
			t.Fatalf("restore=%t: want writes %v, got %v", restore, want, actual)
		}
		for j, w := range b.writes {
			if j > 0 && w.start.Sub(b.writes[j-1].end) < time.Second {
				t.Fatal("unpaced restore/clear", b.writes)
			}
		}
	}
}
func TestQuickAdd_LongReadTimeNeedsNoExtraWait(t *testing.T) {
	s, _, clock := quickFixture()
	runQuick(t, s, "- [x] one", domain.QuickAddOptions{Checked: "close"}, domain.QuickAddRunOptions{OnProgress: func(p domain.QuickAddProgress) error {
		if p.Phase == "closing" {
			clock.advance(2 * time.Second)
		}
		return nil
	}})
	if len(clock.waits) > 0 {
		t.Fatal("already elapsed gap waited", clock.waits)
	}
}
func TestQuickAdd_SettledSaveFailurePreservesUnknownOutcome(t *testing.T) {
	s, b, _ := quickFixture()
	b.failures["CreateIssue.1"] = &domain.Error{Kind: domain.ErrTimeout, Message: "timeout", Uncertain: true}
	r, e := s.RunQuickAdd(context.Background(), quickPlan("one\ntwo", domain.QuickAddOptions{}), domain.QuickAddRunOptions{OnProgress: func(p domain.QuickAddProgress) error {
		if p.Phase == "settled" {
			return errors.New("disk full")
		}
		return nil
	}})
	de := assertKind(t, e, domain.ErrPartial)
	if de.Cause != domain.ErrConfig || !de.Uncertain || r.Counts.Uncertain != 1 || r.Items[0].Error.Kind != domain.ErrTimeout || r.Counts.Unattempted != 1 {
		t.Fatal(r, e)
	}
}
