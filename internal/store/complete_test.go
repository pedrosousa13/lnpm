package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// gutEntry reproduces what an interrupted `lnpm gc` leaves behind: the
// completeness marker is unlinked first, and the RemoveAll that should follow
// it fails partway, so some of the content survives. RemoveEntry writes exactly
// this shape when its second step fails, which is why the marker is removed
// here rather than never written.
func gutEntry(t *testing.T, entry string) {
	t.Helper()

	if err := os.Remove(filepath.Join(entry, markerName)); err != nil {
		t.Fatalf("gut entry: remove marker: %v", err)
	}

	// One content file goes, the rest stay: an entry emptied completely would
	// be caught by any existence check, and it is the half-removed one that
	// used to read as whole.
	removed := false
	err := filepath.Walk(entry, func(path string, info os.FileInfo, err error) error {
		switch {
		case err != nil:
			return err
		case removed || info.IsDir():
			return nil
		}
		if err := os.Remove(path); err != nil {
			return err
		}
		removed = true
		return nil
	})
	if err != nil {
		t.Fatalf("gut entry: %v", err)
	}
	if !removed {
		t.Fatalf("gut entry: %s holds no content file to remove", entry)
	}
}

// TestGetFilesRefusesGuttedEntry is the read-path half of the bug in #330: the
// completeness marker was consulted only when writing, so an entry an
// interrupted gc had gutted was enumerated and linked into a project as if it
// were whole.
func TestGetFilesRefusesGuttedEntry(t *testing.T) {
	s := newTestStore(t)

	src, files := makeSource(t, 3)
	entry, err := s.Store("gutted-pkg", "abc123", files, src)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	gutEntry(t, entry)

	got, err := s.GetFiles("gutted-pkg", "abc123")

	if err == nil {
		t.Fatalf("GetFiles returned %d files for the gutted entry %s instead of refusing it; those files get linked into a consumer project", len(got), entry)
	}
	for _, want := range []string{"gutted-pkg", entry, "gc", "publish"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("GetFiles error does not mention %q, so it does not tell the user what is broken or how it got that way: %v", want, err)
		}
	}
}

// TestGetFilesRefusesEntryWhoseMarkerNamesAnotherHash covers the marker's
// recorded hash being read rather than only written. The entry here is intact
// and marked, but the marker was minted for a different hash, which is what an
// entry copied or moved into another hash's directory looks like.
//
// This is not content verification - re-hashing the entry is #333's job - so
// the only thing it can catch is a marker that does not belong to the directory
// it sits in.
func TestGetFilesRefusesEntryWhoseMarkerNamesAnotherHash(t *testing.T) {
	s := newTestStore(t)

	src, files := makeSource(t, 2)
	entry, err := s.Store("moved-pkg", "abc123", files, src)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	if err := writeMarker(entry, "def456"); err != nil {
		t.Fatalf("rewrite marker: %v", err)
	}

	got, err := s.GetFiles("moved-pkg", "abc123")

	if err == nil {
		t.Fatalf("GetFiles returned %d files for %s, whose marker names hash def456 and not the directory it sits in", len(got), entry)
	}
}

// TestGetFilesServesAHealthyEntry is the other direction: the strict read must
// not refuse an entry the write path committed, or every add and pull would
// fail.
func TestGetFilesServesAHealthyEntry(t *testing.T) {
	s := newTestStore(t)

	src, files := makeSource(t, 3)
	if _, err := s.Store("healthy-pkg", "abc123", files, src); err != nil {
		t.Fatalf("store: %v", err)
	}

	got, err := s.GetFiles("healthy-pkg", "abc123")
	if err != nil {
		t.Fatalf("GetFiles refused a freshly stored entry: %v", err)
	}
	if len(got) != len(files) {
		t.Errorf("GetFiles returned %d files, want the %d stored files", len(got), len(files))
	}
}

// TestNewDoesNotMarkAnEntryItCannotVerify is the sweep's second reproduction
// from #330, which read:
//
//	after store.New(): marker present=true
//	s.Exists("gutted","dead") = true
//	marker contents: {"schemaVersion":1,"hash":"dead"}
//
// Opening the store used to write a marker into every unmarked entry, taking
// the hash from the directory name. That laundered a gutted entry into a
// permanently complete one, and it is why the read path could not be made
// strict without removing it first.
func TestNewDoesNotMarkAnEntryItCannotVerify(t *testing.T) {
	root := legacyStoreRoot(t)
	entry := seedLegacyEntry(t, root, "gutted", "dead")

	s, err := New()
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	if payload, err := os.ReadFile(filepath.Join(entry, markerName)); err == nil {
		t.Errorf("opening the store minted a completeness marker for %s that nothing verified: %s", entry, strings.TrimSpace(string(payload)))
	}
	if _, err := s.GetFiles("gutted", "dead"); err == nil {
		t.Errorf("the unmarked entry %s still reads as a complete package", entry)
	}
}

// TestStoreRefusesToOverwriteAnIncompleteEntry is what makes the refusal's
// closing advice true. Store never deletes a directory standing where it wants
// to commit — the rename is the only way an entry appears — so re-publishing
// byte-identical content over a gutted entry cannot succeed, and the user has
// to remove it themselves. An error naming the directory is the honest outcome;
// silently keeping the damaged copy and reporting success is not.
//
// Content that has changed hashes to a different directory and is unaffected,
// which is why the advice says "if the publish reports it is already there".
func TestStoreRefusesToOverwriteAnIncompleteEntry(t *testing.T) {
	s := newTestStore(t)

	src, files := makeSource(t, 3)
	entry, err := s.Store("stuck-pkg", "abc123", files, src)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	gutEntry(t, entry)

	_, err = s.Store("stuck-pkg", "abc123", files, src)

	if err == nil {
		t.Fatal("re-publishing over the gutted entry reported success; the entry it left behind is whatever survived the interrupted removal")
	}
	if !strings.Contains(err.Error(), entry) {
		t.Errorf("the failure does not name the directory in the way, so the user cannot act on it: %v", err)
	}
}

// TestMarkerRecordsTheEntrysOwnHash pins what the marker written on commit
// contains, since the read now compares that field against the directory name.
func TestMarkerRecordsTheEntrysOwnHash(t *testing.T) {
	s := newTestStore(t)

	src, files := makeSource(t, 2)
	entry, err := s.Store("marked-pkg", "abc123", files, src)
	if err != nil {
		t.Fatalf("store: %v", err)
	}

	payload, err := os.ReadFile(filepath.Join(entry, markerName))
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}
	var m marker
	if err := json.Unmarshal(payload, &m); err != nil {
		t.Fatalf("decode marker: %v", err)
	}
	if m.Hash != "abc123" {
		t.Errorf("marker records hash %q, want the entry's own abc123", m.Hash)
	}
	if m.SchemaVersion != markerSchemaVersion {
		t.Errorf("marker records schema version %d, want %d", m.SchemaVersion, markerSchemaVersion)
	}
}
