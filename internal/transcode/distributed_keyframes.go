package transcode

import (
	"encoding/json"

	"github.com/braedonsaunders/ether/internal/core"
)

const MaxDistributedKeyframes = 1024

// ValidateDistributedKeyframes checks a complete lossless index. It never sorts,
// truncates or downsamples source metadata to make a request fit admission.
func ValidateDistributedKeyframes(info core.MediaInfo) bool {
	if len(info.Keyframes) > MaxDistributedKeyframes {
		return false
	}
	for i, p := range info.Keyframes {
		if p.Offset < 0 || p.Time < 0 {
			return false
		}
		if i > 0 && (p.Offset < info.Keyframes[i-1].Offset || p.Time < info.Keyframes[i-1].Time) {
			return false
		}
	}
	return true
}
func distributedRequestSize(req core.HLSRequest) bool {
	// MediaInfo intentionally excludes indexes from ordinary JSON. Admission must
	// count the same explicit complete index that the private protocol transports.
	payload := struct {
		Request   core.HLSRequest
		Keyframes []core.KeyframePoint
	}{req, req.Info.Keyframes}
	b, err := json.Marshal(payload)
	return err == nil && len(b) <= (64<<10)-8192
}
