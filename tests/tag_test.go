package tests

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/cli"
	"github.com/pedrosousa13/lnpm/internal/db"
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
