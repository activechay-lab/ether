package clusterplayback

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/nodewire"
)

// The exact preparation is saved before first grant publication. The source
// host is never needed to recover these choices after a hard loss.
func (r *Runtime) saveSourceRecovery(ctx context.Context, rt Route) error {
	r.mu.Lock()
	var request *PrepareRequest
	var size int64
	for _, p := range r.preparations {
		if p.finished && !p.retiring && p.result.PlayID == rt.PlayID && p.result.Revision == rt.Revision && p.peer.NodeID == rt.Fence.Controller.ID && p.peer.Boot == rt.Fence.Controller.Boot {
			q := p.request
			q.ReleaseID = p.result.ReleaseID
			q.Exact = true
			size = p.capturedSize
			request = &q
			break
		}
	}
	r.mu.Unlock()
	// Locally prepared callers without an exact transport preparation continue
	// to work, but cannot manufacture a cross-host recovery descriptor.
	if request == nil {
		return nil
	}
	raw, err := json.Marshal(struct {
		Request PrepareRequest
		Size    int64
	}{*request, size})
	if err != nil {
		return err
	}
	_, err = r.o.DB.ExecContext(ctx, `INSERT INTO clusterplayback_source_recovery(session,generation,request) VALUES(?,?,?) ON CONFLICT(session,generation) DO NOTHING`, rt.Fence.Session, rt.Fence.Generation, string(raw))
	return err
}

// Source movement has its own generation; changing encoder placement here
// would kill an otherwise healthy FFmpeg. The durable route CAS fences both
// new reads and already-running source reads through sourcePolicy.
func (r *Runtime) sourceFailover(ctx context.Context, peer nodewire.Identity, m sourceRequest) (sourceResponse, error) {
	r.control.Lock()
	defer r.control.Unlock()
	old := m.Old
	f := old.Scope.Fence
	if f.Controller.ID != r.o.Self.NodeID || f.Controller.Boot != r.o.Self.Boot || peer.Cluster != r.o.Self.Cluster || peer.NodeID != f.Target.NodeID || peer.Boot != f.Target.Boot || r.o.Eligible(ctx, r.o.Self, config.RoleServer) != nil {
		return sourceResponse{}, ErrDenied
	}
	rec, err := r.o.Placement.Lookup(ctx, f.Session)
	if err != nil || rec.Fence != f || rec.Retired || !rec.Authority || !rec.Deadline.Add(-r.o.ClockSkew).After(time.Now()) || old.Expires.After(rec.Deadline.Add(-r.o.ClockSkew)) {
		return sourceResponse{}, ErrDenied
	}
	rt, err := r.route(ctx, f.Session)
	if err != nil || rt.Fence != f || rt.Revision != old.Scope.Revision || viewerString(rt.Viewer) != old.Scope.Viewer || rt.SourceGeneration < old.Scope.SourceGeneration {
		return sourceResponse{}, ErrDenied
	}
	if rt.SourceGeneration == old.Scope.SourceGeneration {
		if rt.SourceGeneration >= 3 {
			return sourceResponse{}, ErrDenied
		}
		var raw string
		if err = r.o.DB.QueryRowContext(ctx, `SELECT request FROM clusterplayback_source_recovery WHERE session=? AND generation=?`, f.Session, f.Generation).Scan(&raw); err != nil {
			return sourceResponse{}, ErrDenied
		}
		var descriptor struct {
			Request PrepareRequest
			Size    int64
		}
		if json.Unmarshal([]byte(raw), &descriptor) != nil || !descriptor.Request.Exact || descriptor.Request.ReleaseID == "" {
			return sourceResponse{}, ErrDenied
		}
		q := descriptor.Request
		rows, e := r.o.DB.QueryContext(ctx, `SELECT id,incarnation FROM cluster_nodes ORDER BY id LIMIT 4096`)
		if e != nil {
			return sourceResponse{}, e
		}
		var candidates []nodewire.Identity
		for rows.Next() {
			id := nodewire.Identity{Cluster: r.o.Self.Cluster}
			if e = rows.Scan(&id.NodeID, &id.Boot); e != nil {
				break
			}
			candidates = append(candidates, id)
		}
		tail := rows.Err()
		rows.Close()
		if e != nil || tail != nil {
			return sourceResponse{}, ErrDenied
		}
		moved := false
		for _, id := range candidates {
			if id == rt.Source || r.o.Eligible(ctx, id, config.RoleSource) != nil {
				continue
			}
			used := false
			for _, prior := range r.sourceRoutes(ctx, rt) {
				if prior.Source == id {
					used = true
					break
				}
			}
			if used {
				continue
			}

			q.Operation = fmt.Sprintf("source-%s-%d-%d", f.Session, f.Generation, rt.SourceGeneration+1)
			if len(q.Operation) > 128 {
				return sourceResponse{}, ErrDenied
			}
			prepared, e := r.PrepareSource(ctx, id, q)
			if e != nil {
				continue
			} // Unknown/failed preparations remain owned by source history.
			if prepared.Owner != id || prepared.Revision != rt.Revision || prepared.Info.Size != descriptor.Size {
				continue
			}
			if _, e = r.sourceCall(ctx, Route{Source: id}, sourceRequest{Op: "prepare-open", Prepare: &q}); e != nil {
				continue
			}
			next := rt
			next.Source = id
			next.PlayID = prepared.PlayID
			next.SourceGeneration++
			before, _ := json.Marshal(rt)
			after, _ := json.Marshal(next)
			tx, e := r.o.DB.BeginTx(ctx, nil)
			if e != nil {
				return sourceResponse{}, e
			}
			_, e = tx.ExecContext(ctx, `INSERT INTO clusterplayback_source_moves(session,generation,source_generation,route,operation) VALUES(?,?,?,?,?)`, f.Session, f.Generation, rt.SourceGeneration, string(before), q.Operation)
			if e == nil {
				result, updateErr := tx.ExecContext(ctx, `UPDATE clusterplayback_routes SET route=? WHERE session=? AND generation=? AND route=?`, string(after), f.Session, f.Generation, string(before))
				e = updateErr
				if e == nil {
					n, countErr := result.RowsAffected()
					if countErr != nil || n != 1 {
						e = ErrDenied
					}
				}
			}
			if e != nil {
				_ = tx.Rollback()
				return sourceResponse{}, e
			}
			if e = tx.Commit(); e != nil {
				return sourceResponse{}, ErrPending
			}
			rt = next
			moved = true
			break
		}
		if !moved {
			return sourceResponse{}, ErrDenied
		}
	}
	// Bind and grant are idempotent after an unknown response. Old grant expiry
	// is preserved; worker renewal extends the replacement through normal CAS.
	if _, err = r.sourceCall(ctx, rt, sourceRequest{Op: "bind"}); err != nil {
		return sourceResponse{}, err
	}
	return r.sourceCall(ctx, rt, sourceRequest{Op: "resolve-failover", Expires: old.Expires})
}

// All prior sources remain retirement responsibilities. Unreachable is pending,
// never successful Close evidence; movement cannot free their held capacity.
func (r *Runtime) sourceRoutes(ctx context.Context, rt Route) []Route {
	rows, e := r.o.DB.QueryContext(ctx, `SELECT route FROM clusterplayback_source_moves WHERE session=? AND generation=? ORDER BY source_generation`, rt.Fence.Session, rt.Fence.Generation)
	if e != nil {
		return nil
	}
	defer rows.Close()
	var out []Route
	for rows.Next() {
		var raw string
		var prior Route
		if rows.Scan(&raw) != nil || json.Unmarshal([]byte(raw), &prior) != nil {
			return nil
		}
		out = append(out, prior)
	}
	if rows.Err() != nil {
		return nil
	}
	return out
}
func (r *Runtime) retireSourceRoutes(ctx context.Context, initial Route, op string) error {
	current, e := r.route(ctx, initial.Fence.Session)
	if e != nil || current.Fence != initial.Fence {
		return ErrPending
	}
	prior := r.sourceRoutes(ctx, current)
	if current.SourceGeneration > 0 && len(prior) != int(current.SourceGeneration) {
		return ErrPending
	}
	pending := false
	for _, rt := range append(prior, current) {
		reply, e := r.sourceCall(ctx, rt, sourceRequest{Op: op})
		if e != nil || !reply.Retired {
			pending = true
		}
	}
	if pending {
		return ErrPending
	}
	return nil
}
