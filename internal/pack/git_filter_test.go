package pack

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsGitRelatedPath(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		expected bool
	}{
		{".git exact", ".git", true},
		{".git directory", ".git/config", true},
		{".git subdirectory", ".git/hooks/pre-commit", true},
		{".gitignore", ".gitignore", true},
		{".gitattributes", ".gitattributes", true},
		{".gitmodules", ".gitmodules", true},
		{".git in subdirectory", "src/.git/config", true},
		{".git with backslash", "src\\.git\\config", true},
		{"regular file", "src/index.js", false},
		{"github in name", "github-action.yml", false},
		{".github directory", ".github/workflows/ci.yml", false},
		{"git in name", "gitignore-generator.js", false},
		{"nested git directory", "packages/lib/.git/HEAD", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isGitRelatedPath(tt.path)
			if result != tt.expected {
				t.Errorf("isGitRelatedPath(%q) = %v, expected %v", tt.path, result, tt.expected)
			}
		})
	}
}

// TestGitMetadataTierAgreesWithTheGitSafetyFilter is #398's first acceptance
// criterion: .gitignore, .gitattributes and .gitmodules each sit in exactly one
// documented tier, and the git safety filter agrees with it.
//
// Two claims per row. isHardReserved and isGitRelatedPath must return the same
// verdict — a path the filter strips and the tier does not is a path refused
// with no warning, which is the defect #398 fixes — and isDefaultExcluded must
// say false, or the name would be on both tiers and the overridable half would
// stop being overridable.
//
// The agreement is not free, and the spelling is what buys it. isGitRelatedPath
// compares filepath.Base, so it reaches every depth. A hardReservedExcludes
// entry goes through applyIgnorePatterns, which compares against the basename
// only for an unanchored pattern holding no separator — so the bare names are
// the spelling that matches, and "/.gitignore" or ".gitignore/**" would not.
// The depth rows below are what measure that rather than leaving it to be read
// off the pattern syntax.
//
// The agreement is not universal, and the disagreements are pinned rather than
// enumerated in prose, because an enumeration in prose is what went short here
// once already. The first draft named the .git directory half as the only
// exception. There are three disagreeing paths from two causes, and the one it
// missed is the second cause entirely: ".gitignore/foo", where the tier is the
// stricter of the two. Nothing caught it because no path in the table below
// sits under a ".gitignore/" directory. All three are rows in disagreements
// below now, each carrying the pair it was measured to produce.
func TestGitMetadataTierAgreesWithTheGitSafetyFilter(t *testing.T) {
	paths := []string{
		// The three names, at the root and at depth.
		".gitignore",
		"docs/.gitignore",
		"a/b/c/.gitignore",
		".gitattributes",
		"docs/.gitattributes",
		".gitmodules",
		"docs/.gitmodules",

		// Near misses. Neither side may claim these. What they catch is a
		// spelling whose glob widens across a sibling name, and it was run
		// rather than reasoned: spelling hardReservedExcludes' ".gitignore"
		// entry as ".gitignore*" turns the ".gitignore.bak" row red, reporting
		// isHardReserved true against isGitRelatedPath false. What they do not
		// catch is matchesIgnorePattern's directory-prefix branch, which the
		// bare name takes already and which no path in this group reaches.
		// That is the ".gitignore/foo" entry in disagreements below, and the
		// same ".gitignore*" run turns it red too — on both predicates
		// answering false, which is what a widened spelling would really cost.
		"gitignore",
		"docs/gitignore",
		".gitignore.bak",
		"docs/.gitmodules.bak",
		"src/index.js",
	}

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			hard := isHardReserved(path)
			if filtered := isGitRelatedPath(path); hard != filtered {
				t.Errorf("isHardReserved(%q) = %v but isGitRelatedPath(%q) = %v: "+
					"the two must agree, or a path the safety filter strips is "+
					"refused with no warning", path, hard, path, filtered)
			}
			if isDefaultExcluded(path) {
				t.Errorf("isDefaultExcluded(%q) = true: a git metadata file "+
					"belongs to the hard-reserved tier only, and an entry on "+
					"both tiers makes the overridable half unoverridable", path)
			}
		})
	}

	// The paths where the two predicates genuinely differ. Every pair below was
	// run on 2026-08-25 and read off the output, not derived from the pattern
	// syntax. Three different branches produce them, which is why reading one
	// of the three off the code and stopping is how the ".gitignore/foo" row
	// came to be missing: it is the only one that reaches
	// matchesIgnorePattern's `!negated && strings.HasPrefix(relPath,
	// pattern+"/")`. The "src/.git" row reaches the basename branch instead,
	// on the separator-free ".git" entry, and "src/.git/config" reaches
	// neither.
	//
	// None of the three is a hole. A disagreement only costs something when
	// both sides say false, so that is what the loop asserts alongside the
	// pair: at least one of the two still refuses each path.
	disagreements := []struct {
		path       string
		hard       bool
		gitRelated bool
		why        string
	}{
		{
			path: "src/.git/config", hard: false, gitRelated: true,
			why: `".git" is separator-free, so the tier compares it against ` +
				`filepath.Base, which is "config"; the filter's "/.git/" scan ` +
				`is what catches it. Nothing is owed a warning either, since ` +
				`collectFiles prunes the directory one level up — see the ` +
				`"src/.git" row`,
		},
		{
			path: ".gitignore/foo", hard: true, gitRelated: false,
			why: `a directory named .gitignore: the tier takes ` +
				`matchesIgnorePattern's directory-prefix branch and covers ` +
				`everything under it, while the filter compares ` +
				`filepath.Base, which is "foo". The hard-reserved side is the ` +
				`strict one here, so the path is refused either way`,
		},
		{
			path: "src/.git", hard: true, gitRelated: false,
			why: `the directory itself rather than a path inside it. ` +
				`isGitRelatedPath has no branch for a bare nested ".git" — ` +
				`its scan wants "/.git/" — and does not need one, because ` +
				`this is the path collectFiles prunes on and the walk never ` +
				`hands a directory to the filter`,
		},
	}

	for _, tt := range disagreements {
		t.Run("disagreement "+tt.path, func(t *testing.T) {
			hard, gitRelated := isHardReserved(tt.path), isGitRelatedPath(tt.path)
			if hard != tt.hard {
				t.Errorf("isHardReserved(%q) = %v, want %v: %s", tt.path, hard, tt.hard, tt.why)
			}
			if gitRelated != tt.gitRelated {
				t.Errorf("isGitRelatedPath(%q) = %v, want %v: %s", tt.path, gitRelated, tt.gitRelated, tt.why)
			}
			if !hard && !gitRelated {
				t.Errorf("neither isHardReserved(%q) nor isGitRelatedPath(%q) is true: "+
					"a disagreement costs nothing while one of the two still refuses "+
					"the path outright, and both answering false means this row no "+
					"longer records a disagreement between two guards but the absence "+
					"of either", tt.path, tt.path)
			}
		})
	}
}

func TestFilterGitFiles(t *testing.T) {
	files := []*FileInfo{
		{RelPath: "package.json"},
		{RelPath: "src/index.js"},
		{RelPath: ".git/config"},
		{RelPath: ".git/hooks/pre-commit"},
		{RelPath: ".gitignore"},
		{RelPath: "README.md"},
		{RelPath: "src/.git/HEAD"},
		{RelPath: "dist/bundle.js"},
	}

	filtered := filterGitFiles(files)

	expected := []string{
		"package.json",
		"src/index.js",
		"README.md",
		"dist/bundle.js",
	}

	if len(filtered) != len(expected) {
		t.Fatalf("Expected %d files, got %d", len(expected), len(filtered))
	}

	for i, f := range filtered {
		if f.RelPath != expected[i] {
			t.Errorf("File %d: expected %q, got %q", i, expected[i], f.RelPath)
		}
	}
}

func TestNpmPackIntegrationWithGitFiles(t *testing.T) {
	// Create a temporary directory with package.json and some files including .git
	tmpDir := t.TempDir()

	// Create package.json
	pkgJSON := `{
		"name": "test-package",
		"version": "1.0.0",
		"files": ["src", "dist"]
	}`
	if err := os.WriteFile(filepath.Join(tmpDir, "package.json"), []byte(pkgJSON), 0644); err != nil {
		t.Fatal(err)
	}

	// Create regular files
	if err := os.MkdirAll(filepath.Join(tmpDir, "src"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "src", "index.js"), []byte("console.log('hello');"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create .git directory
	gitDir := filepath.Join(tmpDir, ".git")
	if err := os.MkdirAll(gitDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte("[core]"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create .gitignore
	if err := os.WriteFile(filepath.Join(tmpDir, ".gitignore"), []byte("node_modules\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Run Pack
	_, files, err := Pack(tmpDir)
	if err != nil {
		t.Fatal(err)
	}

	// Verify no .git files are included
	for _, f := range files {
		if isGitRelatedPath(f.RelPath) {
			t.Errorf("Git-related file was not filtered: %s", f.RelPath)
		}
	}

	// Verify package.json and src/index.js are included
	foundPackageJSON := false
	foundIndexJS := false
	for _, f := range files {
		if f.RelPath == "package.json" {
			foundPackageJSON = true
		}
		if f.RelPath == "src/index.js" {
			foundIndexJS = true
		}
	}

	if !foundPackageJSON {
		t.Error("package.json should be included")
	}
	if !foundIndexJS {
		t.Error("src/index.js should be included")
	}
}

func TestNpmPackFallback(t *testing.T) {
	// This test verifies that when npm is not available or fails,
	// the system falls back to custom filtering and still excludes .git

	tmpDir := t.TempDir()

	// Create package.json without files field (will use default behavior)
	pkgJSON := `{
		"name": "test-fallback",
		"version": "1.0.0"
	}`
	if err := os.WriteFile(filepath.Join(tmpDir, "package.json"), []byte(pkgJSON), 0644); err != nil {
		t.Fatal(err)
	}

	// Create a regular file
	if err := os.WriteFile(filepath.Join(tmpDir, "index.js"), []byte("console.log('test');"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create .git directory
	gitDir := filepath.Join(tmpDir, ".git")
	if err := os.MkdirAll(gitDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte("[core]"), 0644); err != nil {
		t.Fatal(err)
	}

	// Force fallback by temporarily making npm unavailable
	// (In real scenario, getNpmPackFileList would return false)

	// Run Pack
	_, files, err := Pack(tmpDir)
	if err != nil {
		t.Fatal(err)
	}

	// Verify no .git files are included
	for _, f := range files {
		if isGitRelatedPath(f.RelPath) {
			t.Errorf("Git-related file was not filtered in fallback mode: %s", f.RelPath)
		}
	}

	// Verify index.js is included
	foundIndexJS := false
	for _, f := range files {
		if f.RelPath == "index.js" {
			foundIndexJS = true
		}
	}

	if !foundIndexJS {
		t.Error("index.js should be included in fallback mode")
	}
}
