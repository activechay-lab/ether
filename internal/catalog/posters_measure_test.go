package catalog

// Poster measurement on the real TMDB export and a real-style query corpus.
// Not part of CI: it needs the 33 MB exports and recorded TMDB answers.
//
//	ETHER_POSTER_MEASURE=<dir> go test ./internal/catalog -run TestPosterMeasure -v
//
// <dir> holds movie_ids_11_07.json.gz, tv_series_ids_11_07.json.gz,
// library.jsonl (K's Some library: kind,id,title,original,date,votes,pop,
// poster), intents.txt (K's corpus) and, for the replay, tail_lean.jsonl
// (kind,id,status,ms,poster,date: one recorded lean /movie|/tv/{id} answer
// per tail hit). Without tail_lean.jsonl it writes tail_ids.txt, the ids to
// record, and stops.

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/artwork"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/tmdb"
)

type measureTitle struct {
	Kind     string  `json:"kind"`
	ID       int64   `json:"id"`
	Title    string  `json:"title"`
	Original string  `json:"original"`
	Date     string  `json:"date"`
	Votes    int     `json:"votes"`
	Pop      float64 `json:"pop"`
	Poster   string  `json:"poster"`
}

type measureLean struct {
	Kind   string  `json:"kind"`
	ID     int64   `json:"id"`
	Status int     `json:"status"`
	MS     float64 `json:"ms"`
	Poster string  `json:"poster"`
	Date   string  `json:"date"`
	Title  string  `json:"title"`
}

func measureKey(kind string, id int64) core.RatingKey {
	if kind == "tv" {
		return core.ShowKey(id)
	}
	return core.MovieKey(id)
}

func measureIndex(titles []measureTitle) *titleIndex {
	sort.SliceStable(titles, func(i, j int) bool { return titles[i].Pop > titles[j].Pop })
	ix := &titleIndex{}
	b := newTitleIndexBuilder()
	for _, t := range titles {
		kind := titleKindMovie
		if t.Kind == "tv" {
			kind = titleKindShow
		}
		year := 0
		if len(t.Date) >= 4 {
			year, _ = strconv.Atoi(t.Date[:4])
		}
		ix.keys = append(ix.keys, int64(measureKey(t.Kind, t.ID)))
		ix.kinds = append(ix.kinds, kind)
		ix.pops = append(ix.pops, float32(t.Pop))
		ix.votes = append(ix.votes, uint32(max(0, t.Votes)))
		ix.years = append(ix.years, uint16(year))
		names := []string{t.Title}
		if t.Original != "" && t.Original != t.Title {
			names = append(names, t.Original)
		}
		b.add(names)
	}
	b.finish(ix)
	return ix
}

type measureQuery struct{ class, query, types string }

func measureCorpus(t *testing.T, path string) []measureQuery {
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []measureQuery
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, "|")
		if len(parts) < 3 {
			continue
		}
		out = append(out, measureQuery{strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), strings.TrimSpace(parts[2])})
	}
	return out
}

type pageItem struct {
	key    core.RatingKey
	tail   bool
	poster string
	title  measureTitle
}

func TestPosterMeasure(t *testing.T) {
	dir := os.Getenv("ETHER_POSTER_MEASURE")
	if dir == "" {
		t.Skip("ETHER_POSTER_MEASURE not set")
	}
	const limit = 10 // a search page: Plex hubs and Jellyfin hints show about ten per type
	// Library tier: K's Some library.
	var lib []measureTitle
	inLib := map[core.RatingKey]bool{}
	lf, err := os.Open(filepath.Join(dir, "library.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(lf)
	for dec.More() {
		var m measureTitle
		if err := dec.Decode(&m); err != nil {
			t.Fatal(err)
		}
		lib = append(lib, m)
		inLib[measureKey(m.Kind, m.ID)] = true
	}
	lf.Close()
	// Long tail: every other non-adult export id, original name only.
	var tail []measureTitle
	for _, src := range []struct{ kind, file string }{{"movie", "movie_ids_11_07.json.gz"}, {"tv", "tv_series_ids_11_07.json.gz"}} {
		f, err := os.Open(filepath.Join(dir, src.file))
		if err != nil {
			t.Fatal(err)
		}
		gz, err := gzip.NewReader(f)
		if err != nil {
			t.Fatal(err)
		}
		sc := bufio.NewScanner(gz)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			var r struct {
				Adult    bool    `json:"adult"`
				ID       int64   `json:"id"`
				Original string  `json:"original_title"`
				Name     string  `json:"original_name"`
				Pop      float64 `json:"popularity"`
			}
			if json.Unmarshal(sc.Bytes(), &r) != nil || r.Adult || inLib[measureKey(src.kind, r.ID)] {
				continue
			}
			name := r.Original
			if name == "" {
				name = r.Name
			}
			tail = append(tail, measureTitle{Kind: src.kind, ID: r.ID, Title: name, Pop: r.Pop})
		}
		gz.Close()
		f.Close()
	}
	byKey := map[core.RatingKey]measureTitle{}
	for _, m := range lib {
		byKey[measureKey(m.Kind, m.ID)] = m
	}
	for _, m := range tail {
		byKey[measureKey(m.Kind, m.ID)] = m
	}
	start := time.Now()
	libIx, tailIx := measureIndex(lib), measureIndex(tail)
	t.Logf("library %d titles, tail %d titles, indexes built in %v", len(lib), len(tail), time.Since(start))

	// The final page of every query: library and tail hits merged by score
	// (library wins ties), as the two-tier merge does before K's tuning.
	corpus := measureCorpus(t, filepath.Join(dir, "intents.txt"))
	pages := make([][]pageItem, len(corpus))
	for i, q := range corpus {
		wantMovie, wantShow := q.types == "" || strings.Contains(q.types, "m"), q.types == "" || strings.Contains(q.types, "tv")
		sq := parseSearchQuery(q.query)
		var items []pageItem
		var scores []float64
		for _, src := range []struct {
			ix   *titleIndex
			tail bool
		}{{libIx, false}, {tailIx, true}} {
			for _, h := range src.ix.search(sq, wantMovie, wantShow, limit) {
				k := core.RatingKey(src.ix.keys[h.title])
				items = append(items, pageItem{key: k, tail: src.tail, title: byKey[k], poster: byKey[k].Poster})
				scores = append(scores, h.score)
			}
		}
		order := make([]int, len(items))
		for n := range order {
			order[n] = n
		}
		sort.SliceStable(order, func(a, b int) bool { return scores[order[a]] > scores[order[b]] })
		for _, n := range order[:min(limit, len(order))] {
			pages[i] = append(pages[i], items[n])
		}
	}

	leanPath := filepath.Join(dir, "tail_lean.jsonl")
	lean := map[core.RatingKey]measureLean{}
	if f, err := os.Open(leanPath); err == nil {
		dec := json.NewDecoder(f)
		for dec.More() {
			var l measureLean
			if err := dec.Decode(&l); err != nil {
				t.Fatal(err)
			}
			lean[measureKey(l.Kind, l.ID)] = l
		}
		f.Close()
	}
	if len(lean) == 0 {
		var ids []string
		seen := map[core.RatingKey]bool{}
		for _, page := range pages {
			for _, it := range page {
				if it.tail && !seen[it.key] {
					seen[it.key] = true
					ids = append(ids, it.title.Kind+" "+strconv.FormatInt(it.title.ID, 10))
				}
			}
		}
		if err := os.WriteFile(filepath.Join(dir, "tail_ids.txt"), []byte(strings.Join(ids, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %d tail ids to record; record tail_lean.jsonl and run again", len(ids))
		return
	}

	// Before: what each page shows today without a poster. Library rows
	// carry their stored poster; a tail hit has no poster until something
	// resolves it.
	var shown, libNo, tailHits, tailNoTMDB int
	for _, page := range pages {
		for _, it := range page {
			shown++
			if it.tail {
				tailHits++
				if lean[it.key].Poster == "" {
					tailNoTMDB++
				}
			} else if it.poster == "" {
				libNo++
			}
		}
	}
	t.Logf("corpus %d queries, %d titles shown: library hits without a poster %d; tail hits %d (all without a stored poster), of which TMDB has none for %d", len(corpus), shown, libNo, tailHits, tailNoTMDB)

	// Replay: a fake TMDB answers each lean fetch with the recorded poster
	// after the recorded latency.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) != 2 {
			w.WriteHeader(404)
			return
		}
		id, _ := strconv.ParseInt(parts[1], 10, 64)
		kind := parts[0]
		l, ok := lean[measureKey(kind, id)]
		if !ok || l.Status != 200 {
			if ok {
				time.Sleep(time.Duration(l.MS * float64(time.Millisecond)))
			}
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"status_message":"not found"}`))
			return
		}
		time.Sleep(time.Duration(l.MS * float64(time.Millisecond)))
		poster := "null"
		if l.Poster != "" {
			poster = strconv.Quote(l.Poster)
		}
		nameField, dateField := "title", "release_date"
		if kind == "tv" {
			nameField, dateField = "name", "first_air_date"
		}
		fmt.Fprintf(w, `{"id":%d,%q:%q,%q:%q,"poster_path":%s,"popularity":0.5,"vote_count":3}`, id, nameField, l.Title, dateField, l.Date, poster)
	}))
	defer srv.Close()

	for _, run := range []struct {
		names bool
		wait  time.Duration
	}{{false, 0}, {false, posterWait}, {false, 100 * time.Millisecond}, {true, 0}, {true, posterWait}, {true, 100 * time.Millisecond}} {
		names := run.names
		db, err := store.OpenMemory(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		client := tmdb.New("measurekey", slog.Default(), tmdb.WithBaseURL(srv.URL), tmdb.WithCacheTTL(0))
		c, err := New(db, client, config.Catalog{Language: "en-US", Region: "US"}, slog.Default())
		if err != nil {
			t.Fatal(err)
		}
		// With the names file, a dated tail title's poster (or its absence)
		// is known from the monthly walk; only undated titles are looked up.
		unknown := func(tt *core.Title) bool {
			if !names {
				return true
			}
			return lean[tt.Key].Date == ""
		}
		known := func(it pageItem) string {
			if !it.tail {
				return it.poster
			}
			if names && lean[it.key].Date != "" {
				return lean[it.key].Poster
			}
			return ""
		}
		for _, pass := range []string{"cold", "warm"} {
			var added []time.Duration
			var lookups []int64
			var real, generated int
			for _, page := range pages {
				titles := make([]core.Title, len(page))
				for n, it := range page {
					typ := core.TypeMovie
					if it.title.Kind == "tv" {
						typ = core.TypeShow
					}
					titles[n] = core.Title{Key: it.key, Type: typ, Title: it.title.Title, PosterPath: known(it)}
				}
				// The TMDB requests this page starts: uncached titles with an
				// unknown poster, at most posterLookupsPerPage.
				var asks int64
				for n := range titles {
					tt := &titles[n]
					if _, cached := c.posters.get(tt.Key, c.now()); tt.PosterPath == "" && unknown(tt) && !cached && asks < posterLookupsPerPage {
						asks++
					}
				}
				if pass == "cold" {
					// Searches arrive at search-as-you-type pace.
					time.Sleep(300 * time.Millisecond)
				}
				t0 := time.Now()
				c.FillPosters(context.Background(), titles, unknown, run.wait)
				added = append(added, time.Since(t0))
				lookups = append(lookups, asks)
				for _, tt := range titles {
					if tt.PosterPath != "" {
						real++
					} else {
						generated++
					}
				}
			}
			sort.Slice(added, func(a, b int) bool { return added[a] < added[b] })
			sort.Slice(lookups, func(a, b int) bool { return lookups[a] < lookups[b] })
			var total int64
			for _, n := range lookups {
				total += n
			}
			t.Logf("names file %v, wait %v, %s: added latency p50 %v p95 %v max %v; TMDB requests per search mean %.2f p95 %d max %d; shown with a real poster %d, with Ether's generated poster %d, without any 0",
				names, run.wait, pass, added[len(added)/2].Round(time.Microsecond), added[len(added)*95/100].Round(time.Microsecond), added[len(added)-1].Round(time.Microsecond),
				float64(total)/float64(len(lookups)), lookups[len(lookups)*95/100], lookups[len(lookups)-1], real, generated)
			if pass == "cold" {
				// Let detached lookups land, as they would between searches.
				deadline := time.Now().Add(30 * time.Second)
				for c.posters.flights.Len() > 0 && time.Now().Before(deadline) {
					time.Sleep(50 * time.Millisecond)
				}
			}
		}
		client.Close()
		db.Close()
	}

	// Ether's generated poster: the cost of drawing one (no disk cache) at
	// the width Plex and Jellyfin grids ask for, for the tail hits TMDB has
	// no poster for.
	r := artwork.NewRenderer(nil, "", nil)
	var draws []time.Duration
	seen := map[core.RatingKey]bool{}
	for _, page := range pages {
		for _, it := range page {
			if !it.tail || seen[it.key] || lean[it.key].Poster != "" || len(draws) >= 300 {
				continue
			}
			seen[it.key] = true
			tt := core.Title{Key: it.key, Type: core.TypeMovie, Title: lean[it.key].Title, OriginalTitle: it.title.Title}
			if it.title.Kind == "tv" {
				tt.Type = core.TypeShow
			}
			p, _ := artwork.ForTitle(&tt)
			p.Width = 342
			t0 := time.Now()
			if _, _, err := r.Title(context.Background(), p); err != nil {
				t.Fatal(err)
			}
			draws = append(draws, time.Since(t0))
		}
	}
	sort.Slice(draws, func(a, b int) bool { return draws[a] < draws[b] })
	if len(draws) > 0 {
		t.Logf("generated poster at w342, %d titles, uncached: p50 %v p95 %v", len(draws), draws[len(draws)/2].Round(time.Microsecond), draws[len(draws)*95/100].Round(time.Microsecond))
	}
}
