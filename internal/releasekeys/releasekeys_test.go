package releasekeys

import (
	"crypto/elliptic"
	"testing"
)

// The embedded PEMs are the root of trust for every update: if they do not
// parse, the updater refuses every release, and nothing else in the program
// would notice until a user tried to update.
func TestMustTrustedYieldsAtLeastOneP256Key(t *testing.T) {
	keys := MustTrusted()

	if len(keys) == 0 {
		t.Fatal("MustTrusted returned no keys, want at least one embedded release key")
	}
	for i, k := range keys {
		if k.Curve != elliptic.P256() {
			t.Errorf("key %d is on curve %v, want P-256", i, k.Curve)
		}
	}
}
