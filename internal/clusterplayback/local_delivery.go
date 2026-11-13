package clusterplayback

import (
	"context"
	"strings"

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

func neutralMediaSession(path string) (string, bool) {
	const prefix = "/sessions/media/"
	if len(path) > 8192 || !strings.HasPrefix(path, prefix) {
		return "", false
	}
	parts := strings.Split(strings.TrimPrefix(path, prefix), "/")
	if len(parts) < 3 || len(parts) > 8 {
		return "", false
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || len(part) > 512 {
			return "", false
		}
		for _, c := range part {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
				return "", false
			}
		}
	}
	return parts[0], true
}
