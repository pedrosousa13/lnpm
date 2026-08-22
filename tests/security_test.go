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
	if err := cli.RunPublish(false, false, false, true); err == nil {
		t.Fatal("expected publish to reject a traversal package name, got nil error")
	}

	// The malicious target must not have been created.
	if _, err := os.Stat("/tmp/lnpm-evil"); err == nil {
		_ = os.RemoveAll("/tmp/lnpm-evil")
		t.Fatal("traversal name escaped the store: /tmp/lnpm-evil was created")
	}
}

// TestPublishKeepsMixedCaseSecretsOutOfTheStore is issue #317 at the far end of
// the publish path. lnpm's built-in exclusion list matched case-sensitively, so
// ".ENV", ".Env.local" and ".NPMRC" were published while ".env" and ".npmrc"
// were not — on macOS and Windows the same files, held back or not according to
// how the developer typed the name.
//
// internal/pack proves the packed set is right. This proves what actually landed
// on disk, because the two have diverged before (#348): the store directory is
// what a linked project reads, so it is the claim that matters to a user.
func TestPublishKeepsMixedCaseSecretsOutOfTheStore(t *testing.T) {
	env := setupTest(t)

	env.publishPkg("mixed-case-secrets", "1.0.0", map[string]string{
		".ENV":                       "SECRET=1",
		".Env.local":                 "SECRET=2",
		".NPMRC":                     "//registry:_authToken=deadbeef",
		"Node_Modules/evil/index.js": "steal()",
		".envrc":                     "use flake",
		"index.js":                   "module.exports = 'ok';",
	})

	pkg, err := env.Database.GetPackageByName("mixed-case-secrets")
	if err != nil || pkg == nil {
		t.Fatalf("Failed to get package: %v", err)
	}

	for _, rel := range []string{".ENV", ".Env.local", ".NPMRC", "Node_Modules"} {
		if _, err := os.Lstat(filepath.Join(pkg.StorePath, rel)); err == nil {
			t.Errorf("%q reached the store at %s: a default exclude must hold however the name is cased", rel, pkg.StorePath)
		}
	}
	// Positive control, so the assertions above cannot pass on an empty store:
	// README documents .envrc as published, whatever its case.
	for _, rel := range []string{"package.json", "index.js", ".envrc"} {
		if _, err := os.Lstat(filepath.Join(pkg.StorePath, rel)); err != nil {
			t.Errorf("expected %q in the store at %s: %v", rel, pkg.StorePath, err)
		}
	}
}
