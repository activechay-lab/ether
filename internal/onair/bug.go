package onair

import (
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/onair/scene"
)

// configureBug runs during resource preparation, never on a video frame tick.
func configureBug(d *scene.Document, b core.BugSettings, logo string) {
	scale := 1.0
	if b.Size == "small" {
		scale = .75
	}
	if b.Size == "large" {
		scale = 1.3
	}
	w, h := 288*scale, 100*scale
	x, y := 1728-w, 972-h
	if strings.HasSuffix(b.Position, "Left") {
		x = 192
	}
	if strings.HasPrefix(b.Position, "top") {
		y = 108
	}
	mark := scene.Layer{ID: "Title", Type: "text", Text: "{channel.name}", Y: 64.0, Font: "{type.display}", Size: 48.0, Weight: 600, Track: 2.0, Fill: "#eeeeee", MaxWidth: 288, Fit: "shrink", Glow: 2}
	if b.Style == "original" {
		mark.Fill = "{accent}"
	}
	if b.Source == "logo" && logo != "" {
		mark = scene.Layer{ID: "station-logo", Type: "image", Src: logo, W: 288.0, H: 100.0, Fit: "contain", Glow: 2}
		if b.Style == "monochrome" {
			mark.Tint = "#eeeeee"
			mark.TintBlend = "silhouette"
		}
	}
	shadow := mark
	shadow.ID = "bug-shadow"
	shadow.Fill = "#000000"
	shadow.Tint = "#000000"
	shadow.Glow = 0
	shadow.Blur = 2
	shadow.Y = 2.0
	if mark.Type == "text" {
		shadow.Y = 66.0
	}
	d.Layers = []scene.Layer{{ID: "station-bug", Type: "group", X: x, Y: y, Scale: scale, Opacity: *b.Opacity, Layers: []scene.Layer{shadow, mark}}}
	d.Post = nil
}
