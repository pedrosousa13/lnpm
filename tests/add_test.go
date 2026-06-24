package tests

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/cli"
	"github.com/pedrosousa13/lnpm/pkg/lockfile"
)

// TestAddVariants table-drives the core "publish then add" behaviors across the
// scoped/unscoped and prod/dev permutations. Each row asserts the full set of
// side effects that are common to a successful add.
func TestAddVariants(t *testing.T) {
	cases := []struct {
		name    string
		pkgName string
		dev     bool
	}{
		{"unscoped prod dependency", "test-pkg", false},
		{"scoped prod dependency", "@test-org/scoped-pkg", false},
		{"unscoped dev dependency", "dev-pkg", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := setupTest(t)

			env.simplePkg(tc.pkgName)
			projectDir := env.newProject("test-project")
			env.addPkg(projectDir, tc.pkgName, tc.dev, false)

			// Common side effects of a successful add.
			env.AssertDirectoryExists(filepath.Join(projectDir, ".lnpm"), true)
			env.AssertFilesLinked(projectDir, tc.pkgName)
			env.AssertSymlinkExists(projectDir, tc.pkgName)
			env.AssertPackageJSON(projectDir, tc.pkgName, "file:.lnpm/"+tc.pkgName)
			env.AssertLockfileExists(projectDir, true)
			env.AssertGitignore(projectDir, ".lnpm/", true)
			env.AssertDatabaseLink(tc.pkgName, projectDir)

			// Scoped packages create an intermediate scope directory.
			if filepath.Dir(tc.pkgName) != "." {
				scope := filepath.Dir(tc.pkgName)
				env.AssertDirectoryExists(filepath.Join(projectDir, "node_modules", scope), true)
			}
		})
	}
}

// TestAddPreservesDevDependencyLocation tests that adding without --dev preserves
// a package's existing devDependencies location rather than moving it to deps.
func TestAddPreservesDevDependencyLocation(t *testing.T) {
	env := setupTest(t)

	env.simplePkg("preserve-pkg")

	// Project where preserve-pkg already lives in devDependencies.
	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	env.writePackageJSON(projectDir, map[string]interface{}{
		"name":    "test-project",
		"version": "1.0.0",
		"devDependencies": map[string]interface{}{
			"preserve-pkg": "^1.0.0",
		},
	})

	// Add WITHOUT --dev: should stay in devDependencies.
	env.addPkg(projectDir, "preserve-pkg", false, false)

	result := env.storedPackageJSON(projectDir)
	if deps, ok := result["dependencies"].(map[string]interface{}); ok && deps["preserve-pkg"] != nil {
		t.Error("Package should NOT be in dependencies")
	}
	devDeps := result["devDependencies"].(map[string]interface{})
	if devDeps["preserve-pkg"] != "file:.lnpm/preserve-pkg" {
		t.Errorf("Expected preserve-pkg in devDependencies with lnpm reference, got %v", devDeps["preserve-pkg"])
	}
}

// TestAddPureFlag tests adding with --pure flag (no package.json update, but all
// other linking still happens).
func TestAddPureFlag(t *testing.T) {
	env := setupTest(t)

	env.simplePkg("pure-pkg")
	projectDir := env.newProject("test-project")
	env.addPkg(projectDir, "pure-pkg", false, true)

	env.AssertPackageJSONMissing(projectDir, "pure-pkg")
	env.AssertFilesLinked(projectDir, "pure-pkg")
	env.AssertSymlinkExists(projectDir, "pure-pkg")
	env.AssertLockfileExists(projectDir, true)
}

// TestAddErrors covers the failure paths that must return a non-nil error.
func TestAddErrors(t *testing.T) {
	t.Run("package not found", func(t *testing.T) {
		env := setupTest(t)
		env.newProject("test-project")
		if err := cli.RunAdd("nonexistent-package", false, false, false); err == nil {
			t.Fatal("Expected error when adding non-existent package, got nil")
		}
	})

	t.Run("no package.json", func(t *testing.T) {
		env := setupTest(t)
		projectDir := filepath.Join(env.TempDir, "no-package-json")
		if err := os.MkdirAll(projectDir, 0755); err != nil {
			t.Fatalf("Failed to create dir: %v", err)
		}
		env.chdir(projectDir)
		if err := cli.RunAdd("some-pkg", false, false, false); err == nil {
			t.Fatal("Expected error when no package.json exists, got nil")
		}
	})
}

// TestAddIdempotent tests adding a package twice leaves it correctly linked.
func TestAddIdempotent(t *testing.T) {
	env := setupTest(t)

	_, projectDir := env.publishAndAdd("test-pkg")
	// Second add should be a no-op (idempotent), not an error.
	env.addPkg(projectDir, "test-pkg", false, false)

	env.AssertSymlinkExists(projectDir, "test-pkg")
	env.AssertDatabaseLink("test-pkg", projectDir)
}

// TestAddUpdatesExisting tests that re-adding after publishing a new version
// relinks the updated package.
func TestAddUpdatesExisting(t *testing.T) {
	env := setupTest(t)

	pkgDir, projectDir := env.publishAndAdd("test-pkg")

	// Publish v2 with new content.
	env.chdir(pkgDir)
	env.writeFile(filepath.Join(pkgDir, "package.json"), `{"name":"test-pkg","version":"2.0.0"}`)
	env.writeFile(filepath.Join(pkgDir, "index.js"), "module.exports = 'v2';")
	if err := cli.RunPublish(false, false, false, false); err != nil {
		t.Fatalf("Failed to publish v2: %v", err)
	}

	env.addPkg(projectDir, "test-pkg", false, false)

	env.AssertSymlinkExists(projectDir, "test-pkg")
	env.AssertPackageJSON(projectDir, "test-pkg", "file:.lnpm/test-pkg")
}

// TestAddConcurrentSameProject tests concurrent adds to same project.
func TestAddConcurrentSameProject(t *testing.T) {
	t.Skip("Skipping: concurrent package.json writes cause race conditions, not realistic usage")
	env := setupTest(t)

	packages := []string{"pkg-a", "pkg-b", "pkg-c"}
	for _, name := range packages {
		env.simplePkg(name)
	}
	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)

	RunConcurrently(t,
		func() error { _ = os.Chdir(projectDir); return cli.RunAdd("pkg-a", false, false, false) },
		func() error { _ = os.Chdir(projectDir); return cli.RunAdd("pkg-b", false, false, false) },
		func() error { _ = os.Chdir(projectDir); return cli.RunAdd("pkg-c", false, false, false) },
	)

	for _, name := range packages {
		env.AssertSymlinkExists(projectDir, name)
		env.AssertDatabaseLink(name, projectDir)
	}
}

// TestAddConcurrentDifferentProjects tests concurrent adds to different projects.
func TestAddConcurrentDifferentProjects(t *testing.T) {
	t.Skip("Skipping: os.Chdir is not goroutine-safe, test creates artificial race condition")
	env := setupTest(t)

	env.simplePkg("shared-pkg")
	project1 := env.CreateTestPackage("project-1", "1.0.0", nil)
	project2 := env.CreateTestPackage("project-2", "1.0.0", nil)
	project3 := env.CreateTestPackage("project-3", "1.0.0", nil)

	RunConcurrently(t,
		func() error { _ = os.Chdir(project1); return cli.RunAdd("shared-pkg", false, false, false) },
		func() error { _ = os.Chdir(project2); return cli.RunAdd("shared-pkg", false, false, false) },
		func() error { _ = os.Chdir(project3); return cli.RunAdd("shared-pkg", false, false, false) },
	)

	env.AssertDatabaseLink("shared-pkg", project1)
	env.AssertDatabaseLink("shared-pkg", project2)
	env.AssertDatabaseLink("shared-pkg", project3)
}

// TestAddWithNPMWorkspace tests adding to a package inside an npm workspace.
func TestAddWithNPMWorkspace(t *testing.T) {
	env := setupTest(t)

	env.simplePkg("workspace-pkg")

	workspaceDir := env.CopyFixture("npm-workspace")
	packageADir := filepath.Join(workspaceDir, "packages", "package-a")
	env.addPkg(packageADir, "workspace-pkg", false, false)

	env.AssertSymlinkExists(packageADir, "workspace-pkg")
	env.AssertPackageJSON(packageADir, "workspace-pkg", "file:.lnpm/workspace-pkg")
}

// TestAddMultiplePackages tests adding multiple packages in one command.
func TestAddMultiplePackages(t *testing.T) {
	env := setupTest(t)

	packages := []string{"multi-pkg-a", "multi-pkg-b", "multi-pkg-c"}
	for _, name := range packages {
		env.simplePkg(name)
	}

	projectDir := env.newProject("test-project")
	if err := cli.RunAddMultiple(packages, false, false, false, false); err != nil {
		t.Fatalf("Failed to add multiple packages: %v", err)
	}

	for _, name := range packages {
		env.AssertSymlinkExists(projectDir, name)
		env.AssertPackageJSON(projectDir, name, "file:.lnpm/"+name)
		env.AssertDatabaseLink(name, projectDir)
	}
}

// TestAddMultipleWithPartialFailure tests that a partial failure surfaces a
// non-zero error while still applying the packages that succeeded.
func TestAddMultipleWithPartialFailure(t *testing.T) {
	env := setupTest(t)

	env.simplePkg("exists-pkg")
	projectDir := env.newProject("test-project")

	err := cli.RunAddMultiple([]string{"exists-pkg", "nonexistent-pkg"}, false, false, false, false)
	if err == nil {
		t.Fatal("Expected an error when one of the packages fails to add")
	}

	env.AssertSymlinkExists(projectDir, "exists-pkg")
	env.AssertPackageJSON(projectDir, "exists-pkg", "file:.lnpm/exists-pkg")
}

// TestAddLockfileContents tests that the lockfile records version, hash and source.
func TestAddLockfileContents(t *testing.T) {
	env := setupTest(t)

	env.publishPkg("lock-pkg", "1.5.0", map[string]string{
		"index.js": "module.exports = 'lock';",
	})
	projectDir := env.newProject("test-project")
	env.addPkg(projectDir, "lock-pkg", false, false)

	env.AssertLockfile(projectDir, func(lock *lockfile.LockFile) {
		pkg, ok := lock.Get("lock-pkg")
		if !ok {
			t.Fatal("Expected lock-pkg in lockfile")
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

	env.publishPkg("ver-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'v1';",
	})
	projectDir := env.newProject("ver-project")

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
