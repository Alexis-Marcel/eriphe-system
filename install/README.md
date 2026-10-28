# Installing Eriphe

One command installs Eriphe on a machine, and brings its own container runtime: nothing to install
first, not even Docker. `install.sh` covers Linux and macOS, `install.ps1` Windows. Both are served
at `https://eriphe.com/install.sh` and `https://eriphe.com/install.ps1` (a short guide in French
is at [eriphe.com/install](https://eriphe.com/install)); they come from this folder, shipped in the
`eriphe-core` image, which the portal copies them from.

Eriphe is two programs:

- **eriphe-core**, the brain and the workspace (`https://<address>:8443`), on one machine that
  stays on: a NAS, a server, a desktop that does not sleep.
- **eriphe-node**, on every machine Eriphe's agents work on, eriphe-core's own included. A node
  dials eriphe-core and needs no open port.

Contents: [First machine](#1-the-first-machine) · [Workspace](#2-open-the-workspace) ·
[Brain](#3-connect-the-brain) · [Add a machine](#4-add-a-machine) ·
[Google Drive](#5-connect-google-drive-optional) · [Update](#update) ·
[Uninstall](#uninstall) · [Options](#options) · [What it installs](#what-it-installs) ·
[What the VM can and cannot see](#what-the-vm-can-and-cannot-see) · [Windows](#windows) ·
[Troubleshooting](#troubleshooting)

## What it needs

- **Linux**: Debian, Ubuntu, Raspberry Pi OS, Fedora, RHEL, CentOS, Alpine, Arch or openSUSE, on
  amd64 or arm64, with `curl` and root (through `sudo`). Docker is installed if missing.
- **macOS** 13.5 or later, Apple silicon or Intel. No Docker Desktop, no Homebrew. On Apple silicon
  with macOS 26 or later, Eriphe runs on the Mac itself with Apple's `container` (installed if
  missing) and macOS asks for your password (for that package, and for the LaunchDaemon a node
  joining another machine gets); elsewhere in a Lima VM (see [What it installs](#what-it-installs)).
- **Windows** 10 21H2 or later, or 11, Home or Pro, with virtualization on in the firmware, and an
  administrator PowerShell. WSL is installed if missing.
- A few GB of disk for the images (the worker image is the large one), and an account with an
  AI provider: a Claude subscription is the simplest (or ChatGPT, or an API key).

## 1. The first machine

The first machine runs eriphe-core, with a node of its own (core mode).

Linux or macOS:

```sh
curl -fsSL https://eriphe.com/install.sh | sh -s -- --core-mode
```

Windows, in an administrator PowerShell:

```powershell
& ([scriptblock]::Create((irm https://eriphe.com/install.ps1))) -CoreMode
```

The first install downloads the images (and on macOS an Ubuntu image of about 600 MB), so it takes a
few minutes. It ends with the workspace's address on the LAN and a setup code:

```
eriphe-core runs here, with node laptop.
  Workspace: https://192.0.2.10:8443
  Setup code: ABCD-EFGH (open https://192.0.2.10:8443/setup)
  Other machines join it at http://192.0.2.10:8800 (Add a machine, in the workspace).
```

## 2. Open the workspace

Open the address in a browser, from this machine or any other on the LAN. The workspace has its own
self-signed certificate, so the browser warns the first time: accept it (its SHA-256 fingerprint is
in eriphe-core's log, `web: new self-signed certificate … SHA-256 fingerprint …`). On the setup
page, the setup code creates the owner's password; it works once.

## 3. Connect the brain

A short wizard (`/welcome`) follows: pick the AI account Eriphe thinks with. With a Claude
subscription, run `claude setup-token` on any computer where Claude Code is installed and paste
the token; "Use another account" takes a ChatGPT subscription or an API key (OpenRouter, Anthropic,
OpenAI…). Eriphe creates the account's usual agents and starts as soon as a machine is online
(the install already joined this one). Agents, tools and accounts are in Settings later.

Nothing else is on by default: no tool, no rule, no remote access. The catalog's tools (GitHub,
mail, calendar, Home Assistant…) are added from Settings → Accounts & tools.

## 4. Add a machine

In the workspace, Settings → Machines → "Add a machine": give it a short name (lowercase letters,
digits, dashes) and, if you like, labels (`gpu`). The workspace prints the command with everything
filled in: eriphe-core's address, a join token (single use, for this name, valid 24 h) and the
fingerprint of eriphe-core's certificate authority. Run it on the new machine:

```sh
curl -fsSL https://eriphe.com/install.sh | sh -s -- --core http://192.0.2.10:8800 --join-token <token> --name laptop --ca-hash sha256:<hex>
```

```powershell
& ([scriptblock]::Create((irm https://eriphe.com/install.ps1))) -Core 'http://192.0.2.10:8800' -JoinToken <token> -Name pc -CaHash sha256:<hex>
```

The machine then shows on Settings → Machines, with its eriphe-node version. Open the workspace with
the machine's LAN address, not `localhost`, before you generate the command: the address in it is
the one the browser used (or `public_url`), and `localhost` would only work on eriphe-core's own
machine.

Under "Advanced options" the workspace also gives the same settings for a node in an existing
Docker Compose project (`ERIPHE_*` variables, as in `deploy/nas/compose.yaml`), for the eriphe-node
program alone, and for a downloaded `install.ps1`.

Without the workspace, on eriphe-core's machine:

```sh
docker exec eriphe-core eriphe-core join-token -data /data -name laptop          # Linux
wsl -d eriphe -- docker exec eriphe-core eriphe-core join-token -name pc         # Windows
```

A machine that was reinstalled, or lost its identity, keeps its name with "Re-register this
machine" on its card: that token lets it take its name over (on Windows, add `-Rejoin`).

## 5. Connect Google Drive (optional)

To give Eriphe folders of a Google Drive: Settings → Tools → Add a tool → **Google Drive**.

1. "Se connecter à Google Drive" opens Google's page. Tick "Permettre à Eriphe de déposer des
   résultats" first if Eriphe may add files (always in an `Eriphe` subfolder of the folders you
   allow; it never changes or deletes yours); without it, Eriphe only reads.
2. Google may say "Google hasn't verified this app": the Eriphe app is not verified yet. Click
   "Advanced", then "Go to Eriphe", and allow the access to Drive.
3. Back in the workspace, wait a few seconds for the Files server to start on a machine, then open
   the folders and give each one "en lecture" or "lecture + dépôt". Eriphe sees only those folders
   and what is inside them.

This goes through `https://eriphe.com/oauth/callback`, which only sends your browser back to your
workspace (see core/README.md, "Google Drive"), and needs the Eriphe app's client file,
`google-oauth.json`, in eriphe-core's data directory; whoever installed Eriphe gives it to you,
it is never in the images. To put it there (readable by eriphe-core's user, UID 1993):

```sh
sudo install -o 1993 -g 1993 -m 600 google-oauth.json /var/lib/eriphe/core/google-oauth.json      # Linux
LIMA_HOME=~/.eriphe/vm ~/.eriphe/lima/bin/limactl copy google-oauth.json eriphe:/tmp/google-oauth.json   # macOS, then:
LIMA_HOME=~/.eriphe/vm ~/.eriphe/lima/bin/limactl shell eriphe sudo install -o 1993 -g 1993 -m 600 /tmp/google-oauth.json /var/lib/eriphe/core/google-oauth.json
```

```powershell
wsl -d eriphe -- sudo install -o 1993 -g 1993 -m 600 /mnt/c/Users/<you>/Downloads/google-oauth.json /var/lib/eriphe/core/google-oauth.json
```

Without the file, or with a workspace reached at a public name, open "Utiliser mon propre client
Google" on the same page: create a "Web application" OAuth client in your own Google Cloud project
(Google Drive API enabled), with the redirect URI the page shows
(`https://<workspace>/oauth/callback`), and paste its ID and secret.

## Update

Run the same command again, or the script with no option:

```sh
curl -fsSL https://eriphe.com/install.sh | sh -s
```

```powershell
irm https://eriphe.com/install.ps1 | iex
```

It pulls the images again and recreates what changed; the data and the node's identity
(`node.json`) stay, and the join token is not needed any more (it was single use, and is never
written to disk). What is not given again is kept (address, name, labels, mode); given again,
`--labels`, `--core` (e.g. to add a second address) and `--ca-hash` replace the saved ones, and a
joined node keeps its name. Changing between node and core mode needs an uninstall first.

The images follow the releases ([RELEASING.md](../RELEASING.md)): `latest` is the latest one (also
under `0.2.0` and `0.2`), and `edge` is the latest build of `main` (also under `sha-<commit>`). The
scripts that eriphe.com serves are the ones of the latest release too: the portal copies them from
`eriphe-core:latest`. eriphe-core shows its version in Settings → My workspace (`0.2.0`), and each
machine its eriphe-node version on its card. A few times a day it reads which version `latest` is on
the registry: when a newer release is out, the
workspace says "New version available" there, with the command that updates that machine (the
installers tell eriphe-core and the node how they installed them, in `ERIPHE_INSTALL`; a node
without it guesses: Windows in WSL, Linux on a Linux host, a compose project in a container; a compose
project of your own gets `docker compose pull && docker compose up -d`). If an
image cannot be pulled (offline) and a copy of it is already there, the script says so and goes on
with that copy.

### The machines update themselves

Once installed, a machine updates itself when eriphe-core asks, like Tailscale: Settings → Machines.
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
(`ghcr.io/alexis-marcel/eriphe-core`, `eriphe-node`, `eriphe-worker`). Neither eriphe-core nor the
node gets any new right (the socket proxy allows the node exactly what it did, see
deploy/README.md): each only writes a short file in its own data folder. An **updater**, which the
installer puts on the host side, does the rest, as root there:

| | the updater | runs |
|---|---|---|
| Linux | `/opt/eriphe/update.sh`, run by `eriphe-update.service` (systemd) | when a request is dropped (`eriphe-update.path`), and every 10 minutes (`eriphe-update.timer`) |
| macOS with Apple's container | `~/.eriphe/update.sh`, run by the LaunchAgent `org.eriphe.update`: the new `eriphe-node` (and `eriphe-core`) taken from their images, restarted and checked, the previous ones kept in `~/.eriphe/update/previous.*` | when a request is dropped (`WatchPaths`), and every 10 minutes |
| macOS in Lima's VM | the same as Linux, in the VM (Ubuntu, systemd): nothing more on the Mac | the same |
| Windows | `/usr/local/bin/eriphe-update` in the distribution | every 20 s, a loop that the scheduled task starts with eriphe-node |

How a request goes:

1. eriphe-core writes it: for its own machine, `update-requested` in its data folder
   (`/var/lib/eriphe/core`); for a node, in the node's assignments. The node writes it in its root
   (`/var/lib/eriphe/node/update-requested`) once no task is at work. A request is `id`, `action`
   (`update` or `rollback`), `core` (eriphe-core's version) and `auto` (from the night); the
   updater reads these four values, checks them, and never runs the file.
2. The updater pulls the images: eriphe-core at `latest`, `edge` or the pinned release; the node
   and worker of that same build (the tag of eriphe-core's version: `0.2.0`, or `sha-<commit>`
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
command once more (the same one, or `curl -fsSL https://eriphe.com/install.sh | sh -s`, or
`irm https://eriphe.com/install.ps1 | iex`); until then its line says it cannot update itself.
Without systemd (Alpine, OpenRC), the script says so and the machine updates only by running the
command again. A compose project of your own, as `deploy/nas/compose.yaml`, has no updater: keep it
on "Never", and update it with `docker compose pull && docker compose up -d`.

## Uninstall

```sh
curl -fsSL https://eriphe.com/install.sh | sh -s -- --uninstall
```

```powershell
& ([scriptblock]::Create((irm https://eriphe.com/install.ps1))) -Uninstall
```

- Linux: the compose project and its containers, the task and service containers, the networks,
  the `eriphe-cache` volume, Eriphe's images (every tag), the updater's systemd units,
  `/opt/eriphe` and `/var/lib/eriphe`. Docker Engine
  too if the script installed it and no other container is left; otherwise it stays.
- macOS with Apple's container: the LaunchAgents `org.eriphe.node`, `org.eriphe.core` and
  `org.eriphe.update`, the LaunchDaemon `org.eriphe.relay` with its binary, socket folder and log
  (with `sudo`), the task, service and explorer containers and their networks, Eriphe's images,
  and `~/.eriphe`. Apple's `container` too if the script installed it (it notes so in
  `~/.eriphe/container-installed`) and no other container is left; otherwise it stays.
- macOS in Lima's VM: the LaunchAgent, the VM and its disk, `~/.eriphe` (Lima included), and
  Lima's download cache (`~/Library/Caches/lima`) unless another Lima is installed.
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
| `--join-token TOKEN` | `-JoinToken <token>` | from "Add a machine", or `eriphe-core join-token -name NAME`; first install only |
| `--name NAME` | `-Name <name>` | the node's name (default: the host name, lowercased) |
| `--labels gpu,fast` | `-Labels 'gpu,fast'` | what agents can require of this node |
| `--ca-hash sha256:<hex>` | `-CaHash sha256:<hex>` | eriphe-core's CA, to check its certificate (`ERIPHE_CA_HASH` of the node) |
| `--core-mode` | `-CoreMode` | run eriphe-core here, and a local node joined to it |
| `--version 0.2.0` | `-Version 0.2.0` | pin that release (`0.2`: its line); `edge` follows `main`; `latest` the releases again (see [the machines update themselves](#the-machines-update-themselves)) |
| `--rollback` | `-Rollback` | go back to the version before the last update |
| `--share NAME=/PATH[:ro]` | | a place of this machine whose folders may be given to Eriphe, repeatable (see [Folders](#folders)); Linux, and a Mac with Apple's container (which shares its home, iCloud Drive and its cloud folders by itself) |
| `--no-share` | | share no place any more |
| `--uninstall` | `-Uninstall` | remove everything the script installed |
| | `-Rejoin` | with a rejoin token: drop the node's identity and join again under `-Name` |
| | `-Binary <path>` | a Linux eriphe-node binary, instead of the one in `ghcr.io/alexis-marcel/eriphe-node` |
| | `-Distro <name>` | another distribution than `eriphe` (its task and rules get their own names) |

In PowerShell a comma makes a list: quote `-Core` and `-Labels` when they hold one.

`ERIPHE_CORE_IMAGE`, `ERIPHE_NODE_IMAGE`, `ERIPHE_WORKER_IMAGE` replace the images
(`ghcr.io/alexis-marcel/eriphe-*:latest`; `install.ps1` reads the first two); `ERIPHE_VM_CPUS` (4),
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
  configuration file; the script gets the local node's join token with
  `eriphe-core join-token -name <name> -quiet` and reads the setup code from its log.
- the networks: `eriphe-tasks` (internal, isolated gateway with Docker 28 or later: the tasks reach
  only the node), `eriphe-system` (eriphe-core and the services), `eriphe-docker` (the proxy).
- the updater, outside the compose project: `/opt/eriphe/update.sh` and `update.conf` (the images
  it works on), the systemd units `eriphe-update.service`, `.path` and `.timer`, its state in
  `/var/lib/eriphe/update`, and a file `updater` in the node's and eriphe-core's folders that tells
  them it is there (see [the machines update themselves](#the-machines-update-themselves)).

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
and drop its results in the folder's `Eriphe/` subfolder. eriphe-core never sees the path, only
the place's name and a path inside it. Nothing is shared unless you say so.

```sh
curl -fsSL https://eriphe.com/install.sh | sh -s -- --share documents=/home/sam/Documents
# a NAS (Unraid): its shares, appdata excepted
curl -fsSL https://eriphe.com/install.sh | sh -s -- --core-mode --share shares=/mnt/user
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

`irm <url> | iex` takes no parameters: they go through a script block, as in the commands above.
Saved as a file, the same parameters follow it:

```powershell
powershell -ExecutionPolicy Bypass -File .\install.ps1 -Core 'http://192.0.2.10:8800' -JoinToken <token>
```

**Core mode** (`-CoreMode`): eriphe-core runs as a container in the distribution, with its data in
`/var/lib/eriphe-core` there and no configuration file, and this machine's node joins it by itself.

**WSL missing.** The script installs it (`wsl --install --no-distribution`). When Windows needs a
reboot for it, the script saves itself and its parameters in `%ProgramData%\eriphe-node\resume`
(administrators only) and registers a task, "Eriphe install (eriphe)", that runs it again at the
next logon of the same user, in a window; that run removes the task and the copy. Under a bare
`irm | iex` there is no script text to save: run the command again after the reboot.

**eriphe-node** comes from its image: dockerd pulls `ghcr.io/alexis-marcel/eriphe-node` in the
distribution and the binary is copied out of it (`docker create`, `docker cp`). The running node is
stopped only once the new binary is there, so a failed pull changes nothing.

**Running it again** updates eriphe-node (and in core mode eriphe-core, whose container is created
again only when its image changed) and keeps the node's identity.

**From the LAN to eriphe-core (core mode).** WSL 2 puts the distribution behind a NAT of its own
that only Windows' localhost reaches. Windows forwards ports 8800 (API, nodes) and 8443
(workspace) to the distribution's address with `netsh interface portproxy`, and a firewall rule,
"Eriphe core (eriphe)", lets them in from private and domain networks. The distribution's address
changes when WSL restarts, so the scheduled task sets the forwarding again before each start of
the distribution. Mirrored networking (`networkingMode=mirrored` in `.wslconfig`) would avoid the
forwarding but it is a setting for all of the user's distributions, it needs Windows 11 22H2 or
later, and it applies only after `wsl --shutdown`, so the script does not turn it on. When it is
already on, the script adds no forwarding (the Hyper-V firewall may then need a rule:
`New-NetFirewallHyperVRule`).

**The scheduled task** ("Eriphe node", or "Eriphe node (<distribution>)") starts at boot as the
installing user without a session (S4U), and starts the distribution again 5 s after it stops
(a `wsl --shutdown` included); stop it with `Stop-ScheduledTask`. Inside, a loop restarts
eriphe-node, and another runs the updater (`eriphe-update`, every 20 s): it pulls eriphe-node's
image, swaps the binary (the previous one stays in `/var/lib/eriphe-update`), and in core mode
recreates eriphe-core's container (`eriphe-core-run`), as the install does.

**GPU.** `-Labels gpu` is a label for the scheduler, kept across updates. Windows' NVIDIA driver
reaches WSL by itself (`/dev/dxg`); the installer adds nothing for it to the distribution.

All WSL 2 distributions share one network namespace: another distribution running its own dockerd
shares the `docker0` bridge and the iptables chains with this one's. Keep to one.

## Troubleshooting

**Logs.**

| | eriphe-node | eriphe-core (core mode) |
|---|---|---|
| Linux | `docker logs eriphe-node` | `docker logs eriphe-core` |
| macOS, Apple's container | `~/.eriphe/node.log` | `~/.eriphe/core.log` |
| macOS, Lima | `LIMA_HOME=~/.eriphe/vm ~/.eriphe/lima/bin/limactl shell eriphe sudo docker logs eriphe-node` | the same, `eriphe-core` |
| Windows | `wsl -d eriphe -- tail /var/log/eriphe-node.log` | `wsl -d eriphe -- docker logs eriphe-core` |

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
stops the join on purpose: copy it again from Settings → Machines.

**The browser warns about the certificate.** Expected: the workspace's certificate is
self-signed. Compare its fingerprint with the one in eriphe-core's log, then accept it.

**Ports 8800 or 8443 are taken.** Core mode needs both on the machine; the script warns when
something else listens there on a Mac (and stops, with Apple's container). Free them, or run eriphe-core elsewhere.
