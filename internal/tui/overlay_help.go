package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// Help lists every key enabled in the context it was opened from, grouped
// into columns with that context's group first (UI 3.13). It is generated
// from the keymap, so it cannot drift.
type Help struct {
	from    ScreenID
	enabled map[Action]bool
	offset  int
}

func newHelp(a *App, from Screen) *Help {
	c := a.ctxFor(a.theme)
	h := &Help{from: from.ID(), enabled: map[Action]bool{}}
	for _, act := range from.Actions(c) {
		h.enabled[act] = true
	}
	for _, act := range []Action{ActViewIssues, ActViewPRs, ActViewBoard, ActNextView, ActPrevView, ActRefresh, ActHelp, ActQuit} {
		h.enabled[act] = true
	}
	return h
}

func (h *Help) Mode() InputMode         { return ModeNav }
func (h *Help) Cursor(*Ctx) *tea.Cursor { return nil }
func (h *Help) Busy() bool              { return false }
func (h *Help) Actions(*Ctx) []Action   { return []Action{ActBack, ActDown, ActUp, ActHelp} }

func (h *Help) Update(c *Ctx, msg tea.Msg) (tea.Cmd, OverlayResult) {
	m, ok := msg.(ActionMsg)
	if !ok {
		return nil, Keep
	}
	switch m.A {
	case ActBack, ActHelp:
		return nil, Close
	case ActDown:
		h.offset++
	case ActUp:
		h.offset = max(0, h.offset-1)
	}
	return nil, Keep
}

var contextNames = map[ScreenID]string{
	ScreenIssues: "Issues", ScreenIssue: "Issue", ScreenPRs: "Pull requests", ScreenPR: "Pull request",
	ScreenBoard: "Board", ScreenBoardSetup: "Board",
}

// groups returns the help columns, the context's own group first.
func (h *Help) groups(c *Ctx) [][]string {
	t := c.Theme
	order := []int{0, 1, 2}
	if h.from == ScreenBoard || h.from == ScreenIssues || h.from == ScreenIssue {
		order = []int{0, 1, 2}
	}
	var cols [][]string
	for _, gi := range order {
		g := helpGroups[gi]
		var rows []string
		for _, act := range g.Actions {
			if !h.enabled[act] {
				continue
			}
			k, d := HelpText(h.from, act, c.Keys)
			if p, ok := helpPairs[act]; ok {
				k, d = p[0], p[1]
			}
			rows = append(rows, t.accent().Render(fit(k, 9))+" "+t.text().Render(d))
		}
		if len(rows) == 0 {
			continue
		}
		cols = append(cols, append([]string{t.bold().Render(g.Title)}, rows...))
	}
	return cols
}

func (h *Help) View(c *Ctx, body Rect) (string, Rect) {
	t := c.Theme
	w := body.W
	lines := []string{
		" " + t.bold().Render("Help · "+contextNames[h.from]),
		rule(t, "", w),
	}
	cols := h.groups(c)
	colW := (w - 1) / max(1, len(cols))
	rows := 0
	for _, col := range cols {
		rows = max(rows, len(col))
	}
	var table []string
	for r := 0; r < rows; r++ {
		var b strings.Builder
		b.WriteString(" ")
		for _, col := range cols {
			cell := ""
			if r < len(col) {
				cell = col[r]
			}
			b.WriteString(fit(cell, colW))
		}
		table = append(table, b.String())
	}
	h.offset = clamp(h.offset, 0, max(0, len(table)-(body.H-len(lines))))
	lines = append(lines, table[min(h.offset, len(table)):]...)
	return box(lines, w, body.H), body
}
