package livetv

import (
	"context"
	"fmt"
	"github.com/braedonsaunders/ether/internal/faillog"
	"time"
)

// Missing clips are decided once per airing and shared by every viewer of the
// node-local broadcast. Guide reads apply recorded decisions without cache I/O.
func (s *Service) broadcastSchedule(ctx context.Context, c Channel, from, until time.Time, check bool) []Programme {
	c = s.graphicsChannel(c)
	if c.Look == nil || s.o.Graphics == nil {
		return Schedule(c, from, until)
	}
	start, end := from, until
	if c.Look.Placement == "insert" {
		start = dayStart(from, zoneOf(c.Timezone))
		end = until.Add(24 * time.Hour)
	}
	ps := Schedule(c, start, end)
	var out []Programme
	var advance time.Duration
	for _, p := range ps {
		if interstitial(p.Item) && check {
			request := s.clipRequest(c, p)
			p.graphicsRequest = &request
		}
		s.mu.Lock()
		skip := s.graphicsSkips[c.ID+"/"+p.ID]
		s.mu.Unlock()
		if skip.IsZero() && check && interstitial(p.Item) && p.End.Add(-advance).After(from) && p.Start.Add(-advance).Before(until) {
			request := s.clipRequest(c, p)
			if _, ok := s.o.Graphics.Clip(ctx, request); !ok {
				s.o.Graphics.Ensure(ctx, request)
				s.mu.Lock()
				if s.graphicsSkips == nil {
					s.graphicsSkips = map[string]time.Time{}
				}
				key := c.ID + "/" + p.ID
				first := s.graphicsSkips[key].IsZero()
				s.graphicsSkips[key] = p.End
				for k, end := range s.graphicsSkips {
					if end.Before(s.o.Now().Add(-48 * time.Hour)) {
						delete(s.graphicsSkips, k)
					}
				}
				s.mu.Unlock()
				skip = p.End
				if first {
					faillog.New(s.o.Logger, "livetv: interstitial skipped").Fail(fmt.Errorf("clip not cached"), "programme", p.ID, "moment", p.Item.Slate)
					if c.Look.Placement == "insert" {
						s.bumpLineup()
					}
				}
			}
		}
		if !skip.IsZero() {
			if c.Look.Placement == "insert" {
				advance += p.End.Sub(p.Start)
				continue
			}
			plain := c
			plain.Look = nil
			for _, fallback := range Schedule(plain, p.Start, p.End) {
				a, b := p.Start, p.End
				if fallback.Start.After(a) {
					a = fallback.Start
				}
				if fallback.End.Before(b) {
					b = fallback.End
				}
				appendProgramme(&out, plain, fallback.Item, a, b, from, until)
			}
			continue
		}
		p.Start = p.Start.Add(-advance)
		p.End = p.End.Add(-advance)
		if advance > 0 {
			p.ID = fmt.Sprintf("%s-%d", c.ID, p.Start.UnixMilli())
		}
		if p.End.After(from) && p.Start.Before(until) {
			out = append(out, p)
		}
	}
	return out
}
func (s *Service) broadcastGuide(c Channel, from, until time.Time) []Programme {
	if c.Look == nil || c.Look.Placement != "insert" || s.o.Graphics == nil {
		return guideSchedule(c, from, until)
	}
	ps := s.broadcastSchedule(context.Background(), c, dayStart(from, zoneOf(c.Timezone)), until.Add(24*time.Hour), false)
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
