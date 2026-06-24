package tests

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/cli"
)

// publishAllFixture copies a workspace fixture into the test's temp dir, chdirs
// into the copy, and runs `publish --all` against the isolated store. Fixtures
// are never published from the committed tree, and ~/.lnpm is never touched.
func publishAllFixture(t *testing.T, env *TestEnvironment, fixture string) {
	t.Helper()

	dir := env.CopyFixture(fixture)
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Failed to change to fixture directory %s: %v", dir, err)
	}

	// skip validation since test fixtures don't have built files
	if err := cli.RunPublish(false, true, false, true); err != nil {
		t.Fatalf("Failed to publish all packages: %v", err)
	}
}

// assertPublished verifies each named package exists in the isolated store DB.
func assertPublished(t *testing.T, env *TestEnvironment, names ...string) {
	t.Helper()

	for _, name := range names {
		pkg, err := env.Database.GetPackageByName(name)
		if err != nil || pkg == nil {
			t.Errorf("Expected %s to be published", name)
		}
	}
}

// TestPublishAllWorkspaces tests `publish --all` across every supported
// workspace layout. Each row is a distinct workspace type whose fixture must
// yield the expected published package names in the isolated store.
func TestPublishAllWorkspaces(t *testing.T) {
	cases := []struct {
		name     string
		fixture  string
		expected []string
	}{
		// lnpm publishes all workspace packages, including the web app.
		{"turborepo", "turborepo", []string{"@turborepo-test/ui", "@turborepo-test/utils", "turborepo-web-app"}},
		{"pnpm", "pnpm-workspace", []string{"@pnpm-test/lib-a", "@pnpm-test/lib-b"}},
		{"npm", "npm-workspace", []string{"@npm-test/package-a", "@npm-test/package-b"}},
		{"yarn", "yarn-workspace", []string{"@yarn-test/library"}},
		{"nx", "nx", []string{"@nx-test/feature-auth"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := setupTest(t)
			publishAllFixture(t, env, tc.fixture)
			assertPublished(t, env, tc.expected...)
		})
	}
}

// TestNxAddInternalDependency tests that adding a package to an internal Nx package
// doesn't modify the top-level workspace package.json
func TestNxAddInternalDependency(t *testing.T) {
	env := setupTest(t)

	// Copy both fixtures into the test temp dir so the committed tree is untouched.
	nxDir := env.CopyFixture("nx")
	npmDir := env.CopyFixture("npm-workspace")

	// Publish the npm package we'll add later.
	pkgADir := filepath.Join(npmDir, "packages", "package-a")
	if err := os.Chdir(pkgADir); err != nil {
		t.Fatalf("Failed to change to package-a directory: %v", err)
	}
	if err := cli.RunPublish(true, false, false, false); err != nil {
		t.Fatalf("Failed to publish package-a: %v", err)
	}

	// Publish the nx sub-package.
	featureAuthDir := filepath.Join(nxDir, "libs", "feature-auth")
	if err := os.Chdir(featureAuthDir); err != nil {
		t.Fatalf("Failed to change to feature-auth directory: %v", err)
	}
	if err := cli.RunPublish(true, false, false, false); err != nil {
		t.Fatalf("Failed to publish feature-auth package: %v", err)
	}

	// Snapshot the root and sub-package package.json before adding.
	originalRootData, err := os.ReadFile(filepath.Join(nxDir, "package.json"))
	if err != nil {
		t.Fatalf("Failed to read root package.json: %v", err)
	}
	originalSubData, err := os.ReadFile(filepath.Join(featureAuthDir, "package.json"))
	if err != nil {
		t.Fatalf("Failed to read sub-package package.json: %v", err)
	}

	// Add the published package to the sub-package.
	if err := os.Chdir(featureAuthDir); err != nil {
		t.Fatalf("Failed to change to feature-auth directory: %v", err)
	}
	if err := cli.RunAdd("@npm-test/package-a", false, false, false); err != nil {
		t.Fatalf("Failed to add package: %v", err)
	}

	// The sub-package package.json should have changed.
	updatedSubData, err := os.ReadFile(filepath.Join(featureAuthDir, "package.json"))
	if err != nil {
		t.Fatalf("Failed to read updated sub-package package.json: %v", err)
	}
	if string(updatedSubData) == string(originalSubData) {
		t.Error("Expected sub-package package.json to be updated")
	}

	// The top-level workspace package.json must be untouched.
	updatedRootData, err := os.ReadFile(filepath.Join(nxDir, "package.json"))
	if err != nil {
		t.Fatalf("Failed to read updated root package.json: %v", err)
	}
	if string(updatedRootData) != string(originalRootData) {
		t.Error("Expected top-level package.json to remain unchanged")
	}
}
