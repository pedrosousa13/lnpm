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
	defer os.Chdir(oldWd)

	fixtureDir := filepath.Join("fixtures", "turborepo")
	if err := os.Chdir(fixtureDir); err != nil {
		t.Fatalf("Failed to change to fixture directory: %v", err)
	}

	// Clean up any existing database
	os.RemoveAll(filepath.Join(os.Getenv("HOME"), ".lnpm"))

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
	defer os.Chdir(oldWd)

	fixtureDir := filepath.Join("fixtures", "pnpm-workspace")
	if err := os.Chdir(fixtureDir); err != nil {
		t.Fatalf("Failed to change to fixture directory: %v", err)
	}

	// Clean up any existing database
	os.RemoveAll(filepath.Join(os.Getenv("HOME"), ".lnpm"))

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
	defer os.Chdir(oldWd)

	fixtureDir := filepath.Join("fixtures", "npm-workspace")
	if err := os.Chdir(fixtureDir); err != nil {
		t.Fatalf("Failed to change to fixture directory: %v", err)
	}

	// Clean up any existing database
	os.RemoveAll(filepath.Join(os.Getenv("HOME"), ".lnpm"))

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
	defer os.Chdir(oldWd)

	fixtureDir := filepath.Join("fixtures", "yarn-workspace")
	if err := os.Chdir(fixtureDir); err != nil {
		t.Fatalf("Failed to change to fixture directory: %v", err)
	}

	// Clean up any existing database
	os.RemoveAll(filepath.Join(os.Getenv("HOME"), ".lnpm"))

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