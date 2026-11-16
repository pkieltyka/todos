package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	ghrepo "github.com/cli/go-gh/v2/pkg/repository"
	"github.com/pkieltyka/todos/internal/domain"
	"github.com/pkieltyka/todos/internal/service/servicetest"
)

var updateReadGoldens = flag.Bool("update-read-goldens", false, "Rewrite Phase 2 output fixtures")
var fixtureTime = time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)

func fakeDeps(f *servicetest.Fake, out, stderr io.Writer, human bool) Dependencies {
	return Dependencies{In: panicReader{}, Out: out, Err: stderr, Version: "1.2.3", Getenv: func(string) string { return "" }, LookupEnv: func(string) (string, bool) { return "", false }, Now: func() time.Time { return fixtureTime }, IsTTY: func(io.Writer) bool { return human }, Width: func(io.Writer) int { return 80 }, ResolveRepo: func(context.Context, string) (domain.RepoRef, error) {
		return servicetest.DefaultFixture().Session.Repo.Ref, nil
	}, Wire: func(context.Context, func(context.Context) (domain.RepoRef, error), string) (Wiring, error) {
		return Wiring{Service: f}, nil
	}}
}
func runFake(f *servicetest.Fake, human bool, args ...string) (string, string, int) {
	var out, stderr bytes.Buffer
	code := Run(context.Background(), args, fakeDeps(f, &out, &stderr, human))
	return out.String(), stderr.String(), code
}
func readGolden(t *testing.T, name, text string) {
	t.Helper()
	path := filepath.Join("testdata", "reads", name+".golden")
	if *updateReadGoldens {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != text {
		t.Fatalf("%s differs\nwant: %q\n got: %q", name, want, text)
	}
}
func TestJSON_ReadCommandGoldens(t *testing.T) {
	commands := []struct {
		name string
		args []string
	}{{"list", []string{"list", "--state", "all"}}, {"view", []string{"view", "6"}}, {"pr_list", []string{"pr", "list", "--state", "all"}}, {"pr_view", []string{"pr", "view", "101"}}, {"board", []string{"board"}}, {"board_list", []string{"board", "list"}}, {"board_use", []string{"board", "use", "1"}}, {"labels", []string{"labels"}}, {"assignees", []string{"assignees"}}, {"doctor", []string{"doctor"}}}
	for _, command := range commands {
		for _, mode := range []string{"json", "tsv", "human"} {
			t.Run(command.name+"/"+mode, func(t *testing.T) {
				args := append([]string{}, command.args...)
				if mode == "json" {
					args = append(args, "--json")
				}
				if mode == "human" {
					args = append(args, "--no-color")
				}
				out, stderr, code := runFake(servicetest.New(servicetest.DefaultFixture()), mode == "human", args...)
				if code != 0 || stderr != "" {
					t.Fatalf("%d %s", code, stderr)
				}
				readGolden(t, command.name+"."+mode, out)
			})
		}
	}
}
func TestResolveRepo_FlagThenGHRepoThenCurrent(t *testing.T) {
	currentCalls := 0
	current := func() (ghrepo.Repository, error) {
		currentCalls++
		return ghrepo.Repository{Host: "github.com", Owner: "current", Name: "repo"}, nil
	}
	for _, test := range []struct{ flag, env, owner string }{{"flag/repo", "env/repo", "flag"}, {"", "env/repo", "env"}, {"", "", "current"}} {
		r, err := resolveRepository(context.Background(), test.flag, func(string) string { return test.env }, current)
		if err != nil || r.Owner != test.owner {
			t.Fatal(r, err)
		}
	}
	if currentCalls != 1 {
		t.Fatal("reimplemented precedence")
	}
}
func TestResolveRepo_ExplicitInvalidNoFallback(t *testing.T) {
	for _, flag := range []string{"invalid", "/repo", "owner/"} {
		_, err := resolveRepository(context.Background(), flag, func(string) string { return "fallback/repo" }, func() (ghrepo.Repository, error) { t.Fatal("fell through to current"); return ghrepo.Repository{}, nil })
		var de *domain.Error
		if !errors.As(err, &de) || de.Kind != domain.ErrInvalidInput {
			t.Fatal(err)
		}
	}
}
func TestFlags_ReadExclusivityCases(t *testing.T) {
	for _, args := range [][]string{{"list", "--all", "--limit", "50"}, {"list", "--limit", "0"}, {"pr", "list", "--limit", "-1"}, {"list", "--mine", "--assignee", ""}, {"list", "--state", "merged"}, {"pr", "list", "--state", "unknown"}} {
		f := servicetest.New(servicetest.DefaultFixture())
		out, stderr, code := runFake(f, false, args...)
		if code != 2 || out != "" || stderr == "" || len(f.Calls()) != 0 {
			t.Fatal(args, code, out, stderr)
		}
	}
}
func TestList_Limit50AndAll(t *testing.T) {
	fixture := servicetest.DefaultFixture()
	fixture.Issues = servicetest.GenIssues(1204)
	for _, all := range []bool{false, true} {
		f := servicetest.New(fixture)
		args := []string{"list", "--json"}
		if all {
			args = append(args, "--all")
		}
		out, stderr, code := runFake(f, false, args...)
		if code != 0 || stderr != "" {
			t.Fatal(code, stderr)
		}
		var dto IssuesDTO
		json.Unmarshal([]byte(out), &dto)
		want := 50
		if all {
			want = 1204
		}
		if len(dto.Issues) != want || dto.Total != 1204 || dto.Truncated == all {
			t.Fatal(len(dto.Issues), dto.Total, dto.Truncated)
		}
	}
}
func TestList_RawCursorRepeatedFilter(t *testing.T) {
	fixture := servicetest.DefaultFixture()
	fixture.Issues = servicetest.GenIssues(220)
	for i := range fixture.Issues {
		fixture.Issues[i].Issue.Labels = []domain.Label{{Name: "bug"}}
	}
	f := servicetest.New(fixture)
	_, stderr, code := runFake(f, false, "list", "--all", "--label", "bug", "--assignee", "reviewer", "--json")
	if code != 0 {
		t.Fatal(stderr)
	}
	calls := []servicetest.Call{}
	for _, c := range f.Calls() {
		if c.Method == "ListIssues" {
			calls = append(calls, c)
		}
	}
	if len(calls) != 2 || calls[0].Args.Page.Cursor != "" || calls[1].Args.Page.Cursor != "100" || !reflect.DeepEqual(calls[0].Args.Filter, calls[1].Args.Filter) {
		t.Fatal(calls)
	}
}
func TestSearch_AllOver1000InvalidInput(t *testing.T) {
	fixture := servicetest.DefaultFixture()
	fixture.Issues = servicetest.GenIssues(1204)
	out, stderr, code := runFake(servicetest.New(fixture), false, "list", "--search", "Issue", "--all", "--json")
	if code != 2 || out != "" || !strings.Contains(stderr, "invalid_input") {
		t.Fatal(code, out, stderr)
	}
	readGolden(t, "search_all_error.json", stderr)
}
func TestSearch_BoundedProviderTruncated(t *testing.T) {
	fixture := servicetest.DefaultFixture()
	fixture.Issues = servicetest.GenIssues(1204)
	out, stderr, code := runFake(servicetest.New(fixture), false, "list", "--search", "Issue", "--limit", "5", "--json")
	var dto IssuesDTO
	json.Unmarshal([]byte(out), &dto)
	if code != 0 || stderr != "" || len(dto.Issues) != 5 || !dto.Truncated || dto.Total != 1204 {
		t.Fatal(code, out, stderr)
	}
}
func TestList_LaterFailureStdoutEmpty(t *testing.T) {
	fixture := servicetest.DefaultFixture()
	fixture.Issues = servicetest.GenIssues(220)
	f := servicetest.New(fixture)
	f.Hold("ListIssues", 0).Release()
	f.Hold("ListIssues", 0).Fail(&domain.Error{Kind: domain.ErrTimeout, Message: "timeout"})
	out, stderr, code := runFake(f, false, "list", "--all", "--json")
	if code != 1 || out != "" || !strings.Contains(stderr, "timeout") {
		t.Fatal(code, out, stderr)
	}
}
func TestComments_500Cutoff(t *testing.T) {
	fixture := servicetest.DefaultFixture()
	fixture.IssueComments[6] = []domain.Comment{}
	for i := 0; i < 502; i++ {
		fixture.IssueComments[6] = append(fixture.IssueComments[6], domain.Comment{ID: fmt.Sprint(i), Body: fmt.Sprint(i), CreatedAt: fixtureTime.Add(time.Duration(i) * time.Second)})
	}
	out, stderr, code := runFake(servicetest.New(fixture), false, "view", "6", "--json")
	var dto IssueViewDTO
	json.Unmarshal([]byte(out), &dto)
	if code != 0 || stderr != "" || len(dto.Comments) != 500 || !dto.CommentsTruncated || dto.Comments[499].ID != "499" {
		t.Fatal(code, len(dto.Comments), stderr)
	}
}
func TestList_RepeatedCursorStops(t *testing.T) {
	calls := 0
	_, _, _, err := collectRows(context.Background(), ListOptions{All: true}, func(context.Context, domain.PageRequest) (domain.Page[domain.Comment], error) {
		calls++
		return domain.Page[domain.Comment]{HasMore: true, NextCursor: "same", Items: []domain.Comment{{ID: "one"}}}, nil
	}, func(v domain.Comment) string { return v.ID }, false)
	var de *domain.Error
	if !errors.As(err, &de) || de.Kind != domain.ErrGitHub || calls != 2 {
		t.Fatal(calls, err)
	}
}
func TestJSON_EmptyArraysNullsTimes(t *testing.T) {
	i := issueDTO(domain.Issue{CreatedAt: fixtureTime.In(time.FixedZone("offset", -3600)), UpdatedAt: fixtureTime})
	data, _ := json.Marshal(i)
	var v map[string]any
	json.Unmarshal(data, &v)
	if v["created_at"] != "2026-11-13T12:00:00Z" || v["author"] != nil || v["closed_at"] != nil || !reflect.DeepEqual(v["labels"], []any{}) || !reflect.DeepEqual(v["assignees"], []any{}) {
		t.Fatal(v)
	}
	empty := IssuesDTO{Issues: []IssueDTO{}}
	b, _ := json.Marshal(empty)
	if strings.Contains(string(b), "null") {
		t.Fatal(string(b))
	}
}
func TestJSON_ViewFieldsAbsentFromChanges(t *testing.T) {
	data, _ := json.Marshal(changeDTO(domain.Change{}))
	for _, field := range []string{"body", "comments\"", "comments_truncated"} {
		if strings.Contains(string(data), field) {
			t.Fatal(string(data))
		}
	}
}
func TestJSON_InternalFieldsNeverLeak(t *testing.T) {
	data, _ := json.Marshal(issueDTO(domain.Issue{NodeID: "SECRET_NODE", ViewerCanDelete: true, Labels: []domain.Label{{NodeID: "SECRET_LABEL", Name: "bug", Color: "ff0000"}}}))
	for _, field := range []string{"SECRET", "viewer", "color", "node"} {
		if strings.Contains(string(data), field) {
			t.Fatal(string(data))
		}
	}
}
func TestErrors_SingleEnvelopeNoDuplicate(t *testing.T) {
	for _, de := range []*domain.Error{{Kind: domain.ErrRateLimited, Message: "wait", RetryAfter: 1500 * time.Millisecond}, {Kind: domain.ErrPartial, Message: "partly applied", Cause: domain.ErrTimeout, Uncertain: true, Completed: []string{"created", "removed", "added_to_board"}, Issue: &domain.Issue{Number: 12, Title: "Known issue"}}} {
		f := servicetest.New(servicetest.DefaultFixture())
		f.Hold("ListIssues", 0).Fail(de)
		out, stderr, code := runFake(f, false, "list", "--json")
		if out != "" || code != exitCode(de, false) {
			t.Fatal(code, out)
		}
		decoder := json.NewDecoder(strings.NewReader(stderr))
		var env map[string]any
		if err := decoder.Decode(&env); err != nil {
			t.Fatal(err)
		}
		var extra any
		if decoder.Decode(&extra) != io.EOF {
			t.Fatal("duplicate diagnostic")
		}
		errorValue := env["error"].(map[string]any)
		if _, ok := errorValue["uncertain"]; !ok {
			t.Fatal(errorValue)
		}
		readGolden(t, string(de.Kind)+"_error.json", stderr)
	}
}
func TestPartial_NoInternalRemovedStep(t *testing.T) {
	v := errorDTO(&domain.Error{Kind: domain.ErrPartial, Cause: domain.ErrForbidden, Completed: []string{"removed"}})
	if v.Completed == nil || len(*v.Completed) != 0 || v.Cause == nil {
		t.Fatal(v)
	}
}
func TestDoctor_AlwaysStdoutAndFullReport(t *testing.T) {
	for _, human := range []bool{false, true} {
		for _, asJSON := range []bool{false, true} {
			fixture := servicetest.DefaultFixture()
			fixture.Session.Repo.IssuesEnabled = false
			args := []string{"doctor"}
			if asJSON {
				args = append(args, "--json")
			}
			out, stderr, code := runFake(servicetest.New(fixture), human, args...)
			if code != 6 || out == "" || stderr != "" {
				t.Fatal(code, out, stderr)
			}
			if asJSON {
				var dto DoctorDTO
				json.Unmarshal([]byte(out), &dto)
				if len(dto.Checks) != 7 || dto.OK {
					t.Fatal(dto)
				}
			}
		}
	}
}
func TestDoctor_BoardConfigFailureExit0(t *testing.T) {
	fixture := servicetest.DefaultFixture()
	fixture.Projects = nil
	fixture.ConfigCheck = domain.DoctorCheck{Name: "Config", OK: false, Detail: "bad config"}
	out, stderr, code := runFake(servicetest.New(fixture), false, "doctor", "--json")
	if code != 0 || stderr != "" || out == "" {
		t.Fatal(code, out, stderr)
	}
}
func TestDoctor_FirstIssueFailureExitKind(t *testing.T) {
	fixture := servicetest.DefaultFixture()
	fixture.Session.Repo.ViewerCanCreateIssues = false
	out, stderr, code := runFake(servicetest.New(fixture), false, "doctor", "--json")
	if code != 4 || stderr != "" || out == "" {
		t.Fatal(code, out, stderr)
	}
}
func TestDoctor_WiringFailureStillStdout(t *testing.T) {
	var out, stderr bytes.Buffer
	deps := fakeDeps(servicetest.New(servicetest.DefaultFixture()), &out, &stderr, false)
	deps.Wire = func(context.Context, func(context.Context) (domain.RepoRef, error), string) (Wiring, error) {
		return Wiring{}, &domain.Error{Kind: domain.ErrConfig, Message: "home unavailable"}
	}
	code := Run(context.Background(), []string{"doctor", "--json"}, deps)
	var dto DoctorDTO
	json.Unmarshal(out.Bytes(), &dto)
	if code != 6 || stderr.Len() != 0 || len(dto.Checks) != 7 {
		t.Fatal(code, out.String(), stderr.String())
	}
}
func TestDoctor_JSONConfigWarningInReportOnly(t *testing.T) {
	fixture := servicetest.DefaultFixture()
	fixture.ConfigCheck = domain.DoctorCheck{Name: "Config", OK: false, Detail: "bad config"}
	for _, command := range []string{"list", "doctor"} {
		var out, stderr bytes.Buffer
		deps := fakeDeps(servicetest.New(fixture), &out, &stderr, false)
		deps.Wire = func(context.Context, func(context.Context) (domain.RepoRef, error), string) (Wiring, error) {
			return Wiring{Service: servicetest.New(fixture), ConfigWarning: func() *domain.Error { return &domain.Error{Kind: domain.ErrConfig, Message: "bad config"} }}, nil
		}
		code := Run(context.Background(), []string{command, "--json"}, deps)
		if code != 0 || stderr.Len() != 0 {
			t.Fatal(code, stderr.String())
		}
	}
}
func TestPiped_TSVNoHeaderColourOrTruncation(t *testing.T) {
	fixture := servicetest.DefaultFixture()
	title := strings.Repeat("猫", 120)
	fixture.Issues[0].Issue.Title = title
	out, stderr, code := runFake(servicetest.New(fixture), false, "list", "--limit", "1")
	if code != 0 || stderr != "" || strings.Contains(out, "\x1b") || !strings.Contains(out, title) || !strings.HasPrefix(out, "6\t") {
		t.Fatal(code, out, stderr)
	}
}
func TestTerminal_WidthUnicodeAndRelativeTimes(t *testing.T) {
	fixture := servicetest.DefaultFixture()
	fixture.Issues[0].Issue.Title = strings.Repeat("猫", 120)
	out, stderr, code := runFake(servicetest.New(fixture), true, "list", "--limit", "1", "--no-color")
	if code != 0 || stderr != "" || !strings.Contains(out, "…") || !strings.Contains(out, "1h") {
		t.Fatal(code, out, stderr)
	}
}
func TestNoColor_EnvAndFlag(t *testing.T) {
	for _, mode := range []string{"default", "flag", "env-empty"} {
		var out, stderr bytes.Buffer
		deps := fakeDeps(servicetest.New(servicetest.DefaultFixture()), &out, &stderr, true)
		args := []string{"list"}
		if mode == "flag" {
			args = append(args, "--no-color")
		}
		if mode == "env-empty" {
			deps.LookupEnv = func(k string) (string, bool) { return "", k == "NO_COLOR" }
		}
		if code := Run(context.Background(), args, deps); code != 0 {
			t.Fatal(code)
		}
		if strings.Contains(out.String(), "\x1b") != (mode == "default") {
			t.Fatal(mode, out.String())
		}
	}
}
func TestMaliciousText_AlwaysSanitised(t *testing.T) {
	fixture := servicetest.DefaultFixture()
	fixture.Issues[0].Issue.Title = "safe\x1b]52;c;owned\x07\x1b[31mtitle\x1b[0m\u202e"
	for _, human := range []bool{false, true} {
		out, stderr, code := runFake(servicetest.New(fixture), human, "list", "--no-color")
		if code != 0 || stderr != "" || strings.ContainsAny(out, "\x1b\x07\u202e") || strings.Contains(out, "owned") {
			t.Fatal(code, out, stderr)
		}
	}
	out, _, _ := runFake(servicetest.New(fixture), false, "list", "--json")
	var dto IssuesDTO
	json.Unmarshal([]byte(out), &dto)
	if dto.Issues[0].Title != fixture.Issues[0].Issue.Title {
		t.Fatal("JSON changed content")
	}
}
func TestDetail_PipedSafeRawMarkdown(t *testing.T) {
	fixture := servicetest.DefaultFixture()
	fixture.Issues[0].Body = "# Raw **Markdown**\r\n\x1b]52;c;owned\a[link](relative)"
	out, stderr, code := runFake(servicetest.New(fixture), false, "view", "6")
	if code != 0 || stderr != "" || !strings.Contains(out, "# Raw **Markdown**") || strings.ContainsAny(out, "\x1b\r\a") {
		t.Fatal(code, out, stderr)
	}
}
func TestCLI_UsesSharedFixturesAndGates(t *testing.T) {
	f := servicetest.New(servicetest.DefaultFixture())
	gate := f.Hold("ListIssues", 0)
	done := make(chan int, 1)
	go func() { _, _, code := runFake(f, false, "list", "--json"); done <- code }()
	<-gate.Started
	gate.Fail(&domain.Error{Kind: domain.ErrForbidden, Message: "gated"})
	if code := <-done; code != 4 {
		t.Fatal(code)
	}
	if f.Calls()[0].Method != "ListIssues" {
		t.Fatal(f.Calls())
	}
}

func TestDoctor_ServiceFailureStillSevenRows(t *testing.T) {
	f := servicetest.New(servicetest.DefaultFixture())
	f.Hold("Doctor", 0).Fail(&domain.Error{Kind: domain.ErrTimeout, Message: "interrupted check"})
	out, stderr, code := runFake(f, false, "doctor", "--json")
	var dto DoctorDTO
	json.Unmarshal([]byte(out), &dto)
	if code != 1 || stderr != "" || len(dto.Checks) != 7 {
		t.Fatal(code, out, stderr)
	}
}
func TestNoArgs_IgnoresProcessArgs(t *testing.T) {
	saved := os.Args
	os.Args = []string{"test", "--unknown-process-flag"}
	defer func() { os.Args = saved }()
	out, stderr, code := execute(context.Background())
	if code != 2 || out != "" || !strings.Contains(stderr, "TUI requires a terminal") {
		t.Fatal(code, out, stderr)
	}
}
func TestNoPrompt_ReadsStdinOnlyForExplicitDash(t *testing.T) {
	for _, args := range [][]string{{"list"}, {"view", "6"}, {"pr", "view", "101"}, {"labels"}, {"assignees"}, {"board"}, {"doctor"}} {
		_, stderr, code := runFake(servicetest.New(servicetest.DefaultFixture()), false, args...)
		if code != 0 {
			t.Fatal(args, stderr)
		}
	}
}
func TestRef_ReadWrongRepoAndKindNoService(t *testing.T) {
	for _, args := range [][]string{{"view", "https://github.com/pkieltyka/other/issues/6"}, {"view", "https://github.com/pkieltyka/todos/pull/6"}, {"pr", "view", "https://github.com/pkieltyka/todos/issues/6"}, {"board", "use", "https://other.example/users/pkieltyka/projects/1"}} {
		f := servicetest.New(servicetest.DefaultFixture())
		out, stderr, code := runFake(f, false, args...)
		if code != 2 || out != "" || stderr == "" || len(f.Calls()) != 0 {
			t.Fatal(args, code, out, stderr)
		}
	}
}
func TestTSV_EscapesAndSanitisesFields(t *testing.T) {
	got := tsvField("slash\\tab\tline\nreturn\r\x1b[31mred\x1b[0m")
	if got != "slash\\\\tab\\tline\\nreturn\\rred" {
		t.Fatalf("%q", got)
	}
}

func TestSearch_BoundedStopsAt1000EvenIfHasMore(t *testing.T) {
	calls := 0
	rows, total, truncated, err := collectRows(context.Background(), ListOptions{Limit: 2000}, func(_ context.Context, p domain.PageRequest) (domain.Page[domain.Comment], error) {
		calls++
		if calls > 10 {
			t.Fatal("requested beyond provider search ceiling")
		}
		nodes := []domain.Comment{}
		for i := 0; i < 100; i++ {
			nodes = append(nodes, domain.Comment{ID: fmt.Sprint((calls-1)*100 + i)})
		}
		return domain.Page[domain.Comment]{Items: nodes, Total: 1204, HasMore: true, NextCursor: fmt.Sprint(calls), ProviderTruncated: true}, nil
	}, func(v domain.Comment) string { return v.ID }, true)
	if err != nil || len(rows) != 1000 || total != 1204 || !truncated || calls != 10 {
		t.Fatal(len(rows), total, truncated, err, calls)
	}
}
