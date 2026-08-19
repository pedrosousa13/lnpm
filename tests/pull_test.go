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

// lockEntry returns a project's lock entry for pkg, failing if it has none.
func lockEntry(t *testing.T, env *TestEnvironment, projectDir, pkg string) lockfile.Package {
	t.Helper()
	var entry lockfile.Package
	env.AssertLockfile(projectDir, func(lock *lockfile.LockFile) {
		found, ok := lock.Get(pkg)
		if !ok {
			t.Fatalf("Package %s not in lockfile", pkg)
		}
		entry = found
	})
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
func assertLockVersion(t *testing.T, env *TestEnvironment, projectDir, pkg, want string) {
	t.Helper()
	if got := lockEntry(t, env, projectDir, pkg).Version; got != want {
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
	assertLockVersion(t, env, projectDir, "pull-a", "2.0.0")
	assertLockVersion(t, env, projectDir, "pull-b", "3.0.0")

	// The relink must leave the link every consumer resolves through intact:
	// .lnpm/<pkg> still there, node_modules/<pkg> still a symlink into it.
	for _, name := range []string{"pull-a", "pull-b"} {
		env.AssertFilesLinked(projectDir, name)
		env.AssertSymlinkExists(projectDir, name)
	}
}

// TestPullAllReportsInSortedOrder pins that the no-argument form reports
// packages in name order. The set comes from a map, so an unsorted
// implementation gets the right order by luck often enough that one run proves
// nothing - the pull is repeated, and every repeat has to agree.
func TestPullAllReportsInSortedOrder(t *testing.T) {
	env := setupTest(t)

	names := []string{"order-delta", "order-bravo", "order-charlie", "order-alpha"}
	projectDir := env.newProject("test-project")
	for _, name := range names {
		env.simplePkg(name)
		env.addPkg(projectDir, name, false, false)
	}

	wantOrder := []string{"order-alpha", "order-bravo", "order-charlie", "order-delta"}
	for run := 0; run < 4; run++ {
		env.chdir(projectDir)
		out := captureStdout(t, func() {
			if err := cli.RunPull(nil); err != nil {
				t.Errorf("RunPull: %v", err)
			}
		})

		at := -1
		for _, name := range wantOrder {
			idx := strings.Index(out, "Pulling "+name+"...")
			if idx < 0 {
				t.Fatalf("Run %d: expected %s to be reported, got:\n%s", run, name, out)
			}
			if idx < at {
				t.Fatalf("Run %d: expected packages reported in name order %v, got:\n%s", run, wantOrder, out)
			}
			at = idx
		}
	}
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
	assertLockVersion(t, env, projectDir, "named-a", "2.0.0")

	// named-b keeps the contents and lock entry it had from `add`.
	env.AssertLinkedFileContent(projectDir, "named-b", "index.js", "module.exports = 'named-b';")
	assertLockVersion(t, env, projectDir, "named-b", "1.0.0")
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
	// A package that was skipped was not pulled, and the summary must not claim
	// otherwise.
	if strings.Contains(out, "Pulled") {
		t.Errorf("Expected no 'Pulled' summary when nothing moved, got:\n%s", out)
	}
	if !strings.Contains(out, "Already up to date") {
		t.Errorf("Expected an 'Already up to date' summary line, got:\n%s", out)
	}
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

	env.simplePkg("lock-pull-pkg")
	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	env.writePackageJSON(projectDir, map[string]interface{}{
		"name":         "test-project",
		"version":      "1.0.0",
		"dependencies": map[string]interface{}{"lock-pull-pkg": "^4.2.0"},
	})
	env.addPkg(projectDir, "lock-pull-pkg", false, false)

	before := lockEntry(t, env, projectDir, "lock-pull-pkg")

	// Republished from a DIFFERENT directory, so the store's source path really
	// moves and the Source assertion below can only pass if pull writes the
	// store's path rather than carrying the old lock entry's over.
	relocated := env.CreateTestPackage("lock-pull-pkg-relocated", "2.0.0", nil)
	env.republish(relocated, "lock-pull-pkg", "2.0.0", "module.exports = 'v2';")

	env.chdir(projectDir)
	if err := cli.RunPull(nil); err != nil {
		t.Fatalf("RunPull: %v", err)
	}

	stored, err := env.Database.GetPackageByName("lock-pull-pkg")
	if err != nil || stored == nil {
		t.Fatalf("Failed to read lock-pull-pkg from the store: %v", err)
	}

	after := lockEntry(t, env, projectDir, "lock-pull-pkg")
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
	// Missing from the store is nearly always an unpublished package, so the
	// report has to say what to do about it - as `add` does.
	if !strings.Contains(out, "did you run 'lnpm publish' in the package directory") {
		t.Errorf("Expected the report to hint at 'lnpm publish', got:\n%s", out)
	}

	// The healthy package beside it was still refreshed.
	env.AssertLinkedFileContent(projectDir, "present-pkg", "index.js", "module.exports = 'v2';")
	assertLockVersion(t, env, projectDir, "present-pkg", "2.0.0")
}

// TestPullReportsFailuresAtTheFailingPackage pins where a failure is reported:
// the "Pulling <pkg>... " line is closed by the failure itself rather than left
// dangling, and the run ends on the warning block it exits non-zero for, not on
// the success line for the package that did refresh.
func TestPullReportsFailuresAtTheFailingPackage(t *testing.T) {
	env := setupTest(t)

	okDir := env.simplePkg("ok-pkg")
	brokenDir := env.simplePkg("broken-pkg")
	projectDir := env.newProject("test-project")
	env.addPkg(projectDir, "ok-pkg", false, false)
	env.addPkg(projectDir, "broken-pkg", false, false)

	env.republish(okDir, "ok-pkg", "2.0.0", "module.exports = 'ok-v2';")
	env.republish(brokenDir, "broken-pkg", "2.0.0", "module.exports = 'broken-v2';")

	// Delete the store contents the new broken-pkg entry points at, so reading
	// its files fails midway through the pull.
	broken, err := env.Database.GetPackageByName("broken-pkg")
	if err != nil || broken == nil {
		t.Fatalf("Failed to read broken-pkg from the store: %v", err)
	}
	if err := os.RemoveAll(broken.StorePath); err != nil {
		t.Fatalf("Failed to remove the store path: %v", err)
	}

	env.chdir(projectDir)
	var pullErr error
	out := captureStdout(t, func() { pullErr = cli.RunPull(nil) })

	if pullErr == nil {
		t.Fatal("Expected a non-zero result when a package's store files are unreadable")
	}
	if !strings.Contains(out, "Pulling broken-pkg... failed to get package files") {
		t.Errorf("Expected the failure to close the 'Pulling broken-pkg... ' line, got:\n%s", out)
	}

	okAt := strings.Index(out, "Pulled 1 package(s)")
	warnAt := strings.Index(out, "Some packages failed")
	if okAt < 0 || warnAt < 0 {
		t.Fatalf("Expected both a success line and a failure block, got:\n%s", out)
	}
	if okAt > warnAt {
		t.Errorf("Expected the success line before the failure block, got:\n%s", out)
	}
}

// TestPullPicksUpSameVersionContentChange covers the republish that changes
// nothing but the files: same version, new contents. The version alone cannot
// tell pull anything here, so only the content hash can.
func TestPullPicksUpSameVersionContentChange(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.simplePkg("samever-pkg")
	projectDir := env.newProject("test-project")
	env.addPkg(projectDir, "samever-pkg", false, false)

	before := lockEntry(t, env, projectDir, "samever-pkg")

	// Same version 1.0.0, different contents.
	env.republish(pkgDir, "samever-pkg", "1.0.0", "module.exports = 'samever-pkg-patched';")

	env.chdir(projectDir)
	if err := cli.RunPull(nil); err != nil {
		t.Fatalf("RunPull: %v", err)
	}

	env.AssertLinkedFileContent(projectDir, "samever-pkg", "index.js", "module.exports = 'samever-pkg-patched';")

	after := lockEntry(t, env, projectDir, "samever-pkg")
	if after.Version != "1.0.0" {
		t.Errorf("Expected the lock version to stay 1.0.0, got %s", after.Version)
	}
	if after.Hash == before.Hash {
		t.Errorf("Expected the lock hash to change when the contents did, still %s", after.Hash)
	}
	stored, err := env.Database.GetPackageByName("samever-pkg")
	if err != nil || stored == nil {
		t.Fatalf("Failed to read samever-pkg from the store: %v", err)
	}
	if after.Hash != stored.ContentHash {
		t.Errorf("Expected lock hash %s (the store's current content hash), got %s", stored.ContentHash, after.Hash)
	}
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
