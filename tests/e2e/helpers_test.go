package e2e

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// newStore returns a fresh, isolated LNPM_STORE directory for a single test.
//
// Each test gets its OWN store (and therefore its own bbolt database). lnpm
// opens the database with an exclusive file lock, so sharing one store across
// t.Parallel() tests would serialize every binary invocation on that lock. A
// per-test store removes the contention entirely while keeping the store off
// the real ~/.lnpm. TestConcurrentProcessesSharedStore is the one deliberate
// exception — it shares a store precisely to exercise that lock.
func newStore(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// runLNPM runs the compiled lnpm binary with cmd.Dir=dir and an isolated
// LNPM_STORE, returning the combined output. It fails the test on a non-zero
// exit. Using cmd.Dir (not os.Chdir) keeps the tests t.Parallel()-safe.
//
// LNPM_CONFIG is pointed at a non-existent file inside the store so the run
// never picks up the developer's real ~/.lnpm/config.yaml.
func runLNPM(t *testing.T, store, dir string, args ...string) string {
	t.Helper()

	out, err := runLNPMErr(t, store, dir, args...)
	if err != nil {
		t.Fatalf("lnpm %s (dir=%s) failed: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return out
}

// runLNPMErr is runLNPM without the t.Fatalf: it returns the combined output
// and the exit error so the caller can inspect a failure instead of dying on
// it. Contention tests need this — a run that loses the store lock is a valid
// outcome to assert on, not a test failure.
//
// It is safe to call from a goroutine: it only uses t for t.Helper().
func runLNPMErr(t *testing.T, store, dir string, args ...string) (string, error) {
	t.Helper()

	cmd := exec.Command(lnpmBin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"LNPM_STORE="+store,
		"LNPM_CONFIG="+filepath.Join(store, "config.yaml"),
	)

	out, err := cmd.CombinedOutput()
	return string(out), err
}

// runNode runs `node -e <script>` with cmd.Dir=dir and returns trimmed stdout.
// It fails the test if node exits non-zero.
func runNode(t *testing.T, dir, script string) string {
	t.Helper()

	cmd := exec.Command("node", "-e", script)
	cmd.Dir = dir
	cmd.Env = os.Environ()

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node -e (dir=%s) failed: %v\n%s", dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// copyFixture copies a fixture tree to a fresh temp dir, skipping node_modules
// and .lnpm so a clean checkout-like state is reproduced. It returns the path
// to the copied root.
func copyFixture(t *testing.T, name string) string {
	t.Helper()

	_, thisFile, _, _ := runtime.Caller(0)
	src := filepath.Join(filepath.Dir(thisFile), "fixtures", name)
	dst := t.TempDir()

	if err := copyTree(src, dst); err != nil {
		t.Fatalf("failed to copy fixture %s: %v", name, err)
	}
	return dst
}

// copyTree recursively copies src into dst, skipping node_modules and .lnpm.
func copyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		// Skip transient/generated directories so fixtures stay clean.
		if info.IsDir() && (info.Name() == "node_modules" || info.Name() == ".lnpm") {
			return filepath.SkipDir
		}

		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(path, target, info.Mode())
	})
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

// writeFile overwrites a file with the given contents, creating parents.
func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("failed to create dir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("failed to write %s: %v", path, err)
	}
}

// assertSymlink fails the test unless path exists and is a symlink.
func assertSymlink(t *testing.T, path string) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("expected symlink at %s, but it does not exist: %v", path, err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected %s to be a symlink, but it is not (mode=%s)", path, info.Mode())
	}
}

// assertDepValue fails the test unless package.json at projectDir lists pkg in
// dependencies or devDependencies with the expected value.
func assertDepValue(t *testing.T, projectDir, pkg, want string) {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(projectDir, "package.json"))
	if err != nil {
		t.Fatalf("failed to read package.json in %s: %v", projectDir, err)
	}

	var manifest map[string]any
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("failed to parse package.json in %s: %v", projectDir, err)
	}

	for _, field := range []string{"dependencies", "devDependencies"} {
		deps, _ := manifest[field].(map[string]any)
		if v, ok := deps[pkg].(string); ok && v == want {
			return
		}
	}
	t.Fatalf("expected %s in package.json (%s) to be %q, manifest=%s", pkg, projectDir, want, data)
}
