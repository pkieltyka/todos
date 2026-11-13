package tui

import (
	"slices"
	"time"

	"github.com/pkieltyka/todos/internal/domain"
)

// The pending-change engine (architecture section 5, tui.md 8): a replayed
// operation log. The store's base is only ever what GitHub confirmed; what is
// drawn is Effective(base, changes).

// ChangeID identifies one change; it only increases.
type ChangeID uint64

// Patch is one user action on one issue. Exactly one field is set.
type Patch struct {
	Assignees *SetDelta
	Labels    *SetDelta
	State     *StatePatch
	Board     *BoardPatch
}

// SetDelta adds and removes names (logins or label names).
type SetDelta struct{ Add, Remove []string }

// StatePatch closes (with a reason) or reopens.
type StatePatch struct {
	Open   bool
	Reason domain.StateReason // when closing: completed | not_planned
}

// BoardPatch sets a status (nil = "No status") or removes the card.
type BoardPatch struct {
	Status *string
	Remove bool
}

// Phase is where a change is.
type Phase int

const (
	Queued Phase = iota
	InFlight
	Uncertain
)

// Change is one pending change.
type Change struct {
	ID       ChangeID
	Number   int
	Patch    Patch
	Phase    Phase
	FailedAt time.Time // when it became Uncertain
}

// maxInFlight is the most changes sent at once, across issues (A5 rule 2).
const maxInFlight = 4

// PendingSet holds every pending change, per issue in order.
type PendingSet struct {
	byIssue  map[int][]*Change
	inFlight map[ChangeID]bool
	ready    []int // issues whose head is Queued and waiting for a slot, FIFO
	seq      ChangeID
	version  uint64
}

func newPendingSet() *PendingSet {
	return &PendingSet{byIssue: map[int][]*Change{}, inFlight: map[ChangeID]bool{}}
}

// Changes returns the pending changes of an issue, in order.
func (p *PendingSet) Changes(n int) []*Change { return p.byIssue[n] }

// Count is the number of pending changes (queued, in flight or uncertain).
func (p *PendingSet) Count() int {
	n := 0
	for _, cs := range p.byIssue {
		n += len(cs)
	}
	return n
}

// Numbers lists the issues with pending changes.
func (p *PendingSet) Numbers() []int {
	out := make([]int, 0, len(p.byIssue))
	for n := range p.byIssue {
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}

// add appends a change and reports whether it is the issue's head.
func (p *PendingSet) add(n int, patch Patch) *Change {
	p.seq++
	c := &Change{ID: p.seq, Number: n, Patch: patch}
	p.byIssue[n] = append(p.byIssue[n], c)
	if len(p.byIssue[n]) == 1 {
		p.ready = append(p.ready, n)
	}
	p.version++
	return c
}

// next pops issues whose head may be sent now, up to the in-flight limit, and
// marks those heads in flight.
func (p *PendingSet) next() []*Change {
	var out []*Change
	for len(p.inFlight) < maxInFlight && len(p.ready) > 0 {
		n := p.ready[0]
		p.ready = p.ready[1:]
		cs := p.byIssue[n]
		if len(cs) == 0 || cs[0].Phase != Queued {
			continue // settled meanwhile, held (uncertain), or already running
		}
		cs[0].Phase = InFlight
		p.inFlight[cs[0].ID] = true
		out = append(out, cs[0])
	}
	if len(out) > 0 {
		p.version++
	}
	return out
}

// find returns a change by ID.
func (p *PendingSet) find(id ChangeID) *Change {
	for _, cs := range p.byIssue {
		for _, c := range cs {
			if c.ID == id {
				return c
			}
		}
	}
	return nil
}

// remove drops a change and, when its issue has a next change, queues it.
func (p *PendingSet) remove(c *Change) {
	cs := p.byIssue[c.Number]
	for i, x := range cs {
		if x.ID == c.ID {
			cs = append(cs[:i:i], cs[i+1:]...)
			break
		}
	}
	if len(cs) == 0 {
		delete(p.byIssue, c.Number)
	} else {
		p.byIssue[c.Number] = cs
	}
	delete(p.inFlight, c.ID)
	p.advance(c.Number)
	p.version++
}

// advance queues an issue whose new head is Queued (A5 rules 3 and 4).
func (p *PendingSet) advance(n int) {
	if cs := p.byIssue[n]; len(cs) > 0 && cs[0].Phase == Queued && !slices.Contains(p.ready, n) {
		p.ready = append(p.ready, n)
	}
}

// hold marks an in-flight change uncertain; nothing else for its issue is
// sent until a read sent after failedAt lands (A5 rule 5).
func (p *PendingSet) hold(c *Change, failedAt time.Time) {
	c.Phase = Uncertain
	c.FailedAt = failedAt
	delete(p.inFlight, c.ID)
	p.version++
}

// drop forgets every change of an issue (it was deleted).
func (p *PendingSet) drop(n int) {
	for _, c := range p.byIssue[n] {
		delete(p.inFlight, c.ID)
	}
	delete(p.byIssue, n)
	p.version++
}

// held lists issues whose queue is held by an uncertain change.
func (p *PendingSet) held() []int {
	var out []int
	for n, cs := range p.byIssue {
		if len(cs) > 0 && cs[0].Phase == Uncertain {
			out = append(out, n)
		}
	}
	slices.Sort(out)
	return out
}

// ---- the pure fold ----

// Apply replays one patch on an issue. It has set semantics: applying it
// twice, or to an issue that already has the change, changes nothing.
func Apply(i domain.Issue, p Patch, labels []domain.Label) domain.Issue {
	switch {
	case p.Assignees != nil:
		i.Assignees = applyDelta(i.Assignees, *p.Assignees)
	case p.Labels != nil:
		names := make([]string, len(i.Labels))
		for k, l := range i.Labels {
			names[k] = l.Name
		}
		out := make([]domain.Label, 0, len(i.Labels)+len(p.Labels.Add))
		for _, name := range applyDelta(names, *p.Labels) {
			out = append(out, labelNamed(name, i.Labels, labels))
		}
		i.Labels = out
	case p.State != nil:
		if p.State.Open {
			i.State = "open"
			r := domain.StateReason("reopened")
			i.StateReason = &r
		} else {
			i.State = "closed"
			r := p.State.Reason
			i.StateReason = &r
		}
	case p.Board != nil:
		if p.Board.Remove {
			i.Board = nil
			break
		}
		b := domain.Placement{}
		if i.Board != nil {
			b = *i.Board
		}
		b.Status = p.Board.Status
		i.Board = &b
	}
	return i
}

func applyDelta(base []string, d SetDelta) []string {
	out := make([]string, 0, len(base)+len(d.Add))
	for _, s := range base {
		if !slices.Contains(d.Remove, s) {
			out = append(out, s)
		}
	}
	for _, s := range d.Add {
		if !slices.Contains(out, s) && !slices.Contains(d.Remove, s) {
			out = append(out, s)
		}
	}
	return out
}

func labelNamed(name string, have, known []domain.Label) domain.Label {
	for _, l := range have {
		if l.Name == name {
			return l
		}
	}
	for _, l := range known {
		if l.Name == name {
			return l
		}
	}
	return domain.Label{Name: name}
}

// Marks say what is pending on an issue, for drawing (UI 5.2).
type Marks struct {
	Busy, Uncertain bool
	AddedLabels     []string
	RemovedLabels   []string
	AddedPeople     []string
	RemovedPeople   []string
	State, Board    bool
}

// Effective folds every pending change of one issue onto its base, in ID
// order, and reports what is pending.
func Effective(base domain.Issue, changes []*Change, labels []domain.Label) (domain.Issue, Marks) {
	var m Marks
	eff := base
	for _, c := range changes {
		eff = Apply(eff, c.Patch, labels)
		if c.Phase == Uncertain {
			m.Uncertain = true
		} else {
			m.Busy = true
		}
		switch {
		case c.Patch.State != nil:
			m.State = true
		case c.Patch.Board != nil:
			m.Board = true
		}
	}
	if len(changes) == 0 {
		return eff, m
	}
	baseLabels := make([]string, len(base.Labels))
	for k, l := range base.Labels {
		baseLabels[k] = l.Name
	}
	effLabels := make([]string, len(eff.Labels))
	for k, l := range eff.Labels {
		effLabels[k] = l.Name
	}
	m.AddedLabels, m.RemovedLabels = diff(baseLabels, effLabels)
	m.AddedPeople, m.RemovedPeople = diff(base.Assignees, eff.Assignees)
	return eff, m
}

// diff returns what is in b but not a, and what is in a but not b.
func diff(a, b []string) (added, removed []string) {
	for _, s := range b {
		if !slices.Contains(a, s) {
			added = append(added, s)
		}
	}
	for _, s := range a {
		if !slices.Contains(b, s) {
			removed = append(removed, s)
		}
	}
	return added, removed
}
