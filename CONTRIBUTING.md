# Contributing

Issues and pull requests are welcome. For anything larger than a fix, open an issue first so the
design can be discussed before the code. Security problems go through [SECURITY.md](SECURITY.md),
not public issues.

## Build and test

Each component is its own Go module; run the checks from its folder. CI (`.github/workflows/ci.yml`)
runs the same.

| Module | Checks |
|---|---|
| `core/`, `cli/`, `servers/mail/`, `servers/calendar/`, `servers/files/` | `go build ./... && go vet ./... && go test ./...` |
| `node/` | the same, plus `GOOS=windows go build ./...` |
| `install/` | `sh install/lint.sh` (needs `shellcheck`) |
| `.github/scripts/` (releases) | `sh .github/scripts/release_test.sh` |
| `worker/`, and the images | `docker build -t eriphe-base:dev worker/base`, `docker build -t eriphe-agent-claude:dev worker/agents/claude` (the same for `core/`, `node/` and the servers) |
| `ha/` | `python3 -m pytest ha/tests`; the Home Assistant tests are in [ha/README.md](ha/README.md) |

Go code is formatted with `gofmt`. Tests do not need network access or real accounts: providers and
servers are faked.

## Pull request titles and releases

Pull requests are squash-merged: the title becomes the commit on `main`, and CI releases from those
commits ([RELEASING.md](RELEASING.md)). Write it as a
[Conventional Commit](https://www.conventionalcommits.org/en/v1.0.0/) subject,
`type(scope)!: description` (scope and `!` optional), saying what users will notice, in the present
tense; a check fails on a title that does not follow it.

| Type | For | Merging it |
|---|---|---|
| `feat` | something new users can do | releases a minor version (`0.2.0` → `0.3.0`) |
| `fix`, `perf` | a bug fixed, something faster | releases a patch version (`0.2.0` → `0.2.1`) |
| `docs`, `chore`, `ci`, `test`, `refactor`, `style`, `build` | anything else | releases nothing by itself; it ships with the next release |
| any type with `!`, or a `BREAKING CHANGE: ...` footer in the description | a change that breaks a setup, an API or a configuration | releases a major version (a minor before `1.0.0`) |

```text
feat(node): let a node rejoin under its own name
fix(core): keep the session on a reload
docs: explain the backups
feat!: drop the v1 API
```

The release notes group the titles by type (Breaking changes, Features, Fixes, Other). The pull
request's check tells which release merging it makes. A pull request of a single commit is merged
under that commit's subject, which must then follow the convention too.

## Pull requests

- One topic per pull request, with tests for what changes and the README of the component updated
  when its behaviour, options or API change.
- Commit messages in a branch: short, in the present tense, saying what the change does; the
  Conventional Commits form is welcome but only the pull request's title counts.
- Never commit credentials, tokens, personal configuration (`core.json`, a Google
  `client_secret_*.json`) or data.

## License

Eriphe is licensed under the [GNU AGPL v3.0](LICENSE). By contributing, you agree that your
contribution is licensed under the same terms.
