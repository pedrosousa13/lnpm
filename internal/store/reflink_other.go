//go:build !darwin && !linux

package store

import "fmt"

// tryReflink is not supported on this platform
func tryReflink(src, dst string) bool {
	return false
}

// reflinkFile is not supported on this platform
func reflinkFile(src, dst string) error {
	return fmt.Errorf("reflink not supported on this platform")
}
