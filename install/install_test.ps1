# Checks of install.ps1 that need no Windows: run by lint.sh where pwsh is.
$ErrorActionPreference = "Continue"
$script = Join-Path $PSScriptRoot "install.ps1"

# Under irm | iex the script runs in the caller's scope: it must leave the
# window as it was. An unknown ERIPHE_* variable stops it at once.
$env:ERIPHE_NOT_A_PARAMETER = "1"
try { Get-Content $script -Raw | Invoke-Expression } catch { $null = $_ }
Remove-Item Env:ERIPHE_NOT_A_PARAMETER -ErrorAction SilentlyContinue
if ($ErrorActionPreference -ne "Continue" -or (Test-Path variable:fromEnv)) {
  Write-Host "install_test.ps1: under irm | iex, install.ps1 leaves its variables or preferences in the window"
  exit 1
}
Write-Host "install.ps1 under irm | iex leaves the window as it was"

# Its functions that need no Windows, defined here from the script's text.
$failed = 0
function Assert-That([bool] $ok, [string] $what) {
  if (-not $ok) { Write-Host "install_test.ps1: FAIL: $what"; $script:failed++ }
}
$ast = [System.Management.Automation.Language.Parser]::ParseFile($script, [ref] $null, [ref] $null)
foreach ($name in "Test-MirroredText", "Test-ModelServerAddress", "Test-LoopbackAddress", "Write-Secret", "Get-SessionEnv") {
  $def = $ast.Find({ param($n) $n -is [System.Management.Automation.Language.FunctionDefinitionAst] -and $n.Name -eq $name }, $true)
  if (-not $def) { Write-Host "install_test.ps1: no function $name in install.ps1"; exit 1 }
  Invoke-Expression $def.Extent.Text
}

# Mirrored networking counts only in the [wsl2] section.
Assert-That (Test-MirroredText "[wsl2]`r`nmemory=8GB`r`nnetworkingMode=mirrored`r`n") "mirrored in [wsl2]"
Assert-That (Test-MirroredText "[WSL2]`nNetworkingMode = Mirrored # for Ollama") "mirrored, any case, with a comment"
Assert-That (-not (Test-MirroredText "[experimental]`nnetworkingMode=mirrored")) "mirrored in [experimental]"
Assert-That (-not (Test-MirroredText "[wsl2]`nmemory=8GB`n[experimental]`nnetworkingMode=mirrored")) "mirrored after [wsl2]"
Assert-That (-not (Test-MirroredText "networkingMode=mirrored")) "mirrored outside a section"
Assert-That (-not (Test-MirroredText "[wsl2]`n# networkingMode=mirrored")) "mirrored commented out"
Assert-That (-not (Test-MirroredText "[wsl2]`nnetworkingMode=nat")) "NAT"

# -ModelServer addresses.
foreach ($ok in "http://127.0.0.1:11434", "http://[::1]:11434", "https://models.example.org/v1", "http://[fd00::5]:8080/") {
  Assert-That (Test-ModelServerAddress $ok) "-ModelServer $ok refused"
}
foreach ($bad in "127.0.0.1:11434", "ftp://192.0.2.1", "http://[::1", "http://user@192.0.2.1", "http://192.0.2.1:11434/?q") {
  Assert-That (-not (Test-ModelServerAddress $bad)) "-ModelServer $bad accepted"
}
# This PC's loopback, which the node, in its container, does not reach.
foreach ($lo in "http://127.0.0.1:11434", "http://localhost:11434", "http://[::1]:11434", "https://127.0.0.2/v1", "http://0.0.0.0:11434") {
  Assert-That (Test-LoopbackAddress $lo) "$lo is not the loopback"
}
foreach ($not in "http://eriphe-ollama:11434", "http://192.0.2.5:11434", "http://localhost.example.org:11434", "http://[fd00::5]:8080/") {
  Assert-That (-not (Test-LoopbackAddress $not)) "$not is the loopback"
}
# The session helper's folder, as the distribution's sh reads it.
$se = Get-SessionEnv 'C:\ProgramData\eriphe-node\session'
Assert-That ($se -ceq "ERIPHE_SESSION_DIR='C:\ProgramData\eriphe-node\session'") "session env: $se"
Assert-That ((Get-SessionEnv "C:\Sam's data\session") -eq "") "a quote in the session folder"
Assert-That ((Get-SessionEnv "C:\data`nERIPHE_CORE=http://192.0.2.1") -eq "") "a new line in the session folder"
Assert-That ((Get-SessionEnv "") -eq "") "no session folder"
# The setup code is printed, not logged.
$logged = New-Object Collections.Generic.List[string]
function Write-Log([string] $text) { $logged.Add($text) }
Write-Secret "  Setup code: " "ABCD-1234" 6>$null
Assert-That (($logged -join "`n") -notmatch "ABCD-1234") "the setup code in the log"
Assert-That (($logged -join "`n") -match "not logged") "no line in the log for the setup code"
if ($failed) { exit 1 }
Write-Host "install.ps1's .wslconfig, -ModelServer, loopback and session folder checks pass"
