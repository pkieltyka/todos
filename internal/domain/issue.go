// Package domain holds transport-independent todos data.
package domain

import "time"

type Label struct {
	NodeID                   NodeID
	Name, Description, Color string
} // six hex digits, no '#'

type IssueState string // open, closed

type StateReason string // completed, not_planned, reopened; nil for unknown/unsupported

type Placement struct {
	ItemID   NodeID
	Status   *string
	OptionID string
}

type Issue struct {
	NodeID                           NodeID
	Number                           int
	Title                            string
	State                            IssueState
	StateReason                      *StateReason
	Author                           *string
	Assignees                        []string
	AssigneeIDs                      map[string]NodeID // canonical login -> ID from this issue's assignee connection
	Labels                           []Label
	Milestone                        *string
	CommentCount                     int
	CreatedAt, UpdatedAt             time.Time
	ClosedAt                         *time.Time
	URL                              string
	Board                            *Placement // nil=not on selected board or board unavailable
	ViewerCanUpdate, ViewerCanDelete bool
}

type IssueDetail struct {
	Issue Issue
	Body  string
}

type Comment struct {
	ID        string
	Author    *string
	CreatedAt time.Time
	Body, URL string
}

type NewIssue struct {
	Title, Body       string
	Assignees, Labels []string
	AddToBoard        bool
	Status            *string
}

type EditIssue struct{ Title, Body *string } // nil=do not send; &"" clears body

type CloseOptions struct {
	Reason  string
	Comment *string
}

type Change struct {
	Issue   Issue
	Changed bool
}

type CommentResult struct {
	Issue   Issue
	Comment Comment
}

type CreateInput struct {
	RepositoryID          NodeID
	Title, Body           string
	AssigneeIDs, LabelIDs []NodeID
}

type IssuePatch struct {
	Title, Body *string
	AssigneeIDs *[]NodeID
}
