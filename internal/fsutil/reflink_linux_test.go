//go:build linux

package fsutil

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
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
