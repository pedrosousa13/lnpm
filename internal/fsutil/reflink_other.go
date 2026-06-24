//go:build !darwin && !linux

package fsutil

import "fmt"

// tryReflink is not supported on this platform.
func tryReflink(src, dst string) bool {
	return false
}

// Reflink is not supported on this platform.
func Reflink(src, dst string) error {
	return fmt.Errorf("reflink not supported on this platform")
}
