// Package repocheck enthält Prüfungen an den Projektdateien selbst.
package repocheck

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot sucht das Verzeichnis mit der go.mod-Datei.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("go.mod nicht gefunden")
	return ""
}

// TestShellSkripteHabenUnixZeilenenden verhindert die Rückkehr eines Fehlers,
// der eine ganze Fehlersuche gekostet hat.
//
// Enthält ein Shell-Skript Windows-Zeilenenden, wird aus dem Shebang
// "#!/bin/sh" ein "#!/bin/sh\r". Der Linux-Kernel im Container sucht dann ein
// Programm dieses Namens und meldet:
//
//	exec /usr/local/bin/docker-entrypoint.sh: no such file or directory
//
// Gemeint ist der Interpreter, nicht das Skript - eine der irreführendsten
// Meldungen überhaupt. .gitattributes verhindert das beim Auschecken, dieser
// Test verhindert, dass jemand eine solche Datei überhaupt einträgt.
func TestShellSkripteHabenUnixZeilenenden(t *testing.T) {
	root := repoRoot(t)
	geprueft := 0

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", "node_modules", "dist":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".sh") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		geprueft++
		rel, _ := filepath.Rel(root, path)
		if bytes.Contains(data, []byte("\r")) {
			t.Errorf("%s enthält Wagenrücklauf-Zeichen (CRLF). "+
				"Im Container schlägt das Skript damit fehl. "+
				"Beheben mit: tr -d '\\r' < %s > tmp && mv tmp %s", rel, rel, rel)
		}
		if !bytes.HasPrefix(data, []byte("#!")) {
			t.Errorf("%s hat keine Shebang-Zeile", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if geprueft == 0 {
		t.Fatal("keine Shell-Skripte gefunden - stimmt der Suchpfad?")
	}
	t.Logf("%d Shell-Skripte geprüft", geprueft)
}

// TestGitattributesErzwingtZeilenenden stellt sicher, dass die Regel, die das
// Problem an der Wurzel verhindert, nicht versehentlich entfernt wird.
func TestGitattributesErzwingtZeilenenden(t *testing.T) {
	root := repoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, ".gitattributes"))
	if err != nil {
		t.Fatalf(".gitattributes fehlt: %v", err)
	}
	inhalt := string(data)
	for _, muss := range []string{"*.sh", "eol=lf"} {
		if !strings.Contains(inhalt, muss) {
			t.Errorf(".gitattributes enthält %q nicht mehr", muss)
		}
	}
}

// TestDockerfileBereinigtZeilenenden prüft den zweiten Schutzwall: Selbst ein
// bereits schief ausgecheckter Arbeitsordner soll ein brauchbares Image ergeben.
func TestDockerfileBereinigtZeilenenden(t *testing.T) {
	root := repoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `tr -d '\r'`) {
		t.Error("Das Dockerfile bereinigt die Zeilenenden des Startskripts nicht mehr")
	}
}
