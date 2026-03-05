//go:build !windows

package link

import "os"

// createDirSymlink creates a directory symlink (regular symlink on Unix)
func createDirSymlink(target, linkPath string) error {
	return os.Symlink(target, linkPath)
}
