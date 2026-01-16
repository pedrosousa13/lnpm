package tests

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/cli"
)

// TestRetreatBasic tests basic retreat functionality
func TestRetreatBasic(t *testing.T) {
	t.Parallel()
	env := setupTest(t)

	// Create and publish a test package
	pkgDir := env.CreateTestPackage("retreat-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'retreat';",
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

	if err := cli.RunAdd("retreat-pkg", false, false); err != nil {
		t.Fatalf("Failed to add package: %v", err)
	}

	// Verify package was added
	env.AssertSymlinkExists(projectDir, "retreat-pkg")
	env.AssertLockfileExists(projectDir, true)
	env.AssertDirectoryExists(filepath.Join(projectDir, ".lnpm"), true)

	// Run retreat with force flag
	if err := cli.RunRetreat(true); err != nil {
		t.Fatalf("Failed to retreat: %v", err)
	}

	// Verify everything was cleaned up
	env.AssertSymlinkMissing(projectDir, "retreat-pkg")
	env.AssertLockfileExists(projectDir, false)
	env.AssertDirectoryExists(filepath.Join(projectDir, ".lnpm"), false)
	env.AssertDatabaseNoLink("retreat-pkg", projectDir)
}

// TestRetreatNoForceFlag tests that retreat requires --force flag
func TestRetreatNoForceFlag(t *testing.T) {
	t.Parallel()
	env := setupTest(t)

	// Create and publish a test package
	pkgDir := env.CreateTestPackage("force-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'force';",
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

	if err := cli.RunAdd("force-pkg", false, false); err != nil {
		t.Fatalf("Failed to add package: %v", err)
	}

	// Run retreat without force flag (should not fail, but should do nothing)
	if err := cli.RunRetreat(false); err != nil {
		t.Fatalf("Retreat without force failed: %v", err)
	}

	// Verify nothing was changed
	env.AssertSymlinkExists(projectDir, "force-pkg")
	env.AssertLockfileExists(projectDir, true)
	env.AssertDirectoryExists(filepath.Join(projectDir, ".lnpm"), true)
}

// TestRetreatNoLinks tests retreating when no links exist
func TestRetreatNoLinks(t *testing.T) {
	t.Parallel()
	env := setupTest(t)

	// Create a project without any links
	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}

	// Run retreat (should be noop)
	if err := cli.RunRetreat(true); err != nil {
		t.Fatalf("Retreat with no links failed: %v", err)
	}

	// Project should remain unchanged
	env.AssertLockfileExists(projectDir, false)
	env.AssertDirectoryExists(filepath.Join(projectDir, ".lnpm"), false)
}

// TestRetreatRestoresOriginalVersion tests that original dependency version is restored
func TestRetreatRestoresOriginalVersion(t *testing.T) {
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
    "retreat-pkg": "^1.0.0"
  }
}`
	if err := os.WriteFile(pkgJSONPath, []byte(originalContent), 0644); err != nil {
		t.Fatalf("Failed to write package.json: %v", err)
	}

	// Create and publish a test package
	pkgDir := env.CreateTestPackage("retreat-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'retreat';",
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
	if err := cli.RunAdd("retreat-pkg", false, false); err != nil {
		t.Fatalf("Failed to add package: %v", err)
	}

	// Verify file: reference was added
	env.AssertPackageJSON(projectDir, "retreat-pkg", "file:.lnpm/retreat-pkg")

	// Run retreat
	if err := cli.RunRetreat(true); err != nil {
		t.Fatalf("Failed to retreat: %v", err)
	}

	// Verify original version was restored
	env.AssertPackageJSON(projectDir, "retreat-pkg", "^1.0.0")
}

// TestRetreatMultiplePackages tests retreating with multiple linked packages
func TestRetreatMultiplePackages(t *testing.T) {
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

	// Verify all packages were added
	for _, name := range packages {
		env.AssertSymlinkExists(projectDir, name)
	}

	// Run retreat
	if err := cli.RunRetreat(true); err != nil {
		t.Fatalf("Failed to retreat: %v", err)
	}

	// Verify all packages were removed
	for _, name := range packages {
		env.AssertSymlinkMissing(projectDir, name)
		env.AssertDatabaseNoLink(name, projectDir)
	}

	// Verify cleanup
	env.AssertLockfileExists(projectDir, false)
	env.AssertDirectoryExists(filepath.Join(projectDir, ".lnpm"), false)
}

// TestRetreatPreservesOtherDependencies tests that retreat doesn't remove non-lnpm dependencies
func TestRetreatPreservesOtherDependencies(t *testing.T) {
	t.Parallel()
	env := setupTest(t)

	// Create project with mixed dependencies
	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	pkgJSONPath := filepath.Join(projectDir, "package.json")

	// Add both lnpm and regular dependencies
	mixedContent := `{
  "name": "test-project",
  "version": "1.0.0",
  "dependencies": {
    "lodash": "^4.17.21",
    "express": "^4.18.0"
  }
}`
	if err := os.WriteFile(pkgJSONPath, []byte(mixedContent), 0644); err != nil {
		t.Fatalf("Failed to write package.json: %v", err)
	}

	// Create and publish a test package
	pkgDir := env.CreateTestPackage("retreat-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'retreat';",
	})
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPublish(false, "", false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Add lnpm package
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunAdd("retreat-pkg", false, false); err != nil {
		t.Fatalf("Failed to add package: %v", err)
	}

	// Run retreat
	if err := cli.RunRetreat(true); err != nil {
		t.Fatalf("Failed to retreat: %v", err)
	}

	// Verify regular dependencies are still there
	env.AssertPackageJSON(projectDir, "lodash", "^4.17.21")
	env.AssertPackageJSON(projectDir, "express", "^4.18.0")

	// Verify lnpm package was removed
	env.AssertPackageJSONMissing(projectDir, "retreat-pkg")
}

// TestRetreatPartiallyLinked tests retreat with some packages linked, some not
func TestRetreatPartiallyLinked(t *testing.T) {
	t.Parallel()
	env := setupTest(t)

	// Create and publish two packages
	pkg1Dir := env.CreateTestPackage("pkg-linked", "1.0.0", map[string]string{
		"index.js": "module.exports = 'linked';",
	})
	if err := os.Chdir(pkg1Dir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPublish(false, "", false); err != nil {
		t.Fatalf("Failed to publish pkg-linked: %v", err)
	}

	pkg2Dir := env.CreateTestPackage("pkg-notlinked", "1.0.0", map[string]string{
		"index.js": "module.exports = 'notlinked';",
	})
	if err := os.Chdir(pkg2Dir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPublish(false, "", false); err != nil {
		t.Fatalf("Failed to publish pkg-notlinked: %v", err)
	}

	// Create project and add only one package
	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}

	if err := cli.RunAdd("pkg-linked", false, false); err != nil {
		t.Fatalf("Failed to add pkg-linked: %v", err)
	}

	// Verify only one package is linked
	env.AssertSymlinkExists(projectDir, "pkg-linked")
	env.AssertSymlinkMissing(projectDir, "pkg-notlinked")

	// Run retreat
	if err := cli.RunRetreat(true); err != nil {
		t.Fatalf("Failed to retreat: %v", err)
	}

	// Verify cleanup
	env.AssertLockfileExists(projectDir, false)
	env.AssertDirectoryExists(filepath.Join(projectDir, ".lnpm"), false)
}

// TestRetreatCleansGitignore tests that .gitignore is cleaned up
func TestRetreatCleansGitignore(t *testing.T) {
	t.Parallel()
	env := setupTest(t)

	// Create and publish a test package
	pkgDir := env.CreateTestPackage("git-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'git';",
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

	if err := cli.RunAdd("git-pkg", false, false); err != nil {
		t.Fatalf("Failed to add package: %v", err)
	}

	// Verify .gitignore has .lnpm entry
	env.AssertGitignore(projectDir, ".lnpm/", true)

	// Run retreat
	if err := cli.RunRetreat(true); err != nil {
		t.Fatalf("Failed to retreat: %v", err)
	}

	// Verify .gitignore was cleaned (entry removed or file doesn't exist)
	// Note: gitignore.RemoveFromGitignore removes the entry but may leave the file
	gitignorePath := filepath.Join(projectDir, ".gitignore")
	if _, err := os.Stat(gitignorePath); err == nil {
		env.AssertGitignore(projectDir, ".lnpm/", false)
	}
}

// TestRetreatWithDevDependency tests retreat with dev dependencies
func TestRetreatWithDevDependency(t *testing.T) {
	t.Parallel()
	env := setupTest(t)

	// Create and publish a test package
	pkgDir := env.CreateTestPackage("dev-retreat-pkg", "1.0.0", map[string]string{
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

	if err := cli.RunAdd("dev-retreat-pkg", true, false); err != nil {
		t.Fatalf("Failed to add dev package: %v", err)
	}

	// Run retreat
	if err := cli.RunRetreat(true); err != nil {
		t.Fatalf("Failed to retreat: %v", err)
	}

	// Verify cleanup
	env.AssertSymlinkMissing(projectDir, "dev-retreat-pkg")
	env.AssertLockfileExists(projectDir, false)
	env.AssertPackageJSONMissing(projectDir, "dev-retreat-pkg")
}

// TestRetreatWithScopedPackages tests retreat with scoped packages
func TestRetreatWithScopedPackages(t *testing.T) {
	t.Parallel()
	env := setupTest(t)

	// Create and publish a scoped package
	pkgDir := env.CreateTestPackage("@org/scoped-retreat", "1.0.0", map[string]string{
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

	if err := cli.RunAdd("@org/scoped-retreat", false, false); err != nil {
		t.Fatalf("Failed to add package: %v", err)
	}

	// Verify scoped package was added
	env.AssertSymlinkExists(projectDir, "@org/scoped-retreat")

	// Run retreat
	if err := cli.RunRetreat(true); err != nil {
		t.Fatalf("Failed to retreat: %v", err)
	}

	// Verify cleanup
	env.AssertSymlinkMissing(projectDir, "@org/scoped-retreat")
	env.AssertLockfileExists(projectDir, false)
	env.AssertPackageJSONMissing(projectDir, "@org/scoped-retreat")
}
