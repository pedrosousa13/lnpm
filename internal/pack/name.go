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
// It rejects two more shapes for Windows' sake, on every platform, so a package
// published from Linux is not unusable there (#326): a segment naming a DOS
// device, and a segment ending in a dot or a space. See
// windowsReservedDeviceNames and the trailing-character check in
// validatePackageName for what each one is and is not backed by.
//
// This validates a name at the boundary it is presented at; it revalidates
// nothing already on disk. A .lnpm or a store populated before these rules can
// still hold an entry that breaks one, which is why the reap sweeps stay narrow
// and why removal goes through ValidatePackageNameForRemoval.
func ValidatePackageName(name string) error {
	return validatePackageName(name, true)
}

// ValidatePackageNameForRemoval is ValidatePackageName with three reservations
// waived and nothing else changed: the leading-dot rule (#325), the Windows
// device-name rule and the trailing dot-or-space rule (both #326). Use it on
// paths that take a package away; creation, publish, store and pack paths use
// the strict form.
//
// The waiver exists because none of the three is retroactive. A project linked
// before #325 can hold .lnpm/.hidden-pkg and a lock entry naming it; a project
// linked on Linux before #326 can hold .lnpm/con or .lnpm/foo., which are
// perfectly ordinary distinct directories there. Enforcing a new rule on the way
// out would make such an entry permanent: 'lnpm remove' would refuse it and
// 'lnpm remove --all' would skip it on every future run, with no supported way
// to get rid of it.
//
// Waiving them cannot widen the path surface, because not one of the three ever
// guarded that surface. What keeps a removal inside the project is the "."/".."
// segment check, the absolute-path check, the backslash check and the
// two-segment limit, and every one of those still runs here. A leading dot, a
// device name and a trailing dot are all names rather than routes: ".hidden-pkg",
// "con" and "foo." each resolve to a child of .lnpm exactly like "hidden-pkg"
// does.
//
// The waiver is wider than those three shapes, though, and the extra case is
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
// None of those is reached by a leading dot, a device name or a trailing dot or
// space that the other checks would not already have caught.
//
// That is the whole claim. This is not a "safer because removal is safer"
// argument — an unlink is a destructive operation and the traversal checks are
// load-bearing here, which is what TestValidatePackageNameForRemovalStillRejectsUnsafeNames
// and TestUnlinkStillRefusesATraversingName assert.
func ValidatePackageNameForRemoval(name string) error {
	return validatePackageName(name, false)
}

// windowsReservedDeviceNames is Microsoft's documented list of DOS device
// names, lower-cased for a case-folded lookup. Windows resolves these in every
// directory, so a path component named after one is not an ordinary name there.
//
// Honest about what backs this. Windows CI run 32823717266 (windows-latest)
// created a directory for every one of these that it tried — CON, AUX, PRN,
// COM1, LPT1, con, con.js and NUL.txt all landed fine. **Only NUL was refused**,
// with "The system cannot find the path specified." So this list is a
// portability reservation matching Microsoft's documentation, not a fix for a
// reproduced failure; NUL alone is the measured one. The trailing dot-or-space
// rule below is the half with reproduced corruption behind it.
//
// The reservation is not free, and the cost was accepted rather than overlooked:
// con, aux, prn, nul and com1 are real packages published on registry.npmjs.org
// (lpt1 is not), and this makes every one of them uninstallable through lnpm.
var windowsReservedDeviceNames = map[string]bool{
	"con": true, "prn": true, "aux": true, "nul": true,
	"com1": true, "com2": true, "com3": true, "com4": true, "com5": true,
	"com6": true, "com7": true, "com8": true, "com9": true,
	"lpt1": true, "lpt2": true, "lpt3": true, "lpt4": true, "lpt5": true,
	"lpt6": true, "lpt7": true, "lpt8": true, "lpt9": true,
}

// isWindowsReservedDeviceName reports whether a path component names a DOS
// device. Windows ignores the extension when it resolves one, so the test is on
// the part before the first dot: "con.js" and "NUL.txt" are the device, while
// "foo.con" is not, and neither is "console" or "com10".
func isWindowsReservedDeviceName(segment string) bool {
	stem := segment
	if dot := strings.IndexByte(stem, '.'); dot >= 0 {
		stem = stem[:dot]
	}
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
				"ignoring case and any extension, so a package cannot be unusable on Windows", name, p)
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
	return nil
}
