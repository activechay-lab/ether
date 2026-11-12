package plexwatch

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
)

// ratingKey kind base, mirroring core: kind = rating_key / 10^15.
const pmsKeyKindBase = 1_000_000_000_000_000

// airedShowCatalog reads a show's aired episode list from the local catalogue
// mirror with indexed queries only, never the network.
type airedShowCatalog interface {
	AiredShowEpisodes(ctx context.Context, shows []core.RatingKey) (map[core.RatingKey][]core.RatingKey, error)
}

// stepAsideStatus reports one server's step-aside state using indexed counts
// over plexwatch_pms_held and the per-server sync columns, never a scan of the
// catalogue and never a network call.
//
// State is derived from the sync columns the step-aside pass already writes:
//   - failed   — the last pass left a problem recorded for this server.
//   - matching — the last pass matched some titles and a short catch-up run is
//     already scheduled (a large library still converging).
//   - complete — a pass has completed and nothing is outstanding.
//   - never    — no pass has run yet.
func (s *Servers) stepAsideStatus(ctx context.Context, serverID int64) (PMSStepAsideStatus, error) {
	var st PMSStepAsideStatus
	var syncedAt, nextRun, nextAllowed int64
	var failures int
	var problem string
	if err := s.db.QueryRowContext(ctx, `SELECT held_synced_at,held_next_run,held_next_allowed,held_failures,problem
		FROM plexwatch_pms WHERE id=?`, serverID).
		Scan(&syncedAt, &nextRun, &nextAllowed, &failures, &problem); err != nil {
		return st, err
	}
	st.LastSynced = syncedAt
	st.LastScan = syncedAt
	st.Problem = problem
	st.State = stepAsideStateFrom(syncedAt, nextRun, nextAllowed, failures, problem, s.now().UnixNano())

	// Indexed counts by rating-key kind: movies, episodes, and the distinct
	// shows the server holds episodes of.
	var movies, episodes, shows int64
	if err := s.db.QueryRowContext(ctx, `SELECT
		COALESCE(SUM(CASE WHEN rating_key/?=1 THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN rating_key/?=4 THEN 1 ELSE 0 END),0),
		COUNT(DISTINCT CASE WHEN rating_key/?=4 THEN show_key END)
		FROM plexwatch_pms_held WHERE server_id=?`,
		pmsKeyKindBase, pmsKeyKindBase, pmsKeyKindBase, serverID).
		Scan(&movies, &episodes, &shows); err != nil {
		return st, err
	}
	st.Movies = int(movies)
	st.Episodes = int(episodes)

	// Count unmatched titles recorded for this server, the same indexed count
	// the per-account rows use.
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM plexwatch_pms_unmatched WHERE server_id=?`, serverID).Scan(&st.Unmatched)

	// Split held shows into fully held (every aired regular episode is on the
	// server) and partial, bounded to the shows this server holds.
	if shows > 0 {
		full, partial, err := s.classifyHeldShows(ctx, serverID)
		if err != nil {
			return st, err
		}
		st.ShowsFull = full
		st.ShowsPartial = partial
	}
	return st, nil
}

// stepAsideStateFrom derives the console state from the sync columns. nextRun
// is in UnixNano; a catch-up run scheduled within a minute of now means the
// last pass capped and is still converging.
func stepAsideStateFrom(syncedAt, nextRun, nextAllowed int64, failures int, problem string, nowNS int64) string {
	switch {
	case problem != "" || failures > 0:
		return StepAsideStateFailed
	case syncedAt == 0:
		return StepAsideStateNever
	case nextRun > 0 && nextRun-nowNS <= int64(90*1e9) && nextAllowed <= nowNS:
		return StepAsideStateMatching
	default:
		return StepAsideStateComplete
	}
}

// classifyHeldShows counts fully held shows (every aired regular episode on the
// server) versus partial ones, using only the shows the server holds episodes
// of. The aired list comes from the indexed catalogue mirror, never the network.
func (s *Servers) classifyHeldShows(ctx context.Context, serverID int64) (full, partial int, err error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT show_key FROM plexwatch_pms_held
		WHERE server_id=? AND rating_key/?=4 AND show_key<>0`, serverID, pmsKeyKindBase)
	if err != nil {
		return 0, 0, err
	}
	var shows []core.RatingKey
	for rows.Next() {
		var sk int64
		if err := rows.Scan(&sk); err != nil {
			rows.Close()
			return 0, 0, err
		}
		shows = append(shows, core.RatingKey(sk))
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}
	ac, ok := s.catalog.(airedShowCatalog)
	if !ok || len(shows) == 0 {
		return 0, len(shows), nil
	}
	aired, err := ac.AiredShowEpisodes(ctx, shows)
	if err != nil {
		return 0, 0, err
	}
	heldByShow, err := s.heldEpisodesByShow(ctx, serverID)
	if err != nil {
		return 0, 0, err
	}
	for _, sk := range shows {
		total := 0
		for _, ep := range aired[sk] {
			if ep.Episode() > 0 && ep.Season() > 0 {
				total++
			}
		}
		if total == 0 {
			// No local aired episode list yet: cannot judge, count as partial.
			partial++
			continue
		}
		held := heldByShow[sk]
		regHeld := 0
		for _, ep := range aired[sk] {
			if ep.Episode() <= 0 || ep.Season() <= 0 {
				continue
			}
			if _, ok := held[ep]; ok {
				regHeld++
			}
		}
		if regHeld >= total {
			full++
		} else {
			partial++
		}
	}
	return full, partial, nil
}

// heldEpisodesByShow returns the set of held episode keys per show for a server.
func (s *Servers) heldEpisodesByShow(ctx context.Context, serverID int64) (map[core.RatingKey]map[core.RatingKey]struct{}, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT rating_key FROM plexwatch_pms_held
		WHERE server_id=? AND rating_key/?=4`, serverID, pmsKeyKindBase)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[core.RatingKey]map[core.RatingKey]struct{}{}
	for rows.Next() {
		var rk int64
		if err := rows.Scan(&rk); err != nil {
			return nil, err
		}
		key := core.RatingKey(rk)
		sk := core.ShowKey(key.TMDB())
		set := out[sk]
		if set == nil {
			set = map[core.RatingKey]struct{}{}
			out[sk] = set
		}
		set[key] = struct{}{}
	}
	return out, rows.Err()
}
