package shellcmd

import (
	"os/exec"
	"syscall"
)

// Command returns an *exec.Cmd that runs cmdStr in cmd.exe.
//
// CmdLine is set so that os/exec does not build the command line itself: read
// from syscall's exec_windows.go, StartProcess passes SysProcAttr.CmdLine to
// CreateProcessW as lpCommandLine when it is non-empty, and only otherwise
// calls makeCmdLine(argv), which is where syscall.EscapeArg would run and
// mangle cmdStr's quoting. Args is still what exec.Command set, because it is
// what resolves "cmd" to a path for lpApplicationName; StartProcess ignores it
// for the command line once CmdLine is non-empty.
//
// The program name is repeated as the first token because lpCommandLine is the
// whole command line, argv[0] included - the convention CreateProcessW's
// reference states, and the shape makeCmdLine would have produced, since it
// joins every element of argv starting at argv[0].
//
// Not verified on Windows locally: this environment cannot run cmd.exe, and
// cross-compiling proves nothing about escaping. The claims above are read from
// Go's syscall source and Microsoft's CreateProcessW and cmd references; the
// behaviour is asserted by TestCommandRunsAQuotedPath on Windows CI.
func Command(cmdStr string) *exec.Cmd {
	cmd := exec.Command("cmd", "/C", cmdStr)
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: "cmd /C " + cmdStr}
	return cmd
}
