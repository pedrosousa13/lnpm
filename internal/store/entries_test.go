package store

import (
	"os"
	"path/filepath"
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

// TestNewDoesNotFailOnAStoreItCannotScan pins that opening the store stays
// cheap and total. Every command opens it, so a store holding a directory that
// cannot be read must not brick them all — the scan that has an opinion about
// such entries is doctor's, and it runs on request.
func TestNewDoesNotFailOnAStoreItCannotScan(t *testing.T) {
	root := legacyStoreRoot(t)
	seedLegacyEntry(t, root, "left-pad", "aaa111")

	if _, err := New(); err != nil {
		t.Errorf("opening a store holding an unmarked entry failed: %v", err)
	}
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
