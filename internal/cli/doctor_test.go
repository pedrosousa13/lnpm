package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/config"
	"github.com/pedrosousa13/lnpm/internal/db"
	"github.com/pedrosousa13/lnpm/internal/store"
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

// TestRunDoctorReportsPendingBackfill covers both halves of doctor's job for
// the completeness-marker backfill: it says whether the store has been
// backfilled, and it does not do the backfilling. doctor reports and names the
// command that fixes each problem; repairing is somebody else's job.
func TestRunDoctorReportsPendingBackfill(t *testing.T) {
	dir := newDoctorStoreConfig(t)
	if err := os.MkdirAll(filepath.Join(dir, "mystore", "store"), 0755); err != nil {
		t.Fatalf("create store: %v", err)
	}

	out, err := runDoctor(t)
	if err != nil {
		t.Errorf("RunDoctor() = %v for a pending backfill, want nil: a backfill still to run is a warning", err)
	}

	if !strings.Contains(out, "completeness marker") {
		t.Errorf("RunDoctor did not report the pending completeness-marker backfill, output was:\n%s", out)
	}

	done, err := store.BackfillDone()
	if err != nil {
		t.Fatalf("backfill status: %v", err)
	}
	if done {
		t.Error("RunDoctor performed the backfill; doctor must report without writing")
	}
}

// TestRunDoctorSkipsBackfillCheckWithoutStore pins that the backfill report
// stays behind its prerequisite. With no store on the machine there is nothing
// to backfill, and the command doctor would name as the fix cannot run either,
// so warning about it is noise pointing nowhere.
func TestRunDoctorSkipsBackfillCheckWithoutStore(t *testing.T) {
	newDoctorStoreConfig(t) // the configured store is deliberately not created

	out := captureFailingDoctorStdout(t)

	if strings.Contains(out, "completeness marker") {
		t.Errorf("RunDoctor reported on the backfill for a store that does not exist, output was:\n%s", out)
	}
}

// TestRunDoctorReportsCompletedBackfill is the same check read the other way
// round: a store that has been backfilled must not be reported as pending.
func TestRunDoctorReportsCompletedBackfill(t *testing.T) {
	dir := newDoctorStoreConfig(t)
	if err := os.MkdirAll(filepath.Join(dir, "mystore", "store"), 0755); err != nil {
		t.Fatalf("create store: %v", err)
	}
	if _, err := store.New(); err != nil {
		t.Fatalf("open store: %v", err)
	}

	out, err := runDoctor(t)

	if strings.Contains(out, "not been backfilled") {
		t.Errorf("RunDoctor reported a backfilled store as pending, output was:\n%s", out)
	}
	if err != nil {
		t.Errorf("RunDoctor() = %v for a backfilled store, want nil", err)
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
	if err := os.MkdirAll(filepath.Join(dir, "mystore", "store"), 0755); err != nil {
		t.Fatalf("create store: %v", err)
	}
	if _, err := store.New(); err != nil { // marks the backfill done
		t.Fatalf("open store: %v", err)
	}

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
// are broken, so they must not fail the command. The store here exists but has
// no completeness markers yet, which doctor reports as a warning.
func TestRunDoctorSucceedsWithWarningsOnly(t *testing.T) {
	dir := newDoctorStoreConfig(t)
	if err := os.MkdirAll(filepath.Join(dir, "mystore", "store"), 0755); err != nil {
		t.Fatalf("create store: %v", err)
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
				if _, err := store.New(); err != nil { // marks the backfill done
					t.Fatalf("open store: %v", err)
				}
				return "All checks passed"
			},
		},
		{
			name: "backfill pending",
			setup: func(t *testing.T, dir string) string {
				newDoctorStore(t, dir)
				return "PENDING" // and the "! Found N warning(s)" summary
			},
		},
		{
			name:     "backfill status unreadable",
			requires: requireNotADirectoryError,
			setup: func(t *testing.T, dir string) string {
				// A file where the package store should be: the store is a
				// usable directory, so the check runs, but reading the marker
				// inside it fails.
				if err := os.MkdirAll(filepath.Join(dir, "mystore"), 0755); err != nil {
					t.Fatalf("create store: %v", err)
				}
				if err := os.WriteFile(filepath.Join(dir, "mystore", "store"), []byte("not a dir"), 0644); err != nil {
					t.Fatalf("write store file: %v", err)
				}
				return "Failed to read the backfill status"
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

// newDoctorStore creates the configured store directory, without the
// completeness marker that store.New writes.
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
