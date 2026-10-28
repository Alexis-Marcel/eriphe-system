# Releasing

A release is a Git tag `vX.Y.Z` on `main` ([semver](https://semver.org): while Eriphe is `0.x`, the
minor number changes for anything that breaks, the patch for fixes). CI does the rest.

## Cut a release

```sh
git switch main && git pull --ff-only
git tag -a v0.2.0 -m "Eriphe 0.2.0"
git push origin v0.2.0
```

The tag must be on `main`, CI must be green on that commit, and the version must be newer than the
last release. A prerelease is `v0.3.0-rc.1`.

## What CI does

On the tag push, `.github/workflows/ci.yml` runs every check, then builds every image for amd64 and
arm64 (`image.yml`) with the version `0.2.0` (the binaries' `-X main.version`, and the label
`org.opencontainers.image.version`), and publishes it under:

| Pushed | Version | Image tags |
|---|---|---|
| tag `v0.2.0` | `0.2.0` | `0.2.0`, `0.2`, `latest` |
| tag `v0.3.0-rc.1` | `0.3.0-rc.1` | `0.3.0-rc.1` (never `latest`) |
| a commit to `main` | `0.2.0-dev+<short commit>` | `edge`, `sha-<short commit>` (never `latest`) |
| a pull request | `0.2.0-dev+<short commit>` | built, not published |

Once every image is published, the `release` job creates the GitHub release, with notes generated
from the pull requests since the last one (with the workflow's own token; edit them on GitHub
afterwards if needed). A release that exists already is left as it is, so a failed run can be
re-run.

## What follows

- **Machines.** Within six hours, eriphe-core reads the new `latest` and the workspace says "New
  version available" on every machine running an older release; those set to update at night do
  it then (core/README.md, Versions; install/README.md, The machines update themselves). A build
  of `main` (`edge`) is never told to update.
- **eriphe.com.** The portal copies the install scripts from `eriphe-core:latest`
  (`/usr/share/eriphe/install/`): it serves those of the latest release once it is rebuilt on the
  new image. Changes to `install/` on `main` reach eriphe.com only with a release.
- **Pins.** `--version 0.2.0` stays there; `--version 0.2` takes `0.2.x` fixes.

## Something went wrong

- **A check fails on the tag.** Nothing is published. Fix it on `main`, then move to a new patch
  version (`v0.2.1`) rather than moving the tag.
- **A bad release is out.** Publish a fix as the next patch version. Machines that updated to it
  and failed their health check went back by themselves; others go back with "Go back to the
  previous version" (or `--rollback`). `latest` can also be pointed back to the previous release by
  hand: `docker buildx imagetools create -t ghcr.io/alexis-marcel/<image>:latest
  ghcr.io/alexis-marcel/<image>:0.1.0` for each image.
