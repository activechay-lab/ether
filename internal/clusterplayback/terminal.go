package clusterplayback

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/nodewire"
	"github.com/braedonsaunders/ether/internal/nodewire/outbound"
	"github.com/braedonsaunders/ether/internal/placement"
	"github.com/braedonsaunders/ether/internal/remotetranscode"
	"io"
	"net/http"
	"time"
)

const TerminalPath = "/cluster/playback/terminal"

type terminalRequest struct {
	Fence placement.Fence
	Kind  string
	Stop  bool
}
type terminalReply struct {
	Fence placement.Fence
	Node  nodewire.Identity
	Kind  string
	State remotetranscode.TerminalState
}

func (r *Runtime) terminalHTTP(w http.ResponseWriter, q *http.Request) {
	peer, e := r.o.Guard.Request(q)
	if e != nil || r.o.MemberRole(q.Context(), peer, config.RoleServer) != nil {
		http.Error(w, "denied", 401)
		return
	}
	if q.Method != "POST" {
		http.Error(w, "method", 405)
		return
	}
	var request terminalRequest
	d := json.NewDecoder(http.MaxBytesReader(w, q.Body, 8192))
	d.DisallowUnknownFields()
	if d.Decode(&request) != nil || d.Decode(new(any)) != io.EOF {
		http.Error(w, "request", 400)
		return
	}
	reply, e := r.terminalOperation(q.Context(), peer, request)
	if e != nil {
		http.Error(w, "terminal proof unavailable", 409)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(reply)
}
func (r *Runtime) terminalFence(ctx context.Context, f placement.Fence) (Route, error) {
	if f.Generation <= 0 {
		return Route{}, ErrDenied
	}
	record, e := r.o.Placement.Lookup(ctx, f.Session)
	if e != nil || record.Fence != f || record.Authority {
		return Route{}, ErrDenied
	}
	route, e := r.route(ctx, f.Session)
	if e != nil || route.Fence != f {
		return Route{}, ErrDenied
	}
	return route, nil
}
func (r *Runtime) terminalOperation(ctx context.Context, peer nodewire.Identity, q terminalRequest) (terminalReply, error) {
	if peer.Cluster != r.o.Self.Cluster || r.o.MemberRole(ctx, peer, config.RoleServer) != nil {
		return terminalReply{}, ErrDenied
	}
	route, e := r.terminalFence(ctx, q.Fence)
	if e != nil {
		return terminalReply{}, e
	}
	reply := terminalReply{Fence: q.Fence, Node: r.o.Self, Kind: q.Kind, State: remotetranscode.TerminalPending}
	switch q.Kind {
	case "encoder":
		if r.worker == nil || q.Fence.Target.NodeID != r.o.Self.NodeID || q.Fence.Target.Boot != r.o.Self.Boot {
			return terminalReply{}, ErrDenied
		}
		// Recheck immediately before the local terminal mutation. No identity is
		// fabricated for the regular execution Security.Authenticate path.
		if _, e = r.terminalFence(ctx, q.Fence); e != nil {
			return terminalReply{}, e
		}
		snapshot, e := r.worker.Terminal(ctx, remoteIdentity(route), q.Stop)
		if e != nil || snapshot.Identity != remoteIdentity(route) {
			return terminalReply{}, ErrPending
		}
		reply.State = snapshot.State
		return reply, nil
	case "source":
		if r.authority == nil || route.Source != r.o.Self {
			return terminalReply{}, ErrDenied
		}
		key := sourceKey(q.Fence, route.SourceGeneration)
		r.mu.Lock()
		source := r.sources[key]
		r.mu.Unlock()
		if source == nil && q.Stop {
			ps, ok := r.o.Playback.Session(route.PlayID)
			life, owned := r.o.Playback.SourceRetirement(route.PlayID)
			if !ok || !owned || ps == nil || ps.UserID != route.Viewer || core.SourceRevision(ps) != route.Revision {
				return terminalReply{}, ErrPending
			}
			size := int64(0)
			if ps.Source != nil {
				size = ps.Source.Size()
			}
			candidate := &ownedSource{route: route, playID: route.PlayID, life: life, retiring: true, size: size}
			r.mu.Lock()
			source = r.sources[key]
			if source == nil && len(r.sources) < r.o.HistoryCapacity {
				r.sources[key] = candidate
				source = candidate
			}
			r.mu.Unlock()
		}
		// Source absence never attests that acquisition or producers retired.
		if source == nil || source.route != route {
			return terminalReply{}, ErrPending
		}
		r.mu.Lock()
		if q.Stop {
			source.retiring = true
		}
		binding, life, handle := source.binding, source.life, source.handle
		r.mu.Unlock()
		if binding || life == nil {
			return reply, nil
		}
		if q.Stop {
			if _, e = r.terminalFence(ctx, q.Fence); e != nil {
				return terminalReply{}, e
			}
			if handle.Value() != "" {
				if r.authority.Revoke(handle) != nil {
					return terminalReply{}, ErrPending
				}
			}
			if e = r.o.Playback.RetireSource(source.playID, life); e != nil {
				return terminalReply{}, ErrPending
			}
			r.mu.Lock()
			source.sealed = true
			r.mu.Unlock()
		}
		select {
		case <-life.Done():
			if life.Err() != nil {
				reply.State = remotetranscode.TerminalUnverified
			} else if r.sourceJoined(source) && r.bytes.Pending(scopeForRoute(route)) == 0 {
				reply.State = remotetranscode.TerminalRetired
			}
		default:
		}
		return reply, nil
	default:
		return terminalReply{}, ErrDenied
	}
}
func (r *Runtime) terminalCall(ctx context.Context, route Route, kind string, stop bool) (terminalReply, error) {
	id := route.Source
	if kind == "encoder" {
		id = nodewire.Identity{Cluster: r.o.Self.Cluster, NodeID: route.Fence.Target.NodeID, Boot: route.Fence.Target.Boot}
	}
	request := terminalRequest{Fence: route.Fence, Kind: kind, Stop: stop}
	if id == r.o.Self {
		return r.terminalOperation(ctx, r.o.Self, request)
	}
	ep, tc, check, e := r.peer(ctx, id, TerminalPath)
	if e != nil {
		return terminalReply{}, e
	}
	transport, e := outbound.New(ep, tc, 5*time.Second, check)
	if e != nil {
		return terminalReply{}, e
	}
	defer transport.CloseIdleConnections()
	raw, e := json.Marshal(request)
	if e != nil {
		return terminalReply{}, e
	}
	q, e := http.NewRequestWithContext(ctx, "POST", ep, bytes.NewReader(raw))
	if e != nil {
		return terminalReply{}, e
	}
	q.Header.Set("Content-Type", "application/json")
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, e := client.Do(q)
	if e != nil {
		return terminalReply{}, ErrPending
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return terminalReply{}, ErrPending
	}
	raw, e = io.ReadAll(io.LimitReader(response.Body, 8193))
	if e != nil || len(raw) > 8192 {
		return terminalReply{}, ErrPending
	}
	var reply terminalReply
	if json.Unmarshal(raw, &reply) != nil || reply.Fence != route.Fence || reply.Node != id || reply.Kind != kind || reply.State < remotetranscode.TerminalPending || reply.State > remotetranscode.TerminalUnverified {
		return terminalReply{}, ErrPending
	}
	return reply, nil
}

// ReconcileExpired admits only explicit exact-session terminal takeover. The
// returned token stays charged after transport loss. Expiry alone proves nothing.
func (r *Runtime) ReconcileExpired(ctx context.Context, id string) (core.EncoderRetirement, error) {
	if r.o.MemberRole(ctx, r.o.Self, config.RoleServer) != nil {
		return nil, ErrDenied
	}
	record, e := r.o.Placement.Lookup(ctx, id)
	if e != nil || record.Authority {
		return nil, ErrDenied
	}
	route, e := r.terminalFence(ctx, record.Fence)
	if e != nil {
		return nil, e
	}
	r.mu.Lock()
	if x := r.executions[id]; x != nil && !x.retired {
		if x.route.Fence != record.Fence {
			r.mu.Unlock()
			return nil, ErrPending
		}
		x.stopping = true
		token := executionRetirement{r: r, id: id, x: x}
		r.mu.Unlock()
		return token, nil
	}
	if r.closed || r.executions[id] == nil && len(r.executions) >= r.o.HistoryCapacity {
		r.mu.Unlock()
		return nil, ErrDenied
	}
	x := &execution{route: route, reserved: true, stopping: true, foreign: true, done: make(chan struct{})}
	r.executions[id] = x
	r.mu.Unlock()
	return executionRetirement{r: r, id: id, x: x}, nil
}
func (r *Runtime) retireForeign(ctx context.Context, x *execution) error {
	if retired, e := r.retainedReceipt(ctx, x.route); e != nil {
		return e
	} else if retired {
		return nil
	}
	encoder, e := r.terminalCall(ctx, x.route, "encoder", true)
	if e != nil {
		return e
	}
	source, e := r.terminalCall(ctx, x.route, "source", true)
	if e != nil {
		return e
	}
	if encoder.State != remotetranscode.TerminalRetired || source.State != remotetranscode.TerminalRetired {
		return ErrPending
	}
	return r.retireWithEvidence(ctx, x.route, "clusterplayback/terminal-takeover")
}
func (r *Runtime) verifyForeign(ctx context.Context, route Route) error {
	encoder, e := r.terminalCall(ctx, route, "encoder", false)
	if e != nil || encoder.State != remotetranscode.TerminalRetired {
		return ErrPending
	}
	source, e := r.terminalCall(ctx, route, "source", false)
	if e != nil || source.State != remotetranscode.TerminalRetired {
		return ErrPending
	}
	return nil
}
