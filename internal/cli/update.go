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
	// Skip for dev builds
	if currentVersion == "dev" || currentVersion == "" {
		return fmt.Errorf("update not supported for dev builds. Install from source: go install github.com/pedrosousa13/lnpm/cmd/lnpm@latest")
	}

	debug.Logf("update: checking for updates (current: %s)", currentVersion)

	// Check for latest version
	result, err := getLatestVersion(currentVersion)
	if err != nil {
		return fmt.Errorf("failed to check for updates: %w", err)
	}

	if result == nil {
		fmt.Println("Already up to date")
		return nil
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

	// Detect installation method and update accordingly
	if wasInstalledViaGo() {
		return installLatestViaGo()
	}

	return installLatestViaBinary(result.LatestVersion)
}

// getLatestVersion checks for the latest version (fresh check, no cache)
func getLatestVersion(currentVersion string) (*update.Result, error) {
	// Use fresh check when user explicitly runs 'lnpm update' command
	// This ensures we always fetch latest from GitHub, not cached version
	result := update.CheckFresh(currentVersion)
	return result, nil
}

// wasInstalledViaGo checks if lnpm was installed via 'go install'
// by checking if the binary is in GOPATH/bin or GOBIN
func wasInstalledViaGo() bool {
	binPath, err := os.Executable()
	if err != nil {
		return false
	}

	binPath, err = filepath.EvalSymlinks(binPath)
	if err != nil {
		return false
	}

	// Check if in GOBIN
	if gobin := os.Getenv("GOBIN"); gobin != "" {
		if strings.HasPrefix(binPath, gobin) {
			return true
		}
	}

	// Check if in GOPATH/bin
	if gopath := os.Getenv("GOPATH"); gopath != "" {
		gopathBin := filepath.Join(gopath, "bin")
		if strings.HasPrefix(binPath, gopathBin) {
			return true
		}
	}

	// Check default GOPATH location ($HOME/go/bin)
	if home, err := os.UserHomeDir(); err == nil {
		defaultGoBin := filepath.Join(home, "go", "bin")
		if strings.HasPrefix(binPath, defaultGoBin) {
			return true
		}
	}

	// Not in a Go bin directory, assume installed via install script
	return false
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

	// Build download URL for current OS/arch
	filename, url := buildDownloadURL(version)

	debug.Logf("update: downloading from %s", url)
	fmt.Printf("  Downloading from %s\n", url)

	// Download and verify the new binary
	newBin, err := downloadBinary(version, url, filename)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(newBin) }()

	// Backup current binary
	backup := binPath + ".bak"
	if err := os.Rename(binPath, backup); err != nil {
		return fmt.Errorf("failed to backup current binary: %w", err)
	}

	// Replace with new binary
	if err := os.Rename(newBin, binPath); err != nil {
		// Restore backup on failure
		if restoreErr := os.Rename(backup, binPath); restoreErr != nil {
			return fmt.Errorf("failed to install new binary: %w (and failed to restore backup: %v)", err, restoreErr)
		}
		return fmt.Errorf("failed to install new binary: %w", err)
	}

	// Make it executable
	if err := os.Chmod(binPath, 0755); err != nil {
		return fmt.Errorf("failed to make binary executable: %w", err)
	}

	// Remove backup
	_ = os.Remove(backup)

	fmt.Printf("✓ Successfully updated to latest version\n")
	fmt.Printf("  Binary location: %s\n", binPath)
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

	url := fmt.Sprintf("https://github.com/pedrosousa13/lnpm/releases/download/v%s/%s", version, filename)
	return filename, url
}

// downloadBinary downloads the release archive, verifies its SHA-256 against
// the release checksums.txt, then extracts and returns the binary path.
func downloadBinary(version, url, filename string) (string, error) {
	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "lnpm-update-")
	if err != nil {
		return "", fmt.Errorf("failed to create temp directory: %w", err)
	}

	// Download archive
	filePath := filepath.Join(tmpDir, filename)
	if err := downloadToFile(url, filePath); err != nil {
		_ = os.RemoveAll(tmpDir)
		return "", fmt.Errorf("download failed: %w", err)
	}

	// Verify checksum BEFORE extracting or installing — a tampered or
	// corrupted asset must never reach the running binary.
	if err := verifyChecksum(version, filename, filePath); err != nil {
		_ = os.RemoveAll(tmpDir)
		return "", fmt.Errorf("checksum verification failed: %w", err)
	}
	fmt.Println("  ✓ Checksum verified")

	// Extract binary
	var binaryPath string
	if strings.HasSuffix(filename, ".zip") {
		binaryPath, err = extractZip(filePath, tmpDir)
	} else {
		binaryPath, err = extractTarGz(filePath, tmpDir)
	}

	if err != nil {
		_ = os.RemoveAll(tmpDir)
		return "", err
	}

	return binaryPath, nil
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
	return fmt.Sprintf("https://github.com/pedrosousa13/lnpm/releases/download/v%s/checksums.txt", version)
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
