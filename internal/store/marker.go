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

// markerSchemaVersion identifies the marker payload's shape.
const markerSchemaVersion = 1

// marker is what a completeness marker holds: the content hash the entry is
// addressed by, and a schema version. CheckComplete reads the hash back and
// compares it against the directory the marker sits in; the schema version is
// recorded for a future reader that has to tell payload shapes apart, and
// nothing decides anything on it yet.
type marker struct {
	SchemaVersion int    `json:"schemaVersion"`
	Hash          string `json:"hash"`
}

// IncompleteEntryError is what a caller gets for a store entry lnpm cannot
// vouch for. It is a distinct type so a caller can tell "this package was
// never published" from "this package's store entry is damaged", which need
// different things from the user.
type IncompleteEntryError struct {
	// Name is the package the entry belongs to, empty when the entry was
	// checked by path alone and the name was not passed in.
	Name string
	// Path is the entry's directory in the store.
	Path string
	// Reason says what about the entry failed the check.
	Reason string
}

func (e *IncompleteEntryError) Error() string {
	subject := "store entry"
	if e.Name != "" {
		subject = "store entry for " + e.Name
	}
	// The closing clause is not decoration. Store never deletes an entry that
	// is in its way, so re-publishing byte-identical content over a damaged
	// entry fails on the rename with "file exists" — verified by
	// TestStoreRefusesToOverwriteAnIncompleteEntry. Advice that stopped at
	// "re-publish" would be wrong for exactly the case a user hits first.
	return fmt.Sprintf("%s at %s is incomplete: %s. An interrupted 'lnpm gc' or publish leaves an entry in this state; "+
		"lnpm cannot tell what is missing from it, so it will neither use it nor delete it. Re-publish the package to rebuild the entry, "+
		"deleting that directory yourself first if the publish reports it is already there",
		subject, e.Path, e.Reason)
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

// CheckComplete reports whether the store entry at entryPath is one the write
// path committed, returning an *IncompleteEntryError describing the fault when
// it is not.
//
// Two things make an entry fail. It carries no readable completeness marker,
// which is what an interrupted deletion leaves: RemoveEntry unlinks the marker
// before the tree, so a removal that dies partway leaves content behind that
// nothing vouches for. Or its marker records a hash other than the directory
// it sits in, which is what an entry copied or moved between hash directories
// looks like.
//
// The hash comparison is nearly circular — the directory name *is* the hash —
// and it is worth being exact about the two things it does buy. It catches an
// entry whose marker does not belong to it, and it means a marker derived from
// a directory name is no longer self-evidently valid, which is what let the old
// backfill launder a gutted entry into a complete one. It is **not** content
// verification: nothing here reads a single byte of the package, so an entry
// whose files were edited in place passes. Re-hashing store content is #333.
func CheckComplete(entryPath string) error {
	return checkEntry(entryPath, "")
}

// checkEntry is CheckComplete with the package name to put in the error, which
// only a caller addressing the entry by name and hash knows.
func checkEntry(entryPath, name string) error {
	fail := func(reason string) error {
		return &IncompleteEntryError{Name: name, Path: entryPath, Reason: reason}
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
	if m.Hash != filepath.Base(entryPath) {
		return fail(fmt.Sprintf("its completeness marker records the hash %q, which is not the directory it sits in", m.Hash))
	}
	return nil
}
