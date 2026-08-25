package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pedrosousa13/lnpm/internal/db"
	"github.com/pedrosousa13/lnpm/internal/link"
	"github.com/pedrosousa13/lnpm/internal/store"
)

// RunGC executes the garbage collection command
func RunGC(dryRun bool, olderThan string, fixLinks bool, yes bool) error {
	database, err := db.GetDB()
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}

	// Parse olderThan duration
	var maxAge time.Duration
	if olderThan != "" {
		var err error
		maxAge, err = parseDuration(olderThan)
		if err != nil {
			return fmt.Errorf("invalid duration %q: %w", olderThan, err)
		}
	}

	if dryRun {
		fmt.Println("Dry run mode - no changes will be made")
		fmt.Println()
	}

	packages, err := database.ListPackages()
	if err != nil {
		return fmt.Errorf("failed to list packages: %w", err)
	}

	// Store root used to bound destructive RemoveAll calls.
	s, err := store.New()
	if err != nil {
		return fmt.Errorf("failed to access store: %w", err)
	}
	storeRoot := s.Root()

	// Find packages to remove
	var packagesToRemove []*db.Package
	var linksToRemove []linkToRemove
	// Tags of each package name, read once per name. They are reachability
	// roots, so a failure to read them stops the whole run: this pass decides
	// what gets deleted, and deciding it from tags gc could not read would be
	// deleting a version it cannot show is unreachable.
	tagsByName := make(map[string]map[string]string)
	// Project directories still on disk, deduplicated: the temp-directory sweep
	// reads each one once, however many packages are linked into it.
	projectPaths := make(map[string]struct{})
	// Links gc declined to judge, because the project directory does not stat
	// and the filesystem it was recorded on is not mounted where it should be.
	// Reported rather than only counted: these are the links that kept a version
	// alive, so a user wondering why gc reclaimed nothing needs to see which
	// ones, and a user whose drive is genuinely gone for good needs to know
	// there is something to clean up once they say so.
	var skippedLinks []skippedLink

	for _, pkg := range packages {
		// A failure to read the links stops the run, for the reason the tag read
		// below stops it: this pass decides what gets deleted, and a read that
		// failed is indistinguishable here from a version nothing links - so
		// carrying on would delete a version gc cannot show is unreachable.
		//
		// Until #329 this guard caught a failed bolt read and nothing else. The
		// damage that matters here - a link row or an index entry that will not
		// parse - was dropped by the lookup, which then returned a nil error, so
		// the very state this describes arrived as an empty list and went
		// straight past: the run deleted the store entry of a package a project
		// was still consuming, and reported success. A guard is worth no more
		// than the error it inspects, which is why the fix was made there rather
		// than by adding a branch here.
		links, err := database.GetLinksForPackage(pkg.ID)
		if err != nil {
			return fmt.Errorf("failed to read the links of %s@%s: %w", pkg.Name, pkg.Version, err)
		}

		// Check for orphaned links
		// Named lnk, not link, so it does not shadow the link package the
		// temp-directory sweep below calls into.
		for _, lnk := range links {
			// A failure to read the project stops the run too, and for the same
			// reason the link read above stops it: both reasons below are
			// statements about a project, and a read that failed establishes
			// neither. This is the #292 half. Discarding the error left "the
			// record would not parse" indistinguishable from "no record answers
			// for this ID", and the link was filed as orphaned either way.
			//
			// That is destructive on a plain gc, with no flag involved, which is
			// the part worth being exact about. The
			//
			//	validLinks := len(links) - countLinksForPackage(linksToRemove, pkg.ID)
			//
			// below subtracts unconditionally, so a misclassified link takes the
			// version's last consumer with it and the version is collected -
			// store entry and database row both. fixLinks gates only the block
			// that reports and deletes the link rows themselves. So the flagless
			// run is the quieter of the two, not the safer one: the same package
			// is deleted and no line naming the link is ever printed.
			//
			// Checking the error before the nil result is what makes this guard
			// hold whatever the lookup returns. Before #292 it returned a
			// non-nil project alongside the error, and what that project held
			// depended on how the record was damaged - a syntax error decoded
			// nothing and reached os.Stat("") and the wrong reason, while a
			// wrong-typed value left a real Path that stat'd fine and hid the
			// damage entirely. GetProjectByID documents both. This check needs
			// to know about neither.
			proj, err := database.GetProjectByID(lnk.ProjectID)
			if err != nil {
				return fmt.Errorf("failed to read project %d, linked by %s@%s: %w", lnk.ProjectID, pkg.Name, pkg.Version, err)
			}
			if proj == nil {
				linksToRemove = append(linksToRemove, linkToRemove{
					packageID: pkg.ID,
					projectID: lnk.ProjectID,
					reason:    "project not found in database",
				})
				continue
			}
			// Decide whether the project directory was deleted or is merely out
			// of reach. A bare not-exist test used to be the whole answer, and
			// it cannot tell the two apart: an unmounted drive stats ENOENT
			// exactly like a deleted directory, so a drive left unplugged made
			// gc drop the link, take validLinks to zero, and delete the store
			// entry of a package the project was still consuming. Re-mounting
			// could not bring it back. classifyProjectDir documents how the two
			// are separated and, more importantly, the five cases where it
			// still gets the answer wrong.
			state, device := classifyProjectDir(proj.Path, proj.Device)
			switch state {
			case projectGone:
				linksToRemove = append(linksToRemove, linkToRemove{
					packageID:   pkg.ID,
					projectID:   lnk.ProjectID,
					projectPath: proj.Path,
					reason:      "project directory no longer exists",
				})
			case projectUnreachable:
				// Declined, not judged. The link stays out of linksToRemove, so
				// it still counts toward validLinks below and the version is
				// not collected. The direction is what decides, by the
				// principle ADR-0001 argues for on the publish paths: this
				// narrows what a destructive pass removes rather than widening
				// it. That ADR is scoped to swallowed errors in publish, so it
				// is the reasoning being borrowed here and not the ruling.
				//
				// The path is deliberately not added to projectPaths. The
				// temp-directory sweep reads each one, and this directory does
				// not exist - it would only be counted as unreadable and
				// reported as a second, unrelated warning.
				skippedLinks = append(skippedLinks, skippedLink{
					projectPath:  proj.Path,
					packageLabel: fmt.Sprintf("%s@%s", pkg.Name, pkg.Version),
				})
			case projectLive:
				projectPaths[proj.Path] = struct{}{}
				// Re-stamp what the project is on now. Without this the
				// recorded device is only as good as the mount in place when
				// the project was added, and an anonymous device number - tmpfs,
				// btrfs, overlayfs, FUSE and NFS all get one - is not stable
				// across a remount. A record left to drift makes gc decline
				// that project for good, which is safe but leaks space.
				//
				// It is skipped on a dry run. This is the one write the orphan
				// scan makes, and a dry run has already printed that it will
				// change nothing - the other three mutations in this function
				// are all behind the same flag. Declining to re-stamp costs
				// only that the record stays stale until a real run, which is
				// the state it was already in.
				//
				// A failure here is reported and not fatal, for the reason
				// removeOrphanedLinks gives: it leaves a stale device, and a
				// stale device makes gc decline rather than delete.
				if !dryRun && device != 0 && device != proj.Device {
					if err := database.SetProjectDevice(proj.ID, device); err != nil {
						fmt.Printf("  %s Failed to record which filesystem %s is on: %v\n", iconWarn(), proj.Path, err)
					}
				}
			}
		}

		// Re-check links after filtering orphans
		validLinks := len(links) - countLinksForPackage(linksToRemove, pkg.ID)

		if _, ok := tagsByName[pkg.Name]; !ok {
			tags, err := database.TagsForPackage(pkg.Name)
			if err != nil {
				return fmt.Errorf("failed to read the tags of %s: %w", pkg.Name, err)
			}
			tagsByName[pkg.Name] = tags
		}

		// A version stays for as long as a tag names it, whatever its links
		// say. That is the other half of the reachability rule: a build
		// published to a channel is meant to be there when someone asks for
		// the channel, and nothing need be linked to it yet.
		if pinnedByTag(tagsByName[pkg.Name], pkg.ContentHash) {
			continue
		}

		// The version is orphaned if no valid links.
		//
		// A superseded version reaches this with no links of its own, because
		// moving a tag carries them to the version it now names. That is what
		// makes the generations a store used to accumulate collectable at all,
		// and it has a cost worth stating. An incremental relink carries an
		// unchanged file across from the package already in the project with a
		// hard link, so .lnpm/{package} keeps the inode - and therefore the
		// store generation - that first materialised each file, however many
		// versions have been published since. Removing that generation's entry
		// does not touch the project's files: the consumer's hard link keeps the
		// inode alive. What it ends is the sharing. Those bytes are the
		// consumer's alone afterwards, and publishing that exact content again
		// copies them into the store again rather than finding them there.
		//
		// It stays a cost rather than a loss because nothing a link or a tag
		// still reaches is removed.
		if validLinks == 0 {
			// Check age if specified
			if maxAge > 0 {
				age := time.Since(pkg.UpdatedAt)
				if age < maxAge {
					continue
				}
			}
			packagesToRemove = append(packagesToRemove, pkg)
		}
	}

	// Report findings
	//
	// The skipped links are reported before anything else and outside the
	// fixLinks branch, because they explain the rest of the report: a version
	// gc did not collect may be uncollectable only because a link below could
	// not be judged. Gating this on a flag would hide the reason a destructive
	// command declined to act.
	if len(skippedLinks) > 0 {
		fmt.Printf("%s Skipped %d link(s): the project directory is missing and the filesystem it was on is not mounted there\n", iconWarn(), len(skippedLinks))
		for _, s := range skippedLinks {
			fmt.Printf("  - %s (consumes %s)\n", s.projectPath, s.packageLabel)
		}
		// No remedy is suggested, and the omission is deliberate. --fix-links
		// would not collect these: the flag gates reporting and deleting the
		// rows that were already classified as orphaned, and these were never
		// classified at all. Nor would re-running with the filesystem mounted,
		// which makes the project exist again and the link plainly live. There
		// is currently no way to tell lnpm that a drive is gone for good, so
		// saying "run X" would send the user somewhere that does nothing.
		fmt.Println("  These links were kept, so the versions they name were not collected.")
		fmt.Println()
	}

	if fixLinks && len(linksToRemove) > 0 {
		fmt.Printf("Found %d orphaned link(s):\n", len(linksToRemove))
		for _, l := range linksToRemove {
			fmt.Printf("  - Link to %s (%s)\n", l.label(), l.reason)
		}
		fmt.Println()

		if !dryRun {
			// The second sentence says what declining achieves, because "no"
			// reads as "leave this alone" and means only "leave the rows".
			// The validLinks arithmetic above ran before the question and
			// subtracted these links whatever the answer is, so refusing here
			// keeps the records and leaves the versions they name exactly as
			// collectable as they already were.
			//
			// "by itself" is load-bearing: declining withholds no protection, but
			// it is not the only thing that can protect a version. One listed here
			// whose package keeps a live link, or whose hash a tag other than
			// latest names, is not collectable at all, and a flat "can still be
			// collected" would tell that user something false. The default tag is
			// not one of those protections - pinnedByTag skips it deliberately, and
			// says why.
			if confirm("Permanently delete these orphaned link(s)? Declining keeps the records, but does not by itself protect the version(s) they name from collection.", yes) {
				removeOrphanedLinks(database, linksToRemove)
			} else {
				// A third question rather than one shared with the two below.
				// The blocks act on different things — link rows, store
				// entries, temp directories — and #233 already decoupled the
				// other two for that reason. Declining here still leaves the
				// rest of the run to ask about what it found.
				fmt.Println("Skipped deleting orphaned links.")
			}
		}
	}

	if len(packagesToRemove) > 0 {
		fmt.Printf("Found %d orphaned package(s):\n", len(packagesToRemove))
		var totalSize int64
		for _, pkg := range packagesToRemove {
			fmt.Printf("  - %s@%s (%s)\n", pkg.Name, pkg.Version, formatSize(pkg.TotalSize))
			totalSize += pkg.TotalSize
		}
		fmt.Printf("Total size: %s\n", formatSize(totalSize))
		fmt.Println()

		if !dryRun {
			if confirm("Permanently delete these package(s) from the store?", yes) {
				removePackages(database, storeRoot, packagesToRemove)
			} else {
				// Declining skips deleting packages and nothing else. The
				// temp-directory sweep below asks its own question, about a
				// different kind of thing, and coupling the two would mean a
				// user who says no here never learns those directories exist —
				// which is most of what makes them a problem, since no other
				// command mentions them.
				fmt.Println("Skipped deleting packages.")
			}
		}
	}

	// Reclaim the temp directories an interrupted publish or relink left behind.
	//
	// This runs here, inside the window where the database handle is still open,
	// and that ordering is the entire safety argument rather than an incidental
	// detail. bolt.Open takes an exclusive OS file lock on the database file and
	// holds it until Close, and every path that creates one of these directories
	// — publish, add, push — opens the database before it touches the store or a
	// project. So while gc holds the handle no other lnpm process can be
	// mid-write, and a temp directory seen here has no live writer by
	// construction. That is why no age threshold, pid file or lock file is
	// needed: the invariant already exists and the OS already enforces it.
	//
	// Moving this after a Close would void the guarantee in silence, and let the
	// sweep delete the temp directory of a relink that started in the meantime —
	// exactly the half-written package #137 removed. reapTempDirs re-checks the
	// lock so such a reordering fails loudly instead.
	sortedProjects := make([]string, 0, len(projectPaths))
	for path := range projectPaths {
		sortedProjects = append(sortedProjects, path)
	}
	sort.Strings(sortedProjects)
	tempDirsFound, err := reapTempDirs(database, s, sortedProjects, dryRun, yes)
	if err != nil {
		return err
	}

	if len(packagesToRemove) == 0 && len(linksToRemove) == 0 && tempDirsFound == 0 {
		fmt.Printf("%s Nothing to clean up\n", iconOK())
	}

	return nil
}

// pinnedByTag reports whether a tag other than the default one names the version
// with this content hash.
//
// The default tag is excluded, and that asymmetry is the whole of the rule worth
// arguing about. Every publish moves it onto whatever it just wrote, so it
// always names something and names it without anyone deciding to: counting it as
// a root would leave gc unable to collect any current version of any package,
// which is every package a store holds one version of — the command would still
// run, report nothing and free nothing. A tag a user set is a decision to keep a
// build; latest is a side effect of the last publish. What the default tag names
// is still kept while a project links it, which is the rule gc has always
// applied.
func pinnedByTag(tags map[string]string, hash string) bool {
	for tag, tagged := range tags {
		if tag != db.DefaultTag && tagged == hash {
			return true
		}
	}
	return false
}

// removeOrphanedLinks deletes the database row of each orphaned link and reports
// how many rows it actually removed.
//
// It counts successes rather than candidates: gc must not claim what it did not
// reclaim. That rule comes from #273, which stopped the package block discarding
// the error from removing a store entry and made it count what it had actually
// removed; #233 is a different rule, cited at the call site, and decoupled the
// prompts rather than the counting. The count printed here used to be
// len(links), and the error was discarded on the way, so a run where every
// delete failed printed a clean success and the rows it had left behind were
// never named.
//
// The failure is not fatal to the run for the reason ADR-0001 gives: skipping a
// row narrows what this pass removes and leaves it for the next run, and the row
// it could not delete is a stale record rather than anything a project depends
// on.
//
// The failure line says "the link to X" where removePackages and reapTempDirs
// say only "X". That is deliberate and not drift: the findings printed above
// read "- Link to X", and a user matching a failure to a finding should see the
// same words.
//
// The summary is skipped entirely when nothing was removed, and the two blocks
// below now skip theirs on the same rule: a clean success over nothing achieved
// is the defect, not a house style. This block diverged alone when #291 landed,
// because it was the one being fixed that day and the other two still printed
// "Removed 0 package(s), freed 0 B" and "Reclaimed 0 temp director(ies), freed
// 0 B" under a success icon; #358 settled the question the other way round and
// brought them into line. The per-link failure lines above are the report in
// this case.
//
// It is a function rather than an inline block for the reason removePackages is
// one, and for a second: the failure it reports can now be one link's rather
// than the whole pass's. #392 made DeleteLink refuse a link index entry it
// cannot parse instead of deleting the entry, and the refusal comes back out of
// its transaction, so the damaged entry stops that one delete and the loop goes
// on to the links whose entries are readable. When this comment was written no
// damage to the link buckets could drive a failure here at all, and only a
// whole-pass one - bolt.Update's Begin or its Commit, which a closed handle
// drives, as TestReapTempDirsRequiresTheDatabaseLock drives the temp sweep - was
// constructible.
//
// Two shapes still come back nil and are not this loop's to catch: a link row
// that will not parse is skipped by DeleteLink's own lookup, and a link ID that
// lookup does not find is not an error. The first is a defect recorded on
// DeleteLink rather than fixed here.
func removeOrphanedLinks(database *db.DB, links []linkToRemove) {
	removed := 0
	for _, l := range links {
		if err := database.DeleteLink(l.packageID, l.projectID); err != nil {
			fmt.Printf("  %s Failed to remove the link to %s: %v\n", iconWarn(), l.label(), err)
			continue
		}
		removed++
	}
	if removed == 0 {
		return
	}
	fmt.Printf("%s Removed %d orphaned link(s)\n", iconOK(), removed)
}

// removePackages deletes each package's store entry and then its database row,
// and reports how many packages it actually removed. It is a function rather
// than an inline block so gc can decline it and still go on to sweep temp
// directories, which is a separate decision.
//
// Both halves count successes rather than candidates, because gc must not claim
// what it did not reclaim: #273 established that for the store half, and #358
// for the database half. The row delete used to discard its error and advance
// removed and freed regardless, so a refused delete was reported as bytes freed
// while the row was still there - and wherever the half above had run, that row
// was left naming a store entry it had already removed, which is a worse end
// state than either half failing alone.
//
// A refused row delete is not fatal to the run, for the reason removeOrphanedLinks
// gives: it leaves a stale record rather than anything a project depends on, and
// gc can be re-run. What a re-run meets is the entry gone and the row still
// there, and RemoveEntry reads an absent entry as removed, so the retry turns on
// whether the row delete can succeed this time.
//
// The summary is skipped entirely when nothing was removed, as
// removeOrphanedLinks and reapTempDirs skip theirs. This block is only reached
// once the user has confirmed a non-empty list, so removed == 0 here means every
// removal failed, and the per-package failure lines above are the report.
//
// A per-package failure is drivable here, which is what separates this from
// removeOrphanedLinks. #355 made DeletePackage refuse the delete whenever it
// cannot read the package's complete link set, so damaging one package's link
// index fails that package and leaves the rest of the pass alone. A whole gc run
// cannot deliver one: the scan calls GetLinksForPackage on every package first,
// which reads the same index and refuses the same shapes, so a store damaged
// that way aborts before this function is called. The test therefore calls this
// directly, and says so.
func removePackages(database *db.DB, storeRoot string, packagesToRemove []*db.Package) {
	removed := 0
	var freed int64
	for _, pkg := range packagesToRemove {
		// Remove from store, but only if the recorded path is actually
		// inside the store root (guards against a poisoned DB entry).
		if pkg.StorePath != "" {
			if isWithinStore(storeRoot, pkg.StorePath) {
				// The entry is invalidated before its tree is removed,
				// so an interrupted removal leaves something the store
				// reports as absent rather than a truncated package.
				if err := store.RemoveEntry(pkg.StorePath); err != nil {
					// Keep the database row: it is the only record of
					// the entry left to delete, and gc can be re-run.
					fmt.Printf("  %s Failed to remove %s: %v\n", iconWarn(), pkg.Name, err)
					continue
				}
			} else {
				fmt.Printf("  ⚠ Skipping %s: store path %q is outside the store root\n", pkg.Name, pkg.StorePath)
			}
		}
		// Remove from database
		if err := database.DeletePackage(pkg.ID); err != nil {
			fmt.Printf("  %s Failed to remove %s: %v\n", iconWarn(), pkg.Name, err)
			continue
		}
		removed++
		freed += pkg.TotalSize
	}
	if removed == 0 {
		return
	}
	fmt.Printf("%s Removed %d package(s), freed %s\n", iconOK(), removed, formatSize(freed))
}

// tempDirToReap is one temp directory the sweep found, flattened from the two
// surfaces so gc reports them in one list.
type tempDirToReap struct {
	path string
	size int64
	// note says what the directory held. Reporting that is the point of the
	// sweep: these directories are invisible to every other command, so gc is
	// the only place a user can learn real content is sitting there.
	note string
}

// tempDirNote describes what a project-side temp entry held.
//
// Retired means an interrupted swap renamed the *previous* .lnpm/{package}
// aside, so it holds what was linked before rather than something half-written.
// What that is depends on which write path made it: Link populates a directory,
// so a retired directory is a complete copy of the package; LinkSource makes a
// link to a source directory, so a retired link is a link and holds no copy of
// anything. Saying "complete copy of the previous package" about a zero-byte
// link would be plainly false, and a false statement in the one place these
// directories are ever reported is worse than no statement.
func tempDirNote(retired, isLink bool) string {
	switch {
	case retired && isLink:
		return "link to the previously linked source directory"
	case retired:
		return "complete copy of the previous package"
	case isLink:
		return "link to a source directory, from an interrupted relink"
	default:
		return "incomplete"
	}
}

// reapTempDirs reclaims the temp directories an interrupted publish or relink
// left in the store and in the given projects, and reports how many it found.
// It reports what it found rather than what it removed so a dry run, which finds
// them and removes nothing, still counts as having something to clean up.
//
// It must be called while database is open. See the comment at its call site for
// why that ordering, and not an age threshold, is what makes the sweep safe.
func reapTempDirs(database *db.DB, s *store.Store, projectPaths []string, dryRun, yes bool) (int, error) {
	if !database.LockHeld() {
		// Refuse rather than sweep: without the database lock another process
		// may be populating one of these directories right now.
		return 0, fmt.Errorf("refusing to reclaim temp directories: the database lock is not held")
	}

	var found []tempDirToReap
	unreadable := 0

	// Both finders count the directories they could not read instead of failing.
	// Per ADR-0001 the direction is what decides: skipping one narrows what the
	// sweep reclaims and leaves the bytes for the next run, where aborting would
	// abandon every other project as well. gc is the command a user reaches for
	// when something is already wrong, so it has to keep working on what it can
	// still read — but the count is reported rather than only logged, because
	// these directories being invisible is half of what the sweep exists to fix.
	storeTemps, storeUnreadable := s.FindTempDirs()
	unreadable += storeUnreadable
	for _, t := range storeTemps {
		found = append(found, tempDirToReap{
			path: t.Path,
			size: t.Size,
			note: tempDirNote(false, false),
		})
	}

	for _, projectPath := range projectPaths {
		entries, projectUnreadable := link.FindTempEntries(projectPath)
		unreadable += projectUnreadable
		for _, e := range entries {
			found = append(found, tempDirToReap{
				path: e.Path,
				size: e.Size,
				note: tempDirNote(e.Retired, e.Link),
			})
		}
	}

	if unreadable > 0 {
		fmt.Printf("%s Could not scan %d director(ies) for temp directories; check them and re-run gc\n", iconWarn(), unreadable)
	}

	if len(found) == 0 {
		return 0, nil
	}
	total := len(found)

	fmt.Printf("Found %d temp director(ies) left by an interrupted publish or relink:\n", len(found))
	var totalSize int64
	for _, t := range found {
		fmt.Printf("  - %s (%s, %s)\n", t.path, formatSize(t.size), t.note)
		totalSize += t.size
	}
	fmt.Printf("Total size: %s\n", formatSize(totalSize))
	fmt.Println()

	if dryRun {
		return total, nil
	}
	if !confirm("Permanently delete these temp director(ies)?", yes) {
		fmt.Println("Skipped reclaiming temp directories.")
		return total, nil
	}

	removed := 0
	var freed int64
	for _, t := range found {
		// RemoveAll does not follow links, so a leftover from LinkSource loses
		// the link and not the source directory it points at.
		if err := os.RemoveAll(t.path); err != nil {
			fmt.Printf("  %s Failed to remove %s: %v\n", iconWarn(), t.path, err)
			continue
		}
		removed++
		freed += t.size
	}
	// The summary is skipped entirely when nothing was removed, as
	// removeOrphanedLinks and removePackages skip theirs; that block's doc
	// comment carries the reasoning. The count of what was found is still
	// returned, because gc's closing line asks whether there was anything to
	// clean up and not whether the sweep managed it.
	//
	// Unlike the other two this rule has no test behind it, and the reason is a
	// limit on the test rather than on the code. Reaching it needs every
	// os.RemoveAll in the loop to fail. They can: internal/store's
	// blockMarkerRemoval records that permissions, an open handle on Windows and
	// a full disk all reach a removal as an error. What it also records is that
	// none of those behaves the same way everywhere - the obstruction it settled
	// on works only because os.Remove refuses a non-empty directory, which
	// os.RemoveAll deletes without complaint, and a directory mode denies
	// nothing on Windows or as root. So there is no obstruction to RemoveAll
	// that holds on every platform lnpm builds for. That conclusion is drawn
	// here from what blockMarkerRemoval documents; it does not state it.
	if removed == 0 {
		return total, nil
	}
	fmt.Printf("%s Reclaimed %d temp director(ies), freed %s\n", iconOK(), removed, formatSize(freed))
	return total, nil
}

type linkToRemove struct {
	packageID   int64
	projectID   int64
	projectPath string
	reason      string
}

// skippedLink is one link gc declined to judge. It carries the package label as
// well as the path because the two ends answer different questions: the path is
// what the user has to go and plug back in, and the package is what stayed in
// the store because of it.
type skippedLink struct {
	projectPath  string
	packageLabel string
}

// label names the project end of the link, which is what the findings report has
// always printed. It falls back to the project ID because the two orphan reasons
// differ in what they know - a project whose record is gone has no path to print.
//
// One helper rather than the branch written twice, because the findings report
// and the failure report have to name the same link the same way. A user reading
// "Failed to remove the link to project ID 7" needs to match it to a line above
// it, and matching is what the report exists for.
func (l linkToRemove) label() string {
	if l.projectPath != "" {
		return l.projectPath
	}
	return fmt.Sprintf("project ID %d", l.projectID)
}

func countLinksForPackage(links []linkToRemove, packageID int64) int {
	count := 0
	for _, l := range links {
		if l.packageID == packageID {
			count++
		}
	}
	return count
}

// parseDuration parses a duration string like "30d", "1w", "24h"
func parseDuration(s string) (time.Duration, error) {
	if len(s) == 0 {
		return 0, fmt.Errorf("empty duration")
	}

	// Check for days/weeks suffix
	lastChar := s[len(s)-1]
	switch lastChar {
	case 'd', 'D':
		days := s[:len(s)-1]
		var d int
		if _, err := fmt.Sscanf(days, "%d", &d); err != nil {
			return 0, err
		}
		return time.Duration(d) * 24 * time.Hour, nil
	case 'w', 'W':
		weeks := s[:len(s)-1]
		var w int
		if _, err := fmt.Sscanf(weeks, "%d", &w); err != nil {
			return 0, err
		}
		return time.Duration(w) * 7 * 24 * time.Hour, nil
	default:
		// Fall back to standard Go duration parsing
		return time.ParseDuration(s)
	}
}

// isWithinStore reports whether p is the store root or nested inside it.
func isWithinStore(root, p string) bool {
	rootAbs, err1 := filepath.Abs(root)
	pAbs, err2 := filepath.Abs(p)
	if err1 != nil || err2 != nil {
		return false
	}
	rootAbs = filepath.Clean(rootAbs)
	pAbs = filepath.Clean(pAbs)
	return pAbs == rootAbs || strings.HasPrefix(pAbs, rootAbs+string(filepath.Separator))
}

// formatSize is defined in publish.go
