package store

import (
	"os"
	"path/filepath"
	"testing"
)

// newTestStore returns a Store rooted in a fresh temp directory.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	t.Setenv("LNPM_STORE", t.TempDir())
	s, err := New()
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	return s
}

// TestExistsRejectsEntryWithoutMarker simulates a deletion that was
// interrupted after the completeness marker was removed but before the tree
// was: the directory and its files are still there, so a bare directory stat
// reports the package as present. Only the marker distinguishes it from a
// committed entry.
func TestExistsRejectsEntryWithoutMarker(t *testing.T) {
	s := newTestStore(t)

	entry := s.PackagePath("partial-pkg", "abc123")
	if err := os.MkdirAll(entry, 0755); err != nil {
		t.Fatalf("create entry: %v", err)
	}
	if err := os.WriteFile(filepath.Join(entry, "package.json"), []byte(`{"name":"partial-pkg"}`), 0644); err != nil {
		t.Fatalf("write content: %v", err)
	}

	if s.Exists("partial-pkg", "abc123") {
		t.Errorf("Exists reported a marker-less entry at %s as present; a partially deleted entry must read as absent", entry)
	}
}

// TestStoreWritesMarkerOnCommit pins that a committed entry carries the
// marker, so the stricter Exists does not report freshly stored packages as
// missing.
func TestStoreWritesMarkerOnCommit(t *testing.T) {
	s := newTestStore(t)

	src, files := makeSource(t, 2)
	dest, err := s.Store("marked-pkg", "deadbeef", files, src)
	if err != nil {
		t.Fatalf("store: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dest, markerName)); err != nil {
		t.Errorf("committed entry has no marker: %v", err)
	}
	if !s.Exists("marked-pkg", "deadbeef") {
		t.Errorf("Exists reported the freshly stored entry %s as absent", dest)
	}
}

// TestRemoveEntryDeletesTheWholeEntry is the happy path of a gc removal: the
// entry is gone and the caller is told it succeeded.
func TestRemoveEntryDeletesTheWholeEntry(t *testing.T) {
	s := newTestStore(t)

	src, files := makeSource(t, 2)
	entry, err := s.Store("doomed-pkg", "b0b0", files, src)
	if err != nil {
		t.Fatalf("store: %v", err)
	}

	if err := RemoveEntry(entry); err != nil {
		t.Fatalf("remove entry: %v", err)
	}
	if _, err := os.Stat(entry); !os.IsNotExist(err) {
		t.Errorf("entry %s still present after removal (stat err = %v)", entry, err)
	}
	if s.Exists("doomed-pkg", "b0b0") {
		t.Error("Exists still reports the removed entry as present")
	}
}

// blockMarkerRemoval makes an entry's completeness marker impossible to
// unlink, so that a removal which starts with the marker cannot get past it
// and one that starts with the tree can be caught destroying content first.
//
// The obstruction is a non-empty directory standing in the marker's place:
// os.Remove refuses to delete one on every platform lnpm builds for (ENOTEMPTY
// on unix, ERROR_DIR_NOT_EMPTY on Windows, since os.Remove there tries
// DeleteFile and then RemoveDirectory), while os.RemoveAll deletes it without
// complaint. Why the unlink fails is immaterial to what is being observed —
// permissions, an open handle on Windows and a full disk all reach RemoveEntry
// as the same error — and this obstruction is the one that behaves identically
// everywhere, including as root and on Windows, where denying removal through
// a directory mode does not work.
func blockMarkerRemoval(t *testing.T, entryPath string) {
	t.Helper()
	occupant := filepath.Join(entryPath, markerName, "occupied")
	if err := os.MkdirAll(filepath.Dir(occupant), 0755); err != nil {
		t.Fatalf("block marker removal: %v", err)
	}
	if err := os.WriteFile(occupant, []byte("x"), 0644); err != nil {
		t.Fatalf("block marker removal: %v", err)
	}
}

// TestRemoveEntryRemovesMarkerBeforeTree is the ordering criterion, and it is
// the one worth testing carefully: with the tree removed first, an
// interruption partway through leaves the marker on a gutted entry and it
// still reads as complete, which makes the marker decorative.
//
// The order is observed by making the marker's removal fail. A removal that
// starts with the marker gives up there and touches nothing, so the content
// file survives. A removal that starts with the tree deletes that file — and,
// RemoveAll being recursive, the obstructed marker along with it — before it
// reports anything, so the surviving file is what separates the two orders.
func TestRemoveEntryRemovesMarkerBeforeTree(t *testing.T) {
	s := newTestStore(t)

	entry := s.PackagePath("stuck-pkg", "f00d")
	nested := filepath.Join(entry, "dist")
	if err := os.MkdirAll(nested, 0755); err != nil {
		t.Fatalf("create entry: %v", err)
	}
	content := filepath.Join(nested, "index.js")
	if err := os.WriteFile(content, []byte("payload"), 0644); err != nil {
		t.Fatalf("write content: %v", err)
	}
	blockMarkerRemoval(t, entry)

	err := RemoveEntry(entry)

	if _, statErr := os.Stat(content); statErr != nil {
		t.Errorf("RemoveEntry deleted %s before the marker was removed, so an interrupted removal can leave a marked but gutted entry (stat err = %v, RemoveEntry err = %v)", content, statErr, err)
	}
	if err == nil {
		t.Error("RemoveEntry reported success even though the marker could not be removed")
	}
	if !s.Exists("stuck-pkg", "f00d") {
		t.Error("the entry stopped reading as complete even though its removal failed")
	}
}

// TestGetFilesExcludesMarker pins that the marker stays inside the store.
// GetFiles enumerates what gets linked into a consumer project, so a marker it
// returned would be installed inside every linked package.
func TestGetFilesExcludesMarker(t *testing.T) {
	s := newTestStore(t)

	src, files := makeSource(t, 2)
	if _, err := s.Store("linked-pkg", "c0ffee", files, src); err != nil {
		t.Fatalf("store: %v", err)
	}

	got, err := s.GetFiles("linked-pkg", "c0ffee")
	if err != nil {
		t.Fatalf("get files: %v", err)
	}
	for _, f := range got {
		if f.RelPath == markerName {
			t.Errorf("GetFiles returned the completeness marker %q; it would be copied into consumer projects", f.RelPath)
		}
	}
	if len(got) != len(files) {
		t.Errorf("GetFiles returned %d files, want the %d stored files", len(got), len(files))
	}
}
