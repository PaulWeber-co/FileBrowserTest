package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/PaulWeber-co/FileBrowserTest/internal/vfs"
)

type apiError struct {
	Error  string `json:"error"`
	Detail string `json:"detail,omitempty"`
	Code   string `json:"code,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("JSON-Antwort abgebrochen: %v", err)
	}
}

// httpStatus übersetzt VFS-Fehler in passende HTTP-Codes, damit die
// Oberfläche sinnvolle Meldungen zeigen kann statt "500".
func httpStatus(err error) (int, string) {
	switch {
	case errors.Is(err, vfs.ErrNotFound):
		return http.StatusNotFound, "not_found"
	case errors.Is(err, vfs.ErrExists):
		return http.StatusConflict, "exists"
	case errors.Is(err, vfs.ErrPermission):
		return http.StatusForbidden, "permission"
	case errors.Is(err, vfs.ErrNotSupported):
		return http.StatusNotImplemented, "unsupported"
	case errors.Is(err, vfs.ErrIsDir):
		return http.StatusBadRequest, "is_dir"
	case errors.Is(err, vfs.ErrNotDir):
		return http.StatusBadRequest, "not_dir"
	}
	return http.StatusBadGateway, "backend"
}

// friendly übersetzt technische Fehler in eine verständliche Meldung.
func friendly(err error) string {
	switch {
	case errors.Is(err, vfs.ErrNotFound):
		return "Datei oder Ordner nicht gefunden."
	case errors.Is(err, vfs.ErrExists):
		return "Ein Eintrag mit diesem Namen existiert bereits."
	case errors.Is(err, vfs.ErrPermission):
		return "Zugriff verweigert - stimmen Benutzer und Passwort am Netzwerkspeicher?"
	case errors.Is(err, vfs.ErrNotSupported):
		return "Dieses Protokoll unterstützt die Aktion nicht."
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "connection refused"):
		return "Verbindung abgelehnt - läuft der Dienst und stimmt der Port?"
	case strings.Contains(msg, "i/o timeout"), strings.Contains(msg, "deadline exceeded"):
		return "Zeitüberschreitung - der Netzwerkspeicher antwortet nicht."
	case strings.Contains(msg, "no such host"):
		return "Host nicht gefunden - Name oder IP prüfen."
	case strings.Contains(msg, "STATUS_LOGON_FAILURE"):
		return "Anmeldung fehlgeschlagen - Benutzername oder Passwort falsch."
	case strings.Contains(msg, "STATUS_ACCESS_DENIED"):
		return "Der Server verweigert den Zugriff auf diese Freigabe."
	case strings.Contains(msg, "STATUS_BAD_NETWORK_NAME"):
		return "Freigabename unbekannt - Schreibweise prüfen."
	}
	return msg
}

func fail(w http.ResponseWriter, err error) {
	status, code := httpStatus(err)
	writeJSON(w, status, apiError{Error: friendly(err), Detail: err.Error(), Code: code})
}

func failWith(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, apiError{Error: msg})
}

// decodeBody liest einen JSON-Rumpf mit Größenbegrenzung (Standard 1 MiB).
func decodeBody(w http.ResponseWriter, r *http.Request, v any, limit ...int64) error {
	max := int64(1 << 20)
	if len(limit) > 0 && limit[0] > 0 {
		max = limit[0]
	}
	r.Body = http.MaxBytesReader(w, r.Body, max)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("Anfrage nicht lesbar: %w", err)
	}
	return nil
}

func queryInt(r *http.Request, name string, def int) int {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func queryBool(r *http.Request, name string) bool {
	v := strings.ToLower(r.URL.Query().Get(name))
	return v == "1" || v == "true" || v == "yes"
}

// contentDisposition kodiert Dateinamen so, dass auch Umlaute und Emojis in
// jedem Browser ankommen (RFC 5987).
func contentDisposition(kind, name string) string {
	ascii := make([]rune, 0, len(name))
	for _, r := range name {
		if r < 32 || r > 126 || r == '"' || r == '\\' {
			ascii = append(ascii, '_')
		} else {
			ascii = append(ascii, r)
		}
	}
	var enc strings.Builder
	for _, b := range []byte(name) {
		if isURLSafe(b) {
			enc.WriteByte(b)
		} else {
			fmt.Fprintf(&enc, "%%%02X", b)
		}
	}
	return fmt.Sprintf("%s; filename=\"%s\"; filename*=UTF-8''%s", kind, string(ascii), enc.String())
}

func isURLSafe(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9':
		return true
	}
	switch b {
	case '-', '.', '_', '~':
		return true
	}
	return false
}

// clientIP ermittelt die Gegenstelle, ohne blind auf Proxy-Header zu vertrauen.
func clientIP(r *http.Request) string {
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i > 0 {
		host = host[:i]
	}
	return strings.Trim(host, "[]")
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit && exp < 4; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTP"[exp])
}
