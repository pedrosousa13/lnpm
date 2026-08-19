package tests

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/cli"
	"github.com/pedrosousa13/lnpm/pkg/lockfile"
)

// requirePermissionEnforcement skips tests that force a write to fail by making
// the target file read-only. Windows models only a read-only bit and root
// ignores permission bits entirely, so neither can produce the failure these
// tests depend on.
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

// The multi-package path saves the lock file before it rewrites package.json,
// so a failed rewrite is discovered with the lock already durable. Preserving
// the specifier there would be pointless: the rewrite never landed, so
// package.json still holds the user's own specifier, and the package was never
// really added. Keeping a lock entry (and a database link, and a node_modules
// symlink) for it would describe a dependency the project does not have. So the
// batch rolls that package back completely - lock entry removed and the lock
// re-saved, symlink and .lnpm copy unlinked - and reports it as a failure.
func TestAddMultipleRollsBackPackagesWhenPackageJSONWriteFails(t *testing.T) {
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
	output := captureStdout(t, func() {
		if err := cli.RunAddMultiple(specs, false, false, false, false); err == nil {
			t.Error("Expected an error when package.json cannot be written, got nil")
		}
	})

	// Nothing survived the batch, so add must not report progress or send the
	// user on to npm install.
	if strings.Contains(output, "npm install") {
		t.Errorf("Expected no npm install advice when every package rolled back, got output:\n%s", output)
	}
	for _, name := range specs {
		if strings.Contains(output, "Added "+name) {
			t.Errorf("Expected %s not to be reported as added, got output:\n%s", name, output)
		}
	}

	env.AssertLockfile(projectDir, func(lock *lockfile.LockFile) {
		for _, name := range specs {
			if p, ok := lock.Get(name); ok {
				t.Errorf("Expected %s to be rolled back out of the lockfile, got %+v", name, p)
			}
		}
	})

	for _, name := range specs {
		env.AssertSymlinkMissing(projectDir, name)
		env.AssertDatabaseNoLink(name, projectDir)
	}

	// package.json was never rewritten, so the user's specifiers survive.
	env.AssertPackageJSON(projectDir, "failing-multi-a", "^1.0.0")
	env.AssertPackageJSON(projectDir, "failing-multi-b", "~2.3.4")
}

// Rolling a package back is only right when this invocation created it. A
// package added by an earlier run is different: package.json already holds its
// lnpm reference, so the user's real specifier survives nowhere but the lock's
// OriginalVersion. Dropping that entry would destroy the specifier outright,
// and unlinking would delete the .lnpm copy package.json still points at -
// exactly the data loss the rollback exists to prevent. So a failure on a
// re-add must leave the lock entry and the link alone, while a package this run
// added for the first time still rolls back completely.
func TestAddMultipleKeepsPriorStateWhenReAddPackageJSONWriteFails(t *testing.T) {
	requirePermissionEnforcement(t)

	env := setupTest(t)
	env.simplePkg("readd-perm-existing")
	env.simplePkg("readd-perm-new")

	projectDir := env.CreateTestPackage("readd-perm-project", "1.0.0", nil)
	env.writePackageJSON(projectDir, map[string]interface{}{
		"name":    "readd-perm-project",
		"version": "1.0.0",
		"dependencies": map[string]interface{}{
			"readd-perm-existing": "^1.2.3",
			"readd-perm-new":      "~2.3.4",
		},
	})

	// The first add succeeds, so package.json now holds the lnpm reference and
	// "^1.2.3" survives only as OriginalVersion in the lock.
	env.addPkg(projectDir, "readd-perm-existing", false, false)
	env.AssertPackageJSON(projectDir, "readd-perm-existing", "file:.lnpm/readd-perm-existing")
	assertOriginalVersion(t, env, projectDir, "readd-perm-existing", "^1.2.3")

	makePackageJSONReadOnly(t, projectDir)

	env.chdir(projectDir)
	specs := []string{"readd-perm-existing", "readd-perm-new"}
	captureStdout(t, func() {
		if err := cli.RunAddMultiple(specs, false, false, false, false); err == nil {
			t.Error("Expected an error when package.json cannot be written, got nil")
		}
	})

	// The re-added package is left exactly as the earlier add left it.
	assertOriginalVersion(t, env, projectDir, "readd-perm-existing", "^1.2.3")
	env.AssertFilesLinked(projectDir, "readd-perm-existing")
	env.AssertSymlinkExists(projectDir, "readd-perm-existing")
	env.AssertPackageJSON(projectDir, "readd-perm-existing", "file:.lnpm/readd-perm-existing")

	// The package this run added for the first time still rolls back.
	env.AssertLockfile(projectDir, func(lock *lockfile.LockFile) {
		if p, ok := lock.Get("readd-perm-new"); ok {
			t.Errorf("Expected readd-perm-new to be rolled back out of the lockfile, got %+v", p)
		}
	})
	env.AssertSymlinkMissing(projectDir, "readd-perm-new")
	env.AssertDatabaseNoLink("readd-perm-new", projectDir)
	env.AssertPackageJSON(projectDir, "readd-perm-new", "~2.3.4")
}

// package.json can also fail to be read, before the lock is built at all. That
// package has no specifier to record and never gets its reference written, so
// it is rolled back for the same reason a failed write is: a lock entry with an
// empty OriginalVersion would tell a later remove to delete a dependency the
// user still has.
func TestAddMultipleRollsBackPackagesWhenPackageJSONReadFails(t *testing.T) {
	requirePermissionEnforcement(t)

	env := setupTest(t)
	env.simplePkg("unreadable-multi-a")
	env.simplePkg("unreadable-multi-b")

	projectDir := env.CreateTestPackage("unreadable-multi-project", "1.0.0", nil)
	env.writePackageJSON(projectDir, map[string]interface{}{
		"name":    "unreadable-multi-project",
		"version": "1.0.0",
		"dependencies": map[string]interface{}{
			"unreadable-multi-a": "^1.0.0",
			"unreadable-multi-b": "~2.3.4",
		},
	})

	// Mode 0000 fails the read rather than the write, so the batch never
	// learns any specifier. os.Stat still succeeds, so add gets far enough to
	// link the packages first.
	pkgJSONPath := filepath.Join(projectDir, "package.json")
	if err := os.Chmod(pkgJSONPath, 0000); err != nil {
		t.Fatalf("Failed to chmod package.json: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(pkgJSONPath, 0644) })

	env.chdir(projectDir)
	specs := []string{"unreadable-multi-a", "unreadable-multi-b"}
	captureStdout(t, func() {
		if err := cli.RunAddMultiple(specs, false, false, false, false); err == nil {
			t.Error("Expected an error when package.json cannot be read, got nil")
		}
	})

	env.AssertLockfile(projectDir, func(lock *lockfile.LockFile) {
		for _, name := range specs {
			if p, ok := lock.Get(name); ok {
				t.Errorf("Expected %s to be rolled back out of the lockfile, got %+v", name, p)
			}
		}
	})

	for _, name := range specs {
		env.AssertSymlinkMissing(projectDir, name)
		env.AssertDatabaseNoLink(name, projectDir)
	}

	// Restore access before reading: package.json is untouched.
	if err := os.Chmod(pkgJSONPath, 0644); err != nil {
		t.Fatalf("Failed to restore package.json mode: %v", err)
	}
	env.AssertPackageJSON(projectDir, "unreadable-multi-a", "^1.0.0")
	env.AssertPackageJSON(projectDir, "unreadable-multi-b", "~2.3.4")
}
