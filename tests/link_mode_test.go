package tests

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/cli"
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

	env.AssertDatabaseLinkType(projectDir, "report-lib", "link")
}

// TestAddMultipleLinkReportsLiveSource covers the multi-package path, which
// resolves and links in parallel and reports separately from the single-package
// one. It must say the same things: that the packages are linked to their
// sources, and that each got a live link rather than a copy.
func TestAddMultipleLinkReportsLiveSource(t *testing.T) {
	env := setupTest(t)

	libA := env.simplePkg("multi-live-a")
	libB := env.simplePkg("multi-live-b")
	projectDir := env.newProject("multi-live-project")

	out := captureStdout(t, func() {
		env.chdir(projectDir)
		if err := cli.RunAddMultiple([]string{"multi-live-a", "multi-live-b"}, false, false, false, true); err != nil {
			t.Fatalf("add --link failed: %v", err)
		}
	})

	if !strings.Contains(out, "(linked to source)") {
		t.Errorf("Expected the multi-package add to report linking to source, got:\n%s", out)
	}
	for _, name := range []string{"multi-live-a", "multi-live-b"} {
		if !strings.Contains(out, "Added "+name+"@1.0.0") {
			t.Errorf("Expected %s in the summary, got:\n%s", name, out)
		}
	}
	// One "Link type" line per package, both naming the live source link. The
	// bare type name "link" - which is what this path used to print - would not
	// match.
	if got := strings.Count(out, "Link type: link (live source)"); got != 2 {
		t.Errorf("Expected 2 live source link type lines, got %d in:\n%s", got, out)
	}

	env.AssertLiveLink(projectDir, "multi-live-a", libA)
	env.AssertLiveLink(projectDir, "multi-live-b", libB)
	env.AssertDatabaseLinkType(projectDir, "multi-live-a", "link")
	env.AssertDatabaseLinkType(projectDir, "multi-live-b", "link")
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
	env.AssertFileExists(filepath.Join(pkgDir, "package.json"), true)
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

// TestAddDefaultStillCopiesFromStore pins that the default path is untouched:
// without --link, .lnpm/<pkg> is still a real directory of copied files.
//
// This is the guarantee TestAddDefaultProtocolUnchanged (link_protocol_test.go)
// cannot express. That test reads package.json, and the protocol string there
// was already correct before --link pointed at the source at all: it stayed
// "file:.lnpm/<pkg>" whether .lnpm/<pkg> held a copy or a link. Only looking at
// .lnpm/<pkg> itself distinguishes the two.
func TestAddDefaultStillCopiesFromStore(t *testing.T) {
	env := setupTest(t)

	env.simplePkg("copy-lib")
	projectDir := env.newProject("copy-project")

	env.addPkg(projectDir, "copy-lib", false, false)

	env.AssertStoreCopy(projectDir, "copy-lib")
}
