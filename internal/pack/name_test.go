package pack

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidatePackageName(t *testing.T) {
	valid := []string{
		"my-pkg",
		"lodash",
		"@org/my-pkg",
		"@scope/name.with.dots",
		"under_score",
		"a",
	}
	for _, name := range valid {
		if err := ValidatePackageName(name); err != nil {
			t.Errorf("ValidatePackageName(%q) = %v, want nil", name, err)
		}
	}

	invalid := []struct {
		name string
		desc string
	}{
		{"", "empty"},
		{"../evil", "parent traversal"},
		{"../../../../tmp/evil", "deep traversal"},
		{"foo/../bar", "embedded traversal"},
		{"@scope/..", "scoped traversal"},
		{"/abs/path", "absolute"},
		{"a/b/c", "too many segments"},
		{"foo/bar", "unscoped slash"},
		{"@/name", "empty scope"},
		{"name\\with\\backslash", "backslash"},
		{".", "dot"},
		{"..", "dotdot"},
		{"with\x00nul", "nul byte"},
	}
	for _, tc := range invalid {
		if err := ValidatePackageName(tc.name); err == nil {
			t.Errorf("ValidatePackageName(%q) = nil, want error (%s)", tc.name, tc.desc)
		}
	}

	long := make([]byte, maxPackageNameLen+1)
	for i := range long {
		long[i] = 'a'
	}
	if err := ValidatePackageName(string(long)); err == nil {
		t.Errorf("ValidatePackageName(<215 chars>) = nil, want error")
	}
}

// TestValidatePackageNameRejectsDotPrefixedSegments pins #325. A dot-prefixed
// name is what lnpm uses for its own entries under .lnpm and in the store, so
// accepting one as a package name lets a manifest collide with the sweep that
// reclaims them: ".tmp-deadbeef" is both a legal name here and the exact shape
// isTempEntryName matches, and gc deletes it as a crash orphan.
//
// The scoped rows are the ones a naive check misses. strings.Split splits
// "@.evil/pkg" into "@.evil" and "pkg", and "@.evil" starts with "@", not with a
// dot — the scope has to be checked with its "@" stripped.
func TestValidatePackageNameRejectsDotPrefixedSegments(t *testing.T) {
	rejected := []struct {
		name string
		desc string
	}{
		{".tmp-deadbeef", "the collision from the issue body: gc's temp shape"},
		{".hidden-pkg", "any dot-prefixed name, not only the temp shape"},
		{".npmrc", "a dot-prefixed name that is not lnpm-shaped at all"},
		{"@org/.tmp-deadbeef", "scoped, dot on the name segment"},
		{"@org/.hidden", "scoped, dot on the name segment"},
		{"@.evil/pkg", "scoped, dot on the scope segment behind the @"},
		{"@./pkg", "scope that is only a dot behind the @"},
	}
	for _, tc := range rejected {
		err := ValidatePackageName(tc.name)
		if err == nil {
			t.Errorf("ValidatePackageName(%q) = nil, want error (%s)", tc.name, tc.desc)
			continue
		}
		// The message has to name the offender: these come out of a manifest the
		// reader did not necessarily write, and "invalid package name" alone
		// does not say which one.
		if !strings.Contains(err.Error(), tc.name) {
			t.Errorf("ValidatePackageName(%q) error %q does not name the package", tc.name, err)
		}
	}
}

// TestValidatePackageNameAllowsDotsAwayFromSegmentStart guards the other
// direction: the rule is about a *leading* dot, and npm names carry interior
// dots routinely. This passes against HEAD before #325 as well — it exists to
// catch an over-broad fix, not to prove the fix.
//
// Only interior dots are asserted. A trailing dot is deliberately left out: it
// validates today, but #326 owns that question, and pinning it here would be
// this test claiming a guarantee that is not its own.
func TestValidatePackageNameAllowsDotsAwayFromSegmentStart(t *testing.T) {
	valid := []string{
		"name.with.dots",
		"lodash.merge",
		"@my.scope/pkg",
		"@scope/name.with.dots",
	}
	for _, name := range valid {
		if err := ValidatePackageName(name); err != nil {
			t.Errorf("ValidatePackageName(%q) = %v, want nil", name, err)
		}
	}
}

// TestReadPackageJSONRejectsADotPrefixedName is the acceptance criterion stated
// where the untrusted value actually enters: a manifest. Every path that stores,
// links or publishes a package reads its name from here.
func TestReadPackageJSONRejectsADotPrefixedName(t *testing.T) {
	dir := t.TempDir()
	manifest := `{"name": ".tmp-deadbeef", "version": "1.0.0"}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(manifest), 0644); err != nil {
		t.Fatal(err)
	}

	pkg, err := readPackageJSON(dir)
	if err == nil {
		t.Fatalf("readPackageJSON() accepted %q, returning %+v", manifest, pkg)
	}
	if !strings.Contains(err.Error(), ".tmp-deadbeef") {
		t.Errorf("readPackageJSON() error %q does not name the package", err)
	}
}
