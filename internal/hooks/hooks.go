package hooks

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/pedrosousa13/lnpm/internal/config"
	"github.com/pedrosousa13/lnpm/internal/debug"
	"github.com/pedrosousa13/lnpm/internal/shellcmd"
)

// publishScripts is the set of lifecycle scripts lnpm runs before packing, held
// in the order it runs them. The order is npm's, not one lnpm chose: it was
// measured against npm 11.16.0 with a package.json defining all three scripts,
// each appending its own name to one shared log.
//
//	npm publish --dry-run  ->  prepublishOnly, prepack, prepare
//	npm pack               ->  prepack, prepare
//
// prepack before prepare is the half worth guarding. Swap the two and a prepare
// script that reads what prepack produced reads the previous run's bytes
// instead; nothing exits non-zero to say so, and the packed tarball is quietly
// one revision behind. So the order is pinned by a test that names the sequence
// (TestRunPrepareFollowsNpmScriptOrder) rather than left implied by this
// literal, and npm itself is re-measured in tests/e2e
// (TestNpmPackRunsPrepackBeforePrepare) so the claim above cannot go stale
// without a test noticing.
//
// lnpm runs all three on both its publish and its push path. The measurement
// above settles the order of the scripts, not which of them npm would run for
// any one of its own commands.
var publishScripts = []string{"prepublishOnly", "prepack", "prepare"}

// RunPrepare runs prepare scripts before publishing
// Executes every prepublishOnly, prepack and prepare script present in
// package.json, always in that order, and stops at the first one that fails.
// See publishScripts for why that is the order.
func RunPrepare(pkgPath string, skipHooks bool) error {
	// Check if hooks should be skipped
	cfg := config.Get()
	if skipHooks || cfg.Hooks.SkipPrepare {
		debug.Log("hooks: skipping prepare scripts (disabled)")
		return nil
	}

	// Read package.json to get scripts
	pkgJSONPath := filepath.Join(pkgPath, "package.json")
	data, err := os.ReadFile(pkgJSONPath)
	if err != nil {
		return fmt.Errorf("failed to read package.json: %w", err)
	}

	var pkgJSON struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(data, &pkgJSON); err != nil {
		return fmt.Errorf("failed to parse package.json: %w", err)
	}

	// Run every applicable script, in lnpm's publish order
	ran := false
	for _, scriptName := range publishScripts {
		if _, exists := pkgJSON.Scripts[scriptName]; exists {
			fmt.Printf("Running %s script...\n", scriptName)
			debug.Logf("hooks: running %s via npm", scriptName)

			// Run via npm to get proper PATH with node_modules/.bin
			if err := runNpmScript(pkgPath, scriptName); err != nil {
				return fmt.Errorf("%s script failed: %w", scriptName, err)
			}

			ran = true
		}
	}

	if !ran {
		debug.Log("hooks: no prepare scripts found")
	}
	return nil
}

// RunPostAdd runs post-add hook after adding a package
// Only runs if explicitly requested via --install flag (matches yalc behavior)
func RunPostAdd(projectPath string, runInstall bool) error {
	if !runInstall {
		debug.Log("hooks: skipping post-add install (not requested)")
		return nil
	}

	cfg := config.Get()
	if cfg.Hooks.SkipPostAdd {
		debug.Log("hooks: skipping post-add (disabled via config)")
		return nil
	}

	// Use custom hook if configured
	if cfg.Hooks.PostAdd != "" {
		fmt.Println("Running post-add hook...")
		debug.Logf("hooks: running post_add: %s", cfg.Hooks.PostAdd)
		return runScriptFn(projectPath, cfg.Hooks.PostAdd)
	}

	// Default: run package manager install
	pm := config.DetectPackageManager(projectPath)
	installCmd := config.GetInstallCommand(pm)

	fmt.Printf("Running %s to resolve dependencies...\n", installCmd)
	debug.Logf("hooks: running install: %s", installCmd)

	return runScriptFn(projectPath, installCmd)
}

// RunCustom runs a custom hook command
func RunCustom(dir string, cmd string, hookName string) error {
	if cmd == "" {
		return nil
	}

	fmt.Printf("Running %s hook...\n", hookName)
	debug.Logf("hooks: running %s: %s", hookName, cmd)

	return runScript(dir, cmd)
}

// runNpmScript runs a package.json script via npm run (proper PATH with node_modules/.bin)
func runNpmScript(dir string, scriptName string) error {
	// Detect package manager
	pm := config.DetectPackageManager(dir)

	var cmd *exec.Cmd
	switch pm {
	case config.Bun:
		cmd = exec.Command("bun", "run", scriptName)
	case config.PNPM:
		cmd = exec.Command("pnpm", "run", scriptName)
	case config.Yarn:
		cmd = exec.Command("yarn", "run", scriptName)
	default:
		cmd = exec.Command("npm", "run", scriptName)
	}

	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("command failed: %w\n\nHint: To skip this script, use --skip-hooks", err)
	}

	return nil
}

// runScriptFn is runScript behind a variable so a test can check which command
// RunPostAdd decided on without running it: that path ends in a real
// package-manager install, which a test must never start. Only RunPostAdd goes
// through it. Production code never reassigns it.
var runScriptFn = runScript

// runScript executes a shell command in the specified directory
func runScript(dir string, cmdStr string) error {
	cmd := shellcmd.Command(cmdStr)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("command failed: %w\n\nHint: To skip this script, use --skip-hooks", err)
	}

	return nil
}
