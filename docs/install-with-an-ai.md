# Installing Eriphe for someone: a runbook for AI assistants

This page is written for an AI assistant (Claude, ChatGPT, Codex, Gemini, Cursor or any other)
that a person asked to "install Eriphe for me". Follow it in order. The person is probably not a
developer: explain each step in one or two plain sentences, say what they will see before they
see it, and wait for their answer before moving on.

Everything here matches the installers in [`install/`](../install/) and their guide,
[install/README.md](../install/README.md), which has every detail and option. When this page and
the scripts disagree, the scripts are right: read them, follow them, and tell the person.

## 0. Rules for you, the assistant

Read these first and keep to them for the whole session.

1. **Never ask for a secret in the chat.** Passwords, the setup code, Claude's `setup-token`, API
   keys, ChatGPT sign-in codes, join tokens, OAuth client secrets: the person types or pastes
   them **themselves**, in the browser (the workspace) or in their own terminal. If they paste one
   into the chat anyway, tell them so and do not repeat it. **Never run `claude setup-token`
   yourself**, in your shell or any shell whose output you read: the token it prints would land in
   this conversation. Guide the person to run it in their own terminal and paste the token in the
   workspace only (§5).
2. **Never weaken the computer's security.** Do not turn off Windows Defender, an antivirus, the
   firewall, Gatekeeper or macOS's System Integrity Protection (SIP), and do not tell the person to.
   Answer the operating system's questions the way this page says, nothing more. Never add a
   Defender exclusion either: when Windows blocks the installer, see §4C.
3. **Never expose Eriphe to the internet.** No port forwarding on the router, no public tunnel
   (ngrok, Cloudflare Tunnel, a public reverse proxy), no `0.0.0.0` firewall openings to the
   internet. For access away from home, use Eriphe's built-in **Remote access** (Tailscale, §7),
   which opens nothing.
4. **Run only commands from this page or from the workspace**, and only after reading them. Do
   not pipe other scripts into a shell. Prefer the documented one-liners over hand-made steps.
5. **One eriphe-node per machine.** Do not install Eriphe a second time on a machine that already
   runs it (on itself or for another Eriphe): running the same command again updates it, which is
   fine; a second, different install is not.
6. **Prompts you cannot answer.** The installers may ask for the computer's password (`sudo`, or
   macOS's), and Windows needs an administrator PowerShell. Most AI tools' shells cannot answer a
   password prompt or raise to administrator. In that case, give the person the exact command to
   paste into their own terminal window, and ask them to tell you how it ended (the lines after
   "Done", **without** the setup code).
7. **Do not guess addresses.** Use the address the installer prints (`https://<address>:8443`).
   Examples on this page use `192.0.2.10`, which is not a real machine.

## 1. What Eriphe is, in one minute

- **eriphe-core** is the assistant itself: its memory, its accounts and the **workspace**, a web
  page at `https://<address>:8443`. It runs on **one machine that stays on**: a NAS, a mini-PC or
  server, or a desktop that does not sleep. This is Eriphe's *home*.
- **eriphe-node** runs on every device connected to Eriphe, the home machine included. It lets
  Eriphe see the folders the person opens to it and run agents there. A node dials eriphe-core;
  it needs no open port.
- The installer brings everything it needs (its own container runtime). Nothing to install first,
  not even Docker.
- The person needs an **AI account**: a Claude subscription (Pro or Max) is the simplest; a
  ChatGPT subscription (Plus or Pro) or an API key (Anthropic, OpenAI, Google, Mistral, OpenRouter)
  work too.

## 2. Ask the person first

Ask these, in plain words, and write down the answers:

1. **Which machine stays on all the time?** (a NAS, a mini-PC, a server, a desktop that never
   sleeps.) If none: which computer could stay on? A laptop that sleeps or travels is a poor home.
2. **What is each device they want to connect?** Windows, Mac (Apple silicon or Intel, which
   macOS version: Apple menu → About This Mac), Linux (which distribution), a NAS (which brand).
3. **Does the NAS run Docker?** (Unraid, Synology Container Manager, TrueNAS apps, a Raspberry Pi
   with Docker…) If yes, is it **Unraid**?
4. **Which AI account will Eriphe think with?** A Claude subscription, a ChatGPT subscription, or
   an API key (and from which provider). They will connect it themselves in the browser.
5. **Do they use Tailscale**, or want to reach Eriphe from their phone or away from home? (Optional;
   it can be set up at the end.)
6. **Are all the devices on the same home network** (same router, wired or Wi-Fi)?

## 3. Choose Eriphe's home

Pick the first line that matches:

| The person has | Eriphe's home | Install with |
|---|---|---|
| A NAS or Linux machine where **Docker already runs** (Unraid, Synology, TrueNAS, a Pi with Docker) | that machine | §4D, one container (Unraid: its template) |
| An always-on **Linux** machine without Docker (mini-PC, server, Raspberry Pi) | that machine | §4B, `install.sh --core-mode` |
| Only a **Mac** | that Mac, set to never sleep and kept logged in (tell the person: Eriphe runs on a Mac only while their session is open; an always-on machine is a better home) | §4A, `install.sh --core-mode` |
| Only a **Windows** PC | that PC, set to never sleep | §4C, `install.ps1` with `ERIPHE_CORE_MODE` |

Then every **other** device is connected from the workspace (§6): never with `--core-mode` again.

Requirements (the installer checks them and says so if one is missing):

- **Linux**: Debian, Ubuntu, Raspberry Pi OS, Fedora, RHEL, CentOS, Alpine, Arch or openSUSE, amd64
  or arm64, with `curl` and `sudo`. Automatic updates need systemd.
- **macOS** 13.5 or later. On Apple silicon with macOS 26 or later, Eriphe runs on the Mac itself
  with Apple's `container`; otherwise in a small Linux VM (Lima), downloaded by the installer.
- **Windows** 10 21H2 or later, or 11 (Home or Pro), with virtualization turned on in the firmware.
  WSL is installed if missing (it may need one restart).
- A few GB of free disk, and two free ports on the home machine: **8443** (workspace) and **8800**
  (devices) by default. The installers check them before installing anything; if one is taken,
  they name the program and the option to use: `--web-port 9443` / `--api-port 9800` with
  `install.sh`, `-WebPort 9443` / `-ApiPort 9800` (or `$env:ERIPHE_WEB_PORT='9443'`,
  `$env:ERIPHE_API_PORT='9800'` before `irm | iex`) with `install.ps1`, `-e ERIPHE_WEB_PORT=9443`
  / `-e ERIPHE_API_PORT=9800` for the one container. The ports are kept for later runs, and the
  commands of "Connect a device" use the API's port. Use the address and port the installer prints.

## 4. Install on the home machine

### 4A. Mac

1. Tell the person: "Open the Terminal app (Applications → Utilities → Terminal). You may be asked
   for your Mac password: that is the installer, it says why first." Run it as the person, never
   with `sudo` and never as root.
2. The command:

   ```sh
   curl -fsSL https://github.com/Alexis-Marcel/eriphe-system/releases/latest/download/install.sh | sh -s -- --core-mode
   ```

3. What the person may see, and what to answer:
   - "macOS will ask for your Mac password: …" then a password prompt: they type their Mac
     password (nothing shows while typing). It installs Apple's `container` if it is missing.
   - If the macOS firewall is on: "Do you want the application "eriphe-core" to accept incoming
     network connections?" (or "limactl" on an Intel Mac or before macOS 26): **Allow**, or the
     other devices cannot reach the workspace.
   - Later, when Eriphe first opens Documents, Desktop, Downloads or iCloud Drive:
     ""container-runtime-linux" would like to access files in your … folder": **Allow**. It is
     Apple's program that runs Eriphe's tasks. Refused by mistake: System Settings → Privacy &
     Security → Files and Folders → container-runtime-linux.
   - ""container-runtime-linux" would like to find devices on local networks": either answer is
     fine for Eriphe's tasks; **Allow** if a tool of theirs needs the home network.
4. Expected end of the output:

   ```
     ✓ Downloaded
     ✓ Installed
     ✓ Started

   Done: Eriphe runs on this Mac. To finish, open this address in
   your browser and give the setup code:

     https://192.0.2.10:8443/setup
     Setup code: ABCD-EFGH
   ```

   followed by "Good to know" notes and the `Logs:` line. Go to §5.
5. Keep the Mac awake: System Settings → Energy (or Battery → Options) → prevent automatic sleeping
   when the display is off. Eriphe on a Mac runs **only while that user's session is open**: it
   starts at login and stops at logout, and after a restart someone must log in for it to run (the
   installer says so too). Say it plainly, and recommend an always-on machine (NAS, mini-PC) as
   Eriphe's home if they have or plan one.

### 4B. Linux machine (no Docker yet, or Docker installed by hand)

1. The person runs, in a terminal on that machine (or over SSH):

   ```sh
   curl -fsSL https://github.com/Alexis-Marcel/eriphe-system/releases/latest/download/install.sh | sh -s -- --core-mode
   ```

2. "This computer will ask for your password: …": the `sudo` password. If Docker is missing, the
   installer installs Docker Engine (Docker's own packages, or the distribution's on Alpine, Arch,
   openSUSE). An existing Docker is kept; rootless Docker is not supported.
3. Expected end: as in §4A, "Done: Eriphe runs on this computer…", the `https://<address>:8443/setup`
   address and the setup code, then `Logs: docker logs eriphe-core, docker logs eriphe-node`.
4. Optional, to let Eriphe see folders of this machine, run the same command again with
   `--share NAME=/PATH` (repeatable), e.g. `--share documents=/home/<user>/Documents`. The installer
   suggests it at the end.

### 4C. Windows PC

1. The person opens an **administrator** PowerShell: right-click the Start button → **Terminal
   (Admin)** or **Windows PowerShell (Admin)** → **Yes**. Without it, the installer stops and says
   exactly that.
2. They paste this one line (the variable replaces `--core-mode`; `irm … | iex` takes no
   parameters):

   ```powershell
   $env:ERIPHE_CORE_MODE='1'; irm https://github.com/Alexis-Marcel/eriphe-system/releases/latest/download/install.ps1 | iex
   ```

   **If Windows blocks it.** Microsoft Defender, SmartScreen or a policy may stop `irm … | iex`:
   red text such as "This script contains malicious content and has been blocked by your
   antivirus software" or "running scripts is disabled on this system", or a Defender
   notification that a threat was blocked. Do **not** turn Defender off, do not add an exclusion,
   and do not touch the PC's security settings. Instead:
   1. The person downloads `install.ps1` from the
      [latest release](https://github.com/Alexis-Marcel/eriphe-system/releases/latest) in their
      browser (it lands in Downloads), opens it in Notepad and reads it (you can explain it).
   2. In the administrator PowerShell, with the same parameters as the variables
      (`ERIPHE_CORE_MODE='1'` is `-CoreMode`, `ERIPHE_WEB_PORT` is `-WebPort`, …):

      ```powershell
      cd $HOME\Downloads
      powershell -ExecutionPolicy Bypass -File .\install.ps1 -CoreMode
      ```

      `-ExecutionPolicy Bypass` is for that run only.
   3. If Defender blocks or quarantines the file too, stop, and have the person open an issue
      (<https://github.com/Alexis-Marcel/eriphe-system/issues>) with the message it shows.
3. If WSL was missing, it is installed, and the installer may print "Windows needs to restart to
   finish preparing Eriphe. Restart it: the installation carries on by itself when you sign in
   again." The person restarts and signs in as the **same user**; a window opens and the install
   goes on. If it says "Restart it, then run this command again", they restart and paste the same
   line again in an administrator PowerShell.
4. "Windows could not create Eriphe's Linux system: turn on virtualization…": virtualization is off
   in the PC's firmware (BIOS/UEFI). Explain how to reach the firmware settings for their PC brand
   (often a key at boot: Del, F2, F10) and the setting's usual names (Intel VT-x, Intel
   Virtualization Technology, AMD-V, SVM Mode). Do not change other firmware settings.
5. Expected end: "Done: Eriphe runs on this PC. To finish, open this address in your browser and
   give the setup code:", the address, the code, then
   `Logs: wsl -d eriphe -- tail /var/log/eriphe-node.log`.
6. If a "Good to know" note says the network is set to **Public**: Windows Settings → Network &
   internet → the connection → **Private**. Otherwise the other devices cannot reach Eriphe.
7. Keep the PC awake: Settings → System → Power → Screen and sleep → "put my device to sleep" →
   **Never** (when plugged in). Eriphe starts with Windows, even before anyone logs in.

### 4D. NAS or Linux with Docker: one container

Eriphe installs as one container, `ghcr.io/alexis-marcel/eriphe`, the *launcher*, which creates
and updates Eriphe's own containers next to it (eriphe-core, eriphe-node, eriphe-docker-proxy).

**Unraid** (its template, from the Docker page; details in [unraid/README.md](../unraid/README.md)):

1. Docker → **Add Container** → at the bottom, Template repositories: add
   `https://github.com/Alexis-Marcel/eriphe-system` and **Save**.
2. Docker → **Add Container** → Template: **eriphe**. Keep the data folder
   `/mnt/user/appdata/eriphe` and the shares `shares=/mnt/user` (appdata and hidden folders are
   always refused). If something already uses port 8443 (a UniFi controller often does), set
   **Workspace port** to a free one, e.g. 9443, and the WebUI address to match. **Apply**.
3. Open the container's log (its icon → Logs). It ends with the workspace's address and a setup
   code. Go to §5.

**Any other NAS or Linux with Docker** (Synology, TrueNAS, a Pi…): pick a data folder on the
machine (on Synology for instance `/volume1/docker/eriphe`; on Unraid `/mnt/user/appdata/eriphe`)
and the folders Eriphe may be given (`ERIPHE_SHARES`, host paths, `NAME=/PATH` comma-separated, or
leave it out). Then, as root or with `sudo`:

```sh
docker run -d --name eriphe --restart unless-stopped \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v /volume1/docker/eriphe:/data \
  -e ERIPHE_SHARES=shares=/volume1/data \
  ghcr.io/alexis-marcel/eriphe
docker logs -f eriphe
```

- The data folder and the shares are **paths on the host**, and the data folder must not be inside
  a share.
- Port 8443 or 8800 taken: add `-e ERIPHE_WEB_PORT=9443` (or `-e ERIPHE_API_PORT=9800`).
- Expected end of `docker logs eriphe`: "Done: Eriphe runs on this machine. To finish, open this
  address in your browser and give the setup code:", the address, the setup code, and "Good to
  know: then the browser asks you to name your assistant and connect its AI account." Stop
  following the log with Ctrl+C (the container keeps running).
- The containers it makes (eriphe-core, eriphe-node, eriphe-docker-proxy) show without a template:
  leave them alone, the launcher recreates them on updates.

### Check the install

On the home machine, both must succeed (on Windows, use `curl.exe`, not `curl`):

```sh
curl http://127.0.0.1:8800/health                                   # answers: ok
curl -sk -o /dev/null -w '%{http_code}\n' https://127.0.0.1:8443/   # a code such as 200 or 303, not 000
```

From another device of the home network, the same with the home machine's address instead of
`127.0.0.1`. If the first works and the second does not from elsewhere, see §10 (firewall, Public
network, macOS prompt).

**The setup code**, if it scrolled away or the person lost it: it stays the same until the
password is created, and running the installer again prints it again. Or:

| Home machine | Command |
|---|---|
| Linux, one container (§4D) | `sudo docker exec eriphe-core eriphe-core auth setup-code` |
| Mac with Apple's container (macOS 26+, Apple silicon) | `~/.eriphe/bin/eriphe-core auth setup-code -data ~/.eriphe/core` |
| Mac with Lima (Intel, or before macOS 26) | `LIMA_HOME=~/.eriphe/vm ~/.eriphe/lima/bin/limactl shell eriphe sudo docker exec eriphe-core eriphe-core auth setup-code` |
| Windows | `wsl -d eriphe -u root -- docker exec eriphe-core eriphe-core auth setup-code` |

Ask the person to run it and read the code **themselves**; they type it in the browser. Once the
password exists, the command says so and the installer says "Open https://… and sign in
(forgotten password: …)".

**A forgotten password**: the same commands with `auth password-reset` instead of `auth
setup-code` forget the workspace's password, sign every browser out, keep everything else (the
assistant, its memory, devices, accounts) and print a new setup code; the person then opens
`https://<address>:<port>/setup` and chooses a new password. Root on the home machine is the
owner: this needs no other proof. The person runs it in their own terminal.

## 5. Finish in the browser (the person does this)

Guide them; do not ask them to tell you the code, the password or any key.

1. **Open the address** the installer printed, `https://<address>:8443/setup`, from any device of
   the home network. Use the machine's network address, not `localhost`: the commands that
   connect other devices are built from the address the browser used.
2. **The certificate warning is expected**: the workspace uses its own self-signed certificate.
   - Chrome or Edge: "Your connection is not private" → **Advanced** → **Proceed to … (unsafe)**.
   - Safari: "This Connection Is Not Private" → **Show Details** → **visit this website** → confirm.
   - Firefox: "Warning: Potential Security Risk Ahead" → **Advanced…** → **Accept the Risk and
     Continue**.

   Careful people can compare the certificate's SHA-256 fingerprint with the line
   `web: … SHA-256 fingerprint …` in eriphe-core's log first.
3. **Setup page**: they type the setup code and choose the workspace's password (10 characters at
   least). The code works once. The workspace follows the browser's language (French, or English
   for any other); each browser can change it in Réglages → Mon espace → Langue (Settings → My
   workspace → Language). Name menus in both languages when you guide the person.
4. **Name the assistant**: "Comment veux-tu appeler ton assistant ?" (What do you want to call your
   assistant?). Any name of 1 to 32 letters, digits, spaces, apostrophes or dashes. Eriphe is the
   software's name; the assistant has theirs. It can be changed later (Settings → Agents).
5. **Connect the AI account**: they pick its card.
   - **Claude subscription**: they need the token of `claude setup-token`. On any computer, in
     **their own** terminal window (not one you, the assistant, can read): install Claude Code
     if it is missing (Anthropic's instructions: <https://docs.anthropic.com/en/docs/claude-code>),
     run `claude setup-token`, sign in in the browser it opens, and copy the token it prints into
     the workspace's field. **You never run `claude setup-token`**, not in your own shell nor in
     one whose output you read: the token would land in this conversation. The workspace warns
     the person too: « Ne colle pas ce jeton dans une conversation avec une IA » (do not paste
     this token into a conversation with an AI). If they paste it into the chat anyway, tell them
     to make a new one (`claude setup-token` again) and use only that.
   - **ChatGPT subscription**: the page shows a code and a link; they open the link, sign in at
     OpenAI and type the code. If OpenAI refuses, "device code" sign-in must be allowed in
     ChatGPT's security settings.
   - **API key**: they create a key in their provider's console and paste it into the workspace
     only.
6. **The assistant starts** on the home machine (the install already connected it as a device).
   The first time the conversation opens, the assistant writes first and offers a **short
   interview** (who they are, what they expect, how to talk to them). They can answer or skip it;
   it can be done again later from the Memory page ("Faire l'interview").

Nothing else is on by default: no tool, no rule, no remote access.

## 6. Connect the other devices

For each other computer (or a second NAS):

1. In the workspace: **Réglages → Appareils → Connecter un appareil** (Settings → Devices →
   Connect a device). The person gives the device a short name (lowercase letters, digits, dashes:
   `laptop`, `pc`) and, if they like, labels (`gpu`).
2. The workspace prints the command for each system, with everything filled in: eriphe-core's
   address, a join token (single use, for that name, valid 24 hours) and the fingerprint of
   Eriphe's certificate authority. The person copies it **from the workspace** and runs it on the
   new device (Mac, Linux: a terminal; Windows: an administrator PowerShell). The token is a
   secret: do not ask for it; if the person prefers you to run it, they paste it into your shell
   themselves.

   They look like this (do not type these; the real ones come from the workspace):

   ```sh
   curl -fsSL https://github.com/Alexis-Marcel/eriphe-system/releases/latest/download/install.sh | sh -s -- --core http://192.0.2.10:8800 --join-token <token> --name laptop --ca-hash sha256:<hex>
   ```

   ```powershell
   $env:ERIPHE_CORE='http://192.0.2.10:8800'; $env:ERIPHE_JOIN_TOKEN='<token>'; $env:ERIPHE_NODE_NAME='pc'; $env:ERIPHE_CA_HASH='sha256:<hex>'; irm https://github.com/Alexis-Marcel/eriphe-system/releases/latest/download/install.ps1 | iex
   ```

   A NAS with Docker joins with the one container of §4D in node mode: `ERIPHE_MODE=node`,
   `ERIPHE_CORE`, `ERIPHE_JOIN_TOKEN`, `ERIPHE_CA_HASH` (on Unraid, the template's advanced
   settings). The workspace's "Advanced options" give those values.
3. On a Mac that joins, macOS asks for the password once: it installs a small helper
   (`org.eriphe.relay`) that lets Eriphe reach the home machine on the local network.
4. Expected end: "Done: <device> is connected to <assistant name>. Go back to your browser: it now
   appears in Devices." The device's card shows in Settings → Devices. In the node's log
   (`docker logs eriphe-node`, `~/.eriphe/node.log`, or on Windows
   `wsl -d eriphe -- tail /var/log/eriphe-node.log`), the line `connected to http://… as <name>`
   says it joined.
5. Folders: the person chooses what Eriphe may see in **Settings → Folders** ("Partager un
   dossier"): read only, or read and drop results in an `Eriphe/` subfolder. Nothing is shared
   until they do.

## 7. Optional: the phone, away from home, tools

- **Remote access and the phone app.** Settings → Remote access (Réglages → Accès à distance):
  **Turn on**, **Log in to Tailscale** (a free account), **Enable HTTPS** once. The page then shows
  an `https://<name>.<tailnet>.ts.net` address and a QR code. On the phone: install the Tailscale
  app and sign in with the same account, open that address, and add it to the home screen (Settings
  → App & notifications explains it for iPhone and Android). It installs as an app with
  notifications. Nothing is opened on the router. The phone app needs this trusted address: at
  the self-signed `https://192.0.2.10:8443` it works in the browser only.
- **Tools** (mail, calendars, Home Assistant, GitHub, any MCP server): Settings → Accounts & tools.
  Each one asks the person to approve access at the service (OAuth) or to paste a token there:
  they do it, in the workspace.
- **Google Drive** needs the person's own Google app, created once in their Google Cloud console:
  follow [docs/google-drive.md](google-drive.md) with them, step by step. Google refuses a workspace
  opened at an IP address: turn on Remote access first and open the Google Drive page at the
  `ts.net` address. "Google hasn't verified this app" is expected (it is their own app):
  **Advanced** → **Go to Eriphe**.
- **GitHub**: Settings → Accounts & tools → GitHub → "Créer l'app GitHub d'Eriphe" (see
  [install/README.md, Connect GitHub](../install/README.md#6-connect-github-optional)).
- **Voice** through Home Assistant: [ha/README.md](../ha/README.md).

## 8. Final check with the person

- [ ] The workspace opens at `https://<address>:8443` and they can sign in.
- [ ] The assistant answers a message in the conversation.
- [ ] Every device shows in Settings → Devices, connected.
- [ ] The home machine does not sleep.
- [ ] They know where the password is kept (their password manager), and that you never saw it.

## 9. Update and uninstall

**Update.** Settings → Devices shows "Update available" with an **Update** button per machine
(and **Update all**); "Update automatically at night" is a switch there. By hand, the install
command without options:

```sh
curl -fsSL https://github.com/Alexis-Marcel/eriphe-system/releases/latest/download/install.sh | sh -s
```

```powershell
irm https://github.com/Alexis-Marcel/eriphe-system/releases/latest/download/install.ps1 | iex
```

One container: the workspace's Update works the same; by hand `docker exec eriphe eriphe-launcher update`.

**Uninstall** (on the home machine this deletes the assistant's data, its memory included: copy
it out first if they want to keep it; see [install/README.md, Uninstall](../install/README.md#uninstall)):

```sh
curl -fsSL https://github.com/Alexis-Marcel/eriphe-system/releases/latest/download/install.sh | sh -s -- --uninstall
```

```powershell
$env:ERIPHE_UNINSTALL='1'; irm https://github.com/Alexis-Marcel/eriphe-system/releases/latest/download/install.ps1 | iex
```

One container: `docker exec eriphe eriphe-launcher uninstall` (add `--data` to delete the data
folder too), then `docker rm -f eriphe` (on Unraid: Docker → eriphe → Remove). An uninstalled
device stays listed in the workspace: "Forget" it on its card.

## 10. Known problems and fixes

| What happens | What to do |
|---|---|
| "… already uses port 8443 (or 8800) of this computer, which Eriphe needs", or Docker says the port is already allocated | Another program uses the port. Run the same command again with the option the message names: `--web-port 9443` / `--api-port 9800` (`install.sh`), `-WebPort 9443` / `-ApiPort 9800` (`install.ps1`), `-e ERIPHE_WEB_PORT=9443` / `-e ERIPHE_API_PORT=9800` (the one container). Or free the port (find the program: `sudo lsof -nP -iTCP:8443 -sTCP:LISTEN`). Then use the address and port the installer prints. |
| The person forgot the workspace's password | The `auth password-reset` command of §4 (Check the install), run by the person on the home machine: it prints a new setup code for `/setup`; nothing else is lost. |
| Windows: "Eriphe needs an administrator PowerShell" | Open Terminal (Admin) as in §4C and paste the line again. |
| Windows: Defender or SmartScreen blocks the script | Do not turn Defender off. Download `install.ps1` from the [latest release](https://github.com/Alexis-Marcel/eriphe-system/releases/latest), let the person check it, and run it from an administrator PowerShell with `powershell -ExecutionPolicy Bypass -File .\install.ps1 -CoreMode` (or the other parameters, see [install/README.md, Windows](../install/README.md#windows)). If Defender quarantines a file, open an issue rather than adding exclusions. |
| Windows: "Windows needs to restart…" | Restart, sign in as the same user: the install carries on in a window. If it does not, paste the same line again. |
| Windows: "…turn on virtualization in the PC's firmware" | §4C step 4. |
| Windows: other devices cannot reach the workspace | Set the network to Private (§4C step 6). |
| macOS: a folder listing fails or a task cannot read Documents | The "container-runtime-linux" prompt was refused: System Settings → Privacy & Security → Files and Folders → container-runtime-linux → allow. The first listing may time out while the question waits; the next one works. |
| macOS: other devices cannot reach the workspace on the Mac | The firewall prompt was refused: System Settings → Network → Firewall → Options → set `eriphe-core` (or `limactl`) to allow incoming connections. |
| The browser warns about the certificate | Expected (§5 step 2). |
| A device does not join | The join token works once, for the name it was made for, for 24 h: make a new one in the workspace. From that device, `curl http://<home address>:8800/health` must answer `ok`; if not, the home machine is off, on another network, or its firewall blocks 8800. A wrong `--ca-hash` stops the join on purpose: copy the command again. |
| A device was reinstalled and its name is taken | On its card: "Re-register this device" gives a command that takes the name over (on Windows it adds `-Rejoin`). |
| Unraid: the data folder or a share is refused | Paths are the NAS's own (`/mnt/user/...`), the data folder (`/mnt/user/appdata/eriphe`) must not be inside a share, and appdata and hidden folders are never shareable. |
| eriphe-core: `permission denied` on `/data` with a hand-made `docker run` | The folder must be writable by UID 1993: `sudo chown -R 1993:1993 <dir>`. The installers do this themselves. |
| "Docker on this computer is older than version 28" | Eriphe works, but tasks are less isolated: update Docker. |
| Linux: "this computer does not update itself (it has no systemd)" | Run the install command again from time to time to update it. |
| The phone app cannot install, or no notifications | They need a trusted address: Remote access (§7). |
| Google Drive: "redirect URI" refused | The workspace was opened at an IP address: use the Remote access address (§7, [docs/google-drive.md](google-drive.md)). |

**Logs**, to read when something fails (they hold no secret, but read them yourself rather than
asking the person to post them publicly):

| | eriphe-node | eriphe-core (home machine) | the install |
|---|---|---|---|
| Linux, one container | `docker logs eriphe-node` | `docker logs eriphe-core` (and `docker logs eriphe` for the launcher) | `/var/log/eriphe-install.log` |
| Mac, Apple's container | `~/.eriphe/node.log` | `~/.eriphe/core.log` | `~/.eriphe/install.log` |
| Mac, Lima | `LIMA_HOME=~/.eriphe/vm ~/.eriphe/lima/bin/limactl shell eriphe sudo docker logs eriphe-node` | the same with `eriphe-core` | `~/.eriphe/install.log` |
| Windows | `wsl -d eriphe -- tail /var/log/eriphe-node.log` | `wsl -d eriphe -- docker logs eriphe-core` | `C:\ProgramData\eriphe-node\install.log` |

Still stuck: open an issue at <https://github.com/Alexis-Marcel/eriphe-system/issues> with the
system, the command run and the end of the install log, after removing any address, name or
token the person does not want public. Security problems go through
[SECURITY.md](../SECURITY.md), not public issues.
