package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPrintPeerDependencyTipNamesTheProjectsInstallCommand pins #384: the tip
// used to say 'npm install' whatever the project was, sending a pnpm or yarn
// user to a command that rewrites the wrong lock file.
//
// Each case is a directory holding at most one lock file and nothing else - the
// last row holds none at all - and the directory is passed in rather than
// chdir'd into. Passing it in is deliberate: the
// helper takes a project path precisely so it does not read the process working
// directory. Measured against a helper rewritten to call os.Getwd() instead,
// which answers npm for every row because the package's own source directory
// has no lock file: the pnpm, yarn and bun rows go red, and the two npm rows
// stay green. Those two therefore say nothing about which directory was read.
func TestPrintPeerDependencyTipNamesTheProjectsInstallCommand(t *testing.T) {
	tests := []struct {
		name     string
		lockfile string
		want     string
	}{
		{name: "pnpm", lockfile: "pnpm-lock.yaml", want: "Run 'pnpm install' if you need to resolve peer dependencies"},
		{name: "yarn", lockfile: "yarn.lock", want: "Run 'yarn install' if you need to resolve peer dependencies"},
		{name: "bun", lockfile: "bun.lockb", want: "Run 'bun install' if you need to resolve peer dependencies"},
		// npm carries --legacy-peer-deps because that is the command lnpm runs
		// for an npm project; config.GetInstallCommand adds it for npm/cli#2199,
		// the bug that breaks peer dependency resolution over file: deps.
		{name: "npm", lockfile: "package-lock.json", want: "Run 'npm install --legacy-peer-deps' if you need to resolve peer dependencies"},
		// No lock file at all is npm by DetectPackageManager's default, so the
		// tip has to answer for a project that has never been installed.
		{name: "no lock file", lockfile: "", want: "Run 'npm install --legacy-peer-deps' if you need to resolve peer dependencies"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.lockfile != "" {
				if err := os.WriteFile(filepath.Join(dir, tc.lockfile), []byte("{}\n"), 0644); err != nil {
					t.Fatalf("write %s: %v", tc.lockfile, err)
				}
			}

			out := captureStdout(t, func() { printPeerDependencyTip(dir) })

			if !strings.Contains(out, tc.want) {
				t.Errorf("printPeerDependencyTip(%s project) = %q, want it to contain %q", tc.name, out, tc.want)
			}
		})
	}
}

func TestShouldProceed(t *testing.T) {
	tests := []struct {
		name        string
		interactive bool
		yes         bool
		want        confirmDecision
		wantMsg     bool
	}{
		{
			name:        "non-interactive without yes aborts with a message",
			interactive: false,
			yes:         false,
			want:        decisionAbort,
			wantMsg:     true,
		},
		{
			name:        "non-interactive with yes proceeds",
			interactive: false,
			yes:         true,
			want:        decisionProceed,
		},
		{
			name:        "interactive without yes asks the user",
			interactive: true,
			yes:         false,
			want:        decisionAsk,
		},
		{
			name:        "interactive with yes proceeds without asking",
			interactive: true,
			yes:         true,
			want:        decisionProceed,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, msg := shouldProceed(tc.interactive, tc.yes)
			if got != tc.want {
				t.Fatalf("shouldProceed(%t, %t) = %v, want %v", tc.interactive, tc.yes, got, tc.want)
			}
			if tc.wantMsg {
				if !strings.Contains(msg, "--yes") {
					t.Fatalf("shouldProceed(%t, %t) msg = %q, want it to mention --yes", tc.interactive, tc.yes, msg)
				}
			} else if msg != "" {
				t.Fatalf("shouldProceed(%t, %t) msg = %q, want empty", tc.interactive, tc.yes, msg)
			}
		})
	}
}
