package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// The SQLite tier migration copies the library tier into a fresh table and
// swaps it in, instead of deleting the long tail where it is. A delete
// touches every one of catalog_titles' indexes and its FTS index at a
// random page per row, so batch after batch rewrites the same pages: J
// measured 91.6 KB of WAL per removed row at 500 keys a transaction. A copy
// writes each kept row and its index entries once.
//
// Online throughout:
//
//   - copy: catalog_titles_keep (the same definition, with every index
//     under a temporary name, built while it is empty) and its FTS table
//     catalog_titles_fts_keep are filled in key-order chunks behind the WAL
//     budget, each chunk re-checking its key range's Tier A touches in its
//     transaction. Capture triggers on catalog_titles mirror every later
//     write to a row already behind the copy cursor: inserts always,
//     updates of kept rows and of rows Get just opened (detail=1),
//     deletes. The schema does not change while they run.
//   - swap: queries stop pinning indexes (c.indexedBy); one short
//     transaction re-reads every Tier A key, renames the tables (the old
//     one aside), moves the triggers and recounts the tallies. Readers see
//     the old table or the new one, never neither.
//   - cleanup: each temporary index is replaced by one under its real name
//     (the pinned ones first, then pins come back), and the old FTS table
//     and the old table are dropped, each read first so its drop walks
//     cached pages. Then the dependent rows of removed titles are deleted
//     a page of keys a transaction.
//
// Between batches the job runs a TRUNCATE checkpoint when the WAL file has
// passed its budget: readers that never pause keep SQLite from restarting
// the WAL on its own.
//
// The file keeps its size: SQLite returns freed pages to its freelist and
// reuses them for later writes. auto_vacuum is off, so shrinking it needs
// a VACUUM (an owner decision, not part of this job).

const (
	tierKeepTable = "catalog_titles_keep"
	tierKeepFTS   = "catalog_titles_fts_keep"
	tierOldTable  = "catalog_titles_tierold"
	tierOldFTS    = "catalog_titles_fts_tierold"
	tierTempIndex = "tierkeep_"
	tierMetaPhase = "tier_migrate_phase"
	tierMetaIndex = "tier_migrate_indexes"
	tierMetaHints = "tier_migrate_unpinned"
	// tierHintGrace lets queries that named a pinned index finish before
	// the swap moves it.
	tierHintGrace = 2 * time.Second
	// tierTruncateWait bounds how long a TRUNCATE checkpoint waits for
	// readers (writers wait behind it).
	tierTruncateWait = 250 * time.Millisecond
	tierCopyPage     = 2000
	tierCopyStart    = 1000
	tierOrphanPage   = 2000
)

// tierPinnedIndexes are the catalog_titles indexes queries name in INDEXED
// BY (through c.indexedBy). Queries go unpinned from just before the swap
// until cleanup has rebuilt these, first, under their own names.
// TestTierPinnedIndexesCoverIndexedBy keeps this list complete.
var tierPinnedIndexes = func() map[string]bool {
	indexes := map[string]bool{
		keysetIndex:                                  true,
		"catalog_titles_console_pop_page":            true,
		"catalog_titles_console_language_votes_page": true,
		"catalog_titles_console_year_title_page":     true,
		"catalog_titles_type_browse_year":            true,
		"catalog_titles_show":                        true,
		"catalog_titles_episode_recent":              true,
		"catalog_titles_warm_stale":                  true,
	}
	for _, swap := range titleIndexSwaps {
		indexes[swap.old] = true
		if swap.replacement != "" {
			indexes[swap.replacement] = true
		}
	}
	return indexes
}()

const tierCursorSQL = `(SELECT CAST(v AS INTEGER) FROM catalog_meta WHERE k='` + tierMetaCursor + `')`

// swapTiers runs (or resumes) the SQLite copy-and-swap.
func (c *Catalog) swapTiers(ctx context.Context, touched *tierTouched, p *TierMigration) error {
	for {
		phase := c.getMeta(ctx, tierMetaPhase)
		var err error
		switch phase {
		case "", "copy":
			err = c.tierCopy(ctx, touched, p)
		case "swap":
			err = c.tierSwap(ctx, p)
		case "cleanup":
			err = c.tierCleanup(ctx, p)
		case "orphans":
			err = c.tierDeleteOrphans(ctx, p)
		case "swept":
			return nil
		default:
			return fmt.Errorf("catalog: tier migration: unknown phase %q", phase)
		}
		if err != nil {
			return err
		}
	}
}

func (c *Catalog) setTierPhase(ctx context.Context, q execer, phase string) error {
	_, err := q.ExecContext(ctx, `INSERT INTO catalog_meta(k,v) VALUES(?,?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, tierMetaPhase, phase)
	return err
}

// titleColumns are catalog_titles' stored columns, in order (a generated
// column cannot be inserted).
func titleColumns(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, table string) ([]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT name, hidden FROM pragma_table_xinfo(?) ORDER BY cid`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var name string
		var hidden int
		if err := rows.Scan(&name, &hidden); err != nil {
			return nil, err
		}
		if hidden == 0 {
			cols = append(cols, name)
		}
	}
	if len(cols) == 0 && rows.Err() == nil {
		return nil, fmt.Errorf("catalog: table %s has no columns", table)
	}
	return cols, rows.Err()
}

func schemaSQL(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, typ, name string) (string, error) {
	var s sql.NullString
	err := q.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type=? AND name=?`, typ, name).Scan(&s)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return s.String, err
}

var (
	createTableRE = regexp.MustCompile(`(?i)^\s*CREATE\s+TABLE\s+(?:"catalog_titles"|catalog_titles)(\s*\()`)
	createFTSRE   = regexp.MustCompile(`(?i)^\s*CREATE\s+VIRTUAL\s+TABLE\s+(?:"catalog_titles_fts"|catalog_titles_fts)(\s+USING)`)
	onTitlesRE    = regexp.MustCompile(`(?i)\bON\s+"?catalog_titles"?(\s*\(|\s+(?:BEGIN|FOR|WHEN)\b|\s*$|\s+)`)
	ftsNameRE     = regexp.MustCompile(`\bcatalog_titles_fts\b`)
	indexHeadRE   = regexp.MustCompile(`(?i)^\s*CREATE\s+(UNIQUE\s+)?INDEX\s+(?:IF\s+NOT\s+EXISTS\s+)?("?[A-Za-z0-9_]+"?)\s+ON\s+"?[A-Za-z0-9_]+"?\s*\(`)
)

// retargetIndex rewrites a CREATE INDEX statement to name and table.
func retargetIndex(def, name, table string) (string, error) {
	m := indexHeadRE.FindStringSubmatchIndex(def)
	if m == nil {
		return "", fmt.Errorf("catalog: tier migration: cannot read index definition %q", def)
	}
	unique := ""
	if m[2] >= 0 {
		unique = "UNIQUE "
	}
	return "CREATE " + unique + "INDEX " + name + " ON " + table + "(" + def[m[1]:], nil
}

// tierCopy creates the copy and its capture triggers (once), then copies
// the library tier in key order.
func (c *Catalog) tierCopy(ctx context.Context, touched *tierTouched, p *TierMigration) error {
	if err := c.tierPrepareCopy(ctx); err != nil {
		return err
	}
	tables, err := existingTables(ctx, c.db, store.SQLite)
	if err != nil {
		return err
	}
	cols, err := titleColumns(ctx, c.db, "catalog_titles")
	if err != nil {
		return err
	}
	limit := tierCopyStart
	for {
		if !c.waitTierUnpaused(ctx, p) {
			return ctx.Err()
		}
		if err := c.tierWALBudget(ctx); err != nil {
			return err
		}
		began := time.Now()
		n, more, err := c.tierCopyBatch(ctx, tables, cols, limit, touched, p)
		if err != nil {
			return err
		}
		if !more {
			break
		}
		limit = nextBatch(limit, max(n, 1), time.Since(began), tierCopyPage)
	}
	// Everything is copied: from now on every write to catalog_titles is
	// mirrored, whatever its key.
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE catalog_meta SET v=? WHERE k=?`, strconv.FormatInt(math.MaxInt64, 10), tierMetaCursor); err != nil {
		return err
	}
	if err := c.setTierPhase(ctx, tx, "swap"); err != nil {
		return err
	}
	return tx.Commit()
}

// tierPrepareCopy creates catalog_titles_keep, its FTS table and triggers,
// and the capture triggers on catalog_titles, in one transaction.
func (c *Catalog) tierPrepareCopy(ctx context.Context) error {
	if c.getMeta(ctx, tierMetaPhase) == "copy" {
		return nil
	}
	defs, err := c.tierIndexDefs(ctx)
	if err != nil {
		return err
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	def, err := schemaSQL(ctx, tx, "table", "catalog_titles")
	if err != nil {
		return err
	}
	if !createTableRE.MatchString(def) {
		return fmt.Errorf("catalog: tier migration: cannot read catalog_titles' definition")
	}
	ftsDef, err := schemaSQL(ctx, tx, "table", "catalog_titles_fts")
	if err != nil {
		return err
	}
	if !createFTSRE.MatchString(ftsDef) {
		return fmt.Errorf("catalog: tier migration: cannot read catalog_titles_fts' definition")
	}
	stmts := []string{
		`DROP TABLE IF EXISTS ` + tierKeepFTS,
		`DROP TABLE IF EXISTS ` + tierKeepTable,
		createTableRE.ReplaceAllString(def, "CREATE TABLE "+tierKeepTable+"$1"),
		// The FTS content option keeps naming catalog_titles: after the
		// swap that is this copy.
		createFTSRE.ReplaceAllString(ftsDef, "CREATE VIRTUAL TABLE "+tierKeepFTS+"$1"),
	}
	// The copy's FTS table follows the copy the way the original follows
	// catalog_titles: the same triggers, retargeted.
	rows, err := tx.QueryContext(ctx, `SELECT name, sql FROM sqlite_master WHERE type='trigger' AND tbl_name='catalog_titles' AND sql LIKE '%catalog_titles_fts%'`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var name, body string
		if err := rows.Scan(&name, &body); err != nil {
			rows.Close()
			return err
		}
		body = ftsNameRE.ReplaceAllString(body, tierKeepFTS)
		body = onTitlesRE.ReplaceAllString(body, "ON "+tierKeepTable+"$1")
		body = strings.Replace(body, name, tierTempIndex+name, 1)
		stmts = append(stmts, `DROP TRIGGER IF EXISTS `+tierTempIndex+name, body)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	cols, err := titleColumns(ctx, tx, "catalog_titles")
	if err != nil {
		return err
	}
	list := strings.Join(cols, ",")
	var set []string
	for _, col := range cols[1:] {
		set = append(set, col+"=excluded."+col)
	}
	insert := `INSERT INTO ` + tierKeepTable + `(` + list + `) SELECT ` + list + ` FROM catalog_titles WHERE key=new.key`
	upsert := ` ON CONFLICT(key) DO UPDATE SET ` + strings.Join(set, ",") + `;`
	stmts = append(stmts,
		`DROP TRIGGER IF EXISTS catalog_tier_capture_insert`,
		`DROP TRIGGER IF EXISTS catalog_tier_capture_update`,
		`DROP TRIGGER IF EXISTS catalog_tier_capture_delete`,
		// A new row behind the cursor is library tier (the sync, a
		// promotion, a franchise part): it is copied.
		`CREATE TRIGGER catalog_tier_capture_insert AFTER INSERT ON catalog_titles WHEN new.key<=`+tierCursorSQL+` BEGIN `+insert+upsert+` END`,
		// An update behind the cursor follows a copied row, and copies a
		// row Get has just opened (detail=1): a touch while the job runs.
		`CREATE TRIGGER catalog_tier_capture_update AFTER UPDATE ON catalog_titles WHEN new.key<=`+tierCursorSQL+` OR old.key<=`+tierCursorSQL+` BEGIN
 DELETE FROM `+tierKeepTable+` WHERE key=old.key AND old.key<>new.key;
 `+insert+` AND new.key<=`+tierCursorSQL+` AND (new.detail=1 OR EXISTS (SELECT 1 FROM `+tierKeepTable+` k WHERE k.key=new.key))`+upsert+`
END`,
		`CREATE TRIGGER catalog_tier_capture_delete AFTER DELETE ON catalog_titles WHEN old.key<=`+tierCursorSQL+` BEGIN DELETE FROM `+tierKeepTable+` WHERE key=old.key; END`,
	)
	// Every index goes on the empty copy now, under a temporary name: the
	// schema never changes again while the capture triggers run (a schema
	// change under them failed concurrent writes with "no such table").
	for _, d := range defs {
		q, err := retargetIndex(d.SQL, tierTempIndex+d.Name, tierKeepTable)
		if err != nil {
			return err
		}
		stmts = append(stmts, `DROP INDEX IF EXISTS `+tierTempIndex+d.Name, q)
	}
	for _, q := range stmts {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("catalog: tier migration: prepare copy: %w (%.120s)", err, q)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_meta(k,v) VALUES(?,'0') ON CONFLICT(k) DO UPDATE SET v='0'`, tierMetaCursor); err != nil {
		return err
	}
	if err := c.setTierPhase(ctx, tx, "copy"); err != nil {
		return err
	}
	return tx.Commit()
}

// tierCopyBatch copies the library-tier rows of the next limit rows after
// the cursor and moves the cursor past them, in one transaction. It
// reports the rows read and whether any were.
func (c *Catalog) tierCopyBatch(ctx context.Context, tables map[string]bool, cols []string, limit int, touched *tierTouched, p *TierMigration) (int, bool, error) {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback()
	var cursorRaw string
	if err := tx.QueryRowContext(ctx, `SELECT v FROM catalog_meta WHERE k=?`, tierMetaCursor).Scan(&cursorRaw); err != nil {
		return 0, false, err
	}
	cursor, _ := strconv.ParseInt(cursorRaw, 10, 64)
	rows, err := tx.QueryContext(ctx, `SELECT key,type,show_key,popularity,vote_count,detail,collection_id,released_ts FROM catalog_titles WHERE key>? ORDER BY key LIMIT ?`, cursor, limit)
	if err != nil {
		return 0, false, err
	}
	var page []tierRow
	for rows.Next() {
		var r tierRow
		var key, show int64
		var detail int
		if err := rows.Scan(&key, &r.typ, &show, &r.popularity, &r.votes, &detail, &r.collectionID, &r.releasedTS); err != nil {
			rows.Close()
			return 0, false, err
		}
		r.key, r.showKey, r.detail = core.RatingKey(key), core.RatingKey(show), detail != 0
		page = append(page, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, false, err
	}
	if len(page) == 0 {
		return 0, false, nil
	}
	if err := recheckTierA(ctx, tx, tables, tierPageRanges(page), touched); err != nil {
		return 0, false, err
	}
	minPop, minVotes := tierBars(c.config())
	now := c.now()
	keptShows := map[core.RatingKey]bool{}
	var keep []any
	var childShows []any
	childOf := map[core.RatingKey][]core.RatingKey{}
	for _, r := range page {
		switch r.typ {
		case string(core.TypeMovie), string(core.TypeShow):
			if c.tierKeepsOwnFacts(r, minPop, minVotes, now, touched) {
				keep = append(keep, int64(r.key))
				if r.typ == string(core.TypeShow) {
					keptShows[r.key] = true
				}
			}
		case string(core.TypeSeason), string(core.TypeEpisode):
			show := r.showKey
			if show == 0 {
				show = core.ShowKey(r.key.TMDB())
			}
			if keptShows[show] {
				keep = append(keep, int64(r.key))
				continue
			}
			if _, ok := childOf[show]; !ok {
				childShows = append(childShows, int64(show))
			}
			childOf[show] = append(childOf[show], r.key)
		default:
			keep = append(keep, int64(r.key))
		}
	}
	if len(childShows) > 0 {
		// A season or episode stays with its show: kept when the show was
		// copied, or when there is no show row at all (nothing to collapse
		// it into).
		inKeep, err := keysIn(ctx, tx, tierKeepTable, childShows)
		if err != nil {
			return 0, false, err
		}
		inOld, err := keysIn(ctx, tx, "catalog_titles", childShows)
		if err != nil {
			return 0, false, err
		}
		for show, children := range childOf {
			if inKeep[show] || !inOld[show] {
				for _, k := range children {
					keep = append(keep, int64(k))
				}
			}
		}
	}
	list := strings.Join(cols, ",")
	for start := 0; start < len(keep); start += 500 {
		chunk := keep[start:min(start+500, len(keep))]
		if _, err := tx.ExecContext(ctx, `INSERT INTO `+tierKeepTable+`(`+list+`) SELECT `+list+` FROM catalog_titles WHERE key IN (`+placeholders(len(chunk))+`) ON CONFLICT(key) DO NOTHING`, chunk...); err != nil {
			return 0, false, fmt.Errorf("catalog: tier migration: copy: %w", err)
		}
	}
	last := page[len(page)-1].key
	if _, err := tx.ExecContext(ctx, `UPDATE catalog_meta SET v=? WHERE k=?`, strconv.FormatInt(int64(last), 10), tierMetaCursor); err != nil {
		return 0, false, err
	}
	next := *p
	next.Removed += len(page) - len(keep)
	if err := c.saveTierProgress(ctx, tx, next); err != nil {
		return 0, false, err
	}
	if err := tx.Commit(); err != nil {
		return 0, false, err
	}
	*p = next
	return len(page), true, nil
}

// tierPageRanges are the key ranges a copy page's touches can fall in:
// its own range, and its shows' seasons and episodes.
func tierPageRanges(page []tierRow) []keyRange {
	out := []keyRange{{page[0].key, page[len(page)-1].key}}
	var lo, hi int64 = math.MaxInt64, 0
	for _, r := range page {
		if r.key.Kind() == core.KindShow {
			lo, hi = min(lo, r.key.TMDB()), max(hi, r.key.TMDB())
		}
	}
	if hi > 0 {
		out = append(out,
			keyRange{core.SeasonKey(lo, 0), core.SeasonKey(hi, 9999)},
			keyRange{core.EpisodeKey(lo, 0, 0), core.EpisodeKey(hi, 999, 9999)})
	}
	return out
}

func keysIn(ctx context.Context, tx *sql.Tx, table string, keys []any) (map[core.RatingKey]bool, error) {
	out := map[core.RatingKey]bool{}
	for start := 0; start < len(keys); start += 500 {
		chunk := keys[start:min(start+500, len(keys))]
		rows, err := tx.QueryContext(ctx, `SELECT key FROM `+table+` WHERE key IN (`+placeholders(len(chunk))+`)`, chunk...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var k int64
			if err := rows.Scan(&k); err != nil {
				rows.Close()
				return nil, err
			}
			out[core.RatingKey(k)] = true
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// tierIndexDef is one of catalog_titles' indexes as it stood before the
// swap.
type tierIndexDef struct {
	Name string `json:"name"`
	SQL  string `json:"sql"`
}

// tierIndexDefs are catalog_titles' explicit indexes. The first call reads
// them from the schema and stores them; later calls (after the swap, when
// the schema holds temporary names) read the stored list.
func (c *Catalog) tierIndexDefs(ctx context.Context) ([]tierIndexDef, error) {
	if raw := c.getMeta(ctx, tierMetaIndex); raw != "" {
		var defs []tierIndexDef
		if err := json.Unmarshal([]byte(raw), &defs); err != nil {
			return nil, err
		}
		return defs, nil
	}
	rows, err := c.db.QueryContext(ctx, `SELECT name, sql FROM sqlite_master WHERE type='index' AND tbl_name='catalog_titles' AND sql IS NOT NULL ORDER BY name`)
	if err != nil {
		return nil, err
	}
	var defs []tierIndexDef
	for rows.Next() {
		var d tierIndexDef
		if err := rows.Scan(&d.Name, &d.SQL); err != nil {
			rows.Close()
			return nil, err
		}
		defs = append(defs, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(defs)
	if err != nil {
		return nil, err
	}
	return defs, c.setMeta(ctx, tierMetaIndex, string(raw))
}

// tierSwap puts the copy in catalog_titles' place in one transaction.
func (c *Catalog) tierSwap(ctx context.Context, p *TierMigration) error {
	if !c.waitTierUnpaused(ctx, p) {
		return ctx.Err()
	}
	if err := c.tierWALBudget(ctx); err != nil {
		return err
	}
	// The pinned indexes are rebuilt under their names after the swap;
	// until then queries go unpinned (indexedBy), from a moment before the
	// swap so no query naming one is in flight when it moves.
	if err := c.setTierHintsOff(ctx, true); err != nil {
		return err
	}
	if !waitGap(ctx, tierHintGrace) {
		return ctx.Err()
	}
	conn, err := c.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	// Renaming with legacy_alter_table leaves the other tables' triggers
	// naming catalog_titles alone, so they follow the name to the copy.
	if _, err := conn.ExecContext(ctx, `PRAGMA legacy_alter_table=ON`); err != nil {
		return err
	}
	defer conn.ExecContext(context.WithoutCancel(ctx), `PRAGMA legacy_alter_table=OFF`)
	began := time.Now()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	tables, err := existingTables(ctx, tx, store.SQLite)
	if err != nil {
		return err
	}
	// A last Tier A read over every key: a title touched since its page
	// was copied (an import, a watchlist sync) joins the copy now.
	late := newTierTouched()
	lateBegan := time.Now()
	if err := recheckTierA(ctx, tx, tables, []keyRange{{core.MovieKey(0), core.EpisodeKey(99_999_999, 999, 9999)}}, late); err != nil {
		return err
	}
	if err := c.tierCopyLate(ctx, tx, late); err != nil {
		return err
	}
	lateTook := time.Since(lateBegan)
	type trig struct{ name, sql string }
	var saved []trig
	rows, err := tx.QueryContext(ctx, `SELECT name, sql FROM sqlite_master WHERE type='trigger' AND tbl_name IN ('catalog_titles', ?) ORDER BY name`, tierKeepTable)
	if err != nil {
		return err
	}
	var drops []string
	for rows.Next() {
		var t trig
		if err := rows.Scan(&t.name, &t.sql); err != nil {
			rows.Close()
			return err
		}
		drops = append(drops, `DROP TRIGGER "`+t.name+`"`)
		if !strings.HasPrefix(t.name, "catalog_tier_capture_") && !strings.HasPrefix(t.name, tierTempIndex) {
			saved = append(saved, t)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	stmts := append(drops,
		`ALTER TABLE catalog_titles RENAME TO `+tierOldTable,
		`ALTER TABLE `+tierKeepTable+` RENAME TO catalog_titles`,
		`ALTER TABLE catalog_titles_fts RENAME TO `+tierOldFTS,
		`ALTER TABLE `+tierKeepFTS+` RENAME TO catalog_titles_fts`,
	)
	for _, t := range saved {
		stmts = append(stmts, t.sql)
	}
	slow := map[string]string{}
	for _, q := range stmts {
		b := time.Now()
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("catalog: tier migration: swap: %w (%.120s)", err, q)
		}
		if d := time.Since(b); d >= 100*time.Millisecond {
			slow[fmt.Sprintf("%.60s", q)] = d.Round(time.Millisecond).String()
		}
	}
	tallyBegan := time.Now()
	if err := rebuildBrowseTally(ctx, tx); err != nil {
		return err
	}
	if err := rebuildStatsTally(ctx, tx); err != nil {
		return err
	}
	tallyTook := time.Since(tallyBegan)
	if err := c.setTierPhase(ctx, tx, "cleanup"); err != nil {
		return err
	}
	if err := c.saveTierProgress(ctx, tx, *p); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	c.writeGen.Add(1)
	c.log.Info("catalog: tier migration swapped in the library tier", "hold", time.Since(began).Round(time.Millisecond), "late_touched", len(late.keys), "slow", slow, "late_check", lateTook.Round(time.Millisecond), "tallies", tallyTook.Round(time.Millisecond))
	return nil
}

// tierCopyLate copies into the copy the touched titles it lacks, with
// their seasons and episodes.
func (c *Catalog) tierCopyLate(ctx context.Context, tx *sql.Tx, late *tierTouched) error {
	if len(late.keys) == 0 {
		return nil
	}
	keys := make([]any, 0, len(late.keys))
	for k := range late.keys {
		keys = append(keys, int64(k))
	}
	have, err := keysIn(ctx, tx, tierKeepTable, keys)
	if err != nil {
		return err
	}
	var missing []any
	for _, k := range keys {
		if !have[core.RatingKey(k.(int64))] {
			missing = append(missing, k)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	cols, err := titleColumns(ctx, tx, "catalog_titles")
	if err != nil {
		return err
	}
	list := strings.Join(cols, ",")
	for start := 0; start < len(missing); start += 500 {
		chunk := missing[start:min(start+500, len(missing))]
		in := placeholders(len(chunk))
		if _, err := tx.ExecContext(ctx, `INSERT INTO `+tierKeepTable+`(`+list+`) SELECT `+list+` FROM catalog_titles WHERE key IN (`+in+`) OR (show_key IN (`+in+`) AND type IN ('season','episode')) ON CONFLICT(key) DO NOTHING`, append(append([]any{}, chunk...), chunk...)...); err != nil {
			return err
		}
	}
	return nil
}

// tierCleanup gives every index its real name again and drops the old
// table and FTS table, in bounded transactions.
func (c *Catalog) tierCleanup(ctx context.Context, p *TierMigration) error {
	defs, err := c.tierIndexDefs(ctx)
	if err != nil {
		return err
	}
	// The pinned indexes first: queries are unpinned until they exist.
	ordered := make([]tierIndexDef, 0, len(defs))
	for _, pinned := range []bool{true, false} {
		for _, d := range defs {
			if tierPinnedIndexes[d.Name] == pinned {
				ordered = append(ordered, d)
			}
		}
	}
	for i, d := range ordered {
		if i > 0 && tierPinnedIndexes[ordered[i-1].Name] && !tierPinnedIndexes[d.Name] {
			if err := c.setTierHintsOff(ctx, false); err != nil {
				return err
			}
		}
		if !c.waitTierUnpaused(ctx, p) {
			return ctx.Err()
		}
		if err := c.tierWALBudget(ctx); err != nil {
			return err
		}
		temp := tierTempIndex + d.Name
		var exists int
		if err := c.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, temp).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			continue // done before a restart
		}
		q, err := retargetIndex(d.SQL, d.Name, "catalog_titles")
		if err != nil {
			return err
		}
		tx, err := c.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		for _, s := range []string{`DROP INDEX IF EXISTS "` + d.Name + `"`, q, `DROP INDEX "` + temp + `"`} {
			if _, err := tx.ExecContext(ctx, s); err != nil {
				tx.Rollback()
				return fmt.Errorf("catalog: tier migration: rename index %s: %w", d.Name, err)
			}
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	if err := c.setTierHintsOff(ctx, false); err != nil {
		return err
	}
	if _, err := c.db.ExecContext(ctx, `DROP TABLE IF EXISTS `+tierOldFTS); err != nil {
		return err
	}
	// The old table has no index or trigger left. Dropping it frees its
	// pages (a few trunk pages of WAL) but reads every one under the
	// writer lock, so it is read first, outside it, and the drop walks
	// cached pages. Deleting its rows instead would write the whole table
	// to the WAL.
	for _, t := range []struct{ table, read string }{
		{tierOldFTS, `SELECT COUNT(*), COALESCE(SUM(LENGTH(block)),0) FROM ` + tierOldFTS + `_data`},
		{tierOldTable, `SELECT COUNT(*), COALESCE(SUM(LENGTH(summary)+LENGTH(title)),0) FROM ` + tierOldTable},
	} {
		if !c.waitTierUnpaused(ctx, p) {
			return ctx.Err()
		}
		var exists int
		if err := c.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, t.table).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			continue
		}
		var n, bytes int64
		_ = c.db.QueryRowContext(ctx, t.read).Scan(&n, &bytes)
		if err := c.tierWALBudget(ctx); err != nil {
			return err
		}
		began := time.Now()
		if _, err := c.db.ExecContext(ctx, `DROP TABLE `+t.table); err != nil {
			return fmt.Errorf("catalog: tier migration: drop %s: %w", t.table, err)
		}
		c.log.Info("catalog: tier migration dropped the old rows", "table", t.table, "rows", n, "hold", time.Since(began).Round(time.Millisecond))
	}
	if _, err := c.db.ExecContext(ctx, `PRAGMA analysis_limit=1000`); err == nil {
		_, _ = c.db.ExecContext(ctx, `ANALYZE catalog_titles`)
	}
	return c.setTierPhase(ctx, c.db, "orphans")
}

// tierDeleteOrphans deletes the dependent rows of titles the swap left
// out, a page of keys a transaction per table.
func (c *Catalog) tierDeleteOrphans(ctx context.Context, p *TierMigration) error {
	tables, err := existingTables(ctx, c.db, store.SQLite)
	if err != nil {
		return err
	}
	for _, d := range tierDependents {
		if !tables[d.table] {
			continue
		}
		var after int64
		for {
			if !c.waitTierUnpaused(ctx, p) {
				return ctx.Err()
			}
			if err := c.tierWALBudget(ctx); err != nil {
				return err
			}
			rows, err := c.db.QueryContext(ctx, `SELECT DISTINCT `+d.column+` FROM `+d.table+` WHERE `+d.column+`>? ORDER BY `+d.column+` LIMIT ?`, after, tierOrphanPage)
			if err != nil {
				return err
			}
			var keys []any
			for rows.Next() {
				var k int64
				if err := rows.Scan(&k); err != nil {
					rows.Close()
					return err
				}
				keys = append(keys, k)
				after = k
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return err
			}
			if len(keys) == 0 {
				break
			}
			tx, err := c.db.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			have, err := keysIn(ctx, tx, "catalog_titles", keys)
			if err != nil {
				tx.Rollback()
				return err
			}
			var gone []any
			for _, k := range keys {
				if !have[core.RatingKey(k.(int64))] {
					gone = append(gone, k)
				}
			}
			for start := 0; start < len(gone); start += 500 {
				chunk := gone[start:min(start+500, len(gone))]
				if _, err := tx.ExecContext(ctx, `DELETE FROM `+d.table+` WHERE `+d.column+` IN (`+placeholders(len(chunk))+`)`, chunk...); err != nil {
					tx.Rollback()
					return fmt.Errorf("catalog: tier migration: orphans in %s: %w", d.table, err)
				}
			}
			if err := tx.Commit(); err != nil {
				return err
			}
			if len(keys) < tierOrphanPage {
				break
			}
		}
	}
	c.writeGen.Add(1)
	return c.setTierPhase(ctx, c.db, "swept")
}

// setTierHintsOff turns INDEXED BY pins off (or on again) for this process
// and records it, so a restart in the middle keeps them off.
func (c *Catalog) setTierHintsOff(ctx context.Context, off bool) error {
	v := ""
	if off {
		v = "1"
		c.tier.hintsOff.Store(true)
	}
	if err := c.setMeta(ctx, tierMetaHints, v); err != nil {
		return err
	}
	c.tier.hintsOff.Store(off)
	return nil
}

// loadTierHints restores the pin switch a tier migration left off.
func (c *Catalog) loadTierHints(ctx context.Context) {
	c.tier.hintsOff.Store(c.getMeta(ctx, tierMetaHints) != "")
}

// tierWALBudget is the job's back-pressure between batches: the shared WAL
// budget, then a TRUNCATE checkpoint when the WAL file has grown past it
// (steady readers keep it from restarting on its own).
func (c *Catalog) tierWALBudget(ctx context.Context) error {
	if err := store.WaitWALBudget(ctx, c.db); err != nil {
		return err
	}
	_, err := store.TruncateWAL(ctx, c.db, tierTruncateWait)
	return err
}
