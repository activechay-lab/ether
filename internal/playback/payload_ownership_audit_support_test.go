package playback

import (
	"context"

	"time"
)

// releaseLater keeps the payload protected for delay after o is dropped, so a
// replay can share the committed anchor instead of inserting another row.
// A zero delay, or a hash that cannot be shared, retires o immediately.
func (p *payloadStore) releaseLater(ctx context.Context, o payloadOwner, delay time.Duration) error {
	return p.releaseOwnedLater(ctx, o, delay, false, nil)
}
