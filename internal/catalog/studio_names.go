package catalog

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// studioProbeMinTitles is the studio size past which a title-sorted page
// probes each title it walks instead of driving from the studio index. A
// studio index page must sort its every matching title to answer a
// title-sorted page, so its cost follows the studio's size (and so the
// library's); below this many titles that is a bounded, cheap sort, while
// at or above it a probe in title order stops at the page. Measured on a
// 540k-title library: a studio of 200 titles sorts in ~2 ms from its index
// but a probe walks the whole library (~100 ms+); a studio of 12k titles
// probes in ~1.5 ms but sorting reads them all (~18 ms, and ten times that
// on a ten-times library).
const studioProbeMinTitles = 256

// studioFilterTitles is how many titles the filter's positive studio
// predicate could match, from the studio vocabulary (catalog_studio_names),
// or -1 when it cannot be told cheaply (no positive studio predicate, or a
// subquery the vocabulary cannot sum). It decides studioProbe: a small
// studio is sorted from its own index, a large one probed in title order.
func (c *Catalog) studioFilterTitles(ctx context.Context, f core.Filter) int64 {
	names, args, ok := c.positiveStudioNames(f)
	if !ok {
		return -1
	}
	var n int64
	if err := c.db.QueryRowContext(ctx, `SELECT CAST(COALESCE(SUM(n.titles),0) AS BIGINT) FROM catalog_studio_names n WHERE n.studio IN (`+names+`)`, args...).Scan(&n); err != nil {
		return -1
	}
	return n
}

// A Plex studio filter ("studio=Universal") matches every title with a
// studio containing the value, case-insensitively, as PMS does. Asked of
// catalog_studios per title, that walked the library: a page read titles in
// sort order until it had a page of matches, and its total read them all.
//
// catalog_studio_names is the studio vocabulary: one row per stored studio
// name and how many titles carry it, kept by triggers on catalog_studios
// (on SQLite with a trigram index, as titles have). A filter first finds the
// matching names in the vocabulary, then the titles from catalog_studios'
// own (studio, key) index, so it reads the matches, not the library.
func init() {
	vocab := func(a, d string) string {
		return `INSERT INTO catalog_studio_names(studio,titles) VALUES (` + a + `studio,` + d + `) ON CONFLICT(studio) DO UPDATE SET titles=catalog_studio_names.titles+excluded.titles;`
	}
	store.Register("catalog", 51, `CREATE TABLE catalog_studio_names (studio TEXT PRIMARY KEY, titles INTEGER NOT NULL);
CREATE INDEX catalog_studio_names_lower ON catalog_studio_names(LOWER(studio));
INSERT INTO catalog_studio_names(studio,titles) SELECT studio, COUNT(*) FROM catalog_studios GROUP BY studio;
CREATE VIRTUAL TABLE catalog_studio_names_fts USING fts5(studio, content='catalog_studio_names', tokenize='trigram');
INSERT INTO catalog_studio_names_fts(catalog_studio_names_fts) VALUES('rebuild');
CREATE TRIGGER catalog_studio_names_fts_insert AFTER INSERT ON catalog_studio_names BEGIN
 INSERT INTO catalog_studio_names_fts(rowid, studio) VALUES (new.rowid, new.studio);
END;
CREATE TRIGGER catalog_studio_names_fts_delete AFTER DELETE ON catalog_studio_names BEGIN
 INSERT INTO catalog_studio_names_fts(catalog_studio_names_fts, rowid, studio) VALUES ('delete', old.rowid, old.studio);
END;
CREATE TRIGGER catalog_studio_names_insert AFTER INSERT ON catalog_studios BEGIN `+vocab("NEW.", "1")+` END;
CREATE TRIGGER catalog_studio_names_delete AFTER DELETE ON catalog_studios BEGIN `+vocab("OLD.", "-1")+` END;
CREATE TRIGGER catalog_studio_names_update AFTER UPDATE OF studio ON catalog_studios WHEN OLD.studio IS NOT NEW.studio BEGIN `+vocab("OLD.", "-1")+` `+vocab("NEW.", "1")+` END;`)
	store.RegisterDialect("catalog", 51, store.Postgres, `LOCK TABLE catalog_studios IN SHARE ROW EXCLUSIVE MODE;
CREATE TABLE catalog_studio_names (studio TEXT PRIMARY KEY, titles BIGINT NOT NULL);
CREATE INDEX catalog_studio_names_lower ON catalog_studio_names(LOWER(studio));
INSERT INTO catalog_studio_names(studio,titles) SELECT studio, COUNT(*) FROM catalog_studios GROUP BY studio;
CREATE FUNCTION catalog_studio_names() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
IF TG_OP<>'INSERT' THEN `+vocab("OLD.", "-1")+` END IF;
IF TG_OP<>'DELETE' THEN `+vocab("NEW.", "1")+` END IF;
RETURN NULL; END $$;
CREATE TRIGGER catalog_studio_names AFTER INSERT OR DELETE ON catalog_studios FOR EACH ROW EXECUTE FUNCTION catalog_studio_names();
CREATE TRIGGER catalog_studio_names_update AFTER UPDATE OF studio ON catalog_studios FOR EACH ROW WHEN (OLD.studio IS DISTINCT FROM NEW.studio) EXECUTE FUNCTION catalog_studio_names();
DO $$
BEGIN
 CREATE EXTENSION IF NOT EXISTS pg_trgm;
 CREATE INDEX IF NOT EXISTS catalog_studio_names_trgm ON catalog_studio_names USING gin (LOWER(studio) gin_trgm_ops);
EXCEPTION WHEN OTHERS THEN
 RAISE NOTICE 'catalog: pg_trgm unavailable, studio filters scan the vocabulary: %', SQLERRM;
END
$$;`)
}

// studioNamesLike is the stored studio names whose lower-case form matches
// the LIKE pattern like (already lower-case and escaped): a trigram lookup
// on SQLite for a plain substring of three or more characters, else a scan
// of the vocabulary. The recheck keeps LOWER's ASCII-only folding exact.
func (c *Catalog) studioNamesLike(like, value string) (string, []any) {
	exact := `SELECT n.studio FROM catalog_studio_names n WHERE n.titles>0 AND LOWER(n.studio) LIKE ? ESCAPE '\'`
	if store.DialectOf(c.db) == store.SQLite && utf8.RuneCountInString(value) >= 3 && !strings.ContainsAny(value, `%_\`) && strings.HasPrefix(like, "%") && strings.HasSuffix(like, "%") {
		return exact + ` AND n.rowid IN (SELECT rowid FROM catalog_studio_names_fts WHERE studio LIKE ?)`, []any{like, "%" + value + "%"}
	}
	return exact, []any{like}
}

// positiveStudioNames returns the studio-name vocabulary subquery and its
// args for a filter whose studio predicate is exactly one positive clause
// (the shape Plex's studio= sends), so the total can be summed from
// catalog_facet_tally's studio rows instead of counting every matching
// title. ok is false for a filter with no studio predicate, a negative one,
// or more than one (which the tally cannot express). It mirrors browseWhere's
// studio block so the tally membership equals the listing's predicate.
func (c *Catalog) positiveStudioNames(f core.Filter) (string, []any, bool) {
	if len(f.StudioFilters) == 0 {
		if f.Studio == "" {
			return "", nil, false
		}
		return `SELECT ?`, []any{f.Studio}, true
	}
	if len(f.StudioFilters) != 1 {
		return "", nil, false
	}
	sf := f.StudioFilters[0]
	val := strings.ToLower(sf.Value)
	switch sf.Op {
	case "==":
		return `SELECT n.studio FROM catalog_studio_names n WHERE n.titles>0 AND LOWER(n.studio)=?`, []any{val}, true
	case "<=":
		names, args := c.studioNamesLike(escapeLike(val)+"%", val)
		return names, args, true
	case ">=":
		names, args := c.studioNamesLike("%"+escapeLike(val), val)
		return names, args, true
	case "=":
		names, args := c.studioNamesLike("%"+escapeLike(val)+"%", val)
		return names, args, true
	}
	return "", nil, false
}
