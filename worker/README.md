# Worker images

What eriphe-node runs its tasks with: one container per task, started from a small **base**
image, into which the node **injects** the agent (Claude Code, OpenCode, Codex or Mistral Vibe). The agent then
installs what the task needs (git, gh, ImageMagick…) in its container, which is thrown away
with the task; a cache on each machine keeps what was downloaded for the next task. The toolchain
a repository names (Go, Node.js) comes from the node, ready (see below).

| Image | From | What |
|---|---|---|
| `ghcr.io/alexis-marcel/eriphe-base` | [`base/`](base/Dockerfile) | the container tasks start from |
| `ghcr.io/alexis-marcel/eriphe-agent-claude` | [`agents/claude/`](agents/claude/Dockerfile) | Claude Code's native binary |
| `ghcr.io/alexis-marcel/eriphe-agent-codex` | [`agents/codex/`](agents/codex/Dockerfile) | Codex's static (musl) binary |
| `ghcr.io/alexis-marcel/eriphe-agent-opencode` | [`agents/opencode/`](agents/opencode/Dockerfile) | OpenCode's native binary |
| `ghcr.io/alexis-marcel/eriphe-agent-vibe` | [`agents/vibe/`](agents/vibe/Dockerfile) | Mistral Vibe's ACP server (`vibe-acp`) |

Published by CI for linux/amd64 and linux/arm64 (each built on a native runner), with the same
tags as the other images: a release as `:latest`, `:0.3.0` and `:0.3`, each push to `main` as
`:edge` and `:sha-<short sha>` ([RELEASING.md](../RELEASING.md)). eriphe-node takes the base and
the agents of its own build (`:0.3.0` for a release, `:sha-<commit>` for a build of `main`).

They replace `eriphe-worker`, which had the three agents, gh, mise and git built in: 633 MB to
download on an amd64 machine (550 MB on arm64), where the base and one agent are now 241 MB
(Claude Code), 227 MB (Codex) or 192 MB (OpenCode), each downloaded only by a machine that runs
it. `eriphe-worker` is still published, as another name of `eriphe-base` (the same manifest,
under the same tags): the updaters of machines installed before pull it, and a node given `-image
…/eriphe-worker:latest` (the NAS's compose before) gets the base with its agent injected.

| Compressed, per machine | amd64 | arm64 |
|---|---|---|
| `eriphe-base` | 114.5 MB | 113.9 MB |
| `eriphe-agent-claude` | 126.7 MB | 127.4 MB |
| `eriphe-agent-codex` | 112.3 MB | 106.8 MB |
| `eriphe-agent-opencode` | 77.3 MB | 77.3 MB |
| `eriphe-agent-vibe` | about 112 MB | about 106 MB |
| `eriphe-worker` (before) | 632.6 MB | 549.5 MB |

## The base

- Debian trixie with Node.js 24 and npm (`node:24-trixie-slim`), Python 3 and
  [uv](https://docs.astral.sh/uv/) (`uv`, `uvx`), curl, ca-certificates, `LANG=C.UTF-8`
- apt as a task uses it ([`base/apt.conf`](base/apt.conf)): as root without capabilities, so
  without apt's `_apt` sandbox user; packages and lists in `/cache/apt`, kept (the Debian image's
  `docker-clean` is removed); no recommended packages; a lock held by another task of the
  machine is waited for (10 minutes)
- user `eriphe` (UID 1000), for whoever wants a non-root user; tasks run as root
- `PATH` starts with `/opt/eriphe/bin`, where the node injects the agent
- label `eriphe.runtimes=claude-code,opencode,codex,vibe,acp`; no entrypoint, command or health check
  (eriphe-node gives its own)
- no git, gh, mise or compilers: the agent installs them when a task needs them (eriphe-node's
  entrypoint installs git itself before cloning a task's repository). The Go or Node.js version a
  repository names (`go.mod`, `.nvmrc`, `engines.node`) is not baked in either: eriphe-node
  downloads it once per machine, checked, and mounts it read-only at `/opt/eriphe-toolchains`,
  first in `PATH` for the agent and the checks alike ([node/README.md](../node/README.md),
  Toolchains)
- [`eriphe-capture`](base/eriphe-capture) (`/usr/local/bin`), a web page as an image for a
  surface of the workspace (most sites refuse being framed): `eriphe-capture <url> <surface>
  [out.png]`, or `--file <image> <surface>` for a PNG or a JPEG already made. A Node.js script,
  no npm package: it drives Debian's `chromium-headless-shell` over its DevTools pipe, with a
  1280×800 window, a 45 s limit, a profile of its own thrown away after (no cookie, no sign-in:
  only the egress relay's credentials are given), downloads refused, dialogs dismissed, http(s)
  only. It posts the PNG to the node (`ERIPHE_MEDIA_URL`, the task's secret; a task lent
  `eriphe-workspace` only) and prints `{"src": "media:med_…"}`, to write as an `Image`'s `url`
  ([core/README.md](../core/README.md), Captured pages). The browser is not in the image (about
  290 MB unpacked, 103 packages): the first capture on a machine installs it with
  `fonts-liberation` through apt, and `/cache/apt` keeps the packages for the next task.

## Agents

Each agent image is `FROM scratch` and holds its publisher's program, one file
`/opt/eriphe/bin/<agent>` (Vibe: one directory), checked when the image is built:

- **Claude Code**: Anthropic's release (`downloads.claude.ai/claude-code-releases/<version>`),
  whose `manifest.json` is checked against its detached signature with Anthropic's release key
  (fingerprint `31DD DE24 DDFA B679 F42D 7BD2 BAA9 29FF 1A7E CACE`, pinned in the Dockerfile), then
  the binary against the manifest's SHA-256 for `linux-x64` or `linux-arm64` (glibc);
- **Codex**: `codex-<arch>-unknown-linux-musl.tar.gz` of OpenAI's GitHub release, checked against
  the SHA-256 pinned in the Dockerfile (GitHub's asset digest);
- **OpenCode**: `opencode-linux-x64-baseline` (it runs on any x86-64 CPU; the other build needs
  AVX2) or `opencode-linux-arm64` of its GitHub release, checked the same way. One build only:
  the npm package installed two.
- **Mistral Vibe**: `vibe-acp-linux-x86_64` or `vibe-acp-linux-aarch64` of its GitHub release,
  checked the same way. It is not one binary but a PyInstaller directory (the program, Python and
  its libraries in `_internal/`), so it sits whole in `/opt/eriphe/vibe/`, outside `bin/`: the
  node runs `/opt/eriphe/vibe/vibe-acp` by its path.

eriphe-node never runs these images. It downloads one from the registry itself, checks each layer
against its digest, unpacks its `/opt/eriphe` into `<root>/agents/<agent>/<digest>/` (files and
directories only: no link, device or setuid bit) and mounts that directory read-only at
`/opt/eriphe` in the task's container, Docker or Apple's container alike
([node/README.md](../node/README.md), Agents). The same works with any other image a task
names: the agent does not depend on the image, only on glibc (the musl builds of Claude Code and
OpenCode would serve an Alpine image).

Their auto-updaters are off (`DISABLE_AUTOUPDATER`, `OPENCODE_DISABLE_AUTOUPDATE`, `VIBE_ENABLE_AUTO_UPDATE`, Codex's update
check), as the node sets them: the binary is read-only, a new version comes as a new image. An
image of one's own for another ACP agent can be built the same way (`COPY` its binary to
`/opt/eriphe/bin/`); the `acp` runtime runs the command its agent gives, from the task's image.

## Cache

eriphe-node mounts a volume at `/cache` (`eriphe-cache`, see `-cache-volume`; a folder of its
root with Apple's container), kept between the tasks of a machine; the tasks eriphe-core marks
untrusted get another one, `eriphe-cache-untrusted` (node/README.md):

| Path | What |
|---|---|
| `/cache/apt/archives`, `/cache/apt/lists` | apt's packages and lists |
| `/cache/uv`, `/cache/uv/python` | uv's cache and the Pythons it installs (`UV_CACHE_DIR`, `UV_PYTHON_INSTALL_DIR`) |
| `/cache/npm` | npm's cache (`npm_config_cache`) |
| `/cache/pip` | pip's cache (`PIP_CACHE_DIR`) |
| `/cache/go/mod`, `/cache/go/build` | Go modules and build cache (`GOMODCACHE`, `GOCACHE`), for the Go the node gives a task or one it installs |
| `/cache/xdg` | anything else following `XDG_CACHE_HOME` |

Measured on a Mac (OrbStack's Docker, and Apple's container): `apt-get update && apt-get install
imagemagick` in a task takes 7.6 s (5.5 s with container) with an empty cache, 13.1 MB downloaded,
then 3.5 s (2.3 s) in the next task, nothing downloaded but the lists' check.

## Versions

Claude Code, Codex and OpenCode are pinned by `CLAUDE_CODE_VERSION`, `CODEX_VERSION` (with
`CODEX_SHA256_AMD64` and `_ARM64`) and `OPENCODE_VERSION` (with `OPENCODE_SHA256_AMD64` and
`_ARM64`) at the top of their Dockerfile, uv by the base's first `FROM`. To bump one, change it
there (the comments give the command that lists the new checksums) and open a pull request: CI
builds the images, and the merge publishes them.

```sh
docker build -t eriphe-base:dev worker/base
docker build -t eriphe-agent-claude:dev worker/agents/claude
```

A node takes other agent images with `-agent-image` (`{agent}` stands for `claude`, `codex` or
`opencode`), e.g. from a registry of one's own; the live tests in `node/` use a local one
(`docker run -d -p 5001:5000 registry:2`, then `docker buildx build --push -t
localhost:5001/eriphe-agent-claude:dev worker/agents/claude`).
