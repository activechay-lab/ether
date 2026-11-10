package livetv

import (
	"context"
	"encoding/json"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/flight"
)

type liveProber interface {
	ProbeLive(context.Context, string) (*core.MediaInfo, error)
}

type liveProbeEntry struct {
	info    core.MediaInfo
	expires time.Time
}

type liveProbeFlight struct {
	group flight.Group[string, *core.MediaInfo]
}

// liveProbeTTL keeps a successful codec read across channel changes in one
// viewing session. Errors are never cached.
const liveProbeTTL = 15 * time.Minute

// A short cache reuses capability-independent codec analysis on channel switches.
// Errors are never cached; URL changes naturally invalidate signed/rotated feeds.
func (s *Service) probeLiveInput(ctx context.Context, input string) (*core.MediaInfo, error) {
	return s.probeLiveCached(ctx, input, input)
}

// probeSessionInput probes the session's input through its relay grant. The
// cache stays keyed by the upstream URL and headers, not the per-tune token.
func (s *Service) probeSessionInput(ctx context.Context, ss *session) (*core.MediaInfo, error) {
	input, err := s.relayInput(ss)
	if err != nil {
		return nil, err
	}
	key := ss.inputURL
	if len(ss.inputHeaders) != 0 {
		encoded, _ := json.Marshal(ss.inputHeaders)
		key = stableID(ss.inputURL, string(encoded))
	}
	started := time.Now()
	s.probeMu.Lock()
	entry, found := s.probeCache[key]
	cached := found && s.o.Now().Before(entry.expires)
	_, shared := s.probeWait[key]
	s.probeMu.Unlock()
	info, err := s.probeLiveCached(ctx, key, input)
	attrs := []any{"elapsed", time.Since(started), "cache_hit_at_start", cached, "probe_in_flight_at_start", shared}
	if err != nil {
		s.sessionLog(ss).Debug("live probe failed", append(attrs, Diagnose("probe", err).LogAttrs()...)...)
	} else {
		if info != nil {
			if video := info.Video(); video != nil {
				attrs = append(attrs, "video_codec", video.Codec, "width", video.Width, "height", video.Height)
			}
		}
		s.sessionLog(ss).Debug("live probe ready", attrs...)
	}
	return info, err
}

func (s *Service) probeLiveCached(ctx context.Context, key, input string) (*core.MediaInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.probeMu.Lock()
	if entry, found := s.probeCache[key]; found && s.o.Now().Before(entry.expires) {
		s.probeMu.Unlock()
		info := entry.info
		return &info, nil
	}
	// Keep one coordinator in the existing probeWait slot. Its Group removes
	// completed calls; no per-URL wait entries survive a probe.
	if s.probeWait == nil {
		s.probeWait = map[string]*liveProbeFlight{}
	}
	g := s.probeWait[""]
	if g == nil {
		g = &liveProbeFlight{}
		s.probeWait[""] = g
	}
	s.probeMu.Unlock()
	info, err := g.group.Do(ctx, key, func(ctx context.Context) (*core.MediaInfo, error) {
		s.probeMu.Lock()
		if entry, found := s.probeCache[key]; found && s.o.Now().Before(entry.expires) {
			s.probeMu.Unlock()
			info := entry.info
			return &info, nil
		}
		s.probeMu.Unlock()
		info, err := s.probeLiveUncached(ctx, input)
		s.probeMu.Lock()
		if err == nil && info != nil && info.Video() != nil {
			s.storeProbeLocked(key, *info)
		}
		s.probeMu.Unlock()
		return info, err
	})
	if info != nil {
		copy := *info
		return &copy, err
	}
	return nil, err
}

func (s *Service) probeLiveUncached(ctx context.Context, input string) (*core.MediaInfo, error) {
	tc := s.transcoder()
	if p, ok := tc.(liveProber); ok {
		return p.ProbeLive(ctx, input)
	}
	return tc.Probe(ctx, input)
}

func (s *Service) storeProbeLocked(key string, info core.MediaInfo) {
	if s.probeCache == nil {
		s.probeCache = map[string]liveProbeEntry{}
	}
	if len(s.probeCache) >= 256 {
		for key, e := range s.probeCache {
			if !s.o.Now().Before(e.expires) {
				delete(s.probeCache, key)
			}
		}
		if len(s.probeCache) >= 256 {
			var oldest string
			var expires time.Time
			for key, e := range s.probeCache {
				if oldest == "" || e.expires.Before(expires) {
					oldest, expires = key, e.expires
				}
			}
			delete(s.probeCache, oldest)
		}
	}
	s.probeCache[key] = liveProbeEntry{info: info, expires: s.o.Now().Add(liveProbeTTL)}
}
