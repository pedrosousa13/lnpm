package e2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestNpmPackRunsPrepackBeforePrepare re-measures the claim internal/hooks
// pins: npm runs prepack before prepare, so lnpm does too.
//
// It exists because that claim is otherwise a comment. hooks.RunPrepare's order
// is parity with npm, and parity claims rot silently — npm can change, and the
// only way to find out is to ask it. This asks the npm actually installed
// alongside the suite, so the day the answer changes, a test says so instead of
// a user finding a stale build in a tarball.
//
// It runs `npm pack`, not `npm publish --dry-run`, on purpose. Both orders were
// measured by hand against npm 11.16.0 (publish --dry-run gives prepublishOnly,
// prepack, prepare; pack gives prepack, prepare), but only pack is fully local:
// a dry-run publish still resolves the registry, which would tie this suite to
// the network and to npm auth. pack settles prepack-before-prepare on its own,
// and that is the half a wrong order breaks without an error.
//
// The package is deliberately dependency-free, so `npm pack` needs no install
// and no network.
func TestNpmPackRunsPrepackBeforePrepare(t *testing.T) {
	t.Parallel()
	if !nodeAvailable {
		t.Skip("node not available; skipping real-npm lifecycle test")
	}
	// npm is not required by the rest of this package (resolution only needs
	// the symlink lnpm writes), so its absence skips rather than fails.
	if _, err := exec.LookPath("npm"); err != nil {
		t.Skip("npm not found in PATH; skipping real-npm lifecycle test")
	}

	dir := t.TempDir()
	const orderLog = "order.log"
	logFor := func(name string) string {
		return `node -e "require('fs').appendFileSync('` + orderLog + `','` + name + `\n')"`
	}

	manifest, err := json.MarshalIndent(map[string]interface{}{
		"name":    "lifecycle-order-probe",
		"version": "1.0.0",
		"scripts": map[string]string{
			"prepack":  logFor("prepack"),
			"prepare":  logFor("prepare"),
			"postpack": logFor("postpack"),
		},
	}, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal package.json: %v", err)
	}
	writeFile(t, filepath.Join(dir, "package.json"), string(manifest))

	cmd := exec.Command("npm", "pack", "--silent")
	cmd.Dir = dir
	// npm's update notifier, audit and funding messages all reach out to the
	// network. None of them affect the answer, so turn them off rather than
	// let a slow or offline machine turn this into a flake.
	cmd.Env = append(os.Environ(),
		"npm_config_update_notifier=false",
		"npm_config_audit=false",
		"npm_config_fund=false",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("npm pack failed: %v\n%s", err, out)
	}

	data, err := os.ReadFile(filepath.Join(dir, orderLog))
	if err != nil {
		t.Fatalf("failed to read the lifecycle log npm's scripts wrote: %v", err)
	}

	// postpack is included so a run that recorded nothing after prepare is told
	// apart from one where npm stopped early: lnpm does not run postpack, but
	// npm does, and its presence confirms the pack got all the way through.
	got := strings.Fields(string(data))
	want := []string{"prepack", "prepare", "postpack"}
	if !slices.Equal(got, want) {
		t.Errorf("npm pack ran %v, want %v\n"+
			"npm's lifecycle order has changed; internal/hooks.publishScripts follows it and must be re-checked", got, want)
	}
}
