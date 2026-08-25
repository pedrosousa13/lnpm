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

	"github.com/pedrosousa13/lnpm/internal/fsutil"
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

// ErrWorkspaceMemberRefused marks the refusal of a globbed workspace member, so
// Detect can recognise that refusal anywhere along its walk.
//
// Both of requireWithinRoot's refusals raise it - the member whose real path
// falls outside the workspace root, and the member that will not resolve at all
// - because both say the same thing about the config in hand: it names a member
// this command will not accept, so there is nothing to gain by walking past it.
// One sentinel rather than two keeps Detect's guard a single errors.Is, beside
// the one it already runs for doublestar.ErrBadPattern.
//
// requireWithinRoot carries the whole message; this sentinel is only the phrase
// each message opens with.
var ErrWorkspaceMemberRefused = errors.New("refused workspace member")

// Detect detects if the current directory is part of a monorepo workspace
func Detect(startPath string) (*Workspace, error) {
	// Walk up looking for workspace root
	current := startPath
	startDir := true
	for {
		ws, err := detectWorkspaceAt(current)
		// A malformed glob pattern is a config error, not a "no workspace
		// here" signal. Walking past it would end in "no workspace found",
		// which hides the offending pattern from the user, and docs/adr/0001
		// requires a malformed pattern to abort naming the pattern. This guard
		// is unconditional, as #241 wrote it; the starting-directory rule
		// below, settled separately in #288, deliberately does not narrow it.
		//
		// doublestar.ErrBadPattern is path.ErrBadPattern, so this guard would
		// also catch a bad-pattern error raised by path.Match anywhere under
		// detectWorkspaceAt. Nothing under there calls path.Match today.
		if errors.Is(err, doublestar.ErrBadPattern) {
			return nil, err
		}
		// A refused workspace member aborts for the same reason and is
		// unconditional for the same reason. It is found where the workspace
		// config is, and `lnpm publish` run inside a member directory reaches
		// that config on a later iteration - so narrowing this to the starting
		// directory would swallow the refusal on the ordinary monorepo
		// invocation and answer "no workspace found", naming neither the
		// member nor what was wrong with it. Both of requireWithinRoot's
		// refusals wrap the sentinel, so both reach here.
		if errors.Is(err, ErrWorkspaceMemberRefused) {
			return nil, err
		}
		// A config that will not read or will not parse aborts only in the
		// directory Detect started from - the first iteration, and the only
		// place along the walk where the config is one the caller is actually
		// using. Reporting it as "no workspace found" points the user at their
		// command line when the problem is a typo in the file beside them.
		//
		// Later iterations keep swallowing it, deliberately. Detect walks to
		// the filesystem root, so a config anywhere above the user - a stray
		// package.json in a home directory, an unrelated project this one
		// happens to sit beneath - would otherwise break every command run
		// here. Issue #288 weighed aborting everywhere and declined it for
		// that reason; do not widen this to the whole loop.
		if err != nil && startDir {
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
		startDir = false
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

// parsePnpmWorkspace parses a pnpm-workspace.yaml file.
//
// The parse error is wrapped with the path because yaml.Unmarshal describes
// only the syntax problem and never says which file it was reading. The read
// error is not: it is a *fs.PathError, or a refusal that names the file itself,
// and wrapping would give one file two spellings in one message.
//
// The read is capped before the unmarshal, because yaml.v3's parse cost is
// superlinear and this file comes from the repository being worked in - see
// fsutil.MaxYAMLBytes, whose comment carries the measurements.
func parsePnpmWorkspace(root, path string) (*Workspace, error) {
	data, err := fsutil.ReadFileCapped(path, fsutil.MaxYAMLBytes)
	if err != nil {
		return nil, err
	}

	var config struct {
		Packages []string `yaml:"packages"`
	}
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", path, err)
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

// parsePackageJSONWorkspace parses workspaces from package.json. It names the
// file on a parse error and leaves a read error alone, for the reasons
// parsePnpmWorkspace records.
func parsePackageJSONWorkspace(root, path string) (*Workspace, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var pkgJSON struct {
		Workspaces interface{} `json:"workspaces"`
	}
	if err := json.Unmarshal(data, &pkgJSON); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", path, err)
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
//
// Both loops filter on package.json presence and then refuse anything resolving
// outside root - see requireWithinRoot for why, and for why that order matters.
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

			if err := requireWithinRoot(root, pkgPath); err != nil {
				return nil, err
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
			pkgPath := filepath.Join(root, match)

			// The same package.json gate the include loop applies, for the
			// same reason: without it a dangling symlink under a negated
			// pattern would reach requireWithinRoot, fail to resolve, and
			// abort a workspace that expands fine today. Against a
			// filesystem that holds still it cannot change which packages
			// are returned, because excluded is only ever consulted for
			// entries of packages, and every one of those passed this same
			// stat in the loop above. It is not atomic, though: a member
			// whose package.json is deleted between the two loops stays in
			// packages and no longer reaches excluded, so a negated package
			// is returned - the fail-open direction docs/adr/0001 names.
			// SECURITY.md's "Known limits" records the same non-atomicity
			// for the write-path guards.
			pkgJSON := filepath.Join(pkgPath, "package.json")
			if _, err := os.Stat(pkgJSON); err != nil {
				continue
			}

			// A negated match that escapes the root fails the whole
			// workspace, not just that pattern. That is stricter than #328's
			// scenario, which was a member read from outside the root: a
			// negated member is only ever subtracted, never read. It is
			// refused anyway because an exclusion set that depends on a path
			// outside the root is the same hostile shape, and because a
			// silent answer here decides what does get published.
			if err := requireWithinRoot(root, pkgPath); err != nil {
				return nil, err
			}

			excluded[pkgPath] = true
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

// requireWithinRoot refuses a globbed match whose real path falls outside the
// workspace root.
//
// os.DirFS refuses ".." and absolute patterns but follows symlinks, so a
// checkout containing packages/escape -> /somewhere/else otherwise has that
// directory returned as a workspace member and its manifest read from outside
// the root (#328). fsutil.WithinRoot resolves both sides fully, so a chain of
// links cannot slip past the way it would past a single-level check.
//
// This runs only after the caller has stat'd the member's package.json, which
// means the path was there a moment ago and resolvable. A resolution failure on
// it is therefore anomalous rather than the ordinary "this match is not a
// package" case, and it refuses instead of skipping. Both refusals here wrap
// ErrWorkspaceMemberRefused, which is what makes that true of the composed
// behaviour rather than only of this function: Detect swallows an error it does
// not recognise on every iteration of its walk but the first, so an unwrapped
// refusal would go back to skipping the moment the command was run from inside
// a member directory.
//
// Which way that cuts depends on which loop called. From the negation loop the
// ADR decides it: skipping would drop an exclusion and publish the package the
// maintainer excluded, the fail-open direction docs/adr/0001 exists to correct.
// From the include loop it decides nothing, because refusing and skipping both
// publish less and neither is fail-open - and the ADR files this exact case, a
// member whose package.json was there moments earlier, under judgement call
// rather than rule. The judgement made here is that such a member is a broken
// workspace rather than a directory that merely is not a package, so it is worth
// stopping the command over; one rule for both loops also keeps them from
// drifting the way the ADR's "Considered options" warns two separately-decided
// rules do.
//
// The failure can still come from the root rather than the member - a workspace
// root deleted underneath the walk resolves no better than a member does - so
// this does not attribute it. fsutil.WithinRoot names the side that failed, and
// a message here blaming the member for a broken root would send the reader to
// the wrong path.
func requireWithinRoot(root, path string) error {
	within, resolved, err := fsutil.WithinRoot(root, path)
	if err != nil {
		return fmt.Errorf("%w %s: failed to check it against the workspace root: %w",
			ErrWorkspaceMemberRefused, path, err)
	}
	if !within {
		return fmt.Errorf("%w %s: it resolves to %s, which is outside the workspace root %s: "+
			"remove the pattern that matches it, or replace the link with a directory inside the root",
			ErrWorkspaceMemberRefused, path, resolved, root)
	}
	return nil
}

// ListPackages returns all packages in the workspace with their metadata.
//
// A member that will not read, will not parse, or names no package fails the
// whole listing - the decision docs/adr/0001 records. expandGlobs already
// dropped every directory without a package.json before it reached w.Packages,
// so a failure here is a broken member of a workspace the caller asked for - a
// permission problem, a config typo, or a file deleted underneath us - and not
// the non-package directory the ADR weighed under Considered options when it
// declined to hard-fail by default. Skipping it publishes less than the caller
// asked for and still reports success.
//
// Both production callers inherit that. publishAll fails the whole `--all` run,
// and pack.indexWorkspace fails a single-package publish whose manifest carries
// workspace: dependencies, so one broken sibling stops `lnpm publish` on an
// otherwise healthy package.
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
		// returning it with an empty name carries the breakage downstream. The
		// name is absent, explicitly "", or the whole document is a JSON null,
		// which encoding/json unmarshals as a no-op; the message covers all
		// three rather than claiming the key is missing.
		if pkg.Name == "" {
			return nil, fmt.Errorf("workspace package %s has an empty or missing name", pkgJSON)
		}

		packages = append(packages, Package{
			Name:    pkg.Name,
			Version: pkg.Version,
			Path:    pkgPath,
		})
	}

	return packages, nil
}
