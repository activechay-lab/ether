package plugins

import (
	"encoding/json"
	"fmt"
	"github.com/braedonsaunders/ether/internal/onair/scene"
	"time"
)

// Accept the feed interval shorthand without changing existing job manifests.
func (e *EtherBlock) UnmarshalJSON(raw []byte) error {
	type plain EtherBlock
	var wire struct {
		*plain
		Schedule json.RawMessage `json:"schedule"`
	}
	wire.plain = (*plain)(e)
	if err := json.Unmarshal(raw, &wire); err != nil {
		return err
	}
	if len(wire.Schedule) == 0 || string(wire.Schedule) == "null" {
		return nil
	}
	var interval string
	if json.Unmarshal(wire.Schedule, &interval) == nil {
		e.Schedule = []ScheduleEntry{{Every: interval, Call: "/ether/feeds"}}
		return nil
	}
	return json.Unmarshal(wire.Schedule, &e.Schedule)
}
func validateOnAir(m Manifest, add func(string, string)) {
	if m.Ether == nil {
		return
	}
	e := m.Ether
	c := e.Contributes
	if !m.HasCapability(CapOnAirContent) {
		if len(c.OnAirFeeds)+len(c.OnAirSegments) > 0 {
			add("ether.capabilities", "onair_content is required")
		}
		return
	}
	if e.Kind != KindExtension || e.Runtime.Kind != "hosted" {
		add("ether.capabilities", "onair_content needs an extension with a hosted runtime")
	}
	if len(c.OnAirFeeds)+len(c.OnAirSegments) == 0 {
		add("ether.contributes", "onair_content needs feeds or segments")
	}
	if len(c.OnAirFeeds) > 0 && len(e.Schedule) == 0 {
		add("ether.schedule", "a feed schedule is required")
	}
	feeds := map[string]bool{}
	for i, f := range c.OnAirFeeds {
		base := fmt.Sprintf("ether.contributes.onairFeeds[%d]", i)
		if !keyRe.MatchString(f.ID) || feeds[f.ID] {
			add(base+".id", "must be a unique simple key")
		}
		feeds[f.ID] = true
		if d, err := time.ParseDuration(f.MaxAge); err != nil || d <= 0 {
			add(base+".maxAge", "must be a positive duration")
		}
		if len(f.Fields) == 0 || len(f.Fields) > 64 {
			add(base+".fields", "must declare 1 to 64 fields")
		}
		for k, t := range f.Fields {
			if !keyRe.MatchString(k) {
				add(base+".fields."+k, "invalid field name")
			}
			if t != "text" && t != "number" && t != "image" && t != "list" {
				add(base+".fields."+k, "must be text, number, image or list")
			}
		}
	}
	seen := map[string]bool{}
	for i, s := range c.OnAirSegments {
		base := fmt.Sprintf("ether.contributes.onairSegments[%d]", i)
		if !keyRe.MatchString(s.ID) || seen[s.ID] {
			add(base+".id", "must be a unique simple key")
		}
		seen[s.ID] = true
		if s.Name == "" {
			add(base+".name", "is required")
		}
		if s.Duration < 4 || s.Duration > 600 || s.Duration%4 != 0 {
			add(base+".duration", "must be a multiple of 4 seconds, at most 600")
		}
		if s.File == "" || unsafePath(s.File) {
			add(base+".file", "must name a scene inside the package")
		}
		for _, id := range s.Feeds {
			if !feeds[id] {
				add(base+".feeds", "unknown feed "+id)
			}
		}
	}
}
func validateSegmentFiles(m Manifest, files map[string][]byte) error {
	if m.Ether == nil {
		return nil
	}
	for _, s := range m.Ether.Contributes.OnAirSegments {
		raw, ok := files[s.File]
		if !ok {
			return fmt.Errorf("segment %s: missing scene %s", s.ID, s.File)
		}
		d, err := scene.Decode(raw)
		if err != nil {
			return fmt.Errorf("segment %s: %w", s.ID, err)
		}
		if d.Moment != "plugin:"+m.ID+"/"+s.ID {
			return fmt.Errorf("segment %s: scene moment must match plugin segment id", s.ID)
		}
		if d.Duration != float64(s.Duration) {
			return fmt.Errorf("segment %s: scene duration differs from manifest", s.ID)
		}
	}
	return nil
}
