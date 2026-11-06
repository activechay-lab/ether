package clusterplayback

import (
	"context"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/placement"
)

// BindLocalClientSession publishes the exact local source's owner before a full
// node returns a MediaBrowser session. It allocates no encoder or source lease.
// RemotePlayback publishes its source-owner route through BindClientSession.
func (r *Runtime) BindLocalClientSession(ctx context.Context, id string, snapshot *core.PlaySession) error {
	if snapshot == nil || snapshot.ID == "" || snapshot.UserID <= 0 || r.o.Playback == nil || !r.o.Roles.Has(config.RoleServer) || !r.o.Roles.Has(config.RoleSource) || r.o.Eligible(ctx, r.o.Self, config.RoleServer) != nil {
		return ErrDenied
	}
	if parsed, ok := neutralMediaSession("/sessions/media/" + id + "/grant/file"); !ok || parsed != id {
		return ErrDenied
	}
	native, ok := r.o.Playback.Session(snapshot.ID)
	if !ok || native == nil || native.UserID != snapshot.UserID || native.ClientID != snapshot.ClientID || native.Key != snapshot.Key {
		return ErrDenied
	}
	revision := core.SourceRevision(native)
	if revision == "" || revision != core.SourceRevision(snapshot) {
		return core.ErrSourceChanged
	}
	return r.putRoute(ctx, Route{Fence: placement.Fence{Session: id, Controller: placement.Controller{ID: r.o.Self.NodeID, Boot: r.o.Self.Boot}}, Source: r.o.Self, Viewer: native.UserID, PlayID: native.ID, Revision: revision})
}
