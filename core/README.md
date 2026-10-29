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
docker run -d --name eriphe-core -v eriphe-data:/data -p 8800:8800 -p 8443:8443 ghcr.io/alexis-marcel/eriphe-core
docker logs eriphe-core 2>&1 | grep -o 'setup code [A-Z0-9-]*'
```

At first start it makes what it lacks in the data directory, and reuses it after: the API token
(`core-token`), the vault key (`vault.key`), the database (`core.db`, `memory.db`) and the
workspace's self-signed certificate (`web-cert.pem`, `web-key.pem`). The API listens on
`0.0.0.0:8800`, the workspace on `https://0.0.0.0:8443`, and the brain is on, with no agent yet.
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
               "use": "GitHub: issues, pull requests, code search", "trust_annotations": true},
    "home":   {"url": "http://192.0.2.20:8123/api/mcp", "auth": {"bearer_file": "/secrets/home-assistant-token"},
               "use": "the house: lights, heating, sensors"},
    "notion": {"url": "https://mcp.notion.com/mcp", "auth": {"oauth": {}}, "use": "notes and docs in Notion",
               "trust_annotations": true}
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
| `trust_annotations` | the server's own `readOnlyHint` counts for the policy (below); off by default |
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
`client_id_file`/`client_secret_file`, else dynamic registration), PKCE, the `resource`
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
latest release (`updates.go`). A machine is behind when it runs a release older than that one
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
installed. Going back to the previous version, or staying on one, is the install command's
(`--rollback`, `--version`). A request is "update" (to the latest version, or the one the machine
is pinned to) or "rollback" (left for the API and the tests), never an image nor a command. The
choices of before, for all machines and for each one, are dropped at start; "at night" for all
turns the switch on.

The order is Kubernetes': eriphe-core's machine first (its request goes to `update-requested` in
the data folder, for the updater; the outcome comes back in `update-status`); a node only once
eriphe-core runs the latest version, in its assignments (`update`, with eriphe-core's version, the
most the node may get), and the node waits for its tasks to end. While a node has a request, the
scheduler treats it as offline: new tasks wait for it. A request not done within a day is dropped.
A machine without updater (installed before, or by hand, as `deploy/nas/compose.yaml`) says so and
gets no request.

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
  the first byte a client sends (a TLS handshake starts with `0x16`). The CLI, Home Assistant, the
  brain's MCP and every other API caller keep their plain `http://…:8800`; a node reaches the same
  address as `https://…:8800`. On the tailnet, `:8800` keeps its Tailscale certificate and asks for
  a node's certificate too.
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
| `run_task` | start a task: prompt, [resources](#resources) (a repository, the user's folders, catalog tools), agent (or the default), node (or let the scheduler choose); the old fields (`repo/ref/branch/dir`, `tools`, `folders`, `identities`) still work |
| `list_folders` | the user's folders: name, machine (online or not), access (`read` or `drop`), where a task finds it |
| `task_status` | state, the agent's last message, the pending permission; `wait` blocks until the task needs you |
| `list_tasks` | recent tasks |
| `answer_task` | allow or deny the pending permission, or reply to the agent's question |
| `stop_task` | stop the agent |

The scheduler takes the requested node if it is online and fits. Otherwise it works in two passes,
like Kubernetes': it **filters** the online nodes that offer the agent's runtime and carry all its
`needs` labels, then **scores** them: +1 per CPU if the agent prefers `cpu`, +1 per GiB of RAM if it
prefers `memory`, +8 per preferred label the node carries, +2 if the agent's image (or the node's
default image) is already pulled, +2 if the node has the agent's runtime unpacked (`agents_ready`),
-4 per active task. The highest score wins, ties by name.

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
3. **built-in asks**, for what reaches a third party (merging, releasing, publishing, `ssh`, `curl
   -X POST`, external MCP tools): only the user may answer, through the CLI; the brain is told to
   relay them and its answer is refused. The exception: a tool of a catalog server with
   `trust_annotations` that says it is read-only (`readOnlyHint`) is the brain's to judge;
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
| `repo` | `owner/name` on GitHub, or a git URL; `branch` (work branch), `base` (start), `dir` | `read`, `write` | cloned into `/work/repo`; [credentials](#credentials-on-demand) for it; one per task |
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

**On a tainted turn** (the brain read external content), `run_task` refuses any `write` or `drop`
entry, tools that may act, a Drive folder (its files server is lent whole for now) and any legacy
`identities`: the brain asks the user and starts the task in the turn of their answer. Reads still go.

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
| `role` | what it is: a card in `assistantRoles` (`brain` today) |
| `display_name` | what the user calls it, as a person (see [its name](#the-assistants-name)); Eriphe for the brain by default, its id for another |
| `agent`, `node` | what it runs on; without an agent, the default one or the first that can run it |
| `instructions` | its standing instructions (`{{user}}` and `{{name}}` replaced); its role's built-in ones by default; `instructions_file` in the file only |
| `tools` | its grant from the catalog, like a task's; its role may add the catalog's free reads |
| `resources` | what it is lent besides its tools (`{kind, ref, access, …}`): kept, lent with the resources work |
| `triggers` | what wakes it: `conversation` (the user's messages and the tasks' events); later `source:<name>` and `schedule`. Its role's by default; one enabled assistant at most has the conversation |
| `disabled` | its task is stopped, and none started |
| `self_url`, `mcp_servers` | the file only (see the brain) |

The status (`GET /v1/assistants`) has its conditions `Running`, `Busy` and `Ready`, its current
task, the agent it runs, its session, its last turn and its last error; the controller keeps its
restarts there too (table `assistant_status`).

One controller keeps every assistant as its spec says: an enabled assistant has exactly one task;
one that fails starts again after 5 s, then doubling up to 5 min, on its session while the resume
works; a change of its agent, instructions, name, node or resources, or of the tools its grant reaches,
restarts it once its turn is over; a disabled or removed one has its task stopped. In
eriphe-core's first minute, it waits for the trusted servers still connecting. What an assistant
gets comes from its role's card, never from its name, as a runtime's from its harness card: its
self tools (eriphe-core's MCP), its free reads, the system network, streaming, the harness tools
allowed in advance, its node profile, its first turn and its approval path. What wakes it and
where its words go come from its triggers' cards.

The brain is the built-in assistant `eriphe`, of role `brain`: the `brain` section of the file is
its spec (read-only like the file's other entries), and Settings → Agents shows it under
Assistants, where its agent and tools are set when the file leaves them out. Other assistants are
added through the API only, for now; a file may also list them under `assistants`. An install from
before moves by itself at start: the brain's `brain_*` keys become its status, the setting
`brain_agent` its workspace agent, and the same agent keeps its running task.

### The assistant's name

Eriphe is the software's name; each user names their own assistant. The brain's `display_name`
(1 to 32 letters, digits, spaces, apostrophes or dashes) is asked by the first-run wizard (an
empty field: a new install chooses), and set
in Settings → Agents → Assistants, with `PUT /v1/assistants/eriphe` (`{"display_name": "Jarvis"}`)
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
`readOnlyHint` when the server is trusted for its annotations (`trust_annotations`: "lecture
libre" in the workspace, the default of the mail and calendar presets), through its proxy like any
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
tasks under way, the open commitments, the last day's episodes, and the last messages between the
user and the brain, the newest kept, within about 8,000 tokens (`brain_context.go`). It is quoted
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
je te préviens » and the answer stays in the conversation. A sentence from no device is an automation's
(the morning brief), marked `Home Assistant`: it waits up to 80 s for the answer it sends on.

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

1. **Connect**: "Se connecter à Google Drive" sends the browser to Google; "Permettre à Eriphe de
   déposer des résultats" asks for the scope that writes. The account comes back connected, and the
   workspace creates the catalog server `fichiers` (image `ghcr.io/alexis-marcel/eriphe-files`,
   `trust_annotations`: its reads are free, `write_file` is the user's to allow).
2. **Choose**: once the service runs, the page browses the account's folders (My Drive, Shared with
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
hasn't verified this app" (the user goes through "Advanced") and is limited to 100 users, which is
acceptable for now; verification (with a security assessment for restricted scopes) is for later.
`drive` lets the token change anything in the Drive: eriphe-files has no call that does
(only creations, only in `Eriphe/` of a `drop` folder). Reconnected read only, every folder falls
back to `read`. `prompt=consent` and `access_type=offline` make Google give a refresh token at each
connection.

### The Eriphe app and the eriphe.com bounce

Google wants a registered https redirect, and each installation has its own address. The Eriphe
app, published once, registers `https://eriphe.com/oauth/callback`; the portal there only sends the
browser on to the workspace named in the state, Home Assistant's way:

```
workspace → Google      PKCE (S256), state = e1.<base64url payload>.<base64url HMAC-SHA256>
Google → eriphe.com     /oauth/callback?code&state
eriphe.com → workspace  <origin>/oauth/callback?code&state   (nothing else; it stores nothing)
workspace               checks the MAC, the expiry (15 min), the flow (used once), the browser's
                        flow cookie, and that the origin is its own; exchanges the code with the
                        client secret and the PKCE verifier
```

The payload is `{"w": "<workspace origin>", "n": "<flow id>", "x": <expiry>}`, the MAC under a key
only this eriphe-core has (`cloud_state_key` in the store, made once): a state it did not make, or
replayed, is refused at its callback. The portal reads the payload but cannot check the MAC (a key
it shared with every eriphe-core would be in a public image): it keeps the open-redirect surface
small instead, with the same rule as `bounceOrigin` here. It sends on only to an origin (no path,
query or credentials) that is https to a private, loopback or Tailscale address (100.64.0.0/10,
fd7a:115c:a1e0::/48), a local name (one label, `.local`, `.lan`, `.home`, `.internal`,
`.home.arpa`, `.localdomain`, `.localhost`) or a tailnet name (`.ts.net`); or http to a loopback or
private address only. It forwards only `code`, `state` (and `error` when Google sends one), at the
fixed path `/oauth/callback`, never exchanges the code and keeps nothing. PKCE makes a code caught
on the way useless without the verifier, which never leaves eriphe-core. A workspace reached at a
public name (a reverse proxy of your own) is not bounced to: it uses its own Google client.

The flow is bound to the browser like the other workspace flows (the `__Host-eriphe-oauth`
cookie, its hash kept with the flow): another browser with the same link is refused.

**Use my own Google client** (advanced): a "Web application" client created in your own Google
Cloud project (Drive API on), with the workspace's own `https://<workspace>/oauth/callback` as its
redirect: the page shows the exact URI. Its id and secret are typed in the form, go to Google and
then, with the refresh token, to the vault; eriphe.com is not involved.

### Where the Eriphe app's client secret lives

In a file given to the installation, never in this repository and never in an image:
`google-oauth.json` next to the database (the data directory: `/var/lib/eriphe/core` on Linux,
`/data` in the container), or the file (or `vault:<name>`) named by `cloud.google_client_file`. It
is the JSON the Google Cloud console downloads (`{"web": {"client_id", "client_secret", …}}`; the
flat `{"client_id", "client_secret"}` also works). Without it, the page says the Eriphe app is not
installed and offers the own-client form.

```json
"cloud": {"google_client_file": "/data/google-oauth.json", "bounce_url": "https://eriphe.com/oauth/callback"}
```

Why not in the image, injected at build from a GitHub Actions secret: the images are public, so
anything in them is public, and Google treats the secret of a "Web application" client as
confidential (only "Desktop" clients have a secret Google does not count as one, and those only
take loopback redirects, which a workspace on another machine than the browser cannot use). A
public secret would let anyone redeem, as "Eriphe", a code that reached them: with a link that
starts a flow they made (their own PKCE verifier) and sends the code through the bounce, a user
who accepts an unsolicited "Eriphe wants to access your Google Drive" would give that person their
Drive. The portal's rule above narrows where a code can be sent, but cannot tell your workspace
from another one on a tailnet (`*.ts.net` names can be made public with Tailscale Funnel). With
the file, the secret is only as known as the installations it was handed to; rotating it (a new
secret in the console, the file replaced) cuts the old one off. If the app later ships in the
image for everyone, that risk is accepted with it: the decision is one line of `googleApp`.

### Creating the Eriphe app in the Google Cloud console (once, by the maintainers)

1. <https://console.cloud.google.com/projectcreate>: a project named `Eriphe`.
2. APIs & Services → Library → **Google Drive API** → Enable.
3. Google Auth Platform (APIs & Services → OAuth consent screen) → Get started: app name
   `Eriphe`, a support email, audience **External**, a contact email; accept the policy; Create.
4. Branding: home page `https://eriphe.com`, privacy policy `https://eriphe.com/…` (a page to
   write before verification), authorized domain `eriphe.com`; the logo can wait (a logo requires
   verification of the branding).
5. Data Access → Add or remove scopes: `https://www.googleapis.com/auth/drive` and
   `https://www.googleapis.com/auth/drive.readonly` (both listed as restricted) → Update → Save.
6. Audience → Publishing status → **Publish app** → confirm: the status is "In production" (it
   says verification is needed: do not submit it yet). In "Testing", refresh tokens expire after
   7 days and only listed test users can sign in.
7. Clients → Create client: type **Web application**, name `Eriphe`, authorized redirect URI
   `https://eriphe.com/oauth/callback` (no JavaScript origin needed) → Create → **Download JSON**
   at once (the secret is shown only at creation).
8. Give that JSON to the installation as `google-oauth.json` in eriphe-core's data directory,
   readable by eriphe-core's user (UID 1993), e.g. on Linux
   `sudo install -o 1993 -g 1993 -m 600 client_secret_*.json /var/lib/eriphe/core/google-oauth.json`,
   or `docker cp client_secret_*.json eriphe-core:/data/google-oauth.json && docker exec -u 0
   eriphe-core chown 1993:1993 /data/google-oauth.json`. No restart is needed: it is read at each
   connection. Never commit it.

The portal's `/oauth/callback` must be deployed (eriphe repository) before the first connection.

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
inline in the conversation (Yes / No, with an optional reason for the agent), and the settings:
devices (what Eriphe sees and does on each, and the command that connects one), agents, accounts and tools (the vault), the folders
given to Eriphe (see [Folders](#folders)), and the journal of decisions. Settings → Tools lists the catalog's servers with their status (ready, to connect,
error, or waiting for a machine for a local one) and where each comes from, adds one from a list of
presets (Home Assistant, GitHub, Linear, Notion, Mails, Agenda, Google Drive, or any online
service or local image; Google Drive has its own page, see [Google Drive](#google-drive-cloud-folders)).
A preset's form asks only what the owner decides: the credential (with the provider's own
steps and link), the address when it differs per person, the name, what the tool is for, and what
Eriphe may do without asking ("Lecture libre", which sets `trust_annotations`, or "Tout me
demander"); the rest (a vendor's address, the authentication, timeouts, an OAuth client, how a
local server runs) is under the advanced options. Mails and Agenda first ask which kind of account
(Gmail, iCloud, Orange, Free, OVHcloud, Fastmail or any IMAP server; iCloud, Fastmail, Nextcloud or
any CalDAV server) and run `eriphe-mail` and `eriphe-calendar` with its password; "Test the
connection" tries that password (IMAP login, CalDAV PROPFIND). Outlook, Microsoft 365, Google
Calendar and Microsoft calendars are shown as coming soon: they wait for eriphe-core to lend OAuth
tokens to local servers. Linear and Notion open a short page (name, use, access), then "Se
connecter à Linear" saves and signs in at once; the tool is added only once authorized. Settings →
Tools also edits and removes the workspace's own tools, and connects again those that use OAuth
(see [OAuth](#oauth-and-eriphe-connect)). A collapsed "Secrets avancés"
section lists what the vault holds and what each secret belongs to, with replace and delete only,
and the configuration's secret files to import. Settings → Agents shows the agents (model, account,
whether a machine can run them now), the agents worth creating with the accounts (one click), the
default agent and the brain's, and the AI accounts (providers). Adding an account starts from a
card per kind, subscriptions first (Claude, ChatGPT) then API keys, each with who pays, its
limits, its models and the steps to get its credential; "Test the key" lists the provider's models
once with the key typed (OpenRouter also tells the credit left; a Claude subscription token is
only checked for its shape, as Anthropic allows it in Claude Code only; a key pasted in the wrong
card is caught), and the usual agents of the kind are created with it (Opus, Sonnet, Haiku for
Claude; GPT models for ChatGPT; two well-known models for each API key). The agent form offers the
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
| `lang` | `fr` (default) or `en`; each browser can switch in Settings |
| `session_days` | how long a login lasts, 30 by default (a week without use ends it sooner) |

**First start.** With no password yet, eriphe-core logs a one-time setup code:
`web: no password yet: open https://…/setup with the setup code ABCD-EFGH`. Open the page, type
the code and choose the password (10 characters at least). The password is stored as an
argon2id hash; the code is no longer accepted once used. While the brain has no agent, the
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
failure, at most 5 s) but is still checked, so the owner gets in from their own address. At most two
argon2 hashes are computed at once. Changing the password ends the other sessions.
The bearer-token API is unchanged for the CLI and the nodes.

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

The manifest (`/manifest.webmanifest`) and a service worker (`/sw.js`, static files only; it
caches no page or data) are in place; push notifications are not yet.

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
   `eriphe.<tailnet>.ts.net` then appears in the public Certificate Transparency logs.
4. **Ready**: the address `https://eriphe.<tailnet>.ts.net`, a QR code for the phone (drawn in Go,
   no outside service), the links to install Tailscale on the phone, and how to add the workspace
   to the home screen.

The page follows the state live (an event stream). The setting is kept in the database and
Tailscale starts again with eriphe-core. On the tailnet, eriphe-core serves:

| Port | What |
|---|---|
| `443` | the workspace, with HSTS, only for its tailnet name (another `Host` is redirected or refused) |
| `8800` | the API (nodes, CLI, MCP), HTTP/1.1, with the same certificate; a node presents its own (see TLS between machines) |

The local listeners do not change. The certificate is fetched and renewed by tsnet on the
handshakes (Let's Encrypt, via Tailscale). The OAuth redirect of a tool connected from the tailnet
address is that address's `/oauth/callback`.

**Key expiry.** A device logged in with the login link must log in again after 180 days (the
tailnet's setting): the page shows the date, and a banner 14 days before with **Log in again**.
To avoid it, give eriphe-core an auth key of a tag (below): tagged devices do not expire.

**Advanced options.** The machine name; the **coordination server** for
[Headscale](https://headscale.net) (its URL; changing it forgets the current identity; Headscale
has no automatic certificates, so HTTPS stays at the HTTPS step unless the server provides them);
an **auth key** (`tskey-auth-…`, or Headscale's pre-auth key), kept in the vault as
`tailscale.authkey` and used at the next login only; **Log out of the tailnet**, which logs the
device out at the coordination server, deletes its state and turns remote access off. **Turn off**
keeps the identity, so turning it on again needs no login.

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
[Dynamic catalog](#dynamic-catalog-workspace)). Settings → Tools → Secrets avancés lists what the
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
`lesson`), a subject (`owner`, `person:<slug>`, `client:<slug>`, `project:<name>`, `home`…),
dates (`valid_from`, `valid_to` once no longer true; `occurred_at` for an episode; `due_at` and
`state` for a commitment), where it comes from (`source_kind`, `source_ref`: `msg:N`, `task:<id>`)
and a trust level (`owner`, `first_party`, `external`). A change over time supersedes the old
memory, which stays, dated; every change is in `memory_log`. Forgotten memories are purged after
30 days.

The brain's tools (on `/mcp`): `remember`, `recall` (any word matches, best first; boosts for the
subject named and the profile; episodes fade with a 30-day half-life; current facts only unless
`include_past` or `as_of`), `update_memory`, `forget`, `commitments`. Results are data, quoted.

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
user is only proposed. Proposals wait in the workspace (Mémoire → À approuver).

**What it knows about the user.** The Memory page opens on "Ce que Jarvis sait de toi" (the
assistant's name): the profile and the other current memories about the user (subject `owner`,
episodes and commitments aside), the pinned ones first and marked, with the cards' usual actions,
and the button that does the [first-run interview](#workspace-web) again.

**The profile** is the pinned memories, at most 2,500 characters (enforced on write). It is
appended to the brain's prompt when its session starts, and sent again, quoted, with its next
messages whenever it changed. Claude Code's own auto-memory is turned off for the brain
(`CLAUDE_CODE_DISABLE_AUTO_MEMORY=1`): one memory.

**Extraction and journal.** Every night at `extract_at` (and once a busy day's conversation has
been quiet `idle_minutes`), eriphe-core writes out the day's transcript (messages and finished
tasks, each labelled with its trust) and starts a `memory` task (not listed, never the brain's)
with a cheap agent and the memories it may touch. Its JSON answer (ADD, SUPERSEDE, NOOP, each
citing a `source_ref` of the transcript) is checked and applied: episodes (the journal, one per
conversation day and per finished task) and what comes from the user's words are active; what
comes from tasks or external content, and preferences, are proposed; it never writes a profile
or a lesson. One run per day and window; a failure is retried the next night. The same night
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
