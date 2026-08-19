package pack

import (
	"os"
	"path/filepath"
	"testing"
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
// re-include a default-excluded path. collectFiles appends defaultExcludes
// after the user's patterns, and the last matching pattern wins, so a default
// exclude always has the final say.
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
// when package.json has a "files" whitelist and .npmignore tries to negate
// them: excludes are evaluated before the whitelist, and defaultExcludes are
// appended last so they have the final say.
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

	hash1, err := hashFile(file1)
	if err != nil {
		t.Fatalf("hashFile(file1) error: %v", err)
	}
	hash2, err := hashFile(file2)
	if err != nil {
		t.Fatalf("hashFile(file2) error: %v", err)
	}
	hash3, err := hashFile(file3)
	if err != nil {
		t.Fatalf("hashFile(file3) error: %v", err)
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
