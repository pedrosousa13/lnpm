package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// markerName is the file a complete store entry carries. It is written as the
// last file inside the temporary directory, so it commits together with the
// content, and it is removed before the tree when an entry is deleted. Its
// presence is the whole completeness test: nothing reads its contents.
const markerName = ".lnpm-complete"

// markerSchemaVersion identifies the marker payload's shape.
const markerSchemaVersion = 1

// marker is what a completeness marker holds: the content hash the entry is
// addressed by, and a schema version. Both are for debuggability and for a
// future validating check; the completeness decision reads neither.
type marker struct {
	SchemaVersion int    `json:"schemaVersion"`
	Hash          string `json:"hash"`
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

// hasMarker reports whether the entry directory carries its completeness
// marker. This sits on a hot path, so it is a single stat and never reads the
// marker's contents.
func hasMarker(entryPath string) bool {
	_, err := os.Stat(filepath.Join(entryPath, markerName))
	return err == nil
}
