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

	for _, p := range parts {
		if p == "" || p == "." || p == ".." {
			return fmt.Errorf("invalid package name %q: bad path segment", name)
		}
	}
	return nil
}
