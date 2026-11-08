package clusterplayback

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// ProtocolSealer uses the deployment's existing cluster credential key. Nil
// never permits plaintext fallback for a distributed protocol snapshot.
type ProtocolSealer interface {
	Seal(string, []byte) (string, error)
	Unseal(string, string) ([]byte, error)
}

func init() {
	// Sealed Jellyfin/Emby session snapshots are transient; a removed or
	// merged viewer's snapshots are dropped, never moved.
	store.RegisterViewerTable("clusterplayback", "clusterplayback_neutral_bindings", "viewer", store.ViewerMergeDrop)
	store.Register("clusterplayback", 8, `
CREATE TABLE clusterplayback_neutral_bindings (
 session TEXT PRIMARY KEY, generation BIGINT NOT NULL, protocol TEXT NOT NULL,
 viewer BIGINT NOT NULL, device TEXT NOT NULL, expires BIGINT NOT NULL, binding TEXT NOT NULL);
CREATE INDEX clusterplayback_neutral_scope ON clusterplayback_neutral_bindings(protocol,viewer,device,expires);
CREATE TABLE clusterplayback_neutral_stops(session TEXT PRIMARY KEY);

`)
}

const neutralBindingTTL = 10 * time.Minute

func neutralBindingName(id string, generation int64) string {
	return fmt.Sprintf("clusterplayback/neutral/%s/%d", id, generation)
}
func (r *Runtime) NeutralSessionCurrent(ctx context.Context, id string) bool {
	if !r.clockReady() || !r.ownsCoordination(ctx, id) {
		return false
	}
	var stopped int
	return r.o.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM clusterplayback_neutral_stops WHERE session=?`, id).Scan(&stopped) == nil && stopped == 0
}
func (r *Runtime) SaveNeutralBinding(ctx context.Context, id, protocol string, viewer int64, device string, raw []byte) error {
	if !r.clockReady() || r.o.ProtocolSealer == nil || len(raw) == 0 || len(raw) > maxAdmissionBytes || protocol != "jellyfin" && protocol != "emby" || viewer <= 0 || device == "" || len(device) > 256 || !r.ownsCoordination(ctx, id) {
		return ErrDenied
	}
	route, err := r.route(ctx, id)
	if err != nil || route.Viewer != viewer {
		return ErrDenied
	}
	record, err := r.o.Placement.Lookup(ctx, id)
	if err != nil || record.Fence != route.Fence || record.Retired || !record.Authority {
		return ErrDenied
	}
	c, err := r.CurrentCoordinator(ctx, id)
	if err != nil {
		return err
	}
	sealed, err := r.o.ProtocolSealer.Seal(neutralBindingName(id, record.Generation), raw)
	if err != nil {
		return ErrDenied
	}
	// Bound ciphertext and prune expired rows; unexpired rows belong to live
	// placement sessions and leave with their compacted tombstone.
	if len(sealed) > 2*maxAdmissionBytes {
		return ErrDenied
	}
	if _, err = r.o.DB.ExecContext(ctx, `DELETE FROM clusterplayback_neutral_bindings WHERE expires<?`, time.Now().UnixMilli()); err != nil {
		return err
	}
	return r.withNeutralCoordinator(ctx, id, c, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `INSERT INTO clusterplayback_neutral_bindings(session,generation,protocol,viewer,device,expires,binding) SELECT session,generation,?,?,?,?,? FROM clusterplayback_coordinators WHERE session=? AND generation=? AND node=? AND boot=? AND epoch=? AND deadline>? AND NOT EXISTS (SELECT 1 FROM clusterplayback_neutral_stops WHERE session=clusterplayback_coordinators.session) ON CONFLICT(session) DO UPDATE SET generation=excluded.generation,expires=excluded.expires,binding=excluded.binding WHERE clusterplayback_neutral_bindings.generation<=excluded.generation AND clusterplayback_neutral_bindings.protocol=excluded.protocol AND clusterplayback_neutral_bindings.viewer=excluded.viewer AND clusterplayback_neutral_bindings.device=excluded.device`, protocol, viewer, device, time.Now().Add(neutralBindingTTL).UnixMilli(), sealed, id, record.Generation, r.o.Self.NodeID, r.o.Self.Boot, c.Epoch, time.Now().Add(r.o.ClockSkew).UnixMilli())
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if count != 1 {
			return ErrDenied
		}
		return nil
	})
}

func (r *Runtime) LoadNeutralBinding(ctx context.Context, id string) ([]byte, error) {
	if !r.clockReady() || r.o.ProtocolSealer == nil || r.o.MemberRole(ctx, r.o.Self, config.RoleServer) != nil {
		return nil, ErrDenied
	}
	rec, err := r.o.Placement.Lookup(ctx, id)
	if err != nil || rec.Retired || !rec.Authority {
		return nil, ErrDenied
	}
	var sealed string
	var generation int64
	var viewer int64
	err = r.o.DB.QueryRowContext(ctx, `SELECT generation,viewer,binding FROM clusterplayback_neutral_bindings WHERE session=? AND generation<=? AND expires>?`, id, rec.Generation, time.Now().UnixMilli()).Scan(&generation, &viewer, &sealed)
	if err != nil {
		return nil, err
	}
	route, err := r.route(ctx, id)
	if err != nil || route.Viewer != viewer || len(sealed) > 2*maxAdmissionBytes {
		return nil, ErrDenied
	}
	raw, err := r.o.ProtocolSealer.Unseal(neutralBindingName(id, generation), sealed)
	if err != nil || len(raw) > maxAdmissionBytes {
		return nil, ErrDenied
	}
	return raw, nil
}
func (r *Runtime) NeutralBindingIDs(ctx context.Context, protocol string, viewer int64, device string) ([]string, error) {
	if r.o.ProtocolSealer == nil {
		return nil, nil
	}
	if !r.clockReady() || viewer <= 0 || device == "" || protocol != "jellyfin" && protocol != "emby" || r.o.MemberRole(ctx, r.o.Self, config.RoleServer) != nil {
		return nil, ErrDenied
	}
	rows, err := r.o.DB.QueryContext(ctx, `SELECT b.session FROM clusterplayback_neutral_bindings b JOIN placement_sessions p ON p.session=b.session AND p.generation>=b.generation WHERE b.protocol=? AND b.viewer=? AND b.device=? AND b.expires>? AND p.retired=0 ORDER BY b.session LIMIT 4096`, protocol, viewer, device, time.Now().UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// Claim only the existing public registry row; never create a new admission or
// elect over a newer session. The mutable coordinator epoch owns this transfer.
func (r *Runtime) ClaimNeutralBinding(ctx context.Context, id, node string) error {
	if node != r.o.Self.NodeID || !r.ownsCoordination(ctx, id) {
		return ErrDenied
	}
	c, err := r.CurrentCoordinator(ctx, id)
	if err != nil {
		return err
	}
	result, err := r.o.DB.ExecContext(ctx, `UPDATE plex_sessions SET node_id=?,updated_at=? WHERE id=? AND active=1 AND protocol IN ('jellyfin','emby') AND EXISTS (SELECT 1 FROM clusterplayback_coordinators c WHERE c.session=? AND c.node=? AND c.boot=? AND c.epoch=? AND c.deadline>?)`, node, time.Now().UnixMilli(), id, id, node, r.o.Self.Boot, c.Epoch, time.Now().Add(r.o.ClockSkew).UnixMilli())
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return core.ErrNotAvailable
	}
	return nil
}

func (r *Runtime) DeleteNeutralBinding(ctx context.Context, id string) error {
	c, err := r.CurrentCoordinator(ctx, id)
	if err != nil {
		return err
	}
	return r.withNeutralCoordinator(ctx, id, c, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO clusterplayback_neutral_stops(session) VALUES(?) ON CONFLICT(session) DO NOTHING`, id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM clusterplayback_neutral_bindings WHERE session=?`, id)
		return err
	})
}

// Updating the existing coordinator row takes the same row lock as takeover.
// Stop and snapshot writes serialize here, including on PostgreSQL: a late
// pre-stop save cannot resurrect deleted credentials after waiting on a lock.
func (r *Runtime) withNeutralCoordinator(ctx context.Context, id string, c Coordinator, write func(*sql.Tx) error) error {
	if !r.clockReady() {
		return ErrDenied
	}
	tx, err := r.o.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE clusterplayback_coordinators SET epoch=epoch WHERE session=? AND node=? AND boot=? AND epoch=? AND deadline>?`, id, r.o.Self.NodeID, r.o.Self.Boot, c.Epoch, time.Now().Add(r.o.ClockSkew).UnixMilli())
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrDenied
	}
	if err = write(tx); err != nil {
		return err
	}
	return tx.Commit()
}
