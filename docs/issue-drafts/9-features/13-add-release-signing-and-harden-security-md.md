TITLE: Sign release artifacts and add a real vulnerability reporting channel
LABELS: security, enhancement
---
## Background

lnpm ships a self-updater: `lnpm update` downloads a release archive and verifies it against `checksums.txt`, both fetched from the same GitHub release (`verifyChecksum`, `buildChecksumsURL`, and `downloadBinary` in `internal/cli/update.go`, lines roughly 300-331). Because `checksums.txt` lives on the exact same release/CDN as the binaries it's meant to verify, a compromised GitHub release (or a MITM between lnpm and GitHub) could serve a tampered binary alongside a matching tampered checksums file — the current check proves the downloaded bytes match *some* checksums.txt, not that either file actually came from the maintainer. Separately, `SECURITY.md`'s "Reporting a Vulnerability" section (lines 45-57) says "Email security concerns to the maintainers" without giving an actual address, so there's no working private reporting channel today.

## Motivation

As a user running `lnpm update` in an automated environment (CI, a dev container provisioning script, etc.), I want cryptographic proof that the binary being installed was actually built and released by the maintainer, not just that it matches a checksum file that could have been tampered with alongside the binary. Separately, as a security researcher who finds a real issue, I want an actual, working way to report it privately instead of hitting a dead end in `SECURITY.md`.

## Proposed behavior

Release artifacts are signed at release time (cosign keyless/Sigstore, which needs no maintainer-held private key). `lnpm update` verifies the signature before trusting `checksums.txt`, and refuses to install with a clear error if verification fails:

```
$ lnpm update
Installing v1.12.0...
  Downloading from https://github.com/pedrosousa13/lnpm/releases/download/v1.12.0/lnpm_1.12.0_darwin_arm64.tar.gz
✗ signature verification failed for checksums.txt: no matching Sigstore certificate found
```

`SECURITY.md` links to GitHub's private vulnerability reporting flow instead of an unspecified email address.

## Implementation sketch

1. Add a `signs:` section to `.goreleaser.yaml` for cosign, following GoReleaser's documented pattern of signing `checksums.txt` (since every archive is already covered by an entry in that file). Use cosign's keyless mode, which relies on GitHub Actions OIDC rather than a stored private key.
2. Update `.github/workflows/release-please.yaml`'s `goreleaser` job to install cosign and grant it the `id-token: write` permission it needs for keyless signing (the job currently only has `permissions: contents: write`).
3. In `internal/cli/update.go`, add a signature verification step called from `downloadBinary` before `verifyChecksum` (or folded into it): fetch the signature/certificate artifacts GoReleaser publishes alongside `checksums.txt`, and verify them (equivalent to `cosign verify-blob`) before trusting `checksums.txt`'s contents. This closes the gap where the binary and its checksum file could be tampered with together.
4. Update `SECURITY.md`'s "Reporting a Vulnerability" section: replace "Email security concerns to the maintainers" with a link to the repository's GitHub private vulnerability reporting form (Security tab -> "Report a vulnerability"). Add a short "Release Integrity" section documenting that releases are signed and how a user can independently verify a downloaded binary (e.g. a `cosign verify-blob` example command).

## Acceptance criteria

- [ ] Every tagged release produces a cosign signature (and certificate) for `checksums.txt`.
- [ ] `lnpm update` verifies that signature before trusting `checksums.txt` and refuses to install on verification failure.
- [ ] `SECURITY.md` links to a working, actual vulnerability reporting channel instead of an unspecified email.
- [ ] `SECURITY.md` documents how a user can independently verify a release's signature.

## Testing

- Signing itself can't be meaningfully unit tested without live Sigstore/GitHub Actions infrastructure; validate manually against a real tagged release (or a release dry-run) that `checksums.txt`'s signature verifies with `cosign verify-blob`.
- Any new parsing/error-handling logic added to `internal/cli/update.go` for signature verification should get unit tests against fixture signature/certificate files under `tests/`, covering both a valid signature and a tampered/missing one.

## Open questions

- Does the project want to depend on Sigstore's public transparency log (cosign keyless) or a maintainer-held key pair? Keyless is simpler to operate but ties verification to GitHub Actions OIDC and Sigstore's infrastructure being available; a held key pair avoids that dependency but adds key-management overhead.
