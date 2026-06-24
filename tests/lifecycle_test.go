package tests

import (
	"path/filepath"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/cli"
)

// scriptKeys returns the script names present in the package.json at dir.
func scriptKeys(t *testing.T, env *TestEnvironment, dir string) map[string]bool {
	t.Helper()
	pkgJSON := env.storedPackageJSON(dir)
	scripts, _ := pkgJSON["scripts"].(map[string]interface{})
	keys := make(map[string]bool, len(scripts))
	for k := range scripts {
		keys[k] = true
	}
	return keys
}

// TestStripLifecycleScripts verifies that publishing the pkg-with-hooks fixture
// strips the dangerous install-time scripts (prepare/prepublish) from the stored
// package while preserving the benign ones (build/test/postinstall).
func TestStripLifecycleScripts(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.CopyFixture("pkg-with-hooks")
	env.chdir(pkgDir)
	if err := cli.RunPublish(false, false, true, false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	pkg, err := env.Database.GetPackageByName("pkg-with-hooks")
	if err != nil || pkg == nil {
		t.Fatalf("Failed to get package: %v", err)
	}
	keys := scriptKeys(t, env, pkg.StorePath)

	for _, stripped := range []string{"prepare", "prepublish"} {
		if keys[stripped] {
			t.Errorf("Expected '%s' script to be stripped, but it exists", stripped)
		}
	}
	for _, kept := range []string{"build", "test", "postinstall"} {
		if !keys[kept] {
			t.Errorf("Expected '%s' script to be preserved", kept)
		}
	}
}

// TestPublishStripsPrepareScript table-drives publishing packages whose prepare
// script must be stripped from the stored copy, covering both a benign prepare
// (run before stripping) and a husky-style prepare published with --skip-hooks.
func TestPublishStripsPrepareScript(t *testing.T) {
	cases := []struct {
		name      string
		pkgName   string
		prepare   string
		skipHooks bool
	}{
		{"benign prepare runs then stripped", "prepare-pkg", "echo 'prepared' > prepared.txt", false},
		{"husky prepare skipped and stripped", "husky-pkg", "husky install", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := setupTest(t)

			pkgDir := env.CreateTestPackageWithScripts(tc.pkgName, "1.0.0",
				map[string]string{"index.js": "module.exports = 'test';"},
				map[string]string{"prepare": tc.prepare},
			)
			env.chdir(pkgDir)
			if err := cli.RunPublish(false, false, tc.skipHooks, false); err != nil {
				t.Fatalf("Failed to publish: %v", err)
			}

			pkg, err := env.Database.GetPackageByName(tc.pkgName)
			if err != nil || pkg == nil {
				t.Fatalf("Failed to get package: %v", err)
			}
			env.AssertScriptMissing(pkg.StorePath, tc.pkgName, "prepare")
		})
	}
}

// TestAddedPackageHasNoLifecycleScripts verifies the .lnpm copy created on add
// also has lifecycle scripts stripped, not just the store copy.
func TestAddedPackageHasNoLifecycleScripts(t *testing.T) {
	env := setupTest(t)

	pkgDir := env.CopyFixture("pkg-with-hooks")
	env.chdir(pkgDir)
	if err := cli.RunPublish(false, false, true, false); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	projectDir := env.newProject("test-project")
	env.addPkg(projectDir, "pkg-with-hooks", false, false)

	keys := scriptKeys(t, env, filepath.Join(projectDir, ".lnpm", "pkg-with-hooks"))
	for _, stripped := range []string{"prepare", "prepublish"} {
		if keys[stripped] {
			t.Errorf("Expected '%s' script to be stripped from .lnpm copy", stripped)
		}
	}
}
