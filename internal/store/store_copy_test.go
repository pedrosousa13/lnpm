package store

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestCopyFile covers the store-side copyFile directly.
//
// It is exercised indirectly today through Store.Store's copy fallback, but only
// when reflink is unavailable, so its behaviour is not pinned anywhere. Forcing
// the fallback from Store.Store deliberately would need a cross-filesystem
// setup, which is not portable; a direct unit test is the practical target.
func TestCopyFile(t *testing.T) {
	// The modes are chosen for what they can catch. 0777 is masked by any
	// non-zero umask when OpenFile creates the file, so asserting it pins
	// copyFile's explicit Chmod rather than just the mode it passed to
	// OpenFile — the assertion holds under the harness umask without the test
	// having to set one (os.Chmod on the destination afterwards would be
	// pointless here, since the destination's mode is the thing under test).
	// 0600 catches a copyFile that ignored its mode argument in favour of a
	// fixed one.
	for _, mode := range []fs.FileMode{0777, 0600} {
		t.Run(mode.String(), func(t *testing.T) {
			tmpDir := t.TempDir()
			srcPath := filepath.Join(tmpDir, "source.txt")
			dstPath := filepath.Join(tmpDir, "dest.txt")

			content := "test content for the store copy"
			// The source is written with a deliberately different mode, so a
			// copyFile that read the source's permissions instead of using the
			// mode argument would be caught too.
			if err := os.WriteFile(srcPath, []byte(content), 0644); err != nil {
				t.Fatal(err)
			}

			if err := copyFile(srcPath, dstPath, mode); err != nil {
				t.Fatalf("copyFile() error: %v", err)
			}

			data, err := os.ReadFile(dstPath)
			if err != nil {
				t.Fatalf("failed to read copied file: %v", err)
			}
			if string(data) != content {
				t.Errorf("copied content = %q, want %q", string(data), content)
			}

			if runtime.GOOS == "windows" {
				// Windows exposes only a read-only bit, not Unix permissions.
				return
			}
			info, err := os.Stat(dstPath)
			if err != nil {
				t.Fatal(err)
			}
			if got := info.Mode().Perm(); got != mode {
				t.Errorf("destination mode = %o, want %o", got, mode)
			}
		})
	}
}

func TestCopyFileMissingSource(t *testing.T) {
	tmpDir := t.TempDir()
	srcPath := filepath.Join(tmpDir, "does-not-exist.txt")
	dstPath := filepath.Join(tmpDir, "dest.txt")

	err := copyFile(srcPath, dstPath, 0644)
	if err == nil {
		t.Fatal("copyFile() with a missing source succeeded, want an error")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("copyFile() error = %v, want a not-exist error", err)
	}
	if !strings.Contains(err.Error(), "does-not-exist.txt") {
		t.Errorf("copyFile() error = %v, want it to name the missing source", err)
	}
	// The source is opened first, so a missing source must leave no destination
	// behind for a later step to mistake for a copied file.
	if _, err := os.Lstat(dstPath); !os.IsNotExist(err) {
		t.Errorf("destination exists after a failed copy (Lstat err = %v), want it absent", err)
	}
}

func TestCopyFileMissingDestinationDir(t *testing.T) {
	tmpDir := t.TempDir()
	srcPath := filepath.Join(tmpDir, "source.txt")
	dstPath := filepath.Join(tmpDir, "no-such-dir", "dest.txt")

	if err := os.WriteFile(srcPath, []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}

	err := copyFile(srcPath, dstPath, 0644)
	if err == nil {
		t.Fatal("copyFile() into a missing directory succeeded, want an error")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("copyFile() error = %v, want a not-exist error", err)
	}
	if !strings.Contains(err.Error(), "no-such-dir") {
		t.Errorf("copyFile() error = %v, want it to name the missing directory", err)
	}
}
