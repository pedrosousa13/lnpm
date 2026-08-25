package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/pedrosousa13/lnpm/internal/config"
	"github.com/pedrosousa13/lnpm/internal/db"
	"github.com/pedrosousa13/lnpm/internal/gitignore"
	"github.com/pedrosousa13/lnpm/internal/link"
	"github.com/pedrosousa13/lnpm/internal/pack"
	"github.com/pedrosousa13/lnpm/internal/shellcmd"
	"github.com/pedrosousa13/lnpm/internal/ui"
	"github.com/pedrosousa13/lnpm/pkg/lockfile"
)

// RunRetreat removes all lnpm changes from the current project
func RunRetreat(force bool, runInstall bool) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	// Check for lnpm.lock
	lock, err := lockfile.Load(cwd)
	// A missing lock file loads as an empty one, so an error here means the file
	// exists but could not be read or parsed, and lock is nil. Abort instead of
	// cleaning up: without the lock we cannot restore the original package.json
	// dependencies, so removing .lnpm/ and lnpm.lock would leave the project
	// half-retreated.
	if err != nil {
		// The wrapped error names the file it failed on, so repeating the name
		// here would give one artifact two spellings in one message.
		return fmt.Errorf("%w\n\nHint: Fix or remove lnpm.lock, then re-run 'lnpm retreat' to clean up the rest", err)
	}
	if len(lock.List()) == 0 {
		// Check for .lnpm directory
		lnpmDir := filepath.Join(cwd, ".lnpm")
		if _, err := os.Stat(lnpmDir); os.IsNotExist(err) {
			fmt.Println("No lnpm links found in this project")
			return nil
		}
	}

	if !force {
		// Before the listing, not inside it. --force is going to refuse and
		// remove nothing, so describing the packages it would have removed
		// would describe a retreat that is not going to happen - at the one
		// moment the developer is reading. A preview still changes nothing and
		// still returns nil; it just says what is actually coming.
		if err := requireRetreatableNodeModules(cwd, lock.List()); err != nil {
			fmt.Printf("%s 'lnpm retreat --force' will refuse and remove nothing: %v\n", ui.IconWarn(), err)
			return nil
		}

		fmt.Println("This will remove all lnpm links and restore original dependencies.")
		fmt.Println("Run with --force to confirm.")
		fmt.Println()
		fmt.Println("Changes that will be made:")

		// Show what will be removed
		linkedPkgs := lock.List()
		if len(linkedPkgs) > 0 {
			fmt.Printf("  - Remove %d linked package(s)\n", len(linkedPkgs))
			for _, name := range linkedPkgs {
				// The preview exists to tell the developer what --force is about
				// to do, and refusing an entry is part of that. Listing a key
				// that is not a package name as though it were an ordinary
				// dependency would describe a retreat that is not going to
				// happen, at the one moment the developer is reading.
				//
				// Same entry point as the retreat loop below, so the preview and
				// the action agree on which keys are refused. A dot-named
				// package from before #325 is retreated, not refused.
				if err := pack.ValidatePackageNameForRemoval(name); err != nil {
					fmt.Printf("    %s %s: will be skipped, not a valid package name: %v\n", ui.IconWarn(), name, err)
					continue
				}

				pkg, _ := lock.Get(name)
				originalVersion := pkg.OriginalVersion
				// Ignore lnpm's own reference as original version (bug from older versions)
				if isLnpmReference(originalVersion) {
					originalVersion = ""
				}
				if originalVersion != "" {
					fmt.Printf("    %s: file:.lnpm/%s → %s\n", name, name, originalVersion)
				} else {
					fmt.Printf("    %s: will be removed from package.json\n", name)
				}
			}
		}

		fmt.Println("  - Delete .lnpm/ directory")
		// Only when there is a lock file to save. A project with a stray .lnpm/
		// and no lock file reaches here, and --force would print nothing about
		// the lock file, because there is nothing to move aside.
		if _, err := os.Stat(lockfile.Path(cwd)); err == nil {
			fmt.Printf("  - Save lnpm.lock as %s, for 'lnpm restore'\n", lockfile.RetreatFileName)
		}
		return nil
	}

	// Before the first print, let alone the first delete: a refusal here has to
	// leave the project exactly as it was, and saying "Retreating from lnpm..."
	// first would be the only part of that which was not true.
	if err := requireRetreatableNodeModules(cwd, lock.List()); err != nil {
		return fmt.Errorf("%w\n\nHint: nothing was removed - .lnpm/, lnpm.lock and package.json are as they were, so re-run 'lnpm retreat' once that is settled", err)
	}

	// Get database for cleanup
	database, _ := db.GetDB()

	// Get current project, and the links it actually holds.
	//
	// A failed read is returned rather than read as "this project holds no
	// links", which is what remove does with these same two calls for the same
	// reason: the rows are how the link this project actually holds is found, and
	// treating a failure as an empty set would leave every one of them behind
	// while the retreat reported success. A record retreat cannot parse is a
	// record it cannot clean up from, so it is refused rather than read as "this
	// directory is not a project lnpm knows" - which is a different answer, and
	// the one a directory the store holds no row for goes on getting.
	//
	// Both errors are checked, and the first one was not before #391. Discarding
	// it never let a damaged record reach the removal loop: linksOfProject asks
	// the same question of the same path and fails the same way, so retreat
	// refused either way. What the discard cost was which read does the refusing.
	// linksOfProject wraps what it returns, so the damage arrived described as a
	// failed lookup rather than named as a record that will not parse, and the
	// refusal rested on a second reader that has no reason to keep asking this
	// question. Checking it here puts the refusal on the read that found the
	// damage, and the tests tell the two apart by that wrapper rather than by the
	// refusal alone.
	//
	// What #391 changed inside the lookup is what makes the check worth having
	// rather than merely tidier. A wrong-typed string field costs only itself -
	// the decoder records the error and keeps going - so a record damaged that way
	// used to come back as a project carrying a real ID, the value DeleteLink
	// matches link rows on, out of a record nothing could parse. Nothing in retreat
	// ever used it, because linksOfProject refused before the loop. It is nil now,
	// so a caller that did reach it would have no ID to aim a delete with.
	//
	// The refusal carries the same reassurance the node_modules preflight's does,
	// and for its reason: a command that stops partway has to say whether it
	// stopped before or after it began removing things. It does not repeat that
	// one's "re-run once that is settled" - the wrapped error already names lnpm
	// doctor, and a second next step would send the user two ways at once.
	//
	// Above the first print, let alone the first delete, for the reason
	// requireRetreatableNodeModules gives for its own position: a refusal here has
	// to leave the project exactly as it was, and announcing a retreat that is not
	// going to happen would be the only part of that which was not true. Position
	// matters more than it looks - os.RemoveAll(.lnpm) and stashLockForRestore
	// both run below the removal loop and neither is conditional on it, so a
	// refusal raised any later than this would still end with the package's files
	// gone, package.json still carrying its file:.lnpm reference and lnpm.lock
	// moved aside. Returning from here reaches none of them.
	//
	// A database that would not open at all is still tolerated, as it always was:
	// then there are no rows to clean up and no read to fail. A store that opens
	// and holds a record it cannot parse is a different thing and does not inherit
	// that tolerance - the first says nothing was ever recorded here, the second
	// says something was and lnpm can no longer tell what.
	var proj *db.Project
	var held projectLinks
	if database != nil {
		proj, err = database.GetProjectByPath(cwd)
		if err != nil {
			return fmt.Errorf("%w\n\nHint: nothing was removed - .lnpm/, lnpm.lock and package.json are as they were", err)
		}
		held, err = linksOfProject(database, cwd)
		if err != nil {
			return err
		}
	}

	fmt.Println("Retreating from lnpm...")

	// Remove each linked package
	linkedPkgs := lock.List()
	var refused []string
	for _, name := range linkedPkgs {
		// lnpm.lock is a checked-in artifact, so its keys come from whoever
		// wrote the repository rather than from lnpm. Everything below joins the
		// key into a path or edits package.json under it, and the node_modules
		// delete in particular escapes the project outright for a key like
		// "../../nm-victim/id_rsa" - filepath.Join cleans as it joins, so the
		// ".." segments survive into the path os.Remove is handed. Unlink, which
		// does this same job for 'lnpm remove', has always validated first; the
		// asymmetry was the bug.
		//
		// The entry is skipped whole rather than aborting the retreat. Per
		// ADR-0001 the direction is what decides: skipping narrows what this
		// retreat does and leaves the entry where the user can see it, where
		// aborting would strand a developer who has just been handed a tampered
		// lock file with .lnpm/ and every file: reference still in place -
		// retreat being the documented way out of lnpm, that developer is
		// exactly the one who needs it to work. The summary below reports the
		// count, so the narrowing is never silent.
		//
		// The refused entry is still carried into lnpm.lock.retreat with the
		// rest. It is a record, not an instruction, and 'lnpm restore' links
		// through Link, which validates the name again - strictly, so a
		// dot-named entry retreated here cannot be restored.
		//
		// Removal entry point, which waives three reservations - #325's leading
		// dot, and #326's Windows device names and trailing dot or space. None
		// of the three is a path check. Every check above that keeps the
		// node_modules delete inside the project still runs. Refusing a package
		// on one of those grounds here would be worse than pointless: the
		// RemoveAll of .lnpm below takes its files anyway, leaving a
		// node_modules symlink and a package.json file:.lnpm/{name} reference
		// pointing at nothing.
		if err := pack.ValidatePackageNameForRemoval(name); err != nil {
			fmt.Printf("  %s Refused %s: not a valid package name: %v\n", ui.IconWarn(), name, err)
			refused = append(refused, name)
			continue
		}

		pkg, _ := lock.Get(name)

		fmt.Printf("  Removing %s...\n", name)

		// Remove symlink from node_modules
		nodeModulesLink := filepath.Join(cwd, "node_modules", name)
		_ = os.Remove(nodeModulesLink)

		// Restore original package.json dependency
		// Ignore lnpm's own reference as original version (bug from older versions)
		originalVersion := pkg.OriginalVersion
		if isLnpmReference(originalVersion) {
			originalVersion = ""
		}

		if originalVersion != "" {
			if err := restorePackageJSON(cwd, name, originalVersion); err != nil {
				fmt.Printf("    %s Failed to restore package.json: %v\n", ui.IconWarn(), err)
			} else {
				fmt.Printf("    %s Restored %s to %s\n", ui.IconOK(), name, originalVersion)
			}
		} else {
			if err := removeFromPackageJSON(cwd, name); err != nil {
				fmt.Printf("    %s Failed to update package.json: %v\n", ui.IconWarn(), err)
			} else {
				fmt.Printf("    %s Removed %s from package.json\n", ui.IconOK(), name)
			}
		}

		// Remove link from database. The row this project holds, not the one a
		// lookup by name would find: the name index mirrors the default tag, so
		// for a project on a tagged version that lookup names a different record
		// and the delete would silently match nothing.
		//
		// A refused delete is reported and does not join refused (#392). The
		// error became reachable when the delete stopped writing over a link
		// index entry it could not read, and it is the last thing this entry
		// does: the node_modules symlink and the package.json reference have
		// both already been dealt with above, and .lnpm/ goes below whatever
		// happens here. Whether those two actually succeeded is not this
		// error's to answer - the package.json edit reports its own failure and
		// the symlink removal discards its own - and aborting here would repair
		// neither while leaving .lnpm/ in place. refused is not the place for it
		// either: that list means lnpm never wrote the entry and so never
		// touched the project for it, which is what reportRefused tells the user
		// before listing them under "Left in package.json, to remove by hand".
		// This entry was retreated. What survives is a store row recording a
		// consumer that is not one, and this print is the only thing that names
		// that row: doctor's orphaned-link check counts a link as a problem
		// only when its project record is missing or its directory is gone or
		// unreachable, and a retreat leaves the record and the directory both
		// intact, so the row is counted as healthy. Doctor does meet the damage
		// underneath it. The entry refused here can only be in
		// links_by_package - linksOfProject read links_by_project up front and
		// would have refused the whole retreat had that been the damaged one -
		// and every doctor check that walks a package's links reads that index
		// and abandons itself naming what it could not read.
		if database != nil && proj != nil {
			if l, ok := held[name]; ok {
				if err := database.DeleteLink(l.PackageID, proj.ID); err != nil {
					fmt.Printf("    %s Removed %s, but its link record is still in the store: %v\n", ui.IconWarn(), name, err)
				}
			}
		}
	}

	// Remove .lnpm directory
	lnpmDir := filepath.Join(cwd, ".lnpm")
	if err := os.RemoveAll(lnpmDir); err != nil {
		fmt.Printf("  %s Failed to remove .lnpm/: %v\n", ui.IconWarn(), err)
	} else {
		fmt.Printf("  %s Removed .lnpm/\n", ui.IconOK())
	}

	// Clean up .gitignore if enabled
	cfg := config.Get()
	if cfg.ShouldManageGitignore() {
		if err := gitignore.RemoveFromGitignore(cwd, ".lnpm/"); err != nil {
			fmt.Printf("  %s Could not clean .gitignore: %v\n", ui.IconWarn(), err)
		} else {
			fmt.Printf("  %s Cleaned .gitignore\n", ui.IconOK())
		}
	}

	stashLockForRestore(cwd, lock)

	// Run package manager install if requested
	if runInstall {
		pm := config.DetectPackageManager(cwd)
		installCmd := config.GetInstallCommand(pm)
		fmt.Printf("Running %s...\n", installCmd)

		cmd := shellcmd.Command(installCmd)
		cmd.Dir = cwd
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Printf("%s Install failed: %v\n", ui.IconWarn(), err)
		}
	}

	fmt.Println()
	// A retreat that refused part of its work is not a complete one, and saying
	// so is the whole point of counting: package.json still holds a file:.lnpm
	// reference for every refused entry.
	if len(refused) == 0 {
		fmt.Printf("%s Retreat complete!\n", ui.IconOK())
	} else {
		fmt.Printf("%s Retreat incomplete: refused %d of %d lnpm.lock entr(ies)\n", ui.IconWarn(), len(refused), len(linkedPkgs))
		reportRefused(cwd, refused)
	}

	if !runInstall {
		fmt.Printf("\n%s Run 'npm install' to restore original packages\n", ui.IconTip())
	}

	// Non-zero exit, as remove does for the packages it could not remove: a
	// script that retreats before publishing must not read a partial retreat as
	// a clean one. The wording is deliberately not the summary line's: rootCmd
	// silences the usage dump but not the error, so cobra prints this straight
	// after it, and two spellings of one sentence read as a stutter. The summary
	// counts what was refused; this names what is left.
	if len(refused) > 0 {
		return fmt.Errorf("package.json still references %d unretreated lnpm.lock entr(ies)", len(refused))
	}

	return nil
}

// requireRetreatableNodeModules refuses the whole retreat when node_modules -
// or, for a scoped entry, node_modules/{scope} - is not a real directory in the
// project. It answers for every entry the retreat would remove, and it is called
// before anything is removed at all.
//
// The hole it closes. retreat removes node_modules/{name} itself and never goes
// through the linker, so #339's guard did not cover it. Measured against the
// unguarded build: with node_modules committed as a link out of the project,
// retreat deleted nm-victim/my-package - a plain file outside the project - and
// printed "OK Retreat complete!" with a nil error.
//
// Why the whole retreat and not the entry, which is the opposite of what the
// name check beside the removal does with its refusals. That check can skip an
// entry safely because lnpm never wrote anything for it: 'lnpm add' validates
// the name, so there is no .lnpm/{name} to undo, which is exactly what
// reportRefused tells the user. Neither half of that holds here. lnpm did write
// .lnpm/{name}, and RunRetreat's os.RemoveAll takes it whatever the loop
// decided - so a skipped entry would end with its files deleted, package.json
// still carrying file:.lnpm/{name} pointing at nothing, lnpm.lock stashed so a
// re-run reports "No lnpm links found", and reportRefused telling the user lnpm
// had written nothing for it. That is the state the loop's removal-entry-point
// waiver — #325's leading dot plus #326's device-name and trailing dot-or-space
// rules, three reservations in all — calls worse than pointless, for this same
// reason, just above the removal itself. Refusing outright leaves a project the user can still act on: the
// error names the path and the override, and nothing has been touched.
//
// Why a preflight and not a check inside the loop. Both refuse, but only this
// one refuses atomically. The node_modules half is a property of the project, so
// inside the loop it would fire on the first entry and stop before any removal;
// the scope half is not, so with a committed node_modules/@org beside ordinary
// packages the loop would remove however many entries it reached first and then
// abort - a partially retreated project, which is the outcome this whole check
// exists to avoid. Answering for every entry up front is what makes the refusal
// all-or-nothing.
//
// Position was measured, not argued, the way the linker's own guard records it.
// Two wrong placements were run on 2026-08-24, each preceded by go vet and each
// read for the package result line: moved down to just above the .lnpm
// RemoveAll, and moved into the loop below the os.Remove. Both give the same
// answer - TestRunRetreatRefusesASymlinkedNodeModules and its scope twin, red on
// the sentinel and on nothing else. requireRefusalNamesTheWayOut stays green
// under both: the returned error still names the path and the override, and the
// hint below still says "nothing was removed" while the file outside the project
// is already gone. An error-only test cannot tell any of these placements apart,
// which is why the sentinel assertion runs first in every case.
//
// Worth naming what does not move, because it looks like it should.
// TestRunRetreatRefusedForASymlinkedNodeModulesStillRetreatsWithTheOverride
// stays green under the first placement: it sits above the RemoveAll, so .lnpm/
// and lnpm.lock still survive and the override re-run still completes. That test
// pins the recovery, not the position - only the sentinel does the latter.
//
// Deleting the check outright turns the same two tests red on the sentinel, plus
// that recovery test on its "want the refusal this test recovers from" line.
// Deleting only the preview's call moves one test and no others:
// TestRunRetreatPreviewFlagsASymlinkedNodeModules.
//
// Entries the name check will refuse are passed over. They never reach the
// removal, so they are not what this protects, and refusing the retreat on their
// account would take away the escape that check deliberately leaves open for a
// developer holding a tampered lock file.
//
// The names are sorted because lock.List() is a map range and returns them in a
// random order. Without it, a project with two committed scope links names a
// different one on each run.
func requireRetreatableNodeModules(cwd string, names []string) error {
	sorted := slices.Clone(names)
	slices.Sort(sorted)

	for _, name := range sorted {
		if pack.ValidatePackageNameForRemoval(name) != nil {
			continue
		}
		// The linker's own predicate, not a copy of it: one guard, one override
		// key, one message, whichever command reaches the directory.
		if err := link.RequireRealNodeModulesDirs(cwd, name); err != nil {
			return err
		}
	}

	return nil
}

// reportRefused says what a refusal left behind and what to do about it. It runs
// after the rest of the retreat, so it can name where things actually ended up
// rather than where they were when the entry was refused.
//
// That ordering is the whole reason this is separate. The retreat carries on
// past a refusal: .lnpm/ is deleted and lnpm.lock is moved to the snapshot. So a
// refusal cannot tell the user to go and look at lnpm.lock - by the time they
// read it, there is no lnpm.lock, and re-running retreat reports no links at
// all. It has to name the snapshot instead.
//
// What is left behind is safe to leave, which is why naming it beats quietly
// editing package.json under a name lnpm just refused to act on. lnpm never
// wrote these entries: 'lnpm add' validates the name, so there was never a
// .lnpm/{name} directory for them and no link to undo. The file: reference and
// the lock key are both the tampering itself, not damage the retreat did, and
// removing them is a decision for the person who owns the repository.
func reportRefused(cwd string, refused []string) {
	// Where the record went. The snapshot in the ordinary case; lnpm.lock still
	// in place if stashing failed, which it reports for itself above.
	record := lockfile.RetreatFileName
	if _, err := os.Stat(lockfile.Path(cwd)); err == nil {
		record = "lnpm.lock"
	}

	fmt.Printf("%s lnpm never wrote those entries, so there was nothing of its own to clean up for them\n", ui.IconWarn())
	fmt.Printf("%s Left in package.json, to remove by hand:\n", ui.IconWarn())
	for _, name := range refused {
		fmt.Printf("    %q\n", name)
	}
	fmt.Printf("%s The entries themselves are in %s; inspect it for tampering or corruption\n", ui.IconWarn(), record)
}

// stashLockForRestore moves lnpm.lock aside rather than deleting it. The move
// takes it out of the project the same way a delete would - nothing resolves
// lnpm.lock any more - while keeping the record of what was linked, which is the
// only thing 'lnpm restore' can rebuild the links from. lock is that record, as
// this retreat found it.
//
// A snapshot from an earlier retreat may still be sitting there unconsumed,
// because a restore reported failures and kept it, or because none was ever run.
// The lock file being stashed then describes only what has been linked since,
// and moving it over the snapshot would drop every package the earlier retreat
// unlinked and the restore never got to. So the two are merged instead, this
// retreat's entries winning any name they share: they are the newer record of
// the link. The exception is the original version, which is not a fact about the
// link at all and is kept from the snapshot when the newer entry has none - see
// the merge loop.
func stashLockForRestore(cwd string, lock *lockfile.LockFile) {
	if _, err := os.Stat(lockfile.Path(cwd)); err != nil {
		// No lock file means there is nothing to save and nothing to say. Any
		// other stat failure is worth a word, since the file may still be there.
		if !os.IsNotExist(err) {
			fmt.Printf("  %s Could not check lnpm.lock: %v\n", ui.IconWarn(), err)
			fmt.Printf("  %s The links are already gone; if lnpm.lock is still there, re-run 'lnpm retreat' to save it for 'lnpm restore'\n", ui.IconWarn())
		}
		return
	}

	prior, err := lockfile.LoadRetreat(cwd)
	if err != nil {
		// Merging into a snapshot that cannot be read is not possible, and
		// overwriting it would destroy a record only the user can now recover.
		// Leave both files alone and say what to do about it.
		fmt.Printf("  %s Could not read %s: %v\n", ui.IconWarn(), lockfile.RetreatFileName, err)
		fmt.Printf("  %s Kept lnpm.lock: fix or remove %s, then re-run 'lnpm retreat'\n", ui.IconWarn(), lockfile.RetreatFileName)
		return
	}

	if prior == nil {
		if err := os.Rename(lockfile.Path(cwd), lockfile.RetreatPath(cwd)); err != nil {
			fmt.Printf("  %s Failed to save lnpm.lock as %s: %v\n", ui.IconWarn(), lockfile.RetreatFileName, err)
			fmt.Printf("  %s lnpm.lock is still in place; 'lnpm restore' has nothing to work from\n", ui.IconWarn())
			return
		}
		fmt.Printf("  %s Removed lnpm.lock (saved as %s for 'lnpm restore')\n", ui.IconOK(), lockfile.RetreatFileName)
		return
	}

	for _, name := range lock.List() {
		entry, _ := lock.Get(name)
		// The one field the newer entry must not win on when it is empty. Every
		// other field describes the link, which the newer entry describes
		// better; the original version describes what package.json held before
		// lnpm ever touched it, and once lost it cannot be worked out again -
		// later retreats would drop the package from package.json instead of
		// putting the user's range back.
		//
		// It goes missing when the earlier retreat could not write package.json,
		// which it only warns about: package.json then still holds the lnpm
		// reference, so the `lnpm add` that produced this entry read that as the
		// original version and recorded nothing.
		if entry.OriginalVersion == "" {
			if priorEntry, ok := prior.Get(name); ok {
				entry.OriginalVersion = priorEntry.OriginalVersion
			}
		}
		prior.Add(name, entry)
	}
	if err := prior.SaveRetreat(cwd); err != nil {
		fmt.Printf("  %s Failed to save lnpm.lock into %s: %v\n", ui.IconWarn(), lockfile.RetreatFileName, err)
		// The snapshot is written through a temp file and a rename, so the
		// earlier retreat's record is intact and restore can still put those
		// packages back. Only this retreat's own entries are missing from it,
		// and they are still in lnpm.lock, which is still in place.
		fmt.Printf("  %s lnpm.lock is still in place and %s still holds the earlier retreat; re-run 'lnpm retreat' to merge them\n", ui.IconWarn(), lockfile.RetreatFileName)
		return
	}
	if err := os.Remove(lockfile.Path(cwd)); err != nil {
		fmt.Printf("  %s Failed to remove lnpm.lock: %v\n", ui.IconWarn(), err)
		return
	}
	fmt.Printf("  %s Removed lnpm.lock (merged into %s for 'lnpm restore')\n", ui.IconOK(), lockfile.RetreatFileName)
}
