package tests

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/cli"
	"github.com/pedrosousa13/lnpm/pkg/lockfile"
)

// requirePermissionEnforcement skips tests that force a package.json rewrite
// to fail by making the file read-only. Windows models only a read-only bit and
// root ignores permission bits entirely, so neither can produce the failure
// these tests depend on.
func requirePermissionEnforcement(t *testing.T) {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("Windows reports only a read-only bit, not Unix permission bits")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root bypasses the permission checks this test relies on")
	}
}

// makePackageJSONReadOnly makes projectDir's package.json unwritable, so the
// rewrite that replaces the dependency specifier with an lnpm reference fails
// while everything around it (reading package.json, writing lnpm.lock into the
// still-writable directory) keeps working.
func makePackageJSONReadOnly(t *testing.T, projectDir string) {
	t.Helper()

	path := filepath.Join(projectDir, "package.json")
	if err := os.Chmod(path, 0444); err != nil {
		t.Fatalf("Failed to chmod package.json: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0644) })
}

// The user's original specifier lives nowhere but package.json, and the add
// overwrites it with an lnpm reference. If the lock file is written after that
// rewrite, any failure in between destroys the specifier for good: remove and
// retreat delete the dependency instead of restoring it. So the specifier must
// reach the lock file before package.json is touched - a failed rewrite must
// still leave the lock able to restore it.
func TestAddRecordsOriginalVersionWhenPackageJSONWriteFails(t *testing.T) {
	requirePermissionEnforcement(t)

	env := setupTest(t)
	env.simplePkg("failing-single-pkg")

	projectDir := env.CreateTestPackage("failing-single-project", "1.0.0", nil)
	env.writePackageJSON(projectDir, map[string]interface{}{
		"name":         "failing-single-project",
		"version":      "1.0.0",
		"dependencies": map[string]interface{}{"failing-single-pkg": "^1.0.0"},
	})
	makePackageJSONReadOnly(t, projectDir)

	env.chdir(projectDir)
	if err := cli.RunAdd("failing-single-pkg", false, false, false); err == nil {
		t.Fatal("Expected an error when package.json cannot be written, got nil")
	}

	// package.json is untouched, so the specifier is still recoverable - but
	// only if the lock file recorded it.
	env.AssertPackageJSON(projectDir, "failing-single-pkg", "^1.0.0")
	assertOriginalVersion(t, env, projectDir, "failing-single-pkg", "^1.0.0")
}

// The multi-package path has the same ordering constraint, and gets it wrong
// for every package in the batch at once, so it needs its own coverage.
func TestAddMultipleRecordsOriginalVersionsWhenPackageJSONWriteFails(t *testing.T) {
	requirePermissionEnforcement(t)

	env := setupTest(t)
	env.simplePkg("failing-multi-a")
	env.simplePkg("failing-multi-b")

	projectDir := env.CreateTestPackage("failing-multi-project", "1.0.0", nil)
	env.writePackageJSON(projectDir, map[string]interface{}{
		"name":    "failing-multi-project",
		"version": "1.0.0",
		"dependencies": map[string]interface{}{
			"failing-multi-a": "^1.0.0",
			"failing-multi-b": "~2.3.4",
		},
	})
	makePackageJSONReadOnly(t, projectDir)

	env.chdir(projectDir)
	specs := []string{"failing-multi-a", "failing-multi-b"}
	captureStdout(t, func() {
		if err := cli.RunAddMultiple(specs, false, false, false, false); err != nil {
			t.Errorf("RunAddMultiple returned error: %v", err)
		}
	})

	env.AssertLockfile(projectDir, func(lock *lockfile.LockFile) {
		for name, want := range map[string]string{
			"failing-multi-a": "^1.0.0",
			"failing-multi-b": "~2.3.4",
		} {
			p, ok := lock.Get(name)
			if !ok {
				t.Fatalf("Package %s not in lockfile", name)
			}
			if p.OriginalVersion != want {
				t.Errorf("Expected original version %q for %s, got %q", want, name, p.OriginalVersion)
			}
		}
	})
}
