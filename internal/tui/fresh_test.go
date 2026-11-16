package tui

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/pkieltyka/todos/internal/domain"
	"github.com/pkieltyka/todos/internal/service"
	"github.com/pkieltyka/todos/internal/service/servicetest"
)

// A manual refresh is fresh (cache.md 10, tui.md 7.6): the r key and the
// recovery it starts mark every read of that refresh with
// domain.WithFreshSince and the time of the key press, the same time on later
// pages and comment pages, and nothing else is marked.

type freshCall struct {
	method string
	n      int
	fresh  bool
	since  time.Time
}

// freshLog records, for every read, whether its context was marked fresh.
type freshLog struct {
	mu    sync.Mutex
	calls []freshCall
}

func (l *freshLog) add(ctx context.Context, method string, n int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	since, fresh := domain.FreshSince(ctx)
	l.calls = append(l.calls, freshCall{method, n, fresh, since})
}

func (l *freshLog) mark() int { l.mu.Lock(); defer l.mu.Unlock(); return len(l.calls) }

func (l *freshLog) since(i int) []freshCall {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]freshCall(nil), l.calls[i:]...)
}

// freshService wraps the fake and records the fresh mark of every read.
type freshService struct {
	service.Service
	log *freshLog
}

func (s freshService) Session(ctx context.Context) (domain.Session, error) {
	s.log.add(ctx, "Session", 0)
	return s.Service.Session(ctx)
}
func (s freshService) RefreshMetadata(ctx context.Context) error {
	s.log.add(ctx, "RefreshMetadata", 0)
	return s.Service.RefreshMetadata(ctx)
}
func (s freshService) Labels(ctx context.Context) ([]domain.Label, error) {
	s.log.add(ctx, "Labels", 0)
	return s.Service.Labels(ctx)
}
func (s freshService) AssignableUsers(ctx context.Context) ([]domain.User, error) {
	s.log.add(ctx, "AssignableUsers", 0)
	return s.Service.AssignableUsers(ctx)
}
func (s freshService) ListIssues(ctx context.Context, f domain.IssueFilter, r domain.PageRequest) (domain.Page[domain.Issue], error) {
	s.log.add(ctx, "ListIssues", 0)
	return s.Service.ListIssues(ctx, f, r)
}
func (s freshService) GetIssue(ctx context.Context, n int) (domain.IssueDetail, error) {
	s.log.add(ctx, "GetIssue", n)
	return s.Service.GetIssue(ctx, n)
}
func (s freshService) ListIssueComments(ctx context.Context, n int, r domain.PageRequest) (domain.Page[domain.Comment], error) {
	s.log.add(ctx, "ListIssueComments", n)
	return s.Service.ListIssueComments(ctx, n, r)
}
func (s freshService) ListPRs(ctx context.Context, f domain.PRFilter, r domain.PageRequest) (domain.Page[domain.PullRequest], error) {
	s.log.add(ctx, "ListPRs", 0)
	return s.Service.ListPRs(ctx, f, r)
}
func (s freshService) GetPR(ctx context.Context, n int) (domain.PRDetail, error) {
	s.log.add(ctx, "GetPR", n)
	return s.Service.GetPR(ctx, n)
}
func (s freshService) ListPRComments(ctx context.Context, n int, r domain.PageRequest) (domain.Page[domain.Comment], error) {
	s.log.add(ctx, "ListPRComments", n)
	return s.Service.ListPRComments(ctx, n, r)
}
func (s freshService) BoardInfo(ctx context.Context) (domain.BoardInfo, error) {
	s.log.add(ctx, "BoardInfo", 0)
	return s.Service.BoardInfo(ctx)
}
func (s freshService) BoardCandidates(ctx context.Context) (domain.ProjectsResult, error) {
	s.log.add(ctx, "BoardCandidates", 0)
	return s.Service.BoardCandidates(ctx)
}
func (s freshService) UseBoard(ctx context.Context, ref domain.ProjectRef) (domain.Project, error) {
	s.log.add(ctx, "UseBoard", 0)
	return s.Service.UseBoard(ctx, ref)
}
func (s freshService) Board(ctx context.Context) (domain.Board, error) {
	s.log.add(ctx, "Board", 0)
	return s.Service.Board(ctx)
}

func withFreshLog(l *freshLog) harnessOpt {
	return func(_ *harness, d *Deps, _ *Options) { d.Service = freshService{d.Service, l} }
}

func freshHarness(t *testing.T, fx servicetest.Fixture, opts ...harnessOpt) (*harness, *freshLog) {
	t.Helper()
	l := &freshLog{}
	return newHarness(t, fx, append([]harnessOpt{withFreshLog(l)}, opts...)...), l
}

// wantFresh fails unless calls hold exactly the methods in want (with their
// counts) and every one of them has the fresh mark fresh.
func wantFresh(t *testing.T, calls []freshCall, fresh bool, want map[string]int) {
	t.Helper()
	got := map[string]int{}
	for _, c := range calls {
		got[c.method]++
		if c.fresh != fresh {
			t.Errorf("%s(%d): fresh = %v, want %v", c.method, c.n, c.fresh, fresh)
		}
	}
	if fresh {
		wantSince(t, calls, calls[0].since)
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("calls %v, want %v", got, want)
	}
}

// wantSince fails unless every call is fresh since exactly since.
func wantSince(t *testing.T, calls []freshCall, since time.Time) {
	t.Helper()
	if since.IsZero() {
		t.Error("fresh with a zero time")
	}
	for _, c := range calls {
		if !c.fresh || !c.since.Equal(since) {
			t.Errorf("%s(%d): since %v (fresh %v), want %v", c.method, c.n, c.since, c.fresh, since)
		}
	}
}

func manyCommentsOn(fx *servicetest.Fixture, n, count int) {
	fx.IssueComments[n] = manyComments(count)
}

// ---- r is fresh, on every read of the refresh ----

func TestFresh_rOnIssueListMarksEveryPage(t *testing.T) {
	h, l := freshHarness(t, bigFixture(250))
	h.advance(7 * time.Second)
	i := l.mark()
	h.press("r")
	wantFresh(t, l.since(i), true, map[string]int{"ListIssues": 3})
	wantSince(t, l.since(i), h.now) // the key press, the same on every page
}

// One r gives every read of its refresh the same time; the next r a later one.
func TestFresh_eachRCarriesItsKeyPressTime(t *testing.T) {
	fx := bigFixture(250)
	fx.IssueComments[250] = manyComments(250)
	h, l := freshHarness(t, fx)
	h.press("enter") // #250's detail: 3 comment pages
	h.advance(time.Minute)
	first := h.now
	i := l.mark()
	h.press("r")
	wantFresh(t, l.since(i), true, map[string]int{"GetIssue": 1, "ListIssueComments": 3})
	wantSince(t, l.since(i), first)

	h.press("esc")
	h.advance(time.Minute)
	second := h.now
	i = l.mark()
	h.press("r") // the list: 3 pages
	wantFresh(t, l.since(i), true, map[string]int{"ListIssues": 3})
	wantSince(t, l.since(i), second)
	if !second.After(first) {
		t.Fatal("the clock did not move")
	}
}

func TestFresh_rOnPRList(t *testing.T) {
	h, l := freshHarness(t, richFixture(), withStart(ViewPRs))
	i := l.mark()
	h.press("r")
	wantFresh(t, l.since(i), true, map[string]int{"ListPRs": 1})
}

func TestFresh_rOnBoard(t *testing.T) {
	h, l := freshHarness(t, richFixture(), withStart(ViewBoard))
	i := l.mark()
	h.press("r")
	wantFresh(t, l.since(i), true, map[string]int{"BoardInfo": 1, "Board": 1})
}

func TestFresh_rOnIssueDetailMarksEveryCommentPage(t *testing.T) {
	fx := richFixture()
	manyCommentsOn(&fx, 6, 250)
	h, l := freshHarness(t, fx)
	h.press("enter") // #6
	opened := l.since(0)
	i := l.mark()
	h.press("r")
	wantFresh(t, l.since(i), true, map[string]int{"GetIssue": 1, "ListIssueComments": 3})
	// Opening the detail was not fresh.
	n := 0
	for _, c := range opened {
		if c.method == "ListIssueComments" {
			n++
			if c.fresh {
				t.Error("opening a detail read its comments fresh")
			}
		}
	}
	if n != 3 {
		t.Fatalf("%d comment pages when opening", n)
	}
}

func TestFresh_rOnPRDetail(t *testing.T) {
	h, l := freshHarness(t, richFixture(), withStart(ViewPRs))
	h.press("enter")
	i := l.mark()
	h.press("r")
	wantFresh(t, l.since(i), true, map[string]int{"GetPR": 1, "ListPRComments": 1})
}

func TestFresh_recoveryAfterAuthErrorIsFresh(t *testing.T) {
	h, l := freshHarness(t, richFixture(), withLazy())
	g := h.hold("ListIssues", 0)
	h.boot()
	h.fail(g, &domain.Error{Kind: domain.ErrAuth, Message: "bad credentials"})
	i := l.mark()
	h.press("r")
	wantFresh(t, l.since(i), true, map[string]int{"RefreshMetadata": 1, "Session": 1, "ListIssues": 1})
}

func TestFresh_rReloadsLabelsAndUsersOnlyWhenLoaded(t *testing.T) {
	h, l := freshHarness(t, richFixture())
	i := l.mark()
	h.press("r") // nothing has asked for labels or users yet
	wantFresh(t, l.since(i), true, map[string]int{"ListIssues": 1})

	h.press("n") // the create form loads both
	h.press("esc")
	if h.app.store.Meta.Labels == nil || h.app.store.Meta.Users == nil {
		t.Fatal("the form did not load labels and users")
	}
	h.fake.Mutate(func(f *servicetest.Fixture) {
		f.Labels = append(f.Labels, domain.Label{NodeID: "LABEL_new", Name: "made-on-github", Color: "ededed"})
	})
	i = l.mark()
	h.press("r")
	wantFresh(t, l.since(i), true, map[string]int{"ListIssues": 1, "Labels": 1, "AssignableUsers": 1})
	found := false
	for _, lb := range h.app.store.Meta.Labels {
		found = found || lb.Name == "made-on-github"
	}
	if !found {
		t.Fatal("r did not bring in the new label")
	}
}

// A manual refresh replaces a load that is not fresh; a second r while the
// first is still loading changes nothing.
func TestFresh_rReplacesAnAutoRefreshInFlight(t *testing.T) {
	h, l := freshHarness(t, richFixture())
	g := h.hold("ListIssues", 0)
	h.advance(61 * time.Second)
	h.fire(autoRefreshMsg{})
	i := l.mark()
	h.press("r")
	wantFresh(t, l.since(i), true, map[string]int{"ListIssues": 1})
	h.release(g)
	if h.app.busy(Slot{Kind: SlotIssues}) {
		t.Fatal("still loading")
	}

	g = h.hold("ListIssues", 0)
	h.press("r")
	i = l.mark()
	h.press("r")
	if got := l.since(i); len(got) != 0 {
		t.Fatalf("a second r restarted the refresh: %v", got)
	}
	h.release(g)
}

// ---- nothing else is fresh ----

func TestFresh_notOnStartupOpenAutoRereadPrecheckOrProjectSwitch(t *testing.T) {
	h, l := freshHarness(t, richFixture())
	wantFresh(t, l.since(0), false, map[string]int{"Session": 1, "BoardInfo": 1, "Board": 1, "ListIssues": 1})

	i := l.mark()
	h.advance(61 * time.Second)
	h.fire(autoRefreshMsg{})
	wantFresh(t, l.since(i), false, map[string]int{"ListIssues": 1})

	i = l.mark()
	h.press("enter") // open #6
	wantFresh(t, l.since(i), false, map[string]int{"GetIssue": 1, "ListIssueComments": 1})
	h.press("esc")

	i = l.mark()
	h.press("e") // the edit form reads the issue (precheck slot), and labels and users
	h.press("esc")
	for _, c := range l.since(i) {
		if c.fresh {
			t.Errorf("edit form: %s(%d) was fresh", c.method, c.n)
		}
	}

	i = l.mark()
	h.press("3", ">") // move #6 on the board: a re-read follows 5 s later
	h.advance(rereadAfter)
	h.fire(rereadMsg{})
	for _, c := range l.since(i) {
		if c.fresh {
			t.Errorf("board change and re-read: %s(%d) was fresh", c.method, c.n)
		}
	}
}

func TestFresh_notOnProjectSwitch(t *testing.T) {
	fx := richFixture()
	second := fx.Projects[0]
	second.Title, second.NodeID, second.Ref.Number = "Roadmap", "PROJECT_2", 2
	fx.Projects = append(fx.Projects, second)
	h, l := freshHarness(t, fx) // the issue list loads first
	h.press("enter")            // and an open detail is reloaded by the switch too
	h.press("3", "P")
	i := l.mark()
	h.press("enter") // choose the first candidate
	calls := l.since(i)
	got := map[string]int{}
	for _, c := range calls {
		got[c.method]++
	}
	if got["UseBoard"] != 1 || got["ListIssues"] != 1 || got["GetIssue"] != 1 {
		t.Fatalf("project switch calls %v\n%s", got, h.view())
	}
	for _, c := range calls {
		if c.fresh {
			t.Errorf("project switch: %s(%d) was fresh", c.method, c.n)
		}
	}
	h.contains("Using project todos")
}

// pagesAcrossTime presses r and lets the clock move on before each later page
// is sent, so a continuation that took a new time would show it.
func pagesAcrossTime(h *harness, method string, number, pages int) {
	var gs []*hgate
	for range pages {
		gs = append(gs, h.hold(method, number))
	}
	h.press("r")
	for _, g := range gs {
		h.advance(3 * time.Second)
		h.release(g)
	}
}

func TestFresh_laterPagesKeepTheKeyPressTimeWhileTheClockMoves(t *testing.T) {
	t.Run("issue list", func(t *testing.T) {
		h, l := freshHarness(t, bigFixture(250))
		i, press := l.mark(), h.now
		pagesAcrossTime(h, "ListIssues", 0, 3)
		wantFresh(t, l.since(i), true, map[string]int{"ListIssues": 3})
		wantSince(t, l.since(i), press)
	})
	t.Run("pull requests", func(t *testing.T) {
		fx := richFixture()
		base := fx.PRs[0]
		fx.PRs = nil
		for n := 1; n <= 250; n++ {
			p := base
			p.PullRequest = domain.ClonePR(base.PullRequest)
			p.PullRequest.Number = 1000 + n
			p.PullRequest.UpdatedAt = fixtureNow.Add(-time.Duration(n) * time.Minute)
			fx.PRs = append(fx.PRs, p)
		}
		h, l := freshHarness(t, fx, withStart(ViewPRs))
		if n := len(h.app.store.PRs.Numbers); n != 250 {
			t.Fatalf("%d pull requests loaded", n)
		}
		i, press := l.mark(), h.now
		pagesAcrossTime(h, "ListPRs", 0, 3)
		wantFresh(t, l.since(i), true, map[string]int{"ListPRs": 3})
		wantSince(t, l.since(i), press)
	})
	t.Run("comments", func(t *testing.T) {
		fx := richFixture()
		manyCommentsOn(&fx, 6, 250)
		h, l := freshHarness(t, fx)
		h.press("enter")
		i, press := l.mark(), h.now
		pagesAcrossTime(h, "ListIssueComments", 6, 3)
		wantFresh(t, l.since(i), true, map[string]int{"GetIssue": 1, "ListIssueComments": 3})
		wantSince(t, l.since(i), press)
	})
}
