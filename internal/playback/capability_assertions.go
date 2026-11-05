package playback

import "github.com/braedonsaunders/ether/internal/core"

// Assert extension contracts at the producer so a removed method cannot
// silently make downstream consumers select their fallback.
var (
	_ core.Prewarmer                = (*Manager)(nil)
	_ core.ChannelPreparer          = (*Manager)(nil)
	_ core.PlaybackDecisionRecorder = (*Manager)(nil)
)
