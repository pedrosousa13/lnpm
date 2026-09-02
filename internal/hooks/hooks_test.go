package hooks

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/config"
)

// orderLogName is the file each test lifecycle script appends its own name to.
const orderLogName = "order.log"

// requireNpm skips the calling test when npm is not on PATH. RunPrepare shells
// out to `npm run <script>`, so without npm the script tests fail rather than
// exercise the hook (e.g. CI without Node).
func requireNpm(t *testing.T) {
	t.Helper()

	if _, err := exec.LookPath("npm"); err != nil {
		t.Skip("npm not found in PATH; skipping prepare-hook tests")
	}
}

// logFor returns a script that appends the hook name to one shared log file, so
// a test can prove which scripts ran and in which order. It goes through node
// rather than shell built-ins because npm runs scripts under cmd.exe on Windows,
// where `>>` appends a trailing space and `;` is not a command separator. node is
// already required here, so this costs nothing.
func logFor(name string) string {
	return `node -e "require('fs').appendFileSync('` + orderLogName + `','` + name + `\n')"`
}

// writePackageJSON writes a minimal package.json carrying scripts into dir.
func writePackageJSON(t *testing.T, dir string, scripts map[string]string) {
	t.Helper()

	data, err := json.MarshalIndent(map[string]interface{}{
		"name":    "test-pkg",
		"version": "1.0.0",
		"scripts": scripts,
	}, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal package.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), data, 0644); err != nil {
		t.Fatalf("failed to write package.json: %v", err)
	}
}

// readOrderLog returns the script names recorded in dir's order log, in the
// order they ran. A missing log means no script ran.
func readOrderLog(t *testing.T, dir string) []string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(dir, orderLogName))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("failed to read order log: %v", err)
	}

	return strings.Fields(string(data))
}

// TestRunPrepareFollowsNpmScriptOrder pins the sequence RunPrepare runs the
// publish lifecycle scripts in, by observing the scripts themselves rather than
// reading publishScripts back. See publishScripts for the order, the npm
// measurement behind it, and why getting it wrong fails silently.
//
// It sits outside TestRunPrepare's table on purpose. The table varies which
// scripts a package defines; this varies nothing and asserts one thing, so the
// sequence is stated somewhere a reader editing hooks.go will find it.
func TestRunPrepareFollowsNpmScriptOrder(t *testing.T) {
	requireNpm(t)

	dir := t.TempDir()
	writePackageJSON(t, dir, map[string]string{
		"prepack":        logFor("prepack"),
		"prepare":        logFor("prepare"),
		"prepublishOnly": logFor("prepublishOnly"),
	})

	if err := RunPrepare(dir, false); err != nil {
		t.Fatalf("RunPrepare returned an error: %v", err)
	}

	want := []string{"prepublishOnly", "prepack", "prepare"}
	if got := readOrderLog(t, dir); !slices.Equal(got, want) {
		t.Errorf("scripts ran %v, want %v", got, want)
	}
}

// TestRunPrepareForPackSkipsPrepublishOnly pins the set push runs: what
// `npm pack` runs, which is prepack and prepare. prepublishOnly is npm's
// publish-only script, so a package that keeps a registry-only build or a slow
// gate there must not see it fire on every push.
func TestRunPrepareForPackSkipsPrepublishOnly(t *testing.T) {
	requireNpm(t)

	dir := t.TempDir()
	writePackageJSON(t, dir, map[string]string{
		"prepack":        logFor("prepack"),
		"prepare":        logFor("prepare"),
		"prepublishOnly": logFor("prepublishOnly"),
	})

	if err := RunPrepareForPack(dir, false); err != nil {
		t.Fatalf("RunPrepareForPack returned an error: %v", err)
	}

	want := []string{"prepack", "prepare"}
	if got := readOrderLog(t, dir); !slices.Equal(got, want) {
		t.Errorf("scripts ran %v, want %v", got, want)
	}
}

func TestRunPrepare(t *testing.T) {
	requireNpm(t)

	// failAfterLog returns a script that logs its own name and then fails.
	failAfterLog := func(name string) string {
		return `node -e "require('fs').appendFileSync('` + orderLogName + `','` + name + `\n');process.exit(1)"`
	}

	tests := []struct {
		name      string
		scripts   map[string]string
		skipHooks bool
		wantRun   []string // scripts that should run, in order (nil if none)
		wantError bool
	}{
		{
			name: "runs prepare script",
			scripts: map[string]string{
				"prepare": logFor("prepare"),
			},
			wantRun: []string{"prepare"},
		},
		{
			name: "runs prepublishOnly if it is the only script",
			scripts: map[string]string{
				"prepublishOnly": logFor("prepublishOnly"),
			},
			wantRun: []string{"prepublishOnly"},
		},
		{
			name: "runs prepublishOnly before prepare",
			scripts: map[string]string{
				"prepare":        logFor("prepare"),
				"prepublishOnly": logFor("prepublishOnly"),
			},
			wantRun: []string{"prepublishOnly", "prepare"},
		},
		{
			name: "runs prepack if no prepare",
			scripts: map[string]string{
				"prepack": logFor("prepack"),
			},
			wantRun: []string{"prepack"},
		},
		{
			name: "skips when skipHooks=true",
			scripts: map[string]string{
				"prepare": logFor("prepare"),
			},
			skipHooks: true,
			wantRun:   nil,
		},
		{
			name:      "no scripts is ok",
			scripts:   map[string]string{},
			wantRun:   nil,
			wantError: false,
		},
		{
			name: "failing script returns error",
			scripts: map[string]string{
				"prepare": "exit 1",
			},
			wantError: true,
		},
		{
			name: "stops at the first failing script",
			scripts: map[string]string{
				"prepublishOnly": failAfterLog("prepublishOnly"),
				"prepare":        logFor("prepare"),
				"prepack":        logFor("prepack"),
			},
			wantError: true,
			wantRun:   []string{"prepublishOnly"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temp directory
			tmpDir := t.TempDir()
			writePackageJSON(t, tmpDir, tt.scripts)

			// Run prepare
			err := RunPrepare(tmpDir, tt.skipHooks)

			if tt.wantError {
				if err == nil {
					t.Errorf("expected error but got nil")
				}
			} else if err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			// The shared log records exactly which scripts ran, in order, so
			// comparing the whole sequence checks both membership and ordering.
			if got := readOrderLog(t, tmpDir); !slices.Equal(got, tt.wantRun) {
				t.Errorf("scripts ran %v, want %v", got, tt.wantRun)
			}
		})
	}
}

// useHooksConfig points config.Get at a temp config file holding hooks for the
// rest of the test. The loaded config is memoized package-wide, so it is
// cleared both now and on cleanup: otherwise this test would either read
// whatever config another test loaded first, or pin its own on every test after
// it.
func useHooksConfig(t *testing.T, hooks config.HooksConfig) {
	t.Helper()

	t.Setenv("LNPM_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	if err := config.SaveConfig(&config.Config{Hooks: hooks}); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	config.ResetForTesting()
	t.Cleanup(config.ResetForTesting)
}

// ranScript is one command a hook handed to runScriptFn, and the directory it
// asked for it to run in.
type ranScript struct {
	dir string
	cmd string
}

// recordScripts replaces runScriptFn for the rest of the test with one that
// records what it is handed and runs none of it, so a post-add test can check
// what would have run without starting a real package-manager install. The
// returned function reports the recorded scripts, in order.
//
// Don't use t.Parallel() in callers - this helper swaps the process-wide
// runScriptFn var, so a caller must also not run alongside a test that does.
func recordScripts(t *testing.T) func() []ranScript {
	t.Helper()

	var ran []ranScript
	prev := runScriptFn
	runScriptFn = func(dir string, cmdStr string) error {
		ran = append(ran, ranScript{dir: dir, cmd: cmdStr})
		return nil
	}
	t.Cleanup(func() { runScriptFn = prev })

	return func() []ranScript { return ran }
}

func TestRunPostAdd(t *testing.T) {
	tests := []struct {
		name       string
		hooks      config.HooksConfig
		runInstall bool
		createNPM  bool     // create package-lock.json to trigger npm
		wantRan    []string // commands that should run (nil if none)
	}{
		{
			name:       "skips when runInstall=false",
			runInstall: false,
		},
		{
			name:       "skips npm detection when runInstall=false",
			createNPM:  true,
			runInstall: false, // don't run install
		},
		{
			name:       "runs the package manager install when runInstall=true",
			createNPM:  true,
			runInstall: true,
			wantRan:    []string{"npm install --legacy-peer-deps"},
		},
		{
			name:       "runs the custom post_add hook when runInstall=true",
			hooks:      config.HooksConfig{PostAdd: "echo added"},
			runInstall: true,
			wantRan:    []string{"echo added"},
		},
		{
			name:       "skip_post_add suppresses the package manager install",
			hooks:      config.HooksConfig{SkipPostAdd: true},
			createNPM:  true,
			runInstall: true,
		},
		{
			name:       "skip_post_add suppresses the custom post_add hook",
			hooks:      config.HooksConfig{PostAdd: "echo added", SkipPostAdd: true},
			runInstall: true,
		},
		{
			name:       "runInstall=false still skips the custom post_add hook",
			hooks:      config.HooksConfig{PostAdd: "echo added"},
			runInstall: false,
		},
		{
			name:       "runInstall=false skips regardless of skip_post_add",
			hooks:      config.HooksConfig{PostAdd: "echo added", SkipPostAdd: true},
			createNPM:  true,
			runInstall: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()

			if tt.createNPM {
				// Create package-lock.json to indicate npm usage
				lockPath := filepath.Join(tmpDir, "package-lock.json")
				if err := os.WriteFile(lockPath, []byte("{}"), 0644); err != nil {
					t.Fatalf("failed to create package-lock.json: %v", err)
				}
			}

			useHooksConfig(t, tt.hooks)
			ran := recordScripts(t)

			if err := RunPostAdd(tmpDir, tt.runInstall); err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			// Every hook runs in the project directory it was given, so check
			// that alongside the commands themselves.
			var cmds []string
			for _, r := range ran() {
				cmds = append(cmds, r.cmd)
				if r.dir != tmpDir {
					t.Errorf("%q ran in %q, want the project path %q", r.cmd, r.dir, tmpDir)
				}
			}

			if !slices.Equal(cmds, tt.wantRan) {
				t.Errorf("commands ran %v, want %v", cmds, tt.wantRan)
			}
		})
	}
}

func TestRunCustom(t *testing.T) {
	tests := []struct {
		name      string
		cmd       string
		wantError bool
	}{
		{
			name: "runs custom command",
			cmd:  "echo 'test'",
		},
		{
			name: "empty command is ok",
			cmd:  "",
		},
		{
			name:      "failing command returns error",
			cmd:       "exit 1",
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()

			err := RunCustom(tmpDir, tt.cmd, "test")

			if tt.wantError {
				if err == nil {
					t.Errorf("expected error but got nil")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}
