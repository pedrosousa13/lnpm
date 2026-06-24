//go:build linux

package fsutil

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// FICLONE ioctl for copy-on-write cloning on Btrfs, XFS, OCFS2
const FICLONE = 0x40049409

// tryReflink attempts to create a copy-on-write clone using the FICLONE ioctl.
// Returns true if successful, false if not supported.
func tryReflink(src, dst string) bool {
	srcFile, err := os.Open(src)
	if err != nil {
		return false
	}
	defer srcFile.Close()

	// Get source file info to set proper mode
	srcInfo, err := srcFile.Stat()
	if err != nil {
		return false
	}

	// Create destination file
	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, srcInfo.Mode())
	if err != nil {
		return false
	}
	defer dstFile.Close()

	// Try FICLONE ioctl
	srcFd := srcFile.Fd()
	dstFd := dstFile.Fd()

	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		uintptr(dstFd),
		uintptr(FICLONE),
		uintptr(unsafe.Pointer(&srcFd)),
	)

	if errno == 0 {
		if err := dstFile.Sync(); err != nil {
			return false
		}
		return true
	}

	// Clean up on failure
	dstFile.Close()
	_ = os.Remove(dst)
	return false
}

// Reflink creates a copy-on-write clone of src at dst, returning an error if
// reflinks are not supported (the caller should then fall back to copy).
func Reflink(src, dst string) error {
	if tryReflink(src, dst) {
		return nil
	}
	return fmt.Errorf("reflink not supported")
}
