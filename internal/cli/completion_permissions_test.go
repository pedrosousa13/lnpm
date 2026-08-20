package cli

import (
	"os"
	"strings"
	"testing"
)

// unwritableSystemDir creates the system completion directory and takes away
// the write bit, standing in for /etc/bash_completion.d as an unprivileged
// user sees it: present, but closed to everyone but root.
func unwritableSystemDir(t *testing.T, systemDir string) {
	t.Helper()

	if err := os.MkdirAll(systemDir, 0755); err != nil {
		t.Fatalf("create system dir: %v", err)
	}
	if err := os.Chmod(systemDir, 0555); err != nil {
		t.Fatalf("chmod system dir: %v", err)
	}
	// t.TempDir cleanup cannot remove what it cannot write to.
	t.Cleanup(func() { _ = os.Chmod(systemDir, 0755) })
}

// An existing system directory the user cannot write to is the common case for
// an unprivileged user. Existence alone must not commit the install to that
// directory.
func TestInstallBashCompletionFallsBackWhenSystemDirIsNotWritable(t *testing.T) {
	requirePermissionEnforcement(t)

	home, systemDir, fallbackDir := bashCompletionDirs(t)
	unwritableSystemDir(t, systemDir)

	dir, _, err := installBashCompletionIn(t, home, systemDir, fallbackDir)
	if err != nil {
		t.Fatalf("installBashCompletionInto: %v", err)
	}
	if dir != fallbackDir {
		t.Fatalf("installed to %q, want fallback %q", dir, fallbackDir)
	}
	assertCompletionFile(t, fallbackDir)
	assertNoCompletionFile(t, systemDir)
}

// The hint tells the user to source the file from ~/.bashrc, which is right
// only once the install has fallen back into their home directory. The
// system-wide counterpart lives in completion_test.go.
func TestInstallBashCompletionHintsBashrcForHomeInstalls(t *testing.T) {
	requirePermissionEnforcement(t)

	home, systemDir, fallbackDir := bashCompletionDirs(t)
	unwritableSystemDir(t, systemDir)

	dir, out, err := installBashCompletionIn(t, home, systemDir, fallbackDir)
	if err != nil {
		t.Fatalf("installBashCompletionInto: %v", err)
	}
	if dir != fallbackDir {
		t.Fatalf("installed to %q, want fallback %q", dir, fallbackDir)
	}
	if !strings.Contains(out, bashrcHint) {
		t.Fatalf("output missing %q hint:\n%s", bashrcHint, out)
	}
}
