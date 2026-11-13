package tui

import (
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Picker is the assignee, label and status overlay (UI 3.8, tui.md 13). It
// opens in navigation mode: j/k move, space toggles, / or any other letter
// filters, enter applies, esc clears the filter first and then closes.

// PickItem is one row.
type PickItem struct {
	Key   string // what is applied: a login, a label name, a status choice
	Label string // drawn
}

type pickKind int

const (
	pickAssignees pickKind = iota
	pickLabels
	pickStatus
)

// Picker is the overlay.
type Picker struct {
	kind      pickKind
	title     string
	items     []PickItem
	initial   map[string]bool
	selected  map[string]bool
	cursor    int
	filter    lineInput
	filtering bool
	multi     bool
	max       int
	apply     func(c *Ctx, selected []string) tea.Cmd // the target decides what applying means
	rows      []int                                   // indexes into items after filtering
}

func newPicker(kind pickKind, title string, items []PickItem, initial []string, multi bool, max int,
	apply func(c *Ctx, selected []string) tea.Cmd) *Picker {
	p := &Picker{kind: kind, title: title, items: items, initial: map[string]bool{}, selected: map[string]bool{},
		multi: multi, max: max, apply: apply}
	for _, k := range initial {
		p.initial[k] = true
		p.selected[k] = true
	}
	p.refilter()
	if !multi {
		for i, r := range p.rows {
			if p.selected[p.items[r].Key] {
				p.cursor = i
			}
		}
	}
	return p
}

// setItems replaces the rows when fresh metadata lands, keeping the cursor by key.
func (p *Picker) setItems(items []PickItem) {
	key := ""
	if p.cursor < len(p.rows) {
		key = p.items[p.rows[p.cursor]].Key
	}
	p.items = items
	p.refilter()
	for i, r := range p.rows {
		if p.items[r].Key == key {
			p.cursor = i
		}
	}
}

func (p *Picker) refilter() {
	q := strings.ToLower(p.filter.String())
	p.rows = p.rows[:0]
	for i, it := range p.items {
		if q == "" || strings.Contains(strings.ToLower(it.Key+" "+it.Label), q) {
			p.rows = append(p.rows, i)
		}
	}
	p.cursor = clamp(p.cursor, 0, max(0, len(p.rows)-1))
}

func (p *Picker) Busy() bool           { return false }
func (p *Picker) Position(*Ctx) string { return "" }
func (p *Picker) Hints(*Ctx) []hint {
	if p.filtering {
		return []hint{{"enter", "keep filter"}, {"esc", "clear filter"}}
	}
	if p.multi {
		return []hint{{"j/k", "move"}, {"space", "toggle"}, {"enter", "apply"}, {"/", "filter"}, {"esc", "cancel"}}
	}
	return []hint{{"j/k", "move"}, {"enter", "apply"}, {"/", "filter"}, {"esc", "cancel"}}
}
func (p *Picker) Mode() InputMode {
	if p.filtering {
		return ModeText
	}
	return ModeNav
}

func (p *Picker) Actions(*Ctx) []Action {
	acts := []Action{ActDown, ActUp}
	if p.multi {
		acts = append(acts, ActToggle)
	}
	return append(acts, ActApply, ActFilter, ActBack)
}

func (p *Picker) Update(c *Ctx, msg tea.Msg) (tea.Cmd, OverlayResult) {
	if p.filtering {
		if k, ok := msg.(tea.KeyPressMsg); ok {
			enter, esc := p.filter.key(k)
			if esc {
				p.filter.Clear()
			}
			if enter || esc {
				p.filtering = false
			}
			p.refilter()
		}
		return nil, Keep
	}
	switch m := msg.(type) {
	case tea.KeyPressMsg:
		// Any printable letter that is not a picker key starts filtering.
		if t := m.Text; t != "" && m.Mod == 0 && !strings.ContainsAny(t, "jk ") {
			p.filtering = true
			p.filter.insert(t)
			p.refilter()
		}
	case ActionMsg:
		switch m.A {
		case ActDown, ActUp:
			p.cursor = listNav(m.A, p.cursor, len(p.rows), 2)
		case ActToggle:
			if len(p.rows) > 0 {
				k := p.items[p.rows[p.cursor]].Key
				if p.selected[k] {
					delete(p.selected, k)
				} else if p.max > 0 && len(p.selected) >= p.max {
					return c.a.toastErr("GitHub allows at most " + itoaPlain(p.max) + " assignees"), Keep
				} else {
					p.selected[k] = true
				}
			}
		case ActFilter:
			p.filtering = true
		case ActBack:
			if p.filter.String() != "" {
				p.filter.Clear()
				p.refilter()
				return nil, Keep
			}
			return nil, Close
		case ActApply:
			var sel []string
			if p.multi {
				for _, it := range p.items {
					if p.selected[it.Key] {
						sel = append(sel, it.Key)
					}
				}
			} else if len(p.rows) > 0 {
				sel = []string{p.items[p.rows[p.cursor]].Key}
			} else {
				return nil, Keep
			}
			return p.apply(c, sel), Close
		}
	}
	return nil, Keep
}

// Diff is what applying would change, against the initial selection.
func (p *Picker) diff(sel []string) SetDelta {
	var d SetDelta
	for _, k := range sel {
		if !p.initial[k] {
			d.Add = append(d.Add, k)
		}
	}
	for k := range p.initial {
		if !slices.Contains(sel, k) {
			d.Remove = append(d.Remove, k)
		}
	}
	slices.Sort(d.Remove)
	return d
}

func (p *Picker) Cursor(c *Ctx) *tea.Cursor {
	if !p.filtering {
		return nil
	}
	_, b := p.View(c, c.Frame.Body)
	return tea.NewCursor(b.X+4+len(p.filter.value), b.Y+1)
}

func (p *Picker) View(c *Ctx, body Rect) (string, Rect) {
	t := c.Theme
	w := min(44, body.W-4)
	inner := w - 4
	visible := 8
	var lines []string
	filter := t.muted().Render("/ ") + t.text().Render(p.filter.String())
	if p.filtering {
		filter = t.accent().Render("/ ") + t.text().Render(p.filter.String())
	}
	lines = append(lines, filter)
	off := clampOffset(0, p.cursor, visible, len(p.rows))
	for i := off; i < len(p.rows) && i < off+visible; i++ {
		it := p.items[p.rows[i]]
		mark := ""
		if p.multi {
			mark = "[ ] "
			if p.selected[it.Key] {
				mark = "[x] "
			}
		}
		row := gutter(t, i == p.cursor) + mark + it.Label
		if i == p.cursor {
			row = t.selected(fit(row, inner))
		}
		lines = append(lines, row)
	}
	if len(p.rows) == 0 {
		lines = append(lines, t.muted().Render(" nothing matches"))
	}
	if more := len(p.rows) - off - visible; more > 0 {
		lines = append(lines, t.muted().Render(" …"+itoa(more)+" more"))
	}
	lines = append(lines, "")
	if p.multi {
		status := itoa(len(p.selected)) + " selected"
		if p.max > 0 {
			status += " · max " + itoa(p.max)
		}
		lines = append(lines, t.muted().Render(status))
		lines = append(lines, t.accent().Render("space")+t.muted().Render(" toggle  ")+t.accent().Render("enter")+t.muted().Render(" apply  ")+t.accent().Render("esc")+t.muted().Render(" cancel"))
	} else {
		lines = append(lines, t.accent().Render("enter")+t.muted().Render(" apply  ")+t.accent().Render("esc")+t.muted().Render(" cancel"))
	}
	content := frameBox(t, p.title, lines, w)
	h := lipgloss.Height(content)
	b := Rect{X: body.X + (body.W-w)/2, Y: body.Y + max(0, (body.H-h)/2), W: w, H: h}
	return content, b
}

// frameBox draws a rounded box with a title in its top border (UI 3.8).
func frameBox(t Theme, title string, lines []string, w int) string {
	inner := w - 4
	head := "┌ " + title + " "
	top := t.accent().Render(head + strings.Repeat("─", max(0, w-1-lipgloss.Width(head))) + "┐")
	out := []string{top}
	for _, l := range lines {
		out = append(out, t.accent().Render("│")+" "+fit(l, inner)+" "+t.accent().Render("│"))
	}
	out = append(out, t.accent().Render("└"+strings.Repeat("─", w-2)+"┘"))
	return strings.Join(out, "\n")
}
