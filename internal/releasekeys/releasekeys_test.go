package releasekeys

import (
	"crypto/elliptic"
	"testing"
)

// The embedded PEMs are the root of trust for every update: if they do not
// parse, the updater refuses every release, and nothing else in the program
// would notice until a user tried to update.
//
// An embed holding no key at all is reported as an error too, so the error
// check below is also what pins "at least one key is embedded" - asserting
// len(keys) != 0 on top of it could never fail.
func TestTrustedYieldsP256KeysOnly(t *testing.T) {
	keys, err := Trusted()
	if err != nil {
		t.Fatalf("Trusted() error: %v", err)
	}

	for i, k := range keys {
		if k.Curve != elliptic.P256() {
			t.Errorf("key %d is on curve %v, want P-256", i, k.Curve)
		}
	}
}
