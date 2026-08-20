//go:build !windows

package store

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/fsutil"
	"github.com/pedrosousa13/lnpm/internal/pack"
)

// setUmask sets the process umask for the rest of the test and restores the
// previous value afterwards. The umask is process-global, so a test using this
// must not call t.Parallel() and must not run alongside one that does.
func setUmask(t *testing.T, mask int) {
	t.Helper()
	old := syscall.Umask(mask)
	t.Cleanup(func() { syscall.Umask(old) })
}

// TestCopyFile_PreservesModeUnderRestrictiveUmask pins the store's copy path.
// It calls copyFile directly, so the reflink path cannot stand in for it. The
// mode argument to os.OpenFile is masked by the umask, so without an explicit
// chmod a 0755 bin script lands at 0700 under umask 0077 while the content hash
// and the database still record 0755.
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

	if err := copyFile(src, dst, 0755); err != nil {
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

// TestStore_PreservesModeUnderRestrictiveUmask is the end-to-end acceptance
// check: a 0755 file stored under umask 0077 must be executable in the store,
// because both the content hash and the database record 0755. Store tries a
// reflink clone before falling back to a copy, so it logs which one ran — a
// passing run only covers the path it actually took.
func TestStore_PreservesModeUnderRestrictiveUmask(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LNPM_STORE", filepath.Join(tmpDir, "store"))

	s, err := New()
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	sourceDir := filepath.Join(tmpDir, "source")
	if err := os.MkdirAll(filepath.Join(sourceDir, "bin"), 0755); err != nil {
		t.Fatalf("Failed to create source dir: %v", err)
	}
	srcFile := filepath.Join(sourceDir, "bin", "cli.js")
	content := []byte("#!/usr/bin/env node\n")
	if err := os.WriteFile(srcFile, content, 0644); err != nil {
		t.Fatalf("Failed to write source: %v", err)
	}
	if err := os.Chmod(srcFile, 0755); err != nil {
		t.Fatalf("Failed to chmod source: %v", err)
	}

	// Record which path Store will take, so the test says what it covered.
	probe := filepath.Join(tmpDir, "reflink-probe")
	reflinkSupported := fsutil.Reflink(srcFile, probe) == nil
	if reflinkSupported {
		t.Log("filesystem supports reflink: this run covers the clone path, not copyFile")
	} else {
		t.Log("filesystem does not support reflink: this run covers the copyFile path")
	}

	files := []*pack.FileInfo{{
		RelPath:     "bin/cli.js",
		Path:        srcFile,
		Size:        int64(len(content)),
		Mode:        0755,
		ContentHash: "hash-cli",
	}}

	setUmask(t, 0077)

	destPath, err := s.Store("umask-pkg", "hash123", files, sourceDir)
	if err != nil {
		t.Fatalf("Failed to store package: %v", err)
	}

	info, err := os.Stat(filepath.Join(destPath, "bin", "cli.js"))
	if err != nil {
		t.Fatalf("Failed to stat stored file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0755 {
		t.Errorf("Stored file mode = %04o, want 0755 (the store disagrees with the mode it hashed and recorded)", got)
	}
}
