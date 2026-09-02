package update

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pedrosousa13/lnpm/internal/installmethod"
)

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		name    string
		current string
		latest  string
		want    bool
	}{
		{"newer minor with more digits", "1.9.0", "1.11.0", true},
		{"newer patch with more digits", "1.2.9", "1.2.10", true},
		{"same version", "1.11.0", "1.11.0", false},
		{"current newer than latest", "1.11.0", "1.9.0", false},
		{"v-prefixed inputs", "v1.9.0", "v1.11.0", true},
		{"mixed v-prefixed and bare inputs", "1.9.0", "v1.11.0", true},
		{"v-prefixed same version", "v1.11.0", "1.11.0", false},
		{"unparseable current from untagged build", "a1b2c3d", "1.12.0", false},
		{"malformed latest", "1.11.0", "release-1.12", false},
		{"empty latest", "1.11.0", "", false},
		{"newer pre-release", "1.11.0", "1.12.0-rc.1", true},
		{"current pre-release newer than latest", "1.12.0-rc.1", "1.11.0", false},
		{"release supersedes its own rc", "1.12.0-rc.1", "1.12.0", true},
		// A Makefile build stamps `git describe` output, which semver ranks
		// below the tag it descends from - so the release it is already ahead
		// of used to be reported as an available update (#283).
		{"git describe build against the tag it descends from", "v1.12.0-53-g7079f81-dirty", "v1.12.0", false},
		{"git describe build without a dirty marker", "v1.12.0-53-g7079f81", "v1.12.0", false},
		{"dirty checkout of a release tag", "v1.12.0-dirty", "v1.12.0", false},
		{"git describe build against an older release", "v1.12.0-53-g7079f81-dirty", "v1.11.0", false},
		{"git describe build with an uppercase sha", "v1.12.0-53-g7079F81-dirty", "v1.12.0", false},
		{"git describe build with an uppercase marker", "v1.12.0-53-G7079F81", "v1.12.0", false},
		// Suppressing the downgrade must not suppress the check: a genuinely
		// newer release still reaches a dev build.
		{"git describe build against a newer release", "v1.12.0-53-g7079f81-dirty", "v1.13.0", true},
		{"git describe build from a pre-release tag against its release", "v2.1.0-rc.1-53-g7079f81", "v2.1.0", true},
		{"dirty build metadata against a newer release", "v1.11.0+dirty", "v1.12.0", true},
		{"dirty build metadata against its own release", "v1.11.0+dirty", "v1.11.0", false},
		{"unstamped build", "dev", "v1.13.0", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := compareVersions(tt.current, tt.latest)
			if got.UpdateAvailable != tt.want {
				t.Errorf("compareVersions(%q, %q).UpdateAvailable = %v, want %v",
					tt.current, tt.latest, got.UpdateAvailable, tt.want)
			}
		})
	}
}

// startAPIServer points githubAPIBaseURL at a local test server for the
// duration of the test and returns a counter of requests it received. The
// counter is atomic because the handler runs on the server's goroutines.
//
// Don't use t.Parallel() in callers - this helper swaps the process-wide
// githubAPIBaseURL var.
func startAPIServer(t *testing.T, handler http.HandlerFunc) *atomic.Int64 {
	t.Helper()

	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		handler(w, r)
	}))
	t.Cleanup(srv.Close)

	prev := githubAPIBaseURL
	githubAPIBaseURL = srv.URL
	t.Cleanup(func() { githubAPIBaseURL = prev })

	return &hits
}

func TestGithubAPIBaseURLDefault(t *testing.T) {
	// The base URL was extracted into a var purely for testability; the default
	// must stay byte-identical to the previously hardcoded API root.
	if githubAPIBaseURL != "https://api.github.com" {
		t.Errorf("githubAPIBaseURL = %q, want https://api.github.com", githubAPIBaseURL)
	}
}

func TestFetchLatestVersion(t *testing.T) {
	wantPath := "/repos/" + githubRepo + "/releases/latest"
	hits := startAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != wantPath {
			t.Errorf("request path = %q, want %q", r.URL.Path, wantPath)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name": "v1.2.3"}`))
	})

	got, err := fetchLatestVersion(context.Background())
	if err != nil {
		t.Fatalf("fetchLatestVersion returned error: %v", err)
	}
	if got != "v1.2.3" {
		t.Errorf("fetchLatestVersion = %q, want v1.2.3", got)
	}
	if n := hits.Load(); n != 1 {
		t.Errorf("server hits = %d, want 1", n)
	}
}

func TestFetchLatestVersionServerError(t *testing.T) {
	startAPIServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	got, err := fetchLatestVersion(context.Background())
	if err == nil {
		t.Fatalf("fetchLatestVersion = %q, want error on 500", got)
	}
	if got != "" {
		t.Errorf("fetchLatestVersion = %q, want empty string on error", got)
	}
}

func TestSaveAndLoadCacheRoundTrip(t *testing.T) {
	store := t.TempDir()
	t.Setenv("LNPM_STORE", store)

	_, cachePath := loadCache()
	if want := filepath.Join(store, "version_cache.json"); cachePath != want {
		t.Fatalf("cachePath = %q, want %q", cachePath, want)
	}

	saveCache(cachePath, "v4.5.6")

	cache, path := loadCache()
	if path != cachePath {
		t.Errorf("cachePath = %q, want %q", path, cachePath)
	}
	if cache == nil {
		t.Fatal("loadCache returned nil cache after saveCache")
	}
	if cache.LatestVersion != "v4.5.6" {
		t.Errorf("cache.LatestVersion = %q, want v4.5.6", cache.LatestVersion)
	}
	if time.Since(cache.LastCheck) > time.Minute {
		t.Errorf("cache.LastCheck = %v, want ~now", cache.LastCheck)
	}
}

func TestLoadCacheMissingFile(t *testing.T) {
	t.Setenv("LNPM_STORE", t.TempDir())

	cache, cachePath := loadCache()
	if cache != nil {
		t.Errorf("loadCache = %+v, want nil for a missing cache file", cache)
	}
	if cachePath == "" {
		t.Error("loadCache returned an empty path; saveCache would then be a no-op")
	}
}

func TestLoadCacheCorruptJSON(t *testing.T) {
	store := t.TempDir()
	t.Setenv("LNPM_STORE", store)

	cachePath := filepath.Join(store, "version_cache.json")
	if err := os.WriteFile(cachePath, []byte("{not json"), 0644); err != nil {
		t.Fatal(err)
	}

	cache, path := loadCache()
	if cache != nil {
		t.Errorf("loadCache = %+v, want nil for corrupt JSON", cache)
	}
	if path != cachePath {
		t.Errorf("cachePath = %q, want %q", path, cachePath)
	}
}

// writeCache writes a cache file directly so the test can control LastCheck.
func writeCache(t *testing.T, store string, lastCheck time.Time, version string) {
	t.Helper()

	data, err := json.Marshal(cacheFile{LastCheck: lastCheck, LatestVersion: version})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, "version_cache.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
}

func TestCheckUsesFreshCacheWithoutFetching(t *testing.T) {
	store := t.TempDir()
	t.Setenv("LNPM_STORE", store)
	writeCache(t, store, time.Now(), "v9.9.9")

	hits := startAPIServer(t, func(w http.ResponseWriter, _ *http.Request) {
		t.Error("check made an HTTP request despite a fresh cache")
		w.WriteHeader(http.StatusInternalServerError)
	})

	result := check("1.0.0")
	if got := hits.Load(); got != 0 {
		t.Errorf("server hits = %d, want 0", got)
	}
	if result == nil {
		t.Fatal("check returned nil")
	}
	if result.LatestVersion != "v9.9.9" || !result.UpdateAvailable {
		t.Errorf("check = %+v, want LatestVersion v9.9.9 with UpdateAvailable true", result)
	}
}

func TestCheckRefetchesWhenCacheIsStale(t *testing.T) {
	store := t.TempDir()
	t.Setenv("LNPM_STORE", store)
	writeCache(t, store, time.Now().Add(-25*time.Hour), "v9.9.9")

	hits := startAPIServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name": "v2.0.0"}`))
	})

	result := check("1.0.0")
	if got := hits.Load(); got != 1 {
		t.Errorf("server hits = %d, want 1", got)
	}
	if result == nil {
		t.Fatal("check returned nil")
	}
	if result.LatestVersion != "v2.0.0" {
		t.Errorf("check = %+v, want LatestVersion v2.0.0 from the refetch", result)
	}

	// The refetch must also refresh the cache on disk.
	cache, _ := loadCache()
	if cache == nil || cache.LatestVersion != "v2.0.0" {
		t.Errorf("cache after refetch = %+v, want LatestVersion v2.0.0", cache)
	}
}

func TestCheckFreshBypassesCache(t *testing.T) {
	store := t.TempDir()
	t.Setenv("LNPM_STORE", store)
	writeCache(t, store, time.Now(), "v9.9.9")

	hits := startAPIServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name": "v3.0.0"}`))
	})

	result, err := CheckFresh("1.0.0")
	if err != nil {
		t.Fatalf("CheckFresh returned error: %v", err)
	}
	if got := hits.Load(); got != 1 {
		t.Errorf("server hits = %d, want 1 (CheckFresh must ignore a fresh cache)", got)
	}
	if result == nil {
		t.Fatal("CheckFresh returned nil")
	}
	if result.LatestVersion != "v3.0.0" || !result.UpdateAvailable {
		t.Errorf("CheckFresh = %+v, want LatestVersion v3.0.0 with UpdateAvailable true", result)
	}

	cache, _ := loadCache()
	if cache == nil || cache.LatestVersion != "v3.0.0" {
		t.Errorf("cache after CheckFresh = %+v, want LatestVersion v3.0.0", cache)
	}
}

// A failed fetch must be reported, not swallowed: returning a bare nil made a
// network failure indistinguishable from "no update available", so `lnpm update`
// printed "Already up to date" and exited 0 without ever reaching GitHub.
func TestCheckFreshReturnsErrorWhenFetchFails(t *testing.T) {
	t.Setenv("LNPM_STORE", t.TempDir())
	startAPIServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	result, err := CheckFresh("1.0.0")
	if err == nil {
		t.Errorf("CheckFresh error = nil, want non-nil when the fetch fails")
	}
	if result != nil {
		t.Errorf("CheckFresh = %+v, want nil result when the fetch fails", result)
	}
}

// The happy path must survive the error plumbing: a successful check that finds
// no newer version still reports success, so the caller prints "Already up to
// date" and exits 0.
func TestCheckFreshSucceedsWhenAlreadyUpToDate(t *testing.T) {
	t.Setenv("LNPM_STORE", t.TempDir())
	startAPIServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name": "v1.0.0"}`))
	})

	result, err := CheckFresh("1.0.0")
	if err != nil {
		t.Fatalf("CheckFresh returned error: %v", err)
	}
	if result == nil {
		t.Fatal("CheckFresh returned nil result for a successful check")
	}
	if result.UpdateAvailable {
		t.Errorf("CheckFresh = %+v, want UpdateAvailable false", result)
	}
}

func TestCheckFreshSkipsDevBuilds(t *testing.T) {
	t.Setenv("LNPM_STORE", t.TempDir())
	startAPIServer(t, func(w http.ResponseWriter, _ *http.Request) {
		t.Error("CheckFresh made an HTTP request for a dev build")
		w.WriteHeader(http.StatusInternalServerError)
	})

	// The dev-build skip is not a failure, so it must not surface as an error.
	// A pseudo-version is skipped for the same reason "dev" is: its base is
	// synthesised from a timestamp and a commit, so it names no release to
	// compare against.
	for _, v := range []string{"dev", "", "v1.12.1-0.20260819061412-6d9902254937", "7079f81-dirty"} {
		result, err := CheckFresh(v)
		if result != nil || err != nil {
			t.Errorf("CheckFresh(%q) = (%+v, %v), want (nil, nil)", v, result, err)
		}
	}
}

// A dev build that names the release it came from - a `git describe` stamp, or
// a working-tree marker on a tag - still gets checked. Suppressing the
// downgrade in #283 must not have suppressed the check itself.
func TestCheckFreshRunsForDevBuildsThatNameARelease(t *testing.T) {
	t.Setenv("LNPM_STORE", t.TempDir())
	startAPIServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name": "v1.13.0"}`))
	})

	for _, v := range []string{"v1.12.0-53-g7079f81-dirty", "v1.11.0+dirty"} {
		result, err := CheckFresh(v)
		if err != nil {
			t.Fatalf("CheckFresh(%q) returned error: %v", v, err)
		}
		if result == nil {
			t.Fatalf("CheckFresh(%q) returned nil, want the check to run", v)
		}
		if !result.UpdateAvailable || result.LatestVersion != "v1.13.0" {
			t.Errorf("CheckFresh(%q) = %+v, want LatestVersion v1.13.0 with UpdateAvailable true", v, result)
		}
		if result.CurrentVersion != v {
			t.Errorf("CheckFresh(%q) CurrentVersion = %q, want the full stamp", v, result.CurrentVersion)
		}
	}
}

// The explicit path has a user waiting on it, so it must not give up on a
// merely slow connection the way the background check does.
func TestCheckFreshToleratesSlowResponse(t *testing.T) {
	t.Setenv("LNPM_STORE", t.TempDir())
	startAPIServer(t, func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(requestTimeout + 100*time.Millisecond)
		_, _ = w.Write([]byte(`{"tag_name": "v2.0.0"}`))
	})

	result, err := CheckFresh("1.0.0")
	if err != nil {
		t.Fatalf("CheckFresh returned error: %v", err)
	}
	if result == nil || result.LatestVersion != "v2.0.0" {
		t.Errorf("CheckFresh = %+v, want LatestVersion v2.0.0", result)
	}
}

func TestBackgroundRequestTimeoutStaysShort(t *testing.T) {
	// The background check runs alongside ordinary commands, so it must keep a
	// timeout short enough that a slow network never noticeably delays them.
	if requestTimeout > time.Second {
		t.Errorf("requestTimeout = %v, want at most 1s for the background check", requestTimeout)
	}
}

func TestFreshRequestTimeoutIsGenerous(t *testing.T) {
	// The explicit `lnpm update` check is interactive, so it must allow for a
	// slow connection rather than reporting a failure the user cannot act on.
	if freshRequestTimeout < 10*time.Second {
		t.Errorf("freshRequestTimeout = %v, want at least 10s", freshRequestTimeout)
	}
}

func TestCheckStaysSilentOnFetchFailure(t *testing.T) {
	t.Setenv("LNPM_STORE", t.TempDir())
	startAPIServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	if result := check("1.0.0"); result != nil {
		t.Errorf("check = %+v, want nil (the background check must fail silently)", result)
	}
}

// drain reads a result channel to completion, which also guarantees the
// background goroutine has finished before the test's cleanups run.
func drain(ch <-chan *Result) []*Result {
	var got []*Result
	for r := range ch {
		got = append(got, r)
	}
	return got
}

func TestCheckAsyncDeliversAvailableUpdate(t *testing.T) {
	t.Setenv("LNPM_NO_UPDATE_CHECK", "")
	t.Setenv("LNPM_STORE", t.TempDir())
	startAPIServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name": "v9.0.0"}`))
	})

	got := drain(CheckAsync("1.0.0"))
	if len(got) != 1 {
		t.Fatalf("CheckAsync delivered %d results, want 1", len(got))
	}
	if got[0].LatestVersion != "v9.0.0" || !got[0].UpdateAvailable {
		t.Errorf("CheckAsync = %+v, want LatestVersion v9.0.0 with UpdateAvailable true", got[0])
	}
}

func TestCheckAsyncStaysSilentWhenUpToDate(t *testing.T) {
	t.Setenv("LNPM_NO_UPDATE_CHECK", "")
	t.Setenv("LNPM_STORE", t.TempDir())
	startAPIServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name": "v1.0.0"}`))
	})

	if got := drain(CheckAsync("1.0.0")); len(got) != 0 {
		t.Errorf("CheckAsync delivered %+v, want nothing when already up to date", got)
	}
}

func TestCheckAsyncDisabled(t *testing.T) {
	t.Setenv("LNPM_STORE", t.TempDir())
	startAPIServer(t, func(w http.ResponseWriter, _ *http.Request) {
		t.Error("CheckAsync made an HTTP request while disabled")
		w.WriteHeader(http.StatusInternalServerError)
	})

	t.Run("via LNPM_NO_UPDATE_CHECK", func(t *testing.T) {
		t.Setenv("LNPM_NO_UPDATE_CHECK", "1")
		if got := drain(CheckAsync("1.0.0")); len(got) != 0 {
			t.Errorf("CheckAsync delivered %+v, want nothing", got)
		}
	})

	t.Run("for dev builds", func(t *testing.T) {
		t.Setenv("LNPM_NO_UPDATE_CHECK", "")
		// A pseudo-version and an untagged `--always` sha name no release to
		// compare against, so they are skipped for the same reason "dev" is.
		for _, v := range []string{"dev", "", "v1.12.1-0.20260819061412-6d9902254937", "7079f81-dirty"} {
			if got := drain(CheckAsync(v)); len(got) != 0 {
				t.Errorf("CheckAsync(%q) delivered %+v, want nothing", v, got)
			}
		}
	})
}

// The notice a locally built binary used to get: `git describe` stamps
// "v1.12.0-53-g7079f81-dirty", semver ranks that below the plain v1.12.0 tag,
// and the background check offered to overwrite the newer local build with the
// older release tarball (#283).
func TestCheckAsyncNeverOffersADowngradeToAGitDescribeBuild(t *testing.T) {
	t.Setenv("LNPM_NO_UPDATE_CHECK", "")
	t.Setenv("LNPM_STORE", t.TempDir())
	startAPIServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name": "v1.12.0"}`))
	})

	if got := drain(CheckAsync("v1.12.0-53-g7079f81-dirty")); len(got) != 0 {
		t.Errorf("CheckAsync delivered %+v, want nothing for a build ahead of the latest release", got)
	}
}

// The other half of the same rule: the downgrade is suppressed, the check is
// not, so a genuinely newer release still reaches a git describe build.
func TestCheckAsyncStillReportsANewerReleaseToAGitDescribeBuild(t *testing.T) {
	t.Setenv("LNPM_NO_UPDATE_CHECK", "")
	t.Setenv("LNPM_STORE", t.TempDir())
	startAPIServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name": "v1.13.0"}`))
	})

	got := drain(CheckAsync("v1.12.0-53-g7079f81-dirty"))
	if len(got) != 1 {
		t.Fatalf("CheckAsync delivered %d results, want 1", len(got))
	}
	if got[0].LatestVersion != "v1.13.0" || !got[0].UpdateAvailable {
		t.Errorf("CheckAsync = %+v, want LatestVersion v1.13.0 with UpdateAvailable true", got[0])
	}
}

// setMaxAPIResponseBytes lowers the response cap for the duration of a test, so
// the over-cap path is exercised without streaming a megabyte through httptest.
//
// Don't use t.Parallel() in callers - this swaps a process-wide var.
func setMaxAPIResponseBytes(t *testing.T, limit int64) {
	t.Helper()

	prev := maxAPIResponseBytes
	maxAPIResponseBytes = limit
	t.Cleanup(func() { maxAPIResponseBytes = prev })
}

func TestMaxAPIResponseBytesDefault(t *testing.T) {
	// The tests below lower the cap, so the shipped value is only pinned here.
	// A real release payload is a few KiB.
	if want := int64(1 << 20); maxAPIResponseBytes != want {
		t.Errorf("maxAPIResponseBytes = %d, want %d (1 MiB)", maxAPIResponseBytes, want)
	}
}

// A body over the cap must be refused by name, not truncated: the first
// maxAPIResponseBytes of this response are `{"tag_name": "v9.9.9"}` followed by
// padding, so a cap that cut the body short instead of refusing it would decode
// a version the server never finished sending.
func TestFetchLatestVersionRefusesAnOversizedBody(t *testing.T) {
	const prefix = `{"tag_name": "v9.9.9"}`
	setMaxAPIResponseBytes(t, int64(len(prefix))+8)

	startAPIServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(prefix + "          "))
	})

	// context.Background() carries no deadline, so nothing here can fire on
	// time - only on size.
	got, err := fetchLatestVersion(context.Background())
	if err == nil {
		t.Fatalf("fetchLatestVersion = %q, want an error for a body over the cap", got)
	}
	if got != "" {
		t.Errorf("fetchLatestVersion = %q, want empty string on error", got)
	}

	var tooLarge *apiResponseTooLargeError
	if !errors.As(err, &tooLarge) {
		t.Fatalf("fetchLatestVersion error = %v (%T), want *apiResponseTooLargeError", err, err)
	}
	if !strings.Contains(err.Error(), strconv.FormatInt(maxAPIResponseBytes, 10)) {
		t.Errorf("error %q does not name the %d-byte limit", err, maxAPIResponseBytes)
	}
}

// The cap is a refusal, not a ceiling on what decodes: a body exactly at the
// limit is still read whole.
func TestFetchLatestVersionAcceptsABodyExactlyAtTheCap(t *testing.T) {
	const body = `{"tag_name": "v1.2.3"}`
	setMaxAPIResponseBytes(t, int64(len(body)))

	startAPIServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	})

	got, err := fetchLatestVersion(context.Background())
	if err != nil {
		t.Fatalf("fetchLatestVersion returned error: %v", err)
	}
	if got != "v1.2.3" {
		t.Errorf("fetchLatestVersion = %q, want v1.2.3", got)
	}
}

// CheckFresh surfaces the cap as an error rather than reporting no update, so
// the user can tell "could not check" apart from "already current".
func TestCheckFreshSurfacesTheResponseCap(t *testing.T) {
	t.Setenv("LNPM_STORE", t.TempDir())
	setMaxAPIResponseBytes(t, 8)

	startAPIServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name": "v9.9.9"}`))
	})

	result, err := CheckFresh("1.0.0")
	if err == nil {
		t.Fatalf("CheckFresh = %+v, want an error for a body over the cap", result)
	}
	if result != nil {
		t.Errorf("CheckFresh = %+v, want nil result on error", result)
	}

	var tooLarge *apiResponseTooLargeError
	if !errors.As(err, &tooLarge) {
		t.Errorf("CheckFresh error = %v (%T), want it to wrap *apiResponseTooLargeError", err, err)
	}
}

// A managed install is told the command that actually works. 'lnpm update'
// refuses on a Homebrew or Scoop binary and names 'brew upgrade lnpm' or
// 'scoop update lnpm' instead (#508), so a notice pointing at 'lnpm update'
// sends a managed user to a command whose only job is to redirect them - and
// the notice fires far more often than the command is run (#519).
//
// The message is asserted through printUpdateNotice rather than
// PrintUpdateNotice because the exported one writes to os.Stderr and reads the
// running binary's own install method, neither of which a test can choose.
func TestPrintUpdateNoticeNamesTheManagerCommandForAManagedInstall(t *testing.T) {
	tests := []struct {
		method installmethod.Method
		want   string
	}{
		{installmethod.Homebrew, "Run: brew upgrade lnpm\n"},
		{installmethod.Scoop, "Run: scoop update lnpm\n"},
		{installmethod.Go, "Run: lnpm update\n"},
		{installmethod.Binary, "Run: lnpm update\n"},
	}

	for _, tt := range tests {
		var buf bytes.Buffer
		printUpdateNotice(&buf, &Result{
			CurrentVersion:  "1.0.0",
			LatestVersion:   "2.0.0",
			UpdateAvailable: true,
		}, tt.method)

		got := buf.String()
		if !strings.Contains(got, "Update available: 1.0.0 → 2.0.0\n") {
			t.Errorf("printUpdateNotice for method %d = %q, want it to carry the version line", tt.method, got)
		}
		if !strings.Contains(got, tt.want) {
			t.Errorf("printUpdateNotice for method %d = %q, want it to contain %q", tt.method, got, tt.want)
		}
	}
}

// Nothing is printed when there is no update, and nothing is printed for a nil
// result - the notice runs on every command, so a background check that failed
// or found nothing must stay silent.
func TestPrintUpdateNoticeStaysSilentWithoutAnUpdate(t *testing.T) {
	for _, r := range []*Result{nil, {CurrentVersion: "2.0.0", LatestVersion: "2.0.0"}} {
		var buf bytes.Buffer
		printUpdateNotice(&buf, r, installmethod.Homebrew)
		if buf.Len() != 0 {
			t.Errorf("printUpdateNotice(%+v) wrote %q, want nothing", r, buf.String())
		}
	}
}
