package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

// The delayed re-read (Phase 9 ruling, tui.md 8.12). GitHub project workflows
// act a few seconds after our write: a new issue is auto-added to a column, a
// card moved to Done gets its issue closed. After a create, a status change or
// a remove-from-board settles successfully, the issue is read once more,
// rereadAfter later, through the normal read path under the asOf rule. It is
// the one exception to "no extra read after a successful change".

const rereadAfter = 5 * time.Second

type rereadMsg struct {
	number int
	gen    uint64
}

// rereads holds, per issue, the generation of its timer. An entry whose timer
// fired while the issue had pending changes stays as owed: the next settle on
// that issue schedules a new timer.
type rereads struct {
	gen  uint64
	due  map[int]uint64
	want []int // issues to schedule at the end of this Update
}

// wantReread asks for a delayed re-read of n; the timer starts at the end of
// the current Update (flushRereads), replacing any running one for n.
func (a *App) wantReread(n int) { a.rr.want = append(a.rr.want, n) }

// settled is called after any change on n settles successfully: a newer
// change restarts a running or owed timer.
func (a *App) settled(n int) {
	if _, ok := a.rr.due[n]; ok {
		a.wantReread(n)
	}
}

func (a *App) flushRereads() tea.Cmd {
	if len(a.rr.want) == 0 {
		return nil
	}
	var cmds []tea.Cmd
	for _, n := range a.rr.want {
		a.rr.gen++
		gen := a.rr.gen
		a.rr.due[n] = gen
		cmds = append(cmds, a.deps.Tick(rereadAfter, func(time.Time) tea.Msg { return rereadMsg{n, gen} }))
	}
	a.rr.want = nil
	return tea.Batch(cmds...)
}

func (a *App) onReread(m rereadMsg) tea.Cmd {
	if a.rr.due[m.number] != m.gen {
		return nil // restarted or cancelled
	}
	if _, ok := a.store.Issue(m.number); !ok {
		delete(a.rr.due, m.number) // deleted meanwhile
		return nil
	}
	if len(a.pending.Changes(m.number)) > 0 {
		a.rr.due[m.number] = 0 // owed: the next settle schedules it
		return nil
	}
	delete(a.rr.due, m.number)
	return a.issueDetail(m.number, ReasonReread)
}

// cancelRereads stops every timer, on quit. A deleted issue's timer finds
// the issue gone when it fires and does nothing.
func (a *App) cancelRereads() {
	a.rr.due = map[int]uint64{}
	a.rr.want = nil
}
