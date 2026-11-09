package catalog

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/braedonsaunders/ether/internal/contentrating"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// Jellyfin's /Studios lists fifty studio names and how many there are. From
// catalog_facet_tally (facet_tally.go) that summed every studio's rows, the
// whole vocabulary, to slice fifty names and count the rest.
//
// catalog_facet_presence keeps one row per (type, studio): a bit per content
// rating rank whose tally row counts titles (ranks) and the titles over every
// rank (n). catalog_facet_counts keeps, per type and per rating range a
// listing can ask for, how many studios have a title in it. Triggers on the
// tally keep both: a fold that moves deltas into a tally row recomputes its
// studio's presence from the tally (idempotent, so a PostgreSQL row trigger
// that runs at the end of a many-row fold reads the folded state and still
// counts each change once), and a change of presence moves the counts. They
// mirror the folded tally, so a read adds the same pending deltas and
// unsettled titles facetFromTally does, for the few studios those touch.
//
// A page is then the presence rows in name order from their index and the
// total one counts row, corrected over those few studios.

// presenceFacets are the tally facets with a presence, by the FacetFilter
// facet that lists them whole.
var presenceFacets = map[string]string{"studioAll": "studio"}

// presenceFacetSQL is the tally facets with a presence, as a SQL list.
const presenceFacetSQL = `('studio')`

// rankRange is a content-rating rank range a listing can filter on: lo..hi,
// hi 0 for no upper bound. {0, 0} is every rank, unrated included (no
// rating filter); a rating filter starts at 1 (tallyShape).
type rankRange struct{ lo, hi int }

// maxRank is the highest content_rating_rank.
var maxRank = int(contentrating.Levels[len(contentrating.Levels)-1])

func presenceRanges() []rankRange {
	out := []rankRange{{0, 0}}
	for lo := 1; lo <= maxRank; lo++ {
		for hi := lo; hi <= maxRank; hi++ {
			out = append(out, rankRange{lo, hi})
		}
		out = append(out, rankRange{lo, 0})
	}
	return out
}

// bits is the presence mask of the ranks r admits.
func (r rankRange) bits() int64 {
	if r.hi == 0 {
		return -1 << r.lo
	}
	return (int64(1)<<(r.hi+1) - 1) &^ (int64(1)<<r.lo - 1)
}

// rangesSQL is presenceRanges as rows (lo, hi, bits).
func rangesSQL() string {
	var b strings.Builder
	for i, r := range presenceRanges() {
		if i > 0 {
			b.WriteString(" UNION ALL ")
		}
		fmt.Fprintf(&b, "SELECT %d AS lo, %d AS hi, CAST(%d AS BIGINT) AS bits", r.lo, r.hi, r.bits())
	}
	return b.String()
}

// presenceRange is the counted range s filters on; empty when it admits
// no rank. ok is false for a range the counts do not keep.
func presenceRange(s tallyShape) (r rankRange, empty, ok bool) {
	if !s.ranked {
		return rankRange{}, false, true
	}
	lo, hi := s.lo, s.hi
	if lo < 1 || lo > maxRank {
		return rankRange{}, false, false
	}
	if hi > maxRank {
		hi = 0
	} else if hi < lo {
		return rankRange{}, true, true
	}
	return rankRange{lo, hi}, false, true
}

// presenceShift and presenceSum spell the presence's rank bit and sums in
// SQL both engines read alike.
const presenceShift = `CAST(1 AS BIGINT)<<CAST(rating_rank AS INTEGER)`

func presenceSum(x string) string { return `CAST(COALESCE(SUM(` + x + `),0) AS BIGINT)` }

// presenceBackfill counts the presence, then its counts, from the tally.
func presenceBackfill() string { return presenceRows() + presenceCounts() }

func presenceRows() string {
	return `INSERT INTO catalog_facet_presence(type,facet,value,ranks,n)
 SELECT type,facet,value,` + presenceSum(presenceShift) + `,` + presenceSum("n") + ` FROM catalog_facet_tally WHERE facet IN ` + presenceFacetSQL + ` AND n>0 GROUP BY type,facet,value;
`
}

func presenceCounts() string {
	return `INSERT INTO catalog_facet_counts(type,facet,lo,hi,n)
 SELECT p.type,p.facet,r.lo,r.hi,COUNT(*) FROM catalog_facet_presence p JOIN (` + rangesSQL() + `) r ON (p.ranks & r.bits)<>0 GROUP BY p.type,p.facet,r.lo,r.hi;`
}

// presenceRebuild recounts the presence and its counts from the tally, for
// rebuildFacetTally (whose writes the triggers may or may not have seen).
// The counts go last: the presence rows' own trigger moves them.
func presenceRebuild() string {
	return `DELETE FROM catalog_facet_presence;
` + presenceRows() + `DELETE FROM catalog_facet_counts;
` + presenceCounts()
}

func init() {
	// recompute is a's value's presence from its tally rows; guard keeps
	// other facets and unchanged keys out.
	recompute := func(a, guard string) string {
		of := ` FROM catalog_facet_tally WHERE type=` + a + `type AND facet=` + a + `facet AND value=` + a + `value AND n>0)`
		return `INSERT INTO catalog_facet_presence(type,facet,value,ranks,n) SELECT ` + a + `type,` + a + `facet,` + a + `value,(SELECT ` + presenceSum(presenceShift) + of + `,(SELECT ` + presenceSum("n") + of + `
 WHERE ` + a + `facet IN ` + presenceFacetSQL + guard + `
 ON CONFLICT(type,facet,value) DO UPDATE SET ranks=excluded.ranks,n=excluded.n WHERE catalog_facet_presence.ranks<>excluded.ranks OR catalog_facet_presence.n<>excluded.n;`
	}
	moved := `OLD.type<>NEW.type OR OLD.facet<>NEW.facet OR OLD.value<>NEW.value`
	// count moves the counts by a presence row's change from was to NEW.ranks.
	count := func(was string) string {
		in := func(mask string) string { return `((` + mask + ` & r.bits)<>0)` }
		return `INSERT INTO catalog_facet_counts(type,facet,lo,hi,n) SELECT NEW.type,NEW.facet,r.lo,r.hi,(CASE WHEN ` + in("NEW.ranks") + ` THEN 1 ELSE 0 END)-(CASE WHEN ` + in(was) + ` THEN 1 ELSE 0 END)
 FROM (` + rangesSQL() + `) r WHERE ` + in("NEW.ranks") + `<>` + in(was) + `
 ON CONFLICT(type,facet,lo,hi) DO UPDATE SET n=catalog_facet_counts.n+excluded.n;`
	}
	tables := func(integer string) string {
		return `CREATE TABLE catalog_facet_presence(type TEXT NOT NULL, facet TEXT NOT NULL, value TEXT NOT NULL, ranks ` + integer + ` NOT NULL, n ` + integer + ` NOT NULL, PRIMARY KEY(type,facet,value));
CREATE TABLE catalog_facet_counts(type TEXT NOT NULL, facet TEXT NOT NULL, lo ` + integer + ` NOT NULL, hi ` + integer + ` NOT NULL, n ` + integer + ` NOT NULL, PRIMARY KEY(type,facet,lo,hi));
CREATE INDEX catalog_facet_presence_count ON catalog_facet_presence(type,facet,n DESC,value);
`
	}
	store.Register("catalog", 55, tables("INTEGER")+presenceBackfill()+`
CREATE TRIGGER catalog_facet_presence_insert AFTER INSERT ON catalog_facet_tally WHEN NEW.facet IN `+presenceFacetSQL+` BEGIN `+recompute("NEW.", "")+` END;
CREATE TRIGGER catalog_facet_presence_delete AFTER DELETE ON catalog_facet_tally WHEN OLD.facet IN `+presenceFacetSQL+` BEGIN `+recompute("OLD.", "")+` END;
CREATE TRIGGER catalog_facet_presence_update AFTER UPDATE ON catalog_facet_tally WHEN OLD.facet IN `+presenceFacetSQL+` OR NEW.facet IN `+presenceFacetSQL+` BEGIN `+recompute("OLD.", " AND ("+moved+")")+` `+recompute("NEW.", "")+` END;
CREATE TRIGGER catalog_facet_counts_insert AFTER INSERT ON catalog_facet_presence BEGIN `+count("0")+` END;
CREATE TRIGGER catalog_facet_counts_update AFTER UPDATE OF ranks ON catalog_facet_presence WHEN OLD.ranks<>NEW.ranks BEGIN `+count("OLD.ranks")+` END;`)
	// The presence is read in byte order, as the facet lists sort their
	// names; a "C" index serves that order whatever the database collation.
	// The lock keeps folds out (of this or an older Ether) until the
	// backfill and the triggers are visible together.
	store.RegisterDialect("catalog", 55, store.Postgres, `LOCK TABLE catalog_facet_tally IN SHARE ROW EXCLUSIVE MODE;
`+tables("BIGINT")+`CREATE INDEX catalog_facet_presence_order ON catalog_facet_presence(type,facet,value COLLATE "C");
`+presenceBackfill()+`
CREATE FUNCTION catalog_facet_presence() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
IF TG_OP='DELETE' THEN `+recompute("OLD.", "")+`
ELSE
 IF TG_OP='UPDATE' THEN IF `+moved+` THEN `+recompute("OLD.", "")+` END IF; END IF;
 `+recompute("NEW.", "")+`
END IF;
RETURN NULL; END $$;
CREATE FUNCTION catalog_facet_counts() RETURNS trigger LANGUAGE plpgsql AS $$ DECLARE was BIGINT := 0; BEGIN
IF TG_OP='UPDATE' THEN was := OLD.ranks; END IF;
`+count("was")+`
RETURN NULL; END $$;
CREATE TRIGGER catalog_facet_presence_insert AFTER INSERT ON catalog_facet_tally FOR EACH ROW WHEN (NEW.facet IN `+presenceFacetSQL+`) EXECUTE FUNCTION catalog_facet_presence();
CREATE TRIGGER catalog_facet_presence_delete AFTER DELETE ON catalog_facet_tally FOR EACH ROW WHEN (OLD.facet IN `+presenceFacetSQL+`) EXECUTE FUNCTION catalog_facet_presence();
CREATE TRIGGER catalog_facet_presence_update AFTER UPDATE ON catalog_facet_tally FOR EACH ROW WHEN (OLD.facet IN `+presenceFacetSQL+` OR NEW.facet IN `+presenceFacetSQL+`) EXECUTE FUNCTION catalog_facet_presence();
CREATE TRIGGER catalog_facet_counts_insert AFTER INSERT ON catalog_facet_presence FOR EACH ROW EXECUTE FUNCTION catalog_facet_counts();
CREATE TRIGGER catalog_facet_counts_update AFTER UPDATE OF ranks ON catalog_facet_presence FOR EACH ROW WHEN (OLD.ranks IS DISTINCT FROM NEW.ranks) EXECUTE FUNCTION catalog_facet_counts();`)
}

// FacetPage is a page of facet's values (a FacetFilter facet with a
// presence: "studioAll") over the browsable set matching f, in byte order,
// and how many values there are. ok is false when the presence does not
// answer f; FacetFilter counts it whole. Per value the count is
// facetFromTally's; the total is
//
//	counts(range) + Σ over the values the pending deltas and unsettled
//	titles touch of [count > 0] - [tally > 0].
//
// The statement reads the first presence rows in order and returns the
// page alone: it needs offset+limit survivors of the correction, and reads
// further only when the correction removed some.
func (c *Catalog) FacetPage(ctx context.Context, f core.Filter, facet string, offset, limit int) ([]core.FacetValue, int, bool, error) {
	tfacet := presenceFacets[facet]
	if tfacet == "" || limit < 0 {
		return nil, 0, false, nil
	}
	offset = max(offset, 0)
	f = c.viewerPaging(ctx, f)
	s, ok := c.tallyShape(f)
	if !ok || s.genre != "" || s.country != "" || s.years || s.first != nil || len(s.types) != 1 {
		return nil, 0, false, nil
	}
	rr, empty, ok := presenceRange(s)
	if !ok {
		return nil, 0, false, nil
	}
	if empty {
		return []core.FacetValue{}, 0, true, nil
	}
	where, args, err := c.browseWhere(f)
	if err != nil {
		return nil, 0, false, err
	}
	adj, adjArgs, dims, dimArgs := c.presenceAdjustments(s, f, tfacet, where, args)
	order, last := `value`, `value <= (SELECT MAX(value) FROM p)`
	if store.DialectOf(c.db) == store.Postgres {
		order, last = `value COLLATE "C"`, `value COLLATE "C" <= (SELECT MAX(value COLLATE "C") FROM p)`
	}
	q := `WITH ` + adj + `,
p AS (SELECT value FROM catalog_facet_presence WHERE type=? AND facet=? AND (ranks & ?)<>0 ORDER BY ` + order + ` LIMIT ?),
v AS (SELECT c.value AS value, MAX(c.paged) AS paged, SUM(c.d) AS d,
 COALESCE((SELECT SUM(n) FROM catalog_facet_tally WHERE ` + dims + ` AND catalog_facet_tally.value=c.value),0) AS f
 FROM (SELECT value, 1 AS paged, 0 AS d FROM p UNION ALL SELECT value, 0, d FROM adj) c GROUP BY c.value),
w AS (SELECT value, CAST(f+d AS BIGINT) AS n, ROW_NUMBER() OVER (ORDER BY ` + order + `) AS rn FROM v WHERE f+d>0 AND (paged=1 OR (SELECT COUNT(*) FROM p)<? OR ` + last + `))
SELECT 0, value, n, CAST(rn AS BIGINT) FROM w WHERE rn>? AND rn<=?
UNION ALL SELECT 1, '', CAST(COALESCE((SELECT n FROM catalog_facet_counts WHERE type=? AND facet=? AND lo=? AND hi=?),0)
 + COALESCE((SELECT SUM(CASE WHEN f+d>0 THEN 1 ELSE 0 END - CASE WHEN f>0 THEN 1 ELSE 0 END) FROM v),0) AS BIGINT), CAST((SELECT COUNT(*) FROM p) AS BIGINT)
UNION ALL SELECT 2, '', CAST((SELECT COUNT(*) FROM w) AS BIGINT), 0`
	want := offset + limit
	for read := want + 8; ; {
		all := append(append([]any{}, adjArgs...), s.types[0], tfacet, rr.bits(), read)
		all = append(all, dimArgs...)
		all = append(all, read, offset, want, s.types[0], tfacet, rr.lo, rr.hi)
		vals, total, paged, survivors, err := c.facetPageRows(ctx, q, all)
		if err != nil {
			return nil, 0, false, err
		}
		if paged == read && survivors < want {
			// The correction removed some of the names read: read on.
			read += max(read, want-survivors+8)
			continue
		}
		return vals, total, true, nil
	}
}

// facetPageRows reads FacetPage's statement: the page in order, the total,
// how many presence rows it read and how many names survived among them.
func (c *Catalog) facetPageRows(ctx context.Context, q string, args []any) (vals []core.FacetValue, total, paged, survivors int, err error) {
	rows, err := c.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, 0, 0, 0, err
	}
	defer rows.Close()
	type ranked struct {
		v  core.FacetValue
		rn int64
	}
	var page []ranked
	for rows.Next() {
		var kind int
		var value string
		var n, x int64
		if err := rows.Scan(&kind, &value, &n, &x); err != nil {
			return nil, 0, 0, 0, err
		}
		switch kind {
		case 0:
			page = append(page, ranked{core.FacetValue{Key: value, Title: value, Count: int(n)}, x})
		case 1:
			total, paged = int(n), int(x)
		case 2:
			survivors = int(n)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, 0, 0, 0, err
	}
	sort.Slice(page, func(i, j int) bool { return page[i].rn < page[j].rn })
	vals = make([]core.FacetValue, len(page))
	for i, p := range page {
		vals[i] = p.v
	}
	return vals, total, paged, survivors, nil
}

// presenceAdjustments is the CTE adj(value, d): per value of tfacet, what
// the pending deltas and the unsettled titles add to its folded tally count
// for s (facetFromTally's correction), nonzero only. dims is the tally's
// WHERE for s, for the folded counts.
func (c *Catalog) presenceAdjustments(s tallyShape, f core.Filter, tfacet, where string, args []any) (cte string, cteArgs []any, dims string, dimArgs []any) {
	tf := tallyFacets[tfacet]
	dims, dimArgs, member, memberArgs := s.facetDims(tfacet, "")
	unsettled, unsettledArgs := c.tallyUnsettled(s, f)
	cte = `adj AS (SELECT value, SUM(d) AS d FROM (
 SELECT value, d FROM catalog_facet_tally_delta WHERE ` + dims + `
 UNION ALL SELECT ` + tf.titleValue + `, (CASE WHEN ` + where + ` THEN 1 ELSE 0 END) - (CASE WHEN ` + member + ` THEN 1 ELSE 0 END) FROM catalog_titles t ` + tf.join + ` WHERE t.key IN (` + unsettled + `)
) a GROUP BY value HAVING SUM(d)<>0)`
	cteArgs = append(append(append(append(cteArgs, dimArgs...), args...), memberArgs...), unsettledArgs...)
	return cte, cteArgs, dims, dimArgs
}

// topFromPresence is facetFromTally's most-counted values (facet "studio",
// limit of them, most titles first, then by name) for a listing without a
// rating filter, from the presence's count index: the first limit+spare
// rows in that order and the values the correction touches, never every
// value. Past the rows read, a value's count is its folded count, below or
// tied after every row read; so the first limit of the rows read, less the
// ones the correction lowered, stand above it. ok is false for a shape it
// does not answer.
func (c *Catalog) topFromPresence(ctx context.Context, s tallyShape, f core.Filter, tfacet, where string, args []any, limit int) ([]core.FacetValue, bool, error) {
	if s.ranked || len(s.types) != 1 || !strings.Contains(presenceFacetSQL, "'"+tfacet+"'") {
		return nil, false, nil
	}
	adj, adjArgs, _, _ := c.presenceAdjustments(s, f, tfacet, where, args)
	q := `WITH ` + adj + `,
top AS (SELECT value FROM catalog_facet_presence WHERE type=? AND facet=? AND n>0 ORDER BY n DESC, value ASC LIMIT ?),
c AS (SELECT x.value AS value, SUM(x.d) AS d, MAX(x.top) AS top FROM (SELECT value, 0 AS d, 1 AS top FROM top UNION ALL SELECT value, d, 0 FROM adj) x GROUP BY x.value),
v AS (SELECT c.value AS value, c.top AS top, c.d AS d, COALESCE((SELECT p.n FROM catalog_facet_presence p WHERE p.type=? AND p.facet=? AND p.value=c.value),0) AS f FROM c),
w AS (SELECT value, CAST(f+d AS BIGINT) AS n, ROW_NUMBER() OVER (ORDER BY f+d DESC, value ASC) AS rn FROM v WHERE f+d>0)
SELECT 0, value, n, CAST(rn AS BIGINT) FROM w WHERE rn<=?
UNION ALL SELECT 1, '', CAST((SELECT COUNT(*) FROM v WHERE top=1 AND d<0) AS BIGINT), CAST((SELECT COUNT(*) FROM top) AS BIGINT)`
	// Unreleased titles lower the most-counted studios first: read a
	// margin past limit, so one statement is enough.
	for read := limit + max(8, limit/2); ; {
		all := append(append([]any{}, adjArgs...), s.types[0], tfacet, read, s.types[0], tfacet, limit)
		rows, err := c.db.QueryContext(ctx, q, all...)
		if err != nil {
			return nil, false, err
		}
		type ranked struct {
			v    core.FacetValue
			rank int64
		}
		var got []ranked
		var lowered, top int64
		for rows.Next() {
			var kind int
			var value string
			var n, x int64
			if err := rows.Scan(&kind, &value, &n, &x); err != nil {
				rows.Close()
				return nil, false, err
			}
			if kind == 1 {
				lowered, top = n, x
				continue
			}
			got = append(got, ranked{core.FacetValue{Key: value, Count: int(n)}, x})
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, false, err
		}
		if top == int64(read) && int64(read)-lowered < int64(limit) {
			read += max(read, limit)
			continue
		}
		sort.Slice(got, func(i, j int) bool { return got[i].rank < got[j].rank })
		out := make([]core.FacetValue, 0, min(limit, len(got)))
		for _, g := range got[:min(limit, len(got))] {
			out = append(out, g.v)
		}
		return out, true, nil
	}
}
