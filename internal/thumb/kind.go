package thumb

import (
	"path"
	"strings"
)

// Kind ist die grobe Klasse einer Datei - sie steuert Symbol, Vorschau und
// den Viewer in der Oberfläche.
type Kind string

const (
	KindImage   Kind = "image"
	KindHEIC    Kind = "heic"
	KindVideo   Kind = "video"
	KindAudio   Kind = "audio"
	KindPDF     Kind = "pdf"
	KindText    Kind = "text"
	KindCode    Kind = "code"
	KindArchive Kind = "archive"
	KindDoc     Kind = "doc"
	KindOther   Kind = "other"
)

var extKind = map[string]Kind{
	"jpg": KindImage, "jpeg": KindImage, "png": KindImage, "gif": KindImage,
	"bmp": KindImage, "webp": KindImage, "tif": KindImage, "tiff": KindImage,
	"svg": KindImage, "ico": KindImage, "avif": KindImage,

	"heic": KindHEIC, "heif": KindHEIC,

	"mp4": KindVideo, "m4v": KindVideo, "mov": KindVideo, "mkv": KindVideo,
	"avi": KindVideo, "webm": KindVideo, "wmv": KindVideo, "flv": KindVideo,
	"mpg": KindVideo, "mpeg": KindVideo, "3gp": KindVideo, "ts": KindVideo,

	"mp3": KindAudio, "m4a": KindAudio, "aac": KindAudio, "flac": KindAudio,
	"wav": KindAudio, "ogg": KindAudio, "oga": KindAudio, "opus": KindAudio,
	"wma": KindAudio, "aiff": KindAudio,

	"pdf": KindPDF,

	"txt": KindText, "md": KindText, "log": KindText, "csv": KindText,
	"ini": KindText, "cfg": KindText, "conf": KindText, "nfo": KindText,
	"srt": KindText, "vtt": KindText,

	"go": KindCode, "js": KindCode, "mjs": KindCode, "ts_": KindCode,
	"tsx": KindCode, "jsx": KindCode, "py": KindCode, "rb": KindCode,
	"java": KindCode, "c": KindCode, "h": KindCode, "cpp": KindCode,
	"hpp": KindCode, "cs": KindCode, "php": KindCode, "sh": KindCode,
	"bat": KindCode, "ps1": KindCode, "sql": KindCode, "html": KindCode,
	"htm": KindCode, "css": KindCode, "json": KindCode, "xml": KindCode,
	"yaml": KindCode, "yml": KindCode, "toml": KindCode, "rs": KindCode,
	"swift": KindCode, "kt": KindCode, "lua": KindCode, "pl": KindCode,

	"zip": KindArchive, "rar": KindArchive, "7z": KindArchive, "tar": KindArchive,
	"gz": KindArchive, "bz2": KindArchive, "xz": KindArchive, "tgz": KindArchive,
	"iso": KindArchive, "dmg": KindArchive,

	"doc": KindDoc, "docx": KindDoc, "xls": KindDoc, "xlsx": KindDoc,
	"ppt": KindDoc, "pptx": KindDoc, "odt": KindDoc, "ods": KindDoc,
	"odp": KindDoc, "rtf": KindDoc, "epub": KindDoc,
}

// Ext liefert die kleingeschriebene Dateiendung ohne Punkt.
func Ext(name string) string {
	e := strings.ToLower(path.Ext(name))
	return strings.TrimPrefix(e, ".")
}

// KindOf klassifiziert eine Datei anhand ihrer Endung.
func KindOf(name string) Kind {
	if k, ok := extKind[Ext(name)]; ok {
		return k
	}
	return KindOther
}

// CanThumb meldet, ob für diese Datei überhaupt eine Vorschau möglich ist.
// SVG wird bewusst ausgenommen: die zeigt der Browser direkt an.
func CanThumb(name string, hasFFmpeg bool) bool {
	switch KindOf(name) {
	case KindImage:
		e := Ext(name)
		return e != "svg" && e != "ico" && e != "avif"
	case KindHEIC, KindVideo:
		return hasFFmpeg
	}
	return false
}

var extMime = map[string]string{
	"jpg": "image/jpeg", "jpeg": "image/jpeg", "png": "image/png",
	"gif": "image/gif", "webp": "image/webp", "bmp": "image/bmp",
	"tif": "image/tiff", "tiff": "image/tiff", "svg": "image/svg+xml",
	"ico": "image/x-icon", "avif": "image/avif",
	"heic": "image/heic", "heif": "image/heif",

	"mp4": "video/mp4", "m4v": "video/x-m4v", "mov": "video/quicktime",
	"mkv": "video/x-matroska", "webm": "video/webm", "avi": "video/x-msvideo",
	"3gp": "video/3gpp", "ts": "video/mp2t", "mpg": "video/mpeg", "mpeg": "video/mpeg",

	"mp3": "audio/mpeg", "m4a": "audio/mp4", "aac": "audio/aac",
	"flac": "audio/flac", "wav": "audio/wav", "ogg": "audio/ogg",
	"oga": "audio/ogg", "opus": "audio/opus",

	"pdf": "application/pdf",
	"zip": "application/zip", "7z": "application/x-7z-compressed",
	"rar": "application/vnd.rar", "tar": "application/x-tar",
	"gz": "application/gzip",

	"txt": "text/plain; charset=utf-8", "md": "text/plain; charset=utf-8",
	"log": "text/plain; charset=utf-8", "csv": "text/csv; charset=utf-8",
	"json": "application/json; charset=utf-8", "xml": "application/xml; charset=utf-8",
	"html": "text/html; charset=utf-8", "htm": "text/html; charset=utf-8",
	"css": "text/css; charset=utf-8", "js": "text/javascript; charset=utf-8",

	"doc":  "application/msword",
	"docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	"xls":  "application/vnd.ms-excel",
	"xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	"ppt":  "application/vnd.ms-powerpoint",
	"pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
	"epub": "application/epub+zip",
}

// MimeType rät den Inhaltstyp anhand der Endung.
func MimeType(name string) string {
	if m, ok := extMime[Ext(name)]; ok {
		return m
	}
	switch KindOf(name) {
	case KindText, KindCode:
		return "text/plain; charset=utf-8"
	}
	return "application/octet-stream"
}

// InlineSafe meldet, ob der Browser die Datei gefahrlos direkt anzeigen darf.
// HTML und SVG werden bewusst ausgeschlossen: sie können Skripte enthalten
// und würden im Ursprung der Anwendung ausgeführt.
func InlineSafe(name string) bool {
	switch Ext(name) {
	case "html", "htm", "svg", "xhtml", "xml":
		return false
	}
	switch KindOf(name) {
	case KindImage, KindVideo, KindAudio, KindPDF, KindText, KindCode:
		return true
	}
	return false
}

func isJPEG(name string) bool {
	e := Ext(name)
	return e == "jpg" || e == "jpeg"
}
