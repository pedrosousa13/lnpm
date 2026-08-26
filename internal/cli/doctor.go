package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/pedrosousa13/lnpm/internal/config"
	"github.com/pedrosousa13/lnpm/internal/db"
	"github.com/pedrosousa13/lnpm/internal/pack"
	"github.com/pedrosousa13/lnpm/internal/store"
	"github.com/pedrosousa13/lnpm/internal/ui"
)

// RunDoctor executes the doctor command.
//
// verifyContent turns on the one check that reads stored content rather than
// asking about it, which costs a read of the whole store and is therefore opt
// in. Every other check here is bounded by the number of entries.
func RunDoctor(verifyContent bool) error {
	fmt.Println("Running lnpm doctor...")
	fmt.Println()

	issues := 0
	warnings := 0

	// Check 1: Store directory exists and is writable
	fmt.Print("Checking store directory... ")
	storeUsable := false
	storePath, err := config.GetStorePath()
	if err != nil {
		fmt.Printf("%s ERROR\n", ui.IconFail())
		fmt.Printf("  Failed to resolve store path: %v\n", err)
		issues++
	} else if info, err := os.Stat(storePath); err != nil {
		fmt.Printf("%s NOT FOUND\n", ui.IconFail())
		fmt.Printf("  Store directory does not exist: %s\n", storePath)
		fmt.Println("  Fix: Run 'lnpm publish' to create it")
		issues++
	} else if !info.IsDir() {
		fmt.Printf("%s ERROR\n", ui.IconFail())
		fmt.Printf("  %s exists but is not a directory\n", storePath)
		issues++
	} else {
		// Check writable
		testFile := filepath.Join(storePath, ".write-test")
		if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
			fmt.Printf("%s NOT WRITABLE\n", ui.IconFail())
			fmt.Printf("  Cannot write to store directory: %v\n", err)
			issues++
		} else {
			_ = os.Remove(testFile)
			fmt.Printf("%s OK\n", ui.IconOK())
			storeUsable = true
		}
	}

	// Store entries Check 5 looked at, so that Check 7's store-wide sweep does
	// not report the same directory a second time. Matched by path string: a
	// store reached through a different spelling of the same directory would be
	// reported by both checks, which is noise rather than a wrong finding.
	checkedEntries := make(map[string]bool)

	// Whether this store predates completeness markers, decided once and used
	// by both store checks below.
	//
	// On such a store a missing marker says nothing about the entry: no release
	// that wrote it had markers at all, so every entry lacks one and none of
	// them is damaged. Checks 5 and 7 would otherwise fault the entire store and
	// send the user to re-publish all of it, which is both alarming and wrong -
	// the store is migrated in one pass the next time any command opens it, and
	// doctor is the one command that never does.
	//
	// A failure to decide leaves this false, so the checks below report what
	// they find. That is the loud direction, and the quiet one would let a
	// single unreadable path suppress a genuine finding.
	legacyStore := false
	legacyBlockedBy := 0
	if storeUsable {
		if pending, unreadable, err := store.LegacyBackfillPending(); err == nil {
			legacyStore, legacyBlockedBy = pending, unreadable
		}
	}

	// Check 2: Database integrity
	fmt.Print("Checking database... ")
	database, err := db.GetDB()
	if err != nil {
		fmt.Printf("%s ERROR\n", ui.IconFail())
		fmt.Printf("  Failed to open database: %v\n", err)
		issues++
	} else {
		fmt.Printf("%s OK\n", ui.IconOK())

		// Check 3: Orphaned packages (versions no link and no tag reaches)
		//
		// A version a non-default tag names is not counted, and that is not
		// cosmetic: this check's whole output is advice to run gc, and gc keeps
		// exactly those versions. Counting one would send a user to a command
		// that would then find nothing to do, which reads as lnpm being unable to
		// clean up after itself rather than as it holding on to a build on
		// purpose. Reachable only since publish learned --tag; before that no
		// version could be tagged and unlinked at once.
		fmt.Print("Checking for orphaned packages... ")
		packages, _ := database.ListPackages()
		tagsByName := make(map[string]map[string]string)
		orphanedCount := 0
		// Tags are what tells an orphan from a build lnpm is keeping on purpose,
		// so a name whose tags cannot be read makes the count meaningless rather
		// than approximate: every version of that name would be counted as an
		// orphan and the user sent to a gc that would keep them. Reported as an
		// issue and the check abandoned, which is what doctor does with anything
		// it cannot answer.
		var tagsErr error
		// The links are the other half of the same question, and a package whose
		// link index will not parse is one this check can call neither linked nor
		// unlinked. Read as no links it counts as an orphan, and the fix printed
		// below sends the user to gc - which reads that same index, refuses it and
		// aborts, so the advice contradicts what happens next. Abandoned the same
		// way the tags are.
		var linksErr error
		for _, pkg := range packages {
			links, err := database.GetLinksForPackage(pkg.ID)
			if err != nil {
				linksErr = fmt.Errorf("failed to read the links of %s: %w", pkg.Name, err)
				break
			}
			if len(links) > 0 {
				continue
			}
			if _, ok := tagsByName[pkg.Name]; !ok {
				tags, err := database.TagsForPackage(pkg.Name)
				if err != nil {
					tagsErr = fmt.Errorf("failed to read the tags of %s: %w", pkg.Name, err)
					break
				}
				tagsByName[pkg.Name] = tags
			}
			if pinnedByTag(tagsByName[pkg.Name], pkg.ContentHash) {
				continue
			}
			orphanedCount++
		}
		if linksErr != nil {
			fmt.Printf("%s ERROR\n", ui.IconFail())
			fmt.Printf("  %v\n", linksErr)
			issues++
		} else if tagsErr != nil {
			fmt.Printf("%s ERROR\n", ui.IconFail())
			fmt.Printf("  %v\n", tagsErr)
			issues++
		} else if orphanedCount > 0 {
			fmt.Printf("%s %d orphaned package(s)\n", ui.IconWarn(), orphanedCount)
			fmt.Println("  Fix: Run 'lnpm gc' to remove unused packages")
			warnings++
		} else {
			fmt.Printf("%s OK\n", ui.IconOK())
		}

		// Check 4: Orphaned links (links to non-existent projects)
		fmt.Print("Checking for orphaned links... ")
		orphanedLinks := 0
		unreachableLinks := 0
		// Both reads this check makes decide the count, so neither can be
		// discarded and leave a number worth printing. A package whose link index
		// will not parse contributes none of its links, and a project record that
		// will not parse is read as a project that is not there - one counted as
		// orphaned when it may not be, or missed when it is. Either way the fix
		// below is advice drawn from a tally doctor cannot stand behind, so the
		// check is abandoned and the read reported instead, as the two above it
		// are.
		var linkErr error
		for _, pkg := range packages {
			links, err := database.GetLinksForPackage(pkg.ID)
			if err != nil {
				linkErr = fmt.Errorf("failed to read the links of %s: %w", pkg.Name, err)
				break
			}
			for _, link := range links {
				proj, err := database.GetProjectByID(link.ProjectID)
				if err != nil {
					linkErr = fmt.Errorf("failed to read a project %s is linked into: %w", pkg.Name, err)
					break
				}
				if proj == nil {
					orphanedLinks++
					continue
				}
				// The same question gc asks, through the same helper, because
				// the two must not disagree: doctor's answer is what sends a
				// user to gc, and counting an unreachable project as orphaned
				// here advised running the destructive command against a
				// project that was only unplugged. gc would then decline, and
				// the advice would look broken instead of wrong.
				state, _ := classifyProjectDir(proj.Path, proj.Device)
				switch state {
				case projectGone:
					orphanedLinks++
				case projectUnreachable:
					unreachableLinks++
				}
			}
			if linkErr != nil {
				break
			}
		}
		if linkErr != nil {
			fmt.Printf("%s ERROR\n", ui.IconFail())
			fmt.Printf("  %v\n", linkErr)
			issues++
		} else if orphanedLinks > 0 {
			fmt.Printf("%s %d orphaned link(s)\n", ui.IconWarn(), orphanedLinks)
			fmt.Println("  Fix: Run 'lnpm gc --fix-links' to clean up")
			warnings++
		} else {
			fmt.Printf("%s OK\n", ui.IconOK())
		}
		// Behind the same abandonment as the count above it: the sweep stopped
		// early, so this tally is as partial as that one.
		if linkErr == nil && unreachableLinks > 0 {
			// No fix is offered, and that is the point of separating these:
			// there is nothing to clean up. The project may be on a drive that
			// is merely unplugged, and gc will decline to judge it too.
			fmt.Printf("  %s %d link(s) could not be checked: the project directory is missing and the filesystem it was on is not mounted there\n", ui.IconWarn(), unreachableLinks)
			warnings++
		}

		// Check 5: Verify each package's store entry is one lnpm can serve.
		//
		// A directory stat used to be the whole test, and it is the weaker
		// question of the two: an interrupted `lnpm gc` removes an entry's
		// completeness marker before its tree, so a removal that dies partway
		// leaves a directory that stats fine and holds part of a package. That
		// is the state #330 was filed for, and doctor called it healthy. An
		// entry that is gone entirely still fails this check — there is no
		// marker to read in a directory that does not exist — so nothing the
		// stat caught is lost.
		//
		// This asks whether the entry is there and finished, and it used to
		// print under the name "store file integrity" — which it never
		// established, since a complete entry whose files were overwritten
		// passes it unchanged. Integrity is Check 6, and it is named for what
		// it does.
		fmt.Print("Checking store entries... ")
		var damaged []string
		// The entries Check 6 must not read. A missing or gutted entry is
		// already reported here, and re-hashing one would list every file in it
		// a second time under a different heading.
		damagedEntries := make(map[string]bool)
		for _, pkg := range packages {
			if pkg.StorePath == "" {
				continue
			}
			checkedEntries[pkg.StorePath] = true
			// On an unmigrated store the marker cannot be asked about, so this
			// falls back to the question doctor asked before it existed: is the
			// entry there at all. Check 7 reports the migration itself.
			//
			// The directory is named per package rather than only counted,
			// because for the entries that are still there the fix below cannot
			// be carried out without it: Store never renames over an occupied
			// destination, so a re-publish of unchanged content fails until the
			// directory is gone.
			var err error
			if legacyStore {
				_, err = os.Stat(pkg.StorePath)
			} else {
				err = store.CheckComplete(pkg.StorePath)
			}
			if err != nil {
				damaged = append(damaged, fmt.Sprintf("%s@%s  %s", pkg.Name, pkg.Version, pkg.StorePath))
				damagedEntries[pkg.StorePath] = true
			}
		}
		if len(damaged) > 0 {
			fmt.Printf("%s %d package(s) with missing or incomplete store entries\n", ui.IconFail(), len(damaged))
			for _, entry := range damaged {
				fmt.Printf("  %s\n", entry)
			}
			fmt.Println("  Fix: Re-publish the affected packages; delete any directory listed above that is still there first, since lnpm will not overwrite or remove one")
			issues++
		} else {
			fmt.Printf("%s OK\n", ui.IconOK())
		}

		// Check 6: Re-hash stored content and compare it against the hashes the
		// database records for it.
		//
		// This is the only check that tests the store's central claim. The
		// store is content-addressed, so an entry's directory name asserts what
		// the bytes inside it hash to, and nothing ever asked. During #333's
		// reproduction the store was provably poisoned — one project's write
		// reached the shared inode and a second project was then created from
		// the tampered file — and doctor reported the store healthy throughout,
		// because every check it had was about presence, not content.
		//
		// What is compared is content and only content, and that is what keeps
		// the check off the trap #333 laid. Stored files are chmodded to
		// mode &^ 0222 once they are materialised, while the database row
		// records the mode the package was published with, so the two differ on
		// a perfectly healthy store. pack.HashFile is xxhash over the bytes with
		// no mode in it; pack.HashFiles, the package-level hash, folds
		// Mode.Perm() in. Recomputing that package hash from the modes found on
		// disk would fault every entry in the store and advise re-publishing all
		// of it. Undoing the write protection with mode|0222 does not work
		// either: it is a no-op on a file published 0444, so restoring the write
		// bits invents a 0666 the file never had. Neither is needed, because
		// every mode the comparison uses comes out of the database.
		//
		// The package-level hash is not recomputed from disk at all, and does
		// not need to be. fileManifestHash establishes that the recorded rows
		// describe the entry the directory is named for, and each file's content
		// is then checked against those rows — so if both hold, the bytes on
		// disk hash to the name the entry is stored under, by construction.
		//
		// What this detects is corruption and accident: a file overwritten
		// through a shared inode, a truncated write, a bad disk. It is not
		// evidence against someone who chose the replacement bytes — a 64-bit
		// non-cryptographic hash can be collided deliberately, which is #331 —
		// and it does not claim to be.
		//
		// Off by default, because it is the only check here whose cost grows
		// with the size of the store rather than with the number of entries.
		// Measured on a 105 MB store of 5,043 files, the rest of doctor takes
		// under 10 ms and this check takes 0.16-0.19 s warm - about 550 MB/s, so
		// roughly two seconds a gigabyte, and more than that on a cold cache. A
		// small store would not notice; a large one would wait for something the
		// user did not ask for. So the default run says plainly that it did not
		// read the content, rather than printing OK for something it never did.
		fmt.Print("Checking store file integrity... ")
		if verifyContent {
			contentIssues, contentWarnings := reportContentIntegrity(database, packages, damagedEntries)
			issues += contentIssues
			warnings += contentWarnings
		} else {
			fmt.Println("SKIPPED")
			fmt.Println("  Stored content was not re-hashed: that costs one read of the whole store")
			fmt.Println("  Run 'lnpm doctor --verify-content' to check it")
		}
	}

	// Check 7: store entries that carry no usable completeness marker, swept
	// from the store itself rather than from the database.
	//
	// This is the same fault Check 5 reports, reached from the other side, and
	// both are needed. gc deletes a package's database row before its store
	// entry, so the directory a failed removal leaves behind is one no package
	// row names and Check 5 cannot see. An entry Check 5 looked at is skipped
	// here, so one damaged directory counts once.
	//
	// A store written before markers existed is not swept at all: every entry
	// in it would be listed, and the fix would be "re-publish your whole
	// store", which is wrong. Such a store is marked once by the gated backfill
	// in store.New, so what it needs is a command that opens the store — and
	// doctor is the one command that never does, which is why it reports this
	// rather than fixing it.
	//
	// Skipped when Check 1 found no usable store, as the checks above are:
	// with no store there is nothing to sweep, and Check 1's own finding is
	// the one worth acting on.
	if storeUsable {
		fmt.Print("Checking store completeness markers... ")
		incomplete, unreadable, err := store.IncompleteEntries()
		var unreported []string
		for _, entry := range incomplete {
			if !checkedEntries[entry] {
				unreported = append(unreported, entry)
			}
		}

		switch {
		case err != nil:
			fmt.Printf("%s ERROR\n", ui.IconFail())
			fmt.Printf("  Failed to scan the store for incomplete entries: %v\n", err)
			issues++
		case legacyStore && legacyBlockedBy > 0:
			// The migration is not merely waiting for a command to run it: the
			// backfill withholds its decision for as long as any directory
			// cannot be read, so it will stay pending however many commands the
			// user runs. That makes this an issue rather than a warning, and it
			// is the unreadable directory that has to be named — advising "run
			// any command" here would send the user somewhere that cannot help.
			fmt.Printf("%s PENDING\n", ui.IconFail())
			fmt.Println("  This store predates completeness markers and has not been migrated yet")
			fmt.Printf("  The migration cannot run: %d store directory(ies) could not be read, and it stays pending until they can\n", legacyBlockedBy)
			fmt.Println("  Fix: Make them readable, then run 'lnpm gc --dry-run' (or any command that opens the store)")
			issues++
		case legacyStore:
			fmt.Printf("%s PENDING\n", ui.IconWarn())
			fmt.Println("  This store predates completeness markers and has not been migrated yet")
			fmt.Println("  Fix: Run 'lnpm gc --dry-run' (or any command that opens the store)")
			warnings++
		case len(unreported) > 0:
			fmt.Printf("%s %d incomplete store entry(ies)\n", ui.IconFail(), len(unreported))
			for _, entry := range unreported {
				fmt.Printf("  %s\n", entry)
			}
			if unreadable > 0 {
				fmt.Printf("  %d store directory(ies) could not be read and may hold more\n", unreadable)
			}
			fmt.Println("  Fix: Re-publish the affected packages; lnpm will not delete an entry it cannot read, so remove the directories above yourself")
			issues++
		case unreadable > 0:
			// Reported as an issue rather than passed over. The sweep is the
			// only thing that looks at entries no package row names, so a
			// directory it could not read is a part of the store nothing has
			// checked — which is the state this check exists to end.
			fmt.Printf("%s %d store directory(ies) could not be read\n", ui.IconFail(), unreadable)
			fmt.Println("  Fix: Make them readable and run 'lnpm doctor' again; until then their entries are unchecked")
			issues++
		default:
			fmt.Printf("%s OK\n", ui.IconOK())
		}
	}

	// Summary
	fmt.Println()
	if issues == 0 && warnings == 0 {
		fmt.Printf("%s All checks passed!\n", ui.IconOK())
	} else {
		if issues > 0 {
			fmt.Printf("%s Found %d issue(s)\n", ui.IconFail(), issues)
		}
		if warnings > 0 {
			fmt.Printf("%s Found %d warning(s)\n", ui.IconWarn(), warnings)
		}
	}

	// The findings are already printed above; the error exists so that a script
	// running `lnpm doctor && ...` sees a non-zero exit. Warnings do not fail
	// the command: they describe cleanup worth doing, not a broken install.
	if issues > 0 {
		return fmt.Errorf("doctor found %d issue(s)", issues)
	}

	return nil
}

// reportContentIntegrity re-reads every stored file of every package and checks
// it against the content hash the database records, printing the result of
// Check 6 and returning the issues and warnings to add to doctor's tally.
//
// It reports and never repairs, which is deliberate: what to do about an entry
// whose content has changed is a decision lnpm cannot make for the user. The
// entry may be linked into projects that already hold the same bytes, and lnpm
// will not overwrite or delete a store entry in any case.
//
// The findings are kept in three buckets because they are three different
// things, and collapsing them would either alarm the user about something lnpm
// did on purpose or print OK over something nothing checked:
//
//   - altered: a file whose bytes are not the ones recorded for it. Damage, and
//     an issue.
//   - unverifiable: an entry whose recorded file list cannot be used at all -
//     unreadable, absent, or describing another generation of the package. Not
//     damage, but a part of the store this check could not speak for, so it is
//     reported rather than passed over.
//   - rewritten: the one file lnpm changes behind the hash's back. See
//     manifestRewrittenByStore below.
func reportContentIntegrity(database *db.DB, packages []*db.Package, damagedEntries map[string]bool) (issues, warnings int) {
	var altered, unverifiable, rewritten []string
	verified, filesRead, bytesRead := 0, 0, int64(0)
	skipped := 0

	for _, pkg := range packages {
		if pkg.StorePath == "" {
			continue
		}
		if damagedEntries[pkg.StorePath] {
			skipped++
			continue
		}

		entries, err := database.GetFilesForPackage(pkg.ID)
		switch {
		case err != nil:
			unverifiable = append(unverifiable, fmt.Sprintf("%s@%s  its recorded file list could not be read: %v", pkg.Name, pkg.Version, err))
			continue
		case len(entries) == 0:
			unverifiable = append(unverifiable, fmt.Sprintf("%s@%s  no file list is recorded for it, so there is nothing to compare the entry against", pkg.Name, pkg.Version))
			continue
		}
		// The link between the file rows and the name the entry is stored
		// under, and the reason the per-file comparison below is enough on its
		// own. Rows that reproduce the package's content hash describe this
		// generation of it; rows that do not describe some other one, and
		// checking the store against those would pass a stale entry or fault a
		// current one. The same question add asks before it trusts a manifest.
		if got := fileManifestHash(entries); got != pkg.ContentHash {
			unverifiable = append(unverifiable, fmt.Sprintf("%s@%s  its recorded file list describes %s, not the %s the entry is stored under",
				pkg.Name, pkg.Version, shortHash(got), shortHash(pkg.ContentHash)))
			continue
		}

		for _, entry := range entries {
			path := filepath.Join(pkg.StorePath, filepath.FromSlash(entry.RelativePath))
			hash, err := pack.HashFile(path)
			if err != nil {
				altered = append(altered, fmt.Sprintf("%s@%s  %s  could not be read: %v", pkg.Name, pkg.Version, entry.RelativePath, err))
				continue
			}
			filesRead++
			bytesRead += entry.Size
			if hash == entry.ContentHash {
				continue
			}
			if manifestRewrittenByStore(entry.RelativePath) {
				rewritten = append(rewritten, fmt.Sprintf("%s@%s  %s", pkg.Name, pkg.Version, entry.RelativePath))
				continue
			}
			altered = append(altered, fmt.Sprintf("%s@%s  %s  holds %s, not the recorded %s",
				pkg.Name, pkg.Version, entry.RelativePath, shortHash(hash), shortHash(entry.ContentHash)))
		}
		verified++
	}

	switch {
	case len(altered) > 0:
		fmt.Printf("%s %d stored file(s) do not hold the content recorded for them\n", ui.IconFail(), len(altered))
	case len(unverifiable) > 0:
		fmt.Printf("%s %d store entry(ies) could not be verified\n", ui.IconFail(), len(unverifiable))
	default:
		fmt.Printf("%s OK (%d package(s), %d file(s), %s re-hashed)\n", ui.IconOK(), verified, filesRead, formatSize(bytesRead))
	}

	for _, finding := range altered {
		fmt.Printf("  %s\n", finding)
	}
	if len(unverifiable) > 0 {
		if len(altered) > 0 {
			fmt.Printf("  %d further store entry(ies) could not be verified:\n", len(unverifiable))
		}
		for _, finding := range unverifiable {
			fmt.Printf("  %s\n", finding)
		}
	}
	if len(altered) > 0 || len(unverifiable) > 0 {
		fmt.Println("  Fix: Re-publish the affected packages; lnpm will not overwrite or remove a store entry, so delete the entry directory yourself first")
		fmt.Println("  Projects already linked to an affected package hold the same content, so re-run 'lnpm add' or 'lnpm push' for them afterwards")
		// One issue rather than one per finding, so that the summary counts
		// checks that failed rather than files, the way every check above it
		// does.
		issues = 1
	}

	if skipped > 0 {
		// Named rather than passed over, because a run that prints OK has to be
		// OK about the whole store. These entries are reported by Check 5, not
		// excused by it, so they add nothing to the tally here.
		fmt.Printf("  %d entry(ies) were not re-hashed: Check 5 already reported them as missing or incomplete\n", skipped)
	}
	if len(rewritten) > 0 {
		fmt.Printf("  %s %d manifest(s) could not be checked, because lnpm rewrote them after hashing them:\n", ui.IconWarn(), len(rewritten))
		for _, finding := range rewritten {
			fmt.Printf("    %s\n", finding)
		}
		fmt.Println("    A stored package.json has its prepare and prepublish scripts removed once the content hash has been taken, so the recorded hash does not describe the stored bytes")
		// A warning and not an issue. This is lnpm's own doing on a healthy
		// store, so failing the command for it would make
		// `lnpm doctor --verify-content && ...` unusable against any package
		// with a build step in it. It is still said out loud, because the file
		// it covers is the one an attacker would most want to change.
		warnings = 1
	}

	return issues, warnings
}

// manifestRewrittenByStore reports whether relPath names the one file whose
// stored bytes are allowed to differ from the hash recorded for them.
//
// store.stripLifecycleScripts re-marshals the entry's root package.json to
// remove prepare and prepublish, and it runs after publish has hashed the
// packed files, so for a package that has either script the store legitimately
// holds a manifest the database's hash does not describe. Nothing records which
// packages that happened to, so a mismatch here cannot be told apart from
// damage and is reported as unchecked rather than as either.
//
// Only the root manifest: the strip opens exactly destPath/package.json, so a
// package.json nested inside the package is an ordinary file and is compared
// like one.
func manifestRewrittenByStore(relPath string) bool {
	return relPath == "package.json"
}
