package main

import "testing"

func TestResolveVersion_StampedByLdflags(t *testing.T) {
	tests := []struct {
		name    string
		version string
	}{
		{"v-prefixed release tag", "v1.11.0"},
		{"bare release tag", "1.11.0"},
		{"pre-release tag", "v1.12.0-rc.1"},
	}

	original := Version
	t.Cleanup(func() { Version = original })

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			Version = tt.version
			if got := resolveVersion(); got != tt.version {
				t.Errorf("resolveVersion() = %q, want %q", got, tt.version)
			}
		})
	}
}
