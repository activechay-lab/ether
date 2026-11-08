package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/braedonsaunders/ether/internal/store"
)

// The catalogue's join tables are keyed by their whole row. On SQLite a
// rowid table with a composite primary key stores every row twice: once in
// the table and once in its primary-key index. WITHOUT ROWID stores it once,
// in primary-key order, which is also the order every write and per-title
// read uses. On the lab catalogue that is 98 MB of 186 MB for credits.
//
// Four indexes no statement uses are dropped on both engines:
//   - catalog_credits_person(person_tmdb,key): catalog_credits_person_kind
//     (person_tmdb,kind,key) answers every person_tmdb lookup;
//   - catalog_keywords_keyword(keyword,key): every keyword filter matches
//     LOWER(keyword) (catalog_keywords_console_lower);
//   - catalog_genre_pairs_title(key): the pair triggers now delete a
//     title's pairs by primary-key seeks;
//   - catalog_titles_episode_global_aired: catalog_titles_episode_recent
//     (released_ts DESC,key,show_key over the same aired episodes) serves
//     every statement it could.
//
// The person-name trigger no longer rewrites the name on every credit
// insert (it rewrote the name index entry even when the name was
// unchanged); it counts the credit and renames only when the name differs.
var slimTables = []string{
	"catalog_credits", "catalog_genres", "catalog_genre_pairs", "catalog_keywords",
	"catalog_studios", "catalog_countries", "catalog_release_names", "catalog_home_release_dates",
}

var slimDroppedIndexes = []string{
	"catalog_credits_person", "catalog_keywords_keyword", "catalog_genre_pairs_title", "catalog_titles_episode_global_aired",
}

// genrePairsRemove deletes the pairs a genre takes part in. Before the
// delete the title still has the genre, so its pairs are exactly the title's
// genres crossed with it, each a primary-key seek.
const genrePairsRemove = `DELETE FROM catalog_genre_pairs WHERE a=OLD.genre AND b IN (SELECT genre FROM catalog_genres WHERE key=OLD.key) AND key=OLD.key;
DELETE FROM catalog_genre_pairs WHERE b=OLD.genre AND a IN (SELECT genre FROM catalog_genres WHERE key=OLD.key) AND key=OLD.key;`

const personNamesCreditInsertSQLite = `CREATE TRIGGER catalog_person_names_credit_insert AFTER INSERT ON catalog_credits WHEN new.person_tmdb>0 BEGIN
 INSERT INTO catalog_person_names(person_tmdb, name, credits) VALUES (new.person_tmdb, new.name, 1)
 ON CONFLICT(person_tmdb) DO UPDATE SET credits=catalog_person_names.credits+1;
 UPDATE catalog_person_names SET name=new.name WHERE person_tmdb=new.person_tmdb AND new.name<>'' AND name<>new.name;
END;`

func init() {
	var drops strings.Builder
	for _, ix := range slimDroppedIndexes {
		drops.WriteString("DROP INDEX IF EXISTS " + ix + ";\n")
	}
	store.Register("catalog", 47, `SELECT 1;`)
	store.RegisterSQLiteApply("catalog", 47, slimCatalogSQLite)
	store.RegisterDialect("catalog", 47, store.Postgres, `
CREATE OR REPLACE FUNCTION catalog_genre_pairs_remove() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
IF TG_OP='UPDATE' THEN
 PERFORM pg_advisory_xact_lock(hashtextextended('ether:catalog-genres:' || LEAST(OLD.key,NEW.key)::text,0));
 IF OLD.key<>NEW.key THEN PERFORM pg_advisory_xact_lock(hashtextextended('ether:catalog-genres:' || GREATEST(OLD.key,NEW.key)::text,0)); END IF;
ELSE
 PERFORM pg_advisory_xact_lock(hashtextextended('ether:catalog-genres:' || OLD.key::text,0));
END IF;
`+genrePairsRemove+` IF TG_OP='DELETE' THEN RETURN OLD; END IF; RETURN NEW; END $$;
CREATE OR REPLACE FUNCTION catalog_person_names_credit_fn() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF TG_OP = 'INSERT' THEN
  IF NEW.person_tmdb > 0 THEN
   INSERT INTO catalog_person_names(person_tmdb, name, credits) VALUES (NEW.person_tmdb, NEW.name, 1)
   ON CONFLICT(person_tmdb) DO UPDATE SET credits=catalog_person_names.credits+1;
   IF NEW.name<>'' THEN
    UPDATE catalog_person_names SET name=NEW.name WHERE person_tmdb=NEW.person_tmdb AND name<>NEW.name;
   END IF;
  END IF;
 ELSIF TG_OP = 'DELETE' THEN
  IF OLD.person_tmdb > 0 THEN
   UPDATE catalog_person_names SET credits=credits-1 WHERE person_tmdb=OLD.person_tmdb;
  END IF;
 ELSIF NEW.person_tmdb > 0 AND NEW.name<>'' AND OLD.name IS DISTINCT FROM NEW.name THEN
  UPDATE catalog_person_names SET name=NEW.name WHERE person_tmdb=NEW.person_tmdb;
 END IF;
 RETURN NULL;
END $$;
`+drops.String())

	// FTS5 options: no statement ranks matches (bm25 needs the per-row
	// sizes columnsize keeps), titles match per column (detail=column), and
	// person names match word prefixes only (detail=none). On the lab
	// catalogue the two indexes shrink from 35 MB to 18 MB with the same
	// matches. The alternative-title index stays as it is: detail=column
	// saved 1.4 MB there but its writes cost 14.7 WAL frames per detailed
	// title instead of 5.6.
	store.Register("catalog", 48, `SELECT 1;`)
	store.RegisterSQLiteApply("catalog", 48, func(ctx context.Context, tx *sql.Tx) error {
		for _, stmt := range []string{
			`DROP TABLE IF EXISTS catalog_titles_fts`,
			`CREATE VIRTUAL TABLE catalog_titles_fts USING fts5(title, original_title, content='catalog_titles', content_rowid='key', tokenize='trigram', detail=column, columnsize=0)`,
			`INSERT INTO catalog_titles_fts(catalog_titles_fts) VALUES('rebuild')`,
			`DROP TABLE IF EXISTS catalog_person_names_fts`,
			`CREATE VIRTUAL TABLE catalog_person_names_fts USING fts5(name, content='catalog_person_names', content_rowid='person_tmdb', tokenize='unicode61 remove_diacritics 0', prefix='1 2 3', detail=none, columnsize=0)`,
			`INSERT INTO catalog_person_names_fts(catalog_person_names_fts) VALUES('rebuild')`,
		} {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("catalog: slim search indexes: %w", err)
			}
		}
		return nil
	})
}

func init() {
	// Returning series by when their details were fetched, so WarmDetails
	// reads only the stale ones.
	store.Register("catalog", 49, `CREATE INDEX IF NOT EXISTS catalog_titles_warm_stale ON catalog_titles(detail_at) WHERE type='show' AND status='Returning Series' AND detail=1 AND browsable=1 AND adult=0;`)
}

func slimCatalogSQLite(ctx context.Context, tx *sql.Tx) error {
	drop := map[string]bool{}
	for _, ix := range slimDroppedIndexes {
		drop[ix] = true
	}
	for i, table := range slimTables {
		store.WorkProgress(ctx, "catalog_without_rowid", i, len(slimTables))
		if err := rebuildWithoutRowid(ctx, tx, table, drop); err != nil {
			return fmt.Errorf("catalog: %s without rowid: %w", table, err)
		}
	}
	store.WorkProgress(ctx, "catalog_without_rowid", len(slimTables), len(slimTables))
	for _, stmt := range []string{
		`DROP INDEX IF EXISTS catalog_titles_episode_global_aired`,
		`DROP TRIGGER IF EXISTS catalog_genre_pairs_delete`,
		`DROP TRIGGER IF EXISTS catalog_genre_pairs_update_before`,
		`CREATE TRIGGER catalog_genre_pairs_delete BEFORE DELETE ON catalog_genres BEGIN ` + genrePairsRemove + ` END`,
		`CREATE TRIGGER catalog_genre_pairs_update_before BEFORE UPDATE ON catalog_genres BEGIN ` + genrePairsRemove + ` END`,
		`DROP TRIGGER IF EXISTS catalog_person_names_credit_insert`,
		personNamesCreditInsertSQLite,
	} {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM sqlite_stat1 WHERE idx=?`, "catalog_titles_episode_global_aired")
	if err != nil && !strings.Contains(err.Error(), "no such table") {
		return err
	}
	return nil
}

// rebuildWithoutRowid copies table into a WITHOUT ROWID table with the same
// columns and primary key, then puts back every index (except drop) and
// trigger the table had, whichever component created them, and its planner
// statistics. A table already WITHOUT ROWID only loses the dropped indexes.
func rebuildWithoutRowid(ctx context.Context, tx *sql.Tx, table string, drop map[string]bool) error {
	var ddl string
	if err := tx.QueryRowContext(ctx, `SELECT sql FROM sqlite_schema WHERE type='table' AND name=?`, table).Scan(&ddl); err != nil {
		return err
	}
	type object struct{ name, sql string }
	objects := func(kind string) ([]object, error) {
		rows, err := tx.QueryContext(ctx, `SELECT name, sql FROM sqlite_schema WHERE type=? AND tbl_name=? AND sql IS NOT NULL ORDER BY rowid`, kind, table)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []object
		for rows.Next() {
			var o object
			if err := rows.Scan(&o.name, &o.sql); err != nil {
				return nil, err
			}
			out = append(out, o)
		}
		return out, rows.Err()
	}
	indexes, err := objects("index")
	if err != nil {
		return err
	}
	if strings.Contains(strings.ToUpper(ddl), "WITHOUT ROWID") {
		for _, ix := range indexes {
			if drop[ix.name] {
				if _, err := tx.ExecContext(ctx, `DROP INDEX "`+ix.name+`"`); err != nil {
					return err
				}
			}
		}
		return nil
	}
	triggers, err := objects("trigger")
	if err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT name, type, "notnull", dflt_value, pk FROM pragma_table_info(?) ORDER BY cid`, table)
	if err != nil {
		return err
	}
	var cols, defs []string
	pk := map[int]string{}
	for rows.Next() {
		var name, typ string
		var notNull, pkPos int
		var dflt sql.NullString
		if err := rows.Scan(&name, &typ, &notNull, &dflt, &pkPos); err != nil {
			rows.Close()
			return err
		}
		def := `"` + name + `" ` + typ
		if notNull != 0 {
			def += " NOT NULL"
		}
		if dflt.Valid {
			def += " DEFAULT " + dflt.String
		}
		cols, defs = append(cols, `"`+name+`"`), append(defs, def)
		if pkPos > 0 {
			pk[pkPos] = `"` + name + `"`
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(pk) < 2 {
		return fmt.Errorf("expected a composite primary key, have %d columns", len(pk))
	}
	keys := make([]string, len(pk))
	for pos, name := range pk {
		keys[pos-1] = name
	}
	// The planner's statistics carry over: the primary-key index keeps its
	// name and figures, the secondary indexes theirs.
	type stat struct {
		idx  sql.NullString
		stat string
	}
	var stats []stat
	if srows, err := tx.QueryContext(ctx, `SELECT idx, stat FROM sqlite_stat1 WHERE tbl=?`, table); err == nil {
		for srows.Next() {
			var s stat
			if err := srows.Scan(&s.idx, &s.stat); err != nil {
				srows.Close()
				return err
			}
			if !drop[s.idx.String] {
				stats = append(stats, s)
			}
		}
		srows.Close()
	}
	slim := table + "__slim"
	list := strings.Join(cols, ",")
	for _, stmt := range []string{
		`CREATE TABLE "` + slim + `" (` + strings.Join(defs, ", ") + `, PRIMARY KEY(` + strings.Join(keys, ",") + `)) WITHOUT ROWID`,
		`INSERT INTO "` + slim + `"(` + list + `) SELECT ` + list + ` FROM "` + table + `"`,
		`DROP TABLE "` + table + `"`,
		// Other tables' triggers name this table; the modern rename would
		// re-check them while it does not exist.
		`PRAGMA legacy_alter_table=ON`,
		`ALTER TABLE "` + slim + `" RENAME TO "` + table + `"`,
		`PRAGMA legacy_alter_table=OFF`,
	} {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("%s: %w", stmt, err)
		}
	}
	for _, ix := range indexes {
		if drop[ix.name] {
			continue
		}
		if _, err := tx.ExecContext(ctx, ix.sql); err != nil {
			return fmt.Errorf("index %s: %w", ix.name, err)
		}
	}
	for _, tr := range triggers {
		if _, err := tx.ExecContext(ctx, tr.sql); err != nil {
			return fmt.Errorf("trigger %s: %w", tr.name, err)
		}
	}
	for _, s := range stats {
		if _, err := tx.ExecContext(ctx, `INSERT INTO sqlite_stat1(tbl, idx, stat) VALUES(?,?,?)`, table, s.idx, s.stat); err != nil {
			return err
		}
	}
	return nil
}
