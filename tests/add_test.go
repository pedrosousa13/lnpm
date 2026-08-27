package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pedrosousa13/lnpm/internal/cli"
	"github.com/pedrosousa13/lnpm/internal/config"
	"github.com/pedrosousa13/lnpm/internal/db"
	"github.com/pedrosousa13/lnpm/pkg/lockfile"
	bolt "go.etcd.io/bbolt"
)

// TestAddVariants table-drives the core "publish then add" behaviors across the
// scoped/unscoped and prod/dev permutations. Each row asserts the full set of
// side effects that are common to a successful add.
func TestAddVariants(t *testing.T) {
	cases := []struct {
		name    string
		pkgName string
		dev     bool
	}{
		{"unscoped prod dependency", "test-pkg", false},
		{"scoped prod dependency", "@test-org/scoped-pkg", false},
		{"unscoped dev dependency", "dev-pkg", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := setupTest(t)

			env.simplePkg(tc.pkgName)
			projectDir := env.newProject("test-project")
			env.addPkg(projectDir, tc.pkgName, tc.dev, false)

			// Common side effects of a successful add.
			env.AssertDirectoryExists(filepath.Join(projectDir, ".lnpm"), true)
			env.AssertFilesLinked(projectDir, tc.pkgName)
			env.AssertSymlinkExists(projectDir, tc.pkgName)
			env.AssertPackageJSON(projectDir, tc.pkgName, "file:.lnpm/"+tc.pkgName)
			env.AssertLockfileExists(projectDir, true)
			env.AssertGitignore(projectDir, ".lnpm/", true)
			env.AssertDatabaseLink(tc.pkgName, projectDir)

			// Scoped packages create an intermediate scope directory.
			if filepath.Dir(tc.pkgName) != "." {
				scope := filepath.Dir(tc.pkgName)
				env.AssertDirectoryExists(filepath.Join(projectDir, "node_modules", scope), true)
			}
		})
	}
}

// TestAddDoesNotLinkStoreMarker pins that the store's completeness marker
// stays in the store. It is written inside every entry, and what is linked
// into a consumer is enumerated from that same directory, so an added package
// would carry the store's bookkeeping file into the project.
func TestAddDoesNotLinkStoreMarker(t *testing.T) {
	env := setupTest(t)

	env.simplePkg("marker-pkg")
	projectDir := env.newProject("marker-project")
	env.addPkg(projectDir, "marker-pkg", false, false)

	marker := filepath.Join(projectDir, ".lnpm", "marker-pkg", ".lnpm-complete")
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Errorf("linked package carries the store marker %s (stat err = %v)", marker, err)
	}
}

// TestAddPreservesDevDependencyLocation tests that adding without --dev preserves
// a package's existing devDependencies location rather than moving it to deps.
func TestAddPreservesDevDependencyLocation(t *testing.T) {
	env := setupTest(t)

	env.simplePkg("preserve-pkg")

	// Project where preserve-pkg already lives in devDependencies.
	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	env.writePackageJSON(projectDir, map[string]interface{}{
		"name":    "test-project",
		"version": "1.0.0",
		"devDependencies": map[string]interface{}{
			"preserve-pkg": "^1.0.0",
		},
	})

	// Add WITHOUT --dev: should stay in devDependencies.
	env.addPkg(projectDir, "preserve-pkg", false, false)

	result := env.storedPackageJSON(projectDir)
	if deps, ok := result["dependencies"].(map[string]interface{}); ok && deps["preserve-pkg"] != nil {
		t.Error("Package should NOT be in dependencies")
	}
	devDeps := result["devDependencies"].(map[string]interface{})
	if devDeps["preserve-pkg"] != "file:.lnpm/preserve-pkg" {
		t.Errorf("Expected preserve-pkg in devDependencies with lnpm reference, got %v", devDeps["preserve-pkg"])
	}
}

// TestAddPureFlag tests adding with --pure flag (no package.json update, but all
// other linking still happens).
func TestAddPureFlag(t *testing.T) {
	env := setupTest(t)

	env.simplePkg("pure-pkg")
	projectDir := env.newProject("test-project")
	env.addPkg(projectDir, "pure-pkg", false, true)

	env.AssertPackageJSONMissing(projectDir, "pure-pkg")
	env.AssertFilesLinked(projectDir, "pure-pkg")
	env.AssertSymlinkExists(projectDir, "pure-pkg")
	env.AssertLockfileExists(projectDir, true)
}

// TestAddErrors covers the failure paths that must return a non-nil error.
func TestAddErrors(t *testing.T) {
	t.Run("package not found", func(t *testing.T) {
		env := setupTest(t)
		env.newProject("test-project")
		if err := cli.RunAdd("nonexistent-package", false, false, false); err == nil {
			t.Fatal("Expected error when adding non-existent package, got nil")
		}
	})

	t.Run("no package.json", func(t *testing.T) {
		env := setupTest(t)
		projectDir := filepath.Join(env.TempDir, "no-package-json")
		if err := os.MkdirAll(projectDir, 0755); err != nil {
			t.Fatalf("Failed to create dir: %v", err)
		}
		env.chdir(projectDir)
		if err := cli.RunAdd("some-pkg", false, false, false); err == nil {
			t.Fatal("Expected error when no package.json exists, got nil")
		}
	})
}

// TestAddIdempotent tests adding a package twice leaves it correctly linked.
func TestAddIdempotent(t *testing.T) {
	env := setupTest(t)

	_, projectDir := env.publishAndAdd("test-pkg")
	// Second add should be a no-op (idempotent), not an error.
	env.addPkg(projectDir, "test-pkg", false, false)

	env.AssertSymlinkExists(projectDir, "test-pkg")
	env.AssertDatabaseLink("test-pkg", projectDir)
}

// TestAddUpdatesExisting tests that re-adding after publishing a new version
// relinks the updated package.
func TestAddUpdatesExisting(t *testing.T) {
	env := setupTest(t)

	pkgDir, projectDir := env.publishAndAdd("test-pkg")

	// Publish v2 with new content.
	env.chdir(pkgDir)
	env.writeFile(filepath.Join(pkgDir, "package.json"), `{"name":"test-pkg","version":"2.0.0"}`)
	env.writeFile(filepath.Join(pkgDir, "index.js"), "module.exports = 'v2';")
	if err := cli.RunPublish(false, false, false, false); err != nil {
		t.Fatalf("Failed to publish v2: %v", err)
	}

	env.addPkg(projectDir, "test-pkg", false, false)

	env.AssertSymlinkExists(projectDir, "test-pkg")
	env.AssertPackageJSON(projectDir, "test-pkg", "file:.lnpm/test-pkg")
}

// TestAddConcurrentSameProject tests concurrent adds to same project.
func TestAddConcurrentSameProject(t *testing.T) {
	t.Skip("Skipping: concurrent in-process package.json writes cause race conditions, not realistic usage. Real concurrent use is covered by TestConcurrentProcessesSharedStore in tests/e2e/contention_test.go, which runs overlapping lnpm processes against one shared store.")
	env := setupTest(t)

	packages := []string{"pkg-a", "pkg-b", "pkg-c"}
	for _, name := range packages {
		env.simplePkg(name)
	}
	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)

	RunConcurrently(t,
		func() error { _ = os.Chdir(projectDir); return cli.RunAdd("pkg-a", false, false, false) },
		func() error { _ = os.Chdir(projectDir); return cli.RunAdd("pkg-b", false, false, false) },
		func() error { _ = os.Chdir(projectDir); return cli.RunAdd("pkg-c", false, false, false) },
	)

	for _, name := range packages {
		env.AssertSymlinkExists(projectDir, name)
		env.AssertDatabaseLink(name, projectDir)
	}
}

// TestAddConcurrentDifferentProjects tests concurrent adds to different projects.
func TestAddConcurrentDifferentProjects(t *testing.T) {
	t.Skip("Skipping: os.Chdir is not goroutine-safe, test creates artificial race condition. Real concurrent use is covered by TestConcurrentProcessesSharedStore in tests/e2e/contention_test.go, which runs overlapping lnpm processes against one shared store.")
	env := setupTest(t)

	env.simplePkg("shared-pkg")
	project1 := env.CreateTestPackage("project-1", "1.0.0", nil)
	project2 := env.CreateTestPackage("project-2", "1.0.0", nil)
	project3 := env.CreateTestPackage("project-3", "1.0.0", nil)

	RunConcurrently(t,
		func() error { _ = os.Chdir(project1); return cli.RunAdd("shared-pkg", false, false, false) },
		func() error { _ = os.Chdir(project2); return cli.RunAdd("shared-pkg", false, false, false) },
		func() error { _ = os.Chdir(project3); return cli.RunAdd("shared-pkg", false, false, false) },
	)

	env.AssertDatabaseLink("shared-pkg", project1)
	env.AssertDatabaseLink("shared-pkg", project2)
	env.AssertDatabaseLink("shared-pkg", project3)
}

// TestAddWithNPMWorkspace tests adding to a package inside an npm workspace.
func TestAddWithNPMWorkspace(t *testing.T) {
	env := setupTest(t)

	env.simplePkg("workspace-pkg")

	workspaceDir := env.CopyFixture("npm-workspace")
	packageADir := filepath.Join(workspaceDir, "packages", "package-a")
	env.addPkg(packageADir, "workspace-pkg", false, false)

	env.AssertSymlinkExists(packageADir, "workspace-pkg")
	env.AssertPackageJSON(packageADir, "workspace-pkg", "file:.lnpm/workspace-pkg")
}

// TestAddMultiplePackages tests adding multiple packages in one command.
func TestAddMultiplePackages(t *testing.T) {
	env := setupTest(t)

	packages := []string{"multi-pkg-a", "multi-pkg-b", "multi-pkg-c"}
	for _, name := range packages {
		env.simplePkg(name)
	}

	projectDir := env.newProject("test-project")
	if err := cli.RunAddMultiple(packages, false, false, false, false); err != nil {
		t.Fatalf("Failed to add multiple packages: %v", err)
	}

	for _, name := range packages {
		env.AssertSymlinkExists(projectDir, name)
		env.AssertPackageJSON(projectDir, name, "file:.lnpm/"+name)
		env.AssertDatabaseLink(name, projectDir)
	}
}

// TestAddMultipleWithPartialFailure tests that a partial failure surfaces a
// non-zero error while still applying the packages that succeeded.
func TestAddMultipleWithPartialFailure(t *testing.T) {
	env := setupTest(t)

	env.simplePkg("exists-pkg")
	projectDir := env.newProject("test-project")

	err := cli.RunAddMultiple([]string{"exists-pkg", "nonexistent-pkg"}, false, false, false, false)
	if err == nil {
		t.Fatal("Expected an error when one of the packages fails to add")
	}

	env.AssertSymlinkExists(projectDir, "exists-pkg")
	env.AssertPackageJSON(projectDir, "exists-pkg", "file:.lnpm/exists-pkg")
}

// TestAddLockfileContents tests that the lockfile records version, hash and source.
func TestAddLockfileContents(t *testing.T) {
	env := setupTest(t)

	env.publishPkg("lock-pkg", "1.5.0", map[string]string{
		"index.js": "module.exports = 'lock';",
	})
	projectDir := env.newProject("test-project")
	env.addPkg(projectDir, "lock-pkg", false, false)

	env.AssertLockfile(projectDir, func(lock *lockfile.LockFile) {
		pkg, ok := lock.Get("lock-pkg")
		if !ok {
			t.Fatal("Expected lock-pkg in lockfile")
		}
		if pkg.Version != "1.5.0" {
			t.Errorf("Expected version 1.5.0, got %s", pkg.Version)
		}
		if pkg.Hash == "" {
			t.Error("Expected hash to be set")
		}
		if pkg.Source == "" {
			t.Error("Expected source to be set")
		}
	})
}

// TestAddByVersion verifies that name@version resolves against the stored
// (latest) version: a matching version succeeds, a non-matching one fails
// with a clear error rather than silently matching a content hash (#39).
func TestAddByVersion(t *testing.T) {
	env := setupTest(t)

	env.publishPkg("ver-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'v1';",
	})
	projectDir := env.newProject("ver-project")

	// Matching version resolves.
	if err := cli.RunAdd("ver-pkg@1.0.0", false, false, false); err != nil {
		t.Fatalf("add ver-pkg@1.0.0 should succeed, got: %v", err)
	}
	env.AssertPackageJSON(projectDir, "ver-pkg", "file:.lnpm/ver-pkg")

	// Non-matching version fails clearly.
	if err := cli.RunAdd("ver-pkg@9.9.9", false, false, false); err == nil {
		t.Fatal("add ver-pkg@9.9.9 should fail (only 1.0.0 is published)")
	}
}

// TestAddMultipleByVersion verifies a versioned spec resolves in the
// multi-package path exactly as it does in the single-package one: the version
// is matched against the stored version, not against the content hash (#154).
func TestAddMultipleByVersion(t *testing.T) {
	env := setupTest(t)

	env.publishPkg("pkg-a", "1.2.3", map[string]string{
		"index.js": "module.exports=1",
	})
	env.simplePkg("pkg-b")

	projectDir := env.newProject("consumer")

	if err := cli.RunAddMultiple([]string{"pkg-a@1.2.3", "pkg-b"}, false, false, false, false); err != nil {
		t.Fatalf("add pkg-a@1.2.3 pkg-b should succeed, got: %v", err)
	}

	for _, name := range []string{"pkg-a", "pkg-b"} {
		env.AssertSymlinkExists(projectDir, name)
		env.AssertPackageJSON(projectDir, name, "file:.lnpm/"+name)
		env.AssertDatabaseLink(name, projectDir)
	}
}

// TestAddMultipleByVersionMismatch verifies a non-matching version fails only
// for that package, with the same message the single-package path produces, so
// the two paths cannot drift (#154).
func TestAddMultipleByVersionMismatch(t *testing.T) {
	env := setupTest(t)

	env.publishPkg("pkg-a", "1.2.3", map[string]string{
		"index.js": "module.exports=1",
	})
	env.simplePkg("pkg-b")

	projectDir := env.newProject("consumer")

	// The single-package path is authoritative for the wording.
	singleErr := cli.RunAdd("pkg-a@9.9.9", false, false, false)
	if singleErr == nil {
		t.Fatal("add pkg-a@9.9.9 should fail (only 1.2.3 is published)")
	}

	output := captureStdout(t, func() {
		if err := cli.RunAddMultiple([]string{"pkg-a@9.9.9", "pkg-b"}, false, false, false, false); err == nil {
			t.Error("add pkg-a@9.9.9 pkg-b should fail for pkg-a")
		}
	})

	if !strings.Contains(output, singleErr.Error()) {
		t.Errorf("multi-package error should match the single-package one.\nwant substring: %s\ngot output:\n%s",
			singleErr.Error(), output)
	}

	// pkg-b still linked, pkg-a not.
	env.AssertSymlinkExists(projectDir, "pkg-b")
	env.AssertPackageJSON(projectDir, "pkg-b", "file:.lnpm/pkg-b")
	env.AssertDatabaseLink("pkg-b", projectDir)

	env.AssertSymlinkMissing(projectDir, "pkg-a")
	env.AssertPackageJSONMissing(projectDir, "pkg-a")
	env.AssertDatabaseNoLink("pkg-a", projectDir)
}

// TestAddMultipleLeavesSucceedingPackageIntactWhenSiblingFails pins the other
// half of the failure contract: a package that fails leaves the lock file with
// no trace of itself, and the package beside it is recorded in full - symlink,
// package.json reference, database link, and a lock entry carrying its real
// original specifier.
//
// A package.json rewrite cannot be failed for one package alone (the batch
// shares one file), so the failing sibling here is one that never resolves in
// the store. It reaches the same end state the rewrite rollback aims for:
// reported as an error, nothing recorded.
func TestAddMultipleLeavesSucceedingPackageIntactWhenSiblingFails(t *testing.T) {
	env := setupTest(t)

	env.simplePkg("survivor-pkg")

	projectDir := env.CreateTestPackage("survivor-project", "1.0.0", nil)
	env.writePackageJSON(projectDir, map[string]interface{}{
		"name":         "survivor-project",
		"version":      "1.0.0",
		"dependencies": map[string]interface{}{"survivor-pkg": "^3.1.0"},
	})

	env.chdir(projectDir)
	captureStdout(t, func() {
		if err := cli.RunAddMultiple([]string{"survivor-pkg", "missing-pkg"}, false, false, false, false); err == nil {
			t.Error("Expected an error when one of the packages fails to add")
		}
	})

	env.AssertSymlinkExists(projectDir, "survivor-pkg")
	env.AssertPackageJSON(projectDir, "survivor-pkg", "file:.lnpm/survivor-pkg")
	env.AssertDatabaseLink("survivor-pkg", projectDir)
	assertOriginalVersion(t, env, projectDir, "survivor-pkg", "^3.1.0")

	env.AssertLockfile(projectDir, func(lock *lockfile.LockFile) {
		if lock.Has("missing-pkg") {
			t.Error("Expected no lockfile entry for the package that failed to add")
		}
	})
	env.AssertSymlinkMissing(projectDir, "missing-pkg")
}

// damageLinkIndex writes bytes straight into a link index bucket of the store's
// bbolt file, standing in for the damage a torn write leaves behind.
//
// lnpm's handle is closed first and left closed, because bbolt holds an
// exclusive file lock: the command under test reopens the database itself, which
// is also what makes the damage look exactly like damage it found on open.
func damageLinkIndex(t *testing.T, bucket string, id int64, value []byte) {
	t.Helper()

	storePath, err := config.GetStorePath()
	if err != nil {
		t.Fatalf("resolve store path: %v", err)
	}
	db.ResetForTesting()

	handle, err := bolt.Open(filepath.Join(storePath, "lnpm.db"), 0600, &bolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("open the database directly: %v", err)
	}
	key := make([]byte, 8)
	for i := 7; i >= 0; i-- {
		key[i] = byte(id)
		id >>= 8
	}
	err = handle.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte(bucket)).Put(key, value)
	})
	if closeErr := handle.Close(); closeErr != nil {
		t.Fatalf("close the database: %v", closeErr)
	}
	if err != nil {
		t.Fatalf("damage %s: %v", bucket, err)
	}
}

// TestAddMultipleFailsWhenALinkCannotBeRecorded pins the write half of #329 on
// the multi-package path - the one that used to warn and carry on.
//
// The link row is what tells gc a project consumes a package. Without it the
// store entry reads as consumed by nobody, and gc deletes what the project is
// importing. That is the same fail-open shape #329 removed from gc's own read,
// arriving instead through the command that should have written the row, so add
// must not report success over it.
//
// The single-package path already returned this error; only the loop swallowed
// it, printing "Added" per package and exiting zero. So the test drives
// RunAddMultiple with two specs, which is what routes through the loop at all.
func TestAddMultipleFailsWhenALinkCannotBeRecorded(t *testing.T) {
	env := setupTest(t)

	env.simplePkg("linked-pkg")
	env.simplePkg("second-pkg")
	projectDir := env.newProject("test-project")

	// Register the project so there is a links_by_project entry to damage, then
	// damage it. Nothing in lnpm produces this state; a torn write does.
	if err := env.Database.InsertProject(&db.Project{Path: projectDir, Name: "test-project"}); err != nil {
		t.Fatalf("Failed to register the project: %v", err)
	}
	proj, err := env.Database.GetProjectByPath(projectDir)
	if err != nil || proj == nil {
		t.Fatalf("Failed to read the project back: %v", err)
	}
	damageLinkIndex(t, "links_by_project", proj.ID, []byte("[ not ids"))

	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Failed to enter the project: %v", err)
	}
	err = cli.RunAddMultiple([]string{"linked-pkg", "second-pkg"}, false, false, false, false)
	if err == nil {
		t.Fatal("RunAddMultiple() reported success for packages whose links it could not record; gc reads those store entries as consumed by nobody and deletes them")
	}
}

// TestAddTipNamesTheProjectsInstallCommand pins the single-package half of #384
// at its call site, rather than at the helper.
//
// internal/cli's TestPrintPeerDependencyTipNamesTheProjectsInstallCommand drives
// printPeerDependencyTip directly, so it cannot see who calls it: measured, the
// hardcoded line #384 removed can be put back at any of the helper's non-retreat
// call sites and that test, go vet and the whole suite stay green. These two
// tests, TestRestoreTipNamesTheProjectsInstallCommand and internal/cli's
// TestRunRemoveTipNamesTheProjectsInstallCommand are what turns that red.
//
// There are four such sites, not three: #336 gave remove a tip of its own when
// it stopped installing unconditionally, and its guard lives beside RunRemove in
// internal/cli rather than here. Count the call sites from the code before
// trusting this number.
//
// Measured on 2026-08-27, each site reverted on its own to the literal line
// #384 removed, each run preceded by go vet ./... exiting 0 and each read for
// the FAIL <package> result line with a duration rather than for silence, since
// a revert that orphans output.go's config import fails to build instead. Read
// the split rather than the totals - the four are not symmetrical:
//
//   - add's two sites and restore's turn exactly one test red each - the one
//     named for them, all three in this package - and every other package prints
//     ok. So no two of them cover each other, and none is held up by another
//     site still calling the helper.
//   - remove's site turns three red, all in internal/cli and none here:
//     TestRunRemoveTipNamesTheProjectsInstallCommand, which is its named guard,
//     plus TestRunRemoveRunsNoInstallWithoutTheFlag and
//     TestRunRemoveAdvisesAfterAPartialRemoval. Those two match remove_test.go's
//     peerDepTip constant, which carries the --legacy-peer-deps an npm project's
//     derived command has and the reverted literal does not, so they catch the
//     revert as a side effect of asserting the tip was printed at all.
//
// The project is pnpm's, so 'npm install --legacy-peer-deps' is not merely a
// wrong-looking string here - following it rewrites package-lock.json in a
// project that keeps pnpm-lock.yaml. 'pnpm install' is also a string the old
// hardcoded line could not produce under any project, which is why the fixture
// is deliberately not npm's.
//
// runInstall is false, which is both what gates the tip and what keeps the test
// from starting a real install.
func TestAddTipNamesTheProjectsInstallCommand(t *testing.T) {
	env := setupTest(t)

	env.simplePkg("tip-single-pkg")
	projectDir := env.newProject("tip-single-project")
	env.writeFile(filepath.Join(projectDir, "pnpm-lock.yaml"), "lockfileVersion: '9.0'\n")

	var err error
	out := captureStdout(t, func() { err = cli.RunAdd("tip-single-pkg", false, false, false) })

	if err != nil {
		t.Fatalf("RunAdd() = %v, want nil; output was:\n%s", err, out)
	}
	const want = "Run 'pnpm install' if you need to resolve peer dependencies"
	if !strings.Contains(out, want) {
		t.Errorf("add's tip = want it to contain %q, output was:\n%s", want, out)
	}
}

// TestAddMultipleTipNamesTheProjectsInstallCommand is the same pin for the
// batch path, which prints its own copy of the tip after the whole batch rather
// than reaching the single-package one.
//
// Two specs is what routes through that path at all: RunAddMultiple hands a
// one-spec batch straight to runAddSingle, so a one-spec fixture here would
// exercise the call site the test above already covers and say nothing about
// this one.
func TestAddMultipleTipNamesTheProjectsInstallCommand(t *testing.T) {
	env := setupTest(t)

	env.simplePkg("tip-batch-pkg-a")
	env.simplePkg("tip-batch-pkg-b")
	projectDir := env.newProject("tip-batch-project")
	env.writeFile(filepath.Join(projectDir, "pnpm-lock.yaml"), "lockfileVersion: '9.0'\n")

	var err error
	out := captureStdout(t, func() {
		err = cli.RunAddMultiple([]string{"tip-batch-pkg-a", "tip-batch-pkg-b"}, false, false, false, false)
	})

	if err != nil {
		t.Fatalf("RunAddMultiple() = %v, want nil; output was:\n%s", err, out)
	}
	const want = "Run 'pnpm install' if you need to resolve peer dependencies"
	if !strings.Contains(out, want) {
		t.Errorf("add's batch tip = want it to contain %q, output was:\n%s", want, out)
	}
}
