# Docker von Grund auf – erklärt an SpeedNAS

Diese Anleitung bringt dir Docker bei. Nicht als Rezeptsammlung zum
Abschreiben, sondern so, dass du danach selbst entscheiden kannst, wie du
irgendeine Anwendung in einen Container packst.

Als durchgehendes Beispiel dient SpeedNAS – aber jedes Konzept gilt genauso
für eine Datenbank, einen Webserver oder dein nächstes Projekt.

**Inhalt**

1. [Warum überhaupt Container](#1-warum-überhaupt-container)
2. [Das Grundmodell: Image und Container](#2-das-grundmodell-image-und-container)
3. [Installation und erster Kontakt](#3-installation-und-erster-kontakt)
4. [Die zwölf Befehle, die 95 % ausmachen](#4-die-zwölf-befehle-die-95--ausmachen)
5. [Das Dockerfile: Schritt für Schritt](#5-das-dockerfile-schritt-für-schritt)
6. [Ebenen und der Cache](#6-ebenen-und-der-cache)
7. [Mehrstufige Builds](#7-mehrstufige-builds)
8. [Daten, die überleben: Volumes](#8-daten-die-überleben-volumes)
9. [Die Rechte-Falle](#9-die-rechte-falle)
10. [Netzwerk und Ports](#10-netzwerk-und-ports)
11. [Docker Compose](#11-docker-compose)
12. [Fehlersuche](#12-fehlersuche)
13. [Für den Raspberry Pi bauen](#13-für-den-raspberry-pi-bauen)
14. [Images veröffentlichen](#14-images-veröffentlichen)
15. [Gute Gewohnheiten](#15-gute-gewohnheiten)
16. [Übungen](#16-übungen)
17. [Spickzettel](#17-spickzettel)

---

## 1. Warum überhaupt Container

Du kennst das Problem, auch wenn du es noch nie so genannt hast: Ein Programm
läuft auf einem Rechner und auf dem nächsten nicht. Andere Bibliotheksversion,
andere Konfiguration, ein fehlendes Paket. „Bei mir geht's."

Ein **Container** löst das, indem er die Anwendung zusammen mit allem, was sie
zum Laufen braucht, in ein Paket schnürt: Programm, Bibliotheken, Zertifikate,
Verzeichnisstruktur. Dieses Paket läuft überall gleich – auf deinem Windows-PC,
auf einem Raspberry Pi, auf einem Server.

### Container sind keine virtuellen Maschinen

Der wichtigste Unterschied, und der Grund, warum Container praktisch sind:

```
Virtuelle Maschine                   Container
┌─────────────────────────┐          ┌─────────────────────────┐
│  deine App              │          │  deine App              │
│  Bibliotheken           │          │  Bibliotheken           │
│  komplettes Gast-Linux  │          │                         │
│  virtuelle Hardware     │          │                         │
├─────────────────────────┤          ├─────────────────────────┤
│  Hypervisor             │          │  Docker                 │
├─────────────────────────┤          ├─────────────────────────┤
│  Wirt-Betriebssystem    │          │  Wirt-Betriebssystem    │
└─────────────────────────┘          └─────────────────────────┘
   Start: ~30 Sekunden                  Start: ~0,3 Sekunden
   Größe: mehrere GB                    Größe: oft unter 100 MB
```

Eine VM bringt ein vollständiges Betriebssystem samt eigenem Kernel mit. Ein
Container teilt sich den Kernel mit dem Wirt und enthält nur den Rest. Er ist
letztlich ein ganz normaler Prozess auf deinem Rechner – nur einer, der eine
eigene Sicht auf das Dateisystem, das Netzwerk und die Prozessliste hat.

Deshalb startet er sofort und kostet fast nichts. Du kannst zwanzig Container
gleichzeitig laufen lassen; zwanzig VMs würden deinen Rechner in die Knie
zwingen.

> **Für Windows und Mac:** Dort gibt es keinen Linux-Kernel, den man teilen
> könnte. Docker Desktop startet im Hintergrund eine schlanke Linux-VM und
> lässt die Container darin laufen. Du merkst davon nichts, aber es erklärt,
> warum Docker unter Windows etwas mehr Speicher braucht und warum
> Dateizugriffe auf Windows-Ordner langsamer sind als unter Linux.

### Was dir das bei SpeedNAS bringt

- **Kein Go auf dem Zielrechner.** Das Image enthält das fertige Programm.
- **Sauberer Rechner.** Nichts wird installiert; ein `docker rm` und alles ist
  weg, ohne Reste.
- **Automatischer Neustart.** Container starten nach einem Stromausfall von
  selbst wieder – ohne Aufgabenplanung oder systemd-Datei.
- **Gleich auf Pi und PC.** Dasselbe Rezept, andere Architektur.
- **Eingegrenzt.** Der Container sieht vom Wirt nur, was du ihm zeigst.

---

## 2. Das Grundmodell: Image und Container

Das ist der eine Begriff, an dem alles hängt. Wenn du das verinnerlicht hast,
ergibt der Rest sich fast von selbst.

**Image** = ein eingefrorenes, unveränderliches Dateisystem plus die Angabe,
was beim Start ausgeführt werden soll. Ein Bauplan. Eine Vorlage.

**Container** = ein laufendes Exemplar dieses Images.

Der Vergleich, der am besten trägt: Ein Image ist wie eine Installations-DVD
oder ein Systemabbild. Der Container ist der Rechner, der davon gebootet hat.
Aus einem Image kannst du beliebig viele Container starten, und jeder hat
seinen eigenen veränderbaren Bereich.

```
        Dockerfile                Image                    Container
      (dein Rezept)          (das Ergebnis)           (läuft gerade)

     FROM alpine     ──►    speednas:1.0.0    ──►    ┌─ speednas-1
     RUN apk add...          (unveränderlich)   ├─►  ├─ speednas-2
     COPY speednas                              └─►  └─ speednas-3
                          docker build              docker run
```

### Der entscheidende Punkt: Container sind vergänglich

Ein Container schreibt Änderungen in eine dünne, eigene Schicht über dem
Image. Löschst du den Container, ist diese Schicht weg. Das Image bleibt
unberührt.

Das ist kein Nachteil, sondern das ganze Prinzip: Container sollen jederzeit
weggeworfen und neu erzeugt werden können. Ein Update ist deshalb nicht
„installieren", sondern „alten Container weg, neuen aus dem neuen Image
starten".

Woraus sofort die wichtigste Regel folgt:

> **Alles, was einen Neustart überleben soll, muss in ein Volume.**
> Konfiguration, Datenbanken, hochgeladene Dateien. Was nur im Container
> liegt, ist beim nächsten `docker rm` verloren.

Bei SpeedNAS ist das `/data` – dort liegen Konfiguration, Benutzer,
Freigabelinks und Vorschaubilder. Mehr dazu in [Kapitel 8](#8-daten-die-überleben-volumes).

### Registry: der Ort, an dem Images liegen

Images müssen irgendwo herkommen. Eine **Registry** ist ein Server, der Images
verwahrt – wie ein Paketspiegel oder GitHub für Programme.

- **Docker Hub** (`docker.io`) ist die Voreinstellung. `docker pull alpine`
  holt von dort.
- **GitHub Container Registry** (`ghcr.io`) ist praktisch, wenn dein Code
  ohnehin auf GitHub liegt.
- Registries fürs eigene Netz gibt es auch, brauchst du aber selten.

Ein vollständiger Imagename sieht so aus:

```
ghcr.io/paulweber-co/speednas:1.0.0
└──┬──┘ └──────┬──────┘ └──┬───┘ └─┬─┘
Registry     Besitzer     Name   Version ("Tag")
```

Fehlt die Registry, wird Docker Hub angenommen. Fehlt der Tag, wird `latest`
angenommen – was **nicht** „das neueste" bedeutet, sondern schlicht ein Tag
namens `latest` ist. Verlass dich nie darauf: In der Produktion gehört immer
eine feste Version hin, sonst ändert sich dir das Image unter den Füßen.

---

## 3. Installation und erster Kontakt

### Windows

[Docker Desktop](https://www.docker.com/products/docker-desktop/) herunterladen
und installieren. Es braucht WSL 2; der Installer richtet das ein. Nach dem
Neustart läuft ein Wal-Symbol im Infobereich.

### Linux und Raspberry Pi

```bash
curl -fsSL https://get.docker.com | sh

# damit du nicht bei jedem Befehl sudo brauchst:
sudo usermod -aG docker $USER
# danach ab- und wieder anmelden
```

> **Sicherheitshinweis:** Wer in der Gruppe `docker` ist, kann Container mit
> vollem Zugriff auf den Wirt starten – das ist praktisch gleichbedeutend mit
> root. Auf einem Mehrbenutzersystem also bewusst vergeben.

### macOS

Docker Desktop oder – schlanker – [OrbStack](https://orbstack.dev/).

### Läuft es?

```bash
docker run --rm hello-world
```

Das lädt ein winziges Image, startet es, gibt einen Text aus und löscht den
Container wieder (`--rm`). Kommt eine freundliche Begrüßung, ist alles gut.

### Der erste echte Container

Bevor wir SpeedNAS bauen, ein Gefühl für die Sache:

```bash
docker run -it --rm alpine sh
```

Was passiert:

- `run` – Image holen (falls nicht da) und Container starten
- `-i` – Eingabe offen halten (**i**nteractive)
- `-t` – ein Terminal bereitstellen (**t**ty)
- `--rm` – Container nach dem Ende löschen
- `alpine` – das Image
- `sh` – der Befehl darin

Du landest in einer Shell **innerhalb** des Containers. Probier aus:

```sh
cat /etc/os-release     # Alpine Linux, egal auf welchem Wirt du sitzt
ls /                    # ein eigenes, komplettes Dateisystem
ps aux                  # nur die Prozesse des Containers, nicht deine
whoami                  # root - aber nur hier drin
touch /ich-war-hier
exit
```

Und jetzt der Aha-Moment:

```bash
docker run -it --rm alpine sh
ls /ich-war-hier        # ls: /ich-war-hier: No such file or directory
```

Weg. Neuer Container, neue Schreibschicht. Genau das meint „vergänglich".

---

## 4. Die zwölf Befehle, die 95 % ausmachen

Docker hat sehr viele Befehle. Diese hier brauchst du täglich.

### Was läuft gerade?

```bash
docker ps            # laufende Container
docker ps -a         # auch angehaltene und abgestürzte
```

`docker ps -a` ist dein wichtigstes Werkzeug bei der Fehlersuche. In der Spalte
STATUS steht, ob ein Container läuft, wann er beendet wurde und mit welchem
Rückgabewert.

### Container starten

```bash
docker run -d --name speednas -p 8088:8088 speednas:local
```

- `-d` – im Hintergrund (**d**etached), gibt die Konsole sofort frei
- `--name` – ein eigener Name statt eines zufälligen wie `nervous_hopper`
- `-p 8088:8088` – Port abbilden (mehr in [Kapitel 10](#10-netzwerk-und-ports))

### Hineinschauen

```bash
docker logs speednas             # Ausgaben seit dem Start
docker logs -f speednas          # ... und weiter mitlesen (Strg+C beendet)
docker logs --tail 50 speednas   # nur die letzten 50 Zeilen
```

Ein Container hat keine Logdateien im üblichen Sinn: Docker fängt schlicht
alles ab, was das Programm nach stdout und stderr schreibt. **Deshalb sollte
eine Anwendung im Container immer auf die Konsole loggen, nicht in eine
Datei** – sonst siehst du nichts.

### In einen laufenden Container hinein

```bash
docker exec -it speednas sh
```

Das startet eine zusätzliche Shell **neben** dem laufenden Dienst. Zum
Nachsehen, was drin liegt, ob Dateien da sind, ob Rechte stimmen.

Wichtig zu verstehen: `run` erzeugt einen **neuen** Container, `exec` steigt in
einen **bestehenden** ein. Verwechseln die meisten am Anfang.

```bash
docker exec speednas ls -la /data     # einzelner Befehl, ohne Shell
docker exec speednas speednas -version
```

### Anhalten, starten, löschen

```bash
docker stop speednas      # freundlich (SIGTERM, dann nach 10 s SIGKILL)
docker start speednas     # wieder hoch, Daten der Schreibschicht bleiben
docker restart speednas
docker rm speednas        # löschen (muss vorher gestoppt sein)
docker rm -f speednas     # stoppen und löschen in einem
```

### Images verwalten

```bash
docker images                    # was liegt lokal herum
docker pull alpine:3.21          # holen
docker rmi speednas:alt          # löschen
docker image prune               # unbenutzte Zwischen-Images entfernen
```

### Aufräumen

Docker sammelt mit der Zeit gewaltig an: alte Images, gestoppte Container,
Build-Cache. Nach ein paar Wochen sind schnell 30 GB zusammen.

```bash
docker system df                 # was belegt wie viel?
docker system prune              # gestoppte Container, ungenutzte Netze, Cache
docker system prune -a           # zusätzlich alle unbenutzten Images
```

> **Achtung:** `docker system prune --volumes` löscht auch nicht verwendete
> Volumes – und damit womöglich deine Daten. Diesen Schalter nur bewusst
> verwenden.

### Details nachschlagen

```bash
docker inspect speednas          # alles über den Container, als JSON
docker stats                     # laufender Verbrauch: CPU, RAM, Netz
docker top speednas              # Prozessliste im Container
```

`docker inspect` ist geschwätzig. Gezielt fragen geht so:

```bash
docker inspect -f '{{.State.Status}}' speednas
docker inspect -f '{{.NetworkSettings.IPAddress}}' speednas
docker inspect -f '{{json .Mounts}}' speednas | python3 -m json.tool
```

---

## 5. Das Dockerfile: Schritt für Schritt

Ein **Dockerfile** ist das Rezept, aus dem `docker build` ein Image macht.
Jede Zeile ist eine Anweisung, und jede Anweisung erzeugt eine neue Ebene.

Bauen wir das Dockerfile für SpeedNAS von Grund auf – erst naiv, dann gut.

### Der naive erste Versuch

```dockerfile
FROM golang:1.24
WORKDIR /app
COPY . .
RUN go build -o speednas ./cmd/speednas
CMD ["./speednas"]
```

Das funktioniert. Und ist aus drei Gründen schlecht:

1. Das Ergebnis ist **~900 MB groß**, weil der komplette Go-Compiler
   mitkommt – obwohl er nach dem Bauen nie wieder gebraucht wird.
2. Bei **jeder** Quelltextänderung werden alle Abhängigkeiten neu geladen,
   weil `COPY . .` vor dem Download steht.
3. Der Container läuft als **root**.

Alle drei Probleme lösen wir gleich. Erst die Anweisungen selbst.

### Die Anweisungen, die du brauchst

#### FROM – worauf baust du auf

```dockerfile
FROM alpine:3.21
```

Jedes Image beginnt bei einem anderen. `alpine` ist ein sehr kleines Linux
(~8 MB) und der übliche Ausgangspunkt, wenn man wenig braucht.

**Immer eine Version angeben.** `FROM alpine` ohne Tag heißt `alpine:latest`,
und dein Bau von morgen kann auf einer anderen Grundlage stehen als der von
heute. Das ist die häufigste Ursache für „gestern ging es doch noch".

Häufige Ausgangs-Images:

| Image | Größe | wofür |
|---|---|---|
| `alpine:3.21` | 8 MB | fast alles, wenn ein Shell-Userland reicht |
| `debian:12-slim` | 75 MB | wenn Alpine-Eigenheiten stören (musl statt glibc) |
| `gcr.io/distroless/static` | 2 MB | nur ein statisches Programm, keine Shell |
| `scratch` | 0 MB | völlig leer, absolutes Minimum |
| `golang:1.24-alpine` | 250 MB | zum Bauen von Go-Programmen |

#### WORKDIR – das Arbeitsverzeichnis

```dockerfile
WORKDIR /src
```

Setzt das Verzeichnis für alle folgenden Anweisungen und legt es an, falls es
fehlt. Besser als `RUN cd /src`, denn ein `cd` in einem `RUN` gilt nur für
genau diese Zeile.

#### COPY – Dateien ins Image

```dockerfile
COPY go.mod go.sum ./
COPY . .
```

Kopiert aus dem **Build-Kontext** ins Image. Der Build-Kontext ist der Ordner,
den du beim `docker build .` als letzten Punkt angibst – dessen kompletter
Inhalt wird vorher an den Docker-Daemon geschickt.

Deshalb gibt es `.dockerignore`: Was dort steht, wird gar nicht erst
übertragen. Ohne diese Datei wandert bei SpeedNAS auch `.git` und `dist/` mit –
hunderte Megabyte pro Bau, und schlimmer: eine lokale `config.json` mit
NAS-Passwörtern läge im Image.

> `ADD` gibt es auch. Es kann zusätzlich URLs laden und Archive entpacken –
> beides überraschend und selten gewollt. **Nimm COPY.**

#### RUN – Befehle beim Bauen

```dockerfile
RUN apk add --no-cache ca-certificates ffmpeg
```

Führt etwas aus, während das Image entsteht, und friert das Ergebnis als neue
Ebene ein.

Zwei Dinge dabei:

**Befehle zusammenfassen.** Jedes `RUN` ist eine Ebene. Das hier ist schlecht:

```dockerfile
RUN apk update
RUN apk add ca-certificates
RUN apk add ffmpeg
```

Besser eine Zeile mit `&&`. Und `--no-cache` bei apk (bzw.
`rm -rf /var/lib/apt/lists/*` bei apt), sonst bleibt der Paketindex im Image
liegen.

**Löschen in einer späteren Ebene bringt nichts.** Ebenen sind additiv:

```dockerfile
RUN wget riesendatei.tar.gz     # Ebene 1: +500 MB
RUN tar xf riesendatei.tar.gz   # Ebene 2: +500 MB
RUN rm riesendatei.tar.gz       # Ebene 3: löscht - aber Ebene 1 bleibt!
```

Das Image ist danach 1 GB groß, obwohl `ls` nichts mehr zeigt. Alles muss in
**eine** `RUN`-Anweisung:

```dockerfile
RUN wget riesendatei.tar.gz && tar xf riesendatei.tar.gz && rm riesendatei.tar.gz
```

#### ENV – Umgebungsvariablen

```dockerfile
ENV SPEEDNAS_DATA=/data \
    TZ=Europe/Berlin
```

Gelten beim Bauen und später zur Laufzeit, und lassen sich beim Start mit
`-e` überschreiben. Genau deshalb habe ich SpeedNAS beigebracht,
`SPEEDNAS_CONFIG`, `SPEEDNAS_DATA` und `SPEEDNAS_LISTEN` zu lesen: In einem
Container ist das der übliche Weg zu konfigurieren.

> **Nie Passwörter in ENV im Dockerfile.** Sie stehen dann für jeden sichtbar
> in `docker history`. Zur Laufzeit übergeben, per `--env-file` oder Compose.

#### ARG – Variablen nur beim Bauen

```dockerfile
ARG VERSION=docker
RUN go build -ldflags "-X main.version=${VERSION}" ...
```

`ARG` gilt nur während des Baus und landet nicht im fertigen Image. Übergeben
mit `docker build --build-arg VERSION=1.0.0 .`

#### EXPOSE – Dokumentation, sonst nichts

```dockerfile
EXPOSE 8088
```

Häufiges Missverständnis: **`EXPOSE` öffnet keinen Port.** Es ist eine reine
Notiz für Menschen und Werkzeuge. Erreichbar wird ein Port erst durch `-p`
beim Start.

#### USER – nicht als root laufen

```dockerfile
RUN addgroup -g 1000 speednas && adduser -D -u 1000 -G speednas speednas
USER speednas
```

Standardmäßig läuft alles im Container als root. Bricht jemand aus dem
Container aus, hat er root auf dem Wirt. Also: eigenen Benutzer anlegen und
umschalten. Nach `USER` gilt der neue Benutzer für alle folgenden Anweisungen
und für den Start.

#### VOLUME – Hinweis auf veränderliche Daten

```dockerfile
VOLUME ["/data"]
```

Markiert ein Verzeichnis als „hier liegen Daten, die überleben sollen". Wird
beim Start nichts eingebunden, legt Docker automatisch ein anonymes Volume an.
Das rettet dich vor Datenverlust, macht aber unübersichtlich – gib deshalb
beim Start lieber selbst eines an.

#### HEALTHCHECK – lebt das noch?

```dockerfile
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -q -O /dev/null "$HEALTH_URL" || exit 1
```

Docker ruft das regelmäßig auf. Rückgabewert 0 heißt gesund. `docker ps` zeigt
dann `(healthy)` oder `(unhealthy)`, und Compose kann darauf warten, bevor es
abhängige Dienste startet.

`--start-period` ist die Schonfrist beim Start – Fehlschläge darin zählen
nicht als Fehler.

#### ENTRYPOINT und CMD – was beim Start passiert

Das verwirrt am Anfang jeden. Die Regel ist einfach:

- **ENTRYPOINT** = das Programm, das immer läuft
- **CMD** = die Vorgabe-Argumente, die man beim `docker run` überschreiben kann

```dockerfile
ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
CMD ["speednas"]
```

```bash
docker run speednas                      # führt aus: entrypoint.sh speednas
docker run speednas -probe 192.168.2.1   # führt aus: entrypoint.sh -probe 192.168.2.1
docker run --entrypoint sh -it speednas  # Entrypoint umgehen, Shell starten
```

Die letzte Zeile ist ein wichtiger Kniff für die Fehlersuche: Wenn ein
Container beim Start sofort abstürzt, kommst du so trotzdem hinein.

**Klammer- oder Textform?**

```dockerfile
CMD ["speednas", "-listen", ":8088"]     # Exec-Form: empfohlen
CMD speednas -listen :8088               # Shell-Form: läuft über /bin/sh -c
```

Die Exec-Form startet das Programm direkt als Prozess 1. Die Shell-Form
schiebt eine Shell dazwischen, die Signale nicht weitergibt – dein
`docker stop` wartet dann zehn Sekunden und tötet den Prozess hart, statt ihn
sauber beenden zu lassen. **Nimm die Klammern.**

---

## 6. Ebenen und der Cache

Ein Image ist ein Stapel schreibgeschützter Ebenen. Jede Anweisung im
Dockerfile legt eine obendrauf.

```
┌──────────────────────────────┐
│ CMD ["speednas"]             │  Ebene 6  (0 B, nur Metadaten)
│ COPY speednas /usr/local/bin │  Ebene 5  (11 MB)
│ RUN adduser ...              │  Ebene 4  (1 KB)
│ RUN apk add ffmpeg ...       │  Ebene 3  (48 MB)
│ FROM alpine:3.21             │  Ebene 2  (8 MB)
└──────────────────────────────┘
        + Schreibschicht          ← nur im laufenden Container
```

Zwei Folgerungen, die dein Docker-Leben bestimmen:

### Ebenen werden geteilt

Hast du zehn Images auf Basis von `alpine:3.21`, liegt Alpine **einmal** auf
der Platte. Deshalb ist es klug, sich auf wenige Ausgangs-Images festzulegen.

### Der Cache endet bei der ersten Änderung

Beim Bauen prüft Docker für jede Anweisung: „Habe ich das schon mal mit
genau dieser Eingabe gemacht?" Wenn ja, nimmt es das alte Ergebnis. Ändert
sich eine Ebene, werden **alle darüber** neu gebaut.

Deshalb ist die Reihenfolge im Dockerfile keine Geschmacksfrage:

```dockerfile
# SCHLECHT: jede Quelltextänderung wirft den Abhängigkeits-Download weg
COPY . .
RUN go mod download
RUN go build ...

# GUT: der Download bleibt im Cache, solange go.mod unverändert ist
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build ...
```

**Die Regel: was sich selten ändert, kommt nach oben. Der Quelltext ganz
nach unten.**

Bei SpeedNAS macht das den Unterschied zwischen zwei Minuten und zwölf
Sekunden pro Bau.

### Cache-Mounts: noch eine Stufe besser

```dockerfile
RUN --mount=type=cache,target=/root/.cache/go-build \
    go build -o /out/speednas ./cmd/speednas
```

Das hängt ein Cache-Verzeichnis nur für die Dauer dieser einen Anweisung ein.
Es bleibt zwischen Bauvorgängen erhalten, landet aber **nicht** im Image. Der
Go-Compiler kann so auf Zwischenergebnisse zurückgreifen, selbst wenn sich der
Quelltext geändert hat.

Das ist eine BuildKit-Erweiterung; BuildKit ist seit Docker 23 der Standard.

### Ebenen ansehen

```bash
docker history speednas:local
```

Zeigt jede Ebene mit Größe. Wenn ein Image unerwartet groß ist, siehst du hier
sofort, welche Anweisung schuld ist.

---

## 7. Mehrstufige Builds

Jetzt der größte Hebel überhaupt. Das Problem: Zum **Bauen** von SpeedNAS
brauche ich den Go-Compiler (250 MB). Zum **Ausführen** brauche ich ihn nicht.

Ein **mehrstufiger Build** verwendet mehrere `FROM`-Anweisungen in einem
Dockerfile. Nur die letzte Stufe landet im Ergebnis; aus den früheren kannst
du gezielt einzelne Dateien herüberkopieren.

```dockerfile
# ---- Stufe 1: bauen (wird später weggeworfen) ----
FROM golang:1.24-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/speednas ./cmd/speednas

# ---- Stufe 2: das eigentliche Image ----
FROM alpine:3.21
RUN apk add --no-cache ca-certificates ffmpeg
COPY --from=builder /out/speednas /usr/local/bin/speednas
ENTRYPOINT ["speednas"]
```

`COPY --from=builder` ist der Trick: aus der Bau-Stufe kommt genau eine Datei
mit, alles andere bleibt zurück.

**Ergebnis: aus fast 1 GB werden rund 100 MB** – der größte Posten darin ist
ffmpeg. Mit dem `slim`-Ziel ohne ffmpeg sind es etwa 20 MB.

Verlass dich nicht auf meine Zahlen, miss selbst:

```bash
docker build -t speednas:voll .
docker build --target slim -t speednas:slim .
docker images | grep speednas
```

### Warum CGO_ENABLED=0 dazugehört

Go bindet standardmäßig gegen die C-Bibliothek des Bausystems. Ein so
gebautes Programm sucht beim Start nach `libc` – und die Alpine-Variante
(musl) ist eine andere als die von Debian (glibc). Ergebnis:
`no such file or directory`, obwohl die Datei offensichtlich da ist. Eine der
verwirrendsten Fehlermeldungen überhaupt.

`CGO_ENABLED=0` erzeugt ein statisch gebundenes Programm ohne jede solche
Abhängigkeit. Es läuft in jedem Image – sogar in `scratch`, das buchstäblich
leer ist.

### Eine Stufe gezielt bauen

Unser Dockerfile hat zwei End-Stufen. Mit `--target` wählst du aus:

```bash
docker build -t speednas:local .                  # Standard: mit ffmpeg
docker build --target slim -t speednas:slim .     # ohne ffmpeg, ~20 MB
```

Der Unterschied in der Praxis: Ohne ffmpeg gibt es keine Vorschaubilder für
Videos und keine für HEIC-Fotos vom iPhone. Für normale JPEGs und PNGs
brauchst du es nicht.

---

## 8. Daten, die überleben: Volumes

Container sind vergänglich. Daten sollen es nicht sein. Dafür gibt es zwei
Wege, und die Wahl zwischen ihnen ist eine der wenigen wirklich wichtigen
Entscheidungen.

### Benanntes Volume – die Voreinstellung

```bash
docker run -v speednas-data:/data speednas:local
```

Docker verwaltet den Speicherort selbst (unter Linux irgendwo in
`/var/lib/docker/volumes/`). Du sprichst es nur über den Namen an.

```bash
docker volume ls                          # alle Volumes
docker volume inspect speednas-data       # wo liegt es wirklich?
docker volume rm speednas-data            # löschen (Daten weg!)
```

**Vorteile:** Rechte stimmen von allein, funktioniert auf allen Systemen
gleich, ist schnell (auch unter Docker Desktop).
**Nachteil:** Du kommst nicht so bequem mit dem Dateimanager heran.

### Bind Mount – ein Ordner vom Wirt

```bash
docker run -v /home/paul/speednas:/data speednas:local
```

Ein konkretes Verzeichnis deines Rechners wird in den Container gespiegelt.
Änderungen sind sofort auf beiden Seiten sichtbar.

**Vorteile:** Du siehst die Dateien, kannst sie sichern und editieren.
**Nachteile:** Rechteprobleme (siehe nächstes Kapitel), unter Docker Desktop
spürbar langsamer.

Nützliche Zusätze:

```bash
-v /srv/medien:/medien:ro      # :ro = schreibgeschützt
```

### Welches wann?

| Fall | Empfehlung |
|---|---|
| Daten der Anwendung (Konfiguration, Cache) | benanntes Volume |
| Du willst regelmäßig hineinschauen | Bind Mount |
| Quelltext während der Entwicklung | Bind Mount |
| Bestehende Mediensammlung einbinden | Bind Mount, mit `:ro` |

Für SpeedNAS: `/data` als benanntes Volume. Wenn du zusätzlich einen
Medienordner des Wirts anbieten willst, den per Bind Mount dazu – und in der
Oberfläche als Speicherort vom Typ „Lokaler Ordner" eintragen.

### Volumes sichern

Ein benanntes Volume sicherst du, indem du einen Wegwerf-Container darauf
ansetzt:

```bash
# sichern
docker run --rm \
  -v speednas-data:/data:ro \
  -v "$(pwd)":/sicherung \
  alpine tar czf /sicherung/speednas-$(date +%F).tar.gz -C /data .

# zurückspielen
docker run --rm \
  -v speednas-data:/data \
  -v "$(pwd)":/sicherung \
  alpine sh -c "rm -rf /data/* && tar xzf /sicherung/speednas-2026-09-04.tar.gz -C /data"
```

Das Muster ist allgemein nützlich: Ein `alpine`-Container mit zwei Mounts ist
das Schweizer Messer für alles, was man an Volumes tun will.

---

## 9. Die Rechte-Falle

Das ist der Punkt, an dem die meisten Docker-Neulinge eine Stunde verlieren.
Deshalb hier ausführlich.

### Das Problem

Innerhalb des Containers gibt es Benutzer mit Nummern (UID/GID). Auf dem Wirt
auch. **Es sind dieselben Nummern** – der Kernel kennt nur Zahlen, keine
Namen. Der Name `speednas` im Container und `paul` auf dem Wirt sind bloß
Beschriftungen für dieselbe 1000.

Bei einem Bind Mount stößt das aufeinander:

```bash
mkdir daten                          # gehört dir, sagen wir UID 1000
docker run -v ./daten:/data meinimage
# im Container läuft der Dienst als UID 1000 → passt zufällig
```

```bash
sudo mkdir /srv/daten                # gehört root, UID 0
docker run -v /srv/daten:/data meinimage
# Dienst läuft als UID 1000 → permission denied
```

Die Fehlermeldung sagt dann irgendetwas von „read-only file system" oder
„permission denied", und man sucht an der falschen Stelle.

### Die drei Lösungen

**1. Ordner auf dem Wirt passend übereignen** – der direkteste Weg:

```bash
sudo chown -R 1000:1000 /srv/daten
```

**2. Container unter deiner eigenen Kennung starten:**

```bash
docker run --user "$(id -u):$(id -g)" -v /srv/daten:/data meinimage
```

Sauber, aber: Im Image existiert diese UID dann womöglich nicht als
Benutzer. Für die meisten Programme egal, manche stören sich daran.

**3. PUID/PGID im Startskript** – der Weg, den SpeedNAS geht.

Der Container startet als root, das Startskript korrigiert die Rechte auf
`/data` und legt danach die Root-Rechte ab:

```sh
if [ "$(id -u)" = "0" ]; then
    owner="$(stat -c '%u:%g' /data)"
    if [ "$owner" != "${PUID}:${PGID}" ]; then
        chown "${PUID}:${PGID}" /data
    fi
    exec su-exec "${PUID}:${PGID}" "$@"
fi
exec "$@"
```

`su-exec` wechselt den Benutzer und **ersetzt** sich selbst durch das Programm
(`exec`). Wichtig, damit der Dienst Prozess 1 bleibt und Signale bekommt.

Deine eigene Kennung findest du mit:

```bash
id -u    # z. B. 1000
id -g
```

Und trägst sie in die `.env` ein. Das ist genau das Muster, das die
Selfhosting-Welt (linuxserver.io & Co.) durchgängig verwendet – wenn du es
einmal verstanden hast, verstehst du deren Images alle.

> **Warum nicht einfach als root laufen lassen?** Weil ein Ausbruch aus dem
> Container dann root auf dem Wirt bedeutet. Und weil der Container Dateien
> anlegt, die dir hinterher nicht gehören und die du ohne `sudo` nicht mehr
> loswirst.

---

## 10. Netzwerk und Ports

### Ports abbilden

Ein Container hat seine eigene Netzwerkumgebung. Läuft SpeedNAS darin auf
Port 8088, ist das erst mal **nur im Container** so.

```bash
docker run -p 8088:8088 speednas:local
#            ▲     ▲
#            │     └─ Port im Container
#            └─────── Port auf deinem Rechner
```

Die Reihenfolge ist immer `Wirt:Container` – wie bei `cp Quelle Ziel`.

Nützliche Varianten:

```bash
-p 9000:8088              # außen 9000, innen 8088
-p 127.0.0.1:8088:8088    # nur lokal erreichbar, nicht aus dem Netz
-p 8088:8088/udp          # UDP statt TCP
```

Die zweite Variante ist ein gutes Werkzeug: Damit läuft der Dienst hinter
einem Reverse Proxy, ohne dass jemand direkt an ihn herankommt.

### Die vier Netzwerkarten

| Modus | Was passiert | wann |
|---|---|---|
| `bridge` (Standard) | eigenes Netz, Ports müssen abgebildet werden | fast immer |
| `host` | Container teilt das Netz des Wirts, kein `-p` nötig | mDNS, viele Ports |
| `none` | gar kein Netz | Rechenaufgaben ohne Netzbedarf |
| eigenes Netz | wie bridge, aber Container finden sich per Name | Compose |

### Was heißt das für SpeedNAS und den Speedport?

Häufige Sorge: „Kommt der Container überhaupt an meinen Router?"

**Ja, ohne Weiteres.** Ausgehende Verbindungen funktionieren im Bridge-Modus
immer – der Container darf ins Netz hinaus wie jedes andere Programm.
SpeedNAS baut die SMB-Verbindung zu `192.168.2.1:445` selbst auf, also passt
das.

`network_mode: host` brauchst du nur, wenn:

- SpeedNAS Geräte im Netz **finden** soll (mDNS/NetBIOS-Rundrufe kommen nicht
  über die Bridge) – für SpeedNAS irrelevant, dort gibst du die IP direkt an
- du dir das Port-Abbilden sparen willst

Unter Docker Desktop (Windows/Mac) funktioniert `host` nur eingeschränkt, weil
dort ohnehin eine VM dazwischenliegt. Unter Linux und auf dem Raspberry Pi
funktioniert es normal.

### Container untereinander

Im selben benutzerdefinierten Netz erreichen Container sich über ihren
Dienstnamen:

```yaml
services:
  app:
    # kann die Datenbank unter dem Namen "db" erreichen
  db:
    image: postgres:16
```

Aus `app` heraus ist die Datenbank schlicht `db:5432`. Docker betreibt dafür
einen internen DNS. **Das ist der Hauptgrund, Compose zu benutzen**, sobald
mehr als ein Container im Spiel ist.

Wichtig: Innerhalb eines Containers ist `localhost` **der Container selbst**,
nicht der Wirt. Willst du vom Container aus an einen Dienst auf dem Wirt,
nimm `host.docker.internal` (Docker Desktop) bzw. die Bridge-IP unter Linux.

---

## 11. Docker Compose

Sobald ein `docker run` mehr als zwei Zeilen hat, willst du das nicht mehr
tippen. **Compose** schreibt genau dieselben Angaben in eine Datei.

Statt:

```bash
docker run -d --name speednas --restart unless-stopped \
  -p 8088:8088 -v speednas-data:/data \
  -e TZ=Europe/Berlin -e PUID=1000 -e PGID=1000 \
  --security-opt no-new-privileges:true \
  speednas:local
```

schreibst du `docker-compose.yml`:

```yaml
name: speednas

services:
  speednas:
    build:
      context: .
      target: runtime
    image: speednas:local
    container_name: speednas
    restart: unless-stopped
    ports:
      - "8088:8088"
    volumes:
      - speednas-data:/data
    environment:
      TZ: Europe/Berlin
      PUID: 1000
      PGID: 1000
    security_opt:
      - no-new-privileges:true

volumes:
  speednas-data:
```

und startest mit:

```bash
docker compose up -d
```

### Die Compose-Befehle

```bash
docker compose up -d          # bauen (falls nötig) und starten
docker compose up -d --build  # immer neu bauen
docker compose down           # anhalten und entfernen (Volumes bleiben!)
docker compose down -v        # ... samt Volumes - VORSICHT, Daten weg
docker compose ps             # Zustand
docker compose logs -f        # mitlesen
docker compose logs -f speednas   # nur ein Dienst
docker compose exec speednas sh   # hineinsteigen
docker compose restart
docker compose pull           # neuere Images holen
docker compose config         # Datei prüfen und aufgelöst anzeigen
```

`docker compose config` ist praktisch: Es zeigt, was Compose aus deiner Datei
wirklich macht, inklusive eingesetzter Variablen. Bei Tippfehlern in der
Einrückung meckert es sofort.

### Die .env-Datei

Neben der Compose-Datei liegt optional eine `.env`. Compose liest sie
automatisch:

```
NAS_PASSWORT=meinGeheimnis
PUID=1000
PGID=1000
```

Und in der Compose-Datei:

```yaml
environment:
  NAS_PASSWORT: ${NAS_PASSWORT:-}
```

`${VAR:-vorgabe}` bedeutet „nimm VAR, sonst die Vorgabe".

**Die `.env` gehört in `.gitignore`.** Deshalb liegt hier nur eine
`.env.example` als Vorlage.

Der Bogen zu SpeedNAS: In der Konfiguration kannst du als Passwort wörtlich
`${NAS_PASSWORT}` eintragen. SpeedNAS setzt zur Laufzeit den echten Wert ein.
Damit steht das Passwort weder in der Konfigurationsdatei noch im Image –
nur in der `.env`, die nie irgendwo mitwandert.

### Abhängigkeiten

```yaml
services:
  app:
    depends_on:
      db:
        condition: service_healthy
  db:
    image: postgres:16
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U postgres"]
      interval: 5s
      retries: 10
```

`depends_on` allein wartet nur, bis der Container *gestartet* ist – nicht, bis
der Dienst *bereit* ist. Erst zusammen mit `condition: service_healthy` und
einem Healthcheck wird daraus etwas Verlässliches. Ein Klassiker unter den
Stolpersteinen.

---

## 12. Fehlersuche

Das ist die Fähigkeit, die einen Docker-Profi ausmacht. Vier Situationen
decken fast alles ab.

### „Der Container startet und ist sofort wieder weg"

```bash
docker ps -a          # Status ansehen: "Exited (1) 5 seconds ago"
docker logs speednas  # was hat er gesagt?
```

Der Rückgabewert hilft weiter:

| Code | Bedeutung |
|---|---|
| 0 | sauber beendet – meist: das Programm läuft nicht dauerhaft |
| 1 | Fehler im Programm, Meldung steht im Log |
| 125 | Docker selbst konnte nicht starten (falscher Schalter) |
| 126 | Datei nicht ausführbar (`chmod +x` vergessen) |
| 127 | Befehl nicht gefunden (Tippfehler, oder fehlt im Image) |
| 137 | hart getötet – meist Speicher voll (OOM) |
| 143 | durch SIGTERM beendet, also normales `docker stop` |

**Der wichtigste Kniff**, wenn das Log leer bleibt: Entrypoint umgehen und
selbst nachsehen.

```bash
docker run --rm -it --entrypoint sh speednas:local
# jetzt bist du drin und kannst prüfen:
ls -la /usr/local/bin/
/usr/local/bin/speednas -version
ls -la /data
```

### „Ich komme nicht auf den Dienst"

Von innen nach außen prüfen:

```bash
# 1. Läuft er überhaupt und lauscht er?
docker exec speednas wget -qO- http://127.0.0.1:8088/health

# 2. Ist der Port abgebildet?
docker port speednas
# 8088/tcp -> 0.0.0.0:8088

# 3. Vom Wirt aus?
curl http://localhost:8088/health
```

Der mit Abstand häufigste Fehler: Der Dienst lauscht im Container auf
`127.0.0.1` statt auf `0.0.0.0`. Dann ist er nur *innerhalb* des Containers
erreichbar, und `-p` läuft ins Leere. SpeedNAS setzt deshalb im Image
`SPEEDNAS_LISTEN=:8088` – der leere Teil vor dem Doppelpunkt heißt „auf allen
Adressen".

### „Das Image ist riesig"

```bash
docker images                 # wie groß?
docker history speednas:local # welche Ebene ist schuld?
```

Übliche Ursachen: mehrstufigen Bau vergessen, Paket-Cache nicht entfernt,
`.dockerignore` fehlt, Dateien in einer späteren Ebene gelöscht.

### „Meine Änderung kommt nicht an"

```bash
docker compose up -d --build          # neu bauen erzwingen
docker build --no-cache -t x .        # kompletter Neubau ohne Cache
```

Wenn du im Container etwas änderst (`docker exec`), ist das nach dem nächsten
`docker rm` weg – das ist kein Fehler, sondern Absicht. Änderungen gehören ins
Dockerfile.

### Nützlich für Fortgeschrittene

```bash
docker stats                         # was frisst CPU und RAM?
docker diff speednas                 # was hat der Container am Dateisystem geändert?
docker cp speednas:/data/config.json .   # Datei herausholen
docker cp ./config.json speednas:/data/  # Datei hineinlegen
docker events                        # was passiert gerade im Daemon?
docker inspect -f '{{.State.Health.Status}}' speednas
```

`docker diff` ist unterschätzt: Es zeigt genau, welche Dateien ein Container
gegenüber seinem Image angelegt, geändert oder gelöscht hat.

---

## 13. Für den Raspberry Pi bauen

Ein auf deinem PC (x86-64) gebautes Image läuft **nicht** auf einem Raspberry
Pi (ARM). Die Fehlermeldung lautet dann `exec format error`.

Drei Wege:

### Direkt auf dem Pi bauen

Der einfachste. Dauert nur länger.

```bash
git clone https://github.com/PaulWeber-co/FileBrowserTest.git
cd FileBrowserTest
docker compose up -d --build
```

### Mit buildx für mehrere Architekturen

`buildx` ist Dockers erweiterter Bauer und heute eingebaut.

```bash
# einmalig einen Bauer anlegen, der mehrere Architekturen kann
docker buildx create --name mehrarch --use --bootstrap

# für zwei Architekturen bauen und direkt in eine Registry schieben
docker buildx build \
  --platform linux/amd64,linux/arm64 \
  -t ghcr.io/paulweber-co/speednas:1.0.0 \
  --push .
```

Das Ergebnis ist eine *Manifest-Liste*: ein Imagename, hinter dem mehrere
Varianten stecken. Docker holt auf jedem Rechner automatisch die passende.

> Warum `--push`? Der klassische lokale Image-Speicher kann pro Name nur eine
> Architektur halten. Neuere Docker-Versionen mit dem containerd-Speicher
> können auch mehrere per `--load` ablegen; ist der nicht aktiv, scheitert
> `--load` bei mehreren Plattformen. Der Weg über eine Registry funktioniert
> immer.

Unser Dockerfile ist dafür schon vorbereitet:

```dockerfile
FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS builder
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build ...
```

`--platform=$BUILDPLATFORM` sorgt dafür, dass der Compiler **nativ** auf
deinem schnellen Rechner läuft und Go für die Zielarchitektur übersetzt.
Ohne das würde Docker die ganze Bau-Stufe unter QEMU emulieren – zehnmal
langsamer.

Welche Werte gibt es? `linux/amd64` (normale PCs), `linux/arm64` (Pi 4/5,
Apple Silicon), `linux/arm/v7` (ältere Pi).

---

## 14. Images veröffentlichen

### GitHub Container Registry

Da SpeedNAS ohnehin auf GitHub liegt, ist `ghcr.io` naheliegend.

```bash
# Anmelden mit einem Personal Access Token (Rechte: write:packages)
echo "$GITHUB_TOKEN" | docker login ghcr.io -u PaulWeber-co --password-stdin

docker build -t ghcr.io/paulweber-co/speednas:1.0.0 .
docker tag ghcr.io/paulweber-co/speednas:1.0.0 ghcr.io/paulweber-co/speednas:latest
docker push ghcr.io/paulweber-co/speednas:1.0.0
docker push ghcr.io/paulweber-co/speednas:latest
```

### Automatisch bauen lassen

Eine GitHub-Action, die bei jedem Tag ein Multi-Arch-Image baut:

```yaml
# .github/workflows/docker.yml
name: Docker
on:
  push:
    tags: ["v*"]

jobs:
  build:
    runs-on: ubuntu-latest
    permissions:
      contents: read
      packages: write
    steps:
      - uses: actions/checkout@v4
      - uses: docker/setup-qemu-action@v3
      - uses: docker/setup-buildx-action@v3
      - uses: docker/login-action@v3
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}
      - uses: docker/build-push-action@v6
        with:
          platforms: linux/amd64,linux/arm64
          push: true
          tags: |
            ghcr.io/${{ github.repository_owner }}/speednas:${{ github.ref_name }}
            ghcr.io/${{ github.repository_owner }}/speednas:latest
          cache-from: type=gha
          cache-to: type=gha,mode=max
          build-args: VERSION=${{ github.ref_name }}
```

`cache-from`/`cache-to` mit `type=gha` legt den Ebenen-Cache in GitHubs
Cache-Speicher – der zweite Bau dauert dann Sekunden.

### Tags richtig vergeben

```
speednas:1.0.0     genau diese Version - so gehört es in die Produktion
speednas:1.0       neueste Fehlerbehebung von 1.0
speednas:1         neueste 1.x
speednas:latest    was auch immer zuletzt gepusht wurde
```

`latest` ist bequem und trügerisch. Für alles Ernsthafte: feste Version.

---

## 15. Gute Gewohnheiten

**Beim Bauen**

- Ausgangs-Image immer mit Version festnageln
- Mehrstufig bauen, sobald ein Compiler im Spiel ist
- `.dockerignore` von Anfang an anlegen
- Selten Veränderliches nach oben, Quelltext nach unten
- Ein Container, ein Dienst – nicht Datenbank und Webserver in einen
- Keine Geheimnisse ins Image (`docker history` zeigt alles)

**Beim Betreiben**

- Nicht als root laufen lassen
- `--restart unless-stopped` für Dienste, die immer laufen sollen
- Speicher begrenzen, damit ein Fehler nicht den Rechner lahmlegt
- Protokolle begrenzen (`max-size`, `max-file`)
- Healthcheck einbauen
- `no-new-privileges:true` setzen
- Volumes sichern – Docker macht das nicht für dich

**Sicherheit**

```bash
docker scout cves speednas:local     # bekannte Schwachstellen (Docker Desktop)
```

Und die Grundregeln: `--privileged` niemals ohne sehr guten Grund. Den
Docker-Socket (`/var/run/docker.sock`) nicht in Container hängen – wer ihn
hat, kontrolliert den Wirt. Nur nötige Ports abbilden. Basis-Images regelmäßig
aktualisieren.

**Wenn du unsicher bist**, ob etwas ins Image gehört: Frage dich, ob es sich
zwischen zwei Starts ändern darf. Wenn ja, gehört es in ein Volume oder in
eine Umgebungsvariable, nicht ins Image.

---

## 16. Übungen

Zum tatsächlichen Lernen. Der Reihe nach.

**1 – Aufwärmen.** Starte einen Alpine-Container, installiere darin `curl`,
verlasse ihn, starte einen neuen. Ist `curl` noch da? Warum nicht?

**2 – Erstes eigenes Image.** Schreibe ein Dockerfile, das auf Alpine `curl`
installiert und beim Start `curl -s https://example.com` ausführt. Baue es
und starte es.

**3 – Größe messen.** Baue SpeedNAS einmal naiv (nur `FROM golang:1.24`, ohne
zweite Stufe) und einmal mit unserem Dockerfile. Vergleiche
`docker images`. Sieh dir mit `docker history` an, wo der Unterschied
herkommt.

**4 – Den Cache verstehen.** Baue SpeedNAS. Ändere eine Zeile in
`README.md`, baue erneut – wie lange dauert es? Ändere eine Zeile in
`cmd/speednas/main.go`, baue erneut. Erkläre den Unterschied.

**5 – Daten verlieren und retten.** Starte SpeedNAS **ohne** Volume, lege
einen Benutzer an, lösche den Container, starte neu. Weg? Jetzt dasselbe mit
`-v speednas-data:/data`.

**6 – Die Rechte-Falle nachstellen.** Lege ein Verzeichnis an, das root
gehört, binde es als `/data` ein und starte den Container mit
`--user 1000:1000`. Beobachte den Fehler. Repariere ihn auf zwei
verschiedene Arten.

**7 – Ports.** Starte SpeedNAS so, dass es auf dem Wirt unter Port 9000
erreichbar ist. Dann so, dass es *nur* von deinem Rechner selbst erreichbar
ist.

**8 – Fehlersuche.** Baue absichtlich ein kaputtes Image (z. B. Tippfehler im
ENTRYPOINT). Finde den Fehler nur mit `docker ps -a`, `docker logs` und
`--entrypoint sh`.

**9 – Compose.** Übersetze deinen längsten `docker run`-Befehl in eine
Compose-Datei. Prüfe mit `docker compose config`.

**10 – Für den Pi.** Baue ein Multi-Arch-Image mit buildx und schiebe es nach
ghcr.io. Zieh es auf einem Pi und starte es.

---

## 17. Spickzettel

```bash
# ---------- Bauen ----------
docker build -t name:tag .                    # bauen
docker build --target slim -t name:slim .     # bestimmte Stufe
docker build --no-cache -t name .             # ohne Cache
docker build --build-arg VERSION=1.0.0 .      # Bau-Variable
docker history name:tag                       # Ebenen ansehen

# ---------- Starten ----------
docker run -d --name x -p 8088:8088 -v vol:/data image
docker run -it --rm image sh                  # kurz reinschauen
docker run --rm -it --entrypoint sh image     # Entrypoint umgehen
docker run --env-file .env image              # Variablen aus Datei

# ---------- Beobachten ----------
docker ps / docker ps -a                      # was läuft / lief
docker logs -f --tail 100 x                   # Ausgaben
docker exec -it x sh                          # hineinsteigen
docker stats                                  # Verbrauch
docker inspect x                              # alle Details
docker diff x                                 # geänderte Dateien
docker port x                                 # abgebildete Ports

# ---------- Aufräumen ----------
docker stop x && docker rm x
docker rm -f x                                # in einem Rutsch
docker image prune -a                         # ungenutzte Images
docker system df                              # Platzverbrauch
docker system prune -a                        # großes Aufräumen

# ---------- Volumes ----------
docker volume ls / inspect / rm
docker run --rm -v vol:/d -v "$PWD":/b alpine tar czf /b/sich.tgz -C /d .

# ---------- Compose ----------
docker compose up -d [--build]
docker compose down [-v]                      # -v löscht Volumes!
docker compose logs -f [dienst]
docker compose exec dienst sh
docker compose ps / config / pull / restart

# ---------- Mehrere Architekturen ----------
docker buildx create --name m --use --bootstrap
docker buildx build --platform linux/amd64,linux/arm64 -t reg/name:tag --push .
```

### Und für SpeedNAS konkret

```bash
docker compose up -d                          # starten
docker compose logs -f                        # zusehen
docker compose exec speednas speednas -probe 192.168.2.1   # SMB prüfen
docker compose exec speednas speednas -add-user paul       # Benutzer anlegen
docker compose down                           # anhalten
docker compose up -d --build                  # nach Codeänderung neu
```

---

## Wie es weitergeht

Wenn dir das hier vertraut ist, sind die nächsten sinnvollen Schritte:

- **Reverse Proxy**: [Caddy](https://caddyserver.com/) oder Traefik davor,
  dann hast du HTTPS mit echtem Zertifikat und mehrere Dienste unter einem Port.
- **Watchtower**: aktualisiert laufende Container automatisch auf neue Images.
- **Podman**: Container ohne Daemon und ohne root, weitgehend befehlsgleich.
- **Kubernetes**: für viele Rechner. Für ein Heimnetz Überdimensionierung –
  aber die Begriffe (Pod, Volume, Service) kennst du jetzt schon im Kern.

Und: Wirf gelegentlich alles weg (`docker system prune -a`) und baue neu. Wenn
das ohne Nachdenken funktioniert, ist deine Einrichtung wirklich
reproduzierbar – und genau darum geht es bei Containern.
