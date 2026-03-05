//go:build windows

package link

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// createDirSymlink creates a directory symlink on Windows.
// Tries native symlink first (works with Developer Mode), then falls back
// to a junction point via mklink /J (no admin/Developer Mode required).
func createDirSymlink(target, linkPath string) error {
	// Junctions require absolute paths
	absTarget := target
	if !filepath.IsAbs(target) {
		absTarget = filepath.Join(filepath.Dir(linkPath), target)
	}
	absTarget, _ = filepath.Abs(absTarget)

	// Try native symlink first (works if Developer Mode is on)
	if err := os.Symlink(absTarget, linkPath); err == nil {
		return nil
	}

	// Fallback: junction via mklink /J (no admin required)
	cmd := exec.Command("cmd", "/C", "mklink", "/J", linkPath, absTarget)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to create junction: %w (output: %s)", err, output)
	}
	return nil
}
