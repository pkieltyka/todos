//go:build !unix

package cache

import "os"

// These checks are advisory outside the supported Unix platforms. Actual I/O
// still fails as an empty cache when permissions deny it.
func ownedByCurrentUser(os.FileInfo) bool { return true }

func directoryAccessible(_ string, info os.FileInfo) bool {
	return info.Mode().Perm()&0300 == 0300
}
