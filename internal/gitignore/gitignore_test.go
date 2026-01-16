package gitignore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureInGitignore_NewFile(t *testing.T) {
	// Create temp dir
	tmpDir := t.TempDir()

	// Add pattern
	added, err := EnsureInGitignore(tmpDir, ".lnpm/")
	if err != nil {
		t.Fatalf("EnsureInGitignore failed: %v", err)
	}
	if !added {
		t.Error("Expected pattern to be added")
	}

	// Verify file created with correct content
	content, err := os.ReadFile(filepath.Join(tmpDir, ".gitignore"))
	if err != nil {
		t.Fatalf("Failed to read .gitignore: %v", err)
	}

	expected := lnpmMarker + "\n.lnpm/\n"
	if string(content) != expected {
		t.Errorf("Unexpected content.\nGot:\n%s\nExpected:\n%s", content, expected)
	}
}

func TestEnsureInGitignore_Idempotent(t *testing.T) {
	tmpDir := t.TempDir()

	// Add pattern first time
	added, err := EnsureInGitignore(tmpDir, ".lnpm/")
	if err != nil {
		t.Fatalf("First add failed: %v", err)
	}
	if !added {
		t.Error("Expected pattern to be added first time")
	}

	// Add pattern second time - should be idempotent
	added, err = EnsureInGitignore(tmpDir, ".lnpm/")
	if err != nil {
		t.Fatalf("Second add failed: %v", err)
	}
	if added {
		t.Error("Expected pattern not to be added second time (idempotent)")
	}

	// Verify content not duplicated
	content, err := os.ReadFile(filepath.Join(tmpDir, ".gitignore"))
	if err != nil {
		t.Fatalf("Failed to read .gitignore: %v", err)
	}

	lines := strings.Split(string(content), "\n")
	patternCount := 0
	for _, line := range lines {
		if line == ".lnpm/" {
			patternCount++
		}
	}

	if patternCount != 1 {
		t.Errorf("Expected pattern to appear once, found %d times", patternCount)
	}
}

func TestEnsureInGitignore_PreservesExisting(t *testing.T) {
	tmpDir := t.TempDir()
	gitignorePath := filepath.Join(tmpDir, ".gitignore")

	// Create .gitignore with existing content
	existing := "node_modules/\n*.log\n"
	if err := os.WriteFile(gitignorePath, []byte(existing), 0644); err != nil {
		t.Fatalf("Failed to create .gitignore: %v", err)
	}

	// Add pattern
	added, err := EnsureInGitignore(tmpDir, ".lnpm/")
	if err != nil {
		t.Fatalf("EnsureInGitignore failed: %v", err)
	}
	if !added {
		t.Error("Expected pattern to be added")
	}

	// Verify existing content preserved
	content, err := os.ReadFile(gitignorePath)
	if err != nil {
		t.Fatalf("Failed to read .gitignore: %v", err)
	}

	contentStr := string(content)
	if !strings.Contains(contentStr, "node_modules/") {
		t.Error("Existing content not preserved")
	}
	if !strings.Contains(contentStr, "*.log") {
		t.Error("Existing content not preserved")
	}
	if !strings.Contains(contentStr, ".lnpm/") {
		t.Error("New pattern not added")
	}
}

func TestRemoveFromGitignore_Exists(t *testing.T) {
	tmpDir := t.TempDir()
	gitignorePath := filepath.Join(tmpDir, ".gitignore")

	// Create .gitignore with pattern
	content := "node_modules/\n\n" + lnpmMarker + "\n.lnpm/\n"
	if err := os.WriteFile(gitignorePath, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to create .gitignore: %v", err)
	}

	// Remove pattern
	if err := RemoveFromGitignore(tmpDir, ".lnpm/"); err != nil {
		t.Fatalf("RemoveFromGitignore failed: %v", err)
	}

	// Verify pattern removed
	newContent, err := os.ReadFile(gitignorePath)
	if err != nil {
		t.Fatalf("Failed to read .gitignore: %v", err)
	}

	contentStr := string(newContent)
	if strings.Contains(contentStr, ".lnpm/") {
		t.Error("Pattern not removed")
	}
	if strings.Contains(contentStr, lnpmMarker) {
		t.Error("Marker not removed")
	}
	if !strings.Contains(contentStr, "node_modules/") {
		t.Error("Other content was removed")
	}
}

func TestRemoveFromGitignore_NotExists(t *testing.T) {
	tmpDir := t.TempDir()

	// Remove from non-existent file - should not error
	if err := RemoveFromGitignore(tmpDir, ".lnpm/"); err != nil {
		t.Fatalf("RemoveFromGitignore failed on non-existent file: %v", err)
	}
}

func TestRemoveFromGitignore_PatternNotPresent(t *testing.T) {
	tmpDir := t.TempDir()
	gitignorePath := filepath.Join(tmpDir, ".gitignore")

	// Create .gitignore without pattern
	content := "node_modules/\n*.log\n"
	if err := os.WriteFile(gitignorePath, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to create .gitignore: %v", err)
	}

	// Remove pattern that doesn't exist
	if err := RemoveFromGitignore(tmpDir, ".lnpm/"); err != nil {
		t.Fatalf("RemoveFromGitignore failed: %v", err)
	}

	// Verify file unchanged
	newContent, err := os.ReadFile(gitignorePath)
	if err != nil {
		t.Fatalf("Failed to read .gitignore: %v", err)
	}

	if string(newContent) != content {
		t.Error("File was modified when pattern wasn't present")
	}
}

func TestIsInGitignore(t *testing.T) {
	tmpDir := t.TempDir()
	gitignorePath := filepath.Join(tmpDir, ".gitignore")

	// Non-existent file
	exists, err := IsInGitignore(tmpDir, ".lnpm/")
	if err != nil {
		t.Fatalf("IsInGitignore failed: %v", err)
	}
	if exists {
		t.Error("Pattern should not exist in non-existent file")
	}

	// Create .gitignore with pattern
	content := "node_modules/\n.lnpm/\n"
	if err := os.WriteFile(gitignorePath, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to create .gitignore: %v", err)
	}

	// Pattern exists
	exists, err = IsInGitignore(tmpDir, ".lnpm/")
	if err != nil {
		t.Fatalf("IsInGitignore failed: %v", err)
	}
	if !exists {
		t.Error("Pattern should exist")
	}

	// Pattern doesn't exist
	exists, err = IsInGitignore(tmpDir, ".other/")
	if err != nil {
		t.Fatalf("IsInGitignore failed: %v", err)
	}
	if exists {
		t.Error("Pattern should not exist")
	}
}
