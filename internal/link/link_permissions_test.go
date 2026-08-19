package link

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/pack"
)

// TestLink_ReadOnlyDestination tests linking to read-only destination
func TestLink_ReadOnlyDestination(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping on Windows - read-only behavior differs")
	}

	tmpDir := t.TempDir()
	projectDir := filepath.Join(tmpDir, "project")

	// Create read-only project directory
	if err := os.MkdirAll(projectDir, 0555); err != nil {
		t.Fatalf("Failed to create project dir: %v", err)
	}
	defer func() {
		_ = os.Chmod(projectDir, 0755) // Cleanup
	}()

	// Create store
	storeDir := filepath.Join(tmpDir, "store", "test-pkg")
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		t.Fatalf("Failed to create store dir: %v", err)
	}

	// Create test file in store
	testFile := filepath.Join(storeDir, "index.js")
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	files := []pack.FileEntryData{
		{RelPath: "index.js", Size: 4, Mode: 0644, Hash: "test123"},
	}
	fileInfos := pack.FileInfoFromStore(storeDir, files)

	// Try to link - should fail
	linker := New(projectDir)
	_, err := linker.Link("test-pkg", storeDir, fileInfos)
	if err == nil {
		t.Error("Expected error linking to read-only destination")
	}
}

// TestLink_PreservesExecutablePermissions tests that executable permissions are preserved
func TestLink_PreservesExecutablePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping on Windows - permission handling differs")
	}

	tmpDir := t.TempDir()
	projectDir := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatalf("Failed to create project dir: %v", err)
	}

	// Create store with executable file
	storeDir := filepath.Join(tmpDir, "store", "exec-pkg")
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		t.Fatalf("Failed to create store dir: %v", err)
	}

	// Create executable file
	execFile := filepath.Join(storeDir, "bin", "script")
	if err := os.MkdirAll(filepath.Dir(execFile), 0755); err != nil {
		t.Fatalf("Failed to create bin dir: %v", err)
	}
	if err := os.WriteFile(execFile, []byte("#!/bin/sh\necho test"), 0755); err != nil {
		t.Fatalf("Failed to create exec file: %v", err)
	}

	files := []pack.FileEntryData{
		{RelPath: "bin/script", Size: 20, Mode: 0755, Hash: "exec123"},
	}
	fileInfos := pack.FileInfoFromStore(storeDir, files)

	// Link
	linker := New(projectDir)
	_, err := linker.Link("exec-pkg", storeDir, fileInfos)
	if err != nil {
		t.Fatalf("Failed to link: %v", err)
	}

	// Verify executable permission preserved in linked file
	linkedFile := filepath.Join(projectDir, ".lnpm", "exec-pkg", "bin", "script")
	info, err := os.Stat(linkedFile)
	if err != nil {
		t.Fatalf("Failed to stat linked file: %v", err)
	}

	mode := info.Mode() & 0777
	if mode&0111 == 0 {
		t.Errorf("Executable permission not preserved, got %o", mode)
	}
}

// TestLink_DirectoryPermissions tests that directories are created with correct permissions
func TestLink_DirectoryPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping on Windows - permission handling differs")
	}

	tmpDir := t.TempDir()
	projectDir := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatalf("Failed to create project dir: %v", err)
	}

	// Create store
	storeDir := filepath.Join(tmpDir, "store", "test-pkg")
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		t.Fatalf("Failed to create store dir: %v", err)
	}

	// Create nested file
	nestedFile := filepath.Join(storeDir, "lib", "utils", "helper.js")
	if err := os.MkdirAll(filepath.Dir(nestedFile), 0755); err != nil {
		t.Fatalf("Failed to create nested dir: %v", err)
	}
	if err := os.WriteFile(nestedFile, []byte("helper"), 0644); err != nil {
		t.Fatalf("Failed to create file: %v", err)
	}

	files := []pack.FileEntryData{
		{RelPath: "lib/utils/helper.js", Size: 6, Mode: 0644, Hash: "helper123"},
	}
	fileInfos := pack.FileInfoFromStore(storeDir, files)

	// Link
	linker := New(projectDir)
	_, err := linker.Link("test-pkg", storeDir, fileInfos)
	if err != nil {
		t.Fatalf("Failed to link: %v", err)
	}

	// Check .lnpm directory permissions
	lnpmDir := filepath.Join(projectDir, ".lnpm")
	info, err := os.Stat(lnpmDir)
	if err != nil {
		t.Fatalf("Failed to stat .lnpm dir: %v", err)
	}

	mode := info.Mode() & 0777
	expectedMode := os.FileMode(0755)
	if mode != expectedMode {
		t.Errorf("Expected .lnpm mode %o, got %o", expectedMode, mode)
	}

	// Check nested directory permissions
	nestedDir := filepath.Join(projectDir, ".lnpm", "test-pkg", "lib", "utils")
	info, err = os.Stat(nestedDir)
	if err != nil {
		t.Fatalf("Failed to stat nested dir: %v", err)
	}

	mode = info.Mode() & 0777
	if mode != expectedMode {
		t.Errorf("Expected nested dir mode %o, got %o", expectedMode, mode)
	}
}

// TestLink_PackageDirectoryRespectsUmask tests that the package directory Link
// leaves behind has the mode a plain os.MkdirAll(path, 0755) would produce
// under the same umask. Link builds it as a temp directory and renames it into
// place, and the temp directory must not be created in a way that bypasses the
// umask (os.MkdirTemp's fixed 0700, or a Chmod, which ignores the umask and
// would force 0755 no matter how restrictive the umask is).
func TestLink_PackageDirectoryRespectsUmask(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping on Windows - permission handling differs")
	}

	tmpDir := t.TempDir()
	projectDir := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatalf("Failed to create project dir: %v", err)
	}

	storeDir := filepath.Join(tmpDir, "store", "test-pkg")
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		t.Fatalf("Failed to create store dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(storeDir, "index.js"), []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	files := []pack.FileEntryData{
		{RelPath: "index.js", Size: 4, Mode: 0644, Hash: "test123"},
	}
	fileInfos := pack.FileInfoFromStore(storeDir, files)

	// Reference directory, created the way Link used to create the package
	// directory. Comparing against it pins umask-relative behaviour rather
	// than a hardcoded mode.
	refDir := filepath.Join(tmpDir, "reference")
	if err := os.MkdirAll(refDir, 0755); err != nil {
		t.Fatalf("Failed to create reference dir: %v", err)
	}
	refInfo, err := os.Stat(refDir)
	if err != nil {
		t.Fatalf("Failed to stat reference dir: %v", err)
	}

	linker := New(projectDir)
	if _, err := linker.Link("test-pkg", storeDir, fileInfos); err != nil {
		t.Fatalf("Failed to link: %v", err)
	}

	pkgInfo, err := os.Stat(filepath.Join(projectDir, ".lnpm", "test-pkg"))
	if err != nil {
		t.Fatalf("Failed to stat package dir: %v", err)
	}

	if pkgInfo.Mode().Perm() != refInfo.Mode().Perm() {
		t.Errorf("Expected .lnpm/test-pkg mode %o (as os.MkdirAll(path, 0755) yields under this umask), got %o",
			refInfo.Mode().Perm(), pkgInfo.Mode().Perm())
	}
}

// TestLink_NodeModulesPermissions tests node_modules symlink creation permissions
func TestLink_NodeModulesPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping on Windows - symlink handling differs")
	}

	tmpDir := t.TempDir()
	projectDir := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatalf("Failed to create project dir: %v", err)
	}

	// Create store
	storeDir := filepath.Join(tmpDir, "store", "test-pkg")
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		t.Fatalf("Failed to create store dir: %v", err)
	}

	testFile := filepath.Join(storeDir, "index.js")
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	files := []pack.FileEntryData{
		{RelPath: "index.js", Size: 4, Mode: 0644, Hash: "test123"},
	}
	fileInfos := pack.FileInfoFromStore(storeDir, files)

	// Link
	linker := New(projectDir)
	_, err := linker.Link("test-pkg", storeDir, fileInfos)
	if err != nil {
		t.Fatalf("Failed to link: %v", err)
	}

	// Check node_modules directory permissions
	nodeModulesDir := filepath.Join(projectDir, "node_modules")
	info, err := os.Stat(nodeModulesDir)
	if err != nil {
		t.Fatalf("Failed to stat node_modules: %v", err)
	}

	mode := info.Mode() & 0777
	expectedMode := os.FileMode(0755)
	if mode != expectedMode {
		t.Errorf("Expected node_modules mode %o, got %o", expectedMode, mode)
	}

	// Check symlink exists
	symlinkPath := filepath.Join(nodeModulesDir, "test-pkg")
	if _, err := os.Lstat(symlinkPath); err != nil {
		t.Errorf("Symlink doesn't exist: %v", err)
	}
}

// TestLink_ScopedPackageDirectoryPermissions tests scoped package directory creation
func TestLink_ScopedPackageDirectoryPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping on Windows - permission handling differs")
	}

	tmpDir := t.TempDir()
	projectDir := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatalf("Failed to create project dir: %v", err)
	}

	// Create store
	storeDir := filepath.Join(tmpDir, "store", "@org", "scoped-pkg")
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		t.Fatalf("Failed to create store dir: %v", err)
	}

	testFile := filepath.Join(storeDir, "index.js")
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	files := []pack.FileEntryData{
		{RelPath: "index.js", Size: 4, Mode: 0644, Hash: "test123"},
	}
	fileInfos := pack.FileInfoFromStore(storeDir, files)

	// Link scoped package
	linker := New(projectDir)
	_, err := linker.Link("@org/scoped-pkg", storeDir, fileInfos)
	if err != nil {
		t.Fatalf("Failed to link: %v", err)
	}

	// Check @org directory permissions in node_modules
	orgDir := filepath.Join(projectDir, "node_modules", "@org")
	info, err := os.Stat(orgDir)
	if err != nil {
		t.Fatalf("Failed to stat @org dir: %v", err)
	}

	mode := info.Mode() & 0777
	expectedMode := os.FileMode(0755)
	if mode != expectedMode {
		t.Errorf("Expected @org dir mode %o, got %o", expectedMode, mode)
	}
}

// TestLink_ExistingReadOnlyFile tests linking when destination has read-only files
func TestLink_ExistingReadOnlyFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping on Windows - read-only behavior differs")
	}

	tmpDir := t.TempDir()
	projectDir := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatalf("Failed to create project dir: %v", err)
	}

	// Create .lnpm with existing read-only file
	lnpmDir := filepath.Join(projectDir, ".lnpm", "test-pkg")
	if err := os.MkdirAll(lnpmDir, 0755); err != nil {
		t.Fatalf("Failed to create lnpm dir: %v", err)
	}
	existingFile := filepath.Join(lnpmDir, "index.js")
	if err := os.WriteFile(existingFile, []byte("old"), 0444); err != nil {
		t.Fatalf("Failed to create existing file: %v", err)
	}

	// Create store
	storeDir := filepath.Join(tmpDir, "store", "test-pkg")
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		t.Fatalf("Failed to create store dir: %v", err)
	}

	newFile := filepath.Join(storeDir, "index.js")
	if err := os.WriteFile(newFile, []byte("new"), 0644); err != nil {
		t.Fatalf("Failed to create new file: %v", err)
	}

	files := []pack.FileEntryData{
		{RelPath: "index.js", Size: 3, Mode: 0644, Hash: "new123"},
	}
	fileInfos := pack.FileInfoFromStore(storeDir, files)

	// Link populates a temp dir and renames it over the existing .lnpm/{pkg},
	// so the stale read-only file is replaced with the new content.
	linker := New(projectDir)
	if _, err := linker.Link("test-pkg", storeDir, fileInfos); err != nil {
		t.Fatalf("Link should replace existing read-only file, got error: %v", err)
	}
	content, err := os.ReadFile(existingFile)
	if err != nil {
		t.Fatalf("Failed to read linked file: %v", err)
	}
	if string(content) != "new" {
		t.Errorf("Expected linked file content %q, got %q", "new", string(content))
	}
}

// TestUnlink_ReadOnlyFiles tests unlinking read-only files
func TestUnlink_ReadOnlyFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping on Windows - read-only behavior differs")
	}

	tmpDir := t.TempDir()
	projectDir := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatalf("Failed to create project dir: %v", err)
	}

	// Create linked package with read-only files
	lnpmDir := filepath.Join(projectDir, ".lnpm", "test-pkg")
	if err := os.MkdirAll(lnpmDir, 0755); err != nil {
		t.Fatalf("Failed to create lnpm dir: %v", err)
	}

	readOnlyFile := filepath.Join(lnpmDir, "readonly.js")
	if err := os.WriteFile(readOnlyFile, []byte("readonly"), 0444); err != nil {
		t.Fatalf("Failed to create readonly file: %v", err)
	}

	// Create symlink
	nodeModulesDir := filepath.Join(projectDir, "node_modules")
	if err := os.MkdirAll(nodeModulesDir, 0755); err != nil {
		t.Fatalf("Failed to create node_modules: %v", err)
	}
	symlinkPath := filepath.Join(nodeModulesDir, "test-pkg")
	if err := os.Symlink("../.lnpm/test-pkg", symlinkPath); err != nil {
		t.Fatalf("Failed to create symlink: %v", err)
	}

	// Unlink removes the .lnpm/{pkg} tree (RemoveAll handles read-only files
	// since their parent dir is writable) and the node_modules symlink.
	linker := New(projectDir)
	if err := linker.Unlink("test-pkg"); err != nil {
		t.Fatalf("Unlink should remove read-only files, got error: %v", err)
	}
	if _, err := os.Stat(lnpmDir); !os.IsNotExist(err) {
		t.Errorf("Expected .lnpm/test-pkg to be removed, stat err = %v", err)
	}
	if _, err := os.Lstat(symlinkPath); !os.IsNotExist(err) {
		t.Errorf("Expected node_modules symlink to be removed, lstat err = %v", err)
	}
}

// TestLink_PreservesVariousPermissions tests preserving different file permissions
func TestLink_PreservesVariousPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping on Windows - permission handling differs")
	}

	tmpDir := t.TempDir()
	projectDir := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatalf("Failed to create project dir: %v", err)
	}

	// Create store with files of various permissions
	storeDir := filepath.Join(tmpDir, "store", "perm-pkg")
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		t.Fatalf("Failed to create store dir: %v", err)
	}

	testCases := []struct {
		name string
		mode os.FileMode
	}{
		{"file-644.js", 0644},
		{"file-755.sh", 0755},
		{"file-600.key", 0600},
		{"file-640.conf", 0640},
	}

	var files []pack.FileEntryData
	for _, tc := range testCases {
		filePath := filepath.Join(storeDir, tc.name)
		if err := os.WriteFile(filePath, []byte("content"), tc.mode); err != nil {
			t.Fatalf("Failed to create %s: %v", tc.name, err)
		}
		files = append(files, pack.FileEntryData{
			RelPath: tc.name,
			Size:    7,
			Mode:    tc.mode,
			Hash:    "hash-" + tc.name,
		})
	}

	fileInfos := pack.FileInfoFromStore(storeDir, files)

	// Link
	linker := New(projectDir)
	_, err := linker.Link("perm-pkg", storeDir, fileInfos)
	if err != nil {
		t.Fatalf("Failed to link: %v", err)
	}

	// Verify each file's permissions
	for _, tc := range testCases {
		linkedFile := filepath.Join(projectDir, ".lnpm", "perm-pkg", tc.name)
		info, err := os.Stat(linkedFile)
		if err != nil {
			t.Errorf("Failed to stat %s: %v", tc.name, err)
			continue
		}

		actualMode := info.Mode() & 0777
		if actualMode != tc.mode {
			t.Errorf("File %s: expected mode %o, got %o", tc.name, tc.mode, actualMode)
		}
	}
}
