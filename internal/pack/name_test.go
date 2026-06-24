package pack

import "testing"

func TestValidatePackageName(t *testing.T) {
	valid := []string{
		"my-pkg",
		"lodash",
		"@org/my-pkg",
		"@scope/name.with.dots",
		"under_score",
		"a",
	}
	for _, name := range valid {
		if err := ValidatePackageName(name); err != nil {
			t.Errorf("ValidatePackageName(%q) = %v, want nil", name, err)
		}
	}

	invalid := []struct {
		name string
		desc string
	}{
		{"", "empty"},
		{"../evil", "parent traversal"},
		{"../../../../tmp/evil", "deep traversal"},
		{"foo/../bar", "embedded traversal"},
		{"@scope/..", "scoped traversal"},
		{"/abs/path", "absolute"},
		{"a/b/c", "too many segments"},
		{"foo/bar", "unscoped slash"},
		{"@/name", "empty scope"},
		{"name\\with\\backslash", "backslash"},
		{".", "dot"},
		{"..", "dotdot"},
		{"with\x00nul", "nul byte"},
	}
	for _, tc := range invalid {
		if err := ValidatePackageName(tc.name); err == nil {
			t.Errorf("ValidatePackageName(%q) = nil, want error (%s)", tc.name, tc.desc)
		}
	}

	long := make([]byte, maxPackageNameLen+1)
	for i := range long {
		long[i] = 'a'
	}
	if err := ValidatePackageName(string(long)); err == nil {
		t.Errorf("ValidatePackageName(<215 chars>) = nil, want error")
	}
}
