//go:build windows

package store

import (
	"os"
	"path/filepath"
	"testing"
)

// TestIssue444StoreProtectionSurvivesAConsumerUnlink is a probe, not a
// regression test. Nothing in this repo changed to make it pass, and it is not
// paired with a fix: it exists to answer issue #444's first acceptance
// requirement — "reproduce it. Confirm on real Windows" — using the only
// Windows machine this project has, the windows-latest CI runner.
//
// PASS means the protection held. The store's copy is still read-only and still
// refuses a write after a consumer's hard link to it was retired, so #444's
// reasoning does not reproduce and the issue can be closed. #444 names that
// outcome as a good one, not as a failure of this test.
//
// FAIL means #444 is real and reproduced on this runner. The failure text
// spells out what that costs and says not to fix it from here.
//
// What was established before this was written, and what was not. The mechanism
// was read out of Go 1.26.7's source on Linux; the author had no Windows machine
// and observed none of it running. syscall.Chmod (syscall/syscall_windows.go)
// implements a mode with no S_IWRITE by setting FILE_ATTRIBUTE_READONLY, and
// os.Stat reports that attribute back as 0444 (os/types_windows.go). The
// attribute is on the file, and every hard link to a file shares it. os.Remove
// (os/file_windows.go) reacts to a DeleteFile that failed on a read-only file by
// clearing FILE_ATTRIBUTE_READONLY and retrying the delete, and it never puts
// the attribute back. Whether that clearing is observable on the store's
// surviving link is exactly what no amount of reading settles. This run is the
// measurement.
//
// Two things read from the same source that this test should be checked against
// rather than believed, because a prediction in a comment is not evidence:
//
//   - The os.Remove row is the one predicted to reproduce, for the chain above.
//   - The os.RemoveAll row is predicted NOT to reproduce on a current Windows
//     with NTFS, which is what windows-latest is. os.RemoveAll does not recurse
//     through os.Remove: removeall_at.go is built for windows, and its recursion
//     calls removefileat, which is windows.Deleteat, which asks for
//     FILE_DISPOSITION_IGNORE_READONLY_ATTRIBUTE and so deletes a read-only file
//     without clearing anything. Its fallback for older Windows and for
//     filesystems that refuse that flag does clear the attribute, the same way
//     os.Remove does. So a red RemoveAll row here would mean the runner took the
//     fallback, and a green one does not clear other Windows hosts.
//
// The split matters because os.RemoveAll is what the product actually uses on
// both paths #444 asks about: Linker.Unlink retires .lnpm/{package} with
// os.RemoveAll, and the relink path retires the previous tree with os.RemoveAll
// on retiredPath (internal/link/link.go). The per-file os.Remove row is kept
// because it isolates the unlink primitive from the tree walk — if the two rows
// disagree, that disagreement is the answer to #444's second question.
func TestIssue444StoreProtectionSurvivesAConsumerUnlink(t *testing.T) {
	t.Run("consumer link retired with os.Remove", func(t *testing.T) {
		storeCopy, consumerDir := newUnlinkProbe(t)

		if err := os.Remove(filepath.Join(consumerDir, probeFileName)); err != nil {
			t.Fatalf("retire the consumer's link with os.Remove: %v", err)
		}

		requireStoreCopyStillProtected(t, storeCopy, "os.Remove on the link itself")
	})

	t.Run("consumer tree retired with os.RemoveAll", func(t *testing.T) {
		storeCopy, consumerDir := newUnlinkProbe(t)

		// The RemoveAll is aimed at the directory holding the link rather than at
		// the link, because that is what Linker.Unlink and the relink path do. A
		// RemoveAll pointed straight at a file is served by os.Remove in its first
		// line and would only repeat the row above.
		if err := os.RemoveAll(consumerDir); err != nil {
			t.Fatalf("retire the consumer's tree with os.RemoveAll: %v", err)
		}

		requireStoreCopyStillProtected(t, storeCopy, "os.RemoveAll on the tree holding the link")
	})
}
