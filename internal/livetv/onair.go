package livetv

import (
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

func interstitial(it Item) bool {
	if it.Key != 0 {
		return false
	}
	switch it.Slate {
	case "ident", "upnext", "break", "back", "tonight":
		return true
	}
	return false
}

func (c Channel) moments(at time.Time) []Item {
	if c.Look == nil || c.clipDuration == nil {
		return nil
	}
	var kinds []string
	if c.Look.Breaks == "between" && (c.Look.BreakEvery <= 0 || at.Unix()/60%int64(c.Look.BreakEvery) == 0) {
		kinds = append(kinds, "break", "back")
	}
	if c.Look.UpNext {
		kinds = append(kinds, "upnext")
		if at.In(zoneOf(c.Timezone)).Hour() >= 18 {
			kinds = append(kinds, "tonight")
		}
	}
	var items []Item
	for _, kind := range kinds {
		if d, ok := c.clipDuration(c.Look.LookID, kind); ok && d > 0 && d%segmentDuration == 0 {
			items = append(items, Item{Slate: kind, Title: kind, DurationMS: d.Milliseconds()})
		}
	}
	return items
}

// guideSchedule preserves the original gap guide, including filler and off-air
// rows. Insert placement folds clip spans into the preceding programme.
func guideSchedule(c Channel, from, until time.Time) []Programme {
	if c.Look == nil || c.clipDuration == nil {
		return Schedule(c, from, until)
	}
	if c.Look.Placement != "insert" {
		c.Look = nil
		return Schedule(c, from, until)
	}
	ps := Schedule(c, dayStart(from, zoneOf(c.Timezone)).AddDate(0, 0, -1), until.Add(24*time.Hour))
	var out []Programme
	for _, p := range ps {
		if interstitial(p.Item) {
			if len(out) > 0 {
				out[len(out)-1].End = p.End
			}
			continue
		}
		out = append(out, p)
	}
	var visible []Programme
	for _, p := range out {
		if p.End.After(from) && p.Start.Before(until) {
			visible = append(visible, p)
		}
	}
	return visible
}

func (s *Service) clipRequest(c Channel, p Programme) core.ClipRequest {
	r := core.ClipRequest{LookID: c.Look.LookID, Moment: p.Item.Slate, ChannelID: c.ID, Bind: map[string]string{"channel.name": c.Name, "channel.tagline": c.Look.Tagline, "channel.accent": c.Look.Accent}, Art: map[string]string{}}
	if p.Item.Slate != "upnext" && p.Item.Slate != "tonight" && p.Item.Slate != "back" {
		return r
	}
	c = s.graphicsChannel(c)
	for _, next := range Schedule(c, p.Start, p.End.Add(24*time.Hour)) {
		if next.Item.Key != 0 && !next.Start.Before(p.End) {
			r.Bind["next.title"] = next.Item.Title
			r.Bind["next.time"] = next.Start.In(zoneOf(c.Timezone)).Format("15:04")
			r.Art["next.poster"] = next.Item.Thumb
			break
		}
	}
	return r
}
func (s *Service) graphicsChannel(c Channel) Channel {
	if s.o.Graphics != nil {
		c.clipDuration = s.o.Graphics.Duration
	}
	return c
}

func (s *Service) ensureGraphics() {
	if s.o.Graphics == nil {
		return
	}
	cs, err := s.virtualChannels(s.ctx)
	if err != nil {
		return
	}
	now := s.o.Now()
	recent := s.recentlyWatched()
	for _, c := range cs {
		if c.Look == nil || !c.Enabled || !recent[c.ID] {
			continue
		}
		c = s.graphicsChannel(c)
		for _, p := range Schedule(c, now, now.Add(3*time.Hour)) {
			if interstitial(p.Item) || p.Item.Slate == offAirSlate {
				s.o.Graphics.Ensure(s.ctx, s.clipRequest(c, p))
			}
		}
	}
}
