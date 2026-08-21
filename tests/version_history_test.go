// Version history and rollback: `lnpm list <pkg> --versions` and
// `lnpm add <pkg>@<hash>`, end to end through the real store.
//
// The store retains every version a publish wrote until gc collects it, so the
// history is real data rather than a report assembled from somewhere else. These
// tests drive the commands a user would run and read what they print, because
// what the listing shows is exactly what the user will type back into add.
package tests

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/cli"
	"github.com/pedrosousa13/lnpm/internal/db"
	"github.com/pedrosousa13/lnpm/pkg/lockfile"
)

// rowFor returns the single line of out that mentions the given short hash,
// failing when none or several do. The per-version facts are laid out one row
// each, so asserting that a fact is on the right row is the only way to tell
// "the listing says 1.2.0 is linked" from "the listing mentions linking
// somewhere".
func rowFor(t *testing.T, out, shortHash string) string {
	t.Helper()

	var found []string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, shortHash) {
			found = append(found, line)
		}
	}
	if len(found) != 1 {
		t.Fatalf("expected exactly one row mentioning %s, found %d, output was:\n%s", shortHash, len(found), out)
	}
	return found[0]
}

// short is the eight-character hash `lnpm list --versions` prints and a user
// types back into `lnpm add`.
func short(hash string) string {
	if len(hash) > 8 {
		return hash[:8]
	}
	return hash
}

// TestListVersionsListsEveryRetainedVersion covers the first acceptance
// criterion: every retained version of a package, with its hash, its semver
// version and when it was published.
//
// The version the store has moved on from is the whole point. `lnpm list
// --store` and `lnpm status` already show what is there; what neither answers is
// "what can I roll back to", and that question is about the rows the default tag
// no longer names.
func TestListVersionsListsEveryRetainedVersion(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.publishPkg("history-lib", "1.2.0", map[string]string{
		"index.js": "module.exports = 'v1';",
	})
	first, err := env.Database.GetPackageByName("history-lib")
	if err != nil || first == nil {
		t.Fatalf("Failed to read the first version: %v", err)
	}

	env.republish(pkgDir, "history-lib", "1.3.0", "module.exports = 'v2';")
	second, err := env.Database.GetPackageByName("history-lib")
	if err != nil || second == nil {
		t.Fatalf("Failed to read the second version: %v", err)
	}
	if second.ContentHash == first.ContentHash {
		t.Fatal("the republish produced the same content, so there is no history to list")
	}

	out := captureStdout(t, func() {
		if err := cli.RunListVersions("history-lib"); err != nil {
			t.Errorf("RunListVersions() error = %v", err)
		}
	})

	firstRow := rowFor(t, out, short(first.ContentHash))
	if !strings.Contains(firstRow, "1.2.0") {
		t.Errorf("the row for the superseded version does not carry its version:\n%s", firstRow)
	}
	secondRow := rowFor(t, out, short(second.ContentHash))
	if !strings.Contains(secondRow, "1.3.0") {
		t.Errorf("the row for the current version does not carry its version:\n%s", secondRow)
	}

	for _, row := range []string{firstRow, secondRow} {
		if !strings.Contains(row, "published") {
			t.Errorf("a version row carries no publish time:\n%s", row)
		}
	}
	if !strings.Contains(out, "history-lib") {
		t.Errorf("RunListVersions did not name the package, output was:\n%s", out)
	}
	assertNoRawGlyphs(t, out)
}

// TestListVersionsOrdersTheHistoryNewestFirst pins the order a reader is
// entitled to assume. A history is read top-down from the release that just
// broke back to the one that worked, so the current version has to be the first
// row rather than wherever bolt kept it.
func TestListVersionsOrdersTheHistoryNewestFirst(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.publishPkg("ordered-lib", "1.0.0", map[string]string{
		"index.js": "module.exports = 'v1';",
	})
	first, _ := env.Database.GetPackageByName("ordered-lib")
	env.republish(pkgDir, "ordered-lib", "2.0.0", "module.exports = 'v2';")
	second, _ := env.Database.GetPackageByName("ordered-lib")
	if first == nil || second == nil {
		t.Fatal("Failed to read both versions")
	}

	out := captureStdout(t, func() {
		if err := cli.RunListVersions("ordered-lib"); err != nil {
			t.Errorf("RunListVersions() error = %v", err)
		}
	})

	newest := strings.Index(out, short(second.ContentHash))
	oldest := strings.Index(out, short(first.ContentHash))
	if newest < 0 || oldest < 0 {
		t.Fatalf("RunListVersions did not list both versions, output was:\n%s", out)
	}
	if newest > oldest {
		t.Errorf("RunListVersions listed the superseded version above the current one, output was:\n%s", out)
	}
}

// TestListVersionsMarksTheVersionAProjectLinks covers the annotation the issue's
// example carries: a row says which projects are on that build. Without it the
// listing answers "what is in the store" rather than "what would rolling back
// change", which is the question the user opened it with.
//
// The clause must land on the row it describes and on no other, so this asserts
// its absence from the other row as well.
func TestListVersionsMarksTheVersionAProjectLinks(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.publishPkg("linked-history-lib", "1.0.0", map[string]string{
		"index.js": "module.exports = 'v1';",
	})
	first, _ := env.Database.GetPackageByName("linked-history-lib")
	env.republish(pkgDir, "linked-history-lib", "2.0.0", "module.exports = 'v2';")
	second, _ := env.Database.GetPackageByName("linked-history-lib")
	if first == nil || second == nil {
		t.Fatal("Failed to read both versions")
	}

	projectDir := env.newProject("consumer-app")
	env.addPkg(projectDir, "linked-history-lib", false, false)

	out := captureStdout(t, func() {
		if err := cli.RunListVersions("linked-history-lib"); err != nil {
			t.Errorf("RunListVersions() error = %v", err)
		}
	})

	currentRow := rowFor(t, out, short(second.ContentHash))
	if !strings.Contains(currentRow, "consumer-app") {
		t.Errorf("the row for the version the project consumes does not name it:\n%s", currentRow)
	}
	supersededRow := rowFor(t, out, short(first.ContentHash))
	if strings.Contains(supersededRow, "consumer-app") {
		t.Errorf("the row for the superseded version claims the project is on it:\n%s", supersededRow)
	}
}

// TestListVersionsNamesTheTagsOfEachVersion pins that the listing says which
// channel names which build. A user choosing a version to roll back to has to be
// able to tell the current release from the rest, and `latest` is the only thing
// in the store that says which one that is.
func TestListVersionsNamesTheTagsOfEachVersion(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.publishPkg("tagged-history-lib", "1.0.0", map[string]string{
		"index.js": "module.exports = 'v1';",
	})
	first, _ := env.Database.GetPackageByName("tagged-history-lib")
	if first == nil {
		t.Fatal("Failed to read the first version")
	}
	if err := cli.RunTag("tagged-history-lib", "stable", false); err != nil {
		t.Fatalf("Failed to tag the first version: %v", err)
	}

	env.republish(pkgDir, "tagged-history-lib", "2.0.0", "module.exports = 'v2';")
	second, _ := env.Database.GetPackageByName("tagged-history-lib")
	if second == nil {
		t.Fatal("Failed to read the second version")
	}

	out := captureStdout(t, func() {
		if err := cli.RunListVersions("tagged-history-lib"); err != nil {
			t.Errorf("RunListVersions() error = %v", err)
		}
	})

	supersededRow := rowFor(t, out, short(first.ContentHash))
	if !strings.Contains(supersededRow, "stable") {
		t.Errorf("the row for the version tagged stable does not say so:\n%s", supersededRow)
	}
	currentRow := rowFor(t, out, short(second.ContentHash))
	if !strings.Contains(currentRow, db.DefaultTag) {
		t.Errorf("the row for the current version does not name the %s tag:\n%s", db.DefaultTag, currentRow)
	}
	if strings.Contains(supersededRow, db.DefaultTag) {
		t.Errorf("the row for the superseded version claims the %s tag:\n%s", db.DefaultTag, supersededRow)
	}
}

// TestListVersionsOnAnUnknownPackageFails pins that a name the store never held
// is an error rather than an empty listing. An empty history and a mistyped name
// look identical on screen, and only one of them is worth acting on.
func TestListVersionsOnAnUnknownPackageFails(t *testing.T) {
	setupTest(t)

	err := cli.RunListVersions("no-such-lib")
	if err == nil {
		t.Fatal("RunListVersions on an unknown package returned nil, want an error")
	}
	if !strings.Contains(err.Error(), "no-such-lib") {
		t.Errorf("RunListVersions error = %v, want it to name the package", err)
	}
}

// TestListVersionsWithoutAPackageNameFails pins that the flag says what it needs
// rather than listing the history of a package called "". A history is one
// package's; there is nothing sensible for the flag to do without a name.
func TestListVersionsWithoutAPackageNameFails(t *testing.T) {
	setupTest(t)

	err := cli.RunListVersions("")
	if err == nil {
		t.Fatal("RunListVersions with no package name returned nil, want an error")
	}
	if !strings.Contains(err.Error(), "--versions") {
		t.Errorf("RunListVersions error = %v, want it to point at the flag", err)
	}
}

// TestAddByHashLinksAHistoricalBuild covers the second acceptance criterion, and
// the motivating story whole: a consumer that a new release broke is put back on
// the build that worked, without republishing anything from a git checkout.
//
// The hash is the eight-character one the listing prints, because that is what a
// user copies out of it.
func TestAddByHashLinksAHistoricalBuild(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.publishPkg("rollback-lib", "1.2.0", map[string]string{
		"index.js": "module.exports = 'working';",
	})
	working, err := env.Database.GetPackageByName("rollback-lib")
	if err != nil || working == nil {
		t.Fatalf("Failed to read the working version: %v", err)
	}

	projectDir := env.newProject("broken-app")
	env.addPkg(projectDir, "rollback-lib", false, false)

	// The release that breaks the consumer.
	env.republish(pkgDir, "rollback-lib", "1.3.0", "module.exports = 'broken';")
	env.chdir(projectDir)
	env.addPkg(projectDir, "rollback-lib", false, false)
	env.AssertLinkedFileContent(projectDir, "rollback-lib", "index.js", "module.exports = 'broken';")

	// The rollback.
	env.addPkg(projectDir, "rollback-lib@"+short(working.ContentHash), false, false)

	env.AssertLinkedFileContent(projectDir, "rollback-lib", "index.js", "module.exports = 'working';")
	env.AssertLockfile(projectDir, func(lock *lockfile.LockFile) {
		entry, ok := lock.Get("rollback-lib")
		if !ok {
			t.Fatal("rollback-lib is not in the lock file after the rollback")
		}
		if entry.Version != "1.2.0" {
			t.Errorf("the lock file records version %s, want the 1.2.0 the rollback asked for", entry.Version)
		}
		if entry.Hash != working.ContentHash {
			t.Errorf("the lock file records hash %s, want %s", entry.Hash, working.ContentHash)
		}
	})

	// The link has to move with it, or push, remove and status all still believe
	// the project is on the broken build.
	links, err := env.Database.GetLinksForPackage(working.ID)
	if err != nil {
		t.Fatalf("Failed to read the links of the rolled-back version: %v", err)
	}
	if len(links) != 1 {
		t.Errorf("the rolled-back version has %d links, want exactly the one project on it", len(links))
	}
}

// TestAddBySupersededVersionLinksThatBuild is the same rollback by the other
// identifier the listing prints. A user who has just read a version number off a
// row will type that, not a hash, and refusing it would mean showing an
// identifier and then declining to accept it.
func TestAddBySupersededVersionLinksThatBuild(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.publishPkg("version-rollback-lib", "1.2.0", map[string]string{
		"index.js": "module.exports = 'working';",
	})
	env.republish(pkgDir, "version-rollback-lib", "1.3.0", "module.exports = 'broken';")

	projectDir := env.newProject("version-rollback-app")
	env.addPkg(projectDir, "version-rollback-lib", false, false)
	env.AssertLinkedFileContent(projectDir, "version-rollback-lib", "index.js", "module.exports = 'broken';")

	env.addPkg(projectDir, "version-rollback-lib@1.2.0", false, false)
	env.AssertLinkedFileContent(projectDir, "version-rollback-lib", "index.js", "module.exports = 'working';")
}

// TestAddWithLinkRefusesAVersionOrAHash pins the one silent wrong resolution
// widening the spec selector could introduce.
//
// --link points .lnpm/<pkg> at the source directory, which holds the working
// tree; a version and a hash each name a build in the store. Before this
// selector existed, both specs hard-failed with --link because neither resolved
// at all. Now they resolve, and an add that honoured --link anyway would
// live-link the working tree while printing the historical version number it
// resolved to - the same contradiction --link with a dist-tag is refused for,
// and sharper, because a hash names a build more specifically than a tag does.
func TestAddWithLinkRefusesAVersionOrAHash(t *testing.T) {
	for _, tc := range []struct {
		name string
		// spec is built from the superseded build once it is published.
		spec func(superseded *db.Package) string
	}{
		{"version", func(p *db.Package) string { return "live-build-lib@" + p.Version }},
		{"hash", func(p *db.Package) string { return "live-build-lib@" + short(p.ContentHash) }},
	} {
		for _, path := range []string{"single", "parallel"} {
			t.Run(tc.name+"/"+path, func(t *testing.T) {
				env := setupTest(t)

				pkgDir := env.publishPkg("live-build-lib", "1.0.0", map[string]string{
					"index.js": "module.exports = 'working';",
				})
				superseded, err := env.Database.GetPackageByName("live-build-lib")
				if err != nil || superseded == nil {
					t.Fatalf("Failed to read the superseded version: %v", err)
				}
				env.republish(pkgDir, "live-build-lib", "2.0.0", "module.exports = 'broken';")
				env.simplePkg("live-plain-lib")

				projectDir := env.newProject("live-build-project")
				env.chdir(projectDir)

				specs := []string{tc.spec(superseded)}
				if path == "parallel" {
					specs = append(specs, "live-plain-lib")
				}

				captureStdout(t, func() {
					err = cli.RunAddMultiple(specs, false, false, false, true)
				})
				if err == nil {
					t.Fatalf("Adding %s with --link succeeded, want a refusal", specs[0])
				}

				// Nothing about the package is linked, whichever path ran: a
				// live link here would resolve to the working tree while the
				// summary named the superseded build.
				env.AssertDirectoryExists(filepath.Join(projectDir, ".lnpm", "live-build-lib"), false)
				env.AssertPackageJSONMissing(projectDir, "live-build-lib")
			})
		}
	}
}

// TestAddByAnUnknownSpecNamesTheRetainedVersions pins the message at the dead
// end. It used to report what `latest` names as though it were all the store
// held, which is misleading now that the record the user is after may be sitting
// one row down - and this is the moment they need to be told it is.
func TestAddByAnUnknownSpecNamesTheRetainedVersions(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.publishPkg("deadend-lib", "1.0.0", map[string]string{
		"index.js": "module.exports = 'v1';",
	})
	env.republish(pkgDir, "deadend-lib", "2.0.0", "module.exports = 'v2';")

	projectDir := env.newProject("deadend-app")
	env.chdir(projectDir)

	err := cli.RunAdd("deadend-lib@9.9.9", false, false, false)
	if err == nil {
		t.Fatal("add deadend-lib@9.9.9 succeeded, want a refusal")
	}
	for _, want := range []string{"1.0.0", "2.0.0"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal is %v, want it to name the retained version %s", err, want)
		}
	}
}

// TestAddWithNoVersionResolvesTheMostRecentlyPublished covers the third
// acceptance criterion, which the store's move to keeping every version had to
// leave alone. Nothing pinned the version an unversioned add lands on - the
// existing test asserts the symlink and the package.json reference, both of
// which a resolution to the wrong build would satisfy.
func TestAddWithNoVersionResolvesTheMostRecentlyPublished(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.publishPkg("unversioned-lib", "1.0.0", map[string]string{
		"index.js": "module.exports = 'v1';",
	})
	env.republish(pkgDir, "unversioned-lib", "2.0.0", "module.exports = 'v2';")

	projectDir := env.newProject("unversioned-app")
	env.addPkg(projectDir, "unversioned-lib", false, false)

	env.AssertLinkedFileContent(projectDir, "unversioned-lib", "index.js", "module.exports = 'v2';")
	env.AssertLockfile(projectDir, func(lock *lockfile.LockFile) {
		entry, ok := lock.Get("unversioned-lib")
		if !ok {
			t.Fatal("unversioned-lib is not in the lock file")
		}
		if entry.Version != "2.0.0" {
			t.Errorf("an unversioned add landed on %s, want the most recently published 2.0.0", entry.Version)
		}
	})
}

// TestGCKeepsAVersionARollbackLinks covers the fourth acceptance criterion's
// second half, which only became reachable once a project could be put back on a
// superseded build: gc removes the historical versions nothing links, and must
// leave the historical version a rollback does link exactly where it is.
//
// Before rollback existed, a superseded version had no links by construction -
// moving the default tag carries them forward - so "an old version a project is
// on" was a state the store could not be in and no test could cover.
func TestGCKeepsAVersionARollbackLinks(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.publishPkg("gc-rollback-lib", "1.0.0", map[string]string{
		"index.js": "module.exports = 'working';",
	})
	working, err := env.Database.GetPackageByName("gc-rollback-lib")
	if err != nil || working == nil {
		t.Fatalf("Failed to read the working version: %v", err)
	}
	env.republish(pkgDir, "gc-rollback-lib", "2.0.0", "module.exports = 'broken';")
	env.republish(pkgDir, "gc-rollback-lib", "3.0.0", "module.exports = 'newest';")
	projectDir := env.newProject("gc-rollback-app")
	env.addPkg(projectDir, "gc-rollback-lib@"+short(working.ContentHash), false, false)

	// Everything else the store holds for this name is unlinked and untagged, so
	// the run has something to collect as well as something to keep.
	before, err := env.Database.GetPackageVersions("gc-rollback-lib")
	if err != nil {
		t.Fatalf("Failed to read the history: %v", err)
	}
	if len(before) != 3 {
		t.Fatalf("the store holds %d versions of gc-rollback-lib, want the 3 that were published", len(before))
	}

	if err := cli.RunGC(false, "", false, true); err != nil {
		t.Fatalf("Failed to run GC: %v", err)
	}

	kept, err := env.Database.GetPackageByHash("gc-rollback-lib", working.ContentHash)
	if err != nil {
		t.Fatalf("Failed to look up the rolled-back version: %v", err)
	}
	if kept == nil {
		t.Fatal("GC collected the historical version the project is linked to")
	}
	env.AssertDirectoryExists(kept.StorePath, true)
	env.AssertFileExists(filepath.Join(projectDir, ".lnpm", "gc-rollback-lib", "index.js"), true)

	after, err := env.Database.GetPackageVersions("gc-rollback-lib")
	if err != nil {
		t.Fatalf("Failed to re-read the history: %v", err)
	}
	if len(after) != 1 {
		t.Errorf("the store holds %d versions after gc, want only the one the project links", len(after))
	}
}
