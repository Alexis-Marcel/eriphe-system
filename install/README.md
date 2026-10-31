# Installing Eriphe

> **Not a developer?** Start with the README's [Get started](../README.md#get-started): which
> machine to install on, and the five steps after. Installing with an AI assistant: hand it
> [docs/install-with-an-ai.md](../docs/install-with-an-ai.md). This page is the full reference.

One command installs Eriphe on a machine, and brings its own container runtime: nothing to install
first, not even Docker. `install.sh` covers Linux and macOS, `install.ps1` Windows. Both come from
this folder and are attached to every [GitHub release](https://github.com/Alexis-Marcel/eriphe-system/releases),
so the latest release's are always at:

- `https://github.com/Alexis-Marcel/eriphe-system/releases/latest/download/install.sh`
- `https://github.com/Alexis-Marcel/eriphe-system/releases/latest/download/install.ps1`

GitHub answers these with a redirect to the asset, which `curl -L` (in `-fsSL`) and `irm` follow.
The scripts of one release, e.g. to install it exactly as it was, are at
`https://github.com/Alexis-Marcel/eriphe-system/releases/download/v0.2.0/install.sh` (and
`install.ps1`). `https://eriphe.com/install.sh` and `https://eriphe.com/install.ps1` are only short
aliases that redirect to the same GitHub addresses (the portal is maintained elsewhere); nothing
here depends on them.

Eriphe is two programs:

- **eriphe-core**, the brain and the workspace (`https://<address>:8443`), on one machine that
  stays on: a NAS, a server, a desktop that does not sleep.
- **eriphe-node**, on every machine Eriphe's agents work on, eriphe-core's own included. A node
  dials eriphe-core and needs no open port.

## Contents

- [What it needs](#what-it-needs)
- [1. The first machine](#1-the-first-machine)
  - [One container (NAS, any Linux with Docker)](#one-container-nas-any-linux-with-docker)
- [2. Open the workspace](#2-open-the-workspace)
- [3. Connect the brain](#3-connect-the-brain)
- [4. Connect a device](#4-connect-a-device)
- [5. Connect Google Drive (optional)](#5-connect-google-drive-optional)
- [6. Connect GitHub (optional)](#6-connect-github-optional)
- [Update](#update)
  - [The machines update themselves](#the-machines-update-themselves)
- [Uninstall](#uninstall)
- [Options](#options)
- [What it installs](#what-it-installs)
- [Folders](#folders)
- [A model behind this machine](#a-model-behind-this-machine)
- [What the VM can and cannot see](#what-the-vm-can-and-cannot-see)
- [Windows](#windows)
- [Eriphe on a VPS](#eriphe-on-a-vps)
- [Troubleshooting](#troubleshooting)

## What it needs

- **Linux**: Debian, Ubuntu, Raspberry Pi OS, Fedora, RHEL, CentOS, Alpine, Arch or openSUSE, on
  amd64 or arm64, with `curl` and root (through `sudo`). Docker is installed if missing.
- **macOS** 13.5 or later, Apple silicon or Intel. No Docker Desktop, no Homebrew. On Apple silicon
  with macOS 26 or later, Eriphe runs on the Mac itself with Apple's `container` (installed if
  missing) and macOS asks for your password (for that package, and for the LaunchDaemon a node
  joining another machine gets); elsewhere in a Lima VM (see [What it installs](#what-it-installs)).
- **Windows** 10 21H2 or later, or 11, Home or Pro, with virtualization on in the firmware, and an
  administrator PowerShell. WSL is installed if missing.
- A few GB of disk for the images and what tasks install, and an account with an AI provider: a
  Claude subscription is the simplest (or ChatGPT, or an API key).

**What each machine downloads.** The installer pulls eriphe-node (and eriphe-core in core mode)
and the base image tasks start from, `eriphe-base` (114 MB). Then each node downloads, in the
background, the agents eriphe-core has configured, and only those: Claude Code 127 MB, Codex 112
MB, OpenCode 77 MB (amd64; about the same on arm64). With Claude Code alone, 241 MB for the tasks
instead of 633 MB with the former `eriphe-worker`. What a task installs (apt packages, Python and
npm packages) is kept in the machine's cache for the next one ([worker/README.md](../worker/README.md)).

## 1. The first machine

The first machine runs eriphe-core, with a node of its own (core mode).

Linux or macOS:

```sh
curl -fsSL https://github.com/Alexis-Marcel/eriphe-system/releases/latest/download/install.sh | sh -s -- --core-mode
```

Windows, in an administrator PowerShell:

```powershell
$env:ERIPHE_CORE_MODE='1'; irm https://github.com/Alexis-Marcel/eriphe-system/releases/latest/download/install.ps1 | iex
```

The first install downloads the images (and on a Mac in Lima's VM an Ubuntu image of about 600 MB),
so it takes a few minutes. It ends with the workspace's address on the LAN and a setup code:

```
Installing Eriphe on this computer
It takes a few minutes the first time.

This computer will ask for your password: it installs Eriphe for
the whole computer.
[sudo] password for you:

  ✓ Downloaded
  ✓ Installed
  ✓ Started

Done: Eriphe runs on this computer. To finish, open this address
in your browser and give the setup code:

  https://192.0.2.10:8443/setup
  Setup code: ABCD-EFGH

Good to know: to connect your other computers, open Réglages ›
Appareils › Connecter un appareil (Settings › Devices › Connect a
device) in your browser.

Logs: docker logs eriphe-core, docker logs eriphe-node
```

The workspace speaks the browser's language (French, or English for any other), so the installers
name its menus in both. The setup code stays the same until it is used, and every run of the
installer prints it again while there is no password; once there is one, the installer says to
sign in instead, with the command for a [forgotten password](#forgotten-password).

**Another port.** Core mode needs ports 8443 (the workspace) and 8800 (the API, for the other
devices). The installer checks them before it installs anything and says which program holds one.
Choose others with `--web-port 9443 --api-port 9800` (`-WebPort 9443 -ApiPort 9800` on Windows, or
`$env:ERIPHE_WEB_PORT='9443'; $env:ERIPHE_API_PORT='9800'` before `irm | iex`); they are kept for
the next runs, and the commands of "Connect a device" give the API's port.

**IPv4, every address.** On Linux (and the one container) eriphe-core's two ports are published on
every IPv4 address of the machine (`0.0.0.0`), not on IPv6: on `[::]`, Docker's userland proxy
would hand eriphe-core every IPv6 client with the address of Docker's bridge instead of its own,
and eriphe-core could no longer tell the local network from the Internet, nor one client from
another for its sign-in limits. `--bind 127.0.0.1` (or one address of the machine; `ERIPHE_BIND`
for the one container) narrows them further, for a workspace reached only through Tailscale or a
reverse proxy on the machine. On a server with a public address the installer says so: read
[Eriphe on a VPS](#eriphe-on-a-vps).

**Time zone.** A new install gives eriphe-core this machine's time zone (`timedatectl`,
`/etc/timezone` or `/etc/localtime`; on a Mac, the Mac's), kept in `install.conf` for the next
runs; `… | TZ=America/Montreal sh -s -- --core-mode` chooses another. An install made before keeps the zone it
had (Europe/Paris unless `TZ` was given).

The installers show only that: a title, short steps, the outcome, what is good to know, and where
the logs are. A password (`sudo`, macOS's) is asked once, after a sentence saying why. Everything
else, the output of each command included, goes to the install's log: `~/.eriphe/install.log` on a
Mac, `/var/log/eriphe-install.log` on Linux (`~/.eriphe-install.log` without root),
`C:\ProgramData\eriphe-node\install.log` on Windows. When something fails, the installer says what
in a sentence, what to do, and points at that log; the exit code is 1.

### One container (NAS, any Linux with Docker)

On a NAS (Unraid, Synology, TrueNAS, a Raspberry Pi with Docker…) or any Linux where Docker
already runs, Eriphe also installs by starting one container, `ghcr.io/alexis-marcel/eriphe`
(amd64, arm64), with the Docker socket and one data folder:

```sh
docker run -d --name eriphe --restart unless-stopped \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v /srv/eriphe:/data \
  -e ERIPHE_SHARES=documents=/srv/documents \
  ghcr.io/alexis-marcel/eriphe
docker logs -f eriphe
```

`/srv/eriphe` is Eriphe's data folder, any folder of the host (on Unraid `/mnt/user/appdata/eriphe`,
with `ERIPHE_SHARES=shares=/mnt/user`; on Synology for instance `/volume1/docker/eriphe`). On Unraid, the template `unraid/eriphe.xml` does the same from the Docker page
([unraid/README.md](../unraid/README.md)).

This container is the **launcher** (`install/eriphe-launcher`). It makes and keeps running the same
containers as `install.sh` on a Linux host, with `install.sh`'s own functions: `eriphe-core` (no
Docker access), `eriphe-node` (Docker only through the socket proxy), `eriphe-docker-proxy`, and
the networks `eriphe-system`, `eriphe-tasks`, `eriphe-docker` (see [What it
installs](#what-it-installs)). Core and node stay separate containers. The launcher itself listens on
no port, is on no network of Eriphe's (tasks cannot reach it), and runs nothing else: it starts
them, then waits for update requests. It does not even need a network of its own (`--network
none` works).

- **Data.** Everything lives in the folder mounted at `/data`: `core/` (eriphe-core's data, owned
  by UID 1993), `node/` (the node's root, `node.json` its identity), `launcher/` (the compose
  project, `install.conf`, the updater), `update/` (the updater's log and the previous versions).
  The other containers need the folder's path on the host for their bind mounts: the launcher reads
  it from its own container, through the socket (`docker inspect`, the source of `/data`). A named
  volume works too.
- **First start.** It pulls the images, starts eriphe-core, joins its node to it (with the join
  token and the CA's hash eriphe-core gives), and its log (`docker logs eriphe`) ends as
  `install.sh`'s: the workspace's address and the setup code, then the `/welcome` wizard ([Open
  the workspace](#2-open-the-workspace)). Later starts pull only what is missing: a restart
  never updates anything.
- **Updates.** The machine shows as self-updating in Settings → Devices ("Linux (Eriphe
  container)"): its **Update** works as on Linux. eriphe-core and the node drop their request in
  their data folders; the launcher's loop (every 5 s) runs `install.sh`'s updater on it (pull,
  eriphe-core first and checked, then the node, the previous images back if a check fails, a node
  never newer than eriphe-core), then updates **itself, last**: it pulls `eriphe` of the build the
  machine now runs (`0.9.0`, or `sha-<commit>` on edge), tags it under its own reference, and a
  short-lived container of the new image (`eriphe-launcher-swap`, no network) replaces the
  launcher's container by one with the same settings (puts the old one back if the new one does not
  stay up). By hand: `docker exec eriphe eriphe-launcher update`, or `rollback`.
- **Uninstall.** `docker exec eriphe eriphe-launcher uninstall` removes the containers it made, the
  task and service containers, the networks, the `eriphe-cache` volume and Eriphe's images; the
  data folder stays (`uninstall --data` empties it too). It then stops the launcher and keeps it
  from starting again (a mark, `launcher/uninstalled`): remove it with `docker rm -f eriphe` (on
  Unraid, Docker → eriphe → Remove), and its image.

Its settings are environment variables, read at each start (not kept from the start before, but
for the node's name once joined):

| Variable | Default | What |
|---|---|---|
| `ERIPHE_MODE` | `core` | `core`: eriphe-core here, with a node of its own; `node`: join another eriphe-core |
| `ERIPHE_SHARES` | none | places whose folders may be given to Eriphe, `NAME=/PATH[:ro]` comma-separated, host paths (`shares=/mnt/user`), as `--share` ([Folders](#folders)); each is checked on the host by a short-lived container |
| `ERIPHE_WEB_PORT` | `8443` | the workspace's port on the host |
| `ERIPHE_API_PORT` | `8800` | eriphe-core's port on the host, for the other devices (the join commands give it) |
| `ERIPHE_BIND` | every IPv4 address | the host's IPv4 address the two ports listen on, as `--bind` (`127.0.0.1`: only through Tailscale or a reverse proxy on the host) |
| `ERIPHE_VERSION` | the image's tag | `latest` (the releases), `edge` (main), or a release `0.8.0` / `0.8`, as `--version`; `ghcr.io/alexis-marcel/eriphe:edge` follows main by itself |
| `ERIPHE_NODE_NAME` | the Docker host's name | the node's name |
| `ERIPHE_NODE_LABELS` | none | as `--labels` |
| `ERIPHE_MODEL_SERVER` | none | as `--model-server`: `http://eriphe-ollama:11434` for an Ollama container on `eriphe-system` ([A model behind this machine](#a-model-behind-this-machine)) |
| `ERIPHE_CORE`, `ERIPHE_JOIN_TOKEN`, `ERIPHE_CA_HASH` | | node mode: from "Connect a device" (the token only matters at the first start) |
| `ERIPHE_ADDRESS` | found | the host's address, for the log only |
| `TZ` | | eriphe-core's time zone (Unraid gives the server's; without it, Europe/Paris) |

A data folder installed in one mode stays in it: another mode needs another folder, or
`uninstall --data` first. `install.sh` stays the way on a Linux machine without Docker (it
installs it), on macOS and on Windows.

## 2. Open the workspace

Open the address in a browser, from this machine or any other on the LAN. The workspace has its own
self-signed certificate, so the browser warns the first time: accept it (its SHA-256 fingerprint is
in eriphe-core's log, `web: new self-signed certificate … SHA-256 fingerprint …`). On the setup
page, the setup code creates the owner's password; it works once. Until then it stays the same,
across restarts, and `eriphe-core auth setup-code` prints it (`docker exec eriphe-core eriphe-core
auth setup-code` in core mode on Linux, Windows' WSL or the one container).

## 3. Connect the brain

A short wizard (`/welcome`) follows. First, name your assistant: "Comment veux-tu appeler ton
assistant ?" (What do you want to call your assistant?). Eriphe is the software's name; your
assistant has the one you choose (1 to 32 letters, digits, spaces, apostrophes or dashes). It
introduces itself by it, and the workspace, its tab title, the installed app and Home Assistant
call it so. You can change it later in Settings →
Agents. Then pick the AI account it thinks with, among all kinds shown side by side: a
subscription (Claude: run `claude setup-token` on any computer where Claude Code is installed and
paste the token into the workspace, and nowhere else: not in a conversation with an AI, which
should never run that command for you either; ChatGPT) or an API key (Anthropic, OpenAI, Google, Mistral, OpenRouter, or a custom
agent). Eriphe creates the account's usual agents, and your assistant starts on this machine (the
install already joined it as a device). Agents, tools and accounts are in Settings later.

Nothing else is on by default: no tool, no rule, no remote access. The catalog's tools (GitHub,
mail, calendar, Home Assistant…) are added from Settings → Tools.

## 4. Connect a device

Connecting a device gives Eriphe access to it, so it can help with what is there: it sees only the
folders you open to it (Settings → Folders), and puts agents to work there when the full install
is possible, which is the default.

In the workspace, Settings → Devices → "Connect a device": give it a short name (lowercase letters,
digits, dashes) and, if you like, labels (`gpu`). The workspace prints the command with everything
filled in: eriphe-core's address, a join token (single use, for this name, valid 24 h) and the
fingerprint of eriphe-core's certificate authority. Run it on the new machine:

```sh
curl -fsSL https://github.com/Alexis-Marcel/eriphe-system/releases/latest/download/install.sh | sh -s -- --core https://192.0.2.10:8800 --join-token <token> --name laptop --ca-hash sha256:<hex>
```

```powershell
$env:ERIPHE_CORE='https://192.0.2.10:8800'; $env:ERIPHE_JOIN_TOKEN='<token>'; $env:ERIPHE_NODE_NAME='pc'; $env:ERIPHE_CA_HASH='sha256:<hex>'; irm https://github.com/Alexis-Marcel/eriphe-system/releases/latest/download/install.ps1 | iex
```

Once you have named your assistant, the command also gives its name (`--assistant-name 'Jarvis'`,
`ERIPHE_ASSISTANT_NAME`), for what the installer prints: "Connecting this Mac to Jarvis". A name
with an apostrophe is left out, and the installer says Eriphe.

The device then shows on Settings → Devices, with what Eriphe sees and can do there, and its
eriphe-node version. Its page (the "⋯" menu, "Details and log") keeps its access log: the folders
listed, opened or lent to a task (with the right), the tasks run there, the results dropped in a
folder's Eriphe/, for 90 days. "Cut access" in the same menu stops at once what Eriphe does there:
its tasks stop, no work goes to it, its folders are neither listed nor lent, and its connection is
refused until "Restore access". Nothing is uninstalled. Open the workspace with
the machine's LAN address, not `localhost`, before you generate the command: the address in it is
the one the browser used (or `public_url`), and `localhost` would only work on eriphe-core's own
machine.

**Waking a device.** On a device's page, "Eriphe may wake this device" (off by default) lets Eriphe
send it a Wake-on-LAN signal when a task needs it (its folders are there) and it is asleep, and
gives a "Wake now" button that says whether it came back within 2 minutes. The installers record
the device's network card for it (on Windows, the PC's own card, not WSL's). It works only on the
home network, and only once the device accepts it: on a PC, Wake-on-LAN in the BIOS/UEFI and on
the network adapter (Device Manager → the adapter → Power Management, and "Wake on Magic Packet"
under Advanced), wired Ethernet rather than Wi-Fi, and Windows Fast Startup off to wake it from a
full shutdown; on a Mac, System Settings → Energy → "Wake for network access", which wakes it from
sleep, not from a full shutdown. The signal is sent by the machine that stays on (the NAS), from
the host's network. A device installed before this option reports its card after running its
install command again.

Under "Advanced options" the workspace also gives the same settings for a node in an existing
Docker Compose project (`ERIPHE_*` variables, as in `deploy/nas/compose.yaml`), for the eriphe-node
program alone, and for a downloaded `install.ps1`.

Without the workspace, on eriphe-core's machine:

```sh
docker exec eriphe-core eriphe-core join-token -data /data -name laptop          # Linux
wsl -d eriphe -- docker exec eriphe-core eriphe-core join-token -name pc         # Windows
```

A device that was reinstalled, or lost its identity, keeps its name with "Re-register this
device" on its card: that token lets it take its name over (on Windows, add `-Rejoin`).

## 5. Connect Google Drive (optional)

To give Eriphe folders of a Google Drive: Settings → Tools → Add a tool → **Google Drive**.

1. Create your own Google app, once: the page lists the steps in the Google Cloud console and shows
   the exact redirect URI to paste (`https://<workspace>/oauth/callback`), with a Copy button; then
   give it the JSON the console downloads (or the client ID and secret). The same steps, with
   Google's rules and sources: [docs/google-drive.md](../docs/google-drive.md).
2. "Connecter Google Drive" opens Google's page. Tick "Permettre à Eriphe de déposer des
   résultats" first if Eriphe may add files (always in an `Eriphe` subfolder of the folders you
   allow; it never changes or deletes yours); without it, Eriphe only reads. Google says "Google
   hasn't verified this app": it is your own app. Click "Advanced", then "Go to Eriphe".
3. Back in the workspace, wait a few seconds for the Files server to start on a machine, then open
   the folders and give each one "en lecture" or "lecture + dépôt". Eriphe sees only those folders
   and what is inside them.

Google only sends back to an https address whose name ends in a public domain: a workspace opened
at an IP address (`https://192.168.1.2:8443`) or at `eriphe.local` cannot connect Drive. Turn on
Remote access in Settings (an `https://<name>.<tailnet>.ts.net` address) or use your own
domain; or, with nothing to set up, install "Google Drive for desktop" on an always-on machine and
give its Google Drive folder in [Folders](#folders).

## 6. Connect GitHub (optional)

For tasks that clone private repositories, push branches or open pull requests, and for the
GitHub tool: Settings → Tools → GitHub → **Créer l'app GitHub d'Eriphe**.

1. Choose the end of the app's name ("Eriphe – <name>", unique on GitHub) and, if the app should
   belong to an organization, its name. Click "Créer l'app GitHub d'Eriphe", then "Continuer sur
   GitHub".
2. On GitHub, check the name and click **Create GitHub App** for your account (or the
   organization). GitHub sends you back to the workspace: the app is created and kept in Eriphe's
   vault.
3. Click **Installer sur des dépôts**, choose "Only select repositories" and the repositories
   Eriphe may work on, then **Install**. GitHub sends you back to the page, which shows the
   installation.
4. Optional: **Brancher l'outil GitHub sur l'app**, so the GitHub tool (issues, pull requests, code
   search) uses the same app instead of a personal token.

Then a task lent the GitHub tool asks for its access when it needs it: one hour, the task's
repository only, read freely, and the first push waits for your approval in the workspace. The
task's details list every access it asked for. Without the app, a task gets the GitHub tool's
token instead, which lasts and is not limited to one repository: the page warns about it.

The app needs no public address: GitHub sends your browser back to the workspace at the address
you opened it with. Nothing reaches eriphe-core from GitHub (no webhook).

## Update

Run the same command again, or the script with no option:

```sh
curl -fsSL https://github.com/Alexis-Marcel/eriphe-system/releases/latest/download/install.sh | sh -s
```

```powershell
irm https://github.com/Alexis-Marcel/eriphe-system/releases/latest/download/install.ps1 | iex
```

It pulls the images again and recreates what changed; the data and the node's identity
(`node.json`) stay, and the join token is not needed any more (it was single use, and is never
written to disk). What is not given again is kept (address, name, labels, mode); given again,
`--labels`, `--core` (e.g. to add a second address) and `--ca-hash` replace the saved ones, and a
joined node keeps its name. Changing between node and core mode needs an uninstall first.

The images follow the releases ([RELEASING.md](../RELEASING.md)): `latest` is the latest one (also
under `0.2.0` and `0.2`), and `edge` is the latest build of `main` (also under `sha-<commit>`). The
scripts at `releases/latest/download/` are the ones of the latest release too: a change to
`install/` on `main` reaches them only with a release. eriphe-core shows its version in Settings → My workspace (`0.2.0`), and each
machine its eriphe-node version on its card. A few times a day it reads which version `latest` is on
the registry: when a newer release is out, the
workspace says "New version available" there, with the command that updates that machine (the
installers tell eriphe-core and the node how they installed them, in `ERIPHE_INSTALL`; a node
without it guesses: Windows in WSL, Linux on a Linux host, a compose project in a container; a compose
project of your own gets `docker compose pull && docker compose up -d`). If an
image cannot be pulled (offline) and a copy of it is already there, the script says so and goes on
with that copy.

### The machines update themselves

Once installed, a machine updates itself when eriphe-core asks, like Tailscale: Settings → Devices.
The workspace says when a machine is behind ("Update available"), with an **Update** button on
that machine, **Update all** when several are behind, and one switch for all machines, **Update
automatically at night** (off by default): between 3:00 and 5:00 (your time zone), once a night.
A machine without an updater (a compose project of your own) shows the one command to run on it
instead. To go back to the previous version or stay on one, run the install command again with
`--rollback` or `--version <version>` (below). The order is
Kubernetes': eriphe-core's machine first; a node only once eriphe-core runs the latest version (a
node is never newer than eriphe-core), and only once none of its tasks is at work (an idle one,
the brain between two turns included, resumes after the restart). While a node has a request, no
new task starts on it: the tasks wait for it.

eriphe-core never names an image nor a command: it can only ask "update" (to the latest version,
or the pinned one) or "rollback". The images are the ones the installer wrote down
(`ghcr.io/alexis-marcel/eriphe-core`, `eriphe-node`, `eriphe-base`; the node downloads the agents
of its own version itself, see node/README.md, Agents). Neither eriphe-core nor the
node gets any new right (the socket proxy allows the node exactly what it did, see
deploy/README.md): each only writes a short file in its own data folder. An **updater**, which the
installer puts on the host side, does the rest, as root there:

| | the updater | runs |
|---|---|---|
| Linux | `/opt/eriphe/update.sh`, run by `eriphe-update.service` (systemd) | when a request is dropped (`eriphe-update.path`), and every 10 minutes (`eriphe-update.timer`) |
| macOS with Apple's container | `~/.eriphe/update.sh`, run by the LaunchAgent `org.eriphe.update`: the new `eriphe-node` (and `eriphe-core`) taken from their images, restarted and checked, the previous ones kept in `~/.eriphe/update/previous.*` | when a request is dropped (`WatchPaths`), and every 10 minutes |
| macOS in Lima's VM | the same as Linux, in the VM (Ubuntu, systemd): nothing more on the Mac | the same |
| Windows | `/usr/local/bin/eriphe-update` in the distribution | every 20 s, a loop that the scheduled task starts with eriphe-node |
| One container | `install.sh`'s updater, in `/data/launcher/update.sh`, run by the launcher, which then updates itself ([One container](#one-container-nas-any-linux-with-docker)) | when a request is dropped (it looks every 5 s) |

How a request goes:

1. eriphe-core writes it: for its own machine, `update-requested` in its data folder
   (`/var/lib/eriphe/core`); for a node, in the node's assignments. The node writes it in its root
   (`/var/lib/eriphe/node/update-requested`) once no task is at work. A request is `id`, `action`
   (`update` or `rollback`), `core` (eriphe-core's version) and `auto` (from the night); the
   updater reads these four values, checks them, and never runs the file.
2. The updater pulls the images: eriphe-core at `latest`, `edge` or the pinned release; the node
   and base image of that same build (the tag of eriphe-core's version: `0.2.0`, or `sha-<commit>`
   for a build of `main`), not their own `latest`, which may be half published (a tag not there
   yet fails the update, and a later one takes it). A node alone takes eriphe-core's build the
   same way, unless it is pinned to a release; a pinned release newer than eriphe-core's is
   refused ("refused": a node is never newer than eriphe-core). Nothing new: "current".
3. It recreates the containers, eriphe-core first, and checks them: eriphe-core answers on
   `/health`, eriphe-node says `connected to` again, within 3 minutes (Windows: the binary is
   swapped and eriphe-node restarted; eriphe-core's container recreated).
4. If a check fails, it puts the previous images (or binary) back, recreates and checks again:
   "rolled_back", or "rollback_failed" if even that does not come back. That version is noted as
   failed: the night skips it, "Update now" does not.
5. It writes the outcome next to the request (`update-status`), which the node reports and
   eriphe-core shows on the machine's line, with the versions before and after. Its log is
   `/var/lib/eriphe/update/update.log` (Windows: `/var/lib/eriphe-update/update.log`).

The previous images stay, tagged `eriphe-previous-core` or `eriphe-previous-node`: "Go back to the
previous version", or `--rollback` (`-Rollback`) on the machine, puts them back, checks the same
way, and keeps the replaced ones to go forward again.

**A version of your choice.** By default a machine follows `latest`, the releases.

- `--version 0.2.0` (`-Version 0.2.0`) installs that release and stays on it: an update then says
  "pinned", and the night leaves it alone. `--version 0.2` stays on the `0.2` line and takes its
  fixes (`0.2.1`, …) when it updates.
- `--version edge` follows `main`, for testers: each update takes its latest build. A build of
  `main` is never told "New version available" (it is ahead, or someone is testing it): update it
  with "Update now", or by running the command again. A node joined to it follows its build.
- `--version latest` follows the releases again.

The rollback works the same on all of them. The releases are on
[GitHub](https://github.com/Alexis-Marcel/eriphe-system/releases), the images on
[GHCR](https://github.com/Alexis-Marcel?tab=packages).

**Installed before this.** A machine installed before the updaters gets one by running its install
command once more (the same one, or `curl -fsSL https://github.com/Alexis-Marcel/eriphe-system/releases/latest/download/install.sh | sh -s`, or
`irm https://github.com/Alexis-Marcel/eriphe-system/releases/latest/download/install.ps1 | iex`); until then its line says it cannot update itself.
Without systemd (Alpine, OpenRC), the script says so and the machine updates only by running the
command again. A compose project of your own, as `deploy/nas/compose.yaml`, has no updater: keep it
on "Never", and update it with `docker compose pull && docker compose up -d`.

## Uninstall

```sh
curl -fsSL https://github.com/Alexis-Marcel/eriphe-system/releases/latest/download/install.sh | sh -s -- --uninstall
```

```powershell
$env:ERIPHE_UNINSTALL='1'; irm https://github.com/Alexis-Marcel/eriphe-system/releases/latest/download/install.ps1 | iex
```

- Linux: the compose project and its containers, the task and service containers, the networks,
  the `eriphe-cache` volume, Eriphe's images (every tag), the updater's systemd units,
  `/opt/eriphe`, `/var/lib/eriphe` and the install's log (`/var/log/eriphe-install.log`; the
  uninstall writes its own in `/tmp`). Docker Engine too, with its `docker` group, if the script
  installed it and no other container is left; otherwise it stays, the uninstall names the
  containers that keep it, and the mark that Eriphe installed it stays too
  (`/opt/eriphe.docker-installed`), so that `--uninstall` removes Docker once they are gone (and a
  new install takes it back).
- macOS with Apple's container: the LaunchAgents `org.eriphe.node`, `org.eriphe.core` and
  `org.eriphe.update`, the LaunchDaemon `org.eriphe.relay` with its binary, socket folder and log
  (with `sudo`), the task, service and explorer containers and their networks, Eriphe's images,
  and `~/.eriphe`. Apple's `container` too if the script installed it (it notes so in
  `~/.eriphe/container-installed`) and no other container is left; otherwise it stays.
- macOS in Lima's VM: the LaunchAgent, the VM and its disk, `~/.eriphe` (Lima included), and
  Lima's download cache (`~/Library/Caches/lima`) unless another Lima is installed.
- One container: `docker exec eriphe eriphe-launcher uninstall [--data]`, then `docker rm -f
  eriphe` ([One container](#one-container-nas-any-linux-with-docker)).
- Windows: the distribution and its disk, its scheduled task, and in core mode the port forwarding
  and firewall rule.

In core mode this deletes eriphe-core's data (`/var/lib/eriphe/core`; `~/.eriphe/core` on a Mac
with Apple's container, in the VM with Lima, in the distribution on Windows): copy it out first to
keep it.

The node stays known to eriphe-core: "Forget" it on its card, or `DELETE /v1/nodes/{name}`.

## Options

| `install.sh` | `install.ps1` | What |
|---|---|---|
| `--core URL[,URL2]` | `-Core '<url>[,<url2>]'` | the eriphe-core to join; several addresses, preferred first (see node/README.md, Join) |
| `--join-token TOKEN` | `-JoinToken <token>` | from "Connect a device", or `eriphe-core join-token -name NAME`; first install only |
| `--name NAME` | `-Name <name>` | the node's name (default: the host name, lowercased) |
| `--labels gpu,fast` | `-Labels 'gpu,fast'` | what agents can require of this node |
| `--ca-hash sha256:<hex>` | `-CaHash sha256:<hex>` | eriphe-core's CA, to check its certificate (`ERIPHE_CA_HASH` of the node) |
| `--core-mode` | `-CoreMode` | run eriphe-core here, and a local node joined to it |
| `--web-port 9443` | `-WebPort 9443` | core mode: the workspace's port (default 8443; env `ERIPHE_WEB_PORT`), kept for the next runs |
| `--api-port 9800` | `-ApiPort 9800` | core mode: the port other devices join on (default 8800; env `ERIPHE_API_PORT`), kept; "Connect a device" gives it |
| `--bind 127.0.0.1` | | core mode on Linux: the IPv4 address of the machine both ports listen on (default every IPv4 address, `0.0.0.0`; env `ERIPHE_BIND`), kept; `--bind 0.0.0.0` goes back to every address ([Eriphe on a VPS](#eriphe-on-a-vps)) |
| `--assistant-name NAME` | `-AssistantName <name>` | the assistant's name, only for what the script prints (default: Eriphe) |
| `--version 0.2.0` | `-Version 0.2.0` | pin that release (`0.2`: its line); `edge` follows `main`; `latest` the releases again (see [the machines update themselves](#the-machines-update-themselves)) |
| `--rollback` | `-Rollback` | go back to the version before the last update |
| `--share NAME=/PATH[:ro]` | | a place of this machine whose folders may be given to Eriphe, repeatable (see [Folders](#folders)); Linux, and a Mac with Apple's container (which shares its home, iCloud Drive and its cloud folders by itself) |
| `--no-share` | | share no place any more |
| `--model-server URL` | `-ModelServer <url>` | a model server this node lends to Eriphe through its tunnel, as the node reaches it, kept for the next runs (see [A model behind this machine](#a-model-behind-this-machine)); env `ERIPHE_MODEL_SERVER` |
| `--no-model-server` | `-ModelServer none` | lend no model server any more |
| `--uninstall` | `-Uninstall` | remove everything the script installed |
| | `-Rejoin` | with a rejoin token: drop the node's identity and join again under `-Name` |
| | `-Binary <path>` | a Linux eriphe-node binary, instead of the one in `ghcr.io/alexis-marcel/eriphe-node` |
| | `-Distro <name>` | another distribution than `eriphe` (its task and rules get their own names) |

In PowerShell a comma makes a list: quote `-Core` and `-Labels` when they hold one.

`ERIPHE_CORE_IMAGE`, `ERIPHE_NODE_IMAGE`, `ERIPHE_WORKER_IMAGE` (the base image tasks start from,
`eriphe-base`) replace the images (`ghcr.io/alexis-marcel/eriphe-*:latest`; `install.ps1` reads the
first two); `ERIPHE_AGENT_IMAGE` the agents' (node/README.md, Agents); `ERIPHE_VM_CPUS` (4),
`ERIPHE_VM_MEMORY` (`4GiB`) and `ERIPHE_VM_DISK` (`60GiB`) size the macOS VM when it is created.
On Linux the script runs as root (through `sudo` when needed); on macOS as yourself, not root.

## What it installs

The same compose project on every Linux machine, written to `/opt/eriphe` (`compose.yaml`, `.env`,
`install.conf` with the options), data in `/var/lib/eriphe`, as `deploy/nas/compose.yaml` does on
a NAS:

- `eriphe-docker-proxy`: the node's only way to Docker, a socket proxy that allows the calls the
  node makes and bind mounts from `/var/lib/eriphe/node` and the shared places (`--share`) only
  (see deploy/README.md for what it does not stop). It runs as `nobody` in the group of `/var/run/docker.sock`, found at install.
- `eriphe-node`: read-only root, every capability dropped but those that hand the task folders to
  the tasks' user, its root at `/var/lib/eriphe/node` (`node.json`: its identity), and each shared
  place at the same path (read-only for a `:ro` one), told to it in `ERIPHE_NODE_SHARES`.
- `eriphe-core` in core mode: read-only, no capability, its data in `/var/lib/eriphe/core` (owned
  by its UID, 1993), ports 8800 (API, nodes) and 8443 (workspace, HTTPS). It starts with no
  configuration file; the script gets the local node's join token and eriphe-core's CA hash
  from `eriphe-core join-token -name <name>` (a new eriphe-core accepts machines over TLS only,
  from the start: the node joins with `ERIPHE_CA_HASH`, kept in `.env`), and reads the setup
  code from its log.
- the networks: `eriphe-tasks` (internal, isolated gateway with Docker 28 or later: the tasks reach
  only the node), `eriphe-system` (eriphe-core and the services), `eriphe-docker` (the proxy).
- the updater, outside the compose project: `/opt/eriphe/update.sh` and `update.conf` (the images
  it works on), the systemd units `eriphe-update.service`, `.path` and `.timer`, its state in
  `/var/lib/eriphe/update`, and a file `updater` in the node's and eriphe-core's folders that tells
  them it is there (see [the machines update themselves](#the-machines-update-themselves)).
- with systemd and `systemd-inhibit`, `eriphe-awake.path` and `.service`: no sleep while a task is
  at work (node/README.md, Sleep).

Every container restarts with Docker (`restart: unless-stopped`), and Docker starts at boot.

**Linux.** If `docker` is missing, the script installs Docker Engine with Docker's convenience
script (`get.docker.com`) on Debian, Ubuntu, Raspberry Pi OS, Fedora, RHEL and CentOS: Docker's own
packages, a current Engine with the compose plugin, which the distributions' packages often lack or
ship too old (the isolated gateway needs Docker 28). On Alpine, Arch and openSUSE, which that script
does not cover, it uses the distribution's packages. An existing Docker is kept as it is (rootless
Docker is not supported: the proxy needs `/var/run/docker.sock`).

**macOS on Apple silicon, macOS 26 or later**: no VM of Eriphe's. eriphe-node runs on the Mac as
you, and each task is a micro-VM of Apple's container (node/README.md, The container engine).

- [Apple's container](https://github.com/apple/container) 1.6.1, its official package, checked
  against the SHA-256 written in the script and Apple's Developer ID signature, installed with
  `sudo installer` (macOS asks for your password once) if it is missing or older; then
  `container system start --enable-kernel-install` (its Linux kernel, without a question).
- `~/.eriphe/bin/eriphe-node`, and in core mode `~/.eriphe/bin/eriphe-core`: the Mac builds that the
  public images `ghcr.io/alexis-marcel/eriphe-node` and `eriphe-core` carry in
  `/usr/local/share/eriphe/darwin-arm64/`, copied out by a short-lived container: no token, no
  other download, the image's version. `~/.eriphe/node` is the node's root (`node.json`: its
  identity), `~/.eriphe/core` eriphe-core's data.
- the LaunchAgents `org.eriphe.node` (`~/.eriphe/start-node.sh`: container's services, then the node
  with `~/.eriphe/node.env`), `org.eriphe.core` in core mode (eriphe-core on the Mac, ports 8800 and
  8443 on every address), `org.eriphe.update` (the updater). Logs: `~/.eriphe/node.log`, `core.log`,
  `update/update.log`.
- when the node joins another machine's eriphe-core, the root LaunchDaemon `org.eriphe.relay`
  (`/Library/LaunchDaemons`, `/usr/local/libexec/eriphe-relay`, written with `sudo`), the node's
  way to eriphe-core on the LAN (see Troubleshooting, What macOS asks).
- the places shared with Eriphe, by themselves: the home folder (`home`), iCloud Drive (`icloud`),
  and each cloud folder of `~/Library/CloudStorage` (`googledrive`, `onedrive`, `dropbox`…), plus
  the `--share` ones; `--no-share` shares none.

A Mac installed with Lima stays with Lima when the script runs again: `--uninstall` first to move
to Apple's container (in core mode, copy eriphe-core's data out of the VM first).
`ERIPHE_MAC_LIMA=1` keeps a new install on Lima.

**macOS, Intel or before macOS 26**:

- [Lima](https://lima-vm.io) 2.2.1, the official release archive from GitHub, checked against the
  SHA-256 written in the script, in `~/.eriphe/lima`. Its VMs live in `~/.eriphe/vm` (not `~/.lima`:
  apart from any other Lima).
- a VM `eriphe`: Ubuntu 24.04 on Apple's Virtualization framework (`vz`), 4 CPUs, 4 GiB, a 60 GiB
  sparse disk, with Docker Engine and the compose project above inside. Its traffic goes out
  through Apple's NAT (`vzNAT`), not through `limactl`: macOS's local network privacy keeps a
  program that launchd starts off the LAN, so through Lima's own network the node would reach the
  Internet but not an eriphe-core on the LAN.
- a LaunchAgent, `~/Library/LaunchAgents/org.eriphe.vm.plist`, that starts the VM at login (log:
  `~/.eriphe/vm.log`).
- in core mode, the VM's ports 8800 and 8443 forwarded to every address of the Mac, so the LAN
  reaches the workspace. Nothing else is forwarded.

**Windows**: see [Windows](#windows).

## Folders

`--share NAME=/PATH` (repeatable) declares a place of this machine whose folders you may give to
Eriphe, in the workspace (Settings → Folders → "Partager un dossier"): you browse its folders
there from any device, name one ("Clients") and choose what Eriphe may do with it, read, or read
and drop its results in the folder's results subfolder, named after your assistant (`Eriphe/` by
default, `Jarvis/` if you called it Jarvis). eriphe-core never sees the path, only
the place's name and a path inside it. Nothing is shared unless you say so.

```sh
curl -fsSL https://github.com/Alexis-Marcel/eriphe-system/releases/latest/download/install.sh | sh -s -- --share documents=/home/sam/Documents
# a NAS (Unraid): its shares, appdata excepted
curl -fsSL https://github.com/Alexis-Marcel/eriphe-system/releases/latest/download/install.sh | sh -s -- --core-mode --share shares=/mnt/user
```

- `NAME` is lowercase letters, digits, `-` or `_`; `PATH` an existing folder, absolute, without a
  space, a comma or a colon, neither `/` nor Eriphe's own files. `NAME=/PATH:ro` is never written
  to: its folders can only be read.
- Saved with the other options: running the script again keeps them; any `--share` replaces the
  whole list; `--no-share` empties it.
- The node sees each place at the same path, and the socket proxy allows bind mounts from it.
  What the node refuses whatever the place (node/README.md, Shares): hidden folders, keys,
  credentials and settings (`.ssh`, `.gnupg`, `.config`, `appdata`…), a folder that holds one,
  symlinks out of the place, and the node's own data.
- A task runs as UID 1000 with the folder's group added: it reads what that group (or anyone) may
  read. What it drops in `Eriphe/` belongs to UID 1000 and keeps the folder's group,
  group-writable (on a NAS, `users`: every account can open, change and delete it).
- **macOS with Apple's container**: the home, iCloud Drive and the cloud folders are shared by
  themselves (see What it installs), `~/Library` excepted (mail, messages, the apps' settings):
  iCloud Drive and the cloud folders inside it are places of their own. eriphe-node never opens
  them itself: a short-lived container lists them (about 0.7 s a folder), and a task's container
  mounts its folders, so macOS asks once, for Apple's `container-runtime-linux`, and the answer
  outlives Eriphe's updates (node/README.md, The container engine). What a task writes in
  `Eriphe/` is yours, as any file you make. Files kept only in iCloud Drive or a cloud folder are
  downloaded on the Mac before the task starts, up to 2 GB per task (`-download-limit`); a bigger
  folder fails the task with what to do: "Keep Downloaded" on it, in the Finder.
- **macOS in Lima's VM**: `--share` is ignored (the VM sees no Mac folder). **Windows**: not yet.

## A model behind this machine

To use your own model (Ollama, llama.cpp's `llama-server`, vLLM, LM Studio) on a machine of yours,
a home PC with a graphics card or a rented server, install Eriphe there as a device and give it
`--model-server`: the model server keeps listening on that machine only, nothing is opened to the
network or the Internet, and you need no other account. Eriphe reaches it through the device's
tunnel, the one it already has with eriphe-core, and your tasks and the assistant use it wherever
they run. Then, in the workspace: Settings → Agents → Add an AI account → Modèle auto-hébergé →
"Sur un de mes appareils", pick the device, and tick the models it lists.

The address is the one eriphe-node reaches:

- **Linux** (and a NAS, the one-container install): eriphe-node is a container, so the machine's
  `127.0.0.1` is not its own. Run Ollama as a container next to it, on Eriphe's network
  `eriphe-system` (made by the install), **without publishing any port**, and give its name:

  ```sh
  docker run -d --name eriphe-ollama --restart unless-stopped --network eriphe-system \
    -e OLLAMA_NUM_PARALLEL=2 -e OLLAMA_MAX_LOADED_MODELS=1 -e OLLAMA_CONTEXT_LENGTH=16384 \
    -v eriphe-ollama:/root/.ollama ollama/ollama
  # with an NVIDIA card (NVIDIA Container Toolkit): add --gpus=all; Ollama's settings below
  docker exec eriphe-ollama ollama pull qwen3:8b
  curl -fsSL https://github.com/Alexis-Marcel/eriphe-system/releases/latest/download/install.sh | sh -s -- --model-server http://eriphe-ollama:11434
  ```

  (on the one-container install, set `ERIPHE_MODEL_SERVER=http://eriphe-ollama:11434`). Only
  eriphe-core and the node are on that network; tasks are not. The script refuses a loopback
  address here, saying so. `--uninstall` leaves this container: `docker rm -f eriphe-ollama`
  first.
- **A Mac with Apple's container**: eriphe-node runs on the Mac itself: Ollama for macOS listens on
  `127.0.0.1:11434` by default, give `--model-server http://127.0.0.1:11434`. (A Mac in Lima's VM:
  the address the VM reaches, as on Linux.)
- **Windows**: eriphe-node runs in the `eriphe` WSL distribution, behind a NAT of its own by
  default, where `127.0.0.1` is the distribution, not Windows. Ollama for Windows is at
  `-ModelServer http://127.0.0.1:11434` once WSL uses **mirrored networking** (Windows 11 22H2 or
  later):

  1. Add to `%USERPROFILE%\.wslconfig` (create the file if it is not there; WSL reads it only in
     the `[wsl2]` section):

     ```ini
     [wsl2]
     networkingMode=mirrored
     ```

  2. Run `wsl --shutdown`: WSL reads the file only when it starts again. It stops every
     distribution, and the setting applies to all of them, not only Eriphe's.
  3. Run the install command again with `-ModelServer http://127.0.0.1:11434`.

  Ollama for Windows runs in your session only: it starts when you log in and stops when you log
  out, while eriphe-node runs at boot, logged in or not. Add Ollama to your startup programs
  (Win+R, `shell:startup`, and put a shortcut to Ollama there), and stay logged in (a locked
  screen is fine) whenever Eriphe should use it.

At the end, the installer says whether the model server answers eriphe-node: "Lends the model
server at … (answers: yes)". With "no", start the server or check the address; on Windows, with a
loopback address and no mirrored networking, it says to turn it on.

### Ollama's settings

Ollama's defaults suit one person chatting, not Eriphe, which may send the assistant's turn and a
task's at the same time, with long prompts (instructions and tools). Set, as environment variables
of the server (`-e NAME=value` for the container; on Windows and macOS, in the user's environment
variables, then quit and start Ollama again):

| Variable | Value | Why |
|---|---|---|
| `OLLAMA_NUM_PARALLEL` | `2` or more | Two requests at once (the assistant and a task) instead of one waiting for the other, and timing out. |
| `OLLAMA_MAX_LOADED_MODELS` | `1` | One model in memory: two would not fit beside each other on most cards. |
| `OLLAMA_CONTEXT_LENGTH` | `16384` on a card with 8 GB | The context every request gets; tool calling needs 16k to 32k tokens. |
| `OLLAMA_FLASH_ATTENTION` and `OLLAMA_KV_CACHE_TYPE` | `1` and `q8_0` | Halve the context's memory, so that 32k (`OLLAMA_CONTEXT_LENGTH=32768`) fits where 16k did. |

Each parallel slot holds a whole context: two slots at 32k cost twice the memory of one. As a rule
of thumb, a 7B or 8B model (4-bit) takes about 5 GB, plus 1 to 2 GB per slot of 32k with a `q8_0`
cache (twice that without), depending on the model. On a card with 8 GB: 2 slots at 16k, or at 32k
with flash attention and the `q8_0` cache. What does not fit on the card spills into the
computer's memory and runs far slower: 16 GB of RAM is tight for one 7B model with 2 slots at 32k,
next to Windows or a desktop.

A server on your LAN at an address of its own can still be used without a device ("À une
adresse"), but then it listens on the network.

## What the VM can and cannot see

Eriphe's tasks run in containers, on a network of their own (`eriphe-tasks`) that reaches only the
node, and their egress relay never dials a private address (see node/README.md, Egress). What the
machine around them shows depends on the OS:

- **Linux**: no VM. The containers see the folders the node is given (`/var/lib/eriphe/node`, and
  the folders of the shared places a task is lent), not the rest of the host. The socket proxy
  refuses any other bind mount.
- **macOS with Apple's container**: each task is a micro-VM of its own, with its own kernel, that
  sees only its folders (its task folder, the cache, the folders lent to it), on a network of its
  own with no route out: not the LAN, not the Internet but through the node's relay, not the other
  tasks. It does reach the Mac's own services that listen on all of its addresses (eriphe-core's
  8800 and 8443 in core mode, a file share, a development server): see node/README.md, The
  container engine.
- **macOS in Lima's VM**: the VM is the boundary between Eriphe and the Mac.
  - **No Mac folder** is mounted: not your home, not `/Users`, not `/Volumes`. Lima's default home
    mount is off (`mounts: []`).
  - **No key of the Mac** goes in: Lima puts only its own key in the VM, not `~/.ssh/*.pub`, and
    forwards neither the SSH agent nor X11.
  - **The network**: the VM reaches the Internet and the LAN like any machine behind the Mac's
    NAT, the Mac included (`host.lima.internal`). The Mac reaches the VM on its NAT address
    (`192.168.64.x`); the rest of the LAN reaches only what is forwarded (core mode: 8800, 8443).
  - Whoever controls the Mac's account controls the VM (`limactl shell eriphe`), not the other way
    round.
- **Windows**: the WSL distribution `eriphe` mounts no Windows drive (`[automount] enabled =
  false`) and cannot start Windows programs (`[interop] enabled = false`, no Windows `PATH`). It
  reaches the Internet and the LAN through WSL's NAT; the LAN reaches it only on what core mode
  forwards (8800, 8443). All WSL 2 distributions share one network namespace and one kernel: keep
  to this one (see below).

## Windows

`install.ps1` creates its own WSL distribution, `eriphe` (Alpine, Docker Engine, eriphe-node),
without Docker Desktop, and a scheduled task that starts it with Windows, logged in or not.

`irm <url> | iex` takes no parameters: they go in `ERIPHE_*` variables, as in the commands above.
Each parameter has its own: `-Core` `ERIPHE_CORE`, `-JoinToken` `ERIPHE_JOIN_TOKEN`, `-Name`
`ERIPHE_NODE_NAME`, `-Labels` `ERIPHE_NODE_LABELS`, `-CaHash` `ERIPHE_CA_HASH`, `-Version`
`ERIPHE_VERSION`, `-Distro` `ERIPHE_DISTRO`, `-AssistantName` `ERIPHE_ASSISTANT_NAME`, `-WebPort` `ERIPHE_WEB_PORT`, `-ApiPort` `ERIPHE_API_PORT`, and `-CoreMode`, `-Rejoin`, `-Rollback`,
`-Uninstall` `ERIPHE_CORE_MODE`, `ERIPHE_REJOIN`, `ERIPHE_ROLLBACK`, `ERIPHE_UNINSTALL` set to `1`.
The script clears them once read, so that a later run in the same window does not take them again.
An unknown `ERIPHE_*` variable, like an unknown parameter, stops it before it does anything.

The parameters themselves work through a script block, or with the script saved as a file:

```powershell
& ([scriptblock]::Create((irm https://github.com/Alexis-Marcel/eriphe-system/releases/latest/download/install.ps1))) -Core 'http://192.0.2.10:8800' -JoinToken <token>
powershell -ExecutionPolicy Bypass -File .\install.ps1 -Core 'http://192.0.2.10:8800' -JoinToken <token>
```

A parameter given wins over its variable.

**When Windows blocks the command.** Microsoft Defender, SmartScreen or a company policy may stop
`irm … | iex`: the window shows red text such as "This script contains malicious content and has
been blocked by your antivirus software", "running scripts is disabled on this system", or a
Defender notification that it blocked a threat. Do not turn Defender off nor add an exclusion.
Instead, download `install.ps1` from the [latest release](https://github.com/Alexis-Marcel/eriphe-system/releases/latest)
(the browser's download, into your Downloads folder), open it in Notepad and read it, then run it
from an administrator PowerShell in that folder, with the same parameters as the variables you
would have set:

```powershell
cd $HOME\Downloads
powershell -ExecutionPolicy Bypass -File .\install.ps1 -CoreMode
powershell -ExecutionPolicy Bypass -File .\install.ps1 -Core 'http://192.0.2.10:8800' -JoinToken <token> -Name pc
```

`-ExecutionPolicy Bypass` applies to that one run only. If Defender blocks the file too, stop
there and [open an issue](https://github.com/Alexis-Marcel/eriphe-system/issues) with the message
it shows (a false positive is reported to Microsoft from there).

**Core mode** (`-CoreMode`): eriphe-core runs as a container in the distribution, with its data in
`/var/lib/eriphe-core` there and no configuration file, and this machine's node joins it by itself.

**WSL missing.** The script installs it (`wsl --install --no-distribution`, or on a Windows whose
`wsl.exe` refuses it, the Windows features and then `wsl --update` after the restart). On a PC set
up recently this is the long part: Windows finishes its own updates first, and the whole install
can take an hour on a modest mini-PC. When Windows needs a
reboot for it, the script saves itself and its parameters in `%ProgramData%\eriphe-node\resume`
(administrators only) and registers a task, "Eriphe install (eriphe)", that runs it again at the
next logon of the same user, in a window (`powershell -File`, the parameters after it); that run
removes the task and the copy. Under `irm | iex` there is no script text to save: the copy is
downloaded again from the latest release.

**eriphe-node** comes from its image: dockerd pulls `ghcr.io/alexis-marcel/eriphe-node` in the
distribution and the binary is copied out of it (`docker create`, `docker cp`). The running node is
stopped only once the new binary is there, so a failed pull changes nothing.

**Running it again** updates eriphe-node (and in core mode eriphe-core, whose container is created
again only when its image changed) and keeps the node's identity.

**From the LAN to eriphe-core (core mode).** WSL 2 puts the distribution behind a NAT of its own
that only Windows' localhost reaches. Windows forwards ports 8800 (API, nodes) and 8443 (or `-ApiPort`, `-WebPort`)
(workspace) to the distribution's address with `netsh interface portproxy`, and a firewall rule,
"Eriphe core (eriphe)", lets them in from private and domain networks. The distribution's address
changes when WSL restarts, so the scheduled task sets the forwarding again before each start of
the distribution: it runs `%ProgramData%\eriphe-node\boot\eriphe.ps1` (administrators only),
which forwards the ports and then starts the node. Mirrored networking (`networkingMode=mirrored` in `.wslconfig`) would avoid the
forwarding but it is a setting for all of the user's distributions, it needs Windows 11 22H2 or
later, and it applies only after `wsl --shutdown`, so the script does not turn it on. When it is
already on, the script adds no forwarding (the Hyper-V firewall may then need a rule:
`New-NetFirewallHyperVRule`).

**The scheduled task** ("Eriphe node", or "Eriphe node (<distribution>)") starts at boot as the
installing user without a session (S4U). It runs `wsl.exe -d eriphe -u root --exec
/usr/local/bin/eriphe-node-run` (in core mode without mirrored networking, the forwarding script
above), at boot and every minute: a run that is still going is left alone, so the distribution
starts again within a minute after it stops (a `wsl --shutdown` included). Stop it with
`Disable-ScheduledTask`, then `Stop-ScheduledTask`. Inside, a loop restarts
eriphe-node, and another runs the updater (`eriphe-update`, every 20 s): it pulls eriphe-node's
image, swaps the binary (the previous one stays in `/var/lib/eriphe-update`), and in core mode
recreates eriphe-core's container (`eriphe-core-run`), as the install does.

**Sleep.** A second scheduled task, "Eriphe awake" (or "Eriphe awake (<distribution>)"), runs
`%ProgramData%\eriphe-node\boot\<distribution>-awake.ps1` with the same triggers: every 30 s it
asks the distribution whether eriphe-node has a task at work (`/var/lib/eriphe-node/awake`) and,
while it has, keeps Windows out of sleep (`SetThreadExecutionState`); after, Windows sleeps as it
is set to (node/README.md, Sleep).

**GPU.** `-Labels gpu` is a label for the scheduler, kept across updates. Windows' NVIDIA driver
reaches WSL by itself (`/dev/dxg`); the installer adds nothing for it to the distribution.

All WSL 2 distributions share one network namespace: another distribution running its own dockerd
shares the `docker0` bridge and the iptables chains with this one's. Keep to one.

## Eriphe on a VPS

Eriphe's home can be a server rented in the cloud (a VPS) rather than a machine at home: the same
`install.sh --core-mode`. The difference is that such a server has a public address, so whatever
listens on it is reachable from the whole Internet, and that the provider runs the machine. The
installer notices a public address and says so at the end ("Good to know"). Before you use it:

1. **Close the ports in the provider's firewall** (Hetzner, Scaleway, OVHcloud, AWS security
   groups, DigitalOcean…: the firewall of the provider's console, in front of the server). Allow
   SSH, and nothing else of Eriphe's by default:
   - **8443, the workspace**: never open to the world. Reach it one of these ways:
     - **Tailscale**, built in: Réglages › Accès à distance (Settings › Remote access), "Turn on"
       and sign in, from an SSH tunnel the first time (`ssh -L 8443:127.0.0.1:8443 server`, then
       `https://127.0.0.1:8443` on your computer). The workspace then has a
       `https://<name>.<tailnet>.ts.net` address with a real certificate, for your browser, the
       phone app and the devices, and nothing is opened on the server.
     - **Your own domain behind a reverse proxy** with HTTPS (Caddy, nginx, Traefik), signed in
       through your identity provider or the proxy's own login: [docs/reverse-proxy.md](../docs/reverse-proxy.md).
       Open only the proxy's 443.
   - **8800, the API**: open it only if devices join over the Internet rather than over Tailscale.
     Devices talk to it over TLS with a certificate each (mutual TLS, pinned with `--ca-hash`), so
     it is safe to open for them. Other callers of the API (the `eriphe` CLI, Home Assistant, an MCP
     client) send its token: from outside the local network they use the tailnet address
     (`https://<name>.<tailnet>.ts.net:8800`, a trusted certificate) or your proxy, never plain
     `http://` over the Internet, which eriphe-core refuses from a public address.
2. **`--bind 127.0.0.1`** when everything goes through Tailscale (its own connection, not the
   ports) or a reverse proxy on the same server: the ports are then not on the public address at
   all, whatever the firewalls say. Run the install command again with it; it is kept.
3. **UFW does not protect Docker's ports.** Docker writes its own iptables rules ahead of UFW's
   (and of firewalld's), so `ufw deny 8443` does not close a port Docker publishes. Use the
   provider's firewall, `--bind`, or Docker's `DOCKER-USER` chain, which it keeps for you, e.g. on
   the public card `eth0` (persist it with your distribution's iptables service):

   ```sh
   sudo iptables -I DOCKER-USER -i eth0 -p tcp -m conntrack --ctorigdstport 8443 -j DROP
   sudo iptables -I DOCKER-USER -i eth0 -p tcp -m conntrack --ctorigdstport 8800 -j DROP
   ```

4. **IPv6.** eriphe-core's ports are published on IPv4 only ([1. The first
   machine](#1-the-first-machine)): on an IPv6-only server they are not reachable directly; use
   Tailscale or a reverse proxy on the server, which answers on IPv6 and reaches eriphe-core on
   `127.0.0.1`.
5. **Trust.** The provider has the server's disk: Eriphe's memory, conversations, the AI account's
   token, the tools' tokens and the vault's key are on it. Choose a provider you trust with that,
   or keep Eriphe at home.

## Troubleshooting

**Logs.**

| | eriphe-node | eriphe-core (core mode) |
|---|---|---|
| Linux | `docker logs eriphe-node` | `docker logs eriphe-core` |
| macOS, Apple's container | `~/.eriphe/node.log` | `~/.eriphe/core.log` |
| macOS, Lima | `LIMA_HOME=~/.eriphe/vm ~/.eriphe/lima/bin/limactl shell eriphe sudo docker logs eriphe-node` | the same, `eriphe-core` |
| Windows | `wsl -d eriphe -- tail /var/log/eriphe-node.log` | `wsl -d eriphe -- docker logs eriphe-core` |

The install itself, every command's output included, is in `~/.eriphe/install.log` (macOS),
`/var/log/eriphe-install.log` (Linux; `~/.eriphe-install.log` when run without root), or
`C:\ProgramData\eriphe-node\install.log` (Windows); an uninstall on a Mac writes
`$TMPDIR/eriphe-uninstall.log`, as `~/.eriphe` goes.

**What macOS asks, on a Mac with Apple's container.** Each once, for Apple's program, never for
Eriphe's (so not again after an update):

- "« container-runtime-linux » would like to access files in your Documents folder" (or the
  Desktop, Downloads, iCloud Drive, a cloud folder): the first time a folder there is listed in the
  workspace or given to a task. Allow it: refused, the listing fails, and so does the task (System
  Settings → Privacy & Security → Files and Folders → container-runtime-linux, to allow it later).
  The first listing may time out while the question waits; the next one works.
- "« container-runtime-linux » would like to find devices on local networks": when a container on
  container's default network (a service with egress) reaches the LAN. Tasks are on networks with
  no route to the LAN: they reach none, whatever the answer.
- In core mode, if the firewall is on, whether `eriphe-core` may accept incoming connections:
  allow it, or the LAN cannot reach the workspace. Not signed by a Developer ID, it may be asked
  again after an update.

macOS also keeps a LaunchAgent off the LAN (Apple TN3179, local network privacy), and a daemon
run as the user too; only root programs are allowed without asking, and eriphe-node cannot be one
(Apple's container needs your session). So a node that joins an eriphe-core on the LAN gets a root
LaunchDaemon, `/Library/LaunchDaemons/org.eriphe.relay.plist` (written with `sudo`: macOS asks for
the password), that connects to eriphe-core for it and does nothing else: a copy of eriphe-node
that only root can change (`/usr/local/libexec/eriphe-relay core-relay`), on a socket only you can
open (`/var/run/eriphe/relay.sock`), to eriphe-core's addresses only, as your node's files say,
read without following a symlink; TLS stays end to end between the node and eriphe-core
(node/README.md, The container engine). No question from macOS, and none after updates. Running
the install command again updates it (`sudo`). Its log is `/var/log/eriphe-relay.log`. In core
mode there is none: eriphe-core is on the Mac.

**macOS asks whether `limactl` may accept incoming connections** (core mode, first start): allow
it, or the LAN cannot reach the workspace. Refused by mistake: System Settings → Network →
Firewall → Options, set `limactl` to allow incoming connections (or remove it there and run the
install command again). The VM log is `~/.eriphe/vm.log`.

**Windows: other machines cannot reach eriphe-core.** The firewall rule lets the LAN in on Private
and Domain networks only, and the script says so when the network is marked Public. On a home
network, mark it private: Settings → Network & Internet → the connection → Private, or
`Set-NetConnectionProfile -NetworkCategory Private` in an administrator PowerShell.

**eriphe-core cannot write `/data`** (`permission denied` at start, with `docker run -v
<dir>:/data`): the image runs as UID 1993, and a host folder mounted at `/data` must be writable
by it: `sudo chown -R 1993:1993 <dir>`. A named volume (`-v eriphe-data:/data`) needs nothing.
The installers set this up themselves.

**A machine does not join.** The token works once, for the name it was made for, for 24 h: make a
new one. The address must be reachable from that machine (`curl http://<address>:8800/health`
answers `ok`): not `localhost`, and port 8800 open on eriphe-core's machine. A wrong `--ca-hash`
stops the join on purpose: copy it again from Settings → Devices.

**The browser warns about the certificate.** Expected: the workspace's certificate is
self-signed. Compare its fingerprint with the one in eriphe-core's log, then accept it.

**Ports 8800 or 8443 are taken.** Core mode needs both on the machine. The installers check them
before they install anything and stop, naming the program that holds one: run the same command
again with `--web-port 9443` and/or `--api-port 9800` (Windows: `-WebPort`, `-ApiPort`, or
`ERIPHE_WEB_PORT`, `ERIPHE_API_PORT`; the one container: `-e ERIPHE_WEB_PORT=9443`), or free them.
On a Mac in Lima's VM, the VM forwards the ports chosen; on Windows, the port forwarding and the
firewall rule follow them.

<a id="forgotten-password"></a>**Forgotten password.** On eriphe-core's machine (root on that
machine is the owner), this forgets the workspace's password and signs every browser out, keeps
everything else (the assistant, its memory, the devices, the accounts), and prints a new setup
code; open `https://<address>:8443/setup` and choose a new password with it:

| | |
|---|---|
| Linux | `sudo docker exec eriphe-core eriphe-core auth password-reset` |
| the one container (NAS) | `docker exec eriphe-core eriphe-core auth password-reset` |
| macOS, Apple's container | `~/.eriphe/bin/eriphe-core auth password-reset -data ~/.eriphe/core` |
| macOS, Lima | `LIMA_HOME=~/.eriphe/vm ~/.eriphe/lima/bin/limactl shell eriphe sudo docker exec eriphe-core eriphe-core auth password-reset` |
| Windows | `wsl -d eriphe -u root -- docker exec eriphe-core eriphe-core auth password-reset` |

The running eriphe-core asks for the new code at once; no restart. `eriphe-core auth setup-code`
prints the code again while there is no password.

**Eriphe on a Mac stops when you log out.** On macOS, Eriphe runs as LaunchAgents of the user who
installed it: it starts when that user logs in and stops at logout (a restart waits for the login).
That is fine for trying it, or on a Mac that stays logged in and awake; for an Eriphe that is
always there, make an always-on machine its home (a NAS, a mini-PC, a server).
