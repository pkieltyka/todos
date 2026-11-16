package service

import (
	"context"
	"github.com/pkieltyka/todos/internal/domain"
)

// These adapters observe a single mutation result; they never retry a request.
type structureBackend struct {
	Backend
	owner *core
}

func structureCall[T any](ctx context.Context, p *structureBackend, call func() (T, error)) (T, error) {
	v, err := call()
	p.owner.writeFailed(err)
	return v, err
}
func structureVoid(ctx context.Context, p *structureBackend, call func() error) error {
	_, err := structureCall(ctx, p, func() (struct{}, error) { return struct{}{}, call() })
	return err
}
func (p *structureBackend) CreateIssue(ctx context.Context, in domain.CreateInput) (domain.Issue, error) {
	return structureCall(ctx, p, func() (domain.Issue, error) { return p.Backend.CreateIssue(ctx, in) })
}
func (p *structureBackend) UpdateIssue(ctx context.Context, id domain.NodeID, in domain.IssuePatch) (domain.Issue, error) {
	return structureCall(ctx, p, func() (domain.Issue, error) { return p.Backend.UpdateIssue(ctx, id, in) })
}
func (p *structureBackend) AddAssignees(ctx context.Context, id domain.NodeID, ids []domain.NodeID) (domain.Issue, error) {
	return structureCall(ctx, p, func() (domain.Issue, error) { return p.Backend.AddAssignees(ctx, id, ids) })
}
func (p *structureBackend) RemoveAssignees(ctx context.Context, id domain.NodeID, ids []domain.NodeID) (domain.Issue, error) {
	return structureCall(ctx, p, func() (domain.Issue, error) { return p.Backend.RemoveAssignees(ctx, id, ids) })
}
func (p *structureBackend) AddLabels(ctx context.Context, id domain.NodeID, ids []domain.NodeID) (domain.Issue, error) {
	return structureCall(ctx, p, func() (domain.Issue, error) { return p.Backend.AddLabels(ctx, id, ids) })
}
func (p *structureBackend) RemoveLabels(ctx context.Context, id domain.NodeID, ids []domain.NodeID) (domain.Issue, error) {
	return structureCall(ctx, p, func() (domain.Issue, error) { return p.Backend.RemoveLabels(ctx, id, ids) })
}
func (p *structureBackend) AddComment(ctx context.Context, id domain.NodeID, body string) (domain.CommentResult, error) {
	return structureCall(ctx, p, func() (domain.CommentResult, error) { return p.Backend.AddComment(ctx, id, body) })
}
func (p *structureBackend) CloseIssue(ctx context.Context, id domain.NodeID, reason string) (domain.Issue, error) {
	return structureCall(ctx, p, func() (domain.Issue, error) { return p.Backend.CloseIssue(ctx, id, reason) })
}
func (p *structureBackend) ReopenIssue(ctx context.Context, id domain.NodeID) (domain.Issue, error) {
	return structureCall(ctx, p, func() (domain.Issue, error) { return p.Backend.ReopenIssue(ctx, id) })
}
func (p *structureBackend) DeleteIssue(ctx context.Context, id domain.NodeID) error {
	return structureVoid(ctx, p, func() error { return p.Backend.DeleteIssue(ctx, id) })
}
func (p *structureBackend) AddProjectItem(ctx context.Context, projectID, issueID domain.NodeID) (domain.Placement, error) {
	return structureCall(ctx, p, func() (domain.Placement, error) { return p.Backend.AddProjectItem(ctx, projectID, issueID) })
}
func (p *structureBackend) SetProjectStatus(ctx context.Context, projectID, itemID, fieldID domain.NodeID, optionID string) error {
	return structureVoid(ctx, p, func() error { return p.Backend.SetProjectStatus(ctx, projectID, itemID, fieldID, optionID) })
}
func (p *structureBackend) ClearProjectStatus(ctx context.Context, projectID, itemID, fieldID domain.NodeID) error {
	return structureVoid(ctx, p, func() error { return p.Backend.ClearProjectStatus(ctx, projectID, itemID, fieldID) })
}
func (p *structureBackend) RemoveProjectItem(ctx context.Context, projectID, itemID domain.NodeID) error {
	return structureVoid(ctx, p, func() error { return p.Backend.RemoveProjectItem(ctx, projectID, itemID) })
}
func (p *structureBackend) UnarchiveProjectItem(ctx context.Context, projectID, itemID domain.NodeID) (domain.Placement, error) {
	return structureCall(ctx, p, func() (domain.Placement, error) { return p.Backend.UnarchiveProjectItem(ctx, projectID, itemID) })
}
