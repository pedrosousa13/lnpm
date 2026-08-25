//go:build linux

package workspace

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// requireWithinRoot's other refusal is the member that will not resolve, and it
// has to reach the user from a member subdirectory for the same reason the
// escape refusal does: `lnpm publish` run inside a member finds the workspace
// config on a later iteration of Detect's walk, where the starting-directory
// rule swallows an unrecognised error and answers "no workspace found".
//
// Constructing it needs a member whose package.json stats but whose directory
// will not resolve, and on Linux the two are hard to separate: expandGlobs stats
// <member>/package.json first, and everything filepath.EvalSymlinks lstats on
// the way to <member> the kernel already walked for that stat, so a permission
// or existence failure that stops one stops the other. A symlink loop does not
// help - the stat fails first. Neither does link depth, which fails the other
// way round: measured here, a chain of 50 links makes os.Stat return ELOOP while
// EvalSymlinks resolves it, because the kernel's traversal limit is the smaller
// of the two and walkSymlinks only gives up past 255.
//
// /proc/self/fd/N is the one asymmetry available unprivileged. The kernel jumps
// straight to the open directory when it resolves that path, rechecking no
// ancestor, so the stat succeeds; filepath.EvalSymlinks instead reads the magic
// link and walks the string it gets back, which runs through a directory this
// test has made untraversable. The failure is therefore a real EACCES from the
// resolution path and not a filesystem faked behind a seam - but the shape is
// contrived, and it is Linux-only.
func TestDetectRefusesAMemberThatWillNotResolveFromASubdirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: mode 0000 does not deny traversal")
	}

	base := t.TempDir()
	root := filepath.Join(base, "ws")
	member := writePackage(t, root, "packages/package-a")

	// The member the refusal is about: its package.json is reachable through
	// an open directory descriptor, its path is not reachable at all.
	blocked := filepath.Join(base, "blocked")
	target := writePackage(t, blocked, "pkg")

	dir, err := os.Open(target)
	if err != nil {
		t.Fatalf("Failed to open %s: %v", target, err)
	}
	t.Cleanup(func() { dir.Close() })

	if err := os.Chmod(blocked, 0000); err != nil {
		t.Fatalf("Failed to make %s untraversable: %v", blocked, err)
	}
	// t.TempDir's cleanup cannot remove a 0000 directory's contents.
	t.Cleanup(func() { _ = os.Chmod(blocked, 0755) })

	link := filepath.Join(root, "packages", "unresolvable")
	symlinkDirAt(t, fmt.Sprintf("/proc/self/fd/%d", dir.Fd()), link)

	// The fixture only counts if the two calls really do disagree, so check
	// that before asking what Detect did with the disagreement.
	//
	// These skip rather than fail, the same posture symlinkDirAt takes: the
	// asymmetry they set up is a property of the host's /proc, not of lnpm, and
	// a host that does not provide it has failed to build the fixture rather
	// than found a bug. The cost is that a skip here pins nothing, so the run
	// this lands on is read for the row rather than assumed - see
	// docs/agents/verification-discipline.md, "Confirm the run you are reading".
	if _, err := os.Stat(filepath.Join(link, "package.json")); err != nil {
		t.Skipf("cannot stat %s/package.json through the descriptor: %v", link, err)
	}
	if _, err := filepath.EvalSymlinks(link); !errors.Is(err, fs.ErrPermission) {
		t.Skipf("cannot make %s unresolvable while its package.json stats: %v", link, err)
	}

	contents := `{"name":"root","workspaces":["packages/*"]}`
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(contents), 0644); err != nil {
		t.Fatalf("Failed to write the root package.json: %v", err)
	}

	ws, err := Detect(member)

	if err == nil {
		t.Fatalf("Expected Detect(%s) to refuse the member that will not resolve", member)
	}
	if !errors.Is(err, ErrWorkspaceMemberRefused) {
		t.Errorf("Expected the refusal to wrap ErrWorkspaceMemberRefused, got: %v", err)
	}
	// The permission error is what separates this branch from the containment
	// one, which reports no underlying error at all.
	if !errors.Is(err, fs.ErrPermission) {
		t.Errorf("Expected the refusal to carry the resolution failure, got: %v", err)
	}
	if !strings.Contains(err.Error(), link) {
		t.Errorf("Expected the error to name the member %s, got: %v", link, err)
	}
	if ws != nil {
		t.Errorf("Expected no workspace alongside the refusal, got %+v", ws)
	}
}
