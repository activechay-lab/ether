package catalog

import (
	"context"
	"github.com/braedonsaunders/ether/internal/core"
)

const consoleEvidenceWindow = 4096

type consoleEvidencePilotRow struct {
	key        core.RatingKey
	popularity any
}

// The ordered pilot visits a fixed number of physical index entries, then
// applies the complete filter/policy. A full page is globally correct because
// every key outside the window follows it. An incomplete window falls back to
// the selective candidate query; an exhausted window needs no second query.
func (c *Catalog) consoleEvidencePilot(ctx context.Context, typ core.MediaType, where string, args []any, seek consoleSeekRange, limit int) ([]consoleEvidencePilotRow, bool, error) {
	query := `WITH console_window AS MATERIALIZED (
 SELECT t.key,t.popularity FROM catalog_titles t` + c.indexedBy("catalog_titles_console_pop_page") + `
 WHERE t.type=? AND t.browsable=1 AND t.adult=0` + seek.predicate + `
 ORDER BY t.popularity DESC,t.key ASC LIMIT 4096
 ), console_matches AS MATERIALIZED (
 SELECT t.key,t.popularity FROM console_window w CROSS JOIN catalog_titles t ON t.key=w.key
 WHERE ` + where + ` ORDER BY t.popularity DESC,t.key ASC LIMIT ?
 ) SELECT key,popularity,-1 FROM console_matches
 UNION ALL SELECT 0,0,COUNT(*) FROM console_window`
	values := []any{string(typ)}
	values = append(values, seek.args...)
	values = append(values, args...)
	values = append(values, limit)
	rows, err := c.db.QueryContext(ctx, query, values...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	matches := make([]consoleEvidencePilotRow, 0, limit)
	scanned := consoleEvidenceWindow
	for rows.Next() {
		var r consoleEvidencePilotRow
		var count int
		if err := rows.Scan(&r.key, &r.popularity, &count); err != nil {
			return nil, false, err
		}
		if count >= 0 {
			scanned = count
		} else {
			matches = append(matches, r)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	return matches, len(matches) >= limit || scanned < consoleEvidenceWindow, nil
}
