package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/pkieltyka/todos/internal/domain"
	"github.com/pkieltyka/todos/internal/markdown"
	"github.com/pkieltyka/todos/internal/service/servicetest"
	"github.com/pkieltyka/todos/internal/tui"
)

func tuiDeps(t *testing.T, out, stderr io.Writer) Dependencies {
	t.Helper()
	d := fakeDeps(servicetest.New(servicetest.DefaultFixture()), out, stderr, true)
	d.InputIsTTY = func(io.Reader) bool { return true }
	d.RunTUI = func(context.Context, tui.Deps, tui.Options) error { return nil }
	return d
}
func TestTUI_WiringAndStartView(t *testing.T) {
	for _, test := range []struct {
		args []string
		view tui.ViewID
	}{{nil, tui.ViewIssues}, {[]string{"tui"}, tui.ViewIssues}, {[]string{"tui", "issues"}, tui.ViewIssues}, {[]string{"tui", "prs"}, tui.ViewPRs}, {[]string{"tui", "board"}, tui.ViewBoard}} {
		t.Run(strings.Join(test.args, "_"), func(t *testing.T) {
			var out, stderr bytes.Buffer
			d := tuiDeps(t, &out, &stderr)
			f := servicetest.New(servicetest.DefaultFixture())
			repo := domain.RepoRef{Host: "github.com", Owner: "pkieltyka", Name: "playg"}
			resolves, wires, runs, opens := 0, 0, 0, 0
			d.ResolveRepo = func(ctx context.Context, flag string) (domain.RepoRef, error) {
				resolves++
				if flag != "pkieltyka/playg" {
					t.Fatal(flag)
				}
				return repo, nil
			}
			d.Wire = func(ctx context.Context, resolve func(context.Context) (domain.RepoRef, error), project string) (Wiring, error) {
				wires++
				if project != "1" {
					t.Fatal(project)
				}
				for i := 0; i < 2; i++ {
					r, err := resolve(ctx)
					if err != nil || r != repo {
						t.Fatal(r, err)
					}
				}
				return Wiring{Service: f}, nil
			}
			d.Getenv = func(name string) string {
				switch name {
				case "TODOS_THEME":
					return "Light"
				case "VISUAL":
					return `code --wait "a b"`
				case "EDITOR":
					return "ignored"
				}
				return ""
			}
			d.Open = func(url string) error {
				opens++
				if url != "https://example.test" {
					t.Fatal(url)
				}
				return nil
			}
			d.RunTUI = func(ctx context.Context, deps tui.Deps, opts tui.Options) error {
				runs++
				if opts.StartView != test.view || opts.Repo != repo || deps.Service != f || deps.Drafts != nil || deps.Renderer == nil || !deps.Clock().Equal(fixtureTime) || deps.Env.Theme != "Light" || !reflect.DeepEqual(deps.Env.Editor, []string{"code", "--wait", "a b"}) {
					t.Fatalf("deps=%+v opts=%+v", deps, opts)
				}
				if len(f.Calls()) != 0 {
					t.Fatal("wiring made a service read before first frame")
				}
				rendered, err := deps.Renderer.Render("[guide](docs/guide.md)", 80, markdown.Plain)
				if err != nil || !strings.Contains(ansi.Strip(rendered), "https://github.com/pkieltyka/playg/docs/guide.md") {
					t.Fatal(rendered, err)
				}
				return deps.Open("https://example.test")
			}
			args := append([]string{"--repo", "pkieltyka/playg", "--project", "1"}, test.args...)
			if code := Run(context.Background(), args, d); code != 0 || out.Len() != 0 || stderr.Len() != 0 || resolves != 1 || wires != 1 || runs != 1 || opens != 1 {
				t.Fatal(code, out.String(), stderr.String(), resolves, wires, runs, opens)
			}
		})
	}
}
func TestTUI_NoTTYUsage2(t *testing.T) {
	for _, input := range []bool{false, true} {
		for _, output := range []bool{false, true} {
			if input && output {
				continue
			}
			for _, args := range [][]string{nil, {"tui"}, {"tui", "board"}} {
				var out, stderr bytes.Buffer
				d := tuiDeps(t, &out, &stderr)
				d.InputIsTTY = func(io.Reader) bool { return input }
				d.IsTTY = func(io.Writer) bool { return output }
				d.ResolveRepo = func(context.Context, string) (domain.RepoRef, error) {
					t.Fatal("resolved repo without a terminal")
					return domain.RepoRef{}, nil
				}
				d.Wire = func(context.Context, func(context.Context) (domain.RepoRef, error), string) (Wiring, error) {
					t.Fatal("wired without a terminal")
					return Wiring{}, nil
				}
				d.RunTUI = func(context.Context, tui.Deps, tui.Options) error { t.Fatal("started without terminal"); return nil }
				if code := Run(context.Background(), args, d); code != 2 || out.Len() != 0 || !strings.Contains(stderr.String(), "requires a terminal") {
					t.Fatal(args, code, out.String(), stderr.String())
				}
			}
		}
	}
}
func TestTUI_JSONAndInvalidViewRejectBeforeWiring(t *testing.T) {
	for _, args := range [][]string{{"--json"}, {"tui", "--json"}, {"tui", "prs", "--json"}, {"tui", "invalid"}, {"tui", "issues", "extra"}} {
		var out, stderr bytes.Buffer
		d := tuiDeps(t, &out, &stderr)
		d.ResolveRepo = func(context.Context, string) (domain.RepoRef, error) {
			t.Fatal("resolved invalid invocation")
			return domain.RepoRef{}, nil
		}
		code := Run(context.Background(), args, d)
		if code != 2 || out.Len() != 0 {
			t.Fatal(args, code, out.String(), stderr.String())
		}
		if strings.Contains(strings.Join(args, " "), "--json") && !strings.HasPrefix(stderr.String(), `{"error":`) {
			t.Fatal(stderr.String())
		}
	}
}
func TestTUI_NoRepoBeforeStart(t *testing.T) {
	var out, stderr bytes.Buffer
	d := tuiDeps(t, &out, &stderr)
	d.ResolveRepo = func(context.Context, string) (domain.RepoRef, error) {
		return domain.RepoRef{}, &domain.Error{Kind: domain.ErrNoRepo, Message: "no repository", Hint: "use --repo OWNER/REPO"}
	}
	d.Wire = func(context.Context, func(context.Context) (domain.RepoRef, error), string) (Wiring, error) {
		t.Fatal("wired before resolving repo")
		return Wiring{}, nil
	}
	d.RunTUI = func(context.Context, tui.Deps, tui.Options) error { t.Fatal("TUI started with no repo"); return nil }
	if code := Run(context.Background(), []string{"tui"}, d); code != 6 || out.Len() != 0 || !strings.Contains(stderr.String(), "no repository") {
		t.Fatal(code, out.String(), stderr.String())
	}
}
func TestTUI_EditorPrecedenceAndParseError(t *testing.T) {
	for _, test := range []struct {
		visual, editor string
		want           []string
		code           int
	}{{"", "", nil, 0}, {"", `vim -f`, []string{"vim", "-f"}, 0}, {`'my editor' -w`, "ignored", []string{"my editor", "-w"}, 0}, {`vim '`, "ignored", nil, 2}, {"", `vim '`, nil, 2}, {"   ", "ignored", nil, 2}} {
		var out, stderr bytes.Buffer
		d := tuiDeps(t, &out, &stderr)
		d.Getenv = func(key string) string {
			if key == "VISUAL" {
				return test.visual
			}
			if key == "EDITOR" {
				return test.editor
			}
			return ""
		}
		called := false
		d.RunTUI = func(_ context.Context, deps tui.Deps, _ tui.Options) error {
			called = true
			if !reflect.DeepEqual(deps.Env.Editor, test.want) {
				t.Fatal(deps.Env.Editor, test.want)
			}
			return nil
		}
		if code := Run(context.Background(), []string{"tui"}, d); code != test.code || called != (test.code == 0) {
			t.Fatal(test, code, stderr.String())
		}
		if test.code != 0 && !strings.Contains(stderr.String(), "valid editor command") {
			t.Fatal(stderr.String())
		}
	}
}
func TestTUI_ThemeWarningAndNoColor(t *testing.T) {
	for _, mode := range []string{"flag", "env"} {
		var out, stderr bytes.Buffer
		d := tuiDeps(t, &out, &stderr)
		d.Getenv = func(k string) string {
			if k == "TODOS_THEME" {
				return "invalid"
			}
			return ""
		}
		d.LookupEnv = func(k string) (string, bool) { return "", mode == "env" && k == "NO_COLOR" }
		d.RunTUI = func(_ context.Context, deps tui.Deps, _ tui.Options) error {
			if !deps.Env.NoColor || deps.Env.Theme != "invalid" {
				t.Fatal(deps.Env)
			}
			return nil
		}
		args := []string{"tui"}
		if mode == "flag" {
			args = append(args, "--no-color")
		}
		if code := Run(context.Background(), args, d); code != 0 || !strings.Contains(stderr.String(), "invalid TODOS_THEME") {
			t.Fatal(code, stderr.String())
		}
	}
}
func TestTUI_ConfigWarningOnce(t *testing.T) {
	var out, stderr bytes.Buffer
	d := tuiDeps(t, &out, &stderr)
	f := servicetest.New(servicetest.DefaultFixture())
	d.Wire = func(context.Context, func(context.Context) (domain.RepoRef, error), string) (Wiring, error) {
		return Wiring{Service: f, ConfigWarning: func() *domain.Error {
			return &domain.Error{Kind: domain.ErrConfig, Message: "unreadable config\x1b[2J"}
		}}, nil
	}
	if code := Run(context.Background(), []string{"tui"}, d); code != 0 || strings.Count(stderr.String(), "warning:") != 1 || strings.Contains(stderr.String(), "\x1b") {
		t.Fatal(code, stderr.String())
	}
}
func TestTUI_RunErrorAndSignalExit(t *testing.T) {
	for _, signal := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		var out, stderr bytes.Buffer
		d := tuiDeps(t, &out, &stderr)
		d.Interrupted = func() bool { return signal }
		d.RunTUI = func(context.Context, tui.Deps, tui.Options) error { cancel(); return errors.New("program killed") }
		code := Run(ctx, []string{"tui"}, d)
		want := 1
		if signal {
			want = 130
		}
		if code != want || !strings.Contains(stderr.String(), "operation interrupted") {
			t.Fatal(code, stderr.String())
		}
	}
}
func TestTUI_VersionAndHelpStayOffline(t *testing.T) {
	for _, args := range [][]string{{"tui", "--help"}, {"help", "tui"}, {"tui", "--version", "--json"}} {
		var out, stderr bytes.Buffer
		d := tuiDeps(t, &out, &stderr)
		d.ResolveRepo = func(context.Context, string) (domain.RepoRef, error) {
			t.Fatal("offline command resolved repo")
			return domain.RepoRef{}, nil
		}
		d.RunTUI = func(context.Context, tui.Deps, tui.Options) error {
			t.Fatal("offline command launched TUI")
			return nil
		}
		if code := Run(context.Background(), args, d); code != 0 || out.Len() == 0 || stderr.Len() != 0 {
			t.Fatal(args, code, out.String(), stderr.String())
		}
		if strings.Contains(out.String(), "not built") {
			t.Fatal(out.String())
		}
	}
}
