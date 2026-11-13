package catalog

import (
	"context"
	"database/sql"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.RegisterAfter("catalog_episode_release", "catalog")
	store.Register("catalog_episode_release", 1, `CREATE TABLE catalog_episode_release(key BIGINT PRIMARY KEY, earliest_ts BIGINT NOT NULL);`)
}

// RecordPlayableEpisode remembers actual playable evidence independently of
// metadata refreshes. released_ts is the earliest known effective release date;
// the evidence survives a later linear air date returned by the metadata source.
func (c *Catalog) RecordPlayableEpisode(ctx context.Context, key core.RatingKey, at time.Time) error {
	if key.Kind() != core.KindEpisode || at.IsZero() {
		return nil
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// A playable premiere also releases its season navigation container.
	for _, k := range []core.RatingKey{key, core.SeasonKey(key.TMDB(), key.Season())} {
		if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_episode_release(key,earliest_ts) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET earliest_ts=CASE WHEN excluded.earliest_ts<catalog_episode_release.earliest_ts THEN excluded.earliest_ts ELSE catalog_episode_release.earliest_ts END`, int64(k), at.Unix()); err != nil {
			return err
		}
		if err = applyEpisodeRelease(ctx, tx, k); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	c.writeGen.Add(1)
	return nil
}

func applyEpisodeRelease(ctx context.Context, tx *sql.Tx, key core.RatingKey) error {
	_, err := tx.ExecContext(ctx, `UPDATE catalog_titles SET released_ts=(SELECT earliest_ts FROM catalog_episode_release WHERE key=?),browsable=1 WHERE key=? AND EXISTS(SELECT 1 FROM catalog_episode_release e WHERE e.key=catalog_titles.key AND (catalog_titles.released_ts=0 OR e.earliest_ts<catalog_titles.released_ts))`, int64(key), int64(key))
	return err
}
