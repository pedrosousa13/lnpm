package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/cli"
)

// TestSymlinkSurvivesNpmInstall tests that an lnpm symlink remains intact after a
// subsequent `npm install` of an unrelated dependency.
func TestSymlinkSurvivesNpmInstall(t *testing.T) {
	if _, err := exec.LookPath("npm"); err != nil {
		t.Skip("npm not available")
	}

	env := setupTest(t)

	_, projectDir := env.publishAndAdd("symlink-survive-pkg")
	env.AssertSymlinkExists(projectDir, "symlink-survive-pkg")

	// Install a real dependency from a vendored tarball. Installing a file path
	// never contacts the registry, so the install either genuinely happens or
	// genuinely fails - it can no longer half-succeed and leave the symlink
	// untouched because npm did nothing. The fixture is `npm pack` run over a
	// package.json naming lnpm-test-dep@1.0.0 plus a one-line index.js.
	tarball := env.FixturePath("tarballs", "lnpm-test-dep-1.0.0.tgz")
	if _, err := os.Stat(tarball); err != nil {
		t.Fatalf("Vendored tarball fixture missing: %v", err)
	}

	cmd := exec.Command("npm", "install", tarball)
	cmd.Dir = projectDir
	// audit and fund are network paths too, so both stay off.
	cmd.Env = append(os.Environ(), "npm_config_audit=false", "npm_config_fund=false")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("npm install failed: %v\n%s", err, output)
	}

	// The install must actually have landed, otherwise the symlink was never
	// threatened and the assertions below prove nothing.
	if _, err := os.Stat(filepath.Join(projectDir, "node_modules", "lnpm-test-dep", "index.js")); err != nil {
		t.Fatalf("npm install did not install the vendored dependency: %v", err)
	}

	env.AssertSymlinkExists(projectDir, "symlink-survive-pkg")
	content, err := os.ReadFile(filepath.Join(projectDir, "node_modules", "symlink-survive-pkg", "index.js"))
	if err != nil {
		t.Fatalf("Failed to read linked file: %v", err)
	}
	if string(content) != "module.exports = 'symlink-survive-pkg';" {
		t.Errorf("Linked file content mismatch: %s", content)
	}
}

// TestSymlinkTargetCorrect tests that node_modules/<pkg> resolves to .lnpm/<pkg>.
func TestSymlinkTargetCorrect(t *testing.T) {
	env := setupTest(t)

	_, projectDir := env.publishAndAdd("target-pkg")

	symlinkPath := filepath.Join(projectDir, "node_modules", "target-pkg")
	target, err := os.Readlink(symlinkPath)
	if err != nil {
		t.Fatalf("Failed to read symlink: %v", err)
	}

	resolved := target
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(filepath.Dir(symlinkPath), target)
	}
	resolvedAbs, _ := filepath.Abs(resolved)
	expectedAbs, _ := filepath.Abs(filepath.Join(projectDir, ".lnpm", "target-pkg"))

	if filepath.Clean(resolvedAbs) != filepath.Clean(expectedAbs) {
		t.Errorf("Symlink target incorrect.\nExpected: %s\nGot: %s\nRaw target: %s",
			filepath.Clean(expectedAbs), filepath.Clean(resolvedAbs), target)
	}
}

// TestSymlinkIsRelative tests that the symlink uses a relative path.
// On Windows, junctions use absolute paths so this test is skipped.
func TestSymlinkIsRelative(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("junctions use absolute paths on Windows")
	}

	env := setupTest(t)

	_, projectDir := env.publishAndAdd("rel-link-pkg")

	target, err := os.Readlink(filepath.Join(projectDir, "node_modules", "rel-link-pkg"))
	if err != nil {
		t.Fatalf("Failed to read symlink: %v", err)
	}
	if filepath.IsAbs(target) {
		t.Errorf("Expected relative symlink, got absolute: %s", target)
	}
	if expected := filepath.Join("..", ".lnpm", "rel-link-pkg"); target != expected {
		t.Errorf("Expected symlink target %s, got %s", expected, target)
	}
}

// TestSymlinkWithScopedPackage tests that scoped packages get a working symlink
// at node_modules/@scope/name.
func TestSymlinkWithScopedPackage(t *testing.T) {
	env := setupTest(t)

	_, projectDir := env.publishAndAdd("@myorg/scoped-link")

	scopedSymlink := filepath.Join(projectDir, "node_modules", "@myorg", "scoped-link")
	info, err := os.Lstat(scopedSymlink)
	if err != nil {
		t.Fatalf("Scoped symlink not found: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Error("Expected symlink for scoped package")
	}

	content, err := os.ReadFile(filepath.Join(scopedSymlink, "index.js"))
	if err != nil {
		t.Fatalf("Failed to read through scoped symlink: %v", err)
	}
	if string(content) != "module.exports = '@myorg/scoped-link';" {
		t.Errorf("Content mismatch through scoped symlink: %s", content)
	}
}

// TestSymlinkAfterPush tests that the symlink still resolves and reflects new
// content after a push.
func TestSymlinkAfterPush(t *testing.T) {
	env := setupTest(t)

	pkgDir, projectDir := env.publishAndAdd("push-link-pkg")
	env.AssertSymlinkExists(projectDir, "push-link-pkg")

	env.chdir(pkgDir)
	env.writeFile(filepath.Join(pkgDir, "index.js"), "module.exports = 'v2';")
	if err := cli.RunPush(false); err != nil {
		t.Fatalf("Failed to push: %v", err)
	}

	env.AssertSymlinkExists(projectDir, "push-link-pkg")
	content, err := os.ReadFile(filepath.Join(projectDir, "node_modules", "push-link-pkg", "index.js"))
	if err != nil {
		t.Fatalf("Failed to read linked file: %v", err)
	}
	if string(content) != "module.exports = 'v2';" {
		t.Errorf("Expected v2, got %s", content)
	}
}

// TestNodeModulesCreatedIfMissing tests that add creates node_modules when absent.
func TestNodeModulesCreatedIfMissing(t *testing.T) {
	env := setupTest(t)

	env.simplePkg("nm-create-pkg")
	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	_ = os.RemoveAll(filepath.Join(projectDir, "node_modules"))

	env.addPkg(projectDir, "nm-create-pkg", false, false)

	if _, err := os.Stat(filepath.Join(projectDir, "node_modules")); os.IsNotExist(err) {
		t.Error("node_modules was not created")
	}
	env.AssertSymlinkExists(projectDir, "nm-create-pkg")
}
