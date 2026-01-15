package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pedrosousa13/lnpm/internal/db"
	"github.com/pedrosousa13/lnpm/internal/link"
	"github.com/pedrosousa13/lnpm/internal/pack"
	"github.com/pedrosousa13/lnpm/internal/store"
)

// RunPush executes the push command
func RunPush(force bool) error {
	// Get current directory
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	// Get database
	database, err := db.GetDB()
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}

	// Read package.json to get package name (fast, no file scanning yet)
	pkgJSON, err := pack.ReadPackageJSON(cwd)
	if err != nil {
		return fmt.Errorf("failed to read package.json: %w", err)
	}

	// Get existing package from database
	pkg, err := database.GetPackageByName(pkgJSON.Name)
	if err != nil {
		return fmt.Errorf("failed to look up package: %w", err)
	}

	// If not published yet, do full pack and delegate to publish
	if pkg == nil {
		fmt.Printf("Package %s not published yet, publishing...\n", pkgJSON.Name)
		_, files, err := pack.Pack(cwd)
		if err != nil {
			return fmt.Errorf("failed to pack: %w", err)
		}
		return finishPublish(cwd, pkgJSON, files, database, false)
	}

	// Fast path: Use git to detect changes (if available)
	// This is 100x faster than scanning files
	var files []*pack.FileInfo
	var newHash string
	skipScan := false

	if !force {
		// Try git-based change detection first (milliseconds vs hundreds of ms)
		gitChanged, err := checkGitChanges(cwd, pkg.UpdatedAt)
		if err == nil {
			if !gitChanged {
				// Git says no changes since last push
				fmt.Printf("✓ No changes detected (git)\n")
				return nil
			}
			// Git detected changes - fall through to full scan
		} else {
			// Not a git repo or git unavailable - use directory mtime
			dirInfo, err := os.Stat(cwd)
			if err == nil {
				dirMtime := dirInfo.ModTime().UnixNano()
				pkgMtime := pkg.UpdatedAt.UnixNano()

				// Check if directory and critical files are unchanged
				// This is much faster than stat-ing all files
				if dirMtime <= pkgMtime {
					// Check package.json hasn't changed
					pkgJSONPath := filepath.Join(cwd, "package.json")
					pkgJSONInfo, err := os.Stat(pkgJSONPath)
					if err == nil && pkgJSONInfo.ModTime().UnixNano() <= pkgMtime {
						// Looks unchanged - quick validation of file count
						storedFiles, err := database.GetFilesForPackage(pkg.ID)
						if err == nil {
							// Just verify file count matches (not each file)
							// If count differs, something was added/removed
							currentCount := countPackageFiles(cwd)
							if currentCount == len(storedFiles) {
								fmt.Printf("✓ No changes detected (fast check)\n")
								return nil
							}
						}
					}
				}
			}
		}
	}

	// Full scan needed: directory changed, files missing, or forced
	if !skipScan {
		_, files, err = pack.Pack(cwd)
		if err != nil {
			return fmt.Errorf("failed to pack: %w", err)
		}

		// Calculate content hash
		newHash = pack.HashFiles(files)

		// Check if content actually changed
		if pkg.ContentHash == newHash && !force {
			fmt.Printf("✓ No changes detected (hash: %s)\n", shortHash(newHash))
			return nil
		}
	}

	fmt.Printf("Pushing %s@%s...\n", pkgJSON.Name, pkgJSON.Version)

	// Get store
	s, err := store.New()
	if err != nil {
		return fmt.Errorf("failed to access store: %w", err)
	}

	// Store the updated package
	storePath, err := s.Store(pkgJSON.Name, newHash, files, cwd)
	if err != nil {
		return fmt.Errorf("failed to store package: %w", err)
	}

	// Calculate total size
	var totalSize int64
	for _, f := range files {
		totalSize += f.Size
	}

	// Update package in database
	pkg.Version = pkgJSON.Version
	pkg.ContentHash = newHash
	pkg.SourcePath = cwd
	pkg.StorePath = storePath
	pkg.FilesCount = len(files)
	pkg.TotalSize = totalSize

	if err := database.InsertPackage(pkg); err != nil {
		return fmt.Errorf("failed to update package: %w", err)
	}

	// Update file manifest
	fileEntries := make([]*db.FileEntry, len(files))
	for i, f := range files {
		fileEntries[i] = &db.FileEntry{
			PackageID:    pkg.ID,
			RelativePath: f.RelPath,
			ContentHash:  f.ContentHash,
			Size:         f.Size,
			Mode:         f.Mode,
			ModTime:      f.ModTime,
		}
	}
	if err := database.InsertFiles(pkg.ID, fileEntries); err != nil {
		return fmt.Errorf("failed to update files: %w", err)
	}

	// Get linked projects
	projects, err := database.GetProjectsForPackage(pkg.ID)
	if err != nil {
		return fmt.Errorf("failed to get linked projects: %w", err)
	}

	if len(projects) == 0 {
		fmt.Printf("✓ Updated %s@%s in store\n", pkgJSON.Name, pkgJSON.Version)
		fmt.Println("  No linked projects to update")
		return nil
	}

	// Push to all linked projects
	fmt.Printf("Updating %d linked projects...\n", len(projects))

	// Convert files to FileInfo once (avoid walking store per project)
	fileData := make([]pack.FileEntryData, len(files))
	for i, f := range files {
		fileData[i] = pack.FileEntryData{
			RelPath: f.RelPath,
			Size:    f.Size,
			Mode:    f.Mode,
			Hash:    f.ContentHash,
		}
	}
	storeFiles := pack.FileInfoFromStore(storePath, fileData)

	successCount := 0
	for _, proj := range projects {
		// Re-link
		linker := link.New(proj.Path)
		_, err = linker.Link(pkg.Name, storePath, storeFiles)
		if err != nil {
			fmt.Printf("  ✗ %s: %v\n", proj.Path, err)
		} else {
			fmt.Printf("  ✓ %s\n", proj.Path)
			successCount++
		}
	}

	fmt.Printf("\nPushed to %d/%d projects\n", successCount, len(projects))

	return nil
}

// checkGitChanges uses git to detect if any tracked files changed since lastPush
// This is ~100x faster than scanning filesystem for large repos
func checkGitChanges(dir string, lastPush time.Time) (bool, error) {
	// Check if git is available
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return false, err
	}

	// Check if directory is a git repo
	cmd := exec.Command(gitPath, "rev-parse", "--git-dir")
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		return false, err
	}

	// Get last commit time to see if anything committed since lastPush
	cmd = exec.Command(gitPath, "log", "-1", "--format=%ct")
	cmd.Dir = dir
	output, err := cmd.Output()
	if err == nil {
		lastCommitStr := strings.TrimSpace(string(output))
		if lastCommitTs, err := strconv.ParseInt(lastCommitStr, 10, 64); err == nil {
			lastCommit := time.Unix(lastCommitTs, 0)
			if lastCommit.After(lastPush) {
				return true, nil // New commits since last push
			}
		}
	}

	// Check for uncommitted changes (working tree + staged)
	cmd = exec.Command(gitPath, "status", "--porcelain")
	cmd.Dir = dir
	output, err = cmd.Output()
	if err != nil {
		return false, err
	}

	hasChanges := len(strings.TrimSpace(string(output))) > 0
	return hasChanges, nil
}

// countPackageFiles quickly counts files without full scanning
// Used as a sanity check - if count differs, something changed
func countPackageFiles(dir string) int {
	count := 0
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		// Skip common excluded directories for speed
		if info.IsDir() {
			name := info.Name()
			if name == "node_modules" || name == ".git" || name == ".lnpm" {
				return filepath.SkipDir
			}
		} else {
			count++
		}
		return nil
	})
	return count
}
