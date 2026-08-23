package pack

import (
	"fmt"
	"path/filepath"
	"strings"
)

// maxPackageNameLen is npm's documented maximum package name length.
const maxPackageNameLen = 214

// ValidatePackageName rejects package names that are unsafe to use as filesystem
// path segments. The name from an untrusted package.json (or CLI argument) is
// joined into store and project paths, so a name like "../../etc" or an absolute
// path could escape the intended directory (arbitrary write / RemoveAll).
//
// It allows a normal name ("my-pkg") or a single scoped name ("@org/my-pkg")
// and rejects everything else with path semantics.
//
// It also rejects a leading dot on either segment, which npm forbids anyway.
// That is a reservation rather than a path-safety rule: lnpm's own entries under
// .lnpm and in the store are dot-prefixed, so a package free to call itself
// ".tmp-deadbeef" collides with the temp shape gc reclaims (#325).
//
// The dot rule applies at the boundary only, and nothing revalidates what is
// already on disk, so a project linked before it keeps its .lnpm/{name} entry
// and its lock entry — 'lnpm list' and 'lnpm status' still show it, because both
// read the lock file rather than the directory. Every command that puts the name
// back through here now refuses that one package — add, pull, remove and retreat
// each report it, and the ones that handle several packages carry on with the
// rest rather than aborting. Clearing one out means deleting .lnpm/{name} and
// its lock entry by hand. No migration is provided, deliberately — the entry is
// left where its owner can see it rather than removed by a tool that has just
// declared it invalid.
func ValidatePackageName(name string) error {
	if name == "" {
		return fmt.Errorf("package name is empty")
	}
	if len(name) > maxPackageNameLen {
		return fmt.Errorf("package name too long (%d > %d)", len(name), maxPackageNameLen)
	}
	if strings.ContainsRune(name, 0) {
		return fmt.Errorf("package name contains a NUL byte")
	}
	if strings.ContainsRune(name, '\\') {
		return fmt.Errorf("invalid package name %q: backslashes are not allowed", name)
	}
	if filepath.IsAbs(name) {
		return fmt.Errorf("invalid package name %q: must not be an absolute path", name)
	}

	// At most a single "/" separating a scope from the name (@org/name).
	parts := strings.Split(name, "/")
	if len(parts) > 2 {
		return fmt.Errorf("invalid package name %q: too many path segments", name)
	}
	if len(parts) == 2 && (!strings.HasPrefix(parts[0], "@") || parts[0] == "@") {
		return fmt.Errorf("invalid scoped package name %q", name)
	}

	for i, p := range parts {
		if p == "" || p == "." || p == ".." {
			return fmt.Errorf("invalid package name %q: bad path segment", name)
		}

		// The scope segment carries a leading "@", which hides a dot from a
		// plain prefix test: strings.Split("@.evil/pkg", "/") gives "@.evil",
		// which begins with "@" and not with ".". Strip it before testing, so
		// both segments are held to the same rule.
		seg := p
		if i == 0 && len(parts) == 2 {
			seg = strings.TrimPrefix(p, "@")
		}
		if strings.HasPrefix(seg, ".") {
			return fmt.Errorf("invalid package name %q: a name segment must not begin with a dot; "+
				"lnpm reserves dot-prefixed entries under .lnpm and in the store for its own temp "+
				"directories, which gc reclaims", name)
		}
	}
	return nil
}
