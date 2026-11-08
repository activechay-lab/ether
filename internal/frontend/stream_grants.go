package frontend

import (
	"context"
	"github.com/braedonsaunders/ether/internal/streamgrant"
)

const StreamGrantParam = streamgrant.StreamGrantParam

type StreamGrantBinding = streamgrant.GrantBinding

// AuthenticateStreamGrant verifies the bound media capability and rechecks
// the active viewer for each request, including already-cached media.
func AuthenticateStreamGrant(ctx context.Context, method, token string, binding StreamGrantBinding, active func(context.Context, int64) *Viewer) (*Viewer, bool) {
	return streamgrant.AuthenticateGrant(ctx, method, token, binding, active)
}
func StreamGrantURL(raw, token string) string { return streamgrant.GrantURL(raw, token) }
