package sessions

import (
	"context"
)

// renewActive is called only after a GET has established or found a session
// and passed grant, viewer and current media policy checks. HEAD probes never
// keep an abandoned capability alive. The issuing grant is never extended.
func (s *lazyStreams) renewActive(ctx context.Context, hash, reference string, viewerID int64, sessionID string) error {
	return s.renewActiveBinding(ctx, hash, hash, reference, viewerID, sessionID)
}
