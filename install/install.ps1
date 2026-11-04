# Installs Eriphe on Windows in its own WSL distribution, with Docker Engine
# inside: no Docker Desktop, nothing to install first, and it starts with
# Windows, without anyone logged in. Run it from an administrator PowerShell,
# straight from the web, its parameters in ERIPHE_* variables (irm | iex
# takes none):
#
#   $env:ERIPHE_CORE_MODE='1'; irm https://github.com/Alexis-Marcel/eriphe-system/releases/latest/download/install.ps1 | iex
#   $env:ERIPHE_CORE='https://192.0.2.10:8800'; $env:ERIPHE_JOIN_TOKEN='<token>'; $env:ERIPHE_NODE_NAME='pc'; $env:ERIPHE_CA_HASH='sha256:<hex>'; irm https://github.com/Alexis-Marcel/eriphe-system/releases/latest/download/install.ps1 | iex
#
# or with parameters, through a script block or saved as a file:
#
#   & ([scriptblock]::Create((irm https://github.com/Alexis-Marcel/eriphe-system/releases/latest/download/install.ps1))) -Core 'http://192.0.2.10:8800' -JoinToken <token> [-Name pc] [-Labels 'gpu'] [-CaHash sha256:<hex>]
#   powershell -ExecutionPolicy Bypass -File .\install.ps1 -Core 'http://192.0.2.10:8800' -JoinToken <token>
#
# Each parameter has its variable: -Core ERIPHE_CORE, -JoinToken
# ERIPHE_JOIN_TOKEN, -Name ERIPHE_NODE_NAME, -Labels ERIPHE_NODE_LABELS,
# -CaHash ERIPHE_CA_HASH, -Version ERIPHE_VERSION, -Distro ERIPHE_DISTRO,
# -AssistantName ERIPHE_ASSISTANT_NAME (only for what it prints), -WebPort
# ERIPHE_WEB_PORT and -ApiPort ERIPHE_API_PORT (core mode: the workspace's
# port, 8443, and the one other devices join on, 8800; kept), -ModelServer
# ERIPHE_MODEL_SERVER (a model server this node lends through its tunnel, as
# the node in its container reaches it: http://eriphe-ollama:11434 for an
# Ollama container on Eriphe's network; none for none; kept), and the
# switches -CoreMode, -Rejoin, -Rollback and -Uninstall
# ERIPHE_CORE_MODE, ERIPHE_REJOIN, ERIPHE_ROLLBACK and ERIPHE_UNINSTALL,
# set to 1. A parameter given wins over its variable; the script clears
# them, so that a later run does not take them again. An unknown parameter
# or ERIPHE_* variable stops it before it does anything.
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
# the script carries on by itself at the next logon. The distribution is a
# Linux machine with Docker that runs Eriphe's launcher
# (ghcr.io/alexis-marcel/eriphe), as on any Linux (install/README.md, One
# container): eriphe-node runs in a container, behind eriphe-node engine,
# the only one with the Docker socket; eriphe-core too in core mode. What
# only Windows can do stays here: the scheduled tasks, the session helper,
# the Explorer's menu, the network card to wake, the ports of core mode.
#
# Run it again to update Eriphe or the labels; the node keeps its identity,
# and what is not given again is kept: a plain
# `irm https://github.com/Alexis-Marcel/eriphe-system/releases/latest/download/install.ps1 | iex` updates. -Rejoin, with a rejoin
# token for this name, drops that identity and joins again. -Uninstall removes the
# distribution (the node's identity, and eriphe-core's data in core mode),
# its scheduled task, and the port forwarding and firewall rule of core mode.
# An install from before the launcher (eriphe-node a binary in the
# distribution) moves to it, its data and the folders shared kept.
#
# Afterwards the machine updates itself when eriphe-core asks (Settings,
# Machines, Updates): the launcher applies the update, and puts the previous
# version back if the node does not connect again. -Version 0.2.0 pins a
# release, -Version edge follows main (testers), -Version latest follows
# the releases again; -Rollback goes back to the version before the last
# update.
#
# Tasks run on their own Docker network, eriphe-tasks: they reach the node,
# not one another nor this PC's services.
[CmdletBinding()]
param(
  [string] $Core,            # eriphe-core's address(es), comma-separated, preferred first
  [string] $JoinToken,
  [string] $Name,            # the node's name (default: this computer's)
  [string] $Labels,          # what agents can require of this node, comma-separated, e.g. gpu
  [string] $CaHash,          # sha256:<hex>, the eriphe-core CA to trust (TLS)
  [switch] $CoreMode,        # run eriphe-core here too, and join this node to it
  [string] $Distro = "eriphe",
  [switch] $Rejoin,          # join again under -Name, with a rejoin token (the old secret stops working)
  [string] $Version,         # pin a release (0.2.0, or 0.2 for its fixes); edge follows main; latest the releases
  [switch] $Rollback,        # back to the version before the last update
  [switch] $Uninstall,
  [string] $AssistantName,   # the assistant's name, for what this script prints
  [string] $WebPort,         # core mode: the workspace's port (default 8443, kept for the next runs)
  [string] $ApiPort,         # core mode: the port other devices join on (default 8800, kept)
  [string] $ModelServer      # a model server this node lends through its tunnel, as the node in its container reaches it; "none" for none (kept)
)
$bound = @{} + $PSBoundParameters
# This script's own text, for the resume after a reboot (none under irm | iex:
# it is downloaded again from its release).
$self = $MyInvocation.MyCommand.ScriptBlock
# The rest runs in a scope of its own: under irm | iex the script runs in the
# person's window, which would keep its variables and preferences
# ($ErrorActionPreference = "Stop") after it ends.
& {
$ErrorActionPreference = "Stop"
# The parameters' ERIPHE_* variables (see above), read once and cleared.
$fromEnv = @{
  ERIPHE_CORE = "Core"; ERIPHE_JOIN_TOKEN = "JoinToken"; ERIPHE_NODE_NAME = "Name"; ERIPHE_NODE_LABELS = "Labels"
  ERIPHE_CA_HASH = "CaHash"; ERIPHE_VERSION = "Version"; ERIPHE_DISTRO = "Distro"; ERIPHE_ASSISTANT_NAME = "AssistantName"
  ERIPHE_WEB_PORT = "WebPort"; ERIPHE_API_PORT = "ApiPort"; ERIPHE_MODEL_SERVER = "ModelServer"
  ERIPHE_CORE_MODE = "CoreMode"; ERIPHE_REJOIN = "Rejoin"; ERIPHE_ROLLBACK = "Rollback"; ERIPHE_UNINSTALL = "Uninstall"
}
foreach ($v in @(Get-ChildItem Env: | Where-Object Name -like "ERIPHE_*")) {
  if ($v.Name -in "ERIPHE_NODE_IMAGE", "ERIPHE_LAUNCHER_IMAGE") { continue }
  $p = $fromEnv[$v.Name]
  if (-not $p) { throw "unknown variable $($v.Name) (known: $(($fromEnv.Keys | Sort-Object) -join ', '))" }
  Remove-Item "Env:$($v.Name)"
  if ($bound.ContainsKey($p)) { continue }
  if ($p -in "CoreMode", "Rejoin", "Rollback", "Uninstall") {
    if ($v.Value -ne "1") { throw "$($v.Name): 1, or leave it out" }
    $bound[$p] = [switch] $true
  } else {
    $bound[$p] = $v.Value
  }
  Set-Variable -Name $p -Value $bound[$p]
}
$ProgressPreference = "SilentlyContinue"   # Windows PowerShell's progress bar slows downloads down
$env:WSL_UTF8 = "1"
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

$alpine = @{
  AMD64 = @("https://dl-cdn.alpinelinux.org/alpine/v3.22/releases/x86_64/alpine-minirootfs-3.22.7-x86_64.tar.gz",
            "57e364a241bab0699cd8a760c0fd7b270e9cbcbd3cee822cb2b07ca6d925c16c")
  ARM64 = @("https://dl-cdn.alpinelinux.org/alpine/v3.22/releases/aarch64/alpine-minirootfs-3.22.7-aarch64.tar.gz",
            "7e9b34ff3304381909e566e2c93de450664ccc724191261423974fc121c11623")
}
# The install's layout, as install.sh's LAYOUT, which node/update.go's
# installLayout is too. The node image carries this script
# (/usr/local/share/eriphe/install.ps1): the updater of an install from
# before the launcher brings it to a newer layout by itself, with this
# script's setup of the distribution ($changed). $adminLayout is what it
# puts on Windows' side (the scheduled tasks, the session helper, the
# firewall, the Explorer's menu): raise it with a release that changes that,
# which only this script, in an administrator PowerShell, can do; the
# device then says so to its user.
$layout = 4
$adminLayout = 1
# The images, at the pinned version or latest (set once the saved settings
# are read): the launcher's, which chooses the others, and the node's, for
# the session helper it carries; replaced by ERIPHE_LAUNCHER_IMAGE and
# ERIPHE_NODE_IMAGE (tests). eriphe-core never chooses them.
$registry = "ghcr.io/alexis-marcel"
$corePorts = 8800, 8443          # API (nodes, join), workspace: -ApiPort and -WebPort, or kept
$home_ = Join-Path $env:ProgramData "eriphe-node"
# The first distribution keeps the names it always had; another one (-Distro)
# gets its own, so that both can live side by side.
if ($Distro -eq "eriphe") {
  $wslDir = Join-Path $home_ "wsl"
  $task = "Eriphe node"
  $awakeTask = "Eriphe awake"
} else {
  $wslDir = Join-Path $home_ "wsl-$Distro"
  $task = "Eriphe node ($Distro)"
  $awakeTask = "Eriphe awake ($Distro)"
}
$resumeTask = "Eriphe install ($Distro)"
$rule = "Eriphe core ($Distro)"
# What the scheduled task runs in core mode, forwarding included (step 7).
$bootDir = Join-Path $home_ "boot"
$boot = Join-Path $bootDir "$Distro.ps1"
# What keeps the PC awake while a task is at work (step 7).
$awake = Join-Path $bootDir "$Distro-awake.ps1"
# The session helper (step 7): one for the PC, whatever the distribution.
$sessionTask = "Eriphe session"
$helperDir = Join-Path $home_ "bin"
$helperExe = Join-Path $helperDir "eriphe-session.exe"
$sessionDir = Join-Path $home_ "session"
$selfUrl = "https://github.com/Alexis-Marcel/eriphe-system/releases/latest/download/install.ps1"

# What it prints: a title, short steps, the outcome and where the logs are,
# as install.sh. Everything else, the output of every command included, goes
# to its log.
$log = Join-Path $home_ "install.log"
$name_ = ($AssistantName -replace "[\x00-\x1f]", "").Trim()
if (-not $name_) { $name_ = "Eriphe" }
# A check mark where the console can show it (Windows Terminal), a root sign
# elsewhere: the old console's fonts lack the check mark.
$check = if ($env:WT_SESSION) { [char]0x2713 } else { [char]0x221A }
$notes = New-Object Collections.Generic.List[string]

function Write-Log([string] $text) {
  try { Add-Content -Path $log -Value $text -Encoding UTF8 } catch { $null = $_ }
}

function Write-Out([string] $text = "") {
  Write-Host $text
  Write-Log $text
}

# A line ending with a secret (the setup code): on the screen only; the log
# says it was printed.
function Write-Secret([string] $text, [string] $secret) {
  Write-Host "$text$secret"
  Write-Log "$text(printed on the screen, not logged)"
}

# A paragraph, wrapped.
function Write-Para([string] $text) {
  $line = ""
  foreach ($w in $text -split " ") {
    if ($line -and ($line.Length + 1 + $w.Length) -gt 66) { Write-Out $line; $line = $w }
    elseif ($line) { $line += " $w" } else { $line = $w }
  }
  if ($line) { Write-Out $line }
}

function Write-Step([string] $step) { Write-Out "  $check $step" }

# Something to know, said after the outcome.
function Add-Notice([string] $text) {
  $notes.Add($text)
  Write-Log "warning: $text"
}

function Write-GoodToKnow {
  if ($notes.Count -eq 0) { return }
  Write-Out
  if ($notes.Count -eq 1) { Write-Para "Good to know: $($notes[0])"; return }
  Write-Out "Good to know:"
  foreach ($n in $notes) {
    $n = $n.Substring(0, 1).ToUpper() + $n.Substring(1)
    $first = $true
    $line = ""
    foreach ($w in $n -split " ") {
      if ($line -and ($line.Length + 1 + $w.Length) -gt 62) {
        if ($first) { Write-Out "  - $line" } else { Write-Out "    $line" }
        $first = $false
        $line = $w
      } elseif ($line) { $line += " $w" } else { $line = $w }
    }
    if ($first) { Write-Out "  - $line" } else { Write-Out "    $line" }
  }
}

# Runs a Windows command with its output, and the line it prints when it
# went well ("The operation completed successfully."), in the log only.
function Invoke-Quiet([scriptblock] $command) {
  $ErrorActionPreference = "Continue"
  & $command 2>&1 | ForEach-Object { Write-Log "$_" }
}

# How an update or a rollback went (update-status), in a sentence.
function Get-StatusLine([string[]] $status) {
  $s = @{}
  foreach ($l in $status) { if ($l -match "^([a-z]+)=(.*)$") { $s[$Matches[1]] = $Matches[2] } }
  $from, $to, $why = $s["from"], $s["to"], $s["message"]
  switch ("$($s["action"]):$($s["outcome"])") {
    "rollback:ok" { "Back to $to." }
    "rollback:rollback_failed" { "Could not go back, nor to $from again: $why." }
    default { "Could not go back to the previous version: $(if ($why) { $why } else { $s["outcome"] })." }
  }
}

# A failure: what went wrong in a sentence, where the details are; the
# script stops there (exit code 1 when run as a file).
trap {
  $m = $_.Exception.Message
  Write-Log "error: $m"
  Write-Log $_.ScriptStackTrace
  if ($m -notmatch "[.!?]$") { $m += "." }
  Write-Host ""
  Write-Para "Something went wrong: $m"
  if ($log -and (Test-Path $log)) {
    Write-Host ""
    Write-Host "Details: $log"
  }
  if ($PSCommandPath) { exit 1 }
  return
}

function Invoke-InDistro([string] $script) {
  # Read by sh on its stdin, in UTF-8, with Unix line endings: in braces, so
  # that sh reads it whole before it runs, and its commands read nothing of
  # it. PowerShell ends what it pipes with a CRLF: a last comment line takes
  # it. Returns what the script prints; its errors go to the console.
  # What it writes on stderr goes to the log; its last "eriphe: " line says
  # what went wrong.
  $OutputEncoding = New-Object Text.UTF8Encoding $false
  $ErrorActionPreference = "Continue"
  # WSL's service may time out starting the distribution (Wsl/Service/
  # CreateInstance/ERROR_TIMEOUT), right after an uninstall or on a slow PC:
  # the script did not run then, and is given once more.
  for ($try = 1; $try -le 2; $try++) {
    $all = "{`n$($script -replace "`r", '')`n} </dev/null`n#" | wsl.exe -d $Distro -u root -- sh -e 2>&1
    $code = $LASTEXITCODE
    if ($code -eq 0 -or "$all" -notmatch "Wsl/Service/\S*ERROR_TIMEOUT") { break }
    Write-Log "$all"
    Start-Sleep -Seconds 5
  }
  $ErrorActionPreference = "Stop"
  $out = @()
  $why = $null
  foreach ($l in $all) {
    if ($l -is [Management.Automation.ErrorRecord]) {
      Write-Log "$l"
      if ("$l" -match "^eriphe: (.+)$") { $why = $Matches[1] }
    } else {
      $out += $l
    }
  }
  if ($code -ne 0) {
    $out | ForEach-Object { Write-Log "$_" }
    if ($why) { throw $why }
    # WSL's own error code, when WSL itself failed.
    if ("$all" -match "(Wsl/[A-Za-z0-9_/]+)") { throw "WSL failed ($($Matches[1])): restart the PC, then run the command again" }
    throw "the setup in WSL failed (exit code $code)"
  }
  $out
}

# WSL from the Store (the inbox wsl.exe of older Windows 10 knows no --version).
function Test-Wsl {
  cmd.exe /c "wsl.exe --version >nul 2>&1"
  $LASTEXITCODE -eq 0
}

# A file from the web. curl.exe (in Windows since 10 1803) gives up on a
# transfer that stalls and tries again: on a new PC the network blinks when
# WSL starts for the first time, and Invoke-WebRequest then waits for minutes.
function Save-Download([string] $url, [string] $file) {
  $curl = Join-Path $env:SystemRoot "System32\curl.exe"
  if (-not (Test-Path $curl)) {
    Invoke-WebRequest $url -OutFile $file -UseBasicParsing -TimeoutSec 300
    return
  }
  Invoke-Quiet { & $curl -fsSL --connect-timeout 30 --speed-limit 1024 --speed-time 30 --retry 5 --retry-delay 2 -o $file $url }
  if ($LASTEXITCODE -ne 0) { throw "curl.exe could not download $url (exit code $LASTEXITCODE)" }
}

function Test-Distro {
  @(wsl.exe --list --quiet | ForEach-Object { $_.Trim() }) -contains $Distro
}

# After a reboot that WSL needs, the same install, with the same parameters,
# at the next logon of this user, in a window that shows how it goes.
function Register-Resume {
  $dir = Join-Path $home_ "resume"
  New-Item -ItemType Directory -Force $dir | Out-Null
  # Only administrators and SYSTEM: this script runs elevated.
  Invoke-Quiet { icacls.exe $dir /inheritance:r /grant:r "*S-1-5-32-544:(OI)(CI)F" "*S-1-5-18:(OI)(CI)F" }
  $file = Join-Path $dir "install.ps1"
  if ($self -and $self.ToString() -match "function Register-Resume") {
    Set-Content -Path $file -Value $self.ToString() -Encoding UTF8
  } else {
    try { Save-Download $selfUrl $file } catch { Write-Log "$_"; return $false }
  }
  $argv = foreach ($p in $bound.GetEnumerator()) {
    if ($p.Value -is [switch]) { if ($p.Value) { "-$($p.Key)" } }
    else { "-$($p.Key) `"$($p.Value -replace '"', '\"')`"" }
  }
  $me = [Security.Principal.WindowsIdentity]::GetCurrent().Name
  $action = New-ScheduledTaskAction -Execute "powershell.exe" -Argument "-NoProfile -ExecutionPolicy Bypass -NoExit -File `"$file`" $($argv -join ' ')"
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
    Write-Para "Windows is downloading WSL, the Linux layer Eriphe runs in. It can take several minutes: leave this window open."
    Invoke-Quiet { wsl.exe --update }
    if (Test-Wsl) { return $true }
  }
  if ($vmp -ne "EnablePending") {
    # Long and silent on a new PC (Windows finishes its own updates first):
    # said before it starts, so that the window does not look stuck.
    Write-Para "Windows is turning on WSL, the Linux layer Eriphe runs in. On a new PC it can take a quarter of an hour: leave this window open."
    Invoke-Quiet { wsl.exe --install --no-distribution }
    if ($LASTEXITCODE -ne 0) {
      # An older wsl.exe, without --no-distribution: the features, then `wsl --update` after the reboot.
      # Its warning that the restart is left for later goes to the log: the
      # script says so itself.
      Enable-WindowsOptionalFeature -Online -NoRestart -All -FeatureName VirtualMachinePlatform, Microsoft-Windows-Subsystem-Linux -WarningAction SilentlyContinue -WarningVariable warned | Out-Null
      $warned | ForEach-Object { Write-Log "$_" }
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
  (Test-Path $cfg) -and (Test-MirroredText (Get-Content $cfg -Raw))
}

# A .wslconfig's text sets mirrored networking: networkingMode=mirrored in its
# [wsl2] section, the only one WSL reads it in.
function Test-MirroredText([string] $text) {
  $section = ""
  foreach ($line in ($text -split "`r?`n")) {
    $l = $line.Trim()
    if ($l -match "^\[(.+)\]$") { $section = $Matches[1].Trim().ToLower(); continue }
    if ($section -eq "wsl2" -and $l -match "^networkingMode\s*=\s*mirrored\s*([#;].*)?$") { return $true }
  }
  return $false
}

# -ModelServer: an http(s) address, its host a name, an IPv4 address or a
# bracketed IPv6 one (http://[::1]:11434).
function Test-ModelServerAddress([string] $url) {
  $url -match "^https?://([A-Za-z0-9._~-]+|\[[0-9A-Fa-f:.]+\])(:[0-9]{1,5})?(/[A-Za-z0-9._~/-]*)?$"
}

# An address of this PC's own loopback, which the node, in its container,
# does not reach: its loopback is the container's.
function Test-LoopbackAddress([string] $url) {
  $url -match "^https?://(localhost|127\.[0-9.]+|0\.0\.0\.0|\[::1?\])([:/]|$)"
}

function Remove-CorePorts {
  if (Get-NetFirewallRule -Name $rule -ErrorAction SilentlyContinue) {
    Remove-NetFirewallRule -Name $rule
    foreach ($p in $corePorts) { Invoke-Quiet { netsh.exe interface portproxy delete v4tov4 listenaddress=0.0.0.0 listenport=$p } }
  }
}

# The ports eriphe-core's container is published on, as an earlier core-mode
# install left them (API, WEB; none before they could be chosen).
function Get-SavedPorts {
  $ports = @{}
  if ((Test-Wsl) -and (Test-Distro)) {
    wsl.exe -d $Distro -u root -- sh -c "cat /etc/eriphe-core.ports 2>/dev/null; true" | ForEach-Object {
      if ($_ -match "^(API|WEB)_PORT=([0-9]+)$") { $ports[$Matches[1]] = [int] $Matches[2] }
    }
  }
  $ports
}

# Who listens on a port of this PC: a program's name, or nothing.
function Get-PortUser([int] $port) {
  $c = Get-NetTCPConnection -State Listen -LocalPort $port -ErrorAction SilentlyContinue | Select-Object -First 1
  if (-not $c) { return $null }
  $p = Get-Process -Id $c.OwningProcess -ErrorAction SilentlyContinue
  if ($p) { $p.ProcessName } else { "another program" }
}

function Get-LanAddress {
  $route = Get-NetRoute -DestinationPrefix "0.0.0.0/0" -ErrorAction SilentlyContinue |
    Sort-Object { $_.RouteMetric + $_.InterfaceMetric } | Select-Object -First 1
  if ($route) {
    Get-NetIPAddress -AddressFamily IPv4 -InterfaceIndex $route.ifIndex -ErrorAction SilentlyContinue |
      Select-Object -First 1 -ExpandProperty IPAddress
  }
}

# This PC's network card towards eriphe-core, so that eriphe-core can wake it
# (Wake-on-LAN, node/wake.go): its MAC address(es) and its network's
# broadcast address, as "mac[,mac] broadcast", or nothing. The card of the
# route to eriphe-core's address when it is an IPv4 one, else of the default
# route: Windows' own card, which eriphe-node in WSL cannot see. Behind a
# Hyper-V virtual switch, the physical cards that are up too.
function Get-WakeCard([string] $core) {
  $route = $null
  $hostName = try { ([uri] (($core -split ",")[0])).Host } catch { "" }
  $ip = $null
  if ([Net.IPAddress]::TryParse($hostName, [ref] $ip) -and $ip.AddressFamily -eq "InterNetwork" -and -not [Net.IPAddress]::IsLoopback($ip)) {
    $route = Find-NetRoute -RemoteIPAddress $hostName -ErrorAction SilentlyContinue | Where-Object { $_.InterfaceIndex } | Select-Object -First 1
  }
  if (-not $route) {
    $route = Get-NetRoute -DestinationPrefix "0.0.0.0/0" -ErrorAction SilentlyContinue |
      Sort-Object { $_.RouteMetric + $_.InterfaceMetric } | Select-Object -First 1
  }
  if (-not $route) { return "" }
  $index = if ($route.InterfaceIndex) { $route.InterfaceIndex } else { $route.ifIndex }
  $adapter = Get-NetAdapter -InterfaceIndex $index -ErrorAction SilentlyContinue
  if (-not $adapter -or $adapter.MacAddress -notmatch "^([0-9A-Fa-f]{2}-){5}[0-9A-Fa-f]{2}$") { return "" }
  $macs = @($adapter.MacAddress)
  if ($adapter.InterfaceDescription -match "Hyper-V") {
    $macs += Get-NetAdapter -Physical -ErrorAction SilentlyContinue | Where-Object { $_.Status -eq "Up" } |
      ForEach-Object { $_.MacAddress }
  }
  $macs = $macs | Where-Object { $_ } | ForEach-Object { $_.ToLower().Replace("-", ":") } | Select-Object -Unique
  $broadcast = ""
  $addr = Get-NetIPAddress -AddressFamily IPv4 -InterfaceIndex $index -ErrorAction SilentlyContinue | Select-Object -First 1
  if ($addr -and $addr.PrefixLength -ge 1 -and $addr.PrefixLength -le 30) {
    $bytes = ([Net.IPAddress] $addr.IPAddress).GetAddressBytes()
    for ($i = 0; $i -lt 4; $i++) {
      $bits = [Math]::Max(0, [Math]::Min(8, $addr.PrefixLength - 8 * $i))
      $bytes[$i] = $bytes[$i] -bor (255 -shr $bits)
    }
    $broadcast = [Net.IPAddress]::new([byte[]] $bytes).ToString()
  }
  "$($macs -join ',') $broadcast".Trim()
}

# What the distribution's eriphe-node-run is told of the session helper's
# folder ($sessionDir): the Windows folder to mount, read-only, at
# /mnt/eriphe-session, which the launcher gives the node read-only
# (ERIPHE_SESSION_DIR). Single-quoted for sh; none for a path sh cannot
# hold that way.
function Get-SessionEnv([string] $dir) {
  if (-not $dir -or $dir -match "['`r`n]") { return "" }
  "ERIPHE_SESSION_DIR='$dir'"
}

# The folder of the user signed in at the PC (C:\Users\<name>), the one place
# folders may be shared from (node/README.md, Shares): the distribution
# mounts it at /mnt/eriphe/home (eriphe-node-run), and a task gets only the
# folders shared from it, from eriphe-node engine. None for a path sh
# cannot hold single-quoted.
function Get-SessionProfile {
  try {
    $sid = (New-Object Security.Principal.NTAccount (Get-SessionUser)).Translate([Security.Principal.SecurityIdentifier]).Value
    $dir = (Get-CimInstance Win32_UserProfile -Filter "SID='$sid'" -ErrorAction Stop).LocalPath
  } catch { return $null }
  if (-not $dir -or $dir -match "['`r`n,]" -or $dir -notmatch '^[A-Za-z]:\\') { return $null }
  @{ Sid = $sid; Dir = $dir.TrimEnd('\') }
}

# What eriphe-node-run is told of it, and the launcher's list of the
# folders shared starts from, for an install from before it (its place).
function Get-ShareEnv($prof) {
  if (-not $prof) { return "" }
  "ERIPHE_WINDOWS_HOME='$($prof.Dir)'`nERIPHE_NODE_SHARES=home=/mnt/eriphe/home"
}

# The Explorer's right click, "Share with Eriphe..." (Windows 11: under "Show
# more options"): one entry on folders, for the user signed in at the PC,
# running eriphe-share.ps1, which asks for the level (read, next to it,
# manage) and whether the folder may be copied to the user's other machines,
# runs eriphe-share in the distribution (the launcher's eriphe share) and
# says how it went. Per user, in
# their registry (HKCU\Software\Classes\Directory\shell). The former second
# entry (read + results) goes.
$shareScript = Join-Path $helperDir "eriphe-share.ps1"
$shareKeys = "EripheShare", "EripheShareDrop"

function Get-ShareMenuRoots($prof) {
  $roots = @("HKCU:\Software\Classes\Directory\shell")
  if ($prof -and (Test-Path "Registry::HKEY_USERS\$($prof.Sid)")) { $roots += "Registry::HKEY_USERS\$($prof.Sid)\Software\Classes\Directory\shell" }
  $roots
}

# The words of the right click, by the language of Windows.
function Get-ShareWords {
  if ((Get-UICulture).TwoLetterISOLanguageName -eq "fr") {
    $e = [char]0xE9; $a = [char]0xC0; $o = [char]0xF4
    return @{ Menu = "Partager avec Eriphe$([char]0x2026)"; Level = "Niveau de partage avec Eriphe :"; Read = "Lecture"
      Drop = "$a c$o$e (r$e" + "sultats dans un sous-dossier)"; Manage = "G$e" + "rer (modifier le dossier)"
      Copy = "Peut $([char]0xEA)tre copi$e sur mes autres machines"; Ok = "Partager"; Cancel = "Annuler" }
  }
  @{ Menu = "Share with Eriphe$([char]0x2026)"; Level = "Level of sharing with Eriphe:"; Read = "Read"
    Drop = "Next to it (results in a subfolder)"; Manage = "Manage (change the folder)"
    Copy = "May be copied to my other machines"; Ok = "Share"; Cancel = "Cancel" }
}

function Install-ShareMenu($prof) {
  New-Item -ItemType Directory -Force $helperDir | Out-Null
  $w = Get-ShareWords
  Set-Content -Path $shareScript -Encoding UTF8 -Value @"
# Written by install.ps1: the Explorer's "Share with Eriphe...". It asks for
# the level and the copies, shares the folder with Eriphe on this PC,
# through eriphe-node in the distribution $Distro, and says how it went
# (node/README.md, Shared folders).
param([string] `$Path)
`$distro = '$Distro'
`$profileDir = '$($prof.Dir)'
`$words = @{ Level = '$($w.Level)'; Read = '$($w.Read)'; Drop = '$($w.Drop)'; Manage = '$($w.Manage)'; Copy = '$($w.Copy)'; Ok = '$($w.Ok)'; Cancel = '$($w.Cancel)' }
"@
  Add-Content -Path $shareScript -Encoding UTF8 -Value @'
Add-Type -AssemblyName System.Windows.Forms
function Show-Outcome([string] $text, [string] $icon) {
  [void] [System.Windows.Forms.MessageBox]::Show($text, "Eriphe", "OK", $icon)
}
$p = $Path.TrimEnd('\')
if (-not $p.StartsWith($profileDir + '\', [StringComparison]::OrdinalIgnoreCase)) {
  Show-Outcome "Only the folders in $profileDir can be shared with Eriphe." Warning
  exit 1
}
# The level and the copies: a small window of Windows' own controls.
$form = New-Object System.Windows.Forms.Form
$form.Text = "Eriphe"; $form.FormBorderStyle = "FixedDialog"; $form.MaximizeBox = $false; $form.MinimizeBox = $false
$form.StartPosition = "CenterScreen"; $form.AutoSize = $true; $form.AutoSizeMode = "GrowAndShrink"; $form.Padding = New-Object System.Windows.Forms.Padding(12)
$panel = New-Object System.Windows.Forms.FlowLayoutPanel
$panel.FlowDirection = "TopDown"; $panel.AutoSize = $true; $panel.WrapContents = $false
$label = New-Object System.Windows.Forms.Label
$label.Text = "$($words.Level)`n$(Split-Path $p -Leaf)"; $label.AutoSize = $true
$panel.Controls.Add($label)
$choices = @()
foreach ($k in "Read", "Drop", "Manage") {
  $r = New-Object System.Windows.Forms.RadioButton
  $r.Text = $words[$k]; $r.Tag = $k; $r.AutoSize = $true; $r.Checked = ($k -eq "Read")
  $panel.Controls.Add($r); $choices += $r
}
$copy = New-Object System.Windows.Forms.CheckBox
$copy.Text = $words.Copy; $copy.AutoSize = $true; $copy.Margin = New-Object System.Windows.Forms.Padding(3, 10, 3, 10)
$panel.Controls.Add($copy)
$buttons = New-Object System.Windows.Forms.FlowLayoutPanel
$buttons.AutoSize = $true
$ok = New-Object System.Windows.Forms.Button
$ok.Text = $words.Ok; $ok.DialogResult = "OK"
$cancel = New-Object System.Windows.Forms.Button
$cancel.Text = $words.Cancel; $cancel.DialogResult = "Cancel"
$buttons.Controls.AddRange(@($ok, $cancel))
$panel.Controls.Add($buttons)
$form.Controls.Add($panel)
$form.AcceptButton = $ok; $form.CancelButton = $cancel; $form.TopMost = $true
if ($form.ShowDialog() -ne "OK") { exit 0 }
$level = ($choices | Where-Object { $_.Checked }).Tag
$rel = $p.Substring($profileDir.Length + 1) -replace '\\', '/'
$cmd = @('-d', $distro, '-u', 'root', '--exec', '/usr/local/bin/eriphe-share', 'add')
$cmd += switch ($level) { "Drop" { '--drop' } "Manage" { '--manage' } default { '--read' } }
$cmd += if ($copy.Checked) { '--copyable' } else { '--copyable=false' }
$cmd += "/mnt/eriphe/home/$rel"
$env:WSL_UTF8 = '1'
[Console]::OutputEncoding = [Text.Encoding]::UTF8
$out = (& wsl.exe @cmd 2>&1 | Out-String).Trim()
if ($LASTEXITCODE -eq 0) { Show-Outcome $out Information } else {
  if (-not $out) { $out = "Eriphe could not share this folder (wsl.exe: $LASTEXITCODE)." }
  Show-Outcome $out Warning
}
'@
  $run = "powershell.exe -NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -File `"$shareScript`" -Path `"%V`""
  Remove-ShareMenu $prof
  $key = Join-Path (Get-ShareMenuRoots $prof)[-1] $shareKeys[0]
  New-Item -Path "$key\command" -Force | Out-Null
  Set-ItemProperty -Path $key -Name "MUIVerb" -Value $w.Menu
  Set-ItemProperty -Path $key -Name "Icon" -Value "imageres.dll,-1023"
  Set-ItemProperty -Path "$key\command" -Name "(default)" -Value $run
  $w.Menu
}

function Remove-ShareMenu($prof) {
  foreach ($root in (Get-ShareMenuRoots $prof)) {
    foreach ($k in $shareKeys) { Remove-Item -Path (Join-Path $root $k) -Recurse -Force -ErrorAction SilentlyContinue }
  }
}

# The account signed in at the PC (the console's), whose session the helper
# runs in; else the one running this script.
function Get-SessionUser {
  $user = try { (Get-CimInstance Win32_ComputerSystem -ErrorAction Stop).UserName } catch { $null }
  if (-not $user) { $user = [Security.Principal.WindowsIdentity]::GetCurrent().Name }
  $user
}

# The session helper, eriphe-session.exe (node/cmd/eriphe-session): runs as
# the user signed in at the PC, in their session, started at logon by the
# scheduled task "Eriphe session", with no window and no privilege. Every few
# seconds it writes, in $sessionDir, whether they are at the PC and not to be
# disturbed (closed values, never a program's name); eriphe-node, in the
# distribution, reads that folder only, read-only (eriphe-node-run). From the
# node image's copy in the distribution.
function Install-SessionHelper {
  $arch = if (($env:PROCESSOR_ARCHITEW6432, $env:PROCESSOR_ARCHITECTURE) -contains "ARM64") { "arm64" } else { "amd64" }
  $src = "/usr/local/share/eriphe/windows/$arch/eriphe-session.exe"
  $user = Get-SessionUser
  $sid = (New-Object Security.Principal.NTAccount $user).Translate([Security.Principal.SecurityIdentifier]).Value
  $me = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
  New-Item -ItemType Directory -Force $helperDir, $sessionDir | Out-Null
  # The program: administrators and SYSTEM change it, users run it.
  Invoke-Quiet { icacls.exe $helperDir /inheritance:r /grant:r "*S-1-5-32-544:(OI)(CI)F" "*S-1-5-18:(OI)(CI)F" "*S-1-5-32-545:(OI)(CI)RX" }
  # Its folder, all of it replaced: the user writes it, the account the
  # distribution runs as reads it, other users do not see it.
  Remove-Item (Join-Path $sessionDir "*") -Force -ErrorAction SilentlyContinue
  $acl = New-Object Security.AccessControl.DirectorySecurity
  $acl.SetAccessRuleProtection($true, $false)
  foreach ($r in @(@("S-1-5-32-544", "FullControl"), @("S-1-5-18", "FullControl"), @($me, "ReadAndExecute"), @($sid, "Modify"))) {
    $acl.AddAccessRule((New-Object Security.AccessControl.FileSystemAccessRule (
      (New-Object Security.Principal.SecurityIdentifier $r[0]), $r[1], "ContainerInherit, ObjectInherit", "None", "Allow")))
  }
  Set-Acl -Path $sessionDir -AclObject $acl
  if ((wsl.exe -d $Distro -u root -- sh -c "test -s $src && echo yes") -eq "yes") {
    $new = "$helperExe.new"
    Invoke-Quiet { cmd.exe /c "wsl.exe -d $Distro -u root --exec cat $src > `"$new`"" }
    $copied = $LASTEXITCODE -eq 0
    # A Windows program starts with "MZ".
    $head = try {
      $b = New-Object byte[] 2
      $f = [IO.File]::OpenRead($new)
      try { [void] $f.Read($b, 0, 2) } finally { $f.Close() }
      [Text.Encoding]::ASCII.GetString($b)
    } catch { "" }
    if (-not $copied -or $head -ne "MZ") {
      Remove-Item $new -Force -ErrorAction SilentlyContinue
      throw "could not copy eriphe-session.exe out of $Distro"
    }
    if (Get-ScheduledTask -TaskName $sessionTask -ErrorAction SilentlyContinue) { Stop-ScheduledTask -TaskName $sessionTask }
    Get-Process eriphe-session -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
    for ($i = 0; $i -lt 20; $i++) {
      try { Move-Item -Force $new $helperExe -ErrorAction Stop; break } catch { if ($i -eq 19) { throw }; Start-Sleep -Milliseconds 500 }
    }
  }
  if (-not (Test-Path $helperExe)) { Write-Log "No session helper in this image: the session stays unknown."; return }
  $action = New-ScheduledTaskAction -Execute $helperExe -Argument "-file `"$(Join-Path $sessionDir 'session.json')`""
  $trigger = New-ScheduledTaskTrigger -AtLogOn -User $user
  $settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -Hidden -MultipleInstances IgnoreNew `
    -ExecutionTimeLimit ([TimeSpan]::Zero) -RestartCount 999 -RestartInterval (New-TimeSpan -Minutes 1)
  $principal = New-ScheduledTaskPrincipal -UserId $user -LogonType Interactive -RunLevel Limited
  Register-ScheduledTask -TaskName $sessionTask -Action $action -Trigger $trigger -Settings $settings -Principal $principal -Force | Out-Null
  # Now, when the user is signed in; else at their next logon.
  try { Start-ScheduledTask -TaskName $sessionTask } catch { Write-Log "$_" }
  Write-Log "Session helper installed for $user."
}

# The session helper, its task and its folders, once no distribution is left.
function Remove-SessionHelper {
  if (Get-ScheduledTask -TaskName "Eriphe node*" -ErrorAction SilentlyContinue) { return }
  if (Get-ScheduledTask -TaskName $sessionTask -ErrorAction SilentlyContinue) {
    Stop-ScheduledTask -TaskName $sessionTask
    Unregister-ScheduledTask -TaskName $sessionTask -Confirm:$false
  }
  Get-Process eriphe-session -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
  Start-Sleep -Seconds 1
  Remove-Item $helperDir, $sessionDir -Recurse -Force -ErrorAction SilentlyContinue
}

$isAdmin = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole(
  [Security.Principal.WindowsBuiltInRole]::Administrator)
if (-not $isAdmin) {
  Write-Host ""
  Write-Para "Eriphe needs an administrator PowerShell: it installs for the whole PC. Right-click the Start button, choose Terminal (Admin) or Windows PowerShell (Admin) and answer Yes when Windows asks, then paste the command again."
  if ($PSCommandPath) { exit 1 }
  return
}
New-Item -ItemType Directory -Force $home_ | Out-Null
Set-Content -Path $log -Value "install.ps1, $(Get-Date)" -Encoding UTF8
# The log, as boot\: administrators and SYSTEM only, not every user of the PC.
Invoke-Quiet { icacls.exe $log /inheritance:r /grant:r "*S-1-5-32-544:F" "*S-1-5-18:F" }
$here = "this PC"

if ($Uninstall) {
  Write-Out "Removing Eriphe from $here"
  Write-Out
  $sp = Get-SavedPorts
  if ($sp["API"] -and $sp["WEB"]) { $corePorts = $sp["API"], $sp["WEB"] }
  Unregister-Resume
  if (Get-ScheduledTask -TaskName $task -ErrorAction SilentlyContinue) {
    Disable-ScheduledTask -TaskName $task | Out-Null
    Stop-ScheduledTask -TaskName $task
    Unregister-ScheduledTask -TaskName $task -Confirm:$false
  }
  if (Get-ScheduledTask -TaskName $awakeTask -ErrorAction SilentlyContinue) {
    Stop-ScheduledTask -TaskName $awakeTask
    Unregister-ScheduledTask -TaskName $awakeTask -Confirm:$false
  }
  Remove-ShareMenu (Get-SessionProfile)
  Remove-Item $shareScript -Force -ErrorAction SilentlyContinue
  Remove-SessionHelper
  Remove-Item $boot, $awake -Force -ErrorAction SilentlyContinue
  if (-not (Get-ChildItem $bootDir -ErrorAction SilentlyContinue)) { Remove-Item $bootDir -Force -ErrorAction SilentlyContinue }
  $wasCore = $false
  if ((Test-Wsl) -and (Test-Distro)) {
    $wasCore = (wsl.exe -d $Distro -u root -- sh -c "grep -qx ERIPHE_INSTALL_MODE=core /etc/eriphe-node.env && echo yes") -eq "yes"
    Invoke-Quiet { wsl.exe --unregister $Distro }
    if ($LASTEXITCODE -ne 0) { throw "Windows could not remove Eriphe's Linux system (wsl --unregister $Distro failed)" }
  }
  Remove-Item $wslDir -Recurse -Force -ErrorAction SilentlyContinue
  Remove-CorePorts
  Write-Step "Removed"
  Write-Out
  if ($wasCore) {
    Write-Out "Eriphe and its data were removed from $here."
  } else {
    Write-Para "Eriphe was removed from $here. If it was connected to Eriphe on another computer, forget it there too: R$([char]0xE9)glages > Appareils (Settings > Devices), in your browser."
  }
  Write-GoodToKnow
  return
}

if ($Rejoin -and -not $JoinToken) { throw "-Rejoin needs -JoinToken (a rejoin token: eriphe-core join-token -name <name> -rejoin)" }
if ($CoreMode -and $Core) { throw "-CoreMode runs eriphe-core here: leave -Core out" }
if ($Version -match "^v[0-9]") { $Version = $Version.Substring(1) }
if ($Version -and $Version -ne "latest" -and $Version -cnotmatch "^(edge|sha-[0-9a-f]{7,40}|(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?)?)$") {
  throw "-Version: latest, edge, or a release such as 0.2.0 (0.2 for its fixes)"
}

# Back to the version before the last update, with the updater the install left.
if ($Rollback) {
  Write-Out "Going back to the previous version on $here"
  Write-Out
  if (-not ((Test-Wsl) -and (Test-Distro))) { throw "Eriphe is not installed on $here" }
  $status = Invoke-InDistro @"
eriphe-dockerd >&2
if docker inspect eriphe >/dev/null 2>&1; then
  docker start eriphe >/dev/null
  docker exec eriphe eriphe-launcher rollback >&2
elif [ -x /usr/local/bin/eriphe-update ]; then
  eriphe-update rollback >&2
else
  echo "eriphe: no updater here yet: run the install command once, without -Rollback" >&2
  exit 1
fi
t=node
grep -qx ERIPHE_INSTALL_MODE=core /etc/eriphe-node.env && t=core
dir=/var/lib/eriphe/`$t
[ -d "`$dir" ] || dir=/var/lib/eriphe-`$t
cat "`$dir/update-status"
"@
  Write-Para (Get-StatusLine $status)
  Write-Out
  Write-Out "Logs: wsl -d $Distro -- docker logs eriphe"
  return
}

# eriphe-core's ports (core mode): given, or kept from the last run, or 8800
# and 8443. Checked before anything is installed: another program on one of
# them is said plainly, with the parameter that chooses another.
if (($WebPort -or $ApiPort) -and $Core) { throw "-WebPort and -ApiPort are for -CoreMode: a node opens no port" }
foreach ($p in @($WebPort, $ApiPort)) {
  if ($p -and ($p -notmatch "^[1-9][0-9]{0,4}$" -or [int] $p -gt 65535)) { throw "a port is 1 to 65535, not $p" }
}
$hadDistro = (Test-Wsl) -and (Test-Distro)
$savedPorts = Get-SavedPorts
$oldPorts = if ($savedPorts["API"] -and $savedPorts["WEB"]) { @($savedPorts["API"], $savedPorts["WEB"]) } else { @(8800, 8443) }
$apiPort_ = if ($ApiPort) { [int] $ApiPort } else { $oldPorts[0] }
$webPort_ = if ($WebPort) { [int] $WebPort } else { $oldPorts[1] }
if ($apiPort_ -eq $webPort_) { throw "the workspace and the API need two ports, not $webPort_ twice (-WebPort, -ApiPort)" }
$corePorts = $apiPort_, $webPort_
if ($CoreMode -or $WebPort -or $ApiPort) {
  foreach ($p in @(@($webPort_, "-WebPort", "ERIPHE_WEB_PORT", 9443), @($apiPort_, "-ApiPort", "ERIPHE_API_PORT", 9800))) {
    # This PC forwards Eriphe's own ports already (an earlier install).
    if ($hadDistro -and $oldPorts -contains $p[0]) { continue }
    $who = Get-PortUser $p[0]
    if ($who) {
      throw "$who already uses port $($p[0]) of $here, which Eriphe needs. Run the same command again with $($p[1]) $($p[3]) (with irm | iex: `$env:$($p[2])='$($p[3])'), or another free port, or stop that program"
    }
  }
}
# The workspace's menus, named in French and in English: the workspace
# speaks the browser's language.
$devicesMenu = "R$([char]0xE9)glages > Appareils (Settings > Devices)"
$connectMenu = "R$([char]0xE9)glages > Appareils > Connecter un appareil (Settings > Devices > Connect a device)"

$updating = $false
if ($JoinToken -and -not $CoreMode) {
  Write-Out "Connecting $here to $name_"
} elseif ((Test-Wsl) -and (Test-Distro)) {
  $updating = $true
  Write-Out "Updating Eriphe on $here"
} else {
  Write-Out "Installing Eriphe on $here"
}
if (-not $updating) { Write-Out "It takes a few minutes the first time." }
Write-Out

# 1. WSL itself (one reboot, after which this script carries on by itself).
if (-not (Install-Wsl)) {
  if (Register-Resume) {
    Write-Para "Windows needs to restart to finish preparing Eriphe. Restart it: the installation carries on by itself when you sign in again."
  } else {
    Write-Para "Windows needs to restart to finish preparing Eriphe. Restart it, then run this command again."
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
if (-not $bound.ContainsKey("ModelServer")) { $ModelServer = $saved["ERIPHE_MODEL_SERVER"] }
if ($ModelServer -eq "none") { $ModelServer = "" }
if ($ModelServer -and -not (Test-ModelServerAddress $ModelServer)) {
  throw "-ModelServer ${ModelServer}: the model server's address as the node in its container reaches it, e.g. http://eriphe-ollama:11434"
}
# The node runs in a container: this PC's loopback is not its own. An
# earlier install's (Ollama for Windows, through mirrored networking) is no
# longer lent, and said so.
if ($ModelServer -and (Test-LoopbackAddress $ModelServer)) {
  $why = "the node runs in a container, where $ModelServer is the container itself, not Windows: run the model server as a container on Eriphe's network and give its name (install/README.md, A model behind this machine)"
  if ($bound.ContainsKey("ModelServer")) { throw "-ModelServer ${ModelServer}: $why" }
  Add-Notice "the model server at $ModelServer is no longer lent to Eriphe: $why."
  $ModelServer = ""
}
$pin = if ($bound.ContainsKey("Version")) { if ($Version -eq "latest") { "" } else { $Version } } else { $saved["ERIPHE_VERSION"] }
$tag = if ($pin) { $pin } else { "latest" }
$nodeImage = if ($env:ERIPHE_NODE_IMAGE) { $env:ERIPHE_NODE_IMAGE } else { "$registry/eriphe-node:$tag" }
$launcherImage = if ($env:ERIPHE_LAUNCHER_IMAGE) { $env:ERIPHE_LAUNCHER_IMAGE } else { "$registry/eriphe:$tag" }
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
  $rootfs = Join-Path $home_ "alpine-minirootfs.tar.gz"
  try { Save-Download $alpine[$arch][0] $rootfs }
  catch { Write-Log "$_"; throw "could not download Eriphe: check this PC's Internet connection, then run the command again" }
  if ((Get-FileHash $rootfs -Algorithm SHA256).Hash -ne $alpine[$arch][1]) { throw "the Alpine archive does not match its checksum" }
  # Tried twice: WSL's service may time out (Wsl/Service/RegisterDistro/
  # ERROR_TIMEOUT), right after an uninstall or on a slow PC. Only WSL's error
  # codes, which are not translated, say that virtualization is off.
  for ($try = 1; $try -le 2; $try++) {
    $out = @(& { $ErrorActionPreference = "Continue"; wsl.exe --import $Distro $wslDir $rootfs --version 2 2>&1 } | ForEach-Object { "$_" })
    $code = $LASTEXITCODE
    $out | ForEach-Object { Write-Log $_ }
    if ($code -eq 0) { break }
    if (Test-Distro) { Invoke-Quiet { wsl.exe --unregister $Distro } }
    Start-Sleep -Seconds 5
  }
  if ($code -ne 0) {
    if ("$out" -match "HCS_E_HYPERV_NOT_INSTALLED|HCS_E_SERVICE_NOT_AVAILABLE|0x80370102") {
      throw "Windows could not create Eriphe's Linux system: turn on virtualization in the PC's firmware (BIOS or UEFI), then run the command again"
    }
    $err = "$out" -replace "^.*?(Wsl/[A-Za-z0-9_/]+).*$", '$1'
    if ($err -notmatch "^Wsl/") { $err = "exit code $code" }
    throw "Windows could not create Eriphe's Linux system ($err): restart the PC, then run the command again"
  }
  Remove-Item $rootfs
}

# Its setup, again on every run so that an update brings the new scripts.
# The updater of an install from before the launcher (layout 3 or older)
# takes this setup from the node image's install.ps1 and runs it as it is,
# to bring that install here: eriphe-node-run then moves its data, once. It
# must keep writing eriphe-node-run, which that updater looks for.
$changed = Invoke-InDistro @'
apk add --no-cache docker ca-certificates >/dev/null 2>/tmp/apk.log || {
  cat /tmp/apk.log >&2
  echo "eriphe: could not download Eriphe: check this PC's Internet connection, then run the command again" >&2
  exit 1
}
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
# host.docker.internal: the bridge's address, set here because dockerd does
# not always find it at boot in WSL.
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
for _ in $(seq 60); do docker info >/dev/null 2>&1 && exit 0; sleep 1; done
echo "dockerd does not start: see /var/log/dockerd.log" >&2
exit 1
EOF
cat > /usr/local/bin/eriphe-node-run <<'EOF'
#!/bin/sh
# Started by Windows at boot, and every minute (a running one is left as it
# is): Docker Engine, then Eriphe's launcher (ghcr.io/alexis-marcel/eriphe),
# which makes and keeps running eriphe-node engine, eriphe-node, Syncthing
# and, in core mode, eriphe-core, as on any Linux (install/README.md, One
# container): the node, in its container, has neither the Docker socket nor
# Windows. This distribution gives it two Windows folders, mounted here.
# It stays to relay the node's log, and stops Eriphe when it is stopped
# (install.ps1, before it changes the settings; the updater of an install
# from before, when the move to the launcher did not work).
# R: another root, for its tests (install/wsl_test.sh).
R=${ERIPHE_WSL_ROOT:-}
D="$R/var/lib/eriphe"
LOG="$R/var/log/eriphe-node.log"
log() { printf '%s eriphe-node-run: %s\n' "$(date '+%Y-%m-%d %H:%M:%S')" "$*" >>"$LOG"; }
# The updater of an install from before, at work (it waits for the node).
old_updater() { pgrep -f /var/lib/eriphe-update/update.run >/dev/null 2>&1; }

# migrate: an install from before the launcher (eriphe-node a binary here,
# its data in /var/lib/eriphe-node): its node, eriphe-core and Syncthing
# stop, and their data moves into the launcher's folder, once, reachable at
# its old paths too. The launcher's list of the folders shared starts from
# the node's own, in its place (the user's folder).
migrate() {
  [ -d "$R/var/lib/eriphe-node" ] && [ ! -L "$R/var/lib/eriphe-node" ] || return 0
  log "moving this install to Eriphe's launcher: its data goes to $D"
  pkill -f /usr/local/bin/eriphe-update-loop
  pkill -f /usr/local/bin/eriphe-syncthing-run
  pkill -f '/usr/local/lib/eriphe/syncthing/syncthing serve'
  pkill -x eriphe-node
  # The node of before made the task network itself, which Compose refuses
  # as not its own: its tasks go, and so does the network, for the
  # launcher's to take its place (the node of before makes it again if it
  # comes back).
  docker ps -aq --filter label=eriphe.task | xargs -r docker rm -f >/dev/null 2>&1
  docker network rm eriphe-tasks >/dev/null 2>&1
  mkdir -p "$D/launcher"
  chmod 711 "$D"
  docker inspect -f '{{.Config.Image}}' eriphe-core >"$D/launcher/core-before" 2>/dev/null || rm -f "$D/launcher/core-before"
  docker rm -f eriphe-core >/dev/null 2>&1
  for p in "node:$R/var/lib/eriphe-node" "core:$R/var/lib/eriphe-core" "syncthing:$R/var/lib/eriphe-syncthing"; do
    old=${p#*:} new=$D/${p%%:*}
    [ -d "$old" ] && [ ! -L "$old" ] && [ ! -e "$new" ] || continue
    if ! { mv "$old" "$new" && ln -s "$new" "$old"; }; then
      log "could not move $old to $new"
      return 1
    fi
  done
  [ -f "$D/launcher/install.conf" ] ||
    printf 'mode=%s\nshares=%s\n' "${ERIPHE_INSTALL_MODE:-node}" "${ERIPHE_NODE_SHARES:-}" >"$D/launcher/install.conf"
  rm -f "$D/node/update-needs"
  MOVED=1
}

# undo: the move back, Eriphe's containers gone, eriphe-core's of before
# started again: for the updater of before, which then puts the install
# back as it was.
undo() {
  log "the node did not connect through Eriphe's launcher: back to the install of before"
  docker rm -f eriphe eriphe-node eriphe-syncthing eriphe-engine eriphe-core >/dev/null 2>&1
  for p in "node:$R/var/lib/eriphe-node" "core:$R/var/lib/eriphe-core" "syncthing:$R/var/lib/eriphe-syncthing"; do
    old=${p#*:} new=$D/${p%%:*}
    [ -L "$old" ] && [ -d "$new" ] || continue
    rm -f "$old" && mv "$new" "$old"
  done
  if [ -s "$D/launcher/core-before" ] && [ -x "$R/usr/local/bin/eriphe-core-run" ]; then
    "$R/usr/local/bin/eriphe-core-run" "$(cat "$D/launcher/core-before")" || log "eriphe-core of before does not start"
  fi
  rm -rf "$D/launcher"
}

# cleanup: once the node is connected through the launcher, what an install
# from before left (the node's binary, its updater, its Syncthing).
cleanup() {
  [ -e "$R/usr/local/bin/eriphe-node" ] || [ -e "$R/usr/local/bin/eriphe-update" ] || return 0
  log "this install runs through Eriphe's launcher: what the install of before left goes"
  rm -f "$R/usr/local/bin/eriphe-node" "$R/usr/local/bin/eriphe-node.new" "$R/usr/local/bin/eriphe-node.version" \
    "$R/usr/local/bin/eriphe-node.version.new" "$R/usr/local/bin/eriphe-core-run" "$R/usr/local/bin/eriphe-update" \
    "$R/usr/local/bin/eriphe-update-loop" "$R/usr/local/bin/eriphe-syncthing-run" "$R/etc/eriphe-update.conf" "$D/launcher/core-before"
  rm -rf "$R/usr/local/lib/eriphe/syncthing"
  old_updater || rm -rf "$R/var/lib/eriphe-update"
}

# confirm: the node connected, then cleanup; after a move, if it is not
# within 150 s while the updater of before waits for it, undo (and stop).
confirm() {
  trap - TERM INT
  i=0
  until docker logs eriphe-node 2>&1 | grep -q 'connected to '; do
    i=$((i + 5))
    if [ -n "${MOVED:-}" ] && [ "$i" -ge 150 ] && old_updater; then
      undo
      kill -TERM $$
      return
    fi
    sleep 5
  done
  cleanup
}

# launcher_env: the launcher's settings, from this install's (install.ps1).
launcher_env() {
  mode=${ERIPHE_INSTALL_MODE:-node}
  echo "ERIPHE_MODE=$mode"
  if [ "$mode" = core ]; then
    # shellcheck disable=SC1091
    [ ! -f "$R/etc/eriphe-core.ports" ] || . "$R/etc/eriphe-core.ports"
    echo "ERIPHE_API_PORT=${API_PORT:-8800}"
    echo "ERIPHE_WEB_PORT=${WEB_PORT:-8443}"
  else
    echo "ERIPHE_CORE=${ERIPHE_CORE:-}"
    echo "ERIPHE_CA_HASH=${ERIPHE_CA_HASH:-}"
    [ -f "$D/node/node.json" ] || echo "ERIPHE_JOIN_TOKEN=${ERIPHE_JOIN_TOKEN:-}"
  fi
  echo "ERIPHE_NODE_NAME=${ERIPHE_NODE_NAME:-}"
  echo "ERIPHE_NODE_LABELS=${ERIPHE_NODE_LABELS:-}"
  echo "ERIPHE_VERSION=${ERIPHE_VERSION:-}"
  echo "ERIPHE_WAKE_MAC=${ERIPHE_WAKE_MAC:-}"
  echo "ERIPHE_WAKE_BROADCAST=${ERIPHE_WAKE_BROADCAST:-}"
  # The node's loopback is its container's, not Windows'.
  case ${ERIPHE_MODEL_SERVER:-} in
  *://localhost* | *://127.* | *://0.0.0.0* | *://\[::1\]*)
    log "the model server at $ERIPHE_MODEL_SERVER is Windows' own loopback, which the node, in its container, does not reach: it is not lent (install/README.md, A model behind this machine)"
    echo "ERIPHE_MODEL_SERVER="
    ;;
  *) echo "ERIPHE_MODEL_SERVER=${ERIPHE_MODEL_SERVER:-}" ;;
  esac
  ! mountpoint -q /mnt/eriphe-session 2>/dev/null || echo "ERIPHE_SESSION_DIR=/mnt/eriphe-session"
  echo "ERIPHE_SHARE_COMMAND=wsl -d ${ERIPHE_DISTRO:-${WSL_DISTRO_NAME:-eriphe}} eriphe-share"
}

# launcher_up: the launcher's container, made with this install's settings
# when there is none (install.ps1 removes it to change them).
launcher_up() {
  umask 077
  launcher_env >"$R/etc/eriphe-launcher.env"
  umask 022
  if docker inspect eriphe >/dev/null 2>&1; then
    docker start eriphe >/dev/null 2>>"$LOG" && return 0
  fi
  img=${ERIPHE_LAUNCHER_IMAGE:-ghcr.io/alexis-marcel/eriphe:${ERIPHE_VERSION:-latest}}
  docker image inspect "$img" >/dev/null 2>&1 || docker pull -q "$img" >>"$LOG" 2>&1 || {
    log "could not download $img"
    return 1
  }
  docker run -d --name eriphe --restart unless-stopped \
    --cap-drop ALL --cap-add CHOWN --cap-add DAC_OVERRIDE --cap-add FOWNER \
    --read-only --tmpfs /tmp --security-opt no-new-privileges \
    -v /var/run/docker.sock:/var/run/docker.sock -v "$D:/data" \
    --env-file "$R/etc/eriphe-launcher.env" "$img" >/dev/null 2>>"$LOG"
}

stop() {
  kill "${RELAY:-}" "${WATCH:-}" 2>/dev/null
  docker stop eriphe >/dev/null 2>&1
  docker stop eriphe-node eriphe-syncthing eriphe-engine eriphe-core >/dev/null 2>&1
  exit 0
}

# Its functions only, for its tests.
[ -z "${ERIPHE_NODE_RUN_LIB:-}" ] || return 0 2>/dev/null || exit 0
# Once joined, the node has its own secret: the join token is dropped.
[ ! -f /var/lib/eriphe-node/node.json ] || sed -i '/^ERIPHE_JOIN_TOKEN=/d' /etc/eriphe-node.env
set -a
# shellcheck disable=SC1091
. /etc/eriphe-node.env
set +a
# The session helper's folder (install.ps1), read-only: the one Windows
# folder the node sees besides the folders shared, where Windows tells
# whether the user is at the PC. Without it, the session stays unknown.
if [ -n "${ERIPHE_SESSION_DIR:-}" ] && ! mountpoint -q /mnt/eriphe-session 2>/dev/null; then
  mkdir -p /mnt/eriphe-session
  mount -t drvfs "$ERIPHE_SESSION_DIR" /mnt/eriphe-session -o ro,noexec,nosuid,nodev >>"$LOG" 2>&1 ||
    log "the session helper's folder could not be mounted"
fi
# The user's folder (install.ps1): the one place folders may be shared from.
# A task gets only the folders shared from it (eriphe share), from the engine.
if [ -n "${ERIPHE_WINDOWS_HOME:-}" ] && ! mountpoint -q /mnt/eriphe/home 2>/dev/null; then
  mkdir -p /mnt/eriphe/home
  mount -t drvfs "$ERIPHE_WINDOWS_HOME" /mnt/eriphe/home -o noexec,nosuid,nodev >>"$LOG" 2>&1 ||
    log "the user's folder could not be mounted: no folder can be shared"
fi
eriphe-dockerd >>"$LOG" 2>&1 || exit 1
MOVED=
migrate || exit 1
launcher_up || exit 1
trap stop TERM INT
confirm &
WATCH=$!
since=$(date -u +%Y-%m-%dT%H:%M:%SZ)
while :; do
  if docker inspect eriphe-node >/dev/null 2>&1; then
    docker logs -f --since "$since" eriphe-node >>"$LOG" 2>&1 &
    RELAY=$!
    wait "$RELAY"
    since=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  fi
  sleep 5 &
  wait $!
done
EOF
cat > /usr/local/bin/eriphe-share <<'EOF'
#!/bin/sh
# eriphe share, through the launcher, which keeps the list of the folders
# shared: the Explorer's "Share with Eriphe" runs it (wsl -d <distro> -u root
# --exec eriphe-share add FOLDER). Not copyable is the default.
for a; do
  shift
  [ "$a" = --copyable=false ] || set -- "$@" "$a"
done
exec docker exec eriphe eriphe share "$@"
EOF
cat > /usr/local/bin/eriphe-addr <<'EOF'
#!/bin/sh
# The distribution's address behind WSL's NAT, where Windows forwards eriphe-core's ports.
ip -4 addr show eth0 | sed -n 's/.*inet \([0-9.]*\).*/\1/p' | head -n 1
EOF
chmod 755 /usr/local/bin/eriphe-dockerd /usr/local/bin/eriphe-node-run /usr/local/bin/eriphe-addr /usr/local/bin/eriphe-share
# A new install: the node's data in the launcher's folder from the start,
# at its old path too (Windows' "Eriphe awake" reads it there).
mkdir -p /var/lib/eriphe
chmod 711 /var/lib/eriphe
[ -e /var/lib/eriphe-node ] || { mkdir -p /var/lib/eriphe/node && ln -s /var/lib/eriphe/node /var/lib/eriphe-node; }
# The launcher's image, there before eriphe-node-run starts it (the updater
# of an install from before waits a few minutes only).
set -a
# shellcheck disable=SC1091
[ ! -f /etc/eriphe-node.env ] || . /etc/eriphe-node.env
set +a
img=${ERIPHE_LAUNCHER_IMAGE:-ghcr.io/alexis-marcel/eriphe:${ERIPHE_VERSION:-latest}}
{ eriphe-dockerd && docker pull -q "$img"; } >/dev/null 2>&1 || true
'@
if ($changed -contains "restart") { Invoke-Quiet { wsl.exe --terminate $Distro } }

# 3. Docker Engine, for the images. Eriphe keeps running until the new
# images are there: a failed pull leaves it as it was.
Invoke-InDistro "eriphe-dockerd" | Out-Null
# The version running, to say what an update changes (an install from before
# the launcher: its node's binary).
$versionOf = @'
c=eriphe-node
grep -qx ERIPHE_INSTALL_MODE=core /etc/eriphe-node.env 2>/dev/null && c=eriphe-core
v=$(docker inspect -f '{{index .Config.Labels "org.opencontainers.image.version"}}' "$c" 2>/dev/null | sed 's/<no value>//')
[ -n "$v" ] || [ "$c" = eriphe-core ] || v=$(cat /usr/local/bin/eriphe-node.version 2>/dev/null)
echo "$v"
'@
$before = if ($updating) { Invoke-InDistro $versionOf | Select-Object -Last 1 } else { "" }

# Pulls an image, saying so when the registry refuses it.
$pull = @'
pull() {
  if ! docker pull -q "$1" >/tmp/pull.log 2>&1; then
    cat /tmp/pull.log >&2
    grep -qiE 'denied|unauthorized|not found' /tmp/pull.log && echo "the registry refuses $1: it may not be public (yet)" >&2
    echo "eriphe: could not download Eriphe: check this PC's Internet connection, then run the command again" >&2
    exit 3
  fi
}
'@

# 4. The launcher's image, and the node's, which the launcher runs and which
# carries the Windows session helper (step 7).
Write-Log "Pulling $launcherImage and $nodeImage..."
Invoke-InDistro @"
$pull
pull $launcherImage
pull $nodeImage
id=`$(docker create $nodeImage)
rm -rf /usr/local/share/eriphe/windows
mkdir -p /usr/local/share/eriphe
docker cp "`$id:/usr/local/share/eriphe/windows" /usr/local/share/eriphe/windows >/dev/null 2>&1 || true
docker rm "`$id" >/dev/null
"@ | Out-Null

# 5. Core mode: the ports eriphe-core is published on (the launcher's
# ERIPHE_API_PORT and ERIPHE_WEB_PORT, from eriphe-node-run).
if ($CoreMode) {
  Invoke-InDistro "printf 'API_PORT=%s\nWEB_PORT=%s\n' $apiPort_ $webPort_ > /etc/eriphe-core.ports" | Out-Null
} else {
  Remove-CorePorts
  Invoke-InDistro "rm -f /etc/eriphe-core.ports" | Out-Null
}
Write-Step "Downloaded"

# 6. Stop Eriphe, and its launcher, which eriphe-node-run makes again with
# the new settings; the join token only matters until the node has joined.
# The task is disabled first, so that it does not start again meanwhile
# (step 7 registers it again, enabled).
if (Get-ScheduledTask -TaskName $task -ErrorAction SilentlyContinue) {
  Disable-ScheduledTask -TaskName $task | Out-Null
  Stop-ScheduledTask -TaskName $task
}
Invoke-InDistro @'
pkill -f /usr/local/bin/eriphe-node-run || true
pkill -x eriphe-node || true
for i in $(seq 30); do pgrep -f /usr/local/bin/eriphe-node-run >/dev/null || break; sleep 1; done
docker rm -f eriphe >/dev/null 2>&1 || true
'@ | Out-Null
$mode = if ($CoreMode) { "core" } else { "node" }
$coreUrl = if ($CoreMode) { "" } else { $Core }
# This PC's own network card, which the node in WSL cannot see (Get-WakeCard).
$wakeCard = try { Get-WakeCard $Core } catch { "" }
$wakeMac, $wakeBroadcast = ($wakeCard + " ").Split(" ")[0, 1]
$forget = if ($Rejoin) { "rm -f /var/lib/eriphe-node/node.json" } else { "" }
$token = if ($Rejoin -or -not $joined) { $JoinToken } else { "" }
$launcherSetting = if ($env:ERIPHE_LAUNCHER_IMAGE) { "ERIPHE_LAUNCHER_IMAGE=$launcherImage" } else { "" }
$sessionProfile = Get-SessionProfile
Invoke-InDistro @"
umask 077
$forget
# This PC is at its layout: nothing waits for its user (node/update.go).
rm -f /var/lib/eriphe-node/update-needs
cat > /etc/eriphe-node.env <<'EOF'
ERIPHE_INSTALL_MODE=$mode
ERIPHE_INSTALL=windows
ERIPHE_DISTRO=$Distro
ERIPHE_CORE=$coreUrl
ERIPHE_NODE_NAME=$Name
ERIPHE_NODE_LABELS=$Labels
ERIPHE_CA_HASH=$CaHash
ERIPHE_JOIN_TOKEN=$token
ERIPHE_VERSION=$pin
ERIPHE_WAKE_MAC=$wakeMac
ERIPHE_WAKE_BROADCAST=$wakeBroadcast
ERIPHE_MODEL_SERVER=$ModelServer
$launcherSetting
$(Get-SessionEnv $sessionDir)
$(Get-ShareEnv $sessionProfile)
EOF
"@ | Out-Null

# 7. Start at boot, logged in or not, and start again if WSL stops: the task
# runs eriphe-node-run (which starts the launcher) in the distribution, at
# boot and every minute from now on, which starts it again once it has ended
# (a running one is left as it is). In core mode, the ports are forwarded
# first, by a script of its own: the distribution's address changes with
# every start of WSL.
$mirrored = Test-Mirrored
$run ="-d $Distro -u root --exec /usr/local/bin/eriphe-node-run"
if ($CoreMode -and -not $mirrored) {
  New-Item -ItemType Directory -Force $bootDir | Out-Null
  # Only administrators and SYSTEM: the task runs it elevated.
  Invoke-Quiet { icacls.exe $bootDir /inheritance:r /grant:r "*S-1-5-32-544:(OI)(CI)F" "*S-1-5-18:(OI)(CI)F" }
  Set-Content -Path $boot -Encoding UTF8 -Value @"
# Run by the scheduled task "$task" (install.ps1): forwards eriphe-core's
# ports to the distribution's address, then runs eriphe-node-run there.
`$ip = wsl.exe -d $Distro -u root --exec /usr/local/bin/eriphe-addr
if (`$ip) {
  foreach (`$p in $($corePorts -join ', ')) {
    netsh.exe interface portproxy add v4tov4 listenaddress=0.0.0.0 listenport=`$p connectaddress=`$ip connectport=`$p | Out-Null
  }
}
wsl.exe $run
"@
  $action = New-ScheduledTaskAction -Execute "powershell.exe" -Argument "-NoProfile -NonInteractive -ExecutionPolicy Bypass -File `"$boot`""
} else {
  Remove-Item $boot -Force -ErrorAction SilentlyContinue
  $action = New-ScheduledTaskAction -Execute "wsl.exe" -Argument $run
}
$trigger = @((New-ScheduledTaskTrigger -AtStartup),
  (New-ScheduledTaskTrigger -Once -At (Get-Date) -RepetitionInterval (New-TimeSpan -Minutes 1)))
$settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -StartWhenAvailable -MultipleInstances IgnoreNew `
  -ExecutionTimeLimit ([TimeSpan]::Zero) -RestartCount 999 -RestartInterval (New-TimeSpan -Minutes 1)
$principal = New-ScheduledTaskPrincipal -UserId ([Security.Principal.WindowsIdentity]::GetCurrent().Name) -LogonType S4U -RunLevel Highest
Register-ScheduledTask -TaskName $task -Action $action -Trigger $trigger -Settings $settings -Principal $principal -Force | Out-Null

# No sleep while a task is at work. eriphe-node, in its container, writes
# awake in its data meanwhile and removes it after (node/README.md, Sleep);
# the distribution starts no Windows program, so a task of Windows' own
# looks every 30 s and holds the PC awake while the file is there
# (SetThreadExecutionState: ES_CONTINUOUS, with ES_SYSTEM_REQUIRED).
if (Get-ScheduledTask -TaskName $awakeTask -ErrorAction SilentlyContinue) { Stop-ScheduledTask -TaskName $awakeTask }
New-Item -ItemType Directory -Force $bootDir | Out-Null
Invoke-Quiet { icacls.exe $bootDir /inheritance:r /grant:r "*S-1-5-32-544:(OI)(CI)F" "*S-1-5-18:(OI)(CI)F" }
Set-Content -Path $awake -Encoding UTF8 -Value @"
# Run by the scheduled task "$awakeTask" (install.ps1): keeps Windows out of
# sleep while eriphe-node has a task at work in $Distro, and lets it sleep after.
Add-Type -Namespace Eriphe -Name Power -MemberDefinition '[DllImport("kernel32.dll")] public static extern uint SetThreadExecutionState(uint flags);'
while (`$true) {
  wsl.exe -d $Distro -u root --exec test -e /var/lib/eriphe-node/awake
  `$flags = if (`$LASTEXITCODE -eq 0) { [uint32]2147483649 } else { [uint32]2147483648 }
  [void][Eriphe.Power]::SetThreadExecutionState(`$flags)
  Start-Sleep -Seconds 30
}
"@
$awakeAction = New-ScheduledTaskAction -Execute "powershell.exe" -Argument "-NoProfile -NonInteractive -ExecutionPolicy Bypass -File `"$awake`""
Register-ScheduledTask -TaskName $awakeTask -Action $awakeAction -Trigger $trigger -Settings $settings -Principal $principal -Force | Out-Null

# Whether the user is at the PC: neither task above sees their session (WSL,
# and S4U's session 0), so a small program runs in it (Install-SessionHelper).
# Eriphe works without it: the session is then unknown.
try { Install-SessionHelper } catch {
  Write-Log "Session helper: $_"
  Add-Notice "$name_ cannot tell yet whether you are at $here (see the log): run the command again to retry."
}
$shareLabel = $null
if ($sessionProfile) {
  try { $shareLabel = Install-ShareMenu $sessionProfile } catch { Write-Log "Share menu: $_" }
} else {
  Write-Log "No profile folder found for the user signed in at the PC: no folder can be shared."
}

if ($CoreMode) {
  # Other ports than the last run's: its forwarding and firewall rule go.
  if ($hadDistro -and (Compare-Object $oldPorts $corePorts)) {
    $keep = $corePorts
    $corePorts = $oldPorts
    Remove-CorePorts
    $corePorts = $keep
  }
  if (-not (Get-NetFirewallRule -Name $rule -ErrorAction SilentlyContinue)) {
    New-NetFirewallRule -Name $rule -DisplayName $rule -Description "eriphe-core in WSL ($Distro): its API and workspace" `
      -Direction Inbound -Protocol TCP -LocalPort $corePorts -Action Allow -Profile Private, Domain | Out-Null
  }
  if ($mirrored) {
    Add-Notice "with mirrored networking, if other devices cannot reach ports $($corePorts -join ' and '), allow them in the Hyper-V firewall (New-NetFirewallHyperVRule)."
  } else {
    $ip = wsl.exe -d $Distro -u root --exec /usr/local/bin/eriphe-addr
    foreach ($p in $corePorts) { Invoke-Quiet { netsh.exe interface portproxy add v4tov4 listenaddress=0.0.0.0 listenport=$p connectaddress=$ip connectport=$p } }
  }
}
Start-ScheduledTask -TaskName $task
Start-ScheduledTask -TaskName $awakeTask
Write-Step "Installed"

# 8. Wait for the node to join, and say where things are. The launcher
# downloads the images of a first install, and in core mode the node starts
# after eriphe-core: it can take minutes.
for ($i = 0; $i -lt 150; $i++) {
  $joined = (wsl.exe -d $Distro -u root -- sh -c "test -f /var/lib/eriphe-node/node.json && docker logs eriphe-node 2>&1 | grep -q 'connected to ' && echo yes") -eq "yes"
  if ($joined) { break }
  Start-Sleep -Seconds 2
}
if ($joined) {
  if ($CoreMode) { Write-Step "Started" } else { Write-Step "Connected" }
}
Write-Log "eriphe-node is installed as '$Name' (joined: $joined)."
$now = Invoke-InDistro $versionOf | Select-Object -Last 1
Write-Out
if ($updating) {
  if (-not $now) { Write-Out "Updated." }
  elseif ($now -eq $before) { Write-Out "Already up to date ($now)." }
  else { Write-Out "Updated to $now." }
}
if ($CoreMode) {
  $lan = Get-LanAddress
  if (-not $lan) { $lan = "<this-pc>" }
  # The setup code, while there is no password (kept until it is used).
  $setup = Invoke-InDistro @'
if code=$(docker exec eriphe-core eriphe-core auth setup-code 2>/tmp/setup-code.err); then
  printf '%s\n' "$code"
fi
rm -f /tmp/setup-code.err
'@ | Select-Object -Last 1
  # The tailnet's address, when remote access is on.
  $remote = Invoke-InDistro "docker exec eriphe-core eriphe-core remote-address 2>/dev/null; true" | Select-Object -Last 1
  if ($remote -notmatch "^https://[A-Za-z0-9.-]+$") { $remote = "" }
  $done = if ($updating) { "" } else { "Done: Eriphe runs on $here. " }
  if ($setup) {
    Write-Para "${done}To finish, open this address in your browser and give the setup code:"
    Write-Out
    Write-Out "  https://${lan}:${webPort_}/setup"
    Write-Secret "  Setup code: " $setup
  } elseif ($remote) {
    # Remote access on: its address works from every device of the tailnet.
    Write-Para "${done}Open $remote in your browser, from a device on your Tailscale network, and sign in (forgotten password: wsl -d $Distro -u root -- docker exec eriphe-core eriphe-core auth password-reset)."
    Write-Para "On the local network: https://${lan}:${webPort_}"
  } else {
    Write-Para "${done}Open https://${lan}:${webPort_} in your browser and sign in (forgotten password: wsl -d $Distro -u root -- docker exec eriphe-core eriphe-core auth password-reset)."
  }
  if (-not $joined) { Add-Notice "this PC's eriphe-node is still starting: it will appear in $devicesMenu in a few minutes." }
  if (-not $updating) { Add-Notice "to connect your other computers, open $connectMenu in your browser." }
  if (Get-NetConnectionProfile | Where-Object NetworkCategory -eq "Public") {
    Add-Notice "this network is set to Public, so other devices cannot reach $here. In Windows Settings > Network & internet, set it to Private."
  }
} elseif (-not $joined) {
  Write-Para "Installed, but $here is not connected to $name_ yet: it keeps trying. If it does not appear in $devicesMenu in your browser within a few minutes, check that the computer Eriphe runs on is on, and see the logs."
} elseif (-not $updating) {
  Write-Para "Done: $here is connected to $name_. Go back to your browser: it now appears in $devicesMenu."
}
if ($ModelServer) {
  Write-Out
  Write-Out "Lends the model server at $ModelServer."
}
if ($pin -eq "edge") {
  Add-Notice "it follows main, the version in development (-Version latest follows the releases again)."
} elseif ($pin) {
  Add-Notice "it stays on version $pin (-Version latest follows the releases again)."
}
if ($shareLabel) {
  Write-Out
  Write-Out "To share a folder of $($sessionProfile.Dir) with $($name_): right-click it in File Explorer, Show more options, `"$shareLabel`"."
}
Write-GoodToKnow
Write-Out
Write-Out "Logs: wsl -d $Distro -- tail /var/log/eriphe-node.log (updates: wsl -d $Distro -- docker logs eriphe)"
}
