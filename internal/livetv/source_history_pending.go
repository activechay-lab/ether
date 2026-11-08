package livetv

import "time"

// Placeholder for the source history store (source_history.go, WS3a) so this
// branch builds until it lands; the merge deletes this file.

type SourceHistory struct {
	Failures       int
	LastOutcome    string
	MediaSeconds   float64
	FormatChanges  int64
	MaxKeyframeGap time.Duration
	Stalls         int64
	Reconnects     int64
	DamageEvents   int64
}

func (h SourceHistory) DamagePerHour() float64 { return 0 }

func (s *Service) sourceHistory(string, StreamSource) (SourceHistory, bool) {
	return SourceHistory{}, false
}
func (s *Service) noteSourceMode(string, StreamSource, string, string) {}
