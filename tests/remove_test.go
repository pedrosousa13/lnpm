package tests

import (
	"path/filepath"
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

			if err := cli.RunRemove(tc.pkgName, false, false, false); err != nil {
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

	if err := cli.RunRemove("remove-pkg", false, false, false); err != nil {
		t.Fatalf("Failed to remove package: %v", err)
	}
	env.AssertPackageJSON(projectDir, "remove-pkg", "^1.0.0")
}

// TestRemovePackageNotLinked tests removing a package that isn't linked errors.
func TestRemovePackageNotLinked(t *testing.T) {
	env := setupTest(t)

	env.newProject("test-project")
	if err := cli.RunRemove("nonexistent-pkg", false, false, false); err == nil {
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

	if err := cli.RunRemove("pure-pkg", false, false, false); err != nil {
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

	if err := cli.RunRemove("", true, true, false); err != nil {
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

	if err := cli.RunRemove("", true, false, false); err != nil {
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
	if err := cli.RunRemove("", true, true, false); err != nil {
		t.Fatalf("Remove --all with no packages failed: %v", err)
	}
	env.AssertLockfileExists(projectDir, false)
}

// TestRemoveKeepsLockEntryWhenRestoreFails tests that a failed package.json
// restore aborts the removal, keeping the lock entry (with its OriginalVersion)
// and the database link.
func TestRemoveKeepsLockEntryWhenRestoreFails(t *testing.T) {
	env := setupTest(t)

	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	env.writePackageJSON(projectDir, map[string]interface{}{
		"name":         "test-project",
		"version":      "1.0.0",
		"dependencies": map[string]interface{}{"restore-fail-pkg": "^1.0.0"},
	})

	env.simplePkg("restore-fail-pkg")
	env.addPkg(projectDir, "restore-fail-pkg", false, false)
	env.AssertPackageJSON(projectDir, "restore-fail-pkg", "file:.lnpm/restore-fail-pkg")

	// Unparseable package.json: restorePackageJSON fails at json.Unmarshal.
	env.writeFile(filepath.Join(projectDir, "package.json"), "{not valid json")

	if err := cli.RunRemove("restore-fail-pkg", false, false, false); err == nil {
		t.Fatal("Expected error when package.json restore fails, got nil")
	}

	env.AssertLockfile(projectDir, func(lock *lockfile.LockFile) {
		pkg, ok := lock.Get("restore-fail-pkg")
		if !ok {
			t.Fatal("Expected restore-fail-pkg to remain in lockfile after failed restore")
		}
		if pkg.OriginalVersion != "^1.0.0" {
			t.Errorf("Expected OriginalVersion ^1.0.0 to survive, got %q", pkg.OriginalVersion)
		}
	})
	env.AssertDatabaseLink("restore-fail-pkg", projectDir)
}

// TestRemoveKeepsLockEntryWhenPackageJSONRemovalFails covers the other failure
// branch: a package added when package.json had no prior entry has an empty
// OriginalVersion, so remove takes the "delete the dependency" path. A failure
// there must abort the removal too, keeping the lock entry and database link.
func TestRemoveKeepsLockEntryWhenPackageJSONRemovalFails(t *testing.T) {
	env := setupTest(t)

	env.simplePkg("remove-fail-pkg")
	projectDir := env.newProject("test-project")
	env.addPkg(projectDir, "remove-fail-pkg", false, false)
	env.AssertPackageJSON(projectDir, "remove-fail-pkg", "file:.lnpm/remove-fail-pkg")
	env.AssertLockfile(projectDir, func(lock *lockfile.LockFile) {
		pkg, ok := lock.Get("remove-fail-pkg")
		if !ok {
			t.Fatal("Expected remove-fail-pkg in lockfile after add")
		}
		if pkg.OriginalVersion != "" {
			t.Fatalf("Test needs an empty OriginalVersion to reach the removal branch, got %q", pkg.OriginalVersion)
		}
	})

	// Unparseable package.json: removeFromPackageJSON fails at json.Unmarshal.
	env.writeFile(filepath.Join(projectDir, "package.json"), "{not valid json")

	if err := cli.RunRemove("remove-fail-pkg", false, false, false); err == nil {
		t.Fatal("Expected error when package.json update fails, got nil")
	}

	env.AssertLockfile(projectDir, func(lock *lockfile.LockFile) {
		if !lock.Has("remove-fail-pkg") {
			t.Error("Expected remove-fail-pkg to remain in lockfile after failed package.json update")
		}
	})
	env.AssertDatabaseLink("remove-fail-pkg", projectDir)
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

	if err := cli.RunRemove("pkg-b", false, false, false); err != nil {
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
