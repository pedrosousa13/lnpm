# Changelog

## [1.7.6](https://github.com/pedrosousa13/lnpm/compare/v1.7.5...v1.7.6) (2026-01-15)


### Performance Improvements

* parallel hashing/linking, remove npm pack dep, fix defer errors ([342272b](https://github.com/pedrosousa13/lnpm/commit/342272bd7a7c4ec978256dca58f8e70e5019a029))

## [1.7.5](https://github.com/pedrosousa13/lnpm/compare/v1.7.4...v1.7.5) (2026-01-15)


### Bug Fixes

* correct symlink depth for scoped packages ([3d3162d](https://github.com/pedrosousa13/lnpm/commit/3d3162df631e30ca281c14f2aaf6ea1b0898cd05))

## [1.7.4](https://github.com/pedrosousa13/lnpm/compare/v1.7.3...v1.7.4) (2026-01-15)


### Bug Fixes

* move goreleaser to release-please workflow ([2062221](https://github.com/pedrosousa13/lnpm/commit/2062221adf02162325bc6cacf3893b04b4dd2c86))

## [1.7.3](https://github.com/pedrosousa13/lnpm/compare/v1.7.2...v1.7.3) (2026-01-15)


### Bug Fixes

* merge release workflow into CI, ensure CI blocks release ([6b244ff](https://github.com/pedrosousa13/lnpm/commit/6b244ffd6e370ec490164b740823596cd0a2fab7))
* release-please not running after PR merge ([219f321](https://github.com/pedrosousa13/lnpm/commit/219f3212fabed3244a576c3863c5135783562e18))

## [1.7.2](https://github.com/pedrosousa13/lnpm/compare/v1.7.1...v1.7.2) (2026-01-15)


### Bug Fixes

* deprecated filepath.HasPrefix, align CI with release-please ([a2f7739](https://github.com/pedrosousa13/lnpm/commit/a2f77395378724a5f9f3526595cbe7993e8e3bb0))

## [1.7.1](https://github.com/pedrosousa13/lnpm/compare/v1.7.0...v1.7.1) (2026-01-15)


### Bug Fixes

* filepath.hasprefix ([46fac9c](https://github.com/pedrosousa13/lnpm/commit/46fac9cd8a7d10d2cd489da58bf4bb0e6c67d944))

## [1.7.0](https://github.com/pedrosousa13/lnpm/compare/v1.6.1...v1.7.0) (2026-01-15)


### Features

* better completions ([0a8ec9b](https://github.com/pedrosousa13/lnpm/commit/0a8ec9bf082cd415fd257452aa03df9aaf2ef33e))

## [1.6.1](https://github.com/pedrosousa13/lnpm/compare/v1.6.0...v1.6.1) (2026-01-15)


### Bug Fixes

* lnpm update auto ([7ca5607](https://github.com/pedrosousa13/lnpm/commit/7ca560779df7b68ebfc5fa9beda2e4b01c345a53))

## [1.6.0](https://github.com/pedrosousa13/lnpm/compare/v1.5.0...v1.6.0) (2026-01-15)


### Features

* npm pack + tests for all kinds of monorepo ([a7c3cb8](https://github.com/pedrosousa13/lnpm/commit/a7c3cb85605291627d47c4faa77ccf26ea6e3a72))


### Bug Fixes

* CI failures ([ff1d541](https://github.com/pedrosousa13/lnpm/commit/ff1d541da1d2a8447c07b062795b23bb41623a93))

## [1.5.0](https://github.com/pedrosousa13/lnpm/compare/v1.4.0...v1.5.0) (2026-01-15)


### Features

* check files against stage ([5b2016b](https://github.com/pedrosousa13/lnpm/commit/5b2016b2ab356816a48cce77e38de6bc4df495d8))

## [1.4.0](https://github.com/pedrosousa13/lnpm/compare/v1.3.0...v1.4.0) (2026-01-15)


### Features

* keep pushing for perf improvements ([78ce20b](https://github.com/pedrosousa13/lnpm/commit/78ce20bc19d9a04dca529b0f8035cf091dc60695))


### Bug Fixes

* lnpm update should always check for latest version ([24ac467](https://github.com/pedrosousa13/lnpm/commit/24ac467db062867d5132a114b05bd6a60d112909))

## [1.3.0](https://github.com/pedrosousa13/lnpm/compare/v1.2.0...v1.3.0) (2026-01-15)


### Features

* improve perf further ([e7503d0](https://github.com/pedrosousa13/lnpm/commit/e7503d0c57ac501c4d46f2dc6466463b366728f5))

## [1.2.0](https://github.com/pedrosousa13/lnpm/compare/v1.1.1...v1.2.0) (2026-01-15)


### Features

* improve perf by using hard links ([465b64b](https://github.com/pedrosousa13/lnpm/commit/465b64bb73c22e7897dfe2cc116fb8ab4d0b1002))
* lnpm update command ([743e09f](https://github.com/pedrosousa13/lnpm/commit/743e09fbc0e2f197456db49632e480ca2e6d4816))

## [Unreleased]

### Added

- **Reflink (Copy-on-Write) support** for instant file operations on APFS (macOS) and Btrfs/XFS (Linux)
- **Hard link support during publish** - Store operations now use hard links when source and store are on same filesystem
- **Parallel copy operations** - Up to 8 concurrent workers for 4-8x faster copying when linking isn't possible
- **Intelligent linking strategy** - Automatic priority system: reflink → hardlink → parallel copy
- **Config integration** - `link_mode` configuration option is now properly respected
- **Enhanced user feedback** - Clear warnings and tips when falling back from linking to copying
- **Cross-filesystem detection** - Automatic detection and helpful messages for cross-filesystem scenarios

### Performance

- **Up to 1000x faster** for packages with 10,000+ files on modern filesystems (APFS/Btrfs/XFS)
- **Instant publishing** when source and store are on same filesystem
- **4-8x faster copying** when cross-filesystem operations are required

## [1.1.1](https://github.com/pedrosousa13/lnpm/compare/v1.1.0...v1.1.1) (2026-01-15)


### Bug Fixes

* remove unused collectFiles function ([64c2bca](https://github.com/pedrosousa13/lnpm/commit/64c2bcabea63ee65b6f2476bda0a716baed9acba))

## [1.1.0](https://github.com/pedrosousa13/lnpm/compare/v1.0.2...v1.1.0) (2026-01-15)


### Features

* progress indicators + perf improvements ([82d0571](https://github.com/pedrosousa13/lnpm/commit/82d05715e8ab03f06a5cf710479980137f79a115))

## [1.0.2](https://github.com/pedrosousa13/lnpm/compare/v1.0.1...v1.0.2) (2026-01-15)


### Bug Fixes

* run goreleaser in release-please workflow ([9133260](https://github.com/pedrosousa13/lnpm/commit/91332603073902ca6276ff503f1865f187e93b77))

## [1.0.1](https://github.com/pedrosousa13/lnpm/compare/v1.0.0...v1.0.1) (2026-01-15)


### Bug Fixes

* build error ([7b553b4](https://github.com/pedrosousa13/lnpm/commit/7b553b4585b841cc0cb03f7b7c9fd4a932fa6501))
* goreleaser config ([1a055d1](https://github.com/pedrosousa13/lnpm/commit/1a055d199ad9c7dcd7e2e21d9c205821bb14f9af))

## 1.0.0 (2026-01-15)


### Bug Fixes

* build error ([7b553b4](https://github.com/pedrosousa13/lnpm/commit/7b553b4585b841cc0cb03f7b7c9fd4a932fa6501))
