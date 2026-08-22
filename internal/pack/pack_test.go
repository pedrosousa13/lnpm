package pack

import (
	"os"
	"path/filepath"
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
// the package root rather than as an absolute path, and a trailing "/" as a
// directory marker, so the plain, anchored and "/**" spellings of "dist" all
// ship dist/cli/index.js.
//
// "dist/**/" is the exception that keeps the trailing-slash normalization
// honest: npm ships nothing from dist for it, because a trailing slash on a
// glob is not a directory marker. Each expectation here was verified against
// "npm pack --dry-run" on a fixture package.
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
		{"dist/**", true},
		{"/dist/**", true},
		{"dist/**/", false},
		{"lib", false},
		{"/lib", false},
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
// normalization, and "" starts empty. npm 11.16.0 ships the same file set for
// all three as it does for a package with no "files" field at all, so an empty
// normalized pattern includes everything.
//
// isExcluded already skips such a pattern (it neither excludes nor un-excludes
// anything), and the two functions must agree: neither may filter a path out on
// the strength of a degenerate entry.
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

	for _, pattern := range []string{"/", "//", ""} {
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

func TestIsDefaultInclude(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"package.json", true},
		{"README.md", true},
		{"readme.txt", true},
		{"LICENSE", true},
		{"license.md", true},
		{"CHANGELOG.md", true},
		{"src/index.ts", false},
		{"dist/index.js", false},
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

// TestPackDegenerateFilesEntryShipsEverything packs the same fixture once with
// no "files" field, as a baseline, then once for each degenerate spelling —
// ["/"], ["//"] and [""] — and requires every file set to equal the baseline.
// npm 11.16.0 produces the same tarball for all four; lnpm shipped only
// package.json and README.md for the degenerate spellings, dropping every file
// the whitelist named. Regression test for #227.
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

	for _, entry := range []string{"/", "//", ""} {
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

// TestIsExcludedDefaultExcludesCannotBeNegated proves a user "!" pattern cannot
// re-include a default-excluded path when the two lists are concatenated: the
// last matching pattern wins, so a default exclude always has the final say.
// collectFiles reaches the same answer by evaluating defaultExcludes on their
// own, where a user negation is not in the list to begin with.
func TestIsExcludedDefaultExcludesCannotBeNegated(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		userPats []string
	}{
		{"negated .env", ".env", []string{"!.env"}},
		{"negated node_modules file", "node_modules/foo", []string{"!node_modules/foo"}},
		{"negated node_modules dir", "node_modules", []string{"!node_modules"}},
		{"negated .npmrc", ".npmrc", []string{"!.npmrc"}},
		{"negated .git file", ".git/config", []string{"!.git/config"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patterns := append(append([]string{}, tt.userPats...), defaultExcludes...)
			if !isExcluded(tt.path, patterns) {
				t.Errorf("isExcluded(%q, user %v + defaults) = false, want true: a user negation must not re-include a default exclude", tt.path, tt.userPats)
			}
		})
	}
}

// TestPackDefaultExcludesWinInWhitelistMode proves default excludes still win
// when package.json has a "files" whitelist naming them and .npmignore tries to
// negate them: defaultExcludes are evaluated first and on their own, ahead of
// both the user's patterns and the whitelist.
func TestPackDefaultExcludesWinInWhitelistMode(t *testing.T) {
	tmpDir := t.TempDir()

	pkgJSON := `{
		"name": "whitelist-defaults",
		"version": "1.0.0",
		"files": ["dist", ".env", "node_modules"]
	}`
	if err := os.WriteFile(filepath.Join(tmpDir, "package.json"), []byte(pkgJSON), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, ".npmignore"), []byte("!.env\n!node_modules\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, ".env"), []byte("SECRET=1"), 0644); err != nil {
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
	for _, name := range []string{".env", "node_modules/dep/index.js"} {
		if packed[name] {
			t.Errorf("default-excluded %q was packed: a user negation must not re-include it", name)
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

// TestDefaultExcludesStillExclude pins what every defaultExcludes entry keeps
// out, one entry at a time. defaultExcludes is the security guard — it is what
// stops a publish shipping .env, .git or node_modules — and it runs through the
// same matcher as the user's ignore patterns, so a change to that matcher can
// widen a publish without any test of user-facing behavior noticing.
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
// against the guard growing a hole. The completeness check fails the build when
// a defaultExcludes entry is added with no case here, or with an empty one.
func TestDefaultExcludesStillExclude(t *testing.T) {
	// Each case names a defaultExcludes pattern and paths it must exclude on
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
	for _, pattern := range defaultExcludes {
		switch n, ok := covered[pattern]; {
		case !ok:
			t.Errorf("defaultExcludes entry %q has no case here: every default exclude needs one, or the guard can grow a hole unnoticed", pattern)
		case n == 0:
			t.Errorf("defaultExcludes entry %q is listed with no paths, so it asserts nothing", pattern)
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

	// .envrc survives the entire default list, not just the two .env entries.
	if isExcluded(".envrc", defaultExcludes) {
		t.Error("isExcluded(\".envrc\", defaultExcludes) = true, want false: README documents .envrc as published")
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
func TestDefaultExcludesMatchCaseInsensitively(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		// The names from the issue. Each is the same file as its lowercase form
		// on a case-insensitive filesystem.
		{".ENV", true},
		{".Env", true},
		{".Env.local", true},
		{".NPMRC", true},
		{"Node_Modules/evil/index.js", true},
		{"Node_Modules", true},

		// The lowercase forms keep working. Folding case must widen the guard,
		// never move it.
		{".env", true},
		{".env.local", true},
		{".npmrc", true},
		{"node_modules/dep/index.js", true},

		// Entries that carry uppercase in the list itself fold the same way, in
		// both directions.
		{"src/.ds_store", true},
		{"src/.DS_Store", true},
		{"thumbs.db", true},
		{"cvs/Root", true},
		{".GIT/config", true},
		{"DEBUG.LOG", true},
		{"pkg-1.0.0.TGZ", true},

		// Folding case must not turn a default exclude into a prefix. README
		// documents .envrc as published, and that has to hold however it is
		// typed.
		{".envrc", false},
		{".ENVRC", false},
		{"src/.EnvRc", false},
		{"ENV", false},
		{"Node_Modules_Backup/dep.js", false},
		{"LOGGER.JS", false},
		{"index.js", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := isDefaultExcluded(tt.path); got != tt.want {
				t.Errorf("isDefaultExcluded(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
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
// The two are different kinds of rule. defaultExcludes is a guard lnpm applies
// on the user's behalf, and a guard that can be stepped around by holding shift
// is not a guard. A user's ignore pattern is a preference, and git — the tool
// every one of these files was written for — matches it case-sensitively, so
// folding it here would silently drop files the author's own toolchain keeps.
//
// This test is what fails if the fold is applied to the shared matcher
// (applyIgnorePatterns or matchesIgnorePattern) rather than to the built-in set
// alone.
func TestUserIgnorePatternsStayCaseSensitive(t *testing.T) {
	if isExcluded("SECRET.TXT", []string{"secret.txt"}) {
		t.Error(`isExcluded("SECRET.TXT", ["secret.txt"]) = true, want false: a user ignore pattern matches case-sensitively, as it does in git`)
	}
	if isExcluded("Dist/app.js", []string{"dist/"}) {
		t.Error(`isExcluded("Dist/app.js", ["dist/"]) = true, want false: a user directory pattern matches case-sensitively too`)
	}

	tmpDir := t.TempDir()

	pkgJSON := `{
		"name": "case-sensitive-ignores",
		"version": "1.0.0"
	}`
	if err := os.WriteFile(filepath.Join(tmpDir, "package.json"), []byte(pkgJSON), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, ".npmignore"), []byte("secret.txt\n"), 0644); err != nil {
		t.Fatal(err)
	}

	files := map[string]string{
		"secret.txt": "dropped",
		"SECRET.TXT": "kept",
		"index.js":   "ok",
	}
	for rel, content := range files {
		if err := os.WriteFile(filepath.Join(tmpDir, rel), []byte(content), 0644); err != nil {
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

	if packed["secret.txt"] {
		t.Errorf("%q was packed but .npmignore names it, packed set was %v", "secret.txt", packed)
	}
	if !packed["SECRET.TXT"] {
		t.Errorf("%q was not packed: a user ignore pattern must not fold case, packed set was %v", "SECRET.TXT", packed)
	}
}
