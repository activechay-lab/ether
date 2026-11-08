package perfharness

import (
	"bufio"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// OffMirror describes a catalogue as a 0.3.5 server stored it with the Off
// preset: every released title the /discover walk reached, as summary rows
// (title, popularity, votes, genres, country), with the household's
// touches in the stores that hold them. Titles, ids, original names and
// popularity are TMDB's (the daily exports); vote counts are drawn so the
// share of titles at or above the library tier's floor matches the live
// /discover counts (votes >= 10: ~107k movies, ~20k series).
type OffMirror struct {
	MovieExport, TVExport string
	// Movies and Shows are the most popular export ids stored (0.3.5's Off
	// mirror held about 680k movies and 120k series).
	Movies, Shows int
	// DetailMovies and DetailShows were opened or warmed (detail=1); each
	// opened show has DetailSeasons seasons of DetailEpisodes episodes.
	DetailMovies, DetailShows     int
	DetailSeasons, DetailEpisodes int
	// Viewers each watched, listed and favourited titles, a share of them
	// in the long tail.
	Viewers int
	// XtreamIDs assigns an Xtream compact id to every stored movie and
	// series, as a client that loaded the VOD lists has.
	XtreamIDs bool
	Now       time.Time
}

// OffMirror800k is the upgrade fixture: about 800k titles.
var OffMirror800k = OffMirror{Movies: 680_000, Shows: 120_000, DetailMovies: 3_000, DetailShows: 1_500, DetailSeasons: 3, DetailEpisodes: 8, Viewers: 4, XtreamIDs: true}

// OffMirrorSummary counts what GenerateOffMirror wrote.
type OffMirrorSummary struct {
	Movies, Shows, Seasons, Episodes int
	// AtFloor titles meet the Off floor on their stored values; TailTouched
	// are below it and named by a touch source.
	AtFloor, TailTouched int
	Rows                 map[string]int
}

type exportTitle struct {
	ID         int64   `json:"id"`
	Title      string  `json:"original_title"`
	Name       string  `json:"original_name"`
	Popularity float64 `json:"popularity"`
	Adult      bool    `json:"adult"`
	Video      bool    `json:"video"`
}

func readExport(path string, n int) ([]exportTitle, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	var out []exportTitle
	sc := bufio.NewScanner(zr)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var e exportTitle
		if json.Unmarshal(sc.Bytes(), &e) != nil || e.Adult || e.Video || e.ID <= 0 {
			continue
		}
		if e.Title == "" {
			e.Title = e.Name
		}
		out = append(out, e)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Popularity != out[j].Popularity {
			return out[i].Popularity > out[j].Popularity
		}
		return out[i].ID < out[j].ID
	})
	return out[:min(n, len(out))], nil
}

// offWriter batches multi-row INSERTs with the catalogue's triggers set
// aside (SQLite drops and restores them; PostgreSQL loads in the replica
// role, which skips them). The trigger-kept counts are rebuilt at the end.
type offWriter struct {
	ctx     context.Context
	db      *sql.DB
	conn    *sql.Conn
	tx      *sql.Tx
	pg      bool
	pending int
	batches map[string]*pendingInsert
	order   []string
	rows    map[string]int
	cols    map[string][]seedCol
	n       int64
}

type seedCol struct {
	name, typ string
	required  bool
}

func (w *offWriter) begin() error {
	if w.tx != nil {
		return nil
	}
	tx, err := w.conn.BeginTx(w.ctx, nil)
	if err != nil {
		return err
	}
	if w.pg {
		if _, err := tx.ExecContext(w.ctx, `SET LOCAL session_replication_role = replica`); err != nil {
			tx.Rollback()
			return err
		}
	}
	w.tx = tx
	return nil
}

func (w *offWriter) insert(table, cols string, args ...any) error {
	key := table + "(" + cols + ")"
	b := w.batches[key]
	if b == nil {
		b = &pendingInsert{table: table, cols: cols, width: len(args)}
		w.batches[key] = b
		w.order = append(w.order, key)
	}
	b.args = append(b.args, args...)
	b.rows++
	w.pending++
	w.rows[table]++
	if b.rows*b.width >= 30000 || b.rows >= 500 {
		if err := w.write(b); err != nil {
			return err
		}
	}
	if w.pending >= 50000 {
		return w.flush()
	}
	return nil
}

func (w *offWriter) write(b *pendingInsert) error {
	if b.rows == 0 {
		return nil
	}
	if err := w.begin(); err != nil {
		return err
	}
	tuple := "(" + strings.TrimSuffix(strings.Repeat("?,", b.width), ",") + ")"
	q := "INSERT INTO " + b.table + "(" + b.cols + ") VALUES " + strings.TrimSuffix(strings.Repeat(tuple+",", b.rows), ",")
	if _, err := w.tx.ExecContext(w.ctx, q, b.args...); err != nil {
		return fmt.Errorf("%s: %w", b.table, err)
	}
	b.args, b.rows = b.args[:0], 0
	return nil
}

func (w *offWriter) flush() error {
	w.pending = 0
	for _, k := range w.order {
		if err := w.write(w.batches[k]); err != nil {
			return err
		}
	}
	if w.tx == nil {
		return nil
	}
	err := w.tx.Commit()
	w.tx = nil
	if err == nil && !w.pg {
		_, err = w.conn.ExecContext(w.ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)
	}
	return err
}

// columns lists table's columns, marking those an INSERT must set.
func (w *offWriter) columns(table string) ([]seedCol, error) {
	if c, ok := w.cols[table]; ok {
		return c, nil
	}
	q := `SELECT name, type, ("notnull"=1 AND dflt_value IS NULL AND hidden=0) FROM pragma_table_xinfo(?) ORDER BY cid`
	if w.pg {
		q = `SELECT column_name, data_type, (is_nullable='NO' AND column_default IS NULL AND is_generated='NEVER' AND is_identity='NO') FROM information_schema.columns WHERE table_schema=current_schema() AND table_name=? ORDER BY ordinal_position`
	}
	rows, err := w.conn.QueryContext(w.ctx, q, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []seedCol
	for rows.Next() {
		var c seedCol
		if err := rows.Scan(&c.name, &c.typ, &c.required); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	w.cols[table] = out
	return out, rows.Err()
}

// fill inserts a row setting set's columns and a unique placeholder in
// every other column the table requires.
func (w *offWriter) fill(table string, set map[string]any) error {
	cols, err := w.columns(table)
	if err != nil {
		return err
	}
	if len(cols) == 0 {
		return nil
	}
	var names []string
	var args []any
	for _, c := range cols {
		if v, ok := set[c.name]; ok {
			names, args = append(names, c.name), append(args, v)
			continue
		}
		if !c.required {
			continue
		}
		w.n++
		t := strings.ToUpper(c.typ)
		switch {
		case strings.Contains(t, "INT") || strings.Contains(t, "REAL") || strings.Contains(t, "NUMERIC") || strings.Contains(t, "DOUBLE"):
			args = append(args, w.n)
		case strings.Contains(t, "BLOB") || t == "BYTEA":
			args = append(args, []byte(fmt.Sprintf("x%d", w.n)))
		default:
			args = append(args, fmt.Sprintf("x%d", w.n))
		}
		names = append(names, c.name)
	}
	return w.insert(table, strings.Join(names, ","), args...)
}

// GenerateOffMirror fills an empty, migrated database with an Off-preset
// 0.3.5 catalogue and household. The output is deterministic for o.
func GenerateOffMirror(ctx context.Context, db *sql.DB, o OffMirror) (OffMirrorSummary, error) {
	sum := OffMirrorSummary{}
	movies, err := readExport(o.MovieExport, o.Movies)
	if err != nil {
		return sum, err
	}
	shows, err := readExport(o.TVExport, o.Shows)
	if err != nil {
		return sum, err
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return sum, err
	}
	defer conn.Close()
	pg := store.DialectOf(db) == store.Postgres
	w := &offWriter{ctx: ctx, db: db, conn: conn, pg: pg, batches: map[string]*pendingInsert{}, rows: map[string]int{}, cols: map[string][]seedCol{}, n: 1 << 40}
	var restore func() error
	if !pg {
		if restore, err = suspendTriggers(ctx, db, "catalog_titles", "catalog_genres", "catalog_countries", "catalog_credits", "state_watch", "state_history", "frontend_compact_ids"); err != nil {
			return sum, err
		}
	}
	rng := rand.New(rand.NewPCG(7, uint64(o.Movies+o.Shows)))
	now := o.Now
	if now.IsZero() {
		now = time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)
	}
	floorPop, floorVotes := 2.5, 10
	// P(votes >= 10) by popularity rank r: 1/(1+(r/r0)^2.5), r0 fit to the
	// live /discover counts.
	votes := func(rank int, r0 float64) int {
		p := 1 / (1 + math.Pow(float64(rank)/r0, 2.5))
		if rng.Float64() < p {
			return 10 + int(math.Exp(rng.NormFloat64()*1.6+3))
		}
		return rng.IntN(10)
	}
	genres := fixtureGenres
	countries := []string{"US", "GB", "FR", "JP", "KR", "DE", "IN", "IT", "ES", "CA", "BR", "MX"}
	released := func() time.Time {
		span := now.AddDate(0, -2, 0).Sub(time.Date(1950, 1, 1, 0, 0, 0, 0, time.UTC))
		return time.Date(1950, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(rng.Int64N(int64(span))))
	}
	type stored struct {
		key   core.RatingKey
		floor bool
	}
	var library, tail []stored
	var tailShows []core.RatingKey
	write := func(kind core.MediaType, rank int, e exportTitle, detail bool) error {
		var key core.RatingKey
		var v int
		if kind == core.TypeMovie {
			key, v = core.MovieKey(e.ID), votes(rank, 80_000)
		} else {
			key, v = core.ShowKey(e.ID), votes(rank, 15_000)
		}
		rel := released()
		title := strings.TrimSpace(e.Title)
		if title == "" {
			title = fmt.Sprintf("Untitled %d", e.ID)
		}
		fetched := now.Add(-time.Duration(rng.Int64N(int64(30 * 24 * time.Hour)))).Unix()
		d, detailAt := 0, int64(0)
		if detail {
			d, detailAt = 1, fetched
		}
		if err := w.insert("catalog_titles", titleCols, int64(key), string(kind), e.ID, "", 0, 0, "", "", 0, 0, title, title, strings.ToLower(title), "", rel.Year(), rel.Unix(), 0, "", "en", "", float64(rng.IntN(90))/10, v, e.Popularity, "/p.jpg", "/b.jpg", 0, 0, 1, rel.Unix(), fetched, d, detailAt); err != nil {
			return err
		}
		first := rng.IntN(len(genres))
		if err := w.insert("catalog_genres", "key,genre", int64(key), genres[first]); err != nil {
			return err
		}
		if rng.IntN(2) == 0 {
			if err := w.insert("catalog_genres", "key,genre", int64(key), genres[(first+1+rng.IntN(len(genres)-1))%len(genres)]); err != nil {
				return err
			}
		}
		if err := w.insert("catalog_countries", "key,country", int64(key), countries[rng.IntN(len(countries))]); err != nil {
			return err
		}
		floor := e.Popularity >= floorPop || v >= floorVotes
		if floor {
			sum.AtFloor++
		}
		s := stored{key, floor}
		if floor || detail {
			library = append(library, s)
		} else {
			tail = append(tail, s)
			if kind == core.TypeShow {
				tailShows = append(tailShows, key)
			}
		}
		if detail {
			for c := 0; c < 8; c++ {
				if err := w.insert("catalog_credits", "key,person_tmdb,name,role,kind,ord", int64(key), 1+rng.Int64N(400_000), fmt.Sprintf("Person %d", c), "Role", "cast", c); err != nil {
					return err
				}
			}
		}
		return nil
	}
	// Opened titles: the most popular, and a share of the long tail.
	detailMovie := map[int]bool{}
	for i := 0; i < o.DetailMovies; i++ {
		if i < o.DetailMovies*5/6 {
			detailMovie[i] = true
		} else {
			detailMovie[100_000+rng.IntN(max(1, len(movies)-100_000))] = true
		}
	}
	detailShow := map[int]bool{}
	for i := 0; i < o.DetailShows; i++ {
		if i < o.DetailShows*4/5 {
			detailShow[i] = true
		} else {
			detailShow[30_000+rng.IntN(max(1, len(shows)-30_000))] = true
		}
	}
	for i, e := range movies {
		if err := write(core.TypeMovie, i, e, detailMovie[i]); err != nil {
			return sum, err
		}
		sum.Movies++
	}
	for i, e := range shows {
		if err := write(core.TypeShow, i, e, detailShow[i]); err != nil {
			return sum, err
		}
		sum.Shows++
		if !detailShow[i] {
			continue
		}
		show := core.ShowKey(e.ID)
		day := released()
		for season := 1; season <= o.DetailSeasons; season++ {
			st := fmt.Sprintf("Season %d", season)
			if err := w.insert("catalog_titles", titleCols, int64(core.SeasonKey(e.ID, season)), "season", e.ID, "", int64(show), e.ID, e.Title, st, season, 0, st, st, strings.ToLower(st), "", day.Year(), day.Unix(), 0, "", "en", "", 0, 0, 0, "/s.jpg", "", o.DetailEpisodes, o.DetailEpisodes, 0, day.Unix(), now.Unix(), 1, now.Unix()); err != nil {
				return sum, err
			}
			sum.Seasons++
			for ep := 1; ep <= o.DetailEpisodes; ep++ {
				et := fmt.Sprintf("Episode %d", ep)
				if err := w.insert("catalog_titles", titleCols, int64(core.EpisodeKey(e.ID, season, ep)), "episode", e.ID, "", int64(show), e.ID, e.Title, st, season, ep, et, et, strings.ToLower(et), "", day.Year(), day.Unix(), 2700, "", "en", "", 0, 0, 0, "", "", 0, 0, 0, day.Unix(), now.Unix(), 1, now.Unix()); err != nil {
					return sum, err
				}
				sum.Episodes++
				day = day.AddDate(0, 0, 7)
			}
		}
	}
	if err := w.flush(); err != nil {
		return sum, err
	}

	// The household. A pick is a library title three times in four, a
	// long-tail title otherwise (Off viewers open the tail).
	tailTouched := map[core.RatingKey]bool{}
	pick := func() core.RatingKey {
		if len(tail) > 0 && rng.IntN(4) == 0 {
			k := tail[rng.IntN(len(tail))].key
			tailTouched[k] = true
			return k
		}
		return library[rng.IntN(len(library))].key
	}
	nowUnix := now.Unix()
	for v := 0; v < o.Viewers; v++ {
		var user int64
		if err := conn.QueryRowContext(ctx, `INSERT INTO auth_users(username,title,is_owner,origin,active,created_at,updated_at) VALUES(?,?,?,?,1,?,?) RETURNING id`,
			fmt.Sprintf("tier%02d", v), fmt.Sprintf("tier%02d", v), map[bool]int{true: 1}[v == 0], "local", nowUnix, nowUnix).Scan(&user); err != nil {
			return sum, err
		}
		seen := map[core.RatingKey]bool{}
		used := map[string]bool{}
		once := func(table string) (core.RatingKey, bool) {
			k := pick()
			id := table + fmt.Sprint(int64(k))
			if used[id] {
				return 0, false
			}
			used[id] = true
			return k, true
		}
		for i := 0; i < 1500; i++ {
			k := pick()
			if seen[k] {
				continue
			}
			seen[k] = true
			at := nowUnix - rng.Int64N(2*365*86400)
			if err := w.insert("state_watch", "user_id,rating_key,duration_ns,view_count,counted,last_viewed_at,updated_at,watch_at", user, int64(k), int64(45*time.Minute), 1, 1, at, at, at); err != nil {
				return sum, err
			}
			for h := 0; h < 2; h++ {
				if err := w.insert("state_history", "user_id,rating_key,viewed_at,completed", user, int64(k), at-int64(h)*86400, 1); err != nil {
					return sum, err
				}
			}
		}
		for i := 0; i < 150; i++ {
			if k, ok := once("watchlist"); ok {
				if err := w.fill("watchlist_items", map[string]any{"user_id": user, "item_key": int64(k), "source": "manual", "added": nowUnix}); err != nil {
					return sum, err
				}
			}
		}
		for i := 0; i < 50; i++ {
			if k, ok := once("favorites"); ok {
				if err := w.fill("favorites_items", map[string]any{"viewer_id": user, "kind": "title", "subject_id": fmt.Sprint(int64(k))}); err != nil {
					return sum, err
				}
			}
			if k, ok := once("notinterested"); ok {
				if err := w.fill("notinterested_titles", map[string]any{"viewer_id": user, "title_key": fmt.Sprint(int64(k))}); err != nil {
					return sum, err
				}
			}
		}
		for i := 0; i < 200; i++ {
			if k, ok := once("resume"); ok {
				if err := w.fill("playback_resume", map[string]any{"user_id": user, "rating_key": int64(k)}); err != nil {
					return sum, err
				}
			}
			if err := w.fill("stats_plays", map[string]any{"user_id": user, "rating_key": int64(pick())}); err != nil {
				return sum, err
			}
		}
		for i := 0; i < 5000; i++ {
			k := library[rng.IntN(len(library))].key
			if rng.IntN(10) == 0 {
				k = pick()
			}
			if err := w.fill("personalize_item_impressions", map[string]any{"user_id": user, "item_key": int64(k)}); err != nil {
				return sum, err
			}
		}
		// An episode of a long-tail show in progress keeps that show.
		for i := 0; i < 20 && len(tailShows) > 0; i++ {
			show := tailShows[rng.IntN(len(tailShows))]
			tailTouched[show] = true
			if err := w.insert("state_watch", "user_id,rating_key,view_offset_ns,duration_ns,view_count,last_viewed_at,updated_at,watch_at", user, int64(core.EpisodeKey(show.TMDB(), 1, 1+i)), int64(10*time.Minute), int64(45*time.Minute), 0, nowUnix, nowUnix, nowUnix); err != nil {
				return sum, err
			}
		}
	}
	// Collections built from external lists: their members are kept.
	for s := 0; s < 5; s++ {
		for pos := 0; pos < 400; pos++ {
			if err := w.fill("collections_members", map[string]any{"spec_id": int64(9000 + s), "pos": pos, "key": int64(pick())}); err != nil {
				return sum, err
			}
		}
	}
	if o.XtreamIDs {
		id := 0
		for _, set := range [][]stored{library, tail} {
			for _, s := range set {
				id++
				if err := w.insert("frontend_compact_ids", "surface,kind,ref,id", "xtream", "item", fmt.Sprint(int64(s.key)), id); err != nil {
					return sum, err
				}
			}
		}
	}
	if err := w.flush(); err != nil {
		return sum, err
	}
	sum.TailTouched = len(tailTouched)
	sum.Rows = w.rows
	if restore != nil {
		if err := restore(); err != nil {
			return sum, err
		}
	}
	// What the triggers set aside would have kept.
	for _, q := range []string{
		`DELETE FROM catalog_show_episodes`,
		`INSERT INTO catalog_show_episodes(show_key, known) SELECT show_key, COUNT(*) FROM catalog_titles WHERE type='episode' AND adult=0 AND release_known=1 AND show_key<>0 GROUP BY show_key`,
		`DELETE FROM catalog_person_names`,
		`INSERT INTO catalog_person_names(person_tmdb, name, credits) SELECT person_tmdb, MAX(name), COUNT(*) FROM catalog_credits WHERE person_tmdb>0 GROUP BY person_tmdb`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			return sum, fmt.Errorf("%.60s: %w", q, err)
		}
	}
	if err := catalog.RebuildLibraryCounts(ctx, db); err != nil {
		return sum, err
	}
	if !pg {
		for _, q := range []string{
			`INSERT INTO catalog_titles_fts(catalog_titles_fts) VALUES('rebuild')`,
			`INSERT INTO catalog_person_names_fts(catalog_person_names_fts) VALUES('rebuild')`,
			`ANALYZE`,
			`PRAGMA wal_checkpoint(TRUNCATE)`,
		} {
			if _, err := db.ExecContext(ctx, q); err != nil {
				return sum, err
			}
		}
	} else if _, err := db.ExecContext(ctx, `ANALYZE`); err != nil {
		return sum, err
	}
	return sum, nil
}
