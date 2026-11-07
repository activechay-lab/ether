package clusterplayback

import (
	"context"
	"net/http"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/nodewire"
	"github.com/braedonsaunders/ether/internal/placement"
	"github.com/braedonsaunders/ether/internal/remotetranscode"
)

// Reattach only exact historical retirement under the live coordinator epoch.
// This path never prepares a source, starts/renews an encoder, or reopens public
// admission. Durable current retirement and every old fence remain unchanged.
func (r *Runtime) attachDisplacedRetirement(ctx context.Context, displaced placement.Record) error {
	if !r.o.Roles.Has(config.RoleServer) || displaced.Authority || displaced.Retired {
		return ErrDenied
	}
	id := displaced.Session
	c, err := r.CurrentCoordinator(ctx, id)
	if err != nil || c.Identity != r.o.Self || !r.ownsCoordination(ctx, id) {
		return ErrDenied
	}
	r.mu.Lock()
	cached := r.executions[id]
	if cached != nil && cached.epoch == c.Epoch {
		for _, previous := range cached.failed {
			if previous.route.Fence == displaced.Fence && previous.epoch == c.Epoch && previous.session != nil {
				r.mu.Unlock()
				return nil
			}
		}
	}
	r.mu.Unlock()
	current, err := r.route(ctx, id)
	if err != nil || current.Fence.Generation <= displaced.Generation {
		return ErrDenied
	}
	record, err := r.o.Placement.Lookup(ctx, id)
	if err != nil || record.Fence != current.Fence {
		return ErrDenied
	}
	old, err := r.generationRoute(ctx, id, displaced.Generation)
	if err != nil || old.Fence != displaced.Fence {
		return ErrDenied
	}
	r.mu.Lock()
	owner := r.executions[id]
	if r.closed || owner != nil && (owner.route.Fence != current.Fence || !record.Retired && owner.epoch != c.Epoch) || owner == nil && (!record.Retired || len(r.executions) >= r.o.HistoryCapacity) {
		r.mu.Unlock()
		return ErrPending
	}
	r.mu.Unlock()
	peer := nodewire.Identity{Cluster: r.o.Self.Cluster, NodeID: old.Fence.Target.NodeID, Boot: old.Fence.Target.Boot}
	endpoint, tls, check, err := r.peer(ctx, peer, WorkerPath)
	if err != nil {
		return err
	}
	adapter, err := remotetranscode.NewAdapter(remotetranscode.AdapterOptions{
		RequestHeaders: r.coordinatorHeaders(id, c.Epoch), Capabilities: r.o.RemoteCapabilities,
		Endpoint: endpoint, Client: &http.Client{Transport: &http.Transport{TLSClientConfig: tls}}, RecheckServer: check,
		Capacity: 1, Decide: r.o.Decide,
		Assign: func(context.Context, core.HLSRequest) (remotetranscode.Assignment, error) {
			return remotetranscode.Assignment{}, ErrDenied
		},
	})
	if err != nil {
		return err
	}
	call, cancel := context.WithTimeout(ctx, r.o.EncoderRPCDeadline)
	session, err := adapter.AttachRetirement(call, remoteIdentity(old))
	cancel()
	if err != nil {
		adapter.Close()
		return err
	}
	fresh, err := r.CurrentCoordinator(ctx, id)
	if err != nil || fresh.Identity != r.o.Self || fresh.Epoch != c.Epoch || !r.ownsCoordination(ctx, id) {
		adapter.Close()
		return ErrDenied
	}
	entry := &execution{route: old, epoch: c.Epoch, session: session, adapter: adapter, stopping: true, reserved: true}
	var replaced *remotetranscode.Adapter
	r.mu.Lock()
	if r.closed || r.executions[id] != owner {
		r.mu.Unlock()
		adapter.Close()
		return ErrPending
	}
	if owner == nil {
		done := make(chan struct{})
		close(done)
		owner = &execution{route: current, epoch: c.Epoch, retired: true, stopping: true, reserved: true, done: done}
		r.executions[id] = owner
	} else if record.Retired {
		owner.retired, owner.stopping, owner.epoch = true, true, c.Epoch
		if owner.done != nil {
			select {
			case <-owner.done:
			default:
				close(owner.done)
			}
		}
	}
	found := false
	for i, previous := range owner.failed {
		if previous.route.Fence == old.Fence {
			replaced = previous.adapter
			owner.failed[i] = entry
			found = true
			break
		}
	}
	if !found {
		owner.failed = append(owner.failed, entry)
	}
	r.mu.Unlock()
	if replaced != nil {
		replaced.Close()
	}
	return nil
}
