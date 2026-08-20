package fsutil

import (
	"io/fs"
	"path/filepath"

	"github.com/pedrosousa13/lnpm/internal/debug"
)

// DirSize sums the regular files under dir.
//
// Links are counted as themselves rather than followed, so a tree holding a link
// is not reported as holding whatever the link points at. A tree that cannot be
// walked in full is reported as far as it was read: callers use this to say how
// much space removing a directory frees, and an unreadable subdirectory is no
// reason to withhold the figure entirely.
func DirSize(dir string) int64 {
	var total int64
	// WalkDir does not follow links.
	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Type() != 0 {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	if err != nil {
		debug.Logf("fsutil: could not size %s fully: %v", dir, err)
	}
	return total
}
