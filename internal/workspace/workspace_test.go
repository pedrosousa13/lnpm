package workspace

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/fsutil"
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

// --- a config Detect is using, versus one it is walking past ------------------
//
// Detect walks from startPath to the filesystem root, so the two ends of that
// walk are not the same kind of place. The starting directory's config is one
// the caller is actually using; anything found further up belongs to whatever
// happens to sit above them, which on a deep enough path is an unrelated
// project. Issue #288 settled the split there: a broken config you are using
// aborts naming the file, a broken config you are only passing through does
// not become your problem.

func TestDetectUnparseablePackageJSONInStartDirFails(t *testing.T) {
	root := t.TempDir()
	writePackage(t, root, "packages/package-a")

	manifest := filepath.Join(root, "package.json")
	if err := os.WriteFile(manifest, []byte(`{"name":"root","workspaces":["packages/*",}`), 0644); err != nil {
		t.Fatalf("Failed to write package.json: %v", err)
	}

	ws, err := Detect(root)
	if err == nil {
		t.Fatalf("Expected an error for the unparseable package.json, got nil and workspace %+v", ws)
	}
	if !strings.Contains(err.Error(), manifest) {
		t.Errorf("Expected the error to name %s, got: %v", manifest, err)
	}
	// encoding/json describes the syntax problem and nothing else, so the
	// message only reaches the file because Detect's side wraps it. Pin that
	// the wrap keeps the original reachable rather than flattening it.
	var syntax *json.SyntaxError
	if !errors.As(err, &syntax) {
		t.Errorf("Expected the error to wrap a *json.SyntaxError, got: %v", err)
	}
	if ws != nil {
		t.Errorf("Expected no workspace alongside the error, got %+v", ws)
	}
}

func TestDetectUnparseablePnpmWorkspaceInStartDirFails(t *testing.T) {
	root := t.TempDir()
	writePackage(t, root, "packages/package-a")

	config := filepath.Join(root, "pnpm-workspace.yaml")
	if err := os.WriteFile(config, []byte("packages: [packages/*\n"), 0644); err != nil {
		t.Fatalf("Failed to write pnpm-workspace.yaml: %v", err)
	}

	ws, err := Detect(root)
	if err == nil {
		t.Fatalf("Expected an error for the unparseable pnpm-workspace.yaml, got nil and workspace %+v", ws)
	}
	if !strings.Contains(err.Error(), config) {
		t.Errorf("Expected the error to name %s, got: %v", config, err)
	}
	// gopkg.in/yaml.v3 has no exported error type to match on, so assert the
	// weaker but still meaningful thing: the parse error is wrapped, not
	// replaced by a message that names only the file.
	if errors.Unwrap(err) == nil {
		t.Errorf("Expected the error to wrap the YAML parse error, got: %v", err)
	}
	if ws != nil {
		t.Errorf("Expected no workspace alongside the error, got %+v", ws)
	}
}

// A config that cannot be read fails the same way a config that cannot be
// parsed does. os.ReadFile returns a *fs.PathError, which already names the
// file, so this pins that the message stays actionable without Detect adding
// a second spelling of the same path.
func TestDetectUnreadableConfigInStartDirFails(t *testing.T) {
	requirePermissionEnforcement(t)

	root := t.TempDir()
	writePackage(t, root, "packages/package-a")

	manifest := filepath.Join(root, "package.json")
	if err := os.WriteFile(manifest, []byte(`{"name":"root","workspaces":["packages/*"]}`), 0644); err != nil {
		t.Fatalf("Failed to write package.json: %v", err)
	}
	if err := os.Chmod(manifest, 0000); err != nil {
		t.Fatalf("Failed to chmod package.json: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(manifest, 0644) })

	ws, err := Detect(root)
	if err == nil {
		t.Fatalf("Expected an error for the unreadable package.json, got nil and workspace %+v", ws)
	}
	if !strings.Contains(err.Error(), manifest) {
		t.Errorf("Expected the error to name %s, got: %v", manifest, err)
	}
	if !errors.Is(err, fs.ErrPermission) {
		t.Errorf("Expected the error to wrap a permission error, got: %v", err)
	}
	if ws != nil {
		t.Errorf("Expected no workspace alongside the error, got %+v", ws)
	}
}

// The other half of the split. A project with no workspace of its own, sitting
// under a broken one, reports "no workspace" rather than inheriting a failure
// it cannot fix - Detect walks to the filesystem root, so propagating here
// would let any broken config anywhere above a user's home directory break
// every command they run.
func TestDetectWalksPastABrokenAncestorConfig(t *testing.T) {
	for _, tc := range []struct{ name, file, contents string }{
		{"package.json", "package.json", `{"name":"root","workspaces":["packages/*",}`},
		{"pnpm-workspace.yaml", "pnpm-workspace.yaml", "packages: [packages/*\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, tc.file), []byte(tc.contents), 0644); err != nil {
				t.Fatalf("Failed to write %s: %v", tc.file, err)
			}

			project := filepath.Join(root, "nested", "project")
			if err := os.MkdirAll(project, 0755); err != nil {
				t.Fatalf("Failed to create %s: %v", project, err)
			}

			ws, err := Detect(project)
			if err != nil {
				t.Fatalf("Expected the broken ancestor config to be walked past, got: %v", err)
			}
			if ws != nil {
				t.Errorf("Expected no workspace, got %+v", ws)
			}
		})
	}
}

// #241's guard has to keep working at every level of the walk, not only the
// first. A malformed glob pattern is the one failure docs/adr/0001 requires to
// abort wherever it is found, because it widens a publish rather than failing
// closed - it is not covered by the starting-directory rule above and must not
// be narrowed to it.
func TestDetectMalformedPatternInAncestorReturnsError(t *testing.T) {
	root := t.TempDir()
	writePackage(t, root, "packages/public-api")

	manifest := `{"name":"root","workspaces":["packages/[bad"]}`
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(manifest), 0644); err != nil {
		t.Fatalf("Failed to write package.json: %v", err)
	}

	project := filepath.Join(root, "nested", "project")
	if err := os.MkdirAll(project, 0755); err != nil {
		t.Fatalf("Failed to create %s: %v", project, err)
	}

	ws, err := Detect(project)
	if err == nil {
		t.Fatalf("Expected an error for the malformed pattern, got nil and workspace %+v", ws)
	}
	if !strings.Contains(err.Error(), "packages/[bad") {
		t.Errorf("Expected the error to name the offending pattern, got: %v", err)
	}
	if ws != nil {
		t.Errorf("Expected no workspace alongside the error, got %+v", ws)
	}
}

// "There is no workspace here" is not a failure, and the starting-directory
// rule must not turn any of these three into one. Every ordinary project has a
// package.json with no workspaces field, so getting this wrong would fail every
// command run in one.
func TestDetectTreatsAConfigDeclaringNoWorkspaceAsNoWorkspace(t *testing.T) {
	for _, tc := range []struct{ name, file, contents string }{
		{"package.json with no workspaces field", "package.json", `{"name":"solo","version":"1.0.0"}`},
		{"pnpm-workspace.yaml with an empty packages list", "pnpm-workspace.yaml", "packages: []\n"},
		{"neither config present", "README.md", "not a manifest\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, tc.file), []byte(tc.contents), 0644); err != nil {
				t.Fatalf("Failed to write %s: %v", tc.file, err)
			}

			ws, err := Detect(root)
			if err != nil {
				t.Fatalf("Expected no error, got: %v", err)
			}
			if ws != nil {
				t.Errorf("Expected no workspace, got %+v", ws)
			}
		})
	}
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

// TestDetectRefusesAnOversizedPnpmWorkspace covers the cap on the workspace
// config, which shares its reason and its constant with the lock file's:
// yaml.v3's parse cost is superlinear, so an oversized document has to be
// refused rather than parsed.
//
// The fixture is invalid YAML as well as oversized, and that is the point. With
// the cap before the unmarshal the caller gets the size refusal; with it after,
// yaml.Unmarshal reports the syntax error first and the size is never
// mentioned. Asserting which error comes back distinguishes the two placements
// without measuring how long anything takes.
//
// os.Truncate extends the file instead of writing four megabytes out, so the
// oversized case costs nothing to build. Both properties are read back rather
// than assumed - the length, which is what the cap sees, and the leading bytes,
// which are what a misplaced cap would trip over.
func TestDetectRefusesAnOversizedPnpmWorkspace(t *testing.T) {
	root := t.TempDir()
	writePackage(t, root, "packages/package-a")

	config := filepath.Join(root, "pnpm-workspace.yaml")
	const head = "packages: [packages/*\n"
	size := int64(fsutil.MaxYAMLBytes) + 1

	if err := os.WriteFile(config, []byte(head), 0644); err != nil {
		t.Fatalf("Failed to write pnpm-workspace.yaml: %v", err)
	}
	if err := os.Truncate(config, size); err != nil {
		t.Fatalf("Truncate(%s, %d) error: %v", config, size, err)
	}
	info, err := os.Stat(config)
	if err != nil {
		t.Fatalf("Stat(%s) error: %v", config, err)
	}
	if info.Size() != size {
		t.Fatalf("built %s at %d bytes, want %d", config, info.Size(), size)
	}
	built, err := os.Open(config)
	if err != nil {
		t.Fatalf("Open(%s) error: %v", config, err)
	}
	gotHead := make([]byte, len(head))
	if _, err := io.ReadFull(built, gotHead); err != nil {
		_ = built.Close()
		t.Fatalf("reading back the head of %s: %v", config, err)
	}
	_ = built.Close()
	if string(gotHead) != head {
		t.Fatalf("head of %s = %q, want the invalid YAML %q", config, gotHead, head)
	}

	ws, err := Detect(root)
	if err == nil {
		t.Fatalf("Expected a refusal for the oversized pnpm-workspace.yaml, got nil and workspace %+v", ws)
	}
	if ws != nil {
		t.Errorf("Expected no workspace alongside the error, got %+v", ws)
	}
	if !errors.Is(err, fsutil.ErrFileTooLarge) {
		t.Errorf("Detect() error = %v, want it to wrap fsutil.ErrFileTooLarge", err)
	}

	msg := err.Error()
	for _, want := range []string{
		config,
		strconv.FormatInt(size, 10),
		strconv.FormatInt(fsutil.MaxYAMLBytes, 10),
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("Detect() error = %q, want it to name %q", msg, want)
		}
	}
	if strings.Contains(msg, "failed to parse") {
		t.Errorf("Detect() error = %q, want a size refusal; a parse error means the file was unmarshalled before the cap was checked", msg)
	}
}
