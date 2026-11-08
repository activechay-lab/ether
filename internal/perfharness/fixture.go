package perfharness

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/contentrating"
	"github.com/braedonsaunders/ether/internal/core"
)

// Scale sizes a synthetic library. Every title is browsable, detailed and
// fresh, so a request never needs TMDB: the fixture measures Ether, not a
// provider.
type Scale struct {
	Movies            int
	Shows             int
	SeasonsPerShow    int
	EpisodesPerSeason int
	// EpisodeShows is how many series have their episodes stored (0: all).
	// The catalogue stores a series' seasons with its details and its
	// episodes only once a season is opened, so on a full mirror they are
	// the household's series, not the catalogue's.
	EpisodeShows int
	// Viewers beyond the owner, each with HistoryPerViewer watched titles
	// and InProgressPerViewer partly watched ones.
	Viewers             int
	HistoryPerViewer    int
	InProgressPerViewer int
}

// Rows is the number of catalogue rows the scale produces.
func (s Scale) Rows() int {
	opened := s.Shows
	if s.EpisodeShows > 0 {
		opened = min(s.EpisodeShows, s.Shows)
	}
	return s.Movies + s.Shows*(1+s.SeasonsPerShow) + opened*s.SeasonsPerShow*s.EpisodesPerSeason
}

// Divide shrinks the library by n, keeping the household.
func (s Scale) Divide(n int) Scale {
	s.Movies, s.Shows = max(s.Movies/n, 1), max(s.Shows/n, 1)
	return s
}

// Titles is the number of movies and series.
func (s Scale) Titles() int { return s.Movies + s.Shows }

// LargeScale is the CI gate's library: 500k+ catalogue rows and a
// household with deep watch history.
var LargeScale = Scale{Movies: 250_000, Shows: 10_000, SeasonsPerShow: 4, EpisodesPerSeason: 6, Viewers: 2, HistoryPerViewer: 20_000, InProgressPerViewer: 40}

// SmallScale is LargeScale's household over a tenth of its library; the
// gate compares the two, so only the library differs.
var SmallScale = LargeScale.Divide(10)

// XLargeScale is the nightly gate's library: a full TMDB mirror, 1.28M
// titles (1,073,442 movies and 205,326 series, TMDB's dated, non-adult
// counts), each series with two seasons stored and the episodes of 300
// opened series, as the long-tail universe (longtail.go) mirrors it. Its
// pair is XLargeScale.Divide(10).
var XLargeScale = Scale{Movies: 1_073_442, Shows: 205_326, SeasonsPerShow: 2, EpisodesPerSeason: 13, EpisodeShows: 300, Viewers: 2, HistoryPerViewer: 20_000, InProgressPerViewer: 40}

var fixtureGenres = []string{"Action", "Adventure", "Animation", "Comedy", "Crime", "Documentary", "Drama", "Family", "Fantasy", "History", "Horror", "Music", "Mystery", "Romance", "Science Fiction", "Thriller", "War", "Western", "TV Movie"}

var fixtureWords = []string{"Star", "Night", "River", "Empire", "Shadow", "Garden", "Signal", "Harbor", "Winter", "Glass", "Echo", "Stone", "Summer", "Silver", "Iron", "Paper", "Ghost", "Ocean", "Crown", "Wild"}

// Generate fills an empty, migrated database with a synthetic library and
// household at scale s. The output is deterministic for a given scale and
// now.
func Generate(ctx context.Context, db *sql.DB, s Scale, now time.Time) error {
	return generate(ctx, db, s, now, false)
}

// fixtureTriggerTables are the tables whose per-row triggers (search
// indexes, person names, genre pairs, show dates, library tallies, policy
// generation) would dominate the load. Generate sets them aside and
// rebuilds what they maintain once, from the same rows; the history
// triggers stay on, they cost little.
var fixtureTriggerTables = []string{"catalog_titles", "catalog_genres", "catalog_countries", "catalog_credits", "catalog_person_names", "catalog_keywords", "catalog_alt_titles", "catalog_studios", "catalog_studio_names", "catalog_collections", "catalog_home_release"}

// generate is Generate; live keeps every trigger on instead, as production
// writes, so a test can prove the rebuild matches it.
func generate(ctx context.Context, db *sql.DB, s Scale, now time.Time, live bool) error {
	rng := rand.New(rand.NewPCG(42, uint64(s.Rows())))
	g := &generator{ctx: ctx, db: db, rng: rng, now: now, used: map[int32]bool{}}
	g.franchises = max(1, s.Movies/40)
	// TMDB's keyword and company vocabularies, as LongTailTMDB draws them.
	g.keywordPool = rand.NewZipf(rng, 1.05, 1, 30_000)
	g.studioPool = rand.NewZipf(rng, 1.05, 1, 40_000)
	restore, reindex := func() error { return nil }, func() error { return nil }
	if !live {
		var err error
		if restore, err = suspendTriggers(ctx, db, fixtureTriggerTables...); err != nil {
			return err
		}
		// A bulk load: the secondary indexes are set aside and built once
		// from the loaded rows, which is several times faster than keeping
		// 37 indexes of catalog_titles in step row by row.
		if reindex, err = setAsideIndexes(ctx, db, fixtureTriggerTables...); err != nil {
			return err
		}
	}
	if err := g.titles(s); err != nil {
		return fmt.Errorf("titles: %w", err)
	}
	if err := g.ratings(); err != nil {
		return fmt.Errorf("ratings: %w", err)
	}
	if !live {
		if err := reindex(); err != nil {
			return fmt.Errorf("indexes: %w", err)
		}
		if err := rebuildDerived(ctx, db); err != nil {
			return fmt.Errorf("rebuild: %w", err)
		}
		if err := restore(); err != nil {
			return err
		}
	}
	if err := g.viewers(s); err != nil {
		return fmt.Errorf("viewers: %w", err)
	}
	if _, err := db.ExecContext(ctx, `ANALYZE`); err != nil {
		return err
	}
	_, err := db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)
	return err
}

// rebuildDerived rebuilds, from the stored rows, every table and column the
// suspended triggers keep; TestGenerateMatchesLiveTriggers proves the
// result is what the triggers write.
func rebuildDerived(ctx context.Context, db *sql.DB) error {
	for _, q := range []string{
		// Per-series episode counts.
		`DELETE FROM catalog_show_episodes`,
		`INSERT INTO catalog_show_episodes(show_key, known)
		SELECT show_key, COUNT(*) FROM catalog_titles WHERE type='episode' AND adult=0 AND release_known=1 AND show_key<>0 GROUP BY show_key`,
		// A series' date added: its latest aired episode, else its premiere
		// (catalog 28).
		`UPDATE catalog_titles AS t SET show_added_ts=x.ts FROM (SELECT s.key, COALESCE(a.ts, CASE WHEN s.released_ts<>0 THEN s.released_ts ELSE 0 END) AS ts FROM catalog_titles s LEFT JOIN (SELECT show_key, MAX(released_ts) AS ts FROM catalog_titles WHERE type='episode' AND release_known=1 AND released_ts<=CAST(strftime('%s','now') AS INTEGER) GROUP BY show_key) AS a ON a.show_key=s.key WHERE s.type='show') AS x WHERE t.key=x.key`,
		// Genre pairs (the console's genre domain).
		`DELETE FROM catalog_genre_pairs`,
		`INSERT INTO catalog_genre_pairs(a,b,key) SELECT a.genre,b.genre,a.key FROM catalog_genres a JOIN catalog_genres b ON b.key=a.key`,
		// Credited people and their search index.
		`DELETE FROM catalog_person_names`,
		`INSERT INTO catalog_person_names(person_tmdb,name,credits) SELECT person_tmdb, MAX(name), COUNT(*) FROM catalog_credits WHERE person_tmdb>0 GROUP BY person_tmdb`,
		`INSERT INTO catalog_person_names_fts(catalog_person_names_fts) VALUES('rebuild')`,
		`INSERT INTO catalog_alt_titles_fts(catalog_alt_titles_fts) VALUES('rebuild')`,
		// The studio vocabulary and its search index.
		`DELETE FROM catalog_studio_names`,
		`INSERT INTO catalog_studio_names(studio,titles) SELECT studio, COUNT(*) FROM catalog_studios GROUP BY studio`,
		`INSERT INTO catalog_studio_names_fts(catalog_studio_names_fts) VALUES('rebuild')`,
		`INSERT INTO catalog_titles_fts(catalog_titles_fts) VALUES('rebuild')`,
		// Every catalogue write advances the viewer-policy generation.
		`UPDATE viewer_policy_epoch SET generation=generation+1 WHERE id=1`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("%.60s: %w", q, err)
		}
		// Each rebuild writes a WAL the size of its table.
		if _, err := db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
			return err
		}
	}
	// The library totals, the dashboard tally and the per-series ratings.
	return catalog.RebuildLibraryCounts(ctx, db)
}

// setAsideIndexes drops the explicit indexes on tables (primary keys and
// unique constraints stay) and returns a function that recreates them.
func setAsideIndexes(ctx context.Context, db *sql.DB, tables ...string) (func() error, error) {
	in := strings.TrimSuffix(strings.Repeat("?,", len(tables)), ",")
	args := make([]any, len(tables))
	for i, t := range tables {
		args[i] = t
	}
	rows, err := db.QueryContext(ctx, `SELECT name, sql FROM sqlite_master WHERE type='index' AND sql IS NOT NULL AND tbl_name IN (`+in+`)`, args...)
	if err != nil {
		return nil, err
	}
	var names, defs []string
	for rows.Next() {
		var name, def string
		if err := rows.Scan(&name, &def); err != nil {
			rows.Close()
			return nil, err
		}
		names, defs = append(names, name), append(defs, def)
	}
	rows.Close()
	for _, name := range names {
		if _, err := db.ExecContext(ctx, `DROP INDEX "`+name+`"`); err != nil {
			return nil, err
		}
	}
	return func() error {
		for _, def := range defs {
			if _, err := db.ExecContext(ctx, def); err != nil {
				return err
			}
			if _, err := db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
				return err
			}
		}
		return nil
	}, nil
}

// suspendTriggers drops the triggers on tables and returns a function that
// recreates them.
func suspendTriggers(ctx context.Context, db *sql.DB, tables ...string) (func() error, error) {
	in := strings.TrimSuffix(strings.Repeat("?,", len(tables)), ",")
	args := make([]any, len(tables))
	for i, t := range tables {
		args[i] = t
	}
	rows, err := db.QueryContext(ctx, `SELECT name, sql FROM sqlite_master WHERE type='trigger' AND tbl_name IN (`+in+`)`, args...)
	if err != nil {
		return nil, err
	}
	var names, defs []string
	for rows.Next() {
		var name, def string
		if err := rows.Scan(&name, &def); err != nil {
			rows.Close()
			return nil, err
		}
		names, defs = append(names, name), append(defs, def)
	}
	rows.Close()
	for _, name := range names {
		if _, err := db.ExecContext(ctx, `DROP TRIGGER "`+name+`"`); err != nil {
			return nil, err
		}
	}
	return func() error {
		for _, def := range defs {
			if _, err := db.ExecContext(ctx, def); err != nil {
				return err
			}
		}
		return nil
	}, nil
}

type generator struct {
	ctx context.Context
	db  *sql.DB
	rng *rand.Rand
	now time.Time

	tx      *sql.Tx
	pending int
	flushes int
	batches map[string]*pendingInsert
	order   []*pendingInsert // batches in first-use order: titles first
	movies  []core.RatingKey
	shows   []core.RatingKey
	eps     []core.RatingKey
	showEps []int // where each opened series' episodes start in eps

	people      personDeck
	used        map[int32]bool // people already credited on the current title
	keywordPool *rand.Zipf
	studioPool  *rand.Zipf
	franchises  int
	franchise   map[int]string
}

// Rows are written as multi-row INSERTs of fixtureRows rows, committed
// every fixtureBatch rows.
const (
	fixtureBatch = 20000
	fixtureRows  = 100
)

type pendingInsert struct {
	table, cols string
	width, rows int
	args        []any
}

// insert queues one row for table(cols); args must match cols.
func (g *generator) insert(table, cols string, args ...any) error {
	key := table + "(" + cols + ")"
	b := g.batches[key]
	if b == nil {
		if g.batches == nil {
			g.batches = map[string]*pendingInsert{}
		}
		b = &pendingInsert{table: table, cols: cols, width: len(args)}
		g.batches[key] = b
		g.order = append(g.order, b)
	}
	b.args = append(b.args, args...)
	b.rows++
	g.pending++
	if b.rows >= fixtureRows {
		if err := g.write(b); err != nil {
			return err
		}
	}
	if g.pending >= fixtureBatch {
		return g.flush()
	}
	return nil
}

func (g *generator) write(b *pendingInsert) error {
	if b.rows == 0 {
		return nil
	}
	if b.table != "catalog_titles" {
		// As the catalogue writes: a title before its detail rows.
		for _, t := range g.order {
			if t.table == "catalog_titles" && t != b {
				if err := g.write(t); err != nil {
					return err
				}
			}
		}
	}
	if g.tx == nil {
		tx, err := g.db.BeginTx(g.ctx, nil)
		if err != nil {
			return err
		}
		g.tx = tx
	}
	tuple := "(" + strings.TrimSuffix(strings.Repeat("?,", b.width), ",") + ")"
	q := "INSERT INTO " + b.table + "(" + b.cols + ") VALUES " + strings.TrimSuffix(strings.Repeat(tuple+",", b.rows), ",")
	if _, err := g.tx.ExecContext(g.ctx, q, b.args...); err != nil {
		g.tx.Rollback()
		g.tx = nil
		return err
	}
	b.args, b.rows = b.args[:0], 0
	return nil
}

func (g *generator) flush() error {
	g.pending = 0
	for _, b := range g.order {
		if err := g.write(b); err != nil {
			return err
		}
	}
	if g.tx == nil {
		return nil
	}
	err := g.tx.Commit()
	g.tx = nil
	if err != nil {
		return err
	}
	// store opens SQLite with automatic checkpoints off (the server runs a
	// background checkpointer); without one the WAL grows by gigabytes and
	// every later statement slows down.
	g.flushes++
	if g.flushes%5 == 0 {
		_, err = g.db.ExecContext(g.ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)
	}
	return err
}

const titleCols = `key,type,tmdb_id,imdb,tvdb,show_key,show_tmdb,show_title,season_title,season,episode,title,original_title,sort_title,tagline,summary,year,released_ts,primary_released_ts,last_air_ts,runtime_sec,status,language,content_rating,content_rating_source,rating,vote_count,popularity,poster,backdrop,logo,collection_id,collection_name,child_count,leaf_count,browsable,added_at,fetched_at,detail,detail_at,year_source`

// fixtureSyllables build made-up words, so a substring search matches about
// as sparsely as on a real catalogue; one title in fifty carries a common
// English word.
var fixtureSyllables = []string{"ka", "lo", "mi", "ren", "tas", "vo", "qui", "ber", "dan", "el", "fy", "gor", "hu", "ix", "jen", "kel", "mor", "nu", "ost", "pra", "ru", "sel", "tor", "ul", "vek", "wa", "xen", "yo", "zar", "bri"}

func (g *generator) word() string {
	var b strings.Builder
	for range 2 + g.rng.IntN(2) {
		b.WriteString(fixtureSyllables[g.rng.IntN(len(fixtureSyllables))])
	}
	w := b.String()
	return strings.ToUpper(w[:1]) + w[1:]
}

func (g *generator) name(n int) string {
	first := g.word()
	if g.rng.IntN(50) == 0 {
		first = fixtureWords[g.rng.IntN(len(fixtureWords))]
	}
	return first + " " + g.word() + fmt.Sprintf(" %d", n)
}

func (g *generator) released() time.Time {
	// Spread over 1950 to last month.
	span := g.now.AddDate(0, -1, 0).Sub(time.Date(1950, 1, 1, 0, 0, 0, 0, time.UTC))
	return time.Date(1950, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(g.rng.Int64N(int64(span))))
}

func (g *generator) titles(s Scale) error {
	fresh := g.now.Unix()
	for i := 1; i <= s.Movies; i++ {
		key := core.MovieKey(int64(i))
		title := g.name(i)
		rel := g.released()
		sh := g.shape(core.TypeMovie)
		tagline, logo := "", ""
		if sh.Tier >= 0.5 {
			tagline = "A synthetic tagline."
		}
		if sh.Logo {
			logo = "/l.png"
		}
		franchise, collection := 0, ""
		if sh.Collection {
			franchise = 1 + g.rng.IntN(g.franchises)
			collection = g.franchiseName(franchise)
		}
		if err := g.insert("catalog_titles", titleCols, int64(key), "movie", i, fmt.Sprintf("tt%07d", i), 0, 0, 0, "", "", 0, 0, title, title, strings.ToLower(title), tagline, "A synthetic film.", rel.Year(), rel.Unix(), rel.Unix(), 0, 5400+g.rng.IntN(3600), "Released", "en",
			[]string{"G", "PG", "PG-13", "R"}[g.rng.IntN(4)], "tmdb:US:3", sh.Rating, sh.Votes, sh.Popularity, "/p.jpg", "/b.jpg", logo, franchise, collection, 0, 0, 1, rel.Unix(), fresh, 1, fresh, "primary"); err != nil {
			return err
		}
		g.movies = append(g.movies, key)
		if err := g.details(key, sh, title); err != nil {
			return err
		}
		// The movie's home release was checked: it reached home media six
		// weeks after the cinema.
		if err := g.insert("catalog_home_release", "key,checked,earliest_ts,fallback_admitted,attempted_at", int64(key), 1, rel.Unix()+42*24*3600, 0, fresh); err != nil {
			return err
		}
	}
	for i := 1; i <= s.Shows; i++ {
		id := int64(i)
		key := core.ShowKey(id)
		title := g.name(i)
		rel := g.released()
		sh := g.shape(core.TypeShow)
		leaves := s.SeasonsPerShow * s.EpisodesPerSeason
		last := rel.AddDate(0, 0, 7*max(0, leaves-1))
		if err := g.insert("catalog_titles", titleCols, int64(key), "show", i, fmt.Sprintf("tt9%06d", i), 70000+i, 0, 0, "", "", 0, 0, title, title, strings.ToLower(title), "", "A synthetic series.", rel.Year(), rel.Unix(), 0, last.Unix(), 2700, "Ended", "en",
			[]string{"TV-G", "TV-PG", "TV-14", "TV-MA"}[g.rng.IntN(4)], "tmdb:US", sh.Rating, sh.Votes, sh.Popularity, "/p.jpg", "/b.jpg", "", 0, "", s.SeasonsPerShow, leaves, 1, rel.Unix(), fresh, 1, fresh, ""); err != nil {
			return err
		}
		g.shows = append(g.shows, key)
		if err := g.details(key, sh, title); err != nil {
			return err
		}
		day := rel
		opened := s.EpisodeShows == 0 || i <= s.EpisodeShows
		if opened {
			g.showEps = append(g.showEps, len(g.eps))
		}
		for season := 1; season <= s.SeasonsPerShow; season++ {
			skey := core.SeasonKey(id, season)
			stitle := fmt.Sprintf("Season %d", season)
			if err := g.insert("catalog_titles", titleCols, int64(skey), "season", i, "", 0, int64(key), id, title, stitle, season, 0, stitle, stitle, strings.ToLower(stitle), "", "", day.Year(), day.Unix(), 0, 0, 0, "", "en", "", "", 0, 0, 0, "/s.jpg", "", "", 0, "", s.EpisodesPerSeason, s.EpisodesPerSeason, 0, day.Unix(), fresh, 1, fresh, ""); err != nil {
				return err
			}
			for ep := 1; opened && ep <= s.EpisodesPerSeason; ep++ {
				ekey := core.EpisodeKey(id, season, ep)
				etitle := fmt.Sprintf("Episode %d", ep)
				if err := g.insert("catalog_titles", titleCols, int64(ekey), "episode", i, fmt.Sprintf("tt8%03d%02d%02d", i%1000, season, ep), 0, int64(key), id, title, stitle, season, ep, etitle, etitle, strings.ToLower(etitle), "", "", day.Year(), day.Unix(), 0, 0, 2700, "", "en", "", "", 0, 0, 0, "", "", "", 0, "", 0, 0, 0, day.Unix(), fresh, 1, fresh, ""); err != nil {
					return err
				}
				g.eps = append(g.eps, ekey)
				if err := g.details(ekey, g.shape(core.TypeEpisode), etitle); err != nil {
					return err
				}
				day = day.AddDate(0, 0, 7)
			}
		}
	}
	if err := g.franchiseRows(); err != nil {
		return err
	}
	return g.flush()
}

// franchiseName names franchise id, as TMDB's collection does.
func (g *generator) franchiseName(id int) string {
	if g.franchise == nil {
		g.franchise = map[int]string{}
	}
	name, ok := g.franchise[id]
	if !ok {
		name = madeUpWord(splitmix(uint64(id))) + " Collection"
		g.franchise[id] = name
	}
	return name
}

// franchiseRows stores every franchise a movie belongs to, as the mirror's
// collection fetch does.
func (g *generator) franchiseRows() error {
	ids := make([]int, 0, len(g.franchise))
	for id := range g.franchise {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		if err := g.insert("catalog_collections", "tmdb_id,name,summary,poster,backdrop,fetched_at", id, g.franchise[id], "A synthetic franchise.", "/c.jpg", "/cb.jpg", g.now.Unix()); err != nil {
			return err
		}
	}
	return nil
}

func (g *generator) viewers(s Scale) error {
	now := g.now.Unix()
	for v := 0; v <= s.Viewers; v++ {
		owner := 0
		name := fmt.Sprintf("viewer%02d", v)
		if v == 0 {
			owner, name = 1, "owner"
		}
		if err := g.flush(); err != nil {
			return err
		}
		var id int64
		if err := g.db.QueryRowContext(g.ctx, `INSERT INTO auth_users(username,title,is_owner,origin,active,created_at,updated_at) VALUES(?,?,?,?,1,?,?) RETURNING id`, name, name, owner, "local", now, now).Scan(&id); err != nil {
			return err
		}
		if v == RestrictedViewer {
			if err := g.restrict(id, now); err != nil {
				return err
			}
		}
		if err := g.history(id, s); err != nil {
			return err
		}
		if err := g.playlists(id); err != nil {
			return err
		}
		if err := g.favorites(id); err != nil {
			return err
		}
	}
	return g.flush()
}

// ratings finishes what the catalogue derives: seasons and episodes take
// their series' rating, and every rated title stores its rank, which the
// rating-cap SQL compares.
func (g *generator) ratings() error {
	if err := g.flush(); err != nil {
		return err
	}
	if _, err := g.db.ExecContext(g.ctx, `UPDATE catalog_titles SET content_rating=(SELECT s.content_rating FROM catalog_titles s WHERE s.key=catalog_titles.show_key)
		WHERE type IN ('season','episode') AND content_rating=''`); err != nil {
		return err
	}
	// Every rated title's rank, in one pass over the titles.
	var cases strings.Builder
	var args []any
	for _, r := range []string{"G", "PG", "PG-13", "R", "TV-G", "TV-PG", "TV-14", "TV-MA"} {
		cases.WriteString(" WHEN ? THEN ?")
		args = append(args, r, int(contentrating.LevelOf(r)))
	}
	if _, err := g.db.ExecContext(g.ctx, `UPDATE catalog_titles SET content_rating_rank=CASE content_rating`+cases.String()+` END WHERE content_rating IN ('G','PG','PG-13','R','TV-G','TV-PG','TV-14','TV-MA')`, args...); err != nil {
		return err
	}
	return nil
}

// RestrictedViewer is the fixture viewer with a rating cap and a plex.tv
// share filter, so restricted listings are measured on a full library.
const RestrictedViewer = 2

func (g *generator) restrict(user, now int64) error {
	if _, err := g.db.ExecContext(g.ctx, `UPDATE auth_users SET max_content_rating='TV-14' WHERE id=?`, user); err != nil {
		return err
	}
	_, err := g.db.ExecContext(g.ctx, `INSERT INTO auth_plex_defaults(user_id,plex_id,payload,updated_at) VALUES(?,?,?,?)`, user, 9000+user,
		`{"filterMovies":"contentRating!=NC-17","filterTelevision":"contentRating!=TV-MA%2CNC-17|label!=Hidden","hasSettings":true}`, now)
	return err
}

// A viewer's history is drawn from a taste of a fixed size, whatever the
// library's: up to 8000 movies and the episodes of up to 300 series. A
// heavy viewer watches a few hundred series, not a share of the catalogue,
// so the household stays the same when only the library grows.
const (
	tasteMovies = 8000
	tasteShows  = 300
)

func (g *generator) taste(s Scale) (movies, eps []core.RatingKey) {
	for _, i := range g.rng.Perm(len(g.movies))[:min(tasteMovies, len(g.movies))] {
		movies = append(movies, g.movies[i])
	}
	if per := s.SeasonsPerShow * s.EpisodesPerSeason; per > 0 {
		for _, i := range g.rng.Perm(len(g.showEps))[:min(tasteShows, len(g.showEps))] {
			eps = append(eps, g.eps[g.showEps[i]:g.showEps[i]+per]...)
		}
	}
	return movies, eps
}

func (g *generator) history(user int64, s Scale) error {
	seen := map[core.RatingKey]bool{}
	movies, eps := g.taste(s)
	pick := func() core.RatingKey {
		if len(eps) > 0 && g.rng.IntN(2) == 0 {
			return eps[g.rng.IntN(len(eps))]
		}
		return movies[g.rng.IntN(len(movies))]
	}
	// State times are Unix nanoseconds, as internal/state writes them.
	now := g.now.UnixNano()
	span := int64(2 * 365 * 24 * time.Hour)
	for i := 0; i < s.HistoryPerViewer; i++ {
		key := pick()
		at := now - g.rng.Int64N(span)
		if err := g.insert("state_history", "user_id,rating_key,viewed_at,completed", user, int64(key), at, 1); err != nil {
			return err
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		if err := g.insert("state_watch", "user_id,rating_key,duration_ns,view_count,counted,last_viewed_at,updated_at,watch_at", user, int64(key), int64(45*time.Minute), 1, 1, at, at, at); err != nil {
			return err
		}
	}
	for i := 0; i < s.InProgressPerViewer; i++ {
		key := pick()
		if seen[key] {
			continue
		}
		seen[key] = true
		at := now - g.rng.Int64N(int64(30*24*time.Hour))
		if err := g.insert("state_watch", "user_id,rating_key,view_offset_ns,duration_ns,view_count,last_viewed_at,updated_at,watch_at", user, int64(key), int64(20*time.Minute), int64(45*time.Minute), 0, at, at, at); err != nil {
			return err
		}
	}
	return nil
}

// favorites gives the viewer 50 favourite titles and 5 favourite people,
// newest first as favorites.Service orders them.
func (g *generator) favorites(user int64) error {
	var n int
	if err := g.db.QueryRowContext(g.ctx, `SELECT COUNT(*) FROM sqlite_master WHERE name='favorites_items'`).Scan(&n); err != nil || n == 0 {
		return err // a test database without the favourites migration
	}
	now := g.now.UnixNano()
	seen := map[string]bool{}
	for i := range 55 {
		kind, subject := "title", ""
		if i < 50 {
			key := g.movies[g.rng.IntN(len(g.movies))]
			if len(g.shows) > 0 && i%4 == 0 {
				key = g.shows[g.rng.IntN(len(g.shows))]
			}
			subject = key.String()
		} else if len(g.people.dealt) > 0 {
			kind, subject = "person", strconv.Itoa(int(g.people.dealt[g.rng.IntN(len(g.people.dealt))]))
		}
		if subject == "" || seen[kind+subject] {
			continue
		}
		seen[kind+subject] = true
		if err := g.insert("favorites_items", "viewer_id,kind,subject_id,added_at,sort_order", user, kind, subject, now-int64(i)*int64(time.Hour), 0); err != nil {
			return err
		}
	}
	return nil
}

// fixturePlaylists are each viewer's playlists by size: a short list, a
// marathon and one that holds a sizeable part of the library.
var fixturePlaylists = []int{40, 600, 3000}

func (g *generator) playlists(user int64) error {
	now := g.now.UnixNano()
	for i, n := range fixturePlaylists {
		if err := g.flush(); err != nil {
			return err
		}
		var id int64
		if err := g.db.QueryRowContext(g.ctx, `INSERT INTO state_playlists(user_id,title,smart,updated_at,added_at,guid) VALUES(?,?,0,?,?,?) RETURNING id`,
			user, fmt.Sprintf("Playlist %d", i+1), now, now, fmt.Sprintf("perf-playlist-%d-%d", user, i)).Scan(&id); err != nil {
			return err
		}
		for pos := range n {
			key := g.movies[g.rng.IntN(len(g.movies))]
			if len(g.eps) > 0 && pos%3 == 0 {
				key = g.eps[g.rng.IntN(len(g.eps))]
			}
			if err := g.insert("state_playlist_items", "playlist_id,position,rating_key", id, pos, int64(key)); err != nil {
				return err
			}
		}
	}
	return g.flush()
}
