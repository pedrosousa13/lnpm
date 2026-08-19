package db

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

// lockMessage is the phrase users should see when the database is already held
// by a concurrent lnpm invocation.
const lockMessage = "another lnpm process"

// shortenOpenTimeout points initDB at a timeout the test can afford to wait
// out, and restores the production value afterwards.
func shortenOpenTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	previous := openTimeout
	openTimeout = d
	t.Cleanup(func() { openTimeout = previous })
}

// holdDatabaseLock opens the store's bbolt file directly, standing in for a
// second lnpm process that already holds the flock. bbolt locks per open file
// description, so a separate handle in this process conflicts exactly the way
// a separate process would.
func holdDatabaseLock(t *testing.T, storeDir string) *bolt.DB {
	t.Helper()
	holder, err := bolt.Open(filepath.Join(storeDir, "lnpm.db"), 0600, &bolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("Failed to take the database lock: %v", err)
	}
	t.Cleanup(func() { _ = holder.Close() })
	return holder
}

// TestGetDB_LockHeldElsewhere_NamesTheOtherProcess checks that a database the
// process cannot lock within the timeout is reported as a concurrency problem
// rather than a bare "timeout".
func TestGetDB_LockHeldElsewhere_NamesTheOtherProcess(t *testing.T) {
	ResetForTesting()
	storeDir := t.TempDir()
	t.Setenv("LNPM_STORE", storeDir)
	t.Cleanup(ResetForTesting)

	holdDatabaseLock(t, storeDir)
	shortenOpenTimeout(t, 200*time.Millisecond)

	_, err := GetDB()
	if err == nil {
		t.Fatal("Expected an error opening a database locked by another handle")
	}
	if !strings.Contains(err.Error(), lockMessage) {
		t.Errorf("Expected error to mention %q, got: %v", lockMessage, err)
	}
}

// TestGetDB_WaitsOutALockHeldLongerThanASecond checks the production timeout is
// generous enough that a second lnpm invocation waits for an in-flight one
// instead of dying almost immediately. It deliberately uses the production
// openTimeout so that shrinking it back re-breaks this test.
func TestGetDB_WaitsOutALockHeldLongerThanASecond(t *testing.T) {
	ResetForTesting()
	storeDir := t.TempDir()
	t.Setenv("LNPM_STORE", storeDir)
	t.Cleanup(ResetForTesting)

	holder := holdDatabaseLock(t, storeDir)
	time.AfterFunc(1500*time.Millisecond, func() { _ = holder.Close() })

	database, err := GetDB()
	if err != nil {
		t.Fatalf("Expected the open to wait for the lock to be released, got: %v", err)
	}
	if database == nil {
		t.Fatal("Expected a database instance")
	}
}

// TestGetDB_NonTimeoutFailure_DoesNotBlameAnotherProcess pins the error
// classification: an open that fails for a reason other than the lock must not
// be reported as a concurrent lnpm invocation.
func TestGetDB_NonTimeoutFailure_DoesNotBlameAnotherProcess(t *testing.T) {
	ResetForTesting()
	storeDir := t.TempDir()
	t.Setenv("LNPM_STORE", storeDir)
	t.Cleanup(ResetForTesting)

	// A non-empty file that is not a bbolt database fails meta-page validation.
	// Nothing holds the lock, so this cannot be a timeout.
	dbPath := filepath.Join(storeDir, "lnpm.db")
	if err := os.WriteFile(dbPath, []byte(strings.Repeat("not a bbolt database", 256)), 0600); err != nil {
		t.Fatalf("Failed to write invalid database file: %v", err)
	}
	shortenOpenTimeout(t, 200*time.Millisecond)

	_, err := GetDB()
	if err == nil {
		t.Fatal("Expected an error opening an invalid database file")
	}
	if strings.Contains(err.Error(), lockMessage) {
		t.Errorf("Expected error not to blame another lnpm process, got: %v", err)
	}
}
