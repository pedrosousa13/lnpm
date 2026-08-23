//go:build windows

package fsutil

import "testing"

// TestReadFileCappedRefusesANonRegularFile is built on Unix only. This file
// exists so the case shows up as skipped on Windows rather than silently
// absent from the package.
//
// The skip is a build constraint and not a statement about the guard: the Unix
// file makes its FIFO with syscall.Mkfifo, and syscall.Mkfifo is not declared
// on Windows, so that file cannot compile here. Whether the guard behaves the
// same way on Windows is untested either way.
func TestReadFileCappedRefusesANonRegularFile(t *testing.T) {
	t.Skip("syscall.Mkfifo is not declared on Windows, so the named-pipe fixture cannot be built here")
}
