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
// This validates a name at the boundary it is presented at; it revalidates
// nothing already on disk. A .lnpm or a store populated before the dot rule can
// still hold a dot-named entry, which is why the reap sweeps stay narrow and why
// removal goes through ValidatePackageNameForRemoval.
func ValidatePackageName(name string) error {
	return validatePackageName(name, true)
}

// ValidatePackageNameForRemoval is ValidatePackageName with the leading-dot
// reservation waived, and nothing else changed. Use it on paths that take a
// package away; creation, publish, store and pack paths use the strict form.
//
// The waiver exists because the dot rule is not retroactive. A project linked
// before #325 can hold .lnpm/.hidden-pkg and a lock entry naming it, and
// enforcing the new rule on the way out would make that entry permanent:
// 'lnpm remove' would refuse it and 'lnpm remove --all' would skip it on every
// future run, with no supported way to get rid of it.
//
// Waiving it cannot widen the path surface, because the dot rule never guarded
// that surface. What keeps a removal inside the project is the "."/".." segment
// check, the absolute-path check, the backslash check and the two-segment limit,
// and every one of those still runs here. A leading dot on a segment is a name
// lnpm reserves for itself, not a name that escapes anywhere: ".hidden-pkg"
// resolves to a child of .lnpm exactly like "hidden-pkg" does.
//
// The waiver is wider than "dot-prefixed package names", though, and the extra
// case is worth naming because it is not obvious: "@../pkg" is rejected by the
// strict form via this same dot rule, since the "."/".." segment check sees the
// segment as "@.." rather than "..". Removal therefore accepts it. It stays
// contained because "@.." is a literal directory name that filepath.Clean does
// not collapse, so it resolves to a child of .lnpm like any other scope - run
// and confirmed, and pinned by TestUnlinkContainsAScopeNamedLikeATraversal.
//
// The full list of what a removal then does with the name, so the reasoning can
// be checked rather than taken on trust:
//   - joins it into .lnpm/{name} for an os.RemoveAll, and node_modules/{name}
//     for an os.Remove;
//   - for a scoped name, calls removeDirIfEmpty on the parent of each of those.
//     That parent is .lnpm/{scope} or node_modules/{scope}, never higher: a
//     two-segment name is only accepted when its first segment begins with "@",
//     and a one-segment name skips this entirely;
//   - writes it as a package.json dependency key, on retreat's path.
//
// None of those is reached by a dot that the other checks would not already have
// caught.
//
// That is the whole claim. This is not a "safer because removal is safer"
// argument — an unlink is a destructive operation and the traversal checks are
// load-bearing here, which is what TestValidatePackageNameForRemovalStillRejectsUnsafeNames
// and TestUnlinkStillRefusesATraversingName assert.
func ValidatePackageNameForRemoval(name string) error {
	return validatePackageName(name, false)
}

// validatePackageName is the single implementation behind both entry points, so
// the two cannot drift. reserveDotPrefix selects the only difference between
// them: whether a segment beginning with a dot is refused.
func validatePackageName(name string, reserveDotPrefix bool) error {
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

		if !reserveDotPrefix {
			continue
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
