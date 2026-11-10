package livetv

import (
	"context"
	"fmt"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// The guide's on-now title gets the whole cold-tune window to open: the
// opening ident holds the screen meanwhile, and a slow resolve is not a reason
// to air something else. Stand-ins after a real failure get a short budget
// each, so a hung "not available yet" still reaches the channel's fallback.
var (
	virtualAttemptBudget = 6 * time.Second
	virtualOnNowBudget   = broadcastColdProgrammeWait
)

const virtualMaxAttempts = 4

func (s *Service) knownUnavailable(ctx context.Context, key core.RatingKey) bool {
	if key == 0 || s.o.Availability == nil {
		return false
	}
	known, ok := s.o.Availability.Available(ctx, key)
	return known && !ok
}

// virtualCandidates starts with the guide's on-now title, then the channel's
// fallback, then the running order: a stand-in never takes a later airing
// while fallback programming exists. Known-unplayable titles are skipped by
// the caller before Prepare.
func (s *Service) virtualCandidates(c Channel, current Programme) []Programme {
	now := s.o.Now()
	var out []Programme
	seen := map[core.RatingKey]struct{}{}
	add := func(p Programme) {
		if len(out) >= 12 || p.Item.Key == 0 || p.Item.Slate != "" || p.Item.DurationMS <= 0 {
			return
		}
		if _, ok := seen[p.Item.Key]; ok {
			return
		}
		seen[p.Item.Key] = struct{}{}
		out = append(out, p)
	}
	add(current)
	if c.Personal == nil {
		for _, item := range c.Fallback {
			add(standIn(c, item, now))
		}
	}
	for _, p := range s.viewerSchedule(s.ctx, c, now, now.Add(24*time.Hour)) {
		add(p)
	}
	if c.Personal != nil {
		return out
	}
	for _, block := range c.Blocks {
		for _, item := range block.Items {
			add(standIn(c, item, now))
		}
	}
	for _, item := range c.Items {
		add(standIn(c, item, now))
	}
	return out
}

func standIn(c Channel, item Item, now time.Time) Programme {
	dur := time.Duration(item.DurationMS) * time.Millisecond
	return Programme{
		ID:        fmt.Sprintf("%s-fill-%d", c.ID, item.Key),
		ChannelID: c.ID,
		Start:     now,
		End:       now.Add(dur),
		Item:      item,
	}
}
