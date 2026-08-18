package store

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/pack"
)

// TestStore_DoesNotShareInodeWithSource verifies the store owns its own bytes:
// a stored file must never be a hard link to the developer's source file.
// os.SameFile is the portable identity check (inode on unix, volume + file
// index on Windows), so this assertion holds on every platform.
func TestStore_DoesNotShareInodeWithSource(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LNPM_STORE", tmpDir)

	store, err := New()
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	sourceDir := filepath.Join(tmpDir, "source")
	if err := os.MkdirAll(sourceDir, 0755); err != nil {
		t.Fatalf("Failed to create source dir: %v", err)
	}

	sourceFile := filepath.Join(sourceDir, "index.js")
	if err := os.WriteFile(sourceFile, []byte("original content"), 0644); err != nil {
		t.Fatalf("Failed to create source file: %v", err)
	}

	files := []*pack.FileInfo{
		{
			RelPath:     "index.js",
			Path:        sourceFile,
			Size:        int64(len("original content")),
			Mode:        0644,
			ContentHash: "inode123",
		},
	}

	destPath, err := store.Store("test-pkg", "inode-hash", files, sourceDir)
	if err != nil {
		t.Fatalf("Failed to store: %v", err)
	}

	sourceInfo, err := os.Stat(sourceFile)
	if err != nil {
		t.Fatalf("Failed to stat source file: %v", err)
	}
	storedInfo, err := os.Stat(filepath.Join(destPath, "index.js"))
	if err != nil {
		t.Fatalf("Failed to stat stored file: %v", err)
	}

	if os.SameFile(sourceInfo, storedInfo) {
		t.Error("Stored file is the same file as the source - external writes to the source would corrupt the store")
	}
}

// TestStore_SourceMutationDoesNotAffectStore verifies that rewriting the source
// file in place (as tsc, webpack or an editor would) leaves the store entry's
// bytes untouched.
func TestStore_SourceMutationDoesNotAffectStore(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping on Windows - rewriting a file in place is blocked while other handles hold it open")
	}

	tmpDir := t.TempDir()
	t.Setenv("LNPM_STORE", tmpDir)

	store, err := New()
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	sourceDir := filepath.Join(tmpDir, "source")
	if err := os.MkdirAll(sourceDir, 0755); err != nil {
		t.Fatalf("Failed to create source dir: %v", err)
	}

	sourceFile := filepath.Join(sourceDir, "index.js")
	original := []byte("original content")
	if err := os.WriteFile(sourceFile, original, 0644); err != nil {
		t.Fatalf("Failed to create source file: %v", err)
	}

	files := []*pack.FileInfo{
		{
			RelPath:     "index.js",
			Path:        sourceFile,
			Size:        int64(len(original)),
			Mode:        0644,
			ContentHash: "mutate123",
		},
	}

	destPath, err := store.Store("test-pkg", "mutate-hash", files, sourceDir)
	if err != nil {
		t.Fatalf("Failed to store: %v", err)
	}

	// Rewrite the source in place, without unlinking it, exactly as a build
	// tool writing over its own output would.
	f, err := os.OpenFile(sourceFile, os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		t.Fatalf("Failed to open source for rewrite: %v", err)
	}
	if _, err := f.Write([]byte("MUTATED CONTENT")); err != nil {
		_ = f.Close()
		t.Fatalf("Failed to rewrite source: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Failed to close source: %v", err)
	}

	stored, err := os.ReadFile(filepath.Join(destPath, "index.js"))
	if err != nil {
		t.Fatalf("Failed to read stored file: %v", err)
	}

	if string(stored) != string(original) {
		t.Errorf("Store entry changed when source was rewritten: got %q, want %q", string(stored), string(original))
	}
}
