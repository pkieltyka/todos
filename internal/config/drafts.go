package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/pkieltyka/todos/internal/domain"
)

// DraftStore persists user-authored source and send markers only. Independent
// processes use atomic last-save-wins writes; there is no locking protocol.
type DraftStore struct {
	paths Paths
	write func(string, []byte) error
}

func NewDraftStore(paths Paths) *DraftStore { return &DraftStore{paths: paths, write: atomicWrite} }

type draftSpan struct {
	Start int    `json:"start_byte"`
	End   int    `json:"end_byte"`
	Hash  string `json:"source_sha256"`
}
type draftFile struct {
	Version   int         `json:"version"`
	RepoKey   string      `json:"repo_key"`
	Text      string      `json:"text"`
	UpdatedAt time.Time   `json:"updated_at"`
	Uncertain []draftSpan `json:"uncertain"`
	InFlight  *draftSpan  `json:"in_flight"`
	Revision  uint64      `json:"revision"`
}

func draftError(message string, err error) *domain.Error {
	return &domain.Error{Kind: domain.ErrConfig, Message: message, Hint: "repair or move aside the todos draft file before saving again", Err: err}
}
func canonicalDraftKey(key string) (string, error) {
	parts := strings.Split(key, "/")
	if len(parts) != 3 {
		return "", draftError("invalid draft repository key", nil)
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || strings.ContainsAny(part, "\\") || strings.IndexFunc(part, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 || !utf8.ValidString(part) {
			return "", draftError("invalid draft repository key", nil)
		}
	}
	return strings.ToLower(key), nil
}
func draftRepoKey(repo domain.RepoRef) (string, error) {
	host := repo.Host
	if host == "" {
		host = "github.com"
	}
	return canonicalDraftKey(host + "/" + repo.Owner + "/" + repo.Name)
}
func draftPath(paths Paths, repoKey string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(repoKey)))
	return filepath.Join(paths.DraftDir, hex.EncodeToString(sum[:])+".json")
}
func validateDraft(d domain.Draft, key string) error {
	if d.Version != 1 {
		return draftError(fmt.Sprintf("unsupported draft version %d", d.Version), nil)
	}
	canonical, err := canonicalDraftKey(d.RepoKey)
	if err != nil {
		return err
	}
	if canonical != key {
		return draftError("draft belongs to a different repository", nil)
	}
	if !utf8.ValidString(d.Text) {
		return draftError("draft text must be valid UTF-8", nil)
	}
	check := func(span domain.DraftSpan) error {
		if span.StartByte < 0 || span.EndByte <= span.StartByte || span.EndByte > len(d.Text) {
			return draftError("invalid draft source span", nil)
		}
		source := d.Text[span.StartByte:span.EndByte]
		if !utf8.ValidString(source) {
			return draftError("draft source span splits a UTF-8 character", nil)
		}
		sum := sha256.Sum256([]byte(source))
		if span.SourceSHA256 != hex.EncodeToString(sum[:]) {
			return draftError("draft source span hash does not match its text", nil)
		}
		return nil
	}
	for j, span := range d.Uncertain {
		if err := check(span); err != nil {
			return err
		}
		for _, previous := range d.Uncertain[:j] {
			if span.StartByte < previous.EndByte && previous.StartByte < span.EndByte {
				return draftError("overlapping uncertain draft spans", nil)
			}
		}
	}
	if d.InFlight != nil {
		if err := check(*d.InFlight); err != nil {
			return err
		}
		for _, span := range d.Uncertain {
			if span.StartByte < d.InFlight.EndByte && d.InFlight.StartByte < span.EndByte && span != *d.InFlight {
				return draftError("in-flight draft span overlaps an uncertain span", nil)
			}
		}
	}
	return nil
}
func toSpan(s domain.DraftSpan) draftSpan {
	return draftSpan{Start: s.StartByte, End: s.EndByte, Hash: s.SourceSHA256}
}
func fromSpan(s draftSpan) domain.DraftSpan {
	return domain.DraftSpan{StartByte: s.Start, EndByte: s.End, SourceSHA256: s.Hash}
}
func encodeDraft(d domain.Draft) ([]byte, error) {
	f := draftFile{Version: d.Version, RepoKey: d.RepoKey, Text: d.Text, UpdatedAt: d.UpdatedAt.UTC(), Revision: d.Revision, Uncertain: []draftSpan{}}
	for _, s := range d.Uncertain {
		f.Uncertain = append(f.Uncertain, toSpan(s))
	}
	if d.InFlight != nil {
		s := toSpan(*d.InFlight)
		f.InFlight = &s
	}
	return json.Marshal(f)
}
func (s *DraftStore) read(key string) (domain.Draft, bool, error) {
	data, err := os.ReadFile(draftPath(s.paths, key))
	if os.IsNotExist(err) {
		return domain.Draft{}, false, nil
	}
	if err != nil {
		return domain.Draft{}, false, draftError("could not read todos draft", err)
	}
	if !utf8.Valid(data) {
		return domain.Draft{}, false, draftError("draft file must be valid UTF-8", nil)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var f draftFile
	if err := dec.Decode(&f); err != nil {
		return domain.Draft{}, false, draftError("could not parse todos draft", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return domain.Draft{}, false, draftError("draft must contain one JSON document", err)
	}
	d := domain.Draft{Version: f.Version, RepoKey: f.RepoKey, Text: f.Text, UpdatedAt: f.UpdatedAt.UTC(), Revision: f.Revision, Uncertain: []domain.DraftSpan{}}
	for _, span := range f.Uncertain {
		d.Uncertain = append(d.Uncertain, fromSpan(span))
	}
	if f.InFlight != nil {
		span := fromSpan(*f.InFlight)
		d.InFlight = &span
	}
	if err := validateDraft(d, key); err != nil {
		return domain.Draft{}, false, err
	}
	d.RepoKey = key
	return d, true, nil
}
func (s *DraftStore) Load(repo domain.RepoRef) (domain.Draft, bool, error) {
	key, err := draftRepoKey(repo)
	if err != nil {
		return domain.Draft{}, false, err
	}
	d, found, err := s.read(key)
	if err != nil || !found {
		return d, found, err
	}
	if d.InFlight != nil {
		duplicate := false
		for _, span := range d.Uncertain {
			duplicate = duplicate || span == *d.InFlight
		}
		if !duplicate {
			d.Uncertain = append(d.Uncertain, *d.InFlight)
		}
		d.InFlight = nil
	}
	return d, true, nil
}
func (s *DraftStore) Save(d domain.Draft) error {
	d = domain.CloneDraft(d)
	key, err := canonicalDraftKey(d.RepoKey)
	if err != nil {
		return err
	}
	d.RepoKey = key
	if err := validateDraft(d, key); err != nil {
		return err
	}
	if _, _, err := s.read(key); err != nil {
		return err
	}
	if d.UpdatedAt.IsZero() {
		d.UpdatedAt = time.Now().UTC()
	}
	data, err := encodeDraft(d)
	if err != nil {
		return draftError("could not encode todos draft", err)
	}
	if err := s.write(draftPath(s.paths, key), append(data, '\n')); err != nil {
		return draftError("could not save todos draft", err)
	}
	return nil
}
func (s *DraftStore) Delete(repo domain.RepoRef) error {
	key, err := draftRepoKey(repo)
	if err != nil {
		return err
	}
	_, found, err := s.read(key)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	path := draftPath(s.paths, key)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return draftError("could not delete todos draft", err)
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return draftError("could not open draft directory", err)
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		return draftError("could not sync draft directory", err)
	}
	return nil
}
