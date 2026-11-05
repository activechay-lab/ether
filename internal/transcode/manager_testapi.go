// These helpers support offline acceptance tests across package boundaries.
// Runtime callers use the production APIs in the other files.
package transcode

import (
	"time"
)

// WithBuffering tunes seeking and disk use: a request more than
// restartAhead past the encoder restarts ffmpeg there (default 24s); ffmpeg
// is suspended when maxAhead in front of the player (default 2m, 0 = never);
// segments more than keepBehind behind the player are deleted (default 3m,
// 0 = never).
func WithBuffering(restartAhead, maxAhead, keepBehind time.Duration) Option {
	return func(o *options) {
		o.restartAhead, o.maxAhead, o.keepBehind = restartAhead, maxAhead, keepBehind
	}
}
