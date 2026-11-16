package tui

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/pkieltyka/todos/internal/domain"
	"github.com/pkieltyka/todos/internal/markdown"
	"github.com/pkieltyka/todos/internal/service"
	"github.com/pkieltyka/todos/internal/service/servicetest"
)

// Tests for the Phase 3 review findings (reviews/phase-3-lead-dev.md). Each
// fails with its fix removed.

func methods(h *harness, names ...string) []string {
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	var out []string
	for _, c := range h.fake.Calls() {
		if want[c.Method] {
			out = append(out, c.Method)
		}
	}
	return out
}

// Finding 2: after an auth error, r refreshes metadata and the session before
// the visible read, and picks up a changed account.
func TestRecovery_authRetryRefreshesMetadataAndSession(t *testing.T) {
	h := newHarness(t, richFixture(), withLazy())
	g := h.hold("ListIssues", 0)
	h.boot()
	h.fail(g, &domain.Error{Kind: domain.ErrAuth, Message: "bad credentials"})
	n := len(methods(h, "RefreshMetadata", "Session", "ListIssues"))
	h.fake.Mutate(func(f *servicetest.Fixture) { f.Session.Viewer.Login = "octocat" })
	h.press("r")
	got := methods(h, "RefreshMetadata", "Session", "ListIssues")[n:]
	if len(got) != 3 || got[0] != "RefreshMetadata" || got[1] != "Session" || got[2] != "ListIssues" {
		t.Fatalf("calls after r: %v", got)
	}
	if v := h.app.store.Meta.Session.Viewer.Login; v != "octocat" {
		t.Fatalf("viewer %q not reloaded", v)
	}
	h.contains("4 open issues")
}

// A network error is retried directly: no metadata refresh.
func TestRecovery_networkRetryIsPlain(t *testing.T) {
	h := newHarness(t, richFixture(), withLazy())
	g := h.hold("ListIssues", 0)
	h.boot()
	h.fail(g, &domain.Error{Kind: domain.ErrNetwork, Message: "offline"})
	h.press("r")
	if n := len(h.calls("RefreshMetadata")); n != 0 {
		t.Fatalf("%d metadata refreshes", n)
	}
}

func severalFixture() servicetest.Fixture {
	fx := richFixture()
	fx.BoardInfo.Project = domain.Project{}
	second := fx.Projects[0]
	second.Title, second.NodeID, second.Ref.Number = "Roadmap", "PROJECT_2", 2
	fx.Projects = append(fx.Projects, second)
	return fx
}

// Finding 3: choosing a project forgets the old placements at once and
// reloads the issue list and the board.
func TestChooseBoard_clearsOldPlacementsAndReloads(t *testing.T) {
	fx := severalFixture()
	fx.Issues[0].Issue.Board = &domain.Placement{ItemID: "OLD_ITEM", Status: ptr("Todo")}
	h := newHarness(t, fx)
	if i, _ := h.app.store.Issue(6); i.Board == nil || i.Board.ItemID != "OLD_ITEM" {
		t.Fatal("setup: no old placement")
	}
	lists := len(h.calls("ListIssues"))
	h.press("3")
	g := h.hold("ListIssues", 0)
	gb := h.hold("Board", 0) // the board reload (Board, then BoardInfo) is held too
	h.press("enter")         // use the first candidate
	if i, _ := h.app.store.Issue(6); i.Board != nil {
		t.Fatalf("old placement kept: %+v", i.Board)
	}
	if len(h.calls("ListIssues")) != lists+1 {
		t.Fatal("issue list not reloaded")
	}
	h.release(g)
	h.release(gb)
	h.contains("Board: todos (#1)")
}

// Finding 4: a renamed Status keeps its cards: columns come from the Board
// result, and BoardInfo is refreshed with it.
func TestBoard_renamedStatusKeepsCards(t *testing.T) {
	h := newHarness(t, richFixture(), withStart(ViewBoard))
	h.fake.Mutate(func(f *servicetest.Fixture) {
		f.BoardInfo.Options[0].Name = "RenamedTodo"
		for i := range f.Board.Columns {
			if s := f.Board.Columns[i].Status; s != nil && *s == "Todo" {
				f.Board.Columns[i].Status = ptr("RenamedTodo")
				for k := range f.Board.Columns[i].Cards {
					f.Board.Columns[i].Cards[k].Board.Status = ptr("RenamedTodo")
				}
			}
		}
		for i := range f.Issues {
			if b := f.Issues[i].Issue.Board; b != nil && b.Status != nil && *b.Status == "Todo" {
				f.Issues[i].Issue.Board.Status = ptr("RenamedTodo")
			}
		}
	})
	h.press("r")
	h.contains("RenamedTodo  1")
	h.contains("3 cards")
	if n := len(h.calls("BoardInfo")); n != 2 {
		t.Fatalf("BoardInfo called %d times", n)
	}
}

// Finding 5: a comments failure survives a later successful body.
func TestDetail_commentsErrorSurvivesBodySuccess(t *testing.T) {
	h := newHarness(t, richFixture())
	gb := h.hold("GetIssue", 6)
	gc := h.hold("ListIssueComments", 6)
	h.press("enter")
	h.fail(gc, &domain.Error{Kind: domain.ErrNetwork, Message: "COMMENTS_FAILED"})
	h.release(gb)
	d, _ := h.app.store.PeekDetail(6)
	if d.CommentsErr == nil || !d.Loaded {
		t.Fatalf("comments error lost: %+v", d)
	}
	h.contains("Couldn't load comments: COMMENTS_FAILED")
}

// Finding 6: a newer list page is not masked by an older PR detail.
func TestPRs_newerListBeatsOlderDetail(t *testing.T) {
	h := newHarness(t, richFixture(), withStart(ViewPRs))
	h.press("enter", "esc")
	h.fake.Mutate(func(f *servicetest.Fixture) { f.PRs[0].PullRequest.Title = "NEW_TITLE_FROM_GITHUB" })
	h.advance(time.Second)
	h.press("r")
	h.contains("NEW_TITLE_FROM_GITHUB")
}

// Finding 7: a view whose first load failed is retried by auto-refresh.
func TestAutoRefresh_retriesFailedInitialLoad(t *testing.T) {
	h := newHarness(t, richFixture(), withLazy())
	g := h.hold("ListIssues", 0)
	h.boot()
	h.fail(g, &domain.Error{Kind: domain.ErrNetwork, Message: "offline"})
	h.advance(10 * time.Minute)
	h.fire(autoRefreshMsg{})
	if n := len(h.calls("ListIssues")); n != 2 {
		t.Fatalf("ListIssues called %d times", n)
	}
	h.contains("4 open issues")
}

// Finding 8: an auto tick does not restart a detail whose comments are still
// paging.
func TestAutoRefresh_doesNotCancelCommentPaging(t *testing.T) {
	h := newHarness(t, richFixture())
	gc := h.hold("ListIssueComments", 6)
	h.press("enter")
	h.advance(61 * time.Second)
	h.fire(autoRefreshMsg{})
	if n := len(h.calls("GetIssue")); n != 1 {
		t.Fatalf("detail reloaded while comments were paging (%d GetIssue)", n)
	}
	h.release(gc)
	if d, _ := h.app.store.PeekDetail(6); len(d.Comments) != 2 {
		t.Fatalf("comments %d", len(d.Comments))
	}
}

// Finding 10: a failed detail refresh shows its reason and fix above the
// retained body.
func TestDetail_refreshFailureShowsReasonAndHint(t *testing.T) {
	h := newHarness(t, richFixture())
	h.press("enter")
	g := h.hold("GetIssue", 6)
	h.press("r")
	h.fail(g, &domain.Error{Kind: domain.ErrNetwork, Message: "BODY_REFRESH_FAILED", Hint: "Check the connection"})
	h.contains("⚠ Refresh failed: BODY_REFRESH_FAILED")
	h.contains("Check the connection")
	h.contains("can't reach the network") // the old body is still there
}

// Finding 11: a failed candidate list leaves the loading state, says why,
// and P tries again.
func TestBoardSetup_candidatesFailureShowsErrorAndRetry(t *testing.T) {
	h := newHarness(t, unlinkedFixture(), withStart(ViewBoard))
	g := h.hold("BoardCandidates", 0)
	h.press("P")
	h.fail(g, &domain.Error{Kind: domain.ErrForbidden, Message: "NOPE"})
	h.lacks("Loading projects")
	h.contains("Couldn't list projects: NOPE")
	if s := h.app.top(ViewBoard).(*BoardSetup); s.loading {
		t.Fatal("still loading")
	}
	h.press("P")
	if n := len(h.calls("BoardCandidates")); n != 2 {
		t.Fatalf("BoardCandidates called %d times", n)
	}
	h.contains("todos (#1)")
}

// blockingService blocks ListIssues until its context is cancelled and
// reports that context.
type blockingService struct {
	service.Service
	started chan context.Context
}

func (b blockingService) ListIssues(ctx context.Context, f domain.IssueFilter, p domain.PageRequest) (domain.Page[domain.Issue], error) {
	b.started <- ctx
	<-ctx.Done()
	return domain.Page[domain.Issue]{}, ctx.Err()
}

// Finding 9: quitting cancels every read still running.
func TestRun_cancelsReadsOnQuit(t *testing.T) {
	svc := blockingService{Service: servicetest.New(richFixture()), started: make(chan context.Context, 1)}
	in, keys := io.Pipe()
	deps := Deps{Service: svc, Renderer: markdown.NewRenderer(markdown.Options{}), Clock: func() time.Time { return fixtureNow }, Env: Env{Theme: "dark"}}
	done := make(chan error, 1)
	go func() {
		done <- run(context.Background(), deps, Options{Repo: domain.RepoRef{Owner: "pkieltyka", Name: "todos"}},
			tea.WithInput(in), tea.WithOutput(io.Discard), tea.WithWindowSize(80, 24))
	}()
	var readCtx context.Context
	select {
	case readCtx = <-svc.started:
	case <-time.After(5 * time.Second):
		t.Fatal("ListIssues never started")
	}
	_, _ = keys.Write([]byte("q"))
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("program did not quit")
	}
	select {
	case <-readCtx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("read still alive after Run returned")
	}
	_ = keys.Close()
}

// ---- the board load reads Board, then BoardInfo ----

// staleInfoService answers BoardInfo from a stale memo until Board has run,
// as the service does: Board reads the board's fields live and refreshes the
// BoardInfo memo with them.
type staleInfoService struct {
	service.Service
	mu        sync.Mutex
	refreshed bool
}

func (s *staleInfoService) Board(ctx context.Context) (domain.Board, error) {
	b, err := s.Service.Board(ctx)
	s.mu.Lock()
	s.refreshed = true
	s.mu.Unlock()
	return b, err
}

func (s *staleInfoService) BoardInfo(ctx context.Context) (domain.BoardInfo, error) {
	info, err := s.Service.BoardInfo(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.refreshed {
		info.Project.Title = "Old project title"
	}
	return info, err
}

func TestBoard_headerShowsTheProjectInfoBoardRefreshed(t *testing.T) {
	stale := &staleInfoService{}
	h := newHarness(t, richFixture(), withStart(ViewBoard), func(_ *harness, d *Deps, _ *Options) {
		stale.Service = d.Service
		d.Service = stale
	})
	h.contains("Board: todos (#1)")
	h.lacks("Old project title")
}

// The setup states still appear when the board cannot be used: Board, read
// first, fails with the same errors.
func TestBoard_setupStatesStillAppearWithBoardReadFirst(t *testing.T) {
	t.Run("no board", func(t *testing.T) {
		h := newHarness(t, unlinkedFixture(), withStart(ViewBoard))
		if _, ok := h.app.top(ViewBoard).(*BoardSetup); !ok {
			t.Fatal("no board setup screen")
		}
		if len(h.calls("BoardInfo")) != 0 {
			t.Fatal("BoardInfo read after Board failed")
		}
	})
	t.Run("missing scope", func(t *testing.T) {
		h := newHarness(t, richFixture(), withStart(ViewBoard), withLazy())
		g := h.hold("Board", 0)
		h.boot()
		h.fail(g, &domain.Error{Kind: domain.ErrMissingScope, Message: "missing project scope"})
		if _, ok := h.app.top(ViewBoard).(*BoardSetup); !ok {
			t.Fatal("no board setup screen")
		}
		h.contains(`The board needs the "project" permission`)
	})
}
