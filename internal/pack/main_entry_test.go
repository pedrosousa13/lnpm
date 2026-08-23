package pack

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// writeMainEntryTree writes rel -> content, creating parent directories. Keys
// are slash-separated so the fixtures read the same on every platform.
func writeMainEntryTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

// packedRelPaths packs root and returns the packed set sorted, so a test can
// assert the exact set rather than "contains".
func packedRelPaths(t *testing.T, root string) []string {
	t.Helper()
	_, files, err := Pack(root)
	if err != nil {
		t.Fatalf("Pack() error: %v", err)
	}
	got := make([]string, 0, len(files))
	for _, f := range files {
		got = append(got, f.RelPath)
	}
	sort.Strings(got)
	return got
}

func assertPackedSet(t *testing.T, root string, want []string, why string) {
	t.Helper()
	got := packedRelPaths(t, root)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("packed set mismatch: %s\n got: %v\nwant: %v", why, got, want)
	}
}

// TestPackForceIncludesMainUnderFilesWhitelist is #319's fixture, asserted as an
// exact set: main "lib/index.js" with files ["dist"] shipped [dist/a.js,
// package.json], so requiring the published package failed at runtime on the one
// path the manifest names.
//
// The "./" spelling is the same file. npm accepts both, and Node resolves them
// identically, so the two rows must produce the same packed set.
func TestPackForceIncludesMainUnderFilesWhitelist(t *testing.T) {
	for _, main := range []string{"lib/index.js", "./lib/index.js"} {
		t.Run(main, func(t *testing.T) {
			tmpDir := t.TempDir()
			writeMainEntryTree(t, tmpDir, map[string]string{
				"package.json": `{
					"name": "main-entry",
					"version": "1.0.0",
					"main": "` + main + `",
					"files": ["dist"]
				}`,
				"dist/a.js":     "module.exports = {}",
				"lib/index.js":  "module.exports = require('../dist/a.js')",
				"src/index.ts":  "export const x = 1",
				"lib/helper.js": "module.exports = 1",
			})

			assertPackedSet(t, tmpDir, []string{
				"dist/a.js",
				"lib/index.js",
				"package.json",
			}, "the manifest's main must ship whatever the files whitelist says, and "+
				"only the file it names — not its whole directory")
		})
	}
}

// TestPackEmptyMainChangesNothing pins the no-main baseline. Without it the
// force-include could be selecting paths for a reason other than main.
func TestPackEmptyMainChangesNothing(t *testing.T) {
	tmpDir := t.TempDir()
	writeMainEntryTree(t, tmpDir, map[string]string{
		"package.json": `{
			"name": "no-main",
			"version": "1.0.0",
			"files": ["dist"]
		}`,
		"dist/a.js":    "module.exports = {}",
		"lib/index.js": "module.exports = {}",
	})

	assertPackedSet(t, tmpDir, []string{
		"dist/a.js",
		"package.json",
	}, "a manifest with no main selects nothing extra")
}

// TestPackMainSurvivesIgnorePatternsUnderFilesWhitelist pins the decision
// recorded on mainEntryPath: under a whitelist, main beats the user's ignore
// patterns as well as the whitelist itself.
//
// The ignore file names the entry point by basename rather than by its
// directory on purpose. A "lib/" pattern would prune the directory during the
// walk, so this fixture would pass on the strength of the walk never reaching
// the file, which tests nothing about the branch under test.
func TestPackMainSurvivesIgnorePatternsUnderFilesWhitelist(t *testing.T) {
	tmpDir := t.TempDir()
	writeMainEntryTree(t, tmpDir, map[string]string{
		"package.json": `{
			"name": "main-vs-ignore",
			"version": "1.0.0",
			"main": "lib/index.js",
			"files": ["dist"]
		}`,
		".npmignore":   "index.js\n",
		"dist/a.js":    "module.exports = {}",
		"lib/index.js": "module.exports = {}",
	})

	assertPackedSet(t, tmpDir, []string{
		"dist/a.js",
		"lib/index.js",
		"package.json",
	}, "an ignore pattern must not drop the entry point the manifest names")
}

// TestPackMainRespectsIgnorePatternsWithoutFilesWhitelist is the other half of
// that decision, and the guard for the second revert direction: the
// force-include lives inside the whitelist branch, so a package with no "files"
// field behaves exactly as it did before #319.
//
// Hoisting the force-include above the whitelist branch — where it would also
// override the ignore patterns in non-whitelist mode — fails here.
func TestPackMainRespectsIgnorePatternsWithoutFilesWhitelist(t *testing.T) {
	tmpDir := t.TempDir()
	writeMainEntryTree(t, tmpDir, map[string]string{
		"package.json": `{
			"name": "main-no-whitelist",
			"version": "1.0.0",
			"main": "lib/index.js"
		}`,
		".npmignore":   "index.js\n",
		"dist/a.js":    "module.exports = {}",
		"lib/index.js": "module.exports = {}",
	})

	assertPackedSet(t, tmpDir, []string{
		"dist/a.js",
		"package.json",
	}, "with no files field the user's ignore patterns decide the whole tree, "+
		"main included, exactly as they did before #319")
}

// TestPackMissingMainDoesNotAbort pins that pack tolerates a main naming a path
// that is not on disk. See mainEntryPath's comment for why the publish path
// still refuses such a manifest.
func TestPackMissingMainDoesNotAbort(t *testing.T) {
	tmpDir := t.TempDir()
	writeMainEntryTree(t, tmpDir, map[string]string{
		"package.json": `{
			"name": "missing-main",
			"version": "1.0.0",
			"main": "lib/index.js",
			"files": ["dist"]
		}`,
		"dist/a.js": "module.exports = {}",
	})

	assertPackedSet(t, tmpDir, []string{
		"dist/a.js",
		"package.json",
	}, "a main that names nothing on disk selects nothing and must not abort pack")
}

// TestPackMainNamingDirectoryShipsNothingExtra pins that main is matched as one
// path and never as a prefix. Node resolves a directory main through its own
// index/package.json lookup, which #319 puts out of scope; what must not happen
// is main ["lib"] quietly widening the whitelist to everything under lib.
func TestPackMainNamingDirectoryShipsNothingExtra(t *testing.T) {
	tmpDir := t.TempDir()
	writeMainEntryTree(t, tmpDir, map[string]string{
		"package.json": `{
			"name": "dir-main",
			"version": "1.0.0",
			"main": "lib",
			"files": ["dist"]
		}`,
		"dist/a.js":     "module.exports = {}",
		"lib/index.js":  "module.exports = {}",
		"lib/secret.js": "module.exports = {}",
	})

	assertPackedSet(t, tmpDir, []string{
		"dist/a.js",
		"package.json",
	}, "a main naming a directory must not pull the directory's contents in")
}

// TestPackMainEscapingPackageRootSelectsNothing pins that no spelling of main
// reaches outside the package. The file it names really exists here, one level
// above the package root, so the assertion is about the selection rule and not
// about the file being absent.
func TestPackMainEscapingPackageRootSelectsNothing(t *testing.T) {
	for _, main := range []string{"../evil.js", "../../evil.js", "lib/../../evil.js"} {
		t.Run(main, func(t *testing.T) {
			parent := t.TempDir()
			writeMainEntryTree(t, parent, map[string]string{
				"evil.js": "stolen",
			})

			tmpDir := filepath.Join(parent, "pkg")
			writeMainEntryTree(t, tmpDir, map[string]string{
				"package.json": `{
					"name": "escaping-main",
					"version": "1.0.0",
					"main": "` + main + `",
					"files": ["dist"]
				}`,
				"dist/a.js":    "module.exports = {}",
				"lib/index.js": "module.exports = {}",
			})

			assertPackedSet(t, tmpDir, []string{
				"dist/a.js",
				"package.json",
			}, "main must never select a path outside the package root")
		})
	}
}
