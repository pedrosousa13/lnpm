package cli

import (
	"os"
	"testing"
)

// templateAfterInit records the root command's version template as the package
// init left it, before any test mutates the build stamps.
var templateAfterInit string

func TestMain(m *testing.M) {
	templateAfterInit = rootCmd.VersionTemplate()
	os.Exit(m.Run())
}

func TestVersionTemplateShowsCommitAndDateWhenStamped(t *testing.T) {
	got := versionTemplate("v1.11.0", "abc1234", "2026-08-20T10:00:00Z")

	want := "lnpm version v1.11.0\ncommit: abc1234\nbuilt:  2026-08-20T10:00:00Z\n"
	if got != want {
		t.Fatalf("versionTemplate() = %q, want %q", got, want)
	}
}

func TestVersionTemplateOmitsCommitAndDateWhenUnstamped(t *testing.T) {
	got := versionTemplate("dev", "none", "unknown")

	want := "lnpm version dev\n"
	if got != want {
		t.Fatalf("versionTemplate() = %q, want %q", got, want)
	}
}

func TestVersionTemplateOmitsEmptyCommitAndDate(t *testing.T) {
	got := versionTemplate("dev", "", "")

	want := "lnpm version dev\n"
	if got != want {
		t.Fatalf("versionTemplate() = %q, want %q", got, want)
	}
}

func TestSetVersionAppliesTemplateToRootCommand(t *testing.T) {
	restoreBuildInfo(t)

	SetVersion("v1.11.0", "abc1234", "2026-08-20T10:00:00Z")

	if rootCmd.Version != "v1.11.0" {
		t.Errorf("rootCmd.Version = %q, want %q", rootCmd.Version, "v1.11.0")
	}
	want := "lnpm version v1.11.0\ncommit: abc1234\nbuilt:  2026-08-20T10:00:00Z\n"
	if got := rootCmd.VersionTemplate(); got != want {
		t.Errorf("rootCmd.VersionTemplate() = %q, want %q", got, want)
	}
}

// The package init applies the same template as SetVersion, so an unstamped
// binary that never reaches SetVersion still prints the agreed shape.
func TestInitAppliesUnstampedVersionTemplate(t *testing.T) {
	want := "lnpm version dev\n"
	if templateAfterInit != want {
		t.Errorf("version template after init = %q, want %q", templateAfterInit, want)
	}
}

// restoreBuildInfo puts the package-level build stamps and the root command's
// version state back after a test mutates them.
//
// Don't use t.Parallel() in callers - this helper swaps the process-wide
// version/commit/date vars and rootCmd's version state, so a caller must also
// not run alongside a test that does.
func restoreBuildInfo(t *testing.T) {
	t.Helper()
	prevVersion, prevCommit, prevDate := version, commit, date
	prevCmdVersion, prevTemplate := rootCmd.Version, rootCmd.VersionTemplate()
	t.Cleanup(func() {
		version, commit, date = prevVersion, prevCommit, prevDate
		rootCmd.Version = prevCmdVersion
		rootCmd.SetVersionTemplate(prevTemplate)
	})
}
