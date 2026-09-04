// SpeedNAS - ein schneller Dateibrowser für Netzwerkspeicher.
//
// Ein einzelnes Programm, das im Hintergrund läuft und eine Weboberfläche
// bereitstellt. Damit kommt jedes Gerät im Netz an den Speicher - Windows,
// Mac, Linux, iPhone und Android gleichermaßen, ohne App-Installation.
package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	// Zeitzonendatenbank ins Programm einbetten (rund 450 KB). Ohne das
	// laeuft die Uhr in einem Container ohne /usr/share/zoneinfo - etwa auf
	// Basis "scratch" - stillschweigend auf UTC, und alle Zeitstempel in der
	// Oberflaeche sind um ein bis zwei Stunden verschoben.
	_ "time/tzdata"

	"golang.org/x/term"

	"github.com/PaulWeber-co/FileBrowserTest/internal/config"
	"github.com/PaulWeber-co/FileBrowserTest/internal/server"
	"github.com/PaulWeber-co/FileBrowserTest/internal/smbprobe"
)

// version wird beim Bauen über -ldflags gesetzt.
var version = "1.0.0"

func main() {
	log.SetFlags(log.Ltime)

	var (
		cfgPath  = flag.String("config", "", "Pfad zur Konfigurationsdatei")
		listen   = flag.String("listen", "", "Adresse, z. B. :8088 oder 127.0.0.1:8088")
		dataDir  = flag.String("data", "", "Datenverzeichnis für Cache und Zustand")
		useTLS   = flag.Bool("tls", false, "HTTPS mit selbstsigniertem Zertifikat aktivieren")
		certFile = flag.String("cert", "", "TLS-Zertifikat (PEM)")
		keyFile  = flag.String("key", "", "TLS-Schlüssel (PEM)")
		addUser  = flag.String("add-user", "", "Benutzer anlegen oder Passwort ändern und beenden")
		asAdmin  = flag.Bool("admin", true, "Der mit -add-user angelegte Benutzer ist Administrator")
		probe    = flag.String("probe", "", "SMB-Fähigkeiten eines Hosts prüfen und beenden")
		noAuth   = flag.Bool("no-auth", false, "Anmeldung abschalten (nur für abgeschottete Netze)")
		open     = flag.Bool("open", false, "Browser nach dem Start öffnen")
		devWeb   = flag.String("dev-web", "", "Oberfläche aus diesem Verzeichnis laden statt aus dem Binary")
		health   = flag.Bool("health", false, "Selbsttest: prüft den eigenen /health-Endpunkt und beendet sich mit 0 oder 1")
		showVer  = flag.Bool("version", false, "Version ausgeben")
	)
	flag.Usage = usage
	flag.Parse()

	// Umgebungsvariablen als Vorgabe, Schalter haben Vorrang. In einem
	// Container ist das der uebliche Weg: dort gibt es keine Kommandozeile,
	// die man mal eben anpasst, wohl aber environment:-Eintraege.
	envDefault(cfgPath, "SPEEDNAS_CONFIG")
	envDefault(listen, "SPEEDNAS_LISTEN")
	envDefault(dataDir, "SPEEDNAS_DATA")
	envDefault(certFile, "SPEEDNAS_TLS_CERT")
	envDefault(keyFile, "SPEEDNAS_TLS_KEY")
	envDefaultBool(useTLS, "SPEEDNAS_TLS")
	envDefaultBool(noAuth, "SPEEDNAS_NO_AUTH")

	if *showVer {
		fmt.Printf("SpeedNAS %s (%s, %s/%s)\n", version, runtime.Version(), runtime.GOOS, runtime.GOARCH)
		return
	}
	if *health {
		os.Exit(runHealthCheck(*listen, *useTLS || *certFile != ""))
	}
	if *probe != "" {
		runProbe(*probe)
		return
	}

	server.Version = version

	cfg, created, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("Konfiguration: %v", err)
	}
	if created {
		log.Printf("Neue Konfiguration angelegt: %s", cfg.Path())
	}

	if *listen != "" {
		cfg.Server.Listen = *listen
	}
	if *dataDir != "" {
		cfg.Server.DataDir = *dataDir
	}
	if *useTLS {
		cfg.Server.TLS.Enabled = true
		cfg.Server.TLS.SelfSign = *certFile == ""
	}
	if *certFile != "" {
		cfg.Server.TLS.Enabled = true
		cfg.Server.TLS.CertFile = *certFile
		cfg.Server.TLS.KeyFile = *keyFile
	}
	if *noAuth {
		cfg.Auth.Enabled = false
	}

	if *addUser != "" {
		if err := createUser(cfg, *addUser, *asAdmin); err != nil {
			log.Fatalf("Benutzer anlegen: %v", err)
		}
		fmt.Printf("Benutzer %q gespeichert.\n", *addUser)
		return
	}

	if err := cfg.Save(); err != nil {
		log.Printf("Warnung: Konfiguration nicht speicherbar: %v", err)
	}

	app, err := server.New(cfg, *devWeb)
	if err != nil {
		log.Fatalf("Start fehlgeschlagen: %v", err)
	}
	defer app.Close()

	if cfg.Auth.Enabled && len(cfg.Users()) == 0 {
		log.Printf("Noch kein Benutzer angelegt - die Oberfläche führt beim ersten Aufruf durch die Einrichtung.")
	}
	if len(cfg.Snapshot()) == 0 {
		log.Printf("Noch kein Speicherort eingerichtet - in der Oberfläche unter 'Standort hinzufügen'.")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if *open {
		go func() {
			time.Sleep(400 * time.Millisecond)
			openBrowser(localURL(cfg))
		}()
	}

	if err := app.Serve(ctx); err != nil {
		log.Fatalf("Server beendet: %v", err)
	}
	log.Printf("Auf Wiedersehen.")
}

// runHealthCheck fragt den eigenen /health-Endpunkt ab und liefert den
// Rückgabewert für Docker: 0 heißt gesund.
//
// Warum überhaupt eingebaut? Im "slim"-Image auf Basis scratch gibt es weder
// wget noch curl noch eine Shell - das Programm muss sich also selbst prüfen
// können, sonst gäbe es dort keinen Healthcheck.
func runHealthCheck(listen string, useTLS bool) int {
	host, port, err := net.SplitHostPort(strings.TrimSpace(listen))
	if err != nil {
		// Nur ein Port angegeben, etwa "8088"
		host, port = "", strings.TrimSpace(strings.TrimPrefix(listen, ":"))
	}
	// Auf 0.0.0.0 oder :: kann man nicht verbinden - das sind Lausch-, keine
	// Zieladressen.
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		host = "127.0.0.1"
	}
	if port == "" {
		port = "8088"
	}

	scheme := "http"
	client := &http.Client{Timeout: 4 * time.Second}
	if useTLS {
		scheme = "https"
		// Das selbstsignierte Zertifikat prüfen zu wollen, wäre hier sinnlos:
		// Wir reden mit uns selbst über die Loopback-Adresse.
		client.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
	}

	url := fmt.Sprintf("%s://%s/health", scheme, net.JoinHostPort(host, port))
	resp, err := client.Get(url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nicht erreichbar: %v\n", err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "unerwarteter Status %d\n", resp.StatusCode)
		return 1
	}
	return 0
}

// envDefault setzt einen Wert aus der Umgebung, sofern der Schalter leer blieb.
func envDefault(target *string, name string) {
	if *target == "" {
		if v, ok := os.LookupEnv(name); ok {
			*target = v
		}
	}
}

// envDefaultBool versteht 1, true, yes und on als "an".
func envDefaultBool(target *bool, name string) {
	if *target {
		return
	}
	v, ok := os.LookupEnv(name)
	if !ok {
		return
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		*target = true
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `SpeedNAS %s - Dateibrowser für Netzwerkspeicher

Aufruf:
  speednas [Optionen]

Häufige Fälle:
  speednas                          Server starten (Standard: Port 8088)
  speednas -add-user paul           Benutzer anlegen bzw. Passwort setzen
  speednas -probe 192.168.2.1       Prüfen, welche SMB-Version der Router spricht
  speednas -tls -listen :8443       Mit HTTPS starten
  speednas -open                    Starten und Browser öffnen

Umgebungsvariablen (werden von den Schaltern ueberstimmt):
  SPEEDNAS_CONFIG      Pfad zur Konfigurationsdatei
  SPEEDNAS_DATA        Datenverzeichnis
  SPEEDNAS_LISTEN      Adresse und Port
  SPEEDNAS_TLS         1 = HTTPS aktivieren
  SPEEDNAS_TLS_CERT    Zertifikatsdatei
  SPEEDNAS_TLS_KEY     Schluesseldatei
  SPEEDNAS_NO_AUTH     1 = Anmeldung abschalten

Optionen:
`, version)
	flag.PrintDefaults()
}

func runProbe(host string) {
	port := 445
	if h, p, ok := strings.Cut(host, ":"); ok {
		host = h
		fmt.Sscanf(p, "%d", &port)
	}
	fmt.Printf("Prüfe %s:%d ...\n\n", host, port)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	res := smbprobe.Probe(ctx, host, port, 6*time.Second)
	if !res.Reachable {
		fmt.Printf("  nicht erreichbar: %s\n", res.DialError)
		fmt.Printf("\n  Prüfe: Netzwerkspeicher im Router aktiviert? IP richtig? Firewall auf Port %d?\n", port)
		return
	}
	fmt.Printf("  TCP-Antwortzeit    %.1f ms\n", res.RTTms)
	if res.SMB2 {
		fmt.Printf("  SMB2/3             ja - %s\n", res.DialectName)
		fmt.Printf("  Signierung         aktiv=%v erzwungen=%v\n", res.SigningOn, res.SigningForce)
		fmt.Printf("  max. Leseblock     %d KiB\n", res.MaxReadSize/1024)
		fmt.Printf("  max. Schreibblock  %d KiB\n", res.MaxWriteSize/1024)
	} else {
		fmt.Printf("  SMB2/3             nein (%s)\n", res.SMB2Error)
	}
	if res.SMB1 {
		fmt.Printf("  SMB1               ja - %s\n", res.SMB1Dialect)
	} else {
		fmt.Printf("  SMB1               nein\n")
	}

	fmt.Println("\nEinschätzung:")
	switch {
	case res.SMB2:
		fmt.Println("  Der Server spricht SMB2 oder neuer. SpeedNAS kann sich verbinden,")
		fmt.Println("  und auch iPhone und Windows kommen damit grundsätzlich klar.")
	case res.SMB1:
		fmt.Println("  Der Server spricht ausschließlich SMB1.")
		fmt.Println("  Deshalb verweigern iOS-Dateien und der Windows-Explorer den Dienst -")
		fmt.Println("  beide haben SMB1 wegen Sicherheitslücken entfernt. SpeedNAS ebenfalls.")
		fmt.Println("  Lösungen: im Router SMB2 aktivieren bzw. Firmware aktualisieren, oder")
		fmt.Println("  den Speicher stattdessen per FTP einbinden.")
	default:
		fmt.Println("  Port offen, aber keine gültige SMB-Antwort. Läuft dort eine Dateifreigabe?")
	}
}

func createUser(cfg *config.Config, name string, admin bool) error {
	pw, err := readPassword("Passwort für " + name + ": ")
	if err != nil {
		return err
	}
	again, err := readPassword("Passwort wiederholen: ")
	if err != nil {
		return err
	}
	if pw != again {
		return fmt.Errorf("die Passwörter stimmen nicht überein")
	}
	if len(pw) < 8 {
		return fmt.Errorf("mindestens 8 Zeichen bitte")
	}
	hash, err := server.HashPassword(pw)
	if err != nil {
		return err
	}
	return cfg.UpsertUser(config.User{Name: name, Hash: hash, Admin: admin})
}

func readPassword(prompt string) (string, error) {
	fmt.Print(prompt)
	if term.IsTerminal(int(syscall.Stdin)) {
		b, err := term.ReadPassword(int(syscall.Stdin))
		fmt.Println()
		return string(b), err
	}
	// Kein Terminal (z. B. Skript): dann eben von der Standardeingabe.
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	return strings.TrimSpace(line), err
}

func localURL(cfg *config.Config) string {
	scheme := "http"
	if cfg.Server.TLS.Enabled {
		scheme = "https"
	}
	addr := cfg.Server.Listen
	if strings.HasPrefix(addr, ":") {
		addr = "localhost" + addr
	}
	return scheme + "://" + addr
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}
