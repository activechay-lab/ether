package catalog

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/tmdb"
)

// The gate's tail is built as a server builds it: every id in the export
// (library titles too, which the merge leaves to the library), with a names
// file. The gate's names file holds what the names walk would have read
// for the titles the corpus can reach: every library title and every title
// a recorded TMDB search returned (display title, date, poster). Every
// other tail title is known by its export (original) name alone.

// gateExports returns the newest movie and series export in dir.
func gateExports(t testing.TB, dir string) (movies, tv string) {
	t.Helper()
	pick := func(glob string) string {
		m, _ := filepath.Glob(filepath.Join(dir, glob))
		sort.Strings(m)
		if len(m) == 0 {
			t.Fatalf("search gate: no %s in %s", glob, dir)
		}
		return m[len(m)-1]
	}
	return pick("movie_ids_*.json.gz"), pick("tv_series_ids_*.json.gz")
}

// gateNames builds the names file from the library and the recordings.
func gateNames(t testing.TB, library map[core.RatingKey]core.Title, fake *gateTMDB) *tailNames {
	var b namesBuilder
	for _, ti := range library {
		b.addTitle(ti)
	}
	for key, rec := range fake.pages {
		path := key[:strings.Index(key, "|")]
		for _, raw := range rec.Results {
			var r tmdb.MultiResult
			if err := json.Unmarshal(raw, &r); err != nil {
				t.Fatal(err)
			}
			mt := r.MediaType
			if mt == "" {
				mt = map[string]string{"/search/movie": "movie", "/search/tv": "tv"}[path]
			}
			var ti core.Title
			switch mt {
			case "movie":
				ti = core.Title{Key: core.MovieKey(r.ID), Title: r.Title, Released: tmdb.ParseDate(r.ReleaseDate)}
			case "tv":
				ti = core.Title{Key: core.ShowKey(r.ID), Title: r.Name, Released: tmdb.ParseDate(r.FirstAirDate)}
			default:
				continue
			}
			if _, lib := library[ti.Key]; lib {
				continue
			}
			ti.PosterPath = r.PosterPath
			b.addTitle(ti)
		}
	}
	return b.build()
}

func gateTailFiles(t testing.TB) (movies, tv string, ok bool) {
	if dir := os.Getenv("ETHER_SEARCH_GATE_EXPORT"); dir != "" {
		m, s := gateExports(t, dir)
		return m, s, true
	}
	m, s := filepath.Join(searchGateDir, "export_sample_movie.jsonl.gz"), filepath.Join(searchGateDir, "export_sample_tv.jsonl.gz")
	if _, err := os.Stat(m); err != nil {
		return "", "", false
	}
	return m, s, true
}

func buildGateTail(t testing.TB, mp, tp string, names *tailNames) *tailIndex {
	t.Helper()
	mf, err := os.Open(mp)
	if err != nil {
		t.Fatal(err)
	}
	defer mf.Close()
	tf, err := os.Open(tp)
	if err != nil {
		t.Fatal(err)
	}
	defer tf.Close()
	ti, err := buildTailIndex(mf, tf, names, nil)
	if err != nil {
		t.Fatal(err)
	}
	return ti
}

// attachGateTail gives the gate's catalogue its long-tail index (the whole
// export with ETHER_SEARCH_GATE_EXPORT, else the committed sample) and
// returns the titles indexed. ETHER_SEARCH_GATE_NOTAIL=1 measures without it.
func attachGateTail(t testing.TB, g *searchGate) int {
	t.Helper()
	mp, tp, ok := gateTailFiles(t)
	if !ok || os.Getenv("ETHER_SEARCH_GATE_NOTAIL") != "" {
		return 0
	}
	ti := buildGateTail(t, mp, tp, gateNames(t, g.library, g.fake))
	g.c.setTailIndex(ti)
	return ti.len()
}

// TestSearchGateWriteExportSample writes the committed export sample from
// the whole export: every tail title that scores for any corpus query (as
// typed and without its year), so the sample holds the same ranking
// pressure as the 1.5M-title tail.
//
//	ETHER_SEARCH_GATE_EXPORT=<dir> ETHER_SEARCH_GATE_WRITE_SAMPLE=1 go test -run TestSearchGateWriteExportSample ./internal/catalog/
func TestSearchGateWriteExportSample(t *testing.T) {
	dir := os.Getenv("ETHER_SEARCH_GATE_EXPORT")
	if dir == "" || os.Getenv("ETHER_SEARCH_GATE_WRITE_SAMPLE") == "" {
		t.Skip("needs ETHER_SEARCH_GATE_EXPORT and ETHER_SEARCH_GATE_WRITE_SAMPLE")
	}
	library := map[core.RatingKey]core.Title{}
	for _, ti := range loadGateLibrary(t) {
		library[ti.Key] = ti
	}
	mp, tp := gateExports(t, dir)
	ti := buildGateTail(t, mp, tp, gateNames(t, library, loadGateTMDB(t)))
	keep := map[core.RatingKey]bool{}
	for _, q := range loadGateCorpus(t) {
		sq := parseSearchQuery(q.Query)
		qs := []searchQuery{sq}
		if sq.yearless != nil {
			qs = append(qs, *sq.yearless)
		}
		for _, x := range qs {
			for _, h := range ti.ix.search(x, true, true, searchMaxCandidates) {
				keep[core.RatingKey(ti.ix.keys[h.title])] = true
			}
		}
	}
	write := func(src, dst string, key func(int64) core.RatingKey) int {
		in, err := os.Open(src)
		if err != nil {
			t.Fatal(err)
		}
		defer in.Close()
		zr, err := gzip.NewReader(in)
		if err != nil {
			t.Fatal(err)
		}
		out, err := os.Create(dst)
		if err != nil {
			t.Fatal(err)
		}
		zw, _ := gzip.NewWriterLevel(out, gzip.BestCompression)
		sc := bufio.NewScanner(zr)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		n := 0
		for sc.Scan() {
			var e struct {
				ID int64 `json:"id"`
			}
			if json.Unmarshal(sc.Bytes(), &e) != nil || !keep[key(e.ID)] {
				continue
			}
			zw.Write(sc.Bytes())
			zw.Write([]byte{'\n'})
			n++
		}
		if err := sc.Err(); err != nil && err != io.EOF {
			t.Fatal(err)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		if err := out.Close(); err != nil {
			t.Fatal(err)
		}
		return n
	}
	nm := write(mp, filepath.Join(searchGateDir, "export_sample_movie.jsonl.gz"), core.MovieKey)
	nt := write(tp, filepath.Join(searchGateDir, "export_sample_tv.jsonl.gz"), core.ShowKey)
	t.Logf("export sample: %d movies, %d series of a %d-title tail", nm, nt, ti.len())
}
