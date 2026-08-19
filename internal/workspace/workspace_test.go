package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

// writePackage creates a directory under root containing a minimal package.json
func writePackage(t *testing.T, root, relPath string) string {
	t.Helper()

	pkgDir := filepath.Join(root, filepath.FromSlash(relPath))
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		t.Fatalf("Failed to create package dir %s: %v", pkgDir, err)
	}

	name := filepath.Base(pkgDir)
	contents := `{"name":"` + name + `","version":"1.0.0"}`
	if err := os.WriteFile(filepath.Join(pkgDir, "package.json"), []byte(contents), 0644); err != nil {
		t.Fatalf("Failed to write package.json in %s: %v", pkgDir, err)
	}

	return pkgDir
}

// assertPackages compares expanded package paths against the expected list, order included
func assertPackages(t *testing.T, got, want []string) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("Expected %d packages %v, got %d: %v", len(want), want, len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Expected package %d to be %s, got %s", i, want[i], got[i])
		}
	}
}

func TestExpandGlobsExcludesNegatedPackage(t *testing.T) {
	root := t.TempDir()
	pkgA := writePackage(t, root, "packages/package-a")
	writePackage(t, root, "packages/package-b")

	packages, err := expandGlobs(root, []string{"packages/*", "!packages/package-b"})
	if err != nil {
		t.Fatalf("Failed to expand globs: %v", err)
	}

	assertPackages(t, packages, []string{pkgA})
}

func TestExpandGlobsNegationMatchingNothingKeepsAllPackages(t *testing.T) {
	root := t.TempDir()
	pkgA := writePackage(t, root, "packages/package-a")
	pkgB := writePackage(t, root, "packages/package-b")

	packages, err := expandGlobs(root, []string{"packages/*", "!packages/does-not-exist"})
	if err != nil {
		t.Fatalf("Failed to expand globs: %v", err)
	}

	assertPackages(t, packages, []string{pkgA, pkgB})
}

func TestExpandGlobsNegationGlobExcludesEveryMatch(t *testing.T) {
	root := t.TempDir()
	pkgPublic := writePackage(t, root, "packages/public-api")
	writePackage(t, root, "packages/internal-secret")
	writePackage(t, root, "packages/internal-tools")

	packages, err := expandGlobs(root, []string{"packages/*", "!packages/internal-*"})
	if err != nil {
		t.Fatalf("Failed to expand globs: %v", err)
	}

	assertPackages(t, packages, []string{pkgPublic})
}

func TestExpandGlobsWithoutNegationPreservesOrderAndDedup(t *testing.T) {
	root := t.TempDir()
	pkgB := writePackage(t, root, "packages/package-b")
	pkgA := writePackage(t, root, "packages/package-a")

	// package-b is listed explicitly first, then matched again by the wildcard
	packages, err := expandGlobs(root, []string{"packages/package-b", "packages/*"})
	if err != nil {
		t.Fatalf("Failed to expand globs: %v", err)
	}

	assertPackages(t, packages, []string{pkgB, pkgA})
}

func TestExpandGlobsSkipsDirectoriesWithoutPackageJSON(t *testing.T) {
	root := t.TempDir()
	pkgA := writePackage(t, root, "packages/package-a")
	if err := os.MkdirAll(filepath.Join(root, "packages", "not-a-package"), 0755); err != nil {
		t.Fatalf("Failed to create directory: %v", err)
	}

	packages, err := expandGlobs(root, []string{"packages/*"})
	if err != nil {
		t.Fatalf("Failed to expand globs: %v", err)
	}

	assertPackages(t, packages, []string{pkgA})
}

func TestDetectPnpmWorkspaceExcludesNegatedPackage(t *testing.T) {
	root := t.TempDir()
	pkgA := writePackage(t, root, "packages/package-a")
	writePackage(t, root, "packages/package-b")

	yaml := "packages:\n  - 'packages/*'\n  - '!packages/package-b'\n"
	if err := os.WriteFile(filepath.Join(root, "pnpm-workspace.yaml"), []byte(yaml), 0644); err != nil {
		t.Fatalf("Failed to write pnpm-workspace.yaml: %v", err)
	}

	ws, err := Detect(root)
	if err != nil {
		t.Fatalf("Failed to detect workspace: %v", err)
	}
	if ws == nil {
		t.Fatal("Expected workspace, got nil")
	}
	if ws.Type != "pnpm" {
		t.Errorf("Expected pnpm, got %s", ws.Type)
	}

	assertPackages(t, ws.Packages, []string{pkgA})
}
