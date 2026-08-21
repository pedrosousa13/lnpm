package tests

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
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

// TestDatabasePackageUpdatePreservesLinks tests that publishing a new version of
// a package preserves the links to it.
//
// The link is asserted against whatever record the package name resolves to
// rather than against the record it was created on. Those were the same thing
// while a name had one record; now that a superseded version keeps its own
// record, the link is carried across to the version the tag names, and the name
// is how every command that reads links finds the package.
func TestDatabasePackageUpdatePreservesLinks(t *testing.T) {
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
	current, err := env.Database.GetPackageByName("update-pkg")
	if err != nil || current == nil {
		t.Fatalf("Failed to look up update-pkg after the update: %v", err)
	}
	links, err := env.Database.GetLinksForPackage(current.ID)
	if err != nil {
		t.Fatalf("Failed to get links: %v", err)
	}

	if len(links) != 1 {
		t.Errorf("Expected 1 link after update, got %d", len(links))
	}
	if links[0].ProjectID != existingProj.ID {
		t.Errorf("Expected the link to project %d, got %d", existingProj.ID, links[0].ProjectID)
	}
}

// TestDatabaseProjectUpdatePreservesLinks tests that updating a project preserves links
func TestDatabaseProjectUpdatePreservesLinks(t *testing.T) {
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
	env := setupTest(t)

	// Insert multiple projects
	for i := 0; i < 10; i++ {
		proj := &db.Project{
			Path:           filepath.FromSlash(fmt.Sprintf("/path/to/project-%d", i)),
			Name:           fmt.Sprintf("project-%d", i),
			PackageManager: "npm",
		}
		if err := env.Database.InsertProject(proj); err != nil {
			t.Fatalf("Failed to insert project: %v", err)
		}
	}

	// Lookup specific projects
	for i := 0; i < 10; i++ {
		path := filepath.FromSlash(fmt.Sprintf("/path/to/project-%d", i))
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

// --- Multiple versions of one package ----------------------------------------

// insertProject inserts a project and returns it with its assigned ID.
func insertProject(t *testing.T, d *db.DB, path string) *db.Project {
	t.Helper()

	if err := d.InsertProject(&db.Project{Path: path, Name: filepath.Base(path)}); err != nil {
		t.Fatalf("insert project %s: %v", path, err)
	}
	proj, err := d.GetProjectByPath(path)
	if err != nil || proj == nil {
		t.Fatalf("project %s not found after insert: %v", path, err)
	}
	return proj
}

// linkedProjects returns the paths of the projects linked to a package version,
// so a test can say which projects follow which version without unpacking link
// rows itself.
func linkedProjects(t *testing.T, d *db.DB, packageID int64) []string {
	t.Helper()

	projects, err := d.GetProjectsForPackage(packageID)
	if err != nil {
		t.Fatalf("projects for package %d: %v", packageID, err)
	}
	paths := make([]string, 0, len(projects))
	for _, proj := range projects {
		paths = append(paths, proj.Path)
	}
	sort.Strings(paths)
	return paths
}

// TestDatabaseVersionsOfOneNameCoexist pins the schema change tags rest on:
// publishing different content no longer displaces the record of what was
// published before, so two versions of a package can be addressed at once.
func TestDatabaseVersionsOfOneNameCoexist(t *testing.T) {
	env := setupTest(t)

	v1 := insertTagPkg(t, env.Database, "multi-pkg", "h1")
	v2 := insertTagPkg(t, env.Database, "multi-pkg", "h2")

	if v1.ID == v2.ID {
		t.Fatalf("the second version reused record %d instead of taking its own", v1.ID)
	}

	for _, hash := range []string{"h1", "h2"} {
		found, err := env.Database.GetPackageByHash("multi-pkg", hash)
		if err != nil {
			t.Fatalf("get multi-pkg by hash %s: %v", hash, err)
		}
		if found == nil {
			t.Errorf("version %s is no longer in the store", hash)
		}
	}

	current, err := env.Database.GetPackageByName("multi-pkg")
	if err != nil || current == nil {
		t.Fatalf("GetPackageByName = %v, %v", current, err)
	}
	if current.ContentHash != "h2" {
		t.Errorf("GetPackageByName resolves to %s, want the version published last", current.ContentHash)
	}
	assertTags(t, env.Database, "multi-pkg", map[string]string{db.DefaultTag: "h2"})
}

// TestDatabaseRepublishingTheSameContentUpdatesInPlace pins that a version is
// addressed by its content hash and not by the order it was published in.
// Publishing the same content twice describes one version, so it must not leave
// two records naming one store entry — gc removes an entry by path, and the
// second record would then name a directory that is no longer there.
func TestDatabaseRepublishingTheSameContentUpdatesInPlace(t *testing.T) {
	env := setupTest(t)

	first := insertTagPkg(t, env.Database, "same-pkg", "h1")
	second := insertTagPkg(t, env.Database, "same-pkg", "h1")

	if first.ID != second.ID {
		t.Errorf("republishing the same content took a new record (%d, then %d)", first.ID, second.ID)
	}

	packages, err := env.Database.ListPackages()
	if err != nil {
		t.Fatalf("list packages: %v", err)
	}
	count := 0
	for _, pkg := range packages {
		if pkg.Name == "same-pkg" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("the store holds %d records for same-pkg, want 1", count)
	}
}

// TestDatabaseInsertUnderAnotherTagKeepsTheDefaultVersion is the retention the
// whole feature rests on: publishing a beta must leave the version consumers are
// pinned to exactly where it was.
func TestDatabaseInsertUnderAnotherTagKeepsTheDefaultVersion(t *testing.T) {
	env := setupTest(t)

	insertTagPkg(t, env.Database, "beta-pkg", "h1")

	beta := &db.Package{
		Name:        "beta-pkg",
		Version:     "2.0.0-beta.1",
		ContentHash: "h2",
		StorePath:   "/store/beta-pkg/h2",
	}
	if err := env.Database.InsertPackageTagged(beta, "beta"); err != nil {
		t.Fatalf("publish under the beta tag: %v", err)
	}

	current, err := env.Database.GetPackageByName("beta-pkg")
	if err != nil || current == nil {
		t.Fatalf("GetPackageByName = %v, %v", current, err)
	}
	if current.ContentHash != "h1" {
		t.Errorf("publishing a beta moved the default version to %s", current.ContentHash)
	}
	assertTags(t, env.Database, "beta-pkg", map[string]string{db.DefaultTag: "h1", "beta": "h2"})

	if found, _ := env.Database.GetPackageByHash("beta-pkg", "h2"); found == nil {
		t.Error("the beta version was not recorded")
	}
}

// TestDatabaseFirstVersionUnderAnotherTagIsAlsoTheDefault pins that a package's
// first version is reachable by name whatever tag it was published under. Every
// command but a tag-aware add resolves through the name index, so a package
// published only as a beta would otherwise be in the store, on disk and
// invisible to push, remove, restore and status alike.
func TestDatabaseFirstVersionUnderAnotherTagIsAlsoTheDefault(t *testing.T) {
	env := setupTest(t)

	first := &db.Package{Name: "only-beta-pkg", Version: "0.1.0-beta.1", ContentHash: "h1"}
	if err := env.Database.InsertPackageTagged(first, "beta"); err != nil {
		t.Fatalf("publish under the beta tag: %v", err)
	}

	current, err := env.Database.GetPackageByName("only-beta-pkg")
	if err != nil || current == nil {
		t.Fatalf("GetPackageByName = %v, %v; want the only version there is", current, err)
	}
	assertTags(t, env.Database, "only-beta-pkg", map[string]string{db.DefaultTag: "h1", "beta": "h1"})
}

// TestDatabaseLinksFollowTheDefaultTag pins that a project keeps consuming the
// package it linked when a new version is published. Links are how push,
// publish --push, remove and status find a package's consumers, and all of them
// reach a package by name — so a link left behind on a superseded record would
// leave those projects unreachable.
func TestDatabaseLinksFollowTheDefaultTag(t *testing.T) {
	env := setupTest(t)

	v1 := insertTagPkg(t, env.Database, "follow-pkg", "h1")
	proj := insertProject(t, env.Database, filepath.FromSlash("/projects/follower"))
	if err := env.Database.InsertLink(&db.Link{PackageID: v1.ID, ProjectID: proj.ID, LinkType: "reflink"}); err != nil {
		t.Fatalf("insert link: %v", err)
	}

	v2 := insertTagPkg(t, env.Database, "follow-pkg", "h2")

	if got := linkedProjects(t, env.Database, v1.ID); len(got) != 0 {
		t.Errorf("the superseded version still has %v linked", got)
	}
	if got := linkedProjects(t, env.Database, v2.ID); len(got) != 1 || got[0] != proj.Path {
		t.Errorf("the new version has %v linked, want just %s", got, proj.Path)
	}
	if links, _ := env.Database.GetLinksForProject(proj.ID); len(links) != 1 {
		t.Errorf("the project holds %d links, want 1", len(links))
	}
}

// TestDatabaseLinksForAnotherTagStayWithTheirVersion pins that only the projects
// following the tag that moved are carried across. A project that asked for beta
// must not be dragged onto latest because latest happened to point at the same
// version at the time.
func TestDatabaseLinksForAnotherTagStayWithTheirVersion(t *testing.T) {
	env := setupTest(t)

	v1 := insertTagPkg(t, env.Database, "channel-pkg", "h1")
	if err := env.Database.SetTag("channel-pkg", "beta", "h1"); err != nil {
		t.Fatalf("set tag beta: %v", err)
	}

	stable := insertProject(t, env.Database, filepath.FromSlash("/projects/stable"))
	tester := insertProject(t, env.Database, filepath.FromSlash("/projects/tester"))
	if err := env.Database.InsertLink(&db.Link{PackageID: v1.ID, ProjectID: stable.ID, LinkType: "reflink"}); err != nil {
		t.Fatalf("insert the stable link: %v", err)
	}
	if err := env.Database.InsertLink(&db.Link{PackageID: v1.ID, ProjectID: tester.ID, LinkType: "reflink", Tag: "beta"}); err != nil {
		t.Fatalf("insert the beta link: %v", err)
	}

	v2 := insertTagPkg(t, env.Database, "channel-pkg", "h2")

	if got := linkedProjects(t, env.Database, v2.ID); len(got) != 1 || got[0] != stable.Path {
		t.Errorf("the new version has %v linked, want just %s", got, stable.Path)
	}
	if got := linkedProjects(t, env.Database, v1.ID); len(got) != 1 || got[0] != tester.Path {
		t.Errorf("the beta version has %v linked, want just %s", got, tester.Path)
	}
}

// TestDatabaseMovingATagMergesADuplicateLink pins that carrying links across
// does not leave a project holding two links to one package. Everything that
// reads links treats one row per project as given, and a second row would make
// remove and gc report a link that nothing can clear.
func TestDatabaseMovingATagMergesADuplicateLink(t *testing.T) {
	env := setupTest(t)

	v1 := insertTagPkg(t, env.Database, "merge-pkg", "h1")
	v2 := insertTagPkg(t, env.Database, "merge-pkg", "h2")
	proj := insertProject(t, env.Database, filepath.FromSlash("/projects/merger"))

	for _, id := range []int64{v1.ID, v2.ID} {
		if err := env.Database.InsertLink(&db.Link{PackageID: id, ProjectID: proj.ID, LinkType: "reflink"}); err != nil {
			t.Fatalf("insert link to record %d: %v", id, err)
		}
	}

	// Move the default tag back to the first version, carrying its links.
	if err := env.Database.SetTag("merge-pkg", db.DefaultTag, "h1"); err != nil {
		t.Fatalf("move the default tag: %v", err)
	}

	links, err := env.Database.GetLinksForProject(proj.ID)
	if err != nil {
		t.Fatalf("links for the project: %v", err)
	}
	if len(links) != 1 {
		t.Fatalf("the project holds %d links to merge-pkg, want 1", len(links))
	}
	if links[0].PackageID != v1.ID {
		t.Errorf("the surviving link names record %d, want the tagged version %d", links[0].PackageID, v1.ID)
	}
	if got := linkedProjects(t, env.Database, v2.ID); len(got) != 0 {
		t.Errorf("the untagged version still has %v linked", got)
	}
}

// TestDatabaseDeletingASupersededVersionKeepsTheNameLookup pins that collecting
// an old version leaves the current one reachable. gc deletes versions one by
// one, and a delete that cleared the name index would unpublish a package whose
// files are still in the store.
func TestDatabaseDeletingASupersededVersionKeepsTheNameLookup(t *testing.T) {
	env := setupTest(t)

	v1 := insertTagPkg(t, env.Database, "superseded-pkg", "h1")
	insertTagPkg(t, env.Database, "superseded-pkg", "h2")

	if err := env.Database.DeletePackage(v1.ID); err != nil {
		t.Fatalf("delete the superseded version: %v", err)
	}

	current, err := env.Database.GetPackageByName("superseded-pkg")
	if err != nil || current == nil {
		t.Fatalf("GetPackageByName = %v, %v; want the current version", current, err)
	}
	if current.ContentHash != "h2" {
		t.Errorf("GetPackageByName resolves to %s, want h2", current.ContentHash)
	}
	assertTags(t, env.Database, "superseded-pkg", map[string]string{db.DefaultTag: "h2"})
}

// TestDatabaseDeletingAVersionDropsOnlyItsOwnTags pins that removing a version
// takes the tags naming it and no others. A tag left pointing at a deleted
// version resolves to nothing, and would go on protecting nothing from gc.
func TestDatabaseDeletingAVersionDropsOnlyItsOwnTags(t *testing.T) {
	env := setupTest(t)

	v1 := insertTagPkg(t, env.Database, "tagdel-pkg", "h1")
	if err := env.Database.SetTag("tagdel-pkg", "beta", "h1"); err != nil {
		t.Fatalf("set tag beta: %v", err)
	}
	v2 := insertTagPkg(t, env.Database, "tagdel-pkg", "h2")

	if err := env.Database.DeletePackage(v2.ID); err != nil {
		t.Fatalf("delete the current version: %v", err)
	}

	assertTags(t, env.Database, "tagdel-pkg", map[string]string{"beta": "h1"})
	if pkg, _ := env.Database.GetPackageByName("tagdel-pkg"); pkg != nil {
		t.Errorf("the name index still resolves to %s after its version was deleted", pkg.ContentHash)
	}

	if err := env.Database.DeletePackage(v1.ID); err != nil {
		t.Fatalf("delete the beta version: %v", err)
	}
	assertTags(t, env.Database, "tagdel-pkg", map[string]string{})
}

// --- Tag operations ----------------------------------------------------------

// insertTagPkg inserts a package with the given name and content hash under the
// default tag and returns it, so the tag tests read as tag assertions rather
// than as struct literals.
func insertTagPkg(t *testing.T, d *db.DB, name, hash string) *db.Package {
	t.Helper()

	pkg := &db.Package{
		Name:        name,
		Version:     "1.0.0",
		ContentHash: hash,
		SourcePath:  "/src/" + name,
		StorePath:   "/store/" + name + "/" + hash,
		FilesCount:  1,
		TotalSize:   1,
	}
	if err := d.InsertPackage(pkg); err != nil {
		t.Fatalf("insert %s@%s: %v", name, hash, err)
	}
	return pkg
}

// assertTags checks the complete set of tags recorded for name. It compares the
// whole map rather than one entry, because a tag write that also disturbs a tag
// it was not asked about is exactly the failure worth catching.
func assertTags(t *testing.T, d *db.DB, name string, want map[string]string) {
	t.Helper()

	got, err := d.TagsForPackage(name)
	if err != nil {
		t.Fatalf("tags for %s: %v", name, err)
	}
	if len(got) != len(want) {
		t.Fatalf("tags for %s = %v, want %v", name, got, want)
	}
	for tag, hash := range want {
		if got[tag] != hash {
			t.Errorf("tag %s of %s points at %q, want %q", tag, name, got[tag], hash)
		}
	}
}

// TestDatabaseInsertPackageSetsTheLatestTag pins that publishing records the
// default tag, which is what every existing name-based lookup resolves through.
func TestDatabaseInsertPackageSetsTheLatestTag(t *testing.T) {
	env := setupTest(t)

	insertTagPkg(t, env.Database, "tagged-pkg", "h1")

	assertTags(t, env.Database, "tagged-pkg", map[string]string{db.DefaultTag: "h1"})

	resolved, err := env.Database.ResolveTag("tagged-pkg", db.DefaultTag)
	if err != nil {
		t.Fatalf("resolve latest: %v", err)
	}
	if resolved == nil || resolved.ContentHash != "h1" {
		t.Fatalf("ResolveTag(latest) = %v, want the h1 version", resolved)
	}
}

// TestDatabaseSetTagPointsAtAnExistingVersion covers setting a second tag on an
// already published version without republishing it.
func TestDatabaseSetTagPointsAtAnExistingVersion(t *testing.T) {
	env := setupTest(t)

	insertTagPkg(t, env.Database, "set-pkg", "h1")

	if err := env.Database.SetTag("set-pkg", "beta", "h1"); err != nil {
		t.Fatalf("set tag beta: %v", err)
	}

	assertTags(t, env.Database, "set-pkg", map[string]string{db.DefaultTag: "h1", "beta": "h1"})

	resolved, err := env.Database.ResolveTag("set-pkg", "beta")
	if err != nil {
		t.Fatalf("resolve beta: %v", err)
	}
	if resolved == nil || resolved.ContentHash != "h1" {
		t.Fatalf("ResolveTag(beta) = %v, want the h1 version", resolved)
	}
}

// TestDatabaseSetTagRejectsAnUnknownHash pins that a tag cannot be pointed at a
// version the store does not hold. A dangling tag would resolve to nothing and,
// once gc treats tags as reachability roots, would protect nothing either.
func TestDatabaseSetTagRejectsAnUnknownHash(t *testing.T) {
	env := setupTest(t)

	insertTagPkg(t, env.Database, "dangling-pkg", "h1")

	if err := env.Database.SetTag("dangling-pkg", "beta", "nosuchhash"); err == nil {
		t.Fatal("SetTag accepted a hash no version has")
	}
	assertTags(t, env.Database, "dangling-pkg", map[string]string{db.DefaultTag: "h1"})
}

// TestDatabaseDeleteTagRemovesOnlyThatTag pins that deleting one tag leaves the
// others, and the version itself, alone.
func TestDatabaseDeleteTagRemovesOnlyThatTag(t *testing.T) {
	env := setupTest(t)

	insertTagPkg(t, env.Database, "del-pkg", "h1")
	if err := env.Database.SetTag("del-pkg", "beta", "h1"); err != nil {
		t.Fatalf("set tag beta: %v", err)
	}

	if err := env.Database.DeleteTag("del-pkg", "beta"); err != nil {
		t.Fatalf("delete tag beta: %v", err)
	}

	assertTags(t, env.Database, "del-pkg", map[string]string{db.DefaultTag: "h1"})
	resolved, err := env.Database.ResolveTag("del-pkg", "beta")
	if err != nil {
		t.Fatalf("resolve beta: %v", err)
	}
	if resolved != nil {
		t.Errorf("ResolveTag(beta) = %v after deleting it, want nil", resolved)
	}
	if pkg, _ := env.Database.GetPackageByName("del-pkg"); pkg == nil {
		t.Error("deleting a tag removed the package from the name index")
	}
}

// TestDatabaseDeleteTagRefusesTheDefaultTag pins that the tag every name-based
// lookup resolves through cannot be deleted. Removing it would leave the package
// in the store and its files on disk while making it unreachable by name from
// every command lnpm has.
func TestDatabaseDeleteTagRefusesTheDefaultTag(t *testing.T) {
	env := setupTest(t)

	insertTagPkg(t, env.Database, "keep-latest-pkg", "h1")

	err := env.Database.DeleteTag("keep-latest-pkg", db.DefaultTag)
	if err == nil {
		t.Fatal("DeleteTag removed the default tag")
	}
	if !strings.Contains(err.Error(), db.DefaultTag) {
		t.Errorf("DeleteTag error = %v, want it to name the %s tag", err, db.DefaultTag)
	}
	assertTags(t, env.Database, "keep-latest-pkg", map[string]string{db.DefaultTag: "h1"})
	if pkg, _ := env.Database.GetPackageByName("keep-latest-pkg"); pkg == nil {
		t.Error("the package is no longer reachable by name")
	}
}

// TestDatabaseResolveUnknownTag pins that an unknown tag is not an error, so a
// caller can tell "no such tag" from "the lookup failed".
func TestDatabaseResolveUnknownTag(t *testing.T) {
	env := setupTest(t)

	insertTagPkg(t, env.Database, "unknown-tag-pkg", "h1")

	resolved, err := env.Database.ResolveTag("unknown-tag-pkg", "next")
	if err != nil {
		t.Fatalf("resolve an unset tag: %v", err)
	}
	if resolved != nil {
		t.Errorf("ResolveTag(next) = %v, want nil", resolved)
	}
}

// TestDeletePackageRemovesLinks verifies DeletePackage also removes link rows
// and scrubs the link indexes, leaving no dangling references (#44).
func TestDeletePackageRemovesLinks(t *testing.T) {
	env := setupTest(t)
	d := env.Database

	if err := d.InsertPackage(&db.Package{Name: "dp-pkg", Version: "1.0.0", ContentHash: "h1", StorePath: "/s/dp"}); err != nil {
		t.Fatalf("insert package: %v", err)
	}
	if err := d.InsertProject(&db.Project{Path: "/proj/dp-a", Name: "dp-a"}); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	pkg, _ := d.GetPackageByName("dp-pkg")
	proj, _ := d.GetProjectByPath("/proj/dp-a")
	if pkg == nil || proj == nil {
		t.Fatal("setup: package or project not found after insert")
	}
	if err := d.InsertLink(&db.Link{PackageID: pkg.ID, ProjectID: proj.ID, LinkType: "hardlink"}); err != nil {
		t.Fatalf("insert link: %v", err)
	}

	// Sanity: link is present before delete.
	if links, _ := d.GetLinksForProject(proj.ID); len(links) != 1 {
		t.Fatalf("setup: expected 1 link, got %d", len(links))
	}

	if err := d.DeletePackage(pkg.ID); err != nil {
		t.Fatalf("delete package: %v", err)
	}

	if links, _ := d.GetLinksForProject(proj.ID); len(links) != 0 {
		t.Errorf("expected 0 links for project after DeletePackage, got %d (dangling link rows)", len(links))
	}
	if links, _ := d.GetLinksForPackage(pkg.ID); len(links) != 0 {
		t.Errorf("expected 0 links for package after DeletePackage, got %d", len(links))
	}
	if projs, _ := d.GetProjectsForPackage(pkg.ID); len(projs) != 0 {
		t.Errorf("expected 0 projects for package after DeletePackage, got %d", len(projs))
	}
}
