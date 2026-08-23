package link

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/pack"
)

func TestLinkAndUnlink(t *testing.T) {
	// Create temp directories for store and project
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "store", "my-package", "abc123")
	projectPath := filepath.Join(tmpDir, "project")

	if err := os.MkdirAll(storePath, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(projectPath, 0755); err != nil {
		t.Fatal(err)
	}

	// Create files in store
	files := []*pack.FileInfo{
		{RelPath: "package.json", Size: 100, Mode: 0644},
		{RelPath: "dist/index.js", Size: 200, Mode: 0644},
		{RelPath: "dist/utils.js", Size: 150, Mode: 0644},
	}

	// Create actual files in store
	for _, f := range files {
		filePath := filepath.Join(storePath, f.RelPath)
		if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filePath, []byte("content"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// Create linker and link package
	linker := New(projectPath)
	res, err := linker.Link("my-package", storePath, files)
	if err != nil {
		t.Fatalf("Link() error: %v", err)
	}

	// Verify link type (should be hardlink on same filesystem)
	if res.Type != HardLink && res.Type != Copy {
		t.Errorf("linkType = %q, want hardlink or copy", res.Type)
	}

	// Verify .lnpm directory created
	lnpmPath := filepath.Join(projectPath, ".lnpm", "my-package")
	if _, err := os.Stat(lnpmPath); err != nil {
		t.Errorf(".lnpm/my-package not created: %v", err)
	}

	// Verify files exist in .lnpm
	for _, f := range files {
		linkedFile := filepath.Join(lnpmPath, f.RelPath)
		if _, err := os.Stat(linkedFile); err != nil {
			t.Errorf("linked file %s not found: %v", f.RelPath, err)
		}
	}

	// Verify node_modules symlink created
	nodeModulesPath := filepath.Join(projectPath, "node_modules", "my-package")
	info, err := os.Lstat(nodeModulesPath)
	if err != nil {
		t.Fatalf("node_modules/my-package not found: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Error("node_modules/my-package is not a symlink")
	}

	// Verify symlink points to correct location
	target, err := os.Readlink(nodeModulesPath)
	if err != nil {
		t.Fatalf("failed to read symlink: %v", err)
	}
	if runtime.GOOS == "windows" {
		// Junctions use absolute paths on Windows
		expectedAbs := filepath.Join(projectPath, ".lnpm", "my-package")
		if target != expectedAbs {
			t.Errorf("symlink target = %q, want %q", target, expectedAbs)
		}
	} else {
		expectedTarget := filepath.Join("..", ".lnpm", "my-package")
		if target != expectedTarget {
			t.Errorf("symlink target = %q, want %q", target, expectedTarget)
		}
	}

	// Test IsLinked
	if !linker.IsLinked("my-package") {
		t.Error("IsLinked(my-package) = false, want true")
	}
	if linker.IsLinked("other-package") {
		t.Error("IsLinked(other-package) = true, want false")
	}

	// Test ListLinked
	linked, err := linker.ListLinked()
	if err != nil {
		t.Fatalf("ListLinked() error: %v", err)
	}
	if len(linked) != 1 || linked[0] != "my-package" {
		t.Errorf("ListLinked() = %v, want [my-package]", linked)
	}

	// Test Unlink
	if err := linker.Unlink("my-package"); err != nil {
		t.Fatalf("Unlink() error: %v", err)
	}

	// Verify .lnpm directory removed
	if _, err := os.Stat(lnpmPath); !os.IsNotExist(err) {
		t.Error(".lnpm/my-package still exists after unlink")
	}

	// Verify node_modules symlink removed
	if _, err := os.Stat(nodeModulesPath); !os.IsNotExist(err) {
		t.Error("node_modules/my-package still exists after unlink")
	}

	// Verify IsLinked returns false
	if linker.IsLinked("my-package") {
		t.Error("IsLinked(my-package) = true after unlink, want false")
	}
}

// TestUnlinkRemovesADotPrefixedPackageLinkedBeforeItWasInvalid is the exit for
// the trap #325 would otherwise have set. Link still refuses a dot-prefixed
// name, so this seeds the entry by hand: that is exactly the shape a project
// linked before the rule already has on disk, and it is not reachable through
// Link any more by design.
//
// Without the waiver, Unlink refuses it, 'lnpm remove' reports a failure and
// leaves the lock entry, and 'lnpm remove --all' skips it on every future run —
// the package becomes permanently unremovable by lnpm.
func TestUnlinkRemovesADotPrefixedPackageLinkedBeforeItWasInvalid(t *testing.T) {
	projectPath := t.TempDir()
	const name = ".hidden-pkg"

	pkgDir := filepath.Join(projectPath, ".lnpm", name)
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "index.js"), []byte("module.exports = {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	nodeModules := filepath.Join(projectPath, "node_modules")
	if err := os.MkdirAll(nodeModules, 0755); err != nil {
		t.Fatal(err)
	}
	// The node_modules half is Unix-only: creating a symlink directly needs a
	// privilege the test process may not hold on Windows, the same reason
	// TestLinkPreservesSymlinks skips there. The .lnpm half below is what the
	// waiver is actually about and it runs on every platform.
	linkSeeded := runtime.GOOS != "windows"
	if linkSeeded {
		if err := os.Symlink(filepath.Join("..", ".lnpm", name), filepath.Join(nodeModules, name)); err != nil {
			t.Fatal(err)
		}
	}

	linker := New(projectPath)
	if err := linker.Unlink(name); err != nil {
		t.Fatalf("Unlink(%q) error: %v", name, err)
	}

	if _, err := os.Stat(pkgDir); !os.IsNotExist(err) {
		t.Errorf(".lnpm/%s still exists after unlink (stat err = %v)", name, err)
	}
	if linkSeeded {
		if _, err := os.Lstat(filepath.Join(nodeModules, name)); !os.IsNotExist(err) {
			t.Errorf("node_modules/%s still exists after unlink (stat err = %v)", name, err)
		}
	}
}

// TestUnlinkStillRefusesATraversingName pins the other half of the waiver. The
// name is joined into .lnpm/{name} for an os.RemoveAll, so if the removal path
// stopped validating at all, this deletes a directory outside the project.
func TestUnlinkStillRefusesATraversingName(t *testing.T) {
	tmpDir := t.TempDir()
	projectPath := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(filepath.Join(projectPath, ".lnpm"), 0755); err != nil {
		t.Fatal(err)
	}

	// A real directory beside the project, which the traversing name resolves to.
	victim := filepath.Join(tmpDir, "victim")
	if err := os.MkdirAll(victim, 0755); err != nil {
		t.Fatal(err)
	}

	linker := New(projectPath)
	for _, name := range []string{"../../victim", "..", "/etc", "a\\b"} {
		if err := linker.Unlink(name); err == nil {
			t.Errorf("Unlink(%q) = nil, want error", name)
		}
	}

	if _, err := os.Stat(victim); err != nil {
		t.Errorf("Unlink deleted a directory outside the project: %v", err)
	}
}

// TestScopeNamedLikeATraversalIsAcceptedOnRemovalButStaysUnderLnpm pins the
// widest consequence of the removal waiver, which is not obvious from the name
// of the rule it waives.
//
// "@../pkg" is rejected by the strict validator, but only incidentally by the
// dot rule: the "."/".." segment check does not fire, because the segment is
// "@..", not "..". So waiving the dot rule on the removal path lets it through,
// and lnpm.lock keys are attacker-controlled in any repository the developer did
// not write.
//
// It is contained because "@.." is a literal path component: Clean collapses
// "..", and "@.." is not that, so the component survives and the path stays
// under .lnpm. That is a pure path-handling property, so it is asserted here
// without touching a filesystem and runs on every platform - which matters,
// because the filesystem half below cannot run on Windows at all. The dot rule
// itself is covered in pack's name_test.go.
func TestScopeNamedLikeATraversalIsAcceptedOnRemovalButStaysUnderLnpm(t *testing.T) {
	const name = "@../pkg"

	// The asymmetry the waiver creates, which is the reason containment has to
	// be pinned at all.
	if err := pack.ValidatePackageName(name); err == nil {
		t.Errorf("ValidatePackageName(%q) = nil; this test assumes the strict form rejects it", name)
	}
	if err := pack.ValidatePackageNameForRemoval(name); err != nil {
		t.Fatalf("ValidatePackageNameForRemoval(%q) = %v, want nil", name, err)
	}

	// Exactly the two joins Unlink performs. Asserting the full expected path,
	// rather than "is under .lnpm", is what makes this fail loudly if Clean ever
	// did collapse the component: the joined path would lose "@.." and become
	// lnpmDir/pkg.
	// The expected paths are built by concatenation, not by filepath.Join. Using
	// Join on both sides would compare Clean's output with Clean's output, so a
	// Clean that did collapse "@.." would change both and the assertion would
	// pass vacuously.
	sep := string(filepath.Separator)
	lnpmDir := filepath.Join("/project", ".lnpm")

	joined := filepath.Join(lnpmDir, name)
	if want := lnpmDir + sep + "@.." + sep + "pkg"; joined != want {
		t.Errorf("Join(%q, %q) = %q, want %q", lnpmDir, name, joined, want)
	}
	if got, want := filepath.Dir(joined), lnpmDir+sep+"@.."; got != want {
		t.Errorf("Dir(%q) = %q, want %q", joined, got, want)
	}
}

// TestUnlinkContainsAScopeNamedLikeATraversal is the filesystem half of the
// property above: a real Unlink of "@../pkg" takes the entry under .lnpm and
// nothing else. It runs on Unix only.
//
// Verified by running it, not inferred - on Unix. On Windows the fixture cannot
// be built: Win32 path parsing strips trailing dots from a path component, so
// "@.." is not a creatable directory name there and MkdirAll fails with "The
// system cannot find the path specified" before the test reaches Unlink. That is
// a limitation of the fixture rather than of the production behaviour, and it
// happens to confirm the same property from the opposite direction - Windows
// will not even let a directory be named that. The path-handling assertions in
// TestScopeNamedLikeATraversalIsAcceptedOnRemovalButStaysUnderLnpm cover Windows.
func TestUnlinkContainsAScopeNamedLikeATraversal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping on Windows - Win32 path parsing strips trailing dots from a " +
			"path component, so a directory named \"@..\" cannot be created and the " +
			"fixture cannot be built (see #326 for trailing-dot names generally)")
	}

	tmpDir := t.TempDir()
	projectPath := filepath.Join(tmpDir, "project")
	const name = "@../pkg"

	pkgDir := filepath.Join(projectPath, ".lnpm", "@..", "pkg")
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		t.Fatal(err)
	}
	// A directory that a real traversal would reach: the sibling of .lnpm that
	// "@.." would name if it were collapsed rather than treated as a literal.
	victim := filepath.Join(projectPath, "victim")
	if err := os.MkdirAll(victim, 0755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(tmpDir, "outside")
	if err := os.MkdirAll(outside, 0755); err != nil {
		t.Fatal(err)
	}

	linker := New(projectPath)
	if err := linker.Unlink(name); err != nil {
		t.Fatalf("Unlink(%q) error: %v", name, err)
	}

	if _, err := os.Stat(pkgDir); !os.IsNotExist(err) {
		t.Errorf(".lnpm/@../pkg still exists after unlink (stat err = %v)", err)
	}
	for _, dir := range []string{victim, outside} {
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("Unlink(%q) removed %s, which is outside .lnpm: %v", name, dir, err)
		}
	}
}

func TestLinkScopedPackage(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "store", "@org", "my-package", "abc123")
	projectPath := filepath.Join(tmpDir, "project")

	if err := os.MkdirAll(storePath, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(projectPath, 0755); err != nil {
		t.Fatal(err)
	}

	// Create package.json in store
	pkgJSONPath := filepath.Join(storePath, "package.json")
	if err := os.WriteFile(pkgJSONPath, []byte(`{"name": "@org/my-package"}`), 0644); err != nil {
		t.Fatal(err)
	}

	files := []*pack.FileInfo{
		{RelPath: "package.json", Size: 100, Mode: 0644},
	}

	linker := New(projectPath)
	_, err := linker.Link("@org/my-package", storePath, files)
	if err != nil {
		t.Fatalf("Link() error for scoped package: %v", err)
	}

	// Relink to exercise the temp-and-swap replace path
	if _, err := linker.Link("@org/my-package", storePath, files); err != nil {
		t.Fatalf("second Link() error for scoped package: %v", err)
	}

	// Verify scoped .lnpm directory
	lnpmPath := filepath.Join(projectPath, ".lnpm", "@org/my-package")
	if _, err := os.Stat(lnpmPath); err != nil {
		t.Errorf(".lnpm/@org/my-package not created: %v", err)
	}

	// The temp-and-swap path must not leave anything behind in the scope dir
	if names := entryNames(t, filepath.Join(projectPath, ".lnpm", "@org")); len(names) != 1 || names[0] != "my-package" {
		t.Errorf(".lnpm/@org entries = %v, want [my-package]", names)
	}

	// Verify scoped node_modules symlink
	nodeModulesPath := filepath.Join(projectPath, "node_modules", "@org", "my-package")
	info, err := os.Lstat(nodeModulesPath)
	if err != nil {
		t.Fatalf("node_modules/@org/my-package not found: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Error("node_modules/@org/my-package is not a symlink")
	}

	// Verify symlink points to correct location (scoped packages need ../../)
	target, err := os.Readlink(nodeModulesPath)
	if err != nil {
		t.Fatalf("failed to read symlink: %v", err)
	}
	if runtime.GOOS == "windows" {
		expectedAbs := filepath.Join(projectPath, ".lnpm", "@org", "my-package")
		if target != expectedAbs {
			t.Errorf("symlink target = %q, want %q", target, expectedAbs)
		}
	} else {
		expectedTarget := filepath.Join("..", "..", ".lnpm", "@org", "my-package")
		if target != expectedTarget {
			t.Errorf("symlink target = %q, want %q", target, expectedTarget)
		}
	}
}

func TestLinkMultiplePackages(t *testing.T) {
	tmpDir := t.TempDir()
	projectPath := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(projectPath, 0755); err != nil {
		t.Fatal(err)
	}

	packages := []string{"pkg-a", "pkg-b", "pkg-c"}
	linker := New(projectPath)

	// Link multiple packages
	for _, pkgName := range packages {
		storePath := filepath.Join(tmpDir, "store", pkgName, "hash")
		if err := os.MkdirAll(storePath, 0755); err != nil {
			t.Fatal(err)
		}

		// Create package.json
		pkgJSONPath := filepath.Join(storePath, "package.json")
		if err := os.WriteFile(pkgJSONPath, []byte(`{}`), 0644); err != nil {
			t.Fatal(err)
		}

		files := []*pack.FileInfo{
			{RelPath: "package.json", Size: 2, Mode: 0644},
		}

		if _, err := linker.Link(pkgName, storePath, files); err != nil {
			t.Fatalf("Link(%s) error: %v", pkgName, err)
		}
	}

	// Verify all packages linked
	linked, err := linker.ListLinked()
	if err != nil {
		t.Fatalf("ListLinked() error: %v", err)
	}
	if len(linked) != 3 {
		t.Errorf("ListLinked() returned %d packages, want 3", len(linked))
	}

	for _, pkgName := range packages {
		if !linker.IsLinked(pkgName) {
			t.Errorf("IsLinked(%s) = false, want true", pkgName)
		}
	}

	// Unlink one package
	if err := linker.Unlink("pkg-b"); err != nil {
		t.Fatalf("Unlink(pkg-b) error: %v", err)
	}

	// Verify only pkg-b unlinked
	if linker.IsLinked("pkg-b") {
		t.Error("IsLinked(pkg-b) = true after unlink, want false")
	}
	if !linker.IsLinked("pkg-a") {
		t.Error("IsLinked(pkg-a) = false, want true")
	}
	if !linker.IsLinked("pkg-c") {
		t.Error("IsLinked(pkg-c) = false, want true")
	}

	// Verify .lnpm directory still exists (has other packages)
	lnpmDir := filepath.Join(projectPath, ".lnpm")
	if _, err := os.Stat(lnpmDir); err != nil {
		t.Error(".lnpm directory removed while packages still linked")
	}
}

// linkPackage links packageName into linker's project from a store entry
// written under storeRoot, following the setup TestLinkScopedPackage uses.
func linkPackage(t *testing.T, linker *Linker, storeRoot, packageName string) {
	t.Helper()

	storePath := filepath.Join(storeRoot, filepath.FromSlash(packageName))
	files := writeStoreFiles(t, storePath, map[string]string{
		"package.json": `{"name":"` + packageName + `"}`,
	})
	if _, err := linker.Link(packageName, storePath, files); err != nil {
		t.Fatalf("Link(%s) error: %v", packageName, err)
	}
}

// assertLinked fails unless ListLinked reports exactly want, in any order.
func assertLinked(t *testing.T, linker *Linker, want ...string) {
	t.Helper()

	got, err := linker.ListLinked()
	if err != nil {
		t.Fatalf("ListLinked() error: %v", err)
	}
	sorted := slices.Sorted(slices.Values(got))
	slices.Sort(want)
	if !slices.Equal(sorted, want) {
		t.Errorf("ListLinked() = %v, want %v", got, want)
	}
}

// assertNotExist fails unless path is absent.
func assertNotExist(t *testing.T, path string) {
	t.Helper()

	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Errorf("%s still exists, want it removed", path)
	}
}

// assertExists fails unless path is present.
func assertExists(t *testing.T, path string) {
	t.Helper()

	if _, err := os.Lstat(path); err != nil {
		t.Errorf("%s is missing: %v", path, err)
	}
}

func TestListLinkedScopedPackage(t *testing.T) {
	tmpDir := t.TempDir()
	projectPath := filepath.Join(tmpDir, "project")

	linker := New(projectPath)
	linkPackage(t, linker, filepath.Join(tmpDir, "store"), "@org/my-package")

	assertLinked(t, linker, "@org/my-package")
}

func TestListLinkedTwoPackagesInOneScope(t *testing.T) {
	tmpDir := t.TempDir()
	projectPath := filepath.Join(tmpDir, "project")

	linker := New(projectPath)
	storeRoot := filepath.Join(tmpDir, "store")
	linkPackage(t, linker, storeRoot, "@org/pkg-a")
	linkPackage(t, linker, storeRoot, "@org/pkg-b")

	assertLinked(t, linker, "@org/pkg-a", "@org/pkg-b")
}

func TestListLinkedMixesScopedAndUnscopedPackages(t *testing.T) {
	tmpDir := t.TempDir()
	projectPath := filepath.Join(tmpDir, "project")

	linker := New(projectPath)
	storeRoot := filepath.Join(tmpDir, "store")
	for _, pkgName := range []string{"plain-package", "@org/pkg-a", "@org/pkg-b", "@other/pkg-c"} {
		linkPackage(t, linker, storeRoot, pkgName)
	}

	assertLinked(t, linker, "plain-package", "@org/pkg-a", "@org/pkg-b", "@other/pkg-c")
}

// TestListLinkedToleratesAScopeRemovedMidListing drives ListLinked against the
// interleaving this package creates for itself: Unlink deletes a scope
// directory the moment it empties, and ListLinked reads the scope directories
// one at a time after listing them, so a scope can vanish between the two
// reads. A scope that goes must drop out of the listing, not fail it — the
// same way a missing .lnpm is reported as no packages rather than an error.
//
// The interleaving is reproduced rather than simulated, so the proof runs one
// way only: once the ENOENT is skipped the test cannot fail, while without the
// skip it fails as soon as the window is hit. The filler scopes widen that
// window by giving the listing loop other scopes to read before it reaches the
// one being removed.
func TestListLinkedToleratesAScopeRemovedMidListing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows reports a delete-pending directory as ERROR_ACCESS_DENIED, " +
			"which is indistinguishable from a genuine permission failure, so " +
			"scopeVanished deliberately does not treat it as a vanished scope " +
			"(see scope_windows.go, TestScopeVanished, and #170/#236): a scope " +
			"removed mid-listing is still reported as an error on windows")
	}

	tmpDir := t.TempDir()
	projectPath := filepath.Join(tmpDir, "project")
	lnpmDir := filepath.Join(projectPath, ".lnpm")

	// "@filler-NNN" sorts before "@zzz", and ReadDir returns entries sorted, so
	// the loop reaches the scope under test last.
	for i := 0; i < 200; i++ {
		if err := os.MkdirAll(filepath.Join(lnpmDir, fmt.Sprintf("@filler-%03d", i), "pkg"), 0755); err != nil {
			t.Fatal(err)
		}
	}

	linker := New(projectPath)
	done := make(chan struct{})
	// Wait for the writer even when an assertion below ends the test, so it
	// cannot report a failure after the test has finished.
	defer func() { <-done }()

	go func() {
		defer close(done)
		for i := 0; i < 2000; i++ {
			if err := os.MkdirAll(filepath.Join(lnpmDir, "@zzz", "pkg"), 0755); err != nil {
				t.Errorf("failed to recreate the scope: %v", err)
				return
			}
			if err := linker.Unlink("@zzz/pkg"); err != nil {
				t.Errorf("Unlink() error: %v", err)
				return
			}
		}
	}()

	for {
		select {
		case <-done:
			return
		default:
		}

		linked, err := linker.ListLinked()
		if err != nil {
			t.Fatalf("ListLinked() failed while a scope was being removed: %v", err)
		}
		if slices.Contains(linked, "@zzz") {
			t.Fatalf("ListLinked() reported the bare scope @zzz: %v", linked)
		}
	}
}

func TestUnlinkScopedPackageKeepsScopeWithAnotherPackage(t *testing.T) {
	tmpDir := t.TempDir()
	projectPath := filepath.Join(tmpDir, "project")

	linker := New(projectPath)
	storeRoot := filepath.Join(tmpDir, "store")
	linkPackage(t, linker, storeRoot, "@org/pkg-a")
	linkPackage(t, linker, storeRoot, "@org/pkg-b")

	if err := linker.Unlink("@org/pkg-a"); err != nil {
		t.Fatalf("Unlink() error: %v", err)
	}

	assertExists(t, filepath.Join(projectPath, ".lnpm", "@org", "pkg-b"))
	assertExists(t, filepath.Join(projectPath, "node_modules", "@org", "pkg-b"))
	assertNotExist(t, filepath.Join(projectPath, ".lnpm", "@org", "pkg-a"))
	assertLinked(t, linker, "@org/pkg-b")
}

func TestCopyFile(t *testing.T) {
	tmpDir := t.TempDir()

	srcPath := filepath.Join(tmpDir, "source.txt")
	dstPath := filepath.Join(tmpDir, "dest.txt")

	content := "test content for copy"
	if err := os.WriteFile(srcPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	if err := copyFile(srcPath, dstPath); err != nil {
		t.Fatalf("copyFile() error: %v", err)
	}

	// Verify content
	data, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatalf("failed to read copied file: %v", err)
	}
	if string(data) != content {
		t.Errorf("copied content = %q, want %q", string(data), content)
	}
}

// TestCopyFile_LargeFile pins that a multi-megabyte source round-trips byte for
// byte. The transfer is internally chunked whatever io.Copy picks as its buffer
// size, and the source here is deliberately not a whole number of megabytes, so
// a rewrite that truncated the tail, reordered chunks or repeated one would be
// caught here. A source small enough to move in a single chunk — which is every
// other copyFile test — shows none of that.
func TestCopyFile_LargeFile(t *testing.T) {
	tmpDir := t.TempDir()

	srcPath := filepath.Join(tmpDir, "large.bin")
	dstPath := filepath.Join(tmpDir, "large-copy.bin")

	// Deliberately not a whole number of megabytes, so the final chunk of the
	// transfer is a partial one whatever buffer size the implementation picks.
	content := make([]byte, 5*1024*1024+7919)
	// A linear congruential generator: deterministic, dependency-free, and with
	// a period long enough that no two chunks of the file look alike, so a
	// transposed or duplicated chunk cannot compare equal by accident.
	state := uint32(1)
	for i := range content {
		state = state*1664525 + 1013904223
		content[i] = byte(state >> 24)
	}

	if err := os.WriteFile(srcPath, content, 0644); err != nil {
		t.Fatal(err)
	}

	if err := copyFile(srcPath, dstPath); err != nil {
		t.Fatalf("copyFile() error: %v", err)
	}

	got, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatalf("failed to read copied file: %v", err)
	}
	if len(got) != len(content) {
		t.Fatalf("copied file is %d bytes, want %d", len(got), len(content))
	}
	if !bytes.Equal(got, content) {
		for i := range got {
			if got[i] != content[i] {
				t.Fatalf("copied file differs from source at byte %d: got %#02x, want %#02x", i, got[i], content[i])
			}
		}
	}
}

// TestLinkSourceResolvesRelativeSource pins that a relative source path is
// resolved against the working directory, not against .lnpm/. Only an absolute
// target makes the link valid from where it is created - and a Windows junction
// accepts nothing else.
func TestLinkSourceResolvesRelativeSource(t *testing.T) {
	tmpDir := t.TempDir()
	projectPath := filepath.Join(tmpDir, "project")
	sourcePath := filepath.Join(tmpDir, "source")
	for _, dir := range []string{projectPath, sourcePath} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(sourcePath, "index.js"), []byte("live"), 0644); err != nil {
		t.Fatal(err)
	}

	t.Chdir(tmpDir)

	linker := New(projectPath)
	if _, err := linker.LinkSource("my-package", "source"); err != nil {
		t.Fatalf("LinkSource(source): %v", err)
	}

	content, err := os.ReadFile(filepath.Join(projectPath, ".lnpm", "my-package", "index.js"))
	if err != nil {
		t.Fatalf("Failed to read through the live link: %v", err)
	}
	if string(content) != "live" {
		t.Errorf("linked index.js = %q, want %q", content, "live")
	}
}
