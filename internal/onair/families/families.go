// Package families embeds the built-in scene documents; families are data.
package families

import (
	"embed"
	"fmt"
	"github.com/braedonsaunders/ether/internal/onair/scene"
)

//go:embed documents/*.json
var documents embed.FS

func Get(family, moment string) (scene.Document, error) {
	b, e := documents.ReadFile("documents/" + family + "-" + moment + ".json")
	if e != nil {
		return scene.Document{}, fmt.Errorf("family moment: %w", e)
	}
	return scene.Decode(b)
}
func Names() map[string][]string {
	return map[string][]string{"signal": {"screen.card", "screen.static", "screen.bars", "screen.brb", "screen.testcard"}, "clean": {"ident", "offair"}, "late-night": {"ident", "upnext", "offair"}}
}
func Defaults(family string) scene.Bindings {
	b := scene.Bindings{Tokens: map[string]any{"bg": "#060508", "text": "#e9e3ce", "accent": "#c8102e", "moon": "#ded9c5", "fog": "#73828a", "type.display": "playfairdisplay", "type.mono": "specialelite", "energy": 1.0, "seed": 7.0, "textures.grain": .15, "words.upNext": "UP NEXT", "words.tagline": "A little more to discover"}, Slots: map[string]string{"screen.line1": "Reconnecting…", "screen.line2": "Programming will resume shortly", "station.id": "ETHER", "channel.name": "NOCTURNE", "next.title": "The House on the Hill", "next.meta": "1963 · Mystery · 1h 52m", "next.logline": "Some doors were never meant to open.", "next.time": "11:30", "offair.message": "Back at dusk · 8 PM"}}
	if family == "clean" {
		b.Tokens["bg"] = "#0b1320"
		b.Tokens["text"] = "#f2f5fa"
		b.Tokens["accent"] = "#78b9e8"
		b.Slots["channel.name"] = "ETHER"
		b.Slots["offair.message"] = "Programming resumes at 8 PM"
	}
	return b
}
