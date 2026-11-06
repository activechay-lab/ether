package sessions

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// Neutral controls run synchronously on the current coordinator. Forwarding
// happens before snapshot restoration, which is reserved for lease takeover.
type neutralControl interface {
	BindNeutralControl(func(context.Context, frontend.SessionAccess, string, string, frontend.Timeline, frontend.StreamRequest) (string, error)) error
	ForwardNeutralControl(context.Context, frontend.SessionAccess, string, string, frontend.Timeline, frontend.StreamRequest) (string, bool, error)
}

func (b *Broker) forwardNeutralControl(ctx context.Context, a frontend.SessionAccess, id, operation string, timeline frontend.Timeline, request frontend.StreamRequest) (string, bool, error) {
	remote, ok := b.deps.DistributedHLS.(neutralControl)
	if !ok || !neutralProtocol(a.Protocol) {
		return "", false, nil
	}
	viewer, err := b.viewer(ctx, a.Viewer)
	if err != nil {
		return "", true, err
	}
	if a.Owner && (!viewer.IsOwner || viewer.Delegated) {
		return "", true, core.ErrUnauthorized
	}
	a.Viewer = viewer
	return remote.ForwardNeutralControl(ctx, a, id, operation, timeline, request)
}

func (b *Broker) neutralControl(ctx context.Context, a frontend.SessionAccess, id, operation string, timeline frontend.Timeline, request frontend.StreamRequest) (string, error) {
	switch operation {
	case "stop":
		return "", b.Stop(ctx, a, id)
	case "report":
		return "", b.Report(ctx, a, id, timeline)
	case "stream":
		return b.StartStream(ctx, a, id, request)
	default:
		return "", core.ErrNotAvailable
	}
}
