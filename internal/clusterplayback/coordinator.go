package clusterplayback

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/nodewire"
	"github.com/braedonsaunders/ether/internal/placement"
	"github.com/braedonsaunders/ether/internal/remotetranscode"
	"net/http"
	"time"
)

type execution struct {
	route      Route
	request    placement.Request
	adapter    *remotetranscode.Adapter
	session    *remotetranscode.Session
	retirement core.EncoderRetirement
	stopping   bool
	reserved   bool
}

// Supports is a side-effect-free admission gate. Unsupported requests must use
// the existing local backend before any distributed ownership is allocated.
func Supports(q core.HLSRequest) bool {
	if q.Protocol != "" && q.Protocol != core.ProtocolHLS || q.InputLease != nil || len(q.InputHeaders) > 0 || q.LiveInput || q.CopyTimestamps || len(q.SlateText) > 0 || q.SoftSubtitleStream != nil || len(q.Info.Keyframes) > 0 || q.StartOffset < 0 || q.SegmentDuration < 0 || q.SegmentDuration > time.Minute || q.Info.Duration < 0 || len(q.Info.Streams) > 64 || len(q.Info.Chapters) > 128 || len(q.ReleaseID) > 512 {
		return false
	}
	d := q.Decision
	if d.Kind != core.DecisionRemux && d.Kind != core.DecisionTranscode || d.BurnSubtitles || d.SubtitleStream >= 0 || len(d.AudioFilters) > 0 || d.Enhancement != (core.VideoEnhancement{}) {
		return false
	}
	for _, s := range q.Info.Streams {
		if s.External || s.ExternalURL != "" {
			return false
		}
	}
	if len(q.SessionID) == 0 || len(q.SessionID) > 128 {
		return false
	}
	for _, c := range q.SessionID {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	b, e := json.Marshal(q)
	return e == nil && len(b) < remotetranscode.MaxMessage-8192
}

// StartDistributedHLS starts from the exact source already prepared by the
// authenticated broker. ErrUnsupported is returned exclusively before side effects.
func (r *Runtime) StartDistributedHLS(ctx context.Context, viewer int64, playID, revision string, q core.HLSRequest) (core.HLSSession, error) {
	if !Supports(q) {
		return nil, remotetranscode.ErrUnsupported
	}
	if r.o.Playback == nil || r.o.Decide == nil || viewer <= 0 || revision == "" {
		return nil, ErrDenied
	}
	r.mu.Lock()
	if r.closed || r.executions[q.SessionID] != nil || len(r.executions) >= r.o.Capacity {
		r.mu.Unlock()
		return nil, ErrPending
	}
	x := &execution{}
	r.executions[q.SessionID] = x
	r.mu.Unlock()
	// The durable session index is checked even after coordinator reboot. An old
	// generation may not be replaced without exact retirement proof.
	old, e := r.o.Placement.Lookup(ctx, q.SessionID)
	if e == nil && !old.Retired {
		return nil, ErrPending
	}
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return nil, e
	}
	expected := int64(0)
	if e == nil {
		expected = old.Generation
	}
	target, claims, e := r.selectTarget(ctx)
	if e != nil {
		return nil, e
	}
	operation, e := randomToken()
	if e != nil {
		return nil, e
	}
	request := placement.Request{Session: q.SessionID, Operation: operation, ExpectedGeneration: expected, Controller: placement.Controller{ID: r.o.Self.NodeID, Boot: r.o.Self.Boot}, Target: target, Role: config.RoleTranscode, Claims: claims, Lease: 2 * time.Minute}
	r.mu.Lock()
	x.request = request
	r.mu.Unlock()
	rec, e := r.o.Placement.Reserve(ctx, request)
	if e != nil {
		return nil, e
	}
	rt := Route{Fence: rec.Fence, Source: r.o.Self, Viewer: viewer, PlayID: playID, Revision: revision}
	r.mu.Lock()
	x.route = rt
	x.reserved = true
	r.mu.Unlock()
	if e = r.putRoute(ctx, rt); e != nil {
		return nil, e
	}
	bound, e := r.sourceCall(ctx, rt, sourceRequest{Op: "bind"})
	if e != nil {
		return nil, e
	}
	deadline := rec.Deadline.Add(-2 * r.o.ClockSkew).UTC()
	endpoint, tc, check, e := r.peer(ctx, nodewire.Identity{Cluster: r.o.Self.Cluster, NodeID: target.NodeID, Boot: target.Boot}, WorkerPath)
	if e != nil {
		return nil, e
	}
	adapter, e := remotetranscode.NewAdapter(remotetranscode.AdapterOptions{Endpoint: endpoint, Client: &http.Client{Transport: &http.Transport{TLSClientConfig: tc}, Timeout: 30 * time.Second}, RecheckServer: check, Assign: func(context.Context, core.HLSRequest) (remotetranscode.Assignment, error) {
		return remotetranscode.Assignment{Identity: remoteIdentity(rt), Grant: bound.Stable, Expires: deadline}, nil
	}, Decide: r.o.Decide, Hardware: r.o.Hardware, Capacity: 1})
	if e != nil {
		return nil, e
	}
	r.mu.Lock()
	x.adapter = adapter
	r.mu.Unlock()
	q.InputURL = "" // No local URL crosses the execution protocol.
	hs, token, e := adapter.StartHLSOwned(ctx, q)
	r.mu.Lock()
	x.retirement = token
	if s, ok := token.(*remotetranscode.Session); ok {
		x.session = s
	}
	r.mu.Unlock()
	if e != nil || hs == nil {
		return nil, ErrPending
	}
	return hs, nil
}
func (r *Runtime) selectTarget(ctx context.Context) (placement.Target, []placement.Claim, error) {
	rows, e := r.o.DB.QueryContext(ctx, `SELECT node,boot,roles,scopes FROM placement_targets ORDER BY node LIMIT 4096`)
	if e != nil {
		return placement.Target{}, nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var target placement.Target
		var rolesJSON, scopesJSON string
		if e = rows.Scan(&target.NodeID, &target.Boot, &rolesJSON, &scopesJSON); e != nil {
			return target, nil, e
		}
		var roles config.NodeRoles
		var scopes map[config.NodeRole][]string
		if json.Unmarshal([]byte(rolesJSON), &roles) != nil || !roles.Has(config.RoleTranscode) || json.Unmarshal([]byte(scopesJSON), &scopes) != nil {
			continue
		}
		names := scopes[config.RoleTranscode]
		if len(names) == 0 {
			continue
		}
		if e = r.o.Eligible(ctx, nodewire.Identity{Cluster: r.o.Self.Cluster, NodeID: target.NodeID, Boot: target.Boot}, config.RoleTranscode); e != nil {
			continue
		}
		claims := make([]placement.Claim, len(names))
		for i, name := range names {
			claims[i] = placement.Claim{Scope: name, Units: 1}
		}
		return target, claims, nil
	}
	return placement.Target{}, nil, placement.ErrCapacity
}
func (r *Runtime) Session(id string) (core.HLSSession, bool) {
	r.mu.Lock()
	x := r.executions[id]
	var a *remotetranscode.Adapter
	if x != nil {
		a = x.adapter
	}
	r.mu.Unlock()
	if a == nil {
		return nil, false
	}
	return a.Session(id)
}
func (r *Runtime) Stop(id string) {
	r.mu.Lock()
	x := r.executions[id]
	var s *remotetranscode.Session
	if x != nil {
		x.stopping = true
		s = x.session
	}
	r.mu.Unlock()
	if s != nil {
		_ = s.RetireEncoder()
	}
}

// Tick owns only bounded control work; Cleanup runs separately. A lost renewal
// response is reconciled through worker accepted history, never source replay.
func (r *Runtime) Tick(ctx context.Context) error {
	r.mu.Lock()
	list := make([]*execution, 0, len(r.executions))
	for _, x := range r.executions {
		copy := *x
		list = append(list, &copy)
	}
	r.mu.Unlock()
	var result error
	for _, x := range list {
		if e := ctx.Err(); e != nil {
			return e
		}
		if !x.reserved {
			continue
		}
		if x.session == nil {
			result = ErrPending
			continue
		}
		if x.stopping {
			if e := r.retire(ctx, x); e != nil {
				result = e
			}
			continue
		}
		lease := x.session.Lease()
		if lease.Deadline.After(time.Now().Add(time.Minute)) {
			continue
		}
		deadline := time.Now().Add(2 * time.Minute).UTC().Truncate(time.Millisecond)
		if e := r.o.Placement.Renew(ctx, x.route.Fence, deadline.Add(2*r.o.ClockSkew)); e != nil {
			r.Stop(x.route.Fence.Session)
			result = e
			continue
		}
		n := remotetranscode.Renewal{Identity: remoteIdentity(x.route), Operation: lease.Version + 1, PreviousVersion: lease.Version, PreviousDeadline: lease.Deadline, Deadline: deadline}
		if _, e := x.session.Renew(ctx, n); e != nil {
			_ = x.session.Reconcile(ctx)
			accepted := x.session.Lease().Accepted
			if accepted == nil || *accepted != n {
				r.Stop(x.route.Fence.Session)
				result = ErrPending
			}
		}
	}
	return result
}
func (r *Runtime) retire(ctx context.Context, x *execution) error {
	if x.session == nil {
		return ErrPending
	}
	_ = x.session.RetireEncoder()
	_ = x.session.Reconcile(ctx)
	if e := x.session.EncoderErr(); e != nil {
		return e
	}
	source, e := r.sourceCall(ctx, x.route, sourceRequest{Op: "retire"})
	if e != nil || !source.Retired {
		return ErrPending
	}
	return r.o.Placement.Retire(ctx, x.route.Fence, "clusterplayback/exact-retirement")
}
func (r *Runtime) verifyRetired(ctx context.Context, f placement.Fence) error {
	r.mu.Lock()
	x := r.executions[f.Session]
	if x == nil || x.route.Fence != f || x.session == nil {
		r.mu.Unlock()
		return ErrPending
	}
	s := x.session
	rt := x.route
	r.mu.Unlock()
	if s.EncoderErr() != nil {
		return ErrPending
	}
	reply, e := r.sourceCall(ctx, rt, sourceRequest{Op: "status"})
	if e != nil || !reply.Retired {
		return ErrPending
	}
	return nil
}

func (r *Runtime) Supports(q core.HLSRequest) bool { return Supports(q) }
func (r *Runtime) Allocated(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.executions[id] != nil
}
