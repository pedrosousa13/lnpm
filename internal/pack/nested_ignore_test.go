package pack

import (
	"path/filepath"
	"testing"
)

// packTree writes files into a fresh package directory and returns the set of
// relative paths Pack selected. Keys are slash-separated paths relative to the
// package root; a "package.json" entry is supplied unless one is given.
func packTree(t *testing.T, name string, files map[string]string) map[string]bool {
	t.Helper()

	tmpDir := t.TempDir()
	if _, ok := files["package.json"]; !ok {
		writeTestFile(t, filepath.Join(tmpDir, "package.json"),
			`{"name": "`+name+`", "version": "1.0.0"}`)
	}
	for rel, content := range files {
		writeTestFile(t, filepath.Join(tmpDir, filepath.FromSlash(rel)), content)
	}

	_, packed, err := Pack(tmpDir)
	if err != nil {
		t.Fatalf("Pack() error: %v", err)
	}

	set := make(map[string]bool, len(packed))
	for _, f := range packed {
		set[f.RelPath] = true
	}
	return set
}

// assertPacked fails for every path in want that is missing and every path in
// unwanted that is present.
func assertPacked(t *testing.T, got map[string]bool, want, unwanted []string) {
	t.Helper()

	for _, rel := range want {
		if !got[rel] {
			t.Errorf("expected %q to be packed, packed set was %v", rel, got)
		}
	}
	for _, rel := range unwanted {
		if got[rel] {
			t.Errorf("expected %q to be excluded, packed set was %v", rel, got)
		}
	}
}

// TestPackHonoursNestedNpmignore packs the fixture from #315: a .npmignore in a
// subdirectory naming a file beside it. The developer has said "do not publish
// this" in the place npm and git both read it, and lnpm published the file
// anyway — invisibly, because the nested ignore file is itself excluded from the
// pack, so the reported file count looked right.
func TestPackHonoursNestedNpmignore(t *testing.T) {
	packed := packTree(t, "audit-leak", map[string]string{
		"index.js":        "module.exports = {}",
		"src/.npmignore":  "secrets.txt\n",
		"src/secrets.txt": "AKIAIOSFODNN7EXAMPLE",
		"src/index.js":    "export const x = 1",
		"src/helpers.js":  "export const y = 2",
	})

	assertPacked(t,
		packed,
		[]string{"package.json", "index.js", "src/index.js", "src/helpers.js"},
		[]string{"src/secrets.txt", "src/.npmignore"},
	)
}

// TestPackHonoursNestedGitignore is the second fixture from #315: a nested
// .gitignore with no sibling .npmignore, which is the shape a developer gets by
// default when they keep a key out of version control.
func TestPackHonoursNestedGitignore(t *testing.T) {
	packed := packTree(t, "audit-leak-git", map[string]string{
		"index.js":          "module.exports = {}",
		"config/.gitignore": "prod.key\n",
		"config/prod.key":   "-----BEGIN PRIVATE KEY-----",
		"config/dev.json":   `{"env":"dev"}`,
	})

	assertPacked(t,
		packed,
		[]string{"package.json", "index.js", "config/dev.json"},
		[]string{"config/prod.key", "config/.gitignore"},
	)
}

// TestPackNestedNpmignoreWinsOverNestedGitignore pins the root rule at depth: in
// one directory .npmignore replaces .gitignore rather than adding to it, so a
// pattern that lives only in the .gitignore stops applying.
func TestPackNestedNpmignoreWinsOverNestedGitignore(t *testing.T) {
	packed := packTree(t, "nested-precedence", map[string]string{
		"src/.npmignore":              "hidden-by-npmignore.txt\n",
		"src/.gitignore":              "hidden-by-gitignore.txt\n",
		"src/hidden-by-npmignore.txt": "secret",
		"src/hidden-by-gitignore.txt": "not secret to npm",
		"src/index.js":                "export const x = 1",
	})

	assertPacked(t,
		packed,
		[]string{"package.json", "src/index.js", "src/hidden-by-gitignore.txt"},
		[]string{"src/hidden-by-npmignore.txt"},
	)
}

// TestPackNestedPatternsResolveAgainstTheirOwnDirectory proves the base
// directory of a nested pattern is the directory holding the ignore file, not
// the package root. "/lib/gen.js" in src/.npmignore is anchored to src, so it
// excludes src/lib/gen.js and says nothing about lib/gen.js at the root.
//
// Resolving the same pattern text against the package root would flip both
// assertions, which is what makes this the test the second revert direction of
// #315 has to fail on.
func TestPackNestedPatternsResolveAgainstTheirOwnDirectory(t *testing.T) {
	packed := packTree(t, "nested-anchoring", map[string]string{
		"src/.npmignore":  "/lib/gen.js\n",
		"src/lib/gen.js":  "generated, do not publish",
		"src/lib/hand.js": "handwritten",
		"lib/gen.js":      "a different file the nested pattern must not reach",
	})

	assertPacked(t,
		packed,
		[]string{"package.json", "src/lib/hand.js", "lib/gen.js"},
		[]string{"src/lib/gen.js"},
	)
}

// TestPackDeeperIgnoreFileNegationReincludesFile pins precedence between depths:
// the deeper file is evaluated last, so its "!" wins over a root pattern that
// already matched. git allows this where the excluded thing is a file — a file
// under an excluded *directory* cannot be re-included, and that case stays as
// TestPackNpmignoreGitignoreSemantics documents it.
func TestPackDeeperIgnoreFileNegationReincludesFile(t *testing.T) {
	packed := packTree(t, "nested-negation", map[string]string{
		".npmignore":     "*.txt\n",
		"notes.txt":      "excluded at the root, nothing re-includes it",
		"src/.npmignore": "!keep.txt\n",
		"src/keep.txt":   "re-included by the deeper ignore file",
		"src/drop.txt":   "still excluded by the root pattern",
	})

	assertPacked(t,
		packed,
		[]string{"package.json", "src/keep.txt"},
		[]string{"notes.txt", "src/drop.txt"},
	)
}

// TestPackNestedIgnoreCannotReachIntoExcludedDirectory pins that a nested ignore
// file inside an excluded directory never re-includes anything, in both walk
// modes, because git never descends into an ignored directory at all.
//
// The two modes reach that answer by different routes, which is why both are
// asserted. Without a "files" field the walk prunes the directory and never
// reaches the ignore file. With one, #349 made the walk descend into ignored
// directories on purpose — a "files" entry may be a glob, so what it selects
// cannot be known without looking — and the default-include arm then asks the
// ignore files about docs/README.md. That arm read docs/.npmignore and let its
// "!README.md" re-include a file the root had excluded: the same package, the
// same ignore files, and opposite answers depending on whether an unrelated
// manifest field was set. It failed open, which is the direction that ships a
// file the maintainer excluded.
func TestPackNestedIgnoreCannotReachIntoExcludedDirectory(t *testing.T) {
	tests := []struct {
		name     string
		manifest string
	}{
		{
			name:     "no files field: the walk prunes docs",
			manifest: `{"name": "excluded-dir", "version": "1.0.0"}`,
		},
		{
			name:     "files whitelist: the walk descends into docs",
			manifest: `{"name": "excluded-dir", "version": "1.0.0", "files": ["dist"]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			packed := packTree(t, "excluded-dir", map[string]string{
				"package.json":    tt.manifest,
				".npmignore":      "docs/\n",
				"dist/index.js":   "module.exports = {}",
				"docs/.npmignore": "!README.md\n",
				"docs/README.md":  "under an excluded directory, so still excluded",
				"docs/guide.md":   "likewise",
			})

			assertPacked(t,
				packed,
				[]string{"package.json", "dist/index.js"},
				[]string{"docs/README.md", "docs/guide.md"},
			)
		})
	}
}

// TestIgnoreLoaderReadsEachDirectoryOnce pins the cost of reading ignore files
// at one read per directory per pack, however many paths that directory holds
// and however deep the tree goes. Without it every file re-reads its whole
// ancestor chain, which turns a package of N files D directories deep into N*D
// ignore-file reads.
//
// collectFiles builds exactly one loader per pack, so a read counted once here
// is a file opened once there.
//
// Directories with no ignore file have to be cached too, and they are the ones
// a plausible mistake drops: the cache lookup has to test its "ok" rather than
// whether the cached scopes are non-nil, because a directory with no ignore file
// anywhere above it caches an empty result that is indistinguishable from a
// miss. "." and "lib" below have no ignore file for exactly that reason — the
// package that most needs the cache is the one with no ignore files at all.
func TestIgnoreLoaderReadsEachDirectoryOnce(t *testing.T) {
	reads := make(map[string]int)
	withIgnoreFile := map[string]bool{"src": true, "src/deep": true}
	loader := newIgnoreLoader(t.TempDir())
	loader.read = func(dir string) []string {
		reads[dir]++
		if !withIgnoreFile[dir] {
			return nil
		}
		return []string{"ignored.txt"}
	}

	// Two files in each of three directories, plus a sibling branch: the
	// sibling is what catches a cache that stores only the last chain walked.
	for _, relPath := range []string{
		"top.js", "other.js",
		"src/a.js", "src/b.js",
		"src/deep/c.js", "src/deep/d.js",
		"lib/e.js", "lib/f.js",
	} {
		loader.excludes(relPath)
	}

	want := []string{".", "src", "src/deep", "lib"}
	for _, dir := range want {
		if reads[dir] != 1 {
			t.Errorf("directory %q was read %d times, want exactly 1", dir, reads[dir])
		}
	}
	if len(reads) != len(want) {
		t.Errorf("read %d directories (%v), want exactly %v", len(reads), reads, want)
	}
}

// TestPackWithoutNestedIgnoreFilesIsUnchanged pins that a root ignore file still
// governs the whole tree on its own terms: an unanchored pattern matches by
// basename at any depth, and an anchored one does not.
func TestPackWithoutNestedIgnoreFilesIsUnchanged(t *testing.T) {
	packed := packTree(t, "root-only", map[string]string{
		".npmignore":      "secrets.txt\n/local.json\n",
		"secrets.txt":     "secret",
		"local.json":      "{}",
		"src/secrets.txt": "secret at depth, matched by basename",
		"src/local.json":  "not matched: the pattern is anchored to the root",
		"src/index.js":    "export const x = 1",
	})

	assertPacked(t,
		packed,
		[]string{"package.json", "src/local.json", "src/index.js"},
		[]string{"secrets.txt", "local.json", "src/secrets.txt"},
	)
}
