package livetv

import "time"

// Evidence thresholds over a source's recorded history (which covers about
// the last six hours of media it delivered). One stall, reconnect, failed
// open or format change makes a source flaky; so does damage above one event
// a minute or a keyframe gap past two segments. A source is clean only after
// a minute of observed media with none of that.
const (
	steadyFlakyDamagePerHour = 60
	steadyFlakyKeyframeGap   = 2 * steadySegment
	steadyCleanAfter         = 60 * time.Second
)

// steadyClass classifies a channel source for Steady allocation.
func (s *Service) steadyClass(c Channel, src StreamSource) string {
	if src.ID == "" {
		return steadyClassUnknown
	}
	h, ok := s.sourceHistory(c.ID, src)
	return classifySource(h, ok)
}

func classifySource(h SourceHistory, ok bool) string {
	if !ok {
		return steadyClassUnknown
	}
	switch {
	case h.Stalls > 0, h.Reconnects > 0, h.Failures > 0, h.FormatChanges > 0,
		h.DamagePerHour() > steadyFlakyDamagePerHour, h.MaxKeyframeGap() > steadyFlakyKeyframeGap,
		h.LastOutcome == "failed", h.LastOutcome == "escalated":
		return steadyClassFlaky
	case h.MediaSeconds >= steadyCleanAfter.Seconds():
		return steadyClassClean
	}
	return steadyClassUnknown
}

// allocateSteady is the allocation rule: an output of cost fits when the
// budget holds it; a clean source must also leave room for one more.
func allocateSteady(class string, load, cost, budget float64) string {
	switch {
	case load+cost > budget:
		return steadyRefusedCapacity
	case class == steadyClassClean && load+2*cost > budget:
		return steadyRefusedClean
	}
	return ""
}
