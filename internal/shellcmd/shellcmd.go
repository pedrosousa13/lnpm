package shellcmd

import (
	"os/exec"
	"runtime"
	"strings"
)

// Command returns an *exec.Cmd that runs cmdStr in the platform shell.
func Command(cmdStr string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.Command("cmd", "/C", cmdStr)
	}
	return exec.Command("sh", "-c", cmdStr)
}

// QuoteArg quotes a single argument for the platform shell used by Command.
func QuoteArg(arg string) string {
	if runtime.GOOS == "windows" {
		return `"` + strings.ReplaceAll(arg, `"`, `\"`) + `"`
	}
	return "'" + strings.ReplaceAll(arg, "'", "'\\''") + "'"
}
