# iPhone, iPad und Android

Für die mobilen Geräte wird nichts installiert und nichts gekauft. SpeedNAS
läuft auf dem PC, das Handy braucht nur einen Browser – und legt sich das
Ganze auf Wunsch als App-Symbol auf den Homescreen.

## iPhone und iPad

1. **Adresse herausfinden.** Im Fenster von SpeedNAS steht sie beim Start:

   ```
     im Netzwerk: http://192.168.2.105:8088
   ```

   `localhost` funktioniert vom Handy aus nicht – es muss die IP-Adresse des
   PCs sein.

2. **In Safari öffnen** und anmelden. (Safari, nicht Chrome – nur Safari darf
   unter iOS eine Web-App auf den Homescreen legen.)

3. **Teilen-Symbol** unten in der Mitte antippen → nach unten scrollen →
   **Zum Home-Bildschirm** → **Hinzufügen**.

Fertig. SpeedNAS liegt jetzt als Symbol auf dem Homescreen, startet im
Vollbild ohne Browserleiste und sieht aus wie eine normale App.

### Was auf dem iPhone geht

- **Blättern und suchen** wie in der Dateien-App; ein Tipp öffnet, langes
  Drücken bringt das Kontextmenü.
- **Fotos und Videos ansehen** – mit Wischen zum nächsten Bild und Zoom durch
  Antippen. Videos lassen sich vorspulen; SpeedNAS liefert Teilbereiche aus,
  ohne die ganze Datei zu übertragen.
- **Musik hören**, auch im Hintergrund.
- **PDFs lesen.**
- **Hochladen**: über die Schaltfläche *Laden* – wahlweise aus der Fotos-App
  oder aus der Dateien-App.
- **Herunterladen**: über *Herunterladen* im Kontextmenü. Safari legt die
  Datei unter *Downloads* in der Dateien-App ab; von dort lässt sie sich
  weiterreichen.

### Auf dem Sperrbildschirm bleibt alles

Die Anmeldung hält vierzehn Tage (einstellbar). Das Symbol auf dem Homescreen
öffnet also direkt die Dateiliste, ohne jedes Mal Passwort.

### Hinweis zu HTTPS

Ohne HTTPS zeigt Safari beim Tippen in ein Passwortfeld eine Warnung. Im
eigenen WLAN ist das unkritisch. Wer die Warnung loswerden will, startet
SpeedNAS mit `-tls`; dann ist beim ersten Aufruf einmal eine
Zertifikatswarnung wegzuklicken (das Zertifikat ist selbstsigniert). Details
in [betrieb.md](betrieb.md).

## Android

Praktisch identisch, nur heißt der Menüpunkt anders:

1. Adresse in Chrome öffnen und anmelden.
2. Menü (drei Punkte) → **App installieren** bzw. **Zum Startbildschirm
   hinzufügen**.

Android bietet die Installation oft von selbst als Leiste am unteren Rand an.

## Von unterwegs

Damit das Ganze auch außer Haus funktioniert, muss das Handy ins Heimnetz –
per VPN. Die meisten Router bringen das mit; im Speedport findet es sich unter
*Internet → VPN* bzw. bei manchen Modellen über die MagentaZuhause-App.

Ist das VPN verbunden, gilt genau dieselbe Adresse wie zu Hause. Am Symbol auf
dem Homescreen ändert sich nichts.

Zum Tempo unterwegs siehe [performance.md](performance.md) – kurz gefasst:
die Obergrenze ist der Upload deines Heimanschlusses.

**Nicht empfohlen:** den Port im Router direkt ins Internet freigeben. Warum,
steht in [betrieb.md](betrieb.md).

## Wenn das Handy den PC nicht findet

- Sind beide im **selben WLAN**? Gastnetze sind oft vom Heimnetz getrennt.
- Hat der PC eine **andere IP** bekommen? Im Router unter *Heimnetzwerk →
  DHCP* lässt sich ihm dieselbe Adresse dauerhaft zuweisen.
- Blockiert die **Windows-Firewall** eingehende Verbindungen? Beim ersten
  Start fragt Windows danach – hier *Private Netzwerke* erlauben. Nachträglich:
  *Windows-Sicherheit → Firewall → App durch Firewall kommunizieren lassen*.
- Läuft SpeedNAS mit `-listen 127.0.0.1:8088`? Dann ist es absichtlich nur
  lokal erreichbar. Ohne diese Angabe hört es auf allen Adressen.

## Grenzen

Ehrlich gesagt: Eine Web-App ist keine native App. Was fehlt:

- **Keine Integration in die iOS-Dateien-App.** Andere Apps können nicht direkt
  auf den Speicher zugreifen; der Weg führt über Herunterladen und Teilen.
  Dafür bräuchte es eine echte App mit einer File Provider Extension, und die
  ginge nur über den App Store.
- **Kein Hintergrund-Upload.** Wird die App geschlossen, pausiert ein
  laufender Upload; er lässt sich anschließend fortsetzen.
- **Keine automatische Fotosicherung.** Das erlaubt iOS Web-Apps nicht.

Für Blättern, Ansehen, Hoch- und Herunterladen reicht es vollständig – und
genau das war der Punkt, an dem die Standard-Apps am Router gescheitert sind.
