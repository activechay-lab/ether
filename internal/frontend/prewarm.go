package frontend

import (
	"context"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// PrewarmOptions tune a speculative warm-up of a title before the viewer
// presses Play. The options stay owned by core.
type PrewarmOptions = core.PrewarmOptions

// PrewarmDetail is the warm-up reason for an opened detail page.
const PrewarmDetail = core.PrewarmDetail

// Prewarmer is implemented by playback managers that can resolve, open,
// probe and pre-buffer a title ahead of Play. It reports whether a
// warm-up was scheduled.
type Prewarmer interface {
	Prewarm(ctx context.Context, user *User, key RatingKey, caps ClientCaps, opts PrewarmOptions) bool
}

// WarmProber returns the probed media info a completed warm-up already
// produced for this user, client and title.
type WarmProber interface {
	WarmProbeInfo(user *User, caps ClientCaps, key RatingKey) (MediaInfo, bool)
}

// WarmProbeWaiter waits up to a bounded budget for an already-scheduled
// warm probe to finish.
type WarmProbeWaiter interface {
	WaitWarmProbe(ctx context.Context, user *User, caps ClientCaps, key RatingKey, budget time.Duration) (MediaInfo, bool)
}

// MetadataProber obtains exact pre-play source facts without admitting a
// live session. An error means facts are not ready.
type MetadataProber interface {
	ProbeMetadata(context.Context, *User, RatingKey, ClientCaps) error
}
