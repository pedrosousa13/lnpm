package store

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// seedLegacyEntry creates a store entry the way a version of lnpm without
// completeness markers left it: a directory of files and nothing else. It is
// also, byte for byte, what an interrupted `lnpm gc` leaves — which is why
// neither can be served.
func seedLegacyEntry(t *testing.T, storeRoot, name, hash string) string {
	t.Helper()
	entry := filepath.Join(storeRoot, filepath.FromSlash(name), hash)
	if err := os.MkdirAll(entry, 0755); err != nil {
		t.Fatalf("seed entry: %v", err)
	}
	if err := os.WriteFile(filepath.Join(entry, "legacy.js"), []byte("legacy"), 0644); err != nil {
		t.Fatalf("seed entry file: %v", err)
	}
	return entry
}

// legacyStoreRoot returns the store directory a Store would use, with LNPM_STORE
// already pointing at a fresh temp dir, so entries can be seeded before the
// Store is opened for the first time.
func legacyStoreRoot(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	t.Setenv("LNPM_STORE", base)
	root := filepath.Join(base, "store")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatalf("create store root: %v", err)
	}
	return root
}

// TestIncompleteEntriesFindsUnmarkedEntries is what `lnpm doctor` reports from.
// The scan reaches unscoped and scoped entries alike, since the scoped ones sit
// one directory deeper.
func TestIncompleteEntriesFindsUnmarkedEntries(t *testing.T) {
	root := legacyStoreRoot(t)
	plain := seedLegacyEntry(t, root, "left-pad", "aaa111")
	scoped := seedLegacyEntry(t, root, "@scope/ui", "bbb222")

	incomplete, unreadable, err := IncompleteEntries()
	if err != nil {
		t.Fatalf("scan store: %v", err)
	}

	if unreadable != 0 {
		t.Errorf("scan could not read %d directories of a store it should have read whole", unreadable)
	}
	assertSameEntries(t, incomplete, []string{plain, scoped})
}

// TestIncompleteEntriesPassesACommittedEntry is the other direction: the scan
// must not report the entries the write path wrote, or doctor would call every
// healthy store broken.
func TestIncompleteEntriesPassesACommittedEntry(t *testing.T) {
	s := newTestStore(t)

	src, files := makeSource(t, 2)
	if _, err := s.Store("healthy-pkg", "aaa111", files, src); err != nil {
		t.Fatalf("store: %v", err)
	}

	incomplete, _, err := IncompleteEntries()
	if err != nil {
		t.Fatalf("scan store: %v", err)
	}
	if len(incomplete) != 0 {
		t.Errorf("scan reported %v in a store holding one freshly stored entry", incomplete)
	}
}

// TestIncompleteEntriesReportsDirectoriesItCannotRead is the other thing the
// sweep tells doctor, and the reason it is a count rather than a silence: an
// entry inside a directory the sweep could not open has been checked by
// nothing, and reporting the store clean would be a claim the sweep did not
// establish.
func TestIncompleteEntriesReportsDirectoriesItCannotRead(t *testing.T) {
	requireUnreadableDirectories(t)

	root := legacyStoreRoot(t)
	blocked := filepath.Dir(seedLegacyEntry(t, root, "blocked-pkg", "aaa111"))
	chmodForTest(t, blocked, 0000)

	incomplete, unreadable, err := IncompleteEntries()
	if err != nil {
		t.Fatalf("scan store: %v", err)
	}

	if unreadable != 1 {
		t.Errorf("scan reported %d unreadable directories for a store with one unreadable package directory (%s)", unreadable, blocked)
	}
	if len(incomplete) != 0 {
		t.Errorf("scan listed %v, but the entry it would have found sits inside the directory it could not read", incomplete)
	}
}

// TestNewMarksAStoreThatNeverHadMarkers is the upgrade path from lnpm 1.x.
// Completeness markers shipped in 2.0.0, so a store in which nothing is marked
// was written by a release that had none, and refusing every entry in it would
// break every command against a store lnpm itself wrote.
func TestNewMarksAStoreThatNeverHadMarkers(t *testing.T) {
	root := legacyStoreRoot(t)
	plain := seedLegacyEntry(t, root, "left-pad", "aaa111")
	scoped := seedLegacyEntry(t, root, "@scope/ui", "bbb222")

	s, err := New()
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	for _, entry := range []string{plain, scoped} {
		if err := CheckComplete(entry); err != nil {
			t.Errorf("a pre-2.0.0 entry was left unusable after the store was opened: %v", err)
		}
	}
	if _, err := s.GetFiles("@scope/ui", "bbb222"); err != nil {
		t.Errorf("a pre-2.0.0 entry could not be read after the store was opened: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, sentinelName)); err != nil {
		t.Errorf("the backfill did not record its decision: %v", err)
	}
}

// TestNewMarksALegacyStoreOnlyOnce pins the sentinel as the guard. Once the
// decision is recorded, a directory that appears later without a marker is an
// interrupted deletion, and marking it would be the laundering of #330.
func TestNewMarksALegacyStoreOnlyOnce(t *testing.T) {
	root := legacyStoreRoot(t)
	seedLegacyEntry(t, root, "left-pad", "aaa111")

	if _, err := New(); err != nil {
		t.Fatalf("new store: %v", err)
	}
	late := seedLegacyEntry(t, root, "half-gone", "ccc333")

	if _, err := New(); err != nil {
		t.Fatalf("reopen store: %v", err)
	}

	if err := CheckComplete(late); err == nil {
		t.Errorf("the entry %s that appeared after the migration was marked complete", late)
	}
	if err := CheckComplete(filepath.Join(root, "left-pad", "aaa111")); err != nil {
		t.Errorf("the migration's work was lost on reopen: %v", err)
	}
}

// TestNewLeavesAStoreItCannotClassify pins the gate's safe direction. The gate
// asks whether the store has ever been marked, so a package directory the scan
// cannot open makes the answer unknown - a marked entry could be sitting in it.
// Marking on a guess would launder a gutted 2.x entry, so nothing is marked,
// no decision is recorded, and the next open tries again.
//
// It also covers the thing every command depends on: opening the store must
// never fail because one directory is unreadable, or a single bad mode would
// take every lnpm command down with it.
func TestNewLeavesAStoreItCannotClassify(t *testing.T) {
	requireUnreadableDirectories(t)

	root := legacyStoreRoot(t)
	reachable := seedLegacyEntry(t, root, "left-pad", "aaa111")
	blocked := filepath.Dir(seedLegacyEntry(t, root, "blocked-pkg", "bbb222"))
	chmodForTest(t, blocked, 0000)

	if _, err := New(); err != nil {
		t.Fatalf("opening a store with one unreadable package directory failed: %v", err)
	}

	if err := CheckComplete(reachable); err == nil {
		t.Errorf("entries were marked in a store the gate could not classify (%s unreadable)", blocked)
	}
	if _, err := os.Stat(filepath.Join(root, sentinelName)); !os.IsNotExist(err) {
		t.Errorf("a decision was recorded for a store that was never fully read (stat err = %v)", err)
	}

	// With the obstruction gone the next open finishes the job, which is what
	// withholding the decision buys.
	chmodForTest(t, blocked, 0755)
	if _, err := New(); err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	if err := CheckComplete(reachable); err != nil {
		t.Errorf("reopening the store did not retry the migration: %v", err)
	}
}

// TestNewUndoesAMigrationItCannotFinish pins the rollback, which is what keeps
// the gate's premise true. The gate reads "no entry is marked" as "this store
// predates 2.0.0"; a half-marked store would answer that question wrongly on
// the next open, and every entry the pass had not reached would be refused
// forever. So a pass that cannot mark everything marks nothing.
func TestNewUndoesAMigrationItCannotFinish(t *testing.T) {
	requireUnreadableDirectories(t)

	root := legacyStoreRoot(t)
	writable := seedLegacyEntry(t, root, "aaa-first", "aaa111")
	unwritable := seedLegacyEntry(t, root, "zzz-last", "bbb222")
	chmodForTest(t, unwritable, 0555)

	if _, err := New(); err != nil {
		t.Fatalf("new store: %v", err)
	}

	if hasMarker(writable) {
		t.Errorf("%s kept a marker from a migration that could not finish; the next open would read this store as a 2.x one and refuse %s forever", writable, unwritable)
	}
	if _, err := os.Stat(filepath.Join(root, sentinelName)); !os.IsNotExist(err) {
		t.Errorf("a decision was recorded for a migration that failed partway (stat err = %v)", err)
	}
}

// requireUnreadableDirectories skips tests that inject a fault with a directory
// mode. Windows maps a mode like 0000 to the read-only file attribute, which
// still permits reading the directory and creating files inside it; denying
// that needs ACL editing. Root ignores the mode outright.
func requireUnreadableDirectories(t *testing.T) {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("Windows maps a directory mode to the read-only attribute, which still permits reading the directory and creating files in it; denying that needs ACL editing")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root: directory permissions do not deny access")
	}
}

// chmodForTest changes a directory's mode and restores it well enough for
// t.TempDir's cleanup to remove the tree.
func chmodForTest(t *testing.T, dir string, mode os.FileMode) {
	t.Helper()

	if err := os.Chmod(dir, mode); err != nil {
		t.Fatalf("chmod %s: %v", dir, err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0755) })
}

// assertSameEntries compares two entry-path lists regardless of order, which is
// filesystem-dependent.
func assertSameEntries(t *testing.T, got, want []string) {
	t.Helper()

	seen := make(map[string]bool, len(got))
	for _, g := range got {
		seen[g] = true
	}
	for _, w := range want {
		if !seen[w] {
			t.Errorf("entry %s missing from %v", w, got)
		}
		delete(seen, w)
	}
	for extra := range seen {
		t.Errorf("unexpected entry %s in %v", extra, got)
	}
}
