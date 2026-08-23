package fsutil

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// writeSparseFile creates a file at path whose first bytes are head and whose
// length is size, by writing head and extending the file with os.Truncate
// instead of writing size bytes out.
//
// It reads back what it built rather than assuming: a test that wants an
// oversized file needs both properties - the length, so the cap sees it, and the
// leading bytes, so a check placed after the unmarshal fails on the contents
// instead. Whether the extension is stored sparsely is a filesystem question and
// is deliberately not asserted; only the size and the contents are.
func writeSparseFile(t *testing.T, path, head string, size int64) {
	t.Helper()

	if err := os.WriteFile(path, []byte(head), 0644); err != nil {
		t.Fatalf("WriteFile(%s) error: %v", path, err)
	}
	if err := os.Truncate(path, size); err != nil {
		t.Fatalf("Truncate(%s, %d) error: %v", path, size, err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%s) error: %v", path, err)
	}
	if info.Size() != size {
		t.Fatalf("built %s at %d bytes, want %d", path, info.Size(), size)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("Open(%s) error: %v", path, err)
	}
	defer func() { _ = f.Close() }()
	got := make([]byte, len(head))
	if _, err := io.ReadFull(f, got); err != nil {
		t.Fatalf("reading back the head of %s: %v", path, err)
	}
	if string(got) != head {
		t.Fatalf("head of %s = %q, want %q", path, got, head)
	}
}

func TestReadFileCappedReadsAFileUnderTheLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "small.yaml")
	if err := os.WriteFile(path, []byte("packages:\n  - a\n"), 0644); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	data, err := ReadFileCapped(path, MaxYAMLBytes)
	if err != nil {
		t.Fatalf("ReadFileCapped() error: %v", err)
	}
	if string(data) != "packages:\n  - a\n" {
		t.Errorf("ReadFileCapped() = %q, want the file's contents", data)
	}
}

// TestReadFileCappedRefusesAFileOverTheLimit checks the refusal names all three
// things a user needs to act on it: which file, how big it is, and what the
// limit was.
func TestReadFileCappedRefusesAFileOverTheLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "big.yaml")
	writeSparseFile(t, path, "packages: {not valid yaml", MaxYAMLBytes+1)

	data, err := ReadFileCapped(path, MaxYAMLBytes)
	if err == nil {
		t.Fatalf("ReadFileCapped() error = nil, want a refusal; read %d bytes", len(data))
	}
	if data != nil {
		t.Errorf("ReadFileCapped() = %d bytes, want nil alongside the refusal", len(data))
	}
	if !errors.Is(err, ErrFileTooLarge) {
		t.Errorf("ReadFileCapped() error = %v, want it to wrap ErrFileTooLarge", err)
	}
	msg := err.Error()
	for _, want := range []string{
		path,
		strconv.FormatInt(MaxYAMLBytes+1, 10),
		strconv.FormatInt(MaxYAMLBytes, 10),
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("ReadFileCapped() error = %q, want it to name %q", msg, want)
		}
	}
}

// TestReadFileCappedReportsAMissingFileAsNotExist pins the contract
// pkg/lockfile's read depends on: it turns a missing file into (nil, nil), and
// it can only keep doing that if os.IsNotExist still recognises what comes back
// from here.
func TestReadFileCappedReportsAMissingFileAsNotExist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.yaml")

	_, err := ReadFileCapped(path, MaxYAMLBytes)
	if err == nil {
		t.Fatal("ReadFileCapped() error = nil, want a not-exist error")
	}
	if !os.IsNotExist(err) {
		t.Errorf("os.IsNotExist(%v) = false, want true", err)
	}
}
