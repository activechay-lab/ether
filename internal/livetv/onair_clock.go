package livetv

import (
	"fmt"
	"sort"
	"time"
)

// Clock reservations use the same bounded calendar segments as owner blocks.
// Their duration depends only on the manifest, never feed freshness or cache state.
func clockSchedule(c Channel, from, until time.Time) []Programme {
	loc := zoneOf(c.Timezone)
	var out []Programme
	for day := dayStart(from, loc); day.Before(until); day = day.AddDate(0, 0, 1) {
		type reservation struct {
			at   int
			item Item
		}
		var slots []reservation
		for _, slot := range c.Clock {
			on := len(slot.Days) == 0
			for _, d := range slot.Days {
				on = on || int(day.Weekday()) == d
			}
			if !on {
				continue
			}
			item, ok := c.pluginItem(slot.SegmentID)
			if !ok {
				continue
			}
			every := slot.EveryMinutes
			if every == 0 {
				every = 60
			}
			if every < 1 || slot.Minute < 0 || slot.Minute > 59 {
				continue
			}
			for minute := slot.Minute; minute < 1440; minute += every {
				if minute >= slot.From && minute < slot.Until {
					slots = append(slots, reservation{minute, item})
				}
			}
		}
		sort.SliceStable(slots, func(i, j int) bool { return slots[i].at < slots[j].at })
		plain := c
		plain.Clock = nil
		// Calendar boundaries are independent of the caller's query window.
		cursor := day
		end := day.AddDate(0, 0, 1)
		// Compute the underlying day once. Every clock window clips the same
		// deterministic timeline; rebuilding it per reservation is quadratic.
		programmes := Schedule(plain, day, end)
		for _, slot := range slots {
			start := time.Date(day.Year(), day.Month(), day.Day(), slot.at/60, slot.at%60, 0, 0, loc)
			stop := start.Add(time.Duration(slot.item.DurationMS) * time.Millisecond)
			if start.Before(cursor) || stop.After(end) {
				continue
			}
			out = appendClockWindow(out, plain, programmes, cursor, start, from, until)
			appendProgramme(&out, c, slot.item, start, stop, from, until)
			cursor = stop
		}
		out = appendClockWindow(out, plain, programmes, cursor, end, from, until)
	}
	return out
}
func appendClockWindow(out []Programme, c Channel, programmes []Programme, start, end, from, until time.Time) []Programme {
	if !end.After(start) || !end.After(from) || !start.Before(until) {
		return out
	}
	// Reserve calendar spans, bounding a crossing programme as an owner block does.
	for _, p := range programmes {
		a, b := p.Start, p.End
		if a.Before(start) {
			a = start
		}
		if b.After(end) {
			b = end
		}
		if b.After(from) && a.Before(until) && b.After(a) {
			if p.Item.ReelClipID != "" && (!a.Equal(p.Start) || !b.Equal(p.End)) {
				// A fixed clock slot can reserve part of an otherwise whole reel.
				// Leave that dead-air span to filler rather than cutting a master.
				plain := c
				plain.Look = nil
				plain.Clock = nil
				fillGap(&out, plain, c.Filler, a, b, from, until)
				continue
			}
			p.sourceOffset += a.Sub(p.Start)
			p.Start = a
			p.End = b
			p.ID = fmt.Sprintf("%s-%d", c.ID, a.UnixMilli())
			out = append(out, p)
		}
	}
	return out
}
