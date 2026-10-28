# eriphe-node

Runs Eriphe's tasks on one machine, like a kubelet: every task is a container started from a worker
image (`ghcr.io/alexis-marcel/eriphe-worker` by default, built from `worker/`), in which the agent works. eriphe-node
relays its progress, questions and permission requests to eriphe-core.

The node dials eriphe-core and keeps that connection open; eriphe-core sends its requests back
through it (a yamux tunnel over an HTTP upgrade). The node needs no open port and works behind NAT
or inside WSL.

## Join

On the machine running eriphe-core:

```sh
eriphe-core join-token -config core.json -name pc   # single use, for pc only, valid 24 h
```

Then on the new machine, once:

```sh
eriphe-node -core http://nas:8800 -join-token <token> -name pc -ca-hash sha256:<hex>
```

The node saves its own secret in `<root>/node.json`; later runs need no flag. Every flag also reads
an environment variable (`ERIPHE_CORE`, `ERIPHE_JOIN_TOKEN`, `ERIPHE_NODE_NAME`, `ERIPHE_CA_HASH`,
`ERIPHE_ROOT`).

**TLS.** `-ca-hash` is the hash of eriphe-core's certificate authority for machines (see TLS
between machines in eriphe-core's README). With it, the node reaches eriphe-core over TLS only,
`http://` addresses as `https://` on the same port, and joins only once eriphe-core's handshake
shows the CA of that hash: a machine in the middle cannot get the join token. It then asks
eriphe-core for a client certificate of its own (the key never leaves the node), presents it on
every request, and asks for a new one when a third of its life is left. Without `-ca-hash`:

- an `http://` address still joins, as before, and the node gets the CA and its certificate right
  after, then moves to TLS (it trusts the network for that first exchange, as for the join);
- an `https://` address must have a publicly trusted certificate, as eriphe-core's Tailscale one
  (`https://eriphe.<tailnet>.ts.net:8800`); eriphe-core's own is refused.

`node.json` keeps the CA (`ca`), the certificate (`cert`) and its key (`key`), next to the secret:
it stays 0600, a secret of the machine. The addresses stay as given, so an older eriphe-node still
runs with it, on plain HTTP. A node that joined before TLS needs nothing: on its next connection it
asks for its certificate with its secret and moves to TLS by itself (against an older eriphe-core,
which gives none, it stays on plain HTTP). Given `-ca-hash` then, it checks the CA it receives
against it; given a `-ca-hash` that is not the CA it already pinned, it refuses to start.
To join again under the same name (a reinstalled machine), delete `node.json` and use a rejoin token
(`eriphe-core join-token -name pc -rejoin`, or "Réinscrire cette machine" in the workspace): a plain
join token never replaces a registered node. `DELETE /v1/nodes/{name}` on eriphe-core forgets a node.

**Several addresses.** `-core` (or `ERIPHE_CORE`) takes several addresses separated by commas,
preferred first, e.g. the local one then eriphe-core's remote address over Tailscale (Settings →
Remote access on eriphe-core, with the Tailscale app on this machine):

```sh
eriphe-node -core http://192.0.2.10:8800,https://eriphe.tail1234.ts.net:8800 -join-token <token> -name laptop
```

The node connects to the first one that answers (an address that does not answer within 5 s is
skipped), uses it for everything, and, when it is on a later one, checks every 2 minutes whether
an earlier one answers again and moves back there: a laptop works at home and away. `node.json`
keeps them in `cores` (`core` stays the first one, so an older node.json still works and an older
version still reads the new one). Given again on a later run, `-core` replaces the saved
addresses, e.g. to add the remote one to a machine that already joined. The workspace's "Add a
machine" offers both addresses once remote access is ready.

## Install

- **Linux, macOS**: `install/install.sh`, one command that brings its own container runtime
  (Docker Engine on Linux; on a Mac with Apple silicon and macOS 26, Apple's container, the node on
  the Mac itself, see [The container engine](#the-container-engine-a-mac); on other Macs Lima and
  a Linux VM that sees no Mac folder, without Docker Desktop) and runs the node as below, with a
  socket proxy where there is Docker; `--core-mode` installs eriphe-core next to it. See
  `install/README.md`.

  ```sh
  curl -fsSL https://github.com/Alexis-Marcel/eriphe-system/releases/latest/download/install.sh | sh -s -- --core http://192.0.2.10:8800 --join-token <token> --name laptop
  ```

- **NAS** (or by hand on Linux): run the image with the root at the same path inside and outside, attached to the
  task network (`-task-network`, see Networks) so that tasks reach it at `-callback-url`, with Docker reached
  through a socket proxy that only allows what the node needs (`DOCKER_HOST`), never the socket
  itself: see `deploy/nas/compose.yaml` and `deploy/README.md`.
- **Windows**: `install/install.ps1` at the root of the repository (it was `node/install/install.ps1`),
  from an administrator PowerShell; see `install/README.md`. It installs WSL if needed, creates a
  WSL distribution `eriphe` (Alpine, Docker Engine, eriphe-node from its image), without Docker
  Desktop, and a scheduled task that starts it with Windows, logged in or not. Windows drives and
  programs are not visible from it. `-CoreMode` runs eriphe-core there too.

  ```powershell
  & ([scriptblock]::Create((irm https://github.com/Alexis-Marcel/eriphe-system/releases/latest/download/install.ps1))) -Core 'http://192.0.2.10:8800' -JoinToken <token> -Name pc -Labels 'gpu' -CaHash sha256:<hex>
  ```

  In this distribution eriphe-node runs as root next to dockerd, without a proxy: whoever takes
  over the node is root in the distribution. The distribution is the boundary: it sees no Windows
  drive and cannot start Windows programs. A socket proxy there would not change much, since the
  proxy cannot refuse a `--privileged` container (see `deploy/README.md`), and rootless dockerd
  is not practical in it (no systemd or cgroup delegation in WSL's Alpine, slirp4netns
  networking, GPU access through the NVIDIA runtime). The plan: run eriphe-node as UID 1000 with
  the proxy on a Unix socket, once a filter on the whole create request exists, as on the NAS.

## What the node offers

`GET /v1/node` is what the scheduler knows of the machine, and keeps while it is offline: its CPUs,
total memory (`/proc/meminfo` on Linux and WSL, `sysctl` on macOS, absent elsewhere), runtimes,
labels, the images already pulled (a task whose image is there starts sooner), and its volumes.

Labels (`-labels gpu,fast`, env `ERIPHE_NODE_LABELS`) are free words agents can require (`needs`)
or prefer (`prefers`). Volumes are named host folders the node offers to tasks:
`-volume media=/mnt/media`, repeatable, or env `ERIPHE_NODE_VOLUMES=media=/mnt/media,code=/srv/code`.
Only their names are reported, never their paths; they are not mounted into tasks yet: the user's
folders go through shares instead (see [Shares](#shares-the-users-folders)), which `shares` reports
(`[{name, writable, missing}]`).

## Updates

The node does not update itself: it has no right to pull its own image or recreate its own
container. eriphe-core's request ("update" or "rollback", with eriphe-core's own version, never an
image nor a command) comes in the assignments (`update`). The node keeps it until none of its tasks
is at work (`running` or `waiting_permission`; an idle one resumes after the restart), then writes
it in its root for the updater the installer put on the host side (install/README.md, The machines
update themselves):

| File in the root | Written by | What |
|---|---|---|
| `updater` | the installer | an updater is there (`pin=` the version it is pinned to) |
| `update-requested` | the node | `id`, `action`, `core`, `auto`, key=value lines |
| `update-status` | the updater | `id`, `action`, `outcome`, `message`, `from`, `to`, `at` |

`GET /v1/node` reports them as `update`: `{updater, pin, waiting, requested, status}`. Without
`updater` (an install from before, or a node set up by hand) the node writes nothing and
eriphe-core offers no update.

## API (through the tunnel)

| Call | What |
|---|---|
| `GET /v1/node` | OS, CPUs, `memory_bytes`, Docker version, default image, runtimes, labels (`-labels gpu`), `volumes`, `shares`, `images` (up to 50 local `repo:tag`), active tasks |
| `GET /v1/shares` | the places this machine shares folders from: `[{name, writable, missing}]` |
| `GET /v1/shares/{share}/dirs?path=P&check=1` | the directories of a folder of a place (see Shares) |
| `POST /v1/tasks` | start a task: `{runtime, prompt, image?, repo?, ref?, branch?, dir?, model?, identity?, resume?, ...}`; `runtime` is `claude-code`, `opencode`, `codex` or `acp` (with `command`) |
| `GET /v1/tasks`, `GET /v1/tasks/{id}` | state, agent session, pending permissions |
| `GET /v1/tasks/{id}/events?after=N&wait=S` | events after N, long polling |
| `POST /v1/tasks/{id}/messages` | next user turn `{text}` (when the agent is idle) |
| `POST /v1/tasks/{id}/permissions/{pid}` | answer a permission request `{allow, message?}` |
| `POST /v1/tasks/{id}/stop` | stop the agent |
| `/v1/services/{name}/mcp` | a service's streamable HTTP MCP endpoint (see Services) |

States: `running`, `waiting_permission`, `idle` (turn over: done or asking something, the
orchestrator decides), `stopped`, `failed`.

The only ports the node listens on are for its task containers, each with its own secret: the task
listener (`-listen`, 8790), where a Claude Code task calls its permission endpoint `/perm/{id}/mcp`
and its catalog servers at `/mcp/{server}`, and the egress relay (`-egress-listen`, 8791, see
Egress). It binds only the node's address on the task network: its interface there when
the node is a container on it (the NAS), the network's gateway when the node runs on the Docker
host (WSL), or else the loopback (Docker Desktop). `-listen 10.0.0.2:8790` forces an address,
`-listen 0.0.0.0:8790` the old behaviour. The relay binds the same host unless `-egress-listen`
gives one.

## Networks

Tasks run untrusted work, so they are kept apart from what they must not reach:

| Network | Who is on it | Flag |
|---|---|---|
| task network (`eriphe-tasks`, internal) | task containers; on the NAS, eriphe-node too | `-task-network` |
| service network (the NAS's `eriphe-system`, else Docker's default bridge) | services with egress; on the NAS, eriphe-core and eriphe-node | `-network` |
| internal service network | http services without egress, eriphe-node | `-service-network` |

Docker drops traffic between bridge networks, so a task cannot call a service container directly
(`eriphe-svc-<name>:<port>`, which would bypass eriphe-core's grants, policy and tickets) nor
eriphe-core. The task network is internal: a task has no route out, and reaches only the node (its
listener and its egress relay), never the LAN. The node refuses a task network that is also
`-network` or `-service-network`, and never starts an http service on the tasks' network (both on
Docker's default bridge: give `-task-network`). If the task network does not exist, the node creates
it as an internal bridge with `enable_icc=false` (tasks cannot reach one another; the node, outside
Docker, is reached on the gateway). On the NAS the compose file creates it instead, internal with an
isolated gateway (the bridge has no address on the host, so the NAS's own services are not reachable
on it), since the node is a container the tasks must reach by name. An existing task network that is
not internal is kept, with a warning at start: Docker cannot change it, remove it with no task
running and the node (or compose) creates it again.

What it does not stop: on the NAS, tasks share `eriphe-tasks` and can reach one another (nothing in
them listens; each has its own secret). Where the node runs on the Docker host (WSL), the bridge's
gateway is the host: a service of the host listening on all its addresses is reachable there
(WSL's distribution runs only the node). The listener's address keeps it off the LAN, not off the
host's other bridges: every call still needs a task's secret.

## Egress

The tasks' one way out is the egress relay, an HTTP forward proxy in eriphe-node, like the egress
gateway of a Kubernetes NetworkPolicy. Each task gets `HTTP_PROXY`/`HTTPS_PROXY` (and lowercase)
set to `http://<task id>:<task secret>@<callback host>:8791`, `NO_PROXY` for the node itself (its
listener stays direct), and `NODE_USE_ENV_PROXY=1` (Node's own `fetch` ignores the variables
otherwise). The relay:

- knows the task by its credentials (`Proxy-Authorization: Basic`), refused once the task is over;
- applies the task's rule, `egress` in its spec from eriphe-core: `{"mode": "internet"}` (the
  default, also when the spec has none: the public Internet), `{"mode": "none"}`, and `allow`, a
  list of hosts that restricts it (`github.com`, `*.npmjs.org` for any name under it);
- connects to ports 80 and 443 only (HTTPS through `CONNECT`, plain HTTP forwarded without the
  task's credentials): git over HTTPS works, git over SSH does not;
- resolves names itself and dials only public addresses: never loopback, RFC 1918, link-local
  (`169.254.169.254` included), CGNAT `100.64.0.0/10`, ULA `fc00::/7`, multicast, unspecified,
  NAT64/6to4 or other special ranges. It dials the address it checked, and checks it again on the
  socket, so DNS rebinding gains nothing;
- lets a task reach the node's own listener through it (for a client that ignores `NO_PROXY`);
- logs the first connection of a task to each host, allowed or refused:
  `task t-1: egress to github.com`.

What honors the variables (checked through the relay): curl, git, npm, Node's `fetch` (with
`NODE_USE_ENV_PROXY`), Claude Code, Codex, OpenCode (Bun), mise, uv and pip. A program that ignores
them fails closed: no route, no DNS for outside names. The brain (`system_network`) gets no proxy:
it is also on the services' network, which has egress, and calls its MCP servers directly.
`-egress-listen ""` turns the relay off (tasks then reach only the node).

Why networks and not a bearer per service: a local MCP server is a third-party image, which checks
no token the node could inject; only keeping tasks off its network protects it.

## Catalog relay

A task's assignment lists the catalog servers of its grant (`servers`), by name only. The node
puts each in the agent's MCP configuration as `<callback-url>/mcp/<server>` with the task's
secret, and relays the requests to eriphe-core (`/v1/node/tasks/{id}/mcp/{server}`, with the
node's secret and name), streaming the answers. The task's secret is dropped on the way; a task
reaches only the servers lent to it, and only while it runs. The relay uses the same plain HTTP to
eriphe-core as assignments and reports, not the tunnel: it works wherever they do, needs no new
route, and the server's credential stays in eriphe-core (the node never sees it). Catalog servers
reach every runtime the same way (see [ACP](#acp) and [Codex](#codex)); eriphe-core still lends
them to `claude-code` tasks only.

## Runtimes

A task's `runtime` is the agent program the node drives in its container (`runtime.go`). All
write the same events (`text`, `tool_use`, `permission`, `permission_answer`, `turn_end`, `state`,
`error`, `restart`), and all send every permission request to eriphe-core through the task's
inbox. `GET /v1/node` reports the runtimes of the default image, from its `eriphe.runtimes` label
(an image without it, or not pulled yet, counts as `claude-code` only).

- `claude-code` (`runtime_claude.go`): `claude -p` in stream-json mode. Permission requests go
  through an MCP tool served by the node (`approve`, on `/perm/{id}/mcp`). The session id is
  Claude Code's, resumed with `--resume`.
- `opencode` (`runtime_acp.go`, `runtime_opencode.go`): `opencode acp`, driven over the Agent
  Client Protocol (JSON-RPC 2.0 on stdin and stdout), for models reached with an API key
  (OpenAI, Google, Mistral, OpenRouter, Anthropic). The ACP client is generic; only the command
  line and the configuration are OpenCode's.
- `acp` (`runtime_acp.go`): any other ACP agent (Goose, Qwen Code…), started by the spec's
  `command`, one argument per item, run as is by the entrypoint's `exec`: never through a shell
  (a shell or `env` as the program is refused, as is a relative path or a line break). Its
  credential is its provider's, in the variable the provider names; no system prompt (ACP v1 has
  none), so eriphe-core puts the instructions before the prompt.
- `codex` (`runtime_codex.go`): `codex app-server`, OpenAI's Codex driven over its app-server
  protocol (JSON-RPC 2.0 without the `jsonrpc` member, one message per line), for a ChatGPT
  subscription. See [Codex](#codex).

The container clones `repo` if given. If the image has `mise` and the workspace has `mise.toml`,
`.mise.toml` or `.tool-versions`, the container runs `mise install` first; a failure is reported on
stderr and the task goes on. Then it runs the runtime's command. Identities (for example
`CLAUDE_CODE_OAUTH_TOKEN`, `OPENROUTER_API_KEY`, `GH_TOKEN`) are passed as environment variables,
never on a command line.

### ACP

The node is the ACP client. It declares no file system and no terminal capability: the agent
reads, writes and runs commands itself, in its container.

| ACP | Task |
|---|---|
| `initialize` (version 1), then `session/new` with `cwd` (the repository, or `/work`) and the task's MCP servers (see below) | session id saved as the task's, like Claude Code's |
| `session/load` with the saved id and the same MCP servers, if the agent has `loadSession` | after a restart; the replayed conversation is not reported again. If loading fails, a new session starts from the prompt and an `error` event says so |
| `session/prompt`, one per turn | `turn_end` then `idle` when it returns: `stopReason` `end_turn` → subtype `success`, `max_tokens` → `error_max_tokens`, `max_turn_requests` → `error_max_turns`, `refusal`, `cancelled`; a JSON-RPC error → `error_during_execution` (with `auth_error` if it is about a key). `result` is the agent's last message |
| `session/update` `agent_message_chunk` | one `text` event per message (chunks are joined until another message, a tool call, a permission or the end of the turn); with `stream`, each chunk also grows the task's draft, as Claude Code's partial messages do |
| `session/update` `tool_call` / `tool_call_update` | one `tool_use` per call, once it runs (its input is complete then) |
| `session/request_permission` | `Task.decide`, then the `allow_once` or `reject_once` option; never an `always` one. Cancelled if the agent stops meanwhile |
| stop | `session/cancel` if a turn is going on, then stdin is closed |
| any other request of the agent | JSON-RPC error `-32601` |

Tool names and inputs are those of Claude Code, so eriphe-core's policy reads them alike: a call
is named after OpenCode's tool (`bash` → `Bash`, `edit`/`patch` → `Edit`, `write` → `Write`,
`read` → `Read`, `webfetch` → `WebFetch`, `websearch`, `glob`, `grep`, `list`, `task`…), or else
its ACP kind (`execute` → `Bash`, `edit`/`delete`/`move` → `Edit`, `read` → `Read`, `search` →
`Grep`, `fetch` → `WebFetch`). The input is the agent's raw input plus `command`, `file_path` or
`url` (from the raw input, the call's location or its title).

The MCP servers are the ones Claude Code gets (`runtime_mcp.go`): the spec's `mcp_servers` (the
brain's `eriphe-core`; a `${NAME}` in a URL or header is read from the task's identity) and the
catalog servers at `<callback>/mcp/<name>` with the task's secret as bearer, but for the node's
`eriphe_node` (`approve`), which only Claude Code's `--permission-prompt-tool` needs. They go as
ACP's `McpServerHttp` (`{"type":"http","name","url","headers":[{"name","value"}]}`), so only to
an agent that announces `mcpCapabilities.http`: otherwise the task fails. An MCP tool that
OpenCode names `<server>_<tool>` is reported and asked for as `mcp__<server>__<tool>`; OpenCode
asks for one without its input, which is taken from the tool call that runs it.

ACP carries no reason with a denial: the agent learns that the call was refused, and the
reasons of the turn's denials are put before its next prompt (lost if the node restarts
meanwhile).

If the agent reports which model its session runs (a `model` config option) and it is not the
task's, the task fails: OpenCode falls back on another model when the one asked for is not
available.

### OpenCode

The node configures it through `OPENCODE_CONFIG_CONTENT`: the task's `model` (`provider/model`,
for example `openrouter/moonshotai/kimi-k2`), `autoupdate` off, `share` disabled, `ask` for
`edit`, `bash` and `webfetch`, `deny` for `websearch` (a third party's search, off for now; reading
files needs no permission, as with Claude Code), `experimental.continue_loop_on_deny` (a denial does not end the turn, as with Claude Code)
and the workspace's `AGENTS.md` and `CLAUDE.md` as instructions. With a model of another provider
than OpenCode's own, OpenCode's free models are disabled, so the code never goes to them instead.

`OPENCODE_DISABLE_PROJECT_CONFIG=1`: a repository's `opencode.json` is ignored, since it could add
plugins or MCP servers that run without asking, or send the key elsewhere. Sessions live in
`/profile` (`XDG_DATA_HOME=/profile/data`, `XDG_STATE_HOME=/profile/state`), so a restarted task
loads its own. The key comes from the agent's provider, in the variable OpenCode reads.

OpenCode takes no system prompt over ACP: `append_system_prompt` is passed by name as
`ERIPHE_INSTRUCTIONS` to the command, which writes it to `/tmp/eriphe/instructions.md` (the tmpfs),
unsets the variable and starts `opencode acp`; the file is one of the config's `instructions`.
OpenCode would run MCP tools without asking: the config asks for every tool of the task's servers
(`<server>_*`) but the `allowed_tools` (`mcp__<server>` → `<server>_*`, `mcp__<server>__<tool>`
→ `<server>_<tool>`: `allow`), and gives MCP calls an hour (`experimental.mcp_timeout`), as Claude
Code's `MCP_TOOL_TIMEOUT`. With `no_agent_memory` (the brain), the workspace's `AGENTS.md` and
`CLAUDE.md` are left out and `OPENCODE_DISABLE_CLAUDE_CODE=1` turns off `~/.claude/CLAUDE.md` and
Claude Code's skills. OpenCode still attaches an `AGENTS.md` it finds next to a file it reads.

### Codex

Codex, OpenAI's coding agent, signed in with ChatGPT: the way a ChatGPT Plus or Pro plan is
used. The node drives `codex app-server` directly rather than through `codex-acp` (the ACP
adapter): the adapter's modes either rely on Codex's own sandbox (bubblewrap, which cannot run in
a task container without privileges) or never ask. The task container is the sandbox, so every
thread runs with sandbox `danger-full-access` and approval policy `untrusted`: Codex asks before
every command and every file change, and each request goes to `Task.decide`, as with the other
runtimes.

| App-server | Task |
|---|---|
| `initialize`, `initialized`, then `thread/start` with `cwd`, `model`, `approvalPolicy: "untrusted"`, `sandbox: "danger-full-access"`, `developerInstructions` (the task's `append_system_prompt`) and `config` (its MCP servers, see below) | thread id saved as the task's session; a thread running another model than the task's fails it |
| `thread/resume` with the saved id and the same parameters (today's prompt and servers) | after a restart; if it fails, a new thread starts from the prompt and an `error` event says so |
| `turn/start`, one per turn; `turn/completed` ends it | `turn_end` then `idle`: `completed` → `success`, `interrupted` → `cancelled`, `failed` → `error_during_execution` (with `auth_error` if it is about the sign-in). `result` is the agent's last message |
| `item/completed` of an `agentMessage` | one `text` event |
| `item/agentMessage/delta` | with `stream`, grows the task's draft |
| `item/started` of a `commandExecution`, `fileChange`, `mcpToolCall`, `webSearch` | `tool_use` (`Bash`, one `Edit` per file, `mcp__<server>__<tool>`, `WebSearch`) |
| `item/commandExecution/requestApproval` | `Task.decide("Bash", {command})`, then `accept` or `decline`, never for the session |
| `item/fileChange/requestApproval` | `Task.decide("Edit", {file_path})` for each file of the change (from its `item/started`); `decline` at the first denial |
| `mcpServer/elicitation/request` with `_meta.codex_approval_kind: "mcp_tool_call"` | `Task.decide("mcp__<server>__<tool>", arguments)` (the tool from the call's `item/started`), then `accept` or `decline`; any other elicitation is declined |
| stop | `turn/interrupt` if a turn is going on, then stdin is closed |
| any other request (more permissions, questions, client tools) | JSON-RPC error `-32601` |

The thread's `config` sets `mcp_servers` to the task's servers, the same as ACP's (each with `url`
and `http_headers`, or `command`, `args` and `env`, `tool_timeout_sec` 3600), replacing any of
Codex's own. Each asks for every tool (`default_tools_approval_mode: "prompt"`) but the
`allowed_tools` (`approve`, for the server or under `tools.<tool>.approval_mode`). Codex sends
those approvals as MCP elicitations (`features.tool_call_mcp_elicitation=true`, its default, is
set anyway). With `no_agent_memory`, `project_doc_max_bytes` 0: no `AGENTS.md` of the workspace.
`web_search` is always `cached` (OpenAI's index, never `live`, which full access would default to:
live pages would be opened from OpenAI's servers, out of eriphe-core's sight).

Codex runs every command as `/bin/bash -lc '<command>'`: the policy is given the command inside,
so its rules (which look for the start of a command) read it as they read Claude Code's. Denial
reasons are put before the next prompt, as with ACP.

The credential is an `auth.json` of a ChatGPT sign-in that eriphe-core lends as
`ERIPHE_CODEX_AUTH` (see `core/README.md`): a fresh access token, no refresh token. The command
writes it to `/tmp/eriphe-codex/auth.json` (the tmpfs, mode 600), links it from
`CODEX_HOME=/profile/codex/auth.json`, takes the variable out of the environment and starts the
app-server. `CODEX_HOME` is in `/profile`, so a restarted task resumes its thread; the token itself
never reaches the node's disk. Codex is started with `cli_auth_credentials_store="file"`, no
update check, no analytics or feedback.

`ERIPHE_LIVE_IMAGE=eriphe-worker:dev go test -run CodexLive -v` runs the real Codex of an image,
hardened as every task, against a fake OpenAI Responses API on the machine (no ChatGPT plan
needed; `ERIPHE_LIVE_HOST=host.orb.internal` with OrbStack): an allowed command, a denied one,
a restart that resumes the thread, and the auth.json left off the disk.

`ERIPHE_LIVE_IMAGE=eriphe-worker:dev go test -run Live -v` runs the real OpenCode of an image on
one of its free models (`ERIPHE_LIVE_MODEL`, default `opencode/big-pickle`; no account needed):
two turns with an allowed command and a denied write, a restart and a loaded session.

Every task container mounts the Docker volume `eriphe-cache` at `/cache` (`-cache-volume`, empty to
disable): the worker image keeps its tools and package caches there, so they are downloaded once
per node.

## Task containers

Every task container is started with:

- `--user 1000:1000`, the worker image's user, whatever the image says: a task never runs as
  root, and the task's folders, which the node gives to UID 1000, stay writable. An image built
  for another UID, or that needs root at run time (`apt-get`), does not fit.
- `--cap-drop ALL`, `--security-opt no-new-privileges`, `--pids-limit 1024`.
- `--read-only`, with a tmpfs at `/tmp` (2 GB, exec allowed for builds and test binaries; it counts
  towards the memory limit). If `HOME` cannot be written (the worker image's `/home/claude` is on
  the read-only root), the entrypoint moves it to `/tmp/home`. The writable places are `/tmp`,
  `/work` (task folder), `/profile` (agent config) and `/cache` (volume).
- `--memory` from `-task-memory` (default `8g`, empty for none) and `--cpus` from `-task-cpus`
  (default empty: all the machine's CPUs).
- no host mount but the task's `work` and `agent` folders (or its profile), refused unless they
  are under `-root`, and the user's folders lent to it (`folders`, see Shares), checked again at
  each start; never the Docker socket.

## The container engine (a Mac)

`-engine container` (env `ERIPHE_ENGINE=container`, what install.sh sets on a Mac with Apple
silicon and macOS 26) runs each task with [Apple's container](https://github.com/apple/container)
(1.6.1 or later) instead of Docker: eriphe-node runs on macOS itself, as the user, and every task
is a micro-VM of its own (its own Linux kernel, started in about 0.75 s), with the same contract as
a Docker task (`apple.go`). `-docker` is then the `container` CLI (default `container`; the
LaunchAgent gives `/usr/local/bin/container`).

| Docker task | `container run` |
|---|---|
| `-i --rm --name eriphe-task-<id> --label eriphe.task=<id>` | the same, and `--progress none` |
| `--user 1000:1000`, `--cap-drop ALL`, `--read-only` | the same |
| `--tmpfs /tmp:rw,exec,nosuid,nodev,size=2g` | the same |
| `--pids-limit 1024` | `--ulimit nproc=1024` (per user in the VM, the task's only user) |
| `--security-opt no-new-privileges` | none in container: with every capability dropped (the bounding set too), a setuid program gains none |
| `--memory`, `--cpus` | `--memory`, `--cpus`; without `-task-cpus`, all the Mac's CPUs (container's own default is 4 CPUs and 1 GB) |
| `-v <dir>:/work`, `/profile` | `--mount type=bind,source=<dir>,target=/work` (no comma in a path) |
| `-v eriphe-cache:/cache` | `<root>/cache` mounted at `/cache`: a container volume belongs to root, the task's user could not write to it |
| `--network eriphe-tasks`, `--add-host host.docker.internal:<gateway>` | `--network eriphe-task-<id>`, its own (below); the callback is its gateway's address |
| `-e NAME`, `--entrypoint sh <image> -c …` | the same (`-e NAME` takes the value from the CLI's environment) |
| `docker stop -t 10`, `docker rm -f` of strays | `container stop -t 10`, `container delete --force` of what carries `eriphe.task`, and the strays' networks |
| `--group-add` of a folder's group, chown to UID 1000 | none: the Mac's file sharing shows every file of a mount as the task's user's, and what the task writes lands as the Mac user's |

`GET /v1/node` says `"engine": "container"` and `engine_version` (`docker` is empty); the images
are container's (`container image ls`), the runtimes the worker image's label.

**Networks.** Each task gets a network of its own, `container network create --internal --label
eriphe.task=<id> eriphe-task-<id>` (vmnet's host-only mode), deleted with the task: no route out,
no DNS, and no other task on it (container has no `enable_icc`; a network per task is its
equivalent, made in about 0.1 s). The Mac is its gateway, where the node's task listener and egress
relay answer, so the agent's configuration and `HTTP_PROXY` name the gateway's address. A
host-only network's bridge exists only while a container is on it: the listener and the relay bind
every address (`0.0.0.0:8790`, `:8791`) and close at once any connection that comes neither from a
task's network nor from the loopback (`taskListener`): the LAN never reaches them. The brain
(`system_network`) stays on its own network too: it reaches eriphe-core through the node (no
`self_url`), as every task. An http service without egress gets an internal network of its own as
well (the node reaches it on its address; it does not reach the listener); with egress, a service
is on container's default network (NAT), like Docker's default bridge.

Checked on a Mac (`TestAppleLive`, below): a task reaches the listener and the relay; through the
relay, the Internet and not the LAN; directly, neither the Internet, nor DNS, nor the LAN (the
router), nor another task's address. This does not depend on macOS's "find devices on local
networks" permission of `container-runtime-linux`: allowed, tasks still have no route to the LAN.

What it does not stop: **a service of the Mac that listens on all its addresses is reachable from a
task**, on the gateway or any address of the Mac (eriphe-core's 8800 and 8443 in core mode, file
sharing, a development server, AirPlay…), as on WSL (see Networks). The node's own listener
refuses what is not a task. Keeping the Mac's own services out would need a packet filter rule
(pf) on the tasks' subnets, which needs root and is not done.

**macOS's privacy, and the explorer.** Documents, the Desktop, Downloads, iCloud Drive and the
clouds' folders are protected: a program reads them once the user allows it, the permission tied to
the program's signature. eriphe-node is not signed with a Developer ID: each update would ask again
(and a program that launchd starts waits for the answer). So the node never reads a shared place
itself (`apple_explore.go`):

- the listing (`GET /v1/shares/{share}/dirs`) and the checks run in a short-lived container, the
  explorer: alpine, the place's root mounted read-only, no network, UID 1000, every capability
  dropped, about 0.7 s. Paths are resolved there, so a symlink out of the root points nowhere it can
  see (`outside`). Then the checks of Shares apply to what it reports: nothing hidden or sensitive on
  the way, nor in the folder three levels down (`holds_sensitive`), not the node's own data
  (`~/.eriphe`: hidden, and a folder holding it, the home, is listed but not shareable). The
  places' paths are not even looked at when the node starts (`missing` is never set);
- a task's folders are checked the same way when it starts, and `Eriphe/` is made by the explorer,
  the folder mounted writable;
- `~/Library` is never listed nor shared from a root that holds it (mail, messages, browsers' and
  apps' data): iCloud Drive and `~/Library/CloudStorage/*` inside it are roots of their own.

macOS asks once, for Apple's `container-runtime-linux`, the first time a protected folder is listed
or mounted, and keeps the answer across Eriphe's updates. The first listing may wait for that answer
past eriphe-core's timeout; the next one works.

**Files in the cloud.** iCloud Drive and the File Provider clouds keep files "dataless" on a Mac
that optimises its storage. A task cannot have them downloaded (a read in the container fails with
EDEADLK); a read on the Mac does. Before a task starts, the node has the dataless files of its
folders downloaded through Apple's own tools (`find -flags +dataless`, `stat`, `head -c 1`, which
macOS lets into a protected folder where it would hold eriphe-node back), up to `-download-limit`
(2 GB) for the task; beyond, the task fails and says to choose "Keep Downloaded" on the folder in
the Finder.

**The LAN, and the core relay.** macOS's local network privacy ([TN3179](https://developer.apple.com/documentation/technotes/tn3179-understanding-local-network-privacy))
keeps a LaunchAgent off the LAN ("no route to host", checked: the loopback, the Internet and
container's networks are not held back). It would ask only for a program it can identify by its
signature, and an ad-hoc signed eriphe-node is another program after each update; the question
also needs a bundle's `NSLocalNetworkUsageDescription`. What is allowed without asking is a
program run as root. Checked on a Mac: a LaunchDaemon run as the user (`UserName`) is held back
and asked about like an agent; one run as root, a binary never allowed before, reaches the LAN
with no question. eriphe-node itself cannot be a daemon: Apple's container has its services in
the user's session (the `gui/<uid>` launchd domain), and from a daemon its CLI gets "XPC
connection error: Connection invalid". So a root LaunchDaemon, `org.eriphe.relay` (install.sh,
not in core mode), does only the connections to eriphe-core (`corerelay.go`,
`corerelay_unix.go`): `/usr/local/libexec/eriphe-relay core-relay -socket
/var/run/eriphe/relay.sock -dir ~/.eriphe -owner <uid>`, from a copy of eriphe-node that only
root can change (the node's own binary is the user's: root never runs it).

- The node, given `ERIPHE_CORE_RELAY=<sock>`, sends `host:port` and a line break, then the
  connection's bytes once the relay answers `ok`; TLS stays end to end, checked against the
  pinned CA. The loopback is dialed directly, and so is everything when no relay answers.
- The socket is in a folder of root's that no one else writes to, made 0600 from its creation,
  then given to the user (`lchown`): only the user's programs reach the relay.
- The relay dials only eriphe-core's addresses, read at each connection from the user's
  `node/node.json` (`core`, `cores`, as JSON) and `node.env` (`ERIPHE_CORE='url,url'`, for the
  join), as root reads a user's files: no symlink followed (`O_NOFOLLOW`, `node/` and the folder
  checked with `lstat`), regular files owned by `-owner`, at most 64 KB, only http(s) URLs with a
  host and a port, nothing else in them used, nothing of them logged. It takes nothing else from
  the socket than that one line (at most 300 bytes, within 10 s), and refuses anything not in the
  list: it is no way out for anything but the node's tunnel, assignments, reports and catalog
  relay.
- An update of eriphe-node does not change the relay (it needs root); running install.sh again
  does (`sudo`), when the binaries differ. Its log is `/var/log/eriphe-relay.log`.

`ERIPHE_LIVE_CONTAINER=1 ERIPHE_LIVE_LAN=<router>:80 go test -run AppleLive -v` runs real task
containers on a Mac with container: hardening, the network checks above, and folders
(`ERIPHE_LIVE_FOLDERS=a,b` of the home, the first read-only; `ERIPHE_LIVE_ICLOUD=<folder>` of iCloud
Drive, `brctl evict` a file there first to see it downloaded); `ERIPHE_LIVE_IMAGE` for another image
than alpine (the worker's).

## Shares (the user's folders)

The places of this machine whose folders the user may give to Eriphe are declared on the node,
never by eriphe-core: `-share name=/path`, repeatable, or `-share name=/path:ro` for a place whose
folders may only be read; env `ERIPHE_NODE_SHARES=home=/home/sam,media=/mnt/media:ro`. The path
must be the same for the node and for the Docker daemon (as `-root` without `-host-root`): in a
container, mount it at the same path (`install.sh --share` does, see install/README.md), and let
the socket proxy bind-mount from it (`-allowbindmountfrom`). A place that is not there (an unplugged
disk) is reported `missing` and logged at start. eriphe-core knows a place by its name and a folder
by its path relative to the place, never the host path.

Every path is checked when it is used, not only when it was shared:

- relative only: an absolute path, an empty, `.` or `..` part, or a control character is refused
  (`invalid_path`);
- symlinks are resolved (the place's own too) and the result must stay in the place (`outside`); a
  folder is known by its resolved path;
- never a hidden or sensitive directory on the way (`sensitive`): any name starting with a dot, and
  `sensitiveNames` in `shares.go` without case: keys and credentials (`.ssh`, `.gnupg`,
  `.password-store`, `.aws`, `.azure`, `.kube`, `.docker`, `Keychains`, `credentials`,
  `secrets`…), settings and app state where tokens live (`.config`, `.local`, browser and mail
  profiles, `.claude`, `.codex`), a NAS's `appdata`; and key or credential files (`.netrc`,
  `.git-credentials`, `.pgpass`, `id_ed25519`…);
- never the node's own data (`-root`: its secret, the tasks' folders), nor a folder holding it
  (`eriphe_data`);
- a folder can be shared, and mounted, only if no sensitive name is in it or in its subfolders down
  to three levels (`holds_sensitive`, at most 20,000 directories searched): a whole home is refused,
  its `Documents` is not.

`GET /v1/shares/{share}/dirs?path=P` lists the real directories of a folder (no symlink, nothing
hidden or sensitive, sorted, at most 1,000), and how many files it has, never their names nor
their content: `{share, path, dirs, files, truncated, writable}`, `path` being the resolved one.
With `check=1`, `shareable` (and `code`, `reason` when not). A refusal is `{code, error}` with 400,
403, 404 or 503.

A task's spec lists its folders: `folders: [{name, share, path, write}]`. At each start the node
resolves and checks each again (a folder swapped for a symlink out of its place since fails the
task), then gives the container:

- `--mount type=bind,src=<folder>,dst=/work/folders/<name>,readonly`;
- with `write`, `--mount type=bind,src=<folder>/Eriphe,dst=/work/folders/<name>/Eriphe`, refused on
  a `:ro` place. The node creates `Eriphe/` if it is missing: owned by UID 1000 in the folder's
  group, mode 2775 (setgid: what is written there keeps the group). One already there must be a real
  directory (a symlink named `Eriphe` is refused) that UID 1000 can write to;
- `--group-add` of the folder's group and of `Eriphe/`'s (not root's), so the task reads what that
  group may, and `umask 002` in the container: what it writes is group-writable. On a NAS whose
  shares are `nobody:users`, the results are `1000:users`, mode 664, and every account in `users`
  can open, change and delete them;
- `ERIPHE_FOLDERS` with the folders' names.

A folder's content is visible to a task whole: the node does not hide a file inside it. What it
does not cover: a sensitive name deeper than three levels, a sensitive file under another name, and
what changes in the folder between the check and the mount.

**For other hosts** (the contract the Mac and Windows installers build on): declare the places with
`-share` (or `ERIPHE_NODE_SHARES`) at paths the node and its Docker daemon both see, the same path
for both; the node does the listing, the checks and the mounts. On a Mac with Apple's container
the node runs on the Mac and its places are Mac paths; the listing and the checks go through the
explorer, see [The container engine](#the-container-engine-a-mac). A Mac in Lima's VM shares no
folder. On Windows,
the WSL distribution reaches a drive as `/mnt/<letter>/…` once mounted (automount is off today): the
node there gets `-share <name>=/mnt/c/Users/<user>/Documents`, with no proxy in the way. Writes
through such a mount follow its own owner mapping (virtiofs, 9p, DrvFs), not the setgid rule above.

## Services (local MCP servers)

eriphe-core assigns MCP servers to the node next to its tasks (`services` in the assignments, see
`core/README.md`). The node keeps each one's container running (`service.go`) and serves it as a
streamable HTTP MCP endpoint through the tunnel, `/v1/services/{name}/mcp`, which only eriphe-core
reaches (`service_mcp.go`). `GET /v1/node` says `"services": true` when Docker answers.

- A service container is `docker run --rm` (`-i` for stdio), named `eriphe-svc-<name>`, labelled
  `eriphe.service=<name>`. Task containers carry `eriphe.task` instead: at start the node removes
  the stray containers of each label separately, and neither cleanup touches the other's.
- It restarts when it exits or never comes up (5 minutes to pull its image and answer `initialize`,
  or answer HTTP), after a backoff from 1 s doubled up to 5 minutes, back to 1 s once a container
  has run for 2 minutes; there is no maximum. It is reported `failed` with stderr's tail meanwhile.
- A new spec, or a new value of its environment (a rotated secret), restarts it once the old
  container is gone. Unassigned, it is stopped (`docker stop`); its folder stays.
- Hardened like a task: `--user` (the service's, `1000:1000` by default, never root),
  `--cap-drop ALL`, `--security-opt no-new-privileges`, `--pids-limit 256`, `--read-only` unless
  `writable`, a tmpfs at `/tmp` (256 MB), `--memory` from `-service-memory` (default `1g`). Its
  only host mount is `/data` from `<root>/services/<name>` if it has a `volume`, given to its
  user and refused outside `-root`. Its environment is passed by name (`-e NAME`), never on a
  command line. `HOME` is the image's: on a read-only root, set `HOME` (to `/tmp` or `/data`) in
  `env` if the server writes there.
- Network: with egress, the node's `-network` (the NAS's `eriphe-system`) or Docker's default
  bridge, never the task network (see Networks). Without egress, `--network none` for stdio; an http server must still be reached, so it
  goes on `-service-network`, an internal Docker network the node is on (declare it in compose with
  `internal: true` and attach the node), and fails without one.

### stdio: the bridge

The node is the only client of the server's process, over the container's stdin and stdout
(go-sdk `IOTransport`), and serves its tools again as its own MCP server (with the server's name
and instructions), so several sessions of eriphe-core share one process. Caveats: the process sees
one client, so whatever state it keeps between calls (a selected mailbox, a login, a working
directory) is shared by every session; only tools are relayed, not resources, prompts, its
notifications (progress, logs) or its requests (sampling, elicitation). The bridge outlives the
container: during a restart a call fails ("restarting"), and when it is back the same sessions go
on, its tools listed again (`tools/list_changed` if they changed). Sessions idle for an hour, and
all of them when the node restarts, are dropped: the client opens a new one.

### http: the proxy

The node proxies the endpoint to the container's `path`, with a `localhost` Host header (servers
with DNS rebinding protection accept it) and server-sent events flushed as they come. It reaches
the container by name on a network it shares (the NAS: `eriphe-svc-<name>:<port>` on
`eriphe-system`), or else on a loopback port it picks and publishes (`-p 127.0.0.1:<port>:<port>`:
WSL, a Mac).

On the NAS this needs no Docker call beyond those the socket proxy allows already (create with
its options, attach, start, wait, stop, delete, ps), and the volume is under the node's root,
where bind mounts are allowed.

`ERIPHE_LIVE_SERVICE_IMAGE=mcp/sequentialthinking go test -run LiveService -v` runs a real stdio
server image through the reconciler and the bridge with the local Docker (no network, read-only,
UID 1000, a volume) and lists its tools through the node's endpoint.
