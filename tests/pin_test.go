// A pinned link, end to end: `lnpm add mylib@<hash>` and `lnpm add mylib@<ver>`
// link a project to one build and to no channel, and nothing but the user moves
// it off. ADR-0006 records the decisions these tests pin.
//
// The commands are driven the way a user drives them, through the real store,
// because the whole defect #300 reports is one command quietly undoing another
// command's work - which only shows up when both are actually run.
package tests

import (
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
