package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// markerName is the file a complete store entry carries. It is written as the
// last file inside the temporary directory, so it commits together with the
// content, and it is removed before the tree when an entry is deleted.
const markerName = ".lnpm-complete"

// markerSchemaVersion identifies the marker payload's shape, and with it the
// hash format the entry is addressed by.
//
//	1 — xxhash64 over unframed per-file fields, with the root package.json
//	    rewritten after the hash was taken. Written by lnpm 3.x and earlier.
//	2 — the fields length-prefixed (#453) and every manifest rewrite moved in
//	    front of the hash (#447). Written by 4.0.0 and later.
//
// An entry carrying an older version is refused rather than read: see
// checkEntry, and TestCheckCompleteRefusesAnOlderMarkerSchema for why the hash
// change alone does not retire one.
const markerSchemaVersion = 2

// marker is what a completeness marker holds: the content hash the entry is
// addressed by, and a schema version. CheckComplete reads the hash back and
// compares it against the directory the marker sits in, and refuses a schema
// version it does not write - which is what the field was recorded for.
type marker struct {
	SchemaVersion int    `json:"schemaVersion"`
	Hash          string `json:"hash"`
}

// IncompleteEntryError is what a caller gets for a store entry lnpm cannot
// vouch for. It is a distinct type so a caller can tell it from any other read
// failure and say what the user has to do, which differs by Present below.
//
// The message is deliberately terse, in this package's error voice: it states
// the fault and stops. Remediation is the CLI's, at the layer that knows which
// command the user ran and can afford the words.
type IncompleteEntryError struct {
	// Name is the package the entry belongs to, empty when the entry was
	// checked by path alone and the name was not passed in.
	Name string
	// Path is the entry's directory in the store.
	Path string
	// Reason says what about the entry failed the check.
	Reason string
	// Present reports whether the entry directory is there at all.
	//
	// It decides what the user has to do, so it is not cosmetic. Store never
	// renames over an occupied destination, so re-publishing byte-identical
	// content over a directory that is still there fails on the rename with
	// "file exists" — TestStoreRefusesToOverwriteAnIncompleteEntry pins that —
	// and the user has to remove it first. A directory that is simply gone
	// needs nothing but the re-publish.
	Present bool
}

func (e *IncompleteEntryError) Error() string {
	subject := "store entry"
	if e.Name != "" {
		subject = "store entry for " + e.Name
	}
	return fmt.Sprintf("%s at %s is incomplete: %s", subject, e.Path, e.Reason)
}

// writeMarker writes the completeness marker into dir.
func writeMarker(dir, hash string) error {
	payload, err := json.Marshal(marker{SchemaVersion: markerSchemaVersion, Hash: hash})
	if err != nil {
		return fmt.Errorf("failed to encode completeness marker: %w", err)
	}
	payload = append(payload, '\n')
	if err := os.WriteFile(filepath.Join(dir, markerName), payload, 0644); err != nil {
		return fmt.Errorf("failed to write completeness marker: %w", err)
	}
	return nil
}

// RemoveEntry deletes the store entry at entryPath, removing its completeness
// marker before the tree.
//
// The order is load-bearing. Removing the tree first means a removal
// interrupted partway — Ctrl-C, a permission error, a full disk — leaves a
// gutted directory that still carries its marker and still reads as a
// complete package. Removing the marker first makes any interrupted removal
// read as an absent entry, which is the truth.
func RemoveEntry(entryPath string) error {
	if err := os.Remove(filepath.Join(entryPath, markerName)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove completeness marker: %w", err)
	}
	return os.RemoveAll(entryPath)
}

// hasMarker reports whether an entry carries a completeness marker file at all,
// without reading it.
//
// This is the weaker of the two questions and it has exactly one caller: the
// legacy backfill's gate, which asks "has this store ever been marked". For
// that, the file's presence is the whole answer — a marker that is corrupt or
// names the wrong hash still proves some version of lnpm wrote one here. Every
// read of an entry goes through CheckComplete instead.
func hasMarker(entryPath string) bool {
	_, err := os.Stat(filepath.Join(entryPath, markerName))
	return err == nil
}

// CheckComplete reports whether the store entry at entryPath is one the write
// path committed, returning an *IncompleteEntryError describing the fault when
// it is not.
//
// Three things make an entry fail. The entry directory is not there at all. Or
// it carries no readable completeness marker, which is what an interrupted
// deletion leaves: RemoveEntry unlinks the marker before the tree, so a removal
// that dies partway leaves content behind that nothing vouches for. Or its
// marker records a hash other than the directory it sits in, which is what an
// entry copied or moved between hash directories looks like.
//
// The hash comparison is nearly circular — the directory name *is* the hash —
// and it is worth being exact about the two things it does buy. It catches an
// entry whose marker does not belong to it, and it means a marker derived from
// a directory name is no longer self-evidently valid, which is what let a
// backfill of every unmarked entry launder a gutted one into a complete one. It
// is not content verification: nothing here reads a single byte of the package,
// so an entry whose files were edited in place passes. What stops that edit is
// #333's write protection, one layer down - a consumer's hard link into the
// entry is read-only, so the write fails rather than landing. Re-hashing store
// content, which is what would catch an entry edited before that protection
// existed, is #439.
func CheckComplete(entryPath string) error {
	return checkEntry(entryPath, "")
}

// checkEntry is CheckComplete with the package name to put in the error, which
// only a caller addressing the entry by name and hash knows.
func checkEntry(entryPath, name string) error {
	present := true
	fail := func(reason string) error {
		return &IncompleteEntryError{Name: name, Path: entryPath, Reason: reason, Present: present}
	}

	// Whether the directory is there at all is asked separately, and not folded
	// into the marker read below, because it is the difference between "this
	// package was collected or never stored" and "something damaged this entry".
	// They read the same through a missing marker and need different advice.
	if _, err := os.Stat(entryPath); err != nil && os.IsNotExist(err) {
		present = false
		return fail("the entry directory is not there")
	}

	payload, err := os.ReadFile(filepath.Join(entryPath, markerName))
	if err != nil {
		if os.IsNotExist(err) {
			return fail("it carries no completeness marker")
		}
		// Fail closed. A marker that cannot be read is a marker whose contents
		// cannot be checked, and serving the entry anyway would make an
		// unreadable marker weaker protection than an absent one.
		return fail(fmt.Sprintf("its completeness marker could not be read (%v)", err))
	}

	var m marker
	if err := json.Unmarshal(payload, &m); err != nil {
		return fail(fmt.Sprintf("its completeness marker could not be decoded (%v)", err))
	}
	// Before the hash comparison, because for an entry written by 3.x the hash
	// agrees with its directory perfectly well - it is the format both are in
	// that this build cannot use, and saying "the marker records a hash which is
	// not the directory it sits in" about a sound 3.x entry would send the user
	// looking for damage that is not there.
	if m.SchemaVersion < markerSchemaVersion {
		return fail(fmt.Sprintf("it was written by an older lnpm (marker schema %d; this build writes %d), whose package hashes this build does not use: re-publish the package", m.SchemaVersion, markerSchemaVersion))
	}
	if m.Hash != filepath.Base(entryPath) {
		return fail(fmt.Sprintf("its completeness marker records the hash %q, which is not the directory it sits in", m.Hash))
	}
	return nil
}
