//go:build !windows

package link

import "os"

// scopeVanished reports whether an error from reading a scope directory means
// the scope is gone rather than something the caller needs to hear about.
//
// Unlink removes a scope directory the moment it empties, so a listing that has
// already seen the scope in its parent can find it missing a moment later. On
// Unix that always surfaces as ENOENT; the Windows build has a second case.
func scopeVanished(err error) bool {
	return os.IsNotExist(err)
}
