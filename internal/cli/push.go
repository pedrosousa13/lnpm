package cli

import (
	"fmt"
	"os"

	"github.com/pedrosousa13/lnpm/internal/db"
	"github.com/pedrosousa13/lnpm/internal/link"
	"github.com/pedrosousa13/lnpm/internal/pack"
	"github.com/pedrosousa13/lnpm/internal/store"
)

// RunPush executes the push command
func RunPush(force bool) error {
	// Get current directory
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	// Pack the package to get current state
	pkgJSON, files, err := pack.Pack(cwd)
	if err != nil {
		return fmt.Errorf("failed to pack: %w", err)
	}

	// Calculate content hash
	newHash := pack.HashFiles(files)

	// Get database
	database, err := db.GetDB()
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}

	// Get existing package from database
	pkg, err := database.GetPackageByName(pkgJSON.Name)
	if err != nil {
		return fmt.Errorf("failed to look up package: %w", err)
	}

	if pkg == nil {
		return fmt.Errorf("package %s not published yet. Run 'lnpm publish' first", pkgJSON.Name)
	}

	// Check if content has changed
	if pkg.ContentHash == newHash && !force {
		fmt.Printf("⚠ No changes detected (hash: %s)\n", shortHash(newHash))
		fmt.Println("Use --force to push anyway")
		return nil
	}

	fmt.Printf("Pushing %s@%s...\n", pkgJSON.Name, pkgJSON.Version)

	// Get store
	s, err := store.New()
	if err != nil {
		return fmt.Errorf("failed to access store: %w", err)
	}

	// Store the updated package
	storePath, err := s.Store(pkgJSON.Name, newHash, files, cwd)
	if err != nil {
		return fmt.Errorf("failed to store package: %w", err)
	}

	// Calculate total size
	var totalSize int64
	for _, f := range files {
		totalSize += f.Size
	}

	// Update package in database
	pkg.Version = pkgJSON.Version
	pkg.ContentHash = newHash
	pkg.SourcePath = cwd
	pkg.StorePath = storePath
	pkg.FilesCount = len(files)
	pkg.TotalSize = totalSize

	if err := database.InsertPackage(pkg); err != nil {
		return fmt.Errorf("failed to update package: %w", err)
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
		}
	}
	if err := database.InsertFiles(pkg.ID, fileEntries); err != nil {
		return fmt.Errorf("failed to update files: %w", err)
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

	successCount := 0
	for _, proj := range projects {
		// Get updated files from store
		storeFiles, err := s.GetFiles(pkg.Name, newHash)
		if err != nil {
			fmt.Printf("  ✗ %s: failed to get files: %v\n", proj.Path, err)
			continue
		}

		// Re-link
		linker := link.New(proj.Path)
		_, err = linker.Link(pkg.Name, storePath, storeFiles)
		if err != nil {
			fmt.Printf("  ✗ %s: %v\n", proj.Path, err)
		} else {
			fmt.Printf("  ✓ %s\n", proj.Path)
			successCount++
		}
	}

	fmt.Printf("\nPushed to %d/%d projects\n", successCount, len(projects))

	return nil
}
