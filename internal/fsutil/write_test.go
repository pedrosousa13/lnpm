package fsutil

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// These tests cover WriteFileAtomic, the temp-file-and-rename sequence every
// lnpm write of a hand-authored file goes through.
//
// They assert on the two things the rename gives away and the direct write did
// not: the destination's mode, which a rename replaces with the temp file's, and
// the destination's *existence*, which a rename replaces whatever the mode said.
//
// Modes are set with os.Chmod rather than through os.WriteFile's mode argument,
// which the process umask masks. The suite runs under umask 022, and an
// assertion that only holds there would be a fixture artefact rather than a
// statement about WriteFileAtomic.

// tempLeftovers lists the staging files WriteFileAtomic left behind in dir.
//
// The pattern matches os.CreateTemp's, which puts the random component before
// the suffix, so a leftover is `package.json.1234567890.tmp` rather than
// anything ending in the base name.
func tempLeftovers(t *testing.T, dir string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "*.tmp"))
	if err != nil {
		t.Fatalf("Glob(%s) error: %v", dir, err)
	}
	return matches
}

// modeOf reports path's permission bits.
func modeOf(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%s) error: %v", path, err)
	}
	return info.Mode().Perm()
}

// skipIfNoUnixPermissions skips a test whose subject is a permission bit other
// than the owner-write one. Windows models only that bit, so Mode().Perm()
// reports 0666 or 0444 there and nothing else can be asserted.
func skipIfNoUnixPermissions(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Windows reports only a read-only bit, not Unix permission bits")
	}
}

// TestWriteFileAtomicPreservesTheModeOfAnExistingFile pins the property the
// rename would otherwise destroy. os.CreateTemp makes its file 0600, and a
// rename hands the destination the temp file's mode, so an executable or a
// deliberately tightened file would come back 0600 unless the mode is carried
// across on purpose.
func TestWriteFileAtomicPreservesTheModeOfAnExistingFile(t *testing.T) {
	skipIfNoUnixPermissions(t)

	for _, mode := range []os.FileMode{0600, 0755} {
		t.Run(mode.String(), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "package.json")
			if err := os.WriteFile(path, []byte("original\n"), 0644); err != nil {
				t.Fatalf("WriteFile() error: %v", err)
			}
			if err := os.Chmod(path, mode); err != nil {
				t.Fatalf("Chmod() error: %v", err)
			}

			if err := WriteFileAtomic(path, []byte("replacement\n"), 0644); err != nil {
				t.Fatalf("WriteFileAtomic() error: %v", err)
			}

			if got := modeOf(t, path); got != mode {
				t.Errorf("mode = %o, want the file's own %o", got, mode)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("ReadFile() error: %v", err)
			}
			if string(data) != "replacement\n" {
				t.Errorf("contents = %q, want the replacement", data)
			}
		})
	}
}

// TestWriteFileAtomicGivesANewFileTheDefaultMode covers the other half: there is
// no existing mode to carry over, so the caller's default decides, and the
// explicit Chmod means the umask does not get a say in it either.
func TestWriteFileAtomicGivesANewFileTheDefaultMode(t *testing.T) {
	skipIfNoUnixPermissions(t)

	path := filepath.Join(t.TempDir(), "package.json")

	if err := WriteFileAtomic(path, []byte("fresh\n"), 0644); err != nil {
		t.Fatalf("WriteFileAtomic() error: %v", err)
	}

	if got := modeOf(t, path); got != 0644 {
		t.Errorf("mode = %o, want the default 0644", got)
	}
}

// TestWriteFileAtomicRefusesAReadOnlyFile pins the guard that pays for the
// rename: a rename replaces the destination whatever its mode, so a file the
// owner marked read-only would be quietly overwritten unless the refusal is made
// explicitly from the mode.
//
// It covers the ordinary case only, which is all the guard claims. The guard
// tests the owner-write bit and so is not the kernel's permission check, which
// also turns on ownership and effective uid: it is weaker for another user's
// 0644 file (which a direct open would refuse and the rename will not) and
// stricter under root (which ignores the mode the guard reads). WriteFileAtomic's
// doc comment carries the reasoning; this test does not attempt either case,
// since neither can be constructed as an ordinary unprivileged user.
func TestWriteFileAtomicRefusesAReadOnlyFile(t *testing.T) {
	skipIfNoUnixPermissions(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "package.json")
	if err := os.WriteFile(path, []byte("original\n"), 0644); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	if err := os.Chmod(path, 0444); err != nil {
		t.Fatalf("Chmod() error: %v", err)
	}

	err := WriteFileAtomic(path, []byte("replacement\n"), 0644)
	if err == nil {
		t.Fatal("WriteFileAtomic() error = nil, want a refusal to replace a read-only file")
	}
	if !strings.Contains(err.Error(), "read-only") {
		t.Errorf("error = %q, want it to say the file is read-only", err)
	}

	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("ReadFile() error: %v", readErr)
	}
	if string(data) != "original\n" {
		t.Errorf("the refused write landed anyway; contents = %q", data)
	}
	if leftovers := tempLeftovers(t, dir); len(leftovers) != 0 {
		t.Errorf("refusing the write left %v behind; it should not have opened anything", leftovers)
	}
}

// TestWriteFileAtomicLeavesTheOriginalIntactWhenTheWriteFails is the reason the
// whole sequence exists. The failure is injected where a real one lands - the
// staging file cannot be created, as it could not on a full disk - and the
// original has to still be there, byte for byte, with its own mode.
//
// Two limits on what this proves, both deliberate.
//
// It does not measure the atomic write against the truncating one it replaced.
// The read-only directory is a failure only staging can hit - os.WriteFile needs
// no new directory entry to rewrite an existing file, so it simply succeeds
// here. The failure that motivates the change, a crash or a full disk between
// the open and the last byte, cannot be injected without a fake filesystem.
//
// And it injects *before* any byte is written, so the original surviving is
// close to trivially true: nothing had been attempted against path yet. It pins
// the early-failure path only. The post-write case - bytes written and chmodded,
// failure at the rename - is the one that could actually have damaged path, and
// it is covered by TestWriteFileAtomicSurvivesAFailedRenameWithoutLeavingAStagingFile.
func TestWriteFileAtomicLeavesTheOriginalIntactWhenTheWriteFails(t *testing.T) {
	skipIfNoUnixPermissions(t)
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions, so the write cannot be made to fail this way")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "package.json")
	const original = "{\n  \"name\": \"demo\"\n}\n"
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	if err := os.Chmod(path, 0640); err != nil {
		t.Fatalf("Chmod() error: %v", err)
	}

	// Read-only directory: the file is still writable, but no new entry can be
	// created in it, so os.CreateTemp fails.
	if err := os.Chmod(dir, 0555); err != nil {
		t.Fatalf("Chmod(dir) error: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0755) })

	if err := WriteFileAtomic(path, []byte("clobbered\n"), 0644); err == nil {
		t.Fatal("WriteFileAtomic() error = nil, want the staging file's creation to fail")
	}

	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatalf("Chmod(dir) error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the original is unreadable after a failed write: %v", err)
	}
	if string(data) != original {
		t.Errorf("the original did not survive the failed write; contents = %q", data)
	}
	if got := modeOf(t, path); got != 0640 {
		t.Errorf("mode = %o after a failed write, want the untouched 0640", got)
	}
	if leftovers := tempLeftovers(t, dir); len(leftovers) != 0 {
		t.Errorf("a failed write left %v behind", leftovers)
	}
}

// TestWriteFileAtomicSurvivesAFailedRenameWithoutLeavingAStagingFile reaches the
// case the read-only directory cannot: the data was fully written to the staging
// file and chmodded, and only the last step failed. This is the genuine
// mid-write failure - the point at which a truncating write has already
// destroyed the original - so it carries both assertions. The destination has to
// be untouched, and the staging file has to be gone; nothing else proves the
// deferred cleanup runs, and a staging file surviving beside package.json would
// be committed by someone eventually.
//
// The rename is made to fail by pointing path at a directory, which rename(2)
// refuses to replace with a regular file. That shape is not arbitrary: it is the
// only rename failure constructible here without root, since a same-directory
// rename over a regular file has no ordinary way to fail once staging has
// succeeded. The cost is that "the destination survived" has to be read off the
// directory rather than off a file's bytes, so the directory is given a sentinel
// child to make its contents observable.
func TestWriteFileAtomicSurvivesAFailedRenameWithoutLeavingAStagingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "package.json")
	if err := os.Mkdir(path, 0755); err != nil {
		t.Fatalf("Mkdir() error: %v", err)
	}
	sentinel := filepath.Join(path, "sentinel.txt")
	const sentinelBody = "untouched\n"
	if err := os.WriteFile(sentinel, []byte(sentinelBody), 0644); err != nil {
		t.Fatalf("WriteFile(sentinel) error: %v", err)
	}

	if err := WriteFileAtomic(path, []byte("replacement\n"), 0644); err == nil {
		t.Fatal("WriteFileAtomic() error = nil, want the rename onto a directory to fail")
	}

	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("the destination is gone after a failed rename: %v", err)
	}
	if !info.IsDir() {
		t.Errorf("the failed rename replaced the destination; it is now mode %v", info.Mode())
	}
	body, err := os.ReadFile(sentinel)
	if err != nil {
		t.Fatalf("the destination's contents did not survive the failed rename: %v", err)
	}
	if string(body) != sentinelBody {
		t.Errorf("sentinel = %q, want the untouched %q", body, sentinelBody)
	}

	if leftovers := tempLeftovers(t, dir); len(leftovers) != 0 {
		t.Errorf("a failed rename left %v behind", leftovers)
	}
}
