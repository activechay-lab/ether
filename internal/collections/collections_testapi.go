// These helpers support offline acceptance tests across package boundaries.
// Runtime callers use the production APIs in the other files.
package collections

import (
	"time"
)

// WithIndexTTL sets how often the catalogue index checks for changes
// (default 2m; 0 checks on every use).
func WithIndexTTL(d time.Duration) Option { return func(s *Service) { s.opts.indexTTL = d } }

// WithLiveTTL sets how long a live collection's evaluation is reused
// (default 2m).
func WithLiveTTL(d time.Duration) Option { return func(s *Service) { s.liveTTL = d } }

// WithoutDefaults skips seeding the starter collections (tests).
func WithoutDefaults() Option { return func(s *Service) { s.opts.noSeed = true } }
