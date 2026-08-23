package store

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/pedrosousa13/lnpm/internal/config"
	"github.com/pedrosousa13/lnpm/internal/debug"
)

// IncompleteEntries returns every entry of the configured store that a read
// path would refuse, and how many directories the scan could not read. It only
// reads, so `lnpm doctor` can report what it finds without repairing anything.
//
// This replaced a one-time backfill that wrote a completeness marker into every
// unmarked entry, deriving the hash from the directory name. That pass existed
// so entries written before markers did would not all read as missing, and it
// was the laundering mechanism in #330: a gutted entry an interrupted gc had
// left behind was marked complete on the next store open, permanently. Nothing
// available here can establish that an entry is whole — that needs re-hashing
// its content, which is #333 — so an entry lnpm did not commit is left unmarked
// and reported, rather than blessed.
//
// The consequence, deliberately: a store entry written before markers existed
// now reads as incomplete and its package has to be re-published. That is the
// same answer lnpm gives for a gutted entry, because from here the two are
// indistinguishable.
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
