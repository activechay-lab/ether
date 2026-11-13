package transcode

import (
	"fmt"
	"time"
)

// runWatchdog observes actual encoder advancement, not periodic progress
// reports with the same values. A paused encoder and an unrequested session
// are deliberately excluded. Its state belongs only to the watch goroutine.
type runWatchdog struct {
	at       time.Time
	frame    int64
	out      time.Duration
	segments int
}

func (w *runWatchdog) check(s *Session, r *run) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Reserve half the request budget for a software retry. Progress frames
	// alone do not satisfy DASH startup: a driver may advance forever without
	// publishing a playable fragment. This applies only to the active,
	// unpaused hardware run and is permanently bounded by forceCPU.
	budget := min(s.m.opts.manifestWait, s.m.opts.segmentTimeout) / 2
	if s.isDASH() && !s.liveInput && !s.closed && s.cur == r && !r.finished && !r.stopped && !r.stalled && !r.paused && r.hw && !s.forceCPU && !s.inputFailed && r.produced == 0 && budget > 0 && time.Since(r.started) >= budget {
		select {
		case <-r.proc.Done():
			return
		default:
		}
		s.forceCPU = true
		r.stalled = true
		err := fmt.Errorf("transcode: hardware DASH startup exceeded %s", budget)
		s.log.Warn("hardware encoder failed; falling back to CPU", "run", r.seq, "segment", r.startSeg)
		r.proc = failedProc{Process: r.proc, err: err}
		r.proc.Stop()
		return
	}
	timeout := s.m.opts.stallTimeout
	if timeout <= 0 || s.liveInput || s.closed || s.cur != r || r.finished || r.stopped || r.stalled {
		return
	}
	now := time.Now()
	r.progressMu.Lock()
	progress := r.progress
	r.progressMu.Unlock()
	if w.at.IsZero() || len(s.inflight) == 0 || r.paused || progress.Frame > w.frame || progress.OutTime > w.out || r.produced > w.segments {
		w.at = now
		w.frame = progress.Frame
		w.out = progress.OutTime
		w.segments = r.produced
		return
	}
	if now.Sub(w.at) < timeout {
		return
	}
	select {
	case <-r.proc.Done():
		return
	default:
	}
	r.stalled = true
	err := fmt.Errorf("transcode: encoder made no progress for %s", timeout)
	s.log.Warn("stalled encoder; restarting", "run", r.seq, "segment", r.next(), "timeout", timeout)
	r.proc = failedProc{Process: r.proc, err: err}
	r.proc.Stop()
}

// takeStallRetry caps even short-progress/repeated-stall cycles.
func (s *Session) takeStallRetry(now time.Time) bool {
	kept := s.stallRestarts[:0]
	for _, at := range s.stallRestarts {
		if now.Sub(at) < 2*time.Minute {
			kept = append(kept, at)
		}
	}
	s.stallRestarts = kept
	if len(kept) >= 3 {
		return false
	}
	s.stallRestarts = append(kept, now)
	return true
}
