package link

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/pack"
)

// writeStoreFiles creates files with the given relpath -> content mapping under
// storePath and returns the matching pack.FileInfo slice.
func writeStoreFiles(t *testing.T, storePath string, contents map[string]string) []*pack.FileInfo {
	t.Helper()

	var files []*pack.FileInfo
	for relPath, content := range contents {
		filePath := filepath.Join(storePath, filepath.FromSlash(relPath))
		if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		files = append(files, &pack.FileInfo{
			Path:    filePath,
			RelPath: relPath,
			Size:    int64(len(content)),
			Mode:    0644,
		})
	}
	return files
}

// assertNoTempDirs fails if any entry under dir has a dot-prefixed name, which
// is how the atomic relink names its in-progress temp directories.
func assertNoTempDirs(t *testing.T, dir string) {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("failed to read %s: %v", dir, err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			t.Errorf("leftover temp entry %q in %s", entry.Name(), dir)
		}
	}
}

// entryNames returns the names of the entries in dir.
func entryNames(t *testing.T, dir string) []string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("failed to read %s: %v", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

// TestLink_FailedRelinkLeavesPreviousPackageIntact asserts that when a relink
// fails part way through populating the package, the previously linked package
// is still complete and no temp directory survives.
func TestLink_FailedRelinkLeavesPreviousPackageIntact(t *testing.T) {
	tests := []struct {
		name        string
		packageName string
	}{
		{name: "unscoped", packageName: "my-package"},
		{name: "scoped", packageName: "@org/my-package"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			projectPath := filepath.Join(tmpDir, "project")
			if err := os.MkdirAll(projectPath, 0755); err != nil {
				t.Fatal(err)
			}

			// First link: a complete, good package.
			goodStore := filepath.Join(tmpDir, "store", "good")
			original := map[string]string{
				"package.json":  `{"name":"my-package","version":"1.0.0"}`,
				"dist/index.js": "module.exports = 1;\n",
				"dist/utils.js": "module.exports = 2;\n",
			}
			goodFiles := writeStoreFiles(t, goodStore, original)

			linker := New(projectPath)
			if _, err := linker.Link(tt.packageName, goodStore, goodFiles); err != nil {
				t.Fatalf("first Link() error: %v", err)
			}

			// Second link: the store is missing one of the declared files, so
			// population fails part way through.
			badStore := filepath.Join(tmpDir, "store", "bad")
			badFiles := writeStoreFiles(t, badStore, map[string]string{
				"package.json": `{"name":"my-package","version":"2.0.0"}`,
			})
			badFiles = append(badFiles, &pack.FileInfo{
				Path:    filepath.Join(badStore, "dist", "missing.js"),
				RelPath: "dist/missing.js",
				Size:    10,
				Mode:    0644,
			})

			if _, err := linker.Link(tt.packageName, badStore, badFiles); err == nil {
				t.Fatal("second Link() succeeded, want error: the failure injection did not reach the error path")
			}

			// The previously linked package must still be complete.
			lnpmPath := filepath.Join(projectPath, ".lnpm", filepath.FromSlash(tt.packageName))
			for relPath, want := range original {
				got, err := os.ReadFile(filepath.Join(lnpmPath, filepath.FromSlash(relPath)))
				if err != nil {
					t.Errorf("previously linked file %s missing after failed relink: %v", relPath, err)
					continue
				}
				if string(got) != want {
					t.Errorf("previously linked file %s = %q, want %q", relPath, string(got), want)
				}
			}

			// No half-written file from the failed attempt leaked in.
			if _, err := os.Stat(filepath.Join(lnpmPath, "dist", "missing.js")); !os.IsNotExist(err) {
				t.Errorf("failed relink leaked dist/missing.js into %s", lnpmPath)
			}

			// No temp directory survives, at the .lnpm root or in the scope dir.
			assertNoTempDirs(t, filepath.Join(projectPath, ".lnpm"))
			assertNoTempDirs(t, filepath.Dir(lnpmPath))
		})
	}
}

// TestLink_NoTempDirsLeftBehind asserts a successful link leaves .lnpm/ holding
// exactly the package and nothing else.
func TestLink_NoTempDirsLeftBehind(t *testing.T) {
	tmpDir := t.TempDir()
	projectPath := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(projectPath, 0755); err != nil {
		t.Fatal(err)
	}

	storePath := filepath.Join(tmpDir, "store", "my-package")
	files := writeStoreFiles(t, storePath, map[string]string{
		"package.json":  `{"name":"my-package"}`,
		"dist/index.js": "module.exports = 1;\n",
	})

	linker := New(projectPath)
	// Link twice: the second call goes through the replace path.
	for i := 0; i < 2; i++ {
		if _, err := linker.Link("my-package", storePath, files); err != nil {
			t.Fatalf("Link() attempt %d error: %v", i+1, err)
		}
	}

	lnpmDir := filepath.Join(projectPath, ".lnpm")
	names := entryNames(t, lnpmDir)
	if len(names) != 1 || names[0] != "my-package" {
		t.Errorf(".lnpm entries = %v, want [my-package]", names)
	}
}

// TestUnlinkKeepsScopeHoldingATempDirectory pins removeDirIfEmpty's literal
// reading of emptiness: a dot-prefixed entry is not a package, but it still
// counts when Unlink decides whether a scope directory is empty. Deleting a
// scope that a live relink is writing into would destroy that relink's work.
//
// What it catches, measured: filtering dot-prefixed entries out of the count
// leaves this green, because os.Remove refuses a directory that still holds one.
// It goes red - on both assertions below - only when that filtered count is
// paired with os.RemoveAll. So what it pins is os.Remove's refusal rather than
// the count; removeDirIfEmpty's comment says why the literal count is kept
// anyway.
func TestUnlinkKeepsScopeHoldingATempDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	projectPath := filepath.Join(tmpDir, "project")

	linker := New(projectPath)
	linkPackage(t, linker, filepath.Join(tmpDir, "store"), "@org/pkg-a")

	// Stand in for a concurrent relink of @org/pkg-b, whose temp directory is
	// created as a sibling of its target inside the scope directory.
	tempDir := filepath.Join(projectPath, ".lnpm", "@org", ".tmp-inflight")
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		t.Fatal(err)
	}

	if err := linker.Unlink("@org/pkg-a"); err != nil {
		t.Fatalf("Unlink() error: %v", err)
	}

	assertExists(t, tempDir)
	assertExists(t, filepath.Join(projectPath, ".lnpm", "@org"))
}
