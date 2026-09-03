// Package web enthält die eingebettete Weboberfläche.
//
// Alles liegt im Binary: eine einzelne Datei kopieren, starten, fertig.
package web

import (
	"embed"
	"io/fs"
)

//go:embed index.html login.html share.html manifest.webmanifest sw.js css js icons
var files embed.FS

// Assets liefert das eingebettete Dateisystem der Oberfläche.
func Assets() fs.FS { return files }
