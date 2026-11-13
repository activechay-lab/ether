package state

import (
	"context"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.RegisterAfter("state_followed", "state")
	store.Register("state_followed", 1, `CREATE INDEX state_watch_followed ON state_watch(last_viewed_at DESC,rating_key) WHERE last_viewed_at>0;`)
}

// RecentlyViewedKeys returns up to limit distinct titles any viewer watched
// since the given time, newest first. It reads only the state_watch_followed
// index, so its cost is bounded by limit, not by library or history size.
func (s *State) RecentlyViewedKeys(ctx context.Context, since time.Time, limit int) ([]core.RatingKey, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT rating_key FROM state_watch WHERE last_viewed_at>0 AND last_viewed_at>=? ORDER BY last_viewed_at DESC LIMIT ?`, since.Unix(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.RatingKey
	for rows.Next() {
		var key int64
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		out = append(out, core.RatingKey(key))
	}
	return out, rows.Err()
}
