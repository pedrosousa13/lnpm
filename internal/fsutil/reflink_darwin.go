//go:build darwin

package fsutil

import (
	"fmt"

	"golang.org/x/sys/unix"

	"github.com/pedrosousa13/lnpm/internal/debug"
)

// tryReflink attempts to create a copy-on-write clone on APFS.
// Returns true if successful, false if not supported.
func tryReflink(src, dst string) bool {
	// clonefile(const char *src, const char *dst, int flags)
	// https://www.manpagez.com/man/2/clonefile/
	if err := unix.Clonefile(src, dst, 0); err != nil {
		debug.Logf("reflink: clonefile failed: %v", err)
		return false
	}

	return true
}

// Reflink creates a copy-on-write clone of src at dst, returning an error if
// reflinks are not supported (the caller should then fall back to copy).
func Reflink(src, dst string) error {
	if tryReflink(src, dst) {
		return nil
	}
	return fmt.Errorf("reflink not supported")
}
