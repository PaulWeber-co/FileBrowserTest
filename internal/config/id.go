package config

import (
	"crypto/rand"
	"encoding/base32"
	"strings"
)

// NewID erzeugt eine kurze, URL-taugliche Kennung.
func NewID() string {
	var b [10]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b[:]))
}
