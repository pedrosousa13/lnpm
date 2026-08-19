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
