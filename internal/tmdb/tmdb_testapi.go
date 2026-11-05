// These helpers support offline acceptance tests across package boundaries.
// Runtime callers use the production APIs in the other files.
package tmdb

import (
	"time"
)

func WithRateLimit(perSec float64, burst int) Option {
	return func(o *options) { o.ratePerSec, o.burst = perSec, burst }
}
func WithCacheTTL(d time.Duration) Option { return func(o *options) { o.cacheTTL = d } }
