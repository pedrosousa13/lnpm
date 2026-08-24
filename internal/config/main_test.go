package config

import (
	"fmt"
	"os"
	"testing"
)

// TestMain redirects the loader at a temp directory before the first test runs,
// so no test in this package reads the machine's own ~/.lnpm/config.yaml (#371).
//
// Every other package gets this from internal/testenv. This one cannot import
// it - testenv imports config, and these tests are in package config because
// they call loadConfigFile - so it does the same setup inline.
//
// The tests that pin the $HOME fallback and the LNPM_CONFIG override still
// work: t.Setenv replaces what this set for the length of the test and puts it
// back afterwards.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "lnpm-test-config-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: create temp config dir: %v\n", err)
		os.Exit(1)
	}
	IsolateForTesting(dir)

	// The exit code is held in a variable rather than passed straight to
	// os.Exit, so that RemoveAll runs before the process ends: os.Exit never
	// returns, so anything after it in the call would not run.
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
