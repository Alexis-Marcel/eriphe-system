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
foreach ($name in "Test-MirroredText", "Test-ModelServerAddress", "Get-ModelsUrl", "Get-ModelServerAdvice") {
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
Assert-That ((Get-ModelsUrl "http://127.0.0.1:11434") -eq "http://127.0.0.1:11434/v1/models") "models URL"
Assert-That ((Get-ModelsUrl "http://[::1]:8080/v1/") -eq "http://[::1]:8080/v1/models") "models URL of a /v1 address"

# What to do when it does not answer.
Assert-That ((Get-ModelServerAdvice "http://127.0.0.1:11434" $false) -match "mirrored") "loopback without mirrored: mirrored networking"
Assert-That ((Get-ModelServerAdvice "http://[::1]:11434" $false) -match "wsl --shutdown") "IPv6 loopback without mirrored: wsl --shutdown"
Assert-That ((Get-ModelServerAdvice "http://127.0.0.1:11434" $true) -match "start it") "loopback with mirrored: start it"
Assert-That ((Get-ModelServerAdvice "http://192.0.2.5:11434" $false) -match "start it") "LAN address: start it"
if ($failed) { exit 1 }
Write-Host "install.ps1's .wslconfig, -ModelServer and model server checks pass"
