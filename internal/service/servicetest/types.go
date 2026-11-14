// Package servicetest provides the shared direct, in-memory Service fake.
package servicetest

import "github.com/pkieltyka/todos/internal/domain"

type Fixture struct {
	Archived      map[int]domain.Placement
	Session       domain.Session
	Credentials   domain.CredentialInfo
	Issues        []domain.IssueDetail
	IssueComments map[int][]domain.Comment
	PRs           []domain.PRDetail
	PRComments    map[int][]domain.Comment
	Labels        []domain.Label
	Users         []domain.User
	Projects      []domain.Project
	BoardInfo     domain.BoardInfo
	Board         domain.Board
	RateLimit     domain.RateLimit
	ConfigCheck   domain.DoctorCheck
}
type Arguments struct {
	Number      int
	Filter      domain.IssueFilter
	PRFilter    domain.PRFilter
	Page        domain.PageRequest
	Add, Remove []string
	Status      *string
	NewIssue    domain.NewIssue
	Edit        domain.EditIssue
	Close       domain.CloseOptions
	Text        string
	Project     domain.ProjectRef
	QuickAdd    domain.QuickAddOptions
	Index       int
}
type Call struct {
	Method               string
	Args                 Arguments
	StartOrder, EndOrder uint64
	Err                  *domain.Error
}
