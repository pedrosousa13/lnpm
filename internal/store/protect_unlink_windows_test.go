//go:build windows

package store

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRemoveAllRetirementKeepsStoreContentProtected is the regression guard that
// came out of issue #444. It pins the retirement call lnpm's own paths use.
//
// PASS means what the product does today is safe: a consumer's .lnpm/{package}
// tree can be retired with os.RemoveAll and the store's canonical copy is still
// read-only and still refuses a write afterwards, so #333's protection outlives
// the unlink.
//
// FAIL means #333's protection is broken on Windows on a path the product takes.
// It would most likely mean one of two things: someone replaced an os.RemoveAll
// in internal/link or internal/cli with a per-file os.Remove, or Go changed how
// RemoveAll deletes on Windows. Both are covered by the failure text and by
// TestOsRemoveClearsWindowsWriteProtection below.
//
// Why this call and not another. As of 2026-08-27 the retirement sites that
// delete a tree holding hard links into the store are all os.RemoveAll:
// Linker.Unlink at internal/link/link.go:569 (.lnpm/{package}), the relink path
// at link.go:417 and link.go:529 (the retired previous tree), the staging tree
// at link.go:201, and retreat at internal/cli/retreat.go:290 (.lnpm). The hard
// links themselves are made at link.go:255 and link.go:277.
//
// Measured on Windows CI run 33066579971, head SHA 7b59495, job "Test
// (Windows)": this case passed there under its previous name. The rest of what
// that run established is in protect_unlink_probe_test.go's header. CI remains
// the measurement — none of this was observed on a Windows machine by the
// author, who has none.
func TestRemoveAllRetirementKeepsStoreContentProtected(t *testing.T) {
	storeCopy, consumerDir := newUnlinkProbe(t)

	// The RemoveAll is aimed at the directory holding the link rather than at the
	// link itself, because that is what every site listed above does. A RemoveAll
	// pointed straight at a file is served by os.Remove in removeall_at.go's first
	// line, so it would silently become the hazard case below instead of this one.
	if err := os.RemoveAll(consumerDir); err != nil {
		t.Fatalf("retire the consumer's tree with os.RemoveAll: %v", err)
	}

	requireStoreCopyStillProtected(t, storeCopy, "os.RemoveAll on the tree holding the link")
}

// TestOsRemoveClearsWindowsWriteProtection is a characterization test. It passes
// by asserting that a hazard exists, which makes it the odd one out in this
// package: it is not a guarantee lnpm offers, and it is not a bug lnpm has.
//
// What it documents is a real Go behaviour on Windows, reported in issue #444
// and reproduced by this exact case on CI run 33066579971, head SHA 7b59495: on
// a read-only file os.Remove's DeleteFile fails, so os.Remove clears
// FILE_ATTRIBUTE_READONLY and retries (os/file_windows.go:247). The attribute
// lives on the file, which every hard link shares, and nothing puts it back. The
// store's surviving copy came back `-rw-rw-rw-` and accepted a 29-byte write.
//
// lnpm avoids this rather than being exposed to it, and that is why the hazard
// is latent. Audited by grep over non-test .go files on 2026-08-27: no bare
// os.Remove in product code targets a hard link to store content. The call sites
// hit the node_modules symlink (link.go:575, cli/retreat.go:228), project
// lockfiles (cli/remove.go:144, cli/restore.go:83 and :255, cli/retreat.go:541),
// temp or staged files (link.go:500 — a symlink from newTempLink,
// store.go:610 and :616, fsutil/write.go:105, fsutil/reflink_linux.go:58,
// cli/update.go:284 and :326, gitignore.go:65 and :113,
// pack/workspacedeps.go:222, cli/doctor.go:59), an empty directory
// (link.go:777), and the store's own marker files (store/marker.go:84,
// store/entries.go:100). That is a grep on one date, not a rule anything
// enforces: nothing stops a future change from unlinking store content per file,
// and the test above is what would catch it.
//
// PASS means the hazard is still real, so the guard above is load-bearing.
//
// FAIL means Go's behaviour changed — os.Remove no longer clears the attribute,
// or no longer needs to. That is not a defect in lnpm and must not be "fixed" by
// changing product code. Re-read #444, re-read os/file_windows.go for the Go
// version in go.mod, and reconsider whether the guard above still pins anything.
func TestOsRemoveClearsWindowsWriteProtection(t *testing.T) {
	storeCopy, consumerDir := newUnlinkProbe(t)

	if err := os.Remove(filepath.Join(consumerDir, probeFileName)); err != nil {
		t.Fatalf("retire the consumer's link with os.Remove: %v", err)
	}

	info, err := os.Stat(storeCopy)
	if err != nil {
		t.Fatalf("the store's copy is gone after os.Remove of the consumer's link: %v", err)
	}
	perm := info.Mode().Perm()
	written := describeWriteAttempt(storeCopy)

	if perm&writeBits != 0 && written != "" {
		return
	}

	t.Fatalf(`GO'S WINDOWS BEHAVIOUR CHANGED - this is not an lnpm defect.

This test documents a hazard lnpm deliberately avoids rather than a guarantee it
makes, so it passes when the hazard is present. It is failing because the hazard
is no longer reproducible here.

What ran: one file inside a store entry, write protected by the store's own
protectTree; one hard link to it in a directory standing in for a consumer's
.lnpm/{package} tree; that link retired with os.Remove.

What the store's own surviving copy looks like afterwards:
  mode reported by os.Stat: %v (this test expects a write bit here, %#o)
  write attempted through it: %q (this test expects an accepted write)

On CI run 33066579971 the same case returned -rw-rw-rw- and an accepted 29-byte
write, which is issue #444's mechanism: os.Remove clears
FILE_ATTRIBUTE_READONLY to make its own DeleteFile succeed
(os/file_windows.go:247), the attribute is shared by every hard link to the
file, and nothing restores it.

Do NOT change product code to make this pass. Either Go changed how os.Remove
deletes a read-only file, or this runner's filesystem behaves differently from
the one that was measured. Establish which, then reconsider whether
TestRemoveAllRetirementKeepsStoreContentProtected above is still load-bearing
and whether #444 can be closed outright.`, perm, writeBits, written)
}
