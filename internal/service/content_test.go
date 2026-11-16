package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pkieltyka/todos/internal/domain"
)

type contentBackend struct {
	Backend
	base              *fakeBackend
	mu                sync.Mutex
	token             domain.ChangeToken
	page              domain.Page[domain.Issue]
	probeErr, readErr error
	probeHook         func()
	started, release  chan struct{}
}

func (b *contentBackend) ChangeToken(ctx context.Context, _ domain.RepoRef, project domain.NodeID) (domain.ChangeToken, error) {
	b.base.record(backendCall{Name: "ChangeToken", Project: project})
	b.mu.Lock()
	token, err, hook := b.token, b.probeErr, b.probeHook
	b.mu.Unlock()
	if hook != nil {
		hook()
	}
	return token, err
}
func (b *contentBackend) ListIssues(ctx context.Context, _ domain.RepoRef, f domain.IssueFilter, p domain.PageRequest, project domain.NodeID) (domain.Page[domain.Issue], error) {
	b.base.record(backendCall{Name: "ListIssues", Filter: f, Page: p, Project: project})
	b.mu.Lock()
	page, err, started, release := cloneIssuePage(b.page), b.readErr, b.started, b.release
	b.mu.Unlock()
	if started != nil {
		close(started)
		select {
		case <-ctx.Done():
			return domain.Page[domain.Issue]{}, ctx.Err()
		case <-release:
		}
	}
	return page, err
}
func contentFixture(t *testing.T) (*core, *contentBackend, *memoryCache, *cacheClock) {
	t.Helper()
	contentDisabled.Store(false)
	t.Cleanup(func() { contentDisabled.Store(false) })
	c := newCacheClock()
	base := cacheBackend()
	b := &contentBackend{Backend: base, base: base, token: domain.ChangeToken{Viewer: "test", IssuesTotal: 1, NewestIssue: 1, NewestUpdated: c.Now().Add(-time.Minute), ProjectUpdated: c.Now().Add(-time.Minute), ProjectItems: 1, ServerTime: c.Now()}, page: domain.Page[domain.Issue]{Items: []domain.Issue{{Number: 1, NodeID: "I1", Title: "original", State: "open"}}, Total: 1}}
	m := &memoryCache{}
	return cachedCore(b, m, c), b, m, c
}
func readContent(t *testing.T, s *core) domain.Page[domain.Issue] {
	t.Helper()
	p, err := s.ListIssues(context.Background(), domain.IssueFilter{}, domain.PageRequest{Size: 50})
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func contentEntry(t *testing.T, m *memoryCache) (string, string, cacheValue) {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	for compound, value := range m.values {
		space, key, ok := strings.Cut(compound, "|")
		if ok && strings.HasSuffix(space, "/content") {
			return space, key, value
		}
	}
	t.Fatal("no stored content")
	return "", "", cacheValue{}
}
func TestContent_UnchangedTokenServesOldPageAndOriginalAsOf(t *testing.T) {
	s, b, _, c := contentFixture(t)
	at := c.Now()
	first := readContent(t, s)
	if !first.AsOf.IsZero() {
		t.Fatal("live page has a cache timestamp")
	}
	c.Add(time.Second)
	b.page.Items[0].Title = "changed elsewhere, timestamp still old"
	second := readContent(t, s)
	if second.Items[0].Title != "original" || !second.AsOf.Equal(at) || b.base.count("ListIssues") != 1 || b.base.count("ChangeToken") != 2 {
		t.Fatal("accepted lag or probe count changed", second)
	}
	second.Items[0].Title = "caller mutation"
	if readContent(t, s).Items[0].Title != "original" {
		t.Fatal("cached value aliases caller")
	}
}
func TestContent_EachIdentityDimensionMisses(t *testing.T) {
	for _, dimension := range []string{"token", "viewer", "age", "epoch", "credential", "state", "assignee", "label", "size", "cursor", "board", "repository"} {
		t.Run(dimension, func(t *testing.T) {
			s, b, m, c := contentFixture(t)
			readContent(t, s)
			f, p := domain.IssueFilter{}, domain.PageRequest{Size: 50}
			switch dimension {
			case "token":
				b.token.NewestUpdated = b.token.NewestUpdated.Add(time.Second)
			case "viewer":
				b.token.Viewer = "other"
			case "age":
				c.Add(10 * time.Minute)
			case "epoch":
				space, _, _ := contentEntry(t, m)
				m.Bump(space)
			case "credential":
				b.base.credentialKey = "another-credential"
			case "state":
				f.State = "closed"
			case "assignee":
				f.Assignee = "alice"
			case "label":
				f.Labels = []string{"bug"}
			case "size":
				p.Size = 49
			case "cursor":
				p.Cursor = "page-two"
			case "board":
				s.opts.Project = "2" // unavailable: issue read falls back to no board
			case "repository":
				b.base.repo.Ref.Name = "another"
				s.opts.ResolveRepository = func(context.Context) (domain.RepoRef, error) { return b.base.repo.Ref, nil }
			}
			page, err := s.ListIssues(context.Background(), f, p)
			if err != nil || !page.AsOf.IsZero() || b.base.count("ListIssues") != 2 {
				t.Fatal("identity reused another page", page, err)
			}
		})
	}
}
func TestContent_NormalizedKeysAndEachPageProbes(t *testing.T) {
	s, b, _, _ := contentFixture(t)
	for index, f := range []domain.IssueFilter{{Assignee: "@me", Labels: []string{"z", "a", "z"}}, {State: "open", Assignee: "TEST", Labels: []string{"a", "z"}}} {
		size := 0
		if index == 1 {
			size = 100
		}
		if _, err := s.ListIssues(context.Background(), f, domain.PageRequest{Size: size}); err != nil {
			t.Fatal(err)
		}
	}
	if b.base.count("ListIssues") != 1 || b.base.count("ChangeToken") != 2 {
		t.Fatal("effective filter was not normalized")
	}
	for range 2 {
		if _, err := s.ListIssues(context.Background(), domain.IssueFilter{Assignee: "test", Labels: []string{"a", "z"}}, domain.PageRequest{Size: 100, Cursor: "next"}); err != nil {
			t.Fatal(err)
		}
	}
	if b.base.count("ChangeToken") != 4 || b.base.count("ListIssues") != 2 {
		t.Fatal("cursor used remembered evidence")
	}
}
func TestContent_ProbeFailuresReadLiveAndNeverStore(t *testing.T) {
	for _, failure := range []domain.ErrorKind{domain.ErrNetwork, domain.ErrTimeout, domain.ErrAuth, domain.ErrRateLimited, domain.ErrMissingScope, domain.ErrForbidden} {
		t.Run(string(failure), func(t *testing.T) {
			s, b, m, _ := contentFixture(t)
			readContent(t, s)
			b.probeErr = &domain.Error{Kind: failure, Message: "probe failed"}
			b.page.Items[0].Title = "live"
			before := m.puts
			p := readContent(t, s)
			if !p.AsOf.IsZero() || p.Items[0].Title != "live" || m.puts != before {
				t.Fatal("probe error hid the live read", p)
			}
			b.readErr = &domain.Error{Kind: domain.ErrNotFound, Message: "ordinary read"}
			_, err := s.ListIssues(context.Background(), domain.IssueFilter{}, domain.PageRequest{})
			de := assertKind(t, err, domain.ErrNotFound)
			if de.Message != "ordinary read" {
				t.Fatal(de)
			}
		})
	}
}
func TestContent_QuietRule(t *testing.T) {
	for _, condition := range []string{"missing-date", "missing-updated", "future-issue", "future-project", "recent-issue", "recent-project", "ancient-issue", "ancient-project", "exact-five-seconds", "no-board"} {
		t.Run(condition, func(t *testing.T) {
			s, b, m, c := contentFixture(t)
			switch condition {
			case "missing-date":
				b.token.ServerTime = time.Time{}
			case "missing-updated":
				b.token.NewestUpdated = time.Time{}
			case "future-issue":
				b.token.NewestUpdated = c.Now().Add(time.Second)
			case "future-project":
				b.token.ProjectUpdated = c.Now().Add(time.Second)
			case "recent-issue":
				b.token.NewestUpdated = c.Now().Add(-4 * time.Second)
			case "recent-project":
				b.token.ProjectUpdated = c.Now().Add(-4 * time.Second)
			case "ancient-issue":
				b.token.NewestUpdated = c.Now().Add(-11 * 365 * 24 * time.Hour)
			case "ancient-project":
				b.token.ProjectUpdated = c.Now().Add(-11 * 365 * 24 * time.Hour)
			case "exact-five-seconds":
				b.token.NewestUpdated = c.Now().Add(-5 * time.Second)
				b.token.ProjectUpdated = b.token.NewestUpdated
			case "no-board":
				b.base.linked = nil
				b.token.ProjectUpdated = time.Time{}
			}
			readContent(t, s)
			readContent(t, s)
			want := condition == "exact-five-seconds" || condition == "no-board"
			m.mu.Lock()
			n := 0
			for key := range m.values {
				if strings.Contains(key, "/content|") {
					n++
				}
			}
			m.mu.Unlock()
			if (n > 0) != want || b.base.count("ListIssues") != map[bool]int{true: 1, false: 2}[want] {
				t.Fatal("wrong quiet decision", condition, n)
			}
		})
	}
}
func TestContent_FreshSearchAndOffSkipProbeAndStorage(t *testing.T) {
	for _, mode := range []string{"fresh", "search", "off"} {
		t.Run(mode, func(t *testing.T) {
			s, b, m, c := contentFixture(t)
			readContent(t, s)
			ctx := context.Background()
			f := domain.IssueFilter{}
			switch mode {
			case "fresh":
				ctx = domain.WithFreshSince(ctx, c.Now())
			case "search":
				f.Search = "title"
			case "off":
				s.opts.Cache = nil
			}
			before := m.puts
			for range 2 {
				p, err := s.ListIssues(ctx, f, domain.PageRequest{Size: 50})
				if err != nil || !p.AsOf.IsZero() {
					t.Fatal(p, err)
				}
			}
			if m.puts != before || b.base.count("ChangeToken") != 1 || b.base.count("ListIssues") != 3 {
				t.Fatal("bypass used content storage/probe")
			}
		})
	}
}
func TestContent_CorruptPayloadsAndFutureReadsMiss(t *testing.T) {
	for _, mode := range []string{"json", "nil-items", "missing-id", "missing-number", "bad-total", "bad-cursor", "future", "zero-time"} {
		t.Run(mode, func(t *testing.T) {
			s, b, m, c := contentFixture(t)
			readContent(t, s)
			space, key, v := contentEntry(t, m)
			var stored storedIssues
			json.Unmarshal(v.data, &stored)
			switch mode {
			case "json":
				v.data = []byte("{")
			case "nil-items":
				stored.Page.Items = nil
			case "missing-id":
				stored.Page.Items[0].NodeID = ""
			case "missing-number":
				stored.Page.Items[0].Number = 0
			case "zero-time":
				v.at = time.Time{}
			case "bad-total":
				stored.Page.Total = -1
			case "bad-cursor":
				stored.Page.HasMore = true
			case "future":
				v.at = c.Now().Add(time.Second)
			}
			if mode != "json" {
				v.data, _ = json.Marshal(stored)
			}
			m.Put(space, key, v.data, v.at)
			if p := readContent(t, s); !p.AsOf.IsZero() || b.base.count("ListIssues") != 2 {
				t.Fatal("bad stored data used", mode)
			}
		})
	}
}
func TestContent_CancelledProbeDoesNotReadOrServe(t *testing.T) {
	s, b, m, _ := contentFixture(t)
	readContent(t, s)
	ctx, cancel := context.WithCancel(context.Background())
	b.probeHook = cancel
	before := m.puts
	_, err := s.ListIssues(ctx, domain.IssueFilter{}, domain.PageRequest{Size: 50})
	if !errors.Is(err, context.Canceled) || b.base.count("ListIssues") != 1 || m.puts != before {
		t.Fatal("cancelled probe did more work", err)
	}
}
func TestContent_EpochChangesDuringProbeCannotHit(t *testing.T) {
	s, b, m, _ := contentFixture(t)
	readContent(t, s)
	space, _, _ := contentEntry(t, m)
	b.probeHook = func() { m.Bump(space) }
	if p := readContent(t, s); !p.AsOf.IsZero() || b.base.count("ListIssues") != 2 {
		t.Fatal("served across concurrent epoch bump")
	}
}
func TestContent_CredentialChangesDuringReadNeverPersistInOldSpace(t *testing.T) {
	s, b, m, c := contentFixture(t)
	b.started, b.release = make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := s.ListIssues(context.Background(), domain.IssueFilter{}, domain.PageRequest{Size: 50})
		done <- err
	}()
	<-b.started
	b.base.credentialKey = "new-credential"
	c.Add(time.Second)
	close(b.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for key := range m.values {
		if strings.Contains(key, "/content|") {
			t.Fatal("stored in old credential partition")
		}
	}
}

func TestContent_EveryChangeBumpsBeforeSendAndOnReturn(t *testing.T) {
	verbs := map[string]func(*core) error{
		"create": func(s *core) error {
			_, e := s.CreateIssue(context.Background(), domain.NewIssue{Title: "new"})
			return e
		},
		"edit": func(s *core) error {
			_, e := s.EditIssue(context.Background(), 1, domain.EditIssue{Title: str("edited")})
			return e
		},
		"assign": func(s *core) error {
			_, e := s.UpdateAssignees(context.Background(), 1, []string{"alice"}, []string{"test"})
			return e
		},
		"reassign": func(s *core) error { _, e := s.Reassign(context.Background(), 1, []string{"alice"}); return e },
		"label": func(s *core) error {
			_, e := s.UpdateLabels(context.Background(), 1, []string{"docs"}, []string{"bug"})
			return e
		},
		"comment": func(s *core) error { _, e := s.Comment(context.Background(), 1, "comment"); return e },
		"close": func(s *core) error {
			_, e := s.Close(context.Background(), 1, domain.CloseOptions{Reason: "completed"})
			return e
		},
		"reopen-noop":       func(s *core) error { _, e := s.Reopen(context.Background(), 1); return e },
		"delete":            func(s *core) error { return s.Delete(context.Background(), 1) },
		"status":            func(s *core) error { _, e := s.SetStatus(context.Background(), 1, str("Done")); return e },
		"board-add":         func(s *core) error { _, e := s.BoardAdd(context.Background(), 1, str("Todo")); return e },
		"board-remove-noop": func(s *core) error { _, e := s.RemoveFromBoard(context.Background(), 1); return e },
	}
	for name, call := range verbs {
		t.Run(name, func(t *testing.T) {
			_, b := writeFixture()
			m := &memoryCache{}
			s := cachedCore(b, m, newCacheClock())
			space := strings.Repeat("a", 64) + "/content"
			before := m.Epoch(space)
			b.onCall = func(c backendCall) {
				if c.Name != "CredentialKey" && m.Epoch(space) == before {
					t.Fatal("sent before bump", name, c.Name)
				}
			}
			if err := call(s); err != nil {
				t.Fatal(err)
			}
			if m.bumps != 2 {
				t.Fatal("missing entry/return bump", name, m.bumps)
			}
		})
	}
}
func TestContent_UncertainWriteBumpsWithoutRetryAndInvalidatesAllRepositories(t *testing.T) {
	s, b, m, c := contentFixture(t)
	readContent(t, s)
	space, key, v := contentEntry(t, m)
	_, wb := writeFixture()
	wb.repo.Ref.Name = "other"
	wb.fail["CreateIssue"] = &domain.Error{Kind: domain.ErrTimeout, Message: "unknown", Uncertain: true}
	writer := cachedCore(wb, m, c)
	writer.opts.ResolveRepository = func(context.Context) (domain.RepoRef, error) { r := wb.repo.Ref; r.Name = "other"; return r, nil }
	_, err := writer.CreateIssue(context.Background(), domain.NewIssue{Title: "write in another repository"})
	if de := assertKind(t, err, domain.ErrTimeout); !de.Uncertain || wb.count("CreateIssue") != 1 {
		t.Fatal("uncertain write replayed", err)
	}
	var stored storedIssues
	json.Unmarshal(v.data, &stored)
	if stored.Epoch == m.Epoch(space) || m.bumps != 2 {
		t.Fatal("write left pre-write epoch current")
	}
	if _, _, ok := m.Get(space, key); !ok {
		t.Fatal("epoch should invalidate, not require deleting pages")
	}
	if p := readContent(t, s); !p.AsOf.IsZero() || b.base.count("ListIssues") != 2 {
		t.Fatal("other repository's write did not invalidate")
	}
}
func TestContent_FailedBumpDisablesStoredContentProcessWide(t *testing.T) {
	s, b, m, c := contentFixture(t)
	readContent(t, s)
	_, wb := writeFixture()
	writer := cachedCore(wb, m, c)
	m.failBump = true
	if _, err := writer.CreateIssue(context.Background(), domain.NewIssue{Title: "write"}); err != nil {
		t.Fatal(err)
	}
	m.failBump = false
	// Even after a subsequent successful write, no service uses stored content.
	if _, err := writer.CreateIssue(context.Background(), domain.NewIssue{Title: "second"}); err != nil {
		t.Fatal(err)
	}
	before := m.puts
	p := readContent(t, s)
	other := cachedCore(b, m, c)
	readContent(t, other)
	if !p.AsOf.IsZero() || b.base.count("ListIssues") != 3 || b.base.count("ChangeToken") != 1 || m.puts != before {
		t.Fatal("failed bump reused or refilled content")
	}
}
func TestContent_QuickAddBumpsRunAndEachSettledItem(t *testing.T) {
	for _, mode := range []string{"success", "uncertain", "dry-run", "all-skipped", "callback-error"} {
		t.Run(mode, func(t *testing.T) {
			s, b, _ := quickFixture()
			m := &memoryCache{}
			s.opts.Cache = m
			text := "one\ntwo"
			opts := domain.QuickAddRunOptions{}
			switch mode {
			case "uncertain":
				b.failures["CreateIssue.1"] = &domain.Error{Kind: domain.ErrTimeout, Message: "unknown", Uncertain: true}
			case "dry-run":
				opts.DryRun = true
			case "all-skipped":
				text = "- [x] done"
			}
			var settled []int
			opts.OnProgress = func(p domain.QuickAddProgress) error {
				if mode == "callback-error" && p.Phase == "creating" {
					return errors.New("cannot save")
				}
				if p.Phase == "settled" {
					settled = append(settled, m.bumps)
				}
				return nil
			}
			_, err := s.RunQuickAdd(context.Background(), quickPlan(text, domain.QuickAddOptions{}), opts)
			if mode == "callback-error" {
				assertKind(t, err, domain.ErrConfig)
				if m.bumps != 0 {
					t.Fatal("no write began", m.bumps)
				}
				return
			}
			if mode == "uncertain" {
				if !assertKind(t, err, domain.ErrPartial).Uncertain || b.counts["CreateIssue"] != 1 {
					t.Fatal(err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if mode == "dry-run" || mode == "all-skipped" {
				if m.bumps != 0 {
					t.Fatal("read-only batch bumped epochs", m.bumps)
				}
				return
			}
			if len(settled) == 0 || settled[0] < 4 || m.bumps != settled[len(settled)-1]+1 {
				t.Fatal("missing run/item/return bump", settled, m.bumps)
			}
		})
	}
}
func TestContent_ReadAtIsBeforeTheLivePageRequest(t *testing.T) {
	s, b, m, c := contentFixture(t)
	at := c.Now()
	b.started, b.release = make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := s.ListIssues(context.Background(), domain.IssueFilter{}, domain.PageRequest{Size: 50})
		done <- err
	}()
	<-b.started
	c.Add(time.Minute)
	close(b.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	_, _, v := contentEntry(t, m)
	if !v.at.Equal(at) {
		t.Fatal("stored completion time", v.at)
	}
}

func TestContent_PartitionChangesDuringProbeCannotHitOrFill(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(fmt.Sprint(missing), func(t *testing.T) {
			s, b, m, _ := contentFixture(t)
			readContent(t, s)
			before := m.puts
			b.probeHook = func() {
				if missing {
					b.base.fail["CredentialKey"] = &domain.Error{Kind: domain.ErrAuth, Message: "credential lookup failed"}
				} else {
					b.base.credentialKey = "new-credential"
				}
			}
			p := readContent(t, s)
			if !p.AsOf.IsZero() || b.base.count("ListIssues") != 2 || m.puts != before {
				t.Fatal("used old credential's evidence", p)
			}
		})
	}
}

type changingFields struct {
	Backend
	hook func()
}

func (b changingFields) ProjectFields(ctx context.Context, id domain.NodeID) (domain.BoardInfo, error) {
	if b.hook != nil {
		b.hook()
	}
	return b.Backend.ProjectFields(ctx, id)
}
func TestBoard_FieldRefreshCannotPublishAcrossMetadataInvalidation(t *testing.T) {
	s, b, m, _ := contentFixture(t)
	if _, err := s.BoardInfo(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Simulate RefreshMetadata invalidating while ProjectFields was in flight.
	b.Backend = changingFields{Backend: b.base, hook: func() { s.dropStructure() }}
	before := m.puts
	if _, err := s.Board(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.board.Valid || m.puts != before {
		t.Fatal("in-flight fields republished invalidated metadata")
	}
}

func TestContent_UnusableProbeClockCannotServeWarmPage(t *testing.T) {
	for _, mode := range []string{"missing-date", "future-issue", "future-project"} {
		t.Run(mode, func(t *testing.T) {
			s, b, m, c := contentFixture(t)
			readContent(t, s)
			before := m.puts
			switch mode {
			case "missing-date":
				b.token.ServerTime = time.Time{}
			case "future-issue":
				b.token.ServerTime = b.token.NewestUpdated.Add(-time.Second)
			case "future-project":
				b.token.ServerTime = b.token.ProjectUpdated.Add(-time.Second)
			}
			c.Add(time.Second)
			p := readContent(t, s)
			if !p.AsOf.IsZero() || b.base.count("ListIssues") != 2 || m.puts != before {
				t.Fatal("unusable probe clock vouched for cached page", p)
			}
		})
	}
}

func TestContent_CredentialLookupFailureOnWriteDisablesContent(t *testing.T) {
	s, b, m, c := contentFixture(t)
	readContent(t, s)
	_, wb := writeFixture()
	wb.fail["CredentialKey"] = &domain.Error{Kind: domain.ErrAuth, Message: "key unavailable"}
	writer := cachedCore(wb, m, c)
	if _, err := writer.CreateIssue(context.Background(), domain.NewIssue{Title: "write"}); err != nil {
		t.Fatal("cache-key error failed ordinary mutation", err)
	}
	if p := readContent(t, s); !p.AsOf.IsZero() || b.base.count("ChangeToken") != 1 || b.base.count("ListIssues") != 2 {
		t.Fatal("old content used after unpartitioned write")
	}
}

func TestContent_EpochIsReadBeforeTheGatedProbe(t *testing.T) {
	s, b, m, c := contentFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	b.probeHook = func() { close(entered); <-release }
	done := make(chan error, 1)
	go func() {
		_, err := s.ListIssues(context.Background(), domain.IssueFilter{}, domain.PageRequest{Size: 50})
		done <- err
	}()
	<-entered
	_, wb := writeFixture()
	writer := cachedCore(wb, m, c)
	if _, err := writer.CreateIssue(context.Background(), domain.NewIssue{Title: "new"}); err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	b.page.Items[0].Title = "read after the write"
	b.mu.Unlock()
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	b.probeHook = nil
	space, _, entry := contentEntry(t, m)
	var stored storedIssues
	if err := json.Unmarshal(entry.data, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Epoch == m.Epoch(space) {
		t.Fatal("in-flight probe acquired the write's newer epoch")
	}
	if page := readContent(t, s); !page.AsOf.IsZero() || b.base.count("ListIssues") != 2 {
		t.Fatal("page became usable under a post-probe epoch", page)
	}
}

func TestContent_FailedBumpDropsPagesForOtherProcesses(t *testing.T) {
	s, _, m, c := contentFixture(t)
	readContent(t, s)
	space, key, _ := contentEntry(t, m)
	if _, _, ok := m.Get(s.cacheSpace, "session"); !ok {
		t.Fatal("session was not persisted")
	}
	_, wb := writeFixture()
	writer := cachedCore(wb, m, c)
	m.failBump = true
	if _, err := writer.CreateIssue(context.Background(), domain.NewIssue{Title: "write"}); err != nil {
		t.Fatal(err)
	}
	// Direct storage access models another process, which has no disable flag.
	if _, _, ok := m.Get(space, key); ok {
		t.Fatal("other process can still read a pre-write page")
	}
	if _, _, ok := m.Get(s.cacheSpace, "session"); !ok {
		t.Fatal("content drop removed structure too")
	}
}

type cancellingEpoch struct {
	CacheStore
	calls  int
	cancel context.CancelFunc
}

func (c *cancellingEpoch) Epoch(space string) string {
	c.calls++
	epoch := c.CacheStore.Epoch(space)
	if c.calls == 2 {
		c.cancel()
	} // final validation, immediately before hit return
	return epoch
}
func TestContent_CancellationAtHitReturnAborts(t *testing.T) {
	s, b, m, _ := contentFixture(t)
	readContent(t, s)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := &cancellingEpoch{CacheStore: m, cancel: cancel}
	s.opts.Cache = store
	_, err := s.ListIssues(ctx, domain.IssueFilter{}, domain.PageRequest{Size: 50})
	if !errors.Is(err, context.Canceled) || store.calls != 2 || b.base.count("ListIssues") != 1 {
		t.Fatal("cancelled hit was returned or read again", err, store.calls)
	}
}
func TestContent_PageKeyFoldsRepositoryCase(t *testing.T) {
	upper := domain.RepoRef{Host: "GITHUB.COM", Owner: "Test", Name: "Repo"}
	lower := domain.RepoRef{Host: "github.com", Owner: "test", Name: "repo"}
	f, p := domain.IssueFilter{State: "all"}, domain.PageRequest{Size: 50}
	if issuePageKey(upper, f, p, "P1") != issuePageKey(lower, f, p, "P1") {
		t.Fatal("repository case split the cache key")
	}
	lower.Name = "another"
	if issuePageKey(upper, f, p, "P1") == issuePageKey(lower, f, p, "P1") {
		t.Fatal("different repository shares a key")
	}
}

func TestBoard_FieldsLiveExactlyOnceAndSharedWithMetadata(t *testing.T) {
	for _, disk := range []bool{false, true} {
		t.Run(map[bool]string{false: "warm-memo", true: "warm-file"}[disk], func(t *testing.T) {
			s, b, m, c := contentFixture(t)
			if _, err := s.BoardInfo(context.Background()); err != nil {
				t.Fatal(err)
			}
			if disk {
				s = cachedCore(b, m, c)
			}
			for _, name := range []string{"New column", "Renamed column"} {
				info := b.base.fields["P1"]
				info.Options = []domain.StatusOption{{ID: "new", Name: name}}
				b.base.fields["P1"] = info
				b.base.items = []domain.ProjectItem{{Kind: "Issue", RepositoryID: "R1", Issue: &domain.Issue{Number: 1, NodeID: "I1"}, Placement: domain.Placement{ItemID: "ITEM", OptionID: "new"}}}
				beforeFields, beforeItems := b.base.count("ProjectFields"), b.base.count("ProjectItems")
				c.Add(time.Second)
				board, err := s.Board(context.Background())
				if err != nil || len(board.Columns) != 1 || board.Columns[0].Status == nil || *board.Columns[0].Status != name || len(board.Columns[0].Cards) != 1 {
					t.Fatal("columns not built from live fields", board, err)
				}
				if b.base.count("ProjectFields") != beforeFields+1 || b.base.count("ProjectItems") != beforeItems+1 || b.base.count("ChangeToken") != 0 {
					t.Fatal("board must read fields/items once and never probe")
				}
				// Both the local picker and another command must see these options.
				for _, reader := range []*core{s, cachedCore(b, m, c)} {
					got, err := reader.BoardInfo(context.Background())
					if err != nil || len(got.Options) != 1 || got.Options[0].Name != name || b.base.count("ProjectFields") != beforeFields+1 {
						t.Fatal("live fields not shared with memo/file", got, err)
					}
				}
			}
		})
	}
}
func TestBoard_ColdCallReadsFieldsOnlyOnce(t *testing.T) {
	s, b, _, _ := contentFixture(t)
	if _, err := s.Board(context.Background()); err != nil {
		t.Fatal(err)
	}
	if b.base.count("ProjectFields") != 1 || b.base.count("ProjectItems") != 1 {
		t.Fatal("cold board read fields twice")
	}
}
func TestBoard_LiveFieldsFailureDoesNotReadCards(t *testing.T) {
	s, b, _, _ := contentFixture(t)
	if _, err := s.BoardInfo(context.Background()); err != nil {
		t.Fatal(err)
	}
	b.base.fail["ProjectFields"] = &domain.Error{Kind: domain.ErrForbidden, Message: "fields refused"}
	_, err := s.Board(context.Background())
	assertKind(t, err, domain.ErrForbidden)
	if b.base.count("ProjectFields") != 2 || b.base.count("ProjectItems") != 0 {
		t.Fatal("failed live fields read was retried or cards fetched")
	}
}
func TestStructure_ListReusesBoardForFiveMinutesThenExpires(t *testing.T) {
	for _, files := range []bool{false, true} {
		t.Run(fmt.Sprint(files), func(t *testing.T) {
			s, b, _, c := contentFixture(t)
			if !files {
				s.opts.Cache = nil
			}
			readContent(t, s)
			c.Add(5*time.Minute - time.Nanosecond)
			readContent(t, s)
			if b.base.count("LinkedProjects") != 1 || b.base.count("ProjectFields") != 1 {
				t.Fatal("list re-read board structure within five minutes")
			}
			c.Add(time.Nanosecond)
			readContent(t, s)
			if b.base.count("LinkedProjects") != 2 || b.base.count("ProjectFields") != 2 {
				t.Fatal("board structure did not expire at five minutes")
			}
		})
	}
}
