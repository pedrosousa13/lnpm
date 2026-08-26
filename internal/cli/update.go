package cli

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
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
	"github.com/pedrosousa13/lnpm/internal/update"
	"github.com/spf13/cobra"
)

// updateHTTPClient is used for downloading release assets, with a timeout so a
// hung connection can't block the updater indefinitely.
var updateHTTPClient = &http.Client{Timeout: 2 * time.Minute}

// releaseBaseURL is the root under which release assets are published. It is a
// var so tests can point it at a local httptest server instead of GitHub.
var releaseBaseURL = "https://github.com/pedrosousa13/lnpm/releases/download"

// updateCmd updates lnpm to the latest version
var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update lnpm to the latest version",
	Long: `Update lnpm to the latest version from GitHub releases.

This command automatically detects how lnpm was installed and uses the appropriate method:
  - If installed via 'go install': Uses 'go install' to update
  - If installed via install script: Downloads and replaces the binary

Examples:
  lnpm update           # Update to latest version
  lnpm update --check   # Only check for updates, don't install`,
	RunE: func(cmd *cobra.Command, args []string) error {
		checkOnly, _ := cmd.Flags().GetBool("check")
		return RunUpdate(checkOnly, version)
	},
}

// RunUpdate handles the update logic
func RunUpdate(checkOnly bool, currentVersion string) error {
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

	// Install latest version
	fmt.Printf("Installing %s...\n", result.LatestVersion)

	// Detect installation method and update accordingly. A binary whose path
	// cannot be resolved is not treated as go-installed: installLatestViaBinary
	// resolves it again and reports the failure to the user.
	if binPath, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(binPath); err == nil && wasInstalledViaGo(resolved) {
			return installLatestViaGo()
		}
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

// installLatestViaGo uses 'go install' to update
func installLatestViaGo() error {
	installURL := "github.com/pedrosousa13/lnpm/cmd/lnpm@latest"

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

	// Verify checksum BEFORE extracting or installing — a tampered or
	// corrupted asset must never reach the running binary.
	if err := verifyChecksum(version, filename, filePath); err != nil {
		_ = os.RemoveAll(tmpDir)
		return "", tmpDir, fmt.Errorf("checksum verification failed: %w", err)
	}
	fmt.Println("  ✓ Checksum verified")

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

// downloadToFile downloads url into dst using the timeout-bounded client.
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
	if _, err := io.Copy(file, resp.Body); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

// buildChecksumsURL returns the checksums.txt URL for a release version.
func buildChecksumsURL(version string) string {
	version = strings.TrimPrefix(version, "v")
	return fmt.Sprintf("%s/v%s/checksums.txt", releaseBaseURL, version)
}

// verifyChecksum computes the SHA-256 of filePath and compares it to the entry
// for filename in the release checksums.txt.
func verifyChecksum(version, filename, filePath string) error {
	sum, err := sha256File(filePath)
	if err != nil {
		return err
	}

	resp, err := updateHTTPClient.Get(buildChecksumsURL(version))
	if err != nil {
		return fmt.Errorf("failed to fetch checksums: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to fetch checksums: status %d", resp.StatusCode)
	}

	want, ok := findChecksum(resp.Body, filename)
	if !ok {
		return fmt.Errorf("no checksum listed for %s", filename)
	}
	if !strings.EqualFold(want, sum) {
		return fmt.Errorf("sha256 mismatch for %s (got %s, want %s)", filename, sum, want)
	}
	return nil
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
}
