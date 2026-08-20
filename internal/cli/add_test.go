package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/db"
	"github.com/pedrosousa13/lnpm/internal/pack"
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

// publishForLink publishes a package built from files at a fresh source
// directory and returns its database row, which is what add, pull and push each
// start from when they link it.
func publishForLink(t *testing.T, database *db.DB, name string, files map[string]os.FileMode) *db.Package {
	t.Helper()

	src := t.TempDir()
	for rel, mode := range files {
		path := filepath.Join(src, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatalf("seed source dir: %v", err)
		}
		if err := os.WriteFile(path, []byte("// "+rel+"\n"), mode); err != nil {
			t.Fatalf("seed source file: %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(src, "package.json"), []byte(`{"name":"`+name+`","version":"1.0.0"}`), 0644); err != nil {
		t.Fatalf("seed package.json: %v", err)
	}

	pkgJSON, collected, err := pack.Pack(src)
	if err != nil {
		t.Fatalf("pack source: %v", err)
	}
	if err := finishPublish(src, pkgJSON, collected, database, false); err != nil {
		t.Fatalf("publish: %v", err)
	}

	pkg, err := database.GetPackageByName(name)
	if err != nil || pkg == nil {
		t.Fatalf("look up published package: %v", err)
	}
	return pkg
}

// pushFilesForLink is the file set push and publish --push hand to Link, built
// the way both of them build it: pack.FileInfoFromStore over the rows recorded
// for the package.
func pushFilesForLink(t *testing.T, database *db.DB, pkg *db.Package) []*pack.FileInfo {
	t.Helper()

	entries, err := database.GetFilesForPackage(pkg.ID)
	if err != nil {
		t.Fatalf("read recorded files: %v", err)
	}
	data := make([]pack.FileEntryData, len(entries))
	for i, e := range entries {
		data[i] = pack.FileEntryData{RelPath: e.RelativePath, Size: e.Size, Mode: e.Mode, Hash: e.ContentHash}
	}
	return pack.FileInfoFromStore(pkg.StorePath, data)
}

// TestStoreFilesForLinkAgreesWithThePushProducer pins the invariant the
// incremental relink rests on: the two file sets handed to Link describe the
// same package identically.
//
// They are built by different commands from different sources - add and pull
// walk the store entry, push and publish --push carry the packed source's
// files forward - and Link compares one against the other across that boundary,
// through the manifest one link writes and the next one reads. A field they
// disagree about marks every file changed, and the whole package is rewritten
// on every push: exactly the optimisation this exists to deliver, silently off.
//
// The comparison is platform-independent on purpose. A producer that disagreed
// only on Windows, where a mode collapses to a read-only bit, would fail here on
// the machine the developer is actually using.
func TestStoreFilesForLinkAgreesWithThePushProducer(t *testing.T) {
	_, database := newGCStore(t)

	pkg := publishForLink(t, database, "agreeing-pkg", map[string]os.FileMode{
		"index.js":     0644,
		"lib/util.js":  0644,
		"bin/cli.js":   0755,
		"docs/read.md": 0644,
	})

	s, err := store.New()
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	linkFiles, err := storeFilesForLink(database, s, pkg)
	if err != nil {
		t.Fatalf("storeFilesForLink() error = %v", err)
	}
	pushFiles := pushFilesForLink(t, database, pkg)

	fromPush := make(map[string]*pack.FileInfo, len(pushFiles))
	for _, f := range pushFiles {
		fromPush[f.RelPath] = f
	}
	if len(linkFiles) != len(pushFiles) {
		t.Fatalf("the store walk yielded %d files and the recorded rows %d", len(linkFiles), len(pushFiles))
	}

	for _, got := range linkFiles {
		want, ok := fromPush[got.RelPath]
		if !ok {
			t.Errorf("%s is in the store entry but not in what push links, so a push would drop it", got.RelPath)
			continue
		}
		if got.ContentHash != want.ContentHash {
			t.Errorf("%s: add links it as %q and push as %q; a relink across the two rewrites it however little changed", got.RelPath, got.ContentHash, want.ContentHash)
		}
		if got.Mode != want.Mode {
			t.Errorf("%s: add links it with mode %v and push with mode %v; a relink across the two rewrites it however little changed", got.RelPath, got.Mode, want.Mode)
		}
	}
}

// TestStoreFilesForLinkTakesTheRecordedModeNotTheStoresOwn is the mechanism
// behind that agreement: the mode comes from the row the hash comes from, so it
// is the mode the package was published with rather than whatever the store's
// copy happens to stat as now.
//
// Those two parted company for real. copyFile gained its explicit chmod because
// os.OpenFile's mode argument is masked by the umask, so a store entry written
// before that carries permissions the package was never published with - and a
// producer reading them would disagree with push about every file in it for as
// long as the entry lives.
func TestStoreFilesForLinkTakesTheRecordedModeNotTheStoresOwn(t *testing.T) {
	_, database := newGCStore(t)

	pkg := publishForLink(t, database, "drifted-mode-pkg", map[string]os.FileMode{"index.js": 0644})

	stored := filepath.Join(pkg.StorePath, "index.js")
	if err := os.Chmod(stored, 0600); err != nil {
		t.Fatalf("chmod store file: %v", err)
	}
	info, err := os.Stat(stored)
	if err != nil {
		t.Fatalf("stat store file: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Skipf("this platform does not model the permission bits the drift is made of: the store file stats as %v", info.Mode())
	}

	s, err := store.New()
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	files, err := storeFilesForLink(database, s, pkg)
	if err != nil {
		t.Fatalf("storeFilesForLink() error = %v", err)
	}

	for _, f := range files {
		if f.RelPath != "index.js" {
			continue
		}
		if f.Mode.Perm() != 0644 {
			t.Errorf("storeFilesForLink() reports index.js as %v, want the published 0644: push reports the recorded mode, and a relink across the two rewrites every file they disagree about", f.Mode)
		}
	}
}
