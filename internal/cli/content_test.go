package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pkieltyka/todos/internal/cache"
	"github.com/pkieltyka/todos/internal/domain"
	"github.com/pkieltyka/todos/internal/service"
	"github.com/pkieltyka/todos/internal/service/servicetest"
)

type timedPages struct {
	service.Service
	stamps  []time.Time
	calls   int
	advance func()
}

func (s *timedPages) ListIssues(context.Context, domain.IssueFilter, domain.PageRequest) (domain.Page[domain.Issue], error) {
	j := s.calls
	s.calls++
	if s.advance != nil {
		s.advance()
	}
	return domain.Page[domain.Issue]{AsOf: s.stamps[j], Items: []domain.Issue{{Number: j + 1, NodeID: domain.NodeID(fmt.Sprint(j + 1))}}, Total: len(s.stamps), HasMore: s.calls < len(s.stamps), NextCursor: fmt.Sprint(j + 1)}, nil
}
func TestAsOf_ListUsesOldestSendOrCachedReadTime(t *testing.T) {
	for _, mode := range []string{"live", "cached", "mixed"} {
		t.Run(mode, func(t *testing.T) {
			at := fixtureTime.Add(123456789 * time.Nanosecond).In(time.FixedZone("local", -5*3600))
			now := at
			stamps := []time.Time{{}, {}}
			want := at
			if mode == "cached" {
				stamps = []time.Time{at.Add(-4 * time.Minute), at.Add(-5 * time.Minute)}
				want = stamps[1]
			}
			if mode == "mixed" {
				stamps[1] = at.Add(-time.Minute)
				want = stamps[1]
			}
			s := &timedPages{stamps: stamps, advance: func() { now = now.Add(time.Minute) }}
			out, err := collectIssues(context.Background(), s, domain.IssueFilter{}, ListOptions{All: true}, func() time.Time { return now })
			if err != nil || out.AsOf != asOfTimestamp(want) || !strings.HasSuffix(out.AsOf, ".123Z") || s.calls != 2 {
				t.Fatal(out, err)
			}
		})
	}
}

type slowReadService struct {
	service.Service
	advance func()
}

func (s slowReadService) GetIssue(ctx context.Context, n int) (domain.IssueDetail, error) {
	s.advance()
	return s.Service.GetIssue(ctx, n)
}
func (s slowReadService) Board(ctx context.Context) (domain.Board, error) {
	s.advance()
	return s.Service.Board(ctx)
}
func TestAsOf_ViewAndBoardStampBeforeServiceCall(t *testing.T) {
	for _, args := range [][]string{{"view", "6", "--json"}, {"board", "--json"}} {
		t.Run(args[0], func(t *testing.T) {
			now := fixtureTime.Add(789 * time.Millisecond)
			out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			f := servicetest.New(servicetest.DefaultFixture())
			deps := fakeDeps(f, out, stderr, false)
			deps.Now = func() time.Time { return now }
			deps.Wire = func(context.Context, func(context.Context) (domain.RepoRef, error), string) (Wiring, error) {
				return Wiring{Service: slowReadService{Service: f, advance: func() { now = now.Add(time.Minute) }}}, nil
			}
			code := Run(context.Background(), args, deps)
			var data map[string]any
			if code != 0 || json.Unmarshal(out.Bytes(), &data) != nil || data["as_of"] != "2026-11-13T12:00:00.789Z" {
				t.Fatal(code, out.String(), stderr.String())
			}
		})
	}
}

// This uses two actual stores over one directory and a backend with no network.
type diskContentBackend struct {
	service.Backend
	mu               sync.Mutex
	issue            domain.Issue
	reads            int
	started, release chan struct{}
}

func (b *diskContentBackend) CredentialKey(context.Context) (string, error) {
	return strings.Repeat("a", 64), nil
}
func (b *diskContentBackend) Repository(context.Context, domain.RepoRef) (domain.Repository, error) {
	return domain.Repository{Ref: domain.RepoRef{Host: "github.com", Owner: "test", Name: "repo"}, NodeID: "R", IssuesEnabled: true}, nil
}
func (b *diskContentBackend) Viewer(context.Context) (domain.User, error) {
	return domain.User{NodeID: "U", Login: "test"}, nil
}
func (b *diskContentBackend) LinkedProjects(context.Context, domain.RepoRef, domain.PageRequest) (domain.Page[domain.Project], error) {
	return domain.Page[domain.Project]{Items: []domain.Project{}}, nil
}
func (b *diskContentBackend) ChangeToken(context.Context, domain.RepoRef, domain.NodeID) (domain.ChangeToken, error) {
	return domain.ChangeToken{Viewer: "test", IssuesTotal: 1, NewestIssue: 1, NewestUpdated: fixtureTime.Add(-time.Minute), ServerTime: fixtureTime}, nil
}
func (b *diskContentBackend) ListIssues(ctx context.Context, _ domain.RepoRef, _ domain.IssueFilter, _ domain.PageRequest, _ domain.NodeID) (domain.Page[domain.Issue], error) {
	b.mu.Lock()
	b.reads++
	issue := domain.CloneIssue(b.issue)
	started, release := b.started, b.release
	b.started, b.release = nil, nil
	b.mu.Unlock()
	if started != nil {
		close(started)
		select {
		case <-ctx.Done():
			return domain.Page[domain.Issue]{}, ctx.Err()
		case <-release:
		}
	}
	return domain.Page[domain.Issue]{Items: []domain.Issue{issue}, Total: 1}, nil
}
func (b *diskContentBackend) Issue(context.Context, domain.RepoRef, int, domain.NodeID) (domain.IssueDetail, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return domain.IssueDetail{Issue: domain.CloneIssue(b.issue)}, nil
}
func (b *diskContentBackend) UpdateIssue(_ context.Context, _ domain.NodeID, p domain.IssuePatch) (domain.Issue, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.issue.Title = *p.Title
	return domain.CloneIssue(b.issue), nil
}
func TestContent_DiskReadHeldAcrossWriteNeverReusesOldEpoch(t *testing.T) {
	for _, separate := range []bool{false, true} {
		t.Run(fmt.Sprint(separate), func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "cache")
			now := func() time.Time { return fixtureTime }
			first := cache.New(root, cache.Options{Now: now})
			second := cache.New(root, cache.Options{Now: now})
			started, release := make(chan struct{}), make(chan struct{})
			b := &diskContentBackend{issue: domain.Issue{Number: 1, NodeID: "I1", Title: "old"}, started: started, release: release}
			opts := service.Options{Cache: first, Now: now, ResolveRepository: func(context.Context) (domain.RepoRef, error) {
				return domain.RepoRef{Host: "github.com", Owner: "test", Name: "repo"}, nil
			}}
			s := service.New(b, nil, opts)
			writer := s
			if separate {
				opts.Cache = second
				writer = service.New(b, nil, opts)
			}
			done := make(chan error, 1)
			go func() {
				_, err := s.ListIssues(context.Background(), domain.IssueFilter{}, domain.PageRequest{})
				done <- err
			}()
			<-started
			title := "new"
			if _, err := writer.EditIssue(context.Background(), 1, domain.EditIssue{Title: &title}); err != nil {
				t.Fatal(err)
			}
			close(release)
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			files, err := filepath.Glob(filepath.Join(root, strings.Repeat("a", 64), "content", "*.json"))
			if err != nil || len(files) != 1 {
				t.Fatal("held page was not stored", files, err)
			}
			page, err := s.ListIssues(context.Background(), domain.IssueFilter{}, domain.PageRequest{})
			if err != nil || page.Items[0].Title != "new" || !page.AsOf.IsZero() || b.reads != 2 {
				t.Fatal("pre-write epoch reused", page, err, b.reads)
			}
			page, err = s.ListIssues(context.Background(), domain.IssueFilter{}, domain.PageRequest{})
			if err != nil || page.AsOf.IsZero() || b.reads != 2 {
				t.Fatal("new page did not cache", page, err)
			}
			if _, err := os.Stat(files[0]); err != nil {
				t.Fatal(err)
			}
		})
	}
}
