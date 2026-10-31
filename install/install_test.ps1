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
