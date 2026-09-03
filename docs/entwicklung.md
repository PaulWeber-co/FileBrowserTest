# Aufbau und Entwicklung

## Überblick

```
cmd/speednas/          Programmeinstieg, Kommandozeile
internal/
  config/              Konfiguration lesen, schreiben, prüfen
  vfs/                 Dateisystem-Abstraktion über alle Protokolle
    vfs.go             Schnittstellen, Pfadregeln
    pool.go            Verbindungspool
    client.go          gepoolte Sicht mit automatischem Neuversuch
    prefetch.go        paralleles Vorauslesen
    cache.go           Verzeichnis-Cache
    smb.go ftp.go sftp.go webdav.go local.go
  smbprobe/            rohe SMB-Aushandlung für die Diagnose
  thumb/               Vorschaubilder, EXIF, Dateiklassen
  server/              HTTP-Server und gesamte API
web/                   Oberfläche (eingebettet per go:embed)
tools/genicons/        erzeugt die App-Symbole
```

## Die zentrale Abstraktion

`vfs.Conn` ist eine **einzelne physische Verbindung**. Implementierungen dürfen
davon ausgehen, dass jeweils nur eine Goroutine darauf arbeitet – der Pool
stellt das sicher. Das ist nötig, weil eine FTP-Kontrollverbindung nicht
nebenläufig nutzbar ist.

`vfs.Client` ist die Sicht nach außen. Jede Operation leiht sich kurz eine
Verbindung; Datenströme behalten ihre Verbindung bis zum `Close`.

Zwei Eigenschaften sind wichtig und leicht zu übersehen:

1. **Neuversuch bei wiederverwendeten Verbindungen.** Scheitert eine
   Verbindung aus dem Pool mit einem Transportfehler, wird genau einmal mit
   einer frisch aufgebauten wiederholt. Router werfen inaktive SMB-Sitzungen
   kommentarlos weg; ohne diesen Neuversuch scheitert die erste Aktion nach
   jeder Pause. Bei einer *frisch* aufgebauten Verbindung wird nicht
   wiederholt – dort ist der Fehler echt.

2. **Schreiben wird nicht wiederholt**, weil sich der Datenstrom nicht
   zurückspulen lässt. Stattdessen holt sich ein Schreibvorgang ab einer
   gewissen Größe von vornherein eine frische Verbindung.

## Pfade

Intern sind Pfade immer: mit `/` getrennt, ohne führenden Slash, `""` ist die
Wurzel. `vfs.Clean` normalisiert und entfernt dabei jedes `..` – ein Ausbruch
oberhalb der Freigabewurzel ist damit strukturell unmöglich, nicht durch eine
Prüfung an einer Stelle.

## Oberfläche

Reines JavaScript ohne Framework und ohne Bauschritt. Die Module:

| Datei | Aufgabe |
|---|---|
| `util.js` | DOM-Helfer, Symbole, Formatierung |
| `api.js` | fetch-Hülle, Fehler, SSE, Upload mit Fortschritt |
| `ui.js` | Meldungen, Dialoge, Kontextmenüs, Formularbausteine |
| `app.js` | Zustand, Navigation, Liste, Auswahl, Aktionen |
| `upload.js` | Warteschlange, Stückelung, Wiederaufnahme |
| `viewer.js` | Vollbildansicht |
| `settings.js` | Verwaltung und Diagnose |

Während der Entwicklung lohnt `-dev-web`, dann werden die Dateien von der
Platte statt aus dem Programm gelesen:

```bash
go run ./cmd/speednas -dev-web ./web -config /tmp/dev.json
```

Ein Neuladen im Browser genügt dann für Änderungen an CSS und JavaScript.

## Tests

```bash
go test ./...              # alles
go test -race ./...        # mit Race-Detector
go test ./internal/vfs/ -run Prefetch -v
```

Abgedeckt sind unter anderem:

- Pfadnormalisierung und Ausbruchversuche
- natürliche Sortierung
- paralleles Vorauslesen: Byte-Genauigkeit über viele Größen, Offsets und
  Arbeiterzahlen, Fehlerweitergabe, Freigabe der Quelle
- Verbindungspool: Wiederverwendung, Verwerfen kaputter Verbindungen,
  Neuversuch genau einmal
- Range-Auswertung inklusive Sonderfällen
- Sicherheit der Freigabelinks (kein Ausbruch aus dem geteilten Unterbaum)
- Lückenberechnung bei wiederaufnehmbaren Uploads
- SMB-Aushandlung auf Byte-Ebene gegen einen nachgebauten Server
- EXIF-Ausrichtung, Vorschau-Cache
- vollständige HTTP-Integrationstests: Anmeldung, CSRF, Nur-Lesen,
  Dateirundlauf, ZIP, Stück-Upload mit Wiederaufnahme, Freigabelinks

## Symbole neu erzeugen

```bash
go run ./tools/genicons
```

Die Symbole werden gerechnet, nicht gezeichnet – so liegen keine
undurchsichtigen Binärdateien im Repository, und jede Größe ist gleich scharf.

## Einen Protokolltreiber ergänzen

1. `vfs.Conn` implementieren (Vorlage: `local.go`, kürzeste Umsetzung).
2. Eine `NewXxx`-Funktion schreiben, die Pool und `Caps` zusammensetzt.
3. In `config.Location` einen Block und in `Location.Validate`,
   `Resolve` und `Redacted` die passenden Zweige ergänzen.
4. In `server/manager.go` den Fall in `build` aufnehmen.
5. In `web/js/settings.js` ein Formular hinzufügen.

`Caps` steuert, was die Oberfläche anbietet – nicht unterstützte Aktionen
werden gar nicht erst angezeigt, statt mit einem Fehler zu quittieren.
