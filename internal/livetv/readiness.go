package livetv

import (
	"context"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

type channelReadiness struct {
	until time.Time
	next  time.Time
	guide bool
}

// Only demand wakes this worker. Availability is a cache-only lookup: no media
// is opened, and no indexer or downloader is contacted by guide preparation.
func (s *Service) readinessLoop() {
	defer s.wg.Done()
	rows, err := s.o.DB.QueryContext(s.ctx, `SELECT channel_id,active_until FROM livetv_readiness WHERE active_until>?`, s.o.Now().UnixMilli())
	if err == nil {
		for rows.Next() {
			var id string
			var ms int64
			if rows.Scan(&id, &ms) == nil {
				s.mu.Lock()
				r := s.readiness[id]
				if loaded := time.UnixMilli(ms); loaded.After(r.until) {
					r.until = loaded
				}
				s.readiness[id] = r
				s.mu.Unlock()
			}
		}
		rows.Close()
	}
	for {
		s.mu.Lock()
		active := len(s.readiness) > 0
		if !active {
			clear(s.readyTitles)
			clear(s.programmeSubs)
		}
		s.mu.Unlock()
		var tick <-chan time.Time
		var timer *time.Timer
		if active {
			timer = time.NewTimer(time.Minute)
			tick = timer.C
		}
		select {
		case <-s.ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return
		case <-s.readinessWake:
		case <-tick:
		}
		if timer != nil {
			timer.Stop()
		}
		s.mu.Lock()
		ids := make([]string, 0, len(s.readiness))
		now := s.o.Now()
		for id, r := range s.readiness {
			if !r.until.After(now) {
				delete(s.readiness, id)
				continue
			}
			if !r.next.After(now) {
				ids = append(ids, id)
				r.next = now.Add(time.Minute)
				s.readiness[id] = r
			}
		}
		s.mu.Unlock()
		for _, id := range ids {
			c, err := s.Channel(s.ctx, id)
			if err != nil || c.Kind != "virtual" || c.Personal != nil {
				continue
			}
			c = s.plannedChannel(c)
			s.mu.Lock()
			r := s.readiness[id]
			s.mu.Unlock()
			_, _ = s.o.DB.ExecContext(s.ctx, `INSERT INTO livetv_readiness(channel_id,active_until) VALUES(?,?) ON CONFLICT(channel_id) DO UPDATE SET active_until=excluded.active_until`, id, r.until.UnixMilli())
			ps := s.readinessSchedule(c, now, now.Add(3*time.Hour))
			s.mu.Lock()
			later := s.readiness[id].guide
			rr := s.readiness[id]
			rr.guide = false
			s.readiness[id] = rr
			s.mu.Unlock()
			if later {
				seen := map[string]bool{}
				for _, p := range s.readinessSchedule(c, now.Add(3*time.Hour), now.Add(8*24*time.Hour)) {
					if !seen[p.BlockID] {
						seen[p.BlockID] = true
						ps = append(ps, p)
					}
				}
			}
			for _, p := range ps {
				if p.Item.Key != 0 {
					s.checkReady(s.ctx, p.Item.Key)
				}
				select {
				case <-s.ctx.Done():
					return
				case <-time.After(100 * time.Millisecond):
				}
			}
			for i, it := range c.Fallback {
				if i >= 12 {
					break
				}
				s.checkReady(s.ctx, it.Key)
			}
			s.replaceUnready(c, ps)
		}
	}
}

func (s *Service) demandReadinessLocked(id string, duration time.Duration) {
	if s.readiness == nil {
		s.readiness = map[string]channelReadiness{}
	}
	r := s.readiness[id]
	if duration == 24*time.Hour {
		r.guide = true
	}
	until := s.o.Now().Add(duration)
	if until.After(r.until) {
		r.until = until
		s.readiness[id] = r
	}
	s.readiness[id] = r
	select {
	case s.readinessWake <- struct{}{}:
	default:
	}
}
func (s *Service) checkReady(ctx context.Context, key core.RatingKey) {
	if s.o.Availability == nil {
		return
	}
	known, ready := s.o.Availability.Available(ctx, key)
	s.mu.Lock()
	if len(s.readyTitles) > 8192 {
		clear(s.readyTitles)
	}
	if s.readyTitles == nil {
		s.readyTitles = map[core.RatingKey]bool{}
	}
	if known {
		s.readyTitles[key] = ready
	} else {
		delete(s.readyTitles, key)
	}
	s.mu.Unlock()
}
func (s *Service) readinessSchedule(c Channel, from, until time.Time) []Programme {
	s.virtualGuide.mu.RLock()
	ent := s.virtualGuide.entries[c.ID]
	s.virtualGuide.mu.RUnlock()
	if ent == nil {
		return nil
	}
	return s.readyProgrammes(c, ent.playback.slice(c.ID, from, until))
}
func (s *Service) replaceUnready(c Channel, ps []Programme) {
	for _, p := range ps {
		if p.Item.Key == 0 {
			continue
		}
		s.mu.Lock()
		ready, known := s.readyTitles[p.Item.Key]
		s.mu.Unlock()
		if !known || ready {
			continue
		}
		pool := c.Items
		for _, b := range c.Blocks {
			if b.ID == p.BlockID {
				pool = b.Items
				break
			}
		}
		start := 0
		for i, it := range pool {
			if it.Key == p.Item.Key {
				start = i + 1
				break
			}
		}
		for n := 0; n < len(pool) && n < 64; n++ {
			select {
			case <-s.ctx.Done():
				return
			case <-time.After(100 * time.Millisecond):
			}
			it := pool[(start+n)%len(pool)]
			s.checkReady(s.ctx, it.Key)
			s.mu.Lock()
			ok := s.readyTitles[it.Key]
			s.mu.Unlock()
			if !ok || it.Key == p.Item.Key {
				continue
			}
			sub, ok := joinStandIn(p, it, s.o.Now())
			if !ok {
				continue
			}
			s.latchProgramme(sub)
			break
		}
	}
}
func (s *Service) latchProgramme(p Programme) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.programmeSubs == nil {
		s.programmeSubs = map[string]Programme{}
	}
	s.programmeSubs[p.ChannelID+"/"+p.ID] = p
	s.lineupUnix = s.o.Now().Unix()
	for k, v := range s.programmeSubs {
		if v.End.Before(s.o.Now()) {
			delete(s.programmeSubs, k)
		}
	}
}
func (s *Service) readyProgrammes(c Channel, ps []Programme) []Programme {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, p := range ps {
		if sub, ok := s.programmeSubs[c.ID+"/"+p.ID]; ok {
			ps[i] = sub
		}
	}
	return ps
}

// Preserve the original airing interval. Shorter media joins as far in as
// possible while reserving two minutes; a mid-slot replacement never restarts.
func joinStandIn(slot Programme, item Item, now time.Time) (Programme, bool) {
	elapsed := now.Sub(slot.Start)
	if elapsed < 0 {
		elapsed = 0
	}
	runtime := time.Duration(item.DurationMS) * time.Millisecond
	offset := elapsed
	if elapsed >= 30*time.Second {
		if runtime <= 2*time.Minute {
			return Programme{}, false
		}
		if offset > runtime-2*time.Minute {
			offset = runtime - 2*time.Minute
		}
		if offset <= 0 {
			return Programme{}, false
		}
	}
	p := slot
	p.Item = item
	p.policyTag = nil
	p.sourceOffset = offset - elapsed
	return p, true
}
