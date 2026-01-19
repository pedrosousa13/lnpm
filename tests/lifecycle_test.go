package tests

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/cli"
)

// TestStripLifecycleScriptsRemovesPrepare verifies prepare/prepublish stripped from stored pkg
func TestStripLifecycleScriptsRemovesPrepare(t *testing.T) {
	env := setupTest(t)

	// Use pkg-with-hooks fixture
	pkgDir := env.CopyFixture("pkg-with-hooks")
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}

	// Publish
	if err := cli.RunPublish(false, "", false, true, false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Get package from database
	pkg, err := env.Database.GetPackageByName("pkg-with-hooks")
	if err != nil || pkg == nil {
		t.Fatalf("Failed to get package: %v", err)
	}

	// Read stored package.json
	storedPkgJSON := filepath.Join(pkg.StorePath, "package.json")
	data, err := os.ReadFile(storedPkgJSON)
	if err != nil {
		t.Fatalf("Failed to read stored package.json: %v", err)
	}

	var pkgJSON map[string]interface{}
	if err := json.Unmarshal(data, &pkgJSON); err != nil {
		t.Fatalf("Failed to parse package.json: %v", err)
	}

	scripts, ok := pkgJSON["scripts"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected scripts field in package.json")
	}

	// Verify prepare and prepublish are stripped
	if _, exists := scripts["prepare"]; exists {
		t.Error("Expected 'prepare' script to be stripped, but it exists")
	}
	if _, exists := scripts["prepublish"]; exists {
		t.Error("Expected 'prepublish' script to be stripped, but it exists")
	}
}

// TestStripLifecycleScriptsPreservesOthers verifies build/test/postinstall kept
func TestStripLifecycleScriptsPreservesOthers(t *testing.T) {
	env := setupTest(t)

	// Use pkg-with-hooks fixture
	pkgDir := env.CopyFixture("pkg-with-hooks")
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}

	// Publish
	if err := cli.RunPublish(false, "", false, true, false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Get package from database
	pkg, err := env.Database.GetPackageByName("pkg-with-hooks")
	if err != nil || pkg == nil {
		t.Fatalf("Failed to get package: %v", err)
	}

	// Read stored package.json
	storedPkgJSON := filepath.Join(pkg.StorePath, "package.json")
	data, err := os.ReadFile(storedPkgJSON)
	if err != nil {
		t.Fatalf("Failed to read stored package.json: %v", err)
	}

	var pkgJSON map[string]interface{}
	if err := json.Unmarshal(data, &pkgJSON); err != nil {
		t.Fatalf("Failed to parse package.json: %v", err)
	}

	scripts, ok := pkgJSON["scripts"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected scripts field in package.json")
	}

	// Verify other scripts are preserved
	env.AssertScriptExists(pkg.StorePath, "pkg-with-hooks", "build")
	env.AssertScriptExists(pkg.StorePath, "pkg-with-hooks", "test")
	env.AssertScriptExists(pkg.StorePath, "pkg-with-hooks", "postinstall")

	// Double check directly
	if _, exists := scripts["build"]; !exists {
		t.Error("Expected 'build' script to be preserved")
	}
	if _, exists := scripts["test"]; !exists {
		t.Error("Expected 'test' script to be preserved")
	}
	if _, exists := scripts["postinstall"]; !exists {
		t.Error("Expected 'postinstall' script to be preserved")
	}
}

// TestPublishRunsPrepareBeforeStripping verifies prepare runs in source, then stripped
func TestPublishRunsPrepareBeforeStripping(t *testing.T) {
	env := setupTest(t)

	// Create package with prepare script that creates a file
	pkgDir := env.CreateTestPackageWithScripts("prepare-pkg", "1.0.0",
		map[string]string{
			"index.js": "module.exports = 'test';",
		},
		map[string]string{
			"prepare": "echo 'prepared' > prepared.txt",
		},
	)

	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}

	// Publish (skipHooks=false to run prepare)
	if err := cli.RunPublish(false, "", false, false, false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Get package from database
	pkg, err := env.Database.GetPackageByName("prepare-pkg")
	if err != nil || pkg == nil {
		t.Fatalf("Failed to get package: %v", err)
	}

	// Verify prepare script is stripped from stored version
	env.AssertScriptMissing(pkg.StorePath, "prepare-pkg", "prepare")
}

// TestPublishWithHuskyPrepare tests publishing package with husky-like prepare
func TestPublishWithHuskyPrepare(t *testing.T) {
	env := setupTest(t)

	// Create package with husky-like prepare that would fail
	pkgDir := env.CreateTestPackageWithScripts("husky-pkg", "1.0.0",
		map[string]string{
			"index.js": "module.exports = 'husky';",
		},
		map[string]string{
			"prepare": "husky install", // Would fail if husky not installed
		},
	)

	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}

	// Publish with skipHooks=true (simulates what user would do)
	if err := cli.RunPublish(false, "", false, true, false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Get package from database
	pkg, err := env.Database.GetPackageByName("husky-pkg")
	if err != nil || pkg == nil {
		t.Fatalf("Failed to get package: %v", err)
	}

	// Verify prepare is stripped (so consumer npm install won't fail)
	env.AssertScriptMissing(pkg.StorePath, "husky-pkg", "prepare")
}

// TestAddedPackageHasNoLifecycleScripts verifies .lnpm copy has scripts stripped
func TestAddedPackageHasNoLifecycleScripts(t *testing.T) {
	env := setupTest(t)

	// Use pkg-with-hooks fixture
	pkgDir := env.CopyFixture("pkg-with-hooks")
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}

	// Publish
	if err := cli.RunPublish(false, "", false, true, false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Create project and add package
	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}

	if err := cli.RunAdd("pkg-with-hooks", false, false, false); err != nil {
		t.Fatalf("Failed to add package: %v", err)
	}

	// Read package.json from .lnpm directory
	lnpmPkgJSON := filepath.Join(projectDir, ".lnpm", "pkg-with-hooks", "package.json")
	data, err := os.ReadFile(lnpmPkgJSON)
	if err != nil {
		t.Fatalf("Failed to read .lnpm package.json: %v", err)
	}

	var pkgJSON map[string]interface{}
	if err := json.Unmarshal(data, &pkgJSON); err != nil {
		t.Fatalf("Failed to parse package.json: %v", err)
	}

	scripts, _ := pkgJSON["scripts"].(map[string]interface{})

	// Verify lifecycle scripts are stripped
	if _, exists := scripts["prepare"]; exists {
		t.Error("Expected 'prepare' script to be stripped from .lnpm copy")
	}
	if _, exists := scripts["prepublish"]; exists {
		t.Error("Expected 'prepublish' script to be stripped from .lnpm copy")
	}
}
