package livetv

import (
	"math"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

type warmTarget struct {
	channelID string
	key       core.RatingKey
	offsetSec int64
}

// BroadcastVisibilitySnapshot summarizes continuous broadcast flow, live moment
// rendering, holds, and programme warming metrics.
type BroadcastVisibilitySnapshot struct {
	AnimatedMoments    int64
	CachedMoments      int64
	SplitMoments       int64
	LiveSavedMoments   int64
	LiveUnsavedMoments int64
	FallbackMoments    int64
	HoldCount          int64
	HoldDuration       time.Duration
	WarmCount          int64
	BreakReturnWarms   int64
	HandoffCount       int64
	LastWarmLead       time.Duration
	LastRenderSpeedX   float64
}

type broadcastVisibilityState struct {
	mu       sync.Mutex
	snap     BroadcastVisibilitySnapshot
	lastLog  time.Time
	lastKind string
}

// BroadcastVisibility returns a snapshot of broadcast flow and warming counters.
func (s *Service) BroadcastVisibility() BroadcastVisibilitySnapshot {
	if s == nil {
		return BroadcastVisibilitySnapshot{}
	}
	s.vis.mu.Lock()
	defer s.vis.mu.Unlock()
	return s.vis.snap
}

func (s *Service) noteMomentSpeed(speedX float64) {
	if s == nil || speedX <= 0 {
		return
	}
	s.vis.mu.Lock()
	s.vis.snap.LastRenderSpeedX = speedX
	s.vis.mu.Unlock()
}

func (s *Service) noteMomentPlayout(channelID, moment, mode string, speedX float64, reason string) {
	if s == nil {
		return
	}
	now := s.o.Now()
	s.vis.mu.Lock()
	switch mode {
	case "cached":
		s.vis.snap.AnimatedMoments++
		s.vis.snap.CachedMoments++
	case "split-cached":
		s.vis.snap.AnimatedMoments++
		s.vis.snap.SplitMoments++
	case "live-save", "split-live-save":
		s.vis.snap.AnimatedMoments++
		s.vis.snap.LiveSavedMoments++
	case "live-nosave":
		s.vis.snap.AnimatedMoments++
		s.vis.snap.LiveUnsavedMoments++
	case "fallback":
		s.vis.snap.FallbackMoments++
	}
	if speedX > 0 {
		s.vis.snap.LastRenderSpeedX = speedX
	}
	shouldLog := s.vis.lastLog.IsZero() || now.Sub(s.vis.lastLog) >= time.Hour ||
		(mode != s.vis.lastKind && (mode == "live-save" || mode == "split-live-save" || mode == "fallback"))
	snap := s.vis.snap
	if shouldLog {
		s.vis.lastLog = now
		s.vis.lastKind = mode
	}
	s.vis.mu.Unlock()
	if shouldLog && s.o.Logger != nil {
		attrs := []any{
			"channel", channelID,
			"moment", moment,
			"mode", mode,
			"animated_moments", snap.AnimatedMoments,
			"fallback_moments", snap.FallbackMoments,
			"holds", snap.HoldCount,
			"hold_ms", snap.HoldDuration.Milliseconds(),
			"warms", snap.WarmCount,
			"last_warm_lead_ms", snap.LastWarmLead.Milliseconds(),
			"render_speed_x", math.Round(snap.LastRenderSpeedX*100) / 100,
		}
		if reason != "" {
			attrs = append(attrs, "reason", reason)
		}
		s.o.Logger.Info("live broadcast visibility", attrs...)
	}
}

func (s *Service) noteHoldComplete(channelID, kind string, dur time.Duration) {
	if s == nil || dur <= 0 {
		return
	}
	now := s.o.Now()
	s.vis.mu.Lock()
	s.vis.snap.HoldCount++
	s.vis.snap.HoldDuration += dur
	shouldLog := dur >= 500*time.Millisecond && (s.vis.lastLog.IsZero() || now.Sub(s.vis.lastLog) >= 5*time.Minute)
	snap := s.vis.snap
	if shouldLog {
		s.vis.lastLog = now
		s.vis.lastKind = "hold:" + kind
	}
	s.vis.mu.Unlock()
	if shouldLog && s.o.Logger != nil {
		s.o.Logger.Info("live broadcast hold completed",
			"channel", channelID,
			"hold", kind,
			"duration_ms", dur.Milliseconds(),
			"total_holds", snap.HoldCount,
			"total_hold_ms", snap.HoldDuration.Milliseconds(),
		)
	}
}

func (s *Service) noteProgrammeHandoff(channelID string, warmLead time.Duration) {
	if s == nil {
		return
	}
	now := s.o.Now()
	s.vis.mu.Lock()
	s.vis.snap.HandoffCount++
	s.vis.snap.LastWarmLead = warmLead
	shouldLog := s.vis.lastLog.IsZero() || now.Sub(s.vis.lastLog) >= time.Hour
	snap := s.vis.snap
	if shouldLog {
		s.vis.lastLog = now
	}
	s.vis.mu.Unlock()
	if shouldLog && s.o.Logger != nil {
		s.o.Logger.Info("live broadcast handoff summary",
			"channel", channelID,
			"warm_lead_ms", warmLead.Milliseconds(),
			"handoffs", snap.HandoffCount,
			"animated_moments", snap.AnimatedMoments,
			"fallback_moments", snap.FallbackMoments,
			"holds", snap.HoldCount,
			"hold_ms", snap.HoldDuration.Milliseconds(),
		)
	}
}

func (s *Service) noteWarmExecuted(breakReturn bool) {
	if s == nil {
		return
	}
	s.vis.mu.Lock()
	s.vis.snap.WarmCount++
	if breakReturn {
		s.vis.snap.BreakReturnWarms++
	}
	s.vis.mu.Unlock()
}
