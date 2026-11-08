package catalog

import (
	"context"
	"database/sql"
	"sort"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// catalog_facet_tally extends catalog_browse_tally (browse_tally.go) to two
// more library-wide counts every Plex client asks for:
//
//   - facet 'first': the jump bar, one row per first-character bucket of the
//     sort title, so /firstCharacter is a few tally rows instead of a GROUP
//     BY over every browsable title;
//   - facet 'country': a country-filtered listing's total, one row per
//     stored country, as the genre rows answer a genre-filtered total.
//
// Membership is the browse tally's (tallyMemberSQL), so the same
// correction over the unsettled titles (not yet released, availability
// hidden, excluded by key) makes a read exact. Writers append signed
// deltas from triggers; the catalogue's fold moves them into the tally.

// jumpBucketSQL is a title's jump-bar bucket, exactly as countFacet's
// firstCharacter facet computes it. alias is "NEW.", "OLD." or "t.".
func jumpBucketSQL(a string) string {
	return `CASE WHEN ` + a + `sort_title < 'a' THEN '#' WHEN ` + a + `sort_title >= 'a' AND ` + a + `sort_title < '{' THEN UPPER(SUBSTR(` + a + `sort_title, 1, 1)) ELSE '~' END`
}

var facetTallyBackfill = `
INSERT INTO catalog_facet_tally(type,facet,value,rating_rank,n)
 SELECT t.type,'first',` + jumpBucketSQL("t.") + `,t.content_rating_rank,COUNT(*) FROM catalog_titles t WHERE ` + tallyMemberSQL("t.") + ` GROUP BY t.type,3,t.content_rating_rank;
INSERT INTO catalog_facet_tally(type,facet,value,rating_rank,n)
 SELECT t.type,'country',co.country,t.content_rating_rank,COUNT(*) FROM catalog_countries co JOIN catalog_titles t ON t.key=co.key WHERE co.country<>'' AND ` + tallyMemberSQL("t.") + ` GROUP BY t.type,co.country,t.content_rating_rank;`

func init() {
	member := tallyMemberSQL
	titleDelta := func(a, sign string) string {
		return `INSERT INTO catalog_facet_tally_delta(type,facet,value,rating_rank,d) SELECT ` + a + `type,'first',` + jumpBucketSQL(a) + `,` + a + `content_rating_rank,` + sign + ` WHERE ` + member(a) + `;
 INSERT INTO catalog_facet_tally_delta(type,facet,value,rating_rank,d) SELECT ` + a + `type,'country',country,` + a + `content_rating_rank,` + sign + ` FROM catalog_countries WHERE key=` + a + `key AND country<>'' AND ` + member(a) + `;`
	}
	countryDelta := func(a, sign string) string {
		return `INSERT INTO catalog_facet_tally_delta(type,facet,value,rating_rank,d) SELECT t.type,'country',` + a + `country,t.content_rating_rank,` + sign + ` FROM catalog_titles t WHERE t.key=` + a + `key AND ` + a + `country<>'' AND ` + member("t.") + `;`
	}
	changed := `OLD.key IS NOT NEW.key OR OLD.type IS NOT NEW.type OR OLD.browsable IS NOT NEW.browsable OR OLD.adult IS NOT NEW.adult OR (OLD.released_ts<>0) IS NOT (NEW.released_ts<>0) OR OLD.content_rating_rank IS NOT NEW.content_rating_rank OR (` + jumpBucketSQL("OLD.") + `) IS NOT (` + jumpBucketSQL("NEW.") + `)`
	columns := `key,type,browsable,adult,released_ts,content_rating_rank,sort_title`
	store.Register("catalog", 50, `CREATE TABLE catalog_facet_tally(type TEXT NOT NULL, facet TEXT NOT NULL, value TEXT NOT NULL, rating_rank INTEGER NOT NULL, n INTEGER NOT NULL, PRIMARY KEY(type,facet,value,rating_rank));
CREATE TABLE catalog_facet_tally_delta(id INTEGER PRIMARY KEY, type TEXT NOT NULL, facet TEXT NOT NULL, value TEXT NOT NULL, rating_rank INTEGER NOT NULL, d INTEGER NOT NULL);
CREATE INDEX catalog_facet_tally_delta_dims ON catalog_facet_tally_delta(type,facet,value,rating_rank);`+facetTallyBackfill+`
CREATE TRIGGER catalog_facet_tally_title_insert AFTER INSERT ON catalog_titles BEGIN `+titleDelta("NEW.", "1")+` END;
CREATE TRIGGER catalog_facet_tally_title_delete AFTER DELETE ON catalog_titles BEGIN `+titleDelta("OLD.", "-1")+` END;
CREATE TRIGGER catalog_facet_tally_title_update AFTER UPDATE OF `+columns+` ON catalog_titles
WHEN `+changed+` BEGIN `+titleDelta("OLD.", "-1")+` `+titleDelta("NEW.", "1")+` END;
CREATE TRIGGER catalog_facet_tally_country_insert AFTER INSERT ON catalog_countries BEGIN `+countryDelta("NEW.", "1")+` END;
CREATE TRIGGER catalog_facet_tally_country_delete AFTER DELETE ON catalog_countries BEGIN `+countryDelta("OLD.", "-1")+` END;
CREATE TRIGGER catalog_facet_tally_country_update AFTER UPDATE ON catalog_countries BEGIN `+countryDelta("OLD.", "-1")+` `+countryDelta("NEW.", "1")+` END;`)
	// As for the genre rows (browse_tally.go): a country trigger first takes
	// a share lock on its title, so it reads the committed title row, and
	// the migration keeps old-version writers out until the backfill and
	// the triggers are visible together.
	store.RegisterDialect("catalog", 50, store.Postgres, `LOCK TABLE catalog_titles, catalog_countries IN SHARE ROW EXCLUSIVE MODE;
CREATE TABLE catalog_facet_tally(type TEXT NOT NULL, facet TEXT NOT NULL, value TEXT NOT NULL, rating_rank BIGINT NOT NULL, n BIGINT NOT NULL, PRIMARY KEY(type,facet,value,rating_rank));
CREATE TABLE catalog_facet_tally_delta(id BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY, type TEXT NOT NULL, facet TEXT NOT NULL, value TEXT NOT NULL, rating_rank BIGINT NOT NULL, d BIGINT NOT NULL);
CREATE INDEX catalog_facet_tally_delta_dims ON catalog_facet_tally_delta(type,facet,value,rating_rank);`+facetTallyBackfill+`
CREATE FUNCTION catalog_facet_tally_title() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
IF TG_OP<>'INSERT' THEN `+titleDelta("OLD.", "-1")+` END IF;
IF TG_OP<>'DELETE' THEN `+titleDelta("NEW.", "1")+` END IF;
RETURN NULL; END $$;
CREATE FUNCTION catalog_facet_tally_country() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
IF TG_OP<>'INSERT' THEN PERFORM 1 FROM catalog_titles WHERE key=OLD.key FOR SHARE; `+countryDelta("OLD.", "-1")+` END IF;
IF TG_OP<>'DELETE' THEN PERFORM 1 FROM catalog_titles WHERE key=NEW.key FOR SHARE; `+countryDelta("NEW.", "1")+` END IF;
RETURN NULL; END $$;
CREATE TRIGGER catalog_facet_tally_title AFTER INSERT OR DELETE ON catalog_titles FOR EACH ROW EXECUTE FUNCTION catalog_facet_tally_title();
CREATE TRIGGER catalog_facet_tally_title_update AFTER UPDATE OF `+columns+` ON catalog_titles
 FOR EACH ROW WHEN (`+pgDistinct(changed)+`) EXECUTE FUNCTION catalog_facet_tally_title();
CREATE TRIGGER catalog_facet_tally_country AFTER INSERT OR DELETE OR UPDATE ON catalog_countries FOR EACH ROW EXECUTE FUNCTION catalog_facet_tally_country();`)
	store.RegisterRestore("catalog_facet_tally", func(context.Context, *sql.Tx) (func(context.Context, *sql.Tx) error, error) {
		return func(ctx context.Context, tx *sql.Tx) error { return rebuildFacetTally(ctx, tx) }, nil
	}, "catalog_facet_tally", "catalog_facet_tally_delta")
}

func rebuildFacetTally(ctx context.Context, db execer) error {
	for _, q := range []string{`DELETE FROM catalog_facet_tally_delta`, `DELETE FROM catalog_facet_tally`} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	for _, q := range strings.Split(strings.TrimSpace(facetTallyBackfill), ";") {
		if q = strings.TrimSpace(q); q == "" {
			continue
		}
		if _, err := db.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	return nil
}

// jumpBarFromTally answers the firstCharacter facet from the tally when
// tallyShape admits f (without a genre or country); where and args are
// browseWhere(f). ok is false otherwise. Per bucket,
//
//	count = tally(bucket) - |U ∩ member ∩ bucket| + |U ∩ where ∩ bucket|
//
// as tallyTotal reasons for a total.
func (c *Catalog) jumpBarFromTally(ctx context.Context, f core.Filter, where string, args []any) ([]core.FacetValue, bool, error) {
	s, ok := c.tallyShape(f)
	if !ok || s.genre != "" || s.country != "" {
		return nil, false, nil
	}
	dims, dimArgs, member, memberArgs := s.facetDims("first", "")
	sums := map[string]int{}
	rows, err := c.db.QueryContext(ctx, `SELECT value, CAST(SUM(n) AS BIGINT) FROM (SELECT value, n FROM catalog_facet_tally WHERE `+dims+` UNION ALL SELECT value, d FROM catalog_facet_tally_delta WHERE `+dims+`) s GROUP BY value`, append(append([]any{}, dimArgs...), dimArgs...)...)
	if err != nil {
		return nil, false, err
	}
	for rows.Next() {
		var v string
		var n int
		if err := rows.Scan(&v, &n); err != nil {
			rows.Close()
			return nil, false, err
		}
		sums[v] += n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	unsettled, unsettledArgs := c.tallyUnsettled(s, f)
	q := `SELECT ` + jumpBucketSQL("t.") + `, COUNT(CASE WHEN ` + where + ` THEN 1 END) - COUNT(CASE WHEN ` + member + ` THEN 1 END) FROM catalog_titles t WHERE t.key IN (` + unsettled + `) GROUP BY 1`
	all := append(append(append([]any{}, args...), memberArgs...), unsettledArgs...)
	rows, err = c.db.QueryContext(ctx, q, all...)
	if err != nil {
		return nil, false, err
	}
	for rows.Next() {
		var v string
		var n int
		if err := rows.Scan(&v, &n); err != nil {
			rows.Close()
			return nil, false, err
		}
		sums[v] += n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	out := make([]core.FacetValue, 0, len(sums))
	for v, n := range sums {
		if n > 0 {
			out = append(out, core.FacetValue{Key: v, Count: n})
		}
	}
	// ORDER BY 1 in BINARY (SQLite) or "C" (PostgreSQL) collation.
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, true, nil
}
