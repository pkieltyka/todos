package domain

import "time"

// ChangeToken is evidence of an unchanged issue collection, not a snapshot.
type ChangeToken struct {
	Viewer         string
	IssuesTotal    int
	NewestIssue    int
	NewestUpdated  time.Time
	ProjectUpdated time.Time
	ProjectItems   int
	ServerTime     time.Time
}

// Equal excludes the response's clock: only GitHub's data identifies a token.
func (t ChangeToken) Equal(other ChangeToken) bool {
	return t.Viewer == other.Viewer && t.IssuesTotal == other.IssuesTotal &&
		t.NewestIssue == other.NewestIssue && t.NewestUpdated.Equal(other.NewestUpdated) &&
		t.ProjectUpdated.Equal(other.ProjectUpdated) && t.ProjectItems == other.ProjectItems
}
