package perfharness

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"math/rand/v2"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// The long-tail catalogue is a synthetic TMDB: a universe of movies and
// shows whose popularity, votes and detail sizes follow TMDB's long tail,
// served to the real catalogue through the catalog.TMDB interface. Every
// row it leaves behind was written by catalog.Sync, WarmDetails and Get,
// with every trigger on, exactly as a server mirroring that TMDB would
// write it.
//
// The shape model is fitted to the lab catalogue (2026-11-07): about 50k
// of its titles meet the default browsable bar and carry 20 credits, 7.8
// keywords, 2.0 genres, 3.8 alternative titles, 3.1 studios and 1.2
// countries each on average, at most 1403 credits on one long-running show.
// Below the bar the detail shrinks with popularity, as it does on TMDB.

// Shape is the counts-level description of one title: what its TMDB detail
// payload carries. LongTailShape draws it; LongTailTMDB builds the payload
// from it, and the CI fixture writes rows from it directly.
type Shape struct {
	Type       core.MediaType
	Popularity float64
	Votes      int
	Rating     float64
	// Tier is 0 for the obscure tail and 1 for a blockbuster. It scales
	// every detail count below.
	Tier float64

	Cast, Directors, Writers, Crew int
	Keywords                       int
	Genres                         int
	AltTitles                      int
	Studios                        int
	Countries                      int
	// ReleaseCountries is how many countries list release dates (movies).
	ReleaseCountries int
	// Collection is true for a movie in a franchise collection.
	Collection bool
	// CrowdRecs is the length of the first recommendations page.
	CrowdRecs int
	// Seasons and EpisodesPerSeason describe a show.
	Seasons, EpisodesPerSeason int
	// SummaryLen is the overview's length in characters (0: none).
	SummaryLen                   int
	Poster, Backdrop, Logo, IMDB bool
	Certified                    bool
	Returning                    bool
	Language                     string
}

// Browsable reports whether the shape meets the default browsable bar
// (popularity 5 or 100 votes).
func (s Shape) Browsable() bool { return s.Popularity >= 5 || s.Votes >= 100 }

// Credits is the number of credit rows the catalogue keeps for the shape:
// cast is capped at 30 by the catalogue, crew is not.
func (s Shape) Credits() int { return min(s.Cast, 30) + s.Directors + s.Writers + s.Crew }

var longTailLanguages = []string{"en", "en", "en", "en", "en", "ja", "fr", "es", "de", "ko", "it", "hi", "zh", "ru", "pt", "sv", "tr", "nl", "pl", "da"}

// LongTailShape draws one title's shape. Popularity and votes are the first
// draws, so a universe can sort by them cheaply.
func LongTailShape(rng *rand.Rand, typ core.MediaType) Shape {
	s := Shape{Type: typ}
	// About 4% of TMDB reaches popularity 5; the top is in the thousands.
	s.Popularity = math.Min(3000, 0.6*math.Pow(1-rng.Float64(), -0.66))
	// Votes follow popularity loosely; old classics keep votes after their
	// popularity fades.
	s.Votes = max(0, int(math.Exp(rng.NormFloat64()*1.6+0.3)*math.Pow(s.Popularity/0.6, 0.95))-1)
	if s.Votes > 0 {
		s.Rating = math.Round((4+rng.Float64()*4.5)*10) / 10
	}
	// Titles at the browsable bar are well documented on TMDB: the tier
	// reaches 0.7 at popularity 5 or 100 votes and 1 at popularity 12.
	popTier := math.Log(s.Popularity/0.6) / math.Log(12/0.6)
	voteTier := 0.67 * math.Log1p(float64(s.Votes)) / math.Log1p(100)
	s.Tier = math.Max(0, math.Min(1, math.Max(popTier, voteTier)))
	t := s.Tier
	s.Genres = 1 + binomial(rng, 3, 0.15+0.25*t)
	s.Keywords = poisson(rng, 0.4+9*t)
	s.AltTitles = poisson(rng, 0.3+4.5*t)
	s.Studios = poisson(rng, 0.5+3.2*t)
	s.Countries = 1 + poisson(rng, 0.25*t)
	if rng.Float64() < 0.15+0.85*t {
		s.CrowdRecs = min(20, poisson(rng, 3+22*t))
	}
	s.SummaryLen = 0
	if rng.Float64() < 0.8+0.2*t {
		s.SummaryLen = 40 + int(220*t) + rng.IntN(120)
	}
	s.Poster = rng.Float64() < 0.6+0.4*t
	s.Backdrop = rng.Float64() < 0.35+0.65*t
	s.Logo = rng.Float64() < 0.05+0.75*t
	s.IMDB = rng.Float64() < 0.5+0.5*t
	s.Certified = rng.Float64() < 0.1+0.8*t
	s.Language = longTailLanguages[rng.IntN(len(longTailLanguages))]
	switch typ {
	case core.TypeShow:
		// Fitted to the lab's detailed shows: 7.1 seasons and 313 episodes
		// at popularity 12 and up, 3.1 and 83 between 5 and 12, 1.9 and 36
		// for vote-only shows. Daily shows (soaps, talk, news) run hundreds
		// of episodes a season.
		s.Seasons = 1 + min(60, poisson(rng, 0.2+2*t+4*math.Pow(t, 6)))
		s.EpisodesPerSeason = 4 + poisson(rng, 3+9*t)
		if rng.Float64() < 0.08*t {
			s.EpisodesPerSeason = 60 + rng.IntN(240)
		}
		s.Returning = rng.Float64() < 0.1+0.3*t
		s.Cast = poisson(rng, 2+12*t)
		// Long shows credit many directors and writers; a soap opera
		// credits hundreds.
		eps := s.Seasons * s.EpisodesPerSeason
		s.Directors = poisson(rng, 0.3+float64(eps)*0.04*t)
		s.Writers = poisson(rng, 0.3+float64(eps)*0.05*t)
		if rng.Float64() < 0.004*t {
			s.Directors += rng.IntN(500)
			s.Writers += rng.IntN(900)
		}
		s.Crew = poisson(rng, 0.4+1.2*t)
	default:
		s.Cast = poisson(rng, 1.5+18*t)
		s.Directors = 0
		if rng.Float64() < 0.92 {
			s.Directors = 1 + binomial(rng, 2, 0.05)
		}
		s.Writers = poisson(rng, 0.4+1.4*t)
		s.Crew = poisson(rng, 0.2+1.6*t)
		s.Collection = rng.Float64() < 0.02+0.1*t
		if s.Certified {
			s.ReleaseCountries = 1 + poisson(rng, 9*t)
		}
	}
	return s
}

func poisson(rng *rand.Rand, mean float64) int {
	if mean <= 0 {
		return 0
	}
	if mean > 30 {
		return max(0, int(math.Round(mean+rng.NormFloat64()*math.Sqrt(mean))))
	}
	l, k, p := math.Exp(-mean), 0, 1.0
	for {
		p *= rng.Float64()
		if p <= l {
			return k
		}
		k++
	}
}

func binomial(rng *rand.Rand, n int, p float64) int {
	k := 0
	for range n {
		if rng.Float64() < p {
			k++
		}
	}
	return k
}

// LongTailConfig sizes and drives a long-tail generation.
type LongTailConfig struct {
	// Titles is the number of movies plus shows in the TMDB universe.
	Titles int
	// YearCounts, when set, fixes how many movies ([0]) and shows ([1]) the
	// universe has per release year, replacing Titles and MovieShare: for
	// example TMDB's own counts, capped at the 10,000 a discover year slice
	// can return (LoadTMDBYearCounts).
	YearCounts [2]map[int]int
	// MovieShare is the movies' share of Titles (0 = 0.7).
	MovieShare float64
	Seed       uint64
	// Now is the catalogue's clock and the universe's present.
	Now time.Time
	// HideObscure keeps the default browsable bar (popularity 5 or 100
	// votes). Off is "Hide obscure titles" turned off: everything released
	// is browsable, so the catalogue details the whole universe.
	HideObscure bool
	// OpenShows is how many of the most popular shows a household opened,
	// so their seasons and episodes are stored (0 = 300, -1 = none). It
	// does not grow with the universe: viewers open what they watch.
	OpenShows int
	// SkipSync goes straight to detailing (a database a sync already filled).
	SkipSync bool
	// DetailLimit stops warming after this many detail fetches (0 = all).
	DetailLimit int
	// Progress, if set, is called as phases advance.
	Progress func(phase string, done int)
	Log      *slog.Logger
}

// LongTailReport is what a generation did.
type LongTailReport struct {
	Universe       int
	Browsable      int // titles in the universe meeting the default bar
	Synced         time.Duration
	Filled         int // released titles detailed through Get after the sync
	FillWaited     int // of those, Gets that returned before their fetch landed
	FillTook       time.Duration
	Warmed         int
	WarmTook       time.Duration
	SeasonsOpened  int
	DetailRequests int
}

// GenerateLongTail mirrors a long-tail TMDB universe into db through the
// catalogue: a sync, then detail warming until nothing browsable is left
// undetailed, then the seasons a household opened. db must be migrated.
func GenerateLongTail(ctx context.Context, db *sql.DB, cfg LongTailConfig) (LongTailReport, error) {
	if cfg.Now.IsZero() {
		cfg.Now = time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	}
	log := cfg.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	u := NewLongTailTMDB(cfg)
	rep := LongTailReport{Universe: len(u.movies) + len(u.shows)}
	for _, list := range [][]ltEntry{u.movies, u.shows} {
		for _, e := range list {
			if e.pop >= 5 || e.votes >= 100 {
				rep.Browsable++
			}
		}
	}
	catCfg := config.Catalog{Language: "en-US", Region: "US", RefreshInterval: config.Duration(24 * time.Hour)}
	if cfg.HideObscure {
		catCfg.MinPopularity, catCfg.MinVotes = 5, 100
	}
	now := cfg.Now
	c, err := catalog.New(db, u, catCfg, log, catalog.WithNow(func() time.Time { return now }))
	if err != nil {
		return rep, err
	}
	cpCtx, stop := context.WithCancel(ctx)
	defer stop()
	go store.RunCheckpointer(cpCtx, db, func() bool { return true }, nil)
	progress := func(phase string, n int) {
		if cfg.Progress != nil {
			cfg.Progress(phase, n)
		}
	}

	start := time.Now()
	syncCtx, syncDone := context.WithCancel(ctx)
	go func() {
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-syncCtx.Done():
				return
			case <-t.C:
				p := c.SyncProgress()
				progress("sync "+p.Phase, p.TitlesAdded)
			}
		}
	}()
	if !cfg.SkipSync {
		err = c.Sync(ctx)
	}
	syncDone()
	if err != nil {
		return rep, fmt.Errorf("sync: %w", err)
	}
	rep.Synced = time.Since(start)

	// A discover year slice stops at 10,000 titles. A universe larger than
	// that (the whole of TMDB) reaches the mirror through Get, the path a
	// title outside the synced set takes when it is opened or searched: it
	// is fetched in full and stored, browsable under the bar. The synced
	// titles are detailed the same way, concurrently.
	start = time.Now()
	if !cfg.HideObscure {
		if err := fillUnsynced(ctx, db, c, u, progress, &rep); err != nil {
			return rep, err
		}
	}
	rep.FillTook = time.Since(start)

	start = time.Now()
	for cfg.DetailLimit <= 0 || rep.Warmed < cfg.DetailLimit {
		batch := 500
		if cfg.DetailLimit > 0 {
			batch = min(batch, cfg.DetailLimit-rep.Warmed)
		}
		n, err := c.WarmDetails(ctx, batch)
		if err != nil {
			return rep, fmt.Errorf("warm details: %w", err)
		}
		if n == 0 {
			break
		}
		rep.Warmed += n
		progress("details", rep.Warmed)
	}
	c.WaitRefreshes()
	rep.WarmTook = time.Since(start)

	open := cfg.OpenShows
	if open == 0 {
		open = 300
	}
	if open > 0 {
		rows, err := db.QueryContext(ctx, `SELECT tmdb_id, child_count FROM catalog_titles WHERE type='show' AND detail=1 ORDER BY popularity DESC, key LIMIT ?`, open)
		if err != nil {
			return rep, err
		}
		type show struct{ id, seasons int64 }
		var shows []show
		for rows.Next() {
			var s show
			if err := rows.Scan(&s.id, &s.seasons); err != nil {
				rows.Close()
				return rep, err
			}
			shows = append(shows, s)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return rep, err
		}
		for _, s := range shows {
			for n := 1; n <= int(s.seasons); n++ {
				if _, err := c.Get(ctx, core.SeasonKey(s.id, n)); err == nil {
					rep.SeasonsOpened++
				}
			}
			progress("seasons", rep.SeasonsOpened)
		}
		c.WaitRefreshes()
	}
	rep.DetailRequests = int(u.details.Load())
	stop()
	if err := store.Maintain(ctx, db); err != nil {
		return rep, err
	}
	if store.DialectOf(db) != store.Postgres {
		if _, err := db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
			return rep, err
		}
	}
	return rep, nil
}

// LoadTMDBYearCounts reads per-year TMDB discover totals ({"movie":{"years":
// {"2024":[total,released]}},"tv":…}). Capped, each year keeps the 10,000
// titles a discover slice returns, which is what a catalogue sync with
// "Hide obscure titles" off can mirror.
//
// Uncapped, it is the whole of TMDB: a probe that stopped at 20,001 is
// replaced by the exact count in movie_exact_truncated_years.
func LoadTMDBYearCounts(raw []byte, capped bool) ([2]map[int]int, error) {
	var doc struct {
		Movie, TV struct {
			Years map[string][]int `json:"years"`
		}
		Exact map[string]int `json:"movie_exact_truncated_years"`
	}
	var out [2]map[int]int
	if err := json.Unmarshal(raw, &doc); err != nil {
		return out, err
	}
	for kind, years := range []map[string][]int{doc.Movie.Years, doc.TV.Years} {
		out[kind] = map[int]int{}
		for y, v := range years {
			year, err := strconv.Atoi(y)
			if err != nil || len(v) == 0 {
				return out, fmt.Errorf("tmdb counts: year %q", y)
			}
			n := v[0]
			if n >= 20001 {
				exact, ok := doc.Exact[y]
				if !ok || kind != ltMovie {
					return out, fmt.Errorf("tmdb counts: year %s has no exact count", y)
				}
				n = exact
			}
			if capped {
				n = min(n, 10000)
			}
			out[kind][year] = n
		}
	}
	return out, nil
}

// fillUnsynced details every released universe title the sync did not
// store or has not detailed yet, through Get (the path WarmDetails takes),
// eight at a time (the catalogue's writes still take turns); WarmDetails
// then finds nothing left. It works in rounds of fillRound titles and
// checkpoints between them: eight readers always in flight keep a WAL
// checkpoint from ever reaching the end of the log, and the WAL grew past
// 10 GB on a 128k-title universe.
func fillUnsynced(ctx context.Context, db *sql.DB, c *catalog.Catalog, u *LongTailTMDB, progress func(string, int), rep *LongTailReport) error {
	var pending []core.RatingKey
	var failed atomic.Int64
	round := func() error {
		work := make(chan core.RatingKey)
		var wg sync.WaitGroup
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for k := range work {
					if _, err := c.Get(ctx, k); err != nil {
						failed.Add(1)
					}
				}
			}()
		}
		for _, k := range pending {
			work <- k
		}
		close(work)
		wg.Wait()
		c.WaitRefreshes()
		rep.Filled += len(pending)
		pending = pending[:0]
		progress("fill", rep.Filled)
		if err := ctx.Err(); err != nil {
			return err
		}
		return store.Checkpoint(ctx, db, true)
	}
	for kind, list := range [][]ltEntry{u.movies, u.shows} {
		for from := 0; from < len(list); from += 500 {
			to := min(len(list), from+500)
			keys := make([]any, 0, to-from)
			for i := from; i < to; i++ {
				if !u.date(list[i]).After(u.now) {
					keys = append(keys, int64(ltKey(kind, int64(i+1))))
				}
			}
			if len(keys) == 0 {
				continue
			}
			rows, err := db.QueryContext(ctx, `SELECT key FROM catalog_titles WHERE detail=1 AND key IN (`+strings.TrimSuffix(strings.Repeat("?,", len(keys)), ",")+`)`, keys...)
			if err != nil {
				return err
			}
			have := map[int64]bool{}
			for rows.Next() {
				var k int64
				if err := rows.Scan(&k); err != nil {
					rows.Close()
					return err
				}
				have[k] = true
			}
			rows.Close()
			for _, k := range keys {
				if !have[k.(int64)] {
					pending = append(pending, core.RatingKey(k.(int64)))
				}
			}
			if len(pending) >= fillRound {
				if err := round(); err != nil {
					return err
				}
			}
		}
	}
	if len(pending) > 0 {
		if err := round(); err != nil {
			return err
		}
	}
	// A Get that waited out its read budget leaves its fetch running
	// detached; WaitRefreshes and the WarmDetails pass after this finish
	// any that did not land.
	rep.FillWaited = int(failed.Load())
	return nil
}

const fillRound = 2000

func ltKey(kind int, id int64) core.RatingKey {
	if kind == ltShow {
		return core.ShowKey(id)
	}
	return core.MovieKey(id)
}
