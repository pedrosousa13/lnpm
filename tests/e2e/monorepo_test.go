package e2e

import (
	"path/filepath"
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
