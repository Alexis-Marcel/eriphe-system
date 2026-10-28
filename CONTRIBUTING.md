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
| `worker/`, and the images | `docker build -t eriphe-worker:dev worker` (`core/` builds with `--build-context install=install`) |
| `ha/` | `python3 -m pytest ha/tests`; the Home Assistant tests are in [ha/README.md](ha/README.md) |

Go code is formatted with `gofmt`. Tests do not need network access or real accounts: providers and
servers are faked.

## Releases

Merging to `main` publishes the images under `edge` only; users get a change with the next release,
a `vX.Y.Z` tag cut by the maintainers ([RELEASING.md](RELEASING.md)). Note what users will notice in
the pull request's title and description: the release notes are generated from them.

## Pull requests

- One topic per pull request, with tests for what changes and the README of the component updated
  when its behaviour, options or API change.
- Commit messages: short, in the present tense, saying what the change does ("Let a node rejoin
  under its own name").
- Never commit credentials, tokens, personal configuration (`core.json`, `google-oauth.json`) or
  data.

## License

Eriphe is licensed under the [GNU AGPL v3.0](LICENSE). By contributing, you agree that your
contribution is licensed under the same terms.
