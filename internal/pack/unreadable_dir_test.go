package pack

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// denyDirRead makes dir unreadable for the rest of the test and restores it
// afterwards, so t.TempDir's own cleanup can still remove the tree.
//
// It fails rather than skips when the denial does not take, because every caller
// has already skipped the two platforms where that is expected — Windows, where
// a mode maps to the read-only attribute and denies nothing, and root, which
// ignores the bits outright. The idiom and both skip reasons are
// internal/cli/reachable_test.go's.
func denyDirRead(t *testing.T, dir string) {
	t.Helper()

	if err := os.Chmod(dir, 0000); err != nil {
		t.Fatalf("deny read on %s: %v", dir, err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0755) })

	// Confirm the fixture produces the error it means to, rather than passing
	// because the denial did not take.
	if _, err := os.ReadDir(dir); err == nil {
		t.Fatalf("the fixture did not produce a read error on %s", dir)
	}
}

// requireDroppableDirPermissions skips the two runs where a directory's
// permission bits cannot be made to deny anything: Windows, and any run as root.
func requireDroppableDirPermissions(t *testing.T) {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("Skipping on Windows - a directory mode of 0000 maps to the read-only attribute, which still permits traversal, so no permission error can be produced")
	}
	if os.Getuid() == 0 {
		t.Skip("root ignores the permission bits this test relies on")
	}
}

// TestPackSkipsAnUnreadableExcludedDirectory is #348's headline case, in both
// walk modes. A .gitignore names coverage/, coverage cannot be read, and the
// publish has to pack the rest rather than aborting on a directory the package
// already said it did not want.
//
// The two rows are the two modes, and they are separate code paths rather than
// one: without a "files" field the ignore chain decides the whole tree, and with
// one it decides nothing (#318), so "already excluded" is answered by a
// different predicate in each. The acceptance criterion is that both reach the
// same answer for this fixture.
func TestPackSkipsAnUnreadableExcludedDirectory(t *testing.T) {
	requireDroppableDirPermissions(t)

	tests := []struct {
		name     string
		manifest string
	}{
		{
			name:     "no files field",
			manifest: `{"name": "pkg", "version": "1.0.0"}`,
		},
		{
			name:     "files field",
			manifest: `{"name": "pkg", "version": "1.0.0", "files": ["dist"]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			writeMainEntryTree(t, root, map[string]string{
				"package.json":         tt.manifest,
				"dist/index.js":        "module.exports = {}",
				".gitignore":           "coverage/\n",
				"coverage/report.html": "<html></html>",
			})
			denyDirRead(t, filepath.Join(root, "coverage"))

			var files []*FileInfo
			out := capturePackStdout(t, func() {
				var err error
				_, files, err = Pack(root)
				if err != nil {
					t.Errorf("Pack() error: %v, want the unreadable excluded directory skipped", err)
				}
			})
			if t.Failed() {
				return
			}

			got := relPathsOf(files)
			want := []string{"dist/index.js", "package.json"}
			if strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Errorf("packed set = %v, want %v", got, want)
			}
			if !strings.Contains(out, "coverage") {
				t.Errorf("Pack() printed %q, want a warning naming coverage", out)
			}
			if !strings.Contains(out, "warning:") {
				t.Errorf("Pack() printed %q, want the house warning prefix", out)
			}
		})
	}
}

// TestPackSkipsAnUnreadableHardReservedDirectory covers the never-publishable
// list, which no ignore file has to name and no "files" field can reach into —
// isHardReserved is asked in the walk above every selection rule. An unreadable
// node_modules is the likeliest real instance of this bug, and nothing else in
// the fix answers for it: node_modules is in hardReservedExcludes and not in
// defaultExcludes, so with no .gitignore the ignore chain says nothing about it.
// Deleting the isHardReserved term from unreadableDirIsExcluded turns this test
// and only this test red — run, not read.
func TestPackSkipsAnUnreadableHardReservedDirectory(t *testing.T) {
	requireDroppableDirPermissions(t)

	root := t.TempDir()
	writeMainEntryTree(t, root, map[string]string{
		"package.json":              `{"name": "pkg", "version": "1.0.0"}`,
		"index.js":                  "module.exports = {}",
		"node_modules/dep/index.js": "module.exports = {}",
	})
	denyDirRead(t, filepath.Join(root, "node_modules"))

	var files []*FileInfo
	out := capturePackStdout(t, func() {
		var err error
		_, files, err = Pack(root)
		if err != nil {
			t.Errorf("Pack() error: %v, want the unreadable node_modules skipped", err)
		}
	})
	if t.Failed() {
		return
	}

	got := relPathsOf(files)
	want := []string{"index.js", "package.json"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("packed set = %v, want %v", got, want)
	}
	if !strings.Contains(out, "node_modules") {
		t.Errorf("Pack() printed %q, want a warning naming node_modules", out)
	}
}

// TestPackAbortsOnAnUnreadableDirectoryThePackageWouldHavePacked is the other
// direction: a directory that would have been packed is not quietly dropped.
//
// Note which rule that is and is not. docs/adr/0001 is about direction, and the
// direction it calls a bug is the *widening* one; dropping a would-be-packed
// path narrows, which the ADR leaves as "a judgement call to make case by case"
// and ranks as the milder outcome besides. This test pins #348's triage making
// that call, not the ADR enforcing itself. The reason is downstream: a tarball
// missing a file it should contain installs and then fails to load.
//
// The rows are the two non-whitelist ones, then the "files" entries the skip
// predicate has to answer no to, then "main". The row reaching a path inside is
// the one a naive predicate gets wrong — matchFilesField("coverage",
// ["coverage/report.html"]) is filesMatchNone, because the entry names neither
// coverage nor an ancestor of it, and the entry plainly selects into it all the
// same.
func TestPackAbortsOnAnUnreadableDirectoryThePackageWouldHavePacked(t *testing.T) {
	requireDroppableDirPermissions(t)

	tests := []struct {
		name      string
		manifest  string
		gitignore string
	}{
		{
			name:     "no files field and no ignore file",
			manifest: `{"name": "pkg", "version": "1.0.0"}`,
		},
		{
			name:      "no files field and an ignore file naming something else",
			manifest:  `{"name": "pkg", "version": "1.0.0"}`,
			gitignore: "dist/\n",
		},
		{
			name:     "a files entry naming the directory",
			manifest: `{"name": "pkg", "version": "1.0.0", "files": ["coverage"]}`,
		},
		{
			name:     "a files entry reaching a path inside it",
			manifest: `{"name": "pkg", "version": "1.0.0", "files": ["coverage/report.html"]}`,
		},
		{
			name:     "a files entry reaching it by double star",
			manifest: `{"name": "pkg", "version": "1.0.0", "files": ["**/*.html"]}`,
		},
		{
			name:     "main points inside it",
			manifest: `{"name": "pkg", "version": "1.0.0", "main": "coverage/report.js", "files": ["dist"]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			files := map[string]string{
				"package.json":         tt.manifest,
				"dist/index.js":        "module.exports = {}",
				"coverage/report.html": "<html></html>",
			}
			if tt.gitignore != "" {
				files[".gitignore"] = tt.gitignore
			}
			writeMainEntryTree(t, root, files)
			denyDirRead(t, filepath.Join(root, "coverage"))

			var err error
			_ = capturePackStdout(t, func() {
				_, _, err = Pack(root)
			})
			if err == nil {
				t.Fatalf("Pack() = nil error, want an abort naming coverage")
			}
			if !strings.Contains(err.Error(), "coverage") {
				t.Errorf("Pack() error = %v, want it to name coverage", err)
			}
		})
	}
}

// TestPackAbortsWhenAChildCannotBeStatted pins the walk's other error shape,
// which the skip must not swallow. filepath.Walk passes the callback whatever
// its lstat of a child returned, and os.Lstat returns a nil FileInfo with its
// error — read from $(go env GOROOT)/src/path/filepath/path.go on Go 1.26.7, and
// confirmed by the nil dereference that deleting the guard produces here. The
// fixture is a directory with its read bit but not its execute bit, where readdir
// succeeds and lstat of the names it returned does not.
//
// Two things are asserted at once. Nothing dereferences that nil, and a
// per-child error still aborts: it may be a file, and dropping a file the
// package asked for is the *narrowing* side — not the direction docs/adr/0001
// calls a bug, which is the widening one. Aborting here is #348's triage making
// the judgement call that ADR hands over, for the same downstream reason given
// on TestPackAbortsOnAnUnreadableDirectoryThePackageWouldHavePacked above.
//
// It is also this fix's known asymmetry, stated rather than hidden. With no
// "files" field the same fixture never reaches the child, because the walk
// prunes coverage on the callback for coverage itself, where no error arrived —
// run on this fixture with the "files" field removed, which packs
// [dist/index.js package.json] with no error and no warning. So a mode of 0444
// aborts under a "files" field and packs cleanly without one, where mode 0000
// behaves alike in both. #348 was filed for the unreadable directory and this
// narrows to it deliberately.
func TestPackAbortsWhenAChildCannotBeStatted(t *testing.T) {
	requireDroppableDirPermissions(t)

	root := t.TempDir()
	writeMainEntryTree(t, root, map[string]string{
		"package.json":         `{"name": "pkg", "version": "1.0.0", "files": ["dist"]}`,
		"dist/index.js":        "module.exports = {}",
		".gitignore":           "coverage/\n",
		"coverage/report.html": "<html></html>",
	})

	coverage := filepath.Join(root, "coverage")
	if err := os.Chmod(coverage, 0444); err != nil {
		t.Fatalf("deny traversal on %s: %v", coverage, err)
	}
	t.Cleanup(func() { _ = os.Chmod(coverage, 0755) })
	if _, err := os.Lstat(filepath.Join(coverage, "report.html")); err == nil {
		t.Skip("this platform lstats a directory entry without the directory's execute bit, so the fixture cannot be built")
	}

	var err error
	_ = capturePackStdout(t, func() {
		_, _, err = Pack(root)
	})
	if err == nil {
		t.Fatalf("Pack() = nil error, want an abort naming report.html")
	}
	if !strings.Contains(err.Error(), "report.html") {
		t.Errorf("Pack() error = %v, want it to name report.html", err)
	}
}

// TestPackAbortsOnAnUnreadableFile is the acceptance criterion stated outright:
// a file-level read error is unaffected by #348. An ordinary regular file with
// mode 0000, sitting in a directory the package packs, still fails the pack.
//
// It is a different mechanism from every other test here and that is the point.
// The walk lstats this file successfully, so no error reaches the callback at
// all; it is selected, and the failure lands in the second pass, where HashFile
// cannot open it. Nothing in the skip logic is on that path, and the assertion
// on the "failed to hash" wording is what says which pass answered.
//
// Nothing else in internal/pack chmods a *file*, so before this the criterion
// rested on reading the structure rather than on a run.
func TestPackAbortsOnAnUnreadableFile(t *testing.T) {
	requireDroppableDirPermissions(t)

	root := t.TempDir()
	writeMainEntryTree(t, root, map[string]string{
		"package.json":  `{"name": "pkg", "version": "1.0.0", "files": ["dist"]}`,
		"dist/index.js": "module.exports = {}",
	})

	locked := filepath.Join(root, "dist", "index.js")
	if err := os.Chmod(locked, 0000); err != nil {
		t.Fatalf("deny read on %s: %v", locked, err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0644) })
	if f, err := os.Open(locked); err == nil {
		_ = f.Close()
		t.Fatalf("the fixture did not produce a read error on %s", locked)
	}

	var err error
	out := capturePackStdout(t, func() {
		_, _, err = Pack(root)
	})
	if err == nil {
		t.Fatalf("Pack() = nil error, want an abort naming dist/index.js")
	}
	if !strings.Contains(err.Error(), "dist/index.js") {
		t.Errorf("Pack() error = %v, want it to name dist/index.js", err)
	}
	if !strings.Contains(err.Error(), "failed to hash") {
		t.Errorf("Pack() error = %v, want the second pass to be the one that failed", err)
	}
	if strings.Contains(out, "warning:") {
		t.Errorf("Pack() printed %q, want no skip warning for a file", out)
	}
}

// TestCollectFilesAbortsWhenThePackageRootCannotBeRead pins the one path Pack
// cannot reach, because readPackageJSON fails on an unreadable root long before
// collectFiles runs.
//
// It is the relPath != "." guard, and under a "files" field it is load-bearing
// rather than tidy. The predicate matches an entry against the root's one
// segment, the literal ".", which "dist" does not match, so without the guard it
// would call the package root excluded and the walk would return no error and no
// files: a publish of nothing, reported as a success. Deleting the guard turns
// this test and only this test red — run, not read.
func TestCollectFilesAbortsWhenThePackageRootCannotBeRead(t *testing.T) {
	requireDroppableDirPermissions(t)

	root := t.TempDir()
	writeMainEntryTree(t, root, map[string]string{
		"package.json":  `{"name": "pkg", "version": "1.0.0", "files": ["dist"]}`,
		"dist/index.js": "module.exports = {}",
	})
	denyDirRead(t, root)

	files, err := collectFiles(root, []string{"dist"}, "")
	if err == nil {
		t.Fatalf("collectFiles() = %v, nil error, want an abort naming the package root", files)
	}
	if !strings.Contains(err.Error(), root) {
		t.Errorf("collectFiles() error = %v, want it to name %s", err, root)
	}
}

// TestFilesFieldMayReach pins the predicate directly, including the rows no
// fixture distinguishes. It is sound in the abort direction, so every row whose
// want is true is either an entry that really does reach or an entry the
// predicate declines to decide; a false row is a claim that no path at or under
// the directory can be selected.
func TestFilesFieldMayReach(t *testing.T) {
	tests := []struct {
		name     string
		dir      string
		patterns []string
		want     bool
		why      string
	}{
		{"unrelated literal", "coverage", []string{"dist"}, false,
			"the entry names a sibling, so nothing under coverage can be selected"},
		{"names the directory", "coverage", []string{"coverage"}, true,
			"the entry contains everything under it"},
		{"names a path inside", "coverage", []string{"coverage/report.html"}, true,
			"matchFilesField says filesMatchNone for the directory itself, and the entry still selects into it"},
		{"names a deeper path", "coverage", []string{"coverage/html/index.js"}, true,
			"depth does not matter, only that the entry reaches in"},
		{"names an ancestor", "coverage/html", []string{"coverage"}, true,
			"an entry naming an ancestor contains the whole subtree"},
		{"bare double star", "coverage", []string{"**"}, true,
			"a double star reaches any depth"},
		{"leading double star", "coverage", []string{"**/*.html"}, true,
			"the double star can stand for coverage itself"},
		{"interior double star", "coverage", []string{"coverage/**/x.js"}, true,
			"the entry pins the first segment and then reaches any depth"},
		{"double star under a sibling", "coverage", []string{"dist/**"}, false,
			"the first segment cannot match coverage, so the double star is never reached"},
		{"single star segment", "coverage", []string{"*/report.html"}, true,
			"a single star matches the coverage segment"},
		{"single star at the root", "coverage", []string{"*"}, true,
			"the walk stops at the directory and calls that reaching; conservative, since doublestar's single star does not cross a separator and the entry in fact selects nothing under coverage"},
		{"degenerate entry", "coverage", []string{""}, true,
			"the degenerate entry selects everything"},
		{"dot slash prefix", "coverage", []string{"./coverage/report.html"}, true,
			"one leading ./ comes off before matching, as matchFilesField does"},
		{"anchored", "coverage", []string{"/coverage"}, true,
			"a leading / anchors rather than rooting, as matchFilesField reads it"},
		{"trailing slash on a literal", "coverage", []string{"coverage/"}, true,
			"the trailing slash comes off an entry with no star, as matchFilesField does"},
		{"brace literal", "weird{a,b}", []string{"weird{a,b}/**"}, true,
			"doublestar expands the braces and matches nothing, so the literal compare has to answer"},
		{"unparseable segment", "coverage", []string{"[unterminated/x.js"}, true,
			"an entry the glob engine will not parse cannot be decided, so it aborts"},
		{"brace spanning a separator", "ac", []string{"a{b,c/d}/x.js"}, true,
			"the split cuts the brace into three segments, the first is an unbalanced a{b,c that doublestar rejects, and the entry really does select ac/d/x.js"},
		{"second entry reaches", "coverage", []string{"dist", "coverage/report.html"}, true,
			"any one entry reaching is enough"},
		{"no entry reaches", "coverage", []string{"dist", "src/**", "*.md"}, false,
			"*.md cannot match a segment named coverage, and neither of the others starts there"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := filesFieldMayReach(tt.dir, tt.patterns); got != tt.want {
				t.Errorf("filesFieldMayReach(%q, %v) = %v, want %v (%s)",
					tt.dir, tt.patterns, got, tt.want, tt.why)
			}
		})
	}
}

// relPathsOf returns the packed set's relative paths sorted, for the tests here
// that need the files back from Pack rather than only its error.
func relPathsOf(files []*FileInfo) []string {
	got := make([]string, 0, len(files))
	for _, f := range files {
		got = append(got, f.RelPath)
	}
	sort.Strings(got)
	return got
}
