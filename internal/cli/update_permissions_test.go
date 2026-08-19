package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// requirePermissionBits skips tests that depend on Unix permission bits. On
// Windows os models only a read-only bit, so Mode().Perm() reports 0666 or
// 0444 whatever was chmodded, and chmod cannot express the rest.
func requirePermissionBits(t *testing.T) {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("Windows reports only a read-only bit, not Unix permission bits")
	}
}

// requirePermissionEnforcement skips tests that depend on the filesystem
// actually refusing an operation. root bypasses permission bits, and Windows
// does not model them the way these tests assume.
func requirePermissionEnforcement(t *testing.T) {
	t.Helper()

	requirePermissionBits(t)
	if os.Geteuid() == 0 {
		t.Skip("running as root bypasses the permission checks this test relies on")
	}
}

// The downloaded binary lives under the system temp dir, which is frequently a
// different filesystem from the install location - there, renaming it onto the
// target fails with EXDEV and the update can never succeed. installFile must
// therefore stage inside the target's own directory and copy the bytes in.
//
// A source directory the process may not write stands in for that filesystem
// boundary: renaming a file out of a directory needs write permission on that
// directory, reading its bytes does not. An implementation that renames from
// the source cannot pass this test; one that copies into the destination
// directory does not care.
func TestInstallFileDoesNotRenameOutOfTheSourceDirectory(t *testing.T) {
	requirePermissionEnforcement(t)

	src, dst := writeInstallFixture(t, "new-binary", "old-binary")

	srcDir := filepath.Dir(src)
	if err := os.Chmod(srcDir, 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(srcDir, 0755) })

	if err := installFile(src, dst); err != nil {
		t.Fatalf("installFile returned error: %v", err)
	}

	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new-binary" {
		t.Errorf("installed content = %q, want %q", data, "new-binary")
	}
}

// Installing into a directory the user cannot write is the common "installed
// under /usr/local/bin" case. The error has to say what to do about it rather
// than surfacing a bare permission-denied on a temp file name the user has
// never seen.
func TestInstallFileReportsInsufficientPrivileges(t *testing.T) {
	requirePermissionEnforcement(t)

	src, dst := writeInstallFixture(t, "new-binary", "old-binary")

	dstDir := filepath.Dir(dst)
	if err := os.Chmod(dstDir, 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dstDir, 0755) })

	err := installFile(src, dst)
	if err == nil {
		t.Fatal("installFile returned nil for an unwritable destination directory, want an error")
	}
	if !strings.Contains(err.Error(), "privileges") {
		t.Errorf("error = %q, want it to tell the user to re-run with sufficient privileges", err)
	}
}

// The privileges guidance has to be reachable on the path a real user takes.
// replaceBinary backs the current binary up before it ever calls installFile,
// and that rename needs write permission on the very same directory - so an
// lnpm installed under a root-owned /usr/local/bin fails there first. Guidance
// that only installFile produces is guidance the user never sees.
func TestReplaceBinaryReportsInsufficientPrivileges(t *testing.T) {
	requirePermissionEnforcement(t)

	src, dst := writeInstallFixture(t, "new-binary", "old-binary")

	dstDir := filepath.Dir(dst)
	if err := os.Chmod(dstDir, 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dstDir, 0755) })

	err := replaceBinary(src, dst)
	if err == nil {
		t.Fatal("replaceBinary returned nil for an unwritable destination directory, want an error")
	}
	if !strings.Contains(err.Error(), "privileges") {
		t.Errorf("error = %q, want it to tell the user to re-run with sufficient privileges", err)
	}
	if !strings.Contains(err.Error(), dstDir) {
		t.Errorf("error = %q, want it to name the directory %q", err, dstDir)
	}
}

// The staging file is created by os.CreateTemp, which makes it 0600 and
// unusable as a binary. installFile owns making the installed file executable.
// root does not change what chmod records, so this one only needs the bits to
// exist at all - it must still run as root.
func TestInstallFileMakesTheInstalledBinaryExecutable(t *testing.T) {
	requirePermissionBits(t)

	src, dst := writeInstallFixture(t, "new-binary", "old-binary")

	if err := installFile(src, dst); err != nil {
		t.Fatalf("installFile returned error: %v", err)
	}

	info, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0755 {
		t.Errorf("installed binary mode = %04o, want 0755", got)
	}
}
