package tests

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/cli"
	"github.com/pedrosousa13/lnpm/pkg/lockfile"
)

// TestPublishVariants table-drives the publishes whose only meaningful assertion
// is "the package landed in the database with its files stored". Each row
// exercises a different content shape (scoped name, empty package, special
// characters in filenames, deeply nested directories).
func TestPublishVariants(t *testing.T) {
	cases := []struct {
		name     string
		pkgName  string
		files    map[string]string
		minFiles int // minimum file entries expected in the store (0 = skip check)
	}{
		{
			name:    "scoped package",
			pkgName: "@myorg/scoped-pkg",
			files:   map[string]string{"index.js": "module.exports = 'scoped';"},
		},
		{
			name:    "empty package (package.json only)",
			pkgName: "empty-pkg",
			files:   nil,
		},
		{
			name:    "special characters in filenames",
			pkgName: "special-pkg",
			files: map[string]string{
				"file-with-dash.js":       "module.exports = 'dash';",
				"file_with_underscore.js": "module.exports = 'underscore';",
				"file.multiple.dots.js":   "module.exports = 'dots';",
			},
			minFiles: 3,
		},
		{
			name:    "deeply nested directories",
			pkgName: "nested-pkg",
			files: map[string]string{
				"a/b/c/d/e/deep.js": "module.exports = 'deep';",
				"a/b/mid.js":        "module.exports = 'mid';",
				"top.js":            "module.exports = 'top';",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := setupTest(t)
			env.publishPkg(tc.pkgName, "1.0.0", tc.files)
			env.AssertPackageInDatabase(tc.pkgName, true)

			if tc.minFiles > 0 {
				pkg, err := env.Database.GetPackageByName(tc.pkgName)
				if err != nil || pkg == nil {
					t.Fatalf("Failed to get package: %v", err)
				}
				files, err := env.Database.GetFilesForPackage(pkg.ID)
				if err != nil {
					t.Fatalf("Failed to get files: %v", err)
				}
				if len(files) < tc.minFiles {
					t.Errorf("Expected at least %d files, got %d", tc.minFiles, len(files))
				}
			}

			// Nested structure must be preserved in the store.
			if tc.pkgName == "nested-pkg" {
				pkg, _ := env.Database.GetPackageByName("nested-pkg")
				deepFile := filepath.Join(pkg.StorePath, "a", "b", "c", "d", "e", "deep.js")
				if _, err := os.Stat(deepFile); err != nil {
					t.Errorf("Nested file not preserved in store: %v", err)
				}
			}
		})
	}
}

// TestPublishDuplicateHash tests that publishing identical content twice is a
// safe no-op and the package remains in the database.
func TestPublishDuplicateHash(t *testing.T) {
	env := setupTest(t)

	env.simplePkg("dup-pkg")
	// Publish again with same content (should skip).
	if err := cli.RunPublish(false, false, false, false); err != nil {
		t.Fatalf("Failed to publish duplicate: %v", err)
	}
	env.AssertPackageInDatabase("dup-pkg", true)
}

// TestPublishWithPush tests publish with --push flag propagates updates to
// linked projects.
func TestPublishWithPush(t *testing.T) {
	env := setupTest(t)

	pkgDir, projectDir := env.publishAndAdd("push-publish-pkg")

	// Modify and publish with --push.
	env.chdir(pkgDir)
	env.writeFile(filepath.Join(pkgDir, "index.js"), "module.exports = 'v2';")
	if err := cli.RunPublish(true, false, false, false); err != nil {
		t.Fatalf("Failed to publish with push: %v", err)
	}

	env.AssertLinkedFileContent(projectDir, "push-publish-pkg", "index.js", "module.exports = 'v2';")
}

// TestPublishWithPushReportsFailedProjects tests that publish --push returns an
// error when a linked project cannot be updated, while still updating the
// projects that are healthy.
func TestPublishWithPushReportsFailedProjects(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.simplePkg("push-fail-pkg")

	projectA := env.newProject("project-a")
	env.addPkg(projectA, "push-fail-pkg", false, false)
	projectB := env.newProject("project-b")
	env.addPkg(projectB, "push-fail-pkg", false, false)

	// Break project-b by replacing its .lnpm directory with a regular file, so
	// creating .lnpm/push-fail-pkg inside it fails during the push.
	lnpmPath := filepath.Join(projectB, ".lnpm")
	if err := os.RemoveAll(lnpmPath); err != nil {
		t.Fatalf("Failed to remove .lnpm: %v", err)
	}
	env.writeFile(lnpmPath, "not a directory")

	env.chdir(pkgDir)
	env.writeFile(filepath.Join(pkgDir, "index.js"), "module.exports = 'v2';")

	err := cli.RunPublish(true, false, true, true)
	if err == nil {
		t.Fatal("Expected publish --push to fail when a linked project fails")
	}
	if !strings.Contains(err.Error(), "push failed for 1 of 2") {
		t.Errorf("Expected error to report 1 of 2 projects failed, got: %v", err)
	}

	// The healthy project still received the update.
	env.AssertLinkedFileContent(projectA, "push-fail-pkg", "index.js", "module.exports = 'v2';")
}

// TestPublishWithPushSkipsLiveLinkedConsumer covers the relink loop reached by
// `publish --push`, which is a separate one from `push`'s. It has the same
// hazard: relinking a live-linked consumer from the store turns its link into a
// snapshot copy, and the source edits it was added for stop arriving with
// nothing said about it.
func TestPublishWithPushSkipsLiveLinkedConsumer(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.simplePkg("pub-live-lib")
	copyProject := env.newProject("pub-copy-project")
	env.addPkg(copyProject, "pub-live-lib", false, false)
	liveProject := env.newProject("pub-live-project")
	env.addLinkedPkg(liveProject, "pub-live-lib")

	env.chdir(pkgDir)
	env.writeFile(filepath.Join(pkgDir, "index.js"), "module.exports = 'v2';")
	out := captureStdout(t, func() {
		if err := cli.RunPublish(true, false, false, false); err != nil {
			t.Fatalf("Failed to publish with push: %v", err)
		}
	})

	if !strings.Contains(out, "Pushed to 1/2 projects (1 skipped: live link to source)") {
		t.Errorf("Expected a coherent 1/2 summary naming the skip, got:\n%s", out)
	}

	// The copy-linked project got the snapshot, as always.
	env.AssertLinkedFileContent(copyProject, "pub-live-lib", "index.js", "module.exports = 'v2';")

	// The live-linked one is still a link, and a later edit still reaches it
	// with no lnpm command - which is the acceptance criterion a clobbered link
	// silently breaks.
	env.AssertLiveLink(liveProject, "pub-live-lib", pkgDir)
	env.writeFile(filepath.Join(pkgDir, "index.js"), "module.exports = 'v3';")
	env.AssertFileContent(filepath.Join(liveProject, "node_modules", "pub-live-lib", "index.js"),
		"module.exports = 'v3';")
}

// TestPublishConcurrentSamePackage tests concurrent publishes of same package.
func TestPublishConcurrentSamePackage(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.CreateTestPackage("concurrent-pub", "1.0.0", map[string]string{
		"index.js": "module.exports = 'test';",
	})

	RunConcurrently(t,
		func() error { _ = os.Chdir(pkgDir); return cli.RunPublish(false, false, false, false) },
		func() error { _ = os.Chdir(pkgDir); return cli.RunPublish(false, false, false, false) },
		func() error { _ = os.Chdir(pkgDir); return cli.RunPublish(false, false, false, false) },
	)

	env.AssertPackageInDatabase("concurrent-pub", true)
}

// TestPublishNoPackageJSON tests publishing without package.json errors.
func TestPublishNoPackageJSON(t *testing.T) {
	env := setupTest(t)

	pkgDir := filepath.Join(env.TempDir, "no-pkg-json")
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		t.Fatalf("Failed to create dir: %v", err)
	}
	env.chdir(pkgDir)

	if err := cli.RunPublish(false, false, false, false); err == nil {
		t.Fatal("Expected error publishing without package.json")
	}
}

// TestPublishLargePackage tests publishing a package with many files tracks them all.
func TestPublishLargePackage(t *testing.T) {
	env := setupTest(t)

	files := make(map[string]string)
	for i := 0; i < 100; i++ {
		files[fmt.Sprintf("file-%d.js", i)] = fmt.Sprintf("module.exports = %d;", i)
	}
	env.publishPkg("large-pkg", "1.0.0", files)

	pkg, err := env.Database.GetPackageByName("large-pkg")
	if err != nil || pkg == nil {
		t.Fatalf("Failed to get package: %v", err)
	}
	dbFiles, err := env.Database.GetFilesForPackage(pkg.ID)
	if err != nil {
		t.Fatalf("Failed to get files: %v", err)
	}
	if len(dbFiles) < 100 {
		t.Errorf("Expected at least 100 files, got %d", len(dbFiles))
	}
}

// TestPublishVersionUpdate tests publishing a new version updates the recorded version.
func TestPublishVersionUpdate(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.publishPkg("version-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'v1';",
	})

	env.writeFile(filepath.Join(pkgDir, "package.json"), `{"name":"version-pkg","version":"2.0.0"}`)
	env.writeFile(filepath.Join(pkgDir, "index.js"), "module.exports = 'v2';")
	if err := cli.RunPublish(false, false, false, false); err != nil {
		t.Fatalf("Failed to publish v2: %v", err)
	}

	pkg, err := env.Database.GetPackageByName("version-pkg")
	if err != nil || pkg == nil {
		t.Fatalf("Failed to get package: %v", err)
	}
	if pkg.Version != "2.0.0" {
		t.Errorf("Expected version 2.0.0, got %s", pkg.Version)
	}
}

// TestPublishSymlinks tests publishing a package containing symlinks succeeds.
func TestPublishSymlinks(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.CreateTestPackage("symlink-pkg", "1.0.0", map[string]string{
		"real.js": "module.exports = 'real';",
	})
	if err := os.Symlink("real.js", filepath.Join(pkgDir, "link.js")); err != nil {
		t.Skipf("Symlinks not supported: %v", err)
	}
	env.chdir(pkgDir)
	if err := cli.RunPublish(false, false, false, false); err != nil {
		t.Fatalf("Failed to publish with symlinks: %v", err)
	}
	env.AssertPackageInDatabase("symlink-pkg", true)
}

// TestPublishReadOnlyFiles tests publishing read-only files succeeds.
func TestPublishReadOnlyFiles(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.CreateTestPackage("readonly-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'test';",
	})
	if err := os.WriteFile(filepath.Join(pkgDir, "readonly.txt"), []byte("readonly"), 0444); err != nil {
		t.Fatalf("Failed to create readonly file: %v", err)
	}
	env.chdir(pkgDir)
	if err := cli.RunPublish(false, false, false, false); err != nil {
		t.Fatalf("Failed to publish with readonly files: %v", err)
	}
	env.AssertPackageInDatabase("readonly-pkg", true)
}

// TestPublishPreservesFileMetadata tests that executable bits are preserved.
func TestPublishPreservesFileMetadata(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping on Windows - executable permission bits not supported")
	}
	env := setupTest(t)

	pkgDir := env.CreateTestPackage("metadata-pkg", "1.0.0", map[string]string{
		"script.sh": "#!/bin/sh\necho test",
	})
	if err := os.Chmod(filepath.Join(pkgDir, "script.sh"), 0755); err != nil {
		t.Fatalf("Failed to chmod: %v", err)
	}
	env.chdir(pkgDir)
	if err := cli.RunPublish(false, false, false, false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	pkg, err := env.Database.GetPackageByName("metadata-pkg")
	if err != nil || pkg == nil {
		t.Fatalf("Failed to get package: %v", err)
	}
	files, err := env.Database.GetFilesForPackage(pkg.ID)
	if err != nil {
		t.Fatalf("Failed to get files: %v", err)
	}
	foundExec := false
	for _, f := range files {
		if f.RelativePath == "script.sh" && f.Mode&0111 != 0 {
			foundExec = true
			break
		}
	}
	if !foundExec {
		t.Error("Executable permission not preserved in database")
	}
}

// TestPublishExcludesTheRetreatSnapshot walks the exact sequence the README
// documents - retreat --force, then publish - and pins that the snapshot retreat
// leaves behind is treated as lnpm's own state, the way lnpm.lock always was.
//
// The snapshot records every linked package's absolute source path on the
// developer's machine, so shipping it would widen what a publish discloses. It
// sits in the package root at precisely the moment publish runs, which is what
// makes the default exclusion the only thing standing between it and the
// registry.
func TestPublishExcludesTheRetreatSnapshot(t *testing.T) {
	env := setupTest(t)

	env.simplePkg("snapshot-leak-dep")

	pkgDir := env.CreateTestPackage("snapshot-leak-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'snapshot-leak-pkg';",
	})
	env.addPkg(pkgDir, "snapshot-leak-dep", false, false)
	if err := cli.RunRetreat(true, false); err != nil {
		t.Fatalf("Failed to retreat: %v", err)
	}
	env.AssertFileExists(lockfile.RetreatPath(pkgDir), true)

	env.chdir(pkgDir)
	if err := cli.RunPublish(false, false, false, false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	pkg, err := env.Database.GetPackageByName("snapshot-leak-pkg")
	if err != nil || pkg == nil {
		t.Fatalf("Failed to get package: %v", err)
	}
	files, err := env.Database.GetFilesForPackage(pkg.ID)
	if err != nil {
		t.Fatalf("Failed to get files: %v", err)
	}
	snapshotName := filepath.Base(lockfile.RetreatPath(pkgDir))
	for _, f := range files {
		if f.RelativePath == snapshotName {
			t.Errorf("Expected %s to be excluded from the published files, but it was packed", snapshotName)
		}
	}
	env.AssertFileExists(filepath.Join(pkg.StorePath, snapshotName), false)
}

// TestPublishWithPushReportsChangedAndUnchangedCounts covers acceptance
// criterion 5 on `publish --push`'s relink loop, which is a separate one from
// `push`'s and reports separately.
func TestPublishWithPushReportsChangedAndUnchangedCounts(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.publishPkg("pub-counted-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'v1';",
		"lib/a.js": "exports.a = 1;",
	})
	projectDir := env.newProject("pub-counted-project")
	env.addPkg(projectDir, "pub-counted-pkg", false, false)

	untouchedBefore, err := os.Stat(filepath.Join(projectDir, ".lnpm", "pub-counted-pkg", "lib", "a.js"))
	if err != nil {
		t.Fatalf("Failed to stat linked lib/a.js: %v", err)
	}

	env.chdir(pkgDir)
	env.writeFile(filepath.Join(pkgDir, "index.js"), "module.exports = 'v2';")
	out := captureStdout(t, func() {
		if err := cli.RunPublish(true, false, false, false); err != nil {
			t.Fatalf("Failed to publish with push: %v", err)
		}
	})

	if !strings.Contains(out, "(1 changed, 2 unchanged)") {
		t.Errorf("Expected publish --push to report 1 changed of 3 files, got:\n%s", out)
	}

	untouchedAfter, err := os.Stat(filepath.Join(projectDir, ".lnpm", "pub-counted-pkg", "lib", "a.js"))
	if err != nil {
		t.Fatalf("Failed to stat linked lib/a.js after the push: %v", err)
	}
	if !os.SameFile(untouchedBefore, untouchedAfter) {
		t.Error("lib/a.js is a different file after the push, so it was rewritten even though it did not change")
	}
	env.AssertLinkedFileContent(projectDir, "pub-counted-pkg", "index.js", "module.exports = 'v2';")
}
