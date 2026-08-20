package link

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/config"
	"github.com/pedrosousa13/lnpm/internal/pack"
)

// writeHashedStoreFiles is writeStoreFiles with each file's content hash filled
// in, which is what a relink compares against the last link to tell an unchanged
// file from a changed one. pack.HashFile rather than a literal, so "same
// content" and "same hash" cannot drift apart inside a test.
func writeHashedStoreFiles(t *testing.T, storePath string, contents map[string]string) []*pack.FileInfo {
	t.Helper()

	files := writeStoreFiles(t, storePath, contents)
	for _, f := range files {
		hash, err := pack.HashFile(f.Path)
		if err != nil {
			t.Fatalf("failed to hash %s: %v", f.RelPath, err)
		}
		f.ContentHash = hash
	}
	return files
}

// fileIdentity returns a FileInfo describing the file at path as it is now, so
// that os.SameFile still answers about that file after path has been replaced.
//
// os.Stat is not enough, because os.SameFile is not everywhere the eager
// comparison it looks like. On unix a FileInfo carries the inode from the moment
// it was taken. On Windows the fast path of os.stat calls GetFileAttributesEx,
// which returns no file index, so os.fileStat keeps the path instead
// (saveInfoFromPath) and os.SameFile resolves it to a volume and file index only
// when it is called (loadFileId, which opens the path afresh). A FileInfo
// captured before a relink would therefore describe whatever the relink left at
// that path, and a before/after comparison would report the same file however
// much had changed - which makes the "this file was rewritten" half of the
// assertion unfalsifiable and the "this file was not" half true for the wrong
// reason.
//
// Statting through an open file closes that gap on every platform: Windows takes
// that FileInfo from GetFileInformationByHandle, which fills the volume and file
// index in immediately and leaves nothing for os.SameFile to resolve later, and
// unix does an fstat on the same descriptor. Either way the identity is the one
// the file had when it was read.
func fileIdentity(t *testing.T, path string) os.FileInfo {
	t.Helper()

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("failed to open %s: %v", path, err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		t.Fatalf("failed to stat %s: %v", path, err)
	}
	return info
}

// statLinked returns the identity of each relPath inside a linked package, keyed
// by the relative path, for comparison with os.SameFile across a relink.
func statLinked(t *testing.T, lnpmPath string, relPaths ...string) map[string]os.FileInfo {
	t.Helper()

	stats := make(map[string]os.FileInfo, len(relPaths))
	for _, rel := range relPaths {
		stats[rel] = fileIdentity(t, filepath.Join(lnpmPath, filepath.FromSlash(rel)))
	}
	return stats
}

// TestLink_ReusesUnchangedFilesFromThePreviousLink is acceptance criterion 2: a
// relink that changes one file must touch only that file. An unchanged file is
// carried over from the previous link rather than re-materialised, so it keeps
// the identity the consumer already had.
func TestLink_ReusesUnchangedFilesFromThePreviousLink(t *testing.T) {
	tmpDir := t.TempDir()
	projectPath := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(projectPath, 0755); err != nil {
		t.Fatal(err)
	}

	v1 := filepath.Join(tmpDir, "store", "v1")
	files1 := writeHashedStoreFiles(t, v1, map[string]string{
		"package.json":  `{"name":"my-package","version":"1.0.0"}`,
		"dist/index.js": "module.exports = 1;\n",
		"dist/utils.js": "module.exports = 2;\n",
	})

	linker := New(projectPath)
	if _, err := linker.Link("my-package", v1, files1); err != nil {
		t.Fatalf("first Link() error: %v", err)
	}

	lnpmPath := filepath.Join(projectPath, ".lnpm", "my-package")
	before := statLinked(t, lnpmPath, "package.json", "dist/index.js", "dist/utils.js")

	// Only dist/utils.js differs from v1.
	v2 := filepath.Join(tmpDir, "store", "v2")
	files2 := writeHashedStoreFiles(t, v2, map[string]string{
		"package.json":  `{"name":"my-package","version":"1.0.0"}`,
		"dist/index.js": "module.exports = 1;\n",
		"dist/utils.js": "module.exports = 99;\n",
	})

	res, err := linker.Link("my-package", v2, files2)
	if err != nil {
		t.Fatalf("second Link() error: %v", err)
	}
	if res.Changed != 1 || res.Unchanged != 2 {
		t.Errorf("Link() reported %d changed / %d unchanged, want 1 / 2", res.Changed, res.Unchanged)
	}

	after := statLinked(t, lnpmPath, "package.json", "dist/index.js", "dist/utils.js")
	for _, rel := range []string{"package.json", "dist/index.js"} {
		if !os.SameFile(before[rel], after[rel]) {
			t.Errorf("unchanged %s is a different file after the relink, so it was rewritten", rel)
		}
	}
	if os.SameFile(before["dist/utils.js"], after["dist/utils.js"]) {
		t.Error("changed dist/utils.js kept its identity, so the new content was never written")
	}

	got, err := os.ReadFile(filepath.Join(lnpmPath, "dist", "utils.js"))
	if err != nil {
		t.Fatalf("failed to read relinked dist/utils.js: %v", err)
	}
	if string(got) != "module.exports = 99;\n" {
		t.Errorf("relinked dist/utils.js = %q, want the v2 content", string(got))
	}
}

// TestLink_UnchangedPackageIsNotRewritten is acceptance criterion 1: relinking
// a package nothing has changed in must rewrite no file at all.
//
// The package directory's own identity is the assertion that carries it. A
// relink that did any work would have built a replacement and renamed it into
// place, and .lnpm/{package} would be a different directory afterwards however
// identical its contents.
func TestLink_UnchangedPackageIsNotRewritten(t *testing.T) {
	tmpDir := t.TempDir()
	projectPath := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(projectPath, 0755); err != nil {
		t.Fatal(err)
	}

	storePath := filepath.Join(tmpDir, "store", "my-package")
	files := writeHashedStoreFiles(t, storePath, map[string]string{
		"package.json":  `{"name":"my-package","version":"1.0.0"}`,
		"dist/index.js": "module.exports = 1;\n",
		"dist/utils.js": "module.exports = 2;\n",
	})

	linker := New(projectPath)
	if _, err := linker.Link("my-package", storePath, files); err != nil {
		t.Fatalf("first Link() error: %v", err)
	}

	lnpmPath := filepath.Join(projectPath, ".lnpm", "my-package")
	dirBefore := fileIdentity(t, lnpmPath)
	before := statLinked(t, lnpmPath, "package.json", "dist/index.js", "dist/utils.js")

	res, err := linker.Link("my-package", storePath, files)
	if err != nil {
		t.Fatalf("second Link() error: %v", err)
	}
	if res.Changed != 0 || res.Unchanged != 3 {
		t.Errorf("Link() reported %d changed / %d unchanged, want 0 / 3", res.Changed, res.Unchanged)
	}

	dirAfter := fileIdentity(t, lnpmPath)
	if !os.SameFile(dirBefore, dirAfter) {
		t.Error(".lnpm/my-package is a different directory after relinking an unchanged package, so it was rebuilt and swapped in")
	}

	after := statLinked(t, lnpmPath, "package.json", "dist/index.js", "dist/utils.js")
	for rel, was := range before {
		if !os.SameFile(was, after[rel]) {
			t.Errorf("%s is a different file after relinking an unchanged package, so it was rewritten", rel)
		}
	}
}

// TestLink_RestoresAFileDeletedFromUnderIt keeps relinking a repair. A user who
// has lost part of .lnpm/{package} - a build script that cleaned too much, a
// half-finished delete - reaches for the command that put it there, and before
// there was anything to skip, relinking always rebuilt the package and so always
// fixed it. Trusting the manifest without looking would end that: it says the
// file was linked, which is true, and says nothing about it still being there.
func TestLink_RestoresAFileDeletedFromUnderIt(t *testing.T) {
	tmpDir := t.TempDir()
	projectPath := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(projectPath, 0755); err != nil {
		t.Fatal(err)
	}

	storePath := filepath.Join(tmpDir, "store", "my-package")
	files := writeHashedStoreFiles(t, storePath, map[string]string{
		"package.json":  `{"name":"my-package","version":"1.0.0"}`,
		"dist/index.js": "module.exports = 1;\n",
		"dist/utils.js": "module.exports = 2;\n",
	})

	linker := New(projectPath)
	if _, err := linker.Link("my-package", storePath, files); err != nil {
		t.Fatalf("first Link() error: %v", err)
	}

	lnpmPath := filepath.Join(projectPath, ".lnpm", "my-package")
	if err := os.Remove(filepath.Join(lnpmPath, "dist", "utils.js")); err != nil {
		t.Fatal(err)
	}

	if _, err := linker.Link("my-package", storePath, files); err != nil {
		t.Fatalf("second Link() error: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(lnpmPath, "dist", "utils.js"))
	if err != nil {
		t.Fatalf("relinking did not restore the deleted dist/utils.js: %v", err)
	}
	if string(got) != "module.exports = 2;\n" {
		t.Errorf("restored dist/utils.js = %q, want the store's content", string(got))
	}
}

// TestLink_RemovesAStrayLeftInTheLinkedPackage keeps relinking a full repair on
// the side the manifest cannot see. Every question the shortcut asks is about a
// file the manifest names, so a file the manifest does not name - dropped in by
// a build script, or orphaned by a link made before manifests existed - would
// answer none of them and survive, where before this every relink swapped in a
// tree built from the package alone and so removed it.
func TestLink_RemovesAStrayLeftInTheLinkedPackage(t *testing.T) {
	tmpDir := t.TempDir()
	projectPath := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(projectPath, 0755); err != nil {
		t.Fatal(err)
	}

	storePath := filepath.Join(tmpDir, "store", "my-package")
	files := writeHashedStoreFiles(t, storePath, map[string]string{
		"package.json":  `{"name":"my-package","version":"1.0.0"}`,
		"dist/index.js": "module.exports = 1;\n",
	})

	linker := New(projectPath)
	if _, err := linker.Link("my-package", storePath, files); err != nil {
		t.Fatalf("first Link() error: %v", err)
	}

	lnpmPath := filepath.Join(projectPath, ".lnpm", "my-package")
	stray := filepath.Join(lnpmPath, "dist", "stray.js")
	if err := os.WriteFile(stray, []byte("module.exports = 'stray';\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := linker.Link("my-package", storePath, files); err != nil {
		t.Fatalf("second Link() error: %v", err)
	}

	if _, err := os.Stat(stray); !os.IsNotExist(err) {
		t.Errorf("dist/stray.js survived a relink of a package that does not list it (stat err = %v)", err)
	}
	if _, err := os.Stat(filepath.Join(lnpmPath, "dist", "index.js")); err != nil {
		t.Errorf("dist/index.js did not survive the relink: %v", err)
	}
}

// TestLink_RepairsALinkedFileReplacedByASymlink covers the other thing an lstat
// per file cannot tell apart from a healthy link. os.Link does not follow
// symlinks, so a linked file something has replaced with a symlink would be
// carried over as that symlink and survive the swap, and the consumer would go
// on resolving the package's file to whatever it points at.
func TestLink_RepairsALinkedFileReplacedByASymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping on Windows - creating a symlink needs a privilege the test process may not hold")
	}

	tmpDir := t.TempDir()
	projectPath := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(projectPath, 0755); err != nil {
		t.Fatal(err)
	}

	storePath := filepath.Join(tmpDir, "store", "my-package")
	files := writeHashedStoreFiles(t, storePath, map[string]string{
		"package.json":  `{"name":"my-package","version":"1.0.0"}`,
		"dist/index.js": "module.exports = 1;\n",
	})

	linker := New(projectPath)
	if _, err := linker.Link("my-package", storePath, files); err != nil {
		t.Fatalf("first Link() error: %v", err)
	}

	elsewhere := filepath.Join(tmpDir, "elsewhere.js")
	if err := os.WriteFile(elsewhere, []byte("module.exports = 'elsewhere';\n"), 0644); err != nil {
		t.Fatal(err)
	}
	linkedFile := filepath.Join(projectPath, ".lnpm", "my-package", "dist", "index.js")
	if err := os.Remove(linkedFile); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, linkedFile); err != nil {
		t.Fatal(err)
	}

	if _, err := linker.Link("my-package", storePath, files); err != nil {
		t.Fatalf("second Link() error: %v", err)
	}

	info, err := os.Lstat(linkedFile)
	if err != nil {
		t.Fatalf("failed to stat relinked dist/index.js: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Error("relinked dist/index.js is still a symlink, so the relink carried the replacement forward instead of repairing it")
	}
	got, err := os.ReadFile(linkedFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "module.exports = 1;\n" {
		t.Errorf("relinked dist/index.js = %q, want the store's content", string(got))
	}
}

// TestLink_DropsFilesRemovedFromThePackage is acceptance criterion 3. It holds
// because the relink builds a fresh directory and swaps it in, so a file the new
// package does not list simply never enters it. The test pins that the reuse
// path does not quietly turn the rebuild into an in-place update, which would
// leave removed files behind.
func TestLink_DropsFilesRemovedFromThePackage(t *testing.T) {
	tmpDir := t.TempDir()
	projectPath := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(projectPath, 0755); err != nil {
		t.Fatal(err)
	}

	v1 := filepath.Join(tmpDir, "store", "v1")
	files1 := writeHashedStoreFiles(t, v1, map[string]string{
		"package.json":  `{"name":"my-package","version":"1.0.0"}`,
		"dist/index.js": "module.exports = 1;\n",
		"dist/gone.js":  "module.exports = 'gone';\n",
	})

	linker := New(projectPath)
	if _, err := linker.Link("my-package", v1, files1); err != nil {
		t.Fatalf("first Link() error: %v", err)
	}

	// v2 is v1 without dist/gone.js. Every remaining file is unchanged, which is
	// exactly the case a reuse-everything shortcut would get wrong.
	v2 := filepath.Join(tmpDir, "store", "v2")
	files2 := writeHashedStoreFiles(t, v2, map[string]string{
		"package.json":  `{"name":"my-package","version":"1.0.0"}`,
		"dist/index.js": "module.exports = 1;\n",
	})

	if _, err := linker.Link("my-package", v2, files2); err != nil {
		t.Fatalf("second Link() error: %v", err)
	}

	lnpmPath := filepath.Join(projectPath, ".lnpm", "my-package")
	if _, err := os.Stat(filepath.Join(lnpmPath, "dist", "gone.js")); !os.IsNotExist(err) {
		t.Errorf("dist/gone.js survived a relink of a package that no longer lists it (stat err = %v)", err)
	}
	if _, err := os.Stat(filepath.Join(lnpmPath, "dist", "index.js")); err != nil {
		t.Errorf("dist/index.js did not survive the relink: %v", err)
	}
}

// TestLink_RelinkOnAModeChangeAlone rewrites the file whose only change is its
// mode. Reuse is a hard link, so a carried-over file keeps the mode of the one
// already linked - a package whose bin script has just been made executable
// would stay unexecutable if the mode were not part of the comparison.
func TestLink_RelinkOnAModeChangeAlone(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping on Windows - the executable bit is not modelled there")
	}

	tmpDir := t.TempDir()
	projectPath := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(projectPath, 0755); err != nil {
		t.Fatal(err)
	}

	contents := map[string]string{
		"package.json": `{"name":"my-package","version":"1.0.0"}`,
		"bin/cli.js":   "#!/usr/bin/env node\n",
	}

	v1 := filepath.Join(tmpDir, "store", "v1")
	files1 := writeHashedStoreFiles(t, v1, contents)

	linker := New(projectPath)
	if _, err := linker.Link("my-package", v1, files1); err != nil {
		t.Fatalf("first Link() error: %v", err)
	}

	// Same bytes, now executable.
	v2 := filepath.Join(tmpDir, "store", "v2")
	files2 := writeHashedStoreFiles(t, v2, contents)
	for _, f := range files2 {
		if f.RelPath != "bin/cli.js" {
			continue
		}
		if err := os.Chmod(f.Path, 0755); err != nil {
			t.Fatal(err)
		}
		f.Mode = 0755
	}

	res, err := linker.Link("my-package", v2, files2)
	if err != nil {
		t.Fatalf("second Link() error: %v", err)
	}
	if res.Changed != 1 {
		t.Errorf("Link() reported %d changed, want 1: the mode change must not count as unchanged", res.Changed)
	}

	info, err := os.Stat(filepath.Join(projectPath, ".lnpm", "my-package", "bin", "cli.js"))
	if err != nil {
		t.Fatalf("failed to stat relinked bin/cli.js: %v", err)
	}
	if info.Mode().Perm()&0111 == 0 {
		t.Errorf("relinked bin/cli.js has mode %v, want the executable bits the new package carries", info.Mode().Perm())
	}
}

// TestLink_DoesNotReuseThroughALiveLink covers the one way reuse could reach
// content nobody published. After LinkSource, .lnpm/{package} is a link at the
// package's own source directory, so the paths a relink would reuse from resolve
// into a tree the author is still editing. Hard linking out of it would both
// serve unpublished content and hand the consumer an inode inside the author's
// working copy, where an edit through node_modules writes straight back into the
// source.
//
// The source tree here is given a manifest of its own so the refusal has to be
// deliberate. Without one the relink would find nothing to trust and reuse
// nothing by accident, which would prove only that the accident happens to hold.
func TestLink_DoesNotReuseThroughALiveLink(t *testing.T) {
	tmpDir := t.TempDir()
	projectPath := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(projectPath, 0755); err != nil {
		t.Fatal(err)
	}

	published := map[string]string{
		"package.json":  `{"name":"my-package","version":"1.0.0"}`,
		"dist/index.js": "module.exports = 'published';\n",
	}

	storePath := filepath.Join(tmpDir, "store", "my-package")
	files := writeHashedStoreFiles(t, storePath, published)

	linker := New(projectPath)
	if _, err := linker.Link("my-package", storePath, files); err != nil {
		t.Fatalf("Link() error: %v", err)
	}

	// The source directory holds the same paths with edits that were never
	// published, under a manifest claiming they are the published ones. The
	// manifest is written as if for the linked package directory, so it passes
	// every other check a relink makes and only the refusal to read through a
	// live link can stop it.
	sourcePath := filepath.Join(tmpDir, "source")
	writeStoreFiles(t, sourcePath, map[string]string{
		"package.json":  `{"name":"my-package","version":"1.0.0"}`,
		"dist/index.js": "module.exports = 'edited but never published';\n",
	})
	writeManifest(sourcePath, filepath.Join(projectPath, ".lnpm", "my-package"), HardLink, files)

	if _, err := linker.LinkSource("my-package", sourcePath); err != nil {
		t.Fatalf("LinkSource() error: %v", err)
	}

	res, err := linker.Link("my-package", storePath, files)
	if err != nil {
		t.Fatalf("Link() back from source error: %v", err)
	}
	if res.Unchanged != 0 {
		t.Errorf("Link() reported %d unchanged after a live link, want 0: nothing in a source tree may be reused", res.Unchanged)
	}

	lnpmPath := filepath.Join(projectPath, ".lnpm", "my-package")
	got, err := os.ReadFile(filepath.Join(lnpmPath, "dist", "index.js"))
	if err != nil {
		t.Fatalf("failed to read relinked dist/index.js: %v", err)
	}
	if string(got) != published["dist/index.js"] {
		t.Errorf("relinked dist/index.js = %q, want the published content", string(got))
	}

	linked, err := os.Stat(filepath.Join(lnpmPath, "dist", "index.js"))
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.Stat(filepath.Join(sourcePath, "dist", "index.js"))
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(linked, source) {
		t.Error("the relinked dist/index.js is the same file as the one in the source tree, so an edit through node_modules would rewrite the author's source")
	}
}

// TestLink_FailedIncrementalRelinkLeavesPreviousPackageIntact is acceptance
// criterion 4 for the incremental path. The guarantee itself predates this:
// Link has staged into a temp directory and swapped it in since #137, so the
// live package is never touched until the replacement is complete. What this
// pins is that carrying unchanged files over does not turn the rebuild into an
// in-place update, which would let a failed relink leave a package mixing the
// old and new content of the same file.
func TestLink_FailedIncrementalRelinkLeavesPreviousPackageIntact(t *testing.T) {
	tmpDir := t.TempDir()
	projectPath := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(projectPath, 0755); err != nil {
		t.Fatal(err)
	}

	original := map[string]string{
		"package.json":  `{"name":"my-package","version":"1.0.0"}`,
		"dist/index.js": "module.exports = 1;\n",
		"dist/utils.js": "module.exports = 2;\n",
	}

	v1 := filepath.Join(tmpDir, "store", "v1")
	files1 := writeHashedStoreFiles(t, v1, original)

	linker := New(projectPath)
	if _, err := linker.Link("my-package", v1, files1); err != nil {
		t.Fatalf("first Link() error: %v", err)
	}

	// v2 changes dist/utils.js and leaves the rest reusable, but the store is
	// missing a file the package declares, so the relink fails part way through.
	v2 := filepath.Join(tmpDir, "store", "v2")
	files2 := writeHashedStoreFiles(t, v2, map[string]string{
		"package.json":  `{"name":"my-package","version":"1.0.0"}`,
		"dist/index.js": "module.exports = 1;\n",
		"dist/utils.js": "module.exports = 99;\n",
	})
	files2 = append(files2, &pack.FileInfo{
		Path:        filepath.Join(v2, "dist", "missing.js"),
		RelPath:     "dist/missing.js",
		Size:        10,
		Mode:        0644,
		ContentHash: "0000000000000000",
	})

	if _, err := linker.Link("my-package", v2, files2); err == nil {
		t.Fatal("second Link() succeeded, want error: the failure injection did not reach the error path")
	}

	lnpmPath := filepath.Join(projectPath, ".lnpm", "my-package")
	for rel, want := range original {
		got, err := os.ReadFile(filepath.Join(lnpmPath, filepath.FromSlash(rel)))
		if err != nil {
			t.Errorf("previously linked %s missing after a failed incremental relink: %v", rel, err)
			continue
		}
		if string(got) != want {
			t.Errorf("previously linked %s = %q, want %q", rel, string(got), want)
		}
	}
	if _, err := os.Stat(filepath.Join(lnpmPath, "dist", "missing.js")); !os.IsNotExist(err) {
		t.Errorf("failed relink leaked dist/missing.js into %s", lnpmPath)
	}
	assertNoTempDirs(t, filepath.Join(projectPath, ".lnpm"))
}

// TestLink_DoesNotLinkAPackageFileAtTheManifestsName covers the name collision.
// The name belongs to the linker, so a package that ships a file at it does not
// see that file in .lnpm/{package}: what sits there is the record of the link,
// exactly as the store keeps its own completeness marker out of what it hands to
// consumers.
//
// Reserving the name rather than yielding it is what makes the collision a
// question that cannot be asked. One path cannot hold two files, and a linked
// package whose .lnpm-linked is sometimes the linker's record and sometimes
// content that merely looks like one leaves nothing on the way back in able to
// tell which it is reading.
//
// The rest of the package links normally, and because the manifest is the
// linker's on every link, the relink after it reuses like any other.
func TestLink_DoesNotLinkAPackageFileAtTheManifestsName(t *testing.T) {
	tmpDir := t.TempDir()
	projectPath := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(projectPath, 0755); err != nil {
		t.Fatal(err)
	}
	lnpmPath := filepath.Join(projectPath, ".lnpm", "my-package")

	storePath := filepath.Join(tmpDir, "store", "my-package")
	files := writeHashedStoreFiles(t, storePath, map[string]string{
		"package.json":  `{"name":"my-package","version":"1.0.0"}`,
		"dist/index.js": "module.exports = 1;\n",
		manifestName:    "this file belongs to the package\n",
	})

	linker := New(projectPath)
	res, err := linker.Link("my-package", storePath, files)
	if err != nil {
		t.Fatalf("first Link() error: %v", err)
	}
	if res.Changed != 2 || res.Unchanged != 0 {
		t.Errorf("Link() reported %d changed / %d unchanged, want 2 / 0: the reserved name is not one of the package's files", res.Changed, res.Unchanged)
	}

	// What is at the reserved path is the link's own manifest, not the bytes the
	// package shipped there.
	raw, err := os.ReadFile(filepath.Join(lnpmPath, manifestName))
	if err != nil {
		t.Fatalf("failed to read linked %s: %v", manifestName, err)
	}
	if string(raw) == "this file belongs to the package\n" {
		t.Fatalf("linked %s holds the package's own content: the name was not reserved", manifestName)
	}
	m := readManifest(lnpmPath)
	if m == nil {
		t.Fatalf("linked %s does not read as a manifest: %s", manifestName, raw)
	}
	if len(m.Files) != 2 {
		t.Errorf("the manifest describes %d files, want the 2 the package actually ships", len(m.Files))
	}
	if _, ok := m.Files[manifestName]; ok {
		t.Errorf("the manifest describes %s as one of the package's files", manifestName)
	}

	// The package's other files are linked, and the file it shipped at the
	// reserved name is simply absent, as the store's marker is.
	if got, err := os.ReadFile(filepath.Join(lnpmPath, "dist", "index.js")); err != nil || string(got) != "module.exports = 1;\n" {
		t.Errorf("dist/index.js = %q (err %v), want the store's content", string(got), err)
	}

	// Reuse is not lost to the collision the way it was when the package's file
	// won the path.
	again, err := linker.Link("my-package", storePath, files)
	if err != nil {
		t.Fatalf("second Link() error: %v", err)
	}
	if again.Unchanged != 2 {
		t.Errorf("relink reported %d unchanged, want 2: reserving the name keeps the manifest readable across links", again.Unchanged)
	}
}

// TestLink_UnchangedPackageReportsTheTypeItWasBuiltWith pins what Result.Type
// means on the path that materialises nothing.
//
// Every other exit reports what the workers achieved, upgrading to a hard link
// where a reflink landed and downgrading to a copy where hard linking would not.
// Reporting determineLinkType's prediction instead would make the type turn over
// with the configuration rather than with the tree: a caller records it in the
// database link row and prints it, so re-running `add` against an already
// current package would rewrite the recorded type with nothing having changed.
func TestLink_UnchangedPackageReportsTheTypeItWasBuiltWith(t *testing.T) {
	tmpDir := t.TempDir()
	projectPath := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(projectPath, 0755); err != nil {
		t.Fatal(err)
	}

	storePath := filepath.Join(tmpDir, "store", "my-package")
	files := writeHashedStoreFiles(t, storePath, map[string]string{
		"package.json":  `{"name":"my-package","version":"1.0.0"}`,
		"dist/index.js": "module.exports = 1;\n",
	})

	linker := New(projectPath)
	built, err := linker.Link("my-package", storePath, files)
	if err != nil {
		t.Fatalf("first Link() error: %v", err)
	}
	if built.Type != HardLink {
		t.Fatalf("first Link() built the package with %q, want %q: the test needs a tree the configuration below disagrees with", built.Type, HardLink)
	}

	// Ask for copies from here on. Nothing about the linked package changes, so
	// the relink still has nothing to do.
	configPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("link_mode: copy\n"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LNPM_CONFIG", configPath)
	config.ResetForTesting()
	t.Cleanup(config.ResetForTesting)

	res, err := linker.Link("my-package", storePath, files)
	if err != nil {
		t.Fatalf("second Link() error: %v", err)
	}
	if res.Unchanged != len(files) {
		t.Fatalf("second Link() reported %d unchanged of %d, want all of them: the test needs the path that materialises nothing", res.Unchanged, len(files))
	}
	if res.Type != built.Type {
		t.Errorf("Link() reported type %q for a package it did not touch, want %q: the tree is still the one the first link built", res.Type, built.Type)
	}
}

// TestLink_APackageCannotDescribeItsOwnRelink covers the transition out of the
// collision, which is where yielding the name gave way.
//
// A version ships a file at the manifest's path whose bytes are a manifest
// naming the hashes the next version will have, and the version after it drops
// the file. If the package's file could occupy that path, the relink would read
// it, find every path already accounted for, and hand the consumer the previous
// version's bytes while everything else reported the new one.
//
// The forged manifest records the linked package's real location, so the test
// pins the reservation rather than the fact that a published file cannot usually
// guess where it will be linked. Reserving the name means the file never reaches
// .lnpm/{package} at all and the question of trusting it never arises.
func TestLink_APackageCannotDescribeItsOwnRelink(t *testing.T) {
	tmpDir := t.TempDir()
	projectPath := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(projectPath, 0755); err != nil {
		t.Fatal(err)
	}
	lnpmPath := filepath.Join(projectPath, ".lnpm", "my-package")
	// .lnpm up front, so the forged manifest below names the location the same
	// way a read of it will: manifestPath resolves the links along the parent,
	// which it can only do once the parent is there. Without this the forgery
	// would be refused for naming the wrong place - on macOS, where the temp
	// directory reaches /private/var through a link - rather than for being a
	// forgery, which is what this test is about.
	if err := os.MkdirAll(filepath.Dir(lnpmPath), 0755); err != nil {
		t.Fatal(err)
	}

	// v2 first, so v1 can ship a manifest naming its hashes.
	v2 := filepath.Join(tmpDir, "store", "v2")
	files2 := writeHashedStoreFiles(t, v2, map[string]string{
		"package.json":  `{"name":"my-package","version":"2.0.0"}`,
		"dist/index.js": "module.exports = 'v2';\n",
	})

	forged := linkManifest{
		SchemaVersion: manifestSchemaVersion,
		Path:          manifestPath(lnpmPath),
		LinkType:      HardLink,
		Files:         map[string]linkedFile{},
	}
	for _, f := range files2 {
		forged.Files[f.RelPath] = linkedFile{Hash: f.ContentHash, Mode: f.Mode}
	}
	payload, err := json.Marshal(forged)
	if err != nil {
		t.Fatal(err)
	}

	v1 := filepath.Join(tmpDir, "store", "v1")
	files1 := writeHashedStoreFiles(t, v1, map[string]string{
		"package.json":  `{"name":"my-package","version":"1.0.0"}`,
		"dist/index.js": "module.exports = 'v1';\n",
		manifestName:    string(payload),
	})

	linker := New(projectPath)
	if _, err := linker.Link("my-package", v1, files1); err != nil {
		t.Fatalf("first Link() error: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(lnpmPath, manifestName)); err != nil || string(got) == string(payload) {
		t.Fatalf("the package's own %s occupies the reserved path (err = %v)", manifestName, err)
	}

	res, err := linker.Link("my-package", v2, files2)
	if err != nil {
		t.Fatalf("second Link() error: %v", err)
	}
	if res.Unchanged != 0 {
		t.Errorf("Link() reported %d unchanged, want 0: v1 and v2 share no file", res.Unchanged)
	}

	got, err := os.ReadFile(filepath.Join(lnpmPath, "dist", "index.js"))
	if err != nil {
		t.Fatalf("failed to read relinked dist/index.js: %v", err)
	}
	if string(got) != "module.exports = 'v2';\n" {
		t.Errorf("relinked dist/index.js = %q, want v2's content: the consumer kept the previous version's bytes", string(got))
	}
	if _, err := os.Stat(filepath.Join(lnpmPath, manifestName)); err != nil {
		t.Errorf("the linked package carries no %s after the relink: %v", manifestName, err)
	}
}

// TestLink_ReusesWhenTheProjectIsReachedByAnotherSpellingOfItsPath pins the
// manifest's self-location check to the directory rather than to the string a
// caller happened to name it by.
//
// The commands do not agree on that string. add, pull, remove and restore build
// their linker from the working directory; push and publish build theirs from
// the project path the database recorded, which is stored through
// db.normalizePath and so has its symlinks resolved. Every spelling names the
// same directory, and a relink that rejects the manifest because the previous
// command spelled the path differently rewrites the whole package - which is
// what add-then-push did on Windows, where the working directory keeps an 8.3
// short name that the database's normalization expands.
func TestLink_ReusesWhenTheProjectIsReachedByAnotherSpellingOfItsPath(t *testing.T) {
	tmpDir := t.TempDir()
	realDir := filepath.Join(tmpDir, "real")
	projectPath := filepath.Join(realDir, "project")
	if err := os.MkdirAll(projectPath, 0755); err != nil {
		t.Fatal(err)
	}

	// A symlinked parent is this platform's version of the same thing: two
	// paths, one directory, and only one of them what EvalSymlinks returns.
	aliasDir := filepath.Join(tmpDir, "alias")
	if err := os.Symlink(realDir, aliasDir); err != nil {
		t.Skipf("cannot create a symlink to reach the project by a second path: %v", err)
	}

	storePath := filepath.Join(tmpDir, "store", "my-package")
	files := writeHashedStoreFiles(t, storePath, map[string]string{
		"package.json":  `{"name":"my-package","version":"1.0.0"}`,
		"dist/index.js": "module.exports = 1;\n",
		"dist/utils.js": "module.exports = 2;\n",
	})

	if _, err := New(projectPath).Link("my-package", storePath, files); err != nil {
		t.Fatalf("first Link() error: %v", err)
	}

	lnpmPath := filepath.Join(projectPath, ".lnpm", "my-package")
	before := statLinked(t, lnpmPath, "package.json", "dist/index.js", "dist/utils.js")

	res, err := New(filepath.Join(aliasDir, "project")).Link("my-package", storePath, files)
	if err != nil {
		t.Fatalf("second Link() error: %v", err)
	}
	if res.Changed != 0 || res.Unchanged != 3 {
		t.Errorf("Link() through a second spelling of the project path reported %d changed / %d unchanged, want 0 / 3: the manifest the first link wrote describes this directory", res.Changed, res.Unchanged)
	}

	after := statLinked(t, lnpmPath, "package.json", "dist/index.js", "dist/utils.js")
	for rel, was := range before {
		if !os.SameFile(was, after[rel]) {
			t.Errorf("%s is a different file after relinking through a second spelling of the project path, so it was rewritten", rel)
		}
	}
}
