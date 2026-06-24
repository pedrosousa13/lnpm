package tests

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/cli"
)

// TestPublishRejectsTraversalName proves a package whose name contains path
// traversal is rejected at publish time, even with validation skipped (the
// guard lives in the non-bypassable read path, not just ValidatePackage).
func TestPublishRejectsTraversalName(t *testing.T) {
	env := setupTest(t)

	dir := filepath.Join(env.TempDir, "evilpkg")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	pkgJSON := `{"name":"../../../../tmp/lnpm-evil","version":"1.0.0"}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkgJSON), 0644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.js"), []byte("x"), 0644); err != nil {
		t.Fatalf("write index.js: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	// push=false, all=false, skipHooks=false, skipValidation=true
	if err := cli.RunPublish(false, "", false, false, true); err == nil {
		t.Fatal("expected publish to reject a traversal package name, got nil error")
	}

	// The malicious target must not have been created.
	if _, err := os.Stat("/tmp/lnpm-evil"); err == nil {
		_ = os.RemoveAll("/tmp/lnpm-evil")
		t.Fatal("traversal name escaped the store: /tmp/lnpm-evil was created")
	}
}
