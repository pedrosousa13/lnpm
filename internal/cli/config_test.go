package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
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
