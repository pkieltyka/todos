package tui

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/pkieltyka/todos/internal/domain"
	"github.com/pkieltyka/todos/internal/markdown"
)

// ago is a short relative time for lists: 12s, 5m, 2h, 3d, 1w, 1mo, 2y.
func ago(now, t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := now.Sub(t)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return "now" // never "0s" (Phase 9 ruling 2)
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dw", int(d.Hours()/24/7))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dmo", int(d.Hours()/24/30))
	}
	return fmt.Sprintf("%dy", int(d.Hours()/24/365))
}

// agoPhrase is a relative time inside a sentence: "just now" under a minute,
// else "5m ago" (Phase 9 ruling 2: never "0s ago").
func agoPhrase(now, t time.Time) string {
	if !t.IsZero() && now.Sub(t) < time.Minute {
		return "just now"
	}
	return ago(now, t) + " ago"
}

// agoLong is the spelled-out form for wide terminals: "3 days ago".
func agoLong(now, t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := now.Sub(t)
	if d < 0 {
		d = 0
	}
	unit := func(n int, s string) string {
		if n == 1 {
			return "1 " + s + " ago"
		}
		return strconv.Itoa(n) + " " + s + "s ago"
	}
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return unit(int(d.Minutes()), "minute")
	case d < 24*time.Hour:
		return unit(int(d.Hours()), "hour")
	case d < 7*24*time.Hour:
		return unit(int(d.Hours()/24), "day")
	case d < 30*24*time.Hour:
		return unit(int(d.Hours()/24/7), "week")
	case d < 365*24*time.Hour:
		return unit(int(d.Hours()/24/30), "month")
	}
	return unit(int(d.Hours()/24/365), "year")
}

// thousands formats 1204 as "1,204".
func thousands(n int) string {
	s := strconv.Itoa(n)
	if n < 0 {
		return "-" + thousands(-n)
	}
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// line sanitises a short field from GitHub (titles, logins, names).
func line(s string) string { return markdown.SanitizeLine(s) }

// author is "@login", or "ghost" for a deleted account.
func author(a *string) string {
	if a == nil || *a == "" {
		return "ghost"
	}
	return "@" + line(*a)
}

// statusName is a board status for display.
func statusName(s *string) string {
	if s == nil {
		return "No status"
	}
	return line(*s)
}

// errText is the message of an error for display.
func errText(err *domain.Error) string {
	if err == nil {
		return ""
	}
	if m := line(err.Message); m != "" {
		return m
	}
	return string(err.Kind)
}

// errHint is the one-line fix for an error kind (UI 5.4). The service's own
// hint wins; these are the defaults per kind.
func errHint(err *domain.Error) string {
	if err == nil {
		return ""
	}
	if h := line(err.Hint); h != "" {
		return h
	}
	switch err.Kind {
	case domain.ErrAuth:
		return "Run: gh auth login"
	case domain.ErrMissingScope:
		return "Run: gh auth refresh -s project"
	case domain.ErrRateLimited:
		if err.RetryAfter > 0 {
			return "Retry in " + err.RetryAfter.Round(time.Second).String()
		}
		return "Retry later"
	case domain.ErrNetwork, domain.ErrTimeout, domain.ErrGitHub:
		return "Press r to retry"
	case domain.ErrIssuesDisabled:
		return "Issues are disabled for this repository"
	}
	return ""
}

// toDomainError unpacks any error into a *domain.Error. Anything else (which
// the service should never return) is shown as kind internal.
func toDomainError(err error) *domain.Error {
	if err == nil {
		return nil
	}
	var de *domain.Error
	if errors.As(err, &de) && de != nil {
		return de
	}
	return &domain.Error{Kind: domain.ErrInternal, Message: err.Error(), Err: err}
}
