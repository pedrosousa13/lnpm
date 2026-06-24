package tests

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/cli"
)

// retreatLeavesClean asserts the full post-retreat clean state for projectDir:
// every package symlink is gone, the lockfile is deleted, the .lnpm directory is
// removed, and no db links remain.
func retreatLeavesClean(t *testing.T, env *TestEnvironment, projectDir string, packages ...string) {
	t.Helper()
	for _, name := range packages {
		env.AssertSymlinkMissing(projectDir, name)
		env.AssertDatabaseNoLink(name, projectDir)
		env.AssertPackageJSONMissing(projectDir, name)
	}
	env.AssertLockfileExists(projectDir, false)
	env.AssertDirectoryExists(filepath.Join(projectDir, ".lnpm"), false)
}

// TestRetreatVariants table-drives the "add packages, then retreat --force"
// happy path across unscoped, scoped, dev, and multi-package shapes. Each row
// must leave the project fully cleaned up.
func TestRetreatVariants(t *testing.T) {
	cases := []struct {
		name     string
		packages []string
		dev      bool
	}{
		{"single unscoped", []string{"retreat-pkg"}, false},
		{"scoped", []string{"@org/scoped-retreat"}, false},
		{"dev dependency", []string{"dev-retreat-pkg"}, true},
		{"multiple packages", []string{"pkg-a", "pkg-b", "pkg-c"}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := setupTest(t)

			for _, name := range tc.packages {
				env.simplePkg(name)
			}
			projectDir := env.newProject("test-project")
			for _, name := range tc.packages {
				env.addPkg(projectDir, name, tc.dev, false)
				env.AssertSymlinkExists(projectDir, name)
			}

			if err := cli.RunRetreat(true, false); err != nil {
				t.Fatalf("Failed to retreat: %v", err)
			}
			retreatLeavesClean(t, env, projectDir, tc.packages...)
		})
	}
}

// TestRetreatNoForceFlag tests that retreat without --force changes nothing.
func TestRetreatNoForceFlag(t *testing.T) {
	env := setupTest(t)

	_, projectDir := env.publishAndAdd("force-pkg")

	if err := cli.RunRetreat(false, false); err != nil {
		t.Fatalf("Retreat without force failed: %v", err)
	}

	env.AssertSymlinkExists(projectDir, "force-pkg")
	env.AssertLockfileExists(projectDir, true)
	env.AssertDirectoryExists(filepath.Join(projectDir, ".lnpm"), true)
}

// TestRetreatNoLinks tests retreating a project with no links is a safe no-op.
func TestRetreatNoLinks(t *testing.T) {
	env := setupTest(t)

	projectDir := env.newProject("test-project")
	if err := cli.RunRetreat(true, false); err != nil {
		t.Fatalf("Retreat with no links failed: %v", err)
	}
	env.AssertLockfileExists(projectDir, false)
	env.AssertDirectoryExists(filepath.Join(projectDir, ".lnpm"), false)
}

// TestRetreatRestoresOriginalVersion tests that retreat restores the pre-add
// dependency version.
func TestRetreatRestoresOriginalVersion(t *testing.T) {
	env := setupTest(t)

	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	env.writePackageJSON(projectDir, map[string]interface{}{
		"name":         "test-project",
		"version":      "1.0.0",
		"dependencies": map[string]interface{}{"retreat-pkg": "^1.0.0"},
	})

	env.simplePkg("retreat-pkg")
	env.addPkg(projectDir, "retreat-pkg", false, false)
	env.AssertPackageJSON(projectDir, "retreat-pkg", "file:.lnpm/retreat-pkg")

	if err := cli.RunRetreat(true, false); err != nil {
		t.Fatalf("Failed to retreat: %v", err)
	}
	env.AssertPackageJSON(projectDir, "retreat-pkg", "^1.0.0")
}

// TestRetreatPreservesOtherDependencies tests that retreat removes only lnpm
// packages and leaves regular dependencies intact.
func TestRetreatPreservesOtherDependencies(t *testing.T) {
	env := setupTest(t)

	projectDir := env.CreateTestPackage("test-project", "1.0.0", nil)
	env.writePackageJSON(projectDir, map[string]interface{}{
		"name":    "test-project",
		"version": "1.0.0",
		"dependencies": map[string]interface{}{
			"lodash":  "^4.17.21",
			"express": "^4.18.0",
		},
	})

	env.simplePkg("retreat-pkg")
	env.addPkg(projectDir, "retreat-pkg", false, false)

	if err := cli.RunRetreat(true, false); err != nil {
		t.Fatalf("Failed to retreat: %v", err)
	}

	env.AssertPackageJSON(projectDir, "lodash", "^4.17.21")
	env.AssertPackageJSON(projectDir, "express", "^4.18.0")
	env.AssertPackageJSONMissing(projectDir, "retreat-pkg")
}

// TestRetreatPartiallyLinked tests retreat when only some published packages are
// actually linked into the project.
func TestRetreatPartiallyLinked(t *testing.T) {
	env := setupTest(t)

	env.simplePkg("pkg-linked")
	env.simplePkg("pkg-notlinked")

	projectDir := env.newProject("test-project")
	env.addPkg(projectDir, "pkg-linked", false, false)
	env.AssertSymlinkExists(projectDir, "pkg-linked")
	env.AssertSymlinkMissing(projectDir, "pkg-notlinked")

	if err := cli.RunRetreat(true, false); err != nil {
		t.Fatalf("Failed to retreat: %v", err)
	}
	env.AssertLockfileExists(projectDir, false)
	env.AssertDirectoryExists(filepath.Join(projectDir, ".lnpm"), false)
}

// TestRetreatCleansGitignore tests that the .lnpm/ entry is removed from
// .gitignore on retreat.
func TestRetreatCleansGitignore(t *testing.T) {
	env := setupTest(t)

	_, projectDir := env.publishAndAdd("git-pkg")
	env.AssertGitignore(projectDir, ".lnpm/", true)

	if err := cli.RunRetreat(true, false); err != nil {
		t.Fatalf("Failed to retreat: %v", err)
	}

	// The entry must be gone; the file itself may or may not remain.
	if _, err := os.Stat(filepath.Join(projectDir, ".gitignore")); err == nil {
		env.AssertGitignore(projectDir, ".lnpm/", false)
	}
}
