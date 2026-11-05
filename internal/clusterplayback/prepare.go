package clusterplayback

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/nodewire"
)

// PrepareRequest contains authenticated frontend choices, never provider paths.
// Operation is immutable and boot scoped; uncertain retries use the same payload.
type PrepareRequest struct {
	Operation string
	Viewer    int64
	Key       core.RatingKey
	Caps      core.ClientCaps
	ReleaseID string
	Exact     bool
	Protocol  string
}
type PreparedSource struct {
	Owner                       nodewire.Identity
	PlayID, Revision, ReleaseID string
	Info                        core.MediaInfo
}
type preparation struct {
	peer     nodewire.Identity
	hash     [32]byte
	result   PreparedSource
	life     core.SourceRetirement
	finished bool
	running  bool
}

func (r *Runtime) PrepareSource(ctx context.Context, owner nodewire.Identity, q PrepareRequest) (PreparedSource, error) {
	if q.Protocol != "hls" || q.Operation == "" || len(q.Operation) > 128 || q.Viewer <= 0 {
		return PreparedSource{}, ErrDenied
	}
	reply, e := r.sourceCall(ctx, Route{Source: owner}, sourceRequest{Op: "prepare", Prepare: &q})
	return reply.Prepared, e
}
func (r *Runtime) prepare(ctx context.Context, peer nodewire.Identity, q *PrepareRequest) (sourceResponse, error) {
	if q == nil || q.Protocol != "hls" || q.Viewer <= 0 || q.Operation == "" || len(q.Operation) > 128 || r.o.Eligible(ctx, peer, config.RoleServer) != nil || r.o.Eligible(ctx, r.o.Self, config.RoleSource) != nil {
		return sourceResponse{}, ErrDenied
	}
	raw, e := json.Marshal(q)
	if e != nil || len(raw) > 32<<10 {
		return sourceResponse{}, ErrDenied
	}
	hash := sha256.Sum256(raw)
	key := peer.NodeID + "/" + peer.Boot + "/" + q.Operation
	r.mu.Lock()
	previous := r.preparations[key]
	if previous != nil {
		if previous.peer != peer || previous.hash != hash || !previous.finished {
			r.mu.Unlock()
			return sourceResponse{}, ErrPending
		}
		result := previous.result
		r.mu.Unlock()
		if result.PlayID == "" {
			return sourceResponse{}, ErrPending
		}
		viewer, e := r.o.ActiveViewer(ctx, q.Viewer)
		ps, ok := r.o.Playback.Session(result.PlayID)
		if e != nil || viewer == nil || !ok || ps == nil || core.SourceRevision(ps) != result.Revision || r.o.SourcePolicy(ctx, viewer, ps) != nil {
			return sourceResponse{}, ErrDenied
		}
		return sourceResponse{Prepared: result}, nil
	}
	if r.closed || len(r.preparations) >= r.o.HistoryCapacity {
		r.mu.Unlock()
		return sourceResponse{}, ErrDenied
	}
	entry := &preparation{peer: peer, hash: hash, running: true}
	r.preparations[key] = entry
	r.mu.Unlock()
	defer func() { r.mu.Lock(); entry.running = false; r.mu.Unlock() }()
	viewer, e := r.o.ActiveViewer(ctx, q.Viewer)
	if e != nil || viewer == nil || viewer.ID != q.Viewer {
		return sourceResponse{}, ErrDenied
	}
	ps, life, e := r.o.Playback.PrepareSurfaceSource(ctx, viewer, q.Key, q.Caps, q.ReleaseID, q.Exact)
	r.mu.Lock()
	entry.life = life
	if ps != nil {
		entry.result.PlayID = ps.ID
	}
	r.mu.Unlock()
	if e != nil || ps == nil {
		return sourceResponse{}, ErrPending
	}
	if r.o.SourcePolicy(ctx, viewer, ps) != nil {
		return sourceResponse{}, ErrDenied
	}
	revision := core.SourceRevision(ps)
	if revision == "" {
		return sourceResponse{}, ErrDenied
	}
	for _, stream := range ps.Info.Streams {
		if stream.External || stream.ExternalURL != "" {
			return sourceResponse{}, ErrDenied
		}
	}
	result := PreparedSource{Owner: r.o.Self, PlayID: ps.ID, Revision: revision, ReleaseID: ps.SourceReleaseID(), Info: ps.Info}
	r.mu.Lock()
	entry.result = result
	entry.finished = true
	r.mu.Unlock()
	return sourceResponse{Prepared: result}, nil
}

// RetirePreparedSource seals an exact frontend preparation even when no encoder
// reservation was made. A transport error retains responsibility at the caller.
func (r *Runtime) RetirePreparedSource(ctx context.Context, owner nodewire.Identity, operation string) (bool, error) {
	reply, e := r.sourceCall(ctx, Route{Source: owner}, sourceRequest{Op: "prepare-retire", Prepare: &PrepareRequest{Operation: operation}})
	return reply.Retired, e
}
func (r *Runtime) retirePreparation(ctx context.Context, peer nodewire.Identity, q *PrepareRequest) (sourceResponse, error) {
	if q == nil || q.Operation == "" {
		return sourceResponse{}, ErrDenied
	}
	key := peer.NodeID + "/" + peer.Boot + "/" + q.Operation
	r.mu.Lock()
	entry := r.preparations[key]
	if entry == nil || entry.peer != peer {
		r.mu.Unlock()
		return sourceResponse{}, ErrDenied
	}
	life, id := entry.life, entry.result.PlayID
	r.mu.Unlock()
	if life == nil || id == "" {
		return sourceResponse{}, ErrPending
	}
	if e := r.o.Playback.RetireSource(id, life); e != nil {
		return sourceResponse{}, e
	}
	select {
	case <-life.Done():
		return sourceResponse{Retired: life.Err() == nil}, nil
	default:
		return sourceResponse{}, ErrPending
	}
}
