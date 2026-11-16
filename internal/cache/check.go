package cache

import (
	"os"
	"path/filepath"

	"github.com/pkieltyka/todos/internal/domain"
)

func DefaultRoot(getenv func(string) string, home func() (string, error)) string {
	base := getenv("XDG_CACHE_HOME")
	if !filepath.IsAbs(base) {
		h, err := home()
		if err != nil || !filepath.IsAbs(h) {
			return ""
		}
		base = filepath.Join(h, ".cache")
	}
	return filepath.Join(base, "todos", "v1")
}

// Check examines directory metadata only; it never opens entries or repairs,
// creates, or writes a directory, even when the cache does not exist yet.
func Check(root string) domain.DoctorCheck {
	check := domain.DoctorCheck{Name: "Cache", Detail: root, Hint: "delete or repair the disposable cache directory, or set TODOS_CACHE=off"}
	if !filepath.IsAbs(root) {
		check.Detail = "cache directory unavailable"
		return check
	}
	path := root
	for {
		info, err := os.Lstat(path)
		if err == nil {
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !directoryAccessible(path, info) || path == root && !ownedByCurrentUser(info) {
				check.Detail += "; directory is not usable"
				return check
			}
			if path == root && info.Mode().Perm() != 0700 {
				check.Detail += "; permissions must be 0700 (tightened on cache use)"
				return check
			}
			check.OK = true
			check.Hint = ""
			if path != root {
				check.Detail += "; usable (not created yet)"
			} else {
				check.Detail += "; usable"
			}
			return check
		}
		if !os.IsNotExist(err) || filepath.Dir(path) == path {
			check.Detail += "; directory is not usable"
			return check
		}
		path = filepath.Dir(path)
	}
}
