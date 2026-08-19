package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/cli"
	"github.com/pedrosousa13/lnpm/internal/workspace"
)

func TestDetectTurborepo(t *testing.T) {
	root := filepath.Join("fixtures", "turborepo")
	ws, err := workspace.Detect(root)
	if err != nil {
		t.Fatalf("Failed to detect workspace: %v", err)
	}
	if ws == nil {
		t.Fatal("Expected workspace, got nil")
	}
	if ws.Type != "npm" && ws.Type != "yarn" {
		t.Errorf("Expected npm or yarn, got %s", ws.Type)
	}

	packages, err := ws.ListPackages()
	if err != nil {
		t.Fatalf("Failed to list packages: %v", err)
	}
	if len(packages) != 3 { // ui, utils, web
		t.Errorf("Expected 3 packages, got %d", len(packages))
	}
}

func TestDetectPNPMWorkspace(t *testing.T) {
	root := filepath.Join("fixtures", "pnpm-workspace")
	ws, err := workspace.Detect(root)
	if err != nil {
		t.Fatalf("Failed to detect workspace: %v", err)
	}
	if ws == nil {
		t.Fatal("Expected workspace, got nil")
	}
	if ws.Type != "pnpm" {
		t.Errorf("Expected pnpm, got %s", ws.Type)
	}

	packages, err := ws.ListPackages()
	if err != nil {
		t.Fatalf("Failed to list packages: %v", err)
	}
	if len(packages) != 3 { // lib-a, lib-b, app1
		t.Errorf("Expected 3 packages, got %d", len(packages))
	}

	// Verify package names
	names := make(map[string]bool)
	for _, pkg := range packages {
		names[pkg.Name] = true
	}
	if !names["@pnpm-test/lib-a"] || !names["@pnpm-test/lib-b"] {
		t.Error("Expected @pnpm-test/lib-a and @pnpm-test/lib-b")
	}
}

func TestDetectNPMWorkspace(t *testing.T) {
	root := filepath.Join("fixtures", "npm-workspace")
	ws, err := workspace.Detect(root)
	if err != nil {
		t.Fatalf("Failed to detect workspace: %v", err)
	}
	if ws == nil {
		t.Fatal("Expected workspace, got nil")
	}

	packages, err := ws.ListPackages()
	if err != nil {
		t.Fatalf("Failed to list packages: %v", err)
	}
	if len(packages) != 2 {
		t.Errorf("Expected 2 packages, got %d", len(packages))
	}
}

func TestDetectNPMWorkspaceWithNegation(t *testing.T) {
	root := filepath.Join("fixtures", "npm-workspace-negation")
	ws, err := workspace.Detect(root)
	if err != nil {
		t.Fatalf("Failed to detect workspace: %v", err)
	}
	if ws == nil {
		t.Fatal("Expected workspace, got nil")
	}

	packages, err := ws.ListPackages()
	if err != nil {
		t.Fatalf("Failed to list packages: %v", err)
	}
	if len(packages) != 1 {
		t.Fatalf("Expected 1 package, got %d: %v", len(packages), packages)
	}
	if packages[0].Name != "@npm-test/package-a" {
		t.Errorf("Expected @npm-test/package-a, got %s", packages[0].Name)
	}
}

func TestDetectYarnWorkspace(t *testing.T) {
	root := filepath.Join("fixtures", "yarn-workspace")
	ws, err := workspace.Detect(root)
	if err != nil {
		t.Fatalf("Failed to detect workspace: %v", err)
	}
	if ws == nil {
		t.Fatal("Expected workspace, got nil")
	}

	packages, err := ws.ListPackages()
	if err != nil {
		t.Fatalf("Failed to list packages: %v", err)
	}
	if len(packages) != 1 {
		t.Errorf("Expected 1 package, got %d", len(packages))
	}
}

func TestNoWorkspace(t *testing.T) {
	ws, err := workspace.Detect("/tmp")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if ws != nil {
		t.Error("Expected nil workspace for /tmp")
	}
}

// --- workspace: dependency specifiers ----------------------------------------
//
// The pnpm-workspace fixture's lib-b depends on its sibling lib-a with
// "workspace:*". npm cannot install that specifier outside the workspace, so
// publish must resolve it to the sibling's real version on the way into the
// store, without touching the developer's own package.json.

// storedPackageJSONPath returns the path of a published package's package.json
// inside the isolated store.
func storedPackageJSONPath(t *testing.T, env *TestEnvironment, name string) string {
	t.Helper()

	pkg, err := env.Database.GetPackageByName(name)
	if err != nil || pkg == nil {
		t.Fatalf("Package %s not found in database: %v", name, err)
	}
	return filepath.Join(pkg.StorePath, "package.json")
}

func TestPublishResolvesWorkspaceDependency(t *testing.T) {
	env := setupTest(t)

	wsDir := env.CopyFixture("pnpm-workspace")
	libBDir := filepath.Join(wsDir, "packages", "lib-b")
	sourcePath := filepath.Join(libBDir, "package.json")

	source, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("Failed to read lib-b package.json: %v", err)
	}
	if !strings.Contains(string(source), `"workspace:*"`) {
		t.Fatalf("Fixture lib-b must depend on lib-a with workspace:*, got:\n%s", source)
	}

	env.chdir(libBDir)
	// Skip validation: fixtures have no built files.
	if err := cli.RunPublish(false, false, false, true); err != nil {
		t.Fatalf("Failed to publish @pnpm-test/lib-b: %v", err)
	}

	// The developer's own package.json must come out byte-identical.
	after, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("Failed to re-read lib-b package.json: %v", err)
	}
	if string(after) != string(source) {
		t.Errorf("Expected the source package.json to be untouched.\nBefore:\n%s\nAfter:\n%s", source, after)
	}

	// The stored package.json carries lib-a's real version, and nothing but the
	// specifier changed - key order and indentation survive the rewrite.
	want := strings.Replace(string(source), `"workspace:*"`, `"1.0.0"`, 1)
	env.AssertFileContent(storedPackageJSONPath(t, env, "@pnpm-test/lib-b"), want)

	// The content hash addresses those rewritten bytes, not the ones on disk.
	env.AssertStoredContentHash("@pnpm-test/lib-b")

	// And that is exactly what a consumer receives.
	projectDir := env.newProject("lib-b-consumer")
	env.addPkg(projectDir, "@pnpm-test/lib-b", false, false)
	env.AssertLinkedFileContent(projectDir, "@pnpm-test/lib-b", "package.json", want)
}

// A package with no workspace: specifiers is stored byte-for-byte as written.
func TestPublishWithoutWorkspaceDependenciesIsUnchanged(t *testing.T) {
	env := setupTest(t)

	wsDir := env.CopyFixture("pnpm-workspace")
	libADir := filepath.Join(wsDir, "packages", "lib-a")

	source, err := os.ReadFile(filepath.Join(libADir, "package.json"))
	if err != nil {
		t.Fatalf("Failed to read lib-a package.json: %v", err)
	}

	env.chdir(libADir)
	if err := cli.RunPublish(false, false, false, true); err != nil {
		t.Fatalf("Failed to publish @pnpm-test/lib-a: %v", err)
	}

	env.AssertFileContent(storedPackageJSONPath(t, env, "@pnpm-test/lib-a"), string(source))
	env.AssertStoredContentHash("@pnpm-test/lib-a")
}

// A workspace: specifier in a package that is not in any workspace has nothing
// to resolve against, and must fail rather than ship the literal specifier.
func TestPublishWorkspaceDependencyOutsideWorkspaceFails(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.CreateTestPackage("orphan-lib", "1.0.0", map[string]string{
		"index.js": "module.exports = 'orphan-lib';",
	})
	env.writeFile(filepath.Join(pkgDir, "package.json"),
		`{"name":"orphan-lib","version":"1.0.0","dependencies":{"some-sibling":"workspace:*"}}`)

	env.chdir(pkgDir)
	err := cli.RunPublish(false, false, false, true)
	if err == nil {
		t.Fatal("Expected publish to fail for a workspace: specifier outside a workspace, got nil")
	}
	if !strings.Contains(err.Error(), "not part of a workspace") {
		t.Errorf("Expected an error explaining the package is not in a workspace, got: %v", err)
	}
	env.AssertPackageInDatabase("orphan-lib", false)
}

// A workspace: specifier naming a package the workspace does not contain must
// fail too - the sibling's version is simply not knowable.
func TestPublishUnknownWorkspaceSiblingFails(t *testing.T) {
	env := setupTest(t)

	wsDir := env.CopyFixture("pnpm-workspace")
	libBDir := filepath.Join(wsDir, "packages", "lib-b")
	env.writeFile(filepath.Join(libBDir, "package.json"),
		`{"name":"@pnpm-test/lib-b","version":"2.0.0","dependencies":{"@pnpm-test/ghost":"workspace:*"}}`)

	env.chdir(libBDir)
	err := cli.RunPublish(false, false, false, true)
	if err == nil {
		t.Fatal("Expected publish to fail for a sibling missing from the workspace, got nil")
	}
	if !strings.Contains(err.Error(), "no such package in the workspace") {
		t.Errorf("Expected an error naming the missing sibling, got: %v", err)
	}
	env.AssertPackageInDatabase("@pnpm-test/lib-b", false)
}

// push re-packs and re-stores, so it has to resolve specifiers exactly as
// publish does or the first push would put the literal specifier back.
func TestPushResolvesWorkspaceDependency(t *testing.T) {
	env := setupTest(t)

	wsDir := env.CopyFixture("pnpm-workspace")
	libBDir := filepath.Join(wsDir, "packages", "lib-b")

	source, err := os.ReadFile(filepath.Join(libBDir, "package.json"))
	if err != nil {
		t.Fatalf("Failed to read lib-b package.json: %v", err)
	}
	want := strings.Replace(string(source), `"workspace:*"`, `"1.0.0"`, 1)

	env.chdir(libBDir)
	if err := cli.RunPublish(false, false, false, true); err != nil {
		t.Fatalf("Failed to publish @pnpm-test/lib-b: %v", err)
	}

	projectDir := env.newProject("lib-b-push-consumer")
	env.addPkg(projectDir, "@pnpm-test/lib-b", false, false)

	// Change the source so push has new content to store.
	env.writeFile(filepath.Join(libBDir, "index.js"), "module.exports = 'lib-b-v2';")
	env.chdir(libBDir)
	if err := cli.RunPush(false); err != nil {
		t.Fatalf("Failed to push @pnpm-test/lib-b: %v", err)
	}

	env.AssertFileContent(storedPackageJSONPath(t, env, "@pnpm-test/lib-b"), want)
	env.AssertStoredContentHash("@pnpm-test/lib-b")
	env.AssertLinkedFileContent(projectDir, "@pnpm-test/lib-b", "package.json", want)
}

// push on a package that was never published delegates to the publish path, so
// it must resolve specifiers there too.
func TestPushUnpublishedResolvesWorkspaceDependency(t *testing.T) {
	env := setupTest(t)

	wsDir := env.CopyFixture("pnpm-workspace")
	libBDir := filepath.Join(wsDir, "packages", "lib-b")

	source, err := os.ReadFile(filepath.Join(libBDir, "package.json"))
	if err != nil {
		t.Fatalf("Failed to read lib-b package.json: %v", err)
	}

	env.chdir(libBDir)
	if err := cli.RunPush(false); err != nil {
		t.Fatalf("Failed to push @pnpm-test/lib-b: %v", err)
	}

	want := strings.Replace(string(source), `"workspace:*"`, `"1.0.0"`, 1)
	env.AssertFileContent(storedPackageJSONPath(t, env, "@pnpm-test/lib-b"), want)
	env.AssertStoredContentHash("@pnpm-test/lib-b")
}
