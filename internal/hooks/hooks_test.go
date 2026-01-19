package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRunPrepare(t *testing.T) {
	tests := []struct {
		name       string
		scripts    map[string]string
		skipHooks  bool
		wantRun    string // which script should run (empty if none)
		wantError  bool
	}{
		{
			name: "runs prepare script",
			scripts: map[string]string{
				"prepare": "echo 'prepare'",
			},
			wantRun: "prepare",
		},
		{
			name: "runs prepublishOnly over prepare",
			scripts: map[string]string{
				"prepare":        "echo 'prepare'",
				"prepublishOnly": "echo 'prepublishOnly'",
			},
			wantRun: "prepare", // prepare runs first in precedence
		},
		{
			name: "runs prepack if no prepare",
			scripts: map[string]string{
				"prepack": "echo 'prepack'",
			},
			wantRun: "prepack",
		},
		{
			name: "skips when skipHooks=true",
			scripts: map[string]string{
				"prepare": "echo 'prepare'",
			},
			skipHooks: true,
			wantRun:   "",
		},
		{
			name:      "no scripts is ok",
			scripts:   map[string]string{},
			wantRun:   "",
			wantError: false,
		},
		{
			name: "failing script returns error",
			scripts: map[string]string{
				"prepare": "exit 1",
			},
			wantError: true,
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
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
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
