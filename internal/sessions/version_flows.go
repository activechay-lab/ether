package sessions

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

const VersionPreplayTTL = 90 * time.Second
const versionFlowIdentityTTL = 30 * time.Minute
const versionFlowDBTimeout = 5 * time.Second
const (
	VersionRead = iota
	VersionOffer
	VersionFreeze
	VersionEnd
	VersionTouch
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
type VersionSnapshot struct {
	Variants []core.ReleaseVariant
	Frozen   bool
	Until    time.Time
}
type versionFlowResult struct {
	VersionSnapshot
	err error
}
type versionFlowCommand struct {
	key   VersionFlowKey
	data  string
	mode  int
	now   time.Time
	ttl   time.Duration
	reply chan versionFlowResult
}
type VersionFlows struct {
	ctx      context.Context
	db       *sql.DB
	commands chan versionFlowCommand
}

// VersionFlowKey scopes issued versions to one viewer, client and title.
type VersionFlowKey struct {
	UserID   int64
	ClientID string
	Key      core.RatingKey
}

// NewVersionFlows binds the worker to the host lifetime. The host must run Run
// and wait for it during shutdown before closing its database.
func NewVersionFlows(ctx context.Context, db *sql.DB) *VersionFlows {
	return &VersionFlows{ctx: ctx, db: db, commands: make(chan versionFlowCommand, 128)}
}

// call never writes SQL on the request goroutine. A publication barrier is
// essential: returning a pre-play response before its snapshot is committed
// allows the next request on another node to race publication. Concurrent
// operations are batched in one bounded transaction by the worker.
func (v *VersionFlows) Call(ctx context.Context, k VersionFlowKey, vs []core.ReleaseVariant, mode int, now time.Time, ttl time.Duration) (VersionSnapshot, error) {
	if k.UserID == 0 || k.Key == 0 {
		return VersionSnapshot{}, errors.New("invalid version flow identity")
	}
	if len(vs) > core.MaxVariants {
		return VersionSnapshot{}, errors.New("too many versions")
	}
	rows := make([]storedVersion, 0, len(vs))
	for _, x := range vs {
		rows = append(rows, storedVersion{ID: x.Release.ID, Label: x.Label, Edition: x.Edition, Auto: x.Auto, Info: x.Info})
	}
	b, err := json.Marshal(rows)
	if err != nil {
		return VersionSnapshot{}, err
	}
	if len(b) > 64<<10 {
		return VersionSnapshot{}, errors.New("version snapshot too large")
	}
	if len(vs) < 2 && mode == VersionOffer {
		mode = VersionRead
	} // cold detail never pins Auto
	cmd := versionFlowCommand{k, string(b), mode, now, ttl, make(chan versionFlowResult, 1)}
	select {
	case v.commands <- cmd:
	case <-ctx.Done():
		return VersionSnapshot{}, ctx.Err()
	case <-v.ctx.Done():
		return VersionSnapshot{}, errors.New("server closed")
	}
	select {
	case r := <-cmd.reply:
		return r.VersionSnapshot, r.err
	case <-ctx.Done():
		return VersionSnapshot{}, ctx.Err()
	case <-v.ctx.Done():
		return VersionSnapshot{}, errors.New("server closed")
	}
}
func (v *VersionFlows) Run() {
	lastSweep := time.Time{}
	for {
		var first versionFlowCommand
		select {
		case <-v.ctx.Done():
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
		ctx, cancel := context.WithTimeout(v.ctx, versionFlowDBTimeout)
		tx, err := v.db.BeginTx(ctx, nil)
		results := make([]versionFlowResult, len(batch))
		if err == nil {
			for i, c := range batch {
				results[i].VersionSnapshot, err = versionFlowSQL(ctx, tx, c)
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
func versionFlowSQL(ctx context.Context, tx *sql.Tx, c versionFlowCommand) (VersionSnapshot, error) {
	now := c.now.UnixMilli()
	if c.mode == VersionEnd {
		_, err := tx.ExecContext(ctx, `DELETE FROM plex_version_flows WHERE user_id=? AND client_id=? AND title_key=?`, c.key.UserID, c.key.ClientID, int64(c.key.Key))
		return VersionSnapshot{}, err
	}
	if c.mode == VersionTouch {
		_, err := tx.ExecContext(ctx, `UPDATE plex_version_flows SET expires=? WHERE user_id=? AND client_id=? AND title_key=? AND frozen=1 AND expires>? AND expires<?`, c.now.Add(versionFlowIdentityTTL).UnixMilli(), c.key.UserID, c.key.ClientID, int64(c.key.Key), now, c.now.Add(20*time.Minute).UnixMilli())
		return VersionSnapshot{}, err
	}

	if c.mode != VersionRead {
		frozen := 0
		if c.mode == VersionFreeze {
			frozen = 1
		}
		// A freeze adopts the list already issued by any node. Offers may change
		// only before that boundary; an active list is never re-resolved locally.
		_, err := tx.ExecContext(ctx, `INSERT INTO plex_version_flows(user_id,client_id,title_key,data,frozen,expires) VALUES(?,?,?,?,?,?)
ON CONFLICT(user_id,client_id,title_key) DO UPDATE SET
 data=CASE WHEN plex_version_flows.expires<=? OR (plex_version_flows.frozen=0 AND excluded.frozen=0) THEN excluded.data ELSE plex_version_flows.data END,
 frozen=CASE WHEN plex_version_flows.expires<=? OR plex_version_flows.frozen=0 THEN excluded.frozen ELSE plex_version_flows.frozen END,
 expires=CASE WHEN plex_version_flows.expires<=? OR plex_version_flows.frozen=0 OR excluded.frozen=1 THEN excluded.expires ELSE plex_version_flows.expires END`, c.key.UserID, c.key.ClientID, int64(c.key.Key), c.data, frozen, c.now.Add(c.ttl).UnixMilli(), now, now, now)
		if err != nil {
			return VersionSnapshot{}, err
		}
	}
	var data string
	var frozen int
	var expires int64
	err := tx.QueryRowContext(ctx, `SELECT data,frozen,expires FROM plex_version_flows WHERE user_id=? AND client_id=? AND title_key=? AND expires>?`, c.key.UserID, c.key.ClientID, int64(c.key.Key), now).Scan(&data, &frozen, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return VersionSnapshot{}, nil
	}
	if err != nil {
		return VersionSnapshot{}, err
	}
	if len(data) > 64<<10 {
		return VersionSnapshot{}, errors.New("oversized stored version flow")
	}
	var rows []storedVersion
	if err = json.Unmarshal([]byte(data), &rows); err != nil {
		return VersionSnapshot{}, err
	}
	if len(rows) > core.MaxVariants {
		return VersionSnapshot{}, fmt.Errorf("stored version count %d", len(rows))
	}
	out := make([]core.ReleaseVariant, len(rows))
	for i, x := range rows {
		out[i] = core.ReleaseVariant{ID: x.ID, Label: x.Label, Edition: x.Edition, Auto: x.Auto, Release: core.Release{ID: x.ID}, Info: x.Info}
	}
	return VersionSnapshot{out, frozen != 0, time.UnixMilli(expires)}, nil
}
