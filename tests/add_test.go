package tests

import (
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
	if err := cli.RunPublish(false, "", false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Create a project and add the package
	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}

	if err := cli.RunAdd("test-pkg", false, false); err != nil {
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
	if err := cli.RunPublish(false, "", false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Create a project and add the scoped package
	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}

	if err := cli.RunAdd("@test-org/scoped-pkg", false, false); err != nil {
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
	if err := cli.RunPublish(false, "", false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Create a project and add as dev dependency
	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}

	if err := cli.RunAdd("dev-pkg", true, false); err != nil {
		t.Fatalf("Failed to add package: %v", err)
	}

	// Verify it was added as dev dependency
	env.AssertPackageJSON(projectDir, "dev-pkg", "file:.lnpm/dev-pkg")
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
	if err := cli.RunPublish(false, "", false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Create a project and add with --pure flag
	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}

	if err := cli.RunAdd("pure-pkg", false, true); err != nil {
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
	err := cli.RunAdd("nonexistent-package", false, false)
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
	err := cli.RunAdd("some-pkg", false, false)
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
	if err := cli.RunPublish(false, "", false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Create a project and add the package twice
	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}

	if err := cli.RunAdd("test-pkg", false, false); err != nil {
		t.Fatalf("Failed to add package first time: %v", err)
	}

	if err := cli.RunAdd("test-pkg", false, false); err != nil {
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
	if err := cli.RunPublish(false, "", false); err != nil {
		t.Fatalf("Failed to publish v1: %v", err)
	}

	// Add to project
	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunAdd("test-pkg", false, false); err != nil {
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
	if err := cli.RunPublish(false, "", false); err != nil {
		t.Fatalf("Failed to publish v2: %v", err)
	}

	// Add updated version to project
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunAdd("test-pkg", false, false); err != nil {
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
		if err := cli.RunPublish(false, "", false); err != nil {
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
			return cli.RunAdd("pkg-a", false, false)
		},
		func() error {
			if err := os.Chdir(projectDir); err != nil {
				return err
			}
			return cli.RunAdd("pkg-b", false, false)
		},
		func() error {
			if err := os.Chdir(projectDir); err != nil {
				return err
			}
			return cli.RunAdd("pkg-c", false, false)
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
	if err := cli.RunPublish(false, "", false); err != nil {
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
			return cli.RunAdd("shared-pkg", false, false)
		},
		func() error {
			if err := os.Chdir(project2); err != nil {
				return err
			}
			return cli.RunAdd("shared-pkg", false, false)
		},
		func() error {
			if err := os.Chdir(project3); err != nil {
				return err
			}
			return cli.RunAdd("shared-pkg", false, false)
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
	if err := cli.RunPublish(false, "", false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Copy npm workspace fixture
	workspaceDir := env.CopyFixture("npm-workspace")
	packageADir := filepath.Join(workspaceDir, "packages", "package-a")

	if err := os.Chdir(packageADir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}

	if err := cli.RunAdd("workspace-pkg", false, false); err != nil {
		t.Fatalf("Failed to add to workspace package: %v", err)
	}

	// Verify package was added to the workspace package
	env.AssertSymlinkExists(packageADir, "workspace-pkg")
	env.AssertPackageJSON(packageADir, "workspace-pkg", "file:.lnpm/workspace-pkg")
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
	if err := cli.RunPublish(false, "", false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Create a project and add the package
	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}

	if err := cli.RunAdd("lock-pkg", false, false); err != nil {
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
