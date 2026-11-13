package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/pkieltyka/todos/internal/domain"
)

// PRList is the read-only Pull requests view (UI 3.3). "Only mine" is a
// local filter here: GitHub's pull request listing has no author filter
// (5.2, tui.md 7.4).
type PRList struct {
	sel, prevIndex, offset int
	filter                 lineInput
	filtering              bool
	mine                   bool
	rows                   []int
	height                 int
}

func newPRList() *PRList { return &PRList{} }

func (s *PRList) ID() ScreenID             { return ScreenPRs }
func (s *PRList) Slots() []Slot            { return nil }
func (s *PRList) Title(*Ctx) string        { return "pull requests" }
func (s *PRList) Renders(*Ctx) []renderReq { return nil }
func (s *PRList) Mode() InputMode {
	if s.filtering {
		return ModeText
	}
	return ModeNav
}

func (s *PRList) Position(*Ctx) string {
	if len(s.rows) == 0 {
		return ""
	}
	i, _ := indexOf(s.rows, s.sel)
	return itoa(i+1) + "/" + itoa(len(s.rows))
}

func (s *PRList) Actions(*Ctx) []Action {
	acts := []Action{ActDown, ActUp, ActTop, ActBottom, ActHalfDown, ActHalfUp}
	if len(s.rows) > 0 {
		acts = append(acts, ActOpen)
	}
	if s.filter.String() != "" {
		acts = append(acts, ActBack)
	}
	acts = append(acts, ActFilter, ActState, ActMine)
	if len(s.rows) > 0 {
		acts = append(acts, ActBrowser, ActCopyURL)
	}
	return acts
}

func (s *PRList) Sync(c *Ctx) {
	q := s.filter.String()
	rows := s.rows[:0:0]
	for _, n := range c.Store.PRs.Numbers {
		p, ok := c.Store.PR(n)
		if !ok {
			continue
		}
		if s.mine && (p.Author == nil || *p.Author != c.Viewer) {
			continue
		}
		if matchesPR(p, q) {
			rows = append(rows, p.Number)
		}
	}
	s.rows = rows
	s.height = c.Frame.Body.H
	if banner(c, c.Store.PRs.Err, len(c.Store.PRs.Numbers) > 0, c.Store.PRs.LoadedAt) != "" {
		s.height--
	}
	s.sel, s.offset = reselect(s.rows, s.sel, s.prevIndex, s.offset, s.height)
	if i, ok := indexOf(s.rows, s.sel); ok {
		s.prevIndex = i
	}
}

func (s *PRList) selected(c *Ctx) (domain.PullRequest, bool) {
	if len(s.rows) == 0 {
		return domain.PullRequest{}, false
	}
	return c.Store.PR(s.sel)
}

func (s *PRList) Update(c *Ctx, msg tea.Msg) tea.Cmd {
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
	switch m.A {
	case ActDown, ActUp, ActTop, ActBottom, ActHalfDown, ActHalfUp:
		i, _ := indexOf(s.rows, s.sel)
		i = listNav(m.A, i, len(s.rows), s.height)
		if len(s.rows) > 0 {
			s.sel = s.rows[i]
			s.offset = clampOffset(s.offset, i, s.height, len(s.rows))
		}
	case ActOpen:
		if p, ok := s.selected(c); ok {
			c.Push(newPRDetail(p.Number))
			return c.a.loadPR(p.Number, ReasonOpen)
		}
	case ActBack:
		s.filter.Clear()
	case ActFilter:
		s.filtering = true
	case ActMine:
		s.mine = !s.mine
	case ActState:
		next := map[domain.PRState]domain.PRState{"open": "closed", "closed": "merged", "merged": "all", "all": "open"}
		c.Store.PRs.Query.State = next[c.Store.PRs.Query.State]
		return c.a.loadPRs(ReasonQueryChanged)
	case ActBrowser:
		if p, ok := s.selected(c); ok {
			return c.a.openURL(p.URL)
		}
	case ActCopyURL:
		if p, ok := s.selected(c); ok {
			return c.a.copyURL(p.URL)
		}
	}
	return nil
}

func (s *PRList) SubBar(c *Ctx, w int) string {
	t := c.Theme
	l := c.Store.PRs
	left := " " + t.text().Render(string(l.Query.State)+" ▾") + t.subtle().Render(" · ") +
		t.muted().Render("mine: "+onOff(s.mine)) + t.subtle().Render(" · ") + filterLabel(t, s.filtering, s.filter.String())
	if l.Err != nil && len(l.Numbers) == 0 {
		return spread(left, "", w)
	}
	noun := string(l.Query.State) + " PRs"
	if l.Query.State == "all" {
		noun = "PRs"
	}
	var right string
	if s.mine {
		// The match count next to the loaded count, so a cut-off list is not
		// mistaken for a complete one (5.2).
		right = itoa(len(s.rows)) + " mine · " + itoa(len(l.Numbers)) + " loaded of " + itoa(l.Total)
	} else {
		right = listCount(l.Phase, len(s.rows), len(l.Numbers), l.Total, l.Truncated, s.filter.String() != "", noun)
	}
	return spread(left, t.muted().Render(right)+" ", w)
}

func (s *PRList) Cursor(c *Ctx) *tea.Cursor {
	l := c.Store.PRs
	prefix := " " + string(l.Query.State) + " ▾ · mine: " + onOff(s.mine) + " · /"
	return tea.NewCursor(lipgloss.Width(prefix)+len(s.filter.value), c.Frame.SubBar.Y)
}

func (s *PRList) View(c *Ctx, area Rect) string {
	t := c.Theme
	l := c.Store.PRs
	var lines []string
	if b := banner(c, l.Err, len(l.Numbers) > 0, l.LoadedAt); b != "" {
		lines = append(lines, b)
	}
	h := area.H - len(lines)
	switch {
	case len(l.Numbers) == 0 && l.Err != nil:
		return box(append(lines, strings.Split(centered(failed(t, "Couldn't load pull requests", l.Err), Rect{W: area.W, H: h}), "\n")...), area.W, area.H)
	case len(l.Numbers) == 0 && l.Phase != PhaseComplete:
		return centered([]string{t.muted().Render(c.Spin + " Loading pull requests…")}, area)
	case len(l.Numbers) == 0:
		return centered([]string{t.muted().Render("No " + string(l.Query.State) + " pull requests.")}, area)
	case len(s.rows) == 0:
		msg := `Nothing matches "` + s.filter.String() + `". esc clears the filter.`
		if s.filter.String() == "" {
			msg = "None of the loaded pull requests are yours. m shows all."
		}
		return box(append(lines, strings.Split(centered([]string{t.muted().Render(msg)}, Rect{W: area.W, H: h}), "\n")...), area.W, area.H)
	}
	cols := prColumns(area.W)
	for r := s.offset; r < len(s.rows) && len(lines) < area.H; r++ {
		p, _ := c.Store.PR(s.rows[r])
		lines = append(lines, prRow(c, p, cols, s.rows[r] == s.sel, area.W))
	}
	return box(lines, area.W, area.H)
}

func prColumns(w int) []Placed {
	return Fit([]Col{
		{ID: ColState, Min: 2, Max: 2},
		{ID: ColNumber, Min: 6, Max: 7},
		{ID: ColTitle, Min: 24, Need: 36},
		{ID: ColBranch, Min: 12, Max: 24, Drop: 4},
		{ID: ColReview, Min: 10, Max: 19, Drop: 3},
		{ID: ColChecks, Min: 2, Max: 9, Drop: 1},
		{ID: ColUpdated, Min: 4, Max: 12, Drop: 2},
		{ID: ColAuthor, Min: 10, Max: 14, Drop: 5, MinWidth: 100},
	}, w)
}

func prRow(c *Ctx, p domain.PullRequest, cols []Placed, sel bool, w int) string {
	t := c.Theme
	words := w >= 140
	var b strings.Builder
	x := 0
	for _, col := range cols {
		if col.X > x {
			b.WriteString(strings.Repeat(" ", col.X-x))
		}
		var cell string
		switch col.ID {
		case ColState:
			cell = gutter(t, sel) + prGlyph(t, p)
		case ColNumber:
			cell = t.muted().Render("#" + itoaPlain(p.Number))
		case ColTitle:
			cell = t.text().Render(p.Title)
		case ColBranch:
			cell = t.muted().Render(p.Head)
		case ColReview:
			cell = review(t, p.Review, words)
		case ColChecks:
			cell = checks(t, p.Checks, words)
		case ColUpdated:
			if w >= 120 {
				cell = t.muted().Render(agoLong(c.Now, p.UpdatedAt))
			} else {
				cell = t.muted().Render(ago(c.Now, p.UpdatedAt))
			}
		case ColAuthor:
			cell = t.muted().Render(author(p.Author))
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
