package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/pedrosousa13/lnpm/internal/config"
	"github.com/pedrosousa13/lnpm/internal/db"
	"github.com/pedrosousa13/lnpm/internal/gitignore"
	"github.com/pedrosousa13/lnpm/internal/link"
	"github.com/pedrosousa13/lnpm/internal/pkgjson"
	"github.com/pedrosousa13/lnpm/internal/store"
	"github.com/pedrosousa13/lnpm/pkg/lockfile"
)

// RunRestore re-links every package recorded in the snapshot `lnpm retreat`
// left behind, undoing the retreat.
//
// It merges rather than replaces: a package the user added again between the
// retreat and the restore wins over the snapshot's entry for the same name, and
// packages the snapshot never saw are left alone. Nothing is unlinked.
//
// Two parts of the pre-retreat state cannot be rebuilt from the snapshot,
// because the lock file records neither. The first is whether a package was
// added with --link: everything is restored as a store copy, which is what the
// file:.lnpm/<pkg> specifier the command writes describes. The second is which
// dependency field a package belonged in; that is recovered from package.json,
// where retreat has just put the original specifier back, and reported when the
// package has no entry there to recover it from.
func RunRestore() error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	pkgJSONPath := filepath.Join(cwd, "package.json")
	if _, err := os.Stat(pkgJSONPath); err != nil {
		return fmt.Errorf("no package.json found in current directory")
	}

	snapshotName := filepath.Base(lockfile.RetreatPath(cwd))

	snapshot, err := lockfile.LoadRetreat(cwd)
	if err != nil {
		return fmt.Errorf("could not read %s: %w", snapshotName, err)
	}
	if snapshot == nil || len(snapshot.List()) == 0 {
		fmt.Printf("Nothing to restore: no packages recorded by a previous 'lnpm retreat'\n")
		return nil
	}

	database, err := db.GetDB()
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}

	s, err := store.New()
	if err != nil {
		return fmt.Errorf("failed to access store: %w", err)
	}

	lock, err := lockfile.Load(cwd)
	if err != nil {
		return fmt.Errorf("failed to load lock file: %w", err)
	}

	fmt.Printf("Restoring from %s...\n", snapshotName)

	cfg := config.Get()
	if cfg.ShouldManageGitignore() {
		if added, err := gitignore.EnsureInGitignore(cwd, ".lnpm/"); err != nil {
			fmt.Printf("  %s Could not update .gitignore: %v\n", iconWarn(), err)
		} else if added {
			fmt.Printf("  %s Added .lnpm/ to .gitignore\n", iconOK())
		}
	}

	linker := link.New(cwd)

	// Sorted so the report reads the same way on every run; lock.List() walks a
	// map.
	names := snapshot.List()
	sort.Strings(names)

	failed := 0
	restored := 0
	for _, name := range names {
		entry, _ := snapshot.Get(name)

		// A package added again since the retreat is already linked, and the
		// user's newer add is the one to keep.
		if lock.Has(name) {
			fmt.Printf("  %s %s was added again since the retreat; keeping that link\n", iconOK(), name)
			continue
		}

		// The store keeps the latest published version per package, so the
		// version the snapshot recorded may since have been superseded. Report
		// it the way add does and carry on with the rest.
		pkg, err := database.GetPackageByName(name)
		if err != nil {
			fmt.Printf("  %s %s: failed to look up package: %v\n", iconFail(), name, err)
			failed++
			continue
		}
		if pkg == nil {
			fmt.Printf("  %s %s: not found in store. Did you run 'lnpm publish' in the package directory?\n", iconFail(), name)
			failed++
			continue
		}
		if entry.Version != "" && pkg.Version != entry.Version {
			fmt.Printf("  %s %s: %v\n", iconFail(), name, versionNotInStoreError(entry.Version, name, pkg.Version))
			failed++
			continue
		}

		// Read package.json before anything is written to it, so the specifier
		// retreat restored becomes this link's original version.
		deps, err := readPackageJSONDeps(pkgJSONPath, name, false)
		if err != nil {
			fmt.Printf("  %s %s: failed to read package.json: %v\n", iconFail(), name, err)
			failed++
			continue
		}
		originalVersion := deps.originalVersion
		if originalVersion == "" {
			originalVersion = entry.OriginalVersion
		}

		linkType, err := linkPackage(linker, s, pkg, false)
		if err != nil {
			fmt.Printf("  %s %s: %v\n", iconFail(), name, err)
			failed++
			continue
		}

		// ORDERING CONSTRAINT: the same one add documents. The specifier lives
		// only in package.json and the write below overwrites it, so the lock
		// entry that carries it has to be on disk first.
		lock.Add(name, lockfile.Package{
			Version:         pkg.Version,
			Hash:            pkg.ContentHash,
			Source:          pkg.SourcePath,
			Linked:          time.Now(),
			OriginalVersion: originalVersion,
		})
		if err := lock.Save(cwd); err != nil {
			return fmt.Errorf("failed to save lock file: %w", err)
		}

		if !dependencyFieldKnown(deps.src, name) {
			fmt.Printf("  %s %s was not in package.json before the retreat, so its field is unknown; restoring it into %s\n",
				iconWarn(), name, deps.field)
		}

		// The lock entry stays on a failure here: package.json is untouched, so
		// it still holds whatever retreat left, and the entry records that as the
		// original version - which is what a later remove or retreat needs.
		if err := writeLnpmReference(pkgJSONPath, name, false, false); err != nil {
			fmt.Printf("  %s %s: failed to update package.json: %v\n", iconFail(), name, err)
			failed++
			continue
		}

		recordRestoredLink(database, cwd, pkg, linkType)

		fmt.Printf("%s Restored %s@%s\n", iconOK(), name, pkg.Version)
		restored++
	}

	if failed == 0 {
		if err := os.Remove(lockfile.RetreatPath(cwd)); err != nil && !os.IsNotExist(err) {
			fmt.Printf("  %s Failed to remove %s: %v\n", iconWarn(), snapshotName, err)
		}
	}

	fmt.Println()
	if failed > 0 {
		fmt.Printf("  %s %s kept; re-run 'lnpm restore' once the packages above are available\n", iconTip(), snapshotName)
		return fmt.Errorf("%d of %d package(s) failed to restore", failed, len(names))
	}

	fmt.Printf("%s Restore complete!\n", iconOK())
	if restored > 0 {
		fmt.Printf("\n  %s Run 'npm install' if you need to resolve peer dependencies\n", iconTip())
	}
	return nil
}

// recordRestoredLink registers the project and its link to pkg, so `lnpm list`
// and `lnpm push` see the restored package the way they see an added one. A
// failure here costs only bookkeeping - the link on disk is already in place -
// so it is reported and not treated as a failed restore.
func recordRestoredLink(database *db.DB, cwd string, pkg *db.Package, linkType link.LinkType) {
	proj := &db.Project{
		Path:           cwd,
		Name:           getProjectName(cwd),
		PackageManager: string(config.DetectPackageManager(cwd)),
	}
	if err := database.InsertProject(proj); err != nil {
		fmt.Printf("  %s Failed to register project: %v\n", iconWarn(), err)
		return
	}

	existingProj, err := database.GetProjectByPath(cwd)
	if err != nil {
		fmt.Printf("  %s Failed to get project: %v\n", iconWarn(), err)
		return
	}

	dbLink := &db.Link{
		PackageID: pkg.ID,
		ProjectID: existingProj.ID,
		LinkType:  string(linkType),
	}
	if err := database.InsertLink(dbLink); err != nil {
		fmt.Printf("  %s Failed to record link for %s: %v\n", iconWarn(), pkg.Name, err)
	}
}

// dependencyFieldKnown reports whether package.json still says which dependency
// field a package belongs in. When it does not - the package was added with
// --dev or --pure without ever having an entry, and retreat dropped what add had
// written - restore has to guess, and says so.
func dependencyFieldKnown(pkgJSON []byte, name string) bool {
	for _, field := range []string{"dependencies", "devDependencies"} {
		has, err := pkgjson.HasDep(pkgJSON, field, name)
		if err != nil {
			// Unparseable package.json is reported by the read that follows;
			// claiming the field is known keeps this from adding noise to it.
			return true
		}
		if has {
			return true
		}
	}
	return false
}
