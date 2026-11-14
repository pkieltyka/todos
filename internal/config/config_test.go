package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/pkieltyka/todos/internal/domain"
)

var testRepo = domain.RepoRef{Host: "github.com", Owner: "Test", Name: "Repo"}
var testProject = domain.ProjectRef{Host: "github.com", Owner: "Test", Number: 3}

func newPaths(t *testing.T) Paths {
	t.Helper()
	return Paths{ConfigFile: filepath.Join(t.TempDir(), "nested", "config.yml")}
}
func TestConfig_Missing(t *testing.T) {
	p := newPaths(t)
	s := New(p)
	r, err := s.RememberedBoard(context.Background(), testRepo)
	if r != nil || err != nil || s.Warning() != nil {
		t.Fatal(r, err)
	}
	check, err := s.Check(context.Background())
	if err != nil || !check.OK {
		t.Fatal(check, err)
	}
	if _, err = os.Stat(filepath.Dir(p.ConfigFile)); !os.IsNotExist(err) {
		t.Fatal("read created directories")
	}
}
func TestConfig_ReadParseFailureEmptyButNeverOverwritten(t *testing.T) {
	for _, text := range []string{"version: [", "version: 99\n", "version: 1\nversion: 1\n", "version: 1\n---\nversion: 1\n", "version: 1\nboards:\n  x: {owner: test, number: 0}\n"} {
		t.Run(text, func(t *testing.T) {
			p := newPaths(t)
			os.MkdirAll(filepath.Dir(p.ConfigFile), 0700)
			os.WriteFile(p.ConfigFile, []byte(text), 0600)
			s := New(p)
			r, err := s.RememberedBoard(context.Background(), testRepo)
			if r != nil || err != nil || s.Warning() == nil {
				t.Fatal(r, err)
			}
			if err = s.RememberBoard(context.Background(), testRepo, testProject); err == nil {
				t.Fatal("overwrote invalid config")
			}
			data, _ := os.ReadFile(p.ConfigFile)
			if string(data) != text {
				t.Fatal("changed invalid bytes")
			}
			c, err := s.Check(context.Background())
			if err == nil || c.OK {
				t.Fatal("doctor missed config failure")
			}
		})
	}
	p := newPaths(t)
	os.MkdirAll(p.ConfigFile, 0700)
	s := New(p)
	if s.Warning() == nil {
		t.Fatal("unreadable file not diagnosed")
	}
	if err := s.RememberBoard(context.Background(), testRepo, testProject); err == nil {
		t.Fatal("replaced directory")
	}
}
func TestConfig_UnknownTopLevelInlineMap(t *testing.T) {
	p := newPaths(t)
	os.MkdirAll(filepath.Dir(p.ConfigFile), 0700)
	os.WriteFile(p.ConfigFile, []byte("version: 1\ncustom:\n  nested: [one, two]\n"), 0600)
	s := New(p)
	before, _ := s.read()
	if err := s.RememberBoard(context.Background(), testRepo, testProject); err != nil {
		t.Fatal(err)
	}
	after, err := s.read()
	if err != nil || !reflect.DeepEqual(before.Extra, after.Extra) {
		t.Fatal(after, err)
	}
	r, err := New(p).RememberedBoard(context.Background(), testRepo)
	if err != nil || *r != testProject {
		t.Fatal(r, err)
	}
	info, _ := os.Stat(p.ConfigFile)
	if info.Mode().Perm() != 0600 {
		t.Fatal(info.Mode())
	}
}
func TestConfig_AtomicFailurePreservesOriginal(t *testing.T) {
	p := newPaths(t)
	s := New(p)
	if err := s.RememberBoard(context.Background(), testRepo, testProject); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(p.ConfigFile)
	s.write = func(string, []byte) error { return errors.New("disk full") }
	if err := s.RememberBoard(context.Background(), testRepo, domain.ProjectRef{Host: "github.com", Owner: "Other", Number: 5}); err == nil {
		t.Fatal("ignored write failure")
	}
	after, _ := os.ReadFile(p.ConfigFile)
	if string(before) != string(after) {
		t.Fatal("changed original")
	}
	// A real rename failure also removes its temporary file.
	target := filepath.Join(filepath.Dir(p.ConfigFile), "directory")
	os.Mkdir(target, 0700)
	if err := atomicWrite(target, []byte("x")); err == nil {
		t.Fatal("expected rename failure")
	}
	left, _ := filepath.Glob(filepath.Join(filepath.Dir(target), ".todos-*"))
	if len(left) > 0 {
		t.Fatal(left)
	}
}
func TestConfig_LastSaveWins(t *testing.T) {
	p := newPaths(t)
	a, b := New(p), New(p)
	if err := a.RememberBoard(context.Background(), testRepo, testProject); err != nil {
		t.Fatal(err)
	}
	next := testProject
	next.Number = 9
	if err := b.RememberBoard(context.Background(), testRepo, next); err != nil {
		t.Fatal(err)
	}
	r, err := New(p).RememberedBoard(context.Background(), testRepo)
	if err != nil || *r != next {
		t.Fatal(r, err)
	}
}
func TestPaths_XDGMacAndFallback(t *testing.T) {
	for _, xdg := range []string{"", "relative", "/xdg"} {
		t.Run(xdg, func(t *testing.T) {
			p, err := DefaultPaths(func(string) string { return xdg }, func() (string, error) { return "/home/test", nil })
			if err != nil {
				t.Fatal(err)
			}
			prefix := "/home/test/.config"
			if xdg == "/xdg" {
				prefix = "/xdg"
			}
			if p.ConfigFile != filepath.Join(prefix, "todos/config.yml") {
				t.Fatal(p)
			}
		})
	}
	_, err := DefaultPaths(func(string) string { return "" }, func() (string, error) { return "", errors.New("no home") })
	if err == nil {
		t.Fatal("missing home")
	}
}
