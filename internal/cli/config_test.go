package cli

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/config"
	"gopkg.in/yaml.v3"
)

func TestEditConfigSupportsEditorWithArguments(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell command quoting differs on Windows")
	}

	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config dir's", "config.yaml")
	argsPath := filepath.Join(tmp, "editor-args.txt")
	editorPath := filepath.Join(tmp, "fake-editor")

	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$LNPM_TEST_EDITOR_ARGS\"\n"
	if err := os.WriteFile(editorPath, []byte(script), 0755); err != nil {
		t.Fatalf("write fake editor: %v", err)
	}

	t.Setenv("EDITOR", editorPath+" --wait")
	t.Setenv("VISUAL", "")
	t.Setenv("LNPM_CONFIG", configPath)
	t.Setenv("LNPM_TEST_EDITOR_ARGS", argsPath)

	if err := editConfig(); err != nil {
		t.Fatalf("editConfig() error = %v", err)
	}

	data, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read editor args: %v", err)
	}

	args := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(args) != 2 {
		t.Fatalf("editor args = %#v, want --wait and config path", args)
	}
	if args[0] != "--wait" {
		t.Fatalf("first editor arg = %q, want --wait", args[0])
	}
	if args[1] != configPath {
		t.Fatalf("config path arg = %q, want %q", args[1], configPath)
	}
}

// captureStdout runs fn with os.Stdout redirected to a pipe and returns what it
// printed. The config subcommand helpers report their results on stdout and
// only return an error value, so asserting on anything they actually tell the
// user means reading the pipe.
//
// The reader goroutine starts before fn does, so fn can write more than the
// pipe buffer without deadlocking, and the teardown is deferred so os.Stdout is
// restored however fn exits.
func captureStdout(t *testing.T, fn func()) (out string) {
	t.Helper()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("create pipe: %v", err)
	}

	done := make(chan string, 1)
	go func() {
		data, _ := io.ReadAll(r)
		done <- string(data)
	}()

	orig := os.Stdout
	os.Stdout = w

	defer func() {
		os.Stdout = orig
		_ = w.Close() // unblocks the reader goroutine
		out = <-done
		_ = r.Close()
	}()

	fn()
	return
}

// readSavedConfig parses the config file setConfigKey wrote. It deliberately
// does not go through config.LoadConfig: that memoizes its first result in a
// package-level sync.Once for the life of the test binary, so it would not see
// a file written mid-test.
//
// What comes back is therefore the persisted bytes and nothing else — none of
// the defaults loadConfigFile applies on top. That is the point here: these
// tests assert what setConfigKey actually wrote, so a value supplied by a
// default rather than by the write cannot make one pass.
func readSavedConfig(t *testing.T, path string) *config.Config {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("config file was not written: %v", err)
	}

	var cfg config.Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse written config %q: %v", string(data), err)
	}
	return &cfg
}

// TestSetThenGetConfigKey walks every key the config subcommand supports:
// setting it must persist the value to disk, and getting it back — both from
// the in-memory config and from one re-parsed off disk — must print that value.
func TestSetThenGetConfigKey(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "some-store")

	tests := []struct {
		name     string
		key      string
		value    string
		wantYAML string // must appear verbatim in the written file
		wantGet  string // what getConfigKey prints
	}{
		{"store_path", "store_path", storePath, "store_path: " + storePath, storePath},
		{"link_mode copy", "link_mode", "copy", "link_mode: copy", "copy"},
		{"link_mode hardlink", "link_mode", "hardlink", "link_mode: hardlink", "hardlink"},
		{"manage_gitignore true", "manage_gitignore", "true", "manage_gitignore: true", "true"},
		{"manage_gitignore false", "manage_gitignore", "false", "manage_gitignore: false", "false"},
		{"hooks.pre_publish", "hooks.pre_publish", "echo pre", "pre_publish: echo pre", "echo pre"},
		{"hooks.post_publish", "hooks.post_publish", "echo post", "post_publish: echo post", "echo post"},
		{"hooks.post_add", "hooks.post_add", "npm ci", "post_add: npm ci", "npm ci"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			configPath := filepath.Join(t.TempDir(), "config.yaml")
			t.Setenv("LNPM_CONFIG", configPath)

			cfg := &config.Config{}
			var setErr error
			setOut := captureStdout(t, func() { setErr = setConfigKey(cfg, tc.key, tc.value) })
			if setErr != nil {
				t.Fatalf("setConfigKey(%q, %q) error = %v", tc.key, tc.value, setErr)
			}
			if want := "Set " + tc.key + " = " + tc.value; !strings.Contains(setOut, want) {
				t.Errorf("setConfigKey printed %q, want it to contain %q", setOut, want)
			}

			data, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatalf("config file was not written: %v", err)
			}
			if !strings.Contains(string(data), tc.wantYAML) {
				t.Errorf("written config missing %q\n--- file ---\n%s", tc.wantYAML, string(data))
			}

			// Once from the config setConfigKey mutated, once from the file it
			// saved, so a value that never reached disk cannot pass.
			for _, source := range []struct {
				label string
				cfg   *config.Config
			}{
				{"in-memory", cfg},
				{"from disk", readSavedConfig(t, configPath)},
			} {
				label, source := source.label, source.cfg
				var getErr error
				got := captureStdout(t, func() { getErr = getConfigKey(source, tc.key) })
				if getErr != nil {
					t.Fatalf("getConfigKey(%s, %q) error = %v", label, tc.key, getErr)
				}
				if strings.TrimSpace(got) != tc.wantGet {
					t.Errorf("getConfigKey(%s, %q) printed %q, want %q", label, tc.key, strings.TrimSpace(got), tc.wantGet)
				}
			}
		})
	}
}

// TestGetConfigKeyStorePathFallsBackToDefault covers the branch where
// store_path is unset in the config: the resolved store path is printed and
// marked as a default rather than printing nothing.
func TestGetConfigKeyStorePathFallsBackToDefault(t *testing.T) {
	store := t.TempDir()
	t.Setenv("LNPM_STORE", store) // resolves without touching config.LoadConfig

	var err error
	got := captureStdout(t, func() { err = getConfigKey(&config.Config{}, "store_path") })
	if err != nil {
		t.Fatalf("getConfigKey(store_path) error = %v", err)
	}
	if want := store + " (default)"; strings.TrimSpace(got) != want {
		t.Errorf("getConfigKey(store_path) printed %q, want %q", strings.TrimSpace(got), want)
	}
}

// TestSetConfigKeyValidation checks that every rejected input produces an error
// and, just as importantly, leaves no config file behind: a validation path
// that fell through to SaveConfig would persist the bad value.
func TestSetConfigKeyValidation(t *testing.T) {
	tests := []struct {
		name      string
		key       string
		value     string
		wantErrIn []string
		check     func(t *testing.T, cfg *config.Config)
	}{
		{
			name:      "link_mode rejects unknown mode",
			key:       "link_mode",
			value:     "banana",
			wantErrIn: []string{"link_mode", "hardlink", "copy"},
			check: func(t *testing.T, cfg *config.Config) {
				if cfg.LinkMode != "" {
					t.Errorf("LinkMode = %q, want it left untouched", cfg.LinkMode)
				}
			},
		},
		{
			name:      "manage_gitignore rejects non-boolean",
			key:       "manage_gitignore",
			value:     "not-a-bool",
			wantErrIn: []string{"manage_gitignore", "boolean"},
			check: func(t *testing.T, cfg *config.Config) {
				if cfg.ManageGitignore != nil {
					t.Errorf("ManageGitignore = %v, want it left unset", *cfg.ManageGitignore)
				}
			},
		},
		{
			name:      "unknown key",
			key:       "nonsense",
			value:     "x",
			wantErrIn: []string{"unknown config key", "nonsense"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			configPath := filepath.Join(t.TempDir(), "config.yaml")
			t.Setenv("LNPM_CONFIG", configPath)

			cfg := &config.Config{}
			var err error
			out := captureStdout(t, func() { err = setConfigKey(cfg, tc.key, tc.value) })
			if err == nil {
				t.Fatalf("setConfigKey(%q, %q) = nil, want an error", tc.key, tc.value)
			}
			for _, want := range tc.wantErrIn {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err.Error(), want)
				}
			}
			if strings.Contains(out, "Set ") {
				t.Errorf("setConfigKey printed a success line on a rejected value: %q", out)
			}
			if _, statErr := os.Stat(configPath); !os.IsNotExist(statErr) {
				data, _ := os.ReadFile(configPath)
				t.Errorf("config file was written despite the validation failure (stat err %v)\n--- file ---\n%s", statErr, string(data))
			}
			// Nil for the unknown-key case, which mutates no field to check.
			if tc.check != nil {
				tc.check(t, cfg)
			}
		})
	}
}

func TestGetConfigKeyRejectsUnknownKey(t *testing.T) {
	var err error
	out := captureStdout(t, func() { err = getConfigKey(&config.Config{}, "nonsense") })
	if err == nil {
		t.Fatalf("getConfigKey(nonsense) = nil, want an error")
	}
	if !strings.Contains(err.Error(), "unknown config key") || !strings.Contains(err.Error(), "nonsense") {
		t.Errorf("error = %q, want it to mention the unknown config key", err.Error())
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("getConfigKey printed %q for an unknown key, want nothing", out)
	}
}

// TestShowConfig checks that the dump actually carries the settings: the config
// file path plus every value that is set. Asserting only that showConfig
// returned nil would pass against an empty dump.
func TestShowConfig(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv("LNPM_CONFIG", configPath)

	manageGitignore := false
	cfg := &config.Config{
		StorePath:       filepath.Join(t.TempDir(), "shown-store"),
		LinkMode:        "copy",
		ManageGitignore: &manageGitignore,
		Hooks:           config.HooksConfig{PrePublish: "echo before"},
	}

	var err error
	out := captureStdout(t, func() { err = showConfig(cfg) })
	if err != nil {
		t.Fatalf("showConfig() error = %v", err)
	}

	for _, want := range []string{
		"Config file: " + configPath,
		"store_path: " + cfg.StorePath,
		"link_mode: copy",
		"manage_gitignore: false",
		"pre_publish: echo before",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("showConfig output missing %q\n--- output ---\n%s", want, out)
		}
	}
}
