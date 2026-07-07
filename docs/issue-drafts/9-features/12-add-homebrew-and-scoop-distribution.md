TITLE: Distribute lnpm via Homebrew and Scoop
LABELS: enhancement, ci
---
## Background

lnpm builds and publishes releases with GoReleaser, configured in `.goreleaser.yaml`: it produces `tar.gz`/`zip` archives for linux/darwin/windows and `.deb`/`.rpm`/`.apk` packages via the `nfpms` section. There is no Homebrew tap (`brews:`) or Scoop bucket (`scoops:`) section configured, so macOS/Linux users who manage CLI tools with Homebrew and Windows users who use Scoop have to fall back to the README's curl install script or `go install` instead of their usual package manager.

## Motivation

As a macOS developer who manages CLI tools with Homebrew, I want `brew install` to work for lnpm the same way it does for most CLI tools I use, instead of having to run a separate install script or `go install`.

## Proposed behavior

```
$ brew tap pedrosousa13/lnpm
$ brew install lnpm
```

```
> scoop bucket add lnpm https://github.com/pedrosousa13/scoop-lnpm
> scoop install lnpm
```

Both install the same binary GoReleaser already builds, and `brew upgrade`/`scoop update` pick up new releases automatically.

## Implementation sketch

1. Add a `brews:` section to `.goreleaser.yaml` targeting a tap repository (e.g. `pedrosousa13/homebrew-lnpm`), following GoReleaser's standard `brews` config: repository owner/name, commit author, an `install` block running `bin.install "lnpm"`, and a `test` block running `system "#{bin}/lnpm --version"`.
2. Add a `scoops:` section to `.goreleaser.yaml` targeting a bucket repository (e.g. `pedrosousa13/scoop-lnpm`).
3. Both publishing steps need push access to the tap/bucket repos from CI. The release workflow (`.github/workflows/release-please.yaml`) currently runs GoReleaser with only `permissions: contents: write` and the default `secrets.GITHUB_TOKEN`; publishing to a separate tap/bucket repo needs either a PAT with write access to that repo (GoReleaser's documented `HOMEBREW_TAP_GITHUB_TOKEN`-style secret) or the tap/bucket repo needs to live in the same org with appropriate token scope — add the required secret to the workflow.
4. Update the README's "Installation" section (currently: curl script, Windows script, `go install`) with `brew install`/`scoop install` instructions.

## Acceptance criteria

- [ ] `.goreleaser.yaml` has working `brews:` and `scoops:` sections.
- [ ] The release workflow has the token/permissions needed to push the generated formula/manifest to the tap and bucket repos.
- [ ] A real tagged release successfully publishes an updated formula to the tap repo and an updated manifest to the bucket repo.
- [ ] `brew install` and `scoop install` both produce a working `lnpm` binary that reports the correct version.
- [ ] README documents both installation methods.

## Testing

No unit tests apply to release tooling. Verify with a real tagged release (or a GoReleaser dry-run/snapshot build) that the tap/bucket repos receive the generated formula/manifest, then manually run `brew install`/`scoop install` from them once published.

## Open questions

- Which repository hosts the tap — a new dedicated `homebrew-lnpm` repo (Homebrew's naming convention requires the `homebrew-` prefix) or an existing org repo? Same question for the Scoop bucket naming/location.
