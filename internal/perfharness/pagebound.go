package perfharness

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

// A listing page reads what it renders plus an index seek: its titles' rows
// and each title's detail rows (genres, keywords, alternative titles,
// studios, countries, credits, home-release check), which the page
// hydrates. How many detail rows a title has depends on how famous it is,
// so two pages of the same size read very different rows: a page deep in a
// popularity order is famous titles on a large library and obscure ones on
// a small one. The rows ratio between scales cannot tell that from a walk.
//
// For listing (browse) steps the gate checks a bound instead: the rows one
// request reads are at most, for each title it rendered, the most detail
// rows any title of that type has in the fixture, plus pageRowsSlack. A
// walk of the library breaks it at once; a page of famous titles does not.

// pageRowsSlack covers a page's own reads beside its titles: the total, the
// viewer's state and settings, and the like.
const pageRowsSlack = 200

// pageItemRows are the rows beside its detail a rendered title reads: its
// own row, its watch state, guid, artwork colours and trailer.
const pageItemRows = 10

// DetailCaps is, for each title type, the most rows a page's hydration of
// one title of that type reads in the generated library: its detail rows
// (the facts catalog/store.go hydrateFactBatch reads) plus pageItemRows,
// and for a series two rows per season (aired counts).
func DetailCaps(ctx context.Context, db *sql.DB) (map[string]int, error) {
	rows, err := db.QueryContext(ctx, `WITH f(key, n) AS (
 SELECT key, COUNT(*) FROM catalog_genres GROUP BY key
 UNION ALL SELECT key, COUNT(*) FROM catalog_keywords GROUP BY key
 UNION ALL SELECT key, COUNT(*) FROM catalog_alt_titles GROUP BY key
 UNION ALL SELECT key, COUNT(*) FROM catalog_studios GROUP BY key
 UNION ALL SELECT key, COUNT(*) FROM catalog_countries GROUP BY key
 UNION ALL SELECT key, COUNT(*) FROM catalog_credits GROUP BY key
 UNION ALL SELECT key, 1 FROM catalog_home_release
), per AS (SELECT key, SUM(n) AS n FROM f GROUP BY key)
SELECT t.type, MAX(per.n + CASE WHEN t.type='show' THEN 2*t.child_count ELSE 0 END)
FROM per JOIN catalog_titles t ON t.key=per.key GROUP BY t.type`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	caps := map[string]int{}
	for rows.Next() {
		var typ string
		var n int
		if err := rows.Scan(&typ, &n); err != nil {
			return nil, err
		}
		caps[typ] = n + pageItemRows
	}
	return caps, rows.Err()
}

// renderedTitles counts the titles a JSON listing response rendered, by
// type: Plex's MediaContainer.Metadata, Jellyfin and Emby's Items, Silo's
// items and Stremio's metas. Anything else counts nothing.
func renderedTitles(body []byte) map[string]int {
	type item struct {
		Type string `json:"type"`
	}
	var c struct {
		MediaContainer struct {
			Metadata []item `json:"Metadata"`
		} `json:"MediaContainer"`
		Items []item `json:"items"`
		Metas []item `json:"metas"`
	}
	if len(body) == 0 || body[0] != '{' || json.Unmarshal(body, &c) != nil {
		return nil
	}
	out := map[string]int{}
	for _, list := range [][]item{c.MediaContainer.Metadata, c.Items, c.Metas} {
		for _, it := range list {
			switch strings.ToLower(it.Type) {
			case "movie":
				out["movie"]++
			case "show", "series":
				out["show"]++
			case "season":
				out["season"]++
			case "episode":
				out["episode"]++
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ApplyPageBound sets each listing row's page bound: the titles its
// requests rendered and by how much its worst request read past what they
// render (caps from DetailCaps).
func ApplyPageBound(rows []*Row, results []Result, trace map[string]TraceRecord, caps map[string]int) {
	byKey := map[string]*Row{}
	for _, r := range rows {
		if r.Class == classBrowse {
			byKey[r.Client+"\x00"+r.Step] = r
		}
	}
	for _, res := range results {
		r := byKey[res.Client+"\x00"+res.Step]
		if r == nil || len(res.RenderedItems) == 0 {
			continue
		}
		tr, ok := trace[res.Tag]
		if !ok || (res.Phase != "cold" && res.Phase != "warm" && res.Phase != "first") {
			continue
		}
		bound, items := int64(pageRowsSlack), 0
		for typ, n := range res.RenderedItems {
			items += n
			bound += int64(n * caps[typ])
		}
		if !r.PageCounted || tr.Rows-bound > r.PageRows-r.PageBound {
			r.PageRows, r.PageBound, r.PageItems = tr.Rows, bound, items
		}
		r.PageCounted = true
	}
}

// PageOver reports, for a listing row with countable titles, whether its
// worst request read more than its titles render.
func (r *Row) PageOver() (string, bool) {
	if !r.PageCounted || r.PageRows <= r.PageBound {
		return "", false
	}
	return fmt.Sprintf("rows read %d over what its %d titles render (%d)", r.PageRows, r.PageItems, r.PageBound), true
}
