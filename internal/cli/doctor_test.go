package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/config"
	"github.com/pedrosousa13/lnpm/internal/db"
)

// TestRunDoctorChecksConfiguredStorePath pins that doctor inspects the store
// resolved from store_path, not the ~/.lnpm default. The configured directory
// is deliberately absent so doctor names it in its "does not exist" line: that
// makes the assertion key on the configured path regardless of whether ~/.lnpm
// happens to exist on the machine running the test.
func TestRunDoctorChecksConfiguredStorePath(t *testing.T) {
	want := filepath.Join(newDoctorStoreConfig(t), "mystore")

	out := captureDoctorStdout(t)

	if !strings.Contains(out, "Store directory does not exist: "+want) {
		t.Errorf("RunDoctor did not check the configured store %q, output was:\n%s", want, out)
	}
	if home, err := os.UserHomeDir(); err == nil {
		if def := filepath.Join(home, ".lnpm"); strings.Contains(out, def) {
			t.Errorf("RunDoctor named the default store %q instead of the configured one, output was:\n%s", def, out)
		}
	}
}

// TestRunDoctorReportsConfiguredStoreHealthy is the acceptance criterion read
// the other way round: with the configured store present, doctor must not
// report it missing.
func TestRunDoctorReportsConfiguredStoreHealthy(t *testing.T) {
	want := filepath.Join(newDoctorStoreConfig(t), "mystore")
	if err := os.MkdirAll(want, 0755); err != nil {
		t.Fatal(err)
	}

	out := captureDoctorStdout(t)

	if strings.Contains(out, "NOT FOUND") {
		t.Errorf("RunDoctor reported the existing configured store %q as missing, output was:\n%s", want, out)
	}
}

// TestRunDoctorPrefersEnvStoreOverConfig pins that LNPM_STORE still wins over
// store_path, so doctor keeps the resolution order the rest of lnpm uses.
func TestRunDoctorPrefersEnvStoreOverConfig(t *testing.T) {
	dir := newDoctorStoreConfig(t)
	fromConfig := filepath.Join(dir, "mystore")
	fromEnv := filepath.Join(dir, "from-env")
	t.Setenv("LNPM_STORE", fromEnv)

	out := captureDoctorStdout(t)

	if !strings.Contains(out, "Store directory does not exist: "+fromEnv) {
		t.Errorf("RunDoctor did not check the LNPM_STORE path %q, output was:\n%s", fromEnv, out)
	}
	if strings.Contains(out, fromConfig) {
		t.Errorf("RunDoctor checked the configured store %q even though LNPM_STORE was set, output was:\n%s", fromConfig, out)
	}
}

// newDoctorStoreConfig writes a config file setting store_path to a "mystore"
// directory inside a fresh temp dir, points LNPM_CONFIG at it and returns the
// temp dir. The configured directory itself is not created, so the caller
// decides whether doctor should find it.
//
// config caches the parsed file for the process, so the cache is dropped both
// before the test (another test in this package may have populated it already)
// and after it (so this test's config does not leak into the next one).
func newDoctorStoreConfig(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	storePath := filepath.Join(dir, "mystore")
	if err := os.WriteFile(cfgPath, []byte("store_path: "+storePath+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("LNPM_CONFIG", cfgPath)
	t.Setenv("LNPM_STORE", "") // empty is treated as unset, so config wins

	config.ResetForTesting()
	t.Cleanup(config.ResetForTesting)

	return dir
}

// captureDoctorStdout runs RunDoctor with os.Stdout redirected to a pipe and
// returns what it printed. RunDoctor reports every check on stdout rather than
// through its return value, so the findings are only readable this way.
//
// The reader goroutine starts before RunDoctor does, so it can write more than
// the pipe buffer without deadlocking, and the teardown is deferred so
// os.Stdout is restored however RunDoctor exits.
//
// RunDoctor opens the database, which is cached for the process and would keep
// a file handle inside the test's temp dir, so it is released on the way out.
func captureDoctorStdout(t *testing.T) (out string) {
	t.Helper()

	db.ResetForTesting()
	t.Cleanup(db.ResetForTesting)

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

	if err := RunDoctor(); err != nil {
		t.Errorf("RunDoctor() error = %v", err)
	}
	return
}
