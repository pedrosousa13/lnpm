//go:build !windows

package fsutil

import (
	"os"
	"syscall"
)

// DeviceID returns the device ID for the given file info.
// This is used to check if two paths are on the same filesystem.
func DeviceID(info os.FileInfo) uint64 {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(stat.Dev)
	}
	return 0
}
