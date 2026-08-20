package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/cli"
	"github.com/pedrosousa13/lnpm/pkg/lockfile"
)

// TestCheckCleanProject verifies check passes (nil error) when no lnpm
// references are present in package.json.
func TestCheckCleanProject(t *testing.T) {
	env := setupTest(t)

	projectDir := env.CreateTestPackage("clean-project", "1.0.0", nil)
	env.writePackageJSON(projectDir, map[string]interface{}{
		"name":    "clean-project",
		"version": "1.0.0",
		"dependencies": map[string]interface{}{
			"left-pad": "^1.0.0",
		},
		"devDependencies": map[string]interface{}{
			"typescript": "^5.0.0",
		},
	})
	env.chdir(projectDir)

	if err := cli.RunCheck(); err != nil {
		t.Fatalf("expected check to pass on clean project, got: %v", err)
	}
}

// TestCheckDetectsFileRef verifies check fails when a file:.lnpm/ reference is
// present in dependencies.
func TestCheckDetectsFileRef(t *testing.T) {
	env := setupTest(t)

	projectDir := env.CreateTestPackage("dirty-project", "1.0.0", nil)
	env.writePackageJSON(projectDir, map[string]interface{}{
		"name":    "dirty-project",
		"version": "1.0.0",
		"dependencies": map[string]interface{}{
			"my-lib": "file:.lnpm/my-lib",
		},
	})
	env.chdir(projectDir)

	if err := cli.RunCheck(); err == nil {
		t.Fatal("expected check to fail when a file:.lnpm/ reference is present, got nil")
	}
}

// TestCheckDetectsLinkRef verifies check fails when a link:.lnpm/ reference is
// present in devDependencies.
func TestCheckDetectsLinkRef(t *testing.T) {
	env := setupTest(t)

	projectDir := env.CreateTestPackage("dirty-dev-project", "1.0.0", nil)
	env.writePackageJSON(projectDir, map[string]interface{}{
		"name":    "dirty-dev-project",
		"version": "1.0.0",
		"devDependencies": map[string]interface{}{
			"my-lib": "link:.lnpm/my-lib",
		},
	})
	env.chdir(projectDir)

	if err := cli.RunCheck(); err == nil {
		t.Fatal("expected check to fail when a link:.lnpm/ reference is present, got nil")
	}
}

// TestCheckNoPackageJSON verifies check errors when there is no package.json.
func TestCheckNoPackageJSON(t *testing.T) {
	env := setupTest(t)

	dir := filepath.Join(env.TempDir, "no-pkg")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}
	env.chdir(dir)

	if err := cli.RunCheck(); err == nil {
		t.Fatal("expected check to error when no package.json exists, got nil")
	}
}

// TestCheckCatchesRealAddLink verifies that check detects a reference produced
// by `add --link`, closing the loop between the two features.
func TestCheckCatchesRealAddLink(t *testing.T) {
	env := setupTest(t)

	env.simplePkg("link-lib")
	projectDir := env.newProject("consumer")

	// add --link writes a link:.lnpm/ reference.
	if err := cli.RunAddMultiple([]string{"link-lib"}, false, false, false, true); err != nil {
		t.Fatalf("add --link failed: %v", err)
	}

	env.chdir(projectDir)
	if err := cli.RunCheck(); err == nil {
		t.Fatal("expected check to fail after add --link, got nil")
	}
}

// TestCheckDetectsAPublishableRetreatSnapshot covers the hazard this branch
// creates. `lnpm retreat --force` leaves lnpm.lock.retreat in the project root,
// and the README's own sequence runs `npm publish` next. The npm CLI reads none
// of lnpm's packing rules, so in a project with no "files" field and no ignore
// file the snapshot - an absolute source path per linked package - goes into the
// tarball.
func TestCheckDetectsAPublishableRetreatSnapshot(t *testing.T) {
	env := setupTest(t)

	env.simplePkg("snapshot-check-pkg")
	projectDir := env.newProject("snapshot-check-project")
	env.addPkg(projectDir, "snapshot-check-pkg", false, false)

	if err := cli.RunRetreat(true, false); err != nil {
		t.Fatalf("Failed to retreat: %v", err)
	}
	// retreat un-ignores .lnpm/, and this project has nothing else in
	// .gitignore, so nothing stands between the snapshot and the tarball.
	env.writeFile(filepath.Join(projectDir, ".gitignore"), "")
	env.AssertFileExists(lockfile.RetreatPath(projectDir), true)
	env.chdir(projectDir)

	var err error
	out := captureStdout(t, func() { err = cli.RunCheck() })
	if err == nil {
		t.Fatal("Expected check to fail with a publishable retreat snapshot, got nil")
	}
	if !strings.Contains(out, lockfile.RetreatFileName) {
		t.Errorf("Expected the report to name the snapshot, got:\n%s", out)
	}
	if !strings.Contains(err.Error(), lockfile.RetreatFileName) {
		t.Errorf("Expected the error to name the snapshot, got: %v", err)
	}
}

// TestCheckPassesWhenTheSnapshotCannotBePublished is the other half, and the one
// that keeps the guard from being a permanent false alarm. The documented flow
// retreats, checks, publishes and then restores, so the snapshot is on disk at
// check time by design. What check asks is whether it would actually be packed,
// and each of these three project setups answers no - by the same rules npm
// itself reads.
func TestCheckPassesWhenTheSnapshotCannotBePublished(t *testing.T) {
	cases := []struct {
		name  string
		setup func(env *TestEnvironment, projectDir string)
	}{
		{
			name: "listed in .gitignore, which npm falls back to",
			setup: func(env *TestEnvironment, projectDir string) {
				env.writeFile(filepath.Join(projectDir, ".gitignore"), lockfile.RetreatFileName+"\n")
			},
		},
		{
			name: "listed in .npmignore",
			setup: func(env *TestEnvironment, projectDir string) {
				env.writeFile(filepath.Join(projectDir, ".npmignore"), lockfile.RetreatFileName+"\n")
			},
		},
		{
			name: "not in the package.json files whitelist",
			setup: func(env *TestEnvironment, projectDir string) {
				env.writePackageJSON(projectDir, map[string]interface{}{
					"name":    "snapshot-safe-project",
					"version": "1.0.0",
					"files":   []interface{}{"index.js"},
				})
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := setupTest(t)

			env.simplePkg("snapshot-safe-pkg")
			projectDir := env.newProject("snapshot-safe-project")
			env.addPkg(projectDir, "snapshot-safe-pkg", false, false)

			if err := cli.RunRetreat(true, false); err != nil {
				t.Fatalf("Failed to retreat: %v", err)
			}
			env.AssertFileExists(lockfile.RetreatPath(projectDir), true)
			tc.setup(env, projectDir)
			env.chdir(projectDir)

			if err := cli.RunCheck(); err != nil {
				t.Errorf("Expected check to pass when the snapshot cannot be published, got: %v", err)
			}
		})
	}
}
