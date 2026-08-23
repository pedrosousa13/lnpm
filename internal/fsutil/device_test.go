package fsutil

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDeviceIDOfPathIsNonZeroAndSharedWithinOneFilesystem is the portable
// guarantee gc's unreachable-project check rests on: two paths on one
// filesystem report the same non-zero identifier.
//
// The non-zero half is what makes this test worth running on Windows. There the
// FileInfo-shaped DeviceID returns 0 for everything, because a Win32
// FileAttributeData carries no volume identity; only the by-handle form does. A
// build where the Windows implementation regressed to that stub would fail here
// rather than quietly degrade gc to its pre-fix behaviour on one platform.
func TestDeviceIDOfPathIsNonZeroAndSharedWithinOneFilesystem(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.txt")
	b := filepath.Join(dir, "sub", "b.txt")
	if err := os.MkdirAll(filepath.Dir(b), 0755); err != nil {
		t.Fatalf("seed: %v", err)
	}
	for _, p := range []string{a, b} {
		if err := os.WriteFile(p, []byte("x"), 0644); err != nil {
			t.Fatalf("seed %s: %v", p, err)
		}
	}

	devA := DeviceIDOfPath(a)
	devB := DeviceIDOfPath(b)
	devDir := DeviceIDOfPath(dir)

	if devA == 0 {
		t.Fatalf("DeviceIDOfPath(%s) = 0, want a real identifier", a)
	}
	if devA != devB {
		t.Errorf("DeviceIDOfPath disagreed for two files on one filesystem: %d != %d", devA, devB)
	}
	if devA != devDir {
		t.Errorf("DeviceIDOfPath disagreed between a file and its directory: %d != %d", devA, devDir)
	}
}

// TestDeviceIDOfPathIsZeroForAMissingPath pins the value that means "unknown".
// gc reads a zero as "no evidence either way" and falls back to treating a
// missing project directory as deleted, so this is not merely an error code:
// it is the input to the branch that keeps acceptance criterion 2 working.
func TestDeviceIDOfPathIsZeroForAMissingPath(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-directory")
	if got := DeviceIDOfPath(missing); got != 0 {
		t.Errorf("DeviceIDOfPath(%s) = %d, want 0 for a path that does not exist", missing, got)
	}
}

// TestDeviceIDOfPathWorksOnADirectory pins the case gc actually uses. On Windows
// a directory cannot be opened with CreateFile unless FILE_FLAG_BACKUP_SEMANTICS
// is passed; without it this returns 0 and every gc verdict falls back to the
// pre-fix behaviour, on directories only, which is the sort of failure that
// would not show up in a test that only ever measured files.
func TestDeviceIDOfPathWorksOnADirectory(t *testing.T) {
	dir := t.TempDir()
	if got := DeviceIDOfPath(dir); got == 0 {
		t.Errorf("DeviceIDOfPath(%s) = 0 for an existing directory", dir)
	}
}
