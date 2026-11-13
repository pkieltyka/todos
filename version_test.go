package main

import (
	"archive/zip"
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
)

func TestVersion_ModuleBuildInfoAndOverride(t *testing.T) {
	for _, tc := range []struct {
		name, override, module, want string
		ok                           bool
	}{{"installed", "dev", "v1.2.3", "v1.2.3", true}, {"pseudo", "dev", "v0.0.0-20261113230000-0123456789ab", "v0.0.0-20261113230000-0123456789ab", true}, {"local", "dev", "(devel)", "dev", true}, {"missing-module", "dev", "", "dev", true}, {"no-build-info", "dev", "v1.2.3", "dev", false}, {"release-override", "v2.0.0", "v1.2.3", "v2.0.0", true}, {"empty-override", "", "v1.2.3", "v1.2.3", true}} {
		t.Run(tc.name, func(t *testing.T) {
			info := &debug.BuildInfo{Main: debug.Module{Version: tc.module}}
			if got := buildVersion(tc.override, info, tc.ok); got != tc.want {
				t.Fatalf("want %q, got %q", tc.want, got)
			}
		})
	}
	if got := buildVersion("dev", nil, true); got != "dev" {
		t.Fatal(got)
	}
}
func TestVersion_ExplicitOverrideSurvivesMissingBuildInfo(t *testing.T) {
	if got := buildVersion("release-test", nil, false); got != "release-test" {
		t.Fatal(got)
	}
	if got := runtimeVersion("release-test"); got != "release-test" {
		t.Fatal(got)
	}
}

// An isolated file proxy exercises Go's installed-module build metadata using
// the real entrypoint/version source. A tiny CLI sink avoids dependency/network
// access; the normal CLI tests cover JSON rendering separately.
func TestVersion_GoInstallBuildInfo(t *testing.T) {
	const module = "github.com/pkieltyka/todos"
	const installedVersion = "v0.0.0-test"
	dir := t.TempDir()
	proxy := filepath.Join(dir, "proxy", module, "@v")
	if err := os.MkdirAll(proxy, 0700); err != nil {
		t.Fatal(err)
	}
	mod := []byte("module " + module + "\n\ngo 1.27.3\n")
	if err := os.WriteFile(filepath.Join(proxy, "list"), []byte(installedVersion+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proxy, installedVersion+".mod"), mod, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proxy, installedVersion+".info"), []byte(`{"Version":"v0.0.0-test","Time":"2026-11-13T12:00:00Z"}`), 0600); err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{"go.mod": mod, "internal/cli/cli.go": []byte(`package cli
import ("fmt";"os")
func Main(_ []byte, version string){fmt.Fprintln(os.Stdout,version)}
`)}
	for _, name := range []string{"main.go", "version.go", "skills/todos/SKILL.md"} {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		files[name] = data
	}
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	for name, data := range files {
		entry, err := writer.Create(module + "@" + installedVersion + "/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proxy, installedVersion+".zip"), archive.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	controlled := map[string]string{"GOPROXY": "file://" + filepath.Join(dir, "proxy"), "GOSUMDB": "off", "GONOPROXY": "none", "GOPRIVATE": "", "GOMODCACHE": filepath.Join(dir, "modcache"), "GOBIN": filepath.Join(dir, "bin"), "GOENV": "off", "GOTOOLCHAIN": "local", "GOFLAGS": "-modcacherw", "GOWORK": "off"}
	env := []string{}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if _, ok := controlled[key]; !ok {
			env = append(env, entry)
		}
	}
	for key, value := range controlled {
		env = append(env, key+"="+value)
	}
	cmd := exec.Command("go", "install", module+"@"+installedVersion)
	cmd.Dir = dir
	cmd.Env = env
	if data, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("offline go install: %v\n%s", err, data)
	}
	data, err := exec.Command(filepath.Join(dir, "bin", "todos")).Output()
	if err != nil || strings.TrimSpace(string(data)) != installedVersion {
		t.Fatalf("installed version %q: %v", data, err)
	}
}
