package clusterplayback

import (
	"context"
	"github.com/braedonsaunders/ether/internal/nodewire"
	"time"
)

// A source manager deliberately deduplicates a viewer/device/title lifetime.
// A new coordinator reuses that exact protected lifetime rather than opening a
// second preparation or reclaiming it when the old controller disappears.
func (r *Runtime) transferPreparation(ctx context.Context, peer nodewire.Identity, q PrepareRequest, key string, hash [32]byte, previous *preparation) (sourceResponse, error) {
	r.mu.Lock()
	var bound *ownedSource
	for _, source := range r.sources {
		if source.life == previous.life && source.route.PlayID == previous.result.PlayID && !source.retiring {
			bound = source
			break
		}
	}
	finished := previous.finished && !previous.retiring && !previous.running
	r.mu.Unlock()
	if !finished || bound == nil || previous.peer == peer {
		return sourceResponse{}, ErrPending
	}
	route, err := r.route(ctx, bound.route.Fence.Session)
	if err != nil || route != bound.route {
		return sourceResponse{}, ErrPending
	}
	c, err := r.CurrentCoordinator(ctx, route.Fence.Session)
	if err != nil || c.Identity != peer || c.Epoch <= 1 || !c.Deadline.After(time.Now().Add(r.o.ClockSkew)) {
		return sourceResponse{}, ErrDenied
	}
	record, err := r.o.Placement.Lookup(ctx, route.Fence.Session)
	if err != nil || record.Fence != route.Fence || record.Retired || !record.Authority {
		return sourceResponse{}, ErrDenied
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || previous.retiring || !previous.finished || bound.retiring || r.preparations[key] != nil || len(r.preparations) >= r.o.HistoryCapacity {
		return sourceResponse{}, ErrPending
	}
	copy := *previous
	copy.peer, copy.request, copy.hash = peer, q, hash
	copy.operations = 0
	r.preparations[key] = &copy
	return sourceResponse{Prepared: copy.result}, nil
}

// An obsolete preparation cannot close a source protected by a different live
// coordinator. Terminal reconciliation continues after execution authority ends.
func (r *Runtime) preparationController(ctx context.Context, peer nodewire.Identity, entry *preparation) error {
	r.mu.Lock()
	var routes []Route
	for _, source := range r.sources {
		if source.life == entry.life && source.route.PlayID == entry.result.PlayID {
			routes = append(routes, source.route)
		}
	}
	r.mu.Unlock()
	for _, route := range routes {
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
			return ErrDenied
		}
	}
	return nil
}
