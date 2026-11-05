package transcode

import (
	"time"
)

// WithAudioMatchBudget bounds the best-effort loudness analysis run before a
// source swap (default 1.5s). Integration tests on slow or race-instrumented
// machines raise it; production keeps the short budget so a swap never waits.
func WithAudioMatchBudget(d time.Duration) Option {
	return func(o *options) { o.audioMatchBudget = d }
}

// withRungDwell sets how long one rung's run keeps the shared encoder
// before a request on another rung may restart it (default 8s; 0 = switch
// at once). Tests use it; the default suits real players.
func withRungDwell(d time.Duration) Option { return func(o *options) { o.rungDwell = d } }

// WithManifestWait bounds how long a DASH session's first MPD request waits
// for ffmpeg's init segments to learn the exact codecs (default 15s); after
// that the MPD is served with codecs estimated from the probe.
func WithManifestWait(d time.Duration) Option { return func(o *options) { o.manifestWait = d } }

// WithFailureHandler is called (in its own goroutine) when ffmpeg fails
// mid-session, e.g. because the source lost articles; at is the media time
// playback had reached. Playback typically answers with SwapInput.
func WithFailureHandler(f func(sessionID string, at time.Duration, err error)) Option {
	return func(o *options) { o.onFailure = f }
}
func withFinder(f finder) Option { return func(o *options) { o.finder = &f } }
