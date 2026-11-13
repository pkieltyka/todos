package service

import (
	"context"
	"time"

	"github.com/pkieltyka/todos/internal/domain"
)

type pacedBackend struct {
	Backend
	now     func() time.Time
	wait    func(context.Context, time.Duration) error
	lastEnd time.Time
	wrote   bool
}

func pacedCall[T any](ctx context.Context, p *pacedBackend, call func() (T, error)) (T, error) {
	var zero T
	for p.wrote {
		if ctx.Err() != nil {
			return zero, typed(ctx.Err())
		}
		delay := p.lastEnd.Add(time.Second).Sub(p.now())
		if delay <= 0 {
			break
		}
		if err := p.wait(ctx, delay); err != nil {
			return zero, typed(err)
		}
	}
	if ctx.Err() != nil {
		return zero, typed(ctx.Err())
	}
	v, err := call()
	p.lastEnd = p.now()
	p.wrote = true
	return v, err
}
func pacedVoid(ctx context.Context, p *pacedBackend, call func() error) error {
	_, err := pacedCall(ctx, p, func() (struct{}, error) { return struct{}{}, call() })
	return err
}
func (p *pacedBackend) CreateIssue(ctx context.Context, in domain.CreateInput) (domain.Issue, error) {
	return pacedCall(ctx, p, func() (domain.Issue, error) { return p.Backend.CreateIssue(ctx, in) })
}
func (p *pacedBackend) UpdateIssue(ctx context.Context, id domain.NodeID, in domain.IssuePatch) (domain.Issue, error) {
	return pacedCall(ctx, p, func() (domain.Issue, error) { return p.Backend.UpdateIssue(ctx, id, in) })
}
func (p *pacedBackend) AddAssignees(ctx context.Context, id domain.NodeID, ids []domain.NodeID) (domain.Issue, error) {
	return pacedCall(ctx, p, func() (domain.Issue, error) { return p.Backend.AddAssignees(ctx, id, ids) })
}
func (p *pacedBackend) RemoveAssignees(ctx context.Context, id domain.NodeID, ids []domain.NodeID) (domain.Issue, error) {
	return pacedCall(ctx, p, func() (domain.Issue, error) { return p.Backend.RemoveAssignees(ctx, id, ids) })
}
func (p *pacedBackend) AddLabels(ctx context.Context, id domain.NodeID, ids []domain.NodeID) (domain.Issue, error) {
	return pacedCall(ctx, p, func() (domain.Issue, error) { return p.Backend.AddLabels(ctx, id, ids) })
}
func (p *pacedBackend) RemoveLabels(ctx context.Context, id domain.NodeID, ids []domain.NodeID) (domain.Issue, error) {
	return pacedCall(ctx, p, func() (domain.Issue, error) { return p.Backend.RemoveLabels(ctx, id, ids) })
}
func (p *pacedBackend) AddComment(ctx context.Context, id domain.NodeID, body string) (domain.CommentResult, error) {
	return pacedCall(ctx, p, func() (domain.CommentResult, error) { return p.Backend.AddComment(ctx, id, body) })
}
func (p *pacedBackend) CloseIssue(ctx context.Context, id domain.NodeID, reason string) (domain.Issue, error) {
	return pacedCall(ctx, p, func() (domain.Issue, error) { return p.Backend.CloseIssue(ctx, id, reason) })
}
func (p *pacedBackend) ReopenIssue(ctx context.Context, id domain.NodeID) (domain.Issue, error) {
	return pacedCall(ctx, p, func() (domain.Issue, error) { return p.Backend.ReopenIssue(ctx, id) })
}
func (p *pacedBackend) DeleteIssue(ctx context.Context, id domain.NodeID) error {
	return pacedVoid(ctx, p, func() error { return p.Backend.DeleteIssue(ctx, id) })
}
func (p *pacedBackend) AddProjectItem(ctx context.Context, projectID, issueID domain.NodeID) (domain.Placement, error) {
	return pacedCall(ctx, p, func() (domain.Placement, error) { return p.Backend.AddProjectItem(ctx, projectID, issueID) })
}
func (p *pacedBackend) SetProjectStatus(ctx context.Context, projectID, itemID, fieldID domain.NodeID, optionID string) error {
	return pacedVoid(ctx, p, func() error { return p.Backend.SetProjectStatus(ctx, projectID, itemID, fieldID, optionID) })
}
func (p *pacedBackend) ClearProjectStatus(ctx context.Context, projectID, itemID, fieldID domain.NodeID) error {
	return pacedVoid(ctx, p, func() error { return p.Backend.ClearProjectStatus(ctx, projectID, itemID, fieldID) })
}
func (p *pacedBackend) RemoveProjectItem(ctx context.Context, projectID, itemID domain.NodeID) error {
	return pacedVoid(ctx, p, func() error { return p.Backend.RemoveProjectItem(ctx, projectID, itemID) })
}
func (p *pacedBackend) UnarchiveProjectItem(ctx context.Context, projectID, itemID domain.NodeID) (domain.Placement, error) {
	return pacedCall(ctx, p, func() (domain.Placement, error) { return p.Backend.UnarchiveProjectItem(ctx, projectID, itemID) })
}
