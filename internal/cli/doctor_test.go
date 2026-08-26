package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/config"
	"github.com/pedrosousa13/lnpm/internal/db"
	"github.com/pedrosousa13/lnpm/internal/pack"
)

// TestRunDoctorChecksConfiguredStorePath pins that doctor inspects the store
// resolved from store_path, not the ~/.lnpm default. The configured directory
// is deliberately absent so doctor names it in its "does not exist" line: that
// makes the assertion key on the configured path regardless of whether ~/.lnpm
// happens to exist on the machine running the test.
func TestRunDoctorChecksConfiguredStorePath(t *testing.T) {
	want := filepath.Join(newDoctorStoreConfig(t), "mystore")

	out := captureFailingDoctorStdout(t)

	if !strings.Contains(out, "Store directory does not exist: "+want) {
		t.Errorf("RunDoctor did not check the configured store %q, output was:\n%s", want, out)
	}
	if home, err := os.UserHomeDir(); err == nil {
		if def := filepath.Join(home, ".lnpm"); strings.Contains(out, def) {
			t.Errorf("RunDoctor named the default store %q instead of the configured one, output was:\n%s", def, out)
		}
	}
}

// TestRunDoctorReportsConfiguredStoreHealthy is the acceptance criterion read
// the other way round: with the configured store present, doctor must not
// report it missing. newDoctorStoreConfig redirects the home directory, so the
// ~/.lnpm default is absent and a doctor that ignored store_path would report
// it missing here rather than passing by luck.
func TestRunDoctorReportsConfiguredStoreHealthy(t *testing.T) {
	want := filepath.Join(newDoctorStoreConfig(t), "mystore")
	if err := os.MkdirAll(want, 0755); err != nil {
		t.Fatalf("Failed to create configured store %q: %v", want, err)
	}

	out, err := runDoctor(t, false)

	if strings.Contains(out, "NOT FOUND") {
		t.Errorf("RunDoctor reported the existing configured store %q as missing, output was:\n%s", want, out)
	}
	if err != nil {
		t.Errorf("RunDoctor() = %v for a store that is present, want nil", err)
	}
}

// TestRunDoctorPrefersEnvStoreOverConfig pins that LNPM_STORE still wins over
// store_path, so doctor keeps the resolution order the rest of lnpm uses.
func TestRunDoctorPrefersEnvStoreOverConfig(t *testing.T) {
	dir := newDoctorStoreConfig(t)
	fromConfig := filepath.Join(dir, "mystore")
	fromEnv := filepath.Join(dir, "from-env")
	t.Setenv("LNPM_STORE", fromEnv)

	out := captureFailingDoctorStdout(t)

	if !strings.Contains(out, "Store directory does not exist: "+fromEnv) {
		t.Errorf("RunDoctor did not check the LNPM_STORE path %q, output was:\n%s", fromEnv, out)
	}
	if strings.Contains(out, fromConfig) {
		t.Errorf("RunDoctor checked the configured store %q even though LNPM_STORE was set, output was:\n%s", fromConfig, out)
	}
}

// newDoctorStoreConfig writes a config file setting store_path to a "mystore"
// directory inside a fresh temp dir, points LNPM_CONFIG at it and returns the
// temp dir. The configured directory itself is not created, so the caller
// decides whether doctor should find it.
//
// The home directory is redirected into the same temp dir, so the ~/.lnpm
// default resolves somewhere that does not exist. Without that, a machine
// which happens to have a real ~/.lnpm reports it healthy, and a test asserting
// the configured store was found passes even when doctor ignored the config
// entirely.
//
// config caches the parsed file for the process, so the cache is dropped both
// before the test (another test in this package may have populated it already)
// and after it (so this test's config does not leak into the next one).
func newDoctorStoreConfig(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	storePath := filepath.Join(dir, "mystore")
	if err := os.WriteFile(cfgPath, []byte("store_path: "+storePath+"\n"), 0644); err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	t.Setenv("LNPM_CONFIG", cfgPath)
	t.Setenv("LNPM_STORE", "")   // empty is treated as unset, so config wins
	t.Setenv("HOME", dir)        // os.UserHomeDir on unix
	t.Setenv("USERPROFILE", dir) // os.UserHomeDir on windows

	config.ResetForTesting()
	t.Cleanup(config.ResetForTesting)

	return dir
}

// TestRunDoctorReportsIncompleteStoreEntry covers both halves of doctor's job
// for a store entry lnpm cannot vouch for: it names the directory, and it does
// not repair or delete it. doctor reports and names the fix; acting is somebody
// else's job, and here nobody's but the user's - lnpm cannot tell what is
// inside a gutted entry, so it must not destroy it either.
func TestRunDoctorReportsIncompleteStoreEntry(t *testing.T) {
	dir := newDoctorStoreConfig(t)
	entry := seedUnmarkedEntry(t, dir, "left-pad", "aaa111")
	// A committed entry beside it, so the store reads as one lnpm 2.x wrote
	// rather than one awaiting the legacy migration. Those are different
	// findings with different fixes, and the unmarked entry only means
	// "damaged" in a store that has markers.
	writeCompletenessMarker(t, seedUnmarkedEntry(t, dir, "right-pad", "bbb222"), "bbb222")

	out, err := runDoctor(t, false)
	if err == nil {
		t.Errorf("RunDoctor() = nil for a store holding an incomplete entry, want an error; output was:\n%s", out)
	}

	if !strings.Contains(out, entry) {
		t.Errorf("RunDoctor did not name the incomplete entry, output was:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(entry, "legacy.js")); err != nil {
		t.Errorf("RunDoctor deleted the content of the entry it reported: %v", err)
	}
	if _, err := os.Stat(filepath.Join(entry, ".lnpm-complete")); err == nil {
		t.Error("RunDoctor marked the entry it reported as complete; doctor must report without writing")
	}
}

// TestRunDoctorSkipsStoreSweepWithoutStore pins that the sweep stays behind its
// prerequisite. With no store on the machine there is nothing to sweep, and
// Check 1 has already reported the only thing worth acting on.
func TestRunDoctorSkipsStoreSweepWithoutStore(t *testing.T) {
	newDoctorStoreConfig(t) // the configured store is deliberately not created

	out := captureFailingDoctorStdout(t)

	if strings.Contains(out, "completeness marker") {
		t.Errorf("RunDoctor swept a store that does not exist, output was:\n%s", out)
	}
}

// TestRunDoctorReportsALegacyStoreAsPending covers the store that predates
// completeness markers. Every entry in it fails the completeness check, but the
// fix is not "re-publish your whole store" - it is the one-time migration that
// runs when any command opens the store, and doctor is the one command that
// never does. Reporting it as a warning rather than an issue keeps
// `lnpm doctor && ...` working on a store that is about to migrate itself.
func TestRunDoctorReportsALegacyStoreAsPending(t *testing.T) {
	dir := newDoctorStoreConfig(t)
	entry := seedUnmarkedEntry(t, dir, "left-pad", "aaa111")

	out, err := runDoctor(t, false)
	if err != nil {
		t.Errorf("RunDoctor() = %v for a store awaiting its migration, want nil: that is a warning, not a broken install", err)
	}

	if !strings.Contains(out, "PENDING") {
		t.Errorf("RunDoctor did not report the unmigrated store as pending, output was:\n%s", out)
	}
	if strings.Contains(out, entry) {
		t.Errorf("RunDoctor listed %s as damaged; every entry of an unmigrated store would be listed, and none of them needs re-publishing. Output was:\n%s", entry, out)
	}
	if _, err := os.Stat(filepath.Join(entry, ".lnpm-complete")); err == nil {
		t.Error("RunDoctor performed the migration; doctor must report without writing")
	}
}

// TestRunDoctorReportsAMigrationItCannotRun covers the overlap the branch above
// would otherwise hide: a store that predates markers and holds a directory the
// scan cannot read. The migration withholds its decision for as long as anything
// is unreadable, so "run any command that opens the store" is advice that can
// never work here. doctor has to name the directory count and fail, or the user
// is sent round a loop with nothing telling them why it does not end.
func TestRunDoctorReportsAMigrationItCannotRun(t *testing.T) {
	requirePermissionEnforcement(t)

	dir := newDoctorStoreConfig(t)
	seedUnmarkedEntry(t, dir, "left-pad", "aaa111")
	blocked := filepath.Dir(seedUnmarkedEntry(t, dir, "blocked-pkg", "bbb222"))
	if err := os.Chmod(blocked, 0000); err != nil {
		t.Fatalf("chmod %s: %v", blocked, err)
	}
	t.Cleanup(func() { _ = os.Chmod(blocked, 0755) })

	out, err := runDoctor(t, false)
	if err == nil {
		t.Errorf("RunDoctor() = nil for a migration that cannot run, want an error; output was:\n%s", out)
	}

	if !strings.Contains(out, "could not be read") {
		t.Errorf("RunDoctor did not say what is blocking the migration, output was:\n%s", out)
	}
	if !strings.Contains(out, "Make them readable") {
		t.Errorf("RunDoctor advised a fix without the step that unblocks it, output was:\n%s", out)
	}
}

// TestRunDoctorPassesAStoreOfCommittedEntries is the same check read the other
// way round: an entry the write path committed must not be reported.
func TestRunDoctorPassesAStoreOfCommittedEntries(t *testing.T) {
	dir := newDoctorStoreConfig(t)
	entry := seedUnmarkedEntry(t, dir, "left-pad", "aaa111")
	writeCompletenessMarker(t, entry, "aaa111")

	out, err := runDoctor(t, false)

	if strings.Contains(out, "incomplete") {
		t.Errorf("RunDoctor reported a committed entry as incomplete, output was:\n%s", out)
	}
	if err != nil {
		t.Errorf("RunDoctor() = %v for a store of committed entries, want nil", err)
	}
}

// seedUnmarkedEntry writes a store entry with content and no completeness
// marker - the shape an interrupted gc leaves, and the shape every entry had
// before markers existed - and returns its path.
func seedUnmarkedEntry(t *testing.T, dir, name, hash string) string {
	t.Helper()

	entry := filepath.Join(dir, "mystore", "store", name, hash)
	if err := os.MkdirAll(entry, 0755); err != nil {
		t.Fatalf("create store entry: %v", err)
	}
	if err := os.WriteFile(filepath.Join(entry, "legacy.js"), []byte("legacy"), 0644); err != nil {
		t.Fatalf("write store entry file: %v", err)
	}
	return entry
}

// writeCompletenessMarker marks an entry the way the store's write path does.
// The payload is spelled out rather than taken from the store package, so a
// change to what the marker holds has to be made here too and cannot quietly
// stop this test exercising the accepting branch.
func writeCompletenessMarker(t *testing.T, entry, hash string) {
	t.Helper()

	payload := []byte(`{"schemaVersion":1,"hash":"` + hash + `"}` + "\n")
	if err := os.WriteFile(filepath.Join(entry, ".lnpm-complete"), payload, 0644); err != nil {
		t.Fatalf("write completeness marker: %v", err)
	}
}

// TestRunDoctorReportsTamperedStoreContent is the finding #439 was filed for.
// The store is content-addressed, so an entry's directory name is a claim about
// the bytes inside it, and until now nothing ever checked that claim: during
// #333's reproduction the store was provably poisoned and doctor reported
// "store file integrity... OK" throughout.
//
// The report has to name the package, the version and the path, because none of
// the three is recoverable from the others: the entry is addressed by name and
// content hash, so the version lives only in the database, and an entry holding
// hundreds of files is not actionable without the one that changed.
func TestRunDoctorReportsTamperedStoreContent(t *testing.T) {
	dir := newDoctorStoreConfig(t)
	pkg := seedVerifiableEntry(t, dir, "left-pad", "1.2.3", map[string]string{
		"index.js":    "module.exports = require('./lib/util');",
		"lib/util.js": "module.exports = 'util';",
	})

	tamperWithStoreFile(t, pkg.StorePath, "lib/util.js", "module.exports = 'poisoned';")

	out, err := runDoctor(t, true)
	if err == nil {
		t.Errorf("RunDoctor() = nil for a store holding tampered content, want an error so `lnpm doctor && ...` stops; output was:\n%s", out)
	}

	for _, want := range []string{"left-pad", "1.2.3", "lib/util.js"} {
		if !strings.Contains(out, want) {
			t.Errorf("RunDoctor did not name %q, so the finding is not actionable; output was:\n%s", want, out)
		}
	}
	if strings.Contains(out, "All checks passed") {
		t.Errorf("RunDoctor called a store holding tampered content healthy, output was:\n%s", out)
	}
}

// TestRunDoctorDoesNotFaultProtectedStoreContent is the trap recorded on #439
// read as a test. #333 chmods every stored file to mode &^ 0222, and
// pack.HashFiles folds Mode.Perm() into the package hash, so a content check
// that recomputed that hash from the permissions it finds on disk would fault
// every entry of a perfectly healthy store and advise re-publishing all of it.
//
// seedVerifiableEntry carries that difference deliberately - read-only on disk,
// the published mode in the database - so this passes only for a check that
// compares content and nothing else.
func TestRunDoctorDoesNotFaultProtectedStoreContent(t *testing.T) {
	dir := newDoctorStoreConfig(t)
	seedVerifiableEntry(t, dir, "left-pad", "1.2.3", map[string]string{
		"index.js":    "module.exports = require('./lib/util');",
		"lib/util.js": "module.exports = 'util';",
	})

	out, err := runDoctor(t, true)

	if line := doctorCheckLine(t, out, "Checking store file integrity... "); !strings.Contains(line, "OK") {
		t.Errorf("integrity check reported %q for an untouched store entry, want OK; output was:\n%s", line, out)
	}
	if err != nil {
		t.Errorf("RunDoctor() = %v for a store whose content is intact, want nil", err)
	}
}

// TestRunDoctorReportsAFileNoRowRecords covers the one poisoning shape a
// row-by-row comparison cannot see, and it is the shape that reaches consumers.
//
// store.EntryFiles walks the entry directory and returns everything but the
// completeness marker, and storeFilesForLink starts from that walk and only
// annotates the paths it finds rows for - a file no row records keeps its place
// in the list and is materialised into every project that links the package. A
// check that only iterated the rows would never open it.
func TestRunDoctorReportsAFileNoRowRecords(t *testing.T) {
	dir := newDoctorStoreConfig(t)
	pkg := seedVerifiableEntry(t, dir, "left-pad", "1.2.3", map[string]string{
		"index.js": "module.exports = 'left-pad';",
	})

	writeDoctorFixtureFile(t, pkg.StorePath, "lib/injected.js", "module.exports = 'injected';", 0444)

	out, err := runDoctor(t, true)
	if err == nil {
		t.Errorf("RunDoctor() = nil for an entry holding an injected file, want an error; output was:\n%s", out)
	}

	for _, want := range []string{"left-pad", "1.2.3", "lib/injected.js"} {
		if !strings.Contains(out, want) {
			t.Errorf("RunDoctor did not name %q, output was:\n%s", want, out)
		}
	}
}

// TestRunDoctorDoesNotCountTheCompletenessMarkerAsInjected keeps the check
// above off the one file that is meant to be in an entry and is deliberately
// absent from the rows. The marker belongs to the store rather than to the
// package, which is why store.EntryFiles leaves it out of what a consumer
// receives, and it must be left out here for the same reason.
func TestRunDoctorDoesNotCountTheCompletenessMarkerAsInjected(t *testing.T) {
	dir := newDoctorStoreConfig(t)
	seedVerifiableEntry(t, dir, "left-pad", "1.2.3", map[string]string{
		"index.js": "module.exports = 'left-pad';",
	})

	out, err := runDoctor(t, true)

	if line := doctorCheckLine(t, out, "Checking store file integrity... "); !strings.Contains(line, "OK") {
		t.Errorf("integrity check reported %q for an untouched entry, want OK; output was:\n%s", line, out)
	}
	if err != nil {
		t.Errorf("RunDoctor() = %v for an untouched entry, want nil", err)
	}
}

// TestRunDoctorReportsAnEntryStoredUnderAnotherHash closes the last step
// between the rows and the claim the check exists to test. The rows are
// compared against the package row's content hash, which is a database column;
// what the store asserts is the directory the entry sits in. An entry moved or
// copied into a directory named for other content satisfies every other
// comparison here.
func TestRunDoctorReportsAnEntryStoredUnderAnotherHash(t *testing.T) {
	dir := newDoctorStoreConfig(t)
	pkg := seedVerifiableEntry(t, dir, "left-pad", "1.2.3", map[string]string{
		"index.js": "module.exports = 'left-pad';",
	})

	moved := filepath.Join(filepath.Dir(pkg.StorePath), "ffffffffffffffff")
	if err := os.Rename(pkg.StorePath, moved); err != nil {
		t.Fatalf("move store entry: %v", err)
	}
	writeCompletenessMarker(t, moved, "ffffffffffffffff")
	// Addressed by name and content hash, so re-inserting the same pair updates
	// the record in place and repoints it at the directory the entry was moved
	// to. The content hash is deliberately left alone: that disagreement with
	// the directory name is the whole fixture.
	pkg.StorePath = moved
	if err := openDoctorDB(t).InsertPackage(pkg); err != nil {
		t.Fatalf("repoint package at the moved entry: %v", err)
	}

	out, err := runDoctor(t, true)
	if err == nil {
		t.Errorf("RunDoctor() = nil for an entry stored under another hash, want an error; output was:\n%s", out)
	}

	if !strings.Contains(out, "ffffffffffffffff") && !strings.Contains(out, "ffffffff") {
		t.Errorf("RunDoctor did not name the directory the entry is stored under, output was:\n%s", out)
	}
}

// TestRunDoctorFaultsATamperedRootManifest keeps the carve-out for lnpm's own
// rewrite as narrow as the rewrite is.
//
// store.stripLifecycleScripts returns without writing anything unless the
// manifest has a scripts map holding prepare or prepublish, and what it does
// write is a re-marshalled document. So a stored manifest that is not in that
// form was never rewritten, and a mismatch on it is damage - on the one file
// worth tampering with, since main, bin and postinstall survive the strip
// untouched.
func TestRunDoctorFaultsATamperedRootManifest(t *testing.T) {
	dir := newDoctorStoreConfig(t)
	pkg := seedVerifiableEntry(t, dir, "left-pad", "1.2.3", map[string]string{
		"package.json": `{"name":"left-pad","version":"1.2.3"}`,
	})

	tamperWithStoreFile(t, pkg.StorePath, "package.json", `{"name":"left-pad","version":"1.2.3","bin":{"left-pad":"./pwn.js"}}`)

	out, err := runDoctor(t, true)
	if err == nil {
		t.Errorf("RunDoctor() = nil for a tampered root manifest, want an error; output was:\n%s", out)
	}

	if !strings.Contains(out, "do not hold the content recorded for them") {
		t.Errorf("RunDoctor excused a tampered manifest as one lnpm rewrote itself, output was:\n%s", out)
	}
}

// TestRunDoctorExcusesAManifestTheStripCouldHaveWritten is the same carve-out
// read the other way: a stored manifest that is exactly what the strip emits -
// a re-marshalled document with a scripts map and neither prepare nor
// prepublish left in it - is the one case doctor cannot tell from damage, so it
// is reported as unchecked rather than faulted.
//
// The expected bytes are spelled out rather than produced with encoding/json,
// so a change to how the store re-marshals a manifest has to be made here too
// and cannot quietly stop this test exercising the excusing branch.
func TestRunDoctorExcusesAManifestTheStripCouldHaveWritten(t *testing.T) {
	dir := newDoctorStoreConfig(t)
	pkg := seedVerifiableEntry(t, dir, "left-pad", "1.2.3", map[string]string{
		"package.json": `{"name":"left-pad","version":"1.2.3","scripts":{"prepare":"build","test":"t"}}`,
	})

	tamperWithStoreFile(t, pkg.StorePath, "package.json", "{\n  \"name\": \"left-pad\",\n  \"scripts\": {\n    \"test\": \"t\"\n  },\n  \"version\": \"1.2.3\"\n}\n")

	out, err := runDoctor(t, true)
	if err != nil {
		t.Errorf("RunDoctor() = %v for a manifest the store's own rewrite could have written, want nil", err)
	}

	if strings.Contains(out, "do not hold the content recorded for them") {
		t.Errorf("RunDoctor faulted a manifest lnpm rewrote itself, output was:\n%s", out)
	}
	if !strings.Contains(out, "could not be checked") {
		t.Errorf("RunDoctor passed over the manifest it could not check instead of naming it, output was:\n%s", out)
	}
}

// TestRunDoctorDoesNotFailForAPackageWithNoRecordedFiles aligns this check with
// what add already does about the same state. storeFilesForLink treats a
// missing file manifest as a reason to relink the entry in full rather than as
// damage, and Checks 5 and 7 make the same allowance for a store written before
// completeness markers existed. An entry doctor cannot verify is named, but it
// is a gap in what was checked and not a broken install.
func TestRunDoctorDoesNotFailForAPackageWithNoRecordedFiles(t *testing.T) {
	dir := newDoctorStoreConfig(t)
	entry := seedUnmarkedEntry(t, dir, "left-pad", "aaa111")
	writeCompletenessMarker(t, entry, "aaa111")
	if err := openDoctorDB(t).InsertPackage(&db.Package{
		Name: "left-pad", Version: "1.2.3", ContentHash: "aaa111", StorePath: entry,
	}); err != nil {
		t.Fatalf("insert package: %v", err)
	}

	out, err := runDoctor(t, true)
	if err != nil {
		t.Errorf("RunDoctor() = %v for a package with no recorded file list, want nil: add relinks that in full rather than calling it damage", err)
	}

	if !strings.Contains(out, "could not be verified") {
		t.Errorf("RunDoctor printed OK over an entry it never compared against anything, output was:\n%s", out)
	}
}

// TestRunDoctorReportsTheBytesItActuallyRead pins the count in the OK line to
// the files on disk rather than to the sizes the database happens to record.
// The two are independent - size is not part of any hash - so a stale or wrong
// Size would otherwise be reported as though it had been read.
func TestRunDoctorReportsTheBytesItActuallyRead(t *testing.T) {
	dir := newDoctorStoreConfig(t)
	seedVerifiableEntry(t, dir, "left-pad", "1.2.3", map[string]string{
		"index.js": "0123456789", // ten bytes on disk
	})
	misrecordFileSize(t, "left-pad", "index.js", 999999)

	out, _ := runDoctor(t, true)

	line := doctorCheckLine(t, out, "Checking store file integrity... ")
	if !strings.Contains(line, "10 B") {
		t.Errorf("integrity check reported %q, want the 10 bytes it actually read; output was:\n%s", line, out)
	}
}

// misrecordFileSize rewrites the recorded size of one file row, leaving its
// content hash alone, so the row still describes the stored bytes but no longer
// describes how many of them there are.
func misrecordFileSize(t *testing.T, packageName, relPath string, size int64) {
	t.Helper()

	database := openDoctorDB(t)
	pkg, err := database.GetPackageByName(packageName)
	if err != nil || pkg == nil {
		t.Fatalf("look up %s: pkg = %v, err = %v", packageName, pkg, err)
	}
	entries, err := database.GetFilesForPackage(pkg.ID)
	if err != nil {
		t.Fatalf("read file rows: %v", err)
	}
	for _, entry := range entries {
		if entry.RelativePath == relPath {
			entry.Size = size
		}
	}
	if err := database.InsertFiles(pkg.ID, entries); err != nil {
		t.Fatalf("rewrite file rows: %v", err)
	}
}

// TestRunDoctorChecksANestedManifest keeps the carve-out for lnpm's own rewrite
// as narrow as the rewrite is. store.stripLifecycleScripts opens exactly one
// path, the entry's root package.json, so a package.json shipped inside the
// package is an ordinary file and a change to it is damage. Matching on the
// base name instead of the whole relative path would excuse it.
func TestRunDoctorChecksANestedManifest(t *testing.T) {
	dir := newDoctorStoreConfig(t)
	pkg := seedVerifiableEntry(t, dir, "left-pad", "1.2.3", map[string]string{
		"package.json":          `{"name":"left-pad","version":"1.2.3"}`,
		"fixtures/package.json": `{"name":"fixture","version":"0.0.1"}`,
	})

	tamperWithStoreFile(t, pkg.StorePath, "fixtures/package.json", `{"name":"poisoned"}`)

	out, err := runDoctor(t, true)
	if err == nil {
		t.Errorf("RunDoctor() = nil for a tampered nested manifest, want an error; output was:\n%s", out)
	}

	if !strings.Contains(out, "do not hold the content recorded for them") {
		t.Errorf("RunDoctor excused a nested package.json as one lnpm rewrote itself, output was:\n%s", out)
	}
	if !strings.Contains(out, "fixtures/package.json") {
		t.Errorf("RunDoctor did not name the nested manifest it faulted, output was:\n%s", out)
	}
}

// TestRunDoctorSaysWhenContentWasNotRehashed is the other half of #439's fifth
// criterion. Re-hashing is O(bytes) and stays off the default run, so what the
// default must not do is leave the cheap check wearing the thorough one's name -
// that is the defect being fixed, not a smaller version of it. The line has to
// say plainly that the content was not read, and name the flag that reads it.
func TestRunDoctorSaysWhenContentWasNotRehashed(t *testing.T) {
	dir := newDoctorStoreConfig(t)
	pkg := seedVerifiableEntry(t, dir, "left-pad", "1.2.3", map[string]string{
		"index.js": "module.exports = 'left-pad';",
	})

	tamperWithStoreFile(t, pkg.StorePath, "index.js", "module.exports = 'poisoned';")

	out, err := runDoctor(t, false)
	if err != nil {
		t.Errorf("RunDoctor() = %v for a default run, want nil: the default does not read content, so it has no finding to fail on", err)
	}

	line := doctorCheckLine(t, out, "Checking store file integrity... ")
	if !strings.Contains(line, "SKIPPED") {
		t.Errorf("integrity check reported %q on a default run, want it to say the content was not re-hashed; output was:\n%s", line, out)
	}
	if !strings.Contains(out, "--verify-content") {
		t.Errorf("RunDoctor did not name the flag that runs the check it skipped, output was:\n%s", out)
	}
}

// seedVerifiableEntry writes the store entry and the database rows a content
// check needs: the files on disk, the completeness marker, and the package and
// file rows describing them, with the entry addressed by the content hash its
// own manifest produces - so the directory name really is the claim the check
// is there to test.
//
// The files are written read-only while the rows record the mode they were
// published with, because that is the state a store is in after #333 and the
// difference is what the trap on #439 is made of. A fixture that wrote both the
// same way would pass for a check that hashes on-disk permissions, which faults
// every entry of a real store.
func seedVerifiableEntry(t *testing.T, dir, name, version string, files map[string]string) *db.Package {
	t.Helper()

	const publishedMode = os.FileMode(0644)

	// Hashed from a staging copy, because the entry cannot be created until its
	// content hash is known: that hash is the directory's name.
	staging := t.TempDir()
	infos := make([]*pack.FileInfo, 0, len(files))
	for relPath, content := range files {
		infos = append(infos, &pack.FileInfo{
			RelPath:     relPath,
			Size:        int64(len(content)),
			Mode:        publishedMode,
			ContentHash: hashDoctorFixtureFile(t, staging, relPath, content, publishedMode),
		})
	}
	contentHash := pack.HashFiles(infos)

	entry := filepath.Join(dir, "mystore", "store", name, contentHash)
	rows := make([]*db.FileEntry, 0, len(infos))
	for _, f := range infos {
		writeDoctorFixtureFile(t, entry, f.RelPath, files[f.RelPath], publishedMode&^0222)
		rows = append(rows, &db.FileEntry{
			RelativePath: f.RelPath,
			ContentHash:  f.ContentHash,
			Size:         f.Size,
			Mode:         f.Mode,
		})
	}
	writeCompletenessMarker(t, entry, contentHash)

	pkg := &db.Package{Name: name, Version: version, ContentHash: contentHash, StorePath: entry}
	if err := openDoctorDB(t).InsertPackageWithFiles(pkg, rows); err != nil {
		t.Fatalf("insert package with files: %v", err)
	}
	return pkg
}

// hashDoctorFixtureFile writes content under root and returns its hash, taken
// through the same helper the store's write path uses so the fixture cannot
// disagree with production about what a file's hash is.
func hashDoctorFixtureFile(t *testing.T, root, relPath, content string, mode os.FileMode) string {
	t.Helper()

	writeDoctorFixtureFile(t, root, relPath, content, mode)
	hash, err := pack.HashFile(filepath.Join(root, filepath.FromSlash(relPath)))
	if err != nil {
		t.Fatalf("hash %s: %v", relPath, err)
	}
	return hash
}

// writeDoctorFixtureFile writes content at relPath under root, creating the
// parent directories.
func writeDoctorFixtureFile(t *testing.T, root, relPath, content string, mode os.FileMode) {
	t.Helper()

	path := filepath.Join(root, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("create %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// tamperWithStoreFile replaces a file inside a store entry with other bytes,
// which is the damage the integrity check exists to find. It removes and
// recreates rather than writing in place, for the reasons poisonStoreFile in
// the tests package records - it is the same manoeuvre against an entry this
// package assembled rather than one lnpm published.
func tamperWithStoreFile(t *testing.T, entry, relPath, content string) {
	t.Helper()

	path := filepath.Join(entry, filepath.FromSlash(relPath))
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0444); err != nil {
		t.Fatalf("rewrite %s: %v", path, err)
	}
}

// TestRunDoctorFailsWhenIssuesFound pins the exit code a script sees:
// `lnpm doctor && deploy.sh` must not run the deploy when doctor found
// problems. The configured store is deliberately absent, so Check 1 reports an
// issue and the returned error is what carries that out to cobra.
func TestRunDoctorFailsWhenIssuesFound(t *testing.T) {
	newDoctorStoreConfig(t) // the configured store is deliberately not created

	out, err := runDoctor(t, false)

	if !strings.Contains(out, "issue(s)") {
		t.Fatalf("this test needs a run that reports issues, output was:\n%s", out)
	}
	if err == nil {
		t.Errorf("RunDoctor() = nil after reporting issues, want an error; output was:\n%s", out)
	}
}

// TestRunDoctorSucceedsWhenAllChecksPass is the other half of the contract: a
// healthy store must exit zero. The output assertion keeps the nil honest — it
// has to come from a run that actually passed every check, not from one that
// failed to reach the summary.
func TestRunDoctorSucceedsWhenAllChecksPass(t *testing.T) {
	dir := newDoctorStoreConfig(t)
	newDoctorStore(t, dir)

	out, err := runDoctor(t, true)

	if !strings.Contains(out, "All checks passed") {
		t.Fatalf("this test needs a run where every check passes, output was:\n%s", out)
	}
	if err != nil {
		t.Errorf("RunDoctor() = %v on a healthy store, want nil", err)
	}
}

// TestRunDoctorDoesNotClaimAFullPassWithContentUnchecked is #439's own defect
// reproduced in the line users actually read. The check line saying SKIPPED is
// no use if the summary two lines later says every check passed: a skip adds
// neither an issue nor a warning, so the run ended by claiming a clean bill of
// health over a check that never ran.
//
// The summary must not say that, and it must not shout either - a default run
// is the ordinary way to use doctor, not a fault.
func TestRunDoctorDoesNotClaimAFullPassWithContentUnchecked(t *testing.T) {
	dir := newDoctorStoreConfig(t)
	newDoctorStore(t, dir)

	out, err := runDoctor(t, false)
	if err != nil {
		t.Errorf("RunDoctor() = %v on a healthy store with content unchecked, want nil: a skip is not a fault", err)
	}

	if strings.Contains(out, "All checks passed") {
		t.Errorf("RunDoctor claimed every check passed after skipping one, output was:\n%s", out)
	}
	if strings.Contains(out, "issue(s)") || strings.Contains(out, "warning(s)") {
		t.Errorf("RunDoctor reported a skipped check as a finding, output was:\n%s", out)
	}
	if !strings.Contains(out, "--verify-content") {
		t.Errorf("RunDoctor did not say what was left unchecked or how to check it, output was:\n%s", out)
	}
}

// TestRunDoctorSucceedsWithWarningsOnly pins the distinction the summary
// already draws: warnings describe things worth cleaning up, not things that
// are broken, so they must not fail the command. The package planted here was
// published and never linked, which doctor reports as an orphan and a warning.
func TestRunDoctorSucceedsWithWarningsOnly(t *testing.T) {
	dir := newDoctorStoreConfig(t)
	newDoctorStore(t, dir)
	if err := openDoctorDB(t).InsertPackage(&db.Package{Name: "orphan"}); err != nil {
		t.Fatalf("insert package: %v", err)
	}

	out, err := runDoctor(t, false)

	if !strings.Contains(out, "warning(s)") || strings.Contains(out, "issue(s)") {
		t.Fatalf("this test needs a run reporting warnings and no issues, output was:\n%s", out)
	}
	if err != nil {
		t.Errorf("RunDoctor() = %v for warnings alone, want nil", err)
	}
}

// TestRunDoctorMarkersComeFromTheIconHelpers sweeps doctor's report for the
// decorative markers, one scenario per group of call sites, so a check printing
// "✓"/"✗"/"⚠" as a string literal instead of calling the helpers is caught
// wherever it is.
//
// That, and only that, is what this proves. Capturing stdout replaces it with a
// pipe, and the helpers fall back to ASCII whenever stdout is not a terminal,
// so a report free of glyphs here says nothing about NO_COLOR: the pipe alone
// would have produced it. NO_COLOR is set anyway, so the sweep does not depend
// on how the capture is implemented. What NO_COLOR actually does, and what an
// interactive terminal still shows, are covered in output_tty_linux_test.go,
// where stdout is a real terminal.
func TestRunDoctorMarkersComeFromTheIconHelpers(t *testing.T) {
	cases := []struct {
		name string
		// setup prepares the store inside dir so the run reaches a particular
		// group of call sites, and returns a line the report must contain, to
		// keep the scenario honest about which branch it exercised.
		setup func(t *testing.T, dir string) string
		// requires, when set, skips the scenario where the platform cannot
		// produce the failure its branch is reached through.
		requires func(t *testing.T)
		// verifyContent runs the scenario with the content check on, for the
		// call sites that only exist inside it.
		verifyContent bool
	}{
		{
			name: "store missing",
			setup: func(t *testing.T, dir string) string {
				return "NOT FOUND" // and the "x Found N issue(s)" summary
			},
		},
		{
			name: "store path is not a directory",
			setup: func(t *testing.T, dir string) string {
				if err := os.WriteFile(filepath.Join(dir, "mystore"), []byte("not a dir"), 0644); err != nil {
					t.Fatalf("write store file: %v", err)
				}
				return "is not a directory" // and the database check's ERROR line
			},
		},
		{
			name:     "store not writable",
			requires: requirePermissionEnforcement,
			setup: func(t *testing.T, dir string) string {
				storePath := filepath.Join(dir, "mystore")
				if err := os.MkdirAll(storePath, 0500); err != nil {
					t.Fatalf("create read-only store: %v", err)
				}
				t.Cleanup(func() { _ = os.Chmod(storePath, 0700) }) // so TempDir can clean up
				return "NOT WRITABLE"
			},
		},
		{
			name: "everything healthy",
			setup: func(t *testing.T, dir string) string {
				newDoctorStore(t, dir)
				return "Every check that ran passed" // the default run skips Check 6
			},
		},
		{
			name:          "everything healthy, content verified",
			verifyContent: true,
			setup: func(t *testing.T, dir string) string {
				newDoctorStore(t, dir)
				return "All checks passed"
			},
		},
		{
			name:          "tampered store content",
			verifyContent: true,
			setup: func(t *testing.T, dir string) string {
				pkg := seedVerifiableEntry(t, dir, "left-pad", "1.2.3", map[string]string{
					"index.js": "module.exports = 'left-pad';",
				})
				tamperWithStoreFile(t, pkg.StorePath, "index.js", "module.exports = 'poisoned';")
				return "do not hold the content recorded for them" // and the "x Found N issue(s)" summary
			},
		},
		{
			name:          "store entry that could not be verified",
			verifyContent: true,
			setup: func(t *testing.T, dir string) string {
				entry := seedUnmarkedEntry(t, dir, "left-pad", "aaa111")
				writeCompletenessMarker(t, entry, "aaa111")
				if err := openDoctorDB(t).InsertPackage(&db.Package{
					Name: "left-pad", Version: "1.2.3", ContentHash: "aaa111", StorePath: entry,
				}); err != nil {
					t.Fatalf("insert package: %v", err)
				}
				return "could not be verified" // and the "! Found N warning(s)" summary
			},
		},
		{
			name: "incomplete store entry",
			setup: func(t *testing.T, dir string) string {
				seedUnmarkedEntry(t, dir, "left-pad", "aaa111")
				writeCompletenessMarker(t, seedUnmarkedEntry(t, dir, "right-pad", "bbb222"), "bbb222")
				return "incomplete store entry(ies)" // and the "x Found N issue(s)" summary
			},
		},
		{
			name: "legacy store awaiting migration",
			setup: func(t *testing.T, dir string) string {
				seedUnmarkedEntry(t, dir, "left-pad", "aaa111")
				return "PENDING" // and the "! Found N warning(s)" summary
			},
		},
		{
			name:     "legacy migration blocked by an unreadable directory",
			requires: requirePermissionEnforcement,
			setup: func(t *testing.T, dir string) string {
				seedUnmarkedEntry(t, dir, "left-pad", "aaa111")
				blocked := filepath.Dir(seedUnmarkedEntry(t, dir, "blocked-pkg", "bbb222"))
				if err := os.Chmod(blocked, 0000); err != nil {
					t.Fatalf("chmod %s: %v", blocked, err)
				}
				t.Cleanup(func() { _ = os.Chmod(blocked, 0755) })
				return "could not be read" // and the "x Found N issue(s)" summary
			},
		},
		{
			name:     "store cannot be swept",
			requires: requireNotADirectoryError,
			setup: func(t *testing.T, dir string) string {
				// A file where the package store should be: the store root is a
				// usable directory, so the sweep runs, but listing entries
				// inside it fails.
				if err := os.MkdirAll(filepath.Join(dir, "mystore"), 0755); err != nil {
					t.Fatalf("create store: %v", err)
				}
				if err := os.WriteFile(filepath.Join(dir, "mystore", "store"), []byte("not a dir"), 0644); err != nil {
					t.Fatalf("write store file: %v", err)
				}
				return "could not be read"
			},
		},
		{
			name: "orphaned package with missing store files",
			setup: func(t *testing.T, dir string) string {
				newDoctorStore(t, dir)
				database := openDoctorDB(t)
				if err := database.InsertPackage(&db.Package{
					Name:      "orphan",
					StorePath: filepath.Join(dir, "gone"),
				}); err != nil {
					t.Fatalf("insert package: %v", err)
				}
				return "orphaned package(s)" // and the missing-files line
			},
		},
		{
			name: "orphaned link",
			setup: func(t *testing.T, dir string) string {
				newDoctorStore(t, dir)
				database := openDoctorDB(t)
				pkg := &db.Package{Name: "linked"} // no StorePath: nothing to miss
				if err := database.InsertPackage(pkg); err != nil {
					t.Fatalf("insert package: %v", err)
				}
				proj := &db.Project{Path: filepath.Join(dir, "gone-project"), Name: "gone"}
				if err := database.InsertProject(proj); err != nil {
					t.Fatalf("insert project: %v", err)
				}
				if err := database.InsertLink(&db.Link{PackageID: pkg.ID, ProjectID: proj.ID}); err != nil {
					t.Fatalf("insert link: %v", err)
				}
				return "orphaned link(s)"
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.requires != nil {
				tc.requires(t)
			}

			t.Setenv("NO_COLOR", "1")
			dir := newDoctorStoreConfig(t)
			want := tc.setup(t, dir)

			out, _ := runDoctor(t, tc.verifyContent)

			if !strings.Contains(out, want) {
				t.Fatalf("this scenario needs a report containing %q, output was:\n%s", want, out)
			}
			assertNoRawGlyphs(t, out)
		})
	}
}

// TestRunDoctorDoesNotAdviseGCForAPackageWhoseLinksItCannotRead pins the
// orphaned-package check's posture on a read it cannot complete.
//
// Links are what tells an orphan from a version somebody is using, so a package
// whose link index will not parse used to be counted as an orphan and the user
// sent to `lnpm gc` - which reads the same index, refuses, and aborts. Advice
// contradicting outcome. doctor reports what it could not read and offers no
// fix, the way the tags read beside it already does.
func TestRunDoctorDoesNotAdviseGCForAPackageWhoseLinksItCannotRead(t *testing.T) {
	dir := newDoctorStoreConfig(t)
	newDoctorStore(t, dir)
	pkg, _ := seedDoctorLink(t)

	damageDatabase(t, "links_by_package", linkKey(pkg.ID), []byte("[ not ids"))

	out, err := runDoctor(t, false)

	if line := doctorCheckLine(t, out, "Checking for orphaned packages... "); !strings.Contains(line, "ERROR") {
		t.Errorf("orphaned-package check reported %q for a link index it could not read, want an error; output was:\n%s", line, out)
	}
	if strings.Contains(out, "Run 'lnpm gc' to remove unused packages") {
		t.Errorf("RunDoctor advised gc for a package whose links it could not read; gc reads the same index and aborts. Output was:\n%s", out)
	}
	if !strings.Contains(out, "the link index for package") {
		t.Errorf("RunDoctor did not say what it could not read, output was:\n%s", out)
	}
	if err == nil {
		t.Errorf("RunDoctor() = nil after failing to read a package's links, want an error; output was:\n%s", out)
	}
}

// TestRunDoctorReportsLinksTheOrphanedLinkCheckCannotRead pins the same posture
// for the check below it, which had no error path at all.
//
// It counted the links it managed to read and printed OK when that came to
// zero, so a store whose link index will not parse was reported healthy. That is
// the quietest possible answer to real damage.
func TestRunDoctorReportsLinksTheOrphanedLinkCheckCannotRead(t *testing.T) {
	dir := newDoctorStoreConfig(t)
	newDoctorStore(t, dir)
	pkg, _ := seedDoctorLink(t)

	damageDatabase(t, "links_by_package", linkKey(pkg.ID), []byte("[ not ids"))

	out, err := runDoctor(t, false)

	if line := doctorCheckLine(t, out, "Checking for orphaned links... "); !strings.Contains(line, "ERROR") {
		t.Errorf("orphaned-link check reported %q for a link index it could not read, want an error; output was:\n%s", line, out)
	}
	if strings.Contains(out, "--fix-links") {
		t.Errorf("RunDoctor advised gc --fix-links from a count it could not stand behind, output was:\n%s", out)
	}
	if err == nil {
		t.Errorf("RunDoctor() = nil after failing to read a package's links, want an error; output was:\n%s", out)
	}
}

// TestRunDoctorReportsAProjectRecordTheOrphanedLinkCheckCannotRead pins the
// second read that check makes, and it is the one #292's triage singled out.
//
// encoding/json populates every field it decoded before a type mismatch, so a
// partially decoded project used to come back with a real Path still on disk and
// the link read as healthy. GetProjectByID returns nothing with its error now,
// which turns that into a link counted as orphaned - a count doctor cannot stand
// behind either way while the error is discarded. The package's links are left
// readable here so the check above passes and this one is on its own.
//
// The fixture is that type mismatch and not a syntax error, because only this
// shape reaches what the paragraph above describes: name takes a string, so the
// document parses, id and path decode, and the decode then fails with a Path
// that is a real directory this test created. A truncated document would decode
// nothing, leave Path empty, and exercise the shape that was always visible.
func TestRunDoctorReportsAProjectRecordTheOrphanedLinkCheckCannotRead(t *testing.T) {
	dir := newDoctorStoreConfig(t)
	newDoctorStore(t, dir)
	_, proj := seedDoctorLink(t)

	damageDatabase(t, "projects", linkKey(proj.ID),
		fmt.Appendf(nil, `{"id":%d,"path":%q,"name":123}`, proj.ID, proj.Path))

	out, err := runDoctor(t, false)

	if line := doctorCheckLine(t, out, "Checking for orphaned links... "); !strings.Contains(line, "ERROR") {
		t.Errorf("orphaned-link check reported %q for a project record it could not read, want an error; output was:\n%s", line, out)
	}
	if strings.Contains(out, "--fix-links") {
		t.Errorf("RunDoctor advised gc --fix-links for a project record it could not read, output was:\n%s", out)
	}
	if !strings.Contains(out, "the record of project") {
		t.Errorf("RunDoctor did not say what it could not read, output was:\n%s", out)
	}
	if err == nil {
		t.Errorf("RunDoctor() = nil after failing to read a project record, want an error; output was:\n%s", out)
	}
}

// seedDoctorLink plants one package consumed by one project in the database the
// following RunDoctor call reads, and returns both.
//
// The package carries no StorePath, so the store-entry checks have nothing to
// fault and the link checks are what the run reports on.
func seedDoctorLink(t *testing.T) (*db.Package, *db.Project) {
	t.Helper()

	database := openDoctorDB(t)
	pkg := &db.Package{Name: "linked-pkg", Version: "1.0.0", ContentHash: "0123456789abcdef"}
	if err := database.InsertPackage(pkg); err != nil {
		t.Fatalf("insert package: %v", err)
	}
	proj := &db.Project{Path: t.TempDir(), Name: "consumer"}
	if err := database.InsertProject(proj); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if err := database.InsertLink(&db.Link{PackageID: pkg.ID, ProjectID: proj.ID, LinkType: "hardlink"}); err != nil {
		t.Fatalf("insert link: %v", err)
	}
	return pkg, proj
}

// doctorCheckLine returns the line of doctor's report that begins with prefix,
// so a scenario can assert what one check said without matching text another
// check happened to print.
func doctorCheckLine(t *testing.T, out, prefix string) string {
	t.Helper()

	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}
	t.Fatalf("RunDoctor printed no %q line, output was:\n%s", prefix, out)
	return ""
}

// requireNotADirectoryError skips tests that need a path under a plain file to
// be reported as an error. Unix answers ENOTDIR there, which is what doctor
// surfaces; Windows answers "path not found", which is indistinguishable from
// an absent file and doctor reads as "nothing to report yet".
func requireNotADirectoryError(t *testing.T) {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("Windows reports a path under a file as not found, not as a not-a-directory error")
	}
}

// newDoctorStore creates the configured store directory, empty.
func newDoctorStore(t *testing.T, dir string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Join(dir, "mystore", "store"), 0755); err != nil {
		t.Fatalf("create store: %v", err)
	}
}

// openDoctorDB opens the database the following RunDoctor call will read, so a
// scenario can plant the packages, projects and links it needs doctor to find.
func openDoctorDB(t *testing.T) *db.DB {
	t.Helper()

	db.ResetForTesting()
	database, err := db.GetDB()
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	return database
}

// assertNoRawGlyphs fails when out contains any of the decorative markers,
// which must have been rendered as ASCII.
func assertNoRawGlyphs(t *testing.T, out string) {
	t.Helper()

	for _, glyph := range decorativeGlyphs {
		if strings.ContainsRune(out, glyph) {
			t.Errorf("output contains the raw glyph %q, want its ASCII fallback; output was:\n%s", string(glyph), out)
		}
	}
}

// decorativeGlyphs are the markers the icon helpers render on a terminal and
// replace with ASCII everywhere else.
const decorativeGlyphs = "✓✗⚠💡"

// captureFailingDoctorStdout runs RunDoctor against a store doctor is expected
// to fault, and returns what it printed. RunDoctor reports each check on stdout
// rather than through its return value, so the findings are only readable this
// way; the failure itself is asserted here so no caller has to restate it.
func captureFailingDoctorStdout(t *testing.T) string {
	t.Helper()

	out, err := runDoctor(t, false)
	if err == nil {
		t.Errorf("RunDoctor() = nil for a store it reported problems with, want an error; output was:\n%s", out)
	}
	return out
}

// runDoctor runs RunDoctor with stdout captured and returns both what it
// printed and what it returned.
//
// RunDoctor opens the database, which is cached for the process and would keep
// a file handle inside the test's temp dir, so it is released on the way out.
func runDoctor(t *testing.T, verifyContent bool) (string, error) {
	t.Helper()

	db.ResetForTesting()
	t.Cleanup(db.ResetForTesting)

	var err error
	out := captureStdout(t, func() { err = RunDoctor(verifyContent) })
	return out, err
}
