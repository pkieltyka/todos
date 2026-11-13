package main

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestImportRules(t *testing.T) {
	const prefix = "github.com/pkieltyka/todos/"
	owner := func(path string) string {
		p := strings.TrimPrefix(filepath.ToSlash(filepath.Dir(path)), "internal/")
		if p == "." {
			return ""
		}
		if strings.HasPrefix(p, "service/servicetest") {
			return "service/servicetest"
		}
		return strings.Split(p, "/")[0]
	}
	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if strings.HasPrefix(entry.Name(), ".") && path != "." || entry.Name() == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		from := owner(path)
		for _, imp := range file.Imports {
			value, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				return err
			}
			if !strings.HasPrefix(value, prefix+"internal/") {
				continue
			}
			to := strings.TrimPrefix(value, prefix+"internal/")
			if !importAllowed(from, to, strings.HasSuffix(path, "_test.go")) {
				t.Errorf("%s: %s cannot import %s", path, from, to)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestMain_EmbedAndRootBinary(t *testing.T) {
	file, err := os.ReadFile("skills/todos/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(file) != string(skill) {
		t.Fatal("embedded skill differs from source")
	}
	if version != "dev" {
		t.Fatalf("unexpected development version: %q", version)
	}
}

func importAllowed(from, to string, isTest bool) bool {
	// servicetest is a separate architecture component, never a service subpackage shortcut.
	if from == "service" && (to == "service/servicetest" || strings.HasPrefix(to, "service/servicetest/")) {
		return false
	}
	if to == from || from != "" && strings.HasPrefix(to, from+"/") {
		return true
	}
	allowed := map[string]map[string]bool{
		"": {"cli": true}, "domain": {}, "quickadd": {"domain": true}, "config": {"domain": true}, "markdown": {}, "github": {"domain": true}, "service": {"domain": true, "quickadd": true}, "service/servicetest": {"domain": true, "service": true, "quickadd": true}, "tui": {"domain": true, "service": true, "markdown": true, "quickadd": true}, "cli": {"domain": true, "quickadd": true, "config": true, "markdown": true, "github": true, "service": true, "service/servicetest": true, "tui": true}, "tools": {},
	}

	target := strings.Split(to, "/")[0]
	if to == "service/servicetest" || strings.HasPrefix(to, "service/servicetest/") {
		target = "service/servicetest"
	}
	if isTest && (from == "cli" || from == "tui") && target == "service/servicetest" {
		return true
	}
	return allowed[from][target]
}
func TestImportRules_ServiceCannotImportServicetest(t *testing.T) {
	for _, test := range []struct {
		from, to     string
		isTest, want bool
	}{
		{"service", "service/servicetest", false, false},
		{"service", "service/servicetest", true, false},
		{"service", "service/servicetest/nested", false, false},
		{"service", "service/metadata", false, true},
		{"service/servicetest", "service", false, true},
		{"service/servicetest", "quickadd", false, true},
		{"tui", "quickadd", false, true},
		{"tui", "service/servicetest", false, false},
		{"tui", "service/servicetest", true, true},
	} {
		if got := importAllowed(test.from, test.to, test.isTest); got != test.want {
			t.Errorf("%s -> %s (test=%v): %v", test.from, test.to, test.isTest, got)
		}
	}
}
