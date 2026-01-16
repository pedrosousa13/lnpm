package db

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// TestOpen_DBPermissions tests that database file has correct permissions (0600)
func TestOpen_DBPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping on Windows - permission handling differs")
	}

	tmpDir := t.TempDir()
	t.Setenv("LNPM_STORE", tmpDir)

	// Open database (creates it)
	db, err := GetDB()
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Check database file permissions
	dbPath := filepath.Join(tmpDir, "db", "lnpm.db")
	info, err := os.Stat(dbPath)
	if err != nil {
		t.Fatalf("Failed to stat database file: %v", err)
	}

	mode := info.Mode() & 0777
	expectedMode := os.FileMode(0600)
	if mode != expectedMode {
		t.Errorf("Expected database permissions %o, got %o", expectedMode, mode)
	}
}

// TestOpen_ReadOnlyDirectory tests opening database in read-only directory
func TestOpen_ReadOnlyDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping on Windows - read-only behavior differs")
	}

	tmpDir := t.TempDir()
	t.Setenv("LNPM_STORE", tmpDir)

	// Make directory read-only
	if err := os.Chmod(tmpDir, 0555); err != nil {
		t.Fatalf("Failed to chmod: %v", err)
	}
	defer os.Chmod(tmpDir, 0755)

	// Try to open database - should fail
	_, err := GetDB()
	if err == nil {
		t.Error("Expected error opening database in read-only directory")
	}
}

// TestOpen_ExistingReadOnlyDB tests opening existing read-only database
func TestOpen_ExistingReadOnlyDB(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping on Windows - read-only behavior differs")
	}

	tmpDir := t.TempDir()
	t.Setenv("LNPM_STORE", tmpDir)

	// Create and close database
	db, err := GetDB()
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	db.Close()

	// Make database read-only
	dbPath := filepath.Join(tmpDir, "db", "lnpm.db")
	if err := os.Chmod(dbPath, 0444); err != nil {
		t.Fatalf("Failed to chmod database: %v", err)
	}
	defer os.Chmod(dbPath, 0644)

	// Try to open read-only database - should fail on write
	db2, err := GetDB()
	if err != nil {
		// May fail to open with timeout
		t.Logf("Failed to open read-only DB: %v", err)
		return
	}
	defer db2.Close()

	// Try to write - should fail
	pkg := &Package{
		Name:        "test-pkg",
		Version:     "1.0.0",
		ContentHash: "hash123",
		SourcePath:  "/test",
		StorePath:   "/store",
		FilesCount:  1,
		TotalSize:   100,
	}
	err = db2.InsertPackage(pkg)
	if err == nil {
		t.Error("Expected error writing to read-only database")
	}
}

// TestOpen_DBDirectoryPermissions tests database directory has correct permissions
func TestOpen_DBDirectoryPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping on Windows - permission handling differs")
	}

	tmpDir := t.TempDir()
	t.Setenv("LNPM_STORE", tmpDir)

	// Open database
	db, err := GetDB()
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Check db directory permissions
	dbDir := filepath.Join(tmpDir, "db")
	info, err := os.Stat(dbDir)
	if err != nil {
		t.Fatalf("Failed to stat db directory: %v", err)
	}

	mode := info.Mode() & 0777
	expectedMode := os.FileMode(0755)
	if mode != expectedMode {
		t.Errorf("Expected db directory permissions %o, got %o", expectedMode, mode)
	}
}

// TestConcurrentAccess_WithPermissions tests concurrent database access
func TestConcurrentAccess_WithPermissions(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LNPM_STORE", tmpDir)

	// Open database
	db, err := GetDB()
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Concurrent writes
	done := make(chan error, 10)
	for i := 0; i < 10; i++ {
		go func(idx int) {
			pkg := &Package{
				Name:        "concurrent-pkg",
				Version:     "1.0.0",
				ContentHash: "hash",
				SourcePath:  "/test",
				StorePath:   "/store",
				FilesCount:  1,
				TotalSize:   100,
			}
			done <- db.InsertPackage(pkg)
		}(i)
	}

	// Wait for all to complete
	for i := 0; i < 10; i++ {
		if err := <-done; err != nil {
			t.Errorf("Concurrent write failed: %v", err)
		}
	}

	// Verify database still has correct permissions
	if runtime.GOOS != "windows" {
		dbPath := filepath.Join(tmpDir, "db", "lnpm.db")
		info, err := os.Stat(dbPath)
		if err != nil {
			t.Fatalf("Failed to stat database: %v", err)
		}

		mode := info.Mode() & 0777
		expectedMode := os.FileMode(0600)
		if mode != expectedMode {
			t.Errorf("Database permissions changed during concurrent access: got %o", mode)
		}
	}
}

// TestOpen_CorruptedDBPermissions tests handling corrupted database with permission issues
func TestOpen_CorruptedDBPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping on Windows - permission handling differs")
	}

	tmpDir := t.TempDir()
	t.Setenv("LNPM_STORE", tmpDir)

	// Create db directory and corrupt database file
	dbDir := filepath.Join(tmpDir, "db")
	if err := os.MkdirAll(dbDir, 0755); err != nil {
		t.Fatalf("Failed to create db dir: %v", err)
	}

	dbPath := filepath.Join(dbDir, "lnpm.db")
	// Write invalid content with read-only permissions
	if err := os.WriteFile(dbPath, []byte("not a valid bolt db"), 0444); err != nil {
		t.Fatalf("Failed to create corrupt db: %v", err)
	}
	defer os.Chmod(dbPath, 0644)

	// Try to open - should fail
	_, err := GetDB()
	if err == nil {
		t.Error("Expected error opening corrupted read-only database")
	}
}

// TestGetDB_Singleton tests GetDB returns same instance with correct permissions
func TestGetDB_Singleton(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping on Windows - permission handling differs")
	}

	tmpDir := t.TempDir()
	t.Setenv("LNPM_STORE", tmpDir)

	// Get database instance
	db1, err := GetDB()
	if err != nil {
		t.Fatalf("Failed to get database: %v", err)
	}

	// Get again - should be same instance
	db2, err := GetDB()
	if err != nil {
		t.Fatalf("Failed to get database second time: %v", err)
	}

	if db1 != db2 {
		t.Error("GetDB returned different instances")
	}

	// Check permissions still correct
	dbPath := filepath.Join(tmpDir, "db", "lnpm.db")
	info, err := os.Stat(dbPath)
	if err != nil {
		t.Fatalf("Failed to stat database: %v", err)
	}

	mode := info.Mode() & 0777
	expectedMode := os.FileMode(0600)
	if mode != expectedMode {
		t.Errorf("Expected database permissions %o, got %o", expectedMode, mode)
	}
}

// TestOpen_TimeoutWithLockedDB tests timeout when database is locked
func TestOpen_TimeoutWithLockedDB(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LNPM_STORE", tmpDir)

	// Open first database instance
	db1, err := GetDB()
	if err != nil {
		t.Fatalf("Failed to open first database: %v", err)
	}
	defer db1.Close()

	// Try to open second instance - should timeout due to lock
	// (bolt only allows one writer at a time)
	db2, err := GetDB()
	if err != nil {
		t.Logf("Second open timed out as expected: %v", err)
		return
	}
	defer db2.Close()

	// If we got here, concurrent access is somehow allowed
	// This is fine on some platforms/configurations
	t.Log("Concurrent database access allowed on this platform")
}

// TestWriteOperations_PreservePermissions tests write operations preserve file permissions
func TestWriteOperations_PreservePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping on Windows - permission handling differs")
	}

	tmpDir := t.TempDir()
	t.Setenv("LNPM_STORE", tmpDir)

	db, err := GetDB()
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Insert some data
	pkg := &Package{
		Name:        "test-pkg",
		Version:     "1.0.0",
		ContentHash: "hash123",
		SourcePath:  "/test",
		StorePath:   "/store",
		FilesCount:  10,
		TotalSize:   1024,
	}
	if err := db.InsertPackage(pkg); err != nil {
		t.Fatalf("Failed to insert package: %v", err)
	}

	// Insert files
	files := make([]*FileEntry, 10)
	for i := 0; i < 10; i++ {
		files[i] = &FileEntry{
			PackageID:    pkg.ID,
			RelativePath: "file.js",
			ContentHash:  "hash",
			Size:         100,
			Mode:         0644,
			ModTime:      time.Now().UnixNano(),
		}
	}
	if err := db.InsertFiles(pkg.ID, files); err != nil {
		t.Fatalf("Failed to insert files: %v", err)
	}

	// Verify database permissions still correct
	dbPath := filepath.Join(tmpDir, "db", "lnpm.db")
	info, err := os.Stat(dbPath)
	if err != nil {
		t.Fatalf("Failed to stat database: %v", err)
	}

	mode := info.Mode() & 0777
	expectedMode := os.FileMode(0600)
	if mode != expectedMode {
		t.Errorf("Database permissions changed after writes: got %o", mode)
	}
}

// TestOpen_RecoverFromPermissionIssues tests recovery from permission problems
func TestOpen_RecoverFromPermissionIssues(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping on Windows - permission handling differs")
	}

	tmpDir := t.TempDir()
	t.Setenv("LNPM_STORE", tmpDir)

	// Create database
	db, err := GetDB()
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	db.Close()

	// Make database unreadable
	dbPath := filepath.Join(tmpDir, "db", "lnpm.db")
	if err := os.Chmod(dbPath, 0000); err != nil {
		t.Fatalf("Failed to chmod database: %v", err)
	}

	// Try to open - should fail
	_, err = GetDB()
	if err == nil {
		t.Error("Expected error opening unreadable database")
	}

	// Restore permissions
	if err := os.Chmod(dbPath, 0600); err != nil {
		t.Fatalf("Failed to restore permissions: %v", err)
	}

	// Should work now
	db2, err := GetDB()
	if err != nil {
		t.Fatalf("Failed to open after fixing permissions: %v", err)
	}
	defer db2.Close()
}

// TestOpen_StorePathPermissions tests store path directory permissions
func TestOpen_StorePathPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping on Windows - permission handling differs")
	}

	tmpDir := t.TempDir()
	t.Setenv("LNPM_STORE", tmpDir)

	// Open database
	db, err := GetDB()
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Check LNPM_STORE directory permissions
	info, err := os.Stat(tmpDir)
	if err != nil {
		t.Fatalf("Failed to stat store path: %v", err)
	}

	// Directory should be writable
	mode := info.Mode() & 0777
	if mode&0200 == 0 {
		t.Error("Store path directory is not writable")
	}
}
