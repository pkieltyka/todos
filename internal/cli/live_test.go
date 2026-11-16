package cli_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/pkieltyka/todos/internal/cli"
	"github.com/pkieltyka/todos/internal/domain"
	"github.com/pkieltyka/todos/internal/github"
	"github.com/pkieltyka/todos/internal/service"
)

// The opt-in test receives only read methods. It cannot create issues, choose
// a remembered board, or invoke any other remote or local mutation.
type liveReads interface {
	Session(context.Context) (domain.Session, error)
	ListIssues(context.Context, domain.IssueFilter, domain.PageRequest) (domain.Page[domain.Issue], error)
	GetIssue(context.Context, int) (domain.IssueDetail, error)
	ListIssueComments(context.Context, int, domain.PageRequest) (domain.Page[domain.Comment], error)
	ListPRs(context.Context, domain.PRFilter, domain.PageRequest) (domain.Page[domain.PullRequest], error)
	GetPR(context.Context, int) (domain.PRDetail, error)
	ListPRComments(context.Context, int, domain.PageRequest) (domain.Page[domain.Comment], error)
	BoardInfo(context.Context) (domain.BoardInfo, error)
	BoardCandidates(context.Context) (domain.ProjectsResult, error)
	Board(context.Context) (domain.Board, error)
	Labels(context.Context) ([]domain.Label, error)
	AssignableUsers(context.Context) ([]domain.User, error)
	Doctor(context.Context) (domain.DoctorReport, error)
}

func newLiveReads(t *testing.T) (context.Context, liveReads) {
	t.Helper()
	if os.Getenv("TODOS_LIVE_TESTS") != "1" {
		t.Skip("read-only live tests require TODOS_LIVE_TESTS=1")
	}
	requested := os.Getenv("TODOS_LIVE_REPO")
	if requested == "" {
		t.Fatal("set TODOS_LIVE_REPO=OWNER/REPO explicitly")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	ref, err := cli.ResolveRepository(ctx, requested)
	if err != nil {
		t.Fatal(err)
	}
	backend := github.New(github.Options{Version: "live-test", Host: func(context.Context) (string, error) { return ref.Host, nil }})
	s := service.New(backend, nil, service.Options{ResolveRepository: func(context.Context) (domain.RepoRef, error) { return ref, nil }, Project: os.Getenv("TODOS_LIVE_PROJECT")})
	return ctx, s
}
func optionalLiveBoard(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	var de *domain.Error
	if errors.As(err, &de) && (de.Kind == domain.ErrNoBoard || de.Kind == domain.ErrMissingScope || de.Kind == domain.ErrForbidden) {
		t.Skipf("board unavailable (%s); set TODOS_LIVE_PROJECT and check project access", de.Kind)
	}
	t.Fatal(err)
}
func TestLive_ReadPath(t *testing.T) {
	ctx, s := newLiveReads(t)
	session, err := s.Session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if session.Repo.NodeID == "" || session.Viewer.Login == "" || session.Repo.URL == "" {
		t.Fatalf("missing session identity: %+v", session)
	}
	t.Logf("read-only target %s/%s; viewer %s", session.Repo.Ref.Owner, session.Repo.Ref.Name, session.Viewer.Login)
	t.Run("IssuesAndComments", func(t *testing.T) {
		if !session.Repo.IssuesEnabled {
			t.Skip("issues disabled")
		}
		filter := domain.IssueFilter{State: "all"}
		page, err := s.ListIssues(ctx, filter, domain.PageRequest{Size: 2})
		if err != nil {
			t.Fatal(err)
		}
		if page.Items == nil || len(page.Items) > 2 {
			t.Fatal("invalid issue page")
		}
		for _, i := range page.Items {
			if i.NodeID == "" || i.Number <= 0 || !strings.HasPrefix(i.URL, session.Repo.URL+"/issues/") {
				t.Fatal("issue outside selected repository")
			}
			for _, login := range i.Assignees {
				if i.AssigneeIDs[login] == "" {
					t.Fatal("issue-owned assignee ID missing")
				}
			}
		}
		if page.HasMore {
			if page.NextCursor == "" {
				t.Fatal("missing raw cursor")
			}
			next, err := s.ListIssues(ctx, filter, domain.PageRequest{Size: 2, Cursor: page.NextCursor})
			if err != nil {
				t.Fatal(err)
			}
			if next.Items == nil || len(next.Items) > 2 {
				t.Fatal("invalid second issue page")
			}
		}
		if len(page.Items) == 0 {
			t.Log("repository has no issues; detail/comment reads skipped")
			return
		}
		detail, err := s.GetIssue(ctx, page.Items[0].Number)
		if err != nil {
			t.Fatal(err)
		}
		if detail.Issue.NodeID != page.Items[0].NodeID {
			t.Fatal("detail changed issue identity")
		}
		comments, err := s.ListIssueComments(ctx, detail.Issue.Number, domain.PageRequest{Size: 100})
		if err != nil {
			t.Fatal(err)
		}
		if comments.Items == nil || len(comments.Items) > 100 || comments.HasMore && comments.NextCursor == "" {
			t.Fatal("invalid comment page")
		}
	})
	t.Run("PullRequests", func(t *testing.T) {
		page, err := s.ListPRs(ctx, domain.PRFilter{State: "all"}, domain.PageRequest{Size: 2})
		if err != nil {
			t.Fatal(err)
		}
		if page.Items == nil || len(page.Items) > 2 {
			t.Fatal("invalid PR page")
		}
		if len(page.Items) == 0 {
			t.Log("repository has no pull requests; detail/comment reads skipped")
			return
		}
		pr, err := s.GetPR(ctx, page.Items[0].Number)
		if err != nil {
			t.Fatal(err)
		}
		if pr.PullRequest.NodeID != page.Items[0].NodeID {
			t.Fatal("PR identity changed")
		}
		comments, err := s.ListPRComments(ctx, pr.PullRequest.Number, domain.PageRequest{Size: 100})
		if err != nil {
			t.Fatal(err)
		}
		if comments.Items == nil {
			t.Fatal("nil PR comments")
		}
	})
	t.Run("RepositoryMetadata", func(t *testing.T) {
		if !session.Repo.IssuesEnabled {
			t.Skip("issue metadata disabled")
		}
		labels, err := s.Labels(ctx)
		if err != nil {
			t.Fatal(err)
		}
		users, err := s.AssignableUsers(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if labels == nil || users == nil {
			t.Fatal("metadata arrays are nil")
		}
		for _, label := range labels {
			if label.NodeID == "" || label.Name == "" {
				t.Fatal("label identity missing")
			}
		}
		for _, user := range users {
			if user.NodeID == "" || user.Login == "" {
				t.Fatal("user identity missing")
			}
		}
	})
	t.Run("BoardCandidates", func(t *testing.T) {
		result, err := s.BoardCandidates(ctx)
		optionalLiveBoard(t, err)
		seen := map[domain.NodeID]bool{}
		for _, p := range result.Projects {
			if p.NodeID == "" || seen[p.NodeID] {
				t.Fatal("missing/duplicate project identity")
			}
			seen[p.NodeID] = true
		}
	})
	t.Run("Board", func(t *testing.T) {
		info, err := s.BoardInfo(ctx)
		optionalLiveBoard(t, err)
		if !info.Project.Usable || info.StatusFieldID == "" {
			t.Fatal("resolved board is unusable")
		}
		board, err := s.Board(ctx)
		optionalLiveBoard(t, err)
		if board.Project.NodeID != info.Project.NodeID || board.ScannedCount > 1000 {
			t.Fatal("board identity or scan bound invalid")
		}
		for _, column := range board.Columns {
			for _, card := range column.Cards {
				if !strings.HasPrefix(card.URL, session.Repo.URL+"/issues/") {
					t.Fatal("foreign card escaped board filter")
				}
				if card.Board == nil {
					t.Fatal("card lacks placement")
				}
			}
		}
	})
	t.Run("Doctor", func(t *testing.T) {
		report, err := s.Doctor(ctx)
		if len(report.Checks) != 7 {
			t.Fatal("doctor did not report seven checks")
		}
		wantOK := report.Checks[0].OK && report.Checks[1].OK && report.Checks[2].OK
		if report.OK != wantOK || (err == nil) != wantOK {
			t.Fatal("doctor verdict differs from first three checks")
		}
		if err != nil {
			var de *domain.Error
			if !errors.As(err, &de) {
				t.Fatal("untyped doctor error")
			}
			t.Logf("read-only doctor reports %s; no write was attempted", de.Kind)
		}
	})
}
