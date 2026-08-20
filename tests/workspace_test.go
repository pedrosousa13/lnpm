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

// A member the user asked to publish that will not parse must stop the whole
// run: publishing the rest would report success having published less than
// `--all` asked for.
func TestPublishAllUnparseableMemberFailsAndPublishesNothing(t *testing.T) {
	env := setupTest(t)

	wsDir := env.CopyFixture("npm-workspace")
	brokenRel := filepath.Join("packages", "package-b", "package.json")
	env.writeFile(filepath.Join(wsDir, brokenRel), `{"name":"@npm-test/package-b",}`)

	env.chdir(wsDir)
	err := cli.RunPublish(false, true, false, true)
	if err == nil {
		t.Fatal("Expected publish --all to fail for the unparseable member, got nil")
	}
	// Assert on the workspace-relative tail, not the absolute path: RunPublish
	// builds its paths from os.Getwd, which resolves symlinks and 8.3 short
	// names, so it does not always spell the temp root the way t.TempDir did.
	// The tail still tells the two members apart, which is the point.
	if !strings.Contains(err.Error(), brokenRel) {
		t.Errorf("Expected the error to name %s, got: %v", brokenRel, err)
	}
	intactRel := filepath.Join("packages", "package-a", "package.json")
	if strings.Contains(err.Error(), intactRel) {
		t.Errorf("Expected the error to name only the broken member, but it names %s: %v", intactRel, err)
	}

	env.AssertPackageInDatabase("@npm-test/package-a", false)
	env.AssertPackageInDatabase("@npm-test/package-b", false)
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
// The workspace-deps fixture exists for this and nothing else: its lib depends
// on its sibling util with "workspace:*", at 2.3.0. npm cannot install that
// specifier outside the workspace, so publish must resolve it to the sibling's
// real version on the way into the store, without touching the developer's own
// package.json. The other workspace fixtures stay free of workspace: specifiers
// so the tests that only care about workspace discovery do not depend on this
// rewrite working.

func TestPublishResolvesWorkspaceDependency(t *testing.T) {
	env := setupTest(t)

	wsDir := env.CopyFixture("workspace-deps")
	libDir := filepath.Join(wsDir, "packages", "lib")
	sourcePath := filepath.Join(libDir, "package.json")

	source, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("Failed to read lib package.json: %v", err)
	}
	if !strings.Contains(string(source), `"workspace:*"`) {
		t.Fatalf("Fixture lib must depend on util with workspace:*, got:\n%s", source)
	}

	env.chdir(libDir)
	// Skip validation: fixtures have no built files.
	if err := cli.RunPublish(false, false, false, true); err != nil {
		t.Fatalf("Failed to publish @ws-deps/lib: %v", err)
	}

	// The developer's own package.json must come out byte-identical.
	after, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("Failed to re-read lib package.json: %v", err)
	}
	if string(after) != string(source) {
		t.Errorf("Expected the source package.json to be untouched.\nBefore:\n%s\nAfter:\n%s", source, after)
	}

	// The stored package.json carries util's real version, and nothing but the
	// specifier changed - key order and indentation survive the rewrite.
	want := strings.Replace(string(source), `"workspace:*"`, `"2.3.0"`, 1)
	env.AssertFileContent(env.storedPackageJSONPath("@ws-deps/lib"), want)

	// The content hash addresses those rewritten bytes, not the ones on disk.
	env.AssertStoredContentHash("@ws-deps/lib")

	// And that is exactly what a consumer receives.
	projectDir := env.newProject("lib-consumer")
	env.addPkg(projectDir, "@ws-deps/lib", false, false)
	env.AssertLinkedFileContent(projectDir, "@ws-deps/lib", "package.json", want)
}

// A package with no workspace: specifiers is stored byte-for-byte as written.
func TestPublishWithoutWorkspaceDependenciesIsUnchanged(t *testing.T) {
	env := setupTest(t)

	wsDir := env.CopyFixture("workspace-deps")
	utilDir := filepath.Join(wsDir, "packages", "util")

	source, err := os.ReadFile(filepath.Join(utilDir, "package.json"))
	if err != nil {
		t.Fatalf("Failed to read util package.json: %v", err)
	}

	env.chdir(utilDir)
	if err := cli.RunPublish(false, false, false, true); err != nil {
		t.Fatalf("Failed to publish @ws-deps/util: %v", err)
	}

	env.AssertFileContent(env.storedPackageJSONPath("@ws-deps/util"), string(source))
	env.AssertStoredContentHash("@ws-deps/util")
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

	wsDir := env.CopyFixture("workspace-deps")
	libDir := filepath.Join(wsDir, "packages", "lib")
	env.writeFile(filepath.Join(libDir, "package.json"),
		`{"name":"@ws-deps/lib","version":"1.0.0","dependencies":{"@ws-deps/ghost":"workspace:*"}}`)

	env.chdir(libDir)
	err := cli.RunPublish(false, false, false, true)
	if err == nil {
		t.Fatal("Expected publish to fail for a sibling missing from the workspace, got nil")
	}
	if !strings.Contains(err.Error(), "no such package in the workspace") {
		t.Errorf("Expected an error naming the missing sibling, got: %v", err)
	}
	env.AssertPackageInDatabase("@ws-deps/lib", false)
}

// push re-packs and re-stores, so it has to resolve specifiers exactly as
// publish does or the first push would put the literal specifier back.
func TestPushResolvesWorkspaceDependency(t *testing.T) {
	env := setupTest(t)

	wsDir := env.CopyFixture("workspace-deps")
	libDir := filepath.Join(wsDir, "packages", "lib")

	source, err := os.ReadFile(filepath.Join(libDir, "package.json"))
	if err != nil {
		t.Fatalf("Failed to read lib package.json: %v", err)
	}
	want := strings.Replace(string(source), `"workspace:*"`, `"2.3.0"`, 1)

	env.chdir(libDir)
	if err := cli.RunPublish(false, false, false, true); err != nil {
		t.Fatalf("Failed to publish @ws-deps/lib: %v", err)
	}

	projectDir := env.newProject("lib-push-consumer")
	env.addPkg(projectDir, "@ws-deps/lib", false, false)

	// Change the source so push has new content to store.
	env.writeFile(filepath.Join(libDir, "index.js"), "module.exports = 'lib-v2';")
	env.chdir(libDir)
	if err := cli.RunPush(false); err != nil {
		t.Fatalf("Failed to push @ws-deps/lib: %v", err)
	}

	env.AssertFileContent(env.storedPackageJSONPath("@ws-deps/lib"), want)
	env.AssertStoredContentHash("@ws-deps/lib")
	env.AssertLinkedFileContent(projectDir, "@ws-deps/lib", "package.json", want)
}

// push on a package that was never published delegates to the publish path, so
// it must resolve specifiers there too.
func TestPushUnpublishedResolvesWorkspaceDependency(t *testing.T) {
	env := setupTest(t)

	wsDir := env.CopyFixture("workspace-deps")
	libDir := filepath.Join(wsDir, "packages", "lib")

	source, err := os.ReadFile(filepath.Join(libDir, "package.json"))
	if err != nil {
		t.Fatalf("Failed to read lib package.json: %v", err)
	}

	env.chdir(libDir)
	if err := cli.RunPush(false); err != nil {
		t.Fatalf("Failed to push @ws-deps/lib: %v", err)
	}

	want := strings.Replace(string(source), `"workspace:*"`, `"2.3.0"`, 1)
	env.AssertFileContent(env.storedPackageJSONPath("@ws-deps/lib"), want)
	env.AssertStoredContentHash("@ws-deps/lib")
}
