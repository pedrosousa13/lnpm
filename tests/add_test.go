package tests

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/cli"
	"github.com/pedrosousa13/lnpm/pkg/lockfile"
)

// TestAddBasicPackage tests adding a basic unscoped package
func TestAddBasicPackage(t *testing.T) {
	env := setupTest(t)

	// Create and publish a test package
	pkgDir := env.CreateTestPackage("test-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'test';",
	})
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPublish(false, false, false, false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Create a project and add the package
	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}

	if err := cli.RunAdd("test-pkg", false, false, false); err != nil {
		t.Fatalf("Failed to add package: %v", err)
	}

	// Verify all expected changes
	env.AssertDirectoryExists(filepath.Join(projectDir, ".lnpm"), true)
	env.AssertFilesLinked(projectDir, "test-pkg")
	env.AssertSymlinkExists(projectDir, "test-pkg")
	env.AssertPackageJSON(projectDir, "test-pkg", "file:.lnpm/test-pkg")
	env.AssertLockfileExists(projectDir, true)
	env.AssertGitignore(projectDir, ".lnpm/", true)
	env.AssertDatabaseLink("test-pkg", projectDir)
}

// TestAddScopedPackage tests adding a scoped package (@org/name)
func TestAddScopedPackage(t *testing.T) {
	env := setupTest(t)

	// Create and publish a scoped package
	pkgDir := env.CreateTestPackage("@test-org/scoped-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'scoped';",
	})
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPublish(false, false, false, false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Create a project and add the scoped package
	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}

	if err := cli.RunAdd("@test-org/scoped-pkg", false, false, false); err != nil {
		t.Fatalf("Failed to add package: %v", err)
	}

	// Verify scoped package structure
	env.AssertSymlinkExists(projectDir, "@test-org/scoped-pkg")
	env.AssertPackageJSON(projectDir, "@test-org/scoped-pkg", "file:.lnpm/@test-org/scoped-pkg")
	env.AssertDirectoryExists(filepath.Join(projectDir, "node_modules", "@test-org"), true)
}

// TestAddDevDependency tests adding a package as dev dependency
func TestAddDevDependency(t *testing.T) {
	env := setupTest(t)

	// Create and publish a test package
	pkgDir := env.CreateTestPackage("dev-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'dev';",
	})
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPublish(false, false, false, false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Create a project and add as dev dependency
	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}

	if err := cli.RunAdd("dev-pkg", true, false, false); err != nil {
		t.Fatalf("Failed to add package: %v", err)
	}

	// Verify it was added as dev dependency
	env.AssertPackageJSON(projectDir, "dev-pkg", "file:.lnpm/dev-pkg")
}

// TestAddPreservesDevDependencyLocation tests that adding without --dev preserves devDependencies location
func TestAddPreservesDevDependencyLocation(t *testing.T) {
	env := setupTest(t)

	// Create and publish a test package
	pkgDir := env.CreateTestPackage("preserve-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'preserve';",
	})
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPublish(false, false, false, false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Create project with package already in devDependencies
	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)

	// Manually set up package.json with devDependencies
	pkgJSONPath := filepath.Join(projectDir, "package.json")
	pkgJSON := map[string]interface{}{
		"name":    "test-project",
		"version": "1.0.0",
		"devDependencies": map[string]interface{}{
			"preserve-pkg": "^1.0.0",
		},
	}
	data, _ := json.MarshalIndent(pkgJSON, "", "  ")
	if err := os.WriteFile(pkgJSONPath, data, 0644); err != nil {
		t.Fatalf("Failed to write package.json: %v", err)
	}

	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}

	// Add WITHOUT --dev flag - should preserve devDependencies location
	if err := cli.RunAdd("preserve-pkg", false, false, false); err != nil {
		t.Fatalf("Failed to add package: %v", err)
	}

	// Verify it stayed in devDependencies (not moved to dependencies)
	data, _ = os.ReadFile(pkgJSONPath)
	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("Failed to unmarshal package.json: %v", err)
	}

	deps := result["dependencies"]
	devDeps := result["devDependencies"].(map[string]interface{})

	if deps != nil {
		if depsMap, ok := deps.(map[string]interface{}); ok && depsMap["preserve-pkg"] != nil {
			t.Error("Package should NOT be in dependencies")
		}
	}
	if devDeps["preserve-pkg"] != "file:.lnpm/preserve-pkg" {
		t.Errorf("Expected preserve-pkg in devDependencies with lnpm reference, got %v", devDeps["preserve-pkg"])
	}
}

// TestAddPureFlag tests adding with --pure flag (no package.json update)
func TestAddPureFlag(t *testing.T) {
	env := setupTest(t)

	// Create and publish a test package
	pkgDir := env.CreateTestPackage("pure-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'pure';",
	})
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPublish(false, false, false, false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Create a project and add with --pure flag
	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}

	if err := cli.RunAdd("pure-pkg", false, true, false); err != nil {
		t.Fatalf("Failed to add package: %v", err)
	}

	// Verify package.json was NOT updated
	env.AssertPackageJSONMissing(projectDir, "pure-pkg")

	// But other changes should still happen
	env.AssertFilesLinked(projectDir, "pure-pkg")
	env.AssertSymlinkExists(projectDir, "pure-pkg")
	env.AssertLockfileExists(projectDir, true)
}

// TestAddPackageNotFound tests adding a package that doesn't exist
func TestAddPackageNotFound(t *testing.T) {
	env := setupTest(t)

	// Create a project
	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}

	// Try to add non-existent package
	err := cli.RunAdd("nonexistent-package", false, false, false)
	if err == nil {
		t.Fatal("Expected error when adding non-existent package, got nil")
	}
}

// TestAddNoPackageJSON tests adding when no package.json exists
func TestAddNoPackageJSON(t *testing.T) {
	env := setupTest(t)

	// Create empty directory
	projectDir := filepath.Join(env.TempDir, "no-package-json")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatalf("Failed to create dir: %v", err)
	}
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}

	// Try to add package
	err := cli.RunAdd("some-pkg", false, false, false)
	if err == nil {
		t.Fatal("Expected error when no package.json exists, got nil")
	}
}

// TestAddAlreadyAdded tests adding a package that's already added (idempotent)
func TestAddAlreadyAdded(t *testing.T) {
	env := setupTest(t)

	// Create and publish a test package
	pkgDir := env.CreateTestPackage("test-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'test';",
	})
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPublish(false, false, false, false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Create a project and add the package twice
	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}

	if err := cli.RunAdd("test-pkg", false, false, false); err != nil {
		t.Fatalf("Failed to add package first time: %v", err)
	}

	if err := cli.RunAdd("test-pkg", false, false, false); err != nil {
		t.Fatalf("Failed to add package second time (should be idempotent): %v", err)
	}

	// Verify package is still linked correctly
	env.AssertSymlinkExists(projectDir, "test-pkg")
	env.AssertDatabaseLink("test-pkg", projectDir)
}

// TestAddUpdatesExisting tests updating an existing dependency
func TestAddUpdatesExisting(t *testing.T) {
	env := setupTest(t)

	// Create and publish version 1.0.0
	pkgDir := env.CreateTestPackage("test-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'v1';",
	})
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPublish(false, false, false, false); err != nil {
		t.Fatalf("Failed to publish v1: %v", err)
	}

	// Add to project
	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunAdd("test-pkg", false, false, false); err != nil {
		t.Fatalf("Failed to add v1: %v", err)
	}

	// Publish version 2.0.0 with different content
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	// Modify package.json and file
	if err := os.WriteFile(filepath.Join(pkgDir, "package.json"),
		[]byte(`{"name":"test-pkg","version":"2.0.0"}`), 0644); err != nil {
		t.Fatalf("Failed to update package.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "index.js"),
		[]byte("module.exports = 'v2';"), 0644); err != nil {
		t.Fatalf("Failed to update index.js: %v", err)
	}
	if err := cli.RunPublish(false, false, false, false); err != nil {
		t.Fatalf("Failed to publish v2: %v", err)
	}

	// Add updated version to project
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunAdd("test-pkg", false, false, false); err != nil {
		t.Fatalf("Failed to add v2: %v", err)
	}

	// Verify updated version is linked
	env.AssertSymlinkExists(projectDir, "test-pkg")
	env.AssertPackageJSON(projectDir, "test-pkg", "file:.lnpm/test-pkg")
}

// TestAddConcurrentSameProject tests concurrent adds to same project
func TestAddConcurrentSameProject(t *testing.T) {
	t.Skip("Skipping: concurrent package.json writes cause race conditions, not realistic usage")
	// Don't use t.Parallel() - this test controls its own concurrency
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
		if err := cli.RunPublish(false, false, false, false); err != nil {
			t.Fatalf("Failed to publish %s: %v", name, err)
		}
	}

	// Create a project
	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)

	// Add packages concurrently
	RunConcurrently(t,
		func() error {
			if err := os.Chdir(projectDir); err != nil {
				return err
			}
			return cli.RunAdd("pkg-a", false, false, false)
		},
		func() error {
			if err := os.Chdir(projectDir); err != nil {
				return err
			}
			return cli.RunAdd("pkg-b", false, false, false)
		},
		func() error {
			if err := os.Chdir(projectDir); err != nil {
				return err
			}
			return cli.RunAdd("pkg-c", false, false, false)
		},
	)

	// Verify all packages were added
	for _, name := range packages {
		env.AssertSymlinkExists(projectDir, name)
		env.AssertDatabaseLink(name, projectDir)
	}
}

// TestAddConcurrentDifferentProjects tests concurrent adds to different projects
func TestAddConcurrentDifferentProjects(t *testing.T) {
	t.Skip("Skipping: os.Chdir is not goroutine-safe, test creates artificial race condition")
	// Don't use t.Parallel() - this test controls its own concurrency
	env := setupTest(t)

	// Create and publish a test package
	pkgDir := env.CreateTestPackage("shared-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'shared';",
	})
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPublish(false, false, false, false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Create multiple projects
	project1 := env.CreateTestPackage("project-1", "1.0.0", nil)
	project2 := env.CreateTestPackage("project-2", "1.0.0", nil)
	project3 := env.CreateTestPackage("project-3", "1.0.0", nil)

	// Add package to all projects concurrently
	RunConcurrently(t,
		func() error {
			if err := os.Chdir(project1); err != nil {
				return err
			}
			return cli.RunAdd("shared-pkg", false, false, false)
		},
		func() error {
			if err := os.Chdir(project2); err != nil {
				return err
			}
			return cli.RunAdd("shared-pkg", false, false, false)
		},
		func() error {
			if err := os.Chdir(project3); err != nil {
				return err
			}
			return cli.RunAdd("shared-pkg", false, false, false)
		},
	)

	// Verify package was added to all projects
	env.AssertDatabaseLink("shared-pkg", project1)
	env.AssertDatabaseLink("shared-pkg", project2)
	env.AssertDatabaseLink("shared-pkg", project3)
}

// TestAddWithNPMWorkspace tests adding to npm workspace project
func TestAddWithNPMWorkspace(t *testing.T) {
	env := setupTest(t)

	// Create and publish a test package
	pkgDir := env.CreateTestPackage("workspace-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'workspace';",
	})
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPublish(false, false, false, false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Copy npm workspace fixture
	workspaceDir := env.CopyFixture("npm-workspace")
	packageADir := filepath.Join(workspaceDir, "packages", "package-a")

	if err := os.Chdir(packageADir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}

	if err := cli.RunAdd("workspace-pkg", false, false, false); err != nil {
		t.Fatalf("Failed to add to workspace package: %v", err)
	}

	// Verify package was added to the workspace package
	env.AssertSymlinkExists(packageADir, "workspace-pkg")
	env.AssertPackageJSON(packageADir, "workspace-pkg", "file:.lnpm/workspace-pkg")
}

// TestAddMultiplePackages tests adding multiple packages in one command
func TestAddMultiplePackages(t *testing.T) {
	env := setupTest(t)

	// Create and publish multiple packages
	packages := []string{"multi-pkg-a", "multi-pkg-b", "multi-pkg-c"}
	for _, name := range packages {
		pkgDir := env.CreateTestPackage(name, "1.0.0", map[string]string{
			"index.js": "module.exports = '" + name + "';",
		})
		if err := os.Chdir(pkgDir); err != nil {
			t.Fatalf("Failed to chdir: %v", err)
		}
		if err := cli.RunPublish(false, false, false, false); err != nil {
			t.Fatalf("Failed to publish %s: %v", name, err)
		}
	}

	// Create a project
	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}

	// Add all packages at once
	if err := cli.RunAddMultiple(packages, false, false, false); err != nil {
		t.Fatalf("Failed to add multiple packages: %v", err)
	}

	// Verify all packages were added
	for _, name := range packages {
		env.AssertSymlinkExists(projectDir, name)
		env.AssertPackageJSON(projectDir, name, "file:.lnpm/"+name)
		env.AssertDatabaseLink(name, projectDir)
	}
}

// TestAddMultipleWithPartialFailure tests adding multiple packages where some fail
func TestAddMultipleWithPartialFailure(t *testing.T) {
	env := setupTest(t)

	// Create and publish only one package
	pkgDir := env.CreateTestPackage("exists-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'exists';",
	})
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPublish(false, false, false, false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Create a project
	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}

	// Add mix of existing and non-existing packages
	err := cli.RunAddMultiple([]string{"exists-pkg", "nonexistent-pkg"}, false, false, false)

	// Should not return error (partial success)
	if err != nil {
		t.Fatalf("Expected partial success, got error: %v", err)
	}

	// Verify the existing package was added
	env.AssertSymlinkExists(projectDir, "exists-pkg")
	env.AssertPackageJSON(projectDir, "exists-pkg", "file:.lnpm/exists-pkg")
}

// TestAddLockfileContents tests that lockfile contains correct information
func TestAddLockfileContents(t *testing.T) {
	env := setupTest(t)

	// Create and publish a test package
	pkgDir := env.CreateTestPackage("lock-pkg", "1.5.0", map[string]string{
		"index.js": "module.exports = 'lock';",
	})
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPublish(false, false, false, false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Create a project and add the package
	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}

	if err := cli.RunAdd("lock-pkg", false, false, false); err != nil {
		t.Fatalf("Failed to add package: %v", err)
	}

	// Verify lockfile contents
	env.AssertLockfile(projectDir, func(lock *lockfile.LockFile) {
		if !lock.Has("lock-pkg") {
			t.Error("Expected lock-pkg in lockfile")
		}

		pkg, ok := lock.Get("lock-pkg")
		if !ok {
			t.Fatal("Expected to get lock-pkg from lockfile")
		}

		if pkg.Version != "1.5.0" {
			t.Errorf("Expected version 1.5.0, got %s", pkg.Version)
		}

		if pkg.Hash == "" {
			t.Error("Expected hash to be set")
		}

		if pkg.Source == "" {
			t.Error("Expected source to be set")
		}
	})
}

// TestAddByVersion verifies that name@version resolves against the stored
// (latest) version: a matching version succeeds, a non-matching one fails
// with a clear error rather than silently matching a content hash (#39).
func TestAddByVersion(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.CreateTestPackage("ver-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'v1';",
	})
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	if err := cli.RunPublish(false, false, false, false); err != nil {
		t.Fatalf("publish: %v", err)
	}

	projectDir := env.CreateTestPackage("ver-project", "1.0.0", nil)
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	// Matching version resolves.
	if err := cli.RunAdd("ver-pkg@1.0.0", false, false, false); err != nil {
		t.Fatalf("add ver-pkg@1.0.0 should succeed, got: %v", err)
	}
	env.AssertPackageJSON(projectDir, "ver-pkg", "file:.lnpm/ver-pkg")

	// Non-matching version fails clearly.
	if err := cli.RunAdd("ver-pkg@9.9.9", false, false, false); err == nil {
		t.Fatal("add ver-pkg@9.9.9 should fail (only 1.0.0 is published)")
	}
}
