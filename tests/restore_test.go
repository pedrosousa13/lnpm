package tests

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/cli"
	"github.com/pedrosousa13/lnpm/pkg/lockfile"
)

// TestRestoreRelinksPackagesFromSnapshot is the round trip the feature exists
// for: add, retreat, restore, and the project is back where it started. It
// covers the acceptance criteria that restore recreates .lnpm/<pkg>, the
// node_modules symlink and the file:.lnpm/<pkg> specifier for every package in
// the snapshot, and that a clean restore consumes the snapshot and leaves
// lnpm.lock holding the pre-retreat entries.
func TestRestoreRelinksPackagesFromSnapshot(t *testing.T) {
	env := setupTest(t)

	env.simplePkg("restore-pkg-a")
	env.simplePkg("restore-pkg-b")
	projectDir := env.newProject("restore-project")
	env.writePackageJSON(projectDir, map[string]interface{}{
		"name":         "restore-project",
		"version":      "1.0.0",
		"dependencies": map[string]interface{}{"restore-pkg-a": "^1.0.0"},
	})
	env.addPkg(projectDir, "restore-pkg-a", false, false)
	env.addPkg(projectDir, "restore-pkg-b", false, false)

	before := map[string]lockfile.Package{}
	env.AssertLockfile(projectDir, func(lock *lockfile.LockFile) {
		for _, name := range lock.List() {
			before[name], _ = lock.Get(name)
		}
	})

	if err := cli.RunRetreat(true, false); err != nil {
		t.Fatalf("Failed to retreat: %v", err)
	}

	out := captureStdout(t, func() {
		if err := cli.RunRestore(); err != nil {
			t.Errorf("Failed to restore: %v", err)
		}
	})

	for _, name := range []string{"restore-pkg-a", "restore-pkg-b"} {
		env.AssertSymlinkExists(projectDir, name)
		env.AssertStoreCopy(projectDir, name)
		env.AssertPackageJSON(projectDir, name, "file:.lnpm/"+name)
		env.AssertDatabaseLink(name, projectDir)
		if want := "Restored " + name + "@1.0.0"; !strings.Contains(out, want) {
			t.Errorf("Expected the report to contain %q, got:\n%s", want, out)
		}
	}

	// .lnpm/ is lnpm's own working directory; retreat un-ignores it, so a
	// restore that did not put the entry back would leave the linked copies
	// staged for commit.
	env.AssertGitignore(projectDir, ".lnpm/", true)

	// The snapshot is spent once every package in it is linked again.
	env.AssertFileExists(lockfile.RetreatPath(projectDir), false)

	env.AssertLockfileExists(projectDir, true)
	env.AssertLockfile(projectDir, func(lock *lockfile.LockFile) {
		for name, want := range before {
			got, ok := lock.Get(name)
			if !ok {
				t.Fatalf("Expected %s back in lnpm.lock", name)
			}
			// Linked is a timestamp of this restore, so it is deliberately not
			// compared; everything that describes the link itself must match.
			if got.Version != want.Version || got.Hash != want.Hash ||
				got.Source != want.Source || got.OriginalVersion != want.OriginalVersion {
				t.Errorf("lnpm.lock entry for %s = %+v, want the pre-retreat %+v", name, got, want)
			}
		}
		if len(lock.List()) != len(before) {
			t.Errorf("Expected lnpm.lock to hold %d packages, got %v", len(before), lock.List())
		}
	})
}

// TestRestoreWithoutSnapshotReportsNothingToRestore covers the criterion that a
// restore with no prior retreat says so plainly instead of failing.
func TestRestoreWithoutSnapshotReportsNothingToRestore(t *testing.T) {
	env := setupTest(t)

	projectDir := env.newProject("no-snapshot-project")

	out := captureStdout(t, func() {
		if err := cli.RunRestore(); err != nil {
			t.Errorf("Expected restore with no snapshot to succeed, got: %v", err)
		}
	})

	if !strings.Contains(out, "othing to restore") {
		t.Errorf("Expected a 'nothing to restore' message, got:\n%s", out)
	}
	env.AssertLockfileExists(projectDir, false)
	env.AssertDirectoryExists(filepath.Join(projectDir, ".lnpm"), false)
}

// TestRestoreReportsStaleVersionAndContinues covers the criterion that a package
// whose recorded version is no longer the one in the store is reported per
// package and does not abort the rest of the restore. The store is latest-wins,
// so republishing at a new version is what strands the snapshot's entry.
func TestRestoreReportsStaleVersionAndContinues(t *testing.T) {
	env := setupTest(t)

	stalePkgDir := env.simplePkg("stale-restore-pkg")
	env.simplePkg("fresh-restore-pkg")
	projectDir := env.newProject("stale-restore-project")
	env.addPkg(projectDir, "stale-restore-pkg", false, false)
	env.addPkg(projectDir, "fresh-restore-pkg", false, false)

	if err := cli.RunRetreat(true, false); err != nil {
		t.Fatalf("Failed to retreat: %v", err)
	}

	env.republish(stalePkgDir, "stale-restore-pkg", "2.0.0", "module.exports = 'v2';")
	env.chdir(projectDir)

	var err error
	out := captureStdout(t, func() { err = cli.RunRestore() })
	if err == nil {
		t.Error("Expected restore to exit non-zero when a package could not be restored")
	}

	want := "version 1.0.0 of stale-restore-pkg not found in store (latest published is 2.0.0"
	if !strings.Contains(out, want) {
		t.Errorf("Expected the report to contain %q, got:\n%s", want, out)
	}

	// The healthy package is restored regardless.
	env.AssertSymlinkExists(projectDir, "fresh-restore-pkg")
	env.AssertPackageJSON(projectDir, "fresh-restore-pkg", "file:.lnpm/fresh-restore-pkg")
	env.AssertSymlinkMissing(projectDir, "stale-restore-pkg")
	env.AssertPackageJSONMissing(projectDir, "stale-restore-pkg")

	// The snapshot survives an incomplete restore, so the user can republish the
	// stale package and run restore again.
	env.AssertFileExists(lockfile.RetreatPath(projectDir), true)
}

// TestRestoreMergesWithPackagesAddedSinceRetreat pins the chosen answer to the
// issue's open question: restore merges, and a package added since the retreat
// wins over the snapshot's entry for the same name. Nothing is removed and no
// newer add is reported as a stale version.
func TestRestoreMergesWithPackagesAddedSinceRetreat(t *testing.T) {
	env := setupTest(t)

	readdedDir := env.simplePkg("readded-pkg")
	env.simplePkg("snapshot-only-pkg")
	env.simplePkg("added-later-pkg")
	projectDir := env.newProject("merge-restore-project")
	env.addPkg(projectDir, "readded-pkg", false, false)
	env.addPkg(projectDir, "snapshot-only-pkg", false, false)

	if err := cli.RunRetreat(true, false); err != nil {
		t.Fatalf("Failed to retreat: %v", err)
	}

	// Between retreat and restore the user republishes and re-adds one of the
	// snapshot's packages, and adds a package the snapshot never saw.
	env.republish(readdedDir, "readded-pkg", "2.0.0", "module.exports = 'v2';")
	env.addPkg(projectDir, "readded-pkg", false, false)
	env.addPkg(projectDir, "added-later-pkg", false, false)

	if err := cli.RunRestore(); err != nil {
		t.Fatalf("Failed to restore: %v", err)
	}

	env.AssertLockfile(projectDir, func(lock *lockfile.LockFile) {
		readded, ok := lock.Get("readded-pkg")
		if !ok {
			t.Fatal("Expected readded-pkg to survive the restore")
		}
		if readded.Version != "2.0.0" {
			t.Errorf("Expected the newer add of readded-pkg to win at 2.0.0, got %q", readded.Version)
		}
		if !lock.Has("snapshot-only-pkg") {
			t.Error("Expected snapshot-only-pkg to be restored from the snapshot")
		}
		if !lock.Has("added-later-pkg") {
			t.Error("Expected added-later-pkg, added after the retreat, to be left alone")
		}
	})
	env.AssertSymlinkExists(projectDir, "snapshot-only-pkg")
	env.AssertSymlinkExists(projectDir, "added-later-pkg")
	env.AssertFileExists(lockfile.RetreatPath(projectDir), false)
}

// TestRestoreKeepsADevDependencyInDevDependencies is the dev-ness half of a
// faithful restore. The lock file records no dev flag, so restore recovers the
// field from package.json, which retreat has just put the original specifier
// back into. AssertPackageJSON accepts either field, so this reads the file
// directly.
func TestRestoreKeepsADevDependencyInDevDependencies(t *testing.T) {
	env := setupTest(t)

	env.simplePkg("dev-restore-pkg")
	projectDir := env.newProject("dev-restore-project")
	env.writePackageJSON(projectDir, map[string]interface{}{
		"name":            "dev-restore-project",
		"version":         "1.0.0",
		"devDependencies": map[string]interface{}{"dev-restore-pkg": "^1.0.0"},
	})
	env.addPkg(projectDir, "dev-restore-pkg", true, false)

	if err := cli.RunRetreat(true, false); err != nil {
		t.Fatalf("Failed to retreat: %v", err)
	}
	if err := cli.RunRestore(); err != nil {
		t.Fatalf("Failed to restore: %v", err)
	}

	deps, devDeps := dependencyFields(t, projectDir)
	if got := devDeps["dev-restore-pkg"]; got != "file:.lnpm/dev-restore-pkg" {
		t.Errorf("devDependencies[dev-restore-pkg] = %v, want file:.lnpm/dev-restore-pkg", got)
	}
	if _, ok := deps["dev-restore-pkg"]; ok {
		t.Error("Expected dev-restore-pkg to stay out of dependencies")
	}
}

// TestRestoreReportsAnUnknownDependencyField covers the one part of the
// pre-retreat state that is genuinely unrecoverable. A package added with --dev
// (or --pure) that package.json never held before has no original specifier in
// the lock file, and retreat drops it from package.json entirely, so by the time
// restore runs nothing on disk says which field it belonged in. Restore puts it
// in dependencies, matching plain `lnpm add`, and says so rather than moving a
// dev dependency into production dependencies without a word.
func TestRestoreReportsAnUnknownDependencyField(t *testing.T) {
	env := setupTest(t)

	env.simplePkg("orphan-dev-pkg")
	projectDir := env.newProject("orphan-dev-project")
	env.addPkg(projectDir, "orphan-dev-pkg", true, false)

	if err := cli.RunRetreat(true, false); err != nil {
		t.Fatalf("Failed to retreat: %v", err)
	}

	out := captureStdout(t, func() {
		if err := cli.RunRestore(); err != nil {
			t.Errorf("Failed to restore: %v", err)
		}
	})

	// Matched against the note itself, not just the package name and the word
	// "dependencies": both of those also appear in the success line and in the
	// npm install tip, so a looser check passes with the note removed.
	want := "orphan-dev-pkg was not in package.json before the retreat, so its field is unknown; restoring it into dependencies"
	if !strings.Contains(out, want) {
		t.Errorf("Expected the report to contain %q, got:\n%s", want, out)
	}
	deps, _ := dependencyFields(t, projectDir)
	if got := deps["orphan-dev-pkg"]; got != "file:.lnpm/orphan-dev-pkg" {
		t.Errorf("dependencies[orphan-dev-pkg] = %v, want file:.lnpm/orphan-dev-pkg", got)
	}
}

// dependencyFields returns the project's dependencies and devDependencies maps,
// for the assertions that care which of the two an entry landed in.
func dependencyFields(t *testing.T, projectDir string) (deps, devDeps map[string]interface{}) {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(projectDir, "package.json"))
	if err != nil {
		t.Fatalf("Failed to read package.json: %v", err)
	}
	var pkgJSON map[string]interface{}
	if err := json.Unmarshal(data, &pkgJSON); err != nil {
		t.Fatalf("Failed to parse package.json: %v", err)
	}
	return getMapValue(pkgJSON, "dependencies"), getMapValue(pkgJSON, "devDependencies")
}
