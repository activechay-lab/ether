package markers

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.Register("markers", 2, `CREATE TABLE markers_plugin_cache(rating_key INTEGER NOT NULL,runtime_ms INTEGER NOT NULL,source TEXT NOT NULL,ranges TEXT NOT NULL,expires_at INTEGER NOT NULL,PRIMARY KEY(rating_key,runtime_ms,source));`)
}

// PutPlugin stores a background result separately for each source.
func (s *Service) PutPlugin(ctx context.Context, key core.RatingKey, d time.Duration, source string, ranges []Marker) error {
	if s.o.DB == nil || source == "" {
		return errors.New("marker cache requires database and source")
	}
	for i := range ranges {
		if !valid(ranges[i], d) {
			return errors.New("invalid marker range")
		}
		ranges[i].Source = source
	}
	raw, err := json.Marshal(ranges)
	if err != nil {
		return err
	}
	_, err = s.o.DB.ExecContext(ctx, `INSERT INTO markers_plugin_cache VALUES(?,?,?,?,?) ON CONFLICT(rating_key,runtime_ms,source) DO UPDATE SET ranges=excluded.ranges,expires_at=excluded.expires_at`, int64(key), d.Milliseconds(), source, string(raw), s.o.Now().Add(s.o.TTL).Unix())
	return err
}

func (s *Service) mergeSources(ctx context.Context, key core.RatingKey, d time.Duration, chapters, introdb []Marker) []Marker {
	var order []string
	if s.o.Config != nil {
		order = s.o.Config().SourceOrder
	}
	skipdb := s.skipDBEnabled()
	if len(order) == 0 {
		if !skipdb {
			return merge(chapters, introdb)
		}
		order = []string{"chapter", "theintrodb", SkipDBSource}
	} else if skipdb {
		hasSkipDB := false
		for _, source := range order {
			if source == SkipDBSource {
				hasSkipDB = true
				break
			}
		}
		if !hasSkipDB {
			order = append(order, SkipDBSource)
		}
	}
	var out []Marker
	seen := map[string]bool{}
	for _, source := range order {
		if seen[source] {
			continue
		}
		seen[source] = true
		if source == SkipDBSource && !skipdb {
			continue
		}
		switch source {
		case "chapter":
			out = merge(out, chapters)
		case "theintrodb":
			out = merge(out, introdb)
		default:
			if s.o.DB == nil {
				continue
			}
			var raw string
			err := s.o.DB.QueryRowContext(ctx, `SELECT ranges FROM markers_plugin_cache WHERE rating_key=? AND runtime_ms=? AND source=? AND expires_at>?`, int64(key), d.Milliseconds(), source, s.o.Now().Unix()).Scan(&raw)
			if err == nil {
				var rows []Marker
				if json.Unmarshal([]byte(raw), &rows) == nil {
					out = merge(out, rows)
				}
			}
		}
	}
	return out
}

// DeletePlugin removes cached results when a plugin is disabled or removed.
func (s *Service) DeletePlugin(ctx context.Context, source string) error {
	if s.o.DB == nil {
		return nil
	}
	_, err := s.o.DB.ExecContext(ctx, `DELETE FROM markers_plugin_cache WHERE source=?`, source)
	return err
}

func init() {
	store.Register("markers", 3, `CREATE TABLE markers_refresh_queue(rating_key INTEGER NOT NULL,runtime_ms INTEGER NOT NULL,last_played INTEGER NOT NULL,PRIMARY KEY(rating_key,runtime_ms));`)
}

type RefreshTarget struct {
	Key        core.RatingKey
	Duration   time.Duration
	LastPlayed time.Time
}

// RememberRefreshTarget persists the bounded set of recently played and upcoming titles.
func (s *Service) RememberRefreshTarget(ctx context.Context, key core.RatingKey, duration time.Duration) error {
	if s.o.DB == nil {
		return nil
	}
	now := s.o.Now().Unix()
	_, err := s.o.DB.ExecContext(ctx, `INSERT INTO markers_refresh_queue VALUES(?,?,?) ON CONFLICT(rating_key,runtime_ms) DO UPDATE SET last_played=excluded.last_played`, int64(key), duration.Milliseconds(), now)
	return err
}
func (s *Service) RefreshTargets(ctx context.Context) ([]RefreshTarget, error) {
	if s.o.DB == nil {
		return nil, nil
	}
	cutoff := s.o.Now().Add(-30 * 24 * time.Hour).Unix()
	_, err := s.o.DB.ExecContext(ctx, `DELETE FROM markers_refresh_queue WHERE last_played<?`, cutoff)
	if err != nil {
		return nil, err
	}
	rows, err := s.o.DB.QueryContext(ctx, `SELECT rating_key,runtime_ms,last_played FROM markers_refresh_queue ORDER BY last_played DESC LIMIT 500`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RefreshTarget
	for rows.Next() {
		var key, ms, at int64
		if err := rows.Scan(&key, &ms, &at); err != nil {
			return nil, err
		}
		out = append(out, RefreshTarget{core.RatingKey(key), time.Duration(ms) * time.Millisecond, time.Unix(at, 0)})
	}
	return out, rows.Err()
}
