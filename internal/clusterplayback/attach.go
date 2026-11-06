package clusterplayback

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/braedonsaunders/ether/internal/config"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/nodewire"
	"github.com/braedonsaunders/ether/internal/remotetranscode"
)

func (r *Runtime) attachExecution(ctx context.Context, id string) error {
	route, err := r.route(ctx, id)
	if err != nil {
		return err
	}
	record, err := r.o.Placement.Lookup(ctx, id)
	if err != nil || record.Fence != route.Fence || !record.Authority || record.Retired || !r.ownsCoordination(ctx, id) {
		return ErrDenied
	}
	admission, err := r.readExecutionAdmission(ctx, route)
	if err != nil {
		return err
	}
	target := nodewire.Identity{Cluster: r.o.Self.Cluster, NodeID: route.Fence.Target.NodeID, Boot: route.Fence.Target.Boot}
	ep, tc, check, err := r.peer(ctx, target, WorkerPath)
	if err != nil {
		return err
	}
	coordinator, err := r.CurrentCoordinator(ctx, id)
	if err != nil {
		return err
	}
	adapter, err := remotetranscode.NewAdapter(remotetranscode.AdapterOptions{RequestHeaders: r.coordinatorHeaders(id, coordinator.Epoch), Capabilities: r.o.RemoteCapabilities, Endpoint: ep, Client: &http.Client{Transport: &http.Transport{TLSClientConfig: tc}}, RecheckServer: check, Capacity: 1, Decide: r.o.Decide, Assign: func(context.Context, core.HLSRequest) (remotetranscode.Assignment, error) {
		return remotetranscode.Assignment{}, ErrDenied
	}})
	if err != nil {
		return err
	}
	session, err := adapter.Attach(ctx, remoteIdentity(route))
	if err != nil {
		adapter.Close()
		return err
	}
	// Coordination adoption transfers the registry once. Media readers never do
	// this, and a repeated Stop must remain valid after the registry is removed.
	if _, err := r.o.DB.ExecContext(ctx, `UPDATE plex_sessions SET node_id=?,updated_at=? WHERE id=? AND active=1 AND protocol IN ('jellyfin','emby') AND EXISTS (SELECT 1 FROM clusterplayback_coordinators c WHERE c.session=? AND c.node=? AND c.boot=? AND c.epoch=? AND c.deadline>?)`, r.o.Self.NodeID, time.Now().UnixMilli(), id, id, r.o.Self.NodeID, r.o.Self.Boot, coordinator.Epoch, time.Now().Add(r.o.ClockSkew).UnixMilli()); err != nil {
		adapter.Close()
		return err
	}
	x := &execution{epoch: coordinator.Epoch, adopted: true, route: route, reserved: true, adapter: adapter, session: session, retirement: session, done: make(chan struct{}), initial: newInitialAdmission(route, admission.Request)}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || (r.executions[id] != nil && r.executions[id].epoch == coordinator.Epoch) || (r.executions[id] == nil && len(r.executions) >= r.o.HistoryCapacity) {
		adapter.Close()
		return ErrPending
	}
	if old := r.executions[id]; old != nil {
		x.done = old.done
		if old.adapter != nil {
			old.adapter.Close()
		}
	}
	r.executions[id] = x
	return nil
}

// workerAuthenticate authenticates the actual certificate. Control requests
// require the coordinator lease; media observers require a server-role peer
// and are restricted to read operations by authorizeWorker. The primitive's
// execution identity retains its original controller boot.
func (r *Runtime) workerAuthenticate(q *http.Request) (string, error) {
	peer, err := r.o.Guard.Request(q)
	if err != nil {
		return "", err
	}
	// Authenticate cannot consume the bounded protocol body. The wrapper installs
	// the exact execution session in context after decoding a copy of that body.
	id, ok := q.Context().Value(workerSessionKey{}).(string)
	if !ok {
		return "", ErrDenied
	}
	if q.Header.Get(mediaReadHeader) == "1" && r.o.MemberRole(q.Context(), peer, config.RoleServer) == nil {
		route, err := r.route(q.Context(), id)
		return route.Fence.Controller.Boot, err
	}
	c, err := r.CurrentCoordinator(q.Context(), id)
	if err != nil || c.Identity != peer || q.Header.Get(coordinatorEpochHeader) != strconv.FormatInt(c.Epoch, 10) || !c.Deadline.After(time.Now().Add(r.o.ClockSkew)) || r.o.MemberRole(q.Context(), peer, config.RoleServer) != nil {
		return "", ErrDenied
	}
	route, err := r.route(q.Context(), id)
	if err != nil {
		return "", err
	}
	return route.Fence.Controller.Boot, nil
}

type workerSessionKey struct{}
type workerAuthorityKey struct{}
type workerAuthority struct {
	readOnly bool
	peer     nodewire.Identity
	epoch    string
}

func (r *Runtime) workerHTTP(w http.ResponseWriter, q *http.Request) {
	peer, err := r.o.Guard.Request(q)
	if err != nil {
		http.Error(w, "denied", 401)
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, q.Body, remotetranscode.MaxMessage))
	if err != nil {
		http.Error(w, "request", 400)
		return
	}
	var envelope struct{ Identity remotetranscode.Identity }
	if json.Unmarshal(raw, &envelope) != nil {
		http.Error(w, "request", 400)
		return
	}
	ctx := context.WithValue(q.Context(), workerAuthorityKey{}, workerAuthority{peer: peer, epoch: q.Header.Get(coordinatorEpochHeader), readOnly: q.Header.Get(mediaReadHeader) == "1"})
	clone := q.Clone(context.WithValue(ctx, workerSessionKey{}, envelope.Identity.Session))
	clone.Body = io.NopCloser(bytes.NewReader(raw))
	r.worker.ServeHTTP(w, clone)
}

// Source movement changes routing beneath the admitted execution. Only a
// recorded exact source-move chain can supply its historical admission.
func (r *Runtime) readExecutionAdmission(ctx context.Context, route Route) (admissionRecord, error) {
	if route.SourceGeneration == 0 {
		return r.readAdmission(ctx, route)
	}
	prior := r.sourceRoutes(ctx, route)
	if len(prior) != int(route.SourceGeneration) || len(prior) == 0 {
		return admissionRecord{}, ErrDenied
	}
	original := prior[0]
	if original.SourceGeneration != 0 || original.Fence != route.Fence || original.Viewer != route.Viewer || original.Revision != route.Revision {
		return admissionRecord{}, ErrDenied
	}
	return r.readAdmission(ctx, original)
}
