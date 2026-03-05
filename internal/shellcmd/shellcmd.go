package shellcmd

import (
	"os/exec"
	"runtime"
)

// Command returns an *exec.Cmd that runs cmdStr in the platform shell.
func Command(cmdStr string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.Command("cmd", "/C", cmdStr)
	}
	return exec.Command("sh", "-c", cmdStr)
}
