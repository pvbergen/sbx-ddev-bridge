# Development

```bash
make            # vet + tests, dev builds in dist/
make release    # binaries in ddev-bridge-dist/ (VERSION=… optional)
make package    # release + dist/ddev-bridge.tar.gz, the installable package
make clean      # remove all build output
```
Needs Go 1.25 or newer (`os.Root`). The binaries are static; the macOS ones need macOS 12 or newer. Build
output (`dist/`, `ddev-bridge-dist/`) is gitignored.

Install a local build: `make package`, then
`tar -xzf dist/ddev-bridge.tar.gz -C /tmp && /tmp/ddev-bridge/install.sh`.

Run without the global install:
```bash
ddev-bridge enable --client dist/linux-amd64/ddev-bridge-client ~/projects/my-project
ddev-bridge serve             # foreground; or `ddev-bridge ensure` for the background
```

## CI and releases
- `.github/workflows/ci.yml`: on every PR and push to `main`, runs the tests and a test build of the
  package. Nothing is uploaded.
- `.github/workflows/release.yml`: when a GitHub release is published, checks out its tag, runs the tests,
  builds the package with that version, checks the binary's version and `install.sh`, and attaches
  `ddev-bridge.tar.gz` and its `.sha256` to the release.

To release: on GitHub, Releases → Draft a new release, create a tag like `v2.1.0` (`vX.Y.Z`, optionally
`-rc1`, marked as pre-release), publish. Or `gh release create v2.1.0 --generate-notes`. The tarball appears
on the release a few minutes later.
