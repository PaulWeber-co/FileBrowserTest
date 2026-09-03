# SpeedNAS

Ein Dateibrowser für Netzwerkspeicher, der als kleiner Server im eigenen Netz
läuft und seine Oberfläche im Browser bereitstellt. Damit kommt **jedes** Gerät
an die Freigabe am Router – Windows, Mac, Linux, iPhone und Android – ohne dass
irgendwo eine App installiert oder gekauft werden muss.

Entstanden ist er für einen konkreten Fall: eine USB-Platte am **Speedport**,
die sich mit dem Windows-Explorer und der iOS-Dateien-App nicht vernünftig
ansprechen lässt.

```
   iPhone ─┐
   iPad   ─┼─►  SpeedNAS (läuft auf dem PC)  ──►  Speedport  ──►  USB-Speicher
   PC     ─┘         HTTP/HTTPS                      SMB
```

## Was drin ist

**Protokolle** — SMB 2/3 (Router, NAS, Windows-Freigaben), FTP und FTPS, SFTP
über SSH, WebDAV (Nextcloud & Co.) sowie lokale Ordner. Mehrere Speicherorte
gleichzeitig, Dateien lassen sich zwischen ihnen kopieren und verschieben.

**Bedienung** — Listen- und Kachelansicht, Vorschaubilder, Mehrfachauswahl,
Ausschneiden/Kopieren/Einfügen, Umbenennen, Löschen, Ordner anlegen,
rekursive Suche mit laufender Trefferanzeige, Lesezeichen, zuletzt besuchte
Ordner, Sortierung mit natürlicher Zahlenreihenfolge (Bild2 vor Bild10),
Hell/Dunkel, vollständige Tastaturbedienung, Kontextmenüs, Drag & Drop.

**Dateien ansehen** — Bilder mit Blättern und Zoom, Videos und Musik direkt im
Browser (mit Vorspulen), PDF, Text und Quelltext mit eingebautem Editor.

**Übertragen** — Hochladen per Drag & Drop, auch ganze Ordnerbäume. Große
Dateien gehen in Stücken und lassen sich nach einem Abbruch fortsetzen.
Herunterladen einzeln oder als ZIP, das im Vorbeigehen gepackt wird.
Kopier- und Verschiebevorgänge laufen im Hintergrund mit Fortschrittsanzeige.

**Teilen** — Geheime Links auf Dateien oder Ordner, wahlweise mit Ablaufdatum
und Passwort.

**Diagnose** — Ein eingebautes Werkzeug fragt den Router direkt, welche
SMB-Version er spricht, und ein Geschwindigkeitstest misst den echten
Durchsatz. Beides beantwortet die Frage „warum ist das so langsam / warum
geht das nicht" mit Zahlen statt mit Raten.

**Betrieb** — Ein einzelnes Programm ohne Installation und ohne Datenbank.
Anmeldung mit Benutzern und Passwörtern, optional HTTPS, Nur-Lese-Zugänge.

## Schnellstart unter Windows

1. `speednas.exe` in einen Ordner legen, z. B. `C:\SpeedNAS\`.
2. Doppelklick. Beim ersten Start entsteht eine Konfigurationsdatei, und im
   Fenster steht, unter welchen Adressen der Dienst erreichbar ist:

   ```
   SpeedNAS 1.0.0 läuft
     lokal:       http://localhost:8088
     im Netzwerk: http://192.168.2.105:8088
   ```

3. `http://localhost:8088` im Browser öffnen und den ersten Zugang anlegen.
4. **Standort hinzufügen** → Typ *SMB*, Adresse `192.168.2.1` eintragen,
   auf **Freigaben suchen** klicken und die gefundene Freigabe übernehmen.
5. **Verbindung testen** → sollte grün werden. Speichern, fertig.

Läuft nichts? Dann zuerst in **Einstellungen → Diagnose** die Adresse des
Routers prüfen lassen. Das Ergebnis sagt im Klartext, woran es liegt.

## Vom iPhone aus

SpeedNAS läuft auf dem PC; das iPhone braucht nur einen Browser.

1. In Safari `http://192.168.2.105:8088` öffnen (die Adresse aus dem
   Startfenster, nicht `localhost`).
2. Anmelden.
3. Teilen-Symbol → **Zum Home-Bildschirm**.

Danach liegt SpeedNAS als App-Symbol auf dem Homescreen und startet im
Vollbild. Videos lassen sich abspielen und vorspulen, Fotos aus der Mediathek
hochladen, Dateien in die iOS-Dateien-App sichern.

Details und die Einrichtung für Android stehen in
[docs/iphone.md](docs/iphone.md).

## Warum der Explorer und die Dateien-App streiken

Fast immer wegen der Protokollversion. Viele Router bieten die USB-Freigabe ab
Werk nur über **SMB1** an. Microsoft hat SMB1 aus Windows entfernt, Apple aus
iOS – beide wegen gravierender Sicherheitslücken. Genau dieselbe Freigabe war
vor ein paar Jahren noch problemlos erreichbar und ist es heute nicht mehr.

Die Diagnose in SpeedNAS zeigt das in einer Zeile:

```
$ speednas -probe 192.168.2.1

  TCP-Antwortzeit    0.8 ms
  SMB2/3             ja - SMB 2.1
  Signierung         aktiv=true erzwungen=false
  max. Leseblock     64 KiB
  SMB1               ja - NT LM 0.12
```

**Wichtig und ehrlich:** SpeedNAS spricht ebenfalls kein SMB1. Wenn der Router
*ausschließlich* SMB1 kann, hilft nur, im Router SMB2 zu aktivieren bzw. die
Firmware zu aktualisieren – oder den Speicher stattdessen per **FTP**
einzubinden, das SpeedNAS ebenfalls beherrscht und das von der SMB-Version
unabhängig ist.

Mehr dazu in [docs/speedport.md](docs/speedport.md).

## Tempo, VPN und USB 2.0

Der eigentliche Trick steckt in der Architektur: Über VPN spricht das Handy
**HTTP mit SpeedNAS**, und nur SpeedNAS spricht SMB mit dem Router. SMB ist
ein sehr gesprächiges Protokoll und reagiert äußerst empfindlich auf Latenz –
ein Verzeichniswechsel kostet ein Dutzend Hin und Her. Über eine VPN-Strecke
mit 40 ms wird daraus eine spürbare Gedenksekunde pro Klick. HTTP dagegen
kommt mit einer Anfrage pro Aktion aus.

Genau deshalb fühlt sich ein direkt über VPN eingebundenes Netzlaufwerk zäh an,
während dieselbe Freigabe über SpeedNAS flüssig bleibt.

Dazu kommt paralleles Vorauslesen: statt einer Leseanfrage nach der anderen
stellt SpeedNAS mehrere gleichzeitig und hält die Leitung damit gefüllt. Bei
40 ms Antwortzeit und 64-KiB-Blöcken liegt eine einzelne Leseanfrage
rechnerisch bei rund 1,6 MB/s – egal wie schnell die Leitung wirklich ist.
Vier parallele Anfragen heben diese Grenze entsprechend an.

Wie viel das im konkreten Fall bringt, misst der Geschwindigkeitstest unter
**Einstellungen → Diagnose**. Alle Stellschrauben und was realistisch drin ist,
stehen in [docs/performance.md](docs/performance.md).

## Bauen

Vorausgesetzt wird Go 1.24 oder neuer.

```bash
git clone https://github.com/PaulWeber-co/FileBrowserTest.git
cd FileBrowserTest
go build -o speednas ./cmd/speednas
```

Windows-Programm von Linux oder Mac aus erzeugen:

```bash
./scripts/build.sh          # baut für Windows, Linux und macOS nach dist/
```

Die Weboberfläche liegt im Programm eingebettet – es entsteht genau eine Datei
ohne weitere Abhängigkeiten.

## Kommandozeile

```
speednas                          Server starten (Standard: Port 8088)
speednas -add-user paul           Benutzer anlegen bzw. Passwort setzen
speednas -probe 192.168.2.1       Prüfen, welche SMB-Version der Router spricht
speednas -tls -listen :8443       Mit HTTPS starten
speednas -open                    Starten und Browser öffnen
speednas -config pfad.json        Andere Konfigurationsdatei verwenden
```

## Weitere Dokumentation

| Datei | Inhalt |
|---|---|
| [docs/speedport.md](docs/speedport.md) | Freigabe am Router einrichten, Fehlersuche, FTP als Ausweg |
| [docs/iphone.md](docs/iphone.md) | iPhone, iPad und Android als App einrichten |
| [docs/performance.md](docs/performance.md) | VPN, USB 2.0, alle Stellschrauben, realistische Werte |
| [docs/konfiguration.md](docs/konfiguration.md) | Alle Einstellungen der Konfigurationsdatei |
| [docs/betrieb.md](docs/betrieb.md) | Autostart, HTTPS, Sicherheit, Fernzugriff |
| [docs/entwicklung.md](docs/entwicklung.md) | Aufbau des Quelltexts, Tests |

## Sicherheitshinweise in Kürze

- Die Konfigurationsdatei enthält die Zugangsdaten des Netzwerkspeichers im
  Klartext und wird deshalb nur für den eigenen Benutzer lesbar angelegt.
  Wer das vermeiden will, hinterlegt `${UMGEBUNGSVARIABLE}` statt des
  Passworts.
- SpeedNAS ist für das **eigene Netz** gedacht. Den Port nicht ohne Not im
  Router freigeben – für unterwegs ist ein VPN der richtige Weg. Warum, steht
  in [docs/betrieb.md](docs/betrieb.md).
- Freigabelinks sind bewusst ohne Anmeldung erreichbar. Wer sie weitergibt,
  gibt den Inhalt weiter.

## Lizenz

MIT – siehe [LICENSE](LICENSE).
