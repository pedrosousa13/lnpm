//go:build linux

// The tests here put stdout on a real terminal, which is the only way to show
// that NO_COLOR is what turns the markers into ASCII: every other capture in
// the suite uses a pipe, and the helpers already fall back to ASCII for any
// stdout that is not a terminal.
//
// They are linux-only because they name the terminal with the TIOCGPTN ioctl,
// which darwin spells differently and windows has no equivalent for. Making
// them portable would mean taking on a pty dependency. So on those platforms
// nothing shows directly that NO_COLOR is honored or that a terminal still
// gets the glyphs: only the pipe-based sweeps run there, and all they
// establish is that the markers come from the icon helpers.

package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/db"
	"github.com/pedrosousa13/lnpm/pkg/lockfile"
	"golang.org/x/sys/unix"
)

// TestRunDoctorOnATerminal pins both halves of the NO_COLOR contract from the
// only place they can be told apart: a real terminal.
//
// The tests that capture stdout through a pipe cannot show that NO_COLOR is
// what produced the ASCII, because the pipe alone already switches the icon
// helpers to ASCII. Here stdout is the slave side of a pseudo-terminal, so
// decoration is on unless NO_COLOR turns it off — which makes the first case
// the check that an interactive terminal still shows the glyphs, and the
// second the check that NO_COLOR alone takes them away.
func TestRunDoctorOnATerminal(t *testing.T) {
	t.Run("shows the glyphs", func(t *testing.T) {
		noColor(t, false)

		out := runDoctorOnTTY(t)

		for _, want := range []string{"✓ OK", "✓ All checks passed!"} {
			if !strings.Contains(out, want) {
				t.Errorf("Terminal output lost %q; output was:\n%s", want, out)
			}
		}
	})

	t.Run("NO_COLOR replaces them with ASCII", func(t *testing.T) {
		noColor(t, true)

		out := runDoctorOnTTY(t)

		if !strings.Contains(out, "OK All checks passed!") {
			t.Errorf("NO_COLOR output is missing the ASCII marker; output was:\n%s", out)
		}
		assertNoRawGlyphs(t, out)
	})
}

// TestRunRetreatOnATerminal is the same pair for retreat: its own markers on a
// real terminal, and what NO_COLOR does to them. Retreat needs a project
// to retreat from, so this builds the smallest one it will act on - a
// package.json, a lock file naming one package, and a .lnpm directory - rather
// than publishing and adding anything.
func TestRunRetreatOnATerminal(t *testing.T) {
	t.Run("shows the glyphs", func(t *testing.T) {
		noColor(t, false)

		out := runRetreatOnTTY(t)

		for _, want := range []string{"✓ Restored", "✓ Retreat complete!", "💡 Run 'npm install'"} {
			if !strings.Contains(out, want) {
				t.Errorf("Terminal output lost %q; output was:\n%s", want, out)
			}
		}
	})

	t.Run("NO_COLOR replaces them with ASCII", func(t *testing.T) {
		noColor(t, true)

		out := runRetreatOnTTY(t)

		if !strings.Contains(out, "OK Retreat complete!") {
			t.Errorf("NO_COLOR output is missing the ASCII marker; output was:\n%s", out)
		}
		assertNoRawGlyphs(t, out)
	})
}

// noColor sets or clears NO_COLOR for the duration of the test. Clearing it has
// to unset the variable rather than empty it, because the helpers treat any
// value, empty included, as a request for plain output.
func noColor(t *testing.T, on bool) {
	t.Helper()

	t.Setenv("NO_COLOR", "1") // registers the restore of whatever was there
	if !on {
		_ = os.Unsetenv("NO_COLOR")
	}
}

// runDoctorOnTTY runs RunDoctor against a healthy store with stdout on a
// terminal, and returns what it printed.
func runDoctorOnTTY(t *testing.T) string {
	t.Helper()

	dir := newDoctorStoreConfig(t)
	newDoctorStore(t, dir)

	db.ResetForTesting()
	t.Cleanup(db.ResetForTesting)

	return captureTTYStdout(t, func() {
		if err := RunDoctor(false); err != nil {
			t.Errorf("RunDoctor(false) = %v on a healthy store, want nil", err)
		}
	})
}

// runRetreatOnTTY runs RunRetreat against a throwaway project with stdout on a
// terminal, and returns what it printed.
//
// The store and config are redirected first: retreat opens the database, and
// without that it would open the real one.
func runRetreatOnTTY(t *testing.T) string {
	t.Helper()

	newDoctorStoreConfig(t)
	db.ResetForTesting()
	t.Cleanup(db.ResetForTesting)

	project := t.TempDir()
	writeFile(t, filepath.Join(project, "package.json"),
		`{"name":"tty-project","version":"1.0.0","dependencies":{"tty-pkg":"file:.lnpm/tty-pkg"}}`)
	if err := os.MkdirAll(filepath.Join(project, ".lnpm", "tty-pkg"), 0755); err != nil {
		t.Fatalf("create .lnpm: %v", err)
	}
	lock := &lockfile.LockFile{Version: 1, Packages: map[string]lockfile.Package{
		"tty-pkg": {Version: "1.0.0", OriginalVersion: "^1.0.0"},
	}}
	if err := lock.Save(project); err != nil {
		t.Fatalf("save lock file: %v", err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(project); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	return captureTTYStdout(t, func() {
		if err := RunRetreat(true, false); err != nil {
			t.Errorf("RunRetreat() = %v, want nil", err)
		}
	})
}

// captureTTYStdout captures fn with stdout on a terminal and stdin left alone,
// which is all the tests above need.
func captureTTYStdout(t *testing.T, fn func()) string {
	t.Helper()

	return captureTTY(t, "", fn)
}

// captureTTY runs fn with os.Stdout pointed at the slave side of a
// pseudo-terminal and returns what it printed. A pipe would not do: the icon
// helpers ask whether stdout is a character device, so only a terminal
// exercises their decorated branch, and confirm requires the same of stdout
// before it renders a prompt at all.
//
// A non-empty answers puts os.Stdin on that same slave and queues those lines
// on the master, so a prompt can be answered. One read of the slave returns one
// line, because the terminal is in its default canonical mode, so consecutive
// prompts each get the next answer rather than one prompt swallowing them all.
//
// TestRunGCPromptsReadOneAnswerEach pins both halves of that, and has to,
// because neither way of failing to deliver an answer announces itself: a
// starved terminal blocks the read, and a stdin that was never wired here
// reaches EOF instead, which confirm reports as a refusal. Its doc comment
// records the measurement for each.
//
// The reader runs while fn does, so fn can print more than the terminal buffer
// holds. It stops on a sentinel written after fn returns rather than on the
// slave being closed: closing the last slave makes the master report EIO, and
// anything still queued would be lost with it.
func captureTTY(t *testing.T, answers string, fn func()) string {
	t.Helper()

	const sentinel = "@@lnpm-tty-capture-end@@"

	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("No pseudo-terminal available: %v", err)
	}
	defer func() { _ = master.Close() }()

	n, err := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPTN)
	if err != nil {
		t.Skipf("Could not name the pseudo-terminal: %v", err)
	}
	if err := unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		t.Skipf("Could not unlock the pseudo-terminal: %v", err)
	}
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("Could not open the pseudo-terminal: %v", err)
	}
	defer func() { _ = slave.Close() }()

	done := make(chan string, 1)
	go func() {
		var buf []byte
		chunk := make([]byte, 1024)
		for {
			n, err := master.Read(chunk)
			buf = append(buf, chunk[:n]...)
			if err != nil || bytes.Contains(buf, []byte(sentinel)) {
				break
			}
		}
		done <- string(buf)
	}()

	origOut, origIn := os.Stdout, os.Stdin
	if answers != "" {
		if _, err := master.WriteString(answers); err != nil {
			t.Fatalf("Failed to queue the answers: %v", err)
		}
		os.Stdin = slave
	}
	os.Stdout = slave
	func() {
		defer func() { os.Stdout, os.Stdin = origOut, origIn }()
		fn()
	}()

	if _, err := slave.WriteString(sentinel + "\n"); err != nil {
		t.Fatalf("Failed to end the capture: %v", err)
	}

	out := <-done
	if i := strings.Index(out, sentinel); i >= 0 {
		out = out[:i]
	}
	return out
}
