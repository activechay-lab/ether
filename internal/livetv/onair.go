package livetv

import (
	"github.com/braedonsaunders/ether/internal/contentrating"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

func interstitial(it Item) bool {
	if it.Key != 0 {
		return false
	}
	if strings.HasPrefix(it.Slate, "plugin:") {
		return true
	}
	switch it.Slate {
	case "ident", "upnext", "break", "back", "tonight", "welcome", "reel":
		return true
	}
	return false
}

func (c Channel) moments(at time.Time) []Item { return c.momentsWithin(at, 0) }
func (c Channel) momentsWithin(at time.Time, budget time.Duration) []Item {
	if c.Look != nil && c.Look.BreakPattern != nil {
		return c.podItems(at, budget)
	}
	if c.Look == nil || c.clipDuration == nil {
		return nil
	}
	var kinds []string
	breaks := c.Look.Breaks == "between"
	if breaks && c.Look.BreakEvery > 0 && c.momentHistory != nil && !c.momentHistory.lastBreak.IsZero() {
		breaks = at.Sub(c.momentHistory.lastBreak) >= time.Duration(c.Look.BreakEvery)*time.Minute
	}
	if breaks {
		if c.Look.BreakContent == "segments" {
			kinds = append(kinds, c.Look.SegmentIDs...)
		} else {
			kinds = append(kinds, "break")
		}
		kinds = append(kinds, "back")
	}

	if c.Look.Ident {
		kinds = append(kinds, "ident")
	}
	if c.Look.UpNext {
		kinds = append(kinds, "upnext")
		if at.In(zoneOf(c.Timezone)).Hour() >= 18 {
			kinds = append(kinds, "tonight")
		}
	}
	var base []Item
	var reserved time.Duration
	for _, kind := range kinds {
		if strings.HasPrefix(kind, "plugin:") {
			if item, ok := c.pluginItem(kind); ok {
				base = append(base, item)
				reserved += time.Duration(item.DurationMS) * time.Millisecond
			}
			continue
		}
		if d, ok := c.clipDuration(c.Look.LookID, kind); ok && d > 0 && d%segmentDuration == 0 {
			base = append(base, Item{Slate: kind, Title: kind, DurationMS: d.Milliseconds()})
			reserved += d
		}
	}
	if breaks && c.momentHistory != nil && len(base) > 0 {
		c.momentHistory.lastBreak = at
	}
	if budget > 0 && reserved > budget {
		var fit []Item
		for _, item := range base {
			d := time.Duration(item.DurationMS) * time.Millisecond
			if d <= budget {
				fit = append(fit, item)
				budget -= d
			}
		}
		return fit
	}
	var out []Item
	cursor := at
	for _, item := range base {
		out = append(out, item)
		cursor = cursor.Add(time.Duration(item.DurationMS) * time.Millisecond)
		if item.Slate == "break" {
			if c.momentHistory != nil {
				c.momentHistory.lastBreak = at
			}
			available := time.Duration(0)
			if budget > 0 {
				available = budget - reserved
				if available <= 0 {
					continue
				}
			}
			reel := c.reelItems(cursor, available)
			out = append(out, reel...)
			for _, clip := range reel {
				cursor = cursor.Add(time.Duration(clip.DurationMS) * time.Millisecond)
			}
		}
	}
	return out
}

// guideSchedule preserves the original gap guide, including filler and off-air
// rows. Insert placement folds clip spans into the preceding programme.
func guideSchedule(c Channel, from, until time.Time) []Programme {
	if c.Look == nil || c.clipDuration == nil {
		return Schedule(c, from, until)
	}
	if c.Look.Placement != "insert" && c.Look.BreakPattern == nil {
		if len(c.Clock) > 0 {
			// Suppress look interstitials while retaining the clock's channel policy.
			look := *c.Look
			look.Breaks = ""
			look.UpNext = false
			look.Ident = false
			c.Look = &look
		} else {
			c.Look = nil
		}
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
		if c.Look.BreakPattern != nil && len(out) > 0 && p.Item.Key != 0 && out[len(out)-1].Item.Key == p.Item.Key && p.sourceOffset > 0 {
			out[len(out)-1].End = p.End
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
	lookID := ""
	if c.Look != nil {
		lookID = c.Look.LookID
	}
	r := core.ClipRequest{LookID: lookID, Moment: p.Item.Slate, ChannelID: c.ID, Bind: map[string]string{"channel.name": c.Name}, Art: map[string]string{}}
	if c.Look != nil {
		r.Bind["channel.tagline"] = c.Look.Tagline
		r.Bind["channel.accent"] = c.Look.Accent
	}
	if c.Personal != nil && c.personalViewer != nil {
		r.ViewerID = c.personalViewer.ID
		r.Bind["viewer.name"] = viewerName(c.personalViewer)
		r.Bind["viewer.greeting"] = "Welcome back"
		r.Bind["next.reason"] = p.Reason
		if p.Item.Slate == "welcome" {
			r.Bind["resume.title"] = p.Item.Title
			r.Bind["resume.minutes"] = p.Item.Summary
		}
	}
	if p.Item.Slate != "upnext" && p.Item.Slate != "tonight" && p.Item.Slate != "back" && p.Item.Slate != "lowerthird" {
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
		if meta, ok := s.o.Graphics.(core.ChannelSegmentMetadata); ok {
			c.segmentInfo = meta.SegmentInfo
		}
		if c.Look != nil && c.Look.BreakContent == "reel" {
			s.mu.Lock()
			c.reel = s.reelSnapshots[c.Look.ReelID]
			s.mu.Unlock()
		}
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
		if !c.Enabled || !recent[c.ID] {
			continue
		}
		if c.Look == nil && len(c.Clock) == 0 {
			continue
		}
		if c.Look != nil && c.Look.Ident {
			s.o.Graphics.Ensure(s.ctx, s.clipRequest(c, Programme{Item: Item{Slate: "ident"}}))
		}
		c = s.graphicsChannel(c)
		for _, p := range Schedule(c, now, now.Add(3*time.Hour)) {
			if strings.HasPrefix(p.Item.Slate, "plugin:") && p.Start.After(now.Add(15*time.Minute)) {
				continue
			}
			if (interstitial(p.Item) && p.Item.ReelClipID == "") || (p.Item.Slate == offAirSlate && c.Look != nil && c.Look.OffAir) {
				s.o.Graphics.Ensure(s.ctx, s.clipRequest(c, p))
			}
		}
	}
}

func (c Channel) pluginItem(id string) (Item, bool) {
	if c.clipDuration == nil {
		return Item{}, false
	}
	look := ""
	if c.Look != nil {
		look = c.Look.LookID
	}
	d, ok := c.clipDuration(look, id)
	if !ok || d <= 0 || d%segmentDuration != 0 {
		return Item{}, false
	}
	name, rating := id, ""
	if c.segmentInfo != nil {
		var exists bool
		name, rating, exists = c.segmentInfo(id)
		if !exists {
			return Item{}, false
		}
	}
	cap := c.Rating
	if c.Filter != nil && c.Filter.MaxContentRating != "" {
		cap = c.Filter.MaxContentRating
	}
	if cap != "" && rating == "" && (c.Look == nil || !c.Look.AllowUnratedSegments) {
		return Item{}, false
	}
	if rating != "" && (contentrating.Exceeds(rating, c.Rating) || (c.Filter != nil && contentrating.Exceeds(rating, c.Filter.MaxContentRating))) {
		return Item{}, false
	}
	return Item{Slate: id, Title: name, Rating: rating, DurationMS: d.Milliseconds()}, true
}
