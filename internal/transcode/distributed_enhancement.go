package transcode

import "github.com/braedonsaunders/ether/internal/core"

// SupportsDistributedEnhancement accepts inert observation metadata only. A
// genuine default CPU decision reports RTX disabled/off with a reason; retain
// that status unchanged rather than discarding it to fake compatibility.
func SupportsDistributedEnhancement(e core.VideoEnhancement) bool {
	if e == (core.VideoEnhancement{}) {
		return true
	}
	return e.State == "off" && e.Width == 0 && e.Height == 0 && e.Quality == 0 && len(e.Backend) <= 128 && len(e.Reason) <= 1024
}
