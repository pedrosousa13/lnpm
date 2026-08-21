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

// monorepo writes a workspace root under the test's temp dir, with one
// package.json per member, and returns the root directory. rootPkgJSON is the
// root manifest verbatim, so a test can give it a broken "workspaces" field;
// members maps a root-relative directory to the manifest to write there.
func monorepo(env *TestEnvironment, rootPkgJSON map[string]interface{}, members map[string]map[string]interface{}) string {
	env.t.Helper()

	root := filepath.Join(env.TempDir, "monorepo")
	if err := os.MkdirAll(root, 0755); err != nil {
		env.t.Fatalf("Failed to create workspace root: %v", err)
	}
	env.writePackageJSON(root, rootPkgJSON)

	for rel, manifest := range members {
		dir := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(dir, 0755); err != nil {
			env.t.Fatalf("Failed to create workspace member %s: %v", rel, err)
		}
		env.writePackageJSON(dir, manifest)
	}

	return root
}

// TestCheckFindsAWorkspaceMemberReference is the issue's core case: the root
// manifest is clean, so the guard used to pass from the repo root while a member
// still carried a file:.lnpm/ reference that an npm publish would ship.
func TestCheckFindsAWorkspaceMemberReference(t *testing.T) {
	env := setupTest(t)

	root := monorepo(env, map[string]interface{}{
		"name":       "monorepo-root",
		"version":    "1.0.0",
		"private":    true,
		"workspaces": []interface{}{"packages/*"},
	}, map[string]map[string]interface{}{
		"packages/app": {
			"name":    "@mono/app",
			"version": "1.0.0",
			"dependencies": map[string]interface{}{
				"my-lib": "file:.lnpm/my-lib",
			},
		},
		"packages/clean": {
			"name":    "@mono/clean",
			"version": "1.0.0",
			"dependencies": map[string]interface{}{
				"left-pad": "^1.0.0",
			},
		},
	})
	env.chdir(root)

	var err error
	out := captureStdout(t, func() { err = cli.RunCheck() })
	if err == nil {
		t.Fatal("Expected check to fail on a workspace member's lnpm reference, got nil")
	}
	// AC 4: the report has to say which package, or a monorepo result sends the
	// reader hunting through every manifest by hand.
	if !strings.Contains(out, "@mono/app") {
		t.Errorf("Expected the report to name the offending package, got:\n%s", out)
	}
	if !strings.Contains(out, "file:.lnpm/my-lib") {
		t.Errorf("Expected the report to show the reference, got:\n%s", out)
	}
	if strings.Contains(out, "@mono/clean") {
		t.Errorf("Expected the clean member to stay out of the report, got:\n%s", out)
	}
}

// TestCheckFindsAWorkspaceMemberLinkReference covers the link: half of the same
// case through the shared reference helper.
func TestCheckFindsAWorkspaceMemberLinkReference(t *testing.T) {
	env := setupTest(t)

	root := monorepo(env, map[string]interface{}{
		"name":       "monorepo-root",
		"version":    "1.0.0",
		"workspaces": []interface{}{"packages/*"},
	}, map[string]map[string]interface{}{
		"packages/app": {
			"name":    "@mono/app",
			"version": "1.0.0",
			"devDependencies": map[string]interface{}{
				"my-lib": "link:.lnpm/my-lib",
			},
		},
	})
	env.chdir(root)

	var err error
	out := captureStdout(t, func() { err = cli.RunCheck() })
	if err == nil {
		t.Fatal("Expected check to fail on a workspace member's link:.lnpm/ reference, got nil")
	}
	if !strings.Contains(out, "@mono/app") || !strings.Contains(out, "link:.lnpm/my-lib") {
		t.Errorf("Expected the report to name the package and the reference, got:\n%s", out)
	}
}

// TestCheckPassesOnACleanWorkspace keeps the widened scan from becoming a false
// alarm: a workspace with nothing lnpm left behind still exits zero.
func TestCheckPassesOnACleanWorkspace(t *testing.T) {
	env := setupTest(t)

	root := monorepo(env, map[string]interface{}{
		"name":       "monorepo-root",
		"version":    "1.0.0",
		"workspaces": []interface{}{"packages/*"},
	}, map[string]map[string]interface{}{
		"packages/app": {
			"name":    "@mono/app",
			"version": "1.0.0",
			"dependencies": map[string]interface{}{
				"@mono/lib": "workspace:*",
				"left-pad":  "^1.0.0",
			},
		},
		"packages/lib": {
			"name":    "@mono/lib",
			"version": "1.0.0",
		},
	})
	env.chdir(root)

	if err := cli.RunCheck(); err != nil {
		t.Fatalf("Expected check to pass on a clean workspace, got: %v", err)
	}
}

// TestCheckWorkspaceReportsAnUnresolvableMemberList is AC 5. Silently checking
// only the root when the member list will not resolve is the exact failure this
// widening exists to remove, so an unresolvable workspace has to be loud.
func TestCheckWorkspaceReportsAnUnresolvableMemberList(t *testing.T) {
	cases := []struct {
		name  string
		setup func(env *TestEnvironment) string
		want  string
	}{
		{
			// ListPackages fails on a member whose manifest will not parse.
			name: "a member manifest that will not parse",
			setup: func(env *TestEnvironment) string {
				root := monorepo(env, map[string]interface{}{
					"name":       "monorepo-root",
					"version":    "1.0.0",
					"workspaces": []interface{}{"packages/*"},
				}, map[string]map[string]interface{}{
					"packages/app": {"name": "@mono/app", "version": "1.0.0"},
				})
				env.writeFile(filepath.Join(root, "packages", "app", "package.json"), "{ not json")
				return root
			},
			want: "app",
		},
		{
			// Detect itself fails on a malformed workspace glob.
			name: "a workspace pattern that will not parse",
			setup: func(env *TestEnvironment) string {
				return monorepo(env, map[string]interface{}{
					"name":       "monorepo-root",
					"version":    "1.0.0",
					"workspaces": []interface{}{"packages/["},
				}, nil)
			},
			want: "packages/[",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := setupTest(t)
			root := tc.setup(env)
			env.chdir(root)

			var err error
			out := captureStdout(t, func() { err = cli.RunCheck() })
			if err == nil {
				t.Fatalf("Expected check to fail when the workspace cannot be resolved, got nil (stdout:\n%s)", out)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Expected the error to point at %q, got: %v", tc.want, err)
			}
			if strings.Contains(out, "Nothing lnpm left behind") {
				t.Errorf("Expected no success report when the workspace cannot be resolved, got:\n%s", out)
			}
		})
	}
}

// TestCheckOutsideAWorkspacePrintsExactlyWhatItAlwaysHas is AC 3, and it is the
// assertion most easily broken by widening the scan: outside a workspace the
// report stays byte-for-byte what it is today, not merely the same exit code.
// captureStdout redirects stdout to a pipe, so the icons render as their
// plain-ASCII fallbacks and the expected text is stable.
func TestCheckOutsideAWorkspacePrintsExactlyWhatItAlwaysHas(t *testing.T) {
	t.Run("the failing case", func(t *testing.T) {
		env := setupTest(t)

		projectDir := env.CreateTestPackage("byte-for-byte-dirty", "1.0.0", nil)
		env.writePackageJSON(projectDir, map[string]interface{}{
			"name":    "byte-for-byte-dirty",
			"version": "1.0.0",
			"dependencies": map[string]interface{}{
				"my-lib": "file:.lnpm/my-lib",
			},
		})
		env.chdir(projectDir)

		var err error
		out := captureStdout(t, func() { err = cli.RunCheck() })

		want := "x Found 1 lnpm reference(s) in package.json:\n" +
			"  dependencies.my-lib -> file:.lnpm/my-lib\n" +
			"\n" +
			"  tip: Run 'lnpm retreat --force' to restore original dependencies before publishing\n"
		if out != want {
			t.Errorf("Report changed outside a workspace.\n got: %q\nwant: %q", out, want)
		}
		if err == nil {
			t.Fatal("Expected check to fail, got nil")
		}
		if got, want := err.Error(), "1 lnpm reference(s) found in package.json"; got != want {
			t.Errorf("Error message changed outside a workspace.\n got: %q\nwant: %q", got, want)
		}
	})

	t.Run("the passing case", func(t *testing.T) {
		env := setupTest(t)

		projectDir := env.CreateTestPackage("byte-for-byte-clean", "1.0.0", nil)
		env.writePackageJSON(projectDir, map[string]interface{}{
			"name":    "byte-for-byte-clean",
			"version": "1.0.0",
			"dependencies": map[string]interface{}{
				"left-pad": "^1.0.0",
			},
		})
		env.chdir(projectDir)

		var err error
		out := captureStdout(t, func() { err = cli.RunCheck() })
		if err != nil {
			t.Fatalf("Expected check to pass, got: %v", err)
		}
		if want := "OK Nothing lnpm left behind would be published\n"; out != want {
			t.Errorf("Report changed outside a workspace.\n got: %q\nwant: %q", out, want)
		}
	})
}
