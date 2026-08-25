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
// Only interior dots are asserted. A trailing dot is not this test's business:
// #326 rejects it, and TestValidatePackageNameRejectsTrailingDotOrSpace owns
// that row.
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

// TestValidatePackageNameRejectsWindowsReservedDeviceNames pins #326's first
// rule. A segment whose part before the first dot is a DOS device name is
// refused on every platform, in any case, with or without an extension.
//
// Measured on Windows CI run 32823717266 (windows-latest): of this list, only
// NUL is actually refused by os.MkdirAll there. Every other row below created
// fine as a directory. The rule is a portability reservation matching
// Microsoft's documented device names, not a reproduced failure — see the
// comment on windowsReservedDeviceNames for the cost that buys.
func TestValidatePackageNameRejectsWindowsReservedDeviceNames(t *testing.T) {
	rejected := []struct {
		name string
		desc string
	}{
		{"CON", "the console device, upper case"},
		{"con", "lower case — the match folds case"},
		{"Con", "mixed case"},
		{"PRN", "printer"},
		{"AUX", "auxiliary"},
		{"NUL", "the one name windows-latest actually refused to create"},
		{"COM1", "first serial port"},
		{"com1", "lower case serial port"},
		{"COM9", "last serial port on the list"},
		{"LPT1", "first parallel port"},
		{"lpt9", "last parallel port on the list"},
		{"con.js", "extension does not save it: the part before the first dot is con"},
		{"NUL.txt", "same, with a two-part name"},
		{"com1.tar.gz", "same, with two dots"},
		{"@org/con", "scoped, device name on the name segment"},
		{"@org/nul.js", "scoped, device name plus extension on the name segment"},
	}
	for _, tc := range rejected {
		err := ValidatePackageName(tc.name)
		if err == nil {
			t.Errorf("ValidatePackageName(%q) = nil, want error (%s)", tc.name, tc.desc)
			continue
		}
		// Same requirement the dot rule carries: the message has to name the
		// offender, because it comes out of a manifest the reader did not
		// necessarily write.
		if !strings.Contains(err.Error(), tc.name) {
			t.Errorf("ValidatePackageName(%q) error %q does not name the package", tc.name, err)
		}
	}
}

// TestValidatePackageNameAllowsNamesThatMerelyResembleDeviceNames guards
// against an over-broad reserved-name check. The rule matches the segment up to
// its first dot against the exact device list, so a longer name that starts
// with those letters, a number outside 1-9, or a device name sitting *after* a
// dot are all still legal. Every row here validates against HEAD before #326 as
// well: this exists to catch an over-broad fix, not to prove the fix.
func TestValidatePackageNameAllowsNamesThatMerelyResembleDeviceNames(t *testing.T) {
	valid := []string{
		"console",     // longer than CON
		"common",      // longer than COM, and not COM<digit>
		"connect",     // longer than CON
		"auxiliary",   // longer than AUX
		"nullish",     // longer than NUL
		"com0",        // COM0 is not a device: the list is COM1-COM9
		"com10",       // two digits, so not on the list either
		"lpt0",        // LPT0 is not a device
		"lpt10",       // two digits
		"foo.con",     // the part before the first dot is "foo", not "con"
		"my-con",      // device name is not the whole part before the dot
		"con-fig",     // ditto
		"@con/pkg",    // see TestValidatePackageNameDeviceRuleDoesNotStripTheScopeAtSign
		"@com1/tools", // ditto
	}
	for _, name := range valid {
		if err := ValidatePackageName(name); err != nil {
			t.Errorf("ValidatePackageName(%q) = %v, want nil", name, err)
		}
	}
}

// TestValidatePackageNameDeviceRuleDoesNotStripTheScopeAtSign pins the one
// place where the device rule deliberately disagrees with the dot rule next to
// it, in both directions. Read as a pair, or the "@con/pkg" row reads as a bug.
//
// The dot rule strips a leading "@" from the scope segment before testing,
// because "@.evil" is a dot-prefixed name wearing an "@". The device rule must
// NOT do that. The directory the linker creates for scope "@con" is literally
// named "@con", and "@con" is not a device name — measured on Windows CI run
// 32823717266, where a plain "CON" directory created fine in the first place,
// so a name Windows treats even less specially is not the one to refuse.
//
// So the two rows below are the same string in different positions and get
// opposite answers, and that is correct:
//   - "@ns/con"  — the segment is literally "con". Rejected.
//   - "@con/pkg" — the segment is literally "@con". Accepted.
func TestValidatePackageNameDeviceRuleDoesNotStripTheScopeAtSign(t *testing.T) {
	if err := ValidatePackageName("@ns/con"); err == nil {
		t.Errorf(`ValidatePackageName("@ns/con") = nil, want error: the name segment is literally "con"`)
	}
	if err := ValidatePackageName("@con/pkg"); err != nil {
		t.Errorf(`ValidatePackageName("@con/pkg") = %v, want nil: the scope segment is literally "@con", not a device name`, err)
	}
	// The same split for a device name that does carry an extension, so the
	// "@" is not stripped on the with-extension path either.
	if err := ValidatePackageName("@ns/nul.txt"); err == nil {
		t.Errorf(`ValidatePackageName("@ns/nul.txt") = nil, want error`)
	}
	if err := ValidatePackageName("@nul.txt/pkg"); err != nil {
		t.Errorf(`ValidatePackageName("@nul.txt/pkg") = %v, want nil`, err)
	}
}

// TestValidatePackageNameRejectsTrailingDotOrSpace pins #326's second rule, and
// it is the half backed by reproduced corruption rather than by reservation.
//
// Measured on Windows CI run 32823717266 (windows-latest): os.MkdirAll("foo.")
// SUCCEEDS and silently creates a directory named "foo". "foo.", "foo ",
// "foo.." and "foo. " all resolve to one existing "foo". On Linux each is a
// distinct directory. Two lock entries therefore share one directory on
// Windows, and nothing downstream can detect it by checking whether the create
// worked.
//
// The scope rows are what pin the rule as per-segment rather than per-name:
// "@ns./pkg" does not end in a dot, but its first path component "@ns." does,
// and that is the component Windows strips. Measured — moving the check onto
// the whole name turns exactly those two rows red and nothing else.
func TestValidatePackageNameRejectsTrailingDotOrSpace(t *testing.T) {
	rejected := []struct {
		name string
		desc string
	}{
		{"foo.", "trailing dot"},
		{"foo ", "trailing space"},
		{"foo..", "two trailing dots"},
		{"foo. ", "dot then space"},
		{"foo .", "space then dot"},
		{"@org/foo.", "scoped, trailing dot on the name segment"},
		{"@org/foo ", "scoped, trailing space on the name segment"},
		{"@ns./pkg", `scoped, trailing dot on the scope segment: the component is "@ns."`},
		{"@ns /pkg", "scoped, trailing space on the scope segment"},
	}
	for _, tc := range rejected {
		err := ValidatePackageName(tc.name)
		if err == nil {
			t.Errorf("ValidatePackageName(%q) = nil, want error (%s)", tc.name, tc.desc)
			continue
		}
		if !strings.Contains(err.Error(), tc.name) {
			t.Errorf("ValidatePackageName(%q) error %q does not name the package", tc.name, err)
		}
	}
}

// TestValidatePackageNameAllowsInteriorDotsAndSpaces guards the trailing rule
// from over-reach: it is about the last character of a segment, so a dot or a
// space anywhere else is untouched. A leading space is left legal here for the
// same reason it is legal today — #326 does not own it, and no measurement in
// this issue says Windows mishandles one.
func TestValidatePackageNameAllowsInteriorDotsAndSpaces(t *testing.T) {
	valid := []string{
		"foo.bar",
		"foo bar",
		"@org/foo.bar",
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
		// removal entry point only waives rules, it never adds any.
		"my-pkg",
		"@org/my-pkg",
	}
	for _, name := range accepted {
		if err := ValidatePackageNameForRemoval(name); err != nil {
			t.Errorf("ValidatePackageNameForRemoval(%q) = %v, want nil", name, err)
		}
	}
}

// TestValidatePackageNameForRemovalAcceptsWindowsUnsafeNames is the same
// argument as the dot-prefix waiver above, for #326's two rules. Neither is
// retroactive and neither is a path-safety rule.
//
// A project linked on Linux before #326 can hold .lnpm/con or .lnpm/foo. — on
// Linux "foo." is a perfectly ordinary distinct directory — plus a lock entry
// naming it. Enforcing the new rules on the way out would make that entry
// permanent: 'lnpm remove' would refuse it and 'lnpm remove --all' would skip
// it on every future run, with no supported way to get rid of it.
//
// The waiver cannot widen the path surface, because neither rule guarded it.
// What keeps a removal inside the project is the "."/".." segment check, the
// absolute-path check, the backslash check and the two-segment limit, and
// TestValidatePackageNameForRemovalStillRejectsUnsafeNames asserts all four
// still fire.
func TestValidatePackageNameForRemovalAcceptsWindowsUnsafeNames(t *testing.T) {
	accepted := []string{
		"con",
		"CON",
		"nul",
		"con.js",
		"com1",
		"lpt9",
		"foo.",
		"foo ",
		"foo..",
		"@org/con",
		"@org/foo.",
		"@ns./pkg",
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
