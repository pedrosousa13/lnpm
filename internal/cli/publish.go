package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/user/lnpm/internal/db"
	"github.com/user/lnpm/internal/link"
	"github.com/user/lnpm/internal/pack"
	"github.com/user/lnpm/internal/store"
	"github.com/user/lnpm/internal/workspace"
)

// RunPublish executes the publish command
func RunPublish(push bool, tag string, all bool) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	// Handle --all for monorepo publishing
	if all {
		return publishAll(cwd, push, tag)
	}

	return publishSingle(cwd, push, tag)
}

// publishAll publishes all packages in a monorepo workspace
func publishAll(cwd string, push bool, tag string) error {
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

	successCount := 0
	for _, pkg := range packages {
		fmt.Printf("─── %s@%s ───\n", pkg.Name, pkg.Version)
		if err := publishSingle(pkg.Path, push, tag); err != nil {
			fmt.Printf("✗ Failed: %v\n\n", err)
		} else {
			successCount++
			fmt.Println()
		}
	}

	fmt.Printf("Published %d/%d packages\n", successCount, len(packages))
	return nil
}

// publishSingle publishes a single package
func publishSingle(pkgPath string, push bool, tag string) error {
	// Pack the package
	pkgJSON, files, err := pack.Pack(pkgPath)
	if err != nil {
		return fmt.Errorf("failed to pack: %w", err)
	}

	// Calculate content hash
	contentHash := pack.HashFiles(files)

	// Check if already published with same hash
	database, err := db.GetDB()
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}

	existing, err := database.GetPackageByHash(pkgJSON.Name, contentHash)
	if err != nil {
		return fmt.Errorf("failed to check existing package: %w", err)
	}

	if existing != nil && !push {
		fmt.Printf("⚠ Package %s@%s already published with same content (hash: %s)\n",
			pkgJSON.Name, pkgJSON.Version, shortHash(contentHash))
		fmt.Println("Use --push to update linked projects anyway")
		return nil
	}

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

	if err := database.InsertPackage(pkg); err != nil {
		return fmt.Errorf("failed to record package: %w", err)
	}

	// Store file manifest
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
		return fmt.Errorf("failed to record files: %w", err)
	}

	fmt.Printf("✓ Published %s@%s\n", pkgJSON.Name, pkgJSON.Version)
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

// pushToLinkedProjects pushes updates to all projects linked to this package
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

	for _, proj := range projects {
		if err := pushToProject(proj, pkg, s); err != nil {
			fmt.Printf("  ✗ %s: %v\n", proj.Path, err)
		} else {
			fmt.Printf("  ✓ %s\n", proj.Path)
		}
	}

	return nil
}

// pushToProject pushes a package update to a single project
func pushToProject(proj *db.Project, pkg *db.Package, s *store.Store) error {
	// Get files from store
	files, err := s.GetFiles(pkg.Name, pkg.ContentHash)
	if err != nil {
		return err
	}

	// Re-link the package
	linker := link.New(proj.Path)
	_, err = linker.Link(pkg.Name, pkg.StorePath, files)
	return err
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

// GetPackageName returns the name of the package in the current directory
func GetPackageName() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	pkgPath := filepath.Join(cwd, "package.json")
	if _, err := os.Stat(pkgPath); err != nil {
		return "", fmt.Errorf("no package.json found in current directory")
	}

	pkgJSON, _, err := pack.Pack(cwd)
	if err != nil {
		return "", err
	}

	return pkgJSON.Name, nil
}
