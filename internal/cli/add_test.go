package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/db"
	"github.com/pedrosousa13/lnpm/internal/pack"
	"github.com/pedrosousa13/lnpm/internal/store"
)

// seedStoreEntry writes a committed store entry holding one file and returns
// its path.
//
// The completeness marker is part of the fixture because it is part of what the
// write path commits, and the read path refuses an entry without one. Spelling
// the payload out rather than calling into the store keeps a change to the
// marker's contents from quietly turning every test built on this helper into
// one that exercises the refusal instead of what it was written for.
func seedStoreEntry(t *testing.T, storeRoot, name, hash, relPath, content string) string {
	t.Helper()

	entry := filepath.Join(storeRoot, name, hash)
	if err := os.MkdirAll(entry, 0755); err != nil {
		t.Fatalf("seed store entry: %v", err)
	}
	if err := os.WriteFile(filepath.Join(entry, relPath), []byte(content), 0644); err != nil {
		t.Fatalf("seed store file: %v", err)
	}
	marker := []byte(`{"schemaVersion":1,"hash":"` + hash + `"}` + "\n")
	if err := os.WriteFile(filepath.Join(entry, ".lnpm-complete"), marker, 0644); err != nil {
		t.Fatalf("seed completeness marker: %v", err)
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
	if err := finishPublish(src, pkgJSON, collected, database, false, db.DefaultTag); err != nil {
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

// --- resolveAddSpec: version history and rollback -----------------------------

// seedVersion records one version of a package, so the resolution tests read as
// a history rather than as struct literals. StorePath is set because everything
// downstream of a resolved package uses it; nothing here reads its contents.
func seedVersion(t *testing.T, database *db.DB, name, version, hash string) *db.Package {
	t.Helper()

	pkg := &db.Package{
		Name:        name,
		Version:     version,
		ContentHash: hash,
		SourcePath:  filepath.Join("/src", name),
		StorePath:   filepath.Join("/store", name, hash),
	}
	if err := database.InsertPackage(pkg); err != nil {
		t.Fatalf("seed %s@%s (%s): %v", name, version, hash, err)
	}
	return pkg
}

// TestResolveAddSpecResolvesAContentHash covers the rollback selector the issue
// asks for: a spec naming a content hash links that exact build, including one
// `latest` has moved off.
//
// The prefix cases are the point rather than a convenience. `lnpm list --versions`
// prints an eight-character short hash, so a user retypes a prefix and never the
// whole thing; a resolver that only took the full hash would refuse the only
// identifier the listing ever showed them.
func TestResolveAddSpecResolvesAContentHash(t *testing.T) {
	for _, tc := range []struct {
		name      string
		requested string
	}{
		{"short hash, as list --versions prints it", "aaaaaaaa"},
		{"full hash", "aaaaaaaa11111111"},
		{"a shorter prefix that is still unique", "aaaa"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, database := newGCStore(t)

			old := seedVersion(t, database, "rollback-pkg", "1.0.0", "aaaaaaaa11111111")
			seedVersion(t, database, "rollback-pkg", "2.0.0", "bbbbbbbb22222222")

			pkg, tag, _, err := resolveAddSpec(database, "rollback-pkg", tc.requested)
			if err != nil {
				t.Fatalf("resolveAddSpec(%s) error = %v", tc.requested, err)
			}
			if pkg == nil {
				t.Fatalf("resolveAddSpec(%s) resolved nothing, want the version that hash names", tc.requested)
			}
			if pkg.ID != old.ID {
				t.Errorf("resolveAddSpec(%s) resolved %s@%s, want the superseded 1.0.0", tc.requested, pkg.Version, pkg.ContentHash)
			}
			if tag != "" {
				t.Errorf("resolveAddSpec(%s) reported tag %q; a hash names a build, not a channel", tc.requested, tag)
			}
		})
	}
}

// TestResolveAddSpecResolvesASupersededVersion pins the other half of the
// selector. `lnpm list --versions` prints a semver version beside every hash, so
// a user will type the version; refusing it while the record is right there would
// be showing an identifier and then declining to accept it.
//
// This only widens an existing selector. A version spec already resolved when it
// matched what `latest` names; it now also resolves against the versions latest
// has moved off, so nothing that resolved before resolves differently.
func TestResolveAddSpecResolvesASupersededVersion(t *testing.T) {
	_, database := newGCStore(t)

	old := seedVersion(t, database, "superseded-spec-pkg", "1.0.0", "aaaaaaaa11111111")
	seedVersion(t, database, "superseded-spec-pkg", "2.0.0", "bbbbbbbb22222222")

	pkg, tag, _, err := resolveAddSpec(database, "superseded-spec-pkg", "1.0.0")
	if err != nil {
		t.Fatalf("resolveAddSpec(1.0.0) error = %v", err)
	}
	if pkg == nil || pkg.ID != old.ID {
		t.Fatalf("resolveAddSpec(1.0.0) resolved %v, want the superseded 1.0.0 record", pkg)
	}
	if tag != "" {
		t.Errorf("resolveAddSpec(1.0.0) reported tag %q, want the default channel", tag)
	}
}

// TestResolveAddSpecPrefersTheVersionTheDefaultTagNames guards against the one
// regression widening the version selector could cause.
//
// Two records can carry one version string: package.json can be excluded from a
// pack, and two publishes then differ in content while claiming the same version.
// Before, `pkg@1.0.0` compared against what latest names and got it. Preferring
// that record keeps the answer identical, and leaves the ambiguity refusal below
// for the case that used to be a flat error anyway.
func TestResolveAddSpecPrefersTheVersionTheDefaultTagNames(t *testing.T) {
	_, database := newGCStore(t)

	seedVersion(t, database, "collide-pkg", "1.0.0", "aaaaaaaa11111111")
	current := seedVersion(t, database, "collide-pkg", "1.0.0", "bbbbbbbb22222222")

	pkg, _, _, err := resolveAddSpec(database, "collide-pkg", "1.0.0")
	if err != nil {
		t.Fatalf("resolveAddSpec(1.0.0) error = %v; this resolved before the history existed and must go on resolving", err)
	}
	if pkg == nil || pkg.ID != current.ID {
		t.Fatalf("resolveAddSpec(1.0.0) resolved %v, want the version the default tag names", pkg)
	}
}

// TestResolveAddSpecRefusesAnAmbiguousVersion pins that a version string carried
// by several superseded records is refused rather than guessed at. This is the
// command a user reaches for when a release broke something; picking one of two
// builds for them is the last thing it should do.
func TestResolveAddSpecRefusesAnAmbiguousVersion(t *testing.T) {
	_, database := newGCStore(t)

	seedVersion(t, database, "ambiguous-version-pkg", "1.0.0", "aaaaaaaa11111111")
	seedVersion(t, database, "ambiguous-version-pkg", "1.0.0", "bbbbbbbb22222222")
	seedVersion(t, database, "ambiguous-version-pkg", "2.0.0", "cccccccc33333333")

	pkg, _, _, err := resolveAddSpec(database, "ambiguous-version-pkg", "1.0.0")
	if err == nil {
		t.Fatalf("resolveAddSpec(1.0.0) resolved %v, want a refusal: two retained versions carry 1.0.0", pkg)
	}
	for _, want := range []string{"aaaaaaaa", "bbbbbbbb"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal is %v, want it to name the candidate %s so the user can pick one", err, want)
		}
	}
}

// TestResolveAddSpecRefusesAnAmbiguousHashPrefix is the same rule for the looser
// selector. A prefix is not a claim about which version was meant, so a prefix
// two of them share has to be sent back rather than resolved to whichever record
// bolt happened to hand over first.
func TestResolveAddSpecRefusesAnAmbiguousHashPrefix(t *testing.T) {
	_, database := newGCStore(t)

	seedVersion(t, database, "ambiguous-hash-pkg", "1.0.0", "aaaaaaaa11111111")
	seedVersion(t, database, "ambiguous-hash-pkg", "2.0.0", "aaaaaaaa22222222")

	pkg, _, _, err := resolveAddSpec(database, "ambiguous-hash-pkg", "aaaaaaaa")
	if err == nil {
		t.Fatalf("resolveAddSpec(aaaaaaaa) resolved %v, want a refusal: the prefix matches two versions", pkg)
	}
	for _, want := range []string{"aaaaaaaa11111111", "aaaaaaaa22222222"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal is %v, want it to name the candidate %s so the user can lengthen the prefix", err, want)
		}
	}
}

// TestResolveAddSpecPrefersATagOverAVersionAndAHash pins the first step of the
// resolution order, which predates this change and must survive it: a tag is a
// name a human chose for this package, so it wins over both.
func TestResolveAddSpecPrefersATagOverAVersionAndAHash(t *testing.T) {
	_, database := newGCStore(t)

	seedVersion(t, database, "tagfirst-pkg", "1.0.0", "aaaaaaaa11111111")
	pinned := seedVersion(t, database, "tagfirst-pkg", "2.0.0", "bbbbbbbb22222222")
	// A tag named exactly like the other version, which is the only way the two
	// steps can be made to disagree.
	if err := database.SetTag("tagfirst-pkg", "1.0.0", pinned.ContentHash); err != nil {
		t.Fatalf("set the tag: %v", err)
	}

	pkg, tag, _, err := resolveAddSpec(database, "tagfirst-pkg", "1.0.0")
	if err != nil {
		t.Fatalf("resolveAddSpec(1.0.0) error = %v", err)
	}
	if pkg == nil || pkg.ID != pinned.ID {
		t.Fatalf("resolveAddSpec(1.0.0) resolved %v, want the version the tag names", pkg)
	}
	if tag != "1.0.0" {
		t.Errorf("resolveAddSpec reported tag %q, want the link to follow the channel that was asked for", tag)
	}
}

// TestResolveAddSpecPrefersAVersionOverAHashPrefix pins the second step against
// the third. An exact match on a whole version string is more specific than a
// prefix match on a hash, so it wins - and the order has to be fixed rather than
// incidental, because the two can be made to collide.
func TestResolveAddSpecPrefersAVersionOverAHashPrefix(t *testing.T) {
	_, database := newGCStore(t)

	// One record is named "aaaaaaaa"; another's content hash starts with it. The
	// named one is seeded first so the default tag has moved off it, which is
	// what stops this passing on the old "compare against latest" rule alone.
	named := seedVersion(t, database, "collide-order-pkg", "aaaaaaaa", "bbbbbbbb22222222")
	seedVersion(t, database, "collide-order-pkg", "2.0.0", "aaaaaaaa11111111")

	pkg, _, _, err := resolveAddSpec(database, "collide-order-pkg", "aaaaaaaa")
	if err != nil {
		t.Fatalf("resolveAddSpec(aaaaaaaa) error = %v", err)
	}
	if pkg == nil || pkg.ID != named.ID {
		t.Fatalf("resolveAddSpec(aaaaaaaa) resolved %v, want the record whose version is exactly aaaaaaaa", pkg)
	}
}

// TestResolveAddSpecErrorNamesEveryRetainedVersion pins the wording of the dead
// end. The message used to name what `latest` points at as though it were the
// only version there is, which stopped being true the moment the store retained
// more than one - and it is the exact moment the user needs to be told what else
// is available to roll back to.
func TestResolveAddSpecErrorNamesEveryRetainedVersion(t *testing.T) {
	_, database := newGCStore(t)

	seedVersion(t, database, "deadend-pkg", "1.0.0", "aaaaaaaa11111111")
	seedVersion(t, database, "deadend-pkg", "2.0.0", "bbbbbbbb22222222")

	_, _, _, err := resolveAddSpec(database, "deadend-pkg", "9.9.9")
	if err == nil {
		t.Fatal("resolveAddSpec(9.9.9) succeeded, want a refusal: no version carries it")
	}
	for _, want := range []string{"1.0.0", "2.0.0", "aaaaaaaa", "bbbbbbbb"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal is %v, want it to name the retained version %s the user could roll back to", err, want)
		}
	}
	// The name and the spec are two plain strings the message reads in order, so
	// a caller that swaps them still compiles and produces a message that lies
	// about which is which. Reading the opening clause is what catches that.
	if !strings.HasPrefix(err.Error(), "no version of deadend-pkg matches 9.9.9") {
		t.Errorf("the refusal is %v, want it to name the package and then the spec", err)
	}
}

// TestResolveAddSpecErrorCapsTheRetainedVersions pins that the dead end stays a
// readable clause on a package with a long history. The message points at
// `lnpm list <pkg> --versions`, so it does not have to be the history: naming
// every retained version of a package with thirty of them produces a single
// ~700-character error, which the parallel add path then wraps again.
func TestResolveAddSpecErrorCapsTheRetainedVersions(t *testing.T) {
	_, database := newGCStore(t)

	for i := 0; i < 30; i++ {
		seedVersion(t, database, "long-history-pkg",
			fmt.Sprintf("1.0.%d", i),
			fmt.Sprintf("%02dcccccccccccccc", i))
	}

	_, _, _, err := resolveAddSpec(database, "long-history-pkg", "9.9.9")
	if err == nil {
		t.Fatal("resolveAddSpec(9.9.9) succeeded, want a refusal")
	}
	if strings.Contains(err.Error(), "1.0.0") {
		t.Errorf("the refusal names all thirty retained versions:\n%v", err)
	}
	if !strings.Contains(err.Error(), "more") {
		t.Errorf("the refusal is %v, want it to say how many versions it left out", err)
	}
	if len(err.Error()) > 250 {
		t.Errorf("the refusal is %d characters long, want a clause a wrapped error can carry:\n%v", len(err.Error()), err)
	}
}

// TestResolveAddSpecReportsHowTheSpecResolved pins the fact --link has to guard
// on, and which the returned tag cannot carry.
//
// A tag comes back as itself, but a version and a hash both come back with an
// empty tag, because they name a build rather than a channel - the same empty
// tag a bare name returns. A guard reading the tag string therefore cannot tell
// "the user asked for nothing" from "the user asked for a specific build", and
// would let `add pkg@<hash> --link` through to live-link the working tree while
// printing the historical version number.
func TestResolveAddSpecReportsHowTheSpecResolved(t *testing.T) {
	_, database := newGCStore(t)

	seedVersion(t, database, "kind-pkg", "1.0.0", "aaaaaaaa11111111")
	newest := seedVersion(t, database, "kind-pkg", "2.0.0", "bbbbbbbb22222222")
	if err := database.SetTag("kind-pkg", "beta", newest.ContentHash); err != nil {
		t.Fatalf("set the tag: %v", err)
	}

	for _, tc := range []struct {
		requested string
		want      specKind
	}{
		{"", specDefault},
		{"beta", specTag},
		{"1.0.0", specVersion},
		{"aaaaaaaa", specHash},
	} {
		t.Run(string(tc.want), func(t *testing.T) {
			pkg, _, kind, err := resolveAddSpec(database, "kind-pkg", tc.requested)
			if err != nil {
				t.Fatalf("resolveAddSpec(%q) error = %v", tc.requested, err)
			}
			if pkg == nil {
				t.Fatalf("resolveAddSpec(%q) resolved nothing", tc.requested)
			}
			if kind != tc.want {
				t.Errorf("resolveAddSpec(%q) reported kind %q, want %q", tc.requested, kind, tc.want)
			}
		})
	}
}

// TestResolveAddSpecRefusesATooShortHashPrefix pins the floor under the hash
// step. `lnpm add mylib@2` is a user who means version 2; without a minimum it
// is tried against content hashes, and one retained hash beginning with "2"
// silently resolves it to that build - or, worse, several do and the user is
// told their spec is an ambiguous hash prefix.
//
// Four characters is git's minimum for an abbreviated object name, which the
// hash step is modelled on.
func TestResolveAddSpecRefusesATooShortHashPrefix(t *testing.T) {
	for _, requested := range []string{"2", "22", "222"} {
		t.Run(requested, func(t *testing.T) {
			_, database := newGCStore(t)

			seedVersion(t, database, "shortprefix-pkg", "1.0.0", "2222222211111111")
			seedVersion(t, database, "shortprefix-pkg", "3.0.0", "bbbbbbbb22222222")

			pkg, _, _, err := resolveAddSpec(database, "shortprefix-pkg", requested)
			if err == nil {
				t.Fatalf("resolveAddSpec(%s) resolved %v, want a refusal: that is too short to be a hash", requested, pkg)
			}
			if !strings.Contains(err.Error(), "no version of shortprefix-pkg matches") {
				t.Errorf("resolveAddSpec(%s) error = %v, want the dead end a version spec gets, not a hash-prefix verdict", requested, err)
			}
		})
	}
}

// TestResolveAddSpecOnAnUnknownNameResolvesNothing pins that a name the store
// has never held stays a nil package rather than becoming an error. The two add
// paths word that one differently, and both wordings predate this change.
// TestParsePackageSpecComposesTheName pins the lookup half of #327's second
// criterion. The store holds composed names, so a spec typed decomposed has to
// compose before it is looked up or it misses a row that is there - and the two
// spellings render identically, so the miss is undiagnosable from the terminal.
//
// Composing here strands nothing, which is what separates this from the removal
// path, where composing would be a bug. A decomposed row can only be one written
// before #327, and such a row cannot be linked in any case: linkPackage goes
// through link.Link, which validates strictly and refuses it. So the row this
// composition steps past is one no spelling of the spec could have used. Removal
// is the opposite - there the decomposed name is the only handle on the entry.
//
// The version half of the spec is deliberately untouched. It is matched against
// a semver string, not against a path or a name, and #327 says nothing about it.
func TestParsePackageSpecComposesTheName(t *testing.T) {
	const nfc = "caf\u00e9"  // "caf" + LATIN SMALL LETTER E WITH ACUTE
	const nfd = "cafe\u0301" // "cafe" + COMBINING ACUTE ACCENT

	cases := []struct {
		spec        string
		wantName    string
		wantVersion string
	}{
		{nfd, nfc, ""},
		{nfd + "@1.2.3", nfc, "1.2.3"},
		{"@org/" + nfd, "@org/" + nfc, ""},
		{"@" + nfd + "/pkg@2.0.0", "@" + nfc + "/pkg", "2.0.0"},
		{"left-pad@1.0.0", "left-pad", "1.0.0"},
	}
	for _, tc := range cases {
		name, version := parsePackageSpec(tc.spec)
		if name != tc.wantName {
			t.Errorf("parsePackageSpec(%q) name = %q, want %q", tc.spec, name, tc.wantName)
		}
		if version != tc.wantVersion {
			t.Errorf("parsePackageSpec(%q) version = %q, want %q", tc.spec, version, tc.wantVersion)
		}
	}
}

// TestPackageNotInStoreErrorNamesADecomposedRow is the other direction, and the
// one composing the spec cannot fix. A store published before #327 can hold a
// decomposed row, and after the composition above no spelling anyone can type
// reaches it. Without this the user is told "Did you run 'lnpm publish' in the
// package directory?" about a package they did publish, under a name that
// renders exactly like the one in the store.
//
// The remedy has to be doctor rather than a name to retype, because there is no
// name to retype: the row cannot be linked whatever it is called until it is
// re-published.
func TestPackageNotInStoreErrorNamesADecomposedRow(t *testing.T) {
	const nfc = "caf\u00e9"
	const nfd = "cafe\u0301"

	_, database := newGCStore(t)
	seedVersion(t, database, nfd, "1.0.0", "aaa111")

	err := packageNotInStoreError(database, nfc)

	if err == nil {
		t.Fatalf("packageNotInStoreError(%q) = nil, want an error", nfc)
	}
	if strings.Contains(err.Error(), "Did you run") {
		t.Errorf("packageNotInStoreError(%q) accused the user of not publishing: %v", nfc, err)
	}
	if !strings.Contains(err.Error(), "lnpm doctor") {
		t.Errorf("packageNotInStoreError(%q) = %v, want it to point at doctor", nfc, err)
	}
}

// TestPackageNotInStoreErrorKeepsTheOrdinaryAdviceOtherwise is what stops the
// hint above becoming the message everyone sees. A name genuinely absent from
// the store still gets the question that is right for it.
func TestPackageNotInStoreErrorKeepsTheOrdinaryAdviceOtherwise(t *testing.T) {
	_, database := newGCStore(t)
	seedVersion(t, database, "left-pad", "1.0.0", "aaa111")

	err := packageNotInStoreError(database, "no-such-pkg")

	if err == nil {
		t.Fatalf("packageNotInStoreError = nil, want an error")
	}
	if !strings.Contains(err.Error(), "Did you run") {
		t.Errorf("packageNotInStoreError = %v, want the ordinary publish question", err)
	}
	if strings.Contains(err.Error(), "lnpm doctor") {
		t.Errorf("packageNotInStoreError = %v, want no normalization hint for an absent name", err)
	}
}

func TestResolveAddSpecOnAnUnknownNameResolvesNothing(t *testing.T) {
	_, database := newGCStore(t)

	pkg, _, _, err := resolveAddSpec(database, "no-such-pkg", "1.0.0")
	if err != nil {
		t.Fatalf("resolveAddSpec on an unknown name error = %v, want the caller to word it", err)
	}
	if pkg != nil {
		t.Errorf("resolveAddSpec on an unknown name resolved %v", pkg)
	}
}
