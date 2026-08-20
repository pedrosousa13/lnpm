// Package e2e contains real end-to-end tests for lnpm.
//
// Unlike the unit/integration tests in the tests package (which call cli.RunX
// functions in-process), these tests exercise the REAL compiled lnpm binary
// and a REAL node runtime against realistic monorepo layouts (pnpm, Turborepo,
// Nx, npm/yarn workspaces). The goal is to prove that a consumer app actually
// resolves and runs the linked package through the node_modules symlink, and
// that `lnpm push` propagates source changes to linked projects.
//
// These tests are self-contained: every helper lives in this package and does
// not depend on the tests package (which a concurrent refactor may be changing).
package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

var (
	// lnpmBin is the absolute path to the lnpm binary built once in TestMain.
	lnpmBin string

	// nodeAvailable reports whether a node runtime was found on PATH. When
	// false, every test t.Skip()s rather than failing — node is required for
	// real resolution but its absence must not fail a local run. In CI its
	// absence is fatal instead; see TestMain.
	nodeAvailable bool
)

// TestMain builds the lnpm binary once for the whole package and probes for a
// node runtime. node is required for these tests; npm is not (resolution only
// needs the node_modules symlink, which lnpm creates directly).
func TestMain(m *testing.M) {
	if _, err := exec.LookPath("node"); err != nil {
		// Skipping is friendly on a developer machine without node, but in CI
		// it turns the whole suite green by skipping it: a broken node setup
		// would then look exactly like a passing run. CI must fail instead.
		if os.Getenv("CI") == "true" {
			fmt.Println("e2e: node is required in CI but was not found on PATH")
			os.Exit(1)
		}
		nodeAvailable = false
		fmt.Println("e2e: node not found on PATH; e2e tests will be skipped")
	} else {
		nodeAvailable = true
	}

	binDir, err := os.MkdirTemp("", "lnpm-e2e-bin-")
	if err != nil {
		fmt.Printf("e2e: failed to create temp bin dir: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(binDir)

	binName := "lnpm"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	lnpmBin = filepath.Join(binDir, binName)

	// Resolve the repo root relative to this test file: tests/e2e -> ../..
	repoRoot, err := repoRootDir()
	if err != nil {
		fmt.Printf("e2e: failed to resolve repo root: %v\n", err)
		os.Exit(1)
	}

	build := exec.Command("go", "build", "-o", lnpmBin, "./cmd/lnpm")
	build.Dir = repoRoot
	build.Env = os.Environ()
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Printf("e2e: failed to build lnpm binary: %v\n%s\n", err, out)
		os.Exit(1)
	}

	os.Exit(m.Run())
}

// repoRootDir returns the repository root, resolved relative to this source
// file (tests/e2e/main_test.go -> ../..). Using the source location rather than
// the working directory keeps it stable regardless of where `go test` is run.
func repoRootDir() (string, error) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("could not determine caller for repo root")
	}
	// thisFile = <repo>/tests/e2e/main_test.go
	return filepath.Abs(filepath.Join(filepath.Dir(thisFile), "..", ".."))
}
