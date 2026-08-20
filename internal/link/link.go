package link

import (
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/pedrosousa13/lnpm/internal/config"
	"github.com/pedrosousa13/lnpm/internal/debug"
	"github.com/pedrosousa13/lnpm/internal/fsutil"
	"github.com/pedrosousa13/lnpm/internal/pack"
)

// LinkType represents the type of linking used
type LinkType string

const (
	HardLink LinkType = "hardlink"
	Copy     LinkType = "copy"
	// Live is the type recorded when .lnpm/{package} is a link at the package's
	// source directory rather than a materialised copy of the store entry.
	Live LinkType = "link"
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
//
// Files are materialised by reflink, hard link or copy, whichever the platform
// allows. Where a hard link is used the linked file shares an inode with the
// store entry, so a consumer that edits it in place writes back into the store
// and corrupts that entry for every other consumer. Propagation is therefore
// one-way by design: linked packages are read-only from the consumer's side,
// and `push` is the supported way to update them.
func (l *Linker) Link(packageName string, storePath string, files []*pack.FileInfo) (LinkType, error) {
	debug.Logf("link: linking %s from %s (%d files)", packageName, storePath, len(files))

	// Guard against path traversal: packageName is joined into .lnpm/<name>
	// (which we replace) and node_modules/<name>.
	if err := pack.ValidatePackageName(packageName); err != nil {
		return "", err
	}

	// Determine link type based on config and filesystem
	linkType := l.determineLinkType(storePath)
	debug.Logf("link: using %s mode", linkType)

	// Populate a temp directory next to .lnpm/{package} and rename it into
	// place once it is complete. Clearing the live directory up front would
	// expose a consumer building against node_modules/{package} to an empty or
	// half-written package, and an interrupted link would leave that state
	// behind permanently.
	lnpmPath := filepath.Join(l.projectPath, ".lnpm", packageName)
	parentDir := filepath.Dir(lnpmPath)
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create .lnpm directory: %w", err)
	}
	tempPath, err := newTempDir(parentDir)
	if err != nil {
		return "", fmt.Errorf("failed to create temp .lnpm directory: %w", err)
	}
	// Remove the temp dir unless it is successfully committed via rename.
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(tempPath)
		}
	}()

	// Track what methods we actually used (atomic for parallel access)
	var reflinkCount, hardLinkCount, copyCount int32
	actualType := linkType
	var warnedAboutFallback int32
	var filesToCopyMu sync.Mutex
	var filesToCopy []struct {
		src string
		dst string
		rel string
	}

	// Parallel pass: try fast methods (reflink/hardlink)
	numWorkers := min(runtime.NumCPU(), 8)
	if len(files) < numWorkers {
		numWorkers = len(files)
	}

	var wg sync.WaitGroup
	fileChan := make(chan *pack.FileInfo, len(files))
	errChan := make(chan error, 1)

	// Start workers for parallel linking
	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for f := range fileChan {
				srcPath := filepath.Join(storePath, f.RelPath)
				dstPath := filepath.Join(tempPath, f.RelPath)

				// Create parent directory
				if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
					select {
					case errChan <- fmt.Errorf("failed to create directory for %s: %w", f.RelPath, err):
					default:
					}
					return
				}

				linked := false

				// 1. Try reflink (CoW clone) - instant on APFS/Btrfs/XFS
				if fsutil.Reflink(srcPath, dstPath) == nil {
					linked = true
					atomic.AddInt32(&reflinkCount, 1)
				}

				// 2. Try hard link if configured and reflink didn't work
				if !linked && linkType == HardLink {
					if err := os.Link(srcPath, dstPath); err == nil {
						linked = true
						atomic.AddInt32(&hardLinkCount, 1)
					} else {
						// Hard link failed, fall back to copy
						if atomic.CompareAndSwapInt32(&warnedAboutFallback, 0, 1) {
							fmt.Printf("  ⚠ Hard linking failed, falling back to copying files\n")
							debug.Logf("link: hard link failed: %v", err)
						}
					}
				}

				// 3. Queue for parallel copy if linking didn't work
				if !linked {
					filesToCopyMu.Lock()
					filesToCopy = append(filesToCopy, struct {
						src string
						dst string
						rel string
					}{srcPath, dstPath, f.RelPath})
					filesToCopyMu.Unlock()
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

	// Update actualType if we upgraded from copy
	if (reflinkCount > 0 || hardLinkCount > 0) && actualType == Copy {
		actualType = HardLink
	}
	if warnedAboutFallback > 0 {
		actualType = Copy
	}

	// Parallel copy for files that couldn't be linked
	if len(filesToCopy) > 0 {
		debug.Logf("link: copying %d files in parallel", len(filesToCopy))

		numCopyWorkers := min(runtime.NumCPU(), 8)
		if len(filesToCopy) < numCopyWorkers {
			numCopyWorkers = len(filesToCopy)
		}

		var wg2 sync.WaitGroup
		copyChan := make(chan struct {
			src string
			dst string
			rel string
		}, len(filesToCopy))
		errChan2 := make(chan error, 1)

		// Start copy workers
		for w := 0; w < numCopyWorkers; w++ {
			wg2.Add(1)
			go func() {
				defer wg2.Done()
				for item := range copyChan {
					if err := copyFile(item.src, item.dst); err != nil {
						select {
						case errChan2 <- fmt.Errorf("failed to copy %s: %w", item.rel, err):
						default:
						}
						return
					}
					atomic.AddInt32(&copyCount, 1)
				}
			}()
		}

		// Queue copy files
		for _, item := range filesToCopy {
			copyChan <- item
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

	if reflinkCount > 0 || hardLinkCount > 0 {
		debug.Logf("link: reflinked %d, hard linked %d, copied %d files", reflinkCount, hardLinkCount, copyCount)
	}

	// Swap the completed package into place. The previous directory is renamed
	// aside rather than deleted first, so .lnpm/{package} is missing for a
	// single rename instead of for as long as a whole package tree takes to
	// delete, and a failed swap can be rolled back.
	retiredPath := tempPath + ".old"
	hadPrevious := false
	if _, err := os.Lstat(lnpmPath); err == nil {
		if err := os.Rename(lnpmPath, retiredPath); err != nil {
			return "", fmt.Errorf("failed to move aside existing linked package: %w", err)
		}
		hadPrevious = true
	}
	if err := os.Rename(tempPath, lnpmPath); err != nil {
		if hadPrevious {
			// If the rollback fails too, the previous package exists only at
			// retiredPath. Name it in the error so it can be recovered by hand;
			// deleting it here would destroy the user's only copy.
			if rollbackErr := os.Rename(retiredPath, lnpmPath); rollbackErr != nil {
				return "", fmt.Errorf("failed to finalize linked package: %w (the previous package could not be restored: %v, and is preserved at %s)", err, rollbackErr, retiredPath)
			}
		}
		return "", fmt.Errorf("failed to finalize linked package: %w", err)
	}
	committed = true
	if hadPrevious {
		_ = os.RemoveAll(retiredPath)
	}

	// Create symlink in node_modules
	if err := l.createNodeModulesSymlink(packageName); err != nil {
		return "", err
	}

	return actualType, nil
}

// LinkSource points a project at a package's live source directory instead of
// at a copy of its published snapshot: .lnpm/{package} becomes a link to
// sourcePath, and node_modules/{package} the usual link into .lnpm.
//
// Nothing is materialised, so every later edit of the source is visible to the
// consumer with no further command. That is the point, and also the tradeoff:
// the consumer sees files that have not been published, or even committed,
// unlike the snapshot the default path copies out of the store.
func (l *Linker) LinkSource(packageName string, sourcePath string) (LinkType, error) {
	debug.Logf("link: live linking %s to %s", packageName, sourcePath)

	// Guard against path traversal: packageName is joined into .lnpm/<name>
	// (which we replace) and node_modules/<name>.
	if err := pack.ValidatePackageName(packageName); err != nil {
		return "", err
	}

	// An empty source path is rejected before it is resolved, because
	// filepath.Abs("") returns the working directory and a working directory
	// stats as a directory: a package row written without a source path would
	// otherwise link .lnpm/{package} at the consumer's own project root and
	// report success.
	if sourcePath == "" {
		return "", fmt.Errorf("package %s has no recorded source directory - re-publish it from its source", packageName)
	}

	// An absolute target is what a Windows junction needs, and what keeps the
	// link valid however deep .lnpm/{package} sits.
	absSource, err := filepath.Abs(sourcePath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve source path: %w", err)
	}
	info, err := os.Stat(absSource)
	if err != nil {
		return "", fmt.Errorf("source directory %s is not available: %w", absSource, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("source path %s is not a directory", absSource)
	}

	lnpmPath := filepath.Join(l.projectPath, ".lnpm", packageName)
	parentDir := filepath.Dir(lnpmPath)
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create .lnpm directory: %w", err)
	}

	// Create the link beside .lnpm/{package} and swap it in, for the reason
	// Link's comment gives. What is being replaced may be a whole store copy —
	// this is the copy-to-live conversion — and deleting that first would leave
	// .lnpm/{package} missing for as long as the tree takes to delete, with no
	// way back if creating the replacement then failed.
	tempPath, err := newTempLink(parentDir, absSource)
	if err != nil {
		return "", fmt.Errorf("failed to link source directory: %w", err)
	}
	// Remove the temp link unless it is successfully committed via rename.
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tempPath)
		}
	}()

	// Lstat, not Stat, so an existing live link whose source has since moved is
	// still seen and still renamed aside rather than left in place.
	retiredPath := tempPath + ".old"
	hadPrevious := false
	if _, err := os.Lstat(lnpmPath); err == nil {
		if err := os.Rename(lnpmPath, retiredPath); err != nil {
			return "", fmt.Errorf("failed to move aside existing linked package: %w", err)
		}
		hadPrevious = true
	}
	if err := os.Rename(tempPath, lnpmPath); err != nil {
		if hadPrevious {
			// If the rollback fails too, the previous package exists only at
			// retiredPath. Name it in the error so it can be recovered by hand;
			// deleting it here would destroy the user's only copy.
			if rollbackErr := os.Rename(retiredPath, lnpmPath); rollbackErr != nil {
				return "", fmt.Errorf("failed to finalize linked package: %w (the previous package could not be restored: %v, and is preserved at %s)", err, rollbackErr, retiredPath)
			}
		}
		return "", fmt.Errorf("failed to finalize linked package: %w", err)
	}
	committed = true
	if hadPrevious {
		// RemoveAll deletes a link without following it, so retiring a previous
		// live link cannot reach into the source tree it pointed at.
		_ = os.RemoveAll(retiredPath)
	}

	if err := l.createNodeModulesSymlink(packageName); err != nil {
		return "", err
	}

	return Live, nil
}

// Unlink removes a linked package from the project
func (l *Linker) Unlink(packageName string) error {
	if err := pack.ValidatePackageName(packageName); err != nil {
		return err
	}

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
		_ = os.Remove(lnpmDir)
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
	// To: ../.lnpm/{package} (or ../../ for scoped packages)
	// Scoped packages like @org/pkg are nested deeper, need extra ../
	upLevels := ".."
	if strings.Contains(packageName, "/") {
		upLevels = filepath.Join("..", "..")
	}
	relTarget := filepath.Join(upLevels, ".lnpm", packageName)

	if err := createDirSymlink(relTarget, linkPath); err != nil {
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
	storeDev := fsutil.DeviceID(storeInfo)
	projectDev := fsutil.DeviceID(projectInfo)

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

// IsLiveLinked reports whether .lnpm/{package} is a live link at the package's
// source directory rather than a materialised copy of the store entry.
//
// The test is positive - the entry must carry a link mode bit - rather than
// merely "not a directory". Callers skip a live link and report success, so a
// stray file, fifo or device left at that path must not pass: that is a corrupt
// project, and reporting it as skipped would report corruption as success.
//
// The bits to accept come from os/types_windows.go (Go 1.26). Both
// IO_REPARSE_TAG_SYMLINK and IO_REPARSE_TAG_MOUNT_POINT (a junction) are
// reparse-tag name surrogates, and fileStat.mode skips "m |= ModeDir" for those,
// so neither reads as a directory. Its tag switch then sets ModeSymlink for
// IO_REPARSE_TAG_SYMLINK and falls through to "default: m |= ModeIrregular" for
// a junction. Under GODEBUG=winsymlink=0 the retained modePreGo1_23 returns
// ModeSymlink for both tags instead. Accepting either bit therefore covers a
// Unix symlink, a Windows symlink and a junction under both settings.
func (l *Linker) IsLiveLinked(packageName string) bool {
	info, err := os.Lstat(filepath.Join(l.projectPath, ".lnpm", packageName))
	return err == nil && info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0
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
		// Skip dot-prefixed entries: they are in-progress or crash-orphaned
		// relink temp directories, not linked packages. This also skips a
		// package whose name starts with a dot, which is safe in practice: npm
		// forbids such names, so one can never have been linked here
		// (ValidatePackageName itself only rejects "." and "..").
		if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
			packages = append(packages, entry.Name())
		}
	}
	return packages, nil
}

// newTempDir creates a uniquely named directory inside parent and returns its
// path. The name is dot-prefixed so ListLinked skips it and so the retired-path
// scheme in Link stays inside the same namespace.
//
// This exists instead of os.MkdirTemp because MkdirTemp hardcodes mode 0700,
// whereas the linked package directory has always been created with 0755 less
// the process umask. os.Mkdir applies the umask, so the previous permissions
// are preserved by construction; a follow-up Chmod would not preserve them,
// since Chmod ignores the umask and would force 0755 unconditionally.
func newTempDir(parent string) (string, error) {
	for attempt := 0; attempt < 1000; attempt++ {
		path := filepath.Join(parent, fmt.Sprintf(".tmp-%x", rand.Uint64()))
		err := os.Mkdir(path, 0755)
		if err == nil {
			return path, nil
		}
		if !os.IsExist(err) {
			return "", err
		}
	}
	return "", fmt.Errorf("failed to find an unused temp directory name in %s", parent)
}

// createDirSymlinkFn is createDirSymlink behind a variable so a test can force
// the link creation to fail and prove that LinkSource leaves the previous
// .lnpm/{package} intact. Production code never reassigns it.
var createDirSymlinkFn = createDirSymlink

// newTempLink creates a directory link to target under a uniquely named path
// inside parent and returns that path. It is newTempDir's counterpart for
// LinkSource: the name is dot-prefixed for the same reasons, so ListLinked
// skips it and the retired-path scheme stays inside the same namespace.
func newTempLink(parent, target string) (string, error) {
	for attempt := 0; attempt < 1000; attempt++ {
		path := filepath.Join(parent, fmt.Sprintf(".tmp-%x", rand.Uint64()))
		err := createDirSymlinkFn(target, path)
		if err == nil {
			return path, nil
		}
		if !os.IsExist(err) {
			return "", err
		}
	}
	return "", fmt.Errorf("failed to find an unused temp link name in %s", parent)
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
	defer func() { _ = srcFile.Close() }()

	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, srcInfo.Mode())
	if err != nil {
		return err
	}
	defer func() { _ = dstFile.Close() }()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return err
	}

	// OpenFile's mode argument is masked by the process umask, so the copy would
	// not carry the source's exact permission bits: a 0755 bin script would land
	// at 0700 under umask 0077 and fail to execute from node_modules/.bin. Chmod
	// is not masked, so set them explicitly.
	if err := dstFile.Chmod(srcInfo.Mode()); err != nil {
		return err
	}

	return dstFile.Sync()
}
