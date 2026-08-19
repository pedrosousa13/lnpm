package update

import "testing"

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		name    string
		current string
		latest  string
		want    bool
	}{
		{"newer minor with more digits", "1.9.0", "1.11.0", true},
		{"newer patch with more digits", "1.2.9", "1.2.10", true},
		{"same version", "1.11.0", "1.11.0", false},
		{"current newer than latest", "1.11.0", "1.9.0", false},
		{"v-prefixed inputs", "v1.9.0", "v1.11.0", true},
		{"mixed v-prefixed and bare inputs", "1.9.0", "v1.11.0", true},
		{"v-prefixed same version", "v1.11.0", "1.11.0", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := compareVersions(tt.current, tt.latest)
			if got.UpdateAvailable != tt.want {
				t.Errorf("compareVersions(%q, %q).UpdateAvailable = %v, want %v",
					tt.current, tt.latest, got.UpdateAvailable, tt.want)
			}
		})
	}
}
