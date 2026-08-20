//go:build !windows

package link

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// TestScopeVanished pins which read failures ListLinked is allowed to treat as
// a scope that has been unlinked from under it. Everything else must propagate:
// the race test can only ever exercise the disappearing case, so the cases that
// must NOT be skipped are pinned here.
func TestScopeVanished(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "not exist", err: os.ErrNotExist, want: true},
		{
			name: "not exist wrapped by ReadDir",
			err:  &os.PathError{Op: "open", Path: filepath.Join(".lnpm", "@org"), Err: syscall.ENOENT},
			want: true,
		},
		{
			name: "permission denied",
			err:  &os.PathError{Op: "open", Path: filepath.Join(".lnpm", "@org"), Err: syscall.EACCES},
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
