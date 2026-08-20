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

	// Collect "<field>: <pkg> => <ref>" for every lnpm reference found across
	// the standard dependency maps.
	var found []string
	for _, field := range []string{"dependencies", "devDependencies", "optionalDependencies", "peerDependencies"} {
		deps, ok := pkgJSON[field].(map[string]interface{})
		if !ok {
			continue
		}
		for name, v := range deps {
			ref, ok := v.(string)
			if ok && isLnpmReference(ref) {
				found = append(found, fmt.Sprintf("  %s.%s -> %s", field, name, ref))
			}
		}
	}

	var problems []string

	if len(found) > 0 {
		sort.Strings(found)
		fmt.Printf("%s Found %d lnpm reference(s) in package.json:\n", iconFail(), len(found))
		for _, line := range found {
			fmt.Println(line)
		}
		fmt.Printf("\n  %s Run 'lnpm retreat --force' to restore original dependencies before publishing\n", iconTip())
		problems = append(problems, fmt.Sprintf("%d lnpm reference(s) found in package.json", len(found)))
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
