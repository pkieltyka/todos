//go:build unix

package cache

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

type ownerInfo struct {
	os.FileInfo
	stat syscall.Stat_t
}

func (i ownerInfo) Sys() any { return &i.stat }

func TestDirectoryOwnerCheck(t *testing.T) {
	info, err := os.Stat(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	own := ownerInfo{FileInfo: info, stat: syscall.Stat_t{Uid: uint32(os.Geteuid())}}
	other := own
	other.stat.Uid ^= 1
	if !ownedByCurrentUser(own) || ownedByCurrentUser(other) {
		t.Fatal("directory ownership ignored")
	}
}

func TestStore_ForeignDirectoryIsNotUsedOrRepaired(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("changing ownership requires root")
	}
	s := newStore(t)
	if err := os.Mkdir(s.root, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(s.root, 1, -1); err != nil {
		t.Fatal(err)
	}
	defer os.Chown(s.root, 0, -1)
	s.Put("cred", "key", []byte("v"), testNow)
	if s.Bump("cred") || Check(s.root).OK {
		t.Fatal("foreign-owned cache accepted")
	}
	info, err := os.Stat(s.root)
	if err != nil || info.Mode().Perm() != 0755 {
		t.Fatal("foreign directory repaired", err)
	}
	if _, err := os.Stat(filepath.Join(s.root, "cred")); !os.IsNotExist(err) {
		t.Fatal("foreign directory written", err)
	}
}
