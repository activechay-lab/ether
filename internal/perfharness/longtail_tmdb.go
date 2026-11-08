package perfharness

import (
	"context"
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/tmdb"
)

// LongTailTMDB is an in-process TMDB serving a seeded long-tail universe.
// It satisfies catalog.TMDB. A title's payload is rebuilt from its seed on
// every request, so the universe costs a few bytes per title.
type LongTailTMDB struct {
	seed   uint64
	now    time.Time
	movies []ltEntry // index id-1
	shows  []ltEntry
	// byPop and byVotes list indexes into movies/shows, most first, per
	// release year and (key 0) for the whole type.
	byPop, byVotes [2]map[int][]int32
	people         int
	vocab          []string
	details        atomic.Int64
}

var _ catalog.TMDB = (*LongTailTMDB)(nil)

type ltEntry struct {
	pop   float32
	votes int32
	year  int16
	yday  int16
}

const (
	ltMovie = 0
	ltShow  = 1
	// Discover pages hold 20 results and stop at page 500, so a year
	// slice holds at most 10,000 titles. The universe keeps every year
	// under that, so a sync can reach every title.
	ltYearCap = 9500
)

func ltSalt(kind int) uint64 { return 0x6c74_0000 + uint64(kind) }

func (u *LongTailTMDB) shapeRNG(kind int, id int64) *rand.Rand {
	return rand.New(rand.NewPCG(u.seed^ltSalt(kind), uint64(id)))
}

// NewLongTailTMDB builds the universe cfg describes.
func NewLongTailTMDB(cfg LongTailConfig) *LongTailTMDB {
	share := cfg.MovieShare
	if share <= 0 {
		share = 0.7
	}
	now := cfg.Now
	if now.IsZero() {
		now = time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	}
	u := &LongTailTMDB{seed: cfg.Seed, now: now, people: 2000}
	vr := rand.New(rand.NewPCG(cfg.Seed, 0x766f6361))
	seen := map[string]bool{}
	for len(u.vocab) < 6000 {
		var b strings.Builder
		for range 1 + vr.IntN(3) {
			b.WriteString(fixtureSyllables[vr.IntN(len(fixtureSyllables))])
		}
		if w := b.String(); !seen[w] {
			seen[w] = true
			u.vocab = append(u.vocab, w)
		}
	}
	// The common English words, as on a real catalogue, are the most used.
	for i, w := range fixtureWords {
		u.vocab[i*7] = strings.ToLower(w)
	}
	nm := int(math.Round(float64(cfg.Titles) * share))
	counts := [2]int{nm, cfg.Titles - nm}
	for kind := range 2 {
		years := ltYears(counts[kind], now)
		if cfg.YearCounts[kind] != nil {
			years = years[:0]
			for y, n := range cfg.YearCounts[kind] {
				years = append(years, ltYear{y, n})
			}
			slices.SortFunc(years, func(a, b ltYear) int { return a.year - b.year })
			counts[kind] = 0
			for _, y := range years {
				counts[kind] += y.n
			}
		}
		list := make([]ltEntry, counts[kind])
		u.people += counts[kind] * 2
		id := 0
		for _, y := range years {
			for range y.n {
				rng := u.shapeRNG(kind, int64(id+1))
				s := LongTailShape(rng, ltType(kind))
				dr := rand.New(rand.NewPCG(u.seed^0x64617465, uint64(id+1)<<1|uint64(kind)))
				list[id] = ltEntry{pop: float32(s.Popularity), votes: int32(s.Votes), year: int16(y.year), yday: int16(dr.IntN(365))}
				id++
			}
		}
		if kind == ltMovie {
			u.movies = list
		} else {
			u.shows = list
		}
		u.byPop[kind], u.byVotes[kind] = map[int][]int32{}, map[int][]int32{}
		for i, e := range list {
			u.byPop[kind][int(e.year)] = append(u.byPop[kind][int(e.year)], int32(i))
		}
		all := make([]int32, len(list))
		for i := range all {
			all[i] = int32(i)
		}
		u.byPop[kind][0] = all
		for y, ids := range u.byPop[kind] {
			votes := slices.Clone(ids)
			slices.SortStableFunc(ids, func(a, b int32) int {
				return cmpDesc(list[a].pop, list[b].pop)
			})
			slices.SortStableFunc(votes, func(a, b int32) int {
				return cmpDesc(float32(list[a].votes), float32(list[b].votes))
			})
			u.byPop[kind][y], u.byVotes[kind][y] = ids, votes
		}
	}
	return u
}

func cmpDesc(a, b float32) int {
	switch {
	case a > b:
		return -1
	case a < b:
		return 1
	}
	return 0
}

func ltType(kind int) core.MediaType {
	if kind == ltShow {
		return core.TypeShow
	}
	return core.TypeMovie
}

type ltYear struct{ year, n int }

// ltYears spreads n titles over 1900..now+2, growing toward the present as
// TMDB does, with no year over ltYearCap.
func ltYears(n int, now time.Time) []ltYear {
	first, last := 1900, now.Year()+2
	w := make([]float64, last-first+1)
	for i := range w {
		y := first + i
		w[i] = math.Exp(float64(y-first) / 22)
		if y > now.Year() {
			w[i] = w[now.Year()-first] * 0.15 // announced, not yet released
		}
	}
	out := make([]int, len(w))
	remaining, capped := n, make([]bool, len(w))
	for remaining > 0 {
		total := 0.0
		for i := range w {
			if !capped[i] {
				total += w[i]
			}
		}
		if total == 0 {
			break
		}
		given := 0
		for i := range w {
			if capped[i] {
				continue
			}
			add := int(math.Floor(float64(remaining) * w[i] / total))
			if out[i]+add >= ltYearCap {
				add = ltYearCap - out[i]
				capped[i] = true
			}
			out[i] += add
			given += add
		}
		if given == 0 {
			for i := len(w) - 1; i >= 0 && remaining > given; i-- {
				if !capped[i] && out[i] < ltYearCap {
					out[i]++
					given++
				}
			}
			if given == 0 {
				break
			}
		}
		remaining -= given
	}
	years := make([]ltYear, 0, len(w))
	for i, c := range out {
		if c > 0 {
			years = append(years, ltYear{first + i, c})
		}
	}
	return years
}

func (u *LongTailTMDB) entry(kind int, id int64) (ltEntry, bool) {
	list := u.movies
	if kind == ltShow {
		list = u.shows
	}
	if id < 1 || id > int64(len(list)) {
		return ltEntry{}, false
	}
	return list[id-1], true
}

func (u *LongTailTMDB) date(e ltEntry) time.Time {
	return time.Date(int(e.year), 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, int(e.yday))
}

// gen draws a title's content after its shape, from the shape's stream.
type ltGen struct {
	u   *LongTailTMDB
	rng *rand.Rand
	zw  *rand.Zipf
}

func (u *LongTailTMDB) gen(kind int, id int64) (Shape, ltGen) {
	rng := u.shapeRNG(kind, id)
	s := LongTailShape(rng, ltType(kind))
	return s, ltGen{u: u, rng: rng, zw: rand.NewZipf(rng, 1.07, 2, uint64(len(u.vocab)-1))}
}

func (g ltGen) word() string { return g.u.vocab[g.zw.Uint64()] }

func (g ltGen) words(n int, title bool) string {
	parts := make([]string, n)
	for i := range parts {
		w := g.word()
		if title || i == 0 {
			w = strings.ToUpper(w[:1]) + w[1:]
		}
		parts[i] = w
	}
	return strings.Join(parts, " ")
}

func (g ltGen) text(chars int) string {
	if chars <= 0 {
		return ""
	}
	var b strings.Builder
	for b.Len() < chars {
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(g.word())
	}
	b.WriteByte('.')
	return b.String()
}

const base62 = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

func ltPath(rng *rand.Rand, ext string) string {
	b := make([]byte, 28, 32)
	b[0] = '/'
	for i := 1; i < len(b); i++ {
		b[i] = base62[rng.IntN(62)]
	}
	return string(append(b, ext...))
}

func (g ltGen) path(ok bool) string {
	if !ok {
		return ""
	}
	return ltPath(g.rng, ".jpg")
}

// person draws a credited person. A fifth of credits go to one-off people;
// the rest follow a power law over a pool twice the universe's size. At
// the lab's 1.08M credits that gives 65% of people one credit, 3.6 credits
// on average and about 300 at most (the lab: 61%, 2.76, 274); at 800k
// titles 52%, 2.4 and about 255.
func (g ltGen) person() (id int64, name, thumb string) {
	pool := g.u.people
	var idx int
	if g.rng.Float64() < 0.2 {
		idx = pool + int(g.rng.Uint64()%2_000_000_000)
	} else {
		idx = int(math.Floor(float64(pool) * math.Pow(g.rng.Float64(), 1.45)))
	}
	id = int64(idx + 1)
	pr := rand.New(rand.NewPCG(g.u.seed^0x70656f70, uint64(id)))
	pz := rand.NewZipf(pr, 1.07, 2, uint64(len(g.u.vocab)-1))
	cap := func(w string) string { return strings.ToUpper(w[:1]) + w[1:] }
	name = cap(g.u.vocab[pz.Uint64()]) + " " + cap(g.u.vocab[pz.Uint64()])
	p := 0.25
	if idx < pool/20 {
		p = 0.8
	}
	if pr.Float64() < p {
		thumb = ltPath(pr, ".jpg")
	}
	return id, name, thumb
}

func (g ltGen) credits(s Shape) *tmdb.Credits {
	cr := &tmdb.Credits{Cast: []tmdb.CastMember{}, Crew: []tmdb.CastMember{}}
	for i := range s.Cast {
		id, name, thumb := g.person()
		cr.Cast = append(cr.Cast, tmdb.CastMember{ID: id, Name: name, Character: g.words(1+g.rng.IntN(2), true), Order: i, ProfilePath: thumb})
	}
	crew := func(n int, jobs ...string) {
		for range n {
			id, name, thumb := g.person()
			cr.Crew = append(cr.Crew, tmdb.CastMember{ID: id, Name: name, Job: jobs[g.rng.IntN(len(jobs))], ProfilePath: thumb})
		}
	}
	crew(s.Directors, "Director")
	crew(s.Writers, "Screenplay", "Writer", "Novel", "Story")
	if s.Type == core.TypeShow {
		crew(s.Crew, "Creator", "Original Music Composer", "Director of Photography")
	} else {
		crew(s.Crew, "Original Music Composer", "Director of Photography")
	}
	// Jobs the catalogue does not keep, as TMDB sends them.
	crew(poisson(g.rng, 1+6*s.Tier), "Producer", "Editor", "Casting")
	return cr
}

func (g ltGen) keywords(n int) []tmdb.Keyword {
	out := make([]tmdb.Keyword, 0, n)
	kz := rand.NewZipf(g.rng, 1.05, 4, 29999)
	seen := map[int]bool{}
	for len(out) < n {
		k := int(kz.Uint64()) + 1
		if seen[k] {
			continue
		}
		seen[k] = true
		kr := rand.New(rand.NewPCG(g.u.seed^0x6b6579, uint64(k)))
		name := g.u.vocab[kr.IntN(len(g.u.vocab))]
		if kr.IntN(2) == 0 {
			name += " " + g.u.vocab[kr.IntN(len(g.u.vocab))]
		}
		out = append(out, tmdb.Keyword{ID: k, Name: name + " " + strconv.Itoa(k%97)})
	}
	return out
}

var ltMovieGenres = []tmdb.Genre{{ID: 18, Name: "Drama"}, {ID: 35, Name: "Comedy"}, {ID: 99, Name: "Documentary"}, {ID: 53, Name: "Thriller"}, {ID: 28, Name: "Action"}, {ID: 27, Name: "Horror"}, {ID: 10749, Name: "Romance"}, {ID: 16, Name: "Animation"}, {ID: 80, Name: "Crime"}, {ID: 10402, Name: "Music"}, {ID: 12, Name: "Adventure"}, {ID: 10751, Name: "Family"}, {ID: 14, Name: "Fantasy"}, {ID: 878, Name: "Science Fiction"}, {ID: 9648, Name: "Mystery"}, {ID: 36, Name: "History"}, {ID: 10770, Name: "TV Movie"}, {ID: 10752, Name: "War"}, {ID: 37, Name: "Western"}}

var ltTVGenres = []tmdb.Genre{{ID: 18, Name: "Drama"}, {ID: 35, Name: "Comedy"}, {ID: 99, Name: "Documentary"}, {ID: 10764, Name: "Reality"}, {ID: 16, Name: "Animation"}, {ID: 80, Name: "Crime"}, {ID: 10759, Name: "Action & Adventure"}, {ID: 10765, Name: "Sci-Fi & Fantasy"}, {ID: 10751, Name: "Family"}, {ID: 10762, Name: "Kids"}, {ID: 9648, Name: "Mystery"}, {ID: 10767, Name: "Talk"}, {ID: 10763, Name: "News"}, {ID: 10766, Name: "Soap"}, {ID: 10768, Name: "War & Politics"}, {ID: 37, Name: "Western"}}

// genres picks n distinct genres, the common ones (listed first) most.
func (g ltGen) genres(kind, n int) []tmdb.Genre {
	all := ltMovieGenres
	if kind == ltShow {
		all = ltTVGenres
	}
	var out []tmdb.Genre
	for len(out) < n {
		gen := all[int(math.Floor(float64(len(all))*math.Pow(g.rng.Float64(), 1.8)))]
		if !slices.Contains(out, gen) {
			out = append(out, gen)
		}
	}
	return out
}

var ltCountries = []tmdb.Country{{ISO3166: "US", Name: "United States of America"}, {ISO3166: "GB", Name: "United Kingdom"}, {ISO3166: "FR", Name: "France"}, {ISO3166: "JP", Name: "Japan"}, {ISO3166: "DE", Name: "Germany"}, {ISO3166: "CA", Name: "Canada"}, {ISO3166: "IN", Name: "India"}, {ISO3166: "ES", Name: "Spain"}, {ISO3166: "IT", Name: "Italy"}, {ISO3166: "KR", Name: "South Korea"}, {ISO3166: "CN", Name: "China"}, {ISO3166: "AU", Name: "Australia"}, {ISO3166: "BR", Name: "Brazil"}, {ISO3166: "MX", Name: "Mexico"}, {ISO3166: "SE", Name: "Sweden"}, {ISO3166: "RU", Name: "Russia"}, {ISO3166: "NL", Name: "Netherlands"}, {ISO3166: "DK", Name: "Denmark"}, {ISO3166: "BE", Name: "Belgium"}, {ISO3166: "AR", Name: "Argentina"}, {ISO3166: "TR", Name: "Turkey"}, {ISO3166: "PL", Name: "Poland"}, {ISO3166: "NO", Name: "Norway"}, {ISO3166: "FI", Name: "Finland"}, {ISO3166: "HK", Name: "Hong Kong"}}

func (g ltGen) countries(n int) []tmdb.Country {
	var out []tmdb.Country
	for len(out) < n {
		c := ltCountries[int(math.Floor(float64(len(ltCountries))*math.Pow(g.rng.Float64(), 2.5)))]
		if !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	return out
}

func (g ltGen) companies(n int) []tmdb.Company {
	cz := rand.NewZipf(g.rng, 1.1, 3, 39999)
	suffix := []string{"Pictures", "Films", "Studios", "Entertainment", "Productions", "Media"}
	var out []tmdb.Company
	for len(out) < n {
		k := int(cz.Uint64()) + 1
		if slices.ContainsFunc(out, func(c tmdb.Company) bool { return c.ID == k }) {
			continue
		}
		kr := rand.New(rand.NewPCG(g.u.seed^0x636f, uint64(k)))
		w := g.u.vocab[kr.IntN(len(g.u.vocab))]
		out = append(out, tmdb.Company{ID: k, Name: strings.ToUpper(w[:1]) + w[1:] + " " + suffix[k%len(suffix)]})
	}
	return out
}

func (g ltGen) altTitles(n int) *tmdb.AltTitles {
	a := &tmdb.AltTitles{Titles: []tmdb.AltTitle{}}
	for range n {
		c := ltCountries[g.rng.IntN(len(ltCountries))]
		a.Titles = append(a.Titles, tmdb.AltTitle{Country: c.ISO3166, Title: g.words(1+g.rng.IntN(4), true)})
	}
	return a
}

var ltMovieCerts = []string{"G", "PG", "PG-13", "R", "NR"}
var ltTVCerts = []string{"TV-Y", "TV-G", "TV-PG", "TV-14", "TV-MA"}

type ltReleaseCountry = struct {
	ISO3166     string                  `json:"iso_3166_1"`
	ReleaseDate []tmdb.ReleaseDateEntry `json:"release_dates"`
}

func (g ltGen) releaseDates(s Shape, released time.Time) *tmdb.ReleaseDates {
	rd := &tmdb.ReleaseDates{}
	if !s.Certified {
		return rd
	}
	for i, c := range g.countries(min(s.ReleaseCountries, len(ltCountries))) {
		cert := ""
		if c.ISO3166 == "US" || i == 0 {
			cert = ltMovieCerts[g.rng.IntN(len(ltMovieCerts))]
		}
		entries := []tmdb.ReleaseDateEntry{{Certification: cert, ReleaseDate: released.AddDate(0, 0, g.rng.IntN(40)).Format("2006-01-02T15:04:05.000Z"), Type: 3}}
		if g.rng.Float64() < 0.5 {
			entries = append(entries, tmdb.ReleaseDateEntry{Certification: cert, ReleaseDate: released.AddDate(0, 0, 60+g.rng.IntN(90)).Format("2006-01-02T15:04:05.000Z"), Type: 4})
		}
		rd.Results = append(rd.Results, ltReleaseCountry{ISO3166: c.ISO3166, ReleaseDate: entries})
	}
	return rd
}

func genreIDs(gs []tmdb.Genre) []int {
	out := make([]int, len(gs))
	for i, g := range gs {
		out[i] = g.ID
	}
	return out
}

// movieSummary is the discover/list shape of movie id.
func (u *LongTailTMDB) movieSummary(id int64) (tmdb.Movie, bool) {
	m, _, _, _, ok := u.movie(id)
	return m, ok
}

// movie builds movie id's summary and returns the stream positioned after
// it, so its details continue the same draws.
func (u *LongTailTMDB) movie(id int64) (tmdb.Movie, Shape, ltGen, []tmdb.Genre, bool) {
	e, ok := u.entry(ltMovie, id)
	if !ok {
		return tmdb.Movie{}, Shape{}, ltGen{}, nil, false
	}
	s, g := u.gen(ltMovie, id)
	title := g.words(1+g.rng.IntN(4), true)
	m := tmdb.Movie{
		ID: id, Title: title, OriginalTitle: title, Overview: g.text(s.SummaryLen),
		PosterPath: g.path(s.Poster), BackdropPath: g.path(s.Backdrop),
		ReleaseDate: u.date(e).Format("2006-01-02"), OriginalLanguage: s.Language,
		Popularity: s.Popularity, VoteAverage: s.Rating, VoteCount: s.Votes,
	}
	if s.Language != "en" && g.rng.Float64() < 0.7 {
		m.OriginalTitle = g.words(1+g.rng.IntN(3), true)
	}
	gs := g.genres(ltMovie, s.Genres)
	m.GenreIDs = genreIDs(gs)
	return m, s, g, gs, true
}

func (u *LongTailTMDB) showSummary(id int64) (tmdb.TVShow, bool) {
	t, _, _, _, ok := u.show(id)
	return t, ok
}

func (u *LongTailTMDB) show(id int64) (tmdb.TVShow, Shape, ltGen, []tmdb.Genre, bool) {
	e, ok := u.entry(ltShow, id)
	if !ok {
		return tmdb.TVShow{}, Shape{}, ltGen{}, nil, false
	}
	s, g := u.gen(ltShow, id)
	name := g.words(1+g.rng.IntN(4), true)
	t := tmdb.TVShow{
		ID: id, Name: name, OriginalName: name, Overview: g.text(s.SummaryLen),
		PosterPath: g.path(s.Poster), BackdropPath: g.path(s.Backdrop),
		FirstAirDate: u.date(e).Format("2006-01-02"), OriginalLanguage: s.Language,
		Popularity: s.Popularity, VoteAverage: s.Rating, VoteCount: s.Votes,
	}
	if s.Language != "en" && g.rng.Float64() < 0.7 {
		t.OriginalName = g.words(1+g.rng.IntN(3), true)
	}
	gs := g.genres(ltShow, s.Genres)
	t.GenreIDs = genreIDs(gs)
	t.OriginCountry = []string{ltCountries[int(math.Floor(float64(len(ltCountries))*math.Pow(g.rng.Float64(), 2.5)))].ISO3166}
	return t, s, g, gs, true
}

// recs draws n recommended titles of the same kind: mostly popular ones,
// some from anywhere in the universe.
func (u *LongTailTMDB) recs(rng *rand.Rand, kind int, self int64, n int) []int64 {
	top := u.byPop[kind][0]
	var out []int64
	for tries := 0; len(out) < n && tries < n*4; tries++ {
		var idx int32
		if rng.Float64() < 0.7 {
			idx = top[int(math.Floor(float64(min(len(top), 20000))*math.Pow(rng.Float64(), 2)))]
		} else {
			idx = int32(rng.IntN(len(top)))
		}
		id := int64(idx) + 1
		if id != self && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

func (u *LongTailTMDB) MovieDetails(_ context.Context, id int64) (*tmdb.MovieDetails, error) {
	summary, s, g, gs, ok := u.movie(id)
	if !ok {
		return nil, fmt.Errorf("longtail: %w: movie %d", core.ErrNotFound, id)
	}
	u.details.Add(1)
	released, _ := time.Parse("2006-01-02", summary.ReleaseDate)
	d := &tmdb.MovieDetails{Movie: summary, Tagline: g.text(min(s.SummaryLen/4, 60)), Status: "Released", Runtime: 70 + g.rng.IntN(80)}
	if released.After(u.now) {
		d.Status = "Post Production"
	}
	d.Genres = gs
	d.ProductionCompany = g.companies(s.Studios)
	d.ProductionCountry = g.countries(s.Countries)
	d.Credits = g.credits(s)
	d.ReleaseDates = g.releaseDates(s, released)
	d.Keywords = &tmdb.Keywords{Keywords: g.keywords(s.Keywords)}
	d.AlternativeTitles = g.altTitles(s.AltTitles)
	if s.IMDB {
		d.IMDBID = fmt.Sprintf("tt%07d", 100000+id)
	}
	d.ExternalIDs = &tmdb.ExternalIDs{ID: id, IMDBID: d.IMDBID}
	d.Images = &tmdb.Images{}
	if s.Logo {
		d.Images.Logos = []tmdb.ImageFile{{FilePath: ltPath(g.rng, ".png"), ISO639: "en"}}
	}
	if s.Collection {
		cid := 1 + int64(g.rng.IntN(max(10, len(u.movies)/40)))
		d.BelongsTo = &tmdb.CollectionRef{ID: cid, Name: g.words(2, true) + " Collection", PosterPath: g.path(true)}
	}
	rec := &tmdb.Paged[tmdb.Movie]{Page: 1, TotalPages: 1}
	for _, rid := range u.recs(g.rng, ltMovie, id, s.CrowdRecs) {
		if m, ok := u.movieSummary(rid); ok {
			rec.Results = append(rec.Results, m)
		}
	}
	rec.TotalResults = len(rec.Results)
	d.Recommendations = rec
	return d, nil
}

func (u *LongTailTMDB) TVDetails(_ context.Context, id int64) (*tmdb.TVDetails, error) {
	summary, s, g, gs, ok := u.show(id)
	if !ok {
		return nil, fmt.Errorf("longtail: %w: tv %d", core.ErrNotFound, id)
	}
	u.details.Add(1)
	first, _ := time.Parse("2006-01-02", summary.FirstAirDate)
	d := &tmdb.TVDetails{TVShow: summary, Tagline: g.text(min(s.SummaryLen/4, 60)), Status: "Ended", EpisodeRunTime: []int{20 + g.rng.IntN(40)}}
	if s.Returning {
		d.Status = "Returning Series"
	}
	d.Genres = gs
	d.ProductionCompany = g.companies(s.Studios)
	d.ProductionCountry = g.countries(s.Countries)
	d.Networks = []tmdb.Network{{ID: 1 + g.rng.IntN(800), Name: g.words(1, true) + " TV"}}
	d.Credits = g.credits(s)
	d.Keywords = &tmdb.Keywords{Results: g.keywords(s.Keywords)}
	d.AlternativeTitles = &tmdb.AltTitles{Results: g.altTitles(s.AltTitles).Titles}
	d.ExternalIDs = &tmdb.ExternalIDs{ID: id}
	if s.IMDB {
		d.ExternalIDs.IMDBID = fmt.Sprintf("tt%07d", 5000000+id)
		d.ExternalIDs.TVDBID = 70000 + id
	}
	d.Images = &tmdb.Images{}
	if s.Logo {
		d.Images.Logos = []tmdb.ImageFile{{FilePath: ltPath(g.rng, ".png"), ISO639: "en"}}
	}
	if s.Certified {
		d.ContentRatings = &tmdb.ContentRatings{}
		d.ContentRatings.Results = append(d.ContentRatings.Results, struct {
			ISO3166 string `json:"iso_3166_1"`
			Rating  string `json:"rating"`
		}{"US", ltTVCerts[g.rng.IntN(len(ltTVCerts))]})
	}
	last := first
	for n := 1; n <= s.Seasons; n++ {
		air := first.AddDate(n-1, 0, 0)
		if air.After(u.now) {
			break
		}
		last = air
		d.Seasons = append(d.Seasons, tmdb.SeasonSummary{ID: id*100 + int64(n), Name: "Season " + strconv.Itoa(n), SeasonNumber: n, EpisodeCount: s.EpisodesPerSeason, AirDate: air.Format("2006-01-02"), PosterPath: g.path(g.rng.Float64() < 0.3+0.6*s.Tier)})
	}
	d.NumberOfSeasons = len(d.Seasons)
	d.NumberOfEpisodes = len(d.Seasons) * s.EpisodesPerSeason
	d.LastAirDate = last.AddDate(0, 0, 7*(s.EpisodesPerSeason-1)).Format("2006-01-02")
	rec := &tmdb.Paged[tmdb.TVShow]{Page: 1, TotalPages: 1}
	for _, rid := range u.recs(g.rng, ltShow, id, s.CrowdRecs) {
		if t, ok := u.showSummary(rid); ok {
			rec.Results = append(rec.Results, t)
		}
	}
	rec.TotalResults = len(rec.Results)
	d.Recommendations = rec
	return d, nil
}

func (u *LongTailTMDB) SeasonDetails(_ context.Context, tvID int64, season int) (*tmdb.SeasonDetails, error) {
	summary, s, _, _, ok := u.show(tvID)
	if !ok {
		return nil, fmt.Errorf("longtail: %w: tv %d", core.ErrNotFound, tvID)
	}
	first, _ := time.Parse("2006-01-02", summary.FirstAirDate)
	air := first.AddDate(season-1, 0, 0)
	if season < 1 || season > s.Seasons || air.After(u.now) {
		return nil, fmt.Errorf("longtail: %w: tv %d season %d", core.ErrNotFound, tvID, season)
	}
	u.details.Add(1)
	g := ltGen{u: u, rng: rand.New(rand.NewPCG(u.seed^0x7365, uint64(tvID)*1000+uint64(season)))}
	g.zw = rand.NewZipf(g.rng, 1.07, 2, uint64(len(u.vocab)-1))
	sd := &tmdb.SeasonDetails{ID: tvID*100 + int64(season), Name: "Season " + strconv.Itoa(season), SeasonNumber: season, AirDate: air.Format("2006-01-02")}
	for n := 1; n <= s.EpisodesPerSeason; n++ {
		rt := 20 + g.rng.IntN(40)
		e := tmdb.Episode{ID: tvID*100000 + int64(season)*1000 + int64(n), Name: g.words(1+g.rng.IntN(3), true), Overview: g.text(60 + g.rng.IntN(160)),
			EpisodeNumber: n, SeasonNumber: season, AirDate: air.AddDate(0, 0, 7*(n-1)).Format("2006-01-02"), Runtime: &rt,
			StillPath: g.path(g.rng.Float64() < 0.8), VoteAverage: math.Round((5+g.rng.Float64()*4)*10) / 10, VoteCount: g.rng.IntN(50), ShowID: tvID}
		for _, job := range []string{"Director", "Writer"} {
			pid, name, thumb := g.person()
			e.Crew = append(e.Crew, tmdb.CastMember{ID: pid, Name: name, Job: job, ProfilePath: thumb})
		}
		for range poisson(g.rng, 1.5) {
			pid, name, thumb := g.person()
			e.GuestStars = append(e.GuestStars, tmdb.CastMember{ID: pid, Name: name, Character: g.words(1, true), ProfilePath: thumb})
		}
		sd.Episodes = append(sd.Episodes, e)
	}
	return sd, nil
}

func (u *LongTailTMDB) EpisodeExternalIDs(_ context.Context, tvID int64, season, episode int) (*tmdb.ExternalIDs, error) {
	return &tmdb.ExternalIDs{ID: tvID*100000 + int64(season)*1000 + int64(episode), IMDBID: fmt.Sprintf("tt%08d", 20000000+tvID*1000+int64(season)*50+int64(episode))}, nil
}

func (u *LongTailTMDB) CollectionDetails(_ context.Context, id int64) (*tmdb.CollectionDetails, error) {
	return &tmdb.CollectionDetails{ID: id, Name: "Collection " + strconv.FormatInt(id, 10)}, nil
}

func (u *LongTailTMDB) discover(kind int, p tmdb.DiscoverParams) []int32 {
	idx := u.byPop[kind][p.Year]
	if p.SortBy == "vote_count.desc" {
		idx = u.byVotes[kind][p.Year]
	}
	if p.VoteCountGTE > 0 {
		list := u.movies
		if kind == ltShow {
			list = u.shows
		}
		if p.SortBy == "vote_count.desc" {
			n, _ := slices.BinarySearchFunc(idx, int32(p.VoteCountGTE), func(e int32, gte int32) int {
				if list[e].votes >= gte {
					return -1
				}
				return 1
			})
			idx = idx[:n]
		} else {
			kept := make([]int32, 0, len(idx))
			for _, i := range idx {
				if int(list[i].votes) >= p.VoteCountGTE {
					kept = append(kept, i)
				}
			}
			idx = kept
		}
	}
	return idx
}

func ltPage[T any](idx []int32, page int, build func(int64) (T, bool)) *tmdb.Paged[T] {
	if page < 1 {
		page = 1
	}
	total := (len(idx) + 19) / 20
	// TMDB reports the real page count and refuses pages past 500.
	res := &tmdb.Paged[T]{Page: page, TotalPages: total, TotalResults: len(idx)}
	if page > 500 {
		return res
	}
	for i := (page - 1) * 20; i < min(len(idx), page*20); i++ {
		if t, ok := build(int64(idx[i]) + 1); ok {
			res.Results = append(res.Results, t)
		}
	}
	return res
}

func (u *LongTailTMDB) DiscoverMovies(_ context.Context, p tmdb.DiscoverParams) (*tmdb.Paged[tmdb.Movie], error) {
	return ltPage(u.discover(ltMovie, p), p.Page, u.movieSummary), nil
}

func (u *LongTailTMDB) DiscoverTV(_ context.Context, p tmdb.DiscoverParams) (*tmdb.Paged[tmdb.TVShow], error) {
	return ltPage(u.discover(ltShow, p), p.Page, u.showSummary), nil
}

func emptyPage[T any]() *tmdb.Paged[T] { return &tmdb.Paged[T]{Page: 1, TotalPages: 1} }

func (u *LongTailTMDB) SearchMovies(context.Context, string, int) (*tmdb.Paged[tmdb.Movie], error) {
	return emptyPage[tmdb.Movie](), nil
}
func (u *LongTailTMDB) SearchTV(context.Context, string, int) (*tmdb.Paged[tmdb.TVShow], error) {
	return emptyPage[tmdb.TVShow](), nil
}
func (u *LongTailTMDB) SearchMulti(context.Context, string, int) (*tmdb.Paged[tmdb.MultiResult], error) {
	return emptyPage[tmdb.MultiResult](), nil
}

// The live lists are the most popular titles.
func (u *LongTailTMDB) TrendingMovies(_ context.Context, _ string, page int) (*tmdb.Paged[tmdb.Movie], error) {
	return ltPage(u.byPop[ltMovie][0][:min(100, len(u.movies))], page, u.movieSummary), nil
}
func (u *LongTailTMDB) TrendingTV(_ context.Context, _ string, page int) (*tmdb.Paged[tmdb.TVShow], error) {
	return ltPage(u.byPop[ltShow][0][:min(100, len(u.shows))], page, u.showSummary), nil
}
func (u *LongTailTMDB) TrendingAll(context.Context, string, int) (*tmdb.Paged[tmdb.MultiResult], error) {
	return emptyPage[tmdb.MultiResult](), nil
}
func (u *LongTailTMDB) MovieList(ctx context.Context, _ string, page int) (*tmdb.Paged[tmdb.Movie], error) {
	return u.TrendingMovies(ctx, "", page)
}
func (u *LongTailTMDB) TVList(ctx context.Context, _ string, page int) (*tmdb.Paged[tmdb.TVShow], error) {
	return u.TrendingTV(ctx, "", page)
}
func (u *LongTailTMDB) MovieSimilar(context.Context, int64, int) (*tmdb.Paged[tmdb.Movie], error) {
	return emptyPage[tmdb.Movie](), nil
}
func (u *LongTailTMDB) MovieRecommendations(ctx context.Context, id int64, _ int) (*tmdb.Paged[tmdb.Movie], error) {
	d, err := u.MovieDetails(ctx, id)
	if err != nil {
		return nil, err
	}
	return d.Recommendations, nil
}
func (u *LongTailTMDB) TVSimilar(context.Context, int64, int) (*tmdb.Paged[tmdb.TVShow], error) {
	return emptyPage[tmdb.TVShow](), nil
}
func (u *LongTailTMDB) TVRecommendations(ctx context.Context, id int64, _ int) (*tmdb.Paged[tmdb.TVShow], error) {
	d, err := u.TVDetails(ctx, id)
	if err != nil {
		return nil, err
	}
	return d.Recommendations, nil
}
func (u *LongTailTMDB) FindByExternalID(context.Context, string, string) (*tmdb.FindResult, error) {
	return &tmdb.FindResult{}, nil
}
func (u *LongTailTMDB) Genres(_ context.Context, mediaType string) ([]tmdb.Genre, error) {
	if mediaType == "tv" {
		return ltTVGenres, nil
	}
	return ltMovieGenres, nil
}
