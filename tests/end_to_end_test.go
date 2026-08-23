package tests

import (
	"path/filepath"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/cli"
)

// TestE2EPublishAddRemove table-drives the complete publish → add → remove
// lifecycle for both unscoped and scoped packages. Each row asserts the link is
// fully established after add and fully torn down after remove.
func TestE2EPublishAddRemove(t *testing.T) {
	cases := []struct {
		name    string
		pkgName string
		files   map[string]string
	}{
		{
			name:    "unscoped",
			pkgName: "e2e-pkg",
			files: map[string]string{
				"index.js":     "module.exports = 'e2e';",
				"lib/utils.js": "module.exports.util = function() { return 'util'; };",
			},
		},
		{
			name:    "scoped",
			pkgName: "@myorg/scoped-e2e",
			files: map[string]string{
				"index.js":      "module.exports = 'scoped';",
				"lib/helper.js": "exports.help = () => 'help';",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := setupTest(t)

			env.publishPkg(tc.pkgName, "1.0.0", tc.files)
			env.AssertPackageInDatabase(tc.pkgName, true)

			projectDir := env.newProject("e2e-project")
			env.addPkg(projectDir, tc.pkgName, false, false)

			env.AssertSymlinkExists(projectDir, tc.pkgName)
			env.AssertPackageJSON(projectDir, tc.pkgName, "file:.lnpm/"+tc.pkgName)
			env.AssertLockfileExists(projectDir, true)
			env.AssertDatabaseLink(tc.pkgName, projectDir)
			if scope := filepath.Dir(tc.pkgName); scope != "." {
				env.AssertDirectoryExists(filepath.Join(projectDir, "node_modules", scope), true)
			}

			if err := cli.RunRemove(tc.pkgName, false, false, false); err != nil {
				t.Fatalf("Failed to remove package: %v", err)
			}

			env.AssertSymlinkMissing(projectDir, tc.pkgName)
			env.AssertPackageJSONMissing(projectDir, tc.pkgName)
			env.AssertLockfileExists(projectDir, false)
			env.AssertDatabaseNoLink(tc.pkgName, projectDir)
		})
	}
}

// TestE2EMultiplePackagesMultipleProjects tests linking several packages into
// several projects, then removing one package from one project independently.
func TestE2EMultiplePackagesMultipleProjects(t *testing.T) {
	env := setupTest(t)

	packages := []string{"pkg-a", "pkg-b", "pkg-c"}
	for _, name := range packages {
		env.simplePkg(name)
	}

	projects := []string{"project-1", "project-2"}
	projectDirs := make(map[string]string)
	for _, name := range projects {
		projectDirs[name] = env.CreateTestPackage(name, "1.0.0", nil)
	}

	for _, projDir := range projectDirs {
		for _, pkgName := range packages {
			env.addPkg(projDir, pkgName, false, false)
		}
	}

	for _, projDir := range projectDirs {
		for _, pkgName := range packages {
			env.AssertSymlinkExists(projDir, pkgName)
			env.AssertDatabaseLink(pkgName, projDir)
		}
	}

	// Remove one package from one project; the other project is unaffected.
	env.chdir(projectDirs["project-1"])
	if err := cli.RunRemove("pkg-b", false, false, false); err != nil {
		t.Fatalf("Failed to remove pkg-b from project-1: %v", err)
	}
	env.AssertSymlinkMissing(projectDirs["project-1"], "pkg-b")
	env.AssertSymlinkExists(projectDirs["project-2"], "pkg-b")
}

// TestE2ERetreatWorkflow tests adding multiple packages and tearing them all
// down with a single retreat.
func TestE2ERetreatWorkflow(t *testing.T) {
	env := setupTest(t)

	packages := []string{"retreat-a", "retreat-b"}
	for _, name := range packages {
		env.simplePkg(name)
	}

	projectDir := env.newProject("retreat-project")
	for _, name := range packages {
		env.addPkg(projectDir, name, false, false)
		env.AssertSymlinkExists(projectDir, name)
		env.AssertDatabaseLink(name, projectDir)
	}

	if err := cli.RunRetreat(true, false); err != nil {
		t.Fatalf("Failed to retreat: %v", err)
	}
	retreatLeavesClean(t, env, projectDir, packages...)
}

// TestE2EMixedDependencies tests a workflow mixing a prod and a dev dependency,
// removing only the dev one.
func TestE2EMixedDependencies(t *testing.T) {
	env := setupTest(t)

	env.simplePkg("prod-pkg")
	env.simplePkg("dev-pkg")

	projectDir := env.newProject("mixed-project")
	env.addPkg(projectDir, "prod-pkg", false, false)
	env.addPkg(projectDir, "dev-pkg", true, false)

	env.AssertSymlinkExists(projectDir, "prod-pkg")
	env.AssertSymlinkExists(projectDir, "dev-pkg")
	env.AssertDatabaseLink("prod-pkg", projectDir)
	env.AssertDatabaseLink("dev-pkg", projectDir)

	if err := cli.RunRemove("dev-pkg", false, false, false); err != nil {
		t.Fatalf("Failed to remove dev package: %v", err)
	}

	env.AssertSymlinkMissing(projectDir, "dev-pkg")
	env.AssertSymlinkExists(projectDir, "prod-pkg")
	env.AssertLockfileExists(projectDir, true)
}

// TestE2EWorkspacePublish tests publishing every package in an npm workspace.
func TestE2EWorkspacePublish(t *testing.T) {
	env := setupTest(t)

	workspaceDir := env.CopyFixture("npm-workspace")
	env.chdir(workspaceDir)
	if err := cli.RunPublish(false, true, false, false); err != nil {
		t.Fatalf("Failed to publish workspace: %v", err)
	}

	env.AssertPackageInDatabase("@npm-test/package-a", true)
	env.AssertPackageInDatabase("@npm-test/package-b", true)
}
