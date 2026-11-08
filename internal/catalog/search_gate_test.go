package catalog

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/tmdb"
)

// The search-quality gate: a corpus of real queries (testdata/searchgate/
// corpus.jsonl, derived from TMDB votes and popularity by
// scripts/derive_corpus.py) run through catalog.Search, the function every
// frontend reaches, against a Some-sized library (library.jsonl.gz, the
// 2026-11-08 Some walk) with TMDB's live search replaced by recorded
// answers (tmdb_search.jsonl.gz) and, when the tail exists, the long-tail
// index built from an export sample (export_sample.jsonl.gz: every export
// title the corpus's queries can reach) or, with ETHER_SEARCH_GATE_EXPORT
// set to a directory of TMDB daily exports, the whole export.
//
// It reports MRR, top-1 and top-5 per query class and fails when a class
// falls below the committed baseline (baseline.json) or a query that ranked
// its title first in the baseline no longer does (unless allowed in
// regressions.txt with a reason).
//
//	ETHER_SEARCH_GATE_EXPORT=<dir>   full export mode (movie_ids_*.json.gz, tv_series_ids_*.json.gz)
//	ETHER_SEARCH_GATE_WRITE=1        rewrite baseline.json from this run
//	ETHER_SEARCH_GATE_REPORT=<file>  write the per-query report (JSON)
//	ETHER_SEARCH_GATE_ONLY=<substr>  run only queries containing substr (debugging)
//	ETHER_SEARCH_GATE_LATENCY=1      also time warm searches (p50/p95) and count rows read
//	ETHER_SEARCH_GATE_BAR=15,500     library = pop >= 15 or votes >= 500 (the Most preset), rest in the tail; report only

const searchGateDir = "testdata/searchgate"

// searchGateNow is the day the library, the recordings and the exports were taken.
var searchGateNow = time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)

type gateQuery struct {
	Class string `json:"class"`
	Query string `json:"query"`
	Types string `json:"types,omitempty"` // "", "m" or "tv"
	Top1  string `json:"top1"`
	// Alt are titles as good an answer as Top1 (the same match, votes
	// within 1.25x): either first counts as top-1.
	Alt  []string `json:"alt,omitempty"`
	Top5 []string `json:"top5,omitempty"`
	Why  string   `json:"why,omitempty"`
}

func (q gateQuery) id() string {
	if q.Types == "" {
		return q.Query
	}
	return q.Query + " [" + q.Types + "]"
}

// gateKey parses "m:603" or "tv:1396".
func gateKey(s string) (core.RatingKey, error) {
	kind, id, ok := strings.Cut(s, ":")
	n, err := strconv.ParseInt(id, 10, 64)
	if !ok || err != nil || n <= 0 {
		return 0, fmt.Errorf("bad key %q", s)
	}
	switch kind {
	case "m":
		return core.MovieKey(n), nil
	case "tv":
		return core.ShowKey(n), nil
	}
	return 0, fmt.Errorf("bad key %q", s)
}

func gateKeyString(k core.RatingKey) string {
	if k.Kind() == core.KindShow {
		return "tv:" + strconv.FormatInt(k.TMDB(), 10)
	}
	return "m:" + strconv.FormatInt(k.TMDB(), 10)
}

// gateLibTitle is one library row as committed (short names keep the file small).
type gateLibTitle struct {
	K      string  `json:"k"`
	T      string  `json:"t"`
	O      string  `json:"o,omitempty"`
	D      string  `json:"d,omitempty"`
	V      int     `json:"v,omitempty"`
	R      float64 `json:"r,omitempty"`
	P      float64 `json:"p,omitempty"`
	L      string  `json:"l,omitempty"`
	Poster string  `json:"pp,omitempty"`
}

func (g gateLibTitle) title() (core.Title, error) {
	key, err := gateKey(g.K)
	if err != nil {
		return core.Title{}, err
	}
	typ := core.TypeMovie
	if key.Kind() == core.KindShow {
		typ = core.TypeShow
	}
	t := core.Title{Key: key, Type: typ, IDs: core.IDs{TMDB: key.TMDB()}, Title: g.T, OriginalTitle: g.O,
		SortTitle: sortTitle(g.T), Language: g.L, Rating: g.R, VoteCount: g.V, Popularity: g.P, PosterPath: g.Poster}
	if g.O == "" {
		t.OriginalTitle = g.T
	}
	if d := tmdb.ParseDate(g.D); !d.IsZero() {
		t.Released, t.Year = d, d.Year()
	}
	return t, nil
}

func gateOpen(t testing.TB, name string) io.ReadCloser {
	t.Helper()
	f, err := os.Open(filepath.Join(searchGateDir, name))
	if err != nil {
		t.Fatalf("search gate: %v", err)
	}
	if !strings.HasSuffix(name, ".gz") {
		return f
	}
	zr, err := gzip.NewReader(f)
	if err != nil {
		f.Close()
		t.Fatalf("search gate: %s: %v", name, err)
	}
	return struct {
		io.Reader
		io.Closer
	}{zr, f}
}

func gateLines(t testing.TB, name string, each func([]byte) error) {
	t.Helper()
	r := gateOpen(t, name)
	defer r.Close()
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for line := 1; sc.Scan(); line++ {
		if len(strings.TrimSpace(sc.Text())) == 0 {
			continue
		}
		if err := each(sc.Bytes()); err != nil {
			t.Fatalf("search gate: %s:%d: %v", name, line, err)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("search gate: %s: %v", name, err)
	}
}

func loadGateCorpus(t testing.TB) []gateQuery {
	var out []gateQuery
	gateLines(t, "corpus.jsonl", func(b []byte) error {
		var q gateQuery
		if err := json.Unmarshal(b, &q); err != nil {
			return err
		}
		if _, err := gateKey(q.Top1); err != nil {
			return err
		}
		out = append(out, q)
		return nil
	})
	return out
}

func loadGateLibrary(t testing.TB) []core.Title {
	var out []core.Title
	gateLines(t, "library.jsonl.gz", func(b []byte) error {
		var g gateLibTitle
		if err := json.Unmarshal(b, &g); err != nil {
			return err
		}
		ti, err := g.title()
		if err != nil {
			return err
		}
		out = append(out, ti)
		return nil
	})
	return out
}

// gateTMDB answers searches from recordings and fails everything else, as
// an unreachable provider would.
type gateTMDB struct {
	mu     sync.Mutex
	pages  map[string]gateRecorded // path|query|page
	served map[core.RatingKey]bool // keys returned since the last reset
	calls  int
}

type gateRecorded struct {
	TotalPages int               `json:"total_pages"`
	Results    []json.RawMessage `json:"results"`
}

var errGateOffline = errors.New("search gate: offline")

func loadGateTMDB(t testing.TB) *gateTMDB {
	g := &gateTMDB{pages: map[string]gateRecorded{}, served: map[core.RatingKey]bool{}}
	gateLines(t, "tmdb_search.jsonl.gz", func(b []byte) error {
		var r struct {
			Path  string `json:"path"`
			Query string `json:"query"`
			Page  int    `json:"page"`
			gateRecorded
		}
		if err := json.Unmarshal(b, &r); err != nil {
			return err
		}
		g.pages[r.Path+"|"+r.Query+"|"+strconv.Itoa(r.Page)] = r.gateRecorded
		return nil
	})
	return g
}

func gateSearch[T any](g *gateTMDB, path, q string, page int, key func(*T) core.RatingKey) (*tmdb.Paged[T], error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls++
	rec, ok := g.pages[path+"|"+q+"|"+strconv.Itoa(page)]
	out := &tmdb.Paged[T]{Page: page, TotalPages: rec.TotalPages}
	if !ok {
		return out, nil
	}
	for _, raw := range rec.Results {
		var v T
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, err
		}
		if k := key(&v); k != 0 {
			g.served[k] = true
		}
		out.Results = append(out.Results, v)
	}
	out.TotalResults = len(out.Results)
	return out, nil
}

func (g *gateTMDB) SearchMulti(_ context.Context, q string, page int) (*tmdb.Paged[tmdb.MultiResult], error) {
	return gateSearch(g, "/search/multi", q, page, func(r *tmdb.MultiResult) core.RatingKey {
		switch r.MediaType {
		case "movie":
			return core.MovieKey(r.ID)
		case "tv":
			return core.ShowKey(r.ID)
		}
		return 0
	})
}

func (g *gateTMDB) SearchMovies(_ context.Context, q string, page int) (*tmdb.Paged[tmdb.Movie], error) {
	return gateSearch(g, "/search/movie", q, page, func(r *tmdb.Movie) core.RatingKey { return core.MovieKey(r.ID) })
}

func (g *gateTMDB) SearchTV(_ context.Context, q string, page int) (*tmdb.Paged[tmdb.TVShow], error) {
	return gateSearch(g, "/search/tv", q, page, func(r *tmdb.TVShow) core.RatingKey { return core.ShowKey(r.ID) })
}

func (g *gateTMDB) takeServed() []core.RatingKey {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]core.RatingKey, 0, len(g.served))
	for k := range g.served {
		out = append(out, k)
	}
	clear(g.served)
	return out
}

func (*gateTMDB) MovieDetails(context.Context, int64) (*tmdb.MovieDetails, error) {
	return nil, errGateOffline
}
func (*gateTMDB) TVDetails(context.Context, int64) (*tmdb.TVDetails, error) {
	return nil, errGateOffline
}
func (*gateTMDB) SeasonDetails(context.Context, int64, int) (*tmdb.SeasonDetails, error) {
	return nil, errGateOffline
}
func (*gateTMDB) EpisodeExternalIDs(context.Context, int64, int, int) (*tmdb.ExternalIDs, error) {
	return nil, errGateOffline
}
func (*gateTMDB) CollectionDetails(context.Context, int64) (*tmdb.CollectionDetails, error) {
	return nil, errGateOffline
}
func (*gateTMDB) DiscoverMovies(context.Context, tmdb.DiscoverParams) (*tmdb.Paged[tmdb.Movie], error) {
	return nil, errGateOffline
}
func (*gateTMDB) DiscoverTV(context.Context, tmdb.DiscoverParams) (*tmdb.Paged[tmdb.TVShow], error) {
	return nil, errGateOffline
}
func (*gateTMDB) TrendingMovies(context.Context, string, int) (*tmdb.Paged[tmdb.Movie], error) {
	return nil, errGateOffline
}
func (*gateTMDB) TrendingTV(context.Context, string, int) (*tmdb.Paged[tmdb.TVShow], error) {
	return nil, errGateOffline
}
func (*gateTMDB) TrendingAll(context.Context, string, int) (*tmdb.Paged[tmdb.MultiResult], error) {
	return nil, errGateOffline
}
func (*gateTMDB) MovieList(context.Context, string, int) (*tmdb.Paged[tmdb.Movie], error) {
	return nil, errGateOffline
}
func (*gateTMDB) TVList(context.Context, string, int) (*tmdb.Paged[tmdb.TVShow], error) {
	return nil, errGateOffline
}
func (*gateTMDB) MovieSimilar(context.Context, int64, int) (*tmdb.Paged[tmdb.Movie], error) {
	return nil, errGateOffline
}
func (*gateTMDB) MovieRecommendations(context.Context, int64, int) (*tmdb.Paged[tmdb.Movie], error) {
	return nil, errGateOffline
}
func (*gateTMDB) TVSimilar(context.Context, int64, int) (*tmdb.Paged[tmdb.TVShow], error) {
	return nil, errGateOffline
}
func (*gateTMDB) TVRecommendations(context.Context, int64, int) (*tmdb.Paged[tmdb.TVShow], error) {
	return nil, errGateOffline
}
func (*gateTMDB) FindByExternalID(context.Context, string, string) (*tmdb.FindResult, error) {
	return nil, errGateOffline
}
func (*gateTMDB) Genres(context.Context, string) ([]tmdb.Genre, error) { return nil, errGateOffline }

// searchGate is a catalogue holding the gate's library.
type searchGate struct {
	c       *Catalog
	fake    *gateTMDB
	library map[core.RatingKey]core.Title
	tail    int // tail titles indexed (0 before the tail exists)
}

func newSearchGate(t testing.TB) *searchGate {
	t.Helper()
	ctx := context.Background()
	fake := loadGateTMDB(t)
	c, err := New(store.OpenTest(t), fake, config.Catalog{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	c.setNow(func() time.Time { return searchGateNow })
	lib := loadGateLibrary(t)
	if bar := os.Getenv("ETHER_SEARCH_GATE_BAR"); bar != "" {
		// A smaller library (for example "15,500", the Most preset): the
		// rest of the Some library moves to the tail. Not baseline-checked.
		pop, votes, _ := strings.Cut(bar, ",")
		p, _ := strconv.ParseFloat(pop, 64)
		v, _ := strconv.Atoi(votes)
		lib = slices.DeleteFunc(lib, func(ti core.Title) bool { return ti.Popularity < p && ti.VoteCount < v })
	}
	g := &searchGate{c: c, fake: fake, library: make(map[core.RatingKey]core.Title, len(lib))}
	for _, ti := range lib {
		g.library[ti.Key] = ti
	}
	for chunk := range store.Chunks(lib, 1000) {
		if err := c.upsertMany(ctx, chunk, upsertOpts{browsable: true}); err != nil {
			t.Fatal(err)
		}
	}
	c.searchIx.fresh = nil
	ix, err := c.loadTitleIndex(ctx)
	if err != nil {
		t.Fatal(err)
	}
	c.searchIx.ix.Store(ix)
	c.searchIx.gen, c.searchIx.at = c.writeGen.Load(), searchGateNow
	g.tail = attachGateTail(t, g)
	return g
}

// reset returns the catalogue to the library alone, so every query is
// answered as a first search: today's search stores TMDB's hits (and they
// would answer the next query); the gate measures each query on its own.
func (g *searchGate) reset(t testing.TB) {
	t.Helper()
	c := g.c
	for deadline := time.Now().Add(30 * time.Second); ; {
		c.hydrateMu.Lock()
		n := len(c.hydrating)
		c.hydrateMu.Unlock()
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("search gate: refreshes still running after 30s")
		}
		time.Sleep(time.Millisecond)
	}
	ctx := context.Background()
	var extra []any
	for _, k := range g.fake.takeServed() {
		if lt, ok := g.library[k]; ok {
			if _, err := c.db.ExecContext(ctx, `UPDATE catalog_titles SET popularity=?, vote_count=?, rating=? WHERE key=?`, lt.Popularity, lt.VoteCount, lt.Rating, int64(k)); err != nil {
				t.Fatal(err)
			}
			continue
		}
		extra = append(extra, int64(k))
	}
	for chunk := range store.Chunks(extra, 200) {
		if _, err := c.db.ExecContext(ctx, `DELETE FROM catalog_titles WHERE key IN (`+placeholders(len(chunk))+`)`, chunk...); err != nil {
			t.Fatal(err)
		}
	}
	c.listSummaryMu.Lock()
	c.listSummaries = nil
	c.listSummaryMu.Unlock()
	c.cacheMu.Lock()
	c.titleCache = map[core.RatingKey]cachedStoredTitle{}
	c.cacheMu.Unlock()
	c.personMu.Lock()
	c.listMirrored = map[string]time.Time{}
	c.personMu.Unlock()
	c.listMu.Lock()
	c.listCache = map[string]listEntry{}
	c.listMu.Unlock()
	c.searchIx.freshMu.Lock()
	c.searchIx.fresh = nil
	c.searchIx.freshMu.Unlock()
}

func (q gateQuery) types() []core.MediaType {
	switch q.Types {
	case "m":
		return []core.MediaType{core.TypeMovie}
	case "tv":
		return []core.MediaType{core.TypeShow}
	}
	return nil
}

// gateResult is one query's outcome.
type gateResult struct {
	ID    string   `json:"id"`
	Class string   `json:"class"`
	Rank  int      `json:"rank"`    // of top1, 1-based; 0 when not in the first gateLimit
	Top5  bool     `json:"top5"`    // every top5 title in the first five
	Got   []string `json:"got"`     // the first five, "key title (year) score"
	Want  string   `json:"want"`    // top1
	Out   int      `json:"outside"` // results from outside the library in the first five
	Rows  int64    `json:"rows"`    // rows read by the search
	Stmts int64    `json:"stmts"`   // statements run by the search
}

const gateLimit = 20

func (g *searchGate) run(t testing.TB, q gateQuery) gateResult {
	t.Helper()
	ctx, qs := store.WithQueryStats(context.Background())
	items, err := g.c.Search(ctx, q.Query, q.types(), gateLimit)
	if err != nil {
		t.Fatalf("search %q: %v", q.Query, err)
	}
	want := map[core.RatingKey]bool{}
	for _, s := range append([]string{q.Top1}, q.Alt...) {
		k, err := gateKey(s)
		if err != nil {
			t.Fatal(err)
		}
		want[k] = true
	}
	r := gateResult{ID: q.id(), Class: q.Class, Want: q.Top1, Rows: qs.Rows(), Stmts: qs.Queries()}
	for i, it := range items {
		if want[it.Key] && r.Rank == 0 {
			r.Rank = i + 1
		}
		if i < 5 {
			r.Got = append(r.Got, fmt.Sprintf("%s %s (%d) %.3f", gateKeyString(it.Key), it.Title, it.Year, it.SearchScore))
			if _, lib := g.library[it.Key]; !lib {
				r.Out++
			}
		}
	}
	r.Top5 = r.Rank >= 1 && r.Rank <= 5
	for _, s := range q.Top5 {
		k, err := gateKey(s)
		if err != nil {
			t.Fatal(err)
		}
		in := false
		for i := 0; i < min(5, len(items)); i++ {
			in = in || items[i].Key == k
		}
		r.Top5 = r.Top5 && in
	}
	g.reset(t)
	return r
}

// gateClass is one class's summary.
type gateClass struct {
	N    int     `json:"n"`
	MRR  float64 `json:"mrr"`
	Top1 float64 `json:"top1"`
	Top5 float64 `json:"top5"`
}

type gateBaseline struct {
	Note    string               `json:"note"`
	Classes map[string]gateClass `json:"classes"`
	Ranks   map[string]int       `json:"ranks"` // query id -> rank of its top1
}

func summarize(results []gateResult) map[string]gateClass {
	acc := map[string]*gateClass{}
	for _, r := range results {
		for _, cl := range []string{r.Class, "all"} {
			a := acc[cl]
			if a == nil {
				a = &gateClass{}
				acc[cl] = a
			}
			a.N++
			if r.Rank > 0 {
				a.MRR += 1 / float64(r.Rank)
			}
			if r.Rank == 1 {
				a.Top1++
			}
			if r.Top5 {
				a.Top5++
			}
		}
	}
	out := map[string]gateClass{}
	for cl, a := range acc {
		n := float64(a.N)
		out[cl] = gateClass{N: a.N, MRR: round4(a.MRR / n), Top1: round4(a.Top1 / n), Top5: round4(a.Top5 / n)}
	}
	return out
}

func round4(f float64) float64 { return math.Round(f*1e4) / 1e4 }

func loadGateRegressions(t testing.TB) map[string]string {
	out := map[string]string{}
	f, err := os.Open(filepath.Join(searchGateDir, "regressions.txt"))
	if errors.Is(err, os.ErrNotExist) {
		return out
	}
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		id, why, _ := strings.Cut(line, "\t")
		out[id] = why
	}
	return out
}

func TestSearchQualityGate(t *testing.T) {
	if testing.Short() {
		t.Skip("search quality gate builds a 38k-title library")
	}
	corpus := loadGateCorpus(t)
	g := newSearchGate(t)
	only := os.Getenv("ETHER_SEARCH_GATE_ONLY")
	var results []gateResult
	for _, q := range corpus {
		if only != "" && !strings.Contains(q.id(), only) {
			continue
		}
		results = append(results, g.run(t, q))
	}
	classes := summarize(results)
	names := make([]string, 0, len(classes))
	for cl := range classes {
		names = append(names, cl)
	}
	sort.Strings(names)
	mode := "library + recorded TMDB (no tail)"
	if g.tail > 0 {
		mode = fmt.Sprintf("library + recorded TMDB + tail of %d titles", g.tail)
	}
	t.Logf("search gate: %d queries, library %d titles, %s", len(results), len(g.library), mode)
	t.Logf("%-10s %4s %7s %7s %7s", "class", "n", "MRR", "top1", "top5")
	for _, cl := range names {
		s := classes[cl]
		t.Logf("%-10s %4d %7.4f %7.4f %7.4f", cl, s.N, s.MRR, s.Top1, s.Top5)
	}
	for _, r := range results {
		if r.Rank != 1 || !r.Top5 {
			t.Logf("miss %-10s %-34q rank %2d top5 %-5v want %s got %s", r.Class, r.ID, r.Rank, r.Top5, r.Want, strings.Join(r.Got, " | "))
		}
	}
	if path := os.Getenv("ETHER_SEARCH_GATE_REPORT"); path != "" {
		b, _ := json.MarshalIndent(map[string]any{"mode": mode, "classes": classes, "results": results}, "", " ")
		if err := os.WriteFile(path, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if only != "" || os.Getenv("ETHER_SEARCH_GATE_BAR") != "" {
		return
	}
	ranks := map[string]int{}
	for _, r := range results {
		ranks[r.ID] = r.Rank
	}
	basePath := filepath.Join(searchGateDir, "baseline.json")
	if os.Getenv("ETHER_SEARCH_GATE_WRITE") != "" {
		b, _ := json.MarshalIndent(gateBaseline{Note: mode, Classes: classes, Ranks: ranks}, "", " ")
		if err := os.WriteFile(basePath, append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s", basePath)
		return
	}
	raw, err := os.ReadFile(basePath)
	if err != nil {
		t.Fatalf("search gate: no baseline: %v", err)
	}
	var base gateBaseline
	if err := json.Unmarshal(raw, &base); err != nil {
		t.Fatal(err)
	}
	for _, cl := range names {
		b, ok := base.Classes[cl]
		s := classes[cl]
		if !ok {
			continue
		}
		if s.MRR+1e-9 < b.MRR || s.Top1+1e-9 < b.Top1 || s.Top5+1e-9 < b.Top5 {
			t.Errorf("class %s fell below the baseline: MRR %.4f (was %.4f), top1 %.4f (was %.4f), top5 %.4f (was %.4f)", cl, s.MRR, b.MRR, s.Top1, b.Top1, s.Top5, b.Top5)
		}
	}
	allowed := loadGateRegressions(t)
	for _, r := range results {
		if base.Ranks[r.ID] == 1 && r.Rank != 1 {
			if why, ok := allowed[r.ID]; ok {
				t.Logf("allowed regression %q: %s", r.ID, why)
				continue
			}
			t.Errorf("regression: %q ranked %s first in the baseline, now at %d: %s", r.ID, r.Want, r.Rank, strings.Join(r.Got, " | "))
		}
	}
	if os.Getenv("ETHER_SEARCH_GATE_LATENCY") != "" {
		g.latency(t, corpus)
	}
}

// latency times warm searches (each query run once to warm, then timed)
// and reports rows read; wall time on a loaded machine is a rough guide,
// rows and statements are the work metric.
func (g *searchGate) latency(t *testing.T, corpus []gateQuery) {
	var durs []time.Duration
	var rows, stmts []int64
	for round := 0; round < 2; round++ {
		for _, q := range corpus {
			ctx, qs := store.WithQueryStats(context.Background())
			start := time.Now()
			if _, err := g.c.Search(ctx, q.Query, q.types(), gateLimit); err != nil {
				t.Fatal(err)
			}
			d := time.Since(start)
			if round == 1 {
				durs = append(durs, d)
				rows = append(rows, qs.Rows())
				stmts = append(stmts, qs.Queries())
			}
			g.reset(t)
		}
	}
	slices.Sort(durs)
	slices.Sort(rows)
	slices.Sort(stmts)
	pct := func(n int, p float64) int { return min(n-1, int(math.Ceil(p*float64(n)))-1) }
	t.Logf("warm catalog.Search over %d queries: p50 %v p95 %v max %v; rows read p50 %d p95 %d max %d; statements p50 %d max %d",
		len(durs), durs[pct(len(durs), .5)], durs[pct(len(durs), .95)], durs[len(durs)-1],
		rows[pct(len(rows), .5)], rows[pct(len(rows), .95)], rows[len(rows)-1], stmts[pct(len(stmts), .5)], stmts[len(stmts)-1])
}
