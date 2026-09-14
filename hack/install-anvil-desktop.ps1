# Anvil Agents Desktop — per-user Windows install
# Copies anvil-desktop.exe next to this script into %LOCALAPPDATA%\Programs\AnvilAgentsDesktop
# and creates a Start Menu shortcut. Does not require Administrator.
#
# Usage (from the unzipped windows-amd64 folder):
#   powershell -NoProfile -ExecutionPolicy Bypass -File .\install.ps1
#   powershell -NoProfile -ExecutionPolicy Bypass -File .\install.ps1 -Uninstall

param(
    [switch]$Uninstall
)

$ErrorActionPreference = "Stop"
$ProductTitle = "Anvil Agents Desktop"
$Here = Split-Path -Parent $MyInvocation.MyCommand.Path
$ExeName = "anvil-desktop.exe"
$Dest = Join-Path $env:LOCALAPPDATA "Programs\AnvilAgentsDesktop"
$StartMenu = Join-Path $env:APPDATA "Microsoft\Windows\Start Menu\Programs"
$Shortcut = Join-Path $StartMenu "$ProductTitle.lnk"

if ($Uninstall) {
    if (Test-Path $Shortcut) { Remove-Item -Force $Shortcut }
    if (Test-Path $Dest) { Remove-Item -Recurse -Force $Dest }
    Write-Host "Removed $ProductTitle"
    exit 0
}

$Source = Join-Path $Here $ExeName
if (-not (Test-Path $Source)) {
    throw "anvil-desktop.exe not found next to install.ps1"
}

New-Item -ItemType Directory -Force -Path $Dest | Out-Null
Copy-Item -Force $Source (Join-Path $Dest $ExeName)
$Readme = Join-Path $Here "README.txt"
if (Test-Path $Readme) {
    Copy-Item -Force $Readme (Join-Path $Dest "README.txt")
}

New-Item -ItemType Directory -Force -Path $StartMenu | Out-Null
$shell = New-Object -ComObject WScript.Shell
$link = $shell.CreateShortcut($Shortcut)
$link.TargetPath = Join-Path $Dest $ExeName
$link.Arguments = "--open"
$link.WorkingDirectory = $Dest
$link.Description = $ProductTitle
$link.Save()

Write-Host "Installed $ProductTitle to $Dest"
Write-Host "Start Menu shortcut: $Shortcut"
Write-Host "Loopback UI: http://127.0.0.1:1738"
Write-Host "This process signs in to Primaris agents (Chat / Wrapper)."
Write-Host "Local is a second page: activate already-installed grok / Codex / OpenCode (Operate on WSL when chosen)."
