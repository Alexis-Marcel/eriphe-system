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
# the node in WSL reaches it: http://127.0.0.1:11434 with WSL's mirrored
# networking; none for none; kept), and the
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
[CmdletBinding()]
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
  [switch] $Uninstall,
  [string] $AssistantName,   # the assistant's name, for what this script prints
  [string] $WebPort,         # core mode: the workspace's port (default 8443, kept for the next runs)
  [string] $ApiPort,         # core mode: the port other devices join on (default 8800, kept)
  [string] $ModelServer      # a model server this node lends through its tunnel, as the node in WSL reaches it; "none" for none (kept)
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
  if ($v.Name -in "ERIPHE_NODE_IMAGE", "ERIPHE_CORE_IMAGE") { continue }
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
# The images, at the pinned version or latest (set once the saved settings
# are read); replaced by ERIPHE_NODE_IMAGE and ERIPHE_CORE_IMAGE, as with
# install.sh. eriphe-core never chooses them.
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
  (Test-Path $cfg) -and ((Get-Content $cfg -Raw) -match "(?im)^\s*networkingMode\s*=\s*mirrored")
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
if ($Binary -and -not (Test-Path $Binary)) { throw "no eriphe-node binary at $Binary" }
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
[ -x /usr/local/bin/eriphe-update ] || { echo "eriphe: no updater here yet: run the install command once, without -Rollback" >&2; exit 1; }
eriphe-dockerd >&2
eriphe-update rollback >&2
dir=/var/lib/eriphe-node
grep -qx ERIPHE_INSTALL_MODE=core /etc/eriphe-node.env && dir=/var/lib/eriphe-core
cat "`$dir/update-status"
"@
  Write-Para (Get-StatusLine $status)
  Write-Out
  Write-Out "Logs: wsl -d $Distro -- tail /var/log/eriphe-update.log"
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
if ($ModelServer -and $ModelServer -notmatch "^https?://[A-Za-z0-9._~-]+(:[0-9]{1,5})?(/[A-Za-z0-9._~/-]*)?$") {
  throw "-ModelServer ${ModelServer}: the model server's address as the node in WSL reaches it, e.g. http://127.0.0.1:11434"
}
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
# eriphe-core's container, from the image given (install.ps1, eriphe-update),
# on the ports of /etc/eriphe-core.ports (-ApiPort, -WebPort).
[ ! -f /etc/eriphe-core.ports ] || . /etc/eriphe-core.ports
docker rm -f eriphe-core >/dev/null 2>&1 || true
docker run -d --name eriphe-core --restart unless-stopped --read-only --cap-drop ALL \
  --security-opt no-new-privileges:true -v /var/lib/eriphe-core:/data -e ERIPHE_INSTALL=windows \
  -p "0.0.0.0:${API_PORT:-8800}:8800" -p "0.0.0.0:${WEB_PORT:-8443}:8443" -e "ERIPHE_API_PORT=${API_PORT:-8800}" "$1" >/dev/null
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
  # shellcheck disable=SC1091
  [ ! -f /etc/eriphe-core.ports ] || . /etc/eriphe-core.ports
  until wget -q -O /dev/null "http://127.0.0.1:${API_PORT:-8800}/health" 2>/dev/null; do
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
if ($changed -contains "restart") { Invoke-Quiet { wsl.exe --terminate $Distro } }

# 3. Docker Engine, for the images. The node keeps running until the new
# binary is there: a failed pull leaves it as it was.
Invoke-InDistro "eriphe-dockerd" | Out-Null
# The version running, to say what an update changes.
$versionOf = @'
if grep -qx ERIPHE_INSTALL_MODE=core /etc/eriphe-node.env 2>/dev/null; then
  docker inspect -f '{{index .Config.Labels "org.opencontainers.image.version"}}' eriphe-core 2>/dev/null | sed 's/<no value>//'
else
  cat /usr/local/bin/eriphe-node.version 2>/dev/null
fi
true
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

# 4. The binary, next to the running one: from its image, or given (through
# stdin: the distribution does not see Windows' drives).
if ($Binary) {
  Invoke-Quiet { cmd.exe /c "wsl.exe -d $Distro -u root -- sh -c `"cat > /usr/local/bin/eriphe-node.new && chmod 755 /usr/local/bin/eriphe-node.new`" < `"$Binary`"" }
  if ($LASTEXITCODE -ne 0) { throw "copying eriphe-node failed" }
} else {
  Write-Log "Pulling $nodeImage..."
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
  Write-Log "Pulling $coreImage..."
  Invoke-InDistro @"
$pull
mkdir -p /var/lib/eriphe-core
chown 1993:1993 /var/lib/eriphe-core
chmod 700 /var/lib/eriphe-core
pull $coreImage
want=`$(docker image inspect -f '{{.Id}}' $coreImage)
have=`$(docker inspect -f '{{.Image}}' eriphe-core 2>/dev/null || true)
# Its ports: recreated on others.
printf 'API_PORT=%s\nWEB_PORT=%s\n' $apiPort_ $webPort_ > /tmp/eriphe-core.ports
if ! cmp -s /tmp/eriphe-core.ports /etc/eriphe-core.ports; then mv /tmp/eriphe-core.ports /etc/eriphe-core.ports; have=; else rm /tmp/eriphe-core.ports; fi
if [ "`$want" != "`$have" ]; then
  eriphe-core-run $coreImage
  docker image prune -f >/dev/null
else
  docker start eriphe-core >/dev/null
fi
for i in `$(seq 60); do wget -q -O /dev/null http://127.0.0.1:$apiPort_/health 2>/dev/null && exit 0; sleep 1; done
docker logs --tail 20 eriphe-core >&2
echo "eriphe: Eriphe did not start (eriphe-core does not answer on :$apiPort_): run the command again" >&2
exit 1
"@ | Out-Null
  # The node turns this address to TLS by itself once eriphe-core offers it.
  $Core = "http://127.0.0.1:$apiPort_"
  if (-not $joined -or $Rejoin) {
    $joinArgs = "-name $Name"
    if ($Rejoin) { $joinArgs += " -rejoin" }
    # It prints the command that joins a node, with the token and eriphe-core's
    # CA (-ca-hash); eriphe-core may still be starting. A new eriphe-core
    # accepts machines over TLS only, from the start: the node joins with its
    # CA's hash, kept for the next runs.
    $joining = Invoke-InDistro @"
for i in `$(seq 30); do
  if t=`$(docker exec eriphe-core eriphe-core join-token $joinArgs 2>/tmp/join.log | grep -e ' -join-token ') && [ -n "`$t" ]; then echo "`$t"; exit 0; fi
  sleep 2
done
cat /tmp/join.log >&2
echo "eriphe: Eriphe did not start (eriphe-core gives no join token): run the command again" >&2
exit 1
"@ | Select-Object -Last 1
    if ($joining -notmatch " -join-token (\S+)") { throw "Eriphe did not start (eriphe-core gives no join token): run the command again" }
    $JoinToken = $Matches[1]
    if ($joining -match " -ca-hash (sha256:[0-9a-f]+)") { $CaHash = $Matches[1] }
  }
} else {
  Remove-CorePorts
}
Write-Step "Downloaded"

# 6. Stop the node, put the new binary in place, and its settings; the join
# token only matters until the node has joined. Disabled first, so that it
# does not start again meanwhile (step 7 registers it again, enabled).
if (Get-ScheduledTask -TaskName $task -ErrorAction SilentlyContinue) {
  Disable-ScheduledTask -TaskName $task | Out-Null
  Stop-ScheduledTask -TaskName $task
}
Invoke-InDistro "pkill -f /usr/local/bin/eriphe-node-run || true; pkill -x eriphe-node || true" | Out-Null
$mode = if ($CoreMode) { "core" } else { "node" }
$alwaysOn = if ($CoreMode) { "ERIPHE_ALWAYS_ON=1" } else { "" }
# This PC's own network card, which the node in WSL cannot see (Get-WakeCard).
$wakeCard = try { Get-WakeCard $Core } catch { "" }
$wakeMac, $wakeBroadcast = ($wakeCard + " ").Split(" ")[0, 1]
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
mkdir -p /var/lib/eriphe-node
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
ERIPHE_WAKE_MAC=$wakeMac
ERIPHE_WAKE_BROADCAST=$wakeBroadcast
ERIPHE_MODEL_SERVER=$ModelServer
$alwaysOn
EOF
"@ | Out-Null

# 7. Start at boot, logged in or not, and start again if WSL stops: the task
# runs eriphe-node-run (which restarts the node) in the distribution, at
# boot and every minute from now on, which starts it again once it has ended
# (a running one is left as it is). In core mode, the ports are forwarded first, by a script
# of its own: the distribution's address changes with every start of WSL.
$mirrored = Test-Mirrored
$run = "-d $Distro -u root --exec /usr/local/bin/eriphe-node-run"
if ($CoreMode -and -not $mirrored) {
  New-Item -ItemType Directory -Force $bootDir | Out-Null
  # Only administrators and SYSTEM: the task runs it elevated.
  Invoke-Quiet { icacls.exe $bootDir /inheritance:r /grant:r "*S-1-5-32-544:(OI)(CI)F" "*S-1-5-18:(OI)(CI)F" }
  Set-Content -Path $boot -Encoding UTF8 -Value @"
# Run by the scheduled task "$task" (install.ps1): forwards eriphe-core's
# ports to the distribution's address, then runs eriphe-node there.
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

# No sleep while a task is at work. eriphe-node, in the distribution, writes
# /var/lib/eriphe-node/awake meanwhile and removes it after (node/README.md,
# Sleep); the distribution starts no Windows program, so a task of Windows'
# own looks every 30 s and holds the PC awake while the file is there
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

# 8. Wait for the node to join, and say where things are.
for ($i = 0; $i -lt 30; $i++) {
  $joined = (wsl.exe -d $Distro -u root -- sh -c "test -f /var/lib/eriphe-node/node.json && pgrep -x eriphe-node >/dev/null && echo yes") -eq "yes"
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
  # The setup code, while there is no password (kept until it is used); an
  # older eriphe-core has no auth setup-code: its log says it.
  $setup = Invoke-InDistro @'
if code=$(docker exec eriphe-core eriphe-core auth setup-code 2>/tmp/setup-code.err); then
  printf '%s\n' "$code"
elif ! grep -q 'has a password' /tmp/setup-code.err; then
  docker logs eriphe-core 2>&1 | sed -n 's/.*setup code \([A-Za-z0-9-]*\).*/\1/p' | tail -n 1
fi
rm -f /tmp/setup-code.err
'@ | Select-Object -Last 1
  $done = if ($updating) { "" } else { "Done: Eriphe runs on $here. " }
  if ($setup) {
    Write-Para "${done}To finish, open this address in your browser and give the setup code:"
    Write-Out
    Write-Out "  https://${lan}:${webPort_}/setup"
    Write-Out "  Setup code: $setup"
  } else {
    Write-Para "${done}Open https://${lan}:${webPort_} in your browser and sign in (forgotten password: wsl -d $Distro -u root -- docker exec eriphe-core eriphe-core auth password-reset)."
  }
  if (-not $updating) { Add-Notice "to connect your other computers, open $connectMenu in your browser." }
  if (Get-NetConnectionProfile | Where-Object NetworkCategory -eq "Public") {
    Add-Notice "this network is set to Public, so other devices cannot reach $here. In Windows Settings > Network & internet, set it to Private."
  }
} elseif (-not $joined) {
  Write-Para "Installed, but $here is not connected to $name_ yet: it keeps trying. If it does not appear in $devicesMenu in your browser within a few minutes, check that the computer Eriphe runs on is on, and see the logs."
} elseif (-not $updating) {
  Write-Para "Done: $here is connected to $name_. Go back to your browser: it now appears in $devicesMenu."
}
if ($pin -eq "edge") {
  Add-Notice "it follows main, the version in development (-Version latest follows the releases again)."
} elseif ($pin) {
  Add-Notice "it stays on version $pin (-Version latest follows the releases again)."
}
Write-GoodToKnow
Write-Out
Write-Out "Logs: wsl -d $Distro -- tail /var/log/eriphe-node.log"
}
