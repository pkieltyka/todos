package github

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"

	"github.com/pkieltyka/todos/internal/domain"
)

func mutationCases() []struct {
	name, payload, field string
	input                map[string]any
	call                 func(*Client) error
} {
	ctx := context.Background()
	title, body := "title", "body"
	ids := []domain.NodeID{"U1"}
	return []struct {
		name, payload, field string
		input                map[string]any
		call                 func(*Client) error
	}{
		{"CreateIssue", "createIssue", "issue", map[string]any{"repositoryId": "R1", "title": "title", "body": "body", "assigneeIds": []any{"U1"}, "labelIds": []any{"L1"}}, func(c *Client) error {
			_, e := c.CreateIssue(ctx, domain.CreateInput{RepositoryID: "R1", Title: title, Body: body, AssigneeIDs: ids, LabelIDs: []domain.NodeID{"L1"}})
			return e
		}},
		{"EditIssue", "updateIssue", "issue", map[string]any{"id": "I1", "body": "body"}, func(c *Client) error { _, e := c.UpdateIssue(ctx, "I1", domain.IssuePatch{Body: &body}); return e }},
		{"AddAssignees", "addAssigneesToAssignable", "assignable", map[string]any{"assignableId": "I1", "assigneeIds": []any{"U1"}}, func(c *Client) error { _, e := c.AddAssignees(ctx, "I1", ids); return e }},
		{"RemoveAssignees", "removeAssigneesFromAssignable", "assignable", map[string]any{"assignableId": "I1", "assigneeIds": []any{"U1"}}, func(c *Client) error { _, e := c.RemoveAssignees(ctx, "I1", ids); return e }},
		{"AddLabels", "addLabelsToLabelable", "labelable", map[string]any{"labelableId": "I1", "labelIds": []any{"L1"}}, func(c *Client) error { _, e := c.AddLabels(ctx, "I1", []domain.NodeID{"L1"}); return e }},
		{"RemoveLabels", "removeLabelsFromLabelable", "labelable", map[string]any{"labelableId": "I1", "labelIds": []any{"L1"}}, func(c *Client) error { _, e := c.RemoveLabels(ctx, "I1", []domain.NodeID{"L1"}); return e }},
		{"CloseIssue", "closeIssue", "issue", map[string]any{"issueId": "I1", "stateReason": "NOT_PLANNED"}, func(c *Client) error { _, e := c.CloseIssue(ctx, "I1", "not_planned"); return e }},
		{"ReopenIssue", "reopenIssue", "issue", map[string]any{"issueId": "I1"}, func(c *Client) error { _, e := c.ReopenIssue(ctx, "I1"); return e }},
		{"DeleteIssue", "deleteIssue", "repository", map[string]any{"issueId": "I1"}, func(c *Client) error { return c.DeleteIssue(ctx, "I1") }},
		{"AddProjectItem", "addProjectV2ItemById", "item", map[string]any{"projectId": "P1", "contentId": "I1"}, func(c *Client) error { _, e := c.AddProjectItem(ctx, "P1", "I1"); return e }},
		{"SetStatus", "updateProjectV2ItemFieldValue", "projectV2Item", map[string]any{"projectId": "P1", "itemId": "ITEM1", "fieldId": "F1", "value": map[string]any{"singleSelectOptionId": "todo"}}, func(c *Client) error { return c.SetProjectStatus(ctx, "P1", "ITEM1", "F1", "todo") }},
		{"ClearStatus", "clearProjectV2ItemFieldValue", "projectV2Item", map[string]any{"projectId": "P1", "itemId": "ITEM1", "fieldId": "F1"}, func(c *Client) error { return c.ClearProjectStatus(ctx, "P1", "ITEM1", "F1") }},
		{"RemoveProjectItem", "deleteProjectV2Item", "deletedItemId", map[string]any{"projectId": "P1", "itemId": "ITEM1"}, func(c *Client) error { return c.RemoveProjectItem(ctx, "P1", "ITEM1") }},
		{"UnarchiveItem", "unarchiveProjectV2Item", "item", map[string]any{"projectId": "P1", "itemId": "ITEM1"}, func(c *Client) error { _, e := c.UnarchiveProjectItem(ctx, "P1", "ITEM1"); return e }},
	}
}
func TestMutations_AllDocumentsAndInputs(t *testing.T) {
	for _, tc := range mutationCases() {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			c := dataClient(t, func(r request) any {
				calls++
				if r.Name != tc.name || !reflect.DeepEqual(r.Variables, map[string]any{"input": tc.input}) {
					t.Fatalf("%s %#v", r.Name, r.Variables)
				}
				var value any = fixture(t, "issue")
				switch tc.field {
				case "repository":
					value = map[string]any{"id": "R1"}
				case "deletedItemId":
					value = "ITEM1"
				case "item", "projectV2Item":
					value = map[string]any{"id": "ITEM1", "project": map[string]any{"id": "P1"}, "fieldValueByName": map[string]any{"__typename": "ProjectV2ItemFieldSingleSelectValue", "name": "Todo", "optionId": "todo"}}
				}
				return map[string]any{tc.payload: map[string]any{tc.field: value}}
			})
			if e := tc.call(c); e != nil {
				t.Fatal(e)
			}
			if calls != 1 {
				t.Fatal("unexpected retry", calls)
			}
		})
	}
}
func TestMutations_MissingPayloadUncertain(t *testing.T) {
	for _, tc := range mutationCases() {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			c := dataClient(t, func(request) any { calls++; return map[string]any{tc.payload: nil} })
			de := kind(t, tc.call(c), domain.ErrGitHub)
			if !de.Uncertain || calls != 1 {
				t.Fatal(de, calls)
			}
		})
	}
}
func TestMutations_FailuresNeverReplay(t *testing.T) {
	for _, tc := range []struct {
		name      string
		code      int
		body      string
		transport error
		kind      domain.ErrorKind
		uncertain bool
	}{{"network", 0, "", errors.New("connection reset"), domain.ErrNetwork, true}, {"server", 502, "bad gateway", nil, domain.ErrGitHub, true}, {"malformed", 200, "{", nil, domain.ErrGitHub, true}, {"auth", 401, `{"message":"Bad credentials"}`, nil, domain.ErrAuth, false}, {"forbidden", 403, `{"message":"Forbidden"}`, nil, domain.ErrForbidden, false}, {"validation", 422, `{"message":"Validation Failed"}`, nil, domain.ErrInvalidInput, false}, {"rate", 429, `{"message":"rate limit exceeded"}`, nil, domain.ErrRateLimited, false}} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			c := testClient(t, func(request) (*http.Response, error) {
				calls++
				if tc.transport != nil {
					return nil, tc.transport
				}
				return response(tc.code, tc.body, nil), nil
			})
			de := kind(t, c.DeleteIssue(context.Background(), "I1"), tc.kind)
			if de.Uncertain != tc.uncertain || calls != 1 {
				t.Fatal(de, calls)
			}
		})
	}
}
func TestMutations_CancelBeforeStartCertain(t *testing.T) {
	calls := 0
	c := dataClient(t, func(request) any { calls++; return nil })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	de := kind(t, c.DeleteIssue(ctx, "I1"), domain.ErrNetwork)
	if de.Uncertain || calls != 0 || !errors.Is(de, context.Canceled) {
		t.Fatal(de, calls)
	}
}
func TestMutations_CommentAndMapping(t *testing.T) {
	c := dataClient(t, func(r request) any {
		if r.Name != "Comment" || !reflect.DeepEqual(r.Variables["input"], map[string]any{"subjectId": "I1", "body": "**hello**"}) {
			t.Fatal(r)
		}
		return map[string]any{"addComment": map[string]any{"subject": fixture(t, "issue"), "commentEdge": map[string]any{"node": map[string]any{"id": "C1", "body": "**hello**", "author": nil, "createdAt": "2026-11-13T12:00:00Z", "url": "https://github.com/test/repo/issues/1#c1"}}}}
	})
	result, e := c.AddComment(context.Background(), "I1", "**hello**")
	if e != nil || result.Comment.ID != "C1" || result.Comment.Author != nil || result.Issue.NodeID == "" {
		t.Fatal(result, e)
	}
}
func TestMutations_AssigneeClearOmitsOtherFields(t *testing.T) {
	ids := []domain.NodeID{}
	c := dataClient(t, func(r request) any {
		want := map[string]any{"id": "I1", "assigneeIds": []any{}}
		if !reflect.DeepEqual(r.Variables["input"], want) {
			t.Fatal(r.Variables)
		}
		return map[string]any{"updateIssue": map[string]any{"issue": fixture(t, "issue")}}
	})
	_, e := c.UpdateIssue(context.Background(), "I1", domain.IssuePatch{AssigneeIDs: &ids})
	if e != nil {
		t.Fatal(e)
	}
}

func TestMutations_CommentMalformedUncertain(t *testing.T) {
	for _, payload := range []any{nil, map[string]any{"subject": fixture(t, "issue")}, map[string]any{"subject": map[string]any{}, "commentEdge": map[string]any{"node": map[string]any{"id": "C1"}}}} {
		c := dataClient(t, func(request) any { return map[string]any{"addComment": payload} })
		_, e := c.AddComment(context.Background(), "I1", "hello")
		if !kind(t, e, domain.ErrGitHub).Uncertain {
			t.Fatal("malformed acknowledgement was certain")
		}
	}
}

func TestIssue_AssigneeIDsRetained(t *testing.T) {
	raw := rawIssue{Assignees: connection[rawUser]{Nodes: []rawUser{{ID: "DEPARTED", Login: "departed"}}}}
	i := issue(raw, "")
	if len(i.Assignees) != 1 || i.AssigneeIDs["departed"] != "DEPARTED" {
		t.Fatal(i)
	}
	copy := domain.CloneIssue(i)
	copy.AssigneeIDs["departed"] = "changed"
	if i.AssigneeIDs["departed"] != "DEPARTED" {
		t.Fatal("aliased IDs")
	}
}

func TestErrors_RefusalAndValidationColon(t *testing.T) {
	for _, tc := range []struct {
		code int
		kind domain.ErrorKind
		want string
	}{{403, domain.ErrForbidden, "GitHub refused this operation: denied"}, {422, domain.ErrInvalidInput, "GitHub rejected the supplied values: denied"}} {
		c := testClient(t, func(request) (*http.Response, error) {
			return response(tc.code, `{"message":"denied"}`, http.Header{"Content-Type": []string{"application/json"}}), nil
		})
		de := kind(t, c.DeleteIssue(context.Background(), "I1"), tc.kind)
		if de.Message != tc.want {
			t.Fatalf("want %q, got %q", tc.want, de.Message)
		}
	}
}

func TestAddProjectItem_DuplicateRejectionIsCertain(t *testing.T) {
	c := testClient(t, func(request) (*http.Response, error) {
		return response(200, `{"data":{"addProjectV2ItemById":null},"errors":[{"type":"UNPROCESSABLE","message":"Content already exists in this project"}]}`, http.Header{"Content-Type": []string{"application/json"}}), nil
	})
	_, e := c.AddProjectItem(context.Background(), "P1", "I1")
	de := kind(t, e, domain.ErrInvalidInput)
	if de.Uncertain || de.Message != "GitHub rejected the supplied values: Content already exists in this project" {
		t.Fatal(de)
	}
}
