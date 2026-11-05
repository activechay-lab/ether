package playback

import (
	"github.com/braedonsaunders/ether/internal/core"
)

// TimeMapFor builds the TimeMap of a probed source from its MediaInfo,
// including the container keyframe index when one was read, so callers
// outside playback (resume prefetch, trickplay) map with the same points.
func TimeMapFor(info core.MediaInfo) TimeMap {
	return TimeMap{Size: info.Size, Duration: info.Duration, Points: indexPointsOf(info.Keyframes)}
}
