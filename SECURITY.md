# Security Policy

## Supported Versions

| Version | Supported          |
| ------- | ------------------ |
| 3.x     | :white_check_mark: |
| 2.x     | :x:                |
| 1.x     | :x:                |

This table is not generated. The maintainer updates it by hand when the
supported line changes, which in practice means at a major release. Release
tooling will not own it — a settled decision rather than an unexamined one.

Every release-please substitution token puts *the version being released* into
a line or block that already exists. None adds, removes or moves a line, and
none can name a previous major. Annotate the supported row, cut v3.0.0, and the
table reads `3.x` supported over `1.x` unsupported: 2.x has vanished from it
entirely, listed as neither, at the moment a reader would check. Annotating the
second row is worse — with no previous-major token it renders `3.x`
unsupported, contradicting the row above. So the half that can be automated
leaves this file wrong rather than merely stale — and even that half is not
free: `extra-files` is a manifest-config option, while the release workflow
runs the action with `release-type: go` and no config file, so buying it means
migrating a working release pipeline to manifest mode.

What does catch the table going stale is a CI check.
`scripts/check-security-versions.sh` compares the major named by the
`:white_check_mark:` row above against the version in `CHANGELOG.md`'s topmost
`## ` heading, and fails when they disagree — so a major release whose table has
not been updated cannot be merged. It runs in
`.github/workflows/security-versions.yaml`, and `docs/releasing.md` records why
that is a workflow of its own. Only the supported row is checked: whether a
major moves to unsupported is the policy call described above, and stays with
the maintainer.

## Security Considerations

### Lifecycle Scripts

lnpm runs `package.json` lifecycle scripts (`prepare`, `prepublishOnly`, `prepack`) during `publish` and `push`, similar to npm. Users should:

- Only `publish`/`push` packages whose scripts they trust
- Be aware that scripts run with the same permissions as the user
- Use `--skip-hooks` to skip these scripts when needed

### File System Access

lnpm operates within these directories:

- **Store**: `~/.lnpm/` (configurable via `LNPM_STORE` or config)
- **Project**: Current working directory and its `.lnpm/` subdirectory
- **node_modules**: Symlinks created in project's `node_modules/`

A package name is untrusted input: it comes from a `package.json` or an
`lnpm.lock` that is checked into the repository, so whoever wrote the repository
chose it. Against that, lnpm:

- Validates the name before building a path it will **write to or delete**, at
  each boundary that does so — `Store.Store`, the linker's
  `Link`/`LinkSource`/`Unlink`, packing, and `retreat`'s pass over `lnpm.lock`.
  A name that is absolute, holds a `.` or `..` segment, holds a backslash or a
  NUL, or has more than the one `/` a scope allows, is rejected there.
- Requires `.lnpm` — and, for a scoped package, `.lnpm/{scope}` — to be a real
  directory, so a repository cannot commit either as a symlink and redirect
  every write and delete underneath it.
- Requires the same of `node_modules` — and, for a scoped package,
  `node_modules/{scope}` — before creating the link into `.lnpm` and before
  removing it again, in the linker and in `retreat` alike, through one shared
  predicate. That check is overridable, and `.lnpm`'s is not: relocating
  `node_modules` to another volume or out of a synced folder is a setup people
  run, so `follow_symlinked_node_modules: true` in the config file turns it off.
  Leaving it on is what stops a committed link from aiming lnpm's directory
  creation and its deletes outside the project.
- Refuses a workspace member whose real path falls outside the workspace root.
  Glob expansion follows symlinks, so a checkout committing
  `packages/escape -> /somewhere/else` would otherwise have that directory
  listed as a member and its manifest read from outside the root. Both sides are
  resolved in full, so a chain of links cannot slip past a single-level check.

Note that `filepath.Join()` is not itself a defence: it cleans the path it
builds, so `..` segments in a name survive into the result. The validation
above is what stops them.

Known limits, which this section deliberately does not claim otherwise about:

- The checks are not atomic. A path validated as safe can be replaced between
  the check and the use.
- The `node_modules` guard is overridable and `.lnpm`'s is not, so a project
  that sets `follow_symlinked_node_modules` gets the old behaviour back,
  redirect included. That is the trade relocated `node_modules` setups are
  worth, not an oversight. The key is named for the symlink it exists for but
  switches the whole check off, so a regular file or a device at either path is
  accepted under it too.
- The guard covers `node_modules` and `node_modules/{scope}`. The entry beneath
  them is removed with calls that do not follow a link at their last component,
  so a package's own `node_modules/{package}` needs no equivalent check.
- Read paths are not covered. `Store.GetFiles` walks the store path it builds
  from a name, and the link-status queries `pull` runs only `Lstat` it, and
  neither validates the name first. They read rather than write, so they cannot
  destroy anything, but a name chosen by the repository can still steer where
  they look.

### Database

lnpm uses bbolt, an embedded key-value database:
- Database file: `~/.lnpm/lnpm.db`
- File permissions: `0600` (owner read/write only)
- No network access or SQL injection vectors

### Hard Links

Hard links share the same inode as the store's file, so a linked file in
`.lnpm/{package}` and the store entry it came from are one file with two names.
The space saving is the reason lnpm uses them, and the shared inode is what the
saving is.

The blast radius of that sharing reaches other projects. A write inside
`.lnpm/{package}` — a `patch-package` run, a bundler emitting into
`node_modules`, a shell redirect — does not change one project's copy of a
package. It changes the store entry filed under that package's content hash, so
every project that adds that version afterwards is materialised from the
modified bytes, and nothing re-reads the store to notice.

What now prevents it: the store's canonical copy is **write protected**. Store
content is committed with its write bits stripped, so a write into a linked file
fails with `EACCES` instead of rewriting the entry silently. Only the write bits
go, so an executable stays executable, and only regular files are touched, so
directories stay writable and `lnpm gc` can still remove an entry. The
protection holds on every materialisation path — reflink, hard link and copy all
preserve the source's mode — and a store written by an older lnpm is protected
once, when a command next opens it.

Two consequences to know about:

- **Linked packages really are read-only now.** Anything that writes into a
  dependency under `.lnpm` starts failing. That is the point: those writes were
  the poisoning path. `link_mode: copy` and `lnpm add --link` are the modes for
  a dependency you need to write to, and only `--link` gives you a writable
  tree — the copy inherits the store's stripped mode, it just does not share the
  store's inode, so a write there cannot reach the store even if you restore the
  bits yourself.
- **The protection is a lock, not a repair.** An entry poisoned before the
  upgrade stays as it is, protected in its tampered state. Catching one takes a
  check that re-reads the entry, which is what `lnpm doctor --verify-content`
  does. That check compares content and only content: a file's own hash covers
  its bytes with no mode in it, while the package-level hash folds permission
  bits in, so every mode the comparison uses comes out of the database rather
  than off the protected files on disk. Putting the write bits back before
  hashing is not the answer — `mode|0222` on a file published `0444` invents a
  `0666` the file never had.

Hard links cannot cross filesystem boundaries; lnpm falls back to a copy, which
carries the same stripped mode.

### Content Addressing

The store files an entry under the hash of what it holds —
`~/.lnpm/store/{name}/{hash}` — and that hash is **xxhash: 64 bits, and not a
cryptographic hash**. A file is hashed over its bytes alone; the package-level
hash folds each file's path, that per-file hash and its permission bits
together. Nothing else is covered: not size, not modification time, not
ownership.

What that gives you:

- Two publishes of identical content are the same entry, and a change to any
  packed file's bytes, path or permission bits produces a different one.
- A stored file that no longer hashes to the value recorded for it has changed
  since it was published. `lnpm doctor --verify-content` re-reads the store and
  reports those files.

What it does not give you:

- **It is not tamper evidence.** xxhash is not collision resistant and is not
  designed to be. A 64-bit digest puts a birthday collision at roughly 2^32
  hashed inputs, but that is an upper bound on the work rather than the cost to
  expect: the package-level hash concatenates its fields with no lengths and no
  separators, so two different file sets can be made to produce the same input
  to the hash with no cryptanalysis and no search at all. The reach is one
  package name, not the store: an entry lives at `{name}/{hash}` and its
  database record is keyed the same way, so two differently-named packages never
  meet. What someone who publishes under a name can do is make two of their own
  publishes collapse into one entry. That framing defect is tracked in
  [#453](https://github.com/pedrosousa13/lnpm/issues/453).
- What the hash detects is corruption and accident — a truncated write, a bad
  disk, an edit by someone not trying to hide it. What resists deliberate
  tampering is the write protection described above, not the hash.
- A collision serves *stale* content rather than chosen content. `Store()`
  returns the existing entry when the hash is already present and never
  overwrites or deletes one, so the bytes that were there stay there and the
  colliding publish is the one that gets ignored.
- One file sits outside the guarantee today. lnpm removes `prepare` and
  `prepublish` from a stored `package.json` after the content hash has been
  taken, so for a package defining either, the entry does not hold what its hash
  describes, and `doctor --verify-content` reports that manifest as unchecked
  rather than as sound. Tracked in
  [#447](https://github.com/pedrosousa13/lnpm/issues/447).

The decision to accept a non-cryptographic hash here, and the route to tamper
evidence if it is ever needed, are recorded in
`docs/adr/0007-the-stores-content-hash-is-a-consistency-control-not-tamper-evidence.md`.

### Release Integrity

Every release publishes `checksums.txt`, a SHA-256 file holding an entry for each
archive and package on that release. Signing that one file therefore covers the
whole release: verify the signature over `checksums.txt`, then verify your
download against `checksums.txt`.

- The signature is published as `checksums.txt.sig` — an ECDSA P-256 signature
  over the SHA-256 digest of `checksums.txt`, encoded as ASN.1 DER. Raw bytes,
  not base64 and not armored.
- The trusted public keys are committed in `internal/releasekeys/keys/` as SPKI
  PEM and compiled into the binary.

**Which releases are signed.** A release is signed if and only if it publishes a
`checksums.txt.sig` asset — that is the test to apply, because it stays true as
versions move on. Releases up to and including **v3.0.0 publish no signature**;
signing begins with the first release after this change lands. That version is
not named here because release-please chooses it, not this document.

Nothing is stranded by the cutover. `lnpm update` only ever installs the latest
release, never an older one, so once the first signed release exists the only
release it will try to install is a signed one.

**What signature verification covers.** `lnpm update` and only `lnpm update`.
The bootstrap path — `install.sh` and `install.ps1` — verifies checksums alone.
That catches a corrupted download or an archive altered in transit; it does not
catch a release where an attacker replaced the archive and `checksums.txt`
together. A first install is not signature-protected.

Under `lnpm update`, lnpm downloads both files, verifies the signature against
the keys built into the binary you are running now, and only then checks the
archive against `checksums.txt`. A signature that is missing, invalid, or made by
a key your binary does not trust aborts the update and leaves the existing binary
in place. This is what checksum-only verification cannot do: `checksums.txt` is
served from the same release as the binaries it describes, so alone it proves
only that the download matches *some* checksum file, not that the checksum file
came from the maintainer.

**Verifying by hand.** Substitute the tag and archive you are checking for the
placeholders. This works only from the first signed release onward — against an
earlier tag both the `.sig` asset and the key file return 404.

```sh
TAG=v0.0.0                              # the release you are verifying
FILE=lnpm_0.0.0_linux_amd64.tar.gz      # the archive you downloaded
BASE=https://github.com/pedrosousa13/lnpm/releases/download/$TAG

curl -sSLO "$BASE/checksums.txt"
curl -sSLO "$BASE/checksums.txt.sig"
curl -sSLO "$BASE/$FILE"
curl -sSLO "https://raw.githubusercontent.com/pedrosousa13/lnpm/$TAG/internal/releasekeys/keys/release.pem"

openssl dgst -sha256 -verify release.pem -signature checksums.txt.sig checksums.txt

# sha256sum is GNU coreutils; macOS ships shasum instead. Run whichever you have
# — both read the single checksums.txt entry for your archive from stdin.
grep " $FILE\$" checksums.txt | sha256sum -c -
grep " $FILE\$" checksums.txt | shasum -a 256 -c -
```

`openssl` prints `Verified OK`, and the checksum command prints `<archive>: OK`.
If either fails, do not install the download.

More than one key may be present while a key is being rotated, and a release is
valid if any one of them verifies it. List the keys for the tag you are
verifying at
`https://github.com/pedrosousa13/lnpm/tree/$TAG/internal/releasekeys/keys`, then
fetch each at that tag — not from `main`, which may already have rotated.

## Reporting a Vulnerability

Please report security vulnerabilities privately. GitHub's private vulnerability
reporting is the preferred channel:

**[Report a vulnerability](https://github.com/pedrosousa13/lnpm/security/advisories/new)**

You can also reach the same form from the repository's
[Security tab](https://github.com/pedrosousa13/lnpm/security) via the
**Report a vulnerability** button. The report is visible only to the maintainers
until an advisory is published.

When reporting:

1. **DO NOT** open a public issue
2. Include:
   - Description of the vulnerability
   - Steps to reproduce
   - Potential impact
   - Suggested fix (if any)

We will respond within 48 hours and work with you to understand and address the issue.

## Best Practices for Users

1. **Review before publish**: Run `lnpm retreat` before publishing to npm
2. **Trusted sources only**: Only `lnpm add` packages you've published yourself
3. **Lifecycle scripts**: Be cautious publishing packages with untrusted lifecycle scripts; use `--skip-hooks` if needed
4. **Permissions**: Keep `~/.lnpm/` permissions restricted
