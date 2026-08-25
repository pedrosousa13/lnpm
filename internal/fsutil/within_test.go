package fsutil

import (
	"os"
	"path/filepath"
	"testing"
)

// symlinkAt points linkPath at target, and skips the test when the platform will
// not have it.
//
// Windows creates a symlink only with the symlink privilege or developer mode
// turned on, so a refusal there means the guard was never exercised, and saying
// so beats reporting a pass the run did not earn. Every case below reaches this
// helper before it asserts anything, so a skip is never a silent pass.
func symlinkAt(t *testing.T, target, linkPath string) {
	t.Helper()

	if err := os.Symlink(target, linkPath); err != nil {
		t.Skipf("cannot create a symlink at %s: %v", linkPath, err)
	}
}

// mkdirAt creates dir and every missing parent of it.
func mkdirAt(t *testing.T, dir string) string {
	t.Helper()

	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("Failed to create %s: %v", dir, err)
	}
	return dir
}

func TestWithinRootAcceptsTheRootItself(t *testing.T) {
	root := t.TempDir()

	within, resolved, err := WithinRoot(root, root)
	if err != nil {
		t.Fatalf("WithinRoot(%s, %s) failed: %v", root, root, err)
	}
	if !within {
		t.Errorf("Expected the root itself to be within the root, got resolved %s", resolved)
	}
}

func TestWithinRootAcceptsANestedDirectory(t *testing.T) {
	root := t.TempDir()
	nested := mkdirAt(t, filepath.Join(root, "packages", "a"))

	within, resolved, err := WithinRoot(root, nested)
	if err != nil {
		t.Fatalf("WithinRoot(%s, %s) failed: %v", root, nested, err)
	}
	if !within {
		t.Errorf("Expected %s to be within %s, got resolved %s", nested, root, resolved)
	}
}

// A sibling whose name extends the root's is the case a prefix test without a
// trailing separator gets wrong: "/ws-evil" starts with "/ws" and is not inside
// it.
func TestWithinRootRejectsASiblingSharingThePrefix(t *testing.T) {
	base := t.TempDir()
	root := mkdirAt(t, filepath.Join(base, "ws"))
	sibling := mkdirAt(t, filepath.Join(base, "ws-evil"))

	within, _, err := WithinRoot(root, sibling)
	if err != nil {
		t.Fatalf("WithinRoot(%s, %s) failed: %v", root, sibling, err)
	}
	if within {
		t.Errorf("Expected %s to be outside %s", sibling, root)
	}
}

func TestWithinRootFollowsASymlinkOutOfTheRoot(t *testing.T) {
	base := t.TempDir()
	root := mkdirAt(t, filepath.Join(base, "ws"))
	outside := mkdirAt(t, filepath.Join(base, "outside"))
	link := filepath.Join(root, "escape")
	symlinkAt(t, outside, link)

	within, resolved, err := WithinRoot(root, link)
	if err != nil {
		t.Fatalf("WithinRoot(%s, %s) failed: %v", root, link, err)
	}
	if within {
		t.Errorf("Expected %s to be outside %s", link, root)
	}
	if resolved != mustEvalSymlinks(t, outside) {
		t.Errorf("Expected the resolved path to be %s, got %s", outside, resolved)
	}
}

// A single-level os.Readlink stops at the first hop, which lands back inside the
// root here and reads as containment.
func TestWithinRootFollowsAChainOutOfTheRoot(t *testing.T) {
	base := t.TempDir()
	root := mkdirAt(t, filepath.Join(base, "ws"))
	outside := mkdirAt(t, filepath.Join(base, "outside"))

	hop := filepath.Join(root, "hop")
	symlinkAt(t, outside, hop)
	link := filepath.Join(root, "escape")
	symlinkAt(t, hop, link)

	within, resolved, err := WithinRoot(root, link)
	if err != nil {
		t.Fatalf("WithinRoot(%s, %s) failed: %v", root, link, err)
	}
	if within {
		t.Errorf("Expected %s to be outside %s", link, root)
	}
	if resolved != mustEvalSymlinks(t, outside) {
		t.Errorf("Expected the resolved path to be %s, got %s", outside, resolved)
	}
}

func TestWithinRootAcceptsASymlinkThatStaysInside(t *testing.T) {
	root := t.TempDir()
	target := mkdirAt(t, filepath.Join(root, "real", "a"))
	link := filepath.Join(root, "alias")
	symlinkAt(t, target, link)

	within, resolved, err := WithinRoot(root, link)
	if err != nil {
		t.Fatalf("WithinRoot(%s, %s) failed: %v", root, link, err)
	}
	if !within {
		t.Errorf("Expected %s to be within %s, got resolved %s", link, root, resolved)
	}
}

// The root reaches WithinRoot as whatever spelling the caller had, and that
// spelling can itself run through a symlink - on macOS /var is a link to
// /private/var and t.TempDir() hands back a /var/folders/... path. Resolving
// only the member makes every member of such a root look like an escape.
func TestWithinRootResolvesTheRootToo(t *testing.T) {
	base := t.TempDir()
	real := mkdirAt(t, filepath.Join(base, "real"))
	linkedRoot := filepath.Join(base, "root")
	symlinkAt(t, real, linkedRoot)

	member := mkdirAt(t, filepath.Join(real, "packages", "a"))

	within, resolved, err := WithinRoot(linkedRoot, member)
	if err != nil {
		t.Fatalf("WithinRoot(%s, %s) failed: %v", linkedRoot, member, err)
	}
	if !within {
		t.Errorf("Expected %s to be within the symlinked root %s, got resolved %s", member, linkedRoot, resolved)
	}
}

func TestWithinRootFailsOnAPathThatDoesNotResolve(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "gone")

	if _, _, err := WithinRoot(root, missing); err == nil {
		t.Errorf("Expected WithinRoot(%s, %s) to fail on a path that does not exist", root, missing)
	}
}

// mustEvalSymlinks gives the spelling WithinRoot reports, so an assertion on the
// resolved path does not depend on how the test's own temp directory was reached.
func mustEvalSymlinks(t *testing.T, path string) string {
	t.Helper()

	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("Failed to resolve %s: %v", path, err)
	}
	abs, err := filepath.Abs(resolved)
	if err != nil {
		t.Fatalf("Failed to absolutise %s: %v", resolved, err)
	}
	return abs
}
