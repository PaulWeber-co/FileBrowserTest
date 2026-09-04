#!/bin/sh
# Startskript des Containers.
#
# Es loest zwei Dinge, die sonst fast jeden beim ersten Mal treffen:
#
# 1. Rechte auf eingebundenen Verzeichnissen. Ein Verzeichnis vom Wirt behaelt
#    dessen Besitzer. Laeuft der Dienst im Container unter einer anderen
#    Nutzerkennung, darf er nicht hineinschreiben - "permission denied", und
#    niemand weiss warum. Startet der Container als root, wird /data hier
#    zurechtgerueckt und danach werden die Root-Rechte abgelegt.
#
# 2. Bequeme Schalter. "docker run speednas -probe 192.168.2.1" soll
#    funktionieren, ohne dass man den Programmnamen wiederholen muss.
set -e

# Beginnt das erste Argument mit einem Bindestrich, sind es unsere Schalter -
# dann den Programmnamen davorsetzen.
if [ "${1#-}" != "$1" ]; then
    set -- speednas "$@"
fi

if [ "$(id -u)" = "0" ]; then
    PUID="${PUID:-1000}"
    PGID="${PGID:-1000}"

    # Nur die Wurzel von /data anfassen, nicht rekursiv: bei zehntausenden
    # Vorschaubildern dauerte ein "chown -R" sonst bei jedem Start ewig.
    owner="$(stat -c '%u:%g' /data 2>/dev/null || echo 'unbekannt')"
    if [ "$owner" != "${PUID}:${PGID}" ]; then
        echo "SpeedNAS: /data gehoert ${owner}, wird auf ${PUID}:${PGID} gesetzt"
        chown "${PUID}:${PGID}" /data 2>/dev/null || cat <<WARN
SpeedNAS: /data liess sich nicht uebernehmen.
          Auf dem Wirt einmalig ausfuehren:
              sudo chown -R ${PUID}:${PGID} <dein-datenverzeichnis>
          Oder den Container mit passender Kennung starten:
              docker run --user "\$(id -u):\$(id -g)" ...
WARN
    fi

    # Root-Rechte ablegen. Numerische Kennungen, damit auch Werte ohne
    # passenden Eintrag in /etc/passwd funktionieren.
    exec su-exec "${PUID}:${PGID}" "$@"
fi

# Bereits als normaler Benutzer gestartet (user: in compose) - nichts zu tun.
exec "$@"
