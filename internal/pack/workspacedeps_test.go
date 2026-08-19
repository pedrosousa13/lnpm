package pack

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// newTestWorkspace creates an empty pnpm workspace whose packages live under
// packages/ and returns its root.
func newTestWorkspace(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "pnpm-workspace.yaml"), "packages:\n  - 'packages/*'\n")
	return root
}

// addTestPackage writes packages/<dir>/package.json under root and returns the
// package directory.
func addTestPackage(t *testing.T, root, dir, manifest string) string {
	t.Helper()

	pkgDir := filepath.Join(root, "packages", dir)
	writeTestFile(t, filepath.Join(pkgDir, "package.json"), manifest)
	return pkgDir
}

// writeTestFile writes content to path, creating parent directories.
func writeTestFile(t *testing.T, path, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("Failed to create dir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write %s: %v", path, err)
	}
}

// packageJSONEntry returns the packed package.json FileInfo, failing if absent.
func packageJSONEntry(t *testing.T, files []*FileInfo) *FileInfo {
	t.Helper()

	for _, f := range files {
		if f.RelPath == "package.json" {
			return f
		}
	}
	t.Fatal("Expected a package.json entry among the packed files")
	return nil
}

// packWorkspacePkg packs pkgDir and returns its files.
func packWorkspacePkg(t *testing.T, pkgDir string) []*FileInfo {
	t.Helper()

	_, files, err := Pack(pkgDir)
	if err != nil {
		t.Fatalf("Failed to pack %s: %v", pkgDir, err)
	}
	return files
}

// depValue reads field.name out of the package.json at path.
func depValue(t *testing.T, path, field, name string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Failed to read %s: %v", path, err)
	}

	var manifest map[string]any
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("Failed to parse %s: %v", path, err)
	}

	deps, _ := manifest[field].(map[string]any)
	value, _ := deps[name].(string)
	return value
}

// libManifest renders a lib package.json depending on @ws/util with spec.
func libManifest(spec string) string {
	return `{
  "name": "@ws/lib",
  "version": "1.0.0",
  "dependencies": {
    "@ws/util": "` + spec + `"
  }
}
`
}

func TestRewriteWorkspaceDepsResolvesSpecifierForms(t *testing.T) {
	tests := []struct {
		spec string
		want string
	}{
		{"workspace:*", "2.3.0"},
		{"workspace:latest", "2.3.0"},
		{"workspace:^", "^2.3.0"},
		{"workspace:~", "~2.3.0"},
		// Every other form is the protocol prefix stripped off, exactly as pnpm
		// does it: the range the developer wrote is the range consumers get,
		// whatever the sibling's current version happens to be.
		{"workspace:^1.2.3", "^1.2.3"},
		{"workspace:~1.2.3", "~1.2.3"},
		{"workspace:1.2.3", "1.2.3"},
		{"workspace:>=1.0.0 <3", ">=1.0.0 <3"},
	}

	for _, tt := range tests {
		t.Run(tt.spec, func(t *testing.T) {
			root := newTestWorkspace(t)
			addTestPackage(t, root, "util", `{"name":"@ws/util","version":"2.3.0"}`)
			libDir := addTestPackage(t, root, "lib", libManifest(tt.spec))

			files := packWorkspacePkg(t, libDir)
			cleanup, err := RewriteWorkspaceDeps(libDir, files)
			defer cleanup()
			if err != nil {
				t.Fatalf("RewriteWorkspaceDeps failed: %v", err)
			}

			entry := packageJSONEntry(t, files)
			if got := depValue(t, entry.Path, "dependencies", "@ws/util"); got != tt.want {
				t.Errorf("Expected @ws/util to resolve to %q, got %q", tt.want, got)
			}
		})
	}
}

// pnpm accepts the workspace: protocol in all four dependency maps, and a
// specifier left in any of them reaches consumers through the stored manifest.
func TestRewriteWorkspaceDepsRewritesEveryDependencyField(t *testing.T) {
	for _, field := range []string{"dependencies", "devDependencies", "peerDependencies", "optionalDependencies"} {
		t.Run(field, func(t *testing.T) {
			root := newTestWorkspace(t)
			addTestPackage(t, root, "util", `{"name":"@ws/util","version":"2.3.0"}`)
			libDir := addTestPackage(t, root, "lib", `{
  "name": "@ws/lib",
  "version": "1.0.0",
  "`+field+`": {
    "@ws/util": "workspace:^"
  }
}
`)

			files := packWorkspacePkg(t, libDir)
			cleanup, err := RewriteWorkspaceDeps(libDir, files)
			defer cleanup()
			if err != nil {
				t.Fatalf("RewriteWorkspaceDeps failed: %v", err)
			}

			entry := packageJSONEntry(t, files)
			if got := depValue(t, entry.Path, field, "@ws/util"); got != "^2.3.0" {
				t.Errorf("Expected %s.@ws/util to resolve to \"^2.3.0\", got %q", field, got)
			}
		})
	}
}

// findWorkspaceDeps reads Go maps, whose iteration order is randomised, so it
// sorts: errors and debug output must name the same dependency on every run.
// The fields below are deliberately in two orders - depFields lists
// peerDependencies before optionalDependencies, sorting puts them the other way
// round - so this pins the sort rather than the traversal order.
func TestFindWorkspaceDepsSortsByFieldThenName(t *testing.T) {
	deps, err := findWorkspaceDeps([]byte(`{
  "name": "@ws/lib",
  "version": "1.0.0",
  "dependencies": {"@ws/d": "workspace:*", "@ws/b": "workspace:*"},
  "devDependencies": {"@ws/c": "workspace:*", "@ws/a": "workspace:*"},
  "peerDependencies": {"@ws/e": "workspace:*"},
  "optionalDependencies": {"@ws/f": "workspace:*"}
}`))
	if err != nil {
		t.Fatalf("findWorkspaceDeps failed: %v", err)
	}

	want := []workspaceDep{
		{field: "dependencies", name: "@ws/b", spec: "workspace:*"},
		{field: "dependencies", name: "@ws/d", spec: "workspace:*"},
		{field: "devDependencies", name: "@ws/a", spec: "workspace:*"},
		{field: "devDependencies", name: "@ws/c", spec: "workspace:*"},
		{field: "optionalDependencies", name: "@ws/f", spec: "workspace:*"},
		{field: "peerDependencies", name: "@ws/e", spec: "workspace:*"},
	}
	if len(deps) != len(want) {
		t.Fatalf("Expected %d workspace deps, got %d: %v", len(want), len(deps), deps)
	}
	for i, w := range want {
		if deps[i] != w {
			t.Errorf("Dep %d: expected %v, got %v", i, w, deps[i])
		}
	}
}

// The content hash must cover the rewritten bytes, otherwise the hash and the
// file consumers install would disagree.
func TestRewriteWorkspaceDepsRehashesRewrittenBytes(t *testing.T) {
	root := newTestWorkspace(t)
	addTestPackage(t, root, "util", `{"name":"@ws/util","version":"2.3.0"}`)
	libDir := addTestPackage(t, root, "lib", libManifest("workspace:*"))

	files := packWorkspacePkg(t, libDir)
	before := HashFiles(files)
	entry := packageJSONEntry(t, files)
	sourceHash := entry.ContentHash

	cleanup, err := RewriteWorkspaceDeps(libDir, files)
	defer cleanup()
	if err != nil {
		t.Fatalf("RewriteWorkspaceDeps failed: %v", err)
	}

	rewritten, err := HashFile(entry.Path)
	if err != nil {
		t.Fatalf("Failed to hash rewritten package.json: %v", err)
	}
	if entry.ContentHash != rewritten {
		t.Errorf("Expected entry hash %q to match the rewritten file's hash %q", entry.ContentHash, rewritten)
	}
	if entry.ContentHash == sourceHash {
		t.Error("Expected the rewrite to change the package.json content hash")
	}
	if HashFiles(files) == before {
		t.Error("Expected the rewrite to change the package content hash")
	}

	info, err := os.Stat(entry.Path)
	if err != nil {
		t.Fatalf("Failed to stat rewritten package.json: %v", err)
	}
	if entry.Size != info.Size() {
		t.Errorf("Expected entry size %d to match the rewritten file's size %d", entry.Size, info.Size())
	}
	// The stored file's permissions come from this mode (copyFile) or from the
	// file at Path (reflink), and the content hash folds it in, so the two must
	// still agree after the rewrite.
	if info.Mode().Perm() != entry.Mode.Perm() {
		t.Errorf("Expected rewritten package.json mode %v, got %v", entry.Mode.Perm(), info.Mode().Perm())
	}
}

// AC 4: the developer's own package.json must come out byte-identical.
func TestRewriteWorkspaceDepsLeavesSourceUntouched(t *testing.T) {
	root := newTestWorkspace(t)
	addTestPackage(t, root, "util", `{"name":"@ws/util","version":"2.3.0"}`)
	libDir := addTestPackage(t, root, "lib", libManifest("workspace:*"))

	sourcePath := filepath.Join(libDir, "package.json")
	before, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("Failed to read source package.json: %v", err)
	}

	files := packWorkspacePkg(t, libDir)
	cleanup, err := RewriteWorkspaceDeps(libDir, files)
	defer cleanup()
	if err != nil {
		t.Fatalf("RewriteWorkspaceDeps failed: %v", err)
	}

	after, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("Failed to read source package.json: %v", err)
	}
	if string(after) != string(before) {
		t.Errorf("Expected source package.json to be untouched.\nBefore:\n%s\nAfter:\n%s", before, after)
	}

	entry := packageJSONEntry(t, files)
	if entry.Path == sourcePath {
		t.Error("Expected the packed package.json to point away from the source tree")
	}
	if strings.HasPrefix(entry.Path, libDir+string(filepath.Separator)) {
		t.Errorf("Expected the rewritten package.json to live outside %s, got %s", libDir, entry.Path)
	}
}

// The whole point of splicing rather than re-marshalling: every byte outside
// the rewritten value survives, so key order and indentation are preserved.
func TestRewriteWorkspaceDepsPreservesFormatting(t *testing.T) {
	root := newTestWorkspace(t)
	addTestPackage(t, root, "util", `{"name":"@ws/util","version":"2.3.0"}`)
	manifest := `{
    "version": "1.0.0",
    "name": "@ws/lib",
    "zzz": 9007199254740993,
    "dependencies": {
        "@ws/util": "workspace:^"
    }
}
`
	libDir := addTestPackage(t, root, "lib", manifest)

	files := packWorkspacePkg(t, libDir)
	cleanup, err := RewriteWorkspaceDeps(libDir, files)
	defer cleanup()
	if err != nil {
		t.Fatalf("RewriteWorkspaceDeps failed: %v", err)
	}

	entry := packageJSONEntry(t, files)
	data, err := os.ReadFile(entry.Path)
	if err != nil {
		t.Fatalf("Failed to read rewritten package.json: %v", err)
	}

	want := strings.Replace(manifest, `"workspace:^"`, `"^2.3.0"`, 1)
	if string(data) != want {
		t.Errorf("Expected only the specifier to change.\nWant:\n%s\nGot:\n%s", want, data)
	}
}

// AC 3: a package with no workspace: specifiers is left completely alone.
func TestRewriteWorkspaceDepsWithoutWorkspaceSpecifiers(t *testing.T) {
	root := newTestWorkspace(t)
	addTestPackage(t, root, "util", `{"name":"@ws/util","version":"2.3.0"}`)
	libDir := addTestPackage(t, root, "lib", libManifest("^2.0.0"))

	files := packWorkspacePkg(t, libDir)
	entry := packageJSONEntry(t, files)
	beforePath, beforeHash, beforeSize := entry.Path, entry.ContentHash, entry.Size

	cleanup, err := RewriteWorkspaceDeps(libDir, files)
	defer cleanup()
	if err != nil {
		t.Fatalf("RewriteWorkspaceDeps failed: %v", err)
	}

	if entry.Path != beforePath {
		t.Errorf("Expected package.json to still be packed from %s, got %s", beforePath, entry.Path)
	}
	if entry.ContentHash != beforeHash || entry.Size != beforeSize {
		t.Errorf("Expected package.json hash/size to be untouched, got %s/%d", entry.ContentHash, entry.Size)
	}
}

// AC 3 again, for the case Detect reports no workspace at all: a package with
// no workspace: specifiers outside a workspace must still publish.
func TestRewriteWorkspaceDepsOutsideWorkspaceWithoutSpecifiers(t *testing.T) {
	pkgDir := t.TempDir()
	writeTestFile(t, filepath.Join(pkgDir, "package.json"), `{"name":"lonely","version":"1.0.0"}`)

	files := packWorkspacePkg(t, pkgDir)
	cleanup, err := RewriteWorkspaceDeps(pkgDir, files)
	defer cleanup()
	if err != nil {
		t.Fatalf("RewriteWorkspaceDeps failed: %v", err)
	}
}

// AC 5, first case: a workspace: specifier outside any workspace.
func TestRewriteWorkspaceDepsOutsideWorkspaceFails(t *testing.T) {
	pkgDir := t.TempDir()
	writeTestFile(t, filepath.Join(pkgDir, "package.json"), libManifest("workspace:*"))

	files := packWorkspacePkg(t, pkgDir)
	cleanup, err := RewriteWorkspaceDeps(pkgDir, files)
	defer cleanup()
	if err == nil {
		t.Fatal("Expected an error for a workspace: specifier outside a workspace, got nil")
	}
	for _, want := range []string{"@ws/util", "workspace:*", "workspace"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Expected error to mention %q, got: %v", want, err)
		}
	}
}

// AC 5, second case: the sibling is not a package of the detected workspace.
func TestRewriteWorkspaceDepsUnknownSiblingFails(t *testing.T) {
	root := newTestWorkspace(t)
	libDir := addTestPackage(t, root, "lib", libManifest("workspace:*"))

	files := packWorkspacePkg(t, libDir)
	cleanup, err := RewriteWorkspaceDeps(libDir, files)
	defer cleanup()
	if err == nil {
		t.Fatal("Expected an error for a sibling missing from the workspace, got nil")
	}
	for _, want := range []string{"@ws/util", "workspace:*", root} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Expected error to mention %q, got: %v", want, err)
		}
	}
}

// A bare "workspace:" carries no range to strip down to, so there is nothing to
// publish: it must fail rather than ship an empty specifier.
func TestRewriteWorkspaceDepsBareProtocolFails(t *testing.T) {
	root := newTestWorkspace(t)
	addTestPackage(t, root, "util", `{"name":"@ws/util","version":"2.3.0"}`)
	libDir := addTestPackage(t, root, "lib", libManifest("workspace:"))

	files := packWorkspacePkg(t, libDir)
	cleanup, err := RewriteWorkspaceDeps(libDir, files)
	defer cleanup()
	if err == nil {
		t.Fatal("Expected an error for a bare workspace: specifier, got nil")
	}
	for _, want := range []string{"workspace:", "@ws/util"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Expected error to mention %q, got: %v", want, err)
		}
	}
}

// The temporary file must not outlive the publish that created it.
func TestRewriteWorkspaceDepsCleanupRemovesTempFile(t *testing.T) {
	root := newTestWorkspace(t)
	addTestPackage(t, root, "util", `{"name":"@ws/util","version":"2.3.0"}`)
	libDir := addTestPackage(t, root, "lib", libManifest("workspace:*"))

	files := packWorkspacePkg(t, libDir)
	cleanup, err := RewriteWorkspaceDeps(libDir, files)
	if err != nil {
		t.Fatalf("RewriteWorkspaceDeps failed: %v", err)
	}

	entry := packageJSONEntry(t, files)
	if _, err := os.Stat(entry.Path); err != nil {
		t.Fatalf("Expected the rewritten package.json to exist before cleanup: %v", err)
	}

	cleanup()

	if _, err := os.Stat(entry.Path); !os.IsNotExist(err) {
		t.Errorf("Expected cleanup to remove %s, stat returned %v", entry.Path, err)
	}
}

// A rewrite that fails while resolving gives up before any temporary file is
// created, so the packed entry must still point at the source tree - publish
// deferred the cleanup before checking the error, and the entry it hands to the
// store has to be the untouched one.
func TestRewriteWorkspaceDepsFailingResolutionKeepsPackedEntry(t *testing.T) {
	root := newTestWorkspace(t)
	libDir := addTestPackage(t, root, "lib", libManifest("workspace:*"))

	files := packWorkspacePkg(t, libDir)
	entry := packageJSONEntry(t, files)
	sourcePath, sourceHash, sourceSize := entry.Path, entry.ContentHash, entry.Size

	cleanup, err := RewriteWorkspaceDeps(libDir, files)
	defer cleanup()
	if err == nil {
		t.Fatal("Expected an error for a sibling missing from the workspace, got nil")
	}
	if entry.Path != sourcePath {
		t.Errorf("Expected a failed rewrite to leave package.json packed from %s, got %s", sourcePath, entry.Path)
	}
	if entry.ContentHash != sourceHash || entry.Size != sourceSize {
		t.Errorf("Expected a failed rewrite to leave the packed hash/size alone, got %s/%d", entry.ContentHash, entry.Size)
	}
}

// The genuine leak path: a failure raised after the temporary file exists must
// still remove it. Mode 0 is what gets it there - materialization gives the
// temporary file the packed entry's mode, and hashing an unreadable file fails.
func TestRewriteWorkspaceDepsRemovesTempFileWhenMaterializationFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows leaves a mode 0 file readable, so hashing it would not fail")
	}
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode 0 file whatever its permissions say")
	}

	root := newTestWorkspace(t)
	addTestPackage(t, root, "util", `{"name":"@ws/util","version":"2.3.0"}`)
	libDir := addTestPackage(t, root, "lib", libManifest("workspace:*"))

	// os.CreateTemp writes into os.TempDir(), so pointing that at an empty
	// directory makes anything left behind directly observable.
	tmpDir := t.TempDir()
	t.Setenv("TMPDIR", tmpDir)

	files := packWorkspacePkg(t, libDir)
	entry := packageJSONEntry(t, files)
	entry.Mode = 0

	cleanup, err := RewriteWorkspaceDeps(libDir, files)
	defer cleanup()
	if err == nil {
		t.Fatal("Expected hashing an unreadable rewritten package.json to fail, got nil")
	}

	left, readErr := os.ReadDir(tmpDir)
	if readErr != nil {
		t.Fatalf("Failed to read %s: %v", tmpDir, readErr)
	}
	if len(left) != 0 {
		names := make([]string, len(left))
		for i, e := range left {
			names[i] = e.Name()
		}
		t.Errorf("Expected a failed rewrite to leave no temporary file, found %v in %s", names, tmpDir)
	}
}

// A package.json kept out of the published files leaves nothing to rewrite the
// specifier in, which must be reported rather than silently published.
func TestRewriteWorkspaceDepsWithoutPackedManifestFails(t *testing.T) {
	root := newTestWorkspace(t)
	addTestPackage(t, root, "util", `{"name":"@ws/util","version":"2.3.0"}`)
	libDir := addTestPackage(t, root, "lib", libManifest("workspace:*"))
	writeTestFile(t, filepath.Join(libDir, ".npmignore"), "package.json\n")

	files := packWorkspacePkg(t, libDir)
	if packageJSONEntryOrNil(files) != nil {
		t.Fatal("Expected .npmignore to keep package.json out of the packed files")
	}

	cleanup, err := RewriteWorkspaceDeps(libDir, files)
	defer cleanup()
	if err == nil {
		t.Fatal("Expected an error when package.json is not among the packed files, got nil")
	}
	if !strings.Contains(err.Error(), "package.json") {
		t.Errorf("Expected the error to name package.json, got: %v", err)
	}
}

// packageJSONEntryOrNil is packageJSONEntry without the t.Fatal, for the one
// test that expects the entry to be absent.
func packageJSONEntryOrNil(files []*FileInfo) *FileInfo {
	for _, f := range files {
		if f.RelPath == "package.json" {
			return f
		}
	}
	return nil
}
