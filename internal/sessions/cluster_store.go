package sessions

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// clusterStore uses historical table names with protocol-neutral content.
// Broker rows store protocol-neutral facts; native Plex rows are separate.
type clusterStore struct {
	db   *sql.DB
	node string
}

// clusterEntry contains only the non-secret facts remote session readers need.
type clusterEntry struct {
	ID              string         `json:"id"`
	ViewerID        int64          `json:"viewerID"`
	Item            core.RatingKey `json:"item"`
	StartedAt       time.Time      `json:"startedAt"`
	SourceID        string         `json:"sourceID,omitempty"`
	Channel         string         `json:"channel,omitempty"`
	ClientSessionID string         `json:"clientSessionID,omitempty"`
}

func (c clusterStore) put(ctx context.Context, s frontend.Session, sourceID string, viewer core.Viewer, now time.Time) error {
	var err error
	for attempt := 0; attempt < 8; attempt++ {
		err = c.putOnce(ctx, s, sourceID, viewer, now)
		if err == nil || !retryElection(err) {
			return err
		}
		timer := time.NewTimer(time.Duration(attempt+1) * 20 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return err
}

// retryElection covers uniqueness races, serialization failures, and SQLite
// writer contention. Other database failures are returned immediately.
func retryElection(err error) bool {
	var pg interface{ SQLState() string }
	if errors.As(err, &pg) {
		switch pg.SQLState() {
		case "23505", "40001", "40P01":
			return true
		}
	}
	var sqlite interface{ Code() int }
	if errors.As(err, &sqlite) {
		switch sqlite.Code() & 0xff {
		case 5, 6, 19:
			return true
		}
	}
	return false
}

func (c clusterStore) putOnce(ctx context.Context, s frontend.Session, sourceID string, viewer core.Viewer, now time.Time) error {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var key int64
	if err := tx.QueryRowContext(ctx, `UPDATE plex_counters SET value=value+1 WHERE name='session_key' RETURNING value`).Scan(&key); err != nil {
		return err
	}
	// Persist neither a media grant nor credential.
	d := s.Device
	d.Client = ""
	data, err := json.Marshal(clusterEntry{ID: s.ID, ViewerID: viewer.ID, Item: s.Item, StartedAt: s.StartedAt, SourceID: sourceID, Channel: s.Channel, ClientSessionID: s.ClientSessionID})
	if err != nil {
		return err
	}
	device, err := json.Marshal(d)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE plex_sessions SET active=0 WHERE active=1 AND user_id=? AND surface=? AND device_id=? AND protocol IS NOT NULL AND protocol<>'plex'`, s.ViewerID, s.Protocol, d.ID); err != nil {
		return err
	}
	if s.Protocol == "plex" {
		// Decision and start may use different brokers. Adopt only the exact
		// authenticated public identity; keep its original session key/time.
		var oldData, oldProtocol, oldDevice, oldNode string
		var oldViewer int64
		err := tx.QueryRowContext(ctx, `SELECT node_id,user_id,COALESCE(protocol,''),COALESCE(device_id,''),data FROM plex_sessions WHERE id=?`, s.ID).Scan(&oldNode, &oldViewer, &oldProtocol, &oldDevice, &oldData)
		if err == nil {
			var old clusterEntry
			if oldViewer != s.ViewerID || oldProtocol != "plex" || oldDevice != d.ID || json.Unmarshal([]byte(oldData), &old) != nil || old.ID != s.ID || old.ViewerID != s.ViewerID || old.Item != s.Item {
				return core.ErrUnauthorized
			}
			data, err = json.Marshal(clusterEntry{ID: s.ID, ViewerID: viewer.ID, Item: s.Item, StartedAt: old.StartedAt, SourceID: sourceID, Channel: s.Channel, ClientSessionID: s.ClientSessionID})
			if err != nil {
				return err
			}
			result, err := tx.ExecContext(ctx, `UPDATE plex_sessions SET node_id=?,data=?,device_data=?,updated_at=?,active=1 WHERE id=? AND node_id=? AND user_id=? AND protocol='plex' AND device_id=? AND data=?`, c.node, string(data), string(device), now.UnixMilli(), s.ID, oldNode, s.ViewerID, d.ID, oldData)
			if err != nil {
				return err
			}
			n, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if n != 1 {
				return core.ErrNotAvailable
			}
			return tx.Commit()
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO plex_sessions(id,node_id,user_id,session_key,data,updated_at,protocol,surface,device_id,device_data,active) VALUES(?,?,?,?,?,?,?,?,?,?,1)`, s.ID, c.node, s.ViewerID, key, string(data), now.UnixMilli(), s.Protocol, s.Protocol, d.ID, string(device)); err != nil {
		return err
	}
	return tx.Commit()
}

// cleanup never removes Plex rows, including legacy rows with NULL protocol.
func (c clusterStore) cleanup(ctx context.Context) error {
	_, err := c.db.ExecContext(ctx, `DELETE FROM plex_sessions WHERE node_id=? AND COALESCE(protocol,'plex')<>'plex'`, c.node)
	return err
}

func (c clusterStore) expire(ctx context.Context, cutoff time.Time) error {
	_, err := c.db.ExecContext(ctx, `DELETE FROM plex_sessions WHERE COALESCE(protocol,'plex')<>'plex' AND updated_at<?`, cutoff.UnixMilli())
	return err
}

func (c clusterStore) remove(ctx context.Context, id string) error {
	_, err := c.db.ExecContext(ctx, `DELETE FROM plex_sessions WHERE id=? AND node_id=? AND protocol IS NOT NULL`, id, c.node)
	return err
}

func (c clusterStore) touch(ctx context.Context, id string, now time.Time) error {
	_, err := c.db.ExecContext(ctx, `UPDATE plex_sessions SET updated_at=? WHERE id=? AND node_id=? AND protocol IS NOT NULL AND active=1`, now.UnixMilli(), id, c.node)
	return err
}

func (c clusterStore) active(ctx context.Context, id string) (bool, error) {
	var active int
	err := c.db.QueryRowContext(ctx, `SELECT active FROM plex_sessions WHERE id=? AND node_id=? AND COALESCE(protocol,'plex')<>'plex'`, id, c.node).Scan(&active)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return active == 1, err
}
func (c clusterStore) current(ctx context.Context, id string) (bool, error) {
	var active int
	err := c.db.QueryRowContext(ctx, `SELECT active FROM plex_sessions WHERE id=? AND COALESCE(protocol,'plex')<>'plex'`, id).Scan(&active)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return active == 1, err
}

type remoteSession struct {
	session  frontend.Session
	node     string
	sourceID string
}

func (c clusterStore) list(ctx context.Context, cutoff time.Time, live map[string]bool) ([]remoteSession, error) {
	rows, err := c.db.QueryContext(ctx, `SELECT node_id,user_id,data,protocol,device_data FROM plex_sessions WHERE node_id<>? AND protocol IS NOT NULL AND active=1 AND updated_at>=?`, c.node, cutoff.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []remoteSession
	for rows.Next() {
		var node, data, protocol string
		var device sql.NullString
		var viewer int64
		if err := rows.Scan(&node, &viewer, &data, &protocol, &device); err != nil {
			return nil, err
		}
		if live != nil && !live[node] {
			continue
		}
		var e clusterEntry
		var d core.Device
		if json.Unmarshal([]byte(data), &e) != nil || !device.Valid || json.Unmarshal([]byte(device.String), &d) != nil || e.ID == "" || e.ViewerID != viewer {
			continue
		}
		d.Client = ""
		out = append(out, remoteSession{node: node, sourceID: e.SourceID, session: frontend.Session{ID: e.ID, ViewerID: viewer, Protocol: frontend.Protocol(protocol), Item: e.Item, Channel: e.Channel, ClientSessionID: e.ClientSessionID, Device: d, StartedAt: e.StartedAt}})
	}
	return out, rows.Err()
}

// migrateEntries converts pre-v0.3.4 broker snapshots atomically at startup.
// Native Plex snapshots (surface IS NULL) remain owned by the Plex protocol.
func (c clusterStore) migrateEntries(ctx context.Context) (int, error) {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, "SELECT id,user_id,data FROM plex_sessions WHERE surface IS NOT NULL")
	if err != nil {
		return 0, err
	}
	type record struct {
		id     string
		viewer int64
		raw    string
	}
	var all []record
	for rows.Next() {
		var r record
		if err := rows.Scan(&r.id, &r.viewer, &r.raw); err != nil {
			rows.Close()
			return 0, err
		}
		all = append(all, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	changed := 0
	for _, r := range all {
		var probe map[string]json.RawMessage
		if err := json.Unmarshal([]byte(r.raw), &probe); err != nil {
			return 0, err
		}
		if _, current := probe["viewerID"]; current {
			continue
		}
		// Only this converter knows the retired snapshot shape.
		var old struct {
			ID                                 string
			User                               struct{ ID int64 }
			Key                                core.RatingKey
			StartedAt                          time.Time
			SourceID, Channel, ClientSessionID string
		}
		if err := json.Unmarshal([]byte(r.raw), &old); err != nil {
			return 0, err
		}
		if old.ID != r.id || old.User.ID != r.viewer {
			return 0, errors.New("invalid stored broker session identity")
		}
		raw, err := json.Marshal(clusterEntry{ID: r.id, ViewerID: r.viewer, Item: old.Key, StartedAt: old.StartedAt, SourceID: old.SourceID, Channel: old.Channel, ClientSessionID: old.ClientSessionID})
		if err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE plex_sessions SET data=? WHERE id=? AND data=?", string(raw), r.id, r.raw); err != nil {
			return 0, err
		}
		changed++
	}
	return changed, tx.Commit()
}
