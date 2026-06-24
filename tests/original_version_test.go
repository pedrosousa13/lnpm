package tests

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pedrosousa13/lnpm/internal/cli"
	"github.com/pedrosousa13/lnpm/pkg/lockfile"
)

// assertOriginalVersion is a small helper to check a package's recorded
// OriginalVersion in the project's lockfile.
func assertOriginalVersion(t *testing.T, env *TestEnvironment, projectDir, pkg, want string) {
	t.Helper()
	env.AssertLockfile(projectDir, func(lock *lockfile.LockFile) {
		p, ok := lock.Get(pkg)
		if !ok {
			t.Fatalf("Package %s not in lockfile", pkg)
		}
		if p.OriginalVersion != want {
			t.Errorf("Expected original version %q for %s, got %q", want, pkg, p.OriginalVersion)
		}
	})
}

// TestReaddPreservesOriginalVersion tests add → retreat → add keeps the original
// semver across cycles (so retreat always restores it).
func TestReaddPreservesOriginalVersion(t *testing.T) {
	env := setupTest(t)

	env.simplePkg("readd-pkg")

	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	env.writePackageJSON(projectDir, map[string]interface{}{
		"name":         "test-project",
		"version":      "1.0.0",
		"dependencies": map[string]interface{}{"readd-pkg": "^2.0.0"},
	})

	env.addPkg(projectDir, "readd-pkg", false, false)
	assertOriginalVersion(t, env, projectDir, "readd-pkg", "^2.0.0")

	if err := cli.RunRetreat(true, false); err != nil {
		t.Fatalf("Failed to retreat: %v", err)
	}
	env.AssertPackageJSON(projectDir, "readd-pkg", "^2.0.0")

	// Re-add: lockfile must still carry the original.
	env.addPkg(projectDir, "readd-pkg", false, false)
	assertOriginalVersion(t, env, projectDir, "readd-pkg", "^2.0.0")

	if err := cli.RunRetreat(true, false); err != nil {
		t.Fatalf("Failed to retreat second time: %v", err)
	}
	env.AssertPackageJSON(projectDir, "readd-pkg", "^2.0.0")
}

// TestAddIgnoresLnpmReferenceAsOriginal tests that an existing "file:.lnpm/..."
// reference is never recorded as the original version.
func TestAddIgnoresLnpmReferenceAsOriginal(t *testing.T) {
	env := setupTest(t)

	env.simplePkg("lnpm-ref-pkg")

	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	env.writePackageJSON(projectDir, map[string]interface{}{
		"name":         "test-project",
		"version":      "1.0.0",
		"dependencies": map[string]interface{}{"lnpm-ref-pkg": "file:.lnpm/lnpm-ref-pkg"},
	})

	env.addPkg(projectDir, "lnpm-ref-pkg", false, false)

	env.AssertLockfile(projectDir, func(lock *lockfile.LockFile) {
		pkg, ok := lock.Get("lnpm-ref-pkg")
		if !ok {
			t.Fatal("Package not in lockfile")
		}
		if pkg.OriginalVersion == "file:.lnpm/lnpm-ref-pkg" || pkg.OriginalVersion == "link:.lnpm/lnpm-ref-pkg" {
			t.Errorf("Original version should NOT be an lnpm reference, got %q", pkg.OriginalVersion)
		}
	})
}

// TestRetreatIgnoresCorruptedOriginalVersion tests that retreat does not restore
// a corrupted "file:.lnpm/" value stored as OriginalVersion.
func TestRetreatIgnoresCorruptedOriginalVersion(t *testing.T) {
	env := setupTest(t)

	env.simplePkg("my-pkg")

	projectDir := env.CreateTestPackage("corrupted-project", "1.0.0", nil)
	env.writePackageJSON(projectDir, map[string]interface{}{
		"name":         "corrupted-project",
		"version":      "1.0.0",
		"dependencies": map[string]interface{}{"my-pkg": "file:.lnpm/my-pkg"},
	})

	// Corrupted lockfile: OriginalVersion is itself an lnpm reference.
	lock := &lockfile.LockFile{Version: 1, Packages: make(map[string]lockfile.Package)}
	lock.Add("my-pkg", lockfile.Package{
		Version:         "1.0.0",
		Hash:            "abc123",
		Source:          "/some/path",
		Linked:          time.Now(),
		OriginalVersion: "file:.lnpm/my-pkg",
	})
	if err := lock.Save(projectDir); err != nil {
		t.Fatalf("Failed to save lockfile: %v", err)
	}

	// Make retreat believe the package is linked.
	lnpmDir := filepath.Join(projectDir, ".lnpm", "my-pkg")
	if err := os.MkdirAll(lnpmDir, 0755); err != nil {
		t.Fatalf("Failed to create .lnpm dir: %v", err)
	}
	env.writeFile(filepath.Join(lnpmDir, "package.json"), `{"name":"my-pkg","version":"1.0.0"}`)

	env.chdir(projectDir)
	if err := cli.RunRetreat(true, false); err != nil {
		t.Fatalf("Failed to retreat: %v", err)
	}
	env.AssertPackageJSONMissing(projectDir, "my-pkg")
}

// TestAddWithExistingOriginalInLockfile tests that re-adding preserves a true
// original recorded in the lockfile, even when package.json shows the file: ref.
func TestAddWithExistingOriginalInLockfile(t *testing.T) {
	env := setupTest(t)

	env.simplePkg("existing-orig-pkg")

	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	env.writePackageJSON(projectDir, map[string]interface{}{
		"name":         "test-project",
		"version":      "1.0.0",
		"dependencies": map[string]interface{}{"existing-orig-pkg": "file:.lnpm/existing-orig-pkg"},
	})

	// Lockfile carries the true original.
	lock := &lockfile.LockFile{Version: 1, Packages: make(map[string]lockfile.Package)}
	lock.Add("existing-orig-pkg", lockfile.Package{
		Version:         "1.0.0",
		Hash:            "somehash",
		OriginalVersion: "^3.0.0",
	})
	if err := lock.Save(projectDir); err != nil {
		t.Fatalf("Failed to save lockfile: %v", err)
	}

	env.addPkg(projectDir, "existing-orig-pkg", false, false)
	assertOriginalVersion(t, env, projectDir, "existing-orig-pkg", "^3.0.0")
}

// TestAddNewPackageWithoutOriginal tests that a brand-new package (not previously
// in package.json) records an empty original and is fully removed on retreat.
func TestAddNewPackageWithoutOriginal(t *testing.T) {
	env := setupTest(t)

	env.simplePkg("new-pkg")
	projectDir := env.newProject("test-project")
	env.addPkg(projectDir, "new-pkg", false, false)

	assertOriginalVersion(t, env, projectDir, "new-pkg", "")

	if err := cli.RunRetreat(true, false); err != nil {
		t.Fatalf("Failed to retreat: %v", err)
	}
	env.AssertPackageJSONMissing(projectDir, "new-pkg")
}
