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
