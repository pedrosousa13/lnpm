package tests

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/cli"
)

// TestPublishDuplicateHash tests publishing same content twice
func TestPublishDuplicateHash(t *testing.T) {
	env := setupTest(t)

	// Create and publish first time
	pkgDir := env.CreateTestPackage("dup-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'test';",
	})
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPublish(false, "", false); err != nil {
		t.Fatalf("Failed to publish first time: %v", err)
	}

	// Publish again with same content (should skip)
	if err := cli.RunPublish(false, "", false); err != nil {
		t.Fatalf("Failed to publish duplicate: %v", err)
	}

	// Should still be in database
	env.AssertPackageInDatabase("dup-pkg", true)
}

// TestPublishWithPush tests publish with --push flag
func TestPublishWithPush(t *testing.T) {
	env := setupTest(t)

	// Create and publish a package
	pkgDir := env.CreateTestPackage("push-publish-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'v1';",
	})
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPublish(false, "", false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Create project and link
	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunAdd("push-publish-pkg", false, false); err != nil {
		t.Fatalf("Failed to add package: %v", err)
	}

	// Modify and publish with --push
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "index.js"), []byte("module.exports = 'v2';"), 0644); err != nil {
		t.Fatalf("Failed to modify file: %v", err)
	}
	if err := cli.RunPublish(true, "", false); err != nil {
		t.Fatalf("Failed to publish with push: %v", err)
	}

	// Verify project received update
	linkedFile := filepath.Join(projectDir, ".lnpm", "push-publish-pkg", "index.js")
	content, err := os.ReadFile(linkedFile)
	if err != nil {
		t.Fatalf("Failed to read linked file: %v", err)
	}
	if string(content) != "module.exports = 'v2';" {
		t.Errorf("Expected updated content, got %s", string(content))
	}
}

// TestPublishConcurrentSamePackage tests concurrent publishes of same package
func TestPublishConcurrentSamePackage(t *testing.T) {
	// Don't use t.Parallel() - this test controls its own concurrency
	env := setupTest(t)

	// Create package
	pkgDir := env.CreateTestPackage("concurrent-pub", "1.0.0", map[string]string{
		"index.js": "module.exports = 'test';",
	})

	// Publish concurrently
	RunConcurrently(t,
		func() error {
			if err := os.Chdir(pkgDir); err != nil {
				return err
			}
			return cli.RunPublish(false, "", false)
		},
		func() error {
			if err := os.Chdir(pkgDir); err != nil {
				return err
			}
			return cli.RunPublish(false, "", false)
		},
		func() error {
			if err := os.Chdir(pkgDir); err != nil {
				return err
			}
			return cli.RunPublish(false, "", false)
		},
	)

	// Verify package published
	env.AssertPackageInDatabase("concurrent-pub", true)
}

// TestPublishNoPackageJSON tests publishing without package.json
func TestPublishNoPackageJSON(t *testing.T) {
	env := setupTest(t)

	// Create directory without package.json
	pkgDir := filepath.Join(env.TempDir, "no-pkg-json")
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		t.Fatalf("Failed to create dir: %v", err)
	}
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}

	// Try to publish
	err := cli.RunPublish(false, "", false)
	if err == nil {
		t.Fatal("Expected error publishing without package.json")
	}
}

// TestPublishEmptyPackage tests publishing package with no files
func TestPublishEmptyPackage(t *testing.T) {
	env := setupTest(t)

	// Create package with only package.json
	pkgDir := env.CreateTestPackage("empty-pkg", "1.0.0", nil)
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}

	// Publish should work (package.json counts as a file)
	if err := cli.RunPublish(false, "", false); err != nil {
		t.Fatalf("Failed to publish empty package: %v", err)
	}

	// Verify published
	env.AssertPackageInDatabase("empty-pkg", true)
}

// TestPublishLargePackage tests publishing package with many files
func TestPublishLargePackage(t *testing.T) {
	env := setupTest(t)

	// Create package with many files
	files := make(map[string]string)
	for i := 0; i < 100; i++ {
		files[fmt.Sprintf("file-%d.js", i)] = fmt.Sprintf("module.exports = %d;", i)
	}
	pkgDir := env.CreateTestPackage("large-pkg", "1.0.0", files)
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}

	// Publish
	if err := cli.RunPublish(false, "", false); err != nil {
		t.Fatalf("Failed to publish large package: %v", err)
	}

	// Verify all files tracked
	pkg, err := env.Database.GetPackageByName("large-pkg")
	if err != nil || pkg == nil {
		t.Fatalf("Failed to get package: %v", err)
	}

	files_db, err := env.Database.GetFilesForPackage(pkg.ID)
	if err != nil {
		t.Fatalf("Failed to get files: %v", err)
	}

	// Should have package.json + 100 files
	if len(files_db) < 100 {
		t.Errorf("Expected at least 100 files, got %d", len(files_db))
	}
}

// TestPublishNestedDirectories tests publishing with deeply nested directories
func TestPublishNestedDirectories(t *testing.T) {
	env := setupTest(t)

	// Create deeply nested structure
	pkgDir := env.CreateTestPackage("nested-pkg", "1.0.0", map[string]string{
		"a/b/c/d/e/deep.js": "module.exports = 'deep';",
		"a/b/mid.js":        "module.exports = 'mid';",
		"top.js":            "module.exports = 'top';",
	})
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}

	// Publish
	if err := cli.RunPublish(false, "", false); err != nil {
		t.Fatalf("Failed to publish nested package: %v", err)
	}

	// Verify structure preserved
	pkg, err := env.Database.GetPackageByName("nested-pkg")
	if err != nil || pkg == nil {
		t.Fatalf("Failed to get package: %v", err)
	}

	// Check store has nested structure
	deepFile := filepath.Join(pkg.StorePath, "a", "b", "c", "d", "e", "deep.js")
	if _, err := os.Stat(deepFile); err != nil {
		t.Errorf("Nested file not preserved in store: %v", err)
	}
}

// TestPublishScopedPackage tests publishing scoped package
func TestPublishScopedPackage(t *testing.T) {
	env := setupTest(t)

	// Create scoped package
	pkgDir := env.CreateTestPackage("@myorg/scoped-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'scoped';",
	})
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}

	// Publish
	if err := cli.RunPublish(false, "", false); err != nil {
		t.Fatalf("Failed to publish scoped package: %v", err)
	}

	// Verify
	env.AssertPackageInDatabase("@myorg/scoped-pkg", true)
}

// TestPublishVersionUpdate tests publishing new version of same package
func TestPublishVersionUpdate(t *testing.T) {
	env := setupTest(t)

	// Publish v1.0.0
	pkgDir := env.CreateTestPackage("version-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'v1';",
	})
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPublish(false, "", false); err != nil {
		t.Fatalf("Failed to publish v1: %v", err)
	}

	// Update to v2.0.0
	pkgJSONPath := filepath.Join(pkgDir, "package.json")
	if err := os.WriteFile(pkgJSONPath, []byte(`{"name":"version-pkg","version":"2.0.0"}`), 0644); err != nil {
		t.Fatalf("Failed to update version: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "index.js"), []byte("module.exports = 'v2';"), 0644); err != nil {
		t.Fatalf("Failed to update content: %v", err)
	}

	// Publish v2
	if err := cli.RunPublish(false, "", false); err != nil {
		t.Fatalf("Failed to publish v2: %v", err)
	}

	// Both versions should exist (different hashes)
	pkg, err := env.Database.GetPackageByName("version-pkg")
	if err != nil || pkg == nil {
		t.Fatalf("Failed to get package: %v", err)
	}

	// Latest should be v2
	if pkg.Version != "2.0.0" {
		t.Errorf("Expected version 2.0.0, got %s", pkg.Version)
	}
}

// TestPublishSpecialCharacters tests publishing with special characters in filenames
func TestPublishSpecialCharacters(t *testing.T) {
	env := setupTest(t)

	// Create package with special characters (within limits)
	pkgDir := env.CreateTestPackage("special-pkg", "1.0.0", map[string]string{
		"file-with-dash.js":       "module.exports = 'dash';",
		"file_with_underscore.js": "module.exports = 'underscore';",
		"file.multiple.dots.js":   "module.exports = 'dots';",
	})
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}

	// Publish
	if err := cli.RunPublish(false, "", false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Verify files stored
	pkg, err := env.Database.GetPackageByName("special-pkg")
	if err != nil || pkg == nil {
		t.Fatalf("Failed to get package: %v", err)
	}

	files, err := env.Database.GetFilesForPackage(pkg.ID)
	if err != nil {
		t.Fatalf("Failed to get files: %v", err)
	}

	// Should have at least the 3 special files + package.json
	if len(files) < 3 {
		t.Errorf("Expected at least 3 files, got %d", len(files))
	}
}

// TestPublishSymlinks tests publishing package containing symlinks
func TestPublishSymlinks(t *testing.T) {
	env := setupTest(t)

	// Create package
	pkgDir := env.CreateTestPackage("symlink-pkg", "1.0.0", map[string]string{
		"real.js": "module.exports = 'real';",
	})

	// Create symlink (if supported)
	symlinkPath := filepath.Join(pkgDir, "link.js")
	if err := os.Symlink("real.js", symlinkPath); err != nil {
		t.Skipf("Symlinks not supported: %v", err)
	}

	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}

	// Publish - should handle symlinks
	if err := cli.RunPublish(false, "", false); err != nil {
		t.Fatalf("Failed to publish with symlinks: %v", err)
	}

	env.AssertPackageInDatabase("symlink-pkg", true)
}

// TestPublishReadOnlyFiles tests publishing read-only files
func TestPublishReadOnlyFiles(t *testing.T) {
	if os.Getenv("CI") == "true" {
		t.Skip("Skipping read-only test in CI")
	}

	env := setupTest(t)

	// Create package with read-only file
	pkgDir := env.CreateTestPackage("readonly-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'test';",
	})

	// Make file read-only
	readOnlyFile := filepath.Join(pkgDir, "readonly.txt")
	if err := os.WriteFile(readOnlyFile, []byte("readonly"), 0444); err != nil {
		t.Fatalf("Failed to create readonly file: %v", err)
	}

	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}

	// Publish - should handle read-only files
	if err := cli.RunPublish(false, "", false); err != nil {
		t.Fatalf("Failed to publish with readonly files: %v", err)
	}

	env.AssertPackageInDatabase("readonly-pkg", true)
}

// TestPublishPreservesFileMetadata tests that file metadata is preserved
func TestPublishPreservesFileMetadata(t *testing.T) {
	env := setupTest(t)

	// Create package with executable file
	pkgDir := env.CreateTestPackage("metadata-pkg", "1.0.0", map[string]string{
		"script.sh": "#!/bin/sh\necho test",
	})

	// Make it executable
	scriptPath := filepath.Join(pkgDir, "script.sh")
	if err := os.Chmod(scriptPath, 0755); err != nil {
		t.Fatalf("Failed to chmod: %v", err)
	}

	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}

	// Publish
	if err := cli.RunPublish(false, "", false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Verify metadata stored
	pkg, err := env.Database.GetPackageByName("metadata-pkg")
	if err != nil || pkg == nil {
		t.Fatalf("Failed to get package: %v", err)
	}

	files, err := env.Database.GetFilesForPackage(pkg.ID)
	if err != nil {
		t.Fatalf("Failed to get files: %v", err)
	}

	// Find script.sh and check mode
	foundExec := false
	for _, f := range files {
		if f.RelativePath == "script.sh" {
			if f.Mode&0111 != 0 {
				foundExec = true
			}
			break
		}
	}

	if !foundExec {
		t.Error("Executable permission not preserved in database")
	}
}
