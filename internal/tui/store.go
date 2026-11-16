package tui

import (
	"sort"
	"time"

	"github.com/pkieltyka/todos/internal/domain"
)

// LoadPhase is how far a list has loaded.
type LoadPhase int

const (
	PhaseEmpty LoadPhase = iota
	PhaseFirstPage
	PhaseBackground
	PhaseComplete
)

const (
	listCap     = 500 // issues and PRs per list (5.2)
	commentsCap = 500 // comments per detail (5.2)
	cardsCap    = 500 // cards drawn on the board (A7.6)
	pageSize    = 100
)

// IssueEntry is the base of one issue: the issue as last confirmed by GitHub
// and when the request that produced it was sent (A5).
type IssueEntry struct {
	Base domain.Issue
	AsOf time.Time
}

// IssueQuery is the server query of the issue list (tui.md 7.4).
type IssueQuery struct {
	State domain.IssueState // open | closed | all
	Mine  bool
}

// PRQuery is the server query of the PR list.
type PRQuery struct {
	State domain.PRState // open | closed | merged | all
}

// ListCache holds a list as issue numbers in GitHub order; the issues
// themselves live in the store. Its pages can come from the service's read
// cache (cache.md 5), so it keeps two times:
//   - DataAt, when its data was really read from GitHub: the oldest stamp
//     (pageStamp) of the pages it was built from. Everything the user reads
//     (the header's age, the stale banner) uses it.
//   - CheckedAt, when the last load of it completed, by a live read or a
//     cache hit. Auto-refresh scheduling uses it.
type ListCache[Q comparable] struct {
	Query     Q
	Numbers   []int
	Total     int
	Cursor    string
	HasMore   bool
	Phase     LoadPhase
	DataAt    time.Time
	CheckedAt time.Time
	Err       *domain.Error // non-nil together with Numbers = stale
	Truncated bool

	checkSent time.Time     // the first page's send time of the last load
	next      *ListCache[Q] // a refresh being assembled
}

// FromCheck reports whether the list's data was read by its last check
// rather than served from the read cache: only then is it "synced" as of that
// check.
func (l ListCache[Q]) FromCheck() bool {
	return !l.checkSent.IsZero() && !l.DataAt.Before(l.checkSent)
}

// PRListCache holds a pull request list as numbers in GitHub order; the
// summaries live in the store under the same asOf rule as issues (finding 6).
type PRListCache struct {
	Query     PRQuery
	Numbers   []int
	Total     int
	Cursor    string
	HasMore   bool
	Phase     LoadPhase
	LoadedAt  time.Time
	Err       *domain.Error
	Truncated bool

	next *PRListCache
}

// ColumnOrder is one board column as the Board call returned it: its status
// name, its option and its cards in GitHub's order. The displayed columns,
// their names and their order come from here, never from an older BoardInfo
// (finding 4).
type ColumnOrder struct {
	Status   *string // nil = "No status"
	OptionID string
	Numbers  []int
}

// BoardCache holds the board as ordered issue numbers per status (A5).
type BoardCache struct {
	// Extra are issues placed on the board this session (created there, or
	// moved or added by a settled change) that GitHub's Order may not list
	// yet; Columns considers them. Cleared by every board load.
	Extra                     map[int]bool
	Info                      *domain.BoardInfo
	InfoErr                   *domain.Error
	Order                     []ColumnOrder
	Hidden                    int
	Truncated, CardsTruncated bool
	Loaded                    bool
	LoadedAt                  time.Time
	Err                       *domain.Error
}

// DetailEntry is the body and comments of one issue. The body and the
// comments are separate requests with separate errors (finding 5).
type DetailEntry struct {
	Body          string
	BodyAsOf      time.Time
	Loaded        bool
	Comments      []domain.Comment
	CommentsTotal int
	CommentsPhase LoadPhase
	LoadedAt      time.Time
	BodyErr       *domain.Error
	CommentsErr   *domain.Error

	cursor       string
	nextComments []domain.Comment
}

// PRDetailEntry is one pull request's body, view-only fields and comments.
// Its summary (title, state, checks, …) lives in the store's PR entries.
type PRDetailEntry struct {
	Detail        *domain.PRDetail
	Comments      []domain.Comment
	CommentsTotal int
	CommentsPhase LoadPhase
	LoadedAt      time.Time
	DetailErr     *domain.Error
	CommentsErr   *domain.Error

	cursor       string
	nextComments []domain.Comment
}

// Meta is session metadata the TUI shows.
type Meta struct {
	Session *domain.Session
	Labels  []domain.Label
	Users   []domain.User
}

// Store is the TUI's cache of GitHub data (tui.md 7).
type Store struct {
	issues  map[int]*IssueEntry
	prs     map[int]*PREntry
	Issues  ListCache[IssueQuery]
	PRs     PRListCache
	Board   BoardCache
	details *lru[int, *DetailEntry]
	prDet   *lru[int, *PRDetailEntry]
	Meta    Meta
	version uint64
}

// NewStore returns an empty store.
func NewStore() *Store {
	return &Store{
		issues:  make(map[int]*IssueEntry),
		prs:     make(map[int]*PREntry),
		Board:   BoardCache{Extra: map[int]bool{}},
		Issues:  ListCache[IssueQuery]{Query: IssueQuery{State: "open"}},
		PRs:     PRListCache{Query: PRQuery{State: "open"}},
		details: newLRU[int, *DetailEntry](64),
		prDet:   newLRU[int, *PRDetailEntry](32),
	}
}

// Put stores an incoming copy of an issue under the asOf rule (A5 rule 7):
// it replaces the base only if asOf is not older than the current one. asOf
// is the request's sent time for reads and the arrival time for writes.
func (s *Store) Put(i domain.Issue, asOf time.Time) bool {
	if e, ok := s.issues[i.Number]; ok && asOf.Before(e.AsOf) {
		return false
	}
	s.issues[i.Number] = &IssueEntry{Base: i, AsOf: asOf}
	s.version++
	return true
}

// Issue returns the base of an issue.
func (s *Store) Issue(n int) (domain.Issue, bool) {
	e, ok := s.issues[n]
	if !ok {
		return domain.Issue{}, false
	}
	return e.Base, true
}

// Entry returns the stored entry of an issue.
func (s *Store) Entry(n int) (*IssueEntry, bool) {
	e, ok := s.issues[n]
	return e, ok
}

// Remove deletes an issue everywhere.
func (s *Store) Remove(n int) {
	delete(s.issues, n)
	s.Issues.Numbers = without(s.Issues.Numbers, n)
	for i := range s.Board.Order {
		s.Board.Order[i].Numbers = without(s.Board.Order[i].Numbers, n)
	}
	s.details.remove(n)
	s.version++
}

// Detail returns the detail entry of an issue, creating it.
func (s *Store) Detail(n int) *DetailEntry {
	if d, ok := s.details.get(n); ok {
		return d
	}
	d := &DetailEntry{}
	s.details.put(n, d)
	return d
}

// PeekDetail returns the detail entry without creating it.
func (s *Store) PeekDetail(n int) (*DetailEntry, bool) { return s.details.get(n) }

// PRDetail returns the detail entry of a pull request, creating it.
func (s *Store) PRDetail(n int) *PRDetailEntry {
	if d, ok := s.prDet.get(n); ok {
		return d
	}
	d := &PRDetailEntry{}
	s.prDet.put(n, d)
	return d
}

// PREntry is the summary of one pull request and when the request that
// produced it was sent.
type PREntry struct {
	Base domain.PullRequest
	AsOf time.Time
}

// PutPR stores a pull request summary under the asOf rule, so a newer list
// page is never masked by an older detail, nor the other way round.
func (s *Store) PutPR(p domain.PullRequest, asOf time.Time) bool {
	if e, ok := s.prs[p.Number]; ok && asOf.Before(e.AsOf) {
		return false
	}
	s.prs[p.Number] = &PREntry{Base: p, AsOf: asOf}
	s.version++
	return true
}

// PR returns a pull request summary.
func (s *Store) PR(n int) (domain.PullRequest, bool) {
	e, ok := s.prs[n]
	if !ok {
		return domain.PullRequest{}, false
	}
	return e.Base, true
}

// ClearPlacements forgets every issue's board placement, as of t. It runs
// when the selected project changes: placements belong to a project, and
// any read sent before the change is now older than the base (finding 3).
func (s *Store) ClearPlacements(t time.Time) {
	for _, e := range s.issues {
		e.Base.Board = nil
		if e.AsOf.Before(t) {
			e.AsOf = t
		}
	}
	s.version++
}

func without(ns []int, n int) []int {
	out := ns[:0:0]
	for _, v := range ns {
		if v != n {
			out = append(out, v)
		}
	}
	return out
}

// appendNew appends the numbers not already present; an issue that moved
// between pages keeps its row (tui.md 7.3).
func appendNew(ns []int, more []int) []int {
	seen := make(map[int]bool, len(ns))
	for _, n := range ns {
		seen[n] = true
	}
	for _, n := range more {
		if !seen[n] {
			ns = append(ns, n)
			seen[n] = true
		}
	}
	return ns
}

// DisplayColumn is a board column ready to draw.
type DisplayColumn struct {
	Key     string  // option ID, or "" for "No status"
	Name    string  // status name, or "No status"
	Status  *string // nil = "No status"
	Numbers []int
}

// Columns derives the displayed board from the store (A5, tui.md 7.5).
// effective returns the issue to draw (the base with pending changes replayed;
// Phase 6 supplies the replay). extra are issues with a pending board patch
// that may not be in GitHub's order yet.
func (s *Store) Columns(effective func(int) (domain.Issue, bool), extra []int) []DisplayColumn {
	if !s.Board.Loaded {
		return nil
	}
	type col struct {
		DisplayColumn
	}
	// "No status" is always first; it is shown only when it has cards.
	cols := []*col{{DisplayColumn: DisplayColumn{Name: "No status"}}}
	for _, o := range s.Board.Order {
		if o.Status == nil {
			continue
		}
		name := *o.Status
		cols = append(cols, &col{DisplayColumn: DisplayColumn{Key: o.OptionID, Name: line(name), Status: &name}})
	}
	find := func(st *string) *col {
		for _, c := range cols {
			if (st == nil && c.Status == nil) || (st != nil && c.Status != nil && *st == *c.Status) {
				return c
			}
		}
		return nil
	}
	statusOf := func(i domain.Issue) (*string, bool) {
		if i.Board == nil {
			return nil, false
		}
		return i.Board.Status, true
	}
	same := func(a, b *string) bool { return (a == nil && b == nil) || (a != nil && b != nil && *a == *b) }

	placed := map[int]bool{}
	// First: GitHub's order, for issues whose effective status equals both
	// the column and their base status.
	for _, o := range s.Board.Order {
		c := find(o.Status)
		if c == nil {
			continue
		}
		for _, n := range o.Numbers {
			eff, ok := effective(n)
			if !ok {
				continue
			}
			est, on := statusOf(eff)
			base, _ := s.Issue(n)
			bst, bon := statusOf(base)
			if on && bon && same(est, o.Status) && same(bst, o.Status) {
				c.Numbers = append(c.Numbers, n)
				placed[n] = true
			}
		}
	}
	// Then every other member at the bottom of its effective column, by number.
	var rest []int
	for _, o := range s.Board.Order {
		rest = append(rest, o.Numbers...)
	}
	rest = append(rest, extra...)
	sort.Ints(rest)
	for _, n := range rest {
		if placed[n] {
			continue
		}
		eff, ok := effective(n)
		if !ok {
			continue
		}
		st, on := statusOf(eff)
		if !on {
			continue
		}
		if c := find(st); c != nil {
			c.Numbers = append(c.Numbers, n)
			placed[n] = true
		}
	}
	// Draw at most cardsCap cards; "No status" only when it is not empty.
	var out []DisplayColumn
	left := cardsCap
	for i, c := range cols {
		if i == 0 && len(c.Numbers) == 0 {
			continue
		}
		if len(c.Numbers) > left {
			c.Numbers = c.Numbers[:left]
		}
		left -= len(c.Numbers)
		out = append(out, c.DisplayColumn)
	}
	return out
}

// lru is a small bounded map with least-recently-used eviction.
type lru[K comparable, V any] struct {
	cap   int
	order []K
	m     map[K]V
}

func newLRU[K comparable, V any](capacity int) *lru[K, V] {
	return &lru[K, V]{cap: capacity, m: make(map[K]V)}
}

func (l *lru[K, V]) get(k K) (V, bool) {
	v, ok := l.m[k]
	if ok {
		l.touch(k)
	}
	return v, ok
}

func (l *lru[K, V]) put(k K, v V) {
	if _, ok := l.m[k]; ok {
		l.m[k] = v
		l.touch(k)
		return
	}
	l.m[k] = v
	l.order = append(l.order, k)
	if len(l.order) > l.cap {
		delete(l.m, l.order[0])
		l.order = l.order[1:]
	}
}

func (l *lru[K, V]) remove(k K) {
	delete(l.m, k)
	for i, v := range l.order {
		if v == k {
			l.order = append(l.order[:i:i], l.order[i+1:]...)
			return
		}
	}
}

func (l *lru[K, V]) touch(k K) {
	for i, v := range l.order {
		if v == k {
			l.order = append(append(l.order[:i:i], l.order[i+1:]...), k)
			return
		}
	}
}
