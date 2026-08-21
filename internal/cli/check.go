package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pedrosousa13/lnpm/internal/pack"
	"github.com/pedrosousa13/lnpm/internal/workspace"
	"github.com/pedrosousa13/lnpm/pkg/lockfile"
)

// RunCheck reports whatever lnpm has left in the current project that an
// `npm publish` from here would carry into the tarball, and returns a non-nil
// error if there is any. It is the guard the README puts between
// `lnpm retreat --force` and `npm publish`.
//
// Two things qualify. The first is a leftover lnpm reference (file:.lnpm/ or
// link:.lnpm/) in package.json, which would ship a dependency specifier that
// resolves to nothing on the consumer's machine. The second is the snapshot
// `lnpm retreat` leaves behind, which records an absolute source path per
// linked package.
//
// Inside a workspace the reference scan covers the workspace root's manifest
// and every member's as well as the one here, because a guard that passes from
// the repo root while a member still holds a file:.lnpm/ reference is worse than
// no guard at all. The snapshot half stays on the current directory: the
// snapshot is written where retreat ran.
func RunCheck() error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	pkgJSONPath := filepath.Join(cwd, "package.json")
	data, err := os.ReadFile(pkgJSONPath)
	if err != nil {
		return fmt.Errorf("no package.json found in current directory")
	}

	var pkgJSON map[string]interface{}
	if err := json.Unmarshal(data, &pkgJSON); err != nil {
		return fmt.Errorf("failed to parse package.json: %w", err)
	}

	manifests, inWorkspace, err := checkManifests(cwd, pkgJSON)
	if err != nil {
		return err
	}

	// Collect "<field>: <pkg> => <ref>" for every lnpm reference found across
	// the standard dependency maps of every manifest in scope, counting the
	// manifests that carry one. The count is of manifests rather than of the
	// labels on them: the workspace root is one of the manifests and is not a
	// workspace package, and two members can carry the same name.
	var found []string
	dirty := 0
	for _, manifest := range manifests {
		before := len(found)
		for _, field := range []string{"dependencies", "devDependencies", "optionalDependencies", "peerDependencies"} {
			deps, ok := manifest.pkgJSON[field].(map[string]interface{})
			if !ok {
				continue
			}
			for name, v := range deps {
				ref, ok := v.(string)
				if !ok || !isLnpmReference(ref) {
					continue
				}
				if inWorkspace {
					found = append(found, fmt.Sprintf("  %s: %s.%s -> %s", manifest.label, field, name, ref))
					continue
				}
				found = append(found, fmt.Sprintf("  %s.%s -> %s", field, name, ref))
			}
		}
		if len(found) > before {
			dirty++
		}
	}

	var problems []string

	if len(found) > 0 {
		sort.Strings(found)
		if inWorkspace {
			fmt.Printf("%s Found %d lnpm reference(s) in %d package.json file(s):\n", iconFail(), len(found), dirty)
		} else {
			fmt.Printf("%s Found %d lnpm reference(s) in package.json:\n", iconFail(), len(found))
		}
		for _, line := range found {
			fmt.Println(line)
		}
		fmt.Printf("\n  %s Run 'lnpm retreat --force' to restore original dependencies before publishing\n", iconTip())
		if inWorkspace {
			problems = append(problems, fmt.Sprintf("%d lnpm reference(s) found in %d package.json file(s)", len(found), dirty))
		} else {
			problems = append(problems, fmt.Sprintf("%d lnpm reference(s) found in package.json", len(found)))
		}
	}

	if publishableSnapshot(cwd, filesField(pkgJSON)) {
		fmt.Printf("%s %s is in the project root and nothing here keeps it out of a tarball\n", iconFail(), lockfile.RetreatFileName)
		fmt.Printf("  It is lnpm's record of what 'lnpm retreat' unlinked, and it holds an absolute path per package\n")
		fmt.Printf("\n  %s Add %s to .npmignore or .gitignore, or list only what you ship in package.json \"files\"\n", iconTip(), lockfile.RetreatFileName)
		fmt.Printf("      'lnpm restore' consumes the snapshot, but only after you have published\n")
		problems = append(problems, lockfile.RetreatFileName+" would be published")
	}

	if len(problems) == 0 {
		fmt.Printf("%s Nothing lnpm left behind would be published\n", iconOK())
		return nil
	}
	return errors.New(strings.Join(problems, "; "))
}

// checkManifest is one package.json the reference scan applies to: the parsed
// document, and a label naming the package it came from. The label is set only
// inside a workspace, where the report has more than one manifest to tell apart.
type checkManifest struct {
	label   string
	pkgJSON map[string]interface{}
}

// checkManifests returns every manifest the reference scan covers: the one in
// the current directory, plus - when that directory sits in a workspace - the
// workspace root's and every member's. The second return value says which of
// those two it was, so the report does not have to infer it from the labels.
//
// A workspace whose member list will not resolve is an error rather than a
// reason to fall back to the current directory alone. Checking only the root
// and reporting success is precisely the failure the workspace-wide scan exists
// to remove, so an unresolvable workspace fails loudly instead. That covers a
// directory below a broken workspace too: a malformed pattern makes membership
// unknown, and unknown is not the same as "no workspace here".
//
// Both errors add the location and leave the wrapped error to say what is
// wrong with it, because workspace.Detect names the offending pattern or the
// config file it could not read, and ListPackages names the offending manifest.
func checkManifests(cwd string, cwdPkgJSON map[string]interface{}) ([]checkManifest, bool, error) {
	ws, err := workspace.Detect(cwd)
	if err != nil {
		return nil, false, fmt.Errorf("cannot resolve the workspace for %s: %w", cwd, err)
	}
	if ws == nil {
		return []checkManifest{{pkgJSON: cwdPkgJSON}}, false, nil
	}

	packages, err := ws.ListPackages()
	if err != nil {
		return nil, false, fmt.Errorf("cannot resolve the workspace at %s: %w", ws.Root, err)
	}

	// The current directory's manifest is already read and parsed; the rest are
	// the workspace root's - which a check run from a member would otherwise
	// leave unscanned - and one per member.
	manifests := []checkManifest{{
		label:   manifestLabel(nameField(cwdPkgJSON), ws.Root, cwd),
		pkgJSON: cwdPkgJSON,
	}}
	seen := map[string]bool{canonicalPath(cwd): true}

	for _, pkg := range append([]workspace.Package{{Path: ws.Root}}, packages...) {
		key := canonicalPath(pkg.Path)
		if seen[key] {
			continue
		}
		seen[key] = true

		manifestPath := filepath.Join(pkg.Path, "package.json")
		data, err := os.ReadFile(manifestPath)
		if err != nil {
			return nil, false, fmt.Errorf("failed to read %s: %w", manifestPath, err)
		}

		var pkgJSON map[string]interface{}
		if err := json.Unmarshal(data, &pkgJSON); err != nil {
			return nil, false, fmt.Errorf("failed to parse %s: %w", manifestPath, err)
		}

		name := pkg.Name
		if name == "" {
			name = nameField(pkgJSON)
		}
		manifests = append(manifests, checkManifest{
			label:   manifestLabel(name, ws.Root, pkg.Path),
			pkgJSON: pkgJSON,
		})
	}

	return manifests, true, nil
}

// manifestLabel names a manifest in check's report. The package name is what a
// reader can act on, and a manifest without one - a private workspace root
// often has neither name nor version - falls back to its path relative to the
// workspace root.
func manifestLabel(name, root, path string) string {
	if name != "" {
		return name
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." {
		return "workspace root"
	}
	return filepath.ToSlash(rel)
}

// nameField returns the package.json "name", or "" when it is absent or is not
// a string.
func nameField(pkgJSON map[string]interface{}) string {
	name, _ := pkgJSON["name"].(string)
	return name
}

// canonicalPath resolves path as far as the filesystem allows, so that two
// spellings of one directory compare equal. The working directory and a
// workspace member path reach this function by different routes, and on Windows
// a cwd can come back as an 8.3 short name while the member path is spelled
// long; EvalSymlinks reconciles both, and also the case of each component.
//
// Every path reaching here exists - the current directory was just read, and
// expandGlobs kept only directories holding a package.json - so EvalSymlinks
// resolves it, macOS /var to /private/var included, and the Clean fallback is
// for a directory removed underneath us. A missed match there costs a manifest
// scanned twice, never a wrong verdict.
func canonicalPath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return filepath.Clean(path)
}

// publishableSnapshot reports whether the retreat snapshot is sitting in the
// project root with nothing to keep it out of a published tarball.
//
// Its presence is not itself a problem: the documented flow is retreat, check,
// `npm publish`, restore, so the snapshot is on disk at check time by design,
// and failing on that alone would fail every publish of every project that uses
// restore at all. It is a problem only when it would actually be packed, because
// it records an absolute source path for every package that was linked.
//
// `lnpm publish` never packs it - internal/pack excludes it outright - so the
// case this covers is the npm CLI, which reads none of lnpm's rules. What it
// does read is the "files" field and a root .npmignore, falling back to a root
// .gitignore, which is why one line in an ignore file settles this for good.
func publishableSnapshot(dir string, files []string) bool {
	if _, err := os.Stat(lockfile.RetreatPath(dir)); err != nil {
		return false
	}
	return !pack.ExcludedByProjectRules(dir, files, lockfile.RetreatFileName)
}

// filesField returns the package.json "files" entries, dropping anything that is
// not a string, the way pack's own reader does. A "files" field of some other
// shape entirely is no whitelist and reads as nil.
func filesField(pkgJSON map[string]interface{}) []string {
	raw, ok := pkgJSON["files"].([]interface{})
	if !ok {
		return nil
	}
	files := make([]string, 0, len(raw))
	for _, entry := range raw {
		if s, ok := entry.(string); ok {
			files = append(files, s)
		}
	}
	return files
}
