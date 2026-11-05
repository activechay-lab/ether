// Package onair owns owner looks, assets, reels and background master clips.
package onair

import (
	"encoding/json"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/onair/scene"
	"time"
)

type Brand struct {
	Palette struct {
		Background string `json:"background"`
		Accent     string `json:"accent"`
		Text       string `json:"text"`
		Glow       string `json:"glow"`
	} `json:"palette"`
	Type struct {
		Display string `json:"display"`
		Body    string `json:"body"`
	} `json:"type"`
	Energy   float64           `json:"energy"`
	Textures []string          `json:"textures"`
	Sting    string            `json:"sting"`
	Volume   float64           `json:"volume"`
	Seed     uint32            `json:"seed"`
	Tagline  string            `json:"tagline"`
	Words    map[string]string `json:"words"`
	Language string            `json:"language"`
	Logo     string            `json:"logo,omitempty"`
}
type Moment struct {
	Duration float64 `json:"duration"`
	Poster   float64 `json:"poster"`
}
type Look struct {
	Bug        *core.BugSettings         `json:"bug,omitempty"`
	ID         string                    `json:"id"`
	Name       string                    `json:"name"`
	Family     string                    `json:"family"`
	Brand      Brand                     `json:"brand"`
	Moments    map[string]Moment         `json:"moments"`
	Layers     map[string]map[string]any `json:"layers,omitempty"`
	Documents  map[string]scene.Document `json:"documents,omitempty"`
	ChannelIDs []string                  `json:"channelIds"`
	Pack       *Pack                     `json:"pack,omitempty"`
	Render     *Progress                 `json:"render,omitempty"`
}
type Pack struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type Progress struct {
	Done  int `json:"done"`
	Total int `json:"total"`
}
type Asset struct {
	Digest  string   `json:"digest"`
	Name    string   `json:"name"`
	Kind    string   `json:"kind"`
	Size    int64    `json:"size"`
	License string   `json:"license,omitempty"`
	UsedBy  []string `json:"usedBy"`
	URL     string   `json:"url,omitempty"`
	MIME    string   `json:"-"`
}
type Reel struct {
	Anchor        time.Time  `json:"-"`
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Order         string     `json:"order"`
	RepeatHours   float64    `json:"repeatHours"`
	LoudnessMatch bool       `json:"loudnessMatch"`
	TrimSilence   bool       `json:"trimSilence"`
	Clips         []ReelClip `json:"clips"`
	ChannelIDs    []string   `json:"channelIds"`
}
type ReelClip struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	DurationMS   int64   `json:"durationMs"`
	SourceDigest string  `json:"sourceDigest,omitempty"`
	Status       string  `json:"status"`
	Progress     float64 `json:"progress,omitempty"`
	Size         int64   `json:"size,omitempty"`
	Preview      string  `json:"preview,omitempty"`
	Error        string  `json:"error,omitempty"`
}

// Decode each scene through its strict, cost-limited decoder even when nested in
// an owner look document.
func (l *Look) UnmarshalJSON(b []byte) error {
	type plain Look
	var v plain
	if e := json.Unmarshal(b, &v); e != nil {
		return e
	}
	var raw struct {
		Documents map[string]json.RawMessage `json:"documents"`
	}
	if e := json.Unmarshal(b, &raw); e != nil {
		return e
	}
	for k, b := range raw.Documents {
		d, e := scene.Decode(b)
		if e != nil {
			return e
		}
		v.Documents[k] = d
	}
	*l = Look(v)
	return nil
}
