// Package domain holds transport-independent todos data.
package domain

import "time"

type ProjectRef struct {
	Host, Owner string
	Number      int
}

type Project struct {
	Ref                             ProjectRef
	NodeID                          NodeID
	Title, URL                      string
	Usable, Linked, ViewerCanUpdate bool
	TotalItems                      int
	UpdatedAt                       time.Time
}

type StatusOption struct{ ID, Name, Color string }

type BoardInfo struct {
	Project       Project
	StatusFieldID NodeID
	Options       []StatusOption
}

type ProjectsResult struct {
	Projects []Project
	Selected *Project
}

type Card struct{ Issue } // enough for pending changes and opening/editing a card

type Column struct {
	Status   *string
	OptionID string
	Cards    []Card
}

type Board struct {
	Project                                             Project
	Columns                                             []Column
	HiddenCount, ScannedCount, TotalItems, VisibleCount int
	Truncated, CardsTruncated                           bool // scan cap 1000; TUI display cap 500
}

type ProjectItem struct {
	RepositoryID NodeID // content repository identity; used for board scoping
	ID           NodeID
	ProjectID    NodeID
	Archived     bool
	Kind         string // Issue, PullRequest, DraftIssue, Redacted
	Repo         RepoRef
	Issue        *Issue
	Placement    Placement
}
