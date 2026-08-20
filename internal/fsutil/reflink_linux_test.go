//go:build linux

package fsutil

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

// setUmask sets the process umask for the rest of the test and restores the
// previous value afterwards. The umask is process-global, so a test using this
// must not call t.Parallel() and must not run alongside one that does.
func setUmask(t *testing.T, mask int) {
	t.Helper()
	old := syscall.Umask(mask)
	t.Cleanup(func() { syscall.Umask(old) })
}

// TestReflink_PreservesModeUnderRestrictiveUmask pins the FICLONE path. The
// destination file is created with os.OpenFile, whose mode argument the umask
// masks, and FICLONE clones data blocks only — never permissions — so without
// an explicit chmod a 0755 source is cloned to a 0700 destination under umask
// 0077. The test asserts that tryReflink returned true, so the mode it then
// checks belongs to a file the ioctl really cloned rather than one left behind
// by a clone that failed.
func TestReflink_PreservesModeUnderRestrictiveUmask(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "cli.js")
	writeFile(t, src, "#!/usr/bin/env node\n")
	// chmod is not subject to the umask, so the fixture really is 0755.
	if err := os.Chmod(src, 0755); err != nil {
		t.Fatalf("Failed to chmod source: %v", err)
	}

	probe := filepath.Join(dir, "probe.js")
	if !tryReflink(src, probe) {
		t.Skip("Skipping - the filesystem backing t.TempDir() does not support the FICLONE ioctl")
	}

	dst := filepath.Join(dir, "cloned-cli.js")

	setUmask(t, 0077)

	if !tryReflink(src, dst) {
		t.Fatal("tryReflink failed even though the probe clone succeeded, so this run proves nothing about the clone path")
	}

	info, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("Failed to stat cloned file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0755 {
		t.Errorf("Cloned file mode = %04o, want 0755 (FICLONE does not clone permissions, so the umask-masked mode persisted)", got)
	}
}

// assertNoStrayDestination asserts that a failed tryReflink left nothing at
// path. A leftover destination is not harmless: the caller reacts to the
// failure by falling back to os.Link, which then fails with EEXIST against the
// stray file, or to a copy, which silently overwrites it.
func assertNoStrayDestination(t *testing.T, path, failure string) {
	t.Helper()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("After a %s tryReflink left a destination behind at %s (stat error = %v, want os.ErrNotExist); the caller's os.Link fallback would fail with EEXIST", failure, path, err)
	}
}

// stubFiclone replaces the FICLONE syscall for the rest of the test and
// restores the previous value afterwards. The clone either works on the
// filesystem under the test or it does not, so on one without copy-on-write
// support — ext4, the usual dev and CI host — the chmod and Sync steps after
// the ioctl are not otherwise reachable at all.
//
// ficlone is package-global, and production reads it from worker goroutines, so
// a test using this must not call t.Parallel() and must not run alongside one
// that does.
func stubFiclone(t *testing.T, fn func(dstFd, srcFd uintptr) syscall.Errno) {
	t.Helper()
	old := ficlone
	ficlone = fn
	t.Cleanup(func() { ficlone = old })
}

// redirectFD points an already-open descriptor at a different file, so that the
// next operation on the *os.File holding it hits that file instead. It is how
// the tests below make a specific post-clone step fail while leaving the steps
// before it working.
func redirectFD(t *testing.T, fd uintptr, path string) {
	t.Helper()
	replacement, err := syscall.Open(path, syscall.O_RDWR|syscall.O_NONBLOCK, 0)
	if err != nil {
		t.Fatalf("Failed to open %s: %v", path, err)
	}
	defer func() { _ = syscall.Close(replacement) }()
	// Dup3 rather than Dup2: this file builds on every linux architecture, and
	// syscall.Dup2 does not exist on arm64, which the release builds target.
	if err := unix.Dup3(replacement, int(fd), 0); err != nil {
		t.Fatalf("Failed to point fd %d at %s: %v", fd, path, err)
	}
}

// TestTryReflink_RemovesDestinationOnSyncFailure covers the failure this issue
// is really about: the ioctl clones the data, so a destination now exists on
// disk, and then the flush fails. tryReflink reports failure, so it owns the
// file it created and must unlink it.
//
// Reaching that branch needs a clone that succeeds followed by a Sync that
// fails, and no filesystem does both. The stub supplies the successful clone;
// pointing the destination descriptor at a FIFO supplies the failing flush,
// because fsync on a FIFO always returns EINVAL. A FIFO is also the one
// substitute that leaves the preceding fchmod working, so this test fails on
// the Sync step rather than short-circuiting at the chmod above it.
func TestTryReflink_RemovesDestinationOnSyncFailure(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	writeFile(t, src, "payload")
	dst := filepath.Join(dir, "dst.txt")

	fifo := filepath.Join(dir, "fifo")
	if err := syscall.Mkfifo(fifo, 0644); err != nil {
		t.Fatalf("Failed to create FIFO: %v", err)
	}

	stubFiclone(t, func(dstFd, srcFd uintptr) syscall.Errno {
		redirectFD(t, dstFd, fifo)
		return 0
	})

	if tryReflink(src, dst) {
		t.Fatal("tryReflink reported success even though the post-clone Sync failed")
	}
	assertNoStrayDestination(t, dst, "post-clone Sync failure")
}

// TestTryReflink_RemovesDestinationOnChmodFailure covers the other post-clone
// step. FICLONE copies data blocks and never permissions, so tryReflink chmods
// the clone to the source's exact mode; if that fails the clone is left with
// the wrong bits and must be removed rather than handed to the caller.
//
// /dev/null is owned by root, so fchmod on it fails with EPERM for any other
// user — which makes it a destination whose chmod cannot succeed.
func TestTryReflink_RemovesDestinationOnChmodFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("Skipping - running as root, which can chmod /dev/null, so this test cannot force the chmod to fail")
	}

	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	writeFile(t, src, "payload")
	dst := filepath.Join(dir, "dst.txt")

	stubFiclone(t, func(dstFd, srcFd uintptr) syscall.Errno {
		redirectFD(t, dstFd, os.DevNull)
		return 0
	})

	if tryReflink(src, dst) {
		t.Fatal("tryReflink reported success even though the post-clone Chmod failed")
	}
	assertNoStrayDestination(t, dst, "post-clone Chmod failure")
}

// TestTryReflink_RemovesDestinationOnIoctlFailure covers the failure a
// filesystem without copy-on-write support produces on its own, with the real
// ioctl and no stubbing. tryReflink creates the destination before it knows
// whether the clone is possible, so the ioctl failing is the ordinary case
// where it has a file to clean up. The os.Link at the end is the caller's
// actual fallback, and it is what a leftover destination would break.
func TestTryReflink_RemovesDestinationOnIoctlFailure(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	writeFile(t, src, "payload")

	probe := filepath.Join(dir, "probe.txt")
	if tryReflink(src, probe) {
		t.Skip("Skipping - the filesystem backing t.TempDir() supports FICLONE, so the ioctl-failure path is unreachable here")
	}

	dst := filepath.Join(dir, "dst.txt")
	if tryReflink(src, dst) {
		t.Fatal("tryReflink reported success even though the probe clone failed")
	}
	assertNoStrayDestination(t, dst, "FICLONE ioctl failure")

	if err := os.Link(src, dst); err != nil {
		t.Errorf("The caller's os.Link fallback failed after tryReflink: %v", err)
	}
}
