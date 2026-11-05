// Package fonts provides embedded OFL faces. Callers own parsed fonts/faces.
package fonts

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"golang.org/x/image/font/opentype"
	"strings"
)

//go:embed data/*.ttf data/*-OFL.txt data/*-LICENSE.txt data/revision.txt
var data embed.FS

func Bytes(id string) []byte {
	id = strings.ToLower(strings.NewReplacer(" ", "", "-", "", "_", "").Replace(id))
	b, err := data.ReadFile("data/" + id + ".ttf")
	if err != nil {
		b, _ = data.ReadFile("data/notosans.ttf")
	}
	return b
}
func Parse(id string) (*opentype.Font, error) { return opentype.Parse(Bytes(id)) }

// Version covers every bundled face and its source revision for master caching.
func Version() string {
	h := sha256.New()
	entries, _ := data.ReadDir("data")
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".ttf") || e.Name() == "revision.txt" {
			b, _ := data.ReadFile("data/" + e.Name())
			h.Write([]byte(e.Name()))
			h.Write(b)
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}
