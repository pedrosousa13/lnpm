// Package releasekeys holds the public keys that lnpm trusts to have signed a
// release's checksums.txt.
//
// # The placeholder key must be replaced before shipping
//
// keys/PLACEHOLDER-DO-NOT-SHIP.pem is a throwaway key generated to give
// //go:embed something to compile against. It has no private half that anyone
// kept, so it can never verify a real release. Replace it with the maintainer's
// real public key - and delete the placeholder - before cutting a signed
// release, or every 'lnpm update' will refuse to install.
//
// # Why a list
//
// Verification succeeds if any one of these keys verifies the signature. That
// is what lets a key be rotated without breaking updaters built against the old
// one: publish releases signed by the new key while the old key is still
// embedded, and only drop the old key once nobody is running a build that lacks
// the new one.
package releasekeys

import (
	"crypto/ecdsa"
	"crypto/x509"
	"embed"
	"encoding/pem"
	"fmt"
	"io/fs"
)

//go:embed keys/*.pem
var keyFiles embed.FS

// MustTrusted returns the trusted release signing keys, parsed from the
// embedded SPKI PEM files.
//
// It panics rather than returning an error because the input is compiled into
// the binary: a PEM that does not parse is a build that should never have been
// made, not a condition a user can do anything about. The package's own test
// parses them, so the panic is caught before it can ship.
func MustTrusted() []*ecdsa.PublicKey {
	keys, err := trusted()
	if err != nil {
		panic("releasekeys: " + err.Error())
	}
	return keys
}

func trusted() ([]*ecdsa.PublicKey, error) {
	entries, err := fs.Glob(keyFiles, "keys/*.pem")
	if err != nil {
		return nil, err
	}

	keys := make([]*ecdsa.PublicKey, 0, len(entries))
	for _, name := range entries {
		data, err := keyFiles.ReadFile(name)
		if err != nil {
			return nil, err
		}
		key, err := parseKey(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		keys = append(keys, key)
	}

	if len(keys) == 0 {
		return nil, fmt.Errorf("no release signing keys are embedded")
	}
	return keys, nil
}

// parseKey decodes one SPKI PEM block into an ECDSA public key.
func parseKey(data []byte) (*ecdsa.PublicKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("not a PEM file")
	}
	if block.Type != "PUBLIC KEY" {
		return nil, fmt.Errorf("PEM block is %q, want %q", block.Type, "PUBLIC KEY")
	}

	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, err
	}

	key, ok := pub.(*ecdsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("key is %T, want an ECDSA public key", pub)
	}
	return key, nil
}
