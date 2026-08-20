package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"gopkg.in/yaml.v3"
)

// Workspace represents a monorepo workspace
type Workspace struct {
	Root     string   // Root directory of the workspace
	Type     string   // "npm", "yarn", "pnpm", "bun"
	Packages []string // Paths to package directories
}

// Package represents a package in the workspace
type Package struct {
	Name    string
	Version string
	Path    string
}

// Detect detects if the current directory is part of a monorepo workspace
func Detect(startPath string) (*Workspace, error) {
	// Walk up looking for workspace root
	current := startPath
	for {
		ws, err := detectWorkspaceAt(current)
		// A malformed glob pattern is a config error, not a "no workspace
		// here" signal. Walking past it would end in "no workspace found",
		// which hides the offending pattern from the user, and docs/adr/0001
		// requires a malformed pattern to abort naming the pattern. Every
		// other failure keeps the existing walk-up behaviour.
		//
		// doublestar.ErrBadPattern is path.ErrBadPattern, so this guard would
		// also catch a bad-pattern error raised by path.Match anywhere under
		// detectWorkspaceAt. Nothing under there calls path.Match today.
		if errors.Is(err, doublestar.ErrBadPattern) {
			return nil, err
		}
		if err == nil && ws != nil {
			return ws, nil
		}

		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}

	return nil, nil
}

// detectWorkspaceAt checks if a directory is a workspace root
func detectWorkspaceAt(dir string) (*Workspace, error) {
	// Check for pnpm-workspace.yaml
	pnpmWorkspace := filepath.Join(dir, "pnpm-workspace.yaml")
	if _, err := os.Stat(pnpmWorkspace); err == nil {
		return parsePnpmWorkspace(dir, pnpmWorkspace)
	}

	// Check for package.json with workspaces field
	pkgJSON := filepath.Join(dir, "package.json")
	if _, err := os.Stat(pkgJSON); err == nil {
		return parsePackageJSONWorkspace(dir, pkgJSON)
	}

	return nil, nil
}

// parsePnpmWorkspace parses a pnpm-workspace.yaml file
func parsePnpmWorkspace(root, path string) (*Workspace, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var config struct {
		Packages []string `yaml:"packages"`
	}
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, err
	}

	if len(config.Packages) == 0 {
		return nil, nil
	}

	packages, err := expandGlobs(root, config.Packages)
	if err != nil {
		return nil, err
	}

	return &Workspace{
		Root:     root,
		Type:     "pnpm",
		Packages: packages,
	}, nil
}

// parsePackageJSONWorkspace parses workspaces from package.json
func parsePackageJSONWorkspace(root, path string) (*Workspace, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var pkgJSON struct {
		Workspaces interface{} `json:"workspaces"`
	}
	if err := json.Unmarshal(data, &pkgJSON); err != nil {
		return nil, err
	}

	if pkgJSON.Workspaces == nil {
		return nil, nil
	}

	var patterns []string

	// Workspaces can be an array or an object with "packages" field
	switch ws := pkgJSON.Workspaces.(type) {
	case []interface{}:
		for _, p := range ws {
			if s, ok := p.(string); ok {
				patterns = append(patterns, s)
			}
		}
	case map[string]interface{}:
		if pkgs, ok := ws["packages"].([]interface{}); ok {
			for _, p := range pkgs {
				if s, ok := p.(string); ok {
					patterns = append(patterns, s)
				}
			}
		}
	}

	if len(patterns) == 0 {
		return nil, nil
	}

	packages, err := expandGlobs(root, patterns)
	if err != nil {
		return nil, err
	}

	// Detect workspace type from lock file
	wsType := "npm"
	if _, err := os.Stat(filepath.Join(root, "yarn.lock")); err == nil {
		wsType = "yarn"
	} else if _, err := os.Stat(filepath.Join(root, "bun.lockb")); err == nil {
		wsType = "bun"
	}

	return &Workspace{
		Root:     root,
		Type:     wsType,
		Packages: packages,
	}, nil
}

// expandGlobs expands workspace glob patterns to actual package directories.
// Patterns prefixed with "!" are negations: they are collected while the
// included patterns are expanded, then subtracted from the result.
//
// A pattern that will not parse fails the whole expansion, includes and
// negations alike: a swallowed negation failure publishes the package the
// config excluded, which docs/adr/0001 classifies as a bug.
func expandGlobs(root string, patterns []string) ([]string, error) {
	var packages []string
	var negations []string
	seen := make(map[string]bool)

	for _, pattern := range patterns {
		// Collect negation patterns to subtract once all includes are expanded
		if strings.HasPrefix(pattern, "!") {
			negations = append(negations, strings.TrimPrefix(pattern, "!"))
			continue
		}

		// Expand glob. The only failure Glob can report here is a malformed
		// pattern, so this aborts on a config typo and never on a transient
		// filesystem condition. An include failure fails closed, which
		// docs/adr/0001 leaves open, but it follows the negation loop's rule
		// so the two are handled alike.
		matches, err := doublestar.Glob(os.DirFS(root), pattern)
		if err != nil {
			return nil, fmt.Errorf("failed to expand workspace pattern %q: %w", pattern, err)
		}

		for _, match := range matches {
			pkgPath := filepath.Join(root, match)

			// Check if it's a directory with package.json
			pkgJSON := filepath.Join(pkgPath, "package.json")
			if _, err := os.Stat(pkgJSON); err != nil {
				continue
			}

			if !seen[pkgPath] {
				seen[pkgPath] = true
				packages = append(packages, pkgPath)
			}
		}
	}

	if len(negations) == 0 {
		return packages, nil
	}

	excluded := make(map[string]bool)
	for _, pattern := range negations {
		matches, err := doublestar.Glob(os.DirFS(root), pattern)
		if err != nil {
			return nil, fmt.Errorf("failed to expand workspace pattern %q: %w", "!"+pattern, err)
		}

		for _, match := range matches {
			excluded[filepath.Join(root, match)] = true
		}
	}

	filtered := make([]string, 0, len(packages))
	for _, pkgPath := range packages {
		if !excluded[pkgPath] {
			filtered = append(filtered, pkgPath)
		}
	}

	return filtered, nil
}

// ListPackages returns all packages in the workspace with their metadata.
//
// A member that will not read, will not parse, or names no package fails the
// whole listing. This is the "glob legitimately matches a non-package
// directory" case that docs/adr/0001 leaves open, except that it is not that
// case: expandGlobs already dropped every directory without a package.json
// before it reached w.Packages, so a failure here is a broken member of a
// workspace the caller asked for - a permission problem, a config typo, or a
// file deleted underneath us - and not a directory that merely is not a
// package. Skipping it publishes less than `--all` asked for and still reports
// success.
//
// An unreadable member and an unparseable one are deliberately not
// distinguished; both name the offending file and wrap the underlying error.
func (w *Workspace) ListPackages() ([]Package, error) {
	var packages []Package

	for _, pkgPath := range w.Packages {
		pkgJSON := filepath.Join(pkgPath, "package.json")
		data, err := os.ReadFile(pkgJSON)
		if err != nil {
			return nil, fmt.Errorf("failed to read workspace package %s: %w", pkgJSON, err)
		}

		var pkg struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		}
		if err := json.Unmarshal(data, &pkg); err != nil {
			return nil, fmt.Errorf("failed to parse workspace package %s: %w", pkgJSON, err)
		}

		// A nameless package cannot be published or resolved against, and
		// returning it with an empty name carries the breakage downstream.
		if pkg.Name == "" {
			return nil, fmt.Errorf("workspace package %s has no name field", pkgJSON)
		}

		packages = append(packages, Package{
			Name:    pkg.Name,
			Version: pkg.Version,
			Path:    pkgPath,
		})
	}

	return packages, nil
}
