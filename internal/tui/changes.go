package tui

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/pkieltyka/todos/internal/domain"
)

// changeDoneMsg is the result of one pending change's service call.
type changeDoneMsg struct {
	id      ChangeID
	arrived time.Time
	res     domain.Change
	err     error
}

// applyChange appends a change computed against the effective issue and
// sends whatever may be sent now (A5 rules 1 and 2).
func (a *App) applyChange(n int, p Patch) tea.Cmd {
	a.pending.add(n, p)
	return a.pump()
}

// pump sends queued heads while fewer than maxInFlight are in flight.
func (a *App) pump() tea.Cmd {
	var cmds []tea.Cmd
	for _, c := range a.pending.next() {
		cmds = append(cmds, a.sendChange(c))
	}
	return tea.Batch(cmds...)
}

// sendChange is the service call for one change. Writes are never cancelled
// by navigation: they use the program context (tui.md 6.3).
func (a *App) sendChange(c *Change) tea.Cmd {
	svc, n, p, id, clock, ctx := a.deps.Service, c.Number, c.Patch, c.ID, a.deps.Clock, a.ctx
	return func() tea.Msg {
		var (
			res domain.Change
			err error
		)
		switch {
		case p.Assignees != nil:
			res, err = svc.UpdateAssignees(ctx, n, p.Assignees.Add, p.Assignees.Remove)
		case p.Labels != nil:
			res, err = svc.UpdateLabels(ctx, n, p.Labels.Add, p.Labels.Remove)
		case p.State != nil && p.State.Open:
			res, err = svc.Reopen(ctx, n)
		case p.State != nil:
			res, err = svc.Close(ctx, n, domain.CloseOptions{Reason: string(p.State.Reason)})
		case p.Board != nil && p.Board.Remove:
			res, err = svc.RemoveFromBoard(ctx, n)
		case p.Board != nil:
			res, err = svc.SetStatus(ctx, n, p.Board.Status)
		}
		return changeDoneMsg{id: id, arrived: clock(), res: res, err: err}
	}
}

// onChangeDone settles a change (A5 rules 3 to 5, tui.md 8.5).
func (a *App) onChangeDone(m changeDoneMsg) tea.Cmd {
	c := a.pending.find(m.id)
	if c == nil {
		return a.pump() // the issue was deleted meanwhile; its slot is free
	}
	n := c.Number
	var cmds []tea.Cmd
	de := toDomainError(m.err)
	switch {
	case m.err == nil: // rule 3: the acknowledged issue becomes the base; no extra read
		a.pending.remove(c)
		a.putIssue(m.res.Issue, m.arrived, false)
		if c.Patch.Board != nil {
			a.store.Board.Extra[n] = true
		}
		if s := c.Patch.State; s != nil {
			if s.Open {
				cmds = append(cmds, a.toastOK("Reopened #"+itoaPlain(n)))
			} else {
				cmds = append(cmds, a.toastOK("Closed #"+itoaPlain(n)+" · x to undo"))
			}
		}
	case de.Uncertain: // rule 5: hold the queue, re-read by number
		a.pending.hold(c, m.arrived)
		cmds = append(cmds, a.issueDetail(n, ReasonReread))
	case de.Kind == domain.ErrPartial: // a step succeeded, a later one failed
		a.pending.remove(c)
		if de.Issue != nil {
			a.putIssue(*de.Issue, m.arrived, false)
			if de.Issue.Board != nil {
				a.store.Board.Extra[n] = true
			}
		}
		cmds = append(cmds, a.toastErr(partialText(c.Patch, n, de)))
	default: // rule 4: rejected; removing it from the fold is the rollback
		a.pending.remove(c)
		cmds = append(cmds, a.toastErr("Couldn't "+verb(c.Patch)+" #"+itoaPlain(n)+": "+errText(de)+hintSuffix(de)))
	}
	a.store.version++
	cmds = append(cmds, a.pump())
	return tea.Batch(cmds...)
}

// releaseUncertain is called for every successful read that put issue n with
// the given sent time. A read sent after the failure releases the hold
// (A5 rule 5); a read sent before it cannot.
func (a *App) releaseUncertain(n int, sent time.Time) tea.Cmd {
	cs := a.pending.Changes(n)
	if len(cs) == 0 || cs[0].Phase != Uncertain || sent.Before(cs[0].FailedAt) {
		return nil
	}
	c := cs[0]
	a.pending.remove(c)
	var cmd tea.Cmd
	if base, ok := a.store.Issue(n); ok && !applied(base, c.Patch, a.store.Meta.Labels) {
		cmd = a.toastErr("#" + itoaPlain(n) + ": couldn't confirm the change; showing GitHub's current state")
	}
	return tea.Batch(cmd, a.pump())
}

// applied reports whether the base already reflects the patch.
func applied(base domain.Issue, p Patch, labels []domain.Label) bool {
	after := Apply(base, p, labels)
	switch {
	case p.Assignees != nil:
		return reflect.DeepEqual(after.Assignees, base.Assignees)
	case p.Labels != nil:
		a, b := nameList(after.Labels), nameList(base.Labels)
		return reflect.DeepEqual(a, b)
	case p.State != nil:
		return after.State == base.State
	case p.Board != nil:
		if p.Board.Remove {
			return base.Board == nil
		}
		return base.Board != nil && sameStatus(base.Board.Status, p.Board.Status)
	}
	return true
}

func nameList(ls []domain.Label) []string {
	out := make([]string, len(ls))
	for i, l := range ls {
		out[i] = l.Name
	}
	return out
}

func sameStatus(a, b *string) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

// rereadHeld re-reads every issue whose queue is held (tui.md 7.7 step 2).
func (a *App) rereadHeld() tea.Cmd {
	var cmds []tea.Cmd
	for _, n := range a.pending.held() {
		if !a.busy(Slot{Kind: SlotIssue, Number: n}) {
			cmds = append(cmds, a.issueDetail(n, ReasonReread))
		}
	}
	return tea.Batch(cmds...)
}

func verb(p Patch) string {
	switch {
	case p.Assignees != nil:
		return "change the assignees of"
	case p.Labels != nil:
		return "change the labels of"
	case p.State != nil && p.State.Open:
		return "reopen"
	case p.State != nil:
		return "close"
	case p.Board != nil && p.Board.Remove:
		return "remove from the board"
	}
	return "move"
}

func partialText(p Patch, n int, de *domain.Error) string {
	done := strings.Join(de.Completed, ", ")
	if done == "" {
		done = "part of it"
	}
	return "#" + itoaPlain(n) + ": " + done + " done, then failed to " + verb(p) + " it: " + errText(de)
}

// pendingWrites counts what quitting would abandon: pending changes and busy
// overlays (tui.md 8.8).
func (a *App) pendingWrites() int {
	return a.pending.Count() + len(a.writes)
}

// ---- form writes (create, edit, comment, delete) ----

// formWrite is a create, edit, comment or delete in flight. Its result is
// normally handled by the overlay that started it; if that overlay was closed
// meanwhile, the App handles it, so the result is never lost (Phase 6 review).
type formWrite struct {
	overlay   Overlay
	fromBoard bool   // create
	number    int    // edit, comment, delete
	body      string // edit: the body sent
	hasBody   bool
}

func formKey(id uint64) string { return "form:" + itoaPlain(int(id)) }
func deleteKey(n int) string   { return "delete:" + itoaPlain(n) }

func (a *App) beginWrite(key string, w *formWrite) { a.writes[key] = w }

// endWrite forgets a write and reports it, with whether its overlay is still
// open to handle the result.
func (a *App) endWrite(key string) (*formWrite, bool) {
	w, ok := a.writes[key]
	if !ok {
		return nil, false
	}
	delete(a.writes, key)
	return w, slices.Contains(a.overlays, w.overlay)
}

// onWriteResult routes a form write's result: to its overlay when it is still
// open, else handled here.
func (a *App) onWriteResult(msg tea.Msg) tea.Cmd {
	var key string
	switch m := msg.(type) {
	case createDoneMsg:
		key = formKey(m.formID)
	case editDoneMsg:
		key = formKey(m.formID)
	case commentDoneMsg:
		key = formKey(m.formID)
	case deleteDoneMsg:
		key = deleteKey(m.number)
	}
	w, open := a.endWrite(key)
	if w == nil || open {
		return a.toOverlays(msg)
	}
	switch m := msg.(type) {
	case createDoneMsg:
		if m.err != nil {
			de := toDomainError(m.err)
			if de.Kind == domain.ErrPartial && de.Issue != nil {
				a.created(*de.Issue, m.arrived, w.fromBoard)
				return a.toastErr("Created #" + itoaPlain(de.Issue.Number) + ", but " + errText(de))
			}
			if de.Uncertain {
				return a.toastErr("The issue may have been created. Check GitHub before trying again.")
			}
			return a.toastErr("Couldn't create the issue: " + errText(de) + hintSuffix(de))
		}
		a.created(m.res.Issue, m.arrived, w.fromBoard)
		return a.toastOK("Created #" + itoaPlain(m.res.Issue.Number))
	case editDoneMsg:
		if m.err != nil {
			de := toDomainError(m.err)
			return a.toastErr("Couldn't save #" + itoaPlain(w.number) + ": " + errText(de) + hintSuffix(de))
		}
		a.putIssue(m.res.Issue, m.arrived, false)
		if w.hasBody {
			d := a.store.Detail(w.number)
			d.Body, d.BodyAsOf = w.body, m.arrived
		}
		a.store.version++
		return a.toastOK("Saved #" + itoaPlain(w.number))
	case commentDoneMsg:
		if m.err != nil {
			de := toDomainError(m.err)
			return a.toastErr("Couldn't comment on #" + itoaPlain(w.number) + ": " + errText(de) + hintSuffix(de))
		}
		a.putIssue(m.res.Issue, m.arrived, false)
		if d, ok := a.store.PeekDetail(w.number); ok {
			d.Comments = append(d.Comments, m.res.Comment)
			d.CommentsTotal++
		}
		a.store.version++
		return a.toastOK("Commented on #" + itoaPlain(w.number))
	case deleteDoneMsg:
		if m.err != nil {
			de := toDomainError(m.err)
			return a.toastErr("Couldn't delete #" + itoaPlain(m.number) + ": " + errText(de) + hintSuffix(de))
		}
		a.deleted(m.number)
		return a.toastOK("Deleted #" + itoaPlain(m.number))
	}
	return nil
}

// write wraps a blocking write with the arrival time (tui.md 6.2).
func write[T any](ctx context.Context, clock func() time.Time, call func(context.Context) (T, error), wrap func(arrived time.Time, v T, err error) tea.Msg) tea.Cmd {
	return func() tea.Msg {
		v, err := call(ctx)
		return wrap(clock(), v, err)
	}
}

// isHeld reports an issue whose queue waits on an uncertain change.
func (a *App) isHeld(n int) bool {
	cs := a.pending.Changes(n)
	return len(cs) > 0 && cs[0].Phase == Uncertain
}
