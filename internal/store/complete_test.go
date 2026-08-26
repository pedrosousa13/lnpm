package store

import (
	"encoding/json"
	"errors"
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
	for _, want := range []string{"gutted-pkg", entry} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("GetFiles error does not name %q, so a reader cannot tell which entry is broken: %v", want, err)
		}
	}
	// The remediation is not asserted here: the store states the fault and
	// stops, and what the user has to do is added by the CLI, which knows the
	// command and the version. TestAddRefusesGuttedStoreEntry covers that half.
	var incomplete *IncompleteEntryError
	if !errors.As(err, &incomplete) {
		t.Fatalf("GetFiles returned %T, want an *IncompleteEntryError the CLI can branch on", err)
	}
	if !incomplete.Present {
		t.Error("the gutted entry was reported as absent; its directory is still there, and that is what decides whether a re-publish needs it removed first")
	}
}

// TestGetFilesReportsAnAbsentEntryAsAbsent separates the two faults that used
// to read alike. A collected entry needs a plain re-publish; a gutted one has
// to be removed first, because Store will not rename over it. The CLI branches
// on Present to say which.
func TestGetFilesReportsAnAbsentEntryAsAbsent(t *testing.T) {
	s := newTestStore(t)

	_, err := s.GetFiles("never-published", "abc123")

	var incomplete *IncompleteEntryError
	if !errors.As(err, &incomplete) {
		t.Fatalf("GetFiles returned %T for an entry that was never stored, want an *IncompleteEntryError", err)
	}
	if incomplete.Present {
		t.Errorf("an entry directory that does not exist was reported as present: %v", err)
	}
}

// TestGetFilesRefusesEntryWhoseMarkerNamesAnotherHash covers the marker's
// recorded hash being read rather than only written. The entry here is intact
// and marked, but the marker was minted for a different hash, which is what an
// entry copied or moved into another hash's directory looks like.
//
// This is not content verification - re-hashing the entry is #439's job - so
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
// strict without fixing it.
//
// The store here holds a committed entry beside the gutted one, which is what
// the reproduction's store looked like and what makes the fault reachable at
// all: the marker's whole purpose is to distinguish those two, so an entry can
// only be gutted in a store that has markers. That is also exactly the gate the
// legacy backfill in New uses — a store with any marker is a 2.x store and
// nothing in it is minted — so this and TestNewMarksAStoreThatNeverHadMarkers
// pin the two sides of the same decision.
func TestNewDoesNotMarkAnEntryItCannotVerify(t *testing.T) {
	root := legacyStoreRoot(t)
	entry := seedLegacyEntry(t, root, "gutted", "dead")
	if err := writeMarker(seedLegacyEntry(t, root, "healthy", "beef"), "beef"); err != nil {
		t.Fatalf("mark the committed entry: %v", err)
	}

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
	if _, err := s.GetFiles("healthy", "beef"); err != nil {
		t.Errorf("the committed entry beside it stopped reading as complete: %v", err)
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
	// And says what to do about it. The bare rename error names the path too,
	// so a test that stopped above would pass on "rename …: file exists" — which
	// is what push printed before, and what nobody can act on.
	if !strings.Contains(err.Error(), "delete it and publish again") {
		t.Errorf("the failure does not say the directory has to be removed, which is the only way out of this state: %v", err)
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
