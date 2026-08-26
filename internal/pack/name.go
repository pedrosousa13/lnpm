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
// It reserves two more shapes for Windows' sake, on every platform, so a name
// published from Linux stays portable there (#326): a segment naming a DOS
// device, and a segment ending in a dot or a space. The two are not backed
// alike: the trailing-character rule has reproduced silent directory sharing on
// Windows CI, while the device rule has a reproduced refusal for NUL alone and
// reserves the other twenty-one names. See windowsReservedDeviceNames and the
// trailing-character check in validatePackageName for what each one is and is
// not backed by.
//
// It rejects an uppercase letter, which npm forbids in a new package name
// (#327). That one rests on the parity and on nothing else: "MyPkg" and "mypkg"
// were both accepted, which was proven, and the collision that would follow on a
// case-insensitive filesystem was reasoned rather than observed. Following npm
// is what makes the unreproduced half moot instead of deferred.
//
// This validates a name at the boundary it is presented at; it revalidates
// nothing already on disk. A .lnpm or a store populated before these rules can
// still hold an entry that breaks one, which is why the reap sweeps stay narrow
// and why removal goes through ValidatePackageNameForRemoval.
func ValidatePackageName(name string) error {
	return validatePackageName(name, true)
}

// ValidatePackageNameForRemoval is ValidatePackageName with four reservations
// waived and nothing else changed: the leading-dot rule (#325), the Windows
// device-name rule and the trailing dot-or-space rule (both #326), and the
// uppercase rule (#327). Use it on paths that take a package away; creation,
// publish, store and pack paths use the strict form.
//
// The waiver exists because none of the four is retroactive. A project linked
// before #325 can hold .lnpm/.hidden-pkg and a lock entry naming it; a project
// linked on Linux before #326 can hold .lnpm/con or .lnpm/foo., which are
// perfectly ordinary distinct directories there; a project linked before #327
// can hold .lnpm/MyPkg, which every filesystem lnpm runs on today treats as an
// ordinary name. Enforcing a new rule on the way out would make such an entry
// permanent: 'lnpm remove' would refuse it and 'lnpm remove --all' would skip it
// on every future run, with no supported way to get rid of it.
//
// Waiving them cannot widen the path surface, because not one of the four ever
// guarded that surface. What keeps a removal inside the project is the "."/".."
// segment check, the absolute-path check, the backslash check and the
// two-segment limit, and every one of those still runs here. A leading dot, a
// device name, a trailing dot or space and an uppercase letter are all names
// rather than routes: ".hidden-pkg", "con", "foo.", "foo " and "MyPkg" each
// resolve to a child of .lnpm exactly like "hidden-pkg" does.
//
// The uppercase waiver is the easiest of the four to check, and worth checking
// rather than asserting. The names it newly admits are exactly those that differ
// from their own lower-cased form. No path metacharacter has a case - "/", "\\",
// "." and ":" are each their own lower-case - so a name admitted by this waiver
// differs from an already-admitted one only in the spelling of its letters, and
// its segment structure is identical. "C:\\evil" is refused by the backslash
// check exactly as "c:\\evil" is; "@Org/.." is refused by the "."/".." segment
// check exactly as "@org/.." is.
//
// The waiver is wider than those four shapes, though, and the extra case is
// worth naming because it is not obvious: "@../pkg" is rejected by the strict
// form via the dot rule, since the "."/".." segment check sees the segment as
// "@.." rather than "..". Removal therefore accepts it. It stays
// contained because "@.." is a literal path component that filepath.Clean does
// not collapse, so it resolves to a child of .lnpm like any other scope - run
// and confirmed, and pinned on every platform by
// TestScopeNamedLikeATraversalIsAcceptedOnRemovalButStaysUnderLnpm, with
// TestUnlinkContainsAScopeNamedLikeATraversal asserting it against a real
// filesystem on Unix. Windows will not create a directory named "@.." at all,
// which is the same property from the other side.
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
// None of those is reached by a leading dot, a device name, a trailing dot or
// space, or an uppercase letter that the other checks would not already have
// caught.
//
// That is the whole claim. This is not a "safer because removal is safer"
// argument — an unlink is a destructive operation and the traversal checks are
// load-bearing here, which is what TestValidatePackageNameForRemovalStillRejectsUnsafeNames
// and TestUnlinkStillRefusesATraversingName assert.
func ValidatePackageNameForRemoval(name string) error {
	return validatePackageName(name, false)
}

// windowsReservedDeviceNames is the classic DOS device-name set #326 asked for,
// lower-cased for a case-folded lookup. Windows resolves these in every
// directory, so a path component named after one is not an ordinary name there.
//
// It is deliberately not Microsoft's whole list, and the gap is worth naming so
// nobody reads the map as complete: CONIN$ and CONOUT$ are documented device
// names too, as are the superscript spellings COM¹-COM³ and LPT¹-LPT³, and none
// of them is here. Nothing rules them out later; the issue specified the
// classic set and that is what this is.
//
// Honest about what backs this. Windows CI run 32823717266 (windows-latest)
// probed nine names with os.MkdirAll: CON, NUL, AUX, PRN, COM1, LPT1, con,
// con.js and NUL.txt. Seven of those spell an entry in this map — CON and con
// being one entry, since the lookup folds case — while con.js and NUL.txt are
// extension-bearing forms that no entry here spells and that
// isWindowsReservedDeviceName reduces to one. Every probe created an ordinary
// directory except bare NUL, which was refused with "The system cannot find the
// path specified."
//
// So one entry of the twenty-two, nul, is a measured failure. Five more — con,
// aux, prn, com1, lpt1 — were probed and created fine, and the remaining sixteen
// were never probed at all. The list is therefore a portability reservation
// drawn from Microsoft's documentation rather than a fix for a reproduced
// failure. The trailing dot-or-space rule below is the half with reproduced
// corruption behind it.
//
// The reservation is not free, and the cost was accepted rather than overlooked:
// con, aux, prn, nul and com1 are real packages published on registry.npmjs.org.
// Established by requesting each name from registry.npmjs.org on 2026-08-25 —
// all five returned HTTP 200, and lpt1 returned 404. What that costs is not an
// install: lnpm has no registry client and never fetches from npm. It is that a
// local package whose package.json carries one of those names can no longer be
// linked, stored, packed or published through lnpm.
var windowsReservedDeviceNames = map[string]bool{
	"con": true, "prn": true, "aux": true, "nul": true,
	"com1": true, "com2": true, "com3": true, "com4": true, "com5": true,
	"com6": true, "com7": true, "com8": true, "com9": true,
	"lpt1": true, "lpt2": true, "lpt3": true, "lpt4": true, "lpt5": true,
	"lpt6": true, "lpt7": true, "lpt8": true, "lpt9": true,
}

// isWindowsReservedDeviceName reports whether a path component names a DOS
// device under the rule lnpm reserves against: take the part before the first
// dot, trim trailing spaces off it, and match that case-folded against the map
// above. So "con.js", "NUL.txt" and "con .txt" are all refused here, while
// "foo.con" is not, and neither is "console" or "com10".
//
// Ignoring the extension and trimming the stem are not backed alike, and reading
// them as one rule is what makes this look better established than it is.
//
// Ignoring the extension is documentation-derived. Microsoft documents the
// device names and documents that they are resolved even when an extension is
// appended. It is not reproduced here, and the one measurement in reach points
// the other way for the operation lnpm cares about: on Windows CI run
// 32823717266 (windows-latest) os.MkdirAll created "con.js" and "NUL.txt" as
// ordinary directories, and bare "NUL" was the only one of the nine probed names
// it refused. The maintainer kept the rule as a portability reservation knowing
// that.
//
// Trimming trailing spaces off the stem is a judgement rather than a reading.
// Nothing consulted documents what Windows resolves "con .txt" to, and that run
// never probed a "con .txt" of any shape, so no source here says the shape is a
// device. What Microsoft does document is the two hazards either side of it: a
// device name resolves with an extension appended, and a name should never be
// ended with a space or a period. Note that neither one reaches this shape on its
// own — a component-level trailing strip does nothing to "con .txt", whose last
// three characters are "txt". #326 reserved it anyway, on the grounds that a
// stem-then-space-then-extension name is close enough to both documented hazards
// that refusing it is cheaper than being wrong about it on a platform this repo
// cannot run interactively.
//
// One implementation note that is neither: only spaces need trimming, not dots.
// Taking the part before the *first* dot already leaves a stem that cannot end in
// one, so trimming dots as well would be dead work. That one is provable from the
// two lines below rather than from any document.
func isWindowsReservedDeviceName(segment string) bool {
	stem := segment
	if dot := strings.IndexByte(stem, '.'); dot >= 0 {
		stem = stem[:dot]
	}
	stem = strings.TrimRight(stem, " ")
	return windowsReservedDeviceNames[strings.ToLower(stem)]
}

// validatePackageName is the single implementation behind both entry points, so
// the two cannot drift. strict selects the three rules that separate them —
// the leading dot (#325), the Windows device name and the trailing dot or space
// (both #326). Every other check runs either way; see
// ValidatePackageNameForRemoval for why those three and no others are waived.
func validatePackageName(name string, strict bool) error {
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

		if !strict {
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

		// The two rules below test p, the raw segment, and deliberately NOT the
		// "@"-stripped seg above: what Windows sees is the literal path
		// component, so that is what has to be tested.
		//
		// For the device rule the difference is load-bearing. The directory
		// created for scope "@con" is named "@con", which is not a device name,
		// so "@ns/con" must be refused and "@con/pkg" allowed —
		// TestValidatePackageNameDeviceRuleDoesNotStripTheScopeAtSign pins both
		// directions so the second is not read as an oversight, and swapping p
		// for seg here turns four of its and the resemblance test's rows red.
		//
		// For the trailing rule the difference is inert, and saying so is more
		// use than implying otherwise: TrimPrefix only removes a leading "@", so
		// it cannot change a segment's last character, and the one segment where
		// it could ("@" alone) is already refused by the scope check above.
		// Measured — swapping p for seg on the trailing check leaves the whole
		// package green. What the tests do pin about that rule is that it runs
		// per segment: applying it to the whole name instead turns the
		// "@ns./pkg" and "@ns /pkg" rows red.
		if isWindowsReservedDeviceName(p) {
			return fmt.Errorf("invalid package name %q: %q is a Windows reserved device name; "+
				"lnpm reserves CON, PRN, AUX, NUL, COM1-COM9 and LPT1-LPT9 on every platform, "+
				"ignoring case and any extension, to keep names portable to Windows; "+
				"pick another name", name, p)
		}

		// Windows strips a trailing dot or space from a path component, so
		// "foo." and "foo " both resolve to "foo" — measured on windows-latest,
		// where os.MkdirAll("foo.") succeeds and silently produces a directory
		// named "foo". Two entries would then share one directory, and the
		// create returns no error to detect it by.
		if strings.HasSuffix(p, ".") || strings.HasSuffix(p, " ") {
			return fmt.Errorf("invalid package name %q: a name segment must not end with a dot or a space; "+
				"Windows strips both, so %q would silently share a directory with %q", name,
				p, strings.TrimRight(p, ". "))
		}
	}

	// #327's rule is last, and it is the only one here that is a property of the
	// whole name rather than of a segment: case does not change at a "/".
	// Running it after the loop is also what keeps the #326 messages intact.
	// "CON" breaks both the device rule and the case rule, and "a Windows
	// reserved device name" is the message worth printing, because `use "con"
	// instead` would be advice to a name lnpm also refuses.
	if !strict {
		return nil
	}

	// npm forbids an uppercase letter in a new package name. lnpm follows that
	// rule rather than inventing one, and the parity is the justification: a
	// name lnpm refuses here is a name the registry would have refused too.
	//
	// What #327 wanted it for is a collision nobody reproduced. "MyPkg" and
	// "mypkg" were both accepted, which was proven by calling this function, and
	// on a case-insensitive filesystem - macOS APFS-insensitive or HFS+, or NTFS
	// - they resolve to one .lnpm directory while the lock file holds two rows,
	// so unlinking one RemoveAlls the other's content. That second half was
	// reasoned and never observed: no case-insensitive filesystem was available
	// to the audit, and none was available where this was written. Following npm
	// is what makes the question moot rather than deferred - if "MyPkg" is never
	// a valid name there is no pair to collide - but the rule stands on the
	// parity, not on the consequence.
	//
	// The test is "differs from its own lower-cased form", not "contains A-Z":
	// U+00C9 and U+00E9 are the same two spellings of one name outside ASCII,
	// and a character with no lower-case form - a digit, a CJK ideograph - is
	// untouched by it.
	if lower := strings.ToLower(name); lower != name {
		return fmt.Errorf("invalid package name %q: a package name must not contain uppercase letters; "+
			"npm forbids them in new package names and lnpm follows the same rule, so that two "+
			"spellings of one name cannot become two entries on a case-insensitive filesystem; "+
			"use %q instead", name, lower)
	}

	return nil
}
