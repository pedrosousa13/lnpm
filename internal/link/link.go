package link

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/pedrosousa13/lnpm/internal/config"
	"github.com/pedrosousa13/lnpm/internal/debug"
	"github.com/pedrosousa13/lnpm/internal/pack"
)

// LinkType represents the type of linking used
type LinkType string

const (
	HardLink LinkType = "hardlink"
	Copy     LinkType = "copy"
)

// Linker handles linking packages to projects
type Linker struct {
	projectPath string
}

// New creates a new Linker for a project
func New(projectPath string) *Linker {
	return &Linker{projectPath: projectPath}
}

// Link links a package from the store to the project
// It creates hard links in .lnpm/{package}/ and a symlink in node_modules/{package}
func (l *Linker) Link(packageName string, storePath string, files []*pack.FileInfo) (LinkType, error) {
	debug.Logf("link: linking %s from %s (%d files)", packageName, storePath, len(files))

	// Determine link type based on config and filesystem
	linkType := l.determineLinkType(storePath)
	debug.Logf("link: using %s mode", linkType)

	// Create .lnpm/{package} directory
	lnpmPath := filepath.Join(l.projectPath, ".lnpm", packageName)
	if err := os.RemoveAll(lnpmPath); err != nil {
		return "", fmt.Errorf("failed to clean .lnpm directory: %w", err)
	}
	if err := os.MkdirAll(lnpmPath, 0755); err != nil {
		return "", fmt.Errorf("failed to create .lnpm directory: %w", err)
	}

	// Track what methods we actually used
	reflinkCount := 0
	hardLinkCount := 0
	copyCount := 0
	actualType := linkType
	warnedAboutFallback := false

	// Create hard links, reflinks, or copies
	for _, f := range files {
		srcPath := filepath.Join(storePath, f.RelPath)
		dstPath := filepath.Join(lnpmPath, f.RelPath)

		// Create parent directory
		if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
			return "", fmt.Errorf("failed to create directory for %s: %w", f.RelPath, err)
		}

		linked := false

		// Try in priority order: reflink -> hardlink -> copy
		// 1. Try reflink (CoW clone) - works even across directories on same FS
		if reflinkFile(srcPath, dstPath) == nil {
			linked = true
			reflinkCount++
			if actualType == Copy {
				actualType = HardLink // Upgraded to linking
			}
		}

		// 2. Try hard link if configured and reflink didn't work
		if !linked && linkType == HardLink {
			if err := os.Link(srcPath, dstPath); err == nil {
				linked = true
				hardLinkCount++
			} else {
				// Hard link failed, fall back to copy
				if !warnedAboutFallback {
					fmt.Printf("  ⚠ Hard linking failed, falling back to copying files\n")
					debug.Logf("link: hard link failed: %v", err)
					warnedAboutFallback = true
				}
				actualType = Copy
			}
		}

		// 3. Fall back to copy if nothing else worked
		if !linked {
			if err := copyFile(srcPath, dstPath); err != nil {
				return "", fmt.Errorf("failed to copy %s: %w", f.RelPath, err)
			}
			copyCount++
		}
	}

	if reflinkCount > 0 || hardLinkCount > 0 {
		debug.Logf("link: reflinked %d, hard linked %d, copied %d files", reflinkCount, hardLinkCount, copyCount)
	}

	// Create symlink in node_modules
	if err := l.createNodeModulesSymlink(packageName); err != nil {
		return "", err
	}

	return actualType, nil
}

// Unlink removes a linked package from the project
func (l *Linker) Unlink(packageName string) error {
	// Remove .lnpm/{package}
	lnpmPath := filepath.Join(l.projectPath, ".lnpm", packageName)
	if err := os.RemoveAll(lnpmPath); err != nil {
		return fmt.Errorf("failed to remove .lnpm directory: %w", err)
	}

	// Remove node_modules symlink
	nodeModulesPath := filepath.Join(l.projectPath, "node_modules", packageName)
	if err := os.Remove(nodeModulesPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove node_modules symlink: %w", err)
	}

	// Clean up empty .lnpm directory if no packages left
	lnpmDir := filepath.Join(l.projectPath, ".lnpm")
	entries, err := os.ReadDir(lnpmDir)
	if err == nil && len(entries) == 0 {
		os.Remove(lnpmDir)
	}

	return nil
}

// createNodeModulesSymlink creates a symlink from node_modules/{pkg} to .lnpm/{pkg}
func (l *Linker) createNodeModulesSymlink(packageName string) error {
	nodeModulesDir := filepath.Join(l.projectPath, "node_modules")
	if err := os.MkdirAll(nodeModulesDir, 0755); err != nil {
		return fmt.Errorf("failed to create node_modules directory: %w", err)
	}

	// Handle scoped packages (@org/package)
	linkPath := filepath.Join(nodeModulesDir, packageName)
	if err := os.MkdirAll(filepath.Dir(linkPath), 0755); err != nil {
		return fmt.Errorf("failed to create scope directory: %w", err)
	}

	// Remove existing symlink/file/directory
	if err := os.RemoveAll(linkPath); err != nil {
		return fmt.Errorf("failed to remove existing node_modules entry: %w", err)
	}

	// Create relative symlink
	// From: node_modules/{package}
	// To: ../.lnpm/{package}
	relTarget := filepath.Join("..", ".lnpm", packageName)

	if err := os.Symlink(relTarget, linkPath); err != nil {
		return fmt.Errorf("failed to create node_modules symlink: %w", err)
	}

	return nil
}

// determineLinkType checks if hard links are possible between store and project
func (l *Linker) determineLinkType(storePath string) LinkType {
	// Check user config first
	cfg, err := config.LoadConfig()
	if err == nil && cfg.LinkMode != "" {
		switch cfg.LinkMode {
		case "copy":
			debug.Log("link: using copy mode (from config)")
			return Copy
		case "hardlink":
			debug.Log("link: using hardlink mode (from config)")
			// Still need to check filesystem compatibility
		default:
			debug.Logf("link: unknown link_mode in config: %s, using auto-detect", cfg.LinkMode)
		}
	}

	// Get device IDs for both paths
	storeInfo, err := os.Stat(storePath)
	if err != nil {
		return Copy
	}

	projectInfo, err := os.Stat(l.projectPath)
	if err != nil {
		return Copy
	}

	// On Windows, hard links work within the same volume
	// We try hard link first and fall back to copy on failure
	if runtime.GOOS == "windows" {
		// For Windows, we'll try hard links and fall back if they fail
		return HardLink
	}

	// Check if on same filesystem (Unix-specific using device ID)
	storeDev := getDeviceID(storeInfo)
	projectDev := getDeviceID(projectInfo)

	if storeDev != 0 && projectDev != 0 {
		if storeDev == projectDev {
			return HardLink
		}
		debug.Log("link: store and project on different filesystems, using copy")
		return Copy
	}

	// Default to hard link and let it fail if needed
	return HardLink
}

// IsLinked checks if a package is linked in the project
func (l *Linker) IsLinked(packageName string) bool {
	lnpmPath := filepath.Join(l.projectPath, ".lnpm", packageName)
	_, err := os.Stat(lnpmPath)
	return err == nil
}

// ListLinked returns all packages linked in the project
func (l *Linker) ListLinked() ([]string, error) {
	lnpmDir := filepath.Join(l.projectPath, ".lnpm")
	entries, err := os.ReadDir(lnpmDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var packages []string
	for _, entry := range entries {
		if entry.IsDir() {
			packages = append(packages, entry.Name())
		}
	}
	return packages, nil
}

// copyFile copies a file
func copyFile(src, dst string) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}

	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, srcInfo.Mode())
	if err != nil {
		return err
	}
	defer dstFile.Close()

	buf := make([]byte, 64*1024) // 64KB buffer
	for {
		n, err := srcFile.Read(buf)
		if n > 0 {
			if _, writeErr := dstFile.Write(buf[:n]); writeErr != nil {
				return writeErr
			}
		}
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			return err
		}
	}

	return dstFile.Sync()
}
