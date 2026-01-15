package watch

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/fsnotify/fsnotify"
	"github.com/pedrosousa13/lnpm/internal/db"
	"github.com/pedrosousa13/lnpm/internal/debug"
	"github.com/pedrosousa13/lnpm/internal/link"
	"github.com/pedrosousa13/lnpm/internal/pack"
	"github.com/pedrosousa13/lnpm/internal/store"
)

// Watcher watches a package directory for changes and syncs to linked projects
type Watcher struct {
	packagePath   string
	packageName   string
	watcher       *fsnotify.Watcher
	ignorePatterns []string
	debounceMs    int
	execCmd       string

	// Debouncing
	pendingChanges map[string]struct{}
	changeMu       sync.Mutex
	debounceTimer  *time.Timer

	// File cache for incremental hashing
	fileCache   map[string]*pack.CachedFile
	fileCacheMu sync.RWMutex

	// Callbacks
	onSync func(files []string, projects int)
	onError func(error)

	// Control
	stopCh chan struct{}
	doneCh chan struct{}
}

// Options configures the watcher
type Options struct {
	IgnorePatterns []string
	DebounceMs     int
	ExecCmd        string
	OnSync         func(files []string, projects int)
	OnError        func(error)
}

// New creates a new watcher for the given package directory
func New(packagePath string, opts Options) (*Watcher, error) {
	// Read package.json to get name and build initial file cache
	pkgJSON, files, err := pack.Pack(packagePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read package: %w", err)
	}

	fsWatcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create watcher: %w", err)
	}

	debounce := opts.DebounceMs
	if debounce <= 0 {
		debounce = 100
	}

	// Build initial file cache from pack results
	cache := make(map[string]*pack.CachedFile, len(files))
	for _, f := range files {
		cache[f.RelPath] = &pack.CachedFile{
			RelPath:     f.RelPath,
			Size:        f.Size,
			ModTime:     f.ModTime,
			ContentHash: f.ContentHash,
			Mode:        f.Mode,
		}
	}

	w := &Watcher{
		packagePath:    packagePath,
		packageName:    pkgJSON.Name,
		watcher:        fsWatcher,
		ignorePatterns: opts.IgnorePatterns,
		debounceMs:     debounce,
		execCmd:        opts.ExecCmd,
		pendingChanges: make(map[string]struct{}),
		fileCache:      cache,
		onSync:         opts.OnSync,
		onError:        opts.OnError,
		stopCh:         make(chan struct{}),
		doneCh:         make(chan struct{}),
	}

	return w, nil
}

// Start begins watching for changes
func (w *Watcher) Start() error {
	debug.Logf("watch: starting watch on %s", w.packagePath)
	// Add all directories recursively
	if err := w.addWatchRecursive(w.packagePath); err != nil {
		return err
	}

	go w.eventLoop()
	return nil
}

// Stop stops the watcher
func (w *Watcher) Stop() {
	close(w.stopCh)
	<-w.doneCh
	_ = w.watcher.Close()
}

// addWatchRecursive adds all directories under path to the watcher
func (w *Watcher) addWatchRecursive(path string) error {
	return filepath.Walk(path, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip ignored paths
		if w.shouldIgnore(p) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// Only watch directories (fsnotify watches files in the directory)
		if info.IsDir() {
			if err := w.watcher.Add(p); err != nil {
				return fmt.Errorf("failed to watch %s: %w", p, err)
			}
		}

		return nil
	})
}

// shouldIgnore returns true if the path should be ignored
func (w *Watcher) shouldIgnore(path string) bool {
	relPath, err := filepath.Rel(w.packagePath, path)
	if err != nil {
		return false
	}

	baseName := filepath.Base(path)

	// Always ignore these
	defaultIgnore := []string{
		"node_modules",
		".git",
		".lnpm",
		"*.log",
		".DS_Store",
		"*.swp",
		"*.swo",
		"*~",
	}

	allPatterns := append(defaultIgnore, w.ignorePatterns...)

	for _, pattern := range allPatterns {
		// Check against relative path
		if matched, _ := doublestar.Match(pattern, relPath); matched {
			return true
		}
		// Check against basename for simple patterns
		if !strings.Contains(pattern, "/") {
			if matched, _ := doublestar.Match(pattern, baseName); matched {
				return true
			}
		}
	}

	return false
}

// eventLoop processes file system events
func (w *Watcher) eventLoop() {
	defer close(w.doneCh)

	for {
		select {
		case <-w.stopCh:
			return

		case event, ok := <-w.watcher.Events:
			if !ok {
				return
			}
			w.handleEvent(event)

		case err, ok := <-w.watcher.Errors:
			if !ok {
				return
			}
			if w.onError != nil {
				w.onError(err)
			}
		}
	}
}

// handleEvent processes a single file system event
func (w *Watcher) handleEvent(event fsnotify.Event) {
	// Skip ignored paths
	if w.shouldIgnore(event.Name) {
		return
	}
	debug.Logf("watch: event %s %s", event.Op, event.Name)

	// Handle new directories
	if event.Op&fsnotify.Create == fsnotify.Create {
		if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
			_ = w.watcher.Add(event.Name)
		}
	}

	// Only care about write, create, remove, rename
	if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove|fsnotify.Rename) == 0 {
		return
	}

	// Add to pending changes
	w.changeMu.Lock()
	w.pendingChanges[event.Name] = struct{}{}

	// Reset debounce timer
	if w.debounceTimer != nil {
		w.debounceTimer.Stop()
	}
	w.debounceTimer = time.AfterFunc(time.Duration(w.debounceMs)*time.Millisecond, w.processPendingChanges)
	w.changeMu.Unlock()
}

// processPendingChanges syncs all pending changes
func (w *Watcher) processPendingChanges() {
	w.changeMu.Lock()
	changes := make([]string, 0, len(w.pendingChanges))
	for path := range w.pendingChanges {
		relPath, err := filepath.Rel(w.packagePath, path)
		if err == nil {
			changes = append(changes, relPath)
		}
	}
	w.pendingChanges = make(map[string]struct{})
	w.changeMu.Unlock()

	if len(changes) == 0 {
		return
	}

	// Sync changes
	projectCount, err := w.syncChanges()
	if err != nil {
		if w.onError != nil {
			w.onError(err)
		}
		return
	}

	if w.onSync != nil {
		w.onSync(changes, projectCount)
	}
}

// syncChanges pushes changes to the store and all linked projects
func (w *Watcher) syncChanges() (int, error) {
	// Re-pack with incremental hashing using cached state
	w.fileCacheMu.RLock()
	cache := w.fileCache
	w.fileCacheMu.RUnlock()

	pkgJSON, files, err := pack.PackIncremental(w.packagePath, cache)
	if err != nil {
		return 0, fmt.Errorf("failed to pack: %w", err)
	}

	// Update cache with new file state
	newCache := make(map[string]*pack.CachedFile, len(files))
	for _, f := range files {
		newCache[f.RelPath] = &pack.CachedFile{
			RelPath:     f.RelPath,
			Size:        f.Size,
			ModTime:     f.ModTime,
			ContentHash: f.ContentHash,
			Mode:        f.Mode,
		}
	}
	w.fileCacheMu.Lock()
	w.fileCache = newCache
	w.fileCacheMu.Unlock()

	// Calculate new hash
	newHash := pack.HashFiles(files)

	// Get database
	database, err := db.GetDB()
	if err != nil {
		return 0, fmt.Errorf("failed to open database: %w", err)
	}

	// Get existing package
	pkg, err := database.GetPackageByName(pkgJSON.Name)
	if err != nil {
		return 0, fmt.Errorf("failed to look up package: %w", err)
	}

	if pkg == nil {
		return 0, fmt.Errorf("package not published yet")
	}

	// Update store
	s, err := store.New()
	if err != nil {
		return 0, fmt.Errorf("failed to access store: %w", err)
	}

	storePath, err := s.Store(pkgJSON.Name, newHash, files, w.packagePath)
	if err != nil {
		return 0, fmt.Errorf("failed to store: %w", err)
	}

	// Update database
	var totalSize int64
	for _, f := range files {
		totalSize += f.Size
	}

	pkg.Version = pkgJSON.Version
	pkg.ContentHash = newHash
	pkg.StorePath = storePath
	pkg.FilesCount = len(files)
	pkg.TotalSize = totalSize

	if err := database.InsertPackage(pkg); err != nil {
		return 0, fmt.Errorf("failed to update package: %w", err)
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
		return 0, fmt.Errorf("failed to update files: %w", err)
	}

	// Get linked projects
	projects, err := database.GetProjectsForPackage(pkg.ID)
	if err != nil {
		return 0, fmt.Errorf("failed to get linked projects: %w", err)
	}

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

	// Update all linked projects
	successCount := 0
	for _, proj := range projects {
		linker := link.New(proj.Path)
		_, err = linker.Link(pkg.Name, storePath, storeFiles)
		if err == nil {
			successCount++
		}
	}

	return successCount, nil
}
