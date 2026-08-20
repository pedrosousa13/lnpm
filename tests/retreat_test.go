package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/cli"
	"github.com/pedrosousa13/lnpm/pkg/lockfile"
)

// retreatLeavesClean asserts the full post-retreat clean state for projectDir:
// every package symlink is gone, the lockfile is deleted, the .lnpm directory is
// removed, and no db links remain.
func retreatLeavesClean(t *testing.T, env *TestEnvironment, projectDir string, packages ...string) {
	t.Helper()
	for _, name := range packages {
		env.AssertSymlinkMissing(projectDir, name)
		env.AssertDatabaseNoLink(name, projectDir)
		env.AssertPackageJSONMissing(projectDir, name)
	}
	env.AssertLockfileExists(projectDir, false)
	env.AssertDirectoryExists(filepath.Join(projectDir, ".lnpm"), false)
}

// TestRetreatVariants table-drives the "add packages, then retreat --force"
// happy path across unscoped, scoped, dev, and multi-package shapes. Each row
// must leave the project fully cleaned up.
func TestRetreatVariants(t *testing.T) {
	cases := []struct {
		name     string
		packages []string
		dev      bool
	}{
		{"single unscoped", []string{"retreat-pkg"}, false},
		{"scoped", []string{"@org/scoped-retreat"}, false},
		{"dev dependency", []string{"dev-retreat-pkg"}, true},
		{"multiple packages", []string{"pkg-a", "pkg-b", "pkg-c"}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := setupTest(t)

			for _, name := range tc.packages {
				env.simplePkg(name)
			}
			projectDir := env.newProject("test-project")
			for _, name := range tc.packages {
				env.addPkg(projectDir, name, tc.dev, false)
				env.AssertSymlinkExists(projectDir, name)
			}

			if err := cli.RunRetreat(true, false); err != nil {
				t.Fatalf("Failed to retreat: %v", err)
			}
			retreatLeavesClean(t, env, projectDir, tc.packages...)
		})
	}
}

// TestRetreatNoForceFlag tests that retreat without --force changes nothing.
func TestRetreatNoForceFlag(t *testing.T) {
	env := setupTest(t)

	_, projectDir := env.publishAndAdd("force-pkg")

	if err := cli.RunRetreat(false, false); err != nil {
		t.Fatalf("Retreat without force failed: %v", err)
	}

	env.AssertSymlinkExists(projectDir, "force-pkg")
	env.AssertLockfileExists(projectDir, true)
	env.AssertDirectoryExists(filepath.Join(projectDir, ".lnpm"), true)
}

// TestRetreatNoLinks tests retreating a project with no links is a safe no-op.
func TestRetreatNoLinks(t *testing.T) {
	env := setupTest(t)

	projectDir := env.newProject("test-project")
	if err := cli.RunRetreat(true, false); err != nil {
		t.Fatalf("Retreat with no links failed: %v", err)
	}
	env.AssertLockfileExists(projectDir, false)
	env.AssertDirectoryExists(filepath.Join(projectDir, ".lnpm"), false)
}

// TestRetreatRestoresOriginalVersion tests that retreat restores the pre-add
// dependency version.
func TestRetreatRestoresOriginalVersion(t *testing.T) {
	env := setupTest(t)

	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	env.writePackageJSON(projectDir, map[string]interface{}{
		"name":         "test-project",
		"version":      "1.0.0",
		"dependencies": map[string]interface{}{"retreat-pkg": "^1.0.0"},
	})

	env.simplePkg("retreat-pkg")
	env.addPkg(projectDir, "retreat-pkg", false, false)
	env.AssertPackageJSON(projectDir, "retreat-pkg", "file:.lnpm/retreat-pkg")

	if err := cli.RunRetreat(true, false); err != nil {
		t.Fatalf("Failed to retreat: %v", err)
	}
	env.AssertPackageJSON(projectDir, "retreat-pkg", "^1.0.0")
}

// TestRetreatPreservesOtherDependencies tests that retreat removes only lnpm
// packages and leaves regular dependencies intact.
func TestRetreatPreservesOtherDependencies(t *testing.T) {
	env := setupTest(t)

	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	env.writePackageJSON(projectDir, map[string]interface{}{
		"name":    "test-project",
		"version": "1.0.0",
		"dependencies": map[string]interface{}{
			"lodash":  "^4.17.21",
			"express": "^4.18.0",
		},
	})

	env.simplePkg("retreat-pkg")
	env.addPkg(projectDir, "retreat-pkg", false, false)

	if err := cli.RunRetreat(true, false); err != nil {
		t.Fatalf("Failed to retreat: %v", err)
	}

	env.AssertPackageJSON(projectDir, "lodash", "^4.17.21")
	env.AssertPackageJSON(projectDir, "express", "^4.18.0")
	env.AssertPackageJSONMissing(projectDir, "retreat-pkg")
}

// TestRetreatPartiallyLinked tests retreat when only some published packages are
// actually linked into the project.
func TestRetreatPartiallyLinked(t *testing.T) {
	env := setupTest(t)

	env.simplePkg("pkg-linked")
	env.simplePkg("pkg-notlinked")

	projectDir := env.newProject("test-project")
	env.addPkg(projectDir, "pkg-linked", false, false)
	env.AssertSymlinkExists(projectDir, "pkg-linked")
	env.AssertSymlinkMissing(projectDir, "pkg-notlinked")

	if err := cli.RunRetreat(true, false); err != nil {
		t.Fatalf("Failed to retreat: %v", err)
	}
	env.AssertLockfileExists(projectDir, false)
	env.AssertDirectoryExists(filepath.Join(projectDir, ".lnpm"), false)
}

// TestRetreatCorruptLockfile tests that retreat aborts with a clear error when
// lnpm.lock exists but cannot be parsed, in both the preview and --force paths,
// instead of crashing on the nil lock file that lockfile.Load returns.
func TestRetreatCorruptLockfile(t *testing.T) {
	cases := []struct {
		name  string
		force bool
	}{
		{"preview", false},
		{"force", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := setupTest(t)

			_, projectDir := env.publishAndAdd("corrupt-lock-pkg")
			env.chdir(projectDir)
			env.writeFile(filepath.Join(projectDir, "lnpm.lock"), "version: 1\npackages: {not valid yaml")

			err := cli.RunRetreat(tc.force, false)
			if err == nil {
				t.Fatal("Expected an error for a corrupt lock file, got nil")
			}
			if !strings.Contains(err.Error(), "lnpm.lock") {
				t.Errorf("Expected error to mention the lock file, got: %v", err)
			}

			// Nothing may be touched when the lock file cannot be read.
			env.AssertSymlinkExists(projectDir, "corrupt-lock-pkg")
			env.AssertLockfileExists(projectDir, true)
			env.AssertDirectoryExists(filepath.Join(projectDir, ".lnpm"), true)
		})
	}
}

// TestRetreatCleansGitignore tests that the .lnpm/ entry is removed from
// .gitignore on retreat.
func TestRetreatCleansGitignore(t *testing.T) {
	env := setupTest(t)

	_, projectDir := env.publishAndAdd("git-pkg")
	env.AssertGitignore(projectDir, ".lnpm/", true)

	if err := cli.RunRetreat(true, false); err != nil {
		t.Fatalf("Failed to retreat: %v", err)
	}

	// The entry must be gone; the file itself may or may not remain.
	if _, err := os.Stat(filepath.Join(projectDir, ".gitignore")); err == nil {
		env.AssertGitignore(projectDir, ".lnpm/", false)
	}
}

// TestRetreatForceRestoresLinkedPackage covers `retreat --force` on a package
// added with --link: the original specifier comes back, .lnpm/ goes away, and
// the source tree the removed link pointed at is left alone.
//
// It is the teardown half of the story, not the isLnpmReference half. The
// original version this restores is a real one the user wrote, so it exercises
// the same branch a copy-linked package does; what is specific to --link here is
// that .lnpm/<pkg> was a link into a real source tree when retreat deleted it.
// TestRetreatIgnoresLinkReferenceAsOriginalVersion is what pins retreat's
// handling of a link:.lnpm/ specifier.
func TestRetreatForceRestoresLinkedPackage(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.simplePkg("retreat-live-lib")
	projectDir := env.newProject("retreat-live-project")
	env.writePackageJSON(projectDir, map[string]interface{}{
		"name":         "retreat-live-project",
		"version":      "1.0.0",
		"dependencies": map[string]interface{}{"retreat-live-lib": "^1.0.0"},
	})
	env.addLinkedPkg(projectDir, "retreat-live-lib")
	env.AssertPackageJSON(projectDir, "retreat-live-lib", "link:.lnpm/retreat-live-lib")

	if err := cli.RunRetreat(true, false); err != nil {
		t.Fatalf("Failed to retreat: %v", err)
	}

	env.AssertPackageJSON(projectDir, "retreat-live-lib", "^1.0.0")
	env.AssertDirectoryExists(filepath.Join(projectDir, ".lnpm"), false)
	env.AssertLockfileExists(projectDir, false)

	env.AssertDirectoryExists(pkgDir, true)
	env.AssertFileContent(filepath.Join(pkgDir, "index.js"), "module.exports = 'retreat-live-lib';")
	env.AssertFileExists(filepath.Join(pkgDir, "package.json"), true)
}

// TestRetreatIgnoresLinkReferenceAsOriginalVersion pins that retreat treats a
// link:.lnpm/ specifier recorded as an original version the same way it treats
// file:.lnpm/ - as lnpm's own value, not the user's - so the dependency is
// dropped rather than restored to a reference that no longer resolves.
//
// This is the test that carries retreat's --link change: with the recorded
// original version left alone, retreat writes link:.lnpm/stale-ref-lib straight
// back into package.json.
func TestRetreatIgnoresLinkReferenceAsOriginalVersion(t *testing.T) {
	env := setupTest(t)

	env.simplePkg("stale-ref-lib")
	projectDir := env.newProject("stale-ref-project")
	env.addLinkedPkg(projectDir, "stale-ref-lib")

	// Rewrite the lock entry the way an older version could have recorded it.
	env.AssertLockfile(projectDir, func(lock *lockfile.LockFile) {
		entry, ok := lock.Get("stale-ref-lib")
		if !ok {
			t.Fatal("stale-ref-lib missing from lockfile")
		}
		entry.OriginalVersion = "link:.lnpm/stale-ref-lib"
		lock.Add("stale-ref-lib", entry)
		if err := lock.Save(projectDir); err != nil {
			t.Fatalf("Failed to save lockfile: %v", err)
		}
	})

	// The preview must say the same thing the --force run then does.
	out := captureStdout(t, func() {
		if err := cli.RunRetreat(false, false); err != nil {
			t.Fatalf("Failed to preview retreat: %v", err)
		}
	})
	if !strings.Contains(out, "stale-ref-lib: will be removed from package.json") {
		t.Errorf("Expected the preview to drop the dependency, got:\n%s", out)
	}

	if err := cli.RunRetreat(true, false); err != nil {
		t.Fatalf("Failed to retreat: %v", err)
	}

	env.AssertPackageJSONMissing(projectDir, "stale-ref-lib")
}

// TestRetreatMarkersComeFromTheIconHelpers sweeps retreat's progress report for
// the decorative markers, so a line printing "✓"/"⚠"/"💡" as a string literal
// instead of calling the helpers is caught.
//
// That, and only that, is what this proves. Capturing stdout replaces it with a
// pipe, and the helpers fall back to ASCII whenever stdout is not a terminal,
// so a report free of glyphs here says nothing about NO_COLOR: the pipe alone
// would have produced it. NO_COLOR is set anyway, so the sweep does not depend
// on how the capture is implemented. What NO_COLOR does to retreat's own output
// is covered by TestRunRetreatOnATerminal in internal/cli, which runs retreat
// with stdout on a real terminal.
func TestRetreatMarkersComeFromTheIconHelpers(t *testing.T) {
	cases := []struct {
		name string
		// originalVersion, when set, is written into the lock entry so retreat
		// restores the dependency instead of dropping it. Each branch prints
		// its own marker, so both are worth sweeping.
		originalVersion string
		// readOnly, when set, makes part of the project unwritable so
		// retreat's failure lines are reached: they carry markers of their
		// own. Any scenario that sets it needs the filesystem to enforce
		// permissions, so setting it also selects the guard.
		readOnly func(t *testing.T, projectDir string)
		want     string
	}{
		{
			name: "dependency dropped",
			want: "Removed glyph-pkg from package.json",
		},
		{
			name:            "dependency restored",
			originalVersion: "^1.0.0",
			want:            "Restored glyph-pkg to ^1.0.0",
		},
		{
			name:     "package.json cannot be written",
			readOnly: makePackageJSONReadOnly,
			want:     "Failed to update package.json",
		},
		{
			name:            "package.json cannot be restored",
			originalVersion: "^1.0.0",
			readOnly:        makePackageJSONReadOnly,
			want:            "Failed to restore package.json",
		},
		{
			name:            "nothing in the project directory can be removed",
			originalVersion: "^1.0.0",
			readOnly:        makeProjectDirReadOnly,
			want:            "Failed to remove .lnpm/", // and .gitignore, lnpm.lock
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.readOnly != nil {
				requirePermissionEnforcement(t)
			}

			t.Setenv("NO_COLOR", "1")
			env := setupTest(t)
			_, projectDir := env.publishAndAdd("glyph-pkg")

			if tc.originalVersion != "" {
				env.AssertLockfile(projectDir, func(lock *lockfile.LockFile) {
					entry, ok := lock.Get("glyph-pkg")
					if !ok {
						t.Fatal("glyph-pkg missing from lockfile")
					}
					entry.OriginalVersion = tc.originalVersion
					lock.Add("glyph-pkg", entry)
					if err := lock.Save(projectDir); err != nil {
						t.Fatalf("Failed to save lockfile: %v", err)
					}
				})
			}
			if tc.readOnly != nil {
				tc.readOnly(t, projectDir)
			}

			out := captureStdout(t, func() {
				if err := cli.RunRetreat(true, false); err != nil {
					t.Errorf("Failed to retreat: %v", err)
				}
			})

			if !strings.Contains(out, tc.want) {
				t.Fatalf("Expected the report to contain %q, got:\n%s", tc.want, out)
			}
			assertNoRawGlyphs(t, out)
		})
	}
}

// makeProjectDirReadOnly makes the project directory itself unwritable, so
// every unlink and create inside it fails while its existing files stay
// readable and writable. makePackageJSONReadOnly cannot stand in for this: it
// addresses one named file, and its 0444 would also cost the directory the
// execute bit that makes it traversable at all.
func makeProjectDirReadOnly(t *testing.T, projectDir string) {
	t.Helper()

	if err := os.Chmod(projectDir, 0555); err != nil {
		t.Fatalf("Failed to make the project directory read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(projectDir, 0755) })
}

// assertNoRawGlyphs fails when out contains any of the decorative markers,
// which must have been rendered as ASCII.
func assertNoRawGlyphs(t *testing.T, out string) {
	t.Helper()

	for _, glyph := range "✓✗⚠💡" {
		if strings.ContainsRune(out, glyph) {
			t.Errorf("Output contains the raw glyph %q, want its ASCII fallback; output was:\n%s", string(glyph), out)
		}
	}
}

// TestRetreatWritesRestoreSnapshot covers the first acceptance criterion of
// `lnpm restore`: `retreat --force` must preserve a record of what it unlinked
// instead of discarding it, so restore has something to work from. The snapshot
// stands in for the lock file retreat removes, and carries the same entries.
func TestRetreatWritesRestoreSnapshot(t *testing.T) {
	env := setupTest(t)

	env.simplePkg("snapshot-pkg-a")
	env.simplePkg("snapshot-pkg-b")
	projectDir := env.newProject("snapshot-project")
	env.writePackageJSON(projectDir, map[string]interface{}{
		"name":         "snapshot-project",
		"version":      "1.0.0",
		"dependencies": map[string]interface{}{"snapshot-pkg-a": "^1.0.0"},
	})
	env.addPkg(projectDir, "snapshot-pkg-a", false, false)
	env.addPkg(projectDir, "snapshot-pkg-b", false, false)

	if err := cli.RunRetreat(true, false); err != nil {
		t.Fatalf("Failed to retreat: %v", err)
	}

	env.AssertLockfileExists(projectDir, false)
	env.AssertFileExists(lockfile.RetreatPath(projectDir), true)

	snapshot, err := lockfile.LoadRetreat(projectDir)
	if err != nil {
		t.Fatalf("Failed to load the retreat snapshot: %v", err)
	}
	if snapshot == nil {
		t.Fatal("Expected a retreat snapshot, got none")
	}
	for _, name := range []string{"snapshot-pkg-a", "snapshot-pkg-b"} {
		entry, ok := snapshot.Get(name)
		if !ok {
			t.Fatalf("Expected %s in the retreat snapshot", name)
		}
		if entry.Version != "1.0.0" {
			t.Errorf("Expected %s at 1.0.0 in the snapshot, got %q", name, entry.Version)
		}
	}
	if entry, _ := snapshot.Get("snapshot-pkg-a"); entry.OriginalVersion != "^1.0.0" {
		t.Errorf("Expected snapshot-pkg-a to record the original specifier ^1.0.0, got %q", entry.OriginalVersion)
	}
}
