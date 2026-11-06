package clusterplayback

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// RecoveryEvent is control-plane evidence. Missing clientStalls means no player
// measurement exists; controller timing never manufactures that observation.
type RecoveryEvent struct {
	SessionID         string     `json:"sessionId"`
	Role              string     `json:"role"`
	FailedNodeID      string     `json:"failedNodeId"`
	ReplacementNodeID string     `json:"replacementNodeId"`
	Generation        int64      `json:"generation"`
	StartedAt         time.Time  `json:"startedAt"`
	CompletedAt       *time.Time `json:"completedAt,omitempty"`
	Status            string     `json:"status"`
	InterruptionMS    *int64     `json:"interruptionMs,omitempty"`
	ClientStalls      *int       `json:"clientStalls,omitempty"`
}

func (r *Runtime) recordRecoveryEvent(ctx context.Context, tx *sql.Tx, x, next *execution) error {
	started := x.failedSince
	if started.IsZero() {
		started = time.Now()
	}
	event := RecoveryEvent{SessionID: next.route.Fence.Session, Role: "transcode", FailedNodeID: x.route.Fence.Target.NodeID, ReplacementNodeID: next.route.Fence.Target.NodeID, Generation: next.route.Fence.Generation, StartedAt: started.UTC(), Status: "recovering"}
	raw, err := json.Marshal(event)
	if err != nil {
		return err
	}
	// The placement lock serializes inserts and enforces the cluster-wide bound.
	if _, err = tx.ExecContext(ctx, `DELETE FROM clusterplayback_recovery_events WHERE cluster=? AND ordinal IN (SELECT ordinal FROM clusterplayback_recovery_events WHERE cluster=? ORDER BY ordinal DESC LIMIT 4096 OFFSET 255)`, r.o.Self.Cluster, r.o.Self.Cluster); err != nil {
		return err
	}
	var ordinal int64
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(ordinal),0)+1 FROM clusterplayback_recovery_events WHERE cluster=?`, r.o.Self.Cluster).Scan(&ordinal); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO clusterplayback_recovery_events(cluster,ordinal,session,generation,event) VALUES(?,?,?,?,?)`, r.o.Self.Cluster, ordinal, event.SessionID, event.Generation, string(raw))
	return err
}
func (r *Runtime) completeRecoveryEvent(ctx context.Context, route Route) error {
	var raw string
	err := r.o.DB.QueryRowContext(ctx, `SELECT event FROM clusterplayback_recovery_events WHERE cluster=? AND session=? AND generation=?`, r.o.Self.Cluster, route.Fence.Session, route.Fence.Generation).Scan(&raw)
	if err != nil {
		return err
	}
	var event RecoveryEvent
	if json.Unmarshal([]byte(raw), &event) != nil {
		return ErrDenied
	}
	if event.Status == "recovered" {
		return nil
	}
	now := time.Now().UTC()
	elapsed := now.Sub(event.StartedAt).Milliseconds()
	event.CompletedAt = &now
	event.InterruptionMS = &elapsed
	event.Status = "recovered"
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_, err = r.o.DB.ExecContext(ctx, `UPDATE clusterplayback_recovery_events SET event=? WHERE cluster=? AND session=? AND generation=?`, string(data), r.o.Self.Cluster, route.Fence.Session, route.Fence.Generation)
	return err
}
func (r *Runtime) RecoveryEvents(ctx context.Context, limit int) ([]RecoveryEvent, error) {
	if limit <= 0 || limit > 256 {
		limit = 256
	}
	rows, err := r.o.DB.QueryContext(ctx, `SELECT event FROM clusterplayback_recovery_events WHERE cluster=? ORDER BY ordinal DESC LIMIT ?`, r.o.Self.Cluster, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := make([]RecoveryEvent, 0)
	for rows.Next() {
		var raw string
		var event RecoveryEvent
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if len(raw) > 4096 || json.Unmarshal([]byte(raw), &event) != nil {
			return nil, ErrDenied
		}
		events = append(events, event)
	}
	return events, rows.Err()
}
