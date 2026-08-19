package hooks

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// orderLogName is the file each test lifecycle script appends its own name to.
const orderLogName = "order.log"

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

func TestRunPrepare(t *testing.T) {
	// RunPrepare shells out to `npm run <script>` for any matched script.
	// Without npm on PATH the script tests fail rather than exercise the hook,
	// so skip them when npm is unavailable (e.g. CI without Node).
	if _, err := exec.LookPath("npm"); err != nil {
		t.Skip("npm not found in PATH; skipping prepare-hook tests")
	}

	// logFor returns a script that appends the hook name to one shared log file,
	// so the test can prove which scripts ran and in which order. It goes
	// through node rather than shell built-ins because npm runs scripts under
	// cmd.exe on Windows, where `>>` appends a trailing space and `;` is not a
	// command separator. node is already required here, so this costs nothing.
	logFor := func(name string) string {
		return `node -e "require('fs').appendFileSync('` + orderLogName + `','` + name + `\n')"`
	}

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
			name: "runs every publish script in order",
			scripts: map[string]string{
				"prepack":        logFor("prepack"),
				"prepare":        logFor("prepare"),
				"prepublishOnly": logFor("prepublishOnly"),
			},
			wantRun: []string{"prepublishOnly", "prepare", "prepack"},
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

			// Write package.json with scripts
			pkgJSON := map[string]interface{}{
				"name":    "test-pkg",
				"version": "1.0.0",
				"scripts": tt.scripts,
			}
			data, err := json.MarshalIndent(pkgJSON, "", "  ")
			if err != nil {
				t.Fatalf("failed to marshal package.json: %v", err)
			}
			pkgJSONPath := filepath.Join(tmpDir, "package.json")
			if err := os.WriteFile(pkgJSONPath, data, 0644); err != nil {
				t.Fatalf("failed to write package.json: %v", err)
			}

			// Run prepare
			err = RunPrepare(tmpDir, tt.skipHooks)

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

func TestRunPostAdd(t *testing.T) {
	tests := []struct {
		name       string
		runInstall bool
		createNPM  bool // create package-lock.json to trigger npm
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

			err := RunPostAdd(tmpDir, tt.runInstall)

			// When runInstall=false, should not error (skips install)
			if !tt.runInstall && err != nil {
				t.Errorf("unexpected error when runInstall=false: %v", err)
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
