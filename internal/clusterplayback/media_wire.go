package clusterplayback

import (
	"encoding/json"
	"github.com/braedonsaunders/ether/internal/core"
)

const maxPreparedKeyframes = 1024

// Complete indexes are transported separately because MediaInfo deliberately
// excludes its potentially large index from ordinary JSON. Never truncate it.
func validPreparedKeyframes(info core.MediaInfo) bool {
	if len(info.Keyframes) > maxPreparedKeyframes {
		return false
	}
	for i, point := range info.Keyframes {
		if point.Offset < 0 || point.Time < 0 || i > 0 && (point.Offset < info.Keyframes[i-1].Offset || point.Time < info.Keyframes[i-1].Time) {
			return false
		}
	}
	return true
}

type preparedAlias PreparedSource

func (p PreparedSource) MarshalJSON() ([]byte, error) {
	if !validPreparedKeyframes(p.Info) {
		return nil, ErrDenied
	}
	return json.Marshal(struct {
		preparedAlias
		Keyframes []core.KeyframePoint `json:"keyframes"`
	}{preparedAlias: preparedAlias(p), Keyframes: p.Info.Keyframes})
}
func (p *PreparedSource) UnmarshalJSON(raw []byte) error {
	var wire struct {
		preparedAlias
		Keyframes []core.KeyframePoint `json:"keyframes"`
	}
	if json.Unmarshal(raw, &wire) != nil {
		return ErrDenied
	}
	out := PreparedSource(wire.preparedAlias)
	out.Info.Keyframes = wire.Keyframes
	if !validPreparedKeyframes(out.Info) {
		return ErrDenied
	}
	out.Info = core.CloneMediaInfo(out.Info)
	*p = out
	return nil
}

type capabilityRequest struct {
	Request   core.HLSRequest
	Keyframes []core.KeyframePoint `json:"keyframes"`
}
