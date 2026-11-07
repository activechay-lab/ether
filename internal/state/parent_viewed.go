package state

import (
	"context"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// fillParentViewed recovers a parent's display timestamp when imported leaf
// state did not cascade. Batch the indexed leaf ranges so a browse page does
// not issue a separate SQL statement for every unwatched show or season.
func (s *State) fillParentViewed(ctx context.Context, user int64, status map[core.RatingKey]core.WatchStatus) error {
	type parentRange struct{ key, lo, hi core.RatingKey }
	var parents []parentRange
	for k, st := range status {
		if !st.LastViewedAt.IsZero() || (k.Kind() != core.KindShow && k.Kind() != core.KindSeason) {
			continue
		}
		lo, hi := core.EpisodeKey(k.TMDB(), 0, 0), core.EpisodeKey(k.TMDB()+1, 0, 0)
		if k.Kind() == core.KindSeason {
			lo = core.EpisodeKey(k.TMDB(), k.Season(), 0)
			hi = core.EpisodeKey(k.TMDB(), k.Season()+1, 0)
		}
		parents = append(parents, parentRange{k, lo, hi})
	}
	for chunk := range store.Chunks(parents, 200) {
		var q strings.Builder
		q.WriteString(`WITH parents(key,lo,hi) AS (VALUES `)
		args := make([]any, 0, len(chunk)*3+1)
		for i, p := range chunk {
			if i > 0 {
				q.WriteByte(',')
			}
			q.WriteString(`(CAST(? AS BIGINT),CAST(? AS BIGINT),CAST(? AS BIGINT))`)
			args = append(args, int64(p.key), int64(p.lo), int64(p.hi))
		}
		q.WriteString(`) SELECT key,COALESCE((SELECT MAX(last_viewed_at) FROM state_watch WHERE user_id=? AND rating_key>=parents.lo AND rating_key<parents.hi),0) FROM parents`)
		args = append(args, user)
		rows, err := s.db.QueryContext(ctx, q.String(), args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var k core.RatingKey
			var stamp int64
			if err := rows.Scan(&k, &stamp); err != nil {
				rows.Close()
				return err
			}
			st := status[k]
			st.LastViewedAt = unixTime(stamp)
			status[k] = st
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
