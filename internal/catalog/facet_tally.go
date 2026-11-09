package catalog

import (
	"context"
	"database/sql"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// catalog_facet_tally extends catalog_browse_tally (browse_tally.go) to the
// other library-wide counts Plex clients ask for:
//
//   - facet 'first': the jump bar, one row per first-character bucket of the
//     sort title, so /firstCharacter is a few tally rows instead of a GROUP
//     BY over every browsable title;
//   - facets 'country', 'studio' and 'year': the section's filter lists
//     (/country, /studio, /year, /decade) and a country-filtered listing's
//     total, one row per stored value, as the genre rows of the browse
//     tally answer /genre and a genre-filtered total.
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
 SELECT t.type,'year',CAST(t.year AS TEXT),t.content_rating_rank,COUNT(*) FROM catalog_titles t WHERE t.year>0 AND ` + tallyMemberSQL("t.") + ` GROUP BY t.type,t.year,t.content_rating_rank;
INSERT INTO catalog_facet_tally(type,facet,value,rating_rank,n)
 SELECT t.type,'country',co.country,t.content_rating_rank,COUNT(*) FROM catalog_countries co JOIN catalog_titles t ON t.key=co.key WHERE co.country<>'' AND ` + tallyMemberSQL("t.") + ` GROUP BY t.type,co.country,t.content_rating_rank;
INSERT INTO catalog_facet_tally(type,facet,value,rating_rank,n)
 SELECT t.type,'studio',st.studio,t.content_rating_rank,COUNT(*) FROM catalog_studios st JOIN catalog_titles t ON t.key=st.key WHERE st.studio<>'' AND ` + tallyMemberSQL("t.") + ` GROUP BY t.type,st.studio,t.content_rating_rank;`

func init() {
	member := tallyMemberSQL
	titleDelta := func(a, sign string) string {
		return `INSERT INTO catalog_facet_tally_delta(type,facet,value,rating_rank,d) SELECT ` + a + `type,'first',` + jumpBucketSQL(a) + `,` + a + `content_rating_rank,` + sign + ` WHERE ` + member(a) + `;
 INSERT INTO catalog_facet_tally_delta(type,facet,value,rating_rank,d) SELECT ` + a + `type,'year',CAST(` + a + `year AS TEXT),` + a + `content_rating_rank,` + sign + ` WHERE ` + a + `year>0 AND ` + member(a) + `;
 INSERT INTO catalog_facet_tally_delta(type,facet,value,rating_rank,d) SELECT ` + a + `type,'country',country,` + a + `content_rating_rank,` + sign + ` FROM catalog_countries WHERE key=` + a + `key AND country<>'' AND ` + member(a) + `;
 INSERT INTO catalog_facet_tally_delta(type,facet,value,rating_rank,d) SELECT ` + a + `type,'studio',studio,` + a + `content_rating_rank,` + sign + ` FROM catalog_studios WHERE key=` + a + `key AND studio<>'' AND ` + member(a) + `;`
	}
	// pairDelta is a join row's contribution: a stored country or studio.
	pairDelta := func(facet, col, a, sign string) string {
		return `INSERT INTO catalog_facet_tally_delta(type,facet,value,rating_rank,d) SELECT t.type,'` + facet + `',` + a + col + `,t.content_rating_rank,` + sign + ` FROM catalog_titles t WHERE t.key=` + a + `key AND ` + a + col + `<>'' AND ` + member("t.") + `;`
	}
	countryDelta := func(a, sign string) string { return pairDelta("country", "country", a, sign) }
	studioDelta := func(a, sign string) string { return pairDelta("studio", "studio", a, sign) }
	changed := `OLD.key IS NOT NEW.key OR OLD.type IS NOT NEW.type OR OLD.browsable IS NOT NEW.browsable OR OLD.adult IS NOT NEW.adult OR (OLD.released_ts<>0) IS NOT (NEW.released_ts<>0) OR OLD.content_rating_rank IS NOT NEW.content_rating_rank OR OLD.year IS NOT NEW.year OR (` + jumpBucketSQL("OLD.") + `) IS NOT (` + jumpBucketSQL("NEW.") + `)`
	columns := `key,type,browsable,adult,released_ts,content_rating_rank,year,sort_title`
	store.Register("catalog", 50, `CREATE TABLE catalog_facet_tally(type TEXT NOT NULL, facet TEXT NOT NULL, value TEXT NOT NULL, rating_rank INTEGER NOT NULL, n INTEGER NOT NULL, PRIMARY KEY(type,facet,value,rating_rank));
CREATE TABLE catalog_facet_tally_delta(id INTEGER PRIMARY KEY, type TEXT NOT NULL, facet TEXT NOT NULL, value TEXT NOT NULL, rating_rank INTEGER NOT NULL, d INTEGER NOT NULL);
CREATE INDEX catalog_facet_tally_delta_dims ON catalog_facet_tally_delta(type,facet,value,rating_rank);`+facetTallyBackfill+`
CREATE TRIGGER catalog_facet_tally_title_insert AFTER INSERT ON catalog_titles BEGIN `+titleDelta("NEW.", "1")+` END;
CREATE TRIGGER catalog_facet_tally_title_delete AFTER DELETE ON catalog_titles BEGIN `+titleDelta("OLD.", "-1")+` END;
CREATE TRIGGER catalog_facet_tally_title_update AFTER UPDATE OF `+columns+` ON catalog_titles
WHEN `+changed+` BEGIN `+titleDelta("OLD.", "-1")+` `+titleDelta("NEW.", "1")+` END;
CREATE TRIGGER catalog_facet_tally_country_insert AFTER INSERT ON catalog_countries BEGIN `+countryDelta("NEW.", "1")+` END;
CREATE TRIGGER catalog_facet_tally_country_delete AFTER DELETE ON catalog_countries BEGIN `+countryDelta("OLD.", "-1")+` END;
CREATE TRIGGER catalog_facet_tally_country_update AFTER UPDATE ON catalog_countries BEGIN `+countryDelta("OLD.", "-1")+` `+countryDelta("NEW.", "1")+` END;
CREATE TRIGGER catalog_facet_tally_studio_insert AFTER INSERT ON catalog_studios BEGIN `+studioDelta("NEW.", "1")+` END;
CREATE TRIGGER catalog_facet_tally_studio_delete AFTER DELETE ON catalog_studios BEGIN `+studioDelta("OLD.", "-1")+` END;
CREATE TRIGGER catalog_facet_tally_studio_update AFTER UPDATE ON catalog_studios BEGIN `+studioDelta("OLD.", "-1")+` `+studioDelta("NEW.", "1")+` END;`)
	// As for the genre rows (browse_tally.go): a country trigger first takes
	// a share lock on its title, so it reads the committed title row, and
	// the migration keeps old-version writers out until the backfill and
	// the triggers are visible together.
	store.RegisterDialect("catalog", 50, store.Postgres, `LOCK TABLE catalog_titles, catalog_countries, catalog_studios IN SHARE ROW EXCLUSIVE MODE;
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
CREATE FUNCTION catalog_facet_tally_studio() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
IF TG_OP<>'INSERT' THEN PERFORM 1 FROM catalog_titles WHERE key=OLD.key FOR SHARE; `+studioDelta("OLD.", "-1")+` END IF;
IF TG_OP<>'DELETE' THEN PERFORM 1 FROM catalog_titles WHERE key=NEW.key FOR SHARE; `+studioDelta("NEW.", "1")+` END IF;
RETURN NULL; END $$;
CREATE TRIGGER catalog_facet_tally_title AFTER INSERT OR DELETE ON catalog_titles FOR EACH ROW EXECUTE FUNCTION catalog_facet_tally_title();
CREATE TRIGGER catalog_facet_tally_title_update AFTER UPDATE OF `+columns+` ON catalog_titles
 FOR EACH ROW WHEN (`+pgDistinct(changed)+`) EXECUTE FUNCTION catalog_facet_tally_title();
CREATE TRIGGER catalog_facet_tally_country AFTER INSERT OR DELETE OR UPDATE ON catalog_countries FOR EACH ROW EXECUTE FUNCTION catalog_facet_tally_country();
CREATE TRIGGER catalog_facet_tally_studio AFTER INSERT OR DELETE OR UPDATE ON catalog_studios FOR EACH ROW EXECUTE FUNCTION catalog_facet_tally_studio();`)
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

// tallyFacets are the facets facetFromTally answers: where each value's
// tally rows are, how an unsettled title contributes to a value, and the
// order and limit countFacet gives them.
var tallyFacets = map[string]struct {
	table, facet, value string // tally table, facet name ("" for the genre rows), value column
	join, titleValue    string // the unsettled titles' join and value expression
	order               string
}{
	"genre":   {"catalog_browse_tally", "", "genre", "JOIN catalog_genres g ON g.key=t.key AND g.genre<>''", "g.genre", "ORDER BY 2 DESC, 1 ASC"},
	"country": {"catalog_facet_tally", "country", "value", "JOIN catalog_countries co ON co.key=t.key AND co.country<>''", "co.country", "ORDER BY 2 DESC, 1 ASC LIMIT 200"},
	"studio":  {"catalog_facet_tally", "studio", "value", "JOIN catalog_studios st ON st.key=t.key AND st.studio<>''", "st.studio", "ORDER BY 2 DESC, 1 ASC LIMIT 200"},
	// Every studio or country, by name: Jellyfin and Emby list and resolve them all.
	"studioAll":      {"catalog_facet_tally", "studio", "value", "JOIN catalog_studios st ON st.key=t.key AND st.studio<>''", "st.studio", "ORDER BY 1 ASC"},
	"countryAll":     {"catalog_facet_tally", "country", "value", "JOIN catalog_countries co ON co.key=t.key AND co.country<>''", "co.country", "ORDER BY 1 ASC"},
	"year":           {"catalog_facet_tally", "year", "value", "", "CAST(t.year AS TEXT)", "ORDER BY CAST(value AS INTEGER) DESC"},
	"decade":         {"catalog_facet_tally", "year", "CAST((CAST(value AS INTEGER)/10)*10 AS TEXT)", "", "CAST((t.year/10)*10 AS TEXT)", "ORDER BY CAST(value AS INTEGER) DESC"},
	"firstCharacter": {"catalog_facet_tally", "first", "value", "", jumpBucketSQL("t."), "ORDER BY 1 ASC"},
}

// facetFromTally answers a facet's value counts from the tallies when
// tallyShape admits f without a genre or country; where and args are
// browseWhere(f). ok is false otherwise. Per value,
//
//	count = tally(value) - |U ∩ member ∩ value| + |U ∩ where ∩ value|
//
// as tallyTotal reasons for a total, summed and ordered in one statement
// in the database's own collation, as the direct count orders them.
func (c *Catalog) facetFromTally(ctx context.Context, f core.Filter, facet, where string, args []any) ([]core.FacetValue, bool, error) {
	tf, known := tallyFacets[facet]
	if !known {
		return nil, false, nil
	}
	s, ok := c.tallyShape(f)
	if !ok || s.genre != "" || s.country != "" || s.years || s.first != nil {
		return nil, false, nil
	}
	var dims, member string
	var dimArgs, memberArgs []any
	if tf.facet == "" {
		// Every genre row of the browse tally.
		dims = `type IN (` + placeholders(len(s.types)) + `) AND genre<>''`
		for _, typ := range s.types {
			dimArgs = append(dimArgs, typ)
		}
		member, memberArgs = s.typeMember()
		dims, dimArgs, member, memberArgs = s.rankDims(dims, dimArgs, member, memberArgs)
	} else {
		dims, dimArgs, member, memberArgs = s.facetDims(tf.facet, "")
	}
	if tf.facet == "year" {
		member += ` AND t.year>0`
		where = "(" + where + ") AND t.year>0"
	}
	unsettled, unsettledArgs := c.tallyUnsettled(s, f)
	order := tf.order
	if facet == "firstCharacter" && store.DialectOf(c.db) == store.Postgres {
		order = `ORDER BY value COLLATE "C" ASC`
	}
	q := `SELECT value, CAST(SUM(n) AS BIGINT) FROM (
 SELECT ` + tf.value + ` AS value, n FROM ` + tf.table + ` WHERE ` + dims + `
 UNION ALL SELECT ` + tf.value + `, d FROM ` + tf.table + `_delta WHERE ` + dims + `
 UNION ALL SELECT ` + tf.titleValue + `, (CASE WHEN ` + where + ` THEN 1 ELSE 0 END) - (CASE WHEN ` + member + ` THEN 1 ELSE 0 END) FROM catalog_titles t ` + tf.join + ` WHERE t.key IN (` + unsettled + `)
) s GROUP BY value HAVING SUM(n)>0 ` + order
	all := append(append(append(append(append([]any{}, dimArgs...), dimArgs...), args...), memberArgs...), unsettledArgs...)
	rows, err := c.db.QueryContext(ctx, q, all...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	out := []core.FacetValue{}
	for rows.Next() {
		var v core.FacetValue
		if err := rows.Scan(&v.Key, &v.Count); err != nil {
			return nil, false, err
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	return out, true, nil
}
