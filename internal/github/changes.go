package github

import (
	"context"
	"github.com/pkieltyka/todos/internal/domain"
)

func backendNotBuilt() error {
	return &domain.Error{Kind: domain.ErrInternal, Message: "changes are not built yet"}
}
func (s *Client) CreateIssue(context.Context, domain.CreateInput) (domain.Issue, error) {
	var result domain.Issue
	return result, backendNotBuilt()
}
func (s *Client) UpdateIssue(context.Context, domain.NodeID, domain.IssuePatch) (domain.Issue, error) {
	var result domain.Issue
	return result, backendNotBuilt()
}
func (s *Client) AddAssignees(context.Context, domain.NodeID, []domain.NodeID) (domain.Issue, error) {
	var result domain.Issue
	return result, backendNotBuilt()
}
func (s *Client) RemoveAssignees(context.Context, domain.NodeID, []domain.NodeID) (domain.Issue, error) {
	var result domain.Issue
	return result, backendNotBuilt()
}
func (s *Client) AddLabels(context.Context, domain.NodeID, []domain.NodeID) (domain.Issue, error) {
	var result domain.Issue
	return result, backendNotBuilt()
}
func (s *Client) RemoveLabels(context.Context, domain.NodeID, []domain.NodeID) (domain.Issue, error) {
	var result domain.Issue
	return result, backendNotBuilt()
}
func (s *Client) AddComment(ctx context.Context, issueID domain.NodeID, body string) (domain.CommentResult, error) {
	var result domain.CommentResult
	return result, backendNotBuilt()
}
func (s *Client) CloseIssue(ctx context.Context, issueID domain.NodeID, reason string) (domain.Issue, error) {
	var result domain.Issue
	return result, backendNotBuilt()
}
func (s *Client) ReopenIssue(context.Context, domain.NodeID) (domain.Issue, error) {
	var result domain.Issue
	return result, backendNotBuilt()
}
func (s *Client) DeleteIssue(context.Context, domain.NodeID) error { return backendNotBuilt() }
func (s *Client) AddProjectItem(ctx context.Context, projectID, issueID domain.NodeID) (domain.Placement, error) {
	var result domain.Placement
	return result, backendNotBuilt()
}
func (s *Client) SetProjectStatus(ctx context.Context, projectID, itemID, fieldID domain.NodeID, optionID string) error {
	return backendNotBuilt()
}
func (s *Client) ClearProjectStatus(ctx context.Context, projectID, itemID, fieldID domain.NodeID) error {
	return backendNotBuilt()
}
func (s *Client) RemoveProjectItem(ctx context.Context, projectID, itemID domain.NodeID) error {
	return backendNotBuilt()
}
func (s *Client) UnarchiveProjectItem(ctx context.Context, projectID, itemID domain.NodeID) (domain.Placement, error) {
	var result domain.Placement
	return result, backendNotBuilt()
}
