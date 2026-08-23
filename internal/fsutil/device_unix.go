//go:build !windows

package fsutil

import (
	"os"
	"syscall"
)

// DeviceID returns the device ID for the given file info.
// This is used to check if two paths are on the same filesystem.
//
// This form cannot be implemented on Windows - see device_windows.go - so a
// caller that needs an answer on every platform wants DeviceIDOfPath instead.
func DeviceID(info os.FileInfo) uint64 {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(stat.Dev)
	}
	return 0
}

// DeviceIDOfPath returns the identifier of the filesystem the path is on, or 0
// if that cannot be established - because the path does not exist, cannot be
// read, or the platform does not report one.
//
// Zero is a real answer meaning "unknown", and every caller has to treat it as
// such rather than as a device that happens to compare unequal to others. gc
// depends on that distinction: an unknown device sends it back to judging a
// missing project directory the way it did before this existed.
//
// It follows symlinks, matching os.Stat and the paths the database stores, which
// are already run through filepath.EvalSymlinks by normalizePath.
func DeviceIDOfPath(path string) uint64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return DeviceID(info)
}
