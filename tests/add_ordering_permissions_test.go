package tests

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/cli"
)

// makeLockFileReadOnly seeds projectDir with an empty but valid lock file and
// makes it unwritable, so loading it succeeds and saving it fails. Everything
// else - reading and writing package.json in the still-writable directory -
// keeps working.
func makeLockFileReadOnly(t *testing.T, projectDir string) {
	t.Helper()

	path := filepath.Join(projectDir, "lnpm.lock")
	if err := os.WriteFile(path, []byte("version: 1\npackages: {}\n"), 0644); err != nil {
		t.Fatalf("Failed to write lnpm.lock: %v", err)
	}
	if err := os.Chmod(path, 0444); err != nil {
		t.Fatalf("Failed to chmod lnpm.lock: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0644) })
}

// The multi-package add must save the lock file BEFORE it rewrites
// package.json, because the user's original specifiers live nowhere else and
// the rewrite overwrites them. This pins that ordering from the outside: with
// the lock file unwritable, the save fails and the add returns before the
// rewrite loop, so package.json still holds the user's specifiers.
//
// Move the save back below the rewrite loop and this test fails: package.json
// would already hold file:.lnpm references with no lock entry to restore them
// from, which is precisely the data loss the ordering prevents.
func TestAddMultipleSavesLockBeforeRewritingPackageJSON(t *testing.T) {
	requirePermissionEnforcement(t)

	env := setupTest(t)
	env.simplePkg("lock-order-a")
	env.simplePkg("lock-order-b")

	projectDir := env.CreateTestPackage("lock-order-project", "1.0.0", nil)
	env.writePackageJSON(projectDir, map[string]interface{}{
		"name":    "lock-order-project",
		"version": "1.0.0",
		"dependencies": map[string]interface{}{
			"lock-order-a": "^1.0.0",
			"lock-order-b": "~2.3.4",
		},
	})
	makeLockFileReadOnly(t, projectDir)

	env.chdir(projectDir)
	specs := []string{"lock-order-a", "lock-order-b"}
	captureStdout(t, func() {
		if err := cli.RunAddMultiple(specs, false, false, false, false); err == nil {
			t.Error("Expected an error when the lock file cannot be saved, got nil")
		}
	})

	// The lock never reached disk, so package.json must not have been rewritten:
	// the specifiers are the only remaining record of what the user asked for.
	env.AssertPackageJSON(projectDir, "lock-order-a", "^1.0.0")
	env.AssertPackageJSON(projectDir, "lock-order-b", "~2.3.4")
}
