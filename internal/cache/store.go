// Package cache stores disposable bytes. Freshness policy belongs to service.
package cache

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const MaxEntryBytes = 4 << 20

type Options struct{ Now func() time.Time }
type Store struct {
	root string
	now  func() time.Time
}
type entry struct {
	Version int       `json:"version"`
	Space   string    `json:"space"`
	Key     string    `json:"key"`
	ReadAt  time.Time `json:"read_at"`
	Data    []byte    `json:"data"`
}

// New has no filesystem side effects, including when the cache is unavailable.
func New(root string, opts Options) *Store {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if !filepath.IsAbs(root) {
		root = ""
	}
	return &Store{root: filepath.Clean(root), now: opts.Now}
}

func safeSpace(space string) bool {
	if space == "" || filepath.IsAbs(space) || strings.ContainsAny(space, "\\\x00") {
		return false
	}
	for _, part := range strings.Split(space, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func privateDir(path string, create bool) bool {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) && create {
		if err = os.MkdirAll(path, 0700); err != nil {
			return false
		}
		info, err = os.Lstat(path)
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !ownedByCurrentUser(info) {
		return false
	}
	return info.Mode().Perm() == 0700 || os.Chmod(path, 0700) == nil
}

func (s *Store) directory(space string, create bool) (string, bool) {
	if s.root == "." || !safeSpace(space) || !privateDir(s.root, create) {
		return "", false
	}
	path := s.root
	for _, part := range strings.Split(space, "/") {
		path = filepath.Join(path, part)
		if !privateDir(path, create) {
			return "", false
		}
	}
	return path, true
}

func keyFile(key string) string {
	digest := sha256.Sum256([]byte(key))
	return hex.EncodeToString(digest[:]) + ".json"
}

func readPrivate(path string, limit int64) ([]byte, bool) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit || info.Size() < 0 {
		return nil, false
	}
	if info.Mode().Perm() != 0600 && os.Chmod(path, 0600) != nil {
		return nil, false
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	return b, err == nil && int64(len(b)) <= limit
}

func (s *Store) Get(space, key string) ([]byte, time.Time, bool) {
	dir, ok := s.directory(space, false)
	if !ok {
		return nil, time.Time{}, false
	}
	b, ok := readPrivate(filepath.Join(dir, keyFile(key)), MaxEntryBytes)
	var e entry
	if !ok || json.Unmarshal(b, &e) != nil || e.Version != 1 || e.Space != space || e.Key != key || e.ReadAt.IsZero() || e.ReadAt.After(s.now()) {
		return nil, time.Time{}, false
	}
	return e.Data, e.ReadAt, true
}

func atomicWrite(dir, name string, b []byte) bool {
	f, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return false
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err != nil {
		f.Close()
		return false
	}
	if err = f.Close(); err != nil {
		return false
	}
	return os.Rename(f.Name(), filepath.Join(dir, name)) == nil
}

var pruneRoots sync.Map // root -> *sync.Once; at most one sweep per root/process

const sweepMarker = "last-sweep"

func (s *Store) beforeWrite(space string) {
	credential := filepath.Join(s.root, strings.Split(space, "/")[0])
	_ = os.Chtimes(credential, s.now(), s.now())
	value, _ := pruneRoots.LoadOrStore(s.root, &sync.Once{})
	value.(*sync.Once).Do(func() {
		now := s.now()
		if b, ok := readPrivate(filepath.Join(s.root, sweepMarker), 64); ok {
			if last, err := time.Parse(time.RFC3339Nano, string(b)); err == nil && !last.After(now) && now.Sub(last) < 24*time.Hour {
				return
			}
		}
		// Best effort, with no locks: simultaneous first writers may both sweep.
		if !atomicWrite(s.root, sweepMarker, []byte(now.UTC().Format(time.RFC3339Nano))) {
			return
		}
		children, _ := os.ReadDir(s.root)
		for _, child := range children {
			info, err := child.Info()
			if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				continue
			}
			path := filepath.Join(s.root, child.Name())
			if s.now().Sub(info.ModTime()) > 30*24*time.Hour {
				_ = os.RemoveAll(path)
				continue
			}
			_ = filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
				if err == nil && !d.IsDir() && (strings.HasSuffix(d.Name(), ".json") || strings.HasPrefix(d.Name(), ".tmp-")) {
					if info, e := d.Info(); e == nil && s.now().Sub(info.ModTime()) > 24*time.Hour {
						_ = os.Remove(p)
					}
				}
				return nil
			})
		}
	})
}

func (s *Store) Put(space, key string, data []byte, readAt time.Time) {
	if len(data) > MaxEntryBytes || readAt.IsZero() || readAt.After(s.now()) {
		return
	}
	b, err := json.Marshal(entry{Version: 1, Space: space, Key: key, ReadAt: readAt.UTC(), Data: data})
	if err != nil || len(b) > MaxEntryBytes {
		return
	}
	dir, ok := s.directory(space, true)
	if ok {
		s.beforeWrite(space)
		atomicWrite(dir, keyFile(key), b)
	}
}

func (s *Store) Epoch(space string) string {
	dir, ok := s.directory(space, false)
	if ok {
		if b, valid := readPrivate(filepath.Join(dir, "epoch"), 64); valid && len(b) == 64 {
			if _, err := hex.DecodeString(string(b)); err == nil {
				return string(b)
			}
		}
	}
	return "none"
}

func (s *Store) Bump(space string) bool {
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		return false
	}
	if dir, ok := s.directory(space, true); ok {
		s.beforeWrite(space)
		return atomicWrite(dir, "epoch", []byte(hex.EncodeToString(value[:])))
	}
	return false
}

func (s *Store) Drop(space, prefix string) {
	dir, ok := s.directory(space, false)
	if !ok {
		return
	}
	files, _ := os.ReadDir(dir)
	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, file.Name())
		if prefix == "" {
			_ = os.Remove(path)
			continue
		}
		if b, valid := readPrivate(path, MaxEntryBytes); valid {
			var e entry
			if json.Unmarshal(b, &e) == nil && strings.HasPrefix(e.Key, prefix) {
				_ = os.Remove(path)
			}
		}
	}
}
