// Package testenv holds the process-wide setup an lnpm test binary needs before
// its first test runs.
//
// Only _test.go files import it. That is what lets this package import
// "testing" where internal/config cannot: nothing in a production build links
// it, so the weight testing adds never reaches cmd/lnpm.
package testenv

import (
	"fmt"
	"os"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/config"
)

// Run isolates the config loader from the machine's own ~/.lnpm/config.yaml,
// runs the package's tests, and returns the exit code for TestMain to pass to
// os.Exit:
//
//	func TestMain(m *testing.M) { os.Exit(testenv.Run(m)) }
//
// Returning the code instead of exiting here is what lets the temp directory be
// removed. os.Exit skips deferred functions, so the defer has to sit in a frame
// that returns before it.
//
// Isolating once per binary, rather than once per test, is deliberate: #371 is
// about the tests nobody remembered to isolate, and a TestMain covers the ones
// written after it.
//
// A test that wants settings of its own still redirects LNPM_CONFIG itself, and
// that takes two calls, not one. t.Setenv does replace the path for the length
// of the test and put back what this function set afterwards, but LoadConfig
// memoises the parsed file behind a package-level sync.Once for the life of the
// binary: once anything in the package has loaded it, a later read answers from
// that cache and never consults LNPM_CONFIG again. The redirect must therefore
// be paired with config.ResetForTesting(), and with
// t.Cleanup(config.ResetForTesting) so the following test does not inherit
// these settings. internal/link's useConfig and internal/cli's newStatusStore
// do both and explain it at the call site.
//
// Only reads that go through the memoised loader need the pairing.
// GetConfigPath and SaveConfig call getConfigPath on every invocation, which is
// why three of internal/cli/config_test.go's four redirects, the ones driving
// setConfigKey and showConfig, need no reset.
//
// The fourth is worth reading before copying it. It drives editConfig, which
// stats the path, finds nothing there, and calls config.Get() to write a
// starter file. That populates the sync.Once from the redirected path, and the
// cached value outlives t.Setenv's restore. It does no harm only because
// nothing exists at that path when Get() runs, so what lands in the cache is
// the same defaults any later read would have produced, and the two helpers in
// that package that do want settings, newDoctorStoreConfig and newStatusStore,
// drop the cache before and after themselves. That is a property of those
// tests, checked, not a rule. Pairing the two calls is what makes a redirect
// correct without anyone having to check.
//
// internal/config cannot use this - it would be an import cycle - so its
// TestMain calls config.IsolateForTesting directly.
func Run(m *testing.M) int {
	dir, err := os.MkdirTemp("", "lnpm-test-config-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "testenv: create temp config dir: %v\n", err)
		return 1
	}
	defer os.RemoveAll(dir)

	config.IsolateForTesting(dir)

	return m.Run()
}
