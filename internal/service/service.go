// Package service defines the shared CLI and TUI use-case contracts.
package service

import (
	"context"
	"github.com/pkieltyka/todos/internal/domain"
)

type Service interface {
	// session and metadata
	Session(context.Context) (domain.Session, error)
	RefreshMetadata(context.Context) error
	RateLimit() domain.RateLimit
	Labels(context.Context) ([]domain.Label, error)
	AssignableUsers(context.Context) ([]domain.User, error)

	// reads, one page per call
	ListIssues(context.Context, domain.IssueFilter, domain.PageRequest) (domain.Page[domain.Issue], error)
	GetIssue(context.Context, int) (domain.IssueDetail, error)
	ListIssueComments(context.Context, int, domain.PageRequest) (domain.Page[domain.Comment], error)
	ListPRs(context.Context, domain.PRFilter, domain.PageRequest) (domain.Page[domain.PullRequest], error)
	GetPR(context.Context, int) (domain.PRDetail, error)
	ListPRComments(context.Context, int, domain.PageRequest) (domain.Page[domain.Comment], error)

	// board
	BoardInfo(context.Context) (domain.BoardInfo, error)
	BoardCandidates(context.Context) (domain.ProjectsResult, error)
	UseBoard(context.Context, domain.ProjectRef) (domain.Project, error)
	Board(context.Context) (domain.Board, error)

	// changes
	CreateIssue(context.Context, domain.NewIssue) (domain.Change, error)
	EditIssue(context.Context, int, domain.EditIssue) (domain.Change, error)
	UpdateAssignees(ctx context.Context, number int, add, remove []string) (domain.Change, error)
	Reassign(ctx context.Context, number int, logins []string) (domain.Change, error)
	UpdateLabels(ctx context.Context, number int, add, remove []string) (domain.Change, error)
	Close(context.Context, int, domain.CloseOptions) (domain.Change, error)
	Reopen(context.Context, int) (domain.Change, error)
	Comment(ctx context.Context, number int, body string) (domain.CommentResult, error)
	Delete(context.Context, int) error
	SetStatus(ctx context.Context, number int, status *string) (domain.Change, error)
	BoardAdd(ctx context.Context, number int, status *string) (domain.Change, error)
	RemoveFromBoard(context.Context, int) (domain.Change, error)

	// quick add
	PlanQuickAdd(context.Context, string, domain.QuickAddOptions) (domain.QuickAddPlan, error)
	RunQuickAdd(context.Context, domain.QuickAddPlan, domain.QuickAddRunOptions) (domain.QuickAddResult, error)

	Doctor(context.Context) (domain.DoctorReport, error)
}
