//go:build windows

package link

import (
	"errors"
	"os"
	"syscall"
)

// errorSharingViolation is Win32 ERROR_SHARING_VIOLATION. Windows reports it
// when an open is refused because another handle holds the file or directory
// under sharing restrictions, which includes a handle opened in order to delete
// it.
const errorSharingViolation = syscall.Errno(32)

// scopeVanished reports whether an error from reading a scope directory means
// the scope is gone rather than something the caller needs to hear about.
//
// Unlink removes a scope directory the moment it empties, so a listing that has
// already seen the scope in its parent can find it missing a moment later. Unix
// reports that as ENOENT, but Windows has two ways to say it: a delete that has
// completed gives ENOENT, while one still in flight gives a sharing violation,
// because the directory survives its own delete until the last handle closes
// and refuses new opens in the meantime. Both mean the same thing here — the
// scope is on its way out and holds nothing worth listing.
//
// ERROR_ACCESS_DENIED is deliberately not folded in. It is a genuine permission
// failure, not a disappearing scope, and skipping it would hide a project the
// caller cannot read behind an empty listing.
func scopeVanished(err error) bool {
	return os.IsNotExist(err) || errors.Is(err, errorSharingViolation)
}
