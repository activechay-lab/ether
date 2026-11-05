package livetv

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

type sweepResult struct {
	signature string
	available bool
}

func sweepSignature(c Channel) string {
	if c.sweepKey != "" {
		return c.sweepKey
	}
	b, _ := json.Marshal(c.StreamSources)
	return stableID(c.StreamURL, string(b))
}

func (s *Service) sweepState(c Channel) string {
	if c.Kind != "iptv" {
		return ""
	}
	if s.o.HealthSweepMinutes != nil && s.o.HealthSweepMinutes() < 0 {
		return "unchecked"
	}
	s.healthMu.RLock()
	result, ok := s.channelHealth[c.ID]
	s.healthMu.RUnlock()
	if !ok || result.signature != sweepSignature(c) {
		return "unchecked"
	}
	if result.available {
		return "available"
	}
	return "unavailable"
}

func (s *Service) healthSweepLoop() {
	defer s.wg.Done()
	for {
		minutes := 0
		if s.o.HealthSweepMinutes != nil {
			minutes = s.o.HealthSweepMinutes()
		}
		if minutes == 0 {
			minutes = 15
		}
		if minutes < 0 {
			minutes = 15
		}
		if minutes > 24*60 {
			minutes = 24 * 60
		}
		timer := time.NewTimer(time.Duration(minutes) * time.Minute)
		select {
		case <-s.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if s.o.HealthSweepMinutes != nil && s.o.HealthSweepMinutes() < 0 {
			continue
		}
		if s.o.Transcoder != nil {
			s.sweepChannels(s.ctx)
		}
	}
}

// sweepChannels performs all network work away from viewer requests. A cancelled
// sweep leaves the previous complete snapshot in place.
func (s *Service) sweepChannels(ctx context.Context) {
	cs, err := s.allChannels(ctx)
	if err != nil {
		return
	}
	jobs := make(chan Channel)
	next := make(map[string]sweepResult)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for c := range jobs {
				signature := sweepSignature(c)
				playable := false
				if normalizeStreamSources(&c) == nil {
					for _, input := range c.StreamSources {
						if !input.Enabled {
							continue
						}
						probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
						headers, err := s.upstreamHeaders(probeCtx, c, input)
						ss := &session{}
						if err == nil {
							err = s.grantInput(ss, input, headers)
						}
						if err == nil {
							var relay string
							relay, err = s.relayInput(ss)
							if err == nil {
								var infoErr error
								info, e := s.probeLiveCached(probeCtx, sourceHealthKey(c.ID, input), relay)
								infoErr = e
								if infoErr == nil && info != nil && info.Video() != nil {
									playable = true
								}
							}
						}
						s.revokeInput(ss)
						cancel()
						if playable || ctx.Err() != nil {
							break
						}
					}
				}
				mu.Lock()
				next[c.ID] = sweepResult{signature: signature, available: playable}
				mu.Unlock()
			}
		}()
	}
	for _, c := range cs {
		if c.Kind != "iptv" || !c.Enabled {
			continue
		}
		// A provider that sells connections sees a sweep as a channel scan
		// and may ban the account; its channels are judged by real tunes.
		if c.SourceID != "" && s.sourceConnectionLimit(c.SourceID) > 0 {
			continue
		}
		select {
		case jobs <- c:
		case <-ctx.Done():
			break
		}
	}
	close(jobs)
	wg.Wait()
	if ctx.Err() != nil {
		return
	}
	s.healthMu.Lock()
	s.channelHealth = next
	s.healthMu.Unlock()
	s.bumpLineup()
}
