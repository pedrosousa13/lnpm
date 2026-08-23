package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/fsutil"
)

// deviceOf returns the device of an existing path, skipping the test if the
// platform reports none. Every case below turns on comparing two devices, so a
// platform that reports none has nothing for these tests to assert.
//
// That skip has a consequence worth knowing before trusting a green run: if
// fsutil.DeviceIDOfPath ever returned 0 everywhere - which is what the Windows
// implementation did before #335 - every test in this file and every
// protection-side test in gc_unreachable_test.go SKIPS rather than fails, and
// the package still reports ok. Verified by making the Unix implementation
// return 0 and re-running: eight tests here skipped, two passed, none failed.
//
// So the guard against that regression is not here. It is
// TestDeviceIDOfPathIsNonZeroAndSharedWithinOneFilesystem in internal/fsutil,
// which asserts a non-zero device on every platform and is the only thing that
// goes red. Do not weaken it.
func deviceOf(t *testing.T, path string) uint64 {
	t.Helper()
	dev := fsutil.DeviceIDOfPath(path)
	if dev == 0 {
		t.Skipf("this platform reports no device for %s", path)
	}
	return dev
}

// TestClassifyProjectDirSeesALiveProject pins the ordinary case, and that the
// device it reports back is the one gc re-stamps the record with.
func TestClassifyProjectDirSeesALiveProject(t *testing.T) {
	project := t.TempDir()
	want := deviceOf(t, project)

	state, observed := classifyProjectDir(project, want)
	if state != projectLive {
		t.Errorf("classifyProjectDir(live project) = %v, want projectLive", state)
	}
	if observed != want {
		t.Errorf("observed device = %d, want %d", observed, want)
	}
}

// TestClassifyProjectDirReportsTheDeviceEvenWhenTheRecordDisagrees is what makes
// the re-stamp work. A project that stats fine is live whatever was recorded,
// and the device it is actually on has to come back so the stale record can be
// corrected - otherwise a remounted filesystem stays mis-recorded forever and
// its projects are never collectable again.
func TestClassifyProjectDirReportsTheDeviceEvenWhenTheRecordDisagrees(t *testing.T) {
	project := t.TempDir()
	actual := deviceOf(t, project)

	state, observed := classifyProjectDir(project, actual+1)
	if state != projectLive {
		t.Errorf("classifyProjectDir(live project, stale record) = %v, want projectLive", state)
	}
	if observed != actual {
		t.Errorf("observed device = %d, want the device the path is really on, %d", observed, actual)
	}
}

// TestClassifyProjectDirTreatsANonExistErrorAsLive pins the fail-safe that was
// already there before #335 and must survive it. The check tests specifically
// for a not-exist error, so a permission or I/O failure establishes nothing and
// the link is kept.
//
// The denial goes on the parent, not the project directory: a directory with
// mode 0000 still stats fine, because stat needs only search permission on the
// path leading to it.
//
// There is no portable fixture for this. Windows has no mode bits: os.Chmod maps
// a mode to the read-only attribute, which does not deny traversal, so the
// denial silently does not take and the guard below catches it - which is how
// this surfaced on CI rather than as a confusing failure. Standing a regular
// file in for a parent directory was considered as a portable way to force a
// non-ENOENT error (ENOTDIR on Unix), and rejected: whether Windows reports that
// as not-exist could not be established from here, and guessing it would put the
// same failure back on a later CI run. So the property is asserted on Unix,
// where it is real, and this says plainly that Windows does not test it.
func TestClassifyProjectDirTreatsANonExistErrorAsLive(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping on Windows - a directory mode of 0000 maps to the read-only attribute, which still permits traversal, so no permission error can be produced")
	}
	if os.Getuid() == 0 {
		t.Skip("root ignores the permission bits this test relies on")
	}
	parent := filepath.Join(t.TempDir(), "locked")
	project := filepath.Join(parent, "myproject")
	if err := os.MkdirAll(project, 0755); err != nil {
		t.Fatalf("seed: %v", err)
	}
	recorded := deviceOf(t, project)
	if err := os.Chmod(parent, 0000); err != nil {
		t.Fatalf("deny search: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0755) })

	// Confirm the fixture produces the error it means to, rather than passing
	// because the denial did not take.
	if _, err := os.Stat(project); err == nil || os.IsNotExist(err) {
		t.Fatalf("the fixture did not produce a permission error: %v", err)
	}

	state, observed := classifyProjectDir(project, recorded)
	if state != projectLive {
		t.Errorf("classifyProjectDir(permission denied) = %v, want projectLive", state)
	}
	if observed != 0 {
		t.Errorf("observed device = %d, want 0: nothing was successfully stat'd", observed)
	}
}

// TestClassifyProjectDirCallsAMissingProjectGoneWhenTheDeviceMatches is
// acceptance criterion 2. The filesystem that held the project is mounted where
// it should be, and the project is not on it, so it was deleted.
func TestClassifyProjectDirCallsAMissingProjectGoneWhenTheDeviceMatches(t *testing.T) {
	parent := t.TempDir()
	project := filepath.Join(parent, "myproject")
	if err := os.MkdirAll(project, 0755); err != nil {
		t.Fatalf("seed: %v", err)
	}
	recorded := deviceOf(t, project)
	if err := os.RemoveAll(project); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if state, _ := classifyProjectDir(project, recorded); state != projectGone {
		t.Errorf("classifyProjectDir(deleted, device matches) = %v, want projectGone", state)
	}
}

// TestClassifyProjectDirCallsAMissingProjectGoneWithNoRecordedDevice pins the
// fallback that keeps every database written before #335 working.
//
// An unknown device must mean "judge it the way lnpm always did", never "skip".
// Skipping would be the safer-sounding choice and is the wrong one: no existing
// record carries a device, so gc would stop collecting anything at all on every
// install that upgrades, and would do it silently.
func TestClassifyProjectDirCallsAMissingProjectGoneWithNoRecordedDevice(t *testing.T) {
	parent := t.TempDir()
	project := filepath.Join(parent, "myproject")

	if state, _ := classifyProjectDir(project, 0); state != projectGone {
		t.Errorf("classifyProjectDir(deleted, no recorded device) = %v, want projectGone", state)
	}
}

// TestClassifyProjectDirDeclinesWhenTheRecordedDeviceIsNotMounted is the #335
// bug, at the level of the rule.
//
// The fixture supplies the condition directly - a recorded device that is not
// the one now mounted where the project should be - rather than by unmounting
// anything, because the CI runners this has to pass on cannot mount. That the
// condition is what a real unmount produces is established separately, by
// TestGCKeepsAPackageAcrossARealUnmount where a namespace is available.
func TestClassifyProjectDirDeclinesWhenTheRecordedDeviceIsNotMounted(t *testing.T) {
	parent := t.TempDir()
	project := filepath.Join(parent, "myproject")
	elsewhere := deviceOf(t, parent) + 1

	if state, _ := classifyProjectDir(project, elsewhere); state != projectUnreachable {
		t.Errorf("classifyProjectDir(missing, recorded device absent) = %v, want projectUnreachable", state)
	}
}

// TestClassifyProjectDirClimbsToAnUnreachableMountPoint is what makes the climb
// past missing ancestors load-bearing, and it is the case that matters most in
// practice: a project is rarely at the root of a drive, it is at
// /mnt/drive/code/myproject.
//
// With the drive unmounted, the immediate parent (.../code) does not exist
// either. Stopping there finds no device, and falling back to the pre-fix
// behaviour would delete the store entry - reintroducing the whole bug for any
// project nested more than one level below its mount point. Climbing reaches the
// mount point, whose device is the parent filesystem's and not the recorded one,
// and the link is kept.
//
// This test was added because a revert check found the case below passing
// against the wrong implementation. Keeping only that one would have left the
// climb untested while looking covered.
func TestClassifyProjectDirClimbsToAnUnreachableMountPoint(t *testing.T) {
	mountPoint := filepath.Join(t.TempDir(), "external")
	if err := os.MkdirAll(mountPoint, 0755); err != nil {
		t.Fatalf("seed the mount point: %v", err)
	}
	// The drive is not mounted, so nothing under the mount point exists.
	project := filepath.Join(mountPoint, "code", "myproject")
	recorded := deviceOf(t, mountPoint) + 1

	if state, _ := classifyProjectDir(project, recorded); state != projectUnreachable {
		t.Errorf("classifyProjectDir(nested under an unmounted mount point) = %v, want projectUnreachable", state)
	}
}

// TestClassifyProjectDirUsesTheNearestExistingAncestor pins the verdict for a
// whole tree of projects deleted at once, which takes their shared parent with
// it.
//
// It does NOT prove the climb happens, and a revert check confirmed that:
// replacing the climb with the immediate parent leaves this green. The immediate
// parent is gone, so that spelling reads no device and falls back to the pre-fix
// behaviour, which is projectGone - the same answer this wants for a different
// reason. TestClassifyProjectDirClimbsToAnUnreachableMountPoint above is the one
// that pins the climb. This is kept because the verdict is worth pinning on its
// own: a deep deletion on a filesystem that is still mounted must stay
// collectable.
func TestClassifyProjectDirUsesTheNearestExistingAncestor(t *testing.T) {
	base := t.TempDir()
	project := filepath.Join(base, "a", "b", "myproject")
	if err := os.MkdirAll(project, 0755); err != nil {
		t.Fatalf("seed: %v", err)
	}
	recorded := deviceOf(t, project)
	if err := os.RemoveAll(filepath.Join(base, "a")); err != nil {
		t.Fatalf("delete the tree: %v", err)
	}

	if state, _ := classifyProjectDir(project, recorded); state != projectGone {
		t.Errorf("classifyProjectDir(deep deletion) = %v, want projectGone", state)
	}
}
