TITLE: Fix deprecated GoReleaser fields and pin the GoReleaser version
LABELS: ci
---
## Severity

Medium — deprecated config fields plus an unpinned GoReleaser version mean the next release can break at release time with no prior warning.

## Background

GoReleaser is the tool that builds cross-platform binaries and publishes them (as archives, checksums, and deb/rpm/apk packages) to GitHub Releases. Its behavior is driven by `.goreleaser.yaml`. The release workflow (`.github/workflows/release-please.yaml`) runs GoReleaser via `goreleaser/goreleaser-action`, and currently asks it to install `version: latest` — meaning whatever GoReleaser version happens to be newest when the release runs. In GoReleaser v2.6 the singular `format` fields under `archives` were deprecated in favor of a plural `formats` list. Deprecated fields keep working for a while and then are removed in a later major/minor release.

## Problem

The archive config uses the deprecated `format:` and `format_overrides[].format:` fields. Because the release job pulls `version: latest`, the exact GoReleaser version is decided at release time. On the day GoReleaser removes the deprecated fields, the very next release will fail — during the release, not during a normal PR — and block shipping until someone debugs the config under pressure.

## Where to look

- `.goreleaser.yaml:31` — `format: tar.gz` under `archives` (deprecated; should be `formats: [tar.gz]`).
- `.goreleaser.yaml:33-35` — `format_overrides:` with `format: zip` for windows (deprecated; should be `formats: [zip]`).
- `.github/workflows/release-please.yaml:53` — `version: latest` for the GoReleaser action (should be pinned).

## How to fix

1. In `.goreleaser.yaml:31`, replace `format: tar.gz` with `formats: [tar.gz]`.
2. In `.goreleaser.yaml:33-35`, replace the `format: zip` override with `formats: [zip]` (keep the `goos: windows` matcher).
3. In `.github/workflows/release-please.yaml:53`, pin the GoReleaser version instead of `latest`. At minimum use a major-version constraint like `version: "~> v2"` so patch/minor updates flow in but a major bump does not silently change behavior.
4. Validate the config with `goreleaser check` (see Testing). It reports deprecation warnings and hard errors.

## Acceptance criteria

- [ ] `.goreleaser.yaml` uses `formats:` (plural) everywhere; no `format:` remains.
- [ ] `goreleaser check` reports no deprecation warnings and no errors.
- [ ] The release workflow pins GoReleaser to a major version (e.g. `~> v2`) rather than `latest`.
- [ ] A snapshot build still produces the expected `.tar.gz` archives for linux/darwin and a `.zip` for windows.

## Testing

- Run `goreleaser check` against `.goreleaser.yaml` — it must pass cleanly.
- Run a local dry-run that does not publish: `goreleaser release --snapshot --clean` and inspect the `dist/` output to confirm the archive extensions are correct per OS.
- Optionally trigger the release workflow in a dry-run/test context; note the real release job only runs on a release-please-created release, so the `--snapshot` local check is the primary verification.
