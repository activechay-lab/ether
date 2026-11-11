package scene

import (
	"encoding/json"
	"strings"
)

// IsLiveSlotKey reports whether a scene binding key carries dynamic programme,
// lineup, schedule, or viewer state that must never be baked into per-title
// cached masters on disk.
func IsLiveSlotKey(k string) bool {
	return strings.HasPrefix(k, "next.") ||
		strings.HasPrefix(k, "list[") ||
		strings.HasPrefix(k, "now.") ||
		strings.HasPrefix(k, "viewer.") ||
		strings.HasPrefix(k, "resume.") ||
		strings.HasPrefix(k, "lineup.")
}

// HasLiveSlots reports whether d's layer tree references any live programme,
// lineup, or viewer slot (or a daytime-adaptable tonight heading).
func HasLiveSlots(d Document) bool {
	if strings.HasPrefix(d.Moment, "plugin:") || strings.HasPrefix(d.Moment, "screen.") {
		return false
	}
	for _, l := range d.Layers {
		if layerHasLiveSlot(l, d.Moment) {
			return true
		}
	}
	return false
}

// SplitSlotLayers separates a live-slot scene into a slot-free animated base
// document (cached once per look) and a live foreground overlay document
// (composited in real time at air time). If animated procedural layers or
// non-normal blend modes sit above a slot layer, splittable is false and the
// caller renders the full scene live each airing with no disk save.
func SplitSlotLayers(d Document) (base Document, overlay Document, splittable bool) {
	if !HasLiveSlots(d) || len(d.Layers) == 0 {
		return d, Document{}, false
	}
	clone := cloneDocument(d)

	// Built-in retro/grunge/aqua/flat scenes wrap all layers in a single
	// top-level envelope group (fade-in / fade-out). Split inside that envelope
	// when its blend and filter properties are plain alpha.
	if len(clone.Layers) == 1 && isPlainEnvelope(clone.Layers[0]) {
		env := clone.Layers[0]
		idx := firstLiveSlotIndex(env.Layers, clone.Moment)
		if idx <= 0 || !validOverlaySuffix(env.Layers[idx:]) {
			return d, Document{}, false
		}
		base = cloneDocument(clone)
		base.Slots = nil
		base.Layers[0].Layers = append([]Layer(nil), env.Layers[:idx]...)

		overlay = cloneDocument(clone)
		overlay.Slots = nil
		overlay.Audio = nil
		overlay.Post = nil
		overlay.Layers[0].Layers = append([]Layer(nil), env.Layers[idx:]...)
		if base.Validate() != nil || overlay.Validate() != nil {
			return d, Document{}, false
		}
		return base, overlay, true
	}

	idx := firstLiveSlotIndex(clone.Layers, clone.Moment)
	if idx <= 0 || !validOverlaySuffix(clone.Layers[idx:]) {
		return d, Document{}, false
	}
	base = cloneDocument(clone)
	base.Slots = nil
	base.Layers = append([]Layer(nil), clone.Layers[:idx]...)

	overlay = cloneDocument(clone)
	overlay.Slots = nil
	overlay.Audio = nil
	overlay.Post = nil
	overlay.Layers = append([]Layer(nil), clone.Layers[idx:]...)
	if base.Validate() != nil || overlay.Validate() != nil {
		return d, Document{}, false
	}
	return base, overlay, true
}

// StandbyBase derives a clean, text-free animated loop document from a look's
// break or ident scene so holding on a warming segment never shows a still.
func StandbyBase(d Document) (Document, bool) {
	clone := cloneDocument(d)
	clone.Moment = "standby"
	clone.Slots = nil
	var stripText func([]Layer) []Layer
	stripText = func(ls []Layer) []Layer {
		var out []Layer
		for _, l := range ls {
			if l.Type == "text" || l.Type == "art" || layerRefsAnySlot(l) {
				continue
			}
			if l.Type == "group" {
				l.Layers = stripText(l.Layers)
				if len(l.Layers) == 0 {
					continue
				}
			}
			out = append(out, l)
		}
		return out
	}
	clone.Layers = stripText(clone.Layers)
	if len(clone.Layers) == 0 || clone.Validate() != nil {
		return Document{}, false
	}
	return clone, true
}

// RewriteLineupHeading updates the baked "TONIGHT" / "{words.tonight}" heading
// on lineup overlays so daytime airings read "COMING UP" while reusing the
// look's single cached base master.
func RewriteLineupHeading(d *Document, heading string) {
	if d == nil || heading == "" || d.Moment != "tonight" {
		return
	}
	var walk func([]Layer)
	walk = func(ls []Layer) {
		for i := range ls {
			if ls[i].Type == "text" {
				if s, ok := ls[i].Text.(string); ok {
					trimmed := strings.TrimSpace(s)
					if strings.EqualFold(trimmed, "TONIGHT") || trimmed == "{words.tonight}" {
						ls[i].Text = heading
					}
				}
			}
			walk(ls[i].Layers)
		}
	}
	walk(d.Layers)
}

func isPlainEnvelope(l Layer) bool {
	return l.Type == "group" &&
		(l.Blend == "" || l.Blend == "normal") &&
		l.Blur == 0 &&
		l.Glow == 0 &&
		l.Clip == "" &&
		l.Repeat <= 1 &&
		l.Reveal == nil &&
		len(l.Behave) == 0 &&
		len(l.Layers) > 1
}

func firstLiveSlotIndex(ls []Layer, moment string) int {
	for i, l := range ls {
		if layerHasLiveSlot(l, moment) {
			return i
		}
	}
	return -1
}

func validOverlaySuffix(ls []Layer) bool {
	if len(ls) == 0 {
		return false
	}
	for _, l := range ls {
		if l.Blend != "" && l.Blend != "normal" {
			return false
		}
		if hasProceduralLayer(l) {
			return false
		}
	}
	return true
}

func hasProceduralLayer(l Layer) bool {
	switch l.Type {
	case "noise", "blob", "particles", "grid":
		return true
	}
	for _, child := range l.Layers {
		if hasProceduralLayer(child) {
			return true
		}
	}
	return false
}

func layerHasLiveSlot(l Layer, moment string) bool {
	if moment == "tonight" && l.Type == "text" {
		if s, ok := l.Text.(string); ok {
			trimmed := strings.TrimSpace(s)
			if strings.EqualFold(trimmed, "TONIGHT") || trimmed == "{words.tonight}" {
				return true
			}
		}
	}
	for _, v := range []Value{l.X, l.Y, l.W, l.H, l.Scale, l.ScaleX, l.ScaleY, l.Rotate, l.Opacity, l.Fill, l.Stroke, l.StrokeWidth, l.Text, l.Font, l.Size, l.Track, l.Src, l.Tint} {
		if valueHasLiveSlot(v) {
			return true
		}
	}
	for _, child := range l.Layers {
		if layerHasLiveSlot(child, moment) {
			return true
		}
	}
	return false
}

func layerRefsAnySlot(l Layer) bool {
	for _, v := range []Value{l.Text, l.Src} {
		if s, ok := v.(string); ok && strings.Contains(s, "{") {
			return true
		}
	}
	for _, child := range l.Layers {
		if layerRefsAnySlot(child) {
			return true
		}
	}
	return false
}

func valueHasLiveSlot(v Value) bool {
	switch x := v.(type) {
	case string:
		rem := x
		for {
			start := strings.Index(rem, "{")
			if start < 0 {
				return false
			}
			end := strings.Index(rem[start:], "}")
			if end < 0 {
				return false
			}
			key := rem[start+1 : start+end]
			if IsLiveSlotKey(key) {
				return true
			}
			rem = rem[start+end+1:]
		}
	case map[string]any:
		for _, child := range x {
			if valueHasLiveSlot(child) {
				return true
			}
		}
	case []any:
		for _, child := range x {
			if valueHasLiveSlot(child) {
				return true
			}
		}
	}
	return false
}

func cloneDocument(d Document) Document {
	raw, _ := json.Marshal(d)
	var out Document
	_ = json.Unmarshal(raw, &out)
	return out
}
