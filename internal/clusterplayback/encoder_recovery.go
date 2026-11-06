package clusterplayback

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/nodewire"
	"github.com/braedonsaunders/ether/internal/placement"
	"github.com/braedonsaunders/ether/internal/remotetranscode"
)

// probeEncoder distinguishes a successful status reporting an active process
// from transport loss. EncoderErr==pending is normal for an active process.
func (r *Runtime) probeEncoder(ctx context.Context, x *execution) error {
	c, cancel := context.WithTimeout(ctx, r.o.EncoderRPCDeadline)
	defer cancel()
	if err := r.o.Eligible(c, nodewire.Identity{Cluster: r.o.Self.Cluster, NodeID: x.route.Fence.Target.NodeID, Boot: x.route.Fence.Target.Boot}, config.RoleTranscode); err != nil {
		return err
	}
	err := x.session.Reconcile(c)
	if errors.Is(err, core.ErrEncoderPending) {
		return nil
	}
	if err == nil {
		return ErrPending
	} // exact retired observation also needs replacement
	return err
}

func (r *Runtime) recoverEncoder(ctx context.Context, old *execution) error {
	if !r.clockReady() {
		return ErrDenied
	}
	coordinator, err := r.CurrentCoordinator(ctx, old.route.Fence.Session)
	if err != nil || coordinator.Identity != r.o.Self || coordinator.Epoch > 0 && !coordinator.Deadline.After(time.Now().Add(r.o.ClockSkew)) {
		return ErrDenied
	}
	r.mu.Lock()
	x := r.executions[old.route.Fence.Session]
	if x == nil || x.route != old.route || x.stopping || x.retired || x.initial == nil || x.output == nil {
		r.mu.Unlock()
		return ErrPending
	}
	if x.replacement != nil {
		r.mu.Unlock()
		return r.finishSupersession(ctx, x)
	}
	r.mu.Unlock()
	// Delivery's committed contiguous prefix is the only restart watermark.
	snapshot, err := r.o.OutputBuffer.Snapshot(old.route.Fence.Session)
	if err != nil {
		return err
	}
	q := cloneAdmissionRequest(x.initial.request)
	if q.Decision.VideoCopy || !q.Decision.AudioCopy || q.Decision.Kind != core.DecisionTranscode || q.Decision.Container != "mpegts" || q.StartOffset != 0 || q.RecoveryStart != nil || q.LiveInput || q.SoftSubtitleStream != nil {
		return remotetranscode.ErrUnsupported
	}
	next := int(snapshot.State.PublishedFloor + 1)
	q.Resume = &core.HLSResume{FirstSegment: next, Generation: uint64(old.route.Fence.Generation + 1)}
	target, claims, caps, err := r.selectTargetExcept(ctx, q, old.route.Fence.Target.NodeID)
	if err != nil {
		return err
	}
	op, err := randomToken()
	if err != nil {
		return err
	}
	request := placement.Request{Session: old.route.Fence.Session, Operation: op, ExpectedGeneration: old.route.Fence.Generation, Controller: old.route.Fence.Controller, Target: target, Role: config.RoleTranscode, Claims: claims, Lease: 2 * time.Minute}
	route, err := r.route(ctx, old.route.Fence.Session)
	if err != nil || route.Fence != old.route.Fence {
		return ErrDenied
	}
	route.SourceGeneration = 0 // Source generations are scoped to this encoder generation.
	route.Fence.Generation++
	route.Fence.Target = target
	replacement := &execution{route: route, request: request, initial: newInitialAdmission(route, q), done: x.done, recoveryCaps: caps}
	// Serialize publication and its watermark with the durable decision. Any
	// request already fetching the former generation must recheck before writing.
	x.output.mu.Lock()
	defer x.output.mu.Unlock()
	snapshot, err = r.o.OutputBuffer.Snapshot(old.route.Fence.Session)
	if err != nil {
		return err
	}
	if next != int(snapshot.State.PublishedFloor+1) {
		return ErrPending
	}
	r.mu.Lock()
	x.replacement = replacement
	r.mu.Unlock()
	return r.finishSupersession(ctx, x)
}

func (r *Runtime) finishSupersession(ctx context.Context, x *execution) error {
	if !r.clockReady() {
		return ErrDenied
	}
	coordinator, err := r.CurrentCoordinator(ctx, x.route.Fence.Session)
	if err != nil || coordinator.Identity != r.o.Self || coordinator.Epoch > 0 && !coordinator.Deadline.After(time.Now().Add(r.o.ClockSkew)) {
		return ErrDenied
	}

	r.mu.Lock()
	replacement := x.replacement
	old := x.route
	r.mu.Unlock()
	if replacement == nil {
		return nil
	}
	if replacement.reserved {
		return r.startReplacement(ctx, x)
	}
	route, request := replacement.route, replacement.request
	next := replacement.initial.request.Resume.FirstSegment
	snapshot, err := r.o.OutputBuffer.Snapshot(old.Fence.Session)
	if err != nil {
		return err
	}
	admission, err := r.readExecutionAdmission(ctx, old)
	if err != nil {
		return err
	}
	raw, err := admission.encode()
	if err != nil {
		return err
	}
	binding := admissionBinding(admission, raw)
	_, err = r.o.Placement.Supersede(ctx, old.Fence, request, func(c context.Context, tx *sql.Tx, rec placement.Record) error {
		if err := r.lockAdmissionHistory(c, tx); err != nil {
			return err
		}
		gate, err := readGate(c, tx, binding.key())
		if err != nil {
			return err
		}
		if gate.Binding != binding || gate.Seal != nil {
			return ErrDenied
		}
		gate.Seal = &operationSeal{Actor: r.o.Self, Reason: sealAuthorityLoss}
		sealed, err := gate.encode()
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(c, `UPDATE clusterplayback_operation_gates SET gate=? WHERE cluster=? AND session=? AND generation=?`, sealed, binding.key().Cluster, binding.key().Session, binding.key().Generation); err != nil {
			return err
		}
		decision, err := json.Marshal(struct {
			Previous       Route
			Next           Route
			Operation      placement.Request
			Request        core.HLSRequest
			OriginalDigest [32]byte
		}{old, route, request, replacement.initial.request, binding.AdmissionDigest})
		if err != nil || len(decision) > maxAdmissionBytes {
			return ErrDenied
		}
		if _, err = tx.ExecContext(c, `INSERT INTO clusterplayback_recovery_decisions(cluster,session,generation,decision) VALUES(?,?,?,?)`, r.o.Self.Cluster, route.Fence.Session, route.Fence.Generation, string(decision)); err != nil {
			return err
		}
		if err = r.recordRecoveryEvent(c, tx, x, replacement); err != nil {
			return err
		}
		data, err := json.Marshal(route)
		if err != nil {
			return err
		}
		// Preserve source affinity if a source move raced replacement planning.
		var currentRaw string
		if err = tx.QueryRowContext(c, `SELECT route FROM clusterplayback_routes WHERE session=? AND generation=?`, old.Fence.Session, old.Fence.Generation).Scan(&currentRaw); err != nil {
			return err
		}
		var current Route
		if json.Unmarshal([]byte(currentRaw), &current) != nil || current.Source != route.Source || current.PlayID != route.PlayID || current.Revision != route.Revision {
			return ErrDenied
		}
		updated, err := tx.ExecContext(c, `UPDATE clusterplayback_routes SET generation=?,route=? WHERE session=? AND generation=? AND route=?`, rec.Generation, string(data), rec.Session, old.Fence.Generation, currentRaw)
		if err != nil {
			return err
		}
		count, err := updated.RowsAffected()
		if err != nil {
			return err
		}
		if count != 1 {
			return ErrDenied
		}
		coordinatorUpdate, err := tx.ExecContext(c, `UPDATE clusterplayback_coordinators SET generation=? WHERE session=? AND generation=? AND node=? AND boot=? AND epoch=? AND deadline>?`, rec.Generation, rec.Session, old.Fence.Generation, r.o.Self.NodeID, r.o.Self.Boot, coordinator.Epoch, time.Now().Add(r.o.ClockSkew).UnixMilli())
		if err != nil {
			return err
		}
		count, err = coordinatorUpdate.RowsAffected()
		if err != nil {
			return err
		}
		if count != 1 {
			return ErrDenied
		}
		// Credentials continue authorizing the same title while the execution moves.
		_, err = tx.ExecContext(c, `UPDATE clusterplayback_plex_bindings SET generation=? WHERE session=? AND generation=?`, rec.Generation, rec.Session, old.Fence.Generation)
		return err
	})
	if err != nil {
		if !isAdmissionUnknown(err) {
			observed, readErr := r.o.Placement.Lookup(ctx, old.Fence.Session)
			if readErr == nil && observed.Fence == old.Fence {
				r.mu.Lock()
				if x.replacement == replacement {
					x.replacement = nil
				}
				r.mu.Unlock()
			}
		}
		return err
	}
	r.routeChanged(ctx, route.Fence.Session)
	if snapshot.State.Generation != uint64(route.Fence.Generation) {
		err = r.o.OutputBuffer.AdvanceGeneration(old.Fence.Session, snapshot.State, uint64(route.Fence.Generation))
	}
	if err != nil {
		return err
	}
	r.mu.Lock()
	x.replacement = replacement
	x.output.supersededAt = int64(next)
	replacement.reserved = true
	x.failed = append(x.failed, &execution{route: old, session: x.session, adapter: x.adapter, stopping: true, reserved: true})
	r.mu.Unlock()
	return r.startReplacement(ctx, x)
}

func (r *Runtime) startReplacement(ctx context.Context, x *execution) error {
	r.mu.Lock()
	next := x.replacement
	r.mu.Unlock()
	if next == nil {
		return nil
	}
	route := next.route
	r.mu.Lock()
	stopping := x.stopping || r.closed
	if stopping {
		x.epoch = next.epoch
		x.route = next.route
		x.request = next.request
		x.adapter = next.adapter
		x.session = next.session
		x.retirement = next.retirement
		x.initial = next.initial
		x.replacement = nil
	}
	r.mu.Unlock()
	if stopping {
		return ErrPending
	}
	if !r.clockReady() {
		return ErrDenied
	}
	if next.adapter == nil {
		bound, err := r.sourceCall(ctx, route, sourceRequest{Op: "bind"})
		if err != nil {
			return err
		}
		rec, err := r.o.Placement.Lookup(ctx, route.Fence.Session)
		if err != nil || rec.Fence != route.Fence || !rec.Authority {
			return ErrPending
		}
		endpoint, tc, check, err := r.peer(ctx, nodewire.Identity{Cluster: r.o.Self.Cluster, NodeID: route.Fence.Target.NodeID, Boot: route.Fence.Target.Boot}, WorkerPath)
		if err != nil {
			return err
		}
		coordinator, err := r.CurrentCoordinator(ctx, route.Fence.Session)
		if err != nil || coordinator.Identity != r.o.Self || !r.ownsCoordination(ctx, route.Fence.Session) {
			return ErrDenied
		}
		next.epoch = coordinator.Epoch
		adapter, err := remotetranscode.NewAdapter(remotetranscode.AdapterOptions{RequestHeaders: r.coordinatorHeaders(route.Fence.Session, coordinator.Epoch), Capabilities: next.recoveryCaps, Endpoint: endpoint, Client: &http.Client{Transport: &http.Transport{TLSClientConfig: tc}}, RecheckServer: check, Decide: r.o.Decide, Hardware: r.o.Hardware, Capacity: 1,
			Assign: func(context.Context, core.HLSRequest) (remotetranscode.Assignment, error) {
				return remotetranscode.Assignment{Identity: remoteIdentity(route), Grant: bound.Stable, Expires: rec.Deadline.Add(-2 * r.o.ClockSkew).UTC()}, nil
			},
			CommitStart: func(c context.Context, id remotetranscode.Identity, hash [32]byte) error {
				return r.persistAdmission(c, next, id, hash)
			},
		})
		if err != nil {
			return err
		}
		next.adapter = adapter
	}
	hs, token, err := next.adapter.StartHLSOwned(ctx, cloneAdmissionRequest(next.initial.request))
	if token != nil {
		next.retirement = token
		next.session, _ = token.(*remotetranscode.Session)
	}
	if err != nil || hs == nil || next.session == nil {
		return ErrPending
	}
	if err = r.completeRecoveryEvent(ctx, route); err != nil {
		return err
	}
	r.mu.Lock()
	output := &bufferedSession{Session: next.session, r: r, route: route, headers: make(map[int64]http.Header), supersededAt: -1}
	x.previous = append(x.previous, x.output)
	x.epoch = next.epoch
	x.route = route
	x.request = next.request
	x.adapter = next.adapter
	x.session = next.session
	x.initial = next.initial
	x.output = output
	x.replacement = nil
	x.failedSince = time.Time{}
	stopped := x.stopping || r.closed
	r.mu.Unlock()
	if stopped {
		r.Stop(route.Fence.Session)
		return ErrPending
	}
	return nil
}

// executionPermit binds the worker's actual retained acceptance before invoking
// its backend. The hash check also prevents a controller from changing the plan
// between durable admission and the start RPC.
func (r *Runtime) armAcceptedExecution(ctx context.Context, q core.HLSRequest) error {
	id := q.ManifestSessionID
	route, err := r.route(ctx, id)
	if err != nil {
		return err
	}
	handle, err := r.worker.AcceptedStart(ctx, remoteIdentity(route))
	if err != nil {
		return err
	}
	accepted, err := r.worker.InspectAcceptedStart(ctx, handle)
	if err != nil {
		return err
	}
	a, err := r.readAdmission(ctx, route)
	if err != nil {
		return err
	}
	raw, err := a.encode()
	if err != nil {
		return err
	}
	binding := admissionBinding(a, raw)
	if accepted.StartHash != binding.StartHash {
		return ErrDenied
	}
	attempt := acceptedAttempt{Nonce: sha256.Sum256([]byte(accepted.AttemptNonce)), Worker: r.o.Self, Identity: accepted.Identity, StartHash: accepted.StartHash}
	if _, err = r.recordAcceptedAttempt(ctx, binding, attempt); err != nil {
		return err
	}
	payload, _ := json.Marshal(struct {
		Node  nodewire.Identity
		Nonce string
	}{r.o.Self, accepted.AttemptNonce})
	_, err = r.recordArmAssociation(ctx, binding, armAssociation{ID: attempt.Nonce, BindingDigest: binding.AdmissionDigest, Payload: payload})
	return err
}

func (r *Runtime) reconcileDisplaced(ctx context.Context, x *execution) {
	for _, old := range x.failed {
		if old.retired {
			continue
		}
		var retained int
		if err := r.o.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM placement_displaced WHERE session=? AND generation=?`, old.route.Fence.Session, old.route.Fence.Generation).Scan(&retained); err == nil && retained == 0 {
			old.retired = true
			if old.adapter != nil {
				old.adapter.Close()
			}
			continue
		}
		if old.session == nil {
			continue
		}
		c, cancel := context.WithTimeout(ctx, r.o.EncoderRPCDeadline)
		_ = old.session.RetireEncoderContext(c)
		cancel()
		if old.session.EncoderErr() != nil {
			continue
		}
		if r.o.Placement.RetireDisplaced(ctx, old.route.Fence, "clusterplayback/exact-displaced-retirement") == nil {
			old.retired = true
			if old.adapter != nil {
				old.adapter.Close()
			}
		}
	}
}

// Membership must freshly authenticate the DIFFERENT incarnation; mere
// disappearance of the old boot, an empty role list or elapsed time is unknown.
func (r *Runtime) retiredWorkerBoot(ctx context.Context, f placement.Fence) error {
	var removed int
	if err := r.o.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM placement_removed_targets WHERE node=? AND boot=?`, f.Target.NodeID, f.Target.Boot).Scan(&removed); err != nil {
		return err
	}
	if removed > 0 {
		return nil
	}

	var boot string
	if err := r.o.DB.QueryRowContext(ctx, `SELECT boot FROM placement_targets WHERE node=?`, f.Target.NodeID).Scan(&boot); err != nil {
		return err
	}
	if boot == "" || boot == f.Target.Boot {
		return ErrPending
	}
	return r.o.MemberRole(ctx, nodewire.Identity{Cluster: r.o.Self.Cluster, NodeID: f.Target.NodeID, Boot: boot}, config.RoleTranscode)
}
func (r *Runtime) reconcileBootRetirements(ctx context.Context) error {
	rows, err := r.o.DB.QueryContext(ctx, `SELECT record FROM placement_displaced ORDER BY session,generation LIMIT ?`, r.o.HistoryCapacity)
	if err != nil {
		return err
	}
	var records []placement.Record
	for rows.Next() {
		var raw string
		var record placement.Record
		if err = rows.Scan(&raw); err != nil {
			break
		}
		if len(raw) > 16384 || json.Unmarshal([]byte(raw), &record) != nil {
			err = ErrDenied
			break
		}
		records = append(records, record)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	for _, record := range records {
		if r.retiredWorkerBoot(ctx, record.Fence) == nil {
			if err = r.o.Placement.RetireDisplaced(ctx, record.Fence, "clusterplayback/authenticated-new-boot"); err != nil {
				return err
			}
		}
	}
	return nil
}
