package tests

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/cli"
	"github.com/pedrosousa13/lnpm/internal/config"
	"github.com/pedrosousa13/lnpm/internal/pack"
	"github.com/pedrosousa13/lnpm/internal/shellcmd"
)

// dryRunHeading is the prefix of the line a dry run prints above its file list.
const dryRunHeading = "Dry run:"

// The two indents the packed list is printed at: a dry run lists at the top
// level of its own report, while a publish nests the list inside the indented
// summary block under "Packed:".
const (
	dryRunIndent  = "  "
	publishIndent = "    "
)

// dryRunFiles is the fixture the dry-run output tests share. It is chosen for
// one property: filepath.Walk does not visit these paths in the order a sort by
// RelPath puts them in. Walk descends into "a" before it reaches "a.js",
// because it sorts directory entries by name and "a" < "a.js", so the packed
// slice arrives as [a/inner.js, a.js, index.js, package.json]. Sorted by
// RelPath it is [a.js, a/inner.js, index.js, package.json], because '.' (0x2E)
// sorts before '/' (0x2F). A dry run that printed the packed slice as it came
// would therefore pass an "every path is listed" check and fail the ordering
// one, which is what makes the explicit sort load-bearing rather than
// decorative.
var dryRunFiles = map[string]string{
	"a.js":       "module.exports = 'a';",
	"a/inner.js": "module.exports = 'inner';",
	"index.js":   "module.exports = 'index';",
}

// packedPaths returns the RelPaths pack would select for pkgDir, sorted. The
// tests derive their expectations from pack rather than restating the fixture,
// so a change in the selection rules cannot leave them asserting a stale set.
func packedPaths(t *testing.T, pkgDir string) []string {
	t.Helper()

	_, files, err := pack.Pack(pkgDir)
	if err != nil {
		t.Fatalf("Failed to pack %s: %v", pkgDir, err)
	}
	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.RelPath
	}
	sort.Strings(paths)
	return paths
}

// assertListsInOrder checks that every path in want appears in out as a line of
// its own at the given indent, and that the lines appear in the order want
// gives them.
//
// It matches "\n<indent><path>\n" rather than the bare path: "a.js" is a
// substring of "lib/a.js", so a Contains check on the bare path would be
// satisfied by the wrong line. The indent is a parameter because the dry run
// lists at two spaces and the publish summary nests its list at four.
func assertListsInOrder(t *testing.T, out string, want []string, indent string) {
	t.Helper()

	prev := -1
	for _, p := range want {
		line := "\n" + indent + p + "\n"
		at := strings.Index(out, line)
		if at < 0 {
			t.Errorf("Expected the output to list %q on a line of its own; it does not.\nOutput:\n%s", p, out)
			continue
		}
		if at < prev {
			t.Errorf("Expected %q to be listed after the previous path, but it comes before it.\nOutput:\n%s", p, out)
		}
		prev = at
	}
}

// storeSnapshot fingerprints every entry under root: the relative path of each
// directory, and for each file its size, permission bits and the SHA-256 of its
// bytes. The store root holds both the content-addressed store tree and
// lnpm.db, so one snapshot covers "the store" and "the database" together, and
// covers them by content rather than by absence - a write that replaced a file
// with one of the same length would still change the digest.
//
// Directories are recorded too, so a run that only created an empty tree (which
// store.New does before it stores anything) is still a difference.
func storeSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()

	snap := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			snap[rel] = "dir"
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		snap[rel] = fmt.Sprintf("file size=%d mode=%v sha256=%s", info.Size(), info.Mode().Perm(), hex.EncodeToString(sum[:]))
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to snapshot %s: %v", root, err)
	}
	return snap
}

// assertSnapshotsEqual reports every entry that was added, removed or changed
// between two storeSnapshot results.
func assertSnapshotsEqual(t *testing.T, before, after map[string]string, what string) {
	t.Helper()

	for rel, b := range before {
		a, ok := after[rel]
		if !ok {
			t.Errorf("%s: %s disappeared", what, rel)
			continue
		}
		if a != b {
			t.Errorf("%s: %s changed\n  before: %s\n  after:  %s", what, rel, b, a)
		}
	}
	for rel := range after {
		if _, ok := before[rel]; !ok {
			t.Errorf("%s: %s was created (%s)", what, rel, after[rel])
		}
	}
}

// A dry run prints every path it would pack, not just how many there are.
func TestPublishDryRunPrintsEveryPackedPath(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.CreateTestPackage("dryrun-list-pkg", "1.0.0", dryRunFiles)
	env.chdir(pkgDir)
	want := packedPaths(t, pkgDir)

	var err error
	out := captureStdout(t, func() {
		err = cli.RunPublishWith(cli.PublishOptions{DryRun: true})
	})
	if err != nil {
		t.Fatalf("Expected the dry run to succeed, got: %v", err)
	}

	assertListsInOrder(t, out, want, dryRunIndent)
}

// The listed order is a sort by RelPath, which is not the order the walk
// produces, and it is the same on every run.
func TestPublishDryRunOutputOrderIsDeterministic(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.CreateTestPackage("dryrun-order-pkg", "1.0.0", dryRunFiles)
	env.chdir(pkgDir)
	want := packedPaths(t, pkgDir)

	// The fixture only proves anything if the sorted order really does differ
	// from the order pack returns; if a future change to pack made them the
	// same, this test would silently stop testing the sort.
	_, packed, err := pack.Pack(pkgDir)
	if err != nil {
		t.Fatalf("Failed to pack: %v", err)
	}
	asPacked := make([]string, len(packed))
	for i, f := range packed {
		asPacked[i] = f.RelPath
	}
	if strings.Join(asPacked, "\n") == strings.Join(want, "\n") {
		t.Fatalf("Fixture no longer distinguishes packed order from sorted order (%v); the ordering assertion below would pass without any sort", want)
	}

	first := captureStdout(t, func() {
		if err := cli.RunPublishWith(cli.PublishOptions{DryRun: true}); err != nil {
			t.Errorf("First dry run failed: %v", err)
		}
	})
	second := captureStdout(t, func() {
		if err := cli.RunPublishWith(cli.PublishOptions{DryRun: true}); err != nil {
			t.Errorf("Second dry run failed: %v", err)
		}
	})

	assertListsInOrder(t, first, want, dryRunIndent)
	if first != second {
		t.Errorf("Expected two dry runs to print identical output.\nFirst:\n%s\nSecond:\n%s", first, second)
	}
}

// The load-bearing one: a dry run leaves the store tree and the database
// byte-identical, compared by digest rather than by "no error was returned".
func TestPublishDryRunLeavesStoreAndDatabaseByteIdentical(t *testing.T) {
	env := setupTest(t)

	// Publish something real first, so the store and the database both hold
	// content. A comparison over two empty directories would be satisfied by a
	// dry run that wrote nothing only because there was nothing to write.
	env.publishPkg("dryrun-neighbour", "1.0.0", map[string]string{"index.js": "module.exports = 1;"})

	pkgDir := env.CreateTestPackage("dryrun-untouched-pkg", "2.0.0", dryRunFiles)
	env.chdir(pkgDir)

	before := storeSnapshot(t, env.StoreDir)
	if len(before) < 3 {
		t.Fatalf("Expected the store to hold real content before the dry run, snapshot has %d entries: %v", len(before), before)
	}

	if err := cli.RunPublishWith(cli.PublishOptions{DryRun: true}); err != nil {
		t.Fatalf("Expected the dry run to succeed, got: %v", err)
	}

	after := storeSnapshot(t, env.StoreDir)
	assertSnapshotsEqual(t, before, after, "dry run changed the store")

	env.AssertPackageInDatabase("dryrun-untouched-pkg", false)
	env.AssertPackageInDatabase("dryrun-neighbour", true)
}

// On a store that has never been published to, a dry run must not even bring
// the store tree into existence, and must leave no lock file behind.
func TestPublishDryRunOnEmptyStoreCreatesNothing(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.CreateTestPackage("dryrun-empty-store-pkg", "1.0.0", dryRunFiles)
	env.chdir(pkgDir)

	before := storeSnapshot(t, env.StoreDir)
	if err := cli.RunPublishWith(cli.PublishOptions{DryRun: true}); err != nil {
		t.Fatalf("Expected the dry run to succeed, got: %v", err)
	}
	after := storeSnapshot(t, env.StoreDir)

	assertSnapshotsEqual(t, before, after, "dry run changed an untouched store")
	env.AssertDirectoryExists(filepath.Join(env.StoreDir, "store"), false)
	env.AssertFileExists(filepath.Join(pkgDir, "lnpm.lock"), false)
}

// A dry run leaves no temporary files behind, including the one the workspace:
// specifier rewrite has to materialise to hash the rewritten manifest.
//
// TMPDIR is redirected at a directory this test owns so the assertion can be
// "the directory is empty" rather than a diff of the shared system temp
// directory, which other packages in this suite are writing to concurrently.
//
// It is not the guarantee the other tests in this file are, and it was green
// before --dry-run did anything: publishSingle defers the rewrite's cleanup
// unconditionally, so the temp file goes away on every path out of the
// function, dry run or not. It pins the acceptance criterion and it would catch
// a dry-run return placed above that defer, but it does not test the return
// itself.
func TestPublishDryRunLeavesNoTempFiles(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp) // unix
	t.Setenv("TMP", tmp)    // windows
	t.Setenv("TEMP", tmp)   // windows
	if got := os.TempDir(); got != strings.TrimRight(tmp, string(os.PathSeparator)) {
		t.Skipf("os.TempDir() is %s, not the redirected %s; cannot isolate temp files on this platform", got, tmp)
	}

	env := setupTest(t)

	// The workspace-deps fixture's lib depends on its sibling with
	// "workspace:*", which is the only path in publish that writes a temp file
	// before anything is stored.
	wsDir := env.CopyFixture("workspace-deps")
	libDir := filepath.Join(wsDir, "packages", "lib")
	env.chdir(libDir)

	// Skip validation: the fixture has no built files.
	if err := cli.RunPublishWith(cli.PublishOptions{DryRun: true, SkipValidation: true}); err != nil {
		t.Fatalf("Expected the dry run to succeed, got: %v", err)
	}

	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatalf("Failed to read %s: %v", tmp, err)
	}
	if len(entries) != 0 {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Errorf("Expected the dry run to leave no temporary files, found: %v", names)
	}
}

// A successful dry run exits zero; a pack failure comes back as an error, so
// the two are distinguishable by exit code and not only by reading the output.
//
// Each half also checks the output, because the return values alone are what an
// ordinary publish already produces: nil on success, a wrapped pack error on a
// broken manifest. Asserting only on the error would leave the test green with
// --dry-run doing nothing at all, which is what it did on the first run of this
// file.
func TestPublishDryRunExitStatusDistinguishesSuccessFromPackFailure(t *testing.T) {
	env := setupTest(t)

	good := env.CreateTestPackage("dryrun-exit-pkg", "1.0.0", dryRunFiles)
	env.chdir(good)
	want := packedPaths(t, good)

	var goodErr error
	out := captureStdout(t, func() {
		goodErr = cli.RunPublishWith(cli.PublishOptions{DryRun: true})
	})
	if goodErr != nil {
		t.Errorf("Expected a successful dry run to return nil, got: %v", goodErr)
	}
	assertListsInOrder(t, out, want, dryRunIndent)

	// A manifest pack cannot parse. Validation and the prepare hooks are
	// skipped so that pack is the step that fails, rather than one of the
	// steps before it.
	bad := env.CreateTestPackage("dryrun-broken-pkg", "1.0.0", nil)
	env.writeFile(filepath.Join(bad, "package.json"), `{"name": "dryrun-broken-pkg",`)
	env.chdir(bad)

	var badErr error
	badOut := captureStdout(t, func() {
		badErr = cli.RunPublishWith(cli.PublishOptions{DryRun: true, SkipHooks: true, SkipValidation: true})
	})
	if badErr == nil {
		t.Fatal("Expected a dry run over an unpackable package to return an error, got nil")
	}
	if !strings.Contains(badErr.Error(), "failed to pack") {
		t.Errorf("Expected a pack failure, got: %v", badErr)
	}
	if strings.Contains(badOut, dryRunHeading) {
		t.Errorf("Expected a failed pack to report no file list.\nOutput:\n%s", badOut)
	}
}

// A dry run of a package that is already in the store still prints its file
// list. The publish path short-circuits on unchanged content well before it
// writes anything, so a dry run that returned from there would answer "already
// published" to the question "what would you ship?".
func TestPublishDryRunListsFilesForAnAlreadyPublishedPackage(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.publishPkg("dryrun-republish-pkg", "1.0.0", dryRunFiles)
	env.chdir(pkgDir)
	want := packedPaths(t, pkgDir)

	out := captureStdout(t, func() {
		if err := cli.RunPublishWith(cli.PublishOptions{DryRun: true}); err != nil {
			t.Errorf("Expected the dry run to succeed, got: %v", err)
		}
	})

	assertListsInOrder(t, out, want, dryRunIndent)
}

// --dry-run --push pushes nothing: linked projects keep the content they had.
func TestPublishDryRunWithPushUpdatesNoProject(t *testing.T) {
	env := setupTest(t)

	pkgDir, projectDir := env.publishAndAdd("dryrun-push-pkg")
	env.AssertLinkedFileContent(projectDir, "dryrun-push-pkg", "index.js", "module.exports = 'dryrun-push-pkg';")

	// Change the source so a real --push would have something to propagate.
	env.chdir(pkgDir)
	env.writeFile(filepath.Join(pkgDir, "index.js"), "module.exports = 'CHANGED';")

	before := storeSnapshot(t, env.StoreDir)
	if err := cli.RunPublishWith(cli.PublishOptions{DryRun: true, Push: true}); err != nil {
		t.Fatalf("Expected the dry run to succeed, got: %v", err)
	}
	assertSnapshotsEqual(t, before, storeSnapshot(t, env.StoreDir), "dry run --push changed the store")

	env.AssertLinkedFileContent(projectDir, "dryrun-push-pkg", "index.js", "module.exports = 'dryrun-push-pkg';")
}

// A dry run runs pre_publish, because that hook is often what builds the files
// being packed and a dry run that skipped it would report a different set than
// the real publish. It does not run post_publish, because nothing was
// published.
func TestPublishDryRunRunsPrePublishButNotPostPublish(t *testing.T) {
	env := setupTest(t)

	markers := t.TempDir()
	pre := filepath.Join(markers, "pre.txt")
	post := filepath.Join(markers, "post.txt")

	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	env.writeFile(cfgPath, "hooks:\n"+
		"  pre_publish: "+yamlQuote("echo ran > "+shellcmd.QuoteArg(pre))+"\n"+
		"  post_publish: "+yamlQuote("echo ran > "+shellcmd.QuoteArg(post))+"\n")
	t.Setenv("LNPM_CONFIG", cfgPath)
	config.ResetForTesting()
	t.Cleanup(config.ResetForTesting)

	pkgDir := env.CreateTestPackage("dryrun-hooks-pkg", "1.0.0", dryRunFiles)
	env.chdir(pkgDir)

	if err := cli.RunPublishWith(cli.PublishOptions{DryRun: true}); err != nil {
		t.Fatalf("Expected the dry run to succeed, got: %v", err)
	}

	env.AssertFileExists(pre, true)
	env.AssertFileExists(post, false)
}

// --dry-run --all reports every package in the workspace and publishes none of
// them.
func TestPublishDryRunAllReportsEachPackage(t *testing.T) {
	env := setupTest(t)

	wsDir := env.CopyFixture("npm-workspace")
	env.chdir(wsDir)

	wantA := packedPaths(t, filepath.Join(wsDir, "packages", "package-a"))
	wantB := packedPaths(t, filepath.Join(wsDir, "packages", "package-b"))

	before := storeSnapshot(t, env.StoreDir)
	var err error
	out := captureStdout(t, func() {
		err = cli.RunPublishWith(cli.PublishOptions{DryRun: true, All: true, SkipValidation: true})
	})
	if err != nil {
		t.Fatalf("Expected the dry run to succeed, got: %v", err)
	}

	for _, name := range []string{"@npm-test/package-a", "@npm-test/package-b"} {
		if !strings.Contains(out, name) {
			t.Errorf("Expected the dry run to report %s.\nOutput:\n%s", name, out)
		}
	}
	assertListsInOrder(t, out, wantA, dryRunIndent)
	assertListsInOrder(t, out, wantB, dryRunIndent)

	assertSnapshotsEqual(t, before, storeSnapshot(t, env.StoreDir), "dry run --all changed the store")
	env.AssertPackageInDatabase("@npm-test/package-a", false)
	env.AssertPackageInDatabase("@npm-test/package-b", false)
}

// --dry-run --all writes nothing, so the three lines publishAll prints around
// the per-package reports must not say it published anything.
func TestPublishDryRunAllDoesNotClaimItPublished(t *testing.T) {
	env := setupTest(t)

	wsDir := env.CopyFixture("npm-workspace")
	env.chdir(wsDir)

	var err error
	out := captureStdout(t, func() {
		err = cli.RunPublishWith(cli.PublishOptions{DryRun: true, All: true, SkipValidation: true})
	})
	if err != nil {
		t.Fatalf("Expected the dry run to succeed, got: %v", err)
	}

	// The negative is the point: "Publishing 2 packages from npm workspace..."
	// and "Published 2/2 packages" are what this printed before, both of them
	// untrue of a run that wrote nothing.
	for _, claim := range []string{"Publishing ", "Published "} {
		if strings.Contains(out, claim) {
			t.Errorf("Expected a dry run over a workspace not to say %q anywhere.\nOutput:\n%s", claim, out)
		}
	}
	for _, want := range []string{
		"Dry run over 2 packages from npm workspace",
		"Dry run: 2/2 packages packed; nothing was written",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("Expected the output to contain %q.\nOutput:\n%s", want, out)
		}
	}
}

// The error publishAll returns is worded for a dry run too, for the same reason
// as its printed lines: nothing was being published, so nothing failed to
// publish.
//
// package-b is made to fail validation rather than pack, because a package.json
// broken enough to fail pack also fails workspace.ListPackages, which aborts
// publishAll before any package is attempted. That is also why the message says
// "the dry run failed" rather than naming pack: the step that failed here is
// the one before it.
func TestPublishDryRunAllDoesNotClaimAFailedPublish(t *testing.T) {
	env := setupTest(t)

	wsDir := env.CopyFixture("npm-workspace")
	env.writeFile(filepath.Join(wsDir, "packages", "package-b", "package.json"),
		`{"name":"@npm-test/package-b","version":"1.0.0","main":"missing.js"}`)
	env.chdir(wsDir)

	var err error
	out := captureStdout(t, func() {
		err = cli.RunPublishWith(cli.PublishOptions{DryRun: true, All: true})
	})
	if err == nil {
		t.Fatal("Expected the dry run to fail for package-b, got nil")
	}
	if want := "the dry run failed for 1 of 2 package(s)"; err.Error() != want {
		t.Errorf("Expected %q, got: %v", want, err)
	}
	if strings.Contains(out, "Published ") {
		t.Errorf("Expected a failed dry run not to say anything was published.\nOutput:\n%s", out)
	}
}

// An ordinary publish prints the packed set, not only how many files it holds.
// A count cannot show that a secret was packed, and --dry-run cannot answer it
// after the fact: it re-packs the working tree, so it says what would ship now,
// never what did.
func TestPublishPrintsThePackedFileList(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.CreateTestPackage("publish-list-pkg", "1.0.0", dryRunFiles)
	env.chdir(pkgDir)
	want := packedPaths(t, pkgDir)

	var err error
	out := captureStdout(t, func() {
		err = cli.RunPublishWith(cli.PublishOptions{})
	})
	if err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	assertListsInOrder(t, out, want, publishIndent)
}

// push reaches the same summary as publish when the package is not in the store
// yet, and it gets the same list. This is the half a command-specific pointer
// could not serve: telling a `lnpm push` user to run `lnpm publish --dry-run`
// is advice about a command they did not run.
func TestPushPrintsThePackedFileList(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.CreateTestPackage("push-list-pkg", "1.0.0", dryRunFiles)
	env.chdir(pkgDir)
	want := packedPaths(t, pkgDir)

	var err error
	out := captureStdout(t, func() {
		err = cli.RunPush(false)
	})
	if err != nil {
		t.Fatalf("Failed to push: %v", err)
	}
	if !strings.Contains(out, "not published yet, publishing...") {
		t.Fatalf("Expected push to fall through to the publish summary.\nOutput:\n%s", out)
	}

	assertListsInOrder(t, out, want, publishIndent)
}

// yamlQuote renders s as a YAML double-quoted scalar, so a hook command holding
// shell quoting or Windows backslashes survives the config file intact.
func yamlQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
