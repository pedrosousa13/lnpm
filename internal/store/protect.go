package store

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/pedrosousa13/lnpm/internal/debug"
)

// writeBits are the permission bits a committed store entry's content must not
// carry, for owner, group and other alike.
//
// The store's canonical copy is write protected because a consumer holds hard
// links to it. internal/link materialises a package by reflink, hard link or
// copy, and on the hard-link path the file in .lnpm/{package} *is* the store's
// file: one truncating write inside a consumer project rewrites the entry for
// that content hash, and every later `add` of that version serves the tampered
// bytes. Taking the write bits off the store's copy turns that write into an
// EACCES the consumer sees, instead of silent poisoning nothing afterwards
// checks (#333).
//
// It holds on every materialisation path because all three preserve the source
// mode: fsutil.Reflink chmods the clone to srcInfo.Mode(), and link.copyFile
// does the same (#139). A read-only entry therefore yields a read-only consumer
// copy on any filesystem and in either link mode, without giving up the space
// hard linking exists for.
//
// Only regular files are stripped, and only the write bits. Directories keep
// theirs because unlinking a file on Unix needs write permission on its parent
// and not on the file, so a pass that reached directories would leave `lnpm gc`
// unable to delete the entry it had just protected. And the strip is
// mode &^ writeBits rather than a flat 0444, so a 0755 bin script lands at 0555
// and still runs from node_modules/.bin.
const writeBits = 0222

// protectTree strips the write bits from every regular file under root.
//
// The entry's completeness marker is exempt. It is the store's own bookkeeping
// rather than package content — GetFiles drops it from what consumers are
// handed for the same reason — and RemoveEntry unlinks it before the tree, so
// it is the one file inside an entry the store itself has to keep removing as
// part of ordinary operation. The exemption is by path at the entry root, which
// is where the marker lives and how GetFiles recognises it; a package shipping a
// file of that name deeper in its tree is content and gets protected like the
// rest.
//
// Symlinks are left alone, and not only because there is nothing useful to
// strip: os.Chmod follows a link, so chmodding one would change the mode of
// whatever it points at, which for a package shipping an absolute or escaping
// symlink is a file outside the store.
//
// A file that disappears mid-walk is not an error. The one-time pass over an
// existing store can run while `lnpm gc` is removing an entry, and an entry
// being deleted needs no protecting.
func protectTree(root string) error {
	marker := filepath.Join(root, markerName)

	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if !d.Type().IsRegular() || path == marker {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		mode := info.Mode()
		if mode&writeBits == 0 {
			return nil
		}
		if err := os.Chmod(path, mode&^writeBits); err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		return nil
	})
}

// contentProtectedSentinelName records that the one-time pass below has run for
// this store, so it runs once rather than on every store open.
const contentProtectedSentinelName = ".lnpm-content-protected"

// protectExistingEntries write protects every entry of a store written before
// entries were protected on the way in, once.
//
// The alternative was to protect only what is published from here on. That
// leaves every existing store exposed for as long as its packages are not
// re-published — and the hard links consumers already hold into those entries
// stay writable with them — which is a state to leave users in deliberately or
// not at all. The pass is what makes the fix reach a store that predates it.
//
// It is best effort, and that is where it parts company with backfillLegacyStore
// above, whose gate it otherwise resembles. That pass decides which entries lnpm
// may trust, so a failure there has to be undone and retried. This one hardens
// entries that are already trusted: one it could not chmod is exactly as exposed
// as it was before this pass existed, and neither refusing to open the store nor
// re-walking every file of it on each command would make that entry any safer.
// So failures are logged and the decision is recorded regardless, and the cost
// of the pass stays bounded at one walk per store.
//
// Two things it deliberately does not cover, both left to `lnpm doctor` (#439)
// rather than to a pass that runs on every store open. An entry published by an
// older lnpm *after* this store was migrated is unprotected and stays that way.
// And nothing here verifies content: an entry already poisoned before the
// upgrade is protected in its tampered state, since the protection is a lock and
// not a repair.
func protectExistingEntries(storeRoot string) {
	sentinel := filepath.Join(storeRoot, contentProtectedSentinelName)
	if _, err := os.Stat(sentinel); err == nil {
		return
	} else if !os.IsNotExist(err) {
		debug.Logf("store: cannot read the content protection state of %s: %v", storeRoot, err)
		return
	}

	entries, unreadable := listEntries(storeRoot)
	if unreadable > 0 {
		debug.Logf("store: %d director(ies) of %s could not be read while write protecting it", unreadable, storeRoot)
	}
	for _, entry := range entries {
		if err := protectTree(entry); err != nil {
			debug.Logf("store: could not write protect %s: %v", entry, err)
		}
	}

	if err := os.WriteFile(sentinel, []byte(fmt.Sprintf("%d\n", markerSchemaVersion)), 0644); err != nil {
		debug.Logf("store: failed to record the content protection pass: %v", err)
		return
	}
	debug.Logf("store: write protected %d existing entries in %s", len(entries), storeRoot)
}
