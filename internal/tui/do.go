package tui

import (
	"context"
	"errors"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/pkieltyka/todos/internal/domain"
)

// This file is the only code that calls the Service (tui.md 6.1).

// SlotKind names a kind of read; a slot has at most one live request.
type SlotKind int

const (
	SlotSession SlotKind = iota
	SlotRecover
	SlotForm // a form's own GetIssue (edit load, pre-check)
	SlotCandidates
	SlotChoose
	SlotBoard
	SlotIssues
	SlotPRs
	SlotIssue
	SlotIssueComments
	SlotPR
	SlotPRComments
)

// Slot is one read slot; Number is 0 for slots that are not per item.
type Slot struct {
	Kind   SlotKind
	Number int
}

// Request is the live request of a slot.
type Request struct {
	ID     ReqID
	Cancel context.CancelFunc
	Reason LoadReason
	Since  time.Time // a manual refresh's key press: its reads are fresh since then
}

// start registers a new request for slot, cancelling the previous one, and
// returns its ID and context. The first read of a manual refresh is fresh
// since the key press (sinceFor).
func (a *App) start(slot Slot, reason LoadReason) (ReqID, context.Context) {
	return a.startSince(slot, reason, a.sinceFor(reason))
}

// startSince is start with an explicit fresh time. Continuation pages pass
// the Since of the request they continue, so every page of one refresh
// carries the same time (cache.md 10): the service may then reuse what the
// refresh's first read fetched, but nothing read before the key press.
func (a *App) startSince(slot Slot, reason LoadReason, since time.Time) (ReqID, context.Context) {
	if r, ok := a.inflight[slot]; ok {
		r.Cancel()
	}
	a.reqSeq++
	ctx, cancel := context.WithCancel(a.ctx)
	if !since.IsZero() {
		ctx = domain.WithFreshSince(ctx, since)
	}
	a.inflight[slot] = &Request{ID: a.reqSeq, Cancel: cancel, Reason: reason, Since: since}
	return a.reqSeq, ctx
}

// sinceFor is the fresh time of a new read: the last key press of r for a
// manual refresh, and none otherwise.
func (a *App) sinceFor(reason LoadReason) time.Time {
	if reason == ReasonManual {
		return a.manualAt
	}
	return time.Time{}
}

// accept reports whether a result belongs to the live request of its slot,
// and clears the slot when it does.
func (a *App) accept(slot Slot, id ReqID) (*Request, bool) {
	r, ok := a.inflight[slot]
	if !ok || r.ID != id {
		return nil, false
	}
	return r, true
}

func (a *App) finish(slot Slot) {
	if r, ok := a.inflight[slot]; ok {
		r.Cancel()
		delete(a.inflight, slot)
	}
}

func (a *App) cancel(slot Slot) { a.finish(slot) }

func (a *App) busy(slot Slot) bool { _, ok := a.inflight[slot]; return ok }

// cancelled reports a result of a request we cancelled ourselves; it is
// dropped silently (A6).
func cancelled(err error) bool { return err != nil && errors.Is(err, context.Canceled) }

// read wraps a blocking call: the sent time is taken inside the Cmd, right
// before the call (tui.md 6.2).
func read[T any](clock func() time.Time, ctx context.Context, call func(context.Context) (T, error), wrap func(sent time.Time, v T, err error) tea.Msg) tea.Cmd {
	return func() tea.Msg {
		sent := clock()
		v, err := call(ctx)
		return wrap(sent, v, err)
	}
}

// ---- session ----

func (a *App) loadSession() tea.Cmd {
	id, ctx := a.start(Slot{Kind: SlotSession}, ReasonInitial)
	return read(a.deps.Clock, ctx, a.deps.Service.Session, func(sent time.Time, s domain.Session, err error) tea.Msg {
		return sessionMsg{req: id, sent: sent, s: s, err: err}
	})
}

func (a *App) onSession(m sessionMsg) tea.Cmd {
	slot := Slot{Kind: SlotSession}
	if _, ok := a.accept(slot, m.req); !ok {
		return nil
	}
	a.finish(slot)
	if m.err != nil {
		if cancelled(m.err) {
			return nil
		}
		return a.toastErr("Couldn't load the session: " + errText(toDomainError(m.err)))
	}
	s := m.s
	a.store.Meta.Session = &s
	return nil
}

// ---- issues ----

func (a *App) loadIssues(reason LoadReason) tea.Cmd {
	c := &a.store.Issues
	shadow := c.Phase != PhaseEmpty && (reason == ReasonRefresh || reason == ReasonAuto || reason == ReasonManual)
	if !shadow {
		*c = ListCache[IssueQuery]{Query: c.Query}
	} else {
		c.next = &ListCache[IssueQuery]{Query: c.Query}
	}
	return a.issuesPage(c.Query, "", shadow, reason, a.sinceFor(reason))
}

func issueFilter(q IssueQuery) domain.IssueFilter {
	f := domain.IssueFilter{State: string(q.State)}
	if q.Mine {
		f.Assignee = "@me"
	}
	return f
}

func (a *App) issuesPage(q IssueQuery, cursor string, shadow bool, reason LoadReason, since time.Time) tea.Cmd {
	id, ctx := a.startSince(Slot{Kind: SlotIssues}, reason, since)
	call := func(ctx context.Context) (domain.Page[domain.Issue], error) {
		return a.deps.Service.ListIssues(ctx, issueFilter(q), domain.PageRequest{Cursor: cursor, Size: pageSize})
	}
	return read(a.deps.Clock, ctx, call, func(sent time.Time, p domain.Page[domain.Issue], err error) tea.Msg {
		return issuesPageMsg{req: id, sent: sent, q: q, shadow: shadow, page: p, err: err}
	})
}

func (a *App) onIssuesPage(m issuesPageMsg) tea.Cmd {
	slot := Slot{Kind: SlotIssues}
	r, ok := a.accept(slot, m.req)
	c := &a.store.Issues
	if !ok || m.q != c.Query {
		return nil
	}
	if m.err != nil {
		a.finish(slot)
		if cancelled(m.err) {
			return nil
		}
		c.Err = toDomainError(m.err)
		c.next = nil
		if c.Phase == PhaseEmpty {
			c.Phase = PhaseComplete
		}
		return nil
	}
	for _, i := range m.page.Items {
		a.putIssue(i, m.sent, true)
	}
	target := c
	if m.shadow {
		if c.next == nil {
			return nil
		}
		target = c.next
	}
	nums := make([]int, len(m.page.Items))
	for i, it := range m.page.Items {
		nums[i] = it.Number
	}
	target.Numbers = appendNew(target.Numbers, nums)
	if len(target.Numbers) > listCap {
		target.Numbers = target.Numbers[:listCap]
	}
	target.Total = m.page.Total
	target.Cursor = m.page.NextCursor
	target.HasMore = m.page.HasMore
	if target.HasMore && len(target.Numbers) < listCap {
		if target.Phase == PhaseEmpty {
			target.Phase = PhaseFirstPage
		} else {
			target.Phase = PhaseBackground
		}
		return a.issuesPage(c.Query, target.Cursor, m.shadow, r.Reason, r.Since)
	}
	a.finish(slot)
	target.Phase = PhaseComplete
	target.LoadedAt = a.now()
	target.Truncated = target.Total > listCap
	target.Err = nil
	if m.shadow {
		next := *target
		next.next = nil
		*c = next
	}
	return nil
}

// ---- pull requests ----

func (a *App) loadPRs(reason LoadReason) tea.Cmd {
	c := &a.store.PRs
	shadow := c.Phase != PhaseEmpty && (reason == ReasonRefresh || reason == ReasonAuto || reason == ReasonManual)
	if !shadow {
		*c = PRListCache{Query: c.Query}
	} else {
		c.next = &PRListCache{Query: c.Query}
	}
	return a.prsPage(c.Query, "", shadow, reason, a.sinceFor(reason))
}

func (a *App) prsPage(q PRQuery, cursor string, shadow bool, reason LoadReason, since time.Time) tea.Cmd {
	id, ctx := a.startSince(Slot{Kind: SlotPRs}, reason, since)
	call := func(ctx context.Context) (domain.Page[domain.PullRequest], error) {
		return a.deps.Service.ListPRs(ctx, domain.PRFilter{State: string(q.State)}, domain.PageRequest{Cursor: cursor, Size: pageSize})
	}
	return read(a.deps.Clock, ctx, call, func(sent time.Time, p domain.Page[domain.PullRequest], err error) tea.Msg {
		return prsPageMsg{req: id, sent: sent, q: q, shadow: shadow, page: p, err: err}
	})
}

func (a *App) onPRsPage(m prsPageMsg) tea.Cmd {
	slot := Slot{Kind: SlotPRs}
	r, ok := a.accept(slot, m.req)
	c := &a.store.PRs
	if !ok || m.q != c.Query {
		return nil
	}
	if m.err != nil {
		a.finish(slot)
		if cancelled(m.err) {
			return nil
		}
		c.Err = toDomainError(m.err)
		c.next = nil
		if c.Phase == PhaseEmpty {
			c.Phase = PhaseComplete
		}
		return nil
	}
	target := c
	if m.shadow {
		if c.next == nil {
			return nil
		}
		target = c.next
	}
	nums := make([]int, len(m.page.Items))
	for i, p := range m.page.Items {
		a.store.PutPR(sanitizePR(p), m.sent)
		nums[i] = p.Number
	}
	target.Numbers = appendNew(target.Numbers, nums)
	if len(target.Numbers) > listCap {
		target.Numbers = target.Numbers[:listCap]
	}
	target.Total = m.page.Total
	target.Cursor = m.page.NextCursor
	target.HasMore = m.page.HasMore
	if target.HasMore && len(target.Numbers) < listCap {
		if target.Phase == PhaseEmpty {
			target.Phase = PhaseFirstPage
		} else {
			target.Phase = PhaseBackground
		}
		return a.prsPage(c.Query, target.Cursor, m.shadow, r.Reason, r.Since)
	}
	a.finish(slot)
	target.Phase = PhaseComplete
	target.LoadedAt = a.now()
	target.Truncated = target.Total > listCap
	target.Err = nil
	if m.shadow {
		next := *target
		next.next = nil
		*c = next
	}
	return nil
}

// ---- board ----

// loadBoard reads BoardInfo and the board together in one request, so the
// project, its Status options and the cards can never disagree (finding 4).
// With refreshMeta it first drops the service's memoised metadata.
func (a *App) loadBoard(refreshMeta bool, reason LoadReason) tea.Cmd {
	id, ctx := a.start(Slot{Kind: SlotBoard}, reason)
	svc := a.deps.Service
	type both struct {
		info  domain.BoardInfo
		board domain.Board
	}
	call := func(ctx context.Context) (both, error) {
		if refreshMeta {
			if err := svc.RefreshMetadata(ctx); err != nil {
				return both{}, err
			}
		}
		info, err := svc.BoardInfo(ctx)
		if err != nil {
			return both{}, err
		}
		board, err := svc.Board(ctx)
		return both{info, board}, err
	}
	return read(a.deps.Clock, ctx, call, func(sent time.Time, v both, err error) tea.Msg {
		return boardMsg{req: id, sent: sent, info: v.info, board: v.board, err: err}
	})
}

func (a *App) onBoard(m boardMsg) tea.Cmd {
	slot := Slot{Kind: SlotBoard}
	if _, ok := a.accept(slot, m.req); !ok {
		return nil
	}
	a.finish(slot)
	b := &a.store.Board
	if m.err != nil {
		if cancelled(m.err) {
			return nil
		}
		de := toDomainError(m.err)
		if de.Kind == domain.ErrNoBoard || de.Kind == domain.ErrMissingScope || !b.Loaded {
			b.InfoErr = de
			b.Info = nil
			b.Loaded = false
			a.tabs[ViewBoard] = []Screen{newBoardSetup(setupStateFor(de), de)}
			return nil
		}
		b.Err = de // a refresh failed: keep the board, mark it stale
		return nil
	}
	info := m.info
	b.Info = &info
	b.InfoErr = nil
	b.Order = b.Order[:0:0]
	b.Extra = map[int]bool{} // GitHub's order now places every card
	for _, col := range m.board.Columns {
		o := ColumnOrder{Status: col.Status, OptionID: col.OptionID}
		for _, card := range col.Cards {
			a.putIssue(card.Issue, m.sent, true)
			o.Numbers = append(o.Numbers, card.Number)
		}
		b.Order = append(b.Order, o)
	}
	b.Hidden = m.board.HiddenCount
	b.Truncated = m.board.Truncated
	b.CardsTruncated = m.board.CardsTruncated
	b.Loaded = true
	b.LoadedAt = a.now()
	b.Err = nil
	a.store.version++
	if top := a.top(ViewBoard); top == nil || top.ID() != ScreenBoard {
		if s, ok := top.(*BoardSetup); !ok || !s.pushed {
			a.tabs[ViewBoard] = []Screen{newBoard()}
		}
	}
	return nil
}

func (a *App) loadCandidates() tea.Cmd {
	id, ctx := a.start(Slot{Kind: SlotCandidates}, ReasonInitial)
	return read(a.deps.Clock, ctx, a.deps.Service.BoardCandidates, func(sent time.Time, v domain.ProjectsResult, err error) tea.Msg {
		return candidatesMsg{req: id, sent: sent, res: v, err: err}
	})
}

func (a *App) onCandidates(m candidatesMsg) tea.Cmd {
	slot := Slot{Kind: SlotCandidates}
	if _, ok := a.accept(slot, m.req); !ok {
		return nil
	}
	a.finish(slot)
	s, open := a.top(ViewBoard).(*BoardSetup)
	if m.err != nil {
		if cancelled(m.err) {
			return nil
		}
		// The picker leaves its loading state and shows why, with a retry
		// (finding 11).
		if open {
			s.candidatesFailed(toDomainError(m.err))
		}
		return nil
	}
	if open {
		s.setCandidates(m.res.Projects)
	}
	return nil
}

func (a *App) chooseBoard(ref domain.ProjectRef) tea.Cmd {
	id, ctx := a.start(Slot{Kind: SlotChoose}, ReasonInitial)
	call := func(ctx context.Context) (domain.Project, error) { return a.deps.Service.UseBoard(ctx, ref) }
	return read(a.deps.Clock, ctx, call, func(sent time.Time, v domain.Project, err error) tea.Msg {
		return boardChosenMsg{req: id, sent: sent, project: v, err: err}
	})
}

func (a *App) onBoardChosen(m boardChosenMsg) tea.Cmd {
	slot := Slot{Kind: SlotChoose}
	if _, ok := a.accept(slot, m.req); !ok {
		return nil
	}
	a.finish(slot)
	if m.err != nil {
		if cancelled(m.err) {
			return nil
		}
		return a.toastErr("Couldn't use that project: " + errText(toDomainError(m.err)))
	}
	// Placements belong to the old project (finding 3): forget them, and
	// supersede every read that could still bring them back. A read sent
	// before now is older than the cleared bases, so the asOf rule rejects it
	// even if it was already on its way.
	now := a.now()
	a.store.ClearPlacements(now)
	for sl := range a.inflight {
		switch sl.Kind {
		case SlotIssues, SlotIssue, SlotBoard:
			a.cancel(sl)
		}
	}
	a.store.Board = BoardCache{Extra: map[int]bool{}}
	a.tabs[ViewBoard] = []Screen{newBoard()}
	cmds := []tea.Cmd{a.loadBoard(false, ReasonInitial), a.toastOK("Using project " + line(m.project.Title))}
	if a.store.Issues.Phase != PhaseEmpty {
		cmds = append(cmds, a.loadIssues(ReasonRefresh))
	}
	for v := range a.tabs {
		for _, s := range a.tabs[v] {
			if d, ok := s.(*IssueDetail); ok {
				cmds = append(cmds, a.issueDetail(d.number, ReasonRefresh))
			}
		}
	}
	return tea.Batch(cmds...)
}

// ---- issue detail ----

func (a *App) loadIssue(n int, reason LoadReason) tea.Cmd {
	cmds := []tea.Cmd{a.issueDetail(n, reason)}
	if reason != ReasonReread && reason != ReasonPrecheck {
		d := a.store.Detail(n)
		shadow := d.CommentsPhase != PhaseEmpty
		if shadow {
			d.nextComments = []domain.Comment{}
		} else {
			d.Comments = nil
		}
		cmds = append(cmds, a.commentsPage(issueComments, n, "", shadow, reason, a.sinceFor(reason)))
	}
	return tea.Batch(cmds...)
}

func (a *App) issueDetail(n int, reason LoadReason) tea.Cmd {
	id, ctx := a.start(Slot{Kind: SlotIssue, Number: n}, reason)
	call := func(ctx context.Context) (domain.IssueDetail, error) { return a.deps.Service.GetIssue(ctx, n) }
	return read(a.deps.Clock, ctx, call, func(sent time.Time, v domain.IssueDetail, err error) tea.Msg {
		return issueDetailMsg{req: id, sent: sent, number: n, reason: reason, detail: v, err: err}
	})
}

func (a *App) onIssueDetail(m issueDetailMsg) tea.Cmd {
	slot := Slot{Kind: SlotIssue, Number: m.number}
	if _, ok := a.accept(slot, m.req); !ok {
		return nil
	}
	a.finish(slot)
	d := a.store.Detail(m.number)
	if m.err != nil {
		if cancelled(m.err) {
			return nil
		}
		d.BodyErr = toDomainError(m.err)
		a.store.version++
		return nil
	}
	a.putIssue(m.detail.Issue, m.sent, true)
	if !m.sent.Before(d.BodyAsOf) {
		d.Body = m.detail.Body
		d.BodyAsOf = m.sent
	}
	d.Loaded = true
	d.BodyErr = nil // only the body's own error: comments keep theirs
	d.LoadedAt = a.now()
	a.store.version++
	return nil
}

// commentsPage reads one page of comments under the reason and fresh time of
// the load that started it, so a manual refresh's comment pages are fresh
// since the same key press.
func (a *App) commentsPage(kind commentKind, n int, cursor string, shadow bool, reason LoadReason, since time.Time) tea.Cmd {
	sk := SlotIssueComments
	if kind == prComments {
		sk = SlotPRComments
	}
	id, ctx := a.startSince(Slot{Kind: sk, Number: n}, reason, since)
	call := func(ctx context.Context) (domain.Page[domain.Comment], error) {
		req := domain.PageRequest{Cursor: cursor, Size: pageSize}
		if kind == prComments {
			return a.deps.Service.ListPRComments(ctx, n, req)
		}
		return a.deps.Service.ListIssueComments(ctx, n, req)
	}
	return read(a.deps.Clock, ctx, call, func(sent time.Time, v domain.Page[domain.Comment], err error) tea.Msg {
		return commentsPageMsg{req: id, sent: sent, kind: kind, number: n, shadow: shadow, page: v, err: err}
	})
}

// commentState is the shared comment fields of issue and PR details.
type commentState struct {
	comments *[]domain.Comment
	next     *[]domain.Comment
	total    *int
	phase    *LoadPhase
	err      **domain.Error
}

func (a *App) onCommentsPage(m commentsPageMsg) tea.Cmd {
	a.store.version++
	sk := SlotIssueComments
	if m.kind == prComments {
		sk = SlotPRComments
	}
	slot := Slot{Kind: sk, Number: m.number}
	r, ok := a.accept(slot, m.req)
	if !ok {
		return nil
	}
	var st commentState
	if m.kind == prComments {
		d := a.store.PRDetail(m.number)
		st = commentState{&d.Comments, &d.nextComments, &d.CommentsTotal, &d.CommentsPhase, &d.CommentsErr}
	} else {
		d := a.store.Detail(m.number)
		st = commentState{&d.Comments, &d.nextComments, &d.CommentsTotal, &d.CommentsPhase, &d.CommentsErr}
	}
	if m.err != nil {
		a.finish(slot)
		if cancelled(m.err) {
			return nil
		}
		*st.err = toDomainError(m.err)
		*st.next = nil
		*st.phase = PhaseComplete
		return nil
	}
	target := st.comments
	if m.shadow {
		target = st.next
	}
	*target = append(*target, m.page.Items...)
	if len(*target) > commentsCap {
		*target = (*target)[:commentsCap]
	}
	*st.total = m.page.Total
	if m.page.HasMore && len(*target) < commentsCap {
		if !m.shadow {
			*st.phase = PhaseBackground
		}
		return a.commentsPage(m.kind, m.number, m.page.NextCursor, m.shadow, r.Reason, r.Since)
	}
	a.finish(slot)
	if m.shadow {
		*st.comments = *st.next
		*st.next = nil
	}
	*st.phase = PhaseComplete
	*st.err = nil
	return nil
}

// ---- pull request detail ----

func (a *App) loadPR(n int, reason LoadReason) tea.Cmd {
	id, ctx := a.start(Slot{Kind: SlotPR, Number: n}, reason)
	call := func(ctx context.Context) (domain.PRDetail, error) { return a.deps.Service.GetPR(ctx, n) }
	d := a.store.PRDetail(n)
	shadow := d.CommentsPhase != PhaseEmpty
	if shadow {
		d.nextComments = []domain.Comment{}
	} else {
		d.Comments = nil
	}
	return tea.Batch(
		read(a.deps.Clock, ctx, call, func(sent time.Time, v domain.PRDetail, err error) tea.Msg {
			return prDetailMsg{req: id, sent: sent, number: n, detail: v, err: err}
		}),
		a.commentsPage(prComments, n, "", shadow, reason, a.sinceFor(reason)),
	)
}

func (a *App) onPRDetail(m prDetailMsg) tea.Cmd {
	slot := Slot{Kind: SlotPR, Number: m.number}
	if _, ok := a.accept(slot, m.req); !ok {
		return nil
	}
	a.finish(slot)
	d := a.store.PRDetail(m.number)
	if m.err != nil {
		if cancelled(m.err) {
			return nil
		}
		d.DetailErr = toDomainError(m.err)
		a.store.version++
		return nil
	}
	det := m.detail
	det.PullRequest = sanitizePR(det.PullRequest)
	a.store.PutPR(det.PullRequest, m.sent)
	d.Detail = &det
	d.DetailErr = nil
	d.LoadedAt = a.now()
	a.store.version++
	return nil
}

// ---- local effects ----

func (a *App) openURL(url string) tea.Cmd {
	if url == "" || a.deps.Open == nil {
		return nil
	}
	open := a.deps.Open
	return func() tea.Msg { return openedMsg{err: open(url)} }
}

func (a *App) copyURL(url string) tea.Cmd {
	if url == "" {
		return nil
	}
	return tea.Batch(tea.SetClipboard(url), a.toastOK("Copied "+url))
}

// putIssue sanitises the short fields of an issue and stores it under the
// asOf rule. A successful read (read=true) may release an uncertain change
// (A5 rule 5); the resulting command runs after this Update.
func (a *App) putIssue(i domain.Issue, asOf time.Time, read bool) {
	a.store.Put(sanitizeIssue(i), asOf)
	if read {
		a.later = append(a.later, a.releaseUncertain(i.Number, asOf))
	}
}

func sanitizeIssue(i domain.Issue) domain.Issue {
	i.Title = line(i.Title)
	if i.Author != nil {
		s := line(*i.Author)
		i.Author = &s
	}
	if i.Milestone != nil {
		s := line(*i.Milestone)
		i.Milestone = &s
	}
	as := make([]string, len(i.Assignees))
	for k, v := range i.Assignees {
		as[k] = line(v)
	}
	i.Assignees = as
	ls := make([]domain.Label, len(i.Labels))
	for k, l := range i.Labels {
		l.Name = line(l.Name)
		ls[k] = l
	}
	i.Labels = ls
	if i.Board != nil {
		b := *i.Board
		if b.Status != nil {
			s := *b.Status
			b.Status = &s
		}
		i.Board = &b
	}
	return i
}

func sanitizePR(p domain.PullRequest) domain.PullRequest {
	p.Title = line(p.Title)
	p.Head = line(p.Head)
	p.Base = line(p.Base)
	if p.Author != nil {
		s := line(*p.Author)
		p.Author = &s
	}
	return p
}

// ---- recovery after an auth or setup error (finding 2) ----

// needsRecovery reports an error that a plain retry cannot fix: the account
// or its permissions may have changed, so the service must drop its
// memoised metadata and credentials and the session must be read again.
func needsRecovery(err *domain.Error) bool {
	if err == nil {
		return false
	}
	switch err.Kind {
	case domain.ErrAuth, domain.ErrMissingScope, domain.ErrNoRepo, domain.ErrNoBoard,
		domain.ErrIssuesDisabled, domain.ErrConfig:
		return true
	}
	return false
}

// recover refreshes metadata, then reloads the session; onRecovered restarts
// the visible read.
func (a *App) recover() tea.Cmd {
	id, ctx := a.start(Slot{Kind: SlotRecover}, ReasonManual)
	svc := a.deps.Service
	call := func(ctx context.Context) (domain.Session, error) {
		if err := svc.RefreshMetadata(ctx); err != nil {
			return domain.Session{}, err
		}
		return svc.Session(ctx)
	}
	return read(a.deps.Clock, ctx, call, func(sent time.Time, s domain.Session, err error) tea.Msg {
		return recoveredMsg{req: id, sent: sent, s: s, err: err}
	})
}

func (a *App) onRecovered(m recoveredMsg) tea.Cmd {
	slot := Slot{Kind: SlotRecover}
	if _, ok := a.accept(slot, m.req); !ok {
		return nil
	}
	a.finish(slot)
	if m.err != nil {
		if cancelled(m.err) {
			return nil
		}
		de := toDomainError(m.err)
		return a.toastErr("Still failing: " + errText(de) + hintSuffix(de))
	}
	s := m.s
	a.store.Meta.Session = &s
	return a.reload(ReasonManual)
}

func hintSuffix(de *domain.Error) string {
	if h := errHint(de); h != "" {
		return ". " + h
	}
	return ""
}
