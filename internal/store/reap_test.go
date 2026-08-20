package store

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// newReapStore points the store at a throwaway directory and returns it.
func newReapStore(t *testing.T) *Store {
	t.Helper()
	t.Setenv("LNPM_STORE", t.TempDir())
	s, err := New()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return s
}

// seedStoreDir creates dir with one file in it.
func seedStoreDir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("seed %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.js"), []byte("payload"), 0644); err != nil {
		t.Fatalf("seed %s: %v", dir, err)
	}
}

func tempDirPaths(dirs []TempDir) []string {
	paths := make([]string, 0, len(dirs))
	for _, d := range dirs {
		paths = append(paths, d.Path)
	}
	sort.Strings(paths)
	return paths
}

// TestFindTempDirsFindsAnInterruptedPublish covers the store's own leak: Store
// writes into a temp directory beside the target entry and renames it into
// place, and the deferred cleanup that removes it only runs on a normal return.
func TestFindTempDirsFindsAnInterruptedPublish(t *testing.T) {
	s := newReapStore(t)
	orphan := filepath.Join(s.Root(), "my-pkg", ".0123456789abcdef.tmp-987654321")
	seedStoreDir(t, orphan)

	dirs, unreadable := s.FindTempDirs()
	if unreadable != 0 {
		t.Fatalf("FindTempDirs() could not read %d director(ies)", unreadable)
	}
	if len(dirs) != 1 || dirs[0].Path != orphan {
		t.Fatalf("FindTempDirs() found %v, want just %s", tempDirPaths(dirs), orphan)
	}
	if dirs[0].Size == 0 {
		t.Errorf("FindTempDirs() reported size 0 for a directory holding a file")
	}
}

// TestFindTempDirsFindsScopedTempDirs pins that a scoped package's temp
// directory, which sits one level deeper, is not missed.
func TestFindTempDirsFindsScopedTempDirs(t *testing.T) {
	s := newReapStore(t)
	orphan := filepath.Join(s.Root(), "@org", "pkg", ".fedcba9876543210.tmp-42")
	seedStoreDir(t, orphan)

	dirs, unreadable := s.FindTempDirs()
	if unreadable != 0 {
		t.Fatalf("FindTempDirs() could not read %d director(ies)", unreadable)
	}
	if len(dirs) != 1 || dirs[0].Path != orphan {
		t.Fatalf("FindTempDirs() found %v, want just %s", tempDirPaths(dirs), orphan)
	}
}

// TestFindTempDirsLeavesStoreEntriesAlone is the negative case. Store entries,
// the completeness marker and the backfill sentinel all live in the same tree,
// and a sweep that matches any dot-prefixed entry would take the store's own
// bookkeeping with it.
func TestFindTempDirsLeavesStoreEntriesAlone(t *testing.T) {
	s := newReapStore(t)

	keep := []string{
		filepath.Join(s.Root(), "my-pkg", "0123456789abcdef"),
		filepath.Join(s.Root(), "@org", "pkg", "fedcba9876543210"),
		// A package whose name legitimately begins with a dot.
		filepath.Join(s.Root(), ".hidden-pkg", "0011223344556677"),
		// Close to the temp shape but not it: a non-numeric random tail, and a
		// hash portion that is not hex.
		filepath.Join(s.Root(), "my-pkg", ".0123456789abcdef.tmp-notdigits"),
		filepath.Join(s.Root(), "my-pkg", ".nothex.tmp-1234"),
		filepath.Join(s.Root(), "my-pkg", ".tmp-1234"),
	}
	for _, dir := range keep {
		seedStoreDir(t, dir)
	}
	orphan := filepath.Join(s.Root(), "my-pkg", ".aabbccddeeff0011.tmp-7")
	seedStoreDir(t, orphan)

	dirs, unreadable := s.FindTempDirs()
	if unreadable != 0 {
		t.Fatalf("FindTempDirs() could not read %d director(ies)", unreadable)
	}
	if len(dirs) != 1 || dirs[0].Path != orphan {
		t.Fatalf("FindTempDirs() found %v, want just %s", tempDirPaths(dirs), orphan)
	}
	for _, dir := range keep {
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("%s no longer stats: %v", dir, err)
		}
	}
}

// TestFindTempDirsMatchesWhatTheWritePathProduces ties the matcher to the name
// the write path actually creates, by calling the very function Store calls
// rather than re-deriving the name from the same constant the matcher reads.
// Re-deriving it would let the write path change its literal and the sweep go
// quietly blind with every test still green.
func TestFindTempDirsMatchesWhatTheWritePathProduces(t *testing.T) {
	s := newReapStore(t)
	const hash = "0123456789abcdef"
	parent := filepath.Join(s.Root(), "my-pkg")
	if err := os.MkdirAll(parent, 0755); err != nil {
		t.Fatalf("create package dir: %v", err)
	}
	made, err := newTempDir(parent, hash)
	if err != nil {
		t.Fatalf("newTempDir() error = %v", err)
	}

	dirs, unreadable := s.FindTempDirs()
	if unreadable != 0 {
		t.Fatalf("FindTempDirs() could not read %d director(ies)", unreadable)
	}
	if len(dirs) != 1 || dirs[0].Path != made {
		t.Fatalf("FindTempDirs() found %v, want just %s", tempDirPaths(dirs), made)
	}
}

// TestFindTempDirsOnAnEmptyStore pins that a store with nothing in it is not an
// error: gc runs against fresh stores too.
func TestFindTempDirsOnAnEmptyStore(t *testing.T) {
	s := newReapStore(t)
	dirs, unreadable := s.FindTempDirs()
	if unreadable != 0 {
		t.Fatalf("FindTempDirs() could not read %d director(ies)", unreadable)
	}
	if len(dirs) != 0 {
		t.Errorf("FindTempDirs() found %v in an empty store", tempDirPaths(dirs))
	}
}
