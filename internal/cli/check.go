package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// RunCheck scans the current project's package.json for leftover lnpm
// references (file:.lnpm/ or link:.lnpm/) and returns a non-nil error if any
// are found. It is meant as a pre-publish guard: run `lnpm retreat` to clear
// the references before publishing to npm.
func RunCheck() error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	pkgJSONPath := filepath.Join(cwd, "package.json")
	data, err := os.ReadFile(pkgJSONPath)
	if err != nil {
		return fmt.Errorf("no package.json found in current directory")
	}

	var pkgJSON map[string]interface{}
	if err := json.Unmarshal(data, &pkgJSON); err != nil {
		return fmt.Errorf("failed to parse package.json: %w", err)
	}

	// Collect "<field>: <pkg> => <ref>" for every lnpm reference found across
	// the standard dependency maps.
	var found []string
	for _, field := range []string{"dependencies", "devDependencies", "optionalDependencies", "peerDependencies"} {
		deps, ok := pkgJSON[field].(map[string]interface{})
		if !ok {
			continue
		}
		for name, v := range deps {
			ref, ok := v.(string)
			if ok && isLnpmReference(ref) {
				found = append(found, fmt.Sprintf("  %s.%s -> %s", field, name, ref))
			}
		}
	}

	if len(found) == 0 {
		fmt.Printf("%s No lnpm references in package.json\n", iconOK())
		return nil
	}

	sort.Strings(found)
	fmt.Printf("%s Found %d lnpm reference(s) in package.json:\n", iconFail(), len(found))
	for _, line := range found {
		fmt.Println(line)
	}
	fmt.Printf("\n  %s Run 'lnpm retreat --force' to restore original dependencies before publishing\n", iconTip())
	return fmt.Errorf("%d lnpm reference(s) found in package.json", len(found))
}
