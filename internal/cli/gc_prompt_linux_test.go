//go:build linux

// gc's confirmations are only ever printed to an interactive terminal -
// confirm requires stdin and stdout to be character devices before it will
// render a prompt - so the wording can only be read back through the
// pseudo-terminal harness in output_tty_linux_test.go. It is stdout that the
// rest of the gc tests fail on: they capture through a pipe, which is not a
// character device.
//
// That is what makes this file linux-only, for the reason output_tty_linux_test
// gives: it names the terminal with an ioctl darwin spells differently and
// windows has no equivalent for.

package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/db"
)

// TestRunGCOrphanedLinkPromptSaysWhatDecliningDoes pins the sentence #362 added
// to the orphaned-link confirmation, and pins it against the run it describes
// rather than against itself.
//
// The complaint was one of reading. Declining looks like "leave this alone",
// and it is not: the arithmetic that decides which versions are collectable ran
// before the question and counted the link as orphaned either way, so saying no
// keeps the records and nothing else. The wording is the whole fix - no
// behaviour changed - which is why an exact-string assertion is worth having
// here at all.
//
// The fixture is one package whose only link is orphaned, so declining leaves
// it with nothing protecting it and the run reports it for collection
// immediately below. That is one case of the sentence, not its whole claim: a
// version keeping a live link, or one a tag other than latest names, is listed
// here and is not collectable, which is why the sentence says "by itself"
// rather than promising collection.
func TestRunGCOrphanedLinkPromptSaysWhatDecliningDoes(t *testing.T) {
	storeRoot, database := newGCStore(t)
	seedRemovableOrphanedLink(t, database, storeRoot, "linked-pkg")

	// One "n" per confirmation: the orphaned link, then the package. The
	// temp-directory sweep finds nothing here and so never asks a third.
	out := captureTTY(t, "n\nn\n", func() {
		if err := RunGC(false, "", true, false); err != nil {
			t.Fatalf("RunGC() error = %v", err)
		}
	})

	const want = "Permanently delete these orphaned link(s)? Declining keeps the records, but does not by itself protect the version(s) they name from collection. [y/N]: "
	if !strings.Contains(out, want) {
		t.Errorf("gc did not ask %q; output was:\n%s", want, out)
	}

	// What the sentence claims, in the same run that printed it.
	if !strings.Contains(out, "Skipped deleting orphaned links.") {
		t.Errorf("gc did not treat the answer as a refusal, output was:\n%s", out)
	}
	if got := remainingLinks(t); got != 1 {
		t.Errorf("declining did not keep the records, %d link(s) left, want 1", got)
	}
	if !strings.Contains(out, "Found 1 orphaned package(s)") {
		t.Errorf("declining protected the version the link named from collection, output was:\n%s", out)
	}
}

// TestRunGCPromptsReadOneAnswerEach is the only test here that can tell an
// answer the terminal delivered from one it never delivered.
//
// Answering "no" proves nothing on its own, because confirm reports a failed
// read as a refusal. Measured, by deleting the line that puts os.Stdin on the
// terminal: confirm renders the prompt anyway - its interactivity test is a
// character-device test, and the /dev/null go test hands the binary as stdin
// is a character device - then reads EOF and returns false, which RunGC
// reports with the same "Skipped deleting orphaned links." line a real "n"
// produces. TestRunGCOrphanedLinkPromptSaysWhatDecliningDoes passes in full
// through a run that answered nothing at all. This test is the one that goes
// red there, on the "y".
//
// Answering both questions differently is also what pins captureTTY's
// canonical-mode claim, that one read takes one line. Measured, by truncating
// the queued answers to their first line: the second confirm blocks on the
// terminal and the run dies on the -timeout alarm rather than failing an
// assertion, which is a red this test reaches only as a deadlock.
//
// It seeds its own store because it deletes from it.
func TestRunGCPromptsReadOneAnswerEach(t *testing.T) {
	storeRoot, database := newGCStore(t)
	seedRemovableOrphanedLink(t, database, storeRoot, "linked-pkg")

	// The entry is read back from the record the helper wrote rather than
	// spelled out here again, so the hash cannot drift out of step with it.
	//
	// It is also stat'd before the run, which is not redundant with the check
	// after: a path that never existed satisfies os.IsNotExist exactly as well
	// as one gc deleted, so "gone afterwards" establishes nothing until "there
	// beforehand" has been established. Deriving the path narrows the ways that
	// can happen but does not close them - a wrong derivation is still a path
	// that was never there - so both checks are kept.
	packages, err := database.ListPackages()
	if err != nil || len(packages) != 1 {
		t.Fatalf("list packages: %v (%d found)", err, len(packages))
	}
	entry := packages[0].StorePath
	if _, err := os.Stat(entry); err != nil {
		t.Fatalf("os.Stat(%s) = %v before the run, want the seeded store entry", entry, err)
	}

	// "no" to the links, "yes" to the packages: two different answers, so
	// neither can stand in for the other.
	out := captureTTY(t, "n\ny\n", func() {
		if err := RunGC(false, "", true, false); err != nil {
			t.Fatalf("RunGC() error = %v", err)
		}
	})

	if !strings.Contains(out, "Skipped deleting orphaned links.") {
		t.Errorf("the first question did not get its \"n\", output was:\n%s", out)
	}
	if !strings.Contains(out, "Removed 1 package(s)") {
		t.Errorf("the second question did not get its \"y\", output was:\n%s", out)
	}
	if _, err := os.Stat(entry); !os.IsNotExist(err) {
		t.Errorf("os.Stat(%s) = %v, want the store entry to be gone", entry, err)
	}
	database, err = db.GetDB()
	if err != nil {
		t.Fatalf("reopen database: %v", err)
	}
	packages, err = database.ListPackages()
	if err != nil {
		t.Fatalf("list packages: %v", err)
	}
	if len(packages) != 0 {
		t.Errorf("gc left %d package(s) recorded, want 0", len(packages))
	}
}
