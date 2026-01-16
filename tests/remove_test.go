package tests

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/cli"
	"github.com/pedrosousa13/lnpm/pkg/lockfile"
)

// TestRemoveSinglePackage tests removing a single package
func TestRemoveSinglePackage(t *testing.T) {
	t.Parallel()
	env := setupTest(t)

	// Create and publish a test package
	pkgDir := env.CreateTestPackage("remove-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'remove';",
	})
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPublish(false, "", false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Create a project and add the package
	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}

	if err := cli.RunAdd("remove-pkg", false, false); err != nil {
		t.Fatalf("Failed to add package: %v", err)
	}

	// Verify package was added
	env.AssertSymlinkExists(projectDir, "remove-pkg")
	env.AssertDatabaseLink("remove-pkg", projectDir)

	// Remove the package
	if err := cli.RunRemove("remove-pkg", false); err != nil {
		t.Fatalf("Failed to remove package: %v", err)
	}

	// Verify package was removed
	env.AssertSymlinkMissing(projectDir, "remove-pkg")
	env.AssertDatabaseNoLink("remove-pkg", projectDir)
	env.AssertPackageJSONMissing(projectDir, "remove-pkg")

	// Verify lockfile was updated (should be deleted since no packages remain)
	env.AssertLockfileExists(projectDir, false)
}

// TestRemoveAllPackages tests removing all packages with --all flag
func TestRemoveAllPackages(t *testing.T) {
	t.Parallel()
	env := setupTest(t)

	// Create and publish multiple packages
	packages := []string{"pkg-a", "pkg-b", "pkg-c"}
	for _, name := range packages {
		pkgDir := env.CreateTestPackage(name, "1.0.0", map[string]string{
			"index.js": "module.exports = '" + name + "';",
		})
		if err := os.Chdir(pkgDir); err != nil {
			t.Fatalf("Failed to chdir: %v", err)
		}
		if err := cli.RunPublish(false, "", false); err != nil {
			t.Fatalf("Failed to publish %s: %v", name, err)
		}
	}

	// Create a project and add all packages
	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}

	for _, name := range packages {
		if err := cli.RunAdd(name, false, false); err != nil {
			t.Fatalf("Failed to add %s: %v", name, err)
		}
	}

	// Remove all packages
	if err := cli.RunRemove("", true); err != nil {
		t.Fatalf("Failed to remove all packages: %v", err)
	}

	// Verify all packages were removed
	for _, name := range packages {
		env.AssertSymlinkMissing(projectDir, name)
		env.AssertDatabaseNoLink(name, projectDir)
		env.AssertPackageJSONMissing(projectDir, name)
	}

	// Verify lockfile was deleted
	env.AssertLockfileExists(projectDir, false)
}

// TestRemoveRestoresOriginalVersion tests that original version is restored
func TestRemoveRestoresOriginalVersion(t *testing.T) {
	t.Parallel()
	env := setupTest(t)

	// Create project with original dependency
	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	pkgJSONPath := filepath.Join(projectDir, "package.json")

	// Add original dependency
	originalContent := `{
  "name": "test-project",
  "version": "1.0.0",
  "dependencies": {
    "remove-pkg": "^1.0.0"
  }
}`
	if err := os.WriteFile(pkgJSONPath, []byte(originalContent), 0644); err != nil {
		t.Fatalf("Failed to write package.json: %v", err)
	}

	// Create and publish a test package
	pkgDir := env.CreateTestPackage("remove-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'remove';",
	})
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPublish(false, "", false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Add package (should replace ^1.0.0 with file: reference)
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunAdd("remove-pkg", false, false); err != nil {
		t.Fatalf("Failed to add package: %v", err)
	}

	// Verify file: reference was added
	env.AssertPackageJSON(projectDir, "remove-pkg", "file:.lnpm/remove-pkg")

	// Remove the package
	if err := cli.RunRemove("remove-pkg", false); err != nil {
		t.Fatalf("Failed to remove package: %v", err)
	}

	// Verify original version was restored
	env.AssertPackageJSON(projectDir, "remove-pkg", "^1.0.0")
}

// TestRemovePackageNotLinked tests removing a package that isn't linked
func TestRemovePackageNotLinked(t *testing.T) {
	t.Parallel()
	env := setupTest(t)

	// Create a project without any links
	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}

	// Try to remove non-existent link
	err := cli.RunRemove("nonexistent-pkg", false)
	if err == nil {
		t.Fatal("Expected error when removing non-linked package, got nil")
	}
}

// TestRemoveNoOriginalVersion tests removing when no original version recorded
func TestRemoveNoOriginalVersion(t *testing.T) {
	t.Parallel()
	env := setupTest(t)

	// Create and publish a test package
	pkgDir := env.CreateTestPackage("pure-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'pure';",
	})
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPublish(false, "", false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Create a project and add with --pure flag (no package.json update)
	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}

	if err := cli.RunAdd("pure-pkg", false, true); err != nil {
		t.Fatalf("Failed to add package: %v", err)
	}

	// Verify package was NOT added to package.json
	env.AssertPackageJSONMissing(projectDir, "pure-pkg")

	// Remove the package
	if err := cli.RunRemove("pure-pkg", false); err != nil {
		t.Fatalf("Failed to remove package: %v", err)
	}

	// Verify package is still not in package.json
	env.AssertPackageJSONMissing(projectDir, "pure-pkg")
}

// TestRemoveMultipleKeepOne tests removing one package while keeping others
func TestRemoveMultipleKeepOne(t *testing.T) {
	t.Parallel()
	env := setupTest(t)

	// Create and publish multiple packages
	packages := []string{"pkg-a", "pkg-b", "pkg-c"}
	for _, name := range packages {
		pkgDir := env.CreateTestPackage(name, "1.0.0", map[string]string{
			"index.js": "module.exports = '" + name + "';",
		})
		if err := os.Chdir(pkgDir); err != nil {
			t.Fatalf("Failed to chdir: %v", err)
		}
		if err := cli.RunPublish(false, "", false); err != nil {
			t.Fatalf("Failed to publish %s: %v", name, err)
		}
	}

	// Create a project and add all packages
	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}

	for _, name := range packages {
		if err := cli.RunAdd(name, false, false); err != nil {
			t.Fatalf("Failed to add %s: %v", name, err)
		}
	}

	// Remove only pkg-b
	if err := cli.RunRemove("pkg-b", false); err != nil {
		t.Fatalf("Failed to remove pkg-b: %v", err)
	}

	// Verify pkg-b was removed
	env.AssertSymlinkMissing(projectDir, "pkg-b")
	env.AssertDatabaseNoLink("pkg-b", projectDir)

	// Verify pkg-a and pkg-c are still linked
	env.AssertSymlinkExists(projectDir, "pkg-a")
	env.AssertSymlinkExists(projectDir, "pkg-c")
	env.AssertDatabaseLink("pkg-a", projectDir)
	env.AssertDatabaseLink("pkg-c", projectDir)

	// Lockfile should still exist with remaining packages
	env.AssertLockfileExists(projectDir, true)
	env.AssertLockfile(projectDir, func(lock *lockfile.LockFile) {
		if lock.Has("pkg-b") {
			t.Error("Expected pkg-b to be removed from lockfile")
		}
		if !lock.Has("pkg-a") || !lock.Has("pkg-c") {
			t.Error("Expected pkg-a and pkg-c to remain in lockfile")
		}
	})
}

// TestRemoveScopedPackage tests removing a scoped package
func TestRemoveScopedPackage(t *testing.T) {
	t.Parallel()
	env := setupTest(t)

	// Create and publish a scoped package
	pkgDir := env.CreateTestPackage("@org/scoped-remove", "1.0.0", map[string]string{
		"index.js": "module.exports = 'scoped';",
	})
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPublish(false, "", false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Create a project and add the scoped package
	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}

	if err := cli.RunAdd("@org/scoped-remove", false, false); err != nil {
		t.Fatalf("Failed to add package: %v", err)
	}

	// Verify scoped package was added
	env.AssertSymlinkExists(projectDir, "@org/scoped-remove")

	// Remove the scoped package
	if err := cli.RunRemove("@org/scoped-remove", false); err != nil {
		t.Fatalf("Failed to remove package: %v", err)
	}

	// Verify scoped package was removed
	env.AssertSymlinkMissing(projectDir, "@org/scoped-remove")
	env.AssertDatabaseNoLink("@org/scoped-remove", projectDir)
	env.AssertPackageJSONMissing(projectDir, "@org/scoped-remove")
}

// TestRemoveAllNoPackages tests --all flag when no packages are linked
func TestRemoveAllNoPackages(t *testing.T) {
	t.Parallel()
	env := setupTest(t)

	// Create a project without any links
	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}

	// Try to remove all (should be noop)
	if err := cli.RunRemove("", true); err != nil {
		t.Fatalf("Remove --all with no packages failed: %v", err)
	}

	// Project should remain unchanged
	env.AssertLockfileExists(projectDir, false)
}

// TestRemoveDevDependency tests removing a dev dependency
func TestRemoveDevDependency(t *testing.T) {
	t.Parallel()
	env := setupTest(t)

	// Create and publish a test package
	pkgDir := env.CreateTestPackage("dev-remove-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'dev';",
	})
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPublish(false, "", false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Create a project and add as dev dependency
	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}

	if err := cli.RunAdd("dev-remove-pkg", true, false); err != nil {
		t.Fatalf("Failed to add dev package: %v", err)
	}

	// Remove the package
	if err := cli.RunRemove("dev-remove-pkg", false); err != nil {
		t.Fatalf("Failed to remove package: %v", err)
	}

	// Verify package was removed
	env.AssertSymlinkMissing(projectDir, "dev-remove-pkg")
	env.AssertPackageJSONMissing(projectDir, "dev-remove-pkg")
}

// TestRemoveLockfileDeleted tests that lockfile is deleted when empty
func TestRemoveLockfileDeleted(t *testing.T) {
	t.Parallel()
	env := setupTest(t)

	// Create and publish a single package
	pkgDir := env.CreateTestPackage("only-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'only';",
	})
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPublish(false, "", false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Create a project and add the package
	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}

	if err := cli.RunAdd("only-pkg", false, false); err != nil {
		t.Fatalf("Failed to add package: %v", err)
	}

	// Verify lockfile exists
	env.AssertLockfileExists(projectDir, true)

	// Remove the only package
	if err := cli.RunRemove("only-pkg", false); err != nil {
		t.Fatalf("Failed to remove package: %v", err)
	}

	// Verify lockfile was deleted
	env.AssertLockfileExists(projectDir, false)
}

// TestRemoveLockfileUpdated tests that lockfile is updated when not empty
func TestRemoveLockfileUpdated(t *testing.T) {
	t.Parallel()
	env := setupTest(t)

	// Create and publish two packages
	pkg1Dir := env.CreateTestPackage("pkg-1", "1.0.0", map[string]string{
		"index.js": "module.exports = '1';",
	})
	if err := os.Chdir(pkg1Dir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPublish(false, "", false); err != nil {
		t.Fatalf("Failed to publish pkg-1: %v", err)
	}

	pkg2Dir := env.CreateTestPackage("pkg-2", "1.0.0", map[string]string{
		"index.js": "module.exports = '2';",
	})
	if err := os.Chdir(pkg2Dir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPublish(false, "", false); err != nil {
		t.Fatalf("Failed to publish pkg-2: %v", err)
	}

	// Create a project and add both packages
	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}

	if err := cli.RunAdd("pkg-1", false, false); err != nil {
		t.Fatalf("Failed to add pkg-1: %v", err)
	}
	if err := cli.RunAdd("pkg-2", false, false); err != nil {
		t.Fatalf("Failed to add pkg-2: %v", err)
	}

	// Remove pkg-1
	if err := cli.RunRemove("pkg-1", false); err != nil {
		t.Fatalf("Failed to remove pkg-1: %v", err)
	}

	// Verify lockfile still exists and contains pkg-2
	env.AssertLockfileExists(projectDir, true)
	env.AssertLockfile(projectDir, func(lock *lockfile.LockFile) {
		if lock.Has("pkg-1") {
			t.Error("Expected pkg-1 to be removed from lockfile")
		}
		if !lock.Has("pkg-2") {
			t.Error("Expected pkg-2 to remain in lockfile")
		}
	})
}
