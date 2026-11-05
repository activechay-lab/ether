package clusterplayback

import (
	"context"
	"github.com/braedonsaunders/ether/internal/core"
)

// DecisionTranscoder supplies server-only broker decisions without constructing
// providers, FFmpeg or a local encoder. DistributedHLS owns actual starts.
func (r *Runtime) DecisionTranscoder() (core.Transcoder, error) {
	if r.o.Decide == nil {
		return nil, ErrDenied
	}
	return decisionTranscoder{r: r}, nil
}

type decisionTranscoder struct{ r *Runtime }

func (d decisionTranscoder) Probe(context.Context, string) (*core.MediaInfo, error) {
	return nil, core.ErrNotAvailable
}
func (d decisionTranscoder) Decide(i core.MediaInfo, c core.ClientCaps, o core.DecideOptions) core.Decision {
	return d.r.o.Decide(i, c, o)
}
func (d decisionTranscoder) HWAccel() core.HWAccel { return d.r.o.Hardware }
func (d decisionTranscoder) StartHLS(context.Context, core.HLSRequest) (core.HLSSession, error) {
	return nil, core.ErrNotAvailable
}
func (d decisionTranscoder) Session(id string) (core.HLSSession, bool) { return d.r.Session(id) }
func (d decisionTranscoder) Stop(id string)                            { d.r.Stop(id) }
