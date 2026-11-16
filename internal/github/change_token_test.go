package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/pkieltyka/todos/internal/domain"
)

func tokenData() map[string]any {
	return map[string]any{"viewer": map[string]any{"login": "test"}, "repository": map[string]any{"issues": map[string]any{"totalCount": 3, "nodes": []any{map[string]any{"number": 12, "updatedAt": epoch.Add(-time.Minute)}}}}, "node": map[string]any{"updatedAt": epoch.Add(-time.Minute), "items": map[string]any{"totalCount": 2}}}
}
func TestChangeToken_DocumentVariablesAndDate(t *testing.T) {
	for _, project := range []domain.NodeID{"", "P1"} {
		t.Run(string(project), func(t *testing.T) {
			calls := 0
			c := testClient(t, func(r request) (*http.Response, error) {
				calls++
				if r.Name != "ChangeToken" || r.Variables["name"] != "repo" || r.Variables["owner"] != "test" || r.Variables["project"] != string(project) || r.Variables["withProject"] != (project != "") {
					t.Error(r)
				}
				return response(200, envelope(tokenData()), http.Header{"Date": {epoch.Format(http.TimeFormat)}}), nil
			})
			token, err := c.ChangeToken(context.Background(), ref, project)
			if err != nil || calls != 1 || token.Viewer != "test" || token.IssuesTotal != 3 || token.NewestIssue != 12 || !token.NewestUpdated.Equal(epoch.Add(-time.Minute)) || !token.ServerTime.Equal(epoch) {
				t.Fatal(token, err)
			}
			if project != "" {
				if token.ProjectItems != 2 || !token.ProjectUpdated.Equal(epoch.Add(-time.Minute)) {
					t.Fatal(token)
				}
			} else if token.ProjectItems != 0 || !token.ProjectUpdated.IsZero() {
				t.Fatal("unused project half mapped", token)
			}
		})
	}
}
func TestChangeToken_MissingOrUnparseableDateIsZero(t *testing.T) {
	for _, date := range []string{"", "not-a-date"} {
		c := testClient(t, func(request) (*http.Response, error) {
			return response(200, envelope(tokenData()), http.Header{"Date": {date}}), nil
		})
		token, err := c.ChangeToken(context.Background(), ref, "P1")
		if err != nil || !token.ServerTime.IsZero() {
			t.Fatal(token, err)
		}
	}
}
func TestChangeToken_DateIsRequestLocal(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	c := testClient(t, func(r request) (*http.Response, error) {
		date := epoch
		if r.Variables["owner"] == "held" {
			close(entered)
			<-release
		} else {
			date = date.Add(time.Hour)
		}
		return response(200, envelope(tokenData()), http.Header{"Date": {date.Format(http.TimeFormat)}}), nil
	})
	var first domain.ChangeToken
	var firstErr error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		first, firstErr = c.ChangeToken(context.Background(), domain.RepoRef{Host: "github.com", Owner: "held", Name: "repo"}, "P1")
	}()
	<-entered
	second, err := c.ChangeToken(context.Background(), ref, "P1")
	if err != nil {
		t.Fatal(err)
	}
	close(release)
	wg.Wait()
	if firstErr != nil || !first.ServerTime.Equal(epoch) || !second.ServerTime.Equal(epoch.Add(time.Hour)) {
		t.Fatal(first, second, firstErr)
	}
}
func TestChangeToken_ProjectRefusalIsPartialAndNeverSuccessful(t *testing.T) {
	for _, path := range [][]any{{"node"}, {"repository", "issues"}, {}} {
		t.Run(fmt.Sprint(path), func(t *testing.T) {
			data := tokenData()
			data["node"] = nil
			body, _ := json.Marshal(map[string]any{"data": data, "errors": []any{map[string]any{"type": "FORBIDDEN", "message": "project access forbidden", "path": path}}})
			c := testClient(t, func(request) (*http.Response, error) { return response(200, string(body), nil), nil })
			token, err := c.ChangeToken(context.Background(), ref, "P1")
			kind(t, err, domain.ErrForbidden)
			if (token.Viewer != "") != (len(path) > 0 && path[0] == "node") {
				t.Fatal("project refusal indistinguishable from whole probe failure", token, err)
			}
		})
	}
}
func TestChangeToken_ProbeFailureReturnsZero(t *testing.T) {
	c := testClient(t, func(request) (*http.Response, error) { return nil, errors.New("offline") })
	token, err := c.ChangeToken(context.Background(), ref, "P1")
	kind(t, err, domain.ErrNetwork)
	if token != (domain.ChangeToken{}) {
		t.Fatal(token)
	}
}
func TestChangeToken_EmptyRepository(t *testing.T) {
	data := tokenData()
	data["repository"] = map[string]any{"issues": map[string]any{"totalCount": 0, "nodes": []any{}}}
	c := dataClient(t, func(request) any { return data })
	token, err := c.ChangeToken(context.Background(), ref, "")
	if err != nil || token.IssuesTotal != 0 || token.NewestIssue != 0 || !token.NewestUpdated.IsZero() {
		t.Fatal(token, err)
	}
}
func TestChangeToken_MalformedCannotVouchForData(t *testing.T) {
	for _, mode := range []string{"viewer", "login", "repository", "issues", "total", "nodes", "negative-total", "too-many", "no-newest", "number", "updated", "project", "project-time", "project-items", "project-total", "project-negative"} {
		t.Run(mode, func(t *testing.T) {
			data := tokenData()
			issues := data["repository"].(map[string]any)["issues"].(map[string]any)
			node := data["node"].(map[string]any)
			switch mode {
			case "viewer":
				data["viewer"] = nil
			case "login":
				data["viewer"] = map[string]any{"login": ""}
			case "repository":
				data["repository"] = nil
			case "issues":
				data["repository"].(map[string]any)["issues"] = nil
			case "total":
				delete(issues, "totalCount")
			case "nodes":
				delete(issues, "nodes")
			case "negative-total":
				issues["totalCount"] = -1
			case "too-many":
				issues["nodes"] = append(issues["nodes"].([]any), map[string]any{"number": 13, "updatedAt": epoch})
			case "no-newest":
				issues["nodes"] = []any{}
			case "number":
				issues["nodes"].([]any)[0].(map[string]any)["number"] = 0
			case "updated":
				delete(issues["nodes"].([]any)[0].(map[string]any), "updatedAt")
			case "project":
				data["node"] = nil
			case "project-time":
				delete(node, "updatedAt")
			case "project-items":
				delete(node, "items")
			case "project-total":
				node["items"] = map[string]any{}
			case "project-negative":
				node["items"] = map[string]any{"totalCount": -1}
			}
			c := dataClient(t, func(request) any { return data })
			_, err := c.ChangeToken(context.Background(), ref, "P1")
			kind(t, err, domain.ErrGitHub)
		})
	}
}
