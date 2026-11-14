// Package domain holds transport-independent todos data.
package domain

import "time"

type PRState string // open, closed, merged

type ReviewStatus string // approved, changes_requested, review_required, none

type ChecksStatus string // passing, failing, pending, none

type Mergeable string // mergeable, conflicting, unknown

type PullRequest struct {
	NodeID               NodeID
	Number               int
	Title                string
	State                PRState
	Draft                bool
	Author               *string
	Head, Base           string
	Review               ReviewStatus
	Checks               ChecksStatus
	Assignees            []string
	Labels               []Label
	CreatedAt, UpdatedAt time.Time
	ClosedAt, MergedAt   *time.Time
	URL                  string
	CommentCount         int
}

type PRDetail struct {
	PullRequest PullRequest
	Body        string
	Mergeable   Mergeable
	Closes      []int
}
