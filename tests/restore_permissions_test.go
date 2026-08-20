package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/cli"
	"github.com/pedrosousa13/lnpm/pkg/lockfile"
)

// A restore that could not finish a package tells the user to re-run it once the
// obstacle is gone, so the re-run has to finish what the first attempt started.
//
// It can only do that if a half-restored package is distinguishable from one the
// user re-added between the retreat and the restore, which is the other reason a
// name can already be in the lock file - and the reason restore skips it. The
// two are told apart by never writing the lock entry until the package.json
// write it describes has landed: a name in the lock is then always the user's
// own doing.
//
// Without that, the re-run reports the package as re-added, deletes the snapshot
// because nothing failed, and exits zero on a project whose package.json never
// got its lnpm reference.
func TestRestoreRerunFinishesAPartiallyRestoredPackage(t *testing.T) {
	requirePermissionEnforcement(t)

	env := setupTest(t)
	env.simplePkg("partial-restore-pkg")

	projectDir := env.CreateTestPackage("partial-restore-project", "1.0.0", nil)
	env.writePackageJSON(projectDir, map[string]interface{}{
		"name":         "partial-restore-project",
		"version":      "1.0.0",
		"dependencies": map[string]interface{}{"partial-restore-pkg": "^1.0.0"},
	})
	env.addPkg(projectDir, "partial-restore-pkg", false, false)

	if err := cli.RunRetreat(true, false); err != nil {
		t.Fatalf("Failed to retreat: %v", err)
	}
	env.AssertPackageJSON(projectDir, "partial-restore-pkg", "^1.0.0")

	pkgJSONPath := filepath.Join(projectDir, "package.json")
	makePackageJSONReadOnly(t, projectDir)

	env.chdir(projectDir)
	captureStdout(t, func() {
		if err := cli.RunRestore(); err == nil {
			t.Error("Expected restore to fail when package.json cannot be written, got nil")
		}
	})

	// Nothing may claim the package is restored: package.json still holds the
	// user's specifier, so a lock entry would describe a link package.json does
	// not have, and would be read on the re-run as a package the user re-added.
	env.AssertPackageJSON(projectDir, "partial-restore-pkg", "^1.0.0")
	env.AssertLockfile(projectDir, func(lock *lockfile.LockFile) {
		if entry, ok := lock.Get("partial-restore-pkg"); ok {
			t.Errorf("Expected no lock entry for a package whose package.json write failed, got %+v", entry)
		}
	})
	env.AssertFileExists(lockfile.RetreatPath(projectDir), true)

	if err := os.Chmod(pkgJSONPath, 0644); err != nil {
		t.Fatalf("Failed to restore package.json mode: %v", err)
	}

	out := captureStdout(t, func() {
		if err := cli.RunRestore(); err != nil {
			t.Errorf("Expected the re-run to succeed, got: %v", err)
		}
	})

	if strings.Contains(out, "was added again since the retreat") {
		t.Errorf("Expected the re-run to finish the package rather than report it as re-added, got:\n%s", out)
	}
	env.AssertPackageJSON(projectDir, "partial-restore-pkg", "file:.lnpm/partial-restore-pkg")
	env.AssertSymlinkExists(projectDir, "partial-restore-pkg")
	env.AssertLockfile(projectDir, func(lock *lockfile.LockFile) {
		entry, ok := lock.Get("partial-restore-pkg")
		if !ok {
			t.Fatal("Expected partial-restore-pkg in lnpm.lock after the re-run")
		}
		if entry.OriginalVersion != "^1.0.0" {
			t.Errorf("OriginalVersion = %q, want ^1.0.0", entry.OriginalVersion)
		}
	})
	env.AssertFileExists(lockfile.RetreatPath(projectDir), false)
}
