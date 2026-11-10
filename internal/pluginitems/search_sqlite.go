package pluginitems

import (
	"context"
	"database/sql"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
)

func sqliteIndexFTS(ctx context.Context, tx *sql.Tx, seq int64, ref core.ItemRef, libraryID core.LibraryID, title, fieldsText string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM plugin_item_fts WHERE rowid = ?`, seq); err != nil {
		return err
	}
	combined := strings.TrimSpace(title + " " + fieldsText)
	if combined == "" {
		return nil
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO plugin_item_fts(rowid, item_ref, library_id, text) VALUES(?, ?, ?, ?)`,
		seq, string(ref), int64(libraryID), combined)
	return err
}

func sqliteDeleteFTS(ctx context.Context, tx *sql.Tx, seq int64) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM plugin_item_fts WHERE rowid = ?`, seq)
	return err
}

func (s *Service) searchSQLite(ctx context.Context, q SearchQuery, tokens []string, limit int) (BrowsePage, error) {
	parts := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		parts = append(parts, `"`+strings.ReplaceAll(tok, `"`, `""`)+`"*`)
	}
	matchExpr := strings.Join(parts, " AND ")

	var where strings.Builder
	args := []any{matchExpr, int64(q.LibraryID)}
	where.WriteString(`f.plugin_item_fts MATCH ? AND i.library_id = ?`)
	if q.Level != "" {
		where.WriteString(` AND i.level = ?`)
		args = append(args, q.Level)
	}
	appendFacetClauses(&where, &args, q.Facets, nil)

	var total int
	countSQL := `SELECT COUNT(*) FROM plugin_item_fts f JOIN plugin_items i ON i.seq = f.rowid WHERE ` + where.String()
	if err := s.db.QueryRowContext(ctx, countSQL, args...).Scan(&total); err != nil {
		return BrowsePage{}, err
	}
	if total == 0 {
		return BrowsePage{Total: 0}, nil
	}

	querySQL := `SELECT ` + prefixedItemCols("i.") + `
FROM plugin_item_fts f
JOIN plugin_items i ON i.seq = f.rowid
WHERE ` + where.String() + `
ORDER BY bm25(f.plugin_item_fts), i.sort_title ASC, i.item_ref ASC
LIMIT ?`
	qArgs := append(append([]any(nil), args...), limit)
	items, err := s.queryItems(ctx, querySQL, qArgs, true)
	if err != nil {
		return BrowsePage{}, err
	}
	return BrowsePage{Items: items, Total: total}, nil
}
