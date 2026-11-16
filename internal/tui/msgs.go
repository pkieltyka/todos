package tui

import (
	"time"

	"github.com/pkieltyka/todos/internal/domain"
	"github.com/pkieltyka/todos/internal/markdown"
)

// ReqID identifies one read request; it only increases.
type ReqID uint64

// LoadReason says why a read was started.
type LoadReason int

const (
	ReasonInitial LoadReason = iota
	ReasonRefresh            // a reload the app starts itself, such as after a project switch; not fresh
	ReasonAuto
	ReasonQueryChanged
	ReasonOpen
	ReasonReread
	ReasonPrecheck
	// ReasonManual is the user's own refresh: the r key, and the recovery it
	// starts after an auth or setup error. Its reads are marked fresh, so
	// the service skips its memo and the read cache (cache.md 10).
	ReasonManual
)

// Reads carry the request ID and the time the request was sent (A5 rule 7).

type sessionMsg struct {
	req  ReqID
	sent time.Time
	s    domain.Session
	err  error
}

// recoveredMsg ends an auth or setup recovery: metadata was dropped and the
// session reloaded (finding 2).
type recoveredMsg struct {
	req  ReqID
	sent time.Time
	s    domain.Session
	err  error
}

type candidatesMsg struct {
	req  ReqID
	sent time.Time
	res  domain.ProjectsResult
	err  error
}

type boardChosenMsg struct {
	req     ReqID
	sent    time.Time
	project domain.Project
	err     error
}

type issuesPageMsg struct {
	req     ReqID
	sent    time.Time
	q       IssueQuery
	shadow  bool
	page    domain.Page[domain.Issue]
	err     error
	reason  LoadReason
	refresh bool
}

type prsPageMsg struct {
	req    ReqID
	sent   time.Time
	q      PRQuery
	shadow bool
	page   domain.Page[domain.PullRequest]
	err    error
}

type boardMsg struct {
	req   ReqID
	sent  time.Time
	info  domain.BoardInfo
	board domain.Board
	err   error
}

type issueDetailMsg struct {
	req    ReqID
	sent   time.Time
	number int
	reason LoadReason
	detail domain.IssueDetail
	err    error
}

type commentKind int

const (
	issueComments commentKind = iota
	prComments
)

type commentsPageMsg struct {
	req    ReqID
	sent   time.Time
	kind   commentKind
	number int
	shadow bool
	page   domain.Page[domain.Comment]
	err    error
}

type prDetailMsg struct {
	req    ReqID
	sent   time.Time
	number int
	detail domain.PRDetail
	err    error
}

// Everything else.

// renderedMsg carries a batch of finished renders.
type renderedMsg struct{ items []rendered }

type rendered struct {
	key markdown.Key
	src string
	out string
	err error
}

type resizeSettledMsg struct{ gen uint64 }

type autoRefreshMsg struct{ gen uint64 }

type spinnerTickMsg struct{ gen uint64 }

// clockTickMsg redraws once a second so relative times ("synced 12s ago",
// "3m") keep moving while nothing else happens (dogfood, Phase 4).
type clockTickMsg struct{}

type toastExpireMsg struct{ id uint64 }

type openedMsg struct{ err error }
