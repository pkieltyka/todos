package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/pkieltyka/todos/internal/domain"
	"github.com/pkieltyka/todos/internal/service/servicetest"
)

func writeGolden(t *testing.T, name, text string) {
	t.Helper()
	path := filepath.Join("testdata", "changes", name+".golden")
	if *updateReadGoldens {
		if e := os.MkdirAll(filepath.Dir(path), 0755); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(path, []byte(text), 0644); e != nil {
			t.Fatal(e)
		}
	}
	want, e := os.ReadFile(path)
	if e != nil || string(want) != text {
		t.Fatalf("%s: %v\nwant %q\ngot %q", name, e, want, text)
	}
}
func TestJSON_AllWriteCommandGoldens(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{{"add", []string{"add", "A new task", "--assign", "@me", "--label", "bug", "--status", "Todo"}}, {"edit", []string{"edit", "6", "--title", "Changed", "--body", ""}}, {"assign", []string{"assign", "6", "bob"}}, {"unassign", []string{"unassign", "6"}}, {"reassign", []string{"reassign", "6", "bob"}}, {"label", []string{"label", "6", "docs"}}, {"unlabel", []string{"unlabel", "6", "bug"}}, {"comment", []string{"comment", "6", "**hello**"}}, {"close", []string{"close", "6", "--reason", "not-planned", "--comment", "done"}}, {"reopen", []string{"reopen", "5"}}, {"rm", []string{"rm", "6", "--yes"}}, {"move", []string{"move", "6", "Done"}}, {"board_add", []string{"board", "add", "4"}}, {"board_remove", []string{"board", "remove", "6"}}}
	for _, tc := range cases {
		for _, mode := range []string{"json", "human", "pipe"} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				f := servicetest.New(servicetest.DefaultFixture())
				args := append([]string{}, tc.args...)
				if mode == "json" {
					args = append(args, "--json")
				}
				out, stderr, code := runFake(f, mode == "human", args...)
				if code != 0 || stderr != "" {
					t.Fatalf("%d %s", code, stderr)
				}
				writeGolden(t, tc.name+"."+mode, out)
			})
		}
	}
}
func TestFlags_WriteExclusivityAndConfirmationBeforeService(t *testing.T) {
	for _, args := range [][]string{{"add", "title", "--body", "a", "--body-file", "-"}, {"edit", "6"}, {"edit", "6", "--body", "a", "--body-file", "-"}, {"comment", "6"}, {"comment", "6", "text", "--body-file", "-"}, {"comment", "6", "  "}, {"close", "6", "--reason", "not_planned"}, {"close", "6", "--comment", " "}, {"rm", "6"}, {"rm", "6", "--yes=false"}, {"move", "6"}, {"move", "6", "Done", "--no-status"}, {"move", "6", ""}, {"add"}, {"assign", "6"}, {"reassign", "6"}, {"label", "6"}, {"unlabel", "6"}, {"board", "add"}, {"board", "remove", "1", "2"}} {
		f := servicetest.New(servicetest.DefaultFixture())
		out, stderr, code := runFake(f, false, append(args, "--json")...)
		if code != 2 || out != "" || len(f.Calls()) != 0 || !strings.Contains(stderr, `"kind":"usage"`) {
			t.Fatal(args, code, out, stderr, f.Calls())
		}
	}
}
func TestEdit_BodyFileCountsAsEdit(t *testing.T) {
	f := servicetest.New(servicetest.DefaultFixture())
	var out, stderr bytes.Buffer
	deps := fakeDeps(f, &out, &stderr, false)
	deps.In = strings.NewReader("")
	code := Run(context.Background(), []string{"edit", "6", "--body-file", "-", "--json"}, deps)
	if code != 0 {
		t.Fatal(stderr.String())
	}
	calls := f.Calls()
	if len(calls) != 1 || calls[0].Args.Edit.Body == nil || *calls[0].Args.Edit.Body != "" {
		t.Fatal(calls)
	}
}
func TestNoPrompt_WriteReadsStdinOnlyForExplicitDash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "body.md")
	if e := os.WriteFile(path, []byte("file body\n"), 0600); e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		args []string
		in   io.Reader
		body string
	}{{[]string{"add", "title", "--body", "direct"}, panicReader{}, "direct"}, {[]string{"add", "title", "--body-file", path}, panicReader{}, "file body\n"}, {[]string{"comment", "6", "--body-file", "-"}, strings.NewReader("stdin body"), "stdin body"}} {
		f := servicetest.New(servicetest.DefaultFixture())
		var out, stderr bytes.Buffer
		deps := fakeDeps(f, &out, &stderr, false)
		deps.In = tc.in
		if code := Run(context.Background(), tc.args, deps); code != 0 {
			t.Fatal(code, stderr.String())
		}
		call := f.Calls()[0]
		if call.Method == "CreateIssue" && call.Args.NewIssue.Body != tc.body || call.Method == "Comment" && call.Args.Text != tc.body {
			t.Fatal(call)
		}
	}
}
func TestReason_HyphenToUnderscore(t *testing.T) {
	f := servicetest.New(servicetest.DefaultFixture())
	_, stderr, code := runFake(f, false, "close", "6", "--reason", "not-planned")
	if code != 0 || f.Calls()[0].Args.Close.Reason != "not_planned" {
		t.Fatal(stderr, f.Calls())
	}
}
func TestJSON_WriteCommandViewFieldsAbsent(t *testing.T) {
	f := servicetest.New(servicetest.DefaultFixture())
	out, stderr, code := runFake(f, false, "edit", "6", "--body", "new", "--json")
	if code != 0 {
		t.Fatal(stderr)
	}
	var v struct{ Issue map[string]any }
	if e := json.Unmarshal([]byte(out), &v); e != nil {
		t.Fatal(e)
	}
	for _, field := range []string{"body", "comments", "comments_truncated", "NodeID", "viewer_can_update"} {
		if _, ok := v.Issue[field]; ok {
			t.Fatal("leaked", field)
		}
	}
}
func TestPartial_WriteCommandNoInternalRemovedStep(t *testing.T) {
	f := servicetest.New(servicetest.DefaultFixture())
	i := servicetest.DefaultFixture().Issues[0].Issue
	g := f.Hold("UpdateLabels", 6)
	g.Fail(&domain.Error{Kind: domain.ErrPartial, Message: "partly done", Cause: domain.ErrTimeout, Uncertain: true, Completed: []string{"removed", "created", "commented", "added_to_board"}, Issue: &i})
	out, stderr, code := runFake(f, false, "label", "6", "docs", "--json")
	if code != 7 || out != "" {
		t.Fatal(code, out, stderr)
	}
	var envelope struct{ Error map[string]json.RawMessage }
	if e := json.Unmarshal([]byte(stderr), &envelope); e != nil {
		t.Fatal(e)
	}
	keys := []string{"kind", "message", "hint", "uncertain", "completed", "cause", "issue"}
	if len(envelope.Error) != len(keys) {
		t.Fatal(stderr)
	}
	for _, key := range keys {
		if _, ok := envelope.Error[key]; !ok {
			t.Fatal("missing", key)
		}
	}
	if strings.Contains(stderr, "removed") {
		t.Fatal(stderr)
	}
	writeGolden(t, "partial.stderr.json", stderr)
}
func TestOutputFailure_RealWriteNeverRetries(t *testing.T) {
	f := servicetest.New(servicetest.DefaultFixture())
	var stderr bytes.Buffer
	deps := fakeDeps(f, writeFailure{}, &stderr, false)
	code := Run(context.Background(), []string{"add", "title", "--json"}, deps)
	if code != 1 || len(f.Calls()) != 1 || f.Calls()[0].Method != "CreateIssue" {
		t.Fatal(code, stderr.String(), f.Calls())
	}
}
func TestChanges_NoOpAndSafeText(t *testing.T) {
	f := servicetest.New(servicetest.DefaultFixture())
	f.Mutate(func(x *servicetest.Fixture) {
		for j := range x.Issues {
			if x.Issues[j].Issue.Number == 6 {
				x.Issues[j].Issue.Title = "bad\x1b[2Jtitle\n"
				x.Issues[j].Issue.State = "closed"
			}
		}
	})
	out, stderr, code := runFake(f, false, "close", "6", "--comment", "do not add")
	if code != 0 || stderr != "" || !strings.HasPrefix(out, "No change to #6 ") || strings.Contains(out, "\x1b") {
		t.Fatal(code, out, stderr)
	}
}
func TestChanges_UnknownRefsNeverWrite(t *testing.T) {
	f := servicetest.New(servicetest.DefaultFixture())
	out, _, code := runFake(f, false, "rm", "https://github.com/other/repo/issues/6", "--yes")
	if code != 2 || out != "" || len(f.Calls()) > 0 {
		t.Fatal(code, out, f.Calls())
	}
}
func TestChanges_ValueEqualJSONIsBody(t *testing.T) {
	f := servicetest.New(servicetest.DefaultFixture())
	g := f.Hold("CreateIssue", 0)
	g.Fail(&domain.Error{Kind: domain.ErrForbidden, Message: "denied"})
	out, stderr, code := runFake(f, false, "add", "title", "--body", "--json")
	if code != 4 || out != "" || !strings.HasPrefix(stderr, "error:") || f.Calls()[0].Args.NewIssue.Body != "--json" {
		t.Fatal(code, out, stderr)
	}
}
func TestChanges_RemoveAllAndDeltaArguments(t *testing.T) {
	f := servicetest.New(servicetest.DefaultFixture())
	d, _ := f.GetIssue(context.Background(), 6)
	before := len(f.Calls())
	_, stderr, code := runFake(f, false, "unassign", "6")
	if code != 0 {
		t.Fatal(stderr)
	}
	calls := f.Calls()[before:]
	if len(calls) != 2 || calls[0].Method != "GetIssue" || calls[1].Method != "UpdateAssignees" || !reflect.DeepEqual(calls[1].Args.Remove, d.Issue.Assignees) || len(calls[1].Args.Add) != 0 {
		t.Fatal(calls)
	}
}
func TestChanges_WriteErrorEveryFieldAndSignal(t *testing.T) {
	f := servicetest.New(servicetest.DefaultFixture())
	g := f.Hold("CreateIssue", 0)
	g.Fail(&domain.Error{Kind: domain.ErrNetwork, Message: "operation interrupted", Uncertain: true, Err: context.Canceled})
	var out, stderr bytes.Buffer
	deps := fakeDeps(f, &out, &stderr, false)
	deps.Interrupted = func() bool { return true }
	code := Run(context.Background(), []string{"add", "title", "--json"}, deps)
	if code != 130 || out.Len() != 0 || !strings.Contains(stderr.String(), `"uncertain":true`) {
		t.Fatal(code, out.String(), stderr.String())
	}
	if !errors.Is(f.Calls()[0].Err, context.Canceled) {
		t.Fatal("lost cancellation")
	}
}

type writeFailure struct{}

func (writeFailure) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestUnassign_NoNamesRemovesDepartedAssignee(t *testing.T) {
	f := servicetest.New(servicetest.DefaultFixture())
	f.Mutate(func(x *servicetest.Fixture) {
		x.Users = nil
		for j := range x.Issues {
			if x.Issues[j].Issue.Number == 6 {
				x.Issues[j].Issue.Assignees = []string{"Departed"}
				x.Issues[j].Issue.AssigneeIDs = map[string]domain.NodeID{"Departed": "DEPARTED"}
			}
		}
	})
	out, stderr, code := runFake(f, false, "unassign", "6", "--json")
	if code != 0 || stderr != "" {
		t.Fatal(code, out, stderr)
	}
	var result ChangeDTO
	if e := json.Unmarshal([]byte(out), &result); e != nil || !result.Changed || len(result.Issue.Assignees) > 0 {
		t.Fatal(result, e)
	}
	calls := f.Calls()
	if len(calls) != 2 || calls[1].Method != "UpdateAssignees" || !reflect.DeepEqual(calls[1].Args.Remove, []string{"Departed"}) {
		t.Fatal(calls)
	}
}
