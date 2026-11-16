// Package service defines the shared CLI and TUI use-case contracts.
package service

import (
	"context"
	"github.com/pkieltyka/todos/internal/domain"
)

type Backend interface {
	CredentialKey(context.Context) (string, error)
	Credentials(context.Context) (domain.CredentialInfo, error)
	DoctorScopes(context.Context) (domain.CredentialInfo, error)
	Viewer(context.Context) (domain.User, error)
	Repository(context.Context, domain.RepoRef) (domain.Repository, error)
	ListLabels(context.Context, domain.RepoRef, domain.PageRequest) (domain.Page[domain.Label], error)
	ListAssignableUsers(context.Context, domain.RepoRef, domain.PageRequest) (domain.Page[domain.User], error)
	ListIssues(context.Context, domain.RepoRef, domain.IssueFilter, domain.PageRequest, domain.NodeID) (domain.Page[domain.Issue], error)
	Issue(context.Context, domain.RepoRef, int, domain.NodeID) (domain.IssueDetail, error)
	IssueComments(context.Context, domain.RepoRef, int, domain.PageRequest) (domain.Page[domain.Comment], error)
	ListPRs(context.Context, domain.RepoRef, domain.PRFilter, domain.PageRequest) (domain.Page[domain.PullRequest], error)
	PR(context.Context, domain.RepoRef, int) (domain.PRDetail, error)
	PRComments(context.Context, domain.RepoRef, int, domain.PageRequest) (domain.Page[domain.Comment], error)
	LinkedProjects(context.Context, domain.RepoRef, domain.PageRequest) (domain.Page[domain.Project], error)
	OwnerProjects(ctx context.Context, owner string, page domain.PageRequest) (domain.Page[domain.Project], error)
	Project(context.Context, domain.ProjectRef) (domain.Project, error)
	ProjectFields(context.Context, domain.NodeID) (domain.BoardInfo, error)
	ProjectItems(context.Context, domain.NodeID, domain.PageRequest) (domain.Page[domain.ProjectItem], error)
	IssuePlacements(context.Context, domain.NodeID) ([]domain.ProjectItem, error)
	CreateIssue(context.Context, domain.CreateInput) (domain.Issue, error)
	UpdateIssue(context.Context, domain.NodeID, domain.IssuePatch) (domain.Issue, error)
	AddAssignees(context.Context, domain.NodeID, []domain.NodeID) (domain.Issue, error)
	RemoveAssignees(context.Context, domain.NodeID, []domain.NodeID) (domain.Issue, error)
	AddLabels(context.Context, domain.NodeID, []domain.NodeID) (domain.Issue, error)
	RemoveLabels(context.Context, domain.NodeID, []domain.NodeID) (domain.Issue, error)
	AddComment(ctx context.Context, issueID domain.NodeID, body string) (domain.CommentResult, error)
	CloseIssue(ctx context.Context, issueID domain.NodeID, reason string) (domain.Issue, error)
	ReopenIssue(context.Context, domain.NodeID) (domain.Issue, error)
	DeleteIssue(context.Context, domain.NodeID) error
	AddProjectItem(ctx context.Context, projectID, issueID domain.NodeID) (domain.Placement, error)
	SetProjectStatus(ctx context.Context, projectID, itemID, fieldID domain.NodeID, optionID string) error
	ClearProjectStatus(ctx context.Context, projectID, itemID, fieldID domain.NodeID) error
	RemoveProjectItem(ctx context.Context, projectID, itemID domain.NodeID) error
	UnarchiveProjectItem(ctx context.Context, projectID, itemID domain.NodeID) (domain.Placement, error)
	RateLimit() domain.RateLimit
	RefreshCredentials(context.Context) error
}
