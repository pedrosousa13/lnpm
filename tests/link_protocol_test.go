package tests

import (
	"testing"

	"github.com/pedrosousa13/lnpm/internal/cli"
)

// TestAddLinkProtocol verifies that `add --link` writes a link:.lnpm/ reference
// into package.json instead of the default file:.lnpm/ reference.
func TestAddLinkProtocol(t *testing.T) {
	env := setupTest(t)

	env.simplePkg("proto-lib")
	projectDir := env.newProject("proto-project")

	// link=true (last arg) selects the link: protocol.
	if err := cli.RunAddMultiple([]string{"proto-lib"}, false, false, false, true); err != nil {
		t.Fatalf("add --link failed: %v", err)
	}

	env.AssertPackageJSON(projectDir, "proto-lib", "link:.lnpm/proto-lib")
	// Linking side effects must still happen.
	env.AssertSymlinkExists(projectDir, "proto-lib")
	env.AssertFilesLinked(projectDir, "proto-lib")
}

// TestAddDefaultProtocolUnchanged verifies that without --link the default
// file:.lnpm/ reference is still written.
func TestAddDefaultProtocolUnchanged(t *testing.T) {
	env := setupTest(t)

	env.simplePkg("default-lib")
	projectDir := env.newProject("default-project")

	if err := cli.RunAddMultiple([]string{"default-lib"}, false, false, false, false); err != nil {
		t.Fatalf("add failed: %v", err)
	}

	env.AssertPackageJSON(projectDir, "default-lib", "file:.lnpm/default-lib")
}
