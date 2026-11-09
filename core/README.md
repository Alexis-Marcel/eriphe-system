# eriphe-core

Eriphe's control plane (v0): it keeps the state of nodes and tasks in SQLite, schedules tasks on
nodes, follows them, lends credentials, and gives the brain its tools over MCP. One binary, with
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
(`core-token`), the vault key (`vault.key`), the database (`core.db`, `memory.db`) and the
workspace's self-signed certificate (`web-cert.pem`, `web-key.pem`). The API listens on
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

An installer gets a join token for the machine's own node without the workspace:

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
| `anthropic-api` | `claude-code`, `opencode` | `ANTHROPIC_API_KEY` |
| `openai-api` | `opencode` | `OPENAI_API_KEY` |
| `google-api` | `opencode` | `GOOGLE_GENERATIVE_AI_API_KEY` |
| `mistral-api` | `opencode` | `MISTRAL_API_KEY` |
| `openrouter-api` | `opencode` | `OPENROUTER_API_KEY` |
| `openai-compatible` | `opencode` | `SELFHOSTED_API_KEY`, optional (see [Self-hosted model server](#self-hosted-model-server)) |
| `custom-api` | `acp` | the provider's own `env` (for example `DASHSCOPE_API_KEY`); not a system variable |

The `opencode` runtime (OpenCode driven over ACP, see `node/README.md`) reaches any of these with
an API key; its `model` is OpenCode's `provider/model`, and the provider must be the agent's
(`openai/…` for `openai-api`, `google/…`, `mistral/…`, `openrouter/…`, `anthropic/…`), which the
configuration checks. `acp` is any other ACP agent (Goose, Qwen Code…): the agent gives its `command`, one argument per item (`["goose", "acp"]`), run as is in the worker
image or the agent's `image`, never through a shell; it takes no `model`. An
**agent** is what the brain picks for a task: a runtime, a provider, a model, an optional image, the
node labels it needs (`needs`, for example `gpu`), what it would rather have (`prefers`: `cpu`, `memory`,
or any node label), and `use`, which tells the brain when to pick it.
The brain is an agent too (`brain.agent`, or Settings → Agents when the file names none): changing
it restarts the brain once its turn is over, on its session if the harness is the same, or on a
fresh one with the context pack. Its runtime must take MCP servers and a system prompt. The provider's credential is lent to each task.

**Credentials.** A credential (FR « identifiant ») is what a real program in a task uses with a
secret: a package registry, an API with a key. It is declared in Settings → Tools (Credentials,
`/settings/credentials`; the former `/settings/accesses` redirects) or under `accesses`, and lent
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

The task never holds the secret. Its variable holds a placeholder; the node's relay decrypts the
access's hosts only ([node/README](../node/README.md#accesses)) and hands each request to
`/v1/node/tasks/{id}/access`, where eriphe-core puts the secret in that header and goes on to the
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
- The node sees an access's requests, though not its secret. A client that pins its certificates
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
  place, and records the machine's host key. A machine only a node reaches cannot be tested: its
  key can be pasted (`ssh-keyscan`). An access with no host key cannot be lent. A key that changed
  fails the test; emptying the field and testing again records the new one.
- **The path.** `eriphe-cred` is the task's SSH agent, and asks `/v1/node/tasks/{id}/ssh`
  (`{op: keys | sign | connect}`) through its node. eriphe-core signs a login only (RFC 4252's
  `publickey` request, or OpenSSH's host-bound one, whose host key must be the pinned one, for that
  key and the access's `user`): the key signs no commit or file and logs in to no other account. The session leaves from eriphe-core, as an HTTP access's requests
  do: the relay upgrades `GET /v1/node/tasks/{id}/ssh/connect?target=host:port` (`eriphe-ssh`), and
  eriphe-core decides and dials that exact address, carrying SSH's encrypted bytes. So a node need
  not reach the machine. A machine eriphe-core cannot reach answers 502, and the node dials it
  itself. Each connection, login
  and refusal goes in `access_log` (tool `access`). A task lent an SSH access no longer gets the
  built-in ask on `ssh`, `scp`, `sftp` and `rsync`: the access was decided, and reaches that machine only.
- **What it does not do.** SSH lends everything the remote account can do: Eriphe sees the logins,
  not the commands, and `sudo` is the machine's to set. Use a dedicated account. The host key is
  pinned in the task's own `ssh` configuration, which a task can change: it protects a task from
  another machine, not a machine from the task.

**Harness cards.** What each runtime can do, as the node drives it, is one entry of `harnesses`
(`harnesses.go`): MCP servers, how it takes a system prompt (a flag, developer instructions, an
instructions file, or prepended to the prompt), streaming, a PDF or an image in a tool result, its
own web search and fetch, resume, and whether it picks a model when none is named. The core asks
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
- When a node fetches a task of such an agent, eriphe-core adds `model_endpoint` (`base_url`, and
  `key_env` with a key) to its spec and that one `host:port` to a copy of its egress rule
  (`endpoints`): the node's relay lets the task reach that server, even on the LAN or another
  port, and nothing else of the LAN (`node/README.md`, Egress).
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

#### A model behind a node

The server can also stay on its machine, listening on that machine only, a home PC or a rented
server, with no port opened and no account elsewhere: eriphe-node runs there with
`-model-server http://127.0.0.1:11434` (the installers' `--model-server`, `-ModelServer` on
Windows), and the provider names that node in place of an address:

```json
"providers": {"gpu": {"kind": "openai-compatible", "node": "gpu-box", "models": ["qwen3:8b"]}}
```

In the workspace: Add an AI account → Modèle auto-hébergé → "Sur un de mes appareils", then the
device (the ones whose node reports `model_server`; Settings → Devices tags them "Prête un
modèle"). `node` and `base_url` exclude each other.

The address is the one the node reaches (`install/README.md`, A model behind this machine): on
Linux the node is a container, so the server is a container next to it on `eriphe-system`
(`http://eriphe-ollama:11434`); on a Mac with Apple's container, `127.0.0.1`; on Windows, the node
runs in WSL, which reaches Windows' `127.0.0.1` only with mirrored networking (`networkingMode=mirrored`
in the `[wsl2]` section of `%USERPROFILE%\.wslconfig`, Windows 11 22H2 or later, then
`wsl --shutdown`, which applies to every distribution, and the install again). Ollama for Windows
runs only in its user's session, while the node runs without anyone logged in: put it in the
startup programs (Win+R, `shell:startup`). The installers check at the end that the server answers
the node.

The path (`model_relay.go`):

```
task (any node, the brain too) → its node:   <callback>/model/v1/{path}   (the task's secret)
its node → eriphe-core:                      /v1/node/tasks/{id}/model/{path}  (node secret, mTLS)
eriphe-core → the lending node:              /v1/model/{path}   (that node's tunnel)
the lending node → the server:               <model-server>/v1/{path}
```

- eriphe-core relays only for a task that runs on the calling node, is not finished, and whose
  agent (OpenCode) uses a provider with `node`; only `GET` and `POST`, only paths under `/v1`
  (`..`, even escaped, is refused here and again by the lending node, so Ollama's own `/api`,
  with pull and delete, stays out of reach); bodies up to 64 MiB. The task's and the node's
  secrets stop here: the server gets the provider's key (`secret`), if any, which is never lent
  to a task.
- The task's spec carries `model_endpoint: {"relay": true}`, no address and no key; its egress
  rule is unchanged (its node is reached directly, as for its catalog servers).
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
- **Lent to each task.** When a node fetches a task, eriphe-core lends an `auth.json` with the
  access token, the ID token and the account, **no refresh token**, and `last_refresh` set to
  now, so Codex does not refresh it (see `node/README.md` for where the node puts it). A task
  that outlives its access token stops with an authentication error; a restart lends a fresh one.
- Tokens are never logged (the log names a sign-in by the first characters of its random id), never
  stored in a task's spec, never shown: the page shows the account's e-mail and plan only.
  `ERIPHE_CHATGPT_ISSUER` points the sign-in at a fake OpenAI, for tests.

Secrets stay in files (or the vault). A provider's key is read when a task starts, sent to its node,
and never stored or logged; an access's never leaves eriphe-core.

## Dynamic catalog (workspace)

Servers, providers and agents can also be added, edited and removed from the workspace (Settings →
Tools, Settings → Agents) or the API, without touching the file and without a restart. They are kept
in eriphe-core's database (`catalog` table) and merged with the configuration at runtime:

- **The file wins.** Its entries show in the workspace as read-only ("défini dans la configuration")
  and are refused on writes (409); a workspace entry never shadows one of them. The file must stand
  on its own: workspace entries may use the file's providers, not the reverse. `default_agent`, if
  the file sets it, is read-only; otherwise the workspace picks it (the first agent by name until
  then). The brain's agent works the same way: `brain.agent` in the file is read-only;
  without it, the workspace picks it (`PUT /v1/brain-agent`), else the default agent, else the first
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
  services; tasks waiting for a node are placed again; `list_tools` and `list_agents` show the new
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
| `auth` | exactly one of `{"bearer_file": "/secrets/x"}` (a static token, read at each connection), `{"oauth": {...}}`, `{"github_app": true}` (an installation token of Eriphe's GitHub App, refreshed before it ends; with `"account"`, from that account's or organization's installation: see [GitHub in a task](#the-github-app)), `{"none": true}` |
| `run` | instead of `url` and `auth`: a local server, a container a node runs as a service (see [Local MCP servers](#local-mcp-servers-services)) |
| `use` | what it is for, for the brain |
| `access` | what Eriphe may do on it: `read-only`, `reads`, `free` or `ask` (see [access levels](#access-levels)); without it, `trust_annotations` says |
| `trust_annotations` | the older word for it: `true` is `reads`, `false` (the default) `ask` |
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

**The path of a call.** When a node fetches a task, its assignment names the catalog servers of
its grant (`spec.servers`), nothing else. The node gives the agent, for each, an MCP server at
`<callback-url>/mcp/<server>` with the task's own secret, and relays its requests to eriphe-core
at `/v1/node/tasks/{id}/mcp/{server}` with the node's secret (and certificate), over the same
connection as its assignments and reports (so it works wherever they do: the NAS node on the Docker
network, the PC node under WSL). eriphe-core serves there an MCP server built from the cached tools the grant
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
  The older `memory.trusted_servers` is migrated at start-up, each name once: a mark removed in
  the page stays removed. The web (`WebFetch`, `WebSearch`) always taints.
- **A tainted task or assistant returns tainted text.** A worker task is tainted when it was
  started on a tainted turn, when it called a tool not trusted (seen by the proxy, which every
  catalog call crosses, and in its events), when it read the web, or when its events cannot be
  read. The event of its turn's end then says so and taints the turn that reads it
  (`task:<id>`), as `task_status` and a session's report of it do; a task that read only the
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
client or refuses one), PKCE, the `resource`
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

From a terminal, connecting is the CLI's:

```sh
eriphe connect notion
```

It listens on `http://127.0.0.1:<port>/callback` (a loopback redirect, which OAuth 2.1 allows for
native apps: the port of the registered client if there is one, else a free one), asks
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
  database (OAuth tokens). They go from eriphe-core to the server only, never to a node or a task.
- A task reaches only its node, with its own secret, and only the servers of its grant; the node
  reaches eriphe-core with its secret, and only for its own running tasks.
- Every call is a permission request first, under the policy below; the proxy enforces it with
  tickets.
- git and gh in a task get [GitHub in a task](#github-in-a-task): a one-hour token for one
  repository, decided at launch as any lend, and logged; it goes in on the way out and stays out of
  the task. Without a GitHub App, the tool's own token stays in eriphe-core the same way.
- What servers say (descriptions, results, errors) is data for the agents, never instructions:
  the brain's prompt says so, and the policy, not the brain, decides what reaches a third party.

## Nodes

Nodes are not configured: they join. `eriphe-core join-token -name pc` creates a single-use token
for the node `pc` (valid 24 h, `-ttl` to change) and prints the command to run on the new machine;
the workspace's "Connecter un appareil" does the same. It reads the same `-config` or `-data` as
eriphe-core and asks the running one; `-quiet` prints the token alone, for a script. The address
in the command is `public_url` or, without it, the workspace's own host on eriphe-core's port
(a workspace opened at `localhost` says that address only works on that computer); the CLI then
prints a placeholder. A token is `id.secret` and joins only under
the name it was made for, and never under the name of a registered node: a stolen or mistyped token
cannot take over another node's tasks and lent credentials. A reinstalled machine joins again with a
rejoin token for its own name (`join-token -name pc -rejoin`, or "Réinscrire cet appareil" on its
card), which replaces its secret. Tokens made before names existed join a new name only. The node gets its own
secret (only its hash is stored), then keeps a tunnel open to `GET /v1/nodes/connect`; eriphe-core
speaks the node's API through it. A node is online while its tunnel is up and answers.

| Call | What |
|---|---|
| `POST /v1/join-tokens?name=pc&ttl=24h` | new join token for `pc`, with `ca_hash`; `&rejoin=1` for a registered node (bearer token) |
| `GET /v1/nodes` | registered nodes, with `cert_expires` once a node has its certificate, and `version`, `latest`, `update_available`, `update_commands` (bearer token) |
| `GET /v1/version` | `{core: {version, latest, update_available, update_commands}, node: {latest}}`: the latest versions on the registry, empty while unknown (bearer token) |
| `DELETE /v1/nodes/{name}` | forget a node; its tasks become `lost`, its certificate is revoked (bearer token) |
| `GET /v1/nodes/tls`, `PUT /v1/nodes/tls` | the CA's hash, whether nodes must use TLS, which have no certificate yet; advanced: `{"tls_only": false}` keeps the plain path open, `{"tls_only": true}` closes it (bearer token) |
| `POST /v1/nodes/join` | `{name, token}` → `{secret}` (join token for that name) |
| `POST /v1/node/certificate` | `{csr}` (PEM) → `{ca, certificate, expires}` (node secret, and its certificate once it has one) |
| `GET /v1/nodes/connect` | the tunnel (node secret, `Eriphe-Node: name`, and its certificate) |

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
(semver order, `version.go`), or a build from before the releases (`sha-<commit>`); a build of
`main` or a local one never says so, as someone is testing it. The images publish their `latest`
tag each on its own, as their build ends: while a release is half published (eriphe-node's
`latest` out, eriphe-core's or eriphe-base's not yet), the latest release is the older one, which
every image has, and the check runs again five minutes later. The workspace shows `0.2.0`, and a
build of `main` as `0.2.0-dev` with its commit in small.

### Updates of the machines

Each machine updates itself, with the updater its installer put on the host side
(install/README.md, The machines update themselves); eriphe-core says when (`machine_updates.go`).
The workspace always says when a machine is behind (a dot, "Mise à jour disponible"), with one
"Mettre à jour" button on each machine that has an updater, "Tout mettre à jour" when several are
behind (eriphe-core first; the nodes follow once it runs the latest version), and one switch for
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
the data folder, for the updater; the outcome comes back in `update-status`); a node only once
eriphe-core runs the latest version, in its assignments (`update`, with eriphe-core's version, the
most the node may get), and the node waits for its tasks to end. A node is offered eriphe-core's
build, never a newer one: while eriphe-core is behind, a node's "Mettre à jour" asks eriphe-core
first ("Eriphe se met d'abord à jour, puis cet appareil"), and the card says it waits for
eriphe-core until eriphe-core runs the latest version; the node is asked then (`followUpdates`,
as for "Tout mettre à jour"), and not at all if eriphe-core's update ends elsewhere (failed,
rolled back, pinned). Without an updater on eriphe-core's machine, the button waits for its
command. An update that ends "ok" or "current" on another version than the one offered says it
did not reach it, never "À jour". While a node has a request, the
scheduler treats it as offline: new tasks wait for it. A request not done within a day is dropped;
one with the updater (handed to it, or "running") that has not ended within 30 minutes stopped (its
updater died, or a reinstall restarted it): it is dropped, and the machine's line says "La mise à
jour s'est arrêtée sans résultat". A node that runs eriphe-core's version already (its install
command was run again) is not sent its update request, which is dropped.
A machine without updater (installed before, or by hand, as `deploy/nas/compose.yaml`) says so and
gets no request.

An update also brings a machine to the install's layout of its version (what an install puts there
besides the images: services, LaunchAgents, Syncthing; install/README.md, The machines update
themselves). What its updater cannot do alone, the node reports in `update.needs` (`why`:
`password`, a step that needs the Mac's password; `admin`, one that needs an administrator's
PowerShell; `layout`, any other, such as an updater from before the layouts), and the device's card
says « Cette mise à jour a besoin de toi : … » with the command that updates that machine in a code
block to copy (`manualUpdate`, from how it was installed: never a command a node names), while no
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

Nodes reach eriphe-core over TLS, with a certificate authority for machines that eriphe-core makes
at its first start, as kubeadm does for a cluster. It is not the workspace's certificate: it signs
only eriphe-core's certificate for the nodes (for the name `eriphe-core`, which a node checks
whatever address it dials) and one client certificate per node.

- **Where.** `machine-ca.pem` and `machine-ca-key.pem` (0600) in the data folder, next to the
  database. The key is a secret like the vault key. The CA lasts ten years and is never replaced
  by itself: a CA file that does not load stops TLS for nodes (logged at start) rather than making
  a new one, since every node pins it.
- **One port.** The API port serves TLS and plain HTTP side by side: eriphe-core tells them apart by
  the first byte a client sends (a TLS handshake starts with `0x16`). On the local network, the CLI,
  Home Assistant, the brain's MCP and every other API caller may keep their plain `http://…:8800`;
  a node reaches the same address as `https://…:8800`, and the join commands say `https://` when
  they carry `-ca-hash`. On the tailnet, `:8800` keeps its Tailscale certificate (publicly trusted:
  the address to give the CLI or Home Assistant away from home) and asks for a node's certificate
  too.
- **No token in clear over the internet.** A call with the API's token (or a channel's token) over plain HTTP from a public address is refused (403): the token already crossed the
  internet in clear, and a setup like that should fail at once rather than leak it at every call.
  Plain HTTP stays open to private, loopback, link-local and Tailscale addresses (and to the
  Docker gateway, which may be the host itself); TLS works from anywhere. Node requests (join,
  tunnel, reports) are not affected: they are TLS once the node knows the CA.
- **Joining.** The join command carries `-ca-hash sha256:<hex>`, the SHA-256 of the CA's public key
  (like kubeadm's `--discovery-token-ca-cert-hash`): the node finds that CA in eriphe-core's TLS
  handshake, pins it, and only then sends its join token. `eriphe-core join-token` and "Ajouter une
  machine" print it, and Settings → Appareils → Avancé shows it, for a machine set up by hand.
- **Client certificates (mTLS).** A node asks for its certificate with its secret
  (`POST /v1/node/certificate`, with a certificate request: the key never leaves the node), valid
  one year, and for a new one with its current one when a third of its life is left. A request
  that presents a certificate needs the node's secret and its current certificate (or the one
  before, while it moves to the new one): forgetting a node or letting it join again revokes it at
  once, and drops its tunnel.
- **Nodes that joined over plain HTTP.** An existing node (its secret only, in `node.json`) asks for
  its certificate with its secret on its next connection, at the address it knows, pins the CA that
  comes with it, and connects over TLS from then on: nothing to do on it but update it. That first
  request trusts the network it crosses, as the plain join did; given `-ca-hash`, the node checks
  the CA instead.
- **Closing the plain path.** eriphe-core accepts nodes over TLS only, with their certificate: from
  the start on a new installation, and on an older one by itself as soon as every node has its
  certificate (logged; Settings → Appareils marks those still without one, `GET /v1/nodes/tls`
  lists them). It then refuses nodes over plain HTTP and nodes without their certificate, and drops
  the tunnels of those connected so. Joins then need TLS too. API callers other than nodes are not
  affected. `PUT /v1/nodes/tls {"tls_only": false}` is the advanced way out, to keep the plain path
  open for a machine that cannot have its certificate yet; eriphe-core then leaves it so.

## MCP tools (`/mcp`, bearer token)

| Tool | What |
|---|---|
| `list_agents` | agents, when to use each, and whether a node can run them now |
| `list_tools` | the tool catalog: servers, status, `use`, tools, which need the user to connect them |
| `list_nodes` | machines, online or not, what each reports (CPUs, memory, labels, runtimes, volumes, images), active tasks |
| `run_task` | start a task: title (what the user reads; else its contract's objective, else the prompt's first line), prompt, [resources](#resources) (a repository, the user's folders, catalog tools), agent (or the default), node (or let the scheduler choose), an optional [contract](#contracts-contractgo) |
| `list_folders` | the user's folders: name, machine (online or not), access (`read`, `drop` or `manage`), `max`, `copyable`, where a task finds it, and `machines`: every machine holding it (its own, then its copies), each `online` and `up_to_date` or not; a task may run on any up to date |
| `list_skills` | the user's [skills](#skills), approved and on: name, description, files, whether it has scripts |
| `read_skill` | a skill's `SKILL.md`, or another of its text files (never a script: scripts run in tasks) |
| `task_status` | state, the agent's last message (without its receipt), its checked [receipt](#receipts-receiptgo), the pending permission; `wait` blocks until the task needs you |
| `list_tasks` | recent tasks |
| `answer_task` | reply to the agent's question (a permission is never the brain's: it is refused); `release` asks the user on a card to release the tools it was lent [held back](#resources) |
| `stop_task` | stop the agent |

The scheduler takes the requested node if it is online and fits. Otherwise it works in two passes,
like Kubernetes': it **filters** the online nodes that offer the agent's runtime and carry all its
`needs` labels, then **scores** them: +1 per CPU if the agent prefers `cpu`, +1 per GiB of RAM if it
prefers `memory`, +8 per preferred label the node carries, +2 if the agent's image (or the node's
default image) is already pulled, +2 if the node has the agent's runtime unpacked (`agents_ready`),
-4 per active task. The highest score wins, ties by name.

**Placement classes** (`placement.go`), like Kubernetes' hard rules and preferences; none is a
setting:

- **Resident**: an assistant that talks to the user or is woken by events (the brain first). Hard
  rule: an online node started with `-always-on` (the machine eriphe-core runs on) that fits, the
  best other node only when none does (the log says why). If its node has been offline for 30
  seconds and another online node can take it, it moves; if it runs on a node that may sleep and an
  always-on one can take it, it moves there between two turns. Moving starts a fresh session with
  the context pack: the thread is eriphe-core's, and a message its old node never received is
  delivered again.
- **Bound**: a task with the user's folders goes to a node that holds each of them up to date,
  the folder's machine or one keeping a copy in sync ([Tasks on a copy](#copies-of-a-folder)),
  the best of them by the scores above, and waits for one. A task its node has not started yet
  is placed again, like the others, when another such node is online.
- **The others**: the scores above. A task its node has not started yet is placed again after that
  node has been offline for 5 minutes; a running task keeps its node.

A node given by hand (`node` of `run_task`, or an assistant's) always wins: such a task never moves.

**Asleep or offline** (`node_sleep.go`). A machine that sleeps disconnects and comes back by itself:
a Mac in maintenance sleep wakes for a minute every quarter of an hour. eriphe-core counts the
times a node came back after an absence of 2 minutes or more; one that did twice in the last 24
hours is *asleep* while it is away (Settings → Devices says "En veille", `list_nodes` says
`asleep`), any other node away is *offline*. The always-on machine is never asleep. A task its node
has not started yet is placed again after 30 seconds when that node is asleep, instead of 5 minutes.
The node itself keeps its machine awake while a task is at work (node/README.md, Sleep).

**Ready in advance.** Each node's assignments carry `prepare`: `{runtimes, images}`, the runtimes
of the configured agents the node could run (it carries their `needs`) and the images they name.
The node unpacks those agents and pulls those images (and its default one) in the background, at
its first assignments and whenever the list changes: a change of the catalog wakes every node
(node/README.md, Agents). An agent no one configured is never downloaded anywhere.

If no online node fits but a registered one would by what it last reported (a desktop that is off,
say), the task is created `pending` with no `node` and a `pending_reason` ("waiting for a node that
can run claude-code with gpu (offline: pc)"), so the brain sees it waits. Such tasks are scheduled
again whenever a node comes online and every 15 seconds; stopping one stops it at once. If no
registered node could ever run it, `run_task` fails. Nodes are checked every 15 seconds. Tasks that were active when eriphe-core stopped are
followed again at start, and a disconnected node's tasks are followed again when it reconnects; a
node that forgot a task marks it `lost`.

### Contracts (`contract.go`)

`run_task` takes an optional `contract`, for a task that changes something (none for a search or a
read):

```json
{"objective": "One sentence: what must be true at the end.",
 "constraints": ["Do not change the public API"],
 "checks": [{"name": "build", "run": "go build ./...", "dir": "core"},
            {"name": "tests", "run": "go test ./...", "timeout": 600},
            {"name": "report", "exists": "/work/folders/Invoices/Eriphe/report.pdf"}],
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
`"repo/core"`); whether it exists is only known once the repository is cloned, so the node says it
then (a *config* failure, below).

**A misconfigured check.** A check that could not run at all (its `dir` is not there, its command
is not found, it could not be started: the node's `config`) is the contract's fault, neither the
code's nor the agent's: it is not sent back to the agent, does not count toward "the same failure
twice", is no unsupported claim of the receipt, and does not stop the task. The task stays idle and
the brain hears that the contract is misconfigured, with the node's exact reason. It fixes it with
`answer_task` and `checks` (all of them, checked like at the start; `reply` optional): the
contract and the task's spec are updated, and the new checks go to the node with the next turn,
after which they run. The drawer shows such a check as "could not run".

### Retries and budgets (`task_retry.go`)

What follows a worker's turn that failed is decided in code, by the failure's class (from the
causes of `problems.go`), before the brain hears of it:

| Class | Example | What eriphe-core does |
|---|---|---|
| passing (`transient`) | network, the provider's 5xx, overloaded, a rate limit | retries it after 30 s, then 1, 2, 4 minutes (5 at most), within the contract's `attempts` (2 by default): the agent is told to carry on |
| account | key refused, quota or credit spent, model missing | no retry: the brain is told to warn the user |
| invalid | a request refused (400), a turn cut by its limits | no blind retry: the brain changes the instructions or warns the user |
| permission | the policy refused an action | never retried: the brain hears it as before |
| check | a check of the contract failed | sent back to the agent by its node (see eriphe-node's README) |
| config | a check of the contract could not run (its folder missing, its command not found) | never sent back, never counted, never stopped: the brain is told to fix the contract (above) |
| same failure twice | the same class and words (numbers aside) twice in a row, or a check the node stopped sending back | **stops** the task, even with attempts left |

The budget of a contract is kept by eriphe-core: `minutes` (checked every 15 seconds), `turns`
and `tokens` (input, output and cache writes from the turns' `usage`, where the harness reports
it) at the end of a turn; a turn whose receipt says `done` is not stopped (the brain closes the
task), and `answer_task` refuses a new turn once the turns are spent. A task eriphe-core stops
keeps why (`stop_reason`): the brain hears it with the last receipt when the node reports it
stopped, and the drawer says so; a retry on its way shows its time. A task whose agent or container
failed is told to the brain with its class: worth starting again once, or not. Each failure's class
is kept in `task_harness` for the measures. The brain's prompt no longer counts retries itself.

**No work lost on stop.** Before a task's container is removed (stopped by the brain, a budget or
"the same failure twice"), its node pushes the commits no remote has on the branch the task was
lent, with the task's own git (the same decision applies), or keeps
the repository on the node (see eriphe-node's README). Its `work` event says which: the brain hears
it with the stop, even a stop of its own when the work was kept.

### Usage and measures (`task_stats.go`)

A turn's `turn_end` carries its `usage` (`input_tokens`, `output_tokens`, `cache_read_tokens`,
`cache_write_tokens`) and `cost_usd` where the harness reports them (Claude Code both, Codex its
tokens, OpenCode and other ACP agents nothing yet). A task's drawer shows its tokens (cache reads
aside) and cost. The side panel of tasks shows a few measures over the last 30 days of finished
worker tasks, read from what eriphe-core keeps (no service of their own):

- **accepted**: the last receipt says `done`, no check failed, no unsupported claim;
- **first time**: accepted with one turn, no retry, no check sent back and no receipt asked again;
- **unsupported claims**: receipts that said more than eriphe-core saw (done with a failed check, a
  change the diff does not have), and how many `done` receipts claim verifications no check covered;
- **cost per accepted task**: tokens, dollars where known, and minutes;
- **failures by class** (see Retries and budgets).

### Receipts (`receipt.go`)

A worker ends every turn with a **receipt**: a fenced ```` ```json ```` block, the last thing of its
message, `{"receipt": {status, summary, changed, verified, not_verified, decisions, risks,
needs_approval, lessons}}`, with `status` one of `done`, `partial`, `blocked`. Its standing
instruction (`workerVoice`) asks for it. It is a block of the final text, not a tool call, because
every harness (Claude Code, Codex, OpenCode, any ACP agent) hands back its final text, while a tool
would have to be allowed in each one's permission model. eriphe-core reads the last block that
parses (a `<receipt>` tag and a message that is only the JSON work too) and never trusts it:

- **verified** is only what a check run by the node saw pass (`check` events); what the agent says
  it verified without a check stays *claimed, not checked*; a failed check is *not verified*;
- a file said `changed` is compared with the repository's diff, which the node reports when the
  turn ends (`diff` event); files of the diff left out are counted;
- `done` with a failed check, or a change the diff does not have, is an **unsupported claim**.

A turn with no readable receipt is asked once for it (`skip_checks`: the node runs no check after
that turn), at most twice per task and never twice in a row, without the brain; a turn that failed
is not asked. The brain then gets the checked receipt (in French, like its other events) instead of
the agent's last message; without one, the last message, truncated, as before. `task_status`
returns it as `receipt`, the task's card shows its status and how much is verified, its drawer
verified and not verified side by side, the checks with the end of a failed one's output, and the
mismatches. The last receipt is kept in `task_harness`. The night's memory extraction reads a
finished task's receipt instead of its last message (still external).

### Work sessions (`sessions.go`, `session_engine.go`)

A **session** is work of several steps toward one goal: rebuild a service, find a brand name, make
a video, not only code. An assistant has no end; a session has one (a goal, a plan, checkpoints);
a task is one step.

| Who | Does |
|---|---|
| Brain | opens the session with the user (`start_session`: title, goal, `done_when`, budget, steps), the only voice |
| Lead | a temporary assistant (role `lead`, `session_lead.go`): judges each step on its evidence (`settle_step`: accept, retry, reject), amends the plan (`amend_session`), closes it (`end_session`), reports to the brain. The brain when no agent can run one |
| Engine (code, no model) | starts each step once those it comes `after` are accepted, in parallel, passes them their outputs, asks the cards, holds the limits |
| User | approves the plan, answers the choices and approvals, gives more time |

A step is a `task` (prompt, agent, resources, contract, as `run_task`'s), a `choice` (a question
and 2 to 6 options, recorded with `choose_step` in the turn of the user's answer) or an `approval`
(a yes or no card). Its statuses: `waiting`, `running`, `reported` (its agent says it is done: a
claim), `verified` (its checks passed), `accepted`, `rejected`, `blocked` (its task failed or
stopped), `dropped`. A step whose checks failed, or whose receipt claims what they deny, cannot be
accepted.

- **The plan card** (card kind `session`) is the user's approval of the steps as planned: they start
  without asking again, their resources granted as for a watch the user authorised. A step added
  later asks its own start card.
- **One writer per scope**: two steps writing the same repository branch or folder never run at
  once; the second waits.
- **Handoff**: a task step's prompt gets the session's goal, `done_when` and the outputs of the
  steps it comes after (the agent's last message, or what the lead gave on accepting).
- **Limits held by the code**: 2 retries per step (a retry answers the idle task with what to fix,
  or starts a new one), 2 plan changes without asking (then a card), a budget of running time (60
  minutes by default; past it the session pauses, the running steps finish, and a card offers more
  time). The brain never gives itself more time.
- Each step's end reaches the lead as an event `[session N « title »]`, instead of the task's own;
  a session complete or stuck is told once. `sessions` and `session_status` read them;
  `stop_session` stops the tasks and withdraws the cards. The engine's pass runs every 15 seconds
  with the nodes' check: budgets, and steps whose task ended while eriphe-core was down.
- Tables: `sessions`, `session_steps`, `session_asks` (what a session asks, one card each),
  `session_log`.

**The session lead** is born when the user approves the plan (`lead-<id>`, « Chef de session N ») and
goes when the session ends. Its trigger `session:<id>` wakes it with what happened to its steps
(`session_inbox`), one turn at a time; its own tools are `session`, `settle_step`,
`amend_session`, `end_session` and `report`. It never talks to the user nor starts a task. The
brain no longer judges a session that has a lead: it passes the user's words with `tell_lead`, and
hears the lead's reports, the choices to ask, the budget's card and the end.

**The final objective check**: `end_session` on a session with a `done_when` adds a last step,
`final-check`, a fresh agent lent read-only what the steps wrote (their repository branches and
folders) and given the goal and what each step produced; only its receipt with status `done` and no
failed check closes the session. Otherwise the lead hears what is missing; after 2 checks that fail,
the decision goes back to the user through the brain.

**The work interface** (`web_sessions.go`, `sessions.html`, `sessions.css`, `sessions.js`):
`/sessions` lists them; `/sessions/{id}` shows one, kept live by its own event stream
(`/sessions/{id}/events`, event `session`, the page's live part re-rendered on each change). From top
to bottom: its state, goal and actions (pause, resume, stop); its facts (`done_when`, time against the
budget, its lead, steps accepted); **what waits for the user** (the plan card, start cards, choices
as buttons); the summary once done; the plan as stages on a rail (a step stands one stage after the
deepest step it needs, those of one stage run together, the final check last); the log. Each step
wears a mark for how far its evidence goes: a ring for what the agent claims, a check inside for what
the checks verified, filled once the lead accepted, a red cross for a failed check or a rejection.
Under the live part, a line to the lead (`POST /sessions/{id}/lead`) is queued on its inbox as the
user's words. The tasks panel of the home page shows the sessions in progress first.

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
   is the boundary: what leaves it goes through the node's mounts (a folder's level), its egress
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
| Send | `openWorldHint` true or absent: an unannotated tool, and every tool of a server whose annotations are not believed |

Each category is `free`, `approval` or `never`:

| Value | Brain | Task |
|---|---|---|
| `never` | not listed, cannot be lent | not in its `tools/list`; the proxy refuses it, ticket or not |
| `free` | called without asking | called without asking |
| `approval` | a card per call | a card once per task, category and account, granted until it ends |

The grid is read at each call: a change applies at once, running tasks included. A row left out has
the default: Read free, the rest on approval. A level [below](#access-levels) reads as a grid until
a server has one of its own. Every tool's **Edit** opens its page (Settings → Tools,
`/settings/tools/{name}/access`, `?account=` for an account's): the grid, shortcuts that fill it,
whether the server's annotations are believed, the collapsed panel of exceptions, its credential,
whether what it returns is trusted (the [one taint rule](#tool-catalog)), its allowed actions, and a link to its connection (its form, or the page of Google, Drive, Home
Assistant or GitHub). A tool of the configuration file shows its grid, changed in the file. In the
file:

```json
"mail": {"url": "…", "use": "…", "trust_annotations": true,
  "grid": {"read": "free", "change": "free", "delete": "approval", "send": "never"},
  "exceptions": {"create_draft": "free"},
  "account_grids": {"sam@example.org": {"grid": {"send": "approval"}, "exceptions": {"move": "never"}}}}
```

With a `grid`, `trust_annotations` says on its own whether the annotations count: without it, every
tool is Send. `exceptions` replace a tool's category value, Never included; `account_grids` is one
grid per account, in the place of `account_access`, and a server's exceptions hold for its accounts.
An account's `trust_annotations` replaces its server's. Once one account has an access of its own,
the others are written down with the one they had: the server's no longer speaks for them.
A call that names no account takes the strictest of their values, and is refused when they differ,
unless it is a read none of them refuses.

**Free needs a clean start.** Free is the user's delegation, within which only the model's judgment
holds; a hard guarantee is set in the grid. One guard stays, against prompt injection: on a tainted
turn every Free above Read counts as On approval, for the brain until the user's next message
starts a clean turn, for a task if it was started on one (or by the brain alone, or was lent the
tool held back). A task started cleanly keeps its Free writes even after reading external content.
Only the brain's Free reads are allowed in advance and called without a ticket; its writes go
through the policy, so the turn's taint is always seen.

### The approval card

An approval is a card of the conversation (`access_card.go`), answered by the user, never by the
brain. A start's card also takes a plain yes or no read by eriphe-core, unless it may carry someone
else's words (a tainted turn). The others take their buttons only: they come at any moment, so a
yes may answer something else. A card never holds the brain: its call is answered at once as not
run, the card keeps the exact input, and a yes runs it from eriphe-core, however late and even after
the brain restarted, if the tool is still allowed; the result, or a no, comes back as an event. The
same call asked again is the same card. A brain's card says the action in words and lists the call's
parameters by name, the long ones folded, on Telegram too; never the raw call. A call on a pull request
adds its title and the start of its description, read with the tool itself when the card is made. A task's card is shown:

1. **at launch**, on its start's card, for what it is lent outright: it waits (`held`) for the answer;
2. **on release** of a tool lent [held back](#resources);
3. **at the first call** of a category not granted yet; the calls that wait on it are answered with it.

A card at the first call or on release has a message of its own, the question, which notifies as
any question does. These cards and a start's go to
Telegram as well, whatever the messages around them did, answered from the linked account only
("Alerts only" sends none): whoever holds the user's Telegram session can approve, as they can
approve proposals. A task's raw permissions (a rule's `ask`, a command) stay out of Telegram. The
answers are kept in `decisions`, and a grant shows in the task's drawer. A task the user gave a
standing approval for, for a rule, needs no other card. A no refuses what waits; the next call asks again.

### Access levels

Each catalog server has an access level (`access`, `access.go`): **what** Eriphe may do on it,
kept apart from **how** its credential is scoped.

| Level | Workspace | Reads | Writes |
|---|---|---|---|
| `read-only` | Lecture seule / Read only | free | not lent: not in `tools/list` of a task nor in `list_tools`, denied by the policy and refused by the proxy |
| `reads` | Lecture libre, écriture sur accord / Free reads, writes on approval | free | on [approval](#the-approval-card) |
| `free` | Tout libre / Everything free | free | free, on a [clean start](#access-grid) |
| `ask` | Tout sur accord / Everything on approval | the user's | the user's |

A read is what the server itself marks `readOnlyHint`. `ask` is the former "Tout me demander":
the server's annotations do not count. `reads` is the default of the presets; "Other…" (a server
whose annotations nobody vouches for) starts on `ask`. An entry without `access` keeps its meaning:
`trust_annotations: true` is `reads`, without it `ask`; the old form values (`read`, `trust`) are
read the same way. Whatever the level, the built-in denials come first, then the file's rules.

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
| Google Drive | `drive.readonly` | `drive` (when "drop results" is ticked) |
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
`read < drop < write`: `drop` creates new files in an Eriphe-owned place (a folder's `Eriphe/`
subfolder) and never changes the originals; `write` changes the thing (for a repository: push).

```json
"resources": [
  {"kind": "repo", "ref": "me/site", "access": "write", "branch": "eriphe/fix-menu", "base": "main"},
  {"kind": "folder", "ref": "Clients", "access": "drop"},
  {"kind": "drive-folder", "ref": "Factures"},
  {"kind": "tool", "ref": "github.get_*"}
]
```

| Kind | `ref` | Access | What the task gets |
|---|---|---|---|
| `repo` | `owner/name` on GitHub, or a git URL; `branch` (work branch), `base` (start), `dir`; `trust_skills` | `read`, `write` | cloned into `/work/repo`; [credentials](#github-in-a-task) for it; its skills if it is the user's own or trusted (below); one per task |
| `folder` | a machine's folder, by name (`list_folders`) | `read`, `drop`, `write` (a folder at `manage`); at most the folder's own | mounted at `/work/folders/<name>`; the task runs on its machine or one keeping it up to date (copies) |
| `drive-folder` | a Google Drive folder, by name | `read`, `drop` (at most the folder's own) | for now, the files server whole (it reaches every Drive folder given to Eriphe, each with its own access) |
| `tool` | a catalog server, or `server.tool` with globs | `read`, `write`; from the tools' annotations when empty, and `read` is refused for tools that may act | relayed through the proxy |

The access defaults to `read` (for a tool, to what its annotations say). Each entry is checked
against what the user opened: an unknown folder or server is refused, and so is a `drop` on a
folder opened for reading, or a `write` on a folder not at `manage`. The list is kept with the task
(`tasks.resources`, JSON), and the node gets its spec fields (`repo`, `folders`…), derived from it.

**A repository's skills** (`.claude/skills`, `.agents/skills`, `.opencode/skills`: instructions
and scripts its agents load by themselves) load only from the user's own repositories
(`repo_skills.go`): with the GitHub App, one it is installed on; without it, one the GitHub tool's
token may push to. Anything else (another host, someone else's repository, GitHub not reachable)
keeps them off, unless the entry says `"trust_skills": true`. The node gets the answer as the
spec's `repo_skills` and turns them off per agent (node/README.md, Agent skills); whatever loads,
a skill never approves a tool by itself.

**Held back** (`"hold": true`, a `tool` entry only): for work the user validates before it acts
(analyse, report, then act on what they approved). The task sees the tool from the start (its
agent lists its tools once, and the node its servers when it fetches the task), but none of its
calls is authorized, whatever the grid or a grant says (`lend.go`). The brain asks with `answer_task`
`release` (the entries' `ref`, `mail.move`), for an entry lent held only: the user gets a
[card](#the-approval-card), and only their yes releases it. The release is kept in the task's
resources, what it needs is granted, it is logged in `decisions` (`release mail.move`), and the
brain is told the answer. An entry lent outright beside a held one wins.

**On a tainted turn** (the brain read external content), `run_task` refuses a folder with `write` or
`drop`, `trust_skills` and a Drive folder (its files server is lent whole for now): the brain asks the user and starts the task in the turn of their answer. Reads still
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
  as ignored: Eriphe's permissions stay the only judge (node/README.md, Agent skills).
- **A new version** (the same name, uploaded again) waits for approval with its diff against the
  approved one, file by file. Tasks keep the approved version until the user approves the new one
  (the hash they read is checked) or keeps the old one.
- **On or off**, and **delete**. Bundles no skill points at any more are deleted, but for the last five versions a skill replaced or had when deleted (`skill_kept`), which the [learning journal](#what-eriphe-learned) needs to undo a change; they are never given to a task.

**In tasks.** Every worker task gets the skills that are approved and on when it starts, as its
spec's `skills` (name and sha256 of each, `skills_tasks.go`). Its node fetches each bundle with its
own secret (`GET /v1/node/tasks/{id}/skills/{sha256}`: only that task's, only on its node), checks
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
helper in the task's environment. Each request crosses the node's relay, as for any
[access](../node/README.md#accesses), and eriphe-core decides and puts the token in.

**The decision** (`decideRepo`, deny by default; first that applies):

| | Answer |
|---|---|
| another repository than the one its `repo` [resource](#resources) covers | deny |
| a read of it | allow |
| a write (git's push, gh's pull request) to a repository lent for `read` | deny, at once: a push is not lent |
| a write on the declared `branch` of a repository lent for `write`, on a clean start (the user's own clean turn, or a watch they authorised) | allow |
| any other write: no declared branch, or a start that is not clean | the user's yes on the start's card, else deny |

What is decided is the lend, at launch, as for an [access](../node/README.md#accesses): what the task reads afterwards
changes nothing, and nothing waits in a command. The same rules hold with the GitHub App or without.

gh's token writes pull requests and issues, never contents: GitHub itself refuses it a merge, a
release, a repository's settings or its secrets. A task that must merge is lent the catalog's GitHub
tool, under its grid.

**No token in the task.** The token is minted here and goes in on the way out (`git_access.go`).
gh's requests, to `api.github.com`, are not classified (much of it is one `POST /graphql`). It works on the one repository
the task is lent: a REST path naming another is refused, and its token is for that repository, with
write rights if a write is allowed, otherwise read rights, so GitHub refuses the write. A read with no token to be had (a public repository the App is not on)
goes without one. gh needs the GitHub App: the tool's own token holds everything, and gh's requests
are not classified:

```
git → the node's relay → eriphe-core (decides, mints) → GitHub
```

- **Each request is decided**, from what it is: a push is `git-receive-pack`, and a placeholder
  proves nothing. A refusal
  is a 403 with its reason in plain text, which git shows as `remote: …`.
- **What is pushed is not read**, as for any access: what is decided is the lend, a repository, read
  or write. The `branch` lent is what the task is told to push to, and the real limit is the
  provider's: a token for that one repository, and the repository's protection rules on GitHub. The
  shell rules (policy.go) stop a push to `main` or a forced one by mistake, not a script.
- Other requests to `github.com` go as they are, with no token: a public clone, a release's
  download. So they cross eriphe-core too, and a program that refuses the task's certificate fails
  on `github.com`. Large File Storage is a write when the task may write there, else a read.

**Asked at launch.** What a task started by `run_task` needs of the user is foreseen when it starts,
with the same decision (`startNeeds`, `task_start.go`): for each GitHub repository lent for `write`,
its push. When that takes the user's yes, the task does not start: it is kept `pending` with
`desired` `held` (no node is given it, no placement moves it, its `pending_reason` says why) and the
user gets one card in the conversation (kind `taskstart`, [conversation cards](#workspace-web)),
« Pour pousser `eriphe/x` sur owner/repo, il me faut ton accord. ». Oui keeps the approval
(`repo_grants`, logged "approved at launch": a write to that repository, by git or gh, for the
rest of the task) and lets the task go; Non stops it and tells the brain. Like every card, only the
user's click or a plain yes or no of theirs decides it, never the brain (`answer_task` has nothing
to answer on a held task; `stop_task` cancels it). A task under a rule's standing approval needs no
other card: the yes on its approval card covers its repository.

**The credential.** With [the GitHub App](#the-github-app), an installation token restricted to the
one repository and the rights asked for, valid one hour: git reads `contents: read` or `write`; gh
reads `contents`, `pull_requests`, `issues`, `checks`, or writes `pull_requests` and `issues`
(never `contents: write`: a merge is not a task's). eriphe-core reuses a token for the same task and
rights until ten minutes before it ends. Without the app, git goes with the GitHub tool's own bearer
token (**fallback**): it stays in eriphe-core too, each request is checked against the repository
lent, and the log says so. gh cannot use it, and the Tools page says to create the app.

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

**GitHub connections on the app.** A catalog tool on the app is
`{"auth": {"github_app": true, "account": "acme"}}`: its tokens come from the installation on that
account or organization (without `account`, the owner's, or the only one), with its repositories
and rights (reading only for a read-only tool), renewed five minutes before they end, cached per
account. One name is one connection with its own rights, so `github` can use the user's
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

## Assistants

An assistant is a long-lived agent that owns one task, as a Kubernetes Deployment owns its Pod.
Its spec says what it is and what it runs on; its status is what eriphe-core keeps of its task:

| Spec | |
|---|---|
| `role` | what it is: a card in `assistantRoles`, `brain` or `assistant` (below), with the powers it allows |
| `display_name` | what the user calls it, as a person (see [its name](#the-assistants-name)); Eriphe for the brain by default, its id for another |
| `agent`, `node` | what it runs on; without an agent, the default one or the first that can run it |
| `instructions` | its standing instructions (`{{user}}` and `{{name}}` replaced); its role's built-in ones by default; `instructions_file` in the file only |
| `tools` | its grant from the catalog, like a task's; its role may add the catalog's free reads |
| `resources` | what it is lent besides its tools (`{kind, ref, access, …}`): kept, lent with the resources work |
| `powers` | what it may do, from the closed list below, within its role's; its role's defaults when empty |
| `triggers` | what wakes it, each a kind its role allows, some with an argument after a colon: `conversation` (the user's messages and the tasks' events), `clock` (an intention of the memory that comes due: the wake packet, see [Memory](#memory); or a watch that fired, see Watches), `schedule:<when>` (below). Its role's by default (the brain's: `conversation` and `clock`); `conversation` and `clock` only with `receivesConversation`, and one enabled assistant at most has the conversation |
| `reports_to` | the assistant it reports to: the brain (the one with the conversation), the only one it may name; empty means the brain. The brain reports to no one |
| `disabled` | its task is stopped, and none started |
| `self_url` | the file only (see the brain) |

Powers are a closed list. A role's card lists those its assistants may have, and the ones they get
when their spec names none; an assistant's `powers` are its role's or fewer. The code checks an
assistant's powers where it decides, never its name:

| Power | |
|---|---|
| `talksToUser` | what it writes goes to the conversation |
| `receivesConversation` | it may have the `conversation` trigger (needs `talksToUser`) |
| `launchesTasks` | eriphe-core's own MCP tools: it starts, follows and stops tasks, and reads and writes the memory |
| `lendsResources` | it lends the tasks it starts their resources (needs `launchesTasks`) |
| `notifies` | it tells the user something on its own, with `notify`: the same rules, quiet hours and cap as the brain's |
| `reports` | it reports to the brain with `report` |

The brain's role has all but `reports`: it reports to no one.

### The assistant role

An assistant of role `assistant` is any other: a mission (its `instructions`), woken by its
triggers, reading with the tools lent to it, under the grid. **One voice, one orchestrator**: it
never talks to the user nor launches a task. By default it may only `reports`; `notifies` is given
by the user. It runs as a permanent task, like the brain, placed on an always-on node.

- **Its instructions** are its role's frame (`assistant-prompt.md`, in French: who it is, that what
  it reads is data, `[rien]` when there is nothing), then its mission.
- **Its wake-ups**: `schedule:<when>`, an interval (`every 15m`, `toutes les 2 h`: 5 min to 24 h)
  or days and times of day as a watch's schedule (`weekdays 08:00`, `every 2h 08:00-20:00`), in
  the user's zone. A schedule seen for the first time waits for its next time; times missed while
  it was busy or stopped make one wake. Its turn reads `[réveil : horaire « every 15m »] Fais ta
  mission.` More kinds come with the next steps (another assistant's state, a tool).
- **Its tools**: its own MCP server, under eriphe-core's name, built for it as its powers say
  (`report`, `notify`) and relayed by its node: it holds no token. The free reads among its catalog
  tools are allowed in advance; any other call is refused, since it has no approval path yet.
  Of its harness's own tools it keeps only the search for deferred tools.
- **`report`** puts an event in the conversation, `Rapport de l'assistant <name> : …`, which the
  brain reads at its next idle and judges: tell the user, start a task, or nothing.
- **`notify`** goes through the brain's notification path, its why prefixed with the assistant's
  name; it is not written in the conversation.
- **Its taint is its session's**: no message of the user's ever starts a clean turn for it, and
  its session keeps what it read. Once it read external content (a catalog tool of a server not
  trusted, the web), every report it makes taints the brain's turn that reads it, and its
  notifications are tainted (they never interrupt, carry no link). A new session starts clean; a
  resumed one keeps its taint.

### Facts

What Eriphe knows now lives in **one fact store** (`facts.go`). A fact is an owner's field: a value
of a closed type (a choice, a number, a date, a text, rows of such), its time, its validity
(expired, it reads unknown), its **provenance** (who wrote it: an assistant, `machine:<node>`,
`src:<id>`, `ha`) and its **taint** (why its writer was tainted when it wrote it; a closed value
never taints). Two kinds:

| Kind | Owner | Written by | Where |
|---|---|---|---|
| kept | an assistant's name | the assistant, through `set_state`, checked against its schema | the database (`facts`, history in `facts_log`, 7 days) |
| ephemeral | `context` | code alone, from the sources the user turned on (a node, a token source, Home Assistant's follower) | memory only, no history, gone at a restart |

The context's facts are the user's (`context.place`, `context.room`, `context.focus`,
`context.busy_until`), the house's (`context.home_activity`) and each device's
(`context.<device>.presence`, `.dnd`, `.activity`), folded from the signals below. The names
`context`, `eriphe`, `brain` and `user` are kept: no assistant takes them.

The brain reads them in each turn (the `[contexte : …]` and `[assistants : …]` lines, closed values
only), and with its tool **`facts`** (free, no model; `owner` to filter): each fact with its value
if closed (rows by their count, a text by its field's name only: `assistant_view` reads it), its
age, its validity and its writer. The router reads the context's facts. Facts orient, never
authorise: the grid, the cards and the approvals never read them.

Before #244 step 3 the kept facts were the tables `assistant_state` and `assistant_state_log`,
keyed by `assistant`: they are renamed at start, their rows and history kept.

### An assistant's state

What an assistant knows now, typed (`assistant_state.go`): its kept facts. Its schema is its spec's `state`: up to
20 fields, each

| Key | |
|---|---|
| `name` | lowercase letters, digits and `_` |
| `type` | `choice` (with `values`, 1 to 30), `number`, `date` (`YYYY-MM-DD` or `YYYY-MM-DDTHH:MM`, the user's zone), `text` (500 characters), or `rows`: a list of rows whose `columns` are such fields (no rows in rows), at most `max_rows` (50 by default, up to 200), keyed by the column `key` if any |
| `ttl` | its validity, `15m`, `4h`, `2d` (1 min to 30 days); 24 h by default. Expired, it reads unknown |
| `summary` | in the brain's line: a closed field, or rows by their count; never a text |

Its one writer is the assistant, with its own tool `set_state`, checked by code: a value off its
type or list, an unknown field refuse the whole call, nothing written; `null` clears a field; rows
with a key take the whole list or `{"upsert": [rows], "remove": [keys]}`. Its tool `state` reads it
back. Its instructions describe the fields, in French. The whole state holds at most 64 KB; each
write goes in its history (`facts_log`), kept 7 days and 2000 writes per assistant. It
goes with the assistant when it is removed; a value its schema no longer takes reads unknown. For
now an assistant that launches tasks (the brain) has no state.

**Taint per field**: a closed field (a choice, a number, a date, rows of them) never taints; a
text, or rows with a text column, keeps why its writer's session was tainted when it wrote it.

**The brain's line**: each brain turn opens with `[assistants : …]` after the context line, one
part per enabled assistant with summary fields, closed values only, and the age of the oldest:
`[assistants : Vigie : prod ok, incidents 2, mails 3 (il y a 4 min)]`. It never taints. Settings →
Agents shows each assistant's state, with its age and whether it was written after reading
external content; `GET /v1/assistants/{name}` returns it as `state`. The state orients, never
authorises: the grid, the cards and the approvals never read it.

### An assistant's views

Named filters over its state, run by code, that it exposes as read tools (`assistant_views.go`).
Its spec's `views`, up to 10:

| Key | |
|---|---|
| `name`, `about` | its name (as a field's), and what it gives in a few words |
| `field` | a field of its state |
| `where`, `sort`, `limit`, `columns` | rows only: the rows whose columns have these values (checked against them), sorted by a column (`-recu` descending), at most `limit` (20 by default), with these columns only |
| `history` | a field's history over a window, `24h` to `7d`, newest first; not for rows |

A view costs no model and answers with its freshness: `fresh` (known, not expired, its assistant
running), when it was written and until when it holds, and a note when its assistant is late,
failing or turned off, so that the brain reads the source tool itself: it is never blocked.

The brain reads them with two tools of eriphe-core's own, under the concept `assistant`:
`assistants` (every assistant, its status, powers and wake-ups, its closed values, its text fields
by name only, its views) and `assistant_view` (`assistant`, `view`, `n` to cut rows). Both are
free; `assistants` never taints, and a view taints the brain's turn (`assistant:<name>`) only when
what it returns is not closed (a text, or rows with a text column) and was written by a tainted
session. Settings → Agents lists each assistant's views.

### Lent facts

An assistant reads only its own state, and the facts it is lent: its spec's `facts`, owners
(`context`, or another assistant's name; never its own, never a name Eriphe keeps). It reads them
with a tool of its own, `facts` (an `owner` among them, or all), closed values only, as the
brain's `facts` shows them; an owner it is not lent is refused. Its instructions name them.

### Creation by Eriphe

The brain proposes an assistant; the user makes it on its **creation card** (#244 step 6,
`assistant_proposal.go`, `assistant_templates.go`). A rule comes first: `propose_assistant`
refuses without one of an assistant's reasons (`justification`: `boundary`, it reads untrusted
content so the brain does not have to; `tools`, tools or rights of its own; `wake`, a wake-up of
its own that needs a model; `parallel`, work beside the brain's) and a sentence of why a rule
would not do (`why`).

**Templates** (`templates`, free): code reads an assistant's facts (views, conditions, the summary
line), so their schema is known in advance. `tri-mails` (rows of mails: category, urgency, waits
for a reply, due date, received; views `urgents`, `derniers`, `attend_reponse`; woken every hour,
07:00–22:00) and `veille-techno` (rows of what is new, with a sentence for the brief; woken daily)
are assistants; `surveiller-service` is a rule (`outil`, every 5 minutes, on the call failing),
not an assistant. The brain fills what is the user's (the tools to lend, the topics, the times)
and may override any part.

`propose_assistant` checks the assistant as it would be made (`checkAssistant`), its lent facts,
and its wake rules (1 to 3, rule's events that only wake it: no `then`, grant, urgency nor text;
no `alerte`, whose address is the user's), then keeps it in `assistant_proposals` and puts it on
a card (kind `newassistant`). The card shows the six blocks: the assistant and its mission, the
facts it writes and those it is lent, the events and the rules that wake it (as Settings → Règles
says them), its channels (its powers), its tools; then why, where to see it, and its estimated
cost: its wakes in the next 30 days (those on a schedule) times what a turn of its model cost over
the last 30 days, where its harness said (`turnCost`), or "not known yet". On the yes, eriphe-core
makes the assistant and its rules (`then assistant:<name>`, by the owner) at once, all or nothing;
on the no, nothing.

### Real cases (#244 step 8)

- **GetOut production, with no assistant.** An `outil` rule (the `surveiller-service` template, on a
  Grafana query every 5 minutes) keeps its incidents, deduplicated by code (`rule_incidents.go`,
  table `rule_incidents`): one opens when its condition becomes true, confirmed on a second look,
  and closes when it recovers; a true condition seen again while one is open opens nothing. They
  are closed facts owned by `rules`: `rules.r<id>.incident` (`open`, `closed`; unknown before its
  first look, or while its look fails with none open), `rules.r<id>.incidents_7d`,
  `rules.r<id>.since`. A condition, a `fait` rule, the brain's `facts` and an assistant lent
  `rules` read them. The rule wakes the brain on the transition; the prompt has it notify, then
  start an investigation (a task, or a session), a fix going through a PR merged on a card.
- **Mail triage**, an assistant from `tri-mails`: one row per mail, its views, a rule may count its
  rows (`tri-mails.mails > 0`).
- **Tech watch**, an assistant from `veille-techno`, woken daily, that reads the web itself: the
  power `readsWeb` (assistant role only; `assistant_web.go`) gives it its harness's search and page
  reader. A URL is a way out, so an assistant that reads the web is lent no catalog tool and no
  fact: it holds nothing to send. Each read is decided as the brain's are: a local or credentialed
  URL is refused; the web's value in Settings → Tools holds (Never refuses, On approval refuses too:
  an assistant never waits on a card); anything else of its harness is refused. Its `brief` view is
  read at the brief. The template's mission keeps `<les sujets>` until the brain fills it with the
  user's words; `propose_assistant` refuses a mission with a hole.

### The team

Settings → Agents shows the brain's assistants as **the user's team** (« Ton équipe »): for each
one of the assistant role, besides its agent, powers, state, views and tools, the rules that wake
it (as Settings → Règles says them), who reads its facts (the brain, in its summary line or when it
asks; the rules whose `fait` or `if` names them; the assistants lent them), the facts it is lent,
and its last 30 days (`team_stats.go`): wakes by its rules, turns and their cost where its harness
said, reports, notifications and how many the user found not useful or not now, values written.
Its cost is its tasks' (table `assistant_tasks`, kept as the controller starts them; a task of
before it is not counted). Settings → Règles gives each rule its last 30 days too: fires, those
its condition held back, cards made during its wakes of the brain (from the wake to its end, an
hour at most), corrections of its notifications, what the brain's turns then cost; three cards or
more say a standing approval may be missing. The brain reads the same in `assistants` and `rules`
(`last_30_days`).

The status (`GET /v1/assistants`) has its conditions `Running`, `Busy` and `Ready`, its current
task, the agent it runs, its session, its last turn and its last error; the controller keeps its
restarts there too (table `assistant_status`).

One controller keeps every assistant as its spec says: an enabled assistant has exactly one task;
one that fails starts again after 5 s, then doubling up to 5 min, on its session while the resume
works; a change of its agent, instructions, name, node or resources, or of the tools its grant reaches,
restarts it once its turn is over; a disabled or removed one has its task stopped. In
eriphe-core's first minute, it waits for the trusted servers still connecting. What an assistant
gets comes from its role's card, never from its name, as a runtime's from its harness card: its
powers (eriphe-core's own tools among them), its free reads, the system network, streaming, the harness tools
allowed in advance, its node profile, its first turn and its approval path. What wakes it and
where its words go come from its triggers' cards.

The brain is the built-in assistant `eriphe`, of role `brain`: the `brain` section of the file is
its spec (read-only like the file's other entries). Settings → Agents opens on it, as "the brain",
never as an assistant: its name, how it is doing in plain words (available, replying, stopped, no
AI chosen) and its last error, the agent it thinks with, the agents it hands tasks to, and in
technical details its task, its last turn and its extra tools, each set there when the file leaves
it out. Below, the brain's assistants, each saying whom it reports to. Other assistants are
added through the API only, for now; a file may also list them under `assistants`. Each card says
its role, its powers and its wake-ups. An install from
before moves by itself at start: the brain's `brain_*` keys become its status, the setting
`brain_agent` its workspace agent, and the same agent keeps its running task.

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
`short_name`), an added channel's app (`GET /v1/channel`, Home Assistant's conversation agent), and the results subfolder
of a machine's folders lent with `drop` (below). The software keeps its name: the logo glyph, the
images and binaries, the install scripts, eriphe-core and eriphe-node, the GitHub and Google apps
and the OAuth client, the certificate authority, the app passwords and tokens named Eriphe, the
Home Assistant integration's domain, the system texts that name the software, and the results
subfolder of Google Drive folders, `Eriphe/`.

**The results folder.** A machine's folder lent with `drop` takes results in a subfolder named
after the assistant: `Eriphe/` until it has another name, then `Jarvis/` (the display name as a
folder name, `results_folder.go`). A task's spec gives its node the current name (`results`) and
the former ones, newest first (`former`, kept in the settings `results_name` and
`results_former`; `Eriphe` is always among them). The task always sees the current name
(`/work/folders/<name>/Jarvis`). On a folder's next use after a rename, the node renames a former
results folder to the new name; if a folder of the new name is already there (the user's own, say),
nothing is merged: the former one stays in use, mounted under the new name, and the node says so
(a `results_conflict` event, in the device's access log, to rename or merge by hand). A node
older than this names nothing and writes in `Eriphe/`: for a node that does not report
`results_named` (nor runs a release from 0.7.0), the worker is told `Eriphe/`, the folder that node
really mounts, so a rename never points a task at a path it cannot write.

## The brain

With a `brain` section in the config, eriphe-core keeps a permanent orchestrator alive: a long-lived
task on a node (stable `profile`, so its session survives restarts), with only MCP tools
(eriphe-core's own plus its grant from the catalog, `brain.tools`) and its standing instructions:
`brain-prompt.md`, built into the binary, or the file `prompt_file` names instead.

**How it reaches eriphe-core.** Without `self_url`, the brain's node relays eriphe-core's own MCP
server to it as it relays a catalog server (`<callback-url>/mcp/eriphe-core`, with the task's
secret; the node calls `/v1/node/tasks/{id}/mcp/eriphe-core` with its own): it works wherever the
node reaches eriphe-core (the same machine, the LAN, the tailnet), with no address to configure,
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
not [Never](#access-grid), through its proxy like any task. Its Free reads are allowed in advance
in its assignment (`allowed_tools`) and called without a ticket: no permission round trip, a
decision on record all the same. Everything else goes through the policy: a Free write is allowed
on a clean turn and asked on a [card](#the-approval-card) on a tainted one, On approval is a card
per call. A read named by a
policy rule of the configuration is not free: it goes through the policy with its input. The list
is computed when its node fetches the task, so a tool or server added later reaches the brain at
its next start (the proxy itself follows the catalog at once); in eriphe-core's first minute, the
brain waits for the trusted servers that are still connecting before it starts.

Its other catalog tools (`brain.tools`) are not allowed in advance: each call goes through the
policy like a worker's, and a call
without its permission is refused by the proxy. What is the user's waits for the user: eriphe-core says so in the conversation, and the brain's
task shows in `GET /v1/tasks` (and the CLI) until the user allows or denies it. The brain's other
permission requests are denied, but for the web.

**The web.** The brain searches with its harness's own search: Claude Code's `WebSearch` is in its
`allowed_tools`; Codex is held to `web_search = "cached"` by the node; OpenCode's `websearch` is
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

eriphe-core, not the harness, holds the thread: the harness session (`--resume`) is only a cache.
A fresh session (the first one, a dropped resume, a resume the node could not do, another harness)
gets a context pack in its first turn, never in its system prompt: what waits for a decision, the
tasks under way, the intentions under way (the due ones first), the last day's episodes, and the last
messages between the user and the brain, the newest kept, within about 8,000 tokens (`brain_context.go`). It is quoted
data; events are left out, since they carry what tasks said. eriphe-core logs its size.

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
(`[Sam 12:00 #N · Home Assistant, Bureau]`, `· canal sans confiance` when not trusted), and the
brain answers it in the same thread; with a place, short and without markdown, as it is most often
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
and says each sentence on a speaker of its area. What an automation knows of the house (someone
came home, the TV is on) goes to `POST /v1/context` as a signal, with a source token of its own.

## Google accounts (Gmail, Calendar, Drive)

One sign-in per Google account gives Eriphe any of Gmail, Google Calendar and Google Drive
(`google.go`; Settings → Tools → Google, `/settings/google`, `web_google.go`). The user's own
Google app ([below](#your-own-google-app)) is set up once for every service and every account; the
page's guide turns the three APIs on with one link (Gmail, CalDAV, Drive) and gives the scopes and
the redirect URI to copy.

| Service | Scope | Server |
|---|---|---|
| `mail` | `https://mail.google.com/` (IMAP and SMTP with XOAUTH2) | eriphe-mail, an account of `mail`, Gmail preset |
| `calendar` | `https://www.googleapis.com/auth/calendar` (CalDAV) | eriphe-calendar, an account of `agenda`, provider `google` |
| `drive` | `…/auth/drive`, or `drive.readonly` without "drop results" | eriphe-files, `fichiers`, one account of it per Google account |

**Connect.** The page ticks the services, and Connect asks Google for `openid email` and their
scopes only, with `access_type=offline`, `prompt=consent`, PKCE, the signed state of
[the Drive flow](#your-own-google-app) and `include_granted_scopes=true`: a service added later to
the same account (its `login_hint`) asks for that one alone, and Google's answer covers what the
account granted before. The address comes from the ID token of the token endpoint's answer (over
TLS, which OpenID Connect takes in place of its signature). The refresh token, with the client id
and secret, is kept **once per account** in the vault (`google.<hash>.token`), and each service's
server reads it from there in its environment (`MAIL_GOOGLE_TOKEN_<hash>`,
`CALENDAR_GOOGLE_TOKEN_<hash>`, `FILES_GOOGLE_TOKEN_<hash>`), refreshing its access token itself: the token never enters a task or
a spec. Which servers belong to an account is read from their specs (that vault reference). A
service Google did not grant (its box unticked on Google's page) is said back. Connecting again
writes the new token for every service of the account, and the nodes restart them with it.

**Several accounts.** Every account's mailbox is an account of the one `mail`, its calendar an
account of the one `agenda`, its Drive folders in the one `fichiers`, each folder naming its
account. The page lists the
accounts, each with its services, their tools and states, "Ajouter" for a service it has not,
"Reconnecter" and "Retirer ce compte" (its servers and Drive folders go, its token leaves the vault
and is revoked at Google, `oauth2.googleapis.com/revoke`).

**To reconnect.** Every 30 minutes (`googleTick`, with the nodes' check) eriphe-core asks Google
for an access token with each account's refresh token. `invalid_grant` (revoked, or the 7 days of
an app in Testing) or a refused client marks the account "À reconnecter": its servers are held in
`needs_auth` (the pool's `authHold`; a server with another working account is not), a call says
why, `list_tools` gives as `connect` where to do it (`Settings → Tools → Google → Reconnect
<address>`), and the brain is told once (an event in the conversation). The Tools list and the
page show the account to reconnect with a one-click "Reconnecter", which asks again for the
services it had. Google out of reach changes nothing. Gmail and Calendar also say "reconnect it in
Eriphe" when Google refuses their refresh.

Gmail with an app password (the Mail form's other Gmail) stays for a workspace Google cannot send
back to (a local address, [below](#addresses-google-takes)).

## Google Drive (cloud Folders)

A folder of the user's Google Drive becomes a Folder given to Eriphe: a name, the cloud as its
source, and an access, `read` or `drop` (read, and drop results in its `Eriphe/` subfolder; the
originals are never changed). Tasks reach it through `eriphe-files` (servers/files), a local
server like eriphe-mail, which **enforces the folders itself**: it takes only the folder ids it was
configured with and what is inside them (it walks each id's parents up through the Drive API),
refuses everything else, and can only create files, in `Eriphe/`. eriphe-core runs the
authorization, keeps the refresh token in the vault, and lends it to the service's environment on
its node; it never enters a task or a task's spec.

Settings → Tools → Google Drive (`/settings/drive`, `web_drive.go`, the logic in `cloud.go`):

1. **Your Google app**: the guide, then the client JSON (or id and secret) of the user's own
   Google app ([below](#your-own-google-app)), kept in the vault; the same as the Google page's.
2. **Connect**: "Connecter Google Drive" is the [Google account](#google-accounts-gmail-calendar-drive)
   flow with Drive alone; "Permettre à Eriphe de
   déposer des résultats" asks for the scope that writes. The account comes back connected, and the
   workspace creates the catalog server `fichiers` (image `ghcr.io/alexis-marcel/eriphe-files`,
   access `reads`: its reads are free, `write_file` is the user's to allow). Set to read only,
   the tool connects with `drive.readonly` whatever the box says.
3. **Choose**: once the service runs, the page browses each account's folders (a chip per account
   when there are several) (My Drive, Shared with
   me, shared drives) through the server's `browse_folders`, and each one is given "en lecture" or
   "lecture + dépôt". The chosen folders are written in the service's `FILES_CONFIG`, which
   restarts it; removing one, or changing its access, does the same.

`browse_folders` is marked `_meta: {"eriphe/setup": true}`: the pool leaves such a tool out of a
server's tools (`setupTool`, `listTools`), so no grant, no task and not the brain can see or call
it; only the workspace calls it (`browseDrive`).

### Scopes

| Asked | When | Why |
|---|---|---|
| `https://www.googleapis.com/auth/drive.readonly` | "drop results" unticked | list, search and read inside the chosen folders, which can be any folder of the account, shared ones included |
| `https://www.googleapis.com/auth/drive` | "drop results" ticked (the default) | the above, and create `Eriphe/` and new files inside a chosen folder |

`drive.file` is not enough: it only reaches files the app created or that the user opened with
Google's Picker, so it can neither list a folder the user already has nor create `Eriphe/` inside
it. Both scopes kept are **restricted** at Google: an app that is not verified shows "Google
hasn't verified this app" (the user goes through "Advanced") and is limited to 100 users, which
is no limit for a user's own app.
`drive` lets the token change anything in the Drive: eriphe-files has no call that does
(only creations, only in `Eriphe/` of a `drop` folder). Reconnected read only, every folder falls
back to `read`. `prompt=consent` and `access_type=offline` make Google give a refresh token at each
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
typed, and kept in the vault as one secret, `google-drive-client` (`PUT /v1/secrets/
google-drive-client` sets it too); the secret is never shown back or logged. A Desktop client
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
same link is refused. The refresh token goes to the vault with the client id and secret, once per
account (`google.<hash>.token`), which the servers need to refresh it.

### Addresses Google takes

Google checks an authorized redirect URI against its
[validation rules](https://developers.google.com/identity/protocols/oauth2/web-server#uri-validation):
https (http only for localhost), no raw IP address (loopback ones aside), and a host whose
top-level domain is on the public suffix list. `googleOrigin` applies them to the workspace's
origin (`publicsuffix`, ICANN section; `home.arpa` counts as local): a workspace opened at
`https://192.168.1.2:8443` or `https://eriphe.local` cannot connect Google, and the page says so
instead of showing the guide, with what to do:

- turn on **Remote access** (Tailscale gives `https://<name>.<tailnet>.ts.net`, which Google takes;
  when it is on, the page links to itself at that address);
- or put Eriphe behind the user's own domain (`https://eriphe.example.org`);
- or, with no Google app at all, "Google Drive for desktop" on an always-on computer, its synced
  folder given as a folder of that machine ([Folders](#folders)).

`startGoogle` refuses such an origin too (`drive.origin.ip`, `drive.origin.local`,
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

## Local MCP servers (services)

A local MCP server runs as a long-lived container on a node, like a Kubernetes Deployment of one
replica behind a Service: eriphe-core keeps what it should be and where, the node keeps its
container running, and eriphe-core reaches it as a streamable HTTP MCP endpoint through the node's
tunnel. It is a catalog server with `run` (a `RunSpec`, `services.go`) instead of `url` and `auth`;
the pool reaches it through `dialService`, and grants, the policy and the per-task proxy treat it
like any other server:

```json
"servers": {
  "thinking": {"use": "think step by step", "run": {"image": "mcp/sequentialthinking", "transport": "stdio", "egress": false}},
  "notes": {"use": "my notes", "run": {"image": "<your image>", "transport": "http", "port": 8080, "node": "nas", "volume": true,
            "env": {"LOG_LEVEL": "info", "API_KEY": {"file": "/secrets/notes-key"}}}}
}
```

`mcp/sequentialthinking` (Docker Hub's `mcp` namespace, Node.js, stdio, no environment) runs as is
with the defaults. Not every image does: `mcp/time` keeps its Python interpreter under `/root`
(mode 700) and only runs as root, which a service never does; such an image needs rebuilding.

| Field | What |
|---|---|
| `image` | the server's image |
| `transport` | `stdio` (the node holds its stdin and stdout) or `http` (it listens on `port`, MCP at `path`, `/mcp` by default) |
| `args` | arguments after the image's entrypoint |
| `env` | `"NAME": "literal"` or `"NAME": {"file": "/secrets/x"}` (or `{"file": "vault:<name>"}`): the secret is read when the node fetches the service, sent to it, never stored or logged |
| `node`, `needs`, `prefers` | as for agents: pin a node, require labels, prefer `cpu`, `memory` or a label |
| `volume` | a persistent `/data`, kept on its node in `<root>/services/<name>` |
| `user` | `uid:gid` in numbers, `1000:1000` by default; never root |
| `writable` | a writable root filesystem (read-only by default) |
| `egress` | reach the network (default `true`); without it a stdio server gets `--network none` |

The `services` table keeps each one's spec (without secrets), its hash, its node, desired state,
state (`pending`, `starting`, `running`, `failed`, `stopped`), restarts and last error;
`GET /v1/services` (bearer token) lists them. The scheduler places a service like a task, on
online nodes that run services (new nodes say so in `GET /v1/node`), with `needs` and `prefers`,
counting the node's services and tasks as its load. A node gone for 5 minutes loses its services
to another node, unless they are pinned to it or keep a volume there: their data stays where it
is, so they wait for their node. A forgotten node (`DELETE /v1/nodes/{name}`) loses them all.
A changed spec starts the service over on its node, or elsewhere if the node no longer fits; a
server left out of the configuration is removed, and its node stops it (the volume stays).

Nodes receive their services in their assignments, as a `services` list next to `tasks` (old nodes
ignore it), and report each one on `POST /v1/node/services/{name}/report`
(`{state, restarts, error}`). A service whose secret cannot be read fails and is not sent.

Credentials are lent, never stored: they are read at every fetch of the assignments, and the node
restarts a service whose environment changed, its `/data` untouched. Rotating a file-backed
password restarts the server with the new one at the next fetch; a token source lent by
eriphe-core (an OAuth access token for XOAUTH2, refreshed by eriphe-core) fits the same place,
as another kind of `env` value resolved in `lendService`.

`c.dialService(ctx, name)` returns an `mcp.Transport` (a `StreamableClientTransport` to
`http://node/v1/services/{name}/mcp` over the node's tunnel, with no timeout): the pool's
`dialLocal`. When a node reports a service `running`, its upstream is woken instead of waiting
for its backoff. It fails, with the reason, if the service is not
placed, not `running`, or its node not connected; `serviceEndpoint` gives the `*http.Client` and
URL instead. `registerServices(map[string]RunSpec)` sets the desired services; eriphe-core calls it
at start with the catalog's `run` entries (unknown `run` fields are refused).

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
publishes them under; a development build ("dev") runs the untagged image. A node pulls an image
it does not have, never a newer one of one it has: without this, a machine kept the image it pulled
first. A new version of eriphe-core changes their specs, and their node runs them again with the
new image.
A tool of another version still says so: its version (its MCP `serverInfo`) is in `list_tools`
(`version`, and `want_version` when it is not eriphe-core's), its row in Settings → Tools says it
may misread its configuration, and an error of its tells the brain that likely cause.

Two calendars can share an address: an Apple account's and a Google account's. They are two
accounts. The Google one keeps the address as its name (the Google account finds its calendar by
it); the other is named after where it is (`sam@example.org (iCloud)`, or its server's host). A
Google account connected after an Apple calendar of its address renames that calendar, which keeps
its secret, its level, what was allowed for it and its watches. The same calendar added twice is
refused.

Before, each account was a server of its own (`mail`, `mail-sam`, `agenda-icloud`…). At start,
`mergeAccountServers` (`accounts_merge.go`) makes them one, under the service's own name when one
has it: each account keeps its secret's vault reference (nothing is asked again) and the level of
the server it was; the actions allowed for good, the watches (which then name `account`) and the
assistants lent a server move to the one left. Two calendars of one address are both kept, named
as above. It does nothing once they are one.

## Folders

The user's own folders, given to Eriphe ("Dossiers"). A folder is a name ("Clients"), a source (a
machine and a path inside one of the places that machine shares, declared when it was installed:
`--share NAME=/PATH`, see install/README.md; eriphe-core never sees a host path, only the place's
name and a path relative to it) and a level: `read` (Lecture, the default); `drop` (À côté), read
and drop results, which go only to the folder's results subfolder (`Eriphe/`, or the assistant's
name: see [the assistant's name](#the-assistants-name)), the originals never changed; or `manage`
(Gérer), read, create, change, rename and delete anywhere in it, its node taking a snapshot of the
folder before each such task (node/README.md, Shares). A folder is shared on the machine that holds
it, by the user there (`eriphe-share add`, or a right click in the Finder or the Explorer): its
node keeps the list and reports each folder's `name`, `access` (its level now), `max` (the level
chosen on the machine), `copyable` (the user allows copies on their other machines, for the
synchronisation to come) and `id` (`folders` in `GET /v1/node`), never its path. eriphe-core reads
that list, and may only restrict it: take a folder off, lower its level, turn its copies off. It
cannot add a folder, raise one (not even back to `max`), nor look into a machine. A name shared by two machines is told
apart as `Name (machine)`.

**Two sources, one list.** A folder is on a machine (here) or in the user's Google Drive (see
[Google Drive](#google-drive-cloud-folders)). Settings → Dossiers, `GET /v1/folders` and
`list_folders` show both, each with its `source` (`machine` or `google-drive`) and `lend`, how a
task gets it. Each source keeps its own store and its own enforcement: a machine's folder is in its node's list
and mounted by its node; a Drive folder stays in the files server's `FILES_CONFIG`,
which eriphe-files reads and enforces itself, and is lent through that catalog tool (`tools:
["fichiers"]`, which reaches every Drive folder given to Eriphe), not through `folders`, which
refuses it with that hint. Drive folders are added on Settings → Tools → Google Drive; "Ne plus
partager" in Settings → Dossiers removes either kind.

**Who decides a write.** The level says what a task may reach; who decides is set per folder,
like a tool's grid: lending it at `drop`, and at `manage` (lent as `write`), is Free (the default)
or On approval ("Sur accord"), two selectors under the level in Settings → Dossiers (`POST
/settings/folders/value`, kept by machine and id, `folder_values.go`). On approval is asked on the
task's [start card](#the-approval-card), once per task, before it starts; so is a Free write of a
task that did not start cleanly (a tainted turn, or a turn that is not the user's). Reads are
always free. A yes lets the task go with its mount; a no stops it.

**Sharing one** is done on the machine; Settings → Dossiers lists, machine by machine, what is
shared there, each folder with a level selector that offers only its level and those below, a
"Peut être copié" badge with "Ne plus copier", and whether it is there now; one "Comment partager ?"
button opens a dialog with the three levels, the copy option, a reminder that the folder's device
must be added first, and one guide per system (Mac, Windows, Linux or a NAS in Docker), open for
the systems of the user's devices, its commands each with a copy button. Lowering a level, or turning copies off, applies at once here (`folders_lowered`: the
folder is listed and lent at the lower level from then on, even while its machine is away) and on
the machine now or as soon as it is back (`PATCH /v1/shares/{id}` through its tunnel, which stops
the tasks holding it above); the row goes once the machine reports it lowered. An older node
without that call stays lowered by eriphe-core alone. "Ne plus partager" takes a folder off: at once
here (`folders_unshared`: it is never lent again), and on the machine now, or as soon as it is
back (`DELETE /v1/shares/{id}` through its tunnel), which stops the tasks that hold it. A folder
shared or unshared on the machine shows in its access log at the next refresh (every 15 s).

**A folder its machine cannot use** is never silent (`folder_access.go`, node/README.md, Folders it
cannot use): its node reports a `problem` for the folder (`denied`, `read_only`, `missing`; the
folder's `problem` in `GET /v1/folders`), and for a copy (its `problem` among `copies`), with the
`program` refused, `eriphe-node` or `syncthing`. Settings →
Dossiers says it on the folder's tile and on the copy's line, and the device's card marks the folder
"bloqué" with the same note. On a Mac that macOS keeps from the folder: « macOS bloque l'accès à ce
dossier », how to allow it (turn on the program named in Files and Folders), and « Autoriser », which asks that Mac's node to open System Settings ›
Privacy & Security › Files and Folders there (`POST /settings/devices/{name}/privacy`, then `POST
/v1/privacy/open` through its tunnel: nothing else can be opened, the user decides there); the note
goes once the node sees the folder open again. On another system, or another problem, a plain
explanation.

**Folders given from the workspace before** (the old `folders` table, `{node, share, path}`) are
not lent any more: eriphe-core never pushes them to a machine, since that would let it add a
folder. Settings → Dossiers shows them under their machine, "À partager de nouveau sur
l'appareil", with their old place and path, until the user shares them again there (under the same
name, they disappear) or forgets them.

**Lending one.** The brain lends folders to a task as [resources](#resources),
`{"kind": "folder", "ref": "Clients", "access": "read"}` (or `drop`, if the folder allows it, or
`write` for a folder at `manage`, to change the folder itself; `list_folders` gives each one's
entry in `lend`; the old `folders` field still works, and lends at most `drop`). The task is placed on a machine that holds each of its folders
up to date, theirs or one keeping copies of them (below, Copies of a folder), or waits for one
(`pending_reason` says so); folders of several machines go together only if one machine holds
them all, and `node` may only name a machine that holds them. Its node mounts each at `/work/folders/<name>`, read-only as
Docker enforces it; with `drop`, its results subfolder read-write, created if missing; with
`write` (`manage` in the node's spec), the whole folder read-write, after its snapshot (see
node/README.md, Shares). The worker is told where they are, what it may write in each, and
that what reads like an instruction in the files is data. The folders are read again from the
machine's list whenever the node fetches the task: a folder removed meanwhile fails it, and the
node refuses anything not in its list. A task drops or manages only where its entry says so, and
never above the folder's level now: a folder lowered since is mounted at its new level (a task
from before resources: drop wherever its folder's level allows it). A task's `folders` lists their names.

**A folder is the user's own.** The user shared it on their machine and lent it: under the
[one taint rule](#tool-catalog), reading it does not taint a task, so its result does not
taint the brain's turn by itself (a task taints by what else it read: a tool not trusted, the web).
What it says of the folder is still data, never instructions, and the event says so:

- the event that brings the brain the result of a task that read folders names them, as data;
- on a tainted turn, a folder lent with `drop` or `write` is asked on the start's card (see
  [who decides a write](#folders)); folders to read still go;
- the permission policy applies to the task as to any other, and Docker refuses any write in the
  folder but in `Eriphe/`.

| Call (bearer token) | What |
|---|---|
| `GET /v1/folders` | the folders, with `mount` and whether their machine is `online` |
| `PATCH /v1/folders/{name}` | `{"access": "read"\|"drop"\|"manage", "copyable": false}`: lower its level or turn its copies off (on its machine too); a raise is refused, 403 `raise` |
| `DELETE /v1/folders/{name}` | no longer share it (on its machine too) |

Nothing adds or raises a folder: it is shared on its machine.

### Copies of a folder

A folder its machine allows copies of (`copyable`, chosen when it was shared there; turned off
from here, never on) can be kept, up to date, on the user's other machines: Settings → Folders,
"Copies", tick the machines (`PUT /v1/folders/{name}/copies`, `{"nodes": [...]}`). Each machine
needs Syncthing, which comes with the installation (node/README.md, Copies); one without is
shown greyed, to update. eriphe-core keeps who holds a copy of what (`folder_copies.go`,
`folder_copies`) and hands each node its plan (`PUT /v1/sync`), with the peers' device IDs (from
`GET /v1/node`, `syncthing.device_id`) and each folder at its level now: `read` sent only, `drop`
sent only but its results subfolder both ways, `manage` both ways with 30 days of versions on the
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
any machine that holds each of them up to date (`folderHosts`), and the scheduler picks the best
of them as for any task (resources, preferences, load). A machine holds a folder up to date if it
is the folder's machine, or keeps a copy in sync now: the copy idle with nothing to receive, and
the folder's machine idle with nothing to receive from its copies, without error. While the
folder's machine is away, a copy that was complete when that machine was last seen (within 2
minutes: `synced_at`) will do. A task lent several folders goes to a machine that holds them all
so. Its spec lends the copy (`copy` in its folders), at the level the task asked for, never above
the folder's on its machine; with `manage`, the node running the task takes the snapshot of the
copy first. With `drop` or `manage`, it writes on the copy, and Syncthing brings that back to the
folder's machine, at once or when it returns (a conflict keeps both files). With no machine up to
date online, the task waits: for the folder's machine, or for a copy to catch up.

| Call (bearer token) | What |
|---|---|
| `PUT /v1/folders/{name}/copies` | the machines that keep a copy: `{"nodes": ["nas"]}` (`[]`: none, the copies are deleted); 409 if the folder is not copyable or a machine has no Syncthing |

### Copies: the way between machines

The copies of a shared folder on the user's other machines are kept by Syncthing, one next to each
node (node/README.md, Copies). The machines never reach one another directly: a node carries its
Syncthing's connection to a peer as `GET /v1/node/sync/{peer}` with `Upgrade: eriphe-sync` and its
secret (`sync_relay.go`), and eriphe-core goes on through the peer's tunnel (`GET /v1/sync/connect`),
then copies bytes both ways. It does so only if it set up a copy between the two machines, either
way (`folder_copies`: `{node, folder, copy}`): any other pair is refused (403), as is a suspended
machine, and a pair holds at most 4 connections at once (429). When the last copy between two
machines goes, their connections are cut. Syncthing's TLS runs end to end with pinned device IDs:
eriphe-core sees only ciphertext.

## Devices: what Eriphe sees and does there

Connecting a device gives Eriphe access to it, so it can help with what is there; letting agents
work on it is an extra (a node that reports runtimes). Settings → Appareils shows, for each device,
first what Eriphe sees there (its Folders, with their right, or "Rien d'ouvert" and a link to share
one), then what it can do there ("Faire travailler des agents", oui or non, its system, its CPUs,
memory, runtimes and labels), then its status and update, as before (`web_pages.go`,
`web_devices.go`). The addresses under `/settings/machines` still lead there.

**Access log.** eriphe-core records what it did on each device (`node_access.go`, `node_log`
table): a folder listed (`list`, with `share:/path`), a folder opened or closed (`share`,
`unshare`), a task run there (`task`, with its agent; the brain itself is not logged), a folder
lent to a task (`lend`, with its right), a file a task wrote in a folder's `Eriphe/` (`result`,
from the file tools Claude Code reports), and access cut or restored (`cut`, `restore`). The same
entry again within 10 minutes is not repeated; entries are kept 90 days, at most 2000 per device,
and go with the device when it is forgotten. A device's page (`/settings/devices/<name>`) shows
the last ones, 50 at a time.

**Cut access.** "Couper l'accès" (the card's "⋯" menu, or the device's page, after a confirmation)
suspends the device at once, without uninstalling anything: its active tasks are stopped (they
cannot report any more), no new task is placed there (by name, by the scheduler, or for its
folders), its folders are neither listed, opened nor lent, its movable services go to another
device, its tunnel is dropped, and the node is refused (403 on `connect`, and on every node call)
until "Rétablir l'accès". Its registration, secret, certificate and folders stay; it reconnects by
itself once access is restored.

| Call (bearer token) | What |
|---|---|
| `GET /v1/nodes/{name}/log?limit=N&before=ID` | `{node, suspended, log: [{id, time, kind, task, folder, access, detail}]}`, newest first (100, up to 500) |
| `POST /v1/nodes/{name}/suspend` | cut access |
| `POST /v1/nodes/{name}/resume` | restore access |

**Waking a device (Wake-on-LAN).** "Eriphe peut réveiller cet appareil" (the device's page; not on
the machine that stays on) is off by default, a choice per device kept in the `node_wake` table,
and forgotten with the device. Once on, the page shows "Réveiller maintenant" while the device is
off, the card's MAC address it reported (node/README.md, Waking the machine), and how to get it
ready: Wake-on-LAN in the BIOS/UEFI and on the network adapter (Windows: Device Manager → the
adapter → Power Management, and "Wake on Magic Packet" under Advanced; Fast Startup off to wake
from a full shutdown), a cable rather than Wi-Fi; on a Mac, "Wake for network access", from sleep
only. After "Wake now" the page asks every 5 s whether the device connected again since, and says
so, or that it did not within 2 minutes.

`wake(node)` (`wake.go`) sends the magic packets, for each MAC address, to `255.255.255.255` and to
the device's subnet broadcast, on ports 9 and 7, two ways at once: from eriphe-core's own network,
and through every online machine that stays on (`POST /v1/wake` on its node), which sends them from
the host's network. Only the second reaches the home network when eriphe-core runs in a container
on a Docker bridge network, as every installer runs it (`eriphe-system`, published ports): a
broadcast from a bridge stays on the bridge, and Linux does not forward a directed broadcast either
(`bc_forwarding` is off). The first one counts where eriphe-core is on the host's network. It is
refused for a device that is on, whose access is cut, that has not reported a MAC address (an
older eriphe-node), or that the user did not allow. Nothing crosses the Internet: the device must
be on the same network as the machine that stays on. On a Windows or Mac home, where the machine
that stays on runs its node in WSL behind NAT, or as a macOS LaunchAgent held off the LAN, the
packets may not leave it (not checked).

The scheduler wakes a device by itself: when a task waits for a node (`placePending`) and the
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

An unknown task answers 404; a call the task's state does not allow (or that its node refuses)
answers 409 with the reason.

## Workspace (`web`)

Eriphe's own interface, served by eriphe-core on its own HTTPS listener: the conversation with
Eriphe in the middle, its tasks on the side as live cards (machine, agent, repository and branch,
state, the pull request or document the agent mentioned), the permissions waiting for an answer
inline in the conversation (Yes / No, with an optional reason for the agent), and the settings. The assistant manages: what the brain can set itself
(a rule, a watch, a memory, an intention) is asked of it in the conversation, and its pages show
the state, say why, and undo, with a line saying how to ask rather than a form.
The conversation is the one main screen; the header has two buttons, Tâches (a badge counts the
running tasks, and turns urgent when one waits for the user) and Réglages. On a wide screen Tâches
hides or shows the side panel; on a phone it opens the tasks as a sheet over the conversation
(closed by its button, Escape or the browser's back), and from any other page it comes back to
the conversation on its tasks (`/#tasks-panel`).

**Conversation first** (`convo_cards.go`, `web_convo_cards.go`, `templates/convo.html`). What waits
for the user's approval is asked where they talk to the assistant: every proposal (a memory, an
intention, a notification rule, a watch, a setting, a presence mapping, the start of a task that
needs something of theirs: [asked at launch](#github-in-a-task)) is a card bound by the code
to that one proposal, under the brain's message of the turn that made it (or its last message of
the turn), with Oui, Non (the proposal's own approve and reject) and Modifier (the composer is
prefilled, and the brain proposes again). The event stream keeps cards up to date (`cards`:
approved, declined, no longer current). Other proposals (the memory's extraction, a turn that
said nothing) are queued and offered three at most, once an hour at most, when the user writes or
at the brief and the return point, never in the quiet hours, at night (22:00–07:00), when a
source says do not disturb, in a Focus or while busy; the brain says it in a sentence and the
cards come with its message. A card that stops a task is not one of these: if its turn wrote
nothing (a turn that failed), it goes with the brain's next message, whatever the hour. A plain yes or no answers a card in words only when the message is
the user's own (workspace, Telegram or a trusted channel; never a channel the user did not trust, nor `/v1/messages`, whose token may
be lent to the brain), is nothing but a yes or a no, has no file, comes right
after a recent message of the brain's written in a clean turn that shows exactly one waiting card,
whose proposal is not drawn from external content; anything else is the brain's to read, or the
buttons'. The brain has no tool that decides a card. On Telegram, a card follows the reply or the
notification it goes with, with Oui / Non buttons answered from the linked account only (none in
"alerts only"); a channel the user did not trust never answers a card: it says the card waits on the phone, where a notice goes. A read of `since` adds a
foldable digest card (the items, with links to the tasks) to the brain's next message, and a
spontaneous message (a notification) has a "Pourquoi ?" with its why and subject.

The **Journal** (`/settings/journal`, `web_activity.go`) is the black box: the feed the brain reads
with `since` (below), by kind (tasks, notifications, watches with their silences, intentions,
messages) and period; its "tool accesses" kind is the journal of decisions, paged. **À valider**
(`/settings/approve`) is the fallback where whatever still waits is listed: the permissions of
tasks, memories and intentions, notification rules, watches, settings, presence, and the quiet
hours the activity suggests (to adopt in one tap). Each is approved or rejected with its own
action, its card says so, and the list reloads itself. Both are entries of the settings (À
valider with a discreet count), not of the main navigation.

The settings come in four groups: Assistant (the memory, "Ce que Jarvis sait de toi", still at
`/memory`, the watches, the agents, the skills), Connexions (tools, folders), Appareils & accès
(devices, access, notifications) and Mon espace (account, the Journal, À valider); an entry is marked
when it needs a look (an update, a tool in error, a refused key). On a phone the settings say the
way back, "‹ Conversation". The pages that moved keep their addresses as redirects:
`/settings/happened` and `/activity` lead to the Journal, `/activity/approve` and
`/memory?view=inbox` to À valider, `/settings/where` to its section of Notifications, and
`/settings/remote` and `/settings/channels` to Access, whose sections (`#web`, `#app`, `#telegram`,
and `/settings/notifications#push` and `#telegram`) lead, in the browser, to each access's page.
**Access** (`/settings/access`, `web_access_list.go`, `access.html`) lists every way into the
assistant, one row each as the tools are listed: the web workspace and the mobile app always, then
Telegram once its bot is set, then the API accesses. Each row says who speaks through it, what it
carries and its state (the remote access's phase, the devices told, the account linked, whether
the app listens), and opens its page: the web (`/settings/access/web`: the remote access, below, then the workspace's address and how one
signs in, which were in Account),
the mobile app (`/settings/access/app`: installing the workspace, this device's notifications and
the devices told), Telegram (`/settings/access/telegram`) or an API access
(`/settings/access/api/{id}`: its trust, what it may say, its token replaced, removed). **Ajouter
un accès** opens the catalog (`entryCatalog`; `/settings/access/new` without JavaScript): Telegram,
Home Assistant (not trusted, says a sentence, with the integration's steps) or any app or script.
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
side by side: [Eriphe's GitHub App](#the-github-app) (recommended: the user picks the repositories,
each task gets a one-hour token for its repository only, commits and PRs appear as the app,
about five minutes to set up once; it leads to Settings → Tools → GitHub) or a personal token
(quick, acts as the user on everything the token reaches, long-lived; git works with it, gh does
not, so no pull request from a task; its form says so and links back to the app). A GitHub connection on a token shows "Passer à l'app" in the list and
in its form. In the list (`web_tools_groups.go`), each account is a card, as a phone's Settings →
Accounts: the provider's mark, the address, "Google · Mail, Agenda, Drive", then a row for each
of its services (Mail, Agenda, Drive) with its state, access, reconnect, edit and remove. A Google
account's card has its actions: "Gérer" (Settings → Tools → Google), reconnect, and the Google
services it does not have yet ("Ajouter à ce compte"). Drive is a row under each Google account,
with that account's folders. A Gmail on a password goes in the card of its Google address; an
Apple account's mail and calendar are one card, an Apple calendar and a Google calendar of one
address two. Only the list is by account: the servers stay one per service. The other tools of
one kind (GitHub, Notion…) make one card when there are several: the kind, how many accounts and
tools, "Ajouter un compte", then each account with its tools as rows. A kind with one tool stays a
plain row. Mails and Agenda first ask which kind of account
(Gmail, iCloud, Orange, Free, OVHcloud, Fastmail or any IMAP server; iCloud, Fastmail, Nextcloud or
any CalDAV server) and run `eriphe-mail` and `eriphe-calendar` with its password; "Test the
connection" tries that password (IMAP login, CalDAV PROPFIND). "Gmail avec ton compte Google"
(first, recommended) and Google Agenda lead to Settings → Tools → Google, one sign-in for Gmail,
Calendar and Drive ([Google accounts](#google-accounts-gmail-calendar-drive)); the Google tile of
"Add a tool" too. Outlook, Microsoft 365 and Microsoft calendars are shown as coming soon. A vendor's OAuth service (Linear, Notion and the catalog's) opens a short page (name, use, access), then "Se
connecter à Linear" saves and signs in at once; the tool is added only once authorized. Settings →
Tools also edits and removes the workspace's own tools, and connects again those that use OAuth
(see [OAuth](#oauth-and-eriphe-connect)). A collapsed "Secrets avancés"
section lists what the vault holds and what each secret belongs to, with replace and delete only,
and the configuration's secret files to import. Settings → Agents shows the agents (model, account,
whether a machine can run them now), the agents worth creating with the accounts (one click), the
default agent and the brain's, and the AI accounts (providers). Adding an account starts from a
card per kind, subscriptions first (Claude, ChatGPT), then API keys, then a self-hosted model
server (on a device or at an address, an optional key, and its models, detected and ticked), each with who pays, its
limits, its models and the steps to get its credential; "Test the key" lists the provider's models
once with the key typed (OpenRouter also tells the credit left; a Claude subscription token is
only checked for its shape, as Anthropic allows it in Claude Code only; a key pasted in the wrong
card is caught), and the usual agents of the kind are created with it (Opus, Sonnet, Haiku for
Claude; GPT models for ChatGPT; two well-known models for each API key). A key is tried again when
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
The bearer-token API is unchanged for the CLI and the nodes.

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
composer stays above the keyboard (Chrome resizes the page for it, `interactive-widget`; on iOS the
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
| a worker task finished its turn, or failed (or was lost) | « Tâche finie : <title> », « La tâche « <title> » a échoué » | the conversation with that task's drawer (`/?task=<id>`) |
| a permission waits for the user (one that is theirs, or any while the assistant is away), or the assistant ended a message on a question | « <Name> attend ta réponse : <tool and command, or the question> » | the conversation, at that permission or message |
| the brain tells something on its own (`notify`), or answers a due intention, and the decision below says now | its text, with 👍 / 👎 / « pas maintenant » buttons where the platform shows them | that notification in Settings → Notifications, with why |

Each is pushed once, and not at all while a workspace tab is visible and focused: every open tab
tells eriphe-core when it comes in front or leaves (`POST /presence`, and every 20 s while in
front; a tab silent for 45 s no longer counts). Everything goes through one function (`tell`),
for what else will notify later. A push service that answers 404 or 410 has the subscription
removed. The service worker shows the notification and, on a tap, focuses an open workspace window
and takes it there, or opens one, on the workspace's own origin. Push needs a trusted HTTPS origin
(see the certificate above); on iPhone and iPad it reaches the installed app only (iOS 16.4 or
later), while Android and computers receive it in the browser too.

**The brain's notifications** (`notify.go`, `notify_rules.go`, `notify_quiet.go`). The brain has
four tools: `notify(text, level now|brief, why, ref)`, `notifications` (what it said, what waits
for the brief, the feedback, the rules, the quiet hours, the known topics, the settings), `rule` and `setting`. The model writes and asks for
a level; the code decides (`decideNotify`, no model):

- **Quiet hours always hold.** The window is a rule (data): for every night, or for the nights
  before a weekday or a weekend day, 4 to 16 hours long; without one, the seed 22:00–08:00. A
  "now" inside it waits for the brief, unless a topic rule of the user's says "even at night", or it
  is the fixed message of a watch the user marked urgent (below).
  The time zone is the configuration's, or the one set in Settings.
- **A daily cap** of "now" (4 by default, 0 to 20 in Settings), besides reminders the user asked
  for in their own words; over it, the brief.
- **Topic rules** map a `ref` (`project:getout`, which also matches `project:getout:prod`) to now,
  brief or never. A turn that read external content may lower an outcome, never raise it: what it
  notifies never interrupts, goes to the brief marked as such, and may hold no link.
- **The answer to a due intention** is pushed through the same decision, question or not, once per
  wake; a reminder from an intention drawn from external content waits for the brief.
- Every notification is kept (`notifications`) with its why, outcome and reason. What waits for the
  brief is listed for the brain and in Settings (the brief itself comes with routines). Feedback
  (👍, 👎, « pas maintenant », which moves it back to the brief) is given in Settings or on the push
  itself (`POST /notify/react`, authorised by the notification's own token, which only its push
  carries), and returned to the brain per ref.
- **Rules are written by the brain only when the user asked in that clean turn** (its `source_ref`
  cites the user's message of the turn, which speaks of being told); otherwise, and always on a
  tainted turn, they are proposed and the user approves them in À valider (a card in the conversation); Settings →
  Notifications shows every rule in force, to delete (a new or changed one is asked of the brain).
  Since #244 step 5 the topic rules and the quiet hours are rules too: rows of `rules` with source
  `notification` (their kind, match, outcome, nights, lines and hours in the JSON `spec`; `act`
  `deliver` or `quiet`), read by `notifyRules` only, never fired by the rule tick. The table of
  before (`notify_rules`) is moved into `rules` at start (`storeMigrations`), with new ids, and the
  cards on them follow.
- **Adaptive quiet hours, never applied alone.** eriphe-core records cheap signals of activity (the
  user's messages, a workspace tab in front, in 10-minute buckets kept 30 days, reactions to
  notifications) and of unwelcome moments (« pas maintenant »), and suggests a window per kind of
  night from the last 14 days: the median last activity of the evening plus a margin, the median
  first of the morning, widened over repeated « pas maintenant » just outside it; too few days, no
  suggestion. The brain sees it in `notifications` and proposes a quiet rule; nothing changes until
  the user approves. Further signals (a Home Assistant sleep sensor, the phone's focus mode) join
  `activitySources`. The same signals give the user's last activity (`lastSeen`, `awaySince`), and
  `notificationsSince` what was held since then, each with its source, time, why, ref, outcome and
  feedback: what a brief or a point made on the user's return summarises.

- **One topic vocabulary** (`topics.go`). A rule matches the `ref` the brain gives `notify`, so
  labels must not drift: `canonRef` is the one form of a ref (lower case, accents folded, spaces as
  hyphens, `projet:` → `project:`, a one-word topic in the singular: `factures` → `facture`), on both
  sides of the match, so a rule written before stays in force. The known topics (rules, notifications
  of the last 90 days, the memory's `project:`, `person:` and `client:` subjects) are listed in
  `notifications`; a new label of a known word family is kept under the known topic, and a new one
  is flagged to the brain with the known ones.
- **Settings through the brain** (`settings_brain.go`, tool `setting`): the device order, the lines
  off, the delay before the next line, the daily cap, the time zone and the Telegram detail. Applied
  only when the user asked in that clean turn; otherwise kept in `setting_proposals` and approved in
  À valider, then written to the same keys Settings uses (one place per setting).

**The brain's concepts** (`concepts.go`). One concept per need: memory (what the brain must know),
intention (owed or to do once), rule (what comes back or reacts on its own: when, if, then; and
how to be told about a topic, quiet hours), setting (how to be told in general), notify, since,
fact, task, session. What the code enforces is
never a memory: `recall` returns the matching rules typed as rules, and the tools refuse the
wrong concept with a hint naming the right one (`remember` of a notification preference → `rule`
or `setting`; an event rule at a one-off time → `intend`; a recurring `intend` → `rule`). The concept
bench (`concepts_test.go`) checks, without a model, that each request's right call is taken and its
wrong one redirected, and that the map is in the MCP instructions and the prompt.

The interruption bench (`notify_eval_test.go`, `testdata/notifyeval`) runs with the tests: it scores
the decision on labelled cases (accuracy, precision and recall of "now", night wake-ups no rule
allows: none, injection cases blocked: all).

**Channels** (`channels.go`): how the user and Eriphe talk, one notion apart from the tools, which
act. Each channel has its own connection and secret. The built-in ones, in `channelKinds`:

| Channel | Direction | Who speaks (inbound) | What it carries (outbound) |
|---|---|---|---|
| `workspace` (and the CLI) | receives | the user: their session or token | |
| `push` | notifies | | the notification's text |
| `telegram` | both | the user's account, verified with a code | the notification's text |

The user adds others, trusted or not, saying a sentence in the user's room or nothing (above,
"Channels the user adds"). A notification or a rule may name only the channels it is broadcast on
(`push`, `telegram`).

**Push and Telegram** (`channels.go`, `telegram.go`, `web_telegram.go`). What is sent
(`tell`) goes by the user's channels: web push and Telegram, all by default, a narrower default in
Settings → Notifications (Où te prévenir), or a topic rule's own (`channels`: `push`, `telegram` or both). The decision
above is unchanged; the channels only say where a "now" goes. Telegram, two-way, without an
inbound port:

- **Setup.** The user creates a bot with BotFather and pastes its token in Settings → Access (Telegram):
  checked with `getMe`, kept in the vault (`telegram-bot`), never shown, logged or put in an error
  (the Bot API puts it in the URL: errors are stripped of both). Eriphe then shows a one-time code
  (10 minutes, 10 wrong tries); the private-chat account that sends it to the bot (or opens
  `t.me/<bot>?start=<code>`) is recorded, user id and chat id. Only that user id is heard; any
  other sender, group or bot is ignored, counted in Settings and never answered. Unlink, change or
  remove the token (a new bot asks for a new link).
- **Polling.** `getUpdates` long polling (50 s), with a wait from 1 s doubling to 5 min after a
  failure (or Telegram's `retry_after`); it runs only while a token is set, and stops on removal.
- **Out.** A notification carries inline buttons 👍 / 👎 / « pas maintenant » (`fb:<id>:<r>`,
  accepted from the linked account only, recorded like the push's); questions and permissions
  waiting for the user are told with a link to the workspace. The brain's messages that follow a
  message the user wrote on Telegram, until their next message from elsewhere, are sent back there
  (`telegramReplies`). "Alerts only" sends "Eriphe a quelque chose pour toi" and a link instead of
  any text, replies included. Telegram bots are not end-to-end encrypted, and Settings says so.
- **In.** A text from the linked account joins the conversation as the user's message, `via`
  `Telegram` (`[Sam 09:12 #41 · Telegram]` for the brain): a clean turn, because the sender's id
  is checked. Files, photos and voice notes are not read; the bot says so.
- **Proposals as cards, permissions in the workspace.** A proposal of the brain's (a rule, a memory,
  an intention, a watch, a setting, a presence mapping) that goes with a reply or a notification
  sent there comes as a card with Oui / Non buttons (`pc:<card>:<y|n>`), answered from the linked
  account only, through the same code as the workspace's cards (`telegram_cards.go`); none in
  "alerts only". The [access card](#the-approval-card) of a catalog tool goes there too. A task's
  raw permission (a command, a JSON input) is never approved from Telegram: one that is the user's
  (user_only) is told with what waits and a link.

**Where to reach the user** (`route.go`, `route_signals.go`, `route_send.go`, `web_where.go`).
A pure function, `route(note, signals, settings, tried)`, picks one device at a time from signals
eriphe-core already has, never from the model nor the notification's text:

1. Eriphe open and in front on a device (its tab tells its push subscription with `POST
   /presence`) → a banner in that page (the event stream's `notice`, or the presence answer), no
   system push;
2. a computer in use (its node reports the machine's idle time under 2 minutes, not locked, not
   to be disturbed: a game or a meeting in front, the context below) → a push to that computer's
   subscription only;
3. the voice in the room: a channel the user added that says a sentence, listening and reaching
   the room a fresh (under 2 minutes), personal signal of Home Assistant puts the user in (below),
   for a "now" from a clean turn,
   outside the quiet hours, on a line the user turned on (off by default); it says a sentence the
   code writes (« Sam, Eriphe a quelque chose pour toi. »), never the notification's text;
4. otherwise the phone (subscriptions whose label, the user's choice or the browser says phone;
   the installed app first), then Telegram.

A tab left in front on a machine its node says is idle, locked or not to be disturbed is not the
user. A "now" not seen
(opened, answered, or shown in a page) within the delay (10 minutes, Settings; 3 for an
"even at night" topic) is routed again without the devices tried, two steps at most, never in the
quiet hours unless the topic's rule says even at night. A brief, a task done or a question is sent
to one device and not moved on; a test goes to its device. A topic rule keeps channels
(`channels`), adds lines to the first step (`also`: "Getout: the phone too") or forbids some
(`never`); a note from a tainted turn never gets a line added nor moves on. Settings →
Notifications (Où te prévenir) orders the lines (each shown once what it needs is connected, one switch each), sets the
delay, says which subscription is a phone or a computer and which node is on which computer
(suggested by system), and lists the last decisions (`route_log`, planned next to sent). It starts
in **observe** mode: everything goes as before (every channel, nothing while a tab is in front)
and the log shows what the router would have done; one switch turns routing on. What a node tells
of its machine is kept as context signals, in memory for two minutes, never stored. The `notifications` tool gives the brain the order
and, by device, what was seen fast, late or moved on, from which it proposes rules. The bench
(`route_eval_test.go`, `testdata/routeeval`) checks every case, no duplicate, every injection
blocked.

**The user's state and what happened** (`user_state.go`, `since.go`, `since_tool.go`,
`web_since.go`). Each of the brain's turns opens with the time (`[maintenant …]`) and, under it, the
user's state, built by code: `[état de Sam : présent, de retour à 14:50 après 4 h 30 d'absence
(parti à 10:20) · heures calmes à 22:00 (22:00–08:00) · notifications « maintenant » restantes
aujourd'hui : 3 sur 4]`. Present or away comes from the activity signals above: no activity for an
hour is away, and a gap of an hour or more is an absence. "Busy until" comes from `busySources`,
none yet: reading a connected calendar on every turn would cost a call each time and taint every
turn; a source that keeps free/busy times only (never titles) joins there.

**What Eriphe knows about you: the context** (`context.go`, `context_sources.go`,
`web_context.go`, Settings → Ce qu'Eriphe sait de toi). One concept for where the user is and
what they do, turned on by the user: a **level** (`context_level`: 0 Rien, 1 Présence, 2
Contexte; unset is 1, what a Mac's node already told) that is only a preset, and a **switch per
source** (`context_off`). Sources put **signals**: a kind and a value from a closed list, with
the source, when it came and when it expires. A value off its list is refused, so no free text
from a source ever reaches the model.

| Kind | Level | About | Values | Time to live (default, max) |
|---|---|---|---|---|
| `presence` | 1 | a device | `active`, `idle`, `locked` | 2 min, 10 min |
| `dnd` | 1 | a device | `on` (with a `reason`: `game`, `meeting`, `fullscreen`, `mic`, `focus`), `off` | 2 min, 12 h |
| `activity` | 2 | a device | `game`, `meeting`, `code`, `web`, `video`, `music`, `chat`, `other` | 2 min, 12 h |
| `place` | 1 | the user | `home`, `away` | 12 h, 24 h |
| `room` | 1 | the user | one of the user's rooms, listed on the page (`context_rooms`) | 15 min, 1 h |
| `home_activity` | 2 | the house | `tv`, `sleep`, `office`, `cooking`, `other` | 15 min, 12 h |
| `focus` | 1 | the user, declared | `on`, `sleep`, `off` | 4 h, 24 h |
| `busy` | 1 | the user, declared | `busy`, `free` (until it expires) | 1 h, 12 h |

Signals are **ephemeral facts** owned by `context` (see Facts), written by code: they live **in
memory only** (`contextStore`, over `factMemory`), never in the database and with no history; an
expired one is unknown. Level 0 still keeps the activity record above (30 days), and the page says
so. The sources:

- **`machine:<node>`**: with its status (`GET /v1/node?context=N`, N the level for that node, 0
  when it is off), a node tells `idle` (seconds, locked) and `context` (`{known, dnd, reason,
  activity}`, worked out on the machine: `node/context.go`). They become `presence`, `dnd` and
  `activity` signals for two minutes; neither is stored with the node's info.
- **`src:<id>`**: a source made on the page, with its own token (`ecx_…`, shown once, only its
  hash kept, revoked on the page), for an iPhone shortcut, a Home Assistant automation or a script:

  ```
  POST /v1/context
  Authorization: Bearer ecx_…
  {"kind": "place", "value": "home"}
  {"signals": [{"kind": "focus", "value": "on", "ttl_minutes": 60}, {"kind": "room", "value": "Salon"}]}
  ```

  At most 8 signals, 4 KiB, no unknown field; one bad value refuses the whole request (400, with the
  allowed values). A signal the level or the source's switch keeps out is counted, not an error:
  `{"accepted": 1, "ignored": 1}`. The token can do nothing else; plain HTTP from the internet is
  refused. A device signal from such a source is about that source (the page and the header name
  it), not tied to a push subscription.

**Only quieter.** The router reads `presence` and `dnd` of the node tied to a subscription: a
device locked, idle or not to be disturbed is skipped by every line (in the app, the computer,
the last-resort computer), as if it were not there (the bench checks exactly that). A signal never
adds a line, never moves a notification on, never pierces the quiet hours, never wakes the brain
nor reaches the conversation, never authorises anything. The brain's header gets one line, written
by the code from the closed values, under the user's state, only when something is known and the
level is above 0: `[contexte : studio : actif · jeu · ne pas déranger (jeu) · maison : à la maison,
Salon · Focus active]`; a declared `busy` feeds the state's « occupé jusqu'à ». The brain changes
the level and the switches with `setting` (`context_level`, `context_sources_off`), applied when
asked in a clean turn, else proposed in À valider; the first-launch interview asks the level.

**Home Assistant, adapted to each house** (`ha_client.go`, `ha_presence.go`, `ha_tools.go`;
source `ha`; its follower writes the context's facts like any source). Why not Home Assistant's own
MCP server (the catalog's tool): its `GetLiveContext` returns only the entities exposed to Assist,
by name and area with no `entity_id`, and has no subscription, so it can neither tell a mapped
entity apart nor follow it without polling; the WebSocket follower stays. With the long-lived token of the catalog's Home Assistant tool (the `home-assistant`
preset; nothing to install), over Home Assistant's WebSocket API:

1. **Discover** (code, no model): `get_states` and the area, device and entity registries (a user
   that is not an administrator may be refused the registries: then states only, no areas), kept
   in memory, filtered to the candidates: `person`, `device_tracker`, `binary_sensor` of class
   `occupancy`, `presence` or `motion`, room tracker sensors (`*_area`, `*_room`: Bermuda,
   ESPresense), `media_player`, `assist_satellite`, the companion app's focus, sleep or do not
   disturb `input_boolean`s, and the `tts` entities. Each with its id, domain, class, area and
   state; never an attribute.
2. **Map** (the brain proposes, the user approves): `presence_sources` shows the candidates, which
   signal each could give, and the mappings; names and areas are the user's text, so it taints the
   turn. `map_presence(signal, entity, why, source_ref)` checks the entity in code against
   discovery: `home` (person, device_tracker → `place`), `room` (an occupancy sensor's area, or a
   room tracker's state → `room`), `asleep` (a bed's occupancy or a sleep switch →
   `home_activity` `sleep`), `tv` (a media player playing → `home_activity` `tv`), `dnd` (focus →
   `focus`). Applied when the
   user asks in a clean turn, citing their message; otherwise kept as a proposal (`ha_mappings`,
   state `proposed`) in À valider. The page lists the mappings in force and removes them; a room
   mapping adds the area (or, for a room tracker, the house's areas) to the user's rooms. A
   mapping is personal (about the user) for a person, a tracker, a room tracker or the focus, and
   the house's otherwise.
3. **Follow** (code only, `followHA`): while there are mappings, the level is above 0 and the
   source is on, one `subscribe_entities` to the mapped entities only; their states become
   closed signals (`haSignals`: per kind, a personal mapping first, then the newest change;
   a room off the user's list is dropped), put under `ha` with a time to live of 3 minutes and
   refreshed every minute while connected. Reconnects with a growing wait (5 s to 5 min), starts
   again when the mappings, the level or the switch change, and reads the house again every day.
   Unmapped entities, names and attributes never enter the context.
4. **Speak**: the room opens the router's voice line (above), said by a channel the user added that
   reaches it (`GET /v1/channel/out`, above), never with the tool's token. An urgent watch's fixed message is said instead of the generic
   sentence, and may go through the quiet hours, only when the user turned `voice_for_urgent` on
   (the page, or `setting`). A rule's `never: voice` keeps it out; `also: voice` does not turn the
   line on.

**For the next sources.** A source puts signals with `checkSignal` (or `checkSignals`) then
`putSignals`, under a source name of its own; it never calls the brain. A new kind or value goes in `ctxKinds` (and in the page's words), with its level.
A machine whose node cannot see the session (Windows, in WSL) gets it from a session helper's file
(`ERIPHE_SESSION_FILE`, `node/context.go`).

The bench (`context_test.go`): every value off a list refused, every kind expiring, the levels and
switches, a busy device as if absent (every combination), the router skipping a device in a game,
the header's line, the entry point's tokens.

- **`since(t, kinds?, limit?, include_external?)`** returns one chronological feed since `t` (an
  RFC3339 time, `YYYY-MM-DD[THH:MM]` in the user's zone, `away`: since the user left before their
  return, or `last_brief`, from the kv `last_brief` the brief will write): tasks (started; turns
  finished with their receipt's verdict in numbers, waiting, failed, stopped, from the append-only
  `task_log` written where the brain is told), notifications (sent, held, dropped, feedback),
  intentions (due, done, snoozed, cancelled, from `intention_log`), the user's and the brain's
  messages (short; a brain message sent as a notification is that notification), and the watches'
  runs (fired, and what the brain did of them). Each item has a time, kind, event, summary written by code, a ref
  for drill-down (`task:`, `notification:`, `intention:`, `msg:`) and a trust, `own` or
  `external` (a notification or brain message written in a tainted turn, an intention drawn from
  external content, a task started on a tainted turn).
- **Taint.** By default `since` quotes only own items and names external ones without their text,
  so reading it does not taint the turn; `include_external` quotes them too and taints the turn
  (`since.external`), as reading an external memory does.
- The **Journal** (`/settings/journal`) shows the same feed to the
  user, by kind, since they were last here (the start of their last absence; the last 24 hours if
  none), or the last 24 hours or 7 days, external items marked.

**Rules** (`rule.go`, `rule_engine.go`, `rule_schedule.go`, `rule_tick.go`, `rule_poll.go`,
`rule_alert.go`, `rule_urgent.go`, `watch_grant.go`, `judge.go`, `rule_tools.go`, `web_rules.go`).
What Eriphe does on its own, besides a due intention: **when** an event comes, **if** a condition
on closed facts holds, **then** one action from a closed list, all run by code, no model. The
brain writes one with `rule` (`kind: event`, the rule in `event`: `name`, `when`, what the source
needs, `if`, `then`, `prompt`), lists them with `rules` (each with its last runs) and changes them
with `update_rule` (pause, resume, edit, delete); the same `rule` writes the notification rules and
the quiet hours (below). The brief, the point made on the user's return and the weekly review are
not features of their own: they are rules whose prompt composes `since`, `notifications` and
`notify`, which the brain suggests and never imposes.

Before #244 step 4 these were **watches** (tables `watches`, `watch_runs`; tools `watch`,
`watches`, `update_watch`; Settings → Veilles). At start the tables are renamed `rules` and
`rule_runs` (`watch_id` → `rule_id`): every watch is a rule with the same id, history, webhook
token and secret, and no condition, whose action wakes the brain. `/settings/watches` redirects to
`/settings/rules`; the webhook address `/hooks/watch/<token>` is unchanged.

- **When** (`when`): `horaire`, `retour`, `outil`, `alerte` (below), and:
  - `fait`: a closed fact (`fact`, owner.field: `context.place`, `prod.etat`; see Facts) changes,
    or becomes `becomes`. Looked at every 30 s; the first look only notes the value; a change to
    or from unknown counts. Its detail is written by code from closed values, so it taints nothing.
    « Quand je rentre » is `fait` on `context.place` becoming `home`.
  - `fin`: a worker task ends (stopped, failed, lost) or a work session closes (`ends`: `task`,
    `session`, or both). Its detail names the task or session by id, never by its title.
- **If** (`if`, at most 300 characters and 8 terms): `path op value` joined by `and` (`et`), those
  groups by `or` (`ou`); `op` is `=`, `!=`, `<`, `>`, `<=`, `>=`, or `in (a, b)`. A path is a
  **closed** fact (a choice, a number, a date, rows by their count; never a text, so what a
  tainted writer wrote cannot steer a rule) or the time: `now.day` (`mon` … `sun`, `weekday`,
  `weekend`) and `now.time` (`HH:MM`, the user's zone). Paths and choice values are checked when
  the rule is written. Numbers compare as numbers, the rest as words (dates and times compare
  right). A term on an unknown fact is false. Tested when the event comes: false, the run is
  recorded `skipped` and nothing is done (an urgent message neither).
  `context.place = home and now.time >= 18:00` · `now.day in (weekday)` · `prod.incidents > 2`.
- **Then** (`then`): `brain` (the default: wake the brain with `prompt`, below);
  `assistant:<name>` (wake that assistant at its next idle with `prompt` and the event's detail;
  external content is fenced and taints its session); `notify` (eriphe-core sends `text`, the
  user's own words, under `topic`, `now` or `brief`, through the usual rules, quiet hours and cap;
  the run is `notified`).
- **Settings → Règles** (`/settings/rules`) shows each rule as one sentence, « Quand …, si …,
  alors … », with its state and last runs.

- **Sources.** `horaire`: days and times of day in the user's zone (`daily 07:00`, `weekdays
  07:00`, `weekend 09:30`, `sunday 18:00`, `monday,thursday 09:00`, `every 2h 08:00-20:00`; the
  French words too). Each fire is a wall-clock time of its day, computed day by day, so a daylight
  saving change neither skips a day nor fires twice. `retour`: the user comes back after an absence
  of `after` or more (3 h by default, 1 h to 7 days), from the activity signals of `user_state.go`;
  once per return (the return it fired for is kept), only within the hour after it, never for a
  return from before the watch was made or resumed.
- **Firing, without a model.** A timer (every 30 s) fires what is due and records a run; the next
  time is kept in the database, so a restart picks it up. Missed times fire once late, never once
  each; more than 6 hours late, the run is recorded as missed and nothing wakes. One run waits for
  the brain at a time per watch; a paused or deleted watch's waiting run is dropped.
- **The wake.** At the brain's next idle, after any due intention, the clock trigger delivers
  `[règle #N « nom » HH:MM · source …]`, the cause (late, the return and the absence), for a brief
  where the last brief was, and the watch's prompt; the brain answers through `notify` or with
  `[rien]`. It goes on with the current turn, as an intention's wake does; a watch that was
  proposed on a tainted turn (then approved) taints it. When its turn ends (or before the next
  delivery) the run records what the brain did: `notified`, `said` (in the conversation only) or
  `silent`. A brief's wake becomes the kv `last_brief` once its turn ended, for
  `since("last_brief")`.
- **Who writes them.** The brain writes a watch only when the user asked for it in that clean turn
  (its `source_ref` cites the user's message of the turn); otherwise, and always on a tainted turn,
  it is proposed and the user approves it in **À valider (a card in the conversation)**. Settings → Règles lists the routines
  (horaire, retour) apart from the watches (outil, alerte), each with its last runs, to pause,
  resume or delete; there is no form: the user asks the brain, and the page says how.
  Changing or deleting one takes the user's word in a clean turn. Names and prompts go through the
  memory's scanner.
- **`outil`: polling a tool, without a model.** eriphe-core calls a catalog tool itself (the pool's
  session, outside any task) with fixed arguments every N minutes (1 at least, 5 by default). Only
  a free read of the brain's: read-only by its own annotation on a server believed for it, lent to
  the brain, named by no rule of the policy; checked when written and before every call (a change of
  access makes the watch wait, its reason shown). The condition is deterministic: `changed` (the
  value at a dotted path, or the whole result; the first look is the baseline), `compare` (`eq`,
  `ne`, `gt`, `ge`, `lt`, `le` as numbers when both are, `contains`) or `status` (the call fails or
  the tool answers an error). It wakes only on a transition, and on the recovery, each confirmed by
  a second call 30 s later (a flap wakes nobody).
- **`alerte`: a webhook per watch.** `POST https://<workspace>/hooks/watch/<token>` on the
  workspace's HTTPS server, without a session: the token (32 random bytes) is made in Settings and
  shown once, only its hash kept, never seen by the model; a new one replaces the old. A signed
  watch also gets a secret (in the vault, `watch-hook-<id>`) and checks the body's HMAC-SHA256 in
  `X-Hub-Signature-256` (GitHub: a webhook with this address, `application/json`, the secret),
  `X-Grafana-Alerting-Signature` (Grafana: a Webhook contact point with the secret as HMAC signature)
  or `X-Eriphe-Signature` (`sha256=<hex>`). Body 64 KiB at most (413), kept cut to 4,000
  characters without invisible characters; 60 calls an hour at most (429); a paused watch answers
  409.
- **External content.** What `outil` and `alerte` bring is kept with the run as external: the wake
  quotes it as data and taints the turn (`watch.outil`, `watch.alerte`), so what it notifies goes to
  the brief at most (the bench has these cases), it may not lend a task
  anything that writes; `since` names it without quoting it.
- **History and silences.** Each run keeps what the brain did (notified, said, silent, acted, or
  missed, dropped), in Settings and in `since`; the next brief lists the watches that stayed silent
  since the last one.
- **A standing approval** (`standing_approval.go`, table `standing_approvals`; « Accord
  permanent »). A rule may carry one exact action: a catalog
  tool with exact arguments, or a `run_task` with a fixed prompt, agent, resources and contract;
  `max_uses` 1 by default (10 at most), an expiry (7 days by default, 90 at most). The brain proposes
  it only on a turn that read nothing external, citing the user's message of that turn, which must
  ask for it; never from a tainted turn, so an injected payload can neither make nor widen one. It
  is then a card (kind `approval`) naming the exact call, its uses and expiry, and, when its rule
  fires on facts (`fait`, or an `if` on facts other than `now.*`), that facts can be wrong; it holds
  only once the user says yes there (state `proposed`, then `active`; a no revokes it). Editing the
  rule revokes it, and the user revokes it in Settings → Règles, which also shows a proposed one.
  The watches' grants of before (`watch_grants`) are renamed at start and stay active: the user had
  said yes to them. On that watch's own wake turn, tainted or
  not, the call whose canonical input hashes the same is let through by the policy (a ticket, a
  decision on record, `byPolicy`), counted atomically; any other tool or argument still goes to the
  user. A built-in or configured denial is never granted. Each use is logged (`STANDING APPROVAL USED`),
  recorded with the run (`acted`), written in the conversation and notified to the user.
- **Urgent watches** (`watch_urgent.go`). A tainted wake never interrupts, so an `outil` or `alerte`
  watch could never wake the user at night; an urgent one does, bound to that watch, its condition
  and its text. It carries a short fixed message the user validated (« Alerte boutique : la prod ne
  répond plus »), set only on a clean turn citing the user's message of it that asks to be woken or
  reached at any hour (`watch` `urgency: urgent` with `urgent_message`, or `update_watch`
  `urgent`); a proposal or a tainted turn can neither set nor raise it, and the text holds no link
  and passes the scanner. An `outil` watch must use `compare` or `status` (a condition that
  recovers). When the condition becomes true (the deterministic test, confirmed; or a call to the
  authenticated webhook), eriphe-core itself sends that message, never anything the tool or sender
  said, as a "now" through the router (`reason: watch`, the urgent delay), over the quiet hours and
  the daily cap, before any judge or the brain. Once per incident (`incident` on the watch): not
  again until it recovers (`outil`: the condition false again; `alerte`: a call whose top-level
  `status` is `resolved`, as Grafana and Alertmanager send), which sends « <name> : revenu à la
  normale » under the usual rules, urgent too only if the user asked (`urgent_recovery`). A pause,
  resume or edit starts afresh. The brain is then woken as before, tainted, told the message went:
  what it adds goes through `notify` with no urgency of its own. Settings shows the fixed message
  on the watch; the bench's `u-*` and `w-*` cases cover it.
- **Asking before writing.** For `outil` and `alerte`, `watch` never decides the sensitive points for
  the user: without `urgency` (`urgent` or `normal`), an `outil` condition, or `action` /
  `no_action`, it writes nothing and returns `missing`, which the brain asks in one short message
  with the watch's defaults; other fields (every, name, signed) default.
- **The judge** (`judge.go`): an interface shaped like a decision model's API (`Ask(state,
  questions) → probabilities`) that an `outil` condition may ask a yes/no question after the
  deterministic test. Only `none` exists: without a judge, the condition alone decides. A judge may
  only hold a wake back, never make one or act; Jev (with the user's own key) or a local model
  (Ollama logprobs) come once a bench shows they catch what rules miss.

## Remote access (Tailscale)

eriphe-core can join the owner's private Tailscale network (a *tailnet*) by itself, with an
embedded Tailscale ([tsnet](https://tailscale.com/kb/1244/tsnet), BSD-3): the workspace and the
API are then reachable from every device of that tailnet, at home and away, in HTTPS with a
certificate browsers trust, and nothing is opened on the router. It is off by default and turned on
from the workspace, **Settings → Access → Espace web** (owner only):

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
| `8800` | the API (nodes, CLI, MCP), HTTP/1.1, with the same certificate; a node presents its own (see TLS between machines) |

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

- A node away from home uses the operating system's Tailscale app and the tailnet address of the
  API (see the node's README, several addresses).
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

## Memory

Eriphe's own memory, separate from the vault, in `memory.db` next to the database (SQLite, full
text search with FTS5, accents ignored). It points at the vault (`links`) rather than copying it.
A memory has a kind (`profile`, `preference`, `fact`, `person`, `episode`, `commitment`,
`lesson`, `decision`, `progress`), a subject (`owner`, `person:<slug>`, `client:<slug>`,
`project:<name>`, `home`…), dates (`valid_from`, `valid_to` once no longer true, or planned for
what is temporary; `occurred_at` for an episode; `due_at` and `state` for a commitment), where it
comes from (`source_kind`, `source_ref`: `msg:N`, `task:<id>`), a trust level (`owner`,
`first_party`, `external`), and for what the extraction derived a `basis`: `stated` (said in so
many words), `inferred` (deduced) or `ambiguous`, with a matching confidence (0.9, 0.7, 0.3). A
change over time supersedes the old memory, which stays, dated; every change is in `memory_log`.
Forgotten memories are purged after 30 days.

**Durable state.** Raw history is kept for audit (the conversation, `memory_log`, the journal
episodes); what the brain needs to act is compiled from it in facts, lessons, **decisions** (a
choice and its reason, superseded when reversed) and **progress** cards: one current card per
project, client job or goal, with a `state` (`active`, `blocked`, `done`, `paused`) and a text
saying what is done, under way, blocked and left; each change supersedes the card, so the chain
is its history.

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
approved or rejected in À valider (a card in the conversation). Schema 4 moved the `commitment` memories into the
table (state kept; each memory archived, with a log entry naming its intention; one already late
is due and listed, without waking the brain); a `commitment` written since becomes an intention.
An assistant whose spec lists its `triggers` gets the clock only if `clock` is among them.

**Search.** The full-text index holds each memory's text, subject, `keywords` (other words to find
it by: synonyms, variants) and, in a shadow column, the French stems of all three (Snowball French
in Go, plus the plural *s*), so "anniversaires" finds "anniversaire". A query is the question's
words without the question words ("quel est le nom de") nor the user's name, each matched as
typed (a prefix from four letters) or by its stem; any word matches, BM25 ranks. The ranking
then weighs relevance (BM25 over the candidates, the best at 1) by bounded factors: the subject
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
that are not all believed reads (a write, a server not trusted for its annotations) is refused;
a task with reads only, or no catalog tool, still starts. The user's own message of that turn
does not lift this. Only a delivery holding a user message starts a clean turn: a delivery of
events only (a task's permission, its end) goes on with the current turn and its taint, since
what the brain read is still in its context; so does a resumed brain session. Every write goes through a scanner: instructions to a model, invisible
characters, credentials and links carrying tokens are refused; imperative wording not from the
user is only proposed. Proposals wait in the workspace (À valider (a card in the conversation)).

**Reading back is reading too.** A memory of trust `external` (a task's journal episode, a
proposal from a mail the user approved) summarises what was read outside: a turn whose `recall`
returns one is tainted (`memory.external`), like a turn that read the mail
itself. The context pack and the wake packet, which nobody asked for, name such a memory (its id,
kind and source) without quoting its text. And what the brain writes during a tainted turn is
marked: the extraction reads it as `external`, so a mail repeated by the brain is not learnt back
as its own conclusion.

**What it knows about the user.** The Memory page opens on "Ce que Jarvis sait de toi" (the
assistant's name): the profile and the other current memories about the user (subject `owner`,
episodes and commitments aside), the pinned ones first and marked, with the cards' usual actions,
and the button that does the [first-run interview](#workspace-web) again. The list below leaves them out
(each memory shows once); a search or a filter finds them there too.

**The profile** is the pinned memories, at most 2,500 characters (enforced on write). It is
appended to the brain's prompt when its session starts, and sent again, quoted, with its next
messages whenever it changed. Claude Code's own auto-memory is turned off for the brain
(`CLAUDE_CODE_DISABLE_AUTO_MEMORY=1`): one memory.

**Extraction and journal.** Every night at `extract_at` (and once a busy day's conversation has
been quiet `idle_minutes`), eriphe-core writes out the day's transcript (messages and finished
tasks, each labelled with its trust) and starts a `memory` task (not listed, never the brain's)
with a cheap agent and the memories it may touch. Its JSON answer (ADD, SUPERSEDE, NOOP, each
citing a `source_ref` of the transcript, with `keywords`, an `importance` 1–5, a `basis` and,
for what is temporary, a `valid_to`) is checked and applied: episodes (the journal, one per
conversation day and per finished task) and what is stated in the user's words are active; what
comes from tasks or external content, what was only inferred or is ambiguous, and preferences, are
proposed; it never writes a profile, and a lesson only from a task (its receipt), proposed. Each
segment carries its date, against which the
agent resolves "demain" or "vendredi". One run per day and window; a failure is retried the next night. The same night
run purges and backs up (`VACUUM INTO` plus a JSONL export, also downloadable from the page).

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
if there is one, else the default agent. The older `trusted_servers` (catalog servers whose results
do not taint a turn) is still read, once per name, at start-up: each is marked trusted in Settings →
Tools, where the mark is changed since (see the [one taint rule](#tool-catalog)).

### What Eriphe learned

The learning journal (`learning.go`, table `learning_log` in `memory.db`, schema 5) records every
change to what steers Eriphe: a memory of kind `lesson`, `preference`, `decision` or `profile` (or
pinned) made active, changed (superseded or edited in place) or forgotten, and every change of a
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

**For the brain**: `learned` (by kind, status, subject, topic or period, with ids; a memory drawn
from external content is named, not quoted) and `unlearn` (`id`, `reason`, `source_ref`). An
undo needs the user's message; on a tainted turn it is only proposed, unless that message of the
same turn asked for it (as for `remember`), and waits as a card (kind `unlearn`) or on the page.
The wake packet and the brief (`briefLines`) carry what was learned since the last brief and not
told yet, except what the user did by hand, for the brain to say in a sentence each; `since`
lists the entries (kind `learned`), and so does the Journal.

The statuses `trial` and `dormant`, the provenance `consolidation`, the risk class and the
evidence refs are there for the learning loop that comes next: fewer cards by provenance and
risk, skills written by the brain, reflection and consolidation, helpful/harmful counters with
automatic undo.
