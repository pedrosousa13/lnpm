package workspace

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

func TestExpandGlobsMalformedNegationFailsAndKeepsNegatedPackageOut(t *testing.T) {
	root := t.TempDir()
	writePackage(t, root, "packages/public-api")
	writePackage(t, root, "packages/internal-secret")

	packages, err := expandGlobs(root, []string{"packages/*", "!packages/[internal"})
	if err == nil {
		t.Fatalf("Expected an error for the malformed negation, got nil and packages %v", packages)
	}
	if !strings.Contains(err.Error(), "packages/[internal") {
		t.Errorf("Expected the error to name the offending pattern, got: %v", err)
	}
	if len(packages) != 0 {
		t.Errorf("Expected no packages alongside the error, got %v", packages)
	}
}

func TestExpandGlobsMalformedIncludeFails(t *testing.T) {
	root := t.TempDir()
	writePackage(t, root, "packages/public-api")

	packages, err := expandGlobs(root, []string{"packages/[bad"})
	if err == nil {
		t.Fatalf("Expected an error for the malformed include, got nil and packages %v", packages)
	}
	if !strings.Contains(err.Error(), "packages/[bad") {
		t.Errorf("Expected the error to name the offending pattern, got: %v", err)
	}
}

func TestDetectPackageJSONMalformedPatternReturnsError(t *testing.T) {
	root := t.TempDir()
	writePackage(t, root, "packages/public-api")
	writePackage(t, root, "packages/internal-secret")

	manifest := `{"name":"root","workspaces":["packages/*","!packages/[internal"]}`
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(manifest), 0644); err != nil {
		t.Fatalf("Failed to write package.json: %v", err)
	}

	ws, err := Detect(root)
	if err == nil {
		t.Fatalf("Expected an error for the malformed pattern, got nil and workspace %+v", ws)
	}
	if !strings.Contains(err.Error(), "packages/[internal") {
		t.Errorf("Expected the error to name the offending pattern, got: %v", err)
	}
	if ws != nil {
		t.Errorf("Expected no workspace alongside the error, got %+v", ws)
	}
}

func TestDetectPNPMWorkspaceMalformedPatternReturnsError(t *testing.T) {
	root := t.TempDir()
	writePackage(t, root, "packages/public-api")
	writePackage(t, root, "packages/internal-secret")

	yaml := "packages:\n  - 'packages/*'\n  - '!packages/[internal'\n"
	if err := os.WriteFile(filepath.Join(root, "pnpm-workspace.yaml"), []byte(yaml), 0644); err != nil {
		t.Fatalf("Failed to write pnpm-workspace.yaml: %v", err)
	}

	ws, err := Detect(root)
	if err == nil {
		t.Fatalf("Expected an error for the malformed pattern, got nil and workspace %+v", ws)
	}
	if !strings.Contains(err.Error(), "packages/[internal") {
		t.Errorf("Expected the error to name the offending pattern, got: %v", err)
	}
	if ws != nil {
		t.Errorf("Expected no workspace alongside the error, got %+v", ws)
	}
}

func TestDetectPNPMWorkspaceExcludesNegatedPackage(t *testing.T) {
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

// --- ListPackages -----------------------------------------------------------
//
// Every path in w.Packages had a package.json when expandGlobs filtered on it,
// so a member that will not read, will not parse, or names no package is a
// broken member of a workspace the caller asked for, not a directory that
// happens not to be a package. docs/adr/0001 makes that an abort.

// requirePermissionEnforcement skips tests that make a file unreadable with
// chmod. Windows models only a read-only bit and root ignores permission bits
// entirely, so neither can produce the failure these tests depend on.
func requirePermissionEnforcement(t *testing.T) {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("Windows reports only a read-only bit, not Unix permission bits")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root bypasses the permission checks this test relies on")
	}
}

func TestListPackagesReturnsEveryWellFormedMember(t *testing.T) {
	root := t.TempDir()
	pkgA := writePackage(t, root, "packages/package-a")
	pkgB := writePackage(t, root, "packages/package-b")

	ws := &Workspace{Root: root, Type: "npm", Packages: []string{pkgA, pkgB}}
	packages, err := ws.ListPackages()
	if err != nil {
		t.Fatalf("Failed to list packages: %v", err)
	}

	if len(packages) != 2 {
		t.Fatalf("Expected 2 packages, got %d: %v", len(packages), packages)
	}
	for i, want := range []struct{ name, path string }{
		{"package-a", pkgA},
		{"package-b", pkgB},
	} {
		if packages[i].Name != want.name || packages[i].Path != want.path {
			t.Errorf("Expected package %d to be %s at %s, got %s at %s",
				i, want.name, want.path, packages[i].Name, packages[i].Path)
		}
		if packages[i].Version != "1.0.0" {
			t.Errorf("Expected package %d version 1.0.0, got %s", i, packages[i].Version)
		}
	}
}

func TestListPackagesUnparseableMemberFails(t *testing.T) {
	root := t.TempDir()
	pkgA := writePackage(t, root, "packages/package-a")
	pkgB := writePackage(t, root, "packages/package-b")

	broken := filepath.Join(pkgB, "package.json")
	if err := os.WriteFile(broken, []byte(`{"name":"package-b",}`), 0644); err != nil {
		t.Fatalf("Failed to write malformed package.json: %v", err)
	}

	ws := &Workspace{Root: root, Type: "npm", Packages: []string{pkgA, pkgB}}
	packages, err := ws.ListPackages()
	if err == nil {
		t.Fatalf("Expected an error for the unparseable member, got nil and packages %v", packages)
	}
	if !strings.Contains(err.Error(), broken) {
		t.Errorf("Expected the error to name %s, got: %v", broken, err)
	}
	// The doc comment promises the underlying error is wrapped, not just
	// described, so a caller can still reach the syntax error underneath.
	var syntax *json.SyntaxError
	if !errors.As(err, &syntax) {
		t.Errorf("Expected the error to wrap a *json.SyntaxError, got: %v", err)
	}
	if len(packages) != 0 {
		t.Errorf("Expected no packages alongside the error, got %v", packages)
	}
}

func TestListPackagesUnreadableMemberFails(t *testing.T) {
	requirePermissionEnforcement(t)

	root := t.TempDir()
	pkgA := writePackage(t, root, "packages/package-a")
	pkgB := writePackage(t, root, "packages/package-b")

	unreadable := filepath.Join(pkgB, "package.json")
	if err := os.Chmod(unreadable, 0000); err != nil {
		t.Fatalf("Failed to chmod package.json: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(unreadable, 0644) })

	ws := &Workspace{Root: root, Type: "npm", Packages: []string{pkgA, pkgB}}
	packages, err := ws.ListPackages()
	if err == nil {
		t.Fatalf("Expected an error for the unreadable member, got nil and packages %v", packages)
	}
	if !strings.Contains(err.Error(), unreadable) {
		t.Errorf("Expected the error to name %s, got: %v", unreadable, err)
	}
	if !errors.Is(err, fs.ErrPermission) {
		t.Errorf("Expected the error to wrap a permission error, got: %v", err)
	}
	if len(packages) != 0 {
		t.Errorf("Expected no packages alongside the error, got %v", packages)
	}
}

// Three different manifests reach the nameless branch, and the message has to
// be true of all of them. A JSON null is the surprising one: encoding/json
// treats unmarshalling it as a no-op rather than an error, so the document
// parses and leaves the zero value behind.
func TestListPackagesNamelessMemberFails(t *testing.T) {
	for _, tc := range []struct{ name, manifest string }{
		{"missing name key", `{"version":"1.0.0"}`},
		{"empty name", `{"name":"","version":"1.0.0"}`},
		{"null document", `null`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			pkgA := writePackage(t, root, "packages/package-a")
			pkgB := writePackage(t, root, "packages/package-b")

			nameless := filepath.Join(pkgB, "package.json")
			if err := os.WriteFile(nameless, []byte(tc.manifest), 0644); err != nil {
				t.Fatalf("Failed to write nameless package.json: %v", err)
			}

			ws := &Workspace{Root: root, Type: "npm", Packages: []string{pkgA, pkgB}}
			packages, err := ws.ListPackages()
			if err == nil {
				t.Fatalf("Expected an error for the nameless member, got nil and packages %v", packages)
			}
			if !strings.Contains(err.Error(), nameless) {
				t.Errorf("Expected the error to name %s, got: %v", nameless, err)
			}
			// The message must describe every one of these manifests, so it
			// cannot claim the name field is simply absent.
			if !strings.Contains(err.Error(), "empty or missing name") {
				t.Errorf("Expected the error to call the name empty or missing, got: %v", err)
			}
			if len(packages) != 0 {
				t.Errorf("Expected no packages alongside the error, got %v", packages)
			}
		})
	}
}
