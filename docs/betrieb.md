# Betrieb: Autostart, HTTPS, Sicherheit

## Automatisch mitstarten

### Windows – Aufgabenplanung (empfohlen)

Startet SpeedNAS beim Anmelden und ohne sichtbares Fenster.

1. `Aufgabenplanung` öffnen → **Aufgabe erstellen…**
2. *Allgemein*: Name `SpeedNAS`, **Mit höchsten Privilegien ausführen** bleibt
   aus (wird nicht gebraucht).
3. *Trigger*: **Bei Anmeldung**.
4. *Aktionen*: Programm `C:\SpeedNAS\speednas.exe`,
   Argumente `-listen :8088`, „Starten in" `C:\SpeedNAS`.
5. *Bedingungen*: Häkchen bei „Nur starten, wenn Netzverbindung besteht"
   entfernen – sonst startet es bei WLAN-Verzögerung nicht.

Alternativ und schneller eingerichtet: eine Verknüpfung zur `.exe` in den
Autostart-Ordner legen (`Win+R`, `shell:startup`).

### Windows – als echter Dienst

Damit SpeedNAS auch ohne angemeldeten Benutzer läuft, eignet sich
[NSSM](https://nssm.cc/):

```
nssm install SpeedNAS C:\SpeedNAS\speednas.exe
nssm set SpeedNAS AppDirectory C:\SpeedNAS
nssm start SpeedNAS
```

Achtung: Ein Dienst läuft unter einem anderen Konto und hat damit ein anderes
`%APPDATA%`. Konfiguration und Datenverzeichnis deshalb ausdrücklich angeben:

```
nssm set SpeedNAS AppParameters "-config C:\SpeedNAS\config.json -data C:\SpeedNAS\daten"
```

### Linux – systemd

```ini
# /etc/systemd/system/speednas.service
[Unit]
Description=SpeedNAS
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=paul
ExecStart=/usr/local/bin/speednas -config /home/paul/.config/speednas/config.json
Restart=on-failure
RestartSec=5
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=read-only
ReadWritePaths=/home/paul/.config/speednas

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl enable --now speednas
journalctl -u speednas -f
```

### macOS – launchd

`~/Library/LaunchAgents/de.speednas.plist` anlegen und mit
`launchctl load ~/Library/LaunchAgents/de.speednas.plist` aktivieren.

## HTTPS

Ohne HTTPS läuft alles im Klartext durchs Netz – im eigenen WLAN vertretbar,
über VPN ohnehin doppelt verschlüsselt, aber schöner ist es mit.

**Selbstsigniert (ein Schalter):**

```
speednas -tls
```

Beim ersten Start entsteht ein Zertifikat unter `tls/` im Datenverzeichnis,
gültig für `localhost`, `127.0.0.1` und alle IP-Adressen des Rechners. Der
Browser warnt einmal, danach ist Ruhe. Am iPhone lässt sich das Zertifikat
unter *Einstellungen → Allgemein → Info → Zertifikatsvertrauenseinstellungen*
dauerhaft akzeptieren.

**Eigenes Zertifikat:**

```
speednas -cert C:\certs\fullchain.pem -key C:\certs\privkey.pem
```

**Hinter einem Reverse Proxy** (Caddy, nginx, Traefik): SpeedNAS ohne TLS auf
`127.0.0.1:8088` laufen lassen und die Terminierung dem Proxy überlassen. Dann
`publicUrl` in der Konfiguration setzen, damit Freigabelinks die richtige
Adresse tragen.

Caddy genügt dafür eine Zeile:

```
nas.example.de {
    reverse_proxy 127.0.0.1:8088
}
```

## Sicherheit

### Was eingebaut ist

- Passwörter als bcrypt-Hash, nie im Klartext.
- Sitzungscookies `HttpOnly` und `SameSite=Lax`, bei HTTPS zusätzlich `Secure`.
- Jede schreibende Anfrage braucht einen eigenen Header und eine passende
  Herkunft – ein fremdes Formular kann damit nichts auslösen.
- Strikte Content-Security-Policy: keine fremden Skripte, keine Einbettung
  durch Dritte.
- HTML und SVG werden nie zur direkten Anzeige ausgeliefert, sondern immer als
  Download. Sonst könnte eine hochgeladene HTML-Datei Skripte im Ursprung der
  Anwendung ausführen.
- Alle Pfade werden serverseitig normalisiert; `../` führt nie aus der
  Freigabe heraus.
- Verzögerung nach falschem Passwort, gleiche Antwortzeit für unbekannte
  Benutzer.

### Was du tun solltest

**Den Port nicht ins Internet freigeben.** Das ist der wichtigste Punkt. Eine
Portfreigabe macht SpeedNAS für die ganze Welt sichtbar, samt automatisierter
Anmeldeversuche rund um die Uhr. Für unterwegs ist ein **VPN** der richtige
Weg: Es ist genauso bequem, aber niemand außer dir kommt überhaupt an die
Anmeldemaske.

Wenn es doch ohne VPN sein muss: hinter einen Reverse Proxy mit echtem
Zertifikat setzen, ein langes Passwort verwenden und `localOnlyNoAuth`
zwingend auf `false` lassen.

**Nur-Lese-Zugänge nutzen.** Für Mitbewohner oder Kinder einen Benutzer mit
`readOnly` anlegen. Löschen ist dann ausgeschlossen.

**Es gibt keinen Papierkorb.** Gelöscht ist gelöscht – SpeedNAS reicht den
Befehl direkt an den Speicher weiter. Wer sich davor schützen will, setzt den
Speicherort auf `readOnly` und arbeitet über einen zweiten, schreibenden
Zugang bewusst.

**Sicherungen bleiben deine Aufgabe.** Ein USB-Stick am Router ist keine
Sicherung. SpeedNAS macht das Kopieren zwischen zwei Speicherorten leicht –
etwa vom Router-Stick auf eine externe Platte am PC –, aber automatisch
passiert das nicht.

## Sichern und umziehen

Zu sichern sind genau zwei Dateien:

```
config.json    Einstellungen und Zugangsdaten
state.json     Freigabelinks und Lesezeichen
```

`thumbs/` und `uploads/` sind reine Zwischenspeicher und können weg.

Umzug auf einen anderen Rechner: beide Dateien und die `speednas`-Datei
kopieren, starten, fertig. In `config.json` gegebenenfalls `dataDir` anpassen.

## Fehlersuche

**„Port belegt"** – etwas anderes hört schon auf 8088. Mit
`speednas -listen :8090` ausweichen.

**Alles ist plötzlich langsam** – erst den Geschwindigkeitstest laufen lassen
(*Einstellungen → Diagnose*), dann [performance.md](performance.md).

**Ein Speicherort ist rot** – *Verbindung testen* im Bearbeiten-Dialog zeigt
die genaue Fehlermeldung, und bei SMB führt ein Knopf direkt zur
Protokollprüfung.

**Vorschaubilder fehlen** – für Videos und HEIC-Fotos wird `ffmpeg` im
Suchpfad gebraucht. Ob es gefunden wurde, steht unter
*Einstellungen → Leistung → Status*. Für gewöhnliche Bilder wird es nicht
gebraucht.

**Ausführliche Meldungen** stehen im Fenster bzw. bei systemd in
`journalctl -u speednas`.
