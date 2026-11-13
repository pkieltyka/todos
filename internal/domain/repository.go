// Package domain holds transport-independent todos data.
package domain

type NodeID string

type RepoRef struct{ Host, Owner, Name string }

type Repository struct {
	Ref                                  RepoRef
	NodeID                               NodeID
	URL                                  string
	IssuesEnabled, ViewerCanCreateIssues bool
	ViewerPermission                     string // GitHub ADMIN/MAINTAIN/WRITE/TRIAGE/READ, or empty
}

type User struct {
	NodeID      NodeID
	Login, Name string
}

type Session struct {
	Repo   Repository
	Viewer User
}

type CredentialInfo struct {
	Source string
	Scopes []string
} // no token field
