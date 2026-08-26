// A pinned link, end to end: `lnpm add mylib@<hash>` and `lnpm add mylib@<ver>`
// link a project to one build and to no channel, and nothing but the user moves
// it off. ADR-0006 records the decisions these tests pin.
//
// The commands are driven the way a user drives them, through the real store,
// because the whole defect #300 reports is one command quietly undoing another
// command's work - which only shows up when both are actually run.
package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/cli"
	"github.com/pedrosousa13/lnpm/internal/db"
	"github.com/pedrosousa13/lnpm/pkg/lockfile"
)

// linkFor returns the link row a project holds for a package name, failing when
// it holds none. A project holds one row per name, so the name identifies it
// however many versions of that name the store retains.
func linkFor(t *testing.T, env *TestEnvironment, projectDir, name string) *db.Link {
	t.Helper()

	proj, err := env.Database.GetProjectByPath(projectDir)
	if err != nil || proj == nil {
		t.Fatalf("Failed to read the project at %s: %v", projectDir, err)
	}
	links, err := env.Database.GetLinksForProject(proj.ID)
	if err != nil {
		t.Fatalf("Failed to read the project's links: %v", err)
	}
	packages, err := env.Database.ListPackages()
	if err != nil {
		t.Fatalf("Failed to list packages: %v", err)
	}
	nameByID := make(map[int64]string, len(packages))
	for _, pkg := range packages {
		nameByID[pkg.ID] = pkg.Name
	}
	for _, l := range links {
		if nameByID[l.PackageID] == name {
			return l
		}
	}
	t.Fatalf("the project at %s holds no link for %s", projectDir, name)
	return nil
}

// rollBackTo publishes a second version of name over pkgDir, then rolls
// projectDir back onto the first build with the spec given. It returns the
// version record the project ends up on, which is the one the store must still
// hold at the end of every test here.
func rollBackTo(t *testing.T, env *TestEnvironment, pkgDir, projectDir, name, spec string) *db.Package {
	t.Helper()

	env.chdir(projectDir)
	if err := cli.RunAdd(name+"@"+spec, false, false, false); err != nil {
		t.Fatalf("Failed to roll %s back to %s: %v", name, spec, err)
	}
	return packageOnHash(t, env, name, lockEntry(t, env, projectDir, name).Hash)
}

// packageOnHash returns the retained version of name carrying hash, failing when
// the store no longer holds it. Resolving by name would answer with whatever the
// default tag points at, which for a rolled-back build is the wrong record.
func packageOnHash(t *testing.T, env *TestEnvironment, name, hash string) *db.Package {
	t.Helper()

	pkg, err := env.Database.GetPackageByHash(name, hash)
	if err != nil {
		t.Fatalf("Failed to look up %s@%s: %v", name, hash, err)
	}
	if pkg == nil {
		t.Fatalf("the store no longer holds %s at hash %s", name, hash)
	}
	return pkg
}

// pinnedFixture publishes two versions of name, links projectDir to the second
// and then rolls it back onto the first, by hash unless spec says otherwise. It
// returns the package source directory and the build the project is pinned to.
func pinnedFixture(t *testing.T, env *TestEnvironment, name, projectDir string) (string, *db.Package) {
	t.Helper()

	pkgDir := env.publishPkg(name, "1.0.0", map[string]string{
		"index.js": "module.exports = 'the build that works';",
	})
	first, err := env.Database.GetPackageByName(name)
	if err != nil || first == nil {
		t.Fatalf("Failed to read the first build of %s: %v", name, err)
	}

	env.addPkg(projectDir, name, false, false)
	env.republish(pkgDir, name, "2.0.0", "module.exports = 'the broken release';")

	pinned := rollBackTo(t, env, pkgDir, projectDir, name, first.ContentHash[:8])
	if pinned.ID != first.ID {
		t.Fatalf("the rollback landed on package %d, not the first build %d", pinned.ID, first.ID)
	}
	return pkgDir, pinned
}

// TestAddByHashPinsTheLink covers the first acceptance criterion: an add that
// names a build records a link distinguishable from an ordinary add, rather than
// sharing its empty-tag representation.
func TestAddByHashPinsTheLink(t *testing.T) {
	env := setupTest(t)

	projectDir := env.newProject("hash-pinner")
	_, pinned := pinnedFixture(t, env, "hash-pin-lib", projectDir)

	link := linkFor(t, env, projectDir, "hash-pin-lib")
	if !link.Pinned {
		t.Error("an add by content hash left the link unpinned, so the next pull moves the project off the build it rolled back to")
	}
	if link.PackageID != pinned.ID {
		t.Errorf("the link names package %d, want the rolled-back %d", link.PackageID, pinned.ID)
	}
}

// TestAddByVersionPinsTheLink is ADR-0006's second ratified decision. A version
// and a hash both name a build rather than a channel, and resolveAddSpec returns
// an empty tag for both, so the pin keys off how the spec resolved. A version is
// the spelling most people type, so this is the common path rather than the
// exotic one.
func TestAddByVersionPinsTheLink(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.publishPkg("version-pin-lib", "1.0.0", map[string]string{
		"index.js": "module.exports = 'the build that works';",
	})
	projectDir := env.newProject("version-pinner")
	env.addPkg(projectDir, "version-pin-lib", false, false)
	env.republish(pkgDir, "version-pin-lib", "2.0.0", "module.exports = 'the broken release';")

	env.chdir(projectDir)
	if err := cli.RunAdd("version-pin-lib@1.0.0", false, false, false); err != nil {
		t.Fatalf("Failed to roll back by version: %v", err)
	}

	if !linkFor(t, env, projectDir, "version-pin-lib").Pinned {
		t.Error("an add by exact version left the link unpinned")
	}
	assertLockVersion(t, env, projectDir, "version-pin-lib", "1.0.0")
}

// TestAddByTagDoesNotPin holds the line the pin must not cross. A tag is a
// channel, so a project that asked for one is meant to be carried along it: a
// pin there would freeze a consumer on whatever build the tag named the day they
// added it.
func TestAddByTagDoesNotPin(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.publishPkg("tag-pin-lib", "1.0.0", map[string]string{
		"index.js": "module.exports = 'stable';",
	})
	env.chdir(pkgDir)
	env.writeFile(pkgDir+"/index.js", "module.exports = 'experimental';")
	env.writeFile(pkgDir+"/package.json", `{"name":"tag-pin-lib","version":"2.0.0-beta.1"}`)
	if err := cli.RunPublishTagged(false, false, false, false, "beta"); err != nil {
		t.Fatalf("Failed to publish the beta build: %v", err)
	}

	projectDir := env.newProject("tag-follower")
	env.addPkg(projectDir, "tag-pin-lib@beta", false, false)

	if linkFor(t, env, projectDir, "tag-pin-lib").Pinned {
		t.Error("an add under a dist-tag pinned the link, which would stop the channel ever moving the project again")
	}
}

// TestAddWithNoBuildIdentifierUnpins is ADR-0006's third ratified decision, and
// the case that silently does nothing if InsertLink's in-place update does not
// carry the pin: the pinned build is still the current record here, which is
// exactly when a user reaches for the unpin.
func TestAddWithNoBuildIdentifierUnpins(t *testing.T) {
	env := setupTest(t)

	env.publishPkg("unpin-lib", "1.0.0", map[string]string{
		"index.js": "module.exports = 'v1';",
	})
	current, err := env.Database.GetPackageByName("unpin-lib")
	if err != nil || current == nil {
		t.Fatalf("Failed to read the published build: %v", err)
	}

	projectDir := env.newProject("unpinner")
	env.chdir(projectDir)
	if err := cli.RunAdd("unpin-lib@"+current.ContentHash[:8], false, false, false); err != nil {
		t.Fatalf("Failed to add by hash: %v", err)
	}
	if !linkFor(t, env, projectDir, "unpin-lib").Pinned {
		t.Fatal("the add by hash did not pin, so there is nothing here to unpin")
	}

	if err := cli.RunAdd("unpin-lib", false, false, false); err != nil {
		t.Fatalf("Failed to re-add without a build identifier: %v", err)
	}

	if linkFor(t, env, projectDir, "unpin-lib").Pinned {
		t.Error("'lnpm add unpin-lib' left the link pinned while the pinned build was still the current record - the unpin reported success and changed nothing")
	}
}

// TestAddRecordsThePinInTheLockFile pins the transport half of ADR-0006. The
// database row is the authority - it is what pull, push and gc read - but the
// lock file is what `lnpm retreat` renames into its snapshot, so a pin that is
// not written there cannot survive a retreat.
func TestAddRecordsThePinInTheLockFile(t *testing.T) {
	env := setupTest(t)

	projectDir := env.newProject("lock-pinner")
	pinnedFixture(t, env, "lock-pin-lib", projectDir)

	env.AssertLockfile(projectDir, func(lock *lockfile.LockFile) {
		entry, ok := lock.Get("lock-pin-lib")
		if !ok {
			t.Fatal("lock-pin-lib is not in the lock file")
		}
		if !entry.Pinned {
			t.Error("the lock entry does not record the pin, so a retreat would carry a project back unpinned")
		}
	})
}

// TestAddSaysWhenItPins covers the reporting half. A pin changes what every
// later `lnpm pull` does to this package, so an add that produced one silently
// would leave the user to find out from a pull that skipped.
func TestAddSaysWhenItPins(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.publishPkg("say-pin-lib", "1.0.0", map[string]string{
		"index.js": "module.exports = 'v1';",
	})
	projectDir := env.newProject("say-pinner")
	env.addPkg(projectDir, "say-pin-lib", false, false)
	env.republish(pkgDir, "say-pin-lib", "2.0.0", "module.exports = 'v2';")

	env.chdir(projectDir)
	out := captureStdout(t, func() {
		if err := cli.RunAdd("say-pin-lib@1.0.0", false, false, false); err != nil {
			t.Errorf("RunAdd() error = %v", err)
		}
	})

	if !strings.Contains(out, "pinned") {
		t.Errorf("the add that pinned the link never said so, output was:\n%s", out)
	}
}

// TestPullLeavesAPinnedPackageAlone is the defect #300 reports, from the side
// bare `lnpm pull` reaches it: a sweep over the whole lock must refresh
// everything else and leave the pin where it is.
func TestPullLeavesAPinnedPackageAlone(t *testing.T) {
	env := setupTest(t)

	projectDir := env.newProject("sweeper")
	_, pinned := pinnedFixture(t, env, "swept-lib", projectDir)

	env.chdir(projectDir)
	if err := cli.RunPull(nil); err != nil {
		t.Fatalf("RunPull() error = %v", err)
	}

	entry := lockEntry(t, env, projectDir, "swept-lib")
	if entry.Hash != pinned.ContentHash {
		t.Errorf("the lock entry moved to %s, want the pinned %s", entry.Hash, pinned.ContentHash)
	}
	if !entry.Pinned {
		t.Error("the pull dropped the pin from the lock entry")
	}

	link := linkFor(t, env, projectDir, "swept-lib")
	if link.PackageID != pinned.ID {
		t.Errorf("the pull repointed the link to package %d, want the pinned %d", link.PackageID, pinned.ID)
	}
	if !link.Pinned {
		t.Error("the pull dropped the pin from the link row")
	}
}

// TestPullReportsThePackageItSkipped covers the reporting half of the second
// decision. Silence is what makes the current behaviour a defect rather than a
// preference: a user who pulled to update one package has to be told another was
// left alone and why.
func TestPullReportsThePackageItSkipped(t *testing.T) {
	env := setupTest(t)

	projectDir := env.newProject("reporter")
	pinnedFixture(t, env, "reported-lib", projectDir)

	env.chdir(projectDir)
	out := captureStdout(t, func() {
		if err := cli.RunPull(nil); err != nil {
			t.Errorf("RunPull() error = %v", err)
		}
	})

	if !strings.Contains(out, "reported-lib") {
		t.Errorf("the pull did not name the package it skipped, output was:\n%s", out)
	}
	if !strings.Contains(out, "pinned") {
		t.Errorf("the pull did not say a pin was why it skipped, output was:\n%s", out)
	}
}

// TestPullRefreshesOthersAroundAPin is the regression this issue reports, and
// the reason a pin is skipped rather than refused when the pull is a sweep:
// pulling to update one package must not undo another package's rollback, and
// must not be stopped by it either.
func TestPullRefreshesOthersAroundAPin(t *testing.T) {
	env := setupTest(t)

	projectDir := env.newProject("mixed")

	// Y is rolled back; X is an ordinary consumer with an update waiting.
	_, pinnedY := pinnedFixture(t, env, "rollback-y", projectDir)

	pkgX := env.publishPkg("update-x", "1.0.0", map[string]string{
		"index.js": "module.exports = 'x-v1';",
	})
	env.addPkg(projectDir, "update-x", false, false)
	env.republish(pkgX, "update-x", "2.0.0", "module.exports = 'x-v2';")

	env.chdir(projectDir)
	if err := cli.RunPull(nil); err != nil {
		t.Fatalf("RunPull() error = %v", err)
	}

	assertLockVersion(t, env, projectDir, "update-x", "2.0.0")
	if got := lockEntry(t, env, projectDir, "rollback-y").Hash; got != pinnedY.ContentHash {
		t.Errorf("pulling to update update-x moved rollback-y to %s, undoing its rollback", got)
	}
}

// TestPullNamingAPinnedPackageRefuses is the asymmetry ADR-0006 argues for.
// Naming a package is a request rather than a sweep, and the request cannot be
// honoured: a newer build is in the store and the link says not to take it. So
// the user who asked is told which of the two they are in, and how to get out.
func TestPullNamingAPinnedPackageRefuses(t *testing.T) {
	env := setupTest(t)

	projectDir := env.newProject("asker")
	_, pinned := pinnedFixture(t, env, "asked-lib", projectDir)

	env.chdir(projectDir)
	var err error
	out := captureStdout(t, func() {
		err = cli.RunPull([]string{"asked-lib"})
	})
	if err == nil {
		t.Fatal("RunPull() reported success for a package it cannot move")
	}
	message := err.Error() + "\n" + out
	if !strings.Contains(message, "pinned") {
		t.Errorf("the refusal does not say the package is pinned:\n%s", message)
	}
	if !strings.Contains(message, "lnpm add asked-lib") {
		t.Errorf("the refusal does not name the command that unpins:\n%s", message)
	}

	if got := lockEntry(t, env, projectDir, "asked-lib").Hash; got != pinned.ContentHash {
		t.Errorf("the refused pull moved the lock entry to %s anyway", got)
	}
}

// TestPullNamingAPinnedPackageLeavesTheRestAlone pins the refusal's timing. It
// happens with the rest of the named-package checks, before anything is linked,
// so a run that refuses cannot leave the project half-pulled - which is the
// property that block already exists for.
func TestPullNamingAPinnedPackageLeavesTheRestAlone(t *testing.T) {
	env := setupTest(t)

	projectDir := env.newProject("half-puller")
	pinnedFixture(t, env, "refused-lib", projectDir)

	pkgX := env.publishPkg("companion-lib", "1.0.0", map[string]string{
		"index.js": "module.exports = 'x-v1';",
	})
	env.addPkg(projectDir, "companion-lib", false, false)
	env.republish(pkgX, "companion-lib", "2.0.0", "module.exports = 'x-v2';")

	env.chdir(projectDir)
	if err := cli.RunPull([]string{"companion-lib", "refused-lib"}); err == nil {
		t.Fatal("RunPull() reported success for a run naming a pinned package")
	}

	assertLockVersion(t, env, projectDir, "companion-lib", "1.0.0")
}

// assertBuildRetained fails when the store no longer holds the given build,
// checking both halves of what "retained" means: the database row that makes it
// reachable, and the store entry the project's files come from.
func assertBuildRetained(t *testing.T, env *TestEnvironment, name string, build *db.Package) {
	t.Helper()

	pkg, err := env.Database.GetPackageByHash(name, build.ContentHash)
	if err != nil {
		t.Fatalf("Failed to look up %s@%s: %v", name, build.ContentHash, err)
	}
	if pkg == nil {
		t.Fatalf("gc collected the database record for %s@%s, the build a pinned link names", name, build.Version)
	}
	if _, err := os.Stat(build.StorePath); err != nil {
		t.Fatalf("gc removed the store entry at %s: %v", build.StorePath, err)
	}
}

// TestGCKeepsAPinnedBuildWithNoTimeBound covers the third decision directly
// rather than inferring it from reachability, which is what its acceptance
// criterion asks for.
//
// gc costs a pin no new rule - its arithmetic counts every link a package has
// and never consults a link's tag, so a pinned link is already a root - and that
// is exactly why it is worth a test of its own: nothing in gc.go names a pin, so
// nothing in gc.go would stop someone changing the rule that protects one.
//
// The zero threshold is the "no time bound" half. `--older-than 0d` bypasses the
// age filter entirely, so a freshly published orphan is collected under it; a
// pinned build has to survive it anyway.
func TestGCKeepsAPinnedBuildWithNoTimeBound(t *testing.T) {
	env := setupTest(t)

	projectDir := env.newProject("gc-pinner")
	_, pinned := pinnedFixture(t, env, "gc-pin-lib", projectDir)

	env.chdir(projectDir)
	if err := cli.RunGC(false, "0d", false, true); err != nil {
		t.Fatalf("RunGC() error = %v", err)
	}

	assertBuildRetained(t, env, "gc-pin-lib", pinned)
}

// TestGCCollectsTheBuildOnceThePinIsDropped is the counterweight: the pin is
// what kept the build, not something else about a superseded version. Without
// this the test above would keep passing if gc stopped collecting anything at
// all.
//
// Reclaiming takes two steps, as ADR-0002 already made it take two for a tagged
// build: drop the pin, then collect.
func TestGCCollectsTheBuildOnceThePinIsDropped(t *testing.T) {
	env := setupTest(t)

	projectDir := env.newProject("gc-unpinner")
	_, pinned := pinnedFixture(t, env, "gc-unpin-lib", projectDir)

	env.chdir(projectDir)
	if err := cli.RunAdd("gc-unpin-lib", false, false, false); err != nil {
		t.Fatalf("Failed to unpin: %v", err)
	}
	if err := cli.RunGC(false, "0d", false, true); err != nil {
		t.Fatalf("RunGC() error = %v", err)
	}

	pkg, err := env.Database.GetPackageByHash("gc-unpin-lib", pinned.ContentHash)
	if err != nil {
		t.Fatalf("Failed to look up the unpinned build: %v", err)
	}
	if pkg != nil {
		t.Error("gc kept the build after the pin was dropped, so the pin is not what was protecting it")
	}
}

// TestRollbackSurvivesAPullForAnotherPackageAndAGC is #300 itself, end to end
// and in the order the report describes: roll package Y back, pull to update
// package X, and confirm Y is still on its rolled-back build and that build is
// still in the store after a gc.
//
// The gc is the second half of the defect and the half that cannot be undone.
// Before this change the pull repointed Y's link off the historical record, so
// nothing reached that build and the next gc deleted it - the consumer back on
// the broken release, with nothing left to roll back to.
func TestRollbackSurvivesAPullForAnotherPackageAndAGC(t *testing.T) {
	env := setupTest(t)

	projectDir := env.newProject("regression")
	_, pinnedY := pinnedFixture(t, env, "regression-y", projectDir)

	pkgX := env.publishPkg("regression-x", "1.0.0", map[string]string{
		"index.js": "module.exports = 'x-v1';",
	})
	env.addPkg(projectDir, "regression-x", false, false)
	env.republish(pkgX, "regression-x", "2.0.0", "module.exports = 'x-v2';")

	env.chdir(projectDir)
	if err := cli.RunPull(nil); err != nil {
		t.Fatalf("RunPull() error = %v", err)
	}
	if err := cli.RunGC(false, "0d", false, true); err != nil {
		t.Fatalf("RunGC() error = %v", err)
	}

	assertLockVersion(t, env, projectDir, "regression-x", "2.0.0")
	if got := lockEntry(t, env, projectDir, "regression-y").Hash; got != pinnedY.ContentHash {
		t.Errorf("regression-y is on %s, want the rolled-back %s", got, pinnedY.ContentHash)
	}
	assertBuildRetained(t, env, "regression-y", pinnedY)
}

// TestPublishDoesNotCarryAPinnedLinkForward is ADR-0006's correction to #300's
// "what does hold". A pin on the build latest currently names is the ordinary
// case - `lnpm add mylib@1.0.0` while 1.0.0 is the current release - and the
// very next publish moves latest off that record. Carried forward, the pinned
// build loses its last root and the following gc collects it, which is #300's
// failure arriving before pull is ever run.
func TestPublishDoesNotCarryAPinnedLinkForward(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.publishPkg("carry-lib", "1.0.0", map[string]string{
		"index.js": "module.exports = 'v1';",
	})
	current, err := env.Database.GetPackageByName("carry-lib")
	if err != nil || current == nil {
		t.Fatalf("Failed to read the published build: %v", err)
	}

	projectDir := env.newProject("carry-consumer")
	env.chdir(projectDir)
	if err := cli.RunAdd("carry-lib@1.0.0", false, false, false); err != nil {
		t.Fatalf("Failed to pin the current build: %v", err)
	}

	env.republish(pkgDir, "carry-lib", "2.0.0", "module.exports = 'v2';")

	link := linkFor(t, env, projectDir, "carry-lib")
	if link.PackageID != current.ID {
		t.Fatalf("the publish carried the pinned link onto package %d, want it left on %d", link.PackageID, current.ID)
	}

	env.chdir(projectDir)
	if err := cli.RunGC(false, "0d", false, true); err != nil {
		t.Fatalf("RunGC() error = %v", err)
	}
	assertBuildRetained(t, env, "carry-lib", current)
}

// TestPublishWithPushLeavesAPinnedConsumerAlone is the other half of "what does
// hold": push and `publish --push` enumerate consumers of the record just
// written, and a pinned project is not on it. The files in the project must
// still be the ones it pinned.
func TestPublishWithPushLeavesAPinnedConsumerAlone(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.publishPkg("push-lib", "1.0.0", map[string]string{
		"index.js": "module.exports = 'v1';",
	})
	projectDir := env.newProject("push-consumer")
	env.chdir(projectDir)
	if err := cli.RunAdd("push-lib@1.0.0", false, false, false); err != nil {
		t.Fatalf("Failed to pin the current build: %v", err)
	}

	env.chdir(pkgDir)
	env.writeFile(filepath.Join(pkgDir, "package.json"), `{"name":"push-lib","version":"2.0.0"}`)
	env.writeFile(filepath.Join(pkgDir, "index.js"), "module.exports = 'v2';")
	if err := cli.RunPublish(true, false, false, false); err != nil {
		t.Fatalf("Failed to publish with --push: %v", err)
	}

	linked := readBytes(t, filepath.Join(projectDir, ".lnpm", "push-lib", "index.js"))
	if !strings.Contains(string(linked), "v1") {
		t.Errorf("the publish --push overwrote a pinned consumer's files, which now read:\n%s", linked)
	}
}

// TestRetreatAndRestoreReinstateThePin covers the fourth decision. Restore
// rebuilds which build a project was on exactly, through the content hash the
// snapshot recorded; without the pin travelling with it the project comes back
// following latest, and the next pull moves it off the build it was just
// restored onto. That is #300's own defect in another place - a deliberate state
// silently undone.
func TestRetreatAndRestoreReinstateThePin(t *testing.T) {
	env := setupTest(t)

	projectDir := env.newProject("retreater")
	_, pinned := pinnedFixture(t, env, "retreat-lib", projectDir)

	env.chdir(projectDir)
	if err := cli.RunRetreat(true, false); err != nil {
		t.Fatalf("RunRetreat() error = %v", err)
	}

	// The snapshot is a lock file, so the pin has to be in it: it is the only
	// thing restore has to rebuild the link row from.
	snapshot, err := lockfile.LoadRetreat(projectDir)
	if err != nil || snapshot == nil {
		t.Fatalf("Failed to read the retreat snapshot: %v", err)
	}
	if entry, ok := snapshot.Get("retreat-lib"); !ok || !entry.Pinned {
		t.Fatalf("the snapshot does not record the pin: %+v", entry)
	}

	if err := cli.RunRestore(); err != nil {
		t.Fatalf("RunRestore() error = %v", err)
	}

	link := linkFor(t, env, projectDir, "retreat-lib")
	if !link.Pinned {
		t.Error("the restored link is not pinned, so the next pull would move it off the build restore just put it on")
	}
	if link.PackageID != pinned.ID {
		t.Errorf("the restored link names package %d, want the pinned %d", link.PackageID, pinned.ID)
	}
	if !lockEntry(t, env, projectDir, "retreat-lib").Pinned {
		t.Error("the restored lock entry does not record the pin")
	}
}

// TestRestoreDoesNotSendARestoredPinToPull pins the advice. A restore onto a
// build latest no longer names normally ends by telling the user to run `lnpm
// pull`, because the recorded build is simply behind. For a restored pin that is
// exactly backwards, and since the second decision it names a command that
// refuses.
func TestRestoreDoesNotSendARestoredPinToPull(t *testing.T) {
	env := setupTest(t)

	projectDir := env.newProject("advised")
	pinnedFixture(t, env, "advised-lib", projectDir)

	env.chdir(projectDir)
	if err := cli.RunRetreat(true, false); err != nil {
		t.Fatalf("RunRetreat() error = %v", err)
	}
	out := captureStdout(t, func() {
		if err := cli.RunRestore(); err != nil {
			t.Errorf("RunRestore() error = %v", err)
		}
	})

	if strings.Contains(out, "Run 'lnpm pull'") {
		t.Errorf("restore told the user to run a pull that will refuse the pinned package, output was:\n%s", out)
	}
	if !strings.Contains(out, "pinned") {
		t.Errorf("restore did not say the package came back pinned, output was:\n%s", out)
	}
}

// TestStatusShowsThatAPackageIsPinned covers the last surface a pin touches. The
// state was unnameable anywhere before this: `lnpm list <pkg> --versions` shows
// a project sitting on a superseded build, but that is the symptom, and nothing
// distinguished a project that chose to be there from one that had simply not
// pulled.
func TestStatusShowsThatAPackageIsPinned(t *testing.T) {
	env := setupTest(t)

	projectDir := env.newProject("status-pinner")
	pinnedFixture(t, env, "status-pin-lib", projectDir)

	env.chdir(projectDir)
	out := captureStdout(t, func() {
		if err := cli.RunStatus(); err != nil {
			t.Errorf("RunStatus() error = %v", err)
		}
	})

	line := ""
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "status-pin-lib@") {
			line = l
		}
	}
	if line == "" {
		t.Fatalf("status did not list the linked package at all, output was:\n%s", out)
	}
	if !strings.Contains(line, "pinned") {
		t.Errorf("status does not say the package is pinned:\n%s", line)
	}
}
