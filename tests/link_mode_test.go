package tests

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/cli"
	"github.com/pedrosousa13/lnpm/pkg/lockfile"
)

// TestAddLinkPointsAtSource covers the core of live-link mode: `add --link`
// points .lnpm/<pkg> at the package's source directory instead of filling it
// with a copy of the store entry.
func TestAddLinkPointsAtSource(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.simplePkg("live-lib")
	projectDir := env.newProject("live-project")

	env.addLinkedPkg(projectDir, "live-lib")

	env.AssertLiveLink(projectDir, "live-lib", pkgDir)
	env.AssertSymlinkExists(projectDir, "live-lib")
	env.AssertFileContent(filepath.Join(projectDir, "node_modules", "live-lib", "index.js"),
		"module.exports = 'live-lib';")
}

// TestAddLinkReportsLiveSource pins how `add --link` reports itself, and the
// link type it records in the database: a live link is not a hardlink or a
// copy, and the user is told which they got.
func TestAddLinkReportsLiveSource(t *testing.T) {
	env := setupTest(t)

	env.simplePkg("report-lib")
	projectDir := env.newProject("report-project")

	out := captureStdout(t, func() { env.addLinkedPkg(projectDir, "report-lib") })

	if !strings.Contains(out, "Adding report-lib@1.0.0 (linked to source)") {
		t.Errorf("Expected add to report the live link, got:\n%s", out)
	}
	if !strings.Contains(out, "Link type: link (live source)") {
		t.Errorf("Expected the live source link type, got:\n%s", out)
	}

	pkg, err := env.Database.GetPackageByName("report-lib")
	if err != nil || pkg == nil {
		t.Fatalf("report-lib not in database: %v", err)
	}
	proj, err := env.Database.GetProjectByPath(projectDir)
	if err != nil || proj == nil {
		t.Fatalf("project not in database: %v", err)
	}
	links, err := env.Database.GetLinksForProject(proj.ID)
	if err != nil {
		t.Fatalf("Failed to get links: %v", err)
	}
	for _, l := range links {
		if l.PackageID == pkg.ID && l.LinkType != "link" {
			t.Errorf("Expected recorded link type link, got %s", l.LinkType)
		}
	}
}

// TestLinkedSourceEditsAreVisibleImmediately covers the reason live-link mode
// exists: an edit to the source package reaches the consumer through
// node_modules/<pkg> with no publish, push or pull in between.
func TestLinkedSourceEditsAreVisibleImmediately(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.simplePkg("edit-lib")
	projectDir := env.newProject("edit-project")
	env.addLinkedPkg(projectDir, "edit-lib")

	// Edit the source directly. No lnpm command follows.
	env.writeFile(filepath.Join(pkgDir, "index.js"), "module.exports = 'edited';")
	env.writeFile(filepath.Join(pkgDir, "extra.js"), "module.exports = 'new file';")

	consumed := filepath.Join(projectDir, "node_modules", "edit-lib")
	env.AssertFileContent(filepath.Join(consumed, "index.js"), "module.exports = 'edited';")
	env.AssertFileContent(filepath.Join(consumed, "extra.js"), "module.exports = 'new file';")
}

// TestRemoveLinkedPackageKeepsSource covers removal of a live-linked package:
// it is torn down exactly like a copy-linked one, and - because .lnpm/<pkg> is
// now a link into the user's real source tree - the teardown must delete the
// link and nothing behind it.
func TestRemoveLinkedPackageKeepsSource(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.simplePkg("remove-live-lib")
	projectDir := env.newProject("remove-live-project")
	env.addLinkedPkg(projectDir, "remove-live-lib")

	if err := cli.RunRemove("remove-live-lib", false, false); err != nil {
		t.Fatalf("Failed to remove package: %v", err)
	}

	env.AssertSymlinkMissing(projectDir, "remove-live-lib")
	env.AssertDirectoryExists(filepath.Join(projectDir, ".lnpm", "remove-live-lib"), false)
	env.AssertPackageJSONMissing(projectDir, "remove-live-lib")
	env.AssertDatabaseNoLink("remove-live-lib", projectDir)
	env.AssertLockfileExists(projectDir, false)

	// The source package must have survived untouched.
	env.AssertDirectoryExists(pkgDir, true)
	env.AssertFileContent(filepath.Join(pkgDir, "index.js"), "module.exports = 'remove-live-lib';")
	env.AssertDirectoryExists(filepath.Join(pkgDir, "package.json"), true)
}

// TestRetreatForceRestoresLinkedPackage covers `retreat --force` on a
// live-linked package: the original specifier comes back, .lnpm/ goes away, and
// the source tree the removed link pointed at is left alone.
func TestRetreatForceRestoresLinkedPackage(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.simplePkg("retreat-live-lib")
	projectDir := env.newProject("retreat-live-project")
	env.writePackageJSON(projectDir, map[string]interface{}{
		"name":         "retreat-live-project",
		"version":      "1.0.0",
		"dependencies": map[string]interface{}{"retreat-live-lib": "^1.0.0"},
	})
	env.addLinkedPkg(projectDir, "retreat-live-lib")
	env.AssertPackageJSON(projectDir, "retreat-live-lib", "link:.lnpm/retreat-live-lib")

	if err := cli.RunRetreat(true, false); err != nil {
		t.Fatalf("Failed to retreat: %v", err)
	}

	env.AssertPackageJSON(projectDir, "retreat-live-lib", "^1.0.0")
	env.AssertDirectoryExists(filepath.Join(projectDir, ".lnpm"), false)
	env.AssertLockfileExists(projectDir, false)

	env.AssertDirectoryExists(pkgDir, true)
	env.AssertFileContent(filepath.Join(pkgDir, "index.js"), "module.exports = 'retreat-live-lib';")
}

// TestRetreatIgnoresLinkReferenceAsOriginalVersion pins that retreat treats a
// link:.lnpm/ specifier recorded as an original version the same way it treats
// file:.lnpm/ - as lnpm's own value, not the user's - so the dependency is
// dropped rather than restored to a reference that no longer resolves.
func TestRetreatIgnoresLinkReferenceAsOriginalVersion(t *testing.T) {
	env := setupTest(t)

	env.simplePkg("stale-ref-lib")
	projectDir := env.newProject("stale-ref-project")
	env.addLinkedPkg(projectDir, "stale-ref-lib")

	// Rewrite the lock entry the way an older version could have recorded it.
	env.AssertLockfile(projectDir, func(lock *lockfile.LockFile) {
		entry, ok := lock.Get("stale-ref-lib")
		if !ok {
			t.Fatal("stale-ref-lib missing from lockfile")
		}
		entry.OriginalVersion = "link:.lnpm/stale-ref-lib"
		lock.Add("stale-ref-lib", entry)
		if err := lock.Save(projectDir); err != nil {
			t.Fatalf("Failed to save lockfile: %v", err)
		}
	})

	// The preview must say the same thing the --force run then does.
	out := captureStdout(t, func() {
		if err := cli.RunRetreat(false, false); err != nil {
			t.Fatalf("Failed to preview retreat: %v", err)
		}
	})
	if !strings.Contains(out, "stale-ref-lib: will be removed from package.json") {
		t.Errorf("Expected the preview to drop the dependency, got:\n%s", out)
	}

	if err := cli.RunRetreat(true, false); err != nil {
		t.Fatalf("Failed to retreat: %v", err)
	}

	env.AssertPackageJSONMissing(projectDir, "stale-ref-lib")
}

// TestReAddOverLiveLinkKeepsSource covers re-adding on top of an existing live
// link, in both modes. Each add replaces .lnpm/<pkg>, which is the third place
// that could delete through the link into the user's source tree.
func TestReAddOverLiveLinkKeepsSource(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.simplePkg("readd-live-lib")
	projectDir := env.newProject("readd-live-project")
	env.addLinkedPkg(projectDir, "readd-live-lib")

	// Re-adding with --link replaces one live link with another.
	env.addLinkedPkg(projectDir, "readd-live-lib")
	env.AssertLiveLink(projectDir, "readd-live-lib", pkgDir)
	env.AssertFileContent(filepath.Join(pkgDir, "index.js"), "module.exports = 'readd-live-lib';")

	// Adding without --link converts the live link back to a store copy.
	env.addPkg(projectDir, "readd-live-lib", false, false)
	env.AssertStoreCopy(projectDir, "readd-live-lib")
	env.AssertPackageJSON(projectDir, "readd-live-lib", "file:.lnpm/readd-live-lib")
	env.AssertDirectoryExists(pkgDir, true)
	env.AssertFileContent(filepath.Join(pkgDir, "index.js"), "module.exports = 'readd-live-lib';")

	// And back again, over the copy.
	env.addLinkedPkg(projectDir, "readd-live-lib")
	env.AssertLiveLink(projectDir, "readd-live-lib", pkgDir)
	env.AssertFileContent(filepath.Join(pkgDir, "index.js"), "module.exports = 'readd-live-lib';")
}

// TestPullSkipsLiveLinkedPackage covers pull against a live-linked package.
// Relinking it from the store would silently swap the live link for a snapshot
// copy - the consumer would stop seeing source edits with nothing said about it
// - so pull leaves it alone and says so, in both the all and the named form.
func TestPullSkipsLiveLinkedPackage(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.simplePkg("pull-live-lib")
	projectDir := env.newProject("pull-live-project")
	env.addLinkedPkg(projectDir, "pull-live-lib")

	env.republish(pkgDir, "pull-live-lib", "2.0.0", "module.exports = 'v2';")

	env.chdir(projectDir)
	out := captureStdout(t, func() {
		if err := cli.RunPull(nil); err != nil {
			t.Fatalf("RunPull: %v", err)
		}
		if err := cli.RunPull([]string{"pull-live-lib"}); err != nil {
			t.Fatalf("RunPull by name: %v", err)
		}
	})

	if !strings.Contains(out, "live link") {
		t.Errorf("Expected pull to report the live link, got:\n%s", out)
	}
	env.AssertLiveLink(projectDir, "pull-live-lib", pkgDir)
	assertLockVersion(t, env, projectDir, "pull-live-lib", "1.0.0")
	env.AssertFileContent(filepath.Join(projectDir, "node_modules", "pull-live-lib", "index.js"),
		"module.exports = 'v2';")
}

// TestPushSkipsLiveLinkedConsumer covers push from the other side of the same
// hazard pull has: pushing relinks every consumer from the store, which for a
// live-linked consumer would quietly replace its link with a snapshot copy and
// end the live updates it was added for.
func TestPushSkipsLiveLinkedConsumer(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.simplePkg("push-live-lib")
	projectDir := env.newProject("push-live-project")
	env.addLinkedPkg(projectDir, "push-live-lib")

	env.chdir(pkgDir)
	env.writeFile(filepath.Join(pkgDir, "index.js"), "module.exports = 'pushed';")
	out := captureStdout(t, func() {
		if err := cli.RunPush(false); err != nil {
			t.Fatalf("RunPush: %v", err)
		}
	})

	if !strings.Contains(out, "live link") {
		t.Errorf("Expected push to report the live link, got:\n%s", out)
	}
	env.AssertLiveLink(projectDir, "push-live-lib", pkgDir)
	env.AssertFileContent(filepath.Join(projectDir, "node_modules", "push-live-lib", "index.js"),
		"module.exports = 'pushed';")
}

// TestAddDefaultStillCopiesFromStore pins that the default path is untouched:
// without --link, .lnpm/<pkg> is still a real directory of copied files.
func TestAddDefaultStillCopiesFromStore(t *testing.T) {
	env := setupTest(t)

	env.simplePkg("copy-lib")
	projectDir := env.newProject("copy-project")

	env.addPkg(projectDir, "copy-lib", false, false)

	env.AssertStoreCopy(projectDir, "copy-lib")
}

