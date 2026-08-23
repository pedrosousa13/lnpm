//go:build linux

package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/fsutil"
)

// mountNamespaceEnv marks the re-executed run that is already inside a mount
// namespace, so the outer run and the inner one can share a test function.
const mountNamespaceEnv = "LNPM_TEST_MOUNT_NAMESPACE"

// TestGCKeepsAPackageAcrossARealUnmount is the only test here that unmounts a
// real filesystem, and it exists to justify the fixture every other test uses.
//
// The portable tests model an unmounted drive with an empty directory and a
// recorded device set by hand. That models what a stat can see exactly - a real
// unmount leaves the mount point present and empty on the parent filesystem -
// but it is a claim about the kernel, not something those tests establish. This
// one establishes it: it mounts a tmpfs, links a package into a project on it,
// unmounts, and runs the real gc.
//
// It cannot run everywhere. Mounting needs privilege, and the CI runners for
// macOS and Windows have none; even on Linux, unprivileged user namespaces are
// restricted on some distributions. So it skips rather than fails, loudly, and
// the portable tests carry the regression on every platform. A skipped test and
// a passing one look alike in a summary, which is why this is not the primary
// proof of the fix - TestGCKeepsAPackageWhoseProjectFilesystemIsNotMounted is.
func TestGCKeepsAPackageAcrossARealUnmount(t *testing.T) {
	if os.Getenv(mountNamespaceEnv) == "" {
		reexecInMountNamespace(t, "TestGCKeepsAPackageAcrossARealUnmount")
		return
	}

	storeRoot, database := newGCStore(t)

	mountPoint := filepath.Join(t.TempDir(), "external")
	if err := os.MkdirAll(mountPoint, 0755); err != nil {
		t.Fatalf("create the mount point: %v", err)
	}
	mustRun(t, "mount", "-t", "tmpfs", "tmpfs", mountPoint)
	mounted := true
	t.Cleanup(func() {
		if mounted {
			_ = exec.Command("umount", mountPoint).Run()
		}
	})

	project := filepath.Join(mountPoint, "myproject")
	if err := os.MkdirAll(project, 0755); err != nil {
		t.Fatalf("create the project on the mounted filesystem: %v", err)
	}
	proj, pkg := seedProjectAndPackage(t, database, storeRoot, project, "offline-pkg")

	// Confirm the mount really is a separate filesystem, so the unmount below
	// changes something. A bind mount within one filesystem shares its device
	// and would make this test pass without testing anything.
	parentDevice := fsutil.DeviceIDOfPath(filepath.Dir(mountPoint))
	if proj.Device == 0 || proj.Device == parentDevice {
		t.Fatalf("the mount did not produce a distinct filesystem: project device %d, parent device %d", proj.Device, parentDevice)
	}
	t.Logf("project recorded on device %d; the mount point's parent is device %d", proj.Device, parentDevice)

	mustRun(t, "umount", mountPoint)
	mounted = false

	// The state the fixture in gc_unreachable_test.go models by hand.
	if _, err := os.Stat(project); !os.IsNotExist(err) {
		t.Fatalf("after the unmount the project path should be not-exist, got %v", err)
	}
	if _, err := os.Stat(mountPoint); err != nil {
		t.Fatalf("after the unmount the mount point should still exist: %v", err)
	}
	if got := fsutil.DeviceIDOfPath(mountPoint); got != parentDevice {
		t.Errorf("after the unmount the mount point reports device %d, want its parent's %d", got, parentDevice)
	}

	out := captureStdout(t, func() {
		if err := RunGC(false, "", true, true); err != nil {
			t.Errorf("RunGC() error = %v", err)
		}
	})
	t.Logf("gc output:\n%s", out)

	if names := packageNames(t, database); len(names) != 1 {
		t.Errorf("gc collected a package across a real unmount; packages left: %v", names)
	}
	if _, err := os.Stat(pkg.StorePath); err != nil {
		t.Errorf("gc removed the store entry %s across a real unmount: %v", pkg.StorePath, err)
	}
}

// reexecInMountNamespace runs one test function again inside a fresh user and
// mount namespace, where an unprivileged user may mount.
//
// The namespace has to be entered by a new process rather than by this one. The
// Go runtime is multithreaded well before a test runs, and unshare(CLONE_NEWNS)
// applies to the calling thread only, so a namespace entered from inside the
// test would not cover the goroutines doing the work.
func reexecInMountNamespace(t *testing.T, name string) {
	t.Helper()

	if _, err := exec.LookPath("unshare"); err != nil {
		t.Skip("unshare is not available, so no mount namespace can be created")
	}
	if err := exec.Command("unshare", "-r", "-m", "true").Run(); err != nil {
		t.Skipf("unprivileged user and mount namespaces are unavailable here: %v", err)
	}

	cmd := exec.Command("unshare", "-r", "-m", os.Args[0], "-test.run", "^"+name+"$", "-test.v")
	cmd.Env = append(os.Environ(), mountNamespaceEnv+"=1")
	out, err := cmd.CombinedOutput()
	t.Logf("inside the mount namespace:\n%s", out)
	if err != nil {
		t.Fatalf("the run inside the mount namespace failed: %v", err)
	}
	// Exit zero is not enough. `go test` exits zero when its -test.run pattern
	// matches nothing at all, and again when the test it matched skipped - so
	// a renamed function or an inner t.Skip would leave this outer test green
	// while nothing had been unmounted. Requiring the pass line is what makes
	// a green here mean the work happened.
	if want := "--- PASS: " + name; !strings.Contains(string(out), want) {
		t.Fatalf("the run inside the mount namespace did not report %q; it matched no test or skipped. Output:\n%s", want, out)
	}
}

func mustRun(t *testing.T, name string, args ...string) {
	t.Helper()
	if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}
