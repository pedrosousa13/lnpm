package update

import "testing"

func TestIsDevBuild(t *testing.T) {
	tests := []struct {
		name    string
		version string
		want    bool
	}{
		{"unstamped placeholder", "dev", true},
		{"empty stamp", "", true},
		{"release tag", "v1.12.0", false},
		{"bare release tag", "1.12.0", false},
		{"release candidate", "v2.1.0-rc.1", false},
		{"beta pre-release", "v2.0.0-beta.2", false},
		{"pseudo-version", "v1.12.1-0.20260819061412-6d9902254937", true},
		{"pseudo-version with build metadata", "v1.12.1-0.20260819061412-6d9902254937+dirty", true},
		{"release tag with build metadata", "v1.11.0+dirty", true},
		{"git describe with commits ahead", "v1.12.0-53-g7079f81", true},
		{"git describe with commits ahead and dirty tree", "v1.12.0-53-g7079f81-dirty", true},
		{"git describe on the tag itself with dirty tree", "v1.12.0-dirty", true},
		{"git describe from a pre-release tag", "v2.1.0-rc.1-53-g7079f81-dirty", true},
		// git emits a lowercase sha, but the version string can also be stamped
		// by hand, and case is the only thing standing between such a stamp and
		// the downgrade #283 was filed for.
		{"git describe with an uppercase sha", "v1.12.0-53-g7079F81-dirty", true},
		{"git describe with an uppercase marker", "v1.12.0-53-G7079F81", true},
		{"bare git describe of an untagged repo", "7079f81-dirty", true},
		{"unparseable version", "garbage", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsDevBuild(tt.version); got != tt.want {
				t.Errorf("IsDevBuild(%q) = %v, want %v", tt.version, got, tt.want)
			}
		})
	}
}

func TestBaseline(t *testing.T) {
	tests := []struct {
		name    string
		version string
		want    string
		wantOK  bool
	}{
		{"release tag", "v1.12.0", "v1.12.0", true},
		{"bare release tag", "1.12.0", "v1.12.0", true},
		{"release candidate", "v2.1.0-rc.1", "v2.1.0-rc.1", true},
		{"git describe with commits ahead", "v1.12.0-53-g7079f81", "v1.12.0", true},
		{"git describe with commits ahead and dirty tree", "v1.12.0-53-g7079f81-dirty", "v1.12.0", true},
		{"git describe on the tag itself with dirty tree", "v1.12.0-dirty", "v1.12.0", true},
		{"git describe from a pre-release tag", "v2.1.0-rc.1-53-g7079f81", "v2.1.0-rc.1", true},
		{"git describe with an uppercase sha", "v1.12.0-53-g7079F81-dirty", "v1.12.0", true},
		{"git describe with an uppercase marker", "v1.12.0-53-G7079F81", "v1.12.0", true},
		// Build metadata marks a working tree, but it does not hide which
		// release the build came from - and semver ignores it when comparing,
		// so v1.11.0+dirty must still hear about v1.12.0.
		{"release tag with build metadata", "v1.11.0+dirty", "v1.11.0", true},
		{"unstamped placeholder", "dev", "", false},
		{"empty stamp", "", "", false},
		{"pseudo-version", "v1.12.1-0.20260819061412-6d9902254937", "", false},
		{"pseudo-version with build metadata", "v1.12.1-0.20260819061412-6d9902254937+dirty", "", false},
		{"bare git describe of an untagged repo", "7079f81-dirty", "", false},
		{"unparseable version", "release-1.12", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Baseline(tt.version)
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("Baseline(%q) = (%q, %v), want (%q, %v)", tt.version, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}
