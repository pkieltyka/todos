package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/pkieltyka/todos/internal/domain"
)

var epoch = time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
var ref = domain.RepoRef{Host: "github.com", Owner: "test", Name: "repo"}

type request struct {
	Name, Document string
	Variables      map[string]any
	Header         http.Header
}
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(code int, body string, headers http.Header) *http.Response {
	if headers == nil {
		headers = http.Header{}
	}
	req, _ := http.NewRequest(http.MethodPost, "https://api.github.com/graphql", nil)
	return &http.Response{StatusCode: code, Header: headers, Body: io.NopCloser(strings.NewReader(body)), Request: req}
}
func fixture(t *testing.T, name string) map[string]any {
	t.Helper()
	data, err := os.ReadFile("testdata/queries/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err = json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}
func envelope(data any) string { b, _ := json.Marshal(map[string]any{"data": data}); return string(b) }
func testClient(t *testing.T, fn func(request) (*http.Response, error)) *Client {
	t.Helper()
	return New(Options{Version: "test", Host: func(context.Context) (string, error) { return "github.com", nil }, APIHost: "api.github.com", TokenForHost: func(string) (string, string) { return "test-secret", "GH_TOKEN" }, Now: func() time.Time { return epoch }, Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.github.com" || r.Method != http.MethodPost {
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
		}
		var body struct {
			Query     string
			Variables map[string]any
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		m := operationName.FindStringSubmatch(body.Query)
		if len(m) != 2 {
			return nil, errors.New("missing operation")
		}
		return fn(request{Name: m[1], Document: body.Query, Variables: body.Variables, Header: r.Header.Clone()})
	})})
}
func dataClient(t *testing.T, fn func(request) any) *Client {
	return testClient(t, func(r request) (*http.Response, error) { return response(200, envelope(fn(r)), nil), nil })
}
func kind(t *testing.T, err error, want domain.ErrorKind) *domain.Error {
	t.Helper()
	var de *domain.Error
	if !errors.As(err, &de) || de.Kind != want {
		t.Fatalf("want %s, got %v", want, err)
	}
	return de
}
func conn(nodes any, total int, more bool, cursor any) map[string]any {
	return map[string]any{"nodes": nodes, "totalCount": total, "pageInfo": map[string]any{"hasNextPage": more, "endCursor": cursor}}
}
func TestDocuments_AllOperationsAndFragments(t *testing.T) {
	if len(documents) != 33 {
		t.Fatalf("operations: %d", len(documents))
	}
	references := regexp.MustCompile(`\.\.\.([A-Za-z][A-Za-z0-9_]*)`)
	for name, doc := range documents {
		if !strings.Contains(doc, name+"(") && !strings.Contains(doc, name+" {") {
			t.Error(name)
		}
		for _, m := range references.FindAllStringSubmatch(doc, -1) {
			if !strings.Contains(doc, "fragment "+m[1]+" on ") {
				t.Errorf("%s missing %s", name, m[1])
			}
		}
	}
	if _, err := document("ExtraLabels"); err == nil {
		t.Fatal("unbounded labels document")
	}
}
func TestVariables_NoInterpolation(t *testing.T) {
	c := dataClient(t, func(r request) any {
		if strings.Contains(r.Document, "injected") {
			t.Fatal("interpolated name")
		}
		if r.Variables["filter"].(map[string]any)["labels"].([]any)[0] != "injected\"value" {
			t.Fatal(r.Variables)
		}
		return map[string]any{"repository": map[string]any{"issues": conn([]any{}, 0, false, nil)}}
	})
	_, err := c.ListIssues(context.Background(), ref, domain.IssueFilter{Labels: []string{"injected\"value"}}, domain.PageRequest{}, "")
	if err != nil {
		t.Fatal(err)
	}
}
func TestPage_RawCursorAndOneOuterPage(t *testing.T) {
	calls := 0
	c := dataClient(t, func(r request) any {
		calls++
		if r.Variables["first"] != float64(17) || r.Variables["after"] != "opaque==cursor" {
			t.Fatal(r.Variables)
		}
		return map[string]any{"repository": map[string]any{"issues": conn([]any{fixture(t, "issue")}, 45, true, "next==")}}
	})
	p, err := c.ListIssues(context.Background(), ref, domain.IssueFilter{}, domain.PageRequest{Cursor: "opaque==cursor", Size: 17}, "P_1")
	if err != nil || calls != 1 || p.NextCursor != "next==" || p.Total != 45 || !p.HasMore {
		t.Fatal(p, err, calls)
	}
}
func TestIssueSummary_AllEntryPoints(t *testing.T) {
	raw := fixture(t, "issue")
	c := dataClient(t, func(r request) any {
		switch r.Name {
		case "IssueView":
			return map[string]any{"repository": map[string]any{"issue": raw}}
		case "SearchIssues":
			return map[string]any{"search": map[string]any{"issueCount": 1, "nodes": []any{raw}, "pageInfo": map[string]any{}}}
		default:
			return map[string]any{"repository": map[string]any{"issues": conn([]any{raw}, 1, false, nil)}}
		}
	})
	d, err := c.Issue(context.Background(), ref, 12, "P_1")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []domain.IssueFilter{{}, {Search: "Unicode"}} {
		p, err := c.ListIssues(context.Background(), ref, f, domain.PageRequest{}, "P_1")
		if err != nil || !reflect.DeepEqual(d.Issue, p.Items[0]) {
			t.Fatal(p, err)
		}
	}
	if d.Body != "# Issue body" || d.Issue.Author != nil || d.Issue.CreatedAt.Location() != time.UTC || *d.Issue.StateReason != "not_planned" || d.Issue.Board.OptionID != "todo" {
		t.Fatal(d)
	}
}
func TestAuthor_Deleted(t *testing.T) {
	v := issue(rawIssue{Author: nil}, "")
	if v.Author != nil {
		t.Fatal(v)
	}
	if comment(rawComment{}).Author != nil {
		t.Fatal("invented author")
	}
}
func TestUnknownStateReason_Null(t *testing.T) {
	for _, reason := range []string{"", "DUPLICATE", "NEW_REASON"} {
		if issue(rawIssue{StateReason: reason}, "").StateReason != nil {
			t.Fatal(reason)
		}
	}
}
func TestIssueLabels_First100Only(t *testing.T) {
	doc := documents["IssueView"]
	if !strings.Contains(doc, "labels(first: 100)") {
		t.Fatal(doc)
	}
	calls := 0
	c := dataClient(t, func(request) any {
		calls++
		return map[string]any{"repository": map[string]any{"issue": fixture(t, "issue")}}
	})
	_, err := c.Issue(context.Background(), ref, 12, "P_1")
	if err != nil || calls != 1 {
		t.Fatal(calls, err)
	}
}
func TestMemberships_First100Only(t *testing.T) {
	if !strings.Contains(documents["IssueView"], "projectItems(first: 100") {
		t.Fatal("wrong bound")
	}
	TestIssueLabels_First100Only(t)
}
func TestNoExtraLabelsOperation(t *testing.T) {
	if _, ok := documents["ExtraLabels"]; ok {
		t.Fatal("unexpected overflow")
	}
}
func TestIssueMembershipFailure_BestEffort(t *testing.T) {
	for _, nullBubble := range []bool{false, true} {
		calls := 0
		c := testClient(t, func(r request) (*http.Response, error) {
			calls++
			if r.Variables["withBoard"] == false {
				return response(200, envelope(map[string]any{"repository": map[string]any{"issue": fixture(t, "issue")}}), nil), nil
			}
			var repository any = map[string]any{"issue": fixture(t, "issue")}
			if nullBubble {
				repository = nil
			}
			data := map[string]any{"data": map[string]any{"repository": repository}, "errors": []any{map[string]any{"type": "FORBIDDEN", "message": "project forbidden", "path": []any{"repository", "issue", "projectItems"}}}}
			body, _ := json.Marshal(data)
			return response(200, string(body), nil), nil
		})
		d, err := c.Issue(context.Background(), ref, 12, "P_1")
		if err != nil || d.Issue.Board != nil || d.Issue.Number != 12 {
			t.Fatal(d, err)
		}
		want := 1
		if nullBubble {
			want = 2
		}
		if calls != want {
			t.Fatal(calls)
		}
	}
}
func TestPRClosed_ExcludesMerged(t *testing.T) {
	c := dataClient(t, func(r request) any {
		if !reflect.DeepEqual(r.Variables["states"], []any{"CLOSED"}) {
			t.Fatal(r.Variables)
		}
		return map[string]any{"repository": map[string]any{"pullRequests": conn([]any{}, 0, false, nil)}}
	})
	if _, err := c.ListPRs(context.Background(), ref, domain.PRFilter{State: "closed"}, domain.PageRequest{}); err != nil {
		t.Fatal(err)
	}
}
func TestPRRollups_AllStatesAndLegacyChecks(t *testing.T) {
	for _, test := range []struct {
		state string
		total int
		want  domain.ChecksStatus
	}{{"SUCCESS", 2, "passing"}, {"ERROR", 1, "failing"}, {"FAILURE", 1, "failing"}, {"PENDING", 3, "pending"}, {"EXPECTED", 1, "pending"}, {"SUCCESS", 0, "none"}} {
		t.Run(test.state+fmt.Sprint(test.total), func(t *testing.T) {
			raw := fixture(t, "issue")
			raw["commits"] = map[string]any{"nodes": []any{map[string]any{"commit": map[string]any{"statusCheckRollup": map[string]any{"state": test.state, "contexts": map[string]any{"totalCount": test.total}}}}}}
			data, _ := json.Marshal(raw)
			var p rawPR
			json.Unmarshal(data, &p)
			if got := pullRequest(p); got.Checks != test.want || got.Review != "none" {
				t.Fatal(got)
			}
		})
	}
	if pullRequest(rawPR{}).Checks != "none" {
		t.Fatal("absent rollup")
	}
}
func TestPRCloses_PagedAndRepoScoped(t *testing.T) {
	calls := 0
	c := dataClient(t, func(r request) any {
		calls++
		raw := fixture(t, "issue")
		raw["mergeable"] = "UNKNOWN"
		nodes := []any{map[string]any{"number": 1, "repository": map[string]any{"id": "R_1"}}, map[string]any{"number": 9, "repository": map[string]any{"id": "OTHER"}}}
		if calls == 2 {
			nodes = append(nodes, map[string]any{"number": 2, "repository": map[string]any{"id": "R_1"}})
			if r.Variables["afterCloses"] != "next" {
				t.Fatal(r.Variables)
			}
		}
		raw["closingIssuesReferences"] = conn(nodes, 0, calls == 1, "next")
		return map[string]any{"repository": map[string]any{"id": "R_1", "pullRequest": raw}}
	})
	d, err := c.PR(context.Background(), ref, 12)
	if err != nil || !reflect.DeepEqual(d.Closes, []int{1, 2}) || calls != 2 {
		t.Fatal(d, err)
	}
}
func TestProjectFields_MultiplePages(t *testing.T) {
	calls := 0
	c := dataClient(t, func(r request) any {
		calls++
		p := fixture(t, "project")
		if calls == 1 {
			p["fields"] = conn([]any{map[string]any{"id": "OTHER", "name": "Other", "__typename": "ProjectV2Field"}}, 0, true, "fields-next")
		} else if r.Variables["after"] != "fields-next" {
			t.Fatal(r.Variables)
		}
		return map[string]any{"node": p}
	})
	b, err := c.ProjectFields(context.Background(), "P_1")
	if err != nil || calls != 2 || b.StatusFieldID != "F_1" || !b.Project.Usable || b.Options[0].Name != "Todo" {
		t.Fatal(b, err)
	}
}
func TestFields_StatusTypeAndOptionOrder(t *testing.T) {
	for _, name := range []string{"Status", "status"} {
		c := dataClient(t, func(request) any {
			p := fixture(t, "project")
			p["fields"] = conn([]any{map[string]any{"id": "F", "name": name, "__typename": "ProjectV2Field"}}, 0, false, nil)
			return map[string]any{"node": p}
		})
		info, err := c.ProjectFields(context.Background(), "P_1")
		if err != nil || info.Project.Usable || info.StatusFieldID != "" {
			t.Fatal(info, err)
		}
	}
}
func TestBoard_PositionOrderAndNonArchived(t *testing.T) {
	c := dataClient(t, func(r request) any {
		if !strings.Contains(r.Document, "archivedStates: [NOT_ARCHIVED]") || !strings.Contains(r.Document, "field: POSITION, direction: ASC") {
			t.Fatal(r.Document)
		}
		return map[string]any{"node": map[string]any{"items": conn([]any{}, 0, false, nil)}}
	})
	_, err := c.ProjectItems(context.Background(), "P_1", domain.PageRequest{})
	if err != nil {
		t.Fatal(err)
	}
}
func TestBoard_ContentKindsAndRedaction(t *testing.T) {
	nodes := []any{map[string]any{"id": "A", "content": nil}, map[string]any{"id": "B", "content": map[string]any{"__typename": "PullRequest"}}, map[string]any{"id": "C", "content": map[string]any{"__typename": "DraftIssue"}}, map[string]any{"id": "D", "content": fixture(t, "issue")}}
	c := dataClient(t, func(request) any { return map[string]any{"node": map[string]any{"items": conn(nodes, 4, false, nil)}} })
	p, err := c.ProjectItems(context.Background(), "P_1", domain.PageRequest{})
	if err != nil || len(p.Items) != 4 {
		t.Fatal(p, err)
	}
	for i, want := range []string{"Redacted", "PullRequest", "DraftIssue", "Issue"} {
		if p.Items[i].Kind != want {
			t.Fatal(p.Items)
		}
	}
	if p.Items[3].RepositoryID != "R_1" {
		t.Fatal("repository identity lost")
	}
}
func TestIssuePlacements_ArchivedPreflight(t *testing.T) {
	c := dataClient(t, func(r request) any {
		if !strings.Contains(r.Document, "includeArchived: true") {
			t.Fatal("missing archived")
		}
		return map[string]any{"node": map[string]any{"projectItems": conn([]any{map[string]any{"id": "X", "isArchived": true, "project": map[string]any{"id": "P_1"}}}, 200, true, "ignored")}}
	})
	p, err := c.IssuePlacements(context.Background(), "I_12")
	if err != nil || len(p) != 1 || !p[0].Archived {
		t.Fatal(p, err)
	}
}
func TestReadMetadata_Queries(t *testing.T) {
	c := dataClient(t, func(r request) any {
		switch r.Name {
		case "Viewer":
			return map[string]any{"viewer": map[string]any{"id": "U", "login": "test"}}
		case "Session":
			return map[string]any{"repository": map[string]any{"id": "R_1", "nameWithOwner": "test/repo", "hasIssuesEnabled": true}}
		case "Labels":
			return map[string]any{"repository": map[string]any{"labels": conn([]any{map[string]any{"id": "L", "name": "bug"}}, 1, false, nil)}}
		case "AssignableUsers":
			return map[string]any{"repository": map[string]any{"assignableUsers": conn([]any{map[string]any{"id": "U", "login": "test"}}, 1, false, nil)}}
		case "LinkedProjects":
			return map[string]any{"repository": map[string]any{"projectsV2": conn([]any{fixture(t, "project")}, 1, false, nil)}}
		case "OwnerProjects":
			return map[string]any{"repositoryOwner": map[string]any{"projectsV2": conn([]any{fixture(t, "project")}, 1, false, nil)}}
		case "ProjectByNumber":
			return map[string]any{"repositoryOwner": map[string]any{"projectV2": fixture(t, "project")}}
		default:
			t.Fatal(r.Name)
			return nil
		}
	})
	ctx := context.Background()
	if _, err := c.Viewer(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Repository(ctx, ref); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ListLabels(ctx, ref, domain.PageRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ListAssignableUsers(ctx, ref, domain.PageRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.LinkedProjects(ctx, ref, domain.PageRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.OwnerProjects(ctx, "test", domain.PageRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Project(ctx, domain.ProjectRef{Owner: "test", Number: 1}); err != nil {
		t.Fatal(err)
	}
}
func TestComments_OneOuterPageChronological(t *testing.T) {
	calls := 0
	c := dataClient(t, func(r request) any {
		calls++
		key := "issue"
		if r.Name == "PRComments" {
			key = "pullRequest"
		}
		return map[string]any{"repository": map[string]any{key: map[string]any{"comments": conn([]any{map[string]any{"id": "C", "author": nil, "body": "hi", "createdAt": epoch}}, 502, true, "comment-next")}}}
	})
	for _, pr := range []bool{false, true} {
		p, err := c.comments(context.Background(), ref, 12, domain.PageRequest{}, pr)
		if err != nil || p.Total != 502 || p.Items[0].Author != nil || !p.HasMore {
			t.Fatal(p, err)
		}
	}
	if calls != 2 {
		t.Fatal(calls)
	}
}
func TestSearch_ScopeAndProviderCeiling(t *testing.T) {
	for _, text := range []string{"repo:other/repo", "x) OR (x", "is:pr", "\x1b", "\"unclosed"} {
		if _, err := buildIssueSearch(ref, domain.IssueFilter{Search: text}); err == nil {
			t.Fatal(text)
		}
	}
	c := dataClient(t, func(r request) any {
		if !strings.Contains(r.Document, "ISSUE_ADVANCED") || !strings.Contains(r.Variables["query"].(string), "(one OR two)") {
			t.Fatal(r)
		}
		return map[string]any{"search": map[string]any{"issueCount": 1204, "nodes": []any{}, "pageInfo": map[string]any{}}}
	})
	p, err := c.ListIssues(context.Background(), ref, domain.IssueFilter{Search: "one OR two"}, domain.PageRequest{}, "")
	if err != nil || !p.ProviderTruncated || p.Total != 1204 {
		t.Fatal(p, err)
	}
}

func TestIssueListMembershipNullBubble_Fallback(t *testing.T) {
	calls := 0
	c := testClient(t, func(r request) (*http.Response, error) {
		calls++
		if r.Variables["withBoard"] == false {
			return response(200, envelope(map[string]any{"repository": map[string]any{"issues": conn([]any{fixture(t, "issue")}, 1, false, nil)}}), nil), nil
		}
		return response(200, `{"data":{"repository":{"issues":null}},"errors":[{"type":"FORBIDDEN","message":"project forbidden","path":["repository","issues","nodes",0,"projectItems"]}]}`, nil), nil
	})
	page, err := c.ListIssues(context.Background(), ref, domain.IssueFilter{}, domain.PageRequest{}, "P_1")
	if err != nil || calls != 2 || len(page.Items) != 1 || page.Items[0].Board != nil {
		t.Fatal(page, err, calls)
	}
}
func TestSearch_ReturnedRepoGuard(t *testing.T) {
	raw := fixture(t, "issue")
	raw["repository"] = map[string]any{"id": "OTHER", "nameWithOwner": "test/other"}
	c := dataClient(t, func(request) any { return map[string]any{"search": map[string]any{"nodes": []any{raw}}} })
	_, err := c.ListIssues(context.Background(), ref, domain.IssueFilter{Search: "title"}, domain.PageRequest{}, "")
	kind(t, err, domain.ErrInvalidInput)
}
func TestPage_InvalidSizeBeforeHTTP(t *testing.T) {
	c := testClient(t, func(request) (*http.Response, error) { t.Fatal("invalid page reached API"); return nil, nil })
	_, err := c.ListLabels(context.Background(), ref, domain.PageRequest{Size: 101})
	kind(t, err, domain.ErrInvalidInput)
}

func TestMalformedReadConnections_NotEmptySuccess(t *testing.T) {
	for _, test := range []struct {
		name string
		data any
		call func(*Client) error
	}{
		{"null_repository", map[string]any{"repository": nil}, func(c *Client) error { _, err := c.Repository(context.Background(), ref); return err }},
		{"null_issue", map[string]any{"repository": map[string]any{"issue": nil}}, func(c *Client) error { _, err := c.Issue(context.Background(), ref, 12, ""); return err }},
		{"null_project", map[string]any{"node": nil}, func(c *Client) error { _, err := c.ProjectFields(context.Background(), "P"); return err }},

		{"issues", map[string]any{"repository": map[string]any{}}, func(c *Client) error {
			_, err := c.ListIssues(context.Background(), ref, domain.IssueFilter{}, domain.PageRequest{}, "")
			return err
		}},
		{"labels", map[string]any{"repository": map[string]any{}}, func(c *Client) error {
			_, err := c.ListLabels(context.Background(), ref, domain.PageRequest{})
			return err
		}},
		{"users", map[string]any{"repository": map[string]any{}}, func(c *Client) error {
			_, err := c.ListAssignableUsers(context.Background(), ref, domain.PageRequest{})
			return err
		}},
		{"prs", map[string]any{"repository": map[string]any{}}, func(c *Client) error {
			_, err := c.ListPRs(context.Background(), ref, domain.PRFilter{}, domain.PageRequest{})
			return err
		}},
		{"linked", map[string]any{"repository": map[string]any{}}, func(c *Client) error {
			_, err := c.LinkedProjects(context.Background(), ref, domain.PageRequest{})
			return err
		}},
		{"owned", map[string]any{"repositoryOwner": map[string]any{}}, func(c *Client) error {
			_, err := c.OwnerProjects(context.Background(), "test", domain.PageRequest{})
			return err
		}},
		{"items", map[string]any{"node": map[string]any{}}, func(c *Client) error {
			_, err := c.ProjectItems(context.Background(), "P", domain.PageRequest{})
			return err
		}},
		{"placements", map[string]any{"node": map[string]any{}}, func(c *Client) error { _, err := c.IssuePlacements(context.Background(), "I"); return err }},
		{"comments", map[string]any{"repository": map[string]any{"issue": map[string]any{}}}, func(c *Client) error {
			_, err := c.IssueComments(context.Background(), ref, 12, domain.PageRequest{})
			return err
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := dataClient(t, func(request) any { return test.data })
			kind(t, test.call(c), domain.ErrGitHub)
		})
	}
}
