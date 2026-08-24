package testenv

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	gopath "path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// TestConfigIsolationCoversEveryPackage fails if a package with tests can reach
// internal/config without a TestMain that isolates the loader.
//
// "Isolates" means the TestMain body calls testenv.Run or
// config.IsolateForTesting. Merely declaring a function called TestMain does not
// count; see isolates for why that distinction is the whole guard.
//
// The per-package TestMain that #371 installed is a convention, and the issue
// exists because conventions in this area were forgotten twice: the same defect
// was filed for the tests package and, separately, hit in internal/link. A
// tenth package added later that imports the loader without a TestMain
// reproduces it silently, because reading the developer's real config is not a
// failure - it is a pass that depended on a file nobody in the repo can see.
// This test turns that back into a failure.
//
// It parses the tree rather than shelling out to `go list` so it needs no
// build, no network and no module cache. The tradeoff is that it ignores build
// constraints: a file excluded on this platform still contributes its imports,
// which over-approximates reachability. That direction is the safe one, and it
// keeps the result identical on every platform CI runs.
func TestConfigIsolationCoversEveryPackage(t *testing.T) {
	root := repoRoot(t)
	modulePath := modulePath(t, root)
	configPkg := modulePath + "/internal/config"

	pkgs := parseRepo(t, root, modulePath)
	if len(pkgs) == 0 {
		t.Fatal("parsed no packages; the repo walk is broken, not the repo")
	}
	if _, ok := pkgs[configPkg]; !ok {
		t.Fatalf("%s not among the parsed packages, so nothing could be found to reach it", configPkg)
	}

	var covered, missing []string
	for _, importPath := range sortedKeys(pkgs) {
		pkg := pkgs[importPath]
		if !pkg.hasTests {
			continue
		}
		chain, reaches := reaches(pkgs, importPath, configPkg)
		if !reaches {
			continue
		}
		if pkg.testMainIsolates {
			covered = append(covered, importPath)
			continue
		}

		var problem string
		if pkg.testMainFile == "" {
			problem = fmt.Sprintf("has test files but no TestMain. Add %s:",
				filepath.ToSlash(filepath.Join(pkg.dir, "main_test.go")))
		} else {
			problem = fmt.Sprintf(
				"declares TestMain in %s, but its body never calls testenv.Run or\n"+
					"    config.IsolateForTesting, so the tests still read the machine's config.\n"+
					"    Route the existing TestMain through the isolation:",
				pkg.testMainFile)
		}

		missing = append(missing, fmt.Sprintf(
			"%s\n    reaches the config loader: %s\n    %s\n%s",
			importPath,
			strings.Join(trimPaths(chain, modulePath), " -> "),
			problem,
			testMainSnippet(pkg.name, modulePath, importPath == configPkg),
		))
	}

	if len(missing) > 0 {
		t.Fatalf("%d package(s) can read the machine's own ~/.lnpm/config.yaml (#371):\n\n%s",
			len(missing), strings.Join(missing, "\n\n"))
	}

	// A guard that finds nothing to check passes for the wrong reason, and a
	// broken walk or a renamed module path would look exactly like a clean
	// repo. Reaching zero packages means this test stopped testing.
	if len(covered) == 0 {
		t.Fatalf("found no package with tests reaching %s, which cannot be right: "+
			"the walk or the reachability is broken, not the repo", configPkg)
	}
	t.Logf("%d package(s) with tests reach the config loader, all isolating in TestMain:\n  %s",
		len(covered), strings.Join(trimPaths(covered, modulePath), "\n  "))
}

// pkgInfo is what the guard needs to know about one directory's Go files. It is
// keyed by import path, so a directory holding both foo and foo_test collapses
// into one entry, which is what matters here: TestMain may be declared in
// either, and both end up in the same test binary.
type pkgInfo struct {
	dir     string // relative to the repo root, in slash form
	name    string // package clause, for the remediation snippet
	imports map[string]bool

	// nameFromNonTest records that name came from a file that is not a _test.go
	// file, so a later test file does not overwrite it. A directory with no
	// non-test files at all, which tests/ is, takes its name from a test file.
	nameFromNonTest bool

	hasTests bool

	// testMainFile is the file declaring func TestMain, empty when none does,
	// and testMainIsolates records whether that declaration reaches the
	// isolation. The two are separate because a TestMain that does not isolate
	// is a different problem from no TestMain, and needs a different fix.
	testMainFile     string
	testMainIsolates bool
}

// parseRepo parses every .go file under root, _test.go included, and returns
// the packages by import path.
func parseRepo(t *testing.T, root, modulePath string) map[string]*pkgInfo {
	t.Helper()

	pkgs := map[string]*pkgInfo{}
	fset := token.NewFileSet()

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			base := d.Name()
			// testdata and _-prefixed directories are invisible to the go
			// command, and vendor holds code this repo does not own.
			if path != root && (base == "vendor" || base == "testdata" ||
				strings.HasPrefix(base, ".") || strings.HasPrefix(base, "_")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}

		// ParseComments is off: only the package clause, the imports and the
		// top-level function names are read.
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}

		relDir, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}
		relDir = filepath.ToSlash(relDir)

		importPath := modulePath
		if relDir != "." {
			importPath += "/" + relDir
		}

		pkg := pkgs[importPath]
		if pkg == nil {
			pkg = &pkgInfo{dir: relDir, imports: map[string]bool{}}
			pkgs[importPath] = pkg
		}

		isTest := strings.HasSuffix(path, "_test.go")
		if isTest {
			pkg.hasTests = true
			if fn := findTestMain(file); fn != nil {
				pkg.testMainFile = filepath.ToSlash(filepath.Join(relDir, filepath.Base(path)))
				pkg.testMainIsolates = isolates(fn, file, importPath, modulePath)
			}
			if !pkg.nameFromNonTest {
				// The external test package is foo_test, which is not a package
				// clause to print in a snippet the reader will paste.
				pkg.name = strings.TrimSuffix(file.Name.Name, "_test")
			}
		} else {
			pkg.name = file.Name.Name
			pkg.nameFromNonTest = true
		}

		for _, spec := range file.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return fmt.Errorf("%s: bad import %s: %w", relDir, spec.Path.Value, err)
			}
			pkg.imports[path] = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return pkgs
}

// findTestMain returns the declaration of func TestMain in file, or nil.
func findTestMain(file *ast.File) *ast.FuncDecl {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Recv == nil && fn.Name.Name == "TestMain" {
			return fn
		}
	}
	return nil
}

// isolates reports whether this TestMain body calls testenv.Run or
// config.IsolateForTesting.
//
// Checking the body, not just the name, is the difference between a guard and a
// formality. TestMain has other uses: before #371, internal/cli's recorded the
// root command's version template and then called m.Run() directly. A check for
// the name alone would have scored that package covered, so it would not have
// caught the bug it exists to catch, and any TestMain added later for fixtures
// or flags would silently reopen it.
//
// Calls are resolved through the declaring file's own import names, so an
// aliased import is followed and an unrelated Run from some other package is
// not mistaken for this one. The two packages that own the isolation call it
// unqualified, since they cannot import themselves.
//
// A TestMain that delegates to a helper which isolates is reported as not
// isolating. That is a false alarm, and it is the safe direction: it fails
// loudly with a message naming the file, where the opposite error is the silent
// pass this whole issue is about.
func isolates(fn *ast.FuncDecl, file *ast.File, importPath, modulePath string) bool {
	testenvPkg := modulePath + "/internal/testenv"
	configPkg := modulePath + "/internal/config"

	// Import name to import path, honouring an explicit alias. Blank and dot
	// imports cannot be the base of a qualified call, so they are skipped.
	byName := map[string]string{}
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		name := gopath.Base(path)
		if spec.Name != nil {
			if spec.Name.Name == "_" || spec.Name.Name == "." {
				continue
			}
			name = spec.Name.Name
		}
		byName[name] = path
	}

	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if found {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fun := call.Fun.(type) {
		case *ast.SelectorExpr:
			base, ok := fun.X.(*ast.Ident)
			if !ok {
				return true
			}
			switch byName[base.Name] {
			case testenvPkg:
				found = fun.Sel.Name == "Run"
			case configPkg:
				found = fun.Sel.Name == "IsolateForTesting"
			}
		case *ast.Ident:
			// Unqualified, so it can only be the isolation if this package is
			// the one that declares it.
			found = (importPath == testenvPkg && fun.Name == "Run") ||
				(importPath == configPkg && fun.Name == "IsolateForTesting")
		}
		return !found
	})
	return found
}

// reaches reports whether from imports target, directly or through other
// packages in this module, and returns the chain it found.
//
// Transitive rather than direct on purpose: cmd/lnpm imports internal/cli,
// which imports internal/config. Its test binary therefore links the loader
// even though no file in cmd/lnpm names it, and a test there could reach it.
// Imports outside this module are leaves and are not followed, so nothing here
// depends on the module cache.
func reaches(pkgs map[string]*pkgInfo, from, target string) ([]string, bool) {
	// The loader's own package reaches it most directly of all, and its tests
	// call the memoised LoadConfig like anyone else's. Without this it would be
	// the one package the guard never checked.
	if from == target {
		return []string{target}, true
	}

	type step struct {
		pkg   string
		chain []string
	}

	seen := map[string]bool{from: true}
	queue := []step{{pkg: from, chain: []string{from}}}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]

		pkg := pkgs[cur.pkg]
		if pkg == nil {
			continue
		}
		for _, imp := range sortedKeys(pkg.imports) {
			if imp == target {
				return append(cur.chain, target), true
			}
			if seen[imp] {
				continue
			}
			if _, ours := pkgs[imp]; !ours {
				continue
			}
			seen[imp] = true
			queue = append(queue, step{pkg: imp, chain: append(append([]string{}, cur.chain...), imp)})
		}
	}
	return nil, false
}

// testMainSnippet is the file the failure message tells the reader to write.
//
// internal/config needs a different one: testenv imports config, so a test file
// in package config that imported testenv would be an import cycle. Its
// TestMain does the same setup inline against IsolateForTesting, which is why
// that case is spelled out rather than left for the reader to discover from a
// compiler error.
func testMainSnippet(pkgName, modulePath string, isConfigPkg bool) string {
	if isConfigPkg {
		return fmt.Sprintf(`
	package %s

	import (
		"fmt"
		"os"
		"testing"
	)

	// testenv imports this package, so importing it back would be a cycle.
	func TestMain(m *testing.M) {
		dir, err := os.MkdirTemp("", "lnpm-test-config-")
		if err != nil {
			fmt.Fprintf(os.Stderr, "config: create temp config dir: %%v\n", err)
			os.Exit(1)
		}
		IsolateForTesting(dir)

		code := m.Run()
		os.RemoveAll(dir)
		os.Exit(code)
	}
`, pkgName)
	}

	return fmt.Sprintf(`
	package %s

	import (
		"os"
		"testing"

		"%s/internal/testenv"
	)

	func TestMain(m *testing.M) {
		os.Exit(testenv.Run(m))
	}
`, pkgName, modulePath)
}

// repoRoot walks up from the working directory to the directory holding go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod found above the working directory")
		}
		dir = parent
	}
}

// modulePath reads the module line out of go.mod.
func modulePath(t *testing.T, root string) string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.TrimSpace(rest)
		}
	}
	t.Fatal("go.mod has no module line")
	return ""
}

// trimPaths shortens import paths to the part after the module path, so a
// failure message reads internal/cli rather than the full path four times.
func trimPaths(paths []string, modulePath string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = strings.TrimPrefix(strings.TrimPrefix(p, modulePath), "/")
		if out[i] == "" {
			out[i] = "."
		}
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
