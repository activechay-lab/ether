package collections

import (
	"time"
)

// WithEphemeralLimits bounds the ephemeral registry (tests).
func WithEphemeralLimits(ttl time.Duration, max int) Option {
	return func(s *Service) { s.vibeTTL, s.vibeMax = ttl, max }
}
