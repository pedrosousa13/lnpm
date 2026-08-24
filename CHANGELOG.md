# Changelog

## [2.3.0](https://github.com/pedrosousa13/lnpm/compare/v2.2.1...v2.3.0) (2026-08-24)

> **Using a `files` field?** Three changes land together, and the packed set of an
> existing package can move in both directions. Check with `lnpm publish` before
> relying on it.
>
> A `files` entry now overrides the built-in "excluded unless you say otherwise"
> list — but only for a path it **names directly**. `files: [".env.example"]`
> ships that template; `files: ["dist"]` still keeps `dist/.env` out, because
> naming a build directory is not a statement about what landed inside it. A
> `main` entry is not a way in: it loses to both built-in lists and warns instead.
> See [#321](https://github.com/pedrosousa13/lnpm/issues/321).
>
> An entry written `./dist` used to select **nothing**, publishing a package that
> held only `package.json`. It now selects what `dist` does. A bare `.` still
> selects nothing, which is what npm does with it. See
> [#346](https://github.com/pedrosousa13/lnpm/issues/346).
>
> `files` entries now glob through the same engine as your ignore patterns, so
> `**` spans zero or more path segments: `files: ["lib/**/*.js"]` now selects
> `lib/top.js` as well as `lib/sub/a.js`. Brace alternation comes with it, so
> `files: ["weird{a,b}.txt"]` matches `weirda.txt` and `weirdb.txt` — the file of
> that literal name is still selected too, by an exact-path compare npm does not
> have. One gap remains: a `files` entry ending in a wildcard does not expand a
> directory it matched into that directory's subtree, where npm does, so `["*"]`
> ships only the package root's own files. See
> [#350](https://github.com/pedrosousa13/lnpm/issues/350) and
> [#406](https://github.com/pedrosousa13/lnpm/issues/406).
>
> **A publish no longer aborts on an unreadable directory it already excludes.** A
> root-owned `coverage/`, or a `.cache/` left by a tool running as another user,
> used to fail the whole publish by name even when your ignore file excluded it.
> It is now skipped with a warning. A directory that **would** have been packed
> still aborts, because a package silently missing a file is worse than a failed
> command. See [#348](https://github.com/pedrosousa13/lnpm/issues/348).
>
> **Symlinked your `.lnpm` directory?** Read-only link queries now refuse to
> resolve through it, closing the same hole
> [#339](https://github.com/pedrosousa13/lnpm/issues/339) closed for writes in
> 2.2.1. There is no override for this one: the `follow_symlinked_node_modules`
> setting governs `node_modules`, not `.lnpm`. See
> [#340](https://github.com/pedrosousa13/lnpm/issues/340).

### Features

* **push:** list the packed files on a steady-state push ([#397](https://github.com/pedrosousa13/lnpm/issues/397)) ([7fdf579](https://github.com/pedrosousa13/lnpm/commit/7fdf579b69143a7b485108fb67ff21c9dac415bc)), closes [#372](https://github.com/pedrosousa13/lnpm/issues/372)


### Bug Fixes

* **db:** report unreadable link data instead of dropping it ([#393](https://github.com/pedrosousa13/lnpm/issues/393)) ([bbf03c1](https://github.com/pedrosousa13/lnpm/commit/bbf03c1e8a0128918ee4033da805375f11fd9554)), closes [#355](https://github.com/pedrosousa13/lnpm/issues/355)
* **gc:** stop claiming a package row it did not delete ([#395](https://github.com/pedrosousa13/lnpm/issues/395)) ([30e0ae8](https://github.com/pedrosousa13/lnpm/commit/30e0ae8fa85708626c338aacc1f4e9747073fb2a)), closes [#358](https://github.com/pedrosousa13/lnpm/issues/358)
* **link:** refuse read-only link queries through a symlinked .lnpm ([#401](https://github.com/pedrosousa13/lnpm/issues/401)) ([9fa924e](https://github.com/pedrosousa13/lnpm/commit/9fa924e84852149263b26b27e53199332df5508c)), closes [#340](https://github.com/pedrosousa13/lnpm/issues/340)
* **pack:** glob a "files" entry with the engine ignore patterns use ([#350](https://github.com/pedrosousa13/lnpm/issues/350)) ([50bf8b7](https://github.com/pedrosousa13/lnpm/commit/50bf8b723c4595f96e77e69ebaf091ef08e66614))
* **pack:** let a files entry override defaultExcludes, or warn when it cannot ([#400](https://github.com/pedrosousa13/lnpm/issues/400)) ([5ecf0a0](https://github.com/pedrosousa13/lnpm/commit/5ecf0a0fb9dcf7c9970f49bf3e10231f020dbdd8)), closes [#321](https://github.com/pedrosousa13/lnpm/issues/321)
* **pack:** resolve a leading "./" in a "files" entry ([#346](https://github.com/pedrosousa13/lnpm/issues/346)) ([37b3f91](https://github.com/pedrosousa13/lnpm/commit/37b3f91d9ef0f87e1098e2ab2dc41fbdc129d3a7))
* **pack:** skip an unreadable directory the package already excludes ([#348](https://github.com/pedrosousa13/lnpm/issues/348)) ([6ce2a3b](https://github.com/pedrosousa13/lnpm/commit/6ce2a3b6160c3826ee6fd79327d871e79f276890))
* **tests:** stop test binaries reading the machine's own config ([#396](https://github.com/pedrosousa13/lnpm/issues/396)) ([f86c2ec](https://github.com/pedrosousa13/lnpm/commit/f86c2ec181cf456520eee134b34d96f6821c9d3e)), closes [#371](https://github.com/pedrosousa13/lnpm/issues/371)

## [2.2.1](https://github.com/pedrosousa13/lnpm/compare/v2.2.0...v2.2.1) (2026-08-23)

> **Relocated your `node_modules`?** lnpm now refuses to link or unlink through a
> `node_modules` — or a `node_modules/@scope` — that is not a real directory,
> because a symlink there redirects lnpm's writes and deletes outside your
> project. If you relocate `node_modules` deliberately, set
> `follow_symlinked_node_modules: true` in `~/.lnpm/config.yaml` to restore the
> previous behaviour. The refusal message names the file and the key. See
> [#339](https://github.com/pedrosousa13/lnpm/issues/339).
>
> **Upgrading from 1.x?** Store entries written before 2.0.0 carry no
> completeness marker, and lnpm now checks that marker before serving an entry.
> The first command that opens your store migrates it in one pass; until then
> `lnpm doctor` reports the store as pending rather than damaged. If it reports
> that the migration cannot run, a directory in your store could not be read —
> make it readable and run any command again. See
> [#330](https://github.com/pedrosousa13/lnpm/issues/330).
>
> **`lnpm gc` is now more conservative.** It will not collect a package whose
> only consuming project sits on a filesystem that is not mounted where it was
> linked — an unplugged drive or an unmounted network share no longer costs you
> the store entry. Those links are reported as skipped. The trade is that a
> drive gone for good leaves its entries uncollectable for now; see
> [#382](https://github.com/pedrosousa13/lnpm/issues/382).


### Bug Fixes

* **cli:** write package.json through a temp file and rename ([#377](https://github.com/pedrosousa13/lnpm/issues/377)) ([fb6bbe5](https://github.com/pedrosousa13/lnpm/commit/fb6bbe51bc67c0cd7c5cdf426aab1063826b298a)), closes [#324](https://github.com/pedrosousa13/lnpm/issues/324)
* **gc:** do not collect when a project's filesystem is not mounted ([#383](https://github.com/pedrosousa13/lnpm/issues/383)) ([9e3ccf6](https://github.com/pedrosousa13/lnpm/commit/9e3ccf6be3574eafe2aaef238a32204d3930f756)), closes [#335](https://github.com/pedrosousa13/lnpm/issues/335)
* **link:** refuse a symlinked node_modules or scope directory ([#388](https://github.com/pedrosousa13/lnpm/issues/388)) ([4ad8ffb](https://github.com/pedrosousa13/lnpm/commit/4ad8ffb8f6ef6241d93219a5306a4a946c5ff72a)), closes [#339](https://github.com/pedrosousa13/lnpm/issues/339)
* **pack:** reject package names whose segments begin with a dot ([#379](https://github.com/pedrosousa13/lnpm/issues/379)) ([7f1b524](https://github.com/pedrosousa13/lnpm/commit/7f1b52485ac1523af20720c3dc15040882ef8466)), closes [#325](https://github.com/pedrosousa13/lnpm/issues/325)
* **store:** check the completeness marker on the read path ([#381](https://github.com/pedrosousa13/lnpm/issues/381)) ([68b3cb8](https://github.com/pedrosousa13/lnpm/commit/68b3cb8ca29144c39ae84601fa0635ac7faaf621)), closes [#330](https://github.com/pedrosousa13/lnpm/issues/330)

## [2.2.0](https://github.com/pedrosousa13/lnpm/compare/v2.1.0...v2.2.0) (2026-08-23)

> **Packages that excluded `package.json`**: lnpm now always packs the package
> root's own `package.json`, whatever your `.npmignore`, `.gitignore` or `files`
> field says, and refuses to pack at all if the manifest is missing. If one of
> your packages was excluding its manifest, its content hash changes with this
> release — the manifest is what carries the version string into the hashed
> content — so its next publish writes a new store entry instead of updating the
> old one. Only packages exhibiting the bug are affected. See
> [#301](https://github.com/pedrosousa13/lnpm/issues/301).


### Features

* **publish:** add --dry-run and print the packed file list ([3354320](https://github.com/pedrosousa13/lnpm/commit/335432039719e383d1352f603fd33dd0cfba0bbf))


### Bug Fixes

* **lockfile:** bound lock-file and workspace-config size before parsing ([032190d](https://github.com/pedrosousa13/lnpm/commit/032190d8f76bf1e240f007305752adec81103faa))
* **pack:** always pack the manifest, whatever the ignore rules say ([b47a0c3](https://github.com/pedrosousa13/lnpm/commit/b47a0c3d6c8fd6d6fc2bd3f31f63b62ff73a1434))

## [2.1.0](https://github.com/pedrosousa13/lnpm/compare/v2.0.0...v2.1.0) (2026-08-23)


### Features

* **check:** cover every workspace package, not just the working directory ([#337](https://github.com/pedrosousa13/lnpm/issues/337)) ([0ee3ad5](https://github.com/pedrosousa13/lnpm/commit/0ee3ad536593550e345f3c980573a46f699f9803))


### Bug Fixes

* **gc:** abort when a package's links cannot be read ([683f5c8](https://github.com/pedrosousa13/lnpm/commit/683f5c8651b1aef6cc48445b70c37f23ed456b67))
* **gc:** abort when a project row cannot be read ([7445dbe](https://github.com/pedrosousa13/lnpm/commit/7445dbe2d17de14a0d91f3685b005f2327be80fd))
* **gc:** confirm before deleting orphaned links and report what was removed ([ccdcd0b](https://github.com/pedrosousa13/lnpm/commit/ccdcd0b4d537301aa5a87df3c8c7a3ac45cfd6f4))
* **link:** refuse to link through a symlinked .lnpm or scope directory ([#341](https://github.com/pedrosousa13/lnpm/issues/341)) ([d9beea2](https://github.com/pedrosousa13/lnpm/commit/d9beea2b3a22af1dab427317a614487c69c90efd))
* **pack:** anchor the always-included set to the package root ([c3dfe77](https://github.com/pedrosousa13/lnpm/commit/c3dfe772231b3b69d1a74e9884f5bc75c837771e))
* **pack:** force-include the main entry point under a files whitelist ([2156e15](https://github.com/pedrosousa13/lnpm/commit/2156e157f20ed3ffc067348a247ab57c1e847d03))
* **pack:** honour .npmignore and .gitignore in every directory ([#352](https://github.com/pedrosousa13/lnpm/issues/352)) ([a35b938](https://github.com/pedrosousa13/lnpm/commit/a35b93840f2136b7fdee95e63dba8247abace343)), closes [#315](https://github.com/pedrosousa13/lnpm/issues/315)
* **pack:** implement ** in ignore patterns as zero or more path segments ([#351](https://github.com/pedrosousa13/lnpm/issues/351)) ([faedffa](https://github.com/pedrosousa13/lnpm/commit/faedffa6de8721a0fc8faf6208990f54f2c59d68)), closes [#316](https://github.com/pedrosousa13/lnpm/issues/316)
* **pack:** make the files whitelist win over .npmignore and .gitignore ([#349](https://github.com/pedrosousa13/lnpm/issues/349)) ([60c85a5](https://github.com/pedrosousa13/lnpm/commit/60c85a5a08dc6b6554331b22a74162f7e5b5cd28)), closes [#318](https://github.com/pedrosousa13/lnpm/issues/318)
* **pack:** match the force-exclude guards case-insensitively ([e40905e](https://github.com/pedrosousa13/lnpm/commit/e40905ea534d3ca9e66f0c97b84040d6833c69b2))
* **retreat:** validate lock-file package names before deleting the path ([#343](https://github.com/pedrosousa13/lnpm/issues/343)) ([f0ab237](https://github.com/pedrosousa13/lnpm/commit/f0ab2370d25027bdaed31bd2d6df650ff77fdb4b))
* **workspace:** fail on a broken config in the directory Detect started from ([#344](https://github.com/pedrosousa13/lnpm/issues/344)) ([e1f242c](https://github.com/pedrosousa13/lnpm/commit/e1f242c6f8cfb5e1186dfe7a26d6de3814be0bd2))

## [2.0.0](https://github.com/pedrosousa13/lnpm/compare/v1.12.0...v2.0.0) (2026-08-21)

> **Upgrading from 1.9.x or older?** Those versions compare version numbers
> byte-wise, so `lnpm update` reports `Already up to date` and will never offer
> you this release. Reinstall manually with the install script or
> `go install github.com/pedrosousa13/lnpm/cmd/lnpm@latest`. 1.10.0 and later
> upgrade normally. See [#297](https://github.com/pedrosousa13/lnpm/issues/297).
>
> This release also migrates the package database on first open and makes `gc`
> collect superseded versions, so downgrading to 1.x after running it is not
> supported.


### Features

* Add `lnpm pull` to sync linked packages from the store ([#264](https://github.com/pedrosousa13/lnpm/issues/264)) ([fc1c89f](https://github.com/pedrosousa13/lnpm/commit/fc1c89fec4b590f114c0b4bb30c748603b12def7))
* **link:** relink only the files that changed since the last link ([#295](https://github.com/pedrosousa13/lnpm/issues/295)) ([b60c58c](https://github.com/pedrosousa13/lnpm/commit/b60c58c1316f32e1b9db3b862e0a42badba02552))
* **list:** add version history and roll back with add &lt;pkg&gt;@&lt;hash&gt; ([#298](https://github.com/pedrosousa13/lnpm/issues/298)) ([53e73bc](https://github.com/pedrosousa13/lnpm/commit/53e73bc1630aa5ff4fdf2cd97ef7daa48d8f6b9e))
* Point `add --link` at the package's live source directory ([#265](https://github.com/pedrosousa13/lnpm/issues/265)) ([c0fb6e1](https://github.com/pedrosousa13/lnpm/commit/c0fb6e1eae9f3a04ac87c332f8c2e55c153ddffa))
* Resolve `workspace:` dependency specifiers when publishing ([#266](https://github.com/pedrosousa13/lnpm/issues/266)) ([718f4d7](https://github.com/pedrosousa13/lnpm/commit/718f4d74a502c8880a3b8eb1981292e23844d2ae))
* **restore:** add `lnpm restore` to re-link packages after `retreat` ([#294](https://github.com/pedrosousa13/lnpm/issues/294)) ([472f1be](https://github.com/pedrosousa13/lnpm/commit/472f1be8374175e0eff31d3ba2a98d5928262ae6))

### Bug Fixes

* Abort destructive operations in non-interactive mode instead of auto-confirming ([#223](https://github.com/pedrosousa13/lnpm/issues/223)) ([361672f](https://github.com/pedrosousa13/lnpm/commit/361672fb18ed9a58d9d23b1c63be22dd2d0bbae9))
* **add:** roll a package back when its package.json update fails in multi-add ([#244](https://github.com/pedrosousa13/lnpm/issues/244)) ([68039f3](https://github.com/pedrosousa13/lnpm/commit/68039f3fdf4fc4af7948a7ade24638456956caed))
* **add:** save the lock file before rewriting package.json ([#243](https://github.com/pedrosousa13/lnpm/issues/243)) ([4c233f1](https://github.com/pedrosousa13/lnpm/commit/4c233f16130d551c41fc6b40e46bb2c5eff57e8e))
* align build-time ldflags and surface commit and date in --version ([#281](https://github.com/pedrosousa13/lnpm/issues/281)) ([a8b72ac](https://github.com/pedrosousa13/lnpm/commit/a8b72ac99911c6a8669d1807c422b92a42cce066)), closes [#177](https://github.com/pedrosousa13/lnpm/issues/177)
* chmod linked and cloned files so umask cannot strip mode bits ([#274](https://github.com/pedrosousa13/lnpm/issues/274)) ([54c11fe](https://github.com/pedrosousa13/lnpm/commit/54c11feaf0ddea3711e6fbe0f047466e8dfcb01f)), closes [#139](https://github.com/pedrosousa13/lnpm/issues/139)
* Compare versions with a semver library instead of text ordering ([#216](https://github.com/pedrosousa13/lnpm/issues/216)) ([258156a](https://github.com/pedrosousa13/lnpm/commit/258156acde3b7c3e8f44a1fa088ef498962a2c5b))
* fail doctor when it finds issues and print its markers through the icon helpers ([#277](https://github.com/pedrosousa13/lnpm/issues/277)) ([fba8385](https://github.com/pedrosousa13/lnpm/commit/fba838514b72d7f8f78c688478b02327515960d9)), closes [#162](https://github.com/pedrosousa13/lnpm/issues/162)
* fall back to the user completion directory when the system one refuses the write ([#278](https://github.com/pedrosousa13/lnpm/issues/278)) ([c1439d8](https://github.com/pedrosousa13/lnpm/commit/c1439d8b192c36b433eaf58e39264089b3ba6a0b)), closes [#169](https://github.com/pedrosousa13/lnpm/issues/169)
* Fix nil-pointer crash in retreat when lnpm.lock is corrupt ([#221](https://github.com/pedrosousa13/lnpm/issues/221)) ([70971e2](https://github.com/pedrosousa13/lnpm/commit/70971e21f8396d068249645b9b6b5fc8784fa113))
* Fix versioned package spec being treated as a content hash in multi-package add ([#222](https://github.com/pedrosousa13/lnpm/issues/222)) ([c82ec62](https://github.com/pedrosousa13/lnpm/commit/c82ec628d3c5a07eb9b790bb664c5f8f6b621388))
* **fsutil:** call clonefile with the right signature on macOS ([#229](https://github.com/pedrosousa13/lnpm/issues/229)) ([cfbfa9f](https://github.com/pedrosousa13/lnpm/commit/cfbfa9f7d40a56883d4a8c5264d46dd08654f2f2)), closes [#135](https://github.com/pedrosousa13/lnpm/issues/135)
* **gc:** reclaim temp directories left by an interrupted publish or relink ([#289](https://github.com/pedrosousa13/lnpm/issues/289)) ([e021c81](https://github.com/pedrosousa13/lnpm/commit/e021c81a696b863065b2edad1d5e8ddb979a27dd)), closes [#233](https://github.com/pedrosousa13/lnpm/issues/233)
* give store entries a completeness marker ([#273](https://github.com/pedrosousa13/lnpm/issues/273)) ([6650434](https://github.com/pedrosousa13/lnpm/commit/665043464c9ec2a93fd44332d3be56b07ee89323)), closes [#237](https://github.com/pedrosousa13/lnpm/issues/237)
* keep GetDB's init error so every caller sees it ([#290](https://github.com/pedrosousa13/lnpm/issues/290)) ([c73954a](https://github.com/pedrosousa13/lnpm/commit/c73954a0c50610b5b5a4f3fba87dafe7baca1e47)), closes [#253](https://github.com/pedrosousa13/lnpm/issues/253)
* **link:** populate a temp directory and rename-swap instead of clearing the live package ([#232](https://github.com/pedrosousa13/lnpm/issues/232)) ([b3f0a47](https://github.com/pedrosousa13/lnpm/commit/b3f0a479f2b422d4376d3c8902136566b869bd06)), closes [#137](https://github.com/pedrosousa13/lnpm/issues/137)
* Make doctor honor the configured store_path when checking the store ([#249](https://github.com/pedrosousa13/lnpm/issues/249)) ([160ff22](https://github.com/pedrosousa13/lnpm/commit/160ff2287f1dd4fb473861be0f247b12059ca174))
* make hooks.skip_post_add actually skip the post-add hook ([#280](https://github.com/pedrosousa13/lnpm/issues/280)) ([7079f81](https://github.com/pedrosousa13/lnpm/commit/7079f81a58d34cfe1878e06bc4c6021786304e63)), closes [#171](https://github.com/pedrosousa13/lnpm/issues/171)
* Make publish --push fail when every linked-project push fails ([#248](https://github.com/pedrosousa13/lnpm/issues/248)) ([022933e](https://github.com/pedrosousa13/lnpm/commit/022933e24ba38f980b92857e8e29ae523ec70cf9))
* Normalize root-anchored and directory patterns in the files whitelist ([#228](https://github.com/pedrosousa13/lnpm/issues/228)) ([ba1ea4d](https://github.com/pedrosousa13/lnpm/commit/ba1ea4da8b48e0cd177fd5e184adb1ec42d600f9))
* Preserve package.json key order and formatting when editing dependencies ([#250](https://github.com/pedrosousa13/lnpm/issues/250)) ([bf67c59](https://github.com/pedrosousa13/lnpm/commit/bf67c594830efdfe96a45b8e913a9e99b80d2374))
* remove the updater's temp directory and match Go bin dirs by path component ([#276](https://github.com/pedrosousa13/lnpm/issues/276)) ([b29b9da](https://github.com/pedrosousa13/lnpm/commit/b29b9da8a4b87e568271aea72a87829774c53c96)), closes [#147](https://github.com/pedrosousa13/lnpm/issues/147)
* **remove:** keep the lock entry when remove fails to restore package.json ([#245](https://github.com/pedrosousa13/lnpm/issues/245)) ([c13b392](https://github.com/pedrosousa13/lnpm/commit/c13b392d24f3b6418c3e0bdf77ed768de2f0340a))
* Report a real version for go install builds so they can self-update ([#217](https://github.com/pedrosousa13/lnpm/issues/217)) ([a3e9b48](https://github.com/pedrosousa13/lnpm/commit/a3e9b482d43d31367211615e64a4f5e5ace6e544))
* report scoped packages by full name and clean up emptied scope directories ([#279](https://github.com/pedrosousa13/lnpm/issues/279)) ([ac2b4b9](https://github.com/pedrosousa13/lnpm/commit/ac2b4b99d60e2f1cbcc70453e81a9b794ae8aa89)), closes [#170](https://github.com/pedrosousa13/lnpm/issues/170) [#236](https://github.com/pedrosousa13/lnpm/issues/236)
* Rewrite isExcluded to satisfy gitignore and npm ignore semantics ([#220](https://github.com/pedrosousa13/lnpm/issues/220)) ([793e18a](https://github.com/pedrosousa13/lnpm/commit/793e18a8cf5841e9c3d8722ebb5ce76f5c05bcc6))
* Run all applicable publish lifecycle scripts, not just the first ([#251](https://github.com/pedrosousa13/lnpm/issues/251)) ([f8159ba](https://github.com/pedrosousa13/lnpm/commit/f8159babf127d19da3c06521e127e2f6d609b126))
* Stop concurrent lnpm invocations from failing with a cryptic database timeout ([#254](https://github.com/pedrosousa13/lnpm/issues/254)) ([24cd438](https://github.com/pedrosousa13/lnpm/commit/24cd438da14d7a04d9a1cf824437f7c7359c543a))
* stop hardlinking source files into the store ([#213](https://github.com/pedrosousa13/lnpm/issues/213)) ([624a3e3](https://github.com/pedrosousa13/lnpm/commit/624a3e3e857b3023984a38359e80a4b87082f291))
* stop three tests from passing without checking their subject ([#282](https://github.com/pedrosousa13/lnpm/issues/282)) ([7ef8a02](https://github.com/pedrosousa13/lnpm/commit/7ef8a025a0d32d5c06752fe1afbcf473f0f623cd)), closes [#186](https://github.com/pedrosousa13/lnpm/issues/186)
* **store:** never delete the destination before the atomic rename ([#238](https://github.com/pedrosousa13/lnpm/issues/238)) ([f6ebaf7](https://github.com/pedrosousa13/lnpm/commit/f6ebaf7b0bc0a009cbf8ab9ee220b5a7a516146d)), closes [#138](https://github.com/pedrosousa13/lnpm/issues/138)
* stream copies with io.Copy and unlink half-made reflink clones ([#275](https://github.com/pedrosousa13/lnpm/issues/275)) ([9a41948](https://github.com/pedrosousa13/lnpm/commit/9a41948855bc4726a6414f84312e3aa5c0cc5ba5)), closes [#140](https://github.com/pedrosousa13/lnpm/issues/140)
* treat a degenerate files entry as including everything ([#272](https://github.com/pedrosousa13/lnpm/issues/272)) ([a9f9a52](https://github.com/pedrosousa13/lnpm/commit/a9f9a52b5348017649b3041a842d29256aeccd6b)), closes [#227](https://github.com/pedrosousa13/lnpm/issues/227)
* **update:** report update-check failures instead of "Already up to date" ([#239](https://github.com/pedrosousa13/lnpm/issues/239)) ([400cdf1](https://github.com/pedrosousa13/lnpm/commit/400cdf1eac3621412096e606a7b88d686bc3ce48)), closes [#144](https://github.com/pedrosousa13/lnpm/issues/144)
* **update:** stage the updated binary next to the target to avoid cross-filesystem rename failures ([#240](https://github.com/pedrosousa13/lnpm/issues/240)) ([6b4e102](https://github.com/pedrosousa13/lnpm/commit/6b4e1020dba52825bbf44af2e4924bd96afd57d7))
* Verify release checksums in the install scripts before running the binary ([#218](https://github.com/pedrosousa13/lnpm/issues/218)) ([be7f7d2](https://github.com/pedrosousa13/lnpm/commit/be7f7d254c04b15cad8953a8e16cbfe47bb265ba))
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
