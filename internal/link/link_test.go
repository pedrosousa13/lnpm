package link

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/pack"
)

func TestLinkAndUnlink(t *testing.T) {
	// Create temp directories for store and project
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "store", "my-package", "abc123")
	projectPath := filepath.Join(tmpDir, "project")

	if err := os.MkdirAll(storePath, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(projectPath, 0755); err != nil {
		t.Fatal(err)
	}

	// Create files in store
	files := []*pack.FileInfo{
		{RelPath: "package.json", Size: 100, Mode: 0644},
		{RelPath: "dist/index.js", Size: 200, Mode: 0644},
		{RelPath: "dist/utils.js", Size: 150, Mode: 0644},
	}

	// Create actual files in store
	for _, f := range files {
		filePath := filepath.Join(storePath, f.RelPath)
		if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filePath, []byte("content"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// Create linker and link package
	linker := New(projectPath)
	linkType, err := linker.Link("my-package", storePath, files)
	if err != nil {
		t.Fatalf("Link() error: %v", err)
	}

	// Verify link type (should be hardlink on same filesystem)
	if linkType != HardLink && linkType != Copy {
		t.Errorf("linkType = %q, want hardlink or copy", linkType)
	}

	// Verify .lnpm directory created
	lnpmPath := filepath.Join(projectPath, ".lnpm", "my-package")
	if _, err := os.Stat(lnpmPath); err != nil {
		t.Errorf(".lnpm/my-package not created: %v", err)
	}

	// Verify files exist in .lnpm
	for _, f := range files {
		linkedFile := filepath.Join(lnpmPath, f.RelPath)
		if _, err := os.Stat(linkedFile); err != nil {
			t.Errorf("linked file %s not found: %v", f.RelPath, err)
		}
	}

	// Verify node_modules symlink created
	nodeModulesPath := filepath.Join(projectPath, "node_modules", "my-package")
	info, err := os.Lstat(nodeModulesPath)
	if err != nil {
		t.Fatalf("node_modules/my-package not found: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Error("node_modules/my-package is not a symlink")
	}

	// Verify symlink points to correct location
	target, err := os.Readlink(nodeModulesPath)
	if err != nil {
		t.Fatalf("failed to read symlink: %v", err)
	}
	if runtime.GOOS == "windows" {
		// Junctions use absolute paths on Windows
		expectedAbs := filepath.Join(projectPath, ".lnpm", "my-package")
		if target != expectedAbs {
			t.Errorf("symlink target = %q, want %q", target, expectedAbs)
		}
	} else {
		expectedTarget := filepath.Join("..", ".lnpm", "my-package")
		if target != expectedTarget {
			t.Errorf("symlink target = %q, want %q", target, expectedTarget)
		}
	}

	// Test IsLinked
	if !linker.IsLinked("my-package") {
		t.Error("IsLinked(my-package) = false, want true")
	}
	if linker.IsLinked("other-package") {
		t.Error("IsLinked(other-package) = true, want false")
	}

	// Test ListLinked
	linked, err := linker.ListLinked()
	if err != nil {
		t.Fatalf("ListLinked() error: %v", err)
	}
	if len(linked) != 1 || linked[0] != "my-package" {
		t.Errorf("ListLinked() = %v, want [my-package]", linked)
	}

	// Test Unlink
	if err := linker.Unlink("my-package"); err != nil {
		t.Fatalf("Unlink() error: %v", err)
	}

	// Verify .lnpm directory removed
	if _, err := os.Stat(lnpmPath); !os.IsNotExist(err) {
		t.Error(".lnpm/my-package still exists after unlink")
	}

	// Verify node_modules symlink removed
	if _, err := os.Stat(nodeModulesPath); !os.IsNotExist(err) {
		t.Error("node_modules/my-package still exists after unlink")
	}

	// Verify IsLinked returns false
	if linker.IsLinked("my-package") {
		t.Error("IsLinked(my-package) = true after unlink, want false")
	}
}

func TestLinkScopedPackage(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "store", "@org", "my-package", "abc123")
	projectPath := filepath.Join(tmpDir, "project")

	if err := os.MkdirAll(storePath, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(projectPath, 0755); err != nil {
		t.Fatal(err)
	}

	// Create package.json in store
	pkgJSONPath := filepath.Join(storePath, "package.json")
	if err := os.WriteFile(pkgJSONPath, []byte(`{"name": "@org/my-package"}`), 0644); err != nil {
		t.Fatal(err)
	}

	files := []*pack.FileInfo{
		{RelPath: "package.json", Size: 100, Mode: 0644},
	}

	linker := New(projectPath)
	_, err := linker.Link("@org/my-package", storePath, files)
	if err != nil {
		t.Fatalf("Link() error for scoped package: %v", err)
	}

	// Verify scoped .lnpm directory
	lnpmPath := filepath.Join(projectPath, ".lnpm", "@org/my-package")
	if _, err := os.Stat(lnpmPath); err != nil {
		t.Errorf(".lnpm/@org/my-package not created: %v", err)
	}

	// Verify scoped node_modules symlink
	nodeModulesPath := filepath.Join(projectPath, "node_modules", "@org", "my-package")
	info, err := os.Lstat(nodeModulesPath)
	if err != nil {
		t.Fatalf("node_modules/@org/my-package not found: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Error("node_modules/@org/my-package is not a symlink")
	}

	// Verify symlink points to correct location (scoped packages need ../../)
	target, err := os.Readlink(nodeModulesPath)
	if err != nil {
		t.Fatalf("failed to read symlink: %v", err)
	}
	if runtime.GOOS == "windows" {
		expectedAbs := filepath.Join(projectPath, ".lnpm", "@org", "my-package")
		if target != expectedAbs {
			t.Errorf("symlink target = %q, want %q", target, expectedAbs)
		}
	} else {
		expectedTarget := filepath.Join("..", "..", ".lnpm", "@org", "my-package")
		if target != expectedTarget {
			t.Errorf("symlink target = %q, want %q", target, expectedTarget)
		}
	}
}

func TestLinkMultiplePackages(t *testing.T) {
	tmpDir := t.TempDir()
	projectPath := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(projectPath, 0755); err != nil {
		t.Fatal(err)
	}

	packages := []string{"pkg-a", "pkg-b", "pkg-c"}
	linker := New(projectPath)

	// Link multiple packages
	for _, pkgName := range packages {
		storePath := filepath.Join(tmpDir, "store", pkgName, "hash")
		if err := os.MkdirAll(storePath, 0755); err != nil {
			t.Fatal(err)
		}

		// Create package.json
		pkgJSONPath := filepath.Join(storePath, "package.json")
		if err := os.WriteFile(pkgJSONPath, []byte(`{}`), 0644); err != nil {
			t.Fatal(err)
		}

		files := []*pack.FileInfo{
			{RelPath: "package.json", Size: 2, Mode: 0644},
		}

		if _, err := linker.Link(pkgName, storePath, files); err != nil {
			t.Fatalf("Link(%s) error: %v", pkgName, err)
		}
	}

	// Verify all packages linked
	linked, err := linker.ListLinked()
	if err != nil {
		t.Fatalf("ListLinked() error: %v", err)
	}
	if len(linked) != 3 {
		t.Errorf("ListLinked() returned %d packages, want 3", len(linked))
	}

	for _, pkgName := range packages {
		if !linker.IsLinked(pkgName) {
			t.Errorf("IsLinked(%s) = false, want true", pkgName)
		}
	}

	// Unlink one package
	if err := linker.Unlink("pkg-b"); err != nil {
		t.Fatalf("Unlink(pkg-b) error: %v", err)
	}

	// Verify only pkg-b unlinked
	if linker.IsLinked("pkg-b") {
		t.Error("IsLinked(pkg-b) = true after unlink, want false")
	}
	if !linker.IsLinked("pkg-a") {
		t.Error("IsLinked(pkg-a) = false, want true")
	}
	if !linker.IsLinked("pkg-c") {
		t.Error("IsLinked(pkg-c) = false, want true")
	}

	// Verify .lnpm directory still exists (has other packages)
	lnpmDir := filepath.Join(projectPath, ".lnpm")
	if _, err := os.Stat(lnpmDir); err != nil {
		t.Error(".lnpm directory removed while packages still linked")
	}
}

func TestCopyFile(t *testing.T) {
	tmpDir := t.TempDir()

	srcPath := filepath.Join(tmpDir, "source.txt")
	dstPath := filepath.Join(tmpDir, "dest.txt")

	content := "test content for copy"
	if err := os.WriteFile(srcPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	if err := copyFile(srcPath, dstPath); err != nil {
		t.Fatalf("copyFile() error: %v", err)
	}

	// Verify content
	data, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatalf("failed to read copied file: %v", err)
	}
	if string(data) != content {
		t.Errorf("copied content = %q, want %q", string(data), content)
	}
}
