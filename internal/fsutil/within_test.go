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
// so beats reporting a pass the run did not earn. Every case that calls this
// helper calls it before it asserts anything, so a skip is never a silent pass
// there. The four cases that never symlink at all - the root itself, a nested
// directory, the prefix-sharing sibling, and the missing path - do not reach it
// and run everywhere.
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

// The two arguments do not have to be spelled alike: one can be relative and the
// other absolute. That only works if each is made absolute before its symlinks
// are followed, because filepath.Abs joins a relative path to whatever os.Getwd
// reports - and os.Getwd returns $PWD when it names the current directory, which
// a shell (and t.Chdir) sets to the spelling it was handed rather than to the
// real one. Resolve first and absolutise after, and the relative side stops at
// that unresolved spelling while the absolute side is fully resolved, so the two
// are compared across different trees and every member reads as an escape.
//
// The symlinked working directory here stands in for macOS, where /var is a link
// to /private/var and a shell run under /var/folders/... reports exactly that.
func TestWithinRootAcceptsARelativeRootUnderASymlinkedWorkingDirectory(t *testing.T) {
	base := t.TempDir()
	real := mkdirAt(t, filepath.Join(base, "real"))
	linkedRoot := filepath.Join(base, "root")
	symlinkAt(t, real, linkedRoot)

	member := mkdirAt(t, filepath.Join(real, "packages", "a"))

	// t.Chdir sets PWD to this spelling, so os.Getwd - and through it
	// filepath.Abs - reports the link rather than its target.
	t.Chdir(linkedRoot)

	within, resolved, err := WithinRoot(".", member)
	if err != nil {
		t.Fatalf("WithinRoot(., %s) failed: %v", member, err)
	}
	if !within {
		t.Errorf("Expected %s to be within the relative root . under the symlinked cwd %s, got resolved %s",
			member, linkedRoot, resolved)
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

	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("Failed to absolutise %s: %v", path, err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		t.Fatalf("Failed to resolve %s: %v", abs, err)
	}
	return resolved
}
