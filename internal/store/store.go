package store

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/pedrosousa13/lnpm/internal/debug"
	"github.com/pedrosousa13/lnpm/internal/pack"
)

// shortHash returns the first 8 characters of a hash for display
func shortHash(hash string) string {
	if len(hash) > 8 {
		return hash[:8]
	}
	return hash
}

// Store manages the package store at ~/.lnpm/store
type Store struct {
	basePath string
}

// New creates a new Store instance
func New() (*Store, error) {
	basePath, err := getStorePath()
	if err != nil {
		return nil, err
	}

	storePath := filepath.Join(basePath, "store")
	if err := os.MkdirAll(storePath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create store directory: %w", err)
	}

	return &Store{basePath: storePath}, nil
}

// getStorePath returns the lnpm store root path
func getStorePath() (string, error) {
	if storePath := os.Getenv("LNPM_STORE"); storePath != "" {
		return storePath, nil
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}

	return filepath.Join(homeDir, ".lnpm"), nil
}

// PackagePath returns the path to a package in the store
func (s *Store) PackagePath(name, hash string) string {
	return filepath.Join(s.basePath, name, hash)
}

// Exists checks if a package with the given hash exists
func (s *Store) Exists(name, hash string) bool {
	path := s.PackagePath(name, hash)
	_, err := os.Stat(path)
	return err == nil
}

// Store copies or hard links files to the store
func (s *Store) Store(name, hash string, files []*pack.FileInfo, sourceDir string) (string, error) {
	destPath := s.PackagePath(name, hash)
	debug.Logf("store: storing %s hash=%s files=%d dest=%s", name, shortHash(hash), len(files), destPath)

	// Remove existing if present (for updates)
	if err := os.RemoveAll(destPath); err != nil {
		return "", fmt.Errorf("failed to clean existing store path: %w", err)
	}

	// Create destination directory
	if err := os.MkdirAll(destPath, 0755); err != nil {
		return "", fmt.Errorf("failed to create store directory: %w", err)
	}

	// Determine if we can use hard links (same filesystem)
	useHardLink := s.canUseHardLink(sourceDir)
	sameFS := useHardLink
	if useHardLink {
		debug.Log("store: same filesystem detected, will try reflink or hard link")
	} else {
		debug.Log("store: different filesystem, will try reflink or copy")
	}

	// Process files in parallel: try reflink/hardlink first, collect failures for copy
	total := len(files)
	var reflinkCount, hardLinkCount, copyCount int32
	warnedAboutCopy := false
	var filesToCopyMu sync.Mutex
	var filesToCopy []*pack.FileInfo

	// Parallel pass: try fast methods (reflink/hardlink)
	numWorkers := min(runtime.NumCPU(), 8)
	if len(files) < numWorkers {
		numWorkers = len(files)
	}

	var wg sync.WaitGroup
	fileChan := make(chan *pack.FileInfo, len(files))
	errChan := make(chan error, 1)
	var processed int32

	// Start workers for parallel linking
	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for f := range fileChan {
				destFile := filepath.Join(destPath, f.RelPath)

				// Create parent directories
				if err := os.MkdirAll(filepath.Dir(destFile), 0755); err != nil {
					select {
					case errChan <- fmt.Errorf("failed to create directory for %s: %w", f.RelPath, err):
					default:
					}
					return
				}

				linked := false

				// 1. Try reflink (CoW clone) - instant on APFS/Btrfs/XFS
				if reflinkFile(f.Path, destFile) == nil {
					linked = true
					atomic.AddInt32(&reflinkCount, 1)
				}

				// 2. Try hard link if on same filesystem and reflink didn't work
				if !linked && useHardLink {
					if err := os.Link(f.Path, destFile); err == nil {
						linked = true
						atomic.AddInt32(&hardLinkCount, 1)
					}
				}

				// 3. Queue for parallel copy if linking didn't work
				if !linked {
					filesToCopyMu.Lock()
					filesToCopy = append(filesToCopy, f)
					filesToCopyMu.Unlock()
				}

				current := atomic.AddInt32(&processed, 1)
				if total >= 1000 && current%1000 == 0 {
					fmt.Printf("\r  Processing... %d/%d files", current, total)
				}
			}
		}()
	}

	// Queue all files
	for _, f := range files {
		fileChan <- f
	}
	close(fileChan)

	// Wait for linking phase to complete
	wg.Wait()

	// Check for errors
	select {
	case err := <-errChan:
		return "", err
	default:
	}

	// Show warning if needed
	if len(filesToCopy) > 0 && !warnedAboutCopy && sameFS {
		fmt.Printf("  ⚠ Linking not supported, copying files instead\n")
	}

	// Parallel copy for files that couldn't be linked
	if len(filesToCopy) > 0 {
		debug.Logf("store: copying %d files in parallel", len(filesToCopy))

		// Reuse worker pool for parallel copying
		numCopyWorkers := min(runtime.NumCPU(), 8)
		if len(filesToCopy) < numCopyWorkers {
				numCopyWorkers = len(filesToCopy)
		}

		var wg2 sync.WaitGroup
		copyChan := make(chan *pack.FileInfo, len(filesToCopy))
		errChan2 := make(chan error, 1)
		var copyProcessed int32

		// Start copy workers
		for w := 0; w < numCopyWorkers; w++ {
			wg2.Add(1)
			go func() {
				defer wg2.Done()
				for f := range copyChan {
					destFile := filepath.Join(destPath, f.RelPath)
					if err := copyFile(f.Path, destFile, f.Mode); err != nil {
						select {
						case errChan2 <- fmt.Errorf("failed to copy %s: %w", f.RelPath, err):
						default:
						}
						return
					}
					atomic.AddInt32(&copyCount, 1)

					current := atomic.AddInt32(&copyProcessed, 1)
					if total >= 1000 && current%1000 == 0 {
						fmt.Printf("\r  Copying... %d/%d files", current, len(filesToCopy))
					}
				}
			}()
		}

		// Queue copy files
		for _, f := range filesToCopy {
			copyChan <- f
		}
		close(copyChan)

		// Wait for copy completion
		wg2.Wait()

		// Check for copy errors
		select {
		case err := <-errChan2:
			return "", err
		default:
		}
	}

	if total >= 1000 {
		fmt.Printf("\r                              \r") // Clear progress line
	}

	if reflinkCount > 0 || hardLinkCount > 0 {
		debug.Logf("store: reflinked %d, hard linked %d, copied %d files", reflinkCount, hardLinkCount, copyCount)
	} else if copyCount > 0 && !sameFS {
		fmt.Printf("  ℹ Store and source on different filesystems - files were copied\n")
		fmt.Printf("  💡 Tip: Move your store to the same filesystem for instant linking\n")
	}

	// Strip lifecycle scripts from package.json to prevent npm from running them
	// when installed as file: dependency (matches yalc behavior)
	if err := stripLifecycleScripts(destPath); err != nil {
		return "", fmt.Errorf("failed to strip lifecycle scripts: %w", err)
	}

	return destPath, nil
}

// Remove removes a package from the store
func (s *Store) Remove(name, hash string) error {
	path := s.PackagePath(name, hash)
	return os.RemoveAll(path)
}

// List returns all packages in the store
func (s *Store) List() ([]string, error) {
	entries, err := os.ReadDir(s.basePath)
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

// ListVersions returns all versions (hashes) of a package
func (s *Store) ListVersions(name string) ([]string, error) {
	packagePath := filepath.Join(s.basePath, name)
	entries, err := os.ReadDir(packagePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var versions []string
	for _, entry := range entries {
		if entry.IsDir() {
			versions = append(versions, entry.Name())
		}
	}
	return versions, nil
}

// GetFiles returns all files in a stored package
func (s *Store) GetFiles(name, hash string) ([]*pack.FileInfo, error) {
	storePath := s.PackagePath(name, hash)

	var files []*pack.FileInfo
	err := filepath.Walk(storePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		relPath, err := filepath.Rel(storePath, path)
		if err != nil {
			return err
		}

		files = append(files, &pack.FileInfo{
			Path:    path,
			RelPath: filepath.ToSlash(relPath),
			Size:    info.Size(),
			Mode:    info.Mode(),
		})

		return nil
	})

	return files, err
}

// canUseHardLink checks if the source directory and store are on the same filesystem
func (s *Store) canUseHardLink(sourceDir string) bool {
	// Windows: always try hard links, let it fail if needed
	if runtime.GOOS == "windows" {
		return true
	}

	// Unix: check device IDs
	storeInfo, err := os.Stat(s.basePath)
	if err != nil {
		return false
	}

	sourceInfo, err := os.Stat(sourceDir)
	if err != nil {
		return false
	}

	storeDev := getDeviceID(storeInfo)
	sourceDev := getDeviceID(sourceInfo)

	if storeDev != 0 && sourceDev != 0 {
		return storeDev == sourceDev
	}

	// Default to trying hard link
	return true
}

// copyFile copies a file preserving permissions
func copyFile(src, dst string, mode os.FileMode) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = srcFile.Close() }()

	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer func() { _ = dstFile.Close() }()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return err
	}

	return dstFile.Sync()
}

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// stripLifecycleScripts removes prepare/prepublish scripts from package.json
// This prevents npm from running these scripts when the package is installed as a file: dependency
// Matches yalc behavior: https://github.com/wclr/yalc
func stripLifecycleScripts(destPath string) error {
	pkgJSONPath := filepath.Join(destPath, "package.json")

	// Read package.json
	data, err := os.ReadFile(pkgJSONPath)
	if err != nil {
		if os.IsNotExist(err) {
			// No package.json (shouldn't happen but be defensive)
			return nil
		}
		return fmt.Errorf("failed to read package.json: %w", err)
	}

	// Handle empty file (race condition with concurrent publish)
	if len(data) == 0 {
		debug.Log("store: package.json is empty, skipping script stripping")
		return nil
	}

	var pkgJSON map[string]interface{}
	if err := json.Unmarshal(data, &pkgJSON); err != nil {
		// Could be a race condition with concurrent writes, skip gracefully
		debug.Logf("store: failed to parse package.json, skipping script stripping: %v", err)
		return nil
	}

	// Check if scripts field exists
	scripts, ok := pkgJSON["scripts"].(map[string]interface{})
	if !ok || scripts == nil {
		// No scripts, nothing to strip
		return nil
	}

	// Track if we made changes
	modified := false

	// Remove lifecycle scripts that cause issues with file: dependencies
	// - prepare/prepublish: run during npm install of file: deps, can fail (e.g., husky)
	// Matches yalc behavior: https://github.com/wclr/yalc/blob/master/src/copy.ts
	scriptsToRemove := []string{"prepare", "prepublish"}
	for _, script := range scriptsToRemove {
		if _, exists := scripts[script]; exists {
			delete(scripts, script)
			modified = true
			debug.Logf("store: stripped %s script from package.json", script)
		}
	}

	if !modified {
		return nil
	}

	// Write modified package.json atomically using temp file + rename
	output, err := json.MarshalIndent(pkgJSON, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal package.json: %w", err)
	}
	output = append(output, '\n')

	// Write to temp file first
	tmpPath := pkgJSONPath + ".tmp"
	if err := os.WriteFile(tmpPath, output, 0644); err != nil {
		return fmt.Errorf("failed to write temp package.json: %w", err)
	}

	// Atomic rename (overwrites existing)
	if err := os.Rename(tmpPath, pkgJSONPath); err != nil {
		os.Remove(tmpPath) // Clean up on failure
		return fmt.Errorf("failed to rename package.json: %w", err)
	}

	return nil
}
