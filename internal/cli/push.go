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

	// Link to all projects in parallel
	type result struct {
		path string
		err  error
	}
	results := make(chan result, len(projects))
	var wg sync.WaitGroup

	for _, proj := range projects {
		wg.Add(1)
		go func(p *db.Project) {
			defer wg.Done()
			linker := link.New(p.Path)
			_, err := linker.Link(pkg.Name, storePath, storeFiles)
			results <- result{path: p.Path, err: err}
		}(proj)
	}

	// Wait for all links to complete
	wg.Wait()
	close(results)

	// Print results in order received (not deterministic order)
	successCount := 0
	for res := range results {
		if res.err != nil {
			fmt.Printf("  ✗ %s: %v\n", res.path, res.err)
		} else {
			fmt.Printf("  ✓ %s\n", res.path)
			successCount++
		}
	}

	fmt.Printf("\nPushed to %d/%d projects\n", successCount, len(projects))

	return nil
}
