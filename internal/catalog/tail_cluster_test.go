package catalog

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/tmdb"
)

// liveSearchTMDB answers TMDB's live movie search with one title, or fails.
type liveSearchTMDB struct {
	*universeTMDB
	mu   sync.Mutex
	hit  *tmdb.Movie
	down bool
}

func (l *liveSearchTMDB) SearchMovies(context.Context, string, int) (*tmdb.Paged[tmdb.Movie], error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.down {
		return nil, errors.New("tmdb unavailable")
	}
	if l.hit == nil {
		return &tmdb.Paged[tmdb.Movie]{}, nil
	}
	return &tmdb.Paged[tmdb.Movie]{Results: []tmdb.Movie{*l.hit}, Page: 1, TotalPages: 1}, nil
}
func (l *liveSearchTMDB) set(hit *tmdb.Movie, down bool) {
	l.mu.Lock()
	l.hit, l.down = hit, down
	l.mu.Unlock()
}

func tailDirFiles(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}

func searchKeys(t *testing.T, c *Catalog, q string) map[core.RatingKey]bool {
	t.Helper()
	res, err := c.Search(context.Background(), q, []core.MediaType{core.TypeMovie}, 10)
	if err != nil {
		t.Fatalf("search %q errored: %v", q, err)
	}
	out := map[core.RatingKey]bool{}
	for _, r := range res {
		out[r.Key] = true
	}
	return out
}

// Cluster tier: two nodes share one database (PostgreSQL in the cluster
// matrix), and each keeps its own long-tail index in its own directory.
// A node with no index yet (first boot, emptyDir, a recreated PVC) answers
// search from the library index plus TMDB's live search with no errors,
// including when TMDB is down; it never reads or writes another node's
// index; and it becomes ready from its own build.
func TestClusterNodeWithoutTailIndexFallsBack(t *testing.T) {
	ctx := context.Background()
	u := newUniverse()
	live := &liveSearchTMDB{universeTMDB: u}
	db := store.OpenTest(t)
	now := WithNow(func() time.Time { return time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC) })
	dirA, dirB := t.TempDir(), t.TempDir()
	node := func(dir string) *Catalog {
		c, err := New(db, live, config.Catalog{MinPopularity: 5, MinVotes: 100}, nil, WithExports(u), now, WithTailIndex(dir, nil))
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	a, b := node(dirA), node(dirB)
	// An obscure dated title: never a library row, so only the tail or
	// TMDB's live search can find it.
	var m uMovie
	for _, x := range u.movies {
		if x.pop < 5 && x.votes < 100 && x.date != "" && !x.video && !x.adult {
			m = x
			break
		}
	}
	key, name := core.MovieKey(m.id), fmt.Sprintf("Movie %d", m.id)
	for _, c := range []*Catalog{a, b} {
		if st := c.TailStatus(); st.Ready || st.LastError != "" {
			t.Fatalf("fresh node status %+v", st)
		}
	}

	// No index: TMDB's live search answers.
	live.set(&tmdb.Movie{ID: m.id, Title: name, ReleaseDate: m.date, Popularity: m.pop}, false)
	if !searchKeys(t, b, name)[key] {
		t.Fatal("node without an index did not fall back to TMDB live search")
	}
	// No index and TMDB down: an empty answer, never an error.
	live.set(nil, true)
	searchKeys(t, b, "Movie no-such-title")

	// Node A builds its own index; it now finds the title with TMDB down.
	if err := a.buildTail(ctx); err != nil {
		t.Fatal(err)
	}
	if st := a.TailStatus(); !st.Ready || st.LastError != "" {
		t.Fatalf("node A after build %+v", st)
	}
	if !searchKeys(t, a, name)[key] {
		t.Fatal("node A's index did not find the long-tail title")
	}
	// Node B is untouched by A's build: still no index, nothing in its
	// directory, and its search still answers without error.
	if st := b.TailStatus(); st.Ready || tailDirFiles(t, dirB) != 0 {
		t.Fatalf("node B saw node A's index: %+v, %d files", st, tailDirFiles(t, dirB))
	}
	searchKeys(t, b, name)

	// Opening the title on node B with no index still works (detail fetch).
	live.set(nil, false)
	got, err := b.Get(ctx, key)
	if err != nil || got.Key != key {
		t.Fatalf("open on node without an index: %v %v", got, err)
	}

	// Node B's own build makes it ready from its own directory.
	if err := b.buildTail(ctx); err != nil {
		t.Fatal(err)
	}
	if st := b.TailStatus(); !st.Ready || tailDirFiles(t, dirB) == 0 {
		t.Fatalf("node B after its build %+v", st)
	}
	a.installTail(nil)
	b.installTail(nil)
}
