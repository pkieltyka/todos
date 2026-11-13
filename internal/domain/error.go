package domain

import "time"

type ErrorKind string

const (
	ErrNetwork        ErrorKind = "network"
	ErrTimeout        ErrorKind = "timeout"
	ErrGitHub         ErrorKind = "github"
	ErrInternal       ErrorKind = "internal"
	ErrUsage          ErrorKind = "usage"
	ErrInvalidInput   ErrorKind = "invalid_input"
	ErrNotFound       ErrorKind = "not_found"
	ErrAuth           ErrorKind = "auth"
	ErrMissingScope   ErrorKind = "missing_scope"
	ErrForbidden      ErrorKind = "forbidden"
	ErrRateLimited    ErrorKind = "rate_limited"
	ErrNoRepo         ErrorKind = "no_repo"
	ErrNoBoard        ErrorKind = "no_board"
	ErrIssuesDisabled ErrorKind = "issues_disabled"
	ErrConfig         ErrorKind = "config"
	ErrPartial        ErrorKind = "partial"
)

type Error struct {
	Kind          ErrorKind
	Message, Hint string
	RetryAfter    time.Duration
	Uncertain     bool
	Completed     []string // created, commented, added_to_board; internal TUI-only removed
	Cause         ErrorKind
	Issue         *Issue
	Candidates    []Project // internal only; lets TUI show ambiguous-board picker
	Line          int       // internal parser/validation diagnostic; rendered into public message
	Err           error     // underlying error, never serialized
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}
