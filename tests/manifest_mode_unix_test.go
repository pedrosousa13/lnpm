//go:build !windows

package tests

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/cli"
)

// setUmask sets the process umask for the rest of the test and restores the
// previous value afterwards. The umask is process-global, so a test using this
// must not call t.Parallel() and must not run alongside one that does.
func setUmask(t *testing.T, mask int) {
	t.Helper()
	old := syscall.Umask(mask)
	t.Cleanup(func() { syscall.Umask(old) })
}

// assertMode fails unless path carries exactly want. where names the step of
// the path being checked, so a failure says which copy drifted.
func assertMode(t *testing.T, path string, want os.FileMode, where string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Failed to stat %s (%s): %v", where, path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("%s mode = %04o, want %04o (%s)", where, got, want, path)
	}
}

// TestPublishPreservesManifestModeThroughStoreAndConsumer follows one manifest's
// permission bits along the whole path: the author's own file, the store copy
// publish makes, and the .lnpm copy add materialises. The package carries a
// prepare script, so the store's rewrite of package.json runs and is the step
// under test. index.js is carried at the same mode as a control: it never goes
// through that rewrite, so it says whether a failure is the rewrite's or the
// store path's in general.
//
// The umask is pinned rather than inherited. A mode handed to os.WriteFile is
// masked, so an ambient umask can satisfy this assertion instead of the code
// doing it: under umask 0077 a hard-coded 0644 write lands at 0600, which is
// exactly what this test wants to see. Pinning 0022 - the umask this repo's
// tests are run under - keeps a wrong answer visible. syscall.Umask is
// process-global; no test in this package calls t.Parallel().
//
// What this test does not pin: the fix has two halves, reading the mode back
// instead of hard-coding 0644 and chmodding past the umask, and a 0600 fixture
// under umask 0022 only reaches the first. 0600 has no bits 0022 strips, so
// dropping the explicit chmod leaves this green. The half it misses is pinned by
// TestStripLifecycleScripts_PreservesManifestMode's "a mode the umask would
// strip" row (0640 under 0077) in internal/store. This test's own job is the
// reach - that the mode survives all three stages - not the mechanism.
func TestPublishPreservesManifestModeThroughStoreAndConsumer(t *testing.T) {
	setUmask(t, 0022)

	env := setupTest(t)

	pkgDir := env.CreateTestPackageWithScripts("private-manifest-pkg", "1.0.0",
		map[string]string{"index.js": "module.exports = 'test';"},
		map[string]string{"prepare": "echo prepared"},
	)

	// chmod is not masked by the umask, so the source really is 0600.
	for _, rel := range []string{"package.json", "index.js"} {
		if err := os.Chmod(filepath.Join(pkgDir, rel), 0600); err != nil {
			t.Fatalf("Failed to chmod source %s: %v", rel, err)
		}
	}
	assertMode(t, filepath.Join(pkgDir, "package.json"), 0600, "source manifest")
	assertMode(t, filepath.Join(pkgDir, "index.js"), 0600, "source sibling")

	env.chdir(pkgDir)
	if err := cli.RunPublish(false, false, true, false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	pkg, err := env.Database.GetPackageByName("private-manifest-pkg")
	if err != nil || pkg == nil {
		t.Fatalf("Failed to get package: %v", err)
	}
	// The rewrite is the step under test, so confirm it actually ran.
	env.AssertScriptMissing(pkg.StorePath, "private-manifest-pkg", "prepare")

	assertMode(t, filepath.Join(pkg.StorePath, "package.json"), 0600, "store manifest")
	assertMode(t, filepath.Join(pkg.StorePath, "index.js"), 0600, "store sibling")

	projectDir := env.newProject("private-manifest-project")
	env.addPkg(projectDir, "private-manifest-pkg", false, false)

	linked := filepath.Join(projectDir, ".lnpm", "private-manifest-pkg")
	assertMode(t, filepath.Join(linked, "package.json"), 0600, "consumer manifest")
	assertMode(t, filepath.Join(linked, "index.js"), 0600, "consumer sibling")
}
