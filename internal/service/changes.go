package service

import (
	"context"
	"github.com/pkieltyka/todos/internal/domain"
)

func notBuilt() error {
	return &domain.Error{Kind: domain.ErrInternal, Message: "changes are not built yet"}
}
func (s *core) CreateIssue(context.Context, domain.NewIssue) (domain.Change, error) {
	var result domain.Change
	return result, notBuilt()
}
func (s *core) EditIssue(context.Context, int, domain.EditIssue) (domain.Change, error) {
	var result domain.Change
	return result, notBuilt()
}
func (s *core) UpdateAssignees(ctx context.Context, number int, add, remove []string) (domain.Change, error) {
	var result domain.Change
	return result, notBuilt()
}
func (s *core) Reassign(ctx context.Context, number int, logins []string) (domain.Change, error) {
	var result domain.Change
	return result, notBuilt()
}
func (s *core) UpdateLabels(ctx context.Context, number int, add, remove []string) (domain.Change, error) {
	var result domain.Change
	return result, notBuilt()
}
func (s *core) Close(context.Context, int, domain.CloseOptions) (domain.Change, error) {
	var result domain.Change
	return result, notBuilt()
}
func (s *core) Reopen(context.Context, int) (domain.Change, error) {
	var result domain.Change
	return result, notBuilt()
}
func (s *core) Comment(ctx context.Context, number int, body string) (domain.CommentResult, error) {
	var result domain.CommentResult
	return result, notBuilt()
}
func (s *core) Delete(context.Context, int) error { return notBuilt() }
func (s *core) SetStatus(ctx context.Context, number int, status *string) (domain.Change, error) {
	var result domain.Change
	return result, notBuilt()
}
func (s *core) BoardAdd(ctx context.Context, number int, status *string) (domain.Change, error) {
	var result domain.Change
	return result, notBuilt()
}
func (s *core) RemoveFromBoard(context.Context, int) (domain.Change, error) {
	var result domain.Change
	return result, notBuilt()
}
func (s *core) PlanQuickAdd(context.Context, string, domain.QuickAddOptions) (domain.QuickAddPlan, error) {
	var result domain.QuickAddPlan
	return result, notBuilt()
}
func (s *core) RunQuickAdd(context.Context, domain.QuickAddPlan, domain.QuickAddRunOptions) (domain.QuickAddResult, error) {
	var result domain.QuickAddResult
	return result, notBuilt()
}
