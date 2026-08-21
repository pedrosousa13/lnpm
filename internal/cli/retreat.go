package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/pedrosousa13/lnpm/internal/config"
	"github.com/pedrosousa13/lnpm/internal/db"
	"github.com/pedrosousa13/lnpm/internal/gitignore"
	"github.com/pedrosousa13/lnpm/internal/pack"
	"github.com/pedrosousa13/lnpm/internal/shellcmd"
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
				if err := pack.ValidatePackageName(name); err != nil {
					fmt.Printf("    %s %s: not a valid package name, will be skipped (%v)\n", iconWarn(), name, err)
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

	fmt.Println("Retreating from lnpm...")

	// Get database for cleanup
	database, _ := db.GetDB()

	// Get current project, and the links it actually holds.
	//
	// A failed read is returned rather than read as "this project holds no
	// links", which is what remove does with the same call for the same reason:
	// the rows are how the link this project actually holds is found, and
	// treating a failure as an empty set would leave every one of them behind
	// while the retreat reported success. Nothing has been removed yet at this
	// point, so returning here leaves the project untouched.
	//
	// A database that would not open at all is still tolerated, as it always was:
	// then there are no rows to clean up and no read to fail.
	var proj *db.Project
	var held projectLinks
	if database != nil {
		proj, _ = database.GetProjectByPath(cwd)
		held, err = linksOfProject(database, cwd)
		if err != nil {
			return err
		}
	}

	// Remove each linked package
	linkedPkgs := lock.List()
	refused := 0
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
		// The entry is skipped whole rather than aborting the retreat. Retreat
		// is the documented way out of lnpm, and a developer who has just been
		// handed a tampered lock file is exactly the person who needs to leave:
		// aborting would strand them with .lnpm/ and the file: references still
		// in place. Skipping narrows what the command does, which is the safe
		// direction under docs/adr/0001, and the summary below says the retreat
		// was incomplete so the narrowing is never silent.
		//
		// The refused entry is still carried into lnpm.lock.retreat with the
		// rest. It is a record, not an instruction, and 'lnpm restore' links
		// through Link, which validates the name again.
		if err := pack.ValidatePackageName(name); err != nil {
			fmt.Printf("  %s Refused %s: not a valid package name, so nothing was removed for it (%v)\n", iconWarn(), name, err)
			fmt.Printf("  %s lnpm did not write that entry; check lnpm.lock for tampering or corruption\n", iconWarn())
			refused++
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
				fmt.Printf("    %s Failed to restore package.json: %v\n", iconWarn(), err)
			} else {
				fmt.Printf("    %s Restored %s to %s\n", iconOK(), name, originalVersion)
			}
		} else {
			if err := removeFromPackageJSON(cwd, name); err != nil {
				fmt.Printf("    %s Failed to update package.json: %v\n", iconWarn(), err)
			} else {
				fmt.Printf("    %s Removed %s from package.json\n", iconOK(), name)
			}
		}

		// Remove link from database. The row this project holds, not the one a
		// lookup by name would find: the name index mirrors the default tag, so
		// for a project on a tagged version that lookup names a different record
		// and the delete would silently match nothing.
		if database != nil && proj != nil {
			if l, ok := held[name]; ok {
				_ = database.DeleteLink(l.PackageID, proj.ID)
			}
		}
	}

	// Remove .lnpm directory
	lnpmDir := filepath.Join(cwd, ".lnpm")
	if err := os.RemoveAll(lnpmDir); err != nil {
		fmt.Printf("  %s Failed to remove .lnpm/: %v\n", iconWarn(), err)
	} else {
		fmt.Printf("  %s Removed .lnpm/\n", iconOK())
	}

	// Clean up .gitignore if enabled
	cfg := config.Get()
	if cfg.ShouldManageGitignore() {
		if err := gitignore.RemoveFromGitignore(cwd, ".lnpm/"); err != nil {
			fmt.Printf("  %s Could not clean .gitignore: %v\n", iconWarn(), err)
		} else {
			fmt.Printf("  %s Cleaned .gitignore\n", iconOK())
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
			fmt.Printf("%s Install failed: %v\n", iconWarn(), err)
		}
	}

	fmt.Println()
	// A retreat that refused part of its work is not a complete one, and saying
	// so is the whole point of counting: package.json may still hold a
	// file:.lnpm reference for every refused entry.
	if refused > 0 {
		fmt.Printf("%s Retreat incomplete: refused %d of %d lnpm.lock entr(ies), see above\n", iconWarn(), refused, len(linkedPkgs))
	} else {
		fmt.Printf("%s Retreat complete!\n", iconOK())
	}

	if !runInstall {
		fmt.Printf("\n%s Run 'npm install' to restore original packages\n", iconTip())
	}

	// Non-zero exit, as remove does for the packages it could not remove: a
	// script that retreats before publishing must not read a partial retreat as
	// a clean one.
	if refused > 0 {
		return fmt.Errorf("%d of %d lnpm.lock entr(ies) were refused as invalid package names", refused, len(linkedPkgs))
	}

	return nil
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
			fmt.Printf("  %s Could not check lnpm.lock: %v\n", iconWarn(), err)
			fmt.Printf("  %s The links are already gone; if lnpm.lock is still there, re-run 'lnpm retreat' to save it for 'lnpm restore'\n", iconWarn())
		}
		return
	}

	prior, err := lockfile.LoadRetreat(cwd)
	if err != nil {
		// Merging into a snapshot that cannot be read is not possible, and
		// overwriting it would destroy a record only the user can now recover.
		// Leave both files alone and say what to do about it.
		fmt.Printf("  %s Could not read %s: %v\n", iconWarn(), lockfile.RetreatFileName, err)
		fmt.Printf("  %s Kept lnpm.lock: fix or remove %s, then re-run 'lnpm retreat'\n", iconWarn(), lockfile.RetreatFileName)
		return
	}

	if prior == nil {
		if err := os.Rename(lockfile.Path(cwd), lockfile.RetreatPath(cwd)); err != nil {
			fmt.Printf("  %s Failed to save lnpm.lock as %s: %v\n", iconWarn(), lockfile.RetreatFileName, err)
			fmt.Printf("  %s lnpm.lock is still in place; 'lnpm restore' has nothing to work from\n", iconWarn())
			return
		}
		fmt.Printf("  %s Removed lnpm.lock (saved as %s for 'lnpm restore')\n", iconOK(), lockfile.RetreatFileName)
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
		fmt.Printf("  %s Failed to save lnpm.lock into %s: %v\n", iconWarn(), lockfile.RetreatFileName, err)
		// The snapshot is written through a temp file and a rename, so the
		// earlier retreat's record is intact and restore can still put those
		// packages back. Only this retreat's own entries are missing from it,
		// and they are still in lnpm.lock, which is still in place.
		fmt.Printf("  %s lnpm.lock is still in place and %s still holds the earlier retreat; re-run 'lnpm retreat' to merge them\n", iconWarn(), lockfile.RetreatFileName)
		return
	}
	if err := os.Remove(lockfile.Path(cwd)); err != nil {
		fmt.Printf("  %s Failed to remove lnpm.lock: %v\n", iconWarn(), err)
		return
	}
	fmt.Printf("  %s Removed lnpm.lock (merged into %s for 'lnpm restore')\n", iconOK(), lockfile.RetreatFileName)
}
