package validation

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/pedrosousa13/lnpm/internal/pack"
)

// ValidatePackage validates a package before publishing
// Checks package.json validity and verifies declared entry points exist
func ValidatePackage(pkgPath string) error {
	pkgJSON, err := pack.ReadPackageJSON(pkgPath)
	if err != nil {
		return fmt.Errorf("invalid package.json: %w", err)
	}

	// Verify required fields
	if pkgJSON.Name == "" {
		return fmt.Errorf("package.json missing required field: name")
	}
	if pkgJSON.Version == "" {
		return fmt.Errorf("package.json missing required field: version")
	}

	// Verify main entry point exists if declared
	if pkgJSON.Main != "" {
		mainPath := filepath.Join(pkgPath, pkgJSON.Main)
		if _, err := os.Stat(mainPath); err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("main file not found: %s (did you run build scripts?)", pkgJSON.Main)
			}
			return fmt.Errorf("cannot access main file %s: %w", pkgJSON.Main, err)
		}
	}

	return nil
}
