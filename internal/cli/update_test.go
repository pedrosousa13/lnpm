package cli

// Note: the binary swap itself (RunUpdate / installLatestViaBinary) is not
// covered here. It rewrites os.Executable() in place, which is process-global
// and not safely reversible inside a test run; it is exercised manually on
// each release instead.

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

// startReleaseServer points releaseBaseURL at a local test server serving the
// given checksums.txt body for the duration of the test.
//
// Don't use t.Parallel() in callers - this helper swaps the process-wide
// releaseBaseURL var.
func startReleaseServer(t *testing.T, checksums string) {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/checksums.txt") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(checksums))
	}))
	t.Cleanup(srv.Close)

	prev := releaseBaseURL
	releaseBaseURL = srv.URL
	t.Cleanup(func() { releaseBaseURL = prev })
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

// requirePermissionEnforcement skips tests that depend on the filesystem
// actually refusing an operation. root bypasses permission bits, and Windows
// does not model them the way these tests assume.
func requirePermissionEnforcement(t *testing.T) {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("permission bits are not enforced the same way on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root bypasses the permission checks this test relies on")
	}
}

// writeInstallFixture lays down a source file in its own directory and a
// destination file in another, returning both paths.
func writeInstallFixture(t *testing.T, srcContent, dstContent string) (src, dst string) {
	t.Helper()

	src = filepath.Join(t.TempDir(), "lnpm-downloaded")
	if err := os.WriteFile(src, []byte(srcContent), 0644); err != nil {
		t.Fatal(err)
	}
	dst = filepath.Join(t.TempDir(), "lnpm")
	if err := os.WriteFile(dst, []byte(dstContent), 0755); err != nil {
		t.Fatal(err)
	}
	return src, dst
}

// The downloaded binary lives under the system temp dir, which is frequently a
// different filesystem from the install location - there, renaming it onto the
// target fails with EXDEV and the update can never succeed. installFile must
// therefore stage inside the target's own directory and copy the bytes in.
//
// A source directory the process may not write stands in for that filesystem
// boundary: renaming a file out of a directory needs write permission on that
// directory, reading its bytes does not. An implementation that renames from
// the source cannot pass this test; one that copies into the destination
// directory does not care.
func TestInstallFileDoesNotRenameOutOfTheSourceDirectory(t *testing.T) {
	requirePermissionEnforcement(t)

	src, dst := writeInstallFixture(t, "new-binary", "old-binary")

	srcDir := filepath.Dir(src)
	if err := os.Chmod(srcDir, 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(srcDir, 0755) })

	if err := installFile(src, dst); err != nil {
		t.Fatalf("installFile returned error: %v", err)
	}

	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new-binary" {
		t.Errorf("installed content = %q, want %q", data, "new-binary")
	}
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

// The staging file is created by os.CreateTemp, which makes it 0600 and
// unusable as a binary. installFile owns making the installed file executable.
func TestInstallFileMakesTheInstalledBinaryExecutable(t *testing.T) {
	src, dst := writeInstallFixture(t, "new-binary", "old-binary")

	if err := installFile(src, dst); err != nil {
		t.Fatalf("installFile returned error: %v", err)
	}

	info, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0755 {
		t.Errorf("installed binary mode = %04o, want 0755", got)
	}
}

// Installing into a directory the user cannot write is the common "installed
// under /usr/local/bin" case. The error has to say what to do about it rather
// than surfacing a bare permission-denied on a temp file name the user has
// never seen.
func TestInstallFileReportsInsufficientPrivileges(t *testing.T) {
	requirePermissionEnforcement(t)

	src, dst := writeInstallFixture(t, "new-binary", "old-binary")

	dstDir := filepath.Dir(dst)
	if err := os.Chmod(dstDir, 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dstDir, 0755) })

	err := installFile(src, dst)
	if err == nil {
		t.Fatal("installFile returned nil for an unwritable destination directory, want an error")
	}
	if !strings.Contains(err.Error(), "privileges") {
		t.Errorf("error = %q, want it to tell the user to re-run with sufficient privileges", err)
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
