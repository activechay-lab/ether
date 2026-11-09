package collections

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/tmdb"
)

// Each evaluation performs at most this many show-detail requests. Cached
// results are shared by previews and builds; oldest entries are refreshed
// first, so repeated builds make progress through larger catalogues.
const airFetchBudget = 100
const airFreshness = 24 * time.Hour

type airRecord struct {
	episode, premiere, fetched, attempted int64
	failed                                bool
}

func (s *Service) hydrateAirDates(ctx context.Context, cs []*cand, mode string, now time.Time, online bool) (int, int, error) {
	select {
	case s.airGate <- struct{}{}:
	case <-ctx.Done():
		return 0, 0, ctx.Err()
	}
	defer func() { <-s.airGate }()
	if err := ctx.Err(); err != nil {
		return 0, 0, err
	}
	if err := s.loadAirDates(ctx, cs, mode, now); err != nil {
		return 0, 0, err
	}
	var missingStored []core.RatingKey
	for _, c := range cs {
		if c.e.typ == core.TypeShow && c.airDate == 0 {
			missingStored = append(missingStored, c.e.key)
		}
	}
	if len(missingStored) > 0 {
		stored, err := catalog.NewMirror(s.db, nil).ShowAirDates(ctx, missingStored, now, mode == "latestSeasonPremiere")
		if err != nil {
			return 0, 0, err
		}
		for _, c := range cs {
			if c.e.typ == core.TypeShow && c.airDate == 0 {
				c.airDate = stored[c.e.key]
			}
		}
	}
	records := map[core.RatingKey]airRecord{}
	keys := make([]any, 0, len(cs))
	for _, c := range cs {
		if c.e.typ == core.TypeShow {
			keys = append(keys, c.e.key)
		}
	}
	for start := 0; start < len(keys); start += 300 {
		chunk := keys[start:min(start+300, len(keys))]
		placeholders := store.Placeholders(len(chunk))
		rows, err := s.db.QueryContext(ctx, `SELECT show_key,episode_ts,premiere_ts,fetched_at,attempted_at,failed FROM collections_air_dates WHERE show_key IN (`+placeholders+`)`, chunk...)
		if err != nil {
			return 0, 0, err
		}
		for rows.Next() {
			var k core.RatingKey
			var r airRecord
			if err = rows.Scan(&k, &r.episode, &r.premiere, &r.fetched, &r.attempted, &r.failed); err != nil {
				rows.Close()
				return 0, 0, err
			}
			records[k] = r
		}
		if err = rows.Err(); err != nil {
			rows.Close()
			return 0, 0, err
		}
		rows.Close()
	}

	var pending []*cand
	if online && s.api != nil && s.api.c != nil {
		for _, c := range cs {
			r := records[c.e.key]
			retry := int64(airFreshness.Seconds())
			if r.failed {
				retry = int64(time.Hour.Seconds())
			}
			if c.e.typ == core.TypeShow && (r.attempted == 0 || now.Unix()-r.attempted >= retry) {
				pending = append(pending, c)
			}
		}
	}
	sort.SliceStable(pending, func(i, j int) bool { return records[pending[i].e.key].attempted < records[pending[j].e.key].attempted })
	if len(pending) > airFetchBudget {
		pending = pending[:airFetchBudget]
	}
	type result struct {
		key core.RatingKey
		r   airRecord
		err error
	}
	results := make(chan result, len(pending))
	sem := make(chan struct{}, 6)
	var wg sync.WaitGroup
	for _, c := range pending {
		wg.Add(1)
		go func(k core.RatingKey) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()
			r, err := s.fetchAirRecord(ctx, k, now)
			results <- result{k, r, err}
		}(c.e.key)
	}
	wg.Wait()
	close(results)
	for x := range results {
		if x.err != nil {
			x.r = records[x.key]
			x.r.attempted = now.Unix()
			x.r.failed = true
		}
		if _, err := s.db.ExecContext(ctx, `INSERT INTO collections_air_dates(show_key,episode_ts,premiere_ts,fetched_at,attempted_at,failed) VALUES(?,?,?,?,?,?) ON CONFLICT(show_key) DO UPDATE SET episode_ts=excluded.episode_ts,premiere_ts=excluded.premiere_ts,fetched_at=excluded.fetched_at,attempted_at=excluded.attempted_at,failed=excluded.failed`, x.key, x.r.episode, x.r.premiere, x.r.fetched, x.r.attempted, x.r.failed); err != nil {
			return 0, 0, err
		}
		records[x.key] = x.r
	}
	if err := ctx.Err(); err != nil {
		return 0, 0, err
	}
	unknown, stale := 0, 0
	for _, c := range cs {
		if c.e.typ != core.TypeShow {
			continue
		}
		r := records[c.e.key]
		d := r.episode
		if mode == "latestSeasonPremiere" {
			d = r.premiere
		}
		// A successful show-details response describes the current show;
		// a partial mirror cannot establish a more recent season or episode.
		if r.fetched != 0 {
			c.airDate = d
		}
		if c.airDate == 0 {
			unknown++
		} else if r.fetched != 0 && (r.failed || now.Unix()-r.fetched >= int64(airFreshness.Seconds())) {
			stale++
		}
	}
	return unknown, stale, nil
}

// evalAirRules precomputes SQL match sets for latestEpisode and
// latestSeasonPremiere rules and hydrates cold external candidates (such as
// discover-backed sources on an unwarmed mirror) within airFetchBudget.
func (s *Service) evalAirRules(ctx context.Context, sp *core.CollectionSpec, cands []*cand, externalKeys map[core.RatingKey]bool, rules, excludes []*rule, ec *evalCtx) error {
	var airRules []*rule
	needEpisode, needPremiere := false, false
	for _, r := range rules {
		if r.airDateRule() {
			airRules = append(airRules, r)
			if r.field == "latestSeasonPremiere" {
				needPremiere = true
			} else {
				needEpisode = true
			}
		}
	}
	for _, r := range excludes {
		if r.airDateRule() {
			airRules = append(airRules, r)
			if r.field == "latestSeasonPremiere" {
				needPremiere = true
			} else {
				needEpisode = true
			}
		}
	}
	if len(airRules) == 0 {
		return nil
	}
	ec.airMatches = make(map[*rule]map[core.RatingKey]bool, len(airRules))
	mirror := catalog.NewMirror(s.db, nil)

	if sp.LocalOnly {
		for _, r := range airRules {
			matches, err := mirror.MatchingAirDateShows(ctx, r.airDateQuery(ec.now))
			if err != nil {
				return err
			}
			ec.airMatches[r] = matches
		}
		return nil
	}

	if s.api != nil && s.api.c != nil && len(externalKeys) > 0 {
		nowUnix := ec.now.Unix()
		var extShows []*cand
		var extKeys []core.RatingKey
		for _, c := range cands {
			if c.e.typ != core.TypeShow || !externalKeys[c.e.key] {
				continue
			}
			if c.e.released == 0 || (sp.ReleaseMode == "upcoming" && c.e.released <= nowUnix) ||
				(c.e.released > nowUnix && (sp.ReleaseMode != "upcoming" && sp.ReleaseMode != "mixed" || c.e.released > ec.now.AddDate(0, 0, sp.LookaheadDays).Unix())) {
				continue
			}
			if sp.Match == MatchAll {
				ok := true
				for _, nr := range rules {
					if !nr.airDateRule() && !c.assured[nr] && !nr.match(c.e, ec) {
						ok = false
						break
					}
				}
				if !ok {
					continue
				}
			}
			excluded := false
			for _, nx := range excludes {
				if !nx.airDateRule() && nx.match(c.e, ec) {
					excluded = true
					break
				}
			}
			if excluded {
				continue
			}
			extShows = append(extShows, c)
			extKeys = append(extKeys, c.e.key)
		}
		if len(extShows) > 0 {
			stored, err := mirror.ShowsWithStoredAirDates(ctx, extKeys, needPremiere)
			if err != nil {
				return err
			}
			var cold []*cand
			for _, c := range extShows {
				if !stored[c.e.key] {
					cold = append(cold, c)
				}
			}
			if len(cold) > 0 {
				mode := "latestEpisode"
				if needPremiere {
					mode = "latestSeasonPremiere"
				}
				if _, _, err := s.hydrateAirDates(ctx, cold, mode, ec.now, true); err != nil {
					return err
				}
			}
		}
	}

	type cachedAir struct {
		key                        core.RatingKey
		episode, premiere, fetched int64
	}
	var cached []cachedAir
	var cachedKeys []core.RatingKey
	freshWindow := int64(airFreshness.Seconds())
	nowUnix := ec.now.Unix()
	rows, err := s.db.QueryContext(ctx, `SELECT show_key,episode_ts,premiere_ts,fetched_at FROM collections_air_dates WHERE fetched_at <> 0`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var row cachedAir
		if err := rows.Scan(&row.key, &row.episode, &row.premiere, &row.fetched); err != nil {
			rows.Close()
			return err
		}
		cached = append(cached, row)
		cachedKeys = append(cachedKeys, row.key)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	var storedEp, storedPrem map[core.RatingKey]int64
	if len(cachedKeys) > 0 {
		if needEpisode {
			if storedEp, err = mirror.ShowAirDates(ctx, cachedKeys, ec.now, false); err != nil {
				return err
			}
		}
		if needPremiere {
			if storedPrem, err = mirror.ShowAirDates(ctx, cachedKeys, ec.now, true); err != nil {
				return err
			}
		}
	}

	for _, r := range airRules {
		q := r.airDateQuery(ec.now)
		matches, err := mirror.MatchingAirDateShows(ctx, q)
		if err != nil {
			return err
		}
		for _, row := range cached {
			ts := row.episode
			mirrorTs := storedEp[row.key]
			if q.Premiere {
				ts = row.premiere
				mirrorTs = storedPrem[row.key]
			}
			if mirrorTs == 0 {
				// Mirror has no stored episode/season air date for this show:
				// collections_air_dates is authoritative when fresh, and a
				// stale positive date still supplements a cold mirror.
				if q.MatchTimestamp(ts) {
					matches[row.key] = true
				} else if nowUnix-row.fetched < freshWindow {
					delete(matches, row.key)
				}
			} else if nowUnix-row.fetched < freshWindow && ts >= mirrorTs {
				// Both exist and collections_air_dates is at least as current.
				if q.MatchTimestamp(ts) {
					matches[row.key] = true
				} else {
					delete(matches, row.key)
				}
			}
		}
		ec.airMatches[r] = matches
	}
	return nil
}

func (s *Service) fetchAirRecord(ctx context.Context, k core.RatingKey, now time.Time) (airRecord, error) {
	var d tmdb.TVDetails
	if err := s.api.get(ctx, fmt.Sprintf("/tv/%d", k.TMDB()), nil, &d); err != nil {
		return airRecord{}, err
	}
	cutoff := time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day()+1, 0, 0, 0, 0, time.UTC)
	aired := func(date string) int64 {
		t := tmdb.ParseDate(date)
		if t.IsZero() || !t.Before(cutoff) {
			return 0
		}
		return t.Unix()
	}
	r := airRecord{fetched: now.Unix(), attempted: now.Unix()}
	if ep := d.LastEpisodeToAir; ep != nil && ep.SeasonNumber > 0 && ep.EpisodeNumber > 0 {
		r.episode = aired(ep.AirDate)
	}
	latest := 0
	for _, season := range d.Seasons {
		if season.SeasonNumber > latest && season.EpisodeCount > 0 {
			if date := aired(season.AirDate); date != 0 {
				latest = season.SeasonNumber
				r.premiere = date
			}
		}
	}
	// TMDB's last episode can be a special. Read the most recent aired
	// regular season in that case, rather than using the special's date.
	// This costs at most one additional request per selected show.
	if d.LastEpisodeToAir != nil && d.LastEpisodeToAir.SeasonNumber == 0 && latest > 0 {
		var season tmdb.SeasonDetails
		if err := s.api.get(ctx, fmt.Sprintf("/tv/%d/season/%d", k.TMDB(), latest), nil, &season); err != nil {
			return airRecord{}, err
		}
		for _, ep := range season.Episodes {
			if ep.EpisodeNumber > 0 {
				r.episode = max(r.episode, aired(ep.AirDate))
			}
		}
	}
	// A later known aired season without a dated summary must remain unknown,
	// rather than borrowing the premiere of an older season.
	if ep := d.LastEpisodeToAir; ep != nil && ep.SeasonNumber > latest && ep.EpisodeNumber > 0 && aired(ep.AirDate) != 0 {
		r.premiere = 0
	}
	return r, nil
}
