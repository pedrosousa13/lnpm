package pack

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

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

	// Build file list
	files, err := collectFiles(packageDir, pkgJSON.Files)
	if err != nil {
		return nil, nil, err
	}
	debug.Logf("pack: collected %d files", len(files))

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

// collectFiles walks the directory and collects files based on include/exclude rules
func collectFiles(packageDir string, filesField []string) ([]*FileInfo, error) {
	var files []*FileInfo
	fileCount := 0

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


		// Calculate content hash
		hash, err := hashFile(path)
		if err != nil {
			return fmt.Errorf("failed to hash %s: %w", relPath, err)
		}

		fileCount++
		if fileCount%1000 == 0 {
			fmt.Printf("\r  Scanning... %d files", fileCount)
		}

		files = append(files, &FileInfo{
			Path:        path,
			RelPath:     relPath,
			Size:        info.Size(),
			Mode:        info.Mode(),
			ContentHash: hash,
		})

		return nil
	})

	if fileCount >= 1000 {
		fmt.Printf("\r                              \r") // Clear progress line
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
