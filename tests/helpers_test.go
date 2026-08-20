package tests

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/cli"
	"github.com/pedrosousa13/lnpm/internal/db"
	"github.com/pedrosousa13/lnpm/internal/pack"
	"github.com/pedrosousa13/lnpm/pkg/lockfile"
)

// TestEnvironment provides isolated test environment for each test
type TestEnvironment struct {
	TempDir     string
	StoreDir    string
	OriginalDir string
	Database    *db.DB
	t           *testing.T
}

// setupTest creates isolated test environment
func setupTest(t *testing.T) *TestEnvironment {
	t.Helper()

	// Save original directory
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get working directory: %v", err)
	}

	// Create temp store directory
	storeDir := t.TempDir()

	// Set LNPM_STORE env var for isolated store (t.Setenv is parallel-safe)
	t.Setenv("LNPM_STORE", storeDir)

	// Reset database singleton for test isolation
	db.ResetForTesting()

	// Cleanup: close DB and restore directory
	t.Cleanup(func() {
		db.ResetForTesting()
		_ = os.Chdir(originalDir)
	})

	// Get database instance (will use LNPM_STORE)
	database, err := db.GetDB()
	if err != nil {
		t.Fatalf("Failed to initialize database: %v", err)
	}

	return &TestEnvironment{
		TempDir:     t.TempDir(),
		StoreDir:    storeDir,
		OriginalDir: originalDir,
		Database:    database,
		t:           t,
	}
}

// Cleanup performs environment cleanup
func (te *TestEnvironment) Cleanup() {
	_ = os.Chdir(te.OriginalDir)
}

// FixturePath returns the absolute path of a fixture, for the fixtures that are
// read in place rather than copied - a single file, say, which CopyFixture
// cannot serve because it copies a directory tree.
func (te *TestEnvironment) FixturePath(parts ...string) string {
	te.t.Helper()

	return filepath.Join(append([]string{te.OriginalDir, "fixtures"}, parts...)...)
}

// CopyFixture copies a fixture directory to a temp location
func (te *TestEnvironment) CopyFixture(name string) string {
	te.t.Helper()

	src := te.FixturePath(name)
	dst := filepath.Join(te.TempDir, name)

	if err := copyDir(src, dst); err != nil {
		te.t.Fatalf("Failed to copy fixture %s: %v", name, err)
	}

	// Clean any existing .lnpm directories in the copied fixture
	_ = os.RemoveAll(filepath.Join(dst, ".lnpm"))
	_ = cleanLnpmDirs(dst)

	return dst
}

// CreateTestPackage creates a test package directory with files
func (te *TestEnvironment) CreateTestPackage(name, version string, files map[string]string) string {
	te.t.Helper()

	pkgDir := filepath.Join(te.TempDir, "packages", name)
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		te.t.Fatalf("Failed to create package dir: %v", err)
	}

	// Create package.json
	pkgJSON := map[string]interface{}{
		"name":    name,
		"version": version,
	}
	data, _ := json.MarshalIndent(pkgJSON, "", "  ")
	if err := os.WriteFile(filepath.Join(pkgDir, "package.json"), data, 0644); err != nil {
		te.t.Fatalf("Failed to write package.json: %v", err)
	}

	// Create additional files
	for path, content := range files {
		fullPath := filepath.Join(pkgDir, path)
		dir := filepath.Dir(fullPath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			te.t.Fatalf("Failed to create dir %s: %v", dir, err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
			te.t.Fatalf("Failed to write file %s: %v", path, err)
		}
	}

	return pkgDir
}

// AssertPackageJSON verifies package.json contains expected value
func (te *TestEnvironment) AssertPackageJSON(projectPath, packageName, expectedValue string) {
	te.t.Helper()

	data, err := os.ReadFile(filepath.Join(projectPath, "package.json"))
	if err != nil {
		te.t.Fatalf("Failed to read package.json: %v", err)
	}

	var pkgJSON map[string]interface{}
	if err := json.Unmarshal(data, &pkgJSON); err != nil {
		te.t.Fatalf("Failed to parse package.json: %v", err)
	}

	// Check dependencies
	deps := getMapValue(pkgJSON, "dependencies")
	devDeps := getMapValue(pkgJSON, "devDependencies")

	foundDep := getStringValue(deps, packageName)
	foundDevDep := getStringValue(devDeps, packageName)

	if foundDep != expectedValue && foundDevDep != expectedValue {
		te.t.Errorf("Expected %s in package.json with value %s, found dep=%s devDep=%s",
			packageName, expectedValue, foundDep, foundDevDep)
	}
}

// AssertPackageJSONMissing verifies package is NOT in package.json
func (te *TestEnvironment) AssertPackageJSONMissing(projectPath, packageName string) {
	te.t.Helper()

	data, err := os.ReadFile(filepath.Join(projectPath, "package.json"))
	if err != nil {
		te.t.Fatalf("Failed to read package.json: %v", err)
	}

	var pkgJSON map[string]interface{}
	if err := json.Unmarshal(data, &pkgJSON); err != nil {
		te.t.Fatalf("Failed to parse package.json: %v", err)
	}

	deps := getMapValue(pkgJSON, "dependencies")
	devDeps := getMapValue(pkgJSON, "devDependencies")

	if _, ok := deps[packageName]; ok {
		te.t.Errorf("Expected %s NOT in dependencies, but found it", packageName)
	}
	if _, ok := devDeps[packageName]; ok {
		te.t.Errorf("Expected %s NOT in devDependencies, but found it", packageName)
	}
}

// AssertLockfile verifies lockfile contents
func (te *TestEnvironment) AssertLockfile(projectPath string, assertions func(*lockfile.LockFile)) {
	te.t.Helper()

	lock, err := lockfile.Load(projectPath)
	if err != nil {
		te.t.Fatalf("Failed to load lockfile: %v", err)
	}

	assertions(lock)
}

// AssertLockfileExists checks if lockfile exists
func (te *TestEnvironment) AssertLockfileExists(projectPath string, shouldExist bool) {
	te.t.Helper()

	lockPath := filepath.Join(projectPath, "lnpm.lock")
	_, err := os.Stat(lockPath)
	exists := err == nil

	if exists != shouldExist {
		if shouldExist {
			te.t.Errorf("Expected lockfile to exist at %s, but it doesn't", lockPath)
		} else {
			te.t.Errorf("Expected lockfile to NOT exist at %s, but it does", lockPath)
		}
	}
}

// AssertGitignore verifies .gitignore contains pattern
func (te *TestEnvironment) AssertGitignore(projectPath, pattern string, shouldExist bool) {
	te.t.Helper()

	gitignorePath := filepath.Join(projectPath, ".gitignore")
	data, err := os.ReadFile(gitignorePath)
	if err != nil {
		if shouldExist {
			te.t.Errorf("Expected .gitignore to exist and contain %s", pattern)
		}
		return
	}

	contains := strings.Contains(string(data), pattern)
	if contains != shouldExist {
		if shouldExist {
			te.t.Errorf("Expected .gitignore to contain %s, but it doesn't", pattern)
		} else {
			te.t.Errorf("Expected .gitignore to NOT contain %s, but it does", pattern)
		}
	}
}

// AssertDatabaseLink verifies link exists in database
func (te *TestEnvironment) AssertDatabaseLink(packageName, projectPath string) {
	te.t.Helper()

	pkg, err := te.Database.GetPackageByName(packageName)
	if err != nil || pkg == nil {
		te.t.Fatalf("Package %s not found in database", packageName)
	}

	proj, err := te.Database.GetProjectByPath(projectPath)
	if err != nil || proj == nil {
		te.t.Fatalf("Project %s not found in database", projectPath)
	}

	links, err := te.Database.GetLinksForProject(proj.ID)
	if err != nil {
		te.t.Fatalf("Failed to get links: %v", err)
	}

	found := false
	for _, link := range links {
		if link.PackageID == pkg.ID {
			found = true
			break
		}
	}

	if !found {
		te.t.Errorf("Expected link between %s and %s in database, but not found",
			packageName, projectPath)
	}
}

// AssertDatabaseNoLink verifies link does NOT exist in database
func (te *TestEnvironment) AssertDatabaseNoLink(packageName, projectPath string) {
	te.t.Helper()

	pkg, err := te.Database.GetPackageByName(packageName)
	if err != nil || pkg == nil {
		// Package not found is OK for this assertion
		return
	}

	proj, err := te.Database.GetProjectByPath(projectPath)
	if err != nil || proj == nil {
		// Project not found is OK for this assertion
		return
	}

	links, err := te.Database.GetLinksForProject(proj.ID)
	if err != nil {
		te.t.Fatalf("Failed to get links: %v", err)
	}

	for _, link := range links {
		if link.PackageID == pkg.ID {
			te.t.Errorf("Expected NO link between %s and %s in database, but found one",
				packageName, projectPath)
			return
		}
	}
}

// AssertDatabaseLinkType verifies the link between packageName and projectPath
// exists AND was recorded with the given type. A missing row fails: the type is
// only meaningful if there is a row carrying it, so a loop that quietly matches
// nothing would assert nothing at all.
func (te *TestEnvironment) AssertDatabaseLinkType(projectPath, packageName, wantType string) {
	te.t.Helper()

	pkg, err := te.Database.GetPackageByName(packageName)
	if err != nil || pkg == nil {
		te.t.Fatalf("Package %s not found in database: %v", packageName, err)
	}

	proj, err := te.Database.GetProjectByPath(projectPath)
	if err != nil || proj == nil {
		te.t.Fatalf("Project %s not found in database: %v", projectPath, err)
	}

	links, err := te.Database.GetLinksForProject(proj.ID)
	if err != nil {
		te.t.Fatalf("Failed to get links: %v", err)
	}

	for _, link := range links {
		if link.PackageID == pkg.ID {
			if link.LinkType != wantType {
				te.t.Errorf("Expected link type %s for %s in %s, got %s",
					wantType, packageName, projectPath, link.LinkType)
			}
			return
		}
	}

	te.t.Errorf("Expected a link between %s and %s in database, but found none",
		packageName, projectPath)
}

// AssertFilesLinked verifies package files are linked to project
func (te *TestEnvironment) AssertFilesLinked(projectPath, packageName string) {
	te.t.Helper()

	lnpmDir := filepath.Join(projectPath, ".lnpm", packageName)
	if _, err := os.Stat(lnpmDir); err != nil {
		te.t.Errorf("Expected .lnpm/%s directory to exist, but it doesn't", packageName)
	}
}

// AssertSymlinkExists verifies symlink exists in node_modules
func (te *TestEnvironment) AssertSymlinkExists(projectPath, packageName string) {
	te.t.Helper()

	// Handle scoped packages
	nodeModulesPath := filepath.Join(projectPath, "node_modules", packageName)
	linkInfo, err := os.Lstat(nodeModulesPath)
	if err != nil {
		te.t.Errorf("Expected symlink at node_modules/%s, but it doesn't exist", packageName)
		return
	}

	if linkInfo.Mode()&os.ModeSymlink == 0 {
		te.t.Errorf("Expected node_modules/%s to be a symlink, but it's not", packageName)
	}
}

// AssertSymlinkMissing verifies symlink does NOT exist in node_modules
func (te *TestEnvironment) AssertSymlinkMissing(projectPath, packageName string) {
	te.t.Helper()

	nodeModulesPath := filepath.Join(projectPath, "node_modules", packageName)
	_, err := os.Lstat(nodeModulesPath)
	if err == nil {
		te.t.Errorf("Expected NO symlink at node_modules/%s, but it exists", packageName)
	}
}

// AssertDirectoryExists verifies directory exists
func (te *TestEnvironment) AssertDirectoryExists(path string, shouldExist bool) {
	te.t.Helper()

	_, err := os.Stat(path)
	exists := err == nil

	if exists != shouldExist {
		if shouldExist {
			te.t.Errorf("Expected directory %s to exist, but it doesn't", path)
		} else {
			te.t.Errorf("Expected directory %s to NOT exist, but it does", path)
		}
	}
}

// AssertPackageInDatabase verifies package exists in database
func (te *TestEnvironment) AssertPackageInDatabase(packageName string, shouldExist bool) {
	te.t.Helper()

	pkg, err := te.Database.GetPackageByName(packageName)
	exists := err == nil && pkg != nil

	if exists != shouldExist {
		if shouldExist {
			te.t.Errorf("Expected package %s in database, but not found", packageName)
		} else {
			te.t.Errorf("Expected package %s NOT in database, but found it", packageName)
		}
	}
}

// RunConcurrently runs multiple functions concurrently and reports any errors
func RunConcurrently(t *testing.T, funcs ...func() error) {
	t.Helper()

	var wg sync.WaitGroup
	errCh := make(chan error, len(funcs))

	for _, fn := range funcs {
		wg.Add(1)
		go func(f func() error) {
			defer wg.Done()
			if err := f(); err != nil {
				errCh <- err
			}
		}(fn)
	}

	wg.Wait()
	close(errCh)

	// Check for errors
	var errors []error
	for err := range errCh {
		errors = append(errors, err)
	}

	if len(errors) > 0 {
		t.Errorf("Concurrent execution had %d error(s):", len(errors))
		for i, err := range errors {
			t.Errorf("  Error %d: %v", i+1, err)
		}
	}
}

// captureStdout runs fn with os.Stdout redirected to a pipe and returns what it
// printed. The multi-package add reports per-package failures on stdout rather
// than in its returned error, so comparing wording requires reading it.
//
// The reader goroutine starts before fn does, so fn can write more than the
// pipe buffer without deadlocking, and the teardown is deferred so os.Stdout is
// restored however fn exits: a t.Fatal inside fn would otherwise leave the rest
// of the package writing into a dead pipe.
func captureStdout(t *testing.T, fn func()) (out string) {
	t.Helper()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Failed to create pipe: %v", err)
	}

	done := make(chan string, 1)
	go func() {
		data, _ := io.ReadAll(r)
		done <- string(data)
	}()

	orig := os.Stdout
	os.Stdout = w

	defer func() {
		os.Stdout = orig
		_ = w.Close() // unblocks the reader goroutine
		out = <-done
		_ = r.Close()
	}()

	fn()
	return
}

// Helper functions

func getMapValue(m map[string]interface{}, key string) map[string]interface{} {
	if val, ok := m[key]; ok {
		if mapVal, ok := val.(map[string]interface{}); ok {
			return mapVal
		}
	}
	return make(map[string]interface{})
}

func getStringValue(m map[string]interface{}, key string) string {
	if val, ok := m[key]; ok {
		if strVal, ok := val.(string); ok {
			return strVal
		}
	}
	return ""
}

// copyDir recursively copies a directory, skipping generated artifacts
// (node_modules, .lnpm, lnpm.lock) so fixtures copy cleanly regardless of any
// leftover state from prior runs.
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip generated directories/files that must not be copied.
		if info.IsDir() && (info.Name() == "node_modules" || info.Name() == ".lnpm") {
			return filepath.SkipDir
		}
		if !info.IsDir() && info.Name() == "lnpm.lock" {
			return nil
		}
		// Skip symlinks (a fixture's source tree should contain none; any
		// present are leftover links from a previous run).
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}

		// Calculate relative path
		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		dstPath := filepath.Join(dst, relPath)

		if info.IsDir() {
			return os.MkdirAll(dstPath, info.Mode())
		}

		// Copy file
		return copyFile(path, dstPath, info.Mode())
	})
}

// copyFile copies a single file
func copyFile(src, dst string, mode os.FileMode) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() {
		_ = srcFile.Close()
	}()

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}

	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() {
		_ = dstFile.Close()
	}()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return err
	}

	return os.Chmod(dst, mode)
}

// cleanLnpmDirs recursively removes .lnpm directories
func cleanLnpmDirs(root string) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors
		}
		if info.IsDir() && info.Name() == ".lnpm" {
			_ = os.RemoveAll(path)
			return filepath.SkipDir
		}
		return nil
	})
}

// CreateTestPackageWithScripts creates a test package with custom scripts
func (te *TestEnvironment) CreateTestPackageWithScripts(name, version string, files, scripts map[string]string) string {
	te.t.Helper()

	pkgDir := filepath.Join(te.TempDir, "packages", name)
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		te.t.Fatalf("Failed to create package dir: %v", err)
	}

	// Create package.json with scripts
	pkgJSON := map[string]interface{}{
		"name":    name,
		"version": version,
	}
	if len(scripts) > 0 {
		pkgJSON["scripts"] = scripts
	}

	data, _ := json.MarshalIndent(pkgJSON, "", "  ")
	if err := os.WriteFile(filepath.Join(pkgDir, "package.json"), data, 0644); err != nil {
		te.t.Fatalf("Failed to write package.json: %v", err)
	}

	// Create additional files
	for path, content := range files {
		fullPath := filepath.Join(pkgDir, path)
		dir := filepath.Dir(fullPath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			te.t.Fatalf("Failed to create dir %s: %v", dir, err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
			te.t.Fatalf("Failed to write file %s: %v", path, err)
		}
	}

	return pkgDir
}

// AssertScriptMissing verifies script stripped from package.json at given path
func (te *TestEnvironment) AssertScriptMissing(storePath, packageName, scriptName string) {
	te.t.Helper()

	pkgJSONPath := filepath.Join(storePath, "package.json")
	data, err := os.ReadFile(pkgJSONPath)
	if err != nil {
		te.t.Fatalf("Failed to read package.json at %s: %v", pkgJSONPath, err)
	}

	var pkgJSON map[string]interface{}
	if err := json.Unmarshal(data, &pkgJSON); err != nil {
		te.t.Fatalf("Failed to parse package.json: %v", err)
	}

	scripts, ok := pkgJSON["scripts"].(map[string]interface{})
	if !ok {
		// No scripts field means all scripts are missing - OK
		return
	}

	if _, exists := scripts[scriptName]; exists {
		te.t.Errorf("Expected script '%s' to be missing from %s, but it exists", scriptName, pkgJSONPath)
	}
}

// AssertScriptExists verifies script present in package.json at given path
func (te *TestEnvironment) AssertScriptExists(storePath, packageName, scriptName string) {
	te.t.Helper()

	pkgJSONPath := filepath.Join(storePath, "package.json")
	data, err := os.ReadFile(pkgJSONPath)
	if err != nil {
		te.t.Fatalf("Failed to read package.json at %s: %v", pkgJSONPath, err)
	}

	var pkgJSON map[string]interface{}
	if err := json.Unmarshal(data, &pkgJSON); err != nil {
		te.t.Fatalf("Failed to parse package.json: %v", err)
	}

	scripts, ok := pkgJSON["scripts"].(map[string]interface{})
	if !ok {
		te.t.Errorf("Expected scripts field in package.json at %s", pkgJSONPath)
		return
	}

	if _, exists := scripts[scriptName]; !exists {
		te.t.Errorf("Expected script '%s' to exist in %s, but it doesn't", scriptName, pkgJSONPath)
	}
}

// --- Shared CLI workflow helpers ---------------------------------------------
//
// The integration tests overwhelmingly follow the same shape: create a package,
// chdir into it, publish; create a project, chdir into it, add. These helpers
// collapse that boilerplate into single calls and centralize the os.Chdir +
// error-checking so individual tests assert behavior, not plumbing.

// chdir changes into dir, failing the test on error.
func (te *TestEnvironment) chdir(dir string) {
	te.t.Helper()
	if err := os.Chdir(dir); err != nil {
		te.t.Fatalf("Failed to chdir to %s: %v", dir, err)
	}
}

// publishPkg creates a package with the given files, publishes it, and returns
// its source directory. The cwd is left inside the package directory.
func (te *TestEnvironment) publishPkg(name, version string, files map[string]string) string {
	te.t.Helper()
	pkgDir := te.CreateTestPackage(name, version, files)
	te.chdir(pkgDir)
	if err := cli.RunPublish(false, false, false, false); err != nil {
		te.t.Fatalf("Failed to publish %s: %v", name, err)
	}
	return pkgDir
}

// simplePkg publishes a package with a single index.js whose content is derived
// from the name. Convenient for the many tests that only need "some package to
// link" without caring about its contents.
func (te *TestEnvironment) simplePkg(name string) string {
	te.t.Helper()
	return te.publishPkg(name, "1.0.0", map[string]string{
		"index.js": "module.exports = '" + name + "';",
	})
}

// republish rewrites an already published package's source with a new version
// and index.js, then publishes it again WITHOUT --push, so linked projects are
// deliberately left stale. That stale state is exactly what pull has to fix.
// pkgDir need not be the directory the package was first published from -
// passing a different one republishes the same package from a new source path.
// The cwd is left inside the package directory.
func (te *TestEnvironment) republish(pkgDir, name, version, indexJS string) {
	te.t.Helper()
	te.chdir(pkgDir)
	te.writeFile(filepath.Join(pkgDir, "package.json"), `{"name":"`+name+`","version":"`+version+`"}`)
	te.writeFile(filepath.Join(pkgDir, "index.js"), indexJS)
	if err := cli.RunPublish(false, false, false, false); err != nil {
		te.t.Fatalf("Failed to republish %s@%s: %v", name, version, err)
	}
}

// newProject creates an empty project (package.json only) and chdirs into it.
func (te *TestEnvironment) newProject(name string) string {
	te.t.Helper()
	projectDir := te.CreateTestPackage(name, "1.0.0", nil)
	te.chdir(projectDir)
	return projectDir
}

// addPkg chdirs into projectDir and adds spec, failing on error.
func (te *TestEnvironment) addPkg(projectDir, spec string, dev, pure bool) {
	te.t.Helper()
	te.chdir(projectDir)
	if err := cli.RunAdd(spec, dev, pure, false); err != nil {
		te.t.Fatalf("Failed to add %s: %v", spec, err)
	}
}

// addLinkedPkg chdirs into projectDir and adds spec in live-link mode (--link),
// failing on error.
func (te *TestEnvironment) addLinkedPkg(projectDir, spec string) {
	te.t.Helper()
	te.chdir(projectDir)
	if err := cli.RunAddMultiple([]string{spec}, false, false, false, true); err != nil {
		te.t.Fatalf("Failed to add %s --link: %v", spec, err)
	}
}

// AssertLiveLink checks that .lnpm/<pkg> is a link resolving to sourceDir rather
// than a materialised store copy.
func (te *TestEnvironment) AssertLiveLink(projectDir, pkg, sourceDir string) {
	te.t.Helper()

	lnpmPath := filepath.Join(projectDir, ".lnpm", pkg)
	info, err := os.Lstat(lnpmPath)
	if err != nil {
		te.t.Fatalf("Expected .lnpm/%s to exist: %v", pkg, err)
	}
	// A store copy is a real directory. A live link is a symlink, or on Windows
	// possibly a junction, and Lstat reports neither as a directory.
	if info.IsDir() {
		te.t.Fatalf("Expected .lnpm/%s to be a live link, but it is a directory", pkg)
	}

	got, err := filepath.EvalSymlinks(lnpmPath)
	if err != nil {
		te.t.Fatalf("Failed to resolve .lnpm/%s: %v", pkg, err)
	}
	want, err := filepath.EvalSymlinks(sourceDir)
	if err != nil {
		te.t.Fatalf("Failed to resolve source %s: %v", sourceDir, err)
	}
	if got != want {
		te.t.Errorf("Expected .lnpm/%s to resolve to %s, got %s", pkg, want, got)
	}
}

// AssertStoreCopy checks that .lnpm/<pkg> is a real directory of copied files,
// which is what everything but --link produces.
func (te *TestEnvironment) AssertStoreCopy(projectDir, pkg string) {
	te.t.Helper()

	info, err := os.Lstat(filepath.Join(projectDir, ".lnpm", pkg))
	if err != nil {
		te.t.Fatalf("Expected .lnpm/%s to exist: %v", pkg, err)
	}
	if !info.IsDir() {
		te.t.Errorf("Expected .lnpm/%s to be a store copy, but it is a link", pkg)
	}
}

// AssertFileExists is AssertDirectoryExists for a regular file: same check,
// honest name and message. AssertDirectoryExists only stats, so calling it on a
// file passes, but the failure it prints then names the wrong kind of thing.
func (te *TestEnvironment) AssertFileExists(path string, shouldExist bool) {
	te.t.Helper()

	_, err := os.Stat(path)
	if exists := err == nil; exists != shouldExist {
		if shouldExist {
			te.t.Errorf("Expected file %s to exist, but it doesn't", path)
		} else {
			te.t.Errorf("Expected file %s to NOT exist, but it does", path)
		}
	}
}

// AssertFileContent checks that the file at path holds want.
func (te *TestEnvironment) AssertFileContent(path, want string) {
	te.t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		te.t.Fatalf("Failed to read %s: %v", path, err)
	}
	if string(data) != want {
		te.t.Errorf("%s: expected %q, got %q", path, want, data)
	}
}

// publishAndAdd publishes pkgName (single index.js) and adds it to a fresh
// "test-project", returning (pkgDir, projectDir). The cwd is left inside the
// project. This is the single most common setup across the suite.
func (te *TestEnvironment) publishAndAdd(pkgName string) (pkgDir, projectDir string) {
	te.t.Helper()
	pkgDir = te.simplePkg(pkgName)
	projectDir = te.newProject("test-project")
	te.addPkg(projectDir, pkgName, false, false)
	return pkgDir, projectDir
}

// AssertLinkedFileContent checks that .lnpm/<pkg>/<rel> equals want. It is
// AssertFileContent addressed the way most of the suite wants it - by project,
// package and relative path - and reads the same file for the same reason.
func (te *TestEnvironment) AssertLinkedFileContent(projectDir, pkg, rel, want string) {
	te.t.Helper()
	te.AssertFileContent(filepath.Join(projectDir, ".lnpm", pkg, rel), want)
}

// AssertStoredContentHash checks that the package's recorded content hash is
// the hash of the bytes actually sitting in the store, file by file. Anything
// that changes a file's contents between packing and storing - a specifier
// rewrite, say - has to be reflected in both or the store entry is addressed by
// a hash of content it does not hold.
//
// The invariant only holds for packages without a prepare or prepublish script:
// store.stripLifecycleScripts re-marshals the stored package.json after the
// hash is taken, so for those the stored bytes legitimately differ from the
// hashed ones and this helper would report a mismatch it did not cause.
func (te *TestEnvironment) AssertStoredContentHash(packageName string) {
	te.t.Helper()

	pkg, err := te.Database.GetPackageByName(packageName)
	if err != nil || pkg == nil {
		te.t.Fatalf("Package %s not found in database: %v", packageName, err)
	}

	entries, err := te.Database.GetFilesForPackage(pkg.ID)
	if err != nil {
		te.t.Fatalf("Failed to get file manifest for %s: %v", packageName, err)
	}
	if len(entries) == 0 {
		te.t.Fatalf("Expected a file manifest for %s, got none", packageName)
	}

	data := make([]pack.FileEntryData, len(entries))
	for i, e := range entries {
		// pack.HashFile rather than a local xxhash of the bytes: the manifest
		// records what pack produced, so the check has to be pack's own.
		got, err := pack.HashFile(filepath.Join(pkg.StorePath, e.RelativePath))
		if err != nil {
			te.t.Fatalf("Failed to hash stored %s: %v", e.RelativePath, err)
		}
		if got != e.ContentHash {
			te.t.Errorf("Stored %s hashes to %s, but the manifest records %s",
				e.RelativePath, got, e.ContentHash)
		}
		data[i] = pack.FileEntryData{
			RelPath: e.RelativePath,
			Size:    e.Size,
			Mode:    e.Mode,
			Hash:    e.ContentHash,
		}
	}

	if got := pack.HashFiles(pack.FileInfoFromStore(pkg.StorePath, data)); got != pkg.ContentHash {
		te.t.Errorf("Expected content hash %s for %s, got %s", pkg.ContentHash, packageName, got)
	}
}

// storedPackageJSONPath returns the path of a published package's package.json
// inside the isolated store.
func (te *TestEnvironment) storedPackageJSONPath(packageName string) string {
	te.t.Helper()

	pkg, err := te.Database.GetPackageByName(packageName)
	if err != nil || pkg == nil {
		te.t.Fatalf("Package %s not found in database: %v", packageName, err)
	}
	return filepath.Join(pkg.StorePath, "package.json")
}

// writeFile writes content to path (relative to cwd or absolute), failing on error.
func (te *TestEnvironment) writeFile(path, content string) {
	te.t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		te.t.Fatalf("Failed to write %s: %v", path, err)
	}
}

// fileIdentity returns a FileInfo describing the file at path as it is now, so
// that os.SameFile still answers about that file after path has been replaced.
//
// os.Stat is not enough, because os.SameFile is not everywhere the eager
// comparison it looks like. On unix a FileInfo carries the inode from the moment
// it was taken. On Windows the fast path of os.stat calls GetFileAttributesEx,
// which returns no file index, so os.fileStat keeps the path instead
// (saveInfoFromPath) and os.SameFile resolves it to a volume and file index only
// when it is called (loadFileId, which opens the path afresh). A FileInfo
// captured before a push would therefore describe whatever the push left at that
// path, and a before/after comparison would report the same file however much
// had changed - which makes the "this file was rewritten" half of the assertion
// unfalsifiable and the "this file was not" half true for the wrong reason.
//
// Statting through an open file closes that gap on every platform: Windows takes
// that FileInfo from GetFileInformationByHandle, which fills the volume and file
// index in immediately and leaves nothing for os.SameFile to resolve later, and
// unix does an fstat on the same descriptor. Either way the identity is the one
// the file had when it was read.
func fileIdentity(t *testing.T, path string) os.FileInfo {
	t.Helper()

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("Failed to open %s: %v", path, err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		t.Fatalf("Failed to stat %s: %v", path, err)
	}
	return info
}

// writePackageJSON writes a package.json into projectDir from the given map.
func (te *TestEnvironment) writePackageJSON(projectDir string, content map[string]interface{}) {
	te.t.Helper()
	data, err := json.MarshalIndent(content, "", "  ")
	if err != nil {
		te.t.Fatalf("Failed to marshal package.json: %v", err)
	}
	te.writeFile(filepath.Join(projectDir, "package.json"), string(data))
}

// storedPackageJSON loads and parses the package.json at the given store/.lnpm
// path, failing on error.
func (te *TestEnvironment) storedPackageJSON(dir string) map[string]interface{} {
	te.t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		te.t.Fatalf("Failed to read package.json at %s: %v", dir, err)
	}
	var pkgJSON map[string]interface{}
	if err := json.Unmarshal(data, &pkgJSON); err != nil {
		te.t.Fatalf("Failed to parse package.json at %s: %v", dir, err)
	}
	return pkgJSON
}
