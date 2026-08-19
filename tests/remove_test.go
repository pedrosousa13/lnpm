package tests

import (
	"testing"

	"github.com/pedrosousa13/lnpm/internal/cli"
	"github.com/pedrosousa13/lnpm/pkg/lockfile"
)

// TestRemoveVariants table-drives single-package removal across the
// unscoped/scoped and prod/dev permutations. Each row publishes, adds, then
// removes the package and asserts that the symlink, db link, and package.json
// entry are all gone, and that the now-empty lockfile is deleted.
func TestRemoveVariants(t *testing.T) {
	cases := []struct {
		name    string
		pkgName string
		dev     bool
	}{
		{"unscoped prod dependency", "remove-pkg", false},
		{"scoped dependency", "@org/scoped-remove", false},
		{"dev dependency", "dev-remove-pkg", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := setupTest(t)

			env.simplePkg(tc.pkgName)
			projectDir := env.newProject("test-project")
			env.addPkg(projectDir, tc.pkgName, tc.dev, false)
			env.AssertSymlinkExists(projectDir, tc.pkgName)
			env.AssertDatabaseLink(tc.pkgName, projectDir)

			if err := cli.RunRemove(tc.pkgName, false, false); err != nil {
				t.Fatalf("Failed to remove package: %v", err)
			}

			env.AssertSymlinkMissing(projectDir, tc.pkgName)
			env.AssertDatabaseNoLink(tc.pkgName, projectDir)
			env.AssertPackageJSONMissing(projectDir, tc.pkgName)
			// Removing the only package deletes the lockfile.
			env.AssertLockfileExists(projectDir, false)
		})
	}
}

// TestRemoveRestoresOriginalVersion tests that the pre-add dependency version is
// restored on remove.
func TestRemoveRestoresOriginalVersion(t *testing.T) {
	env := setupTest(t)

	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	env.writePackageJSON(projectDir, map[string]interface{}{
		"name":         "test-project",
		"version":      "1.0.0",
		"dependencies": map[string]interface{}{"remove-pkg": "^1.0.0"},
	})

	env.simplePkg("remove-pkg")
	env.addPkg(projectDir, "remove-pkg", false, false)
	env.AssertPackageJSON(projectDir, "remove-pkg", "file:.lnpm/remove-pkg")

	if err := cli.RunRemove("remove-pkg", false, false); err != nil {
		t.Fatalf("Failed to remove package: %v", err)
	}
	env.AssertPackageJSON(projectDir, "remove-pkg", "^1.0.0")
}

// TestRemovePackageNotLinked tests removing a package that isn't linked errors.
func TestRemovePackageNotLinked(t *testing.T) {
	env := setupTest(t)

	env.newProject("test-project")
	if err := cli.RunRemove("nonexistent-pkg", false, false); err == nil {
		t.Fatal("Expected error when removing non-linked package, got nil")
	}
}

// TestRemoveNoOriginalVersion tests that removing a --pure-added package leaves
// package.json clean (it was never written there).
func TestRemoveNoOriginalVersion(t *testing.T) {
	env := setupTest(t)

	env.simplePkg("pure-pkg")
	projectDir := env.newProject("test-project")
	env.addPkg(projectDir, "pure-pkg", false, true)
	env.AssertPackageJSONMissing(projectDir, "pure-pkg")

	if err := cli.RunRemove("pure-pkg", false, false); err != nil {
		t.Fatalf("Failed to remove package: %v", err)
	}
	env.AssertPackageJSONMissing(projectDir, "pure-pkg")
}

// TestRemoveAllPackages tests removing all packages with --all flag.
func TestRemoveAllPackages(t *testing.T) {
	env := setupTest(t)

	packages := []string{"pkg-a", "pkg-b", "pkg-c"}
	for _, name := range packages {
		env.simplePkg(name)
	}
	projectDir := env.newProject("test-project")
	for _, name := range packages {
		env.addPkg(projectDir, name, false, false)
	}

	if err := cli.RunRemove("", true, true); err != nil {
		t.Fatalf("Failed to remove all packages: %v", err)
	}

	for _, name := range packages {
		env.AssertSymlinkMissing(projectDir, name)
		env.AssertDatabaseNoLink(name, projectDir)
		env.AssertPackageJSONMissing(projectDir, name)
	}
	env.AssertLockfileExists(projectDir, false)
}

// TestRemoveAllWithoutYesKeepsPackages tests that a non-interactive --all
// without --yes refuses: every link, symlink and the lock file survive.
func TestRemoveAllWithoutYesKeepsPackages(t *testing.T) {
	env := setupTest(t)

	packages := []string{"keep-a", "keep-b"}
	for _, name := range packages {
		env.simplePkg(name)
	}
	projectDir := env.newProject("test-project")
	for _, name := range packages {
		env.addPkg(projectDir, name, false, false)
	}

	if err := cli.RunRemove("", true, false); err != nil {
		t.Fatalf("Remove --all without --yes failed: %v", err)
	}

	for _, name := range packages {
		env.AssertSymlinkExists(projectDir, name)
		env.AssertDatabaseLink(name, projectDir)
	}
	env.AssertLockfileExists(projectDir, true)
}

// TestRemoveAllNoPackages tests --all when nothing is linked is a safe no-op.
func TestRemoveAllNoPackages(t *testing.T) {
	env := setupTest(t)

	projectDir := env.newProject("test-project")
	if err := cli.RunRemove("", true, true); err != nil {
		t.Fatalf("Remove --all with no packages failed: %v", err)
	}
	env.AssertLockfileExists(projectDir, false)
}

// TestRemoveKeepsOthers tests that removing one package keeps the rest linked and
// updates (does not delete) the still-populated lockfile.
func TestRemoveKeepsOthers(t *testing.T) {
	env := setupTest(t)

	packages := []string{"pkg-a", "pkg-b", "pkg-c"}
	for _, name := range packages {
		env.simplePkg(name)
	}
	projectDir := env.newProject("test-project")
	for _, name := range packages {
		env.addPkg(projectDir, name, false, false)
	}

	if err := cli.RunRemove("pkg-b", false, false); err != nil {
		t.Fatalf("Failed to remove pkg-b: %v", err)
	}

	env.AssertSymlinkMissing(projectDir, "pkg-b")
	env.AssertDatabaseNoLink("pkg-b", projectDir)
	env.AssertSymlinkExists(projectDir, "pkg-a")
	env.AssertSymlinkExists(projectDir, "pkg-c")
	env.AssertDatabaseLink("pkg-a", projectDir)
	env.AssertDatabaseLink("pkg-c", projectDir)

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
