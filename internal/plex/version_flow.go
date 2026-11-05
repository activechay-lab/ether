package plex

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.Register("plex_version_flows", 1, `
CREATE TABLE plex_version_flows (
 user_id BIGINT NOT NULL, client_id TEXT NOT NULL, title_key BIGINT NOT NULL,
 data TEXT NOT NULL, frozen INTEGER NOT NULL, expires BIGINT NOT NULL,
 PRIMARY KEY(user_id,client_id,title_key)
);
CREATE INDEX plex_version_flows_expiry ON plex_version_flows(expires);`)
	// Version flows are transient per-viewer play state: drop on purge/merge.
	store.RegisterViewerTable("plex_version_flows", "plex_version_flows", "user_id", store.ViewerMergeDrop)
}

const versionPreplayTTL = 90 * time.Second
const (
	versionRead = iota
	versionOffer
	versionFreeze
	versionEnd
	versionTouch
)

// Only the facts needed to render versions are shared. No descriptors, URLs,
// credentials, full releases or viewer-authorized metadata are persisted.
type storedVersion struct {
	ID      string         `json:"i"`
	Label   string         `json:"l"`
	Edition string         `json:"e,omitempty"`
	Auto    bool           `json:"a,omitempty"`
	Info    core.MediaInfo `json:"m"`
}
type versionSnapshot struct {
	variants []core.ReleaseVariant
	frozen   bool
	until    time.Time
}
type versionFlowResult struct {
	versionSnapshot
	err error
}
type versionFlowCommand struct {
	key   playIdentityKey
	data  string
	mode  int
	now   time.Time
	ttl   time.Duration
	reply chan versionFlowResult
}
type versionFlowStore struct {
	s        *Server
	db       *sql.DB
	commands chan versionFlowCommand
}

func newVersionFlowStore(s *Server, db *sql.DB) *versionFlowStore {
	v := &versionFlowStore{s: s, db: db, commands: make(chan versionFlowCommand, 128)}
	s.wg.Add(1)
	go v.loop()
	return v
}

// call never writes SQL on the request goroutine. A publication barrier is
// essential: returning a pre-play response before its snapshot is committed
// allows the next request on another node to race publication. Concurrent
// operations are batched in one bounded transaction by the worker.
func (v *versionFlowStore) call(ctx context.Context, k playIdentityKey, vs []core.ReleaseVariant, mode int, now time.Time, ttl time.Duration) (versionSnapshot, error) {
	if !k.valid() {
		return versionSnapshot{}, errors.New("invalid version flow identity")
	}
	if len(vs) > core.MaxVariants {
		return versionSnapshot{}, errors.New("too many versions")
	}
	rows := make([]storedVersion, 0, len(vs))
	for _, x := range vs {
		rows = append(rows, storedVersion{ID: x.Release.ID, Label: x.Label, Edition: x.Edition, Auto: x.Auto, Info: x.Info})
	}
	b, err := json.Marshal(rows)
	if err != nil {
		return versionSnapshot{}, err
	}
	if len(b) > 64<<10 {
		return versionSnapshot{}, errors.New("version snapshot too large")
	}
	if len(vs) < 2 && mode == versionOffer {
		mode = versionRead
	} // cold detail never pins Auto
	cmd := versionFlowCommand{k, string(b), mode, now, ttl, make(chan versionFlowResult, 1)}
	select {
	case v.commands <- cmd:
	case <-ctx.Done():
		return versionSnapshot{}, ctx.Err()
	case <-v.s.ctx.Done():
		return versionSnapshot{}, errors.New("server closed")
	}
	select {
	case r := <-cmd.reply:
		return r.versionSnapshot, r.err
	case <-ctx.Done():
		return versionSnapshot{}, ctx.Err()
	case <-v.s.ctx.Done():
		return versionSnapshot{}, errors.New("server closed")
	}
}
func (v *versionFlowStore) loop() {
	defer v.s.wg.Done()
	lastSweep := time.Time{}
	for {
		var first versionFlowCommand
		select {
		case <-v.s.ctx.Done():
			return
		case first = <-v.commands:
		}
		batch := []versionFlowCommand{first}
	drain:
		for len(batch) < 64 {
			select {
			case c := <-v.commands:
				batch = append(batch, c)
			default:
				break drain
			}
		}
		ctx, cancel := context.WithTimeout(v.s.ctx, dbTimeout)
		tx, err := v.db.BeginTx(ctx, nil)
		results := make([]versionFlowResult, len(batch))
		if err == nil {
			for i, c := range batch {
				results[i].versionSnapshot, err = versionFlowSQL(ctx, tx, c)
				if err != nil {
					break
				}
			}
			if err == nil && first.now.Sub(lastSweep) > time.Minute {
				_, err = tx.ExecContext(ctx, `DELETE FROM plex_version_flows WHERE (user_id,client_id,title_key) IN (SELECT user_id,client_id,title_key FROM plex_version_flows WHERE expires<? LIMIT 128)`, first.now.UnixMilli())
				if err == nil {
					lastSweep = first.now
				}
			}
			if err == nil {
				err = tx.Commit()
			} else {
				_ = tx.Rollback()
			}
		}
		cancel()
		for i, c := range batch {
			results[i].err = err
			c.reply <- results[i]
		}
	}
}
func versionFlowSQL(ctx context.Context, tx *sql.Tx, c versionFlowCommand) (versionSnapshot, error) {
	now := c.now.UnixMilli()
	if c.mode == versionEnd {
		_, err := tx.ExecContext(ctx, `DELETE FROM plex_version_flows WHERE user_id=? AND client_id=? AND title_key=?`, c.key.userID, c.key.clientID, int64(c.key.key))
		return versionSnapshot{}, err
	}
	if c.mode == versionTouch {
		_, err := tx.ExecContext(ctx, `UPDATE plex_version_flows SET expires=? WHERE user_id=? AND client_id=? AND title_key=? AND frozen=1 AND expires>? AND expires<?`, c.now.Add(playIdentityTTL).UnixMilli(), c.key.userID, c.key.clientID, int64(c.key.key), now, c.now.Add(20*time.Minute).UnixMilli())
		return versionSnapshot{}, err
	}

	if c.mode != versionRead {
		frozen := 0
		if c.mode == versionFreeze {
			frozen = 1
		}
		// A freeze adopts the list already issued by any node. Offers may change
		// only before that boundary; an active list is never re-resolved locally.
		_, err := tx.ExecContext(ctx, `INSERT INTO plex_version_flows(user_id,client_id,title_key,data,frozen,expires) VALUES(?,?,?,?,?,?)
ON CONFLICT(user_id,client_id,title_key) DO UPDATE SET
 data=CASE WHEN plex_version_flows.expires<=? OR (plex_version_flows.frozen=0 AND excluded.frozen=0) THEN excluded.data ELSE plex_version_flows.data END,
 frozen=CASE WHEN plex_version_flows.expires<=? OR plex_version_flows.frozen=0 THEN excluded.frozen ELSE plex_version_flows.frozen END,
 expires=CASE WHEN plex_version_flows.expires<=? OR plex_version_flows.frozen=0 OR excluded.frozen=1 THEN excluded.expires ELSE plex_version_flows.expires END`, c.key.userID, c.key.clientID, int64(c.key.key), c.data, frozen, c.now.Add(c.ttl).UnixMilli(), now, now, now)
		if err != nil {
			return versionSnapshot{}, err
		}
	}
	var data string
	var frozen int
	var expires int64
	err := tx.QueryRowContext(ctx, `SELECT data,frozen,expires FROM plex_version_flows WHERE user_id=? AND client_id=? AND title_key=? AND expires>?`, c.key.userID, c.key.clientID, int64(c.key.key), now).Scan(&data, &frozen, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return versionSnapshot{}, nil
	}
	if err != nil {
		return versionSnapshot{}, err
	}
	if len(data) > 64<<10 {
		return versionSnapshot{}, errors.New("oversized stored version flow")
	}
	var rows []storedVersion
	if err = json.Unmarshal([]byte(data), &rows); err != nil {
		return versionSnapshot{}, err
	}
	if len(rows) > core.MaxVariants {
		return versionSnapshot{}, fmt.Errorf("stored version count %d", len(rows))
	}
	out := make([]core.ReleaseVariant, len(rows))
	for i, x := range rows {
		out[i] = core.ReleaseVariant{ID: x.ID, Label: x.Label, Edition: x.Edition, Auto: x.Auto, Release: core.Release{ID: x.ID}, Info: x.Info}
	}
	return versionSnapshot{out, frozen != 0, time.UnixMilli(expires)}, nil
}

func (s *Server) endVersionFlow(ctx context.Context, k playIdentityKey) {
	s.playIDs.mu.Lock()
	if e := s.playIDs.m[k]; e != nil {
		e.frozen = false
		e.resolved = false
		e.variants = nil
		e.flowUntil = time.Time{}
		e.selected = 0
		e.admittedRelease = ""
		e.autoAligned = ""
	}
	s.playIDs.mu.Unlock()
	if s.versionFlows != nil {
		if _, err := s.versionFlows.call(ctx, k, nil, versionEnd, s.now(), 0); err != nil {
			s.log.Warn("end version flow", "err", err)
		}
	}
}
