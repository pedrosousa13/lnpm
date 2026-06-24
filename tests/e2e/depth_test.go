package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// These "depth" tests complement the monorepo_test.go layout matrix. Where the
// monorepo tests prove resolution works across pnpm/turbo/nx/npm-yarn fixtures,
// these prove the harder behaviors of the workflow itself: push fanning out to
// multiple consumers, remove fully tearing a link down, resolution without any
// workspace manifest, and several distinct packages coexisting in one app.
//
// They build their lib/app dirs programmatically (no fixtures) and reuse the
// shared helpers in helpers_test.go plus jsString from monorepo_test.go.

// pkgManifest renders a minimal publishable package.json (has a main entry).
func pkgManifest(name string) string {
	return fmt.Sprintf("{\n  \"name\": %s,\n  \"version\": \"1.0.0\",\n  \"main\": \"index.js\"\n}\n", jsString(name))
}

// appManifest renders a minimal consumer package.json (private, no deps).
func appManifest(name string) string {
	return fmt.Sprintf("{\n  \"name\": %s,\n  \"version\": \"1.0.0\",\n  \"private\": true\n}\n", jsString(name))
}

// makePkgDir creates a fresh temp dir holding a publishable package: a
// package.json and an index.js exporting body.
func makePkgDir(t *testing.T, name, body string) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "package.json"), pkgManifest(name))
	writeFile(t, filepath.Join(dir, "index.js"), body)
	return dir
}

// makeAppDir creates a fresh temp dir holding a private consumer package.json.
func makeAppDir(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "package.json"), appManifest(name))
	return dir
}

// requireScript returns a node -e script that prints the value require(pkg)
// resolves to, so the test can assert which version the app actually loaded.
func requireScript(pkg string) string {
	return "process.stdout.write(require(" + jsString(pkg) + "))"
}

// runNodeExpectError runs `node -e <script>` and fails the test unless node
// exits non-zero. Used to prove a package is truly gone after remove.
func runNodeExpectError(t *testing.T, dir, script string) {
	t.Helper()
	cmd := exec.Command("node", "-e", script)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("expected node -e (dir=%s) to fail, but it succeeded:\n%s", dir, out)
	}
}

// assertNoSymlink fails the test unless nothing exists at path.
func assertNoSymlink(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("expected nothing at %s, but it still exists (err=%v)", path, err)
	}
}

// assertDepAbsent fails the test if pkg appears in dependencies or
// devDependencies of package.json at projectDir.
func assertDepAbsent(t *testing.T, projectDir, pkg string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(projectDir, "package.json"))
	if err != nil {
		t.Fatalf("failed to read package.json in %s: %v", projectDir, err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("failed to parse package.json in %s: %v", projectDir, err)
	}
	for _, field := range []string{"dependencies", "devDependencies"} {
		if deps, ok := manifest[field].(map[string]any); ok {
			if _, found := deps[pkg]; found {
				t.Fatalf("expected %s to be absent from package.json (%s), but it is present: %s", pkg, projectDir, data)
			}
		}
	}
}

// TestMultiConsumerPushFanout proves one published lib can be linked into two
// independent apps, and a single `push` propagates a source change to both.
func TestMultiConsumerPushFanout(t *testing.T) {
	t.Parallel()
	if !nodeAvailable {
		t.Skip("node not available; skipping real-resolution e2e test")
	}

	const pkg = "fanout-lib"
	libDir := makePkgDir(t, pkg, `module.exports = "lib-v1";`+"\n")
	app1 := makeAppDir(t, "fanout-app-1")
	app2 := makeAppDir(t, "fanout-app-2")
	store := newStore(t)

	runLNPM(t, store, libDir, "publish")
	runLNPM(t, store, app1, "add", pkg)
	runLNPM(t, store, app2, "add", pkg)

	script := requireScript(pkg)
	for _, app := range []string{app1, app2} {
		assertSymlink(t, filepath.Join(app, "node_modules", pkg))
		assertDepValue(t, app, pkg, "file:.lnpm/"+pkg)
		if got := runNode(t, app, script); got != "lib-v1" {
			t.Fatalf("expected node in %s to resolve %q to \"lib-v1\", got %q", app, pkg, got)
		}
	}

	// One push must update every linked consumer.
	writeFile(t, filepath.Join(libDir, "index.js"), `module.exports = "lib-v2";`+"\n")
	runLNPM(t, store, libDir, "push")
	for _, app := range []string{app1, app2} {
		if got := runNode(t, app, script); got != "lib-v2" {
			t.Fatalf("after push, expected node in %s to resolve %q to \"lib-v2\", got %q", app, pkg, got)
		}
	}
}

// TestRemoveTearsDownLink proves `lnpm remove` deletes the node_modules symlink,
// drops the file:.lnpm dependency from package.json, and leaves the app unable
// to resolve the package again.
func TestRemoveTearsDownLink(t *testing.T) {
	t.Parallel()
	if !nodeAvailable {
		t.Skip("node not available; skipping real-resolution e2e test")
	}

	const pkg = "remove-lib"
	libDir := makePkgDir(t, pkg, `module.exports = "lib-v1";`+"\n")
	app := makeAppDir(t, "remove-app")
	store := newStore(t)

	runLNPM(t, store, libDir, "publish")
	runLNPM(t, store, app, "add", pkg)

	link := filepath.Join(app, "node_modules", pkg)
	assertSymlink(t, link)
	assertDepValue(t, app, pkg, "file:.lnpm/"+pkg)
	if got := runNode(t, app, requireScript(pkg)); got != "lib-v1" {
		t.Fatalf("expected node to resolve %q to \"lib-v1\" before remove, got %q", pkg, got)
	}

	runLNPM(t, store, app, "remove", pkg)

	assertNoSymlink(t, link)
	assertDepAbsent(t, app, pkg)
	runNodeExpectError(t, app, requireScript(pkg))
}

// TestPlainProjectResolution proves the full publish/add/push/resolve flow works
// for standalone dirs with no workspace manifest at all (not a monorepo).
func TestPlainProjectResolution(t *testing.T) {
	t.Parallel()
	if !nodeAvailable {
		t.Skip("node not available; skipping real-resolution e2e test")
	}

	const pkg = "plain-lib"
	libDir := makePkgDir(t, pkg, `module.exports = "lib-v1";`+"\n")
	app := makeAppDir(t, "plain-app")
	store := newStore(t)

	runLNPM(t, store, libDir, "publish")
	runLNPM(t, store, app, "add", pkg)

	assertSymlink(t, filepath.Join(app, "node_modules", pkg))
	assertDepValue(t, app, pkg, "file:.lnpm/"+pkg)
	script := requireScript(pkg)
	if got := runNode(t, app, script); got != "lib-v1" {
		t.Fatalf("expected node to resolve %q to \"lib-v1\", got %q", pkg, got)
	}

	writeFile(t, filepath.Join(libDir, "index.js"), `module.exports = "lib-v2";`+"\n")
	runLNPM(t, store, libDir, "push")
	if got := runNode(t, app, script); got != "lib-v2" {
		t.Fatalf("after push, expected node to resolve %q to \"lib-v2\", got %q", pkg, got)
	}
}

// TestMultipleLibsOneApp proves two distinct published packages can both be
// linked into a single app (via one `add` call) and coexist at resolve time.
func TestMultipleLibsOneApp(t *testing.T) {
	t.Parallel()
	if !nodeAvailable {
		t.Skip("node not available; skipping real-resolution e2e test")
	}

	const pkgA, pkgB = "multi-lib-a", "multi-lib-b"
	libA := makePkgDir(t, pkgA, `module.exports = "a-v1";`+"\n")
	libB := makePkgDir(t, pkgB, `module.exports = "b-v1";`+"\n")
	app := makeAppDir(t, "multi-app")
	store := newStore(t)

	runLNPM(t, store, libA, "publish")
	runLNPM(t, store, libB, "publish")
	runLNPM(t, store, app, "add", pkgA, pkgB)

	for _, pkg := range []string{pkgA, pkgB} {
		assertSymlink(t, filepath.Join(app, "node_modules", pkg))
		assertDepValue(t, app, pkg, "file:.lnpm/"+pkg)
	}

	script := fmt.Sprintf(`process.stdout.write(require(%s)+"|"+require(%s))`, jsString(pkgA), jsString(pkgB))
	if got := runNode(t, app, script); got != "a-v1|b-v1" {
		t.Fatalf("expected node to resolve both libs to \"a-v1|b-v1\", got %q", got)
	}
}
