package cache

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var testNow = time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)

func newStore(t *testing.T) *Store {
	t.Helper()
	return New(filepath.Join(t.TempDir(), "cache"), Options{Now: func() time.Time { return testNow }})
}

func TestStore_RoundTripPrivateAndOwned(t *testing.T) {
	s := newStore(t)
	data := []byte("private bytes, not necessarily JSON")
	s.Put("credential/structure/host/owner/repo", "labels", data, testNow.Add(-time.Minute))
	data[0] = 'X'
	b, at, ok := s.Get("credential/structure/host/owner/repo", "labels")
	if !ok || string(b) != "private bytes, not necessarily JSON" || !at.Equal(testNow.Add(-time.Minute)) {
		t.Fatal("round trip lost read time or ownership")
	}
	b[0] = 'Z'
	b, _, _ = s.Get("credential/structure/host/owner/repo", "labels")
	if b[0] != 'p' {
		t.Fatal("read aliases stored value")
	}
	err := filepath.WalkDir(s.root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		want := os.FileMode(0600)
		if d.IsDir() {
			want = 0700
		}
		if info.Mode().Perm() != want {
			t.Errorf("permissions on %s: %o", path, info.Mode().Perm())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestStore_InvalidEntriesAreMisses(t *testing.T) {
	for _, mode := range []string{"corrupt", "wrong-version", "wrong-key", "future", "zero", "oversized"} {
		t.Run(mode, func(t *testing.T) {
			s := newStore(t)
			s.Put("cred/structure", "key", []byte("ok"), testNow)
			path := filepath.Join(s.root, "cred/structure", keyFile("key"))
			b, _ := os.ReadFile(path)
			var e entry
			json.Unmarshal(b, &e)
			switch mode {
			case "corrupt":
				b = []byte("{")
			case "wrong-version":
				e.Version = 9
			case "wrong-key":
				e.Key = "other"
			case "future":
				e.ReadAt = testNow.Add(time.Second)
			case "zero":
				e.ReadAt = time.Time{}
			case "oversized":
				b = bytes.Repeat([]byte("x"), MaxEntryBytes+1)
			}
			if mode != "corrupt" && mode != "oversized" {
				b, _ = json.Marshal(e)
			}
			if err := os.WriteFile(path, b, 0600); err != nil {
				t.Fatal(err)
			}
			if _, _, ok := s.Get("cred/structure", "key"); ok {
				t.Fatal("invalid entry used")
			}
		})
	}
}

func TestStore_TooLargePutDoesNotReplaceValue(t *testing.T) {
	s := newStore(t)
	s.Put("cred", "key", []byte("keep"), testNow)
	s.Put("cred", "key", make([]byte, MaxEntryBytes+1), testNow)
	b, _, ok := s.Get("cred", "key")
	if !ok || string(b) != "keep" {
		t.Fatal("oversized write replaced entry")
	}
}

func TestStore_DirectoriesTightenedAndSymlinksRefused(t *testing.T) {
	s := newStore(t)
	s.Put("cred/nested", "key", []byte("ok"), testNow)
	for _, p := range []string{s.root, filepath.Join(s.root, "cred"), filepath.Join(s.root, "cred/nested")} {
		if err := os.Chmod(p, 0777); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(s.root, "cred/nested", keyFile("key"))
	os.Chmod(path, 0666)
	if _, _, ok := s.Get("cred/nested", "key"); !ok {
		t.Fatal("could not tighten own directories")
	}
	for _, p := range []string{s.root, filepath.Join(s.root, "cred"), filepath.Join(s.root, "cred/nested")} {
		info, _ := os.Stat(p)
		if info.Mode().Perm() != 0700 {
			t.Fatal("wide directory")
		}
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("wide file")
	}
	other := t.TempDir()
	os.Symlink(other, filepath.Join(s.root, "link"))
	s.Put("link", "no", []byte("x"), testNow)
	files, _ := os.ReadDir(other)
	if len(files) != 0 {
		t.Fatal("followed symlink space")
	}
	os.Remove(path)
	os.Symlink(filepath.Join(other, "secret"), path)
	if _, _, ok := s.Get("cred/nested", "key"); ok {
		t.Fatal("followed symlink entry")
	}
}

func TestStore_UnusableAndTraversalAreHarmless(t *testing.T) {
	root := filepath.Join(t.TempDir(), "file")
	os.WriteFile(root, []byte("block"), 0600)
	for _, p := range []string{root, filepath.Join(root, "child"), "relative"} {
		s := New(p, Options{})
		s.Put("cred", "key", []byte("x"), time.Now())
		s.Bump("cred")
		s.Drop("cred", "")
		if _, _, ok := s.Get("cred", "key"); ok {
			t.Fatal("unusable store hit")
		}
		if s.Epoch("cred") != "none" {
			t.Fatal("unusable epoch")
		}
	}
	s := newStore(t)
	for _, space := range []string{"../escape", "/absolute", "a/../b", "a//b", "a\\b", ""} {
		s.Put(space, "key", []byte("x"), testNow)
		s.Bump(space)
		if _, _, ok := s.Get(space, "key"); ok {
			t.Fatal("unsafe space used")
		}
	}
	if _, err := os.Stat(s.root); !os.IsNotExist(err) {
		t.Fatal("invalid spaces created a root")
	}
}

func TestStore_DropPrefix(t *testing.T) {
	s := newStore(t)
	for _, key := range []string{"board/1", "board/2", "labels"} {
		s.Put("cred", key, []byte("v"), testNow)
	}
	s.Bump("cred")
	epoch := s.Epoch("cred")
	s.Drop("cred", "board/")
	if _, _, ok := s.Get("cred", "board/1"); ok {
		t.Fatal("prefix retained")
	}
	if _, _, ok := s.Get("cred", "labels"); !ok {
		t.Fatal("other prefix dropped")
	}
	s.Drop("cred", "")
	if _, _, ok := s.Get("cred", "labels"); ok {
		t.Fatal("full drop retained entry")
	}
	if s.Epoch("cred") != epoch {
		t.Fatal("drop removed epoch")
	}
}

func TestStore_EpochAcrossStores(t *testing.T) {
	s := newStore(t)
	other := New(s.root, Options{Now: s.now})
	if s.Epoch("cred/content") != "none" || other.Epoch("cred/content") != "none" {
		t.Fatal("initial epoch")
	}
	s.Bump("cred/content")
	first := s.Epoch("cred/content")
	if len(first) != 64 || other.Epoch("cred/content") != first {
		t.Fatal("epoch not shared")
	}
	other.Bump("cred/content")
	second := s.Epoch("cred/content")
	if second == first || len(second) != 64 {
		t.Fatal("epoch did not change")
	}
	if s.Epoch("other/content") != "none" {
		t.Fatal("credential crossed")
	}
}

func TestStore_ConcurrentWritersAreAtomic(t *testing.T) {
	s := newStore(t)
	other := New(s.root, Options{Now: s.now})
	values := [][]byte{bytes.Repeat([]byte("a"), 8192), bytes.Repeat([]byte("b"), 8192)}
	var wg sync.WaitGroup
	for j, store := range []*Store{s, other} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 40 {
				store.Put("cred/content", "key", values[j], testNow)
				store.Bump("cred/content")
			}
		}()
	}
	for range 80 {
		if b, _, ok := s.Get("cred/content", "key"); ok && !bytes.Equal(b, values[0]) && !bytes.Equal(b, values[1]) {
			t.Error("torn value")
		}
	}
	wg.Wait()
	if s.Epoch("cred/content") != other.Epoch("cred/content") {
		t.Fatal("stores disagree")
	}
}

func TestStore_PrunesOnceWithoutRemovingEpochs(t *testing.T) {
	s := newStore(t)
	old := filepath.Join(s.root, "oldcred")
	active := filepath.Join(s.root, "active/structure")
	os.MkdirAll(old, 0700)
	os.MkdirAll(active, 0700)
	stale := filepath.Join(active, "stale.json")
	os.WriteFile(stale, []byte("x"), 0600)
	os.Chtimes(stale, testNow.Add(-25*time.Hour), testNow.Add(-25*time.Hour))
	os.WriteFile(filepath.Join(active, "epoch"), []byte(strings.Repeat("a", 64)), 0600)
	os.Chtimes(filepath.Join(active, "epoch"), testNow.Add(-25*time.Hour), testNow.Add(-25*time.Hour))
	os.Chtimes(old, testNow.Add(-31*24*time.Hour), testNow.Add(-31*24*time.Hour))
	s.Put("active/structure", "first", []byte("v"), testNow)
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("old credential retained")
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("old entry retained")
	}
	if s.Epoch("active/structure") != strings.Repeat("a", 64) {
		t.Fatal("epoch removed")
	}
	os.WriteFile(stale, []byte("x"), 0600)
	os.Chtimes(stale, testNow.Add(-25*time.Hour), testNow.Add(-25*time.Hour))
	s.Put("active/structure", "second", []byte("v"), testNow)
	if _, err := os.Stat(stale); err != nil {
		t.Fatal("pruned more than once")
	}
}

func TestCheck_NeverCreatesOrRepairs(t *testing.T) {
	s := newStore(t)
	if c := Check(s.root); !c.OK {
		t.Fatal(c)
	}
	if _, err := os.Stat(s.root); !os.IsNotExist(err) {
		t.Fatal("doctor created cache")
	}
	os.Mkdir(s.root, 0755)
	c := Check(s.root)
	info, _ := os.Stat(s.root)
	if c.OK || info.Mode().Perm() != 0755 {
		t.Fatal("doctor repaired permissions")
	}
	if c := Check("relative"); c.OK {
		t.Fatal("relative root accepted")
	}
}

func TestDefaultRoot(t *testing.T) {
	if p := DefaultRoot(func(string) string { return "/xdg" }, func() (string, error) { t.Fatal("home called"); return "", nil }); p != "/xdg/todos/v1" {
		t.Fatal(p)
	}
	if p := DefaultRoot(func(string) string { return "relative" }, func() (string, error) { return "/home/test", nil }); p != "/home/test/.cache/todos/v1" {
		t.Fatal(p)
	}
}

func TestStore_UnwritableParentIsAnEmptyCache(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	parent := filepath.Join(t.TempDir(), "blocked")
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	s := New(filepath.Join(parent, "cache"), Options{})
	if err := os.Chmod(parent, 0000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(parent, 0700)
	s.Put("cred", "key", []byte("v"), time.Now())
	if s.Bump("cred") {
		t.Fatal("unwritable epoch bump reported success")
	}
	s.Drop("cred", "")
	if _, _, ok := s.Get("cred", "key"); ok {
		t.Fatal("unwritable cache produced a hit")
	}
	if s.Epoch("cred") != "none" || Check(s.root).OK {
		t.Fatal("unwritable directory claimed usable")
	}
}

func TestStore_SweepMarkerSharedAcrossProcesses(t *testing.T) {
	s := newStore(t)
	s.Put("cred/structure", "first", []byte("v"), testNow)
	stale := filepath.Join(s.root, "cred/structure/stale.json")
	if err := os.WriteFile(stale, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	old := testNow.Add(-25 * time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	// Forget process-local state, as if a new CLI process opened the same root.
	pruneRoots.Delete(s.root)
	other := New(s.root, Options{Now: func() time.Time { return testNow.Add(time.Hour) }})
	other.Put("cred/structure", "second", []byte("v"), testNow)
	if _, err := os.Stat(stale); err != nil {
		t.Fatal("second process swept before a day elapsed", err)
	}
	pruneRoots.Delete(s.root)
	nextDay := New(s.root, Options{Now: func() time.Time { return testNow.Add(24 * time.Hour) }})
	nextDay.Put("cred/structure", "third", []byte("v"), testNow.Add(24*time.Hour))
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("next day's sweep did not run", err)
	}
}

func TestStore_BumpReportsWriteFailure(t *testing.T) {
	s := newStore(t)
	if !s.Bump("cred/content") {
		t.Fatal("successful bump reported failure")
	}
	before := s.Epoch("cred/content")
	// A blocked destination makes rename fail even when tests run as root.
	path := filepath.Join(s.root, "cred/content/epoch")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if s.Bump("cred/content") {
		t.Fatal("failed epoch write reported success")
	}
	if before == "none" {
		t.Fatal("initial bump did not persist")
	}
	if s.Bump("../escape") {
		t.Fatal("invalid space bump reported success")
	}
}
