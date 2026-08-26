package store

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

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
// It holds on every materialisation path because all three end up with the
// source's mode — but they arrive there by three different mechanisms, which is
// worth naming because a reader checking one will not find the same code in the
// others. stripLifecycleScripts in store.go documents the same split for the
// same reason. link.copyFile chmods the destination explicitly (#139).
// fsutil.Reflink chmods the clone on Linux (reflink_linux.go) but not on darwin,
// where unix.Clonefile carries the mode across as part of the metadata it
// clones. reflink_other.go has no clone path at all — Reflink always fails
// there and the caller falls back to a copy. A read-only entry therefore yields
// a read-only consumer copy on any filesystem and in either link mode, without
// giving up the space hard linking exists for.
//
// Of those, only the copy path is exercised by this repo's tests on the usual
// dev and CI host: ext4 does not support FICLONE, as reflink_linux.go's own
// comment records, so the clone branch never runs there. The reflink half of
// the claim above is read from the two implementations rather than measured.
//
// Only regular files are stripped, and only the write bits. Directories keep
// theirs because unlinking a file on Unix needs write permission on its parent
// and not on the file, so a pass that reached directories would leave `lnpm gc`
// unable to delete the entry it had just protected. And the strip is
// mode &^ writeBits rather than a flat 0444, so a 0755 bin script lands at 0555
// and still runs from node_modules/.bin.
//
// One invariant this deliberately breaks, recorded here because the code cannot
// state it anywhere else: pack folds Mode.Perm() into the content hash, and the
// strip happens after that hash was computed, so a protected entry no longer
// hashes to the hash it is filed under. Nothing re-derives that hash from the
// store's own modes today — publish and push hash the source, and the manifest
// checks in internal/cli use the modes recorded in the database — but a check
// that re-hashes store content has to add the write bits back before it does.
// That is #439's constraint, not this one's.
const writeBits = 0222

// protectTree strips the write bits from every regular file under root.
//
// The entry's completeness marker is exempt because it is the store's own
// bookkeeping rather than package content — GetFiles drops it from what
// consumers are handed for the same reason. The exemption is by path at the
// entry root, which is where the marker lives and how GetFiles recognises it; a
// package shipping a file of that name deeper in its tree is content and gets
// protected like the rest.
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

// protectionSchemaVersion identifies the sentinel payload's shape. It is this
// sentinel's own, deliberately not the completeness marker's: the two files
// record unrelated decisions and versioning them together would tie a change in
// one payload to the other.
const protectionSchemaVersion = 1

// protectionRecord is what the sentinel holds. Entries is how many entries the
// pass walked and Unprotected how many of them it could not finish, so what is
// recorded says whether the store was protected in full or only in part rather
// than only that something ran. Nothing reads it back yet; it is written for a
// reader that has to tell a clean pass from a partial one, in the way the
// completeness marker records its own schema version for a reader that does not
// exist either.
type protectionRecord struct {
	SchemaVersion int `json:"schemaVersion"`
	Entries       int `json:"entries"`
	Unprotected   int `json:"unprotected"`
}

// unrecordedPasses names the store roots whose protection pass ran in this
// process but whose sentinel could not be written — a store root that is not
// writable, most plainly.
//
// Without it that store is walked in full again by the next store open, and the
// walk reads every file of every entry. lnpm is a one-command-per-process CLI,
// so this bounds a store that cannot be recorded at one walk per command rather
// than one per store open, and a process that opens the store repeatedly — the
// test suite, a future daemon — pays it once. It cannot bound the per-command
// cost any further from inside the process, and that is stated rather than
// hidden: a store whose root stays unwritable is walked once per lnpm
// invocation, forever.
var unrecordedPasses sync.Map

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
// in entries.go, whose gate it otherwise resembles. That pass decides which
// entries lnpm may trust, so a failure there has to be undone and retried. This
// one hardens entries that are already trusted: one it could not chmod is
// exactly as exposed as it was before this pass existed, and neither refusing to
// open the store nor re-walking every file of it on each command would make that
// entry any safer. So the decision is recorded even when entries were missed —
// with the count of what was missed in it, so "done" never stands for "all of
// them" — and the cost stays bounded at one walk per store.
//
// Two things it deliberately does not cover, both left to `lnpm doctor` (#439)
// rather than to a pass that runs on every store open. An entry published by an
// older lnpm *after* this store was migrated is unprotected and stays that way.
// And nothing here verifies content: an entry already poisoned before the
// upgrade is protected in its tampered state, since the protection is a lock and
// not a repair.
func protectExistingEntries(storeRoot string) {
	if _, skip := unrecordedPasses.Load(storeRoot); skip {
		return
	}

	sentinel := filepath.Join(storeRoot, contentProtectedSentinelName)
	if _, err := os.Stat(sentinel); err == nil {
		return
	} else if !os.IsNotExist(err) {
		debug.Logf("store: cannot read the content protection state of %s: %v", storeRoot, err)
		return
	}

	entries, unreadable := listEntries(storeRoot)
	// A directory the scan could not read holds entries this pass cannot reach,
	// so it counts against the pass exactly as a failed chmod does.
	unprotected := unreadable
	for _, entry := range entries {
		if err := protectTree(entry); err != nil {
			debug.Logf("store: could not write protect %s: %v", entry, err)
			unprotected++
		}
	}

	if unprotected > 0 {
		debug.Logf("store: write protected %s in part, %d of %d entries and unreadable director(ies) left as they were", storeRoot, unprotected, len(entries))
	} else {
		debug.Logf("store: write protected all %d entries of %s", len(entries), storeRoot)
	}

	if err := recordProtectionPass(sentinel, len(entries), unprotected); err != nil {
		debug.Logf("store: failed to record the content protection pass: %v", err)
		unrecordedPasses.Store(storeRoot, struct{}{})
	}
}

// recordProtectionPass writes the sentinel. It is separate from
// recordBackfillDecision in entries.go rather than sharing it: that one writes a
// different file recording a different decision, and its payload is the
// completeness marker's schema version, which has nothing to say about this
// pass.
func recordProtectionPass(sentinel string, entries, unprotected int) error {
	payload, err := json.Marshal(protectionRecord{
		SchemaVersion: protectionSchemaVersion,
		Entries:       entries,
		Unprotected:   unprotected,
	})
	if err != nil {
		return err
	}
	return os.WriteFile(sentinel, append(payload, '\n'), 0644)
}
