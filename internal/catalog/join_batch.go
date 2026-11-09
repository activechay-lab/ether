package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
)

// joinBatch replaces the facet and credit rows of every title a transaction
// writes, in one read and one write per table for the whole batch rather than
// per title: a group-committed detail batch of eight titles reads its stored
// facets once and inserts all its genres in one statement. A single upsert
// is a batch of one. Only the difference is written: a refresh usually
// brings the same values, and every row written fires the search, tally and
// viewer-policy triggers and touches each index. A title appearing twice is
// written in order (the batch flushes before the repeat).
type joinBatch struct {
	titles []core.Title
	keys   map[int64]bool
	// personFold: the person-name triggers honour the bulk marker, so new
	// credits are counted by foldPersonNames instead. bulk: the
	// transaction already holds the marker (a title fold).
	personFold, bulk bool
}

// orRows matches n rows by their full key: "(key=? AND col=?) OR ...". Each
// parameter takes its column's type on PostgreSQL (row values against a
// VALUES list would bind them as text), and both engines seek the primary
// key per term.
func orRows(n int, cols ...string) string {
	conds := make([]string, len(cols))
	for i, c := range cols {
		conds[i] = c + "=?"
	}
	term := "(" + strings.Join(conds, " AND ") + ")"
	var b strings.Builder
	for i := range n {
		if i > 0 {
			b.WriteString(" OR ")
		}
		b.WriteString(term)
	}
	return b.String()
}

func hasJoins(t *core.Title) bool {
	return t.Genres != nil || t.Keywords != nil || t.AlternativeTitles != nil || t.Studios != nil || t.Countries != nil ||
		t.Cast != nil || t.Directors != nil || t.Writers != nil || t.Crew != nil
}

func (b *joinBatch) add(ctx context.Context, tx *sql.Tx, t core.Title, now int64) error {
	if !hasJoins(&t) {
		return nil
	}
	if b.keys[int64(t.Key)] {
		if err := b.flush(ctx, tx, now); err != nil {
			return err
		}
	}
	// flush clears keys, so (re)create the set after it.
	if b.keys == nil {
		b.keys = map[int64]bool{}
	}
	b.titles = append(b.titles, t)
	b.keys[int64(t.Key)] = true
	return nil
}

// facetTables are the one-value-per-row facets a title upsert replaces.
var facetTables = []struct{ table, col string }{
	{"catalog_genres", "genre"}, {"catalog_keywords", "keyword"}, {"catalog_alt_titles", "title"},
	{"catalog_studios", "studio"}, {"catalog_countries", "country"},
}

func titleFacets(t *core.Title) [][]string {
	return [][]string{t.Genres, t.Keywords, t.AlternativeTitles, t.Studios, t.Countries}
}

// joinChunk bounds the titles one read statement covers and the rows one
// write statement carries, well inside both engines' parameter limits.
const joinChunk = 100

func (b *joinBatch) flush(ctx context.Context, tx *sql.Tx, now int64) error {
	titles := b.titles
	b.titles, b.keys = nil, nil
	if len(titles) == 0 {
		return nil
	}
	changed := map[int64]bool{}
	if err := writeFacets(ctx, tx, titles, changed); err != nil {
		return err
	}
	if err := writeCredits(ctx, tx, titles, changed, b.personFold, b.bulk); err != nil {
		return err
	}
	var keys []any
	for _, t := range titles {
		if k := int64(t.Key); changed[k] {
			keys = append(keys, k)
			delete(changed, k)
		}
	}
	for start := 0; start < len(keys); start += joinChunk {
		part := keys[start:min(start+joinChunk, len(keys))]
		if _, err := tx.ExecContext(ctx, `UPDATE catalog_titles SET fetched_at=? WHERE key IN (`+placeholders(len(part))+`)`, append([]any{now}, part...)...); err != nil {
			return err
		}
	}
	return nil
}

func writeFacets(ctx context.Context, tx *sql.Tx, titles []core.Title, changed map[int64]bool) error {
	var keys []any
	for i := range titles {
		for _, f := range titleFacets(&titles[i]) {
			if f != nil {
				keys = append(keys, int64(titles[i].Key))
				break
			}
		}
	}
	if len(keys) == 0 {
		return nil
	}
	// stored[table index][key] are the stored values. Read even for a new
	// row: facet rows may outlive a deleted title.
	stored := make([]map[int64][]string, len(facetTables))
	for i := range stored {
		stored[i] = map[int64][]string{}
	}
	for start := 0; start < len(keys); start += joinChunk {
		part := keys[start:min(start+joinChunk, len(keys))]
		ph := placeholders(len(part))
		var parts []string
		var args []any
		for i, f := range facetTables {
			parts = append(parts, fmt.Sprintf(`SELECT %d, key, %s FROM %s WHERE key IN (%s)`, i, f.col, f.table, ph))
			args = append(args, part...)
		}
		rows, err := tx.QueryContext(ctx, strings.Join(parts, " UNION ALL "), args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var i int
			var k int64
			var v string
			if err := rows.Scan(&i, &k, &v); err != nil {
				rows.Close()
				return err
			}
			stored[i][k] = append(stored[i][k], v)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	}
	for i, f := range facetTables {
		var stale, added []any
		for ti := range titles {
			vals := titleFacets(&titles[ti])[i]
			if vals == nil {
				continue
			}
			k := int64(titles[ti].Key)
			want := make(map[string]bool, len(vals))
			for _, v := range vals {
				if v != "" {
					want[v] = true
				}
			}
			have := map[string]bool{}
			for _, v := range stored[i][k] {
				have[v] = true
				if !want[v] {
					stale = append(stale, k, v)
					changed[k] = true
				}
			}
			for _, v := range vals {
				if v == "" || have[v] {
					continue
				}
				have[v] = true
				added = append(added, k, v)
				changed[k] = true
			}
		}
		for start := 0; start < len(stale); start += joinChunk * 2 {
			part := stale[start:min(start+joinChunk*2, len(stale))]
			if _, err := tx.ExecContext(ctx, `DELETE FROM `+f.table+` WHERE `+orRows(len(part)/2, "key", f.col), part...); err != nil {
				return err
			}
		}
		for start := 0; start < len(added); start += joinChunk * 2 {
			part := added[start:min(start+joinChunk*2, len(added))]
			if _, err := tx.ExecContext(ctx, `INSERT INTO `+f.table+`(key,`+f.col+`) VALUES `+valueRows(len(part)/2, 2)+` ON CONFLICT DO NOTHING`, part...); err != nil {
				return err
			}
		}
	}
	return nil
}

type creditKey struct {
	person     int64
	kind, role string
}

type creditRow struct {
	name             string
	ord              int
	thumb, provider  string
	imdb             string
	inserted, update bool
}

func hasCredits(t *core.Title) bool {
	return t.Cast != nil || t.Directors != nil || t.Writers != nil || t.Crew != nil
}

// writeCredits makes each title's stored credits exactly its cast and crew.
// The first credit of a (person, kind, role) wins, as INSERT ... DO NOTHING
// kept it.
func writeCredits(ctx context.Context, tx *sql.Tx, titles []core.Title, changed map[int64]bool, personFold, bulk bool) error {
	type titleCredits struct {
		key   int64
		want  map[creditKey]*creditRow
		order []creditKey
	}
	var batch []titleCredits
	var keys []any
	byKey := map[int64]*titleCredits{}
	for i := range titles {
		t := &titles[i]
		if !hasCredits(t) {
			continue
		}
		tc := titleCredits{key: int64(t.Key), want: map[creditKey]*creditRow{}}
		for _, group := range []struct {
			people []core.Person
			kind   string
		}{{t.Cast, "cast"}, {t.Directors, "director"}, {t.Writers, "writer"}, {t.Crew, "crew"}} {
			for ord, p := range group.people {
				k := creditKey{p.TMDB, group.kind, p.Role}
				if tc.want[k] != nil {
					continue
				}
				imdb := ""
				if p.TMDB > 0 && validPersonIMDB(p.IMDB) {
					imdb = p.IMDB
				}
				tc.want[k] = &creditRow{name: p.Name, ord: ord, thumb: p.ThumbPath, provider: p.ID, imdb: imdb, inserted: true}
				tc.order = append(tc.order, k)
			}
		}
		batch = append(batch, tc)
		keys = append(keys, tc.key)
	}
	if len(batch) == 0 {
		return nil
	}
	for i := range batch {
		byKey[batch[i].key] = &batch[i]
	}
	var stale []any
	for start := 0; start < len(keys); start += joinChunk {
		part := keys[start:min(start+joinChunk, len(keys))]
		rows, err := tx.QueryContext(ctx, `SELECT key,person_tmdb,kind,role,name,ord,thumb,provider_id FROM catalog_credits WHERE key IN (`+placeholders(len(part))+`)`, part...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var key int64
			var k creditKey
			var have creditRow
			if err := rows.Scan(&key, &k.person, &k.kind, &k.role, &have.name, &have.ord, &have.thumb, &have.provider); err != nil {
				rows.Close()
				return err
			}
			tc := byKey[key]
			w := tc.want[k]
			if w == nil {
				stale = append(stale, key, k.person, k.kind, k.role)
				changed[key] = true
				continue
			}
			w.inserted = false
			w.update = w.name != have.name || w.ord != have.ord || w.thumb != have.thumb || w.provider != have.provider
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	}
	for start := 0; start < len(stale); start += joinChunk * 4 {
		part := stale[start:min(start+joinChunk*4, len(stale))]
		if _, err := tx.ExecContext(ctx, `DELETE FROM catalog_credits WHERE `+orRows(len(part)/4, "key", "person_tmdb", "kind", "role"), part...); err != nil {
			return err
		}
	}
	var added []any
	for _, tc := range batch {
		for _, k := range tc.order {
			w := tc.want[k]
			switch {
			case w.inserted:
				added = append(added, tc.key, k.person, w.name, k.role, k.kind, w.ord, w.thumb, w.provider)
				changed[tc.key] = true
			case w.update:
				if _, err := tx.ExecContext(ctx, `UPDATE catalog_credits SET name=?,ord=?,thumb=?,provider_id=? WHERE key=? AND person_tmdb=? AND kind=? AND role=?`,
					w.name, w.ord, w.thumb, w.provider, tc.key, k.person, k.kind, k.role); err != nil {
					return err
				}
				changed[tc.key] = true
			}
		}
	}
	fold := personFold && len(added) > 0
	if fold && !bulk {
		if err := beginBulk(ctx, tx); err != nil {
			return err
		}
	}
	for start := 0; start < len(added); start += joinChunk * 8 {
		part := added[start:min(start+joinChunk*8, len(added))]
		if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_credits(key,person_tmdb,name,role,kind,ord,thumb,provider_id) VALUES `+valueRows(len(part)/8, 8)+` ON CONFLICT DO NOTHING`, part...); err != nil {
			return err
		}
	}
	if fold {
		// Every added row was inserted: its key (title, person, kind, role)
		// had no stored row and the batch holds it once.
		credits := make([]personCredit, 0, len(added)/8)
		for i := 0; i < len(added); i += 8 {
			credits = append(credits, personCredit{person: added[i+1].(int64), name: added[i+2].(string)})
		}
		if err := foldPersonNames(ctx, tx, credits); err != nil {
			return err
		}
		if !bulk {
			if err := endBulk(ctx, tx); err != nil {
				return err
			}
		}
	}
	// One upsert of the IMDb ids credited by titles whose credits changed;
	// a person appears once (PostgreSQL refuses to update a row twice in one
	// statement), the latest title's id winning as it did title by title.
	imdb := map[int64]string{}
	var people []int64
	for _, tc := range batch {
		if !changed[tc.key] {
			continue
		}
		first := map[int64]bool{}
		for _, k := range tc.order {
			if w := tc.want[k]; w.imdb != "" && !first[k.person] {
				first[k.person] = true
				if _, ok := imdb[k.person]; !ok {
					people = append(people, k.person)
				}
				imdb[k.person] = w.imdb
			}
		}
	}
	var args []any
	for _, p := range people {
		args = append(args, p, imdb[p])
	}
	for start := 0; start < len(args); start += joinChunk * 2 {
		part := args[start:min(start+joinChunk*2, len(args))]
		if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_people(person_tmdb,imdb) VALUES `+valueRows(len(part)/2, 2)+` ON CONFLICT(person_tmdb) DO UPDATE SET imdb=excluded.imdb WHERE catalog_people.imdb<>excluded.imdb`, part...); err != nil {
			return err
		}
	}
	return nil
}
