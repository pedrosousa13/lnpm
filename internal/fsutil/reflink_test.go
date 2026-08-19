package fsutil

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// writeFile writes content to path, failing the test if it cannot.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write %s: %v", path, err)
	}
}

// assertContent asserts that path holds exactly want.
func assertContent(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Failed to read %s: %v", path, err)
	}
	if string(got) != want {
		t.Errorf("Content of %s = %q, want %q", path, string(got), want)
	}
}

// TestReflink_ClonesIndependentCopy is the acceptance check for the darwin
// clonefile path: Reflink must succeed on APFS (t.TempDir() is on APFS there)
// and produce a logically independent copy. The two-way isolation check is what
// distinguishes a copy-on-write clone from a hard link — a hard link shares the
// inode, so a write through either name would be visible through the other.
func TestReflink_ClonesIndependentCopy(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Skipping - clonefile is darwin-only; other platforms have their own reflink path")
	}

	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	dst := filepath.Join(dir, "dst.txt")

	writeFile(t, src, "original")

	if err := Reflink(src, dst); err != nil {
		t.Fatalf("Reflink failed on APFS: %v", err)
	}

	assertContent(t, dst, "original")

	// Writing through dst must not be visible through src.
	writeFile(t, dst, "changed via dst")
	assertContent(t, src, "original")

	// And writing through src must not be visible through dst.
	writeFile(t, src, "changed via src")
	assertContent(t, dst, "changed via dst")
}

// TestReflink_AgreesWithTryReflink pins the fallback contract that both callers
// rely on: tryReflink reports whether cloning worked, Reflink turns that into a
// nil error or a "not supported" error, and neither panics. It asserts the
// relationship rather than a fixed outcome, so it holds on a filesystem without
// cloning (ext4, the usual dev host) and on one with it (APFS, Btrfs, XFS).
func TestReflink_AgreesWithTryReflink(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	writeFile(t, src, "payload")

	probe := filepath.Join(dir, "probe.txt")
	supported := tryReflink(src, probe)

	dst := filepath.Join(dir, "dst.txt")
	err := Reflink(src, dst)

	if supported {
		if err != nil {
			t.Errorf("tryReflink reported success but Reflink returned %v", err)
		}
		assertContent(t, dst, "payload")
		assertContent(t, probe, "payload")
		return
	}

	// Unsupported filesystem: the caller must get an error to fall back on,
	// not a panic and not a silent success.
	if err == nil {
		t.Error("tryReflink reported failure but Reflink returned nil, so callers would skip the copy fallback")
	}
	if _, statErr := os.Stat(probe); statErr == nil {
		t.Error("tryReflink failed but left the destination behind; a partial file would be mistaken for a good copy")
	}
}

// TestReflink_FailsWithoutPanicOnMissingSource exercises the failure branch on
// every platform, including a CoW-capable one where the test above takes the
// success branch. A missing source can never be cloned, so Reflink must report
// "not supported" and leave nothing behind.
func TestReflink_FailsWithoutPanicOnMissingSource(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "does-not-exist.txt")
	dst := filepath.Join(dir, "dst.txt")

	if tryReflink(src, dst) {
		t.Fatal("tryReflink reported success for a source that does not exist")
	}
	if err := Reflink(src, dst); err == nil {
		t.Error("Reflink returned nil for a source that does not exist")
	}
	if _, err := os.Stat(dst); err == nil {
		t.Error("Reflink created a destination for a source that does not exist")
	}
}
