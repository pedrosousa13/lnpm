package pack

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestPackSkipsSymlinks proves a symlink inside a package is not collected, so
// its target's contents can never be dereferenced into the store.
func TestPackSkipsSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires privileges on Windows")
	}

	// A "secret" outside the package the symlink will point at.
	secretDir := t.TempDir()
	secret := filepath.Join(secretDir, "secret.txt")
	if err := os.WriteFile(secret, []byte("TOP SECRET"), 0600); err != nil {
		t.Fatalf("write secret: %v", err)
	}

	pkgDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(pkgDir, "package.json"),
		[]byte(`{"name":"sym-pkg","version":"1.0.0"}`), 0644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "index.js"), []byte("module.exports={}"), 0644); err != nil {
		t.Fatalf("write index.js: %v", err)
	}
	if err := os.Symlink(secret, filepath.Join(pkgDir, "stolen.txt")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	_, files, err := Pack(pkgDir)
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}

	for _, f := range files {
		if f.RelPath == "stolen.txt" {
			t.Fatalf("symlink stolen.txt was collected; target content would leak into the store")
		}
	}
}
