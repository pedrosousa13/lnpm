//go:build !windows

package link

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

// TestCopyFile_PreservesModeUnderRestrictiveUmask pins the linker's copy path —
// the non-reflink, non-hardlink way a file reaches a consumer's .lnpm/<pkg>. It
// calls copyFile directly so no other materialisation path can stand in for it.
// os.OpenFile's mode argument is masked by the umask, so without an explicit
// chmod a 0755 store entry is copied forward as 0700 under umask 0077 and the
// consumer's node_modules/.bin entry fails to execute.
func TestCopyFile_PreservesModeUnderRestrictiveUmask(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "cli.js")
	dst := filepath.Join(dir, "copied-cli.js")

	if err := os.WriteFile(src, []byte("#!/usr/bin/env node\n"), 0644); err != nil {
		t.Fatalf("Failed to write source: %v", err)
	}
	// chmod is not subject to the umask, so the fixture really is 0755.
	if err := os.Chmod(src, 0755); err != nil {
		t.Fatalf("Failed to chmod source: %v", err)
	}

	setUmask(t, 0077)

	if err := copyFile(src, dst); err != nil {
		t.Fatalf("copyFile failed: %v", err)
	}

	info, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("Failed to stat copied file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0755 {
		t.Errorf("Copied file mode = %04o, want 0755 (the umask masked the mode instead of the copy setting it)", got)
	}
}
