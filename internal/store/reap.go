package store

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/pedrosousa13/lnpm/internal/debug"
	"github.com/pedrosousa13/lnpm/internal/fsutil"
)

// TempDir is one temp directory the store's write path left behind.
type TempDir struct {
	// Path is the absolute path of the directory.
	Path string
	// Size is how many bytes reclaiming it frees.
	Size int64
}

// FindTempDirs returns the temp directories still sitting beside store entries,
// and how many directories it could not read. Store writes a package into one of
// these and renames it into place, and its deferred cleanup only runs on a
// normal return — so a publish killed by a signal, an OOM or a power loss leaves
// one behind holding a full package copy. It only reads; the caller decides
// whether to remove them.
//
// Callers must hold the exclusive database lock. Publish opens the database
// before it writes to the store, so while that lock is held nothing found here
// can have a live writer. Without it this is a list of directories some other
// lnpm process may be writing into right now.
//
// A directory that cannot be read is counted and skipped rather than returned as
// a failure, matching link.FindTempEntries; the reasoning is given there.
func (s *Store) FindTempDirs() (dirs []TempDir, unreadable int) {
	parents, unreadable := packageDirs(s.basePath)

	for _, parent := range parents {
		items, err := os.ReadDir(parent)
		if err != nil {
			debug.Logf("store: cannot scan %s for temp directories: %v", parent, err)
			unreadable++
			continue
		}
		for _, item := range items {
			if !item.IsDir() || !isTempDirName(item.Name()) {
				continue
			}
			path := filepath.Join(parent, item.Name())
			dirs = append(dirs, TempDir{Path: path, Size: fsutil.DirSize(path)})
		}
	}
	return dirs, unreadable
}

// isTempDirName reports whether name is one newTempDir produced: a dot, the
// entry's hash, the infix, and the decimal tail os.MkdirTemp appends.
//
// The match is deliberately narrow rather than "anything dot-prefixed". The
// completeness marker, the backfill sentinel and a dot-named package directory
// stored before #325 made ValidatePackageName reject a leading dot are all
// dot-prefixed, and none of them is ours to delete.
func isTempDirName(name string) bool {
	rest, ok := strings.CutPrefix(name, ".")
	if !ok {
		return false
	}
	i := strings.LastIndex(rest, tempInfix)
	if i <= 0 {
		return false
	}
	return fsutil.IsLowerHex(rest[:i]) && isDigits(rest[i+len(tempInfix):])
}

// isDigits reports whether s is a non-empty run of decimal digits, which is what
// os.MkdirTemp appends to the pattern it is given.
func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
