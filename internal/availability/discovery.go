package availability

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

// EvidenceWindow is a proof window, not a delay: an eligible release unlocks a
// title immediately. Older catalogue entries do not need a scan.
func EvidenceWindow(t core.Title, now time.Time) bool {
	return evidenceWindow(t, now, config.Availability{})
}

func evidenceWindow(t core.Title, now time.Time, cfg config.Availability) bool {
	if t.Released.IsZero() || t.Key.Kind() == core.KindLocal {
		return false
	}
	age := now.Sub(t.Released)
	switch t.Type {
	case core.TypeMovie:
		switch cfg.ModeFor(hiddenMovies) {
		case config.ModeImmediate:
			return false
		case config.ModeDigital:
			adm := catalogHomeReleaseAdmission(t.HomeReleased, cfg.HomeReleaseOffsetDays)
			return adm.IsZero() || now.Sub(adm) < time.Duration(cfg.EffectiveMovieEvidenceDays())*24*time.Hour
		default:
			return !cfg.DisableMovieEvidenceGate && age < time.Duration(cfg.EffectiveMovieEvidenceDays())*24*time.Hour
		}
	case core.TypeEpisode, core.TypeShow:
		if cfg.ModeFor(hiddenShows) != config.ModePlayable {
			return false
		}
		return age < episodeEvidence
	}
	return false
}

// episodeEvidence is how long after release an episode or show needs
// release evidence to be discovered.
const episodeEvidence = 24 * time.Hour

// EpisodesSettledBefore is the release time at or before which
// FilterDiscovery keeps an episode without looking for evidence, at the
// clock the request's later FilterDiscovery calls use.
func (s *Service) EpisodesSettledBefore(ctx context.Context) time.Time {
	snap := s.snapshotFor(ctx)
	if snap.availability.ModeFor(hiddenShows) != config.ModePlayable {
		return snap.now
	}
	return snap.now.Add(-episodeEvidence)
}

// filterReleaseDiscovery checks sticky availability_gate state (plus digital
// dates when configured) for in-window movies and episodes, matching any
// newly arrived retained feed items on ungated titles without rescanning
// already-opened titles.
func (s *Service) filterReleaseDiscovery(ctx context.Context, items []core.Title, input discoverySnapshot) []core.Title {
	titles := map[core.RatingKey]*core.Title{}
	var keys []core.RatingKey
	for i := range items {
		t := &items[i]
		if !input.upcomingMovie(*t) && evidenceWindow(*t, input.now, input.availability) && titles[t.Key] == nil {
			keys = append(keys, t.Key)
			titles[t.Key] = t
		}
	}
	if len(keys) == 0 {
		return items
	}
	if !s.hasGateTable {
		return s.filterLegacyReleaseEvidence(ctx, items, titles, keys, input)
	}

	s.ensureGatePolicySeeded(ctx, input)

	movieMode := input.availability.ModeFor(hiddenMovies)
	acceptable := map[core.RatingKey]bool{}
	var digitalMovies []core.RatingKey
	var playableKeys []core.RatingKey
	for _, k := range keys {
		t := titles[k]
		if t.Type == core.TypeMovie && movieMode == config.ModeDigital {
			digitalMovies = append(digitalMovies, k)
			continue
		}
		playableKeys = append(playableKeys, k)
	}

	if len(digitalMovies) > 0 && input.availability.DigitalDateEvidence {
		s.acceptDigitalReleases(ctx, digitalMovies, input.now, input.availability.HomeReleaseOffsetDays, acceptable)
	}

	if len(playableKeys) > 0 {
		for k := range s.openGateKeys(ctx, input.gatePolicy, playableKeys) {
			acceptable[k] = true
		}
		if input.availability.DigitalDateEvidence {
			var moviePlayable []core.RatingKey
			for _, k := range playableKeys {
				if !acceptable[k] && titles[k].Type == core.TypeMovie {
					moviePlayable = append(moviePlayable, k)
				}
			}
			if len(moviePlayable) > 0 {
				s.acceptDigitalReleases(ctx, moviePlayable, input.now, input.availability.HomeReleaseOffsetDays, acceptable)
			}
		}
		ungated := map[core.RatingKey]*core.Title{}
		for _, k := range playableKeys {
			if !acceptable[k] {
				ungated[k] = titles[k]
			}
		}
		if len(ungated) > 0 {
			feeds := make(map[string]core.ReleaseFeed, len(input.feeds))
			for _, f := range input.feeds {
				feeds[f.ID] = f
			}
			s.matchRetainedAndOpenGate(ctx, ungated, feeds, input, acceptable)
		}
	}

	out := make([]core.Title, 0, len(items))
	for _, t := range items {
		if titles[t.Key] == nil || (!t.IsFutureRelease(input.now) && acceptable[t.Key]) {
			out = append(out, t)
		}
	}
	return out
}

// filterLegacyReleaseEvidence supports read-only databases opened before the
// availability_gate migration without issuing writes.
func (s *Service) filterLegacyReleaseEvidence(ctx context.Context, items []core.Title, titles map[core.RatingKey]*core.Title, keys []core.RatingKey, input discoverySnapshot) []core.Title {
	feeds := map[string]core.ReleaseFeed{}
	for _, f := range input.feeds {
		feeds[f.ID] = f
	}
	acceptable := map[core.RatingKey]bool{}
	if input.availability.DigitalDateEvidence || input.availability.ModeFor(hiddenMovies) == config.ModeDigital {
		s.acceptDigitalReleases(ctx, keys, input.now, input.availability.HomeReleaseOffsetDays, acceptable)
	}
	passed := s.officialDatesPassed(ctx, titles, input.now)
	var pairs []feedPair
	for start := 0; start < len(keys); start += 500 {
		batch := keys[start:min(start+500, len(keys))]
		marks := make([]string, len(batch))
		args := make([]any, 0, len(batch)+1)
		for i, k := range batch {
			marks[i] = "?"
			args = append(args, k)
		}
		args = append(args, input.retentionCutoff().Unix())
		rows, err := s.db.QueryContext(ctx, `SELECT rating_key,source_id,generation,payload,state,revision FROM availability_releases WHERE rating_key IN (`+strings.Join(marks, ",")+`) AND last_seen>=?`, args...)
		if err != nil {
			continue
		}
		for rows.Next() {
			var key core.RatingKey
			var src, gen, payload, state string
			var rev int64
			if rows.Scan(&key, &src, &gen, &payload, &state, &rev) != nil {
				continue
			}
			if acceptable[key] || state == "unavailable" || (state == "unknown" && rev > 0) {
				continue
			}
			if state != "verified" && !passed[key] {
				continue
			}
			f, ok := feeds[src]
			if !ok || f.Generation != gen || !input.usable(f.Protocol) {
				continue
			}
			r, err := s.decode(payload)
			if err != nil {
				continue
			}
			r.Info = releaseparse.Parse(r.Name)
			pairs = append(pairs, feedPair{t: titles[key], r: r})
		}
		rows.Close()
	}
	for i, ok := range s.judge(ctx, pairs, true) {
		if ok {
			acceptable[pairs[i].t.Key] = true
		}
	}
	out := make([]core.Title, 0, len(items))
	for _, t := range items {
		if titles[t.Key] == nil || (!t.IsFutureRelease(input.now) && acceptable[t.Key]) {
			out = append(out, t)
		}
	}
	return out
}

// HiddenMovies reports currently gated recent movies for the owner console.
// Viewer browse/rows/search evaluate availability_gate directly in SQL.
func (s *Service) HiddenMovies(ctx context.Context) []core.RatingKey {
	input := s.snapshotFor(ctx)
	if input.availability.DisableMovieEvidenceGate || input.availability.ModeFor(hiddenMovies) == config.ModeImmediate {
		return nil
	}
	return s.hidden(ctx, hiddenMovies, input)
}

// DigitalDiscovery reports the discovery policy for the current request.
func (s *Service) DigitalDiscovery(ctx context.Context) (bool, bool) {
	a := s.snapshotFor(ctx).availability
	return a.ModeFor(hiddenMovies) == config.ModeDigital, a.DigitalDateEvidence
}

// HiddenShows reports currently gated first-day series.
func (s *Service) HiddenShows(ctx context.Context) []core.RatingKey {
	return s.hidden(ctx, hiddenShows, s.snapshotFor(ctx))
}

// HiddenEpisodes reports currently gated first-day episodes.
func (s *Service) HiddenEpisodes(ctx context.Context) []core.RatingKey {
	return s.hidden(ctx, hiddenEpisodes, s.snapshotFor(ctx))
}

const (
	hiddenMovies   = "movie"
	hiddenShows    = "show"
	hiddenEpisodes = "episode"
)

func (s *Service) recent(ctx context.Context, kind string, input discoverySnapshot) ([]core.Title, bool, error) {
	switch kind {
	case hiddenMovies:
		if input.availability.ModeFor(hiddenMovies) == config.ModeDigital {
			if c, ok := s.d.Catalog.(interface {
				DigitalEvidenceMovies(context.Context, time.Time) ([]core.Title, error)
			}); ok {
				since := input.now.Add(-time.Duration(input.availability.EffectiveMovieEvidenceDays()) * 24 * time.Hour)
				since = since.Add(-time.Duration(input.availability.HomeReleaseOffsetDays) * 24 * time.Hour)
				ts, err := c.DigitalEvidenceMovies(ctx, since)
				return ts, true, err
			}
		}
		c, ok := s.d.Catalog.(interface {
			RecentMovies(context.Context, time.Time) ([]core.Title, error)
		})
		if !ok {
			return nil, false, nil
		}
		ts, err := c.RecentMovies(ctx, input.now.Add(-time.Duration(input.availability.EffectiveMovieEvidenceDays())*24*time.Hour))
		return ts, true, err
	case hiddenEpisodes:
		c, ok := s.d.Catalog.(interface {
			RecentEvidenceEpisodes(context.Context, time.Time) ([]core.Title, error)
		})
		if !ok {
			return nil, false, nil
		}
		ts, err := c.RecentEvidenceEpisodes(ctx, input.now.Add(-24*time.Hour))
		return ts, true, err
	default:
		c, ok := s.d.Catalog.(interface {
			RecentShows(context.Context, time.Time) ([]core.Title, error)
		})
		if !ok {
			return nil, false, nil
		}
		ts, err := c.RecentShows(ctx, input.now.Add(-24*time.Hour))
		return ts, true, err
	}
}

func hiddenKey(kind string, input discoverySnapshot) string {
	return fmt.Sprintf("%s:%s:%+v:%+v:%t", kind, input.policy, input.availability, input.feeds, input.showUpcomingMovies)
}

func (s *Service) hidden(ctx context.Context, kind string, input discoverySnapshot) []core.RatingKey {
	items, ok, err := s.recent(ctx, kind, input)
	if err != nil || !ok || len(items) == 0 {
		return nil
	}
	visible := map[core.RatingKey]bool{}
	for _, t := range s.filterDiscovery(ctx, items, input) {
		visible[t.Key] = true
	}
	seen := map[core.RatingKey]bool{}
	var out []core.RatingKey
	for _, t := range items {
		if !visible[t.Key] && !input.upcomingMovie(t) && evidenceWindow(t, input.now, input.availability) && !seen[t.Key] {
			seen[t.Key] = true
			out = append(out, t.Key)
		}
	}
	return out
}
