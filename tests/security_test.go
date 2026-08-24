package tests

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/cli"
)

// TestPublishRejectsTraversalName proves a package whose name contains path
// traversal is rejected at publish time, even with validation skipped (the
// guard lives in the non-bypassable read path, not just ValidatePackage).
func TestPublishRejectsTraversalName(t *testing.T) {
	env := setupTest(t)

	dir := filepath.Join(env.TempDir, "evilpkg")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	pkgJSON := `{"name":"../../../../tmp/lnpm-evil","version":"1.0.0"}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkgJSON), 0644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.js"), []byte("x"), 0644); err != nil {
		t.Fatalf("write index.js: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	// push=false, all=false, skipHooks=false, skipValidation=true
	if err := cli.RunPublish(false, false, false, true); err == nil {
		t.Fatal("expected publish to reject a traversal package name, got nil error")
	}

	// The malicious target must not have been created.
	if _, err := os.Stat("/tmp/lnpm-evil"); err == nil {
		_ = os.RemoveAll("/tmp/lnpm-evil")
		t.Fatal("traversal name escaped the store: /tmp/lnpm-evil was created")
	}
}

// TestPublishKeepsMixedCaseSecretsOutOfTheStore is issue #317 at the far end of
// the publish path. lnpm's built-in exclusion list matched case-sensitively, so
// ".ENV", ".Env.local" and ".NPMRC" were published while ".env" and ".npmrc"
// were not — on macOS and Windows the same files, held back or not according to
// how the developer typed the name.
//
// internal/pack proves the packed set is right. This proves what actually landed
// on disk, because the two have diverged before (#348): the store directory is
// what a linked project reads, so it is the claim that matters to a user.
func TestPublishKeepsMixedCaseSecretsOutOfTheStore(t *testing.T) {
	env := setupTest(t)

	env.publishPkg("mixed-case-secrets", "1.0.0", map[string]string{
		".ENV":                       "SECRET=1",
		".Env.local":                 "SECRET=2",
		".NPMRC":                     "//registry:_authToken=deadbeef",
		"Node_Modules/evil/index.js": "steal()",
		".envrc":                     "use flake",
		"index.js":                   "module.exports = 'ok';",
	})

	pkg, err := env.Database.GetPackageByName("mixed-case-secrets")
	if err != nil || pkg == nil {
		t.Fatalf("Failed to get package: %v", err)
	}

	for _, rel := range []string{".ENV", ".Env.local", ".NPMRC", "Node_Modules"} {
		if _, err := os.Lstat(filepath.Join(pkg.StorePath, rel)); err == nil {
			t.Errorf("%q reached the store at %s: a default exclude must hold however the name is cased", rel, pkg.StorePath)
		}
	}
	// Positive control, so the assertions above cannot pass on an empty store:
	// README documents .envrc as published, whatever its case.
	for _, rel := range []string{"package.json", "index.js", ".envrc"} {
		if _, err := os.Lstat(filepath.Join(pkg.StorePath, rel)); err != nil {
			t.Errorf("expected %q in the store at %s: %v", rel, pkg.StorePath, err)
		}
	}
}

// hostileLnpmProject replaces projectDir's .lnpm with a symlink at a directory
// outside the project, which is what a repository that committed .lnpm as a
// symlink puts on disk at checkout time - .gitignore does not stop a tracked
// symlink from being checked out. It returns that outside directory so a test
// can assert nothing reached it.
//
// The entry it plants there is a live link, and that detail is what makes the
// three tests below test #340 rather than #313. Measured on the first draft,
// where the outside directory was left empty: with #340's guard removed, the
// unguarded IsLiveLinked lstatted a path that did not exist, answered false, and
// the command carried on into Link - which refused through #313's guard, with a
// message naming .lnpm too. Every assertion still held, so TestPush... and
// TestPublishWithPush... both passed with the change under test deleted. Only
// TestPull... went red, and for an unrelated reason: its package was already up
// to date, so pull returned before reaching Link at all.
//
// With a live link at outside/<package>, the unguarded query answers true, the
// command takes its "skipped (live link to source)" path and reports success,
// and Link is never called - so #313 cannot hold any of these tests up.
//
// Skipping rather than failing where no symlink can be created matches
// TestPublishSymlinks in this package, and it costs real coverage on Windows.
// Stated plainly rather than left implicit: on a Windows runner without symlink
// privilege, os.Symlink fails and all three tests below skip, so the command
// layer's handling of the refusal is not exercised there at all. What still runs
// on Windows is internal/link's own guard tests, whose linkDirAt falls back to a
// junction via mklink /J and therefore keeps working without that privilege -
// so the refusal itself is covered on Windows, and only its three call sites are
// not.
//
// Reaching that fallback from here would mean exporting internal/link's
// createDirSymlink - unexported, and split across symlink_unix.go
// (//go:build !windows) and symlink_windows.go, only the second of which has the
// junction branch - or copying that branch into this package. Neither is worth
// it for three tests: a second copy of a security fixture primitive is exactly
// what "reuse, do not duplicate" argues against, and widening a package's API
// for a test is worse.
func hostileLnpmProject(t *testing.T, projectDir, packageName string) string {
	t.Helper()

	tmpDir := t.TempDir()
	outside := filepath.Join(tmpDir, "outside")
	source := filepath.Join(tmpDir, "source")
	for _, dir := range []string{outside, source} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	if err := os.Symlink(source, filepath.Join(outside, packageName)); err != nil {
		t.Skipf("Symlinks not supported: %v", err)
	}

	lnpmDir := filepath.Join(projectDir, ".lnpm")
	if err := os.RemoveAll(lnpmDir); err != nil {
		t.Fatalf("removing the real .lnpm: %v", err)
	}
	if err := os.Symlink(outside, lnpmDir); err != nil {
		t.Skipf("Symlinks not supported: %v", err)
	}
	return outside
}

// assertOutsideUnchanged fails unless dir holds exactly want. A refusal that is
// reported but arrives after the command already wrote through the link leaves
// the same mess as no check, and the fixture plants one entry there, so
// "untouched" means that entry and nothing else.
func assertOutsideUnchanged(t *testing.T, dir string, want ...string) {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading the directory outside the project: %v", err)
	}
	var got []string
	for _, entry := range entries {
		got = append(got, entry.Name())
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("the directory outside the project holds %v, want it untouched at %v", got, want)
	}
}

// TestPullRefusesThroughASymlinkedLnpm covers #340 at the command level. pull
// asks the linker whether each package is live-linked before it relinks
// anything, so on a hostile checkout the first thing it does is look outside the
// project, and #313's write-path refusal only arrives one step later - if it is
// reached at all, which on this fixture it is not.
//
// Revert measured with #340's guard removed from IsLiveLinked: this test fails
// with "RunPull through a symlinked .lnpm error = nil, want a refusal", and the
// captured output is "Pulling guard-pull-lib... skipped (live link to source)"
// followed by "OK Skipped 1 live-linked package(s)", exit 0.
//
// It is also the only one of the three that pins the call site's handling of the
// refusal. Measured with the guard kept but its error discarded at all three
// call sites: this test goes red and the other two stay green, because push and
// publish carry on into Link and get #313's refusal instead, while pull reports
// "already up to date" and exits 0.
//
// Skips on Windows without symlink privilege; see hostileLnpmProject.
func TestPullRefusesThroughASymlinkedLnpm(t *testing.T) {
	env := setupTest(t)

	env.simplePkg("guard-pull-lib")
	projectDir := env.newProject("guard-pull-project")
	env.addPkg(projectDir, "guard-pull-lib", false, false)
	outside := hostileLnpmProject(t, projectDir, "guard-pull-lib")

	env.chdir(projectDir)
	var err error
	out := captureStdout(t, func() { err = cli.RunPull(nil) })

	if err == nil {
		t.Fatal("RunPull through a symlinked .lnpm error = nil, want a refusal")
	}
	if !strings.Contains(out, ".lnpm") {
		t.Errorf("Expected pull to name the refused .lnpm, got:\n%s", out)
	}
	// Reported as a failure, not as a deliberate skip: nothing about the
	// package could be established, so "skipped (live link to source)" would be
	// a claim the command is in no position to make. The planted live link is
	// what makes this assertion load-bearing - it is exactly what the unguarded
	// command prints.
	if strings.Contains(out, "live link") {
		t.Errorf("Expected pull to report a failure rather than a live-link skip, got:\n%s", out)
	}

	// Where in the sequence the refusal lands. The query is asked before the tag
	// is resolved and before the "Pulling <name>... " line is opened, so this
	// package never opens that line at all and the refusal surfaces in the
	// end-of-run failure block naming it - the same shape as every other failure
	// that happens this early. The two assertions do different jobs: the first
	// is what a guard moved below the print would turn red, and the second pins
	// that the refusal reaches the failure block at all rather than being
	// swallowed on the way there.
	if strings.Contains(out, "Pulling guard-pull-lib") {
		t.Errorf("Expected no 'Pulling guard-pull-lib... ' line for a package refused before tag resolution, got:\n%s", out)
	}
	if !strings.Contains(out, "Some packages failed:") || !strings.Contains(out, "guard-pull-lib:") {
		t.Errorf("Expected the refusal in the failure block naming guard-pull-lib, got:\n%s", out)
	}

	assertOutsideUnchanged(t, outside, "guard-pull-lib")
}

// TestPushRefusesThroughASymlinkedLnpm is the same at the push command, whose
// per-project loop asks the same query in a goroutine and carries the answer
// back on a channel.
//
// Revert measured with #340's guard removed from IsLiveLinked: this test fails
// with "RunPush through a symlinked .lnpm error = nil, want a refusal", and the
// captured output is "Pushed to 0/1 projects (1 skipped: live link to source)",
// exit 0.
//
// A second direction, measured, and it does not move this test: leaving the
// guard in place but discarding its error at the call site (live, _ := ...)
// leaves live false, so push carries on into Link, which refuses through #313
// with the same shape of message. There is no observable difference here, so
// this test cannot pin the call site's error handling - only
// TestPullRefusesThroughASymlinkedLnpm does, because pull can return "already up
// to date" before it ever reaches Link.
//
// Skips on Windows without symlink privilege; see hostileLnpmProject.
func TestPushRefusesThroughASymlinkedLnpm(t *testing.T) {
	env := setupTest(t)

	pkgDir, projectDir := env.publishAndAdd("guard-push-lib")
	outside := hostileLnpmProject(t, projectDir, "guard-push-lib")

	env.chdir(pkgDir)
	env.writeFile(filepath.Join(pkgDir, "index.js"), "module.exports = 'v2';")
	var err error
	out := captureStdout(t, func() { err = cli.RunPush(false) })

	if err == nil {
		t.Fatal("RunPush through a symlinked .lnpm error = nil, want a refusal")
	}
	if !strings.Contains(out, ".lnpm") {
		t.Errorf("Expected push to name the refused .lnpm, got:\n%s", out)
	}
	if strings.Contains(out, "live link") {
		t.Errorf("Expected push to report a failure rather than a live-link skip, got:\n%s", out)
	}
	assertOutsideUnchanged(t, outside, "guard-push-lib")
}

// TestPublishWithPushRefusesThroughASymlinkedLnpm covers the third caller.
// `publish --push` has its own relink loop, separate from push's, and it asks
// the same query first.
//
// Revert measured with #340's guard removed from IsLiveLinked: this test fails
// with "RunPublish(--push) through a symlinked .lnpm error = nil, want a
// refusal", and the captured output is "Pushed to 0/1 projects (1 skipped: live
// link to source)", exit 0.
//
// A second direction, measured, and it does not move this test: leaving the
// guard in place but discarding its error at the call site (live, _ := ...)
// leaves live false, so push carries on into Link, which refuses through #313
// with the same shape of message. There is no observable difference here, so
// this test cannot pin the call site's error handling - only
// TestPullRefusesThroughASymlinkedLnpm does, because pull can return "already up
// to date" before it ever reaches Link.
//
// Skips on Windows without symlink privilege; see hostileLnpmProject.
func TestPublishWithPushRefusesThroughASymlinkedLnpm(t *testing.T) {
	env := setupTest(t)

	pkgDir, projectDir := env.publishAndAdd("guard-publish-lib")
	outside := hostileLnpmProject(t, projectDir, "guard-publish-lib")

	env.chdir(pkgDir)
	env.writeFile(filepath.Join(pkgDir, "index.js"), "module.exports = 'v2';")
	var err error
	out := captureStdout(t, func() { err = cli.RunPublish(true, false, false, false) })

	if err == nil {
		t.Fatal("RunPublish(--push) through a symlinked .lnpm error = nil, want a refusal")
	}
	if !strings.Contains(out, ".lnpm") {
		t.Errorf("Expected publish --push to name the refused .lnpm, got:\n%s", out)
	}
	if strings.Contains(out, "live link") {
		t.Errorf("Expected publish --push to report a failure rather than a live-link skip, got:\n%s", out)
	}
	assertOutsideUnchanged(t, outside, "guard-publish-lib")
}
