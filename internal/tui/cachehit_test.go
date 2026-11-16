package tui

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/pkieltyka/todos/internal/domain"
	"github.com/pkieltyka/todos/internal/service"
	"github.com/pkieltyka/todos/internal/service/servicetest"
)

// Issue list pages can come from the service's read cache, stamped with the
// time they were really read (domain.Page.AsOf, cache.md 5). These tests wrap
// the fake so that list pages can be served as such hits.

// listCacheSim stands in for the service's read cache of issue list pages.
// While record is on, live pages are kept by cursor; while serve is on, kept
// pages are returned, stamped with asOf, without reaching the fake.
type listCacheSim struct {
	mu     sync.Mutex
	record bool
	serve  bool
	asOf   time.Time
	pages  map[string]domain.Page[domain.Issue]
	hits   int
}

func (c *listCacheSim) set(fn func(c *listCacheSim)) { c.mu.Lock(); defer c.mu.Unlock(); fn(c) }

func clonePage(p domain.Page[domain.Issue]) domain.Page[domain.Issue] {
	items := make([]domain.Issue, len(p.Items))
	for i, it := range p.Items {
		items[i] = domain.CloneIssue(it)
	}
	p.Items = items
	return p
}

type cachedListService struct {
	service.Service
	c *listCacheSim
}

func (s cachedListService) ListIssues(ctx context.Context, f domain.IssueFilter, r domain.PageRequest) (domain.Page[domain.Issue], error) {
	s.c.mu.Lock()
	p, ok := s.c.pages[r.Cursor]
	if s.c.serve && ok {
		s.c.hits++
		p = clonePage(p)
		p.AsOf = s.c.asOf
		s.c.mu.Unlock()
		return p, nil
	}
	s.c.mu.Unlock()
	p, err := s.Service.ListIssues(ctx, f, r)
	s.c.mu.Lock()
	if s.c.record && err == nil {
		s.c.pages[r.Cursor] = clonePage(p)
	}
	s.c.mu.Unlock()
	return p, err
}

func withCachedList(c *listCacheSim) harnessOpt {
	return func(_ *harness, d *Deps, _ *Options) { d.Service = cachedListService{d.Service, c} }
}

// cacheHarness boots with the startup list read live and kept as the stored
// pages, read at fixtureNow.
func cacheHarness(t *testing.T, fx servicetest.Fixture, opts ...harnessOpt) (*harness, *listCacheSim) {
	t.Helper()
	c := &listCacheSim{record: true, pages: map[string]domain.Page[domain.Issue]{}}
	h := newHarness(t, fx, append([]harnessOpt{withCachedList(c)}, opts...)...)
	c.set(func(c *listCacheSim) { c.record = false })
	if len(c.pages) == 0 {
		t.Fatal("the startup list was not kept")
	}
	return h, c
}

// autoHit runs the 60-second refresh answered from the cache with pages
// stamped asOf.
func autoHit(h *harness, c *listCacheSim, asOf time.Time) {
	h.t.Helper()
	before := c.hits
	c.set(func(c *listCacheSim) { c.serve, c.asOf = true, asOf })
	h.advance(61 * time.Second)
	h.fire(autoRefreshMsg{})
	c.set(func(c *listCacheSim) { c.serve = false })
	if c.hits == before {
		h.t.Fatal("the refresh was not answered from the cache")
	}
}

func TestCacheHit_pageStampIsAsOfElseSendTime(t *testing.T) {
	sent, read := fixtureNow, fixtureNow.Add(-4*time.Minute)
	if got := pageStamp(time.Time{}, sent); !got.Equal(sent) {
		t.Fatalf("a live page is stamped %v", got)
	}
	if got := pageStamp(read, sent); !got.Equal(read) {
		t.Fatalf("a cache hit is stamped %v", got)
	}
}

// A page read at t1, a detail read sent at t2 > t1, then the page served from
// the cache: the detail's issue is not rolled back.
func TestCacheHit_doesNotRollBackANewerDetail(t *testing.T) {
	h, c := cacheHarness(t, richFixture())
	mutateIssue(h, 6, func(i *domain.Issue) { i.Title = "Renamed on GitHub" })
	h.advance(10 * time.Second)
	h.press("enter", "esc") // the detail reads #6 at t2
	if h.base(6).Title != "Renamed on GitHub" {
		t.Fatal("the detail did not read the new title")
	}
	autoHit(h, c, fixtureNow) // the cached page still has the old title
	if got := h.base(6).Title; got != "Renamed on GitHub" {
		t.Fatalf("a cached page older than the detail rolled #6 back to %q", got)
	}
	h.contains("Renamed on GitHub")
}

// A cached list older than what the view holds changes no numbers, totals,
// order or cursor; only CheckedAt moves.
func TestCacheHit_olderThanTheListChangesOnlyCheckedAt(t *testing.T) {
	h, c := cacheHarness(t, bigFixture(250))
	h.advance(30 * time.Second)
	h.fake.Mutate(func(f *servicetest.Fixture) { f.Issues = f.Issues[1:] }) // #250 deleted on GitHub
	h.press("r")                                                            // a live read: the list without #250
	l := h.app.store.Issues
	want := struct {
		numbers []int
		total   int
		cursor  string
		dataAt  time.Time
	}{slices.Clone(l.Numbers), l.Total, l.Cursor, l.DataAt}
	if slices.Contains(want.numbers, 250) || !want.dataAt.Equal(fixtureNow.Add(30*time.Second)) {
		t.Fatalf("the live read did not land: DataAt %v", want.dataAt)
	}
	autoHit(h, c, fixtureNow) // the stored pages still list #250
	l = h.app.store.Issues
	if !slices.Equal(l.Numbers, want.numbers) || l.Total != want.total || l.Cursor != want.cursor {
		t.Fatalf("an older hit changed the list: %d rows, total %d", len(l.Numbers), l.Total)
	}
	if !l.DataAt.Equal(want.dataAt) {
		t.Fatalf("DataAt moved to %v", l.DataAt)
	}
	if !l.CheckedAt.Equal(h.now) {
		t.Fatalf("CheckedAt %v, want the check's time %v", l.CheckedAt, h.now)
	}
	if issueSel(h).rows[0] == 250 {
		t.Fatal("#250 is back on screen")
	}
}

// A hit newer than what the list holds (another process read it later)
// replaces the list, and the list's data is as old as that read.
func TestCacheHit_newerThanTheListReplacesIt(t *testing.T) {
	h, c := cacheHarness(t, richFixture())
	h.fake.Mutate(func(f *servicetest.Fixture) { f.Issues = f.Issues[1:] }) // #6 deleted
	later, _ := h.fake.ListIssues(context.Background(), domain.IssueFilter{State: "open"}, domain.PageRequest{Size: pageSize})
	c.set(func(c *listCacheSim) { c.pages[""] = clonePage(later) })
	read := fixtureNow.Add(30 * time.Second)
	autoHit(h, c, read)
	l := h.app.store.Issues
	if slices.Contains(l.Numbers, 6) {
		t.Fatal("a newer hit did not replace the list")
	}
	if !l.DataAt.Equal(read) || l.FromCheck() {
		t.Fatalf("DataAt %v, from check %v", l.DataAt, l.FromCheck())
	}
}

// A cached list page read before a live board read leaves the card in the
// board read's column.
func TestCacheHit_beforeALiveBoardReadKeepsTheBoardColumn(t *testing.T) {
	h, c := cacheHarness(t, richFixture())
	h.advance(10 * time.Second)
	progress := "In progress"
	if _, err := h.fake.SetStatus(context.Background(), 6, &progress); err != nil { // moved on GitHub
		t.Fatal(err)
	}
	h.press("3", "r") // a live board read at t2
	column := func() string {
		for _, col := range h.app.ctxFor(h.app.theme).Columns() {
			if slices.Contains(col.Numbers, 6) {
				return col.Name
			}
		}
		return ""
	}
	if got := column(); got != progress {
		t.Fatalf("the board read put #6 in %q", got)
	}
	h.press("1")
	autoHit(h, c, fixtureNow) // the cached page has #6 in its old column
	if got := column(); got != progress {
		t.Fatalf("a cached page older than the board read moved #6 to %q", got)
	}
}

// A hit with an older AsOf neither releases an uncertain change nor unblocks
// its held queue; a live read sent after the failure does.
func TestCacheHit_olderHitNeitherReleasesUncertainNorUnblocksQueue(t *testing.T) {
	h, c := cacheHarness(t, richFixture())
	h.advance(5 * time.Second)
	g := h.hold("Close", 6)
	h.press("x", "x") // close, then a queued reopen
	gr := h.hold("GetIssue", 6)
	h.fail(g, uncertainErr()) // its re-read is held
	autoHit(h, c, fixtureNow)
	if cs := h.app.pending.Changes(6); len(cs) == 0 || cs[0].Phase != Uncertain {
		t.Fatal("a cached page read before the failure released the uncertain change")
	}
	if n := len(h.calls("Reopen")); n != 0 {
		t.Fatal("a cached page unblocked the held queue")
	}
	h.release(gr) // the re-read, sent after the failure
	if n := len(h.calls("Reopen")); n != 1 {
		t.Fatal("the re-read did not release the queue")
	}
}

// Auto-refresh scheduling uses CheckedAt, not the data's age: a list whose
// data is old but was just checked is not refreshed again at once.
func TestCacheHit_autoRefreshCountsFromTheCheck(t *testing.T) {
	h, c := cacheHarness(t, richFixture())
	autoHit(h, c, fixtureNow)
	if age := h.app.age(ViewIssues); age != 0 {
		t.Fatalf("age %v right after a check", age)
	}
	n := len(h.calls("ListIssues"))
	h.press("2", "1") // a tab switch refreshes a view older than the interval
	if len(h.calls("ListIssues")) != n || c.hits != 1 {
		t.Fatal("the list was refreshed again although it was just checked")
	}
}

// ---- the header (UI 5.3) ----

func TestCacheHit_headerTellsTheDataAge(t *testing.T) {
	h, c := cacheHarness(t, richFixture())
	h.size(140, 40)
	h.contains("● synced just now") // a live read
	h.lacks("checked")

	autoHit(h, c, fixtureNow) // data read 61 s ago, checked now
	h.contains("● synced 1m ago · checked just now")
	h.size(99, 30)
	h.contains("● synced 1m ago")
	h.lacks("checked")
	h.size(140, 40)

	c.set(func(c *listCacheSim) { c.serve, c.asOf = true, h.now.Add(-20*time.Second) })
	h.press("2")
	h.advance(61 * time.Second)
	h.app.store.Issues.CheckedAt = time.Time{} // due again
	c.set(func(c *listCacheSim) { c.asOf = h.now.Add(-20 * time.Second) })
	h.press("1") // a hit read 20 s ago, not by this check
	c.set(func(c *listCacheSim) { c.serve = false })
	h.contains("● synced <1m ago · checked just now")
	h.lacks("synced just now")

	h.press("r") // manual: fresh, so read live
	h.contains("● synced just now")
	h.lacks("checked")
}

func TestCacheHit_staleBannerUsesDataAt(t *testing.T) {
	h, c := cacheHarness(t, richFixture())
	h.size(140, 40)
	h.advance(4 * time.Minute)
	autoHit(h, c, fixtureNow.Add(30*time.Second)) // newer than the list, read 4m31s ago
	if !h.app.store.Issues.DataAt.Equal(fixtureNow.Add(30 * time.Second)) {
		t.Fatal("the hit was not applied")
	}
	g := h.hold("ListIssues", 0)
	h.advance(61 * time.Second)
	h.fire(autoRefreshMsg{})
	h.fail(g, &domain.Error{Kind: domain.ErrNetwork, Message: "network unreachable"})
	h.contains("⚠ stale · 5m ago")
	h.contains("Showing data from 5m ago")
}

// The board and the pull request list are live and keep today's header.
func TestCacheHit_boardAndPRHeadersUnchanged(t *testing.T) {
	h, _ := cacheHarness(t, richFixture())
	h.size(140, 40)
	for _, view := range []string{"2", "3"} {
		h.press(view)
		h.contains("● synced just now")
		h.lacks("checked")
	}
}

// A load whose first page is a hit and whose later pages are live is as old
// as its oldest page.
func TestCacheHit_mixedLoadIsAsOldAsItsOldestPage(t *testing.T) {
	h, c := cacheHarness(t, bigFixture(250))
	c.set(func(c *listCacheSim) {
		for cursor := range c.pages {
			if cursor != "" {
				delete(c.pages, cursor) // only page 1 is stored
			}
		}
	})
	h.advance(2 * time.Minute)
	read := fixtureNow.Add(30 * time.Second) // newer than the list, older than the live pages
	autoHit(h, c, read)
	l := h.app.store.Issues
	if !l.DataAt.Equal(read) || l.FromCheck() {
		t.Fatalf("DataAt %v (from check %v), want the hit's %v", l.DataAt, l.FromCheck(), read)
	}
	if len(l.Numbers) != 250 {
		t.Fatalf("%d rows", len(l.Numbers))
	}
}
