package github

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/pkieltyka/todos/internal/domain"
)

// A missing mutation payload is ambiguous: the write may already have landed.
func malformedWrite(message string) error {
	return &domain.Error{Kind: domain.ErrGitHub, Message: "invalid GitHub response: " + message, Uncertain: true}
}
func (c *Client) mutationField(ctx context.Context, operation, payload, field string, input map[string]any, out any) error {
	var data map[string]json.RawMessage
	if err := c.mutate(ctx, operation, map[string]any{"input": input}, &data); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data[payload], &fields); err != nil {
		return malformedWrite("missing " + payload)
	}
	raw := fields[field]
	if len(raw) == 0 || string(raw) == "null" {
		return malformedWrite("missing " + field)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return malformedWrite("malformed " + field)
	}
	return nil
}
func (c *Client) issueMutation(ctx context.Context, operation, payload, field string, input map[string]any) (domain.Issue, error) {
	var raw rawIssue
	if err := c.mutationField(ctx, operation, payload, field, input, &raw); err != nil {
		return domain.Issue{}, err
	}
	if raw.ID == "" || raw.Number < 1 {
		return domain.Issue{}, malformedWrite("missing issue identity")
	}
	return issue(raw, ""), nil
}
func (c *Client) CreateIssue(ctx context.Context, in domain.CreateInput) (domain.Issue, error) {
	return c.issueMutation(ctx, "CreateIssue", "createIssue", "issue", map[string]any{"repositoryId": in.RepositoryID, "title": in.Title, "body": in.Body, "assigneeIds": append([]domain.NodeID{}, in.AssigneeIDs...), "labelIds": append([]domain.NodeID{}, in.LabelIDs...)})
}
func (c *Client) UpdateIssue(ctx context.Context, id domain.NodeID, in domain.IssuePatch) (domain.Issue, error) {
	v := map[string]any{"id": id}
	if in.Title != nil {
		v["title"] = *in.Title
	}
	if in.Body != nil {
		v["body"] = *in.Body
	}
	if in.AssigneeIDs != nil {
		v["assigneeIds"] = append([]domain.NodeID{}, (*in.AssigneeIDs)...)
	}
	return c.issueMutation(ctx, "EditIssue", "updateIssue", "issue", v)
}
func (c *Client) AddAssignees(ctx context.Context, id domain.NodeID, ids []domain.NodeID) (domain.Issue, error) {
	return c.issueMutation(ctx, "AddAssignees", "addAssigneesToAssignable", "assignable", map[string]any{"assignableId": id, "assigneeIds": ids})
}
func (c *Client) RemoveAssignees(ctx context.Context, id domain.NodeID, ids []domain.NodeID) (domain.Issue, error) {
	return c.issueMutation(ctx, "RemoveAssignees", "removeAssigneesFromAssignable", "assignable", map[string]any{"assignableId": id, "assigneeIds": ids})
}
func (c *Client) AddLabels(ctx context.Context, id domain.NodeID, ids []domain.NodeID) (domain.Issue, error) {
	return c.issueMutation(ctx, "AddLabels", "addLabelsToLabelable", "labelable", map[string]any{"labelableId": id, "labelIds": ids})
}
func (c *Client) RemoveLabels(ctx context.Context, id domain.NodeID, ids []domain.NodeID) (domain.Issue, error) {
	return c.issueMutation(ctx, "RemoveLabels", "removeLabelsFromLabelable", "labelable", map[string]any{"labelableId": id, "labelIds": ids})
}
func (c *Client) AddComment(ctx context.Context, id domain.NodeID, body string) (domain.CommentResult, error) {
	var data struct {
		AddComment *struct {
			CommentEdge *struct{ Node *rawComment }
			Subject     *rawIssue
		}
	}
	if err := c.mutate(ctx, "Comment", map[string]any{"input": map[string]any{"subjectId": id, "body": body}}, &data); err != nil {
		return domain.CommentResult{}, err
	}
	p := data.AddComment
	if p == nil || p.Subject == nil || p.Subject.ID == "" || p.Subject.Number < 1 || p.CommentEdge == nil || p.CommentEdge.Node == nil || p.CommentEdge.Node.ID == "" {
		return domain.CommentResult{}, malformedWrite("missing comment or subject")
	}
	return domain.CommentResult{Issue: issue(*p.Subject, ""), Comment: comment(*p.CommentEdge.Node)}, nil
}
func (c *Client) CloseIssue(ctx context.Context, id domain.NodeID, reason string) (domain.Issue, error) {
	return c.issueMutation(ctx, "CloseIssue", "closeIssue", "issue", map[string]any{"issueId": id, "stateReason": strings.ToUpper(reason)})
}
func (c *Client) ReopenIssue(ctx context.Context, id domain.NodeID) (domain.Issue, error) {
	return c.issueMutation(ctx, "ReopenIssue", "reopenIssue", "issue", map[string]any{"issueId": id})
}
func (c *Client) DeleteIssue(ctx context.Context, id domain.NodeID) error {
	var repository struct{ ID domain.NodeID }
	if err := c.mutationField(ctx, "DeleteIssue", "deleteIssue", "repository", map[string]any{"issueId": id}, &repository); err != nil {
		return err
	}
	if repository.ID == "" {
		return malformedWrite("missing repository identity")
	}
	return nil
}
func (c *Client) placementMutation(ctx context.Context, operation, payload, field string, in map[string]any) (domain.Placement, error) {
	var p rawPlacement
	if err := c.mutationField(ctx, operation, payload, field, in, &p); err != nil {
		return domain.Placement{}, err
	}
	if p.ID == "" {
		return domain.Placement{}, malformedWrite("missing item identity")
	}
	return placement(p), nil
}
func (c *Client) AddProjectItem(ctx context.Context, projectID, issueID domain.NodeID) (domain.Placement, error) {
	return c.placementMutation(ctx, "AddProjectItem", "addProjectV2ItemById", "item", map[string]any{"projectId": projectID, "contentId": issueID})
}
func (c *Client) SetProjectStatus(ctx context.Context, projectID, itemID, fieldID domain.NodeID, optionID string) error {
	_, err := c.placementMutation(ctx, "SetStatus", "updateProjectV2ItemFieldValue", "projectV2Item", map[string]any{"projectId": projectID, "itemId": itemID, "fieldId": fieldID, "value": map[string]any{"singleSelectOptionId": optionID}})
	return err
}
func (c *Client) ClearProjectStatus(ctx context.Context, projectID, itemID, fieldID domain.NodeID) error {
	_, err := c.placementMutation(ctx, "ClearStatus", "clearProjectV2ItemFieldValue", "projectV2Item", map[string]any{"projectId": projectID, "itemId": itemID, "fieldId": fieldID})
	return err
}
func (c *Client) RemoveProjectItem(ctx context.Context, projectID, itemID domain.NodeID) error {
	var id domain.NodeID
	if err := c.mutationField(ctx, "RemoveProjectItem", "deleteProjectV2Item", "deletedItemId", map[string]any{"projectId": projectID, "itemId": itemID}, &id); err != nil {
		return err
	}
	if id == "" {
		return malformedWrite("missing deleted item identity")
	}
	return nil
}
func (c *Client) UnarchiveProjectItem(ctx context.Context, projectID, itemID domain.NodeID) (domain.Placement, error) {
	return c.placementMutation(ctx, "UnarchiveItem", "unarchiveProjectV2Item", "item", map[string]any{"projectId": projectID, "itemId": itemID})
}
