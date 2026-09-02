package cli

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/pedrosousa13/lnpm/internal/debug"
	"github.com/pedrosousa13/lnpm/internal/releasekeys"
	"github.com/pedrosousa13/lnpm/internal/ui"
	"github.com/pedrosousa13/lnpm/internal/update"
	"github.com/spf13/cobra"
)

// updateHTTPClient is used for downloading release assets, with a timeout so a
// hung connection can't block the updater indefinitely and a redirect policy so
// a redirect can't take the download off https. Both fetch paths -
// fetchReleaseAsset and downloadToFile - share this one client, so neither can
// be moved off the policy on its own.
var updateHTTPClient = &http.Client{
	Timeout:       2 * time.Minute,
	CheckRedirect: checkUpdateRedirect,
}

// maxUpdateRedirects bounds the redirect chain. net/http passes CheckRedirect
// every request already made, the original included, so this permits nine
// redirects to be followed - the same arithmetic as net/http's own
// defaultCheckRedirect, which this replaces and whose bound it keeps. Read from
// client.go in Go 1.26.7 rather than inferred: reqs is appended to after the
// check, and defaultCheckRedirect refuses at len(via) >= 10.
//
// Confirmed by running it on 2026-08-28 against an endlessly redirecting test
// server: the server answered 10 requests and the refusal carried len(via) 10,
// so 9 redirects were followed. The message below therefore counts requests,
// not redirects - net/http's own says "stopped after 10 redirects" for this
// same len(via), which is one more redirect than it followed.
const maxUpdateRedirects = 10

// checkUpdateRedirect refuses a redirect whose destination is not https, and
// bounds the chain.
//
// Cross-host hops stay allowed: a release download legitimately redirects to a
// separate asset host, so refusing those would break every real update.
//
// This is defence in depth, not an integrity guarantee. verifyRelease runs
// before anything is extracted and refuses an archive whose checksums.txt is
// not signed by an embedded trusted key, so a redirect cannot cause a bad
// install on its own. What it does change is who can feed bytes to the reads
// that happen before that check: without it, anyone on the network path can
// answer them, rather than only whoever serves the release assets.
//
// releaseBaseURL is https, so in practice a refusal here is always a downgrade
// off it. The message does not say so, because the rule is the destination's
// scheme alone and does not consult the hop it came from.
//
// Neither message repeats the destination URL: net/http wraps whatever this
// returns in a *url.Error whose URL is the refused Location, so the user is
// shown 'Get "http://host/path": refused this redirect: ...'. Measured on
// 2026-08-28 by logging the error TestUpdateClientRefusesARedirectThatDowngradesToHTTP
// receives, not read from the docs.
func checkUpdateRedirect(req *http.Request, via []*http.Request) error {
	if req.URL.Scheme != "https" {
		return fmt.Errorf("refused this redirect: an update download must stay on https, not %s", req.URL.Scheme)
	}
	if len(via) >= maxUpdateRedirects {
		return fmt.Errorf("stopped after %d requests while following redirects for a release asset", len(via))
	}
	return nil
}

// maxReleaseMetadataBytes and maxReleaseArchiveBytes bound how much of a
// release asset the updater will read. Both reads happen before verifyRelease
// has established anything, so the only thing standing between a hostile server
// and the updater's memory or disk is these caps and the client timeout above.
//
// Exceeding either is an error, never a truncation: a truncated checksums.txt
// or archive handed on to verification would fail as a signature or checksum
// problem, which is a different diagnosis from the one the user needs.
//
// They are vars so tests can lower them, rather than stream 256 MiB to exercise
// the over-limit path.
var (
	maxReleaseMetadataBytes int64 = 1 << 20   // checksums.txt and its signature
	maxReleaseArchiveBytes  int64 = 256 << 20 // the release archive
)

// releaseBaseURL is the root under which release assets are published. It is a
// var so tests can point it at a local httptest server instead of GitHub.
var releaseBaseURL = "https://github.com/pedrosousa13/lnpm/releases/download"

// trustedReleaseKeys returns the keys a release's checksums.txt must be signed
// by. It is a var so tests can swap in their own generated keys, the same way
// they swap releaseBaseURL.
//
// It is called from the update path rather than resolved at package init: the
// embedded PEMs are only needed to verify a release, and a build whose embed
// does not parse must fail 'lnpm update' with an error the user can read, not
// take down add, remove, gc and doctor along with it.
var trustedReleaseKeys = releasekeys.Trusted

// updateCmd updates lnpm to the latest version
var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update lnpm to the latest version",
	Long: `Update lnpm to the latest version from GitHub releases.

This command automatically detects how lnpm was installed and uses the appropriate method:
  - If installed via 'go install': Uses 'go install' to update
  - If installed via Homebrew or Scoop: Refuses, and names the command to run instead
  - If installed via install script: Downloads and replaces the binary

Examples:
  lnpm update           # Update to latest version
  lnpm update --check   # Only check for updates, don't install
  lnpm update --force   # Replace a Homebrew or Scoop binary anyway`,
	RunE: func(cmd *cobra.Command, args []string) error {
		checkOnly, _ := cmd.Flags().GetBool("check")
		force, _ := cmd.Flags().GetBool("force")
		return RunUpdate(checkOnly, force, version)
	},
}

// RunUpdate handles the update logic
func RunUpdate(checkOnly, force bool, currentVersion string) error {
	// Refuse builds that name no release to update from. A `git describe` build
	// is deliberately not one of them: it names the tag it was built from, so it
	// is checked like any other version - see update.Baseline.
	if _, ok := update.Baseline(currentVersion); !ok {
		return fmt.Errorf("update not supported for dev builds. Install from source: go install github.com/pedrosousa13/lnpm/cmd/lnpm@latest")
	}

	debug.Logf("update: checking for updates (current: %s)", currentVersion)

	// Check for latest version
	result, err := getLatestVersion(currentVersion)
	if err != nil {
		return fmt.Errorf("failed to check for updates: %w", err)
	}

	// Structurally unreachable: CheckFresh returns a nil result only when
	// update.Baseline finds no release to compare against, and the guard at the
	// top of this function asks update.Baseline the same question about the same
	// version and has already returned. The two can no longer disagree - they
	// used to be separate hand-written conditions, which is why this branch was
	// added. It stays only so a future caller that reaches CheckFresh by some
	// other route gets an error rather than a nil dereference on the next line.
	if result == nil {
		return fmt.Errorf("update check returned no result")
	}

	if !result.UpdateAvailable {
		fmt.Printf("Already up to date (%s)\n", currentVersion)
		return nil
	}

	fmt.Printf("Update available: %s → %s\n", result.CurrentVersion, result.LatestVersion)

	if checkOnly {
		fmt.Printf("Run 'lnpm update' to install\n")
		return nil
	}

	// The refusal is asked for before anything is printed as being installed,
	// and after the version check, so a managed user still learns that a newer
	// release exists and is then told how to get it.
	method := currentInstallMethod()
	if name, upgradeCommand, ok := method.manager(); ok && !force {
		return managedInstallError(name, upgradeCommand)
	}

	fmt.Printf("Installing %s...\n", result.LatestVersion)

	if method == installedViaGo {
		return installLatestViaGo()
	}
	return installLatestViaBinary(result.LatestVersion)
}

// getLatestVersion checks for the latest version (fresh check, no cache)
func getLatestVersion(currentVersion string) (*update.Result, error) {
	// Use fresh check when user explicitly runs 'lnpm update' command
	// This ensures we always fetch latest from GitHub, not cached version
	return update.CheckFresh(currentVersion)
}

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

// installMethod names how the running lnpm binary got onto the machine. The
// set is closed because every caller has to answer the same question about
// each one, and a boolean per package manager threaded through the update
// dispatch would let two of them be true at once.
type installMethod int

const (
	installedViaBinary installMethod = iota
	installedViaGo
	installedViaHomebrew
	installedViaScoop
)

// detectInstallMethod classifies the binary at binPath. It takes the path
// rather than reading os.Executable() itself, so the rules below can be tested
// against real macOS and Windows path shapes on any host.
func detectInstallMethod(binPath string) installMethod {
	if wasInstalledViaGo(binPath) {
		return installedViaGo
	}

	segs := containingDirSegments(binPath)
	if isHomebrewPath(segs) {
		return installedViaHomebrew
	}
	if isScoopPath(segs) {
		return installedViaScoop
	}
	return installedViaBinary
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

// manager names the package manager that owns an install and the command that
// upgrades it. ok is false for the two methods 'lnpm update' handles itself.
func (m installMethod) manager() (name, upgradeCommand string, ok bool) {
	switch m {
	case installedViaHomebrew:
		return "Homebrew", "brew upgrade lnpm", true
	case installedViaScoop:
		return "Scoop", "scoop update lnpm", true
	}
	return "", "", false
}

// currentInstallMethod classifies the running binary, asking about the resolved
// path first and the unresolved one second.
//
// The resolved path is what Homebrew needs, because <prefix>/bin/lnpm is a
// symlink into the Caskroom. Resolution cannot be relied on, though. Scoop's
// apps\lnpm\current is an NTFS junction, which os.Executable's path runs
// through, and a junction is neither a symlink nor a directory to Go
// (src/os/types_windows.go, fileStat.mode under the default winsymlink=1,
// reports ModeIrregular), so filepath.EvalSymlinks fails with ENOTDIR on it
// (src/path/filepath/symlink.go, walkSymlinks, Go 1.26.7).
func currentInstallMethod() installMethod {
	binPath, err := os.Executable()
	if err != nil {
		return installedViaBinary
	}
	if resolved, err := filepath.EvalSymlinks(binPath); err == nil {
		if m := detectInstallMethod(resolved); m != installedViaBinary {
			return m
		}
	}
	return detectInstallMethod(binPath)
}

// managedInstallError refuses to replace a binary a package manager owns.
//
// The refusal is not about the replacement failing. It succeeds, and that is
// the problem. The package manager goes on recording the version it installed,
// so its next upgrade overwrites the newer binary with the older one it
// believes is current, and the user is silently rolled back.
func managedInstallError(name, upgradeCommand string) error {
	return fmt.Errorf("this lnpm was installed with %s, so it will not be replaced here. "+
		"Replacing it leaves %s recording the version it installed, and the next upgrade rolls you back to it. "+
		"Run '%s' instead, or 'lnpm update --force' to replace it anyway",
		name, name, upgradeCommand)
}

// installLatestViaGo uses 'go install' to update
func installLatestViaGo() error {
	installURL := "github.com/pedrosousa13/lnpm/cmd/lnpm@latest"

	warnGoInstallIsUnverified()

	debug.Logf("update: installing via go install from %s", installURL)

	cmd := exec.Command("go", "install", installURL)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("installation failed: %w", err)
	}

	// Find the binary path
	binPath, err := exec.LookPath("lnpm")
	if err != nil {
		binPath = "lnpm" // Fallback
	}

	fmt.Printf("✓ Successfully updated to latest version\n")
	fmt.Printf("  Binary location: %s\n", binPath)
	return nil
}

// warnGoInstallIsUnverified tells the user that the update about to run is not
// the signature-verified one, and why.
//
// Only this branch prints it. The download branch reaches verifyRelease, which
// refuses to install anything whose checksums.txt is not signed by a key this
// binary trusts; delegating to 'go install' reaches none of that code, so the
// only integrity check left is whatever the Go toolchain applies to the module -
// the checksum database, which GOSUMDB=off disables outright and which
// GONOSUMDB or GOPRIVATE disable for module paths they match.
//
// It warns rather than refuses: 'go install' is a legitimate way to have
// installed lnpm, and refusing would leave those users with no update path at
// all. Nothing here changes the exit code - the install proceeds exactly as it
// did before this warning existed.
func warnGoInstallIsUnverified() {
	fmt.Printf("%s This update is not signature-verified.\n", ui.IconWarn())
	fmt.Printf("  lnpm was installed with 'go install', so the update is delegated to 'go install' too.\n")
	fmt.Printf("  That builds lnpm from the Go module proxy rather than from the signed release archive,\n")
	fmt.Printf("  so its integrity rests on the Go module checksum database and not on lnpm's release signature.\n")
	fmt.Printf("  For a signature-verified update, install lnpm from a release archive instead.\n")
}

// installLatestViaBinary downloads and replaces the binary directly
func installLatestViaBinary(version string) error {
	// Get current binary path
	binPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to determine binary location: %w", err)
	}

	binPath, err = filepath.EvalSymlinks(binPath)
	if err != nil {
		return fmt.Errorf("failed to resolve binary path: %w", err)
	}

	debug.Logf("update: current binary at %s", binPath)

	if err := downloadAndInstall(version, binPath); err != nil {
		return err
	}

	fmt.Printf("✓ Successfully updated to latest version\n")
	fmt.Printf("  Binary location: %s\n", binPath)
	return nil
}

// downloadAndInstall fetches the release for version and swaps it in at
// binPath, leaving nothing of the download behind.
//
// This is a separate function from installLatestViaBinary for the same reason
// replaceBinary is: installLatestViaBinary resolves its target through
// os.Executable(), which is process-global and cannot be injected, so the
// download-and-clean-up contract could not otherwise be tested.
func downloadAndInstall(version, binPath string) error {
	// Build download URL for current OS/arch
	filename, url := buildDownloadURL(version)

	debug.Logf("update: downloading from %s", url)
	fmt.Printf("  Downloading from %s\n", url)

	// Download and verify the new binary. Removing the whole temp directory
	// rather than just the extracted binary is what takes the downloaded
	// archive - several megabytes, next to the binary - with it.
	newBin, tmpDir, err := downloadBinary(version, url, filename)
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	return replaceBinary(newBin, binPath)
}

// replaceBinary swaps newBin in for the binary at binPath, keeping a backup so
// a failed install leaves the working binary in place.
//
// This is a separate function purely so the backup/restore contract can be
// tested: installLatestViaBinary resolves its target through os.Executable(),
// which is process-global and cannot be injected.
func replaceBinary(newBin, binPath string) error {
	// Backup current binary
	backup := binPath + ".bak"
	if err := os.Rename(binPath, backup); err != nil {
		// The backup rename is the first thing that touches the install
		// directory, so it - not installFile - is where a root-owned
		// /usr/local/bin actually stops the update. os.IsPermission unwraps the
		// *os.LinkError os.Rename returns.
		if os.IsPermission(err) {
			return insufficientPrivilegesError(filepath.Dir(binPath), err)
		}
		return fmt.Errorf("failed to backup current binary: %w", err)
	}

	// Replace with new binary. installFile stages inside binPath's own
	// directory and chmods it, so there is no cross-filesystem rename and no
	// separate chmod to do here.
	if err := installFile(newBin, binPath); err != nil {
		// Restore backup on failure
		if restoreErr := os.Rename(backup, binPath); restoreErr != nil {
			return fmt.Errorf("failed to install new binary: %w (and failed to restore backup: %v)", err, restoreErr)
		}
		return fmt.Errorf("failed to install new binary: %w", err)
	}

	// Remove backup
	_ = os.Remove(backup)
	return nil
}

// insufficientPrivilegesError reports that dir cannot be written, shared by
// every step of the swap that needs write permission on the install directory
// so they all tell the user the same thing.
//
// Deliberate deviation from the "failed to X: %w" convention: this string is
// read by an end user deciding whether to re-run the update under sudo, so it
// leads with the problem and the fix rather than a chain of internal steps.
func insufficientPrivilegesError(dir string, err error) error {
	return fmt.Errorf("cannot write to %s: re-run with sufficient privileges (for example under sudo): %w", dir, err)
}

// installFile puts src's bytes at dst, staging the copy inside dst's own
// directory so the final rename stays within one filesystem. The downloaded
// binary lives under the system temp dir, which is frequently a separate mount
// from the install location - renaming it straight onto dst fails there with
// EXDEV, and the update can never succeed.
func installFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("failed to open downloaded binary: %w", err)
	}
	defer func() { _ = in.Close() }()

	destDir := filepath.Dir(dst)
	staged, err := os.CreateTemp(destDir, ".lnpm-update-*")
	if err != nil {
		if os.IsPermission(err) {
			return insufficientPrivilegesError(destDir, err)
		}
		return fmt.Errorf("failed to stage new binary in %s: %w", destDir, err)
	}

	// Cleared once the rename has consumed the staging file, so this cleanup
	// can never delete the freshly installed binary - or, later, some unrelated
	// file that happens to reuse the name.
	stagedPath := staged.Name()
	defer func() {
		if stagedPath != "" {
			_ = os.Remove(stagedPath)
		}
	}()

	if _, err := io.Copy(staged, in); err != nil {
		_ = staged.Close()
		return fmt.Errorf("failed to write new binary: %w", err)
	}

	// chmod explicitly rather than relying on the process umask: os.CreateTemp
	// makes the file 0600, and the installed binary has to be executable
	// whatever umask the user happens to run with.
	if err := staged.Chmod(0755); err != nil {
		_ = staged.Close()
		return fmt.Errorf("failed to make new binary executable: %w", err)
	}
	if err := staged.Close(); err != nil {
		return fmt.Errorf("failed to write new binary: %w", err)
	}

	if err := os.Rename(stagedPath, dst); err != nil {
		return fmt.Errorf("failed to move the staged binary into place: %w", err)
	}
	stagedPath = ""

	return nil
}

// buildDownloadURL constructs the GitHub release download URL
func buildDownloadURL(version string) (string, string) {
	version = strings.TrimPrefix(version, "v")
	os := runtime.GOOS
	arch := runtime.GOARCH

	var filename string
	if os == "windows" {
		filename = fmt.Sprintf("lnpm_%s_%s_%s.zip", version, os, arch)
	} else {
		filename = fmt.Sprintf("lnpm_%s_%s_%s.tar.gz", version, os, arch)
	}

	url := fmt.Sprintf("%s/v%s/%s", releaseBaseURL, version, filename)
	return filename, url
}

// downloadBinary downloads the release archive, verifies its SHA-256 against
// the release checksums.txt, then extracts it and returns the binary path.
//
// It also returns the temp directory holding both the archive and the extracted
// binary, so the caller can remove the whole thing. Every failure path here
// removes that directory itself, so the returned tmpDir only ever needs
// cleaning up by the caller after a successful return.
func downloadBinary(version, url, filename string) (binaryPath, tmpDir string, err error) {
	// Create temp directory
	tmpDir, err = os.MkdirTemp("", "lnpm-update-")
	if err != nil {
		return "", "", fmt.Errorf("failed to create temp directory: %w", err)
	}

	// Download archive
	filePath := filepath.Join(tmpDir, filename)
	if err := downloadToFile(url, filePath); err != nil {
		_ = os.RemoveAll(tmpDir)
		return "", tmpDir, fmt.Errorf("download failed: %w", err)
	}

	// Verify the release BEFORE extracting or installing — a tampered or
	// corrupted asset must never reach the running binary.
	//
	// The wrapper says "release" rather than "checksum" because verifyRelease
	// refuses on two distinct grounds: a checksum that does not match, and a
	// signature that does not verify or was never published. A wrapper naming
	// only the first tells a user with an unsigned release that their checksums
	// are bad, which is a different problem with a different response.
	if err := verifyRelease(version, filename, filePath); err != nil {
		_ = os.RemoveAll(tmpDir)
		return "", tmpDir, fmt.Errorf("release verification failed: %w", err)
	}
	fmt.Println("  ✓ Signature and checksum verified")

	// Extract binary
	if strings.HasSuffix(filename, ".zip") {
		binaryPath, err = extractZip(filePath, tmpDir)
	} else {
		binaryPath, err = extractTarGz(filePath, tmpDir)
	}

	if err != nil {
		_ = os.RemoveAll(tmpDir)
		return "", tmpDir, err
	}

	return binaryPath, tmpDir, nil
}

// downloadToFile downloads url into dst using the timeout-bounded client,
// refusing a body larger than maxReleaseArchiveBytes.
func downloadToFile(url, dst string) error {
	resp, err := updateHTTPClient.Get(url)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}

	file, err := os.Create(dst)
	if err != nil {
		return err
	}
	// Copy one byte past the cap, so a body that is exactly at it still lands
	// whole and anything larger is detectable rather than silently cut short.
	n, err := io.Copy(file, io.LimitReader(resp.Body, maxReleaseArchiveBytes+1))
	if err != nil {
		_ = file.Close()
		return err
	}
	if n > maxReleaseArchiveBytes {
		_ = file.Close()
		return fmt.Errorf("release archive exceeds the %d-byte download limit", maxReleaseArchiveBytes)
	}
	return file.Close()
}

// buildChecksumsURL returns the checksums.txt URL for a release version.
func buildChecksumsURL(version string) string {
	version = strings.TrimPrefix(version, "v")
	return fmt.Sprintf("%s/v%s/checksums.txt", releaseBaseURL, version)
}

// verifyRelease establishes that the archive at filePath is the one the
// maintainer released, on two grounds: the release's checksums.txt carries a
// signature from a trusted key, and the SHA-256 of filePath matches the entry
// that checksums.txt lists for filename.
//
// Both are needed, and the order matters. Matching an unverified checksums.txt
// proves only that the archive is the one *some* checksums.txt describes -
// which whoever served the archive can always arrange - rather than the one the
// maintainer released.
func verifyRelease(version, filename, filePath string) error {
	sum, err := sha256File(filePath)
	if err != nil {
		return err
	}

	checksums, err := fetchSignedChecksums(version)
	if err != nil {
		return err
	}

	want, ok := findChecksum(bytes.NewReader(checksums), filename)
	if !ok {
		return fmt.Errorf("no checksum listed for %s", filename)
	}
	if !strings.EqualFold(want, sum) {
		return fmt.Errorf("sha256 mismatch for %s (got %s, want %s)", filename, sum, want)
	}
	return nil
}

// fetchSignedChecksums returns the release's checksums.txt, but only after its
// detached signature has been verified against a trusted key. Every failure
// refuses the install; nothing here warns and continues.
func fetchSignedChecksums(version string) ([]byte, error) {
	// Read the whole body - it is a handful of lines - before anything looks at
	// it, because the bytes that get verified must be the same bytes that get
	// parsed.
	checksums, err := fetchReleaseAsset(buildChecksumsURL(version))
	if err != nil {
		return nil, fmt.Errorf("failed to fetch checksums: %w", err)
	}

	sig, err := fetchReleaseAsset(buildChecksumsURL(version) + ".sig")
	if err != nil {
		return nil, signatureUnavailableError(version, err)
	}

	keys, err := trustedReleaseKeys()
	if err != nil {
		return nil, fmt.Errorf("this build trusts no usable release signing key, so no release can be verified - "+
			"reinstall lnpm from a build that does: %w", err)
	}

	if !verifySignature(keys, checksums, sig) {
		return nil, fmt.Errorf("signature verification failed for checksums.txt: not signed by any trusted key")
	}
	return checksums, nil
}

// signatureUnavailableError explains a checksums.txt.sig that could not be
// read, keeping three cases apart: "the release published none", "the body was
// too large to read" and "the fetch failed". Only the last is worth retrying,
// so only the last says so - a response over the read cap is the same size on
// every attempt, and telling the user to check their connection would send them
// after a problem they do not have.
//
// A missing signature is refused rather than tolerated. The rule for whether a
// release is signed is what it publishes: a release is signed if it publishes
// checksums.txt.sig, and unsigned if it does not. Releases up to and including
// v3.0.0 publish none - do not restate that as a boundary the next release
// moves, since it has gone stale here once already.
//
// Refusing is still right for an unsigned release, because 'lnpm update' only
// ever installs the *latest* release - there is no flag to target an older one.
// So the release this refuses is never one of those old artifacts: it is a
// release published after this check shipped, and a missing signature on one of
// those means tampering or a broken release job.
func signatureUnavailableError(version string, err error) error {
	if errors.Is(err, errAssetNotFound) {
		return fmt.Errorf("release v%s publishes no checksums.txt.sig, so it appears unsigned: it will not be installed. "+
			"Do not install it by hand either - report it at https://github.com/pedrosousa13/lnpm/issues",
			strings.TrimPrefix(version, "v"))
	}
	var tooLarge *releaseAssetTooLargeError
	if errors.As(err, &tooLarge) {
		return fmt.Errorf("the checksums signature is too large to be one, so the release cannot be verified and "+
			"will not be installed - report it at https://github.com/pedrosousa13/lnpm/issues: %w", err)
	}
	return fmt.Errorf("failed to fetch the checksums signature, so the release cannot be verified and "+
		"will not be installed - check your connection and try again: %w", err)
}

// verifySignature reports whether sig is an ASN.1 DER ECDSA signature over the
// SHA-256 of body made by any one of keys.
//
// Any key verifying is enough, so that rotating the signing key does not break
// updaters built while an older key was the only one embedded.
func verifySignature(keys []*ecdsa.PublicKey, body, sig []byte) bool {
	digest := sha256.Sum256(body)
	for _, key := range keys {
		if ecdsa.VerifyASN1(key, digest[:], sig) {
			return true
		}
	}
	return false
}

// errAssetNotFound reports a release asset the release does not publish, as
// distinct from one that could not be fetched.
var errAssetNotFound = errors.New("not published on this release")

// releaseAssetTooLargeError reports a release asset refused for exceeding the
// metadata read cap. It is a distinct type so a caller can tell it apart from a
// network failure: retrying a body that is over the cap fetches the same
// over-cap body again.
type releaseAssetTooLargeError struct{ limit int64 }

func (e *releaseAssetTooLargeError) Error() string {
	return fmt.Sprintf("release metadata exceeds the %d-byte read limit", e.limit)
}

// fetchReleaseAsset reads a small release asset fully into memory, refusing a
// body larger than maxReleaseMetadataBytes.
func fetchReleaseAsset(url string) ([]byte, error) {
	resp, err := updateHTTPClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return nil, errAssetNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	// Read one byte past the cap, so a body that is exactly at it still comes
	// back whole and anything larger is detectable rather than silently cut
	// short - a truncated checksums.txt would fail signature verification
	// instead of reporting its real problem.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxReleaseMetadataBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxReleaseMetadataBytes {
		return nil, &releaseAssetTooLargeError{limit: maxReleaseMetadataBytes}
	}
	return body, nil
}

// sha256File returns the hex-encoded SHA-256 of a file.
func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// findChecksum parses goreleaser-style "<hex>  <filename>" lines and returns
// the checksum for the given filename (matched by base name).
func findChecksum(r io.Reader, filename string) (string, bool) {
	target := filepath.Base(filename)
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 2 && filepath.Base(fields[1]) == target {
			return fields[0], true
		}
	}
	return "", false
}

// extractTarGz extracts a tar.gz file and returns the binary path
func extractTarGz(tarPath, tmpDir string) (string, error) {
	file, err := os.Open(tarPath)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()

	gr, err := gzip.NewReader(file)
	if err != nil {
		return "", err
	}
	defer func() { _ = gr.Close() }()

	tr := tar.NewReader(gr)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}

		// Look for the lnpm binary
		if strings.HasSuffix(header.Name, "lnpm") || strings.HasSuffix(header.Name, "lnpm.exe") {
			outPath := filepath.Join(tmpDir, filepath.Base(header.Name))
			out, err := os.Create(outPath)
			if err != nil {
				return "", err
			}
			defer func() { _ = out.Close() }()

			if _, err := io.Copy(out, tr); err != nil {
				return "", err
			}

			return outPath, nil
		}
	}

	return "", fmt.Errorf("lnpm binary not found in archive")
}

// extractZip extracts a zip file and returns the binary path
func extractZip(zipPath, tmpDir string) (string, error) {
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", err
	}
	defer func() { _ = reader.Close() }()

	for _, file := range reader.File {
		if strings.HasSuffix(file.Name, "lnpm.exe") || strings.HasSuffix(file.Name, "lnpm") {
			rc, err := file.Open()
			if err != nil {
				return "", err
			}
			defer func() { _ = rc.Close() }()

			outPath := filepath.Join(tmpDir, filepath.Base(file.Name))
			out, err := os.Create(outPath)
			if err != nil {
				return "", err
			}
			defer func() { _ = out.Close() }()

			if _, err := io.Copy(out, rc); err != nil {
				return "", err
			}

			return outPath, nil
		}
	}

	return "", fmt.Errorf("lnpm binary not found in archive")
}

func init() {
	updateCmd.Flags().BoolP("check", "c", false, "Only check for updates without installing")
	updateCmd.Flags().Bool("force", false, "Update a Homebrew or Scoop install anyway, replacing the package manager's binary")
}
