package pack

import (
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
	}
	for _, name := range valid {
		if err := ValidatePackageName(name); err != nil {
			t.Errorf("ValidatePackageName(%q) = %v, want nil", name, err)
		}
	}
}

// TestValidatePackageNameForRemovalAcceptsDotPrefixedNames covers the one thing
// the removal entry point exists for. #325 made the leading dot invalid, but a
// project linked before it can still hold .lnpm/.hidden-pkg and a lock entry for
// it, and enforcing the new rule on the way out would make that entry permanent:
// 'lnpm remove' refuses it and 'lnpm remove --all' skips it on every future run.
func TestValidatePackageNameForRemovalAcceptsDotPrefixedNames(t *testing.T) {
	accepted := []string{
		".hidden-pkg",
		".tmp-deadbeef",
		".npmrc",
		"@org/.hidden",
		"@.evil/pkg",
		// Everything the strict validator accepts is still accepted here: the
		// removal entry point waives one rule, it does not add any.
		"my-pkg",
		"@org/my-pkg",
	}
	for _, name := range accepted {
		if err := ValidatePackageNameForRemoval(name); err != nil {
			t.Errorf("ValidatePackageNameForRemoval(%q) = %v, want nil", name, err)
		}
	}
}

// TestValidatePackageNameForRemovalStillRejectsUnsafeNames is the test that
// proves the waiver is narrow, and it is the one that matters. The name is
// joined into .lnpm/{name} for an os.RemoveAll and into node_modules/{name} for
// an os.Remove, so a traversal here deletes outside the project.
//
// Waiving the dot rule cannot widen that surface, because the dot rule never
// guarded it: traversal is stopped by the "."/".." segment check, the absolute
// path check, the backslash check and the segment count, and this asserts every
// one of them still fires on the removal path.
func TestValidatePackageNameForRemovalStillRejectsUnsafeNames(t *testing.T) {
	rejected := []struct {
		name string
		desc string
	}{
		{"", "empty"},
		{"..", "parent directory"},
		{".", "current directory"},
		{"../evil", "parent traversal"},
		{"../../../../tmp/evil", "deep traversal"},
		{"foo/../bar", "embedded traversal"},
		{"@scope/..", "scoped traversal"},
		{"/abs/path", "absolute"},
		{"a/b/c", "too many segments"},
		{"foo/bar", "unscoped slash"},
		{"@/name", "empty scope"},
		{"name\\with\\backslash", "backslash"},
		{"with\x00nul", "nul byte"},
	}
	for _, tc := range rejected {
		if err := ValidatePackageNameForRemoval(tc.name); err == nil {
			t.Errorf("ValidatePackageNameForRemoval(%q) = nil, want error (%s)", tc.name, tc.desc)
		}
	}

	long := make([]byte, maxPackageNameLen+1)
	for i := range long {
		long[i] = 'a'
	}
	if err := ValidatePackageNameForRemoval(string(long)); err == nil {
		t.Errorf("ValidatePackageNameForRemoval(<215 chars>) = nil, want error")
	}
}
