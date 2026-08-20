package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/cli"
)

// TestPushPropagatesChanges table-drives the "publish, add, modify source, push,
// assert the linked project saw the change" flow across a few modification
// shapes: a plain content edit, a version bump, and a newly-added file. They all
// share one assertion shape — the linked file in .lnpm reflects the change.
func TestPushPropagatesChanges(t *testing.T) {
	cases := []struct {
		name string
		// mutate applies the source change in pkgDir and returns the
		// (relativePath, expectedContent) the linked project must end up with.
		mutate func(env *TestEnvironment, pkgDir string) (rel, want string)
	}{
		{
			name: "content edit",
			mutate: func(env *TestEnvironment, pkgDir string) (string, string) {
				env.writeFile(filepath.Join(pkgDir, "index.js"), "module.exports = 'v2';")
				return "index.js", "module.exports = 'v2';"
			},
		},
		{
			name: "version bump with content edit",
			mutate: func(env *TestEnvironment, pkgDir string) (string, string) {
				env.writeFile(filepath.Join(pkgDir, "package.json"), `{"name":"push-pkg","version":"2.0.0"}`)
				env.writeFile(filepath.Join(pkgDir, "index.js"), "module.exports = 'v2';")
				return "index.js", "module.exports = 'v2';"
			},
		},
		{
			name: "new file added",
			mutate: func(env *TestEnvironment, pkgDir string) (string, string) {
				libDir := filepath.Join(pkgDir, "lib")
				if err := os.MkdirAll(libDir, 0755); err != nil {
					env.t.Fatalf("Failed to create lib dir: %v", err)
				}
				env.writeFile(filepath.Join(libDir, "utils.js"), "exports.util = () => 'util';")
				return filepath.Join("lib", "utils.js"), "exports.util = () => 'util';"
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := setupTest(t)

			pkgDir, projectDir := env.publishAndAdd("push-pkg")

			env.chdir(pkgDir)
			rel, want := tc.mutate(env, pkgDir)

			if err := cli.RunPush(false); err != nil {
				t.Fatalf("Failed to push: %v", err)
			}
			env.AssertLinkedFileContent(projectDir, "push-pkg", rel, want)
		})
	}
}

// TestPushNoChanges tests push when no changes exist leaves the package's
// version and content hash untouched.
func TestPushNoChanges(t *testing.T) {
	env := setupTest(t)

	env.simplePkg("nochange-pkg")

	pkgBefore, err := env.Database.GetPackageByName("nochange-pkg")
	if err != nil || pkgBefore == nil {
		t.Fatalf("Failed to get package before push: %v", err)
	}

	if err := cli.RunPush(false); err != nil {
		t.Fatalf("Failed to push: %v", err)
	}

	pkgAfter, err := env.Database.GetPackageByName("nochange-pkg")
	if err != nil || pkgAfter == nil {
		t.Fatalf("Failed to get package after push: %v", err)
	}
	if pkgAfter.Version != pkgBefore.Version {
		t.Errorf("Expected version %s unchanged, got %s", pkgBefore.Version, pkgAfter.Version)
	}
	if pkgAfter.ContentHash != pkgBefore.ContentHash {
		t.Errorf("Expected content hash unchanged, got %s (was %s)", pkgAfter.ContentHash, pkgBefore.ContentHash)
	}
}

// TestPushIdempotentNoChange verifies that re-pushing an unchanged package is a
// safe no-op: the content hash is unchanged and the linked project still has the
// correct file content. (Push has no --force flag; this exercises the
// "hash unchanged" branch of RunPush, which reuses the existing store path.)
func TestPushIdempotentNoChange(t *testing.T) {
	env := setupTest(t)

	pkgDir, projectDir := env.publishAndAdd("idempotent-pkg")

	pkgBefore, err := env.Database.GetPackageByName("idempotent-pkg")
	if err != nil || pkgBefore == nil {
		t.Fatalf("Failed to get package before push: %v", err)
	}
	hashBefore := pkgBefore.ContentHash

	env.chdir(pkgDir)
	if err := cli.RunPush(false); err != nil {
		t.Fatalf("Failed to push: %v", err)
	}

	pkgAfter, err := env.Database.GetPackageByName("idempotent-pkg")
	if err != nil || pkgAfter == nil {
		t.Fatalf("Failed to get package after push: %v", err)
	}
	if pkgAfter.ContentHash != hashBefore {
		t.Errorf("Expected content hash to be unchanged after no-op push, got %s (was %s)",
			pkgAfter.ContentHash, hashBefore)
	}

	env.AssertLinkedFileContent(projectDir, "idempotent-pkg", "index.js", "module.exports = 'idempotent-pkg';")
}

// TestPushMultipleProjects tests pushing a change to multiple linked projects.
func TestPushMultipleProjects(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.simplePkg("shared-pkg")

	projects := []string{"project-1", "project-2", "project-3"}
	projectDirs := make(map[string]string)
	for _, name := range projects {
		projectDir := env.newProject(name)
		projectDirs[name] = projectDir
		env.addPkg(projectDir, "shared-pkg", false, false)
	}

	env.chdir(pkgDir)
	env.writeFile(filepath.Join(pkgDir, "index.js"), "module.exports = 'v2';")

	if err := cli.RunPush(false); err != nil {
		t.Fatalf("Failed to push: %v", err)
	}

	for _, dir := range projectDirs {
		env.AssertLinkedFileContent(dir, "shared-pkg", "index.js", "module.exports = 'v2';")
	}
}

// TestPushUnpublishedPackage tests that pushing an unpublished package publishes it.
func TestPushUnpublishedPackage(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.CreateTestPackage("unpublished-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'test';",
	})
	env.chdir(pkgDir)

	if err := cli.RunPush(false); err != nil {
		t.Fatalf("Failed to push unpublished package: %v", err)
	}
	env.AssertPackageInDatabase("unpublished-pkg", true)
}

// TestPushNoLinkedProjects tests that pushing a modified package with no linked
// projects still updates the store (content hash changes).
func TestPushNoLinkedProjects(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.simplePkg("standalone-pkg")

	pkgBefore, err := env.Database.GetPackageByName("standalone-pkg")
	if err != nil || pkgBefore == nil {
		t.Fatalf("Failed to get package before push: %v", err)
	}
	hashBefore := pkgBefore.ContentHash

	env.writeFile(filepath.Join(pkgDir, "index.js"), "module.exports = 'updated';")
	if err := cli.RunPush(false); err != nil {
		t.Fatalf("Failed to push: %v", err)
	}

	pkgAfter, err := env.Database.GetPackageByName("standalone-pkg")
	if err != nil || pkgAfter == nil {
		t.Fatalf("Failed to get package after push: %v", err)
	}
	if pkgAfter.ContentHash == hashBefore {
		t.Errorf("Expected content hash to change after pushing a modified package, but it stayed %s", hashBefore)
	}
}

// TestPushConcurrentSafe verifies that RunPush's internal parallel fan-out
// (it links to every linked project concurrently, one goroutine per project)
// is race-free and delivers the update to every project. Run with -race for the
// strongest signal: `go test -race -run TestPushConcurrentSafe ./tests/`.
func TestPushConcurrentSafe(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.simplePkg("concurrent-pkg")

	const numProjects = 5
	projectDirs := make([]string, 0, numProjects)
	for i := 0; i < numProjects; i++ {
		projectDir := env.newProject("project-" + string(rune('a'+i)))
		projectDirs = append(projectDirs, projectDir)
		env.addPkg(projectDir, "concurrent-pkg", false, false)
	}

	env.chdir(pkgDir)
	env.writeFile(filepath.Join(pkgDir, "index.js"), "module.exports = 'v2';")

	if err := cli.RunPush(false); err != nil {
		t.Fatalf("Failed to push: %v", err)
	}

	// Every project must have received the update — proves the concurrent
	// fan-out didn't drop or corrupt any link.
	for _, dir := range projectDirs {
		env.AssertLinkedFileContent(dir, "concurrent-pkg", "index.js", "module.exports = 'v2';")
	}
}

// TestPushSkipsLiveLinkedConsumer covers push from the other side of the same
// hazard pull has: pushing relinks every consumer from the store, which for a
// live-linked consumer would quietly replace its link with a snapshot copy and
// end the live updates it was added for.
func TestPushSkipsLiveLinkedConsumer(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.simplePkg("push-live-lib")
	projectDir := env.newProject("push-live-project")
	env.addLinkedPkg(projectDir, "push-live-lib")

	env.chdir(pkgDir)
	env.writeFile(filepath.Join(pkgDir, "index.js"), "module.exports = 'pushed';")
	out := captureStdout(t, func() {
		if err := cli.RunPush(false); err != nil {
			t.Fatalf("RunPush: %v", err)
		}
	})

	if !strings.Contains(out, "live link") {
		t.Errorf("Expected push to report the live link, got:\n%s", out)
	}
	env.AssertLiveLink(projectDir, "push-live-lib", pkgDir)
	env.AssertFileContent(filepath.Join(projectDir, "node_modules", "push-live-lib", "index.js"),
		"module.exports = 'pushed';")
}

// TestPushReportsCoherentProjectCounts pins push's summary line against the
// count it announced: the denominator stays every project considered, and a
// live-linked project is reported as a skip rather than removed from the total.
// The two lines used to contradict each other - "Updating 2 linked projects"
// then "Pushed to 1/1 projects" - and an all-live push reported "0/0".
func TestPushReportsCoherentProjectCounts(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.simplePkg("count-lib")
	copyProject := env.newProject("count-copy-project")
	env.addPkg(copyProject, "count-lib", false, false)
	liveProject := env.newProject("count-live-project")
	env.addLinkedPkg(liveProject, "count-lib")

	env.chdir(pkgDir)
	env.writeFile(filepath.Join(pkgDir, "index.js"), "module.exports = 'v2';")
	out := captureStdout(t, func() {
		if err := cli.RunPush(false); err != nil {
			t.Fatalf("RunPush: %v", err)
		}
	})

	if !strings.Contains(out, "Updating 2 linked projects") {
		t.Errorf("Expected 2 projects to be announced, got:\n%s", out)
	}
	if !strings.Contains(out, "Pushed to 1/2 projects (1 skipped: live link to source)") {
		t.Errorf("Expected a coherent 1/2 summary naming the skip, got:\n%s", out)
	}

	env.AssertLinkedFileContent(copyProject, "count-lib", "index.js", "module.exports = 'v2';")
	env.AssertLiveLink(liveProject, "count-lib", pkgDir)
}

// statLinkedFiles returns the identity of each relative path inside a linked
// package, keyed by that path, for comparison with os.SameFile across a push.
func statLinkedFiles(t *testing.T, projectDir, pkg string, rels ...string) map[string]os.FileInfo {
	t.Helper()

	stats := make(map[string]os.FileInfo, len(rels))
	for _, rel := range rels {
		stats[rel] = fileIdentity(t, filepath.Join(projectDir, ".lnpm", pkg, filepath.FromSlash(rel)))
	}
	return stats
}

// TestPushOnlyRewritesTheFilesThatChanged is acceptance criterion 2 end to end:
// a push that changes one file leaves every other file in .lnpm/<pkg> exactly
// as it was, identity included, rather than rebuilding the whole package.
func TestPushOnlyRewritesTheFilesThatChanged(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.publishPkg("incremental-pkg", "1.0.0", map[string]string{
		"index.js":  "module.exports = 'v1';",
		"lib/a.js":  "exports.a = 1;",
		"lib/b.js":  "exports.b = 2;",
		"README.md": "# incremental-pkg\n",
	})
	projectDir := env.newProject("test-project")
	env.addPkg(projectDir, "incremental-pkg", false, false)

	untouched := []string{"package.json", "index.js", "lib/a.js", "README.md"}
	before := statLinkedFiles(t, projectDir, "incremental-pkg", append(untouched, "lib/b.js")...)

	env.chdir(pkgDir)
	env.writeFile(filepath.Join(pkgDir, "lib", "b.js"), "exports.b = 99;")
	if err := cli.RunPush(false); err != nil {
		t.Fatalf("Failed to push: %v", err)
	}

	after := statLinkedFiles(t, projectDir, "incremental-pkg", append(untouched, "lib/b.js")...)
	for _, rel := range untouched {
		if !os.SameFile(before[rel], after[rel]) {
			t.Errorf("%s is a different file after the push, so it was rewritten even though it did not change", rel)
		}
	}
	if os.SameFile(before["lib/b.js"], after["lib/b.js"]) {
		t.Error("lib/b.js kept its identity across the push, so the edit was never written")
	}
	env.AssertLinkedFileContent(projectDir, "incremental-pkg", filepath.Join("lib", "b.js"), "exports.b = 99;")
}

// TestPushReportsChangedAndUnchangedCounts is acceptance criterion 5: the push
// summary says how much of the package it actually rewrote.
func TestPushReportsChangedAndUnchangedCounts(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.publishPkg("counted-pkg", "1.0.0", map[string]string{
		"index.js": "module.exports = 'v1';",
		"lib/a.js": "exports.a = 1;",
		"lib/b.js": "exports.b = 2;",
	})
	projectDir := env.newProject("test-project")
	env.addPkg(projectDir, "counted-pkg", false, false)

	env.chdir(pkgDir)
	env.writeFile(filepath.Join(pkgDir, "lib", "b.js"), "exports.b = 99;")
	out := captureStdout(t, func() {
		if err := cli.RunPush(false); err != nil {
			t.Fatalf("RunPush: %v", err)
		}
	})
	if !strings.Contains(out, "(1 changed, 3 unchanged)") {
		t.Errorf("Expected the push to report 1 changed of 4 files, got:\n%s", out)
	}

	// A push with nothing to carry across says so rather than staying silent.
	out = captureStdout(t, func() {
		if err := cli.RunPush(false); err != nil {
			t.Fatalf("RunPush: %v", err)
		}
	})
	if !strings.Contains(out, "(0 changed, 4 unchanged)") {
		t.Errorf("Expected an unchanged push to report 0 changed of 4 files, got:\n%s", out)
	}
}
