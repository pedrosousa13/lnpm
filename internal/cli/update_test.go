package cli

// Note: the download-verify-extract-swap sequence is covered here, but only
// through downloadAndInstall, which takes the binary to replace as an argument.
// The two functions above it - RunUpdate and installLatestViaBinary - resolve
// that binary through os.Executable(), which is process-global and would mean a
// test rewriting the test binary itself, so their own bodies stay uncovered and
// are exercised manually on each release.

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/installmethod"
)

func TestFindChecksum(t *testing.T) {
	checksums := `aaaa1111  lnpm_1.2.3_darwin_arm64.tar.gz
bbbb2222  lnpm_1.2.3_linux_amd64.tar.gz
cccc3333  lnpm_1.2.3_windows_amd64.zip
`
	sum, ok := findChecksum(strings.NewReader(checksums), "lnpm_1.2.3_linux_amd64.tar.gz")
	if !ok || sum != "bbbb2222" {
		t.Fatalf("findChecksum = (%q, %v), want (bbbb2222, true)", sum, ok)
	}

	if _, ok := findChecksum(strings.NewReader(checksums), "lnpm_9.9.9_linux_amd64.tar.gz"); ok {
		t.Error("findChecksum found a checksum for a missing file")
	}
}

func TestSha256File(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(p, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	// SHA-256("hello")
	const want = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	got, err := sha256File(p)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("sha256File = %s, want %s", got, want)
	}
}

func TestReleaseBaseURLDefault(t *testing.T) {
	// The base URL was extracted into a var purely for testability; the default
	// must stay byte-identical to the previously hardcoded release root.
	if releaseBaseURL != "https://github.com/pedrosousa13/lnpm/releases/download" {
		t.Errorf("releaseBaseURL = %q, want https://github.com/pedrosousa13/lnpm/releases/download", releaseBaseURL)
	}
}

// failingTransport makes every outbound request fail, standing in for a network
// that cannot reach the GitHub API.
type failingTransport struct{}

func (failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("simulated network failure")
}

// An update check that never reached GitHub used to be reported as "Already up
// to date" with exit code 0, leaving users on an outdated version believing they
// were current.
//
// Don't use t.Parallel() here - this swaps the process-wide default transport.
func TestRunUpdateReportsCheckFailure(t *testing.T) {
	t.Setenv("LNPM_STORE", t.TempDir())

	prev := http.DefaultTransport
	http.DefaultTransport = failingTransport{}
	t.Cleanup(func() { http.DefaultTransport = prev })

	err := RunUpdate(true, false, "1.0.0")
	if err == nil {
		t.Fatal("RunUpdate = nil, want an error when the update check cannot reach GitHub")
	}
	if !strings.Contains(err.Error(), "failed to check for updates") {
		t.Errorf("RunUpdate error = %q, want it to contain %q", err, "failed to check for updates")
	}
}

// RunUpdate refuses to run for a build that names no release to update from.
// The guard used to compare against "dev" and "" alone, which let a
// pseudo-version through to a version comparison it cannot win.
//
// Don't use t.Parallel() here - this swaps the process-wide default transport.
func TestRunUpdateRefusesBuildsWithNoRelease(t *testing.T) {
	t.Setenv("LNPM_STORE", t.TempDir())

	prev := http.DefaultTransport
	http.DefaultTransport = failingTransport{}
	t.Cleanup(func() { http.DefaultTransport = prev })

	for _, v := range []string{"dev", "", "v1.12.1-0.20260819061412-6d9902254937", "7079f81-dirty"} {
		err := RunUpdate(true, false, v)
		if err == nil || !strings.Contains(err.Error(), "not supported for dev builds") {
			t.Errorf("RunUpdate(true, false, %q) error = %v, want the dev-build refusal", v, err)
		}
	}
}

// A dev build that names the release it came from is checked rather than
// refused - it just never finds an upgrade in the tag it is already ahead of
// (#283). Reaching the network failure below is what proves the guard let it
// through.
//
// Don't use t.Parallel() here - this swaps the process-wide default transport.
func TestRunUpdateChecksDevBuildsThatNameARelease(t *testing.T) {
	t.Setenv("LNPM_STORE", t.TempDir())

	prev := http.DefaultTransport
	http.DefaultTransport = failingTransport{}
	t.Cleanup(func() { http.DefaultTransport = prev })

	for _, v := range []string{"v1.12.0-53-g7079f81-dirty", "v1.11.0+dirty"} {
		err := RunUpdate(true, false, v)
		if err == nil || !strings.Contains(err.Error(), "failed to check for updates") {
			t.Errorf("RunUpdate(true, false, %q) error = %v, want the update check to have run", v, err)
		}
	}
}

func TestBuildDownloadURL(t *testing.T) {
	ext := ".tar.gz"
	if runtime.GOOS == "windows" {
		ext = ".zip"
	}

	tests := []struct {
		name    string
		version string
		want    string
	}{
		{"bare version", "1.2.3", "1.2.3"},
		{"v-prefixed version", "v1.2.3", "1.2.3"},
		{"pre-release version", "v2.0.0-rc.1", "2.0.0-rc.1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantFilename := fmt.Sprintf("lnpm_%s_%s_%s%s", tt.want, runtime.GOOS, runtime.GOARCH, ext)
			wantURL := releaseBaseURL + "/v" + tt.want + "/" + wantFilename

			filename, url := buildDownloadURL(tt.version)
			if filename != wantFilename {
				t.Errorf("buildDownloadURL(%q) filename = %q, want %q", tt.version, filename, wantFilename)
			}
			if url != wantURL {
				t.Errorf("buildDownloadURL(%q) url = %q, want %q", tt.version, url, wantURL)
			}
		})
	}
}

func TestBuildChecksumsURL(t *testing.T) {
	tests := []struct {
		name    string
		version string
		want    string
	}{
		{"bare version", "1.2.3", releaseBaseURL + "/v1.2.3/checksums.txt"},
		{"v-prefixed version", "v1.2.3", releaseBaseURL + "/v1.2.3/checksums.txt"},
		{"pre-release version", "v2.0.0-rc.1", releaseBaseURL + "/v2.0.0-rc.1/checksums.txt"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := buildChecksumsURL(tt.version); got != tt.want {
				t.Errorf("buildChecksumsURL(%q) = %q, want %q", tt.version, got, tt.want)
			}
		})
	}
}

type archiveEntry struct {
	name    string
	content string
}

// writeTarGz builds a gzipped tar from entries and returns its path.
func writeTarGz(t *testing.T, entries []archiveEntry) string {
	t.Helper()

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for _, e := range entries {
		hdr := &tar.Header{Typeflag: tar.TypeReg, Name: e.name, Mode: 0755, Size: int64(len(e.content))}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(e.content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "archive.tar.gz")
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

// writeZip builds a zip from entries and returns its path.
func writeZip(t *testing.T, entries []archiveEntry) string {
	t.Helper()

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: e.name, Method: zip.Deflate})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(e.content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "archive.zip")
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

// extractDirs returns a destination directory nested two levels under a root,
// plus the path an entry named "../../evil/lnpm" would land on if the extractor
// failed to neutralise it. The escape directory is created up front so a
// vulnerable extractor would succeed in writing there, making the difference
// between "protected" and "vulnerable" observable.
func extractDirs(t *testing.T) (tmpDir, escapePath string) {
	t.Helper()

	root := t.TempDir()
	tmpDir = filepath.Join(root, "nested", "dest")
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "evil"), 0755); err != nil {
		t.Fatal(err)
	}
	return tmpDir, filepath.Join(root, "evil", "lnpm")
}

// assertExtracted checks the extractor returned a file directly inside tmpDir
// holding exactly want.
func assertExtracted(t *testing.T, got, tmpDir, want string) {
	t.Helper()

	if dir := filepath.Dir(got); dir != tmpDir {
		t.Errorf("extracted to %q, want a file directly inside %q", got, tmpDir)
	}
	data, err := os.ReadFile(got)
	if err != nil {
		t.Fatalf("reading extracted binary: %v", err)
	}
	if string(data) != want {
		t.Errorf("extracted content = %q, want %q", data, want)
	}
}

func TestExtractTarGzFindsBinary(t *testing.T) {
	archive := writeTarGz(t, []archiveEntry{
		{"README.md", "docs, not the binary"},
		{"lnpm", "binary-payload"},
	})
	tmpDir := t.TempDir()

	got, err := extractTarGz(archive, tmpDir)
	if err != nil {
		t.Fatalf("extractTarGz returned error: %v", err)
	}
	assertExtracted(t, got, tmpDir, "binary-payload")
}

func TestExtractTarGzBinaryMissing(t *testing.T) {
	archive := writeTarGz(t, []archiveEntry{
		{"README.md", "docs"},
		{"LICENSE", "license"},
	})

	got, err := extractTarGz(archive, t.TempDir())
	if err == nil {
		t.Fatalf("extractTarGz = %q, want an error when no binary is present", got)
	}
	if !strings.Contains(err.Error(), "lnpm binary not found in archive") {
		t.Errorf("error = %q, want it to mention the binary was not found", err)
	}
}

func TestExtractTarGzRejectsPathTraversal(t *testing.T) {
	archive := writeTarGz(t, []archiveEntry{{"../../evil/lnpm", "pwned"}})
	tmpDir, escapePath := extractDirs(t)

	got, err := extractTarGz(archive, tmpDir)
	if err != nil {
		t.Fatalf("extractTarGz returned error: %v", err)
	}
	assertExtracted(t, got, tmpDir, "pwned")
	if _, err := os.Stat(escapePath); !os.IsNotExist(err) {
		t.Errorf("traversal entry escaped the temp dir: %q exists (stat err %v)", escapePath, err)
	}
}

func TestExtractZipFindsBinary(t *testing.T) {
	archive := writeZip(t, []archiveEntry{
		{"README.md", "docs, not the binary"},
		{"lnpm.exe", "binary-payload"},
	})
	tmpDir := t.TempDir()

	got, err := extractZip(archive, tmpDir)
	if err != nil {
		t.Fatalf("extractZip returned error: %v", err)
	}
	assertExtracted(t, got, tmpDir, "binary-payload")
}

func TestExtractZipBinaryMissing(t *testing.T) {
	archive := writeZip(t, []archiveEntry{
		{"README.md", "docs"},
		{"LICENSE", "license"},
	})

	got, err := extractZip(archive, t.TempDir())
	if err == nil {
		t.Fatalf("extractZip = %q, want an error when no binary is present", got)
	}
	if !strings.Contains(err.Error(), "lnpm binary not found in archive") {
		t.Errorf("error = %q, want it to mention the binary was not found", err)
	}
}

func TestExtractZipRejectsPathTraversal(t *testing.T) {
	archive := writeZip(t, []archiveEntry{{"../../evil/lnpm", "pwned"}})
	tmpDir, escapePath := extractDirs(t)

	got, err := extractZip(archive, tmpDir)
	if err != nil {
		t.Fatalf("extractZip returned error: %v", err)
	}
	assertExtracted(t, got, tmpDir, "pwned")
	if _, err := os.Stat(escapePath); !os.IsNotExist(err) {
		t.Errorf("traversal entry escaped the temp dir: %q exists (stat err %v)", escapePath, err)
	}
}

func TestDownloadToFile(t *testing.T) {
	const payload = "release-archive-bytes"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(payload))
	}))
	defer srv.Close()

	dst := filepath.Join(t.TempDir(), "archive.tar.gz")
	if err := downloadToFile(srv.URL+"/archive.tar.gz", dst); err != nil {
		t.Fatalf("downloadToFile returned error: %v", err)
	}

	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != payload {
		t.Errorf("downloaded content = %q, want %q", data, payload)
	}
}

func TestDownloadToFileNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	err := downloadToFile(srv.URL+"/missing.tar.gz", filepath.Join(t.TempDir(), "out"))
	if err == nil {
		t.Fatal("downloadToFile returned nil, want an error on 404")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error = %q, want it to mention status 404", err)
	}
}

// trustTestServerCert points the shared update client's transport at srv's
// certificate for the duration of the test. The client - and so the redirect
// policy under test - stays the single one the updater uses; only the trust
// root moves, because httptest's certificate is not in the system roots.
//
// Every httptest TLS server presents the same built-in certificate, so one
// server's transport trusts another's too. That is what lets a redirect cross
// between two of them, and it was run rather than reasoned about, on
// 2026-08-28, with a scratch pair whose redirector was plaintext http so that
// only the https destination's certificate was in question:
//
//   - trusting no certificate, the destination hop fails on the certificate and
//     never reaches the policy - Get "https://127.0.0.1:38947/asset": tls:
//     failed to verify certificate: x509: certificate signed by unknown
//     authority.
//   - trusting a third, unrelated httptest server - not the destination and not
//     the redirector - the same fetch returns the destination's body. One
//     server's certificate really is every server's.
//
// So a cross-host row going green here is evidence about the policy: the
// certificate cannot be what is carrying it, and an untrusted one would have
// failed loudly and differently.
//
// Don't use t.Parallel() in callers - this swaps a field of a package-wide var.
func trustTestServerCert(t *testing.T, srv *httptest.Server) {
	t.Helper()

	prev := updateHTTPClient.Transport
	updateHTTPClient.Transport = srv.Client().Transport
	t.Cleanup(func() { updateHTTPClient.Transport = prev })
}

// redirectTo starts an https server answering every request with a 302 to
// target. It is https because the policy under test only refuses a destination
// that is not https - the hop it came from is never consulted - so a plaintext
// redirector would test nothing the destination does not already decide.
func redirectTo(t *testing.T, target string) *httptest.Server {
	t.Helper()

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target, http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// An on-path attacker who can inject one redirect could move either release
// fetch to a plaintext http destination and serve arbitrary bytes over what
// looks to the user like a TLS-protected exchange. verifyRelease still refuses
// anything not signed by an embedded trusted key, so this is not an integrity
// break - what it changes is who can reach the byte-count limits the reads are
// bounded by, from whoever serves GitHub's asset bytes to anyone on the path.
//
// Both fetch paths are checked because both use the one shared client, and a
// policy attached anywhere narrower would leave one of them open. Measured on
// 2026-08-28 with the scheme guard removed from checkUpdateRedirect and nothing
// else: both rows here go red on "got nil error", they are the only failures in
// any package, go vet is clean, and internal/cli prints FAIL with a duration
// rather than [build failed].
//
// Don't use t.Parallel() here - trustTestServerCert swaps a package-wide field.
func TestUpdateClientRefusesARedirectThatDowngradesToHTTP(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("attacker-bytes"))
	}))
	t.Cleanup(plain.Close)

	srv := redirectTo(t, plain.URL+"/checksums.txt")
	trustTestServerCert(t, srv)

	assertRefused := func(t *testing.T, err error) {
		t.Helper()
		if err == nil {
			t.Fatal("got nil error, want the https -> http redirect to be refused")
		}
		// net/http wraps a CheckRedirect error in a *url.Error carrying the
		// refused Location, so err.Error() here is the whole string the user is
		// shown - including the destination, which checkUpdateRedirect
		// therefore does not repeat.
		if !strings.Contains(err.Error(), "must stay on https") {
			t.Errorf("error = %q, want it to say the download must stay on https", err)
		}
		if !strings.Contains(err.Error(), plain.URL) {
			t.Errorf("error = %q, want it to name the refused destination %q", err, plain.URL)
		}
	}

	t.Run("metadata fetch", func(t *testing.T) {
		body, err := fetchReleaseAsset(srv.URL + "/checksums.txt")
		assertRefused(t, err)
		if len(body) != 0 {
			t.Errorf("fetchReleaseAsset returned %d bytes, want none", len(body))
		}
	})

	t.Run("archive download", func(t *testing.T) {
		dst := filepath.Join(t.TempDir(), "archive.tar.gz")
		assertRefused(t, downloadToFile(srv.URL+"/archive.tar.gz", dst))
	})
}

// Release downloads legitimately redirect to a separate asset host, so a policy
// that refused cross-host hops would break every real update. The two servers
// here differ by port rather than by name - httptest's certificate carries one
// name - so this pins the hop against a same-host rule; the DNS-name shape is
// TestUpdateRedirectPolicyAllowsTheGitHubAssetHostHop below.
//
// This cannot go red against the unfixed code: net/http's default policy
// follows this redirect too. What it catches is the fix over-restricting -
// measured on 2026-08-28 by adding a via[0].URL.Host != req.URL.Host refusal to
// checkUpdateRedirect, which turns both rows here red on "refused this
// redirect: it leaves 127.0.0.1:<port>" along with the DNS-name row below, with
// go vet clean and internal/cli printing FAIL with a duration.
//
// Don't use t.Parallel() here - trustTestServerCert swaps a package-wide field.
func TestUpdateClientFollowsAnHTTPSRedirectAcrossHosts(t *testing.T) {
	const payload = "release-archive-bytes"
	dest := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(payload))
	}))
	t.Cleanup(dest.Close)

	srv := redirectTo(t, dest.URL+"/asset")
	trustTestServerCert(t, srv)
	if srv.URL == dest.URL {
		t.Fatalf("both test servers share the URL %q, so no host is crossed", srv.URL)
	}

	t.Run("metadata fetch", func(t *testing.T) {
		got, err := fetchReleaseAsset(srv.URL + "/checksums.txt")
		if err != nil {
			t.Fatalf("fetchReleaseAsset returned error: %v", err)
		}
		if string(got) != payload {
			t.Errorf("fetched %q, want %q", got, payload)
		}
	})

	t.Run("archive download", func(t *testing.T) {
		dst := filepath.Join(t.TempDir(), "archive.tar.gz")
		if err := downloadToFile(srv.URL+"/archive.tar.gz", dst); err != nil {
			t.Fatalf("downloadToFile returned error: %v", err)
		}
		data, err := os.ReadFile(dst)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != payload {
			t.Errorf("downloaded %q, want %q", data, payload)
		}
	})
}

// A server that redirects forever must fail rather than loop.
//
// This cannot go red against the unfixed code either: net/http's default policy
// stops at the same count, and the fix keeps that bound rather than changing
// it. It is here so a later edit to the policy cannot drop the bound - which it
// does catch. Measured on 2026-08-28 with the len(via) check deleted from
// checkUpdateRedirect: this test fails after 120.00s on the client's own
// timeout, "context deadline exceeded (Client.Timeout exceeded while awaiting
// headers)", the server having answered 769,649 requests.
//
// Don't use t.Parallel() here - trustTestServerCert swaps a package-wide field.
func TestUpdateClientBoundsTheRedirectChain(t *testing.T) {
	var requests int
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.Redirect(w, r, fmt.Sprintf("/hop%d", requests), http.StatusFound)
	}))
	srv.StartTLS()
	t.Cleanup(srv.Close)
	trustTestServerCert(t, srv)

	_, err := fetchReleaseAsset(srv.URL + "/checksums.txt")
	if err == nil {
		t.Fatal("fetchReleaseAsset returned nil against an endless redirect, want an error")
	}
	if !strings.Contains(err.Error(), "redirect") {
		t.Errorf("error = %q, want it to say the redirect chain was stopped", err)
	}
	// maxUpdateRedirects bounds len(via), which counts requests rather than
	// redirects, so the server may answer that many - the original plus the
	// maxUpdateRedirects-1 redirects that were followed. Measured on 2026-08-28:
	// exactly 10 requests, 9 redirects followed.
	if requests > maxUpdateRedirects {
		t.Errorf("server answered %d requests, want at most %d", requests, maxUpdateRedirects)
	}
}

// The real chain 'lnpm update' follows leaves github.com for the asset host, so
// the policy is asked about that exact pair directly - httptest's single-name
// certificate cannot express it end to end.
//
// Like the row above, this cannot go red against the unfixed code - net/http's
// default policy allows the hop too - and what it catches is the fix
// over-restricting. Measured on 2026-08-28 by adding a
// via[0].URL.Host != req.URL.Host refusal to checkUpdateRedirect: this goes red
// on "CheckRedirect refused the https asset-host hop: refused this redirect: it
// leaves github.com", with go vet clean and internal/cli printing FAIL with a
// duration rather than [build failed].
func TestUpdateRedirectPolicyAllowsTheGitHubAssetHostHop(t *testing.T) {
	if updateHTTPClient.CheckRedirect == nil {
		t.Fatal("updateHTTPClient carries no redirect policy, so neither fetch path is governed by one")
	}

	from := mustRequest(t, releaseBaseURL+"/v1.2.3/lnpm_1.2.3_linux_amd64.tar.gz")
	to := mustRequest(t, "https://objects.githubusercontent.com/github-production-release-asset/1")

	if err := updateHTTPClient.CheckRedirect(to, []*http.Request{from}); err != nil {
		t.Errorf("CheckRedirect refused the https asset-host hop: %v", err)
	}
}

func mustRequest(t *testing.T, url string) *http.Request {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

// TestReleaseSizeLimitDefaults pins the shipped limits, because every other
// size test lowers them to keep the over-limit body small.
//
// What it uniquely catches is a default that is merely *wrong*, in either
// direction, rather than absurdly small. Measured on 2026-08-28, each direction
// preceded by a clean 'go vet ./...' and read for the package result line:
// doubling both defaults turns this test red and nothing else; halving both
// does the same. Only a drastic lowering is caught elsewhere - at 64 bytes for
// both, 13 other tests go red alongside this one, every one of them a
// verifyRelease or downloadAndInstall test whose fixture no longer fits.
func TestReleaseSizeLimitDefaults(t *testing.T) {
	if maxReleaseMetadataBytes != 1<<20 {
		t.Errorf("maxReleaseMetadataBytes = %d, want %d (1 MiB)", maxReleaseMetadataBytes, 1<<20)
	}
	if maxReleaseArchiveBytes != 256<<20 {
		t.Errorf("maxReleaseArchiveBytes = %d, want %d (256 MiB)", maxReleaseArchiveBytes, 256<<20)
	}
}

// setReleaseSizeLimits lowers both caps for the duration of the test, so an
// over-limit body can be a few hundred bytes instead of hundreds of megabytes.
//
// Don't use t.Parallel() in callers - these are process-wide vars.
func setReleaseSizeLimits(t *testing.T, metadata, archive int64) {
	t.Helper()

	prevMetadata, prevArchive := maxReleaseMetadataBytes, maxReleaseArchiveBytes
	maxReleaseMetadataBytes, maxReleaseArchiveBytes = metadata, archive
	t.Cleanup(func() {
		maxReleaseMetadataBytes, maxReleaseArchiveBytes = prevMetadata, prevArchive
	})
}

// serveBody starts a test server answering every request with body, and returns
// its base URL.
func serveBody(t *testing.T, body []byte) string {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestDownloadToFileRejectsOversizedArchive(t *testing.T) {
	setReleaseSizeLimits(t, 1<<20, 64)
	url := serveBody(t, bytes.Repeat([]byte("a"), 65))

	dst := filepath.Join(t.TempDir(), "archive.tar.gz")
	err := downloadToFile(url+"/archive.tar.gz", dst)
	if err == nil {
		t.Fatal("downloadToFile returned nil for a body one byte over the cap, want an error")
	}
	if !strings.Contains(err.Error(), "64") {
		t.Errorf("error = %q, want it to name the 64-byte limit it exceeded", err)
	}
}

func TestDownloadToFileAcceptsArchiveAtLimit(t *testing.T) {
	setReleaseSizeLimits(t, 1<<20, 64)
	payload := bytes.Repeat([]byte("a"), 64)
	url := serveBody(t, payload)

	dst := filepath.Join(t.TempDir(), "archive.tar.gz")
	if err := downloadToFile(url+"/archive.tar.gz", dst); err != nil {
		t.Fatalf("downloadToFile returned error for a body exactly at the cap: %v", err)
	}

	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, payload) {
		t.Errorf("downloaded %d bytes, want the whole %d-byte body", len(data), len(payload))
	}
}

// TestDownloadBinaryRejectsOversizedArchiveBeforeVerification is the row that
// makes the archive cap a refusal rather than a truncation. The release it
// serves publishes a signed checksums.txt listing the SHA-256 of the *first
// cap-many bytes* of the archive, so a cap that truncated silently would hand
// verifyRelease a file that verifies - measured on 2026-08-28 under a
// truncating read, which prints "Signature and checksum verified" - and the
// failure would then come from extraction rather than from the size limit
// ("gzip: invalid header", the truncated bytes not being a gzip stream). The
// failure must instead name the size limit.
func TestDownloadBinaryRejectsOversizedArchiveBeforeVerification(t *testing.T) {
	const limit = 64
	setReleaseSizeLimits(t, 1<<20, limit)

	filename := "lnpm_1.2.3_test_amd64.tar.gz"
	archive := bytes.Repeat([]byte("a"), limit+1)
	startReleaseArchiveServer(t, filename, archive, archiveChecksums(filename, archive[:limit]))

	_, tmpDir, err := downloadBinary("1.2.3", fmt.Sprintf("%s/v1.2.3/%s", releaseBaseURL, filename), filename)
	if err == nil {
		t.Fatal("downloadBinary returned nil for an over-limit archive, want an error")
	}
	if !strings.Contains(err.Error(), strconv.Itoa(limit)) {
		t.Errorf("error = %q, want it to name the %d-byte limit rather than fail verification", err, limit)
	}
	if _, statErr := os.Stat(tmpDir); !os.IsNotExist(statErr) {
		t.Errorf("temp dir %q survived the failed download (stat err %v)", tmpDir, statErr)
	}
}

func TestFetchReleaseAssetRejectsOversizedMetadata(t *testing.T) {
	setReleaseSizeLimits(t, 64, 256<<20)
	url := serveBody(t, bytes.Repeat([]byte("a"), 65))

	_, err := fetchReleaseAsset(url + "/checksums.txt")
	if err == nil {
		t.Fatal("fetchReleaseAsset returned nil for a body one byte over the cap, want an error")
	}
	if !strings.Contains(err.Error(), "64") {
		t.Errorf("error = %q, want it to name the 64-byte limit it exceeded", err)
	}
}

func TestFetchReleaseAssetAcceptsMetadataAtLimit(t *testing.T) {
	setReleaseSizeLimits(t, 64, 256<<20)
	payload := bytes.Repeat([]byte("a"), 64)
	url := serveBody(t, payload)

	got, err := fetchReleaseAsset(url + "/checksums.txt")
	if err != nil {
		t.Fatalf("fetchReleaseAsset returned error for a body exactly at the cap: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("fetched %d bytes, want the whole %d-byte body", len(got), len(payload))
	}
}

// TestVerifyReleaseRejectsOversizedChecksums establishes that the metadata cap
// refuses rather than truncates: it serves a checksums.txt padded past the cap
// with a trailing comment line, whose first cap-many bytes are the real, signed
// listing for the archive. A truncating read would verify; the capped one must
// fail naming the limit.
//
// The padding is appended *after* signing, and that is the whole point of not
// using startReleaseServer here - that helper signs whatever body it is given,
// so the signature would cover the padded body and the truncated prefix would
// be unsigned. Measured on 2026-08-28 with the cap made truncating
// (io.ReadAll over io.LimitReader at exactly maxReleaseMetadataBytes, no +1 and
// no error): this test fails on
//
//	verifyRelease returned nil for an over-limit checksums.txt, want an error
//
// so the truncated prefix really does verify and match, and the refusal is the
// only thing this test is left resting on. Signing the padded body instead -
// which is what startReleaseServer does - would fail on "not signed by any
// trusted key", red for a different reason than the one this test is about.
func TestVerifyReleaseRejectsOversizedChecksums(t *testing.T) {
	const filename = "lnpm_1.2.3_linux_amd64.tar.gz"
	path, checksums := archiveWithChecksums(t, filename)

	limit := int64(len(checksums))
	setReleaseSizeLimits(t, limit, 256<<20)

	key := trustNewReleaseKey(t)
	serveRelease(t, releaseHandler("", nil, checksums+strings.Repeat("#", 32)+"\n", signChecksums(t, key, checksums)))

	err := verifyRelease("1.2.3", filename, path)
	if err == nil {
		t.Fatal("verifyRelease returned nil for an over-limit checksums.txt, want an error")
	}
	if !strings.Contains(err.Error(), strconv.FormatInt(limit, 10)) {
		t.Errorf("error = %q, want it to name the %d-byte limit it exceeded", err, limit)
	}
}

// The metadata cap covers checksums.txt.sig as well as checksums.txt, and the
// two arrive at the user through different wrappers: the signature's failure
// goes through signatureUnavailableError, which is where "check your connection
// and try again" lives. That remedy is right for a dropped connection and wrong
// for an over-cap body, which is the same size on the next attempt too, so the
// size failure has to leave that wrapper by a different branch.
//
// The signature served here is one byte of filler past the cap rather than a
// real signature: the size check runs before anything reads the bytes, so a
// well-formed one would prove nothing extra, and a real ECDSA signature is
// around 70 bytes, which is under the cap this test can set.
func TestVerifyReleaseRejectsAnOversizedSignatureWithoutARetryRemedy(t *testing.T) {
	const filename = "lnpm_1.2.3_linux_amd64.tar.gz"
	path, checksums := archiveWithChecksums(t, filename)

	// The cap has to clear checksums.txt and refuse the signature, so it is set
	// to the listing's own length and the signature served one byte over it.
	limit := int64(len(checksums))
	setReleaseSizeLimits(t, limit, 256<<20)

	trustNewReleaseKey(t)
	serveRelease(t, releaseHandler("", nil, checksums, bytes.Repeat([]byte("s"), int(limit)+1)))

	err := verifyRelease("1.2.3", filename, path)
	if err == nil {
		t.Fatal("verifyRelease returned nil for an over-limit checksums.txt.sig, want an error")
	}
	if !strings.Contains(err.Error(), strconv.FormatInt(limit, 10)) {
		t.Errorf("error = %q, want it to name the %d-byte limit it exceeded", err, limit)
	}
	if strings.Contains(err.Error(), "try again") {
		t.Errorf("error = %q, want no retry remedy for a body that is over the cap on every attempt", err)
	}
	// The other two branches of signatureUnavailableError must not claim this
	// one: it is neither a release publishing no signature nor a fetch that
	// failed.
	for _, unwanted := range []string{"unsigned", "failed to fetch"} {
		if strings.Contains(err.Error(), unwanted) {
			t.Errorf("error = %q, want it not to contain %q", err, unwanted)
		}
	}
}

// serveRelease points releaseBaseURL at a local test server running h for the
// duration of the test.
//
// Don't use t.Parallel() in callers - this helper swaps the process-wide
// releaseBaseURL var.
func serveRelease(t *testing.T, h http.HandlerFunc) {
	t.Helper()

	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	prev := releaseBaseURL
	releaseBaseURL = srv.URL
	t.Cleanup(func() { releaseBaseURL = prev })
}

// newReleaseKey generates a P-256 key for a test release to sign its
// checksums.txt with. No key material is committed: every test makes its own.
func newReleaseKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

// trustReleaseKeys makes exactly these keys the ones the updater trusts, for
// the duration of the test.
//
// Don't use t.Parallel() in callers - this swaps the process-wide
// trustedReleaseKeys var.
func trustReleaseKeys(t *testing.T, keys ...*ecdsa.PublicKey) {
	t.Helper()

	prev := trustedReleaseKeys
	trustedReleaseKeys = func() ([]*ecdsa.PublicKey, error) { return keys, nil }
	t.Cleanup(func() { trustedReleaseKeys = prev })
}

// trustNewReleaseKey generates a signing key and makes it the only trusted one,
// which is what an ordinary well-formed release looks like to the updater.
func trustNewReleaseKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()

	key := newReleaseKey(t)
	trustReleaseKeys(t, &key.PublicKey)
	return key
}

// signChecksums returns the ASN.1 DER ECDSA signature over the SHA-256 of body,
// which is exactly what the release publishes as checksums.txt.sig.
func signChecksums(t *testing.T, key *ecdsa.PrivateKey, body string) []byte {
	t.Helper()

	digest := sha256.Sum256([]byte(body))
	sig, err := ecdsa.SignASN1(rand.Reader, key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return sig
}

// releaseHandler serves a release: checksums.txt, its signature, and - when
// filename is non-empty - the archive. A nil sig publishes no checksums.txt.sig
// at all, which is what an unsigned release looks like. Anything else 404s.
func releaseHandler(filename string, archive []byte, checksums string, sig []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/checksums.txt"):
			_, _ = w.Write([]byte(checksums))
		case strings.HasSuffix(r.URL.Path, "/checksums.txt.sig"):
			if sig == nil {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write(sig)
		case filename != "" && strings.HasSuffix(r.URL.Path, "/"+filename):
			_, _ = w.Write(archive)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}
}

// startReleaseServer serves a correctly signed release that publishes the given
// checksums.txt body and nothing else, so any request for an archive 404s.
func startReleaseServer(t *testing.T, checksums string) {
	t.Helper()

	key := trustNewReleaseKey(t)
	serveRelease(t, releaseHandler("", nil, checksums, signChecksums(t, key, checksums)))
}

// archiveFixtureContent is the stand-in for a release archive's bytes in the
// verification tests, which never extract it - only hash it.
const archiveFixtureContent = "archive-bytes"

// archiveWithChecksums writes an archive fixture to a temp file and returns its
// path alongside a checksums.txt body listing that file's real SHA-256 under
// filename.
//
// It serves nothing and signs nothing. What the release publishes, and which
// keys the updater trusts, are exactly what these tests vary, so the helper
// must not decide either.
func archiveWithChecksums(t *testing.T, filename string) (path, checksums string) {
	t.Helper()

	path = filepath.Join(t.TempDir(), filename)
	if err := os.WriteFile(path, []byte(archiveFixtureContent), 0644); err != nil {
		t.Fatal(err)
	}
	return path, archiveChecksums(filename, []byte(archiveFixtureContent))
}

func TestVerifyReleaseMatches(t *testing.T) {
	const filename = "lnpm_1.2.3_linux_amd64.tar.gz"
	path, checksums := archiveWithChecksums(t, filename)
	startReleaseServer(t, "0000  other-file.zip\n"+checksums)

	if err := verifyRelease("1.2.3", filename, path); err != nil {
		t.Errorf("verifyRelease returned error: %v", err)
	}
}

func TestVerifyReleaseMismatch(t *testing.T) {
	const filename = "lnpm_1.2.3_linux_amd64.tar.gz"
	path, _ := archiveWithChecksums(t, filename)
	tampered := strings.Repeat("ab", 32)
	startReleaseServer(t, fmt.Sprintf("%s  %s\n", tampered, filename))

	err := verifyRelease("1.2.3", filename, path)
	if err == nil {
		t.Fatal("verifyRelease returned nil for a tampered archive, want a mismatch error")
	}
	if !strings.Contains(err.Error(), "mismatch") {
		t.Errorf("error = %q, want it to mention a mismatch", err)
	}
}

func TestVerifyReleaseNotListed(t *testing.T) {
	const filename = "lnpm_1.2.3_linux_amd64.tar.gz"
	path, _ := archiveWithChecksums(t, filename)
	// The archive's own checksum, listed under a platform this run is not: the
	// entry verifyRelease looks for is absent.
	startReleaseServer(t, archiveChecksums("lnpm_1.2.3_darwin_arm64.tar.gz", []byte(archiveFixtureContent)))

	err := verifyRelease("1.2.3", filename, path)
	if err == nil {
		t.Fatal("verifyRelease returned nil for an unlisted file, want an error")
	}
	if !strings.Contains(err.Error(), "no checksum listed") {
		t.Errorf("error = %q, want it to mention no checksum was listed", err)
	}
}

// A checksums.txt is only worth trusting because a maintainer signed it. One
// signed by anybody else proves nothing, so it must be refused - this is the
// case an attacker who can serve their own release assets produces.
func TestVerifyReleaseRefusesASignatureFromAnUntrustedKey(t *testing.T) {
	const filename = "lnpm_1.2.3_linux_amd64.tar.gz"
	path, checksums := archiveWithChecksums(t, filename)

	attacker := newReleaseKey(t)
	trustReleaseKeys(t, &newReleaseKey(t).PublicKey)
	serveRelease(t, releaseHandler("", nil, checksums, signChecksums(t, attacker, checksums)))

	err := verifyRelease("1.2.3", filename, path)
	if err == nil {
		t.Fatal("verifyRelease returned nil for checksums signed by an untrusted key, want an error")
	}
	const want = "signature verification failed for checksums.txt: not signed by any trusted key"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to contain %q", err, want)
	}
}

// A signature is over specific bytes. Replaying a genuine signature over a
// mutated checksums.txt - swapping in the attacker's own archive hash - is the
// attack the signature exists to stop.
func TestVerifyReleaseRefusesTamperedChecksums(t *testing.T) {
	const filename = "lnpm_1.2.3_linux_amd64.tar.gz"
	path, checksums := archiveWithChecksums(t, filename)

	key := trustNewReleaseKey(t)
	sig := signChecksums(t, key, checksums)
	tampered := fmt.Sprintf("%s  %s\n", strings.Repeat("ab", 32), filename)
	serveRelease(t, releaseHandler("", nil, tampered, sig))

	err := verifyRelease("1.2.3", filename, path)
	if err == nil {
		t.Fatal("verifyRelease returned nil for a mutated checksums.txt, want an error")
	}
	if !strings.Contains(err.Error(), "not signed by any trusted key") {
		t.Errorf("error = %q, want it to report the signature did not verify", err)
	}
	// The mismatch must never be reached: the checksums are refused before
	// anything reads them.
	if strings.Contains(err.Error(), "mismatch") {
		t.Errorf("error = %q, want the tampered checksums refused before they are parsed", err)
	}
}

// 'lnpm update' only ever installs the latest release, so a release with no
// signature is not an old one - it is tampering or a broken release job. The
// message has to tell the user that rather than just failing.
func TestVerifyReleaseRefusesAReleaseWithNoSignature(t *testing.T) {
	const filename = "lnpm_1.2.3_linux_amd64.tar.gz"
	path, checksums := archiveWithChecksums(t, filename)

	trustReleaseKeys(t, &newReleaseKey(t).PublicKey)
	serveRelease(t, releaseHandler("", nil, checksums, nil))

	err := verifyRelease("1.2.3", filename, path)
	if err == nil {
		t.Fatal("verifyRelease returned nil for a release publishing no signature, want an error")
	}
	for _, want := range []string{"checksums.txt.sig", "unsigned", "will not be installed"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}
}

// A signature that cannot be fetched at all is a different problem from one the
// release never published, and the user's next move differs too - retry versus
// do not install. The two must not produce the same message.
func TestVerifyReleaseDistinguishesAFetchFailureFromAnUnsignedRelease(t *testing.T) {
	const filename = "lnpm_1.2.3_linux_amd64.tar.gz"
	path, checksums := archiveWithChecksums(t, filename)

	trustReleaseKeys(t, &newReleaseKey(t).PublicKey)
	// The signature request has its connection dropped mid-response, which is
	// what a network failure looks like to the client.
	serveRelease(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/checksums.txt.sig") {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = conn.Close()
			return
		}
		if strings.HasSuffix(r.URL.Path, "/checksums.txt") {
			_, _ = w.Write([]byte(checksums))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})

	err := verifyRelease("1.2.3", filename, path)
	if err == nil {
		t.Fatal("verifyRelease returned nil when the signature could not be fetched, want an error")
	}
	if !strings.Contains(err.Error(), "failed to fetch") {
		t.Errorf("error = %q, want it to report the fetch failed", err)
	}
	if strings.Contains(err.Error(), "unsigned") {
		t.Errorf("error = %q, want an unreachable signature not reported as an unsigned release", err)
	}
}

// Bytes that are not ASN.1 DER at all must be refused like any other bad
// signature, and must not take the process down on the way.
//
// The signature served here is the release's own - made by the trusted key over
// the exact checksums.txt served - with only its outer SEQUENCE tag cleared, so
// the DER framing is the single thing wrong with it. That is what separates
// this test from the untrusted-key and tampered-body ones: with the tag left
// alone the release verifies and this test fails, so nothing but the framing
// can be what refuses it. Corrupting a fresh unused key's signature instead
// would have been refused on the key mismatch alone.
func TestVerifyReleaseRefusesAMalformedSignature(t *testing.T) {
	const filename = "lnpm_1.2.3_linux_amd64.tar.gz"
	path, checksums := archiveWithChecksums(t, filename)

	key := trustNewReleaseKey(t)
	sig := signChecksums(t, key, checksums)
	if sig[0] != 0x30 {
		t.Fatalf("signature starts with %#x, want the DER SEQUENCE tag 0x30", sig[0])
	}
	sig[0] = 0x00
	serveRelease(t, releaseHandler("", nil, checksums, sig))

	err := verifyRelease("1.2.3", filename, path)
	if err == nil {
		t.Fatal("verifyRelease returned nil for a malformed signature, want an error")
	}
	if !strings.Contains(err.Error(), "not signed by any trusted key") {
		t.Errorf("error = %q, want it to report the signature did not verify", err)
	}
}

// The key set is a list so a key can be rotated without breaking updaters built
// against the old one: a release signed by any trusted key verifies, not only
// by the first.
func TestVerifyReleaseAcceptsASignatureFromAnyTrustedKey(t *testing.T) {
	const filename = "lnpm_1.2.3_linux_amd64.tar.gz"
	path, checksums := archiveWithChecksums(t, filename)

	retired := newReleaseKey(t)
	current := newReleaseKey(t)
	trustReleaseKeys(t, &retired.PublicKey, &current.PublicKey)
	serveRelease(t, releaseHandler("", nil, checksums, signChecksums(t, current, checksums)))

	if err := verifyRelease("1.2.3", filename, path); err != nil {
		t.Errorf("verifyRelease returned error for a release signed by the second trusted key: %v", err)
	}
}

// unverifiedUpdateWarning names the phrases the go-install path's warning has
// to carry: that this update is not signature-verified, which mechanism was
// used instead, and where the binary comes from. They are asserted as separate
// substrings rather than as one whole line so the wording can be reflowed
// without the test being about the line breaks.
var unverifiedUpdateWarning = []string{"not signature-verified", "go install", "module proxy"}

// The go-install branch of 'lnpm update' reaches no verification code at all:
// it shells out to 'go install', which builds from the module proxy rather than
// from the signed release asset the download branch checks. #475 settled that it
// warns and still proceeds, so this warning is the only thing that tells a user
// which of the two trust models their update got.
//
// PATH is emptied so that no 'go' can be found and nothing is really installed
// by the test. That makes the install fail, which is why the error is asserted
// too: the warning has to be printed before 'go install' is attempted, not after
// it has succeeded.
func TestInstallLatestViaGoWarnsThatItIsNotSignatureVerified(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	out := captureStdout(t, func() {
		if err := installLatestViaGo(); err == nil {
			t.Error("installLatestViaGo() = nil error with no 'go' on PATH, want the install to fail")
		}
	})

	for _, want := range unverifiedUpdateWarning {
		if !strings.Contains(out, want) {
			t.Errorf("installLatestViaGo() printed %q, which does not mention %q", out, want)
		}
	}
}

// The download branch does verify the signature, so it must not carry the
// warning that says nothing was verified.
//
// This assertion passes both before and after the warning was added - it is a
// control on where the warning is printed, not on whether it exists. Measured:
// hoisting the warning into downloadAndInstall turns it red, so it is not
// vacuous. Hoisting it into RunUpdate does not, because this test calls
// downloadAndInstall directly and never reaches RunUpdate - so the control
// covers the call site below it and nothing above.
func TestDownloadAndInstallDoesNotWarnThatItIsUnverified(t *testing.T) {
	const version = "1.2.3"
	filename, archive := releaseFixture(t, version, "new-binary")
	startReleaseArchiveServer(t, filename, archive, archiveChecksums(filename, archive))
	_, dst := newUpdateTarget(t)

	out := captureStdout(t, func() {
		if err := downloadAndInstall(version, dst); err != nil {
			t.Errorf("downloadAndInstall returned error: %v", err)
		}
	})

	if strings.Contains(out, unverifiedUpdateWarning[0]) {
		t.Errorf("downloadAndInstall printed %q, which claims the verified path is %q", out, unverifiedUpdateWarning[0])
	}
}

// writeInstallFixture lays down a source file in its own directory and a
// destination file in another, returning both paths.
func writeInstallFixture(t *testing.T, srcContent, dstContent string) (string, string) {
	t.Helper()

	src := filepath.Join(t.TempDir(), "lnpm-downloaded")
	if err := os.WriteFile(src, []byte(srcContent), 0644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "lnpm")
	if err := os.WriteFile(dst, []byte(dstContent), 0755); err != nil {
		t.Fatal(err)
	}
	return src, dst
}

// A successful install must not leave its staging file sitting next to the
// installed binary.
func TestInstallFileLeavesNoStagingFileOnSuccess(t *testing.T) {
	src, dst := writeInstallFixture(t, "new-binary", "old-binary")

	if err := installFile(src, dst); err != nil {
		t.Fatalf("installFile returned error: %v", err)
	}

	entries, err := os.ReadDir(filepath.Dir(dst))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(dst) {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("destination directory holds %v, want only %q", names, filepath.Base(dst))
	}
}

// A failure partway through the copy must not leave a half-written staging file
// next to the target.
func TestInstallFileRemovesStagingFileWhenTheCopyFails(t *testing.T) {
	_, dst := writeInstallFixture(t, "unused", "old-binary")
	// A directory can be opened but not read as a stream, so the copy fails
	// after the staging file has already been created.
	src := t.TempDir()

	if err := installFile(src, dst); err == nil {
		t.Fatal("installFile returned nil when the source could not be read, want an error")
	}

	entries, err := os.ReadDir(filepath.Dir(dst))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(dst) {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("destination directory holds %v after a failed install, want only %q", names, filepath.Base(dst))
	}
}

// A failed update must leave the user with a working lnpm: the original binary
// back at its own path, and no stray .bak beside it for the next update to trip
// over.
//
// A directory as the replacement makes the install step fail without touching
// the backup rename, so the restore path is what is under test.
func TestReplaceBinaryRestoresTheOriginalWhenTheInstallFails(t *testing.T) {
	_, dst := writeInstallFixture(t, "unused", "old-binary")
	unreadable := t.TempDir()

	if err := replaceBinary(unreadable, dst); err == nil {
		t.Fatal("replaceBinary returned nil when the new binary could not be read, want an error")
	}

	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("original binary is gone after a failed install: %v", err)
	}
	if string(data) != "old-binary" {
		t.Errorf("binary content = %q after a failed install, want the original %q", data, "old-binary")
	}

	if _, err := os.Stat(dst + ".bak"); !os.IsNotExist(err) {
		t.Errorf("backup %q still exists after a failed install (stat err %v)", dst+".bak", err)
	}
}

// archiveChecksums returns a checksums.txt body listing archive's real SHA-256
// under filename, computed independently of production code.
func archiveChecksums(filename string, archive []byte) string {
	sum := sha256.Sum256(archive)
	return fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), filename)
}

// startReleaseArchiveServer serves a whole correctly signed release: the
// archive bytes at any path ending in filename, plus the given checksums.txt
// body and a matching signature. Pass archiveChecksums(filename, archive) for a
// release that verifies, or any other body to make verification fail.
//
// It sits alongside startReleaseServer rather than replacing it because that
// one serves a release with no archive published at all, which is what makes a
// download fail; the releaseBaseURL plumbing both need lives in serveRelease.
func startReleaseArchiveServer(t *testing.T, filename string, archive []byte, checksums string) {
	t.Helper()

	key := trustNewReleaseKey(t)
	serveRelease(t, releaseHandler(filename, archive, checksums, signChecksums(t, key, checksums)))
}

// setTempDir points os.MkdirTemp's default location at dir, so a test can see
// everything the updater leaves behind. Unix reads TMPDIR; Windows reads TMP,
// then TEMP.
func setTempDir(t *testing.T, dir string) {
	t.Helper()

	t.Setenv("TMPDIR", dir)
	t.Setenv("TMP", dir)
	t.Setenv("TEMP", dir)
}

// releaseFixture builds an archive in this platform's release format holding a
// single lnpm binary, and returns the release filename alongside its bytes.
func releaseFixture(t *testing.T, version, content string) (string, []byte) {
	t.Helper()

	filename, _ := buildDownloadURL(version)

	var path string
	if strings.HasSuffix(filename, ".zip") {
		path = writeZip(t, []archiveEntry{{"lnpm.exe", content}})
	} else {
		path = writeTarGz(t, []archiveEntry{{"lnpm", content}})
	}

	archive, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return filename, archive
}

// originalBinary is what newUpdateTarget puts at the binary to be replaced, so
// a failed update can be checked for having left it alone.
const originalBinary = "old-binary"

// newUpdateTarget allocates a directory for the updater to download into and a
// binary for it to replace, then redirects os.MkdirTemp at that directory.
//
// Both are allocated before the redirect so neither ends up inside the
// directory that is later scanned for leftovers - which also means a test
// needing an archive fixture must build it before calling this.
func newUpdateTarget(t *testing.T) (systemTemp, dst string) {
	t.Helper()

	systemTemp = t.TempDir()
	_, dst = writeInstallFixture(t, "unused", originalBinary)
	setTempDir(t, systemTemp)
	return systemTemp, dst
}

// assertNoUpdateTempDirs fails the test if any updater temp directory is left
// under systemTemp, naming what it still holds.
func assertNoUpdateTempDirs(t *testing.T, systemTemp string) {
	t.Helper()

	entries, err := os.ReadDir(systemTemp)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "lnpm-update-") {
			continue
		}
		left, err := os.ReadDir(filepath.Join(systemTemp, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, l := range left {
			names = append(names, l.Name())
		}
		t.Errorf("temp directory %q survived, holding %v", e.Name(), names)
	}
}

// assertFailedUpdateCleanedUp checks that a failed update took its temp
// directory with it and left the user's binary untouched.
func assertFailedUpdateCleanedUp(t *testing.T, systemTemp, dst string) {
	t.Helper()

	assertNoUpdateTempDirs(t, systemTemp)

	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("reading the binary after a failed update: %v", err)
	}
	if string(data) != originalBinary {
		t.Errorf("binary = %q after a failed update, want the original %q", data, originalBinary)
	}
}

// A successful update used to remove only the extracted binary, leaving the
// temp directory and the multi-megabyte archive inside it on disk forever.
func TestDownloadAndInstallRemovesTheDownloadTempDir(t *testing.T) {
	const version = "1.2.3"
	filename, archive := releaseFixture(t, version, "new-binary")
	startReleaseArchiveServer(t, filename, archive, archiveChecksums(filename, archive))
	systemTemp, dst := newUpdateTarget(t)

	if err := downloadAndInstall(version, dst); err != nil {
		t.Fatalf("downloadAndInstall returned error: %v", err)
	}

	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("reading the installed binary: %v", err)
	}
	if string(data) != "new-binary" {
		t.Errorf("installed binary = %q, want %q", data, "new-binary")
	}

	assertNoUpdateTempDirs(t, systemTemp)
}

// A download that never produces an archive must not leave its temp directory
// behind either.
func TestDownloadAndInstallRemovesTheTempDirWhenTheDownloadFails(t *testing.T) {
	const version = "1.2.3"
	// A release with a checksums.txt but no archive published: the archive
	// request 404s.
	startReleaseServer(t, "")
	systemTemp, dst := newUpdateTarget(t)

	err := downloadAndInstall(version, dst)
	if err == nil {
		t.Fatal("downloadAndInstall returned nil when the archive could not be downloaded, want an error")
	}
	if !strings.Contains(err.Error(), "download failed") {
		t.Errorf("error = %q, want it to mention the download failed", err)
	}

	assertFailedUpdateCleanedUp(t, systemTemp, dst)
}

// An archive that fails verification is left on disk until the temp directory
// goes, so a tampered download must not survive the update that rejected it.
func TestDownloadAndInstallRemovesTheTempDirWhenTheChecksumIsWrong(t *testing.T) {
	const version = "1.2.3"
	filename, archive := releaseFixture(t, version, "new-binary")
	// A checksums.txt listing a sum this archive does not have, as a tampered
	// or corrupted release asset would produce.
	tampered := fmt.Sprintf("%s  %s\n", strings.Repeat("ab", 32), filename)
	startReleaseArchiveServer(t, filename, archive, tampered)
	systemTemp, dst := newUpdateTarget(t)

	err := downloadAndInstall(version, dst)
	if err == nil {
		t.Fatal("downloadAndInstall returned nil for an archive whose checksum does not match, want an error")
	}
	if !strings.Contains(err.Error(), "release verification failed") {
		t.Errorf("error = %q, want it to mention release verification failed", err)
	}

	assertFailedUpdateCleanedUp(t, systemTemp, dst)
}

// The line a user actually sees is the whole wrapped chain, so it is asserted
// whole: the wrapper must describe both kinds of refusal, and must not wrap
// twice or bury the reason.
func TestDownloadAndInstallReportsTheSignatureFailureItsUserSees(t *testing.T) {
	const version = "1.2.3"
	filename, archive := releaseFixture(t, version, "new-binary")
	checksums := archiveChecksums(filename, archive)
	attacker := newReleaseKey(t)
	trustReleaseKeys(t, &newReleaseKey(t).PublicKey)
	serveRelease(t, releaseHandler(filename, archive, checksums, signChecksums(t, attacker, checksums)))
	_, dst := newUpdateTarget(t)

	err := downloadAndInstall(version, dst)
	if err == nil {
		t.Fatal("downloadAndInstall returned nil for a release signed by an untrusted key, want an error")
	}
	const want = "release verification failed: signature verification failed for checksums.txt: not signed by any trusted key"
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}
}

// An unsigned release is not a checksum problem, so the wrapper must not tell
// the user it was one - that was the wrong-prefix bug this pins down.
func TestDownloadAndInstallReportsAnUnsignedReleaseAsUnsigned(t *testing.T) {
	const version = "1.2.3"
	filename, archive := releaseFixture(t, version, "new-binary")
	trustReleaseKeys(t, &newReleaseKey(t).PublicKey)
	serveRelease(t, releaseHandler(filename, archive, archiveChecksums(filename, archive), nil))
	systemTemp, dst := newUpdateTarget(t)

	err := downloadAndInstall(version, dst)
	if err == nil {
		t.Fatal("downloadAndInstall returned nil for a release publishing no signature, want an error")
	}
	if strings.Contains(err.Error(), "checksum verification failed") {
		t.Errorf("error = %q, want an unsigned release not reported as a checksum failure", err)
	}
	for _, want := range []string{"release verification failed", "unsigned", "will not be installed"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}

	assertFailedUpdateCleanedUp(t, systemTemp, dst)
}

// A release refused for its signature is refused before the archive is
// extracted or the binary swapped, so it must leave no more behind than a
// wrong checksum does.
func TestDownloadAndInstallRemovesTheTempDirWhenTheSignatureIsWrong(t *testing.T) {
	const version = "1.2.3"
	filename, archive := releaseFixture(t, version, "new-binary")
	checksums := archiveChecksums(filename, archive)
	// Checksums that match the archive perfectly, signed by a key nobody
	// trusts: only the signature can reject this release.
	attacker := newReleaseKey(t)
	trustReleaseKeys(t, &newReleaseKey(t).PublicKey)
	serveRelease(t, releaseHandler(filename, archive, checksums, signChecksums(t, attacker, checksums)))
	systemTemp, dst := newUpdateTarget(t)

	err := downloadAndInstall(version, dst)
	if err == nil {
		t.Fatal("downloadAndInstall returned nil for a release signed by an untrusted key, want an error")
	}
	if !strings.Contains(err.Error(), "not signed by any trusted key") {
		t.Errorf("error = %q, want it to report the signature did not verify", err)
	}

	assertFailedUpdateCleanedUp(t, systemTemp, dst)
}

// The last failure path: the archive downloads and verifies, and only then
// turns out to be unreadable.
func TestDownloadAndInstallRemovesTheTempDirWhenExtractionFails(t *testing.T) {
	const version = "1.2.3"
	filename, _ := buildDownloadURL(version)
	// Bytes that are neither a gzipped tar nor a zip, published under their own
	// real checksum so verification passes and extraction is what fails.
	archive := []byte("not an archive")
	startReleaseArchiveServer(t, filename, archive, archiveChecksums(filename, archive))
	systemTemp, dst := newUpdateTarget(t)

	// The message is left to the archive reader and differs between the tar.gz
	// and zip formats, so only the failure itself is asserted.
	if err := downloadAndInstall(version, dst); err == nil {
		t.Fatal("downloadAndInstall returned nil for an archive that cannot be read, want an error")
	}

	assertFailedUpdateCleanedUp(t, systemTemp, dst)
}

// The refusal is only useful if it names the command that does work, so that is
// what is asserted. RunUpdate's own dispatch is not reached from a test:
// installmethod.Current reads os.Executable(), which is the test binary, and the
// non-refusing branches would replace it.
func TestManagedInstallErrorNamesTheUpgradeCommand(t *testing.T) {
	for _, method := range []installmethod.Method{installmethod.Homebrew, installmethod.Scoop} {
		name, upgradeCommand, ok := method.Manager()
		if !ok {
			t.Fatalf("installmethod.Method(%d).Manager() reported no manager", method)
		}

		err := managedInstallError(name, upgradeCommand)
		for _, want := range []string{name, upgradeCommand, "lnpm update --force"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("managedInstallError(%q, %q) = %q, want it to contain %q", name, upgradeCommand, err, want)
			}
		}
	}
}
