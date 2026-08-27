//go:build !windows

package store

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRetirementKeepsStoreContentProtectedOnUnix is the control for the Windows
// tests in protect_unlink_windows_test.go, and it is not an answer to issue
// #444.
//
// #444 is a Windows claim and this file cannot speak to it. On Unix the write
// protection is permission bits on the inode, and unlinking one name does not
// touch them, so both rows are expected to pass here and their passing says
// nothing about Windows. That is why both retirement calls are kept in this
// file while the Windows side splits them: the os.Remove row is a hazard there
// and an ordinary pass here, and the difference is the whole of #444.
//
// What this does establish is that the shared harness both files use — build an
// entry, protect it through protectTree, hard link it, retire the link, then
// read the mode and attempt a write — measures what it claims to, on the one
// platform the author could run. Without it, a green Windows guard could mean
// the protection held or could mean the fixture never protected anything.
// newUnlinkProbe's own pre-checks cover the second case; this test is what
// confirms those pre-checks and the verdict compose into a working measurement.
//
// Measured on 2026-08-27 on Linux, Go 1.26.7, both rows running and passing,
// with go vet ./... clean first and internal/store printing its ok result line.
// The same day's CI run 33066579971 passed its Linux and macOS jobs.
func TestRetirementKeepsStoreContentProtectedOnUnix(t *testing.T) {
	// Root writes through a mode with no write bit, so newUnlinkProbe's own
	// pre-check would fail there and report a fixture problem for what is really
	// a privilege one. This is the same guard protect_test.go and entries_test.go
	// use. It is in this file and not in the shared harness because it answers a
	// Unix question: os.Geteuid is -1 on Windows and could never skip anything
	// there.
	if os.Geteuid() == 0 {
		t.Skip("running as root: a mode with no write bit does not deny a write")
	}

	t.Run("consumer link retired with os.Remove", func(t *testing.T) {
		storeCopy, consumerDir := newUnlinkProbe(t)

		if err := os.Remove(filepath.Join(consumerDir, probeFileName)); err != nil {
			t.Fatalf("retire the consumer's link with os.Remove: %v", err)
		}

		requireStoreCopyStillProtected(t, storeCopy, "os.Remove on the link itself")
	})

	t.Run("consumer tree retired with os.RemoveAll", func(t *testing.T) {
		storeCopy, consumerDir := newUnlinkProbe(t)

		if err := os.RemoveAll(consumerDir); err != nil {
			t.Fatalf("retire the consumer's tree with os.RemoveAll: %v", err)
		}

		requireStoreCopyStillProtected(t, storeCopy, "os.RemoveAll on the tree holding the link")
	})
}
