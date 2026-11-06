package clusterplayback

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/nodewire"
)

// A different boot must be authenticated as a current role member. Missing or
// expired membership never proves process death. RemoveTarget is an explicit
// operator attestation of fencing, not membership pruning.
func (r *Runtime) retiredSourceBoot(ctx context.Context, old nodewire.Identity) error {
	var removed int
	if err := r.o.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM placement_removed_targets WHERE node=? AND boot=?`, old.NodeID, old.Boot).Scan(&removed); err != nil {
		return err
	}
	if removed > 0 {
		return nil
	}
	var boot string
	if err := r.o.DB.QueryRowContext(ctx, `SELECT incarnation FROM cluster_nodes WHERE id=?`, old.NodeID).Scan(&boot); err != nil {
		return err
	}
	if boot == "" || boot == old.Boot {
		return ErrPending
	}
	return r.o.MemberRole(ctx, nodewire.Identity{Cluster: old.Cluster, NodeID: old.NodeID, Boot: boot}, config.RoleSource)
}

// Worker process death covers the encoder and its local input gateway. Each
// source generation must independently prove death or exact joined retirement.
func (r *Runtime) sourcesAfterWorkerBoot(ctx context.Context, route Route, op string) error {
	prior := r.sourceRoutes(ctx, route)
	if len(prior) != int(route.SourceGeneration) {
		return ErrPending
	}
	for _, source := range append(prior, route) {
		if r.retiredSourceBoot(ctx, source.Source) == nil {
			continue
		}
		reply, err := r.sourceCall(ctx, source, sourceRequest{Op: op})
		if err != nil || !reply.Retired {
			return ErrPending
		}
	}
	return nil
}

func (r *Runtime) sealDeadBoot(ctx context.Context, route Route) error {
	h, err := r.readOperationGate(ctx, operationKey{r.o.Self.Cluster, route.Fence.Session, route.Fence.Generation})
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	} // no gate was ever published
	if err != nil {
		return err
	}
	if h.Gate.Binding.Route.Fence != route.Fence {
		return ErrDenied
	}
	if h.Gate.Seal != nil {
		return nil
	}
	_, err = r.recordOperationSeal(ctx, h.Gate.Binding, operationSeal{Actor: r.o.Self, Reason: sealAuthorityLoss})
	return err
}

func (r *Runtime) reconcileOrdinaryBootRetirements(ctx context.Context) error {
	if !r.o.Roles.Has(config.RoleServer) || r.o.MemberRole(ctx, r.o.Self, config.RoleServer) != nil {
		return nil
	}
	rows, err := r.o.DB.QueryContext(ctx, `SELECT session FROM placement_sessions WHERE retired=0 ORDER BY session LIMIT ?`, r.o.HistoryCapacity)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		// Tick and sourceFailover share the coordinator's control mutex. Only
		// that live epoch reconciles sources, preventing a route move between
		// source proof and the placement retirement transaction.
		if !r.ownsCoordination(ctx, id) {
			continue
		}
		route, err := r.route(ctx, id)
		if err != nil || r.retiredWorkerBoot(ctx, route.Fence) != nil {
			continue
		}
		record, err := r.o.Placement.Lookup(ctx, id)
		if err != nil || record.Retired || record.Fence != route.Fence {
			continue
		}
		// Tick holds control across this decision and encoder supersession. A
		// dead boot proves the OLD process exited, not that the viewer stopped.
		// Never seal a recoverable current gate here: Supersede must seal it in
		// the same ledger transaction that publishes generation+1. Its displaced
		// claim can then retire independently on the next reconciliation.
		active, err := r.preserveDeadBootRecovery(ctx, route)
		if err != nil {
			return err
		}
		if active {
			continue
		}
		if err = r.sealDeadBoot(ctx, route); err != nil {
			return err
		}
		if r.sourcesAfterWorkerBoot(ctx, route, "retire") != nil {
			continue
		}
		// The placement verifier independently repeats these exact boot/source
		// checks; Retire then compares the full fence under the ledger lock.
		if err = r.o.Placement.Retire(ctx, route.Fence, "clusterplayback/authenticated-dead-boots"); err != nil {
			return err
		}
		var closeAdapter func()
		r.mu.Lock()
		local := r.executions[id]
		if local != nil && local.route.Fence == route.Fence && !local.retired {
			local.stopping, local.retired = true, true
			if local.done != nil {
				close(local.done)
			}
			if local.adapter != nil {
				closeAdapter = local.adapter.Close
			}
		}
		r.mu.Unlock()
		if closeAdapter != nil {
			closeAdapter()
		}
		slog.Info("retired playback capacity after authenticated boot replacement", "session", id, "generation", route.Fence.Generation, "worker", route.Fence.Target.NodeID, "worker_boot", route.Fence.Target.Boot, "source", route.Source.NodeID, "source_boot", route.Source.Boot)
	}
	return nil
}

// Missing local memory (for example during coordinator takeover) cannot prove
// abandonment. Use durable admission to preserve a supported recovery plan until
// the coordinator can attach it. Cancellation is durable and wins against the
// recovery transaction's unsealed-gate CAS.
func (r *Runtime) preserveDeadBootRecovery(ctx context.Context, route Route) (bool, error) {
	h, err := r.readOperationGate(ctx, operationKey{r.o.Self.Cluster, route.Fence.Session, route.Fence.Generation})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if err == nil && h.Gate.Seal != nil && h.Gate.Seal.Reason == sealCancellation {
		return false, nil
	}
	r.mu.Lock()
	x := r.executions[route.Fence.Session]
	stopped := x != nil && x.route.Fence == route.Fence && (x.stopping || x.retired)
	r.mu.Unlock()
	if stopped {
		return false, nil
	}
	a, err := r.readExecutionAdmission(ctx, route)
	if err != nil {
		// Unknown admission is not evidence that the session is unrecoverable.
		return true, nil
	}
	return encoderRecoverySupported(a.Request), nil
}
