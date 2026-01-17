package tests

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/db"
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

	// Set LNPM_STORE env var for isolated store
	originalStore := os.Getenv("LNPM_STORE")
	if err := os.Setenv("LNPM_STORE", storeDir); err != nil {
		t.Fatalf("Failed to set LNPM_STORE: %v", err)
	}

	// Cleanup: restore env and directory
	t.Cleanup(func() {
		_ = os.Chdir(originalDir)
		if originalStore != "" {
			_ = os.Setenv("LNPM_STORE", originalStore)
		} else {
			_ = os.Unsetenv("LNPM_STORE")
		}
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

// CopyFixture copies a fixture directory to a temp location
func (te *TestEnvironment) CopyFixture(name string) string {
	te.t.Helper()

	src := filepath.Join(te.OriginalDir, "fixtures", name)
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

// copyDir recursively copies a directory
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
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
