# Releasing

A release is a Git tag `vX.Y.Z` on `main` ([semver](https://semver.org)). CI cuts it by itself
from the commits merged since the last release, which follow
[Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/); a tag pushed by hand
still works, to choose the version.

## What releases

Pull requests are squash-merged, so each one is a single commit on `main` whose subject is the
pull request's title: `type(scope)!: description`, the scope and the `!` optional. On every push to
`main`, CI reads the commits since the last `vX.Y.Z` tag:

| Commit | Release |
|---|---|
| `feat: let a node rejoin under its own name` | minor: `0.1.1` → `0.2.0`, `1.4.2` → `1.5.0` |
| `fix(core): keep the session on a reload`, `perf: cache the token` | patch: `0.1.0` → `0.1.1` |
| `feat!: drop the v1 API`, or any type with a `BREAKING CHANGE: ...` footer in the body | major: `1.4.2` → `2.0.0`; before `1.0.0`, minor: `0.1.1` → `0.2.0` |
| `docs:`, `chore:`, `ci:`, `test:`, `refactor:`, `style:`, `build:`, or a subject that does not follow the convention | nothing by itself; it ships with the next release |

The highest wins: a `feat` and three `fix` since `0.1.0` release `0.2.0`. When none of them
releases, `main` is only published under `edge`, as for any push.

While Eriphe is `0.x`, a breaking change bumps the minor, not the major (the semver convention for
`0.x`: anything may change), so `1.0.0` is a decision, cut by hand (below). To release a minor
rather than a patch, title the pull request `feat:`.

A pull request's title is checked (`.github/workflows/pr-title.yml`), which also tells which
release merging it makes. The rules are `.github/scripts/release.sh`, tested by
`sh .github/scripts/release_test.sh`; see what `main` would release with:

```sh
git fetch --tags
last=$(git describe --tags --abbrev=0 --match 'v[0-9]*.[0-9]*.[0-9]*' --exclude '*-*' origin/main)
.github/scripts/release.sh log "$last" origin/main | .github/scripts/release.sh next "${last#v}"
.github/scripts/release.sh log "$last" origin/main | .github/scripts/release.sh notes
```

## What CI does

`.github/workflows/ci.yml` runs every check, then builds every image for amd64 and arm64
(`image.yml`) with the build's version (the binaries' `-X main.version`, and the label
`org.opencontainers.image.version`), and publishes it under:

| Pushed | Version | Image tags |
|---|---|---|
| a commit to `main` that releases `0.2.0` | `0.2.0` | `0.2.0`, `0.2`, `latest` |
| a commit to `main` that releases nothing | `0.2.0-dev+<short commit>` | `edge`, `sha-<short commit>` (never `latest`) |
| tag `v0.2.0`, by hand | `0.2.0` | `0.2.0`, `0.2`, `latest` |
| tag `v0.3.0-rc.1`, by hand | `0.3.0-rc.1` | `0.3.0-rc.1` (never `latest`) |
| a pull request | `0.2.0-dev+<short commit>` | built, not published |

A release from `main` happens in that push's run: the `version` job computes the next version, the
images are published under it, and only then does the `release` job create the annotated tag
`v0.2.0` on the commit and the GitHub release, its notes grouped from the commits (Breaking
changes, Features, Fixes, Other). It uses the workflow's own token, so the tag starts no other run.

- **One at a time.** The runs of `main` are queued (`concurrency`), so the next push reads the tag
  of the previous release: two pushes never release the same version.
- **Nothing half done.** A failed check or image build stops the run before the tag: nothing is
  released, and the next push (or a re-run) tries again.
- **Re-runs.** The tag and the GitHub release are each skipped when they exist already; a commit
  that has its tag is released again under it (the same images), so re-running a failed run
  completes it. A computed version whose tag is on another commit is not released (a warning).

## Release by hand

To choose the version (`1.0.0`, a prerelease, or a release of `docs:` changes), tag `main`:

```sh
git switch main && git pull --ff-only
git tag -a v1.0.0 -m "Eriphe 1.0.0"
git push origin v1.0.0
```

The tag must be on `main`, CI must be green on that commit, and the version must be newer than the
last release. A prerelease is `v1.1.0-rc.1`. The tag's run publishes the images and creates the
GitHub release the same way; the next releases count from it.

## What follows

- **Machines.** Within six hours, eriphe-core reads the new `latest` and the workspace says "New
  version available" on every machine running an older release; those set to update at night do
  it then (core/README.md, Versions; install/README.md, The machines update themselves). A build
  of `main` (`edge`) is never told to update.
- **Install scripts.** The release job attaches `install/install.sh` and `install/install.ps1`, as
  they are on the released commit, to the GitHub release, so
  `https://github.com/Alexis-Marcel/eriphe-system/releases/latest/download/install.sh` (and
  `install.ps1`) serves those of the latest release, and `releases/download/vX.Y.Z/install.sh`
  those of one release. Changes to `install/` on `main` reach users only with a release.
  (`eriphe.com/install.sh` only redirects there.)
- **Pins.** `--version 0.2.0` stays there; `--version 0.2` takes `0.2.x` fixes.

## Something went wrong

- **A check fails on a release.** Nothing is tagged or published under the release's tags. Re-run
  it if the failure was passing (a registry down), or merge the fix (`fix:`): its run releases the
  same version, which was never tagged. Never move a tag that exists.
- **A bad release is out.** Merge the fix (`fix:`): it is the next patch version. Machines that
  updated to it and failed their health check went back by themselves; others go back with "Go
  back to the previous version" (or `--rollback`). `latest` can also be pointed back to the
  previous release by hand: `docker buildx imagetools create -t
  ghcr.io/alexis-marcel/<image>:latest ghcr.io/alexis-marcel/<image>:0.1.0` for each image.
