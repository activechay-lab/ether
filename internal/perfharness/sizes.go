package perfharness

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/braedonsaunders/ether/internal/store"
)

// ObjectSize is the on-disk size of one table or index.
type ObjectSize struct {
	Name  string `json:"name"`
	Table string `json:"table"` // the table it belongs to (itself for a table)
	Index bool   `json:"index"`
	Bytes int64  `json:"bytes"`
	Rows  int64  `json:"rows,omitempty"` // tables only
}

// MeasureSizes reports every table's and index's size: dbstat pages on
// SQLite, pg_relation_size (with TOAST for tables) on PostgreSQL. FTS5
// shadow tables count toward their virtual table.
func MeasureSizes(ctx context.Context, db *sql.DB, withRows bool) ([]ObjectSize, error) {
	var q string
	if store.DialectOf(db) == store.Postgres {
		q = `SELECT c.relname, COALESCE(t.relname, c.relname), c.relkind='i',
			pg_relation_size(c.oid) + CASE WHEN c.reltoastrelid<>0 THEN pg_total_relation_size(c.reltoastrelid) ELSE 0 END
			FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
			LEFT JOIN pg_index i ON i.indexrelid=c.oid LEFT JOIN pg_class t ON t.oid=i.indrelid
			WHERE n.nspname=current_schema() AND c.relkind IN ('r','i')`
	} else {
		q = `SELECT s.name, COALESCE(m.tbl_name, s.name), COALESCE(m.type,'table')='index', SUM(s.pgsize)
			FROM dbstat s LEFT JOIN sqlite_master m ON m.name=s.name GROUP BY s.name`
	}
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	var out []ObjectSize
	for rows.Next() {
		var o ObjectSize
		if err := rows.Scan(&o.Name, &o.Table, &o.Index, &o.Bytes); err != nil {
			rows.Close()
			return nil, err
		}
		o.Table = ftsOwner(o.Table)
		out = append(out, o)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if withRows {
		for i := range out {
			if out[i].Index || out[i].Name != out[i].Table || strings.HasPrefix(out[i].Name, "sqlite_") {
				continue
			}
			if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM "`+out[i].Name+`"`).Scan(&out[i].Rows); err != nil {
				return nil, fmt.Errorf("count %s: %w", out[i].Name, err)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Bytes > out[j].Bytes })
	return out, nil
}

// ftsOwner maps an FTS5 shadow table (x_data, x_idx, x_docsize, x_config,
// x_content) to its virtual table.
func ftsOwner(name string) string {
	for _, suffix := range []string{"_data", "_idx", "_docsize", "_config", "_content"} {
		if base, ok := strings.CutSuffix(name, suffix); ok && strings.HasSuffix(base, "_fts") {
			return base
		}
	}
	return name
}

// WriteSizeReport writes sizes grouped by table: the table, each of its
// indexes, and the group's total, with bytes per detailed title when
// detailed is positive.
func WriteSizeReport(w io.Writer, sizes []ObjectSize, detailed int64, minBytes int64) {
	type group struct {
		name  string
		total int64
		objs  []ObjectSize
	}
	groups := map[string]*group{}
	var all int64
	for _, o := range sizes {
		g := groups[o.Table]
		if g == nil {
			g = &group{name: o.Table}
			groups[o.Table] = g
		}
		g.total += o.Bytes
		g.objs = append(g.objs, o)
		all += o.Bytes
	}
	list := make([]*group, 0, len(groups))
	for _, g := range groups {
		list = append(list, g)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].total > list[j].total })
	per := func(b int64) string {
		if detailed <= 0 {
			return ""
		}
		return fmt.Sprintf(" | %.0f", float64(b)/float64(detailed))
	}
	head := "| object | rows | MB"
	if detailed > 0 {
		head += " | B/detailed title"
	}
	fmt.Fprintln(w, head+" |")
	fmt.Fprintln(w, strings.Repeat("|---", strings.Count(head, "|"))+"|")
	for _, g := range list {
		if g.total < minBytes {
			continue
		}
		fmt.Fprintf(w, "| **%s** (total) | | %.1f%s |\n", g.name, float64(g.total)/1e6, per(g.total))
		for _, o := range g.objs {
			rows := ""
			if o.Rows > 0 {
				rows = fmt.Sprint(o.Rows)
			}
			fmt.Fprintf(w, "| %s | %s | %.1f%s |\n", o.Name, rows, float64(o.Bytes)/1e6, per(o.Bytes))
		}
	}
	fmt.Fprintf(w, "| **all** | | %.1f%s |\n", float64(all)/1e6, per(all))
}
