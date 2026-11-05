package sessions

import (
	"context"
	"github.com/braedonsaunders/ether/internal/core"
)

// DistributedHLS preserves broker authentication and stable public URLs. The
// implementation must retain uncertain starts and fence all replacements.
type DistributedHLS interface {
	Supports(core.HLSRequest) bool
	Allocated(string) bool
	StartDistributedHLS(context.Context, int64, string, string, core.HLSRequest) (core.HLSSession, error)
	Session(string) (core.HLSSession, bool)
	Stop(string)
}
type distributedTranscoder struct {
	local  core.Transcoder
	remote DistributedHLS
}

func (d *distributedTranscoder) Probe(c context.Context, u string) (*core.MediaInfo, error) {
	return d.local.Probe(c, u)
}
func (d *distributedTranscoder) Decide(i core.MediaInfo, c core.ClientCaps, o core.DecideOptions) core.Decision {
	return d.local.Decide(i, c, o)
}
func (d *distributedTranscoder) HWAccel() core.HWAccel { return d.local.HWAccel() }
func (d *distributedTranscoder) Session(id string) (core.HLSSession, bool) {
	if d.remote.Allocated(id) {
		return d.remote.Session(id)
	}
	return d.local.Session(id)
}
func (d *distributedTranscoder) Stop(id string) {
	if d.remote.Allocated(id) {
		d.remote.Stop(id)
	}
	d.local.Stop(id)
}
func (d *distributedTranscoder) StartHLS(c context.Context, q core.HLSRequest) (core.HLSSession, error) {
	if d.remote.Allocated(q.SessionID) {
		return nil, core.ErrNotAvailable
	}
	return d.local.StartHLS(c, q)
}
