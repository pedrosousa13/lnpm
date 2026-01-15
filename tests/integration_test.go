package tests

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/cli"
	"github.com/pedrosousa13/lnpm/internal/db"
)

// TestPublishAllTurborepo tests publishing all packages in a Turborepo workspace
func TestPublishAllTurborepo(t *testing.T) {
	// Change to turborepo fixture directory
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()

	fixtureDir := filepath.Join("fixtures", "turborepo")
	if err := os.Chdir(fixtureDir); err != nil {
		t.Fatalf("Failed to change to fixture directory: %v", err)
	}

	// Clean up any existing database
	_ = os.RemoveAll(filepath.Join(os.Getenv("HOME"), ".lnpm"))

	// Test publish --all
	err := cli.RunPublish(false, "", true)
	if err != nil {
		t.Fatalf("Failed to publish all packages: %v", err)
	}

	// Verify packages were published
	database, err := db.GetDB()
	if err != nil {
		t.Fatalf("Failed to get database: %v", err)
	}

	// Check that packages were published
	pkg1, err := database.GetPackageByName("@turborepo-test/ui")
	if err != nil || pkg1 == nil {
		t.Error("Expected @turborepo-test/ui to be published")
	}

	pkg2, err := database.GetPackageByName("@turborepo-test/utils")
	if err != nil || pkg2 == nil {
		t.Error("Expected @turborepo-test/utils to be published")
	}

	// turborepo-web-app should also be published (lnpm publishes all workspace packages)
	pkg3, err := database.GetPackageByName("turborepo-web-app")
	if err != nil || pkg3 == nil {
		t.Error("Expected turborepo-web-app to be published")
	}
}

// TestPublishAllPNPM tests publishing all packages in a PNPM workspace
func TestPublishAllPNPM(t *testing.T) {
	// Change to pnpm fixture directory
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()

	fixtureDir := filepath.Join("fixtures", "pnpm-workspace")
	if err := os.Chdir(fixtureDir); err != nil {
		t.Fatalf("Failed to change to fixture directory: %v", err)
	}

	// Clean up any existing database
	_ = os.RemoveAll(filepath.Join(os.Getenv("HOME"), ".lnpm"))

	// Test publish --all
	err := cli.RunPublish(false, "", true)
	if err != nil {
		t.Fatalf("Failed to publish all packages: %v", err)
	}

	// Verify packages were published
	database, err := db.GetDB()
	if err != nil {
		t.Fatalf("Failed to get database: %v", err)
	}

	pkg1, err := database.GetPackageByName("@pnpm-test/lib-a")
	if err != nil || pkg1 == nil {
		t.Error("Expected @pnpm-test/lib-a to be published")
	}

	pkg2, err := database.GetPackageByName("@pnpm-test/lib-b")
	if err != nil || pkg2 == nil {
		t.Error("Expected @pnpm-test/lib-b to be published")
	}
}

// TestPublishAllNPM tests publishing all packages in an NPM workspace
func TestPublishAllNPM(t *testing.T) {
	// Change to npm fixture directory
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()

	fixtureDir := filepath.Join("fixtures", "npm-workspace")
	if err := os.Chdir(fixtureDir); err != nil {
		t.Fatalf("Failed to change to fixture directory: %v", err)
	}

	// Clean up any existing database
	_ = os.RemoveAll(filepath.Join(os.Getenv("HOME"), ".lnpm"))

	// Test publish --all
	err := cli.RunPublish(false, "", true)
	if err != nil {
		t.Fatalf("Failed to publish all packages: %v", err)
	}

	// Verify packages were published
	database, err := db.GetDB()
	if err != nil {
		t.Fatalf("Failed to get database: %v", err)
	}

	pkg1, err := database.GetPackageByName("@npm-test/package-a")
	if err != nil || pkg1 == nil {
		t.Error("Expected @npm-test/package-a to be published")
	}

	pkg2, err := database.GetPackageByName("@npm-test/package-b")
	if err != nil || pkg2 == nil {
		t.Error("Expected @npm-test/package-b to be published")
	}
}

// TestPublishAllYarn tests publishing all packages in a Yarn workspace
func TestPublishAllYarn(t *testing.T) {
	// Change to yarn fixture directory
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()

	fixtureDir := filepath.Join("fixtures", "yarn-workspace")
	if err := os.Chdir(fixtureDir); err != nil {
		t.Fatalf("Failed to change to fixture directory: %v", err)
	}

	// Clean up any existing database
	_ = os.RemoveAll(filepath.Join(os.Getenv("HOME"), ".lnpm"))

	// Test publish --all
	err := cli.RunPublish(false, "", true)
	if err != nil {
		t.Fatalf("Failed to publish all packages: %v", err)
	}

	// Verify packages were published
	database, err := db.GetDB()
	if err != nil {
		t.Fatalf("Failed to get database: %v", err)
	}

	pkg, err := database.GetPackageByName("@yarn-test/library")
	if err != nil || pkg == nil {
		t.Error("Expected @yarn-test/library to be published")
	}
}

// TestPublishAllNx tests publishing all packages in an Nx workspace
func TestPublishAllNx(t *testing.T) {
	// Change to nx fixture directory
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()

	fixtureDir := filepath.Join("fixtures", "nx")
	if err := os.Chdir(fixtureDir); err != nil {
		t.Fatalf("Failed to change to fixture directory: %v", err)
	}

	// Clean up any existing database and .lnpm directories
	_ = os.RemoveAll(filepath.Join(os.Getenv("HOME"), ".lnpm"))
	_ = os.RemoveAll(".lnpm")
	_ = os.RemoveAll(filepath.Join("libs", "feature-auth", ".lnpm"))

	// Test publish --all
	err := cli.RunPublish(false, "", true)
	if err != nil {
		t.Fatalf("Failed to publish all packages: %v", err)
	}

	// Verify packages were published
	database, err := db.GetDB()
	if err != nil {
		t.Fatalf("Failed to get database: %v", err)
	}

	pkg, err := database.GetPackageByName("@nx-test/feature-auth")
	if err != nil || pkg == nil {
		t.Error("Expected @nx-test/feature-auth to be published")
	}
}

// TestNxAddInternalDependency tests that adding a package to an internal Nx package
// doesn't modify the top-level workspace package.json
func TestNxAddInternalDependency(t *testing.T) {
	var err error

	// Save original working directory
	originalWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(originalWd) }()

	nxFixtureDir := filepath.Join("fixtures", "nx")

	// Use a separate store directory for this test to avoid conflicts
	testStoreDir := filepath.Join(os.TempDir(), "lnpm-test-nx")
	defer func() { _ = os.RemoveAll(testStoreDir) }()
	_ = os.Setenv("LNPM_STORE", testStoreDir)
	defer func() { _ = os.Unsetenv("LNPM_STORE") }()

	// Clean up .lnpm directories in fixtures but preserve global ~/.lnpm for published packages
	_ = os.RemoveAll(filepath.Join(nxFixtureDir, ".lnpm"))
	_ = os.RemoveAll(filepath.Join(nxFixtureDir, "libs", "feature-auth", ".lnpm"))

	// Reset the sub-package package.json to original state
	originalSubPkgJSON := `{
  "name": "@nx-test/feature-auth",
  "version": "1.0.0",
  "main": "src/index.js"
}`
	subPkgJSONPath := filepath.Join(nxFixtureDir, "libs", "feature-auth", "package.json")
	if err := os.WriteFile(subPkgJSONPath, []byte(originalSubPkgJSON), 0644); err != nil {
		t.Fatalf("Failed to reset sub-package package.json: %v", err)
	}

	// First, publish the npm package we need for the test
	// Let's use the npm workspace package-a for this

	// Always publish the package fresh for this test - clean everything first
	_ = os.RemoveAll(testStoreDir)

	// Go back to original directory
	if err := os.Chdir(originalWd); err != nil {
		t.Fatalf("Failed to change back to original directory: %v", err)
	}

	// Publish package-a first
	npmPkgDir := filepath.Join("fixtures", "npm-workspace", "packages", "package-a")
	if err := os.Chdir(npmPkgDir); err != nil {
		t.Fatalf("Failed to change to package-a directory: %v", err)
	}

	err = cli.RunPublish(true, "", false)
	if err != nil {
		t.Fatalf("Failed to publish package-a: %v", err)
	}

	// Go back to original directory
	if err := os.Chdir(originalWd); err != nil {
		t.Fatalf("Failed to change back to original directory: %v", err)
	}

	// Now publish the feature-auth package so we have something to add
	subPkgDir := filepath.Join(nxFixtureDir, "libs", "feature-auth")
	if err := os.Chdir(subPkgDir); err != nil {
		t.Fatalf("Failed to change to sub-package directory: %v", err)
	}

	err = cli.RunPublish(true, "", false)
	if err != nil {
		t.Fatalf("Failed to publish feature-auth package: %v", err)
	}

	// Go back to original directory
	if err := os.Chdir(originalWd); err != nil {
		t.Fatalf("Failed to change back to original directory: %v", err)
	}

	// Change to nx workspace root
	if err := os.Chdir(nxFixtureDir); err != nil {
		t.Fatalf("Failed to change to nx workspace: %v", err)
	}

	// Read the original top-level package.json
	rootPkgJSONPath := "package.json"
	originalRootData, err := os.ReadFile(rootPkgJSONPath)
	if err != nil {
		t.Fatalf("Failed to read root package.json: %v", err)
	}

	// Change to the sub-package directory
	subPkgPath := filepath.Join("libs", "feature-auth")
	if err := os.Chdir(subPkgPath); err != nil {
		t.Fatalf("Failed to change to sub-package directory: %v", err)
	}

	// Read the original sub-package package.json
	subPkgJSONPath = "package.json"
	originalSubData, err := os.ReadFile(subPkgJSONPath)
	if err != nil {
		t.Fatalf("Failed to read sub-package package.json: %v", err)
	}

	// Add the published package to the sub-package
	err = cli.RunAdd("@npm-test/package-a", false, false)
	if err != nil {
		t.Fatalf("Failed to add package: %v", err)
	}

	// Verify the sub-package package.json was updated
	updatedSubData, err := os.ReadFile(subPkgJSONPath)
	if err != nil {
		t.Fatalf("Failed to read updated sub-package package.json: %v", err)
	}

	if string(updatedSubData) == string(originalSubData) {
		t.Error("Expected sub-package package.json to be updated")
	}

	// Verify the top-level package.json was NOT updated
	updatedRootData, err := os.ReadFile(filepath.Join("..", "..", "package.json"))
	if err != nil {
		t.Fatalf("Failed to read updated root package.json: %v", err)
	}

	if string(updatedRootData) != string(originalRootData) {
		t.Error("Expected top-level package.json to remain unchanged")
	}
}