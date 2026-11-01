# eriphe-core

Eriphe's control plane (v0): it keeps the state of nodes and tasks in SQLite, schedules tasks on
nodes, follows them, lends identities, and gives the brain its tools over MCP. One binary, with
k8s-like modules: `store`, `api`/`mcp`, `scheduler`, controllers, `identity`.

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
Nothing else is on by default: no tool, no policy rule, no identity; the catalog's tools (Home
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
  "identities": {"github": {"env": "GH_TOKEN", "file": "/secrets/github-token"}},
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
fresh one with the context pack. Its runtime must take MCP servers and a system prompt. The provider's credential is lent to each task, plus the
`identities` the task asks for (legacy, see below).

**Identities (legacy).** `identities` in `core.json` name a file (or `vault:` secret) lent as is to a
task that asks for it by name, as an environment variable (`{"github": {"env": "GH_TOKEN", "file":
…}}`): a long-lived token inside the task. They still work, but tasks now get their access to
GitHub through [credentials on demand](#credentials-on-demand): short-lived, for one repository,
decided by the policy and logged. A task given a `GH_TOKEN` identity keeps its own way (git uses
that token, eriphe-cred is not set up). On a tainted turn of the brain, `run_task` refuses any
identity.

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
  port, and nothing else of the LAN (`node/README.md`, Egress). A task without egress (`none`)
  gets no endpoint.
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
  rule is unchanged (its node is reached directly, as for its catalog servers), so it works with
  egress `none` too.
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

Secrets stay in files (or the vault); identities are read when a task starts, sent to its node,
and never stored or logged.

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
| `auth` | exactly one of `{"bearer_file": "/secrets/x"}` (a static token, read at each connection), `{"oauth": {...}}`, `{"github_app": true}` (an installation token of Eriphe's GitHub App, refreshed before it ends: see [credentials on demand](#the-github-app)), `{"none": true}` |
| `run` | instead of `url` and `auth`: a local server, a container a node runs as a service (see [Local MCP servers](#local-mcp-servers-services)) |
| `use` | what it is for, for the brain |
| `access` | what Eriphe may do on it: `read-only`, `reads`, `free` or `ask` (see [access levels](#access-levels)); without it, `trust_annotations` says |
| `trust_annotations` | the older word for it: `true` is `reads`, `false` (the default) `ask` |
| `always_load` | its tools are loaded upfront by a harness that otherwise searches for them (Claude Code's tool search): one model round trip less before the first call; for a few tools needed fast, such as the house |
| `timeout` | seconds for one tool call (600) |

A name is lowercase letters, digits and dashes (it becomes `mcp__<name>__<tool>`).

**Upstream pool.** eriphe-core keeps one MCP session per server, lists its tools and keeps them
cached (refreshed on `tools/list_changed`), and reconnects with backoff (1 s to 5 min). A server is
`connecting`, `ready`, `needs_auth` (OAuth: the user must connect it) or `error`.

**Grants.** A task is lent catalog tools as [resources](#resources) of kind `tool`: a server name
(all its tools) or `server.tool`, where the tool may hold `*` globs (`notion`, `github.get_*`,
`github.list_issues`); the old `tools` field of `run_task` still works. The grant is stored on
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

**Tickets.** A call goes upstream only if its permission request was allowed (by the policy, the
brain or the user) for that task, that tool and that exact input, within 30 minutes, once. An agent
that found its task's secret (in its process list, say) cannot call a tool behind its own
permission prompt.

`list_tools` shows the brain the catalog: each server, its status, `use`, its tools (name, a
short description, read-only by the server's own annotation), and the command that connects it
when it needs an authorization. Descriptions come from the servers: they are marked untrusted.

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
- git and gh in a task get [credentials on demand](#credentials-on-demand): a one-hour token for one
  repository, decided by the policy, approved by the user for a first push, and logged; a vendor's
  long-lived token is lent only as the flagged fallback, without a GitHub App.
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
`main` or a local one never says so, as someone is testing it. The workspace shows `0.2.0`, and a
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
most the node may get), and the node waits for its tasks to end. While a node has a request, the
scheduler treats it as offline: new tasks wait for it. A request not done within a day is dropped.
A machine without updater (installed before, or by hand, as `deploy/nas/compose.yaml`) says so and
gets no request.

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
- **No token in clear over the internet.** A call with the API's token (or Home Assistant's voice
  token) over plain HTTP from a public address is refused (403): the token already crossed the
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
| `run_task` | start a task: prompt, [resources](#resources) (a repository, the user's folders, catalog tools), agent (or the default), node (or let the scheduler choose), an optional [contract](#contracts-contractgo); the old fields (`repo/ref/branch/dir`, `tools`, `folders`, `identities`) still work |
| `list_folders` | the user's folders: name, machine (online or not), access (`read` or `drop`), where a task finds it |
| `list_skills` | the user's [skills](#skills), approved and on: name, description, files, whether it has scripts |
| `read_skill` | a skill's `SKILL.md`, or another of its text files (never a script: scripts run in tasks) |
| `task_status` | state, the agent's last message (without its receipt), its checked [receipt](#receipts-receiptgo), the pending permission; `wait` blocks until the task needs you |
| `list_tasks` | recent tasks |
| `answer_task` | allow or deny the pending permission, or reply to the agent's question |
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
- **Bound**: a task with the user's folders goes to the node that has them, and waits for it.
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
checked when the task starts (at most 10 checks, unique names, paths under `/work`, budgets within
7 days, 5 attempts, 500 turns), kept in `task_harness`, given to the agent after its standing
instruction, returned by `task_status` as `contract`, and shown in the task's drawer.

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
| same failure twice | the same class and words (numbers aside) twice in a row, or a check the node stopped sending back | **stops** the task, even with attempts left |

The budget of a contract is kept by eriphe-core: `minutes` (checked every 15 seconds), `turns`
and `tokens` (input, output and cache writes from the turns' `usage`, where the harness reports
it) at the end of a turn; a turn whose receipt says `done` is not stopped (the brain closes the
task), and `answer_task` refuses a new turn once the turns are spent. A task eriphe-core stops
keeps why (`stop_reason`): the brain hears it with the last receipt when the node reports it
stopped, and the drawer says so; a retry on its way shows its time. A task whose agent or container
failed is told to the brain with its class: worth starting again once, or not. Each failure's class
is kept in `task_harness` for the measures. The brain's prompt no longer counts retries itself.

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

## Permission policy

Every permission request of a worker goes through a policy written in code before the brain hears
of it, so that a prompt injection (a mail, a web page) cannot talk the brain into it:

1. **built-in denials**, which no rule lifts: recursive `rm`, pushing to `main`/`master`, force
   pushes, `git reset --hard` and history rewrites, reading secrets (`$GH_TOKEN`, `printenv`,
   `/proc/*/environ`, Claude Code's `.credentials.json`, Codex's `auth.json`, `$ERIPHE_CODEX_AUTH`,
   `$CODEX_HOME`…), writing outside `/work`. OpenCode's and Codex's requests (the `opencode` and
   `codex` runtimes) come under Claude Code's tool names (`Bash`, `Edit`, `Write`, `WebFetch`…)
   with the same inputs, so the same rules apply;
2. the configuration's `policy.rules`, first match wins:
   `{"tool": "Bash", "match": "^go test", "decision": "allow"}` (`tool` is a name, a glob such as
   `mcp__*`, or names joined by `|`; `match` is a regular expression on the command, path or URL;
   `decision` is `allow`, `deny` or `ask`);
   then the actions the user allowed for good ("Toujours autoriser", [below](#access-levels)),
   which the file's rules come before;
3. **built-in asks**, for what reaches a third party (merging, releasing, publishing, `ssh`, `curl
   -X POST`, external MCP tools): only the user may answer, through the CLI; the brain is told to
   relay them and its answer is refused. The exception: what a catalog server's
   [access level](#access-levels) leaves free (a read, or any tool of a `free` server) is the
   brain's to judge, and a write of a `read-only` server is denied before the rules;
4. anything else is the brain's to judge.

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

Every decision (by the policy, the brain or the user) is kept in the `decisions` table.

### Access levels

Each catalog server has an access level (`access`, `access.go`): **what** Eriphe may do on it,
kept apart from **how** its credential is scoped.

| Level | Workspace | Reads | Writes |
|---|---|---|---|
| `read-only` | Lecture seule / Read only | free | not lent: not in `tools/list` of a task nor in `list_tools`, denied by the policy and refused by the proxy |
| `reads` | Lecture libre, écriture sur accord / Free reads, writes on approval | free | the user's to approve |
| `free` | Tout libre / Everything free | free | free (the brain judges a worker's; the brain's own are allowed in advance) |
| `ask` | Tout sur accord / Everything on approval | the user's | the user's |

A read is what the server itself marks `readOnlyHint`. `ask` is the former "Tout me demander":
the server's annotations do not count. `reads` is the default of the presets; "Other…" (a server
whose annotations nobody vouches for) starts on `ask`. An entry without `access` keeps its meaning:
`trust_annotations: true` is `reads`, without it `ask`; the old form values (`read`, `trust`) are
read the same way. Whatever the level, the built-in denials come first, then the file's rules.

**Always allow.** A permission card for a catalog tool's action offers "Toujours autoriser" (the
API: `POST /v1/tasks/{id}/answer` with `"allow": true, "always": true`): the request is allowed,
and `server.tool` is allowed without asking from then on, for any input. These are rules kept in
eriphe-core's database (`allowed_actions`), evaluated after the file's rules (which win) and never
above a built-in denial or a `read-only` level (the card does not offer it there). Each tool's
access page (Settings → Tools → Access, `/settings/tools/{name}/access`) lists them, with
"Retirer"; removing a server forgets them.

**Scoped credentials.** Where the service can scope its credential, the level decides what Eriphe
asks for:

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
| `repo` | `owner/name` on GitHub, or a git URL; `branch` (work branch), `base` (start), `dir`; `trust_skills` | `read`, `write` | cloned into `/work/repo`; [credentials](#credentials-on-demand) for it; its skills if it is the user's own or trusted (below); one per task |
| `folder` | a machine's folder, by name (`list_folders`) | `read`, `drop` (at most the folder's own) | mounted at `/work/folders/<name>`; the task runs on its machine |
| `drive-folder` | a Google Drive folder, by name | `read`, `drop` (at most the folder's own) | for now, the files server whole (it reaches every Drive folder given to Eriphe, each with its own access) |
| `tool` | a catalog server, or `server.tool` with globs | `read`, `write`; from the tools' annotations when empty, and `read` is refused for tools that may act | relayed through the proxy |
| `file` | an attachment of the conversation (`att_…`) | `read` | not yet: refused with a clear error |

The access defaults to `read` (for a tool, to what its annotations say). Each entry is checked
against what the user opened: an unknown folder or server is refused, and so is a `drop` on a
folder opened for reading. The old fields are read into the same list: `repo/ref/branch/dir` become
a `repo` entry with access `read` (its pushes ask, as they always did), `tools` become `tool`
entries, `folders` become `folder` entries with the folder's own access. The list is kept with the
task (`tasks.resources`, JSON; empty for tasks from before it); the old columns (`tools`, `folders`,
`dir`) are still written, and the node still gets its spec fields (`repo`, `folders`…), derived from
the list.

**A repository's skills** (`.claude/skills`, `.agents/skills`, `.opencode/skills`: instructions
and scripts its agents load by themselves) load only from the user's own repositories
(`repo_skills.go`): with the GitHub App, one it is installed on; without it, one the GitHub tool's
token may push to. Anything else (another host, someone else's repository, GitHub not reachable)
keeps them off, unless the entry says `"trust_skills": true`. The node gets the answer as the
spec's `repo_skills` and turns them off per agent (node/README.md, Agent skills); whatever loads,
a skill never approves a tool by itself.

**On a tainted turn** (the brain read external content), `run_task` refuses any `write` or `drop`
entry, `trust_skills`, tools that may act, a Drive folder (its files server is lent whole for now)
and any legacy `identities`: the brain asks the user and starts the task in the turn of their
answer. Reads still go.

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
- **On or off**, and **delete**. Bundles no skill points at any more are deleted.

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

## Credentials on demand

A task asks for the access it needs when it needs it, through the standard extension points of
its tools, and eriphe-core decides and mints a short-lived credential for that one thing: **no
long-lived secret ever enters a task** (the study behind it: tasks adapt to what they need, and
ask for their access the same way they install their tools).

**The path of a request.** `eriphe-cred` (node/cred, a small static Go program) is in every task
at `/opt/eriphe/bin`, with the injected agent (node/README.md, Injected programs). It is:

- git's **credential helper**: the task's entrypoint sets `credential.helper=/opt/eriphe/bin/eriphe-cred`
  and `credential.useHttpPath=true` (environment `GIT_CONFIG_*`, nothing written), so git names the
  repository; git's protocol does not say whether it fetches or pushes, so eriphe-cred reads it
  from the git command that started it (`push` writes, anything else reads);
- **gh's wrapper**: also there as `gh`, ahead of the real one (the image's, or the one the agent
  installs with apt), it asks for a token for the command's
  repository (`-R`, `GH_REPO`, or the `origin` remote), read for `pr view`/`list`/`diff`/`checks`,
  `issue view`/`list`, `api` with GET…, write otherwise, and runs the real gh with `GH_TOKEN` for
  that one process.

It posts `{protocol, host, path, access}` to the node's task listener
(`<callback-url>/v1/task/credential`, with the task's own secret, as the permission endpoint);
the node relays it to `POST /v1/node/tasks/{id}/credential` with its own secret and certificate.

**The decision** (`decideCredential`, deny by default; first that applies):

| | Answer |
|---|---|
| a host eriphe-core has no service for (anything but `github.com`, `api.github.com`) | none: git goes on to another helper, or none |
| not `https`, no repository (git without `useHttpPath`), an access other than `read`/`write` | deny |
| the brain | deny: it works through its tools |
| no GitHub repository is lent to the task: no `repo` [resource](#resources) on GitHub (a task from before resources: no GitHub catalog server in its grant) | deny |
| the first rule of `policy.credentials` that matches (host and repo globs, access) | its decision: `allow`, `ask` (once per task), `deny` |
| no rule: a repository its `repo` resource covers (a task from before resources: its `repo`, as `read`) | read: allow; write by git with access `write` and a declared `branch`: allow (the user's own turn lent it so; a tainted turn cannot); any other write (access `read`, no branch, gh): ask, the first time in the task |
| no rule: another repository | deny |
| a write allowed so far, by a task that read external content (an external catalog tool, a page, a web search) or was started on a tainted turn of the brain | ask |
| allowed so far, without the GitHub App (the fallback below) | ask, the first time in the task |

A repository lent for `write` on a branch makes its first push there approved; a `git push` of that
task to another branch (`git push origin other`, `HEAD:other`, `--all`, `--tags`, `--mirror`) is the
user's to approve (`undeclaredPush`, policy.go). It reads the command line: a push of the current
branch after the task switched to another one is still the brain's to judge, as before.

```json
"policy": {"credentials": [
  {"repo": "my-org/docs", "access": "read", "decision": "allow"},
  {"repo": "me/site", "access": "write", "decision": "allow", "reason": "Eriphe publishes the site"},
  {"repo": "my-org/infra*", "decision": "deny", "reason": "never from a task"}
]}
```

**Approval.** "Ask" goes through the task's permission flow: the node asks a permission
`Credential` (`write github.com/owner/repo`), which only the user may answer (the policy marks it
so, the brain's answer is refused), on the approval card of the workspace like any other. The
tool waits meanwhile. Once allowed, the approval holds for that repository (a write covers reads)
for the rest of the task, and the node asks eriphe-core again, which mints.

**The credential.** With [the GitHub App](#the-github-app), an installation token restricted to the
one repository and the rights asked for, valid one hour: git reads `contents: read` or `write`; gh
reads `contents`, `pull_requests`, `issues`, `checks`, or writes `pull_requests` and `issues`
(never `contents: write`: a merge is not a task's). git gets `password_expiry_utc`; eriphe-core
reuses a token for the same task and rights until ten minutes before it ends. Without the app, the
GitHub tool's own bearer token is lent instead (**fallback**): long-lived and not limited to the
repository, it is lent only once the user approves it (an `ask`, then held for that repository for
the rest of the task), and flagged in the log, in eriphe-core's log (`WARNING: long-lived token lent`) and in
the task's details, and the Tools page says to create the app.

**The log.** Every request is kept (`credential_log`: time, task, tool, host, repository, access,
decision, by whom, source, reason), with the user's answers to the approvals, and shown in the
task's details ("Accès demandés") and by `GET /v1/tasks/{id}/credentials`. Approvals are in
`credential_grants`.

**What it does not stop.** The token is in the task for the tool that asked (an hour, one
repository): a task that leaks it leaks that much. The task's secret, which eriphe-cred uses, is
in the task's environment (the egress relay's address already carries it): the agent could ask
for a credential itself, which is no more than git can, and the policy denies reading it
(`$ERIPHE_TASK_SECRET`) like the other secrets.

### The GitHub App

Each user creates **their own** GitHub App, in one click, from Settings → Tools → GitHub →
"Créer l'app GitHub d'Eriphe" (GitHub's manifest flow, `web_github.go`):

1. eriphe-core writes the manifest: name "Eriphe – <suffix>" (the user's name by default),
   permissions `contents` write, `pull_requests` write, `issues` write, `metadata` read, `checks`
   read, no webhook (`hook_attributes.active` false), private, `redirect_url`
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
   (`github.com/apps/<slug>/installations/new`) and shows where it is installed.

Minting signs a JWT with the app's key (RS256, nine minutes), finds the installation of the
repository (`GET /repos/{owner}/{repo}/installation`) and asks
`POST /app/installations/{id}/access_tokens` with `repositories` and `permissions`.

"Brancher l'outil GitHub sur l'app" puts the GitHub catalog tool on the same app
(`{"auth": {"github_app": true}}`): an installation token of the owner's installation, with its
repositories and rights, renewed five minutes before it ends. One GitHub connection for the tool
and for git; the tool's former token is forgotten. A tool of the configuration file is changed in
the file. "Oublier l'app" removes it from the vault (it stays on GitHub).

## Assistants

An assistant is a long-lived agent that owns one task, as a Kubernetes Deployment owns its Pod.
Its spec says what it is and what it runs on; its status is what eriphe-core keeps of its task:

| Spec | |
|---|---|
| `role` | what it is: a card in `assistantRoles` (`brain` today), with its powers (below) |
| `display_name` | what the user calls it, as a person (see [its name](#the-assistants-name)); Eriphe for the brain by default, its id for another |
| `agent`, `node` | what it runs on; without an agent, the default one or the first that can run it |
| `instructions` | its standing instructions (`{{user}}` and `{{name}}` replaced); its role's built-in ones by default; `instructions_file` in the file only |
| `tools` | its grant from the catalog, like a task's; its role may add the catalog's free reads |
| `resources` | what it is lent besides its tools (`{kind, ref, access, …}`): kept, lent with the resources work |
| `triggers` | what wakes it: `conversation` (the user's messages and the tasks' events), `clock` (an intention of the memory that comes due: the wake packet, see [Memory](#memory); or a watch that fired, see Watches). Its role's by default (the brain's: both); `conversation` and `clock` only for a role that `receivesConversation`, and one enabled assistant at most has the conversation |
| `reports_to` | the assistant it reports to: the brain (the one with the conversation), the only one it may name; empty means the brain. The brain reports to no one |
| `disabled` | its task is stopped, and none started |
| `self_url`, `mcp_servers` | the file only (see the brain) |

A role's card lists its powers, what its assistants may do; the code checks them where it decides,
never a name:

| Power | |
|---|---|
| `talksToUser` | what it writes goes to the conversation |
| `receivesConversation` | it may have the `conversation` trigger (needs `talksToUser`) |
| `launchesTasks` | eriphe-core's own MCP tools: it starts, follows and stops tasks, and reads and writes the memory |
| `lendsResources` | it lends the tasks it starts their resources (needs `launchesTasks`) |

The brain's role has all four; a role with none watches and reports, through its triggers.

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
added through the API only, for now; a file may also list them under `assistants`. An install from
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
`short_name`), Home Assistant's conversation agent (`GET /v1/voice`), and the results subfolder
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
eriphe-core) still makes the brain call that address directly with the API token, as before.

**It reads by itself.** The brain is lent, with no configuration, every tool a server annotates
`readOnlyHint` when the server is trusted for its annotations (any [access level](#access-levels)
but `ask`; `reads` is the default of the presets), and every tool of a server whose access is
`free`, through its proxy like any
task. These free reads are allowed in advance in its assignment (`allowed_tools`) and called
without a ticket: no permission round trip, a decision on record all the same. A read named by a
policy rule of the configuration is not free: it goes through the policy with its input. The list
is computed when its node fetches the task, so a tool or server added later reaches the brain at
its next start (the proxy itself follows the catalog at once); in eriphe-core's first minute, the
brain waits for the trusted servers that are still connecting before it starts.

Its other catalog tools (`brain.tools`) are not allowed in advance: each call goes through the
policy like a worker's, except that what would be the brain's to judge is allowed, and a call
without its permission is refused by the proxy. What is the user's waits for the user: eriphe-core says so in the conversation, and the brain's
task shows in `GET /v1/tasks` (and the CLI) until the user allows or denies it. The brain's other
permission requests are denied, but for the web.

**The web.** The brain searches with its harness's own search: Claude Code's `WebSearch` is in its
`allowed_tools`; Codex is held to `web_search = "cached"` by the node; OpenCode's `websearch` is
denied (a harness without a search is told so in its standing instructions). Its page reader
(`WebFetch`, OpenCode's `webfetch`) is not allowed in advance: each read is decided by eriphe-core
(`brain_web.go`). A URL that is not http(s), carries credentials, or names this machine or the local
network (localhost, a loopback, private, link-local or CGNAT IP, a name without a dot or under
`.local`, `.lan`, `.internal`, `.home.arpa`…) is refused; a policy rule on `WebFetch` that denies or
asks wins; on a clean turn the read is allowed; on a tainted turn it is the user's (told in the
conversation), unless the URL is written in the user's own message of that turn. Reads asked
together are judged on the taint before them. Every decision is logged (`brain: WebFetch …`) and
kept in the decisions table. A read, like a search, taints the turn.

`brain.mcp_servers` (servers given to the brain directly, with their token, and allowed without
asking) still works but is deprecated: move each to `servers`, add it to `brain.tools`, and add
policy rules for what the brain may use without asking. A `token_env` token must move to a file.

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

## Voice (Home Assistant)

Home Assistant's Assist pipeline keeps what it understands (simple commands of the house, handled
locally) and hands the rest to the assistant through its conversation agent (`../ha/`), which calls
`POST /v1/voice` with its own token (and `GET /v1/voice` for the assistant's name, `{"name"}`, which
the agent takes as its own) (Settings → Tools → Home Assistant, the page that has both
ways: Eriphe driving the house through Home Assistant's MCP server, and the house's voice talking
to Eriphe; only the token's hash is kept, and it can do nothing else). The sentence joins the conversation like any of mine, marked with the
room (`[Sam 12:00 #N · voix, Bureau]`), and the brain answers it in the same thread, short and
without markdown. The answer comes back as NDJSON, `{"delta"}` lines then `{"done", "text",
"continue"}`, from the brain's draft as it writes. Only what it says after reading the sentence
counts. Without a word within 10 s (another turn, a long job), the voice answers « Je m'en occupe,
je te préviens » and the answer stays in the conversation.

**An automation is not the user.** A sentence from no device, no satellite and no Home Assistant
user (`from_user`, which the integration sets when the conversation's context has a user) is an
automation's. It may only send a closed request, `brief` (the point of the moment, read aloud);
anything else is refused (400). The request arrives as an event worded by the code (`[événement
… · Home Assistant, automatisation]`), never as the user's message, and its turn is tainted: the
brain may answer and propose, never act nor authorise. It waits up to 80 s for the answer it sends
on. What an automation knows of the house (someone came home, the TV is on) goes to `POST
/v1/context` as a signal, with a source token of its own.

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
   Google app ([below](#your-own-google-app)), kept in the vault.
2. **Connect**: "Connecter Google Drive" sends the browser to Google; "Permettre à Eriphe de
   déposer des résultats" asks for the scope that writes. The account comes back connected, and the
   workspace creates the catalog server `fichiers` (image `ghcr.io/alexis-marcel/eriphe-files`,
   access `reads`: its reads are free, `write_file` is the user's to allow). Set to read only,
   the tool connects with `drive.readonly` whatever the box says.
3. **Choose**: once the service runs, the page browses the account's folders (My Drive, Shared with
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
in [docs/google-drive.md](../docs/google-drive.md)). Nobody else holds its secret, there is no
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
same link is refused. The refresh token goes to the vault with the client id and secret, which
eriphe-files needs to refresh it.

### Addresses Google takes

Google checks an authorized redirect URI against its
[validation rules](https://developers.google.com/identity/protocols/oauth2/web-server#uri-validation):
https (http only for localhost), no raw IP address (loopback ones aside), and a host whose
top-level domain is on the public suffix list. `googleOrigin` applies them to the workspace's
origin (`publicsuffix`, ICANN section; `home.arpa` counts as local): a workspace opened at
`https://192.168.1.2:8443` or `https://eriphe.local` cannot connect Drive, and the page says so
instead of showing the guide, with what to do:

- turn on **Remote access** (Tailscale gives `https://<name>.<tailnet>.ts.net`, which Google takes;
  when it is on, the page links to itself at that address);
- or put Eriphe behind the user's own domain (`https://eriphe.example.org`);
- or, with no Google app at all, "Google Drive for desktop" on an always-on computer, its synced
  folder given as a folder of that machine ([Folders](#folders)).

`startDrive` refuses such an origin too (`drive.origin.ip`, `drive.origin.local`,
`drive.origin.http`).

### Testing or In production

In the console's Audience, a **Testing** app only takes its test users (up to 100), and Google
expires a test user's authorization, refresh token included, seven days after consent
([Manage App Audience](https://support.google.com/cloud/answer/15549945),
[refresh token expiration](https://developers.google.com/identity/protocols/oauth2#expiration)):
Drive must be connected again every week. **Published** ("In production"), that expiry does not
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

## Folders

The user's own folders, given to Eriphe ("Dossiers"). A folder is a name ("Clients"), a source (a
machine and a path inside one of the places that machine shares, declared when it was installed:
`--share NAME=/PATH`, see install/README.md; eriphe-core never sees a host path, only the place's
name and a path relative to it) and a right: `read` (the default), or `drop`, read and drop results,
which go only to the folder's results subfolder (`Eriphe/`, or the assistant's name: see [the
assistant's name](#the-assistants-name)). Originals are never changed nor deleted. Folders
are in the database (`folders` table, names unique without case); a machine's places are what its
node reports (`shares` in `GET /v1/node`).

**Two sources, one list.** A folder is on a machine (here) or in the user's Google Drive (see
[Google Drive](#google-drive-cloud-folders)). Settings → Dossiers, `GET /v1/folders` and
`list_folders` show both, each with its `source` (`machine` or `google-drive`) and `lend`, how a
task gets it. Each source keeps its own store and its own enforcement: a machine's folder is in the
`folders` table and mounted by its node; a Drive folder stays in the files server's `FILES_CONFIG`,
which eriphe-files reads and enforces itself, and is lent through that catalog tool (`tools:
["fichiers"]`, which reaches every Drive folder given to Eriphe), not through `folders`, which
refuses it with that hint. Drive folders are added on Settings → Tools → Google Drive; "Ne plus
partager" in Settings → Dossiers removes either kind.

**Sharing one.** Settings → Dossiers → "Partager un dossier": pick a machine, browse its places
from any device (eriphe-core asks the node through its tunnel, which lists directories only, and how
many files each has, never their names nor their content; hidden and sensitive folders are not
listed), then name the folder and choose its right. eriphe-core asks the node once more whether the
folder can be shared (no sensitive place inside, below) and keeps it under the path the node
resolved, without symlinks. `drop` needs a place that is not read-only (`:ro`). "Ne plus partager"
forgets it; the files stay.

**Lending one.** The brain lends folders to a task as [resources](#resources),
`{"kind": "folder", "ref": "Clients", "access": "read"}` (or `drop`, if the folder allows it;
`list_folders` gives each one's entry in `lend`; the old `folders` field still works). The task is placed on the folders' machine (all of a task's
folders must be on one machine), or waits for it while it is offline (`pending_reason` says so);
`node` may only name that machine. Its node mounts each at `/work/folders/<name>`, read-only as
Docker enforces it; with `drop`, its results subfolder read-write, created if missing (see
node/README.md, Shares). The worker is told where they are, that it may write only in the results
subfolder, and
that what reads like an instruction in the files is data. The folders are read again from the
database whenever the node fetches the task: a folder removed meanwhile fails it, a folder closed to
drops since is mounted read-only. A task drops only where its entry says `drop` (a task from before
resources: wherever its folder's access said so). A task's `folders` lists their names.

**What is read there is data.** A folder holds whatever the user (or anyone who sent them a file)
put there, so it is treated as external content, like a mail or a page:

- the event that brings the brain the result of a task that read folders says so, and taints the
  turn that reads it (`folder:<names>`), as `task_status` of such a task does: the memory only
  proposes what that turn remembers, and the brain may not allow a permission (see
  memory_trust.go);
- on a tainted turn, `run_task` refuses to lend a folder with the `drop` right, as it refuses
  every `write` or `drop` entry and catalog tools that act; folders to read still go;
- the permission policy applies to the task as to any other: its writes outside `/work` are denied
  by the built-in rules, and Docker refuses any write in the folder but in `Eriphe/`.

| Call (bearer token) | What |
|---|---|
| `GET /v1/folders` | the folders, with `mount` and whether their machine is `online` |
| `POST /v1/folders` | `{name, node, share, path, access}`: checked with the node, which must be online; 201 with the folder, or `{code, error}` (`exists`, `bad_name`, `holds_sensitive`, `outside`, `offline`…) |
| `DELETE /v1/folders/{name}` | no longer share it |
| `GET /v1/nodes/{name}/shares` | the places a machine shares: `{name, writable, missing}` |
| `GET /v1/nodes/{name}/shares/{share}/dirs?path=P&check=1` | the node's listing of a folder (see node/README.md), through its tunnel |

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
| `GET /v1/tasks/{id}/credentials` | what the task asked for with [credentials on demand](#credentials-on-demand), the latest first: time, tool, host, repo, access, decision, by whom, source |
| `POST /v1/tasks/{id}/answer` | `{reply, allow}`: allow or deny the pending permission (`allow` set), or reply to an idle task |
| `POST /v1/tasks/{id}/stop` | stop the task's agent |

An unknown task answers 404; a call the task's state does not allow (or that its node refuses)
answers 409 with the reason.

## Workspace (`web`)

Eriphe's own interface, served by eriphe-core on its own HTTPS listener: the conversation with
Eriphe in the middle, its tasks on the side as live cards (machine, agent, repository and branch,
state, the pull request or document the agent mentioned), the permissions waiting for an answer
inline in the conversation (Yes / No, with an optional reason for the agent), Activité (what one
consults) and the settings (what one sets). The assistant manages: what the brain can set itself
(a rule, a watch, a memory, an intention) is asked of it in the conversation, and its pages show
the state, say why, and undo, with a line saying how to ask rather than a form.
The conversation is the one main screen; the header has three buttons, Tâches (a badge counts the
running tasks, and turns urgent when one waits for the user), Activité and Réglages. On a wide screen Tâches
hides or shows the side panel; on a phone it opens the tasks as a sheet over the conversation
(closed by its button, Escape or the browser's back), and from any other page it comes back to
the conversation on its tasks (`/#tasks-panel`).

**Activité** (`/activity`, `web_activity.go`) has two views. The feed is what the brain reads with
`since` (below), by kind (tasks, notifications, watches with their silences, intentions, messages)
and period; its "tool accesses" kind is the journal of decisions, paged. **À valider**
(`/activity/approve`) is the one place where what the brain proposed waits for the user: the
permissions of tasks, memories and intentions, notification rules, watches, and the quiet hours
the activity suggests (to adopt in one tap). Each is approved or rejected with its own action, and
the list reloads itself. The Activité button counts the proposals (not the permissions: Tâches
shows them) and opens À valider while any waits.

The settings come in four groups: Assistant (the memory, "Ce que Jarvis sait de toi", still at
`/memory`, the watches, the agents, the skills), Connexions (tools, folders), Appareils & accès
(devices, remote access, notifications) and Mon espace (account, sign-out); an entry is marked
when it needs a look (an update, a tool in error, a refused key). On a phone the settings say the
way back, "‹ Conversation". The pages that moved keep their addresses as redirects:
`/settings/happened` and `/settings/journal` lead to Activité, `/settings/channels` and
`/settings/where` to their sections of Notifications, `/memory?view=inbox` to À valider.
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
local server runs) is under the advanced options. Mails and Agenda first ask which kind of account
(Gmail, iCloud, Orange, Free, OVHcloud, Fastmail or any IMAP server; iCloud, Fastmail, Nextcloud or
any CalDAV server) and run `eriphe-mail` and `eriphe-calendar` with its password; "Test the
connection" tries that password (IMAP login, CalDAV PROPFIND). Outlook, Microsoft 365, Google
Calendar and Microsoft calendars are shown as coming soon: they wait for eriphe-core to lend OAuth
tokens to local servers. A vendor's OAuth service (Linear, Notion and the catalog's) opens a short page (name, use, access), then "Se
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
| `auth` | how one signs in: see [Signing in](#signing-in); without it, Settings → Account says |
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

The workspace has one owner; how they sign in is set in Settings → Account → Connexion, or in
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

**Changing the mode keeps you in.** Settings → Account → Connexion shows the four modes as
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
| `external_url` | the workspace's address (Settings → Account → Adresse de l'espace): the provider's redirect, the tools' and Google Drive's OAuth redirect, and an origin the forms may come from behind a proxy that changes the host |
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

- **The workspace's address** (Settings → Account → Adresse de l'espace, or `web.external_url` /
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
Settings → Notifications tells how to install it on this device (iPhone, Android or a
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
  tainted turn, they are proposed and the user approves them in Activité → À valider; Settings →
  Notifications shows every rule in force, to delete (a new or changed one is asked of the brain).
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
intention (owed or to do once), watch (comes back on its own), rule (how to be told about a topic,
quiet hours), setting (how to be told in general), notify, since, task. What the code enforces is
never a memory: `recall` returns the matching rules typed as rules, and the tools refuse the
wrong concept with a hint naming the right one (`remember` of a notification preference → `rule`
or `setting`; `watch` at a one-off time → `intend`; a recurring `intend` → `watch`). The concept
bench (`concepts_test.go`) checks, without a model, that each request's right call is taken and its
wrong one redirected, and that the map is in the MCP instructions and the prompt.

The interruption bench (`notify_eval_test.go`, `testdata/notifyeval`) runs with the tests: it scores
the decision on labelled cases (accuracy, precision and recall of "now", night wake-ups no rule
allows: none, injection cases blocked: all).

**Channels: push and Telegram** (`channels.go`, `telegram.go`, `web_telegram.go`). What is sent
(`tell`) goes by the user's channels: web push and Telegram, all by default, a narrower default in
Settings → Notifications (Canaux), or a topic rule's own (`channels`: `push`, `telegram` or both). The decision
above is unchanged; the channels only say where a "now" goes. Telegram, two-way, without an
inbound port:

- **Setup.** The user creates a bot with BotFather and pastes its token in Settings → Notifications (Canaux):
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
- **Approvals stay in the workspace.** No button approves anything: a permission that is the
  user's (user_only) is told on Telegram with what waits and a link, and proposed rules or memories
  are approved in the workspace's À valider only. (An optional "approve
  from Telegram" with a second, explicit confirmation is not built.)

**Where to reach the user** (`route.go`, `route_signals.go`, `route_send.go`, `web_where.go`).
A pure function, `route(note, signals, settings, tried)`, picks one device at a time from signals
eriphe-core already has, never from the model nor the notification's text:

1. Eriphe open and in front on a device (its tab tells its push subscription with `POST
   /presence`) → a banner in that page (the event stream's `notice`, or the presence answer), no
   system push;
2. a computer in use (its node reports the machine's idle time under 2 minutes, not locked, not
   to be disturbed: a game or a meeting in front, the context below) → a push to that computer's
   subscription only;
3. the voice in the room: a Home Assistant speaker mapped to the room a fresh (under 2 minutes),
   personal signal of Home Assistant puts the user in (below), for a "now" from a clean turn,
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

Signals live **in memory only** (`contextStore`), never in the database and with no history; an
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
source `ha`). With the long-lived token of the catalog's Home Assistant tool (the `home-assistant`
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
   `focus`), `voice` (a satellite or a media player with an area, for the router). Applied when the
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
4. **Speak**: a `voice` mapping makes the router's voice line usable (above). eriphe-core calls
   `assist_satellite.announce` (or `tts.speak` with the house's first `tts` entity for a media
   player) through the REST API. An urgent watch's fixed message is said instead of the generic
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
- **Activité** (`/activity`) shows the same feed to the
  user, by kind, since they were last here (the start of their last absence; the last 24 hours if
  none), or the last 24 hours or 7 days, external items marked.

**Watches** (`watch.go`, `watch_schedule.go`, `watch_tick.go`, `watch_poll.go`, `watch_alert.go`,
`watch_grant.go`, `judge.go`, `watch_tools.go`, `web_watch.go`).
What wakes the brain on its own, besides a due intention: one tool, `watch(name, source, schedule |
after, prompt, brief?)`, with `watches` (each with its last runs) and `update_watch` (pause, resume,
edit, delete). The brief, the point made on the user's return and the weekly review are not
features of their own: they are watches whose prompt composes `since`, `notifications` and
`notify`, which the brain suggests and never imposes.

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
  `[veille #N « nom » HH:MM · source …]`, the cause (late, the return and the absence), for a brief
  where the last brief was, and the watch's prompt; the brain answers through `notify` or with
  `[rien]`. It goes on with the current turn, as an intention's wake does; a watch that was
  proposed on a tainted turn (then approved) taints it. When its turn ends (or before the next
  delivery) the run records what the brain did: `notified`, `said` (in the conversation only) or
  `silent`. A brief's wake becomes the kv `last_brief` once its turn ended, for
  `since("last_brief")`.
- **Who writes them.** The brain writes a watch only when the user asked for it in that clean turn
  (its `source_ref` cites the user's message of the turn); otherwise, and always on a tainted turn,
  it is proposed and the user approves it in **Activité → À valider**. Settings → Veilles lists the routines
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
  the brief at most (the bench has these cases), it may not allow a permission nor lend a task
  anything that writes; `since` names it without quoting it.
- **History and silences.** Each run keeps what the brain did (notified, said, silent, acted, or
  missed, dropped), in Settings and in `since`; the next brief lists the watches that stayed silent
  since the last one.
- **An action authorised in advance** (`watch_grants`). A watch may carry one exact action: a catalog
  tool with exact arguments, or a `run_task` with a fixed prompt, agent, resources and contract;
  `max_uses` 1 by default (10 at most), an expiry (7 days by default, 90 at most). It is written only
  on a turn that read nothing external, citing the user's message of that turn, which must authorise
  it; never from a tainted turn, so an injected payload can neither make nor widen one; editing the
  watch revokes it, and the user revokes it in Settings. On that watch's own wake turn, tainted or
  not, the call whose canonical input hashes the same is let through by the policy (a ticket, a
  decision on record, `byPolicy`), counted atomically; any other tool or argument still goes to the
  user. A built-in or configured denial is never granted. Each use is logged (`WATCH GRANT USED`),
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
from the workspace, **Settings → Remote access** (owner only):

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
`secret`, an identity's `file`, a brain MCP server's `token_file`:

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
owed `intend`, `intentions`, `update_intention`. Results are data, quoted.

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
approved or rejected in Activité → À valider. Schema 4 moved the `commitment` memories into the
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
catalog tool (seen by the proxy, or in the turn's events for servers it reaches directly and the
web tools) is tainted: what it remembers is only `proposed`, unless the user's message of that
same turn asked for it. A tainted turn also cannot act: the brain's `answer_task` allowing a
permission is refused and the request becomes the user's to answer, and `run_task` lending tools
that are not all believed reads (a write, a server not trusted for its annotations) is refused;
a task with reads only, or no catalog tool, still starts. The user's own message of that turn
does not lift this. Only a delivery holding a user message starts a clean turn: a delivery of
events only (a task's permission, its end) goes on with the current turn and its taint, since
what the brain read is still in its context; so does a resumed brain session. Every write goes through a scanner: instructions to a model, invisible
characters, credentials and links carrying tokens are refused; imperative wording not from the
user is only proposed. Proposals wait in the workspace (Activité → À valider).

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
  "backup_keep": 14,
  "trusted_servers": ["notes"]
}
```

All keys are optional: `db` defaults to `memory.db` next to `db`, `agent` to the catalog's `haiku`
if there is one, else the default agent; `trusted_servers` lists catalog servers whose results do
not taint a turn (none by default).
