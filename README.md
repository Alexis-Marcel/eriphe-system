# Eriphe

Eriphe is a self-hosted personal assistant: one assistant, always on at home, that you talk to from
a web workspace, a terminal or by voice, which remembers, and which hands longer work to agents
running on your own machines.

- **Sovereign.** eriphe-core, its memory, its credentials and the tasks' files live on machines you
  own. Nothing goes to a third party except the AI provider you choose and the services you connect.
- **Any agent as the brain.** Eriphe is a system, not a model. Its brain and its workers are
  existing coding agents (Claude Code, Codex, OpenCode or any ACP agent), signed in with your own
  account or API key, and swappable without changing the rest.
- **Everything that lasts is outside the brain**, in open formats: MCP for tools, SQLite for state
  and memory, markdown for the brain's instructions.

## Status

Alpha, single user: expect rough edges, breaking changes and missing documentation. The workspace is in French by default, with an English
translation (`"lang": "en"`, or per browser in Settings); the brain's built-in instructions are in
French.

## What works today

- **Conversation** in the web workspace (`https://<address>:8443`), with attachments, and from the
  terminal (`eriphe`).
- **Memory**: people, facts, preferences and a nightly journal, kept by eriphe-core in SQLite and
  recalled by the brain; what it learns from outside content is only proposed until you approve it.
- **Tasks delegated to your machines**: the brain starts an agent in an isolated container on one of
  your machines (eriphe-node), follows it, and relays its questions and permission requests to you.
- **Tools**: mail (IMAP/SMTP), calendars (CalDAV, Microsoft Graph), Google Drive folders, folders of
  your machines, Home Assistant, GitHub and any MCP server, from a catalog in the workspace.
- **Voice** through Home Assistant's Assist pipeline (`ha/`).
- **Remote access** through Tailscale, built in, outbound only.

## Quick start

On the machine that stays on (Linux or macOS):

```sh
curl -fsSL https://eriphe.com/install.sh | sh -s -- --core-mode
```

On Windows, in an administrator PowerShell:

```powershell
& ([scriptblock]::Create((irm https://eriphe.com/install.ps1))) -CoreMode
```

The installer brings its own container runtime. It ends with the workspace's address and a setup
code; the workspace then walks you through the AI account and adding other machines. The full
guide, options and troubleshooting: [install/README.md](install/README.md).

## Architecture

Eriphe is designed like a small Kubernetes:

- **eriphe-core** ([core/](core/README.md)) is the control plane: it keeps nodes, tasks, the tool
  catalog and memory in SQLite, schedules tasks, enforces the permission policy, holds the
  credentials, serves the workspace and the API, and runs the **brain**, a long-lived agent session
  that talks with you and has MCP tools to start and steer tasks.
- **eriphe-node** ([node/](node/README.md)) is the kubelet: on every machine Eriphe may work on, it
  joins eriphe-core, then runs each task as a container from the worker image
  ([worker/](worker/README.md)) and relays its events.
- **The catalog**: tools are MCP servers, remote (with OAuth or a token) or run as local services
  on a node: [servers/mail](servers/mail/README.md), [servers/calendar](servers/calendar/README.md),
  [servers/files](servers/files/README.md), or any MCP image.
- **Clients**: the web workspace (in eriphe-core), the terminal UI ([cli/](cli/)), and the Home
  Assistant integration ([ha/](ha/README.md)).

## Security model

- **Nodes dial out.** A node needs no open port: it keeps a tunnel to eriphe-core, which speaks the
  node's API through it. Nodes join with a single-use token bound to their name.
- **TLS with per-machine certificates.** eriphe-core runs a certificate authority for its machines;
  each node gets its own client certificate (its key never leaves it) and can pin the CA's hash.
- **Tasks are isolated.** Each task is a container with a read-only root filesystem, no
  capabilities and its own secret, on a network that reaches only its node. The catalog's
  credentials stay in eriphe-core (a task calls a server through its node, only for the servers
  it was granted); an identity a task needs, such as a GitHub token, is lent to that task alone.
- **Egress relay.** A task's only way to the Internet is its node's relay, which never dials the
  LAN, loopback or metadata addresses.
- **Policy in code.** Every tool call and permission request goes through eriphe-core's policy
  (rules, user approval), not through the brain's judgement.
- **Taint.** A turn in which the brain read outside content (mail, web page, tool result) is
  tainted: it cannot approve permissions or write trusted memory on its own; those go back to you.

Details: core/README.md (Security model, Permission policy, Memory), node/README.md (Egress), and
[deploy/README.md](deploy/README.md). To report a vulnerability, see [SECURITY.md](SECURITY.md).

## AI accounts and providers' terms

Eriphe runs the official, unmodified agent CLIs (Claude Code, Codex, OpenCode). Each user signs in
with their own account or API key (for a Claude subscription, the token of `claude setup-token`),
and is responsible for using it within their provider's terms.

## Layout

| Path | What |
|---|---|
| `core/` | eriphe-core: control plane, brain, workspace, API, memory |
| `node/` | eriphe-node: runs tasks as containers on a machine |
| `worker/` | eriphe-worker: the image task containers start from |
| `install/` | `install.sh` (Linux, macOS) and `install.ps1` (Windows) |
| `cli/` | `eriphe`: terminal UI and commands |
| `servers/` | MCP servers: `mail`, `calendar`, `files` |
| `ha/` | Home Assistant conversation agent |
| `deploy/` | an example Docker Compose deployment for a NAS |

Each Go module builds on its own (`cd core && go build ./... && go test ./...`); see
[CONTRIBUTING.md](CONTRIBUTING.md).

## License

[GNU Affero General Public License v3.0](LICENSE). If you run a modified Eriphe as a network
service for others, you must offer them its source.
