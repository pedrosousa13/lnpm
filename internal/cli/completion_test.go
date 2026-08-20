package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// bashrcHint is the opening of the instruction only a home-directory install
// should print.
const bashrcHint = "Add this to your ~/.bashrc"

// bashCompletionDirs lays out a home directory and a system completion
// directory side by side under one temp root. The system directory is
// deliberately outside home so the "did it land under home?" check the
// ~/.bashrc hint depends on can tell the two apart.
func bashCompletionDirs(t *testing.T) (home, systemDir, fallbackDir string) {
	t.Helper()

	root := t.TempDir()
	home = filepath.Join(root, "home")
	systemDir = filepath.Join(root, "system")
	fallbackDir = filepath.Join(home, ".bash_completion.d")

	if err := os.MkdirAll(home, 0755); err != nil {
		t.Fatalf("create home: %v", err)
	}
	return home, systemDir, fallbackDir
}

// installBashCompletionIn runs the installer with stdout captured, so the test
// output stays clean and the printed instructions can be asserted on.
//
// Don't use t.Parallel() in callers - this helper swaps the process-wide
// os.Stdout.
func installBashCompletionIn(t *testing.T, home, systemDir, fallbackDir string) (dir, out string, err error) {
	t.Helper()

	out = captureStdout(t, func() {
		dir, err = installBashCompletionInto(home, systemDir, fallbackDir)
	})
	return dir, out, err
}

func assertCompletionFile(t *testing.T, dir string) {
	t.Helper()

	info, err := os.Stat(filepath.Join(dir, "lnpm"))
	if err != nil {
		t.Fatalf("stat completion file in %s: %v", dir, err)
	}
	if info.Size() == 0 {
		t.Fatalf("completion file in %s is empty", dir)
	}
}

func assertNoCompletionFile(t *testing.T, dir string) {
	t.Helper()

	if _, err := os.Stat(filepath.Join(dir, "lnpm")); !os.IsNotExist(err) {
		t.Fatalf("completion file in %s: stat error = %v, want IsNotExist", dir, err)
	}
}

func TestInstallBashCompletionUsesWritableSystemDir(t *testing.T) {
	home, systemDir, fallbackDir := bashCompletionDirs(t)
	if err := os.MkdirAll(systemDir, 0755); err != nil {
		t.Fatalf("create system dir: %v", err)
	}

	dir, _, err := installBashCompletionIn(t, home, systemDir, fallbackDir)
	if err != nil {
		t.Fatalf("installBashCompletionInto: %v", err)
	}
	if dir != systemDir {
		t.Fatalf("installed to %q, want system dir %q", dir, systemDir)
	}
	assertCompletionFile(t, systemDir)
	if _, err := os.Stat(fallbackDir); !os.IsNotExist(err) {
		t.Fatalf("fallback dir %s: stat error = %v, want IsNotExist", fallbackDir, err)
	}
}

func TestInstallBashCompletionFallsBackWhenSystemDirIsAbsent(t *testing.T) {
	home, systemDir, fallbackDir := bashCompletionDirs(t)

	dir, _, err := installBashCompletionIn(t, home, systemDir, fallbackDir)
	if err != nil {
		t.Fatalf("installBashCompletionInto: %v", err)
	}
	if dir != fallbackDir {
		t.Fatalf("installed to %q, want fallback %q", dir, fallbackDir)
	}
	assertCompletionFile(t, fallbackDir)
	if _, err := os.Stat(systemDir); !os.IsNotExist(err) {
		t.Fatalf("system dir %s: stat error = %v, want IsNotExist", systemDir, err)
	}
}

// A system-wide install is already sourced by bash itself, so telling the user
// to source it from ~/.bashrc would be wrong. The matching home-directory case
// lives in completion_permissions_test.go.
func TestInstallBashCompletionOmitsBashrcHintForSystemInstalls(t *testing.T) {
	home, systemDir, fallbackDir := bashCompletionDirs(t)
	if err := os.MkdirAll(systemDir, 0755); err != nil {
		t.Fatalf("create system dir: %v", err)
	}

	dir, out, err := installBashCompletionIn(t, home, systemDir, fallbackDir)
	if err != nil {
		t.Fatalf("installBashCompletionInto: %v", err)
	}
	if dir != systemDir {
		t.Fatalf("installed to %q, want system dir %q", dir, systemDir)
	}
	if strings.Contains(out, bashrcHint) {
		t.Fatalf("output has %q hint for a system-wide install:\n%s", bashrcHint, out)
	}
}
