package tui

import (
	"os"
	"os/exec"
	"strings"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
)

// Editor is the body editor of the create, edit and comment forms
// (tui.md 14, functional 4.8): a plain textarea, a Markdown preview toggle
// and a hand-off to $VISUAL / $EDITOR. It lives in package tui, not a
// subpackage, because the preview renders through Ctx.

// editorDoneMsg ends an external editor session.
type editorDoneMsg struct {
	owner uint64
	path  string
	err   error
}

// editorReadMsg carries the file the external editor saved.
type editorReadMsg struct {
	owner uint64
	text  string
	err   error
}

// Editor is the textarea plus preview.
type Editor struct {
	ta      textarea.Model
	preview bool
	vp      viewport.Model
	owner   uint64
	ext     []string
	w, h    int
	origNL  bool // the value had a trailing newline before the hand-off
}

func newEditor(owner uint64, ext []string, placeholder string) Editor {
	ta := textarea.New()
	ta.ShowLineNumbers = false
	ta.MaxHeight = 0 // no line limit; the default of 99 would cap bodies
	ta.CharLimit = 0
	ta.Prompt = ""
	ta.Placeholder = placeholder
	st := ta.Styles()
	st.Cursor.Blink = false // no blink timer: the cursor is the terminal's
	ta.SetStyles(st)
	ta.SetVirtualCursor(false)
	_ = ta.Focus()
	return Editor{ta: ta, vp: viewport.New(), owner: owner, ext: ext}
}

func (e *Editor) SetSize(w, h int) {
	e.w, e.h = w, h
	e.ta.SetWidth(w)
	e.ta.SetHeight(h)
	e.vp.SetWidth(w)
	e.vp.SetHeight(h)
}

func (e *Editor) Value() string     { return e.ta.Value() }
func (e *Editor) SetValue(s string) { e.ta.SetValue(s); e.ta.MoveToEnd() }
func (e *Editor) Focus()            { _ = e.ta.Focus() }
func (e *Editor) Blur()             { e.ta.Blur() }

// LineCol is the 1-based cursor position for the footer ("L4:C19").
func (e *Editor) LineCol() (int, int) { return e.ta.Line() + 1, e.ta.Column() + 1 }

// Renders asks for the preview's Markdown when the preview is open.
func (e *Editor) Renders() []renderReq {
	if !e.preview {
		return nil
	}
	return []renderReq{{Src: e.Value(), Width: mdWidth(e.w)}}
}

// Update handles keys and paste for the editor. It returns handled=false for
// keys the owner must handle (esc, tab, ctrl+s, …).
func (e *Editor) Update(c *Ctx, msg tea.Msg) (tea.Cmd, bool) {
	switch m := msg.(type) {
	case tea.KeyPressMsg:
		switch m.String() {
		case "ctrl+r":
			e.preview = !e.preview
			if e.preview {
				e.vp.GotoTop()
			}
			return nil, true
		case "ctrl+o":
			if e.ext == nil {
				return nil, true
			}
			return e.handOff(c), true
		}
		if e.preview {
			switch m.String() {
			case "j", "down":
				e.vp.ScrollDown(1)
			case "k", "up":
				e.vp.ScrollUp(1)
			case "pgdown", "ctrl+d":
				e.vp.HalfPageDown()
			case "pgup", "ctrl+u":
				e.vp.HalfPageUp()
			}
			return nil, true // the preview is read-only
		}
		var cmd tea.Cmd
		e.ta, cmd = e.ta.Update(m)
		return cmd, true
	case tea.PasteMsg:
		if e.preview {
			return nil, true
		}
		var cmd tea.Cmd
		e.ta, cmd = e.ta.Update(m)
		return cmd, true
	case editorDoneMsg:
		if m.owner != e.owner {
			return nil, false
		}
		path, failed := m.path, m.err
		return func() tea.Msg {
			b, rerr := os.ReadFile(path)
			_ = os.Remove(path)
			if failed != nil {
				return editorReadMsg{owner: e.owner, err: failed}
			}
			return editorReadMsg{owner: e.owner, text: string(b), err: rerr}
		}, true
	case editorReadMsg:
		if m.owner != e.owner {
			return nil, false
		}
		if m.err != nil {
			return c.a.toastErr("Editor exited with an error; body unchanged"), true
		}
		text := m.text
		if !e.origNL {
			text = strings.TrimSuffix(text, "\n") // editors add one
		}
		e.SetValue(text)
		return nil, true
	}
	return nil, false
}

// handOff writes the draft to a temp .md file and runs the user's editor on
// it (tui.md 14.3). The text travels only in the file, never as an argument
// or through a shell.
func (e *Editor) handOff(c *Ctx) tea.Cmd {
	f, err := os.CreateTemp("", "todos-*.md")
	if err != nil {
		return c.a.toastErr("Couldn't open the editor: " + err.Error())
	}
	v := e.Value()
	e.origNL = strings.HasSuffix(v, "\n")
	_, werr := f.WriteString(v)
	cerr := f.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(f.Name())
		return c.a.toastErr("Couldn't open the editor: could not write the draft")
	}
	cmd := exec.Command(e.ext[0], append(append([]string{}, e.ext[1:]...), f.Name())...)
	owner, path := e.owner, f.Name()
	return c.a.deps.Exec(cmd, func(err error) tea.Msg { return editorDoneMsg{owner: owner, path: path, err: err} })
}

// View draws the textarea, or the rendered preview.
func (e *Editor) View(c *Ctx) string {
	if !e.preview {
		return e.ta.View()
	}
	src := e.Value()
	if strings.TrimSpace(src) == "" {
		return box([]string{c.Theme.muted().Render("Nothing to preview.")}, e.w, e.h)
	}
	e.vp.SetContent(c.Markdown(src, mdWidth(e.w)))
	return e.vp.View()
}

// Cursor is the textarea's real cursor, relative to the editor's area.
func (e *Editor) Cursor() *tea.Cursor {
	if e.preview {
		return nil
	}
	return e.ta.Cursor()
}
