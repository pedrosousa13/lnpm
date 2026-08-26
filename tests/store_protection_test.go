package tests

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/cli"
	"github.com/pedrosousa13/lnpm/internal/config"
)

// storePathOf returns a published package's entry directory in the isolated
// store.
func (te *TestEnvironment) storePathOf(packageName string) string {
	te.t.Helper()

	pkg, err := te.Database.GetPackageByName(packageName)
	if err != nil || pkg == nil {
		te.t.Fatalf("Package %s not found in database: %v", packageName, err)
	}
	return pkg.StorePath
}

// TestAnEditInOneProjectDoesNotReachAnother is #333's scenario end to end, in
// the order it was originally reproduced in: a consumer overwrites a linked
// file, and a project created afterwards asks for the same version.
//
// Before the store's content was write protected the overwrite succeeded, and
// it succeeded *through the store*: the hard link in .lnpm/{package} is the
// store's own inode, so the entry for that content hash held the tampered bytes
// and every later add served them. The write now fails, which is the whole
// point - a build step writing into node_modules finds out, instead of the next
// project finding out.
func TestAnEditInOneProjectDoesNotReachAnother(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root bypasses the permission bits this test relies on")
	}
	env := setupTest(t)

	const legit = "module.exports = 'poison-pkg';"
	env.simplePkg("poison-pkg")
	storePath := env.storePathOf("poison-pkg")

	projectA := env.newProject("project-a")
	env.addPkg(projectA, "poison-pkg", false, false)

	// O_TRUNC is the shape that matters: it is what a redirect or a bundler
	// emitting over the file does, and it destroys the store's copy before a
	// single byte is written.
	linked := filepath.Join(projectA, ".lnpm", "poison-pkg", "index.js")
	f, err := os.OpenFile(linked, os.O_WRONLY|os.O_TRUNC, 0)
	if err == nil {
		_ = f.Close()
		t.Fatalf("Opening %s for truncation succeeded; the store's copy is writable through the link", linked)
	}
	if !os.IsPermission(err) {
		t.Errorf("Opening the linked file failed with %v, want a permission error", err)
	}

	env.AssertFileContent(filepath.Join(storePath, "index.js"), legit)

	projectB := env.newProject("project-b")
	env.addPkg(projectB, "poison-pkg", false, false)
	env.AssertLinkedFileContent(projectB, "poison-pkg", "index.js", legit)
}

// TestGCRemovesAWriteProtectedStoreEntry is the hazard the protection creates,
// and the one that would be worse than the bug: a store lnpm can no longer
// clean up. Unlinking a file on Unix needs write permission on its parent
// directory rather than on the file, and on Windows os.Remove clears the
// read-only attribute and retries - but both are claims about the platform, so
// this runs the real command against a real protected entry on each of them.
func TestGCRemovesAWriteProtectedStoreEntry(t *testing.T) {
	env := setupTest(t)

	env.simplePkg("collectable-pkg")
	storePath := env.storePathOf("collectable-pkg")

	// Establish that there is a protected entry to collect. Without this the
	// test passes against a build that never protects anything, which is the
	// wrong thing to be reassured by: what is being checked is that gc survives
	// the protection, so the protection has to be there. A read-only file reads
	// back as 0444 on Windows too, where os.Stat synthesises the mode from the
	// file attributes, so this needs no platform split.
	content := filepath.Join(storePath, "index.js")
	info, err := os.Stat(content)
	if err != nil {
		t.Fatalf("Failed to stat the store's index.js: %v", err)
	}
	if info.Mode().Perm()&0222 != 0 {
		t.Fatalf("The store's index.js is %04o, want no write bits; gc is not being asked the question this test exists for", info.Mode().Perm())
	}

	if err := cli.RunGC(false, "", false, true); err != nil {
		t.Fatalf("Failed to run GC: %v", err)
	}

	env.AssertPackageInDatabase("collectable-pkg", false)
	env.AssertDirectoryExists(storePath, false)
}

// TestRemoveDeletesAWriteProtectedLinkedPackage is the same hazard at the
// consumer end. The tree under .lnpm/{package} is hard links onto protected
// store files, so `lnpm remove` deletes read-only files too.
func TestRemoveDeletesAWriteProtectedLinkedPackage(t *testing.T) {
	env := setupTest(t)

	_, projectDir := env.publishAndAdd("removable-pkg")

	env.chdir(projectDir)
	if err := cli.RunRemove("removable-pkg", false, true); err != nil {
		t.Fatalf("Failed to remove package: %v", err)
	}

	env.AssertDirectoryExists(filepath.Join(projectDir, ".lnpm", "removable-pkg"), false)
	env.AssertSymlinkMissing(projectDir, "removable-pkg")
}

// TestPullReplacesAWriteProtectedLinkedPackage covers the relink swap. Link
// renames the previous .lnpm/{package} aside and deletes it after the new tree
// is in place, so a pull to a new version is a second removal of read-only
// files - and the tree it puts there has to be the new version's.
func TestPullReplacesAWriteProtectedLinkedPackage(t *testing.T) {
	env := setupTest(t)

	pkgDir, projectDir := env.publishAndAdd("stale-pkg")
	env.republish(pkgDir, "stale-pkg", "2.0.0", "module.exports = 'v2';")

	env.chdir(projectDir)
	if err := cli.RunPull(nil); err != nil {
		t.Fatalf("Failed to pull: %v", err)
	}

	env.AssertLinkedFileContent(projectDir, "stale-pkg", "index.js", "module.exports = 'v2';")
}

// TestCopyLinkModeStillMaterialisesTheStoreEntry covers the acceptance
// criterion that link_mode: copy keeps working. A copy shares no inode with the
// store, so the protection was never what stood between that mode and the
// store's canonical copy - but every file it copies is now read-only at the
// source, and a copy that could not read a protected file, or that landed
// unreadable, would fail here.
func TestCopyLinkModeStillMaterialisesTheStoreEntry(t *testing.T) {
	env := setupTest(t)

	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	env.writeFile(cfgPath, "link_mode: copy\n")
	t.Setenv("LNPM_CONFIG", cfgPath)
	config.ResetForTesting()
	t.Cleanup(config.ResetForTesting)

	env.simplePkg("copied-pkg")
	storePath := env.storePathOf("copied-pkg")

	projectDir := env.newProject("copy-project")
	env.addPkg(projectDir, "copied-pkg", false, false)

	env.AssertLinkedFileContent(projectDir, "copied-pkg", "index.js", "module.exports = 'copied-pkg';")

	// The copy is a private one. os.SameFile is the direct question - it
	// compares the identity the hard-link path would have shared - and it is
	// asked because copy mode's entire reason to exist is that a write in the
	// consumer cannot reach the store.
	storeInfo, err := os.Stat(filepath.Join(storePath, "index.js"))
	if err != nil {
		t.Fatalf("Failed to stat the store's index.js: %v", err)
	}
	linkedInfo, err := os.Stat(filepath.Join(projectDir, ".lnpm", "copied-pkg", "index.js"))
	if err != nil {
		t.Fatalf("Failed to stat the linked index.js: %v", err)
	}
	if os.SameFile(storeInfo, linkedInfo) {
		t.Error("copy mode shared the store's inode with the consumer")
	}
}

// TestLiveLinkedSourceStaysWritable pins the claim the maintainer's decision
// rests on: `add --link` is untouched by the store's write protection.
//
// It is worth measuring rather than assuming, because the whole argument for
// protecting the store over switching link_mode's default is that live sync
// never shared an inode with a store entry to begin with. .lnpm/{package} is a
// link at the package's source directory, and publishing takes a copy into the
// store rather than reaching back into the source, so the author's own files
// keep the modes they had.
func TestLiveLinkedSourceStaysWritable(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.simplePkg("live-writable-pkg")
	projectDir := env.newProject("live-writable-project")
	env.addLinkedPkg(projectDir, "live-writable-pkg")

	source := filepath.Join(pkgDir, "index.js")
	if err := os.WriteFile(source, []byte("module.exports = 'edited';"), 0644); err != nil {
		t.Fatalf("Failed to edit the live-linked source: %v", err)
	}

	env.AssertFileContent(filepath.Join(projectDir, "node_modules", "live-writable-pkg", "index.js"),
		"module.exports = 'edited';")
}
