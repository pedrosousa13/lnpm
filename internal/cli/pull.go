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
		if linker.IsLiveLinked(name) {
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

		// The link type Link reports is deliberately dropped, and no db link row
		// is written: publishing updates the existing package row in place, so
		// the row add recorded still points at the package pull just linked, and
		// no command surfaces the stored link type. The per-file counts are
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
		fmt.Printf("%s Pulled %s@%s\n", iconOK(), names[0], lastVersion)
	} else if refreshed > 0 {
		fmt.Printf("%s Pulled %d package(s)\n", iconOK(), refreshed)
	} else if upToDate > 0 && len(failed) == 0 {
		fmt.Printf("%s Already up to date\n", iconOK())
	}

	// Live-linked packages are reported apart from the up-to-date ones. Nothing
	// about them was compared against the store, so folding them into "already
	// up to date" would claim a store parity that was never checked - a pull
	// where every package is live-linked said so while the store held a newer
	// version and the lock still held the old one.
	if liveLinked > 0 && len(failed) == 0 {
		fmt.Printf("%s Skipped %d live-linked package(s)\n", iconOK(), liveLinked)
	}

	if len(failed) > 0 {
		fmt.Printf("\n%s Some packages failed:\n", iconWarn())
		for _, err := range failed {
			fmt.Printf("  - %v\n", err)
		}
	}
	if saveErr != nil {
		fmt.Printf("\n%s %v\n", iconWarn(), saveErr)
	}

	var pullErr error
	if len(failed) > 0 {
		pullErr = fmt.Errorf("%d of %d package(s) failed to pull", len(failed), len(names))
	}
	return errors.Join(pullErr, saveErr)
}
