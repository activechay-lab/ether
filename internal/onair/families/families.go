// Package families embeds the built-in scene documents; families are data.
package families

import (
	"embed"
	"encoding/json"
	"fmt"
	"github.com/braedonsaunders/ether/internal/onair/scene"
	"strings"
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
	out := map[string][]string{}
	entries, _ := documents.ReadDir("documents")
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".controls.json") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".json")
		for _, family := range []string{"grunge", "aqua", "flat", "late-night", "personal", "cinema", "retro", "kids", "clean", "signal"} {
			if strings.HasPrefix(name, family+"-") {
				out[family] = append(out[family], strings.TrimPrefix(name, family+"-"))
				break
			}
		}
	}
	return out
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
	b.Tokens["glow"] = "#17254a"
	b.Tokens["words.tonight"] = "TONIGHT"
	b.Tokens["words.now"] = "NOW PLAYING"
	b.Tokens["words.break"] = "INTERMISSION"
	b.Tokens["words.back"] = "NOW RESUMING"
	b.Tokens["words.offair"] = "OFF AIR"
	b.Slots["viewer.name"] = "Alex"
	b.Slots["viewer.greeting"] = "Good evening"
	b.Slots["now.title"] = "Carnival of Souls"
	b.Slots["next.reason"] = "Picked for your evening"
	for i, title := range []string{"The Grand Budapest Hotel", "Moonrise Kingdom", "Fantastic Mr. Fox", "The Royal Tenenbaums"} {
		b.Slots[fmt.Sprintf("list[%d].title", i)] = title
		b.Slots[fmt.Sprintf("list[%d].time", i)] = fmt.Sprintf("%d:00", 7+i)
		b.Slots[fmt.Sprintf("list[%d].meta", i)] = "Tonight's selection"
	}
	switch family {
	case "grunge":
		b.Tokens["bg"] = "#141021"
		b.Tokens["accent"] = "#edff38"
		b.Tokens["glow"] = "#ed4c99"
		b.Tokens["text"] = "#ffffff"
		b.Tokens["type.display"] = "archivoblack"
		b.Tokens["type.mono"] = "specialelite"
		b.Slots["channel.name"] = "RIOT 90"
	case "aqua":
		b.Tokens["bg"] = "#031d37"
		b.Tokens["accent"] = "#009edb"
		b.Tokens["glow"] = "#8cf5ff"
		b.Tokens["text"] = "#ffffff"
		b.Tokens["type.display"] = "spacegrotesk"
		b.Tokens["type.mono"] = "inter"
		b.Slots["channel.name"] = "AQUAWAVE 00"
	case "flat":
		b.Tokens["bg"] = "#102a35"
		b.Tokens["accent"] = "#f15440"
		b.Tokens["glow"] = "#ffd66e"
		b.Tokens["text"] = "#ffffff"
		b.Tokens["type.display"] = "inter"
		b.Tokens["type.mono"] = "inter"
		b.Slots["channel.name"] = "OFFSET 10"
	case "signal":
		b.Slots["channel.name"] = "Ether"
		b.Slots["station.id"] = "ETHER"
	case "cinema":
		b.Tokens["bg"] = "#070d1d"
		b.Tokens["accent"] = "#d8b26e"
		b.Tokens["text"] = "#f4eedf"
		b.Tokens["type.display"] = "fraunces"
		b.Tokens["type.mono"] = "inter"
		b.Slots["channel.name"] = "MERIDIAN"
	case "kids":
		b.Tokens["bg"] = "#6b2bd9"
		b.Tokens["accent"] = "#ff7a00"
		b.Tokens["text"] = "#ffffff"
		b.Tokens["glow"] = "#7ce03a"
		b.Tokens["type.display"] = "bungee"
		b.Tokens["type.mono"] = "fredoka"
		b.Tokens["words.tagline"] = "Totally gooey TV"
		b.Tokens["words.break"] = "WE'LL BE RIGHT BACK!"
		b.Tokens["words.back"] = "AND NOW BACK TO"
		b.Tokens["words.offair"] = "BLORP! is napping"
		b.Slots["channel.name"] = "BLORP!"
	case "retro":
		b.Tokens["bg"] = "#12002b"
		b.Tokens["accent"] = "#ff4fd8"
		b.Tokens["glow"] = "#22e6ff"
		b.Tokens["text"] = "#ffffff"
		b.Tokens["type.display"] = "orbitron"
		b.Tokens["type.mono"] = "vt323"
		b.Slots["channel.name"] = "NEON 84"
	case "personal":
		b.Tokens["bg"] = "#100b23"
		b.Tokens["accent"] = "#9b85ff"
		b.Tokens["glow"] = "#ff5ca8"
		b.Tokens["text"] = "#ffffff"
		b.Tokens["type.display"] = "spacegrotesk"
		b.Tokens["type.mono"] = "inter"
		b.Slots["channel.name"] = "Alex TV"
		b.Tokens["words.break"] = "Back in a moment"
		b.Tokens["words.tagline"] = "This channel is made for you."
	case "clean":
		b.Tokens["type.display"] = "inter"
		b.Tokens["type.mono"] = "inter"
		b.Tokens["words.break"] = "Back in a moment"
	}

	if raw, err := Controls(family); err == nil {
		var controls []struct {
			ID    string `json:"id"`
			Value any    `json:"value"`
		}
		if json.Unmarshal(raw, &controls) == nil {
			for _, c := range controls {
				b.Tokens["control."+c.ID] = c.Value
			}
		}
	}
	return b
}

func Controls(id string) ([]byte, error) {
	return documents.ReadFile("documents/" + id + ".controls.json")
}
func Description(id string) (string, string, []string) {
	data := map[string][3]string{
		"clean":      {"Clean", "Room to breathe. Space to discover.", "Clean"},
		"kids":       {"Kids", "Big colours for little adventures.", "Kids"},
		"late-night": {"Late night", "Stay a little longer after dark.", "Late night"},
		"cinema":     {"Cinema", "Every evening deserves a premiere.", "Cinema"},
		"retro":      {"Retro 80", "Neon dreams on a midnight dial.", "80s"},
		"grunge":     {"Grunge 90", "Turn it up. Tear the edges.", "90s"},
		"aqua":       {"Aqua 00", "A bright new world behind glass.", "00s"},
		"flat":       {"Flat 10", "Bold blocks. Perfect timing.", "10s"},
		"personal":   {"Personal", "Your own corner of the airwaves.", "Personal"},
	}
	v, ok := data[id]
	if !ok {
		return id, "Make these airwaves your own.", []string{"Retro"}
	}
	return v[0], v[1], []string{v[2]}
}
