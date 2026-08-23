package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/config"
	"github.com/pedrosousa13/lnpm/internal/db"
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

	out, err := runDoctor(t)

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

	out, err := runDoctor(t)
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

	out, err := runDoctor(t)
	if err != nil {
		t.Errorf("RunDoctor() = %v for a store awaiting its migration, want nil: that is a warning, not a broken install", err)
	}

	if !strings.Contains(out, "PENDING") {
		t.Errorf("RunDoctor did not report the unmigrated store as pending, output was:\n%s", out)
	}
	if strings.Contains(out, entry) {
		t.Errorf("RunDoctor listed %s as damaged; every entry of an unmigrated store would be listed, and none of them needs re-publishing", out)
	}
	if _, err := os.Stat(filepath.Join(entry, ".lnpm-complete")); err == nil {
		t.Error("RunDoctor performed the migration; doctor must report without writing")
	}
}

// TestRunDoctorPassesAStoreOfCommittedEntries is the same check read the other
// way round: an entry the write path committed must not be reported.
func TestRunDoctorPassesAStoreOfCommittedEntries(t *testing.T) {
	dir := newDoctorStoreConfig(t)
	entry := seedUnmarkedEntry(t, dir, "left-pad", "aaa111")
	writeCompletenessMarker(t, entry, "aaa111")

	out, err := runDoctor(t)

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

// TestRunDoctorFailsWhenIssuesFound pins the exit code a script sees:
// `lnpm doctor && deploy.sh` must not run the deploy when doctor found
// problems. The configured store is deliberately absent, so Check 1 reports an
// issue and the returned error is what carries that out to cobra.
func TestRunDoctorFailsWhenIssuesFound(t *testing.T) {
	newDoctorStoreConfig(t) // the configured store is deliberately not created

	out, err := runDoctor(t)

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

	out, err := runDoctor(t)

	if !strings.Contains(out, "All checks passed") {
		t.Fatalf("this test needs a run where every check passes, output was:\n%s", out)
	}
	if err != nil {
		t.Errorf("RunDoctor() = %v on a healthy store, want nil", err)
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

	out, err := runDoctor(t)

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
				return "All checks passed"
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

			out, _ := runDoctor(t)

			if !strings.Contains(out, want) {
				t.Fatalf("this scenario needs a report containing %q, output was:\n%s", want, out)
			}
			assertNoRawGlyphs(t, out)
		})
	}
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

	out, err := runDoctor(t)
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
func runDoctor(t *testing.T) (string, error) {
	t.Helper()

	db.ResetForTesting()
	t.Cleanup(db.ResetForTesting)

	var err error
	out := captureStdout(t, func() { err = RunDoctor() })
	return out, err
}
