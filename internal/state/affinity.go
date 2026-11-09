package state

import (
	"context"
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// Search affinity reads watch state by title across viewers, which the
// (user_id, rating_key) primary key cannot serve. It is its own component:
// state/5 is deliberately left unused (see upgrade_test.go).
func init() {
	store.RegisterAfter("state_search", "state")
	store.Register("state_search", 1, `CREATE INDEX state_watch_key ON state_watch(rating_key)`)
}

// Familiar reports userID's history with each of keys (movies and shows):
// core.AffinityViewer when they watched, started or rated it,
// core.AffinityServer when only someone else on this server did (a PMS
// library holds what its household watches), absent otherwise. A show
// counts through any of its episodes. Every probe is an index seek that
// stops at its first row, so the cost is the number of keys, never the
// size of anyone's history.
func (s *State) Familiar(ctx context.Context, userID int64, keys []core.RatingKey) (map[core.RatingKey]int, error) {
	out := map[core.RatingKey]int{}
	if len(keys) == 0 {
		return out, nil
	}
	const touched = `(view_count>0 OR view_offset_ns>0 OR user_rating>0)`
	type probe struct {
		sql  string
		args []any
	}
	var probes []probe
	for _, k := range keys {
		switch k.Kind() {
		case core.KindMovie:
			probes = append(probes, probe{`SELECT CAST(? AS BIGINT) AS k, CASE
				WHEN EXISTS (SELECT 1 FROM state_watch WHERE user_id=? AND rating_key=? AND ` + touched + `) THEN 2
				WHEN EXISTS (SELECT 1 FROM state_watch WHERE rating_key=? AND ` + touched + `) THEN 1 ELSE 0 END AS level`,
				[]any{int64(k), userID, int64(k), int64(k)}})
		case core.KindShow:
			lo, hi := int64(core.EpisodeKey(k.TMDB(), 0, 0)), int64(core.EpisodeKey(k.TMDB()+1, 0, 0))
			probes = append(probes, probe{`SELECT CAST(? AS BIGINT) AS k, CASE
				WHEN EXISTS (SELECT 1 FROM state_watch WHERE user_id=? AND rating_key>=? AND rating_key<? AND ` + touched + `) THEN 2
				WHEN EXISTS (SELECT 1 FROM state_watch WHERE rating_key>=? AND rating_key<? AND ` + touched + `) THEN 1 ELSE 0 END AS level`,
				[]any{int64(k), userID, lo, hi, lo, hi}})
		}
	}
	for start := 0; start < len(probes); start += 100 {
		var parts []string
		var args []any
		for i, p := range probes[start:min(start+100, len(probes))] {
			// Only titles someone touched come back: most candidates have
			// no history, and the result is one row per familiar title.
			parts = append(parts, "SELECT k, level FROM ("+p.sql+") AS p"+strconv.Itoa(i)+" WHERE level>0")
			args = append(args, p.args...)
		}
		rows, err := s.db.QueryContext(ctx, strings.Join(parts, " UNION ALL "), args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var k int64
			var level int
			if err := rows.Scan(&k, &level); err != nil {
				rows.Close()
				return nil, err
			}
			if level > 0 {
				out[core.RatingKey(k)] = level
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
