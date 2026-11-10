package pluginitems

import (
	"context"
	"strings"
)

func (s *Service) searchPostgres(ctx context.Context, q SearchQuery, tokens []string, limit int) (BrowsePage, error) {
	parts := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		parts = append(parts, tok+":*")
	}
	tsQuery := strings.Join(parts, " & ")

	var where strings.Builder
	args := []any{int64(q.LibraryID), tsQuery}
	where.WriteString(`i.library_id = ? AND i.search_tsv @@ to_tsquery('simple', ?)`)
	if q.Level != "" {
		where.WriteString(` AND i.level = ?`)
		args = append(args, q.Level)
	}
	appendFacetClauses(&where, &args, q.Facets, nil)

	var total int
	countSQL := `SELECT COUNT(*) FROM plugin_items i WHERE ` + where.String()
	if err := s.db.QueryRowContext(ctx, countSQL, args...).Scan(&total); err != nil {
		return BrowsePage{}, err
	}
	if total == 0 {
		return BrowsePage{Total: 0}, nil
	}

	querySQL := `SELECT ` + prefixedItemCols("i.") + `
FROM plugin_items i
WHERE ` + where.String() + `
ORDER BY ts_rank(i.search_tsv, to_tsquery('simple', ?)) DESC, i.sort_title ASC, i.item_ref ASC
LIMIT ?`
	qArgs := append(append([]any(nil), args...), tsQuery, limit)
	items, err := s.queryItems(ctx, querySQL, qArgs, true)
	if err != nil {
		return BrowsePage{}, err
	}
	return BrowsePage{Items: items, Total: total}, nil
}
