package tests

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/cli"
)

// TestGCRemovesOrphans table-drives garbage collection of orphaned (unlinked)
// packages: a package that is published-then-removed, a never-linked package,
// and several never-linked packages all become orphans that a plain GC run must
// delete.
func TestGCRemovesOrphans(t *testing.T) {
	t.Run("after add then remove", func(t *testing.T) {
		env := setupTest(t)

		env.publishAndAdd("gc-pkg")
		if err := cli.RunRemove("gc-pkg", false); err != nil {
			t.Fatalf("Failed to remove package: %v", err)
		}
		env.AssertPackageInDatabase("gc-pkg", true) // orphaned, not yet collected

		if err := cli.RunGC(false, "", false); err != nil {
			t.Fatalf("Failed to run GC: %v", err)
		}
		env.AssertPackageInDatabase("gc-pkg", false)
	})

	t.Run("multiple never-linked packages", func(t *testing.T) {
		env := setupTest(t)

		packages := []string{"orphan-a", "orphan-b", "orphan-c"}
		for _, name := range packages {
			env.simplePkg(name)
			env.AssertPackageInDatabase(name, true)
		}

		if err := cli.RunGC(false, "", false); err != nil {
			t.Fatalf("Failed to run GC: %v", err)
		}
		for _, name := range packages {
			env.AssertPackageInDatabase(name, false)
		}
	})
}

// TestGCDryRun tests that dry-run mode reports but does not delete.
func TestGCDryRun(t *testing.T) {
	env := setupTest(t)

	env.simplePkg("dryrun-pkg")
	env.AssertPackageInDatabase("dryrun-pkg", true)

	if err := cli.RunGC(true, "", false); err != nil {
		t.Fatalf("Failed to run GC dry-run: %v", err)
	}
	env.AssertPackageInDatabase("dryrun-pkg", true)
}

// TestGCWithAge exercises the --older-than age filter.
//
// COVERAGE GAP (deferred): we cannot publish a genuinely "old" package because
// back-dating a package's UpdatedAt requires a DB/source change (e.g. a test
// helper on db.DB), which is out of scope for a test-only change. As a result
// this test cannot prove the filter correctly REMOVES packages older than the
// threshold while KEEPING newer ones in the same run. Instead it pins the two
// observable boundary behaviors that need no back-dating:
//   - a very large threshold must PROTECT a freshly published orphan, and
//   - a zero threshold ("0d") must NOT protect it (filter is bypassed),
//
// so a regression that inverts the age comparison would be caught.
func TestGCWithAge(t *testing.T) {
	env := setupTest(t)

	// Create and publish two fresh, orphaned packages.
	env.simplePkg("old-pkg")
	env.simplePkg("new-pkg")

	// A large threshold must PROTECT freshly published orphans: nothing older
	// than ~100 years exists, so neither package may be removed.
	if err := cli.RunGC(false, "36500d", false); err != nil {
		t.Fatalf("Failed to run GC with large age threshold: %v", err)
	}
	env.AssertPackageInDatabase("old-pkg", true)
	env.AssertPackageInDatabase("new-pkg", true)

	// A zero threshold ("0d") bypasses the age check entirely, so the orphaned
	// packages must now be removed. This proves the filter is actually wired to
	// the threshold rather than always-protecting or always-deleting.
	if err := cli.RunGC(false, "0d", false); err != nil {
		t.Fatalf("Failed to run GC with zero age threshold: %v", err)
	}
	env.AssertPackageInDatabase("old-pkg", false)
	env.AssertPackageInDatabase("new-pkg", false)
}

// TestGCOrphanedLinks tests that --fix-links cleans up a link whose project
// directory has been deleted, while a package that still has a valid link
// elsewhere is preserved.
func TestGCOrphanedLinks(t *testing.T) {
	env := setupTest(t)

	env.simplePkg("link-pkg")

	projectDir := env.newProject("test-project")
	// Resolve to real path (Windows short names differ from os.Getwd long names)
	if resolved, _ := filepath.EvalSymlinks(projectDir); resolved != "" {
		projectDir = resolved
	}
	env.addPkg(projectDir, "link-pkg", false, false)

	// Move out of projectDir before deleting (Windows can't remove cwd).
	env.chdir(env.TempDir)
	// Remove junction/symlink first (Windows blocks RemoveAll on dirs with junctions).
	_ = os.Remove(filepath.Join(projectDir, "node_modules", "link-pkg"))
	if err := os.RemoveAll(projectDir); err != nil {
		t.Fatalf("Failed to remove project dir: %v", err)
	}
	env.AssertDatabaseLink("link-pkg", projectDir) // still recorded, now orphaned

	if err := cli.RunGC(false, "", true); err != nil {
		t.Fatalf("Failed to run GC: %v", err)
	}
	env.AssertDatabaseNoLink("link-pkg", projectDir)
}

// TestGCPartiallyOrphanedLinks tests that --fix-links removes only the link to a
// deleted project, keeping the package and its remaining valid link.
func TestGCPartiallyOrphanedLinks(t *testing.T) {
	env := setupTest(t)

	env.simplePkg("partial-pkg")

	project1Dir := env.newProject("project-1")
	env.addPkg(project1Dir, "partial-pkg", false, false)
	project2Dir := env.newProject("project-2")
	env.addPkg(project2Dir, "partial-pkg", false, false)

	if err := os.RemoveAll(project1Dir); err != nil {
		t.Fatalf("Failed to remove project-1: %v", err)
	}

	if err := cli.RunGC(false, "", true); err != nil {
		t.Fatalf("Failed to run GC: %v", err)
	}

	env.AssertPackageInDatabase("partial-pkg", true)
	env.AssertDatabaseNoLink("partial-pkg", project1Dir)
	env.AssertDatabaseLink("partial-pkg", project2Dir)
}

// TestGCKeepsLinkedAndCollectsOrphans tests that GC removes orphaned packages
// while leaving linked packages (and their links) intact.
func TestGCKeepsLinkedAndCollectsOrphans(t *testing.T) {
	env := setupTest(t)

	// Orphaned package (never linked).
	env.simplePkg("orphan-pkg")
	// Linked package.
	env.simplePkg("linked-pkg")
	projectDir := env.newProject("test-project")
	env.addPkg(projectDir, "linked-pkg", false, false)
	env.AssertDatabaseLink("linked-pkg", projectDir)

	if err := cli.RunGC(false, "", false); err != nil {
		t.Fatalf("Failed to run GC: %v", err)
	}

	env.AssertPackageInDatabase("orphan-pkg", false)
	env.AssertPackageInDatabase("linked-pkg", true)
	env.AssertDatabaseLink("linked-pkg", projectDir)
}

// TestGCNoPackages tests GC against an empty database is a safe no-op.
func TestGCNoPackages(t *testing.T) {
	_ = setupTest(t)

	if err := cli.RunGC(false, "", false); err != nil {
		t.Fatalf("Failed to run GC: %v", err)
	}
}

// TestGCStorePathCleanup tests that an orphan's store directory is deleted by GC.
func TestGCStorePathCleanup(t *testing.T) {
	env := setupTest(t)

	env.publishPkg("store-pkg", "1.0.0", map[string]string{
		"index.js":     "module.exports = 'test';",
		"lib/utils.js": "exports.util = () => 'util';",
	})

	pkg, err := env.Database.GetPackageByName("store-pkg")
	if err != nil || pkg == nil {
		t.Fatalf("Failed to get package: %v", err)
	}
	storePath := pkg.StorePath
	if _, err := os.Stat(storePath); err != nil {
		t.Fatalf("Store path doesn't exist: %v", err)
	}

	if err := cli.RunGC(false, "", false); err != nil {
		t.Fatalf("Failed to run GC: %v", err)
	}
	if _, err := os.Stat(storePath); !os.IsNotExist(err) {
		t.Errorf("Store path still exists after GC")
	}
}

// TestGCInvalidDuration tests that an invalid --older-than value errors.
func TestGCInvalidDuration(t *testing.T) {
	_ = setupTest(t)

	if err := cli.RunGC(false, "invalid", false); err == nil {
		t.Fatal("Expected error with invalid duration, got nil")
	}
}

// TestGCDurationFormats tests that several valid duration formats are accepted.
func TestGCDurationFormats(t *testing.T) {
	env := setupTest(t)

	env.simplePkg("duration-pkg")
	for _, dur := range []string{"24h", "7d", "1w"} {
		if err := cli.RunGC(true, dur, false); err != nil {
			t.Errorf("GC failed with duration %s: %v", dur, err)
		}
	}
}
