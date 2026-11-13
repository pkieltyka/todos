package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pkieltyka/todos/internal/domain"
)

func draftFixture(t *testing.T) (*DraftStore, domain.Draft) {
	t.Helper()
	paths := Paths{DraftDir: filepath.Join(t.TempDir(), "state", "todos", "drafts")}
	d := domain.Draft{Version: 1, RepoKey: "github.com/test/repo", Text: "- first\r\n  body\r\n\r\n- second", UpdatedAt: time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC), Revision: 3, Uncertain: []domain.DraftSpan{}}
	return NewDraftStore(paths), d
}
func span(text string, start, end int) domain.DraftSpan {
	sum := sha256.Sum256([]byte(text[start:end]))
	return domain.DraftSpan{StartByte: start, EndByte: end, SourceSHA256: hex.EncodeToString(sum[:])}
}
func draftKind(t *testing.T, err error) {
	t.Helper()
	var de *domain.Error
	if !errors.As(err, &de) || de.Kind != domain.ErrConfig {
		t.Fatalf("want config, got %v", err)
	}
}
func TestDraft_PrivateAtomicRoundTrip(t *testing.T) {
	s, d := draftFixture(t)
	if _, found, e := s.Load(testRepo); e != nil || found {
		t.Fatal(found, e)
	}
	if _, e := os.Stat(s.paths.DraftDir); !os.IsNotExist(e) {
		t.Fatal("read created directories")
	}
	if e := s.Save(d); e != nil {
		t.Fatal(e)
	}
	got, found, e := s.Load(testRepo)
	if e != nil || !found || !reflect.DeepEqual(d, got) {
		t.Fatal(got, d, e)
	}
	path := draftPath(s.paths, d.RepoKey)
	info, e := os.Stat(path)
	if e != nil || info.Mode().Perm() != 0600 {
		t.Fatal(info, e)
	}
	info, e = os.Stat(s.paths.DraftDir)
	if e != nil || info.Mode().Perm() != 0700 {
		t.Fatal(info, e)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `"uncertain":[]`) || !strings.Contains(string(data), `"in_flight":null`) {
		t.Fatal(string(data))
	}
	files, _ := os.ReadDir(s.paths.DraftDir)
	if len(files) != 1 || len(files[0].Name()) != 64+len(".json") {
		t.Fatal(files)
	}
	if e := s.Delete(testRepo); e != nil {
		t.Fatal(e)
	}
	if e := s.Delete(testRepo); e != nil {
		t.Fatal(e)
	}
	_, found, e = s.Load(testRepo)
	if e != nil || found {
		t.Fatal(found, e)
	}
}
func TestDraft_UnparseableNeverOverwrite(t *testing.T) {
	for _, bad := range []string{"{", "null", `{"version":99}`, `{"version":1,"repo_key":"elsewhere/test/repo"}`, `{} {}`, `{"version":1,"unknown":true}`, string([]byte{255})} {
		t.Run(bad, func(t *testing.T) {
			s, d := draftFixture(t)
			path := draftPath(s.paths, d.RepoKey)
			if e := atomicWrite(path, []byte(bad)); e != nil {
				t.Fatal(e)
			}
			_, _, e := s.Load(testRepo)
			draftKind(t, e)
			draftKind(t, s.Save(d))
			draftKind(t, s.Delete(testRepo))
			after, _ := os.ReadFile(path)
			if string(after) != bad {
				t.Fatal("changed unreadable file")
			}
		})
	}
}
func TestDraft_InFlightReloadUncertain(t *testing.T) {
	s, d := draftFixture(t)
	d.InFlight = new(domain.DraftSpan)
	*d.InFlight = span(d.Text, 0, 20)
	if e := s.Save(d); e != nil {
		t.Fatal(e)
	}
	path := draftPath(s.paths, d.RepoKey)
	before, _ := os.ReadFile(path)
	got, found, e := s.Load(testRepo)
	if e != nil || !found || got.InFlight != nil || len(got.Uncertain) != 1 || got.Uncertain[0] != *d.InFlight {
		t.Fatal(got, e)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("load rewrote draft")
	}
	d.Uncertain = []domain.DraftSpan{*d.InFlight}
	if e := s.Save(d); e != nil {
		t.Fatal(e)
	}
	got, _, e = s.Load(testRepo)
	if e != nil || len(got.Uncertain) != 1 {
		t.Fatal(got, e)
	}
}
func TestDraft_RemainingOffsets(t *testing.T) {
	s, d := draftFixture(t)
	d.Text = "界界\n- second\r\n"
	d.Uncertain = []domain.DraftSpan{span(d.Text, len("界界\n"), len(d.Text))}
	if e := s.Save(d); e != nil {
		t.Fatal(e)
	}
	got, _, e := s.Load(testRepo)
	if e != nil || got.Text != d.Text || !reflect.DeepEqual(got.Uncertain, d.Uncertain) {
		t.Fatal(got, e)
	}
	got.Uncertain[0].StartByte = 0
	again, _, _ := s.Load(testRepo)
	if !reflect.DeepEqual(again.Uncertain, d.Uncertain) {
		t.Fatal("returned spans alias")
	}
}
func TestDraft_InvalidMarkersPreserveOriginal(t *testing.T) {
	for _, kind := range []string{"negative", "past-end", "zero", "utf8-boundary", "wrong-hash", "overlap"} {
		s, d := draftFixture(t)
		if e := s.Save(d); e != nil {
			t.Fatal(e)
		}
		original, _ := os.ReadFile(draftPath(s.paths, d.RepoKey))
		next := d
		next.Text = "界界 body"
		marker := span(next.Text, 0, 3)
		switch kind {
		case "negative":
			marker.StartByte = -1
		case "past-end":
			marker.EndByte = 99
		case "zero":
			marker.EndByte = marker.StartByte
		case "utf8-boundary":
			marker = span(next.Text, 1, 3)
		case "wrong-hash":
			marker.SourceSHA256 = strings.Repeat("0", 64)
		case "overlap":
			next.Uncertain = []domain.DraftSpan{marker}
		}
		next.Uncertain = append(next.Uncertain, marker)
		draftKind(t, s.Save(next))
		after, _ := os.ReadFile(draftPath(s.paths, d.RepoKey))
		if string(original) != string(after) {
			t.Fatal(kind, "changed original")
		}
	}
}
func TestDraft_ConcurrentProcessesLastSaveWins(t *testing.T) {
	first, d := draftFixture(t)
	second := NewDraftStore(first.paths)
	if e := first.Save(d); e != nil {
		t.Fatal(e)
	}
	d.Text = "second process"
	d.Revision = 1
	if e := second.Save(d); e != nil {
		t.Fatal(e)
	}
	got, _, e := first.Load(testRepo)
	if e != nil || got.Text != d.Text || got.Revision != 1 {
		t.Fatal(got, e)
	}
	d.Text = "first process again"
	d.Revision = 0
	if e := first.Save(d); e != nil {
		t.Fatal(e)
	}
	got, _, e = second.Load(testRepo)
	if e != nil || got.Text != d.Text {
		t.Fatal(got, e)
	}
}
func TestDraft_SaveFailureKeepsBytesAndNoTemp(t *testing.T) {
	s, d := draftFixture(t)
	if e := s.Save(d); e != nil {
		t.Fatal(e)
	}
	before, _ := os.ReadFile(draftPath(s.paths, d.RepoKey))
	s.write = func(string, []byte) error { return errors.New("disk full") }
	d.Text = "new"
	draftKind(t, s.Save(d))
	after, _ := os.ReadFile(draftPath(s.paths, d.RepoKey))
	if string(before) != string(after) {
		t.Fatal("destroyed original")
	}
	files, _ := os.ReadDir(s.paths.DraftDir)
	if len(files) != 1 {
		t.Fatal(files)
	}
}
func TestDraft_RepositoryIsolationAndKeyValidation(t *testing.T) {
	s, d := draftFixture(t)
	d.RepoKey = "GitHub.Com/TEST/REPO"
	if e := s.Save(d); e != nil {
		t.Fatal(e)
	}
	got, found, e := s.Load(domain.RepoRef{Owner: "test", Name: "repo"})
	if e != nil || !found || got.RepoKey != "github.com/test/repo" {
		t.Fatal(got, e)
	}
	_, found, e = s.Load(domain.RepoRef{Host: "github.com", Owner: "other", Name: "repo"})
	if e != nil || found {
		t.Fatal(found, e)
	}
	for _, key := range []string{"", "test/repo", "github.com/../repo", "github.com/test/repo/extra", "github.com/test/ repo", "github.com/test/rep\\o"} {
		d.RepoKey = key
		draftKind(t, s.Save(d))
	}
	d.Version = 2
	d.RepoKey = "github.com/test/repo"
	draftKind(t, s.Save(d))
}
func TestDraft_MalformedMarkersOnDiskPreserved(t *testing.T) {
	s, d := draftFixture(t)
	f := draftFile{Version: 1, RepoKey: d.RepoKey, Text: d.Text, Uncertain: []draftSpan{{Start: 0, End: 1, Hash: "bad"}}}
	data, _ := json.Marshal(f)
	if e := atomicWrite(draftPath(s.paths, d.RepoKey), data); e != nil {
		t.Fatal(e)
	}
	_, _, e := s.Load(testRepo)
	draftKind(t, e)
	draftKind(t, s.Save(d))
	after, _ := os.ReadFile(draftPath(s.paths, d.RepoKey))
	if string(after) != string(data) {
		t.Fatal("changed invalid markers")
	}
}
func TestDraft_UpdatedAtFilledWhenZero(t *testing.T) {
	s, d := draftFixture(t)
	d.UpdatedAt = time.Time{}
	if e := s.Save(d); e != nil {
		t.Fatal(e)
	}
	got, _, e := s.Load(testRepo)
	if e != nil || got.UpdatedAt.IsZero() || got.UpdatedAt.Location() != time.UTC {
		t.Fatal(got, e)
	}
}
func TestDraft_UnreadablePathPreserved(t *testing.T) {
	s, d := draftFixture(t)
	path := draftPath(s.paths, d.RepoKey)
	if e := os.MkdirAll(path, 0700); e != nil {
		t.Fatal(e)
	}
	_, _, e := s.Load(testRepo)
	draftKind(t, e)
	draftKind(t, s.Save(d))
	draftKind(t, s.Delete(testRepo))
	if info, e := os.Stat(path); e != nil || !info.IsDir() {
		t.Fatal(info, e)
	}
}

func TestDraft_InFlightCannotOverlapDifferentUncertainSpan(t *testing.T) {
	s, d := draftFixture(t)
	d.Uncertain = []domain.DraftSpan{span(d.Text, 0, 5)}
	flight := span(d.Text, 0, 10)
	d.InFlight = &flight
	draftKind(t, s.Save(d))
	if _, e := os.Stat(draftPath(s.paths, d.RepoKey)); !os.IsNotExist(e) {
		t.Fatal("invalid draft written")
	}
}
