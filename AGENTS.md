# AGENTS.md

Instructions for AI coding assistants (Codex, Cursor, Gemini CLI, Claude Code and others) working
with this repository.

## Installing Eriphe for someone

If a person asked you to install Eriphe (not to change its code), stop here and follow
[docs/install-with-an-ai.md](docs/install-with-an-ai.md) step by step. It says what to ask them,
which commands to run on each system, what they must do themselves, and the safety rules you must
keep (never ask for a password, token or key in the chat; never disable antivirus, firewall or
SIP; never open the workspace to the internet yourself: on a cloud server, only the safe path it
describes). You do not need to build anything: the installers download released images.

## Contributing to the code

### Layout

| Path | What |
|---|---|
| `core/` | eriphe-core (Go): control plane, brain, web workspace (`web*.go`, `web/templates`, `web/static`), API, memory |
| `node/` | eriphe-node (Go): runs tasks as containers on a machine; `node/cred/` is a module of its own |
| `worker/` | the task images: `worker/base` (`eriphe-base`) and one image per agent in `worker/agents/` |
| `install/` | `install.sh` (Linux, macOS), `install.ps1` (Windows), `eriphe-launcher` (the one-container install) |
| `unraid/` | the Unraid template of the launcher |
| `cli/` | `eriphe`, the terminal UI (Go) |
| `servers/` | MCP servers `mail`, `calendar`, `files` (Go, one module each) |
| `ha/` | the Home Assistant integration (Python) |
| `deploy/` | an example Docker Compose deployment |
| `docs/` | guides for users |
| `.github/` | CI, image builds, release scripts ([RELEASING.md](RELEASING.md)) |

Each component's README is its reference: read it before changing the component, and update it
when its behaviour, options or API change.

### Build and test

Each Go module builds on its own; run the checks from its folder (CI runs the same):

| Module | Checks |
|---|---|
| `core/`, `cli/`, `servers/mail/`, `servers/calendar/`, `servers/files/`, `node/cred/` | `go build ./... && go vet ./... && go test ./...` |
| `node/` | the same, plus `GOOS=windows go build ./...` |
| `install/` | `sh install/lint.sh` (needs `shellcheck`) and `sh install/launcher_test.sh` |
| `.github/scripts/` | `sh .github/scripts/release_test.sh` |
| `ha/` | `python3 -m pytest ha/tests` |
| images | `docker build -t eriphe-base:dev worker/base`, `docker build -t eriphe-agent-claude:dev worker/agents/claude`, and the same for `core/`, `node/` and the servers |

Go code is formatted with `gofmt` (`gofmt -l .` prints nothing). Tests need no network and no real
account: providers and servers are faked; tests that need a real machine or account are named
`*_live_test.go` and skip unless their `ERIPHE_LIVE_*` variables are set.

### Conventions

- **Pull request titles drive releases.** PRs are squash-merged and the title becomes the commit
  on `main`: write it as a [Conventional Commit](https://www.conventionalcommits.org/en/v1.0.0/)
  (`feat(node): …`, `fix(core): …`, `docs: …`, `!` for a breaking change). `feat` releases a minor
  version, `fix`/`perf` a patch, the others nothing. Details: [CONTRIBUTING.md](CONTRIBUTING.md).
- **English everywhere**: code, comments, commits, docs, logs and errors.
- **UI texts in French and English.** The workspace is French by default with an English
  translation: every string goes in both tables (`core/web_text*.go`). The brain's built-in
  instructions (`core/brain-prompt.md`) are in French.
- **No personal data**: no real names, addresses, e-mails or hostnames in code, tests, docs or
  commits. Use `example.org`, `192.0.2.x` (documentation addresses) and names like "Sam".
- **No credentials**: never commit tokens, keys, `core.json`, a Google `client_secret_*.json` or
  data.
- **Commits**: short, present tense, saying what the change does. No `Co-Authored-By` trailer and
  no mention of an AI tool in commits or PRs.
- **Tests with the change**: a fix or a feature comes with its test; keep `go vet` and `gofmt`
  clean, and `install/lint.sh` when you touch the scripts.
- **One topic per pull request**; open an issue first for anything larger than a fix. Security
  problems go through [SECURITY.md](SECURITY.md).

### Safety while developing

- **Do not run an eriphe-node on a machine that already runs one** (yours, for a real Eriphe): two
  nodes share the same container runtime, networks and data paths. Test with the fakes, or in a
  throwaway VM.
- Do not point tests or scripts at a real eriphe-core, NAS or account.
- Do not run the installers on your own machine to test a change unless that is the machine you
  mean to install; `install/launcher_test.sh` and `install/lint.sh` cover the scripts.
