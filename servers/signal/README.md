# eriphe-signal

Eriphe's own Signal account, for eriphe-core's Signal channel. Signal has no bots, so Eriphe gets
an account of its own, with a number of its own (a landline works: Signal verifies it by a voice
call). [signal-cli](https://github.com/AsamK/signal-cli) holds that account. This server runs it
in JSON-RPC mode on its stdin and stdout, in multi-account mode, and serves it to eriphe-core
through the tools below.

Every tool is a setup tool (`_meta: {"eriphe/setup": true}`, [servers/README.md](../README.md)), so
the pool leaves them out of the server's tools. No task and not the brain can send from Eriphe's
number. eriphe-core alone registers the number and talks through it.

## Tools

| Tool | Arguments | Answer |
|---|---|---|
| `status` | none | `{number, version}`: the number registered (none before), signal-cli's version |
| `register` | `number` (`+33123456789`), `captcha`, `voice` | `{ok, error, captcha}`: Signal sends a code by SMS, or by a call with `voice`. `captcha: true` when Signal asks for one first: the user solves it at `signalcaptchas.org/registration/generate.html` and gives the `signalcaptcha://…` link |
| `verify` | `number`, `code`, `pin` | `{ok, error}`: the account is registered. signal-cli starts again, to receive with it |
| `set_pin` | `pin` (empty: removed) | `{ok, error}`: the registration lock, so nobody else can register the number |
| `remove` | none | `{ok, error}`: unregistered from Signal, its data deleted here |
| `send` | `to` (a number or an ACI), `text` | `{timestamp}`: the message's, which a reaction to it names |
| `receive` | `after`, `wait` (up to 50 s), `max` (100) | `{messages, last}`: what came after `after`, waiting up to `wait` when nothing did. Give `last` as `after` next |

One number at a time: `register` refuses another while one is registered. `register`, `verify`,
`set_pin` and `remove` run one at a time, because signal-cli's account files do not take two at
once.

A message is `{seq, from, number, name, group, text, attachment, reaction, timestamp}`. `from` is
the sender's ACI (UUID), or their number without one. `group` marks a message sent in a group.
`attachment` covers a file, a sticker or a voice note, whose content is not fetched. `reaction` is
`{emoji, target, remove}`, where `target` is the timestamp of the message reacted to. Only what
someone sent is kept: sync messages (the account's other devices), receipts, typing notes, profile
keys and timers are not. The last 500 are kept in memory. Their `seq` starts from the clock at each
start, so a reader that gives a `last` from a previous run gets everything kept.

## Running

| Env | Default | |
|---|---|---|
| `SIGNAL_DATA` | `/data` | where signal-cli keeps the account and its keys (a volume) |
| `SIGNAL_CLI` | `signal-cli` | the binary |
| `MCP_TRANSPORT` | `stdio` | or `http` (`MCP_ADDR`, `MCP_TOKEN`), as the other servers |

signal-cli that stops is started again, after a wait that doubles from 1 s to 1 min. A request
waiting at that moment fails.

## Image

The image is `gcr.io/distroless/cc-debian12:nonroot` with:

- signal-cli's native build (GraalVM, no Java);
- zlib, which that build needs;
- libgcc, which libsignal's library needs. signal-cli unpacks that library in `/tmp`, the node's
  tmpfs.

It runs read-only, non-root, with every capability dropped. Only `/data` is written.

**The native build is x86_64 only.** The arm64 image has no signal-cli, and every tool then says so
at once. signal-cli's Java build would need libsignal's library built for arm64.

**signal-cli must be kept recent.** Signal stops accepting clients a few months old. To update it,
bump `SIGNAL_CLI_VERSION` and `SIGNAL_CLI_SHA256` in the Dockerfile together, from the release's
`signal-cli-<version>-Linux-native.tar.gz`. The build checks the checksum.

## Tests

The tests run on a fake signal-cli: none reaches Signal. The image was checked by hand on amd64,
read-only, non-root and without a network: `status` and `receive` answer, and `register` reaches
signal-cli's registration. **Not yet tried with a real number.**
