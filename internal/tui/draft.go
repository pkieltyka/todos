package tui

import (
	"crypto/sha256"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/pkieltyka/todos/internal/domain"
	"github.com/pkieltyka/todos/internal/quickadd"
)

// Quick-add draft persistence (functional 5.5, A7.12, tui.md 12.2). The draft
// is saved 500 ms after the last keystroke and at every step change; during a
// run the run goroutine owns it and rewrites it before each item is sent and
// after each settles. One writer at a time: the App's queue below while
// writing, the run goroutine while running.

// draftJob is one save (d != nil) or delete (d == nil).
type draftJob struct {
	repo domain.RepoRef
	d    *domain.Draft
}

type draftSavedMsg struct{ err error }

// draftQueue serialises the App's saves: one at a time, and a newer job
// replaces an unsent older one, so the latest text always wins.
type draftQueue struct {
	saving bool
	next   *draftJob
}

// saveDraft queues a save or delete.
func (a *App) saveDraft(job draftJob) tea.Cmd {
	if a.deps.Drafts == nil {
		return nil
	}
	if a.drafts.saving {
		a.drafts.next = &job
		return nil
	}
	a.drafts.saving = true
	store := a.deps.Drafts
	return func() tea.Msg {
		if job.d == nil {
			return draftSavedMsg{err: store.Delete(job.repo)}
		}
		return draftSavedMsg{err: store.Save(*job.d)}
	}
}

// onDraftSaved sends the next queued job, and reports whether the queue is
// now empty.
func (a *App) onDraftSaved() (tea.Cmd, bool) {
	a.drafts.saving = false
	if a.drafts.next != nil {
		job := *a.drafts.next
		a.drafts.next = nil
		return a.saveDraft(job), false
	}
	return nil, true
}

// repoKey is the draft's repository key: host/owner/name, lower case.
func repoKey(r domain.RepoRef) string {
	host := r.Host
	if host == "" {
		host = "github.com"
	}
	return strings.ToLower(host + "/" + r.Owner + "/" + r.Name)
}

// ---- the run's draft transitions ----

// remaining is the draft text after a run so far: quickadd.Remaining over the
// outcomes recorded so far (items with none count as unattempted), plus, when
// inFlight >= 0, the span of the item being sent. That item is never created
// or skipped yet, so it is in the text, at the sum of the retained sources
// before it.
func remaining(plan domain.QuickAddPlan, outcomes map[int]domain.QuickAddOutcome, inFlight int) (string, []domain.DraftSpan, *domain.DraftSpan) {
	res := domain.QuickAddResult{Items: make([]domain.QuickAddItemResult, len(plan.Items))}
	for i := range plan.Items {
		res.Items[i].Result = "unattempted"
		if o, ok := outcomes[i]; ok {
			res.Items[i].Result = o
		}
	}
	text, spans := quickadd.Remaining(plan, res)
	if inFlight < 0 || inFlight >= len(plan.Items) {
		return text, spans, nil
	}
	start := 0
	for i := 0; i < inFlight; i++ {
		if o := res.Items[i].Result; o != "created" && o != "skipped" {
			start += len(plan.Items[i].Source)
		}
	}
	src := plan.Items[inFlight].Source
	return text, spans, &domain.DraftSpan{StartByte: start, EndByte: start + len(src), SourceSHA256: sourceHash(src)}
}

func sourceHash(s string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(s))) }

// runDraft is the run goroutine's private draft: nothing in it is shared
// with the model.
type runDraft struct {
	d        domain.Draft
	plan     domain.QuickAddPlan
	outcomes map[int]domain.QuickAddOutcome
	store    DraftStore
	repo     domain.RepoRef
}

// apply records one progress event and saves the draft synchronously. A save
// error stops the run before the next write (core_proposal 9, A7.12).
func (r *runDraft) apply(p domain.QuickAddProgress) error {
	inFlight := -1
	switch p.Phase {
	case "creating":
		inFlight = p.Index // saved BEFORE the create is sent
	case "closing", "placing":
		return nil // the item stays in flight until it settles
	case "settled":
		r.outcomes[p.Index] = p.Outcome
	default:
		return nil
	}
	r.d.Text, r.d.Uncertain, r.d.InFlight = remaining(r.plan, r.outcomes, inFlight)
	r.d.Revision++
	if r.store == nil {
		return nil
	}
	if r.d.Text == "" && inFlight < 0 {
		return r.store.Delete(r.repo) // nothing left to send
	}
	return r.store.Save(r.d)
}

// onDraftSavedMsg advances the save queue; when it empties, a quick add
// waiting to start its run starts it, and a save error is shown once.
func (a *App) onDraftSavedMsg(m draftSavedMsg) tea.Cmd {
	next, empty := a.onDraftSaved()
	var cmds []tea.Cmd
	cmds = append(cmds, next)
	for _, o := range a.overlays {
		q, ok := o.(*QuickAdd)
		if !ok {
			continue
		}
		if q.loadWaiting && empty {
			q.loadWaiting = false
			cmds = append(cmds, a.loadDraft(q.repo))
		}
		if m.err == nil {
			q.saveErr = false // a later failure is shown again
		}
		if m.err != nil {
			if q.starting {
				q.starting = false // never run without a saved draft
				cmds = append(cmds, a.toastErr("Couldn't save the draft, so nothing was created: "+m.err.Error()))
				continue
			}
			if !q.saveErr {
				q.saveErr = true
				cmds = append(cmds, a.toastErr("Draft not saved: "+m.err.Error()))
			}
			continue
		}
		if q.starting && empty {
			cmds = append(cmds, q.startRun(a))
		}
	}
	return tea.Batch(cmds...)
}

// flushDraftNow saves an open quick add's draft synchronously; it runs on a
// graceful quit, where there is no later Update to deliver a result.
func (a *App) flushDraftNow() {
	if a.deps.Drafts == nil {
		return
	}
	for _, o := range a.overlays {
		if q, ok := o.(*QuickAdd); ok && q.loaded && !q.saveOff && q.step != qaRunning {
			if strings.TrimSpace(q.editor.Value()) == "" {
				_ = a.deps.Drafts.Delete(q.repo)
			} else {
				_ = a.deps.Drafts.Save(*q.draft())
			}
		}
	}
}

// progressBar mirrors a quick-add run in the terminal's progress bar.
func (a *App) progressBar() *tea.ProgressBar {
	for _, o := range a.overlays {
		if q, ok := o.(*QuickAdd); ok && q.step == qaRunning && len(q.rows) > 0 {
			done := 0
			for _, r := range q.rows {
				if r.phase == "settled" {
					done++
				}
			}
			return tea.NewProgressBar(tea.ProgressBarDefault, 100*done/len(q.rows))
		}
	}
	return nil
}
