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
// Measured on Windows CI run 32823717266 (windows-latest): the probe tried nine
// names there, and os.MkdirAll refused exactly one of them, bare NUL, with "The
// system cannot find the path specified." The other eight created fine as
// directories — CON, AUX, PRN, COM1, LPT1, con, con.js and NUL.txt.
//
// Nine of the sixteen rows below are those probed names. The other seven — Con,
// com1, COM9, lpt9, com1.tar.gz, @org/con and @org/nul.js — were never created
// on Windows at all, and neither was any *file* named after a device, as opposed
// to a directory. The rule is a portability reservation, not a reproduced
// failure: lnpm's map is the classic device set #326 specified, drawn from
// Microsoft's documented device names but deliberately not the whole of that
// list — see the comment on windowsReservedDeviceNames for what it leaves out
// and for the cost the reservation buys.
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
// its first dot, with trailing spaces trimmed off it, against the exact device
// list — so a longer name that starts with those letters, a number outside 1-9,
// or a device name sitting *after* a dot are all still legal. The trim narrows
// nothing here: none of these stems ends in a space, and
// TestValidatePackageNameDeviceRuleTrimsTheStem owns the rows that do. Every row
// here validates against HEAD before #326 as well: this exists to catch an
// over-broad fix, not to prove the fix.
//
// The com0 and lpt0 rows rest on the Windows rule, not on lnpm's map: Windows
// numbers its serial and parallel ports from 1, so there is no COM0 or LPT0
// device to collide with. That is read from Microsoft's documentation — the
// Windows probe never tried to create a com0 or an lpt0.
func TestValidatePackageNameAllowsNamesThatMerelyResembleDeviceNames(t *testing.T) {
	valid := []string{
		"console",     // longer than CON
		"common",      // longer than COM, and not COM<digit>
		"connect",     // longer than CON
		"auxiliary",   // longer than AUX
		"nullish",     // longer than NUL
		"com0",        // COM0 is not a device: Windows numbers serial ports from 1
		"com10",       // two digits, so not on the list either
		"lpt0",        // LPT0 is not a device: parallel ports number from 1 too
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

// TestValidatePackageNameDeviceRuleTrimsTheStem pins the trailing-character trim
// inside isWindowsReservedDeviceName: the device lookup sees the part before the
// first dot with trailing spaces trimmed off it, so "con .txt" is refused as a
// device name. Without the trim the lookup sees "con " and misses, and the
// trailing dot-or-space rule does not cover it either, because the last
// character of "con .txt" is "t".
//
// What the "con .txt" row is, precisely: a reservation lnpm chose, not a
// behaviour anyone here established. Nothing consulted documents what Windows
// resolves "con .txt" to, and Windows CI run 32823717266 never created a
// "con .txt" of any shape. Note in particular that "Win32 strips trailing spaces
// and dots off a path component" does not get you there — that component ends in
// "txt", so a component-level strip leaves it untouched. What #326 had was two
// documented hazards on either side of the shape: Microsoft documents that a
// device name resolves with an extension appended, and separately advises never
// ending a name with a space or a period. Reserving the shape between them was
// judged cheaper than being wrong about it on a platform this repo cannot run
// interactively.
//
// The "con." and "con " rows differ only in that their first step is measured:
// the same run watched "foo.", "foo ", "foo.." and "foo. " each resolve to one
// existing "foo", so a trailing dot or space coming off a whole component is a
// behaviour, not a reading. What the stem then resolves to is the same unprobed
// step as above — and both rows are refused by the trailing dot-or-space rule in
// any case, which is what the next paragraph is about.
//
// Every row asserts the *device* rule fired, not merely that something did.
// "con." and "con " are refused by the trailing dot-or-space rule as well, so a
// row checking only for a non-nil error would stay green with the trim removed.
func TestValidatePackageNameDeviceRuleTrimsTheStem(t *testing.T) {
	rejected := []struct {
		name string
		desc string
	}{
		{"con .txt", "space before the dot: reserved by judgement, never probed"},
		{"con.", "trailing dot after the device name"},
		{"con ", "trailing space after the device name"},
		{"nul .log", "not only con"},
		{"@org/con .txt", "scoped, on the name segment"},
		{"@org/nul ", "scoped, trailing space on the name segment"},
	}
	for _, tc := range rejected {
		err := ValidatePackageName(tc.name)
		if err == nil {
			t.Errorf("ValidatePackageName(%q) = nil, want error (%s)", tc.name, tc.desc)
			continue
		}
		if !strings.Contains(err.Error(), "reserved device name") {
			t.Errorf("ValidatePackageName(%q) = %v, want the device rule to fire (%s)", tc.name, err, tc.desc)
		}
		if !strings.Contains(err.Error(), tc.name) {
			t.Errorf("ValidatePackageName(%q) error %q does not name the package", tc.name, err)
		}
	}

	// The other direction: trimming the stem must not widen the device list. A
	// stem that only ends in a space is still whatever it was before the space.
	//
	// The leading-space rows pin the trim as trailing-only. They are not a claim
	// that Windows keeps a leading space — nothing here measured that — they are
	// what stops the trim being written as strings.TrimSpace, which would trim
	// both ends and quietly start refusing names #326 never argued about.
	valid := []string{
		"con fig",
		"connect .js",
		"com10 .js",
		" con",
		" con.txt",
	}
	for _, name := range valid {
		if err := ValidatePackageName(name); err != nil {
			t.Errorf("ValidatePackageName(%q) = %v, want nil", name, err)
		}
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
// "@ns./pkg" does not end in a dot, but its first path component "@ns." does.
// That Windows strips *that* component is reasoning rather than a measurement,
// and the two halves are worth keeping apart. The run only ever put the dot on
// the *name* segment: it created "@ns/pkg." and left an "@ns" behind. It never
// created an "@ns./pkg". The scope rows follow from the strip being a property
// of every path component Windows parses, which is read from the documentation
// rather than run.
//
// Measured — moving the check onto the whole name turns exactly those two rows
// red and nothing else.
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

// TestValidatePackageNameRejectsUppercase pins the first of #327's two rules.
// npm forbids an uppercase letter in a new package name, and lnpm now follows
// that rule.
//
// What this rests on is npm parity, not a reproduced failure. #327 reasoned that
// "MyPkg" and "mypkg" collide to one .lnpm directory on a case-insensitive
// filesystem — macOS APFS-insensitive or HFS+, or NTFS — while the lock file
// holds two rows, so unlinking one destroys the other's content. That
// consequence was never observed: no such filesystem was available to the audit,
// to the maintainer's environment, or to this one. Only the name acceptance was
// proven, by calling the validator.
//
// The rule stands anyway, and stands on the parity rather than on the
// consequence: npm would refuse to publish any of the names below, so a package
// that carries one is already outside what the ecosystem accepts. That the rule
// also makes the unproven collision impossible to construct is a consequence of
// it, not the evidence for it.
//
// The "MyPkg"/"mypkg" pair is the one the issue body names, and it is asserted
// as a pair — one rejected, the other accepted — because a rule that refused
// both would close the collision by making the name unusable, which is not what
// npm does.
func TestValidatePackageNameRejectsUppercase(t *testing.T) {
	rejected := []struct {
		name string
		desc string
	}{
		{"MyPkg", "the pair from the issue body: MyPkg against mypkg"},
		{"Lodash", "a single leading capital"},
		{"my-PKG", "capitals away from the start"},
		{"myPkg", "an interior capital, which is the common accident"},
		{"@Org/my-pkg", "scoped, capital in the scope segment"},
		{"@org/MyPkg", "scoped, capital in the name segment"},
		// Not an ASCII rule. U+00C9 lower-cases to U+00E9, so the same two
		// spellings of one name exist outside A-Z and are refused there too.
		{"caf\u00c9", "a non-ASCII capital: U+00C9 lower-cases to U+00E9"},
	}
	for _, tc := range rejected {
		err := ValidatePackageName(tc.name)
		if err == nil {
			t.Errorf("ValidatePackageName(%q) = nil, want error (%s)", tc.name, tc.desc)
			continue
		}
		// Same requirement the #325 and #326 rules carry: the message has to name
		// the offender, because it comes out of a manifest the reader did not
		// necessarily write.
		if !strings.Contains(err.Error(), tc.name) {
			t.Errorf("ValidatePackageName(%q) error %q does not name the package", tc.name, err)
		}
		// And it has to say whose rule this is. Rejecting a name a user has been
		// publishing for months is only defensible if the message says the
		// registry would refuse it too.
		if !strings.Contains(err.Error(), "npm") {
			t.Errorf("ValidatePackageName(%q) error %q does not cite npm's rule", tc.name, err)
		}
	}

	// The other half of the pair. Lower-casing the name is the fix the message
	// offers, so the lower-cased form has to be accepted for the advice to be
	// worth anything.
	valid := []string{
		"mypkg",
		"lodash",
		"my-pkg",
		"@org/my-pkg",
		"caf\u00e9", // U+00E9, the lower-case of the rejected row above
	}
	for _, name := range valid {
		if err := ValidatePackageName(name); err != nil {
			t.Errorf("ValidatePackageName(%q) = %v, want nil", name, err)
		}
	}
}

// TestValidatePackageNameAllowsCharactersThatHaveNoCase guards the uppercase
// rule from over-reach. It is a rule about letters that have a lower-case form,
// so digits, punctuation and scripts without a case distinction are untouched.
// Every row here validates against HEAD before #327 as well: this exists to
// catch an over-broad fix, not to prove the fix.
func TestValidatePackageNameAllowsCharactersThatHaveNoCase(t *testing.T) {
	valid := []string{
		"pkg2",
		"my-pkg.v2",
		"under_score",
		"@my.scope/pkg-1",
		// Scripts with no case distinction at all.
		"日本語",
		"שלום",
	}
	for _, name := range valid {
		if err := ValidatePackageName(name); err != nil {
			t.Errorf("ValidatePackageName(%q) = %v, want nil", name, err)
		}
	}
}

// TestValidatePackageNameRejectsNamesNotInNFC pins the second of #327's two
// rules, and it is the half a reader cannot check by eye: the two spellings
// below render identically in every editor and terminal, and differ only in
// their bytes.
//
//   - NFC: "caf" + U+00E9 (LATIN SMALL LETTER E WITH ACUTE), five bytes.
//   - NFD: "cafe" + U+0301 (COMBINING ACUTE ACCENT), six bytes.
//
// Like the case rule, the collision this forecloses is reasoned rather than
// observed — HFS+ stores names in a decomposed form and would fold both to one
// directory, and no filesystem that does so was available here. What is proven
// is the acceptance: before #327 both spellings validated, so two rows could
// name what a user reads as one package.
//
// Both spellings are written as escapes on purpose. A literal "café" in this
// file would be whatever normalisation the editor that saved it applied, which
// is exactly the ambiguity the rule exists to remove.
//
// The rejection is the validator's guarantee, not the user's experience: a name
// read out of a package.json is normalised before it is validated, so an NFD
// manifest publishes fine and stores its NFC spelling. See
// TestReadPackageJSONNormalizesTheNameToNFC. This rejection is what a name that
// never went through that ingestion can still hit — a database row written
// before #327, most of all.
func TestValidatePackageNameRejectsNamesNotInNFC(t *testing.T) {
	const nfc = "caf\u00e9"  // "caf" + LATIN SMALL LETTER E WITH ACUTE
	const nfd = "cafe\u0301" // "cafe" + COMBINING ACUTE ACCENT

	if err := ValidatePackageName(nfc); err != nil {
		t.Errorf("ValidatePackageName(NFC café) = %v, want nil", err)
	}
	err := ValidatePackageName(nfd)
	if err == nil {
		t.Fatalf("ValidatePackageName(NFD café) = nil, want error")
	}
	if !strings.Contains(err.Error(), nfd) {
		t.Errorf("ValidatePackageName(NFD café) error %q does not name the package", err)
	}
	// The message cannot rely on the name looking wrong, because it does not.
	// It has to say what is wrong with it.
	if !strings.Contains(err.Error(), "NFC") {
		t.Errorf("ValidatePackageName(NFD café) error %q does not say the name is not in NFC", err)
	}

	// Scoped, on either segment: the rule is about the whole name, so a
	// decomposed scope is refused as readily as a decomposed name.
	scoped := []string{
		"@org/cafe\u0301",
		"@cafe\u0301/pkg",
	}
	for _, name := range scoped {
		if err := ValidatePackageName(name); err == nil {
			t.Errorf("ValidatePackageName(%q) = nil, want error", name)
		}
	}

	// The other direction: a name with no decomposable character is untouched,
	// and so is one already composed. Every row validates against HEAD before
	// #327 as well.
	valid := []string{
		"my-pkg",
		"@org/my-pkg",
		"日本語",
		"@org/caf\u00e9",
		// A combining mark with no precomposed form is already NFC: there is no
		// single character for "b with acute" to compose into. The rule is
		// "equal to its own NFC form", not "free of combining marks".
		"pkg-b\u0301",
	}
	for _, name := range valid {
		if err := ValidatePackageName(name); err != nil {
			t.Errorf("ValidatePackageName(%q) = %v, want nil", name, err)
		}
	}
}

// TestNormalizePackageName pins the transformation half of #327 on its own,
// away from any caller. Composing is the whole of it: NFC is idempotent, it
// leaves an ASCII name alone byte for byte, and it does not touch case — a
// normaliser that also lower-cased would turn the uppercase rule from a refusal
// into a silent rewrite, which is not what was decided.
func TestNormalizePackageName(t *testing.T) {
	cases := []struct {
		in   string
		want string
		desc string
	}{
		{"cafe\u0301", "caf\u00e9", "NFD composes to NFC"},
		{"caf\u00e9", "caf\u00e9", "NFC is a fixed point"},
		{"my-pkg", "my-pkg", "ASCII is untouched"},
		{"@org/cafe\u0301", "@org/caf\u00e9", "scoped, on the name segment"},
		{"@cafe\u0301/pkg", "@caf\u00e9/pkg", "scoped, on the scope segment"},
		{"MyPkg", "MyPkg", "case is not normalisation's business"},
		{"", "", "an empty name is left empty for the validator to reject"},
	}
	for _, tc := range cases {
		if got := normalizePackageName(tc.in); got != tc.want {
			t.Errorf("normalizePackageName(%q) = %q, want %q (%s)", tc.in, got, tc.want, tc.desc)
		}
	}
}

// TestValidatePackageNameForRemovalAcceptsUppercaseAndNonNFCNames is the same
// argument as the two waivers above it, for #327's two rules. Neither is
// retroactive and neither is a path-safety rule.
//
// A project linked before #327 can hold .lnpm/MyPkg, or a .lnpm entry whose name
// is decomposed, plus a lock entry naming it. Enforcing the new rules on the way
// out would make that entry permanent: 'lnpm remove' would refuse it and
// 'lnpm remove --all' would skip it on every future run, with no supported way
// to get rid of it.
//
// The NFC rule carries a second obligation the other four do not, and it is the
// one worth stating: the removal path must not *normalise* the name either. An
// entry stored as .lnpm/"cafe"+U+0301 is a directory of that literal name on
// every filesystem this repo runs on, and a removal that composed the argument
// first would go looking for a sibling that does not exist and report success
// having deleted nothing. Waiving a rule and applying a transformation are
// opposite things here; only the first is correct.
//
// The waiver cannot widen the path surface, because neither rule guarded it.
// What keeps a removal inside the project is the "."/".." segment check, the
// absolute-path check, the backslash check and the two-segment limit, and
// TestValidatePackageNameForRemovalStillRejectsUnsafeNames asserts all four
// still fire. Neither rule is a route: case and composition are properties of
// the letters in a segment, and no upper-case form and no canonical
// decomposition produces "/", "\" or a "." segment — so a name the waiver newly
// admits differs from an already-admitted one only in the spelling of its
// letters, and resolves to a child of .lnpm exactly as that one does.
func TestValidatePackageNameForRemovalAcceptsUppercaseAndNonNFCNames(t *testing.T) {
	accepted := []string{
		"MyPkg",
		"Lodash",
		"my-PKG",
		"@Org/my-pkg",
		"@org/MyPkg",
		"caf\u00c9",
		"cafe\u0301",
		"@org/cafe\u0301",
		"@cafe\u0301/pkg",
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
// Waiving the five reservations cannot widen that surface, because not one of
// them ever guarded it: traversal is stopped by the "."/".." segment check, the
// absolute path check, the backslash check and the segment count, and this
// asserts every one of them still fires on the removal path.
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
