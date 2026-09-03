# Konfiguration

SpeedNAS legt beim ersten Start eine Datei `config.json` an. Alles darin lässt
sich auch in der Oberfläche einstellen; die Datei ist der Vollständigkeit
halber dokumentiert.

## Wo die Dateien liegen

| System | Ort |
|---|---|
| Windows | `%APPDATA%\speednas\` |
| macOS | `~/Library/Application Support/speednas/` |
| Linux | `~/.config/speednas/` |

Darin:

```
config.json      Einstellungen und Zugangsdaten
state.json       Sitzungen, Freigabelinks, Lesezeichen
thumbs/          Vorschaubilder
uploads/         Zwischenspeicher laufender Uploads
tls/             selbstsigniertes Zertifikat (falls -tls)
```

Mit `-config pfad.json` lässt sich eine andere Datei verwenden, mit `-data
verzeichnis` ein anderes Datenverzeichnis. Beides zusammen macht SpeedNAS
tragbar – etwa auf einem USB-Stick.

## Aufbau

```json
{
  "server": {
    "listen": ":8088",
    "dataDir": "C:\\Users\\Paul\\AppData\\Roaming\\speednas",
    "publicUrl": "",
    "tls": { "enabled": false, "selfSigned": true, "certFile": "", "keyFile": "", "hosts": [] }
  },
  "auth": {
    "enabled": true,
    "sessionTtlHours": 336,
    "localOnlyNoAuth": false,
    "users": [
      { "name": "paul", "passwordHash": "$2a$10$…", "admin": true, "readOnly": false }
    ]
  },
  "performance": {
    "prefetchWorkers": 4,
    "prefetchChunkKb": 1024,
    "listCacheSeconds": 5,
    "thumbCacheMb": 512,
    "thumbWorkers": 3,
    "searchWorkers": 6,
    "uploadPartMb": 4
  },
  "locations": [ … ]
}
```

### server

| Feld | Bedeutung |
|---|---|
| `listen` | Adresse und Port. `:8088` heißt „auf allen Netzwerkkarten", `127.0.0.1:8088` nur lokal. |
| `dataDir` | Verzeichnis für Cache und Zustand. |
| `publicUrl` | Wird für Freigabelinks vorangestellt, falls SpeedNAS hinter einem Reverse Proxy läuft, z. B. `https://nas.example.de`. |
| `tls.enabled` | HTTPS einschalten. |
| `tls.selfSigned` | Zertifikat selbst erzeugen, wenn keines angegeben ist. |
| `tls.certFile` / `keyFile` | Eigenes Zertifikat im PEM-Format. |
| `tls.hosts` | Zusätzliche Namen und IPs im selbstsignierten Zertifikat. |

### auth

| Feld | Bedeutung |
|---|---|
| `enabled` | Anmeldung erforderlich. **Nur ausschalten, wenn niemand sonst ins Netz kommt.** |
| `sessionTtlHours` | Wie lange eine Anmeldung hält. 336 = 14 Tage. |
| `localOnlyNoAuth` | Zugriffe aus privaten Adressbereichen ohne Anmeldung durchlassen. Bequem, aber jedes Gerät im WLAN kommt damit an alle Dateien. |
| `users[].passwordHash` | bcrypt. Nie von Hand eintragen – `speednas -add-user name` benutzen. |
| `users[].readOnly` | Darf nur lesen und herunterladen. |
| `users[].admin` | Darf Speicherorte, Benutzer und Leistung ändern. |

### performance

Ausführlich erklärt in [performance.md](performance.md).

### locations

Jeder Eintrag hat gemeinsame Felder und einen protokollabhängigen Block.

```json
{
  "id": "3kq7…",
  "label": "Speedport USB",
  "type": "smb",
  "root": "",
  "readOnly": false,
  "poolSize": 4,
  "smb": { … }
}
```

| Feld | Bedeutung |
|---|---|
| `id` | Wird automatisch vergeben, nicht ändern (Lesezeichen und Links hängen daran). |
| `label` | Name in der Seitenleiste. |
| `type` | `smb`, `ftp`, `sftp`, `webdav` oder `local`. |
| `root` | Unterordner innerhalb der Freigabe. Alles oberhalb bleibt unsichtbar. |
| `readOnly` | Schreibzugriffe für alle Benutzer sperren. |
| `poolSize` | Gleichzeitige Verbindungen. Standard 4 (FTP: 2). |

**smb**

```json
"smb": {
  "host": "192.168.2.1",
  "port": 445,
  "share": "USB-Speicher",
  "user": "",
  "password": "",
  "domain": "",
  "dialect": "",
  "requireSigning": false,
  "maxCredits": 0
}
```

`dialect` leer heißt automatisch aushandeln; mögliche feste Werte sind
`2.0.2`, `2.1`, `3.0`, `3.0.2` und `3.1.1`. Leerer Benutzer heißt Gastzugang.

**ftp**

```json
"ftp": {
  "host": "192.168.2.1", "port": 21,
  "user": "", "password": "",
  "tls": "none",
  "skipVerify": false,
  "disableEpsv": false
}
```

`tls` kennt `none`, `explicit` (AUTH TLS, Port 21) und `implicit` (Port 990).
`disableEpsv` hilft bei älteren Routern, die den erweiterten Passivmodus nicht
beherrschen.

**sftp**

```json
"sftp": {
  "host": "192.168.2.10", "port": 22,
  "user": "paul", "password": "",
  "keyFile": "C:\\Users\\Paul\\.ssh\\id_ed25519",
  "passphrase": "",
  "hostKey": "SHA256:…"
}
```

`hostKey` wird beim ersten Verbinden automatisch eingetragen. Ändert sich der
Schlüssel der Gegenstelle später, verweigert SpeedNAS die Verbindung – so wie
`ssh` es auch tut.

**webdav**

```json
"webdav": {
  "url": "https://cloud.example.de/remote.php/dav/files/paul",
  "user": "paul", "password": "", "skipVerify": false
}
```

**local**

```json
"local": { "path": "D:\\Medien" }
```

Ein Ordner auf dem Rechner, auf dem SpeedNAS läuft. Praktisch, um Dateien
zwischen PC und NAS zu schieben – oder um SpeedNAS auf einem Raspberry Pi
direkt vor die angeschlossene Platte zu setzen.

## Passwörter aus der Umgebung

Statt eines Passworts kann überall `${NAME}` stehen; SpeedNAS setzt dann die
gleichnamige Umgebungsvariable ein:

```json
"smb": { "host": "192.168.2.1", "share": "USB", "password": "${NAS_PASSWORT}" }
```

So bleibt das Passwort aus der Datei heraus – sinnvoll, wenn die Konfiguration
in einer Sicherung oder einem Repository landet.

## Nach Hand-Änderungen

SpeedNAS liest die Datei beim Start. Nach einer Änderung von Hand also einmal
neu starten. Änderungen über die Oberfläche greifen sofort und werden
zurückgeschrieben.
