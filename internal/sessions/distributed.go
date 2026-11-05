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

// Keep the local transcoder's optional interfaces visible to existing code.
func (b *Broker) transcodeSession(id string) (core.HLSSession, bool) {
	if d := b.deps.DistributedHLS; d != nil && d.Allocated(id) {
		return d.Session(id)
	}
	return b.deps.Transcoder.Session(id)
}
func (b *Broker) transcodeStop(id string) {
	if d := b.deps.DistributedHLS; d != nil && d.Allocated(id) {
		d.Stop(id)
	}
	b.deps.Transcoder.Stop(id)
}
