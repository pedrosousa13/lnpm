package tests

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/cli"
	"github.com/pedrosousa13/lnpm/internal/db"
)

// hasYalc checks if yalc is installed
func hasYalc() bool {
	_, err := exec.LookPath("yalc")
	return err == nil
}

// hasRelativeDeps checks if relative-deps is installed
func hasRelativeDeps() bool {
	// relative-deps is a npm package, check if installed globally
	cmd := exec.Command("npx", "relative-deps", "--help")
	return cmd.Run() == nil
}

// createBenchPackage creates a package with specified file count
func createBenchPackage(b *testing.B, name string, fileCount int) string {
	b.Helper()

	dir := b.TempDir()
	pkgDir := filepath.Join(dir, name)
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		b.Fatalf("Failed to create dir: %v", err)
	}

	// Create package.json
	pkgJSON := fmt.Sprintf(`{"name":"%s","version":"1.0.0"}`, name)
	if err := os.WriteFile(filepath.Join(pkgDir, "package.json"), []byte(pkgJSON), 0644); err != nil {
		b.Fatalf("Failed to write package.json: %v", err)
	}

	// Create source files
	for i := 0; i < fileCount; i++ {
		content := fmt.Sprintf("module.exports = { id: %d, data: '%s' };", i, randomString(100))
		filename := fmt.Sprintf("file-%d.js", i)
		if err := os.WriteFile(filepath.Join(pkgDir, filename), []byte(content), 0644); err != nil {
			b.Fatalf("Failed to write file: %v", err)
		}
	}

	return pkgDir
}

// randomString generates a deterministic string for benchmarks
func randomString(length int) string {
	result := make([]byte, length)
	for i := range result {
		result[i] = 'a' + byte(i%26)
	}
	return string(result)
}

// BenchmarkLnpmPublish benchmarks lnpm publish with 100 files
func BenchmarkLnpmPublish(b *testing.B) {
	// Reset database for each run
	storeDir := b.TempDir()
	b.Setenv("LNPM_STORE", storeDir)
	db.ResetForTesting()

	pkgDir := createBenchPackage(b, "bench-publish", 100)

	if err := os.Chdir(pkgDir); err != nil {
		b.Fatalf("Failed to chdir: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Reset database between iterations
		db.ResetForTesting()

		if err := cli.RunPublish(false, false, true, true); err != nil {
			b.Fatalf("Publish failed: %v", err)
		}
	}
}

// BenchmarkLnpmAdd benchmarks lnpm add
func BenchmarkLnpmAdd(b *testing.B) {
	storeDir := b.TempDir()
	b.Setenv("LNPM_STORE", storeDir)
	db.ResetForTesting()

	// Publish package first
	pkgDir := createBenchPackage(b, "bench-add", 100)
	if err := os.Chdir(pkgDir); err != nil {
		b.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPublish(false, false, true, true); err != nil {
		b.Fatalf("Publish failed: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Create fresh project for each iteration
		projectDir := filepath.Join(b.TempDir(), fmt.Sprintf("project-%d", i))
		if err := os.MkdirAll(projectDir, 0755); err != nil {
			b.Fatalf("Failed to create project dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(projectDir, "package.json"),
			[]byte(`{"name":"bench-project","version":"1.0.0"}`), 0644); err != nil {
			b.Fatalf("Failed to write package.json: %v", err)
		}

		if err := os.Chdir(projectDir); err != nil {
			b.Fatalf("Failed to chdir: %v", err)
		}

		if err := cli.RunAdd("bench-add", false, false, false); err != nil {
			b.Fatalf("Add failed: %v", err)
		}
	}
}

// BenchmarkLnpmPush benchmarks lnpm push
func BenchmarkLnpmPush(b *testing.B) {
	storeDir := b.TempDir()
	b.Setenv("LNPM_STORE", storeDir)
	db.ResetForTesting()

	// Setup: publish and link
	pkgDir := createBenchPackage(b, "bench-push", 100)
	if err := os.Chdir(pkgDir); err != nil {
		b.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPublish(false, false, true, true); err != nil {
		b.Fatalf("Publish failed: %v", err)
	}

	projectDir := filepath.Join(b.TempDir(), "project")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		b.Fatalf("Failed to create project: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "package.json"),
		[]byte(`{"name":"bench-project","version":"1.0.0"}`), 0644); err != nil {
		b.Fatalf("Failed to write package.json: %v", err)
	}
	if err := os.Chdir(projectDir); err != nil {
		b.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunAdd("bench-push", false, false, false); err != nil {
		b.Fatalf("Add failed: %v", err)
	}

	if err := os.Chdir(pkgDir); err != nil {
		b.Fatalf("Failed to chdir: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Modify a file to simulate real push
		content := fmt.Sprintf("module.exports = { iteration: %d };", i)
		if err := os.WriteFile(filepath.Join(pkgDir, "file-0.js"), []byte(content), 0644); err != nil {
			b.Fatalf("Failed to modify file: %v", err)
		}

		if err := cli.RunPush(true); err != nil {
			b.Fatalf("Push failed: %v", err)
		}
	}
}

// BenchmarkYalcPublish benchmarks yalc publish for comparison
func BenchmarkYalcPublish(b *testing.B) {
	if !hasYalc() {
		b.Skip("yalc not installed")
	}

	pkgDir := createBenchPackage(b, "yalc-bench-pub", 100)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cmd := exec.Command("yalc", "publish", "--no-scripts")
		cmd.Dir = pkgDir
		cmd.Stdout = nil
		cmd.Stderr = nil
		if err := cmd.Run(); err != nil {
			b.Fatalf("yalc publish failed: %v", err)
		}
	}
}

// BenchmarkYalcAdd benchmarks yalc add for comparison
func BenchmarkYalcAdd(b *testing.B) {
	if !hasYalc() {
		b.Skip("yalc not installed")
	}

	pkgDir := createBenchPackage(b, "yalc-bench-add", 100)

	// Publish first
	cmd := exec.Command("yalc", "publish", "--no-scripts")
	cmd.Dir = pkgDir
	if err := cmd.Run(); err != nil {
		b.Fatalf("yalc publish failed: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		projectDir := filepath.Join(b.TempDir(), fmt.Sprintf("yalc-proj-%d", i))
		if err := os.MkdirAll(projectDir, 0755); err != nil {
			b.Fatalf("Failed to create project: %v", err)
		}
		if err := os.WriteFile(filepath.Join(projectDir, "package.json"),
			[]byte(`{"name":"yalc-project","version":"1.0.0"}`), 0644); err != nil {
			b.Fatalf("Failed to write package.json: %v", err)
		}

		cmd := exec.Command("yalc", "add", "yalc-bench-add")
		cmd.Dir = projectDir
		cmd.Stdout = nil
		cmd.Stderr = nil
		if err := cmd.Run(); err != nil {
			b.Fatalf("yalc add failed: %v", err)
		}
	}
}

// BenchmarkRelativeDeps benchmarks relative-deps for comparison
func BenchmarkRelativeDeps(b *testing.B) {
	if !hasRelativeDeps() {
		b.Skip("relative-deps not installed")
	}

	pkgDir := createBenchPackage(b, "reldeps-bench", 100)

	// Create project with relativeDependencies config
	projectDir := filepath.Join(b.TempDir(), "project")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		b.Fatalf("Failed to create project: %v", err)
	}

	pkgJSON := fmt.Sprintf(`{
  "name": "reldeps-project",
  "version": "1.0.0",
  "relativeDependencies": {
    "reldeps-bench": "%s"
  }
}`, pkgDir)
	if err := os.WriteFile(filepath.Join(projectDir, "package.json"), []byte(pkgJSON), 0644); err != nil {
		b.Fatalf("Failed to write package.json: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cmd := exec.Command("npx", "relative-deps")
		cmd.Dir = projectDir
		cmd.Stdout = nil
		cmd.Stderr = nil
		if err := cmd.Run(); err != nil {
			b.Fatalf("relative-deps failed: %v", err)
		}
	}
}

// BenchmarkLnpmPublishLarge benchmarks with 500 files
func BenchmarkLnpmPublishLarge(b *testing.B) {
	storeDir := b.TempDir()
	b.Setenv("LNPM_STORE", storeDir)
	db.ResetForTesting()

	pkgDir := createBenchPackage(b, "bench-large", 500)

	if err := os.Chdir(pkgDir); err != nil {
		b.Fatalf("Failed to chdir: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		db.ResetForTesting()
		if err := cli.RunPublish(false, false, true, true); err != nil {
			b.Fatalf("Publish failed: %v", err)
		}
	}
}

// BenchmarkLnpmPublishSmall benchmarks with 10 files
func BenchmarkLnpmPublishSmall(b *testing.B) {
	storeDir := b.TempDir()
	b.Setenv("LNPM_STORE", storeDir)
	db.ResetForTesting()

	pkgDir := createBenchPackage(b, "bench-small", 10)

	if err := os.Chdir(pkgDir); err != nil {
		b.Fatalf("Failed to chdir: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		db.ResetForTesting()
		if err := cli.RunPublish(false, false, true, true); err != nil {
			b.Fatalf("Publish failed: %v", err)
		}
	}
}

// BenchmarkLnpmPushMultipleProjects benchmarks push to 5 linked projects
func BenchmarkLnpmPushMultipleProjects(b *testing.B) {
	storeDir := b.TempDir()
	b.Setenv("LNPM_STORE", storeDir)
	db.ResetForTesting()

	// Setup: publish and link to 5 projects
	pkgDir := createBenchPackage(b, "bench-multi-push", 100)
	if err := os.Chdir(pkgDir); err != nil {
		b.Fatalf("Failed to chdir: %v", err)
	}
	if err := cli.RunPublish(false, false, true, true); err != nil {
		b.Fatalf("Publish failed: %v", err)
	}

	for i := 0; i < 5; i++ {
		projectDir := filepath.Join(b.TempDir(), fmt.Sprintf("multi-proj-%d", i))
		if err := os.MkdirAll(projectDir, 0755); err != nil {
			b.Fatalf("Failed to create project: %v", err)
		}
		if err := os.WriteFile(filepath.Join(projectDir, "package.json"),
			[]byte(fmt.Sprintf(`{"name":"multi-project-%d","version":"1.0.0"}`, i)), 0644); err != nil {
			b.Fatalf("Failed to write package.json: %v", err)
		}
		if err := os.Chdir(projectDir); err != nil {
			b.Fatalf("Failed to chdir: %v", err)
		}
		if err := cli.RunAdd("bench-multi-push", false, false, false); err != nil {
			b.Fatalf("Add failed: %v", err)
		}
	}

	if err := os.Chdir(pkgDir); err != nil {
		b.Fatalf("Failed to chdir: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		content := fmt.Sprintf("module.exports = { iteration: %d };", i)
		if err := os.WriteFile(filepath.Join(pkgDir, "file-0.js"), []byte(content), 0644); err != nil {
			b.Fatalf("Failed to modify file: %v", err)
		}

		if err := cli.RunPush(true); err != nil {
			b.Fatalf("Push failed: %v", err)
		}
	}
}
