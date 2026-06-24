package pack

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/cespare/xxhash/v2"
	"github.com/panjf2000/ants/v2"
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
	debug.Logf("pack: scanning %s", packageDir)
	// Read package.json
	pkgJSON, err := readPackageJSON(packageDir)
	if err != nil {
		return nil, nil, err
	}
	debug.Logf("pack: found %s@%s", pkgJSON.Name, pkgJSON.Version)

	// Collect files using custom filtering
	files, err := collectFiles(packageDir, pkgJSON.Files)
	if err != nil {
		return nil, nil, err
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
	if err := ValidatePackageName(pkg.Name); err != nil {
		return nil, err
	}
	if pkg.Version == "" {
		return nil, fmt.Errorf("package.json must have a version field")
	}

	return &pkg, nil
}

// collectFiles walks directory and returns files to include in a package
func collectFiles(packageDir string, filesField []string) ([]*FileInfo, error) {
	var filesToHash []*FileInfo
	fileCount := 0

	// Load .npmignore or .gitignore patterns
	ignorePatterns := loadIgnorePatterns(packageDir)
	ignorePatterns = append(ignorePatterns, defaultExcludes...)

	// If files field is specified, use whitelist mode
	useWhitelist := len(filesField) > 0

	// First pass: collect all files
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

		// Skip symlinks. Following them would dereference into the store and
		// could pull in files outside the package (e.g. a link to ~/.ssh).
		// npm likewise does not follow symlinks out of the package.
		if info.Mode()&os.ModeSymlink != 0 {
			debug.Logf("pack: skipping symlink %s", relPath)
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

		// Need to hash this file
		filesToHash = append(filesToHash, &FileInfo{
			Path:    path,
			RelPath: relPath,
			Size:    info.Size(),
			Mode:    info.Mode(),
			ModTime: info.ModTime().UnixNano(),
		})

		return nil
	})

	if fileCount >= 1000 {
		fmt.Printf("\r                                        \r") // Clear progress line
	}

	if err != nil {
		return nil, err
	}

	// Second pass: hash files in parallel using ants pool
	if len(filesToHash) > 0 {
		var wg sync.WaitGroup
		wg.Add(len(filesToHash))

		// Capture the first hashing error instead of swallowing it — an
		// empty ContentHash would otherwise corrupt the package content hash.
		var hashErrMu sync.Mutex
		var hashErr error

		pool, err := ants.NewPoolWithFunc(runtime.NumCPU()*2, func(i interface{}) {
			defer wg.Done()
			file := i.(*FileInfo)
			hash, err := hashFile(file.Path)
			if err != nil {
				hashErrMu.Lock()
				if hashErr == nil {
					hashErr = fmt.Errorf("failed to hash %s: %w", file.RelPath, err)
				}
				hashErrMu.Unlock()
				return
			}
			file.ContentHash = hash
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create worker pool: %w", err)
		}
		defer pool.Release()

		// Submit all files to pool
		for _, file := range filesToHash {
			if err := pool.Invoke(file); err != nil {
				return nil, fmt.Errorf("failed to hash files: %w", err)
			}
		}

		// Wait for all workers to complete before proceeding
		wg.Wait()

		if hashErr != nil {
			return nil, hashErr
		}
	}

	return filesToHash, nil
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
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var patterns []string
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
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

		// Handle ** patterns (common in .gitignore)
		if strings.HasSuffix(pattern, "/**") {
			prefix := strings.TrimSuffix(pattern, "/**")
			if relPath == prefix || strings.HasPrefix(relPath, prefix+"/") {
				return true
			}
			continue
		}

		// Exact match
		if pattern == relPath {
			return true
		}

		// For patterns without path separators, also match against basename
		// This follows gitignore behavior: "*.log" matches "foo/bar.log"
		if !strings.Contains(pattern, "/") {
			if matched, _ := filepath.Match(pattern, baseName); matched {
				return true
			}
		} else {
			// Full path glob match
			if matched, _ := filepath.Match(pattern, relPath); matched {
				return true
			}
		}

		// Directory prefix match
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

		// Directory match - if pattern is "dist", include "dist/anything"
		if strings.HasPrefix(relPath, pattern+"/") {
			return true
		}

		// Handle ** patterns
		if strings.HasSuffix(pattern, "/**") {
			prefix := strings.TrimSuffix(pattern, "/**")
			if relPath == prefix || strings.HasPrefix(relPath, prefix+"/") {
				return true
			}
			continue
		}

		// Glob match
		if matched, _ := filepath.Match(pattern, relPath); matched {
			return true
		}
	}
	return false
}

// isDefaultInclude checks if the file is a default include
func isDefaultInclude(relPath string) bool {
	baseName := filepath.Base(relPath)
	for _, pattern := range defaultIncludes {
		matched, _ := filepath.Match(strings.ToLower(pattern), strings.ToLower(baseName))
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
	defer func() { _ = file.Close() }()

	h := xxhash.New()
	if _, err := io.Copy(h, file); err != nil {
		return "", err
	}

	return fmt.Sprintf("%016x", h.Sum64()), nil
}

// HashFiles calculates a combined hash of all files
func HashFiles(files []*FileInfo) string {
	// Sort by path so the hash is independent of collection/cache order, and
	// include the file mode so permission changes (e.g. chmod +x on a bin)
	// produce a new hash.
	sorted := make([]*FileInfo, len(files))
	copy(sorted, files)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].RelPath < sorted[j].RelPath
	})

	h := xxhash.New()
	for _, f := range sorted {
		_, _ = h.Write([]byte(f.RelPath))
		_, _ = h.Write([]byte(f.ContentHash))
		_, _ = fmt.Fprintf(h, "%o", f.Mode.Perm())
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
