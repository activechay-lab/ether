package catalog

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

const releaseMatchCols = `SELECT t.key,t.type,t.tmdb_id,t.imdb,t.tvdb,t.title,t.original_title,t.year,t.adult FROM catalog_titles t `

// ReleaseMatches joins a feed page to local metadata through bounded indexed lookups. It
// never spends a metadata API call per feed item. Ambiguous names are ignored;
// explicit identifier conflicts always defeat a textual match. Season packs are
// expanded only to locally known episodes, never to invented episode identities.
func (c *Catalog) ReleaseMatches(ctx context.Context, releases []core.Release) (map[string][]core.Title, error) {
	byName := map[string][]int{}
	byID := map[string][]int{}
	for i, r := range releases {
		n := releaseparse.NormalizeTitle(r.Info.Title)
		if n != "" {
			byName[n] = append(byName[n], i)
		}
		for _, id := range feedIDs(r.TitleIDs) {
			byID[id] = append(byID[id], i)
		}
	}
	// Each key lookup is capped. If an unusually common key overflows,
	// refuse to guess for every affected release rather than scan the library
	// or choose a candidate from an incomplete set.
	const candidateLimit = 256
	matches := map[int][]core.RatingKey{}
	overflow := map[int]bool{}
	seen := map[int]map[core.RatingKey]bool{}
	lookup := func(query string, value any, indices []int) error {
		rows, err := c.db.QueryContext(ctx, query, value, candidateLimit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		var titles []core.Title
		for rows.Next() {
			var t core.Title
			var adult bool
			if err := rows.Scan(&t.Key, &t.Type, &t.IDs.TMDB, &t.IDs.IMDB, &t.IDs.TVDB, &t.Title, &t.OriginalTitle, &t.Year, &adult); err != nil {
				return err
			}
			if adult {
				t.Type = ""
			}
			titles = append(titles, t)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if len(titles) > candidateLimit {
			for _, i := range indices {
				overflow[i] = true
			}
			return nil
		}
		for _, t := range titles {
			if t.Type != core.TypeMovie && t.Type != core.TypeShow {
				continue
			}
			for _, i := range indices {
				if seen[i] == nil {
					seen[i] = map[core.RatingKey]bool{}
				}
				if seen[i][t.Key] {
					continue
				}
				seen[i][t.Key] = true
				r := releases[i]
				tv := len(r.Info.Seasons) > 0 || len(r.Info.Episodes) > 0 || r.Info.FullSeason || r.Category/1000 == 5
				if (t.Type == core.TypeShow) != tv {
					continue
				}
				same, conflict := releaseparse.CompareTitleIDs(r.TitleIDs, t.IDs)
				if conflict || (!same && (!releaseparse.YearMatches(r.Info.Year, t.Year) || !(releaseparse.TitleMatches(r.Info, t.Title) || releaseparse.TitleMatches(r.Info, t.OriginalTitle)))) {
					continue
				}
				if !same && t.Type == core.TypeMovie && r.Info.Year == 0 {
					continue
				}
				matches[i] = append(matches[i], t.Key)
			}
		}
		return nil
	}
	for name, indices := range byName {
		if err := lookup(releaseMatchCols+`JOIN catalog_release_names n ON n.key=t.key WHERE n.name=? LIMIT ?`, name, indices); err != nil {
			return nil, err
		}
	}
	for id, indices := range byID {
		kind, value, _ := strings.Cut(id, ":")
		column := map[string]string{"tmdb": "tmdb_id", "imdb": "imdb", "tvdb": "tvdb"}[kind]
		if column == "" {
			continue
		}
		var arg any = value
		if kind != "imdb" {
			var err error
			arg, err = strconv.ParseInt(value, 10, 64)
			if err != nil {
				return nil, err
			}
		}
		if err := lookup(releaseMatchCols+`WHERE t.`+column+`=? LIMIT ?`, arg, indices); err != nil {
			return nil, err
		}
	}
	out := map[string][]core.Title{}
	for i, keys := range matches {
		if overflow[i] || len(keys) != 1 {
			continue
		}
		r := releases[i]
		if keys[0].Kind() == core.KindMovie {
			if t, e := c.CachedTitle(ctx, keys[0]); e == nil && t.IsReleased(c.now()) {
				out[r.ID] = []core.Title{*t}
			}
			continue
		}
		eps, e := c.queryTitles(ctx, `SELECT `+titleCols+` FROM catalog_titles WHERE show_key=? AND type='episode' AND adult=0 AND release_known=1 AND released_ts<=? ORDER BY season,episode LIMIT 2000`, int64(keys[0]), c.now().Unix())
		if e != nil {
			return nil, e
		}
		for _, t := range eps {
			season := false
			for _, s := range r.Info.Seasons {
				if s == t.Season {
					season = true
				}
			}
			if releaseparse.EpisodeMatches(r.Info, t.Season, t.Episode) || (r.Info.FullSeason && season) {
				out[r.ID] = append(out[r.ID], t)
			}
		}
	}
	return out, nil
}
func feedIDs(ids core.IDs) []string {
	var out []string
	if ids.TMDB > 0 {
		out = append(out, fmt.Sprintf("tmdb:%d", ids.TMDB))
	}
	if ids.TVDB > 0 {
		out = append(out, fmt.Sprintf("tvdb:%d", ids.TVDB))
	}
	if ids.IMDB != "" {
		out = append(out, "imdb:"+ids.IMDB)
	}
	return out
}
