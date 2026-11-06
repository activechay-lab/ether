package clusterplayback

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/nodewire"
	"github.com/braedonsaunders/ether/internal/store"
)

// Coordinator is the mutable control-plane owner. Epoch fences coordination;
// neither Epoch nor Identity changes the immutable execution fence/generation.
type Coordinator struct {
	Identity nodewire.Identity
	Epoch    int64
	Deadline time.Time
}

func init() {
	store.Register("clusterplayback", 6, `
CREATE TABLE clusterplayback_coordinators (
 session TEXT PRIMARY KEY, generation BIGINT NOT NULL,
 node TEXT NOT NULL, boot TEXT NOT NULL, epoch BIGINT NOT NULL,
 deadline BIGINT NOT NULL);
CREATE TABLE clusterplayback_plex_bindings (
 session TEXT PRIMARY KEY, generation BIGINT NOT NULL, binding TEXT NOT NULL);
`)
}

// CurrentCoordinator resolves the current control owner independently of the
// execution controller retained in Route.Fence. Callers must not infer encoder
// replacement or retirement from a coordination deadline.
func (r *Runtime) CurrentCoordinator(ctx context.Context, id string) (Coordinator, error) {
	route, err := r.route(ctx, id)
	if err != nil {
		return Coordinator{}, err
	}
	var c Coordinator
	var generation, deadline int64
	err = r.o.DB.QueryRowContext(ctx, `SELECT generation,node,boot,epoch,deadline FROM clusterplayback_coordinators WHERE session=?`, id).Scan(&generation, &c.Identity.NodeID, &c.Identity.Boot, &c.Epoch, &deadline)
	if errors.Is(err, sql.ErrNoRows) {
		if route.Fence.Generation > 0 {
			return Coordinator{}, ErrPending
		}
		return Coordinator{Identity: nodewire.Identity{Cluster: r.o.Self.Cluster, NodeID: route.Fence.Controller.ID, Boot: route.Fence.Controller.Boot}}, nil
	}
	if err != nil {
		return Coordinator{}, err
	}
	if generation != route.Fence.Generation {
		return Coordinator{}, ErrPending
	}
	c.Identity.Cluster = r.o.Self.Cluster
	c.Deadline = time.UnixMilli(deadline)
	return c, nil
}

const coordinationLease = 15 * time.Second

func (r *Runtime) initializeCoordinator(ctx context.Context, route Route) error {
	if !r.clockReady() {
		return ErrDenied
	}
	if route.Fence.Generation <= 0 {
		return nil
	}
	_, err := r.o.DB.ExecContext(ctx, `INSERT INTO clusterplayback_coordinators(session,generation,node,boot,epoch,deadline) VALUES(?,?,?,?,1,?) ON CONFLICT(session) DO UPDATE SET generation=excluded.generation,node=excluded.node,boot=excluded.boot,epoch=clusterplayback_coordinators.epoch+1,deadline=excluded.deadline WHERE clusterplayback_coordinators.generation<excluded.generation`, route.Fence.Session, route.Fence.Generation, route.Fence.Controller.ID, route.Fence.Controller.Boot, time.Now().Add(coordinationLease+2*r.o.ClockSkew).UnixMilli())
	return err
}

func (r *Runtime) ownsCoordination(ctx context.Context, id string) bool {
	c, err := r.CurrentCoordinator(ctx, id)
	return err == nil && c.Identity == r.o.Self && c.Deadline.After(time.Now().Add(r.o.ClockSkew))
}

// maintainCoordinators uses a SQL compare-and-swap. A paused old owner cannot
// renew after takeover. Expiry transfers only coordination, never execution.
func (r *Runtime) maintainCoordinators(ctx context.Context) error {
	if !r.clockReady() || !r.o.Roles.Has(config.RoleServer) || r.o.Eligible(ctx, r.o.Self, config.RoleServer) != nil {
		return nil
	}
	rows, err := r.o.DB.QueryContext(ctx, `SELECT c.session FROM clusterplayback_coordinators c JOIN placement_sessions p ON p.session=c.session AND p.generation=c.generation WHERE p.retired=0 ORDER BY c.session LIMIT 4096`)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if !r.clockReady() {
			return ErrDenied
		}
		c, e := r.CurrentCoordinator(ctx, id)
		if e != nil {
			continue
		}
		now := time.Now()
		if c.Identity == r.o.Self && c.Deadline.After(now.Add(r.o.ClockSkew)) {
			// Once a lease has expired even its former owner must win a new epoch.
			_, e = r.o.DB.ExecContext(ctx, `UPDATE clusterplayback_coordinators SET deadline=? WHERE session=? AND node=? AND boot=? AND epoch=? AND deadline>?`, now.Add(coordinationLease+2*r.o.ClockSkew).UnixMilli(), id, r.o.Self.NodeID, r.o.Self.Boot, c.Epoch, now.Add(r.o.ClockSkew).UnixMilli())
		} else if c.Deadline.Before(now.Add(-r.o.ClockSkew)) {
			_, e = r.o.DB.ExecContext(ctx, `UPDATE clusterplayback_coordinators SET node=?,boot=?,epoch=epoch+1,deadline=? WHERE session=? AND epoch=? AND deadline<?`, r.o.Self.NodeID, r.o.Self.Boot, now.Add(coordinationLease+2*r.o.ClockSkew).UnixMilli(), id, c.Epoch, now.Add(-r.o.ClockSkew).UnixMilli())
		}
		if e != nil {
			return e
		}
		c, e = r.CurrentCoordinator(ctx, id)
		if e != nil || c.Identity != r.o.Self || !r.ownsCoordination(ctx, id) {
			continue
		}
		r.mu.Lock()
		x := r.executions[id]
		needs := x == nil || x.epoch != c.Epoch
		r.mu.Unlock()
		if needs {
			if e = r.attachExecution(ctx, id); e != nil {
				continue
			}
		}
	}
	return nil
}

const coordinatorEpochHeader = "X-Ether-Coordinator-Epoch"

func (r *Runtime) coordinatorHeaders(id string, epoch int64) func(context.Context) (http.Header, error) {
	return func(ctx context.Context) (http.Header, error) {
		c, err := r.CurrentCoordinator(ctx, id)
		if err != nil || c.Identity != r.o.Self || c.Epoch != epoch || !c.Deadline.After(time.Now().Add(r.o.ClockSkew)) {
			return nil, ErrDenied
		}
		return http.Header{coordinatorEpochHeader: []string{strconv.FormatInt(epoch, 10)}}, nil
	}
}

func (r *Runtime) ownsExecution(ctx context.Context, x *execution) bool {
	c, err := r.CurrentCoordinator(ctx, x.route.Fence.Session)
	return err == nil && c.Identity == r.o.Self && c.Epoch == x.epoch && c.Deadline.After(time.Now().Add(r.o.ClockSkew))
}
