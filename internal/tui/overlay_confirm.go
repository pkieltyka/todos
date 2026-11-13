package tui

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/pkieltyka/todos/internal/domain"
)

// ---- Confirm: a yes/no box (discard, quit with writes in flight) ----

// Confirm asks one question; y runs onYes, esc closes.
type Confirm struct {
	text  string
	onYes func(c *Ctx) tea.Cmd
	quit  bool // the quit confirmation: a second ctrl+c quits at once
}

func newConfirm(text string, onYes func(c *Ctx) tea.Cmd) *Confirm {
	return &Confirm{text: text, onYes: onYes}
}

func (o *Confirm) Busy() bool              { return false }
func (o *Confirm) Mode() InputMode         { return ModeNav }
func (o *Confirm) Cursor(*Ctx) *tea.Cursor { return nil }
func (o *Confirm) Position(*Ctx) string    { return "" }
func (o *Confirm) Actions(*Ctx) []Action   { return []Action{ActYes, ActBack} }
func (o *Confirm) Hints(*Ctx) []hint       { return []hint{{"y", "yes"}, {"esc", "no"}} }
func (o *Confirm) Update(c *Ctx, msg tea.Msg) (tea.Cmd, OverlayResult) {
	m, ok := msg.(ActionMsg)
	if !ok {
		return nil, Keep
	}
	switch m.A {
	case ActYes:
		return o.onYes(c), Close
	case ActBack:
		return nil, Close
	}
	return nil, Keep
}

func (o *Confirm) View(c *Ctx, body Rect) (string, Rect) {
	t := c.Theme
	w := min(max(lipgloss.Width(o.text)+6, 40), body.W-4)
	lines := []string{t.text().Render(o.text), "", t.accent().Render("y") + t.muted().Render(" yes   ") + t.accent().Render("esc") + t.muted().Render(" no")}
	content := frameBox(t, "Confirm", lines, w)
	h := lipgloss.Height(content)
	return content, Rect{X: body.X + (body.W-w)/2, Y: body.Y + max(0, (body.H-h)/2), W: w, H: h}
}

// ---- Conflict: the edit conflict box (4.5, UI 3.6) ----

// conflictChoiceMsg tells the form what the user chose.
type conflictChoiceMsg struct {
	formID    uint64
	overwrite bool
}

// Conflict says the issue changed on GitHub since the form opened.
type Conflict struct {
	formID uint64
	number int
}

func newConflict(formID uint64, n int) *Conflict { return &Conflict{formID: formID, number: n} }

func (o *Conflict) Busy() bool              { return false }
func (o *Conflict) Mode() InputMode         { return ModeNav }
func (o *Conflict) Cursor(*Ctx) *tea.Cursor { return nil }
func (o *Conflict) Position(*Ctx) string    { return "" }
func (o *Conflict) Actions(*Ctx) []Action   { return []Action{ActOverwrite, ActBack} }
func (o *Conflict) Hints(*Ctx) []hint {
	return []hint{{"o", "overwrite with my version"}, {"esc", "keep editing"}}
}

func (o *Conflict) Update(c *Ctx, msg tea.Msg) (tea.Cmd, OverlayResult) {
	m, ok := msg.(ActionMsg)
	if !ok {
		return nil, Keep
	}
	id := o.formID
	switch m.A {
	case ActOverwrite:
		return func() tea.Msg { return conflictChoiceMsg{formID: id, overwrite: true} }, Close
	case ActBack:
		return func() tea.Msg { return conflictChoiceMsg{formID: id} }, Close
	}
	return nil, Keep
}

func (o *Conflict) View(c *Ctx, body Rect) (string, Rect) {
	t := c.Theme
	w := min(58, body.W-4)
	lines := []string{
		t.text().Render("The title or body was changed by someone else."),
		t.accent().Render("o") + t.muted().Render(" overwrite with my version   ") + t.accent().Render("esc") + t.muted().Render(" keep editing"),
	}
	content := frameBox(t, "#"+itoaPlain(o.number)+" changed on GitHub since you started editing", lines, w)
	h := lipgloss.Height(content)
	return content, Rect{X: body.X + (body.W-w)/2, Y: body.Y + max(0, (body.H-h)/2), W: w, H: h}
}

// ---- DeleteConfirm: type the number to delete (UI 3.12) ----

type deleteDoneMsg struct {
	number int
	err    error
}

// DeleteConfirm deletes an issue permanently after the number is typed. It
// waits for GitHub: the row disappears only after GitHub confirms (4.4).
type DeleteConfirm struct {
	number   int
	title    string
	input    lineInput
	deleting bool
	warn     bool // GitHub says the viewer cannot delete
	err      *domain.Error
}

func newDeleteConfirm(i domain.Issue) *DeleteConfirm {
	return &DeleteConfirm{number: i.Number, title: i.Title, warn: !i.ViewerCanDelete}
}

func (o *DeleteConfirm) Busy() bool            { return o.deleting }
func (o *DeleteConfirm) Mode() InputMode       { return ModeText }
func (o *DeleteConfirm) Actions(*Ctx) []Action { return nil }
func (o *DeleteConfirm) Position(*Ctx) string  { return "" }
func (o *DeleteConfirm) Hints(*Ctx) []hint     { return []hint{{"enter", "delete"}, {"esc", "cancel"}} }

func (o *DeleteConfirm) Update(c *Ctx, msg tea.Msg) (tea.Cmd, OverlayResult) {
	switch m := msg.(type) {
	case deleteDoneMsg:
		if m.number != o.number {
			return nil, Keep
		}
		o.deleting = false
		if m.err != nil {
			o.err = toDomainError(m.err)
			return nil, Keep // the box stays open until esc (UI 3.12)
		}
		c.a.deleted(o.number)
		return c.a.toastOK("Deleted #" + itoaPlain(o.number)), Close
	case tea.KeyPressMsg:
		if o.deleting {
			return nil, Keep
		}
		enter, esc := o.input.key(m)
		if esc {
			return nil, Close
		}
		if enter && o.input.String() == strconv.Itoa(o.number) {
			o.deleting, o.err = true, nil
			a, n := c.a, o.number
			a.beginWrite(deleteKey(n), &formWrite{overlay: o, number: n})
			return func() tea.Msg {
				return deleteDoneMsg{number: n, err: a.deps.Service.Delete(a.ctx, n)}
			}, Keep
		}
		// Only digits are accepted.
		o.input.value = []rune(strings.Map(func(r rune) rune {
			if r >= '0' && r <= '9' {
				return r
			}
			return -1
		}, o.input.String()))
	}
	return nil, Keep
}

func (o *DeleteConfirm) Cursor(c *Ctx) *tea.Cursor {
	if o.deleting {
		return nil
	}
	_, b := o.View(c, c.Frame.Body)
	prompt := "Type " + itoaPlain(o.number) + " to confirm: "
	row := 4
	if o.warn {
		row++
	}
	return tea.NewCursor(b.X+2+lipgloss.Width(prompt)+len(o.input.value), b.Y+row)
}

func (o *DeleteConfirm) View(c *Ctx, body Rect) (string, Rect) {
	t := c.Theme
	w := min(60, body.W-4)
	lines := []string{t.text().Render(`"` + o.title + `"`), t.text().Render("This can't be undone, and deletes its comments too.")}
	if o.warn {
		lines = append(lines, t.pending().Render("GitHub will probably refuse: you need admin rights."))
	}
	lines = append(lines, "", t.text().Render("Type "+itoaPlain(o.number)+" to confirm: ")+t.text().Render(o.input.String()))
	switch {
	case o.deleting:
		lines = append(lines, "", t.muted().Render(c.Spin+" Deleting…"))
	case o.err != nil:
		lines = append(lines, "", t.errorS().Render("✗ "+errText(o.err))+hintSuffixStyled(t, o.err))
	default:
		lines = append(lines, "", t.accent().Render("enter")+t.muted().Render(" delete   ")+t.accent().Render("esc")+t.muted().Render(" cancel"))
	}
	inner := w - 4
	head := "┌ Delete #" + itoaPlain(o.number) + " permanently "
	top := t.errorS().Render(head + strings.Repeat("─", max(0, w-1-lipgloss.Width(head))) + "┐")
	out := []string{top}
	for _, l := range lines {
		out = append(out, t.errorS().Render("│")+" "+fit(l, inner)+" "+t.errorS().Render("│"))
	}
	out = append(out, t.errorS().Render("└"+strings.Repeat("─", w-2)+"┘"))
	content := strings.Join(out, "\n")
	h := len(out)
	return content, Rect{X: body.X + (body.W-w)/2, Y: body.Y + max(0, (body.H-h)/2), W: w, H: h}
}
