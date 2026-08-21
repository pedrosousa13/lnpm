package tests

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/cli"
	"github.com/pedrosousa13/lnpm/internal/db"
)

// TestGCRemovesOrphans table-drives garbage collection of orphaned (unlinked)
// packages: a package that is published-then-removed, a never-linked package,
// and several never-linked packages all become orphans that a plain GC run must
// delete.
func TestGCRemovesOrphans(t *testing.T) {
	t.Run("after add then remove", func(t *testing.T) {
		env := setupTest(t)

		env.publishAndAdd("gc-pkg")
		if err := cli.RunRemove("gc-pkg", false, false); err != nil {
			t.Fatalf("Failed to remove package: %v", err)
		}
		env.AssertPackageInDatabase("gc-pkg", true) // orphaned, not yet collected

		if err := cli.RunGC(false, "", false, true); err != nil {
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

		if err := cli.RunGC(false, "", false, true); err != nil {
			t.Fatalf("Failed to run GC: %v", err)
		}
		for _, name := range packages {
			env.AssertPackageInDatabase(name, false)
		}
	})
}

// TestGCWithoutYesKeepsOrphans tests that a non-interactive GC without --yes
// refuses to delete: the orphan stays in the database and in the store.
func TestGCWithoutYesKeepsOrphans(t *testing.T) {
	env := setupTest(t)

	env.publishPkg("unconfirmed-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'test';",
	})
	env.AssertPackageInDatabase("unconfirmed-pkg", true)

	pkg, err := env.Database.GetPackageByName("unconfirmed-pkg")
	if err != nil || pkg == nil {
		t.Fatalf("Failed to get package: %v", err)
	}
	storePath := pkg.StorePath

	if err := cli.RunGC(false, "", false, false); err != nil {
		t.Fatalf("Failed to run GC without --yes: %v", err)
	}

	env.AssertPackageInDatabase("unconfirmed-pkg", true)
	if _, err := os.Stat(storePath); err != nil {
		t.Errorf("Store path was deleted without confirmation: %v", err)
	}
}

// TestGCDryRun tests that dry-run mode reports but does not delete, whether or
// not --yes is given.
func TestGCDryRun(t *testing.T) {
	for _, yes := range []bool{false, true} {
		name := "without --yes"
		if yes {
			name = "with --yes"
		}
		t.Run(name, func(t *testing.T) {
			env := setupTest(t)

			env.simplePkg("dryrun-pkg")
			env.AssertPackageInDatabase("dryrun-pkg", true)

			if err := cli.RunGC(true, "", false, yes); err != nil {
				t.Fatalf("Failed to run GC dry-run: %v", err)
			}
			env.AssertPackageInDatabase("dryrun-pkg", true)
		})
	}
}

// TestGCWithAge exercises the --older-than age filter.
//
// COVERAGE GAP (deferred): this test does not prove the filter correctly REMOVES
// packages older than the threshold while KEEPING newer ones in the same run.
// Back-dating a package's UpdatedAt would need a DB/source change (e.g. a test
// helper on db.DB), and the one way to get there without it - parseDuration
// falls through to time.ParseDuration, so a sub-second threshold parses and a
// wall-clock sleep would cross it - buys a slow, flaky test of untouched code.
// Instead this pins the two observable boundary behaviors that need neither:
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
	if err := cli.RunGC(false, "36500d", false, true); err != nil {
		t.Fatalf("Failed to run GC with large age threshold: %v", err)
	}
	env.AssertPackageInDatabase("old-pkg", true)
	env.AssertPackageInDatabase("new-pkg", true)

	// A zero threshold ("0d") bypasses the age check entirely, so the orphaned
	// packages must now be removed. This proves the filter is actually wired to
	// the threshold rather than always-protecting or always-deleting.
	if err := cli.RunGC(false, "0d", false, true); err != nil {
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

	if err := cli.RunGC(false, "", true, true); err != nil {
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

	if err := cli.RunGC(false, "", true, true); err != nil {
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

	if err := cli.RunGC(false, "", false, true); err != nil {
		t.Fatalf("Failed to run GC: %v", err)
	}

	env.AssertPackageInDatabase("orphan-pkg", false)
	env.AssertPackageInDatabase("linked-pkg", true)
	env.AssertDatabaseLink("linked-pkg", projectDir)
}

// TestGCCollectsASupersededVersion covers the version-level sweep the store
// needs now that publishing keeps the version published before it. The old
// version is reached by nothing — its link was carried across to the version
// the tag names — so both its record and its files go, while the version the
// project actually consumes stays exactly where it is.
func TestGCCollectsASupersededVersion(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.simplePkg("superseded-pkg")
	projectDir := env.newProject("consumer")
	env.addPkg(projectDir, "superseded-pkg", false, false)

	first, err := env.Database.GetPackageByName("superseded-pkg")
	if err != nil || first == nil {
		t.Fatalf("Failed to get the first version: %v", err)
	}

	env.republish(pkgDir, "superseded-pkg", "2.0.0", "module.exports = 'v2';")
	second, err := env.Database.GetPackageByName("superseded-pkg")
	if err != nil || second == nil {
		t.Fatalf("Failed to get the second version: %v", err)
	}
	if second.ContentHash == first.ContentHash {
		t.Fatal("the republish produced the same content, so there is no superseded version")
	}

	if err := cli.RunGC(false, "", false, true); err != nil {
		t.Fatalf("Failed to run GC: %v", err)
	}

	if stale, _ := env.Database.GetPackageByHash("superseded-pkg", first.ContentHash); stale != nil {
		t.Error("GC kept the record of a version nothing reaches")
	}
	env.AssertDirectoryExists(first.StorePath, false)

	env.AssertPackageInDatabase("superseded-pkg", true)
	env.AssertDirectoryExists(second.StorePath, true)
	env.AssertDatabaseLink("superseded-pkg", projectDir)
}

// TestGCKeepsAVersionATagPins is the reachability rule's other half: a tag is a
// root, so a version nothing links to survives for as long as a tag names it.
//
// The version tagged only latest is collected in the same run, and that
// asymmetry is deliberate. Publish moves latest onto every package it writes, so
// treating it as a root would leave gc nothing it could ever collect and quietly
// retire the command. A tag a user set is a decision; latest is a side effect.
func TestGCKeepsAVersionATagPins(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.simplePkg("pinned-pkg")
	first, err := env.Database.GetPackageByName("pinned-pkg")
	if err != nil || first == nil {
		t.Fatalf("Failed to get the first version: %v", err)
	}

	env.republish(pkgDir, "pinned-pkg", "2.0.0", "module.exports = 'v2';")
	second, err := env.Database.GetPackageByName("pinned-pkg")
	if err != nil || second == nil {
		t.Fatalf("Failed to get the second version: %v", err)
	}

	if err := env.Database.SetTag("pinned-pkg", "beta", first.ContentHash); err != nil {
		t.Fatalf("Failed to tag the first version: %v", err)
	}

	if err := cli.RunGC(false, "", false, true); err != nil {
		t.Fatalf("Failed to run GC: %v", err)
	}

	pinned, err := env.Database.GetPackageByHash("pinned-pkg", first.ContentHash)
	if err != nil {
		t.Fatalf("Failed to look up the tagged version: %v", err)
	}
	if pinned == nil {
		t.Error("GC collected a version a tag names")
	}
	env.AssertDirectoryExists(first.StorePath, true)

	tags, err := env.Database.TagsForPackage("pinned-pkg")
	if err != nil {
		t.Fatalf("Failed to read tags: %v", err)
	}
	if tags["beta"] != first.ContentHash {
		t.Errorf("the beta tag points at %q, want the version it named before GC ran", tags["beta"])
	}

	// Nothing links the version latest names, and latest alone does not keep it.
	env.AssertDirectoryExists(second.StorePath, false)

	// So this run leaves the package reachable by its beta tag and by nothing
	// else: collecting what latest named cleared the name index with it. ADR-0002
	// discloses that a version can outlive its package's name, and this is the
	// run that produces it - asserted here rather than left implied, because it
	// is the one state db.go's two comments about the default tag say publishing
	// never creates.
	byName, err := env.Database.GetPackageByName("pinned-pkg")
	if err != nil {
		t.Fatalf("Failed to look pinned-pkg up by name: %v", err)
	}
	if byName != nil {
		t.Errorf("pinned-pkg still resolves by name to %s, want nothing after its latest was collected", byName.ContentHash)
	}
	if tags[db.DefaultTag] != "" {
		t.Errorf("the %s tag still names %q, want it gone with the version it named", db.DefaultTag, tags[db.DefaultTag])
	}
	resolved, err := env.Database.ResolveTag("pinned-pkg", "beta")
	if err != nil || resolved == nil {
		t.Fatalf("The beta tag no longer resolves: %v, %v", resolved, err)
	}

	// The next publish of the name puts the default tag back, which is the only
	// thing that clears the state.
	env.republish(pkgDir, "pinned-pkg", "3.0.0", "module.exports = 'v3';")
	if pkg, _ := env.Database.GetPackageByName("pinned-pkg"); pkg == nil {
		t.Error("republishing did not make pinned-pkg reachable by name again")
	}
}

// TestGCNoPackages tests GC against an empty database is a safe no-op.
func TestGCNoPackages(t *testing.T) {
	_ = setupTest(t)

	if err := cli.RunGC(false, "", false, true); err != nil {
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

	if err := cli.RunGC(false, "", false, true); err != nil {
		t.Fatalf("Failed to run GC: %v", err)
	}
	if _, err := os.Stat(storePath); !os.IsNotExist(err) {
		t.Errorf("Store path still exists after GC")
	}
}

// TestGCInvalidDuration tests that an invalid --older-than value errors.
func TestGCInvalidDuration(t *testing.T) {
	_ = setupTest(t)

	if err := cli.RunGC(false, "invalid", false, false); err == nil {
		t.Fatal("Expected error with invalid duration, got nil")
	}
}

// TestGCDurationFormats tests that several valid duration formats are accepted.
func TestGCDurationFormats(t *testing.T) {
	env := setupTest(t)

	env.simplePkg("duration-pkg")
	for _, dur := range []string{"24h", "7d", "1w"} {
		if err := cli.RunGC(true, dur, false, false); err != nil {
			t.Errorf("GC failed with duration %s: %v", dur, err)
		}
	}
}
