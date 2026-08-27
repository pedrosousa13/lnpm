package store

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// The probe for issue #444 is split over three files. This one holds the parts
// that are platform independent, so that the Windows probe and the Unix control
// measure the same thing by construction rather than by two similar-looking
// copies. protect_unlink_windows_test.go holds the probe; protect_unlink_unix_test.go
// holds the control, and each header says what a result on that platform does
// and does not settle.
//
// The shape being probed is the one #333's protection assumes. The store's
// canonical copy of a file and a consumer's hard link to it are one file, under
// two names, and lnpm protects that file by stripping the write bits from the
// store's name. #444's claim is that on Windows retiring the consumer's name
// puts write access back, because the protection there is an attribute on the
// shared file rather than a permission on one name.
//
// Nothing here is a fix and nothing here pins a fix. #444 lists two candidate
// fixes and picks neither; this answers whether either is needed.

// probeFileName is the one file in the probe's store entry.
//
// It is deliberately not markerName: protectTree exempts the completeness
// marker at an entry root, so a probe using that name would build an
// unprotected fixture and its assertion would then be about nothing.
const probeFileName = "index.js"

// probeContent is what the probe's file holds. Its only requirement is that a
// successful write can be told from it, which poisonContent below supplies.
const probeContent = "module.exports = 1;\n"

// poisonContent is written through the store's surviving copy if the write is
// accepted after the retirement. It is only ever reached on a build where the
// protection has already failed, and it is written rather than merely attempted
// so the failure reports a completed poisoning rather than an opened handle.
const poisonContent = "module.exports = 'poisoned';\n"

// newUnlinkProbe builds a store entry holding one protected file, plus a hard
// link to that file inside a separate directory standing in for a consumer's
// .lnpm/{package} tree. It returns the store's path to the file and the
// consumer directory holding the link.
//
// The protection is applied by calling protectTree, which is the store's own
// code and the same call Store.Store makes over a finished entry
// (internal/store/store.go). Chmodding the file here instead would probe the
// operating system; going through protectTree probes lnpm.
//
// Every failure below is a t.Fatalf rather than a t.Skip. A skipped probe and a
// passing one are indistinguishable in CI output, and this test exists to
// produce a measurement, so an environment that cannot build the fixture has to
// say so in red. That includes the hard link: a filesystem with no hard links
// cannot express the situation #444 is about at all.
func newUnlinkProbe(t *testing.T) (storeCopy, consumerDir string) {
	t.Helper()

	root := t.TempDir()
	entry := filepath.Join(root, "entry")
	consumerDir = filepath.Join(root, "consumer", "pkg")
	for _, dir := range []string{entry, consumerDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("create %s: %v", dir, err)
		}
	}

	storeCopy = filepath.Join(entry, probeFileName)
	if err := os.WriteFile(storeCopy, []byte(probeContent), 0644); err != nil {
		t.Fatalf("write the store's copy: %v", err)
	}

	if err := protectTree(entry); err != nil {
		t.Fatalf("protectTree: %v", err)
	}

	// The fixture has to start protected, or the assertion after the retirement
	// would pass for the wrong reason on one platform and fail for the wrong
	// reason on another. This is the control that makes the later verdict mean
	// what it says.
	info, err := os.Stat(storeCopy)
	if err != nil {
		t.Fatalf("stat the store's copy: %v", err)
	}
	if info.Mode().Perm()&writeBits != 0 {
		t.Fatalf("protectTree left the store's copy at mode %v, so this probe cannot answer #444 from here", info.Mode().Perm())
	}
	if f, err := os.OpenFile(storeCopy, os.O_WRONLY, 0); err == nil {
		f.Close()
		t.Fatalf("the store's copy accepted a write handle before any consumer link was retired, so this probe cannot answer #444 from here")
	}

	if err := os.Link(storeCopy, filepath.Join(consumerDir, probeFileName)); err != nil {
		t.Fatalf("hard link the store's copy into the consumer tree: %v", err)
	}

	return storeCopy, consumerDir
}

// requireStoreCopyStillProtected is the probe's verdict, and it is written to be
// read by someone looking at a red CI run who has never seen issue #444.
//
// retirement names how the consumer's link was retired, for the failure text.
//
// Two questions are asked, not one. The mode bits are what os.Stat reports, and
// on Windows that report is derived from FILE_ATTRIBUTE_READONLY
// (os/types_windows.go maps the attribute to 0444 and its absence to 0666), so
// it is a real signal there rather than a Unix-shaped approximation. An
// attempted write is asked as well because a mode is a report and an accepted
// write is the event #333 exists to prevent, and only the second one is true by
// observation rather than by the operating system's own summary.
func requireStoreCopyStillProtected(t *testing.T, storeCopy, retirement string) {
	t.Helper()

	info, err := os.Stat(storeCopy)
	if err != nil {
		t.Fatalf("the store's copy is gone after the consumer's link was retired with %s: %v", retirement, err)
	}
	perm := info.Mode().Perm()

	written := describeWriteAttempt(storeCopy)

	if perm&writeBits == 0 && written == "" {
		return
	}

	t.Fatalf(`ISSUE #444 REPRODUCED on %s: the store's write protection did not survive a consumer's unlink.

What ran: one file inside a store entry, write protected by the store's own
protectTree; one hard link to it in a directory standing in for a consumer's
.lnpm/{package} tree; that link retired with %s.

What the store's own surviving copy looks like afterwards:
  mode reported by os.Stat: %v (write bits %#o are what the protection strips)
  write attempted through it: %s

Expected, and what a PASS of this test means: no write bit and a refused write,
because the entry is still the canonical copy that consumers hard link to.

What a failure means: #333's write protection lasts only until the first
unlink of that package, so a consumer can then write through its hard link and
rewrite the store entry for that content hash. Every later install of that
version serves the tampered bytes. On Windows the mechanism #444 names is that
the protection is FILE_ATTRIBUTE_READONLY on the shared file, and the unlink
clears that attribute in order to make its own delete succeed.

This answers step 1 of #444 ("reproduce it") with yes. Do NOT fix it from this
test: #444 records two candidate fixes, calls both larger than #333's scope, and
picks neither.`, runtime.GOOS, retirement, perm, writeBits, written)
}

// describeWriteAttempt tries to write poisonContent over path and returns a
// description of what happened, or "" if the write was refused.
//
// It is a separate function so the refusal it is looking for is one value and
// not three booleans the caller has to combine, and so the caller's failure text
// can state the outcome in one line.
func describeWriteAttempt(path string) string {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		return ""
	}
	defer f.Close()

	n, writeErr := f.WriteString(poisonContent)
	if writeErr != nil {
		return fmt.Sprintf("opened for writing, but the write failed after %d bytes: %v", n, writeErr)
	}
	return fmt.Sprintf("ACCEPTED - %d bytes of %q are now the store's content", n, poisonContent)
}
