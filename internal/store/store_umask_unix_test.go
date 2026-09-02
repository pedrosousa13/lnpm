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
// check: a 0755 file stored under umask 0077 must still be executable in the
// store. Store tries a reflink clone before falling back to a copy, so it logs
// which one ran — a passing run only covers the path it actually took.
//
// The mode wanted is 0555 rather than the 0755 the hash and the database
// record, because Store takes the write bits off an entry's content on the way
// in (#333). The two failures this separates are still distinct: the umask
// deciding the mode lands the file at 0700 and then 0500, so the execute bit —
// which is the bit this test exists for — is missing either way it goes wrong.
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
	if got := info.Mode().Perm(); got != 0555 {
		t.Errorf("Stored file mode = %04o, want 0555 (the umask masked the mode instead of the store setting it, or the protection took more than the write bits)", got)
	}
}

// TestStore_PreservesModeUmaskWouldStrip tests that the stored file carries the
// permission bits of the source, even bits the process umask would mask out of
// an open(2) mode argument — all of them except the write bits, which Store
// takes off an entry's content deliberately (#333).
//
// Why the mode the store ends up with matters at all: pack folds Mode.Perm()
// into the content hash, so bits lost between the source and the store are bits
// the entry is no longer filed under the hash of. The write bits are now lost on
// purpose and are the one exception to that — writeBits' comment in protect.go
// carries what a re-hashing check has to do about it — which is precisely why
// every other bit still has to arrive intact.
//
// It lives here, forcing 0077, rather than in store_permissions_test.go under
// the ambient umask, and that move is #333's doing. A umask of 0022 or 0002
// masks only write bits, the protection removes exactly those, and a store that
// let the umask decide became indistinguishable from one that did not — on
// nearly every machine the suite runs on. Under 0077 the group and other read
// bits are masked too and survive the protection, so the comparison can fail
// again: the wanted 0444 becomes 0400 if the umask is what decides.
//
// It is a different fixture from its 0755 neighbour above, and both are worth
// keeping: this one is a mode with no execute bits, so what it watches is the
// read bits rather than the executability of a bin script.
func TestStore_PreservesModeUmaskWouldStrip(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LNPM_STORE", tmpDir)

	s, err := New()
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	sourceDir := filepath.Join(tmpDir, "source")
	if err := os.MkdirAll(sourceDir, 0755); err != nil {
		t.Fatalf("Failed to create source dir: %v", err)
	}

	sourceFile := filepath.Join(sourceDir, "group-writable.js")
	if err := os.WriteFile(sourceFile, []byte("test content"), 0644); err != nil {
		t.Fatalf("Failed to create source file: %v", err)
	}
	// WriteFile's mode is umask-masked too, so set the bits explicitly.
	if err := os.Chmod(sourceFile, 0666); err != nil {
		t.Fatalf("Failed to chmod source file: %v", err)
	}

	files := []*pack.FileInfo{
		{
			RelPath:     "group-writable.js",
			Path:        sourceFile,
			Size:        12,
			Mode:        0666,
			ContentHash: "umask123",
		},
	}

	setUmask(t, 0077)

	destPath, err := s.Store("test-pkg", "umask-hash", files, sourceDir)
	if err != nil {
		t.Fatalf("Failed to store: %v", err)
	}

	info, err := os.Stat(filepath.Join(destPath, "group-writable.js"))
	if err != nil {
		t.Fatalf("Failed to stat stored file: %v", err)
	}

	if got := info.Mode().Perm(); got != 0666&^writeBits {
		t.Errorf("Stored mode %04o, want %04o - the umask masked the mode instead of the store setting it", got, 0666&^writeBits)
	}
}
