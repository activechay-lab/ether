package clusterplayback

import (
	"context"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/nodesource"
	"github.com/braedonsaunders/ether/internal/nodewire"
	"github.com/braedonsaunders/ether/internal/placement"
	"github.com/braedonsaunders/ether/internal/playbackcontinuity"
)

// ReopenRequest is an immutable same-owner operation. It grants no encoder start
// or new placement authority. Operation remains owned even if the reply is lost.
type ReopenRequest struct {
	Old       playbackcontinuity.SourceBinding
	Operation string
}
type ReopenedSource struct {
	Owner          nodewire.Identity
	Operation      string
	Responsibility *ReopenResponsibility `json:"-"`
	Prepared       PreparedSource
	Proof          string
}
type reopenRecord struct {
	request   ReopenRequest
	peer      nodewire.Identity
	oldRoute  Route
	prepared  PreparedSource
	size      int64
	proof     string
	busy      bool
	nextFence placement.Fence
}

func (r *Runtime) ReopenSource(ctx context.Context, old playbackcontinuity.SourceBinding, operation string) (ReopenedSource, error) {
	owner := nodewire.Identity{Cluster: r.o.Self.Cluster, NodeID: old.Owner.NodeID, Boot: old.Owner.Boot}
	result := ReopenedSource{Owner: owner, Operation: operation}
	if operation == "" || len(operation) > 128 || old.PlayID == "" || old.Revision == "" || old.Fence.Generation <= 0 {
		return result, ErrDenied
	}
	life, e := r.reopenResponsibility(owner, old, operation)
	if e != nil {
		return result, e
	}
	result.Responsibility = life
	r.mu.Lock()
	sealed := life.retiring || life.retired
	r.mu.Unlock()
	if sealed {
		return result, ErrPending
	}
	reply, e := r.sourceCall(ctx, Route{Source: owner}, sourceRequest{Op: "reopen", Reopen: &ReopenRequest{Old: old, Operation: operation}})
	result.Prepared = reply.Prepared
	result.Proof = reply.Proof
	if e != nil {
		life.Retire()
	}
	return result, e
}
func (r *Runtime) reopenSource(ctx context.Context, peer nodewire.Identity, q *ReopenRequest) (sourceResponse, error) {
	if q == nil || q.Operation == "" || len(q.Operation) > 128 || q.Old.Owner != (placement.Target{NodeID: r.o.Self.NodeID, Boot: r.o.Self.Boot}) || q.Old.PlayID == "" || q.Old.Revision == "" || r.o.Eligible(ctx, peer, config.RoleServer) != nil || r.o.Eligible(ctx, r.o.Self, config.RoleSource) != nil {
		return sourceResponse{}, ErrDenied
	}
	key := peer.NodeID + "/" + peer.Boot + "/" + q.Operation
	r.mu.Lock()
	prior := r.reopens[key]
	var entry *reopenRecord
	if prior != nil {
		if prior.request != *q || prior.peer != peer || prior.busy {
			r.mu.Unlock()
			return sourceResponse{}, ErrPending
		}
		if prior.proof != "" {
			reply := sourceResponse{Prepared: prior.prepared, Proof: prior.proof}
			r.mu.Unlock()
			if _, e := r.preparationCurrent(ctx, peer, q.Operation); e != nil {
				return sourceResponse{}, e
			}
			return reply, nil
		}
		prior.busy = true
		entry = prior
	} else {
		if len(r.reopens) >= r.o.HistoryCapacity {
			r.mu.Unlock()
			return sourceResponse{}, ErrDenied
		}
		entry = &reopenRecord{request: *q, peer: peer, busy: true}
		r.reopens[key] = entry
	}
	r.mu.Unlock()
	defer func() { r.mu.Lock(); entry.busy = false; r.mu.Unlock() }()
	route, e := r.route(ctx, q.Old.Fence.Session)
	if e != nil || route.Fence != q.Old.Fence || route.PlayID != q.Old.PlayID || route.Revision != q.Old.Revision || route.Source != r.o.Self {
		return sourceResponse{}, ErrDenied
	}
	record, e := r.o.Placement.Lookup(ctx, q.Old.Fence.Session)
	if e != nil || record.Fence != q.Old.Fence || !record.Retired {
		return sourceResponse{}, ErrPending
	}
	r.mu.Lock()
	source := r.sources[sourceKey(q.Old.Fence)]
	var original *preparation
	// This scan is bounded by the explicit session/preparation history quota,
	// independent of catalogue or library size. No provider/library query occurs.
	for _, p := range r.preparations {
		if p.result.PlayID == q.Old.PlayID && p.result.Revision == q.Old.Revision && p.viewer == route.Viewer && p.peer.NodeID == q.Old.Fence.Controller.ID && p.peer.Boot == q.Old.Fence.Controller.Boot && p.finished {
			original = p
			break
		}
	}
	if source == nil || source.life == nil || original == nil {
		r.mu.Unlock()
		return sourceResponse{}, ErrDenied
	}
	life := source.life
	oldInfo, request := original.result.Info, original.request
	r.mu.Unlock()
	select {
	case <-life.Done():
		if life.Err() != nil {
			return sourceResponse{}, ErrPending
		}
	default:
		return sourceResponse{}, ErrPending
	}
	if r.bytes.Pending(scopeForRoute(route)) != 0 || oldInfo.Size <= 0 {
		return sourceResponse{}, ErrPending
	}
	request.Operation = q.Operation
	request.ReleaseID = original.result.ReleaseID
	request.Exact = true
	reply, e := r.prepare(ctx, peer, &request)
	if e != nil {
		return sourceResponse{}, e
	}
	if reply.Prepared.Owner != r.o.Self || reply.Prepared.PlayID == "" || reply.Prepared.Revision != q.Old.Revision || reply.Prepared.Info.Size != oldInfo.Size {
		return sourceResponse{}, ErrDenied
	}
	proof, e := randomToken()
	if e != nil {
		return sourceResponse{}, e
	}
	r.mu.Lock()
	entry.oldRoute = route
	entry.prepared = reply.Prepared
	entry.size = oldInfo.Size
	entry.proof = proof
	r.mu.Unlock()
	return sourceResponse{Prepared: reply.Prepared, Proof: proof}, nil
}

// VerifyReopen is the concrete same-owner SourceReopenVerifier provider. It
// rechecks current new-fence authority and exact source policy on every call.
// Cross-host/boot revision equivalence is deliberately unsupported.
func (r *Runtime) VerifyReopen(ctx context.Context, old, next playbackcontinuity.SourceBinding, proof string) error {
	if old.Owner != next.Owner || old.Revision != next.Revision || proof == "" {
		return ErrDenied
	}
	owner := nodewire.Identity{Cluster: r.o.Self.Cluster, NodeID: old.Owner.NodeID, Boot: old.Owner.Boot}
	_, e := r.sourceCall(ctx, Route{Source: owner}, sourceRequest{Op: "verify-reopen", OldSource: &old, NextSource: &next, Proof: proof})
	return e
}
func (r *Runtime) verifyReopen(ctx context.Context, peer nodewire.Identity, m sourceRequest) (sourceResponse, error) {
	if m.OldSource == nil || m.NextSource == nil || m.Proof == "" {
		return sourceResponse{}, ErrDenied
	}
	old, next := *m.OldSource, *m.NextSource
	if old.Owner != next.Owner || old.Owner != (placement.Target{NodeID: r.o.Self.NodeID, Boot: r.o.Self.Boot}) || old.Revision != next.Revision || next.Fence.Session != old.Fence.Session || next.Fence.Generation <= old.Fence.Generation || peer.NodeID != next.Fence.Controller.ID || peer.Boot != next.Fence.Controller.Boot {
		return sourceResponse{}, ErrDenied
	}
	r.mu.Lock()
	var selected *reopenRecord
	for _, entry := range r.reopens {
		if entry.proof == m.Proof && entry.peer == peer && entry.request.Old == old && entry.prepared.PlayID == next.PlayID && entry.prepared.Revision == next.Revision && !entry.busy {
			selected = entry
			break
		}
	}
	if selected == nil {
		r.mu.Unlock()
		return sourceResponse{}, ErrDenied
	}
	operation, size := selected.request.Operation, selected.size
	r.mu.Unlock()
	current, e := r.preparationCurrent(ctx, peer, operation)
	if e != nil || current.PlayID != next.PlayID || current.Revision != next.Revision || current.Info.Size != size {
		return sourceResponse{}, ErrDenied
	}
	record, e := r.o.Placement.Lookup(ctx, next.Fence.Session)
	if e != nil || record.Fence != next.Fence || record.Retired || !record.Authority {
		return sourceResponse{}, ErrDenied
	}
	route, e := r.route(ctx, next.Fence.Session)
	if e != nil || route.Fence != next.Fence || route.Source != r.o.Self || route.PlayID != next.PlayID || route.Revision != next.Revision {
		return sourceResponse{}, ErrDenied
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if selected.nextFence.Generation != 0 && selected.nextFence != next.Fence {
		return sourceResponse{}, ErrDenied
	}
	selected.nextFence = next.Fence
	return sourceResponse{Phase: "verified"}, nil
}

var _ playbackcontinuity.SourceReopenVerifier = (*Runtime)(nil)

func scopeForRoute(route Route) nodesource.Scope {
	return nodesource.Scope{SourceGeneration: route.SourceGeneration, Fence: route.Fence, Viewer: viewerString(route.Viewer), Revision: route.Revision}
}
