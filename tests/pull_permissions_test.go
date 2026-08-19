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

// A pull that both fails on a package and then fails to save the lock file must
// report both. Returning only the save error would hide which packages could not
// be pulled, which is the half the user can actually act on.
func TestPullReportsPackageFailuresWhenLockSaveFails(t *testing.T) {
	requirePermissionEnforcement(t)

	env := setupTest(t)

	pkgDir := env.simplePkg("save-fail-pkg")
	projectDir := env.newProject("test-project")
	env.addPkg(projectDir, "save-fail-pkg", false, false)

	// A second, unpullable entry, so the run has a per-package failure to lose.
	lock, err := lockfile.Load(projectDir)
	if err != nil {
		t.Fatalf("Failed to load lockfile: %v", err)
	}
	lock.Add("ghost-pkg", lockfile.Package{Version: "1.0.0", Hash: "deadbeefdeadbeef", Source: "/nowhere", Linked: time.Now()})
	if err := lock.Save(projectDir); err != nil {
		t.Fatalf("Failed to save lockfile: %v", err)
	}

	env.republish(pkgDir, "save-fail-pkg", "2.0.0", "module.exports = 'v2';")

	// Read-only lock file: loading it and linking the package still work, only
	// writing the refreshed entries back fails.
	lockPath := filepath.Join(projectDir, "lnpm.lock")
	if err := os.Chmod(lockPath, 0444); err != nil {
		t.Fatalf("Failed to chmod lnpm.lock: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(lockPath, 0644) })

	env.chdir(projectDir)
	var pullErr error
	out := captureStdout(t, func() { pullErr = cli.RunPull(nil) })

	if pullErr == nil {
		t.Fatal("Expected an error when the lock file cannot be saved")
	}
	if !strings.Contains(pullErr.Error(), "failed to save lock file") {
		t.Errorf("Expected the returned error to report the failed save, got: %v", pullErr)
	}
	if !strings.Contains(pullErr.Error(), "1 of 2 package(s) failed to pull") {
		t.Errorf("Expected the returned error to also report the failed package, got: %v", pullErr)
	}
	if !strings.Contains(out, "ghost-pkg: not found in store") {
		t.Errorf("Expected ghost-pkg to still be reported, got:\n%s", out)
	}
	if !strings.Contains(out, "failed to save lock file") {
		t.Errorf("Expected the failed save to be reported, got:\n%s", out)
	}

	// The save really did fail: the lock file still holds the pre-pull entry.
	assertLockVersion(t, env, projectDir, "save-fail-pkg", "1.0.0")
}
