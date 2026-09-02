package installmethod

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// setGoEnv points the three Go bin locations wasInstalledViaGo consults at
// test-controlled directories, so the result never depends on the machine's own
// Go layout. os.UserHomeDir reads HOME everywhere except Windows, where it
// reads USERPROFILE.
func setGoEnv(t *testing.T, gobin, gopath, home string) {
	t.Helper()

	t.Setenv("GOBIN", gobin)
	t.Setenv("GOPATH", gopath)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
}

// A binary in a directory whose name merely starts with a Go bin directory's
// name - <gopath>/bin-other next to <gopath>/bin - is not go-installed, and
// updating it with 'go install' would install to a different directory than the
// one it actually lives in.
func TestWasInstalledViaGo(t *testing.T) {
	root := t.TempDir()
	gobin := filepath.Join(root, "gobin")
	gopath := filepath.Join(root, "gopath")
	home := filepath.Join(root, "home")

	tests := []struct {
		name    string
		binPath string
		want    bool
	}{
		{"directly in GOBIN", filepath.Join(gobin, "lnpm"), true},
		{"in a sibling of GOBIN", filepath.Join(root, "gobin-other", "lnpm"), false},
		{"directly in GOPATH/bin", filepath.Join(gopath, "bin", "lnpm"), true},
		{"in a sibling of GOPATH/bin", filepath.Join(gopath, "bin-other", "lnpm"), false},
		{"nested below GOPATH/bin", filepath.Join(gopath, "bin", "nested", "lnpm"), false},
		{"directly in the default home go bin", filepath.Join(home, "go", "bin", "lnpm"), true},
		{"in a sibling of the default home go bin", filepath.Join(home, "go", "bin-other", "lnpm"), false},
		{"outside every Go bin directory", filepath.Join(root, "usr", "local", "bin", "lnpm"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setGoEnv(t, gobin, gopath, home)

			if got := wasInstalledViaGo(tt.binPath); got != tt.want {
				t.Errorf("wasInstalledViaGo(%q) = %v, want %v", tt.binPath, got, tt.want)
			}
		})
	}
}

// GOBIN comes from the environment and may carry a trailing separator, while
// the running binary's directory never does.
func TestWasInstalledViaGoAcceptsATrailingSeparatorInGOBIN(t *testing.T) {
	root := t.TempDir()
	gobin := filepath.Join(root, "gobin")
	setGoEnv(t, gobin+string(filepath.Separator), filepath.Join(root, "gopath"), filepath.Join(root, "home"))

	binPath := filepath.Join(gobin, "lnpm")
	if !wasInstalledViaGo(binPath) {
		t.Errorf("wasInstalledViaGo(%q) = false with GOBIN %q, want true", binPath, gobin+string(filepath.Separator))
	}
}

// Windows paths are case-insensitive, so a GOBIN that differs from the running
// binary's directory only in case still names the same directory there and must
// be treated as a match. Elsewhere the comparison stays exact - a deliberate
// choice rather than a claim about the filesystem, since a default macOS volume
// is case-insensitive too; see isInBinDir for why darwin is left alone.
//
// Note this assertion is vacuous on Linux and macOS, where want is false and
// any non-folding implementation satisfies it. The folding branch is only
// really exercised by CI's test-windows job, so a green local run on any other
// platform says nothing about it.
func TestWasInstalledViaGoFoldsCaseOnlyOnWindows(t *testing.T) {
	root := t.TempDir()
	gobin := filepath.Join(root, "GoBin")
	setGoEnv(t, gobin, filepath.Join(root, "gopath"), filepath.Join(root, "home"))

	binPath := filepath.Join(strings.ToLower(gobin), "lnpm")
	want := runtime.GOOS == "windows"
	if got := wasInstalledViaGo(binPath); got != want {
		t.Errorf("wasInstalledViaGo(%q) = %v with GOBIN %q, want %v on %s", binPath, got, gobin, want, runtime.GOOS)
	}
}

// isolateInstallEnv pins every variable the install-method rules read, so a
// path in the table below cannot be claimed by the go-install rules through the
// developer's own GOBIN, GOPATH or $HOME, and so a Scoop root set on the host
// cannot answer for the relocated-root cases.
func isolateInstallEnv(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("GOBIN", filepath.Join(dir, "gobin"))
	t.Setenv("GOPATH", filepath.Join(dir, "gopath"))
	t.Setenv("HOME", dir)
	t.Setenv("SCOOP", "")
	t.Setenv("SCOOP_GLOBAL", "")
}

// Replacing a Homebrew or Scoop binary in place leaves the package manager
// recording the version it installed, and its next upgrade rolls the user back
// (#508). Detection therefore has to hold on the real path shapes of both, from
// a Linux test.
func TestDetect(t *testing.T) {
	isolateInstallEnv(t)

	tests := []struct {
		name    string
		binPath string
		want    Method
	}{
		{"apple silicon cask", "/opt/homebrew/Caskroom/lnpm/4.1.0/lnpm", Homebrew},
		{"intel cask", "/usr/local/Caskroom/lnpm/4.1.0/lnpm", Homebrew},
		{"cellar", "/opt/homebrew/Cellar/lnpm/4.1.0/bin/lnpm", Homebrew},
		{"lowercase caskroom", "/opt/homebrew/caskroom/lnpm/4.1.0/lnpm", Homebrew},
		{"caskroom prefix of another directory", "/home/u/Caskroomish/bin/lnpm", Binary},
		{"cellar inside another directory name", "/home/u/mycellar/bin/lnpm", Binary},
		{"binary named cellar", "/home/u/bin/Cellar", Binary},
		{"scoop current", `C:\Users\u\scoop\apps\lnpm\current\lnpm.exe`, Scoop},
		{"scoop versioned", `C:\Users\u\scoop\apps\lnpm\4.1.0\lnpm.exe`, Scoop},
		{"scoop global", `C:\ProgramData\scoop\apps\lnpm\current\lnpm.exe`, Scoop},
		{"apps under a directory ending in scoop", `C:\Users\u\notscoop\apps\lnpm\lnpm.exe`, Binary},
		{"apps with no scoop parent", `C:\apps\lnpm\lnpm.exe`, Binary},
		{"plain binary", "/usr/local/bin/lnpm", Binary},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := detect(tt.binPath); got != tt.want {
				t.Errorf("detect(%q) = %d, want %d", tt.binPath, got, tt.want)
			}
		})
	}
}

// Scoop's root can be moved, and a moved root leaves no scoop segment on the
// path for the segment rule to find.
func TestDetectWithRelocatedScoopRoot(t *testing.T) {
	for _, env := range []string{"SCOOP", "SCOOP_GLOBAL"} {
		t.Run(env, func(t *testing.T) {
			isolateInstallEnv(t)
			t.Setenv(env, `D:\tools\sc`)

			if got := detect(`D:\tools\sc\apps\lnpm\current\lnpm.exe`); got != Scoop {
				t.Errorf("detect under %s = %d, want Scoop", env, got)
			}
			if got := detect(`D:\tools\scratch\apps\lnpm\lnpm.exe`); got != Binary {
				t.Errorf("detect of a sibling of the %s root = %d, want Binary", env, got)
			}
		})
	}
}

func TestMethodManager(t *testing.T) {
	tests := []struct {
		method             Method
		wantName           string
		wantUpgradeCommand string
		wantOK             bool
	}{
		{Homebrew, "Homebrew", "brew upgrade lnpm", true},
		{Scoop, "Scoop", "scoop update lnpm", true},
		{Go, "", "", false},
		{Binary, "", "", false},
	}

	for _, tt := range tests {
		name, upgradeCommand, ok := tt.method.Manager()
		if name != tt.wantName || upgradeCommand != tt.wantUpgradeCommand || ok != tt.wantOK {
			t.Errorf("Method(%d).Manager() = (%q, %q, %v), want (%q, %q, %v)",
				tt.method, name, upgradeCommand, ok, tt.wantName, tt.wantUpgradeCommand, tt.wantOK)
		}
	}
}
