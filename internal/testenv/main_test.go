package testenv

import (
	"os"
	"testing"
)

// TestMain redirects the config loader at a temp directory before the first
// test runs, so no test in this package can read the machine's own
// ~/.lnpm/config.yaml (#371).
//
// This package holds the mechanism, and testenv.go imports internal/config, so
// TestConfigIsolationCoversEveryPackage names this package too. Satisfying its
// own rule is the point: nothing here is exempt.
func TestMain(m *testing.M) {
	os.Exit(Run(m))
}
