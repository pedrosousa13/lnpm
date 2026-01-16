package tests

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/pedrosousa13/lnpm/internal/db"
)

// TestDatabaseConcurrentPackageInserts tests concurrent package insertions
func TestDatabaseConcurrentPackageInserts(t *testing.T) {
	// Don't use t.Parallel() - this test controls its own concurrency
	env := setupTest(t)

	// Insert packages concurrently
	packageCount := 50
	var wg sync.WaitGroup
	errors := make(chan error, packageCount)

	for i := 0; i < packageCount; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			pkg := &db.Package{
				Name:        fmt.Sprintf("pkg-%d", idx),
				Version:     "1.0.0",
				ContentHash: fmt.Sprintf("hash-%d", idx),
				SourcePath:  fmt.Sprintf("/src/pkg-%d", idx),
				StorePath:   fmt.Sprintf("/store/pkg-%d", idx),
				FilesCount:  10,
				TotalSize:   1024,
			}
			if err := env.Database.InsertPackage(pkg); err != nil {
				errors <- fmt.Errorf("failed to insert pkg-%d: %w", idx, err)
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	// Check for errors
	for err := range errors {
		t.Error(err)
	}

	// Verify all packages were inserted
	packages, err := env.Database.ListPackages()
	if err != nil {
		t.Fatalf("Failed to list packages: %v", err)
	}

	if len(packages) != packageCount {
		t.Errorf("Expected %d packages, got %d", packageCount, len(packages))
	}
}

// TestDatabaseConcurrentLinkInserts tests concurrent link insertions
func TestDatabaseConcurrentLinkInserts(t *testing.T) {
	// Don't use t.Parallel() - this test controls its own concurrency
	env := setupTest(t)

	// Create a package and project first
	pkg := &db.Package{
		Name:        "shared-pkg",
		Version:     "1.0.0",
		ContentHash: "shared-hash",
		SourcePath:  "/src/shared",
		StorePath:   "/store/shared",
		FilesCount:  10,
		TotalSize:   1024,
	}
	if err := env.Database.InsertPackage(pkg); err != nil {
		t.Fatalf("Failed to insert package: %v", err)
	}

	// Create multiple projects and link them concurrently
	projectCount := 30
	var wg sync.WaitGroup
	errors := make(chan error, projectCount)

	for i := 0; i < projectCount; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			// Insert project
			proj := &db.Project{
				Path:           fmt.Sprintf("/project-%d", idx),
				Name:           fmt.Sprintf("project-%d", idx),
				PackageManager: "npm",
			}
			if err := env.Database.InsertProject(proj); err != nil {
				errors <- fmt.Errorf("failed to insert project-%d: %w", idx, err)
				return
			}

			// Get project to get ID
			existingProj, err := env.Database.GetProjectByPath(proj.Path)
			if err != nil {
				errors <- fmt.Errorf("failed to get project-%d: %w", idx, err)
				return
			}

			// Insert link
			link := &db.Link{
				PackageID: pkg.ID,
				ProjectID: existingProj.ID,
				LinkType:  "reflink",
			}
			if err := env.Database.InsertLink(link); err != nil {
				errors <- fmt.Errorf("failed to insert link for project-%d: %w", idx, err)
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	// Check for errors
	for err := range errors {
		t.Error(err)
	}

	// Verify all links were created
	links, err := env.Database.GetLinksForPackage(pkg.ID)
	if err != nil {
		t.Fatalf("Failed to get links: %v", err)
	}

	if len(links) != projectCount {
		t.Errorf("Expected %d links, got %d", projectCount, len(links))
	}
}

// TestDatabaseConcurrentReadsWrites tests concurrent reads and writes
func TestDatabaseConcurrentReadsWrites(t *testing.T) {
	// Don't use t.Parallel() - this test controls its own concurrency
	env := setupTest(t)

	// Insert initial packages
	for i := 0; i < 10; i++ {
		pkg := &db.Package{
			Name:        fmt.Sprintf("pkg-%d", i),
			Version:     "1.0.0",
			ContentHash: fmt.Sprintf("hash-%d", i),
			SourcePath:  fmt.Sprintf("/src/pkg-%d", i),
			StorePath:   fmt.Sprintf("/store/pkg-%d", i),
			FilesCount:  10,
			TotalSize:   1024,
		}
		if err := env.Database.InsertPackage(pkg); err != nil {
			t.Fatalf("Failed to insert initial package: %v", err)
		}
	}

	// Perform concurrent reads and writes
	var wg sync.WaitGroup
	errors := make(chan error, 100)

	// Readers
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			pkgName := fmt.Sprintf("pkg-%d", idx%10)
			_, err := env.Database.GetPackageByName(pkgName)
			if err != nil {
				errors <- fmt.Errorf("reader %d failed: %w", idx, err)
			}
		}(i)
	}

	// Writers
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			pkg := &db.Package{
				Name:        fmt.Sprintf("new-pkg-%d", idx),
				Version:     "1.0.0",
				ContentHash: fmt.Sprintf("new-hash-%d", idx),
				SourcePath:  fmt.Sprintf("/src/new-pkg-%d", idx),
				StorePath:   fmt.Sprintf("/store/new-pkg-%d", idx),
				FilesCount:  10,
				TotalSize:   1024,
			}
			if err := env.Database.InsertPackage(pkg); err != nil {
				errors <- fmt.Errorf("writer %d failed: %w", idx, err)
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	// Check for errors
	for err := range errors {
		t.Error(err)
	}
}

// TestDatabasePackageUpdatePreservesLinks tests that updating a package preserves links
func TestDatabasePackageUpdatePreservesLinks(t *testing.T) {
	t.Parallel()
	env := setupTest(t)

	// Insert a package
	pkg := &db.Package{
		Name:        "update-pkg",
		Version:     "1.0.0",
		ContentHash: "hash-v1",
		SourcePath:  "/src/update-pkg",
		StorePath:   "/store/update-pkg",
		FilesCount:  10,
		TotalSize:   1024,
	}
	if err := env.Database.InsertPackage(pkg); err != nil {
		t.Fatalf("Failed to insert package: %v", err)
	}

	// Create a project and link
	proj := &db.Project{
		Path:           "/project",
		Name:           "project",
		PackageManager: "npm",
	}
	if err := env.Database.InsertProject(proj); err != nil {
		t.Fatalf("Failed to insert project: %v", err)
	}

	existingProj, _ := env.Database.GetProjectByPath(proj.Path)
	link := &db.Link{
		PackageID: pkg.ID,
		ProjectID: existingProj.ID,
		LinkType:  "reflink",
	}
	if err := env.Database.InsertLink(link); err != nil {
		t.Fatalf("Failed to insert link: %v", err)
	}

	// Update the package
	updatedPkg := &db.Package{
		Name:        "update-pkg",
		Version:     "2.0.0",
		ContentHash: "hash-v2",
		SourcePath:  "/src/update-pkg",
		StorePath:   "/store/update-pkg-v2",
		FilesCount:  15,
		TotalSize:   2048,
	}
	if err := env.Database.InsertPackage(updatedPkg); err != nil {
		t.Fatalf("Failed to update package: %v", err)
	}

	// Verify link still exists
	links, err := env.Database.GetLinksForPackage(pkg.ID)
	if err != nil {
		t.Fatalf("Failed to get links: %v", err)
	}

	if len(links) != 1 {
		t.Errorf("Expected 1 link after update, got %d", len(links))
	}
}

// TestDatabaseProjectUpdatePreservesLinks tests that updating a project preserves links
func TestDatabaseProjectUpdatePreservesLinks(t *testing.T) {
	t.Parallel()
	env := setupTest(t)

	// Insert a package
	pkg := &db.Package{
		Name:        "test-pkg",
		Version:     "1.0.0",
		ContentHash: "hash",
		SourcePath:  "/src/test",
		StorePath:   "/store/test",
		FilesCount:  10,
		TotalSize:   1024,
	}
	if err := env.Database.InsertPackage(pkg); err != nil {
		t.Fatalf("Failed to insert package: %v", err)
	}

	// Create a project and link
	proj := &db.Project{
		Path:           "/project",
		Name:           "old-name",
		PackageManager: "npm",
	}
	if err := env.Database.InsertProject(proj); err != nil {
		t.Fatalf("Failed to insert project: %v", err)
	}

	existingProj, _ := env.Database.GetProjectByPath(proj.Path)
	link := &db.Link{
		PackageID: pkg.ID,
		ProjectID: existingProj.ID,
		LinkType:  "reflink",
	}
	if err := env.Database.InsertLink(link); err != nil {
		t.Fatalf("Failed to insert link: %v", err)
	}

	// Update the project
	updatedProj := &db.Project{
		Path:           "/project",
		Name:           "new-name",
		PackageManager: "pnpm",
	}
	if err := env.Database.InsertProject(updatedProj); err != nil {
		t.Fatalf("Failed to update project: %v", err)
	}

	// Verify link still exists
	updatedExisting, _ := env.Database.GetProjectByPath(proj.Path)
	links, err := env.Database.GetLinksForProject(updatedExisting.ID)
	if err != nil {
		t.Fatalf("Failed to get links: %v", err)
	}

	if len(links) != 1 {
		t.Errorf("Expected 1 link after project update, got %d", len(links))
	}
}

// TestDatabaseLinkDeletionDoesntAffectOthers tests that deleting a link doesn't affect other links
func TestDatabaseLinkDeletionDoesntAffectOthers(t *testing.T) {
	t.Parallel()
	env := setupTest(t)

	// Insert a package
	pkg := &db.Package{
		Name:        "shared-pkg",
		Version:     "1.0.0",
		ContentHash: "hash",
		SourcePath:  "/src/shared",
		StorePath:   "/store/shared",
		FilesCount:  10,
		TotalSize:   1024,
	}
	if err := env.Database.InsertPackage(pkg); err != nil {
		t.Fatalf("Failed to insert package: %v", err)
	}

	// Create two projects with links
	proj1 := &db.Project{
		Path:           "/project1",
		Name:           "project1",
		PackageManager: "npm",
	}
	if err := env.Database.InsertProject(proj1); err != nil {
		t.Fatalf("Failed to insert project1: %v", err)
	}

	proj2 := &db.Project{
		Path:           "/project2",
		Name:           "project2",
		PackageManager: "npm",
	}
	if err := env.Database.InsertProject(proj2); err != nil {
		t.Fatalf("Failed to insert project2: %v", err)
	}

	existingProj1, _ := env.Database.GetProjectByPath(proj1.Path)
	existingProj2, _ := env.Database.GetProjectByPath(proj2.Path)

	// Create links
	link1 := &db.Link{
		PackageID: pkg.ID,
		ProjectID: existingProj1.ID,
		LinkType:  "reflink",
	}
	if err := env.Database.InsertLink(link1); err != nil {
		t.Fatalf("Failed to insert link1: %v", err)
	}

	link2 := &db.Link{
		PackageID: pkg.ID,
		ProjectID: existingProj2.ID,
		LinkType:  "reflink",
	}
	if err := env.Database.InsertLink(link2); err != nil {
		t.Fatalf("Failed to insert link2: %v", err)
	}

	// Delete first link
	if err := env.Database.DeleteLink(pkg.ID, existingProj1.ID); err != nil {
		t.Fatalf("Failed to delete link: %v", err)
	}

	// Verify second link still exists
	links, err := env.Database.GetLinksForProject(existingProj2.ID)
	if err != nil {
		t.Fatalf("Failed to get links: %v", err)
	}

	if len(links) != 1 {
		t.Errorf("Expected 1 link for project2, got %d", len(links))
	}
}

// TestDatabasePackageByNameLookup tests package lookup by name
func TestDatabasePackageByNameLookup(t *testing.T) {
	t.Parallel()
	env := setupTest(t)

	// Insert multiple packages
	for i := 0; i < 10; i++ {
		pkg := &db.Package{
			Name:        fmt.Sprintf("lookup-pkg-%d", i),
			Version:     "1.0.0",
			ContentHash: fmt.Sprintf("hash-%d", i),
			SourcePath:  fmt.Sprintf("/src/pkg-%d", i),
			StorePath:   fmt.Sprintf("/store/pkg-%d", i),
			FilesCount:  10,
			TotalSize:   1024,
		}
		if err := env.Database.InsertPackage(pkg); err != nil {
			t.Fatalf("Failed to insert package: %v", err)
		}
	}

	// Lookup specific packages
	for i := 0; i < 10; i++ {
		name := fmt.Sprintf("lookup-pkg-%d", i)
		pkg, err := env.Database.GetPackageByName(name)
		if err != nil {
			t.Errorf("Failed to lookup %s: %v", name, err)
		}
		if pkg == nil {
			t.Errorf("Expected to find %s, got nil", name)
		}
		if pkg != nil && pkg.Name != name {
			t.Errorf("Expected name %s, got %s", name, pkg.Name)
		}
	}

	// Lookup non-existent package
	pkg, err := env.Database.GetPackageByName("nonexistent")
	if err != nil {
		t.Errorf("Unexpected error for nonexistent package: %v", err)
	}
	if pkg != nil {
		t.Error("Expected nil for nonexistent package")
	}
}

// TestDatabasePackageByHashLookup tests package lookup by hash
func TestDatabasePackageByHashLookup(t *testing.T) {
	t.Parallel()
	env := setupTest(t)

	// Insert package with specific hash
	pkg := &db.Package{
		Name:        "hash-pkg",
		Version:     "1.0.0",
		ContentHash: "specific-hash-12345",
		SourcePath:  "/src/hash-pkg",
		StorePath:   "/store/hash-pkg",
		FilesCount:  10,
		TotalSize:   1024,
	}
	if err := env.Database.InsertPackage(pkg); err != nil {
		t.Fatalf("Failed to insert package: %v", err)
	}

	// Lookup by hash
	found, err := env.Database.GetPackageByHash("hash-pkg", "specific-hash-12345")
	if err != nil {
		t.Fatalf("Failed to lookup by hash: %v", err)
	}
	if found == nil {
		t.Fatal("Expected to find package by hash")
	}
	if found.ContentHash != "specific-hash-12345" {
		t.Errorf("Expected hash specific-hash-12345, got %s", found.ContentHash)
	}
}

// TestDatabaseProjectByPathLookup tests project lookup by path
func TestDatabaseProjectByPathLookup(t *testing.T) {
	t.Parallel()
	env := setupTest(t)

	// Insert multiple projects
	for i := 0; i < 10; i++ {
		proj := &db.Project{
			Path:           fmt.Sprintf("/path/to/project-%d", i),
			Name:           fmt.Sprintf("project-%d", i),
			PackageManager: "npm",
		}
		if err := env.Database.InsertProject(proj); err != nil {
			t.Fatalf("Failed to insert project: %v", err)
		}
	}

	// Lookup specific projects
	for i := 0; i < 10; i++ {
		path := fmt.Sprintf("/path/to/project-%d", i)
		proj, err := env.Database.GetProjectByPath(path)
		if err != nil {
			t.Errorf("Failed to lookup %s: %v", path, err)
		}
		if proj == nil {
			t.Errorf("Expected to find project at %s, got nil", path)
		}
		if proj != nil && proj.Path != path {
			t.Errorf("Expected path %s, got %s", path, proj.Path)
		}
	}
}

// TestDatabaseFilesForPackage tests storing and retrieving file entries
func TestDatabaseFilesForPackage(t *testing.T) {
	t.Parallel()
	env := setupTest(t)

	// Insert a package
	pkg := &db.Package{
		Name:        "files-pkg",
		Version:     "1.0.0",
		ContentHash: "files-hash",
		SourcePath:  "/src/files-pkg",
		StorePath:   "/store/files-pkg",
		FilesCount:  100,
		TotalSize:   10240,
	}
	if err := env.Database.InsertPackage(pkg); err != nil {
		t.Fatalf("Failed to insert package: %v", err)
	}

	// Create file entries
	files := make([]*db.FileEntry, 100)
	for i := 0; i < 100; i++ {
		files[i] = &db.FileEntry{
			RelativePath: fmt.Sprintf("file-%d.js", i),
			ContentHash:  fmt.Sprintf("file-hash-%d", i),
			Size:         int64(1024 + i),
			Mode:         0644,
			ModTime:      time.Now().UnixNano(),
		}
	}

	// Insert files
	if err := env.Database.InsertFiles(pkg.ID, files); err != nil {
		t.Fatalf("Failed to insert files: %v", err)
	}

	// Retrieve files
	retrieved, err := env.Database.GetFilesForPackage(pkg.ID)
	if err != nil {
		t.Fatalf("Failed to get files: %v", err)
	}

	if len(retrieved) != 100 {
		t.Errorf("Expected 100 files, got %d", len(retrieved))
	}

	// Verify file contents
	for i, file := range retrieved {
		expectedPath := fmt.Sprintf("file-%d.js", i)
		if file.RelativePath != expectedPath {
			t.Errorf("Expected path %s, got %s", expectedPath, file.RelativePath)
		}
	}
}

// TestDatabaseGetProjectsForPackage tests getting all projects linked to a package
func TestDatabaseGetProjectsForPackage(t *testing.T) {
	t.Parallel()
	env := setupTest(t)

	// Insert a package
	pkg := &db.Package{
		Name:        "multi-project-pkg",
		Version:     "1.0.0",
		ContentHash: "hash",
		SourcePath:  "/src/pkg",
		StorePath:   "/store/pkg",
		FilesCount:  10,
		TotalSize:   1024,
	}
	if err := env.Database.InsertPackage(pkg); err != nil {
		t.Fatalf("Failed to insert package: %v", err)
	}

	// Create and link multiple projects
	projectCount := 5
	for i := 0; i < projectCount; i++ {
		proj := &db.Project{
			Path:           fmt.Sprintf("/project-%d", i),
			Name:           fmt.Sprintf("project-%d", i),
			PackageManager: "npm",
		}
		if err := env.Database.InsertProject(proj); err != nil {
			t.Fatalf("Failed to insert project: %v", err)
		}

		existingProj, _ := env.Database.GetProjectByPath(proj.Path)
		link := &db.Link{
			PackageID: pkg.ID,
			ProjectID: existingProj.ID,
			LinkType:  "reflink",
		}
		if err := env.Database.InsertLink(link); err != nil {
			t.Fatalf("Failed to insert link: %v", err)
		}
	}

	// Get all projects for package
	projects, err := env.Database.GetProjectsForPackage(pkg.ID)
	if err != nil {
		t.Fatalf("Failed to get projects: %v", err)
	}

	if len(projects) != projectCount {
		t.Errorf("Expected %d projects, got %d", projectCount, len(projects))
	}
}
