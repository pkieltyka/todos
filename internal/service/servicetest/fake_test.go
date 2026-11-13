package servicetest_test

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
	"github.com/pkieltyka/todos/internal/service/servicetest"
)

func pointer[T any](v T) *T    { return &v }
func fresh() *servicetest.Fake { return servicetest.New(servicetest.DefaultFixture()) }
func wantKind(t *testing.T, err error, kind domain.ErrorKind) *domain.Error {
	t.Helper()
	var de *domain.Error
	if !errors.As(err, &de) || de.Kind != kind {
		t.Fatalf("error %v, want kind %s", err, kind)
	}
	return de
}
func await[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(5 * time.Second):
		t.Fatal("gate test did not complete")
		var zero T
		return zero
	}
}
func started(t *testing.T, g *servicetest.Gate) { t.Helper(); await(t, g.Started) }

func TestFake_GateReleaseFailureAndCancel(t *testing.T) {
	for _, mode := range []string{"release", "fail", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			f := fresh()
			g := f.Hold("GetIssue", 6)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := f.GetIssue(ctx, 6); done <- err }()
			started(t, g)
			log := f.Calls()
			if len(log) != 1 || log[0].EndOrder != 0 {
				t.Fatal("gate did not hold call")
			}
			switch mode {
			case "release":
				g.Release()
			case "fail":
				g.Fail(&domain.Error{Kind: domain.ErrForbidden, Message: "denied"})
			case "cancel":
				cancel()
			}
			err := await(t, done)
			switch mode {
			case "release":
				if err != nil {
					t.Fatal(err)
				}
			case "fail":
				wantKind(t, err, domain.ErrForbidden)
			case "cancel":
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			}
			if f.Calls()[0].EndOrder <= f.Calls()[0].StartOrder {
				t.Fatal("missing completion order")
			}
		})
	}
	t.Run("FIFO and no mutex across waits", func(t *testing.T) {
		f := fresh()
		a, b := f.Hold("GetIssue", 6), f.Hold("GetIssue", 6)
		one, two := make(chan error, 1), make(chan error, 1)
		go func() { _, err := f.GetIssue(context.Background(), 6); one <- err }()
		started(t, a)
		go func() { _, err := f.GetIssue(context.Background(), 6); two <- err }()
		started(t, b)
		b.Release()
		if err := await(t, two); err != nil {
			t.Fatal(err)
		}
		a.Release()
		if err := await(t, one); err != nil {
			t.Fatal(err)
		}
		log := f.Calls()
		if !(log[0].StartOrder < log[1].StartOrder && log[1].EndOrder < log[0].EndOrder) {
			t.Fatal(log)
		}
	})
}
func TestFake_ApplyThenFailChangesFixture(t *testing.T) {
	f := fresh()
	g := f.Hold("Close", 6)
	done := make(chan error, 1)
	go func() {
		_, err := f.Close(context.Background(), 6, domain.CloseOptions{Reason: "completed"})
		done <- err
	}()
	started(t, g)
	g.ApplyThenFail(&domain.Error{Kind: domain.ErrTimeout, Message: "unknown outcome", Uncertain: true, Err: context.DeadlineExceeded})
	err := await(t, done)
	if !wantKind(t, err, domain.ErrTimeout).Uncertain || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("lost uncertainty/cause")
	}
	i, err := f.GetIssue(context.Background(), 6)
	if err != nil || i.Issue.State != "closed" {
		t.Fatalf("write did not land: %+v %v", i, err)
	}
	// A definite failure does not apply the effect.
	f = fresh()
	g = f.Hold("Close", 6)
	g.Fail(&domain.Error{Kind: domain.ErrForbidden, Message: "no"})
	_, err = f.Close(context.Background(), 6, domain.CloseOptions{})
	wantKind(t, err, domain.ErrForbidden)
	i, _ = f.GetIssue(context.Background(), 6)
	if i.Issue.State != "open" {
		t.Fatal("rejected effect applied")
	}
}
func TestFake_ArgumentsAndCallOrderOwned(t *testing.T) {
	f := fresh()
	add := []string{"octocat"}
	g := f.Hold("UpdateAssignees", 6)
	done := make(chan error, 1)
	go func() { _, err := f.UpdateAssignees(context.Background(), 6, add, []string{"reviewer"}); done <- err }()
	started(t, g)
	add[0] = "changed"
	log := f.Calls()
	if log[0].Args.Add[0] != "octocat" || log[0].Args.Remove[0] != "reviewer" {
		t.Fatal(log)
	}
	log[0].Args.Add[0] = "tamper"
	g.Release()
	if err := await(t, done); err != nil {
		t.Fatal(err)
	}
	i, _ := f.GetIssue(context.Background(), 6)
	if !contains(i.Issue.Assignees, "octocat") || contains(i.Issue.Assignees, "changed") {
		t.Fatal("effect did not capture call inputs")
	}
	if f.Calls()[0].Args.Add[0] != "octocat" {
		t.Fatal("call log aliases result")
	}
}
func contains(v []string, s string) bool {
	for _, x := range v {
		if x == s {
			return true
		}
	}
	return false
}
func TestFake_MutateExternalEdit(t *testing.T) {
	f := fresh()
	before, _ := f.GetIssue(context.Background(), 6)
	f.Mutate(func(x *servicetest.Fixture) {
		x.Issues[0].Issue.Title = "Edited online"
		x.Issues[0].Body = "external body"
	})
	after, _ := f.GetIssue(context.Background(), 6)
	if after.Issue.Title != "Edited online" || after.Body != "external body" || before.Issue.Title == after.Issue.Title {
		t.Fatal("external edit not isolated/visible")
	}
	before.Issue.Labels[0].Name = "tamper"
	again, _ := f.GetIssue(context.Background(), 6)
	if again.Issue.Labels[0].Name == "tamper" {
		t.Fatal("read aliases fixture")
	}
}
func TestFake_RateSnapshot(t *testing.T) {
	f := fresh()
	input := domain.RateLimit{Known: true, Limit: 5000, Remaining: 1, Buckets: []domain.RateBucket{{Resource: "graphql", Remaining: 1}}}
	f.SetRateLimit(input)
	input.Buckets[0].Remaining = 100
	got := f.RateLimit()
	got.Buckets[0].Remaining = 999
	if f.RateLimit().Buckets[0].Remaining != 1 {
		t.Fatal("rate state aliases input/output")
	}
}
func TestFake_DefaultFixtureTimeAndGenIssues(t *testing.T) {
	a, b := servicetest.DefaultFixture(), servicetest.DefaultFixture()
	if !reflect.DeepEqual(a, b) {
		t.Fatal("default fixture nondeterministic")
	}
	if !a.BoardInfo.Project.UpdatedAt.Equal(time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)) {
		t.Fatal("wrong fixture epoch")
	}
	items := servicetest.GenIssues(1204)
	if len(items) != 1204 || items[0].Issue.Number != 1204 || items[1203].Issue.Number != 1 || !items[0].Issue.UpdatedAt.After(items[1].Issue.UpdatedAt) {
		t.Fatal("generator is not sorted/deterministic")
	}
	f := servicetest.New(a)
	a.Issues[0].Issue.Title = "mutated input"
	a.Labels[0].Name = "changed"
	got, _ := f.GetIssue(context.Background(), 6)
	if got.Issue.Title == "mutated input" {
		t.Fatal("constructor retains caller fixture")
	}
}
func TestReads_OnePageRawCursor(t *testing.T) {
	fixture := servicetest.DefaultFixture()
	fixture.Issues = servicetest.GenIssues(1204)
	f := servicetest.New(fixture)
	ctx := context.Background()
	first, err := f.ListIssues(ctx, domain.IssueFilter{}, domain.PageRequest{})
	if err != nil || len(first.Items) != 100 || first.Total != 1204 || first.NextCursor != "100" || !first.HasMore {
		t.Fatalf("first page: %+v %v", first, err)
	}
	next, err := f.ListIssues(ctx, domain.IssueFilter{}, domain.PageRequest{Cursor: first.NextCursor, Size: 50})
	if err != nil || len(next.Items) != 50 || next.Items[0].Number != 1104 || f.Calls()[1].Args.Page.Cursor != "100" {
		t.Fatal("cursor/page not preserved")
	}
	for _, request := range []domain.PageRequest{{Cursor: "bad"}, {Size: 101}, {Size: -1}} {
		_, err = f.ListIssues(ctx, domain.IssueFilter{}, request)
		wantKind(t, err, domain.ErrInvalidInput)
	}
	empty, err := f.ListIssues(ctx, domain.IssueFilter{Labels: []string{"missing"}}, domain.PageRequest{})
	if err != nil || empty.Items == nil || len(empty.Items) != 0 {
		t.Fatal("empty page must use []")
	}
}
func TestIssues_MineExpandsMe(t *testing.T) {
	f := fresh()
	p, err := f.ListIssues(context.Background(), domain.IssueFilter{State: "all", Assignee: "@me"}, domain.PageRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range p.Items {
		if !contains(i.Assignees, "pkieltyka") {
			t.Fatal(i)
		}
	}
	p, err = f.ListIssues(context.Background(), domain.IssueFilter{State: "all", Labels: []string{"bug", "sync"}}, domain.PageRequest{})
	if err != nil || len(p.Items) != 1 || p.Items[0].Number != 6 {
		t.Fatal("labels are not AND")
	}
}
func TestPRs_MineHasNoServerAuthorFilter(t *testing.T) {
	f := fresh()
	p, err := f.ListPRs(context.Background(), domain.PRFilter{State: "closed"}, domain.PageRequest{})
	if err != nil || len(p.Items) != 1 || p.Items[0].State != "closed" {
		t.Fatalf("closed included merged: %+v %v", p, err)
	}
	d, err := f.GetPR(context.Background(), 101)
	if err != nil || d.Body == "" || len(d.Closes) == 0 {
		t.Fatal(err)
	}
	d.Closes[0] = 999
	again, _ := f.GetPR(context.Background(), 101)
	if again.Closes[0] == 999 {
		t.Fatal("PR detail aliases fixture")
	}
}
func TestFake_AllReadMethods(t *testing.T) {
	f := fresh()
	ctx := context.Background()
	if s, err := f.Session(ctx); err != nil || !s.Repo.IssuesEnabled {
		t.Fatal(err)
	}
	if err := f.RefreshMetadata(ctx); err != nil {
		t.Fatal(err)
	}
	labels, err := f.Labels(ctx)
	if err != nil || len(labels) == 0 {
		t.Fatal(err)
	}
	labels[0].Name = "changed"
	again, _ := f.Labels(ctx)
	if again[0].Name == "changed" {
		t.Fatal("labels alias fixture")
	}
	if users, err := f.AssignableUsers(ctx); err != nil || len(users) < 2 {
		t.Fatal(err)
	}
	if p, err := f.BoardCandidates(ctx); err != nil || len(p.Projects) != 1 || p.Selected == nil {
		t.Fatal(err)
	}
	info, err := f.BoardInfo(ctx)
	if err != nil || len(info.Options) != 3 {
		t.Fatal(err)
	}
	info.Options[0].Name = "tamper"
	againInfo, _ := f.BoardInfo(ctx)
	if againInfo.Options[0].Name == "tamper" {
		t.Fatal("options alias fixture")
	}
	if _, err = f.UseBoard(ctx, info.Project.Ref); err != nil {
		t.Fatal(err)
	}
	if b, err := f.Board(ctx); err != nil || b.HiddenCount != 2 || b.Columns[0].Status != nil || b.VisibleCount != 3 {
		t.Fatalf("board: %+v %v", b, err)
	}
	for _, fn := range []func() error{func() error { _, e := f.GetIssue(ctx, 999); return e }, func() error { _, e := f.GetPR(ctx, 999); return e }, func() error { _, e := f.UseBoard(ctx, domain.ProjectRef{Number: 999}); return e }} {
		wantKind(t, fn(), domain.ErrNotFound)
	}
}
func TestFake_CommentsPagesAndOwnership(t *testing.T) {
	fixture := servicetest.DefaultFixture()
	fixture.IssueComments[6] = []domain.Comment{}
	for i := 500; i >= 0; i-- {
		fixture.IssueComments[6] = append(fixture.IssueComments[6], domain.Comment{ID: fmt.Sprint(i), CreatedAt: fixture.BoardInfo.Project.UpdatedAt.Add(time.Duration(i) * time.Second), Author: pointer("author")})
	}
	fixture.PRComments[101] = fixture.IssueComments[6]
	f := servicetest.New(fixture)
	for _, test := range []struct {
		n     int
		fetch func(context.Context, int, domain.PageRequest) (domain.Page[domain.Comment], error)
	}{{6, f.ListIssueComments}, {101, f.ListPRComments}} {
		p, err := test.fetch(context.Background(), test.n, domain.PageRequest{Size: 100})
		if err != nil || p.Total != 501 || len(p.Items) != 100 || p.Items[0].ID != "0" || !p.HasMore {
			t.Fatalf("comments: %+v %v", p, err)
		}
		*p.Items[0].Author = "changed"
		again, _ := test.fetch(context.Background(), test.n, domain.PageRequest{Size: 1})
		if *again.Items[0].Author == "changed" {
			t.Fatal("comments alias fixture")
		}
	}
}
func TestFake_BoardErrorVariants(t *testing.T) {
	for _, variant := range []string{"none", "several", "unusable", "scope"} {
		t.Run(variant, func(t *testing.T) {
			fixture := servicetest.DefaultFixture()
			f := servicetest.New(fixture)
			switch variant {
			case "none":
				f.Mutate(func(x *servicetest.Fixture) { x.BoardInfo = domain.BoardInfo{}; x.Projects = nil })
			case "several":
				f.Mutate(func(x *servicetest.Fixture) {
					x.BoardInfo = domain.BoardInfo{}
					p := x.Projects[0]
					p.Ref.Number = 2
					p.NodeID = "P2"
					x.Projects = append(x.Projects, p)
				})
			case "unusable":
				f.Mutate(func(x *servicetest.Fixture) { x.BoardInfo.StatusFieldID = "" })
			case "scope":
				f.Hold("BoardInfo", 0).Fail(&domain.Error{Kind: domain.ErrMissingScope, Message: "needs project"})
			}
			_, err := f.BoardInfo(context.Background())
			kind := domain.ErrNoBoard
			if variant == "scope" {
				kind = domain.ErrMissingScope
			}
			de := wantKind(t, err, kind)
			if variant == "several" && len(de.Candidates) != 2 || variant == "unusable" && (len(de.Candidates) != 1 || de.Candidates[0].Usable) {
				t.Fatal("bad no_board discriminator")
			}
		})
	}
}
func TestDoctor_NeverWrites(t *testing.T) {
	f := fresh()
	r, err := f.Doctor(context.Background())
	if err != nil || !r.OK || len(r.Checks) != 6 {
		t.Fatalf("doctor %+v %v", r, err)
	}
	if len(f.Calls()) != 1 || f.Calls()[0].Method != "Doctor" {
		t.Fatal("doctor called a write")
	}
}
func TestDoctor_FirstThreeDetermineVerdict(t *testing.T) {
	f := fresh()
	f.Mutate(func(x *servicetest.Fixture) { x.BoardInfo.Project.ViewerCanUpdate = false; x.ConfigCheck.OK = false })
	r, err := f.Doctor(context.Background())
	if err != nil || !r.OK || r.Checks[4].OK || r.Checks[5].OK {
		t.Fatal("board/config failed doctor exit")
	}
	f.Mutate(func(x *servicetest.Fixture) { x.Credentials.Source = ""; x.Session.Repo.IssuesEnabled = false })
	r, err = f.Doctor(context.Background())
	wantKind(t, err, domain.ErrAuth)
	if r.OK || len(r.Checks) != 6 {
		t.Fatal("did not retain full failed report")
	}
}
func TestFake_ConcurrentReadsAndExternalEdits(t *testing.T) {
	f := fresh()
	var wg sync.WaitGroup
	for n := 0; n < 8; n++ {
		wg.Go(func() {
			for i := 0; i < 20; i++ {
				_, _ = f.GetIssue(context.Background(), 6)
				f.SetRateLimit(f.RateLimit())
				f.Mutate(func(x *servicetest.Fixture) { x.Issues[0].Body = strings.Repeat("x", i) })
			}
		})
	}
	wg.Wait()
}

func TestFake_GateFailNilIsGenericInternal(t *testing.T) {
	f := fresh()
	gate := f.Hold("GetIssue", 6)
	gate.Fail(nil)
	_, err := f.GetIssue(context.Background(), 6)
	wantKind(t, err, domain.ErrInternal)
	if strings.Contains(err.Error(), "quick add") {
		t.Fatalf("unrelated stub error: %v", err)
	}
}
