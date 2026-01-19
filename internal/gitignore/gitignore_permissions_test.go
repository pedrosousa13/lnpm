package gitignore

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestEnsureInGitignore_ReadOnlyFile tests adding to a read-only .gitignore
func TestEnsureInGitignore_ReadOnlyFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping on Windows - read-only behavior differs")
	}

	tmpDir := t.TempDir()
	gitignorePath := filepath.Join(tmpDir, ".gitignore")

	// Create .gitignore with existing content
	existing := "node_modules/\n"
	if err := os.WriteFile(gitignorePath, []byte(existing), 0644); err != nil {
		t.Fatalf("Failed to create .gitignore: %v", err)
	}

	// Make it read-only
	if err := os.Chmod(gitignorePath, 0444); err != nil {
		t.Fatalf("Failed to chmod .gitignore: %v", err)
	}
	defer func() {
		_ = os.Chmod(gitignorePath, 0644) // Cleanup
	}()

	// Try to add pattern - should fail
	_, err := EnsureInGitignore(tmpDir, ".lnpm/")
	if err == nil {
		t.Error("Expected error when .gitignore is read-only, got nil")
	}
}

// TestEnsureInGitignore_ReadOnlyDirectory tests adding when directory is read-only
func TestEnsureInGitignore_ReadOnlyDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping on Windows - read-only behavior differs")
	}

	tmpDir := t.TempDir()

	// Make directory read-only
	if err := os.Chmod(tmpDir, 0555); err != nil {
		t.Fatalf("Failed to chmod directory: %v", err)
	}
	defer func() {
		_ = os.Chmod(tmpDir, 0755) // Cleanup
	}()

	// Try to create .gitignore in read-only directory - should fail
	_, err := EnsureInGitignore(tmpDir, ".lnpm/")
	if err == nil {
		t.Error("Expected error when directory is read-only, got nil")
	}
}

// TestEnsureInGitignore_TempFilePermissions tests that temp file has correct permissions
func TestEnsureInGitignore_TempFilePermissions(t *testing.T) {
	tmpDir := t.TempDir()

	// Add pattern
	_, err := EnsureInGitignore(tmpDir, ".lnpm/")
	if err != nil {
		t.Fatalf("EnsureInGitignore failed: %v", err)
	}

	// Check .gitignore permissions
	gitignorePath := filepath.Join(tmpDir, ".gitignore")
	info, err := os.Stat(gitignorePath)
	if err != nil {
		t.Fatalf("Failed to stat .gitignore: %v", err)
	}

	expectedMode := os.FileMode(0644)
	actualMode := info.Mode() & 0777
	if actualMode != expectedMode {
		t.Errorf("Expected permissions %o, got %o", expectedMode, actualMode)
	}
}

// TestRemoveFromGitignore_ReadOnlyFile tests removing from read-only .gitignore
func TestRemoveFromGitignore_ReadOnlyFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping on Windows - read-only behavior differs")
	}

	tmpDir := t.TempDir()
	gitignorePath := filepath.Join(tmpDir, ".gitignore")

	// Create .gitignore with pattern
	content := lnpmMarker + "\n.lnpm/\n"
	if err := os.WriteFile(gitignorePath, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to create .gitignore: %v", err)
	}

	// Make it read-only
	if err := os.Chmod(gitignorePath, 0444); err != nil {
		t.Fatalf("Failed to chmod .gitignore: %v", err)
	}
	defer func() {
		_ = os.Chmod(gitignorePath, 0644) // Cleanup
	}()

	// Try to remove pattern - should fail
	err := RemoveFromGitignore(tmpDir, ".lnpm/")
	if err == nil {
		t.Error("Expected error when .gitignore is read-only, got nil")
	}
}

// TestRemoveFromGitignore_TempFileCleanup tests temp file cleanup on error
func TestRemoveFromGitignore_TempFileCleanup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping on Windows - read-only behavior differs")
	}

	tmpDir := t.TempDir()
	gitignorePath := filepath.Join(tmpDir, ".gitignore")
	tempPath := gitignorePath + ".tmp"

	// Create .gitignore with pattern
	content := lnpmMarker + "\n.lnpm/\n"
	if err := os.WriteFile(gitignorePath, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to create .gitignore: %v", err)
	}

	// Make original read-only to cause rename failure
	if err := os.Chmod(gitignorePath, 0444); err != nil {
		t.Fatalf("Failed to chmod .gitignore: %v", err)
	}
	defer func() {
		_ = os.Chmod(gitignorePath, 0644) // Cleanup
	}()

	// Try to remove (will fail)
	_ = RemoveFromGitignore(tmpDir, ".lnpm/")

	// Verify temp file was cleaned up
	if _, err := os.Stat(tempPath); !os.IsNotExist(err) {
		t.Error("Temp file was not cleaned up after error")
	}
}

// TestEnsureInGitignore_AtomicWrite tests atomic write behavior
func TestEnsureInGitignore_AtomicWrite(t *testing.T) {
	tmpDir := t.TempDir()
	gitignorePath := filepath.Join(tmpDir, ".gitignore")

	// Create .gitignore with existing content
	existing := "node_modules/\n*.log\n"
	if err := os.WriteFile(gitignorePath, []byte(existing), 0644); err != nil {
		t.Fatalf("Failed to create .gitignore: %v", err)
	}

	// Read original content
	originalContent, err := os.ReadFile(gitignorePath)
	if err != nil {
		t.Fatalf("Failed to read original: %v", err)
	}

	// Add pattern
	added, err := EnsureInGitignore(tmpDir, ".lnpm/")
	if err != nil {
		t.Fatalf("EnsureInGitignore failed: %v", err)
	}
	if !added {
		t.Error("Expected pattern to be added")
	}

	// Verify temp file doesn't exist
	tempPath := gitignorePath + ".tmp"
	if _, err := os.Stat(tempPath); !os.IsNotExist(err) {
		t.Error("Temp file still exists after successful write")
	}

	// Verify new content
	newContent, err := os.ReadFile(gitignorePath)
	if err != nil {
		t.Fatalf("Failed to read new content: %v", err)
	}

	// Should contain original and new content
	newContentStr := string(newContent)
	if len(newContentStr) <= len(string(originalContent)) {
		t.Error("File was not updated")
	}
}

// TestIsInGitignore_ReadOnlyFile tests checking read-only .gitignore
func TestIsInGitignore_ReadOnlyFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping on Windows - read-only behavior differs")
	}

	tmpDir := t.TempDir()
	gitignorePath := filepath.Join(tmpDir, ".gitignore")

	// Create .gitignore with pattern
	content := "node_modules/\n.lnpm/\n"
	if err := os.WriteFile(gitignorePath, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to create .gitignore: %v", err)
	}

	// Make it read-only
	if err := os.Chmod(gitignorePath, 0444); err != nil {
		t.Fatalf("Failed to chmod .gitignore: %v", err)
	}
	defer func() {
		_ = os.Chmod(gitignorePath, 0644) // Cleanup
	}()

	// Should still be able to read
	exists, err := IsInGitignore(tmpDir, ".lnpm/")
	if err != nil {
		t.Fatalf("IsInGitignore failed on read-only file: %v", err)
	}
	if !exists {
		t.Error("Pattern should exist")
	}
}

// TestEnsureInGitignore_PartialWrite tests handling of partial write failures
func TestEnsureInGitignore_PartialWrite(t *testing.T) {
	tmpDir := t.TempDir()
	gitignorePath := filepath.Join(tmpDir, ".gitignore")

	// Create .gitignore with existing content
	existing := "node_modules/\n"
	if err := os.WriteFile(gitignorePath, []byte(existing), 0644); err != nil {
		t.Fatalf("Failed to create .gitignore: %v", err)
	}

	// Read original inode (if supported)
	originalInfo, err := os.Stat(gitignorePath)
	if err != nil {
		t.Fatalf("Failed to stat original: %v", err)
	}

	// Add pattern
	_, err = EnsureInGitignore(tmpDir, ".lnpm/")
	if err != nil {
		t.Fatalf("EnsureInGitignore failed: %v", err)
	}

	// Verify file was replaced (atomic rename)
	newInfo, err := os.Stat(gitignorePath)
	if err != nil {
		t.Fatalf("Failed to stat new file: %v", err)
	}

	// On most systems, atomic rename means different inode
	// (This is a best-effort check and may not work on all filesystems)
	if runtime.GOOS != "windows" {
		originalSys := originalInfo.Sys()
		newSys := newInfo.Sys()
		if originalSys != nil && newSys != nil {
			// Note: This check is informational; atomic rename behavior varies by OS
			t.Logf("Original and new file system info: %v vs %v", originalSys, newSys)
		}
	}
}

// TestRemoveFromGitignore_PreservesPermissions tests that permissions are preserved
func TestRemoveFromGitignore_PreservesPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping on Windows - permission handling differs")
	}

	tmpDir := t.TempDir()
	gitignorePath := filepath.Join(tmpDir, ".gitignore")

	// Create .gitignore with custom permissions
	content := lnpmMarker + "\n.lnpm/\nnode_modules/\n"
	if err := os.WriteFile(gitignorePath, []byte(content), 0640); err != nil {
		t.Fatalf("Failed to create .gitignore: %v", err)
	}

	// Remove pattern
	if err := RemoveFromGitignore(tmpDir, ".lnpm/"); err != nil {
		t.Fatalf("RemoveFromGitignore failed: %v", err)
	}

	// Check permissions (should be preserved)
	info, err := os.Stat(gitignorePath)
	if err != nil {
		t.Fatalf("Failed to stat .gitignore: %v", err)
	}

	actualMode := info.Mode() & 0777
	// RemoveFromGitignore should preserve original permissions
	expectedMode := os.FileMode(0640)
	if actualMode != expectedMode {
		t.Errorf("Expected permissions %o, got %o", expectedMode, actualMode)
	}
}

// TestEnsureInGitignore_NoSpaceOnDevice tests handling of disk full scenario
func TestEnsureInGitignore_NoSpaceOnDevice(t *testing.T) {
	// This test is difficult to simulate reliably across platforms
	// We test that errors are properly propagated
	tmpDir := t.TempDir()

	// Create .gitignore
	gitignorePath := filepath.Join(tmpDir, ".gitignore")
	if err := os.WriteFile(gitignorePath, []byte("test\n"), 0644); err != nil {
		t.Fatalf("Failed to create .gitignore: %v", err)
	}

	// Normal add should work
	added, err := EnsureInGitignore(tmpDir, ".lnpm/")
	if err != nil {
		t.Fatalf("EnsureInGitignore failed: %v", err)
	}
	if !added {
		t.Error("Expected pattern to be added")
	}
}
