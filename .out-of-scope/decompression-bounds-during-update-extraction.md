# Decompression bounds during update extraction

`lnpm update` extracts the release archive without any cap on the *decompressed*
size, and it is going to stay that way. Two call sites do the copying:

```go
// internal/cli/update.go:777, the tar.gz path
if _, err := io.Copy(out, tr); err != nil {
    return "", err
}

// internal/cli/update.go:811, the zip path
if _, err := io.Copy(out, rc); err != nil {
    return "", err
}
```

Neither wraps its reader in an `io.LimitReader`, so a high-ratio archive expands
to whatever it expands to, into the temp directory `downloadBinary` made at
`internal/cli/update.go:487`.

## Why this is out of scope

Reaching either line requires a correctly signed release.

`downloadBinary` calls `verifyRelease` at `internal/cli/update.go:507`, before
either extractor is selected at `:514`, and bails out on any error. What
`verifyRelease` establishes is not just a checksum: `fetchSignedChecksums`
(`internal/cli/update.go:598`) fetches `checksums.txt` and its detached
signature, loads the keys embedded from `internal/releasekeys/keys/*.pem`, and
refuses at `:618` unless `verifySignature` accepts the signature against one of
them. Only then is the archive's SHA-256 compared against the entry
`checksums.txt` lists for it (`internal/cli/update.go:585`). A missing signature
is a refusal too, not a fallback — see `signatureUnavailableError` at
`internal/cli/update.go:642`.

The compressed input is already bounded. `downloadToFile` reads through
`io.LimitReader(resp.Body, maxReleaseArchiveBytes+1)` at
`internal/cli/update.go:547` and errors above 256 MiB
(`internal/cli/update.go:99`), which #487 added.

So the attacker this would defend against is one who can produce a release
whose `checksums.txt` verifies under a key compiled into the binary already
running on the victim's machine — that is, someone holding the release signing
key. Such an attacker does not need a decompression bomb. They can sign a
malicious `lnpm` binary and have the updater install it, which is more direct,
more useful to them, and completely unaffected by any size cap on extraction.
Filling a victim's `$TMPDIR` is a strictly worse outcome to buy with a stolen
signing key.

The defence here is the signature gate, and it is the one worth keeping sound.
A bound on decompressed size would sit behind it, guarding a path that only a
key-holder can reach.

## The counter-argument that was heard and rejected

There is a real one, and it is about mistakes rather than attacks: a release
accidentally built with a pathological archive — a build script that packaged
something enormous and compressible — would fill the filesystem holding
`$TMPDIR` instead of failing with a named error. #487 made exactly this argument
for the download path and it carried there.

It does not carry here. On the download path the bytes come from the network
before anything has been verified, so the cap is the only thing standing between
a hostile server and the disk; the comment at `internal/cli/update.go:86` says
as much. On the extraction path the bytes come from an archive the maintainer
signed, so the failure being guarded against is the maintainer shipping a broken
release to themselves and their users — which the release pipeline, and the
first person to run `lnpm update`, would surface immediately and unmistakably.
Trading a "disk full" for a named error in that scenario is a small improvement
to a diagnosis, not a security control, and it is not worth carrying two more
size constants and the tests that pin them.

## Prior requests

- #493: "Archive extraction during update is unbounded, so a signed release
  could be a decompression bomb"
