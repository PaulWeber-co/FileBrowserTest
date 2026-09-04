<#
.SYNOPSIS
    SpeedNAS mit Docker - ein Befehl, alles laeuft.

.DESCRIPTION
    Startet SpeedNAS als Container und zeigt am Ende die Adresse, unter der
    das Handy darauf zugreifen kann.

.EXAMPLE
    .\start.bat
    .\scripts\speednas.ps1 status
    .\scripts\speednas.ps1 probe 192.168.2.1
#>
[CmdletBinding()]
param(
    [Parameter(Position = 0)]
    [ValidateSet('start', 'stop', 'restart', 'status', 'logs', 'update', 'shell',
                 'probe', 'adduser', 'url', 'reset', 'help')]
    [string]$Command = 'start',

    [Parameter(Position = 1)]
    [string]$Argument = '',

    [int]$Port = 8088,
    [switch]$Slim,
    [switch]$NoBuild
)

$ErrorActionPreference = 'Stop'
Set-Location (Join-Path $PSScriptRoot '..')

if (-not (Test-Path 'Dockerfile')) {
    throw 'Dockerfile nicht gefunden. Liegt das Skript noch im Projektordner?'
}

$env:SPEEDNAS_PORT = $Port
$env:SPEEDNAS_TARGET = if ($Slim) { 'slim' } else { 'runtime' }

# --------------------------------------------------------------- Ausgabe ---
function Write-Step { param($m) Write-Host "-> " -ForegroundColor Cyan -NoNewline; Write-Host $m }
function Write-Ok   { param($m) Write-Host "  ok " -ForegroundColor Green -NoNewline; Write-Host $m }
function Write-Warn { param($m) Write-Host "  !! " -ForegroundColor Yellow -NoNewline; Write-Host $m }
function Stop-WithError {
    param($m)
    Write-Host ''
    Write-Host "Fehler: " -ForegroundColor Red -NoNewline
    Write-Host $m
    Write-Host ''
    exit 1
}

# ------------------------------------------------------- Docker pruefen ----
function Test-Docker {
    if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
        Write-Host ''
        Write-Host 'Docker ist nicht installiert.' -ForegroundColor Red
        Write-Host ''
        Write-Host '  Docker Desktop herunterladen und installieren:'
        Write-Host '  https://www.docker.com/products/docker-desktop/'
        Write-Host ''
        Write-Host '  Danach den Rechner neu starten und dieses Skript erneut ausfuehren.'
        Write-Host ''
        exit 1
    }

    docker info 2>&1 | Out-Null
    if ($LASTEXITCODE -ne 0) {
        Write-Host ''
        Write-Host 'Docker laeuft nicht.' -ForegroundColor Red
        Write-Host ''
        Write-Host '  Starte Docker Desktop und warte, bis das Wal-Symbol unten rechts'
        Write-Host '  ruhig ist (nicht mehr animiert). Dann dieses Skript erneut ausfuehren.'
        Write-Host ''
        exit 1
    }

    docker compose version 2>&1 | Out-Null
    if ($LASTEXITCODE -ne 0) {
        Stop-WithError 'Docker Compose fehlt. Bei Docker Desktop ist es normalerweise dabei.'
    }
}

# ---------------------------------------------------------- Hilfsmittel ----
function Initialize-EnvFile {
    if (Test-Path '.env') { return }
    Write-Step 'Lege .env an'
    $tz = (Get-TimeZone).Id
    # Windows nutzt eigene Zeitzonennamen; der Container braucht die
    # IANA-Schreibweise. Fuer den deutschsprachigen Raum passt das hier.
    if ($tz -match 'W. Europe|Mitteleurop') { $tz = 'Europe/Berlin' }
    @"
# Automatisch angelegt von speednas.ps1 - anpassbar.
# Unter Windows kuemmert sich Docker Desktop um die Dateirechte;
# PUID/PGID sind hier nur der Vollstaendigkeit halber gesetzt.
PUID=1000
PGID=1000
TZ=$tz

# Passwort des Netzwerkspeichers. In der SpeedNAS-Oberflaeche traegst du beim
# Speicherort dann woertlich `${NAS_PASSWORT} ein statt des echten Passworts.
NAS_PASSWORT=
"@ | Set-Content -Path '.env' -Encoding UTF8
    Write-Ok ".env angelegt (TZ=$tz)"
}

function Get-LanAddress {
    try {
        # Die Adresse der Karte, ueber die der Standard-Weg nach draussen geht.
        $cfg = Get-NetIPConfiguration -ErrorAction Stop |
               Where-Object { $_.IPv4DefaultGateway -and $_.NetAdapter.Status -eq 'Up' } |
               Select-Object -First 1
        if ($cfg) { return $cfg.IPv4Address.IPAddress }
    } catch { }
    try {
        return (Get-NetIPAddress -AddressFamily IPv4 -ErrorAction Stop |
                Where-Object { $_.IPAddress -notmatch '^(127\.|169\.254\.)' } |
                Select-Object -First 1).IPAddress
    } catch { }
    return $null
}

function Test-Health {
    try {
        $r = Invoke-WebRequest -Uri "http://127.0.0.1:$Port/health" `
             -TimeoutSec 3 -UseBasicParsing -ErrorAction Stop
        return $r.StatusCode -eq 200
    } catch { return $false }
}

function Wait-Healthy {
    Write-Host '   warte auf den Dienst ' -NoNewline
    for ($i = 0; $i -lt 90; $i++) {
        if (Test-Health) {
            Write-Host ' bereit' -ForegroundColor Green
            return $true
        }
        Write-Host '.' -NoNewline
        Start-Sleep -Seconds 1
    }
    Write-Host ''
    Write-Warn 'Nach 90s keine Antwort. Letzte Ausgaben:'
    docker compose logs --tail 30
    return $false
}

function Show-Addresses {
    $ip = Get-LanAddress
    Write-Host ''
    Write-Host '  +--------------------------------------------------------+' -ForegroundColor Green
    Write-Host '  |  SpeedNAS laeuft                                       |' -ForegroundColor Green
    Write-Host '  +--------------------------------------------------------+' -ForegroundColor Green
    Write-Host ''
    Write-Host '   Am PC        ' -NoNewline; Write-Host "http://localhost:$Port"
    if ($ip) {
        Write-Host '   Am Handy     ' -NoNewline
        Write-Host "http://${ip}:$Port" -ForegroundColor White
    } else {
        Write-Host '   Am Handy     IP nicht erkannt - "ipconfig" zeigt sie an'
    }
    Write-Host ''
    Write-Host '   Beim ersten Aufruf legst du einen Zugang an. Danach:' -ForegroundColor DarkGray
    Write-Host '   "Standort hinzufuegen" -> SMB -> Adresse des Routers' -ForegroundColor DarkGray
    Write-Host '   (meist 192.168.2.1) -> "Freigaben suchen".' -ForegroundColor DarkGray
    Write-Host ''
    Write-Host '   Aufs Handy als App: Seite in Safari/Chrome oeffnen,' -ForegroundColor DarkGray
    Write-Host '   Teilen-Menue, "Zum Home-Bildschirm".' -ForegroundColor DarkGray
    Write-Host ''
    if ($ip) {
        Write-Host '   Kommt das Handy nicht durch, fragt Windows beim ersten Mal nach' -ForegroundColor DarkGray
        Write-Host "   der Firewall - dort 'Private Netzwerke' erlauben." -ForegroundColor DarkGray
        Write-Host ''
    }
}

# Im runtime-Image liegt das Programm im Suchpfad, im slim-Image unter /speednas.
function Get-SpeednasBinary {
    docker compose exec -T speednas /speednas -version 2>&1 | Out-Null
    if ($LASTEXITCODE -eq 0) { return '/speednas' }
    return 'speednas'
}

# ------------------------------------------------------------- Befehle -----
function Invoke-Start {
    Test-Docker
    Initialize-EnvFile

    if (-not $NoBuild) {
        Write-Step 'Baue das Image (beim ersten Mal dauert das ein paar Minuten)'
        docker compose build
        if ($LASTEXITCODE -ne 0) {
            Stop-WithError 'Der Bau ist fehlgeschlagen. Die Ausgaben oben sagen, woran es lag.'
        }
        Write-Ok 'Image gebaut'
    }

    Write-Step 'Starte den Container'
    docker compose up -d
    if ($LASTEXITCODE -ne 0) { Stop-WithError 'Der Container liess sich nicht starten.' }

    if (Wait-Healthy) {
        Show-Addresses
        $ip = Get-LanAddress
        $url = if ($ip) { "http://${ip}:$Port" } else { "http://localhost:$Port" }
        Start-Process $url
    } else {
        Stop-WithError 'SpeedNAS ist nicht hochgekommen. Mehr sehen: .\scripts\speednas.ps1 logs'
    }
}

function Invoke-Stop {
    Test-Docker
    Write-Step 'Halte an'
    docker compose down
    Write-Ok 'Angehalten. Die Daten bleiben im Volume erhalten.'
}

function Invoke-Reset {
    Test-Docker
    Write-Host ''
    Write-Host 'Das loescht den Container UND alle Daten:' -ForegroundColor Yellow
    Write-Host '  Zugaenge, eingerichtete Speicherorte, Freigabelinks, Vorschaubilder.'
    Write-Host '  Auf dem Netzwerkspeicher selbst wird nichts angeruehrt.'
    Write-Host ''
    $answer = Read-Host 'Wirklich? Dann "loeschen" tippen'
    if ($answer -ne 'loeschen') { Write-Host 'Abgebrochen.'; return }
    docker compose down -v
    Write-Ok 'Alles entfernt.'
}

switch ($Command) {
    'start'   { Invoke-Start }
    'stop'    { Invoke-Stop }
    'restart' { Invoke-Stop; Invoke-Start }
    'reset'   { Invoke-Reset }
    'url'     { Show-Addresses }
    'logs'    { Test-Docker; docker compose logs -f --tail 100 }
    'status'  {
        Test-Docker
        docker compose ps
        Write-Host ''
        if (Test-Health) { Write-Ok 'Der Dienst antwortet.'; Show-Addresses }
        else { Write-Warn "Der Dienst antwortet nicht auf Port $Port." }
    }
    'update'  {
        Test-Docker
        Write-Step 'Baue neu'
        docker compose build --pull
        Write-Step 'Starte neu'
        docker compose up -d
        if (Wait-Healthy) { Write-Ok 'Aktualisiert.'; Show-Addresses }
    }
    'shell'   {
        Test-Docker
        docker compose exec speednas sh
        if ($LASTEXITCODE -ne 0) {
            Write-Warn 'Keine Shell im Container. Das "slim"-Image hat bewusst keine.'
        }
    }
    'probe'   {
        Test-Docker
        if (-not $Argument) { Stop-WithError 'Beispiel: .\scripts\speednas.ps1 probe 192.168.2.1' }
        docker compose exec -T speednas (Get-SpeednasBinary) -probe $Argument
    }
    'adduser' {
        Test-Docker
        if (-not $Argument) { Stop-WithError 'Beispiel: .\scripts\speednas.ps1 adduser paul' }
        docker compose exec speednas (Get-SpeednasBinary) -add-user $Argument
    }
    'help'    { Get-Help $PSCommandPath -Detailed }
}
