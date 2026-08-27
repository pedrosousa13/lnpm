package tests

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/cli"
	"github.com/pedrosousa13/lnpm/pkg/lockfile"
)

// TestRestoreRelinksPackagesFromSnapshot is the round trip the feature exists
// for: add, retreat, restore, and the project is back where it started. It
// covers the acceptance criteria that restore recreates .lnpm/<pkg>, the
// node_modules symlink and the file:.lnpm/<pkg> specifier for every package in
// the snapshot, and that a clean restore consumes the snapshot and leaves
// lnpm.lock holding the pre-retreat entries.
func TestRestoreRelinksPackagesFromSnapshot(t *testing.T) {
	env := setupTest(t)

	env.simplePkg("restore-pkg-a")
	env.simplePkg("restore-pkg-b")
	projectDir := env.newProject("restore-project")
	env.writePackageJSON(projectDir, map[string]interface{}{
		"name":         "restore-project",
		"version":      "1.0.0",
		"dependencies": map[string]interface{}{"restore-pkg-a": "^1.0.0"},
	})
	env.addPkg(projectDir, "restore-pkg-a", false, false)
	env.addPkg(projectDir, "restore-pkg-b", false, false)

	before := map[string]lockfile.Package{}
	env.AssertLockfile(projectDir, func(lock *lockfile.LockFile) {
		for _, name := range lock.List() {
			before[name], _ = lock.Get(name)
		}
	})

	if err := cli.RunRetreat(true, false); err != nil {
		t.Fatalf("Failed to retreat: %v", err)
	}

	out := captureStdout(t, func() {
		if err := cli.RunRestore(); err != nil {
			t.Errorf("Failed to restore: %v", err)
		}
	})

	for _, name := range []string{"restore-pkg-a", "restore-pkg-b"} {
		env.AssertSymlinkExists(projectDir, name)
		env.AssertStoreCopy(projectDir, name)
		env.AssertPackageJSON(projectDir, name, "file:.lnpm/"+name)
		env.AssertDatabaseLink(name, projectDir)
		if want := "Restored " + name + "@1.0.0"; !strings.Contains(out, want) {
			t.Errorf("Expected the report to contain %q, got:\n%s", want, out)
		}
	}

	// .lnpm/ is lnpm's own working directory; retreat un-ignores it, so a
	// restore that did not put the entry back would leave the linked copies
	// staged for commit.
	env.AssertGitignore(projectDir, ".lnpm/", true)

	// The snapshot is spent once every package in it is linked again.
	env.AssertFileExists(lockfile.RetreatPath(projectDir), false)

	env.AssertLockfileExists(projectDir, true)
	env.AssertLockfile(projectDir, func(lock *lockfile.LockFile) {
		for name, want := range before {
			got, ok := lock.Get(name)
			if !ok {
				t.Fatalf("Expected %s back in lnpm.lock", name)
			}
			// Linked is a timestamp of this restore, so it is deliberately not
			// compared; everything that describes the link itself must match.
			if got.Version != want.Version || got.Hash != want.Hash ||
				got.Source != want.Source || got.OriginalVersion != want.OriginalVersion {
				t.Errorf("lnpm.lock entry for %s = %+v, want the pre-retreat %+v", name, got, want)
			}
		}
		if len(lock.List()) != len(before) {
			t.Errorf("Expected lnpm.lock to hold %d packages, got %v", len(before), lock.List())
		}
	})
}

// TestRestoreWithoutSnapshotReportsNothingToRestore covers the criterion that a
// restore with no prior retreat says so plainly instead of failing.
func TestRestoreWithoutSnapshotReportsNothingToRestore(t *testing.T) {
	env := setupTest(t)

	projectDir := env.newProject("no-snapshot-project")

	out := captureStdout(t, func() {
		if err := cli.RunRestore(); err != nil {
			t.Errorf("Expected restore with no snapshot to succeed, got: %v", err)
		}
	})

	if !strings.Contains(out, "othing to restore") {
		t.Errorf("Expected a 'nothing to restore' message, got:\n%s", out)
	}
	env.AssertLockfileExists(projectDir, false)
	env.AssertDirectoryExists(filepath.Join(projectDir, ".lnpm"), false)
}

// TestRestoreReportsACollectedBuildAndContinues covers the criterion that a
// package restore cannot put back is reported per package and does not abort the
// rest of the restore.
//
// Restore resolves through the content hash the snapshot recorded, so a
// republish no longer strands the entry - the build it names is still in the
// store beside the newer one. What does strand it is gc collecting that build,
// which after a retreat is exactly what happens: nothing links it and no tag
// names it. The healthy package is tagged to keep gc off it, since nothing links
// that either.
func TestRestoreReportsACollectedBuildAndContinues(t *testing.T) {
	env := setupTest(t)

	env.simplePkg("stale-restore-pkg")
	env.simplePkg("fresh-restore-pkg")
	projectDir := env.newProject("stale-restore-project")
	env.addPkg(projectDir, "stale-restore-pkg", false, false)
	env.addPkg(projectDir, "fresh-restore-pkg", false, false)

	if err := cli.RunRetreat(true, false); err != nil {
		t.Fatalf("Failed to retreat: %v", err)
	}

	if err := cli.RunTag("fresh-restore-pkg", "keep", false); err != nil {
		t.Fatalf("Failed to tag the healthy package: %v", err)
	}
	captureStdout(t, func() {
		if err := cli.RunGC(false, "", false, true); err != nil {
			t.Fatalf("Failed to collect the unreferenced build: %v", err)
		}
	})
	env.chdir(projectDir)

	var err error
	out := captureStdout(t, func() { err = cli.RunRestore() })
	if err == nil {
		t.Error("Expected restore to exit non-zero when a package could not be restored")
	}

	want := "stale-restore-pkg@1.0.0 (hash "
	if !strings.Contains(out, want) || !strings.Contains(out, "is no longer in the store") {
		t.Errorf("Expected the report to name the collected build (%q), got:\n%s", want, out)
	}

	// The healthy package is restored regardless.
	env.AssertSymlinkExists(projectDir, "fresh-restore-pkg")
	env.AssertPackageJSON(projectDir, "fresh-restore-pkg", "file:.lnpm/fresh-restore-pkg")
	env.AssertSymlinkMissing(projectDir, "stale-restore-pkg")
	env.AssertPackageJSONMissing(projectDir, "stale-restore-pkg")

	// The snapshot survives an incomplete restore, so the user can republish the
	// stale package and run restore again.
	env.AssertFileExists(lockfile.RetreatPath(projectDir), true)
}

// TestRestoreMergesWithPackagesAddedSinceRetreat pins the chosen answer to the
// issue's open question: restore merges, and a package added since the retreat
// wins over the snapshot's entry for the same name. Nothing is removed and no
// newer add is reported as a stale version.
func TestRestoreMergesWithPackagesAddedSinceRetreat(t *testing.T) {
	env := setupTest(t)

	readdedDir := env.simplePkg("readded-pkg")
	env.simplePkg("snapshot-only-pkg")
	env.simplePkg("added-later-pkg")
	projectDir := env.newProject("merge-restore-project")
	env.addPkg(projectDir, "readded-pkg", false, false)
	env.addPkg(projectDir, "snapshot-only-pkg", false, false)

	if err := cli.RunRetreat(true, false); err != nil {
		t.Fatalf("Failed to retreat: %v", err)
	}

	// Between retreat and restore the user republishes and re-adds one of the
	// snapshot's packages, and adds a package the snapshot never saw.
	env.republish(readdedDir, "readded-pkg", "2.0.0", "module.exports = 'v2';")
	env.addPkg(projectDir, "readded-pkg", false, false)
	env.addPkg(projectDir, "added-later-pkg", false, false)

	if err := cli.RunRestore(); err != nil {
		t.Fatalf("Failed to restore: %v", err)
	}

	env.AssertLockfile(projectDir, func(lock *lockfile.LockFile) {
		readded, ok := lock.Get("readded-pkg")
		if !ok {
			t.Fatal("Expected readded-pkg to survive the restore")
		}
		if readded.Version != "2.0.0" {
			t.Errorf("Expected the newer add of readded-pkg to win at 2.0.0, got %q", readded.Version)
		}
		if !lock.Has("snapshot-only-pkg") {
			t.Error("Expected snapshot-only-pkg to be restored from the snapshot")
		}
		if !lock.Has("added-later-pkg") {
			t.Error("Expected added-later-pkg, added after the retreat, to be left alone")
		}
	})
	env.AssertSymlinkExists(projectDir, "snapshot-only-pkg")
	env.AssertSymlinkExists(projectDir, "added-later-pkg")
	env.AssertFileExists(lockfile.RetreatPath(projectDir), false)
}

// TestRestoreKeepsADevDependencyInDevDependencies is the dev-ness half of a
// faithful restore. The lock file records no dev flag, so restore recovers the
// field from package.json, which retreat has just put the original specifier
// back into. AssertPackageJSON accepts either field, so this reads the file
// directly.
func TestRestoreKeepsADevDependencyInDevDependencies(t *testing.T) {
	env := setupTest(t)

	env.simplePkg("dev-restore-pkg")
	projectDir := env.newProject("dev-restore-project")
	env.writePackageJSON(projectDir, map[string]interface{}{
		"name":            "dev-restore-project",
		"version":         "1.0.0",
		"devDependencies": map[string]interface{}{"dev-restore-pkg": "^1.0.0"},
	})
	env.addPkg(projectDir, "dev-restore-pkg", true, false)

	if err := cli.RunRetreat(true, false); err != nil {
		t.Fatalf("Failed to retreat: %v", err)
	}
	if err := cli.RunRestore(); err != nil {
		t.Fatalf("Failed to restore: %v", err)
	}

	deps, devDeps := dependencyFields(t, projectDir)
	if got := devDeps["dev-restore-pkg"]; got != "file:.lnpm/dev-restore-pkg" {
		t.Errorf("devDependencies[dev-restore-pkg] = %v, want file:.lnpm/dev-restore-pkg", got)
	}
	if _, ok := deps["dev-restore-pkg"]; ok {
		t.Error("Expected dev-restore-pkg to stay out of dependencies")
	}
}

// TestRestoreReportsAnUnknownDependencyField covers the one part of the
// pre-retreat state that is genuinely unrecoverable. A package added with --dev
// or --pure that package.json never held before has no original specifier in the
// lock file, and retreat leaves nothing in package.json, so by the time restore
// runs nothing on disk says which field it belonged in - or whether it belonged
// in one at all. Restore puts it in dependencies, matching plain `lnpm add`, and
// says so rather than moving a dev dependency into production dependencies, or
// giving a --pure package the package.json entry it was added to avoid, without
// a word.
func TestRestoreReportsAnUnknownDependencyField(t *testing.T) {
	cases := []struct {
		name string
		pkg  string
		dev  bool
		pure bool
	}{
		{"added with --dev", "orphan-dev-pkg", true, false},
		{"added with --pure", "orphan-pure-pkg", false, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := setupTest(t)

			env.simplePkg(tc.pkg)
			projectDir := env.newProject("orphan-field-project")
			env.addPkg(projectDir, tc.pkg, tc.dev, tc.pure)

			if err := cli.RunRetreat(true, false); err != nil {
				t.Fatalf("Failed to retreat: %v", err)
			}

			out := captureStdout(t, func() {
				if err := cli.RunRestore(); err != nil {
					t.Errorf("Failed to restore: %v", err)
				}
			})

			// Matched against the note itself, not just the package name and the
			// word "dependencies": both of those also appear in the success line
			// and in the npm install tip, so a looser check passes with the note
			// removed.
			want := tc.pkg + " was not in package.json before the retreat, so its field is unknown; restoring it into dependencies"
			if !strings.Contains(out, want) {
				t.Errorf("Expected the report to contain %q, got:\n%s", want, out)
			}
			// A --pure package is the case the note has to be explicit about:
			// restore writes an entry --pure exists to avoid, and only the user
			// knows that, so the note has to say what to do about it.
			if !strings.Contains(out, "--pure") {
				t.Errorf("Expected the note to tell a --pure user the entry is spurious, got:\n%s", out)
			}

			deps, _ := dependencyFields(t, projectDir)
			if got := deps[tc.pkg]; got != "file:.lnpm/"+tc.pkg {
				t.Errorf("dependencies[%s] = %v, want file:.lnpm/%s", tc.pkg, got, tc.pkg)
			}
		})
	}
}

// collectUnreferencedBuilds takes every build no link and no tag reaches out of
// the isolated store, which after a retreat is every build the project had. The
// packages a caller needs to survive are named in keep and tagged first, since
// gc keeps any version a non-default tag names.
//
// It is how a restore is made to fail on purpose. Republishing no longer does
// it: restore resolves the snapshot's content hash, and the build that hash
// names stays in the store beside whatever was published after it.
func collectUnreferencedBuilds(t *testing.T, keep ...string) {
	t.Helper()

	for _, name := range keep {
		if err := cli.RunTag(name, "keep", false); err != nil {
			t.Fatalf("Failed to tag %s to keep gc off it: %v", name, err)
		}
	}
	captureStdout(t, func() {
		if err := cli.RunGC(false, "", false, true); err != nil {
			t.Fatalf("Failed to collect unreferenced builds: %v", err)
		}
	})
}

// TestRestoreCountsOnlyThePackagesItAttempted pins the denominator of the
// failure summary. Packages restore skipped because the user re-added them were
// never attempted, so counting them makes the failure look like a smaller
// fraction of the work than it was.
func TestRestoreCountsOnlyThePackagesItAttempted(t *testing.T) {
	env := setupTest(t)

	readdedDir := env.simplePkg("count-readded-pkg")
	env.simplePkg("count-stale-pkg")
	env.simplePkg("count-ok-pkg")
	projectDir := env.newProject("count-restore-project")
	env.addPkg(projectDir, "count-readded-pkg", false, false)
	env.addPkg(projectDir, "count-stale-pkg", false, false)
	env.addPkg(projectDir, "count-ok-pkg", false, false)

	if err := cli.RunRetreat(true, false); err != nil {
		t.Fatalf("Failed to retreat: %v", err)
	}

	// One package is re-added since the retreat, so restore skips it untouched;
	// another has had its build collected, so restore cannot put it back. Only
	// two of the three are ever attempted.
	env.republish(readdedDir, "count-readded-pkg", "2.0.0", "module.exports = 'v2';")
	env.addPkg(projectDir, "count-readded-pkg", false, false)
	collectUnreferencedBuilds(t, "count-ok-pkg")
	env.chdir(projectDir)

	var err error
	captureStdout(t, func() { err = cli.RunRestore() })
	if err == nil {
		t.Fatal("Expected restore to exit non-zero when a package could not be restored")
	}
	if want := "1 of 2 package(s) failed to restore"; err.Error() != want {
		t.Errorf("restore error = %q, want %q", err.Error(), want)
	}
}

// TestRestoreRemovesAnEmptySnapshot covers the snapshot a retreat with a lock
// file holding no packages leaves behind. There is nothing in it to restore, but
// it is still a snapshot, and leaving it on disk means every later restore stops
// on it and no later retreat can be told apart from a first one.
func TestRestoreRemovesAnEmptySnapshot(t *testing.T) {
	env := setupTest(t)

	projectDir := env.newProject("empty-snapshot-project")
	env.writeFile(lockfile.RetreatPath(projectDir), "version: 1\npackages: {}\n")

	out := captureStdout(t, func() {
		if err := cli.RunRestore(); err != nil {
			t.Errorf("Expected restore of an empty snapshot to succeed, got: %v", err)
		}
	})

	if !strings.Contains(out, "othing to restore") {
		t.Errorf("Expected a 'nothing to restore' message, got:\n%s", out)
	}
	env.AssertFileExists(lockfile.RetreatPath(projectDir), false)
}

// TestRestoreNamesTheSnapshotInAReadError covers the wording of a corrupt
// snapshot's error. The snapshot and the lock file share a format and a reader,
// so a message that says "lock file" gives one artifact two names and sends the
// user to look at a file that is fine.
func TestRestoreNamesTheSnapshotInAReadError(t *testing.T) {
	env := setupTest(t)

	projectDir := env.newProject("corrupt-snapshot-project")
	env.writeFile(lockfile.RetreatPath(projectDir), "packages: {not valid yaml")

	err := cli.RunRestore()
	if err == nil {
		t.Fatal("Expected an error for a corrupt snapshot, got nil")
	}
	if !strings.Contains(err.Error(), "lnpm.lock.retreat") {
		t.Errorf("Expected the error to name the snapshot, got: %v", err)
	}
	if strings.Contains(err.Error(), "lock file") {
		t.Errorf("Expected the error not to also call the snapshot a lock file, got: %v", err)
	}
}

// TestRestoreMarkersComeFromTheIconHelpers sweeps restore's report for the
// decorative markers, so a line printing "✓"/"⚠"/"💡" as a string literal instead
// of calling the helpers is caught. It is the same guard retreat and doctor
// carry, for the same reason and with the same limits: see
// TestRetreatMarkersComeFromTheIconHelpers on what capturing stdout does and
// does not prove.
func TestRestoreMarkersComeFromTheIconHelpers(t *testing.T) {
	cases := []struct {
		name string
		// collected runs gc after the retreat, which takes the recorded build
		// out of the store - nothing links it and no tag names it - so restore
		// reports a failure.
		collected bool
		// dev adds the package with --dev and no package.json entry, so restore
		// cannot tell which field it belonged in and reports a warning.
		dev  bool
		want string
	}{
		{name: "package restored", want: "Restored glyph-restore-pkg@1.0.0"},
		{name: "build no longer in the store", collected: true, want: "is no longer in the store"},
		{name: "dependency field unknown", dev: true, want: "so its field is unknown"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("NO_COLOR", "1")
			env := setupTest(t)

			env.simplePkg("glyph-restore-pkg")
			projectDir := env.newProject("glyph-restore-project")
			env.addPkg(projectDir, "glyph-restore-pkg", tc.dev, false)

			if err := cli.RunRetreat(true, false); err != nil {
				t.Fatalf("Failed to retreat: %v", err)
			}
			if tc.collected {
				captureStdout(t, func() {
					if err := cli.RunGC(false, "", false, true); err != nil {
						t.Fatalf("Failed to collect the unreferenced build: %v", err)
					}
				})
			}
			env.chdir(projectDir)

			out := captureStdout(t, func() { _ = cli.RunRestore() })

			if !strings.Contains(out, tc.want) {
				t.Fatalf("Expected the report to contain %q, got:\n%s", tc.want, out)
			}
			assertNoRawGlyphs(t, out)
		})
	}
}

// dependencyFields returns the project's dependencies and devDependencies maps,
// for the assertions that care which of the two an entry landed in.
func dependencyFields(t *testing.T, projectDir string) (deps, devDeps map[string]interface{}) {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(projectDir, "package.json"))
	if err != nil {
		t.Fatalf("Failed to read package.json: %v", err)
	}
	var pkgJSON map[string]interface{}
	if err := json.Unmarshal(data, &pkgJSON); err != nil {
		t.Fatalf("Failed to parse package.json: %v", err)
	}
	return getMapValue(pkgJSON, "dependencies"), getMapValue(pkgJSON, "devDependencies")
}

// TestRestoreRerunDoesNotReportItsOwnWorkAsAReAdd covers the re-run restore
// itself advises after a partial failure, in the multi-package project where the
// first run got some of the way.
//
// The snapshot has to survive that run, because the package that failed still
// needs it. Kept whole it would also still name the package the run restored,
// and the re-run would find that name in the lock file - which is otherwise only
// true of a package the user added again - and report restore's own work back to
// them as theirs. The same stale entry would let a later restore re-link a
// package the user has since removed on purpose.
//
// So each name leaves the snapshot as it is dealt with, and what is on disk is
// the work still outstanding.
func TestRestoreRerunDoesNotReportItsOwnWorkAsAReAdd(t *testing.T) {
	env := setupTest(t)

	env.simplePkg("prune-ok-pkg")
	env.simplePkg("prune-stale-pkg")
	projectDir := env.newProject("prune-restore-project")
	env.addPkg(projectDir, "prune-ok-pkg", false, false)
	env.addPkg(projectDir, "prune-stale-pkg", false, false)

	if err := cli.RunRetreat(true, false); err != nil {
		t.Fatalf("Failed to retreat: %v", err)
	}

	// Collecting the build the snapshot recorded strands its entry, so the first
	// run restores one package and fails on the other.
	collectUnreferencedBuilds(t, "prune-ok-pkg")
	env.chdir(projectDir)

	var err error
	captureStdout(t, func() { err = cli.RunRestore() })
	if err == nil {
		t.Fatal("Expected the first run to exit non-zero, got nil")
	}
	env.AssertSymlinkExists(projectDir, "prune-ok-pkg")

	snapshot, err := lockfile.LoadRetreat(projectDir)
	if err != nil {
		t.Fatalf("Failed to load the retreat snapshot: %v", err)
	}
	if snapshot == nil {
		t.Fatal("Expected the snapshot to survive a run that failed, got none")
	}
	if snapshot.Has("prune-ok-pkg") {
		t.Errorf("Expected the restored package to be gone from the snapshot, it holds %v", snapshot.List())
	}
	if !snapshot.Has("prune-stale-pkg") {
		t.Errorf("Expected the package that failed to stay in the snapshot, it holds %v", snapshot.List())
	}

	out := captureStdout(t, func() { _ = cli.RunRestore() })

	if strings.Contains(out, "prune-ok-pkg was added again since the retreat") {
		t.Errorf("Expected the re-run not to report the first run's own work as a re-add, got:\n%s", out)
	}
	env.AssertSymlinkExists(projectDir, "prune-ok-pkg")
	env.AssertPackageJSON(projectDir, "prune-ok-pkg", "file:.lnpm/prune-ok-pkg")
}

// TestRestoreDropsAReAddedPackageFromTheSnapshot is the other name restore is
// finished with the moment it has read it. A package the user added again is
// left alone, and there is nothing further the snapshot's entry for it can ever
// do - except be restored over a later 'lnpm remove' of that same package, or
// have the skip reported again on every re-run.
func TestRestoreDropsAReAddedPackageFromTheSnapshot(t *testing.T) {
	env := setupTest(t)

	readdedDir := env.simplePkg("skip-readded-pkg")
	env.simplePkg("skip-stale-pkg")
	projectDir := env.newProject("skip-restore-project")
	env.addPkg(projectDir, "skip-readded-pkg", false, false)
	env.addPkg(projectDir, "skip-stale-pkg", false, false)

	if err := cli.RunRetreat(true, false); err != nil {
		t.Fatalf("Failed to retreat: %v", err)
	}

	// One package is added again by the user, so restore skips it; the other has
	// had its build collected, so the run fails and the snapshot is kept.
	env.republish(readdedDir, "skip-readded-pkg", "2.0.0", "module.exports = 'v2';")
	env.addPkg(projectDir, "skip-readded-pkg", false, false)
	collectUnreferencedBuilds(t)
	env.chdir(projectDir)

	captureStdout(t, func() { _ = cli.RunRestore() })

	snapshot, err := lockfile.LoadRetreat(projectDir)
	if err != nil {
		t.Fatalf("Failed to load the retreat snapshot: %v", err)
	}
	if snapshot == nil {
		t.Fatal("Expected the snapshot to survive a run that failed, got none")
	}
	if snapshot.Has("skip-readded-pkg") {
		t.Errorf("Expected the re-added package to be gone from the snapshot, it holds %v", snapshot.List())
	}
}

// TestRestoreTipNamesTheProjectsInstallCommand pins restore's call site of the
// #384 tip, for the reason the two add tests in add_test.go give: the helper's
// own unit test drives printPeerDependencyTip directly and so cannot tell
// whether any command still spells the command out itself.
//
// restored > 0 is what gates the tip, so the snapshot has to be restored
// cleanly rather than merely read - a run where every package failed prints the
// re-run advice instead and would pass a weaker assertion.
func TestRestoreTipNamesTheProjectsInstallCommand(t *testing.T) {
	env := setupTest(t)

	env.simplePkg("tip-restore-pkg")
	projectDir := env.newProject("tip-restore-project")
	env.addPkg(projectDir, "tip-restore-pkg", false, false)
	env.writeFile(filepath.Join(projectDir, "pnpm-lock.yaml"), "lockfileVersion: '9.0'\n")

	if err := cli.RunRetreat(true, false); err != nil {
		t.Fatalf("Failed to retreat: %v", err)
	}
	env.chdir(projectDir)

	var err error
	out := captureStdout(t, func() { err = cli.RunRestore() })

	if err != nil {
		t.Fatalf("RunRestore() = %v, want nil; output was:\n%s", err, out)
	}
	const want = "Run 'pnpm install' if you need to resolve peer dependencies"
	if !strings.Contains(out, want) {
		t.Errorf("restore's tip = want it to contain %q, output was:\n%s", want, out)
	}
}
