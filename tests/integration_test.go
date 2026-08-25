package tests

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/cli"
)

// publishAllFixture copies a workspace fixture into the test's temp dir, chdirs
// into the copy, and runs `publish --all` against the isolated store. Fixtures
// are never published from the committed tree, and ~/.lnpm is never touched.
//
// It validates. This call passed skipValidation=true from #28 (1abccde), the
// commit that added internal/validation/validation.go — whose whole body is
// byte-identical to today's, diffed rather than eyeballed — with the reason
// "skip validation since test fixtures don't have built files".
//
// ValidatePackage enforces three things: a non-empty "name", a non-empty
// "version", and, for a declared "main", that os.Stat finds it on disk. Read at
// both ends, 1abccde and now, over all 15 package.json files in these five
// fixture trees: every one of them carries a name and a version, so those two
// halves were satisfied throughout and only the "main" half was ever masked.
// What it masked was turborepo's ui and utils, both naming "dist/index.js" into
// a fixture with no dist directory; every other member either ships the file its
// "main" names or declares none. So the flag was hiding one defect, #365 built
// the two dist/index.js files, and the flag went with it. Ran, not inferred —
// all five rows pass with validation on (2026-08-25).
//
// The warning check is #365's other half. pack.Pack prints
// `warning: package.json "main" is ...` when the packed set does not hold the
// entry point (#319), and it is what found these two fixtures. It catches a
// different route from the validation above. Both routes measured on
// 2026-08-25, and the messages below are quoted whole:
//
//   - Delete the two dist/index.js files and validation refuses first —
//     "validation failed: main file not found: dist/index.js (did you run build
//     scripts?)" for ui and utils, publish --all returns "2 of 3 package(s)
//     failed to publish", and pack never runs, so the warning never prints. That
//     route reaches the t.Fatalf, not this check.
//   - Put a ".npmignore" holding "dist/" in the ui fixture, so the entry point
//     is on disk for validation and dropped from the pack, and this check is
//     the one that fires — the warning appears once and names "dist/index.js".
//     (Nothing force-includes it: ADR-0004's override applies under a "files"
//     whitelist, and neither library has one.)
func publishAllFixture(t *testing.T, env *TestEnvironment, fixture string) {
	t.Helper()

	dir := env.CopyFixture(fixture)
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Failed to change to fixture directory %s: %v", dir, err)
	}

	var err error
	out := captureStdout(t, func() {
		err = cli.RunPublish(false, true, false, false)
	})
	if err != nil {
		t.Fatalf("Failed to publish all packages: %v\npublish output:\n%s", err, out)
	}
	if strings.Contains(out, `warning: package.json "main"`) {
		t.Errorf("publish --all over the %s fixture warned about a missing entry point:\n%s", fixture, out)
	}
}

// assertPublished verifies each named package exists in the isolated store DB.
func assertPublished(t *testing.T, env *TestEnvironment, names ...string) {
	t.Helper()

	for _, name := range names {
		pkg, err := env.Database.GetPackageByName(name)
		if err != nil || pkg == nil {
			t.Errorf("Expected %s to be published", name)
		}
	}
}

// assertPublishedFiles checks the store's file manifest for name against want,
// exactly. The manifest records what publish packed rather than what the
// fixture holds on disk, so it is the set a consumer receives.
//
// want is spelled with "/" on every platform: pack puts each relPath through
// filepath.ToSlash before it becomes FileInfo.RelPath (internal/pack/pack.go),
// and publish copies that field straight into the row
// (internal/cli/publish.go:384). Read from the source for Windows; run here on
// Linux, where ToSlash is identity anyway.
//
// want may be given in any order — it is sorted here, on a copy, so that a row
// never fails for its spelling rather than for what the package shipped. One
// thing it cannot say: slices.Equal treats a nil and an empty slice alike, so a
// want of []string{} asserts nothing while reading like an assertion. Nothing
// reaches that today, since every published package holds at least its
// manifest, which pack refuses to omit (requireManifestPacked).
func assertPublishedFiles(t *testing.T, env *TestEnvironment, name string, want []string) {
	t.Helper()

	pkg, err := env.Database.GetPackageByName(name)
	if err != nil || pkg == nil {
		t.Fatalf("Expected %s to be published: %v", name, err)
	}
	entries, err := env.Database.GetFilesForPackage(pkg.ID)
	if err != nil {
		t.Fatalf("Failed to read the file manifest for %s: %v", name, err)
	}

	got := make([]string, len(entries))
	for i, e := range entries {
		got[i] = e.RelativePath
	}
	slices.Sort(got)

	wantSorted := slices.Clone(want)
	slices.Sort(wantSorted)
	if !slices.Equal(got, wantSorted) {
		t.Errorf("%s published %v, want %v", name, got, wantSorted)
	}
}

// TestPublishAllWorkspaces tests `publish --all` across every supported
// workspace layout. Each row is a distinct workspace type whose fixture must
// yield the expected published package names in the isolated store.
//
// wantFiles is set for turborepo alone, and is #365's assertion: the two
// library members declare main "dist/index.js", so the file has to be in what
// they publish, not merely on disk beside the fixture. The other four rows are
// left asserting names only. Read from those fixtures: none of their manifests
// carries a "files" field, and the one ignore file among them holds ".lnpm/"
// (tests/fixtures/nx/libs/feature-auth/.gitignore), so there is no selection
// rule in play there for a file set to pin — internal/pack's own tests are
// where selection is covered.
func TestPublishAllWorkspaces(t *testing.T) {
	cases := []struct {
		name     string
		fixture  string
		expected []string
		// wantFiles maps a published package name to the exact set of files it
		// must ship. Optional: a nil map asserts nothing about any file set.
		wantFiles map[string][]string
	}{
		// lnpm publishes all workspace packages, including the web app.
		{"turborepo", "turborepo", []string{"@turborepo-test/ui", "@turborepo-test/utils", "turborepo-web-app"}, map[string][]string{
			// Neither library has a "files" field, so both ship their whole
			// tree: the built entry point the manifest names, the source it was
			// built from, and the manifest itself.
			"@turborepo-test/ui":    {"dist/index.js", "package.json", "src/index.js"},
			"@turborepo-test/utils": {"dist/index.js", "package.json", "src/index.js"},
			// The web app has no files of its own beyond its manifest.
			"turborepo-web-app": {"package.json"},
		}},
		{"pnpm", "pnpm-workspace", []string{"@pnpm-test/lib-a", "@pnpm-test/lib-b"}, nil},
		{"npm", "npm-workspace", []string{"@npm-test/package-a", "@npm-test/package-b"}, nil},
		{"yarn", "yarn-workspace", []string{"@yarn-test/library"}, nil},
		{"nx", "nx", []string{"@nx-test/feature-auth"}, nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := setupTest(t)
			publishAllFixture(t, env, tc.fixture)
			assertPublished(t, env, tc.expected...)
			for _, name := range tc.expected {
				if want, ok := tc.wantFiles[name]; ok {
					assertPublishedFiles(t, env, name, want)
				}
			}
		})
	}
}

// TestNxAddInternalDependency tests that adding a package to an internal Nx package
// doesn't modify the top-level workspace package.json
func TestNxAddInternalDependency(t *testing.T) {
	env := setupTest(t)

	// Copy both fixtures into the test temp dir so the committed tree is untouched.
	nxDir := env.CopyFixture("nx")
	npmDir := env.CopyFixture("npm-workspace")

	// Publish the npm package we'll add later.
	pkgADir := filepath.Join(npmDir, "packages", "package-a")
	if err := os.Chdir(pkgADir); err != nil {
		t.Fatalf("Failed to change to package-a directory: %v", err)
	}
	if err := cli.RunPublish(true, false, false, false); err != nil {
		t.Fatalf("Failed to publish package-a: %v", err)
	}

	// Publish the nx sub-package.
	featureAuthDir := filepath.Join(nxDir, "libs", "feature-auth")
	if err := os.Chdir(featureAuthDir); err != nil {
		t.Fatalf("Failed to change to feature-auth directory: %v", err)
	}
	if err := cli.RunPublish(true, false, false, false); err != nil {
		t.Fatalf("Failed to publish feature-auth package: %v", err)
	}

	// Snapshot the root and sub-package package.json before adding.
	originalRootData, err := os.ReadFile(filepath.Join(nxDir, "package.json"))
	if err != nil {
		t.Fatalf("Failed to read root package.json: %v", err)
	}
	originalSubData, err := os.ReadFile(filepath.Join(featureAuthDir, "package.json"))
	if err != nil {
		t.Fatalf("Failed to read sub-package package.json: %v", err)
	}

	// Add the published package to the sub-package.
	if err := os.Chdir(featureAuthDir); err != nil {
		t.Fatalf("Failed to change to feature-auth directory: %v", err)
	}
	if err := cli.RunAdd("@npm-test/package-a", false, false, false); err != nil {
		t.Fatalf("Failed to add package: %v", err)
	}

	// The sub-package package.json should have changed.
	updatedSubData, err := os.ReadFile(filepath.Join(featureAuthDir, "package.json"))
	if err != nil {
		t.Fatalf("Failed to read updated sub-package package.json: %v", err)
	}
	if string(updatedSubData) == string(originalSubData) {
		t.Error("Expected sub-package package.json to be updated")
	}

	// The top-level workspace package.json must be untouched.
	updatedRootData, err := os.ReadFile(filepath.Join(nxDir, "package.json"))
	if err != nil {
		t.Fatalf("Failed to read updated root package.json: %v", err)
	}
	if string(updatedRootData) != string(originalRootData) {
		t.Error("Expected top-level package.json to remain unchanged")
	}
}
