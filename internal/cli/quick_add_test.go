package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pkieltyka/todos/internal/config"
	"github.com/pkieltyka/todos/internal/domain"
	"github.com/pkieltyka/todos/internal/service/servicetest"
)

func quickGolden(t *testing.T, name, text string) {
	t.Helper()
	path := filepath.Join("testdata", "quick_add", name+".golden")
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
		t.Fatalf("%s %v\nwant %q\ngot %q", name, e, want, text)
	}
}
func quickCLI(f *servicetest.Fake, source string, human bool, args ...string) (string, string, int) {
	var out, stderr bytes.Buffer
	d := fakeDeps(f, &out, &stderr, human)
	d.In = strings.NewReader(source)
	code := Run(context.Background(), append([]string{"quick-add", "--file", "-"}, args...), d)
	return out.String(), stderr.String(), code
}
func quickDecode(t *testing.T, text string) QuickDTO {
	t.Helper()
	var v QuickDTO
	if e := json.Unmarshal([]byte(text), &v); e != nil {
		t.Fatal(text, e)
	}
	return v
}
func TestQuickAdd_CommandGoldens(t *testing.T) {
	for _, kind := range []string{"dry", "skip", "close", "board"} {
		for _, mode := range []string{"json", "human", "pipe"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				args := []string{}
				switch kind {
				case "dry":
					args = append(args, "--dry-run")
				case "close":
					args = append(args, "--checked", "close")
				case "board":
					args = append(args, "--status", "Todo")
				}
				if mode == "json" {
					args = append(args, "--json")
				} else {
					args = append(args, "--no-color")
				}
				out, stderr, code := quickCLI(servicetest.New(servicetest.DefaultFixture()), "- first\r\n  **body**\r\n- [x] finished\r\nlast\r\n", mode == "human", args...)
				if code != 0 || stderr != "" {
					t.Fatal(code, out, stderr)
				}
				quickGolden(t, kind+"."+mode, out)
			})
		}
	}
}
func TestQuickAdd_DryRunPlanned(t *testing.T) {
	f := servicetest.New(servicetest.DefaultFixture())
	out, stderr, code := quickCLI(f, "first\n- [x] done", false, "--dry-run", "--checked", "close", "--json")
	if code != 0 || stderr != "" {
		t.Fatal(code, stderr)
	}
	r := quickDecode(t, out)
	if r.Counts.Planned != 2 || r.Counts.Created != 0 || r.Items[1].Result != "planned" || r.Items[0].Issue != nil {
		t.Fatal(r)
	}
	for _, c := range f.Calls() {
		if c.Method == "CreateIssue" {
			t.Fatal("dry run wrote")
		}
	}
}
func TestQuickAdd_PartialStdoutAndStderr7(t *testing.T) {
	f := servicetest.New(servicetest.DefaultFixture())
	first := f.Hold("CreateIssue", 0)
	first.Release()
	second := f.Hold("CreateIssue", 0)
	second.Fail(&domain.Error{Kind: domain.ErrRateLimited, Message: "rate limited", RetryAfter: 1500 * time.Millisecond})
	out, stderr, code := quickCLI(f, "one\ntwo\nthree", false, "--json")
	r := quickDecode(t, out)
	if code != 7 || r.Counts.Created != 1 || r.Counts.Failed != 1 || r.Counts.Unattempted != 1 || !strings.Contains(stderr, `"kind":"partial"`) || r.Items[1].Error.RetryAfterSeconds == nil || *r.Items[1].Error.RetryAfterSeconds != 2 {
		t.Fatal(code, r, stderr)
	}
	quickGolden(t, "partial.stdout.json", out)
	quickGolden(t, "partial.stderr.json", stderr)
}
func TestQuickAdd_FirstRejectedUsesCauseExit(t *testing.T) {
	f := servicetest.New(servicetest.DefaultFixture())
	g := f.Hold("CreateIssue", 0)
	g.Fail(&domain.Error{Kind: domain.ErrForbidden, Message: "denied"})
	out, stderr, code := quickCLI(f, "one\ntwo", false, "--json")
	r := quickDecode(t, out)
	if code != 4 || r.Counts.Failed != 1 || r.Counts.Unattempted != 1 || !strings.Contains(stderr, `"kind":"forbidden"`) {
		t.Fatal(code, r, stderr)
	}
}
func TestQuickAdd_FirstUnknownPartial7(t *testing.T) {
	f := servicetest.New(servicetest.DefaultFixture())
	g := f.Hold("CreateIssue", 0)
	g.ApplyThenFail(&domain.Error{Kind: domain.ErrTimeout, Message: "timeout", Uncertain: true})
	out, stderr, code := quickCLI(f, "one\ntwo", false, "--json")
	r := quickDecode(t, out)
	if code != 7 || r.Counts.Uncertain != 1 || r.Items[0].Issue != nil || !r.Items[0].Error.Uncertain || !strings.Contains(stderr, `"uncertain":true`) {
		t.Fatal(code, r, stderr)
	}
	quickGolden(t, "uncertain.stdout.json", out)
	quickGolden(t, "uncertain.stderr.json", stderr)
}
func TestQuickAdd_CreatedButNotClosedOrPlaced(t *testing.T) {
	for _, where := range []string{"closing", "placing"} {
		for _, jsonMode := range []bool{false, true} {
			f := servicetest.New(servicetest.DefaultFixture())
			args := []string{}
			source := "one\ntwo"
			method := "BoardAdd"
			if where == "closing" {
				source = "- [x] one\ntwo"
				args = append(args, "--checked", "close")
				method = "Close"
			} else {
				args = append(args, "--status", "Todo")
			}
			if jsonMode {
				args = append(args, "--json")
			}
			g := f.Hold(method, 104)
			g.Fail(&domain.Error{Kind: domain.ErrForbidden, Message: "denied"})
			out, stderr, code := quickCLI(f, source, false, args...)
			if code != 7 {
				t.Fatal(code, out, stderr)
			}
			if jsonMode {
				r := quickDecode(t, out)
				if r.Counts.Created != 1 || r.Items[0].Issue == nil || r.Items[0].Closed || r.Items[0].OnBoard || r.Items[0].Error == nil {
					t.Fatal(r)
				}
			} else if where == "closing" && !strings.Contains(out, "not closed") || where == "placing" && !strings.Contains(out, "not placed on board") {
				t.Fatal(out)
			}
		}
	}
}
func TestQuickAdd_SignalAccountsRemaining(t *testing.T) {
	f := servicetest.New(servicetest.DefaultFixture())
	g := f.Hold("CreateIssue", 0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out, stderr bytes.Buffer
	d := fakeDeps(f, &out, &stderr, false)
	d.In = strings.NewReader("one\ntwo\nthree")
	d.Interrupted = func() bool { return ctx.Err() != nil }
	done := make(chan int, 1)
	go func() { done <- Run(ctx, []string{"quick-add", "--file", "-", "--json"}, d) }()
	select {
	case <-g.Started:
	case <-time.After(5 * time.Second):
		t.Fatal("create not reached")
	}
	cancel()
	var code int
	select {
	case code = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation did not stop")
	}
	r := quickDecode(t, out.String())
	if code != 130 || r.Counts.Failed != 1 || r.Counts.Unattempted != 2 || !strings.Contains(stderr.String(), `"kind":"network"`) {
		t.Fatal(code, r, stderr.String())
	}
}
func TestQuickAdd_PreflightErrorsNoStdout(t *testing.T) {
	for _, source := range []string{"", "first\n-\n bad", "  orphan\nfirst", strings.Repeat("- item\n", 51)} {
		f := servicetest.New(servicetest.DefaultFixture())
		out, stderr, code := quickCLI(f, source, false, "--json")
		if code != 2 || out != "" || !strings.Contains(stderr, "line ") {
			t.Fatal(code, out, stderr)
		}
		for _, c := range f.Calls() {
			if c.Method == "CreateIssue" || c.Method == "RunQuickAdd" {
				t.Fatal(c)
			}
		}
	}
	f := servicetest.New(servicetest.DefaultFixture())
	g := f.Hold("PlanQuickAdd", 0)
	g.Fail(&domain.Error{Kind: domain.ErrAuth, Message: "login"})
	out, _, code := quickCLI(f, "one", false, "--json")
	if out != "" || code != 4 {
		t.Fatal(code, out)
	}
}
func TestQuickAdd_FlagValidationNoReadsOrService(t *testing.T) {
	for _, args := range [][]string{{"quick-add"}, {"quick-add", "--file", ""}, {"quick-add", "--file", "-", "--checked", "bad"}, {"quick-add", "--file", "-", "extra"}} {
		f := servicetest.New(servicetest.DefaultFixture())
		out, stderr, code := runFake(f, false, append(args, "--json")...)
		if code != 2 || out != "" || len(f.Calls()) > 0 || !strings.Contains(stderr, `"kind":"usage"`) {
			t.Fatal(code, out, stderr, f.Calls())
		}
	}
}
func TestQuickAdd_FileAndSharedOptions(t *testing.T) {
	f := servicetest.New(servicetest.DefaultFixture())
	path := filepath.Join(t.TempDir(), "tasks.md")
	if e := os.WriteFile(path, []byte("task\n  body"), 0600); e != nil {
		t.Fatal(e)
	}
	out, stderr, code := runFake(f, false, "quick-add", "--file", path, "--assign", "@me", "--label", "bug", "--status", "Todo", "--checked", "close", "--json")
	if code != 0 || stderr != "" {
		t.Fatal(code, out, stderr)
	}
	var found bool
	for _, c := range f.Calls() {
		if c.Method == "PlanQuickAdd" {
			found = true
			if c.Args.Text != "task\n  body" || !c.Args.QuickAdd.AddToBoard || c.Args.QuickAdd.Checked != "close" || len(c.Args.QuickAdd.Assignees) != 1 || len(c.Args.QuickAdd.Labels) != 1 {
				t.Fatal(c)
			}
		}
	}
	if !found {
		t.Fatal("no plan")
	}
}
func TestQuickAdd_FileReadErrorAndJSONValue(t *testing.T) {
	f := servicetest.New(servicetest.DefaultFixture())
	out, stderr, code := runFake(f, false, "quick-add", "--file", "/no-such-todos-file", "--json")
	if code != 2 || out != "" || len(f.Calls()) != 0 || !strings.Contains(stderr, `"kind":"invalid_input"`) {
		t.Fatal(code, out, stderr)
	}
	out, stderr, code = runFake(f, false, "quick-add", "--file", "--json")
	if code != 2 || out != "" || strings.HasPrefix(stderr, "{") {
		t.Fatal(code, out, stderr)
	}
}
func TestQuickAdd_OnlyJSONSkipsSanitising(t *testing.T) {
	for _, jsonMode := range []bool{false, true} {
		f := servicetest.New(servicetest.DefaultFixture())
		args := []string{}
		if jsonMode {
			args = append(args, "--json")
		}
		out, stderr, code := quickCLI(f, "title\x1b[2Jevil", false, args...)
		if code != 0 || stderr != "" || strings.Contains(out, "\x1b") {
			t.Fatal(code, out, stderr)
		}
		if jsonMode && quickDecode(t, out).Items[0].Title != "title\x1b[2Jevil" {
			t.Fatal(out)
		}
	}
}
func TestQuickAdd_OutputFailureDoesNotReplay(t *testing.T) {
	f := servicetest.New(servicetest.DefaultFixture())
	var stderr bytes.Buffer
	d := fakeDeps(f, writeFailure{}, &stderr, false)
	d.In = strings.NewReader("one")
	code := Run(context.Background(), []string{"quick-add", "--file", "-", "--json"}, d)
	if code != 1 {
		t.Fatal(code, stderr.String())
	}
	creates := 0
	for _, c := range f.Calls() {
		if c.Method == "CreateIssue" {
			creates++
		}
	}
	if creates != 1 {
		t.Fatal("write replayed", f.Calls())
	}
}
func TestQuickAdd_PlanFailureLeavesNoDraftFiles(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	d := completeDeps(Dependencies{Out: io.Discard, Err: io.Discard})
	w, e := d.Wire(context.Background(), func(context.Context) (domain.RepoRef, error) {
		return servicetest.DefaultFixture().Session.Repo.Ref, nil
	}, "")
	if e != nil {
		t.Fatal(e)
	}
	if _, ok := w.Drafts.(*config.DraftStore); !ok {
		t.Fatalf("drafts not wired: %T", w.Drafts)
	}
	entries, e := os.ReadDir(state)
	if e != nil || len(entries) != 0 {
		t.Fatal("construction wrote state", entries, e)
	}
}
func TestQuickAdd_ErrorsDoNotDuplicateEnvelope(t *testing.T) {
	f := servicetest.New(servicetest.DefaultFixture())
	g := f.Hold("CreateIssue", 0)
	g.Fail(&domain.Error{Kind: domain.ErrNetwork, Message: "offline", Err: errors.New("hidden")})
	_, stderr, code := quickCLI(f, "one", false, "--json")
	if code != 1 || strings.Count(stderr, "\n") != 1 || strings.Contains(stderr, "hidden") {
		t.Fatal(code, stderr)
	}
}
