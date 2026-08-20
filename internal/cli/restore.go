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
// Each name leaves the snapshot as it is dealt with, so what is on disk at any
// point is the work still outstanding. A run that fails on some package keeps
// the snapshot for the re-run it advises, and the re-run then sees only what is
// genuinely left.
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

	snapshotName := lockfile.RetreatFileName

	// The error already names the file it failed on, so it is returned as it
	// came: wrapping it here would give the snapshot two names in one message.
	snapshot, err := lockfile.LoadRetreat(cwd)
	if err != nil {
		return err
	}
	if snapshot == nil {
		fmt.Printf("Nothing to restore: no snapshot from a previous 'lnpm retreat'\n")
		return nil
	}

	// Sorted so the report reads the same way on every run; List() walks a map.
	names := snapshot.List()
	sort.Strings(names)

	if len(names) == 0 {
		// A snapshot is not the same thing as no snapshot: this one is a retreat
		// that found a lock file with nothing in it. There is nothing to put
		// back, but the file is spent, and leaving it would stop every later
		// restore on it and make every later retreat look like a second one.
		if err := os.Remove(lockfile.RetreatPath(cwd)); err != nil && !os.IsNotExist(err) {
			fmt.Printf("  %s Failed to remove %s: %v\n", iconWarn(), snapshotName, err)
		}
		fmt.Printf("Nothing to restore: the last 'lnpm retreat' recorded no packages\n")
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

	failed := 0
	restored := 0
	attempted := 0
	for _, name := range names {
		entry, _ := snapshot.Get(name)

		// A package already in the lock file was added again by the user since
		// the retreat, and their newer add is the one to keep. Restore's own
		// work cannot be mistaken for it. A package a previous run finished is
		// no longer in the snapshot, so it never reaches this loop; a package a
		// previous run could not finish left no lock entry, because an entry is
		// written only once the package.json write it describes has landed. So a
		// name that is in the snapshot and in the lock file is the user's doing.
		if lock.Has(name) {
			fmt.Printf("  %s %s was added again since the retreat; keeping that link\n", iconOK(), name)
			consumeSnapshotEntry(snapshot, cwd, name)
			continue
		}
		attempted++

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

		// Nothing is recorded for this package until package.json holds the
		// reference: on a failure here it still holds whatever retreat left, and
		// the snapshot - kept, because this counts as a failure - still records
		// the original version, so the re-run has everything it needs.
		if err := writeLnpmReference(pkgJSONPath, name, false, false); err != nil {
			fmt.Printf("  %s %s: failed to update package.json: %v\n", iconFail(), name, err)
			failed++
			continue
		}

		if !dependencyFieldKnown(deps.src, name) {
			fmt.Printf("  %s %s was not in package.json before the retreat, so its field is unknown; restoring it into %s\n",
				iconWarn(), name, deps.field)
			fmt.Printf("      If it was added with --pure, delete that entry: --pure keeps a package out of package.json\n")
		}

		// ORDERING CONSTRAINT, and it is the reverse of the one add documents.
		// add saves the lock entry before rewriting package.json because the
		// specifier the entry carries lives nowhere else, so a failed rewrite
		// would strand it. Restore holds a second copy of it: the snapshot's
		// entry, which records the same original version and is not dropped
		// until the write has landed. So the entry can wait for the write - and
		// it must, because an entry written ahead of the write is
		// indistinguishable, on the re-run restore itself advises, from a
		// package the user re-added, and would have that re-run skip the very
		// package it was run to finish.
		//
		// Saved per package rather than once at the end for the same reason: an
		// entry batched to the end would be missing for every package whose
		// reference is already in package.json when a later one fails hard.
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
		consumeSnapshotEntry(snapshot, cwd, name)

		recordRestoredLink(database, cwd, pkg, linkType)

		fmt.Printf("%s Restored %s@%s\n", iconOK(), name, pkg.Version)
		restored++
	}

	// Nothing failed means every name was consumed above, so the file on disk is
	// an empty snapshot. Removing it says the same thing and says it to every
	// later retreat too, which reads a snapshot that is merely empty as an
	// earlier retreat to merge into.
	if failed == 0 {
		if err := os.Remove(lockfile.RetreatPath(cwd)); err != nil && !os.IsNotExist(err) {
			fmt.Printf("  %s Failed to remove %s: %v\n", iconWarn(), snapshotName, err)
		}
	}

	fmt.Println()
	if failed > 0 {
		fmt.Printf("  %s %s kept, holding only what is left; re-run 'lnpm restore' once the packages above are available\n", iconTip(), snapshotName)
		// Counted against what was attempted, not against the snapshot: a
		// package skipped because the user re-added it was never restore's to
		// fail at, and counting it understates the share of the run that did.
		return fmt.Errorf("%d of %d package(s) failed to restore", failed, attempted)
	}

	fmt.Printf("%s Restore complete!\n", iconOK())
	if restored > 0 {
		fmt.Printf("\n  %s Run 'npm install' if you need to resolve peer dependencies\n", iconTip())
	}
	return nil
}

// consumeSnapshotEntry drops name from the snapshot and writes what is left back
// to disk, so the snapshot always describes the work still outstanding rather
// than the work the retreat once handed over.
//
// It is what makes a re-run after a partial failure honest. The snapshot has to
// survive a run that failed on any package, because the packages that failed
// still need it; kept whole, it would also still name every package this run did
// restore, and the re-run would find them in the lock file and report restore's
// own work back to the user as packages they re-added. It would also let a
// package the user has since removed be re-linked by the next restore.
//
// A failure to write is reported and not treated as a failed restore: the link
// is on disk and the lock file records it, so the run's work stands, and the
// cost is a stale line in a report the user may never see.
func consumeSnapshotEntry(snapshot *lockfile.LockFile, cwd, name string) {
	snapshot.Remove(name)
	if err := snapshot.SaveRetreat(cwd); err != nil {
		fmt.Printf("  %s Failed to update %s: %v\n", iconWarn(), lockfile.RetreatFileName, err)
	}
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
//
// HasDep only fails on a package.json that is not a JSON object, which the read
// this call follows has already parsed as one, so the error cannot arrive here.
// It is folded into "the field is not known" rather than given a branch of its
// own: the answer would be a guess either way, and the caller says so.
func dependencyFieldKnown(pkgJSON []byte, name string) bool {
	for _, field := range []string{"dependencies", "devDependencies"} {
		if has, err := pkgjson.HasDep(pkgJSON, field, name); err == nil && has {
			return true
		}
	}
	return false
}
