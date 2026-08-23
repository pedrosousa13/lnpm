//go:build !windows

package fsutil

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// TestReadFileCappedRefusesANonRegularFile covers the case that makes the size
// check mean anything. os.Stat reports Size() == 0 for a FIFO, so the size
// comparison lets one through - the test asserts that, so the reason the guard
// is needed is visible rather than asserted in a comment.
//
// Its revert behaviour is unusual and worth knowing before running it. Remove
// the regular-file guard and this test does not go red, it hangs: os.ReadFile
// opens the FIFO O_RDONLY and blocks until a writer appears, which with no
// writer is forever, so the package dies on go test's timeout instead. That
// hang is the bypass.
func TestReadFileCappedRefusesANonRegularFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fifo.yaml")
	if err := syscall.Mkfifo(path, 0644); err != nil {
		t.Skipf("mkfifo(%s): %v", path, err)
	}

	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("Lstat(%s) error: %v", path, err)
	}
	if info.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("built %s with mode %v, want a named pipe", path, info.Mode())
	}
	if info.Size() > MaxYAMLBytes {
		t.Fatalf("the fifo stats at %d bytes, over the %d-byte cap; this case only tests the guard if the size check would pass it", info.Size(), int64(MaxYAMLBytes))
	}

	_, err = ReadFileCapped(path, MaxYAMLBytes)
	if err == nil {
		t.Fatal("ReadFileCapped() error = nil, want a refusal for a named pipe")
	}
	for _, want := range []string{path, "not a regular file"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("ReadFileCapped() error = %q, want it to name %q", err, want)
		}
	}
}
