# eriphe-core

Eriphe's control plane (v0): it keeps the state of devices and tasks in SQLite, schedules tasks on
devices, follows them, lends credentials, and gives the brain its tools over MCP. One binary, with
k8s-like modules: `store`, `api`/`mcp`, `scheduler`, controllers.

```sh
go build -o eriphe-core .
eriphe-core -data ./data         # no file: defaults, see First start
eriphe-core -config core.json    # a configuration file, as below
```

## First start

eriphe-core needs no configuration file. Without `-config` (or when the file it names does not
exist), it runs on defaults with everything in its data directory, `-data` (env `ERIPHE_DATA`,
`/data` by default: the image's volume):

```sh
docker run -d --name eriphe-core -v eriphe-data:/data -p 0.0.0.0:8800:8800 -p 0.0.0.0:8443:8443 ghcr.io/alexis-marcel/eriphe-core
docker exec eriphe-core eriphe-core auth setup-code    # the setup code, while there is no password
```

The ports are published on IPv4 only: published on `[::]` too (Docker's default) without IPv6 on
the container's network, Docker's userland proxy relays IPv6 clients, and eriphe-core sees them all
with the bridge's gateway address.

At first start it makes what it lacks in the data directory, and reuses it after: the API token
(`core-token`), the vault key (`vault.key`), the database (`core.db`, `memory.db`), the graph's
nodes (`nodes/`, `nodes-trace/`, `nodes.db`), the memory's readable mirror (`memoire/`) and the
workspace's self-signed certificate (`web-cert.pem`, `web-key.pem`). An existing `core.db` that
lacks a column added by hand (`convertedColumns` in `store.go`: `messages.taint`,
`access_asks.settled`) is refused at start, the error naming the pull request whose SQL adds it:
eriphe-core never reads a missing taint as none. The API listens on
`0.0.0.0:8800`, the workspace on `https://0.0.0.0:8443` (`ERIPHE_API_LISTEN` and `ERIPHE_WEB_LISTEN`,
e.g. `0.0.0.0:9443`, move them, as `install.sh --api-port`, `--web-port` does on a Mac), and the
brain is on, with no agent yet.
The image runs as UID 1993: a host folder mounted at `/data` must be writable by it (`chown
1993:1993 <dir>`), a named volume is. `TZ` sets the time zone (`Europe/Paris` otherwise).

Then, in the workspace: the setup code of the log (`web: no password yet: open https://…/setup
with the setup code ABCD-EFGH`) creates the owner's password, and a short wizard names the
assistant (the user chooses its name) and connects the AI account it thinks with
(a Claude subscription's `claude setup-token`, or any other kind of Settings → Agents, with its
usual agents). The brain starts as soon as a machine has joined.
Nothing else is on by default: no tool, no policy rule, no access; the catalog's tools (Home
Assistant, GitHub, mail…) are added from Settings → Tools.

An installer gets a join token for the machine's own device without the workspace:

```sh
docker exec eriphe-core eriphe-core join-token -name pc -quiet   # prints the token alone
```

A configuration file is read as it always was: what it sets wins, and what it leaves out stays
off (no `web`, no workspace; no `brain`, no brain). `brain.prompt_file` and `brain.self_url` are
optional (see [The brain](#the-brain)).

## Configuration file

```json
{
  "listen": "0.0.0.0:8800",
  "public_url": "http://192.0.2.10:8800",
  "token_file": "core-token",
  "db": "core.db",
  "user": "Sam",
  "providers": {
    "anthropic":  {"kind": "claude-subscription", "secret": "/secrets/claude-oauth-token"},
    "openrouter": {"kind": "openrouter-api", "secret": "/secrets/openrouter-api-key"}
  },
  "agents": {
    "opus":   {"runtime": "claude-code", "provider": "anthropic", "model": "opus",
               "use": "hard code, design, long tasks", "prefers": ["cpu", "memory"]},
    "sonnet": {"runtime": "claude-code", "provider": "anthropic", "model": "sonnet", "use": "default"},
    "haiku":  {"runtime": "claude-code", "provider": "anthropic", "model": "haiku", "use": "small quick tasks"},
    "kimi":   {"runtime": "opencode", "provider": "openrouter", "model": "openrouter/moonshotai/kimi-k2",
               "use": "a second opinion, or code for a model other than Claude"}
  },
  "default_agent": "sonnet",
  "accesses": {
    "npm": {"use": "publish our packages", "secret": "vault:npm-token", "hosts": ["registry.npmjs.org"],
            "env": "NPM_TOKEN", "value": "approval"}
  },
  "servers": {
    "github": {"url": "https://api.githubcopilot.com/mcp/", "auth": {"bearer_file": "/secrets/github-mcp-token"},
               "use": "GitHub: issues, pull requests, code search", "access": "reads"},
    "home":   {"url": "http://192.0.2.20:8123/api/mcp", "auth": {"bearer_file": "/secrets/home-assistant-token"},
               "use": "the house: lights, heating, sensors"},
    "notion": {"url": "https://mcp.notion.com/mcp", "auth": {"oauth": {}}, "use": "notes and docs in Notion",
               "access": "read-only"}
  },
  "brain": {"agent": "opus", "prompt_file": "/etc/eriphe/brain-prompt.md", "self_url": "http://eriphe-core:8800/mcp",
            "tools": ["github.get_*", "github.list_*", "github.search_*", "notion"]}
}
```

`timezone` is the user's IANA time zone (`Europe/Paris` when empty): eriphe-core shows every time
in it, tells the brain the current date and time in it at each turn, and gives it to tasks as `TZ`.

## Providers and agents

Two levels, like a Kubernetes RuntimeClass. A **provider** is where models are and the credential
to reach them; its kind says which runtimes can use it and in which variable its secret is lent:

| Kind | Runtimes | Variable |
|---|---|---|
| `claude-subscription` | `claude-code` | `CLAUDE_CODE_OAUTH_TOKEN` |
| `chatgpt-subscription` | `codex` | `ERIPHE_CODEX_AUTH` (an `auth.json`, see [ChatGPT subscription](#chatgpt-subscription)) |
| `mistral-subscription` | `vibe`, `opencode` | `MISTRAL_API_KEY` (a key made in Vibe's settings: a Mistral plan's monthly usage is shared by Vibe, Le Chat and the API) |
| `anthropic-api` | `claude-code`, `opencode` | `ANTHROPIC_API_KEY` |
| `openai-api` | `opencode` | `OPENAI_API_KEY` |
| `google-api` | `opencode` | `GOOGLE_GENERATIVE_AI_API_KEY` |
| `mistral-api` | `opencode`, `vibe` | `MISTRAL_API_KEY` |
| `openrouter-api` | `opencode` | `OPENROUTER_API_KEY` |
| `openai-compatible` | `opencode` | `SELFHOSTED_API_KEY`, optional (see [Self-hosted model server](#self-hosted-model-server)) |
| `custom-api` | `acp` | the provider's own `env` (for example `DASHSCOPE_API_KEY`); not a system variable |

The `opencode` runtime (OpenCode driven over ACP, see `device/README.md`) reaches any of these with
an API key; its `model` is OpenCode's `provider/model`, and the provider must be the agent's
(`openai/…` for `openai-api`, `google/…`, `mistral/…`, `openrouter/…`, `anthropic/…`), which the
configuration checks. `vibe` is Mistral Vibe, Mistral's own program, driven over ACP too; its
`model` is one of Vibe's aliases (`mistral-medium-3.5`), or none for Vibe's default. `acp` is any other ACP agent (Goose, Qwen Code…): the agent gives its `command`, one argument per item (`["goose", "acp"]`), run as is in the worker
image or the agent's `image`, never through a shell; it takes no `model`. An
**agent** is what the brain chooses for a task: a runtime, a provider, a model, an optional image, the
device labels it needs (`needs`, for example `gpu`), what it would rather have (`prefers`: `cpu`, `memory`,
or any device label), and `use`, which tells the brain when to pick it.
The brain is an agent too (`brain.agent`, or Settings → Agents when the file names none): changing
it restarts the brain once its turn is over, on its session if the harness is the same, or on a
fresh one with the context pack. Its runtime must take MCP servers and a system prompt. The provider's credential is lent to each task.

**Credentials.** A credential (FR « identifiant ») is what a real program in a task uses with a
secret: a package registry, an API with a key. It is declared in Settings → Tools (Credentials,
`/settings/credentials`) or under `accesses`, and lent
as a resource, `{"kind": "access", "ref": "npm"}`; `list_tools` shows them to the brain. The file,
the API and the store still call them accesses; the word "access" in the workspace is for the ways
into Eriphe (Settings → Access).

| Field | What |
|---|---|
| `use` | what it is for, for the brain and the user |
| `secret` | a file, or `vault:<name>`; from the workspace, typed once, straight to the vault as `access.<name>.secret`, and never shown back |
| `hosts` | the exact names it reaches, with a port if not 443; public addresses only |
| `env` | the variable holding the placeholder (`NPM_TOKEN`); not one a task depends on (`PATH`, `GIT_*`, `ERIPHE_*`…), nor its agent's own key |
| `value` | `free`, or `approval` (the default) |
| `header` | the one header the secret goes in; `Authorization` by default, Bearer and Basic alike |
| `rules` | if any, the only requests the secret goes on: `GET /v1/*`, `* /exact`. A path that may climb on the service, or is still encoded, is refused, and a method override header dropped: a limit against mistakes, the real one being the token's rights |

The task never holds the secret. Its variable holds a placeholder; the device's relay decrypts the
access's hosts only ([device/README](../device/README.md#accesses)) and hands each request to
`/v1/device/tasks/{id}/access`, where eriphe-core puts the secret in that header and goes on to the
host the task connected to, whatever its request says. A placeholder sent anywhere else, or in
another header, stays a placeholder; a request that carries none goes as it is. An access has no
grid, since Eriphe sees the use of the secret and not what the program does with it. `approval` is
asked on the card of a task's start, once for the task, by the user only; `free` holds on a clean
start, and asks on any other, as a tool's Free writes do. An access lent with `hold` does nothing
until its release. Each use, and each refusal, is in the `access_log`, shown in the task's
details. It ends with its task.

What this does not do:

- A service that reflects its headers hands the secret back. Response headers are scrubbed, as a
  raw value or as Basic; bodies, trailers and 1xx answers are not: inject into the one header, and
  prefer short-lived tokens. The page says so.
- The device sees an access's requests, though not its secret. A client that pins its certificates
  fails on that access, and so does gRPC (HTTP/1.1 only), and a presigned URL cannot be made from a
  placeholder.
- Each use is one row of `access_log`, written before the request goes, and the table is not
  pruned: a large install through an access makes many rows, and the task's details show the latest.
- It stops a secret from being stolen, not from being used: the real limit is set at the provider.
- What a task reads through an access is external content, and does not taint the task: Eriphe does
  not read a program's traffic, as it does not read its shell output. The page says so too.

**SSH credentials.** A credential of kind `ssh` is a machine a task configures with the real `ssh`, and
so `scp`, `rsync` or Ansible (`core/lent_ssh.go`). Settings → Tools, "Add an SSH machine" (also a tile of "Add a tool"), asks for
the machine and the account; in the file, `kind: "ssh"`, one entry in `hosts` (port 22 by default),
`user`, `secret` (a private key file) and `host_key`.

- **The key.** Saving makes an Ed25519 pair: the private key goes straight to the vault as
  `access.<name>.secret` and is never shown; the page shows the public line for the machine's
  `authorized_keys`. One key per access: removing that line, or the access, ends it. Saving the
  access again keeps the key.
- **The host key.** The page's test connects once from eriphe-core, which confirms the key is in
  place, and records the machine's host key. A machine only a device reaches cannot be tested: its
  key can be pasted (`ssh-keyscan`). An access with no host key cannot be lent. A key that changed
  fails the test; emptying the field and testing again records the new one.
- **The path.** `eriphe-cred` is the task's SSH agent, and asks `/v1/device/tasks/{id}/ssh`
  (`{op: keys | sign | connect}`) through its device. eriphe-core signs a login only (RFC 4252's
  `publickey` request, or OpenSSH's host-bound one, whose host key must be the pinned one, for that
  key and the access's `user`): the key signs no commit or file and logs in to no other account. The session leaves from eriphe-core, as an HTTP access's requests
  do: the relay upgrades `GET /v1/device/tasks/{id}/ssh/connect?target=host:port` (`eriphe-ssh`), and
  eriphe-core decides and dials that exact address, carrying SSH's encrypted bytes. So a device need
  not reach the machine. A machine eriphe-core cannot reach answers 502, and the device dials it
  itself. Each connection, login
  and refusal goes in `access_log` (tool `access`). A task lent an SSH access no longer gets the
  built-in ask on `ssh`, `scp`, `sftp` and `rsync`: the access was decided, and reaches that machine only.
- **What it does not do.** SSH lends everything the remote account can do: Eriphe sees the logins,
  not the commands, and `sudo` is the machine's to set. Use a dedicated account. The host key is
  pinned in the task's own `ssh` configuration, which a task can change: it protects a task from
  another machine, not a machine from the task.

**Harness cards.** What each runtime can do, as the device drives it, is one entry of `harnesses`
(`harnesses.go`): MCP servers, how it takes a system prompt (a flag, developer instructions, an
instructions file, or prepended to the prompt), streaming, a PDF or an image in a tool result, its
own web search and fetch, resume, and whether it chooses a model when none is named. The core asks
the card, never the runtime's name. The models offered for a provider carry what they read and do
(`vision`, `pdf`, `tools`, `context`, from models.dev); a model typed by hand gets its provider's
`Unlisted` assumption. A new runtime is a card plus its name in the providers it can use.

A Claude Pro or Max plan reaches Claude Code only (Anthropic refuses it in other programs); its
credential is the token of `claude setup-token`, valid about a year. A ChatGPT Plus or Pro plan
reaches Codex, OpenAI's agent. Google's Gemini plan no longer reaches these programs: Gemini needs
an API key. API keys (Anthropic, OpenAI, Google, Mistral, OpenRouter) are billed by use.

### Self-hosted model server

A provider of kind `openai-compatible` ("Modèle auto-hébergé" in the workspace) is a model server
the user runs on one of their machines (Ollama, llama.cpp's `llama-server`, vLLM, LM Studio),
reached at its OpenAI-compatible `/v1` address and used like any other model, for tasks and the
brain, through OpenCode:

```json
"providers": {"ollama": {"kind": "openai-compatible", "base_url": "http://192.0.2.10:11434/v1",
                         "models": ["qwen3:8b", "llama3.1:8b"]}},
"agents": {"qwen": {"runtime": "opencode", "provider": "ollama", "model": "selfhosted/qwen3:8b",
                    "use": "private documents, nothing leaves the house"}}
```

- `base_url` is `http(s)://host[:port]/v1`, without credentials, query or fragment. Tasks run in
  containers, so the host is the machine's address on the network (or a public name), never
  `localhost`, a loopback, link-local (`169.254.169.254`), multicast or unspecified address. The
  server must listen on the network (Ollama: `OLLAMA_HOST=0.0.0.0`). The workspace's form refuses
  plain `http://` to a public IP address: prompts would cross the internet in clear and the model
  server would answer anyone. Put such a server behind `https://` with a key, or reach it over
  Tailscale (its `100.x` address); `http://` is for the local network.
- `secret` (the key) is optional: Ollama has none; vLLM (`--api-key`) or LM Studio may. It is lent
  as `SELFHOSTED_API_KEY` and OpenCode reads it from the environment (`{env:…}`), never from its
  config. A key with a quote, a backslash, a brace or a control character is refused.
- `models` are the server's model names, one word each (`qwen3:8b`, `org/model`): the agent form
  offers them, and the workspace creates one agent per model when the account is added (the first
  the default when there is none). An agent's `model` is `selfhosted/<model>`.
- When a device fetches a task of such an agent, eriphe-core adds `model_endpoint` (`base_url`, and
  `key_env` with a key) to its spec and that one `host:port` to a copy of its egress rule
  (`endpoints`): the device's relay lets the task reach that server, even on the LAN or another
  port, and nothing else of the LAN (`device/README.md`, Egress).
- Nothing is assumed of the models but tool calling. Pick one made for it (Qwen 3, Llama 3.1+,
  Mistral…), and give it enough context: Ollama's default is small, and its documentation
  recommends 16k to 32k tokens for tool calling (`OLLAMA_CONTEXT_LENGTH=32768`, or `num_ctx` in a
  Modelfile).
- The brain and a task may ask the same server at once: with Ollama, set `OLLAMA_NUM_PARALLEL=2`
  (or more) so that one does not wait, and time out, behind the other, and
  `OLLAMA_MAX_LOADED_MODELS=1`. Each slot holds a whole context: on a graphics card with 8 GB, use
  16k (`OLLAMA_CONTEXT_LENGTH=16384`), or 32k with `OLLAMA_FLASH_ATTENTION=1` and
  `OLLAMA_KV_CACHE_TYPE=q8_0`, which halve the context's memory. What does not fit on the card
  runs from the computer's memory, far slower: 16 GB of RAM is tight for one 7B model with 2 slots
  at 32k. Details and a rule of thumb: `install/README.md`, Ollama's settings.

The account form asks the server for its models (`POST /settings/provider-models`, `web_models.go`):
`GET <base_url>/models` (with `Authorization: Bearer` when a key is typed or stored), not following
redirects, as soon as a device is chosen or with "Détecter les modèles" for an address. They come
as boxes to tick (the first ticked is the default agent of a new workspace), one agent created per
model added; a save with none ticked lists them instead. A name can be typed by hand only when the
server cannot be asked. On an edit, the models the server serves that the account does not use yet
are offered as new.

#### A model behind a device

The server can also stay on its machine, listening on that machine only, a home PC or a rented
server, with no port opened and no account elsewhere: eriphe-device runs there with
`-model-server http://127.0.0.1:11434` (the installers' `--model-server`, `-ModelServer` on
Windows), and the provider names that device in place of an address:

```json
"providers": {"gpu": {"kind": "openai-compatible", "device": "gpu-box", "models": ["qwen3:8b"]}}
```

In the workspace: Add an AI account → Modèle auto-hébergé → "Sur un de mes appareils", then the
device (the ones whose device reports `model_server`; Settings → Devices tags them "Prête un
modèle"). `device` and `base_url` exclude each other.

The address is the one the device reaches (`install/README.md`, A model behind this machine): on
Linux the device is a container, so the server is a container next to it on `eriphe-system`
(`http://eriphe-ollama:11434`); on a Mac with Apple's container, `127.0.0.1`; on Windows, the device
runs in WSL, which reaches Windows' `127.0.0.1` only with mirrored networking (`networkingMode=mirrored`
in the `[wsl2]` section of `%USERPROFILE%\.wslconfig`, Windows 11 22H2 or later, then
`wsl --shutdown`, which applies to every distribution, and the install again). Ollama for Windows
runs only in its user's session, while the device runs without anyone logged in: put it in the
startup programs (Win+R, `shell:startup`). The installers check at the end that the server answers
the device.

The path (`model_relay.go`):

```
task (any device, the brain too) → its device:   <callback>/model/v1/{path}   (the task's secret)
its device → eriphe-core:                      /v1/device/tasks/{id}/model/{path}  (device secret, mTLS)
eriphe-core → the lending device:              /v1/model/{path}   (that device's tunnel)
the lending device → the server:               <model-server>/v1/{path}
```

- eriphe-core relays only for a task that runs on the calling device, is not finished, and whose
  agent (OpenCode) uses a provider with `device`; only `GET` and `POST`, only paths under `/v1`
  (`..`, even escaped, is refused here and again by the lending device, so Ollama's own `/api`,
  with pull and delete, stays out of reach); bodies up to 64 MiB. The task's and the device's
  secrets stop here: the server gets the provider's key (`secret`), if any, which is never lent
  to a task.
- The task's spec carries `model_endpoint: {"relay": true}`, no address and no key; its egress
  rule is unchanged (its device is reached directly, as for its catalog servers).
- Answers stream through as they come (`FlushInterval: -1` at every hop): a chat completion's
  server-sent events arrive token by token.
- The lending machine must be connected: otherwise the task's request fails with `502` ("the
  machine of the model … is not connected"). The form's list of models goes through the same
  tunnel (`GET /v1/model/models`).

### ChatGPT subscription

eriphe-core owns the ChatGPT sign-in, so that tasks never hold its refresh token (single use: two
holders refreshing it would sign each other out):

- **Sign-in.** Settings → Agents → Add an AI account → ChatGPT subscription starts OpenAI's
  device code flow, the one `codex login --device-auth` uses (Codex's public client, issuer
  `auth.openai.com`): the page shows a code and a link, the user types the code at OpenAI (device
  code sign-in must be allowed in ChatGPT's security settings), eriphe-core polls for up to 15
  minutes and exchanges the answer (PKCE) for tokens. One sign-in per workspace session at a time.
- **Kept and refreshed here.** The tokens are the provider's credential, in the vault
  (`provider.<name>.key`; a configuration file's ChatGPT provider must point at the vault too).
  They are refreshed, one refresh at a time, when the access token has less than 2 hours left or
  is a week old; the new refresh token is written to the vault before the new access token is
  used. A refusal (the sign-in was revoked) fails the lend with "sign in again"; a passing failure
  lends the current token while it is still valid.
- **Lent to each task.** When a device fetches a task, eriphe-core lends an `auth.json` with the
  access token, the ID token and the account, **no refresh token**, and `last_refresh` set to
  now, so Codex does not refresh it (see `device/README.md` for where the device puts it). A task
  that outlives its access token stops with an authentication error; a restart lends a fresh one.
- Tokens are never logged (the log names a sign-in by the first characters of its random id), never
  stored in a task's spec, never shown: the page shows the account's e-mail and plan only.
  `ERIPHE_CHATGPT_ISSUER` points the sign-in at a fake OpenAI, for tests.

Secrets stay in files (or the vault). A provider's key is read when a task starts, sent to its device,
and never stored or logged; an access's never leaves eriphe-core.

## Dynamic catalog (workspace)

Servers, providers and agents can also be added, edited and removed from the workspace (Settings →
Tools, Settings → Agents) or the API, without touching the file and without a restart. They are kept
in eriphe-core's database (`catalog` table) and merged with the configuration at runtime:

- **The file wins.** Its entries show in the workspace as read-only ("défini dans la configuration")
  and are refused on writes (409); a workspace entry never shadows one of them. The file must stand
  on its own: workspace entries may use the file's providers, not the reverse. `default_agent`, if
  the file sets it, is read-only; otherwise the workspace chooses it (the first agent by name until
  then). The brain's agent works the same way: `brain.agent` in the file is read-only;
  without it, the workspace chooses it (`PUT /v1/brain-agent`), else the default agent, else the first
  one that can be the brain. The same holds for its tools and the other fields of the
  [assistants](#assistants): what the file sets is read-only, the workspace sets the rest.
  `brain.tools` may name a server the workspace adds later: it is left out of the brain's grant
  until it exists.
- **Same checks.** Every change runs the configuration's own validation (`checkAgents`,
  `checkServers`, the run specs of local servers) on the merged result, and is refused with its
  reason if it fails. A stored entry that no longer checks at start (the file changed under it) is
  left out, logged, and shown in the workspace.
- **Live.** The merged configuration is an immutable snapshot, swapped whole on each change (the
  many goroutines that read it never see half a change). Then the upstream pool connects new
  servers, closes removed ones and starts changed ones over; local servers are registered again as
  services; tasks waiting for a device are placed again; `list_tools` and `list_agents` show the new
  state; and, with a brain, an event message in the conversation tells it what changed. A running
  brain's own MCP servers are set when it starts: a tool added to `brain.tools` reaches it at its
  next start.
- **Secrets where they are used.** A workspace entry's credential (a bearer token, a provider key,
  an OAuth client, a local server's secret variable) is typed in its own form, stored in the vault
  under a name derived from the entry (`server.<name>.token`, `server.<name>.client-id`,
  `provider.<name>.key`, `service.<name>.<env>`), never shown back ("défini — remplacer"), and
  deleted with the entry or when it stops using it. Workspace entries refer to the vault only, never
  to a file of eriphe-core's machine. Removing a server, or changing its URL or authentication, also
  forgets its OAuth client and tokens.

| Call (bearer token) | What |
|---|---|
| `GET /v1/catalog` | servers, providers, agents and the default agent, each with its `source` (`config` or `workspace`), and the entries left out at start |
| `PUT /v1/servers/{name}` | a server as in the configuration, plus `{"secrets": {"token": "…"}}` (`token`, `client-id`, `client-secret`, `env:<NAME>`); `"auth": {}` with a token is a bearer |
| `DELETE /v1/servers/{name}` | remove a workspace server |
| `PUT /v1/providers/{name}` | `{"kind", "key"}` |
| `DELETE /v1/providers/{name}` | refused (409) while an agent uses it |
| `PUT /v1/agents/{name}` | an agent as in the configuration |
| `DELETE /v1/agents/{name}` | remove a workspace agent |
| `PUT /v1/default-agent` | `{"name"}`, unless the file sets it |
| `GET /v1/assistants[/{name}]` | the [assistants](#assistants), with their spec, source and status |
| `PUT /v1/assistants/{name}` | an assistant's spec; for one of the file's, what the file leaves out |
| `DELETE /v1/assistants/{name}` | remove a workspace assistant (or the workspace's edits of one of the file's) |

`GET /v1/servers` gives each server's `source` too, and `eriphe servers` shows it.

## Tool catalog

`servers` lists the MCP servers tasks can be lent, like `agents` lists what runs them. eriphe-core
holds their credentials and is the only one to reach them: **a task never sees a vendor token**.

| Field | What |
|---|---|
| `url` | a remote server (streamable HTTP) |
| `auth` | exactly one of `{"bearer_file": "/secrets/x"}` (a static token, read at each connection), `{"oauth": {...}}`, `{"connection": "<id>"}` (a connection whose provider mints the tokens, refreshed before they end: Eriphe's GitHub App on one account's or organization's installation, see [GitHub in a task](#the-github-app)), `{"none": true}` |
| `run` | instead of `url` and `auth`: a local server, a container a device runs as a service (see [Local MCP servers](#local-mcp-servers-services)) |
| `use` | what it is for, for the brain: one sentence, 300 characters at most, shown under the heading of its page and edited there in place (its pencil, `POST /settings/tools/{name}/use`) |
| `title` | the user's own name for it, shown on its card and as its page's heading (edited there in place: the pencil beside it, Enter saves, Escape cancels; `POST /settings/tools/{name}/title`); the key stays its technical name, in its tools' names, and a new title restarts nothing |
| `access` | what Eriphe may do on it: `read-only`, `reads`, `free` or `ask` (see [access levels](#access-levels)); without it, `ask` |
| `always_load` | its tools are loaded upfront by a harness that otherwise searches for them (Claude Code's tool search): one model round trip less before the first call; for a few tools needed fast, such as the house |
| `timeout` | seconds for one tool call (600) |

A name is lowercase letters, digits and dashes (it becomes `mcp__<name>__<tool>`).

**Upstream pool.** eriphe-core keeps one MCP session per server, lists its tools and keeps them
cached (refreshed on `tools/list_changed`), and reconnects with backoff (1 s to 5 min). A server is
`connecting`, `ready`, `needs_auth` (OAuth: the user must connect it; or a [Google
account](#google-accounts-gmail-calendar-drive) Google refused, until it is reconnected) or `error`.

**Grants.** A task is lent catalog tools as [resources](#resources) of kind `tool`: a server name
(all its tools) or `server.tool`, where the tool may hold `*` globs (`notion`, `github.get_*`,
`github.list_issues`). The grant is stored on
the task. The brain's is `brain.tools` plus, without configuration, every read-only tool of the
servers trusted for their annotations (see the brain). Catalog tools need an agent whose runtime
takes MCP servers.

**The path of a call.** When a device fetches a task, its assignment names the catalog servers of
its grant (`spec.servers`), nothing else. The device gives the agent, for each, an MCP server at
`<callback-url>/mcp/<server>` with the task's own secret, and relays its requests to eriphe-core
at `/v1/device/tasks/{id}/mcp/{server}` with the device's secret (and certificate), over the same
connection as its assignments and reports (so it works wherever they do: the NAS device on the Docker
network, the PC device under WSL). eriphe-core serves there an MCP server built from the cached tools the grant
covers (their own input schemas; `tools/list` shows only them, any other call is refused), and
forwards each call upstream with the real credential. Tools only for now (no resources or
prompts); a long call streams, within the server's `timeout`.

**Tickets.** A call goes upstream only if its permission request was allowed (by the policy or the
user) for that task, that tool and that exact input, within 30 minutes, once. An agent
that found its task's secret (in its process list, say) cannot call a tool behind its own
permission prompt.

`list_tools` shows the brain the catalog: each server, its status, `use`, its tools (name, a
short description, read-only by the server's own annotation), and the command that connects it
when it needs an authorization. Descriptions come from the servers: they are marked untrusted.

**One taint rule** (`tool_trust.go`, `task_taint.go`). The brain's turn, an assistant's session and
a task are tainted once they read what comes neither from the user nor from Eriphe:

- What the user brings through a channel that guarantees it is them is theirs: their messages and
  their attachments (`read_attachment` does not taint; an attachment of a message from a channel
  the user did not trust taints as that message does). So are what they lent of their own: their
  folders, their repository, the skills they approved.
- **A catalog tool taints unless it is marked trusted**, by the user, visibly: Settings → Tools,
  the tool's page (`/settings/tools/{name}/access`, "Ce qu'il rapporte", a switch for the whole
  server, the configuration file's tools too) and a "de confiance" tag on its row; `list_tools`
  gives it as `trusted_content`. None is trusted by default; mark only a tool whose results are the
  user's own (their notes). The mark is kept in the store (`tool_trust`, a row per decision); a tool
  removed from the catalog leaves it untrusted, so a new one of the same name starts untrusted.
  The web (`WebFetch`, `WebSearch`) always taints.
- **A tainted task or assistant returns tainted text.** A worker task is tainted when it was
  started on a tainted turn, when it called a tool not trusted (seen by the proxy, which every
  catalog call crosses, and in its events), when it read the web, or when its events cannot be
  read. The event of its turn's end then says so and taints the turn that reads it
  (`task:<id>`), as `task_status` and its node's trace do; a task that read only the
  user's own and trusted tools does not. A closed value (a choice, a number, a date, a count of
  rows) never taints, whoever wrote it (assistant state).
- **The limit: a task's shell traffic is not read.** What a program fetches with `curl`, a package
  install, a request through a lent credential (an access), a `git pull` from someone else's remote:
  Eriphe sees none of it, so it does not taint the task. Lend such a task nothing that acts, or
  read its result as data anyway.

### OAuth and `eriphe connect`

`{"oauth": {}}` follows the MCP authorization spec with the go-sdk's authorization code handler:
protected resource metadata, authorization server metadata, a client (a Client ID Metadata
Document if `client_metadata_url` is given and supported, else a pre-registered client
`client_id_file`/`client_secret_file`, else dynamic registration, as a public client, or with
`client_secret_post`/`client_secret_basic` and the secret it gets when the server takes no public
client or refuses one, else, when the server offers no dynamic registration but takes Client ID
Metadata Documents, Eriphe's published client: see below), PKCE, the `resource`
parameter, the `iss` check, refresh tokens (`offline_access` when advertised), and step-up on
`insufficient_scope`. `scopes` replaces what the server advertises. A pre-registered client is
registered at the provider with a loopback redirect, given as `redirect_uri`
(`http://127.0.0.1:<port>/callback`).

From the workspace, Connect sends the browser to the authorization page, which redirects back to
the workspace's own `https://<workspace host>/oauth/callback`: no step to paste. That redirect is
only for flows started from the workspace (the API's connect takes loopback redirects only), and
the dynamic registration of such a flow registers it. The session cookie (`SameSite=Strict`) is
not sent on the redirect from the vendor's site, so the flow is bound to the browser by a cookie of
its own (`__Host-eriphe-oauth`, `SameSite=Lax`, `HttpOnly`, its hash kept with the flow), found by
its `state` (which the go-sdk handler checks, with PKCE), and refused from any other browser or for
a CLI flow. The result page sends the browser back to the tools, where the session cookie applies
again. A server whose pre-registered client only knows a loopback `redirect_uri` keeps the paste
step.

**Eriphe's published client.** Some servers take a client only through a Client ID Metadata
Document (CIMD): no dynamic registration, no app to make at the vendor's (ElevenLabs). For them,
eriphe-core signs in with the document the project publishes on GitHub Pages,
[`site/oauth/client.json`](../site/oauth/client.json): its URL is the `client_id`, and its one
redirect is `http://127.0.0.1:8976/callback`. It describes no installation, so the service never
learns where an Eriphe runs. It is used only when the server's authorization server advertises
`client_id_metadata_document_supported` and no `registration_endpoint`, and the tool has no client
of its own (`client_id_file`, `client_metadata_url`, `redirect_uri`, a client typed in the form, or
a registration kept from before); what the server offers is read once and remembered ten minutes.
From the workspace, such a tool is saved first, then Connect takes the paste step: the browser ends
on the loopback page, which does not load, and its address is pasted back. From a terminal,
`eriphe connect` listens on port 8976 and catches it; the API refuses another port for such a
server.

From a terminal, connecting is the CLI's:

```sh
eriphe connect notion
```

It listens on `http://127.0.0.1:<port>/callback` (a loopback redirect, which OAuth 2.1 allows for
native apps: the port of the registered client if there is one, else 8976, the port of Eriphe's
published client, else a free one), asks
eriphe-core for a flow, opens the authorization URL (or prints it, `-no-browser`), receives the
redirect and hands the code and state over. If the browser is on another machine, paste the URL it
ended on (the page will not load: that is expected). Until connected, the server is `needs_auth`
and its calls fail with the command to run; the brain tells the user.

| Call | What |
|---|---|
| `GET /v1/servers` | the catalog with status, tool count, redirect hint (bearer token) |
| `POST /v1/servers/{name}/connect` | `{redirect_uri}` → `{flow, auth_url}`, or `{status}` if no authorization was needed |
| `POST /v1/servers/{name}/callback` | `{flow, code, state, iss}` or `{flow, error}` → `{status, error, tools}` once connected |

The dynamic registration and the tokens are kept in eriphe-core's SQLite file (`oauth_clients`,
`oauth_tokens`) and restored at start, so a restart needs no new authorization, and a later
connect reuses the registered client for the same redirect. Tokens are secrets: they are never
logged nor put in a task's spec, and a refresh that rotates the refresh token writes it before the
new access token is used. The database file is as sensitive as `/secrets`.

### Security model

- Vendor credentials live in `/secrets` (bearer tokens, OAuth clients) and in eriphe-core's
  database (OAuth tokens). A remote server's go from eriphe-core to that server only. A local
  service of Eriphe's own (the mail, the calendar, the files) reads its account's token in its
  environment, lent by eriphe-core to the device that runs it, which passes it to that service's
  container only ([connections](#connections)): a token is never in a task, a task's spec or a
  service's spec, and never in a device's other containers.
- A task reaches only its device, with its own secret, and only the servers of its grant; the device
  reaches eriphe-core with its secret, and only for its own running tasks.
- Every call is a permission request first, under the policy below; the proxy enforces it with
  tickets.
- git and gh in a task get [GitHub in a task](#github-in-a-task): a one-hour token for one
  repository, decided at launch as any lend, and logged; it goes in on the way out and stays out of
  the task. Only a repository the GitHub App reaches is lent, each push held to the repository's
  cases (Modifier, Supprimer, Envoyer).
- What servers say (descriptions, results, errors) is data for the agents, never instructions:
  the brain's prompt says so, and the policy, not the brain, decides what reaches a third party.

## Devices

Devices are not configured: they join. `eriphe-core join-token -name pc` creates a single-use token
for the device `pc` (valid 24 h, `-ttl` to change) and prints the command to run on the new machine;
the workspace's "Connecter un appareil" does the same. It reads the same `-config` or `-data` as
eriphe-core and asks the running one; `-quiet` prints the token alone, for a script. The address
in the command is `public_url` or, without it, the workspace's own host on eriphe-core's port
(a workspace opened at `localhost` says that address only works on that computer); the CLI then
prints a placeholder. A token is `id.secret` and joins only under
the name it was made for, and never under the name of a registered device: a stolen or mistyped token
cannot take over another device's tasks and lent credentials. A reinstalled machine joins again with a
rejoin token for its own name (`join-token -name pc -rejoin`, or "Réinscrire cet appareil" on its
card), which replaces its secret. Tokens made before names existed join a new name only. The device gets its own
secret (only its hash is stored), then keeps a tunnel open to `GET /v1/devices/connect`; eriphe-core
speaks the device's API through it. A device is online while its tunnel is up and answers.

| Call | What |
|---|---|
| `POST /v1/join-tokens?name=pc&ttl=24h` | new join token for `pc`, with `ca_hash`; `&rejoin=1` for a registered device (bearer token) |
| `GET /v1/devices` | registered devices, with `cert_expires` once a device has its certificate, and `version`, `latest`, `update_available`, `update_commands` (bearer token) |
| `GET /v1/version` | `{core: {version, latest, update_available, update_commands}, device: {latest}}`: the latest versions on the registry, empty while unknown (bearer token) |
| `DELETE /v1/devices/{name}` | forget a device; its tasks become `lost`, its certificate is revoked (bearer token) |
| `GET /v1/devices/tls`, `PUT /v1/devices/tls` | the CA's hash, whether devices must use TLS, which have no certificate yet; advanced: `{"tls_only": false}` keeps the plain path open, `{"tls_only": true}` closes it (bearer token) |
| `POST /v1/devices/join` | `{name, token}` → `{secret}` (join token for that name) |
| `POST /v1/device/certificate` | `{csr}` (PEM) → `{ca, certificate, expires}` (device secret, and its certificate once it has one) |
| `GET /v1/devices/connect` | the tunnel (device secret, `Eriphe-Device: name`, and its certificate) |

**Versions.** Releases are Git tags `vX.Y.Z`, which CI creates from the Conventional Commits merged
to `main` ([RELEASING.md](../RELEASING.md)). All the images of one CI build share its version
(`.github/workflows/ci.yml`, job `version`):

| Built from | Version | Image tags |
|---|---|---|
| a push to `main` that releases `0.2.0`, or a tag `v0.2.0` | `0.2.0` | `0.2.0`, `0.2`, `latest` |
| a tag `v0.3.0-rc.1` (a prerelease) | `0.3.0-rc.1` | `0.3.0-rc.1` |
| a push to `main` that releases nothing | `0.2.0-dev+<short commit>` (the last release, `0.0.0` before the first) | `edge`, `sha-<short commit>` |
| your machine (`go build`, `docker build`) | `dev` | |

The binary has it (`-ldflags -X main.version`), and the image as the label
`org.opencontainers.image.version`, which eriphe-core reads on the registry for `latest`, the
latest release (`updates.go`), at start and then every hour of the wall clock (every five minutes
after a failure; a machine that slept checks when it wakes); Settings → Devices, opened with an
answer older than 15 minutes, asks again in the background; "Vérifier maintenant" in Settings →
Devices → Updates asks at once (once a minute at most) and says when it last did. A machine is behind when it runs a release older than that one
(semver order, `version.go`); a build of
`main` or a local one never says so, as someone is testing it. The images publish their `latest`
tag each on its own, as their build ends: while a release is half published (eriphe-device's
`latest` out, eriphe-core's or eriphe-base's not yet), the latest release is the older one, which
every image has, and the check runs again five minutes later. The workspace shows `0.2.0`, and a
build of `main` as `0.2.0-dev` with its commit in small.

### Updates of the machines

Each machine updates itself, with the updater its installer put on the host side
(install/README.md, The machines update themselves); eriphe-core says when (`machine_updates.go`).
The workspace always says when a machine is behind (a dot, "Mise à jour disponible"), with one
"Mettre à jour" button on each machine that has an updater, "Tout mettre à jour" when several are
behind (eriphe-core first; the devices follow once it runs the latest version), and one switch for
all machines, "Mettre à jour automatiquement la nuit" (off by default; 3:00 to 5:00 in `timezone`,
once a night). A machine without an updater shows the one command that updates it, for how it was
installed (`ERIPHE_INSTALL`: `linux`, `macos`, `windows`, or `docker` for the one-container
install, whose launcher is the updater: `docker exec eriphe eriphe-launcher update`). Going back to the previous version, or staying on one, is the install command's
(`--rollback`, `--version`). A request is "update" (to the latest version, or the one the machine
is pinned to) or "rollback" (left for the API and the tests), never an image nor a command. A
machine pinned to a release (`--version 0.29.0`) says "épinglée sur 0.29.0" and offers no update
(nor a dot); one pinned to a line (`0.29`) still offers that line's fixes. The
choices of before, for all machines and for each one, are dropped at start; "at night" for all
turns the switch on.

The order is Kubernetes': eriphe-core's machine first (its request goes to `update-requested` in
the data folder, for the updater; the outcome comes back in `update-status`); a device only once
eriphe-core runs the latest version, in its assignments (`update`, with eriphe-core's version, the
most the device may get), and the device waits for its tasks to end. A device is offered eriphe-core's
build, never a newer one: while eriphe-core is behind, a device's "Mettre à jour" asks eriphe-core
first ("Eriphe se met d'abord à jour, puis cet appareil"), and the card says it waits for
eriphe-core until eriphe-core runs the latest version; the device is asked then (`followUpdates`,
as for "Tout mettre à jour"), and not at all if eriphe-core's update ends elsewhere (failed,
rolled back, pinned). Without an updater on eriphe-core's machine, the button waits for its
command. An update that ends "ok" or "current" on another version than the one offered says it
did not reach it, never "À jour". While a device has a request, the
scheduler treats it as offline: new tasks wait for it. A request not done within a day is dropped;
one with the updater (handed to it, or "running") that has not ended within 30 minutes stopped (its
updater died, or a reinstall restarted it): it is dropped, and the machine's line says "La mise à
jour s'est arrêtée sans résultat". A device that runs eriphe-core's version already (its install
command was run again) is not sent its update request, which is dropped.
A machine without updater (installed before, or by hand, as `deploy/nas/compose.yaml`) says so and
gets no request.

An update also brings a machine to the install's layout of its version (what an install puts there
besides the images: services, LaunchAgents, Syncthing; install/README.md, The machines update
themselves). What its updater cannot do alone, the device reports in `update.needs` (`why`:
`password`, a step that needs the Mac's password; `admin`, one that needs an administrator's
PowerShell; `layout`, any other, such as an updater from before the layouts), and the device's card
says « Cette mise à jour a besoin de toi : … » with the command that updates that machine in a code
block to copy (`manualUpdate`, from how it was installed: never a command a device names), while no
request is under way. The brain is told once, after 10 minutes with no request under way
(`tellNeeds`), to mention it to the user; it is forgotten once the machine needs nothing.

While a request is pending, the device's card (or eriphe-core's row) follows it without a reload:
it asks `GET /settings/devices/{name}/update?id=<request>` (`/settings/updates/core` for
eriphe-core) every 3 s, 10 s while the request waits for the tasks, and is swapped whole: asked,
under way, then the new version and "À jour", or how it failed, and it stops asking. A request
counts as done as soon as the updater wrote its outcome. While eriphe-core restarts for its own
update, the card keeps asking quietly (`app.js`), the session staying valid as it is kept in the
database; after 10 minutes without an answer it stops and says to reload the page.

### TLS between machines

Devices reach eriphe-core over TLS, with a certificate authority for machines that eriphe-core makes
at its first start, as kubeadm does for a cluster. It is not the workspace's certificate: it signs
only eriphe-core's certificate for the devices (for the name `eriphe-core`, which a device checks
whatever address it dials) and one client certificate per device.

- **Where.** `machine-ca.pem` and `machine-ca-key.pem` (0600) in the data folder, next to the
  database. The key is a secret like the vault key. The CA lasts ten years and is never replaced
  by itself: a CA file that does not load stops TLS for devices (logged at start) rather than making
  a new one, since every device pins it.
- **One port.** The API port serves TLS and plain HTTP side by side: eriphe-core tells them apart by
  the first byte a client sends (a TLS handshake starts with `0x16`). On the local network, the CLI,
  Home Assistant, the brain's MCP and every other API caller may keep their plain `http://…:8800`;
  a device reaches the same address as `https://…:8800`, and the join commands say `https://` when
  they carry `-ca-hash`. On the tailnet, `:8800` keeps its Tailscale certificate (publicly trusted:
  the address to give the CLI or Home Assistant away from home) and asks for a device's certificate
  too.
- **No token in clear over the internet.** A call with the API's token (or a channel's token) over plain HTTP from a public address is refused (403): the token already crossed the
  internet in clear, and a setup like that should fail at once rather than leak it at every call.
  Plain HTTP stays open to private, loopback, link-local and Tailscale addresses (and to the
  Docker gateway, which may be the host itself); TLS works from anywhere. Device requests (join,
  tunnel, reports) are not affected: they are TLS once the device knows the CA.
- **Joining.** The join command carries `-ca-hash sha256:<hex>`, the SHA-256 of the CA's public key
  (like kubeadm's `--discovery-token-ca-cert-hash`): the device finds that CA in eriphe-core's TLS
  handshake, pins it, and only then sends its join token. `eriphe-core join-token` and "Ajouter une
  machine" print it, and Settings → Appareils → Avancé shows it, for a machine set up by hand.
- **Client certificates (mTLS).** A device asks for its certificate with its secret
  (`POST /v1/device/certificate`, with a certificate request: the key never leaves the device), valid
  one year, and for a new one with its current one when a third of its life is left. A request
  that presents a certificate needs the device's secret and its current certificate (or the one
  before, while it moves to the new one): forgetting a device or letting it join again revokes it at
  once, and drops its tunnel.
- **Devices that joined over plain HTTP.** An existing device (its secret only, in `device.json`) asks for
  its certificate with its secret on its next connection, at the address it knows, pins the CA that
  comes with it, and connects over TLS from then on: nothing to do on it but update it. That first
  request trusts the network it crosses, as the plain join did; given `-ca-hash`, the device checks
  the CA instead.
- **Closing the plain path.** eriphe-core accepts devices over TLS only, with their certificate: from
  the start on a new installation, and on an older one by itself as soon as every device has its
  certificate (logged; Settings → Appareils marks those still without one, `GET /v1/devices/tls`
  lists them). It then refuses devices over plain HTTP and devices without their certificate, and drops
  the tunnels of those connected so. Joins then need TLS too. API callers other than devices are not
  affected. `PUT /v1/devices/tls {"tls_only": false}` is the advanced way out, to keep the plain path
  open for a machine that cannot have its certificate yet; eriphe-core then leaves it so.

## MCP tools (`/mcp`, bearer token)

| Tool | What |
|---|---|
| `list_agents` | agents, when to use each, and whether a device can run them now |
| `list_tools` | the tool catalog: servers, status, `use`, tools, which need the user to connect them |
| `list_devices` | machines, online or not, what each reports (CPUs, memory, labels, runtimes, volumes, images), active tasks |
| `run_task` | start a task: title (what the user reads; else its contract's objective, else the prompt's first line), prompt, [resources](#resources) (a repository, the user's folders, catalog tools), agent (or the default), device (or let the scheduler choose), an optional [contract](#contracts-contractgo), `node` (its own node is a child of that node, else of the turn's node, else at the top) |
| `list_repos` | the GitHub repositories the app's connections reach: `repo`, its `cases` (`read`, `change`, `delete`, `send`: `free`, `approval` or `never`) and `own_rules` when it has cases of its own (listed first); a task is lent only one of these |
| `list_folders` | the user's folders: name, machine (online or not), access (`read` or `write`, as shared on its machine), `rights` (`read`, `change`: free, approval or never), `copyable`, where a task finds it, and `machines`: every machine holding it (its own, then its copies), each `online` and `up_to_date` or not; a task may run on any up to date |
| `list_skills` | the user's [skills](#skills), approved and on: name, description, files, whether it has scripts |
| `read_skill` | a skill's `SKILL.md`, or another of its text files (never a script: scripts run in tasks) |
| `task_status` | state, the agent's last message (without its receipt), its checked [receipt](#receipts-receiptgo), the pending permission; `wait` blocks until the task needs you |
| `list_tasks` | recent tasks |
| `answer_task` | reply to the agent's question (a permission is never the brain's: it is refused); `release` asks the user on a card to release the tools it was lent [held back](#resources). For a task with a node, the reply goes to its node's trace (« Eriphe a répondu : … »), so the user can see and contradict it; display only |
| `stop_task` | stop the agent |
| `withdraw_proposal` | withdraw a card the brain made that still waits and the work made obsolete (`ref`): nothing of it runs, nothing is decided for the user; never a task's own ask or start ([The brain on nodes](#the-brain-on-nodes-node_toolsgo-node_turngo-node_netgo)) |
| `node_settle` | decide on an exception of a work cut in steps, when woken by `[nœud <id> « title »]`: on a step `accept`, `retry` (with what to fix), `drop` or `replan` (with the `steps` that replace it); on the work, no step, `accept` (more time, or done after a final check that fails), `retry` (the final check again), `replan` (with `steps` to add) or `drop` ([Work in steps](#work-in-steps-node_stepsgo)) |
| `propose_view` | propose a view: a standing node, its monitor and its skill, on one card ([Views](#views-node_livego-view_cardgo)); its node opens at once as a draft linked to the turn's subject, showing the real page, the card waiting on it; asked for a page kept up to date, the brain opens the subject first, then calls it in the same turn with sensible defaults, no questions first (its choices are changed after, by ask buttons); with `node`, the same draft is rewritten and its card says the new proposal; returns the draft (`node`) and the card's ref (`card`) |
| `catalog_read`, `sources_read` | the whole grammar of a node's block (components, sources, items, sections, glance) and what a block may read ([The catalog tools](#the-catalog-tools-node_cataloggo)) |
| `node_open` | open a work node, for a new subject the brain's first call, before any read (a short title, a one-line « En cours : … » body, filled as the work goes): `title`, `parent` (a child of that node), `goal` (never shown), `body` (markdown text and `eriphe:block` fences), `steps` and `budget_minutes` ([Work in steps](#work-in-steps-node_stepsgo)), `aside` (opened in the background: not shown, the bar stays); its `title` a short name of what it is about, never the user's sentence; written in a tainted turn, the node is external and its title marked; a standing node, a view, is made only by `propose_view` (its draft a work node until the user's yes); the same `openNode` as a task's `node_open`; by default it opens in front of the user (from the home, the bar moves into it) ([The brain on nodes](#the-brain-on-nodes-node_toolsgo-node_turngo-node_netgo)) |
| `node_find` | find nodes by words of their title, goal, text or blocks (any word of three letters or more is enough, the nodes that hold the most first), each with its `status`; and/or by their `tags` (each, or one below it) and properties (`where`: `{key, op, value}`, `op` one of `=`, `!=`, `<`, `<=`, `>`, `>=`, `exists`, `missing`; a number compares as a number, a date as a date, a text without case; `=` on a list holds when one value is equal), sorted (`sort` a property, `title`, `created` or `changed`; `desc`), the properties asked (`columns`) written beside each node's fields, `limit` (30, 200 at most); with neither words nor filters, those running or waiting, then the last changed; `archived` the archived ones too; a title that may be someone else's words (an external node's, not `title_trusted`) says `title_external`, and is quoted only with `include_external`, which taints the turn (as `node_read` of such a node does); an external node's columns are `withheld` the same way. It is also a block's source, `eriphe-core/node_find` (`node_find.go`) |
| `node_read` | a node's title, state, version and `hash`, body, goal, parent, children, links, `backlinks` (the live nodes that link it, by their text or code), done, archived, its items' notes and its trace's end; an external node, or a tainted item note, taints the turn |
| `node_write`, `node_edit` | replace a node's body, or write one block or replace a piece of text, on the version read (`expect`); `node_open`, `node_write` and `node_edit` list the `[[links]]` of the text that name no node yet (`links_waiting`): a stale write is refused, so is one on an archived node and a block carrying `external` (code's field); they answer `page`, what each block shows (the render report, below); a card block `{"card": "<ref>"}` places one of the node's cards ([card blocks](#the-brain-on-nodes-node_toolsgo-node_turngo-node_netgo)) |
| `node_set` | change a node's `title` or `goal`, its properties and tags (`props`: `{"tags": ["cuisine/recette"], "temps": 15, "source": null}`, `null` removes one; set in a tainted turn, they mark the node external: [node-format](../docs/node-format.md#front-matter); an attachment's id as a value keeps that file in the node: [A file the user sent](#a-file-the-user-sent-kept-with-a-node-node_attachgo)), move it under another node (`parent`, `""` for the top; never under itself or a node below it, never a step: `nodestore.SetParent`), or set it `done` (a decision memory `source_ref` `node:<id>`, once; the event `node.done`) or not; a standing node is never done |
| `node_show` | open a node's page on the user's screen (kv `node_show`, read by the page): asked at the home, the turn and the bar move into it (`moveHomeTurn`), from a node it opens beside; writes nothing |
| `turn_home` | the user's message, said in a node's bar, is not about that node: the turn is about no node from then on, as at the home (`turnHome`, node_turn.go). What it raises waits above the bar (a card it raised already is unlinked from that node), and nothing of it is filed there; its lines stay in that node's bar, where the user reads them. At the home, or on events only, it does nothing |
| `item_note` | note an item of a node's source: what the user should know, a state (`act`, `look`, `info`, `none`, `done`) and a group, the section a view shows it in (`À supprimer`; a List filters on `_group`) |

The brain has no archive tool: only the user archives a node (`archiveNode`, by `user` alone), which
archives every node under it, stops their tasks and their steps' work, and withdraws their cards;
`node_find` with `archived` finds them again.

The scheduler takes the requested device if it is online and fits. Otherwise it works in two passes,
like Kubernetes': it **filters** the online devices that offer the agent's runtime and carry all its
`needs` labels, then **scores** them: +1 per CPU if the agent prefers `cpu`, +1 per GiB of RAM if it
prefers `memory`, +8 per preferred label the device carries, +2 if the agent's image (or the device's
default image) is already pulled, +2 if the device has the agent's runtime unpacked (`agents_ready`),
-4 per active task. The highest score wins, ties by name.

**Placement classes** (`placement.go`), like Kubernetes' hard rules and preferences; none is a
setting:

- **Resident**: an assistant that talks to the user or is woken by events (the brain first). Hard
  rule: an online device started with `-always-on` (the machine eriphe-core runs on) that fits, the
  best other device only when none does (the log says why). If its device has been offline for 30
  seconds and another online device can take it, it moves; if it runs on a device that may sleep and an
  always-on one can take it, it moves there between two turns. Moving starts a fresh session with
  the context pack: the conversation is eriphe-core's, and a message its old device never received is
  delivered again.
- **Bound**: a task with the user's folders goes to a device that holds each of them up to date,
  the folder's machine or one keeping a copy in sync ([Tasks on a copy](#copies-of-a-folder)),
  the best of them by the scores above, and waits for one. A task its device has not started yet
  is placed again, like the others, when another such device is online.
- **The others**: the scores above. A task its device has not started yet is placed again after that
  device has been offline for 5 minutes; a running task keeps its device.

A device given by hand (`device` of `run_task`, or an assistant's) always wins: such a task never moves.

**Asleep or offline** (`device_sleep.go`). A machine that sleeps disconnects and comes back by itself:
a Mac in maintenance sleep wakes for a minute every quarter of an hour. eriphe-core counts the
times a device came back after an absence of 2 minutes or more; one that did twice in the last 24
hours is *asleep* while it is away (Settings → Devices says "En veille", `list_devices` says
`asleep`), any other device away is *offline*. The always-on machine is never asleep. A task its device
has not started yet is placed again after 30 seconds when that device is asleep, instead of 5 minutes.
The device itself keeps its machine awake while a task is at work (device/README.md, Sleep).

**Ready in advance.** Each device's assignments carry `prepare`: `{runtimes, images}`, the runtimes
of the configured agents the device could run (it carries their `needs`) and the images they name.
The device unpacks those agents and pulls those images (and its default one) in the background, at
its first assignments and whenever the list changes: a change of the catalog wakes every device
(device/README.md, Agents). An agent no one configured is never downloaded anywhere.

If no online device fits but a registered one would by what it last reported (a desktop that is off,
say), the task is created `pending` with no `device` and a `pending_reason` ("waiting for a device that
can run claude-code with gpu (offline: pc)"), so the brain sees it waits. Such tasks are scheduled
again whenever a device comes online and every 15 seconds; stopping one stops it at once. If no
registered device could ever run it, `run_task` fails. Devices are checked every 15 seconds. Tasks that were active when eriphe-core stopped are
followed again at start, and a disconnected device's tasks are followed again when it reconnects; a
device that forgot a task marks it `lost`.

### Contracts (`contract.go`)

`run_task` takes an optional `contract`, for a task that changes something (none for a search or a
read):

```json
{"objective": "One sentence: what must be true at the end.",
 "constraints": ["Do not change the public API"],
 "checks": [{"name": "build", "run": "go build ./...", "dir": "core"},
            {"name": "tests", "run": "go test ./...", "timeout": 600},
            {"name": "report", "exists": "/work/folders/Invoices/report.pdf"}],
 "evidence": ["the PR link", "the test output"],
 "verify": "auto",
 "budget": {"minutes": 60, "attempts": 2, "turns": 30, "tokens": 2000000}}
```

| Field | Enforced by code? |
|---|---|
| `objective` (required) | no: shown to the agent first, and in the task's drawer |
| `constraints` | in part: what is an access is already enforced by the resources and the policy; the rest is advice |
| `checks` | yes: each `run` (exit code 0, `timeout` seconds, 300 by default, at most 3600) or `exists`, under `/work` only |
| `evidence` | no: asked for in the receipt |
| `verify` | `none`, `auto` (default) or `always`: recorded only, for the independent review to come |
| `budget` | yes: eriphe-core stops the task beyond it |

Approval is never part of a contract: the policy decides it, never the model. A contract is
checked when the task starts (at most 10 checks, unique names, budgets within 7 days, 5 attempts,
500 turns), kept in `task_harness`, given to the agent after its standing instruction, returned by
`task_status` as `contract`, and shown in the task's drawer. `run_task` refuses, with a reason the
brain can act on, what is certainly wrong in a check: an empty `run`, a `dir` given with `exists`,
a `dir` or `exists` outside `/work` or with a `..` element, a path on several lines, a path under
`/work/repo` without a repository lent, or under `/work/folders/<name>` for a folder not lent.
`dir` is relative to the task's working directory (`/work/repo` with a repository: `"core"`, not
`"repo/core"`); whether it exists is only known once the repository is cloned, so the device says it
then (a *config* failure, below).

**A misconfigured check.** A check that could not run at all (its `dir` is not there, its command
is not found, it could not be started: the device's `config`) is the contract's fault, neither the
code's nor the agent's: it is not sent back to the agent, does not count toward "the same failure
twice", is no unsupported claim of the receipt, and does not stop the task. The task stays idle and
the brain hears that the contract is misconfigured, with the device's exact reason. It fixes it with
`answer_task` and `checks` (all of them, checked like at the start; `reply` optional): the
contract and the task's spec are updated, and the new checks go to the device with the next turn,
after which they run. The task's page shows such a check as "could not run".

### Retries and budgets (`task_retry.go`)

What follows a worker's turn that failed is decided in code, by the failure's class (from the
causes of `problems.go`), before the brain hears of it:

| Class | Example | What eriphe-core does |
|---|---|---|
| passing (`transient`) | network, the provider's 5xx, overloaded, a rate limit | retries it after 30 s, then 1, 2, 4 minutes (5 at most), within the contract's `attempts` (2 by default): the agent is told to carry on |
| account | key refused, quota or credit spent, model missing | no retry: the brain is told to warn the user |
| invalid | a request refused (400), a turn cut by its limits | no blind retry: the brain changes the instructions or warns the user |
| permission | the policy refused an action | never retried: the brain hears it as before |
| check | a check of the contract failed | sent back to the agent by its device (see eriphe-device's README) |
| config | a check of the contract could not run (its folder missing, its command not found) | never sent back, never counted, never stopped: the brain is told to fix the contract (above) |
| same failure twice | the same class and words (numbers aside) twice in a row, or a check the device stopped sending back | **stops** the task, even with attempts left |

The budget of a contract is kept by eriphe-core: `minutes` (checked every 15 seconds), `turns`
and `tokens` (input, output and cache writes from the turns' `usage`, where the harness reports
it) at the end of a turn; a turn whose receipt says `done` is not stopped (the brain closes the
task), and `answer_task` refuses a new turn once the turns are spent. A task eriphe-core stops
keeps why (`stop_reason`): the brain hears it with the last receipt when the device reports it
stopped, and the task's page says so; a retry on its way shows its time. A task whose agent or container
failed is told to the brain with its class: worth starting again once, or not. Each failure's class
is kept in `task_harness` for the measures. The brain's prompt no longer counts retries itself.

**No work lost on stop.** Before a task's container is removed (stopped by the brain, a budget or
"the same failure twice"), its device pushes the commits no remote has on the branch the task was
lent, with the task's own git (the same decision applies), or keeps
the repository on the device (see eriphe-device's README). Its `work` event says which: the brain hears
it with the stop, even a stop of its own when the work was kept.

### Usage and measures (`task_stats.go`)

A turn's `turn_end` carries its `usage` (`input_tokens`, `output_tokens`, `cache_read_tokens`,
`cache_write_tokens`, and Claude Code's `context_tokens`: its last call's prompt, the session's size)
and `cost_usd` where the harness reports them (Claude Code both, Codex its
tokens, OpenCode and other ACP agents nothing yet). Claude Code's `cost_usd` is the session's total
so far (carried over a resumed session), so it is never summed: a cost comes from the tokens at the
task's model list price (`task_cost.go`: Opus, Sonnet and Haiku, found in the model's name; another
model has no known cost). The amounts are list-price equivalents, quota with a subscription. A
task's page shows its tokens (cache reads aside) and cost. Settings › Journal shows a few measures over the last 30 days of finished
worker tasks, read from what eriphe-core keeps (no service of their own):

- **accepted**: the last receipt says `done`, no check failed, no unsupported claim;
- **first time**: accepted with one turn, no retry, no check sent back and no receipt asked again;
- **unsupported claims**: receipts that said more than eriphe-core saw (done with a failed check, a
  change the diff does not have), and how many `done` receipts claim verifications no check covered;
- **cost per accepted task**: tokens, dollars where known, and minutes;
- **failures by class** (see Retries and budgets);
- **cost by role**: what every task (workers, the brain, the assistants) cost each of the last 7
  days, by its kind.

### Receipts (`receipt.go`)

A worker ends every turn with a **receipt**: a fenced ```` ```json ```` block, the last thing of its
message, `{"receipt": {status, summary, changed, verified, not_verified, decisions, risks,
needs_approval, lessons}}`, with `status` one of `done`, `partial`, `blocked`. Its standing
instruction (`workerVoice`) asks for it. It is a block of the final text, not a tool call, because
every harness (Claude Code, Codex, OpenCode, any ACP agent) hands back its final text, while a tool
would have to be allowed in each one's permission model. eriphe-core reads the last block that
parses (a `<receipt>` tag and a message that is only the JSON work too) and never trusts it:

- **verified** is only what a check run by the device saw pass (`check` events); what the agent says
  it verified without a check stays *claimed, not checked*; a failed check is *not verified*;
- a file said `changed` is compared with the repository's diff, which the device reports when the
  turn ends (`diff` event); files of the diff left out are counted;
- `done` with a failed check, or a change the diff does not have, is an **unsupported claim**.

A turn with no readable receipt is asked once for it (`skip_checks`: the device runs no check after
that turn), at most twice per task and never twice in a row, without the brain; a turn that failed
is not asked. The brain then gets the checked receipt (in French, like its other events) instead of
the agent's last message; without one, the last message, truncated, as before. `task_status`
returns it as `receipt`, the task's card shows its status and how much is verified, its drawer
verified and not verified side by side, the checks with the end of a failed one's output, and the
mismatches. The last receipt is kept in `task_harness`. The night's memory extraction reads a
finished task's receipt instead of its last message (first party for a task that read nothing
from outside; a task that did is left out of it).

### Attention signals (`attention_signal.go`)

A skill's run says what deserves the user's attention with an **attention signal**, a
structured block rather than a prefix in its prose, in its receipt (`attentionInstruction`, after
`receiptInstruction` in `skillVoice`):

```json
"attention": [{"refs": ["node:k3x9m2p7qa"], "weight": 4, "why": "une facture due aujourd'hui", "say": "Ta facture est due."}]
```

`refs` name the objects it is about, `<kind>:<id>` with a kind among `node`, `card`, `task` and
the keys' kinds (`project`, `alert`…); no kind is a tool's (no `mail:`, `agenda:`, `event:`): an
item of a node's source is raised through its node. Only the form is checked, by `parseRef`. An
entry whose refs are all nodes raises them on the graph and is not told in the run's message.
`weight` is 5 interrupts, 4 needs the user now, 3 matters today, 2 a glance, 1 background; `why` is
cut to 80 characters, `say` is optional. One block alone, or refs given as one string, are read as
a list. A ref of another form, a weight outside 1 to 5 (or not whole), an entry that is not an
object are dropped and logged, never fatal; the rest of the receipt stands. Each ref of a kept
signal enters the [attention queue](#workspace-web) (`c.attend`) with its source (`skill:<name>`,
`assistant:<name>`), marked as drawn from external content when the run read some:
the brain reads it as data.

In a skill's receipt the block is also what tells: no entry tells the user nothing (the run's
message stays in its note), and an entry may leave `refs` out to be about the run's own message,
which then enters the queue as the notification it became (`notification:<id>`), at its weight
(`tellRun`, [Triggered skills](#triggered-skills)). A worker's receipt carries no signal.

### The brain on nodes (`node_tools.go`, `node_turn.go`, `node_net.go`)

The node of the graph is the one object the user sees (#625, #632): one per matter (a pull request
until deployed, an appointment, a research), its doc saying where the matter stands, its trace
what happened in it ([Node store](#node-store-nodestore-nodesgo)).

- **Routing first.** The first seconds of a turn decide where its answer goes (the prompt's
  « D'abord, où ») : a simple answer (a fact, a light action) in one line and no node, at the home
  or in the open node; in the open node; or a new subject, alone or linked to another. For a new
  subject the brain's first call is `node_open`, before any read, `recall` or search (`node_find`
  alone may come before, to take up the node that holds it): a short title and a one-line « En
  cours : … », so the user sees it appear within seconds; the brain, or the tasks that work under it
  (their nodes are its children; the brain writes their result into the subject on their wake),
  fills it as the work goes.
- **Tools.** The brain finds a node before it opens another (`node_find`), opens one at once, with
  no card (`node_open`: a piece of work; what the user follows continuously, never done, is a view
  only `propose_view` makes, standing on their yes), reads it (`node_read`: its version's `hash`), writes it on the version it read
  (`node_write`, `node_edit`, refused when stale: `ErrConflict`), sets its title, goal, `parent` or `done`
  (`node_set`) and opens its page on the user's screen (`node_show`). It links nodes in their
  text, `[[Title]]` ([links between nodes](../docs/node-format.md#links-between-nodes)): there is no
  link tool; front-matter `links` are code's alone (a view's draft, linked to its subject). Each
  checks the node exists (`no node <id>: node_find finds it`) and refuses an archived one
  (`ErrArchived`); a body carrying a block's external is refused
  (`nodefile`'s author API: those are code's). There is **no archive tool**: archiving is the
  user's gesture (`archiveNode`, `by` `user` alone): it archives the node and every node under it
  (`nodestore.Below`), stops their tasks and their steps' work, and withdraws the cards that
  wait on them (`withdrawCards`: linked by `node_cards` or about `node:<id>`, unless a live node
  still holds one): each card is `gone`, its proposal waits no more (a view `dropped`, another
  kind answered no), and reopening the node brings none back; `node_find` `archived` finds them
  again. Its withdrawn cards stay linked to it, gone, so a card block of its document says
  « Proposition retirée »; a card another live node still holds is unlinked from it
  (`linkedNodeGone`).
- **Card blocks** (#663). A block whose JSON is only `{"card": "<ref>"}` places one of the node's
  cards in its document, where its author writes it (under « Ce qu'on a décidé », say).
  `nodefile` reads it as a block of its own (`Block.Card`, `CardRe`: `kind:number`) and refuses
  anything else in it (components, data, sources: `validateCard`); it is written back as
  read. On a write, `nodeGuard` refuses a card that is not one of the node's (`nodeHasCard`: linked
  by `node_cards`, waiting or not); a tainted turn places one too: the block is a ref alone, never
  marked external (a card drawn from external content carries its own badge); a new node (`node_open`,
  a view's draft) holds no card yet, and a view's card waits on its draft, never placed in its subject. The block never carries what the card says: the page
  (`nodePageOf`, `placedCard`) draws the card from the records (pending, decided,
  `gone`), only for the node's own card and a block not external; anything else is sent without
  it and the page says « Carte introuvable », with no Oui or Non. A placed card leaves the page's
  foot (`cards`); an unplaced one waits there. The propose tools return the card's ref (`card`:
  `cardProposed`), and say how to place it (`cardPlaceNote`); `propose_view`'s card waits on its draft.
- **The page's foot only asks.** `nodePageOf` sends at the foot (`cards`) only the cards that still
  ask something (`pendingAsks`: pending, or approved with its call running) and that no
  row answers (any card told on a List's row, `placed`); the decided ones (done, failed, declined,
  withdrawn) go to `settled` (20 at most, the last first), which the page lists in its « ⋯ » menu
  under « Déjà décidé » with each outcome, a failed one with why (`execWhy`, and `execError`: the
  tool's own words, `execErrorExternal` when its server is not trusted, shown marked). A placed
  card decided is not drawn in the body either (the page skips it), and a row's card leaves its
  row once its call is done or failed (`callsAsk`, `accessAsks`). `alive.decided` is the goal the
  user approved the view with (`view_proposals`), shown there as « Ce qu'on a décidé ».
- **What came of a yes** (`card_outcome.go`). The orb says it: the yes is the user talking, so how
  it went reaches the brain as the user's act (`nodeEvent`), whose line is the orb's. A call of
  the conversation tells it at once (`callToBrain`); the calls of a run's or an item's card
  (`runcalls`, `calls`) once the last approved one ran, in one event (`callsCardRan`, told once
  per card: `card_told:<ref>`), what was done and what failed with why. A failed call keeps the
  tool's words (`call_error:<id>`, 500 characters at most) and writes « Échec : <sentence> — <why> »
  in its node's trace (`traceFailedCall`); its item is not noted done. The brain is told to say
  « Échec : » and why in plain words, never to retry by itself, and to propose it again on a new
  card when that makes sense: there is no Retry button.
- **Every card says what came of it** (#750). Each kind of proposal has an `outcome`
  (`proposal_kinds.go`), required at start: on a yes or a no, `decideCardWith`
  (`cardOutcome`) tells the brain at once, as the user's act, on the card's live node, what was
  decided and what came of it (« Sam a dit oui à la carte view:3 (…) : la vue est en place sur le
  nœud X… »), so that it updates what it wrote of the card. A view's is told in the subject it
  came from, naming the view's node. A call's yes is told once it ran (above). A plain yes or no
  in words is told with the message (`cardAnswerLine` carries the outcome). A card withdrawn tells
  nobody. `TestEveryCardKindTellsItsOutcome` decides a card of every kind both ways and fails for a
  kind it has no maker for.
- **Eriphe withdraws his own cards** (`withdraw_proposal`, `card_withdraw.go`). A card of his that
  still waits and the work made obsolete is withdrawn: gone, by `eriphe`, nothing of it runs,
  nobody is told, nothing decided for the user. His own are what the brain proposed, his calls,
  and what his skills' runs keep; never what a task asks for itself (a grant, a release), a task's
  start (`stop_task`), or a tools review. A replacement is proposed as usual; a view's card is changed, not withdrawn (`propose_view` with `node`).
- **No message asks a card again.** A card the brain raises in its turn, its calls and releases
  included, goes with its message of the turn (`cardProposedBy`); a task's access card, with the
  queue's message, which says the card's line, not a question of its own.
- **A row's own card.** There is no yes in a block (no actions, no marks, no Proposal component):
  the page draws a row's card itself, on every List row code told it waits on
  (`lists[].items[].card`, out of band, never from the block's data), even in a block written on
  a tainted turn: its ref is code's. A `Fold` with `list` (a batch) must name a List of the same
  block bound to a source's items (`listsOK`); its line carries the card its rows share. A
  `Metric` with `list` names one the same way: its figure is that List's rows, told to the page
  with them (`blockLists`), its filters applied.
- **Setup buttons** (`setup_buttons.go`). A `Button` with `until` (`tool`, `monitor` or `skill`)
  leaves the page once what it sets up holds: `blockView` drops it, and what only held it, each
  time it draws the block, from code's records (the pool, `monitors`, the skills).
- **The render report** (`page_report.go`). `node_open`, `node_write`, `node_edit` and `propose_view` read the
  node's sources at once (5 s at most, a read under way waited for, then « still reading ») and answer `page`: per block its
  state (`ok`, `error`, `waiting`, `empty`), each source's (a failure's words, and what a
  source holds in place of a missing field, only from a trusted server), the rows each list shows (filters applied), the bindings that lead nowhere in what a
  source gave, the setup buttons hidden and, on a page that lasts, the `Metric`s written by hand.
  Code's words and numbers only: the report taints nothing.
- **Done.** `node_set` `done` (`setNodeDone`) marks the front matter's `done` and the state
  `done` (`nodestore.SetDone`, a version of its own that never conflicts with an author's write),
  once: done again changes nothing. A node done now leaves one dated **decision memory** about it
  (`About` and `source_ref` `node:<id>`, `source_kind` `node`, from its title and its doc's first
  line; external when the node is; one of an earlier done superseded) and the journal's event
  `node.done` (key: its id), a breakpoint of the attention queue while the user is there. A
  standing node is never done.
- **Taint.** Reading a node marked external, or an item note written in a tainted turn, taints
  the turn (`c.turn.taint`); a node written or opened in a tainted turn is marked external, so is
  a title or goal it sets. Words filed in a node's trace from a tainted turn or task mark it
  first (`markNodeExternal`, tried again when a write comes between its read and its own), then
  are written: never words without the mark; `node_find`, the context pack, the about cards and events name an
  external node by its id, never quoting its title.
- **The conversation per node.** Each message belongs to a node or to the home: the `messages.node`
  column (`""`: the home). The user's message belongs to the node the bar talks to: the page
  sends it with the message (`node`, checked by `nodeSaid`: one of the graph's, not archived);
  the brain reads it as `[Sam HH:MM #N · nœud <id>]`. The brain's answer belongs to the node of
  what it answers (`talkOf`: the last user message's, else the last event of a node), an event of
  a node to it (a task's end: `taskEventNode`; a call kept on a card linked to a node:
  `callToBrain`, `[nœud <id> « title »] …`). The turn is **about** the last node it named (a node
  tool on it, a delivered message of it): what it raises meanwhile (an access card, a task's start,
  a proposal) is linked to it (`node_cards`), and the node waits for the user while one waits
  (`cardRaisedFor`; its status, `node_status.go`). A decided card stays linked (a card block still
  places it, the page lists it as settled): the status reads only the cards still pending, in one
  query. The card's state is the one truth of what waits: a proposal that moves other than on its
  card (its task stopped, a page, the user's own change, the memory's expiry) moves its card the
  moment it moves (`settleCards`, called by the task's state, the memory's store and the pages
  that change a proposal; `decideProposal` settles its own), never on a timer; the cards are put
  in step once more when eriphe-core starts (`reconcileCards`: a proposal and its card are not
  written in one transaction). A page's yes or no is the card's own (`answerProposal`). `run_task` works under the node it names, else the turn's node (at the top when the turn is about none), and is about that node (its
  `about`, set by code: it takes none).
- **A subject opened from the home** (`moveHomeTurn`): by default a node the brain opens
  (`node_open` without `aside`) opens in front of the user. In a turn that answers the user at the
  home (typed in the workspace: a channel's request keeps its exchange at the home), the turn moves
  into it, or into the node it shows (`node_show`): the user's messages of the turn and what the brain wrote since get
  the node's `node`, the rest of the answer goes there (`talk`), and the show asks the page to
  move the bar into it (`talk: true`): the exchange goes on in the subject's page, and the cards
  raised next are linked to it. From a node, the new node is shown beside, the bar where it
  is; in a turn that answers no message of the user's (an event, a wake), nothing is shown. With
  `aside` (the user asked for it on the side), nothing is shown and nothing moves: it rises when it
  has something for the user.
- **Code's safety net** (`node_net.go`, contract 5): at the turn's end, the turn's answer (its
  texts joined), past `netMinRunes` (600), or, said in a node, any answer longer than its first
  line (`ackLine`, #663; beside a subject the orb says only its first lines), or, said in a node, any
  answer at all of a turn that answers no act of the user's (an event's, a wake's: the orb never says
  it, so its node's trace keeps it; a line, in its trace only, never as its doc), when the brain wrote no node in the turn (`node_open`,
  `node_write`, `node_edit`) and proposed nothing on a card (`cardProposed`: what it says is then
  about the card, which waits under it), is filed by code: in the node the turn answers in (whole in its
  trace, and as its doc when it has none, never a standing node's), else in a new work node at
  the top titled from the user's request (its first line; a neutral « Réponse du … » when there is
  none, or when it may not be the user's words or holds a secret), the answer its doc, for one
  past `netMinRunes` only (a shorter one at the home stays in the conversation, held whole by
  the orb: `answer`, below); a turn that
  answers the user at the home then moves into it, as for a subject the brain opened
  (`moveHomeTurn`). The answer's
  messages say where (`messages.filed`). Written in a tainted
  turn, the node is external. A refusal never loses the answer: it stays in the conversation.
  The turn's answer is only what the brain wrote since it was last given
  something (messages, events, due intentions; kept in kv `brain_turn`), and it is looked at once:
  a turn's end with nothing delivered (a restarted task's first turn) files nothing, and an older
  unfiled message never counts.
- **The context pack and the cards.** A fresh session's pack lists the nodes running and waiting
  (`activeNodes`, the last changed first, at most 20), each with its card (`about_card.go`: the
  node's line, its status, title and its doc's first line, then its decisions, facts, people,
  intentions, cards and preferences); a turn about a node gets its card when the session does not
  hold it as it is. `since` lists the nodes changed (kind `node`: where each stands now, its title and its doc's first line; an external one's quoted only with `include_external`, the activity page quoting it marked « Contenu extérieur »), and `where_now` the nodes that wait for the user (`nodes_waiting`). The tasks panel lists the nodes
  running and waiting above the tasks (`/espace?open=node-<id>`).
- **Memory.** Where a matter stands is its node's doc, never a memory: the `progress` kind is
  gone. The nightly extraction reads the day's node traces (`nodeSegments`: what each node's trace
  says that day, a segment `node:<id>`; an external node gives nothing) and keeps their decisions,
  facts and people.
- **Showing a node** (`node_show.go`): `node_show` keeps the request (kv `node_show`: `node`, `by`,
  `at`, `seq`, growing at each, and `talk` for a subject opened from the home) and wakes who
  watches (`watchShown`); the graph page (#633) opens the page beside the others, the bar
  still talking where it talked (only the user's gesture moves it), except a show with `talk`
  while the bar talks to the home: it moves into that page. A view's yes shows its new node.
  It writes nothing in the node.
- **A call in the trace** (`proxy.go`, `traceBrainCall`): a call the brain made with the user's
  permission (a ticket, not a free read) is a line of the trace of the node it talks to (the page
  the user spoke from), else, from the home, of the node the turn is about (the last it named):
  `Appel : <server>/<tool>.`. A turn of the home about no node (a light switched on) files it in no
  node: the decisions keep it, and the brain reads nothing first to place it (#706).
- **A node's title is the brain's** (`brainNodeOpen`): a short name of what it is about (« Mes
  mails »), never the user's sentence, and never replaced by code. Opened before the turn reads
  external content, it is trusted; opened after, it is shown as it is and marked (`TitleExternal`),
  like any title written after an outside read.

## Node files (`nodefile/`)

The graph workspace (epic #625) keeps each node as a markdown file: YAML front matter, a markdown
body and typed blocks (`` ```eriphe:block <name> ``). The format is specified in
[docs/node-format.md](../docs/node-format.md); the package `nodefile` parses, checks and writes it:

- **Lossless:** a file parsed then written gives the same bytes; what changes through the API is
  written again in its canonical form, the rest keeps its bytes.
- **Each block checked alone**: the catalog (`a2ui`), its links, and its
  bindings against its own data and its sources (`/<name>`); `actions` and `marks` are unknown
  fields. A bad block is a `BlockError` that names it.
- **Code's fields** (a block's `external`, the node's `external`, `title_trusted` and
  `opened_by`) are set by code (`SetBlockExternal`, `Meta.External`). An author's write
  goes through `AuthorBody` or `PutAuthorBlock`: left out, they are kept from the version
  written over; written otherwise, the write is refused (`ErrCodeField`). Authors may give a block
  in its short form (one component, no root); it is completed. A block's sources' values are never
  in the file (they are code's, in `core.db`): a block that says a `snapshot` (an old version,
  an author copying one) still reads, the snapshot ignored, and is written again without it. An
  old step's front matter `after` reads as a field this package does not know, kept as it was.

The checks that need the core (offers, a card block's ref, a bound Link's value, taint) are the
tools' that write nodes.

## Node store (`nodestore/`, `nodes.go`)

The package `nodestore` keeps the nodes: the files are the truth, the index is for speed.

- **Repository:** `nodes/` next to `core.db`, a git repository written with go-git (no git
  binary). Files are flat: `<slug>-<id>.md`, its media in `files/<id>/`. Git versions the
  documents only. The slug (lowercase ASCII from the title, 40 characters at most) is frozen at
  creation: a new title changes the front matter, never the file's name.
- **Index:** `nodes.db` next to it, outside the repository: a pure index, made again from the files
  and the log when it is deleted, nothing lost: a node's status is not kept, it is computed
  (`node_status.go`). SQLite, its schema version in `meta` (5); a file at another version is
  refused, never migrated: delete it. Tables: `nodes` (file, title, parent, lifetime,
  times, external, last version, the file's git hash, the title's link key, its words' row in
  `node_fts`, so a write replaces them without a scan), `node_links` (the front
  matter's), `node_refs` (each `[[name]]` of a node's text and the node it was resolved to, kept
  across renames; `''` while no node has it), `node_versions` (n → commit
  hash, author, time), `node_sources` (each block's live sources), `node_props` (each value of a node's properties and tags, one row per value of a list, typed: a number's `num`, a date's ISO text, a lowercase `fold`; indexed on (key, fold) and (key, num): `nodestore.Select`, `Props`) and `node_fts` (FTS5 over the title,
  the goal, the text and the words the blocks show). History is never asked of git by path: a
  version's commit is in `node_versions`.
- **Writes:** each is one commit by its author (`brain`, `task:<id>`, `user`…), whose message names
  the versions it makes (`Node: <id> <n>`). `Create` makes version 1; `Change(id, expect,
  author, fn)` writes the last version, an author's body through nodefile's author API (code's
  fields kept, a change to them refused); it is refused with `ErrConflict` when `expect` is not the
  last version's commit hash; a node alike makes no version. An archived node is not written (`ErrArchived`). A node is 512 KiB
  and 32 blocks at most. Secrets are refused by the memory's filter (`scanSecrets` in
  `memory_trust.go`: credentials, long random strings, links with a password or a token); its
  injection rules are not applied, as a node may quote someone else's words. A link's path is not
  read as a random string (a pull request's `owner/repo/pull/640/files`, a document's id): a
  token's known shape anywhere, a query's or fragment's token key or long random value, and a
  link's password still are. A random secret as a bare path segment, in no known shape, passes.
- **Code's writes:** `Change(id, expect, author, fn)` lets code change what no author may (a
  block's or the node's `external`, the title, the goal); the id, parent,
  creation, done and archived never change this way, and the filter runs over the node written.
  `Create` with `External` marks the node and each of its blocks; with `ID`, the node takes an id
  given beforehand (`NewID` gives a free one: a card shows the node before it exists).
- **An author's write over code's:** `Change` made on an older version is not stale when every
  version since changed code's fields only (`onlyCodeSince`: a mark, a block marked external): the
  write applies over them, code's fields kept.
- **Live sources:** `Sources(id)` lists the sources of every block of the nodes not archived (or of
  one node), from `node_sources`: what the live engine reads (`node_live.go`); `KeptSources(id)`
  archived nodes' too, whose pages still show their last values.
- **Reads:** `Read(id, n)` gives version `n` (0: the last), `Versions`, `Children` (archived ones
  included), `Find(query, archived, limit)` (all the words, prefixes from three letters; archived
  nodes only when asked), `Get`.
- **Trace** (`trace.go`): `AppendTrace(id, author, text)` adds a dated entry, never cut (256 KiB an
  entry), at the end of `<id>.trace.md` in `nodes-trace/`, beside the repository (`O_APPEND`): no
  commit, no version, the node's `updated` moved. Past 2 MiB (`MaxTraceFile`) the file becomes its
  older part, `<id>.trace.1.md`, the one before dropped. `Trace(id)` gives both parts, the older
  first (the `.../trace` endpoint, `node_read`'s end of the trace, the nightly extraction). An
  archived node keeps its trace.
- **Status:** none in the store. A node's status (`running`, `waits`, `done`, `idle`) is computed
  by eriphe-core each time it is read (`node_status.go`, below).
- **Archive:** `Archive(id, author)` archives the node and every node below it in one commit, each
  a new version. Only the user archives (the tools of a later step). There is no delete.
- **At start:** the files are compared to the index and the last commit. A file changed, added or
  removed outside Eriphe is committed as a version of its own (author `outside`), reindexed and
  logged; a file that does not read as a node is logged and left alone (a node's row is kept, its
  last good version readable). When `nodes.db` is missing, it is made again from the files, and
  the versions from one walk of the log; when it is behind the repository (`meta.head` is not the
  last commit: a copy put back, a stop between a commit and its indexing), the versions are read
  again from the log. Only the state is not in the files: a rebuilt index starts it over (idle). First, every object the branch reaches is read (each commit and tree,
  each blob present): a repository that lacks one does not open, and the log names the ref and the
  missing object (`nodes: repository …: the repository is broken: refs/heads/main reaches blob …`);
  the graph stays off until the repository is put back from a bundle.
  The start is made for thousands of nodes: the files are read and hashed on every core, and a
  file the index holds as it is (its git hash, the last commit's and the index's, at its last
  version, in today's format) is not parsed; a commit of many files stages them in one read and
  one write of git's index; an index is filled in one transaction, each statement prepared once.
  `TestIndexBench` measures it (`ERIPHE_BENCH_NODES=10000`).
- **Never lost:** nodes are the user's notes. No code removes the repository, a node's file or
  its trace, nor drops history (no gc, prune, repack, reset, clean or force; go-git only in
  `nodestore`, git never run as a program); a node is only archived, by the user. The guard test
  `nodes_guard_test.go` holds it: the store's few removals (the backup's own copies, a file's
  removal by hand committed, a file written whole, a trace's oldest part past 2 MiB) are listed
  there, each with its reason. The index (`nodes.db`) is not the truth: it may be rebuilt from the
  files at any time.
- **File format:** each file says its format, the front matter's `format` (absent: 1, the only
  one today; [Format version](../docs/node-format.md#format-version)). A change of the format
  adds its conversion to `nodefile` (from the format before, a pure function of the file's bytes)
  and a test that reads the old format. At start, a file of an older format is converted, all of
  them in one commit by `convert` (its words committed first as `outside` when changed by hand),
  each node a version of its own, logged; an older version read from the history is converted
  in memory. A file of a newer format stops the graph (the log names it), untouched: update
  Eriphe.
- **Nightly**, at the memory's hour (`memory.extract_at`), into its backup folder
  (`memory.backup_dir`, `memory.backup_keep` days): `nodes-<day>.bundle` (a git bundle of `main`
  and `HEAD`: `git clone` reads it and checks it out), `nodes-<day>.trace.tar` (the traces, each as
  long as it was when the tar opened it) and `nodes-<day>.db` (a copy of the index, named last: it
  says the night's backup is whole).
  The repository is only read: no gc, no repack, no object removed (go-git's repack deleted the
  loose objects before its new pack was checked, and a failed repack lost the history). The
  bundle is packed from the objects as they are, written aside, read back whole (every object
  inflates, the checksum holds, every object of the branch is in it) and only then takes its name;
  older copies are dropped only once the night's are made. A failed backup is logged and leaves
  the repository and the earlier copies as they were, and is tried again at the next minute's
  tick, not the next night. Objects stay loose: to compact the repository, stop Eriphe and run
  `git gc` in it. Writes go on meanwhile: the store's lock is held only to read the branch's
  commit, and the bundle packs what it reaches through a repository handle of its own. The
  index is copied first (`VACUUM INTO`, aside) and takes its name last, so it is never ahead of
  the bundle (put back, it is behind at most, which a start reads again from the log) and its
  presence says the night's backup was made whole: a start after the hour finds it and does not
  make another.

## A task's node (`task_node.go`, `task_nodes.go`)

Every worker task works in a node of its own (#629). `run_task` takes `node`: the task's node is
opened as a child of it, else, for the brain's `run_task`, of its turn's node (one archived is passed over), else at the top (an unknown or archived node named refuses the start). The node
is titled as the task, its goal its contract's objective or its prompt's first line, `external`
when the task starts on a tainted turn. Code keeps which task works in which node (`task_nodes`).

**A node's status** (`node_status.go`) is computed each time it is read, never stored: the graph,
the page, `node_find`, `node_read`, `since`, `where_now` and the context pack read the same one.
`waits`: a card linked to it is pending (whatever asks the user's yes is a card: a task's held
start, on the task's node as well as the turn's; an access a task waits on; a tool lent held back
that a task calls, whose release card code raises at the call, `askHeldCall`), or it is a finished
work's result the user has not said done (below: computed, not a card, since no button asks it);
`running`: a task works in it (one held for its start card does not yet), or it is a step whose
task runs;
`done`: its file says `done`, or the steps' engine finished it (a step accepted or dropped, a work
done whose results are its steps); `idle` otherwise. What waits comes first; an archived node waits
for no one. A task that ended (stopped, failed, lost) leaves its node idle: only the user, or the
brain on their word, says a node is done. The graph's rows are computed in a constant number of
queries (`statusFacts`, `warmNodeCaches`); whatever a status comes from tells the graph when it
moves (`nodeMoved`: a task's state, a step's status, a work's state, a card linked, raised or
decided), and a run or a step that ends, or the node archived, drops its progress line.

- **`eriphe-nodes`**, an MCP server Eriphe provides, relayed by the device like `eriphe-memory`
  (the name is reserved in the catalog): every worker with a node has it, its tools allowed in
  advance; it is not lent. `node_read` (a version, its hash, its body, its sources' last values, its item notes), `node_write`
  (the whole body), `node_edit` (one block, or a piece of text found once) and `node_append` work
  on **its own node only**: another id is refused, naming the task's node. Each write names the
  hash of the version it was made on (`expect`; `node_append` writes over nothing). `node_open`
  opens a child of its node (title, goal, body), which it does not write; with `steps`, its work cut in steps ([below](#work-in-steps-node_stepsgo)). `item_note` notes an item
  a source of its node lists. `progress` adds one line to its trace. It never starts a task.
- **Who writes** is code's: `skill:<name>` for a skill's run, `task:<id>` for another. Its calls
  to `eriphe-nodes` never taint it: what it reads there taints by its own record. A node whose
  title or goal the secret filter refuses is opened without the goal, then with a neutral title
  (« Tâche <id> »): the task still starts.
- **Taint**: a task tainted at start or by what it read (`taskTaint`, read at each call) writes
  external: code marks its node and the blocks it writes, in the same version; a child it opens is
  external; its item notes carry the taint. Reading an external node taints the task.
- **Guards** nodefile does not hold, on every block a write adds or changes (a block, or a fence
  written in its text): a Button only asks, in its author's literal words (`a2ui.CheckActions`),
  tainted or not (written tainted, the block is external, and its click comes back marked: see
  `POST /api/nodes/{id}/ask`); a `Link` never opens a
  source's value; a block's source reads a tool or server the task is lent, or a monitor the user
  approved, and one of a server not trusted marks the block external.
- **Its report**: at the end of each turn, the task's last message (its receipt left out) is filed
  by code, whole: in its node's trace (in several entries past 256 KiB), and as its doc when the
  task wrote none. A turn held only for its receipt files its report then. The same report is
  filed once; a tainted task's report marks its node external. A report refused (a secret) or that cannot be the
  doc is said to the brain, which reads it whole with `task_status`.
- **The brain is woken short**: the task's title, its receipt's verdict (status, checks passed or
  failed, what waits for approval), the report's first line, and its node. A step's task does not
  wake it: the engine judges it ([below](#work-in-steps-node_stepsgo)). What Eriphe or the user
  settles for a task (`answer_task`, a release's card) is a line of its node's trace.

## Work in steps (`node_steps.go`)

A piece of work cut in steps is a node whose children are the steps (#630). The brain or a task opens them,
through the one `node_open` (`openNode`), with `steps` (each a `key`, a `title`, the `after` keys it needs, a `prompt`, and,
as `run_task`'s, an `agent`, `resources`, a `contract`, a `device`), with the work's
`budget_minutes` (60 by default, a day at most). The plan is checked before any node is made:
keys unique, what each comes `after` a step of the plan or of the work, no cycle, 20 unfinished
steps at once and 200 over the work's life, and each step's task as `run_task` would refuse it.
Each step is a child node of the work's node; what it comes `after` is the engine's, in
`node_steps` (core.db), never in the node's file. Every step is checked and given its node's id
(`NewID`, `Check`) before anything is made, then its row is written, all in one transaction, then
its node: a node that cannot be made takes the rows of the steps not made with it, so no step is
without its node, nor a step node without its row.

The **engine** is code, without a model:

- It starts every step whose predecessors are accepted, in parallel when they may: never two that
  write the same repository branch or folder (the second waits). Each step's task works in the
  step's node (`runRequest.inNode`), given the work's goal and the results of the steps it comes
  after (2000 characters each); what those may carry of someone else's words taints it. The engine moves one change at a time (`stepMu`), but never starts a task under it: it claims the step (in memory: a claim a restart loses leaves the step waiting, a final check's work goes back to running), lets go of the lock, starts the task, then records it; a step dropped meanwhile stops the task started for it.
- It **accepts a proven step on its own** (`proven`): its receipt says done, no check of its
  contract failed, nothing it claims is denied by what the device saw. The step's node is done
  (but the work's result, below), its result kept, the decision a line of the work's trace, and the next steps start. No brain turn.
- The brain is **woken only for an exception**, once, by an event `[nœud <id> « title »] …`
  (`wakeForWork`): a failed proof (`failed`), a blocked step (its task could not start, failed,
  stopped or was lost), the budget spent (the work is `held`: nothing new starts, the running steps
  finish), the final check not holding. It decides with **`node_settle`** `{id, step?, outcome,
  note}`: on a step, `accept` (a failed proof whose work holds; never failed checks), `retry`
  with what to fix (2 at most: it answers the idle task, or starts a new one), `drop` (with the
  steps that come after it), `replan` with `steps` (drop them and open those in their place, by
  the brain, tainted by its turn; a plan refused changes nothing); on the work, no step, `accept`
  gives it half its budget more (30 minutes at least) or holds it done after a final check that
  fails, `retry` runs the final check again (2 at most), `replan` with `steps` adds them, `drop`
  stops it.
- Once every step is accepted or dropped, a **final check**: a fresh agent in a child node of the
  work (« Contrôle de l'objectif »), lent read-only what the steps wrote, given the goal and each
  step's result. Its receipt done with no failed check makes the work done; its verdict goes into
  the work's trace. A work done puts its **result before the user** (its status, computed): the
  accepted steps no other accepted step comes after wait for the user (one already done or archived
  stays so), the work around them done, until the user,
  or the brain on their word (`node_set` done), says it is done, or they archive it. A work with no
  such step waits itself.
- **Restart**: what the engine knows is in core.db (`node_steps`: node, parent, after, spec,
  status, task, tries, output, opener, taint, skill, clean, times; `node_works`: state, held,
  budget, time spent, final check), never read back from a node's file. The tick
  (`stepsTick`, every 15 seconds with the devices' check) takes up a task that ended, or turned idle
  with its receipt, while eriphe-core was down, then starts what may start.

**Start cards** follow the rights unchanged: a step starts as a task the brain starts, its cards
asked at launch, tainted by its opener's taint and by what the steps before it handed it. One
bounded addition: a step opened by a **skill's run** (or by a step of one) borrows only what the
skill declares: its trigger's `tools` and its `repos:` (front matter, approved with the skill). It
starts **clean**, its Free writes holding without a card (`cleanStart`, lent by the user), only
when the run was clean (no external event, no external content read), its opener was not tainted,
it borrows nothing the skill does not declare, and nothing external was handed to it. Anything
else (an external event, external content read, an undeclared repository) asks its start card as
any task's.

## Views (`node_live.go`, `view_card.go`)

A view (#631) is what the user follows continuously (their mails, the prod's alerts, the pull
requests to review): a standing node whose blocks code keeps fresh, the monitor that detects what
comes, and the skill that works in the node when it does. Nothing here knows a view: the brain
composes each one from the catalog.

- **Live blocks** (`node_live.go`, table `node_reads` in `core.db`): a block's `sources` are read by
  code, without a model, as the brain's free reads are (`sourceWhy`): a tool with fixed arguments,
  a server's resource, or a monitor the user approved. `eriphe-core/node_find` is read by code from
  the graph itself (`node_find.go`), with node_find's arguments and trust: once a block reads it,
  any node's change reads these blocks again a second later (`nodesChanged`, `readServerSoon`); a
  block whose source says `include_external` is external (`untrustedSources`). A task may not name
  it (`lentSource`). A source new or changed is read at once,
  then every `every` minutes (5 by default), at a resource's update, and a monitor's at each of its
  looks (`monitorLooked`). A read that fails or waits keeps the last good value, with why. The
  values stay in `node_reads`, never in the node's file: a read makes no version, however often it
  runs. The page draws the block from them, whatever the version shown; an archived node's reads
  are kept (`KeptSources`, `keptReads`) and its page shows them, read no more; they go with their
  source. What code acts on (attention, cards, skills, freshness) is a live node's reads only
  (`nodeReads`).
  `node_read` gives them (`values`, by block then source, one past 16 KiB said to be so), tainting
  the turn when a block reads a server not trusted. A block that reads a server not trusted is marked `external`, checked at each read:
  code marks it, never unmarks it. A monitor's value is its latest number, its last points, whether
  it is past its limit or failing, and its last events (kind and time, numbers only).
- **A node's glance** (`node_glance.go`, `nodefile/glance.go`, table `node_glance` in `core.db`): a
  source may declare a `glance` (label, path, threshold, unit `percent`|`amount`, compare
  `day`|`previous`|`fixed` with `base`; [node format](../docs/node-format.md#sources)), one per
  node (`nodeGuard`). Each read of the node (`readNodeOnce` → `noteGlance`) takes the JSON number
  at its path and keeps the day's first value (in the user's zone), the previous one, the last one and
  up to 32 recent values, one per 30 minutes, the last one live; no model, no brain turn. The
  variation is from its reference, in percent of it or in its units; `shown` once it reaches the
  threshold, until it falls under 0.85 of it (`glanceOff`). `seen` is set when the workspace's
  view opens the node while shown (`glanceOpened`, from `PUT /api/espace/view`), or when it crosses
  while open, and cleared under 0.85: seen survives a reload and holds on every screen. A
  declaration changed starts afresh; one taken away, or its node gone, is forgotten. The node's
  row (`graphNode.glance`: label, `labelExternal` when its block is external, unit, value, ref,
  delta, threshold, series, shown, seen) is sent again (`graphEvNode`) when it changes. The
  workspace draws at most 3 tiles (graph/glance.ts).
- **Read again after a change** (`source_fresh.go`): a call that ran well and may change something
  (its tool is not marked `readOnlyHint`) is followed by a read of every live source of its server,
  every node's: those of the account it names (its `account` argument) and those that name none;
  all of them when it names none. Every path counts: the brain's own calls and its tasks' (a skill
  run's too, `proxyCall`), a card's yes, an item's Oui and a view's action (`catalogCall`). The calls
  of a moment are gathered into one read, a second after the last (three at most after the first);
  the new values reach the open pages as `source` events. Until then, the items whose ids the call
  named show `_state: done` at once (a List that keeps what is not done lets them go); the next
  read of their source that began after the call says them as they are, an item it still lists
  comes back (in memory only, two minutes at most). A conversation's call on a view's items wakes
  its skill (`node.gesture`, as a user's gesture does); a task's own call never does. A server that
  says one of its resources changed (`notifications/resources/updated`) has its sources read again
  the same way, once every 15 seconds at most (one said meanwhile is read at the end): eriphe-core follows every resource of a server whose tools a node reads, as well
  as the resources nodes read (`eriphe-mail`'s `mail://changes` tells each change its cache sees,
  wherever it was made; `eriphe-calendar` tells nothing yet, its views wait for the period). What
  changes elsewhere and no server tells: a node's sources read more than a minute ago are read again
  when its page is opened (`GET /api/nodes/{id}`), and every node's when the user comes back
  (`user.returned`), once a minute per node at most. Monitors keep their own looks.
- **A monitor raises its nodes**: crossed and holding, or failing, it puts each node whose blocks
  read it in the attention queue (`node:<id>`, in code's words), tainted when its server is not
  trusted.
- **Item notes** (`node_items.go`, `item_notes.go`): a listing source's `items` (`path`, `id`,
  `title`, `detail`) give its items; a task notes one (`item_note`: note, state `act`, `look`,
  `info`, `none` or `done`, group) in `node_items` in `core.db` (`noteItem`, `nodeItems`: never a
  version, nothing a rebuilt `nodes.db` loses), with its author and taint; no note, state nor group
  takes it away; an archived node is noted no more. A note that changes sends the source again to the pages that show it ([sections](../docs/node-format.md#sections)).
- **Calls on an item** (`item_cards.go`): a skill run's call kept for the user that names exactly
  one item of a source of the run's node, on the call's own server, is kept on that item
  (`access_asks.node`, `source`, `item`): one card per item (kind `calls`), answered alone. A call
  on no item stays on the run's card. The card names its node, unless the node is external.
- **Same-kind calls wait as one card** (`call_fold.go`): calls cards that wait untouched in the
  same node, whose calls do the same thing to other items (same server and tool, same arguments
  but one list whose values are ids of the items the call acts on, as its node's sources list
  them), fold into the oldest: one card, not five. A different account or
  destination, another tool, a card decided or one whose call runs never folds. Folded when a run
  ends and at each card sync (cards made before fold there; the folded cards go, from their node
  too). The card keeps each call as kept, its own `access_asks` row: Oui runs each exactly as
  shown and records each decision. Each call is a line with a box, all ticked (on a folded batch,
  its rows); the yes button says what goes. `POST /cards/{id}/yes` then carries `only` (the calls ticked)
  and `left` (unticked), by id: both must be calls of the card that wait, or nothing is decided;
  `only` run, `left` are refused, and a call folded in after the page drew the card waits on, on
  a new card at once (the decided one keeps the words of what it decided). The workspace sends
  `only` on every calls card's Oui, one call included. A calls card has no Oui by plain text; a
  channel's Oui button carries no choice and decides every call that waits, so a call folded in
  after the channel's message would go with it. Decided and run, the card says what was done and what was left (`doneText`: « 4 déplacés, 1 laissé »). A card of one call keeps the words of `call_words.go`. Non
  refuses every call. On its own node's page, a card's origin, its skill and what Oui does in
  general are under Détails; the « extérieur » badge stays.
- **A draft, then one card** (`view_card.go`, kind `view`, table `view_proposals`): `propose_view` checks the
  node (its body as a task's blocks are: the actions' grammar, an ask in its own words, no
  Link to a source's value; each source a catalog server's, an approved monitor, or the view's
  own), the monitor (`write_monitor`'s checks, its name free) and the skill (`write_skill`'s, its
  name free, with `when`; code writes its `node:`, the draft's id). Then it opens the view's node
  at once as a **draft** (`openDraft`): a work node of the brain's, linked (front-matter `links`,
  an edge on the graph; not a child: the view outlives its subject) to the node the turn is about,
  its body the page itself, its sources read at once (`readNodeLater`), opened beside the subject
  (from the home, with the bar in it). Its card waits on the draft (the kind's `node`, so
  `node_cards` links it there and the draft has the halo of what waits), never on the subject.
  Called again with `node`, the draft (`viewDraft`: its proposal still waiting) is rewritten
  (`rewriteDraft`, checked as `node_write` and `node_set` are) and the same proposal updated: the
  same card, saying the new one. The card is short: one line code writes from what it checked
  (`viewSummary`: how often the monitor looks, when the skill runs on a schedule, « Toutes les 15
  min · chaque jour à 7 h 30 »; the page is the preview), then, under « Détails » (fields marked
  `detail`), the node's blocks with what each reads, what the monitor does, what the skill does,
  when it runs, with the repositories its steps may borrow, and its instructions (marked
  `markdown`, drawn as Markdown); the view's `goal` is its why, where the brain says the choices it
  proposed it with, sensible defaults the user changes after, never asked first (the prompt's « Ce
  qui tournera seul, montre-le d'abord »). One yes (`decideView`, through `decideCardWith` as any
  card) makes the monitor (on) and the skill (approved), then turns the draft standing (opened by
  `user`, its card's; its title vouched for while it is the one proposed), and marks done the work
  nodes it links (its subject: `setNodeDone`); one step that fails takes back those before, the
  draft stays one, and the card waits still. No makes none and archives the draft. Archiving the
  draft drops it (`dropped`, its card `gone`). Written on a tainted turn, the card says so and the
  draft is external. A monitor or skill name taken is refused with what to do: a monitor for the
  same thing is read by its name (leave `monitor` out), never written again under another name.
  The yes opens the view's node (`showNode`).

## Permission policy

Every permission request of a worker goes through a policy written in code before the brain hears
of it, so that a prompt injection (a mail, a web page) cannot talk the brain into it:

1. **built-in denials**, which no rule lifts: a recursive `rm` naming `/work/folders`, pushing to
   `main`/`master`, force pushes, reading secrets (`$ANTHROPIC_API_KEY`, `printenv`,
   `/proc/*/environ`, Claude Code's `.credentials.json`, Codex's `auth.json`, `$ERIPHE_CODEX_AUTH`,
   `$CODEX_HOME`…). OpenCode's and Codex's requests (the `opencode` and
   `codex` runtimes) come under Claude Code's tool names (`Bash`, `Edit`, `Write`, `WebFetch`…)
   with the same inputs, so the same rules apply;
2. a catalog tool at Never, and a call that must name its account: the proxy would refuse them;
3. a tool lent [held back](#resources) is the user's until its release: neither a grant of its
   category nor a free read lifts that;
4. for a catalog tool, its **exception**, then the actions the user allowed for good ("Toujours
   autoriser", [below](#access-levels)), then its **category's value** in the
   [grid](#access-grid) of the account the call names: On approval asks on a
   [card](#the-approval-card), Free is allowed;
5. an MCP tool out of the catalog: refused at once, it is not lent. There is no built-in ask on a command: what a task reaches
   with a secret is what it was lent, decided at launch, and Free asks nothing;
6. anything else that stays in the task's container (its shell, files and web tools: `Bash`,
   `Write`, `Edit`, `WebFetch`…) is allowed (`inContainer`). The container
   is the boundary: what leaves it goes through the device's mounts (a folder's level), its egress
   filter and the credentials eriphe-core lends, which the shell rules above cannot replace (they
   read a command line, and only stop mistakes);
8. what is left is the brain's to judge. A request the brain was told of and left at the end of
   its turn (it relayed it, or could not decide) is the user's. The workspace and the
   notifications show the user only the requests that are theirs (any while the brain is off or
   stopped by an error); a task's own page shows them all.

For an MCP tool (`mcp__<server>__<tool>`), `match` applies to the JSON of its input (keys sorted).
Common safe reads, allowed without asking anyone:

```json
"policy": {"rules": [
  {"tool": "mcp__github__get_*|mcp__github__list_*|mcp__github__search_*", "decision": "allow"},
  {"tool": "mcp__home__GetLiveContext", "decision": "allow"},
  {"tool": "mcp__notion__notion-search|mcp__notion__notion-fetch", "decision": "allow"},
  {"tool": "mcp__github__create_pull_request", "match": "\"draft\":true", "decision": "allow"},
  {"tool": "mcp__home__*", "decision": "ask", "reason": "agit sur la maison"}
]}
```

Every decision (by the policy or the user) is kept in the `decisions` table.

### Access grid

The server says what each tool is, the user says what each kind of tool may do (`access_grid.go`).
A tool falls in one category by its MCP annotations, with MCP's pessimistic defaults:

| Category | Annotations |
|---|---|
| Read | `readOnlyHint: true` |
| Change | `openWorldHint: false`, `destructiveHint: false` |
| Delete | `openWorldHint: false`, `destructiveHint` true or absent |
| Send | `openWorldHint` true or absent: an unannotated tool |

The annotations are always read. A server not to be believed is set to ask everything ("Tout sur
accord", which its page's shortcut fills), or given an exception per action.

Each category is `free`, `approval` or `never`:

| Value | Brain | Task |
|---|---|---|
| `never` | not listed, cannot be lent | not in its `tools/list`; the proxy refuses it, ticket or not |
| `free` | called without asking | called without asking |
| `approval` | a card per call | a card once per task, category and account, granted until it ends |

The grid is read at each call: a change applies at once, running tasks included. A row left out has
the default: Read free, the rest on approval. A level [below](#access-levels) reads as a grid until
a server has one of its own. Every tool's **Edit** opens its page (Settings → Tools,
`/settings/tools/{name}/access`, `?account=` for an account's): the grid, shortcuts that fill it
(Lecture seule, Tout sur accord, Tout libre), the collapsed panel of exceptions, its credential,
whether what it returns is trusted (the [one taint rule](#tool-catalog)), its allowed actions, and a link to its connection (its form, or the page of Google, Drive, Home
Assistant or GitHub). A tool of the configuration file shows its grid, changed in the file. In the
file:

```json
"mail": {"url": "…", "use": "…",
  "grid": {"read": "free", "change": "free", "delete": "approval", "send": "never"},
  "exceptions": {"create_draft": "free"},
  "account_grids": {"sam@example.org": {"grid": {"send": "approval"}, "exceptions": {"move": "never"}}}}
```

`exceptions` replace a tool's category value, Never included. A name with a `*` is a pattern
(`"agents_*": "never"`) for the tools it matches that have no exception of their own, the longest
first; a preset can start a new tool with some (ElevenLabs leaves its voice agent tools,
`agents_*`, at Never). `account_grids` is one
grid per account, in the place of `account_access`, and a server's exceptions hold for its accounts.
Once one account has an access of its own,
the others are written down with the one they had: the server's no longer speaks for them.
A call that names no account takes the strictest of their values, and is refused when they differ,
unless it is a read none of them refuses.

**Free needs a clean start.** Free is the user's delegation, within which only the model's judgment
holds; a hard guarantee is set in the grid. One guard stays, against prompt injection: on a tainted
turn every Free above Read counts as On approval, for the brain until the user's next message
starts a clean turn, for a task if it was started on one (or by the brain alone, or was lent the
tool held back). A task started cleanly keeps its Free writes even after reading external content.
A read of the open web (a read tool whose `openWorldHint` is not `false`, such as a fetch or a web
search) of a server the user does not trust counts as above Read here, as `WebFetch` does: on a
tainted turn it waits for the user. A trusted server's reads stay free, the web's too.
Only the brain's Free reads of closed sources are allowed in advance and called without a ticket;
its writes and its reads of the open web go through the policy, so the turn's taint is always seen.

**Tool definitions are pinned.** The first listing of a catalog server pins each tool's definition
(its name, title, description, schemas and annotations): adding the server is the user's yes. A
tool listed later with another definition, or a new tool, is held: not lent, not shown to any agent,
not sorted by the grid, refused if called by name, until the user accepts it. A pin keeps the
definition it was made of, so a change shows what was before.

The changes wait like any other item, in **« Outils à revoir »**, a standing node code makes at
start when there is none (again if it was archived; `tool_review.go`). Its block lists
`eriphe-catalog/catalog_changes` (each change: `id`, server, tool, kind `new`, `changed` or
`removed` — a pinned tool its connected server no longer lists —, the description, category and
parameters before and now); each change waiting gets a card of its own on its row, made by code at
each read of the list (`reviewCards`: a calls card of one `catalog_accept` call on that id, its
label the change), withdrawn (`gone`) once the change no longer waits. Its Oui accepts the change:
the tool is pinned with exactly the definition shown (a change's id carries its hash; a
tool changed again is a new change, and an id no longer waiting does nothing); a dropped tool's
pin is forgotten. Its ✕ refuses it (`settleChanges`, at the next read): the change no longer waits,
the tool stays held (or its pin stays); a later definition is a new change. The node waits for the
user while a card does, as any node. It is read again whenever what a server lists changes, after
each card, and every 5 minutes. The block reads a
server not trusted: it is external. No message tells the brain.

`eriphe-catalog` is a server Eriphe provides in the pool, lent to no agent (no grant may name it):
`catalog_changes` is a free read that only code calls (a block's source); `catalog_accept` is
`never` to every agent, the brain, a task or a skill, whatever was allowed before, and runs only
on the user's yes on the card code made (`catalogCall`: a gesture tool runs on a card's yes, logged
in the journal as the user's). There is no refuse tool: a no is the card's ✕. The server's page in Réglages keeps "Accepter ces outils"
(`POST /settings/tools/{name}/approve`): it accepts all the server's held tools at once, refused
ones included. A server cannot reword a description or turn a send into a read behind the user's
back. Removing the server forgets its pins and its refusals. Eriphe's own servers are not pinned:
those it provides (folders) and those shipped with it (run from its images: mail, calendar, files,
Signal), whose tools change with Eriphe's updates, which the user already chooses (`tool_pins.go`).

### The approval card

An approval is a card of the conversation (`access_card.go`), answered by the user, never by the
brain. A start's card also takes a plain yes or no read by eriphe-core, unless it may carry someone
else's words (a tainted turn). The others take their buttons only: they come at any moment, so a
yes may answer something else. A card never holds the brain: its call is answered at once as not
run, the card keeps the exact input, and a yes runs it from eriphe-core, however late and even after
the brain restarted, if the tool is still allowed; the result, or a no, comes back as an event. The
same call asked again is the same card. A call that names what it acts on by a number (a pull
request) adds what is behind it (its title, the start of its description), read once when the card
is made, with the read its tool declares (`card_context.go`, `_meta` `eriphe/reads`: the reads to
try, the call's arguments they take, the fields shown and their names in fr and en).

**A call in plain words** (`call_words.go`), the brain's (`access`) as a skill run's (`calls`,
`runcalls`): a sentence of code's says what happens to what, never a question and never a model's
words: « Mettre 2 mails à la corbeille ». Core has no word of its own for a tool (#727): the
sentence is the one the tool declares (below), else its title (`Title` or `annotations.title`; our
servers give every tool one) with its items counted (« Ranger · 2 éléments »), else « Utiliser
« tool » »; the items are of the kind the tool declares (`items`), else « élément(s) ». The
items it acts on follow by their title and detail as the node's sources list them (`callRows`,
`itemLabel`, kept with the call), at most 5 then « et N autres »; a title from a source that is not
a trusted server's keeps its « ⚠ ». Where it acts goes apart, quietly (`context`: « compte
Gmail », the account by its provider when it is the only one, « le dossier Clients »). Under
« détails », its parameters by their meaning (`callDetails`: named by the input schema's title,
else `card.param.*`, else their own name; valued by the tool's declared words, a date in words),
never an id (`id`, `ids`, `…_id`, or a value one of its items' ids) nor the account; then its exact
call (`server/tool` and every parameter, raw ids included, field `raw`) one more click away
(« Appel exact »). No line of what Oui does. A channel gets the sentence and where it acts. Its
origin (a skill's run: `why`) is a quiet line beside where it comes from, never its subtitle, and
that origin names an item by its title, never its id (`card.from.item`).

**A tool that says its own call** (`call_says.go`). Core knows no tool: a server declares how one
call of its tool reads in its `_meta`, under `eriphe/says`, and code fills it in the user's
language. Our mail, calendar and files servers declare theirs; core's own `catalog_accept` too.

```json
"eriphe/says": {
  "text":   {"fr": "{response} {item}[ : « {comment} »]", "en": "{response} {item}[: “{comment}”]"},
  "item":   {"fr": "l'invitation", "en": "the invitation"},
  "items":  {"fr": ["1 invitation", "{n} invitations"], "en": ["1 invitation", "{n} invitations"]},
  "done":   {"fr": ["1 répondue", "{n} répondues"], "en": ["1 answered", "{n} answered"]},
  "values": {"response": {"accept": {"fr": "Accepter", "en": "Accept"}, "decline": {"fr": "Refuser", "en": "Decline"}}}
}
```

`text` is the sentence by language (English, else any, when the user's is missing). `{item}` is
what the call acts on: one item by its label as its node's source listed it (title, detail, ⚠ when
someone else's words; the label bound to the call counts whatever id it names, an invitation's
event), several counted by `items` (one, then many with `{n}`; their lines below), else the
declared `item` words, else counted as « éléments ». `{count}` is how many it acts on, counted the
same way even when one is known by its label. `done` says what its calls did once they ran
(« 2 déplacés »), else « Fait pour 2 éléments ».
`{name}` is an argument: its words in `values` (`""` for an argument left out; a value is matched
as is, then lowercased), else a date or a time said as one (« mardi 24 novembre, 14 h 10 », a time
with an offset in the user's zone), a list joined, a short text as written; a bool or an id with
no words says nothing. A part in `[ … ]` is said only when every placeholder in it has words. A
tool that declares none is said as above (title, name). « Accepter « Picnic » · Sam,
lundi 14 h 10 », « Créer « Dentiste », mardi 24 novembre, 14 h 10 », « Déplacer « Facture » · Sam
vers les archives ».

A server Eriphe does not write cannot declare: its preset may, in its place (`toolPreset.Meta`,
`presets_meta.go`), what its tools' `_meta` would say, by tool name or pattern (`*`); a key the
tool declares itself wins. GitHub's preset says its pull requests (« Fusionner la pull request
#12 ») and reads one by its number for a card. A guard test (`tool_names_guard_test.go`) fails when
a logic file of core names a tool, one of its tools or their arguments, outside the data files it
lists with their reason, and when a text table keeps a word for a tool's name (`act.verb.trash`).

**On its rows.** A brain's call whose ids are those of rows a node's source shows (the node the
turn is about first, else any live node that read them: `node_reads`) is kept on that node and
those items (`access_asks.node`, `source`, `labels`): `itemCards` gives each row the card's ref,
so the page draws its Oui on every row it names (one card on several rows: a Oui on any answers
it, a mark says how many share it, and hovering lights the others), on a folded batch's line, and
never in the page's foot (`placed`: any told row's card). A calls card is on every item its
calls act on (one call on several items, or several calls folded: `call_fold.go`), an item two
cards act on showing the newest. A call on no row keeps its foot card,
in the same words. Decided, its rows settle; once run, its items are noted `done`
(`itemDoneByCode`: an item is done only by its note) and its sources are read again
(`readNodeLater`, `sourcesChanged`); the brain is
told, as for any access card. Such a card, like a task's access, start or memory card, is linked to
the node its run or task works in (`cardNodeOf`, `node_cards`) and is about it: it waits on that
node's page, which stays waiting while it does, never above the bar.

An access card and a start's card read alone (`card_origin.go`): wherever they are shown (the
conversation, a page, a channel) they say where they come from and what their Oui does,
facts of eriphe-core's only, never words of the model's next to Oui and Non. The node is the one
a calls card's calls belong to (the node whose item it acts on, else the node of the run that kept
them, never one a model names), linked to its page (`/espace?open=node-<id>`); its title always
shows: one that may be someone else's words (`TitleExternal`: an external node's title not `title_trusted`, i.e. not written before outside content reached it nor vouched for by the user or a clean author) is followed by « ⚠ (écrit après une lecture extérieure) » and the card's `fromExternal` draws its line in amber: shown, never replaced, never passing for a trusted one. The task that asks is named when the
card's text does not (a call of a task, a start), marked the same way when it was started on a tainted turn; a calls card names its item and source, marked when the source is not a trusted server's or the calls were kept on a tainted turn.
What Oui does follows the answer's code: a grant lets the task act until it ends, a release gives
it its held tools, a call runs once as shown (a step's, then its node's work goes on), a page or a search
is allowed once, a start starts the task. A task's card is shown:

1. **at launch**, on its start's card, for what it is lent outright: it waits (`held`) for the answer;
2. **on release** of a tool lent [held back](#resources);
3. **at the first call** of a category not granted yet; the calls that wait on it are answered with it.

A card at the first call or on release has a message of its own, the question, which notifies as
any question does; so has a start's card when the brain did not launch the task (a step of a work, a skill's run).
These cards and a start's go to
the channels the user writes from as well (Telegram), whatever the messages around them did,
answered from the linked account only ("Alerts only" sends none): that account is the user, a
trusted channel, so its buttons answer as the workspace's. A task's raw permission (a rule's `ask`,
a command) comes there too, with Allow and Deny. The
answers are kept in `decisions`, and a grant shows in the task's drawer. A no refuses what waits;
the next call asks again.

### Access levels

Each catalog server has an access level (`access`, `access.go`): **what** Eriphe may do on it,
kept apart from **how** its credential is scoped.

| Level | Workspace | Reads | Writes |
|---|---|---|---|
| `read-only` | Lecture seule / Read only | free | not lent: not in `tools/list` of a task nor in `list_tools`, denied by the policy and refused by the proxy |
| `reads` | Lecture libre, écriture sur accord / Free reads, writes on approval | free | on [approval](#the-approval-card) |
| `free` | Tout libre / Everything free | free | free, on a [clean start](#access-grid) |
| `ask` | Tout sur accord / Everything on approval | the user's | the user's |

A read is what the server itself marks `readOnlyHint`. `ask` asks for everything, reads included.
`reads` is the default of the presets; "Other…" (a server whose annotations nobody vouches for)
starts on `ask`, as does an entry without `access`. Whatever the level, the built-in denials come first, then the file's rules.

**Per account.** The mail and the calendar are one server each for every account (`mail`,
`agenda`: [one server per service](#mail-and-calendar-one-server-per-service)), and each account
has its own level, in `account_access` (account name to level): a work mailbox can be read only
while a personal one is free. A call is decided at the level of the account its `account` argument
names (case does not count); one that names none is decided at the server's level, the summary of
its accounts (`ask` if one is, `read-only` or `free` if all are, else `reads`), and a write that
names none is denied when the accounts' levels differ, with a reason that says to name it. The
server holds to it too: eriphe-core marks `read_only` in its configuration the accounts that are,
and the server refuses their writes and an id of another account than the one named.

**Always allow.** A permission card for a catalog tool's action offers "Toujours autoriser" (the
API: `POST /v1/tasks/{id}/answer` with `"allow": true, "always": true`): the request is allowed,
and `server.tool` is allowed without asking from then on, for any input. These are rules kept in
eriphe-core's database (`remembered_actions`: tool, account, server), evaluated after the file's
rules (which win) and never above a built-in denial or a Never (the card does not offer it there).
Each is a Free exception: it follows the clean start rule [above](#access-grid). On a server of accounts, it is allowed for the account the call named only. Each tool's page
([above](#access-grid)) lists them, with "Retirer"; removing a server, or an account, forgets them.

**Scoped credentials.** Where the service can scope its credential, the level closest to the grid
decides what Eriphe asks for: Read only when Change, Delete and Send are all Never and no exception
lets a tool do more.

| Tool | Read only | Otherwise |
|---|---|---|
| OAuth server whose advertised scopes (`scopes_supported` of its protected resource metadata) split into reads and writes by their names: `write`, `delete`, `create`, `edit`, `update`, `manage`, `request`, `generate`… name a write (Linear, Calendly, Dropbox, Evernote, Airtable, Canva, Miro, Sentry, Supabase…; the preset table `tokenScopes` fixes Todoist's `data:read` and Hugging Face's) | the advertised scopes that name no write (`read`, `files.content.read`, `boards:read openid email`) | all the advertised scopes |
| Google Drive | `drive.readonly` | `drive` (when "Permettre à Eriphe d'écrire" is ticked) |
| GitHub tool on Eriphe's GitHub App | installation token with `contents`, `pull_requests`, `issues`, `metadata`, `checks` at `read` | the app's rights |
| IMAP/CalDAV password, Home Assistant token, bearer token, OAuth without a read-only scope (Notion's `default`, Stripe's `mcp`, Fastmail, where the consent screen chooses) | Eriphe holds to the level itself: "Le jeton permet plus : Eriphe s'en tient à la lecture" | |

What an OAuth token was asked for is kept with it (`server.<name>.token-access`); a token from
before the levels counts as full. Going down applies at once (Eriphe enforces it) and the page
offers to reconnect to really shrink the token; going up past what the stored token allows shows
"Reconnecte <outil> pour autoriser l'écriture" with the Connect button (writes would fail with
`insufficient_scope` until then). A GitHub App token follows at once (minted each hour); `scopes`
in the configuration are asked as they are.

## Resources

Everything a task is lent is one list, `resources` of `run_task` (`resources.go`). Each entry is a
kind, a reference to one thing the user opened to Eriphe, and an access on one scale for every kind,
`read < write`: `write` changes the thing (for a repository: push; for a folder: create, change,
rename and delete in it).

```json
"resources": [
  {"kind": "repo", "ref": "me/site", "access": "write", "branch": "eriphe/fix-menu", "base": "main"},
  {"kind": "folder", "ref": "Clients", "access": "write"},
  {"kind": "place", "ref": "Factures"},
  {"kind": "tool", "ref": "github.get_*"}
]
```

| Kind | `ref` | Access | What the task gets |
|---|---|---|---|
| `repo` | `owner/name` on GitHub, or a git URL, a connection reaches (`list_repos`); `branch` (work branch), `base` (start), `dir`; `trust_skills` | `read`, `write` (when one of its cases above Lire is not Never) | cloned into `/work/repo`; [credentials](#github-in-a-task) for it; its skills if it is the user's own or trusted (below); one per task |
| `folder` | a machine's folder, by name (`list_folders`) | `read`, `write` (a folder shared write, its Modifier not Never) | mounted at `/work/folders/<name>`; the task runs on its machine or one keeping it up to date (copies) |
| `place` | a tool's place (a folder of a files tool, such as Google Drive's), by name | `read`, `write` (when one of its cases above Lire is not Never) | its tool whole (it reaches every place given to Eriphe, each call held to its place's rights) |
| `tool` | a catalog server, or `server.tool` with globs | `read`, `write`; from the tools' annotations when empty, and `read` is refused for tools that may act | relayed through the proxy |

The access defaults to `read` (for a tool, to what its annotations say). Each entry is checked
against what the user opened: an unknown folder or server is refused, and so is a `write` on a
folder shared read-only, or a case the folder's rights set to Never. The list is kept with the task
(`tasks.resources`, JSON), and the device gets its spec fields (`repo`, `folders`…), derived from it.

**A repository's skills** (`.claude/skills`, `.agents/skills`, `.opencode/skills`: instructions
and scripts its agents load by themselves) load only from the user's own repositories
(`repo_skills.go`): with the GitHub App, one it is installed on; without it, one the GitHub tool's
token may push to. Anything else (another host, someone else's repository, GitHub not reachable)
keeps them off, unless the entry says `"trust_skills": true`. The device gets the answer as the
spec's `repo_skills` and turns them off per agent (device/README.md, Agent skills); whatever loads,
a skill never approves a tool by itself.

**Held back** (`"hold": true`, a `tool` entry only): for work the user validates before it acts
(analyse, report, then act on what they approved). The task sees the tool from the start (its
agent lists its tools once, and the device its servers when it fetches the task), but none of its
calls is authorized, whatever the grid or a grant says (`lend.go`). The brain asks with `answer_task`
`release` (the entries' `ref`, `mail.move`), for an entry lent held only: the user gets a
[card](#the-approval-card), and only their yes releases it. The release is kept in the task's
resources, what it needs is granted, it is logged in `decisions` (`release mail.move`), and the
brain is told the answer. A task that calls a held tool before that waits on the same card,
raised by code at its call (`askHeldCall`, one card per release however many calls wait on it):
a yes lets its calls go, a no refuses them. An entry lent outright beside a held one wins.

**On a tainted turn** (the brain read external content), `run_task` refuses `trust_skills` and a place (its tool is lent whole for now): the brain asks the user and starts the task in the turn of their answer. Reads still
go, and tools, accesses and a GitHub repository for `write`: what they may do is asked on a card at
launch, Free included.

## Skills

A skill ([agentskills.io](https://agentskills.io/specification)) is a folder with a `SKILL.md`
(its `name` and `description`, then instructions) and maybe `scripts/`, `references/` and
`assets/`. The user's skills live in eriphe-core (`skills.go`), and are managed in **Settings →
Skills** (`web_skills.go`):

- **Adding one**: a folder or a `.zip`, read in the browser and sent as files. eriphe-core checks
  the paths (none out of the skill, no link), the limits (200 files, 2 MB each, 8 MB in all), and
  the `SKILL.md`'s front matter. It drops the folder around the skill and the system's junk
  (`.DS_Store`, `__MACOSX`).
- **Storage**: each version is an immutable bundle, a tar of its files in a fixed form (sorted, no
  dates or owners), kept in the database (`skill_bundles`) and named by its **sha256**. A skill
  (`skills`) points at the version approved and at a version waiting for approval.
- **Approving**: nothing reaches a task or the brain before the user has read the skill on its page
  and approved it. The page shows its `SKILL.md` and its files, with the scripts marked (in
  `scripts/`, executable, a script's extension, or not text). A skill's `allowed-tools` is shown
  as ignored: Eriphe's permissions stay the only judge (device/README.md, Agent skills).
- **A new version** (the same name, uploaded again) waits for approval with its diff against the
  approved one, file by file. Tasks keep the approved version until the user approves the new one
  (the hash they read is checked) or keeps the old one.
- **On or off**, and **delete**. A skill the code turned off says why on its page and in the
  list (`skills.off_why`); approving a new version turns it back on. A version that no longer
  reads (its front matter refused) is shown still, with the reason. Bundles no skill points at any more are deleted, but for the last five versions a skill replaced or had when deleted (`skill_kept`), which the [learning journal](#what-eriphe-learned) needs to undo a change; they are never given to a task.

**In tasks.** Every worker task gets the skills that are approved and on when it starts, as its
spec's `skills` (name and sha256 of each, `skills_tasks.go`). Its device fetches each bundle with its
own secret (`GET /v1/device/tasks/{id}/skills/{sha256}`: only that task's, only on its device), checks
the hash, and mounts them read-only at `/run/eriphe/skills/.claude/skills/<name>`. It points
Claude Code, OpenCode and Codex at them. Scripts run in the task's container, under its permissions,
like any other command. When the agent does not load them itself, eriphe-core lists them (name,
description and path) in the task's instructions: an ACP agent, or Claude Code and Codex in a task
on someone else's repository, where every skill is off.

**For the brain**, skills are read, never loaded: `list_skills` and `read_skill` return their
text (not their scripts), and a skill's scripts run in a task. Nothing of a skill enters the brain's
privileged, long-lived container, whatever its runtime. A skill is the user's own, approved by them:
reading one does not taint the turn.

## GitHub in a task

A task lent a GitHub repository uses the real `git` and the real `gh`, which the agent installs with
apt. It holds a placeholder in `GH_TOKEN`: gh reads it, and git gets it from a fixed credential
helper in the task's environment. Each request crosses the device's relay, as for any
[access](../device/README.md#accesses), and eriphe-core decides and puts the token in.

**The decision** (`decideRepo`, deny by default; first that applies):

| | Answer |
|---|---|
| another repository than the one its `repo` [resource](#resources) covers | deny |
| a read of it | allow |
| a write (git's push, gh's pull request) to a repository lent for `read` | deny, at once: a push is not lent |
| a write whose case (below) is Never | deny |
| a write whose case is Free, on a clean start (the user's own clean turn) | allow |
| any other write: its case on approval, or a start that is not clean | the user's yes for that case on the start's card, else deny |

**A repository's cases.** What Eriphe may do on a repository is one rule for the GitHub tool and for
git: the tool's grid (Lire · Modifier · Supprimer · Envoyer) is every repository's default, and a
repository may set a case of its own, above or below it, on Settings → Tools → GitHub →
"Dépôts" (the tool's `place_grids`, by `owner/name`; its `place_args` say a call names its
repository by `owner` and `repo`): each repository a line with the summary of its own cases,
its name opening its [grid in a drawer](#a-places-grid) (`GET /settings/connections/{id}/places/grid?id=`,
`POST …/places/case`). A tool that only reads is its repositories' ceiling. In git, the
relay reads the commands of a push (`pushCommands`, the pkt-lines before the pack) and decides each
ref: a deletion is Supprimer, the default branch (asked of GitHub, kept ten minutes) or a tag is
Envoyer, any other branch Modifier. One ref refused refuses the push, before GitHub sees it. gh's
writes are Modifier.

What is decided is the lend, at launch, as for an [access](../device/README.md#accesses): what the task reads afterwards
changes nothing, and nothing waits in a command. The same rules hold with the GitHub App or without.

gh's token writes pull requests and issues, never contents: GitHub itself refuses it a merge, a
release, a repository's settings or its secrets. A task that must merge is lent the catalog's GitHub
tool, under its grid.

**No token in the task.** The token is minted here and goes in on the way out (`git_access.go`).
gh's requests, to `api.github.com`, are not classified (much of it is one `POST /graphql`). It works on the one repository
the task is lent: a REST path naming another is refused, and its token is for that repository, with
write rights if a write is allowed, otherwise read rights, so GitHub refuses the write. A read with no token to be had (a public repository the App is not on)
goes without one. git and gh need the GitHub App: no other token is ever lent, and gh's requests
are not classified:

```
git → the device's relay → eriphe-core (decides, mints) → GitHub
```

- **Each request is decided**, from what it is: a push is `git-receive-pack`, and a placeholder
  proves nothing. A refusal
  is a 403 with its reason in plain text, which git shows as `remote: …`.
- **What is pushed is not read**, only which refs it updates: what is decided is the lend and the
  repository's cases. The `branch` lent is what the task is told to push to, and the real limit is
  the provider's: a token for that one repository, and the repository's protection rules on GitHub.
  The shell rules (policy.go) stop a forced push by mistake, not a script.
- Other requests to `github.com` go as they are, with no token: a public clone, a release's
  download. So they cross eriphe-core too, and a program that refuses the task's certificate fails
  on `github.com`. Large File Storage is a write when the task may write there, else a read.

**Asked at launch.** What a task started by `run_task` needs of the user is foreseen when it starts,
with the same decision (`startNeeds`, `task_start.go`): for each GitHub repository lent for `write`,
the case its lend names: Modifier for its `branch`, Envoyer without one (it pushes the default
branch). Supprimer is not asked ahead: a deletion goes only where it is Free, on a clean start. When that takes the user's yes, the task does not start: it is kept `pending` with
`desired` `held` (no device is given it, no placement moves it, its `pending_reason` says why) and the
user gets one card in the conversation (kind `taskstart`, [conversation cards](#workspace-web)),
« Pour pousser `eriphe/x` sur owner/repo, il me faut ton accord. ». Oui keeps the approval
(`repo_grants`, logged "approved at launch": that case of that repository, by git or gh, for the
rest of the task) and lets the task go; Non stops it and tells the brain. Like every card, only the
user's click or a plain yes or no of theirs decides it, never the brain (`answer_task` has nothing
to answer on a held task; `stop_task` cancels it).

**The credential.** With [the GitHub App](#the-github-app), an installation token restricted to the
one repository and the rights asked for, valid one hour: git reads `contents: read` or `write`; gh
reads `contents`, `pull_requests`, `issues`, `checks`, or writes `pull_requests` and `issues`
(never `contents: write`: a merge is not a task's). eriphe-core reuses a token for the same task and
rights until ten minutes before it ends. Without the app, git and gh get nothing (the GitHub
tool's own token is never lent), and the Tools page says to create the app.

**The repositories reached.** A task is lent only a repository a GitHub tool on the app's
connection of its owner reaches (`connection_places.go`): every repository of the installation, one
added on GitHub at once. A lend asks write only when one of its cases above Lire is not Never
(`resolveResources`), a write still goes through its card at launch, and the brain reads them with
`list_repos`. A connection removed takes its repositories with it.

**The log.** Every request is kept (`access_log`: time, task, tool, host, target, action,
decision, by whom, source, reason), and shown in the task's details ("Accès demandés") and by `GET /v1/tasks/{id}/access-log`. Approvals are in
`repo_grants`.

**What it does not stop.** What is lent is usable: a task talked into something by a page it read
can push to the branch it was lent, and the limit is then the token and the repository's protection
rules on GitHub.

### The GitHub App

Each user creates **their own** GitHub App, in one click, from Settings → Tools → GitHub →
"Créer l'app GitHub d'Eriphe" (GitHub's manifest flow, `web_github.go`):

1. eriphe-core writes the manifest: name "Eriphe – <suffix>" (the user's name by default),
   permissions `contents` write, `pull_requests` write, `issues` write, `metadata` read, `checks`
   read, no webhook (`hook_attributes.active` false), `public` false unless "Installable sur mes
   organisations" is ticked (`public` true: GitHub installs a private app on its owner's account
   only; a public one, other accounts could install on their own repositories, which gives them
   nothing on the user's, and its key stays in the vault), `redirect_url`
   `https://<workspace>/github-app/callback`, `setup_url` `https://<workspace>/github-app/installed`
   (both lead the browser back to the page: GitHub's redirect does not carry the session cookie,
   which is SameSite=Strict);
2. the browser posts it to `github.com/settings/apps/new` (or an organization's
   `/organizations/<org>/settings/apps/new`) with a state bound to it (the OAuth flow's cookie,
   `__Host-eriphe-oauth`, SameSite=Lax; the page lets its form go to github.com);
3. GitHub redirects the browser back with a code: the redirect is the browser's, so a workspace
   on the LAN works, no public address or bounce is needed. eriphe-core checks the state and the
   cookie (once, within an hour), exchanges the code (`POST /app-manifests/{code}/conversions`)
   for the app's id, slug, private key, webhook secret and client, and keeps them in the vault as
   one secret, `github-app`;
4. the page then installs the app on the repositories Eriphe may work on
   (`github.com/apps/<slug>/installations/new`) and shows where it is installed
   (`GET /app/installations`): each account or organization, and how many repositories when the
   installation has selected ones (`GET /installation/repositories`, with a token that reads
   metadata only). "Installer sur un autre compte ou une organisation" opens the same GitHub page;
   on an organization, one of its owners approves. A private app says it installs on its owner
   only, and how to make it public on GitHub.

Minting signs a JWT with the app's key (RS256, nine minutes), finds the installation of the
repository (`GET /repos/{owner}/{repo}/installation`) and asks
`POST /app/installations/{id}/access_tokens` with `repositories` and `permissions`. Git
credentials of tasks always find the installation per repository, whatever the connections.

**GitHub connections on the app.** Each account or organization the app is installed on is a
[connection](#connections) of the provider `github`, which mints its tokens instead of signing in:
it keeps no secret (the app's key is the app's), and its check asks GitHub whether the app is
still installed there (no longer: lapsed, its tools held). A catalog tool on it is
`{"auth": {"connection": "<id>"}}` (`ServerAuth.Connection`; the pool's `connToken`): its tokens
come from that installation, with its repositories and rights (reading only for a read-only
tool), renewed five minutes before they end, cached per account, and dropped once GitHub refuses
one. One name is one connection with its own rights, so `github` can use the user's
installation and `github-acme` the organization's. The page lists the GitHub connections with
their account, offers a connection for each installation that has none (a name suggested:
`github`, `github-<account>`), and a "Passer à l'app" for each one on a token, with the account
to use. A connection switched keeps its name, so its level, grants and actions allowed; its token
leaves the vault with the change (the vault's orphan secrets are dropped), and the page says to
revoke it on GitHub (`github.com/settings/personal-access-tokens`). A tool of the configuration
file is changed in the file. "Oublier l'app" removes it from the vault (it stays on GitHub).

**A connection by itself, on installing.** Installing the app on an account is the user's choice of
that account and its repositories, so it needs no second click: GitHub sends the browser back to
`setup_url` with the `installation_id`, which goes on to the page (`?installation=`), and that
installation gets its connection there (`connectInstallation`, `web_github.go`). Only an
installation of the app, made in the last 15 minutes and used by no connection: one installed
before, or whose connection the user removed since, keeps the "Ajouter la connexion" button. A
GitHub connection on a token named as this account's would be (`github` for the app owner,
`github-<account>` otherwise) moves to the app, keeping its name and rights (the page then says to
revoke its token); otherwise a new one comes, named as suggested, at the presets' level (free reads,
writes on approval). The page then says which (`?added=`, `?switched=`); reloading it does nothing
again.

## Triggered skills

A skill whose `SKILL.md` has a `when` runs by itself (`skill_triggers.go`, `skill_runs.go`), as a
task in the background, never as a turn of the conversation: the brain is not woken and its
turn is not tainted. Its front matter:

| Key | |
|---|---|
| `when` | an event kind of the [journal](#events) (`monitor.new key=mails`, `monitor.changed key=getout-prod`), with filters on its key or payload (`monitor.received key=getout status=firing`), or a payload number compared (`user.returned away_minutes>=180`, `<=` too); or up to 5 kinds, by commas, that share the filters (`monitor.failed, monitor.recovered key=getout-prod`: one skill for one way of reacting, its run says each event's kind); or `time` and a schedule (`time weekdays 08:00`, `time every 2h 08:00-20:00`, as rules' schedules); or both, the schedule then `;` and the events (`time daily 06:00; user.returned away_minutes>=180`): it runs at its times and on its events |
| `every` | batching, 5m to 1d: the period starts with the first event waiting; then one task gets all of them. Without it, at once |
| `model` | the agent of its tasks (Settings → Agents); the default one otherwise |
| `tools` | the catalog servers its tasks are lent (10 at most); each call still goes through the grid |
| `repos` | the repositories its runs' steps may borrow, `owner/name`, by commas or a list (10 at most): a clean run's step on them starts without a card ([Work in steps](#work-in-steps-node_stepsgo)); a run itself is not lent them |
| `node` | with `when`: the id of a standing node its runs work in (a view's, [Views](#views-node_livego-view_cardgo)): each run's task is that node's, reads and writes it, notes its items and opens its children; the node is never done nor given a run's report. A node that is gone refuses the run. Without it, each run works in a node of its own |
| `urgent` | a fixed message, 160 characters at most, no link: sent at any hour when a run weighs something 5 |

The keys are checked when a version is added: a bad trigger refuses it. A trigger is approved with
the rest of the skill, as one version: Settings → Skills shows it in words, and a change is a new
version, read with its diff.

- **A run** is a worker task with the skill's description and instructions, then its events
  (kind, key, time, payload), and nothing of the conversation. It is lent the skill's tools with
  no start card: the user approved the skill. An external event taints it from its start
  (`task_taint:<id>`), so its writes ask a card as a tainted task's do; a time trigger or own
  events start it clean.
- **It works in a node of its own**, as any worker task
  ([a task's node](#a-tasks-node-task_nodego-task_nodesgo)): it writes there what it found and
  notes the items of its sources (`item_note`), with the user's preferences about what they see
  in its prompt (`seePreferences`), and the catalog's tools beside its node's. A view's skill works in the view's standing node
  ([views](#views-node_livego-view_cardgo)).
- **Its last message** is optional, the run's report, weighed by its receipt's
  [attention signal](#attention-signals-attention_signalgo) (`tellRun`), the same 1-to-5 scale
  as everywhere; a skill's instructions write no prefix. Entries whose refs are all nodes
  raise them on the graph and never tell the message. No entry (or no receipt) tells nothing:
  the run is `silent`, its message kept in its note. Otherwise its message is a notification, why
  « Skill « name » », ref `skill:<name>`, its link-free rules when the task is tainted, weighed as
  its heaviest entry (delivered at once from 4, pushed at 5). One entry without refs (or whose refs
  were all bad: a bad ref never silences a weight) is about the message, the notification it
  became (`notification:<id>`). Entries about objects alone tell the message only from 4, their
  items coming with it (no heavier, delivered with it: one message); below, their items speak
  alone in the queue, not twice. The run's note lists
  what was dropped.
  Then its task stops. The brain is not told of its state and never woken by a run; its
  permissions are the user's, as any task's.
- **One run open per skill and key**: the key's new events wait, and when its turn ends, its task
  gets them as a message instead of a second task.
- **Time triggers** run at their times, in the user's zone; a trigger seen for the first time (or
  approved again) waits for its next time; times missed while eriphe-core was down make one run.
- **Its calls the user must approve never hold it** (`skill_cards.go`): as the brain's, each is
  kept with its exact input (`access_asks.run`), and the task is told it did not run and goes on.
  When the run ends, they show on one card (kind `runcalls`), attached to its message (sent now
  even without an attention entry; « the skill proposes N actions » when it wrote none, or when
  its message is held or not sent, held by the attention queue for its breakpoint): each action with its parameters. The yes runs
  them all, exactly as kept, the no none; their results go to the run's note, not to the brain.
- **Its calls on an item are that item's card** (#575, #631, `item_cards.go`): a kept call whose
  input names exactly one item a source of its node lists, of the call's own server (a value of its
  input equal to the item's id; code matches it, the run never says it), carries the item
  (`access_asks.node`, `source`, `item`) and is on that item's card (kind `calls`, `batch` = its
  first call's id), not on the run's: when the run ends, each item gets its card, with its taint.
  The same call on the same item still waiting, from any run, is the card already there. Its words
  are code's: the action by its tool, its origin the node (unless it is external), Oui running each
  call once as kept; its results go to the run's note and to the node's trace, never to the brain.
- **Its about**: its runs are about their events' key when it names a thing (a monitor's,
  `topic:<name>`, or `kind:slug`; about.go). Their tasks, notifications, cards and signals carry
  it. A run works in its node.
- **Urgent**: with `urgent: <text>`, a run whose attention list weighs something 5 sends that
  fixed text over the quiet hours and the daily cap, once per run, as an urgent rule's. Nothing the
  run read enters it: a tainted run cannot write what wakes the user. Its own message goes as any
  weight 5's, under the rules; without `urgent`, a weight 5 is only that.
- **Limits**: 100 runs a skill in 24 hours (a run past it is kept `refused`), 50 events a run;
  a triggered task's own end never triggers a skill.
- **Tables**: `skill_queue` (events waiting, by skill and key), `skill_runs` (skill, version, key,
  events, task, state `running`, `done`, `silent`, `failed` or `refused`, its last message or why).

**What it did** (`skill_report.go`). Settings → Skills says each approved skill's trigger in words.
A triggered skill's page lists its runs over 30 days, the latest first:
- when it ran, and its state (running, done, nothing to say, failed, not started);
- its events (kind, key, a few words of their subject);
- its task and its node;
- its last message;
- the actions it proposed, each answered there on its own (`POST
  /settings/skills/<name>/calls/<ask>/approve|reject`: a yes runs it as kept);
- whether it told the user, and its cost where the harness gave one;
- whether it read someone else's content.

Above them, the totals: runs, done, silent, failed, cards accepted, refused and waiting, and cost.
While a run is going, the part reloads every 5 seconds (`GET /settings/skills/<name>/runs`). The
brain reads the same with **`skill_runs`** (`name`, `limit`; free). It gets the events' kinds and
keys and the runs' outcomes, but not their text: what a run read stays on its page.

**Written by Eriphe.** The brain's `write_skill` (name, description, instructions, `when`,
`every`, `model`, `tools`, `urgent`, `repos`, `node`) writes a `SKILL.md` and adds it as a version waiting for
approval (source `eriphe`), checked as an upload is (its model an agent, its tools catalog
servers). Its card (kind `skill`) says the trigger in words, the repositories its steps may borrow, the node it works in and the instructions; the yes approves
exactly that version, the no drops it. Written on a tainted turn, the card says so. A triggered
task has no such tool.

## Events

**Four roles, no tool by name.** eriphe-core knows roles, not tools; any tool or system holds one
or several:

| Role | What it is | Examples |
|---|---|---|
| **Tool** (MCP, the catalog) | actions and reads, called by the brain or a skill, under the access grid | Home Assistant, mail, calendar, GitHub, Grafana |
| **Input channel** | where the user talks to Eriphe: the conversation | the workspace, a messaging app (Telegram), Home Assistant's Assist |
| **Output channel** | where Eriphe tells the user | push, a messaging app (Telegram), a speaker through Home Assistant's integration |
| **Event source** | "something happened", which starts triggered skills | a [monitor](#monitors): one that polls a tool that does not tell (Grafana's alerts) or a server's resource (the mail received, `mail://received`), or one that listens on an incoming address any system calls (Grafana, a Home Assistant automation, a shortcut) |

An input channel is **not** an event: what the user writes is the conversation, answered at once
by the brain alone, with the user's authority (a message can be cited to apply a setting, a « oui »
answers a card). An event is everything else, external by default, and starts skills, never the
conversation. What the user does without writing becomes an event only as code derives it
(`user.returned`). Eriphe reads a tool when it needs to know (who is home, the next meeting): no
follower, no state kept in eriphe-core.

The event journal (`event_journal.go`, `event_sources.go`) is where everything that happens is
written, whatever its source, without being interpreted: triggered skills (#275) will subscribe
to it, and the brain reads it with `since` (kind `event`). An event has a kind, a key that
groups the events of one thing, its source, a payload (JSON, 16 KiB at most, each string
cut to 4000 characters: what a skill's run reads of what happened) and whether it is **external**: written by someone else. Whatever reads
an external payload is tainted; `since` names an external event without quoting it unless
`include_external`. Table `event_journal`, kept 30 days; a source past 600 events an hour has
the rest dropped.

| Kind | Source | Key | External |
|---|---|---|---|
| `monitor.changed`, `monitor.new`, `monitor.crossed`, `monitor.failed`, `monitor.recovered`, `monitor.received` | a [monitor](#monitors) (`monitor.received`: an incoming address was called, `status` from the body's top-level `status` when it is a short word: `firing`, `resolved`) | the monitor's name | unless its server is trusted |
| `task.ended` | a worker task's end | the task | no |
| `node.done` | a node set done (`node_set` `done`) | the node's id | no |
| `user.returned` | the user's first activity after an absence of an hour or more; payload `left`, `back`, `away_minutes` | | no |

### Monitors

A monitor (`monitors.go`, #357) **detects, and only that**: it writes events, and a [triggered
skill](#triggered-skills) reacts to them. It never decides, acts or tells the user: no judge, no
action, no notification. That is what the removed rules mixed with detection. What can be
computed is its own (appeared, changed, crossed); what must be understood is a skill's, with a
small model and `every` when the events are many.

eriphe-core calls one tool of the catalog itself, with fixed arguments, or reads one of a catalog
server's MCP resources, every 1 minute to 1 day, outside any task and without a model, and
compares what a path extracts from the result with what it saw:

| Condition | Event | Payload |
|---|---|---|
| `changed`: the value at `path` (the whole result if empty) is not the last one, and a second look 30 s later still finds it is not (back as it was, a flap tells nothing; a value that moves at every look, such as a series with its evaluation time, is still a change: keep such parts out of `path`, which `try_monitor` warns of with `moves_by_itself`) | `monitor.changed` | `value`, `was` |
| `new`: the items of the list at `path` whose `id` was not seen (the last 1000 ids kept) | `monitor.new`, one per item | `id`, `item` |
| `cross`: the number at `path` goes `above` (or `below`) the limit, or back, confirmed the same way: a measure becomes a state, so 47 % then 52 % is nothing. Before its first look the limit counts as not crossed: a number already past it tells | `monitor.crossed` | `value`, `holds` (true past the limit, false back), `above` or `below` |
| three failed looks in a row, then the next good one | `monitor.failed`, `monitor.recovered` | `error` |

The first look is the baseline: it tells nothing. A path is fields joined by dots, an index, `*`
for every item of a list, `[field=value]` to keep some items: `data.rules[state=firing].name`.
Once it went over a list its value is a list, sorted, so the same items in another order are no
change. The result is read as JSON (its structured content, else its text; a resource's text).

**A resource** (`resource`, its URI, instead of `tool`, with no arguments) is what a server offers
to be read rather than called. The server must be in the catalog and list it (`resources/list`,
kept per server by the pool and listed again on `resources/list_changed`); a resource is only
read, so it needs no access policy. The mail server's `mail://received` is the mail just received,
`{"messages": [{"id", "account", "from", "subject", "date"}]}`: a monitor with `cond: new`,
`path: messages`, `id: id` writes one `monitor.new` per message, its sender and subject in the
item, so a skill follows the mail with `when: monitor.new key=mails`. The calendar server's
`calendar://invitations` is the same for the invitations waiting for the user's answer
(`path: invitations`, `id: id`; its title, start, organizer and event id in the item).

- **Keyed by its name**, so a skill follows it with `when: monitor.changed key=getout-prod`. Its
  events are external unless its server is trusted, so the run they start is tainted.
- **Free reads only**: the tool must be read-only by its own annotation, one of the brain's, and
  Free in the user's grid (`freeCall`, with its arguments); a resource must be listed by its
  server. Checked when it is written and before every look: otherwise it waits, with why, and
  nothing is called.
- **The user approves it.** The brain proposes one with `write_monitor` (name, server, tool or
  resource, args, every, cond, path, id, above, below): a card (kind `monitor`) says it in words; the yes turns it on from a new
  baseline, the no drops it. A change of an approved one waits the same way, the old one looking
  meanwhile. Written on a tainted turn, the card says so. A triggered task has no such tool.
  `monitors` (free) lists them: state, what each does, its last look, why it waits, its last error.
  `try_monitor` (free) tries one once, with nothing kept or written: a draft before proposing it,
  or one of the user's by name. It says what the path extracts or why it cannot, and for a monitor
  that has looked, whether a look now would find a change (`changed_since_last_look`, the number of
  new items, or whether the limit holds). A server not trusted taints the turn, as a call of its
  tool would.
- **Its page**, Settings → Surveillance (`web_monitors.go`, `monitors_user.go`): each monitor's
  state (on, paused, waiting with why, failing with its error), what it does in words, its last
  look, its events over 30 days and the skills whose `when` names it; its last 20 events; pause and
  resume (from a new baseline, so what changed meanwhile is not told late), delete (its events
  stay in the journal), and the brain's proposals answered there as on their card. The user adds
  or edits one there: it is theirs, so it looks at once, with no card, and a change the brain
  proposed on it is dropped. The picker offers the free reads, then the servers' resources;
  **Try** calls the tool (or reads the resource) once and shows what the path extracts (ids for `new`, the number for `cross`), nothing kept.
- **Listening: an incoming address** (`incoming_hooks.go`, way `listen`), for a system that can
  tell and reach Eriphe: `POST https://<workspace>/hooks/in/<token>`. Each call writes
  `monitor.received`, keyed by the monitor's name, with the body's `status` when it is a short
  word: `when: monitor.received key=getout status=firing`. The token (32 random bytes) is shown
  once and kept as a hash in the monitor's `hook`; made again under the same name, the old address
  stops working. Signed with an HMAC-SHA256 secret if asked (in the vault, `monitor-hook-<id>`),
  read from the header the user names (`X-Eriphe-Signature` by default; `X-Hub-Signature-256`
  for GitHub, `X-Grafana-Alerting-Signature` for Grafana). Its body (64 KiB) is external
  content; 60 calls an hour at most (429); paused, it answers 503. Only the user makes one, on
  the Monitors page: its token never goes through the conversation, so `write_monitor` proposes
  only polls. The table `alert_hooks` it replaces is dropped at start.
- **Limits**: 50 monitors, arguments of 4 KiB, a value of 4000 characters in an event.
- Table `monitors` (name, state `proposed`, `on` or `paused`, its spec, a change waiting, what its
  looks saw, its next look).

The test case: `getout-prod` calls Grafana's alert rules every 2 minutes with `path`
`data.rules[state=firing].name`; a skill `when: monitor.changed key=getout-prod` investigates in its node,
cutting its work in steps when it needs several. Nothing runs a model while nothing fires, and nothing reaches Eriphe from
outside: the call goes out from eriphe-core to the tool.

## Assistants

An assistant is a long-lived agent that owns one task, as a Kubernetes Deployment owns its Pod.
One is built in, and there are no others: **the brain** (`eriphe`, role `brain`: the only voice
and the only orchestrator, see [The brain](#the-brain)). Work of several steps has no assistant
of its own: code runs its steps ([Work in steps](#work-in-steps-node_stepsgo)). What used to be a specialist assistant (role `assistant`, with a typed
state, views, lent facts and a team page) is a [triggered skill](#triggered-skills) now: a task in
the background, started by an event or a time, that tells the user with its last message.

Their spec says what they are and what they run on; their status is what eriphe-core keeps of
their task:

| Spec | |
|---|---|
| `role` | `brain`, with the powers it allows |
| `display_name` | what the user calls the brain, as a person (see [its name](#the-assistants-name)); Eriphe by default |
| `agent`, `device` | what it runs on; without an agent, the default one or the first that can run it |
| `instructions` | its standing instructions (`{{user}}` and `{{name}}` replaced); its role's built-in ones by default; `instructions_file` in the file only |
| `tools` | its grant from the catalog, like a task's; its role may add the catalog's free reads |
| `powers` | what it may do, within its role's: `talksToUser`, `receivesConversation`, `launchesTasks`, `lendsResources`, `notifies` (the brain's) |
| `triggers` | the brain's: `conversation` (the user's messages and the tasks' events) and `clock` (an intention that comes due: the wake packet, see [Memory](#memory)) |
| `disabled` | its task is stopped, and none started |
| `self_url` | the file only (see the brain) |


### The assistant's name

Eriphe is the software's name; each user names their own assistant. The brain's `display_name`
(1 to 32 letters, digits, spaces, apostrophes or dashes) is asked by the first-run wizard (an
empty field: a new install chooses), and set
at the top of Settings → Agents, with `PUT /v1/assistants/eriphe` (`{"display_name": "Jarvis"}`)
or with `brain.display_name` in the file (then read-only). Its id stays `eriphe`. An install that
never named it keeps Eriphe, with the same fingerprint: the upgrade restarts nothing.

The name is used wherever the assistant is a person: its instructions (`{{name}}` in
`brain-prompt.md`, as `{{user}}`), the workers' standing instruction (their answer is relayed as
its own), the memory's nightly extraction, the conversation's messages about it, the
workspace's words (`{{name}}` in the texts of `web_text*.go`, French elision included: « Tâches
de {{name}} » reads « Tâches d'Eriphe »), the pages' title suffix and the brand next to the logo,
the login page, the installed app (`/manifest.webmanifest` is served with its `name` and
`short_name`), and an added channel's app (`GET /v1/channel`, Home Assistant's conversation agent). The software keeps its name: the logo glyph, the
images and binaries, the install scripts, eriphe-core and eriphe-device, the GitHub and Google apps
and the OAuth client, the certificate authority, the app passwords and tokens named Eriphe, the
Home Assistant integration's domain, and the system texts that name the software.

## The brain

With a `brain` section in the config, eriphe-core keeps a permanent orchestrator alive: a long-lived
task on a device (stable `profile`, so its session survives restarts), with only MCP tools
(eriphe-core's own plus its grant from the catalog, `brain.tools`) and its standing instructions:
`brain-prompt.md`, built into the binary, or the file `prompt_file` names instead.

**Its fixed context** (#706) is what it reads before any work: the standing prompt (in French,
at most 35,000 characters), its tools as the MCP client lists them (at most 45 KB of JSON for the
46 tools) and their instructions (`tools.go`, at most 5 KB, the concepts' map with them). A test
(`brain_size_test.go`) prints and bounds the three. What keeps them small: a rule the code
enforces is code, not a sentence (a home call's trace, a subject's title after an external read, a
heading that repeats the title, dropped by `AuthorBody`); a shape is said once, in one tool's
schema, the others naming it (`sharedShapes`: run_task's contract and resources are node_open's
steps', node_settle's steps node_open's, propose_view's monitor and skill write_monitor's and
write_skill's), and no tool has an output schema; a block's grammar is `catalog_read`'s. The
prompt says how to work, once each: what the code does is the truth, and its behaviour is
measured by the live bench (`nodes_bench_live_test.go`), not by phrases.

**How it reaches eriphe-core.** Without `self_url`, the brain's device relays eriphe-core's own MCP
server to it as it relays a catalog server (`<callback-url>/mcp/eriphe-core`, with the task's
secret; the device calls `/v1/device/tasks/{id}/mcp/eriphe-core` with its own): it works wherever the
device reaches eriphe-core (the same machine, the LAN, the tailnet), with no address to configure,
and the brain's container never holds eriphe-core's API token. Only the brain's task gets it.
`self_url` (e.g. `http://eriphe-core:8800/mcp` when the brain shares a Docker network with
eriphe-core) still makes the brain call that address directly, with a token of its own derived
from the API token (`eat_…`): it opens `/mcp` only, never the rest of the API (folders and their
listing, machines, assistants, settings).

**Its tools, upfront.** eriphe-core's own server is loaded upfront (`alwaysLoad`, either way the
brain reaches it): a harness that defers MCP tools behind a search (Claude Code's tool search)
would otherwise have the brain look for `run_task` or `recall` before their first call, and see
only their names, not the descriptions its concepts rely on. The catalog's servers stay deferred
unless `always_load`. Of its harness's own tools the brain keeps only the web and that search
(`HarnessTools` of its role's card: `WebSearch`, `WebFetch`, `ToolSearch`, Claude Code's
`--tools`); the rest (Agent, Bash, Edit, Read…) it never needs.

**It reads by itself.** The brain is lent, with no configuration, every tool of the catalog that is
not [Never](#access-grid), through its proxy like any task. Its Free reads of closed sources
(not of the open web: `openWorldHint` false) are allowed in advance
in its assignment (`allowed_tools`) and called without a ticket: no permission round trip, a
decision on record all the same. Everything else goes through the policy: a Free write is allowed
on a clean turn and asked on a [card](#the-approval-card) on a tainted one, and so is a Free read
of the open web; On approval is a card
per call. A read named by a
policy rule of the configuration is not free: it goes through the policy with its input. The list
is computed when its device fetches the task, so a tool or server added later reaches the brain at
its next start (the proxy itself follows the catalog at once); in eriphe-core's first minute, the
brain waits for the trusted servers that are still connecting before it starts.

Its other catalog tools (`brain.tools`) are not allowed in advance: each call goes through the
policy like a worker's, and a call
without its permission is refused by the proxy. What is the user's waits for the user: eriphe-core says so in the conversation, and the brain's
task shows in `GET /v1/tasks` (and the CLI) until the user allows or denies it. The brain's other
permission requests are denied, but for the web.

**The web.** The brain searches with its harness's own search: Claude Code's `WebSearch` is in its
`allowed_tools`; Codex is held to `web_search = "cached"` by the device; OpenCode's `websearch` is
denied (a harness without a search is told so in its standing instructions). Its page reader
(`WebFetch`, OpenCode's `webfetch`) and its search are not allowed in advance: the web is a row of
Settings → Tools, Free, On approval or Never, for the brain only (a task's web is its container's).
On approval is a card for each search and page; a yes lets that one through. Each read is decided by eriphe-core
(`brain_web.go`). A URL that is not http(s), carries credentials, or names this machine or the local
network (localhost, a loopback, private, link-local or CGNAT IP, a name without a dot or under
`.local`, `.lan`, `.internal`, `.home.arpa`…) is refused; on a clean turn the read is allowed; on a tainted turn it is the user's (told in the
conversation), unless the URL is written in the user's own message of that turn. Reads asked
together are judged on the taint before them. Every decision is logged (`brain: WebFetch …`) and
kept in the decisions table. A read, like a search, taints the turn.

It gets one turn per event: my messages (`POST /v1/messages {text}`) and what happens to its workers
(a permission request, the end of a turn, a stop), several at once if they queued during a turn.
`GET /v1/messages?after=N&wait=S` returns the conversation. Restarts back off from 5 s to 5 min, and a
resume that keeps failing is dropped for a fresh session.

eriphe-core, not the harness, holds the conversation: the harness session (`--resume`) is only a cache.
Each resumed call reads the whole history, and a cache left for over an hour is written again in
full: a new brain task resumes its session only while its last turn's context was under 150,000
tokens per call and, above 60,000 tokens, that turn ended less than 55 minutes ago
(`brainResumeTokens`, `brainResumeIdle`, `brainResumeIdleFloor`; the role card's `ResumeTokens`,
`ResumeIdle` and `ResumeIdleFloor`): a smaller session costs little to write again and holds more
than the pack. The measure is the session's
last `turn_end`, whichever task ran it: its `usage.context_tokens` (the device's, Claude Code's last
call), or else the turn's input, cache reads and writes. Past either bound, the task starts a fresh
session. A fresh session (the first one, a dropped resume, a session past its bounds, a resume the
device could not do, another harness) gets a context pack in its first turn, never in its system prompt,
within about 16,000 tokens (`brain_context.go`). The state of things comes first, in at most half of
it: the card of each node running or waiting, up to 20 (`about_card.go`: its line says its
state, title and the first line of its doc), the tasks under way, the decisions of the last 14
days, the people and facts of these nodes, the open intentions (the due ones first), what waits
for the user (permissions, cards), the last day's episodes. A card that does not fit leaves its
node in a line; a record a
card shows (its refs) is not said again in the sections after it; then the last messages between
the user and the brain in what is left, the newest kept.
It is quoted data; what may carry what someone else wrote (an external memory, episode, card or
node) is named, not quoted,
and events are left out, since they carry what tasks said. eriphe-core logs its composition: each
section's lines written out of those it had and its size, the state's and the messages' chars.

## API accesses the user adds (Home Assistant, shortcuts, scripts)

For Eriphe a channel is text in and text out. Besides the built-in ones (the workspace, push,
Telegram: below), the user adds any app that talks with text as an API access, from the catalog of
Settings → Access (`channels_added.go`, `web_access_list.go`): its kind (Home Assistant, or an app
or script; kept with it, for its mark and its help), a name, a token shown once (`ech_…`, only its
hash kept, replaced or removed on its page), and what the user declares of it:

- **trusted or not** (not, by default). Trusted: what comes through it is the user's own words, as
  in the workspace, and a plain yes there answers a card. Not trusted: anyone, or anything, may have
  written it (someone in the room, an automation, a relay). Its message taints the brain's turn
  (`canal:<name>`): the brain reads, answers and proposes freely, but a write goes on a card, and a
  yes sent through the channel never decides one. The answer then says « Je te l'ai mis à confirmer
  sur ton téléphone. » and a notice, in the code's words, goes to the user, opening the card. Its
  turn is not the user's for what that allows (`turnFromUser`), and no queued proposal joins it;
- **what it may say**: nothing, or a sentence the code writes, said in the room the user is in
  (the router's voice line, below).

```
POST /v1/channel      {"text", "language"?, "place"?, "wait"?}   → NDJSON {"delta"} … {"done", "text", "continue"}
GET  /v1/channel      {"name", "channel"}                          the assistant's name, the channel's
GET  /v1/channel/out?wait=50&places=Salon,Bureau  → {"messages": [{"id", "text", "place"}]}
```

The text joins the conversation like any of mine, marked with the channel and its place
(`[Sam 12:00 #N · Home Assistant, Bureau]`, `· canal sans confiance` when not trusted: then it
taints the turn, and is never cited as the user's own words, for a preference, a lesson or the
profile, nor read as theirs by the nightly extraction), and the brain answers it in the same conversation; with a place, short and without markdown, as it is most often
read aloud. The answer streams back from the brain's draft as it writes; only what it says after
reading the text counts. Without a word within `wait` seconds (10 by default, at most 80 for an
automation that sends the answer on), it answers « Je m'en occupe, je te préviens » and the answer
stays in the conversation. There is no other case: an automation is a channel like any other, its
trust what the user set.

The outbound request stays open (55 s at most) and answers as soon as something is to be said; the
app says the places it reaches (`places`; none: anywhere) and maps a place to its own speaker.
What waits more than a minute is dropped (10 at most per channel). eriphe-core never calls the
app. `POST` and `GET /v1/voice` are the same as `/v1/channel`, for Home Assistant's integration
before it moved; Home Assistant's former voice token became the channel "Home Assistant", not
trusted, saying a sentence.

**Home Assistant** (`../ha/`) is such a channel: its Assist pipeline keeps what it understands
(simple commands of the house) and hands the rest to Eriphe with the area of the satellite as the
place; it keeps the outbound request open with the areas that have a satellite or a media player,
and says each sentence on a speaker of its area; with each request it also says the areas where
someone is now (its presence sensors), where the voice line speaks (below). What the house has to
tell (someone came home) goes from an automation to an incoming address, as any system's.

## Google accounts (Gmail, Calendar, Drive)

One sign-in per Google account gives Eriphe any of Gmail, Google Calendar and Google Drive
(`connection_tools.go` with Google's rows of the tools' cards, `tool_cards.go`). Nothing in the workspace is Google's
own: the Google, Gmail, Google Calendar and Google Drive tiles are connection presets
(`Form: "connection"`, `Provider: "google"`) that open the provider's connect page,
`/settings/connections/google` (`?tool=` ticks some; `web_connections.go`); the account is then a
card of the tools list ([above](#connections)). The user's own Google app
([below](#your-own-google-app)) is set up once for every service and every account on its
[app page](#connection-apps), `/settings/apps/google`, whose guide turns the three APIs on with one
link (Gmail, CalDAV, Drive) and gives the scopes and the redirect URI to copy.

| Service | Scope | Server |
|---|---|---|
| `mail` | `https://mail.google.com/` (IMAP and SMTP with XOAUTH2) | eriphe-mail, an account of `mail`, Gmail preset |
| `calendar` | `https://www.googleapis.com/auth/calendar` (CalDAV) | eriphe-calendar, an account of `agenda`, provider `google` |
| `drive` | `…/auth/drive`, or `drive.readonly` for an account kept to reading | eriphe-files, `fichiers`, one account of it per Google account |

**Connect.** The connect page ticks the tools, and "Se connecter" asks Google for `openid email` and their
scopes only, with `access_type=offline`, `prompt=consent`, PKCE, the signed state of
[the Drive flow](#your-own-google-app) and `include_granted_scopes=true`: a service added later to
the same account (its `login_hint`) asks for that one alone, and Google's answer covers what the
account granted before. The address comes from the ID token of the token endpoint's answer (over
TLS, which OpenID Connect takes in place of its signature). The refresh token, with the client id
and secret and where to refresh it (`token_url`), is kept **once per account**: the account is a
[connection](#connections) and its token is in the vault, and each service's server reads it from there in its environment
(`TOOL_TOKEN_<hash>`, [the servers' contract](../servers/README.md)), refreshing its access token itself: the token never enters a task or
a spec. Which servers belong to an account is read from their specs (that vault reference). A
service Google did not grant (its box unticked on Google's page) is said back. Connecting again
writes the new token for every service of the account, and the devices restart them with it.

**Several accounts.** Every account's mailbox is an account of the one `mail`, its calendar an
account of the one `agenda`, its Drive an account of the one `fichiers` with its own grid, each
folder naming its account. The page lists the
accounts, each with its services, their tools and states, "Ajouter" for a service it has not,
"Reconnecter" and "Retirer ce compte" (its servers and Drive folders go, its token leaves the vault
and is revoked at Google, `oauth2.googleapis.com/revoke`).

**To reconnect.** Every 30 minutes (`connectionTick`, with the devices' check) eriphe-core asks
Google for an access token with each account's refresh token ([connections](#connections)). `invalid_grant` (revoked, or the 7 days of
an app in Testing) or a refused client marks the account "À reconnecter": its servers are held in
`needs_auth` (the pool's `authHold`; a server with another working account is not), a call says
why, `list_tools` gives as `connect` where to do it (`Settings → Tools → Google → Reconnect
<address>`), and the brain is told once (an event in the conversation). The Tools list and the
page show the account to reconnect with a one-click "Reconnecter", which asks again for the
services it had. Google out of reach changes nothing. Gmail and Calendar also say "reconnect it in
Eriphe" when Google refuses their refresh.

Gmail with an app password (the Mail form's other Gmail) stays for a workspace Google cannot send
back to (a local address, [below](#addresses-google-takes)).

**In the Tools list.** Each connected account is a card (`web_tools_groups.go`), whatever its
provider: its tools, "Ajouter à ce compte" for a kind of tool its provider serves and it has not
(`toolsAt`), "Reconnecter" when it lapsed, and "Retirer ce compte". Their routes name the provider
as data (`web_connections.go`): `POST /settings/connections/{provider}/connect` signs in for the
tools ticked (`tool=`, several, `write=`, `account=` to add to an account), or reconnects one
(`account=` and `reconnect=1`: the tools it has, or else the ones its scopes serve, with the rights
they had); `POST /settings/connections/{provider}/remove` (`account=`) removes it, as above. A
tool that has places has them on its account's page, as a tree ([places](#places)).

**Providers are data.** No logic file of core names a provider: Google, Gmail and its products
are rows of `signin_providers.go`, presets of `presets.go` and words of the text tables; the variables
core writes name no provider (`TestAccountVariablesNameNoProvider`). `TestNoProviderInLogic` fails when a name or a
string of another file names one (comments may, as examples); "drive" is a kind of tool, like mail
and calendar, not a provider.

### Connection apps

A connection app is the user's own OAuth client at a provider (`connection_apps.go`), made once
and used for every account signed in there. Its page, Settings → Tools → `/settings/apps/<provider>`,
is built from the provider's data (`signin_providers.go`): the steps in the provider's console
(their texts are `app.<provider>.<step>` in the text tables, with a link and, where a step needs
it, the scopes to declare or the redirect URI to paste, each with a Copy button), the client id's
form, whether the console gives a JSON file, and the redirect rule (`public-https`: the page says
when this workspace's address is refused, and what to do). The client goes to the vault as
`app.<provider>.client`. Whether the client lists the redirect URI is not probed: the test says
what to check in the console.

**Test** (`POST /settings/apps/<provider>/test`, `testApp`) checks without the provider's consent,
a line each: the workspace's address (the redirect rule); the client, with a code exchange of a
made-up code (`invalid_grant` is a client the provider knows, `invalid_client` a wrong id or
secret); the redirect URI to check; then each account connected there, its refresh token for an
access token (`checkConnection`, which marks it to reconnect when refused). Secrets and tokens are
never shown nor logged.

### Tools on a connection

A tool says what it needs of an account at a provider as data, the `SignIns` of its card in
`tool_cards.go` (`connection_tools.go`): for its kind (`mail`, `calendar`, `drive`), the
provider, the scopes it reads with and, if they differ, the scopes it writes with, and what its
account is in the tool's configuration (the account's `provider` there, and whether the identity
is also its address or its username). A sign-in asks for the scopes of the tools ticked only
(the write ones when the tool may write and is not set to read only), keeps the connection, then
makes each tool granted read its token: a mailbox or a calendar is an account of the one `mail` or
`agenda`, a drive an account of `fichiers` (its variable `TOOL_TOKEN_<hash>`). A provider that
serves the mail or the calendar is one row more, no code.

### Tool cards

Each local tool eriphe-core launches and configures with several accounts has a **card**
(`tool_cards.go`): its kind, the catalog name it is made under, its image, its preset (words and
icon), whether it runs the image of eriphe-core's version, whether each account has its own
level, whether two accounts of one name are one, whether its configuration holds the scopes
granted, what it needs at each provider (`SignIns`), where it keeps its places (`Places`) and
the workspace's objects it reads, each by a read-only tool (`Objects`: `mail:`, `agenda:`, `event:`).
core reads the cards: a server is matched to its card by its image, and nothing else in core names
an image of Eriphe's tools (`TestNoToolImageInLogic`), a card or a kind by name, or GitHub
(`TestNoToolInLogic`): only the catalog's data and GitHub's own files (the app, its page, and git and
gh through it; `toolData`) do; a provider's page and client are registered from its file
(`providerRoutes`, `providerStarts`), and a tool that goes on an app's connection or a token says so
in its preset (`AppPage`). Another image is another card, no code (`TestAnyToolCard`). What the configuration holds is [the servers' contract](../servers/README.md).

### Connections

A connection is one account signed in at one provider (`connections.go`, the `connections` table):
its provider, its identity (the address), the scopes it granted, the vault name of its token, and
whether it lapsed. There is one per identity, shared by every tool that reads it; which tools do
is read from their specs (the vault reference of the token), never kept twice. Nothing in it names
a provider: the provider's data (`signin_providers.go`) says how to refresh and revoke.

- **Token.** A new connection keeps its token as `connection.<id>.token`; signing in again
  overwrites it under the same name, so no tool's environment changes. A Google account connected
  before connections keeps its `google.<hash>.token`.
- **Check.** `connectionTick` asks each provider, every 30 minutes, for an access token. A refusal
  (`invalid_grant`, or a refused client) lapses the connection: the tools that hold no other
  working account are held in `needs_auth` (`connectionHold`, the pool's `authHold`), and the brain
  is told once, in the provider's name. Signing in again lifts it. A provider out of reach changes
  nothing.
- **Removed.** Removing an account removes its tools first, then its token, revoked at the
  provider. The check also collects the tokens (`connection.*.token`, `google.*.token`) no
  connection and no tool reads.

## Google Drive (cloud Folders)

A folder of the user's Google Drive becomes a Folder given to Eriphe: a name, the cloud as its
source, and its rights, set like any tool's: **Lire · Modifier · Supprimer**, each free, on
approval or never (no Envoyer: nothing of a folder is sent anywhere, so the case is not shown).
Modifier adds a file or replaces one's content (Drive keeps the old versions); Supprimer moves to
the trash, where a file stays 30 days. Each Drive account is a tree: its grid (the card's
`AccountLevels`) is the root, the whole Drive, Never until set (`rootAccess`); a folder follows
its parent (a top folder, the account), and any folder may set a case of its own, above or below:
an action's exception wins, then the deepest folder's case, then its parents', then the account's
(`placeAccess`, `access_grid.go`). A call is held to the folder it names (its `folder` argument,
the card's `Places.Arg`), and the server makes it name the deepest folder kept that holds what it
touches; one naming none takes the strictest of the folders, and must name one when they differ
on a write. With its root above Never, the account is itself a place, named after it (`all` in
its `TOOL_CONFIG` account). Tasks reach it through `eriphe-files` (servers/files), a local server like
eriphe-mail, which **keeps every call inside the folders**: it takes only the folder ids it was
configured with and what is inside them (it walks each id's parents up through the Drive API),
refuses everything else, never touches the folder itself, and keeps an account core marks
`read_only` to reading. eriphe-core runs the
authorization, keeps the refresh token in the vault, and lends it to the service's environment on
its device; it never enters a task or a task's spec.

Settings → Tools → Add → Google Drive (the logic in `cloud.go`, the sign-in in `oauth_client.go`):

1. **Your Google app**: the [app page](#connection-apps), with the client JSON (or id and secret)
   of the user's own Google app ([below](#your-own-google-app)), kept in the vault.
2. **Connect**: the connect page with Drive ticked, the [Google account](#google-accounts-gmail-calendar-drive)
   flow; "Permettre à Eriphe d'écrire" asks for the scope that writes. The account comes back connected, and the
   workspace creates the catalog server `fichiers` (image `ghcr.io/alexis-marcel/eriphe-files`),
   the account at Never: nothing is reached until the user opens it.
3. **Choose**: once the service runs, the account's page is its [tree](#places): the whole
   Drive, then its folders (My Drive, Shared with me, shared drives), browsed through the
   server's `browse_places`, each opening its drawer. A folder with cases of its own is written in
   the service's `TOOL_CONFIG` (`places`, with its `parents`), which restarts it; its cases are the
   server's `place_grids`, by folder id.

`browse_places` is marked `_meta: {"eriphe/setup": true}`: the pool leaves such a tool out of a
server's tools (`setupTool`, `listTools`), so no grant, no task and not the brain can see or call
it; only the workspace calls it (`browsePlaces`, `places.go`).

### Scopes

| Asked | When | Why |
|---|---|---|
| `https://www.googleapis.com/auth/drive.readonly` | "Permettre à Eriphe d'écrire" unticked | list, search and read inside the chosen folders, which can be any folder of the account, shared ones included |
| `https://www.googleapis.com/auth/drive` | "Permettre à Eriphe d'écrire" ticked (the default) | the above, and add, modify and trash files inside a chosen folder |

`drive.file` is not enough: it only reaches files the app created or that the user opened with
Google's Picker, so it can neither list a folder the user already has nor write inside it. Both scopes kept are **restricted** at Google: an app that is not verified shows "Google
hasn't verified this app" (the user goes through "Advanced") and is limited to 100 users, which
is no limit for a user's own app.
`drive` lets the token change anything in the Drive: eriphe-files has no call that reaches
outside the folders, nor one that deletes for good. An account at `drive.readonly` is its
ceiling: its Modifier and Supprimer are greyed, with the reason, on its grid and on each of its
folders, and count as Never (`readCeilings`); nothing is taken out of view. `prompt=consent` and `access_type=offline` make Google give a refresh token at each
connection.

### Your own Google app

There is no shared Eriphe app: each user creates **their own** Google OAuth client once, the Home
Assistant way, and the page guides them through Google's console in a few minutes (the same steps
in [docs/google.md](../docs/google.md)). Nobody else holds its secret, there is no
user cap or verification to share with strangers, and no server of the maintainers is in the path.

The client is a **Web application** client of the user's Google Cloud project (Drive API on, an
External audience with the user as test user, or published), with one authorized redirect URI:
the workspace's own `<origin>/oauth/callback`, where `<origin>` is the address the browser
reaches the workspace at (`web.origin`); the page shows it with a Copy button. Its id and secret
are given on the page, as the JSON the console downloads (`{"web": {"client_id",
"client_secret", "redirect_uris", …}}`, the flat `{"client_id", "client_secret"}` also works) or
typed, and kept in the vault as one secret, `app.google.client` (`PUT /v1/secrets/
app.google.client` sets it too; `google-drive-client`, its name before, is still read until the
app is saved again); the secret is never shown back or logged. It is a
[connection app](#connection-apps). A Desktop client
(`{"installed": …}`) is refused: Google sends it only to loopback redirects. A JSON whose
`redirect_uris` lack this workspace's URI is kept, with a warning.

```
workspace → Google      PKCE (S256), state = e1.<base64url payload>.<base64url HMAC-SHA256>,
                        redirect_uri = <origin>/oauth/callback
Google → workspace      <origin>/oauth/callback?code&state   (in the browser)
workspace               checks the MAC, the expiry (15 min), the flow (used once), the browser's
                        flow cookie, and that the origin is its own; exchanges the code with the
                        client secret and the PKCE verifier
```

The payload is `{"w": "<workspace origin>", "n": "<flow id>", "x": <expiry>}`, the MAC under a key
only this eriphe-core has (`cloud_state_key` in the store, made once): a state it did not make, or
replayed, is refused at its callback. The flow is bound to the browser like the other workspace
flows (the `__Host-eriphe-oauth` cookie, its hash kept with the flow): another browser with the
same link is refused. This flow is not Google's own: `oauth_client.go` runs it for any provider
given as data (`signin_providers.go`: endpoints, revocation, extra authorization parameters, what
a client id looks like, which redirects it takes); Google is its first entry. The refresh token
goes to the vault with the client id and secret, once per account (`google.<hash>.token`), which
the servers need to refresh it.

### Addresses Google takes

Google checks an authorized redirect URI against its
[validation rules](https://developers.google.com/identity/protocols/oauth2/web-server#uri-validation):
https (http only for localhost), no raw IP address (loopback ones aside), and a host whose
top-level domain is on the public suffix list. The provider's `public-https` redirect rule applies them to the workspace's
origin (`publicsuffix`, ICANN section; `home.arpa` counts as local): a workspace opened at
`https://192.168.1.2:8443` or `https://eriphe.local` cannot connect Google, and the page says so
instead of showing the guide, with what to do:

- turn on **Remote access** (Tailscale gives `https://<name>.<tailnet>.ts.net`, which Google takes;
  when it is on, the page links to itself at that address);
- or put Eriphe behind the user's own domain (`https://eriphe.example.org`);
- or, with no Google app at all, "Google Drive for desktop" on an always-on computer, its synced
  folder given as a folder of that machine ([Folders](#folders)).

`startSignIn` refuses such an origin too (`drive.origin.ip`, `drive.origin.local`,
`drive.origin.http`).

### Testing or In production

In the console's Audience, a **Testing** app only takes its test users (up to 100), and Google
expires a test user's authorization, refresh token included, seven days after consent
([Manage App Audience](https://support.google.com/cloud/answer/15549945),
[refresh token expiration](https://developers.google.com/identity/protocols/oauth2#expiration)):
the account must be connected again every week (it shows "À reconnecter"). **Published** ("In production"), that expiry does not
apply; the app being unverified, Google shows its unverified-app warning before the consent and
caps it at 100 users, and an app for personal use (fewer than 100 users) may stay unverified
([personal use](https://support.google.com/cloud/answer/13464323)). The page says both, to be
checked in the console.

### Older configurations

Versions before this one used a shared Eriphe app and a sign-in relay on GitHub Pages; both are
gone. `cloud.google_client_file` and `cloud.bounce_url` still load and are ignored, with a line in
the log at start (`legacyCloud`); a Drive connected through the shared app keeps its token for as
long as that app's client exists.

## Services (local MCP servers)

A service is an MCP server run as a long-lived container on a device, like a Kubernetes Deployment of
one replica behind a Service: eriphe-core keeps what it should be and where, the device keeps its
container running, and eriphe-core reaches it as a streamable HTTP MCP endpoint through the device's
tunnel. The services are a module of their own (`services.go`, `service_owners.go`,
`service_client.go`), which knows nothing of tools or channels: its **owners** say which services
they want.

- **The catalog** (`catalog_services.go`) wants its local servers: a catalog server with `run` (a
  `RunSpec`) instead of `url` and `auth` is the service of its name. The pool reaches it through
  `dialService`, and grants, the policy and the per-task proxy treat it like any other server.
- **A channel** that needs a container of its own wants it while it is on, named
  `channel-<id>`: not in the catalog, so in no list of tools and under no access level, reached by
  the channel alone with a `serviceClient`. No catalog server takes a `channel-` name.

`reconcileServices` asks every owner for the services it wants now, and makes their union the
desired services. No owner can drop another's, and none has to be asked first: at start, a
channel's service is not dropped because the catalog was read before the channel. A name is one
owner's (its `Owns`): a name an owner does not own is left out, and the log says so. An owner
that cannot say what it wants keeps what it has. When a device reports a service `running`, its owner
is told (`Running`): the pool wakes the server's upstream, and a channel its own listening.

A catalog server with `run`:

```json
"servers": {
  "thinking": {"use": "think step by step", "run": {"image": "mcp/sequentialthinking", "transport": "stdio", "egress": false}},
  "notes": {"use": "my notes", "run": {"image": "<your image>", "transport": "http", "port": 8080, "device": "nas", "volume": true,
            "env": {"LOG_LEVEL": "info", "API_KEY": {"file": "/secrets/notes-key"}}}}
}
```

`mcp/sequentialthinking` (Docker Hub's `mcp` namespace, Node.js, stdio, no environment) runs as is
with the defaults. Not every image does: `mcp/time` keeps its Python interpreter under `/root`
(mode 700) and only runs as root, which a service never does; such an image needs rebuilding.

| Field | What |
|---|---|
| `image` | the server's image |
| `transport` | `stdio` (the device holds its stdin and stdout) or `http` (it listens on `port`, MCP at `path`, `/mcp` by default) |
| `args` | arguments after the image's entrypoint |
| `env` | `"NAME": "literal"` or `"NAME": {"file": "/secrets/x"}` (or `{"file": "vault:<name>"}`): the secret is read when the device fetches the service, sent to it, never stored or logged |
| `device`, `needs`, `prefers` | as for agents: pin a device, require labels, prefer `cpu`, `memory` or a label |
| `arch` | `amd64` or `arm64`: an image built for one architecture only runs on a device of it (the device's `arch` in `GET /v1/device`) |
| `volume` | a persistent `/data`, kept on its device in `<root>/services/<name>` |
| `user` | `uid:gid` in numbers, `1000:1000` by default; never root |
| `writable` | a writable root filesystem (read-only by default) |
| `egress` | reach the network (default `true`); without it a stdio server gets `--network none` |

The `services` table keeps each one's spec (without secrets), its hash, its device, desired state,
state (`pending`, `starting`, `running`, `failed`, `stopped`), restarts and last error;
`GET /v1/services` (bearer token) lists them. The scheduler places a service like a task, on
online devices that run services (new devices say so in `GET /v1/device`), with `needs` and `prefers`,
counting the device's services and tasks as its load. A device gone for 5 minutes loses its services
to another device, unless they are pinned to it or keep a volume there: their data stays where it
is, so they wait for their device. A forgotten device (`DELETE /v1/devices/{name}`) loses them all.
A changed spec starts the service over on its device, or elsewhere if the device no longer fits; a
server left out of the configuration is removed, and its device stops it (the volume stays).

Devices receive their services in their assignments, as a `services` list next to `tasks` (old devices
ignore it), and report each one on `POST /v1/device/services/{name}/report`
(`{state, restarts, error}`). A service whose secret cannot be read fails and is not sent.

Credentials are lent, never stored: they are read at every fetch of the assignments, and the device
restarts a service whose environment changed, its `/data` untouched. Rotating a file-backed
password restarts the server with the new one at the next fetch; a token source lent by
eriphe-core (an OAuth access token for XOAUTH2, refreshed by eriphe-core) fits the same place,
as another kind of `env` value resolved in `lendService`.

`c.dialService(ctx, name)` returns an `mcp.Transport` (a `StreamableClientTransport` to
`http://device/v1/services/{name}/mcp` over the device's tunnel, with no timeout): the pool's
`dialLocal`, and a `serviceClient`'s. It fails, with the reason, if the service is not placed, not
`running`, or its device not connected; `serviceEndpoint` gives the `*http.Client` and URL instead.
A `serviceClient` (`c.newServiceClient(name)`) is a session to one service for an owner that calls
it itself. The session opens at the first call and is kept, and a new one opens after a call
whose session failed. Its `call` decodes a tool's structured answer and turns a tool's error into
an error. `registerServices(map[string]RunSpec)` sets the desired services, every owner's.
`reconcileServices` gathers them, at start and whenever an owner's change. Unknown `run` fields
are refused.

Eriphe's own images (the mail's, the calendar's, a channel's) run eriphe-core's version.
`RunSpec.Own` says so. It is set by the owner, never by a configuration: the catalog sets it from
its tool cards, and a channel for its own image. `pinnedImage` then tags the image with the
version (`0.79.0`, or `sha-<commit>` for a build of main). An image named with its tag or digest,
or a development build, is left as it is.

### Mail and calendar: one server per service

Every mailbox is an account of one `eriphe-mail` server (`mail`), every calendar an account of one
`eriphe-calendar` server (`agenda`) (`accounts.go`): the brain sees one `search_messages` and one
`list_events` that cover every account, and names `account` for a write. Each account is named
after its address (a calendar after its username, with its server's host when the username is not
an address) and keeps its secret in a variable of its own (`MAIL_PASSWORD_<hash>`,
`MAIL_GOOGLE_TOKEN_<hash>`, `CALENDAR_PASSWORD_<hash>`, `CALENDAR_GOOGLE_TOKEN_<hash>`), its level
in the server's `account_access` ([per account](#access-levels)). The tools list still shows a row
per account, with its own form, access page and removal; the server goes with its last account.

These servers read the configuration eriphe-core writes them, which changes with it: they run the
image of eriphe-core's version (`services.go`, `pinnedImage`), as eriphe-files does. A release
0.79.0 runs `eriphe-mail:0.79.0`, a build of `main` `eriphe-mail:sha-<commit>`, the tags CI
publishes them under; a development build ("dev") runs the untagged image. A device pulls an image
it does not have, never a newer one of one it has: without this, a machine kept the image it pulled
first. A new version of eriphe-core changes their specs, and their device runs them again with the
new image.
A tool of another version still says so: its version (its MCP `serverInfo`) is in `list_tools`
(`version`, and `want_version` when it is not eriphe-core's), its row in Settings → Tools says it
may misread its configuration, and an error of its tells the brain that likely cause.

Two calendars can share an address: an Apple account's and a Google account's. They are two
accounts. The Google one keeps the address as its name (the Google account finds its calendar by
it); the other is named after where it is (`sam@example.org (iCloud)`, or its server's host). A
Google account connected after an Apple calendar of its address renames that calendar, which keeps
its secret, its level and what was allowed for it. The same calendar added twice is
refused.

## Folders

The user's own folders, given to Eriphe ("Dossiers"). A folder is a name ("Clients"), a source (a
machine and a path inside one of the places that machine shares, declared when it was installed:
`--share NAME=/PATH`, see install/README.md; eriphe-core never sees a host path, only the place's
name and a path relative to it) and a level, chosen there: `write` (Modifier, the default), read,
create, change, rename and delete anywhere in it, its device taking a snapshot of the
folder before each such task (device/README.md, Shares), or `read` (Lecture seule), for a sensitive
folder.folder before each such task (device/README.md, Shares). A folder is shared on the machine that holds
it, by the user there (`eriphe-share add`, or a right click in the Finder or the Explorer): its
device keeps the list and reports each folder's `name`, `access` (its level, `read` or `write`; an
older device's `drop` reads as `read`, its `manage` as `write`), `copyable` (the user allows copies on their other machines, for the
synchronisation to come) and `id` (`folders` in `GET /v1/device`), never its path. eriphe-core reads
that list, and may only restrict it: take a folder off, turn its copies off, and set what Eriphe
may do there (its rights, below). It cannot add a folder, share one to write, nor look into a machine
outside the folders shared (a shared folder's files are read through its tool, below). A name shared by two machines is told
apart as `Name (machine)`.

**Two sources, one list.** A folder is on a machine (here) or a tool's place (a Google Drive
folder, see [Places](#places)). Settings → Dossiers, `GET /v1/folders` and `list_folders` show
both, each with its `source` (`machine`, or `place` with its `tool` and `place_id`) and `lend`, how
a task gets it. Each source keeps its own store and its own enforcement: a machine's folder is in
its device's list and mounted by its device; a place stays in its tool's configuration, which its
server reads and enforces itself, and is lent as `{kind: place}` (its tool whole), not through
`folders`, which refuses it with that hint. A tool's places are set on its account's page
(Settings → Tools → an account), to which their tile in Settings → Dossiers links; "Ne plus
partager" there stops sharing a machine's folder.

### A folder is a tool (`folder_tools.go`)

A machine's folder is also a tool like any other: `eriphe-folders`, a server Eriphe provides in
eriphe-core's own process, kept in the pool beside the catalog's servers (`pool.provide`, an
in-memory session), so every path a catalog call takes applies to it unchanged:

| Tool | Case | What |
|---|---|---|
| `list_folders` | read, always free | the machine's folders: name, online, access, rights |
| `list_files` | read | a folder or a subfolder: path, size, modification time; folders first, 500 at most |
| `search_files` | read | names and text (text files up to 1 MB, without case), 50 matches at most |
| `read_file` | read | a text as is (fenced for a model; `text` in its structured result), an image as image content (5 MB at most) |
| `write_file` | change | a new text file (subfolders made as needed); never overwrites |
| `update_file` | change | replaces a file's whole text; answers a short diff |
| `move_file` | change | renames or moves inside the same folder; never overwrites |
| `delete_file` | change | a file (or an empty folder), kept on its machine |

- **Rights, call by call.** A call names its `folder`; the policy decides it like any catalog call
  (`pool.facts`): its reads are the folder's Lire, its writes Modifier (folder_values.go). Lire free
  asks nothing; On approval is a card; Never is refused (Modifier is Never on a folder shared
  read-only); a free write on a tainted turn or an unclean start asks, as for any tool. A call
  naming no folder takes the strictest of all, so a read is allowed in advance to the brain only
  when every folder's Lire is free. The folder is the call's account (`callAccount`): a task's yes
  for one folder is not another's, and lending the tool to a task asks at its start for the
  folders On approval (`folderToolNeeds`).
- **The card shows the change.** A change's card reads the file on its device when it is made: the
  file, and for `update_file` its diff instead of the new content (`folderCardContext`); for a
  delete its size and time. Nothing runs before the yes; the yes runs exactly that call.
- **Where it runs.** Every call goes to the device holding the folder (`POST /v1/shares/{id}/files`,
  device/README.md), which holds it to the folder: relative paths only, nothing hidden, sensitive or
  made private on the way, no symlink, never a host path in what it answers; a snapshot of the
  folder before a write (one an hour for these writes) and the previous version of a file changed
  or deleted kept in its data. A read goes to a machine keeping a copy up to date when the
  folder's own is away; a write only to its own machine. A Mac whose folders are opened by the
  container engine's explorer answers `lend_only`: a read then goes to an up-to-date copy if there
  is one; otherwise, and for a write, the folder is lent to a task there. A write waits 90 s at
  most (its first in an hour takes the snapshot); a write whose caller gave up meanwhile is not
  made.
- **Taint.** The user's folders are theirs (`trustsServer`): what they say is data, never
  instructions, but reading them does not taint a turn or a task by itself.
- **Who has it.** The brain, by default, once a folder is shared (`assistantGrant` lends it every
  lent tool); a skill through its `tools`; a task as
  `{kind: tool, ref: eriphe-folders}`. Lending the folder itself to a task stays for heavy work.
- **Blocks.** Its reads are read-only by their annotations: `sources_read` lists them and a
  node's block may read a file live (`{tool: eriphe-folders/read_file, args: {folder, path}}`, bound
  to `/…/text`). The devices reach a folder through short-lived helpers too, which cannot watch it:
  such a block is read again on its own schedule, and at once after a write through these
  tools (`refreshFolderNodes`).

### Places

A place is what a tool reaches inside it: a drive's folders (`places.go`). A folder follows its
parent, the top ones the account's grid, which is the root, Never until set; or it sets cases of
its own (the server's `place_grids`), which keep it in the tool's configuration with its
`parents`; back to following, it is no longer kept (`setPlaceCase`). A tool has
places when its card says where it keeps them (`Places`: the key of the list in its
`TOOL_CONFIG`, `places`, its setup tool that browses them, and the argument its tools name one
in), so it is found by having places,
never by its name. The tool browses them with a setup tool
(`eriphe/setup`, never given to a task). Lent to a task as `{kind: place}`, a place lends its whole
tool.

**Its page.** An account's page (Settings → Tools → an account,
`/settings/tools/{name}/access?account=`, `web_places.go`) is its tree, above its exceptions:
"Tout le Drive", the root, then its folders as an accordion, a chevron opening one, its children
browsed through the setup tool when first opened (`GET /settings/tools/{name}/tree?account=`,
`parents=` the ids from the top, `pname=` the parent's name, `page=`). A click on a name opens its
[grid in a drawer](#a-places-grid) (`GET …/tree/grid?account=&id=&name=&parents=&pname=`, no `id`
for the root); a folder's column is "Comme <parent>" ("Comme le compte" at the top). `POST
…/tree/case` (the same fields, `case-<category>=`, empty to follow) sets a folder's cases, or the
root's (the account's grid, its exceptions kept). Keeping or letting go of a folder rewrites the
tool's configuration, which restarts its server.

**A place's grid.** A Drive folder, a repository and a machine's folder set their rights in the
tool's grid (Libre · Sur accord · Jamais by case), in a drawer over their list
(`web_case_grid.go`), with one more column: "Comme le compte", "Comme l'outil" or "Comme
l'appareil", which a case follows until it has a value of its own, the value it follows in small.
Each change is saved at once (`case-<category>=` fields, only the ones that changed are set) and
answers with the grid and the place's summary in the list (out of band): its own cases ("Supprimer
: Jamais"), or that it follows. A case above what the place allows is greyed, Never, with the
reason.

**Its rights.** What Eriphe may do in a machine's folder is two cases, Lire and Modifier, each Free,
On approval or Never, as in a tool's grid (`folder_values.go`): a default per machine (Free and Free
until set; `POST /settings/folders/case` with `device=` and `case-<category>=`, kept as
`folder_grid:<device>`), and a folder may set a case of its own, above or below it, in its
[grid](#a-places-grid) (`GET /settings/folders/grid?name=`; "Comme l'appareil", `name=`, empty to
follow; kept by machine and id, `folder_case:<device>:<id>`). A folder shared read-only greys Modifier: Never,
with the reason. On approval is asked on the task's [start card](#the-approval-card), once per
task, before it starts; so is a Free Modifier of a task that did not start cleanly (a tainted turn,
or a turn that is not the user's). Never is refused when the task is lent, and a Modifier set Never
since mounts the folder read-only. A yes lets the task go with its mount; a no stops it.

**Sharing one** is done on the machine; Settings → Dossiers lists, machine by machine, what is
shared there, with the machine's default rights first (the grid, inline), then each folder with its
level as a badge, the summary of its own cases and "Droits", a "Peut être copié" badge with "Ne plus copier", and whether it is there now;
one "Comment partager ?" button opens a dialog with the two levels, the rights, the copy option, a reminder that the folder's device
must be added first, and one guide per system (Mac, Windows, Linux or a NAS in Docker), open for
the systems of the user's devices, its commands each with a copy button. Turning copies off applies
at once here (`folders_lowered`, `nocopy`: never copied again, even while its machine is away) and
on the machine now or as soon as it is back (`PATCH /v1/shares/{id}` through its tunnel); the row
goes once the machine reports it. "Ne plus partager" takes a folder off: at once
here (`folders_unshared`: it is never lent again), and on the machine now, or as soon as it is
back (`DELETE /v1/shares/{id}` through its tunnel), which stops the tasks that hold it. A folder
shared or unshared on the machine shows in its access log at the next refresh (every 15 s).

**A folder its machine cannot use** is never silent (`folder_access.go`, device/README.md, Folders it
cannot use): its device reports a `problem` for the folder (`denied`, `read_only`, `missing`; the
folder's `problem` in `GET /v1/folders`), and for a copy (its `problem` among `copies`), with the
`program` refused, `eriphe-device` or `syncthing`. Settings →
Dossiers says it on the folder's tile and on the copy's line, and the device's card marks the folder
"bloqué" with the same note. On a Mac that macOS keeps from the folder: « macOS bloque l'accès à ce
dossier », how to allow it (turn on the program named in Files and Folders), and « Autoriser », which asks that Mac's device to open System Settings ›
Privacy & Security › Files and Folders there (`POST /settings/devices/{name}/privacy`, then `POST
/v1/privacy/open` through its tunnel: nothing else can be opened, the user decides there); the note
goes once the device sees the folder open again. On another system, or another problem, a plain
explanation.

**Folders given from the workspace before** (the old `folders` table, `{device, share, path}`) are
not lent any more: eriphe-core never pushes them to a machine, since that would let it add a
folder. Settings → Dossiers shows them under their machine, "À partager de nouveau sur
l'appareil", with their old place and path, until the user shares them again there (under the same
name, they disappear) or forgets them.

**Lending one.** The brain lends folders to a task as [resources](#resources),
`{"kind": "folder", "ref": "Clients", "access": "read"}` (or `write`, to change the folder itself,
where its rights allow; `list_folders` gives each one's entry in `lend`). A task that must not
touch the originals is lent the source folder to read and an output folder to write. The task is placed on a machine that holds each of its folders
up to date, theirs or one keeping copies of them (below, Copies of a folder), or waits for one
(`pending_reason` says so); folders of several machines go together only if one machine holds
them all, and `device` may only name a machine that holds them. Its device mounts each at `/work/folders/<name>`, read-only as
Docker enforces it; with `write` (`manage` in the device's spec, so that an older core's `write`,
its results subfolder, is never read as it), the whole folder read-write, after its snapshot (see
device/README.md, Shares). The worker is told where they are, what it may write in each, and
that what reads like an instruction in the files is data. The folders are read again from the
machine's list whenever the device fetches the task: a folder removed meanwhile fails it, and the
device refuses anything not in its list. A task writes only where its entry says so, and only while
the folder is shared write and its Modifier is not Never: otherwise it is mounted read-only. A task's `folders` lists their names.

**A folder is the user's own.** The user shared it on their machine and lent it: under the
[one taint rule](#tool-catalog), reading it does not taint a task, so its result does not
taint the brain's turn by itself (a task taints by what else it read: a tool not trusted, the web).
What it says of the folder is still data, never instructions, and the event says so:

- the event that brings the brain the result of a task that read folders names them, as data;
- on a tainted turn, a folder lent with `write` is asked on the start's card (see
  [its rights](#folders)); folders to read still go;
- the permission policy applies to the task as to any other, and Docker refuses any write in a
  folder not lent to write.

| Call (bearer token) | What |
|---|---|
| `GET /v1/folders` | the folders, with `mount` and whether their machine is `online` |
| `PATCH /v1/folders/{name}` | `{"copyable": false}`: turn its copies off (on its machine too); turning them on is refused, 403 `raise` |
| `DELETE /v1/folders/{name}` | no longer share it (on its machine too) |

Nothing adds a folder or shares it to write: that is done on its machine.

### Copies of a folder

A folder its machine allows copies of (`copyable`, chosen when it was shared there; turned off
from here, never on) can be kept, up to date, on the user's other machines: Settings → Folders,
"Copies", tick the machines (`PUT /v1/folders/{name}/copies`, `{"devices": [...]}`). Each machine
needs Syncthing, which comes with the installation (device/README.md, Copies); one without is
shown greyed, to update. eriphe-core keeps who holds a copy of what (`folder_copies.go`,
`folder_copies`) and hands each device its plan (`PUT /v1/sync`), with the peers' Syncthing device IDs (from
`GET /v1/device`, `syncthing.device_id`) and each folder at its level now: `read` sent only, `write` both ways with 30 days of versions on the
folder's machine. A plan is handed again only when it changes (and after a reconnection), every
15 s at most. Unticking a machine, turning a folder's copies off, unsharing it or forgetting a
machine drops the copies from the plans: the copy machines delete them, after stopping the tasks
working on them. While a machine of the pair is suspended (or back on a version without
Syncthing), the copy is kept, paused, not deleted.

Each copy shows its state: up to date, syncing (with its %), behind, error (Syncthing's first
error), waiting (not synced there yet), and what it takes there (`copies` in `GET /v1/folders`; `machines` lists the folder's machine and its copies, each `online` and `up_to_date`, a task may be lent it there now).
A Mac whose iCloud Drive files are kept in the cloud only sets `icloud_hint`: the page asks to keep
the folder downloaded.

**Tasks on a copy.** Which machine owns a folder does not matter: a task lent folders may run on
any machine that holds each of them up to date (`folderHosts`), and the scheduler chooses the best
of them as for any task (resources, preferences, load). A machine holds a folder up to date if it
is the folder's machine, or keeps a copy in sync now: the copy idle with nothing to receive, and
the folder's machine idle with nothing to receive from its copies, without error. While the
folder's machine is away, a copy that was complete when that machine was last seen (within 2
minutes: `synced_at`) will do. A task lent several folders goes to a machine that holds them all
so. Its spec lends the copy (`copy` in its folders), at the level the task asked for, never above
the folder's on its machine; with `write`, the device running the task takes the snapshot of the
copy first, and writes on the copy, and Syncthing brings that back to the
folder's machine, at once or when it returns (a conflict keeps both files). With no machine up to
date online, the task waits: for the folder's machine, or for a copy to catch up.

| Call (bearer token) | What |
|---|---|
| `PUT /v1/folders/{name}/copies` | the machines that keep a copy: `{"devices": ["nas"]}` (`[]`: none, the copies are deleted); 409 if the folder is not copyable or a machine has no Syncthing |

### Copies: the way between machines

The copies of a shared folder on the user's other machines are kept by Syncthing, one next to each
device (device/README.md, Copies). The machines never reach one another directly: a device carries its
Syncthing's connection to a peer as `GET /v1/device/sync/{peer}` with `Upgrade: eriphe-sync` and its
secret (`sync_relay.go`), and eriphe-core goes on through the peer's tunnel (`GET /v1/sync/connect`),
then copies bytes both ways. It does so only if it set up a copy between the two machines, either
way (`folder_copies`: `{device, folder, copy}`): any other pair is refused (403), as is a suspended
machine, and a pair holds at most 4 connections at once (429). When the last copy between two
machines goes, their connections are cut. Syncthing's TLS runs end to end with pinned device IDs:
eriphe-core sees only ciphertext.

## Devices: what Eriphe sees and does there

Connecting a device gives Eriphe access to it, so it can help with what is there; letting agents
work on it is an extra (a device that reports runtimes). Settings → Appareils shows, for each device,
first what Eriphe sees there (its Folders, with their right, or "Rien d'ouvert" and a link to share
one), then what it can do there ("Faire travailler des agents", oui or non, its system, its CPUs,
memory, runtimes and labels), then its status and update, as before (`web_pages.go`,
`web_devices.go`).

**Access log.** eriphe-core records what it did on each device (`device_access.go`, `device_log`
table): a folder listed (`list`, with `share:/path`), a folder opened or closed (`share`,
`unshare`), a task run there (`task`, with its agent; the brain itself is not logged), a folder
lent to a task (`lend`, with its right), a file a task wrote in a folder lent to write (`result`,
from the file tools Claude Code reports), and access cut or restored (`cut`, `restore`). The same
entry again within 10 minutes is not repeated; entries are kept 90 days, at most 2000 per device,
and go with the device when it is forgotten. A device's page (`/settings/devices/<name>`) shows
the last ones, 50 at a time.

**Cut access.** "Couper l'accès" (the card's "⋯" menu, or the device's page, after a confirmation)
suspends the device at once, without uninstalling anything: its active tasks are stopped (they
cannot report any more), no new task is placed there (by name, by the scheduler, or for its
folders), its folders are neither listed, opened nor lent, its movable services go to another
device, its tunnel is dropped, and the device is refused (403 on `connect`, and on every device call)
until "Rétablir l'accès". Its registration, secret, certificate and folders stay; it reconnects by
itself once access is restored.

| Call (bearer token) | What |
|---|---|
| `GET /v1/devices/{name}/log?limit=N&before=ID` | `{device, suspended, log: [{id, time, kind, task, folder, access, detail}]}`, newest first (100, up to 500) |
| `POST /v1/devices/{name}/suspend` | cut access |
| `POST /v1/devices/{name}/resume` | restore access |

**Waking a device (Wake-on-LAN).** "Eriphe peut réveiller cet appareil" (the device's page; not on
the machine that stays on) is off by default, a choice per device kept in the `device_wake` table,
and forgotten with the device. Once on, the page shows "Réveiller maintenant" while the device is
off, the card's MAC address it reported (device/README.md, Waking the machine), and how to get it
ready: Wake-on-LAN in the BIOS/UEFI and on the network adapter (Windows: Device Manager → the
adapter → Power Management, and "Wake on Magic Packet" under Advanced; Fast Startup off to wake
from a full shutdown), a cable rather than Wi-Fi; on a Mac, "Wake for network access", from sleep
only. After "Wake now" the page asks every 5 s whether the device connected again since, and says
so, or that it did not within 2 minutes.

`wake(device)` (`wake.go`) sends the magic packets, for each MAC address, to `255.255.255.255` and to
the device's subnet broadcast, on ports 9 and 7, two ways at once: from eriphe-core's own network,
and through every online machine that stays on (`POST /v1/wake` on its device), which sends them from
the host's network. Only the second reaches the home network when eriphe-core runs in a container
on a Docker bridge network, as every installer runs it (`eriphe-system`, published ports): a
broadcast from a bridge stays on the bridge, and Linux does not forward a directed broadcast either
(`bc_forwarding` is off). The first one counts where eriphe-core is on the host's network. It is
refused for a device that is on, whose access is cut, that has not reported a MAC address (an
older eriphe-device), or that the user did not allow. Nothing crosses the Internet: the device must
be on the same network as the machine that stays on. On a Windows or Mac home, where the machine
that stays on runs its device in WSL behind NAT, or as a macOS LaunchAgent held off the LAN, the
packets may not leave it (not checked).

The scheduler wakes a device by itself: when a task waits for a device (`placePending`) and the
device its folders are on is off, may be woken and reported its card, eriphe-core wakes it, in the
background, at most once per 15 minutes per device (`last_auto`), and logs it ("task t-… waits for
pc, which is off: waking it").

## Task API (bearer token)

What the `eriphe` CLI uses to follow and answer worker tasks, next to the conversation API. The
handlers are thin layers over the methods behind the MCP tools.

| Call | What |
|---|---|
| `GET /v1/tasks?all=1` | worker tasks, the most recent first; active ones only without `all`; the brain too while it waits for a permission |
| `GET /v1/tasks/{id}?events=N` | the task's status (as `task_status`) with its last N events (20) |
| `GET /v1/tasks/{id}/events?after=N&wait=S` | events after seq N, waiting up to S seconds (max 120) for new ones; a finished task answers at once |
| `GET /v1/tasks/{id}/access-log` | what the task used, as [GitHub in a task](#github-in-a-task) says, the latest first: time, tool, host, target, action, decision, by whom, source |
| `POST /v1/tasks/{id}/answer` | `{reply, allow}`: allow or deny the pending permission (`allow` set), or reply to an idle task |
| `POST /v1/tasks/{id}/stop` | stop the task's agent |

An unknown task answers 404; a call the task's state does not allow (or that its device refuses)
answers 409 with the reason.

## Workspace (`web`)

Eriphe's own interface, served by eriphe-core on its own HTTPS listener. Its home page, and its
only surface for talking to Eriphe, is the [graph workspace](#the-graph-workspace-the-home-page-)
(`/`, also `/espace`): the nodes, the bar, the cards. There is no conversation page and no top
bar: the settings, the memory and the other pages of the shell (`templates/layout.html`) open with
their side menu, whose first entry leads back to the workspace. The assistant manages: what the
brain can set itself (a notification rule, a memory, an intention) is asked of it in the workspace,
and its pages show the state, say why, and undo, with a line saying how to ask rather than a form.
The worker tasks at work are listed in Settings › Journal (`/settings/journal`, the « Tous » kind),
above the reliability of the last 30 days; a task's page (`GET /tasks/{id}`) is a page of the
settings, under the journal, with its trace, its receipt, its access grants, Stop, the reply
to an idle agent and the answer to the permission it waits for; it reloads every 5 s while the
task works, and after each of its actions (`memory-changed`).

**Cards** (`convo_cards.go`, `web_convo_cards.go`). What waits
for the user's approval is asked where they talk to the assistant: every proposal (a memory, an
intention, a notification rule, a setting, a presence mapping, the start of a task that
needs something of theirs: [asked at launch](#github-in-a-task)) is a card bound by the code
to that one proposal, filed with the brain's message of the turn that made it (or its last message of
the turn), with Oui and Non (the proposal's own approve and reject), shown in the workspace
[in the blocks it concerns](#proposals-in-the-blocks-they-concern-card_movesgo); to change one, the
user tells the brain, which proposes again. The kinds of proposals are one table
(`proposal_kinds.go`, `kindsOf`): each row says how its proposal is read, which wait, how a yes or
a no applies, how it is withdrawn, which node it belongs to and whom a decision tells; reading,
deciding, withdrawing and listing go through the same few functions for all of them. The event
stream keeps cards up to date (`proposals`:
approved, declined, no longer current). The memory's extraction makes no card for a memory (an
intention it proposes gets one, queued, `cardQueued`): what it is not
sure of waits in À valider and expires (see [Memory](#memory)). Other proposals (a skill's run, a
turn that said nothing) are queued and enter the attention queue at their card's weight
(`cardWeight`: one a task or a node waits on 4; a skill, a monitor or a run's calls 3; a
reminder 2; a memory, a rule, a setting or an undo 1; `attendQueuedCards` as they are queued: a
turn that ends without a message, an intention the extraction proposes, `cardQueued`); the queue
delivers them like anything else that goes to the user. A card made
outside a brain turn (the start of a task the brain did not launch, such as a step of a work;
a skill run's calls when its message is held or not sent; an access asked at a task's call)
enters the queue with the message eriphe-core will write for it (`cardNow`); the brain is not
woken for it, and nothing it does waits on it.

**Attention queue** (`attention.go`). It is the one path of what goes to the user, kept by code,
one item per object: a ref (`card:<id>`, `notification:<id>`, `node:<id>`, `mail:<id>`…), a
weight from 1 to 5 (5 interrupts, 4 needs the user now, 3 matters today, 2 is a glance, 1 is
background), a short why, an optional say, its source (`brain`, `task:<id>`, `node:<id>`,
`skill:<name>`) and its time. Items about the same ref merge (the highest weight, the latest why).
Every notification (`notification:<id>`, a record's ref no signal can name, apart from the `alert`
key kind), every card (weighted by its kind, above) and every assistant's
[attention signal](#attention-signals-attention_signalgo) enters it; what is in front of the user
already enters delivered (the brain's own message, a card under it). An item leaves once decided
or gone. An item about a node raises it on the graph (its card at the foot of its page);
an item drawn from external content stays marked so, and the brain reads it as data. What the
user wants shown more, less or never is not applied to the queue: it is a
[preference in memory](#preferences-workspace_prefsgo) whoever notes
items follows. The rest is delivered by code, in a message of its own written by eriphe-core as the
brain's, the heaviest first, its cards under it (`attentionBreak`): **weight 4 and 5 at once**
(nothing a running task or node waits on is ever delayed; one message for what enters
together); **weight 3 at the next breakpoint**, where an interruption costs the user least
(Iqbal & Bailey, OASIS): the end of the brain's answer to a user message, a node set done
while the user is there (away, it waits), or its cap, two hours after it entered (checked every
minute, never in the quiet hours); **weight 1 and 2 only at the user's return** after an absence
(`user.returned`), together: the brief (« Pendant ton absence : … »), three light cards at most
each time, the rest waiting for the next. A delivered notification leaves its hold and points to
the message; the return writes the kv `last_brief`. The queue never pushes to a device: only a
notification decided now at weight 5 does (the brain's notifications, `notify.go`). The brain's turn lists the open items
(`[en attente de … : …]`), the heaviest first, a held one marked « retenu »: nothing the brain
does waits on it. A plain yes or no answers a card in words only when the message is
the user's own (workspace, Telegram or a trusted channel; never a channel the user did not trust, nor `/v1/messages`, whose token may
be lent to the brain), is nothing but a yes or a no, has no file, comes right
after a recent message of the brain's written in a clean turn that shows exactly one waiting card,
whose proposal is not drawn from external content; anything else is the brain's to read, or the
buttons'. The brain has no tool that decides a card. On a channel the user writes from (Telegram), a card follows the reply or the
notification it goes with, with Oui / Non buttons answered from the linked account only (none in
"alerts only"); a channel the user did not trust never answers a card: it says the card waits on the phone, where a notice goes. 

The **Journal** (`/settings/journal`, `web_activity.go`) is the black box: the feed the brain reads
with `since` (below), by kind (tasks, notifications, intentions,
messages) and period; its "tool accesses" kind is the journal of decisions, paged. **À valider**
(`/settings/approve`) is the fallback where whatever still waits is listed: the permissions of
tasks, memories and intentions, notification rules, settings, presence, and the quiet
hours the activity suggests (to adopt in one tap). Each is approved or rejected with its own
action, its card says so, and the list reloads itself. Both are entries of the settings (À
valider with a discreet count), not of the main navigation.

The settings come in four groups: Assistant (the memory, "Ce que Jarvis sait de toi", still at
`/memory`, the agents, the skills), Connexions (tools, folders), Appareils & accès
(devices, access, notifications) and Mon espace (account, the Journal, À valider); an entry is marked
when it needs a look (an update, a tool in error, a refused key). The menu's first entry is the
way back to the workspace (« ‹ Espace »); there is no top bar.
**Access** (`/settings/access`, `web_access_list.go`, `access.html`) lists every way into the
assistant, one row each as the tools are listed: the web workspace and the mobile app always, then
each adapter once set up (Telegram, its bot), then the API accesses. Each row says who speaks through it, what it
carries and its state (the remote access's phase, the devices told, the account linked, whether
the app listens), and opens its page: the web (`/settings/access/web`: the remote access, below, then the workspace's address and how one
signs in, which were in Account),
the mobile app (`/settings/access/app`: installing the workspace, this device's notifications and
the devices told), Telegram (`/settings/access/telegram`) or an API access
(`/settings/access/api/{id}`: its trust, what it may say, its token replaced, removed). **Ajouter
un accès** opens the catalog (`entryCatalog`; `/settings/access/new` without JavaScript): each
adapter with a page of its own (Telegram), or an API access for any app or script (Home
Assistant's integration is one: not trusted, says a sentence).
An access carries the conversation; a tool acts. **Notifications** keeps the router's order, the
default channels, the rules and what was sent.
The conversation shows the user's messages and the assistant's answers. The events given to the
brain (a tool or an agent added, a task's turn over, a permission asked) are its notes and are not
shown; a task that failed, was stopped or was lost is one short line that opens its drawer. When
the brain fails (an API error its harness writes as its answer, a turn that ends on an error, a
start that keeps failing), the conversation shows an error card instead, in the assistant's name:
the likely cause in words (key refused, quota, model, network, passing outage), "Vérifier le
compte IA", "Réessayer" (the turn that failed is given again, and a brain that is down starts at
once) and the raw error under "Détails". One card stands for a run of failures, and while it is
open the conversation does not show the assistant as thinking. Errors are shown one way everywhere
(the brain, the tasks' drawers, the tools, the accounts: `problems.go`): a sentence, what to do,
the raw text folded. Settings → Tools lists the catalog's servers with their status (ready, to connect,
error, or waiting for a machine for a local one) and where each comes from, adds one from a list of
presets, a catalog by category with a search (tasks and notes, mail and calendar, files, meetings,
design, development, websites, social networks and newsletters, payments, automation, home): Home Assistant, GitHub, Mails, Agenda,
Google Drive (its own page, see [Google Drive](#google-drive-cloud-folders)), and some fifty
services that host their own MCP server and register Eriphe as an OAuth client on the fly, like
Linear (Notion, Todoist, TickTick, Evernote, Fastmail, Calendly, Dropbox, Granola, Canva, Miro, Buffer, Kit,
Cloudflare, Sentry, Vercel, Supabase, Stripe, Zapier… the list is `core/presets_online.go`, each
checked against its public OAuth metadata). A tile says when the service can give a read-only
token, when it needs a paid plan or is in beta, and when a tool already comes from it. "Autre
service en ligne" takes any MCP server's address and looks first (an initialize without a
credential, then its protected resource and authorization server metadata): open, one-click
sign-in (dynamic registration; a server that takes confidential clients only gets one with a
secret, which Eriphe keeps), an app to make at the vendor, or a key to paste, with the scopes it
asks and whether a read-only set exists; then the form comes filled. Services that need an app
made at their place (Figma, Slack, Asana, Google, Microsoft 365…) are listed as not possible yet.
A preset's form asks only what the owner decides: the credential (with the provider's own
steps and link), the address when it differs per person, the name, what the tool is for, and what
Eriphe may do on it (its [access level](#access-levels), four cards); the rest (a vendor's address, the authentication, timeouts, an OAuth client, how a
local server runs) is under the advanced options. GitHub first asks how to connect, the two ways
side by side: [Eriphe's GitHub App](#the-github-app) (recommended: the user chooses the repositories,
each task gets a one-hour token for its repository only, commits and PRs appear as the app,
about five minutes to set up once; it leads to Settings → Tools → GitHub) or a personal token
(quick, acts as the user on everything the token reaches, long-lived; git works with it, gh does
not, so no pull request from a task; its form says so and links back to the app). A GitHub connection on a token shows "Passer à l'app" in the list and
in its form. In the list (`web_tools_groups.go`), each account is a card, as a phone's Settings →
Accounts: the provider's mark, the address, "Google · Mail, Agenda, Drive", then a row for each
of its services (Mail, Agenda, Fichiers) with its state, access, reconnect, edit and remove. A
connected account's card has its actions ([connections](#connections)): reconnect, "Retirer ce
compte", and the tools its provider serves that it does not have yet ("Ajouter à ce compte").
The files tool is a row under each account it reads, with that account's folders, its page their tree. A Gmail on a password goes in the card of its Google address; an
Apple account's mail and calendar are one card, an Apple calendar and a Google calendar of one
address two. Only the list is by account: the servers stay one per service. The other tools of
one kind (GitHub, Notion…) make one card when there are several: the kind, how many accounts and
tools, "Ajouter un compte", then each account with its tools as rows. A kind with one tool stays a
plain row. Mails and Agenda first ask which kind of account
(Gmail, iCloud, Orange, Free, OVHcloud, Fastmail or any IMAP server; iCloud, Fastmail, Nextcloud or
any CalDAV server) and run `eriphe-mail` and `eriphe-calendar` with its password. Their picker and
form are built from the tool's card (`tool_cards.go`, `account_forms.go`): its kinds of account,
the fields each one asks, the provider guessed from the address and the server's options (the
calendar's time zone); no code knows a mail or calendar form. Once saved, the row that follows the
tool asks its server to sign the account in (its setup tool `test_account`,
[servers](../servers/README.md)): a refused password shows on the row with the server's reason. "Gmail avec ton compte Google"
(first, recommended) and Google Agenda lead to Google's connect page, one sign-in for Gmail,
Calendar and Drive ([Google accounts](#google-accounts-gmail-calendar-drive)); the Google tiles of
"Add a tool" too. Outlook, Microsoft 365 and Microsoft calendars are shown as coming soon. A vendor's OAuth service (Linear, Notion and the catalog's) opens a short page (name, use, access), then "Se
connecter à Linear" saves and signs in at once; the tool is added only once authorized. Settings →
Tools also edits and removes the workspace's own tools, and connects again those that use OAuth
(see [OAuth](#oauth-and-eriphe-connect)). A collapsed "Secrets avancés"
section lists what the vault holds and what each secret belongs to, with replace and delete only,
and the configuration's secret files to import. Settings → Agents shows the agents (model, account,
whether a machine can run them now), the agents worth creating with the accounts (one click), the
default agent and the brain's, and the AI accounts (providers). Adding an account starts from a
card per kind, subscriptions first (Claude, ChatGPT, Mistral), then API keys, then a self-hosted model
server (on a device or at an address, an optional key, and its models, detected and ticked), each with who pays, its
limits, its models and the steps to get its credential; "Test the key" lists the provider's models
once with the key typed (OpenRouter also tells the credit left; a Claude subscription token is
only checked for its shape, as Anthropic allows it in Claude Code only; a key pasted in the wrong
card is caught), and the usual agents of the kind are created with it (Opus, Sonnet, Haiku for
Claude; GPT models for ChatGPT; Mistral Medium on Vibe for Mistral; two well-known models for each API key). A key is tried again when
it is saved, in Settings as in the first-run wizard: one the provider refuses is not saved, and
the form stays with "Clé refusée"; one that could not be checked (the provider unreachable or in
error) is kept, with a warning, and the account and its agents show "Non vérifiée" with the reason
instead of "Prêt" (a test of the stored key that is refused shows "Clé refusée"). An account error met in use (a key or token refused, a quota, a model the key cannot use,
from a task or the brain) is kept on the account the same way, and cleared by the next turn of one
of its agents that goes through, or a test of the stored key that passes. The agent form offers the
account's models (another one can be typed; OpenCode's provider prefix is added), derives the
program from the account, and keeps the machines' needs, preferences, image and program under
"Options avancées" (see [Dynamic catalog](#dynamic-catalog-workspace)). It works
on a phone, in French by default or in English, light or dark as the system is. Server-rendered pages (`web/templates`), [htmx](https://htmx.org) 2.0.4 vendored in
`web/static/htmx.min.js` (SHA-256 `e209dda5c8235479f3166defc7750e1dbcd5a5c1808b7792fc2e6733768fb447`),
Server-Sent Events for what changes; everything is embedded in the binary, with no build step.

```json
"web": {"listen": "0.0.0.0:8443", "hostnames": ["192.0.2.10", "server.local"], "lang": "fr"}
```

| Field | What |
|---|---|
| `listen` | the workspace's address, `0.0.0.0:8443` by default |
| `cert_file`, `key_file` | a certificate to serve; without them, a self-signed one |
| `hostnames` | names and addresses for the self-signed certificate, besides `public_url`'s host, the machine's name, `localhost` and its interfaces' addresses |
| `lang` | `fr` or `en`; without it, the browser's language (`Accept-Language`: French when it prefers French, English otherwise); each browser can switch in Settings, which wins |

**The user's language.** What Eriphe writes for the user follows one language, `fr` or `en`
(`userLang`, `core/user_lang.go`, kv `user_lang`): the language of the last signed-in page that set
it, that is the workspace opening (`/espace/boot`), the language switch in Settings, the channel
settings or a channel's link code; before any, `web.lang`, else French. The fixed texts use it (the
workspace's words, the bot channels' messages), and so do the models' lines: a skill run's
and a task's final message (`skillVoice`, `workerVoice` name it), and a `[langue de l'utilisateur : …]` line at the head of each turn of
the brain and the assistants, for their notifications, cards, nodes and
summaries (the brain answers a message in the language it is written in). The fields of the tools
the user reads (`notify`'s text and why, a rule's or a setting's why, an intention, attention's why
and say, a node's text) ask for "the user's language". What only the models read (their
prompts, packets, receipts, reports) stays in French, and so does the memory, whose index stems
French.
| `session_days` | how long a login lasts, 30 by default (a week without use ends it sooner) |
| `auth` | how one signs in: see [Signing in](#signing-in); without it, Settings → Access → Web workspace says |
| `external_url` | the workspace's address, e.g. `https://eriphe.example.org`: see [the reverse proxy guide](../docs/reverse-proxy.md); `auth.external_url` works too, this one wins |

**First start.** With no password yet, eriphe-core logs a one-time setup code:
`web: no password yet: open https://…/setup with the setup code ABCD-EFGH`. The code is kept in the
database: the same one at every start until it is used, and `eriphe-core auth setup-code` prints it.
Open the page, type the code and choose the password (10 characters at least). The password is
stored as an argon2id hash; the code is no longer accepted once used. While the brain has no agent, the
conversation leads to a wizard (`/welcome`): the assistant's name (an empty field), then the
AI account: every kind as equal cards, as Settings → Agents shows them (subscriptions, then API
keys, the custom one last), none picked; a card (`/welcome?kind=<kind>`) opens its form. Then the
brain's start: on this machine once a device is registered (an install with its core has one of
its own), with "connect a device" only while none is. "Plus tard" skips it.

**The first-run interview.** Once the brain has an agent and the wizard is over, the first time
the conversation opens, eriphe-core gives the brain one turn marked `premier lancement`
(`[Sam 12:00 #N · premier lancement]`, not shown in the conversation; `first_run.go`), and the
brain writes first: it offers a short interview, which the user may skip (`brain-prompt.md`,
« Premier lancement »). What it learns goes to its memory, cited from the user's answers: who they
are, what they expect and how to talk to them pinned to the profile; their address, devices and
household as ordinary memories, recalled when needed. The setting `first_run` (`due`, then
`done`) makes it fire once per install; an install that, at the first start of this release,
already has a conversation or a brain with an agent is `done` at once, and so is one whose brain
has already written. The Memory page asks for it again ("Faire l'interview", `POST
/memory/interview`), as does asking the assistant in the conversation.

**Sessions.** A login sets a random session cookie (`__Host-eriphe`, `HttpOnly`, `Secure`,
`SameSite=Strict`; only its SHA-256 is stored). Every POST carries the session's CSRF token (the
`X-CSRF-Token` header that htmx sends, or a form field) and must come from the same origin.
Logins are rate-limited per address: 5 failures from an address (an IPv6 /64 counts as one) in 15
minutes refuse it for the rest of the window, attempts in flight included. There is no global
lockout: past 20 failures from all addresses, every attempt waits longer first (250 ms more per
failure, at most 5 s) but is still checked, so the owner gets in from their own address.
Connections from eriphe-core's default gateway (in a container, the Docker bridge's: Docker's
userland proxy shows the host's own localhost, a VM's port forwarder, and IPv6 clients of a port
published on `[::]` with that address) may stand for anyone: they share one count of their own,
with 20 failures, and never count as the local network (below). The installers publish the ports
on IPv4 only, so that clients keep their own address. At most two
argon2 hashes are computed at once. Changing the password ends the other sessions.
The bearer-token API is unchanged for the CLI and the devices.

### Preferences (`workspace_prefs.go`)

The workspace keeps no rule and has no fixed control but a card's Oui / Non (the user's answer
under the access policy). How the user wants it is memory; what the brain can do with it are
tools. The user's side of it is in the guide [docs/workspace.md](../docs/workspace.md).
Preferences are [memories](#memory) of kind `preference` about `topic:workspace`, written as facts
about the user. The brain remembers one from the user's words (`remember`; a workspace preference
is never sent to `rule`, whatever its words); the nightly extraction never writes one. A
preference about content is read by whoever notes items: a triggered skill that tends a node with
sources gets the active, current ones in its run (`seePreferences`, 12 at most, the latest first),
and leaves out what the user no longer wants to see. A proposed preference is not read. The brain
does not see the workspace's layout: it works on nodes ([The brain on
nodes](#the-brain-on-nodes-node_toolsgo-node_turngo-node_netgo)); `node_show` asks the page to open one.

### The graph workspace, the home page (`/`)

Eriphe's home page is the graph of nodes (#633, `web_graph.go`, `graph_feed.go`, the app in
`web/espace`): every node a bubble (« bulle », a subject when it is a root), its bubbles around
it, its links; an open node is its page, drawn right where its bubble was. Nothing on it is a
screen planned in code: what shows is the graph eriphe-core streams and what the user opens. On
screen, in French, a node is « bulle » or « sujet », never « nœud ».

**The routes** (`web_pages.go`). `GET /` and `GET /espace` open the workspace, the wizard
(`/welcome`) before it while the brain has no agent (`needsWelcome`). A skipped wizard, or a brain
with no AI to work for it, is told above the bar: the boot's `setup` (`noai`, with a link to
`/welcome`; `nobrain`). What points at a message points at its node (`/espace?open=node-<id>`,
`nodeAt`, `messageAt`; the home node is `/espace`); a permission points at
`/settings/approve#a-perm-<task>-<id>` (`permissionAt`); a task at `/tasks/<id>`.

**What the page reads** (`web_graph.go`), with a session:

- `GET /api/nodes/{id}[?version=n]`: a node's page (`nodePage`): its row (below), the version
  shown, its versions (`n`, `author`, `at`; code's own versions, a source's new values, are not
  listed but the last; nor an empty creation, a step opened for its task to write: `shift` 1, the
  page numbering the versions from the first with something in it), its body as parts in order (`parts`: Markdown `text`, or a `block`), and
  the cards it waits on (`cards`, as the [proposals](#proposals-in-the-blocks-they-concern-card_movesgo)
  show them). A standing node's page says what keeps it alive (`alive`, `node_alive.go`), from
  what code knows, never a model's words: the `sources` its blocks read (`name`, `what`: the tool
  or resource and how often), the `monitors` that watch for it (those its blocks read and those its
  skills run on, `what` from their spec), the `skills` that work in it (`what`: what starts them, at
  most how often) and when it last changed (`updated`). The workspace shows it in the page's ⋯
  menu, « Ce qui le fait vivre »; a permanent subject has one sign, a small ∞ by its title (« Sujet
  permanent », in its page's head and its row on a phone), a satellite slowly orbiting its bubble
  (still with `prefers-reduced-motion`), whose core says « Sujet permanent » too.
  Every page says who wanted its node (`origin`, `node_origin.go`): code records it in the node's
  `opened_by` when it makes it ([node format](../docs/node-format.md)), from what it knows: the
  brain in a turn that answers the user (their message), in a turn of events only (an event); the
  user's yes on a view's card; a skill's run (the skill, what started it); a task or a step (who
  wanted their work); code, for one of Eriphe's own places (« Outils à revoir », no sign). `origin.text` is the sentence the ⋯ menu shows under « D'où il vient »
  (« Ouvert à ta demande : « <the first line of their message> » », « Ouvert par le skill X, après
  ce qu'a vu la surveillance « m », sans demande de ta part », « Ouvert après un événement, sans
  demande de ta part », « Origine inconnue » for a node made before); `origin.unasked` tells a node
  a skill or an event opened. A block (`pageBlock`) is ready to draw: its `name`, its A2UI `components`, its
  `data` with each source's last value at `/<name>` (its latest read, whatever version
  shows: the values are code's, never in the file); or its `error`
  when it does not read as a block. Its media go through eriphe-core (`/espace/media`, signed),
  those an external block's author wrote dropped unless a source gave them; a Link keeps to the
  addresses a Link may open. Each item of a source with `items` carries its note's fields
  (`_note`, `_state`, `_group`, `_external` when the note may carry someone else's words;
  `list_sections.go`). `lists` (`list_rows.go`): each List bound as a whole to the items of a
  source that filters them (`filtered`: the page shows only its rows, code filters), whose source
  opens its items (`open`, `item_open.go`), that a batch folds, or one of whose rows a card waits
  on (`component`, `source`), its rows (`items`: `n`, its place in the source's list, `id`,
  `title`, `detail`, `card` the ref of the card that waits on it); a filtered List's template
  path reaches the page without its filter. The node's
  goal is never sent (it is in its file). `refs`: the node
  each `[[name]]` of its text opens, by the name as written (`id`, `title`, none when it may be
  someone else's words, `external`, `archived`); a name no node has is not there.
- `GET /api/nodes/{id}/trace`: the trace's entries, oldest first (`at`, `author`: brain, user,
  `task:<id>`, eriphe, outside; `text`).
- `GET /api/nodes/{id}/md[?version=n]`: the file as it is (`md`), its body as the user edits it
  (`body`: its text and blocks, without code's fields), its `version`, its `hash` and the last
  version (`last`).
- `GET /api/archives`: what the user archived, the latest first (`nodestore.Archives`): each node
  archived with no parent archived with it, a row as below with `archived` (when) and `with` (how
  many nodes below it went with it).
- A node's row (`graphNode`), in the page and the stream: `id`, `title`, `titleExternal` (its
  title may be someone else's words, written on an external node and not `title_trusted`: the
  workspace shows it as it is, in amber with a ⚠ saying « Titre écrit après une lecture
  extérieure », in the page's header, the list, the archives and a [[link]] to it; on the bubble
  it is drawn as any other title (one sign per meaning: its page's head says it, by one ⚠ for the
  whole node, its title or its content, never a word), the ⚠ in its core's words for a screen reader; never replaced, and code never acts on a title), `unasked` (a skill or an event opened it, not
  the user: a small « ↯ » after its title on its bubble and in its page's header, its tooltip
  « Tu ne l'as pas demandé : un skill ou un événement l'a ouvert. »; the user's own
  nodes, and those whose origin is not known, have none), `parent`, `links`,
  `lifetime`, `status` (`running`, `waits`, `done` or `idle`, computed: above), `created`,
  `updated`, `done`, `external`, `version` and, while it runs (not archived), `line`: its task's
  last word of where it is.

**The stream** (`/events?tab=<tab>&node=<id>`, `web_sse.go`). JSON events, the graph's from
`graph_feed.go`, which the node store tells of each change (`nodestore.Watch`):

- `node`: all the rows at once (`reset`) and when the stream fell behind the feed's last 512
  changes, then those that changed, and the ids of those that left (`gone`: archived);
- `doc`: a new version (`{id, version}`) or a trace that grew (`{id, trace: true}`): a page shown
  reads it again;
- `progress`: a task's line (`{node, text, at}`), from its `progress` tool, only when the task read
  nothing external (a tainted line stays in the trace, never under the bubble);
- `source`: a live block's new value (`{node, block, name, value}`, its items' notes on them),
  sent when its read changed or the notes on its items did (to each block that reads it), with the
  rows of its Lists the page knows (`lists`: `{component, items}`, as the page's `lists` gives
  them);
- `show`: Eriphe asks to open a node's page (`{node, by, at, seq}`), the last one at once, then each
  new one; the page opens it beside what is open, once (an old one, over a minute, is not news);
- `orb` (below), `proposals` and `notice`.

**The user's gestures** (`web_gestures.go`), kept by eriphe-core, each with the session's CSRF
token in `X-CSRF-Token`:

- `PUT /api/nodes/{id}/md` `{body, expect}`: an edit of the .md tab, the user's version
  (`authorWrite` as `user`, the node's guard and the secrets filter as for any author), made on
  the version whose hash is `expect`. A newer version gives `409` with its `version`, `hash` and
  `body`, and writes nothing; an archived node `410`; a body that does not read `400`.
- `POST /api/nodes/{id}/ask` `{block, component}`: a block's button. Its words are read again in
  the stored block (`a2ui.AskText`), never taken from the page, and become the user's message to
  that node. A block marked external asks too, but its words may echo what was read: the message
  is stored tainted (`ask:<node>/<block>`), so the turn it starts is tainted as the one that wrote
  it, and it is never taken for the user's own words (`fromUntrusted`: no owner memory cited from
  it, no text « oui » on a card, no lending, the extraction reads it as external). It acts on
  nothing: no route marks a block's items or acts on them; a yes is a card's.
- `POST /api/nodes/{id}/open` `{block, source, item}`: an item opened in place
  (`item_open.go`), the user's gesture. The source's `items.open` is read again in the stored block
  (`409` when it has none); the item must be one the source lists now (`400`). The tool (a read of
  the source's own server, checked at write) is called with the item's id as the source gave it,
  under its policy: a free read that does not reach the open web runs; any other is `403`
  (`open.notfree`: ask Eriphe, who proposes it on a card); the click is never a yes. `200` `{title, from, date, body, raw, external, note: {group, note,
  external}}`: its fields as the read tool declares them (`_meta` `eriphe/open`), else a title and a sender as a row finds them (`from` as written, « Sam <sam@example.org> »: the page splits name and address), the whole as JSON, `external` for a server not
  trusted; `502` when the tool failed. Nothing is kept: no version, no trace, no journal of what it
  said.
- `POST /api/nodes/{id}/archive` and `.../reopen`: the user's alone (`archiveNode`: what runs
  below it stops, and the cascade leaves the graph; `reopenNode`: back with what was archived with
  it and the archived parents it needs, in one commit).
- `PUT /api/espace/view` `{open, talk, shown, theme, orb, forces, centring}`: the pages open, the one the bar talks
  to, the last of Eriphe's shows the page took (a page the user closed does not open again on the
  next visit), the theme (`""` follows the system, `light`, `dark`), and what the user closed of
  the orb's (`orb`: `{answer, seen, failed}`, seqs that only grow); the graph's
  forces the user changed (`forces`: `{centre, repulsion, link, distance}`, each brought within its
  range, `espaceForceRanges`, a name that is not a force dropped; absent, the defaults) and
  `centring: false` when the open node is not the graph's centre (absent, it is); in the kv `espace_view`. The boot gives
  them back (`view`), on any of the user's screens; where the bubbles are is not kept, and the shell carries the theme
  (`data-theme`) before the app draws.

**The app** (`web/espace/src`):

- `data/`: the requests (`api.ts`), the stream (`stream.ts`: one per page) and the store (`store.ts`, Zustand): the graph as streamed, what is
  open, the theme and the graph's settings, each event a pure step tested on its own. `keep.ts` sends what
  is open, the theme and the graph's settings back to eriphe-core a moment after they change. `presence.ts` says the
  user is there. A page Eriphe shows (`applyShow`) opens beside the others and leaves the bar
  where it was: only the user's click or ⌘-click moves it.
- `graph/`: the layout (`layout.ts`, `Sim`): a force graph like Obsidian's, one simulation for
  the page's life, ticked a frame at a time while it is warm. Four forces, the user's to set
  (`FORCES`, defaults `DEFAULT_FORCES`, bounds `FORCE_RANGES`): the **centre** (0.1) pulls every
  body to the middle, the **repulsion** (2000) pushes every body away from every other
  (many-body), the **link force** (0.8) holds two linked bodies together like a spring, and the
  **link distance** (30) is that spring's rest length, shape to shape (a bubble's circle, a page's
  rectangle; never a label). The links are one per pair of nodes (`pairs`): a bubble and its
  parent, two nodes a `[[link]]` or the front matter joins, one way or both; each is drawn as one
  thin grey straight line. An open page is a node like any other, only a bigger shape: the same
  forces act on it, so a subject and the view it links to end up side by side because they are
  linked. Every body, a bubble with its label or an open page of its measured size (measured again
  whenever its content changes), is a solid box to every other (`collide`). One energy drives it
  all, as in d3: any change (a node come or gone, a link, a page opened, closed or resized, the
  forces set, a drag) warms the graph (0.5), the forces adapt everything as it cools (2.28 % a
  tick, some 300 ticks), and once cold the graph rests: anything still overlapping is set apart at
  once, and nothing ticks until the next change, nothing trembles. The first layout is settled
  before it is shown. A new node is born in the most free space near what it links to (on the
  disc, near its middle, when it links to nothing), then settles. Nothing is kept: a reload lays
  the graph out afresh. A standing view made from a subject's card is linked to that subject
  (`decideView`), so it sits beside it. The user drags a bubble, or a page by its head: its cluster
  comes along (one link away 0.85 of the move, two links 0.35) and the graph stays warm (0.3);
  dropped, nothing is pinned: the body and its cluster take up the forces from where they were let
  go, so they stay on the side the user chose while the centre keeps them in the disc. **The open
  node is the centre** (the setting « Le node ouvert devient le centre », on by default): the page
  in focus, the one the bar writes to, stays where it is and the centre force pulls to it; when the
  focus changes, the graph glides to its new centre and the other bodies spread all round it, in
  the order they stood in (`spreadRound`). Hovering a bubble or a page's head keeps it, its
  neighbours and their links sharp and fades the rest; nothing else dims, pages open or not. Near
  the screen's edge the camera scrolls, driven by the dragged bubble's place on screen, not the
  pointer: a 110 px zone, after 200 ms, speeding up over 700 ms to 16 px a frame. The camera
  (`camera.ts`) frames the page in focus in the middle of the free band, readable: at scale 1, or
  as much less as it takes to show it whole, never below 0.9 (`frameAround`, `FLOOR`); the
  neighbours round it may overflow the screen, and a node that waits for the user out of view
  keeps its arrow at the screen's edge (`Edges`). An enlarged page is framed alone at scale 1.
  With the centring off, it frames every open page if they fit at a scale of 0.8 or more, else the
  page in focus, whole, the others in place, and the whole graph with them when it fits at 0.72 or
  more; with no page open, the whole graph, labels included. It frames what the graph is when it
  comes to rest (or what is open changes), and while the graph moves every quarter of a second,
  not at each frame. Dragging a bubble leaves it framing unless the drag scrolls it at the edge. A
  drag of the background or the wheel moves it by hand, ⌘ or Ctrl with the wheel zooms, a double
  click gives it back. The forces and the centring are set under the corner's gear
  (`hud/Settings.tsx`, `graph/Forces.tsx`: four sliders and « Par défaut »), applied at once
  (`setForces` warms the graph) and kept by eriphe-core with the view (`forces`, `centring`). A bubble's size and light come from what the node is (`model.ts`): a subject is
  bigger, what waits for the user brighter, what runs a little, what is done or old quieter. Under
  760 px wide the graph is a list (`graph/List.tsx`, `listOrder`): each subject, then its bubbles
  under it, depth first, in the order they were made, one that waits for the user said by its
  accent dot alone (its words for a screen reader only), one at work by its line; a tap opens its page alone over the list,
  and a page shown from it takes the screen (the bar with it) until it closes.
- `page/`: a page in the graph, any number open: a click opens one alone, ⌘, Ctrl or shift with
  the click one more. Dragged by its head; its head says what it belongs to, its title, its date
  (`stamp`, in the browser's time: today the time alone, yesterday « hier, 13 h 13 », this year
  the day, else with the year) or « En cours » on the title line, its versions, « Archiver », a
  « ⋯ » menu with its Trace and its .md file (no tabs: the page shows its content), the cross that
  closes this page only; under its head, its content and at its foot the cards it waits on scroll
  as one column (the wheel over a page scrolls it, never the graph; focused, the keyboard too), answered by their
  card's Oui or ✕ (`POST /cards/{id}/yes|no`), read again when a proposal moves, and the question before archiving (how many
  bubbles go with it). An older version shows as it was. The .md tab edits the node's body in
  CodeMirror (`page/Editor.tsx`, in a shadow root: CodeMirror styles a document with a `<style>`
  element, which the CSP refuses, and a shadow root with a constructed stylesheet; its look is
  the page's variables). It saves on the version it was read at (`page/edit.ts`): a newer version
  that came meanwhile shows with its text, nothing written, and the user takes it or keeps theirs.
- `doc/`: the page's parts: text as Markdown by the page's own renderer, which makes no HTML of it;
  each block an A2UI surface (`<node>--<block>`) of the page's own adapter, a source's new value put
  at its place in the block's data. A block marked external is a tainted surface. A button's `ask` goes to eriphe-core (`.../ask`)
  from the last version of a block, an external one's too (eriphe-core marks its words); a row's card is drawn by the page from
  the out-of-band `lists`, never from the block; anything else stays in the page. A card block is
  the card it places (`Cards`, read from eriphe-core), or « Carte introuvable » when the page sent
  no card for it.
- `orb/`: Eriphe in the graph, a small blue orb (`Orb.tsx`), and the bar under it (`Bar.tsx`). The
  bar is the one input, and only a bar: it talks to the page the store's talk names (the last one
  opened or clicked), else to the home (`POST /messages` with `node`). No history, no buttons:
  Eriphe's questions and their `ask` buttons are in the subject's document. Above it, the cards no
  node holds (its only buttons) and the messages that wait for Eriphe, a clock and a ✕ each (**The
  messages that wait**, below). The orb rests in the bar (`.cs-dock`). What it shows comes from the
  stream's `orb` event and what the user just sent (`model.ts`, `orbShow`, pure, in this order).
  It only answers: it leaves the bar in reply to what the user said to it, never unasked (what an
  event made Eriphe say, a node that began to wait: the graph shows it, by the node's halo). It
  never points at a card either: what waits for the user's yes is the graph's (the node's halo,
  the card in its page), and an answer whose turn raised a card is said as any other.
  At work on the user's message (`work`, from the moment the user sends: the first line of what
  it writes, or dots); that turn failed (`failed`: Réessayer, the agents' settings); the answer
  to the user's message (`answer`); else it rests. It glides beside the subject that is about (`placeBeside`: its bubble, or its
  page when open, as drawn): the first side of six where it and its words cover no bubble, no
  title, no page, not the bar nor the corner's icons, keeping the side it had while it stays free;
  when none is, the free spot nearest around it (`NEAR`, 120 px further at most); at the home, a
  free spot near the screen's middle (`placeHome`). When there is none, it covers nothing: it stays
  in the bar and its words are a line above it, as on a phone (`setCrowded`). It is no body of the simulation: it reads where the bodies are when they move (the graph tells it after each frame, `onFrame`) and
  follows its subject; still, it reads nothing. One `OrbProvider` works out what it shows, once, for the orb, the bar and its line. Its words show once it is there, open away from the subject, in a few
  lines (`fewLines` on eriphe-core's side: six lines, 600 characters at most) with « Voir plus »,
  which unfolds the whole answer in place, as Markdown (`full`, scrolling past 40 % of the
  screen); unfolded, it stays until the user moves on. Its answer beside a subject stays until something else
  happens: a newer answer replaces it, or the user moves on (writes, opens another subject,
  closes it with ✕); else they go after two minutes of the page being seen (`WORDS_MS`: a hidden tab does
  not count). What went is remembered by eriphe-core with the view (`orb`, sent at once), so a
  reload, on any of the user's screens, does not replay it. **An answer
  given at the home** (no subject opened) that is more than its lines has no subject to hold it:
  the orb holds it whole (`whole`, Markdown, scrolling past 40 % of the screen) until the user
  writes again, opens a subject, or closes it. Words written after reading external content carry the « extérieur » dot, and a
  title that may be someone else's words shows as it is, marked: never replaced. With less motion
  asked (`prefers-reduced-motion`), it goes at once and does not pulse. A screen reader hears
  its words (an `aria-live` region); the orb is a button that opens its subject. Under 760 px
  wide, it does not glide: its words are a line above the bar, « Sur X » naming the subject. A
  card raised by a turn that answers the user's message is never out of sight: one no node holds
  waits above the bar, and one linked to a node opens that node's page beside what is open, as
  `node_show` does (`showTurnCards`, node_turn.go, when the card is attached to the turn's message
  or its end).
- `hud/`: no bar, the graph takes the whole screen; the bubbles say what waits for the user (a
  breathing accent halo, its core ringed in the same violet: one sign, « t'attend » in its core's words for a screen
  reader; out of view, an arrow at the screen's edge that opens it) and what runs. In the top-right corner, three icons: Archives (its
  count as a badge), what the user archived, each to reopen; the light or dark theme; the
  gear, the workspace's own settings (the graph's forces and its centring, `Settings.tsx`) and a
  link to all the settings (`/settings`); on a phone, where the graph is a list, the gear is that
  link alone. Under them, the notices and a word when the stream is down. A page shows its own
  path; closing the last one brings the bar back to the home.

A link may open a page: `/espace?open=node-<id>`. The theme follows the system, light or dark,
on the tokens of `espace.css`, until the user chooses one (`data-theme`).

**The scenario in a browser** (`web/espace/e2e`, `espace_scenario_test.go`). Playwright plays the
epic's steps, and a writing scenario (an article, its research on the side, its angle chosen by a button, its versions), against a whole eriphe-core on 127.0.0.1, signed in with the tests' password: faked
tools (`maison`, `agenda`, `mail`, `prod`, in memory) and a scripted brain. Nothing is faked inside
the core: the brain and the workers are tasks on a fake device `pc`, which takes its assignments
and reports as a device does, and plays canned calls through its MCP proxy (the brain's own tools,
the catalog's after the permission round trip, a worker's `eriphe-nodes`); the engine, the cards,
the filing and the stream are the real ones. `TestEspaceScenario` serves only when
`ERIPHE_E2E_ADDR` is set; the world outside the user is asked of it at `/e2e/mail` (a monitor's
mail), `/e2e/finish/{key}` (a worker finishes) and `/e2e/tools` (what the tools did), routes of
the test only. The test fails on any console error and any CSP violation. The browser asks for
less motion (`reducedMotion`): the graph and the camera are at rest at once, so the tests check
what the workspace does, not how it moves (the layout's and the camera's geometry are unit tests);
the few tagged `@motion` play with motion. Each test stands alone (`e2e/fixtures.ts`): signed in
once per worker, and the view reset before it, so a spec passes run by itself. CI runs it
(`espace-e2e`) on the committed build:

```sh
cd core/web/espace
npm run build && npx playwright install chromium && npx playwright test
```

**The scenario on a real model** (`nodes_bench_live_test.go`, #634, #707). The same world, the
brain a real model: eriphe-core starts it as on an install (the built-in prompt, its tools and free
reads), and the bench runs it with the local Claude Code CLI (`claude` signed in on the machine) in
stream-json as a device does, its MCP servers the test core's device API, its permissions asked
through a test-only `approve` tool. The CLI runs in a temporary folder, without the machine's
settings, `CLAUDE.md`, memory or MCP servers, and with no harness tool but the search for deferred
tools (no web, no shell). A skill's run (the view's skill) is run by the same model the same way;
the other workers are faked: a task writes a short result that fits its title in its node and
reports done, the first step held until the scenario lets it finish, the plan's first try blocked
once. It plays at most 15 turns of the user's and says yes on the cards; it measures behaviour,
never words: each run is scored from what the core holds and from the brain's calls to its tools
(seen by a receiving middleware on its MCP server), and printed as a Markdown table, one column per
run: lost replies (an answer of the user's turn code's safety net had to file, a turn left
unanswered, a question's answer written in no node), duplicate nodes (opened for a thing that has
one, two live nodes of one title), refused writes (split: their grammar or the catalog, against
taint or a stale version), the turn's node left with `turn_home`, the day's page written and shown,
a step's failure settled with `node_settle`, a tainted turn (tainted, the title it opened marked,
its `run_task` under the turn's node), the reply sent on its card, a child reused, an aside opened
below its subject without being shown, `node_set parent`, `[[links]]` written and resolved, a
glance, a mail watch shown first (its subject opened before any read, with how long it took to
appear, its view's draft linked to it and its card in the turn it is asked, no questionnaire, the
changes offered by ask buttons), approved and valid (its bilan lead, items in sections; its batch
reported), the view's skill noting the new mail (`item_note`) and the cards on its rows, and, last,
« Tu peux suivre et gérer mes journées ? » asked at the home (the seed's day page archived): its
subject first, its view's draft beside it and its card in that turn, no questionnaire, no title made
of the sentence. A quick question at the home (« Il est quelle heure à Tokyo ? ») opens no node. The test fails on a lost
reply, a duplicate node, more than one write refused for its grammar or the catalog, a view that is
not valid, more than 15 turns, or a page asked for not shown first (its subject not first, no draft
and card in its turn, or questions); the rest is reported.

```sh
ERIPHE_LIVE_NODES=1 ERIPHE_LIVE_NODES_RUNS=2 go test -run TestNodesBenchLive -timeout 0 -v .
```

`ERIPHE_LIVE_NODES_MODEL` chooses the model (`sonnet` by default), `ERIPHE_LIVE_NODES_CLI` the
command, `ERIPHE_LIVE_NODES_RECORD` a file the first run's node writes are recorded in.

**The replay** (`nodes_replay_test.go`). The node writes a live run recorded
(`testdata/nodes_replay.json`: `node_open`, `node_write`, `node_edit`, `node_set`, `propose_view`,
`item_note`, each with whether its turn was tainted and what code answered) are played again on the
scenario's core without a model, in CI: ids, hashes and card refs as placeholders, a call on a node
code opened and a piece of text replaced (which depend on what workers wrote) left out. Each call
must be taken or refused as it was, and what they leave must hold to the rules: a card block draws
its card. Its last turn, « tu peux suivre et gérer mes journées ? » (written by hand after a real
case until a run records it: the view proposed after a recall read a mail's fact), must open its
subject first, untainted, then propose its view in that turn, its draft linked to the subject and
its card on it, with no questionnaire; its yes makes a standing page whose `Agenda` reads the
calendar and whose buttons, written in the tainted turn, ask and are marked, and the subject done. Record it again with `ERIPHE_LIVE_NODES_RECORD=testdata/nodes_replay.json`
when the rules change on purpose.

**A full home** (#455). The workspace says the user is
there (`data/presence.ts`): it tells eriphe-core when it comes in front or leaves
(`POST /presence`, visible and focused, `tab`, `front`, `sub` its push subscription's id, the CSRF
token in the form and the header; every 20 s while in front; a beacon as it closes) and registers
the service worker (`/sw.js`). Its tab id is also in its stream's query (`tab`), so a notification
routed to it comes as an in-app notice, by the stream's `notice` or the presence answer: a small
card at the top right for 15 s, its text a link when its URL is a path of the site (`hud/Hud.tsx`).
A workspace left open in front is activity.

**The orb** (`web_orb.go`, `orb/`, #663). Not a chat: the subject's document is the
conversation, the bar under it is only a bar, and Eriphe answers with its orb, beside the subject
its answer belongs to. The orb only answers: the event carries nothing of a turn that answers no
act of the user's (an event's, a wake's, a node's task's: its work, its words, its failure,
its cards), nor any word at all before the user's first message. The user's acts are their
messages and their Oui or Non on a card: what came of a decision reaches the brain as an event
marked as the user's (`nodeEvent`, `messages.user_act`: a call run or refused on a card,
`callToBrain`; a release, `accessDecidedTold`; a task's start declined, `taskStartDeclined`), and
the turn that reads it answers the user (`deliverConversation`, `brainTurn.answersUser`), its
taint and context kept: its reply (« C'est fait », or what failed), its work and its failure are
the orb's as a reply to a message is, and replace what the orb said before (the newest marked
turn wins). The decision alone replaces nothing: a Non Eriphe says nothing of (`[rien]`) leaves
the orb as it was, and its work shows only once it writes words, never on thinking alone
(`lastActed` past `lastAsked`). One stream event for the whole workspace, whichever page the bar talks to,
so that nothing Eriphe says is lost on a page the user is not looking at: `orb` (JSON,
`orbEvent`, `orbFor`), sent when the stream opens and whenever it changes:

- `asked`: the seq of the user's last message Eriphe was given (`lastAsked`);
- `work` when Eriphe is at work (`brainState`) or writing in a turn that answers the user
  (`turnAnswersUser`): `node`, the node its turn answers in
  (`workNode`: the turn's talk, none at the home), and `draft`, the first line of what it writes
  (`ackLine`: the first line that says something, as plain text without its Markdown marks, a
  link's words only, at most 140 characters, its first sentence when that fits, else cut at a
  word with « … »);
- `answer`: Eriphe's words written since that message in a turn that answered it or a card's decision since (`markAnswer`,
  `messages.answer_to`), `{seq, node, text, more, whole, external}`: `node` where its messages belong (`placeOf`: the
  node of the message, else the one its answer was filed in, none for the home); `text`, its last
  messages there joined, said in a few lines (`fewLines`: at most 3 lines and 280 characters, no
  code, table or rule), `more` when that cut it (the rest is in the node: the brain writes it
  there, else the safety net files it); at the home, an answer cut is sent whole instead
  (`whole`, Markdown, at most `answerMax`: the brain's messages of a turn that answered the
  user's home message, `markAnswer`, `messages.answer_to`, filed nowhere); `external` when
  written in a tainted turn (`markBrainMessage`). An answer the user wrote past is no longer sent;
- `failed`: the last line of a turn that answered the user is an error (`{seq, node}`, never its
  raw text; `brainFailed` marks it as `markAnswer` does; a failure to start, or of an event's
  turn, is not the orb's);
- `cards`: the cards that wait and no node holds (`unheldCards`: shown already, linked to no live
  node, such as one proposed at the home before any subject), shown above the bar with their Oui
  and Non wherever it talks, so no card waits out of sight;
- `waiting`: the messages that wait (below).

Eriphe's lines are each brain turn's answer only, what it writes after its last tool call: a note
written before a tool call of the same turn is meant for no one and never copied, and a text stays
the draft until its turn's next tool call or its end decides it (`answerText`, `brain.go`). The
first run's messages are never shown. There is no history to page back through, no suggested
reply: the brain's questions and the buttons that answer them are `ask` buttons in the subject's
document. The brain's memory and the stored messages are untouched: only what the orb shows. The
cards the user must answer are in their node's page, and the attention queue delivers the others.

**The messages that wait** (`messages_waiting.go`). A message the user writes while Eriphe works
is stored undelivered; the brain's next idle hands it everything that waits, in one turn
(`deliverConversation`). Until then the bar shows it: every `orb` event carries the user's
undelivered messages, whatever node they were said to, oldest first (`waiting`: `seq`, `text` cut
at 500 characters, `node` and its clean `title`; the first run's never). While Eriphe is not at
work, a message younger than 2 s is left out (an idle brain takes it at once: no clock that only
blinks). The bar shows the newest three, each in grey with a small clock and the node it was said
to when not the one it talks to, then « +N en attente ». The stream rebuilds the event when what
waits changes (`waitingKey`, each round: a
delivery or a withdrawal adds no message). The ✕ on one is `DELETE /messages/{seq}` (CSRF token):
the message and its attachments are deleted at once, so the brain never reads it and the
conversation keeps no trace of it (the log keeps a line); `204`, `409` once Eriphe has it (or once
its plain yes or no decided a card, `resolveByText`, `messages.card_answer`), `404` when there is no such message of the
user's. The withdrawal and the delivery hold the same lock (`core.delivering`), the delivery from
what it reads to its mark: a message is either in the turn or withdrawn, never both. On a `409` the
bar says it was too late.

**The conversation per node** (#613, #632, `node_turn.go`). One brain, one
queue: every message of the conversation belongs to a node or to the home, in its `node` column
(`""`: the home).

- **A message said to a node belongs to it.** `handleSend` takes the node the bar talks to
  (`node`, checked by `nodeSaid`: one of the graph's, not archived; anything else is the home).
  The message is a turn like any other, the node named: the brain reads
  `[Sam 10:02 #12 · nœud <id>]` (its id only: a title may carry someone else's words, and the
  node's card gives the rest), and the turn is about it (`nodesDelivered`). A channel's, the CLI's,
  the API's messages belong to the home; a block's `ask` button, to the block's node
  (`handleNodeAsk`), as the user's message there, which never decides a card.
- **The brain's answer belongs to the node of what it answers**, fixed at delivery (`talkOf`, kept
  in the turn as `talk`): the last message of the user's in the delivery, else the last event of a
  node; the home for anything else. The node the turn names later (a tool on another node) is for
  its cards only. A failure of the turn (the error line) and the note that asks it again for a
  tool call written as text belong there too; a heartbeat's answer and a failure to start, to the
  home. An event of a node (a card linked to it decided, a task of it that ended its turn or
  stopped, a task's start declined or access decided) belongs to it; the catalog's, a machine's and
  the brain's own notes and notifications to the home. A long answer code filed says where
  (`filed`, [the safety net](#the-brain-on-nodes-node_toolsgo-node_turngo-node_netgo)).
- **Where it shows.** The stream (`GET /events`) is the same whichever page the bar talks to:
  the `orb` event says, for each of Eriphe's words, the node they belong to, and the orb goes
  there. A plain "oui" decides the card just above in its own node only (`resolveByText`).
- **Questions and their answers.** The brain writes its questions in the subject's document, each
  with its default, and below them a block of `ask` buttons for the likely answers, one taking
  every default (« Fais comme tu proposes »); a click sends the button's words, read again from
  the block, as the user's message in that node. On a tainted turn no button is written
  (`nodeGuard`). Each exchange rewrites the document: answered questions go, the choices are
  written under « Ce qu'on a décidé », the next step shows; a child that moves on updates the
  main subject too (the prompt's « Chaque échange réécrit le document »).

**The fallback.** The shell (`templates/espace.html`) has no JavaScript of its own: under
`#root`, until the app draws, a word, and a plain error with a link to try again, revealed by CSS
after 8 s (`.espace-fallback`) for a script that never loads or runs. The app shows it at once
(`.failed`) when its boot fails (`main.tsx`: a boot it cannot read is an error, never made-up data);
an error while it draws shows the same (`FailSafe`, `Fallback.tsx`). An event stream down for more
than 20 s (`streamDown`, `STREAM_GRACE`; the browser opens it again by itself, the page itself once
it closed for good, after checking the session) shows a line saying so; the workspace stays as it
was. A brain that could not answer says so with its orb (the `orb` event's `failed`), with
Réessayer (`POST /brain/retry`, 204) and a link to the agents' settings.

**The look** (`espace.css`, `fonts.css`): neutral greys, light or dark as the system says
(`prefers-color-scheme`; the shell's `color-scheme` and `theme-color` follow it), pages as flat
cards on a quiet ground, hairlines rather than shadows. Colour says a state, never a tool or a
subject: the accent for what waits for the user, hot for what is urgent or failed, warn for a
warning or someone else's words, ok for what is done; each has a fill and a text tone that reads
at WCAG AA on the card and on its own soft fill. Code's « extérieur » beside a block that may
carry someone else's words is a small tag attached to the block under it. One typeface, Geist
(Vercel, `geist` 1.9.2, unchanged): the sans as one variable font, the mono in its one weight,
under the SIL Open Font License (`web/espace/src/fonts/OFL.txt`), served by eriphe-core with the
app (`font-src 'self'`, nothing fetched elsewhere). With no node at all, a line says so
(`espace.empty`). There is no menu and no button that keeps a rule: what the user wants is said
to Eriphe.

A 401 sends the page to the sign-in. `web/espace/src/data/` is the one place that talks to
eriphe-core.

**A2UI surfaces** (`web/espace/src/a2ui/`, epic #415). The page renders [A2UI v1.0](https://github.com/a2ui-project/a2ui) surfaces with the workspace catalog's
components, ours, written in React. The engine is the official one: `@a2ui/web_core` keeps each
surface (components upserted by id, the data model, JSON Pointer bindings, child-list templates,
actions, validation) and `@a2ui/react`'s `createComponentImplementation` gives each component its
props, resolved and kept fresh. The tree is walked by the page (`Surfaces.tsx`), not by
`@a2ui/react`'s `A2uiSurface`, which wraps every component in a custom element whose style it sets,
and the library's basic catalog (Lit custom elements) is never imported: the DOM is our markup,
with classes only. `adapter.ts` is all the rest of the page knows: `applyMessages(msgs)` feeds it
the messages core sends (`createSurface`, `updateComponents`, `updateDataModel`, `deleteSurface`);
`onAction(cb)` and `onError(cb)` hand back A2UI v1.0 `action` and `error` messages, ready to send.
`catalog.tsx` names the catalog once (`CATALOG`: its id and where core serves it, `/espace/catalog/v1.json`)
and implements all 23 of its components, each with exactly the catalog's properties;
`catalog.test.ts` checks both ways against `a2ui/catalog_v1.json` (every component, its
properties and their allowed values, the icon names, the four functions). A node's page
makes a surface of each block (`doc/`, above). With no core behind, the dev server shows a
made-up graph (`data/sample.ts`), or with
`?gallery` the gallery (`a2ui/gallery.json`: surfaces using every component, in French and
English, which the component tests render and a Go test, `TestEspaceGalleryIsValid`, runs
through `a2ui.Validate`).

**The components** are shapes any tool's data can fill, never tied to one:

- *Layout*: Row, Column, List (a template over a list, `emptyText`), Card (the block's own
  frame at the root, a quiet box inside), Tabs (the WAI-ARIA pattern: one tab stop, arrows,
  Home, End; hidden panels stay bound), Divider.
- *Content*: Text (a `title` is a heading), Markdown (a small reader in `markdown.tsx` that
  makes React elements, along CommonMark and GFM: GitHub tables, in a box that scrolls when
  wider than the page; code spans literal, an underscore inside a word never emphasis, a lone
  backtick only a backtick; raw HTML stays text, images show their alt text, links only to
  http(s) or mailto, `rel="noopener noreferrer"`, in a new tab on the user's click), Image
  (loaded only from `/espace/media?…`, the proxy of core; any other address shows its alt
  text: a model's URL is never fetched by the page), Icon (a fixed set of inline SVG,
  `icons.tsx`, no font), Link (a page outside, opened in a new tab on the user's click only,
  `rel="noopener noreferrer"`, `referrerpolicy="no-referrer"`; its host shows beside the label,
  the whole address when it has none or the surface shows external content; an address the
  page's own parse does not take as http(s) with a host and no credentials shows as text: see
  [links](#surfaces-in-a2ui-a2ui)), Code (`code.tsx`: text as it is in a
  fixed-width font, an optional language label; `mode: "diff"` reads a unified diff, its
  files' headers, hunks, added, removed and kept lines each a class, a hunk's counts telling a
  removed `-- x` line from a header. One block per line, every line shown, long lines
  wrapped).
- *Input*: Button (with its icon), Choice, TextField, DateField; the fields write what the
  user enters to the bound data path, as A2UI does. A Button's action keeps to the
  actions' grammar (`a2ui/actions.go`: a Button sends `ask`, its author's words only, never the
  fields; nothing else); in a node's page eriphe-core reads the ask again in the
  stored block (`POST /api/nodes/{id}/ask`).
- *Data*: Metric (value, unit, a signed delta in ink with ▲ or ▼, a trend as a sparkline), Chart (lines or bars in
  SVG, `chart.tsx`: one axis, a palette checked for colour blindness, a legend from two
  series, a tooltip on each mark, the numbers in a table for screen readers), Table (columns
  `text`, `number`, `date`, `change`: a signed gap in ink, + or −), Timeline (`done`, `current`,
  `pending`, `failed`), Progress (a word in ink, a bar of steps whose done part takes the tone,
  the current step named), Message (short: the excerpt; full: the body, opened in place),
  Agenda (`agenda.tsx`, up to 7 days from `date`: the first by the hour on half-hour rows with
  the now line when it is today, a clash of events side by side, stacked when the block is narrow
  (a phone); the next day as lines, the days after as a strip; a `days: 1` Agenda of another
  date is by the hour too. Its events are in the shape servers/calendar returns — `title`,
  `start`, `end`, `place`, `allDay`, `status` — and an invitation to answer carries the amber
  sign of what comes from outside. With its day's
  [dated proposals as ghosts](#proposals-in-the-blocks-they-concern-card_movesgo), each with
  its Oui), Person (initials, what they are to the user, a note, facts as lines: a fact that is
  wholly a phone number or an e-mail address is a `tel:` or `mailto:` link, no other scheme),
  Quote (someone else's words as written, who and when, the page in clear), Alert (four
  levels), Placeholder (« En préparation », a slow shimmer). Changes are told in ink and a
  sign; colour says a state, never good or bad news.
- *Cards* (`page/Cards.tsx`, not a catalog component: no author writes a yes) draw a proposal of
  Eriphe's by its own ref (`kind:id`, the `proposal` of a
  card): `GET /api/proposals/{ref}` (`web_proposals.go`) gives its kind, text, why, fields
  and state in the user's language, where it comes from (`node`, `from`) and what its Oui does
  (`yes`) when it is an [access or a start's card](#the-approval-card), and the card that answers it (404 when no card carries
  it). A `summary` (a view's: one line of what will run by itself) is shown first; a
  field marked `detail` is under a collapsed « Détails », one click away and never removed, and
  one marked `markdown` (a skill's instructions) is drawn as Markdown in a box that scrolls. Its
  header says what the card asks, by its kind; it has one Oui and a discreet ✕, which answer the
  proposal's card (`POST /cards/{id}/yes|no`), then the proposal is read again, as it is whenever
  the stream says it moved (answered anywhere). Once approved, a held call's proposal shows what
  it is doing (`exec`: `running`,
  then `done` or `failed` with `execWhy` `notkept`, `gone`, `notallowed` or `error`, a reason of
  Eriphe's, kept as `call_state:<id>`, the proposal moved at each step; `execError`, the tool's
  own words, apart): « Validé · en cours… » while it runs; done or failed, the card is decided and
  draws nothing (`stillAsks`): the page's ⋯ menu lists it, and the orb says how it went.

Placement is classes only (the day's rows and lanes are `.a2-r-N`, `.a2-span-N`,
`.a2-lane-N`), never a `style` attribute. Fixed words are the app's (`espace.a2.*` in
`web_text_espace.go`), and numbers and dates follow the user's language: the adapter is made
for it (`createAdapter(lang)`), and so are the catalog's functions (`format.ts`:
formatString, formatNumber, formatDate, formatRelative, written here rather than taken from
the engine's basic catalog, which would bring its whole module along). A date is written as
its source wrote it (its wall-clock time).

**Upgrading `@a2ui/*`.** The packages are pinned to an exact version (`package.json`, no `^`) and
change often, with breaking changes in minor versions. Bump them only on purpose, in a pull request
of their own: read their CHANGELOG from the pinned version up, bump `@a2ui/react` and
`@a2ui/web_core` together, diff `A2uiSurface` and `NodeContent` in the new `@a2ui/react` against the
old (`Surfaces.tsx` mirrors them: `NodeResolver`, `ComponentNode`, an implementation's `view`), run `npm test` (the surface tests check rendering, data updates,
templates, actions, errors, and that no `style` attribute, `<style>` element, adopted stylesheet or
custom element appears), rebuild, and open the built page under the workspace's CSP to check the
console for violations.

**Building.** The app is React, Vite, Motion and the A2UI engine. Its built files are committed under
`web/static/espace/` and embedded with the other static files, so `go build` needs no Node:

```sh
cd core/web/espace
npm ci
npm test            # the layout's, the store's, the pages' and the A2UI components' tests (vitest)
npm run build       # type checks, then writes ../static/espace/espace.js, espace.css and the editor's chunk
npm run dev         # http://localhost:5300, on a made-up graph, no eriphe-core needed
```

The built app is what the user's workspace needs, nothing more: the dev server's made-up
workspace (`dev.tsx`: the samples, `?gallery`) is imported on the dev server only
(`import.meta.env.DEV`), and CodeMirror (`page/cm.ts`) is a chunk of its own, named by its
content (`espace-cm-<hash>.js`), loaded with the first .md tab; it imports nothing of the
entry's, which it would load again without its `?v=`. A Go test (`TestEspaceBundle`) checks both.
React draws the graph when what it shows changes; each frame of the simulation writes the
bubbles', pages' and links' places straight on their elements (`Graph.tsx`, `place`), so a
bubble is not drawn again as the graph moves (a test counts it). The viewport says the graph is
at rest (`data-still`) once it is asleep, nothing held, the camera still, no page springing open
and no page being read: the browser tests wait there (`e2e/rest.ts`), never on a fixed sleep.

CI rebuilds and fails when the committed files differ from the build. The words are in
`web_text_espace.go`, sent by `/espace/boot` in the user's language with the CSRF token;
`web/espace/src/strings.json` is their copy for the dev server, which a Go test keeps equal
(`ERIPHE_UPDATE_ESPACE=1 go test -run TestEspaceStrings` writes it again).

**CSP.** The page keeps the workspace's policy, with no `'unsafe-inline'`: the shell
(`templates/espace.html`) loads one script and one stylesheet; the app's words come from JSON.
No markup carries a `style` attribute, which the policy refuses (`style-src-attr`): Motion and the
layout place things by setting the element's style object from script (CSSOM), which a CSP does
not govern; `AnimatePresence`'s `popLayout` mode is
not used, since it injects a `<style>` element.

### Surfaces in A2UI (`a2ui/`)

A block of a node's page is written by a model in
[A2UI v1.0](https://github.com/a2ui-project/a2ui): a flat list of components, each with an `id`
and a `component` type, one of them the `root`; children named by id (`child`, `children`),
or a list template `{"path": "/mails", "componentId": "row"}` repeated for each item; values
literal, bound to the surface's data (`{"@path": "/sales/total"}`, relative to the item inside a
template) or computed by a function (`{"@call": "formatDate", "args": {…}}`). The package
`core/a2ui` speaks it and owns the catalog of shapes a surface is made of:

- **The catalog** (`a2ui/catalog_v1.json`, id `urn:eriphe:a2ui:workspace:v1`): a JSON Schema
  2020-12 document by A2UI's catalog rules. Shapes only, never tools, none named after a
  subject: Text, Markdown, Image, Icon, Link, Code, Row, Column, List, Grid (a gallery of cards,
  as many to a row as fit), Card, Tabs, Fold, Divider, Button (it only asks), Choice, TextField, DateField, Metric, Progress, Chart (`line`,
  `bar`, or `stack`: one bar per x, the series its parts, an allocation now against its
  target), Table, Timeline, Item, Message, Agenda, Person, Quote, Alert, Placeholder. No component says yes: a yes is
  a card, which code draws. Steps, notes, options, checklists and drafts are compositions of
  these (a `Timeline`, a `Markdown`, a `List` of `Item`, `Text` sections), never components of
  their own; and the functions
  formatString, formatNumber, formatDate, formatRelative. A property that carries data takes a
  binding (Metric's `value`, Chart's `series`, Table's `rows`, Agenda's `events`…). Its descriptions
  and `instructions` are for the models, in French. Two keywords of its own on a component's
  schema tell eriphe-core what it needs without naming a component (`a2ui/days.go`):
  `x-rows` names the property that, bound to a source's items, makes them rows (their cards are
  drawn there, not at the page's foot: the Agenda's `events`), and `x-days` says the component
  draws the dated proposals of its days (`a2ui.DaysShown`). Served to the app, with a session, at
  `GET /espace/catalog/v1.json`; a new version gets a new path.
- **Validation**: `a2ui.Validate(raw)` takes one agent message or an array of them (the whole
  life of their surfaces), `a2ui.ValidateSurface(a2ui.Surface{ID, Components, DataModel})` what
  an author writes of one surface, `Catalog.ValidateEvent(raw)` what the page sends back
  (`action`, `error`). First the published v1.0 schemas, embedded unchanged in `a2ui/spec/`
  with the catalog put in place of `catalog.json`; then what a schema cannot say: exactly one
  `root`, every child and template naming a component that exists, no cycle, ids that are
  identifiers (UAX #31) and unique in a list, the catalog's `allowedParents` /
  `allowedChildren`, relative paths and `@index` only inside a list template, well-formed JSON
  pointers, the surface's `catalogId`. Errors have A2UI's shape (`code` `VALIDATION_FAILED`,
  `UNALLOWED_PARENT` or `UNALLOWED_CHILD`, `surfaceId`, `path`, `message`; `.Event()` wraps one
  in the renderer's `error` message): the path points at the field, and a value that may take
  several forms gets one error about the form it was meant to be (the one whose `component` or
  `@call` it names), not one per form.
- **Prompt**: `a2ui.CatalogPrompt()` is a compact French section, written from the catalog
  alone, for any assistant that writes surfaces: the catalog's instructions and examples, each
  component with its properties (`*` required, the enum values, « ou liaison »), each function
  with its arguments. `a2ui/testdata/catalog_prompt.md` keeps its text
  (`ERIPHE_UPDATE_A2UI=1 go test ./a2ui -run TestPromptStable` writes it again).
- **Loading a catalog** (`a2ui.LoadCatalog`) applies A2UI's catalog rules: the definition
  schema, identifiers, each component's `component` const equal to its key, `$ref` only to the
  catalog itself and the allowed common types, everything listed in `anyComponent` /
  `anyFunction`. Nothing is ever fetched: a reference outside the embedded schemas fails.

The JSON Schema validator is
[santhosh-tekuri/jsonschema](https://github.com/santhosh-tekuri/jsonschema) v6: 2020-12 in full
(`unevaluatedProperties`, `$dynamicRef`), the official test suite passed, no dependency beyond
`golang.org/x/text`, structured errors with instance locations. Go's `regexp` knows no
`\p{XID_Start}`: the schemas' identifier pattern is compiled with the Unicode categories it is
made of. The conformance cases of the A2UI repository (`specification/v1_0/test`) run as Go
tests (`a2ui/testdata/conformance`, with their notice), as their runner runs them with ajv.
To move to a newer A2UI release, copy its `specification/v1_0/json` and test cases over these
and update the commit in the two `NOTICE` files.

### What a block may show (`web_graph.go`, `a2ui/`)

A block is drawn as its author wrote it, its data kept fresh by its [live sources](#views-node_livego-view_cardgo); what it shows goes through code first:

- **Links** (`a2ui/links.go`): a Link's address is never built, so that it cannot carry out
  what its author read (A2UI #4045): `url` is a literal or a binding (`@path`), never a function
  call (the schema has no such form: no `formatString`). A literal must be http(s) with a host,
  no user or password, no space, control character or backslash, 2 KiB at most
  (`a2ui.CheckLinks`, `a2ui.LinkURLOK`); a binding must hold such an address as a whole, in a
  source (a page's address a tool gives) or in the author's data (checked like a literal, in
  `checkBindings`); and every bound value goes to the page as it is or as `""`, no link
  (`blockView`: a source may later give anything). An external block may show links: unlike
  an image, nothing leaves without the user's click, and the page then shows the whole address,
  not only its host. Markdown links can still be built with `formatString`: not covered here.
- **Media**: the page never loads an address from outside (A2UI #4045; the CSP forbids it
  anyway). Each media address (an `Image`'s `url`, `a2ui.IsMedia`) is rewritten to
  `GET /espace/media?u=<address>&s=<signature>` (`web_media.go`, with a session): one a
  source gave, and one the author wrote unless the block is external (a model that read something
  from outside could carry it out in an address: dropped); anything but http(s) is dropped. The
  endpoint serves only an address eriphe-core signed (a key per process), fetches it on a public
  address only (the accesses' dialer: never its own network, the LAN or the cloud's metadata),
  without proxy, cookie or credential, 3 redirects, 8 MiB, an image (not SVG) or a video, with
  `nosniff`, a sandbox CSP and 10 minutes of private cache.

### Captured pages (`web_media.go`)

Most sites refuse being framed, and a block never loads an address from outside: to show a page, a
task with a node of its own captures it in its container (`eriphe-capture`, worker/base) and posts
the image to its device (`ERIPHE_MEDIA_URL`, the task's secret, for a task with `eriphe-nodes`
only), which relays it here with its own secret and the task's id:
`POST /v1/device/tasks/{id}/media?node=<id>`, the image as body.

- **Its own node only.** The node must be the task's (`task_nodes`): another, or a task with no
  node, gets 403; an unknown or finished task 404.
- **What is kept.** A PNG or a JPEG, sniffed from its bytes (anything else 415), 5 MiB at most
  (413), 20 files a node at most: in `files/<id>/med_<32 hex>.<ext>` of the graph's repository, one
  commit by the task's author (`nodestore.PutFile`). It is not a version of the node; it stays
  with it when it is archived. The answer is `{"src": "media:<name>", "type", "size"}`: the task
  writes `src` as an `Image`'s `url` in a block of its node. The bytes never go through a model.
- **Shown.** `blockView` signs `media:<node>/<name>` for the node the block is in (whoever wrote
  the block, external or not: the file is the node's own and the address carries nothing out), and
  `GET /espace/media` reads it from that node's files only, with the session: a block of another
  node naming the same file gets 404.

### A file the user sent, kept with a node (`node_attach.go`)

The photo of a shirt sent with « ajoute cette chemise à ma garde-robe », a recipe's PDF: the brain
keeps the file with the node of what it shows (#748) by giving `node_set` a property whose value is
the attachment's id (`props: {"photo": "att_…"}`, a list of them too). Code copies the file into the
node's media, as a task's image (`nodestore.PutFile`, by `brain`), and writes the property as
`media:<name>`; the answer gives it (`media`). No card, as any `node_set`.

- **What is kept.** Only an attachment of one of the user's messages, found by its id (never a path
  the brain names); an image (PNG, JPEG, GIF, WebP) or a PDF, sniffed from its bytes, 5 MiB at most,
  20 files a node; a text is written in the node's body instead. Each refusal says why, for the
  brain to tell the user; everything is checked first (the attachments, the node's files, the
  properties' names), so a refused write keeps nothing.
- **Trust.** The user's own file does not taint the turn, as `read_attachment`; one from a channel
  the user did not trust taints it, and the node is marked external.
- **Shown.** The node's page gives each property that names one of its files with its signed address
  (`pageProp.media`: an image shows as the node's picture, a PDF as a link that downloads it:
  `GET /espace/media` serves a node's non-image file as `Content-Disposition: attachment`). A
  block whose source is `eriphe-core/node_find` with such a column (`columns: ["photo"]`) shows
  each node's file: a `Table` as a thumbnail, an `Image` of a `List` as itself (`nodeFindMedia`
  signs `media:<name>` for the row's own node, and `mediaOrDrop` keeps an address it signed; an
  external node's columns stay withheld). The brain's `node_find` gives `media:<name>` as written.

### Proposals in the blocks they concern (`card_moves.go`)

A proposal shows where it matters (#428), with no code for any tool:

- **Placed.** An author places a card in a node's document with a card block (`{"card":
  "kind:id"}`), or code draws it on the rows of the items it acts on. Its Oui and ✕ answer that
  proposal and no other (its card, `POST /cards/{id}/yes\|no`). One drawn from external content
  carries a small badge (« extérieur »), its whole sentence on hover and for a screen reader.
- **Dated.** A proposal that has a time shows as a ghost in every Agenda that draws its day
  (unless the Agenda says `"proposals": false`, for days that are not the user's), with its Oui,
  answered through the same `decideCardWith`, whichever node the card belongs to. The Agenda is
  then the card's place: a node's page leaves it out of its foot when one of its blocks is an
  Agenda whose days eriphe-core can tell (a written or absent `date`, `card_moves.go`
  `datedOn`) and the card's span is on one of them. A ghost is drawn apart (« Proposé », « Proposé ·
  contenu extérieur » for one drawn from external content), placed by the hour with the events, saying which events it overlaps, which are marked too. A
  ghost only shows: the card answers. The time is generic: an intention's due
  date, or, for a call a card keeps (a held write, a run's calls), its arguments that are a date
  or a date-time, by the tool's input schema (`format` `date-time` or `date`), or, the schema
  saying no format, enum or pattern, by the whole value (RFC 3339, `YYYY-MM-DDTHH:MM[:SS]` in
  the user's zone, `YYYY-MM-DD` for a whole day); the earliest starts it, the latest ends it. No
  name of a tool or of an argument is read. A run's card keeps several calls: each dated one is a
  ghost, all answering the one card. `GET /api/proposals?day=YYYY-MM-DD` gives the day's
  (`{"day", "proposals": [<proposal view with when>]}`, the times with the user's offset; 400
  without a day); `GET /api/proposals/{ref}` gives `when` too.
- **Moved.** A card made (`cardProposed`, `cardNow`, `cardQueued`), decided (`decideCardWith`, the
  one way any answer goes: its buttons, a channel, a plain "oui", or a page through
  `answerProposal`, `TestOneWayToSayYes`) or settled when its proposal moved elsewhere
  (`settleCards`) notes its ref (`proposalMoved`, the last 64 kept). The
  workspace's stream sends them as a `proposals` event, `{"refs": [...], "nodes": [...]}` (`nodes`:
  the nodes their cards are linked to; `{"all": true}` for a stream further behind), at once
  (`watchProposals`); the page reads again each card of those refs, a node's page only when one
  of its node's cards or a proposal it shows moved, and every Agenda's ghosts. The first `proposals` event of a connection, after the
  snapshot, is `{"all": true}`: what the page shows may have been decided while it was away.
  The limits of a time read from values: an unrelated date argument (a recurrence's end, say)
  stretches the span, and a call with a start and a duration but no end shows a half-hour
  ghost.

### The catalog tools (`node_catalog.go`)

Two tools, to compose a node's blocks (what is followed continuously is a
[view](#views-node_livego-view_cardgo)):

| Tool | Does |
|---|---|
| `catalog_read` | the whole grammar of a block, in French: `a2ui.CatalogPrompt()` (the components) then `nodeGrammar` (`node_grammar.go`: the block's envelope and its card block, its live sources and `every`, their `items` and `open`, the fields code writes on each item, the sections' filters, a batch's `Fold`, the `glance`, and an example checked by its test). Kept out of every turn, read on demand: the prompt and the tools' descriptions point to it instead of restating it |
| `sources_read` | what code may read for a block (`workspace_sources.go`): each read-only tool of the brain's grant (`server/tool`, its description cut to 300 characters, whether it is a free read now, whether it gives its result's shape) and each server's resource; with `tool`, that one with its input and output schemas (4 KB each at most). It reads nothing and lends nothing: code reads them under the access policy (`sourceWhy`) |

- **Who has them.** The brain, on its own tools (`/mcp`), and every worker task with a node, beside
  its node's tools on `eriphe-nodes` ([a task's node](#a-tasks-node-task_nodego-task_nodesgo)),
  allowed in advance: they only read what code holds, so they are never lent and taint nothing.
  A task's captured pages go to its own node ([Captured pages](#captured-pages-web_mediago)).

### Signing in

The workspace has one owner; how they sign in is set in Settings → Access → Web workspace → Connexion, or in
the configuration file's `web.auth`, which then wins (the page shows the settings with a
configuration tag and cannot change them).

| Mode | |
|---|---|
| **Password** (`password`, default) | The owner's password, created with the setup code at first start. |
| **Identity provider** (`oidc`) | Any OpenID Connect provider (Authentik, Authelia, Keycloak, PocketID…). Give its issuer, the client ID and secret, the workspace's address (`external_url`, e.g. `https://eriphe.example.org`), and who may enter: the provider's account ids (`allowed_subs`) and/or emails it says are verified (`allowed_emails`). Redirect URI: `<external_url>/auth/callback`. Authorization code flow with PKCE (S256), state and nonce; the ID token's signature, issuer, audience and expiry are checked ([go-oidc](https://github.com/coreos/go-oidc)). Signed-out visitors go to the provider at once, unless `oidc.button` asks for a sign-in button. |
| **Reverse proxy** (`proxy`) | The proxy signs in and passes the user in `Remote-User` (or `proxy.header`). The header is believed only from the proxy's addresses (`proxy.trusted`, addresses or CIDRs), and for the users listed (`proxy.users`); it is needed on every request. |
| **None** (`none`) | No sign-in at all: only for a machine nobody but you can reach. The page lets you choose it only from the local network (not through a proxy); otherwise, on the machine: `docker exec eriphe-core eriphe-core auth none`. |

**Visitors who may not enter** (an account the provider signed in but that is not allowed, a
request without the proxy's user, with another user, or from an address that is not the
proxy's) are sent to `unauthorized_url` when it is set (e.g. `https://www.example.org`), and
otherwise see a page "Cet espace est privé" / "This workspace is private".

**Always a way back in.** In `oidc` and `proxy` modes the password still opens the workspace
from the local network: the browser's address and every hop in `X-Forwarded-For` and
`X-Real-Ip` must be private, loopback, link-local or Tailscale (100.64.0.0/10, fd7a:…) and the
browser's not eriphe-core's default gateway, the request must not come from a declared proxy, and
a request with a `Forwarded` header never counts. `no_local_password` turns that off. And from the machine:

```sh
docker exec eriphe-core eriphe-core auth reset    # or: eriphe-core auth reset -config core.json
```

sets the workspace's mode back to the password (its other settings stay) for the running
eriphe-core at once. When `web.auth` is in the configuration file, change its mode there.

**A forgotten password.** Root on the host is the owner:

```sh
docker exec eriphe-core eriphe-core auth password-reset   # or: eriphe-core auth password-reset -data dir
```

forgets the owner's password and ends every session of the workspace, keeps everything else (the
sign-in settings, the assistant, its memory, devices, accounts), and prints a new setup code; the
running eriphe-core asks for it at `/setup` from the next request. `eriphe-core auth setup-code`
prints the setup code while there is no password, and fails once there is one.

**From the machine, too.** Whoever is root on the host owns the workspace, so the identity
provider can also be set up there, without the page's test sign-in:

```sh
printf %s "$CLIENT_SECRET" | docker exec -i eriphe-core eriphe-core auth oidc \
  -issuer https://auth.example.org/application/o/eriphe/ -client-id eriphe -client-secret-stdin \
  -allow-sub 1f2e3d4c -external-url https://eriphe.example.org
docker exec eriphe-core eriphe-core auth show     # the mode and its settings, never a secret
```

`auth oidc` reads the provider's discovery document and changes nothing if it fails. The client
secret is read from stdin only (never from the command line, never printed) and kept in the vault
(`vault:workspace-oidc`), as the page does; the settings are stored as the page stores them, with
the password kept from the local network, and the running eriphe-core uses them at once. It prints
a one-line summary with the redirect URI to register at the provider. Options: `-allow-sub` and
`-allow-email` (comma-separated, or given again; at least one of them), `-external-url` and
`-unauthorized-url` (kept from before when not given), `-button` (a sign-in button rather than
going to the provider at once), and `-data dir` or `-config file`. `auth oidc` and `auth reset`
refuse a configuration file that sets `web.auth` (change it there); `auth show` then shows the
file's settings, and says so.

> Your proxy must add `X-Forwarded-For` (Traefik, Caddy and nginx's `proxy_add_x_forwarded_for`
> do). If the workspace is published on the internet without a proxy, turn the local password
> off: Docker's port publishing may show visitors with a private address (eriphe-core does not
> count its gateway as local, but another relay on the way could hide them too). Better: do not
> publish it on the internet at all (install/README.md, Eriphe on a VPS).

**Changing the mode keeps you in.** Settings → Access → Web workspace → Connexion shows the four modes as
cards, and below them only the chosen mode's settings (the others stay in the form, so it saves
the same without JavaScript); "Aucune" shows a warning instead. "Tester" reads the
provider's discovery document; "Essayer la connexion" signs in once at the provider with the
settings of the form, and tells who it signed in (with their `sub`, to copy into the allowed
accounts). The identity provider's mode can be saved only after that test passed as an allowed
account, with the same settings; the proxy's only from a page that comes through the proxy as an
allowed user, or while the password stays usable from where you are. The client secret is kept
in the vault (`vault:workspace-oidc`) and never shown back.

```json
"web": {"auth": {
  "mode": "oidc",
  "external_url": "https://eriphe.example.org",
  "unauthorized_url": "https://www.example.org",
  "oidc": {"issuer": "https://auth.example.org/application/o/eriphe/", "client_id": "eriphe",
           "client_secret_file": "vault:workspace-oidc", "allowed_emails": ["me@example.org"]}
}}
```

| Field of `web.auth` | What |
|---|---|
| `mode` | `password` (default), `oidc`, `proxy` or `none` |
| `external_url` | the workspace's address (Settings → Access → Web workspace → Adresse de l'espace): the provider's redirect, the tools' and Google Drive's OAuth redirect, and an origin the forms may come from behind a proxy that changes the host |
| `unauthorized_url` | where visitors who may not enter go; the private page without it |
| `no_local_password` | `true`: no password from the local network in `oidc` and `proxy` modes |
| `oidc.issuer`, `oidc.client_id` | the provider's issuer (exactly as it announces it, trailing slash included) and the client |
| `oidc.client_secret_file` | a file, or `vault:<name>`; none for a public client |
| `oidc.allowed_subs`, `oidc.allowed_emails` | who may enter; at least one is needed |
| `oidc.button` | `true`: a sign-in button rather than going to the provider at once |
| `proxy.header`, `proxy.trusted`, `proxy.users` | the user's header (`Remote-User`), the trusted proxies' addresses or CIDRs (for every mode: see below), who may enter |

**With Authentik.** Applications → Create with provider: an **OAuth2/OpenID** provider,
confidential client, redirect URI `https://eriphe.example.org/auth/callback` (strict), the scopes
`openid`, `email` and `profile`, signed with a certificate (RS256). The issuer is
`https://auth.example.org/application/o/<application slug>/`. Copy the client ID and secret into
the form, "Tester", then "Essayer la connexion": its message gives your `sub` (Authentik's is a
hash of the user by default) to allow; an email counts only if Authentik sends
`email_verified: true`. Bind the application to your user or group, so the others are stopped
at Authentik already.

**Behind a reverse proxy.** The workspace serves HTTPS itself (port 8443): point the proxy at `https://eriphe-core:8443`,
trusting the self-signed certificate (Traefik's `serversTransport` with `insecureSkipVerify`, or
its fingerprint), and set the workspace's address. The [reverse proxy guide](../docs/reverse-proxy.md)
has Traefik, Nginx Proxy Manager and Caddy.

- **The workspace's address** (Settings → Access → Web workspace → Adresse de l'espace, or `web.external_url` /
  `web.auth.external_url`, one value): the provider's sign-in comes back to it, the tools' and
  Google Drive's OAuth redirect URI is `<address>/oauth/callback`, and forms sent from it are the
  workspace's own. A sign-in or an authorization started from another address (the LAN one)
  would come back without its cookie: the provider's sign-in goes to the address first, and
  Connect says to open the workspace there. Without an address, the page's own, as before; the
  panel shows the tailnet's when remote access is on.
- **Trusted proxies** (the same panel; `web.auth.proxy.trusted`, one list with the proxy
  sign-in's): from these addresses only, `X-Forwarded-For` gives the browser's address (the last
  hop that is not a trusted proxy, for the login limiter), and `X-Forwarded-Proto` and
  `X-Forwarded-Host` the origin the browser used (the forms' check). From anywhere else these
  headers are never believed. The password from the local network still never counts through a
  proxy (above).
- **The devices** keep talking to eriphe-core on port 8800, not through the proxy. The panel's
  *second address for the devices* (e.g. the NAS's Tailscale address, `http://100.64.0.2:8800`)
  goes into the "Connect a device" commands after the local one, as remote access's address does
  (`--core LOCAL,SECOND`; every installer takes the list). A device already connected takes it
  when its install command runs again with `--core LOCAL,SECOND` and no token.

Logging out with a provider shows the sign-in page with its button: going to the provider at
once would sign you in again; sign out at the provider too to end that session.

**Headers.** A strict Content Security Policy (no inline script or style, no `eval`, `frame-ancestors
'none'`), `X-Frame-Options: DENY`, `nosniff`, `Referrer-Policy: same-origin`, and HSTS when
serving a configured certificate.

**Certificate.** Without `cert_file`, eriphe-core creates a self-signed ECDSA certificate at first
start (`web-cert.pem`, `web-key.pem` next to the database, the key 0600), logs its SHA-256
fingerprint, and makes a new one when it nears its end (397 days) or misses a name. Browsers warn
about it once; compare the fingerprint before accepting it. In a container the machine's own
addresses are the container's: list the NAS's address and names in `hostnames` (or set
`public_url`). An installable app (PWA) and push notifications need a certificate the phone
trusts, either:

- **Remote access** (below): eriphe-core gets its own certificate for its tailnet name, nothing to
  renew;
- **Tailscale on the host**: `tailscale cert <machine>.<tailnet>.ts.net` writes a Let's Encrypt certificate
  for the machine's tailnet name; point `cert_file`/`key_file` at it and open the workspace at
  that name (renew it every 90 days, e.g. from cron);
- **an own CA** (e.g. `mkcert` or `step-ca`): install the CA's root on the phone and the
  computers, issue a certificate for the NAS's name and address, and point `cert_file`/`key_file`
  at it.

**On a phone, as an app.** The workspace installs as an app (PWA): its manifest
(`/manifest.webmanifest`) carries the assistant's name, its icons (maskable and monochrome
included), `display: standalone`, `start_url` and `scope` `/`; the pages add the Apple touch icon
and meta tags. Installed, it draws under the status bar and around the notch, and keeps its bars
out of the safe areas; fields are 16 px at least on a phone (Safari zooms into smaller ones); the
console stays above the keyboard (Chrome resizes the page for it, `interactive-widget`; on iOS the
page follows the visible viewport). The service worker (`/sw.js`,
served with the static files' version so that each update brings a fresh cache) keeps the static
files only: versioned ones cache first, the others network first; a page is always fetched, and
offline a small static page says so. No page, action or event stream is ever cached.
Settings → Access (App mobile) tells how to install it on this device (iPhone, Android or a
computer), with the browser's own prompt when Chrome or Edge offers one.

**Notifications.** Standard Web Push (RFC 8030, encrypted per RFC 8291, signed with VAPID per RFC
8292; `push.go`, with [webpush-go](https://github.com/SherClockHolmes/webpush-go)). eriphe-core
makes its VAPID key pair at first start (`push-vapid.key` in the data folder, 0600, never logged)
and gives the public key to the page (`GET /push/key`, or the page's own attribute). In Settings →
Notifications, the switch "Me prévenir sur cet appareil" asks the browser's permission, then
subscribes and sends the subscription (`POST /push/subscribe`; `POST /push/unsubscribe` removes
one); the page lists the devices notified, each with a remove button and "cet appareil" on the
one in hand, and sends a test to it. Three things are pushed, titled with the assistant's name, in
the language the device used:

| When | Text | A tap opens |
|---|---|---|
| a worker task finished its turn, or failed (or was lost) | « Tâche finie : <title> », « La tâche « <title> » a échoué » | that task's page (`/tasks/<id>`) |
| a permission waits for the user (one that is theirs, or any while the assistant is away), or the assistant ended a message on a question | « <Name> attend ta réponse : <tool and command, or the question> » | the permission in À valider (`/settings/approve#a-perm-<task>-<id>`), or the message's node (`/espace?open=node-<id>`) |
| the brain tells something on its own (`notify`), or answers a due intention, and the decision below says now at weight 5 | its text, with 👍 / 👎 / « pas maintenant » buttons where the platform shows them | that notification in Settings → Notifications, with why |

Each is pushed once, and not at all while a workspace tab is visible and focused: every open tab
tells eriphe-core when it comes in front or leaves (`POST /presence`, and every 20 s while in
front; a tab silent for 45 s no longer counts). Everything goes through one function (`tell`),
for what else will notify later. A push service that answers 404 or 410 has the subscription
removed. The service worker shows the notification and, on a tap, focuses an open workspace window
and takes it there, or opens one, on the workspace's own origin. Push needs a trusted HTTPS origin
(see the certificate above); on iPhone and iPad it reaches the installed app only (iOS 16.4 or
later), while Android and computers receive it in the browser too.

**The brain's notifications** (`notify.go`, `notify_rules.go`, `notify_quiet.go`). The brain has
four tools: `notify(text, weight 1-5, why, ref)`, `notifications` (what it said, what waits
for the brief, the feedback, the rules, the quiet hours, the known topics, the settings), `rule` and `setting`. The model writes and weighs
on the attention scale (5 interrupts, 4 needs the user now, 3 matters today, 2 a glance, 1
background); the code decides (`decideNotify`, no model), and the notification enters the
[attention queue](#workspace-web) at its decided weight (outcome `now`, `held` or `never`): from
weight 4 it is delivered at once (written in the conversation), and only a 5 is pushed to a
device; a 3 waits for the next breakpoint; a 1 or a 2 for the user's return, the brief. Stored
outcomes `brief` were rewritten `held` at start.

- **Quiet hours always hold.** The window is a rule (data): for every night, or for the nights
  before a weekday or a weekend day, 4 to 16 hours long; without one, the seed 22:00–08:00. A
  weight 4 or 5 inside it waits for the user's return (weight 2), unless a topic rule of the user's says "even at night", or it
  is the fixed message of a skill the user marked urgent (`urgent`). A weight 5 from the model alone
  never goes through.
  The time zone is the configuration's, or the one set in Settings.
- **A daily cap** of pushes (weight 5; 4 by default, 0 to 20 in Settings), besides reminders the
  user asked for in their own words; over it, the next breakpoint (weight 3).
- **Topic rules** bound the weight of what is about a node or a key (`project:getout`, which also
  matches `project:getout:prod`) or a kind of key or record alone with its colon (`project:`, `skill:`; a word without one is a
  topic, `mail` is `topic:mail`; the longest match
  wins): a floor raises it, a ceiling lowers it, a ceiling of 0 is never. A turn that read external
  content may be lowered, never raised: what it notifies never interrupts, waits for the next
  breakpoint at most, marked as such, and may hold no link.
- **The answer to a due intention** is pushed through the same decision, question or not, once per
  wake; a reminder from an intention drawn from external content is held.
- Every notification is kept (`notifications`) with its why, weight, outcome and reason. What is
  held is listed for the brain and in Settings, and waits in the attention queue for its release.
  Feedback (👍, 👎, « pas maintenant », which holds it again for the user's return) is given in Settings or on the push
  itself (`POST /notify/react`, authorised by the notification's own token, which only its push
  carries), and returned to the brain per ref.
- **Rules are written by the brain only when the user asked in that clean turn** (its `source_ref`
  cites the user's message of the turn, which speaks of being told); otherwise, and always on a
  tainted turn, they are proposed and the user approves them in À valider (a card in the conversation); Settings →
  Notifications shows every rule in force, to delete (a new or changed one is asked of the brain).
  Since #244 step 5 the topic rules and the quiet hours are rules too: rows of `rules` with source
  `notification` (their kind, match, floor, ceiling, nights, lines and hours in the JSON `spec`; `act`
  `deliver` or `quiet`), read by `notifyRules` only, never fired by the rule tick.
- **Adaptive quiet hours, never applied alone.** eriphe-core records cheap signals of activity (the
  user's messages but a channel's they did not trust, a tab in front (the conversation's or the workspace's), in 10-minute buckets
  kept 30 days, reactions to notifications, gestures on the workspace's nodes but `ignored`,
  `gestureActivity`) and of unwelcome moments (« pas maintenant »), and suggests a window per kind of
  night from the last 14 days: the median last activity of the evening plus a margin, the median
  first of the morning, widened over repeated « pas maintenant » just outside it; too few days, no
  suggestion. The brain sees it in `notifications` and proposes a quiet rule; nothing changes until
  the user approves. Further signals (a Home Assistant sleep sensor, the phone's focus mode) join
  `activitySources`. The same signals give the user's last activity (`lastSeen`, `awaySince`), and
  `notificationsSince` what was held since then, each with its source, time, why, ref, outcome and
  feedback: what a brief or a point made on the user's return summarises.

- **One topic vocabulary** (`topics.go`). A rule matches what a notification is about (set by
  the code, or given to `notify`: its node or key), else its record's ref,
  so labels must not drift: an about is kept by `aboutKey` ([About and names](#about-and-names)),
  on both sides of the match; a record's ref (`skill:<name>`, `intention:<id>`) as written. The
  known topics (rules, notifications of the last 90 days, the nodes running or waiting, the memory's
  `node:`, `project:`, `person:` and `client:` abouts) are listed in
  `notifications`; a new label of a known word family is kept under the known topic, and a new one
  is flagged to the brain with the known ones.
- **Settings through the brain** (`settings_brain.go`, tool `setting`): the device order, the lines
  off, the delay before the next line, the daily cap, the time zone and a channel's detail. Applied
  only when the user asked in that clean turn; otherwise kept in `setting_proposals` and approved in
  À valider, then written to the same keys Settings uses (one place per setting).

**The brain's concepts** (`concepts.go`). One concept per need: memory (what the brain must know),
intention (owed or to do once), rule (what comes back or reacts on its own: when, if, then; and
how to be told about a topic, quiet hours), setting (how to be told in general), notify, since,
fact, task, session. What the code enforces is
never a memory: `recall` returns the matching rules typed as rules, and the tools refuse the
wrong concept with a hint naming the right one (`remember` of a notification preference → `rule`
or `setting`; a one-off time → `intend`; something recurring → a skill with `when`). The concept
bench (`concepts_test.go`) checks, without a model, that each request's right call is taken and its
wrong one redirected, and that the map is in the MCP instructions: given there once, in English,
the prompt points to it.

The interruption bench (`notify_eval_test.go`, `testdata/notifyeval`) runs with the tests: it scores
the decision on labelled cases (accuracy, precision and recall of "now", night wake-ups no rule
allows: none, injection cases blocked: all).

**Channels** (`channels.go`): how the user and Eriphe talk, one notion apart from the tools, which
act. Each channel has its own connection and secret. eriphe-core treats every channel the same:
what the user writes from one joins the conversation as from the workspace, and what goes out is
routed by the same code. The workspace and push are eriphe-core's own; a service eriphe-core talks
to for the user, such as a messaging app's bot, is an adapter that registers itself as one more
channel (`registerChannel`), with its name, its direction, who speaks, what it carries, its line,
its setup page and how it sends a reply, a card and a notification. A messaging app where the user
links their own account to a bot is a **bot channel** (below): it registers with `registerBot` and
writes only its transport. Nothing outside the adapter names it: the router, the settings, the
brain's tools and prompt, the cards and Settings → Access go through the registry
(`TestBotChannelsNamedByTheirFilesOnly` checks it).

| Channel | Direction | Who speaks (inbound) | What it carries (outbound) |
|---|---|---|---|
| `workspace` (and the CLI) | receives | the user: their session or token | |
| `push` | notifies | | the notification's text |
| `telegram` (an adapter) | both | the user's account, verified with a code | the notification's text, with buttons |
| `discord` (an adapter) | both | the user's account, verified with a code | the notification's text, with buttons |
| `signal` (an adapter) | both | the user's account, verified with a code | the notification's text, with a legend its reactions answer |

Trust has two levels: trusted (the user's session, their verified account, an app the user said
they trust), whose words are the user's and whose buttons answer every card, a task's permission
too; or not (an app anyone near may speak through), whose turn is tainted. The user adds API
accesses, trusted or not, saying a sentence in the user's room or nothing (above, "Channels the
user adds"). A notification or a rule may name only the channels it is broadcast on (`push` and
the adapters', by id).

**What goes back to a channel the user writes from** (`channel_relay.go`), the same for every
adapter: the brain's messages that follow a message the user wrote there, until their next
message from elsewhere (`channelReplies`); the cards that go with them, and those a task waits on
(`channelCards`); how much it is told, per channel (`channel_detail:<id>`: `full`, or `alert`, a
nudge and a link instead of any text; Settings → Notifications and the brain's `setting`
`channel_detail`, "telegram alert"); and a task's permission, answered with `answerPermission` as
the workspace does.

**Push and the bot channels** (`channels.go`, `bot_channel.go`, `bot_channel_cards.go`,
`web_bot_channel.go`, `templates/bot.html`). What is sent (`tell`) goes by the user's channels: web
push and each adapter, all by default, a narrower default in Settings → Notifications (Où te
prévenir), or a topic rule's own (`channels`: `push`, `telegram` or both). The decision above is
unchanged; the channels only say where a "now" goes.

A bot channel is two-way, without an inbound port. Everything below is the bot channel's, once,
for every service; a service writes only its **transport** (`botTransport`: check a token, listen
one round, send a message with buttons, answer a button), its words (`acc.kind.<id>`,
`acc.kind.<id>.desc`, `<id>.how`, `<id>.privacy`) and its `registerBot` (`botSpec`: id, label,
icon, longest text, longest button data, the token's form, a link that opens the bot with the
code). Its routes (`/settings/access/<id>`, `/channels/<id>/…`), page, row, store keys
(`<id>_user`, `<id>_chat`, …) and vault secret (`<id>-bot`) follow from its id.

- **Setup.** The user creates a bot and pastes its token in Settings → Access: checked by the
  transport, kept in the vault, never shown, logged or put in an error (the bot channel strips it
  from whatever the transport returns). Eriphe then shows a one-time code (10 minutes, 10 wrong
  tries); the private-chat account that sends it to the bot is recorded, user id and chat id. Only
  that user id is heard; any other sender, group or bot is ignored, counted in Settings and never
  answered. Unlink, change or remove the token (a new bot asks for a new link).
- **Listening.** The transport listens one round at a time (a long poll, a connection until it
  drops); after a failed round the wait goes from 1 s doubling to 5 min (or what the service asks
  for); it runs only while a token is set, and stops on removal.
- **Out.** A notification carries buttons 👍 / 👎 / « pas maintenant » (`fb:<id>:<r>`, accepted
  from the linked account only, recorded like the push's); a question waiting for the user is told
  with a link to the workspace, a task's permission with Allow and Deny
  (`pm:<task>:<permission>:<y|n>`, answered as the workspace's buttons, a user_only one too; none
  when longer than the service's button data). The brain's replies go back there
  (`channel_relay.go`). "Alerts only" sends "Eriphe a quelque chose pour toi" and a link instead of
  any text, replies included, and no buttons that approve.
- **In.** A text from the linked account joins the conversation as the user's message, `via` the
  channel's label (`[Sam 09:12 #41 · Telegram]` for the brain): a clean turn, because the sender's
  id is checked. Files, photos and voice notes are not read; the bot says so.
- **Proposals as cards.** A proposal of the brain's (a rule, a memory, an intention, a setting)
  that goes with a reply or a notification sent there comes as a card with Oui / Non buttons
  (`pc:<card>:<y|n>`), answered from the linked account only, through the same code as the
  workspace's cards; none in "alerts only". The [access card](#the-approval-card) of a catalog
  tool goes there too.

**Telegram** (`telegram.go`), the first transport: the user creates a bot with BotFather; the
token is checked with `getMe`; `getUpdates` long polling (50 s, Telegram's `retry_after`
followed); `sendMessage` with an inline keyboard (4,000 characters, 64 bytes of button data);
`answerCallbackQuery`, then `editMessageText` once decided; the code also comes as
`/start <code>`, from `t.me/<bot>?start=<code>`. The Bot API puts the token in the URL: its
errors name neither. Telegram bots are not end-to-end encrypted (a bot chat is never a secret
chat), and Settings says so.

**Discord** (`discord.go`), the second: the user creates an application with a bot in Discord's
Developer Portal and pastes its token (checked with `GET /users/@me`, sent in the
`Authorization` header only). Discord lets a bot and a user write to each other in direct messages
only when they share a server: once the token is set, the page links to the bot's invite
(`oauth2/authorize?client_id=<bot>&scope=bot&permissions=0`), then the user sends the code to the
bot in a DM. Listening is one Gateway connection per round (a WebSocket eriphe-core dials: no
port): Hello, heartbeats at Discord's interval (a missing acknowledgement drops the connection),
Identify with the `DIRECT_MESSAGES` intent only (a DM's content comes without the privileged
message content intent), Resume after a drop or op 7, a new session after op 9 or a close code
that forbids resuming; 4004 (the token) and 4014 (the intents) are failures Settings shows.
`MESSAGE_CREATE` in a DM is the user writing (the bot's own messages are dropped; a server's
are not the user's DM: ignored); `INTERACTION_CREATE` is a tap (Discord sends buttons over the
Gateway while the application has no interactions endpoint URL). Out: `POST
/channels/<dm>/messages` (2,000 characters, link previews off, mentions never ping), buttons as
components (100-character `custom_id`); a tap is answered by its interaction's callback, which
needs no bot token: the message updated without its buttons once decided, else an ephemeral note.
A 429's `retry_after` is followed. Direct messages with a bot are not end-to-end encrypted, and
Settings says so.

**Signal** (`signal.go`), the third. Signal has no bots, so Eriphe gets an account of its own, with a
number of its own (a landline works: Signal verifies it by a voice call).
[eriphe-signal](../servers/signal/README.md) holds that account. It is the channel's own service
(`botSpec.Service`, [Services](#services-local-mcp-servers)), named `channel-signal`, and runs
while Signal is on, outside the catalog: in no list of tools, under no access level, reached by the
channel alone through its `serviceClient`. The service is one of Eriphe's own images, which runs
eriphe-core's version, on an amd64 device only (its signal-cli is x86_64 only), with a volume for the
account's keys. It is wanted while `signal_service` is on. That is read from the store, so a
reconciliation before the channel starts keeps it. When its device reports it running, the channel
listens again at once.

There is no token: the number registered stands for it (`botSpec.Account`). Until then, the page's
setup (`botSpec.Setup`) takes its place:

1. Turn Signal on.
2. The number, and SMS or call.
3. Signal's captcha, when it asks for one: solved on `signalcaptchas.org`, its `signalcaptcha://`
   link pasted.
4. The code, with the number's PIN if it has one, and a new PIN to lock it.

Then the linking is the bot channels' own; the page links to `signal.me/#p/<number>`.

Listening is one `receive` per round, a long poll of up to 50 s from the last `seq` seen; a message
in a group is not the user's chat. Out: `send` (2,000 characters).

Signal has no buttons. A message with buttons ends with a legend that gives each an emoji by
position (`👍 Oui · 👎 Non`, `⏳` third). A reaction with that emoji to that message is the tap
(skin tones aside). The emojis of the last 50 such messages are kept by their timestamp
(`signal_buttons`), and dropped once decided. A tap is answered with a short message.

Removing the channel unregisters the number, deletes its data in the service, then turns the
service off. Messages are end-to-end encrypted up to the service, which keeps them and the
account's keys on the device, and Settings says so. Untested against a real account: the tests fake
the service.

**Where to reach the user** (`route.go`, `route_signals.go`, `route_send.go`, `web_where.go`).
A pure function, `route(note, signals, settings, tried)`, chooses one device at a time from signals
eriphe-core already has, never from the model nor the notification's text:

1. Eriphe open and in front on a device (its tab tells its push subscription with `POST
   /presence`) → a banner in that page (the event stream's `notice`, or the presence answer), no
   system push;
2. a computer in use (its device reports the machine's idle time under 2 minutes, not locked, not
   to be disturbed: a game or a meeting in front, the context below) → a push to that computer's
   subscription only;
3. the voice in the room: a channel the user added that says a sentence, listening and reaching
   the room a fresh (under 2 minutes), personal signal of Home Assistant puts the user in (below),
   for a "now" from a clean turn,
   outside the quiet hours, on a line the user turned on (off by default); it says a sentence the
   code writes (« Sam, Eriphe a quelque chose pour toi. »), never the notification's text;
4. otherwise the phone (subscriptions whose label, the user's choice or the browser says phone;
   the installed app first), then each adapter's own line (Telegram), in the order registered.

A tab left in front on a machine its device says is idle, locked or not to be disturbed is not the
user. A "now" not seen
(opened, answered, or shown in a page) within the delay (10 minutes, Settings; 3 for an
"even at night" topic) is routed again without the devices tried, two steps at most, never in the
quiet hours unless the topic's rule says even at night. A brief, a task done or a question is sent
to one device and not moved on; a test goes to its device. A topic rule keeps channels
(`channels`), adds lines to the first step (`also`: "Getout: the phone too") or forbids some
(`never`); a note from a tainted turn never gets a line added nor moves on. Settings →
Notifications (Où te prévenir) orders the lines (each shown once what it needs is connected, one switch each), sets the
delay, says which subscription is a phone or a computer and which device is on which computer
(suggested by system), and lists the last decisions (`route_log`, planned next to sent). It starts
in **observe** mode: everything goes as before (every channel, nothing while a tab is in front)
and the log shows what the router would have done; one switch turns routing on. What a device tells
of its machine is kept as context signals, in memory for two minutes, never stored. The `notifications` tool gives the brain the order
and, by device, what was seen fast, late or moved on, from which it proposes rules. The bench
(`route_eval_test.go`, `testdata/routeeval`) checks every case, no duplicate, every injection
blocked.

**The user's state and what happened** (`user_state.go`, `since.go`, `since_tool.go`,
`web_since.go`). Each of the brain's turns opens with the time (`[maintenant …]`), the user's
language (`[langue de l'utilisateur : …]`, see "The user's language" under [Workspace](#workspace-web)) and the
user's state, built by code: `[état de Sam : présent, de retour à 14:50 après 4 h 30 d'absence
(parti à 10:20) · heures calmes à 22:00 (22:00–08:00) · notifications « maintenant » restantes
aujourd'hui : 3 sur 4]`. Present or away comes from the activity signals above: no activity for an
hour is away, and a gap of an hour or more is an absence. "Busy until" comes from `busySources`,
none yet: reading a connected calendar on every turn would cost a call each time and taint every
turn; a source that keeps free/busy times only (never titles) joins there.

**What Eriphe observes of the machines** (`observe.go`). No context subsystem: with its status
(`GET /v1/device?context=1`), a device tells `idle` (seconds, locked) and `context` (`{known, dnd,
reason}`, worked out on the machine: `device/context.go`; eriphe-core never asks level 2, so the
app in front is never computed). Kept **in memory only**, for two minutes, never stored with the
device's info; a reason off its list (`game`, `meeting`, `fullscreen`, `mic`, `focus`) is dropped.
Settings → Devices shows it on each machine (in use, idle, locked, do not disturb). The router
reads it for the device tied to a subscription: a device locked, idle or not to be disturbed is
skipped by every line; a machine not to be disturbed also holds the cards for a better moment. It
only ever makes Eriphe quieter. The user's situation ("in Lyon until Sunday") is dated memory
(`situation`); anything else (who is home, the next meeting) Eriphe reads from a tool when it
needs it.

**Where the user is, for the voice.** A channel that says sentences tells, with its open request
(`GET /v1/channel/out?…&here=Salon`), where the user is now, among the places it reaches
(`channel_out.go`). Home Assistant's integration works it out with the house's presence,
occupancy and motion sensors; eriphe-core knows no sensor. The voice line is used only while such
a channel listens and says a place.

**Where a notification goes: `where_now` and `notify`'s `where`.** `where_now` gives the brain what
the router sees: each line and whether it reaches the user (and on which device, channel or
room), the devices as their machines say, the voice's room and the quiet hours. `notify(…, where)`
puts that line first (`routeNote.First`, a closed value); without it the router keeps its order.
The lines off, the quiet hours and the rules still win, and a tainted turn's `where` is ignored.

At start, the signal context's and Home Assistant follower's tables and settings (`ha_mappings`,
`ha_follows`, `context_sources`, `context_level`, `context_off`, `context_rooms`) are dropped;
`POST /v1/context` is gone.

- **`since(t, kinds?, limit?, include_external?)`** returns one chronological feed since `t` (an
  RFC3339 time, `YYYY-MM-DD[THH:MM]` in the user's zone, `away`: since the user left before their
  return, or `last_brief`, from the kv `last_brief` the brief writes on the user's return): tasks (started; turns
  finished with their receipt's verdict in numbers, waiting, failed, stopped, from the append-only
  `task_log` written where the brain is told), notifications (sent, held, dropped, feedback),
  intentions (due, done, snoozed, cancelled, from `intention_log`), the user's and the brain's
  messages (short; a brain message sent as a notification is that notification). Each item has a time, kind, event, summary written by code, a ref
  for drill-down (`task:`, `notification:`, `intention:`, `msg:`) and a trust, `own` or
  `external` (a notification or brain message written in a tainted turn, an intention drawn from
  external content, a task started on a tainted turn).
- **Taint.** By default `since` quotes only own items and names external ones without their text,
  so reading it does not taint the turn; `include_external` quotes them too and taints the turn
  (`since.external`), as reading an external memory does.
- The **Journal** (`/settings/journal`) shows the same feed to the
  user, by kind, since they were last here (the start of their last absence; the last 24 hours if
  none), or the last 24 hours or 7 days, external items marked.

**No rules any more** (#278). What Eriphe did on its own through event rules (a schedule, the
user's return, a polled tool, an incoming alert, a fact, a task's end), with their standing
approvals and urgent watches, is a [triggered skill](#triggered-skills) now: `when` names the event
or the time, the skill's instructions say what to do, and its run tells the user with its last
message. The brief, the return point and the weekly review are skills too (`when: time daily
08:00`, `when: user.returned`), whose runs read what happened with `since` (`t: last_run`).
What happens outside is detected by a [monitor](#monitors): one that polls a tool that does not
tell by itself, never a time skill that calls it; or one that listens on an incoming address.

## Remote access (Tailscale)

eriphe-core can join the owner's private Tailscale network (a *tailnet*) by itself, with an
embedded Tailscale ([tsnet](https://tailscale.com/kb/1244/tsnet), BSD-3): the workspace and the
API are then reachable from every device of that tailnet, at home and away, in HTTPS with a
certificate browsers trust, and nothing is opened on the router. It is off by default and turned on
from the workspace, **Settings → Access → Espace web** (owner only; its entry's badge is the address
the workspace is opened at, the remote access's state under it):

1. **Turn on remote access** starts Tailscale (machine name `eriphe` by default).
2. **Log in to Tailscale** opens Tailscale's login page (a free account with Google, Apple or
   Microsoft, or an existing one). If the tailnet approves devices, the page waits for it.
3. **Enable HTTPS (once)**: a one-click consent page of Tailscale turns on HTTPS certificates in
   the tailnet (MagicDNS must be on, as it is by default). Like every public certificate, the name
   `eriphe.<tailnet>.ts.net` then appears in the public Certificate Transparency logs. The page
   then says "Preparing the certificate" while Tailscale issues it (up to a minute the first
   time): the address shows once it is there, so the first visit does not hang.
4. **Ready**: the address `https://eriphe.<tailnet>.ts.net`, a QR code for the phone (drawn in Go,
   no outside service), the links to install Tailscale on the phone, and how to add the workspace
   to the home screen.

The page follows the state live (an event stream). The setting is kept in the database and
Tailscale starts again with eriphe-core. On the tailnet, eriphe-core serves:

| Port | What |
|---|---|
| `443` | the workspace, with HSTS, only for its tailnet name (another `Host` is redirected or refused) |
| `8800` | the API (devices, CLI, MCP), HTTP/1.1, with the same certificate; a device presents its own (see TLS between machines) |

The local listeners do not change: remote access adds the tailnet, it closes nothing. So when the
page is opened at a public address, or from one (not through a declared proxy), it says so in a
banner rather than "only on your local network", with what to do: close 8443 and 8800 in the
hosting provider's firewall, then go through the tailnet or a domain behind a reverse proxy
([install/README.md, Eriphe on a VPS](../install/README.md#eriphe-on-a-vps)).

The certificate is fetched and renewed by tsnet on the
handshakes (Let's Encrypt, via Tailscale). The OAuth redirect of a tool connected from the tailnet
address is that address's `/oauth/callback`.

**Key expiry.** A device logged in with the login link must log in again after 180 days (the
tailnet's setting): the page shows the date, and a banner 14 days before with **Log in again**.
To avoid it, give eriphe-core an auth key of a tag (below): tagged devices do not expire.

**Advanced options.** The machine name; the **coordination server** for
[Headscale](https://headscale.net) (its URL; changing it forgets the current identity; Headscale
has no automatic certificates, so HTTPS stays at the HTTPS step unless the server provides them);
an **auth key** (`tskey-auth-…`, or Headscale's pre-auth key), kept in the vault as
`tailscale.authkey` and given to tsnet only until the identity has logged in once (a new login);
**Log out of the tailnet**, which logs the device out at the coordination server (with remote
access off, tsnet is started for that, without the key, 30 seconds at most), deletes its state and
the saved auth key, and turns remote access off; if the coordination server does not confirm it,
the page says to remove the device in Tailscale's console. **Turn off** keeps the identity, so
turning it on again needs no login.

`eriphe-core remote-address [-config file | -data dir]` prints the workspace's tailnet address
(`https://eriphe.<tailnet>.ts.net`) while remote access is on and has been ready once, and fails
otherwise: the installers print it when they run again.

**State and privacy.** Tailscale's state is in `tailscale/` next to the database (0700): it holds
the device's private keys, so it is a secret like the vault key; never copy it into a repository
or a backup that leaves the house. Userspace networking (no TUN device, no capability), so it runs
in the read-only container with `cap_drop: ALL`; it only needs outgoing UDP and HTTPS. tsnet's
log upload to Tailscale is turned off; its messages go to eriphe-core's log, with auth keys and
login links removed (whoever opens a login link first decides which tailnet Eriphe joins: it is
shown in the workspace only).

**Security notes.**

- Nothing is public: there is no Funnel. Only the devices of the tailnet reach eriphe-core, and
  the workspace still asks for its password.
- Restrict who reaches it with the tailnet's ACLs, e.g. a tag for eriphe-core and access for the
  owner's devices only (use the tag's auth key so the device is tagged):

  ```json
  "tagOwners": {"tag:eriphe": ["autogroup:admin"]},
  "grants": [{"src": ["autogroup:member"], "dst": ["tag:eriphe"], "ip": ["443", "8800"]}]
  ```

- A device away from home uses the operating system's Tailscale app and the tailnet address of the
  API (see the device's README, several addresses).
- The embedded Tailscale only **receives**: eriphe-core's own outgoing calls, and its tasks', do
  not go through it. A model server at a tailnet address (`100.x`, `….ts.net`) is reachable only
  when the host itself runs Tailscale; otherwise the provider's test says so. Lend it from a device
  instead ("On one of my devices"), which needs no route.

## Vault

Secrets can live in eriphe-core's database, encrypted with AES-256-GCM (each value
authenticated with its name), instead of files. The key comes from `ERIPHE_VAULT_KEY` (32 bytes
in hex or base64) or from `vault_key_file` (default `vault.key` next to the database), created
with mode 0600 at first start. Keep the key out of backups of the database, or with them in a
safe place: without it the vault does not open. A key that does not open what the vault holds
is refused at start (and the vault stays closed).

Every field that names a secret file also takes `vault:<name>`: `token_file`, a provider's
`secret`, a brain MCP server's `token_file`:

```json
"providers": {"openrouter": {"kind": "openrouter-api", "secret": "vault:openrouter"}}
```

The workspace's entries keep their secrets here, typed in their own forms (see
[Dynamic catalog](#dynamic-catalog-workspace)). Settings → My workspace → "Clés et mots de passe enregistrés" lists what the
vault holds and what each secret is for, replaces or deletes one, sets a `vault:` reference of the
configuration that has no value yet, and copies a secret file of the configuration into the vault
in a click (then replace its path with the `vault:` reference shown); the API does the same. Values
are never shown back nor logged.

| Call (bearer token) | What |
|---|---|
| `GET /v1/secrets` | names and dates, never values |
| `PUT /v1/secrets/{name}` | `{value}` to set or replace, or `{file}` to import a file on eriphe-core's machine |
| `DELETE /v1/secrets/{name}` | delete |

## About and names

What a record is about, its **about**, is a node (`node:<id>`) or a key: an entity of the memory
(a person, `person:lina`; the user, `owner`; the home; a tool, `tool:x`; Eriphe's own topics,
`topic:workspace`, `topic:journal`, `topic:heartbeat`) or a matter remembered (`project:site`,
`pr:owner/repo#12`, `alert:backup`). Notifications, attention items, cards, tasks and
card pushes (core.db), memories and intentions (memory.db) carry an `about` column next to their
ref (which record: `intention:12`, `skill:<name>`).

- **Keys** (`about_key.go`): a key is `kind:slug`, made by one function (`aliasKey`) for every
  store that keeps one; `aboutKey` takes `node:<id>` too (its id checked, `nodefile.IDOK`). A test
  forbids any other normaliser. Kinds: `project`, `person`, `client`, `pr`, `alert`, `home`,
  `owner`, `tool`, `topic`. A French or plural name is an alias (`projet:` → `project:`, `alerte:`
  → `alert:`); an unknown kind or a bare word is a topic (`brand:velora` → `topic:brand-velora`,
  `factures` → `topic:facture`). `owner` and `home` stand alone. A name's slug is lower case,
  accents folded, spaces as hyphens, its sub-parts kept (`project:site:prod`); a topic's single
  word is singular; a pull request is `pr:owner/repo#12`, lower case; an alert's id is kept as
  written. **A mail or an event is an item** of a node's source (`item_note`), never a key:
  `mail:` and `event:` are refused. A ref (`card:12`, `task:t-…`, `intention:5`, `skill:<name>`)
  names one record, never a key.
- **The names index** (`names.go`, core.db `names(name, target, seen)`): each key written is
  noted (`noteAbout`), naming itself; a key is no node's alias (#632: nodes have no aliases). A
  row that names another target is none of the code's: dropped when the store opens. What a turn
  is named about, and a card's about, is only a live node or a key (`validAbout`): anything else
  (a stale target, a node that is no more) is none.
- **Named in a message** (`named`): a message of the user's names an entity by its key's words
  (project, person, client, tool, topic), compared by their stems, three letters at least; never
  `owner`, `home`, an id or Eriphe's own topics. A name inside a longer one named too gives way to
  it (`site` to `site web`), five at most. What is named goes to the brain under the message
  (`[nommés] Ce message nomme : project:site, person:lina.`, keys only), and the turn is about it
  (`brainTurn.namedRefs`, kept with the turn) when it names one alone and the turn is about no
  node. A person's intention cue is matched the same way.
- **An about card** (`about_card.go`): `aboutCard(about, budget)` compiles, with no model, in
  about 1000 characters, what eriphe-core knows of a node (or an entity): the node's line (its
  state, title and its doc's first line), its last 3 decisions, its facts and people (with the
  people its intentions name), its open intentions, the cards that wait for the user about it
  (`convo_cards.about`, or what a card proposes), its preferences. The budget is shared in rounds
  (each part's first line, then each part's second…), and what does not fit is said. Only its ref
  heads it, and a line that may carry someone else's words (an external memory or node, a tainted
  card or intention) is named, not quoted: a card never taints a turn.
- **Cards pushed**: the code gives a card without being asked. To the brain's turn, after the
  named block, when what the turn is about (`turnAbout`) has a card its session does not hold as
  it is now: the session's cards are kept by hash (`AssistantStatus.Cards`, like the profile), set
  to the pack's on a fresh session and to the cards given since. To a fresh session's context
  pack, one per node running or waiting. To a worker task's first prompt, after what it is asked (its shown
  prompt unchanged). A brain on a self-hosted model gets
  600-character cards. Each card pushed is logged: `card <about> → <place> [<task id>]:
  <chars>, <lines>, <left out>`.
- **Cards measured** (`card_use.go`): each card pushed is counted in `card_pushes` (about, place:
  `turn`, `pack`, `task`; size; the brain's turn; the task) and
  judged once its window is over (30 minutes for the brain, 2 hours for a task), with a
  proxy said as it is: **wrote** when the model it went to wrote or changed a record about it in
  the window (the brain: a memory, an intention or a version of the node written by `brain`; a
  task: a memory its `remember` wrote, `task:<id>`, or a version of the node written by a task or a run); else **answered** when it was the
  card of what the turn is about (the turn's, or that one of a pack delivered with the turn) and
  the turn's answer, the first message after it, is the brain's, without asking for it again in
  the turn (`recall` about it or naming it, `node_read` of it); else **no**. A pack's other
  cards, and a fresh session's first pack, are no turn's: only wrote counts for them. The tasks'
  reliability panel shows the share of the tasks' cards used. Pushes are kept 90 days.
- **A record's about, set by the code**, and one given explicitly wins:
  - **the brain's turn** (`turnAbout`): its node (`brainTurn.node`), else the one entity the
    user's messages name; two named give none, and a clock wake none. What the brain makes in the
    turn takes it: `notify`, `remember` (a fact, a decision, never a replacement, which keeps the
    one it replaces; never a profile, a preference, a person, a lesson or a situation), `intend`,
    `run_task`, a card it proposes;
  - **a maker** (`sourceAbout`): a node (`node:`) gives itself, a task (`task:`) its about;
    notifications and signals of a task take its about, a card raised by a task or a node its
    maker's, an attention item its source's (an offered card's, its card's);
  - **a card** takes its proposal's first (a memory's, an intention's, a skill run's);
  - **a skill's run** takes its events' key where it names a thing: a monitor's (`monitor.*`:
    `topic:<name>`) or a key written `kind:slug`; a task's or a node's id gives none.
    Its notifications, signals and cards follow its task;
  - **the extraction** reuses a task's about (shown on its segment) and a node's trace's
    (`node:<id>`): a decision added from a task is the task's whatever was guessed, anything else
    takes it when none was given; a replacement keeps its own, the day's journal `topic:journal`.
  Notification rules match what a notification is about, else its record's ref (`itemRule`).

## Memory

Eriphe's own memory, separate from the vault, in `memory.db` next to the database (SQLite, full
text search with FTS5, accents ignored). It points at the vault (`links`) rather than copying it.
A memory has a kind (`profile`, `preference`, `fact`, `person`, `episode`, `lesson`,
`decision`, `situation`; what is owed is an intention, never a memory; where a matter stands is
its node's doc, never a memory), an about (see [About and names](#about-and-names): a node
`node:<id>`, or an entity `owner`, `person:<slug>`, `client:<slug>`, `home`…), dates (`valid_from`, `valid_to` once no longer true, or planned for
what is temporary; `occurred_at` for an episode), where it
comes from (`source_kind`, `source_ref`: `msg:N`, `task:<id>`, `node:<id>`), a trust level (`owner`,
`first_party`, `external`), and for what the extraction derived a `basis`: `stated` (said in so
many words), `inferred` (deduced) or `ambiguous`. A change over time supersedes the old memory,
which stays, dated; every change is in `memory_log`. Forgotten memories are purged after 30 days.
An episode is the day's journal (or a wake of the brain's, `topic:heartbeat`), never a task's: what
a task did is its log and its node's. memory.db says its schema in `meta`
(`schema_version`, 8): a new file is made at it, one at 7 is brought to it (`migrateAbout`), a file at another is refused, never changed.

**Durable state.** Raw history is kept for audit (the conversation, `memory_log`, the journal
episodes); what the brain needs to act is compiled from it in facts, lessons, **decisions** (a
choice and its reason, superseded when reversed; a node set done writes one). Where a project,
client job or goal stands is not a memory: it is its node's doc (see [The brain on
nodes](#the-brain-on-nodes-node_toolsgo-node_turngo-node_netgo)).

**En ce moment** (`situations.go`). A `situation` is where the user is or what they do for a
while (« en vacances à Lyon jusqu'à dimanche »): about `owner` by default, and a `valid_to`,
required (a date alone is its whole day). The situations valid now, active only (one drawn from
someone else's content is proposed and shows once approved), newest first, at most 10, open
every brain turn under `[en ce moment]`, each with its end, and the instructions of every worker
task (`append_system_prompt`, at its start). Nothing typed: the brain writes one when the user
says it, a triggered skill when it reads it (a calendar, a home). When its end passes, it is
gone.

**Tasks read it.** Every worker task has a memory server of its own, `eriphe-memory`, relayed by
its device (`/v1/tasks/<id>/mcp/eriphe-memory`; the name is reserved in the catalog): `recall`,
current facts only, allowed without a card. A triggered skill's run also has `remember` (fact,
person, decision, situation; never where the work stands, which its node keeps, never an episode, the user's profile, preferences or
lessons): `source_kind` `task`, `source_ref` `task:<id>`, first party when the task is clean,
`external` and only proposed when it read someone else's content.

The brain's tools (on `/mcp`): `remember`, `recall`, `update_memory`, `forget`, and for what is
owed `intend`, `intentions`, `update_intention`; for what it learned `learned` and `unlearn` ([below](#what-eriphe-learned)). Results are data, quoted.

**Intentions** (prospective memory) are a table of their own, with a lifecycle kept by code: a
kind (`promise`: the user owes someone; `commitment`: the assistant owes the user; `waiting_for`:
someone owes the user; `reminder`), who owes and to whom, a deadline (`due_at`), when to bring it up
(`remind_at`, by default the deadline; a deadline given as a day lasts until that day's end and is
reminded at 9:00) or a person `cue` ("the
next time I talk about Hélène"), and a state: `proposed → open → due → done / cancelled`, `snoozed →
due`. The brain and the nightly extraction form them and revise them with typed patches (done,
cancelled, a new deadline); the extraction's patches never come from external content. Every
minute, without a model, open intentions whose reminder has come and snoozed ones whose snooze is
over become due; a person cue becomes due when the user's message names them, and the delivery of
that message carries the intention (`[mémoire]`). A due intention wakes the brain through the
`clock` trigger (the brain has it with `conversation`): at its next idle, after the user's
messages, it gets the wake packet (what is due now, what was told and is still due, the profile,
the last 24 hours, the heartbeat lessons), says it in a sentence or two or `[rien]`, and the wake
is kept as a `heartbeat` episode. Each intention wakes it once, and once more at its deadline if it
is still due; never periodically. An intention drawn from external content stays `proposed` until
the user approves it; approved, it may remind, never act: the wake turn that reads it is tainted
(`intention.external`), and so is a turn whose `intentions` tool returns one. The Memory page lists
them (Intentions) with done, snooze (an hour, tomorrow 9:00), cancel and reopen; proposed ones are
approved or rejected in À valider (a card in the conversation). `remember` refuses
what is owed (`commitment`, `promise`, `reminder`…) with a hint to `intend`.
An assistant whose spec lists its `triggers` gets the clock only if `clock` is among them.

**Search.** The full-text index holds each memory's text, about, `keywords` (other words to find
it by: synonyms, variants) and, in a shadow column, the French stems of all three (Snowball French
in Go, plus the plural *s*), so "anniversaires" finds "anniversaire". A query is the question's
words without the question words ("quel est le nom de") nor the user's name, each matched as
typed (a prefix from four letters) or by its stem; any word matches, BM25 ranks. The ranking
then weighs relevance (BM25 over the candidates, the best at 1) by bounded factors: the about
named (×1.5), the profile (×1.3), the `importance` 1–5 (×0.8 to ×1.2), and for an episode its
age (30-day half-life) down to half its relevance only, so an old memory that matches well still
comes back. A time in the query, read by code from explicit expressions (hier, avant-hier, la
semaine dernière, ce mois-ci, le mois dernier, début/mi/fin septembre, en mai, le 19 septembre,
il y a trois jours, a date; and their English forms), reorders and never filters: a day is widened
by two days on each side, memories of that time come first (×1.6), facts that were true then are
candidates even if no longer true, and facts not true then are lowered. A question that is only a
time ("que s'est-il passé hier ?") lists that time. Current facts only otherwise, unless
`include_past` or `as_of`. `docs/memory-design.md` keeps the reasons and the measures.

**Trust is enforced in code.** Only the user's own messages (`source_ref` naming a message of role
`user`) create or change a profile, preference or lesson. A brain turn that called an external
catalog tool not marked trusted (seen by the proxy, or in the turn's events for servers it reaches
directly and the web tools; the [one taint rule](#tool-catalog)) is tainted: what it remembers is only `proposed`, unless the user's message of that
same turn asked for it. A tainted turn also cannot act: `run_task` lending tools
that are not all free reads (a write, a server that asks everything) is refused;
a task with reads only, or no catalog tool, still starts. The user's own message of that turn
does not lift this. Only a delivery holding a user message starts a clean turn: a delivery of
events only (a task's permission, its end) goes on with the current turn and its taint, since
what the brain read is still in its context; so does a resumed brain session. Every write goes through a scanner: instructions to a model, invisible
characters, credentials and links carrying tokens are refused; imperative wording not from the
user is only proposed. Proposals wait in the workspace (À valider, and a card in the conversation
but for the extraction's); undecided after 14 days, a proposed memory is forgotten and a proposed
intention cancelled (the nightly run, `expireProposals`).

**Reading back is reading too.** A memory of trust `external` (a skill run's fact read in a mail, a
proposal from a mail the user approved) summarises what was read outside: a turn whose `recall`
returns one is tainted (`memory.external`), like a turn that read the mail
itself. The context pack and the wake packet, which nobody asked for, name such a memory (its id,
kind and source) without quoting its text. And what the brain writes during a tainted turn is
marked: the extraction reads it as `external`, so a mail repeated by the brain is not learnt back
as its own conclusion.

**What it knows about the user.** The Memory page opens on "Ce que Jarvis sait de toi" (the
assistant's name): the profile and the other current memories about the user (about `owner`,
episodes aside), the pinned ones first and marked, with the cards' usual actions,
and the button that does the [first-run interview](#workspace-web) again. The list below leaves them out
(each memory shows once); a search or a filter finds them there too.

**The profile** is the pinned memories, at most 2,500 characters (enforced on write). It is
appended to the brain's prompt when its session starts, and sent again, quoted, with its next
messages whenever it changed. Claude Code's own auto-memory is turned off for the brain
(`CLAUDE_CODE_DISABLE_AUTO_MEMORY=1`): one memory.

**Extraction and journal.** Every night at `extract_at` (and once a busy day's conversation has
been quiet `idle_minutes`), eriphe-core writes out the day's transcript (messages, finished
tasks and what the day wrote in each node's trace, each labelled with its trust; a task that read
someone else's content is left out, its log and its node keep what it found, and a clean one is
first party; so is a node that may hold someone else's words) and starts a `memory` task (not listed, never the brain's)
with a cheap agent and the memories it may touch. Its JSON answer (ADD, SUPERSEDE, NOOP, each
citing a `source_ref` of the transcript, with `keywords`, an `importance` 1–5, a `basis` and,
for what is temporary, a `valid_to`) is checked and applied. Its regime, kept for what serves the
work:

- **One episode a day**, the journal (about `topic:journal`); an episode for a task is refused.
- **Never a profile, a preference nor a lesson**: only the user's own words make one, through the
  brain's `remember` during the conversation.
- **What it is sure of is applied on trial**: stated, not drawn from external content, not worded
  as an instruction. It is active at once and recorded in the [learning journal](#what-eriphe-learned)
  (status `trial`, provenance `extraction`), which is its digest: the wake and the brief tell it,
  « Ce qu'Eriphe a appris » lists it with « Annuler », `unlearn` undoes it.
- **The rest is proposed, without a card** (inferred, ambiguous, external, flagged): it waits in
  À valider (the learned page says how many) and is dropped after 14 days undecided.

What is owed is an intention (INTEND), as above. Each segment carries its date, against which the
agent resolves "demain" or "vendredi". One run per day and window; a failure is retried the next night. The same night
run purges and backs up (`VACUUM INTO` plus a JSONL export, also downloadable from the page, and
the [readable mirror](#the-readable-mirror-memoire)).

```json
"memory": {
  "db": "/data/memory.db",
  "agent": "haiku",
  "extract_at": "03:00",
  "idle_minutes": 30,
  "busy_day": 20,
  "extraction": true,
  "backup_dir": "/backups/memory",
  "backup_keep": 14
}
```

All keys are optional: `db` defaults to `memory.db` next to `db`, `agent` to the catalog's `haiku`
if there is one, else the default agent. Trusted tools (whose results do not taint a turn) are
marked in Settings → Tools (see the [one taint rule](#tool-catalog)).

### The readable mirror (`memoire/`)

`memory_mirror.go` keeps a readable copy of what Eriphe knows in `memoire/`, next to `core.db`
(`/data/memoire/` on an install), so that it can be read, or copied into another program, outside
Eriphe. `memory.db` stays the truth: the mirror is written by code only, again after every change
committed (a memory added, changed, approved, superseded, forgotten, restored, purged, a learned
change undone), each minute for what time changes (a `valid_to` that passed), and at start, from
the database (a file changed by hand is written over, a file of its own removed). A file whose
text did not change is not written; a file no memory is in any more is removed, and its folder
when empty. Files are mode 0600, the folder 0700, in the user's language:

- `profil.md`: the profile, the preferences, what is about the user (about `owner`), the
  lessons, and the situations with their end;
- `personnes/<name>.md`: one file per person (kind `person`, or about `person:<name>`);
- `faits/<subject>.md`: the facts, by their about (`general.md` without one);
- `decisions/<YYYY-MM>.md`: the decisions, by month;
- `journal/<YYYY-MM-DD>.md`: the episodes, one journal a day.

What is in it is what `recall` gives by default: the active memories true now, never a proposal
nor a forgotten one. A line is one memory, its text on one line whatever it holds, ending with its
id (`^m12`, Obsidian's block id); one read in someone else's content (trust `external`) is marked
`(extérieur)`.

It is **outside git**, so that « oublie ça » stays true: a forgotten memory leaves its file at
once, and stays only in the nightly backups, which carry the mirror (`memoire-<day>.tar.gz`, with
`memory-<day>.db`) for `backup_keep` days (14), as the database's copies already keep it. It is
**not a node**: it is not in the graph's repository nor its index, and neither the brain's
`node_read` and `node_find` nor a task's node tools reach it. The brain reads memory through
`recall` only, which keeps the trust rules (an external memory read taints the turn). To
read what Eriphe knows in the workspace, the user opens Settings → Mémoire.

### What Eriphe learned

The learning journal (`learning.go`, table `learning_log` in `memory.db`) records every
change to what steers Eriphe: a memory of kind `lesson`, `preference`, `decision` or `profile` (or
pinned), and any memory but an episode the nightly extraction wrote on its own (a fact, a person),
made active, changed (superseded or edited in place) or forgotten, and every change of a
skill's approved version (approved, replaced, deleted). A proposal is not learned until approved.
Each entry says what it touched (`memory #N`, or the skill's name and versions), the action
(`added`, `changed`, `removed`, `reverted`), a status (`trial`: in force, not confirmed;
`confirmed`: the user's own words or hand, or approved by them; `dormant`; `reverted`), a
provenance (`user`, `brain`, `task`, `external`, `extraction`, `consolidation`), a risk class
(for now `high` for the profile, a lesson, or a skill with scripts; `low` otherwise), a reason
and its evidence (`msg:N`, `task:<id>`, `learned:<id>`). Entries are fixed but for their status
(a trigger refuses the rest). They point at the data rather than copying it: the memories
themselves and `memory_log`'s before and after for a memory, the bundles for a skill.

**Undo.** Undoing an entry forgets an added memory, puts a superseded one back as it was (current
again, pinned again), restores the state before an edit or a forgotten memory, and for a skill
puts the previous approved version back (or removes an added skill, or brings back a deleted
one). Only the newest change of a thing still in force can be undone; the undo is an entry of its
own (`reverted`, pointing at the one undone, which becomes `reverted`), never undone itself.

**For the user**: Settings → « Ce qu'Eriphe a appris » (`/settings/learned`, `web_learned.go`),
newest first, by kind and status, each entry with its source, date, status, what it changed
(before and after) and « Annuler ». A skill shows its name and version, never its files.

**For the brain**: `learned` (by kind, status, about, topic or period, with ids; a memory drawn
from external content is named, not quoted) and `unlearn` (`id`, `reason`, `source_ref`). An
undo needs the user's message; on a tainted turn it is only proposed, unless that message of the
same turn asked for it (as for `remember`), and waits as a card (kind `unlearn`) or on the page.
The wake packet (`learnedBlock`) carries what was learned since the last brief and not
told yet, except what the user did by hand, for the brain to say in a sentence each (what the
extraction kept on its own, in one sentence for the lot); `since`
lists the entries (kind `learned`), and so does the Journal.

The statuses `trial` and `dormant`, the provenance `consolidation`, the risk class and the
evidence refs are there for the learning loop that comes next: fewer cards by provenance and
risk, skills written by the brain, reflection and consolidation, helpful/harmful counters with
automatic undo.
