package pack

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/pedrosousa13/lnpm/pkg/lockfile"
)

func TestReadPackageJSON(t *testing.T) {
	// Create temp directory
	tmpDir := t.TempDir()

	tests := []struct {
		name        string
		content     string
		wantName    string
		wantVersion string
		wantErr     bool
	}{
		{
			name:        "valid package",
			content:     `{"name": "my-package", "version": "1.0.0"}`,
			wantName:    "my-package",
			wantVersion: "1.0.0",
			wantErr:     false,
		},
		{
			name:        "scoped package",
			content:     `{"name": "@org/my-package", "version": "2.0.0"}`,
			wantName:    "@org/my-package",
			wantVersion: "2.0.0",
			wantErr:     false,
		},
		{
			name:    "missing name",
			content: `{"version": "1.0.0"}`,
			wantErr: true,
		},
		{
			name:    "missing version",
			content: `{"name": "my-package"}`,
			wantErr: true,
		},
		{
			name:    "invalid json",
			content: `{invalid}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test directory with package.json
			testDir := filepath.Join(tmpDir, tt.name)
			if err := os.MkdirAll(testDir, 0755); err != nil {
				t.Fatal(err)
			}
			pkgPath := filepath.Join(testDir, "package.json")
			if err := os.WriteFile(pkgPath, []byte(tt.content), 0644); err != nil {
				t.Fatal(err)
			}

			pkg, err := readPackageJSON(testDir)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if pkg.Name != tt.wantName {
				t.Errorf("name = %q, want %q", pkg.Name, tt.wantName)
			}
			if pkg.Version != tt.wantVersion {
				t.Errorf("version = %q, want %q", pkg.Version, tt.wantVersion)
			}
		})
	}
}

// TestReadPackageJSONRejectsADotPrefixedName states #325's acceptance criterion
// where the untrusted value actually enters: a manifest. Every path that stores,
// links or publishes a package reads its name from here, so this is the boundary
// the dot rule has to hold at. The rule itself is exercised in name_test.go.
func TestReadPackageJSONRejectsADotPrefixedName(t *testing.T) {
	dir := t.TempDir()
	manifest := `{"name": ".tmp-deadbeef", "version": "1.0.0"}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(manifest), 0644); err != nil {
		t.Fatal(err)
	}

	pkg, err := readPackageJSON(dir)
	if err == nil {
		t.Fatalf("readPackageJSON() accepted %q, returning %+v", manifest, pkg)
	}
	if !strings.Contains(err.Error(), ".tmp-deadbeef") {
		t.Errorf("readPackageJSON() error %q does not name the package", err)
	}
}

func TestIsExcluded(t *testing.T) {
	tests := []struct {
		path     string
		patterns []string
		want     bool
	}{
		{"node_modules/foo", []string{"node_modules"}, true},
		{"node_modules", []string{"node_modules"}, true},
		{"src/index.ts", []string{"node_modules"}, false},
		{".git/config", []string{".git/**"}, true},
		{"src/test.log", []string{"*.log"}, true},
		{"src/main.ts", []string{"*.log"}, false},
		{".env", []string{".env"}, true},
		{".env.local", []string{".env.*"}, true},
		{"src/.env.test", []string{".env.*"}, true},          // Pattern without / matches anywhere
		{"src/.env.test", []string{"./.env.*"}, false},       // Pattern with / only matches at root
		{"deep/nested/file.log", []string{"*.log"}, true},    // Matches anywhere in tree
		{"src/config.json", []string{"src/*.json"}, true},    // Pattern with / matches specific path
		{"other/config.json", []string{"src/*.json"}, false}, // Doesn't match other paths
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := isExcluded(tt.path, tt.patterns)
			if got != tt.want {
				t.Errorf("isExcluded(%q, %v) = %v, want %v", tt.path, tt.patterns, got, tt.want)
			}
		})
	}
}

// TestIsExcludedGitignoreSemantics pins the gitignore/npmignore semantics
// isExcluded implements: a trailing slash excludes a directory and everything
// under it, a leading slash anchors a pattern to the package root, and among
// several matching patterns the last one wins, so a "!" pattern can re-include
// a path an earlier pattern excluded.
func TestIsExcludedGitignoreSemantics(t *testing.T) {
	tests := []struct {
		path     string
		patterns []string
		want     bool
	}{
		// Trailing slash: "dist/" excludes the dist directory and everything
		// under it, and nothing else.
		// CAVEAT on {"dist", ...}: git's trailing slash matches directories
		// only, and isExcluded gets no directory signal, so it also matches a
		// same-named plain file. This case assumes "dist" is a directory.
		{"dist/index.js", []string{"dist/"}, true},
		{"dist", []string{"dist/"}, true},
		{"src/index.ts", []string{"dist/"}, false},

		// Leading slash anchors the pattern to the package root, so a nested
		// path of the same name further down the tree is not matched.
		{"credentials.json", []string{"/credentials.json"}, true},
		{"dist/index.js", []string{"/dist"}, true},
		{"src/dist/index.js", []string{"/dist"}, false},

		// Negation: the last matching pattern wins, so "!dist/keep.js"
		// re-includes a file that "dist/*" excluded, while its siblings stay
		// excluded. The pattern is "dist/*" and not "dist/" because a trailing
		// slash prunes the directory outright, and nothing beneath a pruned
		// directory can be re-included; on "dist/*" git and npm agree.
		{"dist/keep.js", []string{"dist/*", "!dist/keep.js"}, false},
		{"dist/drop.js", []string{"dist/*", "!dist/keep.js"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := isExcluded(tt.path, tt.patterns)
			if got != tt.want {
				t.Errorf("isExcluded(%q, %v) = %v, want %v", tt.path, tt.patterns, got, tt.want)
			}
		})
	}
}

// TestIsExcludedNegationDoesNotReachDescendants pins git's rule that "it is not
// possible to re-include a file if a parent directory of that file is
// excluded": a "!" pattern re-includes only the paths it matches directly,
// never the paths that merely sit underneath a directory it matched. A positive
// pattern keeps the opposite behavior, because git ignores everything inside an
// ignored directory.
func TestIsExcludedNegationDoesNotReachDescendants(t *testing.T) {
	tests := []struct {
		path     string
		patterns []string
		want     bool
	}{
		// "!foo" names the directory, not its contents, so foo/bar stays out.
		{"foo/bar", []string{"foo/bar", "!foo"}, true},
		{"foo/sub/baz.js", []string{"foo/**", "!foo"}, true},
		{"dist/a.js", []string{"dist/", "!dist/"}, true},

		// A negation still matches its own path exactly.
		{"foo", []string{"foo", "!foo"}, false},
		{"dist", []string{"dist/", "!dist/"}, false},

		// A positive parent pattern still excludes everything below it.
		{"node_modules/foo/index.js", []string{"node_modules"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := isExcluded(tt.path, tt.patterns)
			if got != tt.want {
				t.Errorf("isExcluded(%q, %v) = %v, want %v", tt.path, tt.patterns, got, tt.want)
			}
		})
	}
}

func TestIsIncluded(t *testing.T) {
	tests := []struct {
		path     string
		patterns []string
		want     bool
	}{
		{"dist/index.js", []string{"dist"}, true},
		{"dist/lib/utils.js", []string{"dist"}, true},
		{"src/index.ts", []string{"dist"}, false},
		{"lib/index.js", []string{"dist", "lib"}, true},
		{"README.md", []string{"dist", "README.md"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := isIncluded(tt.path, tt.patterns)
			if got != tt.want {
				t.Errorf("isIncluded(%q, %v) = %v, want %v", tt.path, tt.patterns, got, tt.want)
			}
		})
	}
}

// TestIsIncludedPatternForms pins every spelling of a directory entry in the
// "files" whitelist against one path. npm treats a leading "/" as anchoring to
// the package root rather than as an absolute path, a leading "./" as no part
// of the name, and a trailing "/" as a directory marker, so the plain, anchored,
// "./" and "/**" spellings of "dist" all ship dist/cli/index.js.
//
// "dist/**/" is the exception that keeps the trailing-slash normalization
// honest: npm ships nothing from dist for it, because a trailing slash on a
// glob is not a directory marker.
//
// The ".//dist", "/./dist" and "././dist" rows are what pin #346's trim to one
// "./" and to running before the "/" trim, and their expectations are as
// surprising as they look: npm ships dist for ".//dist" and nothing for either
// "/./dist" or "././dist". Swap the two trims and ".//dist" and "/./dist" both
// flip — measured, not predicted.
//
// Each expectation here was verified against "npm pack --dry-run" on a fixture
// package, the "./" rows included — none is inferred from a neighbouring row.
func TestIsIncludedPatternForms(t *testing.T) {
	const relPath = "dist/cli/index.js"

	tests := []struct {
		pattern string
		want    bool
	}{
		{"dist", true},
		{"/dist", true},
		{"dist/", true},
		{"/dist/", true},
		{"./dist", true},
		{"./dist/", true},
		{".//dist", true},
		{"/./dist", false},
		{"././dist", false},
		{"dist/**", true},
		{"/dist/**", true},
		{"./dist/**", true},
		{"dist/**/", false},
		{"./dist/**/", false},
		{"lib", false},
		{"/lib", false},
		{"./lib", false},
	}

	for _, tt := range tests {
		t.Run(tt.pattern, func(t *testing.T) {
			got := isIncluded(relPath, []string{tt.pattern})
			if got != tt.want {
				t.Errorf("isIncluded(%q, [%q]) = %v, want %v", relPath, tt.pattern, got, tt.want)
			}
		})
	}
}

// TestIsIncludedDegeneratePattern pins the "files" entries that are empty once
// normalized: "/" and "//" lose everything to the leading- and trailing-slash
// normalization, "./" loses everything to #346's "./" trim, and "" starts empty.
// npm 11.16.0 ships the same file set for all four as it does for a package with
// no "files" field at all, so an empty normalized pattern includes everything.
//
// The "." row is the trap and is why it sits here rather than being left
// unwritten. It looks like a fifth degenerate spelling and is not: npm 11.16.0
// ships only the always-included set for "files": ["."] — README.md and
// package.json on the fixture it was run against — so "." selects nothing and
// lnpm matches that. Every expectation in this test was run against
// "npm pack --dry-run" rather than reasoned from the others.
//
// isExcluded already skips a degenerate pattern (it neither excludes nor
// un-excludes anything), and the two functions must agree: neither may filter a
// path out on the strength of one. "." is not in that loop because it is not
// degenerate on either side.
func TestIsIncludedDegeneratePattern(t *testing.T) {
	const relPath = "dist/cli/index.js"

	tests := []struct {
		name    string
		pattern string
		want    bool
	}{
		{"slash", "/", true},
		{"double slash", "//", true},
		{"empty", "", true},
		{"dot slash", "./", true},
		{"dot", ".", false},
		{"dist", "dist", true},
		{"lib", "lib", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isIncluded(relPath, []string{tt.pattern})
			if got != tt.want {
				t.Errorf("isIncluded(%q, [%q]) = %v, want %v", relPath, tt.pattern, got, tt.want)
			}
		})
	}

	for _, pattern := range []string{"/", "//", "", "./"} {
		t.Run("agrees with isExcluded "+pattern, func(t *testing.T) {
			if !isIncluded(relPath, []string{pattern}) {
				t.Errorf("isIncluded(%q, [%q]) = false, want true", relPath, pattern)
			}
			if isExcluded(relPath, []string{pattern}) {
				t.Errorf("isExcluded(%q, [%q]) = true, want false", relPath, pattern)
			}
		})
	}
}

// filesMatchNames spells a filesMatch for a failure message. The type has no
// String method, and "1" versus "2" in a diff says nothing about which of
// containment and naming was expected.
var filesMatchNames = map[filesMatch]string{
	filesMatchNone:     "none",
	filesMatchContains: "contains",
	filesMatchDirect:   "direct",
}

// TestMatchFilesFieldDotSlashAgreesWithUnprefixedForm is #346's core claim, and
// it asserts the classification rather than the boolean isIncluded reduces it
// to. Selecting the same paths is not enough: #321 lets a "files" entry override
// defaultExcludes only for a path it *names*, so a normalization that turned
// "./dist" into a direct match would select the right files and additionally
// publish dist/.env. Each row therefore compares matchFilesField for the two
// spellings, and pins the shared answer so a row cannot pass by both spellings
// being equally broken.
//
// The two filesMatchNone rows are as load-bearing as the rest. A normalization
// wide enough to make "./dist/**/" select something would have repaired an entry
// npm ships nothing for, and "lib" is the row that fails if a bug makes every
// "./"-prefixed entry match everything rather than nothing.
//
// How the trim is ordered against the leading-"/" trim is pinned elsewhere:
// TestIsIncludedPatternForms carries the ".//dist", "/./dist" and "././dist"
// rows. This table cannot express them: every row here runs one entry and that
// same entry with "./" prepended, which is not what those three spellings are.
func TestMatchFilesFieldDotSlashAgreesWithUnprefixedForm(t *testing.T) {
	tests := []struct {
		name    string
		relPath string
		// entry is the unprefixed spelling. Each row runs it and "./"+entry.
		entry string
		want  filesMatch
	}{
		{"directory", "dist/index.js", "dist", filesMatchContains},
		{"nested file", "lib/keep.js", "lib/keep.js", filesMatchDirect},
		{"subtree glob", "dist/index.js", "dist/**", filesMatchContains},
		{"bare wildcard segment", "dist/index.js", "dist/*", filesMatchContains},
		{"glob constraining the segment", "dist/.env", "dist/*.env", filesMatchDirect},
		{"trailing slash directory", "dist/index.js", "dist/", filesMatchContains},
		{"trailing slash on a glob", "dist/cli/index.js", "dist/**/", filesMatchNone},
		{"unrelated directory", "dist/index.js", "lib", filesMatchNone},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plain := matchFilesField(tt.relPath, []string{tt.entry})
			if plain != tt.want {
				t.Fatalf("matchFilesField(%q, [%q]) = %s, want %s",
					tt.relPath, tt.entry, filesMatchNames[plain], filesMatchNames[tt.want])
			}

			dotted := matchFilesField(tt.relPath, []string{"./" + tt.entry})
			if dotted != plain {
				t.Errorf("matchFilesField(%q, [%q]) = %s, but the unprefixed [%q] = %s; "+
					"npm reads a leading \"./\" as no part of the name, so the two "+
					"spellings must classify the same",
					tt.relPath, "./"+tt.entry, filesMatchNames[dotted], tt.entry, filesMatchNames[plain])
			}
		})
	}
}

// TestIsExcludedDoesNotResolveLeadingDotSlash pins the half of #346 that stays
// as it is, and it is the second direction of that issue's revert check: apply
// matchFilesField's "./" normalization to the ignore matcher as well and this
// test goes red.
//
// The asymmetry is deliberate. git does not resolve a leading "./" in an ignore
// pattern, while npm does in a "files" entry, so matching each format's own tool
// means the two matchers differ here. Both halves were run rather than assumed:
// on git 2.43.0 a .gitignore holding "./dist" leaves dist/index.js untracked and
// one holding "dist" ignores it, and on npm 11.16.0 "files": ["./dist"] ships
// the same tarball as ["dist"]. isIncluded's doc comment records the decision.
//
// The isIncluded half of each row is asserted too, so the test states the
// asymmetry rather than only one side of it: the same string reaches the path as
// a "files" entry and does not as an ignore pattern.
//
// This is not the only row that catches the symmetric change. Adding the same
// trim to applyIgnorePatterns also turns TestIsExcluded's "./.env.*" row red,
// which is a pre-existing pin nobody wrote for this purpose — measured, both
// tests fail together.
func TestIsExcludedDoesNotResolveLeadingDotSlash(t *testing.T) {
	tests := []struct {
		name     string
		relPath  string
		pattern  string
		unprefix string
	}{
		{"directory", "dist/index.js", "./dist", "dist"},
		{"root file", "index.js", "./index.js", "index.js"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !isExcluded(tt.relPath, []string{tt.unprefix}) {
				t.Fatalf("isExcluded(%q, [%q]) = false, want true (control)", tt.relPath, tt.unprefix)
			}
			if isExcluded(tt.relPath, []string{tt.pattern}) {
				t.Errorf("isExcluded(%q, [%q]) = true, want false: git does not resolve a "+
					"leading \"./\" in an ignore pattern, so lnpm must not either",
					tt.relPath, tt.pattern)
			}
			if !isIncluded(tt.relPath, []string{tt.pattern}) {
				t.Errorf("isIncluded(%q, [%q]) = false, want true: npm does resolve a "+
					"leading \"./\" in a \"files\" entry",
					tt.relPath, tt.pattern)
			}
		})
	}
}

// TestIsDefaultInclude pins the always-included set to the package root.
//
// The nested rows below all used to answer true: isDefaultInclude matched
// filepath.Base(relPath), so a README, LICENSE or CHANGES anywhere in the tree
// was force-included past a "files" whitelist. Regression test for #320.
//
// The nested rows are also the platform guard, and the Windows CI job is where
// they earn their keep — on Linux and macOS they were already true. Both build
// path_unix.go, so filepath.Separator is "/" on each and the two candidate
// spellings cannot be told apart there. collectFiles hands this function a
// relPath already through filepath.ToSlash, so its separator is always "/", but
// filepath.Match reads its separator from the platform: on Linux
// filepath.Match("readme*", "dist/readme.md") is false — run and confirmed — so
// on Linux alone, dropping filepath.Base would be enough to pass these rows.
// Windows builds path_windows.go, where the separator is "\" and "/" is an
// ordinary character; TestIsExcludedSingleStarNeverCrossesSeparatorOnAnyPlatform
// below pins the same split biting the ignore matcher. isDefaultInclude
// therefore rejects a separator itself, before filepath.Match ever sees the
// path, and only the Windows job can show that the check is load-bearing.
func TestIsDefaultInclude(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		// At the root, unchanged — including the case-folding.
		{"package.json", true},
		{"README.md", true},
		{"readme.txt", true},
		{"LICENSE", true},
		{"license.md", true},
		{"LICENCE", true},
		{"CHANGELOG.md", true},
		{"changes.txt", true},
		{"HISTORY.md", true},
		{"README.anything", true},
		{"src/index.ts", false},
		{"dist/index.js", false},
		// "package.json" is the only entry carrying no "*", so it is the only
		// one a literal strings.HasPrefix(relPath, pattern) anchor could ever
		// fire on: HasPrefix compares "*" as an ordinary byte, so every other
		// entry matches nothing. Run against that spelling, the glob rows above
		// fail for under-matching — a root README.md stops being included at
		// all — and this row is the only one that catches it over-including:
		// strings.HasPrefix("package.jsonc", "package.json") is true, so a
		// package.jsonc would ship past the whitelist.
		{"package.jsonc", false},

		// Below the root, no longer force-included. Every one of these was
		// true before #320.
		{"docs/README.md", false},
		{"dist/README.md", false},
		{"internal-docs/README.private", false},
		{"notes/changes.txt", false},
		{"vendor/foo/LICENSE", false},
		{"vendor/foo/package.json", false},
		{"a/b/c/CHANGELOG.md", false},
		// A nested path whose *directory* carries an always-included name,
		// where every row above carries it in the basename. This does not
		// catch the literal HasPrefix spelling — run and confirmed,
		// strings.HasPrefix("changes/secret.txt", "changes*") is false, so
		// that spelling never reaches it and package.jsonc above is what
		// holds it. What this row catches is the obvious repair to that
		// spelling: strip the "*" and prefix-match the stem, at which point
		// strings.HasPrefix("changes/secret.txt", "changes") is true and
		// nested paths are admitted again.
		{"changes/secret.txt", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := isDefaultInclude(tt.path)
			if got != tt.want {
				t.Errorf("isDefaultInclude(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestPack(t *testing.T) {
	// Create a temp package directory
	tmpDir := t.TempDir()

	// Create package.json
	pkgJSON := `{
		"name": "test-package",
		"version": "1.0.0",
		"main": "dist/index.js",
		"files": ["dist"]
	}`
	if err := os.WriteFile(filepath.Join(tmpDir, "package.json"), []byte(pkgJSON), 0644); err != nil {
		t.Fatal(err)
	}

	// Create dist directory with files
	distDir := filepath.Join(tmpDir, "dist")
	if err := os.MkdirAll(distDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(distDir, "index.js"), []byte("module.exports = {}"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(distDir, "utils.js"), []byte("module.exports = {}"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create src directory (should be excluded)
	srcDir := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "index.ts"), []byte("export {}"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create README
	if err := os.WriteFile(filepath.Join(tmpDir, "README.md"), []byte("# Test"), 0644); err != nil {
		t.Fatal(err)
	}

	// Run pack
	pkg, files, err := Pack(tmpDir)
	if err != nil {
		t.Fatalf("Pack() error: %v", err)
	}

	// Check package info
	if pkg.Name != "test-package" {
		t.Errorf("pkg.Name = %q, want %q", pkg.Name, "test-package")
	}
	if pkg.Version != "1.0.0" {
		t.Errorf("pkg.Version = %q, want %q", pkg.Version, "1.0.0")
	}

	// Check files
	fileNames := make(map[string]bool)
	for _, f := range files {
		fileNames[f.RelPath] = true
	}

	// Should include
	expectedFiles := []string{"package.json", "README.md", "dist/index.js", "dist/utils.js"}
	for _, name := range expectedFiles {
		if !fileNames[name] {
			t.Errorf("expected file %q to be included", name)
		}
	}

	// Should NOT include
	excludedFiles := []string{"src/index.ts"}
	for _, name := range excludedFiles {
		if fileNames[name] {
			t.Errorf("expected file %q to be excluded", name)
		}
	}
}

// TestPackRootAnchoredFilesWhitelist packs a real package whose "files"
// whitelist is spelled with a leading "/", the form npm treats as anchored to
// the package root. Regression test for #207: the anchored entries matched
// nothing, so dist/ was silently dropped and the reporting package published
// without the CLI entry point its "bin" field pointed at.
//
// Every assertion below names a path the whitelist actually controls. The
// fixture's package.json and README.md are deliberately not asserted: they ship
// via isDefaultInclude whatever "files" says, so asserting them would pass even
// with the whitelist broken, which is what this test exists to catch.
func TestPackRootAnchoredFilesWhitelist(t *testing.T) {
	tmpDir := t.TempDir()

	pkgJSON := `{
		"name": "root-anchored-files",
		"version": "1.0.0",
		"files": ["/dist", "/dist-cjs", "README.md"]
	}`
	if err := os.WriteFile(filepath.Join(tmpDir, "package.json"), []byte(pkgJSON), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "README.md"), []byte("# Test"), 0644); err != nil {
		t.Fatal(err)
	}

	cliDir := filepath.Join(tmpDir, "dist", "cli")
	if err := os.MkdirAll(cliDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cliDir, "index.js"), []byte("#!/usr/bin/env node"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "dist", "index.js"), []byte("module.exports = {}"), 0644); err != nil {
		t.Fatal(err)
	}

	cjsDir := filepath.Join(tmpDir, "dist-cjs")
	if err := os.MkdirAll(cjsDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cjsDir, "index.cjs"), []byte("module.exports = {}"), 0644); err != nil {
		t.Fatal(err)
	}

	srcDir := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "index.ts"), []byte("export {}"), 0644); err != nil {
		t.Fatal(err)
	}

	_, files, err := Pack(tmpDir)
	if err != nil {
		t.Fatalf("Pack() error: %v", err)
	}

	packed := make(map[string]bool)
	for _, f := range files {
		packed[f.RelPath] = true
	}

	expected := []string{
		"dist/index.js",
		"dist/cli/index.js",
		"dist-cjs/index.cjs",
	}
	for _, name := range expected {
		if !packed[name] {
			t.Errorf("expected %q to be packed, packed set was %v", name, packed)
		}
	}

	if packed["src/index.ts"] {
		t.Errorf("%q is outside the whitelist and must not be packed", "src/index.ts")
	}
}

// TestPackDotSlashFilesWhitelist is #346's reported fixture, packed through the
// real path. A manifest declaring "files": ["./dist"] published package.json and
// nothing else: matchFilesField dropped a leading "/" from every entry and left a
// leading "./" alone, so the entry matched no path in the tree. npm accepts the
// spelling and treats it as "dist".
//
// No ignore file is written on purpose. The same symptom has a second cause —
// #318, a "files" entry losing to a .gitignore rule — and an ignore file in the
// fixture would leave which one is under test ambiguous.
//
// The nested "./lib/keep.js" entry is the issue's second acceptance criterion
// and a different branch: it is a direct name rather than a directory that
// contains. lib/drop.js sits beside it so the fixture shows the entry selecting
// one file rather than the directory, and src/index.ts shows an unnamed
// directory staying out.
//
// package.json is in the expected set only because assertPackedSet compares the
// whole set. It carries no evidence: it ships via the manifest force-include
// whatever "files" says, and the bug this test is about published exactly
// [package.json]. Every other path in the set is one the whitelist controls.
// The fixture has no README.md for the same reason — isDefaultInclude would
// ship it either way.
func TestPackDotSlashFilesWhitelist(t *testing.T) {
	tmpDir := t.TempDir()
	writeMainEntryTree(t, tmpDir, map[string]string{
		"package.json": `{
			"name": "dot-slash-files",
			"version": "1.0.0",
			"files": ["./dist", "./lib/keep.js"]
		}`,
		"dist/index.js":     "module.exports = {}",
		"dist/cli/index.js": "#!/usr/bin/env node",
		"lib/keep.js":       "module.exports = {}",
		"lib/drop.js":       "module.exports = {}",
		"src/index.ts":      "export {}",
	})

	assertPackedSet(t, tmpDir, []string{
		"dist/cli/index.js",
		"dist/index.js",
		"lib/keep.js",
		"package.json",
	}, "npm reads \"./dist\" as \"dist\" and \"./lib/keep.js\" as \"lib/keep.js\"")
}

// TestPackDegenerateFilesEntryShipsEverything packs the same fixture once with
// no "files" field, as a baseline, then once for each degenerate spelling —
// ["/"], ["//"], [""] and ["./"] — and requires every file set to equal the
// baseline. npm 11.16.0 produces the same tarball for all five; lnpm shipped
// only package.json and README.md for the degenerate spellings, dropping every
// file the whitelist named. Regression test for #227, and for #346 on the
// ["./"] spelling, which #227 did not reach.
//
// ["."] is deliberately absent. npm ships only the always-included set for it,
// so it is not a degenerate spelling at all — TestIsIncludedDegeneratePattern
// carries that row and the measurement behind it.
//
// The dist/ and top.js paths are asserted explicitly as well as compared,
// because package.json and README.md ship via isDefaultInclude whatever "files"
// says: a set comparison that only ever saw those two would pass with the fix
// reverted.
func TestPackDegenerateFilesEntryShipsEverything(t *testing.T) {
	writeFixture := func(t *testing.T, pkgJSON string) map[string]bool {
		t.Helper()
		tmpDir := t.TempDir()

		if err := os.WriteFile(filepath.Join(tmpDir, "package.json"), []byte(pkgJSON), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(tmpDir, "README.md"), []byte("# Test"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(tmpDir, "top.js"), []byte("module.exports = {}"), 0644); err != nil {
			t.Fatal(err)
		}

		cliDir := filepath.Join(tmpDir, "dist", "cli")
		if err := os.MkdirAll(cliDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(cliDir, "index.js"), []byte("#!/usr/bin/env node"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(tmpDir, "dist", "index.js"), []byte("module.exports = {}"), 0644); err != nil {
			t.Fatal(err)
		}

		_, files, err := Pack(tmpDir)
		if err != nil {
			t.Fatalf("Pack() error: %v", err)
		}

		packed := make(map[string]bool)
		for _, f := range files {
			packed[f.RelPath] = true
		}
		return packed
	}

	baseline := writeFixture(t, `{"name": "degenerate-files", "version": "1.0.0"}`)

	for _, entry := range []string{"/", "//", "", "./"} {
		t.Run("files "+entry, func(t *testing.T) {
			pkgJSON := `{"name": "degenerate-files", "version": "1.0.0", "files": ["` + entry + `"]}`
			packed := writeFixture(t, pkgJSON)

			for _, name := range []string{"dist/index.js", "dist/cli/index.js", "top.js"} {
				if !packed[name] {
					t.Errorf("expected %q to be packed for files: [%q], packed set was %v", name, entry, packed)
				}
			}

			for name := range baseline {
				if !packed[name] {
					t.Errorf("files: [%q] dropped %q, which ships with no \"files\" field", entry, name)
				}
			}
			for name := range packed {
				if !baseline[name] {
					t.Errorf("files: [%q] shipped %q, which does not ship with no \"files\" field", entry, name)
				}
			}
		})
	}
}

// TestPackNpmignoreGitignoreSemantics packs a real package whose .npmignore
// uses the pattern forms #150 added: a root-anchored "/credentials.json", a
// trailing-slash directory "dist/", and a negation "!dist/keep.js".
func TestPackNpmignoreGitignoreSemantics(t *testing.T) {
	tmpDir := t.TempDir()

	pkgJSON := `{
		"name": "ignore-semantics",
		"version": "1.0.0"
	}`
	if err := os.WriteFile(filepath.Join(tmpDir, "package.json"), []byte(pkgJSON), 0644); err != nil {
		t.Fatal(err)
	}

	npmignore := "/credentials.json\ndist/\n!dist/keep.js\n"
	if err := os.WriteFile(filepath.Join(tmpDir, ".npmignore"), []byte(npmignore), 0644); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(tmpDir, "credentials.json"), []byte(`{"token":"x"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "index.js"), []byte("module.exports = {}"), 0644); err != nil {
		t.Fatal(err)
	}

	distDir := filepath.Join(tmpDir, "dist")
	if err := os.MkdirAll(distDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(distDir, "keep.js"), []byte("keep"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(distDir, "drop.js"), []byte("drop"), 0644); err != nil {
		t.Fatal(err)
	}

	// A nested credentials.json proves "/credentials.json" is anchored to the
	// package root and does not exclude the same name further down the tree.
	nestedDir := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(nestedDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nestedDir, "credentials.json"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}

	_, files, err := Pack(tmpDir)
	if err != nil {
		t.Fatalf("Pack() error: %v", err)
	}

	packed := make(map[string]bool)
	for _, f := range files {
		packed[f.RelPath] = true
	}

	for _, name := range []string{"package.json", "index.js", "src/credentials.json"} {
		if !packed[name] {
			t.Errorf("expected file %q to be packed, packed set was %v", name, packed)
		}
	}
	for _, name := range []string{"credentials.json", "dist/drop.js", ".npmignore"} {
		if packed[name] {
			t.Errorf("expected file %q to be excluded", name)
		}
	}

	// "dist/" + "!dist/keep.js" is the one combination where git and npm
	// disagree: git prunes the whole directory and never reconsiders keep.js,
	// npm packs both files. lnpm lands on git's answer, but by a different
	// route than isExcluded alone: isExcluded("dist/keep.js", ...) is false
	// here, because the negation is the last matching pattern. The file is
	// still not packed because collectFiles returns filepath.SkipDir for the
	// excluded "dist" directory, so the walk never reaches keep.js to ask.
	//
	// Which answer is correct is an open product decision (see #150). This
	// assertion records what lnpm does today rather than endorsing it.
	if packed["dist/keep.js"] {
		t.Errorf("dist/keep.js was packed: directory pruning no longer beats a negation, which changes documented behavior")
	}
}

// TestPackNpmignoreNegationReincludesFile packs a real package whose .npmignore
// re-includes one file out of an otherwise ignored directory.
//
// The pattern is "dist/*", not "dist/": a trailing slash prunes the directory
// during the walk, so a later negation can never reach inside it. "dist/*"
// matches the entries instead, and on that form git and npm agree — keep.js is
// re-included, drop.js stays out.
func TestPackNpmignoreNegationReincludesFile(t *testing.T) {
	tmpDir := t.TempDir()

	pkgJSON := `{
		"name": "ignore-negation",
		"version": "1.0.0"
	}`
	if err := os.WriteFile(filepath.Join(tmpDir, "package.json"), []byte(pkgJSON), 0644); err != nil {
		t.Fatal(err)
	}

	npmignore := "/credentials.json\ndist/*\n!dist/keep.js\n"
	if err := os.WriteFile(filepath.Join(tmpDir, ".npmignore"), []byte(npmignore), 0644); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(tmpDir, "credentials.json"), []byte(`{"token":"x"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "index.js"), []byte("module.exports = {}"), 0644); err != nil {
		t.Fatal(err)
	}

	distDir := filepath.Join(tmpDir, "dist")
	if err := os.MkdirAll(distDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(distDir, "keep.js"), []byte("keep"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(distDir, "drop.js"), []byte("drop"), 0644); err != nil {
		t.Fatal(err)
	}

	// A nested credentials.json proves "/credentials.json" is anchored to the
	// package root and does not exclude the same name further down the tree.
	nestedDir := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(nestedDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nestedDir, "credentials.json"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}

	_, files, err := Pack(tmpDir)
	if err != nil {
		t.Fatalf("Pack() error: %v", err)
	}

	packed := make(map[string]bool)
	for _, f := range files {
		packed[f.RelPath] = true
	}

	for _, name := range []string{"package.json", "index.js", "src/credentials.json", "dist/keep.js"} {
		if !packed[name] {
			t.Errorf("expected file %q to be packed, packed set was %v", name, packed)
		}
	}
	for _, name := range []string{"credentials.json", "dist/drop.js"} {
		if packed[name] {
			t.Errorf("expected file %q to be excluded", name)
		}
	}
}

// TestIsExcludedHardReservedCannotBeNegated proves a user "!" pattern cannot
// re-include a hard-reserved path when the two lists are concatenated: the last
// matching pattern wins, so a hard-reserved entry always has the final say.
// collectFiles reaches the same answer by a different route — it evaluates
// hardReservedExcludes on its own and above every selection rule, where a user
// negation is not in the list to begin with.
//
// The .env row this test used to carry moved to
// TestIsExcludedDefaultExcludesCanBeNegated below when #321 split the list. That
// is the behaviour change, not a hole here.
func TestIsExcludedHardReservedCannotBeNegated(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		userPats []string
	}{
		{"negated node_modules file", "node_modules/foo", []string{"!node_modules/foo"}},
		{"negated node_modules dir", "node_modules", []string{"!node_modules"}},
		{"negated .npmrc", ".npmrc", []string{"!.npmrc"}},
		{"negated .git file", ".git/config", []string{"!.git/config"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patterns := append(append([]string{}, tt.userPats...), hardReservedExcludes...)
			if !isExcluded(tt.path, patterns) {
				t.Errorf("isExcluded(%q, user %v + hard-reserved) = false, want true: a user negation must not re-include a hard-reserved path", tt.path, tt.userPats)
			}
		})
	}
}

// TestIsExcludedDefaultExcludesCanBeNegated is the matcher-level half of #321,
// and the exact inverse of the test above. defaultExcludes is seeded into the
// ignore chain rather than evaluated above it, which in matcher terms means the
// user's patterns run *after* it — so a negation is the last match and wins.
//
// The concatenation order here is the one ignoreLoader.excludes produces:
// defaults first, the user's patterns after. Reversing it is what the old
// single-list arrangement did, and it is why nothing could override the list.
func TestIsExcludedDefaultExcludesCanBeNegated(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		userPats []string
	}{
		{"negated .env.example", ".env.example", []string{"!.env.example"}},
		{"negated .env", ".env", []string{"!.env"}},
		{"negated log", "app.log", []string{"!app.log"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patterns := append(append([]string{}, defaultExcludes...), tt.userPats...)
			if isExcluded(tt.path, patterns) {
				t.Errorf("isExcluded(%q, defaults + user %v) = true, want false: a user negation must be able to re-include an overridable default", tt.path, tt.userPats)
			}
		})
	}
}

// TestPackHardReservedWinsInWhitelistMode proves the hard-reserved set still
// wins when package.json has a "files" whitelist naming its entries and
// .npmignore tries to negate them: hardReservedExcludes is evaluated first and
// on its own, ahead of both the user's patterns and the whitelist.
//
// The .env rows this test used to carry are now
// TestPackFilesEntryOverridesDefaultExcludes, which asserts the opposite. That
// is #321's change: .env moved to the overridable list, and only the entries
// still on npm's force-ignore list are pinned here.
func TestPackHardReservedWinsInWhitelistMode(t *testing.T) {
	tmpDir := t.TempDir()

	pkgJSON := `{
		"name": "whitelist-defaults",
		"version": "1.0.0",
		"files": ["dist", ".npmrc", "node_modules"]
	}`
	if err := os.WriteFile(filepath.Join(tmpDir, "package.json"), []byte(pkgJSON), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, ".npmignore"), []byte("!.npmrc\n!node_modules\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, ".npmrc"), []byte("//registry:_authToken=deadbeef"), 0644); err != nil {
		t.Fatal(err)
	}

	nodeModules := filepath.Join(tmpDir, "node_modules", "dep")
	if err := os.MkdirAll(nodeModules, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nodeModules, "index.js"), []byte("dep"), 0644); err != nil {
		t.Fatal(err)
	}

	distDir := filepath.Join(tmpDir, "dist")
	if err := os.MkdirAll(distDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(distDir, "index.js"), []byte("ok"), 0644); err != nil {
		t.Fatal(err)
	}

	_, files, err := Pack(tmpDir)
	if err != nil {
		t.Fatalf("Pack() error: %v", err)
	}

	packed := make(map[string]bool)
	for _, f := range files {
		packed[f.RelPath] = true
	}

	if !packed["dist/index.js"] {
		t.Errorf("expected whitelisted file %q to be packed, packed set was %v", "dist/index.js", packed)
	}
	for _, name := range []string{".npmrc", "node_modules/dep/index.js"} {
		if packed[name] {
			t.Errorf("hard-reserved %q was packed: neither a \"files\" entry nor a user negation may re-include it", name)
		}
	}
}

// TestPackFilesFieldWinsOverGitignore packs the standard TypeScript setup: a
// .gitignore holding the build output out of version control, and a "files"
// field asking for exactly that build output. npm documents that a path the
// "files" field names cannot be excluded by .npmignore or .gitignore, so dist
// ships even though it is ignored.
//
// The .gitignore also names two paths the "files" field does not — a coverage
// directory and README.md, which would otherwise arrive through
// defaultIncludes — to prove the ignore rules still apply everywhere the
// whitelist is silent.
//
// Regression test for #318.
func TestPackFilesFieldWinsOverGitignore(t *testing.T) {
	tmpDir := t.TempDir()

	pkgJSON := `{
		"name": "ts-build",
		"version": "1.0.0",
		"main": "dist/index.js",
		"files": ["dist"]
	}`
	if err := os.WriteFile(filepath.Join(tmpDir, "package.json"), []byte(pkgJSON), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, ".gitignore"), []byte("dist/\ncoverage/\nREADME.md\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "README.md"), []byte("# ts-build"), 0644); err != nil {
		t.Fatal(err)
	}

	distDir := filepath.Join(tmpDir, "dist", "nested")
	if err := os.MkdirAll(distDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "dist", "index.js"), []byte("exports.x = 1"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "dist", "index.d.ts"), []byte("export const x: number"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(distDir, "util.js"), []byte("exports.y = 2"), 0644); err != nil {
		t.Fatal(err)
	}

	srcDir := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "index.ts"), []byte("export const x = 1"), 0644); err != nil {
		t.Fatal(err)
	}

	coverageDir := filepath.Join(tmpDir, "coverage")
	if err := os.MkdirAll(coverageDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(coverageDir, "report.html"), []byte("<html></html>"), 0644); err != nil {
		t.Fatal(err)
	}

	_, files, err := Pack(tmpDir)
	if err != nil {
		t.Fatalf("Pack() error: %v", err)
	}

	packed := make(map[string]bool)
	for _, f := range files {
		packed[f.RelPath] = true
	}

	for _, name := range []string{"package.json", "dist/index.js", "dist/index.d.ts", "dist/nested/util.js"} {
		if !packed[name] {
			t.Errorf("expected whitelisted file %q to be packed, packed set was %v", name, packed)
		}
	}
	for _, name := range []string{"src/index.ts", "coverage/report.html", "README.md", ".gitignore"} {
		if packed[name] {
			t.Errorf("expected file %q to be excluded, packed set was %v", name, packed)
		}
	}
}

// TestPackFilesFieldGlobReachesIntoIgnoredDirectory packs a glob "files" entry
// selecting into a .gitignored directory. No lexical reading of the pattern can
// tell in advance which directories a glob selects out of, so the walk has to
// descend into the ignored directory and ask isIncluded per file.
//
// lib/top.js is in dropFiles for "lib/**/*.js" on purpose. It is not packed
// even with no ignore file present: filepath.Match does not let "**" span a
// path separator, so the three-segment pattern never matches a two-segment
// path. That is a pre-existing isIncluded limitation, independent of the
// whitelist-versus-ignore precedence under test, and the case is here to pin it
// so it is not mistaken for a bug in that precedence.
//
// Regression test for #318.
func TestPackFilesFieldGlobReachesIntoIgnoredDirectory(t *testing.T) {
	tests := []struct {
		name      string
		filesJSON string
		wantFile  string
		dropFiles []string
	}{
		{"double star glob", `["lib/**/*.js"]`, "lib/sub/a.js", []string{"lib/sub/a.txt", "lib/top.js"}},
		{"single star directory glob", `["*/keep.js"]`, "lib/keep.js", []string{"lib/drop.js"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()

			pkgJSON := `{
				"name": "glob-reach",
				"version": "1.0.0",
				"files": ` + tt.filesJSON + `
			}`
			if err := os.WriteFile(filepath.Join(tmpDir, "package.json"), []byte(pkgJSON), 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(tmpDir, ".gitignore"), []byte("lib/\n"), 0644); err != nil {
				t.Fatal(err)
			}

			for _, rel := range append([]string{tt.wantFile}, tt.dropFiles...) {
				path := filepath.Join(tmpDir, filepath.FromSlash(rel))
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("x"), 0644); err != nil {
					t.Fatal(err)
				}
			}

			_, files, err := Pack(tmpDir)
			if err != nil {
				t.Fatalf("Pack() error: %v", err)
			}

			packed := make(map[string]bool)
			for _, f := range files {
				packed[f.RelPath] = true
			}

			if !packed[tt.wantFile] {
				t.Errorf("expected %q, selected by files %s, to be packed despite the .gitignore; packed set was %v", tt.wantFile, tt.filesJSON, packed)
			}
			for _, rel := range tt.dropFiles {
				if packed[rel] {
					t.Errorf("expected %q, which files %s does not select, to be excluded; packed set was %v", rel, tt.filesJSON, packed)
				}
			}
		})
	}
}

// TestPackFilesFieldReachesIntoIgnoredDirectory covers the case where the
// "files" field names a path *inside* an ignored directory rather than the
// directory itself. The walk must not prune "lib", or it never reaches the file
// the whitelist asked for. Everything else under lib stays ignored.
//
// Regression test for #318.
func TestPackFilesFieldReachesIntoIgnoredDirectory(t *testing.T) {
	tmpDir := t.TempDir()

	pkgJSON := `{
		"name": "reach-into",
		"version": "1.0.0",
		"files": ["lib/keep.js"]
	}`
	if err := os.WriteFile(filepath.Join(tmpDir, "package.json"), []byte(pkgJSON), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, ".gitignore"), []byte("lib/\n"), 0644); err != nil {
		t.Fatal(err)
	}

	libDir := filepath.Join(tmpDir, "lib")
	if err := os.MkdirAll(libDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(libDir, "keep.js"), []byte("keep"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(libDir, "drop.js"), []byte("drop"), 0644); err != nil {
		t.Fatal(err)
	}

	_, files, err := Pack(tmpDir)
	if err != nil {
		t.Fatalf("Pack() error: %v", err)
	}

	packed := make(map[string]bool)
	for _, f := range files {
		packed[f.RelPath] = true
	}

	if !packed["lib/keep.js"] {
		t.Errorf("expected whitelisted file %q to be packed, packed set was %v", "lib/keep.js", packed)
	}
	if packed["lib/drop.js"] {
		t.Errorf("expected file %q to be excluded, packed set was %v", "lib/drop.js", packed)
	}
}

func TestHashFile(t *testing.T) {
	tmpDir := t.TempDir()

	// Create test files
	file1 := filepath.Join(tmpDir, "file1.txt")
	file2 := filepath.Join(tmpDir, "file2.txt")
	file3 := filepath.Join(tmpDir, "file3.txt")

	if err := os.WriteFile(file1, []byte("hello world"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file2, []byte("hello world"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file3, []byte("different content"), 0644); err != nil {
		t.Fatal(err)
	}

	hash1, err := HashFile(file1)
	if err != nil {
		t.Fatalf("HashFile(file1) error: %v", err)
	}
	hash2, err := HashFile(file2)
	if err != nil {
		t.Fatalf("HashFile(file2) error: %v", err)
	}
	hash3, err := HashFile(file3)
	if err != nil {
		t.Fatalf("HashFile(file3) error: %v", err)
	}

	// Same content should have same hash
	if hash1 != hash2 {
		t.Errorf("hash1 (%s) != hash2 (%s) for same content", hash1, hash2)
	}

	// Different content should have different hash
	if hash1 == hash3 {
		t.Errorf("hash1 (%s) == hash3 (%s) for different content", hash1, hash3)
	}

	// Hash should be 16 hex characters
	if len(hash1) != 16 {
		t.Errorf("hash length = %d, want 16", len(hash1))
	}
}

func TestHashFiles(t *testing.T) {
	files1 := []*FileInfo{
		{RelPath: "a.js", ContentHash: "abc123"},
		{RelPath: "b.js", ContentHash: "def456"},
	}
	files2 := []*FileInfo{
		{RelPath: "a.js", ContentHash: "abc123"},
		{RelPath: "b.js", ContentHash: "def456"},
	}
	files3 := []*FileInfo{
		{RelPath: "a.js", ContentHash: "abc123"},
		{RelPath: "c.js", ContentHash: "ghi789"},
	}

	hash1 := HashFiles(files1)
	hash2 := HashFiles(files2)
	hash3 := HashFiles(files3)

	// Same files should have same combined hash
	if hash1 != hash2 {
		t.Errorf("HashFiles returned different hashes for same files")
	}

	// Different files should have different combined hash
	if hash1 == hash3 {
		t.Errorf("HashFiles returned same hash for different files")
	}
}

// TestDefaultExcludesStillExclude pins what every built-in exclusion entry keeps
// out on its own, one entry at a time, over both hardReservedExcludes and
// defaultExcludes. Together they are what stops a publish shipping .env, .git or
// node_modules unasked, and both run through the same matcher as the user's
// ignore patterns, so a change to that matcher can widen a publish without any
// test of user-facing behavior noticing.
//
// It asks the matcher one pattern at a time, so it says nothing about which list
// a pattern is in or what outranks it. Precedence is pinned by the Pack-level
// tests — TestPackFilesEntryOverridesDefaultExcludes and
// TestPackHardReservedWinsInWhitelistMode — not here.
//
// What this does and does not establish. The paths below are hand-authored, not
// derived from the matcher's behavior before any particular change, and none of
// them reaches the glob engine by a route that engine change would alter: the
// "/**" entries return from the trailing-"/**" branch before globbing, and no
// entry uses "**" elsewhere, "{", "[" or "\". So this test would have passed
// identically before and after the move from filepath.Match to doublestar and
// could not have caught that going wrong — TestIsExcludedDoublestarSpansAnyDepth
// and TestIsExcludedBraceAlternation are the tests that did that job.
//
// Its value is forward-looking: it is a pin against drift, and specifically
// against a guard growing a hole. The completeness check fails the build when an
// entry in either list is added with no case here, or with an empty one.
func TestDefaultExcludesStillExclude(t *testing.T) {
	// Each case names a built-in exclusion pattern and paths it must exclude on
	// its own.
	tests := []struct {
		pattern string
		paths   []string
	}{
		{".git", []string{".git", ".git/config"}},
		{".git/**", []string{".git", ".git/objects/ab/cdef"}},
		{".gitignore", []string{".gitignore", "src/.gitignore"}},
		{".gitattributes", []string{".gitattributes", "src/.gitattributes"}},
		{".hg", []string{".hg", ".hg/store"}},
		{".hg/**", []string{".hg", ".hg/store/data"}},
		{".svn", []string{".svn", ".svn/entries"}},
		{".svn/**", []string{".svn", ".svn/pristine/ab"}},
		{"CVS", []string{"CVS", "CVS/Root"}},
		{"CVS/**", []string{"CVS", "CVS/Root"}},
		{".DS_Store", []string{".DS_Store", "src/.DS_Store"}},
		{"Thumbs.db", []string{"Thumbs.db", "src/Thumbs.db"}},
		{"node_modules", []string{"node_modules", "node_modules/dep/index.js"}},
		{"node_modules/**", []string{"node_modules", "node_modules/dep/index.js"}},
		{".npmrc", []string{".npmrc", "src/.npmrc"}},
		{".npmignore", []string{".npmignore", "src/.npmignore"}},
		{".yalc", []string{".yalc", ".yalc/pkg/index.js"}},
		{".yalc/**", []string{".yalc", ".yalc/pkg/index.js"}},
		{".lnpm", []string{".lnpm", ".lnpm/pkg/index.js"}},
		{".lnpm/**", []string{".lnpm", ".lnpm/pkg/index.js"}},
		{"lnpm.lock", []string{"lnpm.lock", "src/lnpm.lock"}},
		{lockfile.RetreatFileName, []string{"lnpm.lock.retreat", "src/lnpm.lock.retreat"}},
		{"yalc.lock", []string{"yalc.lock", "src/yalc.lock"}},
		{"*.log", []string{"debug.log", "src/deep/debug.log"}},
		{"*.orig", []string{"index.js.orig", "src/index.js.orig"}},
		{"*.swp", []string{".index.js.swp", "src/.index.js.swp"}},
		{"*.swo", []string{".index.js.swo", "src/.index.js.swo"}},
		{"*~", []string{"index.js~", "src/index.js~"}},
		{".env", []string{".env", "src/.env"}},
		{".env.*", []string{".env.local", "src/.env.production"}},
		{"*.tgz", []string{"pkg-1.0.0.tgz", "src/pkg.tgz"}},
	}

	covered := make(map[string]int, len(tests))
	for _, tt := range tests {
		covered[tt.pattern] = len(tt.paths)
	}
	// Both halves of the split, so an entry cannot escape coverage by moving
	// from one list to the other.
	for _, pattern := range append(append([]string{}, hardReservedExcludes...), defaultExcludes...) {
		switch n, ok := covered[pattern]; {
		case !ok:
			t.Errorf("built-in exclusion entry %q has no case here: every one needs one, or the guard can grow a hole unnoticed", pattern)
		case n == 0:
			t.Errorf("built-in exclusion entry %q is listed with no paths, so it asserts nothing", pattern)
		}
	}

	for _, tt := range tests {
		for _, path := range tt.paths {
			t.Run(tt.pattern+" excludes "+path, func(t *testing.T) {
				if !isExcluded(path, []string{tt.pattern}) {
					t.Errorf("isExcluded(%q, [%q]) = false, want true", path, tt.pattern)
				}
			})
		}
	}
}

// TestDefaultExcludesAreLiteralNotPrefixes pins the other half of the guard: a
// default exclude must not swallow a neighbouring name. README's File Filtering
// section documents that ".envrc" matches neither ".env" nor ".env.*" and is
// published, and pack.go's own comment relies on "lnpm.lock" not covering
// "lnpm.lock.retreat" — which is why the retreat snapshot is listed separately.
func TestDefaultExcludesAreLiteralNotPrefixes(t *testing.T) {
	tests := []struct {
		path    string
		pattern string
	}{
		{".envrc", ".env"},
		{".envrc", ".env.*"},
		{"src/.envrc", ".env"},
		{"src/.envrc", ".env.*"},
		{"env", ".env"},
		{"lnpm.lock.retreat", "lnpm.lock"},
		{"node_modules_backup/dep.js", "node_modules"},
		{"logger.js", "*.log"},
		{".gitignore.bak", ".gitignore"},
	}

	for _, tt := range tests {
		t.Run(tt.path+" vs "+tt.pattern, func(t *testing.T) {
			if isExcluded(tt.path, []string{tt.pattern}) {
				t.Errorf("isExcluded(%q, [%q]) = true, want false", tt.path, tt.pattern)
			}
		})
	}

	// .envrc survives both lists in full, not just the two .env entries.
	for name, list := range map[string][]string{
		"hardReservedExcludes": hardReservedExcludes,
		"defaultExcludes":      defaultExcludes,
	} {
		if isExcluded(".envrc", list) {
			t.Errorf("isExcluded(\".envrc\", %s) = true, want false: README documents .envrc as published", name)
		}
	}
}

// TestIsExcludedDoublestarSpansAnyDepth pins "**" to the meaning git and npm
// give it: zero or more path segments. The three "**/*.pem" rows are issue #316
// — matching with filepath.Match, whose "*" never crosses a separator, excluded
// only the two-segment middle row, so a key at the package root and a key three
// deep were both published by the standard "exclude keys everywhere" idiom.
func TestIsExcludedDoublestarSpansAnyDepth(t *testing.T) {
	tests := []struct {
		path     string
		patterns []string
		want     bool
	}{
		// The three depths from the issue. "**/" must be able to stand for no
		// directory at all, which is what excludes the root .pem.
		{"root.pem", []string{"**/*.pem"}, true},
		{"keys/deploy.pem", []string{"**/*.pem"}, true},
		{"src/keys/deploy.pem", []string{"**/*.pem"}, true},
		{"src/keys/deploy.txt", []string{"**/*.pem"}, false},

		// A "**" in the middle spans any depth under its prefix, including
		// none, and never escapes that prefix.
		{"src/prod.key", []string{"src/**/*.key"}, true},
		{"src/deep/nest/prod.key", []string{"src/**/*.key"}, true},
		{"other/prod.key", []string{"src/**/*.key"}, false},

		// A single "*" still does not cross a separator.
		{"src/deploy.pem", []string{"src/*.pem"}, true},
		{"src/keys/deploy.pem", []string{"src/*.pem"}, false},
		{"a/b/c.js", []string{"a/*/c.js"}, true},
		{"a/b/x/c.js", []string{"a/*/c.js"}, false},

		// A trailing "/**" keeps its current meaning: the directory itself and
		// everything under it, and nothing that merely shares the prefix.
		{"dist", []string{"dist/**"}, true},
		{"dist/cli/index.js", []string{"dist/**"}, true},
		{"distant/index.js", []string{"dist/**"}, false},

		// Anchoring survives: a leading "/" pins the pattern to the package
		// root, so "**" beneath it cannot climb back out.
		{"keys/deploy.pem", []string{"/**/*.pem"}, true},
		{"src/keys/deploy.pem", []string{"/src/**/*.pem"}, true},
		{"lib/src/keys/deploy.pem", []string{"/src/**/*.pem"}, false},

		// Negation survives, including its narrowing rule: a "!" pattern
		// re-includes the paths it names directly, never the paths merely
		// underneath a directory it named.
		{"keys/deploy.pem", []string{"**/*.pem", "!keys/deploy.pem"}, false},
		{"keys/other.pem", []string{"**/*.pem", "!keys/deploy.pem"}, true},
		{"src/keys/deploy.pem", []string{"src/**", "!src"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.path+" vs "+tt.patterns[0], func(t *testing.T) {
			got := isExcluded(tt.path, tt.patterns)
			if got != tt.want {
				t.Errorf("isExcluded(%q, %v) = %v, want %v", tt.path, tt.patterns, got, tt.want)
			}
		})
	}
}

// TestPackDoublestarIgnoreExcludesAtEveryDepth is issue #316 end to end: a
// .gitignore holding the "exclude keys everywhere" idiom, against real .pem
// files at three depths. It also proves the pattern does not prune the walk —
// collectFiles asks isExcluded about directories too, and "**/*.pem" must not
// match the "keys" directory and take safe.js down with it.
func TestPackDoublestarIgnoreExcludesAtEveryDepth(t *testing.T) {
	tmpDir := t.TempDir()

	pkgJSON := `{
		"name": "doublestar-ignore",
		"version": "1.0.0"
	}`
	if err := os.WriteFile(filepath.Join(tmpDir, "package.json"), []byte(pkgJSON), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, ".gitignore"), []byte("**/*.pem\nsrc/**/*.key\n"), 0644); err != nil {
		t.Fatal(err)
	}

	files := map[string]string{
		"root.pem":               "key",
		"keys/deploy.pem":        "key",
		"src/keys/deploy.pem":    "key",
		"src/deep/nest/prod.key": "key",
		"other/prod.key":         "not covered by src/**/*.key",
		"keys/safe.js":           "ok",
		"src/deep/nest/index.js": "ok",
		"index.js":               "ok",
	}
	for rel, content := range files {
		path := filepath.Join(tmpDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	_, packedFiles, err := Pack(tmpDir)
	if err != nil {
		t.Fatalf("Pack() error: %v", err)
	}

	packed := make(map[string]bool)
	for _, f := range packedFiles {
		packed[f.RelPath] = true
	}

	for _, name := range []string{"root.pem", "keys/deploy.pem", "src/keys/deploy.pem", "src/deep/nest/prod.key"} {
		if packed[name] {
			t.Errorf("%q was packed: a key matched by the ignore file must never ship, packed set was %v", name, packed)
		}
	}
	for _, name := range []string{"package.json", "index.js", "keys/safe.js", "src/deep/nest/index.js", "other/prod.key"} {
		if !packed[name] {
			t.Errorf("expected %q to be packed, packed set was %v", name, packed)
		}
	}
}

// TestIsExcludedBraceAlternation pins a deliberate consequence of globbing
// ignore patterns with doublestar rather than filepath.Match, not an accident:
// doublestar expands brace alternation and filepath.Match does not. It cuts
// both ways, so both directions are pinned here.
//
// The gain moves toward npm, whose ignore handling goes through minimatch and
// does expand braces. The loss moves away from git, which has no brace syntax
// and would read the pattern literally. lnpm's filtering is modeled on npm's
// conventions, so the trade is accepted — but it must not be silent, which is
// what this test and the README bullet are for.
func TestIsExcludedBraceAlternation(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		pattern string
		want    bool
	}{
		// Gained: a brace pattern now globs. filepath.Match matched none of
		// these, because it read "{pem,key}" as eight literal characters.
		{"alternation matches first branch", "a.pem", "*.{pem,key}", true},
		{"alternation matches second branch", "a.key", "*.{pem,key}", true},
		{"alternation matches nested", "src/deep/a.key", "*.{pem,key}", true},
		{"alternation does not match other extensions", "a.txt", "*.{pem,key}", false},

		// Lost: a filename containing literal braces is no longer matched by a
		// pattern spelling those braces, because the pattern now expands to
		// "weirda.txt" and "weirdb.txt" instead.
		{"literal braces no longer match by basename", "src/weird{a,b}.txt", "weird{a,b}.txt", false},
		{"expanded branch matches instead", "src/weirda.txt", "weird{a,b}.txt", true},

		// Not lost: globbing is not the only route through the matcher. Every
		// branch that compares strings still reads the braces literally, so a
		// pattern naming the full path, and every directory form, still
		// excludes a literal-brace name. Only the two globbing routes —
		// basename, and a full path carrying metacharacters — lost it.
		{"literal braces still match a full-path pattern", "weird{a,b}.txt", "weird{a,b}.txt", true},
		{"literal braces still match an anchored full-path pattern", "src/weird{a,b}.txt", "/src/weird{a,b}.txt", true},
		{"literal-brace directory still matches by name", "weird{a,b}/file.js", "weird{a,b}", true},
		{"literal-brace directory still matches trailing slash", "weird{a,b}/file.js", "weird{a,b}/", true},
		{"literal-brace directory still matches trailing doublestar", "weird{a,b}/deep/file.js", "weird{a,b}/**", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isExcluded(tt.path, []string{tt.pattern})
			if got != tt.want {
				t.Errorf("isExcluded(%q, [%q]) = %v, want %v", tt.path, tt.pattern, got, tt.want)
			}
		})
	}
}

// TestIsExcludedSingleStarNeverCrossesSeparatorOnAnyPlatform pins a platform
// inconsistency the move to doublestar removed.
//
// collectFiles hands isExcluded a relPath that has already been through
// filepath.ToSlash, so separators are always "/". filepath.Match, though, reads
// its separator from the platform: "\" on Windows. A "/" in the path was
// therefore an ordinary character there, and "*" — which matches any run of
// non-separator characters — crossed it freely. So "src/*.pem" excluded
// "src/a/b.pem" on Windows and not on Linux or macOS, from identical inputs.
//
// doublestar's separator is always "/", so this now holds on every platform.
// The Windows CI job is where this test earns its keep — on Linux and macOS it
// was already true, and TestIsExcludedDoublestarSpansAnyDepth covers the rest of
// single-"*" containment. Only the one row that used to differ by platform, and
// the positive control that stops it passing vacuously, live here.
func TestIsExcludedSingleStarNeverCrossesSeparatorOnAnyPlatform(t *testing.T) {
	tests := []struct {
		path    string
		pattern string
		want    bool
	}{
		{"src/a.pem", "src/*.pem", true},
		{"src/a/b.pem", "src/*.pem", false},
	}

	for _, tt := range tests {
		t.Run(tt.path+" vs "+tt.pattern, func(t *testing.T) {
			got := isExcluded(tt.path, []string{tt.pattern})
			if got != tt.want {
				t.Errorf("isExcluded(%q, [%q]) = %v, want %v", tt.path, tt.pattern, got, tt.want)
			}
		})
	}
}

// TestDefaultExcludesMatchCaseInsensitively is issue #317. The force-exclude set
// is a security guard, and it was reading the developer's shift key: ".ENV",
// ".Env.local" and ".NPMRC" all shipped while ".env" and ".npmrc" did not. On
// macOS and Windows those name the same file as the lowercase form, so the guard
// was simply absent for anyone who typed the name differently.
//
// Only the built-in set folds case. TestUserIgnorePatternsStayCaseSensitive pins
// the other side of that line.
//
// #321 split that set in two, and the fold has to survive on both halves —
// splitting a guard is a routine way to drop one of its properties. So each row
// asks the function that owns its path rather than one combined predicate: a row
// asking isDefaultExcluded about ".NPMRC" would go green on a bug that folded
// neither, if the combined answer were taken instead. The `hard` column says
// which list the path belongs to; the completeness check in
// TestDefaultExcludesStillExclude is what stops an entry existing in neither.
func TestDefaultExcludesMatchCaseInsensitively(t *testing.T) {
	// isDefaultExcluded and isHardReserved have identical signatures, so a row
	// naming the wrong one still compiles. The `hard` flag is placed beside the
	// path for that reason: it reads as data rather than as a function value
	// someone can copy-paste past.
	excludedBy := func(hard bool, path string) bool {
		if hard {
			return isHardReserved(path)
		}
		return isDefaultExcluded(path)
	}

	tests := []struct {
		path string
		hard bool
		want bool
	}{
		// The names from the issue. Each is the same file as its lowercase form
		// on a case-insensitive filesystem. #321 put .env on the overridable
		// side of the split and .npmrc and node_modules on the hard-reserved
		// side, so the issue's own names now span both halves — which is why
		// checking the fold on both halves is not ceremony.
		{".ENV", false, true},
		{".Env", false, true},
		{".Env.local", false, true},
		{".NPMRC", true, true},
		{"Node_Modules/evil/index.js", true, true},
		{"Node_Modules", true, true},

		// The lowercase forms keep working. Folding case must widen the guard,
		// never move it.
		{".env", false, true},
		{".env.local", false, true},
		{".npmrc", true, true},
		{"node_modules/dep/index.js", true, true},

		// Entries that carry uppercase in the list itself fold the same way, in
		// both directions.
		{"src/.ds_store", true, true},
		{"src/.DS_Store", true, true},
		{"thumbs.db", false, true},
		{"cvs/Root", true, true},
		{".GIT/config", true, true},
		{"DEBUG.LOG", false, true},
		{"pkg-1.0.0.TGZ", false, true},

		// Folding case must not turn a built-in exclude into a prefix. README
		// documents .envrc as published, and that has to hold however it is
		// typed. The `hard` column is arbitrary on these rows: a path no list
		// covers must be reported false by both, and the loop below asks both
		// on every row.
		{".envrc", false, false},
		{".ENVRC", false, false},
		{"src/.EnvRc", false, false},
		{"ENV", false, false},
		{"Node_Modules_Backup/dep.js", false, false},
		{"LOGGER.JS", false, false},
		{"index.js", false, false},
	}

	setName := func(hard bool) string {
		if hard {
			return "isHardReserved"
		}
		return "isDefaultExcluded"
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := excludedBy(tt.hard, tt.path); got != tt.want {
				t.Errorf("%s(%q) = %v, want %v", setName(tt.hard), tt.path, got, tt.want)
			}

			// The other half must always say false: no path here is covered by
			// both lists. Without this, a row could pass on a split that left an
			// entry duplicated across the two, which would make the overridable
			// half unoverridable again — the exact bug #321 fixes, reintroduced
			// where no packing test would look.
			if excludedBy(!tt.hard, tt.path) {
				t.Errorf("%s(%q) = true as well: the two built-in lists must not both cover a path, or the overridable half stops being overridable", setName(!tt.hard), tt.path)
			}
		})
	}
}

// TestDefaultExcludesHoldNoMetacharactersLoweringWouldRewrite pins the
// invariant lowerDefaultExcludes rests on. Lowering a pattern is only a change
// of case while the pattern holds no character class, brace or escape:
// strings.ToLower rewrites "[A-Z]" into "[a-z]" and "\A" into "\a", which change
// what the pattern matches rather than how it is cased.
//
// The invariant is already recorded in
// docs/adr/0003-ignore-patterns-glob-with-doublestar-syntax-and-all.md ("No
// entry in either list ... contains a brace or a character class"), where it is
// what kept the move to doublestar away from the built-in lists. That sentence
// read "No defaultExcludes entry" until #321 split the list and reworded it;
// re-read the ADR before quoting it again. Nothing enforced the invariant, so
// this test does.
//
// It is deliberately narrow. Each fold is derived from its list rather than
// written out beside it, so a new entry is folded automatically and needs no
// case in TestDefaultExcludesMatchCaseInsensitively; a metacharacter is the one
// way a new entry can break the fold silently.
func TestDefaultExcludesHoldNoMetacharactersLoweringWouldRewrite(t *testing.T) {
	// Both halves: lowerHardReservedExcludes and lowerDefaultExcludes are
	// derived the same way, so the invariant is the same one twice.
	for _, pattern := range append(append([]string{}, hardReservedExcludes...), defaultExcludes...) {
		if i := strings.IndexAny(pattern, `[]{}\`); i >= 0 {
			t.Errorf("built-in exclusion entry %q contains %q: lowering it for the case-insensitive match would rewrite the pattern, not just its case — a class becomes [a-z] and an escape becomes \\a. See docs/adr/0003-ignore-patterns-glob-with-doublestar-syntax-and-all.md, which records that no entry carries one",
				pattern, pattern[i])
		}
	}
}

// TestPackMixedCaseSecretsNeverShip is issue #317 end to end, against real files
// on disk. The unit table above asks the matcher; this asks what a publish would
// actually carry, which is the claim that matters.
func TestPackMixedCaseSecretsNeverShip(t *testing.T) {
	tmpDir := t.TempDir()

	pkgJSON := `{
		"name": "mixed-case-secrets",
		"version": "1.0.0"
	}`
	if err := os.WriteFile(filepath.Join(tmpDir, "package.json"), []byte(pkgJSON), 0644); err != nil {
		t.Fatal(err)
	}

	files := map[string]string{
		".ENV":                       "SECRET=1",
		".Env.local":                 "SECRET=2",
		".NPMRC":                     "//registry:_authToken=deadbeef",
		"Node_Modules/evil/index.js": "steal()",
		"index.js":                   "ok",
		".envrc":                     "use flake",
	}
	for rel, content := range files {
		path := filepath.Join(tmpDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	_, packedFiles, err := Pack(tmpDir)
	if err != nil {
		t.Fatalf("Pack() error: %v", err)
	}

	packed := make(map[string]bool)
	for _, f := range packedFiles {
		packed[f.RelPath] = true
	}

	for _, name := range []string{".ENV", ".Env.local", ".NPMRC", "Node_Modules/evil/index.js"} {
		if packed[name] {
			t.Errorf("%q was packed: a default exclude must hold however the name is cased, packed set was %v", name, packed)
		}
	}
	for _, name := range []string{"package.json", "index.js", ".envrc"} {
		if !packed[name] {
			t.Errorf("expected %q to be packed, packed set was %v", name, packed)
		}
	}
}

// TestUserIgnorePatternsStayCaseSensitive pins the scope of issue #317: the
// built-in force-exclude set folds case, a pattern the user wrote in .npmignore
// or .gitignore does not.
//
// The two are different kinds of rule, and they fail in opposite directions. A
// built-in exclusion is a guard lnpm applies on the user's behalf, and a guard
// that can be stepped around by holding shift is not a guard; folding it only
// ever withholds more. That holds for both halves of the list #321 split, which
// is why both fold. A user's ignore pattern is a preference, and folding it would
// drop files the author never asked to drop. See isDefaultExcluded for the rest
// of the reasoning, including why matching git is not one of the reasons — git
// folds ignore matching itself when core.ignorecase is set, as it is by default
// on macOS and Windows.
//
// This test is what fails if the fold is applied to the shared matcher
// (applyIgnorePatterns or matchesIgnorePattern) rather than to the built-in set
// alone.
func TestUserIgnorePatternsStayCaseSensitive(t *testing.T) {
	if isExcluded("SECRET.TXT", []string{"secret.txt"}) {
		t.Error(`isExcluded("SECRET.TXT", ["secret.txt"]) = true, want false: only the built-in exclusion list folds case, a pattern the user wrote does not`)
	}
	if isExcluded("Dist/app.js", []string{"dist/"}) {
		t.Error(`isExcluded("Dist/app.js", ["dist/"]) = true, want false: a user directory pattern matches case-sensitively too`)
	}

	// The two names go in two packages, never one directory, and that is not
	// tidiness — it is the same filesystem property this whole issue is about,
	// turned on the fixture. On macOS and Windows "secret.txt" and "SECRET.TXT"
	// are one file, so writing both into one directory leaves a single entry
	// under the name written first, and an assertion that "SECRET.TXT" ships
	// cannot hold there whatever the product does. The bug was dangerous for
	// exactly the reason the fixture is impossible. Both filesystems are
	// case-preserving, though: a package holding only "SECRET.TXT" reports that
	// name to the walk, so with the user's pattern spelled "secret.txt" and no
	// folding, it ships on all three platforms.
	packOne := func(name string, files map[string]string) map[string]bool {
		t.Helper()

		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"`+name+`","version":"1.0.0"}`), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".npmignore"), []byte("secret.txt\n"), 0644); err != nil {
			t.Fatal(err)
		}
		for rel, content := range files {
			if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0644); err != nil {
				t.Fatal(err)
			}
		}

		_, packedFiles, err := Pack(dir)
		if err != nil {
			t.Fatalf("Pack(%s) error: %v", name, err)
		}

		packed := make(map[string]bool)
		for _, f := range packedFiles {
			packed[f.RelPath] = true
		}
		return packed
	}

	lower := packOne("ignores-lowercase-name", map[string]string{
		"secret.txt": "dropped",
		"index.js":   "ok",
	})
	if lower["secret.txt"] {
		t.Errorf("%q was packed but .npmignore names it, packed set was %v", "secret.txt", lower)
	}
	if !lower["index.js"] {
		t.Errorf("expected %q to be packed, packed set was %v", "index.js", lower)
	}

	upper := packOne("ignores-uppercase-name", map[string]string{
		"SECRET.TXT": "kept",
		"index.js":   "ok",
	})
	if !upper["SECRET.TXT"] {
		t.Errorf("%q was not packed: a user ignore pattern must not fold case, packed set was %v", "SECRET.TXT", upper)
	}
}

// TestPackDefaultIncludesAnchoredToRoot packs the audit fixture from #320 and
// asserts the exact packed set, not just the absence of the paths the issue
// names. The whitelist is "files": ["dist"], and the fixture salts the tree with
// files whose basenames sit in defaultIncludes. Every one of them shipped before
// the fix, because isDefaultInclude matched filepath.Base at any depth, so a
// whitelist the developer wrote as ["dist"] did not narrow the package the way
// it looks like it does.
//
// Two entries in the expected set need saying out loud:
//
//   - "dist/README.md" ships, and is meant to. It arrives through the whitelist
//     — isIncluded("dist/README.md", ["dist"]) is true — not through
//     defaultIncludes. It is in the fixture so that a fix which anchored by
//     rejecting every path with a separator *after* the whitelist had already
//     spoken would be caught here.
//   - "history.db" at the root ships. #320 lists it among the four paths a
//     ["dist"] whitelist wrongly shipped, but at the root it is not an instance
//     of the depth bug: it matches the "history*" entry in defaultIncludes as a
//     root path, and root paths are exactly what the always-included set is for.
//     Whether "HISTORY*" belongs in that set at all — npm's does not contain it,
//     as README's divergence list records — is out of scope for #320, which the
//     brief states in those words. "data/history.db" below is the same name at
//     depth and is dropped, which is the part #320 is about.
func TestPackDefaultIncludesAnchoredToRoot(t *testing.T) {
	tmpDir := t.TempDir()

	write := func(rel, content string) {
		t.Helper()
		full := filepath.Join(tmpDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	write("package.json", `{
		"name": "anchored-includes",
		"version": "1.0.0",
		"main": "dist/index.js",
		"files": ["dist"]
	}`)

	// Root: the always-included set, which must keep working.
	write("README.md", "# anchored-includes")
	write("LICENSE", "MIT")
	write("history.db", "root history")

	// Whitelisted.
	write("dist/index.js", "module.exports = {}")
	write("dist/README.md", "# built")

	// The four paths #320 names, plus the same names at depth.
	write("internal-docs/README.private", "internal only")
	write("notes/changes.txt", "internal changelog")
	write("vendor/foo/LICENSE", "vendored MIT")
	write("data/history.db", "nested history")

	// Outside the whitelist and not an always-included name, as a control.
	write("src/index.ts", "export const x = 1")

	_, files, err := Pack(tmpDir)
	if err != nil {
		t.Fatalf("Pack() error: %v", err)
	}

	got := make([]string, 0, len(files))
	for _, f := range files {
		got = append(got, f.RelPath)
	}
	sort.Strings(got)

	want := []string{
		"LICENSE",
		"README.md",
		"dist/README.md",
		"dist/index.js",
		"history.db",
		"package.json",
	}

	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("packed set mismatch: the always-included set must match at the "+
			"package root only, so a \"files\" whitelist of [\"dist\"] ships nothing "+
			"below the root that it did not name\n got: %v\nwant: %v", got, want)
	}
}

// TestExcludedByProjectRulesAnchorsDefaultIncludesToRoot records what anchoring
// isDefaultInclude did to the other call site.
//
// ExcludedByProjectRules answers "would a tool that reads only this project's
// own rules ship relPath?", which is what `npm publish` is. npm's always-
// included set is root-only, so anchoring makes this function *more* faithful,
// not less: before the fix it reported a nested docs/README.md as shipped under
// a ["dist"] whitelist, which npm would not do.
//
// For the only caller today the answer is unchanged. internal/cli/check.go:256
// passes lockfile.RetreatFileName, a root-level name, and it matches nothing in
// defaultIncludes at any depth — so isDefaultInclude answered false for it
// before the fix and answers false after. The last case below pins that, so the
// claim is checked rather than asserted.
//
// The nested cases are deliberately outside ExcludedByProjectRules' documented
// contract. Its doc comment says it reads the root ignore file only, so a caller
// passing a nested path gets an answer that ignores the ignore file next to it.
// The assertions still hold because this fixture has no ignore file anywhere —
// only a package.json — so there is no per-directory rule for the root-only read
// to miss, and the "files" field is the sole thing deciding. That is what makes
// it a clean probe of the change under test: it isolates isDefaultInclude's
// anchoring from the ignore-file gap. A fixture with a docs/.npmignore in it
// would be testing the documented gap instead, and #320 does not close that gap.
func TestExcludedByProjectRulesAnchorsDefaultIncludesToRoot(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "package.json"), []byte(`{"name":"x","version":"1.0.0"}`), 0644); err != nil {
		t.Fatal(err)
	}

	filesField := []string{"dist"}

	tests := []struct {
		relPath string
		want    bool
		why     string
	}{
		{"README.md", false, "a root README is always included, as it is for npm"},
		{"LICENSE", false, "a root LICENSE is always included, as it is for npm"},
		{"docs/README.md", true, "npm does not force-include a README below the root"},
		{"vendor/foo/LICENSE", true, "npm does not force-include a LICENSE below the root"},
		{"dist/index.js", false, "the whitelist selects it"},
		{"src/index.ts", true, "outside the whitelist and not an always-included name"},
		{lockfile.RetreatFileName, true, "the only caller's path: unchanged by #320"},
	}

	for _, tt := range tests {
		t.Run(tt.relPath, func(t *testing.T) {
			got := ExcludedByProjectRules(tmpDir, filesField, tt.relPath)
			if got != tt.want {
				t.Errorf("ExcludedByProjectRules(dir, %v, %q) = %v, want %v (%s)",
					filesField, tt.relPath, got, tt.want, tt.why)
			}
		})
	}
}

// writeMainEntryTree writes rel -> content under root, delegating to
// writeTestFile so a failure names the path it could not write. Keys are
// slash-separated so the fixtures read the same on every platform.
func writeMainEntryTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		writeTestFile(t, filepath.Join(root, filepath.FromSlash(rel)), content)
	}
}

// packedRelPaths packs root and returns the packed set sorted, so a test can
// assert the exact set rather than "contains".
func packedRelPaths(t *testing.T, root string) []string {
	t.Helper()
	_, files, err := Pack(root)
	if err != nil {
		t.Fatalf("Pack() error: %v", err)
	}
	got := make([]string, 0, len(files))
	for _, f := range files {
		got = append(got, f.RelPath)
	}
	sort.Strings(got)
	return got
}

func assertPackedSet(t *testing.T, root string, want []string, why string) {
	t.Helper()
	got := packedRelPaths(t, root)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("packed set mismatch: %s\n got: %v\nwant: %v", why, got, want)
	}
}

// TestMainEntryPath pins the normalization directly, which the Pack-level tests
// cannot do for every row: two spellings that both select nothing are
// indistinguishable through Pack, and the platform-dependent rows have no
// fixture that could assert them on one OS.
//
// The backslash and drive-absolute rows are the reason this test exists rather
// than being folded into the Pack fixtures. Their expectations are chosen by
// runtime.GOOS because the same string genuinely means different things per
// platform: "lib\\index.js" is one filename on Linux and a two-segment path on
// Windows, and "C:/x" is a relative path under a directory named "C:" on Linux
// and an absolute path on Windows. Asserting one answer on both would be wrong
// on one of them. Before these rows existed no fixture spelled main with a
// backslash at all, so no CI job checked either half.
func TestMainEntryPath(t *testing.T) {
	// On Windows filepath.ToSlash folds "\" and filepath.IsAbs accepts a drive
	// letter; on Linux and macOS neither happens.
	backslash := `lib\index.js`
	driveAbsolute := "C:/x"
	if runtime.GOOS == "windows" {
		backslash = "lib/index.js"
		driveAbsolute = ""
	}

	tests := []struct {
		main string
		want string
		why  string
	}{
		{"", "", "no main declared selects nothing"},
		{"lib/index.js", "lib/index.js", "the plain form is already normalized"},
		{"./lib/index.js", "lib/index.js", "npm accepts a leading ./ and Node resolves it the same"},
		{"lib/../lib/index.js", "lib/index.js", "Clean collapses an interior .."},
		{"./index.js", "index.js", "a root entry point keeps working"},
		{"lib", "lib", "a directory is returned and matches nothing, never a prefix"},
		{"lib/", "lib", "Clean drops a trailing slash"},
		{".", "", "the package root is not an entry point"},
		{"..", "", "the parent is outside the package"},
		{"../evil.js", "", "an escaping path selects nothing"},
		{"lib/../../evil.js", "", "an escape that only appears after Clean is still rejected"},
		{"/etc/passwd", "", "a rooted path is outside the package"},
		{`lib\index.js`, backslash, "backslashes fold on Windows and are a literal filename elsewhere"},
		{"C:/x", driveAbsolute, "drive-absolute on Windows, an ordinary relative path elsewhere"},
	}

	for _, tt := range tests {
		t.Run(tt.main, func(t *testing.T) {
			if got := mainEntryPath(tt.main); got != tt.want {
				t.Errorf("mainEntryPath(%q) = %q, want %q (%s)", tt.main, got, tt.want, tt.why)
			}
		})
	}
}

// TestPackForceIncludesMainUnderFilesWhitelist is #319's fixture, asserted as an
// exact set: main "lib/index.js" with files ["dist"] shipped [dist/a.js,
// package.json], so requiring the published package failed at runtime on the one
// path the manifest names.
//
// The "./" spelling is the same file. npm accepts both, and Node resolves them
// identically, so the two rows must produce the same packed set.
func TestPackForceIncludesMainUnderFilesWhitelist(t *testing.T) {
	for _, main := range []string{"lib/index.js", "./lib/index.js"} {
		t.Run(main, func(t *testing.T) {
			tmpDir := t.TempDir()
			writeMainEntryTree(t, tmpDir, map[string]string{
				"package.json": `{
					"name": "main-entry",
					"version": "1.0.0",
					"main": "` + main + `",
					"files": ["dist"]
				}`,
				"dist/a.js":     "module.exports = {}",
				"lib/index.js":  "module.exports = require('../dist/a.js')",
				"src/index.ts":  "export const x = 1",
				"lib/helper.js": "module.exports = 1",
			})

			assertPackedSet(t, tmpDir, []string{
				"dist/a.js",
				"lib/index.js",
				"package.json",
			}, "the manifest's main must ship whatever the files whitelist says, and "+
				"only the file it names — not its whole directory")
		})
	}
}

// TestPackEmptyMainChangesNothing pins the no-main baseline. Without it the
// force-include could be selecting paths for a reason other than main.
func TestPackEmptyMainChangesNothing(t *testing.T) {
	tmpDir := t.TempDir()
	writeMainEntryTree(t, tmpDir, map[string]string{
		"package.json": `{
			"name": "no-main",
			"version": "1.0.0",
			"files": ["dist"]
		}`,
		"dist/a.js":    "module.exports = {}",
		"lib/index.js": "module.exports = {}",
	})

	assertPackedSet(t, tmpDir, []string{
		"dist/a.js",
		"package.json",
	}, "a manifest with no main selects nothing extra")
}

// TestPackMainCannotDefeatHardReserved pins the far side of the boundary the
// force-include opens. main beats the "files" whitelist and the user's ignore
// patterns; it must never beat hardReservedExcludes.
//
// Its sibling TestPackMainCannotDefeatDefaultExcludes pins the other built-in
// list. The two are separate tests because #321 left them enforced by different
// mechanisms — this one structurally, that one by an explicit check — and a
// single test could not report which had broken.
//
// The distinction is the one hardReservedExcludes records: that list is what is
// never publishable by any route, and a rule that can be stepped around by
// naming the file in "main" is not that — it would make the manifest a way to
// smuggle .npmrc or anything out of node_modules past the one list that exists
// to hold them back. docs/adr/0004 records the boundary, with a note recording
// what #321 moved off it.
//
// The property holds structurally rather than by a check in the force-include
// itself: collectFiles evaluates isHardReserved in the walk and returns early,
// above the whitelist branch the force-include lives in. Nothing else pins that
// — TestPackHardReservedWinsInWhitelistMode does not exercise main — so a
// refactor hoisting the force-include above the isHardReserved check would open
// the hole with every other test still green. This is the test that goes red on
// it: hoisted there, the .npmrc row packs [.npmrc dist/a.js package.json]. Run
// and confirmed at this commit by moving the mainEntry arm above the
// isHardReserved check.
//
// The .npmrc row is the load-bearing one. The node_modules row is held up by a
// second, independent barrier and stays green under that same hoist: the
// isHardReserved check returns filepath.SkipDir for the node_modules directory,
// so the walk never reaches the nested file for any per-file check to see. It is
// kept as a row because that pruning is worth pinning too, but it is not what
// catches a hoisted force-include — do not read it as covering the .npmrc case.
//
// The .env row this test used to carry moved to
// TestPackMainCannotDefeatDefaultExcludes, which asserts the same outcome
// against the list .env now lives in. That leaves one root row here where there
// were two; see docs/agents/verification-discipline.md on why a row held up by
// pruning is not a substitute for it.
func TestPackMainCannotDefeatHardReserved(t *testing.T) {
	tests := []struct {
		name string
		main string
	}{
		{"npmrc", ".npmrc"},
		{"nested in node_modules", "node_modules/evil/index.js"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			writeMainEntryTree(t, tmpDir, map[string]string{
				"package.json": `{
					"name": "main-vs-guard",
					"version": "1.0.0",
					"main": "` + tt.main + `",
					"files": ["dist"]
				}`,
				"dist/a.js": "module.exports = {}",
				tt.main:     "SECRET=hunter2",
			})

			assertPackedSet(t, tmpDir, []string{
				"dist/a.js",
				"package.json",
			}, "main overrides the user's ignore patterns but never the "+
				"hard-reserved set, so naming "+tt.main+" in \"main\" must "+
				"not ship it")
		})
	}
}

// TestPackMainCannotDefeatDefaultExcludes is the other half of the boundary
// docs/adr/0004 draws, and #321 changed how it is enforced without changing what
// it says. main ".env" does not ship .env.
//
// Before #321 nothing pinned this arm specifically, and nothing needed to: the
// single built-in list was evaluated in the walk above the whitelist branch, so
// a .env named in "main" never reached the force-include at all and
// TestPackMainCannotDefeatHardReserved's .env row passed on the strength of that
// early return. Splitting the list moved defaultExcludes into
// ignoreLoader.excludes, which this arm deliberately does not call — not calling
// it is what an override is there — so the property now rests on the arm's own
// `softExcluded.covers(relPath) && !isIncludedDirectly(relPath, filesField)`.
// covers is ancestor-aware, so a main under a covered directory is refused too;
// the isIncludedDirectly half is the exception TestPackMainNamedByFilesFieldIsPacked
// pins, where a "files" entry naming the same path supplies the consent "main"
// does not.
//
// That is why this test exists rather than being folded back into the
// hard-reserved one. Run and confirmed by deleting that check: every row here
// packs the excluded file, while TestPackMainCannotDefeatHardReserved stays
// fully green.
//
// The warning half matters as much as the packed set. Refusing the file is only
// the safe failure if the maintainer is told, and warnMainEntryNotPacked is what
// tells them — the file is on disk, so validation.ValidatePackage's os.Stat
// passes and nothing else on the publish path would say a word.
func TestPackMainCannotDefeatDefaultExcludes(t *testing.T) {
	tests := []struct {
		name string
		main string
	}{
		{"dotenv", ".env"},
		{"dotenv variant", ".env.production"},
		{"log", "debug.log"},
		{"tarball", "pkg-1.0.0.tgz"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			writeMainEntryTree(t, tmpDir, map[string]string{
				"package.json": `{
					"name": "main-vs-default",
					"version": "1.0.0",
					"main": "` + tt.main + `",
					"files": ["dist"]
				}`,
				"dist/a.js": "module.exports = {}",
				tt.main:     "SECRET=hunter2",
			})

			var packed []string
			out := capturePackStdout(t, func() {
				packed = packedRelPaths(t, tmpDir)
			})

			if strings.Join(packed, "\n") != "dist/a.js\npackage.json" {
				t.Errorf("naming %q in \"main\" must not ship it: main beats the "+
					"user's ignore patterns but neither built-in exclusion list "+
					"(docs/adr/0004)\npacked: %v", tt.main, packed)
			}
			if !strings.Contains(out, "warning:") {
				t.Errorf("Pack() refused to pack the main %q and said nothing; the "+
					"file is on disk, so validation.ValidatePackage passes and "+
					"publish would report success on a package that does not "+
					"load\ngot stdout: %q", tt.main, out)
			}
		})
	}
}

// TestPackMainSurvivesIgnorePatternsUnderFilesWhitelist pins the decision
// recorded in docs/adr/0004: under a whitelist, main beats the user's ignore
// patterns as well as the whitelist itself. This is the case that runs against
// docs/adr/0001's direction rule, so it is the one to read the ADR beside.
//
// The ignore file names the entry point by basename rather than by its
// directory on purpose, though not for the reason this comment gave until #321
// corrected it. It claimed a "lib/" pattern would prune the directory during the
// walk; whitelist mode does not prune on ignore patterns — collectFiles skips
// the ignore check entirely when there is a "files" field, which is #318's rule
// — so the walk reaches lib/index.js either way and the fixture would still
// exercise the branch. The basename spelling is kept because it also matches the
// non-whitelist sibling TestPackMainRespectsIgnorePatternsWithoutFilesWhitelist,
// where pruning is real and the two fixtures are meant to differ only in the
// "files" field.
func TestPackMainSurvivesIgnorePatternsUnderFilesWhitelist(t *testing.T) {
	tmpDir := t.TempDir()
	writeMainEntryTree(t, tmpDir, map[string]string{
		"package.json": `{
			"name": "main-vs-ignore",
			"version": "1.0.0",
			"main": "lib/index.js",
			"files": ["dist"]
		}`,
		".npmignore":   "index.js\n",
		"dist/a.js":    "module.exports = {}",
		"lib/index.js": "module.exports = {}",
	})

	assertPackedSet(t, tmpDir, []string{
		"dist/a.js",
		"lib/index.js",
		"package.json",
	}, "an ignore pattern must not drop the entry point the manifest names")
}

// TestPackMainRespectsIgnorePatternsWithoutFilesWhitelist is the other half of
// that decision, and the guard for the second revert direction: the
// force-include lives inside the whitelist branch, so a package with no "files"
// field behaves exactly as it did before #319.
//
// Hoisting the force-include above the whitelist branch — where it would also
// override the ignore patterns in non-whitelist mode — fails here.
func TestPackMainRespectsIgnorePatternsWithoutFilesWhitelist(t *testing.T) {
	tmpDir := t.TempDir()
	writeMainEntryTree(t, tmpDir, map[string]string{
		"package.json": `{
			"name": "main-no-whitelist",
			"version": "1.0.0",
			"main": "lib/index.js"
		}`,
		".npmignore":   "index.js\n",
		"dist/a.js":    "module.exports = {}",
		"lib/index.js": "module.exports = {}",
	})

	assertPackedSet(t, tmpDir, []string{
		"dist/a.js",
		"package.json",
	}, "with no files field the user's ignore patterns decide the whole tree, "+
		"main included, exactly as they did before #319")
}

// TestPackMainNamingDirectoryShipsNothingExtra pins that main is matched as one
// path and never as a prefix. Node resolves a directory main through its own
// index/package.json lookup, which #319 puts out of scope; what must not happen
// is main ["lib"] quietly widening the whitelist to everything under lib.
func TestPackMainNamingDirectoryShipsNothingExtra(t *testing.T) {
	tmpDir := t.TempDir()
	writeMainEntryTree(t, tmpDir, map[string]string{
		"package.json": `{
			"name": "dir-main",
			"version": "1.0.0",
			"main": "lib",
			"files": ["dist"]
		}`,
		"dist/a.js":     "module.exports = {}",
		"lib/index.js":  "module.exports = {}",
		"lib/secret.js": "module.exports = {}",
	})

	assertPackedSet(t, tmpDir, []string{
		"dist/a.js",
		"package.json",
	}, "a main naming a directory must not pull the directory's contents in")
}

// TestPackMainEscapingPackageRootSelectsNothing pins that no spelling of main
// reaches outside the package. The file it names really exists here, one level
// above the package root, so the assertion is about the selection rule and not
// about the file being absent.
func TestPackMainEscapingPackageRootSelectsNothing(t *testing.T) {
	for _, main := range []string{"../evil.js", "../../evil.js", "lib/../../evil.js"} {
		t.Run(main, func(t *testing.T) {
			parent := t.TempDir()
			writeMainEntryTree(t, parent, map[string]string{
				"evil.js": "stolen",
			})

			tmpDir := filepath.Join(parent, "pkg")
			writeMainEntryTree(t, tmpDir, map[string]string{
				"package.json": `{
					"name": "escaping-main",
					"version": "1.0.0",
					"main": "` + main + `",
					"files": ["dist"]
				}`,
				"dist/a.js":    "module.exports = {}",
				"lib/index.js": "module.exports = {}",
			})

			assertPackedSet(t, tmpDir, []string{
				"dist/a.js",
				"package.json",
			}, "main must never select a path outside the package root")
		})
	}
}

// TestPackMissingMainDoesNotAbort pins #319's "does not abort" criterion: pack
// tolerates a main naming a path that is not on disk. The abort for that case
// stays where it already was, in validation.ValidatePackage, which
// internal/cli/publish.go runs before it packs. docs/adr/0004 records why it was
// not moved.
func TestPackMissingMainDoesNotAbort(t *testing.T) {
	tmpDir := t.TempDir()
	writeMainEntryTree(t, tmpDir, map[string]string{
		"package.json": `{
			"name": "missing-main",
			"version": "1.0.0",
			"main": "lib/index.js",
			"files": ["dist"]
		}`,
		"dist/a.js": "module.exports = {}",
	})

	assertPackedSet(t, tmpDir, []string{
		"dist/a.js",
		"package.json",
	}, "a main that names nothing on disk selects nothing and must not abort pack")
}

// capturePackStdout runs fn with os.Stdout redirected and returns what was
// written. Pack's warning goes to stdout because internal/pack has no warning
// idiom of its own to match — iconWarn and its siblings are unexported in
// internal/cli, which imports this package, so borrowing them would be a cycle.
func capturePackStdout(t *testing.T, fn func()) string {
	t.Helper()

	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe(): %v", err)
	}
	os.Stdout = w

	// Drained on a goroutine so a warning longer than the pipe buffer cannot
	// deadlock the writer.
	done := make(chan string, 1)
	go func() {
		var sb strings.Builder
		_, _ = io.Copy(&sb, r)
		done <- sb.String()
	}()

	fn()

	os.Stdout = orig
	if err := w.Close(); err != nil {
		t.Fatalf("closing the pipe: %v", err)
	}
	out := <-done
	_ = r.Close()
	return out
}

// TestPackWarnsWhenMainIsNotPacked covers the half of #319 the force-include
// does not reach: a main that is still missing from the finished set.
//
// The three rows are three different routes to the same broken package, which is
// why the check is on the packed set rather than on the selection branch. Only
// the first is caught by validation.ValidatePackage, and only on the publish
// path — internal/cli/push.go packs with no validation at all, and `lnpm publish
// --skip-validation` turns that check off — so without this warning the other
// two ship silently and the first ships silently on `lnpm push`.
func TestPackWarnsWhenMainIsNotPacked(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
	}{
		{
			name: "not on disk at all",
			files: map[string]string{
				"package.json": `{"name":"p","version":"1.0.0","main":"lib/index.js","files":["dist"]}`,
				"dist/a.js":    "module.exports = {}",
			},
		},
		{
			name: "held back by defaultExcludes",
			files: map[string]string{
				"package.json": `{"name":"p","version":"1.0.0","main":".env","files":["dist"]}`,
				"dist/a.js":    "module.exports = {}",
				".env":         "SECRET=hunter2",
			},
		},
		{
			name: "dropped by an ignore pattern with no files field",
			files: map[string]string{
				"package.json": `{"name":"p","version":"1.0.0","main":"lib/index.js"}`,
				".npmignore":   "index.js\n",
				"dist/a.js":    "module.exports = {}",
				"lib/index.js": "module.exports = {}",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			writeMainEntryTree(t, tmpDir, tt.files)

			var files []*FileInfo
			var err error
			out := capturePackStdout(t, func() {
				_, files, err = Pack(tmpDir)
			})

			if err != nil {
				t.Fatalf("Pack() must warn and continue, not fail: %v", err)
			}
			if !strings.Contains(out, "warning:") {
				t.Errorf("Pack() printed no warning for a main that is not in the "+
					"packed set; the publish would report success on a package that "+
					"does not load\ngot stdout: %q", out)
			}

			// The rest of the package must still be there — the warning reports
			// the defect, it does not narrow the publish.
			var names []string
			for _, f := range files {
				names = append(names, f.RelPath)
			}
			sort.Strings(names)
			if strings.Join(names, "\n") != "dist/a.js\npackage.json" {
				t.Errorf("warning must not change what is packed\ngot: %v", names)
			}
		})
	}
}

// TestPackDoesNotWarnWhenMainIsPacked is the negative half. A warning that fires
// on healthy packages is one every reader learns to skip past, so the quiet
// cases are worth pinning as hard as the loud one.
//
// The escaping row records a real gap rather than an endorsement: mainEntryPath
// maps both "no main" and "main outside the package" to "", so the warning
// cannot tell them apart and stays quiet for a manifest that is arguably
// defective. #319 is about the entry point going missing from the tarball, and
// widening the warning to malformed manifests is a separate call.
func TestPackDoesNotWarnWhenMainIsPacked(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
	}{
		{
			name: "force-included past the whitelist",
			files: map[string]string{
				"package.json": `{"name":"p","version":"1.0.0","main":"lib/index.js","files":["dist"]}`,
				"dist/a.js":    "module.exports = {}",
				"lib/index.js": "module.exports = {}",
			},
		},
		{
			name: "declared with a leading ./",
			files: map[string]string{
				"package.json": `{"name":"p","version":"1.0.0","main":"./lib/index.js","files":["dist"]}`,
				"dist/a.js":    "module.exports = {}",
				"lib/index.js": "module.exports = {}",
			},
		},
		{
			name: "no main declared",
			files: map[string]string{
				"package.json": `{"name":"p","version":"1.0.0","files":["dist"]}`,
				"dist/a.js":    "module.exports = {}",
			},
		},
		{
			name: "main escaping the package root",
			files: map[string]string{
				"package.json": `{"name":"p","version":"1.0.0","main":"../evil.js","files":["dist"]}`,
				"dist/a.js":    "module.exports = {}",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			writeMainEntryTree(t, tmpDir, tt.files)

			out := capturePackStdout(t, func() {
				if _, _, err := Pack(tmpDir); err != nil {
					t.Errorf("Pack() error: %v", err)
				}
			})

			if strings.Contains(out, "warning:") {
				t.Errorf("Pack() warned about a main that needs no warning\ngot stdout: %q", out)
			}
		})
	}
}

// TestPackManifestSurvivesIgnorePatterns is #301's fixture. An .npmignore line
// reading "package.json" removed the manifest from the pack, so lnpm published a
// package with no manifest in it — something npm does not permit and no consumer
// resolving through the package can survive.
//
// The three rows are the three routes the issue names, and all three packed
// [index.js] before the fix. Run and confirmed at bdd5447 by writing these rows
// and reading the failure: each reported `got: [index.js]` against a want that
// also holds package.json.
//
// Two distinct code sites dropped it, which is why the rows are not redundant.
// Rows 1 and 2 go through the non-whitelist prune, where ignores.excludes decides
// the whole tree; row 3 goes through the whitelist switch, where the
// isDefaultInclude arm re-consults the same patterns. A fix to one leaves the
// other reachable.
//
// Neither ignore file appears in any want: .npmignore and .gitignore are both in
// defaultExcludes, so they never ship. TestDefaultExcludesStillExclude pins that
// separately.
func TestPackManifestSurvivesIgnorePatterns(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
	}{
		{
			name: "npmignore names the manifest",
			files: map[string]string{
				"package.json": `{"name":"manifest-vs-npmignore","version":"1.0.0"}`,
				".npmignore":   "package.json\n",
				"index.js":     "module.exports = {}",
			},
		},
		{
			name: "gitignore fallback names the manifest",
			files: map[string]string{
				"package.json": `{"name":"manifest-vs-gitignore","version":"1.0.0"}`,
				".gitignore":   "package.json\n",
				"index.js":     "module.exports = {}",
			},
		},
		{
			name: "files whitelist omits it and npmignore names it",
			files: map[string]string{
				"package.json": `{"name":"manifest-vs-both","version":"1.0.0","files":["index.js"]}`,
				".npmignore":   "package.json\n",
				"index.js":     "module.exports = {}",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			writeMainEntryTree(t, tmpDir, tt.files)

			assertPackedSet(t, tmpDir, []string{
				"index.js",
				"package.json",
			}, "no ignore rule and no files field may drop the manifest; a package "+
				"without one is not installable")
		})
	}
}

// TestPackContentHashCoversVersionWithManifestIgnored is the identical-hash
// reproduction from #301's body, kept as its own test because it is the
// consequence that reaches the store rather than the tarball.
//
// The manifest is the only file carrying the version string into the hashed
// content. With it dropped, bumping the version changed nothing the hash sees, so
// two genuinely different releases hashed the same and insertPackageTx treated
// them as one record and overwrote its version in place (#196). Run and
// confirmed at bdd5447: this fixture hashed 1.0.0 and 2.0.0 to the same digest.
//
// The assertion is that the two differ, not that either equals a literal. A
// hard-coded digest would pin xxhash's output and HashFiles' framing, neither of
// which #301 is about, and would have to be rewritten by anyone who touches
// either.
func TestPackContentHashCoversVersionWithManifestIgnored(t *testing.T) {
	hashAtVersion := func(version string) string {
		t.Helper()
		tmpDir := t.TempDir()
		writeMainEntryTree(t, tmpDir, map[string]string{
			"package.json": `{"name":"hash-covers-version","version":"` + version + `"}`,
			".npmignore":   "package.json\n",
			"index.js":     "module.exports = {}",
		})

		_, files, err := Pack(tmpDir)
		if err != nil {
			t.Fatalf("Pack() error: %v", err)
		}
		return HashFiles(files)
	}

	first := hashAtVersion("1.0.0")
	second := hashAtVersion("2.0.0")
	if first == second {
		t.Errorf("packing the same tree at 1.0.0 and 2.0.0 produced the same content "+
			"hash %s; the manifest was dropped, so the hash no longer covers the "+
			"version and the store records two releases as one", first)
	}
}

// TestPackManifestForceIncludeIsRootAnchored pins that the exemption covers the
// package's own manifest and nothing that merely shares its name. A nested
// sub/package.json is a different package's manifest, or a fixture, and an
// .npmignore naming "package.json" must still drop it.
//
// This is the same anchoring isDefaultInclude already applies after #320, and it
// is asserted here rather than left to that function because the force-include is
// a separate comparison that could have been written as a basename test.
//
// The row is not purely a guard: at bdd5447 it fails on the root manifest, since
// the unanchored "package.json" pattern drops both. Run and confirmed — the
// failure read `got: [index.js sub/a.js]`.
func TestPackManifestForceIncludeIsRootAnchored(t *testing.T) {
	tmpDir := t.TempDir()
	writeMainEntryTree(t, tmpDir, map[string]string{
		"package.json":     `{"name":"nested-manifest","version":"1.0.0"}`,
		".npmignore":       "package.json\n",
		"index.js":         "module.exports = {}",
		"sub/package.json": `{"name":"not-this-one","version":"1.0.0"}`,
		"sub/a.js":         "module.exports = {}",
	})

	assertPackedSet(t, tmpDir, []string{
		"index.js",
		"package.json",
		"sub/a.js",
	}, "only the package root's own manifest is unexcludable; a nested "+
		"package.json is an ordinary file the user's patterns still govern")
}

// TestPackFailsWhenManifestIsNotPacked pins #301's backstop: if the finished set
// somehow holds no manifest, Pack refuses rather than handing a caller a package
// that cannot be installed.
//
// The fixture is a symlinked package.json, which is the one route to a
// manifest-free set that survives the force-include. readPackageJSON goes through
// os.ReadFile, which follows the link, so the manifest reads and parses fine and
// Pack gets as far as the walk; the walk then skips every symlink before any
// include check runs, so nothing the force-include does can put it back. Run and
// confirmed at bdd5447 before any fix existed: Pack returned nil error and the
// packed set was [index.js].
//
// This is an abort where the sibling case in docs/adr/0004 is a warning, and the
// two are not inconsistent. A missing "main" leaves a package that is present and
// does not load, and validation.ValidatePackage already refuses it on the
// ordinary publish path. A missing manifest leaves something that is not a
// package at all — nothing downstream can read its name or version — and no
// existing check catches it: readPackageJSON reads from disk, not from the packed
// set, so it passes precisely in this case.
//
// It lives in Pack rather than in the publish command for the reason
// warnMainEntryNotPacked does: internal/cli/publish.go:159 is one of three
// callers, and internal/cli/push.go:169 and push.go:194 pack with no validation
// at all.
func TestPackFailsWhenManifestIsNotPacked(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires privileges on Windows")
	}

	// The real manifest lives outside the package so the walk finds only the
	// link. Writing it inside would leave a second, ordinary package.json for
	// the force-include to select, and the fixture would prove nothing.
	outside := t.TempDir()
	writeMainEntryTree(t, outside, map[string]string{
		"manifest.json": `{"name":"symlinked-manifest","version":"1.0.0"}`,
	})

	tmpDir := t.TempDir()
	writeMainEntryTree(t, tmpDir, map[string]string{
		"index.js": "module.exports = {}",
	})
	if err := os.Symlink(filepath.Join(outside, "manifest.json"),
		filepath.Join(tmpDir, "package.json")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	_, files, err := Pack(tmpDir)
	if err == nil {
		var names []string
		for _, f := range files {
			names = append(names, f.RelPath)
		}
		sort.Strings(names)
		t.Fatalf("Pack() succeeded on a package with no package.json in the packed "+
			"set; the published package is not installable\npacked: %v", names)
	}
	if !strings.Contains(err.Error(), "package.json") {
		t.Errorf("Pack() error must name the missing file so the reader knows what "+
			"to look for\ngot: %v", err)
	}
}

// TestPackSucceedsWhenManifestIsPacked is the backstop's negative half. An abort
// that can fire on a healthy package is worse than the bug it guards, and every
// other Pack test would report the same failure text, so the ordinary case is
// pinned on its own.
func TestPackSucceedsWhenManifestIsPacked(t *testing.T) {
	tmpDir := t.TempDir()
	writeMainEntryTree(t, tmpDir, map[string]string{
		"package.json": `{"name":"healthy","version":"1.0.0","files":["index.js"]}`,
		"index.js":     "module.exports = {}",
		"src/index.ts": "export const x = 1",
	})

	assertPackedSet(t, tmpDir, []string{
		"index.js",
		"package.json",
	}, "the backstop must not fire on a package whose manifest is packed")
}

// TestPackManifestCannotDefeatHardReserved pins where the manifest
// force-include sits in collectFiles: below the isHardReserved check, never
// above it. #301 puts the manifest beyond the reach of the user's own rules and
// of the overridable defaults; it does not exempt it from the hard-reserved set,
// for the reason docs/adr/0004 gives for "main" and docs/adr/0005 repeats for
// the manifest — a rule anything can step around is not one.
//
// It mutated defaultExcludes before #321 split the list. Both mutations express
// the same intent, but only this one still tests it: the manifest force-include
// now sits *above* the overridable defaults on purpose, so seeding package.json
// into defaultExcludes would leave Pack succeeding and the test would fail for
// the new correct behaviour rather than for the bug it exists to catch.
//
// No ordinary fixture can see that placement, because no hardReservedExcludes
// entry matches package.json, so this test puts one there for its own duration
// and asks which wins. Below the guard, the manifest is dropped and Pack then
// refuses via requireManifestPacked; hoisted above it, the manifest ships past
// the guard and Pack succeeds. Run and confirmed red under that hoist
// (isHardReserved consulted as "&& !isManifest"): both rows reported
// `packed: [index.js package.json]`.
//
// package.json is appended to lowerHardReservedExcludes as well as to
// hardReservedExcludes, because isHardReserved reads only the lowered copy —
// appended rather than recomputed, which is the wording docs/adr/0005's
// amendment settles on. Run and confirmed: dropping the lowered line leaves the
// guard exactly as it was, and the test then fails with
// the same `packed: [index.js package.json]` a real hoist produces — a failure
// that looks like the bug this test exists to catch but is not it.
//
// This mutates package-level state, so it must not call t.Parallel() and must
// not run beside a test that does. No test in this package calls it.
func TestPackManifestCannotDefeatHardReserved(t *testing.T) {
	originalExcludes, originalLowered := hardReservedExcludes, lowerHardReservedExcludes
	t.Cleanup(func() {
		hardReservedExcludes, lowerHardReservedExcludes = originalExcludes, originalLowered
	})
	// New slices rather than append-in-place, so the restored originals cannot
	// share a backing array with the mutated copies.
	hardReservedExcludes = append(append([]string{}, originalExcludes...), manifestFileName)
	lowerHardReservedExcludes = append(append([]string{}, originalLowered...), manifestFileName)

	tests := []struct {
		name     string
		manifest string
	}{
		{
			name:     "no files field",
			manifest: `{"name":"guard-vs-manifest","version":"1.0.0"}`,
		},
		{
			name:     "files whitelist",
			manifest: `{"name":"guard-vs-manifest","version":"1.0.0","files":["index.js"]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			writeMainEntryTree(t, tmpDir, map[string]string{
				"package.json": tt.manifest,
				"index.js":     "module.exports = {}",
			})

			_, files, err := Pack(tmpDir)
			if err == nil {
				var names []string
				for _, f := range files {
					names = append(names, f.RelPath)
				}
				sort.Strings(names)
				t.Fatalf("Pack() succeeded with package.json in "+
					"hardReservedExcludes; the manifest force-include has been "+
					"hoisted above the hard-reserved check, which is what makes "+
					"that list steppable\npacked: %v", names)
			}
			if !strings.Contains(err.Error(), "package.json") {
				t.Errorf("Pack() must fail through requireManifestPacked, which "+
					"names the missing file\ngot: %v", err)
			}
		})
	}
}

// TestPackWithNothingNamedExplicitlyKeepsBuiltInExcludesOut is the
// unchanged-behaviour half of #321. Splitting the built-in exclusion list into a
// hard-reserved set and an overridable one moves *where* the overridable half is
// evaluated — out of the walk's first check and into the shallowest layer of the
// ignore chain — and a package that names nothing explicitly must not be able to
// tell.
//
// So this is a characterization test, not a red-green one. It was written before
// the split, run against the unsplit code and observed green there; it is here to
// go red if the move changes what an ordinary package ships.
//
// Every path below is drawn from the built-in lists except three. index.js and
// README.md are the positive controls, and they ship. coverage/report.html is
// excluded by the fixture's own .npmignore rather than by either built-in list —
// it is here so the assertion cannot pass on a walk that has stopped consulting
// the user's patterns at all, which is a different bug the built-in paths alone
// would not catch. Everything else was excluded before the split and must stay
// excluded after it.
//
// The .npmignore line is not decoration for the same reason. It puts a real
// ignore scope in the chain, so the seeded verdict has to survive being carried
// through applyIgnorePatterns rather than being returned from a chain with no
// scopes in it at all.
func TestPackWithNothingNamedExplicitlyKeepsBuiltInExcludesOut(t *testing.T) {
	tmpDir := t.TempDir()
	writeMainEntryTree(t, tmpDir, map[string]string{
		"package.json":              `{"name":"nothing-explicit","version":"1.0.0"}`,
		".npmignore":                "coverage/\n",
		"index.js":                  "module.exports = {}",
		"README.md":                 "# nothing-explicit",
		".env":                      "SECRET=1",
		".env.example":              "SECRET=",
		"app.log":                   "noise",
		"pkg-1.0.0.tgz":             "tarball",
		"merged.js.orig":            "<<<<<<< HEAD",
		".npmrc":                    "//registry:_authToken=deadbeef",
		"Thumbs.db":                 "cruft",
		"node_modules/dep/index.js": "dep",
		"coverage/report.html":      "<html></html>",
	})

	assertPackedSet(t, tmpDir, []string{
		"README.md",
		"index.js",
		"package.json",
	}, "a package that names nothing explicitly must ship exactly what it "+
		"shipped before the hard-reserved/overridable split")
}

// TestPackFilesEntryOverridesDefaultExcludes is #321's headline case. A
// "files" entry naming .env.example could not publish it: isDefaultExcluded ran
// first in the walk, above every user-driven selection rule, so the ".env.*"
// entry in the built-in list won and there was no way to ship the template.
//
// .env sits in the same fixture as the control. It is not named by "files", so
// the overridable default still holds it back — the entry overrides the default
// for the path it names and for nothing else.
func TestPackFilesEntryOverridesDefaultExcludes(t *testing.T) {
	tmpDir := t.TempDir()
	writeMainEntryTree(t, tmpDir, map[string]string{
		"package.json": `{
			"name": "files-beats-default",
			"version": "1.0.0",
			"files": ["index.js", ".env.example"]
		}`,
		"index.js":     "module.exports = {}",
		".env.example": "API_KEY=",
		".env":         "API_KEY=hunter2",
	})

	assertPackedSet(t, tmpDir, []string{
		".env.example",
		"index.js",
		"package.json",
	}, "a \"files\" entry must override the overridable half of the built-in "+
		"exclusion list, and must not widen it to a sibling it does not name")
}

// TestPackNegationOverridesDefaultExcludes is the other direction #321 asks for:
// an "!" negation in .npmignore re-including an overridable default, in a
// package with no "files" field at all.
//
// The negation is the only rule in the ignore file, so nothing here depends on
// last-match-wins between two user patterns; what it depends on is the
// overridable set being seeded *into* the ignore chain rather than decided above
// it, which is the only way a user pattern can be the last word on it.
func TestPackNegationOverridesDefaultExcludes(t *testing.T) {
	tmpDir := t.TempDir()
	writeMainEntryTree(t, tmpDir, map[string]string{
		"package.json": `{"name":"negation-beats-default","version":"1.0.0"}`,
		".npmignore":   "!.env.example\n",
		"index.js":     "module.exports = {}",
		".env.example": "API_KEY=",
		".env":         "API_KEY=hunter2",
	})

	assertPackedSet(t, tmpDir, []string{
		".env.example",
		"index.js",
		"package.json",
	}, "an \"!\" negation must re-include an overridable default, and .env, "+
		"which nothing negates, must stay out")
}

// TestPackEnvExampleFixtureFromIssue321 is fixture J from the issue, verbatim:
// "files": ["index.js", ".env.example", "app.log"] plus an .npmignore holding
// "!.env.example". It packed [index.js, package.json] — .env.example was matched
// by the ".env.*" entry in the built-in list and app.log by "*.log", and neither
// the whitelist nor the negation could reach them.
//
// It is kept separate from the two single-mechanism tests above because it is the
// case both mechanisms fire on at once, and because app.log arrives through
// "files" alone with no negation behind it.
//
// Both revert directions were run against this fixture, per
// docs/agents/verification-discipline.md, and both are red:
//
//   - Remove the fix — seed ignoreLoader.excludes with false again and put
//     isDefaultExcluded back in the walk beside isHardReserved. This fixture
//     packs [index.js package.json], which is the set #321 reports verbatim.
//     TestPackFilesEntryOverridesDefaultExcludes and
//     TestPackNegationOverridesDefaultExcludes report the same. Eight rows of
//     TestPackFilesEntryOverridesDefaultExcludesOnlyByDirectMatch join them —
//     every row where an entry names a path directly, since with the list back
//     in the walk no entry can name anything past it — along with
//     TestPackFilesEntryNamingAnIgnoreFile/.npmignore and
//     TestPackMainNamedByFilesFieldIsPacked/files_names_the_path_main_names.
//   - Move the fix back before the exclusion pass — keep the seed but let the
//     walk check isDefaultExcluded too. Identical output: the seed is shadowed
//     by the early return, so keeping it changes nothing. That is the direction
//     worth running, because a reader who saw only the seed added would think
//     the fix was in place.
//
// Neither direction touches TestPackMainCannotDefeatDefaultExcludes or
// TestPackMainCannotDefeatHardReserved, which stay green under both: "main" is
// refused either way. Those have revert checks of their own; see each.
func TestPackEnvExampleFixtureFromIssue321(t *testing.T) {
	tmpDir := t.TempDir()
	writeMainEntryTree(t, tmpDir, map[string]string{
		"package.json": `{
			"name": "fixture-j",
			"version": "1.0.0",
			"files": ["index.js", ".env.example", "app.log"]
		}`,
		".npmignore":   "!.env.example\n",
		"index.js":     "module.exports = {}",
		".env.example": "API_KEY=",
		"app.log":      "started",
	})

	assertPackedSet(t, tmpDir, []string{
		".env.example",
		"app.log",
		"index.js",
		"package.json",
	}, "issue #321 fixture J must ship every path the manifest names")
}

// TestPackWarnsWhenFilesNamesHardReserved is #321's other acceptance criterion:
// node_modules and .git stay unpackable when named explicitly in "files", and
// naming one says so out loud instead of dropping it in silence. Silence is the
// failure mode the issue is about — before the split there was no way to publish
// .env.example and no message explaining that either.
//
// The warning is derived from the "files" entries, not from the walk, and it has
// to be: node_modules and .git are pruned at the directory level, so a "files"
// entry naming a path inside one never reaches a walked path for any per-file
// check to notice.
//
// The four rows are not equal evidence on their packed-set half. Disabling the
// isHardReserved check in the walk and running this test is what established
// which is which — not reading the code, and re-run after #346 rather than
// carried over. Three red, one green:
//
//   - .npmrc reported `packed: [.npmrc index.js package.json]`, and
//     node_modules and "./node_modules" both reported `packed: [index.js
//     node_modules/dep/index.js package.json]`. All three are real evidence
//     that the check is what refuses the entry.
//   - .git stayed green. filterGitFiles runs over the finished set in Pack and
//     drops anything under .git whatever collectFiles decided, so this row
//     cannot fail for the reason the test is about. It is kept because #321
//     names .git, and it is the weakest row in the file — do not read it as
//     covering the hard-reserved check.
//
// The "./node_modules" row moved into the first group with #346. Before it,
// matchFilesField resolved no leading "./", so the entry selected nothing
// whatever the built-in lists said and the row stayed green for that third
// reason. Its original value was entirely in the warning half — it is the row
// that caught namesHardReserved's comment claiming such an entry is refused in
// silence, when the basename fold makes it warn.
//
// The warning half is load-bearing on all four rows, since nothing else in the
// codebase produces that message.
func TestPackWarnsWhenFilesNamesHardReserved(t *testing.T) {
	tests := []struct {
		name  string
		entry string
		files map[string]string
	}{
		{
			name:  "npmrc",
			entry: ".npmrc",
			files: map[string]string{".npmrc": "//registry:_authToken=deadbeef"},
		},
		{
			name:  "node_modules",
			entry: "node_modules",
			files: map[string]string{"node_modules/dep/index.js": "dep"},
		},
		{
			name:  "dot git",
			entry: ".git",
			files: map[string]string{".git/config": "[core]"},
		},
		{
			name:  "dot slash prefixed",
			entry: "./node_modules",
			files: map[string]string{"node_modules/dep/index.js": "dep"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			tree := map[string]string{
				"package.json": `{
					"name": "files-vs-hard-reserved",
					"version": "1.0.0",
					"files": ["index.js", "` + tt.entry + `"]
				}`,
				"index.js": "module.exports = {}",
			}
			for rel, content := range tt.files {
				tree[rel] = content
			}
			writeMainEntryTree(t, tmpDir, tree)

			var packed []string
			out := capturePackStdout(t, func() {
				packed = packedRelPaths(t, tmpDir)
			})

			if strings.Join(packed, "\n") != "index.js\npackage.json" {
				t.Errorf("naming %q in \"files\" must not publish it\npacked: %v", tt.entry, packed)
			}
			if !strings.Contains(out, "warning:") || !strings.Contains(out, tt.entry) {
				t.Errorf("Pack() must warn that the \"files\" entry %q names a path "+
					"lnpm never publishes; dropping it in silence is the failure "+
					"mode #321 is about\ngot stdout: %q", tt.entry, out)
			}
		})
	}
}

// TestPackDoesNotWarnForOrdinaryFilesEntries is the negative half. A warning
// that fires on healthy manifests is one every reader learns to skip past, and
// the overridable rows are the ones that would fire if the warning were wired to
// the wrong list — .env.example naming a path lnpm *does* now publish must be
// silent.
func TestPackDoesNotWarnForOrdinaryFilesEntries(t *testing.T) {
	tmpDir := t.TempDir()
	writeMainEntryTree(t, tmpDir, map[string]string{
		"package.json": `{
			"name": "ordinary-files",
			"version": "1.0.0",
			"files": ["dist", ".env.example", "app.log", "node_modules_backup"]
		}`,
		"dist/a.js":                        "module.exports = {}",
		".env.example":                     "API_KEY=",
		"app.log":                          "started",
		"node_modules_backup/dep/index.js": "dep",
	})

	out := capturePackStdout(t, func() {
		_ = packedRelPaths(t, tmpDir)
	})

	if strings.Contains(out, "warning:") {
		t.Errorf("Pack() warned about \"files\" entries it publishes just fine\ngot stdout: %q", out)
	}
}

// TestPackFilesEntryOverridesDefaultExcludesOnlyByDirectMatch pins how far
// #321's override reaches. A "files" entry overrides defaultExcludes for a path
// it names and never for a path it merely contains.
//
// The first draft of #321 got this wrong in the direction that widens a publish.
// isIncluded matches by directory prefix, and the whitelist arm consults no
// ignore rules, so "files": ["dist"] — the commonest "files" field there is —
// packed dist/.env, dist/app.log and dist/pkg.tgz, none of which origin/main
// shipped. The narrowing check in collectFiles' isIncluded arm is what stops it.
//
// The check has three separable parts and each was reverted on its own. Every
// figure below was measured against this table as it now stands — eighteen
// rows — and re-measured when #346 added the two "./" rows, not carried over
// from an earlier draft.
//
// Delete the whole check at the isIncluded arm: nine rows red, nine green.
// The nine red, with what each packs — "directory entry"
// [dist/.env dist/README.md dist/a.js dist/app.log dist/pkg.tgz package.json];
// "subtree glob entry", "bare wildcard segment" and "dot slash directory entry"
// all three [dist/.env dist/a.js package.json]; "bare wildcard at the root"
// [.env app.log index.js package.json pkg-1.0.0.tgz]; "double bare wildcard at
// the root" [.env index.js package.json]; "direct entry outranks"
// [dist/.env dist/a.js dist/app.log package.json], leaking the log beside the
// .env it was right about; "directory entry ... subdirectory"
// [package.json src/.env/config src/a.js]; "degenerate entry"
// [.env index.js package.json]. The nine green are the eight rows that assert
// only a direct name, plus "dot entry". Note "direct entry outranks" is red
// despite carrying a direct entry: its dist/.env still packs, and it fails on
// the dist/app.log its containing entry leaks.
//
// Swap softExcluded.covers back to isDefaultExcluded, keeping the rest: exactly
// one row red, "directory entry does not reach a file under a default-excluded
// subdirectory". That row is the only guard on the ancestor half — every other
// containment row names a file that is itself default-excluded and stays green
// without it.
//
// Classify every filepath.Match hit as direct again, undoing the bare-wildcard
// rule: exactly three rows red — "bare wildcard segment" packs
// [dist/.env dist/a.js package.json], "bare wildcard at the root" packs
// [.env app.log index.js package.json pkg-1.0.0.tgz], and "double bare wildcard
// at the root" packs [.env index.js package.json]. Nothing else moves, which is
// the evidence that the rule only ever narrows.
//
// The boundary is a glob's final segment: a bare "*" or "**" sweeps a
// directory, anything that constrains the segment names files. So "dist/*",
// "dist/**" and "dist" agree — all three are "everything in dist", which says
// nothing about any particular file in it — while "dist/*.env", ".env.*" and
// "*.example" name what they match.
//
// An earlier draft had "dist/*" naming paths, on the reasoning that
// filepath.Match's "*" cannot cross a separator so the entry picks out one
// level rather than a tree. That is true and it is not the question. It also
// made "files": ["*"] publish .env, app.log and pkg-1.0.0.tgz from a manifest
// that had named none of them, and it split the spellings of "ship everything"
// — "" and "*" here — so that one kept .env out and the other did not. The
// "degenerate entry" and "bare wildcard at the root" rows pin them agreeing:
// both go red when the narrowing check is deleted, so each pins the
// classification on its own.
//
// "dot entry" is a different kind of row and does not pin the classification.
// It stays green under all three reverts above, because "." matches nothing at
// all in matchFilesField — not the package root, not anything under it — so
// .env is unpacked there however the classification behaves. What it does pin
// is npm parity, and that is the point of keeping it: npm 11.16.0 ships only
// the always-included set for "files": ["."], run and confirmed on a fixture
// package, so selecting nothing is the correct answer and not the #346 defect
// it resembles. #346 fixed "./", which npm does resolve, and deliberately left
// "." alone. Normalize "." to "" — the obvious-looking extension of that fix —
// and this row goes red packing [index.js package.json], which is the revert it
// exists to catch. Measured, not reasoned.
//
// This is not npm's behaviour, and the divergence is wider than one rule. npm
// does not ignore .env at all, so `npm publish` with "files": ["dist"]
// publishes dist/.env outright. lnpm withholds it by default and publishes it
// when the maintainer names that exact path.
func TestPackFilesEntryOverridesDefaultExcludesOnlyByDirectMatch(t *testing.T) {
	tests := []struct {
		name  string
		files string
		tree  map[string]string
		want  []string
	}{
		{
			name:  "directory entry does not reach a default-excluded file inside it",
			files: `["dist"]`,
			tree: map[string]string{
				"dist/a.js":      "module.exports = {}",
				"dist/.env":      "SECRET=hunter2",
				"dist/app.log":   "started",
				"dist/pkg.tgz":   "tarball",
				"dist/README.md": "# dist",
			},
			want: []string{"dist/README.md", "dist/a.js", "package.json"},
		},
		{
			name:  "subtree glob entry is containment too",
			files: `["dist/**"]`,
			tree: map[string]string{
				"dist/a.js": "module.exports = {}",
				"dist/.env": "SECRET=hunter2",
			},
			want: []string{"dist/a.js", "package.json"},
		},
		{
			name:  "entry naming the path exactly",
			files: `["dist/.env"]`,
			tree: map[string]string{
				"dist/a.js": "module.exports = {}",
				"dist/.env": "SECRET=hunter2",
			},
			want: []string{"dist/.env", "package.json"},
		},
		{
			name:  "bare wildcard segment is containment",
			files: `["dist/*"]`,
			tree: map[string]string{
				"dist/a.js": "module.exports = {}",
				"dist/.env": "SECRET=hunter2",
			},
			want: []string{"dist/a.js", "package.json"},
		},
		{
			name:  "bare wildcard at the root is containment",
			files: `["*"]`,
			tree: map[string]string{
				"index.js":      "module.exports = {}",
				".env":          "SECRET=hunter2",
				"app.log":       "started",
				"pkg-1.0.0.tgz": "tarball",
			},
			want: []string{"index.js", "package.json"},
		},
		{
			name:  "double bare wildcard at the root is containment",
			files: `["**"]`,
			tree: map[string]string{
				"index.js": "module.exports = {}",
				".env":     "SECRET=hunter2",
			},
			want: []string{"index.js", "package.json"},
		},
		{
			name:  "glob constraining the segment names the path",
			files: `["index.js", "*.example"]`,
			tree: map[string]string{
				"index.js":     "module.exports = {}",
				".env.example": "API_KEY=",
				".env":         "API_KEY=hunter2",
			},
			want: []string{".env.example", "index.js", "package.json"},
		},
		{
			name:  "glob constraining the segment by prefix names the path",
			files: `["index.js", ".env.*"]`,
			tree: map[string]string{
				"index.js":     "module.exports = {}",
				".env.example": "API_KEY=",
				".env":         "API_KEY=hunter2",
			},
			want: []string{".env.example", "index.js", "package.json"},
		},
		{
			name:  "nested glob constraining the segment names the path",
			files: `["dist/*.env"]`,
			tree: map[string]string{
				"dist/a.js": "module.exports = {}",
				"dist/.env": "SECRET=hunter2",
			},
			want: []string{"dist/.env", "package.json"},
		},
		{
			name:  "direct entry outranks a containing entry beside it",
			files: `["dist", "dist/.env"]`,
			tree: map[string]string{
				"dist/a.js":    "module.exports = {}",
				"dist/.env":    "SECRET=hunter2",
				"dist/app.log": "started",
			},
			want: []string{"dist/.env", "dist/a.js", "package.json"},
		},
		{
			name:  "directory entry does not reach a file under a default-excluded subdirectory",
			files: `["src"]`,
			tree: map[string]string{
				"src/a.js":        "module.exports = {}",
				"src/.env/config": "SECRET=hunter2",
			},
			want: []string{"package.json", "src/a.js"},
		},
		{
			name:  "entry naming a path under a default-excluded directory",
			files: `["src/.env/config"]`,
			tree: map[string]string{
				"src/a.js":        "module.exports = {}",
				"src/.env/config": "SECRET=hunter2",
			},
			want: []string{"package.json", "src/.env/config"},
		},
		{
			name:  "entry naming a path under a glob-matched excluded directory",
			files: `[".env.d/keep.js"]`,
			tree: map[string]string{
				".env.d/keep.js": "module.exports = {}",
				".env.d/drop.js": "module.exports = {}",
			},
			want: []string{".env.d/keep.js", "package.json"},
		},
		{
			name:  "dot slash directory entry is containment",
			files: `["./dist"]`,
			tree: map[string]string{
				"dist/a.js": "module.exports = {}",
				"dist/.env": "SECRET=hunter2",
			},
			want: []string{"dist/a.js", "package.json"},
		},
		{
			name:  "dot slash entry naming the path exactly",
			files: `["./dist/.env"]`,
			tree: map[string]string{
				"dist/a.js": "module.exports = {}",
				"dist/.env": "SECRET=hunter2",
			},
			want: []string{"dist/.env", "package.json"},
		},
		{
			name:  "dot entry",
			files: `["."]`,
			tree: map[string]string{
				"index.js": "module.exports = {}",
				".env":     "SECRET=hunter2",
			},
			want: []string{"package.json"},
		},
		{
			name:  "degenerate entry ships what no files field ships",
			files: `[""]`,
			tree: map[string]string{
				"index.js": "module.exports = {}",
				".env":     "SECRET=hunter2",
			},
			want: []string{"index.js", "package.json"},
		},
		{
			name:  "root entry naming the path exactly",
			files: `[".env.example"]`,
			tree: map[string]string{
				".env.example": "API_KEY=",
				".env":         "API_KEY=hunter2",
			},
			want: []string{".env.example", "package.json"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			tree := map[string]string{
				"package.json": `{
					"name": "direct-match",
					"version": "1.0.0",
					"files": ` + tt.files + `
				}`,
			}
			for rel, content := range tt.tree {
				tree[rel] = content
			}
			writeMainEntryTree(t, tmpDir, tree)

			assertPackedSet(t, tmpDir, tt.want,
				"a \"files\" entry overrides defaultExcludes for a path it names "+
					"and never for a path it only contains")
		})
	}
}

// TestPackWarnsWhenIgnoreNegationNamesHardReserved covers the second override
// mechanism #321 introduced. A maintainer can ask for a default-excluded path
// with a "files" entry or with an "!" negation, and a refused ask must say so
// whichever way it was written — TestPackWarnsWhenFilesNamesHardReserved is the
// same claim for the other half.
//
// The npmignore and gitignore rows are not redundant. loadIgnorePatterns tries
// .npmignore first and falls back to .gitignore only when there is none, so a
// warning wired to one filename alone would stay green on the other row.
//
// The nested row records a gap rather than a guarantee, and it asserts silence
// on purpose so the gap is visible in the test file rather than only in a
// comment. warnHardReservedIgnoreNegation reads the package root's ignore file
// only, so a negation in src/.npmignore is refused without a word. Serving it
// would mean plumbing ignoreLoader's scopes out of collectFiles.
func TestPackWarnsWhenIgnoreNegationNamesHardReserved(t *testing.T) {
	tests := []struct {
		name       string
		ignoreFile string
		line       string
		wantWarn   bool
	}{
		{"npmignore", ".npmignore", "!node_modules\n", true},
		{"gitignore", ".gitignore", "!node_modules\n", true},
		{"dot git", ".npmignore", "!.git\n", true},
		{"trailing slash", ".npmignore", "!node_modules/\n", true},
		{"ordinary negation", ".npmignore", "dist/\n!dist/keep.js\n", false},
		{"overridable default", ".npmignore", "!.env.example\n", false},
		{"nested ignore file is not read", "src/.npmignore", "!node_modules\n", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			writeMainEntryTree(t, tmpDir, map[string]string{
				"package.json":  `{"name":"negation-warning","version":"1.0.0"}`,
				tt.ignoreFile:   tt.line,
				"index.js":      "module.exports = {}",
				"src/nested.js": "module.exports = {}",
			})

			out := capturePackStdout(t, func() {
				if _, _, err := Pack(tmpDir); err != nil {
					t.Errorf("Pack() error: %v", err)
				}
			})

			if got := strings.Contains(out, "warning:"); got != tt.wantWarn {
				t.Errorf("Pack() warned = %v, want %v for %s containing %q\ngot stdout: %q",
					got, tt.wantWarn, tt.ignoreFile, tt.line, out)
			}
		})
	}
}

// TestPackFilesEntryNamingAnIgnoreFile pins an asymmetry #321 created among the
// three ignore-file entries in defaultExcludes. All three became overridable,
// but only .npmignore actually publishes when named: filterGitFiles runs over
// the finished set and drops .gitignore and .gitattributes by basename at every
// depth, whatever collectFiles decided.
//
// Before the split none of the three could be published at all, so the
// difference is new and user-visible. It is pinned rather than left to a comment
// because a comment claiming it would be exactly the kind of assertion that goes
// stale when #398 reconciles filterGitFiles with these lists — this test is what
// will go red and make that reconciliation deliberate.
//
// Publishing an .npmignore is harmless: it describes what was left out, not a
// secret. That is why #321 left it overridable rather than hard-reserving it,
// which would have been a membership change and out of scope.
func TestPackFilesEntryNamingAnIgnoreFile(t *testing.T) {
	tests := []struct {
		entry    string
		wantPack bool
	}{
		{".npmignore", true},
		{".gitignore", false},
		{".gitattributes", false},
	}

	for _, tt := range tests {
		t.Run(tt.entry, func(t *testing.T) {
			tmpDir := t.TempDir()
			writeMainEntryTree(t, tmpDir, map[string]string{
				"package.json": `{
					"name": "names-an-ignore-file",
					"version": "1.0.0",
					"files": ["index.js", "` + tt.entry + `"]
				}`,
				"index.js": "module.exports = {}",
				tt.entry:   "node_modules\n",
			})

			want := []string{"index.js", "package.json"}
			why := "filterGitFiles strips this from the finished set whatever " +
				"the \"files\" field says"
			if tt.wantPack {
				want = []string{tt.entry, "index.js", "package.json"}
				why = "no safety pass covers .npmignore, so naming it publishes it"
			}
			assertPackedSet(t, tmpDir, want, why)
		})
	}
}

// TestPackMainNamedByFilesFieldIsPacked is #321's first acceptance criterion —
// `"files": [".env.example"]` packs `.env.example` — in the one arrangement that
// failed it after the split. Setting "main" to the same path made the mainEntry
// arm of collectFiles' whitelist switch fire first, and that arm refuses a
// default-excluded path, so the manifest's own "files" entry was never consulted
// and the template did not ship.
//
// The fix is the isIncludedDirectly half of that arm's check, not a reordering
// of the arms: the arm order carries #319's rule that main outranks the
// whitelist, and moving it would trade this bug for that one.
//
// Both halves are here because they are the two sides of docs/adr/0004's
// boundary and only having both shows where the line is. A "files" entry naming
// the path is consent and packs it; "main" naming it, with the whitelist silent
// on that path, is not consent and is refused with a warning.
// TestPackMainCannotDefeatDefaultExcludes carries the refusal at more depth.
func TestPackMainNamedByFilesFieldIsPacked(t *testing.T) {
	tests := []struct {
		name     string
		files    string
		want     []string
		wantWarn bool
	}{
		{
			name:     "files names the path main names",
			files:    `["index.js", ".env.example"]`,
			want:     []string{".env.example", "index.js", "package.json"},
			wantWarn: false,
		},
		{
			name:     "files is silent on the path main names",
			files:    `["index.js"]`,
			want:     []string{"index.js", "package.json"},
			wantWarn: true,
		},
		{
			name:     "files only contains the directory main names",
			files:    `["index.js", "cfg"]`,
			want:     []string{"cfg/keep.js", "index.js", "package.json"},
			wantWarn: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			main := ".env.example"
			tree := map[string]string{
				"index.js":     "module.exports = {}",
				".env.example": "API_KEY=",
			}
			if strings.Contains(tt.files, "cfg") {
				main = "cfg/.env.example"
				tree = map[string]string{
					"index.js":         "module.exports = {}",
					"cfg/keep.js":      "module.exports = {}",
					"cfg/.env.example": "API_KEY=",
				}
			}
			tree["package.json"] = `{
				"name": "main-named-by-files",
				"version": "1.0.0",
				"main": "` + main + `",
				"files": ` + tt.files + `
			}`
			writeMainEntryTree(t, tmpDir, tree)

			var packed []string
			out := capturePackStdout(t, func() {
				packed = packedRelPaths(t, tmpDir)
			})

			if strings.Join(packed, "\n") != strings.Join(tt.want, "\n") {
				t.Errorf("packed set mismatch: a \"files\" entry naming the path is "+
					"consent and \"main\" naming it is not\n got: %v\nwant: %v", packed, tt.want)
			}
			if got := strings.Contains(out, "warning:"); got != tt.wantWarn {
				t.Errorf("Pack() warned = %v, want %v for main %q with files %s\ngot stdout: %q",
					got, tt.wantWarn, main, tt.files, out)
			}
		})
	}
}

// TestPackNegationDoesNotOverrideInWhitelistMode pins the limit on #321's second
// override mechanism. An "!" negation re-includes a default-excluded path only
// in a package with no "files" field.
//
// It is not a gap. A "files" field turns the ignore chain off for the two arms
// that select ordinary paths, isIncluded and mainEntry — that is #318's rule
// that a "files" entry beats .npmignore and .gitignore — and defaultExcludes
// rides inside that same chain, so a negation has nowhere to speak for the paths
// they handle. The route for such a package is naming the path in "files", which
// the row below does as its control.
//
// The rule is limited to those two arms, and the limit is load-bearing: the
// third arm, isDefaultInclude, does still consult the chain, and a negation does
// reach through it. dist/.env is not a defaultIncludes match, so this fixture is
// unaffected — but a comment claiming no arm consults the chain would be false.
// TestPackNegationOverridesInWhitelistModeForDefaultIncludes has that case.
//
// Pinned because the two mechanisms read as interchangeable everywhere they are
// described, and nothing else asserts that they are not.
// TestPackNegationOverridesDefaultExcludes is scoped to a package with no "files"
// field and would stay green however whitelist mode behaved.
func TestPackNegationDoesNotOverrideInWhitelistMode(t *testing.T) {
	tests := []struct {
		name  string
		files string
		want  []string
	}{
		{
			name:  "negation alone does not re-include",
			files: `["dist"]`,
			want:  []string{"dist/a.js", "package.json"},
		},
		{
			name:  "naming the path in files does",
			files: `["dist", "dist/.env"]`,
			want:  []string{"dist/.env", "dist/a.js", "package.json"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			writeMainEntryTree(t, tmpDir, map[string]string{
				"package.json": `{
					"name": "negation-in-whitelist-mode",
					"version": "1.0.0",
					"files": ` + tt.files + `
				}`,
				".npmignore": "!dist/.env\n",
				"dist/a.js":  "module.exports = {}",
				"dist/.env":  "SECRET=hunter2",
			})

			assertPackedSet(t, tmpDir, tt.want,
				"an \"!\" negation says nothing under a \"files\" whitelist, because "+
					"the isIncluded and mainEntry arms do not consult the "+
					"ignore chain (#318)")
		})
	}
}

// TestPackNegationOverridesInWhitelistModeForDefaultIncludes is the exception to
// the test above, and the reason its rule had to be narrowed rather than merely
// stated. Two of collectFiles' three whitelist arms skip the ignore chain —
// isIncluded and mainEntry — but isDefaultInclude does not: it re-consults
// ignores.excludes, and defaultExcludes is seeded into that chain. So for the
// paths that arm handles, an "!" negation is the last match and wins, under a
// "files" whitelist.
//
// Which paths those are is the intersection of the two built-in lists, derived
// from the lists as they stand rather than from an example: a root file whose
// name matches a defaultIncludes stem (readme, license, licence, changelog,
// changes, history) and also matches one of the suffix patterns in
// defaultExcludes (*.log, *~, *.tgz, *.swp, *.swo). Run and confirmed over a
// candidate sweep — changes.log, README.md~, history.log, license.tgz,
// HISTORY.SWO and their case variants are all in both lists. package.json is the
// one defaultIncludes entry outside the intersection: no defaultExcludes pattern
// matches it, and it has an arm of its own regardless.
//
// This is a characterization test. It passed the first time it was run, which is
// the point — nothing here changes the isDefaultInclude arm, whose behaviour
// under a whitelist is #360's subject. It exists because the negation boundary
// was documented as "no arm consults the chain", which is false, and a rule
// stated in prose but asserted nowhere is how that got through review twice.
func TestPackNegationOverridesInWhitelistModeForDefaultIncludes(t *testing.T) {
	tests := []struct {
		name       string
		ignoreLine string
		want       []string
	}{
		{
			name:       "no negation",
			ignoreLine: "",
			want:       []string{"index.js", "package.json"},
		},
		{
			name:       "negation re-includes through the default-include arm",
			ignoreLine: "!changes.log\n",
			want:       []string{"changes.log", "index.js", "package.json"},
		},
		{
			name:       "same for an editor backup of a default include",
			ignoreLine: "!README.md~\n",
			want:       []string{"README.md~", "index.js", "package.json"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			writeMainEntryTree(t, tmpDir, map[string]string{
				"package.json": `{
					"name": "negation-vs-default-include",
					"version": "1.0.0",
					"files": ["index.js"]
				}`,
				".npmignore":  tt.ignoreLine,
				"index.js":    "module.exports = {}",
				"changes.log": "v1",
				"README.md~":  "# draft",
			})

			assertPackedSet(t, tmpDir, tt.want,
				"the isDefaultInclude arm still consults the ignore chain, so a "+
					"negation reaches a path in both built-in lists even under a "+
					"\"files\" whitelist")
		})
	}
}
