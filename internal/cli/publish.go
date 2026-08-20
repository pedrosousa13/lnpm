package cli

import (
	"fmt"
	"os"
	"runtime"
	"sync"

	"github.com/pedrosousa13/lnpm/internal/config"
	"github.com/pedrosousa13/lnpm/internal/db"
	"github.com/pedrosousa13/lnpm/internal/hooks"
	"github.com/pedrosousa13/lnpm/internal/link"
	"github.com/pedrosousa13/lnpm/internal/pack"
	"github.com/pedrosousa13/lnpm/internal/store"
	"github.com/pedrosousa13/lnpm/internal/validation"
	"github.com/pedrosousa13/lnpm/internal/workspace"
)

// RunPublish executes the publish command
func RunPublish(push bool, all bool, skipHooks bool, skipValidation bool) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	// Handle --all for monorepo publishing
	if all {
		return publishAll(cwd, push, skipHooks, skipValidation)
	}

	return publishSingle(cwd, push, skipHooks, skipValidation)
}

// publishAll publishes all packages in a monorepo workspace
func publishAll(cwd string, push bool, skipHooks bool, skipValidation bool) error {
	ws, err := workspace.Detect(cwd)
	if err != nil {
		return fmt.Errorf("failed to detect workspace: %w", err)
	}

	if ws == nil {
		return fmt.Errorf("no workspace found. --all requires a monorepo with workspaces configured")
	}

	packages, err := ws.ListPackages()
	if err != nil {
		return fmt.Errorf("failed to list packages: %w", err)
	}

	if len(packages) == 0 {
		return fmt.Errorf("no packages found in workspace")
	}

	fmt.Printf("Publishing %d packages from %s workspace...\n\n", len(packages), ws.Type)

	// Publish packages in parallel with worker pool
	numWorkers := min(runtime.NumCPU(), len(packages))
	if numWorkers < 1 {
		numWorkers = 1
	}

	type publishResult struct {
		pkg *workspace.Package
		err error
	}

	pkgChan := make(chan *workspace.Package, len(packages))
	resultChan := make(chan publishResult, len(packages))
	var wg sync.WaitGroup
	var outputMu sync.Mutex // Synchronize output per package

	// Start worker goroutines
	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for pkg := range pkgChan {
				// Synchronize output for clean formatting
				outputMu.Lock()
				sep := hrule(3)
				fmt.Printf("%s %s@%s %s\n", sep, pkg.Name, pkg.Version, sep)
				outputMu.Unlock()

				err := publishSingle(pkg.Path, push, skipHooks, skipValidation)

				outputMu.Lock()
				if err != nil {
					fmt.Printf("%s Failed: %v\n\n", iconFail(), err)
				} else {
					fmt.Println()
				}
				outputMu.Unlock()

				resultChan <- publishResult{pkg: pkg, err: err}
			}
		}()
	}

	// Queue all packages
	for i := range packages {
		pkgChan <- &packages[i]
	}
	close(pkgChan)

	// Wait for all workers to complete
	wg.Wait()
	close(resultChan)

	// Count successes
	successCount := 0
	for res := range resultChan {
		if res.err == nil {
			successCount++
		}
	}

	fmt.Printf("Published %d/%d packages\n", successCount, len(packages))
	if successCount < len(packages) {
		return fmt.Errorf("%d of %d package(s) failed to publish", len(packages)-successCount, len(packages))
	}
	return nil
}

// publishSingle publishes a single package
func publishSingle(pkgPath string, push bool, skipHooks bool, skipValidation bool) error {
	// Validate package before proceeding
	if !skipValidation {
		if err := validation.ValidatePackage(pkgPath); err != nil {
			return fmt.Errorf("validation failed: %w", err)
		}
	}

	// Run custom pre_publish hook before packing
	cfg := config.Get()
	if !skipHooks {
		if err := hooks.RunCustom(pkgPath, cfg.Hooks.PrePublish, "pre_publish"); err != nil {
			return fmt.Errorf("pre_publish hook failed: %w", err)
		}
	}

	// Run prepare scripts before packing
	if err := hooks.RunPrepare(pkgPath, skipHooks); err != nil {
		return fmt.Errorf("prepare hook failed: %w", err)
	}

	// Pack the package
	pkgJSON, files, err := pack.Pack(pkgPath)
	if err != nil {
		return fmt.Errorf("failed to pack: %w", err)
	}

	// Resolve any workspace: dependency specifiers before the content hash is
	// taken, so the hash covers the bytes consumers actually install. The
	// cleanup must outlive the store copy, so it is deferred to the end of the
	// publish rather than released here.
	cleanup, err := pack.RewriteWorkspaceDeps(pkgPath, files)
	defer cleanup()
	if err != nil {
		// Deliberately unwrapped, against the "always wrap" convention: every
		// error out of RewriteWorkspaceDeps already names the specifier, the
		// dependency and the workspace, so a "failed to resolve workspace
		// dependencies:" prefix would only stutter in front of it.
		return err
	}

	// Check if already published with same hash
	database, err := db.GetDB()
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}

	contentHash := pack.HashFiles(files)
	existing, err := database.GetPackageByHash(pkgJSON.Name, contentHash)
	if err != nil {
		return fmt.Errorf("failed to check existing package: %w", err)
	}

	if existing != nil && !push {
		fmt.Printf("%s Package %s@%s already published with same content (hash: %s)\n",
			iconWarn(), pkgJSON.Name, pkgJSON.Version, shortHash(contentHash))
		fmt.Println("Use --push to update linked projects anyway")
		return nil
	}

	if err := finishPublish(pkgPath, pkgJSON, files, database, push); err != nil {
		return err
	}

	// Run custom post_publish hook after a successful publish
	if !skipHooks {
		if err := hooks.RunCustom(pkgPath, cfg.Hooks.PostPublish, "post_publish"); err != nil {
			return fmt.Errorf("post_publish hook failed: %w", err)
		}
	}

	return nil
}

// finishPublish completes publishing with pre-packed data (used by push too)
func finishPublish(pkgPath string, pkgJSON *pack.PackageJSON, files []*pack.FileInfo, database *db.DB, push bool) error {
	contentHash := pack.HashFiles(files)

	// Store the package
	s, err := store.New()
	if err != nil {
		return fmt.Errorf("failed to create store: %w", err)
	}

	fmt.Printf("Publishing %s@%s (%d files)...\n", pkgJSON.Name, pkgJSON.Version, len(files))

	storePath, err := s.Store(pkgJSON.Name, contentHash, files, pkgPath)
	if err != nil {
		return fmt.Errorf("failed to store package: %w", err)
	}

	// Calculate total size
	var totalSize int64
	for _, f := range files {
		totalSize += f.Size
	}

	// Record in database
	pkg := &db.Package{
		Name:        pkgJSON.Name,
		Version:     pkgJSON.Version,
		ContentHash: contentHash,
		SourcePath:  pkgPath,
		StorePath:   storePath,
		FilesCount:  len(files),
		TotalSize:   totalSize,
	}

	// The package row and its file manifest go in together. A relink reads the
	// file rows to decide which files it can leave alone, so a package row that
	// named this generation while the file rows still described the previous one
	// would let a changed file be carried over unchanged.
	fileEntries := make([]*db.FileEntry, len(files))
	for i, f := range files {
		fileEntries[i] = &db.FileEntry{
			RelativePath: f.RelPath,
			ContentHash:  f.ContentHash,
			Size:         f.Size,
			Mode:         f.Mode,
			ModTime:      f.ModTime,
		}
	}
	if err := database.InsertPackageWithFiles(pkg, fileEntries); err != nil {
		return fmt.Errorf("failed to record package: %w", err)
	}

	fmt.Printf("%s Published %s@%s\n", iconOK(), pkgJSON.Name, pkgJSON.Version)
	fmt.Printf("  Hash: %s\n", shortHash(contentHash))
	fmt.Printf("  Files: %d\n", len(files))
	fmt.Printf("  Size: %s\n", formatSize(totalSize))
	fmt.Printf("  Store: %s\n", storePath)

	// If push requested, push to all linked projects
	if push {
		if err := pushToLinkedProjects(database, pkg, s); err != nil {
			return err
		}
	}

	return nil
}

// pushToLinkedProjects pushes updates to all projects linked to this package.
// It returns an error if any linked project failed to update, matching push's
// own pushToAllProjects.
func pushToLinkedProjects(database *db.DB, pkg *db.Package, s *store.Store) error {
	projects, err := database.GetProjectsForPackage(pkg.ID)
	if err != nil {
		return fmt.Errorf("failed to get linked projects: %w", err)
	}

	if len(projects) == 0 {
		fmt.Println("\nNo linked projects to update")
		return nil
	}

	fmt.Printf("\nPushing to %d linked projects...\n", len(projects))

	// Push to all projects in parallel
	type result struct {
		path    string
		skipped bool
		link    link.Result
		err     error
	}
	results := make(chan result, len(projects))
	var wg sync.WaitGroup

	for _, proj := range projects {
		wg.Add(1)
		go func(p *db.Project) {
			defer wg.Done()
			linkRes, skipped, err := pushToProject(database, p, pkg, s)
			results <- result{path: p.Path, skipped: skipped, link: linkRes, err: err}
		}(proj)
	}

	// Wait for all pushes to complete
	wg.Wait()
	close(results)

	// Print results
	successCount := 0
	skippedCount := 0
	failedCount := 0
	for res := range results {
		switch {
		case res.err != nil:
			fmt.Printf("  %s %s: %v\n", iconFail(), res.path, res.err)
			failedCount++
		case res.skipped:
			fmt.Printf("  %s %s: skipped (live link to source)\n", iconOK(), res.path)
			skippedCount++
		default:
			fmt.Printf("  %s %s (%d changed, %d unchanged)\n", iconOK(), res.path, res.link.Changed, res.link.Unchanged)
			successCount++
		}
	}

	fmt.Printf("\nPushed to %d/%d projects", successCount, len(projects))
	if skippedCount > 0 {
		fmt.Printf(" (%d skipped: live link to source)", skippedCount)
	}
	fmt.Println()

	if failedCount > 0 {
		return fmt.Errorf("push failed for %d of %d project(s)", failedCount, len(projects))
	}

	return nil
}

// pushToProject pushes a package update to a single project, reporting whether
// the project was skipped rather than relinked and, when it was relinked, how
// much of the package the relink actually had to rewrite.
func pushToProject(database *db.DB, proj *db.Project, pkg *db.Package, s *store.Store) (res link.Result, skipped bool, err error) {
	// A project that added this package with --link already resolves to the
	// source directory just published. Relinking it from the store would replace
	// its live link with a snapshot copy and silently end the live updates it
	// was added for - the same hazard push guards against, reached here by
	// `publish --push`.
	linker := link.New(proj.Path)
	if linker.IsLiveLinked(pkg.Name) {
		return link.Result{}, true, nil
	}

	// Get files from store
	files, err := storeFilesForLink(database, s, pkg)
	if err != nil {
		return link.Result{}, false, err
	}

	// Re-link the package
	res, err = linker.Link(pkg.Name, pkg.StorePath, files)
	return res, false, err
}

// shortHash returns the first 8 characters of a hash
func shortHash(hash string) string {
	if len(hash) > 8 {
		return hash[:8]
	}
	return hash
}

// formatSize formats a byte size in human-readable form
func formatSize(size int64) string {
	const (
		KB = 1024
		MB = 1024 * KB
		GB = 1024 * MB
	)

	switch {
	case size >= GB:
		return fmt.Sprintf("%.2f GB", float64(size)/GB)
	case size >= MB:
		return fmt.Sprintf("%.2f MB", float64(size)/MB)
	case size >= KB:
		return fmt.Sprintf("%.2f KB", float64(size)/KB)
	default:
		return fmt.Sprintf("%d B", size)
	}
}
