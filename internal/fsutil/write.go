package fsutil

import (
	"fmt"
	"os"
	"path/filepath"
)

// WriteFileAtomic writes data to path through a staging file in the same
// directory and a rename onto path.
//
// The indirection is what makes a failed write harmless. A direct os.WriteFile
// opens the destination O_TRUNC, so a crash, a full disk or a SIGKILL between
// the open and the last byte leaves a truncated or empty file where the original
// was. The rename is the only step that touches path, and it either happens or
// does not.
//
// Four details are what make the rename safe to substitute for the open, and
// none of them are optional:
//
// A rename hands the destination the staging file's own mode, so the mode of the
// file being replaced has to be carried over deliberately. defaultMode is used
// only when there is nothing at path to take a mode from.
//
// A rename also replaces the destination whatever its mode, so a file the owner
// marked read-only has to be refused here explicitly. That is the ordinary case
// - `chmod -w package.json`, or a tool that did it - and it is the case the
// guard is for.
//
// The guard is a mode check and deliberately not a reproduction of the kernel's
// permission check, which also depends on the file's ownership and the process's
// effective uid. It therefore diverges in both directions, and neither is a bug
// to be fixed here:
//
//   - Weaker: another user's 0644 file in a directory we can write. os.WriteFile
//     would get EACCES from the open; 0200 is set, so the guard passes and the
//     rename succeeds. Staging plus rename genuinely can replace a file the
//     opening write could not, because the permission that matters is the
//     directory's.
//   - Stricter: running as root against a 0444 file. os.WriteFile succeeds,
//     since root ignores the mode; the guard refuses. write_test.go skips its
//     directory-permission case on os.Geteuid() == 0 for the same reason.
//
// Reproducing the kernel's answer would mean stat-ing owner and group and
// comparing against the effective uid and supplementary groups, which is racy
// against a concurrent chmod and still wrong on filesystems carrying ACLs. The
// mode check is the cheap approximation that catches what users actually do.
//
// The Chmod is explicit rather than left to the process umask, because
// os.CreateTemp makes its file 0600, which is not a mode any of these files has
// ever had. Chmod is not masked, so the result does not depend on the umask the
// caller happened to run under.
//
// The staging file is closed before the rename. On Windows that ordering is
// load-bearing rather than tidy: renaming a file requires DELETE access to it,
// and syscall.Open - the path os.CreateTemp takes - opens with a share mode of
// FILE_SHARE_READ|FILE_SHARE_WRITE and no FILE_SHARE_DELETE
// (go1.26.7 src/syscall/syscall_windows.go:32), so a rename attempted while the
// handle is still open is refused with a sharing violation. Read from the Go
// source, not run: this repo's Windows evidence here is cross-compilation only.
//
// Two behaviours differ from a direct write. Both were already true of
// pkg/lockfile, which now calls this, and of internal/gitignore, which still
// carries its own hand-rolled copy of the sequence rather than calling anything
// (see gitignore.go:59 and :103 - migrating it is out of scope). Neither is
// changed here:
//
// Staging needs a new entry in path's directory, so a writable file inside a
// read-only directory can no longer be rewritten.
//
// A symlink at path is replaced rather than followed. os.Stat follows the link,
// so the mode carried over is the target's, but the rename puts a regular file
// where the symlink was and the indirection is gone. A caller that must write
// through a symlink has to resolve it with filepath.EvalSymlinks first.
func WriteFileAtomic(path string, data []byte, defaultMode os.FileMode) error {
	mode := defaultMode
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
		if mode&0200 == 0 {
			return fmt.Errorf("failed to write %s: it is read-only (mode %o)", path, mode)
		}
	}

	staged, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}

	// Cleared once the rename has consumed the staging file.
	//
	// Not because the cleanup would otherwise delete the file it just put in
	// place - it would not. After the rename stagedPath still names the source,
	// which the rename emptied, so the Remove is an ignored ENOENT. Neutralising
	// this assignment fails no test in the suite, which is how that was
	// established rather than assumed.
	//
	// What it prevents is narrower: a consumed staging name is free, so a
	// concurrent writer's os.CreateTemp could claim it between the rename and
	// this deferred Remove, and the Remove would delete that writer's staging
	// file instead. Vanishingly unlikely against a random name, and one
	// assignment to rule out.
	stagedPath := staged.Name()
	defer func() {
		if stagedPath != "" {
			_ = os.Remove(stagedPath)
		}
	}()

	if _, err := staged.Write(data); err != nil {
		_ = staged.Close()
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	if err := staged.Chmod(mode); err != nil {
		_ = staged.Close()
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	if err := staged.Close(); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}

	if err := os.Rename(stagedPath, path); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	stagedPath = ""

	return nil
}
