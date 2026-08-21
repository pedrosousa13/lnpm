package tests

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/cli"
	"github.com/pedrosousa13/lnpm/internal/db"
	"github.com/pedrosousa13/lnpm/pkg/lockfile"
)

// publishTagged rewrites an already published package's source with a new
// version and index.js, then publishes it under tag. It is republish with a tag,
// and exists here rather than in helpers_test.go because only the dist-tag tests
// need a publish that does not move latest.
func publishTagged(t *testing.T, env *TestEnvironment, pkgDir, name, version, indexJS, tag string) {
	t.Helper()

	env.chdir(pkgDir)
	env.writeFile(filepath.Join(pkgDir, "package.json"), `{"name":"`+name+`","version":"`+version+`"}`)
	env.writeFile(filepath.Join(pkgDir, "index.js"), indexJS)
	if err := cli.RunPublishTagged(false, false, false, false, tag); err != nil {
		t.Fatalf("Failed to publish %s@%s under tag %s: %v", name, version, tag, err)
	}
}

// assertTagged fails unless the named tag of pkg points at wantHash.
func assertTagged(t *testing.T, env *TestEnvironment, name, tag, wantHash string) {
	t.Helper()

	tags, err := env.Database.TagsForPackage(name)
	if err != nil {
		t.Fatalf("Failed to read the tags of %s: %v", name, err)
	}
	if got := tags[tag]; got != wantHash {
		t.Errorf("Tag %s of %s points at %q, want %q", tag, name, got, wantHash)
	}
}

// TestPublishWithTagDoesNotMoveLatest covers the first acceptance criterion:
// publishing under a tag stores the build and leaves latest where it was, so
// consumers who never asked for the channel keep the release they had.
func TestPublishWithTagDoesNotMoveLatest(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.publishPkg("tagged-lib", "1.0.0", map[string]string{
		"index.js": "module.exports = 'stable';",
	})
	stable, err := env.Database.GetPackageByName("tagged-lib")
	if err != nil || stable == nil {
		t.Fatalf("Failed to read the published package: %v", err)
	}

	publishTagged(t, env, pkgDir, "tagged-lib", "2.0.0-beta.1", "module.exports = 'beta';", "beta")

	assertTagged(t, env, "tagged-lib", db.DefaultTag, stable.ContentHash)

	beta, err := env.Database.ResolveTag("tagged-lib", "beta")
	if err != nil {
		t.Fatalf("Failed to resolve the beta tag: %v", err)
	}
	if beta == nil {
		t.Fatal("The beta tag names no version, want the build just published")
	}
	if beta.Version != "2.0.0-beta.1" {
		t.Errorf("The beta tag names version %s, want 2.0.0-beta.1", beta.Version)
	}

	// The name index mirrors latest, so the stable release must still be what a
	// lookup by name answers with.
	byName, err := env.Database.GetPackageByName("tagged-lib")
	if err != nil || byName == nil {
		t.Fatalf("Failed to look up tagged-lib by name: %v", err)
	}
	if byName.Version != "1.0.0" {
		t.Errorf("Looking tagged-lib up by name gives %s, want the untouched 1.0.0", byName.Version)
	}
}

// TestPublishWithTagMovesTheTagOnUnchangedContent pins the case a happy-path
// test cannot reach: publish returns early when the content it packed is already
// in the store, and that early return has to be tag-aware. Without it a
// `publish --tag beta` of an unchanged working tree reports success and sets no
// tag at all.
func TestPublishWithTagMovesTheTagOnUnchangedContent(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.publishPkg("unchanged-lib", "1.0.0", map[string]string{
		"index.js": "module.exports = 'same';",
	})
	published, err := env.Database.GetPackageByName("unchanged-lib")
	if err != nil || published == nil {
		t.Fatalf("Failed to read the published package: %v", err)
	}

	env.chdir(pkgDir)
	if err := cli.RunPublishTagged(false, false, false, false, "beta"); err != nil {
		t.Fatalf("Failed to publish unchanged content under a tag: %v", err)
	}

	assertTagged(t, env, "unchanged-lib", "beta", published.ContentHash)
	assertTagged(t, env, "unchanged-lib", db.DefaultTag, published.ContentHash)
}

// TestPublishWithoutTagStillSkipsUnchangedContent is the other half of the
// condition above: making the early return tag-aware must not stop it firing for
// an ordinary republish of unchanged content, which is what tells a user their
// working tree holds nothing new.
func TestPublishWithoutTagStillSkipsUnchangedContent(t *testing.T) {
	env := setupTest(t)

	env.publishPkg("noop-lib", "1.0.0", map[string]string{
		"index.js": "module.exports = 'same';",
	})

	out := captureStdout(t, func() {
		if err := cli.RunPublish(false, false, false, false); err != nil {
			t.Errorf("Failed to re-publish unchanged content: %v", err)
		}
	})

	if !strings.Contains(out, "already published with same content") {
		t.Errorf("Re-publishing unchanged content did not report it, output was:\n%s", out)
	}
}

// TestPublishFirstVersionUnderATagIsReachableByName pins that a package whose
// first publish names some other channel is still addressable by name. Every
// command but a tag-aware add - push, remove, restore, status - resolves through
// the name index, so a beta-only package that never got the default tag would
// sit in the store invisible to all of them.
func TestPublishFirstVersionUnderATagIsReachableByName(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.CreateTestPackage("beta-only-lib", "0.1.0-beta.1", map[string]string{
		"index.js": "module.exports = 'beta';",
	})
	env.chdir(pkgDir)
	if err := cli.RunPublishTagged(false, false, false, false, "beta"); err != nil {
		t.Fatalf("Failed to publish under a tag: %v", err)
	}

	pkg, err := env.Database.GetPackageByName("beta-only-lib")
	if err != nil {
		t.Fatalf("Failed to look up beta-only-lib by name: %v", err)
	}
	if pkg == nil {
		t.Fatal("A package published only under beta is not reachable by name")
	}

	assertTagged(t, env, "beta-only-lib", "beta", pkg.ContentHash)
	assertTagged(t, env, "beta-only-lib", db.DefaultTag, pkg.ContentHash)
}

// TestAddByTagLinksTheTaggedBuild covers the second acceptance criterion:
// `lnpm add mylib@beta` materialises the build the beta tag names, not the one
// latest names.
func TestAddByTagLinksTheTaggedBuild(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.publishPkg("channel-lib", "1.0.0", map[string]string{
		"index.js": "module.exports = 'stable';",
	})
	publishTagged(t, env, pkgDir, "channel-lib", "2.0.0-beta.1", "module.exports = 'beta';", "beta")

	projectDir := env.newProject("channel-project")
	env.addPkg(projectDir, "channel-lib@beta", false, false)

	env.AssertLinkedFileContent(projectDir, "channel-lib", "index.js", "module.exports = 'beta';")
	env.AssertLockfile(projectDir, func(lock *lockfile.LockFile) {
		entry, ok := lock.Get("channel-lib")
		if !ok {
			t.Fatal("channel-lib is missing from the lock file")
		}
		if entry.Version != "2.0.0-beta.1" {
			t.Errorf("The lock file records version %s, want the tagged 2.0.0-beta.1", entry.Version)
		}
	})
}

// TestAddByTagRecordsTheTagOnTheLink pins that a project which asked for a
// channel is recorded as following it. Without the tag on the link row, the next
// time latest moves the link is carried onto it and the beta consumer is
// silently switched to the stable release it deliberately did not ask for.
func TestAddByTagRecordsTheTagOnTheLink(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.publishPkg("pinned-lib", "1.0.0", map[string]string{
		"index.js": "module.exports = 'stable';",
	})
	publishTagged(t, env, pkgDir, "pinned-lib", "2.0.0-beta.1", "module.exports = 'beta';", "beta")

	projectDir := env.newProject("pinned-project")
	env.addPkg(projectDir, "pinned-lib@beta", false, false)

	beta, err := env.Database.ResolveTag("pinned-lib", "beta")
	if err != nil || beta == nil {
		t.Fatalf("Failed to resolve the beta tag: %v", err)
	}
	links, err := env.Database.GetLinksForPackage(beta.ID)
	if err != nil {
		t.Fatalf("Failed to read the links of the beta build: %v", err)
	}
	if len(links) != 1 {
		t.Fatalf("The beta build has %d link(s), want exactly 1", len(links))
	}
	if links[0].Tag != "beta" {
		t.Errorf("The link follows tag %q, want beta", links[0].Tag)
	}

	// Moving latest must leave the beta consumer where it is.
	env.republish(pkgDir, "pinned-lib", "3.0.0", "module.exports = 'newer stable';")

	stillBeta, err := env.Database.ResolveTag("pinned-lib", "beta")
	if err != nil || stillBeta == nil {
		t.Fatalf("Failed to re-resolve the beta tag: %v", err)
	}
	links, err = env.Database.GetLinksForPackage(stillBeta.ID)
	if err != nil {
		t.Fatalf("Failed to re-read the links of the beta build: %v", err)
	}
	if len(links) != 1 {
		t.Errorf("Publishing to latest left the beta build with %d link(s), want 1", len(links))
	}
}

// TestAddWithoutATagStillResolvesLatest covers the third acceptance criterion:
// the default resolution is untouched, so a plain add gets the build latest
// names even when another channel holds something newer.
func TestAddWithoutATagStillResolvesLatest(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.publishPkg("default-lib", "1.0.0", map[string]string{
		"index.js": "module.exports = 'stable';",
	})
	publishTagged(t, env, pkgDir, "default-lib", "2.0.0-beta.1", "module.exports = 'beta';", "beta")

	projectDir := env.newProject("default-project")
	env.addPkg(projectDir, "default-lib", false, false)

	env.AssertLinkedFileContent(projectDir, "default-lib", "index.js", "module.exports = 'stable';")
	env.AssertLockfile(projectDir, func(lock *lockfile.LockFile) {
		entry, _ := lock.Get("default-lib")
		if entry.Version != "1.0.0" {
			t.Errorf("A plain add recorded version %s, want the latest-tagged 1.0.0", entry.Version)
		}
	})
}

// TestAddByAnUnsetTagStillReportsTheVersion pins that resolving a tag first does
// not swallow the message an exact-version add gives. A spec that names no tag
// falls through to the version check, which is what still tells a user their
// version is not the one in the store.
func TestAddByAnUnsetTagStillReportsTheVersion(t *testing.T) {
	env := setupTest(t)

	env.simplePkg("exact-lib")
	projectDir := env.newProject("exact-project")
	env.chdir(projectDir)

	err := cli.RunAdd("exact-lib@9.9.9", false, false, false)
	if err == nil {
		t.Fatal("Adding a version that is neither a tag nor the stored version succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "9.9.9") || !strings.Contains(err.Error(), "1.0.0") {
		t.Errorf("The error is %v, want it to name both the requested and the stored version", err)
	}
}

// TestAddMultipleResolvesTagsToo pins that the parallel add path resolves a tag
// the same way the single-package one does. The two duplicate their resolution,
// and a fix applied to only one of them is exactly the kind of drift that makes
// `lnpm add a@beta b` behave differently from `lnpm add a@beta`.
func TestAddMultipleResolvesTagsToo(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.publishPkg("multi-tagged-lib", "1.0.0", map[string]string{
		"index.js": "module.exports = 'stable';",
	})
	publishTagged(t, env, pkgDir, "multi-tagged-lib", "2.0.0-beta.1", "module.exports = 'beta';", "beta")
	env.simplePkg("multi-plain-lib")

	projectDir := env.newProject("multi-project")
	env.chdir(projectDir)
	if err := cli.RunAddMultiple([]string{"multi-tagged-lib@beta", "multi-plain-lib"}, false, false, false, false); err != nil {
		t.Fatalf("Failed to add a tagged and a plain package together: %v", err)
	}

	env.AssertLinkedFileContent(projectDir, "multi-tagged-lib", "index.js", "module.exports = 'beta';")

	beta, err := env.Database.ResolveTag("multi-tagged-lib", "beta")
	if err != nil || beta == nil {
		t.Fatalf("Failed to resolve the beta tag: %v", err)
	}
	links, err := env.Database.GetLinksForPackage(beta.ID)
	if err != nil {
		t.Fatalf("Failed to read the links of the beta build: %v", err)
	}
	if len(links) != 1 || links[0].Tag != "beta" {
		t.Errorf("The parallel add recorded links %+v, want exactly one following beta", links)
	}
}

// TestTagPointsATagAtTheStoredVersion covers the fourth acceptance criterion's
// first half: `lnpm tag mylib beta` names the version already in the store,
// without a republish. Nothing is packed, hashed or copied - the store entry the
// package resolves to is the one the tag ends up on.
func TestTagPointsATagAtTheStoredVersion(t *testing.T) {
	env := setupTest(t)

	env.simplePkg("taggable-lib")
	stored, err := env.Database.GetPackageByName("taggable-lib")
	if err != nil || stored == nil {
		t.Fatalf("Failed to read the published package: %v", err)
	}

	out := captureStdout(t, func() {
		if err := cli.RunTag("taggable-lib", "beta", false); err != nil {
			t.Errorf("RunTag() error = %v", err)
		}
	})

	assertTagged(t, env, "taggable-lib", "beta", stored.ContentHash)
	if !strings.Contains(out, "beta") || !strings.Contains(out, "taggable-lib") {
		t.Errorf("RunTag did not report what it tagged, output was:\n%s", out)
	}
	assertNoRawGlyphs(t, out)

	// A tag is only worth having if an add resolves through it.
	projectDir := env.newProject("taggable-project")
	env.addPkg(projectDir, "taggable-lib@beta", false, false)
	env.AssertFilesLinked(projectDir, "taggable-lib")
}

// TestTagDeleteRemovesTheTag covers the criterion's second half: `--delete`
// takes the tag off without touching the version it named, which stays in the
// store and stays reachable by name.
func TestTagDeleteRemovesTheTag(t *testing.T) {
	env := setupTest(t)

	env.simplePkg("untaggable-lib")
	if err := cli.RunTag("untaggable-lib", "beta", false); err != nil {
		t.Fatalf("Failed to set the tag: %v", err)
	}

	out := captureStdout(t, func() {
		if err := cli.RunTag("untaggable-lib", "beta", true); err != nil {
			t.Errorf("RunTag(delete) error = %v", err)
		}
	})

	tags, err := env.Database.TagsForPackage("untaggable-lib")
	if err != nil {
		t.Fatalf("Failed to read the tags: %v", err)
	}
	if _, ok := tags["beta"]; ok {
		t.Errorf("The beta tag survived --delete: %v", tags)
	}
	if _, ok := tags[db.DefaultTag]; !ok {
		t.Errorf("Deleting beta also removed %s: %v", db.DefaultTag, tags)
	}
	env.AssertPackageInDatabase("untaggable-lib", true)
	assertNoRawGlyphs(t, out)
}

// TestTagDeleteRefusesTheDefaultTag pins that the database's refusal reaches the
// user rather than being worked around. The name index mirrors the default tag,
// so removing it would leave the package published, its files on disk, and every
// command that resolves by name unable to see it.
func TestTagDeleteRefusesTheDefaultTag(t *testing.T) {
	env := setupTest(t)

	env.simplePkg("keep-latest-lib")

	err := cli.RunTag("keep-latest-lib", db.DefaultTag, true)
	if err == nil {
		t.Fatal("Deleting the default tag succeeded, want a refusal")
	}
	if !strings.Contains(err.Error(), db.DefaultTag) {
		t.Errorf("The refusal is %v, want it to name the %s tag", err, db.DefaultTag)
	}
	env.AssertPackageInDatabase("keep-latest-lib", true)
}

// TestTagOnAnUnknownPackageFails pins that tagging something the store does not
// hold is an error rather than a tag pointing at nothing. A dangling tag
// resolves to nothing and, since tags are gc reachability roots, would protect
// nothing either.
func TestTagOnAnUnknownPackageFails(t *testing.T) {
	env := setupTest(t)
	_ = env

	err := cli.RunTag("no-such-lib", "beta", false)
	if err == nil {
		t.Fatal("Tagging a package that is not in the store succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "no-such-lib") {
		t.Errorf("The error is %v, want it to name the package", err)
	}
}

// TestTagCarriesConsumersOfThatChannelOnly pins what moving a tag on an existing
// package does to the projects following it: the beta consumer is carried onto
// the version beta now names, and the latest consumer is left where it is.
func TestTagCarriesConsumersOfThatChannelOnly(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.publishPkg("carry-lib", "1.0.0", map[string]string{
		"index.js": "module.exports = 'one';",
	})
	publishTagged(t, env, pkgDir, "carry-lib", "2.0.0-beta.1", "module.exports = 'two';", "beta")

	betaProject := env.newProject("beta-consumer")
	env.addPkg(betaProject, "carry-lib@beta", false, false)
	stableProject := env.newProject("stable-consumer")
	env.addPkg(stableProject, "carry-lib", false, false)

	// Point beta back at the version latest names.
	if err := cli.RunTag("carry-lib", "beta", false); err != nil {
		t.Fatalf("Failed to move the beta tag: %v", err)
	}

	stable, err := env.Database.GetPackageByName("carry-lib")
	if err != nil || stable == nil {
		t.Fatalf("Failed to read carry-lib: %v", err)
	}
	links, err := env.Database.GetLinksForPackage(stable.ID)
	if err != nil {
		t.Fatalf("Failed to read the links of the stable build: %v", err)
	}
	if len(links) != 2 {
		t.Fatalf("The stable build has %d link(s), want both consumers after beta was moved onto it", len(links))
	}

	tagsSeen := map[string]int{}
	for _, l := range links {
		tagsSeen[l.Tag]++
	}
	if tagsSeen["beta"] != 1 || tagsSeen[""] != 1 {
		t.Errorf("The links follow %v, want one on beta and one on the default tag", tagsSeen)
	}
}
