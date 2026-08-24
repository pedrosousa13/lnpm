//go:build windows

package shellcmd

import (
	"os/exec"
	"syscall"
)

// Command returns an *exec.Cmd that runs cmdStr in cmd.exe.
//
// CmdLine is set so that the command line is not built from Args: read from
// syscall's exec_windows.go, StartProcess passes SysProcAttr.CmdLine to
// CreateProcessW as lpCommandLine when it is non-empty, and only otherwise
// calls makeCmdLine(argv) - which is where appendEscapeArg would run and mangle
// cmdStr's quoting.
//
// The program name is repeated as the first token because lpCommandLine is the
// whole command line, argv[0] included: the convention CreateProcessW's
// reference states, and the shape makeCmdLine would have produced, since it
// joins every element of argv starting at argv[0].
//
// exec.Command still builds the Cmd, for c.Path rather than for Args. LookPath
// resolves "cmd" into c.Path, and Start passes a value derived from c.Path -
// not from Args - as StartProcess's argv0, which becomes lpApplicationName.
// Args feeds only c.argv(), whose consumer here is the makeCmdLine call that a
// non-empty CmdLine skips, so it is unused on this path; os/exec's own doc
// suggests leaving Args empty for exactly this case. It is left as exec.Command
// set it because Cmd.String() reads it and clearing it would buy nothing.
//
// Measured on Windows CI, run 32767343693: TestCommandRunsAQuotedPath's
// a_space, a_single_quote and an_ampersand rows PASS there rather than skip,
// and TestPublishDryRunRunsPrePublishButNotPostPublish - the test that failed
// on Windows in run 32634796756 before this fix - passes too. Nothing here was
// executed locally: this environment cannot run cmd.exe, and the reasoning
// above is read from Go's syscall source and Microsoft's CreateProcessW and cmd
// references.
func Command(cmdStr string) *exec.Cmd {
	cmd := exec.Command("cmd", "/C", cmdStr)
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: "cmd /C " + cmdStr}
	return cmd
}
