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
	"github.com/pedrosousa13/lnpm/pkg/lockfile"
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
	// The snapshot `lnpm retreat` leaves in place of the lock file. The
	// documented publish flow is retreat, then publish, so it is sitting in the
	// package root at exactly this moment, and it records an absolute source
	// path per linked package. The patterns here are literal, not prefixes, so
	// "lnpm.lock" above does not cover it.
	lockfile.RetreatFileName,
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
				// Pruning is the one place last-match-wins does not hold end
				// to end: the walk never descends, so a later "!" pattern
				// inside this directory is never consulted.
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
			hash, err := HashFile(file.Path)
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

// isExcluded checks if a path matches any exclude pattern.
//
// It follows gitignore semantics: every pattern is evaluated and the last one
// that matches decides the outcome, so a later "!pattern" re-includes a path an
// earlier pattern excluded. collectFiles appends defaultExcludes after the
// user's patterns, which is what keeps a user negation from re-including a
// default-excluded path such as .env or node_modules.
func isExcluded(relPath string, patterns []string) bool {
	baseName := filepath.Base(relPath)
	excluded := false

	for _, pattern := range patterns {
		// A leading "!" negates: a match un-excludes the path.
		negated := strings.HasPrefix(pattern, "!")
		if negated {
			pattern = pattern[1:]
		}

		// A leading "/" anchors the pattern to the package root, so it is
		// matched against the full relative path only, never the basename.
		anchored := strings.HasPrefix(pattern, "/")
		if anchored {
			pattern = pattern[1:]
		}

		// A trailing "/" marks a directory pattern: it matches the directory
		// itself and everything under it. A negated one matches the directory
		// only, never its contents, because git cannot re-include a file whose
		// parent directory is excluded.
		//
		// Limitation: git's trailing slash matches directories only, but
		// isExcluded receives no directory signal, so a plain *file* named
		// "dist" is also matched by "dist/". Threading an isDir flag through
		// the signature is out of scope here.
		if strings.HasSuffix(pattern, "/") {
			dir := strings.TrimSuffix(pattern, "/")
			if dir == "" {
				continue
			}
			if relPath == dir || (!negated && strings.HasPrefix(relPath, dir+"/")) {
				excluded = !negated
			}
			continue
		}

		if pattern == "" {
			continue
		}

		if matchesIgnorePattern(relPath, baseName, pattern, anchored, negated) {
			excluded = !negated
		}
	}

	return excluded
}

// matchesIgnorePattern reports whether relPath matches a single ignore pattern
// that has already had its "!" and leading "/" stripped. Directory patterns
// (a trailing "/") never reach here: isExcluded handles them inline.
//
// negated says the "!" was there. It only ever narrows the match: a negated
// pattern matches the paths it names directly, but not the paths merely
// underneath a directory it names, because git cannot re-include a file whose
// parent directory is excluded. A positive pattern keeps that reach, since git
// ignores everything inside an ignored directory.
func matchesIgnorePattern(relPath, baseName, pattern string, anchored, negated bool) bool {
	// "dir/**" matches the directory and everything under it.
	if strings.HasSuffix(pattern, "/**") {
		prefix := strings.TrimSuffix(pattern, "/**")
		return relPath == prefix || (!negated && strings.HasPrefix(relPath, prefix+"/"))
	}

	// Exact match
	if pattern == relPath {
		return true
	}

	if !anchored && !strings.Contains(pattern, "/") {
		// For unanchored patterns without path separators, also match against
		// the basename. This follows gitignore behavior: "*.log" matches
		// "foo/bar.log".
		if matched, _ := filepath.Match(pattern, baseName); matched {
			return true
		}
	} else {
		// Full path glob match
		if matched, _ := filepath.Match(pattern, relPath); matched {
			return true
		}
	}

	// Directory prefix match: "node_modules" excludes "node_modules/foo".
	return !negated && strings.HasPrefix(relPath, pattern+"/")
}

// isIncluded checks if a path matches any include pattern (files field).
//
// Each pattern is normalized before matching, the way npm reads a "files"
// entry. A leading "/" anchors the pattern to the package root rather than
// naming an absolute path, and it is dropped because every match below is
// against the full relative path: isIncluded never matches a basename, so
// anchoring is already how it behaves and the "/" carries no information.
// Contrast isExcluded, which keeps the leading "/" as its anchored flag
// precisely because it does match basenames and must suppress that. Teaching
// isIncluded to match basenames (npm's "files" does match a bare "*.md"
// against a nested path) would mean handling anchoring here too.
//
// A trailing "/" marks a directory whose contents are included, and is dropped
// only from patterns with no glob metacharacter. npm does not read a trailing
// slash on a glob as a directory marker, so "dist/**/" matches nothing and
// must not be normalized into "dist/**", which matches everything under dist.
//
// So "dist", "/dist", "dist/" and "/dist/" are all equivalent, as they are to
// npm, and "dist/**" and "/dist/**" are equivalent to each other. "dist/**/"
// is equivalent to none of them.
//
// An entry that is empty once normalized — "", "/" or "//" — includes
// everything, which is what npm does with it: all three ship the same files as
// a package with no "files" field. isExcluded skips such a pattern for the same
// reason, so neither function filters a path out on the strength of one.
func isIncluded(relPath string, patterns []string) bool {
	for _, pattern := range patterns {
		pattern = strings.TrimPrefix(pattern, "/")
		if !strings.Contains(pattern, "*") {
			pattern = strings.TrimSuffix(pattern, "/")
		}

		if pattern == "" {
			return true
		}

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

// HashFile calculates the xxhash of a file's contents
func HashFile(path string) (string, error) {
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

// ExcludedByProjectRules reports whether the package at dir keeps relPath out of
// a published tarball through rules of its own: the package.json "files" field
// passed as filesField, and the root .npmignore - falling back to a root
// .gitignore when there is none, as npm does.
//
// It deliberately does not consult defaultExcludes. Those are lnpm's additions
// to npm's rules, and this answers what a tool that reads only the project's own
// rules would ship - which is what `npm publish` is. `lnpm check` uses it to ask
// whether a file lnpm left in the project root is about to be published by
// something other than lnpm.
func ExcludedByProjectRules(dir string, filesField []string, relPath string) bool {
	if len(filesField) > 0 && !isIncluded(relPath, filesField) && !isDefaultInclude(relPath) {
		return true
	}
	return isExcluded(relPath, loadIgnorePatterns(dir))
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
