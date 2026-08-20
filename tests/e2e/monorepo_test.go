package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// monorepoCase describes one monorepo tool's fixture layout.
type monorepoCase struct {
	// fixture is the directory under fixtures/ to copy.
	fixture string
	// libDir is the lib package path relative to the workspace root.
	libDir string
	// appDir is the consumer app path relative to the workspace root.
	appDir string
	// pkgName is the published library's package name (may be scoped).
	pkgName string
}

// runMonorepoCase exercises the full real E2E flow for one monorepo layout:
//
//	publish -> add -> assert symlink + package.json -> node resolves lib-v1
//	-> edit lib -> push -> node resolves lib-v2
//
// Every lnpm invocation runs the compiled binary with cmd.Dir set, and every
// resolution check shells out to real node, so this proves a consumer app
// actually loads the linked package through the node_modules symlink and that
// `lnpm push` propagates source changes.
func runMonorepoCase(t *testing.T, c monorepoCase) {
	t.Helper()

	if !nodeAvailable {
		t.Skip("node not available; skipping real-resolution e2e test")
	}

	root := copyFixture(t, c.fixture)
	store := newStore(t)

	libDir := filepath.Join(root, c.libDir)
	appDir := filepath.Join(root, c.appDir)

	// b. publish the library into the isolated store.
	runLNPM(t, store, libDir, "publish")

	// c. add the library into the consumer app.
	runLNPM(t, store, appDir, "add", c.pkgName)

	// d. the node_modules entry must exist and be a symlink, and package.json
	//    must reference the lnpm file: link.
	assertSymlink(t, filepath.Join(appDir, "node_modules", filepath.FromSlash(c.pkgName)))
	assertDepValue(t, appDir, c.pkgName, "file:.lnpm/"+c.pkgName)

	// e. real node must resolve the package through the symlink and load v1.
	script := "process.stdout.write(require(" + jsString(c.pkgName) + "))"
	if got := runNode(t, appDir, script); got != "lib-v1" {
		t.Fatalf("expected node to resolve %q to \"lib-v1\", got %q", c.pkgName, got)
	}

	// f. change the lib source, push, and confirm node now loads v2 in the
	//    consumer app — proving propagation through the existing symlink.
	writeFile(t, filepath.Join(libDir, "index.js"), `module.exports = "lib-v2";`+"\n")
	runLNPM(t, store, libDir, "push")
	if got := runNode(t, appDir, script); got != "lib-v2" {
		t.Fatalf("after push, expected node to resolve %q to \"lib-v2\", got %q", c.pkgName, got)
	}
}

// jsString renders a Go string as a double-quoted JS string literal so package
// names are safely embedded in the `node -e` script.
func jsString(s string) string {
	out := make([]rune, 0, len(s)+2)
	out = append(out, '"')
	for _, r := range s {
		switch r {
		case '"', '\\':
			out = append(out, '\\', r)
		default:
			out = append(out, r)
		}
	}
	out = append(out, '"')
	return string(out)
}

func TestPnpmWorkspaceResolution(t *testing.T) {
	t.Parallel()
	runMonorepoCase(t, monorepoCase{
		fixture: "pnpm",
		libDir:  filepath.Join("packages", "lib"),
		appDir:  filepath.Join("apps", "app"),
		pkgName: "@pnpm-fix/lib",
	})
}

func TestTurborepoResolution(t *testing.T) {
	t.Parallel()
	runMonorepoCase(t, monorepoCase{
		fixture: "turborepo",
		libDir:  filepath.Join("packages", "lib"),
		appDir:  filepath.Join("apps", "app"),
		pkgName: "@turbo-fix/lib",
	})
}

func TestNxResolution(t *testing.T) {
	t.Parallel()
	runMonorepoCase(t, monorepoCase{
		fixture: "nx",
		libDir:  filepath.Join("libs", "lib"),
		appDir:  filepath.Join("apps", "app"),
		pkgName: "nx-fix-lib",
	})
}

func TestNpmYarnWorkspaceResolution(t *testing.T) {
	t.Parallel()
	runMonorepoCase(t, monorepoCase{
		fixture: "npm-yarn",
		libDir:  filepath.Join("packages", "lib"),
		appDir:  filepath.Join("apps", "app"),
		pkgName: "npm-yarn-fix-lib",
	})
}

// TestWorkspaceDependencyResolution publishes two packages of one workspace,
// where the library depends on its sibling through "workspace:^", into a
// consumer project that sits outside the workspace entirely.
//
// npm cannot install a workspace: specifier there, so the manifest lnpm links
// must carry the sibling's real version instead — and real node must still
// resolve the library and, through it, the sibling.
func TestWorkspaceDependencyResolution(t *testing.T) {
	t.Parallel()

	if !nodeAvailable {
		t.Skip("node not available; skipping real-resolution e2e test")
	}

	root := copyFixture(t, "workspace-deps")
	store := newStore(t)

	libDir := filepath.Join(root, "monorepo", "packages", "lib")
	utilDir := filepath.Join(root, "monorepo", "packages", "util")
	consumerDir := filepath.Join(root, "consumer")

	runLNPM(t, store, utilDir, "publish")
	runLNPM(t, store, libDir, "publish")

	runLNPM(t, store, consumerDir, "add", "@ws-deps/util")
	runLNPM(t, store, consumerDir, "add", "@ws-deps/lib")

	// The linked manifest must be installable by npm: the sibling specifier is
	// resolved, and no workspace: protocol survives anywhere in it.
	linked := filepath.Join(consumerDir, ".lnpm", "@ws-deps", "lib")
	assertDepValue(t, linked, "@ws-deps/util", "^2.3.0")
	manifest, err := os.ReadFile(filepath.Join(linked, "package.json"))
	if err != nil {
		t.Fatalf("failed to read linked package.json: %v", err)
	}
	if strings.Contains(string(manifest), "workspace:") {
		t.Fatalf("linked package.json still carries a workspace: specifier:\n%s", manifest)
	}

	// The developer's own manifest keeps the workspace: specifier.
	source, err := os.ReadFile(filepath.Join(libDir, "package.json"))
	if err != nil {
		t.Fatalf("failed to read source package.json: %v", err)
	}
	if !strings.Contains(string(source), `"workspace:^"`) {
		t.Fatalf("expected the source package.json to keep workspace:^, got:\n%s", source)
	}

	// node must resolve the library and the sibling it requires.
	script := `process.stdout.write(require("@ws-deps/lib"))`
	if got := runNode(t, consumerDir, script); got != "lib-v1+util-v1" {
		t.Fatalf("expected node to resolve @ws-deps/lib to \"lib-v1+util-v1\", got %q", got)
	}
}

// TestPublishAllRejectsMalformedWorkspacePattern proves that a workspace glob
// that will not parse stops `publish --all` outright instead of publishing the
// package the config tried to exclude. The store is checked afterwards so a
// silent partial publish cannot pass as a failure.
func TestPublishAllRejectsMalformedWorkspacePattern(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newStore(t)

	writeFile(t, filepath.Join(root, "package.json"),
		`{"name":"root","private":true,"workspaces":["packages/*","!packages/[internal"]}`)
	writeFile(t, filepath.Join(root, "packages", "public-api", "package.json"),
		`{"name":"e2e-public-api","version":"1.0.0"}`)
	writeFile(t, filepath.Join(root, "packages", "internal-secret", "package.json"),
		`{"name":"e2e-internal-secret","version":"1.0.0"}`)

	out, err := runLNPMErr(t, store, root, "publish", "--all")
	if err == nil {
		t.Fatalf("expected publish --all to exit non-zero for a malformed pattern, output:\n%s", out)
	}
	if !strings.Contains(out, "packages/[internal") {
		t.Errorf("expected the failure to name the offending pattern, output:\n%s", out)
	}

	status := runLNPM(t, store, root, "status")
	for _, name := range []string{"e2e-internal-secret", "e2e-public-api"} {
		if strings.Contains(status, name) {
			t.Errorf("expected nothing published, but %s is in the store:\n%s", name, status)
		}
	}
}
