package app

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/diagnostics"
	"github.com/braedonsaunders/ether/internal/store"
)

// collectDatabaseStorage reads allocation only, never row values or file paths.
// A bounded read transaction keeps the page counts internally consistent and
// cannot checkpoint, vacuum, or delete data while preparing a support bundle.
func collectDatabaseStorage(db *sql.DB, fact *diagnostics.StorageFact) {
	if db == nil {
		return
	}
	fact.DatabaseEngine = store.DialectOf(db).String()
	if store.DialectOf(db) != store.SQLite {
		return
	}
	// The transaction lives as long as the table scan below; the page
	// counts keep their own short bound.
	txCtx, txCancel := context.WithTimeout(context.Background(), supportStorageBudget+2*time.Second)
	defer txCancel()
	tx, err := db.BeginTx(txCtx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		fact.DatabaseAllocationStatus = "unavailable"
		return
	}
	ctx, cancel := context.WithTimeout(txCtx, 2*time.Second)
	defer cancel()
	defer tx.Rollback()
	var pageSize, pageCount, freePages int64
	for _, q := range []struct {
		sql string
		out *int64
	}{
		{"PRAGMA page_size", &pageSize}, {"PRAGMA page_count", &pageCount}, {"PRAGMA freelist_count", &freePages},
	} {
		if err = tx.QueryRowContext(ctx, q.sql).Scan(q.out); err != nil {
			fact.DatabaseAllocationStatus = "unavailable"
			return
		}
	}
	fact.DatabasePageSize = pageSize
	fact.DatabasePageCount = pageCount
	fact.DatabaseFreePages = freePages
	fact.DatabaseAllocationStatus = "complete"
	// Include indexes and FTS backing tables separately: they can account for
	// most of a database's bytes. Do not inspect stored titles, queries or URLs.
	scanCtx, scanCancel := context.WithTimeout(context.Background(), supportStorageBudget)
	defer scanCancel()
	started := time.Now()
	objects, tables, status := sqliteObjects(scanCtx, tx)
	fact.DatabaseTablesMillis = time.Since(started).Milliseconds()
	fact.DatabaseTables, fact.DatabaseTablesStatus = tables, status
	if status == "unavailable" {
		fact.DatabaseObjectsStatus = "unavailable"
		return
	}
	fact.DatabaseObjects = objects
	fact.DatabaseObjectsStatus = status
}

// supportStorageBudget bounds the per-table allocation scan. It reads
// only interior b-tree pages (btreePages), about 1% of the file, so it
// normally completes in well under a second; tables are measured largest
// first (by planner row estimates) in case a cold, busy disk makes it slow,
// and the fact says whether the list is complete and how long it took. It runs only while an owner builds a support bundle, outside the
// recorder lock, in one read transaction, which never blocks writers.
const supportStorageBudget = 15 * time.Second

// supportDetailBudget bounds the catalogue counts and the other detail
// queries, which read one narrow pass over catalog_titles at most.
const supportDetailBudget = 10 * time.Second

// collectStorageDetail adds what grew: per-table bytes (table and indexes
// apart), row estimates, catalogue counts by type, browsable and detail
// state, On Air asset bytes by kind, the catalogue's browsable bar, and the
// pre-migrate snapshots and backups beside the database. It reads
// allocation and counts only, never stored titles, names or paths.
func collectStorageDetail(db *sql.DB, dataDir string, cat config.Catalog, fact *diagnostics.StorageFact) {
	fact.CatalogSettings = &diagnostics.CatalogSettingsFact{
		MinPopularity: cat.MinPopularity, MinVotes: cat.MinVotes, HideObscure: cat.MinPopularity > 0 || cat.MinVotes > 0,
		IncludeAdult: cat.IncludeAdult, ShowUpcomingMovies: cat.ShowUpcomingMovies,
	}
	if dataDir != "" {
		fact.PreMigrateSnapshots = fileSet(filepath.Join(dataDir, "ether.db.pre-migrate.bak*"))
		fact.Backups = fileSet(filepath.Join(dataDir, "backups", "*"))
	}
	if db == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), supportDetailBudget)
	defer cancel()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		fact.CatalogCountsStatus = "unavailable"
		return
	}
	defer tx.Rollback()
	if store.DialectOf(db) == store.Postgres {
		started := time.Now()
		fact.DatabaseTables, fact.DatabaseTablesStatus = postgresTables(ctx, tx)
		fact.DatabaseTablesMillis = time.Since(started).Milliseconds()
	} else {
		var mode int
		if tx.QueryRowContext(ctx, `PRAGMA auto_vacuum`).Scan(&mode) == nil {
			fact.DatabaseAutoVacuum = map[int]string{0: "none", 1: "full", 2: "incremental"}[mode]
		}
		_ = tx.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&fact.DatabaseJournalMode)
	}
	fact.OnAirAssets = onAirAssetKinds(ctx, tx)
	fact.CatalogCounts, fact.CatalogCountsStatus = catalogCounts(ctx, tx)
}

func fileSet(pattern string) diagnostics.FileSetFact {
	var out diagnostics.FileSetFact
	matches, _ := filepath.Glob(pattern)
	for _, m := range matches {
		if info, err := os.Stat(m); err == nil && info.Mode().IsRegular() {
			out.Files++
			out.Bytes += info.Size()
		}
	}
	return out
}

// catalogCounts counts catalogue rows by type and browsable state, then by
// browsable and detail state. Each grouping reads one narrow covering
// index (type,browsable,… and browsable,detail,…), never the table.
func catalogCounts(ctx context.Context, tx *sql.Tx) ([]diagnostics.CatalogCountFact, string) {
	var out []diagnostics.CatalogCountFact
	for _, q := range []string{
		`SELECT type, browsable, -1, COUNT(*) FROM catalog_titles GROUP BY type, browsable`,
		`SELECT '', browsable, detail, COUNT(*) FROM catalog_titles GROUP BY browsable, detail`,
	} {
		rows, err := tx.QueryContext(ctx, q)
		if err != nil {
			return nil, "unavailable"
		}
		for rows.Next() {
			var c diagnostics.CatalogCountFact
			var browsable, detail int
			if err := rows.Scan(&c.Type, &browsable, &detail, &c.Rows); err != nil {
				rows.Close()
				return nil, "unavailable"
			}
			c.Browsable = browsable != 0
			if detail >= 0 {
				d := detail != 0
				c.Detailed = &d
			}
			out = append(out, c)
		}
		rows.Close()
		if rows.Err() != nil {
			return nil, "unavailable"
		}
	}
	return out, "complete"
}

func onAirAssetKinds(ctx context.Context, tx *sql.Tx) []diagnostics.AssetKindFact {
	// size precedes the content column, so this reads no blob pages.
	rows, err := tx.QueryContext(ctx, `SELECT kind, COUNT(*), COALESCE(SUM(size),0) FROM onair_assets GROUP BY kind ORDER BY 3 DESC LIMIT 20`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []diagnostics.AssetKindFact
	for rows.Next() {
		var a diagnostics.AssetKindFact
		if rows.Scan(&a.Kind, &a.Count, &a.Bytes) != nil {
			return nil
		}
		a.Kind = supportToken(a.Kind)
		out = append(out, a)
	}
	return out
}

// supportToken keeps a short category token and drops anything else.
func supportToken(s string) string {
	if len(s) > 32 {
		return "other"
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.' || r == '/') {
			return "other"
		}
	}
	return s
}

// sqliteObjects measures each b-tree's pages, largest row estimate first,
// until ctx's budget runs out. It returns the 20 largest objects and every
// measured table with its indexes.
func sqliteObjects(ctx context.Context, tx *sql.Tx) ([]diagnostics.DatabaseObjectFact, []diagnostics.DatabaseTableFact, string) {
	var pageSize int64
	var header []byte
	if err := tx.QueryRowContext(ctx, `PRAGMA page_size`).Scan(&pageSize); err != nil {
		return nil, nil, "unavailable"
	}
	if err := tx.QueryRowContext(ctx, `SELECT data FROM sqlite_dbpage WHERE pgno=1`).Scan(&header); err != nil || len(header) < 100 {
		return nil, nil, "unavailable"
	}
	usable := pageSize - int64(header[20]) // reserved bytes per page
	type object struct {
		name, table string
		index       bool
		rows, root  int64
	}
	estimates := map[string]int64{}
	if rows, err := tx.QueryContext(ctx, `SELECT tbl, COALESCE(idx,''), stat FROM sqlite_stat1`); err == nil {
		for rows.Next() {
			var tbl, idx, stat string
			if rows.Scan(&tbl, &idx, &stat) != nil {
				continue
			}
			n, _ := strconv.ParseInt(strings.Fields(stat + " 0")[0], 10, 64)
			if idx == "" {
				idx = tbl
			}
			estimates[idx] = n
		}
		rows.Close()
	}
	rows, err := tx.QueryContext(ctx, `SELECT name, tbl_name, type='index', rootpage FROM sqlite_schema WHERE type IN ('table','index') AND rootpage>0
		UNION ALL SELECT 'sqlite_schema', 'sqlite_schema', 0, 1`)
	if err != nil {
		return nil, nil, "unavailable"
	}
	var objects []object
	for rows.Next() {
		var o object
		if rows.Scan(&o.name, &o.table, &o.index, &o.root) != nil {
			rows.Close()
			return nil, nil, "unavailable"
		}
		o.table = ftsBase(o.table)
		o.rows = estimates[o.name]
		objects = append(objects, o)
	}
	rows.Close()
	// sqlite_stat1 has a table row only for a table without indexes; its
	// indexes' first figure is the table's row count.
	tableRows := map[string]int64{}
	for _, o := range objects {
		tableRows[o.table] = max(tableRows[o.table], o.rows)
	}
	for i := range objects {
		if !objects[i].index {
			objects[i].rows = tableRows[objects[i].table]
		}
	}
	// Largest first; a table before its indexes, so a partial scan still
	// has the biggest tables' own bytes.
	sort.SliceStable(objects, func(i, j int) bool {
		if objects[i].rows != objects[j].rows {
			return objects[i].rows > objects[j].rows
		}
		return !objects[i].index && objects[j].index
	})
	tables := map[string]*diagnostics.DatabaseTableFact{}
	var sizes []diagnostics.DatabaseObjectFact
	status := "complete"
	for i, o := range objects {
		pages, err := btreePages(ctx, tx, o.root, usable, o.rows)
		bytes := pages * pageSize
		if err != nil {
			if i == 0 {
				return nil, nil, "unavailable"
			}
			status = "partial"
			break
		}
		sizes = append(sizes, diagnostics.DatabaseObjectFact{Name: o.name, Bytes: bytes})
		t := tables[o.table]
		if t == nil {
			t = &diagnostics.DatabaseTableFact{Name: o.table, Rows: tableRows[o.table]}
			tables[o.table] = t
		}
		if o.index {
			t.IndexBytes += bytes
			t.Indexes++
		} else {
			t.TableBytes += bytes
		}
	}
	out := make([]diagnostics.DatabaseTableFact, 0, len(tables))
	for _, t := range tables {
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].TableBytes+out[i].IndexBytes > out[j].TableBytes+out[j].IndexBytes
	})
	if len(out) > 60 {
		out = out[:60]
	}
	sort.Slice(sizes, func(i, j int) bool {
		if sizes[i].Bytes != sizes[j].Bytes {
			return sizes[i].Bytes > sizes[j].Bytes
		}
		return sizes[i].Name < sizes[j].Name
	})
	if len(sizes) > 20 {
		sizes = sizes[:20]
	}
	return sizes, out, status
}

// ftsBase maps an FTS5 shadow table to its virtual table.
func ftsBase(name string) string {
	for _, suffix := range []string{"_data", "_idx", "_docsize", "_config", "_content"} {
		if base, ok := strings.CutSuffix(name, suffix); ok && strings.HasSuffix(base, "_fts") {
			return base
		}
	}
	return name
}

func postgresTables(ctx context.Context, tx *sql.Tx) ([]diagnostics.DatabaseTableFact, string) {
	rows, err := tx.QueryContext(ctx, `SELECT c.relname,
		pg_relation_size(c.oid) + CASE WHEN c.reltoastrelid<>0 THEN pg_total_relation_size(c.reltoastrelid) ELSE 0 END,
		pg_indexes_size(c.oid), (SELECT COUNT(*) FROM pg_index i WHERE i.indrelid=c.oid), GREATEST(c.reltuples,0)::bigint
		FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
		WHERE n.nspname=current_schema() AND c.relkind='r'
		ORDER BY pg_total_relation_size(c.oid) DESC LIMIT 60`)
	if err != nil {
		return nil, "unavailable"
	}
	defer rows.Close()
	var out []diagnostics.DatabaseTableFact
	for rows.Next() {
		var t diagnostics.DatabaseTableFact
		if rows.Scan(&t.Name, &t.TableBytes, &t.IndexBytes, &t.Indexes, &t.Rows) != nil {
			return nil, "unavailable"
		}
		out = append(out, t)
	}
	if rows.Err() != nil {
		return nil, "unavailable"
	}
	return out, "complete"
}

// btreePages counts a b-tree's pages by reading only its interior pages
// (through sqlite_dbpage, inside the caller's read transaction): the
// children of the lowest interior level are the leaves. dbstat reads and
// parses every page, which on a multi-gigabyte database takes minutes; the
// interior pages are about one in a hundred. A table of large values (at
// most four rows per leaf, at most btreeLeafScan leaves: On Air assets)
// also has its leaves read, so its overflow pages are counted from each
// cell's payload size without reading them.
func btreePages(ctx context.Context, tx *sql.Tx, root, usable, rows int64) (int64, error) {
	page := func(n int64) ([]byte, int, error) {
		var data []byte
		if err := tx.QueryRowContext(ctx, `SELECT data FROM sqlite_dbpage WHERE pgno=?`, n).Scan(&data); err != nil {
			return nil, 0, err
		}
		off := 0
		if n == 1 {
			off = 100 // the database header
		}
		if len(data) < off+12 {
			return nil, 0, errBadPage
		}
		return data, off, nil
	}
	level := []int64{root}
	total := int64(0)
	for depth := 0; depth < 32; depth++ {
		var children []int64
		for _, n := range level {
			data, off, err := page(n)
			if err != nil {
				return 0, err
			}
			switch data[off] {
			case 0x0a, 0x0d: // a leaf: only the root can be one here
				return total + 1 + overflowPages(data, off, usable), nil
			case 0x02, 0x05:
			default:
				return 0, errBadPage
			}
			total++
			cells := int(data[off+3])<<8 | int(data[off+4])
			for i := 0; i < cells; i++ {
				c, ok := cellOffset(data, off+12, i)
				if !ok || c+4 > len(data) {
					return 0, errBadPage
				}
				children = append(children, int64(be32(data[c:])))
			}
			children = append(children, int64(be32(data[off+8:])))
		}
		// The tree is balanced: if one child is a leaf, they all are.
		data, off, err := page(children[0])
		if err != nil {
			return 0, err
		}
		if data[off] != 0x0a && data[off] != 0x0d {
			level = children
			continue
		}
		total += int64(len(children))
		// Only a table whose rows average more than a quarter page can
		// spill; its leaves are few.
		if data[off] == 0x0d && len(children) <= btreeLeafScan && rows <= 4*int64(len(children)) {
			for _, n := range children {
				data, off, err := page(n)
				if err != nil {
					return 0, err
				}
				total += overflowPages(data, off, usable)
			}
		}
		return total, nil
	}
	return 0, errBadPage
}

const btreeLeafScan = 4096

var errBadPage = errors.New("unexpected b-tree page")

func be32(b []byte) uint32 {
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

func cellOffset(data []byte, array, i int) (int, bool) {
	p := array + 2*i
	if p+2 > len(data) {
		return 0, false
	}
	return int(data[p])<<8 | int(data[p+1]), true
}

// overflowPages is the number of overflow pages a leaf's cells chain to,
// from SQLite's file format: a payload beyond the local maximum spills
// into pages of usable-4 bytes each.
func overflowPages(data []byte, off int, usable int64) int64 {
	table := data[off] == 0x0d
	cells := int(data[off+3])<<8 | int(data[off+4])
	u := usable
	maxLocal := u - 35
	if !table {
		maxLocal = (u-12)*64/255 - 23
	}
	minLocal := (u-12)*32/255 - 23
	var n int64
	for i := 0; i < cells; i++ {
		c, ok := cellOffset(data, off+8, i)
		if !ok || c >= len(data) {
			return n
		}
		payload, _ := readVarint(data[c:])
		if payload <= maxLocal {
			continue
		}
		local := minLocal + (payload-minLocal)%(u-4)
		if local > maxLocal {
			local = minLocal
		}
		n += (payload - local + u - 5) / (u - 4)
	}
	return n
}

func readVarint(b []byte) (int64, int) {
	var v int64
	for i := 0; i < 9 && i < len(b); i++ {
		if i == 8 {
			return v<<8 | int64(b[i]), 9
		}
		v = v<<7 | int64(b[i]&0x7f)
		if b[i] < 0x80 {
			return v, i + 1
		}
	}
	return v, len(b)
}
