# tarballs

`lnpm-test-dep-1.0.0.tgz` is a real npm tarball that `TestSymlinkSurvivesNpmInstall`
(`tests/symlink_test.go`) installs by absolute path. The test needs a genuine
`npm install` to run against the project so it can prove the lnpm symlink
survives one; installing a file path gives it that without ever contacting the
npm registry. It replaced an `is-odd` dependency that did.

It is the only binary artifact in the repository, so its source is committed
next to it: `lnpm-test-dep/` holds exactly the two files that were packed.

## Regenerating

```bash
cd tests/fixtures/tarballs/lnpm-test-dep
npm pack --pack-destination ..
```

Packed with npm 11.16.0 on node v24.18.1. The result is byte-identical to the
committed archive: `npm pack` normalises every entry's mtime to a fixed date
rather than writing the current time, so the same sources and the same npm
produce the same bytes.

Verify with:

```bash
sha256sum tests/fixtures/tarballs/lnpm-test-dep-1.0.0.tgz
# c85a354a45c440b4f22f5ff4fd281e125573fa41532acecbf801e6d67e91180e
```

Keep this README outside `lnpm-test-dep/`. Anything placed in that directory is
packed into the archive.
