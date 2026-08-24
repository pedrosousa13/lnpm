//go:build !windows

package shellcmd

import "os/exec"

// Command returns an *exec.Cmd that runs cmdStr in sh.
//
// Nothing re-escapes cmdStr on the way: execve takes argv as a list, so the
// string sh receives is the one built here. That is the property the Windows
// file has to reconstruct by hand.
func Command(cmdStr string) *exec.Cmd {
	return exec.Command("sh", "-c", cmdStr)
}
