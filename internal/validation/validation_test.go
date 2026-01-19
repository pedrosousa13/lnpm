package validation

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestValidatePackage(t *testing.T) {
	tests := []struct {
		name      string
		pkgJSON   map[string]interface{}
		files     map[string]string
		wantError bool
		errorMsg  string
	}{
		{
			name: "valid package",
			pkgJSON: map[string]interface{}{
				"name":    "test-pkg",
				"version": "1.0.0",
				"main":    "index.js",
			},
			files: map[string]string{
				"index.js": "module.exports = {}",
			},
			wantError: false,
		},
		{
			name: "missing name",
			pkgJSON: map[string]interface{}{
				"version": "1.0.0",
			},
			wantError: true,
			errorMsg:  "must have a name field",
		},
		{
			name: "missing version",
			pkgJSON: map[string]interface{}{
				"name": "test-pkg",
			},
			wantError: true,
			errorMsg:  "must have a version field",
		},
		{
			name: "missing main file",
			pkgJSON: map[string]interface{}{
				"name":    "test-pkg",
				"version": "1.0.0",
				"main":    "lib/index.js",
			},
			wantError: true,
			errorMsg:  "main file not found: lib/index.js",
		},
		{
			name: "no main field is valid",
			pkgJSON: map[string]interface{}{
				"name":    "test-pkg",
				"version": "1.0.0",
			},
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temp directory
			tmpDir := t.TempDir()

			// Write package.json
			pkgJSONPath := filepath.Join(tmpDir, "package.json")
			data, err := json.MarshalIndent(tt.pkgJSON, "", "  ")
			if err != nil {
				t.Fatalf("failed to marshal package.json: %v", err)
			}
			if err := os.WriteFile(pkgJSONPath, data, 0644); err != nil {
				t.Fatalf("failed to write package.json: %v", err)
			}

			// Create test files
			for name, content := range tt.files {
				filePath := filepath.Join(tmpDir, name)
				dir := filepath.Dir(filePath)
				if err := os.MkdirAll(dir, 0755); err != nil {
					t.Fatalf("failed to create directory: %v", err)
				}
				if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
					t.Fatalf("failed to write file %s: %v", name, err)
				}
			}

			// Run validation
			err = ValidatePackage(tmpDir)

			if tt.wantError {
				if err == nil {
					t.Errorf("expected error but got nil")
					return
				}
				if tt.errorMsg != "" && !contains(err.Error(), tt.errorMsg) {
					t.Errorf("expected error containing %q, got %q", tt.errorMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
