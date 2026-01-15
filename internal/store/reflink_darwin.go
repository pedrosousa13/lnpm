//go:build darwin

package store

import (
	"fmt"
	"syscall"
	"unsafe"

	"github.com/pedrosousa13/lnpm/internal/debug"
)

// SYS_CLONEFILE syscall number for macOS
const SYS_CLONEFILE = 462

// tryReflink attempts to create a copy-on-write clone on APFS
// Returns true if successful, false if not supported
func tryReflink(src, dst string) bool {
	// clonefile syscall on macOS

	srcPtr, err := syscall.BytePtrFromString(src)
	if err != nil {
		debug.Logf("reflink: BytePtrFromString(src) failed: %v", err)
		return false
	}

	dstPtr, err := syscall.BytePtrFromString(dst)
	if err != nil {
		debug.Logf("reflink: BytePtrFromString(dst) failed: %v", err)
		return false
	}

	// clonefile(const char *src, const char *dst, int flags)
	_, _, errno := syscall.Syscall(
		uintptr(SYS_CLONEFILE),
		uintptr(unsafe.Pointer(srcPtr)),
		uintptr(unsafe.Pointer(dstPtr)),
		uintptr(0), // flags = 0 for default behavior
	)

	if errno != 0 {
		debug.Logf("reflink: clonefile failed: %v (errno %d)", errno, errno)
		return false
	}

	return true
}

// reflinkFile attempts to create a reflink copy
func reflinkFile(src, dst string) error {
	if tryReflink(src, dst) {
		return nil
	}
	return fmt.Errorf("reflink not supported")
}
