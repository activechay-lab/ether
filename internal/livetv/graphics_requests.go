package livetv

import (
	"context"
	"github.com/braedonsaunders/ether/internal/core"
	"time"
)

// GraphicsRequests is a background-only planning snapshot. Empty lookID limits
// it to recently watched channels and includes their next three hours of clips.
// A lookID requests static clips for every channel using that saved look.
func (s *Service) GraphicsRequests(ctx context.Context, lookID string) []core.ClipRequest {
	cs, e := s.virtualChannels(ctx)
	if e != nil {
		return nil
	}
	recent := s.recentlyWatched()
	now := s.o.Now()
	out := []core.ClipRequest{}
	for _, c := range cs {
		if c.Look == nil || !c.Enabled {
			continue
		}
		if lookID != "" {
			if c.Look.LookID != lookID {
				continue
			}
		} else if !recent[c.ID] {
			continue
		}
		c = s.graphicsChannel(c)
		for _, m := range []string{"ident", "offair", "break"} {
			out = append(out, s.clipRequest(c, Programme{Item: Item{Slate: m}, Start: now, End: now}))
		}
		if lookID == "" {
			for _, p := range Schedule(c, now, now.Add(3*time.Hour)) {
				if interstitial(p.Item) || p.Item.Slate == offAirSlate {
					out = append(out, s.clipRequest(c, p))
				}
			}
		}
	}
	return out
}
