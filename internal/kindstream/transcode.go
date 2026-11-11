package kindstream

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
)

// OpenFunc opens one kindstream ByteSource. It is the leaf-level opener a
// resolver hands to the transcode bridge: called lazily, once per open, with
// the session's context so cancellation and stream-limit accounting apply.
type OpenFunc func(ctx context.Context) (ByteSource, error)

// TranscodeSource adapts a kindstream ByteSource opener into the opener shape
// the playback session pipeline expects (spec §15.1). A plugin video/audio
// leaf therefore becomes a normal transcoder input: the existing decision,
// HLS/DASH and probe machinery runs unchanged over the returned MediaSource.
//
// releaseID names the underlying source for session accounting and snapshots;
// when empty the ByteSource's Name is used. The ByteSource is opened on the
// first call to the returned function, so a caller that never plays pays
// nothing.
func TranscodeSource(open OpenFunc, releaseID string) func(ctx context.Context) (core.MediaSource, error) {
	return func(ctx context.Context) (core.MediaSource, error) {
		bs, err := open(ctx)
		if err != nil {
			return nil, err
		}
		id := releaseID
		if id == "" {
			id = bs.Name()
		}
		return MediaSource(bs, id), nil
	}
}
