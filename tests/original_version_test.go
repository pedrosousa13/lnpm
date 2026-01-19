package tests

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pedrosousa13/lnpm/internal/cli"
	"github.com/pedrosousa13/lnpm/pkg/lockfile"
)

// TestReaddPreservesOriginalVersion tests add → retreat → add keeps original version
func TestReaddPreservesOriginalVersion(t *testing.T) {
	env := setupTest(t)

	// Create and publish a test package
	pkgDir := env.CreateTestPackage("readd-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'readd';",
	})
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPublish(false, "", false, false, false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Create project with original dependency version
	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	pkgJSONPath := filepath.Join(projectDir, "package.json")

	originalContent := `{
  "name": "test-project",
  "version": "1.0.0",
  "dependencies": {
    "readd-pkg": "^2.0.0"
  }
}`
	if err := os.WriteFile(pkgJSONPath, []byte(originalContent), 0644); err != nil {
		t.Fatalf("Failed to write package.json: %v", err)
	}

	// First add
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunAdd("readd-pkg", false, false, false); err != nil {
		t.Fatalf("Failed to add package: %v", err)
	}

	// Verify lockfile stores original version
	env.AssertLockfile(projectDir, func(lock *lockfile.LockFile) {
		pkg, ok := lock.Get("readd-pkg")
		if !ok {
			t.Fatal("Package not in lockfile")
		}
		if pkg.OriginalVersion != "^2.0.0" {
			t.Errorf("Expected original version ^2.0.0, got %s", pkg.OriginalVersion)
		}
	})

	// Retreat
	if err := cli.RunRetreat(true, false); err != nil {
		t.Fatalf("Failed to retreat: %v", err)
	}

	// Verify original restored
	env.AssertPackageJSON(projectDir, "readd-pkg", "^2.0.0")

	// Re-add
	if err := cli.RunAdd("readd-pkg", false, false, false); err != nil {
		t.Fatalf("Failed to re-add package: %v", err)
	}

	// Verify lockfile still has original version (not file:.lnpm/...)
	env.AssertLockfile(projectDir, func(lock *lockfile.LockFile) {
		pkg, ok := lock.Get("readd-pkg")
		if !ok {
			t.Fatal("Package not in lockfile after re-add")
		}
		if pkg.OriginalVersion != "^2.0.0" {
			t.Errorf("Expected original version ^2.0.0 after re-add, got %s", pkg.OriginalVersion)
		}
	})

	// Retreat again
	if err := cli.RunRetreat(true, false); err != nil {
		t.Fatalf("Failed to retreat second time: %v", err)
	}

	// Should still restore to ^2.0.0
	env.AssertPackageJSON(projectDir, "readd-pkg", "^2.0.0")
}

// TestAddIgnoresLnpmReferenceAsOriginal tests "file:.lnpm/pkg" not saved as original
func TestAddIgnoresLnpmReferenceAsOriginal(t *testing.T) {
	env := setupTest(t)

	// Create and publish a test package
	pkgDir := env.CreateTestPackage("lnpm-ref-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'lnpm-ref';",
	})
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPublish(false, "", false, false, false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Create project with lnpm reference already in package.json
	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	pkgJSONPath := filepath.Join(projectDir, "package.json")

	// Simulate previous lnpm add (file: reference)
	lnpmRefContent := `{
  "name": "test-project",
  "version": "1.0.0",
  "dependencies": {
    "lnpm-ref-pkg": "file:.lnpm/lnpm-ref-pkg"
  }
}`
	if err := os.WriteFile(pkgJSONPath, []byte(lnpmRefContent), 0644); err != nil {
		t.Fatalf("Failed to write package.json: %v", err)
	}

	// Add package (should NOT save file:.lnpm as original)
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunAdd("lnpm-ref-pkg", false, false, false); err != nil {
		t.Fatalf("Failed to add package: %v", err)
	}

	// Verify lockfile does NOT have file:.lnpm as original
	env.AssertLockfile(projectDir, func(lock *lockfile.LockFile) {
		pkg, ok := lock.Get("lnpm-ref-pkg")
		if !ok {
			t.Fatal("Package not in lockfile")
		}
		// Original should be empty, not the lnpm reference
		if pkg.OriginalVersion == "file:.lnpm/lnpm-ref-pkg" {
			t.Error("Original version should NOT be file:.lnpm reference")
		}
		if pkg.OriginalVersion == "link:.lnpm/lnpm-ref-pkg" {
			t.Error("Original version should NOT be link:.lnpm reference")
		}
	})
}

// TestRetreatIgnoresCorruptedOriginalVersion tests retreat ignores file:.lnpm/ as original
func TestRetreatIgnoresCorruptedOriginalVersion(t *testing.T) {
	env := setupTest(t)

	// Create and publish the package first (so retreat doesn't fail on missing pkg)
	pkgDir := env.CreateTestPackage("my-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'my-pkg';",
	})
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPublish(false, "", false, false, false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Create project with corrupted lockfile (originalVersion is file:.lnpm/...)
	projectDir := env.CreateTestPackage("corrupted-project", "1.0.0", nil)

	// Write package.json with file: reference
	pkgJSON := `{
  "name": "corrupted-project",
  "version": "1.0.0",
  "dependencies": {
    "my-pkg": "file:.lnpm/my-pkg"
  }
}`
	if err := os.WriteFile(filepath.Join(projectDir, "package.json"), []byte(pkgJSON), 0644); err != nil {
		t.Fatalf("Failed to write package.json: %v", err)
	}

	// Create corrupted lockfile with file:.lnpm/ as originalVersion
	lock := &lockfile.LockFile{
		Version:  1,
		Packages: make(map[string]lockfile.Package),
	}
	lock.Add("my-pkg", lockfile.Package{
		Version:         "1.0.0",
		Hash:            "abc123",
		Source:          "/some/path",
		Linked:          time.Now(),
		OriginalVersion: "file:.lnpm/my-pkg", // Corrupted - should be ignored
	})
	if err := lock.Save(projectDir); err != nil {
		t.Fatalf("Failed to save lockfile: %v", err)
	}

	// Create .lnpm directory so retreat thinks package is linked
	lnpmDir := filepath.Join(projectDir, ".lnpm", "my-pkg")
	if err := os.MkdirAll(lnpmDir, 0755); err != nil {
		t.Fatalf("Failed to create .lnpm dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(lnpmDir, "package.json"), []byte(`{"name":"my-pkg","version":"1.0.0"}`), 0644); err != nil {
		t.Fatalf("Failed to write package.json: %v", err)
	}

	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}

	// Retreat should NOT restore file:.lnpm reference
	if err := cli.RunRetreat(true, false); err != nil {
		t.Fatalf("Failed to retreat: %v", err)
	}

	// Package should be removed, not restored to corrupted value
	env.AssertPackageJSONMissing(projectDir, "my-pkg")
}

// TestAddWithExistingOriginalInLockfile tests re-add preserves lockfile original
func TestAddWithExistingOriginalInLockfile(t *testing.T) {
	env := setupTest(t)

	// Create and publish package
	pkgDir := env.CreateTestPackage("existing-orig-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'test';",
	})
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPublish(false, "", false, false, false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Create project with package.json already having file: reference
	// but lockfile having the true original
	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	pkgJSONPath := filepath.Join(projectDir, "package.json")

	// Package.json has file: reference (from previous add)
	pkgContent := `{
  "name": "test-project",
  "version": "1.0.0",
  "dependencies": {
    "existing-orig-pkg": "file:.lnpm/existing-orig-pkg"
  }
}`
	if err := os.WriteFile(pkgJSONPath, []byte(pkgContent), 0644); err != nil {
		t.Fatalf("Failed to write package.json: %v", err)
	}

	// Create lockfile with true original
	lock := &lockfile.LockFile{
		Version:  1,
		Packages: make(map[string]lockfile.Package),
	}
	lock.Add("existing-orig-pkg", lockfile.Package{
		Version:         "1.0.0",
		Hash:            "somehash",
		OriginalVersion: "^3.0.0",
	})
	if err := lock.Save(projectDir); err != nil {
		t.Fatalf("Failed to save lockfile: %v", err)
	}

	// Add package again
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunAdd("existing-orig-pkg", false, false, false); err != nil {
		t.Fatalf("Failed to add package: %v", err)
	}

	// Verify lockfile preserved original from existing lockfile entry
	env.AssertLockfile(projectDir, func(lock *lockfile.LockFile) {
		pkg, ok := lock.Get("existing-orig-pkg")
		if !ok {
			t.Fatal("Package not in lockfile")
		}
		if pkg.OriginalVersion != "^3.0.0" {
			t.Errorf("Expected preserved original ^3.0.0, got %s", pkg.OriginalVersion)
		}
	})
}

// TestAddNewPackageWithoutOriginal tests adding new package has empty original
func TestAddNewPackageWithoutOriginal(t *testing.T) {
	env := setupTest(t)

	// Create and publish package
	pkgDir := env.CreateTestPackage("new-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'new';",
	})
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPublish(false, "", false, false, false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Create project without the package in dependencies
	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)

	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}

	// Add new package (not previously in package.json)
	if err := cli.RunAdd("new-pkg", false, false, false); err != nil {
		t.Fatalf("Failed to add package: %v", err)
	}

	// Verify lockfile has empty original (since it wasn't in package.json before)
	env.AssertLockfile(projectDir, func(lock *lockfile.LockFile) {
		pkg, ok := lock.Get("new-pkg")
		if !ok {
			t.Fatal("Package not in lockfile")
		}
		if pkg.OriginalVersion != "" {
			t.Errorf("Expected empty original for new package, got %s", pkg.OriginalVersion)
		}
	})

	// Retreat should remove the package (no original to restore)
	if err := cli.RunRetreat(true, false); err != nil {
		t.Fatalf("Failed to retreat: %v", err)
	}

	env.AssertPackageJSONMissing(projectDir, "new-pkg")
}
