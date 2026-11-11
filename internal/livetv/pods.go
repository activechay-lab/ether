package livetv

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// BreakPattern is opt-in. Bounds apply to whole pods, including bumpers.
type BreakPattern struct {
	Preset          string `json:"preset,omitempty"`
	Seconds         int    `json:"seconds"`
	MinSeconds      int    `json:"minSeconds"`
	MaxSeconds      int    `json:"maxSeconds"`
	Spots           int    `json:"spots"`
	MinSpots        int    `json:"minSpots"`
	MaxSpots        int    `json:"maxSpots"`
	PodsPerHalfHour int    `json:"podsPerHalfHour"`
	Inside          bool   `json:"inside"`
	NoRepeatMinutes int    `json:"noRepeatMinutes"`
	StationID       bool   `json:"stationId"`
	BumperStyle     string `json:"bumperStyle"`
	ClockAlignment  string `json:"clockAlignment"`
}

func EraBreakPattern(era string) (BreakPattern, bool) {
	p := BreakPattern{Preset: era, MinSeconds: 60, MaxSeconds: 180, MinSpots: 2, MaxSpots: 6, PodsPerHalfHour: 2, Inside: true, NoRepeatMinutes: 60, BumperStyle: "break-back", ClockAlignment: "none"}
	switch era {
	case "1980s":
		p.Seconds = 120
		p.Spots = 4
		p.ClockAlignment = "half"
		p.StationID = true
	case "1990s":
		p.Seconds = 150
		p.Spots = 5
		p.StationID = true
	case "2000s":
		p.Seconds = 180
		p.Spots = 6
		p.MaxSeconds = 240
		p.MaxSpots = 8
		p.PodsPerHalfHour = 3
	case "2010s":
		p.Seconds = 210
		p.Spots = 7
		p.MaxSeconds = 270
		p.MaxSpots = 9
		p.PodsPerHalfHour = 3
	default:
		return BreakPattern{}, false
	}
	return p, true
}

func (p BreakPattern) Validate() error {
	if p.MinSeconds < 0 || p.MaxSeconds < 1 || p.MaxSeconds > 600 || p.MinSeconds > p.MaxSeconds || p.Seconds < 0 || p.Seconds > p.MaxSeconds || p.MinSpots < 0 || p.MaxSpots < 1 || p.MaxSpots > 30 || p.MinSpots > p.MaxSpots || p.Spots < 0 || p.Spots > p.MaxSpots || p.PodsPerHalfHour < 1 || p.PodsPerHalfHour > 6 || p.NoRepeatMinutes < 0 || p.NoRepeatMinutes > 1440 || (p.ClockAlignment != "none" && p.ClockAlignment != "half") || (p.BumperStyle != "break-back" && p.BumperStyle != "none") {
		return fmt.Errorf("invalid commercial break pattern")
	}
	return nil
}

func normalizeRundown(configured []string) ([]string, bool) {
	if configured == nil {
		return nil, false
	}
	var out []string
	for _, raw := range configured {
		m := strings.TrimSpace(raw)
		if m == "" || m == "none" {
			continue
		}
		out = append(out, m)
	}
	return out, true
}

func validateRundownSlice(list []string) error {
	if len(list) > 8 {
		return fmt.Errorf("rundown list exceeds 8 moments")
	}
	for _, raw := range list {
		m := strings.TrimSpace(raw)
		switch {
		case m == "none", m == "ident", m == "upnext", m == "tonight", m == "break", m == "back":
		case strings.HasPrefix(m, "plugin:") && len(m) > len("plugin:"):
		default:
			return fmt.Errorf("invalid rundown moment %q", raw)
		}
	}
	return nil
}

func validateLookRundown(l *ChannelLook) error {
	if l == nil {
		return nil
	}
	if err := validateRundownSlice(l.BeforeBreak); err != nil {
		return err
	}
	if err := validateRundownSlice(l.AfterBreak); err != nil {
		return err
	}
	return validateRundownSlice(l.BetweenProgrammes)
}

func (c Channel) rundownMomentItem(kind string) (Item, bool) {
	if strings.HasPrefix(kind, "plugin:") {
		return c.pluginItem(kind)
	}
	if c.clipDuration != nil {
		if d, ok := c.momentDuration(kind); ok && d > 0 && d%segmentDuration == 0 {
			return Item{Slate: kind, Title: kind, DurationMS: d.Milliseconds()}, true
		}
	}
	return Item{}, false
}

func (c Channel) podItems(at time.Time, budget time.Duration) []Item {
	return c.podItemsAround(at, budget, false)
}

func (c Channel) podItemsAround(at time.Time, budget time.Duration, betweenProgrammes bool) []Item {
	if c.Look == nil || c.Look.BreakPattern == nil || c.reel == nil || c.momentHistory == nil || at.Before(c.reel.Anchor) {
		return nil
	}
	p := *c.Look.BreakPattern
	if p.Validate() != nil {
		return nil
	}
	h := c.momentHistory
	// A frequency ceiling plus spacing prevents bunching around half-hour edges.
	interval := 30 * time.Minute / time.Duration(p.PodsPerHalfHour)
	minimumSpacing := interval
	if p.Inside {
		minimumSpacing = 3 * time.Minute
	}
	if !h.lastBreak.IsZero() && at.Sub(h.lastBreak) < minimumSpacing {
		return nil
	}

	// Build before-break and after-break moment sequences.
	var beforeKinds, afterKinds []string
	if cfgBefore, custom := normalizeRundown(c.Look.BeforeBreak); custom {
		beforeKinds = cfgBefore
	} else if betweenProgrammes {
		if tonightEnabledAt(c, at) {
			beforeKinds = append(beforeKinds, "tonight")
		}
		if c.Look.UpNext {
			beforeKinds = append(beforeKinds, "upnext")
		}
		if p.BumperStyle != "none" {
			beforeKinds = append(beforeKinds, "break")
		}
	} else {
		// Mid-programme break: air the "coming up" lineup graphic before the
		// break bumper when explicitly enabled on the channel/look.
		if c.Look.Tonight != nil && *c.Look.Tonight {
			beforeKinds = append(beforeKinds, "tonight")
		}
		if p.BumperStyle != "none" {
			beforeKinds = append(beforeKinds, "break")
		}
	}

	if cfgAfter, custom := normalizeRundown(c.Look.AfterBreak); custom {
		afterKinds = cfgAfter
	} else if betweenProgrammes {
		if p.BumperStyle != "none" {
			afterKinds = append(afterKinds, "back")
		}
		if c.Look.Ident || p.StationID {
			afterKinds = append(afterKinds, "ident")
		}
	} else {
		if p.BumperStyle != "none" {
			afterKinds = append(afterKinds, "back")
		}
		if p.StationID && !c.Look.Ident {
			afterKinds = append(afterKinds, "ident")
		}
	}

	var beforeItems, afterItems []Item
	var beforeDur, afterDur, intrinsicBumperDur time.Duration
	for _, k := range beforeKinds {
		if it, ok := c.rundownMomentItem(k); ok {
			d := time.Duration(it.DurationMS) * time.Millisecond
			beforeItems = append(beforeItems, it)
			beforeDur += d
			if k == "break" {
				intrinsicBumperDur += d
			}
		}
	}
	for _, k := range afterKinds {
		if it, ok := c.rundownMomentItem(k); ok {
			d := time.Duration(it.DurationMS) * time.Millisecond
			afterItems = append(afterItems, it)
			afterDur += d
			if k == "back" || (k == "ident" && p.StationID && !c.Look.Ident) {
				intrinsicBumperDur += d
			}
		}
	}

	extraMomentsDur := (beforeDur + afterDur) - intrinsicBumperDur
	if budget > 0 && beforeDur+afterDur > budget {
		return nil
	}

	limit := time.Duration(p.MaxSeconds) * time.Second
	if p.ClockAlignment == "half" {
		rem := at.Unix() % 1800
		if rem < 0 {
			rem += 1800
		}
		remaining := time.Duration(1800-rem) * time.Second
		if remaining-extraMomentsDur < limit {
			limit = remaining - extraMomentsDur
		}
	}
	if budget > 0 && budget-extraMomentsDur < limit {
		limit = budget - extraMomentsDur
	}
	if limit <= 0 {
		return nil
	}

	reserved := intrinsicBumperDur
	pool := append([]ReelClip(nil), c.reel.Clips...)
	day := dayStart(at, zoneOf(c.Timezone)).Unix()
	sort.SliceStable(pool, func(i, j int) bool {
		return stepHash(c.Seed, day, int64Hash(pool[i].ID)) < stepHash(c.Seed, day, int64Hash(pool[j].ID))
	})
	// Rotate a stable daily permutation by the pod clock, preserving reproducibility.
	if len(pool) > 0 {
		lp := int64(len(pool))
		n := int(((at.Unix()/int64(interval/time.Second))%lp + lp) % lp)
		pool = append(pool[n:], pool[:n]...)
	}
	var spots []Item
	played := reserved
	chosen := map[string]time.Time{}
	repeat := time.Duration(p.NoRepeatMinutes) * time.Minute
	if c.reel.Repeat > repeat {
		repeat = c.reel.Repeat
	}
	for _, clip := range pool {
		if len(spots) >= p.MaxSpots || (p.Spots > 0 && len(spots) >= p.Spots) || (p.Seconds > 0 && played >= time.Duration(p.Seconds)*time.Second && len(spots) >= p.MinSpots) {
			break
		}
		start := at.Add(beforeDur + (played - reserved))
		if clip.Duration <= 0 || played+clip.Duration > limit {
			continue
		}
		if last, ok := h.last[clip.ID]; ok && start.Sub(last) < repeat {
			continue
		}
		spots = append(spots, Item{Slate: "reel", Title: clip.Name, DurationMS: clip.Duration.Milliseconds(), ReelID: c.reel.ID, ReelClipID: clip.ID})
		chosen[clip.ID] = start
		played += clip.Duration
	}
	if len(spots) < p.MinSpots || len(spots) == 0 || played < time.Duration(p.MinSeconds)*time.Second {
		return nil
	}
	out := make([]Item, 0, len(beforeItems)+len(spots)+len(afterItems))
	out = append(out, beforeItems...)
	out = append(out, spots...)
	out = append(out, afterItems...)
	for k, v := range chosen {
		h.last[k] = v
	}
	h.lastBreak = at
	return out
}

// emitProgrammeWithPods consumes offline chapter/black/silence points only.
// sourceOffset resumes the programme at its original media timestamp.
func emitProgrammeWithPods(out *[]Programme, c Channel, item Item, start, finish, from, until, end time.Time) time.Time {
	item = availableBreakItem(item, start)
	cursor := start
	offset := time.Duration(0)
	if c.Look != nil && c.Look.BreakPattern != nil && c.Look.BreakPattern.Inside {
		points := append([]int64(nil), item.BreakPointsMS...)
		if len(item.BreakPoints) > 0 {
			points = nil
			for _, p := range item.BreakPoints {
				points = append(points, p.AtMS)
			}
		}
		if c.Look.BreakPlacement == "between" {
			points = nil
		}
		if c.Look.BreakPlacement == "even" {
			points = nil
			spacing := int64(30 * 60 * 1000 / c.Look.BreakPattern.PodsPerHalfHour)
			for at := spacing; at < item.DurationMS; at += spacing {
				points = append(points, at)
			}
		}
		sort.Slice(points, func(i, j int) bool { return points[i] < points[j] })
		for _, ms := range points {
			point := time.Duration(ms) * time.Millisecond
			if point <= offset || point >= finish.Sub(start) {
				continue
			}
			boundary := cursor.Add(point - offset)
			if !boundary.Before(end) {
				break
			}
			available := end.Sub(boundary) - (finish.Sub(start) - point)
			if available <= 0 {
				continue
			}
			pieces := c.podItems(boundary, available)
			if len(pieces) == 0 {
				continue
			}
			n := len(*out)
			appendProgramme(out, c, item, cursor, boundary, from, until)
			if len(*out) > n {
				(*out)[len(*out)-1].sourceOffset = offset
			}
			cursor = boundary
			for _, clip := range pieces {
				next := cursor.Add(time.Duration(clip.DurationMS) * time.Millisecond)
				appendProgramme(out, c, clip, cursor, next, from, until)
				cursor = next
			}
			offset = point
		}
	}
	next := cursor.Add(finish.Sub(start) - offset)
	if next.After(end) {
		next = end
	}
	n := len(*out)
	appendProgramme(out, c, item, cursor, next, from, until)
	if len(*out) > n {
		(*out)[len(*out)-1].sourceOffset = offset
	}
	return next
}

// Evidence discovered during an airing applies only to a later programme start.
// Existing airing IDs, slices and guide boundaries must remain deterministic.
func availableBreakItem(item Item, start time.Time) Item {
	if item.BreakAvailableAfterMS > 0 && start.UnixMilli() < item.BreakAvailableAfterMS {
		item.BreakPoints = nil
		item.BreakPointsMS = nil
		item.BreakReleaseID = ""
		item.BreakRevision = ""
	}
	return item
}
