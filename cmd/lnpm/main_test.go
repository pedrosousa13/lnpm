package main

import (
	"os"
	"runtime/debug"
	"testing"

	"github.com/pedrosousa13/lnpm/internal/testenv"
)

// TestMain redirects the config loader at a temp directory before the first
// test runs (#371). Nothing tested here reaches the loader today - pickVersion
// is pure - but main links internal/cli, so a test that drove a command would,
// and this stops that one reading the machine's own ~/.lnpm/config.yaml.
func TestMain(m *testing.M) {
	os.Exit(testenv.Run(m))
}

func buildInfo(version string) *debug.BuildInfo {
	return &debug.BuildInfo{Main: debug.Module{Version: version}}
}

func TestPickVersion(t *testing.T) {
	tests := []struct {
		name    string
		stamped string
		info    *debug.BuildInfo
		ok      bool
		want    string
	}{
		{"v-prefixed release tag", "v1.11.0", nil, false, "v1.11.0"},
		{"bare release tag", "1.11.0", nil, false, "1.11.0"},
		{"pre-release tag", "v1.12.0-rc.1", nil, false, "v1.12.0-rc.1"},
		// The Makefile stamps `git describe --tags --always --dirty`. That full
		// string is what `lnpm --version` must print - the update check
		// disqualifies it separately, on its own terms (#283).
		{"git describe stamp", "v1.12.0-53-g7079f81-dirty", nil, false, "v1.12.0-53-g7079f81-dirty"},
		{"unstamped with released module version", "dev", buildInfo("v1.11.0"), true, "v1.11.0"},
		{"empty stamp with released module version", "", buildInfo("v1.11.0"), true, "v1.11.0"},
		{"no build info", "dev", nil, false, "dev"},
		{"empty module version", "dev", buildInfo(""), true, "dev"},
		{"devel module version", "dev", buildInfo("(devel)"), true, "dev"},
		{"pseudo-version", "dev", buildInfo("v1.12.1-0.20260819061412-6d9902254937"), true, "dev"},
		{"dirty pseudo-version", "dev", buildInfo("v1.12.1-0.20260819061412-6d9902254937+dirty"), true, "dev"},
		{"dirty release tag", "dev", buildInfo("v1.11.0+dirty"), true, "dev"},
		{"git describe module version", "dev", buildInfo("v1.12.0-53-g7079f81-dirty"), true, "dev"},
		{"dirty-marked module version", "dev", buildInfo("v1.12.0-dirty"), true, "dev"},
		{"non-semver module version", "dev", buildInfo("garbage"), true, "dev"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pickVersion(tt.stamped, tt.info, tt.ok); got != tt.want {
				t.Errorf("pickVersion(%q, %+v, %v) = %q, want %q", tt.stamped, tt.info, tt.ok, got, tt.want)
			}
		})
	}
}
