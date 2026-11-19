package tui

import (
	"context"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/pkieltyka/todos/internal/domain"
)

// The create and edit form, the comment form (UI 3.6, 3.7, tui.md 8.9, 14).
// Both are full-body overlays in text mode and handle form shortcuts before
// passing text input to the editor. They wait for GitHub (A5) before updating
// issue state.

// hint is one footer entry of an overlay.
type hint struct{ key, desc string }

type createDoneMsg struct {
	formID  uint64
	arrived time.Time
	res     domain.Change
	err     error
}

type editDoneMsg struct {
	formID  uint64
	arrived time.Time
	res     domain.Change
	err     error
}

type commentDoneMsg struct {
	formID  uint64
	arrived time.Time
	res     domain.CommentResult
	err     error
}

// formReadMsg is a GetIssue for a form: loading the body to edit, or the
// pre-check before saving (tui.md 14.4). It has its own slot so it never
// cancels, or is cancelled by, the detail screen's reads.
type formReadMsg struct {
	req      ReqID
	formID   uint64
	sent     time.Time
	precheck bool
	detail   domain.IssueDetail
	err      error
}

type formField int

const (
	fieldTitle formField = iota
	fieldAssignees
	fieldLabels
	fieldStatus
	fieldBody
)

// IssueForm creates or edits an issue.
type IssueForm struct {
	formID    uint64
	edit      bool
	number    int
	fromBoard bool

	title      lineInput
	body       Editor
	assignees  []string
	labels     []string
	status     *string
	addToBoard bool

	focus   formField
	orig    *domain.IssueDetail // edit: what the form was opened from
	loading bool
	saving  bool
	err     *domain.Error
}

func (a *App) nextFormID() uint64 { a.formSeq++; return a.formSeq }

// newCreateForm opens the create form. From the Board view the issue is
// always added to the board, in the focused column (A4).
func newCreateForm(a *App, fromBoard bool, status *string) *IssueForm {
	id := a.nextFormID()
	f := &IssueForm{formID: id, fromBoard: fromBoard, body: newEditor(id, a.deps.Env.Editor, "Describe the issue (Markdown)")}
	if fromBoard {
		f.addToBoard = true
		f.status = status
	}
	f.body.Blur()
	return f
}

// newEditForm opens the edit form. The body to edit comes from GitHub; until
// it arrives the form shows a loading line.
func newEditForm(a *App, n int) (*IssueForm, tea.Cmd) {
	id := a.nextFormID()
	f := &IssueForm{formID: id, edit: true, number: n, loading: true, body: newEditor(id, a.deps.Env.Editor, "")}
	f.body.Blur()
	return f, a.formRead(id, n, false)
}

// formRead reads one issue for a form, in its own slot.
func (a *App) formRead(formID uint64, n int, precheck bool) tea.Cmd {
	id, ctx := a.start(Slot{Kind: SlotForm, Number: n}, ReasonPrecheck)
	call := func(ctx context.Context) (domain.IssueDetail, error) { return a.deps.Service.GetIssue(ctx, n) }
	return read(a.deps.Clock, ctx, call, func(sent time.Time, v domain.IssueDetail, err error) tea.Msg {
		return formReadMsg{req: id, formID: formID, sent: sent, precheck: precheck, detail: v, err: err}
	})
}

func (f *IssueForm) Busy() bool      { return f.saving }
func (f *IssueForm) Mode() InputMode { return ModeText }
func (f *IssueForm) Actions(*Ctx) []Action {
	return nil // text mode: keys arrive raw
}
func (f *IssueForm) Renders(*Ctx) []renderReq { return f.body.Renders() }

func (f *IssueForm) Hints(c *Ctx) []hint {
	verb := "create"
	if f.edit {
		verb = "save"
	}
	hs := []hint{{"tab", "field"}, {c.Keys.Binding(ActSubmit).Help().Key, verb}, {"ctrl+r", "preview"}}
	if c.a.deps.Env.Editor != nil {
		hs = append(hs, hint{"ctrl+o", "$EDITOR"})
	}
	return append(hs, hint{"esc", "cancel"})
}

func (f *IssueForm) Position(*Ctx) string {
	if f.focus != fieldBody {
		return ""
	}
	l, col := f.body.LineCol()
	return "L" + itoaPlain(l) + ":C" + itoaPlain(col)
}

func (f *IssueForm) fields() []formField {
	if f.edit {
		return []formField{fieldTitle, fieldBody}
	}
	return []formField{fieldTitle, fieldAssignees, fieldLabels, fieldStatus, fieldBody}
}

func (f *IssueForm) setFocus(ff formField) {
	f.focus = ff
	if ff == fieldBody {
		f.body.Focus()
	} else {
		f.body.Blur()
	}
}

func (f *IssueForm) cycle(d int) {
	fs := f.fields()
	i := slices_index(fs, f.focus)
	f.setFocus(fs[(i+d+len(fs))%len(fs)])
}

func slices_index(fs []formField, x formField) int {
	for i, v := range fs {
		if v == x {
			return i
		}
	}
	return 0
}

// dirty reports unsaved text.
func (f *IssueForm) dirty() bool {
	if f.edit {
		return f.orig != nil && (f.title.String() != f.orig.Issue.Title || f.body.Value() != normalise(f.orig.Body))
	}
	return strings.TrimSpace(f.title.String()) != "" || strings.TrimSpace(f.body.Value()) != "" ||
		len(f.assignees) > 0 || len(f.labels) > 0
}

// normalise turns CRLF into LF: bodies edited on github.com often contain
// CRLF and the textarea holds LF (tui.md 14.4).
func normalise(s string) string { return strings.ReplaceAll(s, "\r\n", "\n") }

func (f *IssueForm) Update(c *Ctx, msg tea.Msg) (tea.Cmd, OverlayResult) {
	switch m := msg.(type) {
	case formReadMsg:
		return f.onRead(c, m)
	case createDoneMsg:
		if m.formID != f.formID {
			return nil, Keep
		}
		return f.onCreated(c, m)
	case editDoneMsg:
		if m.formID != f.formID {
			return nil, Keep
		}
		f.saving = false
		if m.err != nil {
			f.err = toDomainError(m.err)
			return nil, Keep
		}
		c.a.putIssue(m.res.Issue, m.arrived, false)
		d := c.Store.Detail(f.number)
		d.Body, d.BodyAsOf = f.body.Value(), m.arrived
		c.Store.version++
		return c.a.toastOK("Saved #" + itoaPlain(f.number)), Close
	case conflictChoiceMsg:
		if m.formID != f.formID {
			return nil, Keep
		}
		if m.overwrite {
			return f.sendEdit(c), Keep
		}
		f.saving = false
		return nil, Keep
	case editorDoneMsg, editorReadMsg:
		cmd, _ := f.body.Update(c, msg)
		return cmd, Keep
	case tea.PasteMsg:
		switch f.focus {
		case fieldTitle:
			f.title.insert(strings.ReplaceAll(m.Content, "\n", " "))
		case fieldBody:
			cmd, _ := f.body.Update(c, m)
			return cmd, Keep
		}
	case tea.KeyPressMsg:
		return f.onKey(c, m)
	}
	return nil, Keep
}

func (f *IssueForm) onKey(c *Ctx, k tea.KeyPressMsg) (tea.Cmd, OverlayResult) {
	pressed := k.String()
	if f.saving {
		if pressed == "esc" && !f.edit {
			c.a.pushOverlay(newConfirm("The issue may still be created. Close the form anyway?", func(*Ctx) tea.Cmd {
				return c.a.closeOverlay(f)
			}))
		}
		return nil, Keep
	}
	if f.loading {
		if pressed == "esc" {
			return nil, Close
		}
		return nil, Keep
	}
	if key.Matches(k, c.Keys.Binding(ActSubmit)) {
		return f.submit(c), Keep
	}
	switch pressed {
	case "esc":
		if f.dirty() {
			c.a.pushOverlay(newConfirm("Discard changes?", func(*Ctx) tea.Cmd { return c.a.closeOverlay(f) }))
			return nil, Keep
		}
		return nil, Close
	case "tab":
		f.cycle(1)
		return nil, Keep
	case "shift+tab":
		f.cycle(-1)
		return nil, Keep
	case "ctrl+r", "ctrl+o":
		cmd, _ := f.body.Update(c, k)
		return cmd, Keep
	}
	switch f.focus {
	case fieldTitle:
		if enter, _ := f.title.key(k); enter {
			f.cycle(1)
		}
	case fieldBody:
		cmd, _ := f.body.Update(c, k)
		return cmd, Keep
	case fieldAssignees, fieldLabels, fieldStatus:
		if pressed == "enter" {
			return f.openPicker(c), Keep
		}
	}
	return nil, Keep
}

// openPicker opens the picker for a button row; applying sets the field.
func (f *IssueForm) openPicker(c *Ctx) tea.Cmd {
	switch f.focus {
	case fieldAssignees:
		return c.a.openAssigneePicker("Assignees", f.assignees, func(_ *Ctx, sel []string) tea.Cmd {
			f.assignees = sel
			return nil
		})
	case fieldLabels:
		return c.a.openLabelPicker("Labels", f.labels, func(_ *Ctx, sel []string) tea.Cmd {
			f.labels = sel
			return nil
		})
	case fieldStatus:
		return c.a.openStatusPicker("Board status", statusChoice(f.status, f.addToBoard), false, true, !f.fromBoard,
			func(_ *Ctx, sel []string) tea.Cmd {
				f.status, f.addToBoard = parseStatusChoice(sel[0])
				if f.fromBoard {
					f.addToBoard = true // the Board view always adds (A4)
				}
				return nil
			})
	}
	return nil
}

func (f *IssueForm) submit(c *Ctx) tea.Cmd {
	title := strings.TrimSpace(f.title.String())
	if title == "" {
		f.err = &domain.Error{Kind: domain.ErrInvalidInput, Message: "A title is required"}
		f.setFocus(fieldTitle)
		return nil
	}
	f.err = nil
	if !f.edit {
		f.saving = true
		in := domain.NewIssue{Title: title, Body: f.body.Value(), Assignees: f.assignees, Labels: f.labels,
			Status: f.status, AddToBoard: f.addToBoard}
		a := c.a
		a.beginWrite(formKey(f.formID), &formWrite{overlay: f, fromBoard: f.fromBoard})
		return write(a.ctx, a.deps.Clock, func(ctx context.Context) (domain.Change, error) { return a.deps.Service.CreateIssue(ctx, in) },
			func(arrived time.Time, v domain.Change, err error) tea.Msg {
				return createDoneMsg{formID: f.formID, arrived: arrived, res: v, err: err}
			})
	}
	if f.edit && f.orig != nil && f.editPatch().Title == nil && f.editPatch().Body == nil {
		return tea.Batch(c.a.toastInfo("No changes"), c.a.closeOverlay(f))
	}
	f.saving = true
	return c.a.formRead(f.formID, f.number, true) // the conflict pre-check (4.5)
}

// editPatch sends only what changed against what the form started from.
func (f *IssueForm) editPatch() domain.EditIssue {
	var e domain.EditIssue
	if t := strings.TrimSpace(f.title.String()); f.orig != nil && t != f.orig.Issue.Title {
		e.Title = &t
	}
	if b := f.body.Value(); f.orig != nil && b != normalise(f.orig.Body) {
		e.Body = &b
	}
	return e
}

func (f *IssueForm) sendEdit(c *Ctx) tea.Cmd {
	a, n, e := c.a, f.number, f.editPatch()
	f.saving = true
	a.beginWrite(formKey(f.formID), &formWrite{overlay: f, number: n, body: f.body.Value(), hasBody: e.Body != nil})
	return write(a.ctx, a.deps.Clock, func(ctx context.Context) (domain.Change, error) { return a.deps.Service.EditIssue(ctx, n, e) },
		func(arrived time.Time, v domain.Change, err error) tea.Msg {
			return editDoneMsg{formID: f.formID, arrived: arrived, res: v, err: err}
		})
}

func (f *IssueForm) onRead(c *Ctx, m formReadMsg) (tea.Cmd, OverlayResult) {
	if m.formID != f.formID {
		return nil, Keep
	}
	slot := Slot{Kind: SlotForm, Number: f.number}
	if _, ok := c.a.accept(slot, m.req); !ok {
		return nil, Keep
	}
	c.a.finish(slot)
	if m.err != nil {
		if cancelled(m.err) {
			return nil, Keep
		}
		f.loading, f.saving = false, false
		f.err = toDomainError(m.err)
		return nil, Keep
	}
	c.a.putIssue(m.detail.Issue, m.sent, true)
	if !m.precheck { // the body to edit has arrived
		d := m.detail
		f.orig = &d
		f.loading = false
		f.title.value = []rune(d.Issue.Title)
		f.body.SetValue(normalise(d.Body))
		return nil, Keep
	}
	// The pre-check compares GitHub's current raw values with the raw values
	// the form started from (tui.md 14.4).
	if m.detail.Issue.Title == f.orig.Issue.Title && m.detail.Body == f.orig.Body {
		return f.sendEdit(c), Keep
	}
	f.saving = false // nothing is sent while the user decides; overwrite sends (sendEdit)
	c.a.pushOverlay(newConflict(f.formID, f.number))
	return nil, Keep
}

// onCreated settles a create (tui.md 8.9).
func (f *IssueForm) onCreated(c *Ctx, m createDoneMsg) (tea.Cmd, OverlayResult) {
	f.saving = false
	if m.err != nil {
		de := toDomainError(m.err)
		if de.Kind == domain.ErrPartial && de.Issue != nil {
			c.a.created(*de.Issue, m.arrived, f.fromBoard)
			return c.a.toastErr("Created #" + itoaPlain(de.Issue.Number) + ", but " + errText(de)), Close
		}
		if de.Uncertain {
			de = &domain.Error{Kind: de.Kind, Message: "The issue may have been created. Check GitHub before trying again.", Hint: de.Hint}
		}
		f.err = de
		return nil, Keep
	}
	c.a.created(m.res.Issue, m.arrived, f.fromBoard)
	return c.a.toastOK("Created #" + itoaPlain(m.res.Issue.Number)), Close
}

func (f *IssueForm) Cursor(c *Ctx) *tea.Cursor {
	if f.saving || f.loading {
		return nil
	}
	body := c.Frame.Body
	switch f.focus {
	case fieldTitle:
		return tea.NewCursor(body.X+11+len(f.title.value), body.Y+1)
	case fieldBody:
		cur := f.body.Cursor()
		if cur == nil {
			return nil
		}
		cur.X += body.X + 2
		cur.Y += body.Y + f.bodyTop()
		return cur
	}
	return nil
}

func (f *IssueForm) bodyTop() int {
	n := 3 // heading, title, rule
	if !f.edit {
		n += 3 // assignees, labels, status
	}
	if f.err != nil {
		n++
	}
	return n
}

func (f *IssueForm) View(c *Ctx, body Rect) (string, Rect) {
	t := c.Theme
	w := body.W
	heading := "New issue"
	if f.edit {
		heading = "Edit #" + itoaPlain(f.number)
	}
	mode := t.accent().Render("✎ write") + t.subtle().Render(" │ ") + t.muted().Render("preview")
	if f.body.preview {
		mode = t.muted().Render("write") + t.subtle().Render(" │ ") + t.accent().Render("◉ preview")
	}
	lines := []string{spread(" "+t.bold().Render(heading), mode+" ", w)}
	label := func(ff formField, name string) string {
		if f.focus == ff {
			return t.accent().Render(fit(name, 10))
		}
		return t.muted().Render(fit(name, 10))
	}
	if f.loading {
		lines = append(lines, "", " "+t.muted().Render(c.Spin+" Loading #"+itoaPlain(f.number)+"…"))
		return box(lines, w, body.H), body
	}
	title := f.title.String()
	if f.focus == fieldTitle {
		title = t.accent().Render("▌") + title
	} else {
		title = " " + title
	}
	lines = append(lines, " "+label(fieldTitle, "Title")+title)
	if !f.edit {
		people := t.muted().Render("—")
		if len(f.assignees) > 0 {
			people = t.text().Render("@" + strings.Join(f.assignees, " @"))
		}
		labels := t.muted().Render("—")
		if len(f.labels) > 0 {
			var ls []domain.Label
			for _, n := range f.labels {
				ls = append(ls, labelNamed(n, nil, c.Store.Meta.Labels))
			}
			labels = labelDots(t, ls)
		}
		status := t.muted().Render("Not on board")
		if f.addToBoard {
			status = t.text().Render(statusName(f.status))
		}
		btn := t.muted().Render("(enter)")
		lines = append(lines,
			spread("  "+label(fieldAssignees, "Assignees")+people, btn+" ", w),
			spread("  "+label(fieldLabels, "Labels")+labels, btn+" ", w),
			spread("  "+label(fieldStatus, "Status")+status, btn+" ", w))
	}
	if f.err != nil {
		lines = append(lines, " "+t.errorS().Render("✗ "+errText(f.err))+hintSuffixStyled(t, f.err))
	}
	ruleLabel := " Body"
	if f.focus == fieldBody {
		ruleLabel = t.accent().Render(" Body")
	}
	lines = append(lines, t.subtle().Render(strings.Repeat("─", max(0, w-lipgloss.Width(ruleLabel)-1)))+ruleLabel+" ")
	if f.saving {
		verb := "Creating"
		if f.edit {
			verb = "Saving"
		}
		lines = append(lines, " "+t.muted().Render(c.Spin+" "+verb+"…"))
	}
	f.layout(c)
	for _, l := range strings.Split(f.body.View(c), "\n") {
		lines = append(lines, "  "+l)
	}
	return box(lines, w, body.H), body
}

// layout sizes the editor to the space under the form's fields.
func (f *IssueForm) layout(c *Ctx) {
	top := f.bodyTop()
	if f.saving {
		top++
	}
	f.body.SetSize(c.Frame.Body.W-4, max(1, c.Frame.Body.H-top))
}

func hintSuffixStyled(t Theme, de *domain.Error) string {
	if h := errHint(de); h != "" {
		return " " + t.muted().Render(h)
	}
	return ""
}

// ---- comment form (UI 3.7) ----

// CommentForm adds a comment.
type CommentForm struct {
	formID  uint64
	number  int
	body    Editor
	sending bool
	err     *domain.Error
}

func newCommentForm(a *App, n int) *CommentForm {
	id := a.nextFormID()
	return &CommentForm{formID: id, number: n, body: newEditor(id, a.deps.Env.Editor, "Write a comment (Markdown)")}
}

func (f *CommentForm) Busy() bool               { return f.sending }
func (f *CommentForm) Mode() InputMode          { return ModeText }
func (f *CommentForm) Actions(*Ctx) []Action    { return nil }
func (f *CommentForm) Renders(*Ctx) []renderReq { return f.body.Renders() }
func (f *CommentForm) Position(*Ctx) string {
	l, col := f.body.LineCol()
	return "L" + itoaPlain(l) + ":C" + itoaPlain(col)
}
func (f *CommentForm) Hints(c *Ctx) []hint {
	hs := []hint{{c.Keys.Binding(ActSubmit).Help().Key, "comment"}, {"ctrl+r", "preview"}}
	if c.a.deps.Env.Editor != nil {
		hs = append(hs, hint{"ctrl+o", "$EDITOR"})
	}
	return append(hs, hint{"esc", "cancel"})
}

func (f *CommentForm) Update(c *Ctx, msg tea.Msg) (tea.Cmd, OverlayResult) {
	switch m := msg.(type) {
	case commentDoneMsg:
		if m.formID != f.formID {
			return nil, Keep
		}
		f.sending = false
		if m.err != nil {
			f.err = toDomainError(m.err)
			return nil, Keep
		}
		c.a.putIssue(m.res.Issue, m.arrived, false)
		d := c.Store.Detail(f.number)
		d.Comments = append(d.Comments, m.res.Comment)
		d.CommentsTotal++
		c.Store.version++
		return c.a.toastOK("Commented on #" + itoaPlain(f.number)), Close
	case editorDoneMsg, editorReadMsg, tea.PasteMsg:
		if !f.sending {
			cmd, _ := f.body.Update(c, msg)
			return cmd, Keep
		}
	case tea.KeyPressMsg:
		if f.sending {
			return nil, Keep
		}
		if m.String() == "esc" {
			if strings.TrimSpace(f.body.Value()) != "" {
				c.a.pushOverlay(newConfirm("Discard the comment?", func(*Ctx) tea.Cmd { return c.a.closeOverlay(f) }))
				return nil, Keep
			}
			return nil, Close
		}
		if key.Matches(m, c.Keys.Binding(ActSubmit)) {
			body := f.body.Value()
			if strings.TrimSpace(body) == "" {
				return c.a.toastErr("Comment is empty"), Keep
			}
			f.sending, f.err = true, nil
			a, n := c.a, f.number
			a.beginWrite(formKey(f.formID), &formWrite{overlay: f, number: n})
			return write(a.ctx, a.deps.Clock, func(ctx context.Context) (domain.CommentResult, error) { return a.deps.Service.Comment(ctx, n, body) },
				func(arrived time.Time, v domain.CommentResult, err error) tea.Msg {
					return commentDoneMsg{formID: f.formID, arrived: arrived, res: v, err: err}
				}), Keep
		}
		cmd, _ := f.body.Update(c, m)
		return cmd, Keep
	}
	return nil, Keep
}

func (f *CommentForm) Cursor(c *Ctx) *tea.Cursor {
	if f.sending {
		return nil
	}
	cur := f.body.Cursor()
	if cur == nil {
		return nil
	}
	top := 3
	if f.err != nil {
		top++
	}
	cur.X += c.Frame.Body.X + 2
	cur.Y += c.Frame.Body.Y + top
	return cur
}

func (f *CommentForm) View(c *Ctx, body Rect) (string, Rect) {
	t := c.Theme
	w := body.W
	i, _ := c.Issue(f.number)
	mode := t.accent().Render("✎ write") + t.subtle().Render(" │ ") + t.muted().Render("preview")
	if f.body.preview {
		mode = t.muted().Render("write") + t.subtle().Render(" │ ") + t.accent().Render("◉ preview")
	}
	lines := []string{
		" " + t.accent().Render("‹ #"+itoaPlain(f.number)) + " " + t.text().Render(i.Title),
		spread(" "+t.bold().Render("New comment"), mode+" ", w),
		t.subtle().Render(strings.Repeat("─", w)),
	}
	if f.err != nil {
		lines = append(lines, " "+t.errorS().Render("✗ "+errText(f.err))+hintSuffixStyled(t, f.err))
	}
	if f.sending {
		lines = append(lines, " "+t.muted().Render(c.Spin+" Sending…"))
	}
	f.layout(c)
	for _, l := range strings.Split(f.body.View(c), "\n") {
		lines = append(lines, "  "+l)
	}
	return box(lines, w, body.H), body
}

// layout sizes the editor under the heading.
func (f *CommentForm) layout(c *Ctx) {
	top := 3
	if f.err != nil {
		top++
	}
	if f.sending {
		top++
	}
	f.body.SetSize(c.Frame.Body.W-4, max(1, c.Frame.Body.H-top))
}
