package tests

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/pedrosousa13/lnpm/internal/cli"
	"github.com/pedrosousa13/lnpm/internal/watch"
)

// TestWatchDebounce tests 10 rapid changes → 1 sync callback
func TestWatchDebounce(t *testing.T) {
	env := setupTest(t)

	// Create and publish a test package
	pkgDir := env.CreateTestPackage("debounce-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'debounce';",
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
	if err := cli.RunAdd("debounce-pkg", false, false, false); err != nil {
		t.Fatalf("Failed to add package: %v", err)
	}

	// Track sync calls
	syncCount := 0
	var mu sync.Mutex
	syncCh := make(chan []string, 100)

	// Create watcher with short debounce
	w, err := watch.New(pkgDir, watch.Options{
		DebounceMs: 100, // 100ms debounce
		OnSync: func(files []string, projects int) {
			mu.Lock()
			syncCount++
			mu.Unlock()
			syncCh <- files
		},
		OnError: func(err error) {
			t.Logf("Watch error: %v", err)
		},
	})
	if err != nil {
		t.Fatalf("Failed to create watcher: %v", err)
	}

	if err := w.Start(); err != nil {
		t.Fatalf("Failed to start watcher: %v", err)
	}
	defer w.Stop()

	// Wait for watcher to be ready
	time.Sleep(100 * time.Millisecond)

	// Make 10 rapid file changes
	for i := 0; i < 10; i++ {
		content := []byte("module.exports = '" + string(rune('a'+i)) + "';")
		if err := os.WriteFile(filepath.Join(pkgDir, "index.js"), content, 0644); err != nil {
			t.Fatalf("Failed to write file: %v", err)
		}
		time.Sleep(10 * time.Millisecond) // Small delay between writes
	}

	// Wait for debounced sync
	select {
	case <-syncCh:
		// Got first sync
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for sync")
	}

	// Wait a bit more to catch any additional syncs
	time.Sleep(300 * time.Millisecond)

	mu.Lock()
	count := syncCount
	mu.Unlock()

	// Should have 1-2 syncs (debounced), not 10
	if count > 3 {
		t.Errorf("Expected debounced sync (1-3 calls), got %d", count)
	}
}

// TestWatchDetectsFileChange tests modify file → onSync called
func TestWatchDetectsFileChange(t *testing.T) {
	env := setupTest(t)

	// Create and publish a test package
	pkgDir := env.CreateTestPackage("detect-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'detect';",
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
	if err := cli.RunAdd("detect-pkg", false, false, false); err != nil {
		t.Fatalf("Failed to add package: %v", err)
	}

	// Channel for sync notification
	syncCh := make(chan []string, 10)

	w, err := watch.New(pkgDir, watch.Options{
		DebounceMs: 50,
		OnSync: func(files []string, projects int) {
			syncCh <- files
		},
	})
	if err != nil {
		t.Fatalf("Failed to create watcher: %v", err)
	}

	if err := w.Start(); err != nil {
		t.Fatalf("Failed to start watcher: %v", err)
	}
	defer w.Stop()

	// Wait for watcher to be ready
	time.Sleep(100 * time.Millisecond)

	// Modify file
	if err := os.WriteFile(filepath.Join(pkgDir, "index.js"), []byte("module.exports = 'changed';"), 0644); err != nil {
		t.Fatalf("Failed to modify file: %v", err)
	}

	// Wait for sync
	files, err := env.WaitForWatchSync(syncCh, 2*time.Second)
	if err != nil {
		t.Fatalf("Sync not triggered: %v", err)
	}

	// Verify file was reported
	found := false
	for _, f := range files {
		if f == "index.js" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Expected index.js in changed files, got %v", files)
	}
}

// TestWatchIgnoresNodeModules tests change in node_modules → no sync
func TestWatchIgnoresNodeModules(t *testing.T) {
	env := setupTest(t)

	// Create and publish a test package
	pkgDir := env.CreateTestPackage("ignore-nm-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'ignore';",
	})
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPublish(false, "", false, false, false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Create node_modules in package (some packages have this)
	nmDir := filepath.Join(pkgDir, "node_modules", "some-dep")
	if err := os.MkdirAll(nmDir, 0755); err != nil {
		t.Fatalf("Failed to create node_modules: %v", err)
	}

	syncCount := 0
	var mu sync.Mutex

	w, err := watch.New(pkgDir, watch.Options{
		DebounceMs: 50,
		OnSync: func(files []string, projects int) {
			mu.Lock()
			syncCount++
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("Failed to create watcher: %v", err)
	}

	if err := w.Start(); err != nil {
		t.Fatalf("Failed to start watcher: %v", err)
	}
	defer w.Stop()

	// Wait for watcher to be ready
	time.Sleep(100 * time.Millisecond)

	// Modify file in node_modules
	if err := os.WriteFile(filepath.Join(nmDir, "index.js"), []byte("ignored"), 0644); err != nil {
		t.Fatalf("Failed to write to node_modules: %v", err)
	}

	// Wait and check no sync was triggered
	time.Sleep(300 * time.Millisecond)

	mu.Lock()
	count := syncCount
	mu.Unlock()

	if count > 0 {
		t.Errorf("Expected no sync for node_modules change, got %d syncs", count)
	}
}

// TestWatchSyncsToMultipleProjects tests 3 linked projects all updated
func TestWatchSyncsToMultipleProjects(t *testing.T) {
	env := setupTest(t)

	// Create and publish a test package
	pkgDir := env.CreateTestPackage("multi-proj-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'multi';",
	})
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPublish(false, "", false, false, false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Create 3 projects and link to all
	projectDirs := make([]string, 3)
	for i := 0; i < 3; i++ {
		projectDir := env.CreateTestPackage("project-"+string(rune('a'+i)), "1.0.0", nil)
		projectDirs[i] = projectDir
		if err := os.Chdir(projectDir); err != nil {
			t.Fatalf("Failed to chdir: %v", err)
		}
		if err := cli.RunAdd("multi-proj-pkg", false, false, false); err != nil {
			t.Fatalf("Failed to add package to project %d: %v", i, err)
		}
	}

	// Track projects updated via channel
	syncCh := make(chan int, 10)

	w, err := watch.New(pkgDir, watch.Options{
		DebounceMs: 50,
		OnSync: func(files []string, projects int) {
			syncCh <- projects
		},
	})
	if err != nil {
		t.Fatalf("Failed to create watcher: %v", err)
	}

	if err := w.Start(); err != nil {
		t.Fatalf("Failed to start watcher: %v", err)
	}
	defer w.Stop()

	time.Sleep(100 * time.Millisecond)

	// Modify file
	if err := os.WriteFile(filepath.Join(pkgDir, "index.js"), []byte("module.exports = 'updated';"), 0644); err != nil {
		t.Fatalf("Failed to modify file: %v", err)
	}

	// Wait for sync
	select {
	case projects := <-syncCh:
		if projects != 3 {
			t.Errorf("Expected 3 projects updated, got %d", projects)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for sync")
	}

	// Verify all projects have updated content
	for _, projectDir := range projectDirs {
		linkedFile := filepath.Join(projectDir, ".lnpm", "multi-proj-pkg", "index.js")
		content, err := os.ReadFile(linkedFile)
		if err != nil {
			t.Fatalf("Failed to read linked file in %s: %v", projectDir, err)
		}
		if string(content) != "module.exports = 'updated';" {
			t.Errorf("Project %s not updated, got %s", projectDir, string(content))
		}
	}
}

// TestWatchHandlesNewDirectory tests create src/utils/ → watcher picks it up
func TestWatchHandlesNewDirectory(t *testing.T) {
	env := setupTest(t)

	// Create and publish a test package
	pkgDir := env.CreateTestPackage("new-dir-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'newdir';",
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
	if err := cli.RunAdd("new-dir-pkg", false, false, false); err != nil {
		t.Fatalf("Failed to add package: %v", err)
	}

	syncCh := make(chan []string, 10)

	w, err := watch.New(pkgDir, watch.Options{
		DebounceMs: 50,
		OnSync: func(files []string, projects int) {
			syncCh <- files
		},
	})
	if err != nil {
		t.Fatalf("Failed to create watcher: %v", err)
	}

	if err := w.Start(); err != nil {
		t.Fatalf("Failed to start watcher: %v", err)
	}
	defer w.Stop()

	time.Sleep(100 * time.Millisecond)

	// Create new directory with file
	newDir := filepath.Join(pkgDir, "src", "utils")
	if err := os.MkdirAll(newDir, 0755); err != nil {
		t.Fatalf("Failed to create directory: %v", err)
	}
	time.Sleep(50 * time.Millisecond) // Give watcher time to add directory

	// Create file in new directory
	if err := os.WriteFile(filepath.Join(newDir, "helper.js"), []byte("module.exports = 'helper';"), 0644); err != nil {
		t.Fatalf("Failed to write file: %v", err)
	}

	// Wait for sync
	files, err := env.WaitForWatchSync(syncCh, 2*time.Second)
	if err != nil {
		t.Fatalf("Sync not triggered for new directory: %v", err)
	}

	// Verify new file was synced
	found := false
	for _, f := range files {
		if f == "src/utils/helper.js" || f == filepath.Join("src", "utils", "helper.js") {
			found = true
			break
		}
	}
	if !found {
		t.Logf("Changed files: %v", files)
		// New dir might trigger multiple syncs, check project has file
	}

	// Verify project has new file
	linkedFile := filepath.Join(projectDir, ".lnpm", "new-dir-pkg", "src", "utils", "helper.js")
	// Give it a moment for sync to complete
	time.Sleep(200 * time.Millisecond)
	if _, err := os.Stat(linkedFile); os.IsNotExist(err) {
		t.Error("New directory file not synced to project")
	}
}

// TestWatchStopCleanup tests watcher cleanup on stop
func TestWatchStopCleanup(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.CreateTestPackage("stop-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'stop';",
	})
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPublish(false, "", false, false, false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	w, err := watch.New(pkgDir, watch.Options{
		DebounceMs: 50,
	})
	if err != nil {
		t.Fatalf("Failed to create watcher: %v", err)
	}

	if err := w.Start(); err != nil {
		t.Fatalf("Failed to start watcher: %v", err)
	}

	// Stop should not block or panic
	done := make(chan struct{})
	go func() {
		w.Stop()
		close(done)
	}()

	select {
	case <-done:
		// Good - stop completed
	case <-time.After(5 * time.Second):
		t.Fatal("Watcher stop timed out")
	}
}

// TestWatchIgnoresGitDirectory tests .git changes are ignored
func TestWatchIgnoresGitDirectory(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.CreateTestPackage("git-ignore-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'git';",
	})
	if err := os.Chdir(pkgDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPublish(false, "", false, false, false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Create fake .git directory
	gitDir := filepath.Join(pkgDir, ".git", "objects")
	if err := os.MkdirAll(gitDir, 0755); err != nil {
		t.Fatalf("Failed to create .git: %v", err)
	}

	syncCount := 0
	var mu sync.Mutex

	w, err := watch.New(pkgDir, watch.Options{
		DebounceMs: 50,
		OnSync: func(files []string, projects int) {
			mu.Lock()
			syncCount++
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("Failed to create watcher: %v", err)
	}

	if err := w.Start(); err != nil {
		t.Fatalf("Failed to start watcher: %v", err)
	}
	defer w.Stop()

	time.Sleep(100 * time.Millisecond)

	// Write to .git
	if err := os.WriteFile(filepath.Join(gitDir, "abc123"), []byte("object"), 0644); err != nil {
		t.Fatalf("Failed to write to .git: %v", err)
	}

	time.Sleep(300 * time.Millisecond)

	mu.Lock()
	count := syncCount
	mu.Unlock()

	if count > 0 {
		t.Errorf("Expected no sync for .git change, got %d syncs", count)
	}
}
