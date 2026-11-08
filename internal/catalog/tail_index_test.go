package catalog

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/tmdb"
)

func gzLines(t *testing.T, lines ...string) *bytes.Buffer {
	t.Helper()
	var b bytes.Buffer
	zw := gzip.NewWriter(&b)
	for _, l := range lines {
		fmt.Fprintln(zw, l)
	}
	zw.Close()
	return &b
}

func testTail(t *testing.T) *tailIndex {
	t.Helper()
	movies := gzLines(t,
		`{"adult":false,"id":78,"original_title":"Blade Runner","popularity":17.4,"video":false}`,
		`{"adult":false,"id":335984,"original_title":"Blade Runner 2049","popularity":36.1,"video":false}`,
		`{"adult":false,"id":8773,"original_title":"L'Amour à vingt ans","popularity":1.1,"video":false}`,
		`{"adult":false,"id":999,"original_title":"Concert Film","popularity":0.5,"video":true}`,
	)
	tv := gzLines(t, `{"id":2,"original_name":"Clerks","popularity":7.06}`)
	b := &namesBuilder{}
	b.add(core.MovieKey(8773), "Love at Twenty", time.Date(1962, 6, 22, 0, 0, 0, 0, time.UTC), "/love.jpg", 0)
	b.add(core.MovieKey(78), "Blade Runner", time.Date(1982, 6, 25, 0, 0, 0, 0, time.UTC), "", tailPosterNone)
	ix, err := buildTailIndex(movies, tv, b.build(), nil)
	if err != nil {
		t.Fatal(err)
	}
	ix.day = time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)
	return ix
}

// A tail index survives its file: written, mapped and read back, it
// answers the same lookups and searches; a damaged file is rejected.
func TestTailIndexFileRoundTrip(t *testing.T) {
	built := testTail(t)
	dir := t.TempDir()
	path, err := writeTailFile(dir, built, 1)
	if err != nil {
		t.Fatal(err)
	}
	mapped, unmap, err := openTailFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer unmap()
	for _, ix := range []*tailIndex{built, mapped} {
		i, ok := ix.find(core.MovieKey(8773))
		if !ok {
			t.Fatal("8773 not found")
		}
		e := ix.entry(i)
		if e.Title.Title != "Love at Twenty" || e.Title.OriginalTitle != "L'Amour à vingt ans" || e.Title.Year != 1962 || e.Title.PosterPath != "/love.jpg" || e.DateUnknown {
			t.Fatalf("entry %+v", e)
		}
		if i, _ := ix.find(core.MovieKey(78)); !ix.entry(i).PosterNone {
			t.Fatal("poster-none flag lost")
		}
		if i, _ := ix.find(core.ShowKey(2)); !ix.entry(i).DateUnknown || ix.entry(i).Title.Type != core.TypeShow {
			t.Fatalf("show entry %+v", ix.entry(i))
		}
		if i, _ := ix.find(core.MovieKey(999)); !ix.entry(i).Video {
			t.Fatal("video flag lost")
		}
		hits := ix.ix.search(parseSearchQuery("blade run"), true, true, 10)
		if len(hits) != 2 || ix.ix.keys[hits[0].title] != int64(core.MovieKey(335984)) {
			t.Fatalf("search %v", hits)
		}
		if hits := ix.ix.search(parseSearchQuery("love at twenty"), true, true, 10); len(hits) == 0 || ix.ix.keys[hits[0].title] != int64(core.MovieKey(8773)) {
			t.Fatalf("localised title search %v", hits)
		}
	}
	data, _ := os.ReadFile(path)
	data[len(data)-3] ^= 0xff
	bad := filepath.Join(dir, "tail-2026-11-07-002.idx")
	os.WriteFile(bad, data, 0o644)
	if _, _, err := openTailFile(bad); err == nil {
		t.Fatal("damaged file accepted")
	}
}

// Search finds a long-tail title the library does not hold without
// storing it, and opening it promotes it into the library.
func TestTailSearchAndPromotion(t *testing.T) {
	ctx := context.Background()
	u := newUniverse()
	c := universeCatalog(t, u, config.Catalog{MinPopularity: 5, MinVotes: 100}, false)
	tail := &tailBuilder{day: time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)}
	names := &namesBuilder{}
	for _, m := range u.movies {
		tail.add(false, tmdb.ExportRow{ID: m.id, Popularity: m.pop}, fmt.Sprintf("Movie %d", m.id))
		if m.date != "" {
			names.add(core.MovieKey(m.id), fmt.Sprintf("Movie %d", m.id), tmdb.ParseDate(m.date), "", 0)
		}
	}
	c.setTailIndex(tail.build(names.build()))
	// An old, obscure, dated title no bar admits.
	var m uMovie
	for _, x := range u.movies {
		if x.pop < 5 && x.votes < 100 && x.date != "" && x.date < "1990" && !x.video && !x.adult {
			m = x
			break
		}
	}
	key := core.MovieKey(m.id)
	res, err := c.Search(ctx, fmt.Sprintf("Movie %d", m.id), []core.MediaType{core.TypeMovie}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) == 0 || res[0].Key != key {
		t.Fatalf("search: %+v", res)
	}
	if ok, _ := c.Findable(ctx, key); !ok {
		t.Fatal("not findable")
	}
	if _, err := c.loadByKey(ctx, key); err == nil {
		t.Fatal("search stored a long-tail row")
	}
	got, err := c.Get(ctx, key)
	if err != nil || got.Key != key {
		t.Fatalf("open: %v %v", got, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if st, err := c.loadByKey(ctx, key); err == nil && st.detail {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("opened tail title not promoted")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// The lean builder the tail uses builds the same index as the library's
// builder: every array is identical (surface spellings aside, which no
// search reads).
func TestLeanTitleIndexMatchesBuilder(t *testing.T) {
	names := [][]string{{"The Office"}, {"Spider-Man: No Way Home", "Homem-Aranha"}, {"Amélie", "Le Fabuleux Destin d'Amélie Poulain"},
		{"Up"}, {"S.W.A.T."}, {"The The"}, {"Don't Look Up"}, {"1917"}, {"Office Space"}}
	a, b := &titleIndex{}, &titleIndex{}
	tb := newTitleIndexBuilder()
	for _, n := range names {
		tb.add(n)
	}
	tb.finish(a)
	buildLeanTitleIndex(b, len(names), func(i int) []string { return names[i] })
	a.lx.surface, b.lx.surface = nil, nil
	if fmt.Sprint(a.lx) != fmt.Sprint(b.lx) {
		t.Fatalf("lexicon differs:\n%v\n%v", a.lx, b.lx)
	}
	a.lx, b.lx = nil, nil
	if fmt.Sprintf("%+v", *a) != fmt.Sprintf("%+v", *b) {
		t.Fatalf("index differs:\n%+v\n%+v", *a, *b)
	}
}

// A node builds, maps and swaps its tail index: a new version replaces the
// old one, and the old file is deleted only after its last reader is done.
func TestTailBuildAndSwap(t *testing.T) {
	ctx := context.Background()
	u := newUniverse()
	dir := t.TempDir()
	c := universeCatalog(t, u, config.Catalog{}, true)
	c.tail.dir = dir
	if st := c.TailStatus(); st.Ready {
		t.Fatal("ready before a build")
	}
	if err := c.buildTail(ctx); err != nil {
		t.Fatal(err)
	}
	st := c.TailStatus()
	if !st.Ready || st.Titles != len(u.movies)-1 { // the adult title is not in the plain export
		t.Fatalf("status %+v", st)
	}
	first := tailFiles(dir)
	if len(first) != 1 {
		t.Fatalf("files %v", first)
	}
	old, release := c.acquireTail() // a search still reading the first version
	if err := c.buildTail(ctx); err != nil {
		t.Fatal(err)
	}
	if files := tailFiles(dir); len(files) != 2 {
		t.Fatalf("old version deleted under a reader: %v", files)
	}
	if _, ok := old.find(core.MovieKey(1)); !ok {
		t.Fatal("old version unreadable while held")
	}
	release()
	if files := tailFiles(dir); len(files) != 1 || files[0] == first[0] {
		t.Fatalf("after release: %v", files)
	}
	// A restart maps the newest valid file.
	c2 := universeCatalog(t, u, config.Catalog{}, true)
	c2.tail.dir = dir
	c2.loadNewestTail()
	if !c2.TailStatus().Ready {
		t.Fatal("restart did not load the index")
	}
	c2.installTail(nil)
	c.installTail(nil)
}
