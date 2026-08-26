package store

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/pack"
)

// storeExecutable publishes a package holding one 0644 file and one 0755 file,
// and returns the committed entry's path. The two modes are what the write
// protection has to treat differently: it strips the write bits from both and
// must leave the second one executable.
func storeExecutable(t *testing.T, s *Store, name, hash string) string {
	t.Helper()

	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "bin"), 0755); err != nil {
		t.Fatalf("create source: %v", err)
	}
	plain := filepath.Join(src, "index.js")
	if err := os.WriteFile(plain, []byte("module.exports = 1;\n"), 0644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	script := filepath.Join(src, "bin", "cli.js")
	if err := os.WriteFile(script, []byte("#!/usr/bin/env node\n"), 0644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	// chmod is not masked by the umask, so the fixture really is 0755.
	if err := os.Chmod(script, 0755); err != nil {
		t.Fatalf("chmod source: %v", err)
	}

	files := []*pack.FileInfo{
		{Path: plain, RelPath: "index.js", Size: 20, Mode: 0644},
		{Path: script, RelPath: "bin/cli.js", Size: 20, Mode: 0755},
	}
	entry, err := s.Store(name, hash, files, src)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	return entry
}

// TestStoreWriteProtectsEntryContent is the criterion of #333: a committed store
// entry's content carries no write bit, so a consumer holding a hard link to it
// cannot write through the shared inode and rewrite the canonical copy.
func TestStoreWriteProtectsEntryContent(t *testing.T) {
	s := newTestStore(t)

	entry := storeExecutable(t, s, "protected-pkg", "abc123")

	for _, rel := range []string{"index.js", "bin/cli.js"} {
		info, err := os.Stat(filepath.Join(entry, rel))
		if err != nil {
			t.Fatalf("stat stored %s: %v", rel, err)
		}
		if info.Mode().Perm()&0222 != 0 {
			t.Errorf("stored %s has mode %04o, want no write bits: a consumer's in-place write would reach the store through the shared inode", rel, info.Mode().Perm())
		}
	}
}

// TestStoreKeepsProtectedContentExecutable pins the half of the strip that is
// easy to get wrong: only the write bits go. A 0755 bin script has to land at
// 0555 and not 0444, or every package shipping an executable is broken by the
// protection.
//
// Unix only. Go's os.Stat on Windows synthesises a mode from the file
// attributes, so a protected file reads back as 0444 there whatever it was
// stored with, and the execute bit is not a thing Windows has to lose.
func TestStoreKeepsProtectedContentExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows synthesises the mode from file attributes, so there is no execute bit to preserve")
	}
	s := newTestStore(t)

	entry := storeExecutable(t, s, "exec-pkg", "abc123")

	info, err := os.Stat(filepath.Join(entry, "bin", "cli.js"))
	if err != nil {
		t.Fatalf("stat stored script: %v", err)
	}
	if got := info.Mode().Perm(); got != 0555 {
		t.Errorf("stored bin/cli.js mode = %04o, want 0555 (the protection must strip the write bits and nothing else)", got)
	}
}

// TestStoreLeavesEntryDirectoriesWritable covers the other half of the same
// rule. Unlinking a file needs write permission on its parent directory rather
// than on the file, so a protection pass that reached directories too would
// leave `lnpm gc` unable to delete the entry it had just protected.
//
// Unix only, for the reason gc.go's comment on obstructing a removal gives: a
// directory mode denies nothing on Windows.
func TestStoreLeavesEntryDirectoriesWritable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a directory mode does not deny removal on Windows")
	}
	s := newTestStore(t)

	entry := storeExecutable(t, s, "dirs-pkg", "abc123")

	for _, rel := range []string{".", "bin"} {
		info, err := os.Stat(filepath.Join(entry, rel))
		if err != nil {
			t.Fatalf("stat %s: %v", rel, err)
		}
		if info.Mode().Perm()&0200 == 0 {
			t.Errorf("entry directory %s has mode %04o, want the owner write bit: nothing inside it could be unlinked", rel, info.Mode().Perm())
		}
	}
}

// TestStoreLeavesCompletenessMarkerWritable keeps the store's own bookkeeping
// out of the protection. RemoveEntry unlinks the marker before the tree, and
// the marker is the one file in an entry that the store itself has to be able
// to remove as part of ordinary operation.
func TestStoreLeavesCompletenessMarkerWritable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows synthesises the mode from file attributes")
	}
	s := newTestStore(t)

	entry := storeExecutable(t, s, "marker-pkg", "abc123")

	info, err := os.Stat(filepath.Join(entry, markerName))
	if err != nil {
		t.Fatalf("stat marker: %v", err)
	}
	if info.Mode().Perm()&0200 == 0 {
		t.Errorf("completeness marker mode = %04o, want the owner write bit", info.Mode().Perm())
	}
}

// TestRemoveEntryRemovesWriteProtectedContent is the hazard the protection
// creates and the one that would be worse than the bug it fixes: a `lnpm gc`
// that cannot delete. Removal is a property of the parent directory on Unix,
// and on Windows os.Remove clears the read-only attribute and retries — but
// that is a claim about the platform, so it is measured rather than assumed,
// and this test is what measures it on the Windows CI job.
func TestRemoveEntryRemovesWriteProtectedContent(t *testing.T) {
	s := newTestStore(t)

	entry := storeExecutable(t, s, "doomed-pkg", "abc123")

	if err := RemoveEntry(entry); err != nil {
		t.Fatalf("RemoveEntry on a write-protected entry: %v", err)
	}
	if _, err := os.Stat(entry); !os.IsNotExist(err) {
		t.Errorf("entry %s survived removal (stat err = %v)", entry, err)
	}
}

// TestStoreCleansUpAProtectedTempDirectory covers the write path's own
// interruption. The protection is applied inside the temp directory, before the
// marker and the rename, so a store that fails after it has to remove a tree of
// read-only files — the same removal gc does, one step earlier.
//
// The failure is provoked with an occupied destination: finalize refuses to
// rename onto a directory that is not a complete entry, which leaves the
// deferred cleanup to run with the protection already applied.
func TestStoreCleansUpAProtectedTempDirectory(t *testing.T) {
	s := newTestStore(t)

	occupied := s.PackagePath("blocked-pkg", "abc123")
	if err := os.MkdirAll(occupied, 0755); err != nil {
		t.Fatalf("create occupant: %v", err)
	}
	if err := os.WriteFile(filepath.Join(occupied, "stray.js"), []byte("x"), 0644); err != nil {
		t.Fatalf("write occupant: %v", err)
	}

	src, files := makeSource(t, 2)
	if _, err := s.Store("blocked-pkg", "abc123", files, src); err == nil {
		t.Fatal("expected Store to refuse an occupied destination")
	}

	noTempLeftovers(t, s, "blocked-pkg")
}

// TestStoreAcceptsARepublishOfAProtectedEntry pins the short-circuit: the same
// content published twice must still succeed with the first copy write
// protected, rather than failing somewhere against its own read-only files.
func TestStoreAcceptsARepublishOfAProtectedEntry(t *testing.T) {
	s := newTestStore(t)

	first := storeExecutable(t, s, "again-pkg", "abc123")
	second := storeExecutable(t, s, "again-pkg", "abc123")

	if first != second {
		t.Errorf("republish landed at %s, want the existing entry %s", second, first)
	}
	info, err := os.Stat(filepath.Join(second, "index.js"))
	if err != nil {
		t.Fatalf("stat stored index.js: %v", err)
	}
	if info.Mode().Perm()&0222 != 0 {
		t.Errorf("stored index.js has mode %04o after a republish, want no write bits", info.Mode().Perm())
	}
}

// TestNewProtectsEntriesStoredBeforeProtectionExisted is the migration
// decision. An entry written by an older lnpm is writable, and the consumer
// hard links it already hold stay writable with it, so protecting only what is
// published from now on would leave every existing store exposed for as long as
// its packages are not re-published.
func TestNewProtectsEntriesStoredBeforeProtectionExisted(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows synthesises the mode from file attributes")
	}
	root := t.TempDir()
	t.Setenv("LNPM_STORE", root)

	entry := filepath.Join(root, "store", "@scope", "old-pkg", "abc123")
	if err := os.MkdirAll(filepath.Join(entry, "bin"), 0755); err != nil {
		t.Fatalf("create legacy entry: %v", err)
	}
	if err := os.WriteFile(filepath.Join(entry, "index.js"), []byte("old"), 0644); err != nil {
		t.Fatalf("write legacy content: %v", err)
	}
	script := filepath.Join(entry, "bin", "cli.js")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"), 0644); err != nil {
		t.Fatalf("write legacy script: %v", err)
	}
	if err := os.Chmod(script, 0755); err != nil {
		t.Fatalf("chmod legacy script: %v", err)
	}
	if err := writeMarker(entry, "abc123"); err != nil {
		t.Fatalf("mark legacy entry: %v", err)
	}

	if _, err := New(); err != nil {
		t.Fatalf("new store: %v", err)
	}

	for rel, want := range map[string]os.FileMode{"index.js": 0444, "bin/cli.js": 0555} {
		info, err := os.Stat(filepath.Join(entry, rel))
		if err != nil {
			t.Fatalf("stat %s: %v", rel, err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("existing entry's %s mode = %04o, want %04o", rel, got, want)
		}
	}
	info, err := os.Stat(filepath.Join(entry, markerName))
	if err != nil {
		t.Fatalf("stat marker: %v", err)
	}
	if info.Mode().Perm()&0200 == 0 {
		t.Errorf("the pass protected the completeness marker (mode %04o), which the store has to be able to remove", info.Mode().Perm())
	}
}

// TestNewProtectsExistingEntriesOnlyOnce pins the sentinel. The pass walks every
// file of every entry, which is not a cost to pay on each store open, and it is
// recorded the way backfillLegacyStore records its own one-time decision.
func TestNewProtectsExistingEntriesOnlyOnce(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows synthesises the mode from file attributes")
	}
	root := t.TempDir()
	t.Setenv("LNPM_STORE", root)

	entry := filepath.Join(root, "store", "old-pkg", "abc123")
	if err := os.MkdirAll(entry, 0755); err != nil {
		t.Fatalf("create legacy entry: %v", err)
	}
	if err := os.WriteFile(filepath.Join(entry, "index.js"), []byte("old"), 0644); err != nil {
		t.Fatalf("write legacy content: %v", err)
	}
	if err := writeMarker(entry, "abc123"); err != nil {
		t.Fatalf("mark legacy entry: %v", err)
	}

	if _, err := New(); err != nil {
		t.Fatalf("new store: %v", err)
	}
	// A file the user deliberately made writable again after the migration is
	// the observable proxy for "the pass ran a second time".
	if err := os.Chmod(filepath.Join(entry, "index.js"), 0644); err != nil {
		t.Fatalf("chmod back: %v", err)
	}
	if _, err := New(); err != nil {
		t.Fatalf("reopen store: %v", err)
	}

	info, err := os.Stat(filepath.Join(entry, "index.js"))
	if err != nil {
		t.Fatalf("stat index.js: %v", err)
	}
	if got := info.Mode().Perm(); got != 0644 {
		t.Errorf("index.js mode = %04o, want 0644: the one-time pass ran again on a store that already recorded it", got)
	}
}
