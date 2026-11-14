package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/pkieltyka/todos/internal/tui"
)

func browserFixture(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	marker := filepath.Join(dir, "opened")
	script := filepath.Join(dir, "browser")
	body := "#!/bin/sh\nprintf '%s' \"$1\" > \"$TODOS_TEST_BROWSER_MARKER\"\nprintf 'unexpected stdout\\n'\nprintf 'unexpected stderr\\n' >&2\n"
	if e := os.WriteFile(script, []byte(body), 0700); e != nil {
		t.Fatal(e)
	}
	t.Setenv("TODOS_TEST_BROWSER_MARKER", marker)
	return script, marker
}
func TestTUI_BrowserLauncherOutputDiscarded(t *testing.T) {
	script, marker := browserFixture(t)
	t.Setenv("GH_BROWSER", script)
	var out, stderr bytes.Buffer
	d := tuiDeps(t, &out, &stderr)
	d.Open = nil
	d.RunTUI = func(_ context.Context, deps tui.Deps, _ tui.Options) error {
		return deps.Open("https://example.test/issue")
	}
	if code := Run(context.Background(), []string{"tui"}, d); code != 0 || out.Len() != 0 || stderr.Len() != 0 {
		t.Fatal(code, out.String(), stderr.String())
	}
	data, e := os.ReadFile(marker)
	if e != nil || string(data) != "https://example.test/issue" {
		t.Fatal(string(data), e)
	}
}
func TestBrowser_DefaultOSLauncherOutputDiscarded(t *testing.T) {
	if goruntime.GOOS != "linux" && goruntime.GOOS != "darwin" {
		t.Skip("supported platforms only")
	}
	script, marker := browserFixture(t)
	dir := filepath.Dir(script)
	for _, name := range []string{"xdg-open", "open", "gio", "x-www-browser", "wslview"} {
		data, _ := os.ReadFile(script)
		if e := os.WriteFile(filepath.Join(dir, name), data, 0700); e != nil {
			t.Fatal(e)
		}
	}
	t.Setenv("GH_BROWSER", "")
	t.Setenv("BROWSER", "")
	t.Setenv("GH_CONFIG_DIR", t.TempDir())
	t.Setenv("PATH", dir)
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	child := exec.Command(exe, "-test.run=^TestBrowser_QuietHelper$")
	child.Env = append(os.Environ(), "TODOS_BROWSER_TEST_CHILD=1")
	var stdout, stderr bytes.Buffer
	child.Stdout = &stdout
	child.Stderr = &stderr
	if e := child.Run(); e != nil || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatal(e, stdout.String(), stderr.String())
	}
	data, e := os.ReadFile(marker)
	if e != nil || !strings.Contains(string(data), "https://example.test/fallback") {
		t.Fatal(string(data), e)
	}
}
func TestBrowser_LaunchErrorReturned(t *testing.T) {
	t.Setenv("GH_BROWSER", filepath.Join(t.TempDir(), "missing"))
	if e := quietBrowser("https://example.test"); e == nil {
		t.Fatal("launcher error disappeared")
	}
}

func TestBrowser_QuietHelper(t *testing.T) {
	if os.Getenv("TODOS_BROWSER_TEST_CHILD") != "1" {
		return
	}
	if e := quietBrowser("https://example.test/fallback"); e != nil {
		os.Stderr.WriteString(e.Error())
		os.Exit(1)
	}
	os.Exit(0)
}
