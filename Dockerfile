# syntax=docker/dockerfile:1
#
# SpeedNAS als Container.
#
# Zweistufiger Bau: In der ersten Stufe steht das komplette Go-Werkzeug
# (mehrere hundert MB), in der zweiten landet nur noch das fertige Programm.
# Das Endergebnis ist dadurch rund 100 MB statt fast einem Gigabyte - der
# groesste Brocken darin ist ffmpeg. Ohne das (Ziel "slim") sind es ~20 MB.
# Nachmessen: docker images
#
# Bauen:    docker build -t speednas .
# Starten:  docker run -d -p 8088:8088 -v speednas-data:/data speednas

# ---------------------------------------------------------------- Stufe 1 --
# --platform=$BUILDPLATFORM: der Compiler laeuft immer nativ auf der Maschine,
# die baut. Go kann fuer fremde Architekturen uebersetzen, ohne dass eine
# langsame Emulation noetig waere - ein Bau fuer den Raspberry Pi dauert damit
# Sekunden statt Minuten.
FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS builder

# Diese Werte setzt buildx automatisch. Beim einfachen "docker build" bleiben
# sie leer, dann uebernimmt Go die Vorgabe der Maschine.
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=docker

WORKDIR /src

# Erst nur die Abhaengigkeitslisten kopieren und herunterladen.
#
# Der Grund ist der Ebenen-Cache: Docker baut jede Anweisung als eigene Ebene
# und verwendet sie wieder, solange sich ihre Eingaben nicht aendern. Weil
# go.mod sich selten aendert, der Quelltext aber staendig, spart diese
# Reihenfolge bei jedem Bau den kompletten Download.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# Jetzt der Quelltext. Ab hier wird bei jeder Aenderung neu gebaut - aber eben
# nur ab hier.
COPY . .

# --mount=type=cache haelt den Go-Build-Cache zwischen Bauvorgaengen vor,
# ohne dass er im Image landet. Zweiter Bau: Sekunden statt Minuten.
#
# CGO_ENABLED=0 erzeugt ein statisch gebundenes Programm ohne Abhaengigkeit
# von der C-Bibliothek. Nur deshalb laeuft dasselbe Binary spaeter auch in
# einem sehr kleinen Basis-Image.
#
# -trimpath entfernt Pfade des Bausystems, -s -w die Debug-Informationen.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/speednas ./cmd/speednas

# Ein leeres Verzeichnis mit der richtigen Kennung vorbereiten. In "scratch"
# gibt es kein RUN und damit kein chown - der Umweg ueber COPY --chown ist der
# einzige Weg, dort ein beschreibbares /data zu bekommen.
RUN mkdir -p /emptydata

# ---------------------------------------------------------------- Stufe 2 --
# Alpine statt scratch, weil ffmpeg gebraucht wird: damit entstehen Vorschauen
# fuer Videos und fuer HEIC-Fotos vom iPhone. Wer darauf verzichten kann,
# nimmt das Ziel "slim" weiter unten - das ist dann rund 20 MB gross.
FROM alpine:3.21 AS runtime

# ca-certificates: sonst schlaegt jede HTTPS-Verbindung fehl (WebDAV!)
# tzdata:          sonst laeuft die Uhr im Container auf UTC
# ffmpeg:          Video- und HEIC-Vorschauen
# su-exec:         zum Ablegen der Root-Rechte im Startskript
RUN apk add --no-cache ca-certificates tzdata ffmpeg su-exec

# Ein eigener Benutzer. Ein Container, der als root laeuft, laeuft im
# Zweifel auch mit root-Rechten auf gemountete Verzeichnisse los.
RUN addgroup -g 1000 speednas && \
    adduser -D -u 1000 -G speednas -h /data speednas

# Beschreibende Etiketten nach OCI-Standard. "docker inspect" zeigt sie an,
# Werkzeuge wie Watchtower oder Renovate lesen sie aus.
LABEL org.opencontainers.image.title="SpeedNAS" \
      org.opencontainers.image.description="Dateibrowser fuer Netzwerkspeicher (SMB, FTP, SFTP, WebDAV)" \
      org.opencontainers.image.source="https://github.com/PaulWeber-co/FileBrowserTest" \
      org.opencontainers.image.licenses="MIT"

COPY --from=builder /out/speednas /usr/local/bin/speednas
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh

# Zeilenenden bereinigen, bevor das Skript ausfuehrbar gemacht wird.
#
# Wurde das Repository unter Windows ausgecheckt, kann Git aus jedem "\n" ein
# "\r\n" gemacht haben. Der Shebang lautet dann "#!/bin/sh\r", und der Kernel
# sucht einen Interpreter namens "/bin/sh\r". Docker meldet daraufhin
# "exec /usr/local/bin/docker-entrypoint.sh: no such file or directory" -
# gemeint ist aber der Interpreter, nicht das Skript.
#
# .gitattributes verhindert das an der Wurzel; diese Zeile macht das Image
# zusaetzlich unempfindlich, damit auch ein bereits schief ausgecheckter
# Arbeitsordner ohne weiteres Zutun funktioniert.
RUN tr -d '\r' < /usr/local/bin/docker-entrypoint.sh > /tmp/entrypoint && \
    mv /tmp/entrypoint /usr/local/bin/docker-entrypoint.sh && \
    chmod +x /usr/local/bin/docker-entrypoint.sh

# Alles Veraenderliche liegt unter /data: Konfiguration, Sitzungen,
# Vorschaubilder, angefangene Uploads.
ENV SPEEDNAS_CONFIG=/data/config.json \
    SPEEDNAS_DATA=/data \
    SPEEDNAS_LISTEN=:8088 \
    HEALTH_URL=http://127.0.0.1:8088/health \
    PUID=1000 \
    PGID=1000 \
    TZ=Europe/Berlin

RUN mkdir -p /data && chown speednas:speednas /data
VOLUME ["/data"]

EXPOSE 8088

# Docker fragt selbst regelmaessig nach, ob der Dienst noch antwortet.
# "docker ps" zeigt das Ergebnis als (healthy) bzw. (unhealthy) an.
# Wird der Port geaendert, muss HEALTH_URL mitgeaendert werden - sonst
# meldet Docker den Container faelschlich als "unhealthy".
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -q -O /dev/null "$HEALTH_URL" || exit 1

ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
CMD ["speednas"]

# ------------------------------------------------------- Stufe 2, Variante --
# Die kleinste mögliche Variante: "scratch" ist ein vollkommen leeres Image.
# Kein Betriebssystem, keine Shell, keine Paketverwaltung - nur das Programm.
#
#   Vorteil:   ~15 MB statt ~100 MB, und praktisch keine Angriffsflaeche.
#              Es gibt schlicht nichts, was verwundbar sein koennte.
#   Nachteil:  ohne ffmpeg keine Vorschauen fuer Videos und HEIC-Fotos, und
#              "docker exec ... sh" geht nicht (es gibt keine Shell).
#
# Moeglich ist das nur, weil das Programm mit CGO_ENABLED=0 statisch gebunden
# ist und die Zeitzonendatenbank im Programm steckt.
#
#   docker build --target slim -t speednas:slim .
FROM scratch AS slim

# Ohne diese Datei schlaegt jede HTTPS-Verbindung fehl (WebDAV, Freigabelinks).
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt

# Leeres, dem Dienstbenutzer gehoerendes /data. Docker uebernimmt Besitzer und
# Rechte dieses Verzeichnisses beim Anlegen eines benannten Volumes - nur
# deshalb darf der unprivilegierte Prozess spaeter hineinschreiben.
COPY --from=builder --chown=1000:1000 /emptydata /data

COPY --from=builder /out/speednas /speednas

LABEL org.opencontainers.image.title="SpeedNAS (slim)" \
      org.opencontainers.image.description="Dateibrowser fuer Netzwerkspeicher, minimales Image" \
      org.opencontainers.image.source="https://github.com/PaulWeber-co/FileBrowserTest" \
      org.opencontainers.image.licenses="MIT"

ENV SPEEDNAS_CONFIG=/data/config.json \
    SPEEDNAS_DATA=/data \
    SPEEDNAS_LISTEN=:8088 \
    TZ=Europe/Berlin

# Rein numerisch, weil es in scratch keine /etc/passwd gibt.
USER 1000:1000
VOLUME ["/data"]
EXPOSE 8088

# Kein wget vorhanden - deshalb prueft das Programm sich selbst.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["/speednas", "-health"]

ENTRYPOINT ["/speednas"]
