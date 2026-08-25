package fsutil

import (
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
// Both paths must exist: filepath.EvalSymlinks fails on a path that does not,
// and that failure is returned rather than swallowed. A caller that cannot rule
// out a missing path must decide for itself what a failure to look means;
// docs/adr/0001 is the standard for which direction that decision goes.
//
// internal/cli.isWithinStore answers a similar-looking question and is
// deliberately not this function. It compares store paths lnpm built itself from
// its own configured root, where resolution buys nothing and a hard error on a
// path gc has already deleted would be a regression, so it stops at
// filepath.Abs plus Clean and returns a bare bool. This one compares a path a
// possibly-hostile checkout supplied - a workspace member reached by a glob that
// followed a symlink out of the tree - where an unresolved comparison is exactly
// what the attacker needs. The two are not redundant and neither should be
// rewritten in terms of the other.
func WithinRoot(root, path string) (bool, string, error) {
	resolvedRoot, err := resolve(root)
	if err != nil {
		return false, "", err
	}
	resolvedPath, err := resolve(path)
	if err != nil {
		return false, "", err
	}

	if resolvedPath == resolvedRoot {
		return true, resolvedPath, nil
	}
	// The separator is what keeps a sibling out: "/ws-evil" has "/ws" as a
	// string prefix and is not inside it.
	return strings.HasPrefix(resolvedPath, resolvedRoot+string(filepath.Separator)), resolvedPath, nil
}

// resolve gives a path its one canonical spelling: absolute, with every symlink
// followed. filepath.Abs cleans as well, and is applied after EvalSymlinks
// because EvalSymlinks keeps a relative input relative.
func resolve(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	return filepath.Abs(resolved)
}
