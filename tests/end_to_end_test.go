package tests

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/cli"
)

// TestE2EPublishAddRemove tests complete publish → add → remove workflow
func TestE2EPublishAddRemove(t *testing.T) {
	env := setupTest(t)

	// 1. Publish a package
	pkgDir := env.CreateTestPackage("e2e-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'e2e';",
		"lib/utils.js": "module.exports.util = function() { return 'util'; };",
	})
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir to package: %v", err)
	}
	if err := cli.RunPublish(false, "", false); err != nil {
		t.Fatalf("Failed to publish package: %v", err)
	}

	// Verify package was published to database
	env.AssertPackageInDatabase("e2e-pkg", true)

	// 2. Add to a project
	projectDir := env.CreateTestPackage("e2e-project", "1.0.0", nil)
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Failed to chdir to project: %v", err)
	}
	if err := cli.RunAdd("e2e-pkg", false, false); err != nil {
		t.Fatalf("Failed to add package: %v", err)
	}

	// Verify package was added
	env.AssertSymlinkExists(projectDir, "e2e-pkg")
	env.AssertPackageJSON(projectDir, "e2e-pkg", "file:.lnpm/e2e-pkg")
	env.AssertLockfileExists(projectDir, true)
	env.AssertDatabaseLink("e2e-pkg", projectDir)

	// 3. Remove from project
	if err := cli.RunRemove("e2e-pkg", false); err != nil {
		t.Fatalf("Failed to remove package: %v", err)
	}

	// Verify package was removed
	env.AssertSymlinkMissing(projectDir, "e2e-pkg")
	env.AssertPackageJSONMissing(projectDir, "e2e-pkg")
	env.AssertLockfileExists(projectDir, false)
	env.AssertDatabaseNoLink("e2e-pkg", projectDir)
}

// TestE2EMultiplePackagesMultipleProjects tests linking multiple packages to multiple projects
func TestE2EMultiplePackagesMultipleProjects(t *testing.T) {
	env := setupTest(t)

	// Publish multiple packages
	packages := []string{"pkg-a", "pkg-b", "pkg-c"}
	for _, name := range packages {
		pkgDir := env.CreateTestPackage(name, "1.0.0", map[string]string{
			"index.js": "module.exports = '" + name + "';",
		})
		if err := os.Chdir(pkgDir); err != nil {
			t.Fatalf("Failed to chdir to %s: %v", name, err)
		}
		if err := cli.RunPublish(false, "", false); err != nil {
			t.Fatalf("Failed to publish %s: %v", name, err)
		}
	}

	// Create multiple projects
	projects := []string{"project-1", "project-2"}
	projectDirs := make(map[string]string)
	for _, name := range projects {
		projectDir := env.CreateTestPackage(name, "1.0.0", nil)
		projectDirs[name] = projectDir
	}

	// Add all packages to all projects
	for projName, projDir := range projectDirs {
		if err := os.Chdir(projDir); err != nil {
			t.Fatalf("Failed to chdir to %s: %v", projName, err)
		}

		for _, pkgName := range packages {
			if err := cli.RunAdd(pkgName, false, false); err != nil {
				t.Fatalf("Failed to add %s to %s: %v", pkgName, projName, err)
			}
		}
	}

	// Verify all links exist
	for _, projDir := range projectDirs {
		for _, pkgName := range packages {
			env.AssertSymlinkExists(projDir, pkgName)
			env.AssertDatabaseLink(pkgName, projDir)
		}
	}

	// Remove one package from one project
	if err := os.Chdir(projectDirs["project-1"]); err != nil {
		t.Fatalf("Failed to chdir to project-1: %v", err)
	}
	if err := cli.RunRemove("pkg-b", false); err != nil {
		t.Fatalf("Failed to remove pkg-b from project-1: %v", err)
	}

	// Verify pkg-b removed from project-1 but still exists in project-2
	env.AssertSymlinkMissing(projectDirs["project-1"], "pkg-b")
	env.AssertSymlinkExists(projectDirs["project-2"], "pkg-b")
}

// TestE2ERetreatWorkflow tests full retreat workflow
func TestE2ERetreatWorkflow(t *testing.T) {
	env := setupTest(t)

	// Publish packages
	packages := []string{"retreat-a", "retreat-b"}
	for _, name := range packages {
		pkgDir := env.CreateTestPackage(name, "1.0.0", map[string]string{
			"index.js": "module.exports = '" + name + "';",
		})
		if err := os.Chdir(pkgDir); err != nil {
			t.Fatalf("Failed to chdir to %s: %v", name, err)
		}
		if err := cli.RunPublish(false, "", false); err != nil {
			t.Fatalf("Failed to publish %s: %v", name, err)
		}
	}

	// Create project and add packages
	projectDir := env.CreateTestPackage("retreat-project", "1.0.0", nil)
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Failed to chdir to project: %v", err)
	}

	for _, name := range packages {
		if err := cli.RunAdd(name, false, false); err != nil {
			t.Fatalf("Failed to add %s: %v", name, err)
		}
	}

	// Verify packages are linked
	for _, name := range packages {
		env.AssertSymlinkExists(projectDir, name)
		env.AssertDatabaseLink(name, projectDir)
	}

	// Run retreat
	if err := cli.RunRetreat(true); err != nil {
		t.Fatalf("Failed to retreat: %v", err)
	}

	// Verify all links removed
	for _, name := range packages {
		env.AssertSymlinkMissing(projectDir, name)
		env.AssertDatabaseNoLink(name, projectDir)
	}

	// Verify cleanup
	env.AssertLockfileExists(projectDir, false)
	env.AssertDirectoryExists(filepath.Join(projectDir, ".lnpm"), false)
}

// TestE2EWorkspacePublish tests publishing from a workspace
func TestE2EWorkspacePublish(t *testing.T) {
	env := setupTest(t)

	// Copy npm workspace fixture
	workspaceDir := env.CopyFixture("npm-workspace")
	if err := os.Chdir(workspaceDir); err != nil {
		t.Fatalf("Failed to chdir to workspace: %v", err)
	}

	// Publish all workspace packages
	if err := cli.RunPublish(false, "", true); err != nil {
		t.Fatalf("Failed to publish workspace: %v", err)
	}

	// Verify packages were published
	env.AssertPackageInDatabase("@npm-test/package-a", true)
	env.AssertPackageInDatabase("@npm-test/package-b", true)
}

// TestE2EAddToWorkspacePackage tests adding a package to a workspace sub-package
func TestE2EAddToWorkspacePackage(t *testing.T) {
	env := setupTest(t)

	// Publish a standalone package
	pkgDir := env.CreateTestPackage("standalone-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'standalone';",
	})
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir to package: %v", err)
	}
	if err := cli.RunPublish(false, "", false); err != nil {
		t.Fatalf("Failed to publish package: %v", err)
	}

	// Copy workspace and add to sub-package
	workspaceDir := env.CopyFixture("npm-workspace")
	packageADir := filepath.Join(workspaceDir, "packages", "package-a")
	if err := os.Chdir(packageADir); err != nil {
		t.Fatalf("Failed to chdir to workspace package: %v", err)
	}

	if err := cli.RunAdd("standalone-pkg", false, false); err != nil {
		t.Fatalf("Failed to add to workspace package: %v", err)
	}

	// Verify package was added to workspace sub-package
	env.AssertSymlinkExists(packageADir, "standalone-pkg")
	env.AssertPackageJSON(packageADir, "standalone-pkg", "file:.lnpm/standalone-pkg")
}

// TestE2EScopedPackageWorkflow tests complete workflow with scoped packages
func TestE2EScopedPackageWorkflow(t *testing.T) {
	env := setupTest(t)

	// Publish scoped package
	pkgDir := env.CreateTestPackage("@myorg/scoped-e2e", "1.0.0", map[string]string{
		"index.js": "module.exports = 'scoped';",
		"lib/helper.js": "exports.help = () => 'help';",
	})
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir to package: %v", err)
	}
	if err := cli.RunPublish(false, "", false); err != nil {
		t.Fatalf("Failed to publish package: %v", err)
	}

	// Add to project
	projectDir := env.CreateTestPackage("scoped-project", "1.0.0", nil)
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Failed to chdir to project: %v", err)
	}
	if err := cli.RunAdd("@myorg/scoped-e2e", false, false); err != nil {
		t.Fatalf("Failed to add package: %v", err)
	}

	// Verify scoped package structure
	env.AssertSymlinkExists(projectDir, "@myorg/scoped-e2e")
	env.AssertDirectoryExists(filepath.Join(projectDir, "node_modules", "@myorg"), true)
	env.AssertPackageJSON(projectDir, "@myorg/scoped-e2e", "file:.lnpm/@myorg/scoped-e2e")

	// Remove scoped package
	if err := cli.RunRemove("@myorg/scoped-e2e", false); err != nil {
		t.Fatalf("Failed to remove package: %v", err)
	}

	// Verify removal
	env.AssertSymlinkMissing(projectDir, "@myorg/scoped-e2e")
	env.AssertPackageJSONMissing(projectDir, "@myorg/scoped-e2e")
}

// TestE2EMixedDependencies tests workflow with both regular and dev dependencies
func TestE2EMixedDependencies(t *testing.T) {
	env := setupTest(t)

	// Publish packages
	prodPkgDir := env.CreateTestPackage("prod-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'prod';",
	})
	if err := os.Chdir(prodPkgDir); err != nil {
		t.Fatalf("Failed to chdir to prod package: %v", err)
	}
	if err := cli.RunPublish(false, "", false); err != nil {
		t.Fatalf("Failed to publish prod package: %v", err)
	}

	devPkgDir := env.CreateTestPackage("dev-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'dev';",
	})
	if err := os.Chdir(devPkgDir); err != nil {
		t.Fatalf("Failed to chdir to dev package: %v", err)
	}
	if err := cli.RunPublish(false, "", false); err != nil {
		t.Fatalf("Failed to publish dev package: %v", err)
	}

	// Create project and add packages
	projectDir := env.CreateTestPackage("mixed-project", "1.0.0", nil)
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Failed to chdir to project: %v", err)
	}

	// Add prod dependency
	if err := cli.RunAdd("prod-pkg", false, false); err != nil {
		t.Fatalf("Failed to add prod package: %v", err)
	}

	// Add dev dependency
	if err := cli.RunAdd("dev-pkg", true, false); err != nil {
		t.Fatalf("Failed to add dev package: %v", err)
	}

	// Verify both are linked
	env.AssertSymlinkExists(projectDir, "prod-pkg")
	env.AssertSymlinkExists(projectDir, "dev-pkg")
	env.AssertDatabaseLink("prod-pkg", projectDir)
	env.AssertDatabaseLink("dev-pkg", projectDir)

	// Remove only dev dependency
	if err := cli.RunRemove("dev-pkg", false); err != nil {
		t.Fatalf("Failed to remove dev package: %v", err)
	}

	// Verify dev removed but prod still exists
	env.AssertSymlinkMissing(projectDir, "dev-pkg")
	env.AssertSymlinkExists(projectDir, "prod-pkg")

	// Lockfile should still exist
	env.AssertLockfileExists(projectDir, true)
}
