//go:build windows

package fsutil

import "os"

// DeviceID returns the device ID for the given file info.
// On Windows, device IDs aren't exposed the same way; return 0 and let hard
// link attempts fail naturally (falling back to copy).
func DeviceID(info os.FileInfo) uint64 {
	return 0
}
