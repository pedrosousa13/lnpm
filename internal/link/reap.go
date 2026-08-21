package link

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/pedrosousa13/lnpm/internal/debug"
	"github.com/pedrosousa13/lnpm/internal/fsutil"
)

// TempEntry is one temp entry a project's .lnpm directory still holds.
type TempEntry struct {
	// Path is the absolute path of the entry.
	Path string
	// Size is how many bytes reclaiming it frees. A link is removed as a link,
	// so a leftover from LinkSource counts as nothing rather than as its target.
	Size int64
	// Retired marks the shape an interrupted swap leaves behind. Link and
	// LinkSource rename the previous .lnpm/{package} aside under this name
	// before renaming the replacement into place, so a retired entry holds what
	// was linked before it rather than something half-written.
	Retired bool
	// Link distinguishes LinkSource's shape from Link's. LinkSource makes a link
	// to a source directory where Link populates a directory of its own, and the
	// two hold different things — which matters when saying what a retired entry
	// held, since a retired link is a link and not a copy of anything.
	Link bool
}

// FindTempEntries returns the temp entries under projectPath's .lnpm directory,
// including those inside scope directories, and how many directories it could
// not read. It only reads; the caller decides whether to remove them.
//
// Callers must hold the exclusive database lock. Every path that creates one of
// these entries opens the database first, so while that lock is held nothing
// found here can have a live writer. Without it this is a list of directories
// some other lnpm process may be writing into right now.
//
// A directory that cannot be read is counted and skipped rather than returned as
// a failure. Per ADR-0001 the direction is what matters: skipping narrows what a
// sweep reclaims, which leaves bytes on disk for the next run, where aborting
// would abandon every other project and the store sweep that already succeeded.
// gc is the command a user reaches for when something is already wrong, so it
// has to keep working on the parts it can still read. The count is reported
// rather than only logged, because these directories being invisible is half of
// what this sweep exists to fix.
//
// A .lnpm directory that does not exist is neither an entry nor a failure: a
// project may never have linked anything. One that is a link rather than a
// directory is counted as unreadable and not scanned at all, for the reason
// refuseLinkedDir gives.
func FindTempEntries(projectPath string) (entries []TempEntry, unreadable int) {
	lnpmDir := filepath.Join(projectPath, ".lnpm")

	// ReadDir follows a symlinked .lnpm, so without this the sweep would list -
	// and gc would then offer to delete - temp entries belonging to whatever a
	// checkout pointed .lnpm at. Counting it as one directory that could not be
	// read is the shape this function already has for a directory it must not
	// act on: nothing is reclaimed here, the count says so, and the remaining
	// projects are still swept.
	if err := refuseLinkedDir(".lnpm directory", lnpmDir); err != nil {
		debug.Logf("link: not scanning %s for temp entries: %v", lnpmDir, err)
		return nil, 1
	}

	scopes, err := os.ReadDir(lnpmDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0
		}
		debug.Logf("link: cannot scan %s for temp entries: %v", lnpmDir, err)
		return nil, 1
	}

	dirs := []string{lnpmDir}
	// Scope directories hold a scoped package's entries, so the temp entries for
	// @org/name sit one level down rather than at the top. Only "@" directories
	// are descended into: everything else at the top level is a package, and its
	// contents belong to the user.
	for _, scope := range scopes {
		if scope.IsDir() && strings.HasPrefix(scope.Name(), "@") {
			dirs = append(dirs, filepath.Join(lnpmDir, scope.Name()))
		}
	}

	for _, dir := range dirs {
		items, err := os.ReadDir(dir)
		if err != nil {
			debug.Logf("link: cannot scan %s for temp entries: %v", dir, err)
			unreadable++
			continue
		}
		for _, item := range items {
			retired, ok := isTempEntryName(item.Name())
			if !ok {
				continue
			}
			// Neither constructor makes a regular file: newTempDir makes a
			// directory and newTempLink a link. Excluding only regular files
			// keeps LinkSource's leftover in scope without depending on which
			// mode bits a platform reports for a link to a directory — a
			// Windows junction among them.
			if item.Type().IsRegular() {
				continue
			}
			path := filepath.Join(dir, item.Name())
			// Only a real directory is walked for its size: a link is removed as
			// a link, so its target is not what is being freed.
			var size int64
			if item.IsDir() {
				size = fsutil.DirSize(path)
			}
			entries = append(entries, TempEntry{
				Path:    path,
				Size:    size,
				Retired: retired,
				Link:    !item.IsDir(),
			})
		}
	}
	return entries, unreadable
}

// isTempEntryName reports whether name is one newTempDir or newTempLink
// produced, and whether it is the retired form the swap derives from it.
//
// The match is deliberately narrow. ListLinked hides every dot-prefixed entry,
// but a package name may legitimately begin with a dot, so "dot-prefixed" is not
// the same thing as "ours" — reclaiming a user's dot-named package would be a
// far worse bug than the leak this sweep exists to fix. Only the exact shape the
// constructors produce is matched: the prefix, then the lowercase hex of a
// uint64, optionally followed by the retired suffix.
func isTempEntryName(name string) (retired, ok bool) {
	rest, retired := strings.CutSuffix(name, retiredSuffix)
	rest, hasPrefix := strings.CutPrefix(rest, tempPrefix)
	if !hasPrefix || !fsutil.IsLowerHex(rest) {
		return false, false
	}
	return retired, true
}
