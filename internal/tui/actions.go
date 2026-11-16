package tui

import (
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/pkieltyka/todos/internal/domain"
)

// issueActions are the per-issue actions shared by the Issues list, the
// issue detail and the board (UI 4). Quick add (A) is Phase 8.
var issueActions = []Action{ActNew, ActEdit, ActAssign, ActLabel, ActComment, ActClose, ActCloseNotPlanned,
	ActStatus, ActStatusPrev, ActStatusNext, ActDelete}

// issueAction runs one per-issue action on issue n (tui.md 8.1). status is
// the board column a new issue goes to when created from the Board view.
func (a *App) issueAction(c *Ctx, act Action, n int, fromBoard bool, status *string) tea.Cmd {
	if act == ActNew {
		a.pushOverlay(newCreateForm(a, fromBoard, status))
		return a.loadMeta()
	}
	if act == ActQuickAdd {
		return a.openQuickAdd(fromBoard, status)
	}
	i, ok := c.Issue(n) // effective: patches are computed against what is drawn (A5 rule 1)
	if !ok {
		return nil
	}
	switch act {
	case ActEdit:
		f, cmd := newEditForm(a, n)
		a.pushOverlay(f)
		return cmd
	case ActComment:
		a.pushOverlay(newCommentForm(a, n))
		return nil
	case ActDelete:
		a.pushOverlay(newDeleteConfirm(i))
		return nil
	case ActAssign:
		return a.openAssigneePicker("Assignees · #"+itoaPlain(n), i.Assignees, func(c *Ctx, sel []string) tea.Cmd {
			return a.applyPicked(n, sel, i.Assignees, func(d SetDelta) Patch { return Patch{Assignees: &d} })
		})
	case ActLabel:
		names := nameList(i.Labels)
		return a.openLabelPicker("Labels · #"+itoaPlain(n), names, func(c *Ctx, sel []string) tea.Cmd {
			return a.applyPicked(n, sel, names, func(d SetDelta) Patch { return Patch{Labels: &d} })
		})
	case ActClose:
		if i.State == "closed" {
			return a.applyChange(n, Patch{State: &StatePatch{Open: true}})
		}
		return a.applyChange(n, Patch{State: &StatePatch{Reason: "completed"}})
	case ActCloseNotPlanned:
		if i.State == "closed" {
			return a.toastInfo("#" + itoaPlain(n) + " is already closed")
		}
		return a.applyChange(n, Patch{State: &StatePatch{Reason: "not_planned"}})
	case ActStatus:
		if !a.store.Board.Loaded {
			return a.toastErr("The board isn't available" + boardReason(a.store.Board.InfoErr))
		}
		return a.openStatusPicker("Board status · #"+itoaPlain(n), statusChoice(boardStatus(i)), i.Board != nil, false, false,
			func(c *Ctx, sel []string) tea.Cmd {
				st, on := parseStatusChoice(sel[0])
				switch {
				case sel[0] == choiceRemove:
					return a.applyChange(n, Patch{Board: &BoardPatch{Remove: true}})
				case !on:
					return nil
				case i.Board != nil && sameStatus(i.Board.Status, st):
					return nil // nothing changes
				}
				return a.applyChange(n, Patch{Board: &BoardPatch{Status: st}})
			})
	case ActStatusPrev, ActStatusNext:
		return a.stepStatus(n, i, act == ActStatusNext)
	}
	return nil
}

func boardReason(err *domain.Error) string {
	if err == nil {
		return ""
	}
	return ": " + errText(err)
}

func boardStatus(i domain.Issue) (*string, bool) {
	if i.Board == nil {
		return nil, false
	}
	return i.Board.Status, true
}

// applyPicked turns a picker selection into one change with add and remove
// (tui.md 13). An empty difference changes nothing.
func (a *App) applyPicked(n int, sel, initial []string, mk func(SetDelta) Patch) tea.Cmd {
	var d SetDelta
	for _, k := range sel {
		if !slices.Contains(initial, k) {
			d.Add = append(d.Add, k)
		}
	}
	for _, k := range initial {
		if !slices.Contains(sel, k) {
			d.Remove = append(d.Remove, k)
		}
	}
	if len(d.Add) == 0 && len(d.Remove) == 0 {
		return nil
	}
	return a.applyChange(n, mk(d))
}

// stepStatus moves an issue to the neighbouring column. Stepping needs a
// current column, so an issue that is not on the board is not added
// (UI 9 #7).
func (a *App) stepStatus(n int, i domain.Issue, next bool) tea.Cmd {
	if i.Board == nil {
		return a.toastInfo("#" + itoaPlain(n) + " isn't on the board · M to add it")
	}
	order := a.columnStatuses() // nil ("No status") first, then the board's columns
	at := 0
	for k, st := range order {
		if sameStatus(st, i.Board.Status) {
			at = k
		}
	}
	to := at - 1
	if next {
		to = at + 1
	}
	if to < 0 || to >= len(order) {
		return nil
	}
	return a.applyChange(n, Patch{Board: &BoardPatch{Status: order[to]}})
}

// columnStatuses is "No status" followed by the board's columns, in the
// order the Board call returned them (finding 4).
func (a *App) columnStatuses() []*string {
	out := []*string{nil}
	for _, o := range a.store.Board.Order {
		if o.Status != nil {
			out = append(out, o.Status)
		}
	}
	return out
}

// ---- status choices ----

const (
	choicePrefix   = "status:"
	choiceNone     = "none"     // "No status": on the board, no status
	choiceNotBoard = "notboard" // create form: "Not on board"
	choiceRemove   = "remove"   // "Remove from board"
)

func statusChoice(st *string, onBoard bool) string {
	switch {
	case !onBoard:
		return choiceNotBoard
	case st == nil:
		return choiceNone
	}
	return choicePrefix + *st
}

func parseStatusChoice(k string) (*string, bool) {
	switch k {
	case choiceNone:
		return nil, true
	case choiceNotBoard, choiceRemove:
		return nil, false
	}
	s := k[len(choicePrefix):]
	return &s, true
}

// ---- pickers ----

func (a *App) openAssigneePicker(title string, initial []string, apply func(*Ctx, []string) tea.Cmd) tea.Cmd {
	p := newPicker(pickAssignees, title, a.assigneeItems(), initial, true, 10, apply)
	a.pushOverlay(p)
	return a.loadMeta()
}

func (a *App) openLabelPicker(title string, initial []string, apply func(*Ctx, []string) tea.Cmd) tea.Cmd {
	p := newPicker(pickLabels, title, a.labelItems(), initial, true, 0, apply)
	a.pushOverlay(p)
	return a.loadMeta()
}

// openStatusPicker lists the board's columns, then "No status", then either
// "Remove from board" (an issue on the board) or "Not on board" (a create
// form opened outside the Board view) (tui.md 11).
func (a *App) openStatusPicker(title, current string, onBoard, form, notOnBoard bool, apply func(*Ctx, []string) tea.Cmd) tea.Cmd {
	var items []PickItem
	for _, st := range a.columnStatuses()[1:] {
		items = append(items, PickItem{Key: choicePrefix + *st, Label: line(*st)})
	}
	items = append(items, PickItem{Key: choiceNone, Label: "No status"})
	if !form && onBoard {
		items = append(items, PickItem{Key: choiceRemove, Label: "Remove from board"})
	}
	if form && notOnBoard {
		items = append(items, PickItem{Key: choiceNotBoard, Label: "Not on board"})
	}
	a.pushOverlay(newPicker(pickStatus, title, items, []string{current}, false, 0, apply))
	return nil
}

func (a *App) assigneeItems() []PickItem {
	viewer := ""
	if s := a.store.Meta.Session; s != nil {
		viewer = s.Viewer.Login
	}
	var items []PickItem
	if viewer != "" {
		items = append(items, PickItem{Key: viewer, Label: "@me (" + line(viewer) + ")"})
	}
	for _, u := range a.store.Meta.Users {
		if u.Login != viewer {
			items = append(items, PickItem{Key: u.Login, Label: "@" + line(u.Login)})
		}
	}
	return items
}

func (a *App) labelItems() []PickItem {
	t := a.theme
	var items []PickItem
	for _, l := range a.store.Meta.Labels {
		items = append(items, PickItem{Key: l.Name, Label: t.fg(LabelColor(t, l.Color)).Render("●") + " " + line(l.Name)})
	}
	return items
}

// ---- metadata for pickers (tui.md 7.6) ----

type labelsMsg struct {
	labels []domain.Label
	err    error
}

type usersMsg struct {
	users []domain.User
	err   error
}

// loadMeta refreshes labels and users in the background; the service
// memoises them for 60 s, so this is usually free.
func (a *App) loadMeta() tea.Cmd {
	svc, ctx := a.deps.Service, a.ctx
	return tea.Batch(
		func() tea.Msg { l, err := svc.Labels(ctx); return labelsMsg{l, err} },
		func() tea.Msg { u, err := svc.AssignableUsers(ctx); return usersMsg{u, err} },
	)
}

// reloadMetaFresh is part of a manual refresh: labels and assignable users
// are read again, fresh since the key press, so a label made on github.com shows in the pickers
// after r. Only lists already in the store are reloaded; a list nothing has
// asked for yet costs nothing.
func (a *App) reloadMetaFresh() tea.Cmd {
	svc, ctx := a.deps.Service, domain.WithFreshSince(a.ctx, a.manualAt)
	var cmds []tea.Cmd
	if a.store.Meta.Labels != nil {
		cmds = append(cmds, func() tea.Msg { l, err := svc.Labels(ctx); return labelsMsg{l, err} })
	}
	if a.store.Meta.Users != nil {
		cmds = append(cmds, func() tea.Msg { u, err := svc.AssignableUsers(ctx); return usersMsg{u, err} })
	}
	return tea.Batch(cmds...)
}

func (a *App) onMeta(msg tea.Msg) {
	switch m := msg.(type) {
	case labelsMsg:
		if m.err == nil {
			a.store.Meta.Labels = m.labels
			a.refreshPickers(pickLabels, a.labelItems())
		}
	case usersMsg:
		if m.err == nil {
			a.store.Meta.Users = m.users
			a.refreshPickers(pickAssignees, a.assigneeItems())
		}
	}
}

func (a *App) refreshPickers(kind pickKind, items []PickItem) {
	for _, o := range a.overlays {
		if p, ok := o.(*Picker); ok && p.kind == kind {
			p.setItems(items)
		}
	}
}

// ---- after a create or delete (tui.md 8.9) ----

// created stores a new issue and shows it: at the top of the Issues list if
// it matches the list's query, at the bottom of its board column, selected.
func (a *App) created(i domain.Issue, arrived time.Time, fromBoard bool) {
	a.putIssue(i, arrived, false)
	a.wantReread(i.Number) // a project workflow may add it to the board
	q := a.store.Issues.Query
	viewer := ""
	if s := a.store.Meta.Session; s != nil {
		viewer = s.Viewer.Login
	}
	matches := (q.State == "open" || q.State == "all") && (!q.Mine || slices.Contains(i.Assignees, viewer))
	if matches && a.store.Issues.Phase != PhaseEmpty && !slices.Contains(a.store.Issues.Numbers, i.Number) {
		a.store.Issues.Numbers = append([]int{i.Number}, a.store.Issues.Numbers...)
		a.store.Issues.Total++
	}
	if i.Board != nil {
		a.store.Board.Extra[i.Number] = true
	}
	if l, ok := a.tabs[ViewIssues][0].(*IssueList); ok && !fromBoard {
		l.sel = i.Number
	}
	if b, ok := a.top(ViewBoard).(*Board); ok && fromBoard {
		b.sel = i.Number
	}
	a.store.version++
}

// deleted removes an issue everywhere and pops any detail showing it.
func (a *App) deleted(n int) {
	a.store.Remove(n)
	a.pending.drop(n)
	for v := range a.tabs {
		st := a.tabs[v]
		for len(st) > 1 {
			if d, ok := st[len(st)-1].(*IssueDetail); ok && d.number == n {
				st = st[:len(st)-1]
				continue
			}
			break
		}
		a.tabs[v] = st
	}
}
