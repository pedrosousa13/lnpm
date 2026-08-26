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

// TestNpmPackRunsPrepackBeforePrepare re-measures against the installed npm the
// order hooks.publishScripts follows. That declaration holds the measurement and
// the argument for it; this test exists because a measurement written down once
// rots silently. npm can change, and the only way to find out is to ask it.
//
// It asks with `npm pack`, not `npm publish --dry-run`, even though the latter
// covers all three scripts: a dry-run publish still resolves the registry, which
// would tie this suite to the network and to npm auth. pack settles
// prepack-before-prepare on its own, which is the part of the order lnpm can get
// wrong without anything failing. The probe package has no dependencies, so pack
// needs no install and no network at all.
func TestNpmPackRunsPrepackBeforePrepare(t *testing.T) {
	t.Parallel()
	if !nodeAvailable {
		t.Skip("node not available; skipping real-npm lifecycle test")
	}
	// npm is not required by the rest of this package (resolution only needs
	// the symlink lnpm writes), so its absence skips on a developer machine.
	// Not in CI: this test's whole job is to notice when npm's answer changes,
	// and a skipped test and a passing one look the same in a run summary, so a
	// CI box without npm would retire the canary without saying so. TestMain
	// makes the same call for node, for the same reason.
	if _, err := exec.LookPath("npm"); err != nil {
		if os.Getenv("CI") == "true" {
			t.Fatal("npm is required in CI but was not found on PATH")
		}
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
