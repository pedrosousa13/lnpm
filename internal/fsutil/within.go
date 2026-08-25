package fsutil

import (
	"fmt"
	"path/filepath"
	"strings"
)

// WithinRoot reports whether path is root itself or nested inside it, comparing
// the two only after resolving every symlink on both. It also returns path's
// resolved form, so a caller refusing a path can say where it actually pointed
// rather than only that it pointed away.
//
// Both sides are resolved, not just path. A root spelling can run through a
// symlink of its own - on macOS /var is a link to /private/var, so t.TempDir()
// hands back a /var/folders/... path whose real form is /private/var/folders/...
// - and resolving only path against an unresolved root makes every member of
// such a root look like an escape. internal/db.normalizePath and
// internal/cli.canonicalPath resolve for the same reason.
//
// Resolving does not make the prefix test below case-insensitive, and the
// filesystem underneath may well be. Only Windows normalises case here:
// filepath.EvalSymlinks runs toNorm/normBase over every component there, so two
// spellings of one path come back identical - the property
// internal/cli.canonicalPath records, and it scopes that to Windows too,
// alongside the 8.3 short-name case. On Unix evalSymlinks is a bare
// walkSymlinks with no case handling at all, so on macOS - case-insensitive by
// default, and one of this repo's CI platforms - strings.HasPrefix still
// compares case-sensitively.
//
// The comparison is nonetheless correct as workspace.expandGlobs uses it, but
// that is a fact about the caller rather than a property of this function: the
// member path is filepath.Join(root, match) where match came out of a directory
// walk rooted at that same root string, so both sides carry the identical root
// spelling and no difference in case exists for the prefix test to miss. A
// future caller handing this function two independently spelled paths on a
// case-insensitive filesystem would not be protected, and would have to fold
// case itself.
//
// Both paths must exist: filepath.EvalSymlinks fails on a path that does not,
// and that failure is returned rather than swallowed, naming the side it came
// from. A caller that cannot rule out a missing path must decide for itself what
// a failure to look means; docs/adr/0001 settles only the direction that widens
// a publish, and leaves the fail-closed direction a judgement call to make case
// by case.
//
// internal/cli.isWithinStore answers a similar-looking question and is
// deliberately not this function: it runs inside gc, over a store path gc may
// already have deleted, and a hard error from EvalSymlinks there would be a
// regression. It stops at filepath.Abs plus Clean and returns a bare bool.
func WithinRoot(root, path string) (bool, string, error) {
	resolvedRoot, err := resolve(root)
	if err != nil {
		return false, "", fmt.Errorf("failed to resolve the root %s: %w", root, err)
	}
	resolvedPath, err := resolve(path)
	if err != nil {
		return false, "", fmt.Errorf("failed to resolve %s: %w", path, err)
	}

	if resolvedPath == resolvedRoot {
		return true, resolvedPath, nil
	}
	// The separator is what keeps a sibling out: "/ws-evil" has "/ws" as a
	// string prefix and is not inside it.
	return strings.HasPrefix(resolvedPath, resolvedRoot+string(filepath.Separator)), resolvedPath, nil
}

// resolve gives a path its one canonical spelling: made absolute first, then
// with every symlink followed. EvalSymlinks cleans the result as well.
//
// The order is the whole point. EvalSymlinks keeps a relative input relative, so
// resolving first would leave filepath.Abs to join the result to an unresolved
// working directory - and the working directory is a spelling that can run
// through a symlink of its own. On Unix os.Getwd hands back $PWD whenever it is
// absolute and names the current directory, so a shell that reached the
// directory through a link reports the link; on Windows and plan9 os.Getwd calls
// syscall.Getwd directly, with the comment that $PWD must not be relied on. The
// ordering is right on either platform, because filepath.Abs joins to whatever
// unresolved spelling os.Getwd did report. Under such a cwd a relative argument
// and an absolute one canonicalise against different trees, and every member of
// the root looks like an escape.
func resolve(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}
