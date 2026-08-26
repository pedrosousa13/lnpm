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
//
// A pinned package is the one thing pull will not move, and it answers the two
// ways it can be asked differently: a sweep skips it and says so, and a request
// that names it is refused. See ADR-0006, and pinnedPullError.
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

	// A pinned package the user named by hand refuses the whole run, and it
	// refuses it from here - alongside the "not linked in this project" check
	// above, before anything is linked - so a run that cannot be honoured leaves
	// the project exactly as it found it rather than half-pulled.
	//
	// Naming a package is a request rather than a sweep, and this request cannot
	// be honoured: there is a newer build in the store and the link says not to
	// take it. That is deliberately not what a live link does, which is skipped
	// even when it is named. A live link has nothing to refresh, so a skip is a
	// complete answer; a pinned link has something to refresh and a reason not
	// to, so the user who asked has to be told which of the two they are in and
	// how to get out of it. See ADR-0006.
	if len(packageNames) > 0 {
		for _, name := range names {
			if held.pinned(name) {
				return pinnedPullError(name)
			}
		}
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
	pinned := 0
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

		// A pinned package follows no channel, so there is nothing for a sweep
		// to resolve it through and nothing it should be moved onto. Reaching
		// this at all means the pull was a bare one - a named pin was refused
		// above - and a bare pull is a sweep over the whole lock, so one pinned
		// package must not stop the other twenty being refreshed.
		//
		// It is reported rather than passed over, because silence is the whole
		// of #300: a rollback undone by a pull the user ran for some other
		// package is a defect precisely because nothing said so. The version is
		// named so the line says what the project is being left on, and the
		// closing count says how to move it.
		if held.pinned(name) {
			fmt.Printf("Pulling %s... skipped (pinned to %s)\n", name, entry.Version)
			pinned++
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
				// Carried rather than assumed false. Nothing pinned reaches
				// here today - a pinned package is skipped or refused above -
				// but this literal is built field by field, and one that
				// dropped the pin would unpin a project as a side effect of a
				// refresh it was never meant to get.
				Pinned: l.Pinned,
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

	// Pinned packages are counted apart from both of those, for the live-linked
	// line's own reason and one more. Nothing about them was compared against
	// the store either, and a pin is a state the user can leave, so the count
	// carries the way out - a skip nobody knows how to undo is only half a
	// report.
	if pinned > 0 && len(failed) == 0 {
		fmt.Printf("%s Skipped %d pinned package(s); run 'lnpm add <package>' to unpin one\n", ui.IconOK(), pinned)
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

// pinnedPullError refuses a pull that names a pinned package.
//
// It names the unpin, and the unpin has to be a command that exists and does
// something: `lnpm add <package>` with no @suffix resolves through the default
// channel and clears the pin, which is the same act as saying "follow the
// channel again". A refusal pointing at nothing would leave the user with a
// package they cannot pull and no way to change that.
//
// It says pinned rather than naming the build. The version is in lnpm.lock and
// in `lnpm status`, and this message's job is the state and the way out of it -
// a user who wants to see which build they are on has `lnpm list <package>
// --versions`, which shows the alternatives too.
func pinnedPullError(name string) error {
	return fmt.Errorf("%s is pinned to one build, so 'lnpm pull' will not move it; run 'lnpm add %s' to unpin it and follow %s again",
		name, name, db.DefaultTag)
}
