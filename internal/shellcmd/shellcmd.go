// Package shellcmd runs a command string through the platform shell.
//
// Command and QuoteArg are a pair: a caller builds a command string by pasting
// QuoteArg's output into it and hands the whole string to Command. #375 was
// that the two did not compose on Windows, so the pairing is now the package's
// stated contract rather than an implicit one.
//
// The Windows half is the reason Command is split across build-tagged files.
// exec.Command("cmd", "/C", s) leaves os/exec to build the child command line
// with syscall.EscapeArg, which quotes for CommandLineToArgvW - the C runtime's
// parser - and rewrites an inner double quote as \". cmd.exe implements a
// different parser and does not read \" as an escape: it takes the backslash
// literally and lets the quote close the quoted run. Microsoft's own cmd
// reference spells the rules cmd.exe does apply, and os/exec's Command doc
// names cmd.exe as one of the exceptions to its quoting, pointing at
// SysProcAttr.CmdLine for exactly this case. So shellcmd_windows.go sets
// CmdLine and shellcmd_unix.go does not, and SysProcAttr carries that field
// only on Windows.
//
// Known limitation, recorded rather than worked around: cmd.exe has no escape
// for a double quote inside a quoted string, so QuoteArg cannot deliver an
// argument containing one on Windows. Nothing in this repo hits that, because a
// double quote is one of the characters Microsoft's file-naming rules reserve
// in a Windows path component, and QuoteArg's only production caller quotes a
// path.
package shellcmd

import (
	"runtime"
	"strings"
)

// QuoteArg quotes a single argument for the platform shell used by Command.
func QuoteArg(arg string) string {
	if runtime.GOOS == "windows" {
		return quoteForCmd(arg)
	}
	return quoteForSh(arg)
}

// quoteForCmd and quoteForSh are split out of QuoteArg, and left free of any
// build tag, so a test on any platform compiles and exercises both. QuoteArg
// itself branches at run time, so neither is dead code on either GOOS.

// quoteForCmd quotes arg for cmd.exe. cmd.exe strips a surrounding pair of
// double quotes and reads what is between them literally; it has no escape for
// a double quote in that position, which is why arg passes through untouched.
func quoteForCmd(arg string) string {
	return `"` + arg + `"`
}

// quoteForSh quotes arg for a POSIX shell, which reads a single-quoted string
// literally and offers no escape inside it either - hence closing, escaping the
// quote outside, and reopening.
func quoteForSh(arg string) string {
	return "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
}
