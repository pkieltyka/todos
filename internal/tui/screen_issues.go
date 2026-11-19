package tui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/pkieltyka/todos/internal/domain"
)

// IssueList is the Issues view (UI 3.1).
type IssueList struct {
	sel, prevIndex, offset int
	filter                 lineInput
	filtering              bool
	rows                   []int
	height                 int
	lastClosed             map[int]time.Time // the closed view's last seen closed times (asClosed)
}

func newIssueList() *IssueList { return &IssueList{} }

func (s *IssueList) ID() ScreenID         { return ScreenIssues }
func (s *IssueList) TargetIssue(*Ctx) int { return s.sel }
func (s *IssueList) Slots() []Slot        { return nil }
func (s *IssueList) Title(*Ctx) string    { return "" }
func (s *IssueList) Mode() InputMode {
	if s.filtering {
		return ModeText
	}
	return ModeNav
}
func (s *IssueList) Renders(*Ctx) []renderReq { return nil }

func (s *IssueList) Position(*Ctx) string {
	if len(s.rows) == 0 {
		return ""
	}
	i, _ := indexOf(s.rows, s.sel)
	return itoa(i+1) + "/" + itoa(len(s.rows))
}

func (s *IssueList) Actions(*Ctx) []Action {
	acts := []Action{ActDown, ActUp, ActTop, ActBottom, ActHalfDown, ActHalfUp}
	has := len(s.rows) > 0
	if has {
		acts = append(acts, ActOpen)
	}
	if s.filter.String() != "" {
		acts = append(acts, ActBack)
	}
	acts = append(acts, ActNew, ActQuickAdd)
	if has {
		acts = append(acts, ActEdit, ActClose)
	}
	acts = append(acts, ActFilter, ActState, ActMine)
	if has {
		acts = append(acts, ActAssign, ActLabel, ActStatus, ActComment, ActCloseNotPlanned,
			ActStatusPrev, ActStatusNext, ActDelete, ActBrowser, ActCopyURL)
	}
	return acts
}

// Sync recomputes the visible rows: the list's numbers, filtered locally.
func (s *IssueList) Sync(c *Ctx) {
	q := s.filter.String()
	rows := s.rows[:0:0]
	var shown []domain.Issue
	for _, n := range c.Store.Issues.Numbers {
		if i, ok := c.Issue(n); ok && matches(i, q) {
			rows = append(rows, n)
			shown = append(shown, s.asClosed(c, i))
		}
	}
	if c.Store.Issues.Query.State == "closed" {
		rows = closedOrder(shown) // the selection stays on its issue: reselect is by number
	}
	s.rows = rows
	s.height = s.bodyHeight(c)
	s.sel, s.offset = reselect(s.rows, s.sel, s.prevIndex, s.offset, s.height)
	if i, ok := indexOf(s.rows, s.sel); ok {
		s.prevIndex = i
	}
}

func (s *IssueList) bodyHeight(c *Ctx) int {
	h := c.Frame.Body.H
	if banner(c, c.Store.Issues.Err, len(c.Store.Issues.Numbers) > 0, c.Store.Issues.DataAt) != "" {
		h--
	}
	return h
}

func (s *IssueList) selected(c *Ctx) (domain.Issue, bool) {
	if len(s.rows) == 0 {
		return domain.Issue{}, false
	}
	return c.Issue(s.sel)
}

func (s *IssueList) Update(c *Ctx, msg tea.Msg) tea.Cmd {
	if s.filtering {
		if k, ok := msg.(tea.KeyPressMsg); ok {
			enter, esc := s.filter.key(k)
			if esc {
				s.filter.Clear()
			}
			if enter || esc {
				s.filtering = false
			}
		}
		if p, ok := msg.(tea.PasteMsg); ok {
			s.filter.insert(p.Content)
		}
		return nil
	}
	m, ok := msg.(ActionMsg)
	if !ok {
		return nil
	}
	cache := &c.Store.Issues
	switch m.A {
	case ActDown, ActUp, ActTop, ActBottom, ActHalfDown, ActHalfUp:
		i, _ := indexOf(s.rows, s.sel)
		i = listNav(m.A, i, len(s.rows), s.height)
		if len(s.rows) > 0 {
			s.sel = s.rows[i]
			s.offset = clampOffset(s.offset, i, s.height, len(s.rows))
		}
	case ActOpen:
		if i, ok := s.selected(c); ok {
			c.Push(newIssueDetail(i.Number, "Issues"))
			return c.a.loadIssue(i.Number, ReasonOpen)
		}
	case ActBack:
		s.filter.Clear()
	case ActFilter:
		s.filtering = true
	case ActMine:
		cache.Query.Mine = !cache.Query.Mine
		return c.a.loadIssues(ReasonQueryChanged)
	case ActState:
		next := map[domain.IssueState]domain.IssueState{"open": "closed", "closed": "all", "all": "open"}
		cache.Query.State = next[cache.Query.State]
		return c.a.loadIssues(ReasonQueryChanged)
	case ActNew, ActQuickAdd, ActEdit, ActAssign, ActLabel, ActComment, ActClose, ActCloseNotPlanned,
		ActStatus, ActStatusPrev, ActStatusNext, ActDelete:
		return c.a.issueAction(c, m.A, s.sel, false, nil)
	case ActBrowser:
		if i, ok := s.selected(c); ok {
			return c.a.openURL(i.URL)
		}
	case ActCopyURL:
		if i, ok := s.selected(c); ok {
			return c.a.copyURL(i.URL)
		}
	}
	return nil
}

func (s *IssueList) SubBar(c *Ctx, w int) string {
	t := c.Theme
	if s.filtering && w < compactWidth {
		return t.accent().Render(s.filter.compactFilter(w))
	}
	l := c.Store.Issues
	left := " " + t.text().Render(string(l.Query.State)+" ▾") + closedNote(t, l.Query.State) + t.subtle().Render(" · ") +
		t.muted().Render("mine: "+onOff(l.Query.Mine)) + t.subtle().Render(" · ") + s.filterLabel(c)
	if l.Err != nil && len(l.Numbers) == 0 {
		return spread(left, "", w)
	}
	return spread(left, t.muted().Render(listCount(l.Phase, len(s.rows), len(l.Numbers), l.Total, l.Truncated,
		s.filter.String() != "", issueNoun(l.Query)))+" ", w)
}

// asClosed is the issue as the closed view orders and times it. A reopen
// clears the closed time and bumps the updated time, which would move the
// row to the top; the view keeps the last closed time it saw instead, so a
// reopened issue stays where it was until it leaves the list, as in the open
// and all views. A new close brings a new closed time and sorts by it.
func (s *IssueList) asClosed(c *Ctx, i domain.Issue) domain.Issue {
	if c.Store.Issues.Query.State != "closed" {
		return i
	}
	if s.lastClosed == nil {
		s.lastClosed = map[int]time.Time{}
	}
	if i.ClosedAt != nil {
		s.lastClosed[i.Number] = *i.ClosedAt
	} else if t, ok := s.lastClosed[i.Number]; ok {
		i.ClosedAt = &t
	}
	return i
}

// closedNote says, in the closed view only, that rows are ordered and timed
// by closed time.
func closedNote(t Theme, state domain.IssueState) string {
	if state != "closed" {
		return ""
	}
	return t.muted().Render(" by closed time")
}

func (s *IssueList) filterLabel(c *Ctx) string {
	return filterLabel(c.Theme, s.filtering, s.filter.String())
}

func (s *IssueList) Cursor(c *Ctx) *tea.Cursor {
	if w := c.Frame.SubBar.W; w < compactWidth {
		return tea.NewCursor(lipgloss.Width(s.filter.compactFilter(w)), c.Frame.SubBar.Y)
	}
	l := c.Store.Issues
	t := c.Theme
	prefix := " " + t.text().Render(string(l.Query.State)+" ▾") + closedNote(t, l.Query.State) + " · " + "mine: " + onOff(l.Query.Mine) + " · /"
	return tea.NewCursor(lipgloss.Width(prefix)+len(s.filter.value), c.Frame.SubBar.Y)
}

func (s *IssueList) View(c *Ctx, area Rect) string {
	t := c.Theme
	l := c.Store.Issues
	var lines []string
	if b := banner(c, l.Err, len(l.Numbers) > 0, l.DataAt); b != "" {
		lines = append(lines, b)
	}
	h := area.H - len(lines)
	switch {
	case len(l.Numbers) == 0 && l.Err != nil:
		return box(append(lines, strings.Split(centered(failed(t, "Couldn't load issues", l.Err), Rect{W: area.W, H: h}), "\n")...), area.W, area.H)
	case len(l.Numbers) == 0 && l.Phase != PhaseComplete:
		return centered([]string{t.muted().Render(c.Spin + " Loading issues…")}, area)
	case len(l.Numbers) == 0:
		return centered([]string{t.muted().Render("No " + string(l.Query.State) + " issues. Press n to create one, A to quick add.")}, area)
	case len(s.rows) == 0:
		return box(append(lines, strings.Split(centered([]string{t.muted().Render(`Nothing matches "` + s.filter.String() + `". esc clears the filter.`)}, Rect{W: area.W, H: h}), "\n")...), area.W, area.H)
	}
	cols := issueColumns(area.W)
	for r := s.offset; r < len(s.rows) && len(lines) < area.H; r++ {
		i, _ := c.Issue(s.rows[r])
		i = s.asClosed(c, i)
		lines = append(lines, issueRow(c, i, cols, s.rows[r] == s.sel, area.W))
	}
	return box(lines, area.W, area.H)
}

func issueColumns(w int) []Placed {
	return Fit([]Col{
		{ID: ColState, Min: 2, Max: 2},
		{ID: ColNumber, Min: 6, Max: 7},
		{ID: ColTitle, Min: 24, Need: 44},
		{ID: ColLabels, Min: 10, Max: 24, Drop: 4},
		{ID: ColAssignees, Min: 10, Max: 14, Drop: 3},
		{ID: ColStatus, Min: 12, Max: 14, Drop: 5, MinWidth: 120},
		{ID: ColUpdated, Min: 4, Max: 14, Drop: 2}, // "10 months ago" at ≥ 120
	}, w)
}

func issueRow(c *Ctx, i domain.Issue, cols []Placed, sel bool, w int) string {
	t := c.Theme
	wide := w >= 120
	lend, aright := -1, false // labels' borrowed end, and whether the assignee moves right
	if !wide {
		lend, aright = borrowForLabels(c, i, cols)
	}
	var b strings.Builder
	x := 0
	for _, col := range cols {
		if col.X > x {
			b.WriteString(strings.Repeat(" ", col.X-x))
		} else if col.X < x {
			// the labels borrowed into this column (borrowForLabels)
			col.W -= x - col.X
			col.X = x
		}
		var cell string
		switch col.ID {
		case ColState:
			cell = pendingGutter(c, i.Number, sel) + issueGlyph(t, i)
		case ColNumber:
			cell = t.muted().Render("#" + itoaPlain(i.Number))
		case ColTitle:
			cell = t.text().Render(i.Title)
		case ColLabels:
			if wide {
				cell = labelDots(t, i.Labels)
			} else {
				if lend > col.X+col.W {
					col.W = lend - col.X
				}
				cell = labelNames(t, i.Labels, col.W)
			}
		case ColAssignees:
			cell = people(t, i.Assignees)
			if aright {
				cell = strings.Repeat(" ", max(0, col.W-lipgloss.Width(cell))) + cell
			}
		case ColStatus:
			if i.Board != nil {
				cell = t.text().Render(statusName(i.Board.Status))
			} else {
				cell = t.muted().Render("—")
			}
		case ColUpdated:
			if wide {
				cell = t.muted().Render(agoLong(c.Now, listTime(c.Store.Issues.Query.State, i)))
			} else {
				cell = t.muted().Render(ago(c.Now, listTime(c.Store.Issues.Query.State, i)))
			}
		}
		b.WriteString(fit(cell, col.W))
		x = col.X + col.W
	}
	row := fit(b.String(), w)
	if sel {
		return t.selected(row)
	}
	return row
}

// borrowForLabels gives the labels the room the assignee column does not use
// on this row (Phase 9 polish): when the labels are cut and the assignee is
// narrower than its column, the labels extend into it and the assignee moves
// to the column's right edge, keeping one space between them. It returns the
// labels' new end (-1: no change) and whether the assignee moves.
func borrowForLabels(c *Ctx, i domain.Issue, cols []Placed) (int, bool) {
	for k := 0; k+1 < len(cols); k++ {
		lc, ac := cols[k], cols[k+1]
		if lc.ID != ColLabels || ac.ID != ColAssignees {
			continue
		}
		full := lipgloss.Width(labelNames(c.Theme, i.Labels, 1<<10))
		if full <= lc.W {
			return -1, false
		}
		slack := ac.W - lipgloss.Width(people(c.Theme, i.Assignees))
		room := ac.X + slack - 1 - (lc.X + lc.W) // up to one space before the moved assignee
		if room <= 0 {
			return -1, false
		}
		return lc.X + lc.W + min(full-lc.W, room), true
	}
	return -1, false
}

// ---- shared list helpers ----

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// gutter is the selection bar, or a space.
func gutter(t Theme, sel bool) string {
	if sel {
		return t.accent().Render("▌")
	}
	return " "
}

// plural turns "open issues" into "open issue" for one.
func plural(n int, noun string) string {
	if n == 1 {
		return strings.TrimSuffix(noun, "s")
	}
	return noun
}

func filterLabel(t Theme, filtering bool, q string) string {
	switch {
	case filtering:
		return t.accent().Render("/" + q)
	case q != "":
		return t.text().Render("/" + q)
	}
	return t.muted().Render("filter: —")
}

func issueNoun(q IssueQuery) string {
	if q.State == "all" {
		return "issues"
	}
	return string(q.State) + " issues"
}

// listCount is the sub-bar's right side (UI 3.1).
func listCount(phase LoadPhase, shown, loaded, total int, truncated, filtered bool, noun string) string {
	var parts []string
	switch {
	case filtered:
		parts = append(parts, itoa(shown)+" of "+itoa(loaded))
	case truncated:
		parts = append(parts, itoa(loaded)+" of "+itoa(total)+" · list cut off")
	case phase != PhaseEmpty:
		parts = append(parts, itoa(total)+" "+plural(total, noun))
	}
	if phase == PhaseBackground || phase == PhaseFirstPage {
		parts = append(parts, "loading "+itoa(loaded)+"/"+itoa(min(total, listCap))+"…")
	}
	return strings.Join(parts, " · ")
}

// banner is the stale-data line at the top of a body (UI 3.14).
func banner(c *Ctx, err *domain.Error, has bool, loaded time.Time) string {
	if err == nil || !has {
		return ""
	}
	when := ""
	if !loaded.IsZero() {
		when = ". Showing data from " + agoPhrase(c.Now, loaded)
	}
	return fit(" "+c.Theme.pending().Render("⚠ Refresh failed: "+errText(err)+when+" · r retry"), c.Frame.Body.W)
}

// failed is the failed-to-load state: the reason and the fix (UI 3.1).
func failed(t Theme, what string, err *domain.Error) []string {
	out := []string{t.errorS().Render("✗ " + what + ": " + errText(err) + ".")}
	if h := errHint(err); h != "" {
		out = append(out, t.muted().Render(h))
	}
	return out
}
