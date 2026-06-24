package config

import (
	"os"
	"path/filepath"
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
