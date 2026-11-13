package transcode

import (
	"context"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// Keep speculative output isolated: a Matroska client retains the initial
// header, and both runs use the same copy contract and title clock.
func (s *Session) startResumeWarm(ctx context.Context, req core.HLSRequest) {
	if req.ResumeHint <= 0 || req.StartOffset != 0 || req.LiveInput || req.Resume != nil ||
		!s.decision.VideoCopy || s.container != ContainerMatroska || s.isDASH() || len(s.rungs) > 1 ||
		req.ResumeHint >= s.info.Duration || s.plan == nil {
		return
	}
	at := s.plan.IndexAt(req.ResumeHint)
	if at <= 2 || at >= s.total {
		return
	}
	work, err := s.encoder.reserve(1)
	if err != nil {
		return
	}
	life, err := s.m.reserveEncoder()
	if err != nil {
		work[0].finish()
		return
	}
	req.SessionID = s.id + "-resume-" + randomHex(4)
	req.ResumeHint = 0
	// The parent's work ticket keeps its source lease alive until the child is
	// fully drained. A lease cannot be claimed by two encoder lifetimes.
	req.InputLease = nil
	hs, err := s.m.startHLS(ctx, req, life, at)
	if err != nil {
		life.seal()
		go func() { <-life.done; work[0].finish() }()
		return
	}
	child := hs.(*Session)
	s.mu.Lock()
	if s.closed || s.encoder.isSealed() {
		child.encoder.seal()
	} else {
		s.resumeWarm, s.resumeWarmAt = child, at
	}
	s.mu.Unlock()
	go func() { <-life.done; work[0].finish() }()
	// One short-lived extra copy run, bounded by both time and aheadLimit.
	time.AfterFunc(5*time.Second, func() {
		s.mu.Lock()
		if !s.resumeWarmUsed && s.resumeWarm == child {
			s.resumeWarm = nil
			child.encoder.seal()
		}
		s.mu.Unlock()
	})
}

func (s *Session) resumeSession(n int) *Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	child := s.resumeWarm
	if child == nil || s.closed || n < s.resumeWarmAt {
		return nil
	}
	// A different seek should use the normal restart path, rather than promote
	// speculative output that the player never requested.
	if !s.resumeWarmUsed && n > s.resumeWarmAt+1 {
		return nil
	}
	s.resumeWarmUsed = true
	child.mu.Lock()
	child.speculative = false
	child.mu.Unlock()
	return child
}
