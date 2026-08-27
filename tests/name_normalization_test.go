package tests

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pedrosousa13/lnpm/pkg/lockfile"
)

// TestPublishAndAddComposeADecomposedNameEverywhereItIsWritten is the
// integration half of #327's composition rule. internal/pack's
// TestReadPackageJSONNormalizesTheNameToNFC pins the transformation at the one
// point it happens, on the struct readPackageJSON returns; readPackageJSON's own
// comment then argues *structurally* that the store directory, the database row
// and the lock file all inherit that spelling because all three flow from it.
// This test is what turns that argument into a measurement: it publishes a
// package whose package.json name is decomposed, adds it to a project, and
// asserts the composed spelling on each of the three surfaces the comment names.
//
// The gap it closes was found by mutation testing the branch on 2026-08-27:
// neutering the composition in readPackageJSON turned exactly one test red -
// the unit test at the composition point - and left the whole tests package
// green. Nothing downstream noticed, so a composition that moved or was dropped
// on the way to the store would have shipped against a green integration suite.
//
// All three surfaces are reachable from this package's fixtures and all three
// are asserted here:
//
//   - the store directory, read two ways. The database row's StorePath is the
//     path lnpm recorded, so its parent's base name is lnpm's own bytes and is
//     platform-independent; the os.ReadDir of the store root is what the
//     filesystem hands back, which is the half that could differ on a
//     normalizing filesystem. Every filesystem CI runs on - ext4, APFS, NTFS -
//     preserves the bytes it was given. HFS+ decomposes them and would fail the
//     ReadDir row for a reason that has nothing to do with lnpm; that is read
//     from Apple's documentation, not run here, and no CI runner uses HFS+.
//   - the database row, via GetPackageByName. The lookup is by exact key, so a
//     row written decomposed is not found by the composed name at all.
//   - the lnpm.lock entry, asserted as the whole entry set rather than as a
//     lookup, so a second decomposed row beside the composed one would fail
//     just as a missing composed one does.
//
// The two spellings are written as escapes because they are indistinguishable
// any other way: "caf"+U+00E9 and "cafe"+U+0301 render identically. The name is
// lowercase on purpose - #327's other rule refuses an uppercase letter, and a
// mixed-case fixture would be refused before composition was ever reached.
//
// Only one spelling is ever created on disk, which the "Some evidence only CI
// can produce" section of docs/agents/verification-discipline.md requires:
// writing both into one directory is unsatisfiable on a filesystem that folds
// them together, regardless of whether lnpm is correct.
//
// Measured on 2026-08-27 on Linux, go vet ./... exiting 0 first in each
// direction and each run read for the FAIL <package> result line with a
// duration rather than for the absence of output:
//
//   - Neuter the composition alone, assigning the raw name in readPackageJSON.
//     Two failures in the whole suite: this test, the only one in tests, which
//     prints FAIL github.com/pedrosousa13/lnpm/tests 24.127s, and
//     TestReadPackageJSONNormalizesTheNameToNFC in internal/pack. Every other
//     package prints ok. Note that a plain self-assignment does not build here -
//     go vet reports "self-assignment of pkg.Name" and exits 1 - so the neuter
//     has to go through a local, which is the "a revert experiment that never
//     built" trap the discipline doc names.
//
//     It fails at the publish step, not at an assertion below, and that is
//     worth reading rather than counting: the very next line validates the
//     name, and the NFC rule in ValidatePackageName refuses the decomposed
//     spelling, so a publish that composes nothing never reaches the store at
//     all. The message is "Failed to publish café: validation failed: invalid
//     package.json: invalid package name "café": the name is not in Unicode
//     normalization form NFC ...".
//
//   - Neuter the composition AND waive the NFC rule in validatePackageName, so
//     the decomposed name reaches the store. This is the direction that moves
//     the assertions themselves rather than the refusal in front of them. Two
//     rows red here: the store root lists ["cafe"+U+0301] against a want of
//     ["caf"+U+00E9], and there is no database row under the composed name at
//     all. The database row is a Fatalf, so the recorded-StorePath row and the
//     lock row below it do not run under this direction - saying so is more use
//     than implying four rows were measured. Elsewhere in the suite:
//     TestValidatePackageNameRejectsNamesNotInNFC,
//     TestValidatePackageNameAdviceIsItselfAValidName and
//     TestReadPackageJSONNormalizesTheNameToNFC in internal/pack, and
//     TestRunDoctorReportsAStoredNameThatIsNotComposed in internal/cli, all of
//     which the waiver rather than this test's subject reaches.
//
//     Under this direction the add is what fails when the assertions are put
//     after it, with "package café not found in store, but the store holds one
//     whose name renders identically and is not in Unicode NFC" - one failure
//     naming no surface. That measurement is why the store assertions sit
//     before the add.
func TestPublishAndAddComposeADecomposedNameEverywhereItIsWritten(t *testing.T) {
	const nfc = "caf\u00e9"  // "caf" + LATIN SMALL LETTER E WITH ACUTE
	const nfd = "cafe\u0301" // "cafe" + COMBINING ACUTE ACCENT

	env := setupTest(t)

	// The manifest carries the decomposed spelling; everything asserted below
	// is expected to carry the composed one.
	env.publishPkg(nfd, "1.0.0", map[string]string{
		"index.js": "module.exports = 'cafe';",
	})

	// The store's two surfaces are asserted before the add, deliberately. An
	// add of a decomposed store entry fails outright - link validates strictly,
	// so packageNotInStoreError is what a caller gets - and asserting through it
	// would report every broken composition as one add failure, saying nothing
	// about which of the three surfaces moved. Measured: see the second
	// direction below.
	//
	// The store directory, as the filesystem reports it.
	storeRoot := filepath.Join(env.StoreDir, "store")
	entries, err := os.ReadDir(storeRoot)
	if err != nil {
		t.Fatalf("read the store root %s: %v", storeRoot, err)
	}
	// Only the directories: the store root also holds the two bookkeeping
	// files New() writes there, .lnpm-content-protected and
	// .lnpm-markers-backfilled, and neither is a package.
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	if len(names) != 1 || names[0] != nfc {
		t.Errorf("store root holds %q, want exactly the composed spelling [%q]", names, nfc)
	}

	// The database row. GetPackageByName looks up by exact key, so a row
	// written decomposed is simply absent under the composed name.
	pkg, err := env.Database.GetPackageByName(nfc)
	if err != nil {
		t.Fatalf("GetPackageByName(%q): %v", nfc, err)
	}
	if pkg == nil {
		t.Fatalf("no database row under the composed name %q; the publish stored an uncomposed spelling", nfc)
	}
	if pkg.Name != nfc {
		t.Errorf("database row name = %q, want the composed spelling %q", pkg.Name, nfc)
	}

	// The store directory, as lnpm recorded it. StorePath is
	// <store>/store/<name>/<hash>, so the name is the parent's base.
	if got := filepath.Base(filepath.Dir(pkg.StorePath)); got != nfc {
		t.Errorf("store directory recorded in the database = %q, want the composed spelling %q", got, nfc)
	}

	// The lock file, which the add is what writes.
	projectDir := env.newProject("cafe-project")
	env.addPkg(projectDir, nfc, false, false)

	// Asserted as the whole entry set, so a decomposed entry beside a composed
	// one fails as loudly as a missing composed one.
	env.AssertLockfile(projectDir, func(lock *lockfile.LockFile) {
		listed := lock.List()
		if len(listed) != 1 || listed[0] != nfc {
			t.Errorf("lnpm.lock holds %q, want exactly the composed spelling [%q]", listed, nfc)
		}
	})
}
