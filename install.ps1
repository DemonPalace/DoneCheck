# Build donecheck from this checkout and install it as a command (Windows).
# Usage: powershell -ExecutionPolicy Bypass -File .\install.ps1
param(
    [string]$InstallDir = (Join-Path $env:LOCALAPPDATA 'donecheck\bin'),
    [switch]$NoPath
)
$ErrorActionPreference = 'Stop'

if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    Write-Error 'Go is not installed or not on PATH. Install Go 1.25+ from https://go.dev/dl/ and retry.'
    exit 1
}

New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
$exe = Join-Path $InstallDir 'donecheck.exe'

Write-Host "Building donecheck with $(go version) ..."
Push-Location $PSScriptRoot
try {
    go build -trimpath -o $exe .
    if ($LASTEXITCODE -ne 0) { throw "go build failed with exit code $LASTEXITCODE" }
} finally {
    Pop-Location
}

# Prove the installed binary actually runs before claiming success.
& $exe help | Out-Null
if ($LASTEXITCODE -ne 0) { throw "installed binary failed to run (exit code $LASTEXITCODE)" }
Write-Host "Installed: $exe"

if ($NoPath) {
    Write-Host "Skipped PATH update. Add $InstallDir to your PATH to run 'donecheck' anywhere."
    return
}

$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
$entries = @()
if ($userPath) { $entries = $userPath -split ';' | Where-Object { $_ } }
if ($entries -notcontains $InstallDir) {
    [Environment]::SetEnvironmentVariable('Path', (($entries + $InstallDir) -join ';'), 'User')
    Write-Host "Added $InstallDir to your user PATH. Open a new terminal, then run: donecheck help"
} else {
    Write-Host 'Ready. Try: donecheck help'
}
