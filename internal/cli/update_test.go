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
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
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

	err := RunUpdate(true, "1.0.0")
	if err == nil {
		t.Fatal("RunUpdate = nil, want an error when the update check cannot reach GitHub")
	}
	if !strings.Contains(err.Error(), "failed to check for updates") {
		t.Errorf("RunUpdate error = %q, want it to contain %q", err, "failed to check for updates")
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

// startReleaseServer serves a release that publishes the given checksums.txt
// body and nothing else, so any request for an archive 404s.
func startReleaseServer(t *testing.T, checksums string) {
	t.Helper()

	serveRelease(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/checksums.txt") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(checksums))
	})
}

// writeArchiveFixture writes content to a temp file and returns its path plus
// the hex SHA-256 of the content, computed independently of production code.
func writeArchiveFixture(t *testing.T, filename, content string) (string, string) {
	t.Helper()

	path := filepath.Join(t.TempDir(), filename)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(content))
	return path, hex.EncodeToString(sum[:])
}

func TestVerifyChecksumMatches(t *testing.T) {
	const filename = "lnpm_1.2.3_linux_amd64.tar.gz"
	path, sum := writeArchiveFixture(t, filename, "archive-bytes")
	startReleaseServer(t, fmt.Sprintf("0000  other-file.zip\n%s  %s\n", sum, filename))

	if err := verifyChecksum("1.2.3", filename, path); err != nil {
		t.Errorf("verifyChecksum returned error: %v", err)
	}
}

func TestVerifyChecksumMismatch(t *testing.T) {
	const filename = "lnpm_1.2.3_linux_amd64.tar.gz"
	path, _ := writeArchiveFixture(t, filename, "archive-bytes")
	tampered := strings.Repeat("ab", 32)
	startReleaseServer(t, fmt.Sprintf("%s  %s\n", tampered, filename))

	err := verifyChecksum("1.2.3", filename, path)
	if err == nil {
		t.Fatal("verifyChecksum returned nil for a tampered archive, want a mismatch error")
	}
	if !strings.Contains(err.Error(), "mismatch") {
		t.Errorf("error = %q, want it to mention a mismatch", err)
	}
}

func TestVerifyChecksumNotListed(t *testing.T) {
	const filename = "lnpm_1.2.3_linux_amd64.tar.gz"
	path, sum := writeArchiveFixture(t, filename, "archive-bytes")
	startReleaseServer(t, fmt.Sprintf("%s  lnpm_1.2.3_darwin_arm64.tar.gz\n", sum))

	err := verifyChecksum("1.2.3", filename, path)
	if err == nil {
		t.Fatal("verifyChecksum returned nil for an unlisted file, want an error")
	}
	if !strings.Contains(err.Error(), "no checksum listed") {
		t.Errorf("error = %q, want it to mention no checksum was listed", err)
	}
}

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

// startReleaseArchiveServer serves a whole release: the archive bytes at any
// path ending in filename, plus the given checksums.txt body. Pass
// archiveChecksums(filename, archive) for a release that verifies, or any other
// body to make verification fail.
//
// It sits alongside startReleaseServer rather than replacing it because that
// one serves a release with no archive published at all, which is what makes a
// download fail; the releaseBaseURL plumbing both need lives in serveRelease.
func startReleaseArchiveServer(t *testing.T, filename string, archive []byte, checksums string) {
	t.Helper()

	serveRelease(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/checksums.txt"):
			_, _ = w.Write([]byte(checksums))
		case strings.HasSuffix(r.URL.Path, "/"+filename):
			_, _ = w.Write(archive)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
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
	if !strings.Contains(err.Error(), "checksum verification failed") {
		t.Errorf("error = %q, want it to mention checksum verification failed", err)
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
