// Package fonts provides embedded OFL faces. Callers own parsed fonts/faces.
package fonts

import (
	"embed"
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
