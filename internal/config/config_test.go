package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGetStorePathHonorsConfig(t *testing.T) {
	dir := t.TempDir()
	want := filepath.Join(dir, "mystore")

	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("store_path: "+want+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LNPM_CONFIG", cfgPath)
	t.Setenv("LNPM_STORE", "") // empty is treated as unset, so config wins

	got, err := GetStorePath()
	if err != nil {
		t.Fatalf("GetStorePath: %v", err)
	}
	if got != want {
		t.Errorf("GetStorePath = %q, want %q (store_path config ignored)", got, want)
	}
}

func TestGetStorePathEnvWins(t *testing.T) {
	t.Setenv("LNPM_STORE", "/tmp/from-env")
	got, err := GetStorePath()
	if err != nil {
		t.Fatalf("GetStorePath: %v", err)
	}
	if got != "/tmp/from-env" {
		t.Errorf("GetStorePath = %q, want /tmp/from-env", got)
	}
}

// TestSaveConfigRoundTrip checks that everything SaveConfig writes comes back
// out of the file unchanged, and that the parent directory is created on the
// way.
//
// The read-back goes through loadConfigFile rather than LoadConfig on purpose:
// LoadConfig memoizes the first successful load in a package-level sync.Once
// for the life of the test binary, so it would either return another test's
// config or pin this one for everyone else. loadConfigFile reads the file every
// time, which is what a round-trip needs.
func TestSaveConfigRoundTrip(t *testing.T) {
	// "nested" does not exist yet, so a SaveConfig that skipped MkdirAll would
	// fail to write here.
	configPath := filepath.Join(t.TempDir(), "nested", "config.yaml")
	t.Setenv("LNPM_CONFIG", configPath)

	manageGitignore := false
	want := &Config{
		StorePath:       filepath.Join(t.TempDir(), "custom-store"),
		LinkMode:        "copy",
		ManageGitignore: &manageGitignore,
		Hooks:           HooksConfig{PostAdd: "echo added", SkipPrepare: true},
	}

	if err := SaveConfig(want); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	got, err := loadConfigFile()
	if err != nil {
		t.Fatalf("loadConfigFile: %v", err)
	}

	if got.StorePath != want.StorePath {
		t.Errorf("StorePath = %q, want %q", got.StorePath, want.StorePath)
	}
	if got.LinkMode != want.LinkMode {
		t.Errorf("LinkMode = %q, want %q", got.LinkMode, want.LinkMode)
	}
	if got.ManageGitignore == nil {
		t.Errorf("ManageGitignore = nil, want a pointer to false")
	} else if *got.ManageGitignore {
		t.Errorf("ManageGitignore = true, want false")
	}
	if got.ShouldManageGitignore() {
		t.Errorf("ShouldManageGitignore() = true, want false (saved value lost, fell back to the default)")
	}
	if got.Hooks.PostAdd != want.Hooks.PostAdd {
		t.Errorf("Hooks.PostAdd = %q, want %q", got.Hooks.PostAdd, want.Hooks.PostAdd)
	}
	if !got.Hooks.SkipPrepare {
		t.Errorf("Hooks.SkipPrepare = false, want true")
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read written config: %v", err)
	}
	raw := string(data)

	// The values, not just the keys, have to be on disk: a marshal that emitted
	// the key with an empty value would still round-trip past a key-only check.
	for _, line := range []string{
		"store_path: " + want.StorePath,
		"link_mode: copy",
		"manage_gitignore: false",
		"post_add: echo added",
		"skip_prepare: true",
	} {
		if !strings.Contains(raw, line) {
			t.Errorf("written config missing %q\n--- file ---\n%s", line, raw)
		}
	}

	// omitempty: keys for fields that were never set must not appear at all.
	for _, absent := range []string{"pre_publish", "post_publish", "skip_post_add"} {
		if strings.Contains(raw, absent) {
			t.Errorf("written config contains unset key %q (omitempty lost)\n--- file ---\n%s", absent, raw)
		}
	}
}

// TestSaveConfigOmitsEveryUnsetField pins the omitempty tags on every field: an
// all-zero Config must serialize to the empty mapping, with no keys at all.
func TestSaveConfigOmitsEveryUnsetField(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv("LNPM_CONFIG", configPath)

	if err := SaveConfig(&Config{}); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read written config: %v", err)
	}
	if got := strings.TrimSpace(string(data)); got != "{}" {
		t.Errorf("empty config serialized to %q, want \"{}\" (a field lost its omitempty)", got)
	}
}

func TestGetConfigPathHonorsEnv(t *testing.T) {
	want := filepath.Join(t.TempDir(), "elsewhere", "lnpm.yaml")
	t.Setenv("LNPM_CONFIG", want)

	if got := GetConfigPath(); got != want {
		t.Errorf("GetConfigPath() = %q, want %q", got, want)
	}
}

func TestGetConfigPathDefaultsUnderHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LNPM_CONFIG", "")   // empty is treated as unset, so the default applies
	t.Setenv("HOME", home)        // os.UserHomeDir on unix
	t.Setenv("USERPROFILE", home) // os.UserHomeDir on windows

	want := filepath.Join(home, ".lnpm", "config.yaml")
	if got := GetConfigPath(); got != want {
		t.Errorf("GetConfigPath() = %q, want %q", got, want)
	}
}

// TestDetectPackageManagerReadsLockfiles pins one of the two things lnpm does
// with a lockfile name: stat it in a project directory to choose which package
// manager to shell out to. The other is internal/workspace's
// parsePackageJSONWorkspace, which stats yarn.lock and bun.lockb in a workspace
// root to set Workspace.Type, a field whose only non-test readers print it in a
// progress line. internal/pack's comment on the hardReservedExcludes lockfile
// entries enumerates both; keep the two in step.
//
// It is a regression test for #399 rather than a test of anything #399 changed.
// That issue put package-lock.json, yarn.lock, pnpm-lock.yaml and bun.lockb into
// internal/pack's hard-reserved exclusion list, so those four names now mean
// "never publish this" in one package and "the project uses this package
// manager" in this one. The two questions are unrelated — this function stats a
// consuming project's own directory and never sees a packed set — and nothing
// here was touched, which is exactly the claim worth pinning before someone
// reconciles the two lists on the strength of the shared names.
//
// bun.lock is checked beside bun.lockb because DetectPackageManager accepts
// both spellings and internal/pack's list deliberately carries only bun.lockb,
// which npm's own list names.
func TestDetectPackageManagerReadsLockfiles(t *testing.T) {
	tests := []struct {
		lockfile string
		want     PackageManager
	}{
		{"package-lock.json", NPM},
		{"yarn.lock", Yarn},
		{"pnpm-lock.yaml", PNPM},
		{"bun.lockb", Bun},
		{"bun.lock", Bun},
	}

	for _, tt := range tests {
		t.Run(tt.lockfile, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, tt.lockfile), []byte("{}"), 0644); err != nil {
				t.Fatal(err)
			}

			if got := DetectPackageManager(dir); got != tt.want {
				t.Errorf("DetectPackageManager(dir with %s) = %v, want %v", tt.lockfile, got, tt.want)
			}
		})
	}

	// A project with no lockfile at all still falls back to npm.
	if got := DetectPackageManager(t.TempDir()); got != NPM {
		t.Errorf("DetectPackageManager(dir with no lockfile) = %v, want %v", got, NPM)
	}
}

func TestGetPackageStorePath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LNPM_STORE", dir)

	want := filepath.Join(dir, "store")
	got, err := GetPackageStorePath()
	if err != nil {
		t.Fatalf("GetPackageStorePath: %v", err)
	}
	if got != want {
		t.Errorf("GetPackageStorePath() = %q, want %q", got, want)
	}
}
