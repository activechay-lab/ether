package catalog

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/tmdb"
)

// universeTMDB is an in-process TMDB over a fixed set of movies. Its
// /discover pages hold 2 results, so the 500-page cap bites at 1,000
// results instead of 10,000 and the split logic runs on a small universe.
type universeTMDB struct {
	TMDB
	movies []uMovie
}

type uMovie struct {
	id    int64
	pop   float64
	votes int
	date  string // "" undated
	adult bool
	video bool
}

const universePage = 2

func (u *universeTMDB) DiscoverMovies(_ context.Context, p tmdb.DiscoverParams) (*tmdb.Paged[tmdb.Movie], error) {
	if p.Page > discoverMaxPages {
		return nil, fmt.Errorf("page must be less than or equal to 500")
	}
	lte := -1
	if v := p.Extra.Get("vote_count.lte"); v != "" {
		lte, _ = strconv.Atoi(v)
	}
	var hits []uMovie
	for _, m := range u.movies {
		if m.date == "" || m.video || (m.adult && !p.IncludeAdult) {
			continue // /discover never matches undated titles
		}
		if p.ReleaseDateGTE != "" && m.date < p.ReleaseDateGTE || p.ReleaseDateLTE != "" && m.date > p.ReleaseDateLTE {
			continue
		}
		if m.votes < p.VoteCountGTE || lte >= 0 && m.votes > lte {
			continue
		}
		hits = append(hits, m)
	}
	sort.Slice(hits, func(i, j int) bool {
		if p.SortBy == "vote_count.desc" && hits[i].votes != hits[j].votes {
			return hits[i].votes > hits[j].votes
		}
		if hits[i].pop != hits[j].pop {
			return hits[i].pop > hits[j].pop
		}
		return hits[i].id < hits[j].id
	})
	total := min(len(hits), 20001) // TMDB caps the reported count
	out := &tmdb.Paged[tmdb.Movie]{Page: p.Page, TotalResults: total, TotalPages: (total + universePage - 1) / universePage}
	page := max(p.Page, 1)
	for i := (page - 1) * universePage; i < min(page*universePage, len(hits)); i++ {
		out.Results = append(out.Results, hits[i].summary())
	}
	return out, nil
}

func (m uMovie) summary() tmdb.Movie {
	return tmdb.Movie{ID: m.id, Title: fmt.Sprintf("Movie %d", m.id), ReleaseDate: m.date, Popularity: m.pop, VoteCount: m.votes, VoteAverage: 6, Adult: m.adult, Video: m.video}
}

func (u *universeTMDB) MovieSummary(_ context.Context, id int64) (*tmdb.MovieDetails, error) {
	for _, m := range u.movies {
		if m.id == id {
			return &tmdb.MovieDetails{Movie: m.summary(), Status: "Released"}, nil
		}
	}
	return nil, fmt.Errorf("%w: %d", core.ErrNotFound, id)
}

func (u *universeTMDB) MovieDetails(ctx context.Context, id int64) (*tmdb.MovieDetails, error) {
	return u.MovieSummary(ctx, id)
}

func (u *universeTMDB) SearchMulti(context.Context, string, int) (*tmdb.Paged[tmdb.MultiResult], error) {
	return &tmdb.Paged[tmdb.MultiResult]{}, nil
}
func (u *universeTMDB) SearchMovies(context.Context, string, int) (*tmdb.Paged[tmdb.Movie], error) {
	return &tmdb.Paged[tmdb.Movie]{}, nil
}
func (u *universeTMDB) SearchTV(context.Context, string, int) (*tmdb.Paged[tmdb.TVShow], error) {
	return &tmdb.Paged[tmdb.TVShow]{}, nil
}
func (u *universeTMDB) MovieSimilar(context.Context, int64, int) (*tmdb.Paged[tmdb.Movie], error) {
	return &tmdb.Paged[tmdb.Movie]{}, nil
}
func (u *universeTMDB) MovieRecommendations(context.Context, int64, int) (*tmdb.Paged[tmdb.Movie], error) {
	return &tmdb.Paged[tmdb.Movie]{}, nil
}

func (u *universeTMDB) TVSummary(context.Context, int64) (*tmdb.TVDetails, error) {
	return nil, core.ErrNotFound
}

func (u *universeTMDB) DiscoverTV(context.Context, tmdb.DiscoverParams) (*tmdb.Paged[tmdb.TVShow], error) {
	return &tmdb.Paged[tmdb.TVShow]{}, nil
}
func (u *universeTMDB) Genres(context.Context, string) ([]tmdb.Genre, error) { return nil, nil }
func (u *universeTMDB) MovieList(context.Context, string, int) (*tmdb.Paged[tmdb.Movie], error) {
	return &tmdb.Paged[tmdb.Movie]{}, nil
}
func (u *universeTMDB) TVList(context.Context, string, int) (*tmdb.Paged[tmdb.TVShow], error) {
	return &tmdb.Paged[tmdb.TVShow]{}, nil
}
func (u *universeTMDB) TrendingMovies(context.Context, string, int) (*tmdb.Paged[tmdb.Movie], error) {
	return &tmdb.Paged[tmdb.Movie]{}, nil
}
func (u *universeTMDB) TrendingTV(context.Context, string, int) (*tmdb.Paged[tmdb.TVShow], error) {
	return &tmdb.Paged[tmdb.TVShow]{}, nil
}
func (u *universeTMDB) TrendingAll(context.Context, string, int) (*tmdb.Paged[tmdb.MultiResult], error) {
	return &tmdb.Paged[tmdb.MultiResult]{}, nil
}

func (u *universeTMDB) EachNamed(ctx context.Context, kind tmdb.ExportKind, fn func(tmdb.ExportRow, string) error) (tmdb.ExportInfo, error) {
	return u.Each(ctx, kind, func(r tmdb.ExportRow) error { return fn(r, fmt.Sprintf("Movie %d", r.ID)) })
}

// Each serves the universe as a daily export (movies only).
func (u *universeTMDB) Each(_ context.Context, kind tmdb.ExportKind, fn func(tmdb.ExportRow) error) (tmdb.ExportInfo, error) {
	info := tmdb.ExportInfo{Kind: kind}
	if kind != tmdb.ExportMovies {
		return info, nil
	}
	for _, m := range u.movies {
		if m.adult {
			continue // adult titles are in the adult_* files
		}
		info.Rows++
		if err := fn(tmdb.ExportRow{ID: m.id, Popularity: m.pop, Video: m.video}); err != nil {
			return info, err
		}
	}
	return info, nil
}

// newUniverse has more than the reachable 1,000 results in a year (2020),
// in a month (March 2020) and in a day (2020-03-15, split by votes), plus
// undated, unreleased, video and adult titles.
func newUniverse() *universeTMDB {
	u := &universeTMDB{}
	id := int64(0)
	add := func(date string, pop float64, votes int) *uMovie {
		id++
		pop += 0.01 // every title above a near-zero bar
		u.movies = append(u.movies, uMovie{id: id, date: date, pop: pop, votes: votes})
		return &u.movies[len(u.movies)-1]
	}
	votes := []int{0, 3, 40, 400, 4000}
	for i := range 1010 {
		add("2020-03-15", float64(i%97)/7, votes[i%5]+i%3)
	}
	for i := range 40 {
		add(fmt.Sprintf("2020-03-%02d", 1+i%28), float64(i%13), 50*i)
	}
	for i := range 220 {
		add(fmt.Sprintf("2020-%02d-%02d", 4+i%9, 1+i%28), float64(i%19), 7*i)
	}
	for i := range 400 {
		add(fmt.Sprintf("%d-%02d-%02d", 1890+i%135, 1+i%12, 1+i%28), float64(i%41)/3, i%250)
	}
	for i := range 30 {
		add("", float64(i), 200) // undated: only the export lists them
	}
	add("2031-01-01", 50, 0)                // announced, unreleased
	add("2019-12-31", 40, 300).video = true // a video release
	add("2019-12-31", 40, 300).adult = true
	add("2021-01-01", 0.2, 0) // year boundary
	add("2019-12-31", 0.2, 0)
	return u
}

func universeCatalog(t *testing.T, u *universeTMDB, cfg config.Catalog, exports bool) *Catalog {
	t.Helper()
	var opts []Option
	if exports {
		opts = append(opts, WithExports(u))
	}
	opts = append(opts, WithNow(func() time.Time { return time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC) }))
	c, err := New(store.OpenTest(t), u, cfg, nil, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func universeKeys(t *testing.T, c *Catalog, where string) map[int64]bool {
	t.Helper()
	rows, err := c.db.Query(`SELECT key FROM catalog_titles WHERE type='movie' ` + where)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[int64]bool{}
	for rows.Next() {
		var k int64
		if err := rows.Scan(&k); err != nil {
			t.Fatal(err)
		}
		out[core.RatingKey(k).TMDB()] = true
	}
	return out
}

// The walk reads every dated title exactly once though a year, a month and
// a day each exceed what one /discover query can page.
func TestDiscoverWalkReachesEveryTitleOnce(t *testing.T) {
	u := newUniverse()
	// A bar every title in the universe meets, so the walk stores all it reads.
	c := universeCatalog(t, u, config.Catalog{MinPopularity: 1e-6}, false)
	ctx := context.Background()
	seen := map[int64]int{}
	w := newSyncWriter(c)
	w.onStored = func(t core.Title) { seen[t.IDs.TMDB]++ }
	if err := c.walkDiscover(ctx, c.popularityWalk(false), w); err != nil {
		t.Fatal(err)
	}
	now := c.now().Format("2006-01-02")
	for _, m := range u.movies {
		want := 0
		if m.date != "" && m.date <= now && !m.video && !m.adult {
			want = 1
		}
		if seen[m.id] != want {
			t.Fatalf("movie %d (%s, votes %d) stored %d times, want %d", m.id, m.date, m.votes, seen[m.id], want)
		}
	}
}

// The export path and the /discover path list the same browsable titles
// for the same universe and preset, and the export path never stores a
// long-tail title (one no bar admits) as a row.
func TestExportAndDiscoverSyncAgree(t *testing.T) {
	ctx := context.Background()
	for _, cfg := range []config.Catalog{
		{},                                // Off: the floor
		{MinPopularity: 5, MinVotes: 100}, // Some
		{MinPopularity: 9, MinVotes: 1000},
		{MinVotes: 100},
	} {
		u := newUniverse()
		discover := universeCatalog(t, u, cfg, false)
		if err := discover.Sync(ctx); err != nil {
			t.Fatal(err)
		}
		export := universeCatalog(t, u, cfg, true)
		if err := export.Sync(ctx); err != nil {
			t.Fatal(err)
		}
		a, b := universeKeys(t, discover, "AND browsable=1"), universeKeys(t, export, "AND browsable=1")
		if len(a) == 0 || fmt.Sprint(sortedIDs(a)) != fmt.Sprint(sortedIDs(b)) {
			t.Fatalf("%+v: discover browsable %d, export browsable %d", cfg, len(a), len(b))
		}
		minPop, minVotes := cfg.Bars()
		for id := range universeKeys(t, export, "") {
			m := u.movies[id-1]
			if m.pop < minPop && m.votes < minVotes {
				t.Fatalf("%+v: long-tail title %d (pop %v, votes %d) stored as a row", cfg, id, m.pop, m.votes)
			}
		}
	}
}

func sortedIDs(m map[int64]bool) []int64 {
	out := make([]int64, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
