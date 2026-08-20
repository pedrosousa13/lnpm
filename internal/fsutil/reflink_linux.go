//go:build linux

package fsutil

import (
	"fmt"
	"os"
	"syscall"
)

// FICLONE ioctl for copy-on-write cloning on Btrfs, XFS, OCFS2
const FICLONE = 0x40049409

// ficlone issues the FICLONE ioctl. The third ioctl arg is the SOURCE fd passed
// BY VALUE (ioctl(dest_fd, FICLONE, src_fd)) — not a pointer to it.
//
// It is a variable so that tests can drive the steps that run after a
// successful clone. A filesystem either supports cloning or it does not, so on
// one that does not — ext4, the usual dev and CI host — the chmod and Sync
// steps below are unreachable, and with them the cleanup of a clone that was
// made and then had to be disowned. The ioctl-failure branch needs no stub: on
// such a filesystem it is the branch that always runs.
var ficlone = func(dstFd, srcFd uintptr) syscall.Errno {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, dstFd, uintptr(FICLONE), srcFd)
	return errno
}

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
	// From here on the destination exists on disk, so every failure below owns
	// it. One deferred cleanup serves them all: it closes dstFile exactly once,
	// on every return path, and unlinks the destination unless the clone ran to
	// completion. Leaving a half-made destination behind would break the
	// caller's fallback, which reacts to a false return by trying os.Link at
	// the same path (EEXIST) or a copy (silently overwriting the remains).
	success := false
	defer func() {
		_ = dstFile.Close()
		if !success {
			_ = os.Remove(dst)
		}
	}()

	if errno := ficlone(dstFile.Fd(), srcFile.Fd()); errno != 0 {
		return false
	}

	// FICLONE clones data blocks only, never permissions, and the destination
	// was created with a mode the umask masked. Chmod is not masked, so the
	// clone ends up with the source's exact bits. If it fails, the cleanup
	// makes the caller fall back to a copy rather than keeping a clone with the
	// wrong mode.
	if err := dstFile.Chmod(srcInfo.Mode()); err != nil {
		return false
	}
	if err := dstFile.Sync(); err != nil {
		return false
	}

	success = true
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
