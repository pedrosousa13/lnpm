package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pedrosousa13/lnpm/internal/config"
	"github.com/pedrosousa13/lnpm/internal/debug"
)

// sentinelName is the store-level file recording that every entry written
// before completeness markers existed has been marked. Entries cannot be
// grandfathered — "no marker" has to mean "incomplete" for the marker to be
// worth anything — so they are marked once, here, instead.
const sentinelName = ".lnpm-markers-backfilled"

// backfillMarkers writes a completeness marker into every entry of storeRoot
// that lacks one, then writes the sentinel last.
//
// The sentinel is the commit point, mirroring the temp-dir-then-rename
// discipline the write path uses: if the backfill is interrupted, the sentinel
// is absent and the next run redoes the work. Marking an entry that already
// carries a marker is a no-op, so repeated and concurrent runs are harmless.
//
// An entry that cannot be marked — an unwritable directory, an unreadable
// package directory — is skipped rather than fatal, and skipping anything
// withholds the sentinel. Every store entry the pass could reach ends up
// marked, `lnpm doctor` keeps reporting the backfill as pending, and the next
// store open retries the rest. Failing instead would brick every command that
// opens the store on account of a single bad directory, which is a far worse
// outcome than one package that has to be re-published.
//
// One entry it can mark wrongly: a deletion in progress. `lnpm gc` removes an
// entry's marker before its tree, so a gc running concurrently with the very
// first backfill can present a marker-less entry that this pass then marks
// complete again. The window is bounded — it exists only before the sentinel
// is written, once in a store's life — and it only leaves anything behind if
// that gc's tree removal also fails, since a removal that finishes takes the
// directory and the freshly written marker with it.
func backfillMarkers(storeRoot string) error {
	sentinel := filepath.Join(storeRoot, sentinelName)
	_, err := os.Stat(sentinel)
	if err == nil {
		return nil
	}
	if !os.IsNotExist(err) {
		// Whether the backfill has run cannot be decided, and a store root
		// that cannot even be stat'ed is not one any command can work with.
		return fmt.Errorf("failed to read the completeness marker backfill state: %w", err)
	}

	entries, skipped := listEntries(storeRoot)

	marked := 0
	for _, entry := range entries {
		if hasMarker(entry) {
			continue
		}
		if err := writeMarker(entry, filepath.Base(entry)); err != nil {
			debug.Logf("store: leaving %s unmarked: %v", entry, err)
			skipped++
			continue
		}
		marked++
	}

	if skipped > 0 {
		debug.Logf("store: marked %d entries, skipped %d; leaving the backfill pending", marked, skipped)
		return nil
	}
	if err := os.WriteFile(sentinel, []byte(fmt.Sprintf("%d\n", markerSchemaVersion)), 0644); err != nil {
		debug.Logf("store: failed to record the completeness marker backfill: %v", err)
		return nil
	}
	debug.Logf("store: backfilled completeness markers for %d of %d entries", marked, len(entries))
	return nil
}

// BackfillDone reports whether the completeness marker backfill has run
// against the configured store. It only reads, so `lnpm doctor` can report the
// status without performing the backfill itself.
func BackfillDone() (bool, error) {
	storeRoot, err := config.GetPackageStorePath()
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(filepath.Join(storeRoot, sentinelName)); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// listEntries returns the path of every package entry in the store, and how
// many directories it could not read. Entries live at <store>/<name>/<hash>,
// with one extra level for scoped names: <store>/@scope/<name>/<hash>.
// Dot-prefixed directories are the store's own (the write path's temp
// directories), never entries.
//
// A directory that cannot be read is reported rather than returned as a
// failure, so the caller can mark what it did find and leave the backfill
// pending for the rest.
func listEntries(storeRoot string) (entries []string, unreadable int) {
	names, err := readDirs(storeRoot)
	if err != nil {
		debug.Logf("store: cannot scan %s for entries: %v", storeRoot, err)
		return nil, 1
	}

	var packageDirs []string
	for _, name := range names {
		dir := filepath.Join(storeRoot, name)
		if !strings.HasPrefix(name, "@") {
			packageDirs = append(packageDirs, dir)
			continue
		}
		scoped, err := readDirs(dir)
		if err != nil {
			debug.Logf("store: cannot scan scope %s: %v", dir, err)
			unreadable++
			continue
		}
		for _, s := range scoped {
			packageDirs = append(packageDirs, filepath.Join(dir, s))
		}
	}

	for _, dir := range packageDirs {
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
