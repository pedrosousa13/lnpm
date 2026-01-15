//go:build windows

package store

import (
	"os"
)

// getDeviceID returns the device ID for the given file info (Windows)
// Windows doesn't expose device IDs the same way, so we return 0
// and let hard link attempts fail naturally
func getDeviceID(info os.FileInfo) uint64 {
	return 0
}
