# Netzwerkspeicher am Speedport einrichten

Diese Anleitung beschreibt den Weg von „USB-Stick steckt im Router" bis
„SpeedNAS zeigt die Dateien an" – und was zu tun ist, wenn es hakt.

Die Menüs unterscheiden sich je nach Modell und Firmwarestand. Die Begriffe
sind aber über die Speedport-Reihe hinweg ähnlich; wo es abweicht, hilft die
Suche in der Router-Oberfläche nach „Speicher" oder „USB".

## 1. Freigabe im Router aktivieren

1. `http://speedport.ip` oder `http://192.168.2.1` aufrufen und anmelden.
2. **Heimnetzwerk → USB-Speicher** (je nach Modell auch *Netzwerkspeicher*
   oder *Speicher (NAS)*).
3. Die Freigabe einschalten. Notiere dir:
   - den **Freigabenamen** (steht meist direkt daneben, z. B. `USB-Speicher`)
   - ob ein **Benutzername und Passwort** verlangt wird oder Gastzugriff geht
4. Falls es eine Option für die **SMB-Version** gibt: auf SMB2 oder höher
   stellen. Das ist der wichtigste Schalter überhaupt – siehe unten.

Der Freigabename lässt sich auch raten lassen: In SpeedNAS beim Anlegen eines
SMB-Standorts auf **Freigaben suchen** klicken. Wenn der Router antwortet,
erscheinen die Namen zur Auswahl.

## 2. Standort in SpeedNAS anlegen

**Einstellungen → Speicherorte → Speicherort hinzufügen**

| Feld | Wert |
|---|---|
| Typ | SMB / Windows-Freigabe |
| Name | frei wählbar, z. B. `Speedport USB` |
| Adresse | `192.168.2.1` (oder die IP deines Routers) |
| Freigabename | der Name aus Schritt 1 |
| Benutzer | falls der Router einen verlangt; leer lassen heißt Gastzugang |
| Passwort | dazu passend |
| Protokollversion | *Automatisch* – nur bei Problemen ändern |
| Unterordner | leer, außer du willst nur einen Teilbaum sehen |

Dann **Verbindung testen**. Bei Erfolg erscheint die Zahl der gefundenen
Einträge und der freie Speicherplatz.

**Tipp:** Feste IP für den Router ist hier kein Thema, wohl aber für den PC,
auf dem SpeedNAS läuft – sonst ändert sich die Adresse, unter der du vom Handy
aus zugreifst. Im Router unter *Heimnetzwerk → DHCP* lässt sich dem PC dieselbe
Adresse dauerhaft zuweisen.

## 3. Wenn es nicht klappt

Zuerst die Diagnose laufen lassen: **Einstellungen → Diagnose**, Adresse des
Routers eintragen, **Prüfen**. Oder auf der Kommandozeile:

```
speednas -probe 192.168.2.1
```

### „Der Server ist nicht erreichbar"

Der Router antwortet auf Port 445 nicht.

- Ist die Freigabe im Router wirklich aktiviert und der Stick erkannt?
- Stimmt die IP-Adresse? (`ipconfig` unter Windows zeigt unter
  *Standardgateway* die Adresse des Routers.)
- Blockiert eine Firewall oder ein Virenscanner ausgehende Verbindungen auf
  Port 445? Windows tut das in öffentlichen Netzwerkprofilen gelegentlich.
- Manche Modelle bieten die Freigabe nur an, wenn der Stick in einem von SMB
  unterstützten Dateisystem formatiert ist (FAT32, exFAT oder NTFS – **nicht**
  ext4 oder APFS).

### „Der Server spricht ausschließlich SMB1"

Das ist der häufigste Fall bei älteren Firmwareständen und die Erklärung
dafür, dass der Windows-Explorer und die iOS-Dateien-App nichts mehr finden:
beide haben SMB1 entfernt.

Der Reihe nach:

1. **Firmware aktualisieren.** *Einstellungen → Firmware-Update*. Neuere
   Stände bringen häufig SMB2 mit.
2. **Im Router nach einer SMB-Einstellung suchen.** Manche Modelle haben einen
   Schalter für die Protokollversion oder für „ältere Geräte unterstützen" –
   Letzterer schaltet SMB1 *zusätzlich* ein, nicht SMB2 aus.
3. **FTP verwenden.** Bietet der Router eine FTP-Freigabe an, richte den
   Speicherort in SpeedNAS als *FTP* ein. FTP ist von der SMB-Version völlig
   unabhängig und funktioniert auch dort, wo SMB1 die einzige Option wäre.
4. **Am Router vorbei.** Wenn nichts davon geht, ist der ehrliche Rat: Die
   USB-Freigabe eines Routers ist ohnehin die langsamste denkbare Variante.
   Ein Raspberry Pi mit der Platte am USB-Anschluss, auf dem SpeedNAS direkt
   läuft, ist schneller, sicherer und kostet wenig.

SpeedNAS unterstützt SMB1 bewusst nicht. Das Protokoll gilt seit
WannaCry/EternalBlue als nicht mehr vertretbar; es einzubauen hieße, die
Lücke wieder aufzumachen, die Microsoft und Apple gerade geschlossen haben.

### „Anmeldung fehlgeschlagen"

- Benutzername leer lassen und es als Gast versuchen – manche Router führen
  gar keine Benutzerverwaltung für die Freigabe.
- Manche Router erwarten den Benutzernamen in einer bestimmten Schreibweise
  oder mit vorangestelltem Gerätenamen. Steht meist in der Router-Oberfläche
  direkt bei der Freigabe.
- Falls ein *Domäne*-Feld nötig ist: `WORKGROUP` eintragen.

### „Freigabename unbekannt"

Groß- und Kleinschreibung sowie Bindestriche müssen exakt stimmen. Am
sichersten ist der Weg über **Freigaben suchen**.

### Verbindung bricht ständig ab

Router beenden ungenutzte SMB-Sitzungen gern nach kurzer Zeit. SpeedNAS merkt
das und baut die Verbindung automatisch neu auf – ein Abbruch sollte für dich
unsichtbar bleiben. Falls es trotzdem klemmt, hilft in
**Einstellungen → Leistung** ein kleinerer Wert für *Parallele Leseanfragen*
(2 statt 4); schwache Router-CPUs kommen mit vielen gleichzeitigen Anfragen
mitunter nicht zurecht.

### Es funktioniert, ist aber sehr langsam

Das ist ein eigenes Thema: [performance.md](performance.md).

## 4. Was der Diagnosebericht bedeutet

```
  TCP-Antwortzeit    0.8 ms      Netzwerklaufzeit zum Router
  SMB2/3             ja - SMB 2.1   ausgehandelte Protokollversion
  Signierung         aktiv=true erzwungen=false
  max. Leseblock     64 KiB      größte Leseanfrage, die der Server annimmt
  max. Schreibblock  64 KiB
  SMB1               nein
```

- **Antwortzeit** unter 2 ms heißt LAN, 5–20 ms heißt WLAN, darüber VPN oder
  Mobilfunk. Je höher der Wert, desto mehr bringt paralleles Vorauslesen.
- **Signierung erzwungen** kostet auf schwacher Hardware Durchsatz, weil jedes
  Paket kryptografisch geprüft wird. Lässt sich das im Router abschalten, ist
  ein Vergleichstest lohnend.
- **max. Leseblock** ist die Obergrenze für die Blockgröße unter
  *Einstellungen → Leistung*. Ein größerer Wert bringt dort nichts.
