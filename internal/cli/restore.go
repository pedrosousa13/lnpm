package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pedrosousa13/lnpm/internal/config"
	"github.com/pedrosousa13/lnpm/internal/db"
	"github.com/pedrosousa13/lnpm/internal/gitignore"
	"github.com/pedrosousa13/lnpm/internal/link"
	"github.com/pedrosousa13/lnpm/internal/pkgjson"
	"github.com/pedrosousa13/lnpm/internal/store"
	"github.com/pedrosousa13/lnpm/internal/ui"
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
// Which build each package was on is rebuilt exactly: the snapshot records a
// content hash, and that names one version of one package however many of them
// the store holds.
//
// Three parts of the pre-retreat state cannot be rebuilt, because the lock file
// records none of them. The first is whether a package was added with --link:
// everything is restored as a store copy, which is what the file:.lnpm/<pkg>
// specifier the command writes describes. The second is which dependency field
// a package belonged in; that is recovered from package.json, where retreat has
// just put the original specifier back, and reported when the package has no
// entry there to recover it from. The third is the dist-tag a package was added
// under: the restored link follows the default channel, and a package restored
// on a build that channel does not name is reported.
//
// A pin is not a fourth. The lock file records one, so the snapshot does too,
// and a project that retreated pinned comes back pinned - see ADR-0006, and
// recordRestoredLink for why that is not the same call as guessing a channel.
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
			fmt.Printf("  %s Failed to remove %s: %v\n", ui.IconWarn(), snapshotName, err)
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
			fmt.Printf("  %s Could not update .gitignore: %v\n", ui.IconWarn(), err)
		} else if added {
			fmt.Printf("  %s Added .lnpm/ to .gitignore\n", ui.IconOK())
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
			fmt.Printf("  %s %s was added again since the retreat; keeping that link\n", ui.IconOK(), name)
			consumeSnapshotEntry(snapshot, cwd, name)
			continue
		}
		attempted++

		// Resolved through the content hash the snapshot recorded, which names
		// one build of one package exactly. Resolving by name would answer with
		// whatever the default tag points at, and the store can hold several
		// versions of a name at once now: a consumer that was on another channel
		// would have had the build it asked for reported as missing while it sat
		// in the store, and a consumer on the default channel would be put back
		// on a release it never linked.
		//
		// An entry with no hash falls back to the name, which resolves through
		// the default tag. No lnpm ever wrote one: lockfile.Package has carried
		// Hash since the file's first commit and every writer sets it, so this
		// is defence against a hand-edited lock file and nothing else. The
		// fallback restores whatever the default tag names today, which for a
		// hand-edited entry is the best the entry supports.
		var pkg *db.Package
		if entry.Hash != "" {
			pkg, err = database.GetPackageByHash(name, entry.Hash)
		} else {
			pkg, err = database.GetPackageByName(name)
		}
		if err != nil {
			fmt.Printf("  %s %s: failed to look up package: %v\n", ui.IconFail(), name, err)
			failed++
			continue
		}
		if pkg == nil {
			// Reported per package and not fatal, so one collected build does not
			// abort the rest of the restore.
			if entry.Hash == "" {
				fmt.Printf("  %s %s: not found in store. Did you run 'lnpm publish' in the package directory?\n", ui.IconFail(), name)
			} else {
				fmt.Printf("  %s %s@%s (hash %s) is no longer in the store; re-publish it, then re-run 'lnpm restore'\n",
					ui.IconFail(), name, entry.Version, shortHash(entry.Hash))
			}
			failed++
			continue
		}

		// Read package.json before anything is written to it, so the specifier
		// retreat restored becomes this link's original version.
		deps, err := readPackageJSONDeps(pkgJSONPath, name, false)
		if err != nil {
			fmt.Printf("  %s %s: failed to read package.json: %v\n", ui.IconFail(), name, err)
			failed++
			continue
		}
		originalVersion := deps.originalVersion
		if originalVersion == "" {
			originalVersion = entry.OriginalVersion
		}

		linkRes, err := linkPackage(database, linker, s, pkg, false)
		if err != nil {
			fmt.Printf("  %s %s: %v\n", ui.IconFail(), name, err)
			failed++
			continue
		}

		// Nothing is recorded for this package until package.json holds the
		// reference: on a failure here it still holds whatever retreat left, and
		// the snapshot - kept, because this counts as a failure - still records
		// the original version, so the re-run has everything it needs.
		if err := writeLnpmReference(pkgJSONPath, name, false, false); err != nil {
			fmt.Printf("  %s %s: failed to update package.json: %v\n", ui.IconFail(), name, err)
			failed++
			continue
		}

		if !dependencyFieldKnown(deps.src, name) {
			fmt.Printf("  %s %s was not in package.json before the retreat, so its field is unknown; restoring it into %s\n",
				ui.IconWarn(), name, deps.field)
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
			Pinned:          entry.Pinned,
		})
		if err := lock.Save(cwd); err != nil {
			return fmt.Errorf("failed to save lock file: %w", err)
		}
		consumeSnapshotEntry(snapshot, cwd, name)

		if err := recordRestoredLink(database, cwd, pkg, linkRes.Type, entry.Pinned); err != nil {
			fmt.Printf("  %s %s: %v\n", ui.IconFail(), name, err)
			failed++
			continue
		}
		reportWhatTheRestoredLinkFollows(database, name, pkg, entry.Pinned)

		fmt.Printf("%s Restored %s@%s\n", ui.IconOK(), name, pkg.Version)
		restored++
	}

	// Nothing failed means every name was consumed above, so the file on disk is
	// an empty snapshot. Removing it says the same thing and says it to every
	// later retreat too, which reads a snapshot that is merely empty as an
	// earlier retreat to merge into.
	if failed == 0 {
		if err := os.Remove(lockfile.RetreatPath(cwd)); err != nil && !os.IsNotExist(err) {
			fmt.Printf("  %s Failed to remove %s: %v\n", ui.IconWarn(), snapshotName, err)
		}
	}

	fmt.Println()
	if failed > 0 {
		fmt.Printf("  %s %s kept, holding only what is left; re-run 'lnpm restore' once the packages above are available\n", ui.IconTip(), snapshotName)
		// Counted against what was attempted, not against the snapshot: a
		// package skipped because the user re-added it was never restore's to
		// fail at, and counting it understates the share of the run that did.
		return fmt.Errorf("%d of %d package(s) failed to restore", failed, attempted)
	}

	fmt.Printf("%s Restore complete!\n", ui.IconOK())
	if restored > 0 {
		fmt.Printf("\n  %s Run 'npm install' if you need to resolve peer dependencies\n", ui.IconTip())
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
		fmt.Printf("  %s Failed to update %s: %v\n", ui.IconWarn(), lockfile.RetreatFileName, err)
	}
}

// recordRestoredLink registers the project and its link to pkg, so `lnpm list`
// and `lnpm push` see the restored package the way they see an added one.
//
// A failure here used to be reported and not counted as a failed restore, on
// the grounds that it cost only bookkeeping because the link on disk was
// already in place. That was wrong in the one direction that matters: the row
// is what tells gc the project consumes this package, so without it the store
// entry the restored link points at reads as unreferenced and is deleted. The
// files on disk are what make that a live consumer, not a harmless orphan. So
// the error is returned and the restore counts as failed - the snapshot entry
// then survives for the re-run, which is exactly what a half-restored package
// needs.
func recordRestoredLink(database *db.DB, cwd string, pkg *db.Package, linkType link.LinkType, pinned bool) error {
	proj := &db.Project{
		Path:           cwd,
		Name:           getProjectName(cwd),
		PackageManager: string(config.DetectPackageManager(cwd)),
	}
	if err := database.InsertProject(proj); err != nil {
		return fmt.Errorf("failed to register project: %w", err)
	}

	existingProj, err := database.GetProjectByPath(cwd)
	if err != nil {
		return fmt.Errorf("failed to get project: %w", err)
	}

	// The link follows the default tag, which is what an unset Tag means. Nothing
	// records the channel a consumer was on: the lock file has never held one, so
	// neither does the snapshot retreat writes from it. Guessing from the tags
	// that happen to name this build today would be a guess about what someone
	// asked for months ago, and a wrong one is worse than none - it would have
	// later publishes carry the project down a channel it never chose.
	// reportWhatTheRestoredLinkFollows says so out loud instead.
	//
	// pushTag reads the same tags and does infer from them, which is not a
	// contradiction: it is asking a different question. Push asks which channel
	// the build in front of it belongs to, about a tree its user is editing now,
	// and a wrong answer is a release the next command can re-tag. Here the
	// question is what a user chose before a retreat that may be weeks old, and
	// a wrong answer is written into a link row that quietly steers every
	// publish after it.
	// The pin is the one part of the link's state that does come back, and it
	// comes back because the lock file records it. That is not a contradiction
	// of the paragraph above: a channel was never written down anywhere, so
	// restoring one would be an invention, while a pin is a fact the snapshot
	// carries alongside the content hash it already carries for the same
	// purpose. Declining to record a fact that is available is not caution - it
	// would put a project back on a build it pinned while following latest, and
	// the next pull would move it off. See ADR-0006.
	dbLink := &db.Link{
		PackageID: pkg.ID,
		ProjectID: existingProj.ID,
		LinkType:  string(linkType),
		Pinned:    pinned,
	}
	if err := database.InsertLink(dbLink); err != nil {
		return fmt.Errorf("failed to record the link: %w", err)
	}
	return nil
}

// reportWhatTheRestoredLinkFollows says what the link restore has just written
// will do on the next `lnpm pull`, whenever that is not the obvious thing.
//
// It says nothing at all in the ordinary case - an unpinned link on the build the
// default tag names, which is a project that will simply stay current. The three
// cases below are the ones where the files on disk look right and the link does
// something the user would not predict from them, which is exactly the shape of
// surprise that surfaces later and elsewhere.
//
// A restored pin is answered first, and it is the one case that does not depend
// on which build the default tag names. The trigger is wider than the other two
// on purpose: a pin the user cannot see is a pin that ambushes them at the next
// pull, and it does that whether or not the pinned build happens to be current
// today - the moment the package is published again, a project that looked
// current stops moving. Being off the default channel is the whole point here
// rather than a problem to resolve, so the advice is not `lnpm pull`, which would
// be backwards and which ADR-0006 has refuse a pinned package anyway. The user is
// told the pin came back and how to leave it.
//
// The other two are both a restored link that follows the default tag - the third
// thing restore cannot rebuild from the snapshot, alongside whether a package was
// added with --link and which dependency field it was in - sitting on a build
// that tag does not name. They take opposite advice, so which one happened is
// worked out rather than hedged over.
//
// A tag naming the restored build means the project was following that channel:
// the snapshot cannot say so, but a build that a channel still names is one a
// consumer plausibly asked for, and re-adding under the tag says it again.
//
// No tag naming it means the channel is not the story. The build was the default
// one and the package has been published since, which hash-based restore turned
// from a failure into this: the recorded build comes back exactly, and it is
// simply behind. Sending that user after a dist-tag would have them hunt for a
// channel their package never had; what they want is `lnpm pull`.
//
// A failure to read is not reported. This is advice about a state restore has
// already left the project in, and turning a failed read into a second warning
// about the first would say less than nothing.
func reportWhatTheRestoredLinkFollows(database *db.DB, name string, pkg *db.Package, pinned bool) {
	if pinned {
		fmt.Printf("  %s %s was pinned to %s before the retreat and is pinned again, so 'lnpm pull' will leave it there\n",
			ui.IconTip(), name, pkg.Version)
		fmt.Printf("      Run 'lnpm add %s' to unpin it and follow %s again\n", name, db.DefaultTag)
		return
	}

	current, err := database.ResolveTag(name, db.DefaultTag)
	if err != nil || (current != nil && current.ContentHash == pkg.ContentHash) {
		return
	}

	tags, err := database.TagsForPackage(name)
	if err != nil {
		return
	}

	// The default tag is not among these: it names other content, or nothing.
	if naming := tagsNamingList(tags, pkg.ContentHash); len(naming) > 0 {
		fmt.Printf("  %s %s was restored on the build tagged %s, but the restored link follows %s\n",
			ui.IconWarn(), name, strings.Join(naming, ", "), db.DefaultTag)
		fmt.Printf("      If it was added under a dist-tag, run 'lnpm add %s@<tag>' to follow that channel again\n", name)
		return
	}

	fmt.Printf("  %s %s has been published since the retreat, so the build restored is no longer the one %s names\n",
		ui.IconWarn(), name, db.DefaultTag)
	fmt.Printf("      Run 'lnpm pull' to move onto the current release\n")
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
