package tests

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pedrosousa13/lnpm/internal/cli"
)

// TestPushBasic tests basic push functionality
func TestPushBasic(t *testing.T) {
	env := setupTest(t)

	// Create and publish a package
	pkgDir := env.CreateTestPackage("push-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'v1';",
	})
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPublish(false, false, false, false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Create a project and add the package
	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunAdd("push-pkg", false, false, false); err != nil {
		t.Fatalf("Failed to add package: %v", err)
	}

	// Modify package
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "index.js"), []byte("module.exports = 'v2';"), 0644); err != nil {
		t.Fatalf("Failed to modify file: %v", err)
	}

	// Push changes
	if err := cli.RunPush(false); err != nil {
		t.Fatalf("Failed to push: %v", err)
	}

	// Verify package was updated in project
	linkedFile := filepath.Join(projectDir, ".lnpm", "push-pkg", "index.js")
	content, err := os.ReadFile(linkedFile)
	if err != nil {
		t.Fatalf("Failed to read linked file: %v", err)
	}
	if string(content) != "module.exports = 'v2';" {
		t.Errorf("Expected updated content, got %s", string(content))
	}
}

// TestPushNoChanges tests push when no changes exist
func TestPushNoChanges(t *testing.T) {
	env := setupTest(t)

	// Create and publish a package
	pkgDir := env.CreateTestPackage("nochange-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'test';",
	})
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPublish(false, false, false, false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Push without changes (should be noop)
	if err := cli.RunPush(false); err != nil {
		t.Fatalf("Failed to push: %v", err)
	}
}

// TestPushForceFlag tests push with --force flag
func TestPushForceFlag(t *testing.T) {
	env := setupTest(t)

	// Create and publish a package
	pkgDir := env.CreateTestPackage("force-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'test';",
	})
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPublish(false, false, false, false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Create a project and add the package
	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunAdd("force-pkg", false, false, false); err != nil {
		t.Fatalf("Failed to add package: %v", err)
	}

	// Push with force (even with no changes)
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPush(false); err != nil {
		t.Fatalf("Failed to force push: %v", err)
	}
}

// TestPushMultipleProjects tests pushing to multiple linked projects
func TestPushMultipleProjects(t *testing.T) {
	env := setupTest(t)

	// Create and publish a package
	pkgDir := env.CreateTestPackage("shared-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'v1';",
	})
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPublish(false, false, false, false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Create multiple projects and add package to each
	projects := []string{"project-1", "project-2", "project-3"}
	projectDirs := make(map[string]string)
	for _, name := range projects {
		projectDir := env.CreateTestPackage(name, "1.0.0", nil)
		projectDirs[name] = projectDir
		if err := os.Chdir(projectDir); err != nil {
			t.Fatalf("Failed to chdir to %s: %v", name, err)
		}
		if err := cli.RunAdd("shared-pkg", false, false, false); err != nil {
			t.Fatalf("Failed to add package to %s: %v", name, err)
		}
	}

	// Modify package
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "index.js"), []byte("module.exports = 'v2';"), 0644); err != nil {
		t.Fatalf("Failed to modify file: %v", err)
	}

	// Push to all projects
	if err := cli.RunPush(false); err != nil {
		t.Fatalf("Failed to push: %v", err)
	}

	// Verify all projects received update
	for name, dir := range projectDirs {
		linkedFile := filepath.Join(dir, ".lnpm", "shared-pkg", "index.js")
		content, err := os.ReadFile(linkedFile)
		if err != nil {
			t.Errorf("Failed to read linked file in %s: %v", name, err)
			continue
		}
		if string(content) != "module.exports = 'v2';" {
			t.Errorf("Project %s: expected updated content, got %s", name, string(content))
		}
	}
}

// TestPushUnpublishedPackage tests pushing a package that hasn't been published yet
func TestPushUnpublishedPackage(t *testing.T) {
	env := setupTest(t)

	// Create package but don't publish
	pkgDir := env.CreateTestPackage("unpublished-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'test';",
	})
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}

	// Push should publish it
	if err := cli.RunPush(false); err != nil {
		t.Fatalf("Failed to push unpublished package: %v", err)
	}

	// Verify package is now in database
	env.AssertPackageInDatabase("unpublished-pkg", true)
}

// TestPushVersionUpdate tests pushing with version change
func TestPushVersionUpdate(t *testing.T) {
	env := setupTest(t)

	// Create and publish v1.0.0
	pkgDir := env.CreateTestPackage("version-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'v1';",
	})
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPublish(false, false, false, false); err != nil {
		t.Fatalf("Failed to publish v1: %v", err)
	}

	// Create project and add package
	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunAdd("version-pkg", false, false, false); err != nil {
		t.Fatalf("Failed to add package: %v", err)
	}

	// Update version to 2.0.0
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	pkgJSONPath := filepath.Join(pkgDir, "package.json")
	if err := os.WriteFile(pkgJSONPath, []byte(`{"name":"version-pkg","version":"2.0.0"}`), 0644); err != nil {
		t.Fatalf("Failed to update version: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "index.js"), []byte("module.exports = 'v2';"), 0644); err != nil {
		t.Fatalf("Failed to modify file: %v", err)
	}

	// Push new version
	if err := cli.RunPush(false); err != nil {
		t.Fatalf("Failed to push v2: %v", err)
	}

	// Verify project has new version
	linkedFile := filepath.Join(projectDir, ".lnpm", "version-pkg", "index.js")
	content, err := os.ReadFile(linkedFile)
	if err != nil {
		t.Fatalf("Failed to read linked file: %v", err)
	}
	if string(content) != "module.exports = 'v2';" {
		t.Errorf("Expected v2 content, got %s", string(content))
	}
}

// TestPushNoLinkedProjects tests pushing when no projects are linked
func TestPushNoLinkedProjects(t *testing.T) {
	env := setupTest(t)

	// Create and publish a package
	pkgDir := env.CreateTestPackage("standalone-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'test';",
	})
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPublish(false, false, false, false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Modify and push without any linked projects
	if err := os.WriteFile(filepath.Join(pkgDir, "index.js"), []byte("module.exports = 'updated';"), 0644); err != nil {
		t.Fatalf("Failed to modify file: %v", err)
	}
	if err := cli.RunPush(false); err != nil {
		t.Fatalf("Failed to push: %v", err)
	}
}

// TestPushConcurrentSafe tests that concurrent pushes don't cause race conditions
func TestPushConcurrentSafe(t *testing.T) {
	env := setupTest(t)

	// Create and publish a package
	pkgDir := env.CreateTestPackage("concurrent-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'v1';",
	})
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPublish(false, false, false, false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Create multiple projects
	for i := 0; i < 5; i++ {
		projectDir := env.CreateTestPackage("project-"+string(rune('a'+i)), "1.0.0", nil)
		if err := os.Chdir(projectDir); err != nil {
			t.Fatalf("Failed to chdir: %v", err)
		}
		if err := cli.RunAdd("concurrent-pkg", false, false, false); err != nil {
			t.Fatalf("Failed to add package: %v", err)
		}
	}

	// Modify package
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "index.js"), []byte("module.exports = 'v2';"), 0644); err != nil {
		t.Fatalf("Failed to modify file: %v", err)
	}

	// Push should handle concurrent linking safely
	if err := cli.RunPush(false); err != nil {
		t.Fatalf("Failed to push: %v", err)
	}
}

// TestPushWithAddedFiles tests pushing when new files are added
func TestPushWithAddedFiles(t *testing.T) {
	env := setupTest(t)

	// Create and publish initial version
	pkgDir := env.CreateTestPackage("addfiles-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'test';",
	})
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPublish(false, false, false, false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Create project and add package
	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunAdd("addfiles-pkg", false, false, false); err != nil {
		t.Fatalf("Failed to add package: %v", err)
	}

	// Add new files to package
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	libDir := filepath.Join(pkgDir, "lib")
	if err := os.MkdirAll(libDir, 0755); err != nil {
		t.Fatalf("Failed to create lib dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(libDir, "utils.js"), []byte("exports.util = () => 'util';"), 0644); err != nil {
		t.Fatalf("Failed to write new file: %v", err)
	}

	// Push with new files
	if err := cli.RunPush(false); err != nil {
		t.Fatalf("Failed to push: %v", err)
	}

	// Verify new file exists in project
	linkedFile := filepath.Join(projectDir, ".lnpm", "addfiles-pkg", "lib", "utils.js")
	if _, err := os.Stat(linkedFile); err != nil {
		t.Errorf("New file not found in linked project: %v", err)
	}
}

// TestPushAfterDelay tests that push detects changes after a time delay
func TestPushAfterDelay(t *testing.T) {
	env := setupTest(t)

	// Create and publish a package
	pkgDir := env.CreateTestPackage("delay-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'v1';",
	})
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPublish(false, false, false, false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Create project and add package
	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunAdd("delay-pkg", false, false, false); err != nil {
		t.Fatalf("Failed to add package: %v", err)
	}

	// Wait a bit, then modify
	time.Sleep(100 * time.Millisecond)
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "index.js"), []byte("module.exports = 'v2';"), 0644); err != nil {
		t.Fatalf("Failed to modify file: %v", err)
	}

	// Push should detect changes
	if err := cli.RunPush(false); err != nil {
		t.Fatalf("Failed to push: %v", err)
	}

	// Verify update was pushed
	linkedFile := filepath.Join(projectDir, ".lnpm", "delay-pkg", "index.js")
	content, err := os.ReadFile(linkedFile)
	if err != nil {
		t.Fatalf("Failed to read linked file: %v", err)
	}
	if string(content) != "module.exports = 'v2';" {
		t.Errorf("Expected updated content, got %s", string(content))
	}
}
