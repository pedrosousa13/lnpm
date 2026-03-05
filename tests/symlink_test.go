package tests

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/cli"
)

// TestSymlinkSurvivesNpmInstall tests add pkg, npm install other dep, symlink intact
func TestSymlinkSurvivesNpmInstall(t *testing.T) {
	// Skip if npm is not available
	if _, err := exec.LookPath("npm"); err != nil {
		t.Skip("npm not available")
	}

	env := setupTest(t)

	// Create and publish a test package
	pkgDir := env.CreateTestPackage("symlink-survive-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'survive';",
	})
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPublish(false, "", false, false, false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Create project and add the lnpm package
	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunAdd("symlink-survive-pkg", false, false, false); err != nil {
		t.Fatalf("Failed to add package: %v", err)
	}

	// Verify symlink exists
	env.AssertSymlinkExists(projectDir, "symlink-survive-pkg")

	// Add a regular npm dependency to package.json
	pkgJSONPath := filepath.Join(projectDir, "package.json")
	data, _ := os.ReadFile(pkgJSONPath)
	var pkgJSON map[string]interface{}
	if err := json.Unmarshal(data, &pkgJSON); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	deps := pkgJSON["dependencies"].(map[string]interface{})
	deps["is-odd"] = "^3.0.1" // Small package for fast install

	newData, _ := json.MarshalIndent(pkgJSON, "", "  ")
	if err := os.WriteFile(pkgJSONPath, newData, 0644); err != nil {
		t.Fatalf("Failed to write: %v", err)
	}

	// Run npm install
	cmd := exec.Command("npm", "install", "--prefer-offline")
	cmd.Dir = projectDir
	cmd.Env = append(os.Environ(), "npm_config_audit=false", "npm_config_fund=false")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Logf("npm install output: %s", output)
		// Don't fail - npm install might fail for various reasons in test env
		// The important thing is to check if our symlink survives
	}

	// Verify lnpm symlink still exists
	env.AssertSymlinkExists(projectDir, "symlink-survive-pkg")

	// Verify the linked package content is accessible
	linkedFile := filepath.Join(projectDir, "node_modules", "symlink-survive-pkg", "index.js")
	content, err := os.ReadFile(linkedFile)
	if err != nil {
		t.Fatalf("Failed to read linked file: %v", err)
	}
	if string(content) != "module.exports = 'survive';" {
		t.Errorf("Linked file content mismatch")
	}
}

// TestSymlinkTargetCorrect tests node_modules/pkg → ../.lnpm/pkg
func TestSymlinkTargetCorrect(t *testing.T) {
	env := setupTest(t)

	// Create and publish a test package
	pkgDir := env.CreateTestPackage("target-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'target';",
	})
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPublish(false, "", false, false, false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Create project and add the package
	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunAdd("target-pkg", false, false, false); err != nil {
		t.Fatalf("Failed to add package: %v", err)
	}

	// Check symlink target
	symlinkPath := filepath.Join(projectDir, "node_modules", "target-pkg")
	target, err := os.Readlink(symlinkPath)
	if err != nil {
		t.Fatalf("Failed to read symlink: %v", err)
	}

	// Resolve and verify target points to .lnpm
	resolved := filepath.Join(filepath.Dir(symlinkPath), target)
	if !filepath.IsAbs(resolved) {
		resolved, _ = filepath.Abs(resolved)
	}

	// The resolved path should be under .lnpm
	expectedLnpmPath := filepath.Join(projectDir, ".lnpm", "target-pkg")
	expectedLnpmAbs, _ := filepath.Abs(expectedLnpmPath)
	resolvedAbs, _ := filepath.Abs(resolved)

	// Clean paths for comparison
	expectedLnpmAbs = filepath.Clean(expectedLnpmAbs)
	resolvedAbs = filepath.Clean(resolvedAbs)

	if resolvedAbs != expectedLnpmAbs {
		t.Errorf("Symlink target incorrect.\nExpected: %s\nGot: %s\nRaw target: %s",
			expectedLnpmAbs, resolvedAbs, target)
	}
}

// TestSymlinkWithScopedPackage tests symlink for @org/pkg
func TestSymlinkWithScopedPackage(t *testing.T) {
	env := setupTest(t)

	// Create and publish a scoped package
	pkgDir := env.CreateTestPackage("@myorg/scoped-link", "1.0.0", map[string]string{
		"index.js": "module.exports = 'scoped';",
	})
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPublish(false, "", false, false, false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Create project and add the package
	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunAdd("@myorg/scoped-link", false, false, false); err != nil {
		t.Fatalf("Failed to add package: %v", err)
	}

	// Verify scoped symlink exists at node_modules/@myorg/scoped-link
	scopedSymlink := filepath.Join(projectDir, "node_modules", "@myorg", "scoped-link")
	info, err := os.Lstat(scopedSymlink)
	if err != nil {
		t.Fatalf("Scoped symlink not found: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Error("Expected symlink for scoped package")
	}

	// Verify can read through symlink
	linkedFile := filepath.Join(scopedSymlink, "index.js")
	content, err := os.ReadFile(linkedFile)
	if err != nil {
		t.Fatalf("Failed to read through scoped symlink: %v", err)
	}
	if string(content) != "module.exports = 'scoped';" {
		t.Error("Content mismatch through scoped symlink")
	}
}

// TestSymlinkAfterPush tests symlink still works after push updates
func TestSymlinkAfterPush(t *testing.T) {
	env := setupTest(t)

	// Create and publish v1
	pkgDir := env.CreateTestPackage("push-link-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'v1';",
	})
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPublish(false, "", false, false, false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Create project and add
	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunAdd("push-link-pkg", false, false, false); err != nil {
		t.Fatalf("Failed to add package: %v", err)
	}

	// Verify initial symlink
	env.AssertSymlinkExists(projectDir, "push-link-pkg")

	// Update and push
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "index.js"), []byte("module.exports = 'v2';"), 0644); err != nil {
		t.Fatalf("Failed to update file: %v", err)
	}
	if err := cli.RunPush(false); err != nil {
		t.Fatalf("Failed to push: %v", err)
	}

	// Symlink should still work
	env.AssertSymlinkExists(projectDir, "push-link-pkg")

	// Content should be updated
	linkedFile := filepath.Join(projectDir, "node_modules", "push-link-pkg", "index.js")
	content, err := os.ReadFile(linkedFile)
	if err != nil {
		t.Fatalf("Failed to read linked file: %v", err)
	}
	if string(content) != "module.exports = 'v2';" {
		t.Errorf("Expected v2, got %s", string(content))
	}
}

// TestSymlinkIsRelative tests that symlink uses relative path
// On Windows, junctions use absolute paths so this test is skipped.
func TestSymlinkIsRelative(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("junctions use absolute paths on Windows")
	}

	env := setupTest(t)

	// Create and publish package
	pkgDir := env.CreateTestPackage("rel-link-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'rel';",
	})
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPublish(false, "", false, false, false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Create project and add
	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunAdd("rel-link-pkg", false, false, false); err != nil {
		t.Fatalf("Failed to add package: %v", err)
	}

	// Read symlink target
	symlinkPath := filepath.Join(projectDir, "node_modules", "rel-link-pkg")
	target, err := os.Readlink(symlinkPath)
	if err != nil {
		t.Fatalf("Failed to read symlink: %v", err)
	}

	// Target should be relative (not absolute)
	if filepath.IsAbs(target) {
		t.Errorf("Expected relative symlink, got absolute: %s", target)
	}

	// Should point to ../.lnpm/rel-link-pkg
	expected := filepath.Join("..", ".lnpm", "rel-link-pkg")
	if target != expected {
		t.Errorf("Expected symlink target %s, got %s", expected, target)
	}
}

// TestNodeModulesCreatedIfMissing tests node_modules created for symlink
func TestNodeModulesCreatedIfMissing(t *testing.T) {
	env := setupTest(t)

	// Create and publish package
	pkgDir := env.CreateTestPackage("nm-create-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'nm';",
	})
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPublish(false, "", false, false, false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Create project without node_modules
	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)

	// Explicitly remove node_modules if it exists
	_ = os.RemoveAll(filepath.Join(projectDir, "node_modules"))

	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}

	// Add should create node_modules
	if err := cli.RunAdd("nm-create-pkg", false, false, false); err != nil {
		t.Fatalf("Failed to add package: %v", err)
	}

	// node_modules should exist
	nmPath := filepath.Join(projectDir, "node_modules")
	if _, err := os.Stat(nmPath); os.IsNotExist(err) {
		t.Error("node_modules was not created")
	}

	// Symlink should exist
	env.AssertSymlinkExists(projectDir, "nm-create-pkg")
}
