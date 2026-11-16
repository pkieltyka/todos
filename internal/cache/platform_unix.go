//go:build unix

package cache

import (
	"os"
	"syscall"
)

func ownedByCurrentUser(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid())
}

func directoryAccessible(path string, _ os.FileInfo) bool {
	return syscall.Access(path, 3) == nil // write and search; no side effects
}
