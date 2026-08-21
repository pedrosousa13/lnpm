package link

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/config"
	"github.com/pedrosousa13/lnpm/internal/pack"
)

// storeFixture creates a fresh store directory and populates it, returning the
// store path plus the matching []*pack.FileInfo in the same shape Link receives
// from Store.GetFiles. The population itself is writeStoreFiles, from
// link_atomic_test.go in this package.
func storeFixture(t *testing.T, root string, contents map[string]string) (string, []*pack.FileInfo) {
	t.Helper()

	storePath := filepath.Join(root, "store", "my-package", "abc123")
	if err := os.MkdirAll(storePath, 0755); err != nil {
		t.Fatal(err)
	}
	return storePath, writeStoreFiles(t, storePath, contents)
}

// linkDirAt points linkPath at target the way the linker itself points
// .lnpm/{package} at a source directory: a symlink where the platform allows
// one, a junction on Windows where it does not. That is what a hostile checkout
// gets to commit, and a junction is the case a plain os.ModeSymlink test would
// miss.
//
// Skipping rather than failing when neither can be created keeps the test honest
// on a Windows runner holding no symlink privilege: the guard is not exercised
// there, and saying so beats pretending it passed.
func linkDirAt(t *testing.T, target, linkPath string) {
	t.Helper()
	if err := createDirSymlink(target, linkPath); err != nil {
		t.Skipf("cannot create a directory link at %s: %v", linkPath, err)
	}
}

func assertNoSymlink(t *testing.T, projectPath, packageName string) {
	t.Helper()
	nodeModulesPath := filepath.Join(projectPath, "node_modules", packageName)
	if _, err := os.Lstat(nodeModulesPath); !os.IsNotExist(err) {
		t.Errorf("node_modules/%s exists after a failed Link (Lstat err = %v), want it absent", packageName, err)
	}
}

// TestLinkMissingStoreFile drives the failure path where the store changes
// between the caller listing its files and the workers materialising them.
//
// Exactly one store file is removed, so exactly one worker can fail. That
// matters because the error channels hold a single slot: if several files could
// fail, any one of their errors could win the slot and an assertion naming a
// specific file would be flaky.
//
// A missing source also drives the copy fallback: reflink cannot open the file,
// the hard link fails, so the file is queued into filesToCopy and the error
// surfaces through the copy pass's error channel.
func TestLinkMissingStoreFile(t *testing.T) {
	tmpDir := t.TempDir()
	projectPath := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(projectPath, 0755); err != nil {
		t.Fatal(err)
	}

	contents := map[string]string{
		"package.json":  `{"name":"my-package"}`,
		"dist/index.js": "index contents",
		"dist/utils.js": "utils contents",
	}
	storePath, files := storeFixture(t, tmpDir, contents)

	// Delete after the slice is built, before Link: the store no longer matches
	// what the caller listed.
	missing := filepath.Join(storePath, "dist", "utils.js")
	if err := os.Remove(missing); err != nil {
		t.Fatal(err)
	}

	linker := New(projectPath)
	_, err := linker.Link("my-package", storePath, files)
	if err == nil {
		t.Fatal("Link() succeeded with a missing store file, want an error")
	}
	// Assert this is the right error, not merely some error: it must name the
	// file that went missing and it must be a not-exist failure.
	if !strings.Contains(err.Error(), "dist/utils.js") {
		t.Errorf("Link() error = %v, want it to name dist/utils.js", err)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Link() error = %v, want a not-exist error", err)
	}

	// The symlink is created only after every file is in place, so a failed
	// Link must not leave node_modules pointing at anything.
	assertNoSymlink(t, projectPath, "my-package")

	// Link populates a temp directory and renames it into place, so a failure
	// leaves no half-populated .lnpm/my-package at all — not even the partially
	// populated directory an in-place implementation would leave behind — and no
	// abandoned temp directory either.
	lnpmPath := filepath.Join(projectPath, ".lnpm", "my-package")
	if _, err := os.Lstat(lnpmPath); !os.IsNotExist(err) {
		t.Errorf(".lnpm/my-package exists after a failed Link (Lstat err = %v), want it absent", err)
	}
	if names := entryNames(t, filepath.Join(projectPath, ".lnpm")); len(names) != 0 {
		t.Errorf(".lnpm entries after a failed Link = %v, want none", names)
	}
	if linker.IsLinked("my-package") {
		t.Error("IsLinked(my-package) = true after a failed Link, want false")
	}

	// Repair the store and retry: the retry must succeed and produce a complete
	// package, including the file that was missing the first time.
	if err := os.WriteFile(missing, []byte(contents["dist/utils.js"]), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := linker.Link("my-package", storePath, files); err != nil {
		t.Fatalf("Link() after repairing the store: %v", err)
	}

	for rel, want := range contents {
		got, err := os.ReadFile(filepath.Join(lnpmPath, filepath.FromSlash(rel)))
		if err != nil {
			t.Errorf("reading linked %s: %v", rel, err)
			continue
		}
		if string(got) != want {
			t.Errorf("linked %s = %q, want %q", rel, string(got), want)
		}
	}

	// The retry must not have inherited leftovers from the failed attempt.
	if names := entryNames(t, filepath.Join(projectPath, ".lnpm")); len(names) != 1 || names[0] != "my-package" {
		t.Errorf(".lnpm entries after retry = %v, want [my-package]", names)
	}

	nodeModulesPath := filepath.Join(projectPath, "node_modules", "my-package")
	info, err := os.Lstat(nodeModulesPath)
	if err != nil {
		t.Fatalf("node_modules/my-package not created by the retry: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode()&os.ModeSymlink == 0 {
		t.Error("node_modules/my-package is not a symlink after the retry")
	}
}

// useCopyMode points config at a temp file selecting link_mode: copy and resets
// the memoised config so the setting takes effect regardless of what other
// tests in this package loaded first (config.ResetForTesting, added for exactly
// this, removes the ordering dependency the singleton would otherwise create).
func useCopyMode(t *testing.T) {
	t.Helper()

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("link_mode: copy\n"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LNPM_CONFIG", configPath)
	config.ResetForTesting()
	// Runs before t.Setenv's restore (cleanups are LIFO), so the next test sees
	// both a cleared singleton and the original environment.
	t.Cleanup(config.ResetForTesting)

	if got := New(t.TempDir()).determineLinkType(t.TempDir()); got != Copy {
		t.Fatalf("determineLinkType() = %q with link_mode: copy, want %q", got, Copy)
	}
}

// TestLinkCopyMode exercises the copy fallback's success path: with
// link_mode: copy no hard link is attempted, so every file reaches the
// filesToCopy queue and is materialised by copyFile.
//
// Reflink is still attempted first (and succeeds on APFS/Btrfs/XFS), so the
// assertions key on outcomes rather than on which syscall ran: the returned
// LinkType is reported as a hard link whenever a reflink succeeded, and both a
// reflink and a plain copy give the linked file its own inode and its own
// bytes. What both outcomes rule out — and what a hard link would not — is the
// linked file sharing storage with the store entry.
func TestLinkCopyMode(t *testing.T) {
	useCopyMode(t)

	tmpDir := t.TempDir()
	projectPath := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(projectPath, 0755); err != nil {
		t.Fatal(err)
	}

	contents := map[string]string{
		"package.json":  `{"name":"my-package"}`,
		"dist/index.js": "index contents",
	}
	storePath, files := storeFixture(t, tmpDir, contents)

	linker := New(projectPath)
	// The reported LinkType is deliberately discarded rather than asserted: Link
	// reports HardLink whenever a reflink succeeded, which it does on APFS and
	// Btrfs, so the value describes the filesystem rather than the config. That
	// the config was honoured is already pinned by useCopyMode's
	// determineLinkType check; what remains to prove here is the outcome.
	if _, err := linker.Link("my-package", storePath, files); err != nil {
		t.Fatalf("Link() in copy mode: %v", err)
	}

	lnpmPath := filepath.Join(projectPath, ".lnpm", "my-package")
	for rel, want := range contents {
		got, err := os.ReadFile(filepath.Join(lnpmPath, filepath.FromSlash(rel)))
		if err != nil {
			t.Errorf("reading linked %s: %v", rel, err)
			continue
		}
		if string(got) != want {
			t.Errorf("linked %s = %q, want %q", rel, string(got), want)
		}
	}

	storeFile := filepath.Join(storePath, "package.json")
	linkedFile := filepath.Join(lnpmPath, "package.json")

	if runtime.GOOS != "windows" {
		storeInfo, err := os.Stat(storeFile)
		if err != nil {
			t.Fatal(err)
		}
		linkedInfo, err := os.Stat(linkedFile)
		if err != nil {
			t.Fatal(err)
		}
		if os.SameFile(storeInfo, linkedInfo) {
			t.Error("linked file shares an inode with the store entry in copy mode, want independent storage")
		}
	}

	// Independent storage, checked through behaviour rather than inode numbers
	// so this also holds on Windows: rewriting the store entry must not change
	// what the project sees.
	if err := os.WriteFile(storeFile, []byte("mutated in the store"), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(linkedFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != contents["package.json"] {
		t.Errorf("linked package.json = %q after mutating the store entry, want the original %q", string(got), contents["package.json"])
	}
}

// TestLinkCopyModeUnreadableStoreFile drives the copy pass's open error.
//
// It runs in copy mode deliberately. In the default (hard link) mode an
// unreadable source is not a failure at all: a hard link needs no read
// permission on its target, so os.Link of an unreadable-but-present file
// succeeds and the link pass never falls back to copying — the same test
// written in auto mode would assert nothing. In copy mode no hard link is
// attempted, so an unreadable source can only reach copyFile, which fails
// deterministically on open.
func TestLinkCopyModeUnreadableStoreFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod 0000 does not deny reads on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root: mode 0000 does not deny reads")
	}

	useCopyMode(t)

	tmpDir := t.TempDir()
	projectPath := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(projectPath, 0755); err != nil {
		t.Fatal(err)
	}

	contents := map[string]string{
		"package.json":  `{"name":"my-package"}`,
		"dist/index.js": "index contents",
		"dist/utils.js": "utils contents",
	}
	storePath, files := storeFixture(t, tmpDir, contents)

	// Exactly one unreadable file, so exactly one worker can fail and the
	// single-slot error channel can only carry that file's error.
	unreadable := filepath.Join(storePath, "dist", "utils.js")
	if err := os.Chmod(unreadable, 0000); err != nil {
		t.Fatal(err)
	}
	// Restore before t.TempDir's cleanup walks the tree.
	t.Cleanup(func() { _ = os.Chmod(unreadable, 0644) })

	linker := New(projectPath)
	if _, err := linker.Link("my-package", storePath, files); err == nil {
		t.Fatal("Link() succeeded with an unreadable store file, want an error")
	} else {
		if !strings.Contains(err.Error(), "dist/utils.js") {
			t.Errorf("Link() error = %v, want it to name dist/utils.js", err)
		}
		if !errors.Is(err, fs.ErrPermission) {
			t.Errorf("Link() error = %v, want a permission error", err)
		}
	}

	assertNoSymlink(t, projectPath, "my-package")
	if _, err := os.Lstat(filepath.Join(projectPath, ".lnpm", "my-package")); !os.IsNotExist(err) {
		t.Errorf(".lnpm/my-package exists after a failed Link (Lstat err = %v), want it absent", err)
	}
}

// TestUnlinkRemovesEmptyLnpmDir pins both sides of Unlink's cleanup branch: the
// .lnpm directory survives while another package is still linked, and is
// removed once the last one goes.
func TestUnlinkRemovesEmptyLnpmDir(t *testing.T) {
	tmpDir := t.TempDir()
	projectPath := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(projectPath, 0755); err != nil {
		t.Fatal(err)
	}

	linker := New(projectPath)
	for _, name := range []string{"pkg-a", "pkg-b"} {
		storePath := filepath.Join(tmpDir, "store", name)
		if err := os.MkdirAll(storePath, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(storePath, "package.json"), []byte(`{}`), 0644); err != nil {
			t.Fatal(err)
		}
		files := []*pack.FileInfo{{RelPath: "package.json", Size: 2, Mode: 0644}}
		if _, err := linker.Link(name, storePath, files); err != nil {
			t.Fatalf("Link(%s): %v", name, err)
		}
	}

	lnpmDir := filepath.Join(projectPath, ".lnpm")

	if err := linker.Unlink("pkg-a"); err != nil {
		t.Fatalf("Unlink(pkg-a): %v", err)
	}
	if _, err := os.Stat(lnpmDir); err != nil {
		t.Fatalf(".lnpm removed while pkg-b is still linked: %v", err)
	}
	if names := entryNames(t, lnpmDir); len(names) != 1 || names[0] != "pkg-b" {
		t.Errorf(".lnpm entries = %v, want [pkg-b]", names)
	}

	if err := linker.Unlink("pkg-b"); err != nil {
		t.Fatalf("Unlink(pkg-b): %v", err)
	}
	if _, err := os.Stat(lnpmDir); !os.IsNotExist(err) {
		t.Errorf(".lnpm still exists after unlinking the last package (Stat err = %v), want it removed", err)
	}
}

// TestUnlinkScopedPackageRemovesEmptyScopeDirectories is the scoped counterpart
// of TestUnlinkRemovesEmptyLnpmDir. A scoped package leaves a scope directory
// behind under both .lnpm and node_modules, and until that goes the empty-.lnpm
// cleanup above can never fire for a scoped package.
func TestUnlinkScopedPackageRemovesEmptyScopeDirectories(t *testing.T) {
	tmpDir := t.TempDir()
	projectPath := filepath.Join(tmpDir, "project")

	linker := New(projectPath)
	linkPackage(t, linker, filepath.Join(tmpDir, "store"), "@org/my-package")

	if err := linker.Unlink("@org/my-package"); err != nil {
		t.Fatalf("Unlink() error: %v", err)
	}

	assertNotExist(t, filepath.Join(projectPath, ".lnpm", "@org"))
	assertNotExist(t, filepath.Join(projectPath, "node_modules", "@org"))
	// With the scope directory gone, the empty-.lnpm cleanup fires.
	assertNotExist(t, filepath.Join(projectPath, ".lnpm"))
	assertLinked(t, linker)
}

// TestLinkSourceRejectsUnusableSource drives LinkSource's guards on the source
// directory recorded when the package was published. That directory can have
// been deleted or replaced since, and linking .lnpm/{package} at it anyway
// would defer the failure to whatever tool next resolved node_modules.
func TestLinkSourceRejectsUnusableSource(t *testing.T) {
	tmpDir := t.TempDir()
	projectPath := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(projectPath, 0755); err != nil {
		t.Fatal(err)
	}

	sourceFile := filepath.Join(tmpDir, "a-file")
	if err := os.WriteFile(sourceFile, []byte("not a package"), 0644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		source  string
		wantErr string
	}{
		{"missing source", filepath.Join(tmpDir, "deleted-source"), "is not available"},
		{"source is a file", sourceFile, "is not a directory"},
		// A package row can carry no source path at all. filepath.Abs("")
		// resolves to the working directory, which stats as a directory, so
		// without an explicit guard this links .lnpm/{package} at whatever
		// directory the consumer happened to run lnpm from.
		{"empty source", "", "no recorded source directory"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			linker := New(projectPath)

			_, err := linker.LinkSource("my-package", tc.source)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("LinkSource(%s) error = %v, want one containing %q", tc.source, err, tc.wantErr)
			}

			assertNoSymlink(t, projectPath, "my-package")
			if _, err := os.Lstat(filepath.Join(projectPath, ".lnpm", "my-package")); !os.IsNotExist(err) {
				t.Errorf(".lnpm/my-package exists after a failed LinkSource (Lstat err = %v), want it absent", err)
			}
		})
	}
}

// TestLinkSourceKeepsPreviousOnFailure pins LinkSource's rollback. The previous
// .lnpm/{package} is a store copy here - the copy-to-live conversion, which is
// the case Link's comment names, because deleting a whole package tree is not
// instantaneous - and it must survive a failure to create the replacement link
// instead of being cleared up front with nothing left to restore.
func TestLinkSourceKeepsPreviousOnFailure(t *testing.T) {
	tmpDir := t.TempDir()
	projectPath := filepath.Join(tmpDir, "project")
	sourcePath := filepath.Join(tmpDir, "source")
	for _, dir := range []string{projectPath, sourcePath} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}

	contents := map[string]string{
		"package.json":  `{"name":"my-package"}`,
		"dist/index.js": "index contents",
	}
	storePath, files := storeFixture(t, tmpDir, contents)

	linker := New(projectPath)
	if _, err := linker.Link("my-package", storePath, files); err != nil {
		t.Fatalf("Link(): %v", err)
	}

	// Force the replacement link's creation to fail. Nothing else about the
	// project is disturbed, so the only reason .lnpm/my-package could go missing
	// is LinkSource having removed it before creating the replacement.
	forced := errors.New("forced link failure")
	original := createDirSymlinkFn
	createDirSymlinkFn = func(string, string) error { return forced }
	t.Cleanup(func() { createDirSymlinkFn = original })

	if _, err := linker.LinkSource("my-package", sourcePath); !errors.Is(err, forced) {
		t.Fatalf("LinkSource() error = %v, want one wrapping the forced failure", err)
	}

	lnpmPath := filepath.Join(projectPath, ".lnpm", "my-package")
	info, err := os.Lstat(lnpmPath)
	if err != nil {
		t.Fatalf(".lnpm/my-package is gone after a failed LinkSource: %v", err)
	}
	if !info.IsDir() {
		t.Errorf(".lnpm/my-package mode = %v after a failed LinkSource, want the previous store copy", info.Mode())
	}
	for rel, want := range contents {
		got, err := os.ReadFile(filepath.Join(lnpmPath, filepath.FromSlash(rel)))
		if err != nil {
			t.Errorf("reading %s from the preserved package: %v", rel, err)
			continue
		}
		if string(got) != want {
			t.Errorf("preserved %s = %q, want %q", rel, string(got), want)
		}
	}

	// No abandoned temp link either.
	if names := entryNames(t, filepath.Join(projectPath, ".lnpm")); len(names) != 1 || names[0] != "my-package" {
		t.Errorf(".lnpm entries after a failed LinkSource = %v, want [my-package]", names)
	}
}

// TestIsLiveLinkedRejectsNonLinks pins what IsLiveLinked accepts. pull and push
// skip a live-linked package and report the skip as success, so anything at
// .lnpm/{package} that is not a link - a stray file left by a crashed tool, say
// - must not be taken for one: that would report a corrupt project as fine.
func TestIsLiveLinkedRejectsNonLinks(t *testing.T) {
	tmpDir := t.TempDir()
	projectPath := filepath.Join(tmpDir, "project")
	lnpmDir := filepath.Join(projectPath, ".lnpm")
	sourcePath := filepath.Join(tmpDir, "source")
	for _, dir := range []string{lnpmDir, sourcePath} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}

	linker := New(projectPath)

	if linker.IsLiveLinked("absent") {
		t.Error("IsLiveLinked(absent) = true, want false")
	}

	if err := os.MkdirAll(filepath.Join(lnpmDir, "a-copy"), 0755); err != nil {
		t.Fatal(err)
	}
	if linker.IsLiveLinked("a-copy") {
		t.Error("IsLiveLinked(a-copy) = true for a store copy, want false")
	}

	if err := os.WriteFile(filepath.Join(lnpmDir, "a-file"), []byte("stray"), 0644); err != nil {
		t.Fatal(err)
	}
	if linker.IsLiveLinked("a-file") {
		t.Error("IsLiveLinked(a-file) = true for a regular file, want false")
	}

	if _, err := linker.LinkSource("a-link", sourcePath); err != nil {
		t.Fatalf("LinkSource(a-link): %v", err)
	}
	if !linker.IsLiveLinked("a-link") {
		t.Error("IsLiveLinked(a-link) = false for a live link, want true")
	}

	// A live link whose source has since been deleted is still a live link:
	// relinking it from the store would still end the live updates silently.
	if err := os.RemoveAll(sourcePath); err != nil {
		t.Fatal(err)
	}
	if !linker.IsLiveLinked("a-link") {
		t.Error("IsLiveLinked(a-link) = false for a dangling live link, want true")
	}
}

// TestLinkSourceRejectsTraversingName asserts that LinkSource validates the
// package name before joining it into .lnpm and node_modules, exactly as Link
// does: the name reaches both from the store, and a traversing one would place
// a link outside the project.
func TestLinkSourceRejectsTraversingName(t *testing.T) {
	tmpDir := t.TempDir()
	projectPath := filepath.Join(tmpDir, "project")
	sourcePath := filepath.Join(tmpDir, "source")
	for _, dir := range []string{projectPath, sourcePath} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}

	linker := New(projectPath)
	if _, err := linker.LinkSource("../escaped", sourcePath); err == nil {
		t.Fatal("LinkSource(../escaped) error = nil, want a validation error")
	}

	if _, err := os.Lstat(filepath.Join(tmpDir, "escaped")); !os.IsNotExist(err) {
		t.Errorf("a link was created outside the project (Lstat err = %v), want none", err)
	}
}

// TestLinkRefusesASymlinkedLnpmDirectory covers the ancestor pack's name
// validation does not reach. A repository can commit .lnpm as a symlink pointing
// anywhere - .gitignore does not stop a tracked symlink from being checked out -
// and every path the linker builds under it then lands outside the project.
func TestLinkRefusesASymlinkedLnpmDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	projectPath := filepath.Join(tmpDir, "project")
	outside := filepath.Join(tmpDir, "outside")
	for _, dir := range []string{projectPath, outside} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	linkDirAt(t, outside, filepath.Join(projectPath, ".lnpm"))

	storePath, files := storeFixture(t, tmpDir, map[string]string{
		"package.json":  `{"name":"my-package"}`,
		"dist/index.js": "index contents",
	})

	_, err := New(projectPath).Link("my-package", storePath, files)
	if err == nil {
		t.Fatal("Link() through a symlinked .lnpm error = nil, want a refusal")
	}
	if !strings.Contains(err.Error(), ".lnpm") {
		t.Errorf("Link() error = %v, want it to name .lnpm", err)
	}

	// The refusal has to be effective, not merely reported: an error raised after
	// the tree was materialised through the link would leave the same files
	// outside the project as no check at all.
	entries, readErr := os.ReadDir(outside)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Errorf("the directory outside the project holds %d entries after a refused Link, want it untouched", len(entries))
	}
	assertNoSymlink(t, projectPath, "my-package")
}

// TestLinkSourceRefusesASymlinkedLnpmDirectory is the same hole reached through
// the live-link path, which writes .lnpm/{package} directly rather than through
// a temp directory.
func TestLinkSourceRefusesASymlinkedLnpmDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	projectPath := filepath.Join(tmpDir, "project")
	sourcePath := filepath.Join(tmpDir, "source")
	outside := filepath.Join(tmpDir, "outside")
	for _, dir := range []string{projectPath, sourcePath, outside} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	linkDirAt(t, outside, filepath.Join(projectPath, ".lnpm"))

	_, err := New(projectPath).LinkSource("demo-pkg", sourcePath)
	if err == nil {
		t.Fatal("LinkSource() through a symlinked .lnpm error = nil, want a refusal")
	}
	if !strings.Contains(err.Error(), ".lnpm") {
		t.Errorf("LinkSource() error = %v, want it to name .lnpm", err)
	}

	if _, err := os.Lstat(filepath.Join(outside, "demo-pkg")); !os.IsNotExist(err) {
		t.Errorf("demo-pkg was created outside the project (Lstat err = %v), want none", err)
	}
}

// TestUnlinkRefusesASymlinkedLnpmDirectory is the destructive half. Unlink's
// RemoveAll of .lnpm/{package} follows a symlinked .lnpm, so an attacker who
// picks the package name picks a directory to delete outside the project.
func TestUnlinkRefusesASymlinkedLnpmDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	projectPath := filepath.Join(tmpDir, "project")
	victim := filepath.Join(tmpDir, "victim")
	documents := filepath.Join(victim, "Documents")
	for _, dir := range []string{projectPath, documents} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	taxes := filepath.Join(documents, "taxes.txt")
	if err := os.WriteFile(taxes, []byte("keep me"), 0644); err != nil {
		t.Fatal(err)
	}
	linkDirAt(t, victim, filepath.Join(projectPath, ".lnpm"))

	err := New(projectPath).Unlink("Documents")
	if err == nil {
		t.Fatal("Unlink() through a symlinked .lnpm error = nil, want a refusal")
	}
	if !strings.Contains(err.Error(), ".lnpm") {
		t.Errorf("Unlink() error = %v, want it to name .lnpm", err)
	}

	if got, err := os.ReadFile(taxes); err != nil || string(got) != "keep me" {
		t.Errorf("victim/Documents/taxes.txt = %q (err %v) after a refused Unlink, want it intact", string(got), err)
	}
}

// TestLinkSourceRefusesASymlinkedScopeDirectory covers the second ancestor a
// scoped name adds. A real .lnpm holding a symlinked @org redirects every scoped
// package just as completely, one level down.
func TestLinkSourceRefusesASymlinkedScopeDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	projectPath := filepath.Join(tmpDir, "project")
	sourcePath := filepath.Join(tmpDir, "source")
	victim := filepath.Join(tmpDir, "victim")
	for _, dir := range []string{filepath.Join(projectPath, ".lnpm"), sourcePath, victim} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	linkDirAt(t, victim, filepath.Join(projectPath, ".lnpm", "@org"))

	_, err := New(projectPath).LinkSource("@org/scoped", sourcePath)
	if err == nil {
		t.Fatal("LinkSource() through a symlinked scope directory error = nil, want a refusal")
	}
	if !strings.Contains(err.Error(), "@org") {
		t.Errorf("LinkSource() error = %v, want it to name the @org scope directory", err)
	}

	if _, err := os.Lstat(filepath.Join(victim, "scoped")); !os.IsNotExist(err) {
		t.Errorf("scoped was created outside the project (Lstat err = %v), want none", err)
	}
}

// TestUnlinkRefusesWhenTheLnpmDirectoryCannotBeInspected pins the direction the
// guard fails in, which is what docs/adr/0001 is about: only "the entry is not
// there" means there is nothing to refuse. Every other Lstat failure - a
// permission denied on the project directory here, an I/O error or a too-long
// name elsewhere - leaves the guard unable to tell a real directory from a link,
// and a guard that cannot tell must not wave the caller through.
//
// What this test can prove is bounded, and worth stating: an unsearchable
// project directory also blocks the RemoveAll that follows, so both the guarded
// and the unguarded build fail here. The difference it asserts is therefore
// which failure comes back - the guard refusing and naming the directory it
// could not inspect, rather than a downstream error from the work it should
// never have started. Nothing portable makes an Lstat fail while leaving the
// work that follows able to succeed.
func TestUnlinkRefusesWhenTheLnpmDirectoryCannotBeInspected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode 0000 does not deny directory traversal on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root: mode 0000 does not deny traversal")
	}

	tmpDir := t.TempDir()
	projectPath := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(filepath.Join(projectPath, ".lnpm", "my-package"), 0755); err != nil {
		t.Fatal(err)
	}

	// Unsearchable, so Lstat of anything inside it fails with EACCES rather than
	// with a not-exist error.
	if err := os.Chmod(projectPath, 0000); err != nil {
		t.Fatal(err)
	}
	// Restore before t.TempDir's cleanup walks the tree.
	t.Cleanup(func() { _ = os.Chmod(projectPath, 0755) })

	err := New(projectPath).Unlink("my-package")
	if err == nil {
		t.Fatal("Unlink() with an uninspectable .lnpm error = nil, want a refusal")
	}
	if !strings.Contains(err.Error(), "inspect") {
		t.Errorf("Unlink() error = %v, want the guard's refusal to inspect .lnpm rather than a downstream failure", err)
	}
	if !strings.Contains(err.Error(), ".lnpm") {
		t.Errorf("Unlink() error = %v, want it to name .lnpm", err)
	}
}
