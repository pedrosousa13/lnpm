package cli

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/pedrosousa13/lnpm/internal/db"
	"github.com/pedrosousa13/lnpm/internal/link"
	"github.com/pedrosousa13/lnpm/internal/store"
	"github.com/pedrosousa13/lnpm/internal/ui"
	"github.com/pedrosousa13/lnpm/pkg/lockfile"
)

// RunPull re-links packages already linked in this project to whatever the
// store now holds for them. With no names it refreshes every package in
// lnpm.lock.
//
// Unlike add, pull never touches package.json: the reference there already
// points at .lnpm/<pkg>, and only the contents behind it and the lock entry
// change when the source package is republished.
func RunPull(packageNames []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	database, err := db.GetDB()
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}

	lock, err := lockfile.Load(cwd)
	if err != nil {
		return fmt.Errorf("failed to load lock file: %w", err)
	}

	// Named packages must already be linked here - pull refreshes links, it does
	// not create them. Check them all before any linking so a typo cannot leave
	// the project half-pulled.
	names := packageNames
	if len(names) == 0 {
		names = lock.List()
		if len(names) == 0 {
			fmt.Println("No linked packages to pull")
			return nil
		}
		// lock.List() ranges over a map, so sort to keep the report stable
		// between runs.
		sort.Strings(names)
	} else {
		for _, name := range names {
			if !lock.Has(name) {
				return fmt.Errorf("package %s is not linked in this project", name)
			}
		}
	}

	// Which channel this project follows for each package. Resolving by name
	// alone would answer with whatever latest points at, so a consumer that asked
	// for beta would be relinked onto the stable release and have its lock
	// rewritten to match - the carry-over onto latest that asking for a channel
	// exists to prevent, reached through pull instead of through a moved tag.
	held, err := linksOfProject(database, cwd)
	if err != nil {
		return err
	}

	s, err := store.New()
	if err != nil {
		return fmt.Errorf("failed to access store: %w", err)
	}

	linker := link.New(cwd)

	var failed []error
	refreshed := 0
	upToDate := 0
	liveLinked := 0
	lockChanged := false
	lastVersion := ""

	for _, name := range names {
		entry, _ := lock.Get(name)

		// A package added with --link resolves to its source directory, so there
		// is nothing to refresh: relinking it from the store would replace the
		// live link with a snapshot copy and silently end the live updates the
		// consumer was relying on.
		//
		// A refusal - the project's .lnpm is not a directory the project owns -
		// joins the per-package failures collected below rather than being
		// reported as a skip, which would claim the package was deliberately
		// left alone when nothing about it could be established. It is recorded
		// before the "Pulling <name>... " line is printed, matching every other
		// failure that happens this early.
		live, liveErr := linker.IsLiveLinked(name)
		if liveErr != nil {
			failed = append(failed, fmt.Errorf("%s: %w", name, liveErr))
			continue
		}
		if live {
			fmt.Printf("Pulling %s... skipped (live link to source)\n", name)
			liveLinked++
			continue
		}

		tag := held.tag(name)
		var pkg *db.Package
		if !isDefaultTag(tag) {
			pkg, err = database.ResolveTag(name, tag)
			if err != nil {
				failed = append(failed, fmt.Errorf("%s: failed to resolve tag %s: %w", name, tag, err))
				continue
			}
			if pkg == nil {
				// Reported rather than quietly resolved by name: falling back
				// would move the project onto latest, which is the one outcome
				// this whole lookup exists to avoid.
				failed = append(failed, fmt.Errorf("%s: tag %s is no longer set in the store - re-add it under a tag that is", name, tag))
				continue
			}
		} else {
			pkg, err = database.GetPackageByName(name)
			if err != nil {
				failed = append(failed, fmt.Errorf("%s: failed to look up package: %w", name, err))
				continue
			}
			if pkg == nil {
				failed = append(failed, fmt.Errorf("%s: not found in store - did you run 'lnpm publish' in the package directory", name))
				continue
			}
		}

		fmt.Printf("Pulling %s%s... ", name, tagSuffix(tag))

		if entry.Version == pkg.Version && entry.Hash == pkg.ContentHash {
			fmt.Printf("already up to date (%s)\n", pkg.Version)
			upToDate++
			continue
		}

		// Every path out of here closes the "Pulling <name>... " line, so a
		// failure names itself where it happened instead of leaving the line
		// dangling until the end-of-run report.
		files, err := storeFilesForLink(database, s, pkg)
		if err != nil {
			fmt.Printf("failed to get package files: %v\n", err)
			failed = append(failed, fmt.Errorf("%s: failed to get package files: %w", name, err))
			continue
		}

		// The link type Link reports is deliberately dropped: no command surfaces
		// the stored one, and pull does not change it. The per-file counts are
		// kept: pull runs the same incremental relink push does, and reporting
		// nothing would make it the one command that leaves the user guessing
		// whether the refresh cost the whole package.
		linkRes, err := linker.Link(pkg.Name, pkg.StorePath, files)
		if err != nil {
			fmt.Printf("failed to link package: %v\n", err)
			failed = append(failed, fmt.Errorf("%s: failed to link package: %w", name, err))
			continue
		}

		// Keep the original specifier: it lives only in the lock file once add
		// has rewritten package.json, and remove/retreat need it to restore the
		// dependency.
		lock.Add(name, lockfile.Package{
			Version:         pkg.Version,
			Hash:            pkg.ContentHash,
			Source:          pkg.SourcePath,
			Linked:          time.Now(),
			OriginalVersion: entry.OriginalVersion,
		})
		lockChanged = true

		fmt.Printf("updated %s -> %s (%d changed, %d unchanged)\n", entry.Version, pkg.Version, linkRes.Changed, linkRes.Unchanged)
		refreshed++
		lastVersion = pkg.Version

		// Leave the database describing what was just linked.
		//
		// Usually there is nothing to do: a publish that moves a tag carries the
		// links following it onto the version it now names, so the row add wrote
		// already names this one. That breaks down when a tag has been deleted
		// and set again by a publish - there is no previous version to carry
		// links from - and the row is then left on a build this project no longer
		// has. That row is what remove deletes, what gc reads as a reason to keep
		// that build, and what `publish --tag --push` decides whom to push to
		// from, so all three would act on the wrong version.
		//
		// Only an existing row is repointed. pull refreshes links rather than
		// creating them, and a project with no row for this package had none
		// before pull ran either.
		if l, ok := held[name]; ok && l.PackageID != pkg.ID {
			repointed := &db.Link{
				PackageID: pkg.ID,
				ProjectID: l.ProjectID,
				LinkType:  l.LinkType,
				Tag:       l.Tag,
			}
			// Reported as a failed pull, not warned past. Leaving the old row in
			// place is not a smaller version of the same state: it names the
			// build this project no longer has, so gc keeps that one as linked
			// and reads the build now on disk as consumed by nobody - and
			// deletes it. The files were refreshed above, which is what makes
			// the stale row dangerous rather than merely untidy.
			if err := database.InsertLink(repointed); err != nil {
				fmt.Printf("  %s Failed to record the link for %s: %v\n", ui.IconWarn(), name, err)
				failed = append(failed, fmt.Errorf("%s: failed to record the link: %w", name, err))
			}
		}
	}

	// A failed save must not swallow the per-package failures: both are
	// reported, and both are returned.
	var saveErr error
	if lockChanged {
		if err := lock.Save(cwd); err != nil {
			saveErr = fmt.Errorf("failed to save lock file: %w", err)
		}
	}

	// The success line comes first so a partially failed run ends on the warning
	// block it exits non-zero for.
	if refreshed == 1 && len(packageNames) == 1 {
		fmt.Printf("%s Pulled %s@%s\n", ui.IconOK(), names[0], lastVersion)
	} else if refreshed > 0 {
		fmt.Printf("%s Pulled %d package(s)\n", ui.IconOK(), refreshed)
	} else if upToDate > 0 && len(failed) == 0 {
		fmt.Printf("%s Already up to date\n", ui.IconOK())
	}

	// Live-linked packages are reported apart from the up-to-date ones. Nothing
	// about them was compared against the store, so folding them into "already
	// up to date" would claim a store parity that was never checked - a pull
	// where every package is live-linked said so while the store held a newer
	// version and the lock still held the old one.
	if liveLinked > 0 && len(failed) == 0 {
		fmt.Printf("%s Skipped %d live-linked package(s)\n", ui.IconOK(), liveLinked)
	}

	if len(failed) > 0 {
		fmt.Printf("\n%s Some packages failed:\n", ui.IconWarn())
		for _, err := range failed {
			fmt.Printf("  - %v\n", err)
		}
	}
	if saveErr != nil {
		fmt.Printf("\n%s %v\n", ui.IconWarn(), saveErr)
	}

	var pullErr error
	if len(failed) > 0 {
		pullErr = fmt.Errorf("%d of %d package(s) failed to pull", len(failed), len(names))
	}
	return errors.Join(pullErr, saveErr)
}
