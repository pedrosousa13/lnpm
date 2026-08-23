package cli

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/pedrosousa13/lnpm/internal/config"
	"github.com/pedrosousa13/lnpm/internal/db"
	"github.com/pedrosousa13/lnpm/internal/store"
)

// newGCStore points lnpm at a fresh store and database, and returns the store
// directory and the open database for the caller to seed.
func newGCStore(t *testing.T) (string, *db.DB) {
	t.Helper()

	base := t.TempDir()
	t.Setenv("LNPM_STORE", base)
	db.ResetForTesting()
	t.Cleanup(db.ResetForTesting)

	// gc deletes things. Prove the override took effect before any of these
	// tests reaches a RemoveAll, rather than trusting that it did: a test that
	// silently ran against the real store would destroy a real user's packages.
	resolved, err := config.GetStorePath()
	if err != nil {
		t.Fatalf("resolve store path: %v", err)
	}
	if resolved != base {
		t.Fatalf("store path is %s, not the temp directory %s - refusing to run gc", resolved, base)
	}

	database, err := db.GetDB()
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	return filepath.Join(base, "store"), database
}

// TestRunGCReportsEntryRemovalFailure pins that gc stops discarding the error
// from removing a store entry. A removal that fails silently is how a
// partially deleted entry gets left behind while gc claims it cleaned up, and
// the database row it would then have dropped is the only record the entry
// ever existed.
func TestRunGCReportsEntryRemovalFailure(t *testing.T) {
	storeRoot, database := newGCStore(t)

	entry := filepath.Join(storeRoot, "stuck-pkg", "f00d")
	if err := os.MkdirAll(filepath.Join(entry, "dist"), 0755); err != nil {
		t.Fatalf("seed entry: %v", err)
	}
	if err := os.WriteFile(filepath.Join(entry, "dist", "index.js"), []byte("payload"), 0644); err != nil {
		t.Fatalf("seed entry file: %v", err)
	}
	// A non-empty directory in the marker's place, which os.Remove refuses to
	// delete on every platform, so removing this entry fails at its first step.
	// internal/store's blockMarkerRemoval explains why the obstruction takes
	// this shape rather than a permission denial.
	occupant := filepath.Join(entry, ".lnpm-complete", "occupied")
	if err := os.MkdirAll(filepath.Dir(occupant), 0755); err != nil {
		t.Fatalf("block marker removal: %v", err)
	}
	if err := os.WriteFile(occupant, []byte("x"), 0644); err != nil {
		t.Fatalf("block marker removal: %v", err)
	}

	if err := database.InsertPackage(&db.Package{
		Name:        "stuck-pkg",
		Version:     "1.0.0",
		ContentHash: "f00d",
		StorePath:   entry,
	}); err != nil {
		t.Fatalf("insert package: %v", err)
	}

	out := captureStdout(t, func() {
		if err := RunGC(false, "", false, true); err != nil {
			t.Errorf("RunGC() error = %v", err)
		}
	})

	if !strings.Contains(out, "Failed to remove stuck-pkg") {
		t.Errorf("RunGC did not report the failed removal, output was:\n%s", out)
	}
	if strings.Contains(out, "Removed 1 package(s)") {
		t.Errorf("RunGC claimed it removed a package it could not remove, output was:\n%s", out)
	}

	packages, err := database.ListPackages()
	if err != nil {
		t.Fatalf("list packages: %v", err)
	}
	if len(packages) != 1 {
		t.Errorf("RunGC dropped the database row for an entry it failed to remove, %d package(s) left", len(packages))
	}
}

// seedTempDir creates dir with one file in it, standing in for a temp directory
// an interrupted publish or relink left behind.
func seedTempDir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("seed %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.js"), []byte("payload"), 0644); err != nil {
		t.Fatalf("seed %s: %v", dir, err)
	}
}

// seedLinkedProject registers a project in the database with one package linked
// into it, so gc sweeps the project the way it does in production, and returns
// the project directory as the database records it.
//
// It returns the database's path and not the one handed to it, because those two
// are not always the same string. InsertProject stores normalizePath's result,
// which is filepath.EvalSymlinks, and on Windows EvalSymlinks expands an 8.3
// short name: a temp directory that arrives as C:\Users\RUNNER~1\... is recorded
// as C:\Users\runneradmin\... . gc reads project paths back out of the database,
// so that longer spelling is what it prints. Seeding files under the returned
// path keeps the test's idea of where things are and gc's idea in the same form.
func seedLinkedProject(t *testing.T, database *db.DB, storeRoot string) string {
	t.Helper()

	project := t.TempDir()
	proj := &db.Project{Path: project, Name: "consumer"}
	if err := database.InsertProject(proj); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	pkg := &db.Package{
		Name:        "linked-pkg",
		Version:     "1.0.0",
		ContentHash: "0123456789abcdef",
		StorePath:   filepath.Join(storeRoot, "linked-pkg", "0123456789abcdef"),
	}
	if err := database.InsertPackage(pkg); err != nil {
		t.Fatalf("insert package: %v", err)
	}
	if err := database.InsertLink(&db.Link{PackageID: pkg.ID, ProjectID: proj.ID, LinkType: "hardlink"}); err != nil {
		t.Fatalf("insert link: %v", err)
	}
	// InsertProject rewrites proj.Path in place with the normalized form.
	return proj.Path
}

// resolvePath returns path with any 8.3 short name or symlink along it expanded,
// so two spellings of one location compare equal.
//
// The store side and the project side of gc's report reach it by different
// routes and are not normalized alike: a project path is stored through
// db.normalizePath, which calls filepath.EvalSymlinks, while the store path is
// whatever LNPM_STORE holds, returned by config.GetStorePath untouched. On
// Windows those two produce different spellings of the same directory. Resolving
// both sides of a comparison is what makes the assertion independent of which
// route a path took, rather than correct only as long as today's routes stay put.
//
// EvalSymlinks needs the path to exist, and these assertions run after gc has
// removed the directories, so this walks up to the deepest ancestor that is still
// there and rejoins the rest. On Unix it is an ordinary symlink resolution.
func resolvePath(path string) string {
	rest := ""
	for cur := filepath.Clean(path); ; {
		if resolved, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(resolved, rest)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return filepath.Clean(path)
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}

// reportedTempDirs parses gc's temp-directory report into a map from each listed
// directory, resolved, to the note saying what it held.
//
// It matches whole paths rather than looking for one as a substring of the
// output. That is deliberately stricter: a substring test cannot tell the entry
// it wants from a longer path that merely contains it, and a basename test could
// not tell .lnpm/.tmp-c0ffee from .lnpm/@org/.tmp-c0ffee at all.
//
// The report lines are "  - <path> (<size>, <note>)". formatSize never emits a
// comma, so the first ", " after the size separates the two.
func reportedTempDirs(t *testing.T, out string) map[string]string {
	t.Helper()

	reported := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		body, ok := strings.CutPrefix(strings.TrimRight(line, "\r"), "  - ")
		if !ok {
			continue
		}
		open := strings.LastIndex(body, " (")
		if open < 0 || !strings.HasSuffix(body, ")") {
			continue
		}
		path := body[:open]
		details := body[open+2 : len(body)-1]
		comma := strings.Index(details, ", ")
		if comma < 0 {
			// An orphaned-package or orphaned-link line, not a temp directory.
			continue
		}
		reported[resolvePath(path)] = details[comma+2:]
	}
	return reported
}

// assertReclaimReported fails unless gc listed path with the expected note.
func assertReclaimReported(t *testing.T, out, path, wantNote string) {
	t.Helper()

	reported := reportedTempDirs(t, out)
	note, ok := reported[resolvePath(path)]
	if !ok {
		t.Errorf("gc did not report %s; it reported %v\nfull output:\n%s", path, reported, out)
		return
	}
	if note != wantNote {
		t.Errorf("gc reported %s as %q, want %q", path, note, wantNote)
	}
}

// TestRunGCReclaimsOrphanedTempDirs covers all three shapes at once: the store's
// in-progress directory, the project's in-progress directory (inside a scope
// directory, which a one-level sweep would miss), and the retired directory an
// interrupted swap leaves holding a complete copy of the previous package.
//
// It also seeds a package whose name begins with a dot. ListLinked hides every
// dot-prefixed entry, so a sweep written against that filter rather than against
// the temp-name convention would delete a real package here.
func TestRunGCReclaimsOrphanedTempDirs(t *testing.T) {
	storeRoot, database := newGCStore(t)
	project := seedLinkedProject(t, database, storeRoot)

	storeTemp := filepath.Join(storeRoot, "linked-pkg", ".0123456789abcdef.tmp-4242")
	projectTemp := filepath.Join(project, ".lnpm", ".tmp-deadbeef")
	scopedTemp := filepath.Join(project, ".lnpm", "@org", ".tmp-c0ffee")
	retiredTemp := filepath.Join(project, ".lnpm", "@org", ".tmp-c0ffee.old")
	for _, dir := range []string{storeTemp, projectTemp, scopedTemp, retiredTemp} {
		seedTempDir(t, dir)
	}

	keep := []string{
		filepath.Join(project, ".lnpm", "ordinary-pkg"),
		filepath.Join(project, ".lnpm", ".hidden-pkg"),
		filepath.Join(project, ".lnpm", "@org", "scoped-pkg"),
	}
	for _, dir := range keep {
		seedTempDir(t, dir)
	}

	out := captureStdout(t, func() {
		if err := RunGC(false, "", false, true); err != nil {
			t.Errorf("RunGC() error = %v", err)
		}
	})

	for _, dir := range []string{storeTemp, projectTemp, scopedTemp, retiredTemp} {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("RunGC left %s behind (stat err = %v)", dir, err)
		}
	}
	for _, dir := range keep {
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("RunGC removed %s, which is a package and not a temp directory: %v", dir, err)
		}
	}
	// The scope directory must survive a temp directory inside it being taken.
	if _, err := os.Stat(filepath.Join(project, ".lnpm", "@org")); err != nil {
		t.Errorf("RunGC removed the scope directory: %v", err)
	}

	// gc has to say what it reclaimed: these directories are invisible to every
	// other command, so silence here means the user never learns they existed.
	assertReclaimReported(t, out, storeTemp, "incomplete")
	assertReclaimReported(t, out, projectTemp, "incomplete")
	assertReclaimReported(t, out, scopedTemp, "incomplete")
	assertReclaimReported(t, out, retiredTemp, "complete copy of the previous package")
	if len(reportedTempDirs(t, out)) != 4 {
		t.Errorf("RunGC reported %v, want exactly the four seeded temp directories", reportedTempDirs(t, out))
	}
	if strings.Contains(out, "Nothing to clean up") {
		t.Errorf("RunGC reported nothing to clean up while reclaiming temp directories, output was:\n%s", out)
	}
}

// TestRunGCReportsARetiredDirectoryAsACompletePackage pins the distinction that
// makes the retired shape worth reporting separately: it is not wasted space,
// it is a complete copy of the package that was linked before the interrupted
// relink, and the user has no other way to discover it.
func TestRunGCReportsARetiredDirectoryAsACompletePackage(t *testing.T) {
	storeRoot, database := newGCStore(t)
	project := seedLinkedProject(t, database, storeRoot)

	inProgress := filepath.Join(project, ".lnpm", ".tmp-11aa")
	retired := filepath.Join(project, ".lnpm", ".tmp-22bb.old")
	seedTempDir(t, inProgress)
	seedTempDir(t, retired)

	out := captureStdout(t, func() {
		if err := RunGC(false, "", false, true); err != nil {
			t.Errorf("RunGC() error = %v", err)
		}
	})

	assertReclaimReported(t, out, retired, "complete copy of the previous package")
	assertReclaimReported(t, out, inProgress, "incomplete")
}

// TestRunGCDryRunKeepsOrphanedTempDirs pins that the sweep goes through the same
// dry-run flow as the rest of gc. The pre-existing --fix-links path deletes
// orphaned link records before the confirmation guard; this must not repeat it.
func TestRunGCDryRunKeepsOrphanedTempDirs(t *testing.T) {
	storeRoot, database := newGCStore(t)
	project := seedLinkedProject(t, database, storeRoot)

	orphan := filepath.Join(project, ".lnpm", ".tmp-abcdef")
	seedTempDir(t, orphan)

	out := captureStdout(t, func() {
		if err := RunGC(true, "", false, true); err != nil {
			t.Errorf("RunGC() error = %v", err)
		}
	})

	if _, err := os.Stat(orphan); err != nil {
		t.Errorf("a dry run removed %s: %v", orphan, err)
	}
	assertReclaimReported(t, out, orphan, "incomplete")
	if strings.Contains(out, "Nothing to clean up") {
		t.Errorf("a dry run reported nothing to clean up after listing a temp directory, output was:\n%s", out)
	}
}

// TestRunGCDeclinedKeepsOrphanedTempDirs pins that reclaiming is confirmed
// before it happens, like every other destructive step in gc.
func TestRunGCDeclinedKeepsOrphanedTempDirs(t *testing.T) {
	storeRoot, database := newGCStore(t)
	project := seedLinkedProject(t, database, storeRoot)

	orphan := filepath.Join(project, ".lnpm", ".tmp-abcdef")
	seedTempDir(t, orphan)

	// yes=false with a non-interactive stdin is how confirm reports a refusal,
	// so this is the "the user did not agree" case.
	captureStdout(t, func() {
		if err := RunGC(false, "", false, false); err != nil {
			t.Errorf("RunGC() error = %v", err)
		}
	})

	if _, err := os.Stat(orphan); err != nil {
		t.Errorf("RunGC removed %s without confirmation: %v", orphan, err)
	}
}

// TestReapTempDirsRequiresTheDatabaseLock pins the ordering the whole safety
// argument rests on. The sweep is safe only because gc holds the exclusive
// database lock across it: every path that creates one of these directories
// opens the database first, so while the lock is held no temp directory can
// have a live writer. A future change that closes the database before sweeping
// would void that silently and let the sweep delete a live relink's temp
// directory mid-write — the corruption #137 removed. It must fail loudly
// instead, which is what this asserts.
func TestReapTempDirsRequiresTheDatabaseLock(t *testing.T) {
	storeRoot, database := newGCStore(t)
	project := seedLinkedProject(t, database, storeRoot)

	orphan := filepath.Join(project, ".lnpm", ".tmp-abcdef")
	seedTempDir(t, orphan)
	storeTemp := filepath.Join(storeRoot, "linked-pkg", ".0123456789abcdef.tmp-1")
	seedTempDir(t, storeTemp)

	if database.LockHeld() != true {
		t.Fatalf("LockHeld() = false on an open database")
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}
	if database.LockHeld() != false {
		t.Errorf("LockHeld() = true after Close")
	}

	s, err := store.New()
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	var sweepErr error
	captureStdout(t, func() {
		_, sweepErr = reapTempDirs(database, s, []string{project}, false, true)
	})
	if sweepErr == nil {
		t.Fatalf("reapTempDirs swept with the database lock released")
	}
	if !strings.Contains(sweepErr.Error(), "database lock") {
		t.Errorf("reapTempDirs error = %v, want it to name the database lock", sweepErr)
	}
	for _, dir := range []string{orphan, storeTemp} {
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("reapTempDirs removed %s with the lock released: %v", dir, err)
		}
	}
}

// TestRunGCReportsTempDirsWhenPackageDeletionIsDeclined pins that the two
// prompts are independent decisions. Declining to delete orphaned packages used
// to return before the sweep ran, so a user who said no there never learned temp
// directories existed — and no other command would ever have told them.
func TestRunGCReportsTempDirsWhenPackageDeletionIsDeclined(t *testing.T) {
	storeRoot, database := newGCStore(t)
	project := seedLinkedProject(t, database, storeRoot)

	// An orphaned package, so the package prompt is reached and declined.
	orphanEntry := filepath.Join(storeRoot, "orphan-pkg", "aabbccddeeff0011")
	seedTempDir(t, orphanEntry)
	if err := database.InsertPackage(&db.Package{
		Name:        "orphan-pkg",
		Version:     "1.0.0",
		ContentHash: "aabbccddeeff0011",
		StorePath:   orphanEntry,
	}); err != nil {
		t.Fatalf("insert package: %v", err)
	}

	tempDir := filepath.Join(project, ".lnpm", ".tmp-abcdef")
	seedTempDir(t, tempDir)

	// yes=false with a non-interactive stdin is how confirm reports a refusal,
	// so both prompts are declined here.
	out := captureStdout(t, func() {
		if err := RunGC(false, "", false, false); err != nil {
			t.Errorf("RunGC() error = %v", err)
		}
	})

	if _, ok := reportedTempDirs(t, out)[resolvePath(tempDir)]; !ok {
		t.Errorf("declining the package prompt suppressed the temp directory report, output was:\n%s", out)
	}
	// Both declines must still be honoured.
	if _, err := os.Stat(tempDir); err != nil {
		t.Errorf("RunGC removed %s without confirmation: %v", tempDir, err)
	}
	if _, err := os.Stat(orphanEntry); err != nil {
		t.Errorf("RunGC removed %s without confirmation: %v", orphanEntry, err)
	}
}

// TestRunGCReportsARetiredLinkAsALink pins that the retired label tracks what
// the entry actually holds. LinkSource retires a link, which holds no copy of
// anything, so calling it a complete copy of the previous package would be false.
func TestRunGCReportsARetiredLinkAsALink(t *testing.T) {
	storeRoot, database := newGCStore(t)
	project := seedLinkedProject(t, database, storeRoot)

	lnpm := filepath.Join(project, ".lnpm")
	if err := os.MkdirAll(lnpm, 0755); err != nil {
		t.Fatalf("create .lnpm: %v", err)
	}
	retiredLink := filepath.Join(lnpm, ".tmp-5566.old")
	if err := os.Symlink(t.TempDir(), retiredLink); err != nil {
		t.Skipf("cannot create a symlink here: %v", err)
	}

	out := captureStdout(t, func() {
		if err := RunGC(false, "", false, true); err != nil {
			t.Errorf("RunGC() error = %v", err)
		}
	})

	assertReclaimReported(t, out, retiredLink, "link to the previously linked source directory")
}

// TestRunGCReportsAPathThatNormalisationRewrote reproduces, on any platform, the
// divergence that broke the Windows job: the store path and a project path reach
// gc's report by different routes and only one of them is normalized. A project
// path is stored through db.normalizePath, which calls filepath.EvalSymlinks,
// while the store path is whatever LNPM_STORE holds and is never resolved. On
// Windows the two spellings were an 8.3 short name and its long form; a
// symlinked project directory produces the same mismatch everywhere.
//
// Without this, the only evidence that the assertions tolerate both spellings is
// a green Windows job, which is a slow and remote way to find out.
func TestRunGCReportsAPathThatNormalisationRewrote(t *testing.T) {
	storeRoot, database := newGCStore(t)

	base := t.TempDir()
	realDir := filepath.Join(base, "real-project")
	if err := os.MkdirAll(realDir, 0755); err != nil {
		t.Fatalf("create project: %v", err)
	}
	alias := filepath.Join(base, "alias-project")
	if err := os.Symlink(realDir, alias); err != nil {
		t.Skipf("cannot create a symlink here: %v", err)
	}

	// Registered under the alias; the database records the resolved spelling, and
	// that is the one gc will print.
	proj := &db.Project{Path: alias, Name: "consumer"}
	if err := database.InsertProject(proj); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if proj.Path == alias {
		t.Skipf("this platform did not rewrite %s, so there is no divergence to test", alias)
	}
	pkg := &db.Package{
		Name:        "linked-pkg",
		Version:     "1.0.0",
		ContentHash: "0123456789abcdef",
		StorePath:   filepath.Join(storeRoot, "linked-pkg", "0123456789abcdef"),
	}
	if err := database.InsertPackage(pkg); err != nil {
		t.Fatalf("insert package: %v", err)
	}
	if err := database.InsertLink(&db.Link{PackageID: pkg.ID, ProjectID: proj.ID, LinkType: "hardlink"}); err != nil {
		t.Fatalf("insert link: %v", err)
	}

	// Seeded under the alias, reported under the resolved path.
	orphan := filepath.Join(alias, ".lnpm", ".tmp-abcdef")
	seedTempDir(t, orphan)

	out := captureStdout(t, func() {
		if err := RunGC(false, "", false, true); err != nil {
			t.Errorf("RunGC() error = %v", err)
		}
	})

	assertReclaimReported(t, out, orphan, "incomplete")
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Errorf("RunGC left %s behind (stat err = %v)", orphan, err)
	}
}

// --- Unreadable links --------------------------------------------------------

// seedCollectableLink records a package with a store entry on disk and one
// project linked to it, and returns the entry's directory and the link's ID.
//
// The entry is the thing the regression is about: with the link readable gc
// keeps it, and the sweep on #329 showed that damaging the link is enough to
// make gc delete it while the project is still consuming it.
func seedCollectableLink(t *testing.T, database *db.DB, storeRoot string) (string, int64) {
	t.Helper()

	entry := filepath.Join(storeRoot, "linked-pkg", "0123456789abcdef")
	if err := os.MkdirAll(entry, 0755); err != nil {
		t.Fatalf("seed entry: %v", err)
	}
	if err := os.WriteFile(filepath.Join(entry, "index.js"), []byte("payload"), 0644); err != nil {
		t.Fatalf("seed entry file: %v", err)
	}

	proj := &db.Project{Path: t.TempDir(), Name: "consumer"}
	if err := database.InsertProject(proj); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	pkg := &db.Package{
		Name:        "linked-pkg",
		Version:     "1.0.0",
		ContentHash: "0123456789abcdef",
		StorePath:   entry,
	}
	if err := database.InsertPackage(pkg); err != nil {
		t.Fatalf("insert package: %v", err)
	}
	lnk := &db.Link{PackageID: pkg.ID, ProjectID: proj.ID, LinkType: "hardlink"}
	if err := database.InsertLink(lnk); err != nil {
		t.Fatalf("insert link: %v", err)
	}
	return entry, lnk.ID
}

// damageDatabase writes bytes straight into a bucket of the store's bbolt file,
// standing in for the damage a torn write leaves behind.
//
// It closes lnpm's handle first and leaves it closed, because bbolt holds an
// exclusive file lock: the run under test reopens the database itself, which is
// also what makes the damage look to gc exactly like damage it found on open.
func damageDatabase(t *testing.T, bucket string, key []byte, value []byte) {
	t.Helper()

	storePath, err := config.GetStorePath()
	if err != nil {
		t.Fatalf("resolve store path: %v", err)
	}
	db.ResetForTesting()

	handle, err := bolt.Open(filepath.Join(storePath, "lnpm.db"), 0600, &bolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("open the database directly: %v", err)
	}
	err = handle.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte(bucket)).Put(key, value)
	})
	if closeErr := handle.Close(); closeErr != nil {
		t.Fatalf("close the database: %v", closeErr)
	}
	if err != nil {
		t.Fatalf("damage %s: %v", bucket, err)
	}
}

// linkKey encodes a record ID the way every bucket keyed by one keys its rows -
// the link buckets it was written for, and the projects bucket alike.
func linkKey(id int64) []byte {
	key := make([]byte, 8)
	binary.BigEndian.PutUint64(key, uint64(id))
	return key
}

// assertGCDeletedNothing fails unless the store entry and the database row are
// both still there after the run.
func assertGCDeletedNothing(t *testing.T, entry string) {
	t.Helper()

	if _, err := os.Stat(entry); err != nil {
		t.Errorf("gc removed the store entry of a package whose links it could not read: %v", err)
	}
	database, err := db.GetDB()
	if err != nil {
		t.Fatalf("reopen database: %v", err)
	}
	packages, err := database.ListPackages()
	if err != nil {
		t.Fatalf("list packages: %v", err)
	}
	if len(packages) != 1 {
		t.Errorf("gc dropped the database row of a package whose links it could not read, %d package(s) left", len(packages))
	}
}

// TestRunGCAbortsWhenALinkRowCannotBeRead is the sweep's first reproduction on
// #329, turned into a regression test.
//
// gc's orphan scan already states the rule - a read that failed is
// indistinguishable from a version nothing links - and already aborts when the
// lookup returns an error. Before the fix the lookup dropped the unreadable row
// and returned a nil error, so gc collected the package and reported success
// while a project was still consuming it.
func TestRunGCAbortsWhenALinkRowCannotBeRead(t *testing.T) {
	storeRoot, database := newGCStore(t)
	entry, linkID := seedCollectableLink(t, database, storeRoot)

	damageDatabase(t, "links", linkKey(linkID), []byte("{ not a link"))

	out := captureStdout(t, func() {
		err := RunGC(false, "", false, true)
		if err == nil {
			t.Fatal("RunGC() returned no error for a link row it could not read")
		}
		if !strings.Contains(err.Error(), "linked-pkg@1.0.0") {
			t.Errorf("RunGC() error = %v, want it to name the package it could not read", err)
		}
	})

	if strings.Contains(out, "orphaned package") {
		t.Errorf("gc called a package orphaned on links it could not read, output was:\n%s", out)
	}
	assertGCDeletedNothing(t, entry)
}

// TestRunGCAbortsWhenALinkIndexEntryCannotBeRead is the sweep's second
// reproduction: the by-package index entry is the damaged one, which hides every
// link the package has rather than one of them.
func TestRunGCAbortsWhenALinkIndexEntryCannotBeRead(t *testing.T) {
	storeRoot, database := newGCStore(t)
	entry, _ := seedCollectableLink(t, database, storeRoot)

	packages, err := database.ListPackages()
	if err != nil || len(packages) != 1 {
		t.Fatalf("list packages: %v (%d found)", err, len(packages))
	}
	damageDatabase(t, "links_by_package", linkKey(packages[0].ID), []byte("[ not ids"))

	out := captureStdout(t, func() {
		err := RunGC(false, "", false, true)
		if err == nil {
			t.Fatal("RunGC() returned no error for a link index entry it could not read")
		}
		if !strings.Contains(err.Error(), "linked-pkg@1.0.0") {
			t.Errorf("RunGC() error = %v, want it to name the package it could not read", err)
		}
	})

	if strings.Contains(out, "orphaned package") {
		t.Errorf("gc called a package orphaned on an index entry it could not read, output was:\n%s", out)
	}
	assertGCDeletedNothing(t, entry)
}

// --- Unreadable projects -----------------------------------------------------

// seededProjectID returns the ID of the project seedCollectableLink linked the
// package into, read back through the same lookups gc uses.
func seededProjectID(t *testing.T, database *db.DB) int64 {
	t.Helper()

	packages, err := database.ListPackages()
	if err != nil || len(packages) != 1 {
		t.Fatalf("list packages: %v (%d found)", err, len(packages))
	}
	links, err := database.GetLinksForPackage(packages[0].ID)
	if err != nil || len(links) != 1 {
		t.Fatalf("read the seeded link: %v (%d found)", err, len(links))
	}
	return links[0].ProjectID
}

// assertLinkSurvived fails unless the seeded package still has its live link.
//
// It is kept apart from assertGCDeletedNothing, which the tests below call
// alongside it, because the two assert different rows: that one is about the
// package's store entry and database row, this one about the link row, and
// --fix-links deletes link rows on a pass of its own. Keeping them separate is
// what lets the dangling-link test assert that gc removed one link and kept the
// other, which a merged helper could not express.
func assertLinkSurvived(t *testing.T) {
	t.Helper()

	database, err := db.GetDB()
	if err != nil {
		t.Fatalf("reopen database: %v", err)
	}
	packages, err := database.ListPackages()
	if err != nil || len(packages) != 1 {
		t.Fatalf("list packages: %v (%d found)", err, len(packages))
	}
	links, err := database.GetLinksForPackage(packages[0].ID)
	if err != nil {
		t.Fatalf("read links back: %v", err)
	}
	if len(links) != 1 {
		t.Errorf("gc removed the live link of linked-pkg@1.0.0, %d link(s) left", len(links))
	}
}

// assertAbortedOnProject runs gc over a store whose project record is damaged
// and fails unless the run aborted naming projectID, reported nothing as
// orphaned, and left the package and its link where they were.
func assertAbortedOnProject(t *testing.T, entry string, projectID int64, fixLinks bool) {
	t.Helper()

	out := captureStdout(t, func() {
		err := RunGC(false, "", fixLinks, true)
		if err == nil {
			t.Fatal("RunGC() returned no error for a project record it could not read")
		}
		if !strings.Contains(err.Error(), fmt.Sprintf("%d", projectID)) {
			t.Errorf("RunGC() error = %v, want it to name project %d", err, projectID)
		}
	})

	if strings.Contains(out, "orphaned") {
		t.Errorf("gc called something orphaned on a project it could not read, output was:\n%s", out)
	}
	assertGCDeletedNothing(t, entry)
	assertLinkSurvived(t)
}

// TestRunGCAbortsWhenAProjectRowCannotBeRead pins #292 on a plain run, with no
// flag set, because that is the run the misclassification hurt most.
//
// The orphan scan discarded the error from the project lookup and classified on
// a nil result, so a record that would not parse was indistinguishable from a
// project that had gone away. The link was filed as orphaned, and the validLinks
// subtraction in the scan takes it off the version's consumer count
// unconditionally - fixLinks gates only the block that reports and deletes the
// link rows. So without the flag the version was still collected and its store
// entry still removed, and because the reporting is behind the flag, no line
// naming the link was printed: the same loss with nothing said about it.
func TestRunGCAbortsWhenAProjectRowCannotBeRead(t *testing.T) {
	storeRoot, database := newGCStore(t)
	entry, _ := seedCollectableLink(t, database, storeRoot)
	projectID := seededProjectID(t, database)

	damageDatabase(t, "projects", linkKey(projectID), []byte("{ not a project"))

	assertAbortedOnProject(t, entry, projectID, false)
}

// TestRunGCAbortsWhenAProjectRowCannotBeReadWithFixLinks is the same damage with
// --fix-links, which adds the deletion of the link row to what the plain run
// already destroyed.
func TestRunGCAbortsWhenAProjectRowCannotBeReadWithFixLinks(t *testing.T) {
	storeRoot, database := newGCStore(t)
	entry, _ := seedCollectableLink(t, database, storeRoot)
	projectID := seededProjectID(t, database)

	damageDatabase(t, "projects", linkKey(projectID), []byte("{ not a project"))

	assertAbortedOnProject(t, entry, projectID, true)
}

// TestRunGCAbortsWhenAProjectRowHoldsAWrongTypedValue drives the other damage
// shape, which failed in the opposite direction and so is not covered by the
// two above.
//
// json.Unmarshal validates a document before decoding any of it, so the syntax
// error those tests use decodes nothing and leaves Path empty. A document that
// parses but holds a value of the wrong type decodes up to the mismatch and
// carries on, so Path survives - and pre-fix gc stat'd that real directory,
// judged the link healthy and swept straight past the damage. Nothing was
// deleted, which is why it is not the shape ADR-0001 calls a bug, but nothing
// was reported either, and a record gc cannot read is not one it may vouch for.
func TestRunGCAbortsWhenAProjectRowHoldsAWrongTypedValue(t *testing.T) {
	storeRoot, database := newGCStore(t)
	entry, _ := seedCollectableLink(t, database, storeRoot)
	projectID := seededProjectID(t, database)

	// A real directory in a record that will not decode: name takes a string.
	damaged := fmt.Sprintf(`{"id":%d,"path":%q,"name":123}`, projectID, filepath.ToSlash(t.TempDir()))
	damageDatabase(t, "projects", linkKey(projectID), []byte(damaged))

	assertAbortedOnProject(t, entry, projectID, false)
}

// --- Confirming and reporting orphaned-link deletion -------------------------

// seedOrphanedLink adds a second link to the package seedCollectableLink
// recorded, naming a project ID no record answers for, so the orphan scan files
// exactly one link as orphaned and leaves the package's live link alone.
//
// InsertLink keeps one row per project and package name, so this adds a row
// rather than replacing the live one.
func seedOrphanedLink(t *testing.T, database *db.DB) {
	t.Helper()

	packages, err := database.ListPackages()
	if err != nil || len(packages) != 1 {
		t.Fatalf("list packages: %v (%d found)", err, len(packages))
	}
	if err := database.InsertLink(&db.Link{PackageID: packages[0].ID, ProjectID: 4242, LinkType: "hardlink"}); err != nil {
		t.Fatalf("insert the dangling link: %v", err)
	}
}

// remainingLinks counts the link rows the seeded package still has, read back
// through a fresh handle so it sees what gc committed rather than what the
// test's own handle remembers.
func remainingLinks(t *testing.T) int {
	t.Helper()

	database, err := db.GetDB()
	if err != nil {
		t.Fatalf("reopen database: %v", err)
	}
	packages, err := database.ListPackages()
	if err != nil || len(packages) != 1 {
		t.Fatalf("list packages: %v (%d found)", err, len(packages))
	}
	links, err := database.GetLinksForPackage(packages[0].ID)
	if err != nil {
		t.Fatalf("read links back: %v", err)
	}
	return len(links)
}

// TestRunGCDeclinedKeepsOrphanedLinks pins that --fix-links asks before it
// deletes, like every other destructive step in gc.
//
// The block deleted link rows the moment the flag was set. It honoured
// --dry-run, but --yes was meaningless because nothing ever asked, so there was
// no answer a user could give that stopped it.
func TestRunGCDeclinedKeepsOrphanedLinks(t *testing.T) {
	storeRoot, database := newGCStore(t)
	entry, _ := seedCollectableLink(t, database, storeRoot)
	seedOrphanedLink(t, database)

	// yes=false with a non-interactive stdin is how confirm reports a refusal,
	// so this is the "the user did not agree" case.
	out := captureStdout(t, func() {
		if err := RunGC(false, "", true, false); err != nil {
			t.Fatalf("RunGC() error = %v", err)
		}
	})

	if !strings.Contains(out, "Found 1 orphaned link(s)") {
		t.Errorf("gc did not report the orphaned link before asking, output was:\n%s", out)
	}
	if strings.Contains(out, "Removed") {
		t.Errorf("gc claimed it removed a link the user did not agree to, output was:\n%s", out)
	}
	if got := remainingLinks(t); got != 2 {
		t.Errorf("gc deleted a link without confirmation, %d link(s) left, want 2", got)
	}
	assertGCDeletedNothing(t, entry)
}

// TestRunGCConfirmedRemovesOrphanedLinks is the other side of the gate: --yes
// satisfies the confirmation, so the orphaned row goes and the live one stays.
//
// Without this the gate could be satisfied by never deleting anything at all.
func TestRunGCConfirmedRemovesOrphanedLinks(t *testing.T) {
	storeRoot, database := newGCStore(t)
	entry, _ := seedCollectableLink(t, database, storeRoot)
	seedOrphanedLink(t, database)

	out := captureStdout(t, func() {
		if err := RunGC(false, "", true, true); err != nil {
			t.Fatalf("RunGC() error = %v", err)
		}
	})

	if !strings.Contains(out, "Removed 1 orphaned link(s)") {
		t.Errorf("gc did not report removing the orphaned link, output was:\n%s", out)
	}
	if strings.Contains(out, "Skipped deleting orphaned links") {
		t.Errorf("--yes was treated as a refusal, output was:\n%s", out)
	}
	if got := remainingLinks(t); got != 1 {
		t.Errorf("%d link(s) left after --fix-links --yes, want 1 (the live one)", got)
	}
	assertLinkSurvived(t)
	assertGCDeletedNothing(t, entry)
}

// TestRunGCDryRunKeepsOrphanedLinks pins that adding the gate did not change
// what a dry run prints or leaves behind. A dry run reports its findings and
// stops before the question, so it must not print the prompt's refusal line
// either — a dry run has nothing to refuse.
func TestRunGCDryRunKeepsOrphanedLinks(t *testing.T) {
	storeRoot, database := newGCStore(t)
	entry, _ := seedCollectableLink(t, database, storeRoot)
	seedOrphanedLink(t, database)

	out := captureStdout(t, func() {
		if err := RunGC(true, "", true, true); err != nil {
			t.Fatalf("RunGC() error = %v", err)
		}
	})

	if !strings.Contains(out, "Found 1 orphaned link(s)") {
		t.Errorf("a dry run did not report the orphaned link, output was:\n%s", out)
	}
	if strings.Contains(out, "Removed") || strings.Contains(out, "Skipped deleting orphaned links") {
		t.Errorf("a dry run reached the deletion step, output was:\n%s", out)
	}
	if got := remainingLinks(t); got != 2 {
		t.Errorf("a dry run deleted a link, %d link(s) left, want 2", got)
	}
	assertGCDeletedNothing(t, entry)
}

// TestRemoveOrphanedLinksReportsEveryFailedDelete pins the other half of #291:
// the delete's error was discarded, and the summary counted the candidates
// rather than the successes, so a run where every delete failed printed a clean
// success and the rows it had not touched were never mentioned again.
//
// The failure is driven by closing the database handle before the deletes, which
// is the same device TestReapTempDirsRequiresTheDatabaseLock uses. It is the only
// failure DeleteLink has: the function it hands to bolt.Update always returns
// nil, so every error it can return comes from the transaction rather than from
// one link. Damaging a link row or its index entry was tried and returns a nil
// error - the damaged row is skipped by the ForEach that looks the ID up, and a
// link ID that is not found is not an error - so there is no per-link failure to
// drive, and a partial one cannot be produced without a fake database.
func TestRemoveOrphanedLinksReportsEveryFailedDelete(t *testing.T) {
	storeRoot, database := newGCStore(t)
	seedCollectableLink(t, database, storeRoot)

	packages, err := database.ListPackages()
	if err != nil || len(packages) != 1 {
		t.Fatalf("list packages: %v (%d found)", err, len(packages))
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}

	links := []linkToRemove{
		{packageID: packages[0].ID, projectID: 4242, reason: "project not found in database"},
		{packageID: packages[0].ID, projectID: 77, projectPath: filepath.Join("gone", "project"), reason: "project directory no longer exists"},
	}
	out := captureStdout(t, func() {
		removeOrphanedLinks(database, links)
	})

	// Both failures named, so a user knows which rows are still there.
	if !strings.Contains(out, "4242") {
		t.Errorf("the failed delete of the link to project 4242 was not reported, output was:\n%s", out)
	}
	if !strings.Contains(out, filepath.Join("gone", "project")) {
		t.Errorf("the failed delete of the link to %s was not reported, output was:\n%s", filepath.Join("gone", "project"), out)
	}
	// The reason each one failed, not just that it did.
	if !strings.Contains(out, "database not open") {
		t.Errorf("gc swallowed the error DeleteLink returned, output was:\n%s", out)
	}
	// And no claim about what was removed: nothing was.
	if strings.Contains(out, "Removed") {
		t.Errorf("gc claimed it removed links it did not remove, output was:\n%s", out)
	}
	if strings.Contains(out, iconOK()) {
		t.Errorf("gc reported success for a run where every delete failed, output was:\n%s", out)
	}
}

// TestRunGCReportsALinkToAMissingProjectAsOrphaned is the other side of the same
// branch, and the reason the fix is an error check rather than a wider guard.
//
// A lookup that succeeds and finds no record has established what the reason
// string says, so that link stays orphaned and stays removable. The package
// keeps its other link and so is not collected, which is what separates this
// from the abort above.
func TestRunGCReportsALinkToAMissingProjectAsOrphaned(t *testing.T) {
	storeRoot, database := newGCStore(t)
	entry, _ := seedCollectableLink(t, database, storeRoot)

	packages, err := database.ListPackages()
	if err != nil || len(packages) != 1 {
		t.Fatalf("list packages: %v (%d found)", err, len(packages))
	}
	// A link naming a project ID no record answers for. InsertLink keeps one row
	// per project and package name, so this adds a row rather than replacing the
	// live one.
	if err := database.InsertLink(&db.Link{PackageID: packages[0].ID, ProjectID: 4242, LinkType: "hardlink"}); err != nil {
		t.Fatalf("insert the dangling link: %v", err)
	}

	out := captureStdout(t, func() {
		if err := RunGC(false, "", true, true); err != nil {
			t.Fatalf("RunGC() error = %v", err)
		}
	})

	if !strings.Contains(out, "project not found in database") {
		t.Errorf("gc did not report the dangling link under its own reason, output was:\n%s", out)
	}
	if !strings.Contains(out, "Found 1 orphaned link(s)") {
		t.Errorf("gc did not report exactly one orphaned link, output was:\n%s", out)
	}
	if strings.Contains(out, "orphaned package") {
		t.Errorf("gc collected a package a live link still names, output was:\n%s", out)
	}
	assertGCDeletedNothing(t, entry)
	assertLinkSurvived(t)
}
