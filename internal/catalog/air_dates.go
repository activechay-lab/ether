package catalog

import (
	"context"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// AirDates returns dates from locally mirrored regular episodes, excluding future air dates.
// For premieres, the highest numbered season with an aired episode must have an aired episode one.
func (m *Mirror) AirDates(ctx context.Context, now time.Time, premiere bool) (map[core.RatingKey]int64, error) {
	cutoff := time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day()+1, 0, 0, 0, 0, time.UTC).Unix()
	query := `SELECT show_key, MAX(released_ts) FROM catalog_titles
		WHERE type = 'episode' AND season > 0 AND episode > 0 AND release_known = 1 AND released_ts < ? GROUP BY show_key`
	if premiere {
		query = `SELECT show_key, released_ts FROM catalog_titles e
			WHERE type = 'episode' AND season > 0 AND episode = 1 AND release_known = 1 AND released_ts < ?
			AND season = (SELECT MAX(season) FROM catalog_titles x WHERE x.show_key = e.show_key AND x.type = 'episode' AND x.season > 0 AND x.episode > 0 AND x.release_known = 1 AND x.released_ts < ?)`
	}
	args := []any{cutoff}
	if premiere {
		args = append(args, cutoff)
	}
	rows, err := m.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	dates := make(map[core.RatingKey]int64)
	for rows.Next() {
		var key core.RatingKey
		var date int64
		if err := rows.Scan(&key, &date); err != nil {
			return nil, err
		}
		if prev, ok := dates[key]; !ok || date > prev {
			dates[key] = date
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return dates, nil
}

// AirDateQuery describes a bounded TV air-date filter evaluated in SQL over
// stored show, season and episode rows.
type AirDateQuery struct {
	Now      time.Time
	Premiere bool
	HasLo    bool
	Lo       int64
	HasHi    bool
	Hi       int64
}

// Cutoff returns the exclusive UTC timestamp of tomorrow's start, matching
// AirDates so future episodes and future season premieres are ignored.
func (q AirDateQuery) Cutoff() int64 {
	u := q.Now.UTC()
	return time.Date(u.Year(), u.Month(), u.Day()+1, 0, 0, 0, 0, time.UTC).Unix()
}

// MatchTimestamp reports whether a non-zero air-date timestamp falls inside q.
func (q AirDateQuery) MatchTimestamp(ts int64) bool {
	if ts == 0 {
		return false
	}
	if ts >= q.Cutoff() {
		return false
	}
	if q.HasLo && ts < q.Lo {
		return false
	}
	if q.HasHi && ts > q.Hi {
		return false
	}
	return true
}

// MatchingAirDateShows returns the show rating keys in catalog_titles whose
// latest aired regular episode (Premiere=false) or latest aired regular season
// premiere (Premiere=true) falls within q.
func (m *Mirror) MatchingAirDateShows(ctx context.Context, q AirDateQuery) (map[core.RatingKey]bool, error) {
	u := q.Now.UTC()
	todayStart := time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC).Unix()
	cutoff := time.Date(u.Year(), u.Month(), u.Day()+1, 0, 0, 0, 0, time.UTC).Unix()
	effectiveHi := cutoff - 1
	if q.HasHi && q.Hi < effectiveHi {
		effectiveHi = q.Hi
	}
	if q.HasLo && q.Lo > effectiveHi {
		return map[core.RatingKey]bool{}, nil
	}

	var query string
	var args []any
	if !q.Premiere {
		var branches []string
		if q.HasLo {
			// Omit inner GROUP BY when HasLo is set so the planner range-seeks
			// released_ts >= Lo AND released_ts < cutoff rather than scanning
			// every show's episode index in show_key order.
			branches = append(branches,
				`SELECT key AS show_key, last_air_ts AS ts FROM catalog_titles WHERE type = 'show' AND adult = 0 AND last_air_ts <> 0 AND last_air_ts >= ? AND last_air_ts < ?`,
				`SELECT show_key, released_ts AS ts FROM catalog_titles WHERE type = 'episode' AND release_known = 1 AND season > 0 AND episode > 0 AND released_ts >= ? AND released_ts < ?`,
				`SELECT show_key, released_ts AS ts FROM catalog_titles WHERE type = 'season' AND release_known = 1 AND adult = 0 AND season > 0 AND child_count > 0 AND released_ts >= ? AND released_ts < ?`,
			)
			args = append(args, q.Lo, cutoff, q.Lo, cutoff, q.Lo, cutoff)
		} else {
			branches = append(branches,
				`SELECT key AS show_key, last_air_ts AS ts FROM catalog_titles WHERE type = 'show' AND adult = 0 AND last_air_ts <> 0 AND last_air_ts < ?`,
				`SELECT show_key, MAX(released_ts) AS ts FROM catalog_titles WHERE type = 'episode' AND release_known = 1 AND season > 0 AND episode > 0 AND released_ts < ? GROUP BY show_key`,
				`SELECT show_key, MAX(released_ts) AS ts FROM catalog_titles WHERE type = 'season' AND release_known = 1 AND adult = 0 AND season > 0 AND child_count > 0 AND released_ts < ? GROUP BY show_key`,
			)
			args = append(args, cutoff, cutoff, cutoff)
		}
		if !q.HasHi || q.Hi >= todayStart {
			if q.HasLo {
				branches = append(branches,
					`SELECT key AS show_key, released_ts AS ts FROM catalog_titles WHERE type = 'show' AND adult = 0 AND detail = 0 AND last_air_ts = 0 AND release_known = 1 AND released_ts >= ? AND released_ts < ?`,
				)
				args = append(args, q.Lo, cutoff)
			} else {
				branches = append(branches,
					`SELECT key AS show_key, released_ts AS ts FROM catalog_titles WHERE type = 'show' AND adult = 0 AND detail = 0 AND last_air_ts = 0 AND release_known = 1 AND released_ts < ?`,
				)
				args = append(args, cutoff)
			}
		}
		query = `SELECT show_key FROM (` + strings.Join(branches, ` UNION ALL `) + `) u GROUP BY show_key`
		if q.HasHi {
			query += ` HAVING MAX(ts) <= ?`
			args = append(args, effectiveHi)
		}
	} else {
		var candBranches []string
		if q.HasLo {
			candBranches = append(candBranches,
				`SELECT show_key, season FROM catalog_titles WHERE type = 'season' AND release_known = 1 AND adult = 0 AND season > 0 AND child_count > 0 AND released_ts >= ? AND released_ts <= ?`,
				`SELECT e.show_key, e.season FROM catalog_titles e WHERE e.type = 'episode' AND e.release_known = 1 AND e.season > 0 AND e.episode = 1 AND e.released_ts >= ? AND e.released_ts <= ? AND NOT EXISTS (SELECT 1 FROM catalog_titles sn WHERE sn.type = 'season' AND sn.show_key = e.show_key AND sn.season = e.season AND sn.child_count > 0 AND sn.release_known = 1 AND sn.released_ts < ?)`,
			)
			args = append(args, q.Lo, effectiveHi, q.Lo, effectiveHi, cutoff)
		} else {
			candBranches = append(candBranches,
				`SELECT show_key, season FROM catalog_titles WHERE type = 'season' AND release_known = 1 AND adult = 0 AND season > 0 AND child_count > 0 AND released_ts <= ?`,
				`SELECT e.show_key, e.season FROM catalog_titles e WHERE e.type = 'episode' AND e.release_known = 1 AND e.season > 0 AND e.episode = 1 AND e.released_ts <= ? AND NOT EXISTS (SELECT 1 FROM catalog_titles sn WHERE sn.type = 'season' AND sn.show_key = e.show_key AND sn.season = e.season AND sn.child_count > 0 AND sn.release_known = 1 AND sn.released_ts < ?)`,
			)
			args = append(args, effectiveHi, effectiveHi, cutoff)
		}
		query = `SELECT DISTINCT cand.show_key FROM (` + strings.Join(candBranches, ` UNION ALL `) + `) cand
			WHERE NOT EXISTS (
				SELECT 1 FROM catalog_titles s2
				WHERE s2.type = 'season' AND s2.show_key = cand.show_key AND s2.season > cand.season
					AND s2.child_count > 0 AND s2.release_known = 1 AND s2.released_ts < ?
			)
			AND NOT EXISTS (
				SELECT 1 FROM catalog_titles e2
				WHERE e2.type = 'episode' AND e2.show_key = cand.show_key AND e2.season > cand.season
					AND e2.episode > 0 AND e2.release_known = 1 AND e2.released_ts < ?
			)`
		args = append(args, cutoff, cutoff)
		if !q.HasHi || q.Hi >= todayStart {
			if q.HasLo {
				query += ` UNION SELECT s.key AS show_key FROM catalog_titles s
					WHERE s.type = 'show' AND s.adult = 0 AND s.detail = 0 AND s.release_known = 1
						AND s.released_ts >= ? AND s.released_ts < ?
						AND NOT EXISTS (SELECT 1 FROM catalog_titles sn WHERE sn.type = 'season' AND sn.show_key = s.key)
						AND NOT EXISTS (SELECT 1 FROM catalog_titles ep WHERE ep.type = 'episode' AND ep.show_key = s.key AND ep.release_known = 1)`
				args = append(args, q.Lo, cutoff)
			} else {
				query += ` UNION SELECT s.key AS show_key FROM catalog_titles s
					WHERE s.type = 'show' AND s.adult = 0 AND s.detail = 0 AND s.release_known = 1
						AND s.released_ts < ?
						AND NOT EXISTS (SELECT 1 FROM catalog_titles sn WHERE sn.type = 'season' AND sn.show_key = s.key)
						AND NOT EXISTS (SELECT 1 FROM catalog_titles ep WHERE ep.type = 'episode' AND ep.show_key = s.key AND ep.release_known = 1)`
				args = append(args, cutoff)
			}
		}
	}

	rows, err := m.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[core.RatingKey]bool)
	for rows.Next() {
		var key core.RatingKey
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		out[key] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// ShowsWithStoredAirDates reports which of keys already carry stored air-date
// data in catalog_titles (so external discover candidates only spend the TMDB
// hydration budget when the mirror is cold for them).
func (m *Mirror) ShowsWithStoredAirDates(ctx context.Context, keys []core.RatingKey, premiere bool) (map[core.RatingKey]bool, error) {
	out := make(map[core.RatingKey]bool)
	if len(keys) == 0 {
		return out, nil
	}
	for start := 0; start < len(keys); start += 300 {
		chunk := keys[start:min(start+300, len(keys))]
		ph := store.Placeholders(len(chunk))
		inArgs := make([]any, len(chunk))
		for i, k := range chunk {
			inArgs[i] = k
		}
		var branches []string
		var args []any
		if !premiere {
			branches = append(branches,
				`SELECT key AS show_key FROM catalog_titles WHERE key IN (`+ph+`) AND type = 'show' AND last_air_ts <> 0`,
				`SELECT DISTINCT show_key FROM catalog_titles WHERE show_key IN (`+ph+`) AND type = 'season' AND season > 0 AND child_count > 0 AND release_known = 1`,
				`SELECT DISTINCT show_key FROM catalog_titles WHERE show_key IN (`+ph+`) AND type = 'episode' AND season > 0 AND episode > 0 AND release_known = 1`,
			)
			args = append(args, inArgs...)
			args = append(args, inArgs...)
			args = append(args, inArgs...)
		} else {
			branches = append(branches,
				`SELECT DISTINCT show_key FROM catalog_titles WHERE show_key IN (`+ph+`) AND type = 'season' AND season > 0 AND child_count > 0 AND release_known = 1`,
				`SELECT DISTINCT show_key FROM catalog_titles WHERE show_key IN (`+ph+`) AND type = 'episode' AND season > 0 AND episode = 1 AND release_known = 1`,
			)
			args = append(args, inArgs...)
			args = append(args, inArgs...)
		}
		rows, err := m.db.QueryContext(ctx, strings.Join(branches, ` UNION `), args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var k core.RatingKey
			if err := rows.Scan(&k); err != nil {
				rows.Close()
				return nil, err
			}
			out[k] = true
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return out, nil
}

// ShowAirDates returns stored latest-episode or latest-season-premiere timestamps
// for keys from show last_air_ts, season summaries, and mirrored regular episodes.
func (m *Mirror) ShowAirDates(ctx context.Context, keys []core.RatingKey, now time.Time, premiere bool) (map[core.RatingKey]int64, error) {
	out := make(map[core.RatingKey]int64)
	if len(keys) == 0 {
		return out, nil
	}
	cutoff := time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day()+1, 0, 0, 0, 0, time.UTC).Unix()
	for start := 0; start < len(keys); start += 300 {
		chunk := keys[start:min(start+300, len(keys))]
		ph := store.Placeholders(len(chunk))
		inArgs := make([]any, len(chunk))
		for i, k := range chunk {
			inArgs[i] = k
		}
		var query string
		var args []any
		if !premiere {
			query = `SELECT show_key, MAX(ts) FROM (
				SELECT key AS show_key, last_air_ts AS ts FROM catalog_titles WHERE key IN (` + ph + `) AND type = 'show' AND adult = 0 AND last_air_ts <> 0 AND last_air_ts < ?
				UNION ALL
				SELECT show_key, MAX(released_ts) AS ts FROM catalog_titles WHERE show_key IN (` + ph + `) AND type = 'episode' AND release_known = 1 AND season > 0 AND episode > 0 AND released_ts < ? GROUP BY show_key
				UNION ALL
				SELECT show_key, MAX(released_ts) AS ts FROM catalog_titles WHERE show_key IN (` + ph + `) AND type = 'season' AND release_known = 1 AND adult = 0 AND season > 0 AND child_count > 0 AND released_ts < ? GROUP BY show_key
			) u GROUP BY show_key`
			args = append(args, inArgs...)
			args = append(args, cutoff)
			args = append(args, inArgs...)
			args = append(args, cutoff)
			args = append(args, inArgs...)
			args = append(args, cutoff)
		} else {
			query = `SELECT cand.show_key, MAX(cand.ts) FROM (
				SELECT show_key, season, released_ts AS ts FROM catalog_titles
				WHERE show_key IN (` + ph + `) AND type = 'season' AND release_known = 1 AND adult = 0 AND season > 0 AND child_count > 0 AND released_ts < ?
				UNION ALL
				SELECT e.show_key, e.season, e.released_ts AS ts FROM catalog_titles e
				WHERE e.show_key IN (` + ph + `) AND e.type = 'episode' AND e.release_known = 1 AND e.season > 0 AND e.episode = 1 AND e.released_ts < ?
					AND NOT EXISTS (SELECT 1 FROM catalog_titles sn WHERE sn.type = 'season' AND sn.show_key = e.show_key AND sn.season = e.season AND sn.child_count > 0 AND sn.release_known = 1 AND sn.released_ts < ?)
			) cand
			WHERE NOT EXISTS (
				SELECT 1 FROM catalog_titles s2
				WHERE s2.type = 'season' AND s2.show_key = cand.show_key AND s2.season > cand.season
					AND s2.child_count > 0 AND s2.release_known = 1 AND s2.released_ts < ?
			)
			AND NOT EXISTS (
				SELECT 1 FROM catalog_titles e2
				WHERE e2.type = 'episode' AND e2.show_key = cand.show_key AND e2.season > cand.season
					AND e2.episode > 0 AND e2.release_known = 1 AND e2.released_ts < ?
			)
			GROUP BY cand.show_key`
			args = append(args, inArgs...)
			args = append(args, cutoff)
			args = append(args, inArgs...)
			args = append(args, cutoff, cutoff, cutoff, cutoff)
		}
		rows, err := m.db.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var k core.RatingKey
			var ts int64
			if err := rows.Scan(&k, &ts); err != nil {
				rows.Close()
				return nil, err
			}
			out[k] = ts
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return out, nil
}
