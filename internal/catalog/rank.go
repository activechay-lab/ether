package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/store"
)

// The popularity rank. TMDB moves almost every title's popularity every
// day, and an index ordered by popularity moves each changed title's entry
// to a random leaf: a daily refresh of a 127k-title library rewrote 5 GB of
// WAL through the nine catalog_titles indexes that ordered by popularity,
// and 61 MB without them.
//
// Popularity order therefore lives in a narrow rank table, one row per
// movie and show: the columns browse filters on and rk, the title's
// popularity negated as of the last rebuild, so that rk ascending, then
// key, is most popular first with a deterministic tie order. There are two
// copies, catalog_rank_a and catalog_rank_b, and the view catalog_rank
// names the live one. A daily rebuild writes the idle copy in key order,
// builds its indexes in bulk and swaps the view in the same transaction,
// so a reader sees one copy or the other, never a mix.
//
// Triggers keep both copies current for every write that is not a
// popularity change: a new title gets its rank row (rk from its
// popularity) as it is stored, so it appears in popularity order at once;
// a title whose type, browsability, adult flag, release, year, language,
// runtime or status changes has both rows updated; a deleted title loses
// both. A popularity change alone writes nothing here: the next rebuild
// picks it up.

const rankColumns = `key, type, browsable, adult, release_known, released_ts, year, language, runtime_sec, status, rk`

func rankTableSQL(side string, bigint string) string {
	t := "catalog_rank_" + side
	return `CREATE TABLE ` + t + ` (key ` + bigint + ` PRIMARY KEY, type TEXT NOT NULL, browsable INTEGER NOT NULL, adult INTEGER NOT NULL,
 release_known INTEGER NOT NULL, released_ts ` + bigint + ` NOT NULL, year INTEGER NOT NULL, language TEXT NOT NULL,
 runtime_sec INTEGER NOT NULL, status TEXT NOT NULL, rk REAL NOT NULL);`
}

// rankIndexSQL is a copy's indexes: popularity order alone and within a
// year, language, runtime or status, each covering the released check.
func rankIndexSQL(side string) string {
	t := "catalog_rank_" + side
	return `CREATE INDEX ` + t + `_pop ON ` + t + `(type, browsable, adult, rk, release_known, released_ts);
CREATE INDEX ` + t + `_year ON ` + t + `(type, browsable, adult, year, rk);
CREATE INDEX ` + t + `_lang ON ` + t + `(type, browsable, adult, language, rk);
CREATE INDEX ` + t + `_runtime ON ` + t + `(type, browsable, adult, runtime_sec, rk);
CREATE INDEX ` + t + `_status ON ` + t + `(type, browsable, adult, status, rk);`
}

const rankFill = `SELECT key, type, browsable, adult, release_known, released_ts, year, language, runtime_sec, status, -popularity FROM catalog_titles WHERE type IN ('movie','show')`

// rankUpsert writes src's rank row into one copy when src is a movie or
// show, keeping that copy's rk (a popularity change waits for the rebuild).
func rankUpsert(side, src string) string {
	t := "catalog_rank_" + side
	return `INSERT INTO ` + t + `(` + rankColumns + `) SELECT ` + src + `.key, ` + src + `.type, ` + src + `.browsable, ` + src + `.adult,
 CASE WHEN ` + src + `.released_ts<>0 THEN 1 ELSE 0 END, ` + src + `.released_ts, ` + src + `.year, ` + src + `.language, ` + src + `.runtime_sec, ` + src + `.status, -` + src + `.popularity
 WHERE ` + src + `.type IN ('movie','show')
 ON CONFLICT(key) DO UPDATE SET type=excluded.type, browsable=excluded.browsable, adult=excluded.adult, release_known=excluded.release_known,
 released_ts=excluded.released_ts, year=excluded.year, language=excluded.language, runtime_sec=excluded.runtime_sec, status=excluded.status;`
}

// rankWatched is every catalog_titles column a rank row copies, popularity
// excepted.
const rankWatched = `type, browsable, adult, released_ts, year, language, runtime_sec, status`

func init() {
	sqlite := rankTableSQL("a", "INTEGER") + rankIndexSQL("a") + rankTableSQL("b", "INTEGER") + rankIndexSQL("b") +
		`CREATE VIEW catalog_rank AS SELECT ` + rankColumns + ` FROM catalog_rank_a;
CREATE TRIGGER catalog_rank_insert AFTER INSERT ON catalog_titles WHEN new.type IN ('movie','show') BEGIN
 ` + rankUpsert("a", "new") + `
 ` + rankUpsert("b", "new") + `
END;
CREATE TRIGGER catalog_rank_update AFTER UPDATE OF ` + rankWatched + ` ON catalog_titles WHEN new.type IN ('movie','show') OR old.type IN ('movie','show') BEGIN
 DELETE FROM catalog_rank_a WHERE key=old.key AND new.type NOT IN ('movie','show');
 DELETE FROM catalog_rank_b WHERE key=old.key AND new.type NOT IN ('movie','show');
 ` + rankUpsert("a", "new") + `
 ` + rankUpsert("b", "new") + `
END;
CREATE TRIGGER catalog_rank_delete AFTER DELETE ON catalog_titles WHEN old.type IN ('movie','show') BEGIN
 DELETE FROM catalog_rank_a WHERE key=old.key;
 DELETE FROM catalog_rank_b WHERE key=old.key;
END;
INSERT INTO catalog_meta(k,v) VALUES('rank_side','a') ON CONFLICT(k) DO UPDATE SET v=excluded.v;`
	store.Register("catalog", 53, sqlite)
	// PostgreSQL: one row trigger function writes both copies. The view
	// swap is CREATE OR REPLACE VIEW (an ACCESS EXCLUSIVE lock on the view
	// only, held to the end of the rebuild's transaction).
	pgUpsert := func(side string) string {
		t := "catalog_rank_" + side
		return `INSERT INTO ` + t + `(` + rankColumns + `) VALUES (NEW.key, NEW.type, NEW.browsable, NEW.adult,
  CASE WHEN NEW.released_ts<>0 THEN 1 ELSE 0 END, NEW.released_ts, NEW.year, NEW.language, NEW.runtime_sec, NEW.status, -NEW.popularity)
  ON CONFLICT(key) DO UPDATE SET type=excluded.type, browsable=excluded.browsable, adult=excluded.adult, release_known=excluded.release_known,
  released_ts=excluded.released_ts, year=excluded.year, language=excluded.language, runtime_sec=excluded.runtime_sec, status=excluded.status;`
	}
	store.RegisterDialect("catalog", 53, store.Postgres, rankTableSQL("a", "BIGINT")+rankIndexSQL("a")+rankTableSQL("b", "BIGINT")+rankIndexSQL("b")+
		`CREATE VIEW catalog_rank AS SELECT `+rankColumns+` FROM catalog_rank_a;
CREATE FUNCTION catalog_rank_fn() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF TG_OP='DELETE' OR (TG_OP='UPDATE' AND NEW.type NOT IN ('movie','show')) THEN
  DELETE FROM catalog_rank_a WHERE key=OLD.key;
  DELETE FROM catalog_rank_b WHERE key=OLD.key;
 END IF;
 IF TG_OP<>'DELETE' AND NEW.type IN ('movie','show') THEN
  `+pgUpsert("a")+`
  `+pgUpsert("b")+`
 END IF;
 RETURN NULL;
END $$;
CREATE TRIGGER catalog_rank_insert AFTER INSERT ON catalog_titles FOR EACH ROW WHEN (NEW.type IN ('movie','show')) EXECUTE FUNCTION catalog_rank_fn();
CREATE TRIGGER catalog_rank_update AFTER UPDATE OF `+rankWatched+` ON catalog_titles FOR EACH ROW WHEN (NEW.type IN ('movie','show') OR OLD.type IN ('movie','show')) EXECUTE FUNCTION catalog_rank_fn();
CREATE TRIGGER catalog_rank_delete AFTER DELETE ON catalog_titles FOR EACH ROW WHEN (OLD.type IN ('movie','show')) EXECUTE FUNCTION catalog_rank_fn();
INSERT INTO catalog_meta(k,v) VALUES('rank_side','a') ON CONFLICT(k) DO UPDATE SET v=excluded.v;`)
}

// rankRebuildEvery spaces the rank rebuilds: TMDB publishes popularity
// once a day.
const rankRebuildEvery = 24 * time.Hour

// RankRebuild is what one rebuild did.
type RankRebuild struct {
	Side  string
	Rows  int64
	Took  time.Duration
	Built time.Time
}

// RebuildRank refreshes every title's rank from its current popularity:
// it rebuilds the idle copy and makes it the live one.
func (c *Catalog) RebuildRank(ctx context.Context) (RankRebuild, error) {
	start := c.now()
	began := time.Now()
	var out RankRebuild
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	pg := store.DialectOf(c.db) == store.Postgres
	if pg {
		// One rebuild at a time across a cluster's nodes.
		if err := store.LockKey(ctx, store.Postgres, tx, "catalog-rank-rebuild"); err != nil {
			return out, err
		}
	}
	live := "a"
	var v string
	if err := tx.QueryRowContext(ctx, `SELECT v FROM catalog_meta WHERE k='rank_side'`).Scan(&v); err == nil && v == "b" {
		live = "b"
	}
	idle := map[string]string{"a": "b", "b": "a"}[live]
	t := "catalog_rank_" + idle
	bigint := "INTEGER"
	if pg {
		bigint = "BIGINT"
	}
	stmts := []string{`DROP TABLE IF EXISTS ` + t, strings.TrimSuffix(rankTableSQL(idle, bigint), ";"),
		`INSERT INTO ` + t + `(` + rankColumns + `) ` + rankFill + ` ORDER BY key`}
	stmts = append(stmts, strings.Split(strings.TrimSuffix(rankIndexSQL(idle), ";"), ";\n")...)
	if !pg {
		// One transaction: the write gate is held for the rebuild, and a
		// reader's snapshot holds the old view or the new one.
		stmts = append(stmts, `DROP VIEW IF EXISTS catalog_rank`, `CREATE VIEW catalog_rank AS SELECT `+rankColumns+` FROM `+t)
	}
	for _, s := range stmts {
		res, err := tx.ExecContext(ctx, s)
		if err != nil {
			return out, fmt.Errorf("catalog: rank rebuild: %w", err)
		}
		if strings.HasPrefix(s, "INSERT") {
			out.Rows, _ = res.RowsAffected()
		}
	}
	if !pg {
		if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_meta(k,v) VALUES('rank_side',?),('rank_built',?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, idle, fmt.Sprint(start.Unix())); err != nil {
			return out, err
		}
	}
	if _, err := tx.ExecContext(ctx, `ANALYZE `+t); err != nil {
		return out, err
	}
	if err := tx.Commit(); err != nil {
		return out, err
	}
	if pg {
		// The swap is its own short transaction: CREATE OR REPLACE VIEW
		// takes an ACCESS EXCLUSIVE lock on the view, which waits for the
		// rank's current readers and makes new ones queue behind it. A
		// lock timeout keeps a long reader from building a queue; the copy
		// is complete and current (both copies take every trigger write),
		// so a later attempt swaps the same way.
		if err := c.swapRankViewPG(ctx, t); err != nil {
			return out, err
		}
	}
	c.rankReady.Store(true)
	c.writeGen.Add(1)
	out.Side, out.Took, out.Built = idle, time.Since(began), start
	return out, nil
}

// rankDue reports whether the rank has not been rebuilt within
// rankRebuildEvery (or ever).
func (c *Catalog) rankDue(ctx context.Context) bool {
	var v string
	var at int64
	if err := c.db.QueryRowContext(ctx, `SELECT v FROM catalog_meta WHERE k='rank_built'`).Scan(&v); err != nil {
		return true
	}
	if _, err := fmt.Sscan(v, &at); err != nil {
		return true
	}
	return c.now().Sub(time.Unix(at, 0)) >= rankRebuildEvery
}

// rankLoop rebuilds the rank when it is due: at start on a database that
// never had one built (an upgrade fills it here, outside the migration
// transaction), then daily.
func (c *Catalog) rankLoop(ctx context.Context) {
	for {
		if c.rankDue(ctx) {
			if r, err := c.RebuildRank(ctx); err != nil {
				if ctx.Err() == nil {
					c.log.Warn("catalog: rebuild popularity rank", "err", err)
				}
			} else {
				c.log.Info("catalog: popularity rank rebuilt", "titles", r.Rows, "took", r.Took.Round(time.Millisecond))
			}
		}
		if err := c.swapTitleIndexes(ctx); err != nil && ctx.Err() == nil {
			c.log.Warn("catalog: replace popularity indexes", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Hour):
		}
	}
}

// loadRankReady reads whether a rank has ever been built.
func (c *Catalog) loadRankReady(ctx context.Context) {
	var v string
	if err := c.db.QueryRowContext(ctx, `SELECT v FROM catalog_meta WHERE k='rank_built'`).Scan(&v); err == nil {
		c.rankReady.Store(true)
	} else if err != sql.ErrNoRows {
		c.log.Debug("catalog: read rank state", "err", err)
	}
}

func (c *Catalog) swapRankViewPG(ctx context.Context, table string) error {
	side := strings.TrimPrefix(table, "catalog_rank_")
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		err = func() error {
			tx, err := c.db.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			defer tx.Rollback()
			for _, s := range []string{`SET LOCAL lock_timeout = '2s'`, `CREATE OR REPLACE VIEW catalog_rank AS SELECT ` + rankColumns + ` FROM ` + table} {
				if _, err := tx.ExecContext(ctx, s); err != nil {
					return err
				}
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_meta(k,v) VALUES('rank_side',?),('rank_built',?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, side, fmt.Sprint(c.now().Unix())); err != nil {
				return err
			}
			return tx.Commit()
		}()
		if err == nil || ctx.Err() != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(attempt+1) * time.Second):
		}
	}
	return fmt.Errorf("catalog: swap rank view: %w", err)
}
