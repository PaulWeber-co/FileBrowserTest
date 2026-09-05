# SMB1 – warum es unsicher ist und warum SpeedNAS es trotzdem kann

Der Speedport 4 bietet seine USB-Freigabe nur über **SMB1** an. Windows hat
SMB1 seit Version 1709 nicht mehr an Bord, iOS unterstützt es nie. Genau
deshalb finden der Explorer und die Dateien-App nichts.

SpeedNAS bringt einen eigenen SMB1-Client mit. Diese Seite erklärt, was an
SMB1 kaputt ist, warum SpeedNAS es trotzdem anbietet, und wie die Umsetzung
den Schaden begrenzt.

## 1. Warum SMB1 als unsicher gilt

SMB1 ist von 1983 (als „Core Protocol"). Es stammt aus einer Zeit, in der ein
Netzwerk ein Raum mit einem Dutzend Rechnern war und jeder darin
vertrauenswürdig. Die Probleme sind nicht ein einzelner Fehler, sondern
mehrere Schichten:

### a) Die Verbindung ist unverschlüsselt – und es gibt keinen Schalter dafür

SMB3 kann jedes Paket verschlüsseln (AES-GCM). SMB1 kann das nicht, in keiner
Variante. Alles geht im Klartext über die Leitung:

- die Dateinamen,
- die Dateiinhalte,
- die Verzeichnisstruktur.

Wer im selben Netz mitliest – ein anderes Gerät im WLAN, ein kompromittierter
Repeater, ein offener Gäste-Zugang – sieht deine Dateien wortwörtlich mit.
Nicht Metadaten, sondern die Bytes.

Das Passwort selbst geht nicht im Klartext über die Leitung (siehe b), aber
das ändert nichts an den Inhalten.

### b) Die Anmeldung ist knackbar

SMB1 kennt vier Anmeldeverfahren, drei davon sind gebrochen:

| Verfahren | Problem |
|---|---|
| **LM** (LAN Manager) | Passwort wird auf 14 Zeichen gekürzt, in zwei 7-Zeichen-Hälften zerlegt, in Großbuchstaben gewandelt. Ein 14-Zeichen-Passwort ist damit nur so stark wie zwei 7-Zeichen-Passwörter aus 69 Zeichen – auf heutiger Hardware Minuten. |
| **NTLMv1** | Nutzt DES mit einem 56-Bit-Schlüssel aus dem Passwort-Hash. Rainbow-Tables dafür gibt es seit 2007 fertig zum Download. |
| **NTLMv2** | Kryptografisch brauchbar (HMAC-MD5 mit Zeitstempel gegen Wiedereinspielen), aber: der Hash geht als „Challenge-Response" über die Leitung und lässt sich offline durchprobieren. Bei schwachem Passwort fällt er. |
| **Kerberos** | Sicher – aber praktisch nie in Router-Firmware vorhanden. |

Dazu kommt: SMB1 kennt **kein Pre-Authentication-Integrity**. Bei SMB 3.1.1
wird der gesamte Verhandlungsverlauf am Ende gemeinsam gehasht. Wenn ein
Angreifer in der Mitte auch nur ein Bit der Verhandlung verändert hat, passt
der Hash nicht und die Verbindung bricht ab. SMB1 hat nichts Vergleichbares.

### c) Downgrade-Angriff: SMB1 gefährdet auch Verbindungen, die es nicht nutzen

Das ist der Punkt, der Microsoft dazu gebracht hat, SMB1 komplett zu
**entfernen** statt es nur abzuschalten.

Client und Server einigen sich am Anfang auf einen Dialekt. Der Client
schickt eine Liste („ich kann SMB 3.1.1, 3.0, 2.1, NT LM 0.12"), der Server
sucht sich den besten aus. Diese Verhandlung ist bei SMB1 **ungeschützt**.

Ein Angreifer in der Mitte streicht aus der Liste alles außer SMB1. Beide
Seiten glauben, die Gegenseite könne nichts Besseres, und sprechen SMB1 –
also unverschlüsselt und mit schwacher Anmeldung. Solange ein Client SMB1
überhaupt beherrscht, ist er dafür angreifbar, selbst wenn er es nie
absichtlich benutzt.

### d) Die Implementierung war zusätzlich löchrig

SMB1 hat eine sehr große Angriffsfläche: über 70 Befehle, viele davon mit
variablen Längenfeldern, die auf Feldern aus derselben Nachricht beruhen.
Genau das ging jahrelang schief:

- **EternalBlue (MS17-010, 2017)** – ein Fehler in der Umrechnung zwischen
  zwei Paketformaten (`SMB_COM_TRANSACTION2` vs. `TRANS2_SECONDARY`) führt zu
  einem Pufferüberlauf im **Windows-Kernel**. Ergebnis: Codeausführung mit
  SYSTEM-Rechten, ohne Anmeldung, allein durch ein Paket auf Port 445.
  WannaCry und NotPetya haben damit 2017 weltweit Krankenhäuser,
  Reedereien und Fabriken lahmgelegt.
- **SMBLoris (2017)** – ein paar hundert Verbindungen bringen einen Server
  zum Stillstand, ohne Anmeldung.
- **Badlock (2016)**, **SMBv1-Nullsitzungen** – Aufzählung von Benutzern und
  Freigaben ohne Zugangsdaten.

Wichtig zum Verständnis: Das waren Fehler in *Microsofts Server*-Code, nicht
im Protokoll selbst. Aber die Komplexität von SMB1 hat sie erst möglich
gemacht – und dieselbe Komplexität steckt in jeder Router-Firmware, die SMB1
anbietet, meist in einer uralten Samba-Version, die seit Jahren keine
Sicherheitsupdates mehr gesehen hat.

## 2. Was das für deinen Speedport konkret bedeutet

Die Gefahren ordnen sich sehr unterschiedlich ein:

| Risiko | Gilt bei dir? |
|---|---|
| Mitlesen im lokalen Netz | **Ja, aber begrenzt.** Nur wer schon in deinem WLAN ist, kann mitlesen. Bei WPA2/WPA3 mit gutem Passwort ist das niemand. Ein Gäste-WLAN oder ein gekapertes IoT-Gerät im selben Netz ändert das. |
| Passwort offline knacken | **Ja, begrenzt.** Gleiche Voraussetzung: der Angreifer muss den Anmeldeverkehr mitschneiden können. |
| Downgrade-Angriff | **Nicht relevant.** Der Router kann ohnehin nichts Besseres – es gibt nichts, wovon heruntergestuft werden könnte. |
| EternalBlue & Co. | **Nicht auf deinem PC.** Das sind Lücken in *Servern*. SpeedNAS ist der *Client*. Betroffen wäre der Router selbst – und der ist aus dem Internet nicht erreichbar, solange du Port 445 nicht weiterleitest (siehe unten). |
| SMB1 über VPN/Internet | **Das wäre gefährlich.** Deshalb macht SpeedNAS es nicht (siehe 4.). |

Die ehrliche Kurzfassung: **SMB1 nur im eigenen LAN, für einen USB-Stick am
Router, ist ein vertretbares Restrisiko.** SMB1 über ein fremdes Netz ist es
nicht.

## 3. Was du trotzdem tun solltest

1. **Firmware aktualisieren.** *Einstellungen → Firmware-Update*. Neuere
   Speedport-Stände bringen teils SMB2 mit. Dann in SpeedNAS den Dialekt von
   „SMB 1" auf „automatisch" zurückstellen.
2. **Port 445 niemals im Router weiterleiten.** Kein Port-Forwarding, keine
   DMZ, keine „Exposed Host"-Einstellung für den Router selbst. Sonst steht
   ein zwanzig Jahre alter SMB1-Server im offenen Internet – das ist genau
   das Szenario, in dem WannaCry funktioniert hat.
3. **Ein eigenes Passwort für die Freigabe.** Nicht dasselbe wie fürs
   Router-Menü, fürs WLAN oder für irgendetwas anderes. Wenn es doch
   mitgeschnitten und geknackt wird, ist der Schaden auf den USB-Stick
   begrenzt.
4. **Keine sensiblen Daten unverschlüsselt auf den Stick.** Steuerunterlagen,
   Ausweiskopien, Passwortdatenbanken: vorher in ein verschlüsseltes Archiv
   (7-Zip mit AES-256, VeraCrypt-Container). SpeedNAS überträgt die Datei
   dann als undurchsichtigen Block.
5. **Nur wenn du sowieso über SMB1 nachdenkst:** Ein Raspberry Pi mit der
   Platte am USB-Anschluss, auf dem SpeedNAS direkt läuft, ist schneller
   *und* sicherer als jede Router-Freigabe – und kostet unter 50 Euro.

## 4. Wie SpeedNAS den Schaden begrenzt

Der eingebaute SMB1-Client ist kein „SMB1 wieder anschalten". Er ist bewusst
so schmal wie möglich gehalten:

- **Nur NTLMv2, nichts Schwächeres.** LM und NTLMv1 sind gar nicht erst
  implementiert – nicht abgeschaltet, sondern nicht vorhanden. Verlangt ein
  Server sie, schlägt die Anmeldung fehl. Das ist Absicht.
  Siehe `internal/vfs/smb1/auth.go`.
- **Nur der Dialekt „NT LM 0.12".** Die noch älteren Varianten (Core,
  LANMAN 1.0/2.1) werden gar nicht angeboten. Ein Server, der nur die kann,
  wird abgelehnt.
- **SMB1 wird nie automatisch gewählt.** Der Dialekt „SMB 1" musst du in
  *Einstellungen → Speicherort → SMB → Dialekt* ausdrücklich einstellen. Die
  Automatik verhandelt ausschließlich SMB2/SMB3. Damit ist ein
  Downgrade-Angriff auf SpeedNAS ausgeschlossen: Es gibt keine Verhandlung,
  die man herunterstufen könnte.
- **Die Oberfläche warnt.** Wählst du SMB 1, erscheint direkt darunter der
  Hinweis, dass die Übertragung unverschlüsselt ist.
- **SMB1 bleibt im LAN.** Das ist der wichtigste Punkt und ergibt sich aus
  der Architektur: Von unterwegs spricht dein Handy **HTTP durch den
  VPN-Tunnel** mit SpeedNAS – der Tunnel selbst ist verschlüsselt, optional
  zusätzlich HTTPS (*Einstellungen → Server → TLS*). Und nur SpeedNAS
  spricht SMB1 mit dem Router, über ein paar Meter LAN-Kabel.
  Das unverschlüsselte Protokoll verlässt dein Netz nie. Nebenbei ist das
  auch der Grund, warum es über VPN schnell ist: siehe
  [performance.md](performance.md).

```
   Handy (unterwegs)               dein Zuhause
   ┌──────────┐   HTTP im VPN   ┌───────────┐   SMB1    ┌──────────┐
   │ Browser  │════════════════▶│ SpeedNAS  │──────────▶│ Speedport│
   └──────────┘  verschlüsselt  └───────────┘  im LAN   └──────────┘
                                            unverschlüsselt, verlässt
                                            aber das Haus nicht
```

- **Ein Client, kein Server.** SpeedNAS nimmt keine SMB-Verbindungen an. Die
  Klasse von Lücken, zu der EternalBlue gehört, betrifft Server. SpeedNAS
  hat keinen.
- **Alle Längenfelder werden geprüft.** Jede Antwort wird gegen die
  tatsächlich empfangene Nachrichtenlänge validiert, bevor daraus gelesen
  wird. Ein bösartiger Server bekommt einen Fehler, keinen Absturz.

## 5. Einrichtung

*Einstellungen → Speicherorte → Neu → SMB*, dann:

| Feld | Wert |
|---|---|
| Server | `192.168.2.1` (die Router-Adresse) |
| Freigabe | z. B. `USB-Speicher` – exakte Schreibweise, siehe Router-Menü |
| Benutzer / Passwort | wie im Router hinterlegt, sonst leer für Gast |
| **Dialekt** | **`SMB 1 (nur wenn der Router nichts anderes kann)`** |

Vorher lohnt der Diagnosebericht – er sagt, ob SMB2 wirklich fehlt:

```
$ speednas -probe 192.168.2.1

  TCP-Antwortzeit    0.8 ms
  SMB2/3             nein
  SMB1               ja - NT LM 0.12
```

Steht dort bei SMB2/3 ein „ja", nimm die Automatik. SMB1 ist nur für den
Fall gedacht, dass dort „nein" steht.

## 6. Was über SMB1 geht – und was nicht

Getestet gegen einen echten SMB1-Server (Samba, auf `NT1` festgenagelt):

| Funktion | Status |
|---|---|
| Verzeichnisse anzeigen | ✅ |
| Dateien lesen, auch Teilbereiche (Video-Sprung) | ✅ |
| Hochladen, auch fortsetzbar in Teilen | ✅ |
| Umbenennen, Löschen, Ordner anlegen | ✅ |
| Umlaute und Leerzeichen in Namen | ✅ (UTF-16) |
| Ordner rekursiv löschen | ✅ – SpeedNAS läuft den Baum selbst ab |
| ZIP-Download eines ganzen Ordners | ✅ |
| Freien Speicherplatz anzeigen | ✅ |
| Änderungsdatum setzen | ❌ – nicht unterstützt |
| Serverseitiges rekursives Löschen | ❌ – SMB1 kennt es nicht, SpeedNAS ersetzt es |
| Verschlüsselung | ❌ – gibt es in SMB1 nicht |

Das Tempo entspricht SMB2: Der Vorauslese-Leser holt mehrere Bereiche
parallel, das gleicht die höhere Zahl an Rundreisen aus.

## 7. Technische Umsetzung

Für alle, die es interessiert – der Code liegt in `internal/vfs/smb1/`:

| Datei | Inhalt |
|---|---|
| `proto.go` | NetBIOS-Rahmen, 32-Byte-SMB-Kopf, Statuscodes, UTF-16 |
| `session.go` | Dialektverhandlung, Anmeldung, Tree-Connect |
| `ntlmssp.go` | NTLMSSP-Nachrichten (Negotiate/Challenge/Authenticate) |
| `auth.go` | NTLMv2-Berechnung |
| `trans2.go` | TRANS2, inklusive mehrteiliger Antworten |
| `ops.go` | Listen, Stat, Öffnen, Lesen, Schreiben, Umbenennen, Löschen |

Zwei Details, die beim Bauen Zeit gekostet haben und in keiner Anleitung
stehen:

1. **Samba lehnt „rohes" NTLMv2 ab.** Ein NTLMv2-Response direkt im
   `SESSION_SETUP_ANDX` wird mit `NT_STATUS_INVALID_PARAMETER` quittiert
   („Rejecting raw NTLMv2 authentication"). Nötig ist die erweiterte
   Sicherheit: NTLMSSP in zwei Runden, wobei die erste Runde mit
   `STATUS_MORE_PROCESSING_REQUIRED` beantwortet wird und die dort
   vergebene UID für die zweite Runde übernommen werden muss.
   SPNEGO/ASN.1 braucht es dafür nicht – der Client sucht die
   NTLMSSP-Signatur im Blob und kommt ohne ASN.1-Parser aus.
2. **TRANS2-Offsets zählen ab Nachrichtenanfang**, nicht ab dem Datenblock.
   Deshalb hält der Client die Rohnachricht fest, statt sie nach dem Parsen
   wegzuwerfen.

Und eine Eigenheit, die die Sperre im Client erklärt: **eine SMB1-Verbindung
verträgt immer nur eine Anfrage gleichzeitig.** Der Vorauslese-Leser feuert
mehrere `ReadAt` parallel ab; ohne Sperre würden sich zwei Goroutinen ihre
Antworten gegenseitig wegschnappen. Die Parallelität entsteht deshalb über
mehrere Verbindungen im Pool, nicht innerhalb einer.
