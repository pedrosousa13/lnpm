package cli

import (
	"fmt"
	"os"
	"sync"

	"github.com/pedrosousa13/lnpm/internal/db"
	"github.com/pedrosousa13/lnpm/internal/hooks"
	"github.com/pedrosousa13/lnpm/internal/link"
	"github.com/pedrosousa13/lnpm/internal/pack"
	"github.com/pedrosousa13/lnpm/internal/store"
)

// RunPush executes the push command
func RunPush(skipHooks bool) error {
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

	// Read package.json to get package name
	pkgJSON, err := pack.ReadPackageJSON(cwd)
	if err != nil {
		return fmt.Errorf("failed to read package.json: %w", err)
	}

	// Get existing package from database
	pkg, err := database.GetPackageByName(pkgJSON.Name)
	if err != nil {
		return fmt.Errorf("failed to look up package: %w", err)
	}

	// If not published yet, delegate to publish
	if pkg == nil {
		fmt.Printf("Package %s not published yet, publishing...\n", pkgJSON.Name)

		// Run prepare scripts before packing
		if err := hooks.RunPrepare(cwd, skipHooks); err != nil {
			return fmt.Errorf("prepare hook failed: %w", err)
		}

		_, files, err := pack.Pack(cwd)
		if err != nil {
			return fmt.Errorf("failed to pack: %w", err)
		}
		cleanup, err := pack.RewriteWorkspaceDeps(cwd, files)
		defer cleanup()
		if err != nil {
			return err
		}
		return finishPublish(cwd, pkgJSON, files, database, false)
	}

	// Always run prepare scripts before packing
	if err := hooks.RunPrepare(cwd, skipHooks); err != nil {
		return fmt.Errorf("prepare hook failed: %w", err)
	}

	// Pack files
	_, files, err := pack.Pack(cwd)
	if err != nil {
		return fmt.Errorf("failed to pack: %w", err)
	}

	// Resolve any workspace: dependency specifiers before hashing, so a push
	// stores the same resolved package.json a publish would.
	cleanup, err := pack.RewriteWorkspaceDeps(cwd, files)
	defer cleanup()
	if err != nil {
		return err
	}

	// Calculate content hash
	newHash := pack.HashFiles(files)

	fmt.Printf("Pushing %s@%s...\n", pkgJSON.Name, pkgJSON.Version)

	// Get store
	s, err := store.New()
	if err != nil {
		return fmt.Errorf("failed to access store: %w", err)
	}

	// Check if we need to store (hash changed or doesn't exist)
	var storePath string
	if pkg.ContentHash != newHash || !s.Exists(pkgJSON.Name, newHash) {
		// Store the updated package
		storePath, err = s.Store(pkgJSON.Name, newHash, files, cwd)
		if err != nil {
			return fmt.Errorf("failed to store package: %w", err)
		}
	} else {
		// Reuse existing store path
		storePath = s.PackagePath(pkgJSON.Name, newHash)
	}

	// Calculate total size
	var totalSize int64
	for _, f := range files {
		totalSize += f.Size
	}

	// Update package in database
	hashChanged := pkg.ContentHash != newHash
	pkg.Version = pkgJSON.Version
	pkg.ContentHash = newHash
	pkg.SourcePath = cwd
	pkg.StorePath = storePath
	pkg.FilesCount = len(files)
	pkg.TotalSize = totalSize

	if err := database.InsertPackage(pkg); err != nil {
		return fmt.Errorf("failed to update package: %w", err)
	}

	// Only update file manifest if content hash changed
	if hashChanged {
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
	}

	// Get linked projects
	projects, err := database.GetProjectsForPackage(pkg.ID)
	if err != nil {
		return fmt.Errorf("failed to get linked projects: %w", err)
	}

	if len(projects) == 0 {
		fmt.Printf("%s Updated %s@%s in store\n", iconOK(), pkgJSON.Name, pkgJSON.Version)
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

	// Link to all projects in parallel
	type result struct {
		path    string
		skipped bool
		err     error
	}
	results := make(chan result, len(projects))
	var wg sync.WaitGroup

	for _, proj := range projects {
		wg.Add(1)
		go func(p *db.Project) {
			defer wg.Done()
			linker := link.New(p.Path)
			// A project that added this package with --link already resolves to
			// the source directory being pushed. Relinking it from the store
			// would replace its live link with a snapshot copy and silently end
			// the live updates it was added for.
			if linker.IsLiveLinked(pkg.Name) {
				results <- result{path: p.Path, skipped: true}
				return
			}
			_, err := linker.Link(pkg.Name, storePath, storeFiles)
			results <- result{path: p.Path, err: err}
		}(proj)
	}

	// Wait for all links to complete
	wg.Wait()
	close(results)

	// Print results in order received (not deterministic order)
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
			fmt.Printf("  %s %s\n", iconOK(), res.path)
			successCount++
		}
	}

	// The denominator is every project considered, matching the count announced
	// above. Live-linked projects are reported as skipped instead of being taken
	// out of the total: removing them made the two lines contradict each other,
	// and an all-live push report "Pushed to 0/0 projects".
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
