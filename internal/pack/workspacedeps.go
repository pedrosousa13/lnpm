package pack

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pedrosousa13/lnpm/internal/debug"
	"github.com/pedrosousa13/lnpm/internal/pkgjson"
	"github.com/pedrosousa13/lnpm/internal/workspace"
)

// workspaceProtocol is the specifier prefix a monorepo uses to point a package
// at a sibling of the same workspace. npm has no idea what it means outside that
// workspace, so it must never reach a consumer's package.json.
const workspaceProtocol = "workspace:"

// depFields are the package.json fields whose specifiers get resolved.
var depFields = []string{"dependencies", "devDependencies"}

// workspaceDep is one dependency entry carrying a workspace: specifier.
type workspaceDep struct {
	field string
	name  string
	spec  string
}

// workspaceIndex is the version of every package in a workspace, keyed by name.
type workspaceIndex struct {
	root     string
	versions map[string]string
}

// RewriteWorkspaceDeps resolves the workspace: dependency specifiers of the
// package at pkgDir against the other packages of its workspace, so consumers
// receive a specifier npm can install.
//
// The developer's own package.json is never modified. The rewritten document is
// written to a temporary file outside the source tree and the package.json entry
// of files is repointed at it, with its hash and size recomputed - store.Store
// copies from FileInfo.Path, and HashFiles must cover the bytes that reach the
// store rather than the ones still on disk in the workspace.
//
// The returned cleanup removes that temporary file. It is never nil, so it can
// be deferred before the error is checked, and it must not run until the files
// have been stored.
func RewriteWorkspaceDeps(pkgDir string, files []*FileInfo) (func(), error) {
	noop := func() {}

	src, err := os.ReadFile(filepath.Join(pkgDir, "package.json"))
	if err != nil {
		return noop, fmt.Errorf("failed to read package.json: %w", err)
	}

	deps, err := findWorkspaceDeps(src)
	if err != nil {
		return noop, err
	}
	if len(deps) == 0 {
		return noop, nil
	}

	index, err := indexWorkspace(pkgDir, deps[0])
	if err != nil {
		return noop, err
	}

	entry := packedPackageJSON(files)
	if entry == nil {
		return noop, fmt.Errorf("cannot resolve workspace dependencies: package.json is excluded from the published files")
	}

	out := src
	for _, dep := range deps {
		resolved, err := resolveWorkspaceSpec(dep, index)
		if err != nil {
			return noop, err
		}
		if out, err = pkgjson.SetDep(out, dep.field, dep.name, resolved); err != nil {
			return noop, fmt.Errorf("failed to rewrite %s in package.json: %w", dep.name, err)
		}
		debug.Logf("pack: resolved %s %s -> %s", dep.name, dep.spec, resolved)
	}

	return materializePackageJSON(entry, out)
}

// findWorkspaceDeps returns the workspace: entries of src, sorted by field and
// name so errors and debug output do not depend on Go's map iteration order.
//
// It reads the dependency maps rather than editing them: pkgjson can set a named
// entry but cannot enumerate one, and round-tripping the document through a map
// to find the names would reformat everything the splice exists to preserve.
func findWorkspaceDeps(src []byte) ([]workspaceDep, error) {
	var manifest struct {
		Dependencies    map[string]any `json:"dependencies"`
		DevDependencies map[string]any `json:"devDependencies"`
	}
	if err := json.Unmarshal(src, &manifest); err != nil {
		return nil, fmt.Errorf("failed to parse package.json: %w", err)
	}

	byField := map[string]map[string]any{
		"dependencies":    manifest.Dependencies,
		"devDependencies": manifest.DevDependencies,
	}

	var deps []workspaceDep
	for _, field := range depFields {
		for name, value := range byField[field] {
			spec, ok := value.(string)
			if !ok || !strings.HasPrefix(spec, workspaceProtocol) {
				continue
			}
			deps = append(deps, workspaceDep{field: field, name: name, spec: spec})
		}
	}

	sort.Slice(deps, func(i, j int) bool {
		if deps[i].field != deps[j].field {
			return deps[i].field < deps[j].field
		}
		return deps[i].name < deps[j].name
	})

	return deps, nil
}

// indexWorkspace lists the packages of pkgDir's workspace. Detect reports "no
// workspace here" as (nil, nil), which is its own failure in this context: the
// package names a sibling but has no workspace to resolve it against. first is
// only used to name a dependency in that error.
func indexWorkspace(pkgDir string, first workspaceDep) (*workspaceIndex, error) {
	ws, err := workspace.Detect(pkgDir)
	if err != nil {
		return nil, fmt.Errorf("failed to detect workspace: %w", err)
	}
	if ws == nil {
		return nil, fmt.Errorf("cannot resolve %q for dependency %s: %s is not part of a workspace",
			first.spec, first.name, pkgDir)
	}

	packages, err := ws.ListPackages()
	if err != nil {
		return nil, fmt.Errorf("failed to list workspace packages: %w", err)
	}

	versions := make(map[string]string, len(packages))
	for _, pkg := range packages {
		versions[pkg.Name] = pkg.Version
	}

	return &workspaceIndex{root: ws.Root, versions: versions}, nil
}

// resolveWorkspaceSpec turns a workspace: specifier into the version range a
// consumer outside the workspace can install, matching pnpm and yalc. A form it
// cannot resolve is an error rather than a pass-through: shipping the literal
// specifier is exactly the breakage this rewrite exists to prevent.
func resolveWorkspaceSpec(dep workspaceDep, index *workspaceIndex) (string, error) {
	version, ok := index.versions[dep.name]
	if !ok {
		return "", fmt.Errorf("cannot resolve %q for dependency %s: no such package in the workspace at %s",
			dep.spec, dep.name, index.root)
	}

	switch strings.TrimPrefix(dep.spec, workspaceProtocol) {
	case "*", "latest":
		return version, nil
	case "^":
		return "^" + version, nil
	case "~":
		return "~" + version, nil
	}

	return "", fmt.Errorf("unsupported workspace specifier %q for dependency %s", dep.spec, dep.name)
}

// packedPackageJSON returns the package.json entry of files, or nil.
func packedPackageJSON(files []*FileInfo) *FileInfo {
	for _, f := range files {
		if f.RelPath == "package.json" {
			return f
		}
	}
	return nil
}

// materializePackageJSON writes out to a temporary file and repoints entry at
// it, recomputing the hash and size the store and the file manifest are built
// from. The temporary file carries entry's mode because a reflinked store copy
// inherits the permissions of the file it clones, and the content hash folds
// those permissions in.
func materializePackageJSON(entry *FileInfo, out []byte) (func(), error) {
	noop := func() {}

	tmp, err := os.CreateTemp("", "lnpm-package-json-")
	if err != nil {
		return noop, fmt.Errorf("failed to create temporary package.json: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }

	if err := writeTempPackageJSON(tmp, out, entry.Mode); err != nil {
		cleanup()
		return noop, err
	}

	hash, err := hashFile(tmpPath)
	if err != nil {
		cleanup()
		return noop, fmt.Errorf("failed to hash rewritten package.json: %w", err)
	}

	entry.Path = tmpPath
	entry.ContentHash = hash
	entry.Size = int64(len(out))

	return cleanup, nil
}

// writeTempPackageJSON fills and closes an already created temporary file.
func writeTempPackageJSON(tmp *os.File, out []byte, mode os.FileMode) error {
	if _, err := tmp.Write(out); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("failed to write rewritten package.json: %w", err)
	}
	if err := tmp.Chmod(mode.Perm()); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("failed to set rewritten package.json permissions: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to write rewritten package.json: %w", err)
	}
	return nil
}
