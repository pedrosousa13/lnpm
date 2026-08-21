# Changelog

## [2.0.0](https://github.com/pedrosousa13/lnpm/compare/v1.12.0...v2.0.0) (2026-08-21)


### Features

* **link:** relink only the files that changed since the last link ([#295](https://github.com/pedrosousa13/lnpm/issues/295)) ([b60c58c](https://github.com/pedrosousa13/lnpm/commit/b60c58c1316f32e1b9db3b862e0a42badba02552))
* **list:** add version history and roll back with add &lt;pkg&gt;@&lt;hash&gt; ([#298](https://github.com/pedrosousa13/lnpm/issues/298)) ([53e73bc](https://github.com/pedrosousa13/lnpm/commit/53e73bc1630aa5ff4fdf2cd97ef7daa48d8f6b9e))
* **restore:** add `lnpm restore` to re-link packages after `retreat` ([#294](https://github.com/pedrosousa13/lnpm/issues/294)) ([472f1be](https://github.com/pedrosousa13/lnpm/commit/472f1be8374175e0eff31d3ba2a98d5928262ae6))


### Bug Fixes

* **add:** roll a package back when its package.json update fails in multi-add ([#244](https://github.com/pedrosousa13/lnpm/issues/244)) ([68039f3](https://github.com/pedrosousa13/lnpm/commit/68039f3fdf4fc4af7948a7ade24638456956caed))
* **add:** save the lock file before rewriting package.json ([#243](https://github.com/pedrosousa13/lnpm/issues/243)) ([4c233f1](https://github.com/pedrosousa13/lnpm/commit/4c233f16130d551c41fc6b40e46bb2c5eff57e8e))
* align build-time ldflags and surface commit and date in --version ([#281](https://github.com/pedrosousa13/lnpm/issues/281)) ([a8b72ac](https://github.com/pedrosousa13/lnpm/commit/a8b72ac99911c6a8669d1807c422b92a42cce066)), closes [#177](https://github.com/pedrosousa13/lnpm/issues/177)
* chmod linked and cloned files so umask cannot strip mode bits ([#274](https://github.com/pedrosousa13/lnpm/issues/274)) ([54c11fe](https://github.com/pedrosousa13/lnpm/commit/54c11feaf0ddea3711e6fbe0f047466e8dfcb01f)), closes [#139](https://github.com/pedrosousa13/lnpm/issues/139)
* fail doctor when it finds issues and print its markers through the icon helpers ([#277](https://github.com/pedrosousa13/lnpm/issues/277)) ([fba8385](https://github.com/pedrosousa13/lnpm/commit/fba838514b72d7f8f78c688478b02327515960d9)), closes [#162](https://github.com/pedrosousa13/lnpm/issues/162)
* fall back to the user completion directory when the system one refuses the write ([#278](https://github.com/pedrosousa13/lnpm/issues/278)) ([c1439d8](https://github.com/pedrosousa13/lnpm/commit/c1439d8b192c36b433eaf58e39264089b3ba6a0b)), closes [#169](https://github.com/pedrosousa13/lnpm/issues/169)
* **fsutil:** call clonefile with the right signature on macOS ([#229](https://github.com/pedrosousa13/lnpm/issues/229)) ([cfbfa9f](https://github.com/pedrosousa13/lnpm/commit/cfbfa9f7d40a56883d4a8c5264d46dd08654f2f2)), closes [#135](https://github.com/pedrosousa13/lnpm/issues/135)
* **gc:** reclaim temp directories left by an interrupted publish or relink ([#289](https://github.com/pedrosousa13/lnpm/issues/289)) ([e021c81](https://github.com/pedrosousa13/lnpm/commit/e021c81a696b863065b2edad1d5e8ddb979a27dd)), closes [#233](https://github.com/pedrosousa13/lnpm/issues/233)
* give store entries a completeness marker ([#273](https://github.com/pedrosousa13/lnpm/issues/273)) ([6650434](https://github.com/pedrosousa13/lnpm/commit/665043464c9ec2a93fd44332d3be56b07ee89323)), closes [#237](https://github.com/pedrosousa13/lnpm/issues/237)
* keep GetDB's init error so every caller sees it ([#290](https://github.com/pedrosousa13/lnpm/issues/290)) ([c73954a](https://github.com/pedrosousa13/lnpm/commit/c73954a0c50610b5b5a4f3fba87dafe7baca1e47)), closes [#253](https://github.com/pedrosousa13/lnpm/issues/253)
* **link:** populate a temp directory and rename-swap instead of clearing the live package ([#232](https://github.com/pedrosousa13/lnpm/issues/232)) ([b3f0a47](https://github.com/pedrosousa13/lnpm/commit/b3f0a479f2b422d4376d3c8902136566b869bd06)), closes [#137](https://github.com/pedrosousa13/lnpm/issues/137)
* make hooks.skip_post_add actually skip the post-add hook ([#280](https://github.com/pedrosousa13/lnpm/issues/280)) ([7079f81](https://github.com/pedrosousa13/lnpm/commit/7079f81a58d34cfe1878e06bc4c6021786304e63)), closes [#171](https://github.com/pedrosousa13/lnpm/issues/171)
* remove the updater's temp directory and match Go bin dirs by path component ([#276](https://github.com/pedrosousa13/lnpm/issues/276)) ([b29b9da](https://github.com/pedrosousa13/lnpm/commit/b29b9da8a4b87e568271aea72a87829774c53c96)), closes [#147](https://github.com/pedrosousa13/lnpm/issues/147)
* **remove:** keep the lock entry when remove fails to restore package.json ([#245](https://github.com/pedrosousa13/lnpm/issues/245)) ([c13b392](https://github.com/pedrosousa13/lnpm/commit/c13b392d24f3b6418c3e0bdf77ed768de2f0340a))
* report scoped packages by full name and clean up emptied scope directories ([#279](https://github.com/pedrosousa13/lnpm/issues/279)) ([ac2b4b9](https://github.com/pedrosousa13/lnpm/commit/ac2b4b99d60e2f1cbcc70453e81a9b794ae8aa89)), closes [#170](https://github.com/pedrosousa13/lnpm/issues/170) [#236](https://github.com/pedrosousa13/lnpm/issues/236)
* stop hardlinking source files into the store ([#213](https://github.com/pedrosousa13/lnpm/issues/213)) ([624a3e3](https://github.com/pedrosousa13/lnpm/commit/624a3e3e857b3023984a38359e80a4b87082f291))
* stop three tests from passing without checking their subject ([#282](https://github.com/pedrosousa13/lnpm/issues/282)) ([7ef8a02](https://github.com/pedrosousa13/lnpm/commit/7ef8a025a0d32d5c06752fe1afbcf473f0f623cd)), closes [#186](https://github.com/pedrosousa13/lnpm/issues/186)
* **store:** never delete the destination before the atomic rename ([#238](https://github.com/pedrosousa13/lnpm/issues/238)) ([f6ebaf7](https://github.com/pedrosousa13/lnpm/commit/f6ebaf7b0bc0a009cbf8ab9ee220b5a7a516146d)), closes [#138](https://github.com/pedrosousa13/lnpm/issues/138)
* stream copies with io.Copy and unlink half-made reflink clones ([#275](https://github.com/pedrosousa13/lnpm/issues/275)) ([9a41948](https://github.com/pedrosousa13/lnpm/commit/9a41948855bc4726a6414f84312e3aa5c0cc5ba5)), closes [#140](https://github.com/pedrosousa13/lnpm/issues/140)
* treat a degenerate files entry as including everything ([#272](https://github.com/pedrosousa13/lnpm/issues/272)) ([a9f9a52](https://github.com/pedrosousa13/lnpm/commit/a9f9a52b5348017649b3041a842d29256aeccd6b)), closes [#227](https://github.com/pedrosousa13/lnpm/issues/227)
* **update:** report update-check failures instead of "Already up to date" ([#239](https://github.com/pedrosousa13/lnpm/issues/239)) ([400cdf1](https://github.com/pedrosousa13/lnpm/commit/400cdf1eac3621412096e606a7b88d686bc3ce48)), closes [#144](https://github.com/pedrosousa13/lnpm/issues/144)
* **update:** stage the updated binary next to the target to avoid cross-filesystem rename failures ([#240](https://github.com/pedrosousa13/lnpm/issues/240)) ([6b4e102](https://github.com/pedrosousa13/lnpm/commit/6b4e1020dba52825bbf44af2e4924bd96afd57d7))
* **workspace:** abort ListPackages on a member that will not read or parse ([#293](https://github.com/pedrosousa13/lnpm/issues/293)) ([cd1aca9](https://github.com/pedrosousa13/lnpm/commit/cd1aca9483aed5609c1dcea564b00c89f0ba0f82))
* **workspace:** fail expansion when a workspace glob pattern will not parse ([#287](https://github.com/pedrosousa13/lnpm/issues/287)) ([16c83a3](https://github.com/pedrosousa13/lnpm/commit/16c83a379d3122c14a51bb53520123fb6413d60b)), closes [#241](https://github.com/pedrosousa13/lnpm/issues/241)
* **workspace:** subtract negation patterns in publish --all instead of dropping them ([#242](https://github.com/pedrosousa13/lnpm/issues/242)) ([812d21d](https://github.com/pedrosousa13/lnpm/commit/812d21dba139fd9151ae6cbeac3907b5ca5a37b3))


### Continuous Integration

* check PR titles and document the 2.0.0 upgrade path ([#299](https://github.com/pedrosousa13/lnpm/issues/299)) ([89745e9](https://github.com/pedrosousa13/lnpm/commit/89745e96079487d592349b0aa56814b281448da8))

## [1.12.0](https://github.com/pedrosousa13/lnpm/compare/v1.11.0...v1.12.0) (2026-07-23)


### Features

* add `lnpm check` guard and `add --link` protocol ([#56](https://github.com/pedrosousa13/lnpm/issues/56)) ([58e1345](https://github.com/pedrosousa13/lnpm/commit/58e1345b323cb9d6f309b9a2c7851acdf9d66cdf))


### Bug Fixes

* **config:** support editor commands with args ([#204](https://github.com/pedrosousa13/lnpm/issues/204)) ([6b1727d](https://github.com/pedrosousa13/lnpm/commit/6b1727dc15e1a0c87d1f322efd96b3a3c7e0dccc))
* detect bun.lock text lockfile and honor list package argument ([#205](https://github.com/pedrosousa13/lnpm/issues/205)) ([f9b0cfb](https://github.com/pedrosousa13/lnpm/commit/f9b0cfb02b9a8722c0d4d03cdc9880a0de603f71))

## [1.11.0](https://github.com/pedrosousa13/lnpm/compare/v1.10.0...v1.11.0) (2026-06-24)


### Features

* **cli:** NO_COLOR/tty-aware output, rune-safe truncate, consistent confirmations, wire pre/post_publish hooks ([#53](https://github.com/pedrosousa13/lnpm/issues/53), [#51](https://github.com/pedrosousa13/lnpm/issues/51)) ([9cd95a2](https://github.com/pedrosousa13/lnpm/commit/9cd95a2b892ba18f4b6d714dcf963c8399449281))


### Bug Fixes

* DeletePackage cleans up links + add GetProjectByID ([#44](https://github.com/pedrosousa13/lnpm/issues/44)) ([7d84361](https://github.com/pedrosousa13/lnpm/commit/7d84361da7b1b2d797d0f6f50f2d04a27b708b9e))
* honor store_path config option ([#51](https://github.com/pedrosousa13/lnpm/issues/51) partial) ([8a2b83d](https://github.com/pedrosousa13/lnpm/commit/8a2b83da7c95fc4eff22d477526193df9938c3c3))
* make content hash correct and deterministic ([#45](https://github.com/pedrosousa13/lnpm/issues/45), [#46](https://github.com/pedrosousa13/lnpm/issues/46)) ([0c9450d](https://github.com/pedrosousa13/lnpm/commit/0c9450d9fe404d939c52c1dc31fc69b611cd50d0))
* make store writes atomic via temp dir + rename ([#47](https://github.com/pedrosousa13/lnpm/issues/47)) ([8324b9c](https://github.com/pedrosousa13/lnpm/commit/8324b9cff0c16113a00d8ebd6aa8395c6cea2fa9))
* non-zero exit on partial failure; silence usage on error ([#48](https://github.com/pedrosousa13/lnpm/issues/48)) ([faa800f](https://github.com/pedrosousa13/lnpm/commit/faa800f7db2ea949cbdc8ab328f4cdb3c311d24d))
* reflink dead on Linux — pass FICLONE source fd by value ([#38](https://github.com/pedrosousa13/lnpm/issues/38)) ([4d08c1a](https://github.com/pedrosousa13/lnpm/commit/4d08c1afeb97c4ee35f931da4376149341a7917c))
* resolve add pkg@version by version; remove dead --tag ([#39](https://github.com/pedrosousa13/lnpm/issues/39)) ([a29a0ad](https://github.com/pedrosousa13/lnpm/commit/a29a0ad97dcef2c96aee71a6b56a4caa1a763b2b))
* skip symlinks during packing to prevent file exfiltration ([#42](https://github.com/pedrosousa13/lnpm/issues/42)) ([1451c9b](https://github.com/pedrosousa13/lnpm/commit/1451c9b853d4bd4241bd9c07d97a0ebddc117ab3))
* validate package names to prevent path traversal ([#40](https://github.com/pedrosousa13/lnpm/issues/40)) ([7547650](https://github.com/pedrosousa13/lnpm/commit/7547650a54bc1421575a3ad58426d14b36f5f32d))
* verify SHA-256 checksum on self-update + add HTTP timeout ([#41](https://github.com/pedrosousa13/lnpm/issues/41)) ([5a57a87](https://github.com/pedrosousa13/lnpm/commit/5a57a877b965abb6328109c523b2a8516e551cfe))

## [1.10.0](https://github.com/pedrosousa13/lnpm/compare/v1.9.0...v1.10.0) (2026-03-05)


### Features

* windows support ([#36](https://github.com/pedrosousa13/lnpm/issues/36)) ([c80f364](https://github.com/pedrosousa13/lnpm/commit/c80f3641f762d58c294c52d4f8c04f7d37b8bab5))

## [1.9.0](https://github.com/pedrosousa13/lnpm/compare/v1.8.2...v1.9.0) (2026-01-30)


### Features

* **add:** support multiple packages in single command ([#32](https://github.com/pedrosousa13/lnpm/issues/32)) ([8d52feb](https://github.com/pedrosousa13/lnpm/commit/8d52febe19fd02ce09112b066f0e6f2b86a4a88d))

## [1.8.2](https://github.com/pedrosousa13/lnpm/compare/v1.8.1...v1.8.2) (2026-01-19)


### Bug Fixes

* align add and push with yalc ([#28](https://github.com/pedrosousa13/lnpm/issues/28)) ([1abccde](https://github.com/pedrosousa13/lnpm/commit/1abccde8f8ead26a13de6711cd91bdd5aeecc779))

## [1.8.1](https://github.com/pedrosousa13/lnpm/compare/v1.8.0...v1.8.1) (2026-01-19)


### Performance Improvements

* **push:** parallelize link, push, publish ops ([#26](https://github.com/pedrosousa13/lnpm/issues/26)) ([f8a9936](https://github.com/pedrosousa13/lnpm/commit/f8a9936e2235e819c0043be9fb415245370363be))

## [1.8.0](https://github.com/pedrosousa13/lnpm/compare/v1.7.7...v1.8.0) (2026-01-19)


### Features

* add manage_gitignore config option ([cb6f51f](https://github.com/pedrosousa13/lnpm/commit/cb6f51f7e546c2fec84785019cee3ed7ea5263bb))


### Bug Fixes

* **ci:** skip concurrent package.json write test ([79360bb](https://github.com/pedrosousa13/lnpm/commit/79360bb422135b56626e30129015bb20db11343d))
* **ci:** skip flaky concurrent test in CI ([8b11c09](https://github.com/pedrosousa13/lnpm/commit/8b11c09d98577cd5dbd97cdecefb37d0c17f6178))
* remove unused fmt import ([2122a51](https://github.com/pedrosousa13/lnpm/commit/2122a515cb1abaf5735212ca0382a08c765b844b))
* revert db path and remove t.Parallel() ([0e33339](https://github.com/pedrosousa13/lnpm/commit/0e333397a6ed31463b59d1a8edaa0a9978da7911))
* skip flaky symlink test in CI ([dec5411](https://github.com/pedrosousa13/lnpm/commit/dec5411353cc05de4aba534071d460e9d693cb2b))
* test failures and permission handling ([9c4030b](https://github.com/pedrosousa13/lnpm/commit/9c4030b61cbe3b93d72ec1d6235e7ff623511c87))
* **tests:** path normalization & test fixes ([f239a04](https://github.com/pedrosousa13/lnpm/commit/f239a049d0914fd7fb6af5aefd49a71035e9b11b))
* wrap all unchecked error returns ([a8b3431](https://github.com/pedrosousa13/lnpm/commit/a8b3431a7b300cd74edcc7bdb7cdb487ebec558c))
* wrap defer os.Chmod in anonymous functions ([ffb08da](https://github.com/pedrosousa13/lnpm/commit/ffb08daab46e181f0a911ee791377083d36daacf))
* wrap defer os.Chmod in store tests ([ca75562](https://github.com/pedrosousa13/lnpm/commit/ca7556231e711c640c9cc8202e8cb964c1c9ac52))

## [1.7.7](https://github.com/pedrosousa13/lnpm/compare/v1.7.6...v1.7.7) (2026-01-15)


### Bug Fixes

* race condition in pack ([e5f6793](https://github.com/pedrosousa13/lnpm/commit/e5f67935af042be7e952276aa9f741938c749f17))

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
