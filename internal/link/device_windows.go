//go:build windows

package link

import (
	"os"
)

// getDeviceID returns the device ID for the given file info
// On Windows, we return 0 as we handle this differently (try hard link, fall back to copy)
func getDeviceID(info os.FileInfo) uint64 {
	// Windows doesn't expose device ID the same way
	// Hard links work within the same volume, which we detect by trying
	return 0
}
