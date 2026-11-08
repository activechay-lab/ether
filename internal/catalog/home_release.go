package catalog

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/tmdb"
)

// homeReleaseAge is the owner-approved cinema-to-home admission window.
const homeReleaseAge = 120 * 24 * time.Hour

// HomeReleaseAgeAdmitted reports admission based on a known cinema date.
func HomeReleaseAgeAdmitted(t *core.Title, now time.Time) bool {
	return t.Type == core.TypeMovie && !t.Released.IsZero() && t.Released.Unix() != 0 && t.Released.Before(now.Add(-homeReleaseAge))
}

func homeReleaseCutoff(now time.Time) int64 {
	cutoff := now.Add(-homeReleaseAge)
	if cutoff.Nanosecond() != 0 {
		return cutoff.Unix() + 1
	}
	return cutoff.Unix()
}

// homeReleaseVisible accepts a passed home date. Only when no home date is
// known does the cinema-age admission stand in for one: a known future
// digital date is never overridden.
func homeReleaseVisible(t *core.Title, now time.Time) bool {
	if t.Type != core.TypeMovie {
		return true
	}
	if t.HomeReleaseChecked && !t.HomeReleased.IsZero() {
		return !t.HomeReleased.After(now)
	}
	return HomeReleaseAgeAdmitted(t, now)
}

func (c *Catalog) homeVisible(t *core.Title) bool { return homeReleaseVisible(t, c.now()) }

func homeReleaseSQL(alias string) string {
	return "(" + alias + ".type<>'movie' OR (" + alias + ".release_known=1 AND " + alias + ".released_ts<? AND NOT EXISTS (SELECT 1 FROM catalog_home_release hk WHERE hk.key=" + alias + ".key AND hk.checked=1 AND hk.earliest_ts<>0)) OR EXISTS (SELECT 1 FROM catalog_home_release hr WHERE hr.key=" + alias + ".key AND hr.checked=1 AND hr.earliest_ts<>0 AND hr.earliest_ts<=?))"
}

func (c *Catalog) hydrateHomeRelease(ctx context.Context, items []core.Title) error {
	if len(items) == 0 {
		return nil
	}
	idx := make(map[core.RatingKey][]int, len(items))
	keys := make([]any, 0, len(items))
	for i := range items {
		if _, ok := idx[items[i].Key]; !ok {
			keys = append(keys, int64(items[i].Key))
		}
		idx[items[i].Key] = append(idx[items[i].Key], i)
		items[i].HomeReleaseChecked = false
		items[i].HomeReleased = time.Time{}
		items[i].HomeReleaseFallbackAdmitted = false
	}
	// Bound SQLite parameters independently of the caller's page size. A title
	// can appear in several shelves; hydrate every occurrence from one row.
	const batchSize = 200
	for start := 0; start < len(keys); start += batchSize {
		args := keys[start:min(start+batchSize, len(keys))]
		err := func() error {
			rows, err := c.db.QueryContext(ctx, `SELECT t.key,COALESCE(h.checked,0),COALESCE(h.earliest_ts,0),COALESCE(h.fallback_admitted,0),t.released_ts,t.year FROM catalog_titles t LEFT JOIN catalog_home_release h ON t.key=h.key WHERE t.key IN (`+placeholders(len(args))+`)`, args...)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var key, ts, released int64
				var checked, admitted, year int
				if err := rows.Scan(&key, &checked, &ts, &admitted, &released, &year); err != nil {
					return err
				}
				for _, i := range idx[core.RatingKey(key)] {
					items[i].Released = time.Time{}
					if released != 0 {
						items[i].Released = time.Unix(released, 0).UTC()
					}
					items[i].Year = year
					items[i].HomeReleaseChecked = checked != 0
					items[i].HomeReleaseFallbackAdmitted = admitted != 0
					if ts != 0 {
						items[i].HomeReleased = time.Unix(ts, 0).UTC()
					}
				}
			}
			return rows.Err()
		}()
		if err != nil {
			return err
		}
	}
	return nil
}

func saveHomeReleases(ctx context.Context, tx *sql.Tx, t core.Title, dates *tmdb.ReleaseDates, now int64) error {
	type homeDate struct {
		country string
		kind    int
		ts      int64
	}
	var earliest time.Time
	want := map[homeDate]bool{}
	for _, country := range dates.Results {
		for _, release := range country.ReleaseDate {
			if release.Type != 4 && release.Type != 5 {
				continue
			}
			date := tmdb.ParseDate(strings.SplitN(release.ReleaseDate, "T", 2)[0])
			if date.IsZero() {
				continue
			}
			if earliest.IsZero() || date.Before(earliest) {
				earliest = date
			}
			want[homeDate{strings.ToUpper(country.ISO3166), release.Type, date.Unix()}] = true
		}
	}
	// A refresh usually brings the same dates; rewriting them would fire the
	// viewer-policy triggers for nothing.
	same := true
	rows, err := tx.QueryContext(ctx, `SELECT country,kind,released_ts FROM catalog_home_release_dates WHERE key=?`, int64(t.Key))
	if err != nil {
		return err
	}
	n := 0
	for rows.Next() {
		var d homeDate
		if err := rows.Scan(&d.country, &d.kind, &d.ts); err != nil {
			rows.Close()
			return err
		}
		same = same && want[d]
		n++
	}
	rows.Close()
	if !same || n != len(want) {
		if _, err := tx.ExecContext(ctx, `DELETE FROM catalog_home_release_dates WHERE key=?`, int64(t.Key)); err != nil {
			return err
		}
		var args []any
		for d := range want {
			args = append(args, int64(t.Key), d.country, d.kind, d.ts)
		}
		for start := 0; start < len(args); start += 800 {
			part := args[start:min(start+800, len(args))]
			if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_home_release_dates(key,country,kind,released_ts) VALUES `+valueRows(len(part)/4, 4)+` ON CONFLICT DO NOTHING`, part...); err != nil {
				return err
			}
		}
	}
	var ts int64
	if !earliest.IsZero() {
		ts = earliest.Unix()
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO catalog_home_release(key,checked,earliest_ts,attempted_at) VALUES(?,1,?,?) ON CONFLICT(key) DO UPDATE SET checked=1,earliest_ts=excluded.earliest_ts,attempted_at=excluded.attempted_at`, int64(t.Key), ts, now)
	return err
}

func (c *Catalog) HomeReleases(ctx context.Context, keys []core.RatingKey) (map[core.RatingKey]time.Time, error) {
	out := map[core.RatingKey]time.Time{}
	for start := 0; start < len(keys); start += 500 {
		batch := keys[start:min(start+500, len(keys))]
		args := make([]any, len(batch))
		for i, k := range batch {
			args[i] = int64(k)
		}
		rows, err := c.db.QueryContext(ctx, `SELECT key,earliest_ts FROM catalog_home_release WHERE checked=1 AND key IN (`+placeholders(len(batch))+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var key, ts int64
			if err = rows.Scan(&key, &ts); err != nil {
				rows.Close()
				return nil, err
			}
			if ts > 0 {
				out[core.RatingKey(key)] = time.Unix(ts, 0).UTC()
			} else {
				out[core.RatingKey(key)] = time.Time{}
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

// BackfillHomeReleases fetches at most limit details, never from a browse.
// Missing and future dates are revisited daily; failures also cool down for a
// day so an unavailable title cannot starve the remaining catalogue.
// Recently reached dates are rechecked daily; settled dates are revisited
// after 90 days to pick up later corrections.
func (c *Catalog) BackfillHomeReleases(ctx context.Context, limit int) (int, error) {
	if c.tmdb() == nil || limit <= 0 {
		return 0, nil
	}
	now := c.now().Unix()
	rows, err := c.db.QueryContext(ctx, `SELECT t.key FROM catalog_titles t LEFT JOIN catalog_home_release h ON h.key=t.key WHERE t.type='movie' AND t.adult=0 AND t.release_known=1 AND t.released_ts<=? AND (h.key IS NULL OR (h.attempted_at<=? AND (h.checked=0 OR h.earliest_ts=0 OR h.earliest_ts>? OR h.attempted_at<=?))) ORDER BY COALESCE(h.attempted_at,0), t.browsable DESC, t.popularity DESC,t.key LIMIT ?`, now, now-int64((24*time.Hour).Seconds()), now-int64((30*24*time.Hour).Seconds()), now-int64((90*24*time.Hour).Seconds()), limit)
	if err != nil {
		return 0, err
	}
	var keys []core.RatingKey
	for rows.Next() {
		var k int64
		if err = rows.Scan(&k); err != nil {
			rows.Close()
			return 0, err
		}
		keys = append(keys, core.RatingKey(k))
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	count := 0
	for _, key := range keys {
		if err = ctx.Err(); err != nil {
			return count, err
		}
		_, err = c.db.ExecContext(ctx, `INSERT INTO catalog_home_release(key,attempted_at) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET attempted_at=excluded.attempted_at`, int64(key), now)
		if err != nil {
			return count, err
		}
		d, fetchErr := c.tmdb().MovieDetails(ctx, key.TMDB())
		if fetchErr != nil || d == nil {
			continue
		}
		t := c.movieDetailsToTitle(ctx, d)
		if err = c.upsert(ctx, t, upsertOpts{browsable: c.meetsThreshold(t), detail: true, releaseDates: d.ReleaseDates}); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}
