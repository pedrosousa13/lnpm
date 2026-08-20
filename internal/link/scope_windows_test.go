//go:build windows

package link

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// errorAccessDenied is Win32 ERROR_ACCESS_DENIED, the neighbouring error that
// must keep propagating.
const errorAccessDenied = syscall.Errno(5)

// TestScopeVanished pins which read failures ListLinked is allowed to treat as
// a scope that has been unlinked from under it. Everything else must propagate:
// the race test can only ever exercise the disappearing cases, so the case that
// must NOT be skipped is pinned here.
//
// The access-denied case is the reason
// TestListLinkedToleratesAScopeRemovedMidListing skips on windows. Windows
// reports a delete-pending directory as ERROR_ACCESS_DENIED, so the race it
// drives produces the one error scopeVanished refuses to swallow. Swallowing it
// would make a scope directory the caller genuinely cannot read report as no
// packages, which is a worse trade than leaving the race unhandled here.
func TestScopeVanished(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "not exist", err: os.ErrNotExist, want: true},
		{
			name: "not exist wrapped by ReadDir",
			err:  &os.PathError{Op: "open", Path: filepath.Join(".lnpm", "@org"), Err: syscall.ERROR_FILE_NOT_FOUND},
			want: true,
		},
		{name: "sharing violation", err: errorSharingViolation, want: true},
		{
			name: "sharing violation wrapped by ReadDir",
			err:  &os.PathError{Op: "open", Path: filepath.Join(".lnpm", "@org"), Err: errorSharingViolation},
			want: true,
		},
		{
			name: "access denied",
			err:  &os.PathError{Op: "open", Path: filepath.Join(".lnpm", "@org"), Err: errorAccessDenied},
			want: false,
		},
		{name: "no error", err: nil, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := scopeVanished(tt.err); got != tt.want {
				t.Errorf("scopeVanished(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
