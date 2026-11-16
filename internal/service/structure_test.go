package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pkieltyka/todos/internal/domain"
)

type cacheValue struct {
	data []byte
	at   time.Time
}
type memoryCache struct {
	mu                sync.Mutex
	values            map[string]cacheValue
	gets, puts, drops int
}

func (m *memoryCache) Get(space, key string) ([]byte, time.Time, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gets++
	v, ok := m.values[space+"|"+key]
	return append([]byte(nil), v.data...), v.at, ok
}
func (m *memoryCache) Put(space, key string, data []byte, at time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.values == nil {
		m.values = map[string]cacheValue{}
	}
	m.puts++
	m.values[space+"|"+key] = cacheValue{append([]byte(nil), data...), at}
}
func (m *memoryCache) Drop(space, prefix string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.drops++
	for key := range m.values {
		if strings.HasPrefix(key, space+"|"+prefix) {
			delete(m.values, key)
		}
	}
}
func (*memoryCache) Epoch(string) string { return "none" }
func (*memoryCache) Bump(string) bool    { return true }

type cacheClock struct{ ns atomic.Int64 }

func newCacheClock() *cacheClock {
	c := &cacheClock{}
	c.ns.Store(time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC).UnixNano())
	return c
}
func (c *cacheClock) Now() time.Time      { return time.Unix(0, c.ns.Load()).UTC() }
func (c *cacheClock) Add(d time.Duration) { c.ns.Add(int64(d)) }
func cacheBackend() *fakeBackend {
	b := backendFixture()
	b.labels[0].NodeID = "L1"
	b.users[0].NodeID = "U1"
	return b
}
func cachedCore(b Backend, m CacheStore, c *cacheClock) *core {
	return New(b, &fakeSettings{}, Options{Cache: m, Now: c.Now, ResolveRepository: func(context.Context) (domain.RepoRef, error) {
		return domain.RepoRef{Host: "github.com", Owner: "test", Name: "repo"}, nil
	}}).(*core)
}
func warmStructure(t *testing.T, s *core, ctx context.Context) {
	t.Helper()
	if _, err := s.Session(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Labels(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AssignableUsers(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BoardInfo(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestStructure_PersistentAcrossServicesAndFreshBypassesBoth(t *testing.T) {
	c := newCacheClock()
	m := &memoryCache{}
	b := cacheBackend()
	s := cachedCore(b, m, c)
	warmStructure(t, s, context.Background())
	second := cachedCore(b, m, c)
	warmStructure(t, second, context.Background())
	for _, name := range []string{"Repository", "Viewer", "Labels", "AssignableUsers", "ProjectFields"} {
		if b.count(name) != 1 {
			t.Fatalf("%s not reused across services: %d", name, b.count(name))
		}
	}
	c.Add(time.Nanosecond)
	warmStructure(t, second, domain.WithFreshSince(context.Background(), c.Now()))
	for _, name := range []string{"Repository", "Viewer", "Labels", "AssignableUsers", "ProjectFields"} {
		if b.count(name) != 2 {
			t.Fatalf("fresh reused %s", name)
		}
	}
}

func TestStructure_OneExpiryFromOriginalReadAt(t *testing.T) {
	for _, board := range []bool{false, true} {
		t.Run(map[bool]string{false: "five-minute", true: "board-one-minute"}[board], func(t *testing.T) {
			c := newCacheClock()
			m := &memoryCache{}
			b := cacheBackend()
			s := cachedCore(b, m, c)
			load := func(s *core) {
				if board {
					if _, e := s.BoardInfo(context.Background()); e != nil {
						t.Fatal(e)
					}
				} else {
					if _, e := s.Labels(context.Background()); e != nil {
						t.Fatal(e)
					}
				}
			}
			load(s)
			ttl := 5 * time.Minute
			if board {
				ttl = time.Minute
			}
			c.Add(ttl - time.Second)
			second := cachedCore(b, m, c)
			load(second)
			name := "Labels"
			if board {
				name = "ProjectFields"
			}
			if b.count(name) != 1 {
				t.Fatal("file was not reused")
			}
			c.Add(time.Second)
			load(second)
			if b.count(name) != 2 {
				t.Fatal("disk hit extended in-memory expiry")
			}
		})
	}
}

func TestStructure_SessionExpiresAfterFiveMinutes(t *testing.T) {
	c := newCacheClock()
	m := &memoryCache{}
	b := cacheBackend()
	s := cachedCore(b, m, c)
	s.Session(context.Background())
	c.Add(5 * time.Minute)
	s.Session(context.Background())
	if b.count("Repository") != 2 || b.count("Viewer") != 2 {
		t.Fatal("session remains unbounded")
	}
}

func TestStructure_ReadBeforeRefreshStoredAfterIsMiss(t *testing.T) {
	c := newCacheClock()
	m := &memoryCache{}
	b := cacheBackend()
	s := cachedCore(b, m, c)
	warmStructure(t, s, context.Background())
	space := s.cacheSpace
	oldData, oldAt, _ := m.Get(space, "labels")
	c.Add(time.Second)
	if err := s.RefreshMetadata(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Another process completes an old in-flight read after this refresh.
	c.Add(time.Second)
	m.Put(space, "labels", oldData, oldAt)
	b.labels = []domain.Label{{NodeID: "L2", Name: "new"}}
	labels, err := s.Labels(context.Background())
	if err != nil || len(labels) != 1 || labels[0].Name != "new" || b.count("Labels") != 2 {
		t.Fatal("late old file resurrected metadata", err)
	}
}

func TestStructure_AnotherServiceReadHeldAcrossRefresh(t *testing.T) {
	c := newCacheClock()
	m := &memoryCache{}
	b := cacheBackend()
	s := cachedCore(b, m, c)
	if _, err := s.Session(context.Background()); err != nil {
		t.Fatal(err)
	}
	oldBackend := cacheBackend()
	oldBackend.labelStarted = make(chan struct{}, 1)
	oldBackend.labelRelease = make(chan struct{})
	other := cachedCore(oldBackend, m, c)
	done := make(chan error, 1)
	go func() { _, err := other.Labels(context.Background()); done <- err }()
	<-oldBackend.labelStarted
	c.Add(time.Second)
	if err := s.RefreshMetadata(context.Background()); err != nil {
		t.Fatal(err)
	}
	c.Add(time.Second)
	close(oldBackend.labelRelease)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	b.labels = []domain.Label{{NodeID: "NEW", Name: "new"}}
	labels, err := s.Labels(context.Background())
	if err != nil || labels[0].Name != "new" || b.count("Labels") != 1 {
		t.Fatal("old in-flight read was accepted after refresh", err)
	}
}

func TestStructure_ReadAtPrecedesBlockedGitHubRead(t *testing.T) {
	c := newCacheClock()
	m := &memoryCache{}
	b := cacheBackend()
	b.labelStarted = make(chan struct{}, 1)
	b.labelRelease = make(chan struct{})
	s := cachedCore(b, m, c)
	startedAt := c.Now()
	done := make(chan error, 1)
	go func() { _, e := s.Labels(context.Background()); done <- e }()
	<-b.labelStarted
	c.Add(4 * time.Minute)
	close(b.labelRelease)
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	_, at, ok := m.Get(s.cacheSpace, "labels")
	if !ok || !at.Equal(startedAt) {
		t.Fatal("file timestamp was completion time")
	}
	c.Add(time.Minute)
	s.Labels(context.Background())
	if b.count("Labels") != 2 {
		t.Fatal("read age measured from completion")
	}
}

func TestStructure_CredentialChangesPartitionMemoAndFile(t *testing.T) {
	c := newCacheClock()
	m := &memoryCache{}
	b := cacheBackend()
	s := cachedCore(b, m, c)
	warmStructure(t, s, context.Background())
	first := s.cacheSpace
	b.credentialKey = strings.Repeat("b", 64)
	b.labels = []domain.Label{{NodeID: "L2", Name: "private-other"}}
	labels, err := s.Labels(context.Background())
	if err != nil || labels[0].Name != "private-other" || s.cacheSpace == first || b.count("Labels") != 2 || b.count("Repository") != 2 {
		t.Fatal("different credential reused metadata", err)
	}
}

type switchingLabels struct {
	Backend
	switchToken func()
}

func (b *switchingLabels) ListLabels(ctx context.Context, ref domain.RepoRef, page domain.PageRequest) (domain.Page[domain.Label], error) {
	b.switchToken()
	return b.Backend.ListLabels(ctx, ref, page)
}
func TestStructure_CredentialChangedDuringFillIsNotStoredInOldPartition(t *testing.T) {
	c := newCacheClock()
	m := &memoryCache{}
	b := cacheBackend()
	backend := &switchingLabels{Backend: b, switchToken: func() { b.credentialKey = strings.Repeat("b", 64) }}
	s := cachedCore(backend, m, c)
	if _, err := s.Session(context.Background()); err != nil {
		t.Fatal(err)
	}
	old := s.cacheSpace
	if _, err := s.Labels(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := m.Get(old, "labels"); ok {
		t.Fatal("new credential data stored in old partition")
	}
	if s.labels.Valid {
		t.Fatal("old identity memo was published")
	}
}

func TestStructure_InvalidPayloadAndFutureReadAreMisses(t *testing.T) {
	for _, payload := range []string{"{", "null", "{}", "[{}]"} {
		t.Run(payload, func(t *testing.T) {
			c := newCacheClock()
			m := &memoryCache{}
			b := cacheBackend()
			s := cachedCore(b, m, c)
			s.Session(context.Background())
			m.Put(s.cacheSpace, "labels", []byte(payload), c.Now())
			labels, e := s.Labels(context.Background())
			if e != nil || len(labels) != 1 || b.count("Labels") != 1 {
				t.Fatal("bad payload used", e)
			}
		})
	}
	c := newCacheClock()
	m := &memoryCache{}
	b := cacheBackend()
	s := cachedCore(b, m, c)
	s.Session(context.Background())
	data, _ := json.Marshal([]domain.Label{{NodeID: "other", Name: "future"}})
	m.Put(s.cacheSpace, "labels", data, c.Now().Add(time.Second))
	v, e := s.Labels(context.Background())
	if e != nil || v[0].Name != "bug" {
		t.Fatal("future read accepted")
	}
}

func TestStructure_DifferentBoardKeysAndUseBoard(t *testing.T) {
	c := newCacheClock()
	m := &memoryCache{}
	b := cacheBackend()
	p := b.linked[0]
	p.Ref.Number = 2
	p.NodeID = "P2"
	p.Title = "Two"
	b.fields["P2"] = domain.BoardInfo{Project: p, StatusFieldID: "F2", Options: []domain.StatusOption{{ID: "s", Name: "Sprint"}}}
	s := cachedCore(b, m, c)
	s.opts.Project = "1"
	one, e := s.BoardInfo(context.Background())
	if e != nil || one.Project.NodeID != "P1" {
		t.Fatal(e)
	}
	second := cachedCore(b, m, c)
	second.opts.Project = "2"
	two, e := second.BoardInfo(context.Background())
	if e != nil || two.Project.NodeID != "P2" {
		t.Fatal("project key crossed", e)
	}
	_, e = second.UseBoard(context.Background(), domain.ProjectRef{Host: "github.com", Owner: "test", Number: 1})
	if e != nil {
		t.Fatal(e)
	}
	v, e := second.BoardInfo(context.Background())
	if e != nil || v.Project.NodeID != "P1" {
		t.Fatal("UseBoard reused another entry", e)
	}
}

func TestStructure_CancellationSkipsStoreAndHTTP(t *testing.T) {
	c := newCacheClock()
	m := &memoryCache{}
	b := cacheBackend()
	s := cachedCore(b, m, c)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := s.Labels(ctx)
	if !errors.Is(err, context.Canceled) || m.gets != 0 || m.puts != 0 || len(b.calls) != 0 {
		t.Fatal("cancelled cache call did work", err)
	}
}

func TestStructure_DoctorBypassesWarmCacheAndWritesNothing(t *testing.T) {
	c := newCacheClock()
	m := &memoryCache{}
	b := cacheBackend()
	s := cachedCore(b, m, c)
	warmStructure(t, s, context.Background())
	gets, puts, drops := m.gets, m.puts, m.drops
	report, e := s.Doctor(context.Background())
	if e != nil || len(report.Checks) != 7 || !report.OK {
		t.Fatal(report, e)
	}
	if m.gets != gets || m.puts != puts || m.drops != drops {
		t.Fatal("doctor accessed store")
	}
	for _, name := range []string{"Repository", "Viewer", "ProjectFields"} {
		if b.count(name) != 2 {
			t.Fatal("doctor used memo", name)
		}
	}
	s.opts.CacheCheck = func() domain.DoctorCheck { return domain.DoctorCheck{Name: "Cache", OK: false, Detail: "unusable"} }
	report, e = s.Doctor(context.Background())
	if e != nil || !report.OK || report.Checks[6].OK {
		t.Fatal("cache failure changed doctor verdict", e)
	}
}

type rejectedCreate struct {
	Backend
	err   error
	calls int
}

func (b *rejectedCreate) CreateIssue(context.Context, domain.CreateInput) (domain.Issue, error) {
	b.calls++
	return domain.Issue{}, b.err
}
func TestStructure_WriteRefusalsDropOnlyOnNotFoundOrInvalid(t *testing.T) {
	for _, kind := range []domain.ErrorKind{domain.ErrNotFound, domain.ErrInvalidInput, domain.ErrForbidden, domain.ErrNetwork} {
		t.Run(string(kind), func(t *testing.T) {
			c := newCacheClock()
			m := &memoryCache{}
			b := &rejectedCreate{Backend: cacheBackend(), err: &domain.Error{Kind: kind, Message: "rejected", Uncertain: kind == domain.ErrNetwork}}
			s := cachedCore(b, m, c)
			warmStructure(t, s, context.Background())
			before := m.drops
			_, e := s.CreateIssue(context.Background(), domain.NewIssue{Title: "test"})
			if e == nil || b.calls != 1 {
				t.Fatal("mutation missing or replayed")
			}
			want := kind == domain.ErrNotFound || kind == domain.ErrInvalidInput
			if (m.drops > before) != want {
				t.Fatal("wrong structure invalidation")
			}
			if want && s.session.Valid {
				t.Fatal("warm memo survived refusal")
			}
		})
	}
}

func TestStructure_FreshCutoffAcceptsAtOrAfterAndRejectsBefore(t *testing.T) {
	for _, disk := range []bool{false, true} {
		for _, relation := range []string{"before", "at", "after"} {
			t.Run(map[bool]string{false: "memo", true: "file"}[disk]+"/"+relation, func(t *testing.T) {
				c := newCacheClock()
				m := &memoryCache{}
				b := cacheBackend()
				s := cachedCore(b, m, c)
				readAt := c.Now()
				if _, err := s.Session(context.Background()); err != nil {
					t.Fatal(err)
				}
				if disk {
					s = cachedCore(b, m, c)
				}
				c.Add(time.Second)
				cutoff := readAt
				switch relation {
				case "before":
					cutoff = readAt.Add(time.Nanosecond)
				case "after":
					cutoff = readAt.Add(-time.Nanosecond)
				}
				if _, err := s.Session(domain.WithFreshSince(context.Background(), cutoff)); err != nil {
					t.Fatal(err)
				}
				want := 1
				if relation == "before" {
					want = 2
				}
				if b.count("Repository") != want || b.count("Viewer") != want {
					t.Fatal("wrong freshness boundary")
				}
			})
		}
	}
}

func TestStructure_FreshNestedReadsReachEachBackendOnce(t *testing.T) {
	for _, files := range []bool{false, true} {
		t.Run(map[bool]string{false: "no-store", true: "file-store"}[files], func(t *testing.T) {
			for _, command := range []string{"list", "board", "create"} {
				t.Run(command, func(t *testing.T) {
					_, b := writeFixture()
					var m CacheStore
					if files {
						m = &memoryCache{}
					}
					c := newCacheClock()
					s := cachedCore(b, m, c)
					warmStructure(t, s, context.Background())
					names := []string{"Repository", "Viewer", "LinkedProjects", "ProjectFields", "Labels", "AssignableUsers"}
					before := map[string]int{}
					for _, name := range names {
						before[name] = b.count(name)
					}
					c.Add(time.Nanosecond)
					ctx := domain.WithFreshSince(context.Background(), c.Now())
					var err error
					switch command {
					case "list":
						for _, cursor := range []string{"", "page-two", "page-three"} {
							_, err = s.ListIssues(ctx, domain.IssueFilter{}, domain.PageRequest{Cursor: cursor})
							if err != nil {
								t.Fatal(err)
							}
						}
					case "board":
						_, err = s.Board(ctx)
					case "create":
						status := "Todo"
						_, err = s.CreateIssue(ctx, domain.NewIssue{Title: "test", Assignees: []string{"test"}, Labels: []string{"bug"}, Status: &status})
					}
					if err != nil {
						t.Fatal(err)
					}
					for _, name := range names {
						want := 1
						if (name == "Labels" || name == "AssignableUsers") && command != "create" {
							want = 0
						}
						if got := b.count(name) - before[name]; got != want {
							t.Errorf("%s: got %d backend reads, want %d", name, got, want)
						}
					}
				})
			}
		})
	}
}

func TestStructure_NoFileStoreKeepsOriginalMemoRequestCounts(t *testing.T) {
	b := cacheBackend()
	c := newCacheClock()
	s := cachedCore(b, nil, c)
	ctx := domain.WithFreshSince(context.Background(), c.Now())
	for range 2 {
		if _, err := s.ListIssues(ctx, domain.IssueFilter{}, domain.PageRequest{}); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"Repository", "Viewer", "LinkedProjects", "ProjectFields"} {
		if b.count(name) != 1 {
			t.Fatal("nil store disabled memo", name)
		}
	}
	if b.count("ListIssues") != 2 || b.count("CredentialKey") != 0 {
		t.Fatal("off did extra work")
	}
}

func TestStructure_DoctorReadsEachStructureOnce(t *testing.T) {
	b := cacheBackend()
	s := cachedCore(b, &memoryCache{}, newCacheClock())
	if _, err := s.Doctor(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Repository", "Viewer", "LinkedProjects", "ProjectFields", "ProjectItems", "DoctorScopes"} {
		if b.count(name) != 1 {
			t.Fatal("doctor repeated a read", name, b.count(name))
		}
	}
}

func TestStructure_CacheCannotChooseTheReadError(t *testing.T) {
	for _, failure := range []string{"credentials", "repository", "both"} {
		t.Run(failure, func(t *testing.T) {
			for _, files := range []bool{false, true} {
				b := cacheBackend()
				if failure != "repository" {
					b.fail["CredentialKey"] = &domain.Error{Kind: domain.ErrAuth, Message: "no credentials"}
				}
				var m CacheStore
				if files {
					m = &memoryCache{}
				}
				s := cachedCore(b, m, newCacheClock())
				if failure != "credentials" {
					s.opts.ResolveRepository = func(context.Context) (domain.RepoRef, error) {
						return domain.RepoRef{}, &domain.Error{Kind: domain.ErrNoRepo, Message: "outside a repository", Hint: "use --repo OWNER/REPO"}
					}
				} else {
					b.fail["Repository"] = &domain.Error{Kind: domain.ErrAuth, Message: "ordinary repository read needs credentials"}
				}
				_, err := s.Session(context.Background())
				if failure == "credentials" {
					de := assertKind(t, err, domain.ErrAuth)
					if de.Message != "ordinary repository read needs credentials" {
						t.Fatal(de)
					}
				} else {
					de := assertKind(t, err, domain.ErrNoRepo)
					if de.Message != "outside a repository" || de.Hint != "use --repo OWNER/REPO" {
						t.Fatal(de)
					}
				}
			}
		})
	}
}

func TestStructure_SessionFromAnotherRepositoryIsMiss(t *testing.T) {
	b := cacheBackend()
	m := &memoryCache{}
	c := newCacheClock()
	s := cachedCore(b, m, c)
	space := s.structureSpace(context.Background())
	other := domain.Session{Repo: b.repo, Viewer: b.viewer}
	other.Repo.Ref.Name = "another"
	data, err := json.Marshal(other)
	if err != nil {
		t.Fatal(err)
	}
	m.Put(space, "session", data, c.Now())
	got, err := s.Session(context.Background())
	if err != nil || got.Repo.Ref != b.repo.Ref || b.count("Repository") != 1 {
		t.Fatal("cross-repository session accepted", got, err)
	}
}

func TestStructure_RepositoryCaseSharesSpace(t *testing.T) {
	b := cacheBackend()
	m := &memoryCache{}
	c := newCacheClock()
	upper := cachedCore(b, m, c)
	upper.opts.ResolveRepository = func(context.Context) (domain.RepoRef, error) {
		return domain.RepoRef{Host: "GITHUB.COM", Owner: "TEST", Name: "REPO"}, nil
	}
	if space := upper.structureSpace(context.Background()); space != strings.ToLower(space) {
		t.Fatal("repository spelling leaked into space", space)
	}
	warmStructure(t, upper, context.Background())
	lower := cachedCore(b, m, c)
	warmStructure(t, lower, context.Background())
	if upper.cacheSpace != lower.cacheSpace || upper.cacheSpace != strings.ToLower(upper.cacheSpace) {
		t.Fatal("case changed the space", upper.cacheSpace, lower.cacheSpace)
	}
	for _, name := range []string{"Repository", "Viewer", "Labels", "AssignableUsers", "ProjectFields"} {
		if b.count(name) != 1 {
			t.Fatal("case lost cached metadata", name, b.count(name))
		}
	}
}

func TestStructure_RenamedRepositoryUsesCanonicalSpace(t *testing.T) {
	b := cacheBackend()
	m := &memoryCache{}
	c := newCacheClock()
	s := cachedCore(b, m, c)
	s.opts.ResolveRepository = func(context.Context) (domain.RepoRef, error) {
		return domain.RepoRef{Host: "github.com", Owner: "test", Name: "old-name"}, nil
	}
	warmStructure(t, s, context.Background())
	warmStructure(t, s, context.Background())
	if !strings.HasSuffix(s.cacheSpace, "/structure/github.com/test/repo") {
		t.Fatal("noncanonical space", s.cacheSpace)
	}
	for compound, value := range m.values {
		if strings.Contains(compound, "/old-name|") {
			t.Fatal("wrote an unusable alias entry", compound)
		}
		if strings.HasSuffix(compound, "|session") {
			var session domain.Session
			if json.Unmarshal(value.data, &session) != nil || !metadataIdentity(session, strings.TrimSuffix(compound, "|session")) {
				t.Fatal("session rejects its own space")
			}
		}
	}
	canonical := cachedCore(b, m, c)
	warmStructure(t, canonical, context.Background())
	for _, name := range []string{"Repository", "Viewer", "Labels", "AssignableUsers", "ProjectFields"} {
		if b.count(name) != 1 {
			t.Fatal("canonical metadata not reused", name, b.count(name))
		}
	}
}

func TestStructure_QuickRunRefusalDropsBothMemos(t *testing.T) {
	s, b, _ := quickFixture()
	s.opts.Cache = &memoryCache{}
	ctx := context.Background()
	warmStructure(t, s, ctx)
	run := s.quickCore(nil)
	// The label disappears after planning but before the first create.
	b.labels = []domain.Label{}
	b.failures["CreateIssue.1"] = &domain.Error{Kind: domain.ErrInvalidInput, Message: "label was deleted"}
	_, err := run.CreateIssue(ctx, domain.NewIssue{Title: "one", Labels: []string{"bug"}})
	assertKind(t, err, domain.ErrInvalidInput)
	if s.labels.Valid || run.labels.Valid {
		t.Fatal("refusal retained a parent or run memo")
	}
	labels, err := run.Labels(ctx)
	if err != nil || len(labels) != 0 || b.count("Labels") != 2 {
		t.Fatal("run reused the deleted label", labels, err)
	}
	_, err = run.CreateIssue(ctx, domain.NewIssue{Title: "two", Labels: []string{"bug"}})
	assertKind(t, err, domain.ErrInvalidInput)
	if b.counts["CreateIssue"] != 1 {
		t.Fatal("sent a stale label ID after refusal")
	}
}
