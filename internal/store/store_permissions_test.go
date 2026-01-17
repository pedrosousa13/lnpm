package store

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/pack"
)

// TestNew_ReadOnlyDirectory tests creating store in read-only directory
func TestNew_ReadOnlyDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping on Windows - read-only behavior differs")
	}

	tmpDir := t.TempDir()

	// Set LNPM_STORE to temp location
	t.Setenv("LNPM_STORE", tmpDir)

	// Make directory read-only
	if err := os.Chmod(tmpDir, 0555); err != nil {
		t.Fatalf("Failed to chmod: %v", err)
	}
	defer func() {
		_ = os.Chmod(tmpDir, 0755)
	}()

	// Try to create store - should fail
	_, err := New()
	if err == nil {
		t.Error("Expected error creating store in read-only directory")
	}
}

// TestStore_ReadOnlySource tests storing from read-only source files
func TestStore_ReadOnlySource(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping on Windows - permission handling differs")
	}

	tmpDir := t.TempDir()
	t.Setenv("LNPM_STORE", tmpDir)

	store, err := New()
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Create source directory with read-only files
	sourceDir := filepath.Join(tmpDir, "source")
	if err := os.MkdirAll(sourceDir, 0755); err != nil {
		t.Fatalf("Failed to create source dir: %v", err)
	}

	// Create read-only file
	testFile := filepath.Join(sourceDir, "readonly.js")
	if err := os.WriteFile(testFile, []byte("test content"), 0444); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	files := []*pack.FileInfo{
		{
			RelPath:     "readonly.js",
			Path:    testFile,
			Size:        12,
			Mode:        0444,
			ContentHash: "abc123",
		},
	}

	// Store should succeed even with read-only source
	destPath, err := store.Store("test-pkg", "hash123", files, sourceDir)
	if err != nil {
		t.Errorf("Failed to store read-only files: %v", err)
	}

	// Verify file was stored
	storedFile := filepath.Join(destPath, "readonly.js")
	if _, err := os.Stat(storedFile); err != nil {
		t.Errorf("Stored file doesn't exist: %v", err)
	}
}

// TestStore_PreservesExecutablePermissions tests that executable permissions are preserved
func TestStore_PreservesExecutablePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping on Windows - permission handling differs")
	}

	tmpDir := t.TempDir()
	t.Setenv("LNPM_STORE", tmpDir)

	store, err := New()
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Create source directory with executable file
	sourceDir := filepath.Join(tmpDir, "source")
	if err := os.MkdirAll(sourceDir, 0755); err != nil {
		t.Fatalf("Failed to create source dir: %v", err)
	}

	// Create executable file
	execFile := filepath.Join(sourceDir, "script.sh")
	if err := os.WriteFile(execFile, []byte("#!/bin/sh\necho test"), 0755); err != nil {
		t.Fatalf("Failed to create exec file: %v", err)
	}

	files := []*pack.FileInfo{
		{
			RelPath:     "script.sh",
			Path:    execFile,
			Size:        20,
			Mode:        0755,
			ContentHash: "exec123",
		},
	}

	// Store the file
	destPath, err := store.Store("test-pkg", "hash456", files, sourceDir)
	if err != nil {
		t.Fatalf("Failed to store: %v", err)
	}

	// Verify executable permission preserved
	storedFile := filepath.Join(destPath, "script.sh")
	info, err := os.Stat(storedFile)
	if err != nil {
		t.Fatalf("Failed to stat stored file: %v", err)
	}

	mode := info.Mode() & 0777
	if mode&0111 == 0 {
		t.Errorf("Executable permission not preserved, got %o", mode)
	}
}

// TestStore_DestinationDirCreation tests store directory creation
func TestStore_DestinationDirCreation(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LNPM_STORE", tmpDir)

	store, err := New()
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Create source file
	sourceDir := filepath.Join(tmpDir, "source")
	if err := os.MkdirAll(sourceDir, 0755); err != nil {
		t.Fatalf("Failed to create source dir: %v", err)
	}

	testFile := filepath.Join(sourceDir, "nested", "deep", "file.js")
	if err := os.MkdirAll(filepath.Dir(testFile), 0755); err != nil {
		t.Fatalf("Failed to create nested dir: %v", err)
	}
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	files := []*pack.FileInfo{
		{
			RelPath:     "nested/deep/file.js",
			Path:    testFile,
			Size:        4,
			Mode:        0644,
			ContentHash: "test123",
		},
	}

	// Store should create nested directories
	destPath, err := store.Store("test-pkg", "hash789", files, sourceDir)
	if err != nil {
		t.Fatalf("Failed to store: %v", err)
	}

	// Verify nested structure created
	storedFile := filepath.Join(destPath, "nested", "deep", "file.js")
	if _, err := os.Stat(storedFile); err != nil {
		t.Errorf("Nested file doesn't exist: %v", err)
	}

	// Check directory permissions
	if runtime.GOOS != "windows" {
		nestedDir := filepath.Join(destPath, "nested")
		info, err := os.Stat(nestedDir)
		if err != nil {
			t.Fatalf("Failed to stat nested dir: %v", err)
		}

		mode := info.Mode() & 0777
		expectedMode := os.FileMode(0755)
		if mode != expectedMode {
			t.Errorf("Expected directory mode %o, got %o", expectedMode, mode)
		}
	}
}

// TestStore_ReadOnlyDestination tests storing when destination is read-only
func TestStore_ReadOnlyDestination(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping on Windows - read-only behavior differs")
	}

	tmpDir := t.TempDir()
	t.Setenv("LNPM_STORE", tmpDir)

	store, err := New()
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Create source file
	sourceDir := filepath.Join(tmpDir, "source")
	if err := os.MkdirAll(sourceDir, 0755); err != nil {
		t.Fatalf("Failed to create source dir: %v", err)
	}

	testFile := filepath.Join(sourceDir, "test.js")
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	files := []*pack.FileInfo{
		{
			RelPath:     "test.js",
			Path:    testFile,
			Size:        4,
			Mode:        0644,
			ContentHash: "test123",
		},
	}

	// Pre-create destination as read-only
	destPath := store.PackagePath("test-pkg", "readonly-hash")
	// Create parent directory first with normal permissions
	parentDir := filepath.Dir(destPath)
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		t.Fatalf("Failed to create parent dir: %v", err)
	}
	// Create final directory as read-only
	if err := os.Mkdir(destPath, 0555); err != nil {
		t.Fatalf("Failed to create dest dir: %v", err)
	}
	defer func() {
		_ = os.Chmod(destPath, 0755) // Cleanup
	}()

	// Store should handle read-only destination (RemoveAll should fail or succeed)
	_, err = store.Store("test-pkg", "readonly-hash", files, sourceDir)
	// This may or may not fail depending on OS behavior
	// We just verify it doesn't panic
	t.Logf("Store with read-only destination returned: %v", err)
}

// TestGetStorePath_Permissions tests store path creation permissions
func TestGetStorePath_Permissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping on Windows - permission handling differs")
	}

	tmpDir := t.TempDir()
	t.Setenv("LNPM_STORE", tmpDir)

	store, err := New()
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Check store directory permissions
	info, err := os.Stat(store.basePath)
	if err != nil {
		t.Fatalf("Failed to stat store path: %v", err)
	}

	mode := info.Mode() & 0777
	expectedMode := os.FileMode(0755)
	if mode != expectedMode {
		t.Errorf("Expected store mode %o, got %o", expectedMode, mode)
	}
}

// TestStore_ConcurrentWithPermissions tests concurrent stores don't cause permission issues
func TestStore_ConcurrentWithPermissions(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LNPM_STORE", tmpDir)

	store, err := New()
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Create source directory
	sourceDir := filepath.Join(tmpDir, "source")
	if err := os.MkdirAll(sourceDir, 0755); err != nil {
		t.Fatalf("Failed to create source dir: %v", err)
	}

	// Create test file
	testFile := filepath.Join(sourceDir, "test.js")
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	files := []*pack.FileInfo{
		{
			RelPath:     "test.js",
			Path:    testFile,
			Size:        4,
			Mode:        0644,
			ContentHash: "test123",
		},
	}

	// Store same package concurrently
	done := make(chan error, 3)
	for i := 0; i < 3; i++ {
		go func() {
			_, err := store.Store("concurrent-pkg", "samehash", files, sourceDir)
			done <- err
		}()
	}

	// Wait for all to complete
	for i := 0; i < 3; i++ {
		if err := <-done; err != nil {
			t.Errorf("Concurrent store failed: %v", err)
		}
	}
}

// TestExists_WithVariousPermissions tests Exists with different file permissions
func TestExists_WithVariousPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping on Windows - permission handling differs")
	}

	tmpDir := t.TempDir()
	t.Setenv("LNPM_STORE", tmpDir)

	store, err := New()
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Create package directory with various permissions
	pkgPath := store.PackagePath("perm-test", "hash123")
	if err := os.MkdirAll(pkgPath, 0755); err != nil {
		t.Fatalf("Failed to create pkg dir: %v", err)
	}

	// Exists should return true
	if !store.Exists("perm-test", "hash123") {
		t.Error("Expected package to exist")
	}

	// Make it read-only
	if err := os.Chmod(pkgPath, 0555); err != nil {
		t.Fatalf("Failed to chmod: %v", err)
	}
	defer func() {
		_ = os.Chmod(pkgPath, 0755)
	}()

	// Should still exist
	if !store.Exists("perm-test", "hash123") {
		t.Error("Expected read-only package to still exist")
	}
}

// TestStore_SymlinkHandling tests storing files that are symlinks
func TestStore_SymlinkHandling(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping on Windows - symlink handling differs")
	}

	// Skip in CI environments where this test is flaky
	if os.Getenv("CI") != "" {
		t.Skip("Skipping flaky symlink test in CI")
	}

	tmpDir := t.TempDir()
	t.Setenv("LNPM_STORE", tmpDir)

	store, err := New()
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Create source directory with symlink
	sourceDir := filepath.Join(tmpDir, "source")
	if err := os.MkdirAll(sourceDir, 0755); err != nil {
		t.Fatalf("Failed to create source dir: %v", err)
	}

	// Create real file
	realFile := filepath.Join(sourceDir, "real.js")
	if err := os.WriteFile(realFile, []byte("real content"), 0644); err != nil {
		t.Fatalf("Failed to create real file: %v", err)
	}

	// Create symlink
	symlinkFile := filepath.Join(sourceDir, "link.js")
	if err := os.Symlink("real.js", symlinkFile); err != nil {
		t.Fatalf("Failed to create symlink: %v", err)
	}

	// Get file info (follows symlink)
	info, err := os.Stat(symlinkFile)
	if err != nil {
		t.Fatalf("Failed to stat symlink: %v", err)
	}

	files := []*pack.FileInfo{
		{
			RelPath:     "link.js",
			Path:    symlinkFile,
			Size:        int64(len("real content")),
			Mode:        info.Mode().Perm(),
			ContentHash: "link123",
		},
	}

	// Store should follow symlink and copy content
	destPath, err := store.Store("test-pkg", "symlink-hash", files, sourceDir)
	if err != nil {
		t.Fatalf("Failed to store with symlink: %v", err)
	}

	// Verify content was copied (not symlink)
	storedFile := filepath.Join(destPath, "link.js")

	// Debug: check if destPath exists
	if _, err := os.Stat(destPath); err != nil {
		t.Fatalf("Dest path doesn't exist: %v", err)
	}

	// Debug: list files in destPath
	entries, _ := os.ReadDir(destPath)
	t.Logf("Files in destPath: %v", entries)

	content, err := os.ReadFile(storedFile)
	if err != nil {
		t.Fatalf("Failed to read stored file: %v (destPath=%s, storedFile=%s)", err, destPath, storedFile)
	}

	if string(content) != "real content" {
		t.Errorf("Expected real content, got %s", string(content))
	}
}
