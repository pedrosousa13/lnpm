package gitignore

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	lnpmMarker = "# Added by lnpm"
)

// EnsureInGitignore adds pattern to .gitignore if not present
// Returns true if pattern was added, false if already present
func EnsureInGitignore(projectPath, pattern string) (bool, error) {
	gitignorePath := filepath.Join(projectPath, ".gitignore")

	// Check if pattern already exists
	exists, err := IsInGitignore(projectPath, pattern)
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("checking .gitignore: %w", err)
	}
	if exists {
		return false, nil
	}

	// Read existing content and check permissions
	var content []byte
	var fileMode os.FileMode = 0644
	if info, err := os.Stat(gitignorePath); err == nil {
		fileMode = info.Mode().Perm()

		// Check if file is read-only (no write permission for owner)
		if fileMode&0200 == 0 {
			return false, fmt.Errorf(".gitignore is read-only (mode %o)", fileMode)
		}

		content, err = os.ReadFile(gitignorePath)
		if err != nil {
			return false, fmt.Errorf("reading .gitignore: %w", err)
		}
	}

	// Append pattern with marker
	var sb strings.Builder
	if len(content) > 0 {
		sb.Write(content)
		// Ensure newline before adding new content
		if !strings.HasSuffix(string(content), "\n") {
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}
	sb.WriteString(lnpmMarker + "\n")
	sb.WriteString(pattern + "\n")

	// Atomic write: write to temp file, then rename
	tempPath := gitignorePath + ".tmp"
	if err := os.WriteFile(tempPath, []byte(sb.String()), fileMode); err != nil {
		return false, fmt.Errorf("writing temp .gitignore: %w", err)
	}
	if err := os.Rename(tempPath, gitignorePath); err != nil {
		_ = os.Remove(tempPath) // Clean up temp file
		return false, fmt.Errorf("updating .gitignore: %w", err)
	}

	return true, nil
}

// RemoveFromGitignore removes pattern from .gitignore
func RemoveFromGitignore(projectPath, pattern string) error {
	gitignorePath := filepath.Join(projectPath, ".gitignore")

	// Check if file exists and get permissions
	info, err := os.Stat(gitignorePath)
	if os.IsNotExist(err) {
		return nil // Nothing to remove
	}
	if err != nil {
		return fmt.Errorf("stat .gitignore: %w", err)
	}

	// Check if file is read-only
	fileMode := info.Mode().Perm()
	if fileMode&0200 == 0 {
		return fmt.Errorf(".gitignore is read-only (mode %o)", fileMode)
	}

	file, err := os.Open(gitignorePath)
	if err != nil {
		return fmt.Errorf("opening .gitignore: %w", err)
	}
	defer func() {
		_ = file.Close()
	}()

	var lines []string
	scanner := bufio.NewScanner(file)
	skipNext := false

	for scanner.Scan() {
		line := scanner.Text()

		// Skip marker line and next line if it matches pattern
		if line == lnpmMarker {
			skipNext = true
			continue
		}
		if skipNext && line == pattern {
			skipNext = false
			continue
		}
		skipNext = false
		lines = append(lines, line)
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("reading .gitignore: %w", err)
	}

	// Remove trailing empty lines added by lnpm
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	// Atomic write (preserve original permissions)
	tempPath := gitignorePath + ".tmp"
	content := strings.Join(lines, "\n")
	if len(lines) > 0 {
		content += "\n"
	}

	if err := os.WriteFile(tempPath, []byte(content), fileMode); err != nil {
		return fmt.Errorf("writing temp .gitignore: %w", err)
	}
	if err := os.Rename(tempPath, gitignorePath); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("updating .gitignore: %w", err)
	}

	return nil
}

// IsInGitignore checks if pattern exists in .gitignore
func IsInGitignore(projectPath, pattern string) (bool, error) {
	gitignorePath := filepath.Join(projectPath, ".gitignore")

	file, err := os.Open(gitignorePath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("opening .gitignore: %w", err)
	}
	defer func() {
		_ = file.Close()
	}()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if scanner.Text() == pattern {
			return true, nil
		}
	}

	if err := scanner.Err(); err != nil {
		return false, fmt.Errorf("reading .gitignore: %w", err)
	}

	return false, nil
}
