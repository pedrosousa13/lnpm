package tests

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/cli"
)

// TestCheckCleanProject verifies check passes (nil error) when no lnpm
// references are present in package.json.
func TestCheckCleanProject(t *testing.T) {
	env := setupTest(t)

	projectDir := env.CreateTestPackage("clean-project", "1.0.0", nil)
	env.writePackageJSON(projectDir, map[string]interface{}{
		"name":    "clean-project",
		"version": "1.0.0",
		"dependencies": map[string]interface{}{
			"left-pad": "^1.0.0",
		},
		"devDependencies": map[string]interface{}{
			"typescript": "^5.0.0",
		},
	})
	env.chdir(projectDir)

	if err := cli.RunCheck(); err != nil {
		t.Fatalf("expected check to pass on clean project, got: %v", err)
	}
}

// TestCheckDetectsFileRef verifies check fails when a file:.lnpm/ reference is
// present in dependencies.
func TestCheckDetectsFileRef(t *testing.T) {
	env := setupTest(t)

	projectDir := env.CreateTestPackage("dirty-project", "1.0.0", nil)
	env.writePackageJSON(projectDir, map[string]interface{}{
		"name":    "dirty-project",
		"version": "1.0.0",
		"dependencies": map[string]interface{}{
			"my-lib": "file:.lnpm/my-lib",
		},
	})
	env.chdir(projectDir)

	if err := cli.RunCheck(); err == nil {
		t.Fatal("expected check to fail when a file:.lnpm/ reference is present, got nil")
	}
}

// TestCheckDetectsLinkRef verifies check fails when a link:.lnpm/ reference is
// present in devDependencies.
func TestCheckDetectsLinkRef(t *testing.T) {
	env := setupTest(t)

	projectDir := env.CreateTestPackage("dirty-dev-project", "1.0.0", nil)
	env.writePackageJSON(projectDir, map[string]interface{}{
		"name":    "dirty-dev-project",
		"version": "1.0.0",
		"devDependencies": map[string]interface{}{
			"my-lib": "link:.lnpm/my-lib",
		},
	})
	env.chdir(projectDir)

	if err := cli.RunCheck(); err == nil {
		t.Fatal("expected check to fail when a link:.lnpm/ reference is present, got nil")
	}
}

// TestCheckNoPackageJSON verifies check errors when there is no package.json.
func TestCheckNoPackageJSON(t *testing.T) {
	env := setupTest(t)

	dir := filepath.Join(env.TempDir, "no-pkg")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}
	env.chdir(dir)

	if err := cli.RunCheck(); err == nil {
		t.Fatal("expected check to error when no package.json exists, got nil")
	}
}

// TestCheckCatchesRealAddLink verifies that check detects a reference produced
// by `add --link`, closing the loop between the two features.
func TestCheckCatchesRealAddLink(t *testing.T) {
	env := setupTest(t)

	env.simplePkg("link-lib")
	projectDir := env.newProject("consumer")

	// add --link writes a link:.lnpm/ reference.
	if err := cli.RunAddMultiple([]string{"link-lib"}, false, false, false, true); err != nil {
		t.Fatalf("add --link failed: %v", err)
	}

	env.chdir(projectDir)
	if err := cli.RunCheck(); err == nil {
		t.Fatal("expected check to fail after add --link, got nil")
	}
}
