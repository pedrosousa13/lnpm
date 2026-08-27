package store

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Issue #444's tests are split over three files. This one holds the parts that
// are platform independent, so the Windows tests and the Unix control measure
// the same thing by construction rather than through two similar-looking
// copies. protect_unlink_windows_test.go holds the regression guard and the
// characterization of the Windows hazard; protect_unlink_unix_test.go holds the
// control. Each header says what a result on that platform does and does not
// settle.
//
// The shape being exercised is the one #333's protection assumes. The store's
// canonical copy of a file and a consumer's hard link to it are one file under
// two names, and lnpm protects that file by stripping the write bits from the
// store's name (protectTree, called by Store.Store). #444 asked whether
// retiring the consumer's name puts write access back on Windows, where the
// protection is an attribute on the shared file rather than a permission on one
// name.
//
// That question is now answered, and the answer splits by which call retires the
// link. Measured on Windows CI run 33066579971, head SHA 7b59495, job
// "Test (Windows)", both rows read from the log as `=== RUN` and neither
// skipped:
//
//   - os.Remove on the link reproduces it. The store's surviving copy came back
//     `-rw-rw-rw-` and accepted a 29-byte write.
//   - os.RemoveAll on the tree holding the link does not. The protection held.
//
// The same run's Linux and macOS jobs passed, and the Unix control passes here.
//
// What the product does with that is the subject of the audit comment in
// protect_unlink_windows_test.go: the retirement paths lnpm actually uses are
// os.RemoveAll, so the hazard is latent rather than live.
//
// Nothing in these files is a fix. #444 records two candidate fixes, calls both
// larger than #333's scope, and picks neither.

// probeFileName is the one file in the fixture's store entry.
//
// It is deliberately not markerName: protectTree exempts the completeness
// marker at an entry root, so a fixture using that name would start unprotected
// and its assertions would then be about nothing.
const probeFileName = "index.js"

// probeContent is what the fixture's file holds. Its only requirement is that a
// successful write can be told from it, which poisonContent below supplies.
const probeContent = "module.exports = 1;\n"

// poisonContent is written through the store's surviving copy when the write is
// accepted after the retirement. It is written rather than merely attempted so
// that a result reports a completed poisoning rather than an opened handle —
// which is what the Windows run above was able to state.
const poisonContent = "module.exports = 'poisoned';\n"

// newUnlinkProbe builds a store entry holding one protected file, plus a hard
// link to that file inside a separate directory standing in for a consumer's
// .lnpm/{package} tree. It returns the store's path to the file and the
// consumer directory holding the link.
//
// The protection is applied by calling protectTree, which is the store's own
// code and the same call Store.Store makes over a finished entry
// (internal/store/store.go). Chmodding the file here instead would exercise the
// operating system; going through protectTree exercises lnpm.
//
// Every failure below is a t.Fatalf rather than a t.Skip. A skipped test and a
// passing one are indistinguishable in CI output, and these tests exist to
// produce measurements, so an environment that cannot build the fixture has to
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

	// The fixture has to start protected, or every verdict below means something
	// other than what it says — the guard would pass for the wrong reason and the
	// characterization would pass without the retirement having done anything.
	info, err := os.Stat(storeCopy)
	if err != nil {
		t.Fatalf("stat the store's copy: %v", err)
	}
	if info.Mode().Perm()&writeBits != 0 {
		t.Fatalf("protectTree left the store's copy at mode %v, so nothing in this file can be measured from here", info.Mode().Perm())
	}
	if f, err := os.OpenFile(storeCopy, os.O_WRONLY, 0); err == nil {
		f.Close()
		t.Fatalf("the store's copy accepted a write handle before any consumer link was retired, so nothing in this file can be measured from here")
	}

	if err := os.Link(storeCopy, filepath.Join(consumerDir, probeFileName)); err != nil {
		t.Fatalf("hard link the store's copy into the consumer tree: %v", err)
	}

	return storeCopy, consumerDir
}

// requireStoreCopyStillProtected is the verdict for the regression guards, and
// it is written to be read by someone looking at a red CI run who has never seen
// issue #444.
//
// retirement names how the consumer's link was retired, for the failure text.
//
// Two questions are asked, not one. The mode bits are what os.Stat reports, and
// on Windows that report is derived from FILE_ATTRIBUTE_READONLY
// (os/types_windows.go maps the attribute to 0444 and its absence to 0666), so
// it is a real signal there rather than a Unix-shaped approximation — the
// Windows run above returned `-rw-rw-rw-` through exactly that mapping. An
// attempted write is asked as well because a mode is a report and an accepted
// write is the event #333 exists to prevent, and only the second is true by
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

	t.Fatalf(`STORE PROTECTION LOST on %s: retiring a consumer's hard link left the store's copy writable.

What ran: one file inside a store entry, write protected by the store's own
protectTree; one hard link to it in a directory standing in for a consumer's
.lnpm/{package} tree; that link retired with %s.

What the store's own surviving copy looks like afterwards:
  mode reported by os.Stat: %v (write bits %#o are what the protection strips)
  write attempted through it: %s

Expected, and what a PASS of this test means: no write bit and a refused write,
because the entry is still the canonical copy every consumer hard links to.

What this failure means: #333's write protection now lasts only until the first
retirement of that package, so a consumer can write through its hard link and
rewrite the store entry for that content hash. Every later install of that
version serves the tampered bytes.

This is the retirement call lnpm's own unlink and relink paths use, so a red
here is a live defect and not a hypothetical. On Windows it means issue #444's
mechanism — the protection is FILE_ATTRIBUTE_READONLY on the shared file, and a
delete that clears the attribute to make itself succeed never puts it back — has
reached a path the product takes. Read #444 before choosing a fix: it records
two candidates, calls both larger than #333's scope, and picks neither.`,
		runtime.GOOS, retirement, perm, writeBits, written)
}

// describeWriteAttempt tries to write poisonContent over path and returns a
// description of what happened, or "" if the write was refused.
//
// It is a separate function so that a refusal is one value rather than three
// booleans each caller has to recombine, and so both verdicts in this package
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
