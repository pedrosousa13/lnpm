package tests

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/cli"
)

// TestGCOrphanedPackages tests garbage collection of orphaned packages
func TestGCOrphanedPackages(t *testing.T) {
	env := setupTest(t)

	// Create and publish a package
	pkgDir := env.CreateTestPackage("gc-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'test';",
	})
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPublish(false, "", false, false, false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Create project and add package
	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunAdd("gc-pkg", false, false, false); err != nil {
		t.Fatalf("Failed to add package: %v", err)
	}

	// Remove the package
	if err := cli.RunRemove("gc-pkg", false); err != nil {
		t.Fatalf("Failed to remove package: %v", err)
	}

	// Package is now orphaned
	env.AssertPackageInDatabase("gc-pkg", true)

	// Run GC
	if err := cli.RunGC(false, "", false); err != nil {
		t.Fatalf("Failed to run GC: %v", err)
	}

	// Package should be removed
	env.AssertPackageInDatabase("gc-pkg", false)
}

// TestGCDryRun tests garbage collection in dry-run mode
func TestGCDryRun(t *testing.T) {
	env := setupTest(t)

	// Create and publish a package
	pkgDir := env.CreateTestPackage("dryrun-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'test';",
	})
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPublish(false, "", false, false, false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Package is orphaned (no projects linked)
	env.AssertPackageInDatabase("dryrun-pkg", true)

	// Run GC in dry-run mode
	if err := cli.RunGC(true, "", false); err != nil {
		t.Fatalf("Failed to run GC dry-run: %v", err)
	}

	// Package should still exist
	env.AssertPackageInDatabase("dryrun-pkg", true)
}

// TestGCWithAge tests GC with age filter
func TestGCWithAge(t *testing.T) {
	env := setupTest(t)

	// Create and publish two packages
	pkg1Dir := env.CreateTestPackage("old-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'old';",
	})
	if err := os.Chdir(pkg1Dir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPublish(false, "", false, false, false); err != nil {
		t.Fatalf("Failed to publish old-pkg: %v", err)
	}

	pkg2Dir := env.CreateTestPackage("new-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'new';",
	})
	if err := os.Chdir(pkg2Dir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPublish(false, "", false, false, false); err != nil {
		t.Fatalf("Failed to publish new-pkg: %v", err)
	}

	// Both packages are orphaned but "new"
	// Run GC with age filter (30 days) - nothing should be deleted
	if err := cli.RunGC(false, "30d", false); err != nil {
		t.Fatalf("Failed to run GC with age: %v", err)
	}

	// Both packages should still exist
	env.AssertPackageInDatabase("old-pkg", true)
	env.AssertPackageInDatabase("new-pkg", true)
}

// TestGCOrphanedLinks tests cleaning up orphaned links
func TestGCOrphanedLinks(t *testing.T) {
	env := setupTest(t)

	// Create and publish a package
	pkgDir := env.CreateTestPackage("link-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'test';",
	})
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPublish(false, "", false, false, false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Create project and add package
	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunAdd("link-pkg", false, false, false); err != nil {
		t.Fatalf("Failed to add package: %v", err)
	}

	// Move out of projectDir before deleting (Windows can't remove cwd)
	if err := os.Chdir(env.TempDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	// Remove junction/symlink first (Windows blocks RemoveAll on dirs with junctions)
	_ = os.Remove(filepath.Join(projectDir, "node_modules", "link-pkg"))
	// Delete the project directory (simulating removed project)
	if err := os.RemoveAll(projectDir); err != nil {
		t.Fatalf("Failed to remove project dir: %v", err)
	}

	// Link should be orphaned
	env.AssertDatabaseLink("link-pkg", projectDir)

	// Run GC with fixLinks
	if err := cli.RunGC(false, "", true); err != nil {
		t.Fatalf("Failed to run GC: %v", err)
	}

	// Orphaned link should be cleaned
	env.AssertDatabaseNoLink("link-pkg", projectDir)
}

// TestGCLinkedPackages tests that GC doesn't remove linked packages
func TestGCLinkedPackages(t *testing.T) {
	env := setupTest(t)

	// Create and publish a package
	pkgDir := env.CreateTestPackage("linked-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'test';",
	})
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPublish(false, "", false, false, false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Create project and add package
	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunAdd("linked-pkg", false, false, false); err != nil {
		t.Fatalf("Failed to add package: %v", err)
	}

	// Package has valid link
	env.AssertDatabaseLink("linked-pkg", projectDir)

	// Run GC
	if err := cli.RunGC(false, "", false); err != nil {
		t.Fatalf("Failed to run GC: %v", err)
	}

	// Package should still exist
	env.AssertPackageInDatabase("linked-pkg", true)
	env.AssertDatabaseLink("linked-pkg", projectDir)
}

// TestGCMultipleOrphanedPackages tests GC with multiple orphaned packages
func TestGCMultipleOrphanedPackages(t *testing.T) {
	env := setupTest(t)

	// Create and publish multiple packages
	packages := []string{"orphan-a", "orphan-b", "orphan-c"}
	for _, name := range packages {
		pkgDir := env.CreateTestPackage(name, "1.0.0", map[string]string{
			"index.js": "module.exports = '" + name + "';",
		})
		if err := os.Chdir(pkgDir); err != nil {
			t.Fatalf("Failed to chdir to %s: %v", name, err)
		}
		if err := cli.RunPublish(false, "", false, false, false); err != nil {
			t.Fatalf("Failed to publish %s: %v", name, err)
		}
	}

	// All packages are orphaned
	for _, name := range packages {
		env.AssertPackageInDatabase(name, true)
	}

	// Run GC
	if err := cli.RunGC(false, "", false); err != nil {
		t.Fatalf("Failed to run GC: %v", err)
	}

	// All packages should be removed
	for _, name := range packages {
		env.AssertPackageInDatabase(name, false)
	}
}

// TestGCMixedPackages tests GC with both linked and orphaned packages
func TestGCMixedPackages(t *testing.T) {
	env := setupTest(t)

	// Create orphaned package
	orphanDir := env.CreateTestPackage("orphan-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'orphan';",
	})
	if err := os.Chdir(orphanDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPublish(false, "", false, false, false); err != nil {
		t.Fatalf("Failed to publish orphan: %v", err)
	}

	// Create linked package
	linkedDir := env.CreateTestPackage("linked-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'linked';",
	})
	if err := os.Chdir(linkedDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPublish(false, "", false, false, false); err != nil {
		t.Fatalf("Failed to publish linked: %v", err)
	}

	// Link one package to a project
	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunAdd("linked-pkg", false, false, false); err != nil {
		t.Fatalf("Failed to add linked package: %v", err)
	}

	// Run GC
	if err := cli.RunGC(false, "", false); err != nil {
		t.Fatalf("Failed to run GC: %v", err)
	}

	// Orphaned should be removed, linked should remain
	env.AssertPackageInDatabase("orphan-pkg", false)
	env.AssertPackageInDatabase("linked-pkg", true)
}

// TestGCNoPackages tests GC when no packages exist
func TestGCNoPackages(t *testing.T) {
	_ = setupTest(t)

	// Run GC with empty database
	if err := cli.RunGC(false, "", false); err != nil {
		t.Fatalf("Failed to run GC: %v", err)
	}
}

// TestGCStorePathCleanup tests that store paths are cleaned up
func TestGCStorePathCleanup(t *testing.T) {
	env := setupTest(t)

	// Create and publish a package
	pkgDir := env.CreateTestPackage("store-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'test';",
		"lib/utils.js": "exports.util = () => 'util';",
	})
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPublish(false, "", false, false, false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Get package info to find store path
	pkg, err := env.Database.GetPackageByName("store-pkg")
	if err != nil || pkg == nil {
		t.Fatalf("Failed to get package: %v", err)
	}
	storePath := pkg.StorePath

	// Verify store path exists
	if _, err := os.Stat(storePath); err != nil {
		t.Fatalf("Store path doesn't exist: %v", err)
	}

	// Package is orphaned
	// Run GC
	if err := cli.RunGC(false, "", false); err != nil {
		t.Fatalf("Failed to run GC: %v", err)
	}

	// Store path should be cleaned up
	if _, err := os.Stat(storePath); !os.IsNotExist(err) {
		t.Errorf("Store path still exists after GC")
	}
}

// TestGCPartiallyOrphanedLinks tests GC with some valid and some orphaned links
func TestGCPartiallyOrphanedLinks(t *testing.T) {
	env := setupTest(t)

	// Create and publish a package
	pkgDir := env.CreateTestPackage("partial-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'test';",
	})
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPublish(false, "", false, false, false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Create two projects and link to both
	project1Dir := env.CreateTestPackage("project-1", "1.0.0", nil)
	if err := os.Chdir(project1Dir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunAdd("partial-pkg", false, false, false); err != nil {
		t.Fatalf("Failed to add to project-1: %v", err)
	}

	project2Dir := env.CreateTestPackage("project-2", "1.0.0", nil)
	if err := os.Chdir(project2Dir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunAdd("partial-pkg", false, false, false); err != nil {
		t.Fatalf("Failed to add to project-2: %v", err)
	}

	// Delete project-1 directory
	if err := os.RemoveAll(project1Dir); err != nil {
		t.Fatalf("Failed to remove project-1: %v", err)
	}

	// Run GC with fixLinks
	if err := cli.RunGC(false, "", true); err != nil {
		t.Fatalf("Failed to run GC: %v", err)
	}

	// Package should still exist (project-2 still linked)
	env.AssertPackageInDatabase("partial-pkg", true)
	// Project-1 link should be removed
	env.AssertDatabaseNoLink("partial-pkg", project1Dir)
	// Project-2 link should remain
	env.AssertDatabaseLink("partial-pkg", project2Dir)
}

// TestGCInvalidDuration tests GC with invalid duration string
func TestGCInvalidDuration(t *testing.T) {
	_ = setupTest(t)

	// Run GC with invalid duration
	err := cli.RunGC(false, "invalid", false)
	if err == nil {
		t.Fatal("Expected error with invalid duration, got nil")
	}
}

// TestGCDurationFormats tests GC with various duration formats
func TestGCDurationFormats(t *testing.T) {
	env := setupTest(t)

	// Create orphaned package
	pkgDir := env.CreateTestPackage("duration-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'test';",
	})
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPublish(false, "", false, false, false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Test various duration formats
	durations := []string{"24h", "7d", "1w"}
	for _, dur := range durations {
		if err := cli.RunGC(true, dur, false); err != nil {
			t.Errorf("GC failed with duration %s: %v", dur, err)
		}
	}
}
