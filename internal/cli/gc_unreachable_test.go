package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/db"
)

// seedProjectAndPackage registers a project at projectPath with one package
// linked into it, and returns both records. The project directory must already
// exist: InsertProject runs the path through EvalSymlinks and records the device
// it finds, and both of those need a path that is there.
func seedProjectAndPackage(t *testing.T, database *db.DB, storeRoot, projectPath, pkgName string) (*db.Project, *db.Package) {
	t.Helper()

	proj := &db.Project{Path: projectPath, Name: "consumer"}
	if err := database.InsertProject(proj); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if proj.Path != projectPath {
		t.Fatalf("the database normalised the project path to %q, not %q; the test's idea of the path and gc's have diverged", proj.Path, projectPath)
	}
	pkg := &db.Package{
		Name:        pkgName,
		Version:     "1.0.0",
		ContentHash: "0123456789abcdef",
		StorePath:   filepath.Join(storeRoot, pkgName, "0123456789abcdef"),
	}
	if err := os.MkdirAll(pkg.StorePath, 0755); err != nil {
		t.Fatalf("seed store entry: %v", err)
	}
	if err := database.InsertPackage(pkg); err != nil {
		t.Fatalf("insert package: %v", err)
	}
	if err := database.InsertLink(&db.Link{PackageID: pkg.ID, ProjectID: proj.ID, LinkType: "hardlink"}); err != nil {
		t.Fatalf("insert link: %v", err)
	}
	return proj, pkg
}

// resolvedTempDir returns a fresh temp directory in the spelling lnpm's database
// will record it under.
//
// The two are not the same string on two of the three platforms this has to pass
// on. InsertProject stores db.normalizePath's result, which is EvalSymlinks: on
// macOS a temp path arrives as /var/folders/... and is stored as
// /private/var/folders/..., because /var is a symlink; on Windows TEMP is an 8.3
// short name, so C:\Users\RUNNER~1\... is stored as C:\Users\runneradmin\... .
// Linux happens not to differ, which is why building fixtures from the raw path
// looks correct there and fails on the other two.
//
// Every assertion here compares against what gc read out of the database or
// printed, so the fixture has to start from the same spelling. resolvePath is
// the helper gc_test.go already uses for this; it is reused rather than
// re-derived so there is one answer to the question in this package.
//
// This divergence does not need CI to reproduce, which is worth knowing before
// spending a run on it. Point TMPDIR at a symlink and the macOS shape appears on
// Linux:
//
//	mkdir -p /tmp/d/real && ln -sfn /tmp/d/real /tmp/d/link
//	TMPDIR=/tmp/d/link go test ./internal/cli/ ./internal/db/ -count=1
//
// Against the fixtures that failed CI that reproduces all nine of them, plus the
// internal/db one, with the same "the test's idea of the path and gc's have
// diverged" message.
func resolvedTempDir(t *testing.T) string {
	t.Helper()
	return resolvePath(t.TempDir())
}

// resolvedDir creates dir and returns it in the spelling the database will use.
func resolvedDir(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("seed %s: %v", dir, err)
	}
	return resolvePath(dir)
}

func packageNames(t *testing.T, database *db.DB) []string {
	t.Helper()
	packages, err := database.ListPackages()
	if err != nil {
		t.Fatalf("list packages: %v", err)
	}
	names := make([]string, 0, len(packages))
	for _, p := range packages {
		names = append(names, p.Name)
	}
	return names
}

// seedUnreachableProject builds the state an unmounted drive leaves behind: an
// empty mount-point directory, a project path under it that does not exist, and
// a recorded device that is not the one now mounted there.
//
// The mount point is modelled by an ordinary empty directory, and that is exact
// rather than approximate for everything a stat can see. A real unmount leaves
// the mount point present and empty on the PARENT filesystem, reverting to the
// parent's device - which is precisely what an ordinary empty directory has. The
// two are indistinguishable on disk, and that indistinguishability is the
// finding behind this whole fix, not a shortcut taken by the fixture. Verified
// against a real tmpfs unmounted in a user+mount namespace; see
// TestGCKeepsAPackageAcrossARealUnmount, which does the unmount for real where
// the platform allows it.
//
// What the fixture cannot model is the device drifting on its own, so the
// recorded device is set directly.
func seedUnreachableProject(t *testing.T, database *db.DB, storeRoot, pkgName string) (string, *db.Package) {
	t.Helper()

	project := resolvedDir(t, filepath.Join(t.TempDir(), "external", "myproject"))
	mountPoint := filepath.Dir(project)
	proj, pkg := seedProjectAndPackage(t, database, storeRoot, project, pkgName)
	if proj.Device == 0 {
		t.Skip("this platform reports no device, so gc has nothing to compare")
	}

	// The unmount: the project goes, the mount point stays.
	if err := os.RemoveAll(project); err != nil {
		t.Fatalf("model the unmount: %v", err)
	}
	if _, err := os.Stat(mountPoint); err != nil {
		t.Fatalf("the mount point should still exist after the unmount: %v", err)
	}
	// The filesystem that held the project is no longer the one mounted there.
	if err := database.SetProjectDevice(proj.ID, proj.Device+1); err != nil {
		t.Fatalf("record a device that is not mounted here: %v", err)
	}
	return project, pkg
}

// TestGCKeepsAPackageWhoseProjectFilesystemIsNotMounted is the #335 regression
// test and the primary proof of the fix.
//
// Before the fix this same scenario deleted the store entry and the database row
// of a package whose only consumer was sitting on an unplugged drive, on a plain
// gc with no flag involved, and re-mounting could not bring it back.
func TestGCKeepsAPackageWhoseProjectFilesystemIsNotMounted(t *testing.T) {
	storeRoot, database := newGCStore(t)
	_, pkg := seedUnreachableProject(t, database, storeRoot, "offline-pkg")

	out := captureStdout(t, func() {
		if err := RunGC(false, "", true, true); err != nil {
			t.Errorf("RunGC() error = %v", err)
		}
	})
	t.Logf("gc output:\n%s", out)

	if names := packageNames(t, database); len(names) != 1 {
		t.Errorf("gc collected a package whose only project is on an unmounted filesystem; packages left: %v", names)
	}
	if _, err := os.Stat(pkg.StorePath); err != nil {
		t.Errorf("gc removed the store entry %s: %v", pkg.StorePath, err)
	}
	if strings.Contains(out, "project directory no longer exists") {
		t.Errorf("gc classified the link as orphaned; output was:\n%s", out)
	}
}

// TestGCReportsTheLinksItSkipped pins that declining is visible. A destructive
// command that silently does less than usual is how the space leak this fix
// trades for safety would go unnoticed - the recorded device drifts on any
// filesystem with an anonymous device number, and the report is the only place
// a user finds out gc has stopped collecting a project.
func TestGCReportsTheLinksItSkipped(t *testing.T) {
	storeRoot, database := newGCStore(t)
	project, _ := seedUnreachableProject(t, database, storeRoot, "offline-pkg")

	out := captureStdout(t, func() {
		if err := RunGC(false, "", false, true); err != nil {
			t.Errorf("RunGC() error = %v", err)
		}
	})
	t.Logf("gc output:\n%s", out)

	if !strings.Contains(out, "Skipped 1 link(s)") {
		t.Errorf("gc did not report the skipped link count; output was:\n%s", out)
	}
	if !strings.Contains(out, project) {
		t.Errorf("gc did not name the project it skipped; output was:\n%s", out)
	}
	if !strings.Contains(out, "offline-pkg@1.0.0") {
		t.Errorf("gc did not name the package the skipped link kept alive; output was:\n%s", out)
	}
}

// TestGCReportsSkippedLinksWithoutFixLinks pins that the skip report is not
// gated on --fix-links. The orphaned-link report is, and copying that here would
// hide the reason a flagless run reclaimed nothing - which is the run most users
// make.
func TestGCReportsSkippedLinksWithoutFixLinks(t *testing.T) {
	storeRoot, database := newGCStore(t)
	seedUnreachableProject(t, database, storeRoot, "offline-pkg")

	out := captureStdout(t, func() {
		if err := RunGC(true, "", false, true); err != nil {
			t.Errorf("RunGC() error = %v", err)
		}
	})
	if !strings.Contains(out, "Skipped 1 link(s)") {
		t.Errorf("a dry run without --fix-links hid the skipped link; output was:\n%s", out)
	}
}

// TestGCCollectsWhenTheProjectDirectoryIsGone is acceptance criterion 2, and the
// half of this fix that is easiest to break: a project genuinely deleted, with
// its filesystem still mounted where it always was, must still be collected.
// Anything that made gc refuse to judge a missing directory in general would
// pass the test above and fail this one.
func TestGCCollectsWhenTheProjectDirectoryIsGone(t *testing.T) {
	storeRoot, database := newGCStore(t)

	project := resolvedDir(t, filepath.Join(t.TempDir(), "myproject"))
	_, pkg := seedProjectAndPackage(t, database, storeRoot, project, "deleted-pkg")
	if err := os.RemoveAll(project); err != nil {
		t.Fatalf("delete project: %v", err)
	}

	out := captureStdout(t, func() {
		if err := RunGC(false, "", true, true); err != nil {
			t.Errorf("RunGC() error = %v", err)
		}
	})
	t.Logf("gc output:\n%s", out)

	if names := packageNames(t, database); len(names) != 0 {
		t.Errorf("gc did not collect a package whose only project was genuinely deleted; packages left: %v", names)
	}
	if _, err := os.Stat(pkg.StorePath); !os.IsNotExist(err) {
		t.Errorf("gc left the store entry %s behind, stat error = %v", pkg.StorePath, err)
	}
	if strings.Contains(out, "Skipped") {
		t.Errorf("gc declined to judge a plainly deleted project; output was:\n%s", out)
	}
}

// TestGCCollectsWhenTheProjectHasNoRecordedDevice pins the fallback for every
// database written before this field existed.
//
// Those records hold no device, and gc has nothing to compare - so it must judge
// them the way it always did rather than skip them. Skipping is the choice that
// sounds safer and is worse: no existing record carries a device, so gc would
// stop collecting anything at all on every install that upgrades, and would do
// it without saying why.
func TestGCCollectsWhenTheProjectHasNoRecordedDevice(t *testing.T) {
	storeRoot, database := newGCStore(t)

	project := resolvedDir(t, filepath.Join(t.TempDir(), "myproject"))
	proj, _ := seedProjectAndPackage(t, database, storeRoot, project, "legacy-pkg")
	// A record as an older lnpm would have written it.
	if err := database.SetProjectDevice(proj.ID, 0); err != nil {
		t.Fatalf("clear the recorded device: %v", err)
	}
	if err := os.RemoveAll(project); err != nil {
		t.Fatalf("delete project: %v", err)
	}

	out := captureStdout(t, func() {
		if err := RunGC(false, "", true, true); err != nil {
			t.Errorf("RunGC() error = %v", err)
		}
	})
	t.Logf("gc output:\n%s", out)

	if names := packageNames(t, database); len(names) != 0 {
		t.Errorf("gc stopped collecting from a record written before the device field existed; packages left: %v", names)
	}
}

// TestGCTreatsAPermissionErrorAsLive pins the fail-safe that predates #335. The
// check tests specifically for a not-exist error, so a permission or I/O failure
// falls through to treating the project as live.
//
// The denial is on the parent, not the project directory: a directory with mode
// 0000 still stats fine, because stat needs only search permission on the path
// leading to it.
//
// Skipped on Windows for the reason
// TestClassifyProjectDirTreatsANonExistErrorAsLive gives: the fixture cannot be
// built there, because a mode maps to the read-only attribute and does not deny
// traversal.
func TestGCTreatsAPermissionErrorAsLive(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping on Windows - a directory mode of 0000 maps to the read-only attribute, which still permits traversal, so no permission error can be produced")
	}
	if os.Getuid() == 0 {
		t.Skip("root ignores the permission bits this test relies on")
	}
	storeRoot, database := newGCStore(t)

	project := resolvedDir(t, filepath.Join(t.TempDir(), "locked", "myproject"))
	parent := filepath.Dir(project)
	seedProjectAndPackage(t, database, storeRoot, project, "guarded-pkg")

	if err := os.Chmod(parent, 0000); err != nil {
		t.Fatalf("deny search on the parent: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0755) })

	// Confirm the fixture really produces a non-ENOENT error, rather than
	// passing because the denial silently did not take.
	_, statErr := os.Stat(project)
	if statErr == nil || os.IsNotExist(statErr) {
		t.Fatalf("the fixture did not produce a permission error: %v", statErr)
	}

	out := captureStdout(t, func() {
		if err := RunGC(false, "", true, true); err != nil {
			t.Errorf("RunGC() error = %v", err)
		}
	})
	t.Logf("gc output:\n%s", out)

	if names := packageNames(t, database); len(names) != 1 {
		t.Errorf("gc collected a package whose project directory could not be read for want of permission; packages left: %v", names)
	}
}

// TestGCRestampsTheDeviceOfAProjectItCanStat pins the refresh that keeps a
// recorded device from going stale.
//
// Without it the value is only as good as the mount in place when the project
// was added. An anonymous device number is not stable across a remount - a tmpfs
// unmounted and remounted after two other mounts had taken slots was measured
// moving from 163 to 214 on Linux 6.12 - and a record left to drift makes gc
// decline that project for good, leaking its space silently.
func TestGCRestampsTheDeviceOfAProjectItCanStat(t *testing.T) {
	storeRoot, database := newGCStore(t)

	project := resolvedTempDir(t)
	proj, _ := seedProjectAndPackage(t, database, storeRoot, project, "live-pkg")
	real := proj.Device
	if real == 0 {
		t.Skip("this platform reports no device")
	}
	if err := database.SetProjectDevice(proj.ID, real+1); err != nil {
		t.Fatalf("seed a stale device: %v", err)
	}

	captureStdout(t, func() {
		if err := RunGC(false, "", true, true); err != nil {
			t.Errorf("RunGC() error = %v", err)
		}
	})

	stored, err := database.GetProjectByID(proj.ID)
	if err != nil || stored == nil {
		t.Fatalf("GetProjectByID = %v, %v", stored, err)
	}
	if stored.Device != real {
		t.Errorf("gc left the stale device %d on a project it stat'd successfully, want %d", stored.Device, real)
	}
}

// TestDoctorDoesNotCallAnUnreachableProjectAnOrphanedLink covers the same check
// in doctor, which shares gc's helper.
//
// doctor is not destructive, but it is what sends a user to the command that is:
// it counted an unreachable project as an orphaned link and advised running
// 'lnpm gc --fix-links'. Following that advice does nothing now that gc declines
// the same link, so leaving doctor alone would have replaced a data-loss bug
// with a bad instruction.
func TestDoctorDoesNotCallAnUnreachableProjectAnOrphanedLink(t *testing.T) {
	storeRoot, database := newGCStore(t)
	seedUnreachableProject(t, database, storeRoot, "offline-pkg")

	out, _ := runDoctor(t, false)
	t.Logf("doctor output:\n%s", out)

	if strings.Contains(out, "orphaned link(s)") {
		t.Errorf("doctor called an unreachable project an orphaned link; output was:\n%s", out)
	}
	if strings.Contains(out, "gc --fix-links") {
		t.Errorf("doctor advised the destructive command for a project that is only unreachable; output was:\n%s", out)
	}
	if !strings.Contains(out, "could not be checked") {
		t.Errorf("doctor did not report the link it could not check; output was:\n%s", out)
	}
}

// TestGCDryRunDoesNotRestampTheDevice pins that the orphan scan's one write
// stays behind the dry-run flag.
//
// gc --dry-run prints "no changes will be made" before it reads anything, and
// commands.go documents the flag as showing what would be removed. The device
// re-stamp is a database write like the three below it, and an unguarded one
// made that printed line false - quietly, since nothing in the output would
// mention it.
func TestGCDryRunDoesNotRestampTheDevice(t *testing.T) {
	storeRoot, database := newGCStore(t)

	project := resolvedTempDir(t)
	proj, _ := seedProjectAndPackage(t, database, storeRoot, project, "live-pkg")
	real := proj.Device
	if real == 0 {
		t.Skip("this platform reports no device")
	}
	stale := real + 1
	if err := database.SetProjectDevice(proj.ID, stale); err != nil {
		t.Fatalf("seed a stale device: %v", err)
	}

	out := captureStdout(t, func() {
		if err := RunGC(true, "", true, true); err != nil {
			t.Errorf("RunGC() error = %v", err)
		}
	})
	if !strings.Contains(out, "no changes will be made") {
		t.Fatalf("the fixture did not take the dry-run path; output was:\n%s", out)
	}

	stored, err := database.GetProjectByID(proj.ID)
	if err != nil || stored == nil {
		t.Fatalf("GetProjectByID = %v, %v", stored, err)
	}
	if stored.Device != stale {
		t.Errorf("gc --dry-run rewrote the recorded device from %d to %d after printing that it would change nothing", stale, stored.Device)
	}
}
