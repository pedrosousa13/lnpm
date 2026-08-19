package e2e

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// libManifest renders a publishable package.json at an explicit version, so a
// lib can be republished as a new version between runs. pkgManifest in
// depth_test.go always renders 1.0.0, which a pull test cannot move off.
func libManifest(name, version string) string {
	return fmt.Sprintf("{\n  \"name\": %s,\n  \"version\": %s,\n  \"main\": \"index.js\"\n}\n",
		jsString(name), jsString(version))
}

// TestMultiPackagePull proves the whole point of `lnpm pull` end to end, with
// the real binary and real node: two libs are published and added to one app,
// both are then republished WITHOUT --push (so the app keeps resolving the old
// contents), and a single argument-less `lnpm pull` in the app brings both up
// to date through the existing node_modules symlinks.
func TestMultiPackagePull(t *testing.T) {
	t.Parallel()
	if !nodeAvailable {
		t.Skip("node not available; skipping real-resolution e2e test")
	}

	const pkgA, pkgB = "pull-lib-a", "pull-lib-b"
	libA := makePkgDir(t, pkgA, `module.exports = "a-v1";`+"\n")
	libB := makePkgDir(t, pkgB, `module.exports = "b-v1";`+"\n")
	app := makeAppDir(t, "pull-app")
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

	// Republish both at 2.0.0 without --push: the app must still see v1.
	writeFile(t, filepath.Join(libA, "package.json"), libManifest(pkgA, "2.0.0"))
	writeFile(t, filepath.Join(libA, "index.js"), `module.exports = "a-v2";`+"\n")
	writeFile(t, filepath.Join(libB, "package.json"), libManifest(pkgB, "2.0.0"))
	writeFile(t, filepath.Join(libB, "index.js"), `module.exports = "b-v2";`+"\n")
	runLNPM(t, store, libA, "publish")
	runLNPM(t, store, libB, "publish")

	if got := runNode(t, app, script); got != "a-v1|b-v1" {
		t.Fatalf("publish without --push must leave the app stale, but node resolved %q", got)
	}

	// One pull, no arguments: every linked package moves to the store's version.
	out := runLNPM(t, store, app, "pull")
	for _, want := range []string{
		"Pulling " + pkgA + "... updated 1.0.0 -> 2.0.0",
		"Pulling " + pkgB + "... updated 1.0.0 -> 2.0.0",
		"Pulled 2 package(s)",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected pull output to contain %q, got:\n%s", want, out)
		}
	}

	if got := runNode(t, app, script); got != "a-v2|b-v2" {
		t.Fatalf("after pull, expected node to resolve both libs to \"a-v2|b-v2\", got %q", got)
	}

	// The symlinks the app resolves through survived the relink, and
	// package.json was never touched.
	for _, pkg := range []string{pkgA, pkgB} {
		assertSymlink(t, filepath.Join(app, "node_modules", pkg))
		assertDepValue(t, app, pkg, "file:.lnpm/"+pkg)
	}

	// A second pull has nothing left to do.
	if out := runLNPM(t, store, app, "pull"); !strings.Contains(out, "already up to date") {
		t.Fatalf("expected a second pull to report everything already up to date, got:\n%s", out)
	}
}
