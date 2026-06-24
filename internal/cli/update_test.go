package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindChecksum(t *testing.T) {
	checksums := `aaaa1111  lnpm_1.2.3_darwin_arm64.tar.gz
bbbb2222  lnpm_1.2.3_linux_amd64.tar.gz
cccc3333  lnpm_1.2.3_windows_amd64.zip
`
	sum, ok := findChecksum(strings.NewReader(checksums), "lnpm_1.2.3_linux_amd64.tar.gz")
	if !ok || sum != "bbbb2222" {
		t.Fatalf("findChecksum = (%q, %v), want (bbbb2222, true)", sum, ok)
	}

	if _, ok := findChecksum(strings.NewReader(checksums), "lnpm_9.9.9_linux_amd64.tar.gz"); ok {
		t.Error("findChecksum found a checksum for a missing file")
	}
}

func TestSha256File(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(p, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	// SHA-256("hello")
	const want = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	got, err := sha256File(p)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("sha256File = %s, want %s", got, want)
	}
}
