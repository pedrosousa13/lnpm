package update

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/pedrosousa13/lnpm/internal/config"
	"github.com/pedrosousa13/lnpm/internal/debug"
	"golang.org/x/mod/semver"
)

const (
	githubRepo    = "pedrosousa13/lnpm"
	checkInterval = 24 * time.Hour
	// requestTimeout bounds the background check, which runs alongside ordinary
	// commands and must never noticeably delay them.
	requestTimeout = 500 * time.Millisecond
	// freshRequestTimeout bounds the explicit `lnpm update` check, which the user
	// is waiting on and which must not give up on a merely slow connection.
	freshRequestTimeout = 15 * time.Second
)

// githubAPIBaseURL is the GitHub API root. It is a var so tests can point it at
// a local httptest server instead of the real API.
var githubAPIBaseURL = "https://api.github.com"

type cacheFile struct {
	LastCheck     time.Time `json:"last_check"`
	LatestVersion string    `json:"latest_version"`
}

type githubRelease struct {
	TagName string `json:"tag_name"`
}

// Result holds the update check result
type Result struct {
	CurrentVersion  string
	LatestVersion   string
	UpdateAvailable bool
}

// CheckFresh checks for latest version without using cache
// Used when user explicitly runs 'lnpm update'
//
// It returns (nil, nil) only when the running version names no release to
// compare against - see Baseline, which does check a dev build that names one.
// A failed fetch is returned as an error rather than swallowed, so the caller
// can tell "no update available" apart from "could not check".
func CheckFresh(currentVersion string) (*Result, error) {
	if _, ok := Baseline(currentVersion); !ok {
		return nil, nil
	}

	debug.Logf("update: checking for fresh version %s (bypassing cache)", currentVersion)

	// Fetch from GitHub directly
	ctx, cancel := context.WithTimeout(context.Background(), freshRequestTimeout)
	defer cancel()

	latest, err := fetchLatestVersion(ctx)
	if err != nil {
		debug.Logf("update: fetch failed: %v", err)
		return nil, fmt.Errorf("failed to fetch latest release from %s: %w", githubAPIBaseURL, err)
	}

	// Update cache
	_, cachePath := loadCache()
	saveCache(cachePath, latest)

	return compareVersions(currentVersion, latest), nil
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

	// Skip builds that name no release to compare against
	if _, ok := Baseline(currentVersion); !ok {
		debug.Logf("update: skipping check for %q, which names no release", currentVersion)
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
	url := fmt.Sprintf("%s/repos/%s/releases/latest", githubAPIBaseURL, githubRepo)

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

// compareVersions reports whether latest supersedes current.
//
// Every release up to v1.12.0 shipped a byte-wise string comparison here, which
// made "1.12.0" > "1.9.0" false and left anyone below 1.10.0 unable to
// self-update. The fix only runs in a binary those users can never reach, so the
// next release was forced to 2.0.0: a major bump is the only tag that is both
// semver-greater and string-greater than every 1.x, so the stale comparison in
// their installed binary still sees it. See issue #297.
func compareVersions(current, latest string) *Result {
	// Normalize mixed v-prefixed and bare inputs to canonical vX.Y.Z form
	latestSemver := vPrefixed(latest)

	// Compare the release current descends from, not current itself: a
	// `git describe` stamp such as "v1.12.0-53-g7079f81-dirty" is a pre-release
	// of v1.12.0 as far as semver is concerned, so comparing it verbatim
	// reported the tag it is 53 commits ahead of as an upgrade (#283).
	//
	// Baseline reporting no release also stands in for the validity check this
	// comparison used to do itself: semver.Compare treats an invalid version as
	// less than any valid one, so an unparseable current (e.g. a bare commit
	// hash from an untagged build) would otherwise always look outdated.
	currentSemver, ok := Baseline(current)

	// CurrentVersion keeps the full stamp rather than the baseline, so the
	// notice still tells the user which build they are actually running.
	result := &Result{
		CurrentVersion:  current,
		LatestVersion:   latest,
		UpdateAvailable: ok && semver.Compare(latestSemver, currentSemver) > 0,
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
