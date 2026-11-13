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

	"github.com/pkieltyka/todos/internal/domain"
	"github.com/spf13/cobra"
)

type panicReader struct{}

func (panicReader) Read([]byte) (int, error) { panic("foundation command read stdin") }
func execute(ctx context.Context, args ...string) (stdout, stderr string, code int) {
	var out, err bytes.Buffer
	code = Run(ctx, args, Dependencies{In: panicReader{}, Out: &out, Err: &err, Version: "1.2.3", Skill: []byte("---\nname: todos\n---\nplaceholder\n")})
	return out.String(), err.String(), code
}
func golden(t *testing.T, name, actual string) {
	t.Helper()
	want, err := os.ReadFile(filepath.Join("testdata", name+".golden"))
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != actual {
		t.Fatalf("%s differs\nwant: %q\n got: %q", name, want, actual)
	}
}

func TestOffline_HelpVersionSkillNoService(t *testing.T) {
	t.Setenv("GH_TOKEN", "invalid-do-not-use")
	t.Setenv("GH_REPO", "invalid repo")
	t.Setenv("GH_HOST", "invalid.example")
	t.Setenv("PATH", t.TempDir())
	t.Chdir(t.TempDir())
	for _, args := range [][]string{{"--help"}, {"help"}, {"help", "version"}, {"version"}, {"--version"}, {"skill"}} {
		out, err, code := execute(context.Background(), args...)
		if code != 0 || err != "" || out == "" {
			t.Fatalf("%v: %d %q %q", args, code, out, err)
		}
	}
}
func TestJSON_AllCommandGoldens(t *testing.T) {
	for _, args := range [][]string{{"version", "--json"}, {"--json", "version"}, {"--version", "--json"}, {"version", "--json=true"}} {
		out, err, code := execute(context.Background(), args...)
		if code != 0 || err != "" {
			t.Fatalf("%v: %d %q", args, code, err)
		}
		golden(t, "version.json", out)
	}
	out, err, code := execute(context.Background(), "skill", "--json")
	if code != 2 || out != "" {
		t.Fatal("skill JSON usage contract")
	}
	golden(t, "skill_json.stderr.json", err)
}
func TestVersion_TextAndSkillBytes(t *testing.T) {
	out, err, code := execute(context.Background(), "version")
	if code != 0 || err != "" {
		t.Fatal(code, err)
	}
	golden(t, "version.stdout", out)
	out, err, code = execute(context.Background(), "skill")
	if code != 0 || err != "" || out != "---\nname: todos\n---\nplaceholder\n" {
		t.Fatal("skill bytes changed")
	}
}

func TestCommands_ArgumentBounds(t *testing.T) {
	for _, args := range [][]string{{"version", "extra"}, {"skill", "extra"}, {"unknown"}, {"--unknown"}, {"--repo"}, {"--json"}, {"--json", "skill"}} {
		out, err, code := execute(context.Background(), args...)
		if code != 2 || out != "" || err == "" {
			t.Fatalf("%v: %d %q %q", args, code, out, err)
		}
	}
}
func TestHelp_GroupsUsageAndNoCompletion(t *testing.T) {
	out, err, code := execute(context.Background(), "--help")
	if code != 0 || err != "" || !strings.Contains(out, "no arguments opens the TUI") || !strings.Contains(out, "Other commands:") || strings.Contains(out, "completion") {
		t.Fatalf("help: %q %q %d", out, err, code)
	}
	previous := -1
	for _, heading := range []string{"Issues:", "Board:", "Pull requests:", "Other commands:"} {
		index := strings.Index(out, heading)
		if index <= previous {
			t.Fatalf("help group order: %s\n%s", heading, out)
		}
		previous = index
	}

	out, err, code = execute(context.Background(), "help", "version")
	if code != 0 || !strings.Contains(out, "todos version --json") {
		t.Fatal("missing version help/examples")
	}
	out, err, code = execute(context.Background(), "version", "extra")
	if code != 2 || out != "" || !strings.Contains(err, "Usage:") || strings.Contains(err, "Available Commands:") {
		t.Fatal("usage printed full help")
	}
}
func TestJSON_UnknownFlagRespectsRequestedMode(t *testing.T) {
	for _, args := range [][]string{{"--unknown", "--json"}, {"unknown", "--json"}, {"--json", "--unknown"}, {"skill", "--json"}, {"--json"}} {
		out, stderr, code := execute(context.Background(), args...)
		if code != 2 || out != "" {
			t.Fatal(code, out)
		}
		var envelope map[string]any
		dec := json.NewDecoder(strings.NewReader(stderr))
		if err := dec.Decode(&envelope); err != nil {
			t.Fatalf("%v: %v %q", args, err, stderr)
		}
		if _, ok := envelope["error"]; !ok {
			t.Fatal("missing envelope")
		}
		var extra any
		if dec.Decode(&extra) != io.EOF {
			t.Fatal("stderr has trailing prose/document")
		}
	}
}
func TestDetectJSON_ValuesAndEndOfFlags(t *testing.T) {
	for _, test := range []struct {
		args []string
		want bool
	}{{[]string{"--json"}, true}, {[]string{"--repo", "--json", "version"}, false}, {[]string{"--project", "--json"}, false}, {[]string{"--json", "--json=false"}, false}, {[]string{"--", "--json"}, false}, {[]string{"--repo=owner/repo", "--json=true"}, true}} {
		if got := detectJSON(NewRoot(Dependencies{}), test.args); got != test.want {
			t.Fatalf("%v: %v", test.args, got)
		}
	}
}
func TestExit_AllKindsAndSignal130(t *testing.T) {
	for _, test := range []struct {
		kind domain.ErrorKind
		code int
	}{{domain.ErrNetwork, 1}, {domain.ErrTimeout, 1}, {domain.ErrGitHub, 1}, {domain.ErrInternal, 1}, {domain.ErrUsage, 2}, {domain.ErrInvalidInput, 2}, {domain.ErrNotFound, 3}, {domain.ErrAuth, 4}, {domain.ErrMissingScope, 4}, {domain.ErrForbidden, 4}, {domain.ErrRateLimited, 5}, {domain.ErrNoRepo, 6}, {domain.ErrNoBoard, 6}, {domain.ErrIssuesDisabled, 6}, {domain.ErrConfig, 6}, {domain.ErrPartial, 7}} {
		t.Run(string(test.kind), func(t *testing.T) {
			if got := exitCode(&domain.Error{Kind: test.kind}, false); got != test.code {
				t.Fatal(got)
			}
		})
	}
	e := &domain.Error{Kind: domain.ErrPartial, Err: &domain.Error{Kind: domain.ErrNetwork, Err: context.Canceled}}
	if exitCode(e, true) != 130 || exitCode(e, false) != 7 || exitCode(nil, false) != 0 || exitCode(errors.New("unexpected"), false) != 1 {
		t.Fatal("signal or fallback mapping")
	}
}
func TestInterrupt_130(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out, stderr bytes.Buffer
	code := Run(ctx, []string{"version", "--json"}, Dependencies{Out: &out, Err: &stderr, Interrupted: func() bool { return true }})
	if code != 130 || out.Len() != 0 || !strings.Contains(stderr.String(), "operation interrupted") {
		t.Fatal(code, out.String(), stderr.String())
	}
}

type brokenWriter struct{ short bool }

func (w brokenWriter) Write(p []byte) (int, error) {
	if w.short {
		return len(p) - 1, nil
	}
	return 0, errors.New("broken stream")
}
func TestOutputFailure_NeverRetriesWrite(t *testing.T) {
	for _, short := range []bool{true, false} {
		var stderr bytes.Buffer
		code := Run(context.Background(), []string{"version"}, Dependencies{Out: brokenWriter{short: short}, Err: &stderr})
		if code != 1 || !strings.Contains(stderr.String(), "could not write output") {
			t.Fatal(code, stderr.String())
		}
	}
}

func TestJSON_FlagValueEqualsJSON(t *testing.T) {
	for _, args := range [][]string{
		{"--body", "--json", "extra"},
		{"-b", "--json", "extra"},
		{"--body=--json", "extra"},
		{"--unknown", "--body", "--json"},
		{"--unknown", "-b", "--json"},
	} {
		root := NewRoot(Dependencies{})
		root.PersistentFlags().StringP("body", "b", "", "Future body flag")
		parseFailed := false
		root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { parseFailed = true; return err })
		root.SetArgs(args)
		cmd, err := root.ExecuteContextC(context.Background())
		if err == nil {
			t.Fatalf("expected usage error: %v", args)
		}
		if errorJSONMode(root, cmd, args, parseFailed) {
			t.Fatalf("body value changed error format: %v", args)
		}
	}
	// An explicit JSON flag must still win after a body whose value is --json.
	root := NewRoot(Dependencies{})
	root.PersistentFlags().String("body", "", "Future body flag")
	args := []string{"--body", "--json", "--json", "extra"}
	root.SetArgs(args)
	cmd, err := root.ExecuteContextC(context.Background())
	if err == nil || !errorJSONMode(root, cmd, args, false) {
		t.Fatal("explicit JSON flag lost")
	}
}
