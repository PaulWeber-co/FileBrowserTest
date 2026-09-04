#!/usr/bin/env bash
#
# SpeedNAS - ein Befehl, alles laeuft.
#
#   ./start.sh                  starten (baut beim ersten Mal)
#   ./scripts/speednas.sh stop  anhalten
#   ./scripts/speednas.sh logs  zusehen
#
# Getestet mit bash unter Linux und macOS. Fuer Windows: scripts\speednas.ps1

set -euo pipefail

# ------------------------------------------------------------- Ausgabe -----
if [ -t 1 ] && [ "${NO_COLOR:-}" = "" ]; then
    B=$'\033[1m'; DIM=$'\033[2m'; R=$'\033[0m'
    GRN=$'\033[32m'; YLW=$'\033[33m'; RED=$'\033[31m'; BLU=$'\033[36m'
else
    B=""; DIM=""; R=""; GRN=""; YLW=""; RED=""; BLU=""
fi

info()  { printf '%s\n' "$*"; }
step()  { printf '%s->%s %s\n' "$BLU" "$R" "$*"; }
ok()    { printf '%s  ok%s %s\n' "$GRN" "$R" "$*"; }
warn()  { printf '%s  !!%s %s\n' "$YLW" "$R" "$*"; }
die()   { printf '\n%sFehler:%s %s\n\n' "$RED" "$R" "$*" >&2; exit 1; }

# --------------------------------------------------------- Grundlagen ------
cd "$(dirname "$0")/.."
ROOT="$PWD"
[ -f Dockerfile ] || die "Dockerfile nicht gefunden. Liegt das Skript noch im Projektordner?"

PORT="${SPEEDNAS_PORT:-8088}"
TARGET="${SPEEDNAS_TARGET:-runtime}"
DO_BUILD=1
CMD="start"

usage() {
    cat <<USAGE
${B}SpeedNAS mit Docker${R}

  ./start.sh [Befehl] [Optionen]

Befehle
  start      starten (Vorgabe); baut das Image, wenn noetig
  stop       anhalten
  restart    neu starten
  status     Zustand und Adressen anzeigen
  logs       Ausgaben mitlesen (Strg+C beendet)
  update     Quelltext neu bauen und neu starten
  shell      Shell im Container oeffnen (nicht bei --slim)
  probe HOST SMB-Version eines Routers pruefen, z. B.: probe 192.168.2.1
  adduser N  Benutzer anlegen oder Passwort aendern
  url        nur die Adressen ausgeben
  reset      Container und ALLE Daten loeschen (fragt nach)

Optionen
  --port N       anderer Port auf dem Rechner (Vorgabe 8088)
  --slim         kleineres Image ohne ffmpeg (keine Video-/HEIC-Vorschauen)
  --no-build     nicht neu bauen, vorhandenes Image verwenden
  -h, --help     diese Hilfe
USAGE
}

# --------------------------------------------------------- Argumente -------
while [ $# -gt 0 ]; do
    case "$1" in
        start|stop|restart|status|logs|update|shell|url|reset) CMD="$1"; shift ;;
        probe)   CMD="probe";   PROBE_HOST="${2:-}"; shift; [ $# -gt 0 ] && shift ;;
        adduser) CMD="adduser"; NEW_USER="${2:-}";  shift; [ $# -gt 0 ] && shift ;;
        --port)  PORT="${2:?--port braucht eine Nummer}"; shift 2 ;;
        --slim)  TARGET="slim"; shift ;;
        --no-build) DO_BUILD=0; shift ;;
        -h|--help) usage; exit 0 ;;
        *) die "Unbekannte Angabe: $1  (./start.sh --help)" ;;
    esac
done

export SPEEDNAS_PORT="$PORT"
export SPEEDNAS_TARGET="$TARGET"

# ------------------------------------------------- Docker vorhanden? -------
check_docker() {
    if ! command -v docker >/dev/null 2>&1; then
        cat >&2 <<EOF

${RED}Docker ist nicht installiert.${R}

  Linux / Raspberry Pi:
      curl -fsSL https://get.docker.com | sh
      sudo usermod -aG docker \$USER      # danach ab- und wieder anmelden

  macOS:
      https://www.docker.com/products/docker-desktop/
      (oder: brew install --cask orbstack)

  Windows:
      Docker Desktop installieren und dann start.bat verwenden.

EOF
        exit 1
    fi

    if ! docker info >/dev/null 2>&1; then
        cat >&2 <<EOF

${RED}Docker laeuft nicht.${R}

  macOS / Windows:  Docker Desktop starten und warten, bis das Wal-Symbol ruhig ist.
  Linux:            sudo systemctl start docker

  Kommt "permission denied", fehlt die Gruppenmitgliedschaft:
      sudo usermod -aG docker \$USER      # danach neu anmelden

EOF
        exit 1
    fi

    # Compose v2 ist eingebaut, v1 war ein eigenes Programm.
    if docker compose version >/dev/null 2>&1; then
        DC=(docker compose)
    elif command -v docker-compose >/dev/null 2>&1; then
        DC=(docker-compose)
        warn "Altes docker-compose gefunden. Es funktioniert, aber Docker Compose v2 ist besser."
    else
        die "Docker Compose fehlt. Bei Docker Desktop ist es dabei; unter Linux: sudo apt install docker-compose-plugin"
    fi
}

# ------------------------------------------------------- Hilfsmittel -------

# .env anlegen, damit der Dienst im Container unter deiner Kennung laeuft.
ensure_env() {
    [ -f .env ] && return 0
    step "Lege .env an (Kennung und Zeitzone)"
    local uid gid tz
    uid="$(id -u)"; gid="$(id -g)"
    # Als root gestartet? Dann NICHT 0 eintragen - sonst liefe der Dienst im
    # Container als root, und genau das wollen wir vermeiden.
    if [ "$uid" = "0" ]; then
        uid=1000; gid=1000
        warn "Als root gestartet - der Dienst läuft trotzdem als 1000:1000."
    fi
    tz="$(cat /etc/timezone 2>/dev/null || true)"
    [ -z "$tz" ] && tz="$(readlink /etc/localtime 2>/dev/null | sed 's#.*/zoneinfo/##')"
    [ -z "$tz" ] && tz="Europe/Berlin"
    cat > .env <<ENVEOF
# Automatisch angelegt von start.sh - anpassbar.
PUID=$uid
PGID=$gid
TZ=$tz

# Passwort des Netzwerkspeichers. In der SpeedNAS-Oberflaeche traegst du beim
# Speicherort dann woertlich \${NAS_PASSWORT} ein statt des echten Passworts.
NAS_PASSWORT=
ENVEOF
    ok ".env angelegt (PUID=$uid, PGID=$gid, TZ=$tz)"
}

# Die Adresse dieses Rechners im Heimnetz. Die IP des Containers hilft nicht -
# vom Handy aus ist nur der Wirt erreichbar.
lan_ip() {
    local ip=""
    if command -v ip >/dev/null 2>&1; then
        ip="$(ip route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="src"){print $(i+1); exit}}')"
    fi
    if [ -z "$ip" ] && command -v ipconfig >/dev/null 2>&1; then
        for dev in en0 en1; do
            ip="$(ipconfig getifaddr "$dev" 2>/dev/null || true)"
            [ -n "$ip" ] && break
        done
    fi
    if [ -z "$ip" ] && command -v hostname >/dev/null 2>&1; then
        ip="$(hostname -I 2>/dev/null | awk '{print $1}')" || true
    fi
    printf '%s' "$ip"
}

# Im "runtime"-Image liegt das Programm in /usr/local/bin (also im Suchpfad),
# im "slim"-Image direkt unter /speednas. Einmal feststellen statt jedes Mal
# einen Fehlversuch zu machen.
speednas_bin() {
    if "${DC[@]}" exec -T speednas /speednas -version >/dev/null 2>&1; then
        printf '/speednas'
    else
        printf 'speednas'
    fi
}

http_get() {
    if command -v curl >/dev/null 2>&1; then
        curl -sf --max-time 3 "$1" >/dev/null 2>&1
    else
        wget -q -O /dev/null --timeout=3 "$1" 2>/dev/null
    fi
}

# Bekannte Fehlerbilder in den Container-Ausgaben erkennen und erklaeren.
# Die Meldungen von Docker sind an genau diesen Stellen irrefuehrend.
explain_failure() {
    local logs="$1"

    if printf '%s' "$logs" | grep -q "docker-entrypoint.sh: no such file or directory"; then
        cat <<'HINT'

   Diagnose: Das Startskript im Container hat Windows-Zeilenenden (CRLF).

   Die Meldung meint nicht das Skript, sondern den Interpreter: Aus der ersten
   Zeile "#!/bin/sh" wurde "#!/bin/sh\r", und ein Programm dieses Namens gibt
   es nicht. Git wandelt unter Windows beim Auschecken standardmaessig um.

   Behebung - neueste Fassung holen und neu bauen:

       git pull
       ./start.sh update

   Das Dockerfile bereinigt die Zeilenenden inzwischen selbst.
   Wer den Arbeitsordner zusaetzlich sauber ziehen will:

       git rm --cached -r . >/dev/null && git reset --hard

HINT
        return 0
    fi

    if printf '%s' "$logs" | grep -qiE "permission denied|read-only file system"; then
        cat <<'HINT'

   Diagnose: Der Dienst darf nicht in sein Datenverzeichnis schreiben.

   Meist gehoert ein eingebundener Ordner einem anderen Benutzer. Deine
   Kennung findest du mit "id -u" und "id -g"; beides gehoert in die .env:

       PUID=1000
       PGID=1000

HINT
        return 0
    fi

    if printf '%s' "$logs" | grep -qiE "address already in use|port is already allocated"; then
        cat <<HINT

   Diagnose: Port ${PORT} ist bereits belegt.

   Anderen Port nehmen:   ./start.sh --port 9000

HINT
        return 0
    fi

    if printf '%s' "$logs" | grep -q "exec format error"; then
        cat <<'HINT'

   Diagnose: Das Image passt nicht zur Architektur dieses Rechners.

   Ein auf dem PC gebautes Image laeuft nicht auf einem Raspberry Pi und
   umgekehrt. Auf dem Zielgeraet selbst bauen:

       ./start.sh update

HINT
        return 0
    fi
    return 1
}

wait_healthy() {
    local url="http://127.0.0.1:${PORT}/health" i=0 max=90
    printf '   warte auf den Dienst '
    while [ $i -lt $max ]; do
        if http_get "$url"; then printf ' %sbereit%s\n' "$GRN" "$R"; return 0; fi
        # Ist der Container zwischendurch gestorben? Dann nicht weiter warten.
        if ! "${DC[@]}" ps --status running 2>/dev/null | grep -q speednas; then
            if [ $i -gt 3 ]; then
                printf '\n'
                warn "Der Container laeuft nicht mehr. Letzte Ausgaben:"
                "${DC[@]}" logs --tail 30 2>&1 | sed 's/^/     /'
                return 1
            fi
        fi
        printf '.'
        sleep 1
        i=$((i + 1))
    done
    printf '\n'
    warn "Nach ${max}s keine Antwort. Ausgaben:"
    local logs
    logs="$("${DC[@]}" logs --tail 30 2>&1)"
    printf '%s\n' "$logs" | sed 's/^/     /'
    explain_failure "$logs" || true
    return 1
}

print_addresses() {
    local ip; ip="$(lan_ip)"
    local host_url="http://localhost:${PORT}"
    local lan_url=""
    [ -n "$ip" ] && lan_url="http://${ip}:${PORT}"

    printf '\n'
    printf '  %s┌────────────────────────────────────────────────────────┐%s\n' "$GRN" "$R"
    printf '  %s│%s  %sSpeedNAS läuft%s                                        %s│%s\n' "$GRN" "$R" "$B" "$R" "$GRN" "$R"
    printf '  %s└────────────────────────────────────────────────────────┘%s\n' "$GRN" "$R"
    printf '\n'
    printf '   %sAm PC%s        %s\n' "$B" "$R" "$host_url"
    if [ -n "$lan_url" ]; then
        printf '   %sAm Handy%s     %s%s%s\n' "$B" "$R" "$B" "$lan_url" "$R"
    else
        printf '   %sAm Handy%s     %sIP nicht erkannt - "ip addr" bzw. "ifconfig" zeigt sie%s\n' "$B" "$R" "$DIM" "$R"
    fi
    printf '\n'

    if [ -n "$lan_url" ] && command -v qrencode >/dev/null 2>&1; then
        printf '   Mit der Kamera scannen:\n\n'
        qrencode -t ANSIUTF8 -m 2 "$lan_url" | sed 's/^/   /'
        printf '\n'
    fi

    cat <<NEXT
   ${DIM}Beim ersten Aufruf legst du einen Zugang an. Danach:
   "Standort hinzufügen" -> SMB -> Adresse des Routers (meist 192.168.2.1)
   -> "Freigaben suchen" -> Verbindung testen.

   Aufs Handy als App: Seite in Safari/Chrome öffnen, Teilen-Menü,
   "Zum Home-Bildschirm".${R}

NEXT

    if [ -n "$lan_url" ]; then
        cat <<FW
   ${DIM}Kommt das Handy nicht durch, blockiert meist die Firewall des Rechners
   Port ${PORT}. Beide Geräte müssen im selben WLAN sein (kein Gastnetz).${R}

FW
    fi
}

# ---------------------------------------------------------- Befehle --------
cmd_start() {
    check_docker
    ensure_env

    # Belegt jemand anders den Port?
    if http_get "http://127.0.0.1:${PORT}/health"; then
        if "${DC[@]}" ps --status running 2>/dev/null | grep -q speednas; then
            ok "SpeedNAS läuft bereits."
            print_addresses
            return 0
        fi
    fi

    if [ "$DO_BUILD" = "1" ]; then
        step "Baue das Image (beim ersten Mal dauert das ein paar Minuten)"
        if ! "${DC[@]}" build 2>&1 | sed 's/^/     /'; then
            die "Der Bau ist fehlgeschlagen. Die Ausgaben oben sagen, woran es lag."
        fi
        ok "Image gebaut"
    fi

    step "Starte den Container"
    "${DC[@]}" up -d 2>&1 | sed 's/^/     /'

    wait_healthy || die "SpeedNAS ist nicht hochgekommen. Mehr sehen: ./scripts/speednas.sh logs"
    print_addresses
}

cmd_stop() {
    check_docker
    step "Halte an"
    "${DC[@]}" down 2>&1 | sed 's/^/     /'
    ok "Angehalten. Die Daten bleiben im Volume erhalten."
}

cmd_status() {
    check_docker
    "${DC[@]}" ps
    printf '\n'
    if http_get "http://127.0.0.1:${PORT}/health"; then
        ok "Der Dienst antwortet."
        print_addresses
    else
        warn "Der Dienst antwortet nicht auf Port ${PORT}."
    fi
}

cmd_update() {
    check_docker
    step "Baue neu"
    "${DC[@]}" build --pull 2>&1 | sed 's/^/     /'
    step "Starte neu"
    "${DC[@]}" up -d 2>&1 | sed 's/^/     /'
    wait_healthy && ok "Aktualisiert." && print_addresses
}

cmd_reset() {
    check_docker
    printf '\n%sDas löscht den Container UND alle Daten:%s\n' "$YLW" "$R"
    printf '  Zugänge, eingerichtete Speicherorte, Freigabelinks, Vorschaubilder.\n'
    printf '  Auf dem Netzwerkspeicher selbst wird nichts angerührt.\n\n'
    printf 'Wirklich? Dann "loeschen" tippen: '
    read -r answer
    [ "$answer" = "loeschen" ] || { info "Abgebrochen."; return 0; }
    "${DC[@]}" down -v 2>&1 | sed 's/^/     /'
    ok "Alles entfernt."
}

case "$CMD" in
    start)   cmd_start ;;
    stop)    cmd_stop ;;
    restart) cmd_stop; cmd_start ;;
    status)  cmd_status ;;
    update)  cmd_update ;;
    reset)   cmd_reset ;;
    logs)    check_docker; "${DC[@]}" logs -f --tail 100 ;;
    url)     check_docker; print_addresses ;;
    shell)
        check_docker
        "${DC[@]}" exec speednas sh 2>/dev/null \
            || die "Keine Shell im Container. Das \"slim\"-Image hat bewusst keine."
        ;;
    probe)
        check_docker
        [ -n "${PROBE_HOST:-}" ] || die "Beispiel: ./scripts/speednas.sh probe 192.168.2.1"
        "${DC[@]}" exec -T speednas "$(speednas_bin)" -probe "$PROBE_HOST"
        ;;
    adduser)
        check_docker
        [ -n "${NEW_USER:-}" ] || die "Beispiel: ./scripts/speednas.sh adduser paul"
        "${DC[@]}" exec speednas "$(speednas_bin)" -add-user "$NEW_USER"
        ;;
esac
