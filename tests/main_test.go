package tests

import (
	"os"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/testenv"
)

// TestMain redirects the config loader at a temp directory before the first
// test runs, so no test in this package can read the machine's own
// ~/.lnpm/config.yaml (#371).
func TestMain(m *testing.M) {
	os.Exit(testenv.Run(m))
}
