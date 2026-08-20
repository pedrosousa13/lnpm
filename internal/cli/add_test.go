package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/db"
	"github.com/pedrosousa13/lnpm/internal/store"
)

// seedStoreEntry writes a store entry holding one file and returns its path.
func seedStoreEntry(t *testing.T, storeRoot, name, hash, relPath, content string) string {
	t.Helper()

	entry := filepath.Join(storeRoot, name, hash)
	if err := os.MkdirAll(entry, 0755); err != nil {
		t.Fatalf("seed store entry: %v", err)
	}
	if err := os.WriteFile(filepath.Join(entry, relPath), []byte(content), 0644); err != nil {
		t.Fatalf("seed store file: %v", err)
	}
	return entry
}

// TestStoreFilesForLinkRefusesAManifestFromAnotherGeneration pins the one thing
// that makes a relink's file-level decisions safe: the hashes it stamps onto the
// store's files have to describe those files.
//
// The package row and its file rows are written in one transaction, so they can
// no longer name different generations - but a database written before that was
// true can still hold the mismatch, and stamping a superseded generation's
// hashes onto the current one would mark a genuinely changed file reusable and
// carry stale content into the consumer's project. Recomputing the package
// content hash from the file rows costs no I/O and catches exactly that, and
// falling back to hashless files costs one full relink.
func TestStoreFilesForLinkRefusesAManifestFromAnotherGeneration(t *testing.T) {
	storeRoot, database := newGCStore(t)

	entry := seedStoreEntry(t, storeRoot, "drifted-pkg", "2222222222222222", "index.js", "module.exports = 'v2';")

	// The file rows describe the previous generation: the same path, the hash
	// it used to hold.
	stale := []*db.FileEntry{{RelativePath: "index.js", ContentHash: "aaaaaaaaaaaaaaaa", Size: 3, Mode: 0644}}

	pkg := &db.Package{
		Name:        "drifted-pkg",
		Version:     "2.0.0",
		ContentHash: "2222222222222222",
		StorePath:   entry,
	}
	if err := database.InsertPackage(pkg); err != nil {
		t.Fatalf("insert package: %v", err)
	}
	if err := database.InsertFiles(pkg.ID, stale); err != nil {
		t.Fatalf("insert files: %v", err)
	}

	s, err := store.New()
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	files, err := storeFilesForLink(database, s, pkg)
	if err != nil {
		t.Fatalf("storeFilesForLink() error = %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("storeFilesForLink() returned %d files, want 1", len(files))
	}
	if files[0].ContentHash != "" {
		t.Errorf("storeFilesForLink() stamped %q onto index.js from a file manifest that describes another generation; a relink would then treat a changed file as reusable", files[0].ContentHash)
	}
}

// TestStoreFilesForLinkStampsAManifestThatMatches is the other half: a file
// manifest that does describe the package row's generation must still be used,
// or every relink falls back to rewriting the whole package and the incremental
// path never runs.
func TestStoreFilesForLinkStampsAManifestThatMatches(t *testing.T) {
	storeRoot, database := newGCStore(t)

	entries := []*db.FileEntry{{RelativePath: "index.js", ContentHash: "aaaaaaaaaaaaaaaa", Size: 3, Mode: 0644}}
	hash := fileManifestHash(entries)

	entry := seedStoreEntry(t, storeRoot, "matched-pkg", hash, "index.js", "module.exports = 'v1';")

	pkg := &db.Package{
		Name:        "matched-pkg",
		Version:     "1.0.0",
		ContentHash: hash,
		StorePath:   entry,
	}
	if err := database.InsertPackageWithFiles(pkg, entries); err != nil {
		t.Fatalf("insert package with files: %v", err)
	}

	s, err := store.New()
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	files, err := storeFilesForLink(database, s, pkg)
	if err != nil {
		t.Fatalf("storeFilesForLink() error = %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("storeFilesForLink() returned %d files, want 1", len(files))
	}
	if files[0].ContentHash != "aaaaaaaaaaaaaaaa" {
		t.Errorf("storeFilesForLink() stamped %q onto index.js, want the recorded hash: a matching manifest is what the incremental relink runs on", files[0].ContentHash)
	}
}
