package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pedrosousa13/lnpm/internal/cli"
	"github.com/pedrosousa13/lnpm/pkg/lockfile"
)

// republish rewrites an already published package's source with a new version
// and index.js, then publishes it again WITHOUT --push, so linked projects are
// deliberately left stale. That stale state is exactly what pull has to fix.
// The cwd is left inside the package directory.
func (te *TestEnvironment) republish(pkgDir, name, version, indexJS string) {
	te.t.Helper()
	te.chdir(pkgDir)
	te.writeFile(filepath.Join(pkgDir, "package.json"), `{"name":"`+name+`","version":"`+version+`"}`)
	te.writeFile(filepath.Join(pkgDir, "index.js"), indexJS)
	if err := cli.RunPublish(false, false, false, false); err != nil {
		te.t.Fatalf("Failed to republish %s@%s: %v", name, version, err)
	}
}

// lockEntry returns a project's lock entry for pkg, failing if it has none.
func lockEntry(t *testing.T, projectDir, pkg string) lockfile.Package {
	t.Helper()
	lock, err := lockfile.Load(projectDir)
	if err != nil {
		t.Fatalf("Failed to load lockfile: %v", err)
	}
	entry, ok := lock.Get(pkg)
	if !ok {
		t.Fatalf("Package %s not in lockfile", pkg)
	}
	return entry
}

// readBytes reads a file, failing the test on error.
func readBytes(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Failed to read %s: %v", path, err)
	}
	return data
}

// assertLockVersion checks a package's recorded version in the project's lockfile.
func assertLockVersion(t *testing.T, projectDir, pkg, want string) {
	t.Helper()
	if got := lockEntry(t, projectDir, pkg).Version; got != want {
		t.Errorf("Expected %s at version %s in lockfile, got %s", pkg, want, got)
	}
}

// TestPullAllRefreshesEveryLinkedPackage covers `lnpm pull` with no arguments:
// every package in lnpm.lock is refreshed to the version now in the store.
func TestPullAllRefreshesEveryLinkedPackage(t *testing.T) {
	env := setupTest(t)

	pkgA := env.simplePkg("pull-a")
	pkgB := env.simplePkg("pull-b")
	projectDir := env.newProject("test-project")
	env.addPkg(projectDir, "pull-a", false, false)
	env.addPkg(projectDir, "pull-b", false, false)

	env.republish(pkgA, "pull-a", "2.0.0", "module.exports = 'a-v2';")
	env.republish(pkgB, "pull-b", "3.0.0", "module.exports = 'b-v2';")

	env.chdir(projectDir)
	if err := cli.RunPull(nil); err != nil {
		t.Fatalf("RunPull: %v", err)
	}

	env.AssertLinkedFileContent(projectDir, "pull-a", "index.js", "module.exports = 'a-v2';")
	env.AssertLinkedFileContent(projectDir, "pull-b", "index.js", "module.exports = 'b-v2';")
	assertLockVersion(t, projectDir, "pull-a", "2.0.0")
	assertLockVersion(t, projectDir, "pull-b", "3.0.0")
}

// TestPullNamedPackageLeavesOthersAlone covers `lnpm pull <pkg>`: only the named
// package is refreshed, even though a sibling is equally out of date.
func TestPullNamedPackageLeavesOthersAlone(t *testing.T) {
	env := setupTest(t)

	pkgA := env.simplePkg("named-a")
	pkgB := env.simplePkg("named-b")
	projectDir := env.newProject("test-project")
	env.addPkg(projectDir, "named-a", false, false)
	env.addPkg(projectDir, "named-b", false, false)

	env.republish(pkgA, "named-a", "2.0.0", "module.exports = 'a-v2';")
	env.republish(pkgB, "named-b", "2.0.0", "module.exports = 'b-v2';")

	env.chdir(projectDir)
	if err := cli.RunPull([]string{"named-a"}); err != nil {
		t.Fatalf("RunPull: %v", err)
	}

	env.AssertLinkedFileContent(projectDir, "named-a", "index.js", "module.exports = 'a-v2';")
	assertLockVersion(t, projectDir, "named-a", "2.0.0")

	// named-b keeps the contents and lock entry it had from `add`.
	env.AssertLinkedFileContent(projectDir, "named-b", "index.js", "module.exports = 'named-b';")
	assertLockVersion(t, projectDir, "named-b", "1.0.0")
}

// TestPullUpToDateIsNoOp pins that pulling a package already at the store's
// current version neither errors nor touches the lock file.
func TestPullUpToDateIsNoOp(t *testing.T) {
	env := setupTest(t)

	_, projectDir := env.publishAndAdd("uptodate-pkg")
	lockPath := filepath.Join(projectDir, "lnpm.lock")
	before := readBytes(t, lockPath)

	env.chdir(projectDir)
	out := captureStdout(t, func() {
		if err := cli.RunPull(nil); err != nil {
			t.Errorf("Pulling an up-to-date package must not error, got: %v", err)
		}
	})

	if got := readBytes(t, lockPath); string(got) != string(before) {
		t.Errorf("Expected lnpm.lock to be untouched for an up-to-date pull.\nbefore:\n%s\nafter:\n%s", before, got)
	}
	if !strings.Contains(out, "already up to date") {
		t.Errorf("Expected an 'already up to date' notice, got:\n%s", out)
	}
	// The linked copy is still the one add produced.
	env.AssertLinkedFileContent(projectDir, "uptodate-pkg", "index.js", "module.exports = 'uptodate-pkg';")
}

// TestPullUnlinkedPackageErrors pins that naming a package this project has
// never linked fails with a clear error and links nothing, even though the
// package does exist in the store.
func TestPullUnlinkedPackageErrors(t *testing.T) {
	env := setupTest(t)

	env.simplePkg("linked-pkg")
	env.simplePkg("stranger-pkg")
	projectDir := env.newProject("test-project")
	env.addPkg(projectDir, "linked-pkg", false, false)

	env.chdir(projectDir)
	err := cli.RunPull([]string{"stranger-pkg"})
	if err == nil {
		t.Fatal("Expected an error when pulling a package that is not linked in this project")
	}
	if !strings.Contains(err.Error(), "stranger-pkg") || !strings.Contains(err.Error(), "not linked") {
		t.Errorf("Expected an error naming stranger-pkg as not linked, got: %v", err)
	}

	env.AssertDirectoryExists(filepath.Join(projectDir, ".lnpm", "stranger-pkg"), false)
	env.AssertSymlinkMissing(projectDir, "stranger-pkg")
	env.AssertLockfile(projectDir, func(lock *lockfile.LockFile) {
		if lock.Has("stranger-pkg") {
			t.Error("Expected no lockfile entry for a package that was never linked")
		}
	})
}

// TestPullNeverModifiesPackageJSON pins that pull leaves package.json exactly as
// it found it - byte for byte - even when it does refresh the package.
func TestPullNeverModifiesPackageJSON(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.simplePkg("pj-pkg")
	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	env.writePackageJSON(projectDir, map[string]interface{}{
		"name":         "test-project",
		"version":      "1.0.0",
		"dependencies": map[string]interface{}{"pj-pkg": "^1.0.0"},
	})
	env.addPkg(projectDir, "pj-pkg", false, false)

	env.republish(pkgDir, "pj-pkg", "2.0.0", "module.exports = 'v2';")

	pkgJSONPath := filepath.Join(projectDir, "package.json")
	before := readBytes(t, pkgJSONPath)

	env.chdir(projectDir)
	if err := cli.RunPull(nil); err != nil {
		t.Fatalf("RunPull: %v", err)
	}

	if got := readBytes(t, pkgJSONPath); string(got) != string(before) {
		t.Errorf("Expected package.json to be untouched by pull.\nbefore:\n%s\nafter:\n%s", before, got)
	}
	// The reference `add` wrote is still what package.json holds.
	env.AssertPackageJSON(projectDir, "pj-pkg", "file:.lnpm/pj-pkg")
	// ...and the pull really did happen.
	env.AssertLinkedFileContent(projectDir, "pj-pkg", "index.js", "module.exports = 'v2';")
}

// TestPullUpdatesLockfileEntry pins the whole lock entry a pull writes: the new
// version, the store's current content hash and source, a Linked timestamp that
// advanced, and the OriginalVersion carried over from `add` (losing it would
// make a later remove delete the dependency instead of restoring the specifier).
func TestPullUpdatesLockfileEntry(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.simplePkg("lock-pull-pkg")
	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	env.writePackageJSON(projectDir, map[string]interface{}{
		"name":         "test-project",
		"version":      "1.0.0",
		"dependencies": map[string]interface{}{"lock-pull-pkg": "^4.2.0"},
	})
	env.addPkg(projectDir, "lock-pull-pkg", false, false)

	before := lockEntry(t, projectDir, "lock-pull-pkg")

	env.republish(pkgDir, "lock-pull-pkg", "2.0.0", "module.exports = 'v2';")

	env.chdir(projectDir)
	if err := cli.RunPull(nil); err != nil {
		t.Fatalf("RunPull: %v", err)
	}

	stored, err := env.Database.GetPackageByName("lock-pull-pkg")
	if err != nil || stored == nil {
		t.Fatalf("Failed to read lock-pull-pkg from the store: %v", err)
	}

	after := lockEntry(t, projectDir, "lock-pull-pkg")
	if after.Version != "2.0.0" {
		t.Errorf("Expected lock version 2.0.0, got %s", after.Version)
	}
	if after.Hash == before.Hash {
		t.Errorf("Expected the lock hash to change on pull, still %s", after.Hash)
	}
	if after.Hash != stored.ContentHash {
		t.Errorf("Expected lock hash %s (the store's current content hash), got %s", stored.ContentHash, after.Hash)
	}
	if after.Source != stored.SourcePath {
		t.Errorf("Expected lock source %s, got %s", stored.SourcePath, after.Source)
	}
	if !after.Linked.After(before.Linked) {
		t.Errorf("Expected the Linked timestamp to advance, went %v -> %v", before.Linked, after.Linked)
	}
	if after.OriginalVersion != "^4.2.0" {
		t.Errorf("Expected pull to preserve OriginalVersion ^4.2.0, got %q", after.OriginalVersion)
	}
}

// TestPullReportsVersionChange pins the per-package summary: the old and new
// versions are both named, and the closing line reports what was pulled.
func TestPullReportsVersionChange(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.simplePkg("report-pkg")
	projectDir := env.newProject("test-project")
	env.addPkg(projectDir, "report-pkg", false, false)

	env.republish(pkgDir, "report-pkg", "2.5.0", "module.exports = 'v2';")

	env.chdir(projectDir)
	out := captureStdout(t, func() {
		if err := cli.RunPull([]string{"report-pkg"}); err != nil {
			t.Errorf("RunPull: %v", err)
		}
	})

	if !strings.Contains(out, "Pulling report-pkg... updated 1.0.0 -> 2.5.0") {
		t.Errorf("Expected the summary to name both versions, got:\n%s", out)
	}
	if !strings.Contains(out, "Pulled report-pkg@2.5.0") {
		t.Errorf("Expected a closing line naming the pulled package, got:\n%s", out)
	}
}

// TestPullReportsPackageMissingFromStore covers a lock entry whose package is
// gone from the store: it is reported and pull exits non-zero, while the
// packages beside it are still refreshed.
func TestPullReportsPackageMissingFromStore(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.simplePkg("present-pkg")
	projectDir := env.newProject("test-project")
	env.addPkg(projectDir, "present-pkg", false, false)

	// A lock entry for a package this store has never held.
	lock, err := lockfile.Load(projectDir)
	if err != nil {
		t.Fatalf("Failed to load lockfile: %v", err)
	}
	lock.Add("ghost-pkg", lockfile.Package{Version: "1.0.0", Hash: "deadbeefdeadbeef", Source: "/nowhere", Linked: time.Now()})
	if err := lock.Save(projectDir); err != nil {
		t.Fatalf("Failed to save lockfile: %v", err)
	}

	env.republish(pkgDir, "present-pkg", "2.0.0", "module.exports = 'v2';")

	env.chdir(projectDir)
	var pullErr error
	out := captureStdout(t, func() { pullErr = cli.RunPull(nil) })

	if pullErr == nil {
		t.Fatal("Expected a non-zero result when a locked package is missing from the store")
	}
	if !strings.Contains(out, "ghost-pkg: not found in store") {
		t.Errorf("Expected ghost-pkg to be reported as missing from the store, got:\n%s", out)
	}

	// The healthy package beside it was still refreshed.
	env.AssertLinkedFileContent(projectDir, "present-pkg", "index.js", "module.exports = 'v2';")
	assertLockVersion(t, projectDir, "present-pkg", "2.0.0")
}

// TestPullNoLinkedPackages pins that pulling in a project with nothing linked
// says so instead of failing.
func TestPullNoLinkedPackages(t *testing.T) {
	env := setupTest(t)

	projectDir := env.newProject("empty-project")
	env.chdir(projectDir)

	out := captureStdout(t, func() {
		if err := cli.RunPull(nil); err != nil {
			t.Errorf("Pulling with nothing linked must not error, got: %v", err)
		}
	})

	if !strings.Contains(out, "No linked packages to pull") {
		t.Errorf("Expected a 'No linked packages to pull' notice, got:\n%s", out)
	}
	env.AssertLockfileExists(projectDir, false)
}
