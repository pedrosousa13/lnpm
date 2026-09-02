# Distributing lnpm through the npm registry

lnpm is not published to the npm registry and is not going to be. The install
paths are Homebrew, Scoop, the `install.sh` / `install.ps1` scripts, the deb,
rpm and apk packages GoReleaser builds, and `go install`.

## Why this keeps being proposed

The argument is not about the implementation language. It is that lnpm's users
are JavaScript developers who already have npm on the machine, so `npm i -g` is
the install command they reach for before any other. Several Go and Rust
command-line tools aimed at the same audience ship on npm for that reason, with
a `postinstall` script that fetches the platform binary from a GitHub release.
esbuild, swc and biome are the usual examples.

That reasoning is sound as far as it goes, and it is why the option was costed
rather than dismissed.

## Why it is out of scope

**The name is taken.** `lnpm` on the npm registry is an unrelated package,
"npm client for cnpmjs.org", latest version 3.4.4. Measured 2026-09-02:
`https://registry.npmjs.org/lnpm` returns HTTP 200 with that manifest. So the
install command the proposal exists to provide, `npm i -g lnpm`, is not
available at any price. `lnpm-cli`, `@lnpm/cli` and `lnpm-bin` were all
unclaimed on the same date, and any of them would work, but none of them is the
reflex the argument rests on. A developer who has to look up the package name is
a developer who could equally have looked up `brew install`.

**The cost is a permanent one.** A registry package is a release surface that has
to stay green: a `postinstall` that fetches per-platform archives and verifies
them against the signed `checksums.txt`, a launcher shim, a publish step in
`.github/workflows/release-please.yaml`, and an `NPM_TOKEN` secret with its own
rotation and its own stale-credential failure mode. `docs/releasing.md` already
records what one stale credential costs this pipeline. Adding a second is not
free.

**Homebrew and Scoop cover the same platforms.** They landed for #194 and reach
macOS, Linux and Windows without a Node runtime anywhere in the path. The
remaining gap is narrow: a developer who has Node but not Homebrew, on a machine
where `curl | sh` is unwelcome.

## The counter-argument that was heard and rejected

A scoped package under `@lnpm` would sidestep the name collision entirely and
leave the whole namespace owned rather than contested. It would also read as
official in a way `lnpm-cli` does not.

It was rejected on the same cost grounds. The scope removes the name problem
without restoring the reflex, since `npm i -g @lnpm/cli` is no more memorable
than `brew install`, and it adds the extra step of creating the organisation and
publishing with `--access public`. The decision is that the install story is
already adequate for the audience and does not justify a second signed
distribution channel.

Revisit if a measured share of would-be users report bouncing off installation,
rather than on the strength of the argument alone.
