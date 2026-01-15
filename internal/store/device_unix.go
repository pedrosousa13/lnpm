//go:build !windows

package store

import (
	"os"
	"syscall"
)

// getDeviceID returns the device ID for the given file info
// This is used to check if two paths are on the same filesystem
func getDeviceID(info os.FileInfo) uint64 {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(stat.Dev)
	}
	return 0
}
