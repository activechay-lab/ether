package clusterplayback

import (
	"context"
	"fmt"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/nodewire"
)

var errPreparationCoordinator = fmt.Errorf("%w: preparation coordinator", ErrDenied)

// A source manager deliberately deduplicates a viewer/device/title lifetime.
// Frontends share that exact protected lifetime while retaining separate,
// immutable operation identities and retirement obligations.
func (r *Runtime) sharePreparation(ctx context.Context, peer nodewire.Identity, q PrepareRequest, key string, hash [32]byte, previous *preparation) (sourceResponse, error) {
	// A preparation is a source responsibility, not execution authority. Each
	// authenticated frontend gets an immutable operation alias for the same
	// lifetime; placement and coordinator fencing still guard execution.
	viewer, err := r.o.ActiveViewer(ctx, q.Viewer)
	if err != nil || viewer == nil || viewer.ID != q.Viewer {
		return sourceResponse{}, ErrDenied
	}
	r.mu.Lock()
	if r.closed || previous.retiring || !previous.finished || previous.running || r.preparations[key] != nil || len(r.preparations) >= r.o.HistoryCapacity {
		r.mu.Unlock()
		return sourceResponse{}, ErrPending
	}
	previous.operations++
	result := previous.result
	r.mu.Unlock()
	defer func() { r.mu.Lock(); previous.operations--; r.mu.Unlock() }()
	ps, ok := r.o.Playback.Session(result.PlayID)
	if !ok || ps == nil || ps.UserID != q.Viewer || ps.ClientID != q.Caps.ClientID || ps.Key != q.Key || core.SourceRevision(ps) != result.Revision || r.o.SourcePolicy(ctx, viewer, ps) != nil {
		return sourceResponse{}, ErrDenied
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || previous.retiring || r.preparations[key] != nil || len(r.preparations) >= r.o.HistoryCapacity {
		return sourceResponse{}, ErrPending
	}
	copy := *previous
	copy.peer, copy.request, copy.hash = peer, q, hash
	copy.operations = 0
	copy.activityVersion = new(uint64)
	r.preparations[key] = &copy
	return sourceResponse{Prepared: copy.result}, nil
}

// An obsolete preparation cannot close a source protected by a different live
// coordinator. Terminal reconciliation continues after execution authority ends.
func (r *Runtime) preparationController(ctx context.Context, peer nodewire.Identity, entry *preparation, retiring bool) error {
	r.mu.Lock()
	type binding struct {
		route    Route
		retiring bool
	}
	var routes []binding
	for _, source := range r.sources {
		if source.life == entry.life && source.route.PlayID == entry.result.PlayID {
			routes = append(routes, binding{source.route, source.retiring})
		}
	}
	r.mu.Unlock()
	for _, bound := range routes {
		route := bound.route
		record, err := r.o.Placement.Lookup(ctx, route.Fence.Session)
		if err != nil {
			return err
		}
		if record.Retired || !record.Authority {
			continue
		}
		c, err := r.CurrentCoordinator(ctx, route.Fence.Session)
		if err != nil {
			return err
		}
		if c.Identity != peer {
			return errPreparationCoordinator
		}
		if retiring && !bound.retiring {
			return ErrPending
		}
	}
	return nil
}
