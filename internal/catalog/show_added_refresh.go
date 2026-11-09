package catalog

import (
	"context"
	"time"

	"github.com/braedonsaunders/ether/internal/store"
)

// refreshShowAdded advances a durable episode cursor. Each transaction reads
// at most 16 index entries, including ties; restarting a leader resumes it.
func (c *Catalog) refreshShowAdded(ctx context.Context) (bool, error) {
	const batchSize = 16
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var ts, key int64
	if err = tx.QueryRowContext(ctx, `SELECT ts,episode_key FROM catalog_show_added_cursor WHERE id=1`).Scan(&ts, &key); err != nil {
		return false, err
	}
	now := c.now().Unix()
	indexHint := ""
	if store.DialectOf(c.db) == store.SQLite {
		indexHint = c.indexedBy("catalog_titles_episode_recent")
	}
	type episode struct{ ts, key, show int64 }
	var batch []episode
	read := func(predicate string, args ...any) error {
		args = append(args, batchSize-len(batch))
		rows, err := tx.QueryContext(ctx, `SELECT released_ts,key,show_key FROM catalog_titles`+indexHint+` WHERE type='episode' AND release_known=1 AND `+predicate+` ORDER BY released_ts ASC,key DESC LIMIT ?`, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var e episode
			if err := rows.Scan(&e.ts, &e.key, &e.show); err != nil {
				return err
			}
			batch = append(batch, e)
		}
		return rows.Err()
	}
	if ts <= now {
		if err = read(`released_ts=? AND key<?`, ts, key); err != nil {
			return false, err
		}
		if len(batch) < batchSize {
			if err = read(`released_ts>? AND released_ts<=?`, ts, now); err != nil {
				return false, err
			}
		}
	}
	for _, e := range batch {
		// A recomputation handles a premiere later than an earlier aired episode.
		_, err = tx.ExecContext(ctx, `UPDATE catalog_titles SET show_added_ts=COALESCE((SELECT e.released_ts FROM catalog_titles e WHERE e.type='episode' AND e.show_key=catalog_titles.key AND e.release_known=1 AND e.released_ts<=? ORDER BY e.released_ts DESC LIMIT 1),released_ts) WHERE type='show' AND key=?`, now, e.show)
		if err != nil {
			return false, err
		}
		ts, key = e.ts, e.key
	}
	if len(batch) < batchSize {
		ts = now
		key = 1<<63 - 1
	}
	if _, err = tx.ExecContext(ctx, `UPDATE catalog_show_added_cursor SET ts=?,episode_key=? WHERE id=1`, ts, key); err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	if len(batch) > 0 {
		c.writeGen.Add(1)
	}
	return len(batch) == batchSize, nil
}

func (c *Catalog) refreshShowAddedLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		for ctx.Err() == nil {
			more, err := c.refreshShowAdded(ctx)
			if err != nil {
				if ctx.Err() == nil {
					c.log.Warn("catalog: refresh show date added", "err", err)
				}
				break
			}
			if !more {
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
