package store

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/pack"
)

// seedLegacyEntry creates a store entry the way a version of lnpm without
// completeness markers left it: a directory of files and nothing else.
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

// TestNewBackfillsPreExistingEntries covers the upgrade: entries written
// before markers existed must not read as missing, or the whole store would be
// re-published. They are marked once, at store initialization.
func TestNewBackfillsPreExistingEntries(t *testing.T) {
	root := legacyStoreRoot(t)
	seedLegacyEntry(t, root, "left-pad", "aaa111")
	seedLegacyEntry(t, root, "@scope/ui", "bbb222")

	s, err := New()
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	if !s.Exists("left-pad", "aaa111") {
		t.Error("pre-existing entry left-pad/aaa111 reads as missing after the backfill")
	}
	if !s.Exists("@scope/ui", "bbb222") {
		t.Error("pre-existing scoped entry @scope/ui/bbb222 reads as missing after the backfill")
	}
	if _, err := os.Stat(filepath.Join(root, sentinelName)); err != nil {
		t.Errorf("backfill did not record its sentinel: %v", err)
	}
}

// TestBackfilledEntryIsNotRepublished is the same criterion seen from the
// store's write path: a backfilled entry short-circuits Store, so its content
// is left exactly as it was rather than rebuilt from the publisher's files.
func TestBackfilledEntryIsNotRepublished(t *testing.T) {
	root := legacyStoreRoot(t)
	seedLegacyEntry(t, root, "left-pad", "aaa111")

	s, err := New()
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	src, files := makeSource(t, 2)
	if _, err := s.Store("left-pad", "aaa111", files, src); err != nil {
		t.Fatalf("store: %v", err)
	}

	got, err := s.GetFiles("left-pad", "aaa111")
	if err != nil {
		t.Fatalf("get files: %v", err)
	}
	if len(got) != 1 || got[0].RelPath != "legacy.js" {
		t.Errorf("backfilled entry was re-published: files are %v, want the original legacy.js", relPaths(got))
	}
}

// TestBackfillRunsOnlyOnce pins the sentinel as the guard: once it is written,
// strict checking applies to everything, so a directory that appears later
// without a marker is an interrupted deletion and must not be blessed.
func TestBackfillRunsOnlyOnce(t *testing.T) {
	root := legacyStoreRoot(t)
	seedLegacyEntry(t, root, "left-pad", "aaa111")

	if _, err := New(); err != nil {
		t.Fatalf("new store: %v", err)
	}

	// A partially deleted entry appearing after the backfill has committed.
	seedLegacyEntry(t, root, "half-gone", "ccc333")

	s, err := New()
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	if s.Exists("half-gone", "ccc333") {
		t.Error("the backfill ran a second time and marked a marker-less entry as complete")
	}
	if !s.Exists("left-pad", "aaa111") {
		t.Error("the first backfill's work was lost")
	}
}

// TestBackfillSkipsEntriesItCannotMark pins that one bad directory cannot take
// the whole store down with it. Opening the store is on the path of every
// command, so a backfill that gave up on the first unwritable entry would
// leave the user with no working command and no way out. The pass marks what
// it can reach, withholds the sentinel, and the next open retries the rest.
func TestBackfillSkipsEntriesItCannotMark(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping on Windows - the unmarkable entry is injected with a read-only directory mode, which maps to the read-only file attribute there and still permits creating the marker file inside it; denying that needs ACL editing. TestBackfillDoesNotFailWhenItCannotScan covers the non-fatal half on every platform, the withhold-and-retry half stays unverified on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root: directory permissions do not deny writing")
	}
	root := legacyStoreRoot(t)
	good := seedLegacyEntry(t, root, "good-pkg", "aaa111")
	bad := seedLegacyEntry(t, root, "bad-pkg", "bbb222")
	if err := os.Chmod(bad, 0555); err != nil {
		t.Fatalf("chmod entry: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(bad, 0755) })

	s, err := New()
	if err != nil {
		t.Fatalf("opening the store failed because one entry could not be marked: %v", err)
	}
	if !s.Exists("good-pkg", "aaa111") {
		t.Errorf("the entry the backfill could reach (%s) was left unmarked", good)
	}
	if _, err := os.Stat(filepath.Join(root, sentinelName)); !os.IsNotExist(err) {
		t.Errorf("the sentinel was committed even though an entry was skipped (stat err = %v)", err)
	}

	// With the obstruction gone, the next open finishes the job.
	if err := os.Chmod(bad, 0755); err != nil {
		t.Fatalf("chmod entry back: %v", err)
	}
	s2, err := New()
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	if !s2.Exists("bad-pkg", "bbb222") {
		t.Errorf("reopening the store did not retry the skipped entry %s", bad)
	}
	if _, err := os.Stat(filepath.Join(root, sentinelName)); err != nil {
		t.Errorf("the sentinel was not committed once every entry could be marked: %v", err)
	}
}

// TestBackfillDoesNotFailWhenItCannotScan is the part of the same guarantee
// that can be checked on every platform: a store the pass cannot read leaves
// the backfill pending instead of failing the store open, and no sentinel is
// left claiming a scan that never happened. The store root is missing here
// because that is the one scan failure every platform can be made to produce;
// on a real store the cause would be a permission or IO error.
//
// It does not cover the other half — that a pass which marked some entries
// still withholds the sentinel — because that needs an entry which can be read
// but not written, and the only injection for that is a directory mode, which
// Windows ignores. TestBackfillSkipsEntriesItCannotMark covers it everywhere
// else.
func TestBackfillDoesNotFailWhenItCannotScan(t *testing.T) {
	root := filepath.Join(t.TempDir(), "not-created")

	if err := backfillMarkers(root); err != nil {
		t.Errorf("a store that cannot be scanned failed the backfill instead of leaving it pending: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, sentinelName)); !os.IsNotExist(err) {
		t.Errorf("the sentinel was committed for a store the backfill never scanned (stat err = %v)", err)
	}
}

// TestBackfillIsIdempotent runs the backfill again over a store it has already
// processed, as a crashed first run followed by a retry would.
func TestBackfillIsIdempotent(t *testing.T) {
	root := legacyStoreRoot(t)
	entry := seedLegacyEntry(t, root, "left-pad", "aaa111")

	for i := 0; i < 2; i++ {
		if err := backfillMarkers(root); err != nil {
			t.Fatalf("backfill run %d: %v", i+1, err)
		}
		if err := os.Remove(filepath.Join(root, sentinelName)); err != nil {
			t.Fatalf("clear sentinel after run %d: %v", i+1, err)
		}
	}

	if !hasMarker(entry) {
		t.Error("entry lost its marker across repeated backfills")
	}
}

func relPaths(files []*pack.FileInfo) []string {
	var out []string
	for _, f := range files {
		out = append(out, f.RelPath)
	}
	return out
}
