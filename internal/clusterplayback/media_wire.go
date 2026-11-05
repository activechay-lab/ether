package clusterplayback

import (
	"encoding/json"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/remotetranscode"
)

const maxPreparedKeyframes = remotetranscode.MaxKeyframes

func validPreparedKeyframes(info core.MediaInfo) bool { return remotetranscode.ValidateKeyframes(info) }

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
