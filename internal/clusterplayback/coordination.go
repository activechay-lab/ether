package clusterplayback

import (
	"context"
	"database/sql"
	"errors"
	"time"

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
