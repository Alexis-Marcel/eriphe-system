# eriphe-worker

The image eriphe-node runs its tasks in: one container per task, in which the agent (Claude Code,
OpenCode or Codex) works. eriphe-node gives it its own entrypoint (clone, tools, agent), so the image has no
entrypoint, command or health check of its own. Its `eriphe.runtimes` label
(`claude-code,opencode,codex,acp`) tells the node which agents it has; `acp` is any other ACP agent,
run by the command its agent gives (installed in this image, or in the agent's own image).

Published by CI on every push to `main` as `ghcr.io/alexis-marcel/eriphe-worker:latest` and
`:sha-<short sha>`, for linux/amd64 and linux/arm64 (each built on a native runner).

## Inside

- Debian trixie with Node.js 24 (`node:24-trixie-slim`)
- Claude Code (`@anthropic-ai/claude-code`), with its auto-updater off
- OpenCode (`opencode-ai`), for models reached with an API key, driven over ACP (`opencode acp`),
  with its auto-updater off (`OPENCODE_DISABLE_AUTOUPDATE`); eriphe-node configures it for each
  task (see `node/README.md`). Its downloads (models list, language servers) go to
  `/cache/xdg/opencode`; its sessions to the task's `/profile`
- Codex (`@openai/codex`), for a ChatGPT subscription, driven through `codex app-server`; its npm
  package carries Codex's static (musl) binary for the platform. eriphe-node gives it its
  credential and configuration for each task (see `node/README.md`); its threads go to the
  task's `/profile/codex`
- `gh` and `mise`, from their GitHub releases, checked against the published checksums
- git, curl, ca-certificates, ripgrep, jq, less, procps, openssh-client
- user `claude` (UID 1000), working directory `/work`

eriphe-node runs it as UID 1000 with a read-only root filesystem and no capabilities (see
`node/README.md`): a task writes to `/work`, `/profile`, `/cache` or `/tmp`, and `HOME` becomes
`/tmp/home`.

## Cache

`/cache` belongs to `claude`, so a named volume mounted there (eriphe-node mounts `eriphe-cache`,
see `-cache-volume`) starts with the same owner. Everything a task downloads goes there and is
kept between tasks on the same node:

| Path | What |
|---|---|
| `/cache/mise` | tools installed by mise (`MISE_DATA_DIR`); `/cache/mise/shims` comes first in `PATH` |
| `/cache/mise-cache` | mise's downloads (`MISE_CACHE_DIR`) |
| `/cache/go/mod`, `/cache/go/build` | Go modules and build cache (`GOMODCACHE`, `GOCACHE`) |
| `/cache/npm` | npm cache (`npm_config_cache`) |
| `/cache/pip` | pip cache (`PIP_CACHE_DIR`) |
| `/cache/xdg` | anything else following `XDG_CACHE_HOME` |

When a workspace has `mise.toml`, `.mise.toml` or `.tool-versions`, eriphe-node's entrypoint runs
`mise install` before starting the agent; the second task with the same tools starts instantly.

## Versions

Claude Code, OpenCode, Codex, gh and mise are pinned by the `CLAUDE_CODE_VERSION`, `OPENCODE_VERSION`,
`CODEX_VERSION`, `GH_VERSION` and `MISE_VERSION` build arguments at the top of the `Dockerfile`. To bump one, change its default there and open a
pull request: CI builds the image, and the merge publishes it.

```sh
docker build -t eriphe-worker:dev worker
```
