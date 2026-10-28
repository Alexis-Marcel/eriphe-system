# Installs Eriphe on Windows in its own WSL distribution, with Docker Engine
# inside: no Docker Desktop, nothing to install first, and it starts with
# Windows, without anyone logged in. Run it from an administrator PowerShell,
# straight from the web (parameters go through a script block):
#
#   & ([scriptblock]::Create((irm https://github.com/Alexis-Marcel/eriphe-system/releases/latest/download/install.ps1))) -CoreMode [-Name pc]
#   & ([scriptblock]::Create((irm https://github.com/Alexis-Marcel/eriphe-system/releases/latest/download/install.ps1))) -Core 'http://192.0.2.10:8800' -JoinToken <token> [-Name pc] [-Labels 'gpu'] [-CaHash sha256:<hex>]
#
# or saved as a file:
#
#   powershell -ExecutionPolicy Bypass -File .\install.ps1 -Core 'http://192.0.2.10:8800' -JoinToken <token>
#
# The first one runs eriphe-core here; "Add a machine" in the workspace gives
# the second one, token included. Guide: https://github.com/Alexis-Marcel/eriphe-system/blob/main/install/README.md
#
# Two modes. A node (-Core, -JoinToken): eriphe-node joins an eriphe-core
# that runs elsewhere. -CoreMode: eriphe-core runs here too, as a container
# in the distribution, reachable from the LAN, with this machine's node
# joined to it.
#
# Without WSL, the script installs it; when Windows needs a reboot for it,
# the script carries on by itself at the next logon. eriphe-node comes from
# its image (ghcr.io/alexis-marcel/eriphe-node), pulled in the distribution;
# -Binary gives a Linux eriphe-node binary instead.
#
# Run it again to update eriphe-node (and eriphe-core) or the labels; the
# node keeps its identity, and what is not given again is kept: a plain
# `irm https://github.com/Alexis-Marcel/eriphe-system/releases/latest/download/install.ps1 | iex` updates. -Rejoin, with a rejoin
# token for this name, drops that identity and joins again. -Uninstall removes the
# distribution (the node's identity, and eriphe-core's data in core mode),
# its scheduled task, and the port forwarding and firewall rule of core mode.
#
# Afterwards the machine updates itself when eriphe-core asks (Settings,
# Machines, Updates): an updater in the distribution, started with the node,
# pulls the new image, swaps the binary (and eriphe-core's container), and
# puts the previous one back if the node does not connect again. -Version
# 0.2.0 pins a release, -Version edge follows main (testers), -Version
# latest follows the releases again; -Rollback goes back to the version
# before the last update.
#
# Tasks run on their own Docker network, eriphe-tasks, which the node
# creates: they reach the node on its gateway, not one another nor the
# services on the default bridge.
param(
  [string] $Core,            # eriphe-core's address(es), comma-separated, preferred first
  [string] $JoinToken,
  [string] $Name,            # the node's name (default: this computer's)
  [string] $Labels,          # what agents can require of this node, comma-separated, e.g. gpu
  [string] $CaHash,          # sha256:<hex>, the eriphe-core CA to trust (TLS)
  [switch] $CoreMode,        # run eriphe-core here too, and join this node to it
  [string] $Binary,          # a Linux eriphe-node binary, instead of the one in its image
  [string] $Distro = "eriphe",
  [switch] $Rejoin,          # join again under -Name, with a rejoin token (the old secret stops working)
  [string] $Version,         # pin a release (0.2.0, or 0.2 for its fixes); edge follows main; latest the releases
  [switch] $Rollback,        # back to the version before the last update
  [switch] $Uninstall
)
$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"   # Windows PowerShell's progress bar slows downloads down
$env:WSL_UTF8 = "1"
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

$alpine = @{
  AMD64 = @("https://dl-cdn.alpinelinux.org/alpine/v3.22/releases/x86_64/alpine-minirootfs-3.22.7-x86_64.tar.gz",
            "57e364a241bab0699cd8a760c0fd7b270e9cbcbd3cee822cb2b07ca6d925c16c")
  ARM64 = @("https://dl-cdn.alpinelinux.org/alpine/v3.22/releases/aarch64/alpine-minirootfs-3.22.7-aarch64.tar.gz",
            "7e9b34ff3304381909e566e2c93de450664ccc724191261423974fc121c11623")
}
# The images, at the pinned version or latest (set once the saved settings
# are read); replaced by ERIPHE_NODE_IMAGE and ERIPHE_CORE_IMAGE, as with
# install.sh. eriphe-core never chooses them.
$registry = "ghcr.io/alexis-marcel"
$corePorts = 8800, 8443          # API (nodes, join), workspace
$home_ = Join-Path $env:ProgramData "eriphe-node"
# The first distribution keeps the names it always had; another one (-Distro)
# gets its own, so that both can live side by side.
if ($Distro -eq "eriphe") {
  $wslDir = Join-Path $home_ "wsl"
  $task = "Eriphe node"
} else {
  $wslDir = Join-Path $home_ "wsl-$Distro"
  $task = "Eriphe node ($Distro)"
}
$resumeTask = "Eriphe install ($Distro)"
$rule = "Eriphe core ($Distro)"
# This script's own text, for the resume after a reboot (empty under a bare iex).
$self = $MyInvocation.MyCommand.ScriptBlock

function Invoke-InDistro([string] $script) {
  # Sent as base64, so no quoting or line ending gets in the way. Returns
  # what the script prints; its errors go to the console.
  $b64 = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes(($script -replace "`r", "")))
  $out = wsl.exe -d $Distro -u root -- sh -c "echo $b64 | base64 -d | sh -e"
  if ($LASTEXITCODE -ne 0) { $out | Write-Host; throw "the setup in WSL failed ($LASTEXITCODE)" }
  $out
}

# WSL from the Store (the inbox wsl.exe of older Windows 10 knows no --version).
function Test-Wsl {
  cmd.exe /c "wsl.exe --version >nul 2>&1"
  $LASTEXITCODE -eq 0
}

function Test-Distro {
  @(wsl.exe --list --quiet | ForEach-Object { $_.Trim() }) -contains $Distro
}

# After a reboot that WSL needs, the same install, with the same parameters,
# at the next logon of this user, in a window that shows how it goes.
function Register-Resume {
  if (-not $self -or $self.ToString() -notmatch "function Register-Resume") { return $false }
  $dir = Join-Path $home_ "resume"
  New-Item -ItemType Directory -Force $dir | Out-Null
  # Only administrators and SYSTEM: this script runs elevated.
  icacls.exe $dir /inheritance:r /grant:r "*S-1-5-32-544:(OI)(CI)F" "*S-1-5-18:(OI)(CI)F" | Out-Null
  $file = Join-Path $dir "install.ps1"
  Set-Content -Path $file -Value $self.ToString() -Encoding UTF8
  $argv = foreach ($p in $bound.GetEnumerator()) {
    if ($p.Value -is [switch]) { if ($p.Value) { "-$($p.Key)" } }
    else { "-$($p.Key) '$($p.Value -replace "'", "''")'" }
  }
  $command = "& '$file' $($argv -join ' ')"
  $encoded = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($command))
  $me = [Security.Principal.WindowsIdentity]::GetCurrent().Name
  $action = New-ScheduledTaskAction -Execute "powershell.exe" -Argument "-NoProfile -ExecutionPolicy Bypass -NoExit -EncodedCommand $encoded"
  $trigger = New-ScheduledTaskTrigger -AtLogOn -User $me
  $principal = New-ScheduledTaskPrincipal -UserId $me -LogonType Interactive -RunLevel Highest
  Register-ScheduledTask -TaskName $resumeTask -Action $action -Trigger $trigger -Principal $principal -Force | Out-Null
  $true
}

function Unregister-Resume {
  if (Get-ScheduledTask -TaskName $resumeTask -ErrorAction SilentlyContinue) {
    Unregister-ScheduledTask -TaskName $resumeTask -Confirm:$false
  }
  Remove-Item (Join-Path $home_ "resume") -Recurse -Force -ErrorAction SilentlyContinue
}

# WSL itself: `wsl --install --no-distribution` turns on the Virtual Machine
# Platform and installs WSL (Windows 10 21H2 or later, 11, Home or Pro). True
# when it works; false when Windows must reboot first.
function Install-Wsl {
  $vmp = (Get-WindowsOptionalFeature -Online -FeatureName VirtualMachinePlatform).State
  if ($vmp -eq "Enabled" -and (Test-Wsl)) { return $true }
  if ($vmp -eq "Enabled") {
    # The feature is there (after the reboot): WSL itself may still be missing.
    wsl.exe --update
    if (Test-Wsl) { return $true }
  }
  if ($vmp -ne "EnablePending") {
    Write-Host "Installing WSL..."
    wsl.exe --install --no-distribution
    if ($LASTEXITCODE -ne 0) {
      # An older wsl.exe, without --no-distribution: the features, then `wsl --update` after the reboot.
      Enable-WindowsOptionalFeature -Online -NoRestart -All -FeatureName VirtualMachinePlatform, Microsoft-Windows-Subsystem-Linux | Out-Null
    }
    $vmp = (Get-WindowsOptionalFeature -Online -FeatureName VirtualMachinePlatform).State
    if ($vmp -eq "Enabled" -and (Test-Wsl)) { return $true }
  }
  return $false
}

# eriphe-core's ports, from the LAN. WSL 2 puts the distribution behind a NAT
# of its own, which only Windows' localhost reaches: Windows forwards the
# ports to the distribution's address (netsh portproxy, refreshed at every
# start, as that address changes), and the firewall lets them in from private
# networks. With mirrored networking (.wslconfig), the distribution has the
# machine's addresses and needs no forwarding.
function Test-Mirrored {
  $cfg = Join-Path $env:USERPROFILE ".wslconfig"
  (Test-Path $cfg) -and ((Get-Content $cfg -Raw) -match "(?im)^\s*networkingMode\s*=\s*mirrored")
}

function Remove-CorePorts {
  if (Get-NetFirewallRule -Name $rule -ErrorAction SilentlyContinue) {
    Remove-NetFirewallRule -Name $rule
    foreach ($p in $corePorts) { netsh.exe interface portproxy delete v4tov4 listenaddress=0.0.0.0 listenport=$p | Out-Null }
  }
}

function Get-LanAddress {
  $route = Get-NetRoute -DestinationPrefix "0.0.0.0/0" -ErrorAction SilentlyContinue |
    Sort-Object { $_.RouteMetric + $_.InterfaceMetric } | Select-Object -First 1
  if ($route) {
    Get-NetIPAddress -AddressFamily IPv4 -InterfaceIndex $route.ifIndex -ErrorAction SilentlyContinue |
      Select-Object -First 1 -ExpandProperty IPAddress
  }
}

$isAdmin = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole(
  [Security.Principal.WindowsBuiltInRole]::Administrator)
if (-not $isAdmin) { throw "run this from an administrator PowerShell" }
$bound = @{} + $PSBoundParameters

if ($Uninstall) {
  Unregister-Resume
  if (Get-ScheduledTask -TaskName $task -ErrorAction SilentlyContinue) {
    Stop-ScheduledTask -TaskName $task
    Unregister-ScheduledTask -TaskName $task -Confirm:$false
  }
  $wasCore = $false
  if ((Test-Wsl) -and (Test-Distro)) {
    $wasCore = (wsl.exe -d $Distro -u root -- sh -c "grep -qx ERIPHE_INSTALL_MODE=core /etc/eriphe-node.env && echo yes") -eq "yes"
    wsl.exe --unregister $Distro | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "wsl --unregister $Distro failed" }
  }
  Remove-Item $wslDir -Recurse -Force -ErrorAction SilentlyContinue
  Remove-CorePorts
  if ($wasCore) {
    Write-Host "The distribution $Distro, with eriphe-core and its data, and its scheduled task are removed."
  } else {
    Write-Host "The distribution $Distro and its scheduled task are removed."
    Write-Host "eriphe-core still knows the node: remove it under Machines in the workspace (or DELETE /v1/nodes/<name>)."
  }
  return
}

if ($Rejoin -and -not $JoinToken) { throw "-Rejoin needs -JoinToken (a rejoin token: eriphe-core join-token -name <name> -rejoin)" }
if ($CoreMode -and $Core) { throw "-CoreMode runs eriphe-core here: leave -Core out" }
if ($Binary -and -not (Test-Path $Binary)) { throw "no eriphe-node binary at $Binary" }
if ($Version -match "^v[0-9]") { $Version = $Version.Substring(1) }
if ($Version -and $Version -ne "latest" -and $Version -cnotmatch "^(edge|sha-[0-9a-f]{7,40}|(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?)?)$") {
  throw "-Version: latest, edge, or a release such as 0.2.0 (0.2 for its fixes)"
}

# Back to the version before the last update, with the updater the install left.
if ($Rollback) {
  if (-not ((Test-Wsl) -and (Test-Distro))) { throw "Eriphe is not installed here (no distribution $Distro)" }
  Invoke-InDistro @"
[ -x /usr/local/bin/eriphe-update ] || { echo "no updater here yet: run the install command once, without -Rollback" >&2; exit 1; }
eriphe-dockerd
eriphe-update rollback
dir=/var/lib/eriphe-node
grep -qx ERIPHE_INSTALL_MODE=core /etc/eriphe-node.env && dir=/var/lib/eriphe-core
sed -n 's/^outcome=//p; s/^message=\(..*\)/  \1/p; s/^from=\(..*\)/  from \1/p; s/^to=\(..*\)/  to \1/p' "`$dir/update-status"
"@ | Write-Host
  return
}

# 1. WSL itself (one reboot, after which this script carries on by itself).
if (-not (Install-Wsl)) {
  if (Register-Resume) {
    Write-Host "WSL is being installed: reboot Windows, the install carries on by itself at the next logon."
  } else {
    Write-Host "WSL is being installed: reboot Windows, then run this command again."
  }
  return
}
Unregister-Resume

# 2. The distribution: a minimal Alpine with Docker Engine.
$exists = Test-Distro
$saved = @{}
if ($exists) {
  # What an earlier run set, kept unless given again.
  wsl.exe -d $Distro -u root -- sh -c "cat /etc/eriphe-node.env 2>/dev/null; true" | ForEach-Object {
    if ($_ -match "^(ERIPHE_[A-Z_]+)=(.*)$") { $saved[$Matches[1]] = $Matches[2] }
  }
  $joined = (wsl.exe -d $Distro -u root -- sh -c "test -f /var/lib/eriphe-node/node.json && echo yes") -eq "yes"
} else {
  $joined = $false
}
if ($saved["ERIPHE_INSTALL_MODE"] -eq "core") {
  if ($Core) { throw "$Distro runs eriphe-core: -Uninstall it first to make it a plain node" }
  $CoreMode = [switch] $true
}
if (-not $bound.ContainsKey("Name")) {
  $Name = if ($saved["ERIPHE_NODE_NAME"]) { $saved["ERIPHE_NODE_NAME"] } else { ($env:COMPUTERNAME.ToLower() -replace "[^a-z0-9-]", "-").Trim("-") }
}
if (-not $bound.ContainsKey("Labels")) { $Labels = $saved["ERIPHE_NODE_LABELS"] }
if (-not $bound.ContainsKey("CaHash")) { $CaHash = $saved["ERIPHE_CA_HASH"] }
$pin = if ($bound.ContainsKey("Version")) { if ($Version -eq "latest") { "" } else { $Version } } else { $saved["ERIPHE_VERSION"] }
$tag = if ($pin) { $pin } else { "latest" }
$nodeImage = if ($env:ERIPHE_NODE_IMAGE) { $env:ERIPHE_NODE_IMAGE } else { "$registry/eriphe-node:$tag" }
$coreImage = if ($env:ERIPHE_CORE_IMAGE) { $env:ERIPHE_CORE_IMAGE } else { "$registry/eriphe-core:$tag" }
if (-not $Core -and -not $CoreMode) { $Core = $saved["ERIPHE_CORE"] }
if ($Name -notmatch "^[a-z0-9][a-z0-9-]*$") { throw "-Name ${Name}: lowercase letters, digits and dashes" }
if ($Labels -notmatch "^[A-Za-z0-9_.,=-]*$") { throw "-Labels ${Labels}: comma-separated words" }
if ($CaHash -and $CaHash -notmatch "^sha256:[0-9a-fA-F]{64}$") { throw "-CaHash: sha256:<64 hex digits>" }
if (-not $CoreMode) {
  if (-not $Core) { throw "give -Core <eriphe-core's address> and -JoinToken <token> (from: eriphe-core join-token), or -CoreMode" }
  if (-not $joined -and -not $JoinToken) { throw "a new node needs -JoinToken (from: eriphe-core join-token)" }
}

if (-not $exists) {
  $arch = $env:PROCESSOR_ARCHITECTURE
  if ($env:PROCESSOR_ARCHITEW6432) { $arch = $env:PROCESSOR_ARCHITEW6432 }
  if (-not $alpine.ContainsKey($arch)) { throw "unsupported processor: $arch" }
  New-Item -ItemType Directory -Force $home_ | Out-Null
  $rootfs = Join-Path $home_ "alpine-minirootfs.tar.gz"
  Invoke-WebRequest $alpine[$arch][0] -OutFile $rootfs -UseBasicParsing
  if ((Get-FileHash $rootfs -Algorithm SHA256).Hash -ne $alpine[$arch][1]) { throw "the Alpine archive does not match its checksum" }
  wsl.exe --import $Distro $wslDir $rootfs --version 2
  if ($LASTEXITCODE -ne 0) { throw "wsl --import failed (is virtualization turned on in the firmware?)" }
  Remove-Item $rootfs
}

# Its setup, again on every run so that an update brings the new scripts.
$changed = Invoke-InDistro @'
apk add --no-cache docker ca-certificates >/dev/null
# Windows stays out of reach of the tasks: no drives, no Windows programs.
cat > /tmp/wsl.conf <<'EOF'
[automount]
enabled = false
[interop]
enabled = false
appendWindowsPath = false
[user]
default = root
EOF
if ! cmp -s /tmp/wsl.conf /etc/wsl.conf; then mv /tmp/wsl.conf /etc/wsl.conf; echo restart; else rm /tmp/wsl.conf; fi
# Task containers reach the node through host.docker.internal: the bridge's
# address, set here because dockerd does not always find it at boot in WSL.
mkdir -p /etc/docker
cat > /etc/docker/daemon.json <<'EOF'
{"host-gateway-ips": ["172.17.0.1"]}
EOF
cat > /usr/local/bin/eriphe-dockerd <<'EOF'
#!/bin/sh
# Docker Engine, unless it runs already.
docker info >/dev/null 2>&1 && exit 0
# Detached from the WSL session: its end must not send dockerd a reload.
setsid dockerd >>/var/log/dockerd.log 2>&1 < /dev/null &
for i in $(seq 60); do docker info >/dev/null 2>&1 && exit 0; sleep 1; done
echo "dockerd does not start: see /var/log/dockerd.log" >&2
exit 1
EOF
cat > /usr/local/bin/eriphe-node-run <<'EOF'
#!/bin/sh
# Started by Windows at boot: Docker Engine, then eriphe-node.
# Once joined, the node has its own secret: the join token is dropped.
[ -f /var/lib/eriphe-node/node.json ] && sed -i '/^ERIPHE_JOIN_TOKEN=/d' /etc/eriphe-node.env
set -a
. /etc/eriphe-node.env
set +a
eriphe-dockerd
# The updater, next to the node: it applies eriphe-core's update requests.
pgrep -f /usr/local/bin/eriphe-update-loop >/dev/null || setsid /usr/local/bin/eriphe-update-loop </dev/null >/dev/null 2>&1 &
# Windows only restarts a task that fails to start: this loop restarts the node.
while true; do
  eriphe-node -root /var/lib/eriphe-node >>/var/log/eriphe-node.log 2>&1
  echo "eriphe-node exited ($?), restarting in 5 s" >>/var/log/eriphe-node.log
  sleep 5
done
EOF
cat > /usr/local/bin/eriphe-addr <<'EOF'
#!/bin/sh
# The distribution's address behind WSL's NAT, where Windows forwards eriphe-core's ports.
ip -4 addr show eth0 | sed -n 's/.*inet \([0-9.]*\).*/\1/p' | head -n 1
EOF
cat > /usr/local/bin/eriphe-core-run <<'EOF'
#!/bin/sh
# eriphe-core's container, from the image given (install.ps1, eriphe-update).
docker rm -f eriphe-core >/dev/null 2>&1 || true
docker run -d --name eriphe-core --restart unless-stopped --read-only --cap-drop ALL \
  --security-opt no-new-privileges:true -v /var/lib/eriphe-core:/data -e ERIPHE_INSTALL=windows \
  -p 8800:8800 -p 8443:8443 "$1" >/dev/null
EOF
cat > /usr/local/bin/eriphe-update-loop <<'EOF'
#!/bin/sh
# Started with eriphe-node: looks for an update request every 20 s.
while true; do
  /usr/local/bin/eriphe-update >>/var/log/eriphe-update.log 2>&1
  sleep 20
done
EOF
cat > /usr/local/bin/eriphe-update <<'EOF'
#!/bin/sh
# Applies the update requests of eriphe-core and eriphe-node, in the WSL
# distribution (install/README.md, Updates): eriphe-node's binary from its
# image, eriphe-core's container in core mode, each checked and put back
# as it was if it does not come back. "rollback" goes back to the version
# before the last update. eriphe-update-loop runs it every 20 s.
set -u
# shellcheck disable=SC1091
. /etc/eriphe-update.conf
STATE=/var/lib/eriphe-update
HEALTH=${ERIPHE_UPDATE_HEALTH:-180}
BIN=/usr/local/bin/eriphe-node
NODE_LOG=/var/log/eriphe-node.log
mkdir -p "$STATE"
chmod 700 "$STATE"
log() {
  printf '%s %s\n' "$(date '+%Y-%m-%d %H:%M:%S')" "$*" >>"$STATE/update.log"
  printf 'eriphe-update: %s\n' "$*" >&2
}
if command -v flock >/dev/null 2>&1; then
  exec 9>"$STATE/lock"
  flock -n 9 || exit 0
fi

dir_of() { if [ "$1" = core ]; then echo /var/lib/eriphe-core; else echo /var/lib/eriphe-node; fi; }

status() {
  f=$(dir_of "$1")/update-status
  msg=$(printf '%s' "$5" | tr '\n\r' '  ' | cut -c 1-300)
  printf 'id=%s\naction=%s\noutcome=%s\nmessage=%s\nfrom=%s\nto=%s\nat=%s\n' "$2" "$3" "$4" "$msg" "$6" "$7" "$(date +%s)" >"$f.new"
  chmod 600 "$f.new"
  [ "$1" = core ] && chown 1993:1993 "$f.new"
  mv "$f.new" "$f"
  [ "$4" = running ] || log "$1 $3 $2: $4${5:+ ($5)}"
}

label() {
  v=$(docker image inspect -f '{{index .Config.Labels "org.opencontainers.image.version"}}' "$1" 2>/dev/null)
  case $v in
    '' | '<no value>') printf '%s' "$1" | sed 's/^sha256://' | cut -c 1-12 ;;
    *) printf '%s' "$v" ;;
  esac
}
node_version() { cat "$BIN.version" 2>/dev/null || echo unknown; }
# tag_of VERSION: the images' tag of a build: X.Y.Z for a release,
# sha-<commit> for a build of main (X.Y.Z-dev+<commit>); none for a local one.
tag_of() {
  case $1 in
    *-dev+*) t=sha-${1##*+} ;;
    *+*) t= ;;
    *) t=$1 ;;
  esac
  printf '%s' "$t" | grep -Eq '^(sha-[0-9a-f]{7,40}|[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?)$' && printf '%s' "$t"
}
release() { printf '%s' "$1" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$'; }
# newer A B: release A is newer than release B.
newer() {
  printf '%s %s\n' "$1" "$2" | awk '{ split($1, a, "."); split($2, b, ".")
    for (i = 1; i <= 3; i++) { if (a[i] + 0 > b[i] + 0) exit 0; if (a[i] + 0 < b[i] + 0) exit 1 }
    exit 1 }'
}
# pinned: on a release of its own choice (edge follows main instead).
pinned() { [ -n "$PIN" ] && [ "$PIN" != edge ]; }

# node_healthy SIZE: eriphe-node, restarted by eriphe-node-run, says it is
# connected after byte SIZE of its log.
node_healthy() {
  i=0
  until tail -c +"$(($1 + 1))" "$NODE_LOG" 2>/dev/null | grep -q 'connected to '; do
    i=$((i + 2)); [ $i -lt "$HEALTH" ] || { why="eriphe-node is not connected after $HEALTH s"; return 1; }
    sleep 2
  done
}

# node_swap: the binary in place is started again, and checked.
node_swap() {
  size=$(wc -c <"$NODE_LOG" 2>/dev/null || echo 0)
  pkill -x eriphe-node || true
  node_healthy "$size"
}

core_healthy() {
  i=0
  until wget -q -O /dev/null http://127.0.0.1:8800/health 2>/dev/null; do
    i=$((i + 2)); [ $i -lt "$HEALTH" ] || { why="eriphe-core does not answer"; return 1; }
    sleep 2
  done
}

# core_swap ID: eriphe-core's container again, from image ID, and checked.
core_swap() {
  docker tag "$1" "$CORE_IMAGE"
  /usr/local/bin/eriphe-core-run "$CORE_IMAGE" || { why="eriphe-core does not start"; return 1; }
  core_healthy
}

# pull_node [TAG]: the binary of eriphe-node at TAG, eriphe-core's build
# (or, without, the latest, edge or pinned one), as $BIN.new, and its
# version in $to.
pull_node() {
  src=$NODE_IMAGE
  case ${1:-}:$NODE_IMAGE in ?*:*/eriphe-node:*) src=${NODE_IMAGE%:*}:$1 ;; esac
  docker pull -q "$src" >/tmp/eriphe-pull.log 2>&1 || { why="could not pull $src: $(tail -n 1 /tmp/eriphe-pull.log)"; return 1; }
  to=$(label "$src")
  c=$(docker create "$src") || { why="docker create failed"; return 1; }
  docker cp "$c:/usr/local/bin/eriphe-node" "$BIN.new" >/dev/null || { docker rm "$c" >/dev/null; why="docker cp failed"; return 1; }
  docker rm "$c" >/dev/null
  docker rmi "$src" >/dev/null 2>&1 || true
  chmod 755 "$BIN.new"
}

# update_node ID AUTO CORE [TAG]: without TAG, eriphe-core's build (the tag
# of CORE), unless pinned to a release.
update_node() {
  id=$1 auto=$2 core=$3 from=$(node_version)
  build=${4:-}
  if [ -z "$build" ] && ! pinned; then build=$(tag_of "$core"); fi
  status node "$id" update running "" "$from" ""
  pull_node "$build" || { status node "$id" update failed "$why" "$from" ""; return; }
  if cmp -s "$BIN.new" "$BIN"; then
    rm -f "$BIN.new"
    if pinned; then status node "$id" update pinned "pinned to $PIN" "$from" "$to"; else status node "$id" update current "" "$from" "$to"; fi
    return
  fi
  # A node is never newer than eriphe-core (a release pinned here may be).
  if release "$core" && release "$to" && newer "$to" "$core"; then
    rm -f "$BIN.new"
    status node "$id" update refused "eriphe-core runs $core: $to would be newer" "$from" "$to"
    return
  fi
  sum=$(sha256sum "$BIN.new" | cut -d ' ' -f 1)
  if [ "$auto" = 1 ] && grep -qx "$sum" "$STATE/failed" 2>/dev/null; then
    rm -f "$BIN.new"
    status node "$id" update skipped "$to failed here before" "$from" "$to"
    return
  fi
  cp "$BIN" "$STATE/node.before"
  mv "$BIN.new" "$BIN"
  echo "$to" >"$BIN.version"
  if node_swap; then
    mv "$STATE/node.before" "$STATE/node.previous"
    echo "$from" >"$STATE/node.previous.version"
    status node "$id" update ok "" "$from" "$to"
    return
  fi
  failed=$why
  echo "$sum" >>"$STATE/failed"
  mv "$STATE/node.before" "$BIN"
  echo "$from" >"$BIN.version"
  if node_swap; then status node "$id" update rolled_back "$failed" "$from" "$to"; else status node "$id" update rollback_failed "$failed; then: $why" "$from" "$to"; fi
}

rollback_node() {
  id=$1 from=$(node_version)
  [ -f "$STATE/node.previous" ] || { status node "$id" rollback failed "no previous version here" "$from" ""; return; }
  to=$(cat "$STATE/node.previous.version" 2>/dev/null || echo unknown)
  status node "$id" rollback running "" "$from" "$to"
  cp "$BIN" "$STATE/node.before"
  cp "$STATE/node.previous" "$BIN"
  echo "$to" >"$BIN.version"
  if node_swap; then
    sha256sum "$STATE/node.before" | cut -d ' ' -f 1 >>"$STATE/failed"
    mv "$STATE/node.before" "$STATE/node.previous"
    echo "$from" >"$STATE/node.previous.version"
    status node "$id" rollback ok "" "$from" "$to"
    return
  fi
  failed=$why
  mv "$STATE/node.before" "$BIN"
  echo "$from" >"$BIN.version"
  if node_swap; then status node "$id" rollback failed "$failed: back on $from" "$from" "$to"; else status node "$id" rollback rollback_failed "$failed; then: $why" "$from" "$to"; fi
}

# update_core ID AUTO: eriphe-core first, then this machine's node.
update_core() {
  id=$1 auto=$2
  have=$(docker inspect -f '{{.Image}}' eriphe-core 2>/dev/null)
  from=$(label "$have")
  status core "$id" update running "" "$from" ""
  if ! docker pull -q "$CORE_IMAGE" >/tmp/eriphe-pull.log 2>&1; then
    status core "$id" update failed "could not pull $CORE_IMAGE: $(tail -n 1 /tmp/eriphe-pull.log)" "$from" ""
    return
  fi
  want=$(docker image inspect -f '{{.Id}}' "$CORE_IMAGE")
  to=$(label "$want")
  # This machine's node: from the same build as eriphe-core (the tag of its
  # version), not its own latest, which may be half published.
  match=$(tag_of "$to")
  if [ "$want" = "$have" ]; then
    # eriphe-core is current: its node may not be.
    (update_node "$id-node" "$auto" "$to" "$match") # its own variables
    if pinned; then status core "$id" update pinned "pinned to $PIN" "$from" "$to"; else status core "$id" update current "" "$from" "$to"; fi
    return
  fi
  if [ "$auto" = 1 ] && grep -qx "$want" "$STATE/failed" 2>/dev/null; then
    docker tag "$have" "$CORE_IMAGE"
    status core "$id" update skipped "$to failed here before" "$from" "$to"
    return
  fi
  docker tag "$have" "${CORE_IMAGE%:*}:eriphe-previous"
  if core_swap "$want"; then
    echo "$have" >"$STATE/core.previous"
    (update_node "$id-node" "$auto" "$to" "$match") # its own variables
    status core "$id" update ok "" "$from" "$to"
    return
  fi
  failed=$why
  echo "$want" >>"$STATE/failed"
  if core_swap "$have"; then status core "$id" update rolled_back "$failed" "$from" "$to"; else status core "$id" update rollback_failed "$failed; then: $why" "$from" "$to"; fi
}

rollback_core() {
  id=$1
  have=$(docker inspect -f '{{.Image}}' eriphe-core 2>/dev/null)
  from=$(label "$have")
  prev=$(cat "$STATE/core.previous" 2>/dev/null)
  if [ -z "$prev" ] || ! docker image inspect "$prev" >/dev/null 2>&1; then
    status core "$id" rollback failed "no previous version here" "$from" ""
    return
  fi
  to=$(label "$prev")
  status core "$id" rollback running "" "$from" "$to"
  if core_swap "$prev"; then
    echo "$have" >"$STATE/core.previous"
    echo "$have" >>"$STATE/failed"
    docker tag "$have" "${CORE_IMAGE%:*}:eriphe-previous"
    (rollback_node "$id-node")
    status core "$id" rollback ok "" "$from" "$to"
    return
  fi
  failed=$why
  if core_swap "$have"; then status core "$id" rollback failed "$failed: back on $from" "$from" "$to"; else status core "$id" rollback rollback_failed "$failed; then: $why" "$from" "$to"; fi
}

field() { sed -n "s/^$2=//p" "$1" | head -n 1; }

if [ "${1:-}" = rollback ]; then
  if [ "$MODE" = core ]; then rollback_core "local-$(date +%s)"; else rollback_node "local-$(date +%s)"; fi
  exit 0
fi
for target in core node; do
  f=$(dir_of "$target")/update-requested
  [ -f "$f" ] || continue
  [ "$target" = node ] || [ "$MODE" = core ] || { rm -f "$f"; continue; }
  id=$(field "$f" id) action=$(field "$f" action) core=$(field "$f" core) auto=$(field "$f" auto)
  rm -f "$f"
  printf '%s' "$id" | grep -Eq '^[a-z0-9-]{1,40}$' || { log "$target: a request without a valid id"; continue; }
  printf '%s' "$core" | grep -Eq '^(sha-[0-9a-f]{7,40}|dev|[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?)?$' || core=
  [ "$auto" = 1 ] || auto=0
  case $target:$action in
    core:update) update_core "$id" "$auto" ;;
    core:rollback) rollback_core "$id" ;;
    node:update) update_node "$id" "$auto" "$core" ;;
    node:rollback) rollback_node "$id" ;;
    *) status "$target" "$id" "$action" failed "unknown action" "" "" ;;
  esac
done
EOF
chmod 755 /usr/local/bin/eriphe-dockerd /usr/local/bin/eriphe-node-run /usr/local/bin/eriphe-addr \
  /usr/local/bin/eriphe-core-run /usr/local/bin/eriphe-update-loop /usr/local/bin/eriphe-update
'@
if ($changed -contains "restart") { wsl.exe --terminate $Distro | Out-Null }

# 3. Docker Engine, for the images. The node keeps running until the new
# binary is there: a failed pull leaves it as it was.
Invoke-InDistro "eriphe-dockerd" | Out-Null

# Pulls an image, saying so when the registry refuses it.
$pull = @'
pull() {
  if ! docker pull -q "$1" >/tmp/pull.log 2>&1; then
    cat /tmp/pull.log >&2
    grep -qiE 'denied|unauthorized|not found' /tmp/pull.log && echo "the registry refuses $1: it may not be public (yet)" >&2
    exit 3
  fi
}
'@

# 4. The binary, next to the running one: from its image, or given (through
# stdin: the distribution does not see Windows' drives).
if ($Binary) {
  cmd.exe /c "wsl.exe -d $Distro -u root -- sh -c `"cat > /usr/local/bin/eriphe-node.new && chmod 755 /usr/local/bin/eriphe-node.new`" < `"$Binary`""
  if ($LASTEXITCODE -ne 0) { throw "copying eriphe-node failed" }
} else {
  Write-Host "Pulling $nodeImage..."
  Invoke-InDistro @"
$pull
pull $nodeImage
id=`$(docker create $nodeImage)
docker cp "`$id:/usr/local/bin/eriphe-node" /usr/local/bin/eriphe-node.new
docker rm "`$id" >/dev/null
docker image inspect -f '{{index .Config.Labels "org.opencontainers.image.version"}}' $nodeImage | sed 's/<no value>/unknown/' >/usr/local/bin/eriphe-node.version.new
docker rmi $nodeImage >/dev/null 2>&1 || true
chmod 755 /usr/local/bin/eriphe-node.new
"@ | Out-Null
}

# 5. Core mode: eriphe-core as a container, its data in /var/lib/eriphe-core,
# recreated when its image changes; the node joins it on the loopback.
if ($CoreMode) {
  Write-Host "Pulling $coreImage..."
  Invoke-InDistro @"
$pull
mkdir -p /var/lib/eriphe-core
chown 1993:1993 /var/lib/eriphe-core
chmod 700 /var/lib/eriphe-core
pull $coreImage
want=`$(docker image inspect -f '{{.Id}}' $coreImage)
have=`$(docker inspect -f '{{.Image}}' eriphe-core 2>/dev/null || true)
if [ "`$want" != "`$have" ]; then
  eriphe-core-run $coreImage
  docker image prune -f >/dev/null
else
  docker start eriphe-core >/dev/null
fi
for i in `$(seq 60); do wget -q -O /dev/null http://127.0.0.1:8800/health 2>/dev/null && exit 0; sleep 1; done
echo "eriphe-core does not answer on :8800: wsl -d $Distro -- docker logs eriphe-core" >&2
exit 1
"@ | Out-Null
  # The node turns this address to TLS by itself once eriphe-core offers it.
  $Core = "http://127.0.0.1:8800"
  if (-not $joined -or $Rejoin) {
    $joinArgs = "-name $Name -quiet"
    if ($Rejoin) { $joinArgs += " -rejoin" }
    # -quiet prints the token alone; eriphe-core may still be starting. On the
    # loopback the node needs no CA hash: it pins the CA of its first exchange.
    $JoinToken = Invoke-InDistro @"
for i in `$(seq 30); do
  if t=`$(docker exec eriphe-core eriphe-core join-token $joinArgs 2>/tmp/join.log) && [ -n "`$t" ]; then echo "`$t"; exit 0; fi
  sleep 2
done
cat /tmp/join.log >&2
exit 1
"@ | Select-Object -Last 1
    if (-not $JoinToken) { throw "no join token from eriphe-core" }
    $JoinToken = $JoinToken.Trim()
  }
} else {
  Remove-CorePorts
}

# 6. Stop the node, put the new binary in place, and its settings; the join
# token only matters until the node has joined.
Stop-ScheduledTask -TaskName $task -ErrorAction SilentlyContinue
Invoke-InDistro "pkill -f /usr/local/bin/eriphe-node-run || true; pkill -x eriphe-node || true" | Out-Null
$mode = if ($CoreMode) { "core" } else { "node" }
$alwaysOn = if ($CoreMode) { "ERIPHE_ALWAYS_ON=1" } else { "" }
$forget = if ($Rejoin) { "rm -f /var/lib/eriphe-node/node.json" } else { "" }
$token = if ($Rejoin -or -not $joined) { $JoinToken } else { "" }
# The updater works on these images; the files "updater" tell eriphe-core
# and the node that it is there (not with -Binary: it would replace that one).
$updater = if ($Binary) { "rm -f /var/lib/eriphe-node/updater /var/lib/eriphe-core/updater" } else {
  # pin: the release it stays on; channel: edge when it follows main.
  $mark = if ($pin -eq "edge") { "printf 'version=1\npin=\nchannel=edge\n'" } else { "printf 'version=1\npin=%s\nchannel=\n' '$pin'" }
  $marks = "$mark > /var/lib/eriphe-node/updater"
  if ($CoreMode) { $marks += "; $mark > /var/lib/eriphe-core/updater; chown 1993:1993 /var/lib/eriphe-core/updater" }
  $marks
}
$version = if ($Binary) { "echo custom > /usr/local/bin/eriphe-node.version" } else { "mv /usr/local/bin/eriphe-node.version.new /usr/local/bin/eriphe-node.version" }
Invoke-InDistro @"
mv /usr/local/bin/eriphe-node.new /usr/local/bin/eriphe-node
$version
umask 077
$forget
cat > /etc/eriphe-update.conf <<'EOF'
MODE='$mode' PIN='$pin'
NODE_IMAGE='$nodeImage' CORE_IMAGE='$coreImage'
EOF
$updater
cat > /etc/eriphe-node.env <<'EOF'
ERIPHE_INSTALL_MODE=$mode
ERIPHE_INSTALL=windows
ERIPHE_CORE=$Core
ERIPHE_NODE_NAME=$Name
ERIPHE_NODE_LABELS=$Labels
ERIPHE_CA_HASH=$CaHash
ERIPHE_JOIN_TOKEN=$token
ERIPHE_VERSION=$pin
$alwaysOn
EOF
"@ | Out-Null

# 7. Start at boot, logged in or not, and start again if WSL stops. In core
# mode, the ports are forwarded again first: the distribution's address
# changes with every start of WSL.
$mirrored = Test-Mirrored
$forward = ""
if ($CoreMode -and -not $mirrored) {
  $forward = @"
  `$ip = wsl.exe -d $Distro -u root --exec /usr/local/bin/eriphe-addr
  if (`$ip) { foreach (`$p in $($corePorts -join ', ')) { netsh.exe interface portproxy add v4tov4 listenaddress=0.0.0.0 listenport=`$p connectaddress=`$ip connectport=`$p | Out-Null } }
"@
}
$loop = @"
`$env:WSL_UTF8 = '1'
while (`$true) {
$forward
  wsl.exe -d $Distro -u root --exec /usr/local/bin/eriphe-node-run
  Start-Sleep -Seconds 5
}
"@
$encoded = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($loop))
$action = New-ScheduledTaskAction -Execute "powershell.exe" -Argument "-NoProfile -NonInteractive -WindowStyle Hidden -EncodedCommand $encoded"
$trigger = New-ScheduledTaskTrigger -AtStartup
$settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -StartWhenAvailable `
  -ExecutionTimeLimit ([TimeSpan]::Zero) -RestartCount 999 -RestartInterval (New-TimeSpan -Minutes 1)
$principal = New-ScheduledTaskPrincipal -UserId ([Security.Principal.WindowsIdentity]::GetCurrent().Name) -LogonType S4U -RunLevel Highest
Register-ScheduledTask -TaskName $task -Action $action -Trigger $trigger -Settings $settings -Principal $principal -Force | Out-Null

if ($CoreMode) {
  if (-not (Get-NetFirewallRule -Name $rule -ErrorAction SilentlyContinue)) {
    New-NetFirewallRule -Name $rule -DisplayName $rule -Description "eriphe-core in WSL ($Distro): its API and workspace" `
      -Direction Inbound -Protocol TCP -LocalPort $corePorts -Action Allow -Profile Private, Domain | Out-Null
  }
  if ($mirrored) {
    Write-Host "Mirrored networking: if the LAN cannot reach ports $($corePorts -join ' and '), allow them in the Hyper-V firewall (New-NetFirewallHyperVRule)."
  } else {
    $ip = wsl.exe -d $Distro -u root --exec /usr/local/bin/eriphe-addr
    foreach ($p in $corePorts) { netsh.exe interface portproxy add v4tov4 listenaddress=0.0.0.0 listenport=$p connectaddress=$ip connectport=$p | Out-Null }
  }
}
Start-ScheduledTask -TaskName $task

# 8. Wait for the node to join, and say where things are.
for ($i = 0; $i -lt 30; $i++) {
  $joined = (wsl.exe -d $Distro -u root -- sh -c "test -f /var/lib/eriphe-node/node.json && pgrep -x eriphe-node >/dev/null && echo yes") -eq "yes"
  if ($joined) { break }
  Start-Sleep -Seconds 2
}
if ($joined) {
  Write-Host "eriphe-node is installed as '$Name' and starts with Windows."
} else {
  Write-Host "eriphe-node is installed as '$Name' and starts with Windows, but has not joined eriphe-core yet: see its log."
}
Write-Host "Logs: wsl -d $Distro -- tail /var/log/eriphe-node.log"
if ($CoreMode) {
  $lan = Get-LanAddress
  if (-not $lan) { $lan = "<this machine>" }
  $setup = wsl.exe -d $Distro -u root -- sh -c "docker logs eriphe-core 2>&1 | sed -n 's/.*setup code \([A-Za-z0-9-]*\).*/\1/p' | tail -n 1"
  Write-Host ""
  Write-Host "eriphe-core runs here. Workspace: https://${lan}:8443"
  if ($setup) { Write-Host "Setup code (to choose the workspace's password): $setup" }
  Write-Host "Other machines join it at http://${lan}:8800, with a token from:"
  Write-Host "  wsl -d $Distro -- docker exec eriphe-core eriphe-core join-token -name <name>"
  if (Get-NetConnectionProfile | Where-Object NetworkCategory -eq "Public") {
    Write-Host "This network is Public: the firewall lets the LAN in on Private networks only (Set-NetConnectionProfile -NetworkCategory Private)."
  }
}
