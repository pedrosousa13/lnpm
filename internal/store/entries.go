package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pedrosousa13/lnpm/internal/config"
	"github.com/pedrosousa13/lnpm/internal/debug"
)

// sentinelName is the store-level file recording that the one-time legacy
// decision in backfillLegacyStore has been taken for this store, whichever way
// it went. Its presence means "do not scan for this again"; it does not mean
// any entry was marked. Stores created by lnpm 2.0.0 and 2.1.0 carry one
// written by the pass this replaces, and it is read the same way here.
const sentinelName = ".lnpm-markers-backfilled"

// backfillLegacyStore marks every entry of a store that has never seen a
// completeness marker, once, and marks nothing in any other store.
//
// The gate is the whole safety argument, and the asymmetry in it is not
// obvious, so: markers shipped in 2.0.0 (#273) and no release before it wrote
// one. A store in which *no* entry is marked therefore predates 2.0.0. In that
// store a gutted entry was already undetectable — nothing recorded which
// entries were whole, so lnpm linked whatever it found — and marking its
// entries leaves the user exactly where 1.x left them, while refusing them
// instead would break every command against a store lnpm itself wrote. A store
// in which *any* entry is marked is a 2.x store, and an unmarked entry in it is
// the interrupted-gc case #330 exists to catch, so nothing there is marked.
//
// That distinction is what separates this from the pass #330 was filed about.
// That one marked any unmarked entry on every store open, so a gutted 2.x entry
// was laundered into a complete one, permanently. This one asks about the store
// rather than the entry, and only ever runs before the store's first marker
// exists.
//
// A pass that cannot finish marks nothing: the markers it did write are removed
// again and the sentinel is withheld, so the store is still recognisably
// never-marked and the next open retries the lot. Leaving them would make the
// store look 2.x to the gate above, and the entries it had not reached yet
// would be refused forever.
func backfillLegacyStore(storeRoot string) error {
	sentinel := filepath.Join(storeRoot, sentinelName)
	if _, err := os.Stat(sentinel); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		// Whether the decision has been taken cannot be read, and a store root
		// that cannot even be stat'ed is not one any command can work with.
		return fmt.Errorf("failed to read the completeness marker backfill state: %w", err)
	}

	entries, unreadable := listEntries(storeRoot)

	var unmarked []string
	for _, entry := range entries {
		if hasMarker(entry) {
			// A 2.x store. Record the decision so this scan runs once, and
			// leave every unmarked entry to the read path.
			debug.Logf("store: %s already has completeness markers; not backfilling", storeRoot)
			recordBackfillDecision(sentinel)
			return nil
		}
		unmarked = append(unmarked, entry)
	}

	if unreadable > 0 {
		// A marked entry may be sitting in the part that could not be read, so
		// "this store has never been marked" is not established. Withhold the
		// sentinel and retry on the next open rather than guess; doctor reports
		// the unreadable directories meanwhile.
		debug.Logf("store: cannot classify %s, %d director(ies) unreadable; leaving the backfill pending", storeRoot, unreadable)
		return nil
	}

	marked := make([]string, 0, len(unmarked))
	for _, entry := range unmarked {
		if err := writeMarker(entry, filepath.Base(entry)); err != nil {
			debug.Logf("store: could not mark %s, undoing the backfill: %v", entry, err)
			for _, done := range marked {
				_ = os.Remove(filepath.Join(done, markerName))
			}
			return nil
		}
		marked = append(marked, entry)
	}

	recordBackfillDecision(sentinel)
	debug.Logf("store: backfilled completeness markers for %d pre-2.0.0 entries", len(marked))
	return nil
}

// recordBackfillDecision writes the sentinel. A failure is logged and swallowed:
// the only cost is that the next store open scans and decides again, and
// failing here would brick every command that opens the store.
func recordBackfillDecision(sentinel string) {
	if err := os.WriteFile(sentinel, []byte(fmt.Sprintf("%d\n", markerSchemaVersion)), 0644); err != nil {
		debug.Logf("store: failed to record the completeness marker backfill: %v", err)
	}
}

// LegacyBackfillPending reports whether the configured store is still waiting
// for its one-time legacy marking: it holds entries, none of them is marked,
// and the decision has not been recorded.
//
// `lnpm doctor` asks so it can say "run any command that opens the store"
// instead of listing every entry as damaged and sending the user to re-publish
// their whole store. doctor never opens the store itself, so on a 1.x store it
// is the one command that can see this state and do nothing about it.
func LegacyBackfillPending() (bool, error) {
	storeRoot, err := config.GetPackageStorePath()
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(filepath.Join(storeRoot, sentinelName)); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, err
	}

	entries, _ := listEntries(storeRoot)
	if len(entries) == 0 {
		return false, nil
	}
	for _, entry := range entries {
		if hasMarker(entry) {
			return false, nil
		}
	}
	return true, nil
}

// IncompleteEntries returns every entry of the configured store that a read
// path would refuse, and how many directories the scan could not read. It only
// reads, so `lnpm doctor` can report what it finds without repairing anything.
//
// An entry written before completeness markers existed is reported by this the
// same as a gutted one, because from here the two are indistinguishable. What
// keeps that from faulting every pre-2.0.0 store is backfillLegacyStore, which
// marks such a store once when a command opens it; the gate there is what makes
// the difference between the two safe to ignore here.
func IncompleteEntries() (incomplete []string, unreadable int, err error) {
	storeRoot, err := config.GetPackageStorePath()
	if err != nil {
		return nil, 0, err
	}
	if _, err := os.Stat(storeRoot); os.IsNotExist(err) {
		// Nothing has ever been published. An absent package-store directory is
		// an empty store rather than an unreadable one - store.New creates it -
		// and reporting it would fault a machine on which lnpm simply has not
		// been used yet.
		return nil, 0, nil
	}

	entries, unreadable := listEntries(storeRoot)
	for _, entry := range entries {
		if err := CheckComplete(entry); err != nil {
			debug.Logf("store: %v", err)
			incomplete = append(incomplete, entry)
		}
	}
	return incomplete, unreadable, nil
}

// listEntries returns the path of every package entry in the store, and how
// many directories it could not read. Entries live at <store>/<name>/<hash>,
// with one extra level for scoped names: <store>/@scope/<name>/<hash>.
// Dot-prefixed directories are the store's own (the write path's temp
// directories), never entries.
//
// A directory that cannot be read is reported rather than returned as a
// failure, so the caller can act on what it did find and say how much of the
// store it could not see.
func listEntries(storeRoot string) (entries []string, unreadable int) {
	dirs, unreadable := packageDirs(storeRoot)

	for _, dir := range dirs {
		hashes, err := readDirs(dir)
		if err != nil {
			debug.Logf("store: cannot scan package %s: %v", dir, err)
			unreadable++
			continue
		}
		for _, hash := range hashes {
			entries = append(entries, filepath.Join(dir, hash))
		}
	}
	return entries, unreadable
}

// packageDirs returns the directory holding the entries of every package in the
// store, and how many directories could not be read. A package's entries live
// at <store>/<name>/<hash>, with one extra level for scoped names, so these are
// the directories <hash> entries and the write path's temp directories sit in.
func packageDirs(storeRoot string) (dirs []string, unreadable int) {
	names, err := readDirs(storeRoot)
	if err != nil {
		debug.Logf("store: cannot scan %s for entries: %v", storeRoot, err)
		return nil, 1
	}

	for _, name := range names {
		dir := filepath.Join(storeRoot, name)
		if !strings.HasPrefix(name, "@") {
			dirs = append(dirs, dir)
			continue
		}
		scoped, err := readDirs(dir)
		if err != nil {
			debug.Logf("store: cannot scan scope %s: %v", dir, err)
			unreadable++
			continue
		}
		for _, s := range scoped {
			dirs = append(dirs, filepath.Join(dir, s))
		}
	}
	return dirs, unreadable
}

// readDirs returns the names of dir's subdirectories, skipping dot-prefixed ones.
func readDirs(dir string) ([]string, error) {
	items, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, item := range items {
		if item.IsDir() && !strings.HasPrefix(item.Name(), ".") {
			names = append(names, item.Name())
		}
	}
	return names, nil
}
