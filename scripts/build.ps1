# Baut SpeedNAS unter Windows nach dist\.
#
#   .\scripts\build.ps1
#   .\scripts\build.ps1 -Version 1.2.0 -AllPlatforms

param(
    [string]$Version = "",
    [switch]$AllPlatforms
)

$ErrorActionPreference = "Stop"
Set-Location (Join-Path $PSScriptRoot "..")

if (-not $Version) {
    try { $Version = (git describe --tags --always --dirty 2>$null) } catch { }
    if (-not $Version) { $Version = "dev" }
}

$ldflags = "-s -w -X main.version=$Version"
New-Item -ItemType Directory -Force -Path dist | Out-Null

function Build($goos, $goarch, $ext, $label) {
    Write-Host "  $label"
    $env:CGO_ENABLED = "0"
    $env:GOOS = $goos
    $env:GOARCH = $goarch
    go build -trimpath -ldflags $ldflags -o "dist/speednas-$goos-$goarch$ext" ./cmd/speednas
    if ($LASTEXITCODE -ne 0) { throw "Bauen fehlgeschlagen: $goos/$goarch" }
}

Write-Host "SpeedNAS $Version wird gebaut:"
Build "windows" "amd64" ".exe" "Windows (64 Bit)"
if ($AllPlatforms) {
    Build "windows" "arm64" ".exe" "Windows (ARM)"
    Build "linux"   "amd64" ""     "Linux (64 Bit)"
    Build "linux"   "arm64" ""     "Linux (ARM)"
    Build "darwin"  "arm64" ""     "macOS (Apple Silicon)"
}

Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED -ErrorAction SilentlyContinue
Write-Host ""
Get-ChildItem dist | ForEach-Object {
    "{0,-34} {1,8:N1} MB" -f $_.Name, ($_.Length / 1MB)
}
