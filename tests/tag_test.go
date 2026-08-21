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
	setupTest(t)

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

// TestListStoreShowsWhichTagsNameEachVersion covers the fifth acceptance
// criterion. With more than one version of a name live at once, the store
// listing is the only place a user can see which of them a channel points at.
func TestListStoreShowsWhichTagsNameEachVersion(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.publishPkg("listed-lib", "1.0.0", map[string]string{
		"index.js": "module.exports = 'stable';",
	})
	publishTagged(t, env, pkgDir, "listed-lib", "2.0.0-beta.1", "module.exports = 'beta';", "beta")

	out := captureStdout(t, func() {
		if err := cli.RunList(true, "", false); err != nil {
			t.Errorf("RunList(--store) error = %v", err)
		}
	})

	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.Contains(line, "listed-lib@1.0.0"):
			if !strings.Contains(line, db.DefaultTag) {
				t.Errorf("The 1.0.0 line does not name the %s tag: %q", db.DefaultTag, line)
			}
			if strings.Contains(line, "beta") {
				t.Errorf("The 1.0.0 line claims the beta tag: %q", line)
			}
		case strings.Contains(line, "listed-lib@2.0.0-beta.1"):
			if !strings.Contains(line, "beta") {
				t.Errorf("The 2.0.0-beta.1 line does not name the beta tag: %q", line)
			}
			if strings.Contains(line, db.DefaultTag) {
				t.Errorf("The 2.0.0-beta.1 line claims the %s tag: %q", db.DefaultTag, line)
			}
		}
	}

	if !strings.Contains(out, "listed-lib@1.0.0") || !strings.Contains(out, "listed-lib@2.0.0-beta.1") {
		t.Errorf("The store listing is missing one of the two live versions, output was:\n%s", out)
	}
}

// TestListStoreLeavesAnUntaggedVersionUnmarked pins that the tag column says
// nothing when there is nothing to say. A superseded version no tag names is
// exactly what gc will collect, and marking it would be a claim about a channel
// that does not exist.
func TestListStoreLeavesAnUntaggedVersionUnmarked(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.publishPkg("superseded-lib", "1.0.0", map[string]string{
		"index.js": "module.exports = 'old';",
	})
	env.republish(pkgDir, "superseded-lib", "2.0.0", "module.exports = 'new';")

	out := captureStdout(t, func() {
		if err := cli.RunList(true, "", false); err != nil {
			t.Errorf("RunList(--store) error = %v", err)
		}
	})

	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "superseded-lib@1.0.0") && strings.Contains(line, db.DefaultTag) {
			t.Errorf("The superseded version is shown as tagged: %q", line)
		}
	}
	assertNoRawGlyphs(t, out)
}

// TestDoctorDoesNotCallATagPinnedBuildOrphaned pins a report that only became
// reachable once `publish --tag` existed. doctor counts a package with no links
// as an orphan and advises running gc - but gc keeps anything a non-default tag
// names, so on a tagged build the advice describes a run that would find
// nothing. Warning about a build lnpm is deliberately keeping is worse than
// saying nothing: it sends a user to a command that cannot act on it.
func TestDoctorDoesNotCallATagPinnedBuildOrphaned(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.publishPkg("doctor-tagged-lib", "1.0.0", map[string]string{
		"index.js": "module.exports = 'stable';",
	})
	publishTagged(t, env, pkgDir, "doctor-tagged-lib", "2.0.0-beta.1", "module.exports = 'beta';", "beta")

	// Link the stable build so the only unlinked version left is the tagged one.
	projectDir := env.newProject("doctor-tagged-project")
	env.addPkg(projectDir, "doctor-tagged-lib", false, false)

	out := captureStdout(t, func() {
		if err := cli.RunDoctor(); err != nil {
			t.Errorf("RunDoctor() error = %v", err)
		}
	})

	if strings.Contains(out, "orphaned package(s)") {
		t.Errorf("doctor called the tag-pinned build an orphan, output was:\n%s", out)
	}
	if !strings.Contains(out, "All checks passed!") {
		t.Errorf("doctor did not pass on a store whose only unlinked build is tagged, output was:\n%s", out)
	}
}

// TestDoctorStillReportsAnUntaggedOrphan is the other half: dropping tag-pinned
// builds from the count must not drop the ones gc really would collect.
func TestDoctorStillReportsAnUntaggedOrphan(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.publishPkg("doctor-super-lib", "1.0.0", map[string]string{
		"index.js": "module.exports = 'old';",
	})
	env.republish(pkgDir, "doctor-super-lib", "2.0.0", "module.exports = 'new';")

	out := captureStdout(t, func() {
		if err := cli.RunDoctor(); err != nil {
			t.Errorf("RunDoctor() error = %v", err)
		}
	})

	if !strings.Contains(out, "orphaned package(s)") {
		t.Errorf("doctor did not report the untagged, unlinked versions, output was:\n%s", out)
	}
}

// TestPullKeepsAProjectOnItsChannel pins that refreshing does not switch
// channels. pull resolves by name, and the name index mirrors latest, so a beta
// consumer running `lnpm pull` would have its files replaced with the stable
// release and its lock rewritten to match - the very carry-over onto latest that
// asking for a channel exists to prevent, reached through a different command.
//
// Unreachable before add learned tags: no link could follow anything but the
// default one.
func TestPullKeepsAProjectOnItsChannel(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.publishPkg("pull-lib", "1.0.0", map[string]string{
		"index.js": "module.exports = 'stable';",
	})
	publishTagged(t, env, pkgDir, "pull-lib", "2.0.0-beta.1", "module.exports = 'beta';", "beta")

	projectDir := env.newProject("pull-project")
	env.addPkg(projectDir, "pull-lib@beta", false, false)

	// Move latest well past the beta build.
	env.republish(pkgDir, "pull-lib", "3.0.0", "module.exports = 'newer stable';")

	env.chdir(projectDir)
	if err := cli.RunPull(nil); err != nil {
		t.Fatalf("Failed to pull: %v", err)
	}

	env.AssertLinkedFileContent(projectDir, "pull-lib", "index.js", "module.exports = 'beta';")
	env.AssertLockfile(projectDir, func(lock *lockfile.LockFile) {
		entry, _ := lock.Get("pull-lib")
		if entry.Version != "2.0.0-beta.1" {
			t.Errorf("pull rewrote the lock to %s, want the beta build 2.0.0-beta.1", entry.Version)
		}
	})
}

// TestPullFollowsItsChannelForward is the other half: staying on a channel must
// still mean picking up what that channel now names, or pull would have stopped
// doing its job for tagged consumers.
func TestPullFollowsItsChannelForward(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.publishPkg("pull-forward-lib", "1.0.0", map[string]string{
		"index.js": "module.exports = 'stable';",
	})
	publishTagged(t, env, pkgDir, "pull-forward-lib", "2.0.0-beta.1", "module.exports = 'beta one';", "beta")

	projectDir := env.newProject("pull-forward-project")
	env.addPkg(projectDir, "pull-forward-lib@beta", false, false)

	publishTagged(t, env, pkgDir, "pull-forward-lib", "2.0.0-beta.2", "module.exports = 'beta two';", "beta")

	env.chdir(projectDir)
	if err := cli.RunPull(nil); err != nil {
		t.Fatalf("Failed to pull: %v", err)
	}

	env.AssertLinkedFileContent(projectDir, "pull-forward-lib", "index.js", "module.exports = 'beta two';")
	env.AssertLockfile(projectDir, func(lock *lockfile.LockFile) {
		entry, _ := lock.Get("pull-forward-lib")
		if entry.Version != "2.0.0-beta.2" {
			t.Errorf("pull recorded %s, want the newer beta build 2.0.0-beta.2", entry.Version)
		}
	})
}

// linkRowsFor returns every link row the project at projectDir holds for a
// package name, whichever version each of them names.
//
// Deliberately not linksOfProject: that keys by name, so a project holding two
// rows for one package collapses to one and the very defect these tests pin
// becomes invisible.
func linkRowsFor(t *testing.T, env *TestEnvironment, projectDir, name string) []*db.Link {
	t.Helper()

	proj, err := env.Database.GetProjectByPath(projectDir)
	if err != nil || proj == nil {
		t.Fatalf("Project %s is not in the database: %v", projectDir, err)
	}
	links, err := env.Database.GetLinksForProject(proj.ID)
	if err != nil {
		t.Fatalf("Failed to read the links of %s: %v", projectDir, err)
	}
	packages, err := env.Database.ListPackages()
	if err != nil {
		t.Fatalf("Failed to list packages: %v", err)
	}
	nameByID := make(map[int64]string, len(packages))
	for _, pkg := range packages {
		nameByID[pkg.ID] = pkg.Name
	}

	var held []*db.Link
	for _, l := range links {
		if nameByID[l.PackageID] == name {
			held = append(held, l)
		}
	}
	return held
}

// assertOnlyLink fails unless the project holds exactly one link row for name,
// on the given version and following the given channel, and returns it.
func assertOnlyLink(t *testing.T, env *TestEnvironment, projectDir, name string, wantPkg *db.Package, wantTag string) {
	t.Helper()

	held := linkRowsFor(t, env, projectDir, name)
	if len(held) != 1 {
		t.Fatalf("The project holds %d link row(s) for %s, want exactly 1: %+v", len(held), name, held)
	}
	if held[0].PackageID != wantPkg.ID {
		t.Errorf("The link names record %d (%s), want %d (%s)",
			held[0].PackageID, name, wantPkg.ID, wantPkg.Version)
	}
	if held[0].Tag != wantTag {
		t.Errorf("The link follows tag %q, want %q", held[0].Tag, wantTag)
	}
}

// republishPushed republishes a package and pushes it to every project linked
// to the version the default tag lands on, which is what a maintainer's
// ordinary `lnpm publish --push` does.
func republishPushed(t *testing.T, env *TestEnvironment, pkgDir, name, version, indexJS string) {
	t.Helper()

	env.chdir(pkgDir)
	env.writeFile(filepath.Join(pkgDir, "package.json"), `{"name":"`+name+`","version":"`+version+`"}`)
	env.writeFile(filepath.Join(pkgDir, "index.js"), indexJS)
	captureStdout(t, func() {
		if err := cli.RunPublish(true, false, false, false); err != nil {
			t.Fatalf("Failed to publish %s@%s with --push: %v", name, version, err)
		}
	})
}

// TestSwitchingAProjectOntoAChannel covers the flow the whole feature exists
// for: a project already consuming a package opts into another channel of it.
//
// Nothing reconciles the link row the project already holds, so both shapes of
// the switch used to corrupt it. When the two tags name different versions the
// add wrote a second row, leaving the project recorded as consuming one package
// twice - which moveLinksTx, linksOfProject and remove all treat as impossible -
// and the stale row went on carrying the project onto every later release. When
// the two tags name the same version, which is what `lnpm tag` produces, the add
// found the existing row and updated it without recording the tag, so the
// project stayed a latest follower and the next publish dragged it forward.
//
// The subtests differ only in whether the channels start out on one version or
// two, which is exactly the distinction that decided which corruption happened.
func TestSwitchingAProjectOntoAChannel(t *testing.T) {
	cases := []struct {
		name string
		// separate publishes a distinct build under beta; otherwise beta is
		// pointed at the version latest already names, as `lnpm tag` does.
		separate bool
		wantBeta string
	}{
		{name: "the channels name different versions", separate: true, wantBeta: "module.exports = 'beta';"},
		{name: "the channels name one version", wantBeta: "module.exports = 'stable';"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := setupTest(t)

			pkgDir := env.publishPkg("switch-lib", "1.0.0", map[string]string{
				"index.js": "module.exports = 'stable';",
			})
			if tc.separate {
				publishTagged(t, env, pkgDir, "switch-lib", "2.0.0-beta.1", "module.exports = 'beta';", "beta")
			} else if err := cli.RunTag("switch-lib", "beta", false); err != nil {
				t.Fatalf("Failed to tag the stored version as beta: %v", err)
			}

			projectDir := env.newProject("switch-project")
			env.addPkg(projectDir, "switch-lib", false, false)
			env.addPkg(projectDir, "switch-lib@beta", false, false)

			beta, err := env.Database.ResolveTag("switch-lib", "beta")
			if err != nil || beta == nil {
				t.Fatalf("Failed to resolve the beta tag: %v", err)
			}
			assertOnlyLink(t, env, projectDir, "switch-lib", beta, "beta")
			env.AssertLinkedFileContent(projectDir, "switch-lib", "index.js", tc.wantBeta)

			// A later release of the stable channel must reach neither the link
			// row nor the files, whether or not it pushes.
			env.republish(pkgDir, "switch-lib", "3.0.0", "module.exports = 'newer stable';")
			assertOnlyLink(t, env, projectDir, "switch-lib", beta, "beta")

			republishPushed(t, env, pkgDir, "switch-lib", "4.0.0", "module.exports = 'newest stable';")
			assertOnlyLink(t, env, projectDir, "switch-lib", beta, "beta")
			env.AssertLinkedFileContent(projectDir, "switch-lib", "index.js", tc.wantBeta)
		})
	}
}

// TestSwitchingAProjectBackOntoLatest is the other direction: a project that
// tried a channel goes back to the stable release, and has to start following
// it again - files, link row and all later publishes included.
func TestSwitchingAProjectBackOntoLatest(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.publishPkg("unswitch-lib", "1.0.0", map[string]string{
		"index.js": "module.exports = 'stable';",
	})
	publishTagged(t, env, pkgDir, "unswitch-lib", "2.0.0-beta.1", "module.exports = 'beta';", "beta")

	projectDir := env.newProject("unswitch-project")
	env.addPkg(projectDir, "unswitch-lib@beta", false, false)
	env.addPkg(projectDir, "unswitch-lib", false, false)

	stable, err := env.Database.GetPackageByName("unswitch-lib")
	if err != nil || stable == nil {
		t.Fatalf("Failed to look up unswitch-lib by name: %v", err)
	}
	assertOnlyLink(t, env, projectDir, "unswitch-lib", stable, "")
	env.AssertLinkedFileContent(projectDir, "unswitch-lib", "index.js", "module.exports = 'stable';")

	// The project follows latest again, so a plain publish carries its link
	// across and a pushing one refreshes its files.
	env.republish(pkgDir, "unswitch-lib", "3.0.0", "module.exports = 'newer stable';")
	newer, err := env.Database.GetPackageByName("unswitch-lib")
	if err != nil || newer == nil {
		t.Fatalf("Failed to look up unswitch-lib after the republish: %v", err)
	}
	assertOnlyLink(t, env, projectDir, "unswitch-lib", newer, "")

	republishPushed(t, env, pkgDir, "unswitch-lib", "4.0.0", "module.exports = 'newest stable';")
	newest, err := env.Database.GetPackageByName("unswitch-lib")
	if err != nil || newest == nil {
		t.Fatalf("Failed to look up unswitch-lib after the pushing republish: %v", err)
	}
	assertOnlyLink(t, env, projectDir, "unswitch-lib", newest, "")
	env.AssertLinkedFileContent(projectDir, "unswitch-lib", "index.js", "module.exports = 'newest stable';")

	// The beta build keeps no link of its own, so gc is free to collect it once
	// the tag is dropped.
	beta, err := env.Database.ResolveTag("unswitch-lib", "beta")
	if err != nil || beta == nil {
		t.Fatalf("Failed to resolve the beta tag: %v", err)
	}
	if links, _ := env.Database.GetLinksForPackage(beta.ID); len(links) != 0 {
		t.Errorf("The beta build kept %d link row(s) after the project moved off it", len(links))
	}
}

// TestUnlinkingATaggedConsumerDeletesItsLinkRow pins that the commands which
// tear a project down delete the link the project actually holds.
//
// Both find it by name, and the name index mirrors latest, so a project linked
// to a tagged version had its files unlinked and its lock cleared while the link
// row survived. What is left is not cosmetic: status and `list --projects` keep
// naming a project that no longer consumes the package, `publish --tag --push`
// tries to push into it, and gc counts the link as valid and so never collects
// the version - the one build a tag was deliberately keeping is joined by one
// nothing is keeping on purpose.
//
// Unreachable before add learned tags: every link was on the version latest
// named, so looking it up by name always found it.
func TestUnlinkingATaggedConsumerDeletesItsLinkRow(t *testing.T) {
	cases := []struct {
		name   string
		unlink func(t *testing.T) error
	}{
		{"remove", func(t *testing.T) error { return cli.RunRemove("unlink-lib", false, true) }},
		{"retreat", func(t *testing.T) error { return cli.RunRetreat(true, false) }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := setupTest(t)

			pkgDir := env.publishPkg("unlink-lib", "1.0.0", map[string]string{
				"index.js": "module.exports = 'stable';",
			})
			publishTagged(t, env, pkgDir, "unlink-lib", "2.0.0-beta.1", "module.exports = 'beta';", "beta")

			projectDir := env.newProject("unlink-project")
			env.addPkg(projectDir, "unlink-lib@beta", false, false)

			beta, err := env.Database.ResolveTag("unlink-lib", "beta")
			if err != nil || beta == nil {
				t.Fatalf("Failed to resolve the beta tag: %v", err)
			}
			if links, _ := env.Database.GetLinksForPackage(beta.ID); len(links) != 1 {
				t.Fatalf("The beta build starts with %d link(s), want 1", len(links))
			}

			env.chdir(projectDir)
			captureStdout(t, func() {
				if err := tc.unlink(t); err != nil {
					t.Errorf("Failed to unlink: %v", err)
				}
			})

			links, err := env.Database.GetLinksForPackage(beta.ID)
			if err != nil {
				t.Fatalf("Failed to read the links of the beta build: %v", err)
			}
			if len(links) != 0 {
				t.Errorf("The beta build kept %d link row(s) after %s", len(links), tc.name)
			}
		})
	}
}
