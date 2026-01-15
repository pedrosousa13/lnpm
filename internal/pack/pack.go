package pack

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/cespare/xxhash/v2"
	"github.com/pedrosousa13/lnpm/internal/debug"
)

// PackageJSON represents the relevant fields from package.json
type PackageJSON struct {
	Name    string   `json:"name"`
	Version string   `json:"version"`
	Main    string   `json:"main"`
	Files   []string `json:"files"`
}

// FileInfo contains information about a file to be packed
type FileInfo struct {
	Path        string      // Absolute path
	RelPath     string      // Relative path from package root
	Size        int64       // File size
	Mode        os.FileMode // File permissions
	ContentHash string      // xxhash of content
	ModTime     int64       // Unix nano timestamp
}

// defaultIncludes are files always included regardless of config
var defaultIncludes = []string{
	"package.json",
	"README*",
	"readme*",
	"LICENSE*",
	"license*",
	"LICENCE*",
	"licence*",
	"CHANGELOG*",
	"changelog*",
	"CHANGES*",
	"changes*",
	"HISTORY*",
	"history*",
}

// defaultExcludes are patterns always excluded
var defaultExcludes = []string{
	".git",
	".git/**",
	".gitignore",
	".gitattributes",
	".hg",
	".hg/**",
	".svn",
	".svn/**",
	"CVS",
	"CVS/**",
	".DS_Store",
	"Thumbs.db",
	"node_modules",
	"node_modules/**",
	".npmrc",
	".npmignore",
	".yalc",
	".yalc/**",
	".lnpm",
	".lnpm/**",
	"lnpm.lock",
	"yalc.lock",
	"*.log",
	"*.orig",
	"*.swp",
	"*.swo",
	"*~",
	".env",
	".env.*",
	"*.tgz",
}

// Pack determines which files should be included in a package publish
func Pack(packageDir string) (*PackageJSON, []*FileInfo, error) {
	return PackIncremental(packageDir, nil)
}

// CachedFile holds previous file state for incremental packing
type CachedFile struct {
	RelPath     string
	Size        int64
	ModTime     int64
	ContentHash string
	Mode        os.FileMode
}

// PackIncremental packs with optional cached state to skip rehashing unchanged files
func PackIncremental(packageDir string, cache map[string]*CachedFile) (*PackageJSON, []*FileInfo, error) {
	debug.Logf("pack: scanning %s", packageDir)
	// Read package.json
	pkgJSON, err := readPackageJSON(packageDir)
	if err != nil {
		return nil, nil, err
	}
	debug.Logf("pack: found %s@%s", pkgJSON.Name, pkgJSON.Version)

	// Try to use npm pack for file list (universal standard)
	fileList, useNpmPack := getNpmPackFileList(packageDir)

	var files []*FileInfo
	if useNpmPack {
		debug.Logf("pack: using npm pack file list (%d files)", len(fileList))
		// Use npm pack output with explicit .git safety filter
		files, err = collectFilesFromList(packageDir, fileList, cache)
		if err != nil {
			return nil, nil, err
		}
	} else {
		debug.Log("pack: npm pack unavailable, using custom filtering")
		// Fall back to custom file collection
		files, err = collectFilesIncremental(packageDir, pkgJSON.Files, cache)
		if err != nil {
			return nil, nil, err
		}
	}

	// Apply explicit .git safety filter (defense in depth)
	files = filterGitFiles(files)

	debug.Logf("pack: collected %d files (after safety filters)", len(files))

	return pkgJSON, files, nil
}

// readPackageJSON reads and parses package.json
func readPackageJSON(dir string) (*PackageJSON, error) {
	path := filepath.Join(dir, "package.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read package.json: %w", err)
	}

	var pkg PackageJSON
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nil, fmt.Errorf("failed to parse package.json: %w", err)
	}

	if pkg.Name == "" {
		return nil, fmt.Errorf("package.json must have a name field")
	}
	if pkg.Version == "" {
		return nil, fmt.Errorf("package.json must have a version field")
	}

	return &pkg, nil
}

// collectFilesIncremental walks directory with optional cache for skipping unchanged files
func collectFilesIncremental(packageDir string, filesField []string, cache map[string]*CachedFile) ([]*FileInfo, error) {
	var files []*FileInfo
	fileCount := 0
	cacheHits := 0

	// Load .npmignore or .gitignore patterns
	ignorePatterns := loadIgnorePatterns(packageDir)
	ignorePatterns = append(ignorePatterns, defaultExcludes...)

	// If files field is specified, use whitelist mode
	useWhitelist := len(filesField) > 0

	err := filepath.Walk(packageDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Get relative path
		relPath, err := filepath.Rel(packageDir, path)
		if err != nil {
			return err
		}

		// Skip root directory
		if relPath == "." {
			return nil
		}

		// Normalize path separators for pattern matching
		relPath = filepath.ToSlash(relPath)

		// Check if excluded
		if isExcluded(relPath, ignorePatterns) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip directories (we only care about files)
		if info.IsDir() {
			return nil
		}

		// Check if included
		if useWhitelist {
			if !isIncluded(relPath, filesField) && !isDefaultInclude(relPath) {
				return nil
			}
		}

		fileCount++
		if fileCount%1000 == 0 {
			fmt.Printf("\r  Scanning... %d files", fileCount)
		}

		// Check cache - skip hashing if size+mtime unchanged
		mtime := info.ModTime().UnixNano()
		size := info.Size()
		if cache != nil {
			if cached, ok := cache[relPath]; ok {
				if cached.Size == size && cached.ModTime == mtime {
					// File unchanged, reuse cached hash
					cacheHits++
					files = append(files, &FileInfo{
						Path:        path,
						RelPath:     relPath,
						Size:        size,
						Mode:        info.Mode(),
						ContentHash: cached.ContentHash,
						ModTime:     mtime,
					})
					return nil
				}
			}
		}

		// Calculate content hash (cache miss or no cache)
		hash, err := hashFile(path)
		if err != nil {
			return fmt.Errorf("failed to hash %s: %w", relPath, err)
		}

		files = append(files, &FileInfo{
			Path:        path,
			RelPath:     relPath,
			Size:        size,
			Mode:        info.Mode(),
			ContentHash: hash,
			ModTime:     mtime,
		})

		return nil
	})

	if fileCount >= 1000 {
		fmt.Printf("\r                              \r") // Clear progress line
	}
	if cache != nil && cacheHits > 0 {
		debug.Logf("pack: cache hits %d/%d files (%.0f%%)", cacheHits, fileCount, float64(cacheHits)/float64(fileCount)*100)
	}

	return files, err
}

// loadIgnorePatterns reads .npmignore or .gitignore
func loadIgnorePatterns(dir string) []string {
	var patterns []string

	// Try .npmignore first
	npmignorePath := filepath.Join(dir, ".npmignore")
	if patterns = readIgnoreFile(npmignorePath); patterns != nil {
		return patterns
	}

	// Fall back to .gitignore
	gitignorePath := filepath.Join(dir, ".gitignore")
	if patterns = readIgnoreFile(gitignorePath); patterns != nil {
		return patterns
	}

	return nil
}

// readIgnoreFile reads an ignore file and returns patterns
func readIgnoreFile(path string) []string {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()

	var patterns []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, line)
	}

	return patterns
}

// isExcluded checks if a path matches any exclude pattern
func isExcluded(relPath string, patterns []string) bool {
	baseName := filepath.Base(relPath)

	for _, pattern := range patterns {
		// Handle negation patterns
		if strings.HasPrefix(pattern, "!") {
			continue // Skip negation for now
		}

		// Full path match
		matched, _ := doublestar.Match(pattern, relPath)
		if matched {
			return true
		}

		// For patterns without path separators, also match against basename
		// This follows gitignore behavior: "*.log" matches "foo/bar.log"
		if !strings.Contains(pattern, "/") {
			if matched, _ := doublestar.Match(pattern, baseName); matched {
				return true
			}
		}

		// Also check if it's a directory prefix
		if strings.HasPrefix(relPath, pattern+"/") {
			return true
		}
	}
	return false
}

// isIncluded checks if a path matches any include pattern (files field)
func isIncluded(relPath string, patterns []string) bool {
	for _, pattern := range patterns {
		// Direct match
		if pattern == relPath {
			return true
		}

		// Glob match
		matched, _ := doublestar.Match(pattern, relPath)
		if matched {
			return true
		}

		// Directory match - if pattern is "dist", include "dist/anything"
		if strings.HasPrefix(relPath, pattern+"/") {
			return true
		}

		// Pattern might be a directory, include all contents
		if matched, _ := doublestar.Match(pattern+"/**", relPath); matched {
			return true
		}
	}
	return false
}

// isDefaultInclude checks if the file is a default include
func isDefaultInclude(relPath string) bool {
	baseName := filepath.Base(relPath)
	for _, pattern := range defaultIncludes {
		matched, _ := doublestar.Match(strings.ToLower(pattern), strings.ToLower(baseName))
		if matched {
			return true
		}
	}
	return false
}

// hashFile calculates the xxhash of a file's contents
func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	h := xxhash.New()
	if _, err := io.Copy(h, file); err != nil {
		return "", err
	}

	return fmt.Sprintf("%016x", h.Sum64()), nil
}

// HashFiles calculates a combined hash of all files
func HashFiles(files []*FileInfo) string {
	h := xxhash.New()
	for _, f := range files {
		_, _ = h.Write([]byte(f.RelPath))
		_, _ = h.Write([]byte(f.ContentHash))
	}
	return fmt.Sprintf("%016x", h.Sum64())
}

// FileInfoFromStore creates FileInfo slice from store path and relative paths
// Used to avoid re-walking store directory when file list is known from DB
func FileInfoFromStore(storePath string, entries []FileEntryData) []*FileInfo {
	files := make([]*FileInfo, len(entries))
	for i, e := range entries {
		files[i] = &FileInfo{
			Path:        filepath.Join(storePath, e.RelPath),
			RelPath:     e.RelPath,
			Size:        e.Size,
			Mode:        e.Mode,
			ContentHash: e.Hash,
		}
	}
	return files
}

// FileEntryData is a minimal struct for file data needed for linking
type FileEntryData struct {
	RelPath string
	Size    int64
	Mode    os.FileMode
	Hash    string
}

// ReadPackageJSON reads and returns just the package.json without scanning files
func ReadPackageJSON(dir string) (*PackageJSON, error) {
	return readPackageJSON(dir)
}

// HasFileChanges quickly checks if any source files are newer than stored versions
// Uses modification time comparison to avoid full file hashing
func HasFileChanges(packageDir string, storedFiles []*FileEntry) bool {
	// Build a map of stored files for quick lookup
	storedMap := make(map[string]*FileEntry, len(storedFiles))
	for _, f := range storedFiles {
		storedMap[f.RelativePath] = f
	}

	// Check for modified or new files
	hasChanges := false
	_ = filepath.Walk(packageDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}

		relPath, err := filepath.Rel(packageDir, path)
		if err != nil {
			return nil
		}
		relPath = filepath.ToSlash(relPath)

		// Check if this is a file we would include (quick check)
		if shouldIgnore(relPath) {
			return nil
		}

		stored, exists := storedMap[relPath]
		if !exists {
			// New file detected
			debug.Logf("pack: new file detected: %s", relPath)
			hasChanges = true
			return filepath.SkipAll
		}

		// Check if modified (size or mtime changed)
		modTime := info.ModTime().UnixNano()
		if info.Size() != stored.Size || modTime > stored.ModTime {
			debug.Logf("pack: file changed: %s (size: %d->%d, mtime: %d->%d)",
				relPath, stored.Size, info.Size(), stored.ModTime, modTime)
			hasChanges = true
			return filepath.SkipAll
		}

		return nil
	})

	// Check for deleted files
	if !hasChanges {
		for relPath := range storedMap {
			fullPath := filepath.Join(packageDir, filepath.FromSlash(relPath))
			if _, err := os.Stat(fullPath); os.IsNotExist(err) {
				debug.Logf("pack: file deleted: %s", relPath)
				hasChanges = true
				break
			}
		}
	}

	return hasChanges
}

// FileEntry represents a stored file entry (from database)
type FileEntry struct {
	RelativePath string
	Size         int64
	ModTime      int64
	ContentHash  string
}

// shouldIgnore does a quick check if a path should be ignored
// This is a fast pre-filter before full pattern matching
func shouldIgnore(relPath string) bool {
	// Quick checks for common patterns
	if strings.HasPrefix(relPath, "node_modules/") ||
		strings.HasPrefix(relPath, ".git/") ||
		strings.HasPrefix(relPath, ".lnpm/") ||
		strings.Contains(relPath, "/.") {
		return true
	}
	return false
}

// npmPackOutput represents the JSON output from npm pack --dry-run --json
type npmPackOutput struct {
	Files []npmPackFile `json:"files"`
}

type npmPackFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
	Mode int    `json:"mode"`
}

var (
	// Cache npm availability check
	npmAvailable     *bool
	npmCheckTime     time.Time
	npmCacheDuration = 30 * time.Second
)

// getNpmPackFileList attempts to get file list from npm pack --dry-run --json
// Returns file list and whether npm pack was successful
func getNpmPackFileList(packageDir string) ([]string, bool) {
	// Check if npm is available (with caching)
	if !isNpmAvailable() {
		return nil, false
	}

	// Execute npm pack --dry-run --json
	cmd := exec.Command("npm", "pack", "--dry-run", "--json")
	cmd.Dir = packageDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		debug.Logf("pack: npm pack failed: %v (stderr: %s)", err, stderr.String())
		return nil, false
	}

	// Parse JSON output
	var output []npmPackOutput
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		debug.Logf("pack: failed to parse npm pack output: %v", err)
		return nil, false
	}

	if len(output) == 0 {
		debug.Log("pack: npm pack returned empty output")
		return nil, false
	}

	// Extract file paths
	files := make([]string, 0, len(output[0].Files))
	for _, f := range output[0].Files {
		// npm pack returns paths like "package/file.js", strip "package/" prefix
		path := strings.TrimPrefix(f.Path, "package/")
		if path != "" {
			files = append(files, path)
		}
	}

	debug.Logf("pack: npm pack found %d files", len(files))
	return files, true
}

// isNpmAvailable checks if npm binary is available in PATH
// Results are cached for 30 seconds to avoid repeated checks
func isNpmAvailable() bool {
	now := time.Now()
	if npmAvailable != nil && now.Sub(npmCheckTime) < npmCacheDuration {
		return *npmAvailable
	}

	// Check if npm is in PATH
	_, err := exec.LookPath("npm")
	available := err == nil

	npmAvailable = &available
	npmCheckTime = now

	if !available {
		debug.Log("pack: npm not found in PATH, using custom filtering")
	}

	return available
}

// collectFilesFromList collects files from a predetermined list (from npm pack)
// Applies incremental caching and explicit .git filtering
func collectFilesFromList(packageDir string, fileList []string, cache map[string]*CachedFile) ([]*FileInfo, error) {
	files := make([]*FileInfo, 0, len(fileList))
	cacheHits := 0

	for _, relPath := range fileList {
		// Skip if it contains .git (defense in depth)
		if isGitRelatedPath(relPath) {
			debug.Logf("pack: filtering git-related file: %s", relPath)
			continue
		}

		path := filepath.Join(packageDir, filepath.FromSlash(relPath))
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				debug.Logf("pack: skipping missing file: %s", relPath)
				continue
			}
			return nil, fmt.Errorf("failed to stat %s: %w", relPath, err)
		}

		if info.IsDir() {
			continue // Skip directories
		}

		// Normalize path separators
		relPath = filepath.ToSlash(relPath)

		// Check cache - skip hashing if size+mtime unchanged
		mtime := info.ModTime().UnixNano()
		size := info.Size()
		if cache != nil {
			if cached, ok := cache[relPath]; ok {
				if cached.Size == size && cached.ModTime == mtime {
					// File unchanged, reuse cached hash
					cacheHits++
					files = append(files, &FileInfo{
						Path:        path,
						RelPath:     relPath,
						Size:        size,
						Mode:        info.Mode(),
						ContentHash: cached.ContentHash,
						ModTime:     mtime,
					})
					continue
				}
			}
		}

		// Calculate content hash (cache miss or no cache)
		hash, err := hashFile(path)
		if err != nil {
			return nil, fmt.Errorf("failed to hash %s: %w", relPath, err)
		}

		files = append(files, &FileInfo{
			Path:        path,
			RelPath:     relPath,
			Size:        size,
			Mode:        info.Mode(),
			ContentHash: hash,
			ModTime:     mtime,
		})
	}

	if cache != nil && cacheHits > 0 {
		debug.Logf("pack: cache hits %d/%d files (%.0f%%)", cacheHits, len(fileList), float64(cacheHits)/float64(len(fileList))*100)
	}

	return files, nil
}

// filterGitFiles removes any files related to .git directories
// This is a defense-in-depth safety filter applied after all other filtering
func filterGitFiles(files []*FileInfo) []*FileInfo {
	filtered := make([]*FileInfo, 0, len(files))
	removedCount := 0

	for _, f := range files {
		if isGitRelatedPath(f.RelPath) {
			debug.Logf("pack: safety filter removed: %s", f.RelPath)
			removedCount++
			continue
		}
		filtered = append(filtered, f)
	}

	if removedCount > 0 {
		debug.Logf("pack: safety filter removed %d git-related files", removedCount)
	}

	return filtered
}

// isGitRelatedPath checks if a path is related to .git directories
func isGitRelatedPath(relPath string) bool {
	// Normalize path separators
	normalized := filepath.ToSlash(strings.ToLower(relPath))

	// Check for exact .git match or .git prefix
	if normalized == ".git" {
		return true
	}

	// Check if path contains .git directory
	if strings.HasPrefix(normalized, ".git/") {
		return true
	}

	// Check if .git appears anywhere in the path
	if strings.Contains(normalized, "/.git/") || strings.Contains(normalized, "\\.git\\") {
		return true
	}

	// Check for git-related files
	base := filepath.Base(normalized)
	if base == ".gitignore" || base == ".gitattributes" || base == ".gitmodules" {
		return true
	}

	return false
}
