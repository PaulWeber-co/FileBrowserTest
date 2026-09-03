# Tempo: VPN, USB 2.0 und was wirklich bremst

Dieses Kapitel erklärt, wo die Grenzen liegen, warum SpeedNAS über VPN
deutlich schneller ist als ein direkt eingebundenes Netzlaufwerk, und welche
Einstellung was bewirkt.

## Wo der Engpass sitzt

Vier Dinge begrenzen die Geschwindigkeit. Sie wirken hintereinander – der
langsamste gewinnt:

| Glied | Typische Obergrenze |
|---|---|
| USB 2.0 am Router | 480 Mbit/s brutto, real 25–35 MB/s |
| CPU des Routers | oft der eigentliche Engpass: 3–15 MB/s |
| Netzwerkstrecke | LAN 110 MB/s, WLAN 5 GHz 30–70 MB/s, VPN je nach Upload |
| Latenz × Protokoll | siehe unten – oft schlimmer als alles andere |

Bei einer USB-Platte am Router ist meistens nicht USB 2.0 der Flaschenhals,
sondern die CPU. Router sind für Paketvermittlung gebaut, nicht für
Dateiserverdienste. 5–10 MB/s sind ein normaler Wert, und daran ändert keine
Einstellung etwas Grundsätzliches.

**Miss es, bevor du drehst:** *Einstellungen → Diagnose → Geschwindigkeitstest*.
Der Test liest dieselbe Datei zweimal – einmal mit einer einzelnen Leseanfrage,
einmal mit mehreren parallelen – und zeigt beide Werte nebeneinander.

## Warum Latenz so weh tut

SMB ist ein gesprächiges Protokoll. Eine Datei zu öffnen kostet mehrere
Nachrichten hin und zurück, ein Verzeichnis zu lesen ebenfalls. Solange die
Antwortzeit unter einer Millisekunde liegt, fällt das nicht auf. Über eine
VPN-Strecke mit 40 ms wird jede dieser Nachrichten zu einer Achtzigstelsekunde
Wartezeit.

Rechenbeispiel für einen Download über eine einzelne Leseanfrage:

```
Blockgröße 64 KiB, Antwortzeit 40 ms
→ 25 Anfragen pro Sekunde × 64 KiB ≈ 1,6 MB/s
```

Diese Grenze hat nichts mit der Bandbreite zu tun. Selbst über eine
Gigabit-Leitung bleiben es 1,6 MB/s, solange jeweils nur eine Anfrage
unterwegs ist. Genau das ist der Grund, warum ein per VPN eingebundenes
Netzlaufwerk sich zäh anfühlt.

SpeedNAS setzt an zwei Stellen an.

### 1. HTTP statt SMB über die lange Strecke

```
ohne SpeedNAS:   iPhone ──── SMB über VPN (40 ms, sehr gesprächig) ──── Router
mit SpeedNAS:    iPhone ──── HTTP über VPN (40 ms, eine Anfrage) ──── PC ── SMB (0,5 ms) ── Router
```

Die gesprächige SMB-Unterhaltung bleibt im lokalen Netz, wo Latenz keine Rolle
spielt. Über die VPN-Strecke läuft nur noch HTTP, und das kommt mit einer
Anfrage pro Aktion aus. Ein Verzeichniswechsel ist dann ein einziger
Roundtrip statt zwölf.

### 2. Paralleles Vorauslesen

Statt eine Leseanfrage nach der anderen zu stellen, hat SpeedNAS mehrere
gleichzeitig unterwegs und gibt die Daten trotzdem in der richtigen Reihenfolge
aus. Aus dem Rechenbeispiel oben wird mit vier parallelen 1-MiB-Anfragen:

```
4 Anfragen × 1 MiB pro 40 ms ≈ 100 MB/s theoretisch
```

Das ist natürlich nicht das, was am Ende herauskommt – dann greifen wieder
USB, Router-CPU und Leitung. Aber die Latenz ist als Engpass beseitigt.

Verbindungen bleiben außerdem offen und werden wiederverwendet. Ein frischer
SMB-Handshake kostet vier bis sechs Roundtrips; über VPN sind das schnell
250 ms – pro Klick.

## Die Stellschrauben

**Einstellungen → Leistung**

### Parallele Leseanfragen (Standard 4)

Der wichtigste Regler.

| Situation | Empfehlung |
|---|---|
| Gleiches LAN, Kabel | 2–4 |
| WLAN | 4 |
| VPN oder Mobilfunk | 6–8 |
| Router bricht ein, Fehler häufen sich | 2 |

Mehr ist nicht automatisch besser: Jede offene Anfrage belegt Speicher auf dem
Router. Wenn der Geschwindigkeitstest zeigt, dass parallel *langsamer* ist als
seriell, ist die Gegenstelle überlastet – dann runter auf 2.

### Blockgröße (Standard 1024 KB)

Wie viel pro Anfrage geholt wird. Größere Blöcke bedeuten weniger Anfragen.

Wichtig: **nicht größer als der maximale Leseblock des Servers.** Den zeigt die
Diagnose an. Meldet der Router 64 KiB, bringt ein Wert über 64 nichts.

Der Speicherbedarf ist `Parallele Anfragen × Blockgröße` pro laufendem
Download – bei 4 × 1024 KB also 4 MB.

### Verzeichnis-Cache (Standard 5 Sekunden)

Wie lange eine einmal geholte Ordnerliste wiederverwendet wird. Das macht
Zurückspringen unmittelbar. Änderungen durch SpeedNAS selbst leeren den Cache
gezielt; Änderungen, die jemand anders am NAS vornimmt, sind bis zu diese
Sekundenzahl später sichtbar. `0` schaltet den Cache ab.

### Upload-Teilgröße (Standard 4 MB)

Große Dateien gehen in Stücken zum Server. Kleinere Stücke werden nach einem
Abbruch schneller nachgeholt, erzeugen aber mehr Anfragen. Bei wackligem WLAN
sind 2 MB sinnvoll, im stabilen LAN ruhig 8.

### Vorschaubild-Cache (Standard 512 MB)

Vorschaubilder werden auf der Platte des Rechners abgelegt, auf dem SpeedNAS
läuft. Ohne diesen Cache müsste jedes Bild beim Scrollen erneut vollständig
über SMB gelesen werden. Bei großen Fotosammlungen ruhig auf 2000 setzen.

## Was beim Hochladen passiert

Große Dateien laufen in zwei Etappen:

```
Browser ──(volle LAN-Geschwindigkeit)──► SpeedNAS ──(so schnell der Router kann)──► NAS
             Stücke, wiederaufnehmbar        Zwischenspeicher auf Platte
```

Das hat drei Vorteile: Der Browser ist schnell fertig und blockiert den Tab
nicht; ein Abbruch kostet nur das fehlende Stück statt der ganzen Datei; und
auf dem NAS landet ein einziger durchgehender Schreibvorgang statt vieler
kleiner – was gerade langsamen USB-Sticks entgegenkommt.

Der Zwischenspeicher liegt im Datenverzeichnis (siehe
[konfiguration.md](konfiguration.md)) und wird nach dem Abschluss sofort
gelöscht, Reste spätestens nach zwölf Stunden.

## VPN richtig einstellen

Für den Zugriff von unterwegs ist ein VPN in den eigenen Router die richtige
Lösung – nicht eine Portfreigabe.

**WireGuard** ist deutlich schneller als IPSec oder OpenVPN, weil es weniger
Rechenaufwand pro Paket hat. Wenn dein Router es anbietet, nimm es.

**Der Upload zu Hause ist die Obergrenze.** Beim Herunterladen vom NAS aufs
Handy zählt nicht deine Downloadrate unterwegs, sondern der **Upload** deines
Heimanschlusses. Bei einem typischen DSL-Anschluss sind das 10–40 Mbit/s,
also 1,2–5 MB/s. Das ist dann der Engpass, und keine Einstellung ändert daran
etwas.

**MTU-Probleme** sind die häufigste Ursache für „geht, ist aber absurd
langsam" oder „bricht bei großen Dateien ab". VPN-Pakete tragen zusätzlichen
Kopf; passt das Ergebnis nicht mehr in ein normales Paket, wird zerlegt oder
verworfen. In der WireGuard-Konfiguration des Clients:

```
[Interface]
MTU = 1380
```

Testweise auf 1280 heruntergehen. Wird es dadurch besser, war es die MTU.

**Prüfen, ob es am VPN liegt:** Miss den Geschwindigkeitstest einmal im
Heimnetz und einmal über VPN. Ist der Wert im Heimnetz gut und über VPN
schlecht, liegt es an der Strecke, nicht am NAS.

## Realistische Erwartungen

| Aufbau | Lesen |
|---|---|
| USB-2.0-Stick am Router, LAN | 3–10 MB/s |
| USB-2.0-Festplatte am Router, LAN | 5–15 MB/s |
| dasselbe über WLAN 5 GHz | 4–12 MB/s |
| dasselbe über VPN | so viel wie dein Upload zu Hause hergibt |
| SSD am Raspberry Pi 4 (USB 3), LAN | 60–110 MB/s |

Wenn der Geschwindigkeitstest im LAN schon nur 4 MB/s zeigt, ist der Router
am Ende – dann bringt Feintuning nichts mehr. Der wirksamste Schritt ist dann,
den Speicher nicht mehr am Router zu betreiben. SpeedNAS läuft genauso auf
einem Raspberry Pi und spricht dort direkt mit der lokal angeschlossenen
Platte (Speicherort vom Typ *Lokaler Ordner*), womit SMB komplett entfällt.
