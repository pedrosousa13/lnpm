package shellcmd

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The tests here are written so that the interesting case runs on every
// platform rather than only on the one the bug was reported from. quoteForSh
// and quoteForCmd are pure functions in a file with no build tag, so both are
// compiled and exercised everywhere; only Command is split by GOOS, and the
// execution test below asserts whichever shell the running platform has.

// A POSIX shell reads a single-quoted string literally, so quoting is
// wrap-in-quotes plus the standard close-escape-reopen dance for an embedded
// quote.
func TestQuoteForShQuotesForAPosixShell(t *testing.T) {
	cases := []struct {
		name string
		arg  string
		want string
	}{
		{"a plain path", "/tmp/a/b.txt", `'/tmp/a/b.txt'`},
		{"a space", "/tmp/a b/c.txt", `'/tmp/a b/c.txt'`},
		{"a single quote", "/tmp/it's/c.txt", `'/tmp/it'\''s/c.txt'`},
		{"a double quote", `/tmp/a"b/c.txt`, `'/tmp/a"b/c.txt'`},
		{"an ampersand", "/tmp/a&b/c.txt", `'/tmp/a&b/c.txt'`},
		{"empty", "", `''`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := quoteForSh(tc.arg); got != tc.want {
				t.Errorf("quoteForSh(%q) = %q, want %q", tc.arg, got, tc.want)
			}
		})
	}
}

// cmd.exe quotes with a bare pair of double quotes and has no escape for a
// double quote inside them. In particular it does not implement the C runtime's
// \" escape, which is what os/exec's syscall.EscapeArg emits and what this
// helper used to emit as well - see #375 and the package comment.
func TestQuoteForCmdQuotesForCmdExe(t *testing.T) {
	cases := []struct {
		name string
		arg  string
		want string
	}{
		{"a plain path", `C:\tmp\a\b.txt`, `"C:\tmp\a\b.txt"`},
		{"a space", `C:\tmp\a b\c.txt`, `"C:\tmp\a b\c.txt"`},
		{"a single quote", `C:\tmp\it's\c.txt`, `"C:\tmp\it's\c.txt"`},
		{"an ampersand", `C:\tmp\a&b\c.txt`, `"C:\tmp\a&b\c.txt"`},
		// A double quote is a reserved character in a Windows path component,
		// so no path reaches this. The row pins that the backslash escape is
		// gone rather than claiming the result is usable.
		{"a double quote", `a"b`, `"a"b"`},
		{"empty", "", `""`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := quoteForCmd(tc.arg)
			if got != tc.want {
				t.Errorf("quoteForCmd(%q) = %q, want %q", tc.arg, got, tc.want)
			}
			if strings.Contains(got, `\"`) {
				t.Errorf("quoteForCmd(%q) = %q, which contains the C runtime escape \\\" that cmd.exe does not implement", tc.arg, got)
			}
		})
	}
}

// QuoteArg has to pick the quoting for the shell Command will actually start.
func TestQuoteArgMatchesTheShellCommandStarts(t *testing.T) {
	const arg = "a b/c'd"
	want := quoteForSh(arg)
	if runtime.GOOS == "windows" {
		want = quoteForCmd(arg)
	}
	if got := QuoteArg(arg); got != want {
		t.Errorf("QuoteArg(%q) = %q, want %q on GOOS=%s", arg, got, want, runtime.GOOS)
	}
}

// The composition the package exists for and the one #375 broke: a caller
// pastes QuoteArg's output into a command string and hands the whole string to
// Command. Every row deliberately puts the marker inside a directory whose name
// contains a space - t.TempDir alone does not supply one on CI - so the row
// fails if the quoting does not survive the trip to the shell.
func TestCommandRunsAQuotedPath(t *testing.T) {
	cases := []struct {
		name           string
		dirName        string
		legalOnWindows bool
	}{
		{"a space", "dir with space", true},
		{"a single quote", "dir with 'quote' and space", true},
		{"an ampersand", "dir with & and space", true},
		{"a double quote", `dir with "quote" and space`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if runtime.GOOS == "windows" && !tc.legalOnWindows {
				t.Skipf(`SKIPPED ON WINDOWS: a directory cannot be named %q there, because Microsoft's file-naming rules reserve < > : " / \ | ? * in a path component, so this row can only ever exercise the sh half of the package`, tc.dirName)
			}

			dir := filepath.Join(t.TempDir(), tc.dirName)
			if err := os.Mkdir(dir, 0o755); err != nil {
				t.Fatalf("creating the fixture directory: %v", err)
			}
			marker := filepath.Join(dir, "marker.txt")

			cmdStr := "echo ran > " + QuoteArg(marker)
			out, err := Command(cmdStr).CombinedOutput()
			if err != nil {
				t.Fatalf("running %q failed: %v\nshell output: %s", cmdStr, err, out)
			}

			got, err := os.ReadFile(marker)
			if err != nil {
				t.Fatalf("the quoted path did not survive %q: %v\nshell output: %s", cmdStr, err, out)
			}
			// cmd.exe's echo keeps the space before the redirection operator
			// and ends the line with CRLF, so compare the trimmed text.
			if strings.TrimSpace(string(got)) != "ran" {
				t.Errorf("marker holds %q, want %q after trimming", string(got), "ran")
			}
		})
	}
}

// The three callers that pass a command string with no quoted argument at all
// still get a shell that runs it. Setting SysProcAttr.CmdLine on Windows
// replaces the whole command line os/exec would have built, so a plain string
// is the case that would break first if the replacement were malformed.
func TestCommandRunsAPlainCommandString(t *testing.T) {
	out, err := Command("echo hello").Output()
	if err != nil {
		t.Fatalf("running \"echo hello\" failed: %v", err)
	}
	if strings.TrimSpace(string(out)) != "hello" {
		t.Errorf("stdout = %q, want %q after trimming", string(out), "hello")
	}
}

// A failing command reports its exit status rather than being swallowed, which
// is what every caller turns into its own error message.
func TestCommandReportsANonZeroExit(t *testing.T) {
	if err := Command("exit 3").Run(); err == nil {
		t.Fatal("expected an error from a command exiting 3, got nil")
	}
}
