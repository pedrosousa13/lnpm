// Package installmethod classifies how the running lnpm binary got onto the
// machine, so that both 'lnpm update' and the ambient update notice can answer
// the question the same way.
//
// It is a leaf package - it imports nothing of lnpm's own - because
// internal/update prints the notice and internal/cli runs the update, and
// internal/cli already imports internal/update.
package installmethod

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Method names how the running lnpm binary got onto the machine. The set is
// closed because every caller has to answer the same question about each one,
// and a boolean per package manager threaded through the update dispatch would
// let two of them be true at once.
type Method int

const (
	Binary Method = iota
	Go
	Homebrew
	Scoop
)

// wasInstalledViaGo reports whether the binary at binPath was installed via
// 'go install', by checking whether it sits in any of the three directories
// 'go install' writes to: GOBIN, GOPATH/bin, or - when neither variable is set,
// which is the usual case - the default $HOME/go/bin.
//
// It takes the path rather than reading os.Executable() itself so the
// directory-matching rules below can be tested without a real installed binary.
func wasInstalledViaGo(binPath string) bool {
	// Check if in GOBIN
	if gobin := os.Getenv("GOBIN"); gobin != "" {
		if isInBinDir(binPath, gobin) {
			return true
		}
	}

	// Check if in GOPATH/bin
	if gopath := os.Getenv("GOPATH"); gopath != "" {
		gopathBin := filepath.Join(gopath, "bin")
		if isInBinDir(binPath, gopathBin) {
			return true
		}
	}

	// Check default GOPATH location ($HOME/go/bin)
	if home, err := os.UserHomeDir(); err == nil {
		defaultGoBin := filepath.Join(home, "go", "bin")
		if isInBinDir(binPath, defaultGoBin) {
			return true
		}
	}

	// Not in a Go bin directory, assume installed via install script
	return false
}

// isInBinDir reports whether binPath names a file sitting directly inside
// binDir.
//
// Comparing the containing directory is what a prefix match cannot do: the
// string <gopath>/bin is a prefix of the sibling directory <gopath>/bin-other,
// so prefix matching claims binaries there as go-installed. 'go install' only
// ever writes straight into the bin directory, so exact directory equality is
// also the rule that matches what is being asked.
//
// binDir is cleaned because it can come from the environment with a trailing
// separator, which filepath.Dir never produces. On Windows the comparison folds
// case, because its paths are case-insensitive and GOBIN or GOPATH may well
// differ in case from the path os.Executable() reports.
//
// macOS raises the same question - a default APFS volume is case-insensitive
// too, so the same mismatch is possible there - and is deliberately left
// comparing exactly. Folding there would change which update method a darwin
// binary gets, which is outside what this check was fixed for, and the prefix
// match it replaced did not fold either.
func isInBinDir(binPath, binDir string) bool {
	dir := filepath.Dir(binPath)
	binDir = filepath.Clean(binDir)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(dir, binDir)
	}
	return dir == binDir
}

// detect classifies the binary at binPath. It takes the path rather than
// reading os.Executable() itself, so the rules below can be tested against real
// macOS and Windows path shapes on any host.
func detect(binPath string) Method {
	if wasInstalledViaGo(binPath) {
		return Go
	}

	segs := containingDirSegments(binPath)
	if isHomebrewPath(segs) {
		return Homebrew
	}
	if isScoopPath(segs) {
		return Scoop
	}
	return Binary
}

// pathSegments splits p into its path components.
//
// Splitting on '\' as well as '/' is what lets the rules below be checked on a
// Linux host. filepath treats a backslash as an ordinary character off Windows,
// so a real Scoop path would otherwise arrive as one segment.
func pathSegments(p string) []string {
	return strings.FieldsFunc(p, func(r rune) bool { return r == '/' || r == '\\' })
}

// containingDirSegments splits binPath and drops the last segment, which names
// the binary rather than a directory it sits under. A file someone named Cellar
// is not a Homebrew install.
func containingDirSegments(binPath string) []string {
	segs := pathSegments(binPath)
	if len(segs) == 0 {
		return nil
	}
	return segs[:len(segs)-1]
}

// isHomebrewPath reports whether segs walks through Homebrew's Cellar or
// Caskroom.
//
// brew --prefix is deliberately not consulted and <prefix>/bin is deliberately
// not tested. The cask symlinks <prefix>/bin/lnpm into the Caskroom, so the
// resolved path carries the Caskroom segment already, while a prefix test would
// claim the binary of anyone who installed with LNPM_INSTALL_DIR=/usr/local/bin.
//
// The comparison folds case here and in isScoopPath, on every GOOS. These are
// macOS and Windows path shapes, and both filesystems are case-insensitive by
// default, so a fold that depended on runtime.GOOS would answer differently for
// the same path.
func isHomebrewPath(segs []string) bool {
	for _, seg := range segs {
		if strings.EqualFold(seg, "Cellar") || strings.EqualFold(seg, "Caskroom") {
			return true
		}
	}
	return false
}

// isScoopPath reports whether segs walks through a Scoop apps directory.
//
// Whole-segment matching on apps under scoop covers both the per-user root
// %USERPROFILE%\scoop\apps and the global C:\ProgramData\scoop\apps. Scoop also
// lets its root be moved, and a moved root leaves no scoop segment on the path
// at all, so SCOOP and SCOOP_GLOBAL are read for that case.
func isScoopPath(segs []string) bool {
	for i, seg := range segs {
		if i > 0 && strings.EqualFold(seg, "apps") && strings.EqualFold(segs[i-1], "scoop") {
			return true
		}
	}

	for _, env := range []string{"SCOOP", "SCOOP_GLOBAL"} {
		root := os.Getenv(env)
		if root == "" {
			continue
		}
		if hasSegmentPrefix(segs, append(pathSegments(root), "apps")) {
			return true
		}
	}
	return false
}

// hasSegmentPrefix reports whether segs starts with prefix, comparing segment
// by segment so that a scoop root of C:\my is not read as a prefix of
// C:\mystuff.
func hasSegmentPrefix(segs, prefix []string) bool {
	if len(prefix) == 0 || len(segs) < len(prefix) {
		return false
	}
	for i, want := range prefix {
		if !strings.EqualFold(segs[i], want) {
			return false
		}
	}
	return true
}

// Manager names the package manager that owns an install and the command that
// upgrades it. ok is false for the two methods 'lnpm update' handles itself.
func (m Method) Manager() (name, upgradeCommand string, ok bool) {
	switch m {
	case Homebrew:
		return "Homebrew", "brew upgrade lnpm", true
	case Scoop:
		return "Scoop", "scoop update lnpm", true
	}
	return "", "", false
}

// Current classifies the running binary, asking about the resolved path first
// and the unresolved one second.
//
// The resolved path is what Homebrew needs, because <prefix>/bin/lnpm is a
// symlink into the Caskroom. Resolution cannot be relied on, though. Scoop's
// apps\lnpm\current is an NTFS junction, which os.Executable's path runs
// through, and a junction is neither a symlink nor a directory to Go
// (src/os/types_windows.go, fileStat.mode under the default winsymlink=1,
// reports ModeIrregular), so filepath.EvalSymlinks fails with ENOTDIR on it
// (src/path/filepath/symlink.go, walkSymlinks, Go 1.26.7).
func Current() Method {
	binPath, err := os.Executable()
	if err != nil {
		return Binary
	}
	if resolved, err := filepath.EvalSymlinks(binPath); err == nil {
		if m := detect(resolved); m != Binary {
			return m
		}
	}
	return detect(binPath)
}
