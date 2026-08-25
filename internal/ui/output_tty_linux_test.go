//go:build linux

// The test here puts stdout on a real terminal, which is the only way to tell
// decorate's two conditions apart: a capture through a pipe makes
// IsTTY(os.Stdout) false on its own, so ASCII read out of one says nothing
// about NO_COLOR.
//
// It is linux-only because it names the terminal with the TIOCGPTN ioctl, which
// darwin spells differently and windows has no equivalent for. Making it
// portable would mean taking on a pty dependency.

package ui

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// TestIconsOnATerminal pins the markers themselves, so that a caller printing
// the right ASCII from a literal rather than from a helper still has something
// left to fail against.
//
// The NO_COLOR case is the one that has to run here rather than under a pipe:
// on a terminal the glyphs are what the helpers would return, so ASCII coming
// back is attributable to NO_COLOR and nothing else.
func TestIconsOnATerminal(t *testing.T) {
	t.Run("shows the glyphs", func(t *testing.T) {
		noColor(t, false)

		got := captureTTYStdout(t, printIcons)

		if want := "✓ ✗ ⚠ 💡"; !strings.Contains(got, want) {
			t.Errorf("Icons on a terminal = %q, want them to contain %q", got, want)
		}
	})

	t.Run("NO_COLOR replaces them with ASCII", func(t *testing.T) {
		noColor(t, true)

		got := captureTTYStdout(t, printIcons)

		if want := "OK x ! tip:"; !strings.Contains(got, want) {
			t.Errorf("Icons under NO_COLOR = %q, want them to contain %q", got, want)
		}
	})
}

// printIcons writes all four markers to stdout in a fixed order, so each case
// above asserts one line rather than four fragments.
func printIcons() {
	fmt.Printf("%s %s %s %s\n", IconOK(), IconFail(), IconWarn(), IconTip())
}

// noColor sets or clears NO_COLOR for the duration of the test. Clearing it has
// to unset the variable rather than empty it, because decorate treats any
// value, empty included, as a request for plain output.
func noColor(t *testing.T, on bool) {
	t.Helper()

	t.Setenv("NO_COLOR", "1") // registers the restore of whatever was there
	if !on {
		_ = os.Unsetenv("NO_COLOR")
	}
}

// captureTTYStdout runs fn with os.Stdout pointed at the slave side of a
// pseudo-terminal and returns what it printed. A pipe would not do: decorate
// asks whether stdout is a character device, so only a terminal exercises its
// decorated branch.
//
// The reader runs while fn does, so fn can print more than the terminal buffer
// holds. It stops on a sentinel written after fn returns rather than on the
// slave being closed: closing the last slave makes the master report EIO, and
// anything still queued would be lost with it.
//
// internal/cli has a longer relative of this, captureTTY, which also wires
// stdin so a prompt can be answered. Nothing here prompts, so this one leaves
// stdin alone.
func captureTTYStdout(t *testing.T, fn func()) string {
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

	origOut := os.Stdout
	os.Stdout = slave
	func() {
		defer func() { os.Stdout = origOut }()
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
