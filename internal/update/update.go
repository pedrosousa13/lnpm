package update

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pedrosousa13/lnpm/internal/config"
	"github.com/pedrosousa13/lnpm/internal/debug"
)

const (
	githubRepo     = "pedrosousa13/lnpm"
	checkInterval  = 24 * time.Hour
	requestTimeout = 500 * time.Millisecond
)

type cacheFile struct {
	LastCheck     time.Time `json:"last_check"`
	LatestVersion string    `json:"latest_version"`
}

type githubRelease struct {
	TagName string `json:"tag_name"`
}

// Result holds the update check result
type Result struct {
	CurrentVersion string
	LatestVersion  string
	UpdateAvailable bool
}

// CheckFresh checks for latest version without using cache
// Used when user explicitly runs 'lnpm update'
func CheckFresh(currentVersion string) *Result {
	if currentVersion == "dev" || currentVersion == "" {
		return nil
	}

	debug.Logf("update: checking for fresh version %s (bypassing cache)", currentVersion)

	// Fetch from GitHub directly
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	latest, err := fetchLatestVersion(ctx)
	if err != nil {
		debug.Logf("update: fetch failed: %v", err)
		return nil
	}

	// Update cache
	_, cachePath := loadCache()
	saveCache(cachePath, latest)

	return compareVersions(currentVersion, latest)
}

// CheckAsync starts a background version check and returns a channel
// that will receive the result. The channel is buffered so it won't block.
func CheckAsync(currentVersion string) <-chan *Result {
	ch := make(chan *Result, 1)

	// Check if disabled
	if os.Getenv("LNPM_NO_UPDATE_CHECK") != "" {
		debug.Logf("update: check disabled via LNPM_NO_UPDATE_CHECK")
		close(ch)
		return ch
	}

	// Skip for dev builds
	if currentVersion == "dev" || currentVersion == "" {
		debug.Logf("update: skipping check for dev build")
		close(ch)
		return ch
	}

	go func() {
		defer close(ch)
		debug.Logf("update: checking for version %s", currentVersion)

		result := check(currentVersion)
		if result != nil && result.UpdateAvailable {
			ch <- result
		}
	}()

	return ch
}

func check(currentVersion string) *Result {
	// Try cache first
	cache, cachePath := loadCache()
	if cache != nil && time.Since(cache.LastCheck) < checkInterval {
		debug.Logf("update: using cached version %s", cache.LatestVersion)
		return compareVersions(currentVersion, cache.LatestVersion)
	}

	// Fetch from GitHub
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	latest, err := fetchLatestVersion(ctx)
	if err != nil {
		debug.Logf("update: fetch failed: %v", err)
		return nil
	}

	// Save cache
	saveCache(cachePath, latest)

	return compareVersions(currentVersion, latest)
}

func loadCache() (*cacheFile, string) {
	storePath, err := config.GetStorePath()
	if err != nil {
		return nil, ""
	}

	cachePath := filepath.Join(storePath, "version_cache.json")
	data, err := os.ReadFile(cachePath)
	if err != nil {
		return nil, cachePath
	}

	var cache cacheFile
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil, cachePath
	}

	return &cache, cachePath
}

func saveCache(cachePath, version string) {
	if cachePath == "" {
		return
	}

	cache := cacheFile{
		LastCheck:     time.Now(),
		LatestVersion: version,
	}

	data, err := json.Marshal(cache)
	if err != nil {
		return
	}

	_ = os.MkdirAll(filepath.Dir(cachePath), 0755)
	_ = os.WriteFile(cachePath, data, 0644)
}

func fetchLatestVersion(ctx context.Context) (string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", githubRepo)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", err
	}

	return release.TagName, nil
}

func compareVersions(current, latest string) *Result {
	// Normalize versions (strip v prefix)
	currentNorm := strings.TrimPrefix(current, "v")
	latestNorm := strings.TrimPrefix(latest, "v")

	result := &Result{
		CurrentVersion:  current,
		LatestVersion:   latest,
		UpdateAvailable: false,
	}

	// Simple string comparison works for semver if same length
	// For proper comparison we'd need a semver library, but this handles most cases
	if latestNorm != currentNorm && latestNorm > currentNorm {
		result.UpdateAvailable = true
	}

	debug.Logf("update: current=%s latest=%s available=%v", current, latest, result.UpdateAvailable)
	return result
}

// PrintUpdateNotice prints an update notice to stderr
func PrintUpdateNotice(r *Result) {
	if r == nil || !r.UpdateAvailable {
		return
	}

	fmt.Fprintf(os.Stderr, "\nUpdate available: %s → %s\n", r.CurrentVersion, r.LatestVersion)
	fmt.Fprintf(os.Stderr, "Run: lnpm update\n")
}
